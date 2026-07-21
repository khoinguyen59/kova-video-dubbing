package ollama

import (
	"os"
	"strings"
	"testing"
)

// TestOllamaCloudSmoke is intentionally opt-in because it makes a billable
// request to the live Ollama Cloud service. Run it only with both
// RUN_OLLAMA_CLOUD_SMOKE=1 and OLLAMA_API_KEY set in the process environment.
func TestOllamaCloudSmoke(t *testing.T) {
	if os.Getenv("RUN_OLLAMA_CLOUD_SMOKE") != "1" {
		t.Skip("set RUN_OLLAMA_CLOUD_SMOKE=1 to run the live Ollama Cloud smoke test")
	}
	if strings.TrimSpace(os.Getenv(DefaultAPIKeyEnvName)) == "" {
		t.Skip("OLLAMA_API_KEY is not set")
	}

	model := os.Getenv("OLLAMA_CLOUD_SMOKE_MODEL")
	if model == "" {
		model = "gpt-oss:20b-cloud"
	}
	client, err := NewClient("https://ollama.com", model, DefaultAPIKeyEnvName, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	response, err := client.ChatCompletion("Translate exactly into Vietnamese and output only the translation: The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if strings.TrimSpace(response) == "" {
		t.Fatal("ChatCompletion() returned empty content")
	}
	t.Logf("Ollama Cloud returned %d characters", len([]rune(response)))
}
