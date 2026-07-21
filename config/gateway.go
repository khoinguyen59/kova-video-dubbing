package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// KOVAGatewayBaseURL is the user-managed 9router-compatible gateway selected
// for the current KOVA deployment. It deliberately remains HTTP because that
// is the supplied endpoint; deploy it behind HTTPS before exposing it outside
// a trusted network.
const KOVAGatewayBaseURL = "http://3.27.172.90/v1"

// GatewayFreeLLMModel is a fixed, reviewed option exposed by KOVA while this
// gateway is in free-model-only mode. Do not accept arbitrary model IDs for
// this configured endpoint: a typo could silently select a paid model.
type GatewayFreeLLMModel struct {
	ID      string
	LabelVI string
	LabelEN string
}

var gatewayFreeLLMModels = []GatewayFreeLLMModel{
	{ID: "oc/deepseek-v4-flash-free", LabelVI: "DeepSeek V4 Flash Free", LabelEN: "DeepSeek V4 Flash Free"},
	{ID: "oc/mimo-v2.5-free", LabelVI: "MiMo V2.5 Free", LabelEN: "MiMo V2.5 Free"},
	{ID: "oc/hy3-free", LabelVI: "HY3 Free", LabelEN: "HY3 Free"},
	{ID: "oc/big-pickle", LabelVI: "Big Pickle", LabelEN: "Big Pickle"},
	{ID: "oc/nemotron-3-ultra-free", LabelVI: "Nemotron 3 Ultra Free", LabelEN: "Nemotron 3 Ultra Free"},
	{ID: "oc/north-mini-code-free", LabelVI: "North Mini Code Free", LabelEN: "North Mini Code Free"},
}

func GatewayFreeLLMModels() []GatewayFreeLLMModel {
	return append([]GatewayFreeLLMModel(nil), gatewayFreeLLMModels...)
}

func IsGatewayFreeLLMModel(model string) bool {
	model = strings.TrimSpace(model)
	for _, candidate := range gatewayFreeLLMModels {
		if candidate.ID == model {
			return true
		}
	}
	return false
}

func IsKOVAGatewayURL(rawURL string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") && parsed.Hostname() == "3.27.172.90" && strings.TrimRight(parsed.Path, "/") == "/v1"
}

// ResolveLLMAPIKey gives session input priority, then the local ignored
// config, then the configured environment variable. It never logs a value.
func ResolveLLMAPIKey() string {
	if key := strings.TrimSpace(Conf.Llm.SessionApiKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(Conf.Llm.ApiKey); key != "" {
		return key
	}
	if envName := strings.TrimSpace(Conf.Llm.ApiKeyEnv); envName != "" {
		return strings.TrimSpace(os.Getenv(envName))
	}
	return ""
}

// ResolveGatewayTTSAPIKey mirrors the LLM secret precedence for the dedicated
// TTS gateway. ApiKeyEnv is intentionally persisted only as a variable name.
func ResolveGatewayTTSAPIKey() string {
	if key := strings.TrimSpace(Conf.Tts.Gateway.SessionAPIKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(Conf.Tts.Gateway.ApiKey); key != "" {
		return key
	}
	if envName := strings.TrimSpace(Conf.Tts.Gateway.ApiKeyEnv); envName != "" {
		return strings.TrimSpace(os.Getenv(envName))
	}
	return ""
}

// ResolveTranscriptionAPIKey keeps the speech-to-text credential scoped to
// the current application session when KOVA is configured through its API
// Gateway.  A dedicated transcription key is still supported for deployments
// that use a different OpenAI-compatible audio provider.
func ResolveTranscriptionAPIKey() string {
	if key := strings.TrimSpace(Conf.Transcribe.Openai.SessionAPIKey); key != "" {
		return key
	}
	return strings.TrimSpace(Conf.Transcribe.Openai.ApiKey)
}

// ConfigureKOVAGatewayTranscription selects cloud speech-to-text for the
// source stage.  It deliberately uses the existing session/environment-backed
// gateway key and does not download a Whisper model or require a local GPU.
// The gateway must expose the OpenAI-compatible /v1/audio/transcriptions API.
func ConfigureKOVAGatewayTranscription() error {
	key := ResolveLLMAPIKey()
	if key == "" {
		return errors.New("chưa cấu hình API key cho KOVA API Gateway dùng speech-to-text")
	}
	Conf.Transcribe.Provider = "openai"
	Conf.Transcribe.Openai.BaseUrl = KOVAGatewayBaseURL
	Conf.Transcribe.Openai.SessionAPIKey = key
	if strings.TrimSpace(Conf.Transcribe.Openai.Model) == "" {
		Conf.Transcribe.Openai.Model = "whisper-1"
	}
	return nil
}

// ConfigureKOVAGatewayTranslation is the only path used by the Wails model
// dropdown. It does not persist a key and it refuses non-free model IDs.
func ConfigureKOVAGatewayTranslation(model string) error {
	if !IsGatewayFreeLLMModel(model) {
		return fmt.Errorf("model gateway không nằm trong danh sách free được phép: %s", strings.TrimSpace(model))
	}
	Conf.Llm.Provider = "openai-compatible"
	Conf.Llm.BaseUrl = KOVAGatewayBaseURL
	Conf.Llm.Model = model
	if ResolveLLMAPIKey() == "" {
		return errors.New("chưa cấu hình API key cho KOVA API Gateway")
	}
	return nil
}
