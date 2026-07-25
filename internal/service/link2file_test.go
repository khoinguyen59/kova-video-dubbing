package service

import (
	"reflect"
	"strings"
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

func TestShortVideoSessionBrowsersNeverReferenceTheUserBrowserProfile(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		want       []string
	}{
		{
			name:       "auto creates fresh Edge then Chrome sessions",
			configured: "auto",
			want:       []string{"edge", "chrome"},
		},
		{
			name:       "configured Edge uses an isolated Edge session",
			configured: "edge",
			want:       []string{"edge"},
		},
		{
			name:       "temporary browser session explicitly disabled",
			configured: "none",
			want:       nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shortVideoSessionBrowsers(test.configured); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("shortVideoSessionBrowsers() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestShortVideoSessionUserAgentMatchesSelectedTemporaryBrowser(t *testing.T) {
	if got := shortVideoSessionUserAgent("chrome"); !strings.Contains(got, "Chrome/") || strings.Contains(got, " Edg/") {
		t.Fatalf("chrome temporary session must use a Chrome User-Agent, got %q", got)
	}
	if got := shortVideoSessionUserAgent("C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe"); !strings.Contains(got, " Edg/") {
		t.Fatalf("edge temporary session must use an Edge User-Agent, got %q", got)
	}
}

func TestYtDlpRequestsFreshCookies(t *testing.T) {
	if !ytDlpRequestsFreshCookies([]byte("ERROR: [Douyin] Fresh cookies (not necessarily logged in) are needed")) {
		t.Fatal("expected fresh-cookie diagnostic to trigger browser fallback")
	}
	if ytDlpRequestsFreshCookies([]byte("ERROR: network timeout")) {
		t.Fatal("network error must not trigger a browser-cookie retry")
	}
}

func TestBrowserPreviewVideoFormatPrefersAVCMP4(t *testing.T) {
	format := browserPreviewVideoFormat()
	if !strings.HasPrefix(format, "bestvideo[vcodec^=avc1][height<=1080][ext=mp4]") {
		t.Fatalf("browserPreviewVideoFormat() = %q, want AVC MP4 preferred", format)
	}
	if !strings.Contains(format, "/bestvideo[height<=1080][ext=mp4]") {
		t.Fatalf("browserPreviewVideoFormat() = %q, want compatibility fallback", format)
	}
}
