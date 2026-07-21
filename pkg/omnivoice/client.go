package omnivoice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kova/internal/types"
)

const (
	defaultLanguage       = "vi"
	defaultSpeed          = 1.0
	defaultNumStep        = 32
	defaultTimeoutSeconds = 1800
	maxAudioResponseBytes = 256 << 20
	maxErrorResponseBytes = 1 << 20
	minimumWAVBytes       = 45 // A 44-byte WAV is only a header with no samples.
)

type Config struct {
	BaseURL string
	// APIKey is the temporary bearer token printed by a Kova Colab notebook
	// run. It is intentionally supplied by the caller, never read from disk.
	APIKey           string
	Language         string
	ReferenceAudio   string
	ReferenceText    string
	Instruct         string
	Speed            float64
	NumStep          int
	TimeoutSeconds   int
	RequireReference bool
	ConsentConfirmed bool
}

type Client struct {
	baseURL          string
	endpoint         string
	apiKey           string
	language         string
	referenceAudio   string
	referenceText    string
	instruct         string
	speed            float64
	numStep          int
	requireReference bool
	consentConfirmed bool
	httpClient       *http.Client
	referenceMu      sync.Mutex
	referenceCache   map[string]string
}

// ProbeColabGPU is stricter than Probe: a Kova voice-clone job can only use a
// remote HTTPS worker that reports CUDA. This avoids silently falling back to
// local CPU inference or creating an unapproved voice on the desktop.
func ProbeColabGPU(baseURL string, timeout time.Duration) (Health, error) {
	return probeColabGPU(baseURL, timeout, nil)
}

// ProbeColabGPUWithAPIKey verifies a secured remote Kova worker. The token is
// sent only as an Authorization header and is deliberately never included in
// an error message or URL.
func ProbeColabGPUWithAPIKey(baseURL, apiKey string, timeout time.Duration) (Health, error) {
	return probeColabGPUWithAPIKey(baseURL, apiKey, timeout, nil)
}

// probeColabGPU accepts an optional client so package tests can validate the
// remote-CUDA contract without opening a real tunnel. Production callers use
// the normal HTTP client through ProbeColabGPU.
func probeColabGPU(baseURL string, timeout time.Duration, client *http.Client) (Health, error) {
	return probeColabGPUWithAPIKey(baseURL, "", timeout, client)
}

func probeColabGPUWithAPIKey(baseURL, apiKey string, timeout time.Duration, client *http.Client) (Health, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return Health{}, errors.New("Colab worker URL must be a remote HTTPS URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return Health{}, errors.New("local OmniVoice workers are not allowed for Kova cloning")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return Health{}, errors.New("local OmniVoice workers are not allowed for Kova cloning")
	}
	health, err := probeWithHTTPClientWithAPIKey(baseURL, apiKey, timeout, client)
	if err != nil {
		return Health{}, err
	}
	if !strings.Contains(strings.ToLower(health.Device), "cuda") {
		return Health{}, fmt.Errorf("Colab worker must report CUDA, got %q", health.Device)
	}
	return health, nil
}

// Health is the small, stable contract used by the desktop before a Colab
// tunnel is accepted. It intentionally does not expose a model path or other
// runtime-specific internals.
type Health struct {
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
	Device string `json:"device"`
	Dtype  string `json:"dtype"`
}

// Probe verifies that a pasted worker URL is a Kova OmniVoice worker, rather
// than merely a reachable Cloudflare page. It calls the worker's /health route
// with a short timeout and never starts an inference request.
func Probe(baseURL string, timeout time.Duration) (Health, error) {
	return probeWithHTTPClient(baseURL, timeout, nil)
}

// ProbeWithAPIKey is the generic authenticated health probe. Most desktop
// callers should use ProbeColabGPUWithAPIKey so the CUDA-only policy is kept.
func ProbeWithAPIKey(baseURL, apiKey string, timeout time.Duration) (Health, error) {
	return probeWithHTTPClientWithAPIKey(baseURL, apiKey, timeout, nil)
}

func probeWithHTTPClient(baseURL string, timeout time.Duration, client *http.Client) (Health, error) {
	return probeWithHTTPClientWithAPIKey(baseURL, "", timeout, client)
}

func probeWithHTTPClientWithAPIKey(baseURL, apiKey string, timeout time.Duration, client *http.Client) (Health, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return Health{}, errors.New("OmniVoice worker URL must be a valid http/https URL")
	}
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else if client.Timeout <= 0 {
		copy := *client
		copy.Timeout = timeout
		client = &copy
	}
	request, err := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return Health{}, fmt.Errorf("create OmniVoice worker health request: %w", err)
	}
	applyBearerToken(request, apiKey)
	response, err := client.Do(request)
	if err != nil {
		return Health{}, fmt.Errorf("call OmniVoice worker health endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Health{}, decodeWorkerError(response)
	}
	var payload struct {
		Status  string `json:"status"`
		Ready   bool   `json:"ready"`
		Device  string `json:"device"`
		Dtype   string `json:"dtype"`
		OK      bool   `json:"ok"`
		Name    string `json:"name"`
		Runtime struct {
			Status    string `json:"status"`
			Device    string `json:"device"`
			Dtype     string `json:"dtype"`
			Installed bool   `json:"installed"`
		} `json:"runtime"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorResponseBytes)).Decode(&payload); err != nil {
		return Health{}, fmt.Errorf("decode OmniVoice worker health response: %w", err)
	}
	health := Health{Status: payload.Status, Ready: payload.Ready, Device: payload.Device, Dtype: payload.Dtype}
	if payload.OK && strings.Contains(strings.ToLower(payload.Name), "voice") {
		health.Status = payload.Runtime.Status
		health.Ready = payload.Runtime.Installed && payload.Runtime.Status != "error" && payload.Runtime.Status != "not_installed"
		health.Device = payload.Runtime.Device
		health.Dtype = payload.Runtime.Dtype
	}
	if !health.Ready {
		return Health{}, errors.New("OmniVoice worker is not ready")
	}
	return health, nil
}

type synthesisRequest struct {
	Text     string   `json:"text"`
	RefAudio string   `json:"ref_audio,omitempty"`
	RefText  string   `json:"ref_text,omitempty"`
	Language string   `json:"language,omitempty"`
	Instruct string   `json:"instruct,omitempty"`
	Speed    float64  `json:"speed"`
	NumSteps int      `json:"num_steps"`
	Duration *float64 `json:"duration,omitempty"`
}

type workerErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Detail string `json:"detail"`
}

func NewClient(cfg Config) *Client {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	language := strings.TrimSpace(cfg.Language)
	if language == "" {
		language = defaultLanguage
	}
	speed := cfg.Speed
	if speed <= 0 {
		speed = defaultSpeed
	}
	numStep := cfg.NumStep
	if numStep <= 0 {
		numStep = defaultNumStep
	}
	timeoutSeconds := cfg.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTimeoutSeconds
	}

	baseURL = strings.TrimRight(baseURL, "/")
	endpoint := ""
	if baseURL != "" {
		endpoint = baseURL + "/synthesize"
	}
	return &Client{
		baseURL:          baseURL,
		endpoint:         endpoint,
		apiKey:           strings.TrimSpace(cfg.APIKey),
		language:         language,
		referenceAudio:   strings.TrimSpace(strings.TrimPrefix(cfg.ReferenceAudio, "local:")),
		referenceText:    strings.TrimSpace(cfg.ReferenceText),
		instruct:         strings.TrimSpace(cfg.Instruct),
		speed:            speed,
		numStep:          numStep,
		requireReference: cfg.RequireReference,
		consentConfirmed: cfg.ConsentConfirmed,
		httpClient:       &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		referenceCache:   make(map[string]string),
	}
}

func applyBearerToken(request *http.Request, apiKey string) {
	if request == nil {
		return
	}
	if token := strings.TrimSpace(apiKey); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

func (c *Client) applyAuthorization(request *http.Request) {
	if c == nil {
		return
	}
	applyBearerToken(request, c.apiKey)
}

func (c *Client) Text2Speech(text, voice, outputFile string) error {
	return c.text2Speech(text, voice, outputFile, nil)
}

// Text2SpeechWithDuration asks OmniVoice to synthesize a subtitle slot at its
// native target duration. OmniVoice uses duration as a generation constraint,
// avoiding post-generation time stretching whenever possible.
func (c *Client) Text2SpeechWithDuration(text, voice, outputFile string, duration float64) error {
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return fmt.Errorf("OmniVoice duration must be finite and > 0: %v", duration)
	}
	if err := c.text2Speech(text, voice, outputFile, &duration); err == nil {
		return nil
	} else {
		// Duration is only a model hint. On an occasional very short cue the
		// model returns no samples under that constraint, so retry the exact same
		// reference clone without it. The dubbing layer still measures and fits
		// the resulting waveform to the original subtitle slot.
		if fallbackErr := c.text2Speech(text, voice, outputFile, nil); fallbackErr == nil {
			return nil
		} else {
			return fmt.Errorf("OmniVoice duration synthesis failed: %w; unconstrained fallback failed: %v", err, fallbackErr)
		}
	}
}

func (c *Client) text2Speech(text, voice, outputFile string, duration *float64) error {
	if c == nil {
		return errors.New("OmniVoice client is nil")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("OmniVoice text is empty")
	}
	if strings.TrimSpace(outputFile) == "" {
		return errors.New("OmniVoice output file is empty")
	}

	refAudio := strings.TrimSpace(strings.TrimPrefix(voice, "local:"))
	if refAudio == "" {
		refAudio = c.referenceAudio
	}
	// A previously created Studio profile is already an opaque, consented voice
	// identity on the worker. Do not mistake profile:<id> for a desktop path or
	// upload a new reference for each cue: that would create voice drift and
	// breaks the fixed-voice contract.
	if strings.HasPrefix(refAudio, "profile:") {
		profileID := strings.TrimSpace(strings.TrimPrefix(refAudio, "profile:"))
		if profileID == "" {
			return errors.New("KOVA Voice Studio profile id is empty")
		}
		if strings.TrimSpace(c.baseURL) == "" {
			return errors.New("OmniVoice remote worker URL is required; configure the Google Colab URL first")
		}
		return c.studioSynthesize(text, profileID, outputFile, duration)
	}
	if refAudio != "" && !filepath.IsAbs(refAudio) {
		absoluteRef, absErr := filepath.Abs(refAudio)
		if absErr != nil {
			return fmt.Errorf("resolve OmniVoice reference audio: %w", absErr)
		}
		refAudio = absoluteRef
	}
	if c.requireReference && refAudio == "" {
		return errors.New("OmniVoice clone mode requires a local reference_audio or --voice-clone-source")
	}
	if refAudio != "" && !c.consentConfirmed {
		return errors.New("voice clone consent is required before using reference audio")
	}
	if strings.TrimSpace(c.baseURL) == "" {
		return errors.New("OmniVoice remote worker URL is required; configure the Google Colab URL first")
	}
	if refAudio != "" {
		var uploadErr error
		refAudio, uploadErr = c.uploadReference(refAudio)
		if uploadErr != nil {
			return uploadErr
		}
	}
	if strings.HasPrefix(refAudio, "profile:") {
		return c.studioSynthesize(text, strings.TrimPrefix(refAudio, "profile:"), outputFile, duration)
	}
	payload := synthesisRequest{
		Text:     text,
		RefAudio: refAudio,
		RefText:  c.referenceText,
		Language: c.language,
		Instruct: c.instruct,
		Speed:    c.speed,
		NumSteps: c.numStep,
		Duration: duration,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode OmniVoice request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OmniVoice request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav")
	c.applyAuthorization(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call OmniVoice worker at %s: %w", safeWorkerOrigin(c.endpoint), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeWorkerError(resp)
	}
	return commitAudioResponse(resp.Body, outputFile)
}

func commitAudioResponse(body io.Reader, outputFile string) error {
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		return fmt.Errorf("create OmniVoice output directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputFile), ".omnivoice-*.wav")
	if err != nil {
		return fmt.Errorf("create OmniVoice temporary output: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, copyErr := io.Copy(tmp, io.LimitReader(body, maxAudioResponseBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return fmt.Errorf("write OmniVoice audio: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close OmniVoice audio: %w", closeErr)
	}
	if written < minimumWAVBytes {
		return fmt.Errorf("OmniVoice worker returned empty WAV audio (%d bytes)", written)
	}
	if written > maxAudioResponseBytes {
		return fmt.Errorf("OmniVoice audio exceeds %d bytes", maxAudioResponseBytes)
	}
	if err := os.Remove(outputFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace OmniVoice output: %w", err)
	}
	if err := os.Rename(tmpName, outputFile); err != nil {
		return fmt.Errorf("commit OmniVoice output: %w", err)
	}
	return nil
}

// studioSynthesize keeps the remote Studio profile stable for every cue while
// forwarding the original SRT slot duration. Earlier profile requests dropped
// duration even though KOVA Voice Studio accepts it, forcing the assembler to
// time-stretch nearly every generated clip afterward.
func (c *Client) studioSynthesize(text, profileID, outputFile string, duration *float64) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"text":          text,
		"profile_id":    profileID,
		"ref_text":      c.referenceText,
		"language":      c.language,
		"speed":         strconv.FormatFloat(c.speed, 'f', -1, 64),
		"num_step":      strconv.Itoa(c.numStep),
		"output_format": "wav",
	}
	if duration != nil {
		fields["duration"] = strconv.FormatFloat(*duration, 'f', -1, 64)
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return fmt.Errorf("encode KOVA Voice Studio field %s: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish KOVA Voice Studio request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/generate", &body)
	if err != nil {
		return fmt.Errorf("create KOVA Voice Studio generation request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "audio/wav")
	c.applyAuthorization(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call KOVA Voice Studio at %s: %w", safeWorkerOrigin(c.baseURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeWorkerError(resp)
	}
	return commitAudioResponse(resp.Body, outputFile)
}

func (c *Client) uploadStudioProfile(localPath string) (string, error) {
	if !c.consentConfirmed {
		return "", errors.New("voice clone consent is required before uploading reference audio")
	}
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open KOVA Voice Studio reference audio: %w", err)
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"name":              "Kova fixed clone - " + filepath.Base(localPath),
		"consent_confirmed": strconv.FormatBool(c.consentConfirmed),
		"ref_text":          c.referenceText,
		"language":          c.language,
	} {
		if err := writer.WriteField(name, value); err != nil {
			return "", fmt.Errorf("encode KOVA Voice Studio profile field %s: %w", name, err)
		}
	}
	part, err := writer.CreateFormFile("ref_audio", filepath.Base(localPath))
	if err != nil {
		return "", fmt.Errorf("create KOVA Voice Studio audio field: %w", err)
	}
	if _, err := io.Copy(part, io.LimitReader(file, maxAudioResponseBytes+1)); err != nil {
		return "", fmt.Errorf("encode KOVA Voice Studio reference audio: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finish KOVA Voice Studio profile request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/profiles", &body)
	if err != nil {
		return "", fmt.Errorf("create KOVA Voice Studio profile request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuthorization(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload reference audio to KOVA Voice Studio at %s: %w", safeWorkerOrigin(c.baseURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", decodeWorkerError(resp)
	}
	var profile struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorResponseBytes)).Decode(&profile); err != nil {
		return "", fmt.Errorf("decode KOVA Voice Studio profile response: %w", err)
	}
	if strings.TrimSpace(profile.ID) == "" {
		return "", errors.New("KOVA Voice Studio returned an empty profile id")
	}
	return "profile:" + profile.ID, nil
}

// uploadReference moves a selected local reference clip to the worker once.
// This is essential when the worker runs on Colab: its filesystem cannot read
// a C:\\ path from the desktop. The worker returns an opaque reference:<id>
// value which is then reused for every cue in the dubbing job.
func (c *Client) uploadReference(localPath string) (string, error) {
	if c == nil {
		return "", errors.New("OmniVoice client is nil")
	}
	if !c.consentConfirmed {
		return "", errors.New("voice clone consent is required before uploading reference audio")
	}
	localPath = strings.TrimSpace(strings.TrimPrefix(localPath, "local:"))
	if localPath == "" {
		return "", errors.New("OmniVoice reference audio is empty")
	}
	absolutePath, err := filepath.Abs(localPath)
	if err != nil {
		return "", fmt.Errorf("resolve OmniVoice reference audio: %w", err)
	}
	stat, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("read OmniVoice reference audio: %w", err)
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 {
		return "", errors.New("OmniVoice reference audio must be a non-empty regular file")
	}
	if stat.Size() > maxAudioResponseBytes {
		return "", fmt.Errorf("OmniVoice reference audio exceeds %d bytes", maxAudioResponseBytes)
	}
	cacheKey := fmt.Sprintf("%s:%d:%d", absolutePath, stat.Size(), stat.ModTime().UnixNano())

	c.referenceMu.Lock()
	defer c.referenceMu.Unlock()
	if reference := c.referenceCache[cacheKey]; reference != "" {
		return reference, nil
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		return "", fmt.Errorf("open OmniVoice reference audio: %w", err)
	}
	defer file.Close()

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/reference", file)
	if err != nil {
		return "", fmt.Errorf("create OmniVoice reference upload: %w", err)
	}
	req.ContentLength = stat.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-OmniVoice-Reference-Name", filepath.Base(absolutePath))
	c.applyAuthorization(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload reference audio to OmniVoice worker at %s: %w", safeWorkerOrigin(c.endpoint), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		studioReference, studioErr := c.uploadStudioProfile(absolutePath)
		if studioErr != nil {
			return "", studioErr
		}
		c.referenceCache[cacheKey] = studioReference
		return studioReference, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", decodeWorkerError(resp)
	}
	var result struct {
		Reference string `json:"reference"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorResponseBytes)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode OmniVoice reference upload response: %w", err)
	}
	if !strings.HasPrefix(result.Reference, "reference:") || len(strings.TrimPrefix(result.Reference, "reference:")) < 8 {
		return "", errors.New("OmniVoice worker returned an invalid reference id")
	}
	c.referenceCache[cacheKey] = result.Reference
	return result.Reference, nil
}

func decodeWorkerError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseBytes))
	if err != nil {
		return fmt.Errorf("OmniVoice worker returned HTTP %d", resp.StatusCode)
	}
	var decoded workerErrorResponse
	if json.Unmarshal(body, &decoded) == nil && decoded.Error.Message != "" {
		if decoded.Error.Code != "" {
			return fmt.Errorf("OmniVoice worker returned HTTP %d (%s): %s", resp.StatusCode, decoded.Error.Code, decoded.Error.Message)
		}
		return fmt.Errorf("OmniVoice worker returned HTTP %d: %s", resp.StatusCode, decoded.Error.Message)
	}
	if decoded.Detail != "" {
		return fmt.Errorf("OmniVoice worker returned HTTP %d: %s", resp.StatusCode, decoded.Detail)
	}
	return fmt.Errorf("OmniVoice worker returned HTTP %d", resp.StatusCode)
}

func safeWorkerOrigin(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "configured endpoint"
	}
	return parsed.Scheme + "://" + parsed.Host
}

var _ types.Ttser = (*Client)(nil)
