package service

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kova/config"
	"kova/internal/types"
)

const (
	colabAudioSeparationPath = "/audio/separations"
	maxSeparatedStemBytes    = int64(2 * 1024 * 1024 * 1024)
)

// sourceWorkflowNeedsAudioSeparation is deliberately independent of the
// platform-caption code.  When the source is transcribed, KOVA must send a
// music-free vocal stem to STT rather than letting background music distort
// timestamps and words.
func sourceWorkflowNeedsAudioSeparation(method string) bool {
	method = normalizeWorkflowSourceMethod(method)
	return method == sourceMethodSpeechToText || method == sourceMethodSpeechToTextAndOCR
}

// separateSourceAudioForSTT requests the two-stem Demucs operation from the
// authenticated CUDA STT Colab worker.  It keeps the original download
// untouched, writes only task-local stems, and selects vocals as AudioFilePath
// for the later transcription pass.  CPU fallback is intentionally refused:
// a fake or slow local split would leave music in the voice track and defeat
// the point of this stage.
func (s Service) separateSourceAudioForSTT(ctx context.Context, step *types.SubtitleTaskStepParam) error {
	if step == nil {
		return errors.New("audio separation requires workflow parameters")
	}
	input := strings.TrimSpace(step.SourceAudioFilePath)
	if input == "" {
		input = strings.TrimSpace(step.AudioFilePath)
	}
	if input == "" {
		return errors.New("source audio is unavailable for voice/music separation")
	}
	if info, err := os.Stat(input); err != nil || info.IsDir() || info.Size() == 0 {
		if err == nil {
			err = errors.New("source audio is empty")
		}
		return fmt.Errorf("read source audio before separation: %w", err)
	}
	if !config.Conf.Transcribe.RemoteAudioSeparation {
		return errors.New("voice/music separation requires the KOVA STT Google Colab GPU worker; open the current KOVA STT notebook, Run all, then select Google Colab STT before starting the source step")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.Conf.Transcribe.Openai.BaseUrl), "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	token := strings.TrimSpace(config.ResolveTranscriptionAPIKey())
	if baseURL == "" || token == "" {
		return errors.New("the KOVA STT Google Colab URL/token is required before separating voice and music")
	}

	vocals := filepath.Join(step.TaskBasePath, types.SubtitleTaskVocalAudioFileName)
	background := filepath.Join(step.TaskBasePath, types.SubtitleTaskBackgroundAudioFileName)
	reportSourceProgress(step, "separate_audio", 0, "Uploading source audio to the CUDA voice/music separator")
	if err := requestColabAudioSeparation(ctx, baseURL+"/v1"+colabAudioSeparationPath, token, input, vocals, background, func(percent uint8, detail string) {
		reportSourceProgress(step, "separate_audio", percent, detail)
	}); err != nil {
		return err
	}
	step.SourceAudioFilePath = input
	step.VocalAudioFilePath = vocals
	step.BackgroundAudioFilePath = background
	step.AudioFilePath = vocals
	reportSourceProgress(step, "separate_audio", 100, "Vocal and background stems are ready; STT will use vocals only")
	return nil
}

func requestColabAudioSeparation(ctx context.Context, endpoint, token, source, vocals, background string, progress func(uint8, string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source audio for separation: %w", err)
	}
	defer input.Close()

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	writeDone := make(chan error, 1)
	go func() {
		defer writer.Close()
		defer multipartWriter.Close()
		if err := multipartWriter.WriteField("stems", "vocals,no_vocals"); err != nil {
			writeDone <- err
			return
		}
		part, err := multipartWriter.CreateFormFile("file", filepath.Base(source))
		if err != nil {
			writeDone <- err
			return
		}
		_, err = io.Copy(part, input)
		writeDone <- err
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if progress != nil {
		progress(15, "CUDA separator is receiving the source audio")
	}
	client := &http.Client{Timeout: 20 * time.Minute}
	response, err := client.Do(req)
	writeErr := <-writeDone
	if writeErr != nil {
		return fmt.Errorf("upload source audio to CUDA separator: %w", writeErr)
	}
	if err != nil {
		return fmt.Errorf("contact CUDA voice/music separator: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		if response.StatusCode == http.StatusNotFound {
			return errors.New("the connected STT Colab worker is older and has no audio-separation endpoint; reopen KOVA_STT_GPU.ipynb from this KOVA release and Run all")
		}
		return fmt.Errorf("CUDA voice/music separator returned %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	if progress != nil {
		progress(85, "CUDA separator finished; saving vocal and background stems")
	}
	temporary, err := os.CreateTemp(filepath.Dir(vocals), "kova-stems-*.zip")
	if err != nil {
		return fmt.Errorf("create stem archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	written, copyErr := io.Copy(temporary, io.LimitReader(response.Body, maxSeparatedStemBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("download separated stems: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("finalize separated stems: %w", closeErr)
	}
	if written == 0 || written > maxSeparatedStemBytes {
		return errors.New("CUDA separator returned an empty or oversized stem archive")
	}
	if err := extractSeparatedStemArchive(temporaryPath, vocals, background); err != nil {
		return err
	}
	return nil
}

func extractSeparatedStemArchive(archivePath, vocals, background string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open CUDA stem archive: %w", err)
	}
	defer archive.Close()
	written := map[string]bool{}
	for _, file := range archive.File {
		name := strings.ToLower(filepath.Base(file.Name))
		var destination string
		switch name {
		case "vocals.wav":
			destination = vocals
		case "no_vocals.wav", "background.wav":
			destination = background
		default:
			continue
		}
		if file.UncompressedSize64 == 0 || file.UncompressedSize64 > uint64(maxSeparatedStemBytes) {
			return fmt.Errorf("invalid %s stem in CUDA archive", name)
		}
		if err := writeStemArchiveFile(file, destination); err != nil {
			return err
		}
		written[destination] = true
	}
	if !written[vocals] || !written[background] {
		return errors.New("CUDA separator archive is missing vocals.wav or no_vocals.wav")
	}
	return nil
}

func writeStemArchiveFile(file *zip.File, destination string) error {
	input, err := file.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	temporaryPath := destination + ".tmp"
	output, err := os.Create(temporaryPath)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxSeparatedStemBytes+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || written == 0 || written > maxSeparatedStemBytes {
		_ = os.Remove(temporaryPath)
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return fmt.Errorf("invalid extracted stem %s", filepath.Base(destination))
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporaryPath)
		return err
	}
	return os.Rename(temporaryPath, destination)
}
