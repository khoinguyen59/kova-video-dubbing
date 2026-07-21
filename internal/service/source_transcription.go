package service

import (
	"context"
	"errors"
	"fmt"
	"kova/config"
	"kova/internal/deps"
	"kova/internal/types"
	"kova/pkg/util"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Five-minute compressed MP3 chunks stay well below the common upload
	// limits of OpenAI-compatible speech-to-text APIs while retaining enough
	// context for natural subtitle timing.
	sourceTranscriptionChunkSeconds = 5 * 60
	sourceSubtitleMaxDuration       = 5.5
	sourceSubtitleMaxCharacters     = 84
)

// transcribeSourceForReview is deliberately source-only. It downloads no
// platform captions and never invokes translation; the generated original SRT
// is the checkpoint the user must review before any translation stage exists.
func (s Service) transcribeSourceForReview(ctx context.Context, workflow *subtitleWorkflow, task *types.SubtitleTask, step *types.SubtitleTaskStepParam) error {
	if s.Transcriber == nil {
		return errors.New("speech-to-text chưa được khởi tạo; hãy kiểm tra Faster-Whisper và model cục bộ trong Cài đặt")
	}
	if step == nil || strings.TrimSpace(step.AudioFilePath) == "" {
		return errors.New("không tìm thấy audio nguồn để speech-to-text")
	}
	remoteColabSTT := strings.EqualFold(strings.TrimSpace(config.Conf.Transcribe.Provider), "openai") && strings.TrimSpace(config.Conf.Transcribe.Openai.SessionAPIKey) != ""
	if remoteColabSTT {
		reportSourceProgress(step, "speech_to_text", 0, "Connecting to the Google Colab CUDA transcription worker")
	} else {
		reportSourceProgress(step, "speech_to_text", 0, "Preparing local Faster-Whisper model and timed transcription")
	}
	if !remoteColabSTT {
		if err := deps.CheckLocalTranscriptionDependency(); err != nil {
			return fmt.Errorf("không thể chuẩn bị speech-to-text cục bộ: %w", err)
		}
	}

	points, err := GetSplitPoints(step.AudioFilePath, sourceTranscriptionChunkSeconds)
	if err != nil {
		return fmt.Errorf("không thể chia audio nguồn cho speech-to-text: %w", err)
	}
	if len(points) < 2 {
		return errors.New("audio nguồn không có thời lượng hợp lệ để speech-to-text")
	}

	allBlocks := make([]*util.SrtBlock, 0)
	detectedLanguage := ""
	segmentCount := len(points) - 1
	for index := 0; index < segmentCount; index++ {
		chunkPath := filepath.Join(step.TaskBasePath, fmt.Sprintf("source_stt_%03d.mp3", index))
		if err := ClipAudio(step.AudioFilePath, chunkPath, points[index], points[index+1]); err != nil {
			return fmt.Errorf("không thể cắt audio cho đoạn STT %d: %w", index+1, err)
		}
		data, err := s.transcribeAudio(index, chunkPath, string(step.OriginLanguage), step.TaskBasePath)
		if err != nil {
			return sourceTranscriptionError(index+1, segmentCount, err)
		}
		if detectedLanguage == "" {
			detectedLanguage = normalizeDetectedLanguage(data.Language)
		}
		blocks, err := sourceSRTBlocksFromTranscription(data, points[index])
		if err != nil {
			return fmt.Errorf("speech-to-text đoạn %d/%d không trả timestamp dùng được: %w", index+1, segmentCount, err)
		}
		allBlocks = append(allBlocks, blocks...)
		reportSourceProgress(step, "speech_to_text", uint8(((index+1)*100)/segmentCount), fmt.Sprintf("Transcribing segment %d/%d", index+1, segmentCount))
		// Source download owns 0-10%; cloud ASR owns 10-32%; 35% means the
		// reviewable SRT has been written and awaits the user's approval.
		task.ProcessPct = uint8(10 + ((index+1)*22)/segmentCount)
	}
	if len(allBlocks) == 0 {
		return errors.New("speech-to-text không tạo được câu phụ đề nào từ audio nguồn")
	}
	for index, block := range allBlocks {
		block.Index = index + 1
	}

	reportSourceProgress(step, "source_srt", 0, "Writing review SRT")
	srtPath := filepath.Join(step.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	if err := writeSourceSRT(srtPath, allBlocks); err != nil {
		return err
	}
	if err := writeWorkflowText(filepath.Join(step.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), allBlocks, false); err != nil {
		return err
	}
	reportSourceProgress(step, "source_srt", 100, "Review SRT ready")
	if workflow != nil && strings.EqualFold(strings.TrimSpace(workflow.OriginLanguage), "auto") && detectedLanguage != "" {
		workflow.mu.Lock()
		workflow.OriginLanguage = detectedLanguage
		workflow.mu.Unlock()
		step.OriginLanguage = types.StandardLanguageCode(detectedLanguage)
		task.OriginLanguage = detectedLanguage
	}
	return nil
}

// sourceTranscriptionError preserves the provider's original detail while
// making the common API Gateway configuration failure actionable. KOVA can
// send a valid gateway key, but only the gateway owner can attach the
// upstream STT provider credential.
func sourceTranscriptionError(index, total int, err error) error {
	if err == nil {
		return nil
	}
	detail := err.Error()
	if strings.EqualFold(strings.TrimSpace(config.Conf.Transcribe.Provider), "openai") && strings.TrimSpace(config.Conf.Transcribe.Openai.SessionAPIKey) != "" {
		return fmt.Errorf("speech-to-text đoạn %d/%d không thể chạy trên worker Google Colab. Kiểm tra URL/token STT, giữ notebook đang chạy GPU rồi thử lại: %w", index, total, err)
	}
	if strings.Contains(strings.ToLower(detail), "no credentials for provider: openai") {
		return fmt.Errorf("speech-to-text đoạn %d/%d không thể chạy: API Gateway chưa có credential upstream cho provider OpenAI. Hãy thêm credential STT/OpenAI tại API Gateway, hoặc chọn endpoint OpenAI-compatible hỗ trợ /v1/audio/transcriptions: %w", index, total, err)
	}
	return fmt.Errorf("speech-to-text đoạn %d/%d thất bại: %w", index, total, err)
}

func normalizeDetectedLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "zh-cn" || language == "zh_cn" {
		return "zh"
	}
	if len(language) == 2 {
		return language
	}
	return ""
}

// sourceSRTBlocksFromTranscription converts timestamped ASR output directly
// into original-language subtitle cues.  It first uses word timestamps for
// readable cue grouping and falls back to provider segment timestamps.
func sourceSRTBlocksFromTranscription(data *types.TranscriptionData, offset float64) ([]*util.SrtBlock, error) {
	if data == nil {
		return nil, errors.New("STT trả về dữ liệu trống")
	}
	blocks := make([]*util.SrtBlock, 0)
	var words []types.Word
	for _, word := range data.Words {
		word.Text = strings.TrimSpace(word.Text)
		if word.Text == "" || word.Start < 0 || word.End <= word.Start {
			continue
		}
		words = append(words, word)
	}
	if len(words) > 0 {
		start := words[0].Start
		end := words[0].End
		parts := make([]string, 0, 16)
		flush := func() {
			text := strings.TrimSpace(strings.Join(parts, " "))
			if text == "" {
				return
			}
			if end <= start {
				end = start + 0.4
			}
			blocks = append(blocks, &util.SrtBlock{
				Index:                  len(blocks) + 1,
				Timestamp:              util.ConvertTimes(float32(start+offset), float32(end+offset)),
				OriginLanguageSentence: text,
			})
			parts = parts[:0]
		}
		for _, word := range words {
			if len(parts) == 0 {
				start = word.Start
			}
			parts = append(parts, word.Text)
			end = word.End
			text := strings.Join(parts, " ")
			endsSentence := strings.HasSuffix(word.Text, ".") || strings.HasSuffix(word.Text, "!") || strings.HasSuffix(word.Text, "?") || strings.HasSuffix(word.Text, "…")
			if endsSentence || end-start >= sourceSubtitleMaxDuration || len([]rune(text)) >= sourceSubtitleMaxCharacters {
				flush()
			}
		}
		flush()
		return blocks, nil
	}

	for _, segment := range data.Segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" || segment.Start < 0 || segment.End <= segment.Start {
			continue
		}
		blocks = append(blocks, &util.SrtBlock{
			Index:                  len(blocks) + 1,
			Timestamp:              util.ConvertTimes(float32(segment.Start+offset), float32(segment.End+offset)),
			OriginLanguageSentence: text,
		})
	}
	if len(blocks) == 0 {
		return nil, errors.New("API STT không trả word hoặc segment timestamp; chọn provider hỗ trợ verbose_json timestamps")
	}
	return blocks, nil
}

func writeSourceSRT(path string, blocks []*util.SrtBlock) error {
	if len(blocks) == 0 {
		return errors.New("không thể ghi SRT gốc rỗng")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("không thể tạo thư mục SRT gốc: %w", err)
	}
	var output strings.Builder
	for index, block := range blocks {
		if block == nil || strings.TrimSpace(block.Timestamp) == "" || strings.TrimSpace(block.OriginLanguageSentence) == "" {
			continue
		}
		output.WriteString(fmt.Sprintf("%d\n%s\n%s\n\n", index+1, block.Timestamp, strings.TrimSpace(block.OriginLanguageSentence)))
	}
	if strings.TrimSpace(output.String()) == "" {
		return errors.New("STT không tạo cue SRT hợp lệ")
	}
	if err := os.WriteFile(path, []byte(output.String()), 0644); err != nil {
		return fmt.Errorf("không thể ghi SRT gốc: %w", err)
	}
	if _, err := workflowSRTBlocks(path); err != nil {
		return fmt.Errorf("SRT gốc tạo từ STT không hợp lệ: %w", err)
	}
	return nil
}
