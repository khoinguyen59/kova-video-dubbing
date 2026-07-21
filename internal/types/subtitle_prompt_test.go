package types

import (
	"strings"
	"testing"
)

func TestKovaContextPromptRequiresTargetLanguageOnly(t *testing.T) {
	for _, required := range []string{
		"Kova",
		"entirely in the target language",
		"proper name",
		"Vietnamese",
		"Translation only",
	} {
		if !strings.Contains(SplitTextWithContextPrompt, required) {
			t.Fatalf("Kova context prompt missing %q", required)
		}
	}
}
