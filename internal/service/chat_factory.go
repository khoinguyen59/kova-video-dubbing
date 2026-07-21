package service

import (
	"fmt"
	"kova/config"
	"kova/internal/types"
	"kova/pkg/ollama"
	"kova/pkg/openai"
	"strings"
)

type failedChatCompleter struct {
	err error
}

func (c *failedChatCompleter) ChatCompletion(string) (string, error) {
	return "", c.err
}

func newChatCompleter() types.ChatCompleter {
	provider := strings.ToLower(strings.TrimSpace(config.Conf.Llm.Provider))
	switch provider {
	case "ollama":
		apiKey := config.Conf.Llm.SessionApiKey
		if strings.TrimSpace(apiKey) == "" {
			// Accept a pre-existing config value for migration, while current
			// desktop entries use the non-persistent SessionApiKey field.
			apiKey = config.Conf.Llm.ApiKey
		}
		client, err := ollama.NewClientWithAPIKey(
			config.Conf.Llm.BaseUrl,
			config.Conf.Llm.Model,
			apiKey,
			config.Conf.Llm.ApiKeyEnv,
			config.Conf.App.Proxy,
		)
		if err != nil {
			return &failedChatCompleter{err: fmt.Errorf("initialize Ollama LLM: %w", err)}
		}
		return client
	case "", "openai", "openai-compatible":
		return openai.NewClient(config.Conf.Llm.BaseUrl, config.ResolveLLMAPIKey(), config.Conf.App.Proxy)
	default:
		return &failedChatCompleter{err: fmt.Errorf("unsupported LLM provider %q", config.Conf.Llm.Provider)}
	}
}
