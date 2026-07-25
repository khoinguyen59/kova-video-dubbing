package dubbing

import (
	"math"
	"strings"
	"testing"
)

func TestBuildAtempoFilterChainsLargeSpeed(t *testing.T) {
	got, err := buildAtempoFilter(3.0)
	if err != nil {
		t.Fatalf("buildAtempoFilter(3) error = %v", err)
	}
	if got != "atempo=2.000,atempo=1.500" {
		t.Fatalf("buildAtempoFilter(3) = %q", got)
	}
}

func TestBuildAtempoFilterChainsSmallSpeed(t *testing.T) {
	got, err := buildAtempoFilter(0.25)
	if err != nil {
		t.Fatalf("buildAtempoFilter(0.25) error = %v", err)
	}
	if got != "atempo=0.500,atempo=0.500" {
		t.Fatalf("buildAtempoFilter(0.25) = %q", got)
	}
}

func TestBuildAtempoFilterRejectsInvalidSpeed(t *testing.T) {
	for _, speed := range []float64{0, -1, math.Inf(1), math.NaN()} {
		if got, err := buildAtempoFilter(speed); err == nil {
			t.Fatalf("buildAtempoFilter(%v) = %q, nil error", speed, got)
		}
	}
}

func TestBuildMuxArgsMapsVideoAndDubAudio(t *testing.T) {
	args := buildMuxArgs("input.mp4", "dub.wav", "out.mp4")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-map 0:v:0") || !strings.Contains(joined, "-map 1:a:0") {
		t.Fatalf("args should map original video and dub audio: %v", args)
	}
	if !strings.Contains(joined, "-shortest") {
		t.Fatalf("args should include -shortest: %v", args)
	}
	if !strings.Contains(joined, "-af apad") {
		t.Fatalf("args should pad a short dubbed track: %v", args)
	}
}

func TestBuildBackgroundMixArgsUsesSeparatedBackgroundNotSourceVideoAudio(t *testing.T) {
	args := buildBackgroundMixArgs("tts.wav", "source_background.wav", "mixed.wav", 0.38)
	joined := strings.Join(args, " ")
	for _, expected := range []string{"-i tts.wav", "-i source_background.wav", "amix=inputs=2", "volume=0.380", "-map [kova_mix]", "mixed.wav"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("background-mix args missing %q: %v", expected, args)
		}
	}
	if strings.Contains(joined, "input.mp4") {
		t.Fatalf("background mix must not reintroduce source-video audio: %v", args)
	}
}

func TestBuildSlotFitFilterPinsExactSubtitleDuration(t *testing.T) {
	got, err := buildSlotFitFilter(1.2, 2.5)
	if err != nil {
		t.Fatalf("buildSlotFitFilter() error = %v", err)
	}
	for _, want := range []string{"atempo=1.200", "apad=whole_dur=2.500", "atrim=duration=2.500"} {
		if !strings.Contains(got, want) {
			t.Fatalf("filter = %q, want %q", got, want)
		}
	}
}
