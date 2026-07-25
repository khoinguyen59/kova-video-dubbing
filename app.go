package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kova/config"
	"kova/internal/processutil"
	"kova/internal/project"
	"kova/internal/server"
	"kova/internal/service"
	"kova/internal/visualocr"
	"kova/log"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

const (
	defaultColabNotebookURL    = "https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb"
	defaultSTTColabNotebookURL = "https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/notebooks/KOVA_STT_GPU.ipynb"
	defaultOCRColabNotebookURL = "https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/notebooks/KOVA_VISUAL_OCR_GPU.ipynb"
)

var (
	taskIDPattern              = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	workflowSecretValuePattern = regexp.MustCompile(`(?i)\b(?:authorization|api[_-]?key|session[_-]?api[_-]?key|token)\b\s*[:=]\s*[^\s,;]+|\bBearer\s+[A-Za-z0-9._-]+`)
)

// App is the desktop boundary for KOVA.  The UI receives typed Wails methods;
// workflow state belongs to the project store, never to React component memory.
type App struct {
	ctx             context.Context
	serverStarted   atomic.Bool
	httpClient      *http.Client
	projectMu       sync.Mutex
	projectStore    *project.Store
	projectDataRoot string
}

type DesktopStage struct {
	ID      string `json:"id"`
	Number  string `json:"number"`
	TitleVI string `json:"title_vi"`
	TitleEN string `json:"title_en"`
}

type DesktopBootstrap struct {
	Name                string         `json:"name"`
	LegacyAPIBaseURL    string         `json:"legacy_api_base_url"`
	ColabNotebookURL    string         `json:"colab_notebook_url"`
	STTColabNotebookURL string         `json:"stt_colab_notebook_url"`
	OCRColabNotebookURL string         `json:"ocr_colab_notebook_url"`
	Stages              []DesktopStage `json:"stages"`
	Locales             []string       `json:"locales"`
}

type StartStageRequest struct {
	TaskID  string          `json:"task_id"`
	Stage   string          `json:"stage"`
	Payload json.RawMessage `json:"payload"`
}

type APIReply struct {
	Error   int             `json:"error"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type VoiceHealthRequest struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
}

type VoiceHealth struct {
	Reachable bool            `json:"reachable"`
	Status    int             `json:"status"`
	Data      json.RawMessage `json:"data,omitempty"`
	Message   string          `json:"message"`
}

// CapCutDraftSettings controls the optional native CapCut project export.
// The rendered MP4 remains available either way; this setting is only for the
// separate editable project, which keeps video, audio, text and subtitle
// tracks independently editable inside CapCut.
type CapCutDraftSettings struct {
	Enabled           bool   `json:"enabled"`
	Backend           string `json:"backend"`
	DraftRoot         string `json:"draft_root"`
	DetectedDraftRoot string `json:"detected_draft_root,omitempty"`
	PythonPath        string `json:"python_path"`
}

// VoiceProfile deliberately carries no reference-audio path or token. The
// desktop uses ID as a stable KOVA library ID; the currently active remote ID
// stays in the private local library so a Colab reset can be recovered from a
// consented local backup without changing a user's selected voice.
type VoiceProfile struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Language        string `json:"language"`
	Status          string `json:"status"`
	Saved           bool   `json:"saved,omitempty"`
	BackupAvailable bool   `json:"backup_available,omitempty"`
	WorkerURL       string `json:"worker_url,omitempty"`
	// ReferenceClean is a worker declaration that its persisted reference is
	// the vocals-only stem. It is not a local path and contains no audio data.
	ReferenceClean bool `json:"reference_clean,omitempty"`
}

// VoiceProfileCreateRequest is local to the desktop session. The file is
// streamed to the user-controlled Voice Studio worker and, after a successful
// creation, copied to KOVA's private voice library for future restoration.
type VoiceProfileCreateRequest struct {
	BaseURL            string `json:"base_url"`
	Token              string `json:"token"`
	Name               string `json:"name"`
	ReferenceAudioPath string `json:"reference_audio_path"`
	Language           string `json:"language"`
	ConsentConfirmed   bool   `json:"consent_confirmed"`
}

// VoicePreviewRequest keeps Colab access session-only; only a short generated
// WAV data URL crosses back to the UI for the user to listen before choosing a
// fixed voice.
type VoicePreviewRequest struct {
	BaseURL   string `json:"base_url"`
	Token     string `json:"token"`
	ProfileID string `json:"profile_id"`
	Language  string `json:"language"`
}

type VoicePreview struct {
	ProfileID string `json:"profile_id"`
	DataURL   string `json:"data_url"`
}

type VoiceProfileDeleteRequest struct {
	BaseURL   string `json:"base_url"`
	Token     string `json:"token"`
	ProfileID string `json:"profile_id"`
}

// TTSOption drives the dropdown.  A gateway choice is a provider/model preset,
// not a secret: the credential is managed separately in the local settings.
type TTSOption struct {
	ID           string `json:"id"`
	LabelVI      string `json:"label_vi"`
	LabelEN      string `json:"label_en"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	NeedsWorker  bool   `json:"needs_worker"`
	NeedsProfile bool   `json:"needs_profile"`
}

// TranslationModelOption is intentionally a fixed free-tier list for the
// configured KOVA gateway. Credentials and endpoint values never cross the
// Wails boundary.
type TranslationModelOption struct {
	ID      string `json:"id"`
	LabelVI string `json:"label_vi"`
	LabelEN string `json:"label_en"`
}

// STTOption is separate from text translation and TTS.  Source
// transcription uses a local adapter and is never silently redirected to an
// API Gateway.
type STTOption struct {
	ID          string `json:"id"`
	LabelVI     string `json:"label_vi"`
	LabelEN     string `json:"label_en"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	NeedsWorker bool   `json:"needs_worker"`
}

type DesktopProjectRequest struct {
	Name           string `json:"name"`
	TargetLanguage string `json:"target_language"`
}

type DesktopWorkflowStartRequest struct {
	ProjectID           string  `json:"project_id"`
	Stage               string  `json:"stage"`
	SourceURL           string  `json:"source_url"`
	OriginLanguage      string  `json:"origin_language"`
	TargetLanguage      string  `json:"target_language"`
	STTOptionID         string  `json:"stt_option_id"`
	STTWorkerURL        string  `json:"stt_worker_url"`
	STTWorkerToken      string  `json:"stt_worker_token"`
	SourceMethod        string  `json:"source_method"`
	ReviewMode          string  `json:"review_mode"`
	SourceCookieBrowser string  `json:"source_cookie_browser"`
	OCREngine           string  `json:"ocr_engine"`
	OCRWorkerURL        string  `json:"ocr_worker_url"`
	OCRWorkerToken      string  `json:"ocr_worker_token"`
	OCRLanguage         string  `json:"ocr_language"`
	OCRRegionX          float64 `json:"ocr_region_x"`
	OCRRegionY          float64 `json:"ocr_region_y"`
	OCRRegionWidth      float64 `json:"ocr_region_width"`
	OCRRegionHeight     float64 `json:"ocr_region_height"`
	OCRSampleIntervalMS int     `json:"ocr_sample_interval_ms"`
	OCRPreferGPU        bool    `json:"ocr_prefer_gpu"`
	OCRFallbackToSTT    bool    `json:"ocr_fallback_to_stt"`
	TranslationModelID  string  `json:"translation_model_id"`
	// GatewayAPIKey is session-only input from the desktop Auto form. It is
	// deliberately excluded from project drafts and config.toml.
	GatewayAPIKey    string  `json:"gateway_api_key"`
	TTSOptionID      string  `json:"tts_option_id"`
	VoiceProfileID   string  `json:"voice_profile_id"`
	WorkerURL        string  `json:"worker_url"`
	WorkerToken      string  `json:"worker_token"`
	BlurOriginalText bool    `json:"blur_original_text"`
	BlurRegionX      float64 `json:"blur_region_x"`
	BlurRegionY      float64 `json:"blur_region_y"`
	BlurRegionWidth  float64 `json:"blur_region_width"`
	BlurRegionHeight float64 `json:"blur_region_height"`
	BlurStrength     int     `json:"blur_strength"`
}

// VisualOCRHealth keeps the Python bridge diagnostics short and actionable in
// the desktop UI. The raw subprocess output stays in the Go error instead of
// filling the source-stage page with an unreadable traceback.
type VisualOCRHealth struct {
	Ready         bool   `json:"ready"`
	CUDAAvailable bool   `json:"cuda_available"`
	Python        string `json:"python,omitempty"`
	Message       string `json:"message"`
}

type DesktopWorkflowAction struct {
	Run            project.StageRun `json:"run"`
	WorkflowTaskID string           `json:"workflow_task_id,omitempty"`
	Message        string           `json:"message,omitempty"`
}

type DesktopWorkflowArtifact struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
}

// DesktopFinalVideo is a local desktop-only handoff for the finished MP4. The
// path is shown so the user can find the file, while the two explicit methods
// below reveal it in Explorer or save a user-selected copy.
type DesktopFinalVideo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
	SizeBytes   int64  `json:"size_bytes"`
}

type DesktopWorkflowProgressStep struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Percent uint8  `json:"percent"`
	Detail  string `json:"detail,omitempty"`
}

// TranslationWarning is advisory review data returned by the worker. It is
// not a failure and never prevents the user from approving a translation.
type TranslationWarning struct {
	CueIndex        int      `json:"cue_index"`
	SuspiciousWords []string `json:"suspicious_words"`
	Reason          string   `json:"reason,omitempty"`
	Text            string   `json:"text"`
}

type DesktopWorkflowSnapshot struct {
	WorkflowTaskID        string                        `json:"workflow_task_id"`
	CurrentStage          string                        `json:"current_stage"`
	FailedStage           string                        `json:"failed_stage,omitempty"`
	ProcessPercent        uint8                         `json:"process_percent"`
	Message               string                        `json:"message"`
	FailureReason         string                        `json:"failure_reason,omitempty"`
	SourceWarning         string                        `json:"source_warning,omitempty"`
	ReviewRequired        bool                          `json:"review_required"`
	SourceSRTURL          string                        `json:"source_srt_url,omitempty"`
	TranslatedSRTURL      string                        `json:"translated_srt_url,omitempty"`
	SourceSteps           []DesktopWorkflowProgressStep `json:"source_steps,omitempty"`
	TranslationSteps      []DesktopWorkflowProgressStep `json:"translation_steps,omitempty"`
	DubbingSteps          []DesktopWorkflowProgressStep `json:"dubbing_steps,omitempty"`
	RenderSteps           []DesktopWorkflowProgressStep `json:"render_steps,omitempty"`
	TranslationWarnings   []TranslationWarning          `json:"translation_warnings,omitempty"`
	UpdatedAt             string                        `json:"updated_at,omitempty"`
	EstimatedCompletionAt string                        `json:"estimated_completion_at,omitempty"`
	CompletedAt           string                        `json:"completed_at,omitempty"`
	FinalVideo            *DesktopFinalVideo            `json:"final_video,omitempty"`
	Artifacts             []DesktopWorkflowArtifact     `json:"artifacts,omitempty"`
}

func NewApp() *App {
	return &App{httpClient: &http.Client{Timeout: 15 * time.Second}}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if !a.serverStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		if err := server.StartBackend(); err != nil {
			log.GetLogger().Error("KOVA local API could not start", zap.Error(err))
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "kova:local-api-error", err.Error())
			}
		}
	}()
}

func (a *App) Shutdown(_ context.Context) {
	a.projectMu.Lock()
	if a.projectStore != nil {
		_ = a.projectStore.Close()
		a.projectStore = nil
	}
	a.projectMu.Unlock()
	if err := server.StopBackend(); err != nil {
		log.GetLogger().Warn("KOVA local API did not stop cleanly", zap.Error(err))
	}
}

// Bootstrap returns only display information.  API keys, cookies, reference
// audio and Colab bearer tokens must never be sent to the renderer.
func (a *App) Bootstrap() DesktopBootstrap {
	return DesktopBootstrap{
		Name:                "KOVA",
		LegacyAPIBaseURL:    localAPIBaseURL(),
		ColabNotebookURL:    defaultColabNotebookURL,
		STTColabNotebookURL: defaultSTTColabNotebookURL,
		OCRColabNotebookURL: defaultOCRColabNotebookURL,
		Locales:             []string{"vi", "en"},
		Stages: []DesktopStage{
			{ID: "source", Number: "01", TitleVI: "Tải video", TitleEN: "Download video"},
			{ID: "translation", Number: "02", TitleVI: "Tạo script, dịch và phụ đề", TitleEN: "Create script, translation and subtitles"},
			{ID: "dubbing_audio", Number: "03", TitleVI: "Giọng lồng tiếng cố định", TitleEN: "Fixed dubbing voice"},
			{ID: "render", Number: "04", TitleVI: "Xuất hình và tinh chỉnh", TitleEN: "Video output and tuning"},
			{ID: "outputs", Number: "05", TitleVI: "Chạy và nhận output", TitleEN: "Run and receive outputs"},
		},
	}
}

// OpenColabNotebook is invoked only after the user presses the explicit UI
// button.  It opens the notebook in the user's default browser/Chrome session.
func (a *App) OpenColabNotebook(notebookURL string) error {
	if a.ctx == nil {
		return errors.New("ứng dụng chưa sẵn sàng / application is not ready")
	}
	if strings.TrimSpace(notebookURL) == "" {
		notebookURL = defaultColabNotebookURL
	}
	u, err := url.ParseRequestURI(notebookURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "colab.research.google.com") {
		return errors.New("URL Colab không hợp lệ / invalid Colab URL")
	}
	runtime.BrowserOpenURL(a.ctx, u.String())
	return nil
}

// StartStage remains a deliberately constrained compatibility bridge for the
// original v1 worker routes.  It cannot be used as a generic local HTTP proxy.
func (a *App) StartStage(request StartStageRequest) (APIReply, error) {
	endpoint, method, needsTaskID, err := stageEndpoint(request.Stage)
	if err != nil {
		return APIReply{}, err
	}
	if needsTaskID {
		request.TaskID = strings.TrimSpace(request.TaskID)
		if !taskIDPattern.MatchString(request.TaskID) {
			return APIReply{}, errors.New("mã job không hợp lệ / invalid job id")
		}
		endpoint = strings.Replace(endpoint, ":task_id", url.PathEscape(request.TaskID), 1)
	}
	body := request.Payload
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	return a.callLocalAPI(method, endpoint, body)
}

func stageEndpoint(stage string) (endpoint, method string, needsTaskID bool, err error) {
	switch stage {
	case "source":
		return "/api/v1/jobs/subtitle/stages/source", http.MethodPost, false, nil
	case "translation":
		return "/api/v1/jobs/subtitle/:task_id/translation", http.MethodPost, true, nil
	case "dubbing_audio":
		return "/api/v1/jobs/subtitle/:task_id/dubbing/audio", http.MethodPost, true, nil
	case "dubbing_video":
		return "/api/v1/jobs/subtitle/:task_id/dubbing/video", http.MethodPost, true, nil
	case "render":
		return "/api/v1/jobs/subtitle/:task_id/render", http.MethodPost, true, nil
	default:
		return "", "", false, fmt.Errorf("stage không được hỗ trợ / unsupported stage: %s", stage)
	}
}

func (a *App) callLocalAPI(method, endpoint string, payload json.RawMessage) (APIReply, error) {
	req, err := http.NewRequest(method, strings.TrimRight(localAPIBaseURL(), "/")+endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return APIReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(req)
	if err != nil {
		return APIReply{}, fmt.Errorf("không kết nối được KOVA local API / cannot reach KOVA local API: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return APIReply{}, err
	}
	var envelope struct {
		Error int             `json:"error"`
		Msg   string          `json:"msg"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return APIReply{}, fmt.Errorf("KOVA local API returned invalid JSON: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || envelope.Error != 0 {
		if envelope.Msg == "" {
			envelope.Msg = response.Status
		}
		return APIReply{Error: envelope.Error, Message: envelope.Msg, Data: envelope.Data}, errors.New(envelope.Msg)
	}
	return APIReply{Error: 0, Message: envelope.Msg, Data: envelope.Data}, nil
}

func (a *App) desktopProjectStore() (*project.Store, error) {
	a.projectMu.Lock()
	defer a.projectMu.Unlock()
	if a.projectStore != nil {
		return a.projectStore, nil
	}
	store, err := project.Open(project.DefaultDatabasePath())
	if err != nil {
		return nil, err
	}
	a.projectStore = store
	a.projectDataRoot = filepath.Dir(project.DefaultDatabasePath())
	return a.projectStore, nil
}

// The following typed methods back the review-first desktop shell.  They do
// not trigger downloads, model loading, GPU work, or automatic publication.
func (a *App) CreateDesktopProject(request DesktopProjectRequest) (project.Project, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.Project{}, err
	}
	return store.CreateProject(context.Background(), request.Name, request.TargetLanguage)
}

func (a *App) ListDesktopProjects() ([]project.Project, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return nil, err
	}
	return store.ListProjects(context.Background())
}

func (a *App) GetDesktopProject(projectID string) (project.Snapshot, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.Snapshot{}, err
	}
	return store.Snapshot(context.Background(), strings.TrimSpace(projectID))
}

// DeleteDesktopProject explicitly clears the desktop timeline, its review
// drafts, and the paired task-owned workflow directory. It is intentionally
// irreversible so a user can test a source URL end-to-end without reusing a
// previous stuck job or its artifacts.
func (a *App) DeleteDesktopProject(projectID string) error {
	store, err := a.desktopProjectStore()
	if err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	if !taskIDPattern.MatchString(projectID) {
		return errors.New("invalid desktop project id")
	}
	snapshot, err := store.Snapshot(context.Background(), projectID)
	if err != nil {
		return err
	}
	if taskID := strings.TrimSpace(snapshot.Project.WorkflowTaskID); taskID != "" {
		if !taskIDPattern.MatchString(taskID) {
			return errors.New("invalid workflow task id")
		}
		if _, err := a.callLocalAPI(http.MethodDelete, "/api/v1/jobs/subtitle/"+url.PathEscape(taskID)+"/workflow", nil); err != nil {
			return fmt.Errorf("delete task workflow: %w", err)
		}
	}
	if err := store.DeleteProject(context.Background(), projectID); err != nil {
		return err
	}
	root := a.desktopProjectDataRoot()
	projectDir := filepath.Join(root, "projects", projectID)
	relative, err := filepath.Rel(filepath.Join(root, "projects"), projectDir)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return errors.New("invalid desktop project data path")
	}
	if err := os.RemoveAll(projectDir); err != nil {
		return fmt.Errorf("delete project drafts: %w", err)
	}
	return nil
}

func (a *App) StartDesktopStage(projectID, stage string) (project.StageRun, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.StageRun{}, err
	}
	return store.StartStage(context.Background(), strings.TrimSpace(projectID), project.Stage(stage))
}

// StartDesktopWorkflowStage records an explicit project-stage run and then
// invokes exactly one matching legacy workflow operation. Manual runs stop at
// their review gate. The desktop's separate Auto tab observes the persisted
// auto-approved gates and starts the next explicit stage, retaining an
// auditable run and artifact for every step.
func (a *App) StartDesktopWorkflowStage(request DesktopWorkflowStartRequest) (DesktopWorkflowAction, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return DesktopWorkflowAction{}, err
	}
	request.ProjectID, request.Stage = strings.TrimSpace(request.ProjectID), strings.TrimSpace(request.Stage)
	snapshot, err := store.Snapshot(context.Background(), request.ProjectID)
	if err != nil {
		return DesktopWorkflowAction{}, err
	}
	// One API Gateway credential covers translation and the Gateway TTS presets.
	// It is held only in process memory for this desktop session.
	if key := strings.TrimSpace(request.GatewayAPIKey); key != "" {
		config.Conf.Llm.SessionApiKey = key
		config.Conf.Tts.Gateway.SessionAPIKey = key
	}
	// Script creation belongs to Stage 02. Validate STT/OCR just before that
	// stage starts, after the source video has already been downloaded and
	// reviewed in Stage 01. This prevents a worker error from discarding a
	// perfectly good source download.
	if request.Stage == string(project.StageTranslation) {
		sourceMethod, err := normalizeDesktopSourceMethod(request.SourceMethod)
		if err != nil {
			return DesktopWorkflowAction{}, err
		}
		request.SourceMethod = sourceMethod
		if desktopSourceMethodUsesSTT(sourceMethod) {
			if err := configureDesktopSTT(request.STTOptionID, request.STTWorkerURL, request.STTWorkerToken); err != nil {
				return DesktopWorkflowAction{}, err
			}
			if isDesktopRemoteSTTOption(request.STTOptionID) {
				health := a.CheckSTTHealth(VoiceHealthRequest{BaseURL: request.STTWorkerURL, Token: request.STTWorkerToken})
				if !health.Reachable {
					return DesktopWorkflowAction{}, fmt.Errorf("Google Colab STT chÆ°a sáºµn sÃ ng: %s", firstNonEmpty(health.Message, "khÃ´ng káº¿t ná»‘i Ä‘Æ°á»£c worker"))
				}
			}
		}
		if desktopSourceMethodUsesOCR(sourceMethod) {
			ocrEngine, engineErr := normalizeDesktopOCREngine(request.OCREngine)
			if engineErr != nil {
				return DesktopWorkflowAction{}, engineErr
			}
			request.OCREngine = ocrEngine
			if ocrEngine == "colab" {
				health := a.CheckOCRHealth(VoiceHealthRequest{BaseURL: request.OCRWorkerURL, Token: request.OCRWorkerToken})
				if !health.Reachable {
					return DesktopWorkflowAction{}, fmt.Errorf("Google Colab OCR chưa sẵn sàng: %s", firstNonEmpty(health.Message, "không kết nối được worker"))
				}
			}
		}
	}
	run, err := store.StartStage(context.Background(), request.ProjectID, project.Stage(request.Stage))
	if err != nil {
		return DesktopWorkflowAction{}, err
	}
	action := DesktopWorkflowAction{Run: run, WorkflowTaskID: snapshot.Project.WorkflowTaskID}
	// Preserve the user-selected source before the legacy job is allowed to
	// start. That way an I/O failure cannot leave a remote job running without
	// an immutable KOVA record of the exact input the user approved.
	if strings.TrimSpace(request.SourceURL) != "" && request.Stage == string(project.StageSource) {
		if _, err := a.SaveDesktopDraft(request.ProjectID, run.ID, request.Stage, request.SourceURL); err != nil {
			_, _ = store.FailStage(context.Background(), run.ID, workflowFailureDetail(fmt.Errorf("save source review input: %w", err)))
			return action, fmt.Errorf("save source review input: %w", err)
		}
	}
	if request.Stage == string(project.StageTranslation) {
		if err := config.ConfigureKOVAGatewayTranslation(strings.TrimSpace(request.TranslationModelID)); err != nil {
			_, _ = store.FailStage(context.Background(), run.ID, workflowFailureDetail(err))
			return action, err
		}
	}
	workflowTaskID, startErr := a.startLegacyWorkflowStage(snapshot.Project, request)
	if startErr != nil {
		_, _ = store.FailStage(context.Background(), run.ID, workflowFailureDetail(startErr))
		return action, startErr
	}
	if workflowTaskID != "" {
		if _, err := store.SetWorkflowTaskID(context.Background(), snapshot.Project.ID, workflowTaskID); err != nil {
			_, _ = store.FailStage(context.Background(), run.ID, workflowFailureDetail(fmt.Errorf("save workflow link: %w", err)))
			return action, err
		}
		action.WorkflowTaskID = workflowTaskID
	}
	action.Message = "workflow stage started"
	return action, nil
}

func (a *App) startLegacyWorkflowStage(desktopProject project.Project, request DesktopWorkflowStartRequest) (string, error) {
	stage := project.Stage(request.Stage)
	workflowTaskID := strings.TrimSpace(desktopProject.WorkflowTaskID)
	switch stage {
	case project.StageSource:
		sourceURL := strings.TrimSpace(request.SourceURL)
		if sourceURL == "" {
			return "", errors.New("source URL or local source path is required")
		}
		originLanguage := strings.TrimSpace(request.OriginLanguage)
		if originLanguage == "" {
			originLanguage = "auto"
		}
		targetLanguage := strings.TrimSpace(request.TargetLanguage)
		if targetLanguage == "" {
			targetLanguage = strings.TrimSpace(desktopProject.TargetLanguage)
		}
		if targetLanguage == "" {
			targetLanguage = "vi"
		}
		sourceCookieBrowser, err := normalizeDesktopSourceCookieBrowser(request.SourceCookieBrowser)
		if err != nil {
			return "", err
		}
		payload, err := json.Marshal(map[string]any{
			"url":                           sourceURL,
			"origin_lang":                   originLanguage,
			"target_lang":                   targetLanguage,
			"bilingual":                     0,
			"translation_subtitle_pos":      1,
			"modal_filter":                  0,
			"tts":                           0,
			"language":                      targetLanguage,
			"embed_subtitle_video_type":     "horizontal",
			"origin_language_word_one_line": 12,
			"vtt_switch":                    false,
			"source_method":                 request.SourceMethod,
			"review_mode":                   normalizeDesktopReviewMode(request.ReviewMode),
			"source_cookie_browser":         sourceCookieBrowser,
			"ocr_engine":                    request.OCREngine,
			"ocr_worker_url":                request.OCRWorkerURL,
			"ocr_worker_token":              request.OCRWorkerToken,
			"ocr_language":                  request.OCRLanguage,
			"ocr_region_x":                  request.OCRRegionX,
			"ocr_region_y":                  request.OCRRegionY,
			"ocr_region_width":              request.OCRRegionWidth,
			"ocr_region_height":             request.OCRRegionHeight,
			"ocr_sample_interval_ms":        request.OCRSampleIntervalMS,
			"ocr_prefer_gpu":                request.OCRPreferGPU,
			"ocr_fallback_to_stt":           request.OCRFallbackToSTT,
		})
		if err != nil {
			return "", err
		}
		reply, err := a.callLocalAPI(http.MethodPost, "/api/v1/jobs/subtitle/stages/source", payload)
		if err != nil {
			return "", err
		}
		var data struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(reply.Data, &data); err != nil || !taskIDPattern.MatchString(strings.TrimSpace(data.TaskID)) {
			return "", errors.New("source workflow did not return a valid task id")
		}
		return data.TaskID, nil
	case project.StageTranslation:
		payload, err := json.Marshal(map[string]any{
			"source_method":          request.SourceMethod,
			"review_mode":            normalizeDesktopReviewMode(request.ReviewMode),
			"origin_lang":            request.OriginLanguage,
			"target_lang":            request.TargetLanguage,
			"ocr_engine":             request.OCREngine,
			"ocr_worker_url":         request.OCRWorkerURL,
			"ocr_worker_token":       request.OCRWorkerToken,
			"ocr_language":           request.OCRLanguage,
			"ocr_region_x":           request.OCRRegionX,
			"ocr_region_y":           request.OCRRegionY,
			"ocr_region_width":       request.OCRRegionWidth,
			"ocr_region_height":      request.OCRRegionHeight,
			"ocr_sample_interval_ms": request.OCRSampleIntervalMS,
			"ocr_prefer_gpu":         request.OCRPreferGPU,
			"ocr_fallback_to_stt":    request.OCRFallbackToSTT,
		})
		if err != nil {
			return workflowTaskID, err
		}
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/translation", payload)
	case project.StageDubbingAudio:
		payload, err := a.configureDesktopTTS(request)
		if err != nil {
			return workflowTaskID, err
		}
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/dubbing/audio", payload)
	case project.StageRender:
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/dubbing/video", nil)
	case project.StageOutputs:
		payload, err := json.Marshal(map[string]any{
			"blur_original_text": request.BlurOriginalText,
			"blur_region_x":      request.BlurRegionX,
			"blur_region_y":      request.BlurRegionY,
			"blur_region_width":  request.BlurRegionWidth,
			"blur_region_height": request.BlurRegionHeight,
			"blur_strength":      request.BlurStrength,
		})
		if err != nil {
			return workflowTaskID, err
		}
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/render", payload)
	default:
		return workflowTaskID, fmt.Errorf("unsupported desktop workflow stage: %s", request.Stage)
	}
}

func (a *App) startExistingWorkflowStage(workflowTaskID, suffix string, payload []byte) error {
	workflowTaskID = strings.TrimSpace(workflowTaskID)
	if !taskIDPattern.MatchString(workflowTaskID) {
		return errors.New("start the source stage before this workflow stage")
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	_, err := a.callLocalAPI(http.MethodPost, "/api/v1/jobs/subtitle/"+url.PathEscape(workflowTaskID)+suffix, payload)
	return err
}

func (a *App) configureDesktopTTS(request DesktopWorkflowStartRequest) ([]byte, error) {
	var selected *TTSOption
	options := a.ListTTSOptions()
	for index := range options {
		option := options[index]
		if option.ID == strings.TrimSpace(request.TTSOptionID) {
			selected = &option
			break
		}
	}
	if selected == nil {
		return nil, errors.New("select a supported TTS option")
	}
	config.Conf.Tts.Provider = selected.Provider
	if selected.Provider == "gateway" {
		config.Conf.Tts.Gateway.Model = selected.Model
		// The standard KOVA Gateway uses one bearer credential for text, STT
		// and TTS. Reuse an already-entered session key only when the user has
		// not configured a dedicated TTS key; it remains memory-only.
		if strings.TrimSpace(config.ResolveGatewayTTSAPIKey()) == "" {
			config.Conf.Tts.Gateway.SessionAPIKey = config.ResolveLLMAPIKey()
		}
		if strings.TrimSpace(config.ResolveGatewayTTSAPIKey()) == "" {
			return nil, errors.New("Google/Edge Gateway TTS cần API key; nhập key Gateway trong Cài đặt trước khi bắt đầu")
		}
		return json.Marshal(map[string]any{"tts_voice_code": "auto"})
	}
	if !selected.NeedsWorker || !selected.NeedsProfile {
		return nil, errors.New("unsupported desktop TTS option configuration")
	}
	workerURL, err := normalizeVoiceURL(request.WorkerURL)
	if err != nil {
		return nil, err
	}
	profileID := strings.TrimSpace(request.VoiceProfileID)
	if profileID == "" {
		return nil, errors.New("select one fixed Voice Studio profile")
	}
	if strings.TrimSpace(request.WorkerToken) == "" {
		return nil, errors.New("paste the temporary Voice Studio worker token")
	}
	remoteProfileID, err := a.ensureVoiceProfileOnWorker(profileID, workerURL, strings.TrimSpace(request.WorkerToken))
	if err != nil {
		return nil, err
	}
	// The token is session-only: this method updates runtime memory for the
	// immediately requested stage and never calls config.SaveConfig.
	config.Conf.Tts.Omnivoice.BaseUrl = workerURL
	config.Conf.Tts.Omnivoice.SessionApiKey = strings.TrimSpace(request.WorkerToken)
	return json.Marshal(map[string]any{
		"tts_voice_code":               "profile:" + remoteProfileID,
		"tts_voice_clone_src_file_url": "profile:" + remoteProfileID,
		"voice_clone_consent":          true,
	})
}

// RefreshDesktopWorkflow reads artifact/status metadata from the existing
// workflow and transitions the matching v2 run to review-required. No model,
// download, render, or approval is started by this read-only method.
func (a *App) RefreshDesktopWorkflow(projectID string) (DesktopWorkflowSnapshot, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return DesktopWorkflowSnapshot{}, err
	}
	snapshot, err := store.Snapshot(context.Background(), strings.TrimSpace(projectID))
	if err != nil {
		return DesktopWorkflowSnapshot{}, err
	}
	workflowTaskID := strings.TrimSpace(snapshot.Project.WorkflowTaskID)
	if !taskIDPattern.MatchString(workflowTaskID) {
		return DesktopWorkflowSnapshot{}, errors.New("this project has not started a source workflow")
	}
	reply, err := a.callLocalAPI(http.MethodGet, "/api/v1/jobs/subtitle/"+url.PathEscape(workflowTaskID)+"/workflow", json.RawMessage(`{}`))
	if err != nil {
		return DesktopWorkflowSnapshot{}, err
	}
	var workflow DesktopWorkflowSnapshot
	if err := json.Unmarshal(reply.Data, &workflow); err != nil {
		return DesktopWorkflowSnapshot{}, fmt.Errorf("decode workflow status: %w", err)
	}
	workflow.WorkflowTaskID = workflowTaskID
	// The legacy worker intentionally returns only web-safe artifact URLs. The
	// desktop additionally resolves the final MP4 to its exact local location
	// so the completed output is never hidden behind an internal artifact list.
	if finalVideo, outputErr := desktopFinalVideoForTask(workflowTaskID); outputErr == nil {
		workflow.FinalVideo = finalVideo
	}
	if stage, ok := reviewStageForLegacyStatus(workflow.CurrentStage); ok {
		if run := latestProjectRun(snapshot.StageRuns, stage); run != nil && run.Status == project.StatusRunning {
			if _, err := store.MarkReviewRequired(context.Background(), run.ID, "stage.review_required"); err != nil {
				return DesktopWorkflowSnapshot{}, err
			}
		}
	}
	if stage, ok := autoApprovedStageForLegacyStatus(workflow.CurrentStage); ok {
		if run := latestProjectRun(snapshot.StageRuns, stage); run != nil && (run.Status == project.StatusRunning || run.Status == project.StatusReviewNeeded) {
			if _, err := store.AutoApproveStage(context.Background(), run.ID); err != nil {
				return DesktopWorkflowSnapshot{}, err
			}
		}
	}
	if workflow.CurrentStage == "failed" {
		workflow.FailureReason = workflowFailureDetail(errors.New(firstNonEmpty(workflow.FailureReason, workflow.Message, "workflow reported a failed stage")))
		if run := latestRunningProjectRun(snapshot.StageRuns); run != nil {
			_, _ = store.FailStage(context.Background(), run.ID, workflow.FailureReason)
		} else if run := latestFailedProjectRun(snapshot.StageRuns); run != nil {
			_, _ = store.SetFailureDetail(context.Background(), run.ID, workflow.FailureReason)
		}
	}
	return workflow, nil
}

func desktopFinalVideoForTask(taskID string) (*DesktopFinalVideo, error) {
	taskID = strings.TrimSpace(taskID)
	if !taskIDPattern.MatchString(taskID) {
		return nil, errors.New("invalid workflow task id")
	}
	basePath := filepath.Join("tasks", taskID)
	// Prefer the user-requested final subtitled video. The muxed dubbed video
	// is a useful fallback only when no subtitle render was requested.
	candidates := []string{
		filepath.Join(basePath, "output", "horizontal_embed.mp4"),
		filepath.Join(basePath, "output", "vertical_embed.mp4"),
		filepath.Join(basePath, "video_with_tts.mp4"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve final video path: %w", err)
		}
		return &DesktopFinalVideo{
			Name:        filepath.Base(candidate),
			Path:        absolute,
			DownloadURL: "/api/v1/files/" + filepath.ToSlash(candidate),
			SizeBytes:   info.Size(),
		}, nil
	}
	return nil, errors.New("final video was not found")
}

func (a *App) desktopFinalVideoForProject(projectID string) (*DesktopFinalVideo, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return nil, err
	}
	snapshot, err := store.Snapshot(context.Background(), strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	return desktopFinalVideoForTask(snapshot.Project.WorkflowTaskID)
}

// RevealDesktopWorkflowFinalVideo opens Explorer with the final MP4 selected.
// It does not open a shell window or execute any untrusted path.
func (a *App) RevealDesktopWorkflowFinalVideo(projectID string) error {
	finalVideo, err := a.desktopFinalVideoForProject(projectID)
	if err != nil {
		return err
	}
	command := exec.Command("explorer.exe", "/select,", finalVideo.Path)
	processutil.HideConsole(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open output folder: %w", err)
	}
	return nil
}

// SaveDesktopWorkflowFinalVideo copies the final MP4 only after the user
// explicitly picks the target path in the native Windows save dialog.
func (a *App) SaveDesktopWorkflowFinalVideo(projectID string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	finalVideo, err := a.desktopFinalVideoForProject(projectID)
	if err != nil {
		return "", err
	}
	defaultDirectory := filepath.Dir(finalVideo.Path)
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		videos := filepath.Join(home, "Videos")
		if info, statErr := os.Stat(videos); statErr == nil && info.IsDir() {
			defaultDirectory = videos
		}
	}
	destination, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "Lưu video KOVA hoàn chỉnh",
		DefaultDirectory: defaultDirectory,
		DefaultFilename:  finalVideo.Name,
		Filters: []runtime.FileFilter{{
			DisplayName: "MP4 video",
			Pattern:     "*.mp4",
		}},
	})
	if err != nil {
		return "", fmt.Errorf("choose output location: %w", err)
	}
	if strings.TrimSpace(destination) == "" {
		return "", nil // user cancelled the native dialog
	}
	if strings.EqualFold(filepath.Ext(destination), "") {
		destination += ".mp4"
	}
	source, err := os.Open(finalVideo.Path)
	if err != nil {
		return "", fmt.Errorf("open final video: %w", err)
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("create saved video: %w", err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return "", fmt.Errorf("copy final video: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("finish saved video: %w", closeErr)
	}
	return destination, nil
}

// ReadDesktopWorkflowSubtitle returns the translated review SRT produced by
// Stage 02. Stage 01 only downloads media and intentionally has no editable
// subtitle document.
func (a *App) ReadDesktopWorkflowSubtitle(projectID, stage string) (string, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return "", err
	}
	snapshot, err := store.Snapshot(context.Background(), strings.TrimSpace(projectID))
	if err != nil {
		return "", err
	}
	taskID := strings.TrimSpace(snapshot.Project.WorkflowTaskID)
	if !taskIDPattern.MatchString(taskID) {
		return "", errors.New("start the source workflow before reading subtitle output")
	}
	var name string
	switch project.Stage(strings.TrimSpace(stage)) {
	case project.StageTranslation:
		name = "target_language_srt.srt"
	default:
		return "", errors.New("this workflow stage has no editable subtitle output")
	}
	content, err := os.ReadFile(filepath.Join("tasks", taskID, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("subtitle output is not ready yet")
		}
		return "", err
	}
	return string(content), nil
}

// workflowFailureDetail is safe to expose in the desktop UI. The actual
// worker diagnosis is retained because users need to act on it, while
// credential-shaped values are redacted and oversized output is bounded.
func workflowFailureDetail(err error) string {
	if err == nil {
		return "workflow failed without a reported reason"
	}
	detail := strings.TrimSpace(workflowSecretValuePattern.ReplaceAllString(err.Error(), "[redacted credential]"))
	if detail == "" {
		return "workflow failed without a reported reason"
	}
	const maxDetailLength = 900
	if len(detail) > maxDetailLength {
		detail = strings.TrimSpace(detail[:maxDetailLength]) + "…"
	}
	return detail
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func reviewStageForLegacyStatus(status string) (project.Stage, bool) {
	switch strings.TrimSpace(status) {
	case "awaiting_source_review":
		return project.StageSource, true
	case "awaiting_translation_review":
		return project.StageTranslation, true
	case "awaiting_dubbing_audio_review":
		return project.StageDubbingAudio, true
	case "awaiting_dubbing_video_review":
		return project.StageRender, true
	case "completed":
		return project.StageOutputs, true
	default:
		return "", false
	}
}

func autoApprovedStageForLegacyStatus(status string) (project.Stage, bool) {
	switch strings.TrimSpace(status) {
	case "source_approved":
		return project.StageSource, true
	case "translation_approved":
		return project.StageTranslation, true
	case "dubbing_audio_approved":
		return project.StageDubbingAudio, true
	case "dubbing_video_approved":
		return project.StageRender, true
	case "completed":
		return project.StageOutputs, true
	default:
		return "", false
	}
}

func latestProjectRun(runs []project.StageRun, stage project.Stage) *project.StageRun {
	for index := len(runs) - 1; index >= 0; index-- {
		if runs[index].Stage == stage {
			return &runs[index]
		}
	}
	return nil
}

func latestRunningProjectRun(runs []project.StageRun) *project.StageRun {
	for index := len(runs) - 1; index >= 0; index-- {
		if runs[index].Status == project.StatusRunning {
			return &runs[index]
		}
	}
	return nil
}

func latestFailedProjectRun(runs []project.StageRun) *project.StageRun {
	for index := len(runs) - 1; index >= 0; index-- {
		if runs[index].Status == project.StatusFailed {
			return &runs[index]
		}
	}
	return nil
}

func (a *App) MarkDesktopStageForReview(runID, messageKey string) (project.StageRun, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.StageRun{}, err
	}
	if strings.TrimSpace(messageKey) == "" {
		messageKey = "stage.review_required"
	}
	return store.MarkReviewRequired(context.Background(), strings.TrimSpace(runID), messageKey)
}

func (a *App) ApproveDesktopStage(runID string) (project.StageRun, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.StageRun{}, err
	}
	return store.ApproveStage(context.Background(), strings.TrimSpace(runID))
}

// SaveDesktopWorkflowDraft updates the underlying translated SRT only when
// that workflow exists, then persists the same reviewed content as an
// immutable KOVA artifact. Later stages keep notes locally because their
// actual artifacts are produced by the worker.
func (a *App) SaveDesktopWorkflowDraft(projectID, runID, stage, content string) (project.Artifact, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.Artifact{}, err
	}
	snapshot, err := store.Snapshot(context.Background(), strings.TrimSpace(projectID))
	if err != nil {
		return project.Artifact{}, err
	}
	workflowTaskID := strings.TrimSpace(snapshot.Project.WorkflowTaskID)
	switch project.Stage(strings.TrimSpace(stage)) {
	case project.StageTranslation:
		if !taskIDPattern.MatchString(workflowTaskID) {
			return project.Artifact{}, errors.New("start the source workflow before saving translated subtitles")
		}
		if err := a.updateExistingWorkflowSubtitle(workflowTaskID, "target", content); err != nil {
			return project.Artifact{}, err
		}
	}
	return a.SaveDesktopDraft(projectID, runID, stage, content)
}

func (a *App) updateExistingWorkflowSubtitle(workflowTaskID, kind, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("subtitle draft is empty")
	}
	payload, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	_, err = a.callLocalAPI(http.MethodPut, "/api/v1/jobs/subtitle/"+url.PathEscape(workflowTaskID)+"/subtitles/"+url.PathEscape(kind), payload)
	return err
}

// ApproveDesktopWorkflowStage advances only the corresponding legacy review
// gate and then marks the v2 stage approved. This preserves the user's right
// to inspect each output before opening its successor.
func (a *App) ApproveDesktopWorkflowStage(projectID, runID, stage string) (project.StageRun, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.StageRun{}, err
	}
	snapshot, err := store.Snapshot(context.Background(), strings.TrimSpace(projectID))
	if err != nil {
		return project.StageRun{}, err
	}
	workflowTaskID := strings.TrimSpace(snapshot.Project.WorkflowTaskID)
	var approvalSuffix string
	switch project.Stage(strings.TrimSpace(stage)) {
	case project.StageSource:
		approvalSuffix = "/source/approve"
	case project.StageTranslation:
		approvalSuffix = "/translation/approve"
	case project.StageDubbingAudio:
		approvalSuffix = "/dubbing/audio/approve"
	case project.StageRender:
		approvalSuffix = "/dubbing/video/approve"
	case project.StageOutputs:
		// The renderer has no further backend action. Approval records that the
		// user accepted the final files listed in the output stage.
	default:
		return project.StageRun{}, fmt.Errorf("unsupported approval stage: %s", stage)
	}
	if approvalSuffix != "" {
		if err := a.startExistingWorkflowStage(workflowTaskID, approvalSuffix, nil); err != nil {
			return project.StageRun{}, err
		}
	}
	return store.ApproveStage(context.Background(), strings.TrimSpace(runID))
}

// SaveDesktopDraft persists the user-reviewed text for the current stage as an
// immutable artifact. It intentionally does not advance or approve the stage;
// the user must still press the separate review and approval controls.
func (a *App) SaveDesktopDraft(projectID, runID, stage, content string) (project.Artifact, error) {
	projectID, runID, stage, content = strings.TrimSpace(projectID), strings.TrimSpace(runID), strings.TrimSpace(stage), strings.TrimSpace(content)
	if projectID == "" || runID == "" || content == "" {
		return project.Artifact{}, errors.New("project, stage run, and draft content are required")
	}
	if len(content) > 1<<20 {
		return project.Artifact{}, errors.New("draft content exceeds 1 MiB")
	}
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.Artifact{}, err
	}
	snapshot, err := store.Snapshot(context.Background(), projectID)
	if err != nil {
		return project.Artifact{}, err
	}
	var run *project.StageRun
	for index := range snapshot.StageRuns {
		candidate := &snapshot.StageRuns[index]
		if candidate.ID == runID {
			run = candidate
			break
		}
	}
	if run == nil || string(run.Stage) != stage {
		return project.Artifact{}, errors.New("stage run does not belong to this project stage")
	}
	if run.Status != project.StatusRunning && run.Status != project.StatusReviewNeeded {
		return project.Artifact{}, errors.New("draft can only be saved while a stage is running or under review")
	}

	root := a.desktopProjectDataRoot()
	relativePath := filepath.ToSlash(filepath.Join("projects", projectID, "drafts", fmt.Sprintf("%s-run-%s.txt", stage, runID)))
	absPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return project.Artifact{}, fmt.Errorf("create draft directory: %w", err)
	}
	temporaryPath := absPath + ".tmp"
	if err := os.WriteFile(temporaryPath, []byte(content+"\n"), 0600); err != nil {
		return project.Artifact{}, fmt.Errorf("write draft: %w", err)
	}
	if err := os.Rename(temporaryPath, absPath); err != nil {
		_ = os.Remove(temporaryPath)
		return project.Artifact{}, fmt.Errorf("commit draft: %w", err)
	}
	digest := sha256.Sum256([]byte(content + "\n"))
	return store.CreateArtifact(context.Background(), project.Artifact{
		ProjectID:  projectID,
		StageRunID: runID,
		Kind:       stage + "_review_draft",
		Path:       relativePath,
		Checksum:   fmt.Sprintf("%x", digest[:]),
		Revision:   run.InputRevision,
	})
}

func (a *App) desktopProjectDataRoot() string {
	a.projectMu.Lock()
	defer a.projectMu.Unlock()
	if a.projectDataRoot != "" {
		return a.projectDataRoot
	}
	return filepath.Dir(project.DefaultDatabasePath())
}

func (a *App) CheckVoiceHealth(request VoiceHealthRequest) VoiceHealth {
	baseURL, err := normalizeVoiceURL(request.BaseURL)
	if err != nil {
		return VoiceHealth{Message: err.Error()}
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/health", nil)
	if err != nil {
		return VoiceHealth{Message: err.Error()}
	}
	if token := strings.TrimSpace(request.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := a.httpClient.Do(req)
	if err != nil {
		return VoiceHealth{Message: "Không kết nối được Voice Studio / Cannot reach Voice Studio: " + err.Error()}
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return VoiceHealth{Status: response.StatusCode, Message: "Voice Studio returned " + response.Status}
	}
	return VoiceHealth{Reachable: true, Status: response.StatusCode, Data: body, Message: "Kết nối thành công / Connected"}
}

// CheckSTTHealth verifies the independent, CUDA-only Colab transcription
// worker before a source job starts. The worker's OpenAI-compatible endpoint
// is configured under /v1, but health deliberately stays at the tunnel root.
func (a *App) CheckSTTHealth(request VoiceHealthRequest) VoiceHealth {
	baseURL, err := normalizeColabSTTURL(request.BaseURL)
	if err != nil {
		return VoiceHealth{Message: err.Error()}
	}
	if strings.TrimSpace(request.Token) == "" {
		return VoiceHealth{Message: "Chưa dán token STT Google Colab / Missing Google Colab STT token"}
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return VoiceHealth{Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(request.Token))
	response, err := a.httpClient.Do(req)
	if err != nil {
		return VoiceHealth{Message: "Không kết nối được worker STT Colab / Cannot reach Colab STT worker: " + err.Error()}
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return VoiceHealth{Status: response.StatusCode, Message: "Colab STT worker returned " + response.Status}
	}
	var health struct {
		Ready  bool   `json:"ready"`
		Device string `json:"device"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		return VoiceHealth{Status: response.StatusCode, Message: "Phản hồi health STT không hợp lệ / Invalid STT health response"}
	}
	if !health.Ready || !strings.EqualFold(strings.TrimSpace(health.Device), "cuda") {
		return VoiceHealth{Status: response.StatusCode, Data: body, Message: "Worker STT chưa sẵn sàng CUDA / Colab STT worker is not CUDA-ready"}
	}
	return VoiceHealth{Reachable: true, Status: response.StatusCode, Data: body, Message: "Worker STT Colab CUDA đã sẵn sàng / Colab CUDA STT worker is ready"}
}

// CheckOCRHealth verifies the independent Visual OCR Colab worker before the
// source stage starts. OCR has its own URL/token because its CUDA environment
// contains Paddle/PaddleOCR rather than Faster-Whisper.
func (a *App) CheckOCRHealth(request VoiceHealthRequest) VoiceHealth {
	health, err := visualocr.CheckRemoteHealth(context.Background(), visualocr.RemoteConfig{
		BaseURL: request.BaseURL,
		Token:   request.Token,
		Client:  a.httpClient,
	})
	if err != nil {
		return VoiceHealth{Message: err.Error()}
	}
	body, _ := json.Marshal(health)
	return VoiceHealth{
		Reachable: true,
		Status:    http.StatusOK,
		Data:      body,
		Message:   "Worker OCR Colab CUDA is ready",
	}
}

// SelectVoiceReferenceAudio opens the native picker for a reference clip.
func (a *App) SelectVoiceReferenceAudio() (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	selected, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Chọn audio mẫu để clone giọng",
		Filters: []runtime.FileFilter{{
			DisplayName: "Audio mẫu (WAV, MP3, FLAC)",
			Pattern:     "*.wav;*.mp3;*.flac",
		}},
	})
	if err != nil {
		return "", fmt.Errorf("choose reference audio: %w", err)
	}
	return strings.TrimSpace(selected), nil // empty means the user cancelled
}

// SelectSourceVideo opens the native video picker. Returning a local: value
// is deliberate: it preserves the workflow contract and lets the service copy
// the selected video into the task folder for preview and later rendering.
func (a *App) SelectSourceVideo() (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	selected, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Chọn video nguồn cho KOVA",
		Filters: []runtime.FileFilter{{
			DisplayName: "Video (MP4, MOV, MKV, WEBM, AVI)",
			Pattern:     "*.mp4;*.mov;*.mkv;*.webm;*.avi;*.m4v",
		}},
	})
	if err != nil {
		return "", fmt.Errorf("choose source video: %w", err)
	}
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return "", nil
	}
	return "local:" + selected, nil
}

// OpenShortVideoSession opens only KOVA's isolated, persistent browser
// profile. It does not touch the user's normal Chrome/Edge profile. This is
// the explicit recovery path when Douyin/TikTok asks for login or CAPTCHA.
func (a *App) OpenShortVideoSession(sourceURL, browser string) error {
	if a.ctx == nil {
		return errors.New("application is not ready")
	}
	strategy, err := normalizeDesktopSourceCookieBrowser(browser)
	if err != nil {
		return err
	}
	return service.OpenManagedShortVideoSession(sourceURL, strategy)
}

// GetCapCutDraftSettings exposes only non-secret local export settings. The
// first common Windows CapCut Draft location is suggested but never enabled
// until the user explicitly saves the setting in KOVA.
func (a *App) GetCapCutDraftSettings() CapCutDraftSettings {
	configuredRoot := strings.TrimSpace(config.Conf.Creator.CapCutDraftRoot)
	detectedRoot := ""
	if configuredRoot == "" {
		detectedRoot = detectCapCutDraftRoot()
	}
	pythonPath := strings.TrimSpace(config.Conf.Creator.PythonPath)
	if pythonPath == "" {
		pythonPath = "python"
	}
	backend := strings.TrimSpace(config.Conf.Creator.CompilerBackend)
	if backend == "" {
		backend = "pycapcut"
	}
	return CapCutDraftSettings{
		Enabled:           config.Conf.Creator.CompileDraft,
		Backend:           backend,
		DraftRoot:         configuredRoot,
		DetectedDraftRoot: detectedRoot,
		PythonPath:        pythonPath,
	}
}

// SelectCapCutDraftRoot opens the native directory picker. KOVA writes a new
// draft folder below this root; it never alters an existing CapCut project.
func (a *App) SelectCapCutDraftRoot() (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	selected, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Chọn thư mục CapCut Drafts",
	})
	if err != nil {
		return "", fmt.Errorf("choose CapCut Draft root: %w", err)
	}
	return strings.TrimSpace(selected), nil
}

// SaveCapCutDraftSettings enables direct native-project export after the user
// has selected CapCut's own Drafts folder. It deliberately does not install
// Python packages or create a project until the output stage is started.
func (a *App) SaveCapCutDraftSettings(settings CapCutDraftSettings) error {
	root := strings.TrimSpace(settings.DraftRoot)
	if root == "" {
		root = detectCapCutDraftRoot()
	}
	if settings.Enabled {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return errors.New("hãy chọn thư mục CapCut Drafts hợp lệ trước khi bật xuất project chỉnh sửa")
		}
	}
	config.Conf.Creator.CompilerBackend = "pycapcut"
	config.Conf.Creator.CapCutDraftRoot = root
	config.Conf.Creator.CompileDraft = settings.Enabled
	if pythonPath := strings.TrimSpace(settings.PythonPath); pythonPath != "" {
		config.Conf.Creator.PythonPath = pythonPath
	}
	if strings.TrimSpace(config.Conf.Creator.PyCapCutBridgePath) == "" {
		config.Conf.Creator.PyCapCutBridgePath = filepath.Join("scripts", "kova_pycapcut_builder.py")
	}
	return config.SaveConfig()
}

func detectCapCutDraftRoot() string {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(localAppData, "CapCut", "CapCut Drafts"),
		filepath.Join(localAppData, "JianyingPro", "User Data", "Projects", "com.lveditor.draft"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (a *App) CheckVisualOCR() VisualOCRHealth {
	runner := visualocr.Runner{Config: visualocr.Config{
		PythonPath: config.Conf.VisualOCR.PythonPath,
		ScriptPath: config.Conf.VisualOCR.ScriptPath,
	}}
	result, err := runner.Preflight(context.Background())
	if err != nil {
		return VisualOCRHealth{Message: fmt.Sprintf("OCR chưa sẵn sàng: %v", err)}
	}
	return VisualOCRHealth{
		Ready:         true,
		CUDAAvailable: result.CUDAAvailable,
		Python:        result.Python,
		Message:       "OCR local sẵn sàng.",
	}
}

// InstallVisualOCR installs the optional local OCR dependencies only after an
// explicit user action in the UI. It never runs automatically before a video
// download, avoiding surprise Python package installs during a workflow.
func (a *App) InstallVisualOCR() VisualOCRHealth {
	runner := visualocr.Runner{Config: visualocr.Config{
		PythonPath: config.Conf.VisualOCR.PythonPath,
		ScriptPath: config.Conf.VisualOCR.ScriptPath,
	}}
	result, err := runner.InstallDependencies(context.Background())
	if err != nil {
		return VisualOCRHealth{Message: fmt.Sprintf("Không thể cài OCR local: %v", err)}
	}
	return VisualOCRHealth{
		Ready:         true,
		CUDAAvailable: result.CUDAAvailable,
		Python:        result.Python,
		Message:       "Đã cài và kiểm tra OCR local thành công.",
	}
}

// CreateVoiceProfile streams one consented WAV/MP3/FLAC reference to Voice
// Studio and then saves a private local backup outside project folders. Tokens
// and the original source path are never written to that library.
func (a *App) CreateVoiceProfile(request VoiceProfileCreateRequest) (VoiceProfile, error) {
	if !request.ConsentConfirmed {
		return VoiceProfile{}, errors.New("confirm that you have permission to use this voice before creating a profile")
	}
	baseURL, err := normalizeVoiceURL(request.BaseURL)
	if err != nil {
		return VoiceProfile{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len([]rune(name)) > 120 {
		return VoiceProfile{}, errors.New("voice name is required and must be at most 120 characters")
	}
	localPath := strings.TrimSpace(request.ReferenceAudioPath)
	if localPath == "" {
		return VoiceProfile{}, errors.New("choose a WAV, MP3, or FLAC reference audio file")
	}
	absolutePath, err := filepath.Abs(localPath)
	if err != nil {
		return VoiceProfile{}, fmt.Errorf("resolve reference audio path: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return VoiceProfile{}, fmt.Errorf("read reference audio: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return VoiceProfile{}, errors.New("reference audio must be a non-empty file")
	}
	const maxReferenceBytes = 256 * 1024 * 1024
	if info.Size() > maxReferenceBytes {
		return VoiceProfile{}, fmt.Errorf("reference audio exceeds %d MiB", maxReferenceBytes/(1024*1024))
	}
	if extension := strings.ToLower(filepath.Ext(absolutePath)); extension != ".wav" && extension != ".mp3" && extension != ".flac" {
		return VoiceProfile{}, errors.New("reference audio must be WAV, MP3, or FLAC")
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		return VoiceProfile{}, fmt.Errorf("open reference audio: %w", err)
	}
	defer file.Close()
	// Stream the file through an io.Pipe so selecting a reference clip never
	// makes KOVA retain a second full copy in desktop memory.
	pipeReader, pipeWriter := io.Pipe()
	defer pipeReader.Close()
	writer := multipart.NewWriter(pipeWriter)
	writeErr := make(chan error, 1)
	go func() {
		defer pipeWriter.Close()
		defer writer.Close()
		fields := map[string]string{
			"name":              name,
			"consent_confirmed": "true",
			"language":          firstNonEmpty(strings.TrimSpace(request.Language), "vi"),
		}
		for field, value := range fields {
			if err := writer.WriteField(field, value); err != nil {
				writeErr <- err
				return
			}
		}
		part, err := writer.CreateFormFile("ref_audio", filepath.Base(absolutePath))
		if err != nil {
			writeErr <- err
			return
		}
		_, err = io.Copy(part, io.LimitReader(file, maxReferenceBytes+1))
		writeErr <- err
	}()

	httpRequest, err := http.NewRequest(http.MethodPost, baseURL+"/profiles", pipeReader)
	if err != nil {
		return VoiceProfile{}, err
	}
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	if token := strings.TrimSpace(request.Token); token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return VoiceProfile{}, fmt.Errorf("upload reference audio to Voice Studio: %w", err)
	}
	defer response.Body.Close()
	if err := <-writeErr; err != nil {
		return VoiceProfile{}, fmt.Errorf("encode reference-audio upload: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return VoiceProfile{}, fmt.Errorf("read Voice Studio response: %w", readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return VoiceProfile{}, fmt.Errorf("Voice Studio profile upload returned %s: %s", response.Status, workflowFailureDetail(errors.New(string(responseBody))))
	}
	var payload struct {
		ID      string       `json:"id"`
		Profile VoiceProfile `json:"profile"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return VoiceProfile{}, fmt.Errorf("decode Voice Studio profile response: %w", err)
	}
	if strings.TrimSpace(payload.Profile.ID) == "" {
		payload.Profile.ID = strings.TrimSpace(payload.ID)
		payload.Profile.Name = name
		payload.Profile.Language = firstNonEmpty(strings.TrimSpace(request.Language), "vi")
		payload.Profile.Status = "ready"
	}
	if strings.TrimSpace(payload.Profile.ID) == "" {
		return VoiceProfile{}, errors.New("Voice Studio returned an empty voice profile id")
	}
	saved, err := a.saveVoiceProfileBackup(payload.Profile, baseURL, absolutePath)
	if err != nil {
		return VoiceProfile{}, err
	}
	// New KOVA Voice Studio workers export the Demucs-cleaned reference. Swap
	// the private fallback copy immediately so a later Colab reset restores a
	// music-free profile. Older workers intentionally retain the local original
	// rather than making profile creation fail just because they predate this
	// endpoint.
	if payload.Profile.ReferenceClean {
		_ = a.importRemoteVoiceBackup(baseURL, request.Token, payload.Profile)
	}
	return saved, nil
}

func (a *App) ListTTSOptions() []TTSOption {
	return []TTSOption{
		{ID: "omnivoice", LabelVI: "KOVA Voice Studio (clone giọng cố định)", LabelEN: "KOVA Voice Studio (fixed voice clone)", Provider: "omnivoice", Model: "k2-fsa/OmniVoice", NeedsWorker: true, NeedsProfile: true},
		{ID: "gateway-google-vi", LabelVI: "Google TTS tiếng Việt qua API Gateway", LabelEN: "Google TTS Vietnamese through API Gateway", Provider: "gateway", Model: "google-tts/vi"},
		{ID: "gateway-google-en", LabelVI: "Google TTS tiếng Anh qua API Gateway", LabelEN: "Google TTS English through API Gateway", Provider: "gateway", Model: "google-tts/en"},
		{ID: "gateway-edge-vi-female", LabelVI: "Edge TTS tiếng Việt · Hoài My", LabelEN: "Edge TTS Vietnamese · Hoai My", Provider: "gateway", Model: "edge-tts/vi-VN-HoaiMyNeural"},
		{ID: "gateway-edge-vi-male", LabelVI: "Edge TTS tiếng Việt · Nam Minh", LabelEN: "Edge TTS Vietnamese · Nam Minh", Provider: "gateway", Model: "edge-tts/vi-VN-NamMinhNeural"},
	}
}

func (a *App) ListTranslationModels() []TranslationModelOption {
	models := config.GatewayFreeLLMModels()
	options := make([]TranslationModelOption, 0, len(models))
	for _, model := range models {
		options = append(options, TranslationModelOption{ID: model.ID, LabelVI: model.LabelVI, LabelEN: model.LabelEN})
	}
	return options
}

var desktopSTTOptions = []STTOption{
	{ID: "colab-fasterwhisper-medium", LabelVI: "Google Colab GPU · Faster-Whisper Medium (khuyến nghị)", LabelEN: "Google Colab GPU · Faster-Whisper Medium (recommended)", Provider: "colab", Model: "medium", NeedsWorker: true},
	{ID: "fasterwhisper-tiny", LabelVI: "Faster-Whisper · Tiny (cục bộ, nhanh)", LabelEN: "Faster-Whisper · Tiny (local, fast)", Provider: "fasterwhisper", Model: "tiny"},
	{ID: "fasterwhisper-medium", LabelVI: "Faster-Whisper · Medium (cục bộ, khuyến nghị)", LabelEN: "Faster-Whisper · Medium (local, recommended)", Provider: "fasterwhisper", Model: "medium"},
	{ID: "fasterwhisper-large-v2", LabelVI: "Faster-Whisper · Large V2 (cục bộ, chính xác hơn)", LabelEN: "Faster-Whisper · Large V2 (local, more accurate)", Provider: "fasterwhisper", Model: "large-v2"},
}

func (a *App) ListSTTOptions() []STTOption {
	return append([]STTOption(nil), desktopSTTOptions...)
}

func isDesktopRemoteSTTOption(optionID string) bool {
	optionID = strings.TrimSpace(optionID)
	if optionID == "" {
		optionID = "colab-fasterwhisper-medium"
	}
	for _, option := range desktopSTTOptions {
		if option.ID == optionID {
			return option.NeedsWorker
		}
	}
	return false
}

func normalizeDesktopSourceMethod(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "speech_to_text":
		return "speech_to_text", nil
	case "visual_ocr":
		return "visual_ocr", nil
	case "speech_to_text_and_visual_ocr":
		return "speech_to_text_and_visual_ocr", nil
	default:
		return "", fmt.Errorf("phương thức tạo script không hợp lệ: %s", raw)
	}
}

func desktopSourceMethodUsesSTT(method string) bool {
	return method == "speech_to_text" || method == "speech_to_text_and_visual_ocr"
}

func desktopSourceMethodUsesOCR(method string) bool {
	return method == "visual_ocr" || method == "speech_to_text_and_visual_ocr"
}

func normalizeDesktopOCREngine(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "colab":
		return "colab", nil
	case "local":
		return "local", nil
	default:
		return "", errors.New("OCR engine must be Google Colab or local")
	}
}

func normalizeDesktopReviewMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "auto") {
		return "auto"
	}
	return "manual"
}

// normalizeDesktopSourceCookieBrowser stores only a strategy name. Browser
// cookie values stay inside yt-dlp's local child process and never cross the
// Wails bridge or enter project/workflow persistence.
func normalizeDesktopSourceCookieBrowser(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto", nil
	case "none", "chrome", "edge":
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("trÃ¬nh duyá»‡t cookie cho nguá»“n khÃ´ng há»£p lá»‡: %s", raw)
	}
}

func configureDesktopSTT(optionID, workerURL, workerToken string) error {
	optionID = strings.TrimSpace(optionID)
	if optionID == "" {
		optionID = "colab-fasterwhisper-medium"
	}
	for _, option := range desktopSTTOptions {
		if option.ID != optionID {
			continue
		}
		config.Conf.Transcribe.Provider = option.Provider
		switch option.Provider {
		case "colab":
			return config.ConfigureRemoteColabTranscription(workerURL, workerToken, option.Model)
		case "fasterwhisper":
			config.Conf.Transcribe.Fasterwhisper.Model = option.Model
			config.Conf.Transcribe.RemoteAudioSeparation = false
			// Explicitly discard an earlier remote session token when the user
			// switches back to local STT.
			config.Conf.Transcribe.Openai.SessionAPIKey = ""
		default:
			return fmt.Errorf("STT provider không được KOVA desktop hỗ trợ: %s", option.Provider)
		}
		return nil
	}
	return fmt.Errorf("tùy chọn speech-to-text không hợp lệ: %s", optionID)
}

func normalizeVoiceURL(raw string) (string, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("URL Voice Studio không hợp lệ / invalid Voice Studio URL")
	}
	localHost := strings.EqualFold(u.Hostname(), "localhost") || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && localHost) {
		return "", errors.New("Voice Studio phải dùng HTTPS, trừ localhost / Voice Studio must use HTTPS except localhost")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func normalizeColabSTTURL(raw string) (string, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("URL worker STT phải là HTTPS công khai / STT worker URL must be public HTTPS")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "", errors.New("không cho phép worker STT cục bộ; hãy dùng URL tunnel từ Google Colab")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return "", errors.New("không cho phép worker STT cục bộ; hãy dùng URL tunnel từ Google Colab")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func localAPIBaseURL() string {
	host := strings.TrimSpace(config.Conf.Server.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, config.Conf.Server.Port)
}
