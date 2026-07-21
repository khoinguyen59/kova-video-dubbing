// Package gatewaytts implements the OpenAI-compatible Text-to-Speech shape
// exposed by API gateways such as 9Router. It is deliberately separate from
// OmniVoice: gateway voices are provider/model presets, while OmniVoice is the
// reference-audio cloning workflow.
package gatewaytts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultModel = "edge-tts"
	// An empty format reproduces the 9Router examples exactly: model + input
	// with binary MP3 returned by the selected provider. WAV remains opt-in for
	// gateways that expose an OpenAI-compatible response_format parameter.
	defaultResponseFormat = ""
	maxAudioBytes         = 256 << 20
	maxResponseBytes      = 1 << 20
)

type Client struct {
	endpoint       string
	apiKey         string
	model          string
	responseFormat string
	httpClient     *http.Client
}

type requestBody struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type jsonAudioResponse struct {
	Format string `json:"format"`
	Audio  string `json:"audio"`
}

// NewClient accepts either the full /v1/audio/speech URL shown by 9Router or
// the gateway base URL. The latter is expanded to the documented TTS route.
func NewClient(endpoint, apiKey, model, responseFormat string) *Client {
	if strings.TrimSpace(model) == "" {
		model = defaultModel
	}
	if strings.TrimSpace(responseFormat) == "" {
		responseFormat = defaultResponseFormat
	}
	return &Client{
		endpoint:       normalizeEndpoint(endpoint),
		apiKey:         strings.TrimSpace(apiKey),
		model:          strings.TrimSpace(model),
		responseFormat: strings.TrimSpace(responseFormat),
		httpClient:     &http.Client{Timeout: 90 * time.Second},
	}
}

func normalizeEndpoint(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	if strings.HasSuffix(value, "/v1/audio/speech") {
		return value
	}
	if strings.HasSuffix(value, "/v1") {
		return value + "/audio/speech"
	}
	return value + "/v1/audio/speech"
}

func (c *Client) Text2Speech(text, voice, outputFile string) error {
	if c == nil {
		return errors.New("gateway TTS client is nil")
	}
	if c.apiKey == "" {
		return errors.New("API gateway TTS key is empty")
	}
	if c.endpoint == "" {
		return errors.New("API gateway TTS endpoint is empty")
	}
	parsed, err := url.ParseRequestURI(c.endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("API gateway TTS endpoint must be a valid http/https URL")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("API gateway TTS input is empty")
	}
	if strings.TrimSpace(outputFile) == "" {
		return errors.New("API gateway TTS output file is empty")
	}

	voice = strings.TrimSpace(voice)
	if strings.EqualFold(voice, "auto") {
		voice = ""
	}
	body, err := json.Marshal(requestBody{
		Model:          c.model,
		Input:          text,
		Voice:          voice,
		ResponseFormat: c.responseFormat,
	})
	if err != nil {
		return fmt.Errorf("encode API gateway TTS request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.httpClient.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create API gateway TTS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav, audio/mpeg, application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call API gateway TTS: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
		return fmt.Errorf("API gateway TTS returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	audio, err := readAudio(response)
	if err != nil {
		return err
	}
	if len(audio) == 0 {
		return errors.New("API gateway TTS returned empty audio")
	}
	if err := os.MkdirAll(filepath.Dir(outputFile), 0o755); err != nil {
		return fmt.Errorf("create API gateway TTS output directory: %w", err)
	}
	if err := os.WriteFile(outputFile, audio, 0o644); err != nil {
		return fmt.Errorf("write API gateway TTS output: %w", err)
	}
	return nil
}

func readAudio(response *http.Response) ([]byte, error) {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAudioBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read API gateway TTS response: %w", err)
	}
	if len(body) > maxAudioBytes {
		return nil, fmt.Errorf("API gateway TTS audio exceeds %d bytes", maxAudioBytes)
	}
	if strings.Contains(contentType, "application/json") || looksLikeJSON(body) {
		var decoded jsonAudioResponse
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil, fmt.Errorf("decode API gateway TTS JSON response: %w", err)
		}
		if strings.TrimSpace(decoded.Audio) == "" {
			return nil, errors.New("API gateway TTS JSON response has no audio field")
		}
		audio, err := base64.StdEncoding.DecodeString(decoded.Audio)
		if err != nil {
			audio, err = base64.RawStdEncoding.DecodeString(decoded.Audio)
		}
		if err != nil {
			return nil, fmt.Errorf("decode API gateway TTS base64 audio: %w", err)
		}
		return audio, nil
	}
	return body, nil
}

func looksLikeJSON(body []byte) bool {
	return len(bytes.TrimSpace(body)) > 0 && bytes.TrimSpace(body)[0] == '{'
}
