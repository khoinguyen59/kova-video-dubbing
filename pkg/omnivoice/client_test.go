package omnivoice

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestText2SpeechWritesWorkerAudioAndSendsVietnameseSettings(t *testing.T) {
	var got synthesisRequest
	uploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotToken := r.Header.Get("Authorization"); gotToken != "Bearer test-session-worker-token" {
			t.Fatalf("Authorization = %q, want session bearer token", gotToken)
		}
		switch r.URL.Path {
		case "/reference":
			uploads++
			if gotName := r.Header.Get("X-OmniVoice-Reference-Name"); gotName != "clone.wav" {
				t.Fatalf("reference name = %q", gotName)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"reference":"reference:clone.wav"}`))
			return
		case "/synthesize":
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte(strings.Repeat("a", minimumWAVBytes)))
	}))
	defer server.Close()

	reference := filepath.Join(t.TempDir(), "clone.wav")
	if err := os.WriteFile(reference, []byte("reference"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{
		BaseURL:          server.URL + "/",
		APIKey:           "test-session-worker-token",
		Language:         "vi",
		ReferenceAudio:   "default.wav",
		ReferenceText:    "Giọng tham chiếu.",
		Instruct:         "giọng tự nhiên",
		Speed:            1.05,
		NumStep:          24,
		TimeoutSeconds:   10,
		ConsentConfirmed: true,
	})
	out := filepath.Join(t.TempDir(), "speech.wav")
	if err := client.Text2Speech("Xin chào", "local:"+reference, out); err != nil {
		t.Fatalf("Text2Speech() error = %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != strings.Repeat("a", minimumWAVBytes) {
		t.Fatalf("audio = %q", data)
	}
	if uploads != 1 || got.Text != "Xin chào" || got.Language != "vi" || got.RefAudio != "reference:clone.wav" {
		t.Fatalf("request = %#v", got)
	}
	if got.RefText != "Giọng tham chiếu." || got.Instruct != "giọng tự nhiên" || got.Speed != 1.05 || got.NumSteps != 24 {
		t.Fatalf("request settings = %#v", got)
	}
}

func TestText2SpeechUsesConfiguredReferenceWhenVoiceIsEmpty(t *testing.T) {
	var got synthesisRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reference" {
			_, _ = w.Write([]byte(`{"reference":"reference:default.wav"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(strings.Repeat("b", minimumWAVBytes)))
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "speech.wav")
	reference := filepath.Join(t.TempDir(), "default.wav")
	if err := os.WriteFile(reference, []byte("reference"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{BaseURL: server.URL, ReferenceAudio: "local:" + reference, ConsentConfirmed: true})
	if err := client.Text2Speech("hello", "", out); err != nil {
		t.Fatalf("Text2Speech() error = %v", err)
	}
	if got.RefAudio != "reference:default.wav" || got.Language != "vi" || got.NumSteps != 32 || got.Speed != 1 {
		t.Fatalf("request defaults = %#v", got)
	}
}

func TestText2SpeechWithDurationSendsNativeSlotLength(t *testing.T) {
	var got synthesisRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(strings.Repeat("c", minimumWAVBytes)))
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "speech.wav")
	client := NewClient(Config{BaseURL: server.URL})
	if err := client.Text2SpeechWithDuration("Xin chào", "", out, 2.4); err != nil {
		t.Fatalf("Text2SpeechWithDuration() error = %v", err)
	}
	if got.Duration == nil || *got.Duration != 2.4 {
		t.Fatalf("duration = %#v, want 2.4", got.Duration)
	}
}

func TestText2SpeechCloneModeRequiresReference(t *testing.T) {
	client := NewClient(Config{RequireReference: true})
	err := client.Text2Speech("Xin chào", "", filepath.Join(t.TempDir(), "out.wav"))
	if err == nil || !strings.Contains(err.Error(), "requires a local reference_audio") {
		t.Fatalf("Text2Speech() error = %v, want reference-audio error", err)
	}
}

func TestText2SpeechWithDurationFallsBackWhenNativeGenerationIsEmpty(t *testing.T) {
	var requests []synthesisRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request synthesisRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		requests = append(requests, request)
		if request.Duration != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"synthesis_failed","message":"empty samples"}}`))
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("d", minimumWAVBytes)))
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "speech.wav")
	if err := NewClient(Config{BaseURL: server.URL}).Text2SpeechWithDuration("Bức tượng.", "", out, 0.96); err != nil {
		t.Fatalf("Text2SpeechWithDuration() error = %v", err)
	}
	if len(requests) != 2 || requests[0].Duration == nil || requests[1].Duration != nil {
		t.Fatalf("requests = %#v, want native then unconstrained fallback", requests)
	}
}

func TestText2SpeechReturnsStructuredWorkerErrorWithoutCreatingOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_ref_audio","message":"reference audio is missing"}}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	reference := filepath.Join(tempDir, "reference.wav")
	if writeErr := os.WriteFile(reference, []byte("reference"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	out := filepath.Join(tempDir, "speech.wav")
	err := NewClient(Config{BaseURL: server.URL, ConsentConfirmed: true}).Text2Speech("hello", reference, out)
	if err == nil {
		t.Fatal("Text2Speech() error = nil")
	}
	if !strings.Contains(err.Error(), "invalid_ref_audio") || !strings.Contains(err.Error(), "reference audio is missing") {
		t.Fatalf("error = %q", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("output should not exist, stat error = %v", statErr)
	}
}

func TestText2SpeechUploadsReferenceOnceForAllCueRequests(t *testing.T) {
	reference := filepath.Join(t.TempDir(), "fixed-speaker.m4a")
	if err := os.WriteFile(reference, []byte("reference"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploads := 0
	var references []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reference":
			uploads++
			_, _ = w.Write([]byte(`{"reference":"reference:fixed-speaker.m4a"}`))
		case "/synthesize":
			var request synthesisRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			references = append(references, request.RefAudio)
			_, _ = w.Write([]byte(strings.Repeat("c", minimumWAVBytes)))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, ConsentConfirmed: true})
	for index, text := range []string{"Câu một.", "Câu hai."} {
		output := filepath.Join(t.TempDir(), fmt.Sprintf("%d.wav", index))
		if err := client.Text2Speech(text, reference, output); err != nil {
			t.Fatalf("Text2Speech(%d): %v", index, err)
		}
	}
	if uploads != 1 || len(references) != 2 || references[0] != "reference:fixed-speaker.m4a" || references[1] != "reference:fixed-speaker.m4a" {
		t.Fatalf("uploads=%d references=%#v", uploads, references)
	}
}

func TestProbeRequiresReadyHealthResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","ready":true,"device":"cuda","dtype":"float16"}`))
	}))
	defer server.Close()

	health, err := Probe(server.URL+"/", time.Second)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if health.Status != "ok" || health.Device != "cuda" || health.Dtype != "float16" {
		t.Fatalf("health = %#v", health)
	}
}

func TestProbeAcceptsPublishedOmniVoiceStudioHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"name":"Voice Clone","runtime":{"status":"installed","installed":true,"device":"cuda"}}`))
	}))
	defer server.Close()
	health, err := Probe(server.URL, time.Second)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !health.Ready || health.Status != "installed" || health.Device != "cuda" {
		t.Fatalf("health = %#v", health)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestProbeColabGPURejectsCPUWorker(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://kova-colab-worker.example/health" {
			t.Fatalf("health URL = %q", request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok","ready":true,"device":"cpu"}`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	if _, err := probeColabGPU("https://kova-colab-worker.example", time.Second, client); err == nil || !strings.Contains(err.Error(), "CUDA") {
		t.Fatalf("probeColabGPU() error = %v, want CUDA rejection", err)
	}
}

func TestProbeColabGPUAcceptsRemoteCUDAWorker(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok","ready":true,"device":"cuda:0","dtype":"float16"}`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	health, err := probeColabGPU("https://kova-colab-worker.example", time.Second, client)
	if err != nil {
		t.Fatalf("probeColabGPU() error = %v", err)
	}
	if health.Device != "cuda:0" || health.Dtype != "float16" {
		t.Fatalf("health = %#v", health)
	}
}

func TestProbeColabGPUWithAPIKeySendsBearerToken(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer test-session-worker-token" {
			t.Fatalf("Authorization = %q, want session bearer token", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok","ready":true,"device":"cuda:0"}`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	if _, err := probeColabGPUWithAPIKey("https://kova-colab-worker.example", "test-session-worker-token", time.Second, client); err != nil {
		t.Fatalf("authenticated Colab probe error = %v", err)
	}
}

func TestProbeColabGPURejectsNonRemoteURL(t *testing.T) {
	for _, endpoint := range []string{"http://worker.example", "https://localhost:11435", "https://127.0.0.1:11435", "https://127.0.0.2:11435", "https://worker.local"} {
		if _, err := ProbeColabGPU(endpoint, time.Second); err == nil {
			t.Fatalf("ProbeColabGPU(%q) error = nil", endpoint)
		}
	}
}

func TestPublishedOmniVoiceStudioProfileAndGenerateAdapter(t *testing.T) {
	reference := filepath.Join(t.TempDir(), "studio-reference.wav")
	if err := os.WriteFile(reference, []byte("studio-reference-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	profileUploads := 0
	generatedProfiles := make([]string, 0, 2)
	generatedDurations := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotToken := r.Header.Get("Authorization"); gotToken != "Bearer test-session-worker-token" {
			t.Fatalf("Authorization = %q, want session bearer token", gotToken)
		}
		switch r.URL.Path {
		case "/reference":
			w.WriteHeader(http.StatusNotFound)
		case "/profiles":
			profileUploads++
			if err := r.ParseMultipartForm(maxAudioResponseBytes); err != nil {
				t.Fatalf("parse profile form: %v", err)
			}
			file, _, err := r.FormFile("ref_audio")
			if err != nil {
				t.Fatalf("profile ref_audio: %v", err)
			}
			data, _ := io.ReadAll(file)
			_ = file.Close()
			if string(data) != "studio-reference-audio" || r.FormValue("consent_confirmed") != "true" || r.FormValue("language") != "vi" {
				t.Fatalf("profile form audio=%q consent=%q language=%q", data, r.FormValue("consent_confirmed"), r.FormValue("language"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"studio-fixed-profile"}`))
		case "/generate":
			if err := r.ParseMultipartForm(maxAudioResponseBytes); err != nil {
				t.Fatalf("parse generate form: %v", err)
			}
			generatedProfiles = append(generatedProfiles, r.FormValue("profile_id"))
			generatedDurations = append(generatedDurations, r.FormValue("duration"))
			if r.FormValue("output_format") != "wav" || r.FormValue("text") == "" {
				t.Fatalf("generate form output=%q text=%q", r.FormValue("output_format"), r.FormValue("text"))
			}
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write([]byte(strings.Repeat("s", minimumWAVBytes)))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, APIKey: "test-session-worker-token", ReferenceAudio: reference, ReferenceText: "Câu tham chiếu", Language: "vi", ConsentConfirmed: true})
	for index, text := range []string{"Câu một", "Câu hai"} {
		output := filepath.Join(t.TempDir(), fmt.Sprintf("studio-%d.wav", index))
		var err error
		if index == 0 {
			err = client.Text2SpeechWithDuration(text, "", output, 2.4)
		} else {
			err = client.Text2Speech(text, "", output)
		}
		if err != nil {
			t.Fatalf("Text2Speech(%d): %v", index, err)
		}
	}
	if profileUploads != 1 || len(generatedProfiles) != 2 || generatedProfiles[0] != "studio-fixed-profile" || generatedProfiles[1] != "studio-fixed-profile" {
		t.Fatalf("profileUploads=%d generatedProfiles=%#v", profileUploads, generatedProfiles)
	}
	if got, want := generatedDurations, []string{"2.4", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("generated durations = %#v, want %#v", got, want)
	}
}

func TestText2SpeechUsesExistingStudioProfileWithoutReferenceUpload(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generate" {
			t.Fatalf("path = %q, want /generate only", r.URL.Path)
		}
		if err := r.ParseMultipartForm(maxAudioResponseBytes); err != nil {
			t.Fatalf("parse generate form: %v", err)
		}
		requests++
		if got := r.FormValue("profile_id"); got != "fixed-voice-profile" {
			t.Fatalf("profile_id = %q", got)
		}
		if got := r.FormValue("duration"); got != "1.8" {
			t.Fatalf("duration = %q", got)
		}
		_, _ = w.Write([]byte(strings.Repeat("p", minimumWAVBytes)))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "profile.wav")
	client := NewClient(Config{BaseURL: server.URL, RequireReference: true})
	if err := client.Text2SpeechWithDuration("Câu có giọng cố định", "profile:fixed-voice-profile", output, 1.8); err != nil {
		t.Fatalf("Text2SpeechWithDuration() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("generate requests = %d, want 1", requests)
	}
}

func TestText2SpeechRejectsReferenceWithoutConsent(t *testing.T) {
	reference := filepath.Join(t.TempDir(), "reference.wav")
	if err := os.WriteFile(reference, []byte("reference"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	err := NewClient(Config{BaseURL: server.URL}).Text2Speech("Xin chào", reference, filepath.Join(t.TempDir(), "out.wav"))
	if err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("Text2Speech() error = %v, want consent error", err)
	}
	if calls != 0 {
		t.Fatalf("worker calls = %d, want no reference upload", calls)
	}
}

func TestText2SpeechRejectsBlankWorkerURL(t *testing.T) {
	err := NewClient(Config{}).Text2Speech("Xin chào", "", filepath.Join(t.TempDir(), "out.wav"))
	if err == nil || !strings.Contains(err.Error(), "remote worker URL") {
		t.Fatalf("Text2Speech() error = %v, want missing worker URL error", err)
	}
}

func TestText2SpeechRejectsEmptyInput(t *testing.T) {
	client := NewClient(Config{})
	if err := client.Text2Speech(" ", "", "out.wav"); err == nil {
		t.Fatal("Text2Speech() error = nil for empty text")
	}
}
