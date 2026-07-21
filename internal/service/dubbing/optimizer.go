package dubbing

import (
	"context"
	"fmt"
	"strings"

	"kova/internal/types"
)

type LLMOptimizer struct {
	chat types.ChatCompleter
}

func NewLLMOptimizer(chat types.ChatCompleter) *LLMOptimizer {
	return &LLMOptimizer{chat: chat}
}

// Optimize is retained for older callers. New planner code calls
// OptimizeForLanguage so the model is explicitly told which language must be
// emitted instead of relying on a generic "target language" instruction.
func (o *LLMOptimizer) Optimize(ctx context.Context, text string, availableSeconds float64, reason string) (string, error) {
	return o.OptimizeForLanguage(ctx, text, availableSeconds, reason, "")
}

func (o *LLMOptimizer) OptimizeForLanguage(ctx context.Context, text string, availableSeconds float64, reason string, language types.StandardLanguageCode) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if o == nil || o.chat == nil {
		return text, nil
	}
	targetLanguage := "the original target language"
	if language != "" {
		targetLanguage = types.GetStandardLanguageName(language)
	}
	prompt := fmt.Sprintf(`Rewrite the subtitle below into a shorter, natural spoken sentence in %s.
Rules:
1. Preserve the core meaning and add no new facts.
2. Output only one line in %s; do not explain the rewrite.
3. Translate every ordinary word into %s. Keep only genuine proper names, brands, acronyms, code, URLs, or numbers unchanged.
4. Aim for natural narration within %.1f seconds.
Reason: %s

Subtitle:
%s`, targetLanguage, targetLanguage, targetLanguage, availableSeconds, reason, text)
	resp, err := o.chat.ChatCompletion(prompt)
	if err != nil {
		return "", err
	}
	resp = strings.TrimSpace(resp)
	resp = strings.ReplaceAll(resp, "\r", " ")
	resp = strings.ReplaceAll(resp, "\n", " ")
	resp = strings.Join(strings.Fields(resp), " ")
	if resp == "" {
		return text, nil
	}
	return resp, nil
}
