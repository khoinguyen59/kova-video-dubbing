package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type capturedRequest struct {
	method      string
	path        string
	contentType string
	accept      string
	auth        string
	body        map[string]any
}

func TestChatCompletionSendsNativeOllamaRequest(t *testing.T) {
	t.Setenv(DefaultAPIKeyEnvName, "test-key")
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		decodeErr := json.NewDecoder(r.Body).Decode(&body)
		if decodeErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		captured <- capturedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			auth:        r.Header.Get("Authorization"),
			body:        body,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Xin chao"},"done":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/", "test-model:cloud", "", "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.ChatCompletion("Translate this")
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if result != "Xin chao" {
		t.Fatalf("ChatCompletion() = %q, want %q", result, "Xin chao")
	}

	got := <-captured
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", got.path)
	}
	if got.contentType != "application/json" || got.accept != "application/json" {
		t.Errorf("JSON headers = Content-Type %q, Accept %q", got.contentType, got.accept)
	}
	if got.auth != "Bearer test-key" {
		t.Errorf("Authorization header is missing or malformed")
	}
	if got.body["model"] != "test-model:cloud" {
		t.Errorf("model = %#v", got.body["model"])
	}
	stream, present := got.body["stream"]
	if !present || stream != false {
		t.Errorf("stream = %#v, present = %v; want explicit false", stream, present)
	}
	think, present := got.body["think"]
	if !present || think != false {
		t.Errorf("think = %#v, present = %v; want explicit false", think, present)
	}
	messages, ok := got.body["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v, want two messages", got.body["messages"])
	}
	system, ok := messages[0].(map[string]any)
	if !ok || system["role"] != "system" || system["content"] != systemPrompt {
		t.Errorf("system message = %#v", messages[0])
	}
	user, ok := messages[1].(map[string]any)
	if !ok || user["role"] != "user" || user["content"] != "Translate this" {
		t.Errorf("user message = %#v", messages[1])
	}
}

func TestChatCompletionOmitsAuthorizationWithoutAPIKey(t *testing.T) {
	t.Setenv(DefaultAPIKeyEnvName, "")
	auth := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-model", "", "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.ChatCompletion("hello"); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := <-auth; got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestChatCompletionRequiresAPIKeyForDirectOllamaCloud(t *testing.T) {
	t.Setenv(DefaultAPIKeyEnvName, "")
	client, err := NewClient("https://ollama.com", "deepseek-v4-flash:cloud", "", "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.ChatCompletion("hello")
	if err == nil || !strings.Contains(err.Error(), DefaultAPIKeyEnvName) {
		t.Fatalf("ChatCompletion() error = %v, want missing API key error", err)
	}
}

func TestChatCompletionUsesCustomAPIKeyEnvironmentVariable(t *testing.T) {
	const envName = "KOVA_TEST_OLLAMA_KEY"
	t.Setenv(DefaultAPIKeyEnvName, "wrong-key")
	t.Setenv(envName, "custom-test-key")
	auth := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-model", envName, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.ChatCompletion("hello"); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := <-auth; got != "Bearer custom-test-key" {
		t.Errorf("custom environment key was not used")
	}
}

func TestChatCompletionPrefersExplicitSessionAPIKey(t *testing.T) {
	t.Setenv(DefaultAPIKeyEnvName, "environment-key")
	auth := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	client, err := NewClientWithAPIKey(server.URL, "test-model", "session-key", "", "")
	if err != nil {
		t.Fatalf("NewClientWithAPIKey() error = %v", err)
	}
	if _, err := client.ChatCompletion("hello"); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := <-auth; got != "Bearer session-key" {
		t.Fatalf("Authorization = %q, want explicit session key", got)
	}
}

func TestChatCompletionFallsBackWhenDeepSeekCloudRequiresSubscription(t *testing.T) {
	t.Setenv(DefaultAPIKeyEnvName, "test-key")
	models := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		model, _ := body["model"].(string)
		models = append(models, model)
		if model == "deepseek-v4-flash:cloud" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"this model requires a subscription"}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"bản dịch"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "deepseek-v4-flash:cloud", "", "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.ChatCompletion("Translate this")
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if result != "bản dịch" {
		t.Fatalf("ChatCompletion() = %q", result)
	}
	if len(models) != 2 || models[0] != "deepseek-v4-flash:cloud" || models[1] != SubscriptionFallbackModel {
		t.Fatalf("models = %#v, want DeepSeek then %q", models, SubscriptionFallbackModel)
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "server root", base: "http://localhost:11434", want: "http://localhost:11434/api/chat"},
		{name: "trailing slash", base: "http://localhost:11434/", want: "http://localhost:11434/api/chat"},
		{name: "api base", base: "http://localhost:11434/api", want: "http://localhost:11434/api/chat"},
		{name: "complete endpoint", base: "http://localhost:11434/api/chat", want: "http://localhost:11434/api/chat"},
		{name: "path prefix", base: "https://example.com/ollama", want: "https://example.com/ollama/api/chat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEndpoint(tt.base)
			if err != nil {
				t.Fatalf("normalizeEndpoint() error = %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("normalizeEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChatCompletionReturnsSanitizedHTTPError(t *testing.T) {
	t.Setenv(DefaultAPIKeyEnvName, "test-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized token test-secret"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-model", "", "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.ChatCompletion("hello")
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error = %q, want status and API message", err)
	}
	if strings.Contains(err.Error(), "test-secret") {
		t.Errorf("error leaked the bearer token")
	}
}

func TestChatCompletionReturnsBodyErrorOnSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "missing-model", "", "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.ChatCompletion("hello")
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("ChatCompletion() error = %v, want body error", err)
	}
}

func TestChatCompletionRejectsMalformedOrEmptyResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed JSON", body: `{`, want: "decode ollama chat response"},
		{name: "missing content", body: `{"message":{"role":"assistant","content":""}}`, want: "missing message.content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "test-model", "", "")
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			_, err = client.ChatCompletion("hello")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ChatCompletion() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNewClientValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		model string
		proxy string
	}{
		{name: "missing model", base: "http://localhost:11434"},
		{name: "invalid base scheme", base: "ftp://localhost:11434", model: "m"},
		{name: "missing base host", base: "http:///api", model: "m"},
		{name: "base query", base: "http://localhost:11434?x=1", model: "m"},
		{name: "invalid proxy", base: "https://example.com", model: "m", proxy: "://bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClient(tt.base, tt.model, "", tt.proxy); err == nil {
				t.Fatal("NewClient() error = nil, want validation error")
			}
		})
	}
}

func TestProxyRouting(t *testing.T) {
	const proxyAddress = "http://127.0.0.1:18080"

	local, err := NewClient("http://localhost:11434", "m", "", proxyAddress)
	if err != nil {
		t.Fatalf("NewClient(local) error = %v", err)
	}
	localTransport := local.httpClient.Transport.(*http.Transport)
	if localTransport.Proxy != nil {
		t.Fatal("loopback Ollama client must bypass proxies")
	}

	remote, err := NewClient("https://example.com", "m", "", proxyAddress)
	if err != nil {
		t.Fatalf("NewClient(remote) error = %v", err)
	}
	remoteTransport := remote.httpClient.Transport.(*http.Transport)
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}}
	gotProxy, err := remoteTransport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if gotProxy == nil || gotProxy.String() != proxyAddress {
		t.Errorf("Proxy() = %v, want %s", gotProxy, proxyAddress)
	}
}

func TestClientSatisfiesChatCompleterShape(t *testing.T) {
	var _ interface {
		ChatCompletion(string) (string, error)
	} = (*Client)(nil)
}
