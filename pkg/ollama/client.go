package ollama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL       = "http://localhost:11434"
	DefaultAPIKeyEnvName = "OLLAMA_API_KEY"
	// SubscriptionFallbackModel keeps the Kova default requested by the user
	// while allowing a Cloud key without the DeepSeek subscription entitlement
	// to finish a subtitle job on a lower-cost Cloud model.
	SubscriptionFallbackModel = "gpt-oss:20b-cloud"

	requestTimeout  = 5 * time.Minute
	maxResponseSize = 4 << 20 // 4 MiB is ample for subtitle translation responses.
	maxErrorLength  = 1024

	systemPrompt = "You are an assistant that helps with subtitle translation."
)

// Client implements the subtitle translation ChatCompleter contract through
// Ollama's native /api/chat endpoint. A caller may supply an in-memory session
// key (for the desktop form) or an environment-variable name. The caller is
// responsible for keeping a session key out of persistent configuration.
type Client struct {
	endpoint      string
	model         string
	apiKey        string
	apiKeyEnvName string
	httpClient    *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// Do not add omitempty: Ollama streams NDJSON by default when this field is
	// absent, while this client deliberately handles one bounded JSON response.
	Stream bool `json:"stream"`
	// Subtitle translation needs final text only. Disabling thinking avoids
	// consuming cloud usage on hidden reasoning output.
	Think bool `json:"think"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
	Error   string      `json:"error"`
}

// NewClient creates an Ollama chat client.
//
// baseURL may be the server root, an /api base, or the complete /api/chat URL.
// An empty baseURL uses DefaultBaseURL. model is required. apiKeyEnvName names
// the environment variable containing an optional bearer token; when empty it
// defaults to OLLAMA_API_KEY. proxyAddr is used only for non-loopback Ollama
// servers so a configured application proxy cannot break local Ollama access.
func NewClient(baseURL, model, apiKeyEnvName, proxyAddr string) (*Client, error) {
	return newClient(baseURL, model, "", apiKeyEnvName, proxyAddr)
}

// NewClientWithAPIKey creates an Ollama client that prefers an explicit,
// in-memory session key over the configured environment variable. This is
// intended for a key entered into Kova's desktop UI; it is never written by
// this package and callers must not serialize it to disk.
func NewClientWithAPIKey(baseURL, model, apiKey, apiKeyEnvName, proxyAddr string) (*Client, error) {
	return newClient(baseURL, model, apiKey, apiKeyEnvName, proxyAddr)
}

func newClient(baseURL, model, apiKey, apiKeyEnvName, proxyAddr string) (*Client, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("ollama model is required")
	}

	endpointURL, err := normalizeEndpoint(baseURL)
	if err != nil {
		return nil, err
	}

	var proxyURL *url.URL
	if strings.TrimSpace(proxyAddr) != "" {
		proxyURL, err = parseHTTPURL(proxyAddr, "proxy")
		if err != nil {
			return nil, err
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if isLoopbackHost(endpointURL.Hostname()) {
		// Local Ollama should never be sent through either the app proxy or an
		// environment proxy.
		transport.Proxy = nil
	} else if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	apiKeyEnvName = strings.TrimSpace(apiKeyEnvName)
	if apiKeyEnvName == "" {
		apiKeyEnvName = DefaultAPIKeyEnvName
	}

	return &Client{
		endpoint:      endpointURL.String(),
		model:         model,
		apiKey:        strings.TrimSpace(apiKey),
		apiKeyEnvName: apiKeyEnvName,
		httpClient: &http.Client{
			Timeout:   requestTimeout,
			Transport: transport,
		},
	}, nil
}

// ChatCompletion sends one non-streaming Ollama chat request and returns only
// the assistant message content expected by the existing translation pipeline.
func (c *Client) ChatCompletion(query string) (string, error) {
	result, err := c.chatCompletion(query, c.model)
	if err == nil || !shouldRetryWithSubscriptionFallback(c.endpoint, c.model, err) {
		return result, err
	}
	return c.chatCompletion(query, SubscriptionFallbackModel)
}

func (c *Client) chatCompletion(query, model string) (string, error) {
	payload, err := json.Marshal(chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: query},
		},
		Stream: false,
		Think:  false,
	})
	if err != nil {
		return "", fmt.Errorf("encode ollama chat request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create ollama chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	apiKey := c.apiKey
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(c.apiKeyEnvName))
	}
	if isOllamaCloudHost(c.endpoint) && apiKey == "" {
		return "", fmt.Errorf("%s must be set to call the Ollama Cloud API", c.apiKeyEnvName)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama chat request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read ollama chat response: %w", err)
	}
	if len(body) > maxResponseSize {
		return "", fmt.Errorf("ollama chat response exceeds %d bytes", maxResponseSize)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", newAPIError(resp.StatusCode, body, apiKey)
	}

	var result chatResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode ollama chat response: %w", err)
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", fmt.Errorf("ollama chat failed: %s", safeErrorMessage(result.Error, apiKey))
	}
	if strings.TrimSpace(result.Message.Content) == "" {
		return "", fmt.Errorf("ollama chat response is missing message.content")
	}

	return result.Message.Content, nil
}

func shouldRetryWithSubscriptionFallback(_ string, model string, err error) bool {
	if err == nil || !strings.EqualFold(strings.TrimSpace(model), "deepseek-v4-flash:cloud") {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "requires a subscription") || strings.Contains(message, "subscription required")
}

func normalizeEndpoint(rawBaseURL string) (*url.URL, error) {
	rawBaseURL = strings.TrimSpace(rawBaseURL)
	if rawBaseURL == "" {
		rawBaseURL = DefaultBaseURL
	}

	u, err := parseHTTPURL(rawBaseURL, "base URL")
	if err != nil {
		return nil, err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("ollama base URL must not contain a query or fragment")
	}

	path := strings.TrimRight(u.Path, "/")
	switch {
	case path == "":
		path = "/api/chat"
	case strings.HasSuffix(path, "/api/chat"):
		// The caller supplied the complete endpoint.
	case strings.HasSuffix(path, "/api"):
		path += "/chat"
	default:
		path += "/api/chat"
	}
	u.Path = path
	u.RawPath = ""
	return u, nil
}

func parseHTTPURL(rawURL, label string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid ollama %s: %w", label, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid ollama %s: scheme must be http or https", label)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid ollama %s: host is required", label)
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isOllamaCloudHost(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && strings.EqualFold(u.Hostname(), "ollama.com")
}

func newAPIError(statusCode int, body []byte, apiKey string) error {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err == nil && strings.TrimSpace(response.Error) != "" {
		return fmt.Errorf("ollama chat failed with HTTP %d: %s", statusCode, safeErrorMessage(response.Error, apiKey))
	}
	return fmt.Errorf("ollama chat failed with HTTP %d", statusCode)
}

func safeErrorMessage(message, apiKey string) string {
	message = strings.TrimSpace(message)
	if apiKey != "" {
		message = strings.ReplaceAll(message, apiKey, "[redacted]")
	}
	if len(message) > maxErrorLength {
		message = message[:maxErrorLength] + "..."
	}
	return message
}
