package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceWorkflowNeedsAudioSeparationOnlyForSTTMethods(t *testing.T) {
	if !sourceWorkflowNeedsAudioSeparation(sourceMethodSpeechToText) || !sourceWorkflowNeedsAudioSeparation(sourceMethodSpeechToTextAndOCR) {
		t.Fatal("STT source methods must separate vocals before transcription")
	}
	if sourceWorkflowNeedsAudioSeparation(sourceMethodVisualOCR) {
		t.Fatal("OCR-only source does not submit an audio stem to STT")
	}
}

func TestSTTColabNotebookUsesAsynchronousSeparationJobs(t *testing.T) {
	notebook, err := os.ReadFile(filepath.Join("..", "..", "notebooks", "KOVA_STT_GPU.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(notebook)
	for _, required := range []string{
		"background_tasks.add_task(run_separation_job",
		"JSONResponse(status_code=202",
		"@app.get('/v1/audio/separations/{job_id}')",
		"status='ready', progress=100",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("STT Colab notebook is missing async separation contract %q", required)
		}
	}
}

func TestExtractSeparatedStemArchiveRequiresVocalsAndBackground(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "stems.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, body := range map[string]string{"nested/vocals.wav": "clean-voice", "nested/no_vocals.wav": "music-only"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	vocals := filepath.Join(dir, "source_vocals.wav")
	background := filepath.Join(dir, "source_background.wav")
	if err := extractSeparatedStemArchive(archivePath, vocals, background); err != nil {
		t.Fatalf("extractSeparatedStemArchive() error = %v", err)
	}
	if got, _ := os.ReadFile(vocals); string(got) != "clean-voice" {
		t.Fatalf("vocals = %q", got)
	}
	if got, _ := os.ReadFile(background); string(got) != "music-only" {
		t.Fatalf("background = %q", got)
	}
}

func TestRequestColabAudioSeparationPollsAsynchronousJob(t *testing.T) {
	archive := separatedStemArchiveForTest(t)
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/audio/separations":
			if err := request.ParseMultipartForm(2 << 20); err != nil {
				t.Errorf("ParseMultipartForm() error = %v", err)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(writer).Encode(colabSeparationJob{JobID: "job-1", Status: "queued", Progress: 15})
		case request.Method == http.MethodGet && request.URL.Path == "/v1/audio/separations/job-1":
			if polls.Add(1) == 1 {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(writer).Encode(colabSeparationJob{JobID: "job-1", Status: "running", Progress: 42})
				return
			}
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(archive)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldInterval := separationPollInterval
	separationPollInterval = 5 * time.Millisecond
	defer func() { separationPollInterval = oldInterval }()

	directory := t.TempDir()
	source := filepath.Join(directory, "source.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	vocals := filepath.Join(directory, "vocals.wav")
	background := filepath.Join(directory, "background.wav")
	var progressValues []uint8
	err := requestColabAudioSeparation(context.Background(), server.URL+"/v1/audio/separations", "test-token", source, vocals, background, func(percent uint8, _ string) {
		progressValues = append(progressValues, percent)
	})
	if err != nil {
		t.Fatalf("requestColabAudioSeparation() error = %v", err)
	}
	if polls.Load() < 2 {
		t.Fatalf("poll count = %d, want at least 2", polls.Load())
	}
	if got, _ := os.ReadFile(vocals); string(got) != "clean-voice" {
		t.Fatalf("vocals = %q", got)
	}
	if got, _ := os.ReadFile(background); string(got) != "music-only" {
		t.Fatalf("background = %q", got)
	}
	if len(progressValues) < 4 || progressValues[len(progressValues)-1] != 95 {
		t.Fatalf("progress updates = %v, want queued, running and download phases", progressValues)
	}
}

func separatedStemArchiveForTest(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{"vocals.wav": "clean-voice", "no_vocals.wav": "music-only"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
