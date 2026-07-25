package service

import (
	"context"
	"errors"
	"fmt"
	"kova/config"
	"kova/internal/dto"
	"kova/internal/service/dubbing"
	"kova/internal/types"
	"kova/internal/visualocr"
	"kova/pkg/util"
	"path/filepath"
	"strings"
	"sync"
)

const visualOCRMergeGapMS = 450

// normalizeWorkflowOCRRequest makes Visual OCR a first-class source branch.
// It uses the lower subtitle band by default, while accepting an explicit ROI
// from the desktop for videos whose hard captions appear elsewhere.
func normalizeWorkflowOCRRequest(req dto.StartVideoSubtitleTaskReq, sourceMethod string) (string, visualocr.Region, int, bool, error) {
	if !workflowUsesOCR(sourceMethod) {
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
	switch normalizeWorkflowSourceMethod(sourceMethod) {
	case sourceMethodSpeechToTextAndOCR:
		return "Đang tải video/audio nguồn, chạy speech-to-text và OCR khung hình, rồi ghép thành SRT gốc để bạn kiểm tra."
	case sourceMethodVisualOCR:
		return "Đang tải video/audio nguồn, sau đó chạy OCR khung hình để tạo SRT gốc cho bạn kiểm tra."
	}
	return "Đang tải video/audio nguồn, sau đó chạy speech-to-text để tạo SRT gốc cho bạn kiểm tra."
}

func sourceWorkflowReviewMessage(sourceMethod string) string {
	switch normalizeWorkflowSourceMethod(sourceMethod) {
	case sourceMethodSpeechToTextAndOCR:
		return "Đã tạo SRT gốc kết hợp STT + OCR. Hãy xem/sửa script rồi bấm Duyệt nguồn."
	case sourceMethodVisualOCR:
		return "Đã tạo video nguồn và SRT từ OCR. Hãy xem/sửa script rồi bấm Duyệt nguồn."
	}
	return "Đã tạo video nguồn và phụ đề gốc. Hãy xem/sửa SRT rồi bấm Duyệt nguồn."
}

// extractVisualOCRSourceForReview reads visible, hardcoded subtitles from the
// downloaded video. It never invokes STT. OCR may run in the dedicated Colab
// worker or, only when explicitly selected, through the local PaddleOCR bridge.
// The resulting
// source SRT and script use exactly the same review and approval gate as STT.
func (s Service) extractVisualOCRSourceForReview(ctx context.Context, workflow *subtitleWorkflow, task *types.SubtitleTask, step *types.SubtitleTaskStepParam, destination ...string) error {
	if workflow == nil || task == nil || step == nil {
		return errors.New("thiếu dữ liệu workflow để chạy Visual OCR")
	}
	if strings.TrimSpace(step.InputVideoPath) == "" {
		return errors.New("không tìm thấy video nguồn để OCR")
	}
	engine := strings.TrimSpace(workflow.OCREngine)
	if engine == "" {
		engine = ocrEngineColab
	}
	if engine == ocrEngineColab {
		reportSourceProgress(step, "visual_ocr", 0, "Uploading video to the dedicated Colab GPU OCR worker")
	} else {
		reportSourceProgress(step, "visual_ocr", 0, "Preparing local PaddleOCR for visible captions")
	}
	task.ProcessPct = 12
	outputSRTPath := filepath.Join(step.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	if len(destination) > 0 && strings.TrimSpace(destination[0]) != "" {
		outputSRTPath = strings.TrimSpace(destination[0])
	}
	request := visualocr.Request{
		VideoPath:        step.InputVideoPath,
		OutputSRTPath:    outputSRTPath,
		Region:           workflow.OCRRegion,
		Language:         workflow.OCRLanguage,
		SampleIntervalMS: workflow.OCRIntervalMS,
		PreferGPU:        workflow.OCRPreferGPU,
		MergeGapMS:       visualOCRMergeGapMS,
	}
	var result visualocr.Result
	var err error
	if engine == ocrEngineColab {
		result, err = visualocr.ExtractRemote(ctx, visualocr.RemoteConfig{
			BaseURL: workflow.OCRWorkerURL,
			Token:   workflow.OCRWorkerToken,
		}, request)
	} else {
		runner := visualocr.Runner{Config: visualocr.Config{
			PythonPath: config.Conf.VisualOCR.PythonPath,
			ScriptPath: config.Conf.VisualOCR.ScriptPath,
		}}
		result, err = runner.Extract(ctx, request)
	}
	if err != nil {
		return fmt.Errorf("Visual OCR không tạo được SRT nguồn: %w", err)
	}
	blocks, err := workflowSRTBlocks(outputSRTPath)
	if err != nil {
		return fmt.Errorf("SRT do Visual OCR tạo không hợp lệ: %w", err)
	}
	if outputSRTPath == filepath.Join(step.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName) {
		reportSourceProgress(step, "source_srt", 0, "Writing OCR script for review")
		if err := writeWorkflowText(filepath.Join(step.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), blocks, false); err != nil {
			return err
		}
	}
	device := result.Device
	if result.FallbackToCPU {
		device = "cpu fallback"
	}
	reportSourceProgress(step, "visual_ocr", 100, fmt.Sprintf("OCR created %d timed cues on %s", len(blocks), device))
	if outputSRTPath == filepath.Join(step.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName) {
		reportSourceProgress(step, "source_srt", 100, "OCR SRT and script ready for review")
	}
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

// extractCombinedSourceForReview creates three inspectable artifacts: the
// STT transcript, the OCR transcript, and a canonical review SRT.  OCR only
// replaces a timed STT cue when their intervals overlap, preserving STT as
// the timing backbone and avoiding duplicate or out-of-order subtitle cues.
func (s Service) extractCombinedSourceForReview(ctx context.Context, workflow *subtitleWorkflow, task *types.SubtitleTask, step *types.SubtitleTaskStepParam) error {
	canonical := filepath.Join(step.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	sttPath := filepath.Join(step.TaskBasePath, "origin_language_stt.srt")
	ocrPath := filepath.Join(step.TaskBasePath, "origin_language_ocr.srt")

	// STT (including its audio-separation prerequisite) and OCR use different
	// Colab workers and different input files. Run them at the same time. Each
	// branch owns a copy of the mutable task parameters so the race detector
	// and the UI see independent, deterministic progress.
	sttStep, ocrStep := *step, *step
	sttTask, ocrTask := *task, *task
	sttStep.TaskPtr = &sttTask
	ocrStep.TaskPtr = &ocrTask

	sttErr, ocrErr := runParallelSourceExtractors(func() error {
		if err := s.separateSourceAudioForSTT(ctx, &sttStep); err != nil {
			return err
		}
		return s.transcribeSourceForReview(ctx, workflow, &sttTask, &sttStep, sttPath)
	}, func() error {
		return s.extractVisualOCRSourceForReview(ctx, workflow, &ocrTask, &ocrStep, ocrPath)
	})

	if sttErr != nil {
		detail := "Nhánh STT không hoàn tất: " + sttErr.Error()
		workflow.failTranslationStep("separate_audio", detail)
		workflow.failTranslationStep("speech_to_text", detail)
		workflow.addSourceWarning(detail)
	}
	if ocrErr != nil {
		detail := "Nhánh OCR không hoàn tất: " + ocrErr.Error()
		workflow.failTranslationStep("visual_ocr", detail)
		workflow.addSourceWarning(detail)
	}
	if sttErr != nil && ocrErr != nil {
		return fmt.Errorf("cả hai worker tạo script đều thất bại; STT: %v; OCR: %v", sttErr, ocrErr)
	}

	reportSourceProgress(step, "source_srt", 0, "Creating the canonical review SRT from completed extractor branches")
	var (
		blocks   []*util.SrtBlock
		replaced int
		err      error
		detail   string
	)
	switch {
	case sttErr == nil && ocrErr == nil:
		replaced, blocks, err = combineSTTAndOCRSourceSRT(sttPath, ocrPath)
		detail = fmt.Sprintf("Hybrid review SRT ready; OCR corrected %d aligned STT cue(s)", replaced)
	case sttErr == nil:
		blocks, err = workflowSRTBlocks(sttPath)
		detail = "STT review SRT ready; OCR warning is shown separately"
	default:
		blocks, err = workflowSRTBlocks(ocrPath)
		detail = "OCR review SRT ready; STT warning is shown separately"
	}
	if err != nil {
		return fmt.Errorf("không thể đọc artifact tạo script đã hoàn tất: %w", err)
	}
	if err := writeSourceSRT(canonical, blocks); err != nil {
		return err
	}
	if err := writeWorkflowText(filepath.Join(step.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), blocks, false); err != nil {
		return err
	}
	reportSourceProgress(step, "source_srt", 100, detail)
	originLanguage := strings.TrimSpace(string(sttStep.OriginLanguage))
	if sttErr != nil || originLanguage == "" || strings.EqualFold(originLanguage, "auto") {
		originLanguage = strings.TrimSpace(string(ocrStep.OriginLanguage))
	}
	workflow.mu.Lock()
	if originLanguage != "" && !strings.EqualFold(originLanguage, "auto") {
		workflow.OriginLanguage = originLanguage
	} else {
		originLanguage = workflow.OriginLanguage
	}
	workflow.mu.Unlock()
	step.OriginLanguage = types.StandardLanguageCode(originLanguage)
	task.OriginLanguage = originLanguage
	task.ProcessPct = 33
	return nil
}

func runParallelSourceExtractors(stt, ocr func() error) (error, error) {
	var sttErr, ocrErr error
	var branches sync.WaitGroup
	branches.Add(2)
	go func() {
		defer branches.Done()
		sttErr = stt()
	}()
	go func() {
		defer branches.Done()
		ocrErr = ocr()
	}()
	branches.Wait()
	return sttErr, ocrErr
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

func combineSTTAndOCRSourceSRT(sttPath, ocrPath string) (int, []*util.SrtBlock, error) {
	stt, err := dubbing.ParseSRTFile(sttPath)
	if err != nil {
		return 0, nil, fmt.Errorf("không thể đọc SRT STT: %w", err)
	}
	ocr, err := dubbing.ParseSRTFile(ocrPath)
	if err != nil {
		return 0, nil, fmt.Errorf("không thể đọc SRT OCR: %w", err)
	}
	if len(stt) == 0 {
		return 0, nil, errors.New("SRT STT không có cue để kết hợp OCR")
	}
	result := make([]*util.SrtBlock, 0, len(stt))
	replaced := 0
	for index, cue := range stt {
		text := strings.TrimSpace(cue.Text)
		bestOverlap := 0.0
		bestText := ""
		for _, visible := range ocr {
			overlap := minFloat(cue.End, visible.End) - maxFloat(cue.Start, visible.Start)
			if overlap <= 0 || strings.TrimSpace(visible.Text) == "" {
				continue
			}
			// At least 45% of the shorter cue must overlap. This prevents a
			// transient title or watermark from replacing spoken dialogue.
			shorter := minFloat(cue.End-cue.Start, visible.End-visible.Start)
			if shorter <= 0 || overlap/shorter < 0.45 || overlap <= bestOverlap {
				continue
			}
			bestOverlap = overlap
			bestText = strings.TrimSpace(visible.Text)
		}
		if bestText != "" && !strings.EqualFold(bestText, text) {
			text = bestText
			replaced++
		}
		result = append(result, &util.SrtBlock{
			Index:                  index + 1,
			Timestamp:              fmt.Sprintf("%s --> %s", dubbing.FormatTimestamp(cue.Start), dubbing.FormatTimestamp(cue.End)),
			OriginLanguageSentence: text,
		})
	}
	return replaced, result, nil
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
