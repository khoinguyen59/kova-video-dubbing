package service

import (
	"context"
	"errors"
	"fmt"
	"kova/config"
	"kova/internal/dto"
	"kova/internal/storage"
	"kova/internal/types"
	"kova/log"
	"kova/pkg/omnivoice"
	"kova/pkg/util"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/samber/lo"
	"go.uber.org/zap"
)

func (s Service) StartSubtitleTask(req dto.StartVideoSubtitleTaskReq) (*dto.StartVideoSubtitleTaskResData, error) {
	if req.Tts == types.SubtitleTaskTtsYes && strings.EqualFold(strings.TrimSpace(config.Conf.Tts.Provider), "omnivoice") {
		if strings.TrimSpace(req.TtsVoiceCloneSrcFileUrl) == "" {
			return nil, errors.New("OmniVoice yêu cầu chọn audio mẫu cho job này; Kova không tự tạo giọng")
		}
		if !req.VoiceCloneConsent {
			return nil, errors.New("cần xác nhận bạn có quyền sử dụng audio mẫu trước khi clone giọng")
		}
		if err := config.ValidateRemoteOmniVoiceWorker(); err != nil {
			return nil, err
		}
		if config.Conf.Tts.Omnivoice.RequireCUDA {
			if _, err := omnivoice.ProbeColabGPUWithAPIKey(config.Conf.Tts.Omnivoice.BaseUrl, config.Conf.Tts.Omnivoice.SessionApiKey, 12*time.Second); err != nil {
				return nil, fmt.Errorf("worker OmniVoice Colab chưa sẵn sàng: %w", err)
			}
		}
	}
	// 校验链接
	if util.IsYouTubeURL(req.Url) {
		videoId, _ := util.GetYouTubeID(req.Url)
		if videoId == "" {
			return nil, fmt.Errorf("URL không hợp lệ")
		}
	}
	if strings.Contains(req.Url, "bilibili.com") {
		videoId := util.GetBilibiliVideoId(req.Url)
		if videoId == "" {
			return nil, fmt.Errorf("URL không hợp lệ")
		}
	}
	// 生成任务id
	seperates := strings.Split(req.Url, "/")
	taskId := fmt.Sprintf("%s_%s", util.SanitizePathName(string([]rune(strings.ReplaceAll(seperates[len(seperates)-1], " ", ""))[:16])), util.GenerateRandStringWithUpperLowerNum(4))
	taskId = strings.ReplaceAll(taskId, "=", "") // 等于号影响ffmpeg处理
	taskId = strings.ReplaceAll(taskId, "?", "") // 问号影响ffmpeg处理
	// 构造任务所需参数
	var resultType types.SubtitleResultType
	// 根据入参选项确定要返回的字幕类型
	if req.TargetLang == "none" {
		resultType = types.SubtitleResultTypeOriginOnly
	} else {
		if req.Bilingual == types.SubtitleTaskBilingualYes {
			if req.TranslationSubtitlePos == types.SubtitleTaskTranslationSubtitlePosTop {
				resultType = types.SubtitleResultTypeBilingualTranslationOnTop
			} else {
				resultType = types.SubtitleResultTypeBilingualTranslationOnBottom
			}
		} else {
			resultType = types.SubtitleResultTypeTargetOnly
		}
	}
	// 文字替换map
	replaceWordsMap := make(map[string]string)
	if len(req.Replace) > 0 {
		for _, replace := range req.Replace {
			beforeAfter := strings.Split(replace, "|")
			if len(beforeAfter) == 2 {
				replaceWordsMap[beforeAfter[0]] = beforeAfter[1]
			} else {
				log.GetLogger().Info("generateAudioSubtitles replace param length err", zap.Any("replace", replace), zap.Any("taskId", taskId))
			}
		}
	}
	protectedTerms := make(map[string]string)
	for index, term := range req.ProtectTerms {
		term = strings.TrimSpace(term)
		if term != "" {
			protectedTerms[term] = fmt.Sprintf("[[KOVA_PROPER_%03d]]", index+1)
		}
	}
	var err error
	ctx := context.Background()
	// 创建字幕任务文件夹
	taskBasePath := filepath.Join("./tasks", taskId)
	if _, err = os.Stat(taskBasePath); os.IsNotExist(err) {
		// 不存在则创建
		err = os.MkdirAll(filepath.Join(taskBasePath, "output"), os.ModePerm)
		if err != nil {
			log.GetLogger().Error("StartVideoSubtitleTask MkdirAll err", zap.Any("req", req), zap.Error(err))
		}
	}

	// 创建任务
	taskPtr := &types.SubtitleTask{
		TaskId:   taskId,
		VideoSrc: req.Url,
		Status:   types.SubtitleTaskStatusProcessing,
	}
	storage.SubtitleTasks.Store(taskId, taskPtr)

	// 处理声音克隆源
	var voiceCloneAudioUrl string
	if req.TtsVoiceCloneSrcFileUrl != "" {
		localFileUrl := strings.TrimPrefix(req.TtsVoiceCloneSrcFileUrl, "local:")
		if config.Conf.Tts.Provider == "omnivoice" {
			if _, statErr := os.Stat(localFileUrl); statErr != nil {
				return nil, fmt.Errorf("không đọc được file giọng mẫu OmniVoice: %w", statErr)
			}
			voiceCloneAudioUrl = localFileUrl
		} else {
			fileKey := util.GenerateRandStringWithUpperLowerNum(5) + filepath.Ext(localFileUrl) // 防止url encode的问题，这里统一处理
			err = s.OssClient.UploadFile(context.Background(), fileKey, localFileUrl, s.OssClient.Bucket)
			if err != nil {
				log.GetLogger().Error("StartVideoSubtitleTask UploadFile err", zap.Any("req", req), zap.Error(err))
				return nil, errors.New("không thể tải audio mẫu để clone giọng")
			}
			voiceCloneAudioUrl = fmt.Sprintf("https://%s.oss-cn-shanghai.aliyuncs.com/%s", s.OssClient.Bucket, fileKey)
			log.GetLogger().Info("StartVideoSubtitleTask 上传声音克隆源成功", zap.Any("oss url", voiceCloneAudioUrl))
		}
	}

	stepParam := types.SubtitleTaskStepParam{
		TaskId:             taskId,
		TaskPtr:            taskPtr,
		TaskBasePath:       taskBasePath,
		Link:               req.Url,
		SubtitleResultType: resultType,
		EnableModalFilter:  req.ModalFilter == types.SubtitleTaskModalFilterYes,
		EnableTts:          req.Tts == types.SubtitleTaskTtsYes,
		TtsVoiceCode:       req.TtsVoiceCode,
		VoiceCloneAudioUrl: voiceCloneAudioUrl,
		// Consent is deliberately scoped to this request. The dubbing stage
		// re-checks it before it can send a reference clip to the remote
		// OmniVoice Colab worker, so it must survive request -> step mapping.
		VoiceCloneConsent:       req.VoiceCloneConsent,
		ReplaceWordsMap:         replaceWordsMap,
		ProtectedTerms:          protectedTerms,
		OriginLanguage:          types.StandardLanguageCode(req.OriginLanguage),
		TargetLanguage:          types.StandardLanguageCode(req.TargetLang),
		UserUILanguage:          types.StandardLanguageCode(req.Language),
		EmbedSubtitleVideoType:  req.EmbedSubtitleVideoType,
		VerticalVideoMajorTitle: req.VerticalMajorTitle,
		VerticalVideoMinorTitle: req.VerticalMinorTitle,
		MaxWordOneLine:          12, // 默认值
		VttSwitch:               req.VttSwitch,
	}
	if req.OriginLanguageWordOneLine != 0 {
		stepParam.MaxWordOneLine = req.OriginLanguageWordOneLine
	}

	log.GetLogger().Info("current task info", zap.String("taskId", taskId), zap.Any("param", stepParam))

	go func() {
		defer func() {
			if r := recover(); r != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				log.GetLogger().Error("autoVideoSubtitle panic", zap.Any("panic:", r), zap.Any("stack:", buf))
				stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
			}
		}()
		// 新版流程：链接->本地音频文件->视频信息获取（若有）->本地字幕文件->语言合成->视频合成->字幕文件链接生成
		log.GetLogger().Info("video subtitle start task", zap.String("taskId", taskId))
		err = s.linkToFile(ctx, &stepParam)
		if err != nil {
			log.GetLogger().Error("StartVideoSubtitleTask linkToFile err", zap.Any("req", req), zap.Error(err))
			stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
			stepParam.TaskPtr.FailReason = err.Error()
			return
		}
		// 暂时不加视频信息
		//err = s.getVideoInfo(ctx, &stepParam)
		//if err != nil {
		//	log.GetLogger().Error("StartVideoSubtitleTask getVideoInfo err", zap.Any("req", req), zap.Error(err))
		//	stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
		//	stepParam.TaskPtr.FailReason = "get video info error"
		//	return
		//}

		// 针对YouTube视频优先尝试使用yt-dlp下载字幕
		if util.IsYouTubeURL(req.Url) && stepParam.VttSwitch {
			log.GetLogger().Info("Start Process youtube video with vtt", zap.String("taskId", taskId))
			req := &YoutubeSubtitleReq{
				TaskBasePath:        stepParam.TaskBasePath,
				TaskId:              taskId,
				OriginLanguage:      string(stepParam.OriginLanguage),
				TargetLanguage:      string(stepParam.TargetLanguage),
				URL:                 req.Url,
				TaskPtr:             stepParam.TaskPtr,
				ProtectedTerms:      stepParam.ProtectedTerms,
				TargetLanguageFirst: config.Conf.App.TargetLanguageFirst,
			}

			// 先下载VTT字幕
			vttFile, err := s.YouTubeSubtitleSrv.downloadYouTubeSubtitle(ctx, req)
			if err != nil {
				// 下载失败，回退到音频转录方式
				log.GetLogger().Warn("Failed to download YouTube subtitles, falling back to audio transcription",
					zap.String("taskId", taskId), zap.Error(err))
				stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
				stepParam.TaskPtr.FailReason = err.Error()
				return
			}
			req.VttFile = vttFile

			// 检测VTT格式类型
			hasWordTimestamps := true // 默认假设有单词级时间戳
			if config.Conf.App.EnableBlockVttBatch {
				// 只有启用了新功能才进行格式检测
				detected, detectErr := s.YouTubeSubtitleSrv.DetectVttFormat(vttFile)
				if detectErr != nil {
					log.GetLogger().Warn("VTT格式检测失败，使用默认处理方式",
						zap.String("taskId", taskId), zap.Error(detectErr))
				} else {
					hasWordTimestamps = detected
				}
			}

			var srtFile string
			if hasWordTimestamps {
				// 使用原有的word-level处理流程（完全不变）
				log.GetLogger().Info("使用word-level VTT处理流程", zap.String("taskId", taskId))
				srtFile, err = s.YouTubeSubtitleSrv.processYouTubeSubtitle(ctx, req)
			} else {
				// 使用新的block-level处理流程
				log.GetLogger().Info("使用block-level VTT处理流程", zap.String("taskId", taskId))
				srtFile, err = s.YouTubeSubtitleSrv.ProcessBlockLevelVtt(ctx, req)
			}

			if err != nil {
				// 处理字幕失败，回退到音频转录方式
				log.GetLogger().Warn("Failed to process YouTube subtitles, falling back to audio transcription",
					zap.String("taskId", taskId), zap.Error(err))
				stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
				stepParam.TaskPtr.FailReason = err.Error()
				return
			}

			stepParam.BilingualSrtFilePath = srtFile
			err = splitSrt(&stepParam)
			if err != nil {
				return
			}
			stepParam.TaskPtr.ProcessPct = 95
		} else {
			// 非YouTube视频，使用原来的音频转录流程
			err = s.audioToSubtitle(ctx, &stepParam)
			if err != nil {
				log.GetLogger().Error("StartVideoSubtitleTask audioToSubtitle err", zap.Any("req", req), zap.Error(err))
				stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
				stepParam.TaskPtr.FailReason = err.Error()
				return
			}
		}
		err = s.srtFileToSpeech(ctx, &stepParam)
		if err != nil {
			log.GetLogger().Error("StartVideoSubtitleTask srtFileToSpeech err", zap.Any("req", req), zap.Error(err))
			stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
			stepParam.TaskPtr.FailReason = err.Error()
			return
		}
		err = s.embedSubtitles(ctx, &stepParam)
		if err != nil {
			log.GetLogger().Error("StartVideoSubtitleTask embedSubtitles err", zap.Any("req", req), zap.Error(err))
			stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
			stepParam.TaskPtr.FailReason = err.Error()
			return
		}
		err = s.uploadSubtitles(ctx, &stepParam)
		if err != nil {
			log.GetLogger().Error("StartVideoSubtitleTask uploadSubtitles err", zap.Any("req", req), zap.Error(err))
			stepParam.TaskPtr.Status = types.SubtitleTaskStatusFailed
			stepParam.TaskPtr.FailReason = err.Error()
			return
		}

		log.GetLogger().Info("video subtitle task end", zap.String("taskId", taskId))
	}()

	return &dto.StartVideoSubtitleTaskResData{
		TaskId: taskId,
	}, nil
}

func (s Service) GetTaskStatus(req dto.GetVideoSubtitleTaskReq) (*dto.GetVideoSubtitleTaskResData, error) {
	task, ok := storage.SubtitleTasks.Load(req.TaskId)
	if !ok || task == nil {
		return nil, errors.New("không tìm thấy job")
	}
	taskPtr := task.(*types.SubtitleTask)
	if taskPtr.Status == types.SubtitleTaskStatusFailed {
		return nil, fmt.Errorf("Job thất bại: %s", taskPtr.FailReason)
	}
	return &dto.GetVideoSubtitleTaskResData{
		TaskId:         taskPtr.TaskId,
		ProcessPercent: taskPtr.ProcessPct,
		VideoInfo: &dto.VideoInfo{
			Title:                 taskPtr.Title,
			Description:           taskPtr.Description,
			TranslatedTitle:       taskPtr.TranslatedTitle,
			TranslatedDescription: taskPtr.TranslatedDescription,
		},
		SubtitleInfo: lo.Map(taskPtr.SubtitleInfos, func(item types.SubtitleInfo, _ int) *dto.SubtitleInfo {
			return &dto.SubtitleInfo{
				Name:        item.Name,
				DownloadUrl: item.DownloadUrl,
			}
		}),
		VideoOutputs: lo.Map(taskPtr.VideoOutputInfos, func(item types.SubtitleInfo, _ int) *dto.SubtitleInfo {
			return &dto.SubtitleInfo{
				Name:        item.Name,
				DownloadUrl: item.DownloadUrl,
			}
		}),
		Artifacts: lo.Map(taskPtr.Artifacts, func(item types.ArtifactInfo, _ int) *dto.ArtifactInfo {
			return &dto.ArtifactInfo{
				Kind:        item.Kind,
				Label:       item.Label,
				Name:        item.Name,
				DownloadUrl: item.DownloadUrl,
			}
		}),
		TargetLanguage:    taskPtr.TargetLanguage,
		SpeechDownloadUrl: taskPtr.SpeechDownloadUrl,
	}, nil
}
