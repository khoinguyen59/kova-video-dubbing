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

func TestIsSupportedVideoURLAcceptsDesktopPlatformsAndRejectsLookalikes(t *testing.T) {
	for _, rawURL := range []string{
		"https://www.youtube.com/watch?v=uDVoZ39mONk",
		"https://vm.tiktok.com/ZM123example/",
		"https://www.tiktok.com/@kova/video/1234567890",
		"https://v.douyin.com/abcdef/",
		"https://www.douyin.com/video/1234567890",
		"https://www.bilibili.com/video/BV1xx411c7mD",
	} {
		if !IsSupportedVideoURL(rawURL) {
			t.Fatalf("IsSupportedVideoURL(%q) = false", rawURL)
		}
	}

	for _, rawURL := range []string{
		"https://tiktok.com.evil.example/video/123",
		"https://example.com/tiktok.com/video/123",
		"ftp://www.tiktok.com/video/123",
		"not a URL",
	} {
		if IsSupportedVideoURL(rawURL) {
			t.Fatalf("IsSupportedVideoURL(%q) = true", rawURL)
		}
	}
}
