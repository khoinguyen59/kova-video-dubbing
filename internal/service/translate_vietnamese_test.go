package service

import (
	"testing"

	"kova/internal/types"
	"kova/pkg/util"
)

func TestNeedsVietnameseReview(t *testing.T) {
	tests := []struct {
		name       string
		origin     string
		translated string
		want       bool
	}{
		{"ordinary English remains", "Drink a beverage", "Uống beverage", true},
		{"fully Vietnamese", "Drink a beverage", "Uống một đồ uống", false},
		{"Vietnamese VTT labelled English with video", "Trong video này, tôi sẽ chia sẻ", "Trong video này, tôi sẽ chia sẻ", false},
		{"reported Vietnamese cue with video", "Trong video này, tôi sẽ chia sẻ với bạn kỹ thuật mà tôi dùng để cải thiện kỹ năng nghe.", "Trong video này, tôi sẽ chia sẻ với bạn kỹ thuật mà tôi dùng để cải thiện kỹ năng nghe.", false},
		{"reported Vietnamese cue with ordinary ASCII syllables", "First, let's talk about why most people fail to improve their listening.", "Đầu tiên, hãy nói về lý do tại sao hầu hết mọi người không cải thiện khả năng lắng nghe.", false},
		{"reported Vietnamese cue with so-sánh", "Let's compare this technique with watching movies.", "Hãy so sánh kỹ thuật này với việc xem phim.", false},
		{"reported Vietnamese cue with can đảm", "We must be brave and persevere.", "Chúng ta cũng phải phát triển sức mạnh nội tâm của chính mình, có can đảm để kiên trì vượt qua những", false},
		{"ambiguous English do remains outside Vietnamese context", "Do it now.", "Hãy do ngay.", true},
		{"ambiguous English can remains outside Vietnamese context", "Can you continue?", "Bạn can tiếp tục không?", true},
		{"ambiguous English so remains outside Vietnamese context", "So, let's begin.", "So, hãy bắt đầu.", true},
		{"ASCII loanword video in Vietnamese candidate", "Watch this video", "Hãy xem video này", false},
		{"English residue alongside video", "Watch this video", "Hãy watch video này", true},
		{"English residue cannot hide behind Vietnamese source", "Trong video này, tôi sẽ giải thích", "Trong video này, I will explain", true},
		{"English candidate", "Set up a statue", "Set up a statue", true},
		{"proper term alongside Vietnamese", "Use the OpenAI API", "Dùng API của OpenAI", false},
		{"URL alongside Vietnamese", "Open the URL", "Mở URL https://example.com/video", false},
		{"protected proper-name token", "[[KOVA_PROPER_001]] tests the system", "[[KOVA_PROPER_001]] kiểm tra hệ thống", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsVietnameseReview(tt.origin, tt.translated); got != tt.want {
				t.Fatalf("needsVietnameseReview(%q, %q) = %v, want %v", tt.origin, tt.translated, got, tt.want)
			}
		})
	}
}

func TestEnforceTargetLanguageLeavesSuspicionForUserReview(t *testing.T) {
	translator := &Translator{}

	got, err := translator.enforceTargetLanguage("Drink a beverage", "Uống beverage", types.LanguageNameEnglish, types.LanguageNameVietnamese)
	if err != nil {
		t.Fatalf("enforceTargetLanguage() error = %v", err)
	}
	if got != "Uống beverage" {
		t.Fatalf("enforceTargetLanguage() = %q", got)
	}
}

func TestTranslationReviewWarningsAreAdvisoryAndCueSpecific(t *testing.T) {
	blocks := []*util.SrtBlock{
		{Index: 80, OriginLanguageSentence: "A hooded raincoat or poncho keeps you warm and dry.", TargetLanguageSentence: "Một chiếc áo khoác mưa có mũ hoặc áo poncho sẽ giữ cho cơ thể bạn ấm áp và khô ráo."},
		{Index: 81, OriginLanguageSentence: "Drink a beverage.", TargetLanguageSentence: "Uống một đồ uống."},
	}
	warnings := translationReviewWarnings(blocks, "en", "vi")
	if len(warnings) != 1 {
		t.Fatalf("translationReviewWarnings() = %#v, want one warning", warnings)
	}
	if warnings[0].CueIndex != 80 || len(warnings[0].SuspiciousWords) != 1 || warnings[0].SuspiciousWords[0] != "poncho" {
		t.Fatalf("warning = %#v, want cue 80 / poncho", warnings[0])
	}
	if message := translationReviewMessage(warnings); message == "" || !containsVietnameseMarker(message) {
		t.Fatalf("translationReviewMessage() = %q, want non-empty Vietnamese review guidance", message)
	}
}

func TestEnforceTargetLanguagePreservesVietnameseVTTWithVideo(t *testing.T) {
	// The source-language metadata can say English even when YouTube's VTT cue
	// is already Vietnamese. This must not require an LLM round trip or fail
	// merely because "video" is an ASCII loanword.
	translator := &Translator{}
	cue := "Trong video này, tôi sẽ chia sẻ với bạn kỹ thuật mà tôi sử dụng để cải thiện kỹ năng nghe."
	got, err := translator.enforceTargetLanguage(cue, cue, types.LanguageNameEnglish, types.LanguageNameVietnamese)
	if err != nil {
		t.Fatalf("enforceTargetLanguage() error = %v", err)
	}
	if got != cue {
		t.Fatalf("enforceTargetLanguage() = %q, want %q", got, cue)
	}
}

func TestEnforceTargetLanguagePreservesVietnameseCanDamPhrase(t *testing.T) {
	// Regression for the exact quality-gate failure reported by the desktop:
	// “can” in “can đảm” is Vietnamese, not the English modal verb.
	translator := &Translator{}
	cue := "Chúng ta cũng phải phát triển sức mạnh nội tâm của chính mình, có can đảm để kiên trì vượt qua những"
	got, err := translator.enforceTargetLanguage("We must be brave and persevere.", cue, types.LanguageNameEnglish, types.LanguageNameVietnamese)
	if err != nil {
		t.Fatalf("enforceTargetLanguage() error = %v", err)
	}
	if got != cue {
		t.Fatalf("enforceTargetLanguage() = %q, want %q", got, cue)
	}
}

func TestIsTranslationValidRejectsResidualEnglish(t *testing.T) {
	translator := &Translator{}
	if translator.isTranslationValid("Drink a beverage", "Uống beverage", types.LanguageNameEnglish, types.LanguageNameVietnamese) {
		t.Fatal("isTranslationValid() accepted a subtitle with an ordinary English word")
	}
	if !translator.isTranslationValid("Trong video này, tôi sẽ chia sẻ", "Trong video này, tôi sẽ chia sẻ", types.LanguageNameEnglish, types.LanguageNameVietnamese) {
		t.Fatal("isTranslationValid() rejected a Vietnamese platform cue labelled English")
	}
	if translator.isTranslationValid("Trong video này, I will explain", "Trong video này, I will explain", types.LanguageNameEnglish, types.LanguageNameVietnamese) {
		t.Fatal("isTranslationValid() accepted residual English hidden in a Vietnamese VTT cue")
	}
}

func TestLatinWordsDoesNotSplitVietnameseDiacriticsIntoEnglishTokens(t *testing.T) {
	words := latinWords("Tôi uống một đồ uống và xem video")
	if len(words) != 2 || words[0] != "xem" || words[1] != "video" {
		t.Fatalf("latinWords() = %#v, want standalone ASCII Vietnamese/loanword tokens only", words)
	}
}
