package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"kova/internal/deps"
	"kova/internal/processutil"
	"kova/internal/storage"
	"kova/internal/types"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RenderProgress is emitted for every independently visible render task.
// During encoding FFmpeg provides the encoded media timestamp, which lets us
// report a measured percentage and a continually recalculated ETA instead of
// a fixed milestone such as 94%.
type RenderProgress struct {
	Phase                 string
	Percent               uint8
	Detail                string
	EstimatedCompletionAt time.Time
}

type RenderProgressFunc func(RenderProgress)

type RenderVideoRequest struct {
	Workdir      string
	InputVideo   string
	SubtitleFile string
	OutputFile   string
	Horizontal   bool
	StepParam    *types.SubtitleTaskStepParam
	Progress     RenderProgressFunc
}

type resolutionProbe func(inputVideo string) (int, int, error)

// FFmpeg's boxblur radius must be smaller than the smallest dimension of the
// selected crop.  KOVA's default caption band is only 20% high on short
// videos, and the portable FFmpeg build rejects radius 12 there.  Keep one
// conservative upper bound across the API and UI instead of discovering it
// after an expensive final encode has begun.
const maxRenderBlurStrength = 11

func (s Service) RenderVideo(ctx context.Context, req RenderVideoRequest) (string, error) {
	return renderSubtitleFile(ctx, req)
}

func renderAssPath(req RenderVideoRequest) string {
	base := strings.TrimSuffix(filepath.Base(req.OutputFile), filepath.Ext(req.OutputFile))
	if base == "" || base == "." {
		base = "subtitles"
	}
	return filepath.Join(req.Workdir, fmt.Sprintf("formatted_%s.ass", base))
}

func escapeAssFilterPath(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	p = strings.ReplaceAll(p, ":", `\:`)
	return p
}

func buildEmbedSubtitleArgs(req RenderVideoRequest) ([]string, string) {
	assPath := renderAssPath(req)
	ass := escapeAssFilterPath(assPath)
	args := []string{
		"-y",
		"-i", req.InputVideo,
	}
	if step := req.StepParam; step != nil && step.BlurOriginalText {
		filter := buildBlurredSubtitleFilter(ass, step)
		args = append(args, []string{
			"-filter_complex", filter,
			"-map", "[kova_subtitles]",
			"-map", "0:a?",
		}...)
	} else {
		args = append(args, "-vf", fmt.Sprintf("ass=%s", ass))
	}
	// FFmpeg options must precede their output file. In particular, placing
	// -filter_complex after the incomplete "-c:v" option makes FFmpeg treat
	// the filter graph as a codec/output filename instead of a filter graph.
	args = append(args,
		"-c:v", "libx264",
		"-preset", "fast",
		"-c:a", "aac",
		"-b:a", "192k",
		req.OutputFile,
	)
	return args, assPath
}

// buildBlurredSubtitleFilter keeps the rest of the frame intact, blurs only
// the selected hardcoded-caption band, then draws KOVA's approved ASS output
// on top. Normalized expressions work for horizontal and converted vertical
// renders without guessing a fixed pixel resolution.
func buildBlurredSubtitleFilter(ass string, step *types.SubtitleTaskStepParam) string {
	x, y, width, height := step.BlurRegionX, step.BlurRegionY, step.BlurRegionWidth, step.BlurRegionHeight
	if width <= 0 || height <= 0 || x < 0 || y < 0 || x+width > 1 || y+height > 1 {
		x, y, width, height = defaultRenderBlurRegion()
	}
	strength := step.BlurStrength
	if strength < 1 || strength > maxRenderBlurStrength {
		strength = 8
	}
	return fmt.Sprintf("[0:v]split=2[kova_base][kova_crop];[kova_crop]crop=iw*%.4f:ih*%.4f:iw*%.4f:ih*%.4f,boxblur=luma_radius=%d:luma_power=1[kova_blur];[kova_base][kova_blur]overlay=main_w*%.4f:main_h*%.4f[kova_masked];[kova_masked]ass=%s[kova_subtitles]", width, height, x, y, strength, x, y, ass)
}

func renderSubtitleFile(ctx context.Context, req RenderVideoRequest) (string, error) {
	emitRenderProgress(req, "render_preflight", 0, "Checking FFmpeg and media tools.", time.Time{})
	// Keep direct callers safe as well as staged desktop renders. This is
	// idempotent and resolves the executable-relative build/bin tools when a
	// portable KOVA process has just started.
	if err := deps.EnsureDubbingMediaTools(); err != nil {
		return "", fmt.Errorf("renderSubtitleFile ffmpeg/ffprobe preflight error: %w", err)
	}
	emitRenderProgress(req, "render_preflight", 100, "FFmpeg and FFprobe are ready.", time.Time{})
	if err := os.MkdirAll(filepath.Dir(req.OutputFile), 0755); err != nil {
		return "", fmt.Errorf("renderSubtitleFile mkdir output dir error: %w", err)
	}

	assPath := renderAssPath(req)
	stepParam := req.StepParam
	if stepParam == nil {
		stepParam = &types.SubtitleTaskStepParam{TaskBasePath: req.Workdir}
		req.StepParam = stepParam
	}
	emitRenderProgress(req, "render_subtitle", 0, "Preparing video layout and subtitle canvas.", time.Time{})
	preparedReq, err := prepareSubtitleRenderLayout(req, getResolution, convertToVertical)
	if err != nil {
		return "", fmt.Errorf("renderSubtitleFile prepare subtitle layout error: %w", err)
	}
	req = preparedReq
	if err := srtToAss(req.SubtitleFile, assPath, req.Horizontal, req.StepParam); err != nil {
		return "", fmt.Errorf("renderSubtitleFile srtToAss error: %w", err)
	}
	emitRenderProgress(req, "render_subtitle", 100, "ASS subtitle file is ready.", time.Time{})
	args, _ := buildEmbedSubtitleArgs(req)
	duration, durationErr := mediaDuration(ctx, req.InputVideo)
	if durationErr != nil {
		// FFmpeg itself can still render a valid file when duration metadata is
		// missing. Keep rendering and expose an indeterminate encoding detail
		// rather than converting a progress-only problem into a failed job.
		duration = 0
	}
	emitRenderProgress(req, "render_encode", 0, "FFmpeg is starting video encoding.", time.Time{})
	output, err := runFFmpegWithProgress(ctx, args, duration, func(percent uint8, eta time.Time, detail string) {
		emitRenderProgress(req, "render_encode", percent, detail, eta)
	})
	if err != nil {
		return "", fmt.Errorf("renderSubtitleFile ffmpeg error: %w, output: %s", err, ffmpegErrorTail(output, 12))
	}
	emitRenderProgress(req, "render_encode", 100, "Video encoding completed.", time.Time{})
	info, err := os.Stat(req.OutputFile)
	if err != nil || info.IsDir() || info.Size() == 0 {
		if err == nil {
			err = fmt.Errorf("output video is empty")
		}
		return "", fmt.Errorf("renderSubtitleFile verify output error: %w", err)
	}
	emitRenderProgress(req, "render_verify", 100, fmt.Sprintf("Output verified (%d MiB).", info.Size()/(1024*1024)), time.Time{})
	return req.OutputFile, nil
}

func emitRenderProgress(req RenderVideoRequest, phase string, percent uint8, detail string, estimatedCompletionAt time.Time) {
	if req.Progress == nil {
		return
	}
	if percent > 100 {
		percent = 100
	}
	req.Progress(RenderProgress{
		Phase:                 phase,
		Percent:               percent,
		Detail:                detail,
		EstimatedCompletionAt: estimatedCompletionAt,
	})
}

func mediaDuration(ctx context.Context, input string) (time.Duration, error) {
	cmd := exec.CommandContext(ctx, storage.FfprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	)
	processutil.HideConsole(cmd)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("invalid media duration %q", strings.TrimSpace(string(output)))
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// runFFmpegWithProgress keeps stderr for useful failures while parsing
// FFmpeg's machine-readable -progress output on stdout. processutil hides the
// command window on Windows, so polling no longer creates black consoles.
func runFFmpegWithProgress(ctx context.Context, args []string, total time.Duration, report func(uint8, time.Time, string)) ([]byte, error) {
	commandArgs := append([]string{"-progress", "pipe:1", "-nostats"}, args...)
	cmd := exec.CommandContext(ctx, storage.FfmpegPath, commandArgs...)
	processutil.HideConsole(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return stderr.Bytes(), err
	}
	var encoded time.Duration
	lastPercent := -1
	lastReportedAt := time.Time{}
	reportProgress := func(force bool) {
		if total <= 0 || encoded <= 0 {
			if force && report != nil {
				report(0, time.Time{}, "FFmpeg is encoding; source duration is unavailable.")
			}
			return
		}
		percent := int((float64(encoded) / float64(total)) * 100)
		if percent < 0 {
			percent = 0
		}
		// 100% is only reported after FFmpeg exits and the output is verified.
		if percent > 99 {
			percent = 99
		}
		now := time.Now()
		if !force && percent == lastPercent && now.Sub(lastReportedAt) < time.Second {
			return
		}
		var eta time.Time
		if encoded < total && encoded > 0 {
			elapsed := now.Sub(startedAt)
			remaining := time.Duration(float64(elapsed) * (float64(total-encoded) / float64(encoded)))
			if remaining > 0 {
				eta = now.Add(remaining)
			}
		}
		if report != nil {
			report(uint8(percent), eta, fmt.Sprintf("Encoding %s / %s.", formatRenderDuration(encoded), formatRenderDuration(total)))
		}
		lastPercent = percent
		lastReportedAt = now
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us", "out_time_ms":
			microseconds, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr == nil && microseconds >= 0 {
				encoded = time.Duration(microseconds) * time.Microsecond
			}
		case "progress":
			reportProgress(value == "end")
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return stderr.Bytes(), scanErr
	}
	if waitErr != nil {
		return stderr.Bytes(), waitErr
	}
	reportProgress(true)
	return stderr.Bytes(), nil
}

func formatRenderDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	seconds := int(value.Round(time.Second).Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds%3600)/60, seconds%60)
}

// ffmpegErrorTail removes the verbose version/configuration banner from a
// failed render while preserving the lines that identify the actual filter,
// codec, or file error in the desktop UI.
func ffmpegErrorTail(output []byte, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	nonEmpty := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if maxLines <= 0 || len(nonEmpty) <= maxLines {
		return strings.Join(nonEmpty, "\n")
	}
	return strings.Join(nonEmpty[len(nonEmpty)-maxLines:], "\n")
}

type verticalConverter func(inputVideo, outputVideo, majorTitle, minorTitle string) error

func prepareSubtitleRenderLayout(req RenderVideoRequest, probe resolutionProbe, convert verticalConverter) (RenderVideoRequest, error) {
	if req.StepParam == nil {
		req.StepParam = &types.SubtitleTaskStepParam{TaskBasePath: req.Workdir}
	}
	width, height, err := probe(req.InputVideo)
	if err != nil {
		return req, fmt.Errorf("get resolution error: %w", err)
	}
	if !req.Horizontal {
		inputVideo, err := prepareRenderVideoInput(req, width, height, convert)
		if err != nil {
			return req, fmt.Errorf("prepare vertical input error: %w", err)
		}
		req.InputVideo = inputVideo
		if width > height {
			width, height = 720, 1280
		}
	}
	req.StepParam.RenderWidth = width
	req.StepParam.RenderHeight = height
	return req, nil
}

func prepareRenderVideoInput(req RenderVideoRequest, width, height int, convert verticalConverter) (string, error) {
	if req.Horizontal || width <= height {
		return req.InputVideo, nil
	}
	majorTitle, minorTitle := "", ""
	if req.StepParam != nil {
		majorTitle = req.StepParam.VerticalVideoMajorTitle
		minorTitle = req.StepParam.VerticalVideoMinorTitle
	}
	output := filepath.Join(req.Workdir, types.SubtitleTaskTransferredVerticalVideoFileName)
	if err := convert(req.InputVideo, output, majorTitle, minorTitle); err != nil {
		return "", err
	}
	if req.StepParam != nil {
		req.StepParam.InputVideoPath = output
	}
	return output, nil
}
