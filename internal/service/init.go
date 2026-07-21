package service

import (
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

	switch config.Conf.Tts.Provider {
	case "openai":
		ttsClient = openai.NewClient(config.Conf.Tts.Openai.BaseUrl, config.Conf.Tts.Openai.ApiKey, config.Conf.App.Proxy)
	case "aliyun":
		ttsClient = aliyun.NewTtsClient(config.Conf.Tts.Aliyun.Speech.AccessKeyId, config.Conf.Tts.Aliyun.Speech.AccessKeySecret, config.Conf.Tts.Aliyun.Speech.AppKey)
	case "edge-tts":
		ttsClient = localtts.NewEdgeTtsClient()
	case "minimax":
		ttsClient = minimax.NewTtsClient(config.Conf.Tts.Minimax.BaseUrl, config.Conf.Tts.Minimax.ApiKey, config.Conf.Tts.Minimax.Model)
	case "omnivoice":
		ttsClient = omnivoice.NewClient(omnivoice.Config{
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
		ttsClient = gatewaytts.NewClient(
			config.Conf.Tts.Gateway.Endpoint,
			config.ResolveGatewayTTSAPIKey(),
			config.Conf.Tts.Gateway.Model,
			config.Conf.Tts.Gateway.ResponseFormat,
		)
	}

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
