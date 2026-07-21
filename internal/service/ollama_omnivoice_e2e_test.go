package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kova/pkg/ollama"
	"kova/pkg/omnivoice"
)

// TestOllamaCloudToOmniVoiceColabSmoke is opt-in: it calls the live Cloud API
// and a user-provided remote Colab GPU worker. It is excluded from ordinary
// test runs and never starts or uses OmniVoice on the desktop.
func TestOllamaCloudToOmniVoiceColabSmoke(t *testing.T) {
	if os.Getenv("RUN_OLLAMA_OMNIVOICE_E2E") != "1" {
		t.Skip("set RUN_OLLAMA_OMNIVOICE_E2E=1 to run the live integration smoke test")
	}
	if strings.TrimSpace(os.Getenv(ollama.DefaultAPIKeyEnvName)) == "" {
		t.Skip("OLLAMA_API_KEY is not set")
	}

	workerURL := strings.TrimSpace(os.Getenv("KOVA_COLAB_OMNIVOICE_URL"))
	if workerURL == "" {
		t.Skip("KOVA_COLAB_OMNIVOICE_URL is not set")
	}
	workerToken := strings.TrimSpace(os.Getenv("KOVA_OMNIVOICE_SESSION_TOKEN"))
	if workerToken == "" {
		t.Skip("KOVA_OMNIVOICE_SESSION_TOKEN is not set")
	}
	referenceAudio := strings.TrimSpace(os.Getenv("KOVA_OMNIVOICE_REFERENCE_AUDIO"))
	if referenceAudio == "" {
		t.Skip("KOVA_OMNIVOICE_REFERENCE_AUDIO is not set")
	}
	if _, err := os.Stat(referenceAudio); err != nil {
		t.Skipf("reference audio is unavailable: %v", err)
	}
	if _, err := omnivoice.ProbeColabGPUWithAPIKey(workerURL, workerToken, 12*time.Second); err != nil {
		t.Fatalf("remote Colab worker is not CUDA-ready: %v", err)
	}

	chat, err := ollama.NewClient("https://ollama.com", "deepseek-v4-flash:cloud", ollama.DefaultAPIKeyEnvName, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	translated, err := chat.ChatCompletion("Translate exactly into Vietnamese and output only the translation: Good morning, welcome to our video.")
	if err != nil {
		t.Fatalf("Cloud translation error = %v", err)
	}
	if strings.TrimSpace(translated) == "" {
		t.Fatal("Cloud translation returned empty text")
	}

	output := filepath.Join(t.TempDir(), "ollama-to-omnivoice.wav")
	tts := omnivoice.NewClient(omnivoice.Config{
		BaseURL:          workerURL,
		APIKey:           workerToken,
		Language:         "vi",
		Speed:            1,
		NumStep:          8,
		TimeoutSeconds:   180,
		RequireReference: true,
		ConsentConfirmed: true,
	})
	if err := tts.Text2Speech(translated, referenceAudio, output); err != nil {
		t.Fatalf("OmniVoice synthesis error = %v", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("OmniVoice output is empty")
	}
	t.Logf("translated %d characters into %d bytes of WAV", len([]rune(translated)), info.Size())
}
