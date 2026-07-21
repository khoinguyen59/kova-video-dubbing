package service

import (
	"context"
	"errors"
	"fmt"
	"kova/config"
	"kova/internal/dto"
	"kova/internal/types"
	"kova/internal/visualocr"
	"path/filepath"
	"strings"
)

const visualOCRMergeGapMS = 450

// normalizeWorkflowOCRRequest makes Visual OCR a first-class source branch.
// It uses the lower subtitle band by default, while accepting an explicit ROI
// from the desktop for videos whose hard captions appear elsewhere.
func normalizeWorkflowOCRRequest(req dto.StartVideoSubtitleTaskReq, sourceMethod string) (string, visualocr.Region, int, bool, error) {
	if sourceMethod != sourceMethodVisualOCR {
		return "", visualocr.Region{}, 0, false, nil
	}
	language := strings.ToLower(strings.TrimSpace(req.OCRLanguage))
	if language == "" {
		language = "en"
	}
	if len(language) > 24 {
		return "", visualocr.Region{}, 0, false, errors.New("ngôn ngữ OCR không hợp lệ")
	}
	region := visualocr.Region{X: 0.10, Y: 0.70, Width: 0.80, Height: 0.20}
	if req.OCRRegionWidth != 0 || req.OCRRegionHeight != 0 {
		region = visualocr.Region{X: req.OCRRegionX, Y: req.OCRRegionY, Width: req.OCRRegionWidth, Height: req.OCRRegionHeight}
	}
	if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 || region.X+region.Width > 1 || region.Y+region.Height > 1 {
		return "", visualocr.Region{}, 0, false, errors.New("vùng OCR phải nằm trong khung video, dùng tọa độ từ 0 đến 1")
	}
	interval := req.OCRSampleIntervalMS
	if interval == 0 {
		interval = config.Conf.VisualOCR.SampleIntervalMS
	}
	if interval == 0 {
		interval = 250
	}
	if interval < 40 || interval > 5000 {
		return "", visualocr.Region{}, 0, false, errors.New("khoảng quét OCR phải từ 40 đến 5000 ms")
	}
	return language, region, interval, req.OCRPreferGPU, nil
}

func sourceWorkflowStartMessage(sourceMethod string) string {
	if normalizeWorkflowSourceMethod(sourceMethod) == sourceMethodVisualOCR {
		return "Đang tải video/audio nguồn, sau đó chạy OCR khung hình để tạo SRT gốc cho bạn kiểm tra."
	}
	return "Đang tải video/audio nguồn, sau đó chạy speech-to-text để tạo SRT gốc cho bạn kiểm tra."
}

func sourceWorkflowReviewMessage(sourceMethod string) string {
	if normalizeWorkflowSourceMethod(sourceMethod) == sourceMethodVisualOCR {
		return "Đã tạo video nguồn và SRT từ OCR. Hãy xem/sửa script rồi bấm Duyệt nguồn."
	}
	return "Đã tạo video nguồn và phụ đề gốc. Hãy xem/sửa SRT rồi bấm Duyệt nguồn."
}

// extractVisualOCRSourceForReview reads visible, hardcoded subtitles from the
// downloaded video. It never invokes STT or an online OCR API. The resulting
// source SRT and script use exactly the same review and approval gate as STT.
func (s Service) extractVisualOCRSourceForReview(ctx context.Context, workflow *subtitleWorkflow, task *types.SubtitleTask, step *types.SubtitleTaskStepParam) error {
	if workflow == nil || task == nil || step == nil {
		return errors.New("thiếu dữ liệu workflow để chạy Visual OCR")
	}
	if strings.TrimSpace(step.InputVideoPath) == "" {
		return errors.New("không tìm thấy video nguồn để OCR")
	}
	reportSourceProgress(step, "visual_ocr", 0, "Preparing local PaddleOCR for visible captions")
	task.ProcessPct = 12
	outputSRTPath := filepath.Join(step.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	runner := visualocr.Runner{Config: visualocr.Config{
		PythonPath: config.Conf.VisualOCR.PythonPath,
		ScriptPath: config.Conf.VisualOCR.ScriptPath,
	}}
	result, err := runner.Extract(ctx, visualocr.Request{
		VideoPath:        step.InputVideoPath,
		OutputSRTPath:    outputSRTPath,
		Region:           workflow.OCRRegion,
		Language:         workflow.OCRLanguage,
		SampleIntervalMS: workflow.OCRIntervalMS,
		PreferGPU:        workflow.OCRPreferGPU,
		MergeGapMS:       visualOCRMergeGapMS,
	})
	if err != nil {
		return fmt.Errorf("Visual OCR không tạo được SRT nguồn: %w", err)
	}
	blocks, err := workflowSRTBlocks(outputSRTPath)
	if err != nil {
		return fmt.Errorf("SRT do Visual OCR tạo không hợp lệ: %w", err)
	}
	reportSourceProgress(step, "source_srt", 0, "Writing OCR script for review")
	if err := writeWorkflowText(filepath.Join(step.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), blocks, false); err != nil {
		return err
	}
	device := result.Device
	if result.FallbackToCPU {
		device = "cpu fallback"
	}
	reportSourceProgress(step, "visual_ocr", 100, fmt.Sprintf("OCR created %d timed cues on %s", len(blocks), device))
	reportSourceProgress(step, "source_srt", 100, "OCR SRT and script ready for review")
	task.ProcessPct = 33
	if strings.EqualFold(strings.TrimSpace(workflow.OriginLanguage), "auto") {
		if detected := normalizeVisualOCRLanguage(workflow.OCRLanguage); detected != "" {
			workflow.mu.Lock()
			workflow.OriginLanguage = detected
			workflow.mu.Unlock()
			step.OriginLanguage = types.StandardLanguageCode(detected)
			task.OriginLanguage = detected
		}
	}
	return nil
}

func normalizeVisualOCRLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ch", "zh", "zh-cn", "zh_cn":
		return "zh"
	case "japan", "ja":
		return "ja"
	case "korean", "ko":
		return "ko"
	case "en", "vi", "fr", "de", "es", "ru", "pt", "it":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}
