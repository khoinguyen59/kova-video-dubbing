package service

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"kova/internal/storage"
)

func TestScoreBrowserMediaRejectsPageAssetsAndAcceptsVideo(t *testing.T) {
	if got := scoreBrowserMedia("https://example.com/app.js", "application/javascript", "Script"); got.Score != 0 {
		t.Fatalf("script was accepted as media: %+v", got)
	}
	got := scoreBrowserMedia("https://v9-dy-o-abtest.zjcdn.com/video/tos/cn/tos-cn.mp4", "video/mp4", "Media")
	if got.Score < 180 {
		t.Fatalf("Douyin MP4 media score = %d, want a high-confidence candidate", got.Score)
	}
	audioURL := "https://example.zjcdn.com/video/tos/cn/id/media-audio-und-mp4a/?mime_type=video_mp4"
	if got := scoreBrowserMedia(audioURL, "video/mp4", "Fetch"); got.Score != 0 {
		t.Fatalf("audio-only DASH stream was accepted as video: %+v", got)
	}
	if got := scoreBrowserAudio(audioURL, "video/mp4", "Fetch"); got.Score < 150 {
		t.Fatalf("Douyin audio DASH score = %d, want a high-confidence candidate", got.Score)
	}
}

func TestCopyMediaWithProgressReportsCompletion(t *testing.T) {
	payload := bytes.Repeat([]byte("kova"), 128*1024)
	var destination bytes.Buffer
	var finalPercent uint8
	written, err := copyMediaWithProgress(&destination, bytes.NewReader(payload), int64(len(payload)), 20, 90, func(percent uint8, _ string) {
		finalPercent = percent
	})
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(payload)) || !bytes.Equal(destination.Bytes(), payload) {
		t.Fatal("copied media does not match input")
	}
	if finalPercent != 90 {
		t.Fatalf("final progress = %d, want 90", finalPercent)
	}
}

func TestManagedShortVideoProfileIsOutsideTheProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KOVA_DATA_DIR", root)
	profile, err := managedShortVideoProfileDir("douyin", "edge")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "browser-sessions", "douyin", "edge")
	if profile != want {
		t.Fatalf("profile = %q, want %q", profile, want)
	}
}

// This test is deliberately opt-in because it uses the live public Douyin
// page and a locally installed Chromium browser. It is run before release
// against the exact failing user URL, but normal unit tests stay deterministic.
func TestDouyinManagedBrowserLive(t *testing.T) {
	sourceURL := os.Getenv("KOVA_DOUYIN_E2E_URL")
	if sourceURL == "" {
		t.Skip("set KOVA_DOUYIN_E2E_URL to run the live resolver test")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("ffmpeg is required for the live resolver test")
	}
	storage.FfmpegPath = ffmpeg
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	output := filepath.Join(t.TempDir(), "douyin.mp4")
	if err := downloadDouyinSourceVideo(ctx, sourceURL, output, "auto", nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 64*1024 {
		t.Fatalf("downloaded file is only %d bytes", info.Size())
	}
	if ffprobe, lookupErr := exec.LookPath("ffprobe"); lookupErr == nil {
		for _, stream := range []string{"v:0", "a:0"} {
			command := exec.CommandContext(ctx, ffprobe,
				"-v", "error",
				"-select_streams", stream,
				"-show_entries", "stream=codec_type",
				"-of", "default=noprint_wrappers=1:nokey=1",
				output,
			)
			value, probeErr := command.Output()
			if probeErr != nil || len(value) == 0 {
				t.Fatalf("downloaded Douyin file is missing stream %s: %v", stream, probeErr)
			}
		}
	}
}
