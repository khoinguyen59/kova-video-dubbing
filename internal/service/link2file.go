package service

import (
	"context"
	"errors"
	"fmt"
	"kova/config"
	"kova/internal/storage"
	"kova/internal/types"
	"kova/log"
	"kova/pkg/util"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

func reportSourceProgress(stepParam *types.SubtitleTaskStepParam, id string, percent uint8, detail string) {
	if stepParam != nil && stepParam.SourceProgress != nil {
		stepParam.SourceProgress(id, percent, detail)
	}
}

func (s Service) linkToFile(ctx context.Context, stepParam *types.SubtitleTaskStepParam) error {
	var (
		err    error
		output []byte
	)
	link := stepParam.Link
	audioPath := fmt.Sprintf("%s/%s", stepParam.TaskBasePath, types.SubtitleTaskAudioFileName)
	videoPath := fmt.Sprintf("%s/%s", stepParam.TaskBasePath, types.SubtitleTaskVideoFileName)
	stepParam.TaskPtr.ProcessPct = 3
	reportSourceProgress(stepParam, "download_audio", 0, "Preparing source audio")
	if strings.Contains(link, "local:") {
		// 本地文件
		videoPath = strings.ReplaceAll(link, "local:", "")
		reportSourceProgress(stepParam, "download_video", 100, "Local source selected")
		cmd := exec.Command(storage.FfmpegPath, "-i", videoPath, "-vn", "-ar", "44100", "-ac", "2", "-ab", "192k", "-f", "mp3", audioPath)
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.GetLogger().Error("generateAudioSubtitles.linkToFile ffmpeg error", zap.Any("step param", stepParam), zap.String("output", string(output)), zap.Error(err))
			return fmt.Errorf("generateAudioSubtitles.linkToFile ffmpeg error: %w", err)
		}
	} else if util.IsYouTubeURL(link) {
		var videoId string
		videoId, err = util.GetYouTubeID(link)
		if err != nil {
			log.GetLogger().Error("linkToFile.GetYouTubeID error", zap.Any("step param", stepParam), zap.Error(err))
			return fmt.Errorf("linkToFile.GetYouTubeID error: %w", err)
		}
		stepParam.Link = "https://www.youtube.com/watch?v=" + videoId
		// The review-first Kova workflow exposes the source audio as a real
		// artifact whenever it also downloads an MP4 for review/rendering or a
		// later dubbing step.  Historically a VTT-only subtitle request skipped
		// this download, which left the advertised "Audio nguồn" artifact absent
		// even though the user had explicitly asked for a complete, inspectable
		// source package.  Keep the lightweight legacy VTT-only path intact.
		if shouldDownloadStandaloneSourceAudio(stepParam) {
			// 使用更灵活的音频格式选择器，避免 HTTP 403 错误。
			cmdArgs := []string{
				"-f", "bestaudio[ext=m4a]/bestaudio[ext=mp3]/bestaudio/worst",
				"--extract-audio",
				"--audio-format", "mp3",
				"--audio-quality", "192K",
				"-o", audioPath,
				stepParam.Link,
			}
			if config.Conf.App.Proxy != "" {
				cmdArgs = append(cmdArgs, "--proxy", config.Conf.App.Proxy)
			}
			cmdArgs = appendCookiesArgs(cmdArgs, youtubeCookiesPath)
			if storage.FfmpegPath != "ffmpeg" {
				cmdArgs = append(cmdArgs, "--ffmpeg-location", storage.FfmpegPath)
			}
			cmd := exec.Command(storage.YtdlpPath, cmdArgs...)
			output, err = cmd.CombinedOutput()
			if err != nil {
				log.GetLogger().Error("linkToFile download audio yt-dlp error", zap.Any("step param", stepParam), zap.String("output", string(output)), zap.Error(err))
				return fmt.Errorf("linkToFile download audio yt-dlp error: %w", err)
			}
		}
	} else if strings.Contains(link, "bilibili.com") {
		videoId := util.GetBilibiliVideoId(link)
		if videoId == "" {
			return errors.New("linkToFile error: invalid link")
		}
		stepParam.Link = "https://www.bilibili.com/video/" + videoId
		cmdArgs := []string{"-f", "bestaudio[ext=m4a]", "-x", "--audio-format", "mp3", "-o", audioPath, stepParam.Link}
		if config.Conf.App.Proxy != "" {
			cmdArgs = append(cmdArgs, "--proxy", config.Conf.App.Proxy)
		}
		if storage.FfmpegPath != "ffmpeg" {
			cmdArgs = append(cmdArgs, "--ffmpeg-location", storage.FfmpegPath)
		}
		cmd := exec.Command(storage.YtdlpPath, cmdArgs...)
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.GetLogger().Error("linkToFile download audio yt-dlp error", zap.Any("step param", stepParam), zap.String("output", string(output)), zap.Error(err))
			return fmt.Errorf("linkToFile download audio yt-dlp error: %w", err)
		}
	} else {
		log.GetLogger().Info("linkToFile.unsupported link type", zap.Any("step param", stepParam))
		return errors.New("linkToFile error: unsupported link, only support youtube, bilibili and local file")
	}
	stepParam.TaskPtr.ProcessPct = 6
	reportSourceProgress(stepParam, "download_audio", 100, "Source audio downloaded")
	stepParam.AudioFilePath = audioPath

	// Download the source MP4 whenever the user asks for a dubbing track or a
	// subtitle-rendered video. The old condition only downloaded it for burned
	// subtitles, leaving the TTS assembler without InputVideoPath.
	if !strings.HasPrefix(link, "local:") && (stepParam.EmbedSubtitleVideoType != "none" || stepParam.EnableTts) {
		reportSourceProgress(stepParam, "download_video", 0, "Downloading source video")
		// 需要下载原视频
		cmdArgs := []string{"-f", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=480][ext=mp4]+bestaudio[ext=m4a]", "-o", videoPath, stepParam.Link}
		if config.Conf.App.Proxy != "" {
			cmdArgs = append(cmdArgs, "--proxy", config.Conf.App.Proxy)
		}
		// Keep the video-download request authenticated in exactly the same way
		// as the platform-subtitle request. Without this, a valid cookies.txt can
		// fetch the VTT but the source MP4 may still fail before a user can review
		// or render the approved subtitles.
		cmdArgs = appendCookiesArgs(cmdArgs, youtubeCookiesPath)
		if storage.FfmpegPath != "ffmpeg" {
			cmdArgs = append(cmdArgs, "--ffmpeg-location", storage.FfmpegPath)
		}
		cmd := exec.Command(storage.YtdlpPath, cmdArgs...)
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.GetLogger().Error("linkToFile download video yt-dlp error", zap.Any("step param", stepParam), zap.String("output", string(output)), zap.Error(err))
			return fmt.Errorf("linkToFile download video yt-dlp error: %w", err)
		}
		reportSourceProgress(stepParam, "download_video", 100, "Source video downloaded")
	}
	stepParam.InputVideoPath = videoPath

	// 更新字幕任务信息
	stepParam.TaskPtr.ProcessPct = 10
	return nil
}

func shouldDownloadStandaloneSourceAudio(stepParam *types.SubtitleTaskStepParam) bool {
	if stepParam == nil {
		return false
	}
	return !stepParam.VttSwitch || stepParam.EmbedSubtitleVideoType != "none" || stepParam.EnableTts
}
