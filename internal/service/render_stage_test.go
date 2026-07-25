package service

import (
	"kova/internal/types"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildEmbedSubtitleArgsUsesRequestedSubtitleAndOutput(t *testing.T) {
	req := RenderVideoRequest{
		Workdir:      "tasks/demo",
		InputVideo:   "tasks/demo/origin_video.mp4",
		SubtitleFile: "tasks/demo/target_language_srt.srt",
		OutputFile:   "tasks/demo/output/horizontal_dubbed.mp4",
		Horizontal:   true,
	}
	args, assPath := buildEmbedSubtitleArgs(req)
	joined := strings.Join(args, " ")
	if !strings.Contains(assPath, filepath.Join("tasks", "demo")) {
		t.Fatalf("assPath = %q does not use workdir", assPath)
	}
	if !strings.Contains(joined, "tasks/demo/origin_video.mp4") {
		t.Fatalf("args do not contain input video: %v", args)
	}
	if !strings.Contains(joined, "tasks/demo/output/horizontal_dubbed.mp4") {
		t.Fatalf("args do not contain output file: %v", args)
	}
}

func TestBuildEmbedSubtitleArgsCanBlurOldCaptionBandBeforeASS(t *testing.T) {
	req := RenderVideoRequest{
		Workdir:      "tasks/demo",
		InputVideo:   "tasks/demo/origin_video.mp4",
		SubtitleFile: "tasks/demo/target_language_srt.srt",
		OutputFile:   "tasks/demo/output/final.mp4",
		Horizontal:   true,
		StepParam: &types.SubtitleTaskStepParam{
			BlurOriginalText: true,
			BlurRegionX:      0.10,
			BlurRegionY:      0.70,
			BlurRegionWidth:  0.80,
			BlurRegionHeight: 0.20,
			BlurStrength:     maxRenderBlurStrength,
		},
	}
	args, _ := buildEmbedSubtitleArgs(req)
	joined := strings.Join(args, " ")
	for _, expected := range []string{"-filter_complex", "boxblur=luma_radius=11", "[kova_subtitles]", "-map 0:a?", "ass="} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("blurred render args missing %q: %s", expected, joined)
		}
	}
	filterIndex, codecIndex := -1, -1
	for index, arg := range args {
		if arg == "-filter_complex" {
			filterIndex = index
		}
		if arg == "-c:v" {
			codecIndex = index
		}
	}
	if filterIndex < 0 || codecIndex < 0 || filterIndex > codecIndex {
		t.Fatalf("filter must be specified before codec/output options, args=%q", args)
	}
}

func TestFFmpegErrorTailDropsVerboseBanner(t *testing.T) {
	output := []byte("version banner\nconfiguration banner\ninput details\n[Parsed_ass] bad path\nError opening output files: Invalid argument\n")
	got := ffmpegErrorTail(output, 2)
	if strings.Contains(got, "version banner") || !strings.Contains(got, "bad path") || !strings.Contains(got, "Invalid argument") {
		t.Fatalf("ffmpegErrorTail() = %q", got)
	}
}

func TestRenderAssPathDerivesFromOutputFile(t *testing.T) {
	req := RenderVideoRequest{
		Workdir:    "tasks/demo",
		OutputFile: "tasks/demo/output/horizontal_dubbed.mp4",
	}

	got := renderAssPath(req)
	want := filepath.Join("tasks", "demo", "formatted_horizontal_dubbed.ass")
	if got != want {
		t.Fatalf("renderAssPath() = %q, want %q", got, want)
	}
	if strings.Contains(got, "formatted_subtitles.ass") {
		t.Fatalf("renderAssPath() still uses fixed subtitle name: %q", got)
	}
}

func TestEscapeAssFilterPathEscapesWindowsDriveAndSeparators(t *testing.T) {
	got := escapeAssFilterPath(`C:\tasks\demo\formatted_horizontal_dubbed.ass`)
	want := `C\:/tasks/demo/formatted_horizontal_dubbed.ass`
	if got != want {
		t.Fatalf("escapeAssFilterPath() = %q, want %q", got, want)
	}
}

func TestPrepareRenderVideoInputConvertsHorizontalVerticalRequest(t *testing.T) {
	workdir := filepath.Join("tasks", "demo")
	req := RenderVideoRequest{
		Workdir:    workdir,
		InputVideo: filepath.Join(workdir, "origin_video.mp4"),
		Horizontal: false,
		StepParam: &types.SubtitleTaskStepParam{
			TaskBasePath: workdir,
		},
	}

	got, err := prepareRenderVideoInput(req, 1280, 720, func(input, output, majorTitle, minorTitle string) error {
		if input != req.InputVideo {
			t.Fatalf("convert input = %q, want %q", input, req.InputVideo)
		}
		wantOutput := filepath.Join(workdir, types.SubtitleTaskTransferredVerticalVideoFileName)
		if output != wantOutput {
			t.Fatalf("convert output = %q, want %q", output, wantOutput)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prepareRenderVideoInput() error = %v", err)
	}
	want := filepath.Join(workdir, types.SubtitleTaskTransferredVerticalVideoFileName)
	if got != want {
		t.Fatalf("prepareRenderVideoInput() = %q, want %q", got, want)
	}
	if req.StepParam.InputVideoPath != want {
		t.Fatalf("StepParam.InputVideoPath = %q, want %q", req.StepParam.InputVideoPath, want)
	}
}

func TestPrepareSubtitleRenderLayoutSetsHorizontalDimensions(t *testing.T) {
	req := RenderVideoRequest{
		InputVideo: "tasks/demo/origin_video.mp4",
		Horizontal: true,
		StepParam:  &types.SubtitleTaskStepParam{},
	}

	got, err := prepareSubtitleRenderLayout(req, func(input string) (int, int, error) {
		if input != req.InputVideo {
			t.Fatalf("probe input = %q, want %q", input, req.InputVideo)
		}
		return 1920, 1080, nil
	}, func(input, output, majorTitle, minorTitle string) error {
		t.Fatal("horizontal render should not convert video")
		return nil
	})
	if err != nil {
		t.Fatalf("prepareSubtitleRenderLayout() error = %v", err)
	}
	if got.StepParam.RenderWidth != 1920 || got.StepParam.RenderHeight != 1080 {
		t.Fatalf("Render dimensions = %dx%d, want 1920x1080", got.StepParam.RenderWidth, got.StepParam.RenderHeight)
	}
	if got.InputVideo != req.InputVideo {
		t.Fatalf("InputVideo = %q, want %q", got.InputVideo, req.InputVideo)
	}
}

func TestPrepareSubtitleRenderLayoutSetsConvertedVerticalDimensions(t *testing.T) {
	workdir := filepath.Join("tasks", "demo")
	req := RenderVideoRequest{
		Workdir:    workdir,
		InputVideo: filepath.Join(workdir, "origin_video.mp4"),
		Horizontal: false,
		StepParam:  &types.SubtitleTaskStepParam{TaskBasePath: workdir},
	}

	got, err := prepareSubtitleRenderLayout(req, func(input string) (int, int, error) {
		return 1280, 720, nil
	}, func(input, output, majorTitle, minorTitle string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("prepareSubtitleRenderLayout() error = %v", err)
	}
	wantInput := filepath.Join(workdir, types.SubtitleTaskTransferredVerticalVideoFileName)
	if got.InputVideo != wantInput {
		t.Fatalf("InputVideo = %q, want converted vertical path %q", got.InputVideo, wantInput)
	}
	if got.StepParam.RenderWidth != 720 || got.StepParam.RenderHeight != 1280 {
		t.Fatalf("Render dimensions = %dx%d, want 720x1280", got.StepParam.RenderWidth, got.StepParam.RenderHeight)
	}
}

func TestGetFontPathsUsesChineseCapableFontsOnDarwin(t *testing.T) {
	bold, regular, err := fontPathsForOS("darwin", func(path string) bool {
		return strings.Contains(path, "Hiragino Sans GB")
	})
	if err != nil {
		t.Fatalf("fontPathsForOS() error = %v", err)
	}
	if !strings.Contains(bold, "Arial Unicode") && !strings.Contains(bold, "Hiragino") && !strings.Contains(bold, "Heiti") {
		t.Fatalf("bold font %q does not look Chinese-capable", bold)
	}
	if !strings.Contains(regular, "Arial Unicode") && !strings.Contains(regular, "Hiragino") && !strings.Contains(regular, "Heiti") {
		t.Fatalf("regular font %q does not look Chinese-capable", regular)
	}
}

func TestGetFontPathsUsesChineseCapableFontsOnWindows(t *testing.T) {
	bold, regular, err := fontPathsForOS("windows", func(path string) bool {
		return strings.Contains(path, "msyh")
	})
	if err != nil {
		t.Fatalf("fontPathsForOS() error = %v", err)
	}
	if !strings.Contains(bold, "msyh") || !strings.Contains(regular, "msyh") {
		t.Fatalf("windows fonts = %q, %q; want Microsoft YaHei candidates", bold, regular)
	}
}

func TestBuildVerticalFilterEscapesTitleTextAndUsesCompactHeader(t *testing.T) {
	filter := buildVerticalFilter("CLI 集成测试: A's", "副标题", "/fonts/chinese.ttf", "/fonts/chinese.ttf")
	if !strings.Contains(filter, "drawbox=y=0:h=250") {
		t.Fatalf("filter should use compact 250px title header: %s", filter)
	}
	if !strings.Contains(filter, "fontsize=44") {
		t.Fatalf("filter should use smaller title font size: %s", filter)
	}
	if !strings.Contains(filter, `CLI 集成测试\: A\\'s`) {
		t.Fatalf("filter did not escape title text safely: %s", filter)
	}
}
