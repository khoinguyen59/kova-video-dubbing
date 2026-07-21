package whisper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestColabWorkerOpenAIContractUsesVerboseTimestampedTranscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" {
			t.Fatalf("request = %s %s, want POST /v1/audio/transcriptions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer temporary-colab-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Fatalf("ParseMultipartForm() error = %v", err)
		}
		if got := r.FormValue("model"); got != "medium" {
			t.Fatalf("model = %q, want medium", got)
		}
		if got := r.FormValue("response_format"); got != "verbose_json" {
			t.Fatalf("response_format = %q, want verbose_json", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile(file) error = %v", err)
		}
		defer file.Close()
		if body, _ := io.ReadAll(file); string(body) != "fake audio" {
			t.Fatalf("uploaded file = %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"Hello world","language":"en","segments":[{"id":0,"seek":0,"start":0,"end":1.2,"text":"Hello world","tokens":[],"temperature":0,"avg_logprob":0,"compression_ratio":0,"no_speech_prob":0}],"words":[{"word":"Hello","start":0,"end":0.5},{"word":" world","start":0.5,"end":1.2}]}`))
	}))
	defer server.Close()

	audio := filepath.Join(t.TempDir(), "chunk.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0600); err != nil {
		t.Fatal(err)
	}
	client := NewClientWithModel(server.URL+"/v1", "temporary-colab-token", "", "medium")
	data, err := client.Transcription(audio, "en", t.TempDir())
	if err != nil {
		t.Fatalf("Transcription() error = %v", err)
	}
	if data.Language != "en" || data.Text != "Hello world" || len(data.Words) != 2 || len(data.Segments) != 1 {
		t.Fatalf("Transcription() = %+v", data)
	}
	if got := strings.TrimSpace(data.Words[1].Text); got != "world" {
		t.Fatalf("second timestamped word = %q", data.Words[1].Text)
	}
}
