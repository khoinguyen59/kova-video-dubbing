package main

import (
	"context"
	"errors"
	"io"
	"net"
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

func TestCreateVoiceProfileUploadsConsentedReferenceWithoutLeakingPath(t *testing.T) {
	referencePath := filepath.Join(t.TempDir(), "narrator.mp3")
	if err := os.WriteFile(referencePath, []byte("voice-reference-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/profiles" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer private-worker-token" {
			t.Fatalf("authorization = %q", got)
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if request.FormValue("name") != "Narrator" || request.FormValue("consent_confirmed") != "true" || request.FormValue("language") != "vi" {
			t.Fatalf("profile fields name=%q consent=%q language=%q", request.FormValue("name"), request.FormValue("consent_confirmed"), request.FormValue("language"))
		}
		file, header, err := request.FormFile("ref_audio")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		if header.Filename != "narrator.mp3" || string(body) != "voice-reference-bytes" {
			t.Fatalf("audio filename=%q body=%q", header.Filename, body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"voice-123","profile":{"id":"voice-123","name":"Narrator","language":"vi","status":"ready"}}`))
	}))
	defer server.Close()

	app := NewApp()
	app.httpClient = server.Client()
	app.projectDataRoot = t.TempDir()
	created, err := app.CreateVoiceProfile(VoiceProfileCreateRequest{
		BaseURL:            server.URL,
		Token:              "private-worker-token",
		Name:               "Narrator",
		ReferenceAudioPath: referencePath,
		Language:           "vi",
		ConsentConfirmed:   true,
	})
	if err != nil {
		t.Fatalf("CreateVoiceProfile() error = %v", err)
	}
	if created.ID != "voice-123" || created.Name != "Narrator" || created.Status != "ready" || !created.Saved || !created.BackupAvailable {
		t.Fatalf("created profile = %#v", created)
	}
	manifest, err := os.ReadFile(filepath.Join(app.projectDataRoot, "voice-library", "profiles.json"))
	if err != nil {
		t.Fatalf("read saved voice library: %v", err)
	}
	if strings.Contains(string(manifest), "private-worker-token") || !strings.Contains(string(manifest), "voice-123") {
		t.Fatalf("voice library metadata is incorrect or leaked the token: %s", manifest)
	}
	backup, err := os.ReadFile(filepath.Join(app.projectDataRoot, "voice-library", "references", "voice-123.mp3"))
	if err != nil || string(backup) != "voice-reference-bytes" {
		t.Fatalf("voice backup = %q, error = %v", backup, err)
	}
}

func TestVoiceLibraryKeepsBackedUpProfileAfterAppRestart(t *testing.T) {
	root := t.TempDir()
	referencePath := filepath.Join(root, "reference.flac")
	if err := os.WriteFile(referencePath, []byte("consented-reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := NewApp()
	first.projectDataRoot = root
	if _, err := first.saveVoiceProfileBackup(VoiceProfile{ID: "stable-voice", Name: "Narrator", Language: "vi", Status: "ready"}, "https://voice.example.test", referencePath); err != nil {
		t.Fatalf("saveVoiceProfileBackup() error = %v", err)
	}

	second := NewApp()
	second.projectDataRoot = root
	profiles, err := second.ListVoiceProfiles(VoiceHealthRequest{})
	if err != nil {
		t.Fatalf("ListVoiceProfiles(offline) error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("saved profiles = %#v, want one profile", profiles)
	}
	profile := profiles[0]
	if profile.ID != "stable-voice" || profile.Name != "Narrator" || !profile.Saved || !profile.BackupAvailable || profile.WorkerURL != "https://voice.example.test" {
		t.Fatalf("restored profile = %#v", profile)
	}
}

func TestPreviewVoiceProfileRestoresSavedProfileAndReturnsAudio(t *testing.T) {
	root := t.TempDir()
	referencePath := filepath.Join(root, "reference.wav")
	if err := os.WriteFile(referencePath, []byte("consented-reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer current-colab-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch request.URL.Path {
		case "/v1/voices":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"id":"stable-voice","name":"Narrator","language":"vi","status":"ready"}]`))
		case "/generate":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got := request.FormValue("profile_id"); got != "stable-voice" {
				t.Fatalf("profile_id = %q", got)
			}
			if got := request.FormValue("text"); got != voicePreviewText {
				t.Fatalf("preview text = %q", got)
			}
			writer.Header().Set("Content-Type", "audio/wav")
			_, _ = writer.Write([]byte("RIFFpreview"))
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	app := NewApp()
	app.projectDataRoot = root
	app.httpClient = server.Client()
	if _, err := app.saveVoiceProfileBackup(VoiceProfile{ID: "stable-voice", Name: "Narrator", Language: "vi", Status: "ready"}, server.URL, referencePath); err != nil {
		t.Fatalf("saveVoiceProfileBackup() error = %v", err)
	}
	preview, err := app.PreviewVoiceProfile(VoicePreviewRequest{BaseURL: server.URL, Token: "current-colab-token", ProfileID: "stable-voice", Language: "vi"})
	if err != nil {
		t.Fatalf("PreviewVoiceProfile() error = %v", err)
	}
	if preview.ProfileID != "stable-voice" || !strings.HasPrefix(preview.DataURL, "data:audio/wav;base64,") {
		t.Fatalf("preview = %#v", preview)
	}
}

func TestDeleteVoiceProfileRemovesWorkerAndPrivateBackup(t *testing.T) {
	root := t.TempDir()
	referencePath := filepath.Join(root, "reference.flac")
	if err := os.WriteFile(referencePath, []byte("consented-reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	deletedRemotely := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/v1/profiles/stable-voice" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer current-colab-token" {
			t.Fatalf("authorization = %q", got)
		}
		deletedRemotely = true
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	app := NewApp()
	app.projectDataRoot = root
	app.httpClient = server.Client()
	if _, err := app.saveVoiceProfileBackup(VoiceProfile{ID: "stable-voice", Name: "Narrator", Language: "vi", Status: "ready"}, server.URL, referencePath); err != nil {
		t.Fatalf("saveVoiceProfileBackup() error = %v", err)
	}
	if err := app.DeleteVoiceProfile(VoiceProfileDeleteRequest{BaseURL: server.URL, Token: "current-colab-token", ProfileID: "stable-voice"}); err != nil {
		t.Fatalf("DeleteVoiceProfile() error = %v", err)
	}
	if !deletedRemotely {
		t.Fatal("expected remote profile deletion")
	}
	profiles, err := app.ListVoiceProfiles(VoiceHealthRequest{})
	if err != nil || len(profiles) != 0 {
		t.Fatalf("profiles after delete = %#v, error = %v", profiles, err)
	}
	if _, err := os.Stat(filepath.Join(root, "voice-library", "references", "stable-voice.flac")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saved reference still exists, error = %v", err)
	}
}

func TestVoiceLibraryRestoresProfileWhenColabHasReset(t *testing.T) {
	root := t.TempDir()
	referencePath := filepath.Join(root, "reference.wav")
	if err := os.WriteFile(referencePath, []byte("consented-reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	var uploaded bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer current-colab-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch request.URL.Path {
		case "/v1/voices":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[]`))
		case "/profiles":
			uploaded = true
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			file, _, err := request.FormFile("ref_audio")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			body, _ := io.ReadAll(file)
			if string(body) != "consented-reference" {
				t.Fatalf("restored audio = %q", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"remote-new","profile":{"id":"remote-new","name":"Narrator","language":"vi","status":"ready"}}`))
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	app := NewApp()
	app.projectDataRoot = root
	app.httpClient = server.Client()
	if _, err := app.saveVoiceProfileBackup(VoiceProfile{ID: "stable-voice", Name: "Narrator", Language: "vi", Status: "ready"}, server.URL, referencePath); err != nil {
		t.Fatalf("saveVoiceProfileBackup() error = %v", err)
	}
	remoteID, err := app.ensureVoiceProfileOnWorker("stable-voice", server.URL, "current-colab-token")
	if err != nil {
		t.Fatalf("ensureVoiceProfileOnWorker() error = %v", err)
	}
	if remoteID != "remote-new" || !uploaded {
		t.Fatalf("restored remote id = %q, uploaded = %v", remoteID, uploaded)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "voice-library", "profiles.json"))
	if err != nil || !strings.Contains(string(manifest), `"remote_profile_id": "remote-new"`) || strings.Contains(string(manifest), "current-colab-token") {
		t.Fatalf("restored manifest=%s error=%v", manifest, err)
	}
}

func TestListVoiceProfilesImportsConsentedReferenceFromOlderWorker(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer current-colab-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch request.URL.Path {
		case "/v1/voices":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"id":"legacy-voice","name":"Legacy narrator","language":"vi","status":"ready"}]`))
		case "/v1/profiles/legacy-voice/reference":
			writer.Header().Set("Content-Disposition", `attachment; filename="legacy.flac"`)
			_, _ = writer.Write([]byte("legacy-consented-reference"))
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	app := NewApp()
	app.projectDataRoot = t.TempDir()
	app.httpClient = server.Client()
	profiles, err := app.ListVoiceProfiles(VoiceHealthRequest{BaseURL: server.URL, Token: "current-colab-token"})
	if err != nil {
		t.Fatalf("ListVoiceProfiles() error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != "legacy-voice" || !profiles[0].Saved || !profiles[0].BackupAvailable {
		t.Fatalf("profiles = %#v", profiles)
	}
	backup, err := os.ReadFile(filepath.Join(app.projectDataRoot, "voice-library", "references", "legacy-voice.flac"))
	if err != nil || string(backup) != "legacy-consented-reference" {
		t.Fatalf("backup = %q, error = %v", backup, err)
	}
}

func TestCheckSTTHealthAcceptsOnlyCUDAReadyRemoteWorker(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer stt-session-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = writer.Write([]byte(`{"ready":true,"device":"cuda"}`))
	}))
	defer server.Close()

	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	app := NewApp()
	app.httpClient = &http.Client{Transport: transport}
	remoteURL := strings.Replace(server.URL, "127.0.0.1", "example.com", 1)
	result := app.CheckSTTHealth(VoiceHealthRequest{BaseURL: remoteURL, Token: "stt-session-token"})
	if !result.Reachable || result.Status != http.StatusOK {
		t.Fatalf("CheckSTTHealth() = %+v", result)
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

func TestDeleteDesktopProjectClearsTimelineAndDraftDirectory(t *testing.T) {
	root := t.TempDir()
	store, err := project.Open(filepath.Join(root, "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := NewApp()
	app.projectStore = store
	app.projectDataRoot = root
	created, err := store.CreateProject(context.Background(), "Delete desktop project", "vi")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartStage(context.Background(), created.ID, project.StageSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveDesktopDraft(created.ID, run.ID, "source", "https://youtu.be/example"); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteDesktopProject(created.ID); err != nil {
		t.Fatalf("DeleteDesktopProject() error = %v", err)
	}
	if _, err := store.Snapshot(context.Background(), created.ID); !errors.Is(err, project.ErrProjectNotFound) {
		t.Fatalf("deleted project snapshot error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", created.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted project draft directory exists: %v", err)
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

func TestConfigureDesktopTTSReplacesOmniVoiceWithGoogleGatewayAndReusesSessionKey(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })

	config.Conf.Tts.Provider = "omnivoice"
	config.Conf.Tts.Gateway.Endpoint = "https://gateway.example/v1/audio/speech"
	config.Conf.Tts.Gateway.ApiKey = ""
	config.Conf.Tts.Gateway.ApiKeyEnv = ""
	config.Conf.Tts.Gateway.SessionAPIKey = ""
	config.Conf.Llm.SessionApiKey = "gateway-session-key"

	payload, err := NewApp().configureDesktopTTS(DesktopWorkflowStartRequest{TTSOptionID: "gateway-google-vi"})
	if err != nil {
		t.Fatalf("configureDesktopTTS() error = %v", err)
	}
	if config.Conf.Tts.Provider != "gateway" || config.Conf.Tts.Gateway.Model != "google-tts/vi" {
		t.Fatalf("gateway selection was not applied: %+v", config.Conf.Tts)
	}
	if config.Conf.Tts.Gateway.SessionAPIKey != "gateway-session-key" {
		t.Fatal("existing KOVA Gateway session key was not made available to TTS")
	}
	if string(payload) != `{"tts_voice_code":"auto"}` {
		t.Fatalf("gateway payload = %s, want clone-free auto voice", payload)
	}
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
