package voices

import (
	"fmt"
	"sort"
	"strings"

	"kova/internal/pipeline"
)

const (
	ProviderAliyun    = "aliyun"
	ProviderOpenAI    = "openai"
	ProviderEdge      = "edge-tts"
	ProviderMinimax   = "minimax"
	ProviderOmniVoice = "omnivoice"
	ProviderGateway   = "gateway"
	// Minimax is kept as an alias for callers compiled against the older name.
	Minimax = ProviderMinimax
)

func List(provider string) ([]pipeline.Voice, error) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	switch provider {
	case ProviderAliyun:
		return cloneVoices(aliyunVoices), nil
	case ProviderOpenAI:
		return cloneVoices(openaiVoices), nil
	case ProviderMinimax:
		return cloneVoices(minimaxVoices), nil
	case ProviderOmniVoice:
		return cloneVoices(omnivoiceVoices), nil
	case ProviderGateway:
		return cloneVoices(gatewayVoices), nil
	case ProviderEdge:
		return nil, fmt.Errorf("edge-tts voice listing is not supported yet; use edge-tts --list-voices")
	default:
		return nil, fmt.Errorf("unsupported tts provider: %s", provider)
	}
}

func Providers() []string {
	return []string{ProviderAliyun, ProviderOpenAI, ProviderMinimax, ProviderOmniVoice, ProviderGateway, ProviderEdge}
}

func cloneVoices(in []pipeline.Voice) []pipeline.Voice {
	out := append([]pipeline.Voice(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Code < out[j].Code
	})
	return out
}

var aliyunVoices = []pipeline.Voice{
	{Provider: ProviderAliyun, Code: "longxiaochun_v2", Name: "龙小淳", Language: "zh-CN", Gender: "female", Scenario: "General female voice"},
	{Provider: ProviderAliyun, Code: "longxiaoxia_v2", Name: "龙小夏", Language: "zh-CN", Gender: "female", Scenario: "Natural female voice"},
	{Provider: ProviderAliyun, Code: "longxiaocheng_v2", Name: "龙小诚", Language: "zh-CN", Gender: "male", Scenario: "General male voice"},
	{Provider: ProviderAliyun, Code: "longxiaobai_v2", Name: "龙小白", Language: "zh-CN", Gender: "female", Scenario: "Sweet female voice"},
	{Provider: ProviderAliyun, Code: "longlaotie_v2", Name: "龙老铁", Language: "zh-CN", Gender: "male", Scenario: "Northeastern accent"},
	{Provider: ProviderAliyun, Code: "longshu_v2", Name: "龙叔", Language: "zh-CN", Gender: "male", Scenario: "Steady male voice"},
	{Provider: ProviderAliyun, Code: "longshuo_v2", Name: "龙硕", Language: "zh-CN", Gender: "male", Scenario: "Reading voice"},
	{Provider: ProviderAliyun, Code: "longjing_v2", Name: "龙婧", Language: "zh-CN", Gender: "female", Scenario: "News voice"},
	{Provider: ProviderAliyun, Code: "longmiao_v2", Name: "龙妙", Language: "zh-CN", Gender: "female", Scenario: "Customer-service voice"},
	{Provider: ProviderAliyun, Code: "longyue_v2", Name: "龙悦", Language: "zh-CN", Gender: "female", Scenario: "Gentle female voice"},
}

var openaiVoices = []pipeline.Voice{
	{Provider: ProviderOpenAI, Code: "alloy", Language: "multi", Scenario: "balanced"},
	{Provider: ProviderOpenAI, Code: "ash", Language: "multi", Scenario: "calm"},
	{Provider: ProviderOpenAI, Code: "ballad", Language: "multi", Scenario: "expressive"},
	{Provider: ProviderOpenAI, Code: "coral", Language: "multi", Scenario: "warm"},
	{Provider: ProviderOpenAI, Code: "echo", Language: "multi", Scenario: "clear"},
	{Provider: ProviderOpenAI, Code: "fable", Language: "multi", Scenario: "narration"},
	{Provider: ProviderOpenAI, Code: "nova", Language: "multi", Scenario: "bright"},
	{Provider: ProviderOpenAI, Code: "onyx", Language: "multi", Scenario: "deep"},
	{Provider: ProviderOpenAI, Code: "sage", Language: "multi", Scenario: "neutral"},
	{Provider: ProviderOpenAI, Code: "shimmer", Language: "multi", Scenario: "soft"},
}

var minimaxVoices = []pipeline.Voice{
	{Provider: ProviderMinimax, Code: "English_Graceful_Lady", Name: "Graceful Lady", Language: "en", Gender: "female", Scenario: "Graceful female voice"},
	{Provider: ProviderMinimax, Code: "English_radiant_girl", Name: "Radiant Girl", Language: "en", Gender: "female", Scenario: "Lively female voice"},
	{Provider: ProviderMinimax, Code: "English_Insightful_Speaker", Name: "Insightful Speaker", Language: "en", Gender: "male", Scenario: "Steady male voice"},
	{Provider: ProviderMinimax, Code: "English_Persuasive_Man", Name: "Persuasive Man", Language: "en", Gender: "male", Scenario: "Persuasive male voice"},
	{Provider: ProviderMinimax, Code: "English_expressive_narrator", Name: "Expressive Narrator", Language: "en", Gender: "male", Scenario: "Narration"},
	{Provider: ProviderMinimax, Code: "English_Lucky_Robot", Name: "Lucky Robot", Language: "en", Gender: "neutral", Scenario: "Robot"},
}

var omnivoiceVoices = []pipeline.Voice{
	{Provider: ProviderOmniVoice, Code: "auto", Name: "KOVA Voice Studio clone", Language: "vi", Gender: "neutral", Scenario: "Colab GPU only; one consented reference audio per job"},
}

// Gateway model catalogues vary by deployment. "auto" omits the voice field
// and lets models such as edge-tts or google-tts/en use their preset. A user
// may enter a gateway-specific voice/profile code in the desktop project tab.
var gatewayVoices = []pipeline.Voice{
	{Provider: ProviderGateway, Code: "auto", Name: "Gateway preset", Language: "multi", Gender: "neutral", Scenario: "9Router/API gateway model default"},
}
