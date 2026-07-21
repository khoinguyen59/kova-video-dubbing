package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kova/config"
	"kova/internal/project"
)

func TestStageEndpointOnlyAllowsExplicitWorkflowStages(t *testing.T) {
	tests := []struct {
		stage     string
		endpoint  string
		needsTask bool
		wantErr   bool
	}{
		{"source", "/api/v1/jobs/subtitle/stages/source", false, false},
		{"translation", "/api/v1/jobs/subtitle/:task_id/translation", true, false},
		{"dubbing_audio", "/api/v1/jobs/subtitle/:task_id/dubbing/audio", true, false},
		{"render", "/api/v1/jobs/subtitle/:task_id/render", true, false},
		{"http://example.invalid", "", false, true},
	}
	for _, test := range tests {
		t.Run(test.stage, func(t *testing.T) {
			endpoint, method, needsTask, err := stageEndpoint(test.stage)
			if (err != nil) != test.wantErr {
				t.Fatalf("stageEndpoint(%q) error = %v", test.stage, err)
			}
			if test.wantErr {
				return
			}
			if endpoint != test.endpoint || method != http.MethodPost || needsTask != test.needsTask {
				t.Fatalf("stageEndpoint(%q) = (%q, %q, %t), want (%q, POST, %t)", test.stage, endpoint, method, needsTask, test.endpoint, test.needsTask)
			}
		})
	}
}

func TestNormalizeVoiceURLRejectsInsecureRemoteURLs(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"https://worker.example.test/", "https://worker.example.test", false},
		{"http://127.0.0.1:3900/", "http://127.0.0.1:3900", false},
		{"http://worker.example.test", "", true},
		{"not a URL", "", true},
	}
	for _, test := range tests {
		got, err := normalizeVoiceURL(test.raw)
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("normalizeVoiceURL(%q) = (%q, %v), want (%q, error=%t)", test.raw, got, err, test.want, test.wantErr)
		}
	}
}

func TestCheckVoiceHealthUsesBearerTokenWithoutEchoingIt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/health" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer private-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = writer.Write([]byte(`{"device":"cuda"}`))
	}))
	defer server.Close()

	app := NewApp()
	app.httpClient = server.Client()
	result := app.CheckVoiceHealth(VoiceHealthRequest{BaseURL: server.URL, Token: "private-token"})
	if !result.Reachable || result.Status != http.StatusOK {
		t.Fatalf("CheckVoiceHealth() = %+v", result)
	}
	if result.Message == "private-token" {
		t.Fatal("secret token leaked into result")
	}
}

func TestSaveDesktopDraftCreatesImmutableReviewArtifact(t *testing.T) {
	root := t.TempDir()
	store, err := project.Open(filepath.Join(root, "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := NewApp()
	app.projectStore = store
	app.projectDataRoot = root

	created, err := store.CreateProject(context.Background(), "KOVA test", "vi")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartStage(context.Background(), created.ID, project.StageSource)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := app.SaveDesktopDraft(created.ID, run.ID, "source", "https://youtu.be/example")
	if err != nil {
		t.Fatalf("SaveDesktopDraft() error = %v", err)
	}
	if artifact.Kind != "source_review_draft" || artifact.Path == "" || len(artifact.Checksum) != 64 {
		t.Fatalf("artifact = %+v", artifact)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.Path)))
	if err != nil || string(content) != "https://youtu.be/example\n" {
		t.Fatalf("draft = %q, err = %v", content, err)
	}
}

func TestTTSOptionsIncludeDropdownGooglePreset(t *testing.T) {
	options := NewApp().ListTTSOptions()
	for _, option := range options {
		if option.ID == "gateway-google-vi" && option.Model == "google-tts/vi" {
			return
		}
	}
	t.Fatalf("ListTTSOptions() = %+v, want Google Gateway preset", options)
}

func TestTranslationModelDropdownContainsOnlyApprovedFreeGatewayModels(t *testing.T) {
	options := NewApp().ListTranslationModels()
	if len(options) != 6 {
		t.Fatalf("ListTranslationModels() count = %d, want 6", len(options))
	}
	for _, option := range options {
		if !config.IsGatewayFreeLLMModel(option.ID) {
			t.Fatalf("ListTranslationModels() returned non-free model: %+v", option)
		}
	}
}

func TestSTTDropdownDefaultsToAndConfiguresColabFasterWhisper(t *testing.T) {
	original := config.Conf.Transcribe
	t.Cleanup(func() { config.Conf.Transcribe = original })

	options := NewApp().ListSTTOptions()
	if len(options) < 4 || options[0].ID != "colab-fasterwhisper-medium" || !options[0].NeedsWorker {
		t.Fatalf("ListSTTOptions() = %+v, want Colab Faster-Whisper medium", options)
	}
	if err := configureDesktopSTT("", "https://worker.trycloudflare.com", "session-token"); err != nil {
		t.Fatalf("configureDesktopSTT(default): %v", err)
	}
	if config.Conf.Transcribe.Provider != "openai" || config.Conf.Transcribe.Openai.BaseUrl != "https://worker.trycloudflare.com/v1" || config.Conf.Transcribe.Openai.SessionAPIKey != "session-token" {
		t.Fatalf("default STT config = %+v", config.Conf.Transcribe)
	}
	if err := configureDesktopSTT("fasterwhisper-medium", "", ""); err != nil {
		t.Fatalf("configureDesktopSTT(local): %v", err)
	}
	if config.Conf.Transcribe.Provider != "fasterwhisper" || config.Conf.Transcribe.Fasterwhisper.Model != "medium" || config.Conf.Transcribe.Openai.SessionAPIKey != "" {
		t.Fatalf("local STT config = %+v", config.Conf.Transcribe)
	}
	if err := configureDesktopSTT("gateway", "", ""); err == nil {
		t.Fatal("configureDesktopSTT accepted an invalid option")
	}
}

func TestReviewStageForLegacyStatusCoversTheFiveStepWorkflow(t *testing.T) {
	tests := []struct {
		status string
		stage  project.Stage
		ok     bool
	}{
		{"awaiting_source_review", project.StageSource, true},
		{"awaiting_translation_review", project.StageTranslation, true},
		{"awaiting_dubbing_audio_review", project.StageDubbingAudio, true},
		{"awaiting_dubbing_video_review", project.StageRender, true},
		{"completed", project.StageOutputs, true},
		{"running", "", false},
	}
	for _, test := range tests {
		stage, ok := reviewStageForLegacyStatus(test.status)
		if stage != test.stage || ok != test.ok {
			t.Fatalf("reviewStageForLegacyStatus(%q) = (%q, %t), want (%q, %t)", test.status, stage, ok, test.stage, test.ok)
		}
	}
}

func TestWorkflowFailureDetailRedactsCredentials(t *testing.T) {
	detail := workflowFailureDetail(errors.New("download failed: token=private-value; retry later"))
	if strings.Contains(detail, "private-value") {
		t.Fatalf("credential leaked in failure detail: %q", detail)
	}
	if !strings.Contains(detail, "[redacted credential]") {
		t.Fatalf("failure detail was not redacted: %q", detail)
	}
}
