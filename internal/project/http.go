package project

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// HTTPHandler exposes the v2 persistence contract without coupling it to the
// legacy v1 subtitle handler. The store opens lazily so constructing routers
// for unit tests or CLI help never writes a user database.
type HTTPHandler struct {
	databasePath string
	mu           sync.Mutex
	store        *Store
	err          error
}

func NewHTTPHandler(databasePath string) *HTTPHandler {
	return &HTTPHandler{databasePath: databasePath}
}

func DefaultDatabasePath() string {
	if root := strings.TrimSpace(os.Getenv("KOVA_DATA_DIR")); root != "" {
		return filepath.Join(root, "kova.db")
	}
	if root, err := os.UserConfigDir(); err == nil && root != "" {
		return filepath.Join(root, "KOVA", "kova.db")
	}
	return filepath.Join("data", "kova.db")
}

func RegisterRoutes(group *gin.RouterGroup, handler *HTTPHandler) {
	group.GET("/status", handler.status)
	group.POST("/projects", handler.createProject)
	group.GET("/projects", handler.listProjects)
	group.GET("/projects/:projectId", handler.projectSnapshot)
	group.POST("/projects/:projectId/stages/:stage/start", handler.startStage)
	group.POST("/stage-runs/:runId/review", handler.reviewStage)
	group.POST("/stage-runs/:runId/approve", handler.approveStage)
	group.POST("/stage-runs/:runId/fail", handler.failStage)
	group.POST("/projects/:projectId/artifacts", handler.createArtifact)
}

func (h *HTTPHandler) database() (*Store, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.store != nil || h.err != nil {
		return h.store, h.err
	}
	h.store, h.err = Open(h.databasePath)
	return h.store, h.err
}

// Close is primarily used by deterministic tests and controlled application
// shutdown. It also lets the owning service release SQLite before a portable
// data directory is moved or deleted.
func (h *HTTPHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.store == nil {
		return nil
	}
	err := h.store.Close()
	h.store = nil
	return err
}

func (h *HTTPHandler) status(c *gin.Context) {
	reply(c, http.StatusOK, gin.H{
		"name":        "KOVA",
		"api_version": "v2",
		"capabilities": []string{
			"projects", "stage-review", "immutable-artifacts", "voice-connector",
		},
	})
}

func (h *HTTPHandler) createProject(c *gin.Context) {
	var request struct {
		Name           string `json:"name"`
		TargetLanguage string `json:"target_language"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", err)
		return
	}
	store, err := h.database()
	if err == nil {
		project, createErr := store.CreateProject(c, request.Name, request.TargetLanguage)
		if createErr == nil {
			reply(c, http.StatusCreated, project)
			return
		}
		err = createErr
	}
	fail(c, http.StatusBadRequest, "project_create_failed", err)
}

func (h *HTTPHandler) listProjects(c *gin.Context) {
	store, err := h.database()
	if err == nil {
		var projects []Project
		projects, err = store.ListProjects(c)
		if err == nil {
			reply(c, http.StatusOK, projects)
			return
		}
	}
	fail(c, http.StatusInternalServerError, "project_list_failed", err)
}

func (h *HTTPHandler) projectSnapshot(c *gin.Context) {
	store, err := h.database()
	if err == nil {
		var snapshot Snapshot
		snapshot, err = store.Snapshot(c, c.Param("projectId"))
		if err == nil {
			reply(c, http.StatusOK, snapshot)
			return
		}
	}
	failStore(c, err)
}

func (h *HTTPHandler) startStage(c *gin.Context) {
	store, err := h.database()
	if err == nil {
		var run StageRun
		run, err = store.StartStage(c, c.Param("projectId"), Stage(c.Param("stage")))
		if err == nil {
			reply(c, http.StatusAccepted, run)
			return
		}
	}
	failStore(c, err)
}

func (h *HTTPHandler) reviewStage(c *gin.Context) {
	var request struct {
		MessageKey string `json:"message_key"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", err)
		return
	}
	if strings.TrimSpace(request.MessageKey) == "" {
		request.MessageKey = "stage.review_required"
	}
	store, err := h.database()
	if err == nil {
		var run StageRun
		run, err = store.MarkReviewRequired(c, c.Param("runId"), request.MessageKey)
		if err == nil {
			reply(c, http.StatusOK, run)
			return
		}
	}
	failStore(c, err)
}

func (h *HTTPHandler) approveStage(c *gin.Context) {
	store, err := h.database()
	if err == nil {
		var run StageRun
		run, err = store.ApproveStage(c, c.Param("runId"))
		if err == nil {
			reply(c, http.StatusOK, run)
			return
		}
	}
	failStore(c, err)
}

func (h *HTTPHandler) failStage(c *gin.Context) {
	var request struct {
		FailureCode string `json:"failure_code"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", err)
		return
	}
	store, err := h.database()
	if err == nil {
		var run StageRun
		run, err = store.FailStage(c, c.Param("runId"), request.FailureCode)
		if err == nil {
			reply(c, http.StatusOK, run)
			return
		}
	}
	failStore(c, err)
}

func (h *HTTPHandler) createArtifact(c *gin.Context) {
	var artifact Artifact
	if err := c.ShouldBindJSON(&artifact); err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", err)
		return
	}
	artifact.ProjectID = c.Param("projectId")
	store, err := h.database()
	if err == nil {
		artifact, err = store.CreateArtifact(c, artifact)
		if err == nil {
			reply(c, http.StatusCreated, artifact)
			return
		}
	}
	failStore(c, err)
}

func reply(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"error": 0, "msg": "ok", "data": data})
}

func failStore(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrProjectNotFound):
		fail(c, http.StatusNotFound, "project_not_found", err)
	case errors.Is(err, ErrInvalidStage):
		fail(c, http.StatusBadRequest, "invalid_stage", err)
	case errors.Is(err, ErrPrerequisite):
		fail(c, http.StatusConflict, "stage_prerequisite_required", err)
	case errors.Is(err, ErrInvalidTransition):
		fail(c, http.StatusConflict, "invalid_stage_transition", err)
	default:
		fail(c, http.StatusInternalServerError, "project_store_failed", err)
	}
}

func fail(c *gin.Context, status int, code string, err error) {
	message := "request failed"
	if err != nil {
		message = err.Error()
	}
	c.JSON(status, gin.H{"error": -1, "code": code, "msg": message})
}
