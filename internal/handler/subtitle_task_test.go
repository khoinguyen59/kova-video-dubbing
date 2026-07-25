package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDownloadFileServesWorkflowVideoInlineForPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "tasks", "job"), 0o755); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(workdir, "tasks", "job", "origin_video.mp4")
	if err := os.WriteFile(videoPath, []byte("ftypisom-preview-fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	router := gin.New()
	router.GET("/api/v1/files/*filepath", Handler{}.DownloadFile)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/files/tasks/job/origin_video.mp4", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("DownloadFile status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if disposition := recorder.Header().Get("Content-Disposition"); strings.Contains(strings.ToLower(disposition), "attachment") {
		t.Fatalf("DownloadFile forced a browser download instead of inline preview: %q", disposition)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "video/mp4") {
		t.Fatalf("Content-Type = %q, want video/mp4", recorder.Header().Get("Content-Type"))
	}
}
