package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"kova/config"
	"kova/internal/api"
	"kova/internal/handler"
	"kova/log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"go.uber.org/zap"
)

type SubtitleManager struct {
	window             fyne.Window
	handler            *handler.Handler
	videoUrl           string
	videoPaths         []string
	audioPath          string
	uploadedAudioURL   string
	sourceLang         string
	targetLang         string
	bilingualEnabled   bool
	bilingualPosition  int
	voiceoverEnabled   bool
	ttsVoiceCode       string
	fillerFilter       bool
	wordReplacements   []api.WordReplacement
	protectedTerms     []string
	embedSubtitle      string // none, horizontal, vertical, all
	verticalTitles     [2]string
	progressBar        *widget.ProgressBar
	progressLabel      *widget.Label
	progressPanel      *fyne.Container
	downloadContainer  *fyne.Container
	downloadPanel      *fyne.Container
	tipsLabel          *widget.Label
	tipsPanel          *fyne.Container
	onVideoSelected    func(string)
	onVideosSelected   func([]string)
	onAudioSelected    func(string)
	voiceoverAudioPath string
	voiceCloneConsent  bool
	multiTaskResults   []taskResult
	stageOutputs       map[string]func(string)
	onTaskFinished     func(error)
	// workflowHTTPClient is injectable for focused tests. Normal desktop runs
	// use workflowClient(), which always has a finite deadline so an embedded
	// backend that stalls cannot leave a visible Start button disabled forever.
	workflowHTTPClient *http.Client
}

const defaultWorkflowRequestTimeout = 15 * time.Second

type taskResult struct {
	fileName          string
	subtitleInfo      []api.SubtitleResult
	videoOutputs      []api.SubtitleResult
	speechDownloadURL string
	taskId            string
	artifacts         []api.ArtifactResult
}

// WorkflowSnapshot is the desktop representation of a staged Kova job.  The
// server deliberately returns a snapshot after every user-controlled action
// so the desktop never has to guess that a later stage may start.
//
// It is kept in the desktop package instead of the public API package because
// it describes the native review UI (editable SRT URLs and enabled actions),
// not the legacy one-click subtitle endpoint.
type WorkflowSnapshot struct {
	TaskID            string               `json:"task_id"`
	SourceURL         string               `json:"source_url"`
	CurrentStage      string               `json:"current_stage"`
	ProcessPercent    int                  `json:"process_percent"`
	Message           string               `json:"message"`
	FailureReason     string               `json:"failure_reason"`
	SourceSRTURL      string               `json:"source_srt_url"`
	TranslatedSRTURL  string               `json:"translated_srt_url"`
	SourceTextURL     string               `json:"source_text_url"`
	TranslatedTextURL string               `json:"translated_text_url"`
	BilingualSRTURL   string               `json:"bilingual_srt_url"`
	Artifacts         []api.ArtifactResult `json:"artifacts"`
	CanStart          map[string]bool      `json:"can_start"`
}

func NewSubtitleManager(window fyne.Window) *SubtitleManager {
	return &SubtitleManager{
		window:            window,
		sourceLang:        "en",
		targetLang:        "vi",
		bilingualEnabled:  false,
		bilingualPosition: 1,
		fillerFilter:      true,
		voiceoverEnabled:  false,
		ttsVoiceCode:      "",
		embedSubtitle:     "none",
		downloadContainer: container.NewVBox(),
		tipsLabel:         widget.NewLabel(""),
		videoPaths:        make([]string, 0),
		stageOutputs:      make(map[string]func(string)),
	}
}

// SetStageOutputCallback connects a visible native panel to one pipeline
// stage. It avoids asking users to infer stage results from a browser or a
// typed filesystem path.
func (sm *SubtitleManager) SetStageOutputCallback(stage string, callback func(string)) {
	if callback == nil {
		delete(sm.stageOutputs, stage)
		return
	}
	sm.stageOutputs[stage] = callback
}

// SetTaskFinishedCallback lets the shared workflow controller restore every
// Start button after either a completed job or an error. It prevents the old
// page-05-only button from silently disappearing on a failed request.
func (sm *SubtitleManager) SetTaskFinishedCallback(callback func(error)) {
	sm.onTaskFinished = callback
}

func (sm *SubtitleManager) finishTask(err error) {
	if sm.onTaskFinished != nil {
		sm.onTaskFinished(err)
	}
}

func (sm *SubtitleManager) reportStageOutput(stage, message string) {
	if callback := sm.stageOutputs[stage]; callback != nil {
		callback(message)
	}
}

func (sm *SubtitleManager) reportTranslationConfiguration() {
	sm.reportStageOutput("translation", fmt.Sprintf("Sẽ tạo SRT với nguồn %s → đích %s.", sm.sourceLang, sm.targetLang))
}

func (sm *SubtitleManager) SetVideoSelectedCallback(callback func(string)) {
	sm.onVideoSelected = callback
}

func (sm *SubtitleManager) SetVideosSelectedCallback(callback func([]string)) {
	sm.onVideosSelected = callback
}

func (sm *SubtitleManager) ShowFileDialog() {
	sm.videoPaths = make([]string, 0)

	sm.addVideoFile(false)
}

func (sm *SubtitleManager) addVideoFile(continueAdding bool) {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, sm.window)
			return
		}
		if reader == nil {
			if len(sm.videoPaths) > 0 {
				confirmDialog := dialog.NewConfirm(
					"Tải file lên / Upload",
					fmt.Sprintf("Đã chọn %d file. Bắt đầu tải lên? / Start upload?", len(sm.videoPaths)),
					func(confirm bool) {
						if confirm {
							sm.uploadMultipleFiles()
						}
					},
					sm.window)
				confirmDialog.Show()
			}
			return
		}
		defer reader.Close()

		filePath := reader.URI().Path()

		sm.videoPaths = append(sm.videoPaths, filePath)

		filesMessage := fmt.Sprintf("Đã chọn %d file / Selected files:\n", len(sm.videoPaths))
		for i, path := range sm.videoPaths {
			filesMessage += fmt.Sprintf("%d. %s\n", i+1, filepath.Base(path))
		}
		filesMessage += "\nTiếp tục thêm file? / Continue adding files?"

		confirmDialog := dialog.NewConfirm(
			"Tiếp tục chọn / Continue",
			filesMessage,
			func(cont bool) {
				if cont {
					sm.addVideoFile(true)
				} else {
					sm.uploadMultipleFiles()
				}
			},
			sm.window,
		)
		confirmDialog.Show()
	}, sm.window)

	fd.SetFilter(storage.NewExtensionFileFilter([]string{".mp4", ".mov", ".avi", ".mkv", ".wmv"}))
	fd.Show()
}

func (sm *SubtitleManager) uploadMultipleFiles() {
	if len(sm.videoPaths) == 0 {
		return
	}

	filesList := fmt.Sprintf("Tải lên %d file / Uploading:\n", len(sm.videoPaths))
	for i, path := range sm.videoPaths {
		filesList += fmt.Sprintf("%d. %s\n", i+1, filepath.Base(path))
	}

	progressDialog := dialog.NewProgress("Đang tải lên / Uploading", filesList, sm.window)
	progressDialog.Show()

	go func() {
		defer progressDialog.Hide()

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		for i, filePath := range sm.videoPaths {
			file, err := os.Open(filePath)
			if err != nil {
				dialog.ShowError(err, sm.window)
				return
			}

			part, err := writer.CreateFormFile("file", filepath.Base(filePath))
			if err != nil {
				file.Close()
				dialog.ShowError(err, sm.window)
				return
			}

			_, err = io.Copy(part, file)
			file.Close()
			if err != nil {
				dialog.ShowError(err, sm.window)
				return
			}

			progressDialog.SetValue(float64(i+1) / float64(len(sm.videoPaths)))
		}

		err := writer.Close()
		if err != nil {
			dialog.ShowError(err, sm.window)
			return
		}

		resp, err := http.Post(fmt.Sprintf("http://%s:%d/api/v1/files", config.Conf.Server.Host, config.Conf.Server.Port), writer.FormDataContentType(), body)
		if err != nil {
			dialog.ShowError(err, sm.window)
			return
		}
		defer resp.Body.Close()

		var result struct {
			Error int    `json:"error"`
			Msg   string `json:"msg"`
			Data  struct {
				FilePaths []string `json:"file_paths"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			dialog.ShowError(err, sm.window)
			return
		}

		if result.Error != 0 && result.Error != 200 {
			dialog.ShowError(fmt.Errorf("%s", result.Msg), sm.window)
			return
		}

		tempPaths := make([]string, len(result.Data.FilePaths))
		copy(tempPaths, result.Data.FilePaths)
		sm.videoPaths = tempPaths

		if len(result.Data.FilePaths) > 0 {
			sm.videoUrl = result.Data.FilePaths[0]
		}

		if sm.onVideosSelected != nil {
			sm.onVideosSelected(result.Data.FilePaths)
		} else if sm.onVideoSelected != nil && len(result.Data.FilePaths) > 0 {
			sm.onVideoSelected(result.Data.FilePaths[0])
		}
		if len(result.Data.FilePaths) > 0 {
			names := make([]string, 0, len(result.Data.FilePaths))
			for _, path := range result.Data.FilePaths {
				names = append(names, filepath.Base(path))
			}
			sm.reportStageOutput("source", "Output nguồn đã sẵn sàng: "+strings.Join(names, ", "))
		}

		successMessage := fmt.Sprintf("Đã tải lên %d file / Uploaded files:\n", len(result.Data.FilePaths))
		for i, url := range result.Data.FilePaths {
			successMessage += fmt.Sprintf("%d. %s\n", i+1, filepath.Base(url))
		}

		dialog.ShowInformation("Tải lên thành công / Upload complete", successMessage, sm.window)
	}()
}

func (sm *SubtitleManager) ShowAudioFileDialog() {
	fileDialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, sm.window)
			return
		}
		if reader == nil {
			return
		}
		defer reader.Close()

		// Preserve the selected extension. Renaming MP3/M4A bytes to .wav makes
		// a clone backend mis-detect the codec, so a native picker must carry the
		// file type through unchanged.
		ext := filepath.Ext(reader.URI().Path())
		if ext == "" {
			ext = ".wav"
		}
		tempFile, err := os.CreateTemp("", "audio-*"+ext)
		if err != nil {
			dialog.ShowError(err, sm.window)
			return
		}
		defer tempFile.Close()

		_, err = io.Copy(tempFile, reader)
		if err != nil {
			dialog.ShowError(err, sm.window)
			return
		}

		sm.voiceoverAudioPath = tempFile.Name()
		sm.reportStageOutput("voice", "Audio tham chiếu đã chọn: "+filepath.Base(tempFile.Name()))
		if sm.onAudioSelected != nil {
			sm.onAudioSelected(tempFile.Name())
		}
	}, sm.window)
	fileDialog.SetFilter(storage.NewExtensionFileFilter([]string{".wav", ".mp3", ".m4a", ".flac", ".ogg"}))
	fileDialog.Show()
}

func (sm *SubtitleManager) uploadVideo(localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("không thể mở file / failed to open file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(localPath))
	if err != nil {
		return fmt.Errorf("không thể tạo form upload / failed to create upload form: %w", err)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return fmt.Errorf("không thể đọc nội dung file / failed to copy file data: %w", err)
	}
	writer.Close()

	resp, err := http.Post(fmt.Sprintf("http://%s:%d/api/v1/files", config.Conf.Server.Host, config.Conf.Server.Port), writer.FormDataContentType(), body)
	if err != nil {
		return fmt.Errorf("tải file lên thất bại / upload failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  struct {
			FilePath string `json:"file_path"`
		} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return fmt.Errorf("không thể đọc phản hồi / invalid response: %w", err)
	}

	if result.Error != 0 && result.Error != 200 {
		return fmt.Errorf("%s", result.Msg)
	}

	sm.videoUrl = result.Data.FilePath
	return nil
}

func (sm *SubtitleManager) uploadAudio() error {
	file, err := os.Open(sm.audioPath)
	if err != nil {
		return fmt.Errorf("không thể mở file / failed to open file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(sm.audioPath))
	if err != nil {
		return fmt.Errorf("không thể tạo form upload / failed to create upload form: %w", err)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return fmt.Errorf("không thể đọc nội dung file / failed to copy file data: %w", err)
	}
	writer.Close()

	resp, err := http.Post(fmt.Sprintf("http://%s:%d/api/v1/files", config.Conf.Server.Host, config.Conf.Server.Port), writer.FormDataContentType(), body)
	if err != nil {
		return fmt.Errorf("tải file lên thất bại / upload failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  struct {
			FilePath string `json:"file_path"`
		} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return fmt.Errorf("không thể đọc phản hồi / invalid response: %w", err)
	}

	if result.Error != 0 && result.Error != 200 {
		return fmt.Errorf("%s", result.Msg)
	}

	sm.uploadedAudioURL = result.Data.FilePath
	return nil
}

func (sm *SubtitleManager) SetSourceLang(lang string) {
	sm.sourceLang = lang
	sm.reportTranslationConfiguration()
}

func (sm *SubtitleManager) SetTargetLang(lang string) {
	sm.targetLang = lang
	sm.reportTranslationConfiguration()
}

func (sm *SubtitleManager) SetBilingualEnabled(enabled bool) {
	sm.bilingualEnabled = enabled
}

func (sm *SubtitleManager) SetBilingualPosition(position int) {
	sm.bilingualPosition = position
}

func (sm *SubtitleManager) SetFillerFilter(enabled bool) {
	sm.fillerFilter = enabled
}

// SetProtectedTerms retains user-approved proper names through translation.
// The service protects each term with a stable token before the LLM call and
// restores it before SRT and TTS artifacts are produced.
func (sm *SubtitleManager) SetProtectedTerms(terms []string) {
	sm.protectedTerms = append([]string(nil), terms...)
	if len(terms) == 0 {
		sm.reportStageOutput("translation", "Tên riêng giữ nguyên: không có (không bắt buộc).")
		return
	}
	sm.reportStageOutput("translation", "Tên riêng sẽ được giữ nguyên: "+strings.Join(terms, ", "))
}

func (sm *SubtitleManager) SetVoiceoverEnabled(enabled bool) {
	sm.voiceoverEnabled = enabled
	if enabled {
		sm.reportStageOutput("voice", "Lồng tiếng được bật. Chọn một profile hoặc audio clone.")
	} else {
		sm.reportStageOutput("voice", "Lồng tiếng đang tắt; job chỉ tạo phụ đề/output không có audio dub.")
	}
}

func (sm *SubtitleManager) SetVoiceCloneConsent(consent bool) {
	sm.voiceCloneConsent = consent
}

func (sm *SubtitleManager) SetTtsVoiceCode(code string) {
	sm.ttsVoiceCode = code
	if strings.TrimSpace(code) != "" {
		sm.reportStageOutput("voice", "Profile giọng cố định: "+code)
	}
}

func (sm *SubtitleManager) SetEmbedSubtitle(mode string) {
	sm.embedSubtitle = mode
	if mode == "none" {
		sm.reportStageOutput("export", "MP4 chưa được bật; output sẽ gồm SRT và audio khi có lồng tiếng.")
	} else {
		sm.reportStageOutput("export", "MP4 có phụ đề sẽ được tạo theo chế độ: "+mode)
	}
}

func (sm *SubtitleManager) SetVerticalTitles(mainTitle, subTitle string) {
	sm.verticalTitles = [2]string{mainTitle, subTitle}
}

func (sm *SubtitleManager) SetProgressBar(progress *widget.ProgressBar) {
	sm.progressBar = progress
}

func (sm *SubtitleManager) SetDownloadContainer(container *fyne.Container) {
	sm.downloadContainer = container
}

func (sm *SubtitleManager) SetProgressPanel(panel *fyne.Container) {
	sm.progressPanel = panel
}

func (sm *SubtitleManager) SetDownloadPanel(panel *fyne.Container) {
	sm.downloadPanel = panel
}

func (sm *SubtitleManager) SetTipsPanel(panel *fyne.Container) {
	sm.tipsPanel = panel
}

// PrepareRun resets only the visible run/output widgets. The source and the
// user's configuration remain intact, so correcting a missing key or worker
// URL never forces them to start over.
func (sm *SubtitleManager) PrepareRun() {
	if sm.progressBar != nil {
		sm.progressBar.SetValue(0)
		sm.progressBar.Show()
	}
	if sm.progressLabel != nil {
		sm.progressLabel.SetText("0%")
		sm.progressLabel.Show()
	}
	if sm.progressPanel != nil {
		sm.progressPanel.Show()
	}
	if sm.downloadContainer != nil {
		sm.downloadContainer.Objects = nil
		sm.downloadContainer.Hide()
	}
	if sm.downloadPanel != nil {
		sm.downloadPanel.Hide()
	}
	if sm.tipsLabel != nil {
		sm.tipsLabel.SetText("")
		sm.tipsLabel.Hide()
	}
	if sm.tipsPanel != nil {
		sm.tipsPanel.Hide()
	}
}

func (sm *SubtitleManager) SetTipsLabel(label *widget.Label) {
	sm.tipsLabel = label
}

func (sm *SubtitleManager) SetAudioSelectedCallback(callback func(string)) {
	sm.onAudioSelected = callback
}

func (sm *SubtitleManager) SetVideoUrl(url string) {
	sm.videoUrl = url
	if strings.TrimSpace(url) != "" {
		sm.reportStageOutput("source", "Nguồn URL đã chọn: "+url)
	}
}

func (sm *SubtitleManager) GetVideoUrl() string {
	return sm.videoUrl
}

func (sm *SubtitleManager) SetProgressLabel(label *widget.Label) {
	sm.progressLabel = label
}

func (sm *SubtitleManager) StartTask() error {
	if len(sm.videoPaths) > 1 {
		go sm.processMultipleVideos()
		return nil
	} else if len(sm.videoPaths) == 1 {
		sm.videoUrl = sm.videoPaths[0]
	}

	task := &api.SubtitleTask{
		URL:                     sm.videoUrl,
		Language:                "vi",
		OriginLang:              sm.sourceLang,
		TargetLang:              sm.targetLang,
		Bilingual:               boolToInt(sm.bilingualEnabled),
		TranslationSubtitlePos:  sm.bilingualPosition,
		TTS:                     boolToInt(sm.voiceoverEnabled),
		TTSVoiceCode:            sm.ttsVoiceCode,
		TTSVoiceCloneSrcFileURL: sm.voiceoverAudioPath,
		VoiceCloneConsent:       sm.voiceCloneConsent,
		VttSwitch:               true,
		ModalFilter:             boolToInt(sm.fillerFilter),
		ProtectTerms:            append([]string(nil), sm.protectedTerms...),
		EmbedSubtitleVideoType:  sm.embedSubtitle,
		VerticalMajorTitle:      sm.verticalTitles[0],
		VerticalMinorTitle:      sm.verticalTitles[1],
	}

	jsonData, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("không thể mã hóa dữ liệu job / failed to encode job: %w", err)
	}

	resp, err := http.Post(fmt.Sprintf("http://%s:%d/api/v1/jobs/subtitle", config.Conf.Server.Host, config.Conf.Server.Port), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("không thể gửi job / failed to submit job: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  struct {
			TaskId string `json:"task_id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("không thể đọc phản hồi / invalid response: %w", err)
	}

	if result.Error != 0 && result.Error != 200 {
		return fmt.Errorf("%s", result.Msg)
	}

	go sm.pollTaskStatus(result.Data.TaskId)
	return nil
}

// StartSourceWorkflow starts only step 01.  Unlike StartTask it intentionally
// does not translate, synthesize speech, or render a video.  The user must
// review and approve the source SRT before those separate endpoints can run.
func (sm *SubtitleManager) StartSourceWorkflow() (WorkflowSnapshot, error) {
	if len(sm.videoPaths) > 1 {
		return WorkflowSnapshot{}, fmt.Errorf("quy trình theo bước hiện hỗ trợ kiểm tra từng video; hãy chọn một video cho mỗi job")
	}
	if len(sm.videoPaths) == 1 && strings.TrimSpace(sm.videoUrl) == "" {
		sm.videoUrl = sm.videoPaths[0]
	}
	if strings.TrimSpace(sm.videoUrl) == "" {
		return WorkflowSnapshot{}, fmt.Errorf("chưa có nguồn video hoặc URL")
	}

	// The staged source endpoint persists all output settings, but keeps TTS
	// disabled until the user explicitly starts step 03.
	task := sm.newWorkflowTask()
	return sm.requestWorkflow(http.MethodPost, "/api/v1/jobs/subtitle/stages/source", task)
}

func (sm *SubtitleManager) newWorkflowTask() *api.SubtitleTask {
	return &api.SubtitleTask{
		URL:                    sm.videoUrl,
		Language:               "vi",
		OriginLang:             sm.sourceLang,
		TargetLang:             sm.targetLang,
		Bilingual:              boolToInt(sm.bilingualEnabled),
		TranslationSubtitlePos: sm.bilingualPosition,
		// The staged worker only enables speech after the user starts the
		// dubbing stage.  Sending false here prevents accidental local or
		// remote voice work while the source/translation is being reviewed.
		TTS:                    boolToInt(false),
		TTSVoiceCode:           sm.ttsVoiceCode,
		VttSwitch:              true,
		ModalFilter:            boolToInt(sm.fillerFilter),
		ProtectTerms:           append([]string(nil), sm.protectedTerms...),
		EmbedSubtitleVideoType: sm.embedSubtitle,
		VerticalMajorTitle:     sm.verticalTitles[0],
		VerticalMinorTitle:     sm.verticalTitles[1],
	}
}

// GetWorkflowSnapshot asks the server for the current staged workflow state.
// It is used by the native progress poller and never triggers work itself.
func (sm *SubtitleManager) GetWorkflowSnapshot(taskID string) (WorkflowSnapshot, error) {
	return sm.requestWorkflow(http.MethodGet, "/api/v1/jobs/subtitle/"+taskID+"/workflow", nil)
}

func (sm *SubtitleManager) SaveWorkflowSRT(taskID, kind, content string) (WorkflowSnapshot, error) {
	path, err := workflowSubtitlePath(taskID, kind)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if strings.TrimSpace(content) == "" {
		return WorkflowSnapshot{}, fmt.Errorf("nội dung SRT không được để trống")
	}
	return sm.requestWorkflow(http.MethodPut, path, map[string]string{"content": content})
}

func (sm *SubtitleManager) ApproveWorkflowStage(taskID, stage string) (WorkflowSnapshot, error) {
	if strings.TrimSpace(taskID) == "" {
		return WorkflowSnapshot{}, fmt.Errorf("chưa có job để duyệt")
	}
	var path string
	switch stage {
	case "source":
		path = "/api/v1/jobs/subtitle/" + taskID + "/source/approve"
	case "translation":
		path = "/api/v1/jobs/subtitle/" + taskID + "/translation/approve"
	case "dubbing", "dubbing_audio":
		path = "/api/v1/jobs/subtitle/" + taskID + "/dubbing/audio/approve"
	case "dubbing_video":
		path = "/api/v1/jobs/subtitle/" + taskID + "/dubbing/video/approve"
	case "dubbing_legacy":
		path = "/api/v1/jobs/subtitle/" + taskID + "/dubbing/approve"
	default:
		return WorkflowSnapshot{}, fmt.Errorf("bước duyệt không hợp lệ: %s", stage)
	}
	return sm.requestWorkflow(http.MethodPost, path, map[string]any{})
}

func (sm *SubtitleManager) StartWorkflowStage(taskID, stage string) (WorkflowSnapshot, error) {
	if strings.TrimSpace(taskID) == "" {
		return WorkflowSnapshot{}, fmt.Errorf("chưa có job để chạy bước này")
	}
	base := "/api/v1/jobs/subtitle/" + taskID
	switch stage {
	case "translation":
		return sm.requestWorkflow(http.MethodPost, base+"/translation", map[string]any{})
	case "render":
		return sm.requestWorkflow(http.MethodPost, base+"/render", map[string]any{})
	case "dubbing", "dubbing_audio":
		return sm.requestWorkflow(http.MethodPost, base+"/dubbing/audio", sm.workflowDubbingPayload())
	case "dubbing_video":
		return sm.requestWorkflow(http.MethodPost, base+"/dubbing/video", map[string]any{})
	default:
		return WorkflowSnapshot{}, fmt.Errorf("bước chạy không hợp lệ: %s", stage)
	}
}

// SkipWorkflowDubbing is an explicit user decision, persisted by the server.
// It is important after a failed optional TTS action: simply changing local UI
// state would leave the workflow locked in the failed stage and unable to
// render a subtitle-only MP4.
func (sm *SubtitleManager) SkipWorkflowDubbing(taskID string) (WorkflowSnapshot, error) {
	if strings.TrimSpace(taskID) == "" {
		return WorkflowSnapshot{}, fmt.Errorf("chưa có job để bỏ qua lồng tiếng")
	}
	return sm.requestWorkflow(http.MethodPost, "/api/v1/jobs/subtitle/"+taskID+"/dubbing/skip", map[string]any{})
}

func (sm *SubtitleManager) workflowDubbingPayload() map[string]any {
	referenceAudio := ""
	cloneConsent := false
	// A gateway/preset voice must not inherit an old OmniVoice reference
	// selected earlier in the form. Sending it would make the server treat a
	// non-clone TTS job as an unsupported clone request.
	if strings.EqualFold(strings.TrimSpace(config.Conf.Tts.Provider), "omnivoice") {
		referenceAudio = workflowReferenceAudioURL(sm.voiceoverAudioPath)
		cloneConsent = sm.voiceCloneConsent
	}
	return map[string]any{
		"tts_voice_code":               sm.ttsVoiceCode,
		"tts_voice_clone_src_file_url": referenceAudio,
		"voice_clone_consent":          cloneConsent,
	}
}

func workflowSubtitlePath(taskID, kind string) (string, error) {
	switch kind {
	case "source":
		return "/api/v1/jobs/subtitle/" + taskID + "/subtitles/source", nil
	case "translated":
		return "/api/v1/jobs/subtitle/" + taskID + "/subtitles/translated", nil
	default:
		return "", fmt.Errorf("loại SRT không hợp lệ: %s", kind)
	}
}

func workflowReferenceAudioURL(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "local:") || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return "local:" + path
}

func (sm *SubtitleManager) requestWorkflow(method, path string, payload any) (WorkflowSnapshot, error) {
	endpoint := sm.workflowEndpoint(path)
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return WorkflowSnapshot{}, fmt.Errorf("không thể mã hóa yêu cầu workflow: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("không thể tạo yêu cầu workflow: %w", err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := sm.workflowClient().Do(request)
	if err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("không thể kết nối workflow Kova: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		return WorkflowSnapshot{}, fmt.Errorf("workflow Kova trả về %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return decodeWorkflowSnapshot(response.Body)
}

func decodeWorkflowSnapshot(reader io.Reader) (WorkflowSnapshot, error) {
	var envelope struct {
		Error int             `json:"error"`
		Msg   string          `json:"msg"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(reader).Decode(&envelope); err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("không thể đọc phản hồi workflow: %w", err)
	}
	if envelope.Error != 0 && envelope.Error != http.StatusOK {
		return WorkflowSnapshot{}, fmt.Errorf("workflow thất bại: %s", envelope.Msg)
	}
	var wrapped struct {
		TaskID   string            `json:"task_id"`
		Workflow *WorkflowSnapshot `json:"workflow"`
	}
	if err := json.Unmarshal(envelope.Data, &wrapped); err == nil && wrapped.Workflow != nil {
		if wrapped.Workflow.TaskID == "" {
			wrapped.Workflow.TaskID = wrapped.TaskID
		}
		return *wrapped.Workflow, nil
	}
	var snapshot WorkflowSnapshot
	if err := json.Unmarshal(envelope.Data, &snapshot); err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("phản hồi workflow không hợp lệ: %w", err)
	}
	if snapshot.TaskID == "" {
		snapshot.TaskID = wrapped.TaskID
	}
	return snapshot, nil
}

// ReadWorkflowText downloads an editable SRT/script artifact.  The service
// may return either a relative Kova file path or a fully qualified URL.
func (sm *SubtitleManager) ReadWorkflowText(location string) (string, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return "", fmt.Errorf("workflow chưa cung cấp file để xem")
	}
	if !strings.HasPrefix(location, "http://") && !strings.HasPrefix(location, "https://") {
		location = sm.workflowEndpoint(location)
	}
	response, err := sm.workflowClient().Get(location)
	if err != nil {
		return "", fmt.Errorf("không thể tải nội dung để kiểm tra: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("không thể tải nội dung để kiểm tra: %s", response.Status)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024))
	if err != nil {
		return "", fmt.Errorf("không thể đọc nội dung để kiểm tra: %w", err)
	}
	return string(content), nil
}

// workflowClient intentionally does not use http.DefaultClient. The desktop
// workflow has a visible busy state while it waits for the embedded server;
// an unbounded request would make that state permanent when the local server
// accepts a connection but stops responding.
func (sm *SubtitleManager) workflowClient() *http.Client {
	if sm.workflowHTTPClient != nil {
		return sm.workflowHTTPClient
	}
	return &http.Client{Timeout: defaultWorkflowRequestTimeout}
}

func (sm *SubtitleManager) workflowEndpoint(path string) string {
	host := strings.TrimSpace(config.Conf.Server.Host)
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("http://%s:%d%s", host, config.Conf.Server.Port, path)
}

// displayWorkflowArtifacts reuses the existing native download controls for
// final and intermediate artifact manifests returned by the staged API.
func (sm *SubtitleManager) displayWorkflowArtifacts(snapshot WorkflowSnapshot) {
	if len(snapshot.Artifacts) == 0 {
		return
	}
	sm.multiTaskResults = []taskResult{{
		fileName:  filepath.Base(sm.videoUrl),
		taskId:    snapshot.TaskID,
		artifacts: snapshot.Artifacts,
	}}
	sm.displayMultiTaskDownloadLinks()
}

func (sm *SubtitleManager) processMultipleVideos() {
	originalURL := sm.videoUrl

	sm.multiTaskResults = make([]taskResult, 0, len(sm.videoPaths))
	sm.PrepareRun()

	go func() {
		for i, url := range sm.videoPaths {
			fileName := filepath.Base(url)

			percentage := float64(i) / float64(len(sm.videoPaths))
			sm.progressBar.SetValue(percentage)

			if sm.progressLabel != nil {
				displayName := fileName
				if len(displayName) > 20 {
					displayName = displayName[:17] + "..."
				}
				sm.progressLabel.SetText(fmt.Sprintf("Đang xử lý / Processing: %d/%d\n%s", i+1, len(sm.videoPaths), displayName))
				sm.progressLabel.Show()
			}

			sm.videoUrl = url

			task := &api.SubtitleTask{
				URL:                     url,
				Language:                "vi",
				OriginLang:              sm.sourceLang,
				TargetLang:              sm.targetLang,
				Bilingual:               boolToInt(sm.bilingualEnabled),
				TranslationSubtitlePos:  sm.bilingualPosition,
				TTS:                     boolToInt(sm.voiceoverEnabled),
				TTSVoiceCode:            sm.ttsVoiceCode,
				TTSVoiceCloneSrcFileURL: sm.voiceoverAudioPath,
				VoiceCloneConsent:       sm.voiceCloneConsent,
				VttSwitch:               true,
				ModalFilter:             boolToInt(sm.fillerFilter),
				ProtectTerms:            append([]string(nil), sm.protectedTerms...),
				EmbedSubtitleVideoType:  sm.embedSubtitle,
				VerticalMajorTitle:      sm.verticalTitles[0],
				VerticalMinorTitle:      sm.verticalTitles[1],
			}

			jsonData, err := json.Marshal(task)
			if err != nil {
				log.GetLogger().Error("Failed to encode job data", zap.Error(err))
				continue
			}

			resp, err := http.Post(fmt.Sprintf("http://%s:%d/api/v1/jobs/subtitle", config.Conf.Server.Host, config.Conf.Server.Port), "application/json", bytes.NewBuffer(jsonData))
			if err != nil {
				log.GetLogger().Error("Failed to submit job", zap.Error(err))
				continue
			}

			var result struct {
				Error int    `json:"error"`
				Msg   string `json:"msg"`
				Data  struct {
					TaskId string `json:"task_id"`
				} `json:"data"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				resp.Body.Close()
				log.GetLogger().Error("Failed to parse response", zap.Error(err))
				continue
			}
			resp.Body.Close()

			if result.Error != 0 && result.Error != 200 {
				log.GetLogger().Error("Failed to create job", zap.String("msg", result.Msg))
				continue
			}

			taskRes := sm.waitTaskCompleted(result.Data.TaskId, fileName)

			sm.multiTaskResults = append(sm.multiTaskResults, taskRes)
		}

		sm.videoUrl = originalURL

		sm.displayMultiTaskDownloadLinks()

		dialog.ShowInformation("Hoàn tất / Complete", "Đã xử lý toàn bộ video / All videos completed", sm.window)
		sm.finishTask(nil)
	}()
}

func (sm *SubtitleManager) waitTaskCompleted(taskId string, originalFileName string) taskResult {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastPercent := 0

	res := taskResult{
		fileName: originalFileName,
		taskId:   taskId,
	}

	for {
		resp, err := http.Get(fmt.Sprintf("http://%s:%d/api/v1/jobs/subtitle?taskId=%s", config.Conf.Server.Host, config.Conf.Server.Port, taskId))
		if err != nil {
			log.GetLogger().Error("Failed to get job status", zap.Error(err))
			time.Sleep(5 * time.Second)
			continue
		}

		var result struct {
			Error int    `json:"error"`
			Msg   string `json:"msg"`
			Data  struct {
				ProcessPercent    int                  `json:"process_percent"`
				SubtitleInfo      []api.SubtitleResult `json:"subtitle_info"`
				VideoOutputs      []api.SubtitleResult `json:"video_outputs"`
				Artifacts         []api.ArtifactResult `json:"artifacts"`
				SpeechDownloadURL string               `json:"speech_download_url"`
				TaskId            string               `json:"task_id"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			log.GetLogger().Error("Failed to parse response", zap.Error(err))
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		if result.Data.ProcessPercent != lastPercent {
			progress := float64(result.Data.ProcessPercent) / 100.0
			sm.progressBar.SetValue(progress)

			if sm.progressLabel != nil {
				sm.progressLabel.SetText(fmt.Sprintf("%d%%", result.Data.ProcessPercent))
				sm.progressLabel.Show()
			}

			lastPercent = result.Data.ProcessPercent
		}

		if result.Data.ProcessPercent >= 100 {
			res.subtitleInfo = result.Data.SubtitleInfo
			res.videoOutputs = result.Data.VideoOutputs
			res.artifacts = result.Data.Artifacts
			res.speechDownloadURL = result.Data.SpeechDownloadURL
			break
		}

		time.Sleep(2 * time.Second)
	}

	return res
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 2
}

func (sm *SubtitleManager) pollTaskStatus(taskId string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastPercent := 0

	for range ticker.C {
		resp, err := http.Get(fmt.Sprintf("http://%s:%d/api/v1/jobs/subtitle?taskId=%s", config.Conf.Server.Host, config.Conf.Server.Port, taskId))
		if err != nil {
			log.GetLogger().Error("Failed to get job status", zap.Error(err))
			sm.finishTask(fmt.Errorf("không thể lấy trạng thái job / failed to get job status: %w", err))
			return
		}

		var result struct {
			Error int    `json:"error"`
			Msg   string `json:"msg"`
			Data  struct {
				ProcessPercent    int                  `json:"process_percent"`
				SubtitleInfo      []api.SubtitleResult `json:"subtitle_info"`
				VideoOutputs      []api.SubtitleResult `json:"video_outputs"`
				Artifacts         []api.ArtifactResult `json:"artifacts"`
				SpeechDownloadURL string               `json:"speech_download_url"`
				TaskId            string               `json:"task_id"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			log.GetLogger().Error("Failed to parse response", zap.Error(err))
			resp.Body.Close()
			sm.finishTask(fmt.Errorf("không thể đọc trạng thái job: %w", err))
			return
		}
		resp.Body.Close()

		if result.Error != 0 {
			log.GetLogger().Error("Failed to get job status", zap.String("msg", result.Msg))
			sm.finishTask(fmt.Errorf("job thất bại: %s", result.Msg))
			return
		}

		if result.Data.ProcessPercent != lastPercent {
			progress := float64(result.Data.ProcessPercent) / 100.0
			sm.progressBar.SetValue(progress)

			if sm.progressLabel != nil {
				sm.progressLabel.SetText(fmt.Sprintf("%d%%", result.Data.ProcessPercent))
				sm.progressLabel.Show()
			}

			lastPercent = result.Data.ProcessPercent
		}

		if result.Data.ProcessPercent >= 100 {
			artifactNames := make([]string, 0, len(result.Data.Artifacts))
			for _, artifact := range result.Data.Artifacts {
				if strings.TrimSpace(artifact.Label) != "" {
					artifactNames = append(artifactNames, artifact.Label)
				}
			}
			if len(artifactNames) > 0 {
				previewCount := len(artifactNames)
				if previewCount > 2 {
					previewCount = 2
				}
				sm.reportStageOutput("source", "Artifact nguồn đã sẵn sàng: "+strings.Join(artifactNames[:previewCount], ", "))
			}
			subtitleNames := make([]string, 0, len(result.Data.SubtitleInfo))
			for _, subtitle := range result.Data.SubtitleInfo {
				subtitleNames = append(subtitleNames, subtitle.Name)
			}
			if len(subtitleNames) > 0 {
				sm.reportStageOutput("translation", "Output SRT đã sẵn sàng: "+strings.Join(subtitleNames, ", "))
			}
			if result.Data.SpeechDownloadURL != "" {
				sm.reportStageOutput("voice", "Output audio lồng tiếng đã sẵn sàng. Bấm nút lưu audio ở mục 05.")
			}
			if len(result.Data.VideoOutputs) > 0 {
				videoNames := make([]string, 0, len(result.Data.VideoOutputs))
				for _, video := range result.Data.VideoOutputs {
					videoNames = append(videoNames, video.Name)
				}
				sm.reportStageOutput("export", "Output MP4 đã sẵn sàng: "+strings.Join(videoNames, ", "))
			} else if sm.embedSubtitle != "none" {
				sm.reportStageOutput("export", "Không có MP4 được trả về. Kiểm tra log render trước khi lưu output.")
			}
			taskRes := taskResult{
				fileName:          filepath.Base(sm.videoUrl),
				subtitleInfo:      result.Data.SubtitleInfo,
				videoOutputs:      result.Data.VideoOutputs,
				artifacts:         result.Data.Artifacts,
				speechDownloadURL: result.Data.SpeechDownloadURL,
				taskId:            result.Data.TaskId,
			}

			sm.multiTaskResults = []taskResult{taskRes}

			sm.displayMultiTaskDownloadLinks()

			sm.tipsLabel.SetText("Hoàn tất. Dùng các nút Lưu SRT, Lưu audio hoặc Lưu MP4 để chọn vị trí lưu trên máy.")
			sm.tipsLabel.Show()
			if sm.tipsPanel != nil {
				sm.tipsPanel.Show()
			}
			sm.finishTask(nil)

			return
		}
	}
}

func (sm *SubtitleManager) displayMultiTaskDownloadLinks() {
	sm.downloadContainer.Objects = []fyne.CanvasObject{}

	if len(sm.multiTaskResults) == 0 {
		return
	}

	allTasksContainer := container.NewVBox()

	for taskIndex, taskRes := range sm.multiTaskResults {
		taskLabel := widget.NewLabelWithStyle(
			fmt.Sprintf("File: %s", taskRes.fileName),
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		)

		taskContainer := container.NewVBox(taskLabel)

		if len(taskRes.artifacts) > 0 {
			taskContainer.Add(widget.NewLabelWithStyle("Output theo thứ tự pipeline:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
			for _, artifact := range taskRes.artifacts {
				downloadURL := strings.TrimSpace(artifact.DownloadURL)
				if downloadURL == "" {
					continue
				}
				name := strings.TrimSpace(artifact.Name)
				if name == "" {
					name = filepath.Base(downloadURL)
				}
				label := strings.TrimSpace(artifact.Label)
				if label == "" {
					label = strings.TrimSpace(artifact.Kind)
				}
				button := widget.NewButton("Lưu "+label+": "+name, func(url, suggested string) func() {
					return func() {
						go sm.downloadFile(url, suggested)
					}
				}(downloadURL, name))
				if strings.Contains(artifact.Kind, "video") {
					button.Importance = widget.HighImportance
				} else {
					button.Importance = widget.MediumImportance
				}
				taskContainer.Add(button)
			}
		} else {
			// Backward-compatible fallback for older servers that do not yet return
			// the ordered artifact manifest.
			for _, result := range taskRes.subtitleInfo {
				downloadURL := result.DownloadURL
				fileName := result.Name
				btn := widget.NewButton("Lưu SRT: "+fileName, func(url string) func() {
					return func() { go sm.downloadFile(url, filepath.Base(url)) }
				}(downloadURL))
				btn.Importance = widget.MediumImportance
				taskContainer.Add(btn)
			}
			for _, result := range taskRes.videoOutputs {
				downloadURL := result.DownloadURL
				fileName := result.Name
				btn := widget.NewButton("Lưu MP4: "+fileName, func(url, suggested string) func() {
					return func() { go sm.downloadFile(url, suggested) }
				}(downloadURL, fileName))
				btn.Importance = widget.HighImportance
				taskContainer.Add(btn)
			}
			if taskRes.speechDownloadURL != "" {
				url := taskRes.speechDownloadURL
				ttsFileName := fmt.Sprintf("tts_%s.wav", filepath.Base(taskRes.speechDownloadURL))
				speechBtn := widget.NewButton("Lưu audio: "+ttsFileName, func(u, f string) func() {
					return func() { go sm.downloadFile(u, f) }
				}(url, ttsFileName))
				speechBtn.Importance = widget.MediumImportance
				taskContainer.Add(speechBtn)
			}
		}

		taskTip := widget.NewLabel("Mỗi nút Lưu mở hộp thoại chọn vị trí trên máy. Không cần nhập đường dẫn.")
		taskTip.Alignment = fyne.TextAlignCenter
		taskContainer.Add(taskTip)

		if taskIndex < len(sm.multiTaskResults)-1 {
			divider := canvas.NewLine(color.NRGBA{R: 200, G: 200, B: 200, A: 128})
			divider.StrokeWidth = 1
			taskContainer.Add(divider)
		}

		allTasksContainer.Add(taskContainer)
	}

	sm.downloadContainer.Add(allTasksContainer)
	sm.downloadContainer.Show()
	if sm.downloadPanel != nil {
		sm.downloadPanel.Show()
	}
}

func (sm *SubtitleManager) downloadFile(downloadURL, suggestedFileName string) {
	resp, err := http.Get(fmt.Sprintf("http://%s:%d", config.Conf.Server.Host, config.Conf.Server.Port) + downloadURL)
	if err != nil {
		dialog.ShowError(fmt.Errorf("không thể tải output: %w", err), sm.window)
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_ = resp.Body.Close()
		dialog.ShowError(fmt.Errorf("không thể tải output: máy chủ trả về %s", resp.Status), sm.window)
		return
	}

	saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			_ = resp.Body.Close()
			dialog.ShowError(err, sm.window)
			return
		}
		if writer == nil {
			_ = resp.Body.Close()
			return
		}
		defer writer.Close()
		defer resp.Body.Close()

		_, err = io.Copy(writer, resp.Body)
		if err != nil {
			dialog.ShowError(fmt.Errorf("không thể lưu file / failed to save file: %v", err), sm.window)
			return
		}

		dialog.ShowInformation("Tải xuống hoàn tất / Download complete", "Đã lưu file / File saved", sm.window)
	}, sm.window)

	saveDialog.SetFileName(suggestedFileName)
	saveDialog.Show()
}
