package desktop

import (
	"kova/config"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDecodeWorkflowSnapshotSupportsCreateAndStatusResponses(t *testing.T) {
	wrapped := `{"error":0,"msg":"ok","data":{"task_id":"job-source","workflow":{"current_stage":"awaiting_source_review","process_percent":100,"source_srt_url":"/api/v1/files/job-source/source.srt","can_start":{"translation":false}}}}`
	snapshot, err := decodeWorkflowSnapshot(strings.NewReader(wrapped))
	if err != nil {
		t.Fatalf("decode wrapped response: %v", err)
	}
	if snapshot.TaskID != "job-source" || snapshot.CurrentStage != "awaiting_source_review" {
		t.Fatalf("wrapped snapshot = %#v", snapshot)
	}
	if snapshot.CanStart["translation"] {
		t.Fatal("translation must remain closed while source review is pending")
	}

	direct := `{"error":0,"msg":"ok","data":{"task_id":"job-source","current_stage":"source_approved","process_percent":100,"can_start":{"translation":true}}}`
	snapshot, err = decodeWorkflowSnapshot(strings.NewReader(direct))
	if err != nil {
		t.Fatalf("decode direct response: %v", err)
	}
	if snapshot.TaskID != "job-source" || snapshot.CurrentStage != "source_approved" || !snapshot.CanStart["translation"] {
		t.Fatalf("direct snapshot = %#v", snapshot)
	}
}

func TestWorkflowReferenceAudioURLIsExplicitlyLocal(t *testing.T) {
	if got := workflowReferenceAudioURL(`C:\refs\speaker.wav`); got != `local:C:\refs\speaker.wav` {
		t.Fatalf("local reference URL = %q", got)
	}
	if got := workflowReferenceAudioURL("https://example.invalid/reference.wav"); got != "https://example.invalid/reference.wav" {
		t.Fatalf("remote reference URL = %q", got)
	}
	if got := workflowReferenceAudioURL(""); got != "" {
		t.Fatalf("empty reference URL = %q", got)
	}
}

func TestGatewayTTSModelDropdownIncludesVietnameseAndPreservesSavedModel(t *testing.T) {
	if got := gatewayTTSModelLabel("google-tts/vi"); got != "Google TTS tiếng Việt" {
		t.Fatalf("Vietnamese Google TTS label = %q", got)
	}
	if got := gatewayTTSModelLabel("google-tts/en"); got != "Google TTS tiếng Anh" {
		t.Fatalf("English Google TTS label = %q", got)
	}
	if got := gatewayTTSModelLabel("customer-tts-v2"); got != "Model gateway đã lưu: customer-tts-v2" {
		t.Fatalf("saved custom gateway model was not preserved: %q", got)
	}
	if got := gatewayTTSModelLabel(""); got != "Google TTS tiếng Việt" {
		t.Fatalf("empty gateway model should select the Vietnamese Kova default: %q", got)
	}
}

func TestWorkflowDubbingPayloadDoesNotLeakCloneReferenceToGateway(t *testing.T) {
	originalConfig := config.Conf
	defer func() { config.Conf = originalConfig }()

	sm := NewSubtitleManager(nil)
	sm.ttsVoiceCode = "preset"
	sm.voiceoverAudioPath = `C:\refs\speaker.wav`
	sm.voiceCloneConsent = true

	config.Conf.Tts.Provider = "gateway"
	payload := sm.workflowDubbingPayload()
	if payload["tts_voice_clone_src_file_url"] != "" || payload["voice_clone_consent"] != false {
		t.Fatalf("gateway payload retained clone data: %#v", payload)
	}

	config.Conf.Tts.Provider = "omnivoice"
	payload = sm.workflowDubbingPayload()
	if payload["tts_voice_clone_src_file_url"] != `local:C:\refs\speaker.wav` || payload["voice_clone_consent"] != true {
		t.Fatalf("OmniVoice payload lost explicit clone data: %#v", payload)
	}
}

func TestSkipWorkflowDubbingPersistsTheUserDecision(t *testing.T) {
	originalConfig := config.Conf
	defer func() { config.Conf = originalConfig }()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/jobs/subtitle/job-42/dubbing/skip" {
			t.Errorf("path = %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"error":0,"msg":"ok","data":{"task_id":"job-42","current_stage":"translation_approved","can_start":{"render":true}}}`))
	}))
	defer server.Close()
	configureWorkflowTestServer(t, server.URL)

	snapshot, err := NewSubtitleManager(nil).SkipWorkflowDubbing("job-42")
	if err != nil {
		t.Fatalf("skip dubbing: %v", err)
	}
	if snapshot.CurrentStage != "translation_approved" || !snapshot.CanStart["render"] {
		t.Fatalf("unexpected skip snapshot: %#v", snapshot)
	}
}

func TestWorkflowClientUsesSeparateAudioAndVideoDubbingRoutes(t *testing.T) {
	originalConfig := config.Conf
	t.Cleanup(func() { config.Conf = originalConfig })

	paths := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"error":0,"msg":"ok","data":{"task_id":"job-42","current_stage":"ok","can_start":{}}}`))
	}))
	defer server.Close()
	configureWorkflowTestServer(t, server.URL)

	sm := NewSubtitleManager(nil)
	for _, action := range []struct {
		start bool
		stage string
	}{
		{start: true, stage: "dubbing_audio"},
		{stage: "dubbing_audio"},
		{start: true, stage: "dubbing_video"},
		{stage: "dubbing_video"},
	} {
		var err error
		if action.start {
			_, err = sm.StartWorkflowStage("job-42", action.stage)
		} else {
			_, err = sm.ApproveWorkflowStage("job-42", action.stage)
		}
		if err != nil {
			t.Fatalf("%s request failed: %v", action.stage, err)
		}
	}

	want := []string{
		"/api/v1/jobs/subtitle/job-42/dubbing/audio",
		"/api/v1/jobs/subtitle/job-42/dubbing/audio/approve",
		"/api/v1/jobs/subtitle/job-42/dubbing/video",
		"/api/v1/jobs/subtitle/job-42/dubbing/video/approve",
	}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("workflow dubbing paths = %#v, want %#v", paths, want)
	}
}

func TestWorkflowRequestHasFiniteClientTimeout(t *testing.T) {
	originalConfig := config.Conf
	t.Cleanup(func() { config.Conf = originalConfig })

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		// A backend can accept the request yet fail to answer. Respecting the
		// request context lets the test server finish as soon as the client
		// deadline expires.
		<-request.Context().Done()
	}))
	defer server.Close()
	configureWorkflowTestServer(t, server.URL)

	sm := NewSubtitleManager(nil)
	sm.workflowHTTPClient = &http.Client{Timeout: 30 * time.Millisecond}
	started := time.Now()
	_, err := sm.GetWorkflowSnapshot("job-timeout")
	if err == nil {
		t.Fatal("workflow request unexpectedly succeeded against a stalled server")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("workflow timeout took %s; it must release the UI promptly", elapsed)
	}
}

func configureWorkflowTestServer(t *testing.T, rawURL string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	config.Conf.Server.Host = host
	config.Conf.Server.Port = port
}

func TestWorkflowStageGates(t *testing.T) {
	if !workflowStageReached("source_approved", "source") {
		t.Fatal("source approval must unlock translation")
	}
	if workflowStageReached("awaiting_source_review", "source") {
		t.Fatal("source review must not unlock translation")
	}
	if !workflowStageReached("translation_approved", "translation") {
		t.Fatal("translation approval must unlock optional voice/render")
	}
	if workflowStageReached("awaiting_dubbing_audio_review", "dubbing") || workflowStageReached("awaiting_dubbing_video_review", "dubbing") {
		t.Fatal("neither audio nor video review may unlock render before the final approval")
	}
}

func TestIsLoopbackLLMHost(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "127.0.0.1", "::1", "worker.local"} {
		if !isLoopbackLLMHost(host) {
			t.Fatalf("%q must be treated as a local host", host)
		}
	}
	if isLoopbackLLMHost("gateway.example") {
		t.Fatal("remote gateway hosts must not be considered loopback")
	}
}

func TestValidateTranslationStageConfigAcceptsInMemoryGatewaySessionKey(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })
	t.Setenv("KOVA_API_GATEWAY_API_KEY", "")

	config.Conf.Llm = config.LlmConfig{
		Provider:      "openai-compatible",
		BaseUrl:       config.KOVAGatewayBaseURL,
		Model:         "oc/deepseek-v4-flash-free",
		ApiKeyEnv:     "KOVA_API_GATEWAY_API_KEY",
		SessionApiKey: "in-memory-session-key",
	}
	if err := validateTranslationStageConfig(); err != nil {
		t.Fatalf("validateTranslationStageConfig() error = %v", err)
	}
}
