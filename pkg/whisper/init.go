package whisper

import (
	"github.com/sashabaranov/go-openai"
	"kova/config"
	"net/http"
)

type Client struct {
	client *openai.Client
	model  string
}

func NewClient(baseUrl, apiKey, proxyAddr string) *Client {
	return NewClientWithModel(baseUrl, apiKey, proxyAddr, openai.Whisper1)
}

func NewClientWithModel(baseUrl, apiKey, proxyAddr, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	if baseUrl != "" {
		cfg.BaseURL = baseUrl
	}

	if proxyAddr != "" {
		transport := &http.Transport{
			Proxy: http.ProxyURL(config.Conf.App.ParsedProxy),
		}
		cfg.HTTPClient = &http.Client{
			Transport: transport,
		}
	}

	client := openai.NewClientWithConfig(cfg)
	if model == "" {
		model = openai.Whisper1
	}
	return &Client{client: client, model: model}
}
