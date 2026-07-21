package service

import (
	"fmt"
	"kova/config"
	"kova/internal/types"
	"kova/log"
	"kova/pkg/aliyun"
	"kova/pkg/fasterwhisper"
	"kova/pkg/gatewaytts"
	pkgimage "kova/pkg/image"
	"kova/pkg/localtts"
	"kova/pkg/minimax"
	"kova/pkg/omnivoice"
	"kova/pkg/openai"
	"kova/pkg/whisper"
	"kova/pkg/whispercpp"
	"kova/pkg/whisperkit"
	"strings"

	"go.uber.org/zap"
)

type Service struct {
	Transcriber        types.Transcriber
	ChatCompleter      types.ChatCompleter
	TtsClient          types.Ttser
	OssClient          *aliyun.OssClient
	VoiceCloneClient   *aliyun.VoiceCloneClient
	YouTubeSubtitleSrv *YouTubeSubtitleService
	ImageClient        *pkgimage.OpenAICompatibleClient
}

func NewService() *Service {
	var transcriber types.Transcriber
	var chatCompleter types.ChatCompleter
	var ttsClient types.Ttser

	switch config.Conf.Transcribe.Provider {
	case "openai":
		transcriber = whisper.NewClientWithModel(config.Conf.Transcribe.Openai.BaseUrl, config.ResolveTranscriptionAPIKey(), config.Conf.App.Proxy, config.Conf.Transcribe.Openai.Model)
	case "fasterwhisper":
		transcriber = fasterwhisper.NewFastwhisperProcessor(config.Conf.Transcribe.Fasterwhisper.Model)
	case "whispercpp":
		transcriber = whispercpp.NewWhispercppProcessor(config.Conf.Transcribe.Whispercpp.Model)
	case "whisperkit":
		transcriber = whisperkit.NewWhisperKitProcessor(config.Conf.Transcribe.Whisperkit.Model)
	case "aliyun":
		cc, err := aliyun.NewAsrClient(config.Conf.Transcribe.Aliyun.Speech.AccessKeyId, config.Conf.Transcribe.Aliyun.Speech.AccessKeySecret, config.Conf.Transcribe.Aliyun.Speech.AppKey, true)
		if err != nil {
			log.GetLogger().Error("创建阿里云语音识别客户端失败： ", zap.Error(err))
			return nil
		}
		transcriber = cc
	}
	log.GetLogger().Info("当前选择的转录源： ", zap.String("transcriber", config.Conf.Transcribe.Provider))

	chatCompleter = newChatCompleter()

	ttsClient = newTTSClient()

	s := &Service{
		Transcriber:      transcriber,
		ChatCompleter:    chatCompleter,
		TtsClient:        ttsClient,
		OssClient:        aliyun.NewOssClient(config.Conf.Transcribe.Aliyun.Oss.AccessKeyId, config.Conf.Transcribe.Aliyun.Oss.AccessKeySecret, config.Conf.Transcribe.Aliyun.Oss.Bucket),
		VoiceCloneClient: aliyun.NewVoiceCloneClient(config.Conf.Tts.Aliyun.Speech.AccessKeyId, config.Conf.Tts.Aliyun.Speech.AccessKeySecret, config.Conf.Tts.Aliyun.Speech.AppKey),
		ImageClient:      pkgimage.NewOpenAICompatibleClient(config.Conf.Image.Openai.BaseUrl, config.Conf.Image.Openai.ApiKey, config.Conf.Image.Openai.Model),
	}
	s.YouTubeSubtitleSrv = NewYouTubeSubtitleService()

	return s
}

// newTTSClient creates the client for the provider that is selected now.
// The desktop may switch its dropdown after the local API server started, so
// this must not be called only during application boot.
func newTTSClient() types.Ttser {
	switch strings.ToLower(strings.TrimSpace(config.Conf.Tts.Provider)) {
	case "openai":
		return openai.NewClient(config.Conf.Tts.Openai.BaseUrl, config.Conf.Tts.Openai.ApiKey, config.Conf.App.Proxy)
	case "aliyun":
		return aliyun.NewTtsClient(config.Conf.Tts.Aliyun.Speech.AccessKeyId, config.Conf.Tts.Aliyun.Speech.AccessKeySecret, config.Conf.Tts.Aliyun.Speech.AppKey)
	case "edge-tts":
		return localtts.NewEdgeTtsClient()
	case "minimax":
		return minimax.NewTtsClient(config.Conf.Tts.Minimax.BaseUrl, config.Conf.Tts.Minimax.ApiKey, config.Conf.Tts.Minimax.Model)
	case "omnivoice":
		return omnivoice.NewClient(omnivoice.Config{
			BaseURL:  config.Conf.Tts.Omnivoice.BaseUrl,
			APIKey:   config.Conf.Tts.Omnivoice.SessionApiKey,
			Language: config.Conf.Tts.Omnivoice.Language,
			// The reference clip is intentionally job-scoped in srtFileToSpeech.
			// Never reuse the config value implicitly.
			ReferenceAudio:   "",
			ReferenceText:    config.Conf.Tts.Omnivoice.ReferenceText,
			Instruct:         config.Conf.Tts.Omnivoice.Instruct,
			Speed:            config.Conf.Tts.Omnivoice.Speed,
			NumStep:          config.Conf.Tts.Omnivoice.NumStep,
			TimeoutSeconds:   config.Conf.Tts.Omnivoice.RequestTimeoutSeconds,
			RequireReference: true,
			ConsentConfirmed: false,
		})
	case "gateway":
		return gatewaytts.NewClient(
			config.Conf.Tts.Gateway.Endpoint,
			config.ResolveGatewayTTSAPIKey(),
			config.Conf.Tts.Gateway.Model,
			config.Conf.Tts.Gateway.ResponseFormat,
		)
	}
	return nil
}

// RefreshTranscriptionClient rebuilds only the STT client immediately before
// the explicit source stage.  The desktop switches to the user's cloud API
// Gateway after the local API process is already running, so retaining the
// startup client would otherwise incorrectly use a local ASR implementation.
func (s *Service) RefreshTranscriptionClient() {
	if s == nil {
		return
	}
	switch config.Conf.Transcribe.Provider {
	case "openai":
		s.Transcriber = whisper.NewClientWithModel(config.Conf.Transcribe.Openai.BaseUrl, config.ResolveTranscriptionAPIKey(), config.Conf.App.Proxy, config.Conf.Transcribe.Openai.Model)
	case "fasterwhisper":
		s.Transcriber = fasterwhisper.NewFastwhisperProcessor(config.Conf.Transcribe.Fasterwhisper.Model)
	case "whispercpp":
		s.Transcriber = whispercpp.NewWhispercppProcessor(config.Conf.Transcribe.Whispercpp.Model)
	case "whisperkit":
		s.Transcriber = whisperkit.NewWhisperKitProcessor(config.Conf.Transcribe.Whisperkit.Model)
	case "aliyun":
		cc, err := aliyun.NewAsrClient(config.Conf.Transcribe.Aliyun.Speech.AccessKeyId, config.Conf.Transcribe.Aliyun.Speech.AccessKeySecret, config.Conf.Transcribe.Aliyun.Speech.AppKey, true)
		if err != nil {
			log.GetLogger().Error("could not refresh Aliyun ASR client", zap.Error(err))
			s.Transcriber = nil
			return
		}
		s.Transcriber = cc
	default:
		s.Transcriber = nil
	}
}

// RefreshTranslationClients rebuilds only the clients captured by the subtitle
// translator. The desktop can accept a KOVA Gateway key for the current
// session after the HTTP server has started; keeping the old translator would
// otherwise make the next explicit translation step read only the old
// environment configuration. Source review is complete before this method is
// called, so replacing this stage-specific client cannot interrupt a running
// source download.
func (s *Service) RefreshTranslationClients() {
	if s == nil {
		return
	}
	s.ChatCompleter = newChatCompleter()
	s.YouTubeSubtitleSrv = NewYouTubeSubtitleService()
}

// RefreshTTSClient rebuilds the provider captured by a running local API.
// In particular, selecting Google/Edge Gateway TTS must replace a stale
// OmniVoice client before a single cue is synthesized.  This is deliberately
// separate from the translator refresh: changing a TTS dropdown never
// restarts download, STT, or translation work.
func (s *Service) RefreshTTSClient() {
	if s == nil {
		return
	}
	s.TtsClient = newTTSClient()
}

// ValidateTTSPreflight fails synchronously before a dubbing worker is
// started. It catches an outdated in-memory provider and missing Gateway
// settings without wasting time preparing or synthesizing subtitles.
func (s *Service) ValidateTTSPreflight() error {
	if s == nil {
		return fmt.Errorf("KOVA TTS service is unavailable")
	}
	provider := strings.ToLower(strings.TrimSpace(config.Conf.Tts.Provider))
	switch provider {
	case "gateway":
		if strings.TrimSpace(config.Conf.Tts.Gateway.Endpoint) == "" {
			return fmt.Errorf("Google/Edge Gateway TTS thiếu endpoint")
		}
		if strings.TrimSpace(config.ResolveGatewayTTSAPIKey()) == "" {
			return fmt.Errorf("Google/Edge Gateway TTS thiếu API key; nhập key Gateway trong Cài đặt hoặc dùng key phiên hiện tại")
		}
		if strings.TrimSpace(config.Conf.Tts.Gateway.Model) == "" {
			return fmt.Errorf("Google/Edge Gateway TTS thiếu model")
		}
		if _, ok := s.TtsClient.(*gatewaytts.Client); !ok {
			return fmt.Errorf("KOVA chưa áp dụng Google/Edge Gateway TTS cho worker; hãy chạy lại bước lồng tiếng")
		}
	case "omnivoice":
		if _, ok := s.TtsClient.(*omnivoice.Client); !ok {
			return fmt.Errorf("KOVA chưa áp dụng OmniVoice cho worker; hãy chạy lại bước lồng tiếng")
		}
	case "openai", "aliyun", "edge-tts", "minimax":
		if s.TtsClient == nil {
			return fmt.Errorf("KOVA không thể khởi tạo TTS provider %q", provider)
		}
	default:
		return fmt.Errorf("KOVA không hỗ trợ TTS provider %q", provider)
	}
	return nil
}
