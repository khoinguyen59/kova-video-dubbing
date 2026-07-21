package config

import (
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func validTestConfig() Config {
	return Config{
		Llm: LlmConfig{
			Provider:  "ollama",
			BaseUrl:   "http://localhost:11434",
			ApiKeyEnv: "OLLAMA_API_KEY",
			Model:     "deepseek-v4-flash:cloud",
		},
		Transcribe: Transcribe{
			Provider: "fasterwhisper",
			Fasterwhisper: LocalModelConfig{
				Model: "medium",
			},
		},
		Tts: Tts{
			Provider: "omnivoice",
			Omnivoice: OmniVoiceTtsConfig{
				BaseUrl:          "https://kova-colab-worker.example",
				SessionApiKey:    "test-session-worker-token",
				Language:         "vi",
				Speed:            1,
				NumStep:          32,
				RequireReference: true,
				RemoteOnly:       true,
				RequireCUDA:      true,
			},
		},
	}
}

func TestOmniVoiceSessionTokenIsNeverSerialized(t *testing.T) {
	config := validTestConfig()
	config.Tts.Omnivoice.SessionApiKey = "do-not-write-this-session-token"

	data, err := toml.Marshal(config)
	if err != nil {
		t.Fatalf("toml.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), config.Tts.Omnivoice.SessionApiKey) || strings.Contains(string(data), "session_api_key") {
		t.Fatalf("OmniVoice session token leaked into TOML: %s", data)
	}
}

func TestValidateConfigAcceptsOllamaWithoutPersistedAPIKey(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	Conf = validTestConfig()

	if err := validateConfig(); err != nil {
		t.Fatalf("validateConfig() error = %v", err)
	}
	if Conf.Llm.ApiKey != "" {
		t.Fatal("test setup unexpectedly persisted an Ollama key")
	}
}

func TestValidateConfigRejectsInvalidNativeProviderSettings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "unknown provider", edit: func(c *Config) { c.Llm.Provider = "unknown" }, want: "LLM provider"},
		{name: "missing Ollama model", edit: func(c *Config) { c.Llm.Model = "" }, want: "llm.model"},
		{name: "invalid Ollama URL", edit: func(c *Config) { c.Llm.BaseUrl = "://bad" }, want: "llm.base_url"},
		{name: "invalid OmniVoice URL", edit: func(c *Config) { c.Tts.Omnivoice.BaseUrl = "file:///worker" }, want: "tts.omnivoice.base_url"},
		{name: "missing OmniVoice session token", edit: func(c *Config) { c.Tts.Omnivoice.SessionApiKey = "" }, want: "tts.omnivoice.session_api_key"},
		{name: "OmniVoice local mode disabled", edit: func(c *Config) { c.Tts.Omnivoice.RemoteOnly = false }, want: "tts.omnivoice.remote_only"},
		{name: "OmniVoice CPU mode disabled", edit: func(c *Config) { c.Tts.Omnivoice.RequireCUDA = false }, want: "tts.omnivoice.require_cuda"},
		{name: "OmniVoice reference is required", edit: func(c *Config) { c.Tts.Omnivoice.RequireReference = false }, want: "tts.omnivoice.require_reference_audio"},
		{name: "OmniVoice rejects local worker", edit: func(c *Config) { c.Tts.Omnivoice.BaseUrl = "https://127.0.0.1:11435" }, want: "không cho phép OmniVoice local"},
		{name: "invalid OmniVoice speed", edit: func(c *Config) { c.Tts.Omnivoice.Speed = 0 }, want: "tts.omnivoice.speed"},
		{name: "invalid OmniVoice steps", edit: func(c *Config) { c.Tts.Omnivoice.NumStep = 0 }, want: "tts.omnivoice.num_step"},
		{name: "gateway missing key", edit: func(c *Config) {
			c.Tts.Provider = "gateway"
			c.Tts.Gateway.Endpoint = "https://gateway.example/v1/audio/speech"
			c.Tts.Gateway.Model = "edge-tts"
		}, want: "tts.gateway.api_key"},
		{name: "gateway invalid endpoint", edit: func(c *Config) {
			c.Tts.Provider = "gateway"
			c.Tts.Gateway.ApiKey = "key"
			c.Tts.Gateway.Endpoint = "file:///gateway"
			c.Tts.Gateway.Model = "edge-tts"
		}, want: "tts.gateway.endpoint"},
		{name: "gateway missing model", edit: func(c *Config) {
			c.Tts.Provider = "gateway"
			c.Tts.Gateway.ApiKey = "key"
			c.Tts.Gateway.Endpoint = "https://gateway.example/v1/audio/speech"
		}, want: "tts.gateway.model"},
		{name: "unknown TTS provider", edit: func(c *Config) { c.Tts.Provider = "unknown" }, want: "TTS provider"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := Conf
			t.Cleanup(func() { Conf = original })
			Conf = validTestConfig()
			tt.edit(&Conf)
			err := validateConfig()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRemoteOmniVoiceWorkerAcceptsOnlyHTTPSRemoteURLs(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })

	tests := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{name: "HTTPS tunnel", endpoint: "https://worker.trycloudflare.com", wantErr: false},
		{name: "blank", endpoint: "", wantErr: true},
		{name: "HTTP", endpoint: "http://worker.example", wantErr: true},
		{name: "localhost", endpoint: "https://localhost:11435", wantErr: true},
		{name: "loopback", endpoint: "https://127.0.0.1:11435", wantErr: true},
		{name: "alternate loopback", endpoint: "https://127.0.0.2:11435", wantErr: true},
		{name: "local domain", endpoint: "https://omnivoice.local", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Conf = validTestConfig()
			Conf.Tts.Omnivoice.BaseUrl = tt.endpoint
			err := ValidateRemoteOmniVoiceWorker()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRemoteOmniVoiceWorker() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigureRemoteColabTranscriptionKeepsTokenSessionOnly(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	Conf = validTestConfig()

	if err := ConfigureRemoteColabTranscription("https://worker.trycloudflare.com/", "stt-session-token", "medium"); err != nil {
		t.Fatalf("ConfigureRemoteColabTranscription() error = %v", err)
	}
	if Conf.Transcribe.Provider != "openai" || Conf.Transcribe.Openai.BaseUrl != "https://worker.trycloudflare.com/v1" || Conf.Transcribe.Openai.Model != "medium" {
		t.Fatalf("remote STT config = %+v", Conf.Transcribe)
	}
	if Conf.Transcribe.Openai.SessionAPIKey != "stt-session-token" {
		t.Fatal("remote STT token was not retained in session memory")
	}
	data, err := toml.Marshal(Conf)
	if err != nil {
		t.Fatalf("toml.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "stt-session-token") || strings.Contains(string(data), "session_api_key") {
		t.Fatalf("STT token leaked into TOML: %s", data)
	}
}

func TestConfigureRemoteColabTranscriptionRejectsLocalAndBlankToken(t *testing.T) {
	for _, tc := range []struct {
		url   string
		token string
	}{
		{url: "https://localhost:3940", token: "token"},
		{url: "http://worker.example", token: "token"},
		{url: "https://worker.example", token: ""},
	} {
		if err := ConfigureRemoteColabTranscription(tc.url, tc.token, "medium"); err == nil {
			t.Fatalf("ConfigureRemoteColabTranscription(%q, %q) accepted invalid remote worker", tc.url, tc.token)
		}
	}
}

func TestValidateConfigAcceptsGatewayTTSAlongsideOmniVoice(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	Conf = validTestConfig()
	Conf.Tts.Provider = "gateway"
	Conf.Tts.Gateway = GatewayTtsConfig{
		Endpoint:       "https://gateway.example/v1/audio/speech",
		ApiKey:         "test-gateway-key",
		Model:          "google-tts/en",
		ResponseFormat: "wav",
	}

	if err := validateConfig(); err != nil {
		t.Fatalf("validateConfig() error = %v", err)
	}
	if Conf.Tts.Omnivoice.BaseUrl == "" {
		t.Fatal("gateway selection should not erase the independent OmniVoice configuration")
	}
}

func TestSaveConfigDoesNotPersistOllamaSessionKey(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	t.Chdir(t.TempDir())

	Conf = validTestConfig()
	Conf.Llm.SessionApiKey = "session-key-must-stay-in-memory"
	// This simulates a legacy configuration written by an earlier build. Kova
	// must scrub it when the active provider is Ollama as well.
	Conf.Llm.ApiKey = "legacy-ollama-key-must-not-be-written"
	if err := SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	data, err := os.ReadFile("config/config.toml")
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(data)
	if strings.Contains(text, Conf.Llm.SessionApiKey) || strings.Contains(text, "legacy-ollama-key-must-not-be-written") {
		t.Fatalf("SaveConfig() persisted an Ollama credential: %q", text)
	}
	if Conf.Llm.SessionApiKey != "session-key-must-stay-in-memory" {
		t.Fatal("SaveConfig() should not erase the in-memory session key")
	}
}
