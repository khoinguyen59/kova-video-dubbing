package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kova/internal/service/dubbing"
	"kova/internal/types"
)

func TestCollectVideoOutputsReturnsExistingDistinctMP4Artifacts(t *testing.T) {
	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("create output directory: %v", err)
	}

	horizontal := filepath.Join(outputDir, types.SubtitleTaskHorizontalEmbedVideoFileName)
	vertical := filepath.Join(outputDir, types.SubtitleTaskVerticalEmbedVideoFileName)
	dubbed := filepath.Join(tempDir, types.SubtitleTaskVideoWithTtsFileName)
	for _, path := range []string{horizontal, vertical, dubbed} {
		if err := os.WriteFile(path, []byte("mp4"), 0o644); err != nil {
			t.Fatalf("create artifact %s: %v", path, err)
		}
	}

	outputs := collectVideoOutputs(&types.SubtitleTaskStepParam{
		TaskId:               "desktop-output-test",
		TaskBasePath:         tempDir,
		VideoWithTtsFilePath: dubbed,
	})

	if len(outputs) != 3 {
		t.Fatalf("expected 3 video outputs, got %#v", outputs)
	}
	for index, expectedPath := range []string{horizontal, vertical, dubbed} {
		if outputs[index].TaskId != "desktop-output-test" {
			t.Errorf("output %d task id = %q", index, outputs[index].TaskId)
		}
		if outputs[index].Name != filepath.Base(expectedPath) {
			t.Errorf("output %d name = %q, want %q", index, outputs[index].Name, filepath.Base(expectedPath))
		}
		if outputs[index].DownloadUrl != artifactDownloadURL(expectedPath) {
			t.Errorf("output %d url = %q, want %q", index, outputs[index].DownloadUrl, artifactDownloadURL(expectedPath))
		}
		if !strings.HasPrefix(outputs[index].DownloadUrl, "/api/v1/files/") {
			t.Errorf("output %d must use v1 file route, got %q", index, outputs[index].DownloadUrl)
		}
	}

	// Dubbing can render over the horizontal output. It must still be exposed
	// once, not as duplicate save buttons in the desktop UI.
	outputs = collectVideoOutputs(&types.SubtitleTaskStepParam{
		TaskId:               "desktop-output-test",
		TaskBasePath:         tempDir,
		VideoWithTtsFilePath: horizontal,
	})
	if len(outputs) != 2 {
		t.Fatalf("expected duplicate horizontal output to be removed, got %#v", outputs)
	}
}

func TestCollectArtifactsReturnsCompleteOrderedManifest(t *testing.T) {
	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	dubbingDir := filepath.Join(tempDir, "dubbing")
	for _, dir := range []string{outputDir, dubbingDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	paths := []string{
		filepath.Join(tempDir, types.SubtitleTaskVideoFileName),
		filepath.Join(tempDir, types.SubtitleTaskAudioFileName),
		filepath.Join(tempDir, types.SubtitleTaskOriginLanguageSrtFileName),
		filepath.Join(tempDir, types.SubtitleTaskTargetLanguageSrtFileName),
		filepath.Join(tempDir, types.SubtitleTaskBilingualSrtFileName),
		filepath.Join(tempDir, types.TtsResultAudioFileName),
		filepath.Join(tempDir, types.SubtitleTaskVideoWithTtsFileName),
		filepath.Join(outputDir, types.SubtitleTaskHorizontalEmbedVideoFileName),
		filepath.Join(outputDir, types.SubtitleTaskVerticalEmbedVideoFileName),
		filepath.Join(dubbingDir, dubbing.DubSubtitleFileName),
		filepath.Join(dubbingDir, dubbing.DubbingReportName),
		filepath.Join(outputDir, types.SubtitleTaskTargetLanguageTextFileName),
		filepath.Join(outputDir, types.SubtitleTaskOriginLanguageTextFileName),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("artifact"), 0o644); err != nil {
			t.Fatalf("create artifact %s: %v", path, err)
		}
	}

	artifacts := collectArtifacts(&types.SubtitleTaskStepParam{
		TaskId:               "manifest-test",
		TaskBasePath:         tempDir,
		BilingualSrtFilePath: paths[4],
		TtsResultFilePath:    paths[5],
		VideoWithTtsFilePath: paths[6],
	})
	wantKinds := []string{
		"source_video", "source_srt", "source_text", "translated_srt", "dubbed_audio", "dubbed_video",
		"subtitled_horizontal_video", "subtitled_vertical_video", "source_audio",
		"bilingual_srt", "dubbing_srt", "dubbing_report", "translated_text",
	}
	wantPaths := map[string]string{
		"source_video":               paths[0],
		"source_audio":               paths[1],
		"source_srt":                 paths[2],
		"source_text":                paths[12],
		"translated_srt":             paths[3],
		"bilingual_srt":              paths[4],
		"dubbed_audio":               paths[5],
		"dubbed_video":               paths[6],
		"subtitled_horizontal_video": paths[7],
		"subtitled_vertical_video":   paths[8],
		"dubbing_srt":                paths[9],
		"dubbing_report":             paths[10],
		"translated_text":            paths[11],
	}
	if len(artifacts) != len(wantKinds) {
		t.Fatalf("artifact count = %d, want %d: %#v", len(artifacts), len(wantKinds), artifacts)
	}
	for index, kind := range wantKinds {
		artifact := artifacts[index]
		expectedPath := wantPaths[kind]
		if artifact.TaskId != "manifest-test" || artifact.Kind != kind {
			t.Errorf("artifact %d = %#v, want task manifest-test and kind %q", index, artifact, kind)
		}
		if artifact.Name != filepath.Base(expectedPath) {
			t.Errorf("artifact %d name = %q, want %q", index, artifact.Name, filepath.Base(expectedPath))
		}
		if artifact.DownloadUrl != artifactDownloadURL(expectedPath) {
			t.Errorf("artifact %d URL = %q, want %q", index, artifact.DownloadUrl, artifactDownloadURL(expectedPath))
		}
		if artifact.Label == "" || !strings.HasPrefix(artifact.DownloadUrl, "/api/v1/files/") {
			t.Errorf("artifact %d must have a label and v1 download URL: %#v", index, artifact)
		}
	}
}
