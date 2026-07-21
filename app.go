package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kova/config"
	"kova/internal/project"
	"kova/internal/server"
	"kova/log"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

const defaultColabNotebookURL = "https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb"

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
	Name             string         `json:"name"`
	LegacyAPIBaseURL string         `json:"legacy_api_base_url"`
	ColabNotebookURL string         `json:"colab_notebook_url"`
	Stages           []DesktopStage `json:"stages"`
	Locales          []string       `json:"locales"`
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

// VoiceProfile deliberately carries no reference-audio path or token.  KOVA
// stores only an opaque profile ID; reference audio remains inside Voice Studio.
type VoiceProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Language string `json:"language"`
	Status   string `json:"status"`
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
	ID       string `json:"id"`
	LabelVI  string `json:"label_vi"`
	LabelEN  string `json:"label_en"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type DesktopProjectRequest struct {
	Name           string `json:"name"`
	TargetLanguage string `json:"target_language"`
}

type DesktopWorkflowStartRequest struct {
	ProjectID          string `json:"project_id"`
	Stage              string `json:"stage"`
	SourceURL          string `json:"source_url"`
	STTOptionID        string `json:"stt_option_id"`
	TranslationModelID string `json:"translation_model_id"`
	TTSOptionID        string `json:"tts_option_id"`
	VoiceProfileID     string `json:"voice_profile_id"`
	WorkerURL          string `json:"worker_url"`
	WorkerToken        string `json:"worker_token"`
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

type DesktopWorkflowProgressStep struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Percent uint8  `json:"percent"`
	Detail  string `json:"detail,omitempty"`
}

type DesktopWorkflowSnapshot struct {
	WorkflowTaskID   string                        `json:"workflow_task_id"`
	CurrentStage     string                        `json:"current_stage"`
	FailedStage      string                        `json:"failed_stage,omitempty"`
	ProcessPercent   uint8                         `json:"process_percent"`
	Message          string                        `json:"message"`
	FailureReason    string                        `json:"failure_reason,omitempty"`
	ReviewRequired   bool                          `json:"review_required"`
	SourceSRTURL     string                        `json:"source_srt_url,omitempty"`
	TranslatedSRTURL string                        `json:"translated_srt_url,omitempty"`
	SourceSteps      []DesktopWorkflowProgressStep `json:"source_steps,omitempty"`
	Artifacts        []DesktopWorkflowArtifact     `json:"artifacts,omitempty"`
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
		Name:             "KOVA",
		LegacyAPIBaseURL: localAPIBaseURL(),
		ColabNotebookURL: defaultColabNotebookURL,
		Locales:          []string{"vi", "en"},
		Stages: []DesktopStage{
			{ID: "source", Number: "01", TitleVI: "Nguồn video", TitleEN: "Video source"},
			{ID: "translation", Number: "02", TitleVI: "Dịch và phụ đề", TitleEN: "Translation and subtitles"},
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

func (a *App) StartDesktopStage(projectID, stage string) (project.StageRun, error) {
	store, err := a.desktopProjectStore()
	if err != nil {
		return project.StageRun{}, err
	}
	return store.StartStage(context.Background(), strings.TrimSpace(projectID), project.Stage(stage))
}

// StartDesktopWorkflowStage records an explicit project-stage run and then
// invokes exactly one matching legacy workflow operation. It never chains
// later stages automatically; completion is observed with RefreshDesktopWorkflow
// and must still be reviewed and approved by the user.
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
	if request.Stage == string(project.StageSource) {
		if err := configureDesktopSTT(request.STTOptionID); err != nil {
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
		payload, err := json.Marshal(map[string]any{
			"url":                           sourceURL,
			"origin_lang":                   "auto",
			"target_lang":                   desktopProject.TargetLanguage,
			"bilingual":                     0,
			"translation_subtitle_pos":      1,
			"modal_filter":                  0,
			"tts":                           0,
			"language":                      desktopProject.TargetLanguage,
			"embed_subtitle_video_type":     "horizontal",
			"origin_language_word_one_line": 12,
			"vtt_switch":                    false,
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
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/translation", nil)
	case project.StageDubbingAudio:
		payload, err := a.configureDesktopTTS(request)
		if err != nil {
			return workflowTaskID, err
		}
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/dubbing/audio", payload)
	case project.StageRender:
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/dubbing/video", nil)
	case project.StageOutputs:
		return workflowTaskID, a.startExistingWorkflowStage(workflowTaskID, "/render", nil)
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
	// The token is session-only: this method updates runtime memory for the
	// immediately requested stage and never calls config.SaveConfig.
	config.Conf.Tts.Omnivoice.BaseUrl = workerURL
	config.Conf.Tts.Omnivoice.SessionApiKey = strings.TrimSpace(request.WorkerToken)
	return json.Marshal(map[string]any{
		"tts_voice_code":               "profile:" + profileID,
		"tts_voice_clone_src_file_url": "profile:" + profileID,
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
	if stage, ok := reviewStageForLegacyStatus(workflow.CurrentStage); ok {
		if run := latestProjectRun(snapshot.StageRuns, stage); run != nil && run.Status == project.StatusRunning {
			if _, err := store.MarkReviewRequired(context.Background(), run.ID, "stage.review_required"); err != nil {
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

// ReadDesktopWorkflowSubtitle returns the actual review SRT produced by the
// worker. The renderer never receives a filesystem path; it asks only for a
// source or translated subtitle belonging to the selected project's validated
// workflow task ID.
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
	case project.StageSource:
		name = "origin_language_srt.srt"
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

// SaveDesktopWorkflowDraft updates the underlying source/translation SRT only
// when that workflow exists, then persists the same reviewed content as an
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
	case project.StageSource:
		if taskIDPattern.MatchString(workflowTaskID) {
			if err := a.updateExistingWorkflowSubtitle(workflowTaskID, "source", content); err != nil {
				return project.Artifact{}, err
			}
		}
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

// ListVoiceProfiles populates a dropdown only after the user has supplied a
// worker URL and token. It never returns worker paths or reference audio.
func (a *App) ListVoiceProfiles(request VoiceHealthRequest) ([]VoiceProfile, error) {
	baseURL, err := normalizeVoiceURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/voices?status=ready", nil)
	if err != nil {
		return nil, err
	}
	if token := strings.TrimSpace(request.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot load Voice Studio profiles: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Voice Studio returned %s", response.Status)
	}
	var profiles []VoiceProfile
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode Voice Studio profiles: %w", err)
	}
	return profiles, nil
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
	{ID: "fasterwhisper-tiny", LabelVI: "Faster-Whisper · Tiny (cục bộ, nhanh)", LabelEN: "Faster-Whisper · Tiny (local, fast)", Provider: "fasterwhisper", Model: "tiny"},
	{ID: "fasterwhisper-medium", LabelVI: "Faster-Whisper · Medium (cục bộ, khuyến nghị)", LabelEN: "Faster-Whisper · Medium (local, recommended)", Provider: "fasterwhisper", Model: "medium"},
	{ID: "fasterwhisper-large-v2", LabelVI: "Faster-Whisper · Large V2 (cục bộ, chính xác hơn)", LabelEN: "Faster-Whisper · Large V2 (local, more accurate)", Provider: "fasterwhisper", Model: "large-v2"},
}

func (a *App) ListSTTOptions() []STTOption {
	return append([]STTOption(nil), desktopSTTOptions...)
}

func configureDesktopSTT(optionID string) error {
	optionID = strings.TrimSpace(optionID)
	if optionID == "" {
		optionID = "fasterwhisper-medium"
	}
	for _, option := range desktopSTTOptions {
		if option.ID != optionID {
			continue
		}
		config.Conf.Transcribe.Provider = option.Provider
		switch option.Provider {
		case "fasterwhisper":
			config.Conf.Transcribe.Fasterwhisper.Model = option.Model
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

func localAPIBaseURL() string {
	host := strings.TrimSpace(config.Conf.Server.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, config.Conf.Server.Port)
}
