package service

import (
	"testing"

	"kova/internal/types"
)

func TestShouldDownloadStandaloneSourceAudio(t *testing.T) {
	tests := []struct {
		name string
		step *types.SubtitleTaskStepParam
		want bool
	}{
		{"nil", nil, false},
		{"legacy VTT only", &types.SubtitleTaskStepParam{VttSwitch: true, EmbedSubtitleVideoType: "none"}, false},
		{"source review MP4", &types.SubtitleTaskStepParam{VttSwitch: true, EmbedSubtitleVideoType: "horizontal"}, true},
		{"dubbing needs audio", &types.SubtitleTaskStepParam{VttSwitch: true, EmbedSubtitleVideoType: "none", EnableTts: true}, true},
		{"legacy non VTT", &types.SubtitleTaskStepParam{VttSwitch: false, EmbedSubtitleVideoType: "none"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldDownloadStandaloneSourceAudio(test.step); got != test.want {
				t.Fatalf("shouldDownloadStandaloneSourceAudio() = %v, want %v", got, test.want)
			}
		})
	}
}
