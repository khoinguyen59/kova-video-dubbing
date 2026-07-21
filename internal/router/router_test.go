package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kova/config"

	"github.com/gin-gonic/gin"
)

func TestKovaV1Status(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)

	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, expected := range []string{`"name":"Kova"`, `"api_version":"v1"`, `"fixed-voice-dubbing"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("status response missing %q: %s", expected, body)
		}
	}
}

func TestStagedWorkflowRoutesAreRegisteredAndRejectInvalidInputSafely(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)

	// This deliberately invalid source avoids any network/download work while
	// proving that the staged source route, rather than the one-click legacy
	// job endpoint, handles the request.
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/subtitle/stages/source", bytes.NewBufferString(`{"url":"not-a-youtube-url"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(recorder, req)
	if recorder.Code == http.StatusNotFound {
		t.Fatalf("staged source endpoint was not registered: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":-1`) {
		t.Fatalf("invalid staged source did not return a controlled error: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/subtitle/unknown_workflow/workflow", nil))
	if recorder.Code == http.StatusNotFound {
		t.Fatalf("workflow status endpoint was not registered: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":-1`) {
		t.Fatalf("unknown workflow did not return a controlled error: %s", recorder.Body.String())
	}
}

func TestStagedWorkflowDubbingSkipRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)

	// An unknown task is expected to be a controlled API error. The important
	// assertion here is that the native staged skip route is registered rather
	// than falling through to a 404 or the legacy one-click job handler.
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/subtitle/unknown_workflow/dubbing/skip", nil))
	if recorder.Code == http.StatusNotFound {
		t.Fatalf("staged dubbing skip endpoint was not registered: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":-1`) {
		t.Fatalf("unknown staged dubbing skip did not return a controlled error: %s", recorder.Body.String())
	}
}

func TestStagedWorkflowSeparateDubbingReviewRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)

	for _, path := range []string{
		"/api/v1/jobs/subtitle/unknown_workflow/dubbing/audio",
		"/api/v1/jobs/subtitle/unknown_workflow/dubbing/audio/approve",
		"/api/v1/jobs/subtitle/unknown_workflow/dubbing/video",
		"/api/v1/jobs/subtitle/unknown_workflow/dubbing/video/approve",
	} {
		recorder := httptest.NewRecorder()
		r.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`)))
		if recorder.Code == http.StatusNotFound {
			t.Fatalf("staged dubbing route was not registered: %s", path)
		}
		if !strings.Contains(recorder.Body.String(), `"error":-1`) {
			t.Fatalf("unknown staged dubbing route did not return controlled error for %s: %s", path, recorder.Body.String())
		}
	}
}

func TestKovaConfigResponseDoesNotExposeSecrets(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })
	config.Conf.Llm.ApiKey = "llm-secret-value"
	config.Conf.Transcribe.Openai.ApiKey = "asr-secret-value"
	config.Conf.Tts.Gateway.ApiKey = "tts-secret-value"
	config.Conf.Tts.Omnivoice.SessionApiKey = "colab-session-secret-value"

	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, secret := range []string{"llm-secret-value", "asr-secret-value", "tts-secret-value", "colab-session-secret-value"} {
		if strings.Contains(body, secret) {
			t.Fatalf("configuration response leaked secret %q", secret)
		}
	}
}

func TestKovaConfigResponseShowsImmutableRemoteClonePolicy(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })
	config.Conf.Tts.Omnivoice.BaseUrl = "https://kova-worker.trycloudflare.com"
	config.Conf.Tts.Omnivoice.ReferenceAudio = "C:/private/old-speaker.wav"
	config.Conf.Tts.Omnivoice.RequireReference = true
	config.Conf.Tts.Omnivoice.RemoteOnly = true
	config.Conf.Tts.Omnivoice.RequireCUDA = true

	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		`"requireReferenceAudio":true`,
		`"remoteOnly":true`,
		`"requireCuda":true`,
		`"referenceAudio":""`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("configuration response missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, "old-speaker.wav") {
		t.Fatalf("configuration response leaked a previous reference path: %s", body)
	}
}
