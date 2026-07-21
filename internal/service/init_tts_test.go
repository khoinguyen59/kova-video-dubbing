package service

import (
	"kova/config"
	"kova/pkg/gatewaytts"
	"kova/pkg/omnivoice"
	"testing"
)

func TestRefreshTTSClientReplacesStaleOmniVoiceWithGatewayBeforeDubbing(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })

	config.Conf.Tts.Provider = "gateway"
	config.Conf.Tts.Gateway.Endpoint = "https://gateway.example/v1/audio/speech"
	config.Conf.Tts.Gateway.ApiKey = "test-gateway-key"
	config.Conf.Tts.Gateway.ApiKeyEnv = ""
	config.Conf.Tts.Gateway.SessionAPIKey = ""
	config.Conf.Tts.Gateway.Model = "google-tts/vi"

	svc := &Service{TtsClient: omnivoice.NewClient(omnivoice.Config{})}
	svc.RefreshTTSClient()
	if _, ok := svc.TtsClient.(*gatewaytts.Client); !ok {
		t.Fatalf("TTS client = %T, want *gatewaytts.Client after Google Gateway selection", svc.TtsClient)
	}
	if err := svc.ValidateTTSPreflight(); err != nil {
		t.Fatalf("ValidateTTSPreflight() error = %v", err)
	}
}
