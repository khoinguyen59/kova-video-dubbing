package service

import (
	"sort"
	"strings"

	"kova/pkg/util"
)

// protectTerms replaces approved proper names before an LLM sees a cue.  A
// longer name wins over a contained shorter name (for example “Alice Nguyen”
// before “Alice”), preventing partial substitutions.
func protectTerms(text string, terms map[string]string) string {
	if text == "" || len(terms) == 0 {
		return text
	}
	keys := make([]string, 0, len(terms))
	for term := range terms {
		if strings.TrimSpace(term) != "" {
			keys = append(keys, term)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len([]rune(keys[i])) > len([]rune(keys[j])) })
	replacements := make([]string, 0, len(keys)*2)
	for _, term := range keys {
		replacements = append(replacements, term, terms[term])
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

func restoreProtectedTerms(items []*TranslatedItem, terms map[string]string) {
	if len(items) == 0 || len(terms) == 0 {
		return
	}
	replacements := make([]string, 0, len(terms)*2)
	for original, token := range terms {
		replacements = append(replacements, token, original)
	}
	replacer := strings.NewReplacer(replacements...)
	for _, item := range items {
		if item == nil {
			continue
		}
		item.OriginText = replacer.Replace(item.OriginText)
		item.TranslatedText = replacer.Replace(item.TranslatedText)
	}
}

// protectSrtBlockTerms applies the same per-job proper-name policy to the
// YouTube VTT path. That path translates SRT blocks in batches rather than
// TranslatedItem values, so it needs its own small adapter to avoid allowing a
// name such as "Alice Nguyen" to be translated or left half-English.
func protectSrtBlockTerms(blocks []*util.SrtBlock, terms map[string]string) {
	if len(blocks) == 0 || len(terms) == 0 {
		return
	}
	for _, block := range blocks {
		if block == nil {
			continue
		}
		block.OriginLanguageSentence = protectTerms(block.OriginLanguageSentence, terms)
	}
}

func restoreSrtBlockTerms(blocks []*util.SrtBlock, terms map[string]string) {
	if len(blocks) == 0 || len(terms) == 0 {
		return
	}
	replacements := make([]string, 0, len(terms)*2)
	for original, token := range terms {
		replacements = append(replacements, token, original)
	}
	replacer := strings.NewReplacer(replacements...)
	for _, block := range blocks {
		if block == nil {
			continue
		}
		block.OriginLanguageSentence = replacer.Replace(block.OriginLanguageSentence)
		block.TargetLanguageSentence = replacer.Replace(block.TargetLanguageSentence)
	}
}
