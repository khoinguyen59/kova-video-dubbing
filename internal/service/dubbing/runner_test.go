package dubbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRunnerWritingOutputs(dir string) CommandRunner {
	return func(args []string) error {
		out := args[len(args)-1]
		if strings.HasSuffix(out, ".wav") || strings.HasSuffix(out, ".mp4") {
			return os.WriteFile(out, []byte("media"), 0644)
		}
		return nil
	}
}

func fakeRunnerWritingOutputsWithoutMkdir() CommandRunner {
	return func(args []string) error {
		out := args[len(args)-1]
		if strings.HasSuffix(out, ".wav") || strings.HasSuffix(out, ".mp4") {
			return os.WriteFile(out, []byte("media"), 0644)
		}
		return nil
	}
}

func TestRunWritesDubbingArtifactsWithFakeTTS(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	video := filepath.Join(dir, "origin.mp4")
	if err := os.WriteFile(input, []byte("1\n00:00:00,000 --> 00:00:01,000\n你好\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "zh_cn",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		InputVideo:  video,
		OutputAudio: filepath.Join(dir, "tts_final_audio.wav"),
		OutputVideo: filepath.Join(dir, "video_with_tts.mp4"),
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputs(dir),
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}
	result, err := NewRunner(deps).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(dir, DubbingDirName, DubbingInputFileName),
		filepath.Join(dir, DubbingDirName, DubbingPlanFileName),
		filepath.Join(dir, DubbingDirName, DubbingReportName),
		filepath.Join(dir, DubbingDirName, DubSubtitleFileName),
		result.Audio,
		result.Video,
	} {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("missing output %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestRunWritesCleanedDubbingInput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	video := filepath.Join(dir, "origin.mp4")
	if err := os.WriteFile(input, []byte("1\n00:00:00,000 --> 00:00:01,000\n(music) 你好 ™\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "zh_cn",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		InputVideo:  video,
		OutputAudio: filepath.Join(dir, "tts_final_audio.wav"),
		OutputVideo: filepath.Join(dir, "video_with_tts.mp4"),
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputs(dir),
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}
	if _, err := NewRunner(deps).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, DubbingDirName, DubbingInputFileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "music") || strings.Contains(text, "™") {
		t.Fatalf("dubbing input contains unclean text: %q", text)
	}
	if !strings.Contains(text, "你好") {
		t.Fatalf("dubbing input missing speech text: %q", text)
	}
}

func TestRunWithDurationAwareTTSKeepsSubtitleTimingAndDoesNotChunk(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	video := filepath.Join(dir, "origin.mp4")
	if err := os.WriteFile(input, []byte("1\n00:00:01,000 --> 00:00:02,000\nXin chào\n\n2\n00:00:03,000 --> 00:00:04,500\nTạm biệt\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	tts := &durationAwareFakeTTS{fakeTTS: fakeTTS{writeOnReturn: true}}
	result, err := NewRunner(Dependencies{
		TTS:         tts,
		Language:    "vi",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		InputVideo:  video,
		OutputAudio: filepath.Join(dir, "tts_final_audio.wav"),
		OutputVideo: filepath.Join(dir, "video_with_tts.mp4"),
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputs(dir),
		Duration: func(string) (float64, error) {
			return 1, nil
		},
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(tts.durations) != 2 || tts.durations[0] != 1 || tts.durations[1] != 1.5 {
		t.Fatalf("duration requests = %+v, want [1 1.5]", tts.durations)
	}
	if len(result.Plan) != 2 || result.Plan[0].NewStart != 1 || result.Plan[0].NewEnd != 2 || result.Plan[1].NewStart != 3 || result.Plan[1].NewEnd != 4.5 {
		t.Fatalf("result plan = %+v, want original SRT slots", result.Plan)
	}
	if result.Plan[0].SpeedFactor != 1 || result.Plan[1].SpeedFactor != DefaultConfig().SpeedMin {
		t.Fatalf("speed factors = %.3f %.3f, want natural 1.0 and minimum %.2f", result.Plan[0].SpeedFactor, result.Plan[1].SpeedFactor, DefaultConfig().SpeedMin)
	}
	if !warningsContain(result.Report.Warnings, "padding the remaining slot with silence") {
		t.Fatalf("warnings = %+v, want short-cue padding warning", result.Report.Warnings)
	}
	if len(result.Chunks) != 0 {
		t.Fatalf("duration-aware run should not merge chunks: %+v", result.Chunks)
	}
	for _, path := range []string{filepath.Join(dir, DubbingDirName, "segments", "raw", "1.wav"), filepath.Join(dir, DubbingDirName, "segments", "raw", "2.wav")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing per-subtitle raw output %s: %v", path, err)
		}
	}
}

func TestRunRejectsEmptySRTBeforeWritingArtifacts(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	video := filepath.Join(dir, "origin.mp4")
	if err := os.WriteFile(input, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "zh_cn",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		InputVideo:  video,
		OutputAudio: filepath.Join(dir, "tts_final_audio.wav"),
		OutputVideo: filepath.Join(dir, "video_with_tts.mp4"),
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputs(dir),
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}

	_, err := NewRunner(deps).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "input srt has no cues") {
		t.Fatalf("Run() error = %v, want no cues error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, DubbingDirName, DubbingInputFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dubbing input stat error = %v, want not exist", statErr)
	}
}

func TestRunCreatesCustomOutputParentDirs(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	video := filepath.Join(dir, "origin.mp4")
	outputAudio := filepath.Join(dir, "nested", "audio", "tts_final_audio.wav")
	outputVideo := filepath.Join(dir, "nested", "video", "video_with_tts.mp4")
	if err := os.WriteFile(input, []byte("1\n00:00:00,000 --> 00:00:01,000\n你好\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "zh_cn",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		InputVideo:  video,
		OutputAudio: outputAudio,
		OutputVideo: outputVideo,
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputsWithoutMkdir(),
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}

	result, err := NewRunner(deps).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, path := range []string{result.Audio, result.Video} {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("missing output %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestRunFailsWhenMuxDoesNotCreateOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	video := filepath.Join(dir, "origin.mp4")
	if err := os.WriteFile(input, []byte("1\n00:00:00,000 --> 00:00:01,000\n你好\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "zh_cn",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		InputVideo:  video,
		OutputAudio: filepath.Join(dir, "tts_final_audio.wav"),
		OutputVideo: filepath.Join(dir, "video_with_tts.mp4"),
		Config:      DefaultConfig(),
		FFmpeg: func(args []string) error {
			out := args[len(args)-1]
			if strings.HasSuffix(out, ".wav") {
				return os.WriteFile(out, []byte("media"), 0644)
			}
			return nil
		},
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}
	_, err := NewRunner(deps).Run(context.Background())
	if err == nil {
		t.Fatalf("Run() error = nil, want missing mux output error")
	}
}

func TestRunnerRequiresInputVideoForMux(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	if err := os.WriteFile(input, []byte("1\n00:00:00,000 --> 00:00:01,000\n你好\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "zh_cn",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		OutputAudio: filepath.Join(dir, "tts_final_audio.wav"),
		OutputVideo: filepath.Join(dir, "video_with_tts.mp4"),
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputs(dir),
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}
	_, err := NewRunner(deps).Run(context.Background())
	if err == nil {
		t.Fatalf("Run() error = nil, want missing input video error")
	}
}

func TestSynthesizeCreatesAudioWithoutMuxingVideo(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.srt")
	outputAudio := filepath.Join(dir, "tts_final_audio.wav")
	outputVideo := filepath.Join(dir, "video_with_tts.mp4")
	if err := os.WriteFile(input, []byte("1\n00:00:00,000 --> 00:00:01,000\nXin chào\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := NewRunner(Dependencies{
		TTS:         &fakeTTS{writeOnReturn: true},
		Language:    "vi",
		Voice:       "voice",
		Workdir:     dir,
		InputSRT:    input,
		OutputAudio: outputAudio,
		OutputVideo: outputVideo,
		Config:      DefaultConfig(),
		FFmpeg:      fakeRunnerWritingOutputs(dir),
		Duration: func(string) (float64, error) {
			return 0.8, nil
		},
	}).Synthesize(context.Background())
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if result.Audio != outputAudio {
		t.Fatalf("audio = %q, want %q", result.Audio, outputAudio)
	}
	if _, err := os.Stat(outputAudio); err != nil {
		t.Fatalf("audio artifact missing: %v", err)
	}
	if _, err := os.Stat(outputVideo); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Synthesize unexpectedly muxed a video: %v", err)
	}
}

func TestMuxUsesApprovedAudioWithoutTTS(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "origin.mp4")
	audio := filepath.Join(dir, "tts_final_audio.wav")
	output := filepath.Join(dir, "video_with_tts.mp4")
	for path, data := range map[string]string{video: "video", audio: "audio"} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := NewRunner(Dependencies{
		Workdir:     dir,
		InputVideo:  video,
		OutputAudio: audio,
		OutputVideo: output,
		FFmpeg:      fakeRunnerWritingOutputs(dir),
	}).Mux()
	if err != nil {
		t.Fatalf("Mux() error = %v", err)
	}
	if got != output {
		t.Fatalf("Mux() = %q, want %q", got, output)
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("muxed video missing: info=%v err=%v", info, err)
	}
}
