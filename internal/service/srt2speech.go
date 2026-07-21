package service

import (
	"context"
	"fmt"
	"kova/config"
	"kova/internal/service/dubbing"
	"kova/internal/types"
	"kova/pkg/omnivoice"
	"path/filepath"
	"strings"
)

func targetSRTPathForDubbing(taskBasePath string) string {
	return filepath.Join(taskBasePath, types.SubtitleTaskTargetLanguageSrtFileName)
}

type voiceCloneFunc func(prefix, audioURL string) (string, error)

func resolveDubbingVoiceCode(baseVoice, cloneURL string, clone voiceCloneFunc) (string, error) {
	if cloneURL == "" {
		return baseVoice, nil
	}
	if clone == nil {
		return "", fmt.Errorf("srtFileToSpeech CosyVoiceClone error: voice clone client is nil")
	}
	code, err := clone("kova", cloneURL)
	if err != nil {
		return "", fmt.Errorf("srtFileToSpeech CosyVoiceClone error: %w", err)
	}
	return code, nil
}

// resolveGatewayVoiceCode keeps gateway synthesis independent of the Aliyun
// clone API. Gateways such as 9Router expose preset/model voices, not a
// reference-audio clone contract; users who need cloning select OmniVoice.
func resolveGatewayVoiceCode(baseVoice, cloneURL string) (string, error) {
	if strings.TrimSpace(cloneURL) != "" {
		return "", fmt.Errorf("API Gateway TTS không hỗ trợ audio clone; chọn provider omnivoice để clone giọng")
	}
	return baseVoice, nil
}

// 输入目标语言字幕，生成配音
// srtFileToSpeech preserves the historical one-call behavior for the legacy
// endpoint. The native staged Kova workflow uses synthesizeSRTToSpeech and
// muxDubbedAudioVideo separately, with an explicit user approval in between.
func (s Service) srtFileToSpeech(ctx context.Context, stepParam *types.SubtitleTaskStepParam) error {
	runner, err := s.newDubbingRunner(stepParam)
	if err != nil {
		return err
	}
	if runner == nil {
		return nil
	}
	result, err := runner.Run(ctx)
	if err != nil {
		return fmt.Errorf("srtFileToSpeech dubbing runner error: %w", err)
	}
	stepParam.TtsResultFilePath = result.Audio
	stepParam.VideoWithTtsFilePath = result.Video
	if stepParam.TaskPtr != nil {
		stepParam.TaskPtr.ProcessPct = 98
	}
	return nil
}

// synthesizeSRTToSpeech generates reviewable audio only. It deliberately
// does not mux the source video or make a later rendering decision.
func (s Service) synthesizeSRTToSpeech(ctx context.Context, stepParam *types.SubtitleTaskStepParam) error {
	runner, err := s.newDubbingRunner(stepParam)
	if err != nil {
		return err
	}
	if runner == nil {
		return nil
	}
	result, err := runner.Synthesize(ctx)
	if err != nil {
		return fmt.Errorf("synthesizeSRTToSpeech dubbing runner error: %w", err)
	}
	stepParam.TtsResultFilePath = result.Audio
	if stepParam.TaskPtr != nil {
		stepParam.TaskPtr.ProcessPct = 84
	}
	return nil
}

// muxDubbedAudioVideo only joins an already approved audio file with its
// source video. It does not create a TTS client or use clone credentials.
func (s Service) muxDubbedAudioVideo(stepParam *types.SubtitleTaskStepParam) error {
	if stepParam == nil {
		return fmt.Errorf("muxDubbedAudioVideo stepParam is nil")
	}
	outputAudio, outputVideo := dubbingOutputPaths(stepParam)
	runner := dubbing.NewRunner(dubbing.Dependencies{
		Workdir:     stepParam.TaskBasePath,
		InputVideo:  stepParam.InputVideoPath,
		OutputAudio: outputAudio,
		OutputVideo: outputVideo,
	})
	video, err := runner.Mux()
	if err != nil {
		return fmt.Errorf("muxDubbedAudioVideo dubbing runner error: %w", err)
	}
	stepParam.TtsResultFilePath = outputAudio
	stepParam.VideoWithTtsFilePath = video
	if stepParam.TaskPtr != nil {
		stepParam.TaskPtr.ProcessPct = 92
	}
	return nil
}

func dubbingOutputPaths(stepParam *types.SubtitleTaskStepParam) (string, string) {
	outputAudio := stepParam.TtsResultFilePath
	if outputAudio == "" {
		outputAudio = filepath.Join(stepParam.TaskBasePath, types.TtsResultAudioFileName)
	}
	outputVideo := stepParam.VideoWithTtsFilePath
	if outputVideo == "" {
		outputVideo = filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskVideoWithTtsFileName)
	}
	return outputAudio, outputVideo
}

// newDubbingRunner is used only for synthesis. Muxing is intentionally kept
// provider-independent so an approved audio can be joined later without
// retaining a clone reference in workflow state.
func (s Service) newDubbingRunner(stepParam *types.SubtitleTaskStepParam) (*dubbing.Runner, error) {
	if stepParam == nil {
		return nil, fmt.Errorf("srtFileToSpeech stepParam is nil")
	}
	if !stepParam.EnableTts {
		return nil, nil
	}
	if stepParam.TtsSourceFilePath == "" {
		stepParam.TtsSourceFilePath = targetSRTPathForDubbing(stepParam.TaskBasePath)
	}

	var voiceCode string
	ttsClient := s.TtsClient
	switch strings.ToLower(strings.TrimSpace(config.Conf.Tts.Provider)) {
	case "omnivoice":
		// The reference and consent are per-job only. Do not reuse a persisted
		// config reference or fall back to a synthetic/default local voice.
		if strings.TrimSpace(stepParam.VoiceCloneAudioUrl) == "" {
			return nil, fmt.Errorf("OmniVoice requires a consented reference audio or fixed Voice Studio profile for this job")
		}
		if !stepParam.VoiceCloneConsent {
			return nil, fmt.Errorf("OmniVoice clone consent is required for this job")
		}
		voiceCode = stepParam.VoiceCloneAudioUrl
		ttsClient = omnivoice.NewClient(omnivoice.Config{
			BaseURL:          config.Conf.Tts.Omnivoice.BaseUrl,
			APIKey:           config.Conf.Tts.Omnivoice.SessionApiKey,
			Language:         config.Conf.Tts.Omnivoice.Language,
			ReferenceText:    config.Conf.Tts.Omnivoice.ReferenceText,
			Instruct:         config.Conf.Tts.Omnivoice.Instruct,
			Speed:            config.Conf.Tts.Omnivoice.Speed,
			NumStep:          config.Conf.Tts.Omnivoice.NumStep,
			TimeoutSeconds:   config.Conf.Tts.Omnivoice.RequestTimeoutSeconds,
			RequireReference: true,
			ConsentConfirmed: true,
		})
	case "gateway":
		var err error
		voiceCode, err = resolveGatewayVoiceCode(stepParam.TtsVoiceCode, stepParam.VoiceCloneAudioUrl)
		if err != nil {
			return nil, err
		}
	default:
		var clone voiceCloneFunc
		if s.VoiceCloneClient != nil {
			clone = s.VoiceCloneClient.CosyVoiceClone
		}
		var err error
		voiceCode, err = resolveDubbingVoiceCode(stepParam.TtsVoiceCode, stepParam.VoiceCloneAudioUrl, clone)
		if err != nil {
			return nil, err
		}
	}

	outputAudio, outputVideo := dubbingOutputPaths(stepParam)
	return dubbing.NewRunner(dubbing.Dependencies{
		TTS:         ttsClient,
		Chat:        s.ChatCompleter,
		Language:    stepParam.TargetLanguage,
		Voice:       voiceCode,
		Workdir:     stepParam.TaskBasePath,
		InputSRT:    stepParam.TtsSourceFilePath,
		InputVideo:  stepParam.InputVideoPath,
		OutputAudio: outputAudio,
		OutputVideo: outputVideo,
		Config: dubbing.Config{
			MinSubtitleDuration: config.Conf.Dubbing.MinSubtitleDuration,
			MaxChunkSize:        config.Conf.Dubbing.MaxChunkSize,
			GapTolerance:        config.Conf.Dubbing.GapTolerance,
			SpeedMin:            config.Conf.Dubbing.SpeedMin,
			SpeedAccept:         config.Conf.Dubbing.SpeedAccept,
			SpeedMax:            config.Conf.Dubbing.SpeedMax,
			EnableTextRewrite:   config.Conf.Dubbing.EnableTextRewrite,
			RewriteMaxAttempts:  config.Conf.Dubbing.RewriteMaxAttempts,
			Estimator:           config.Conf.Dubbing.Estimator,
		},
	}), nil
}
