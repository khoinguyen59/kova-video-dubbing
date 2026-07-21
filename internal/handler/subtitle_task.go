package handler

import (
	"fmt"
	"kova/internal/deps"
	"kova/internal/dto"
	"kova/internal/response"
	"kova/internal/service"
	"kova/log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h Handler) StartSubtitleTask(c *gin.Context) {
	var req dto.StartVideoSubtitleTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		log.GetLogger().Error("StartSubtitleTask ShouldBindJSON err", zap.Error(err))
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Dữ liệu không hợp lệ / Invalid request",
			Data:  nil,
		})
		return
	}

	if configUpdated {
		log.GetLogger().Info("Kova configuration changed; reinitializing services")
		deps.CheckDependency()
		h.Service = service.NewService()
		configUpdated = false
	}

	svc := h.Service

	data, err := svc.StartSubtitleTask(req)
	if err != nil {
		response.R(c, response.Response{
			Error: -1,
			Msg:   err.Error(),
			Data:  nil,
		})
		return
	}
	response.R(c, response.Response{
		Error: 0,
		Msg:   "Thành công / Success",
		Data:  data,
	})
}

func (h Handler) GetSubtitleTask(c *gin.Context) {
	var req dto.GetVideoSubtitleTaskReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Dữ liệu không hợp lệ / Invalid request",
			Data:  nil,
		})
		return
	}

	if configUpdated {
		log.GetLogger().Info("Kova configuration changed; reinitializing services")
		h.Service = service.NewService()
		configUpdated = false
	}

	svc := h.Service
	data, err := svc.GetTaskStatus(req)
	if err != nil {
		response.R(c, response.Response{
			Error: -1,
			Msg:   err.Error(),
			Data:  nil,
		})
		return
	}
	response.R(c, response.Response{
		Error: 0,
		Msg:   "Thành công / Success",
		Data:  data,
	})
}

// StartWorkflowSource starts only the source-download/script-extraction stage.
// The caller explicitly selects speech-to-text or Visual OCR; both stop at
// the same editable source-SRT review gate.
// It deliberately does not use StartSubtitleTask because that legacy endpoint
// automatically continues into translation, TTS and rendering.
func (h Handler) StartWorkflowSource(c *gin.Context) {
	var req dto.StartVideoSubtitleTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		workflowError(c, fmt.Errorf("dữ liệu nguồn không hợp lệ: %w", err))
		return
	}
	// The Wails application starts its API in-process, so unlike the legacy
	// server entrypoint it has not necessarily initialised command paths yet.
	// Prepare only media tools here. KOVA prepares the local STT engine after
	// video/audio completes, so the first-run engine/model download is shown
	// under the separate speech-to-text progress phase.
	if err := deps.CheckPlatformSubtitleDependency(); err != nil {
		workflowError(c, fmt.Errorf("không thể chuẩn bị công cụ tải nguồn video: %w", err))
		return
	}
	svc := h.currentWorkflowService()
	svc.RefreshTranscriptionClient()
	data, err := svc.StartWorkflowSource(req)
	workflowResponse(c, data, err)
}

func (h Handler) GetWorkflow(c *gin.Context) {
	data, err := h.currentWorkflowService().GetWorkflow(c.Param("taskId"))
	workflowResponse(c, data, err)
}

func (h Handler) UpdateWorkflowSubtitle(c *gin.Context) {
	var req dto.UpdateWorkflowSubtitleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		workflowError(c, fmt.Errorf("nội dung SRT không hợp lệ: %w", err))
		return
	}
	data, err := h.currentWorkflowService().UpdateWorkflowSubtitle(c.Param("taskId"), c.Param("kind"), req.Content)
	workflowResponse(c, data, err)
}

func (h Handler) ApproveWorkflowSource(c *gin.Context) {
	data, err := h.currentWorkflowService().ApproveWorkflowStage(c.Param("taskId"), "source")
	workflowResponse(c, data, err)
}

func (h Handler) StartWorkflowTranslation(c *gin.Context) {
	// The desktop may have just entered a session-only KOVA Gateway key. The
	// server and its initial translator are already running at that point, so
	// refresh only the translation clients immediately before this explicit
	// user-started stage. No source/download/TTS work is restarted here.
	svc := h.currentWorkflowService()
	svc.RefreshTranslationClients()
	data, err := svc.StartWorkflowTranslation(c.Param("taskId"))
	workflowResponse(c, data, err)
}

func (h Handler) ApproveWorkflowTranslation(c *gin.Context) {
	data, err := h.currentWorkflowService().ApproveWorkflowStage(c.Param("taskId"), "translation")
	workflowResponse(c, data, err)
}

func (h Handler) StartWorkflowDubbing(c *gin.Context) {
	h.StartWorkflowDubbingAudio(c)
}

// StartWorkflowDubbingAudio starts speech synthesis only. The user must
// approve its saved audio artifact before a separate video mux endpoint opens.
func (h Handler) StartWorkflowDubbingAudio(c *gin.Context) {
	var req dto.StartWorkflowDubbingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		workflowError(c, fmt.Errorf("cấu hình lồng tiếng không hợp lệ: %w", err))
		return
	}
	data, err := h.currentWorkflowService().StartWorkflowDubbingAudio(c.Param("taskId"), req)
	workflowResponse(c, data, err)
}

// SkipWorkflowDubbing selects the subtitle-only branch after translation has
// been reviewed. The service deletes only task-owned dubbing/render outputs
// and preserves the reviewed translated SRT for the later render action.
func (h Handler) SkipWorkflowDubbing(c *gin.Context) {
	data, err := h.currentWorkflowService().SkipWorkflowDubbing(c.Param("taskId"))
	workflowResponse(c, data, err)
}

func (h Handler) ApproveWorkflowDubbing(c *gin.Context) {
	h.ApproveWorkflowDubbingAudio(c)
}

func (h Handler) ApproveWorkflowDubbingAudio(c *gin.Context) {
	data, err := h.currentWorkflowService().ApproveWorkflowStage(c.Param("taskId"), "dubbing_audio")
	workflowResponse(c, data, err)
}

func (h Handler) StartWorkflowDubbingVideo(c *gin.Context) {
	data, err := h.currentWorkflowService().StartWorkflowDubbingVideo(c.Param("taskId"))
	workflowResponse(c, data, err)
}

func (h Handler) ApproveWorkflowDubbingVideo(c *gin.Context) {
	data, err := h.currentWorkflowService().ApproveWorkflowStage(c.Param("taskId"), "dubbing_video")
	workflowResponse(c, data, err)
}

func (h Handler) StartWorkflowRender(c *gin.Context) {
	data, err := h.currentWorkflowService().StartWorkflowRender(c.Param("taskId"))
	workflowResponse(c, data, err)
}

func (h Handler) currentWorkflowService() *service.Service {
	if configUpdated || h.Service == nil {
		// Unlike the legacy endpoint this intentionally does not run the full
		// dependency check. A source speech-to-text review is cloud-backed and
		// must not be blocked by a local ASR model.
		h.Service = service.NewService()
		configUpdated = false
	}
	return h.Service
}

func workflowResponse(c *gin.Context, data *dto.SubtitleWorkflowData, err error) {
	if err != nil {
		workflowError(c, err)
		return
	}
	response.R(c, response.Response{Error: 0, Msg: "Thành công / Success", Data: data})
}

func workflowError(c *gin.Context, err error) {
	response.R(c, response.Response{Error: -1, Msg: err.Error(), Data: nil})
}

func (h Handler) UploadFile(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Không đọc được file upload / Unable to read upload",
			Data:  nil,
		})
		return
	}

	files := form.File["file"]
	if len(files) == 0 {
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Chưa có file nào được tải lên / No files uploaded",
			Data:  nil,
		})
		return
	}

	var savedFiles []string
	for _, file := range files {
		savePath := "./uploads/" + file.Filename
		if err := c.SaveUploadedFile(file, savePath); err != nil {
			response.R(c, response.Response{
				Error: -1,
				Msg:   "Không thể lưu file / Failed to save file: " + file.Filename,
				Data:  nil,
			})
			return
		}
		savedFiles = append(savedFiles, "local:"+savePath)
	}

	response.R(c, response.Response{
		Error: 0,
		Msg:   "Tải file thành công / Upload successful",
		Data: gin.H{
			"file_path":  savedFiles[0],
			"file_paths": savedFiles,
		},
	})
}

func (h Handler) DownloadFile(c *gin.Context) {
	requestedFile := c.Param("filepath")
	if requestedFile == "" {
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Đường dẫn file trống / Empty file path",
			Data:  nil,
		})
		return
	}

	// A Gin wildcard begins with "/". Normalize it to a relative task path so
	// /api/v1/files/tasks/<job>/... works on both Windows and Unix.
	requestedFile = strings.TrimLeft(filepath.FromSlash(requestedFile), `/\\`)
	requestedFile = filepath.Clean(requestedFile)
	if requestedFile == "." || filepath.IsAbs(requestedFile) || requestedFile == ".." || strings.HasPrefix(requestedFile, ".."+string(os.PathSeparator)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Truy cập bị từ chối / Forbidden"})
		return
	}
	localFilePath := filepath.Join(".", requestedFile)

	// Restrict downloads to the tasks output directory to prevent
	// arbitrary file reads (e.g. config/config.toml with API keys).
	tasksDir, err := filepath.Abs("tasks")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi nội bộ / Internal error"})
		return
	}
	absPath, err := filepath.Abs(localFilePath)
	if err != nil || (absPath != tasksDir && !strings.HasPrefix(absPath, tasksDir+string(os.PathSeparator))) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Truy cập bị từ chối / Forbidden"})
		return
	}

	if _, err := os.Stat(localFilePath); os.IsNotExist(err) {
		response.R(c, response.Response{
			Error: -1,
			Msg:   "File không tồn tại / File not found",
			Data:  nil,
		})
		return
	}
	c.FileAttachment(localFilePath, filepath.Base(localFilePath))
}
