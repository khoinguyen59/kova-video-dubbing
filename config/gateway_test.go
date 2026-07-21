package config

import (
	"strings"
	"testing"
)

func TestKOVAGatewayFreeModelCatalogIsFixedAndRecognized(t *testing.T) {
	models := GatewayFreeLLMModels()
	if len(models) != 6 {
		t.Fatalf("GatewayFreeLLMModels() count = %d, want 6", len(models))
	}
	for _, model := range models {
		if !strings.HasPrefix(model.ID, "oc/") || !IsGatewayFreeLLMModel(model.ID) {
			t.Fatalf("free model = %+v, want approved oc/ model", model)
		}
	}
	if IsGatewayFreeLLMModel("deepseek/deepseek-v4-pro") {
		t.Fatal("paid/non-free gateway model must not pass the temporary allowlist")
	}
}

func TestConfigureKOVAGatewayTranslationRejectsNonFreeModelAndKeepsSecretLocal(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	Conf = validTestConfig()
	Conf.Llm.ApiKey = "test-only-key"

	if err := ConfigureKOVAGatewayTranslation("oc/deepseek-v4-flash-free"); err != nil {
		t.Fatalf("ConfigureKOVAGatewayTranslation() error = %v", err)
	}
	if Conf.Llm.Provider != "openai-compatible" || Conf.Llm.BaseUrl != KOVAGatewayBaseURL {
		t.Fatalf("gateway config = %+v", Conf.Llm)
	}
	if err := ConfigureKOVAGatewayTranslation("deepseek/deepseek-v4-pro"); err == nil {
		t.Fatal("expected a non-free model to be rejected")
	}
}

func TestResolveGatewayKeysSupportsEnvironmentWithoutLeakingItToConfig(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	t.Setenv("KOVA_TEST_GATEWAY_KEY", "test-only-env-key")
	Conf = validTestConfig()
	Conf.Llm.ApiKey = ""
	Conf.Llm.ApiKeyEnv = "KOVA_TEST_GATEWAY_KEY"
	Conf.Tts.Gateway.ApiKey = ""
	Conf.Tts.Gateway.ApiKeyEnv = "KOVA_TEST_GATEWAY_KEY"
	if ResolveLLMAPIKey() != "test-only-env-key" || ResolveGatewayTTSAPIKey() != "test-only-env-key" {
		t.Fatal("gateway key resolver did not read the configured environment variable")
	}
}

func TestConfigureKOVAGatewayTranscriptionUsesSessionKeyOnly(t *testing.T) {
	original := Conf
	t.Cleanup(func() { Conf = original })
	Conf = validTestConfig()
	Conf.Llm.Provider = "openai-compatible"
	Conf.Llm.BaseUrl = KOVAGatewayBaseURL
	Conf.Llm.ApiKey = "test-only-key"

	if err := ConfigureKOVAGatewayTranscription(); err != nil {
		t.Fatalf("ConfigureKOVAGatewayTranscription() error = %v", err)
	}
	if Conf.Transcribe.Provider != "openai" || Conf.Transcribe.Openai.BaseUrl != KOVAGatewayBaseURL {
		t.Fatalf("transcription gateway config = %+v", Conf.Transcribe)
	}
	if Conf.Transcribe.Openai.SessionAPIKey != "test-only-key" || Conf.Transcribe.Openai.ApiKey != "" {
		t.Fatalf("transcription key was not session-only: %+v", Conf.Transcribe.Openai)
	}
}
