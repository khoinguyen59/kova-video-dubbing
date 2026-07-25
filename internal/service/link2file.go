package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"kova/config"
	"kova/internal/processutil"
	"kova/internal/storage"
	"kova/internal/types"
	"kova/pkg/util"
)

func reportSourceProgress(stepParam *types.SubtitleTaskStepParam, id string, percent uint8, detail string) {
	if stepParam != nil && stepParam.SourceProgress != nil {
		stepParam.SourceProgress(id, percent, detail)
	}
}

func (s Service) linkToFile(ctx context.Context, stepParam *types.SubtitleTaskStepParam) error {
	if stepParam == nil || stepParam.TaskPtr == nil {
		return errors.New("linkToFile requires a task and task parameters")
	}
	link := stepParam.Link
	audioPath := filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskAudioFileName)
	videoPath := filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskVideoFileName)
	if err := os.MkdirAll(stepParam.TaskBasePath, 0755); err != nil {
		return fmt.Errorf("create source task directory: %w", err)
	}

	// The staged KOVA workflow must expose an actual MP4 artifact before STT
	// or OCR begins. Download/remux the video first, then derive audio from that
	// exact artifact. It makes the preview available even when transcription later
	// fails, and avoids a second inconsistent download.
	needSourceVideo := strings.HasPrefix(link, "local:") || stepParam.EmbedSubtitleVideoType != "none" || stepParam.EnableTts
	if needSourceVideo {
		stepParam.TaskPtr.ProcessPct = 2
		reportSourceProgress(stepParam, "download_video", 0, "Downloading source video for preview")
		if err := downloadOrCopySourceVideo(ctx, link, videoPath, stepParam.SourceCookieBrowser, func(percent uint8, detail string) {
			reportSourceProgress(stepParam, "download_video", percent, detail)
		}); err != nil {
			return err
		}
		reportSourceProgress(stepParam, "download_video", 100, "Source video ready for preview")
		stepParam.TaskPtr.ProcessPct = 6
		reportSourceProgress(stepParam, "download_audio", 0, "Extracting source audio from downloaded video")
		if err := extractSourceAudio(ctx, videoPath, audioPath); err != nil {
			return err
		}
		reportSourceProgress(stepParam, "download_audio", 100, "Source audio ready")
		stepParam.AudioFilePath = audioPath
		stepParam.SourceAudioFilePath = audioPath
		stepParam.InputVideoPath = videoPath
		stepParam.TaskPtr.ProcessPct = 10
		return nil
	}

	// Legacy VTT-only calls may request audio without a reviewable video. Keep
	// that compatibility path, but the native staged workflow never takes it.
	stepParam.TaskPtr.ProcessPct = 3
	reportSourceProgress(stepParam, "download_audio", 0, "Preparing source audio")
	if !util.IsSupportedVideoURL(link) {
		return errors.New("linkToFile error: unsupported link; use YouTube, TikTok, Douyin, Bilibili, or a local video file")
	}
	// Douyin's web detail endpoint now requires JavaScript-generated
	// __ac_signature/a_bogus state. A cookie-only yt-dlp retry still reaches
	// encrypt_data_miss, so Douyin is handled by the managed browser resolver
	// before the generic extractor path.
	if isDouyinURL(link) && !strings.EqualFold(strings.TrimSpace(stepParam.SourceCookieBrowser), "none") {
		if err := downloadDouyinSourceVideo(ctx, link, videoPath, stepParam.SourceCookieBrowser, func(percent uint8, detail string) {
			reportSourceProgress(stepParam, "download_video", percent, detail)
		}); err != nil {
			return fmt.Errorf("tải video Douyin qua phiên trình duyệt đã ký của KOVA: %w", err)
		}
		if err := extractSourceAudio(ctx, videoPath, audioPath); err != nil {
			return err
		}
		reportSourceProgress(stepParam, "download_audio", 100, "Source audio ready")
		stepParam.AudioFilePath = audioPath
		stepParam.SourceAudioFilePath = audioPath
		stepParam.InputVideoPath = videoPath
		stepParam.TaskPtr.ProcessPct = 10
		return nil
	}
	cmdArgs := []string{
		"-f", "bestaudio[ext=m4a]/bestaudio[ext=mp3]/bestaudio/worst",
		"--no-playlist",
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", "192K",
		"-o", audioPath,
	}
	if config.Conf.App.Proxy != "" {
		cmdArgs = append(cmdArgs, "--proxy", config.Conf.App.Proxy)
	}
	if util.IsYouTubeURL(link) {
		cmdArgs = appendCookiesArgs(cmdArgs, youtubeCookiesPath)
	}
	if storage.FfmpegPath != "ffmpeg" {
		cmdArgs = append(cmdArgs, "--ffmpeg-location", storage.FfmpegPath)
	}
	output, err := runYtDlpSourceDownload(ctx, cmdArgs, link, stepParam.SourceCookieBrowser)
	if err != nil {
		return sourceDownloadError("tải audio nguồn", err, output, link, stepParam.SourceCookieBrowser)
	}
	reportSourceProgress(stepParam, "download_audio", 100, "Source audio downloaded")
	stepParam.AudioFilePath = audioPath
	stepParam.SourceAudioFilePath = audioPath
	stepParam.InputVideoPath = videoPath
	stepParam.TaskPtr.ProcessPct = 10
	return nil
}

// downloadOrCopySourceVideo places every staged input inside the task
// directory. The file endpoint deliberately serves only task-owned files, so
// keeping a local source at its original path would make the preview/artifact
// silently disappear.
func downloadOrCopySourceVideo(ctx context.Context, link, videoPath, sourceCookieBrowser string, progress douyinProgress) error {
	if strings.HasPrefix(link, "local:") {
		source := strings.TrimSpace(strings.TrimPrefix(link, "local:"))
		info, err := os.Stat(source)
		if err != nil || info.IsDir() {
			if err == nil {
				err = errors.New("source is a directory")
			}
			return fmt.Errorf("local source video cannot be read: %w", err)
		}
		command := exec.CommandContext(ctx, storage.FfmpegPath,
			"-y", "-i", source, "-map", "0", "-c", "copy", "-movflags", "+faststart", videoPath,
		)
		processutil.HideConsole(command)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("cannot copy local source into KOVA preview MP4: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if !util.IsSupportedVideoURL(link) {
		return errors.New("linkToFile error: unsupported link; use YouTube, TikTok, Douyin, Bilibili, or a local video file")
	}
	if isDouyinURL(link) && !strings.EqualFold(strings.TrimSpace(sourceCookieBrowser), "none") {
		if err := downloadDouyinSourceVideo(ctx, link, videoPath, sourceCookieBrowser, progress); err != nil {
			return fmt.Errorf("tải video Douyin qua phiên trình duyệt đã ký của KOVA: %w", err)
		}
		return nil
	}
	cmdArgs := []string{
		"-f", browserPreviewVideoFormat(),
		"--no-playlist",
		"--merge-output-format", "mp4",
		"-o", videoPath,
	}
	if config.Conf.App.Proxy != "" {
		cmdArgs = append(cmdArgs, "--proxy", config.Conf.App.Proxy)
	}
	if util.IsYouTubeURL(link) {
		cmdArgs = appendCookiesArgs(cmdArgs, youtubeCookiesPath)
	}
	if storage.FfmpegPath != "ffmpeg" {
		cmdArgs = append(cmdArgs, "--ffmpeg-location", storage.FfmpegPath)
	}
	output, err := runYtDlpSourceDownload(ctx, cmdArgs, link, sourceCookieBrowser)
	if err != nil {
		return sourceDownloadError("tải video nguồn", err, output, link, sourceCookieBrowser)
	}
	if info, err := os.Stat(videoPath); err != nil || info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("yt-dlp did not create the expected source video %q", videoPath)
	}
	return nil
}

// runYtDlpSourceDownload owns the short-video session fallback. Douyin and
// TikTok occasionally deny their public JSON endpoints even for a fresh
// yt-dlp build. We first make the normal anonymous request. If the platform
// asks for fresh cookies, KOVA creates a brand-new temporary browser profile,
// opens the URL headlessly, and retries with only that ephemeral session.
//
// This deliberately does not use --cookies-from-browser. Modern Windows
// Chrome/Edge profiles use App-Bound DPAPI encryption, which makes that method
// fail for a desktop downloader and produced the exact error this fallback
// replaces. No personal browser cookie is decrypted, copied, logged, saved, or
// sent over KOVA's API.
func runYtDlpSourceDownload(ctx context.Context, baseArgs []string, link, sourceCookieBrowser string) ([]byte, error) {
	args := append(append([]string(nil), baseArgs...), link)
	command := exec.CommandContext(ctx, storage.YtdlpPath, args...)
	processutil.HideConsole(command)
	output, err := command.CombinedOutput()
	if err == nil || !ytDlpRequestsFreshCookies(output) || !isCookieRestrictedShortVideoURL(link) || strings.EqualFold(strings.TrimSpace(sourceCookieBrowser), "none") {
		return output, err
	}

	for _, browser := range shortVideoSessionBrowsers(sourceCookieBrowser) {
		cookiePath, cleanup, sessionErr := createFreshShortVideoCookieFile(ctx, link, browser)
		if sessionErr != nil {
			output = append(output, []byte("\nKOVA_TEMP_BROWSER_SESSION_ERROR: "+sessionErr.Error())...)
			continue
		}
		retryArgs := append([]string(nil), baseArgs...)
		// Use the same ordinary browser User-Agent for the temporary-browser
		// session and yt-dlp's follow-up request. Some short-video sites bind
		// their freshness checks to the session's User-Agent.
		retryArgs = append(retryArgs,
			"--cookies", cookiePath,
			"--user-agent", shortVideoSessionUserAgent(browser),
			link,
		)
		retryCommand := exec.CommandContext(ctx, storage.YtdlpPath, retryArgs...)
		processutil.HideConsole(retryCommand)
		output, err = retryCommand.CombinedOutput()
		cleanup()
		if err == nil {
			return output, nil
		}
	}
	return output, err
}

func shortVideoSessionBrowsers(configured string) []string {
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "none":
		return nil
	case "chrome":
		return []string{"chrome"}
	case "edge":
		return []string{"edge"}
	default:
		// Auto tries separate new browser profiles, not a user's existing
		// profile. Edge is first because it is installed with Windows and Chrome
		// is a compatible fallback when Edge is unavailable.
		return []string{"edge", "chrome"}
	}
}

func isCookieRestrictedShortVideoURL(link string) bool {
	lower := strings.ToLower(strings.TrimSpace(link))
	return strings.Contains(lower, "douyin.com") || strings.Contains(lower, "tiktok.com")
}

func ytDlpRequestsFreshCookies(output []byte) bool {
	lower := strings.ToLower(string(output))
	return strings.Contains(lower, "fresh cookies") ||
		strings.Contains(lower, "cookies (not necessarily logged in) are needed") ||
		strings.Contains(lower, "cookies are needed")
}

func sourceDownloadError(operation string, err error, output []byte, link, sourceCookieBrowser string) error {
	if ytDlpRequestsFreshCookies(output) && isCookieRestrictedShortVideoURL(link) {
		attempt := "phiên trình duyệt tạm của KOVA (Edge rồi Chrome)"
		switch strings.ToLower(strings.TrimSpace(sourceCookieBrowser)) {
		case "none":
			attempt = "phiên công khai (chế độ cookie đang tắt)"
		case "chrome":
			attempt = "phiên Chrome tạm của KOVA"
		case "edge":
			attempt = "phiên Microsoft Edge tạm của KOVA"
		}
		if strings.Contains(string(output), "KOVA_TEMP_BROWSER_SESSION_ERROR:") {
			return fmt.Errorf("%s bị Douyin/TikTok chặn vì cần cookie phiên mới, đồng thời KOVA không tạo được %s: %s", operation, attempt, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("%s bị Douyin/TikTok chặn vì cần cookie phiên mới sau khi KOVA đã tự thử %s", operation, attempt)
	}
	return fmt.Errorf("%s với yt-dlp thất bại: %w: %s", operation, err, strings.TrimSpace(string(output)))
}

// browserPreviewVideoFormat prefers H.264/AVC MP4 streams. YouTube's AV1 MP4
// streams are efficient but are not consistently decoded by the Windows
// WebView runtime used by the desktop preview. The final fallback preserves
// prior download behavior when an AVC stream is unavailable.
func browserPreviewVideoFormat() string {
	return "bestvideo[vcodec^=avc1][height<=1080][ext=mp4]+bestaudio[ext=m4a]/bestvideo[vcodec^=avc1][height<=720][ext=mp4]+bestaudio[ext=m4a]/bestvideo[vcodec^=avc1][height<=480][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=480][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best"
}

func extractSourceAudio(ctx context.Context, videoPath, audioPath string) error {
	command := exec.CommandContext(ctx, storage.FfmpegPath,
		"-y", "-i", videoPath, "-vn", "-ar", "44100", "-ac", "2", "-b:a", "192k", audioPath,
	)
	processutil.HideConsole(command)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("extract source audio from preview video: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if info, err := os.Stat(audioPath); err != nil || info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("ffmpeg did not create the expected source audio %q", audioPath)
	}
	return nil
}

// shouldDownloadStandaloneSourceAudio remains the compatibility predicate for
// legacy callers. The staged KOVA source workflow always requests a video and
// therefore takes the explicit video-then-audio path above.
func shouldDownloadStandaloneSourceAudio(stepParam *types.SubtitleTaskStepParam) bool {
	if stepParam == nil {
		return false
	}
	return !stepParam.VttSwitch || stepParam.EmbedSubtitleVideoType != "none" || stepParam.EnableTts
}
