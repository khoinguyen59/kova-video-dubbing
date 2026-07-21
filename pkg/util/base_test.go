package util

import "testing"

func TestGetYouTubeIDSupportsShortURLs(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{url: "https://youtu.be/uDVoZ39mONk?si=rcNR58geRVDCR_3t", want: "uDVoZ39mONk"},
		{url: "https://www.youtube.com/watch?v=uDVoZ39mONk", want: "uDVoZ39mONk"},
		{url: "https://m.youtube.com/embed/uDVoZ39mONk", want: "uDVoZ39mONk"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := GetYouTubeID(tt.url)
			if err != nil {
				t.Fatalf("GetYouTubeID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("GetYouTubeID() = %q, want %q", got, tt.want)
			}
			if !IsYouTubeURL(tt.url) {
				t.Fatalf("IsYouTubeURL(%q) = false", tt.url)
			}
		})
	}
}

func TestIsYouTubeURLRejectsLookalikesAndMissingVideoID(t *testing.T) {
	for _, rawURL := range []string{
		"https://youtube.com.evil.example/watch?v=uDVoZ39mONk",
		"https://example.com/youtube.com/watch?v=uDVoZ39mONk",
		"https://youtu.be/",
	} {
		if rawURL == "https://youtu.be/" {
			if !IsYouTubeURL(rawURL) {
				t.Fatalf("short YouTube host should be recognized for validation")
			}
			if _, err := GetYouTubeID(rawURL); err == nil {
				t.Fatalf("GetYouTubeID(%q) error = nil", rawURL)
			}
			continue
		}
		if IsYouTubeURL(rawURL) {
			t.Fatalf("IsYouTubeURL(%q) = true", rawURL)
		}
	}
}
