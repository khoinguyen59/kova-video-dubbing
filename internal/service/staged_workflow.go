package service

// The legacy subtitle job intentionally remains available for integrations,
// but the native Kova desktop uses this file's staged workflow.  A stage is
// never advanced implicitly: every generated SRT/audio/video is persisted,
// exposed as an artifact, and must be approved by the user before a later
// action can start.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"kova/config"
	"kova/internal/dto"
	"kova/internal/service/dubbing"
	"kova/internal/storage"
	"kova/internal/types"
	"kova/internal/visualocr"
	"kova/log"
	"kova/pkg/omnivoice"
	"kova/pkg/util"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const workflowStateFileName = "workflow_state.json"

const (
	sourceMethodSpeechToText = "speech_to_text"
	sourceMethodVisualOCR    = "visual_ocr"
)

const (
	workflowSourceRunning        = "source_running"
	workflowAwaitSourceReview    = "awaiting_source_review"
	workflowSourceApproved       = "source_approved"
	workflowTranslationRunning   = "translation_running"
	workflowAwaitTranslation     = "awaiting_translation_review"
	workflowTranslationApproved  = "translation_approved"
	workflowDubbingAudioRunning  = "dubbing_audio_running"
	workflowAwaitDubbingAudio    = "awaiting_dubbing_audio_review"
	workflowDubbingAudioApproved = "dubbing_audio_approved"
	workflowDubbingVideoRunning  = "dubbing_video_running"
	workflowAwaitDubbingVideo    = "awaiting_dubbing_video_review"
	workflowDubbingVideoApproved = "dubbing_video_approved"
	workflowRenderRunning        = "render_running"
	workflowCompleted            = "completed"
	workflowFailed               = "failed"
)

// Deprecated symbolic aliases keep in-package compatibility for older tests
// and callers. They resolve to the new explicit milestones, never to the old
// persisted string values.
const (
	workflowDubbingRunning  = workflowDubbingAudioRunning
	workflowAwaitDubbing    = workflowAwaitDubbingAudio
	workflowDubbingApproved = workflowDubbingVideoApproved
)

var (
	workflowSessions   sync.Map // task ID -> *subtitleWorkflow
	workflowTaskIDExpr = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// subtitleWorkflow is deliberately persisted without any reference-audio
// value.  A clone reference and consent are supplied only at the explicit
// dubbing action, never retained in the task JSON after that request returns.
type subtitleWorkflow struct {
	mu sync.Mutex

	TaskID        string           `json:"task_id"`
	TaskBasePath  string           `json:"task_base_path"`
	URL           string           `json:"url"`
	SourceMethod  string           `json:"source_method"`
	OCRLanguage   string           `json:"ocr_language,omitempty"`
	OCRRegion     visualocr.Region `json:"ocr_region,omitempty"`
	OCRIntervalMS int              `json:"ocr_sample_interval_ms,omitempty"`
	OCRPreferGPU  bool             `json:"ocr_prefer_gpu"`

	OriginLanguage string   `json:"origin_language"`
	TargetLanguage string   `json:"target_language"`
	UserLanguage   string   `json:"user_language"`
	Bilingual      bool     `json:"bilingual"`
	TargetFirst    bool     `json:"target_first"`
	ModalFilter    bool     `json:"modal_filter"`
	ProtectedTerms []string `json:"protected_terms,omitempty"`
	EmbedType      string   `json:"embed_type"`
	VerticalTitle  string   `json:"vertical_title,omitempty"`
	VerticalSub    string   `json:"vertical_subtitle,omitempty"`

	CurrentStage         string `json:"current_stage"`
	Message              string `json:"message"`
	FailureReason        string `json:"failure_reason,omitempty"`
	FailedStage          string `json:"failed_stage,omitempty"`
	SourceApproved       bool   `json:"source_approved"`
	TranslationApproved  bool   `json:"translation_approved"`
	DubbingRequested     bool   `json:"dubbing_requested"`
	DubbingAudioApproved bool   `json:"dubbing_audio_approved"`
	DubbingVideoApproved bool   `json:"dubbing_video_approved"`
	// DubbingApproved is read only to migrate workspaces produced before the
	// audio-review/video-review split. New code must use the two explicit
	// approval fields above.
	DubbingApproved     bool                       `json:"dubbing_approved,omitempty"`
	SourceRevision      int                        `json:"source_revision"`
	TranslationRevision int                        `json:"translation_revision"`
	SourceSteps         []dto.WorkflowProgressStep `json:"source_steps,omitempty"`
	TranslationWarnings []dto.TranslationWarning   `json:"translation_warnings,omitempty"`
	UpdatedAt           string                     `json:"updated_at"`
}

func initialSourceSteps() []dto.WorkflowProgressStep {
	return initialSourceStepsFor(sourceMethodSpeechToText)
}

func initialSourceStepsFor(sourceMethod string) []dto.WorkflowProgressStep {
	textStep := "speech_to_text"
	if normalizeWorkflowSourceMethod(sourceMethod) == sourceMethodVisualOCR {
		textStep = "visual_ocr"
	}
	return []dto.WorkflowProgressStep{
		{ID: "download_video", State: "pending", Percent: 0},
		{ID: "download_audio", State: "pending", Percent: 0},
		{ID: textStep, State: "pending", Percent: 0},
		{ID: "source_srt", State: "pending", Percent: 0},
	}
}

func normalizeWorkflowSourceMethod(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), sourceMethodVisualOCR) {
		return sourceMethodVisualOCR
	}
	return sourceMethodSpeechToText
}

func validateWorkflowSourceMethod(raw string) (string, error) {
	method := strings.ToLower(strings.TrimSpace(raw))
	switch method {
	case "", sourceMethodSpeechToText:
		return sourceMethodSpeechToText, nil
	case sourceMethodVisualOCR:
		return sourceMethodVisualOCR, nil
	default:
		return "", fmt.Errorf("source_method không hỗ trợ: %s", raw)
	}
}

func cloneSourceSteps(steps []dto.WorkflowProgressStep) []dto.WorkflowProgressStep {
	return append([]dto.WorkflowProgressStep(nil), steps...)
}

func cloneTranslationWarnings(warnings []dto.TranslationWarning) []dto.TranslationWarning {
	result := make([]dto.TranslationWarning, 0, len(warnings))
	for _, warning := range warnings {
		warning.SuspiciousWords = append([]string(nil), warning.SuspiciousWords...)
		result = append(result, warning)
	}
	return result
}

func (w *subtitleWorkflow) updateSourceStep(id string, percent uint8, detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if len(w.SourceSteps) == 0 {
		w.SourceSteps = initialSourceStepsFor(w.SourceMethod)
	}
	for index := range w.SourceSteps {
		if w.SourceSteps[index].ID != id {
			continue
		}
		if percent > 100 {
			percent = 100
		}
		w.SourceSteps[index].Percent = percent
		w.SourceSteps[index].Detail = detail
		if percent >= 100 {
			w.SourceSteps[index].State = "completed"
		} else {
			w.SourceSteps[index].State = "running"
		}
		break
	}
	w.mu.Unlock()
}

func (w *subtitleWorkflow) failActiveSourceStep(detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if len(w.SourceSteps) == 0 {
		w.SourceSteps = initialSourceStepsFor(w.SourceMethod)
	}
	for index := range w.SourceSteps {
		if w.SourceSteps[index].State == "running" {
			w.SourceSteps[index].State = "failed"
			w.SourceSteps[index].Detail = detail
			break
		}
	}
	w.mu.Unlock()
}

// sourceStepsForSnapshot backfills explicit source phases for workflows that
// were created before source_steps was persisted. This lets a user reopen an
// older failed job and still see that the download succeeded before STT
// failed, instead of seeing one misleading aggregate percentage.
func sourceStepsForSnapshot(steps []dto.WorkflowProgressStep, sourceMethod, stage, basePath, failure string) []dto.WorkflowProgressStep {
	if len(steps) > 0 {
		return cloneSourceSteps(steps)
	}
	if stage != workflowSourceRunning && stage != workflowAwaitSourceReview && stage != workflowFailed {
		return nil
	}
	method := normalizeWorkflowSourceMethod(sourceMethod)
	textStep := "speech_to_text"
	textStepComplete := "Timed transcript created"
	textStepRunning := "Preparing timed transcription"
	if method == sourceMethodVisualOCR {
		textStep = "visual_ocr"
		textStepComplete = "Visible captions extracted with OCR"
		textStepRunning = "Preparing visual subtitle OCR"
	}
	result := initialSourceStepsFor(method)
	complete := func(id, detail string) {
		for index := range result {
			if result[index].ID == id {
				result[index].State = "completed"
				result[index].Percent = 100
				result[index].Detail = detail
				return
			}
		}
	}
	set := func(id, state, detail string) {
		for index := range result {
			if result[index].ID == id {
				result[index].State = state
				result[index].Detail = detail
				return
			}
		}
	}
	hasAudio := fileExists(filepath.Join(basePath, types.SubtitleTaskAudioFileName))
	hasVideo := fileExists(filepath.Join(basePath, types.SubtitleTaskVideoFileName))
	hasSRT := fileExists(filepath.Join(basePath, types.SubtitleTaskOriginLanguageSrtFileName))
	if hasAudio {
		complete("download_audio", "Source audio downloaded")
	}
	if hasVideo {
		complete("download_video", "Source video downloaded")
	}
	if hasSRT {
		complete(textStep, textStepComplete)
		complete("source_srt", "Review SRT ready")
		return result
	}
	if stage == workflowFailed {
		switch {
		case !hasAudio:
			set("download_audio", "failed", failure)
		case !hasVideo:
			set("download_video", "failed", failure)
		default:
			set(textStep, "failed", failure)
		}
		return result
	}
	if hasAudio && hasVideo {
		set(textStep, "running", textStepRunning)
	} else if hasAudio {
		set("download_video", "running", "Downloading source video")
	} else {
		set("download_audio", "running", "Downloading source audio")
	}
	return result
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (w *subtitleWorkflow) task() *types.SubtitleTask {
	if cached, ok := storage.SubtitleTasks.Load(w.TaskID); ok {
		if task, ok := cached.(*types.SubtitleTask); ok && task != nil {
			return task
		}
	}
	task := &types.SubtitleTask{
		TaskId:         w.TaskID,
		VideoSrc:       w.URL,
		OriginLanguage: w.OriginLanguage,
		TargetLanguage: w.TargetLanguage,
		Status:         types.SubtitleTaskStatusProcessing,
	}
	storage.SubtitleTasks.Store(w.TaskID, task)
	return task
}

func (w *subtitleWorkflow) protectedTermMap() map[string]string {
	terms := make(map[string]string)
	for index, value := range w.ProtectedTerms {
		value = strings.TrimSpace(value)
		if value != "" {
			terms[value] = fmt.Sprintf("[[KOVA_PROPER_%03d]]", index+1)
		}
	}
	return terms
}

func (w *subtitleWorkflow) subtitleResultType() types.SubtitleResultType {
	if strings.EqualFold(w.TargetLanguage, "none") {
		return types.SubtitleResultTypeOriginOnly
	}
	if !w.Bilingual {
		return types.SubtitleResultTypeTargetOnly
	}
	if w.TargetFirst {
		return types.SubtitleResultTypeBilingualTranslationOnTop
	}
	return types.SubtitleResultTypeBilingualTranslationOnBottom
}

func (w *subtitleWorkflow) stepParam(task *types.SubtitleTask) *types.SubtitleTaskStepParam {
	return &types.SubtitleTaskStepParam{
		TaskId:                      w.TaskID,
		TaskPtr:                     task,
		TaskBasePath:                w.TaskBasePath,
		Link:                        w.URL,
		SubtitleResultType:          w.subtitleResultType(),
		EnableModalFilter:           w.ModalFilter,
		ReplaceWordsMap:             map[string]string{},
		ProtectedTerms:              w.protectedTermMap(),
		OriginLanguage:              types.StandardLanguageCode(w.OriginLanguage),
		TargetLanguage:              types.StandardLanguageCode(w.TargetLanguage),
		UserUILanguage:              types.StandardLanguageCode(w.UserLanguage),
		BilingualSrtFilePath:        filepath.Join(w.TaskBasePath, types.SubtitleTaskBilingualSrtFileName),
		ShortOriginMixedSrtFilePath: filepath.Join(w.TaskBasePath, types.SubtitleTaskShortOriginMixedSrtFileName),
		TtsSourceFilePath:           filepath.Join(w.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName),
		TtsResultFilePath:           filepath.Join(w.TaskBasePath, types.TtsResultAudioFileName),
		InputVideoPath:              filepath.Join(w.TaskBasePath, types.SubtitleTaskVideoFileName),
		VideoWithTtsFilePath:        filepath.Join(w.TaskBasePath, types.SubtitleTaskVideoWithTtsFileName),
		EmbedSubtitleVideoType:      w.EmbedType,
		VerticalVideoMajorTitle:     w.VerticalTitle,
		VerticalVideoMinorTitle:     w.VerticalSub,
		MaxWordOneLine:              12,
		// The staged KOVA source flow always creates the review SRT through
		// speech-to-text. It must not request or depend on a platform VTT.
		VttSwitch: false,
	}
}

func workflowPath(basePath string) string {
	return filepath.Join(basePath, workflowStateFileName)
}

func persistWorkflow(workflow *subtitleWorkflow) error {
	workflow.mu.Lock()
	workflow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(workflow, "", "  ")
	basePath := workflow.TaskBasePath
	workflow.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return err
	}
	return os.WriteFile(workflowPath(basePath), append(data, '\n'), 0644)
}

func validWorkflowTaskID(taskID string) bool {
	return workflowTaskIDExpr.MatchString(strings.TrimSpace(taskID))
}

func loadWorkflow(taskID string) (*subtitleWorkflow, error) {
	taskID = strings.TrimSpace(taskID)
	if !validWorkflowTaskID(taskID) {
		return nil, errors.New("mã job workflow không hợp lệ")
	}
	if cached, ok := workflowSessions.Load(taskID); ok {
		if workflow, ok := cached.(*subtitleWorkflow); ok && workflow != nil {
			return workflow, nil
		}
	}
	basePath := filepath.Join("tasks", taskID)
	data, err := os.ReadFile(workflowPath(basePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("không tìm thấy workflow; hãy bắt đầu từ bước nguồn")
		}
		return nil, fmt.Errorf("không thể đọc workflow: %w", err)
	}
	workflow := &subtitleWorkflow{}
	if err := json.Unmarshal(data, workflow); err != nil {
		return nil, fmt.Errorf("workflow_state.json không hợp lệ: %w", err)
	}
	if workflow.TaskID != taskID || workflow.TaskBasePath == "" {
		return nil, errors.New("workflow_state.json không khớp mã job")
	}
	if normalizeLegacyWorkflowDubbingState(workflow) {
		if err := persistWorkflow(workflow); err != nil {
			return nil, err
		}
	}
	workflowSessions.Store(taskID, workflow)
	workflow.task()
	return workflow, nil
}

// normalizeLegacyWorkflowDubbingState makes a persisted job created before
// the split safe to resume. A previous combined approval never silently
// grants the two new review gates: it returns to the audio-review checkpoint.
// A persisted "running" stage cannot have a live goroutine after restart, so
// it is exposed as a retryable audio-stage failure instead of remaining stuck.
func normalizeLegacyWorkflowDubbingState(workflow *subtitleWorkflow) bool {
	if workflow == nil {
		return false
	}
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	changed := false
	switch workflow.CurrentStage {
	case "dubbing_running":
		workflow.CurrentStage = workflowFailed
		workflow.FailedStage = workflowDubbingAudioRunning
		workflow.Message = "Bước tạo audio trước đó đã dừng khi khởi động lại; hãy bấm tạo audio để chạy lại."
		changed = true
	case "awaiting_dubbing_review", "dubbing_approved":
		workflow.CurrentStage = workflowAwaitDubbingAudio
		workflow.FailedStage = ""
		workflow.FailureReason = ""
		workflow.DubbingRequested = true
		workflow.DubbingAudioApproved = false
		workflow.DubbingVideoApproved = false
		workflow.Message = "Job cũ cần được duyệt lại audio trước khi ghép video theo luồng mới."
		changed = true
	}
	if workflow.DubbingApproved {
		workflow.DubbingApproved = false
		changed = true
	}
	return changed
}

func workflowTaskID(url string) string {
	base := "kova"
	if videoID, err := util.GetYouTubeID(url); err == nil && videoID != "" {
		base = videoID
	}
	base = util.SanitizePathName(base)
	if base == "" {
		base = "kova"
	}
	return fmt.Sprintf("%s_%s", base, util.GenerateRandStringWithUpperLowerNum(8))
}

func createWorkflow(req dto.StartVideoSubtitleTaskReq) (*subtitleWorkflow, error) {
	sourceURL := strings.TrimSpace(req.Url)
	if !util.IsYouTubeURL(sourceURL) && !strings.HasPrefix(sourceURL, "local:") {
		return nil, errors.New("quy trình nguồn cần URL YouTube/youtu.be hoặc đường dẫn local:<video>")
	}
	if util.IsYouTubeURL(sourceURL) {
		if videoID, err := util.GetYouTubeID(sourceURL); err != nil || strings.TrimSpace(videoID) == "" {
			return nil, errors.New("URL YouTube không hợp lệ")
		}
	}
	sourceMethod, err := validateWorkflowSourceMethod(req.SourceMethod)
	if err != nil {
		return nil, err
	}
	ocrLanguage, ocrRegion, ocrInterval, ocrPreferGPU, err := normalizeWorkflowOCRRequest(req, sourceMethod)
	if err != nil {
		return nil, err
	}
	for range 8 {
		taskID := workflowTaskID(req.Url)
		basePath := filepath.Join("tasks", taskID)
		if _, err := os.Stat(basePath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(basePath, "output"), 0755); err != nil {
			return nil, err
		}
		workflow := &subtitleWorkflow{
			TaskID:         taskID,
			TaskBasePath:   basePath,
			URL:            sourceURL,
			SourceMethod:   sourceMethod,
			OCRLanguage:    ocrLanguage,
			OCRRegion:      ocrRegion,
			OCRIntervalMS:  ocrInterval,
			OCRPreferGPU:   ocrPreferGPU,
			OriginLanguage: strings.TrimSpace(req.OriginLanguage),
			TargetLanguage: strings.TrimSpace(req.TargetLang),
			UserLanguage:   strings.TrimSpace(req.Language),
			Bilingual:      req.Bilingual == types.SubtitleTaskBilingualYes,
			TargetFirst:    req.TranslationSubtitlePos == types.SubtitleTaskTranslationSubtitlePosTop,
			ModalFilter:    req.ModalFilter == types.SubtitleTaskModalFilterYes,
			ProtectedTerms: append([]string(nil), req.ProtectTerms...),
			EmbedType:      strings.TrimSpace(req.EmbedSubtitleVideoType),
			VerticalTitle:  req.VerticalMajorTitle,
			VerticalSub:    req.VerticalMinorTitle,
			CurrentStage:   workflowSourceRunning,
			Message:        sourceWorkflowStartMessage(sourceMethod),
			SourceRevision: 1,
			SourceSteps:    initialSourceStepsFor(sourceMethod),
		}
		if workflow.OriginLanguage == "" {
			workflow.OriginLanguage = "en"
		}
		if workflow.TargetLanguage == "" {
			workflow.TargetLanguage = "vi"
		}
		if workflow.UserLanguage == "" {
			workflow.UserLanguage = "vi"
		}
		task := workflow.task()
		task.ProcessPct = 1
		if err := persistWorkflow(workflow); err != nil {
			return nil, err
		}
		workflowSessions.Store(taskID, workflow)
		return workflow, nil
	}
	return nil, errors.New("không thể tạo mã job duy nhất")
}

// StartWorkflowSource creates only source artifacts.  Translation, dubbing,
// and rendering are not queued from here.
func (s Service) StartWorkflowSource(req dto.StartVideoSubtitleTaskReq) (*dto.SubtitleWorkflowData, error) {
	workflow, err := createWorkflow(req)
	if err != nil {
		return nil, err
	}
	go s.runWorkflowSource(workflow)
	return workflowSnapshot(workflow), nil
}

func (s Service) runWorkflowSource(workflow *subtitleWorkflow) {
	task := workflow.task()
	step := workflow.stepParam(task)
	// The source stage deliberately downloads an MP4 even before rendering so
	// the user can inspect the actual source artifact and later render without a
	// hidden second download. It does not call any subtitle/video render action.
	step.EmbedSubtitleVideoType = "horizontal"
	step.EnableTts = false
	step.SourceProgress = func(id string, percent uint8, detail string) {
		workflow.updateSourceStep(id, percent, detail)
	}
	if err := s.linkToFile(context.Background(), step); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	var sourceErr error
	if normalizeWorkflowSourceMethod(workflow.SourceMethod) == sourceMethodVisualOCR {
		sourceErr = s.extractVisualOCRSourceForReview(context.Background(), workflow, task, step)
	} else {
		sourceErr = s.transcribeSourceForReview(context.Background(), workflow, task, step)
	}
	if sourceErr != nil {
		s.failWorkflow(workflow, task, sourceErr)
		return
	}
	task.ProcessPct = 35
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitSourceReview
	workflow.Message = sourceWorkflowReviewMessage(workflow.SourceMethod)
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

func (s Service) finishSourceWithoutSubtitle(workflow *subtitleWorkflow, task *types.SubtitleTask, subtitleErr error) {
	task.ProcessPct = 35
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitSourceReview
	workflow.Message = "Đã tải video và audio nguồn. Không tìm được phụ đề YouTube; hãy dán hoặc nhập SRT gốc để kiểm tra trước khi duyệt nguồn."
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	if err := persistWorkflow(workflow); err != nil {
		log.GetLogger().Warn("could not persist source-without-subtitle workflow", zap.Error(err))
	}
	log.GetLogger().Warn("source media downloaded without a YouTube subtitle track", zap.String("taskId", workflow.TaskID), zap.Error(subtitleErr))
}

// extractSourceSRTForReview is the source-only half of the historical VTT
// pipeline. It intentionally ends before BatchTranslateSrtBlocks so the user
// can correct text and timestamps before any model is asked to translate it.
func (s *YouTubeSubtitleService) extractSourceSRTForReview(req *YoutubeSubtitleReq) (string, error) {
	if req == nil || strings.TrimSpace(req.VttFile) == "" {
		return "", errors.New("không có file VTT nguồn")
	}
	originFile := filepath.Join(req.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	hasWordTimestamps, err := s.DetectVttFormat(req.VttFile)
	if err != nil {
		return "", fmt.Errorf("không thể xác định định dạng VTT: %w", err)
	}
	if !hasWordTimestamps {
		if err := util.ConvertBlockVttToSrt(req.VttFile, originFile); err != nil {
			return "", fmt.Errorf("không thể chuyển VTT block sang SRT: %w", err)
		}
		blocks, err := workflowSRTBlocks(originFile)
		if err != nil {
			return "", err
		}
		return originFile, writeWorkflowText(filepath.Join(req.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), blocks, false)
	}
	words, err := s.ExtractWordsFromVtt(req.VttFile)
	if err != nil {
		return "", fmt.Errorf("không thể đọc từ VTT: %w", err)
	}
	sentences := s.groupWordsIntoSentences(words)
	if len(sentences) == 0 {
		return "", errors.New("VTT không tạo được câu phụ đề")
	}
	blocks, err := s.generateOriginLanguageSrt(sentences, originFile, req)
	if err != nil {
		return "", fmt.Errorf("không thể tạo SRT gốc: %w", err)
	}
	if err := writeWorkflowText(filepath.Join(req.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), blocks, false); err != nil {
		return "", err
	}
	return originFile, nil
}

func workflowSRTBlocks(path string) ([]*util.SrtBlock, error) {
	cues, err := dubbing.ParseSRTFile(path)
	if err != nil {
		return nil, fmt.Errorf("SRT không hợp lệ: %w", err)
	}
	if len(cues) == 0 {
		return nil, errors.New("SRT không có cue nào")
	}
	blocks := make([]*util.SrtBlock, 0, len(cues))
	for _, cue := range cues {
		text := strings.TrimSpace(cue.Text)
		if text == "" {
			return nil, fmt.Errorf("cue %d không có nội dung", cue.Index)
		}
		blocks = append(blocks, &util.SrtBlock{
			Index:                  cue.Index,
			Timestamp:              fmt.Sprintf("%s --> %s", dubbing.FormatTimestamp(cue.Start), dubbing.FormatTimestamp(cue.End)),
			OriginLanguageSentence: text,
		})
	}
	return blocks, nil
}

func writeWorkflowText(path string, blocks []*util.SrtBlock, target bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block == nil {
			continue
		}
		value := block.OriginLanguageSentence
		if target {
			value = block.TargetLanguageSentence
		}
		if value = strings.TrimSpace(value); value != "" {
			lines = append(lines, value)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func (s Service) StartWorkflowTranslation(taskID string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowTranslationRunning
	allowed := (workflow.CurrentStage == workflowSourceApproved || retrying) && workflow.SourceApproved
	if allowed {
		workflow.CurrentStage = workflowTranslationRunning
		workflow.Message = "Đang dịch bản SRT gốc đã được duyệt."
		workflow.FailureReason = ""
		workflow.FailedStage = ""
		workflow.TranslationWarnings = nil
	}
	workflow.mu.Unlock()
	if !allowed {
		return nil, errors.New("hãy duyệt phụ đề gốc trước khi bắt đầu dịch")
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	go s.runWorkflowTranslation(workflow)
	return workflowSnapshot(workflow), nil
}

func (s Service) runWorkflowTranslation(workflow *subtitleWorkflow) {
	task := workflow.task()
	originPath := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	blocks, err := workflowSRTBlocks(originPath)
	if err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	if strings.TrimSpace(workflow.TargetLanguage) == "" || strings.EqualFold(workflow.TargetLanguage, "none") {
		s.failWorkflow(workflow, task, errors.New("hãy chọn ngôn ngữ đích trước khi dịch"))
		return
	}
	if s.YouTubeSubtitleSrv == nil || s.YouTubeSubtitleSrv.translator == nil {
		s.failWorkflow(workflow, task, ErrYouTubeSubtitleServiceNotInitialized)
		return
	}
	task.ProcessPct = 50
	terms := workflow.protectedTermMap()
	protectSrtBlockTerms(blocks, terms)
	err = s.YouTubeSubtitleSrv.translator.BatchTranslateSrtBlocks(blocks, workflow.OriginLanguage, workflow.TargetLanguage, task)
	restoreSrtBlockTerms(blocks, terms)
	if err != nil {
		s.failWorkflow(workflow, task, fmt.Errorf("dịch batch thất bại: %w", err))
		return
	}
	targetPath := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName)
	if err := s.YouTubeSubtitleSrv.writeTargetLanguageSrtFile(blocks, targetPath); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	bilingualPath := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskBilingualSrtFileName)
	if err := s.YouTubeSubtitleSrv.writeBilingualSrtFile(blocks, bilingualPath, workflow.TargetFirst); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	// A reviewed bilingual SRT is a safe vertical fallback. The traditional
	// shortener depends on raw VTT word groups, which are intentionally no
	// longer kept after a user has edited the source script.
	shortPath := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskShortOriginMixedSrtFileName)
	if data, readErr := os.ReadFile(bilingualPath); readErr == nil {
		_ = os.WriteFile(shortPath, data, 0644)
	}
	if err := writeWorkflowText(filepath.Join(workflow.TaskBasePath, "output", types.SubtitleTaskTargetLanguageTextFileName), blocks, true); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	task.ProcessPct = 70
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitTranslation
	workflow.TranslationWarnings = translationReviewWarnings(blocks, workflow.OriginLanguage, workflow.TargetLanguage)
	workflow.Message = translationReviewMessage(workflow.TranslationWarnings)
	workflow.FailureReason = ""
	workflow.TranslationRevision++
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

// StartWorkflowDubbing is kept as a compatibility alias. It starts audio
// synthesis only; native clients use StartWorkflowDubbingAudio explicitly.
func (s Service) StartWorkflowDubbing(taskID string, req dto.StartWorkflowDubbingReq) (*dto.SubtitleWorkflowData, error) {
	return s.StartWorkflowDubbingAudio(taskID, req)
}

// StartWorkflowDubbingAudio starts only speech synthesis. It never muxes a
// video: the user must approve the produced audio before video assembly.
func (s Service) StartWorkflowDubbingAudio(taskID string, req dto.StartWorkflowDubbingReq) (*dto.SubtitleWorkflowData, error) {
	// The desktop TTS selector mutates the session configuration immediately
	// before this request. Rebuild the captured client here, before changing
	// workflow state or starting a goroutine, so Google TTS can never inherit a
	// stale OmniVoice client from an earlier run.
	s.RefreshTTSClient()
	if err := s.ValidateTTSPreflight(); err != nil {
		return nil, err
	}
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowDubbingAudioRunning
	allowed := (workflow.CurrentStage == workflowTranslationApproved || retrying) && workflow.TranslationApproved
	if allowed {
		workflow.CurrentStage = workflowDubbingAudioRunning
		workflow.Message = "Đang tạo audio lồng tiếng từ bản dịch đã duyệt."
		workflow.FailureReason = ""
		workflow.FailedStage = ""
		workflow.DubbingRequested = true
		workflow.DubbingAudioApproved = false
		workflow.DubbingVideoApproved = false
		workflow.DubbingApproved = false
	}
	workflow.mu.Unlock()
	if !allowed {
		return nil, errors.New("hãy duyệt bản dịch trước khi tạo audio lồng tiếng")
	}
	if err := validateWorkflowDubbingRequest(req); err != nil {
		workflow.mu.Lock()
		workflow.CurrentStage = workflowTranslationApproved
		workflow.DubbingRequested = false
		workflow.DubbingAudioApproved = false
		workflow.DubbingVideoApproved = false
		workflow.DubbingApproved = false
		workflow.mu.Unlock()
		_ = persistWorkflow(workflow)
		return nil, err
	}
	// A deliberate re-synthesis invalidates every downstream audio/video
	// artifact. The reviewed translated SRT remains untouched.
	if err := clearWorkflowDubbingArtifacts(workflow.TaskBasePath); err != nil {
		s.failWorkflow(workflow, workflow.task(), fmt.Errorf("cannot clear previous dubbing artifacts: %w", err))
		return nil, err
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	go s.runWorkflowDubbingAudio(workflow, req)
	return workflowSnapshot(workflow), nil
}

// SkipWorkflowDubbing discards an optional dub without invalidating the
// reviewed translation.  It is intentionally available only after the
// translation review has been approved: callers can either decide not to
// create a dub at all, discard a generated dub during its review, or recover
// from a failed dubbing attempt.  No local synthesis is attempted here.
func (s Service) SkipWorkflowDubbing(taskID string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	task := workflow.task()

	workflow.mu.Lock()
	if !workflow.TranslationApproved || !canSkipWorkflowDubbing(workflow.CurrentStage, workflow.FailedStage) {
		workflow.mu.Unlock()
		return nil, errors.New("chỉ có thể bỏ qua lồng tiếng sau khi đã duyệt bản dịch")
	}

	// Hold the workflow lock while clearing task-owned files so a new dubbing
	// request cannot start and write into the same paths in between this check
	// and the state transition.
	if err := clearWorkflowDubbingArtifacts(workflow.TaskBasePath); err != nil {
		workflow.mu.Unlock()
		return nil, fmt.Errorf("không thể xoá đầu ra lồng tiếng cũ: %w", err)
	}
	workflow.DubbingRequested = false
	workflow.DubbingAudioApproved = false
	workflow.DubbingVideoApproved = false
	workflow.DubbingApproved = false
	workflow.CurrentStage = workflowTranslationApproved
	workflow.Message = "Đã bỏ qua lồng tiếng. Bản dịch đã duyệt vẫn được giữ nguyên; bạn có thể render video phụ đề."
	workflow.FailureReason = ""
	workflow.FailedStage = ""
	workflow.mu.Unlock()

	// A dubbing failure must not leave the whole task visibly failed after the
	// user explicitly chooses the subtitle-only branch.
	task.Status = types.SubtitleTaskStatusProcessing
	task.FailReason = ""
	task.ProcessPct = 75
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	return workflowSnapshot(workflow), nil
}

func canSkipWorkflowDubbing(stage, failedStage string) bool {
	switch stage {
	case workflowTranslationApproved,
		workflowAwaitDubbingAudio,
		workflowDubbingAudioApproved,
		workflowAwaitDubbingVideo,
		workflowDubbingVideoApproved:
		return true
	case workflowFailed:
		return failedStage == workflowDubbingAudioRunning || failedStage == workflowDubbingVideoRunning
	default:
		return false
	}
}

// clearWorkflowDubbingArtifacts removes only generated dubbing/render files.
// The source SRT, reviewed translated SRT, bilingual SRT, and translated text
// deliberately remain so the user can immediately select subtitle-only
// rendering without losing their review work.
func clearWorkflowDubbingArtifacts(basePath string) error {
	paths := []string{
		filepath.Join(basePath, types.TtsResultAudioFileName),
		filepath.Join(basePath, types.SubtitleTaskVideoWithTtsFileName),
		filepath.Join(basePath, types.SubtitleTaskTransferredVerticalVideoFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName),
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// The runner owns this directory (plans, timing report, segments and its
	// generated dubbing SRT); removing it cannot affect a reviewed subtitle.
	if err := os.RemoveAll(filepath.Join(basePath, dubbing.DubbingDirName)); err != nil {
		return err
	}
	return nil
}

// clearWorkflowDubbedVideoArtifacts invalidates only video products while
// retaining an already approved audio track and its timing report. It is used
// when the user starts (or retries) the separate mux stage.
func clearWorkflowDubbedVideoArtifacts(basePath string) error {
	paths := []string{
		filepath.Join(basePath, types.SubtitleTaskVideoWithTtsFileName),
		filepath.Join(basePath, types.SubtitleTaskTransferredVerticalVideoFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName),
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func requireWorkflowArtifact(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("không tìm thấy %s: %w", label, err)
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("%s không hợp lệ hoặc rỗng", label)
	}
	return nil
}

func validateWorkflowDubbingRequest(req dto.StartWorkflowDubbingReq) error {
	if strings.EqualFold(strings.TrimSpace(config.Conf.Tts.Provider), "omnivoice") {
		if strings.TrimSpace(req.TtsVoiceCloneSrcFileUrl) == "" {
			return errors.New("OmniVoice yêu cầu chọn audio mẫu cho job này")
		}
		if !req.VoiceCloneConsent {
			return errors.New("cần xác nhận quyền sử dụng audio mẫu trước khi clone giọng")
		}
		if err := config.ValidateRemoteOmniVoiceWorker(); err != nil {
			return err
		}
		if config.Conf.Tts.Omnivoice.RequireCUDA {
			if _, err := omnivoice.ProbeColabGPUWithAPIKey(config.Conf.Tts.Omnivoice.BaseUrl, config.Conf.Tts.Omnivoice.SessionApiKey, 12*time.Second); err != nil {
				return fmt.Errorf("worker OmniVoice Colab chưa sẵn sàng: %w", err)
			}
		}
	}
	return nil
}

func (s Service) runWorkflowDubbingAudio(workflow *subtitleWorkflow, req dto.StartWorkflowDubbingReq) {
	task := workflow.task()
	step := workflow.stepParam(task)
	step.EnableTts = true
	step.TtsVoiceCode = strings.TrimSpace(req.TtsVoiceCode)
	if step.TtsVoiceCode == "" {
		step.TtsVoiceCode = "auto"
	}
	step.VoiceCloneConsent = req.VoiceCloneConsent
	if strings.EqualFold(strings.TrimSpace(config.Conf.Tts.Provider), "omnivoice") {
		step.VoiceCloneAudioUrl = strings.TrimPrefix(strings.TrimSpace(req.TtsVoiceCloneSrcFileUrl), "local:")
		if _, err := os.Stat(step.VoiceCloneAudioUrl); err != nil {
			s.failWorkflow(workflow, task, fmt.Errorf("không thể đọc audio mẫu OmniVoice: %w", err))
			return
		}
	} else if strings.TrimSpace(req.TtsVoiceCloneSrcFileUrl) != "" {
		s.failWorkflow(workflow, task, errors.New("chỉ OmniVoice Colab hỗ trợ clone bằng audio mẫu trong workflow này"))
		return
	}
	if err := s.synthesizeSRTToSpeech(context.Background(), step); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	task.ProcessPct = 84
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitDubbingAudio
	workflow.Message = "Đã tạo audio lồng tiếng. Hãy nghe kiểm tra rồi bấm Duyệt audio; video chưa được ghép."
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

// StartWorkflowDubbingVideo starts only muxing of the already approved audio
// with the source video. It deliberately has no TTS request payload, so no
// clone reference can be re-used or sent to a worker at this stage.
func (s Service) StartWorkflowDubbingVideo(taskID string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowDubbingVideoRunning
	allowed := workflow.TranslationApproved && workflow.DubbingRequested && workflow.DubbingAudioApproved &&
		(workflow.CurrentStage == workflowDubbingAudioApproved || retrying)
	if allowed {
		workflow.CurrentStage = workflowDubbingVideoRunning
		workflow.Message = "Đang ghép audio đã duyệt vào video nguồn."
		workflow.FailureReason = ""
		workflow.FailedStage = ""
		workflow.DubbingVideoApproved = false
		workflow.DubbingApproved = false
	}
	workflow.mu.Unlock()
	if !allowed {
		return nil, errors.New("hãy duyệt audio lồng tiếng trước khi ghép video")
	}
	if err := clearWorkflowDubbedVideoArtifacts(workflow.TaskBasePath); err != nil {
		s.failWorkflow(workflow, workflow.task(), fmt.Errorf("cannot clear previous dubbed video artifacts: %w", err))
		return nil, err
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	go s.runWorkflowDubbingVideo(workflow)
	return workflowSnapshot(workflow), nil
}

func (s Service) runWorkflowDubbingVideo(workflow *subtitleWorkflow) {
	task := workflow.task()
	step := workflow.stepParam(task)
	if err := s.muxDubbedAudioVideo(step); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	task.ProcessPct = 92
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitDubbingVideo
	workflow.Message = "Đã ghép video lồng tiếng. Hãy kiểm tra video rồi bấm Duyệt video lồng tiếng."
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

func (s Service) StartWorkflowRender(taskID string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowRenderRunning
	allowed := workflow.TranslationApproved && (workflow.CurrentStage == workflowTranslationApproved || workflow.CurrentStage == workflowDubbingVideoApproved || retrying)
	if workflow.DubbingRequested {
		allowed = allowed && workflow.DubbingVideoApproved && workflow.CurrentStage == workflowDubbingVideoApproved
	}
	if allowed {
		workflow.CurrentStage = workflowRenderRunning
		workflow.Message = "Đang render video từ đầu ra đã được duyệt."
		workflow.FailureReason = ""
		workflow.FailedStage = ""
	}
	workflow.mu.Unlock()
	if !allowed {
		return nil, errors.New("hãy duyệt bản dịch, và duyệt audio nếu đã bật lồng tiếng, trước khi render")
	}
	if strings.TrimSpace(workflow.EmbedType) == "" || workflow.EmbedType == "none" {
		workflow.mu.Lock()
		workflow.CurrentStage = workflowTranslationApproved
		workflow.mu.Unlock()
		_ = persistWorkflow(workflow)
		return nil, errors.New("hãy bật xuất video có phụ đề ở bước 04 trước khi render")
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	go s.runWorkflowRender(workflow)
	return workflowSnapshot(workflow), nil
}

func (s Service) runWorkflowRender(workflow *subtitleWorkflow) {
	task := workflow.task()
	step := workflow.stepParam(task)
	workflow.mu.Lock()
	step.EnableTts = workflow.DubbingRequested && workflow.DubbingVideoApproved
	workflow.mu.Unlock()
	if err := s.embedSubtitles(context.Background(), step); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	task.ProcessPct = 100
	task.Status = types.SubtitleTaskStatusSuccess
	workflow.mu.Lock()
	workflow.CurrentStage = workflowCompleted
	workflow.Message = "Đã render xong. Từng artifact có thể tải ở bước 05."
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

func (s Service) ApproveWorkflowStage(taskID, stage string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	task := workflow.task()
	workflow.mu.Lock()
	var approveErr error
	switch stage {
	case "source":
		if workflow.CurrentStage != workflowAwaitSourceReview {
			approveErr = errors.New("phụ đề gốc chưa sẵn sàng để duyệt")
		} else if _, err := workflowSRTBlocks(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)); err != nil {
			approveErr = err
		} else {
			workflow.SourceApproved = true
			workflow.CurrentStage = workflowSourceApproved
			workflow.Message = "Nguồn đã duyệt. Bạn có thể bắt đầu dịch khi sẵn sàng."
			task.ProcessPct = 45
		}
	case "translation":
		if workflow.CurrentStage != workflowAwaitTranslation {
			approveErr = errors.New("bản dịch chưa sẵn sàng để duyệt")
		} else if err := s.synchronizeWorkflowTranslationArtifacts(workflow); err != nil {
			approveErr = err
		} else {
			workflow.TranslationApproved = true
			workflow.CurrentStage = workflowTranslationApproved
			workflow.Message = "Bản dịch đã duyệt. Bạn có thể tạo audio hoặc render video phụ đề."
			task.ProcessPct = 75
		}
	case "dubbing", "dubbing_audio":
		if workflow.CurrentStage != workflowAwaitDubbingAudio {
			approveErr = errors.New("audio lồng tiếng chưa sẵn sàng để duyệt")
		} else if err := requireWorkflowArtifact(filepath.Join(workflow.TaskBasePath, types.TtsResultAudioFileName), "audio lồng tiếng"); err != nil {
			approveErr = err
		} else {
			workflow.DubbingAudioApproved = true
			workflow.DubbingVideoApproved = false
			workflow.DubbingApproved = false
			workflow.CurrentStage = workflowDubbingAudioApproved
			workflow.Message = "Audio đã duyệt. Bạn có thể bắt đầu ghép video lồng tiếng khi sẵn sàng."
			task.ProcessPct = 86
		}
	case "dubbing_video":
		if workflow.CurrentStage != workflowAwaitDubbingVideo {
			approveErr = errors.New("video lồng tiếng chưa sẵn sàng để duyệt")
		} else if err := requireWorkflowArtifact(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskVideoWithTtsFileName), "video lồng tiếng"); err != nil {
			approveErr = err
		} else {
			workflow.DubbingVideoApproved = true
			workflow.DubbingApproved = false
			workflow.CurrentStage = workflowDubbingVideoApproved
			workflow.Message = "Video lồng tiếng đã duyệt. Bạn có thể xuất MP4 cuối khi sẵn sàng."
			task.ProcessPct = 94
		}
	default:
		approveErr = errors.New("bước duyệt không hợp lệ")
	}
	workflow.mu.Unlock()
	if approveErr != nil {
		return nil, approveErr
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	return workflowSnapshot(workflow), nil
}

func (s Service) UpdateWorkflowSubtitle(taskID, kind, content string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	content = strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if content == "" {
		return nil, errors.New("nội dung SRT không được để trống")
	}
	if err := validateWorkflowSRT(content, workflow.TaskBasePath); err != nil {
		return nil, err
	}
	if kind == "translated" {
		if err := validateWorkflowTargetAlignment(workflow, content); err != nil {
			return nil, err
		}
	}
	workflow.mu.Lock()
	var path string
	switch kind {
	case "source":
		path = filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
		workflow.SourceApproved = false
		workflow.TranslationApproved = false
		workflow.TranslationWarnings = nil
		workflow.DubbingRequested = false
		workflow.DubbingAudioApproved = false
		workflow.DubbingVideoApproved = false
		workflow.DubbingApproved = false
		workflow.CurrentStage = workflowAwaitSourceReview
		workflow.Message = "Đã lưu phụ đề gốc. Hãy duyệt lại nguồn trước khi dịch."
		workflow.SourceRevision++
		workflow.TranslationRevision = 0
		workflow.FailureReason = ""
	case "translated":
		path = filepath.Join(workflow.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName)
		workflow.TranslationApproved = false
		workflow.DubbingRequested = false
		workflow.DubbingAudioApproved = false
		workflow.DubbingVideoApproved = false
		workflow.DubbingApproved = false
		workflow.CurrentStage = workflowAwaitTranslation
		workflow.Message = "Đã lưu bản dịch. Hãy duyệt lại bản dịch trước khi tạo audio/render."
		workflow.TranslationRevision++
		workflow.FailureReason = ""
	default:
		workflow.mu.Unlock()
		return nil, errors.New("loại SRT không hợp lệ")
	}
	workflow.mu.Unlock()
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		return nil, err
	}
	if kind == "source" {
		invalidateWorkflowOutputs(workflow.TaskBasePath, true)
		blocks, parseErr := workflowSRTBlocks(path)
		if parseErr == nil {
			_ = writeWorkflowText(filepath.Join(workflow.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName), blocks, false)
		}
	} else {
		invalidateWorkflowOutputs(workflow.TaskBasePath, false)
		if err := s.synchronizeWorkflowTranslationArtifacts(workflow); err != nil {
			return nil, err
		}
		warnings, warningErr := translationReviewWarningsFromWorkflow(workflow)
		if warningErr != nil {
			// The edited SRT has already passed syntax and timing validation.
			// Warning extraction is advisory, so it must never prevent a user from
			// saving the review draft.
			log.GetLogger().Warn("could not refresh translation review warnings", zap.Error(warningErr))
		} else {
			workflow.mu.Lock()
			workflow.TranslationWarnings = warnings
			workflow.Message = translationReviewMessage(warnings)
			workflow.mu.Unlock()
		}
	}
	task := workflow.task()
	task.Status = types.SubtitleTaskStatusProcessing
	if kind == "source" {
		task.ProcessPct = 35
	} else {
		task.ProcessPct = 70
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	return workflowSnapshot(workflow), nil
}

func validateWorkflowSRT(content, workdir string) error {
	file, err := os.CreateTemp(workdir, "kova-srt-review-*.srt")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	blocks, err := workflowSRTBlocks(tempPath)
	if err != nil {
		return fmt.Errorf("SRT không hợp lệ, chưa lưu: %w", err)
	}
	if len(blocks) == 0 {
		return errors.New("SRT phải có ít nhất một cue")
	}
	return nil
}

func invalidateWorkflowOutputs(basePath string, sourceChanged bool) {
	// These are fixed, task-owned derived artifacts. Removing them prevents a
	// stale translated/audio/rendered file from being mistaken for a new user
	// approved result after an edit. Source video and source SRT are retained.
	paths := []string{
		filepath.Join(basePath, types.SubtitleTaskBilingualSrtFileName),
		filepath.Join(basePath, types.SubtitleTaskShortOriginMixedSrtFileName),
		filepath.Join(basePath, types.TtsResultAudioFileName),
		filepath.Join(basePath, types.SubtitleTaskVideoWithTtsFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskTargetLanguageTextFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName),
		filepath.Join(basePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName),
	}
	if sourceChanged {
		paths = append(paths,
			filepath.Join(basePath, types.SubtitleTaskTargetLanguageSrtFileName),
			filepath.Join(basePath, "output", types.SubtitleTaskOriginLanguageTextFileName),
		)
	}
	for _, path := range paths {
		_ = os.Remove(path)
	}
	// The timing plan and dubbing SRT are also derived from the edited text;
	// do not expose them as reviewable output once their source changed.
	_ = os.RemoveAll(filepath.Join(basePath, dubbing.DubbingDirName))
}

// validateWorkflowTargetAlignment keeps an edited translation tied to the
// reviewed source timing.  Users may change the Vietnamese wording, but a
// target cue cannot silently add/remove/re-time a line that the dubbing and
// renderer will associate with a different source cue.
func validateWorkflowTargetAlignment(workflow *subtitleWorkflow, content string) error {
	file, err := os.CreateTemp(workflow.TaskBasePath, "kova-target-review-*.srt")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	target, err := workflowSRTBlocks(tempPath)
	if err != nil {
		return fmt.Errorf("SRT bản dịch không hợp lệ: %w", err)
	}
	source, err := workflowSRTBlocks(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName))
	if err != nil {
		return fmt.Errorf("không thể đối chiếu SRT nguồn: %w", err)
	}
	return validateWorkflowCueAlignment(source, target)
}

func validateWorkflowCueAlignment(source, target []*util.SrtBlock) error {
	if len(source) != len(target) {
		return fmt.Errorf("SRT bản dịch có %d cue, không khớp %d cue của SRT nguồn", len(target), len(source))
	}
	for index := range source {
		if source[index].Index != target[index].Index || source[index].Timestamp != target[index].Timestamp {
			return fmt.Errorf("cue %d của bản dịch phải giữ nguyên số thứ tự và timestamp của SRT nguồn", index+1)
		}
	}
	return nil
}

// synchronizeWorkflowTranslationArtifacts rebuilds all derived subtitle
// files after a user edits the translated SRT. This prevents a stale
// bilingual/vertical subtitle file from being rendered after review.
func (s Service) synchronizeWorkflowTranslationArtifacts(workflow *subtitleWorkflow) error {
	if s.YouTubeSubtitleSrv == nil {
		return ErrYouTubeSubtitleServiceNotInitialized
	}
	source, err := workflowSRTBlocks(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName))
	if err != nil {
		return err
	}
	target, err := workflowSRTBlocks(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName))
	if err != nil {
		return err
	}
	if err := validateWorkflowCueAlignment(source, target); err != nil {
		return err
	}
	for index := range source {
		source[index].TargetLanguageSentence = target[index].OriginLanguageSentence
	}
	bilingual := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskBilingualSrtFileName)
	if err := s.YouTubeSubtitleSrv.writeBilingualSrtFile(source, bilingual, workflow.TargetFirst); err != nil {
		return err
	}
	short := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskShortOriginMixedSrtFileName)
	data, err := os.ReadFile(bilingual)
	if err != nil {
		return err
	}
	if err := os.WriteFile(short, data, 0644); err != nil {
		return err
	}
	return writeWorkflowText(filepath.Join(workflow.TaskBasePath, "output", types.SubtitleTaskTargetLanguageTextFileName), source, true)
}

func translationReviewWarningsFromWorkflow(workflow *subtitleWorkflow) ([]dto.TranslationWarning, error) {
	if workflow == nil {
		return nil, errors.New("workflow is nil")
	}
	source, err := workflowSRTBlocks(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName))
	if err != nil {
		return nil, err
	}
	target, err := workflowSRTBlocks(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName))
	if err != nil {
		return nil, err
	}
	if err := validateWorkflowCueAlignment(source, target); err != nil {
		return nil, err
	}
	for index := range source {
		source[index].TargetLanguageSentence = target[index].OriginLanguageSentence
	}
	return translationReviewWarnings(source, workflow.OriginLanguage, workflow.TargetLanguage), nil
}

func (s Service) failWorkflow(workflow *subtitleWorkflow, task *types.SubtitleTask, err error) {
	if task != nil {
		task.Status = types.SubtitleTaskStatusFailed
		task.FailReason = err.Error()
	}
	workflow.failActiveSourceStep(err.Error())
	workflow.mu.Lock()
	failedStage := workflow.CurrentStage
	workflow.CurrentStage = workflowFailed
	workflow.FailedStage = failedStage
	workflow.FailureReason = err.Error()
	workflow.Message = "Bước hiện tại thất bại. Bạn có thể kiểm tra/sửa và chạy lại đúng bước đó."
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

func workflowSnapshot(workflow *subtitleWorkflow) *dto.SubtitleWorkflowData {
	workflow.mu.Lock()
	stage := workflow.CurrentStage
	message := workflow.Message
	failure := workflow.FailureReason
	taskID := workflow.TaskID
	sourceURL := workflow.URL
	basePath := workflow.TaskBasePath
	dubbingRequested := workflow.DubbingRequested
	sourceApproved := workflow.SourceApproved
	translationApproved := workflow.TranslationApproved
	dubbingAudioApproved := workflow.DubbingAudioApproved
	dubbingVideoApproved := workflow.DubbingVideoApproved
	failedStage := workflow.FailedStage
	sourceMethod := workflow.SourceMethod
	sourceSteps := sourceStepsForSnapshot(workflow.SourceSteps, sourceMethod, stage, basePath, failure)
	translationWarnings := cloneTranslationWarnings(workflow.TranslationWarnings)
	workflow.mu.Unlock()
	task := workflow.task()
	processPercent := task.ProcessPct
	if processPercent == 0 {
		processPercent = workflowStageProgress(stage)
	}
	data := &dto.SubtitleWorkflowData{
		TaskId:              taskID,
		SourceUrl:           sourceURL,
		CurrentStage:        stage,
		ProcessPercent:      processPercent,
		Message:             message,
		FailureReason:       failure,
		SourceSteps:         sourceSteps,
		TranslationWarnings: translationWarnings,
		Artifacts:           workflowArtifacts(taskID, basePath),
		CanStart:            map[string]bool{},
		ReviewRequired:      strings.HasPrefix(stage, "awaiting_"),
	}
	data.SourceSrtUrl = existingWorkflowDownload(filepath.Join(basePath, types.SubtitleTaskOriginLanguageSrtFileName))
	data.TranslatedSrtUrl = existingWorkflowDownload(filepath.Join(basePath, types.SubtitleTaskTargetLanguageSrtFileName))
	data.BilingualSrtUrl = existingWorkflowDownload(filepath.Join(basePath, types.SubtitleTaskBilingualSrtFileName))
	data.SourceTextUrl = existingWorkflowDownload(filepath.Join(basePath, "output", types.SubtitleTaskOriginLanguageTextFileName))
	data.TranslatedTextUrl = existingWorkflowDownload(filepath.Join(basePath, "output", types.SubtitleTaskTargetLanguageTextFileName))
	data.CanStart["source"] = false
	data.CanStart["source_approve"] = stage == workflowAwaitSourceReview
	data.CanStart["translation"] = sourceApproved && (stage == workflowSourceApproved || (stage == workflowFailed && failedStage == workflowTranslationRunning))
	data.CanStart["translation_approve"] = stage == workflowAwaitTranslation
	// Keep the two old generic keys as aliases for integrations that have not
	// yet adopted the explicit audio endpoint. They mean audio synthesis and
	// audio approval only; no API path can auto-mux a video from these keys.
	audioStart := translationApproved && (stage == workflowTranslationApproved || (stage == workflowFailed && failedStage == workflowDubbingAudioRunning))
	audioApprove := stage == workflowAwaitDubbingAudio
	videoStart := translationApproved && dubbingRequested && dubbingAudioApproved && (stage == workflowDubbingAudioApproved || (stage == workflowFailed && failedStage == workflowDubbingVideoRunning))
	videoApprove := stage == workflowAwaitDubbingVideo
	data.CanStart["dubbing"] = audioStart
	data.CanStart["dubbing_audio"] = audioStart
	data.CanStart["dubbing_approve"] = audioApprove
	data.CanStart["dubbing_audio_approve"] = audioApprove
	data.CanStart["dubbing_video"] = videoStart
	data.CanStart["dubbing_video_approve"] = videoApprove
	data.CanStart["dubbing_skip"] = translationApproved && canSkipWorkflowDubbing(stage, failedStage)
	data.CanStart["render"] = translationApproved && (stage == workflowTranslationApproved || (dubbingRequested && dubbingVideoApproved && stage == workflowDubbingVideoApproved) || (stage == workflowFailed && failedStage == workflowRenderRunning))
	return data
}

// workflowStageProgress supplies a stable progress milestone after the
// desktop/server restarts. SubtitleTask progress lives only in memory, while
// the workflow stage is persisted on disk.
func workflowStageProgress(stage string) uint8 {
	switch stage {
	case workflowSourceRunning:
		return 1
	case workflowAwaitSourceReview, workflowSourceApproved, workflowTranslationRunning:
		return 35
	case workflowAwaitTranslation:
		return 60
	case workflowTranslationApproved, workflowDubbingAudioRunning:
		return 75
	case workflowAwaitDubbingAudio:
		return 84
	case workflowDubbingAudioApproved, workflowDubbingVideoRunning:
		return 86
	case workflowAwaitDubbingVideo:
		return 92
	case workflowDubbingVideoApproved, workflowRenderRunning:
		return 94
	case workflowCompleted:
		return 100
	default:
		return 0
	}
}

func existingWorkflowDownload(path string) string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return artifactDownloadURL(path)
	}
	return ""
}

func workflowArtifacts(taskID, basePath string) []dto.WorkflowArtifact {
	type candidate struct {
		kind, label, path string
	}
	candidates := []candidate{
		{"source_video", "01 · Video nguồn / Source video", filepath.Join(basePath, types.SubtitleTaskVideoFileName)},
		{"source_srt", "02 · Phụ đề gốc / Original SRT", filepath.Join(basePath, types.SubtitleTaskOriginLanguageSrtFileName)},
		{"source_text", "02b · Script gốc / Original script", filepath.Join(basePath, "output", types.SubtitleTaskOriginLanguageTextFileName)},
		{"translated_srt", "03 · Phụ đề tiếng Việt / Vietnamese SRT", filepath.Join(basePath, types.SubtitleTaskTargetLanguageSrtFileName)},
		{"dubbed_audio", "04 · Âm thanh lồng tiếng / Dubbed audio", filepath.Join(basePath, types.TtsResultAudioFileName)},
		{"dubbed_video", "05 · Video đã lắp âm thanh / Video with dubbed audio", filepath.Join(basePath, types.SubtitleTaskVideoWithTtsFileName)},
		{"subtitled_horizontal_video", "06 · Video cuối có phụ đề / Final subtitled video", filepath.Join(basePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName)},
		{"subtitled_vertical_video", "07 · Video cuối dọc có phụ đề / Vertical final video", filepath.Join(basePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName)},
		{"source_audio", "08 · Audio nguồn / Source audio", filepath.Join(basePath, types.SubtitleTaskAudioFileName)},
		{"bilingual_srt", "09 · Phụ đề song ngữ / Bilingual SRT", filepath.Join(basePath, types.SubtitleTaskBilingualSrtFileName)},
		{"dubbing_srt", "10 · Phụ đề dùng để lồng tiếng / Dubbing SRT", filepath.Join(basePath, dubbing.DubbingDirName, dubbing.DubSubtitleFileName)},
		{"dubbing_report", "11 · Báo cáo khớp thời lượng / Dubbing timing report", filepath.Join(basePath, dubbing.DubbingDirName, dubbing.DubbingReportName)},
		{"translated_text", "12 · Nội dung đã dịch / Translated text", filepath.Join(basePath, "output", types.SubtitleTaskTargetLanguageTextFileName)},
	}
	artifacts := make([]dto.WorkflowArtifact, 0, len(candidates))
	for _, item := range candidates {
		if info, err := os.Stat(item.path); err == nil && !info.IsDir() {
			artifacts = append(artifacts, dto.WorkflowArtifact{
				Kind:        item.kind,
				Label:       item.label,
				Name:        filepath.Base(item.path),
				DownloadUrl: artifactDownloadURL(item.path),
			})
		}
	}
	return artifacts
}

func (s Service) GetWorkflow(taskID string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	return workflowSnapshot(workflow), nil
}
