package service

import (
	"testing"

	"kova/pkg/util"
)

func TestProtectTermsPreservesLongerNamesAndRestoresCueText(t *testing.T) {
	terms := map[string]string{"Alice": "[[KOVA_PROPER_002]]", "Alice Nguyen": "[[KOVA_PROPER_001]]"}
	protected := protectTerms("Alice Nguyen met Alice.", terms)
	if protected != "[[KOVA_PROPER_001]] met [[KOVA_PROPER_002]]." {
		t.Fatalf("protected text = %q", protected)
	}
	items := []*TranslatedItem{{OriginText: protected, TranslatedText: "[[KOVA_PROPER_001]] đã gặp [[KOVA_PROPER_002]]."}}
	restoreProtectedTerms(items, terms)
	if items[0].TranslatedText != "Alice Nguyen đã gặp Alice." {
		t.Fatalf("restored translation = %q", items[0].TranslatedText)
	}
}

func TestProtectSrtBlockTermsRestoresNamesAfterVTTBatchTranslation(t *testing.T) {
	terms := map[string]string{"Alice Nguyen": "[[KOVA_PROPER_001]]"}
	blocks := []*util.SrtBlock{{OriginLanguageSentence: "Alice Nguyen tests the system."}}

	protectSrtBlockTerms(blocks, terms)
	if got := blocks[0].OriginLanguageSentence; got != "[[KOVA_PROPER_001]] tests the system." {
		t.Fatalf("protected VTT block = %q", got)
	}
	blocks[0].TargetLanguageSentence = "[[KOVA_PROPER_001]] kiểm tra hệ thống."
	restoreSrtBlockTerms(blocks, terms)
	if got := blocks[0].TargetLanguageSentence; got != "Alice Nguyen kiểm tra hệ thống." {
		t.Fatalf("restored VTT translation = %q", got)
	}
}
