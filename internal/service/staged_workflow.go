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
	"kova/internal/capcutstudio"
	"kova/internal/deps"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	workflowStateFileName       = "workflow_state.json"
	dubbingHeartbeatTimeout     = 90 * time.Second
	stalledDubbingFailureReason = "Tạo audio không có cập nhật tiến độ trong hơn 90 giây. KOVA đã dừng job treo; hãy chạy lại bước 03 từ SRT đã duyệt."
)

const (
	sourceMethodSpeechToText       = "speech_to_text"
	sourceMethodVisualOCR          = "visual_ocr"
	sourceMethodSpeechToTextAndOCR = "speech_to_text_and_visual_ocr"
	ocrEngineColab                 = "colab"
	ocrEngineLocal                 = "local"
	reviewModeManual               = "manual"
	reviewModeAuto                 = "auto"
	sourceCookieBrowserAuto        = "auto"
	sourceCookieBrowserNone        = "none"
	sourceCookieBrowserChrome      = "chrome"
	sourceCookieBrowserEdge        = "edge"
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

	TaskID       string `json:"task_id"`
	TaskBasePath string `json:"task_base_path"`
	URL          string `json:"url"`
	SourceMethod string `json:"source_method"`
	ReviewMode   string `json:"review_mode"`
	// This stores only the temporary-browser selection, never a browser cookie
	// or profile secret. It lets a retried source stage preserve the user's
	// download preference without retaining credentials in workflow_state.json.
	SourceCookieBrowser string `json:"source_cookie_browser,omitempty"`
	OCREngine           string `json:"ocr_engine,omitempty"`
	OCRWorkerURL        string `json:"ocr_worker_url,omitempty"`
	// A Colab token lasts only for the current desktop process and must never
	// be retained in workflow_state.json or project data.
	OCRWorkerToken string           `json:"-"`
	OCRLanguage    string           `json:"ocr_language,omitempty"`
	OCRRegion      visualocr.Region `json:"ocr_region,omitempty"`
	OCRIntervalMS  int              `json:"ocr_sample_interval_ms,omitempty"`
	OCRPreferGPU   bool             `json:"ocr_prefer_gpu"`
	SourceWarning  string           `json:"source_warning,omitempty"`

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
	BlurOldText    bool     `json:"blur_old_text"`
	BlurRegionX    float64  `json:"blur_region_x,omitempty"`
	BlurRegionY    float64  `json:"blur_region_y,omitempty"`
	BlurRegionW    float64  `json:"blur_region_width,omitempty"`
	BlurRegionH    float64  `json:"blur_region_height,omitempty"`
	BlurStrength   int      `json:"blur_strength,omitempty"`

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
	DubbingApproved                  bool                       `json:"dubbing_approved,omitempty"`
	SourceRevision                   int                        `json:"source_revision"`
	TranslationRevision              int                        `json:"translation_revision"`
	SourceSteps                      []dto.WorkflowProgressStep `json:"source_steps,omitempty"`
	TranslationSteps                 []dto.WorkflowProgressStep `json:"translation_steps,omitempty"`
	DubbingSteps                     []dto.WorkflowProgressStep `json:"dubbing_steps,omitempty"`
	RenderSteps                      []dto.WorkflowProgressStep `json:"render_steps,omitempty"`
	TranslationWarnings              []dto.TranslationWarning   `json:"translation_warnings,omitempty"`
	RenderStartedAt                  string                     `json:"render_started_at,omitempty"`
	RenderEstimatedCompletionAt      string                     `json:"render_estimated_completion_at,omitempty"`
	RenderCompletedAt                string                     `json:"render_completed_at,omitempty"`
	TranslationStartedAt             string                     `json:"translation_started_at,omitempty"`
	TranslationEstimatedCompletionAt string                     `json:"translation_estimated_completion_at,omitempty"`
	TranslationCompletedAt           string                     `json:"translation_completed_at,omitempty"`
	UpdatedAt                        string                     `json:"updated_at"`
}

func initialSourceSteps() []dto.WorkflowProgressStep {
	// Stage 01 is intentionally media-only.  Script extraction belongs to
	// Stage 02 so a completed video download remains useful even if an STT or
	// OCR worker is unavailable later.
	return []dto.WorkflowProgressStep{
		{ID: "download_video", State: "pending", Percent: 0},
		{ID: "download_audio", State: "pending", Percent: 0},
	}
}

func initialTranslationStepsFor(sourceMethod string) []dto.WorkflowProgressStep {
	// Download progress belongs exclusively to stage 01. Stage 02 begins with
	// the selected script extractors and never presents a second, ambiguous
	// "prepare video" row after the source video is already approved.
	steps := make([]dto.WorkflowProgressStep, 0, 7)
	switch normalizeWorkflowSourceMethod(sourceMethod) {
	case sourceMethodVisualOCR:
		steps = append(steps, dto.WorkflowProgressStep{ID: "visual_ocr", State: "pending", Percent: 0})
	case sourceMethodSpeechToTextAndOCR:
		steps = append(steps,
			dto.WorkflowProgressStep{ID: "separate_audio", State: "pending", Percent: 0},
			dto.WorkflowProgressStep{ID: "speech_to_text", State: "pending", Percent: 0},
			dto.WorkflowProgressStep{ID: "visual_ocr", State: "pending", Percent: 0},
		)
	default:
		steps = append(steps,
			dto.WorkflowProgressStep{ID: "separate_audio", State: "pending", Percent: 0},
			dto.WorkflowProgressStep{ID: "speech_to_text", State: "pending", Percent: 0},
		)
	}
	steps = append(steps,
		dto.WorkflowProgressStep{ID: "source_srt", State: "pending", Percent: 0},
		dto.WorkflowProgressStep{ID: "translation_prepare", State: "pending", Percent: 0},
		dto.WorkflowProgressStep{ID: "translation_model", State: "pending", Percent: 0},
		dto.WorkflowProgressStep{ID: "translation_write", State: "pending", Percent: 0},
	)
	return steps
}

func initialDubbingSteps() []dto.WorkflowProgressStep {
	return []dto.WorkflowProgressStep{
		{ID: "prepare", State: "pending", Percent: 0},
		{ID: "synthesize", State: "pending", Percent: 0},
		{ID: "fit", State: "pending", Percent: 0},
		{ID: "assemble", State: "pending", Percent: 0},
	}
}

// initialRenderSteps deliberately models the expensive final stage instead
// of presenting one opaque 94% bar.  Each item is independently observable
// and therefore a user can tell whether KOVA is preparing media, producing
// ASS subtitles, encoding, or just verifying the output file.
func initialRenderSteps() []dto.WorkflowProgressStep {
	return []dto.WorkflowProgressStep{
		{ID: "render_preflight", State: "pending", Percent: 0},
		{ID: "render_subtitle", State: "pending", Percent: 0},
		{ID: "render_encode", State: "pending", Percent: 0},
		{ID: "render_verify", State: "pending", Percent: 0},
	}
}

func normalizeWorkflowSourceMethod(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case sourceMethodVisualOCR:
		return sourceMethodVisualOCR
	case sourceMethodSpeechToTextAndOCR:
		return sourceMethodSpeechToTextAndOCR
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
	case sourceMethodSpeechToTextAndOCR:
		return sourceMethodSpeechToTextAndOCR, nil
	default:
		return "", fmt.Errorf("source_method không hỗ trợ: %s", raw)
	}
}

func normalizeWorkflowSourceCookieBrowser(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", sourceCookieBrowserAuto:
		return sourceCookieBrowserAuto, nil
	case sourceCookieBrowserNone:
		return sourceCookieBrowserNone, nil
	case sourceCookieBrowserChrome:
		return sourceCookieBrowserChrome, nil
	case sourceCookieBrowserEdge:
		return sourceCookieBrowserEdge, nil
	default:
		return "", fmt.Errorf("source_cookie_browser khÃ´ng há»— trá»£: %s", raw)
	}
}

func normalizeWorkflowOCREngine(raw string, sourceMethod string) (string, error) {
	if !workflowUsesOCR(sourceMethod) {
		return "", nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ocrEngineColab:
		return ocrEngineColab, nil
	case ocrEngineLocal:
		return ocrEngineLocal, nil
	default:
		return "", errors.New("OCR engine phải là Google Colab hoặc local")
	}
}

func normalizeWorkflowReviewMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), reviewModeAuto) {
		return reviewModeAuto
	}
	return reviewModeManual
}

func workflowUsesSTT(method string) bool {
	method = normalizeWorkflowSourceMethod(method)
	return method == sourceMethodSpeechToText || method == sourceMethodSpeechToTextAndOCR
}

func workflowUsesOCR(method string) bool {
	method = normalizeWorkflowSourceMethod(method)
	return method == sourceMethodVisualOCR || method == sourceMethodSpeechToTextAndOCR
}

func cloneSourceSteps(steps []dto.WorkflowProgressStep) []dto.WorkflowProgressStep {
	return append([]dto.WorkflowProgressStep(nil), steps...)
}

func cloneWorkflowSteps(steps []dto.WorkflowProgressStep) []dto.WorkflowProgressStep {
	return append([]dto.WorkflowProgressStep(nil), steps...)
}

func cloneTranslationWarnings(warnings []dto.TranslationWarning) []dto.TranslationWarning {
	result := make([]dto.TranslationWarning, 0, len(warnings))
	for _, warning := range warnings {
		// Always serialize this as an array. Older saved workflows can have a
		// nil slice here; sending that as JSON null used to crash the desktop
		// renderer when it listed the suspect words.
		warning.SuspiciousWords = append(make([]string, 0, len(warning.SuspiciousWords)), warning.SuspiciousWords...)
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
		w.SourceSteps = initialSourceSteps()
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

func (w *subtitleWorkflow) updateDubbingStep(id string, percent uint8, detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if len(w.DubbingSteps) == 0 {
		w.DubbingSteps = initialDubbingSteps()
	}
	for index := range w.DubbingSteps {
		if w.DubbingSteps[index].ID != id {
			continue
		}
		if percent > 100 {
			percent = 100
		}
		w.DubbingSteps[index].Percent = percent
		w.DubbingSteps[index].Detail = detail
		if percent >= 100 {
			w.DubbingSteps[index].State = "completed"
		} else {
			w.DubbingSteps[index].State = "running"
		}
		break
	}
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	w.mu.Unlock()
}

// updateTranslationStep persists cue-level translation progress independently
// from source, dubbing, and render. The ETA is conservative and is calculated
// only after at least one cue has completed.
func (w *subtitleWorkflow) updateTranslationStep(id string, percent uint8, detail string, estimatedCompletion time.Time) {
	if w == nil {
		return
	}
	if percent > 100 {
		percent = 100
	}
	w.mu.Lock()
	if len(w.TranslationSteps) == 0 {
		w.TranslationSteps = initialTranslationStepsFor(w.SourceMethod)
	}
	for index := range w.TranslationSteps {
		if w.TranslationSteps[index].ID != id {
			continue
		}
		w.TranslationSteps[index].Percent = percent
		w.TranslationSteps[index].Detail = detail
		if percent >= 100 {
			w.TranslationSteps[index].State = "completed"
		} else {
			w.TranslationSteps[index].State = "running"
		}
		break
	}
	if !estimatedCompletion.IsZero() {
		w.TranslationEstimatedCompletionAt = estimatedCompletion.UTC().Format(time.RFC3339Nano)
	}
	w.Message = detail
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Unlock()
}

func (w *subtitleWorkflow) finishTranslationProgress(detail string) {
	if w == nil {
		return
	}
	w.updateTranslationStep("translation_write", 100, detail, time.Time{})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Lock()
	w.TranslationEstimatedCompletionAt = ""
	w.TranslationCompletedAt = now
	w.UpdatedAt = now
	w.mu.Unlock()
}

// failTranslationStep records a branch-local failure without failing the
// entire translation stage. Combined STT + OCR runs independent workers, so
// one usable transcript may continue to the review SRT while the unavailable
// branch remains visibly failed for the user.
func (w *subtitleWorkflow) failTranslationStep(id, detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	for index := range w.TranslationSteps {
		if w.TranslationSteps[index].ID != id || w.TranslationSteps[index].State == "completed" {
			continue
		}
		w.TranslationSteps[index].State = "failed"
		w.TranslationSteps[index].Detail = detail
		break
	}
	w.Message = detail
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Unlock()
}

func (w *subtitleWorkflow) addSourceWarning(detail string) {
	if w == nil || strings.TrimSpace(detail) == "" {
		return
	}
	w.mu.Lock()
	if strings.TrimSpace(w.SourceWarning) == "" {
		w.SourceWarning = strings.TrimSpace(detail)
	} else if !strings.Contains(w.SourceWarning, strings.TrimSpace(detail)) {
		w.SourceWarning += " | " + strings.TrimSpace(detail)
	}
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Unlock()
}

func (w *subtitleWorkflow) updateRenderStep(id string, percent uint8, detail string, estimatedCompletion time.Time) {
	if w == nil {
		return
	}
	if percent > 100 {
		percent = 100
	}
	w.mu.Lock()
	if len(w.RenderSteps) == 0 {
		w.RenderSteps = initialRenderSteps()
	}
	for index := range w.RenderSteps {
		if w.RenderSteps[index].ID != id {
			continue
		}
		w.RenderSteps[index].Percent = percent
		w.RenderSteps[index].Detail = detail
		if percent >= 100 {
			w.RenderSteps[index].State = "completed"
		} else {
			w.RenderSteps[index].State = "running"
		}
		break
	}
	if !estimatedCompletion.IsZero() {
		w.RenderEstimatedCompletionAt = estimatedCompletion.UTC().Format(time.RFC3339Nano)
	}
	w.Message = detail
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Unlock()
	w.task().ProcessPct = workflowRenderProcessPercent(id, percent)
}

func (w *subtitleWorkflow) finishRenderProgress(detail string) {
	if w == nil {
		return
	}
	w.updateRenderStep("render_verify", 100, detail, time.Time{})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Lock()
	w.RenderEstimatedCompletionAt = ""
	w.RenderCompletedAt = now
	w.UpdatedAt = now
	w.mu.Unlock()
}

func (w *subtitleWorkflow) failActiveSourceStep(detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if len(w.SourceSteps) == 0 {
		w.SourceSteps = initialSourceSteps()
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

func (w *subtitleWorkflow) failActiveDubbingStep(detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	for index := range w.DubbingSteps {
		if w.DubbingSteps[index].State == "running" {
			w.DubbingSteps[index].State = "failed"
			w.DubbingSteps[index].Detail = detail
			break
		}
	}
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	w.mu.Unlock()
}

func (w *subtitleWorkflow) failActiveTranslationStep(detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	for index := range w.TranslationSteps {
		if w.TranslationSteps[index].State == "running" {
			w.TranslationSteps[index].State = "failed"
			w.TranslationSteps[index].Detail = detail
			break
		}
	}
	w.TranslationEstimatedCompletionAt = ""
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Unlock()
}

func (w *subtitleWorkflow) failActiveRenderStep(detail string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	for index := range w.RenderSteps {
		if w.RenderSteps[index].State == "running" {
			w.RenderSteps[index].State = "failed"
			w.RenderSteps[index].Detail = detail
			break
		}
	}
	w.RenderEstimatedCompletionAt = ""
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.mu.Unlock()
}

// sourceStepsForSnapshot backfills the media-only source stage for workflows
// created before source_steps was persisted. Script extraction is deliberately
// not inferred here: it is a Stage 02 responsibility.
func sourceStepsForSnapshot(steps []dto.WorkflowProgressStep, sourceMethod, stage, basePath, failure string) []dto.WorkflowProgressStep {
	if len(steps) > 0 {
		return cloneSourceSteps(steps)
	}
	if stage != workflowSourceRunning && stage != workflowAwaitSourceReview && stage != workflowFailed {
		return nil
	}
	_ = sourceMethod
	result := initialSourceSteps()
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
	if hasAudio {
		complete("download_audio", "Source audio downloaded")
	}
	if hasVideo {
		complete("download_video", "Source video downloaded")
	}
	if stage == workflowFailed {
		switch {
		case !hasAudio:
			set("download_audio", "failed", failure)
		case !hasVideo:
			set("download_video", "failed", failure)
		default:
			set("download_video", "failed", failure)
		}
		return result
	}
	if hasAudio && hasVideo {
		return result
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
		SourceCookieBrowser:         w.SourceCookieBrowser,
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
		TtsMixedAudioFilePath:       filepath.Join(w.TaskBasePath, types.TtsMixedAudioFileName),
		InputVideoPath:              filepath.Join(w.TaskBasePath, types.SubtitleTaskVideoFileName),
		SourceAudioFilePath:         filepath.Join(w.TaskBasePath, types.SubtitleTaskAudioFileName),
		VocalAudioFilePath:          filepath.Join(w.TaskBasePath, types.SubtitleTaskVocalAudioFileName),
		BackgroundAudioFilePath:     filepath.Join(w.TaskBasePath, types.SubtitleTaskBackgroundAudioFileName),
		VideoWithTtsFilePath:        filepath.Join(w.TaskBasePath, types.SubtitleTaskVideoWithTtsFileName),
		EmbedSubtitleVideoType:      w.EmbedType,
		VerticalVideoMajorTitle:     w.VerticalTitle,
		VerticalVideoMinorTitle:     w.VerticalSub,
		BlurOriginalText:            w.BlurOldText,
		BlurRegionX:                 w.BlurRegionX,
		BlurRegionY:                 w.BlurRegionY,
		BlurRegionWidth:             w.BlurRegionW,
		BlurRegionHeight:            w.BlurRegionH,
		BlurStrength:                w.BlurStrength,
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

// recoverStalledDubbingAudio makes a persisted, dead desktop worker
// retryable. A normal gateway request is bounded to 25 seconds and each
// ffmpeg fitting command is bounded to 60 seconds, so a 90-second missing
// heartbeat means this process is no longer producing observable work. This
// prevents an old pre-progress job from presenting itself as "running" for
// hours after the desktop application has been restarted.
func recoverStalledDubbingAudio(workflow *subtitleWorkflow, now time.Time) bool {
	if workflow == nil {
		return false
	}
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	if workflow.CurrentStage != workflowDubbingAudioRunning {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(workflow.UpdatedAt))
	if err != nil {
		updatedAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(workflow.UpdatedAt))
	}
	if err == nil && now.UTC().Sub(updatedAt.UTC()) <= dubbingHeartbeatTimeout {
		return false
	}
	if len(workflow.DubbingSteps) == 0 {
		workflow.DubbingSteps = initialDubbingSteps()
	}
	for index := range workflow.DubbingSteps {
		if workflow.DubbingSteps[index].State == "running" {
			workflow.DubbingSteps[index].State = "failed"
			workflow.DubbingSteps[index].Detail = stalledDubbingFailureReason
			break
		}
	}
	workflow.CurrentStage = workflowFailed
	workflow.FailedStage = workflowDubbingAudioRunning
	workflow.FailureReason = stalledDubbingFailureReason
	workflow.Message = "Job tạo audio đã bị dừng vì không còn heartbeat. Bạn có thể chạy lại bước 03 hoặc xóa dự án để thử luồng mới."
	workflow.UpdatedAt = now.UTC().Format(time.RFC3339)
	return true
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
	if !util.IsSupportedVideoURL(sourceURL) && !strings.HasPrefix(sourceURL, "local:") {
		return nil, errors.New("quy trình nguồn cần URL YouTube, TikTok, Douyin/Bilibili hoặc video local đã chọn")
	}
	if util.IsYouTubeURL(sourceURL) {
		if videoID, err := util.GetYouTubeID(sourceURL); err != nil || strings.TrimSpace(videoID) == "" {
			return nil, errors.New("URL YouTube không hợp lệ")
		}
	}
	if strings.HasPrefix(sourceURL, "local:") {
		localPath := strings.TrimSpace(strings.TrimPrefix(sourceURL, "local:"))
		if info, statErr := os.Stat(localPath); statErr != nil || info.IsDir() {
			if statErr == nil {
				statErr = errors.New("path is a directory")
			}
			return nil, fmt.Errorf("cannot read local source video: %w", statErr)
		}
	}
	sourceMethod, err := validateWorkflowSourceMethod(req.SourceMethod)
	if err != nil {
		return nil, err
	}
	sourceCookieBrowser, err := normalizeWorkflowSourceCookieBrowser(req.SourceCookieBrowser)
	if err != nil {
		return nil, err
	}
	ocrLanguage, ocrRegion, ocrInterval, ocrPreferGPU, err := normalizeWorkflowOCRRequest(req, sourceMethod)
	if err != nil {
		return nil, err
	}
	ocrEngine, err := normalizeWorkflowOCREngine(req.OCREngine, sourceMethod)
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
			TaskID:              taskID,
			TaskBasePath:        basePath,
			URL:                 sourceURL,
			SourceMethod:        sourceMethod,
			ReviewMode:          normalizeWorkflowReviewMode(req.ReviewMode),
			SourceCookieBrowser: sourceCookieBrowser,
			OCREngine:           ocrEngine,
			OCRWorkerURL:        strings.TrimSpace(req.OCRWorkerURL),
			OCRWorkerToken:      strings.TrimSpace(req.OCRWorkerToken),
			OCRLanguage:         ocrLanguage,
			OCRRegion:           ocrRegion,
			OCRIntervalMS:       ocrInterval,
			OCRPreferGPU:        ocrPreferGPU,
			OriginLanguage:      strings.TrimSpace(req.OriginLanguage),
			TargetLanguage:      strings.TrimSpace(req.TargetLang),
			UserLanguage:        strings.TrimSpace(req.Language),
			Bilingual:           req.Bilingual == types.SubtitleTaskBilingualYes,
			TargetFirst:         req.TranslationSubtitlePos == types.SubtitleTaskTranslationSubtitlePosTop,
			ModalFilter:         req.ModalFilter == types.SubtitleTaskModalFilterYes,
			ProtectedTerms:      append([]string(nil), req.ProtectTerms...),
			EmbedType:           strings.TrimSpace(req.EmbedSubtitleVideoType),
			VerticalTitle:       req.VerticalMajorTitle,
			VerticalSub:         req.VerticalMinorTitle,
			CurrentStage:        workflowSourceRunning,
			Message:             "Đang tải video nguồn để xem trước. Bước 02 sẽ tạo script bằng STT/OCR.",
			SourceRevision:      1,
			SourceSteps:         initialSourceSteps(),
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

// StartWorkflowSource downloads only source media. Script creation, translation,
// dubbing and rendering are deliberately deferred to their own explicit stages.
func (s Service) StartWorkflowSource(req dto.StartVideoSubtitleTaskReq) (*dto.SubtitleWorkflowData, error) {
	sourceMethod, err := validateWorkflowSourceMethod(req.SourceMethod)
	if err != nil {
		return nil, err
	}
	req.SourceMethod = sourceMethod
	workflow, err := createWorkflow(req)
	if err != nil {
		return nil, err
	}
	go s.runWorkflowSource(workflow)
	return workflowSnapshot(workflow), nil
}

// resolveSourceMethodWithOCRFallback makes the hybrid Auto mode resilient to
// an optional local OCR installation.  The user explicitly chose a timed STT
// backbone in this mode, so it remains a correct source of truth when OCR is
// unavailable.  OCR-only runs never fall back: without visible text there is
// no equivalent source requested by the user.
func resolveSourceMethodWithOCRFallback(sourceMethod, reviewMode string, allowFallback bool, preflight func() error) (string, string, error) {
	sourceMethod = normalizeWorkflowSourceMethod(sourceMethod)
	if !workflowUsesOCR(sourceMethod) {
		return sourceMethod, "", nil
	}
	if preflight == nil {
		return "", "", errors.New("Visual OCR preflight is unavailable")
	}
	if err := preflight(); err != nil {
		if sourceMethod == sourceMethodSpeechToTextAndOCR && reviewMode == reviewModeAuto && allowFallback {
			return sourceMethodSpeechToText,
				"Visual OCR không sẵn sàng trong Python local; KOVA bỏ qua OCR và tiếp tục tự động bằng Speech-to-Text. Bạn có thể cài Paddle/PaddleOCR sau nếu muốn chạy chế độ STT + OCR.",
				nil
		}
		return "", "", fmt.Errorf("Visual OCR is not ready before the video download: %w", err)
	}
	return sourceMethod, "", nil
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
	task.ProcessPct = 20
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitSourceReview
	workflow.Message = "Video và audio nguồn đã tải xong. Xem trước rồi duyệt để chuyển sang bước 02 tạo script bằng STT/OCR."
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
	if workflow.ReviewMode == reviewModeAuto {
		if _, err := s.ApproveWorkflowStage(workflow.TaskID, "source"); err != nil {
			s.failWorkflow(workflow, task, fmt.Errorf("không thể tự duyệt nguồn: %w", err))
		}
	}
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

// StartWorkflowTranslation preserves the no-body API for older clients. New
// desktop clients call StartWorkflowTranslationWithSource so script settings
// are selected in Stage 02, after the source video is already downloaded.
func (s Service) StartWorkflowTranslation(taskID string) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	return s.startWorkflowTranslation(workflow)
}

// StartWorkflowTranslationWithSource applies the script/STT/OCR configuration
// immediately before the script stage. Tokens remain in memory only and never
// enter workflow_state.json.
func (s Service) StartWorkflowTranslationWithSource(taskID string, req dto.StartVideoSubtitleTaskReq) (*dto.SubtitleWorkflowData, error) {
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	sourceMethod, err := validateWorkflowSourceMethod(req.SourceMethod)
	if err != nil {
		return nil, err
	}
	ocrLanguage, ocrRegion, ocrInterval, ocrPreferGPU, err := normalizeWorkflowOCRRequest(req, sourceMethod)
	if err != nil {
		return nil, err
	}
	ocrEngine, err := normalizeWorkflowOCREngine(req.OCREngine, sourceMethod)
	if err != nil {
		return nil, err
	}
	var sourceWarning string
	if workflowUsesOCR(sourceMethod) {
		var preflight func() error
		if ocrEngine == ocrEngineColab {
			preflight = func() error {
				_, healthErr := visualocr.CheckRemoteHealth(context.Background(), visualocr.RemoteConfig{BaseURL: req.OCRWorkerURL, Token: req.OCRWorkerToken})
				return healthErr
			}
		} else {
			runner := visualocr.Runner{Config: visualocr.Config{PythonPath: config.Conf.VisualOCR.PythonPath, ScriptPath: config.Conf.VisualOCR.ScriptPath}}
			preflight = func() error {
				_, healthErr := runner.Preflight(context.Background())
				return healthErr
			}
		}
		sourceMethod, sourceWarning, err = resolveSourceMethodWithOCRFallback(sourceMethod, normalizeWorkflowReviewMode(req.ReviewMode), req.OCRFallbackToSTT, preflight)
		if err != nil {
			return nil, err
		}
	}
	workflow.mu.Lock()
	workflow.SourceMethod = sourceMethod
	workflow.ReviewMode = normalizeWorkflowReviewMode(req.ReviewMode)
	workflow.OCREngine = ocrEngine
	workflow.OCRWorkerURL = strings.TrimSpace(req.OCRWorkerURL)
	workflow.OCRWorkerToken = strings.TrimSpace(req.OCRWorkerToken)
	workflow.OCRLanguage = ocrLanguage
	workflow.OCRRegion = ocrRegion
	workflow.OCRIntervalMS = ocrInterval
	workflow.OCRPreferGPU = ocrPreferGPU
	workflow.SourceWarning = sourceWarning
	if value := strings.TrimSpace(req.OriginLanguage); value != "" {
		workflow.OriginLanguage = value
	}
	if value := strings.TrimSpace(req.TargetLang); value != "" {
		workflow.TargetLanguage = value
	}
	workflow.mu.Unlock()
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	return s.startWorkflowTranslation(workflow)
}

func (s Service) startWorkflowTranslation(workflow *subtitleWorkflow) (*dto.SubtitleWorkflowData, error) {
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowTranslationRunning
	allowed := (workflow.CurrentStage == workflowSourceApproved || retrying) && workflow.SourceApproved
	if allowed {
		workflow.CurrentStage = workflowTranslationRunning
		workflow.Message = "Đang tạo script từ video nguồn, sau đó dịch thành phụ đề để bạn kiểm tra."
		workflow.FailureReason = ""
		workflow.FailedStage = ""
		workflow.TranslationWarnings = nil
		workflow.TranslationSteps = initialTranslationStepsFor(workflow.SourceMethod)
		workflow.TranslationStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		workflow.TranslationEstimatedCompletionAt = ""
		workflow.TranslationCompletedAt = ""
	}
	workflow.mu.Unlock()
	if !allowed {
		return nil, errors.New("hãy duyệt video nguồn trước khi tạo script và dịch")
	}
	if err := persistWorkflow(workflow); err != nil {
		return nil, err
	}
	go s.runWorkflowTranslation(workflow)
	return workflowSnapshot(workflow), nil
}

func (s Service) runWorkflowTranslation(workflow *subtitleWorkflow) {
	task := workflow.task()
	step := workflow.stepParam(task)
	step.SourceProgress = func(id string, percent uint8, detail string) {
		workflow.updateTranslationStep(id, percent, detail, time.Time{})
	}
	var sourceErr error
	switch normalizeWorkflowSourceMethod(workflow.SourceMethod) {
	case sourceMethodVisualOCR:
		sourceErr = s.extractVisualOCRSourceForReview(context.Background(), workflow, task, step)
	case sourceMethodSpeechToTextAndOCR:
		sourceErr = s.extractCombinedSourceForReview(context.Background(), workflow, task, step)
	default:
		if sourceWorkflowNeedsAudioSeparation(workflow.SourceMethod) {
			sourceErr = s.separateSourceAudioForSTT(context.Background(), step)
		}
		if sourceErr == nil {
			sourceErr = s.transcribeSourceForReview(context.Background(), workflow, task, step)
		}
	}
	if sourceErr != nil {
		s.failWorkflow(workflow, task, sourceErr)
		return
	}
	workflow.updateTranslationStep("translation_prepare", 0, "Reading generated source SRT", time.Time{})
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
	workflow.updateTranslationStep("translation_prepare", 100, fmt.Sprintf("Approved source SRT contains %d cue(s)", len(blocks)), time.Time{})
	translationStarted := time.Now()
	workflow.updateTranslationStep("translation_model", 0, "Sending subtitle batches to the selected translation model", time.Time{})
	task.ProcessPct = 50
	terms := workflow.protectedTermMap()
	protectSrtBlockTerms(blocks, terms)
	err = s.YouTubeSubtitleSrv.translator.BatchTranslateSrtBlocksWithProgress(blocks, workflow.OriginLanguage, workflow.TargetLanguage, task, func(completed, total int) {
		percent := uint8(0)
		if total > 0 {
			percent = uint8((completed * 100) / total)
		}
		var eta time.Time
		if completed > 0 && total > completed {
			elapsed := time.Since(translationStarted)
			eta = time.Now().Add((elapsed / time.Duration(completed)) * time.Duration(total-completed))
		}
		workflow.updateTranslationStep("translation_model", percent, fmt.Sprintf("Translated batch %d/%d", completed, total), eta)
		if err := persistWorkflow(workflow); err != nil {
			log.GetLogger().Warn("could not persist translation progress", zap.String("task_id", workflow.TaskID), zap.Error(err))
		}
	})
	restoreSrtBlockTerms(blocks, terms)
	if err != nil {
		s.failWorkflow(workflow, task, fmt.Errorf("dịch batch thất bại: %w", err))
		return
	}
	workflow.updateTranslationStep("translation_model", 100, "All subtitle batches translated", time.Time{})
	workflow.updateTranslationStep("translation_write", 0, "Writing reviewable translated SRT", time.Time{})
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
	workflow.finishTranslationProgress("Translated SRT and review artifacts are ready")
	workflow.mu.Lock()
	workflow.CurrentStage = workflowAwaitTranslation
	workflow.TranslationWarnings = translationReviewWarnings(blocks, workflow.OriginLanguage, workflow.TargetLanguage)
	workflow.Message = translationReviewMessage(workflow.TranslationWarnings)
	workflow.FailureReason = ""
	workflow.TranslationRevision++
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
	if workflow.ReviewMode == reviewModeAuto {
		if _, err := s.ApproveWorkflowStage(workflow.TaskID, "translation"); err != nil {
			s.failWorkflow(workflow, task, fmt.Errorf("cannot auto-approve translation: %w", err))
		}
	}
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
	if err := requireWorkflowArtifact(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskBackgroundAudioFileName), "nhạc nền đã tách"); err != nil {
		return nil, fmt.Errorf("không thể bắt đầu lồng tiếng sạch nhạc: %w. Hãy chạy lại bước Nguồn bằng Speech-to-text hoặc STT + OCR với notebook KOVA STT GPU hiện tại", err)
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowDubbingAudioRunning
	// An audio artifact is deliberately reviewable before it is approved. Let
	// the user synthesize it again from that review state without having to
	// edit/reapprove the translated SRT just to change the selected TTS voice.
	restartingReviewAudio := workflow.CurrentStage == workflowAwaitDubbingAudio
	allowed := (workflow.CurrentStage == workflowTranslationApproved || retrying || restartingReviewAudio) && workflow.TranslationApproved
	if allowed {
		workflow.CurrentStage = workflowDubbingAudioRunning
		workflow.Message = "Đang tạo audio lồng tiếng từ bản dịch đã duyệt."
		workflow.FailureReason = ""
		workflow.FailedStage = ""
		workflow.DubbingRequested = true
		workflow.DubbingAudioApproved = false
		workflow.DubbingVideoApproved = false
		workflow.DubbingApproved = false
		workflow.DubbingSteps = initialDubbingSteps()
		workflow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
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
		filepath.Join(basePath, types.TtsMixedAudioFileName),
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
		filepath.Join(basePath, types.TtsMixedAudioFileName),
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

// normalizeOmniVoiceReference keeps a saved Voice Studio profile distinct from
// a local reference-audio filename. A profile:<id> value is an opaque identity
// owned by the remote Voice Studio worker; it must never be passed to os.Stat
// or otherwise interpreted as a Windows path.
func normalizeOmniVoiceReference(value string) (reference string, isProfile bool, err error) {
	reference = strings.TrimPrefix(strings.TrimSpace(value), "local:")
	if !strings.HasPrefix(strings.ToLower(reference), "profile:") {
		return reference, false, nil
	}
	profileID := strings.TrimSpace(reference[len("profile:"):])
	if profileID == "" {
		return "", true, errors.New("profile OmniVoice không có mã định danh")
	}
	return "profile:" + profileID, true, nil
}

func (s Service) runWorkflowDubbingAudio(workflow *subtitleWorkflow, req dto.StartWorkflowDubbingReq) {
	task := workflow.task()
	step := workflow.stepParam(task)
	step.EnableTts = true
	step.TtsVoiceCode = strings.TrimSpace(req.TtsVoiceCode)
	if step.TtsVoiceCode == "" {
		step.TtsVoiceCode = "auto"
	}
	step.DubbingProgress = func(id string, percent uint8, detail string) {
		workflow.updateDubbingStep(id, percent, detail)
		task.ProcessPct = workflowDubbingProcessPercent(id, percent)
		workflow.mu.Lock()
		if workflow.CurrentStage == workflowDubbingAudioRunning {
			workflow.Message = detail
		}
		workflow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		workflow.mu.Unlock()
		if err := persistWorkflow(workflow); err != nil {
			log.GetLogger().Warn("could not persist dubbing progress", zap.String("task_id", workflow.TaskID), zap.Error(err))
		}
	}
	step.VoiceCloneConsent = req.VoiceCloneConsent
	if strings.EqualFold(strings.TrimSpace(config.Conf.Tts.Provider), "omnivoice") {
		reference, isProfile, err := normalizeOmniVoiceReference(req.TtsVoiceCloneSrcFileUrl)
		if err != nil {
			s.failWorkflow(workflow, task, err)
			return
		}
		step.VoiceCloneAudioUrl = reference
		// A Voice Studio profile already contains the consented reference on
		// the remote worker. Checking it as a desktop file was the source of
		// "GetFileAttributesEx profile:<id>" failures on Windows.
		if !isProfile {
			if _, err := os.Stat(step.VoiceCloneAudioUrl); err != nil {
				s.failWorkflow(workflow, task, fmt.Errorf("không thể đọc audio mẫu OmniVoice: %w", err))
				return
			}
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
	workflow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
	if workflow.ReviewMode == reviewModeAuto {
		if _, err := s.ApproveWorkflowStage(workflow.TaskID, "dubbing_audio"); err != nil {
			s.failWorkflow(workflow, task, fmt.Errorf("cannot auto-approve dubbed audio: %w", err))
		}
	}
}

func workflowDubbingProcessPercent(phase string, percent uint8) uint8 {
	if percent > 100 {
		percent = 100
	}
	switch phase {
	case "prepare":
		return 75 + uint8((2*int(percent))/100)
	case "synthesize":
		return 77 + uint8((4*int(percent))/100)
	case "fit":
		return 81 + uint8((2*int(percent))/100)
	case "assemble":
		return 83 + uint8(percent/100)
	default:
		return 75
	}
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
	if workflow.ReviewMode == reviewModeAuto {
		if _, err := s.ApproveWorkflowStage(workflow.TaskID, "dubbing_video"); err != nil {
			s.failWorkflow(workflow, task, fmt.Errorf("cannot auto-approve dubbed video: %w", err))
		}
	}
}

func defaultRenderBlurRegion() (x, y, width, height float64) {
	return 0.10, 0.70, 0.80, 0.20
}

func normalizeWorkflowRenderBlur(req dto.StartWorkflowRenderReq) (enabled bool, x, y, width, height float64, strength int, err error) {
	if !req.BlurOriginalText {
		return false, 0, 0, 0, 0, 0, nil
	}
	x, y, width, height = req.BlurRegionX, req.BlurRegionY, req.BlurRegionWidth, req.BlurRegionHeight
	if x == 0 && y == 0 && width == 0 && height == 0 {
		x, y, width, height = defaultRenderBlurRegion()
	}
	if x < 0 || y < 0 || width <= 0 || height <= 0 || x+width > 1 || y+height > 1 {
		return false, 0, 0, 0, 0, 0, errors.New("render blur region must stay inside the video frame")
	}
	strength = req.BlurStrength
	if strength == 0 {
		strength = 8
	}
	if strength < 1 || strength > maxRenderBlurStrength {
		return false, 0, 0, 0, 0, 0, fmt.Errorf("render blur strength must be between 1 and %d", maxRenderBlurStrength)
	}
	return true, x, y, width, height, strength, nil
}

// StartWorkflowRender preserves the no-options API used by older clients.
func (s Service) StartWorkflowRender(taskID string) (*dto.SubtitleWorkflowData, error) {
	return s.StartWorkflowRenderWithOptions(taskID, dto.StartWorkflowRenderReq{})
}

func (s Service) StartWorkflowRenderWithOptions(taskID string, req dto.StartWorkflowRenderReq) (*dto.SubtitleWorkflowData, error) {
	// Render can be started after a desktop/server restart, when no TTS request
	// has run in this process yet. Resolve the portable ffmpeg/ffprobe pair
	// synchronously here so getResolution never reaches exec.Command with an
	// empty FfprobePath after the user has already created a render job.
	if err := deps.EnsureDubbingMediaTools(); err != nil {
		return nil, fmt.Errorf("KOVA cannot prepare ffmpeg/ffprobe before rendering: %w", err)
	}
	blurEnabled, blurX, blurY, blurWidth, blurHeight, blurStrength, err := normalizeWorkflowRenderBlur(req)
	if err != nil {
		return nil, err
	}
	workflow, err := loadWorkflow(taskID)
	if err != nil {
		return nil, err
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowRenderRunning
	rerendering := workflow.CurrentStage == workflowCompleted
	allowed := workflow.TranslationApproved && (workflow.CurrentStage == workflowTranslationApproved || workflow.CurrentStage == workflowDubbingVideoApproved || retrying || rerendering)
	if workflow.DubbingRequested {
		allowed = allowed && workflow.DubbingVideoApproved && (workflow.CurrentStage == workflowDubbingVideoApproved || retrying || rerendering)
	}
	if allowed {
		workflow.BlurOldText = blurEnabled
		workflow.BlurRegionX = blurX
		workflow.BlurRegionY = blurY
		workflow.BlurRegionW = blurWidth
		workflow.BlurRegionH = blurHeight
		workflow.BlurStrength = blurStrength
		workflow.CurrentStage = workflowRenderRunning
		workflow.RenderSteps = initialRenderSteps()
		workflow.RenderStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		workflow.RenderEstimatedCompletionAt = ""
		workflow.RenderCompletedAt = ""
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
	// A render has a dedicated 0-100 progress range. Do not inherit an earlier
	// approval milestone (such as 94%), because that looks like encoding is
	// almost complete before FFmpeg has even started.
	task.ProcessPct = 0
	step := workflow.stepParam(task)
	workflow.mu.Lock()
	step.EnableTts = workflow.DubbingRequested && workflow.DubbingVideoApproved
	workflow.mu.Unlock()
	workflow.updateRenderStep("render_preflight", 0, "Checking media files and FFmpeg.", time.Time{})
	if err := persistWorkflow(workflow); err != nil {
		log.GetLogger().Warn("could not persist render start", zap.String("task_id", workflow.TaskID), zap.Error(err))
	}
	progress := func(update RenderProgress) {
		workflow.updateRenderStep(update.Phase, update.Percent, update.Detail, update.EstimatedCompletionAt)
		// Save each visible heartbeat as well. The desktop reads the in-memory
		// workflow every second; persisting additionally prevents a stale fake
		// progress display after an app restart.
		if err := persistWorkflow(workflow); err != nil {
			log.GetLogger().Warn("could not persist render progress", zap.String("task_id", workflow.TaskID), zap.Error(err))
		}
	}
	if err := s.embedSubtitlesWithProgress(context.Background(), step, progress); err != nil {
		s.failWorkflow(workflow, task, err)
		return
	}
	capcutResult, capcutErr := s.exportWorkflowCapCutDraft(context.Background(), workflow)
	workflow.finishRenderProgress("Final video file verified.")
	task.ProcessPct = 100
	task.Status = types.SubtitleTaskStatusSuccess
	workflow.mu.Lock()
	workflow.CurrentStage = workflowCompleted
	workflow.Message = "Đã render xong. Từng artifact có thể tải ở bước 05."
	if capcutErr != nil {
		workflow.Message += " MP4 đã sẵn sàng; KOVA chưa tạo được project CapCut chỉnh sửa: " + capcutErr.Error()
	} else if capcutResult.Compiled {
		workflow.Message += " Đã tạo CapCut draft chỉnh sửa được tại: " + capcutResult.DraftDirectory
	} else {
		workflow.Message += " Đã tạo timeline CapCut có thể kiểm tra tại artifact kova-capcut-draft-spec.json. Cấu hình pycapcut + thư mục Draft CapCut để KOVA compile thành draft mở trực tiếp trong CapCut."
	}
	workflow.FailureReason = ""
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

// exportWorkflowCapCutDraft keeps the editable material separate from the
// burned-in MP4. The source video, dubbed voice and both subtitle languages
// become independent tracks in a KOVA CapCut spec; when the user has enabled
// the configured pycapcut/capcut-cli compiler, the same reviewed spec is then
// compiled into a real CapCut draft without regenerating the timeline.
func (s Service) exportWorkflowCapCutDraft(ctx context.Context, workflow *subtitleWorkflow) (capcutstudio.BuildResult, error) {
	if workflow == nil {
		return capcutstudio.BuildResult{}, errors.New("thiếu workflow để xuất CapCut")
	}
	basePath := workflow.TaskBasePath
	sourceVideo := filepath.Join(basePath, types.SubtitleTaskVideoFileName)
	if err := requireWorkflowArtifact(sourceVideo, "video nguồn"); err != nil {
		return capcutstudio.BuildResult{}, err
	}
	voiceovers := make([]string, 0, 1)
	if workflow.DubbingRequested {
		dubbedAudio := filepath.Join(basePath, types.TtsResultAudioFileName)
		if err := requireWorkflowArtifact(dubbedAudio, "audio lồng tiếng"); err != nil {
			return capcutstudio.BuildResult{}, err
		}
		voiceovers = append(voiceovers, dubbedAudio)
	}
	blurMasks := make([]capcutstudio.BlurMask, 0, 1)
	if workflow.BlurOldText {
		blurMasks = append(blurMasks, capcutstudio.BlurMask{
			Shape: capcutstudio.MaskRectangle, X: workflow.BlurRegionX, Y: workflow.BlurRegionY,
			Width: workflow.BlurRegionW, Height: workflow.BlurRegionH, Feather: 0.08,
		})
	}
	outputDir := filepath.Join(basePath, "output", "capcut-editable")
	builder := capcutstudio.Builder{Config: capcutstudio.Config{
		FFprobePath:        storage.FfprobePath,
		NodePath:           config.Conf.Creator.NodePath,
		CapCutCLIPath:      config.Conf.Creator.CapCutCLIPath,
		CompilerBackend:    config.Conf.Creator.CompilerBackend,
		PythonPath:         config.Conf.Creator.PythonPath,
		PyCapCutBridgePath: config.Conf.Creator.PyCapCutBridgePath,
		CapCutDraftRoot:    config.Conf.Creator.CapCutDraftRoot,
	}}
	return builder.Build(ctx, capcutstudio.BuildRequest{
		Name:                "KOVA " + workflow.TaskID,
		Source:              capcutstudio.Source{VideoPath: sourceVideo},
		VoiceoverInputs:     voiceovers,
		SourceSRT:           filepath.Join(basePath, types.SubtitleTaskOriginLanguageSrtFileName),
		TargetSRT:           filepath.Join(basePath, types.SubtitleTaskTargetLanguageSrtFileName),
		SourceLanguage:      workflow.OriginLanguage,
		TargetLanguage:      workflow.TargetLanguage,
		SourceSubtitleStyle: capcutstudio.DefaultSourceStyle(),
		TargetSubtitleStyle: capcutstudio.DefaultTargetStyle(),
		SourceSubtitleY:     -0.54,
		TargetSubtitleY:     -0.72,
		BlurMasks:           blurMasks,
		OutputDir:           outputDir,
		CompileDraft:        config.Conf.Creator.CompileDraft,
	})
}

// workflowRenderProcessPercent reports the current final-render task only.
// It is intentionally independent from the old whole-workflow milestones.
func workflowRenderProcessPercent(phase string, percent uint8) uint8 {
	if percent > 100 {
		percent = 100
	}
	switch phase {
	case "render_preflight":
		return uint8((5 * int(percent)) / 100)
	case "render_subtitle":
		return 5 + uint8((10*int(percent))/100)
	case "render_encode":
		return 15 + uint8((80*int(percent))/100)
	case "render_verify":
		return 95 + uint8((5*int(percent))/100)
	default:
		return 0
	}
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
			approveErr = errors.New("video nguồn chưa sẵn sàng để duyệt")
		} else if err := requireWorkflowArtifact(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskVideoFileName), "video nguồn"); err != nil {
			approveErr = err
		} else {
			workflow.SourceApproved = true
			workflow.CurrentStage = workflowSourceApproved
			workflow.Message = "Nguồn đã duyệt. Bạn có thể tạo script và dịch ở bước 02."
			task.ProcessPct = 25
		}
	case "translation":
		if workflow.CurrentStage != workflowAwaitTranslation {
			approveErr = errors.New("bản dịch chưa sẵn sàng để duyệt")
		} else if err := s.synchronizeWorkflowTranslationArtifacts(workflow); err != nil {
			// Quality findings and malformed model output are advisory at this
			// review gate. An explicit approval rebuilds a valid target SRT on the
			// source timeline instead of trapping the user at this step.
			repairedCues, repairErr := repairWorkflowTranslationForApproval(workflow)
			if repairErr == nil {
				repairErr = s.synchronizeWorkflowTranslationArtifacts(workflow)
			}
			if repairErr != nil {
				approveErr = fmt.Errorf("cannot normalize translation for approval: %w", repairErr)
			} else {
				workflow.TranslationApproved = true
				workflow.CurrentStage = workflowTranslationApproved
				workflow.Message = fmt.Sprintf("Translation approved after normalizing malformed SRT. %d cue(s) retained their source text so you can continue.", repairedCues)
				task.ProcessPct = 75
			}
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

// repairWorkflowTranslationForApproval converts a malformed model response
// into a normal SRT while keeping the source timeline authoritative. It is
// only used after the user explicitly approves the translation review gate.
// The original malformed file is retained beside the repaired file for audit.
func repairWorkflowTranslationForApproval(workflow *subtitleWorkflow) (int, error) {
	if workflow == nil {
		return 0, errors.New("workflow is nil")
	}

	sourcePath := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)
	sourceCues, err := dubbing.ParseSRTFile(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("read source SRT: %w", err)
	}
	if len(sourceCues) == 0 {
		return 0, errors.New("source SRT has no usable cues")
	}
	for _, cue := range sourceCues {
		if strings.TrimSpace(cue.Text) == "" {
			return 0, fmt.Errorf("source cue %d has no text", cue.Index)
		}
	}

	targetPath := filepath.Join(workflow.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName)
	rawTarget, err := os.ReadFile(targetPath)
	if err != nil {
		return 0, fmt.Errorf("read translated SRT: %w", err)
	}
	recovered := recoverWorkflowTargetTexts(string(rawTarget))
	rebuilt := make([]dubbing.Cue, 0, len(sourceCues))
	fallbackCount := 0
	for _, sourceCue := range sourceCues {
		text := recovered[sourceCue.Index]
		if text == "" {
			// The user chose to continue. Keep this cue audible/renderable rather
			// than failing the whole workflow because the model omitted it.
			text = strings.TrimSpace(sourceCue.Text)
			fallbackCount++
		}
		rebuilt = append(rebuilt, dubbing.Cue{
			Index: sourceCue.Index,
			Start: sourceCue.Start,
			End:   sourceCue.End,
			Text:  text,
		})
	}

	backupPath := targetPath + ".before_approval_repair.srt"
	if err := os.WriteFile(backupPath, rawTarget, 0644); err != nil {
		return 0, fmt.Errorf("backup malformed translated SRT: %w", err)
	}
	if err := dubbing.WriteSRTFile(targetPath, rebuilt); err != nil {
		return 0, fmt.Errorf("write normalized translated SRT: %w", err)
	}
	return fallbackCount, nil
}

func recoverWorkflowTargetTexts(content string) map[int]string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	partsByCue := make(map[int][]string)
	currentCue := 0

	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		line := strings.TrimSpace(lines[lineIndex])
		if line == "" {
			continue
		}
		if cueIndex, timestampIndex, ok := workflowSRTHeader(lines, lineIndex); ok {
			currentCue = cueIndex
			lineIndex = timestampIndex
			continue
		}
		if currentCue == 0 || workflowSRTTimestamp(line) {
			continue
		}
		if _, err := strconv.Atoi(line); err == nil {
			// Malformed model responses often add nested numbered items inside a
			// valid cue. They are structure, not spoken subtitle text.
			continue
		}
		if text := cleanRecoveredTranslationText(line); text != "" {
			partsByCue[currentCue] = append(partsByCue[currentCue], text)
		}
	}

	result := make(map[int]string, len(partsByCue))
	for cueIndex, parts := range partsByCue {
		if text := strings.TrimSpace(strings.Join(parts, " ")); text != "" {
			result[cueIndex] = text
		}
	}
	return result
}

func workflowSRTHeader(lines []string, index int) (cueIndex, timestampIndex int, ok bool) {
	if index < 0 || index >= len(lines) {
		return 0, 0, false
	}
	parsedIndex, err := strconv.Atoi(strings.TrimSpace(lines[index]))
	if err != nil || parsedIndex <= 0 {
		return 0, 0, false
	}
	for next := index + 1; next < len(lines); next++ {
		candidate := strings.TrimSpace(lines[next])
		if candidate == "" {
			continue
		}
		if !workflowSRTTimestamp(candidate) {
			return 0, 0, false
		}
		return parsedIndex, next, true
	}
	return 0, 0, false
}

func workflowSRTTimestamp(line string) bool {
	parts := strings.Split(line, "-->")
	if len(parts) != 2 {
		return false
	}
	_, startErr := dubbing.ParseTimestamp(strings.TrimSpace(parts[0]))
	_, endErr := dubbing.ParseTimestamp(strings.TrimSpace(parts[1]))
	return startErr == nil && endErr == nil
}

func cleanRecoveredTranslationText(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(line, "[[KOVA_TRANSLATION_MISSING]]"))
	if marker := strings.Index(line, "%!(EXTRA"); marker >= 0 {
		line = strings.TrimSpace(line[:marker])
	}
	if line == "[NO_TEXT]" || strings.Contains(line, "EXTRA string=") {
		return ""
	}
	return line
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
	workflow.failActiveTranslationStep(err.Error())
	workflow.failActiveDubbingStep(err.Error())
	workflow.failActiveRenderStep(err.Error())
	workflow.mu.Lock()
	failedStage := workflow.CurrentStage
	workflow.CurrentStage = workflowFailed
	workflow.FailedStage = failedStage
	workflow.FailureReason = err.Error()
	workflow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	workflow.Message = "Bước hiện tại thất bại. Bạn có thể kiểm tra/sửa và chạy lại đúng bước đó."
	workflow.mu.Unlock()
	_ = persistWorkflow(workflow)
}

func workflowSnapshot(workflow *subtitleWorkflow) *dto.SubtitleWorkflowData {
	workflow.mu.Lock()
	stage := workflow.CurrentStage
	message := workflow.Message
	failure := workflow.FailureReason
	sourceWarning := workflow.SourceWarning
	taskID := workflow.TaskID
	sourceURL := workflow.URL
	reviewMode := normalizeWorkflowReviewMode(workflow.ReviewMode)
	basePath := workflow.TaskBasePath
	dubbingRequested := workflow.DubbingRequested
	sourceApproved := workflow.SourceApproved
	translationApproved := workflow.TranslationApproved
	dubbingAudioApproved := workflow.DubbingAudioApproved
	dubbingVideoApproved := workflow.DubbingVideoApproved
	failedStage := workflow.FailedStage
	sourceMethod := workflow.SourceMethod
	sourceSteps := sourceStepsForSnapshot(workflow.SourceSteps, sourceMethod, stage, basePath, failure)
	translationSteps := cloneWorkflowSteps(workflow.TranslationSteps)
	dubbingSteps := cloneWorkflowSteps(workflow.DubbingSteps)
	renderSteps := cloneWorkflowSteps(workflow.RenderSteps)
	translationWarnings := cloneTranslationWarnings(workflow.TranslationWarnings)
	updatedAt := workflow.UpdatedAt
	estimatedCompletionAt := workflow.RenderEstimatedCompletionAt
	completedAt := workflow.RenderCompletedAt
	if stage == workflowTranslationRunning || stage == workflowAwaitTranslation || stage == workflowTranslationApproved {
		estimatedCompletionAt = workflow.TranslationEstimatedCompletionAt
		completedAt = workflow.TranslationCompletedAt
	}
	workflow.mu.Unlock()
	task := workflow.task()
	processPercent := task.ProcessPct
	if processPercent == 0 && !(stage == workflowRenderRunning && len(renderSteps) > 0) && !(stage == workflowTranslationRunning && len(translationSteps) > 0) {
		processPercent = workflowStageProgress(stage)
	}
	data := &dto.SubtitleWorkflowData{
		TaskId:                taskID,
		SourceUrl:             sourceURL,
		ReviewMode:            reviewMode,
		CurrentStage:          stage,
		ProcessPercent:        processPercent,
		Message:               message,
		FailureReason:         failure,
		SourceWarning:         sourceWarning,
		SourceSteps:           sourceSteps,
		TranslationSteps:      translationSteps,
		DubbingSteps:          dubbingSteps,
		RenderSteps:           renderSteps,
		TranslationWarnings:   translationWarnings,
		UpdatedAt:             updatedAt,
		EstimatedCompletionAt: estimatedCompletionAt,
		CompletedAt:           completedAt,
		Artifacts:             workflowArtifacts(taskID, basePath),
		CanStart:              map[string]bool{},
		ReviewRequired:        strings.HasPrefix(stage, "awaiting_"),
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
	audioStart := translationApproved && (stage == workflowTranslationApproved || stage == workflowAwaitDubbingAudio || (stage == workflowFailed && failedStage == workflowDubbingAudioRunning))
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
	renderRetry := stage == workflowFailed && failedStage == workflowRenderRunning
	renderStart := translationApproved && (stage == workflowTranslationApproved || stage == workflowCompleted || renderRetry)
	if dubbingRequested {
		renderStart = translationApproved && dubbingVideoApproved && (stage == workflowDubbingVideoApproved || stage == workflowCompleted || renderRetry)
	}
	data.CanStart["render"] = renderStart
	return data
}

// workflowStageProgress supplies a stable progress milestone after the
// desktop/server restarts. SubtitleTask progress lives only in memory, while
// the workflow stage is persisted on disk.
func workflowStageProgress(stage string) uint8 {
	switch stage {
	case workflowSourceRunning:
		return 1
	case workflowAwaitSourceReview:
		return 20
	case workflowSourceApproved:
		return 25
	case workflowTranslationRunning:
		return 30
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
		{"capcut_editable_spec", "Editable CapCut timeline", filepath.Join(basePath, "output", "capcut-editable", "kova-capcut-draft-spec.json")},
		{"source_stt_srt", "STT transcript", filepath.Join(basePath, "origin_language_stt.srt")},
		{"source_ocr_srt", "OCR transcript", filepath.Join(basePath, "origin_language_ocr.srt")},
		{"source_video", "01 · Video nguồn / Source video", filepath.Join(basePath, types.SubtitleTaskVideoFileName)},
		{"source_srt", "02 · Phụ đề gốc / Original SRT", filepath.Join(basePath, types.SubtitleTaskOriginLanguageSrtFileName)},
		{"source_text", "02b · Script gốc / Original script", filepath.Join(basePath, "output", types.SubtitleTaskOriginLanguageTextFileName)},
		{"translated_srt", "03 · Phụ đề tiếng Việt / Vietnamese SRT", filepath.Join(basePath, types.SubtitleTaskTargetLanguageSrtFileName)},
		{"dubbed_audio", "04 · Âm thanh lồng tiếng / Dubbed audio", filepath.Join(basePath, types.TtsResultAudioFileName)},
		{"source_vocals", "04a · Giọng nguồn đã tách / Separated source vocals", filepath.Join(basePath, types.SubtitleTaskVocalAudioFileName)},
		{"source_background", "04b · Nhạc nền đã tách / Separated background", filepath.Join(basePath, types.SubtitleTaskBackgroundAudioFileName)},
		{"dubbed_mixed_audio", "04c · Audio hoàn chỉnh / Dubbed voice with background", filepath.Join(basePath, types.TtsMixedAudioFileName)},
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
	if recoverStalledDubbingAudio(workflow, time.Now()) {
		task := workflow.task()
		task.Status = types.SubtitleTaskStatusFailed
		task.FailReason = stalledDubbingFailureReason
		if err := persistWorkflow(workflow); err != nil {
			return nil, err
		}
	}
	return workflowSnapshot(workflow), nil
}

// DeleteWorkflow removes only a task-owned workflow directory. It is called
// by the desktop project's explicit delete action so a user can restart the
// complete flow without stale media, SRTs, reports, or persisted job state.
func (s Service) DeleteWorkflow(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if !validWorkflowTaskID(taskID) {
		return errors.New("mã job workflow không hợp lệ")
	}
	basePath := filepath.Clean(filepath.Join("tasks", taskID))
	if workflow, err := loadWorkflow(taskID); err == nil && workflow != nil {
		if filepath.Clean(workflow.TaskBasePath) != basePath {
			return errors.New("workflow task path is outside its task directory")
		}
	}
	workflowSessions.Delete(taskID)
	storage.SubtitleTasks.Delete(taskID)
	if err := os.RemoveAll(basePath); err != nil {
		return fmt.Errorf("không thể xóa thư mục workflow: %w", err)
	}
	return nil
}
