package config

import (
	"errors"
	"fmt"
	"kova/log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"go.uber.org/zap"
)

var ConfigBackup Config

type App struct {
	SegmentDuration       int      `toml:"segment_duration"`
	TranscribeParallelNum int      `toml:"transcribe_parallel_num"`
	TranslateParallelNum  int      `toml:"translate_parallel_num"`
	TranscribeMaxAttempts int      `toml:"transcribe_max_attempts"`
	TranslateMaxAttempts  int      `toml:"translate_max_attempts"`
	MaxSentenceLength     int      `toml:"max_sentence_length"`
	EnableBlockVttBatch   bool     `toml:"enable_block_vtt_batch"`
	VttBatchSize          int      `toml:"vtt_batch_size"`
	TargetLanguageFirst   bool     `toml:"target_language_first"`
	ShortSubtitleMaxChars int      `toml:"short_subtitle_max_chars"`
	Proxy                 string   `toml:"proxy"`
	ParsedProxy           *url.URL `toml:"-"`
}

type Server struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

type OpenaiCompatibleConfig struct {
	BaseUrl string `toml:"base_url"`
	ApiKey  string `toml:"api_key"`
	// SessionAPIKey is intentionally never written to config.toml.  It lets
	// the desktop reuse the user's API Gateway credential for a cloud STT
	// request without creating another on-disk copy of that secret.
	SessionAPIKey string `toml:"-"`
	Model         string `toml:"model"`
}

// LlmConfig keeps the OpenAI-compatible gateway fields. SessionApiKey is
// deliberately memory-only so a key pasted into the KOVA desktop form cannot
// be serialized to config.toml.
type LlmConfig struct {
	Provider      string `toml:"provider"`
	BaseUrl       string `toml:"base_url"`
	ApiKey        string `toml:"api_key"`
	ApiKeyEnv     string `toml:"api_key_env"`
	Model         string `toml:"model"`
	SessionApiKey string `toml:"-"`
}

type LocalModelConfig struct {
	Model string `toml:"model"`
}

type AliyunSpeechConfig struct {
	AccessKeyId     string `toml:"access_key_id"`
	AccessKeySecret string `toml:"access_key_secret"`
	AppKey          string `toml:"app_key"`
}

type AliyunOssConfig struct {
	AccessKeyId     string `toml:"access_key_id"`
	AccessKeySecret string `toml:"access_key_secret"`
	Bucket          string `toml:"bucket"`
}

type AliyunTranscribeConfig struct {
	Oss    AliyunOssConfig    `toml:"oss"`
	Speech AliyunSpeechConfig `toml:"speech"`
}

type Transcribe struct {
	Provider              string                 `toml:"provider"`
	EnableGpuAcceleration bool                   `toml:"enable_gpu_acceleration"`
	Openai                OpenaiCompatibleConfig `toml:"openai"`
	Fasterwhisper         LocalModelConfig       `toml:"fasterwhisper"`
	Whisperkit            LocalModelConfig       `toml:"whisperkit"`
	Whispercpp            LocalModelConfig       `toml:"whispercpp"`
	Aliyun                AliyunTranscribeConfig `toml:"aliyun"`
}

type AliyunTtsConfig struct {
	Oss    AliyunOssConfig    `toml:"oss"`
	Speech AliyunSpeechConfig `toml:"speech"`
}

type OmniVoiceTtsConfig struct {
	BaseUrl string `toml:"base_url"`
	// SessionApiKey is issued by the current Colab notebook run. It must never
	// be written to config.toml: a trycloudflare URL is public, so the matching
	// bearer token is the boundary protecting the temporary voice worker.
	SessionApiKey         string  `toml:"-"`
	Language              string  `toml:"language"`
	ReferenceAudio        string  `toml:"reference_audio"`
	ReferenceText         string  `toml:"reference_text"`
	Instruct              string  `toml:"instruct"`
	Speed                 float64 `toml:"speed"`
	NumStep               int     `toml:"num_step"`
	RequestTimeoutSeconds int     `toml:"request_timeout_seconds"`
	RequireReference      bool    `toml:"require_reference_audio"`
	RemoteOnly            bool    `toml:"remote_only"`
	RequireCUDA           bool    `toml:"require_cuda"`
}

// GatewayTtsConfig is for an OpenAI-compatible audio gateway (for example
// 9Router). Endpoint may be a base URL or the full /v1/audio/speech URL.
// It deliberately has no reference-audio fields: use OmniVoice when cloning a
// fixed speaker from an audio sample is required.
type GatewayTtsConfig struct {
	Endpoint       string `toml:"endpoint"`
	ApiKey         string `toml:"api_key"`
	ApiKeyEnv      string `toml:"api_key_env"`
	SessionAPIKey  string `toml:"-"`
	Model          string `toml:"model"`
	ResponseFormat string `toml:"response_format"`
}

type Tts struct {
	Provider  string                 `toml:"provider"`
	Openai    OpenaiCompatibleConfig `toml:"openai"`
	Aliyun    AliyunTtsConfig        `toml:"aliyun"`
	Minimax   OpenaiCompatibleConfig `toml:"minimax"`
	Omnivoice OmniVoiceTtsConfig     `toml:"omnivoice"`
	Gateway   GatewayTtsConfig       `toml:"gateway"`
}

type Dubbing struct {
	MinSubtitleDuration float64 `toml:"min_subtitle_duration"`
	MaxChunkSize        int     `toml:"max_chunk_size"`
	GapTolerance        float64 `toml:"gap_tolerance"`
	SpeedMin            float64 `toml:"speed_min"`
	SpeedAccept         float64 `toml:"speed_accept"`
	SpeedMax            float64 `toml:"speed_max"`
	EnableTextRewrite   bool    `toml:"enable_text_rewrite"`
	RewriteMaxAttempts  int     `toml:"rewrite_max_attempts"`
	Estimator           string  `toml:"estimator"`
}

type Image struct {
	Provider string                 `toml:"provider"`
	Openai   OpenaiCompatibleConfig `toml:"openai"`
}

// CreatorConfig contains only local desktop tooling for Kova Auto-Builder.
// CapCut itself remains a user-installed editor; Kova writes an inspectable
// draft specification and invokes a user-selected external compiler only when
// the user enables it.
type CreatorConfig struct {
	FFprobePath        string `toml:"ffprobe_path"`
	NodePath           string `toml:"node_path"`
	CapCutCLIPath      string `toml:"capcut_cli_path"`
	CompilerBackend    string `toml:"compiler_backend"`
	PythonPath         string `toml:"python_path"`
	PyCapCutBridgePath string `toml:"pycapcut_bridge_path"`
	CapCutDraftRoot    string `toml:"capcut_draft_root"`
	DefaultOutputDir   string `toml:"default_output_dir"`
	CompileDraft       bool   `toml:"compile_capcut_draft"`
}

// VisualOCRConfig describes the optional offline PaddleOCR bridge. No model
// is bundled in the Go binary; the selected Python environment owns Paddle,
// PaddleOCR and its CUDA/CPU fallback.
type VisualOCRConfig struct {
	PythonPath       string `toml:"python_path"`
	ScriptPath       string `toml:"script_path"`
	PreferGPU        bool   `toml:"prefer_gpu"`
	SampleIntervalMS int    `toml:"sample_interval_ms"`
}

type OpenAiWhisper struct {
	BaseUrl string `toml:"base_url"`
	ApiKey  string `toml:"api_key"`
}

type Config struct {
	App        App             `toml:"app"`
	Server     Server          `toml:"server"`
	Llm        LlmConfig       `toml:"llm"`
	Transcribe Transcribe      `toml:"transcribe"`
	Tts        Tts             `toml:"tts"`
	Dubbing    Dubbing         `toml:"dubbing"`
	Image      Image           `toml:"image"`
	Creator    CreatorConfig   `toml:"creator"`
	VisualOCR  VisualOCRConfig `toml:"visual_ocr"`
}

var Conf = Config{
	App: App{
		SegmentDuration:       5,
		TranslateParallelNum:  3,
		TranscribeParallelNum: 1,
		TranscribeMaxAttempts: 3,
		TranslateMaxAttempts:  3,
		MaxSentenceLength:     70,
		EnableBlockVttBatch:   false,
		VttBatchSize:          10,
	},
	Server: Server{
		Host: "127.0.0.1",
		Port: 8888,
	},
	Llm: LlmConfig{
		Provider:  "openai-compatible",
		BaseUrl:   KOVAGatewayBaseURL,
		ApiKeyEnv: "KOVA_API_GATEWAY_API_KEY",
		Model:     "oc/deepseek-v4-flash-free",
	},
	Transcribe: Transcribe{
		Provider:              "fasterwhisper",
		EnableGpuAcceleration: false,
		Openai: OpenaiCompatibleConfig{
			Model: "whisper-1",
		},
		Fasterwhisper: LocalModelConfig{
			Model: "medium",
		},
		Whisperkit: LocalModelConfig{
			Model: "large-v2",
		},
		Whispercpp: LocalModelConfig{
			Model: "large-v2",
		},
	},
	Tts: Tts{
		Provider: "omnivoice",
		Openai: OpenaiCompatibleConfig{
			Model: "gpt-4o-mini-tts",
		},
		Omnivoice: OmniVoiceTtsConfig{
			// A blank endpoint intentionally prevents accidental local cloning.
			// The desktop requires a fresh, remote Colab worker URL per job.
			BaseUrl:               "",
			Language:              "vi",
			Speed:                 1.0,
			NumStep:               32,
			RequestTimeoutSeconds: 1800,
			RequireReference:      true,
			RemoteOnly:            true,
			RequireCUDA:           true,
		},
		Gateway: GatewayTtsConfig{
			Endpoint:  KOVAGatewayBaseURL,
			ApiKeyEnv: "KOVA_API_GATEWAY_API_KEY",
			Model:     "google-tts/vi",
		},
	},
	Dubbing: Dubbing{
		MinSubtitleDuration: 2.5,
		MaxChunkSize:        5,
		GapTolerance:        1.5,
		SpeedMin:            0.95,
		SpeedAccept:         1.08,
		SpeedMax:            1.12,
		EnableTextRewrite:   true,
		RewriteMaxAttempts:  2,
		Estimator:           "statistical",
	},
	Image: Image{
		Provider: "openai-compatible",
		Openai: OpenaiCompatibleConfig{
			Model: "gpt-image-1",
		},
	},
	Creator: CreatorConfig{
		FFprobePath:        "ffprobe",
		NodePath:           "node",
		CompilerBackend:    "pycapcut",
		PythonPath:         "python",
		PyCapCutBridgePath: "scripts/kova_pycapcut_builder.py",
		DefaultOutputDir:   "output",
		// Exporting an inspectable Kova spec is safe by default. A user must
		// explicitly enable compilation after setting the external CapCut tool.
		CompileDraft: false,
	},
	VisualOCR: VisualOCRConfig{
		PythonPath:       "python",
		ScriptPath:       "scripts/kova_visual_ocr.py",
		PreferGPU:        true,
		SampleIntervalMS: 250,
	},
}

// ValidateRemoteOmniVoiceWorker applies Kova's explicit policy for voice
// cloning: inference is remote-only, and a tunnel URL must be HTTPS and not
// loopback. The actual CUDA/ready check is performed by the desktop probe and
// again by the service before a job starts.
func ValidateRemoteOmniVoiceWorker() error {
	endpoint := strings.TrimSpace(Conf.Tts.Omnivoice.BaseUrl)
	if endpoint == "" {
		return errors.New("chưa dán URL worker Google Colab OmniVoice")
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("worker OmniVoice phải là URL HTTPS Google Colab/tunnel hợp lệ")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("không cho phép OmniVoice local; hãy dùng URL GPU Google Colab")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return errors.New("không cho phép OmniVoice local; hãy dùng URL GPU Google Colab")
	}
	return nil
}

// ConfigureRemoteColabTranscription selects a user-started Faster-Whisper
// worker running on Google Colab. The URL and bearer token are held only for
// the current desktop session; neither value is written to config.toml.
// The worker exposes /v1/audio/transcriptions while callers paste its tunnel
// root, exactly as printed by the KOVA notebook.
func ConfigureRemoteColabTranscription(rawURL, token, model string) error {
	endpoint, err := normalizeRemoteColabWorkerURL(rawURL)
	if err != nil {
		return fmt.Errorf("URL worker STT Google Colab không hợp lệ: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("chưa dán token STT do notebook Google Colab in ra")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "medium"
	}
	Conf.Transcribe.Provider = "openai"
	Conf.Transcribe.Openai.BaseUrl = endpoint + "/v1"
	Conf.Transcribe.Openai.SessionAPIKey = strings.TrimSpace(token)
	Conf.Transcribe.Openai.Model = model
	return nil
}

func normalizeRemoteColabWorkerURL(rawURL string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("worker phải là URL HTTPS công khai")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "", errors.New("không cho phép worker STT cục bộ; hãy dùng URL tunnel từ Google Colab")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return "", errors.New("không cho phép worker STT cục bộ; hãy dùng URL tunnel từ Google Colab")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateConfig() error {
	llmProvider := strings.ToLower(strings.TrimSpace(Conf.Llm.Provider))
	if llmProvider == "" {
		llmProvider = "openai"
	}
	switch llmProvider {
	case "openai", "openai-compatible":
		parsed, err := url.ParseRequestURI(Conf.Llm.BaseUrl)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("llm.base_url của API Gateway phải là URL http/https hợp lệ")
		}
		if IsKOVAGatewayURL(Conf.Llm.BaseUrl) && !IsGatewayFreeLLMModel(Conf.Llm.Model) {
			return fmt.Errorf("llm.model phải thuộc danh sách model free KOVA Gateway: %s", Conf.Llm.Model)
		}
		if ResolveLLMAPIKey() == "" {
			return errors.New("llm API key không được để trống")
		}
	case "ollama":
		if Conf.Llm.Model == "" {
			return errors.New("sử dụng Ollama cần cấu hình llm.model")
		}
		if Conf.Llm.BaseUrl != "" {
			parsed, err := url.ParseRequestURI(Conf.Llm.BaseUrl)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return errors.New("llm.base_url của Ollama phải là URL http/https hợp lệ")
			}
		}
	default:
		return fmt.Errorf("không hỗ trợ LLM provider %q", Conf.Llm.Provider)
	}

	ttsProvider := strings.ToLower(strings.TrimSpace(Conf.Tts.Provider))
	switch ttsProvider {
	case "openai", "aliyun", "edge-tts", "minimax", "omnivoice", "gateway":
	default:
		return fmt.Errorf("không hỗ trợ TTS provider %q", Conf.Tts.Provider)
	}
	if ttsProvider == "omnivoice" {
		// OmniVoice is reserved for the fixed-voice, remote Colab workflow.
		// Keep these safeguards in configuration validation as well as in the
		// desktop, so a stale client cannot silently re-enable local inference
		// or a synthetic voice.
		if !Conf.Tts.Omnivoice.RequireReference {
			return errors.New("tts.omnivoice.require_reference_audio must be true for Kova")
		}
		if !Conf.Tts.Omnivoice.RemoteOnly {
			return errors.New("tts.omnivoice.remote_only must be true for Kova")
		}
		if !Conf.Tts.Omnivoice.RequireCUDA {
			return errors.New("tts.omnivoice.require_cuda must be true for Kova")
		}
		if Conf.Tts.Omnivoice.BaseUrl != "" {
			parsed, err := url.ParseRequestURI(Conf.Tts.Omnivoice.BaseUrl)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return errors.New("tts.omnivoice.base_url phải là URL http/https hợp lệ")
			}
		}
		if strings.TrimSpace(Conf.Tts.Omnivoice.BaseUrl) != "" && strings.TrimSpace(Conf.Tts.Omnivoice.SessionApiKey) == "" {
			return errors.New("tts.omnivoice.session_api_key không được để trống; dán Session token do notebook Colab in ra")
		}
		if Conf.Tts.Omnivoice.RemoteOnly && strings.TrimSpace(Conf.Tts.Omnivoice.BaseUrl) != "" {
			if err := ValidateRemoteOmniVoiceWorker(); err != nil {
				return err
			}
		}
		if Conf.Tts.Omnivoice.Speed <= 0 {
			return errors.New("tts.omnivoice.speed phải lớn hơn 0")
		}
		if Conf.Tts.Omnivoice.NumStep <= 0 {
			return errors.New("tts.omnivoice.num_step phải lớn hơn 0")
		}
	}
	if ttsProvider == "gateway" {
		if ResolveGatewayTTSAPIKey() == "" {
			return errors.New("tts.gateway.api_key hoặc tts.gateway.api_key_env không được để trống")
		}
		if strings.TrimSpace(Conf.Tts.Gateway.Endpoint) == "" {
			return errors.New("tts.gateway.endpoint không được để trống")
		}
		parsed, err := url.ParseRequestURI(Conf.Tts.Gateway.Endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("tts.gateway.endpoint phải là URL http/https hợp lệ")
		}
		if strings.TrimSpace(Conf.Tts.Gateway.Model) == "" {
			return errors.New("tts.gateway.model không được để trống")
		}
	}

	switch Conf.Transcribe.Provider {
	case "openai":
		if ResolveTranscriptionAPIKey() == "" {
			return errors.New("OpenAI transcription cần API key / requires an API key")
		}
	case "fasterwhisper":
		if Conf.Transcribe.Fasterwhisper.Model != "tiny" && Conf.Transcribe.Fasterwhisper.Model != "medium" && Conf.Transcribe.Fasterwhisper.Model != "large-v2" {
			return errors.New("model FasterWhisper không hợp lệ / invalid FasterWhisper model")
		}
	case "whisperkit":
		if runtime.GOOS != "darwin" {
			log.GetLogger().Error("WhisperKit only supports macOS", zap.String("current_os", runtime.GOOS))
			return fmt.Errorf("WhisperKit chỉ hỗ trợ macOS / only supports macOS")
		}
		if Conf.Transcribe.Whisperkit.Model != "large-v2" {
			return errors.New("model WhisperKit không hợp lệ / invalid WhisperKit model")
		}
	case "whispercpp":
		if runtime.GOOS != "windows" {
			log.GetLogger().Error("whispercpp only support windows", zap.String("current os", runtime.GOOS))
			return fmt.Errorf("whispercpp only support windows")
		}
		if Conf.Transcribe.Whispercpp.Model != "large-v2" {
			return errors.New("model Whisper.cpp không hợp lệ / invalid Whisper.cpp model")
		}
	case "aliyun":
		if Conf.Transcribe.Aliyun.Speech.AccessKeyId == "" || Conf.Transcribe.Aliyun.Speech.AccessKeySecret == "" || Conf.Transcribe.Aliyun.Speech.AppKey == "" {
			return errors.New("Aliyun Speech cần đủ thông tin xác thực / requires credentials")
		}
	default:
		return errors.New("nhà cung cấp nhận dạng không được hỗ trợ / unsupported transcription provider")
	}

	return nil
}

func LoadConfig() bool {
	var err error
	configPath := "./config/config.toml"
	if _, err = os.Stat(configPath); os.IsNotExist(err) {
		log.GetLogger().Info("Kova configuration file not found")
		return false
	} else {
		log.GetLogger().Info("Loading Kova configuration file")
		if _, err = toml.DecodeFile(configPath, &Conf); err != nil {
			log.GetLogger().Error("Failed to load Kova configuration file", zap.Error(err))
			return false
		}
		return true
	}
}

func CheckConfig() error {
	var err error
	Conf.App.ParsedProxy, err = url.Parse(Conf.App.Proxy)
	if err != nil {
		return err
	}
	return validateConfig()
}

func SaveConfig() error {
	configPath := filepath.Join("config", "config.toml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		err = os.MkdirAll(filepath.Dir(configPath), os.ModePerm)
		if err != nil {
			return err
		}
	}

	// Keep a temporary session key in memory only. ApiKey is retained for the
	// ignored local KOVA Gateway config, while an old legacy Ollama key from a
	// previous version is stripped during the next save as an additional safeguard.
	configForDisk := Conf
	if strings.EqualFold(strings.TrimSpace(configForDisk.Llm.Provider), "ollama") {
		configForDisk.Llm.ApiKey = ""
	}
	data, err := toml.Marshal(configForDisk)
	if err != nil {
		return err
	}

	err = os.WriteFile(configPath, data, 0644)
	if err != nil {
		return err
	}

	return nil
}
