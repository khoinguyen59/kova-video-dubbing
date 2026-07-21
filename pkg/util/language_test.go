package util

import "testing"

func TestMapLanguageForYouTubeUsesAllForAutoDetection(t *testing.T) {
	if got := MapLanguageForYouTube("auto"); got != "all" {
		t.Fatalf("MapLanguageForYouTube(auto) = %q, want all", got)
	}
}
