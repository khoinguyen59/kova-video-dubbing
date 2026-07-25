package gatewaytts

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestText2SpeechUses9RouterCompatibleRequestAndStoresBinaryAudio(t *testing.T) {
	var got requestBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer gateway-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-binary-audio"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "speech.wav")
	client := NewClient(server.URL, "gateway-key", "google-tts/en", "wav")
	if err := client.Text2Speech("Xin chào", "", output); err != nil {
		t.Fatalf("Text2Speech() error = %v", err)
	}
	if got.Model != "google-tts/en" || got.Input != "Xin chào" || got.ResponseFormat != "wav" || got.Voice != "" {
		t.Fatalf("request = %#v", got)
	}
	audio, err := os.ReadFile(output)
	if err != nil || string(audio) != "ID3-binary-audio" {
		t.Fatalf("output = %q err=%v", audio, err)
	}
}

func TestText2SpeechSupportsBase64JSONResponseAndOptionalVoice(t *testing.T) {
	want := []byte("RIFF-json-audio")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request requestBody
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Voice != "en-US-JennyNeural" {
			t.Fatalf("voice = %q", request.Voice)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"format":"wav","audio":"` + base64.StdEncoding.EncodeToString(want) + `"}`))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "speech.wav")
	if err := NewClient(server.URL+"/v1/audio/speech", "key", "edge-tts", "wav").Text2Speech("Hi", "en-US-JennyNeural", output); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != string(want) {
		t.Fatalf("output = %q err=%v", got, err)
	}
}

func TestText2SpeechDefaultRequestMatchesGatewayModelAndInputExample(t *testing.T) {
	var got requestBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("ID3-default"))
	}))
	defer server.Close()

	if err := NewClient(server.URL, "key", "", "").Text2Speech("Hello", "auto", filepath.Join(t.TempDir(), "out.wav")); err != nil {
		t.Fatal(err)
	}
	if got.Model != "edge-tts" || got.Input != "Hello" || got.ResponseFormat != "" || got.Voice != "" {
		t.Fatalf("request = %#v, want model+input only", got)
	}
}

func TestText2SpeechRejectsInvalidConfigBeforeOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "speech.wav")
	if err := NewClient("", "", "", "").Text2Speech("hello", "", output); err == nil {
		t.Fatal("Text2Speech() error = nil")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("output should not exist: %v", err)
	}
}

func TestGatewayClientFastFailsAtOneAttempt(t *testing.T) {
	client := NewClient("https://gateway.example", "key", "edge-tts", "")
	if got := client.TTSMaxAttempts(); got != 1 {
		t.Fatalf("TTSMaxAttempts() = %d, want 1", got)
	}
}

func TestText2SpeechRetriesTransientGatewayFailureWithoutRunnerRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"temporary upstream failure"}`))
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-recovered"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "recovered.mp3")
	if err := NewClient(server.URL, "key", "google-tts/vi", "").Text2Speech("Xin chào", "auto", output); err != nil {
		t.Fatalf("Text2Speech() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("gateway attempts = %d, want 2", attempts)
	}
	if audio, err := os.ReadFile(output); err != nil || string(audio) != "ID3-recovered" {
		t.Fatalf("output = %q err=%v", audio, err)
	}
}
