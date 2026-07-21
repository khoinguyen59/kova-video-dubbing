package service

import (
	"kova/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewChatCompleterPassesInMemoryOllamaSessionKey(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })
	t.Setenv("OLLAMA_API_KEY", "environment-key")

	auth := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	config.Conf.Llm = config.LlmConfig{
		Provider:      "ollama",
		BaseUrl:       server.URL,
		Model:         "test-model",
		ApiKeyEnv:     "OLLAMA_API_KEY",
		SessionApiKey: "session-key",
	}
	result, err := newChatCompleter().ChatCompletion("hello")
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if result != "ok" {
		t.Fatalf("ChatCompletion() = %q, want ok", result)
	}
	if got := <-auth; got != "Bearer session-key" {
		t.Fatalf("Authorization = %q, want session key", got)
	}
}

func TestRefreshTranslationClientsUsesCurrentSessionKey(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })

	auth := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	config.Conf.Llm = config.LlmConfig{
		Provider:      "ollama",
		BaseUrl:       server.URL,
		Model:         "test-model",
		ApiKeyEnv:     "OLLAMA_API_KEY",
		SessionApiKey: "updated-session-key",
	}
	service := &Service{}
	service.RefreshTranslationClients()
	if service.YouTubeSubtitleSrv == nil || service.YouTubeSubtitleSrv.translator == nil {
		t.Fatal("RefreshTranslationClients() did not create a translator")
	}
	if _, err := service.YouTubeSubtitleSrv.translator.chatCompleter.ChatCompletion("hello"); err != nil {
		t.Fatalf("refreshed ChatCompletion() error = %v", err)
	}
	if got := <-auth; got != "Bearer updated-session-key" {
		t.Fatalf("Authorization = %q, want current session key", got)
	}
}
