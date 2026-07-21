package handler

import (
	"kova/config"
	"kova/internal/response"
	"kova/log"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var configUpdated bool

type ConfigRequest struct {
	App struct {
		SegmentDuration       int    `json:"segmentDuration"`
		TranscribeParallelNum int    `json:"transcribeParallelNum"`
		TranslateParallelNum  int    `json:"translateParallelNum"`
		TranscribeMaxAttempts int    `json:"transcribeMaxAttempts"`
		TranslateMaxAttempts  int    `json:"translateMaxAttempts"`
		MaxSentenceLength     int    `json:"maxSentenceLength"`
		Proxy                 string `json:"proxy"`
	} `json:"app"`
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"server"`
	Llm struct {
		Provider  string `json:"provider"`
		BaseUrl   string `json:"baseUrl"`
		ApiKey    string `json:"apiKey"`
		ApiKeyEnv string `json:"apiKeyEnv"`
		Model     string `json:"model"`
	} `json:"llm"`
	Transcribe struct {
		Provider              string `json:"provider"`
		EnableGpuAcceleration bool   `json:"enableGpuAcceleration"`
		Openai                struct {
			BaseUrl string `json:"baseUrl"`
			ApiKey  string `json:"apiKey"`
			Model   string `json:"model"`
		} `json:"openai"`
		Fasterwhisper struct {
			Model string `json:"model"`
		} `json:"fasterwhisper"`
		Whisperkit struct {
			Model string `json:"model"`
		} `json:"whisperkit"`
		Whispercpp struct {
			Model string `json:"model"`
		} `json:"whispercpp"`
		Aliyun struct {
			Oss struct {
				AccessKeyId     string `json:"accessKeyId"`
				AccessKeySecret string `json:"accessKeySecret"`
				Bucket          string `json:"bucket"`
			} `json:"oss"`
			Speech struct {
				AccessKeyId     string `json:"accessKeyId"`
				AccessKeySecret string `json:"accessKeySecret"`
				AppKey          string `json:"appKey"`
			} `json:"speech"`
		} `json:"aliyun"`
	} `json:"transcribe"`
	Tts struct {
		Provider string `json:"provider"`
		Openai   struct {
			BaseUrl string `json:"baseUrl"`
			ApiKey  string `json:"apiKey"`
			Model   string `json:"model"`
		} `json:"openai"`
		Aliyun struct {
			Oss struct {
				AccessKeyId     string `json:"accessKeyId"`
				AccessKeySecret string `json:"accessKeySecret"`
				Bucket          string `json:"bucket"`
			} `json:"oss"`
			Speech struct {
				AccessKeyId     string `json:"accessKeyId"`
				AccessKeySecret string `json:"accessKeySecret"`
				AppKey          string `json:"appKey"`
			} `json:"speech"`
		} `json:"aliyun"`
		Omnivoice struct {
			BaseUrl string `json:"baseUrl"`
			// Accept a session token only on update. GetConfig constructs a fresh
			// response and intentionally never copies this memory-only secret.
			SessionApiKey         string  `json:"sessionApiKey,omitempty"`
			Language              string  `json:"language"`
			ReferenceAudio        string  `json:"referenceAudio"`
			ReferenceText         string  `json:"referenceText"`
			Instruct              string  `json:"instruct"`
			Speed                 float64 `json:"speed"`
			NumStep               int     `json:"numStep"`
			RequestTimeoutSeconds int     `json:"requestTimeoutSeconds"`
			RequireReference      bool    `json:"requireReferenceAudio"`
			RemoteOnly            bool    `json:"remoteOnly"`
			RequireCUDA           bool    `json:"requireCuda"`
		} `json:"omnivoice"`
		Gateway struct {
			Endpoint       string `json:"endpoint"`
			ApiKey         string `json:"apiKey"`
			ApiKeyEnv      string `json:"apiKeyEnv"`
			Model          string `json:"model"`
			ResponseFormat string `json:"responseFormat"`
		} `json:"gateway"`
	} `json:"tts"`
}

func (h Handler) GetConfig(c *gin.Context) {
	log.GetLogger().Info("Kova configuration requested")

	configResponse := ConfigRequest{
		App: struct {
			SegmentDuration       int    `json:"segmentDuration"`
			TranscribeParallelNum int    `json:"transcribeParallelNum"`
			TranslateParallelNum  int    `json:"translateParallelNum"`
			TranscribeMaxAttempts int    `json:"transcribeMaxAttempts"`
			TranslateMaxAttempts  int    `json:"translateMaxAttempts"`
			MaxSentenceLength     int    `json:"maxSentenceLength"`
			Proxy                 string `json:"proxy"`
		}{
			SegmentDuration:       config.Conf.App.SegmentDuration,
			TranscribeParallelNum: config.Conf.App.TranscribeParallelNum,
			TranslateParallelNum:  config.Conf.App.TranslateParallelNum,
			TranscribeMaxAttempts: config.Conf.App.TranscribeMaxAttempts,
			TranslateMaxAttempts:  config.Conf.App.TranslateMaxAttempts,
			MaxSentenceLength:     config.Conf.App.MaxSentenceLength,
			Proxy:                 config.Conf.App.Proxy,
		},
		Server: struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}{
			Host: config.Conf.Server.Host,
			Port: config.Conf.Server.Port,
		},
		Llm: struct {
			Provider  string `json:"provider"`
			BaseUrl   string `json:"baseUrl"`
			ApiKey    string `json:"apiKey"`
			ApiKeyEnv string `json:"apiKeyEnv"`
			Model     string `json:"model"`
		}{
			Provider:  config.Conf.Llm.Provider,
			BaseUrl:   config.Conf.Llm.BaseUrl,
			ApiKey:    "",
			ApiKeyEnv: config.Conf.Llm.ApiKeyEnv,
			Model:     config.Conf.Llm.Model,
		},
	}

	configResponse.Transcribe.Provider = config.Conf.Transcribe.Provider
	configResponse.Transcribe.EnableGpuAcceleration = config.Conf.Transcribe.EnableGpuAcceleration
	configResponse.Transcribe.Openai.BaseUrl = config.Conf.Transcribe.Openai.BaseUrl
	configResponse.Transcribe.Openai.Model = config.Conf.Transcribe.Openai.Model
	configResponse.Transcribe.Fasterwhisper.Model = config.Conf.Transcribe.Fasterwhisper.Model
	configResponse.Transcribe.Whisperkit.Model = config.Conf.Transcribe.Whisperkit.Model
	configResponse.Transcribe.Whispercpp.Model = config.Conf.Transcribe.Whispercpp.Model
	configResponse.Transcribe.Aliyun.Oss.Bucket = config.Conf.Transcribe.Aliyun.Oss.Bucket
	configResponse.Transcribe.Aliyun.Speech.AppKey = config.Conf.Transcribe.Aliyun.Speech.AppKey

	configResponse.Tts.Provider = config.Conf.Tts.Provider
	configResponse.Tts.Openai.BaseUrl = config.Conf.Tts.Openai.BaseUrl
	configResponse.Tts.Openai.Model = config.Conf.Tts.Openai.Model
	configResponse.Tts.Aliyun.Oss.Bucket = config.Conf.Tts.Aliyun.Oss.Bucket
	configResponse.Tts.Aliyun.Speech.AppKey = config.Conf.Tts.Aliyun.Speech.AppKey
	configResponse.Tts.Omnivoice.BaseUrl = config.Conf.Tts.Omnivoice.BaseUrl
	configResponse.Tts.Omnivoice.Language = config.Conf.Tts.Omnivoice.Language
	// A reference clip is deliberately job-scoped. Do not expose or retain a
	// path from an earlier clone in the general application configuration.
	configResponse.Tts.Omnivoice.ReferenceAudio = ""
	configResponse.Tts.Omnivoice.ReferenceText = config.Conf.Tts.Omnivoice.ReferenceText
	configResponse.Tts.Omnivoice.Instruct = config.Conf.Tts.Omnivoice.Instruct
	configResponse.Tts.Omnivoice.Speed = config.Conf.Tts.Omnivoice.Speed
	configResponse.Tts.Omnivoice.NumStep = config.Conf.Tts.Omnivoice.NumStep
	configResponse.Tts.Omnivoice.RequestTimeoutSeconds = config.Conf.Tts.Omnivoice.RequestTimeoutSeconds
	configResponse.Tts.Omnivoice.RequireReference = config.Conf.Tts.Omnivoice.RequireReference
	configResponse.Tts.Omnivoice.RemoteOnly = config.Conf.Tts.Omnivoice.RemoteOnly
	configResponse.Tts.Omnivoice.RequireCUDA = config.Conf.Tts.Omnivoice.RequireCUDA
	configResponse.Tts.Gateway.Endpoint = config.Conf.Tts.Gateway.Endpoint
	configResponse.Tts.Gateway.ApiKeyEnv = config.Conf.Tts.Gateway.ApiKeyEnv
	configResponse.Tts.Gateway.Model = config.Conf.Tts.Gateway.Model
	configResponse.Tts.Gateway.ResponseFormat = config.Conf.Tts.Gateway.ResponseFormat

	response.R(c, response.Response{
		Error: 0,
		Msg:   "Đã tải cấu hình Kova / Kova configuration loaded",
		Data:  configResponse,
	})
}

func (h Handler) UpdateConfig(c *gin.Context) {
	var req ConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.GetLogger().Error("UpdateConfig ShouldBindJSON err", zap.Error(err))
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Dữ liệu không hợp lệ / Invalid request: " + err.Error(),
			Data:  nil,
		})
		return
	}

	log.GetLogger().Info("Updating Kova configuration")

	config.ConfigBackup = config.Conf

	configUpdated = true

	config.Conf.App.SegmentDuration = req.App.SegmentDuration
	config.Conf.App.TranscribeParallelNum = req.App.TranscribeParallelNum
	config.Conf.App.TranslateParallelNum = req.App.TranslateParallelNum
	config.Conf.App.TranscribeMaxAttempts = req.App.TranscribeMaxAttempts
	config.Conf.App.TranslateMaxAttempts = req.App.TranslateMaxAttempts
	config.Conf.App.MaxSentenceLength = req.App.MaxSentenceLength
	config.Conf.App.Proxy = req.App.Proxy

	config.Conf.Server.Host = req.Server.Host
	config.Conf.Server.Port = req.Server.Port

	config.Conf.Llm.BaseUrl = req.Llm.BaseUrl
	if req.Llm.ApiKey != "" {
		config.Conf.Llm.ApiKey = req.Llm.ApiKey
	}
	config.Conf.Llm.Provider = req.Llm.Provider
	config.Conf.Llm.ApiKeyEnv = req.Llm.ApiKeyEnv
	config.Conf.Llm.Model = req.Llm.Model

	config.Conf.Transcribe.Provider = req.Transcribe.Provider
	config.Conf.Transcribe.EnableGpuAcceleration = req.Transcribe.EnableGpuAcceleration
	config.Conf.Transcribe.Openai.BaseUrl = req.Transcribe.Openai.BaseUrl
	if req.Transcribe.Openai.ApiKey != "" {
		config.Conf.Transcribe.Openai.ApiKey = req.Transcribe.Openai.ApiKey
	}
	config.Conf.Transcribe.Openai.Model = req.Transcribe.Openai.Model
	config.Conf.Transcribe.Fasterwhisper.Model = req.Transcribe.Fasterwhisper.Model
	config.Conf.Transcribe.Whisperkit.Model = req.Transcribe.Whisperkit.Model
	config.Conf.Transcribe.Whispercpp.Model = req.Transcribe.Whispercpp.Model
	if req.Transcribe.Aliyun.Oss.AccessKeyId != "" {
		config.Conf.Transcribe.Aliyun.Oss.AccessKeyId = req.Transcribe.Aliyun.Oss.AccessKeyId
	}
	if req.Transcribe.Aliyun.Oss.AccessKeySecret != "" {
		config.Conf.Transcribe.Aliyun.Oss.AccessKeySecret = req.Transcribe.Aliyun.Oss.AccessKeySecret
	}
	config.Conf.Transcribe.Aliyun.Oss.Bucket = req.Transcribe.Aliyun.Oss.Bucket
	if req.Transcribe.Aliyun.Speech.AccessKeyId != "" {
		config.Conf.Transcribe.Aliyun.Speech.AccessKeyId = req.Transcribe.Aliyun.Speech.AccessKeyId
	}
	if req.Transcribe.Aliyun.Speech.AccessKeySecret != "" {
		config.Conf.Transcribe.Aliyun.Speech.AccessKeySecret = req.Transcribe.Aliyun.Speech.AccessKeySecret
	}
	config.Conf.Transcribe.Aliyun.Speech.AppKey = req.Transcribe.Aliyun.Speech.AppKey

	config.Conf.Tts.Provider = req.Tts.Provider
	config.Conf.Tts.Openai.BaseUrl = req.Tts.Openai.BaseUrl
	if req.Tts.Openai.ApiKey != "" {
		config.Conf.Tts.Openai.ApiKey = req.Tts.Openai.ApiKey
	}
	config.Conf.Tts.Openai.Model = req.Tts.Openai.Model
	if req.Tts.Aliyun.Oss.AccessKeyId != "" {
		config.Conf.Tts.Aliyun.Oss.AccessKeyId = req.Tts.Aliyun.Oss.AccessKeyId
	}
	if req.Tts.Aliyun.Oss.AccessKeySecret != "" {
		config.Conf.Tts.Aliyun.Oss.AccessKeySecret = req.Tts.Aliyun.Oss.AccessKeySecret
	}
	config.Conf.Tts.Aliyun.Oss.Bucket = req.Tts.Aliyun.Oss.Bucket
	if req.Tts.Aliyun.Speech.AccessKeyId != "" {
		config.Conf.Tts.Aliyun.Speech.AccessKeyId = req.Tts.Aliyun.Speech.AccessKeyId
	}
	if req.Tts.Aliyun.Speech.AccessKeySecret != "" {
		config.Conf.Tts.Aliyun.Speech.AccessKeySecret = req.Tts.Aliyun.Speech.AccessKeySecret
	}
	config.Conf.Tts.Aliyun.Speech.AppKey = req.Tts.Aliyun.Speech.AppKey
	config.Conf.Tts.Omnivoice.BaseUrl = req.Tts.Omnivoice.BaseUrl
	if req.Tts.Omnivoice.SessionApiKey != "" {
		config.Conf.Tts.Omnivoice.SessionApiKey = req.Tts.Omnivoice.SessionApiKey
	}
	config.Conf.Tts.Omnivoice.Language = req.Tts.Omnivoice.Language
	// Do not make a prior speaker sample sticky across jobs or clients.
	config.Conf.Tts.Omnivoice.ReferenceAudio = ""
	config.Conf.Tts.Omnivoice.ReferenceText = req.Tts.Omnivoice.ReferenceText
	config.Conf.Tts.Omnivoice.Instruct = req.Tts.Omnivoice.Instruct
	config.Conf.Tts.Omnivoice.Speed = req.Tts.Omnivoice.Speed
	config.Conf.Tts.Omnivoice.NumStep = req.Tts.Omnivoice.NumStep
	config.Conf.Tts.Omnivoice.RequestTimeoutSeconds = req.Tts.Omnivoice.RequestTimeoutSeconds
	// These flags are reported by the API for the UI, but cannot be weakened
	// through a generic settings request. OmniVoice is always remote CUDA and
	// always requires a newly selected reference clip in Kova.
	config.Conf.Tts.Omnivoice.RequireReference = true
	config.Conf.Tts.Omnivoice.RemoteOnly = true
	config.Conf.Tts.Omnivoice.RequireCUDA = true
	config.Conf.Tts.Gateway.Endpoint = req.Tts.Gateway.Endpoint
	if req.Tts.Gateway.ApiKey != "" {
		config.Conf.Tts.Gateway.ApiKey = req.Tts.Gateway.ApiKey
	}
	config.Conf.Tts.Gateway.ApiKeyEnv = req.Tts.Gateway.ApiKeyEnv
	config.Conf.Tts.Gateway.Model = req.Tts.Gateway.Model
	config.Conf.Tts.Gateway.ResponseFormat = req.Tts.Gateway.ResponseFormat

	if err := config.CheckConfig(); err != nil {
		log.GetLogger().Error("Kova configuration validation failed", zap.Error(err))
		config.Conf = config.ConfigBackup
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Cấu hình không hợp lệ / Invalid configuration: " + err.Error(),
			Data:  nil,
		})
		return
	}

	if err := config.SaveConfig(); err != nil {
		log.GetLogger().Error("Failed to save Kova configuration", zap.Error(err))
		response.R(c, response.Response{
			Error: -1,
			Msg:   "Không thể lưu cấu hình / Failed to save configuration: " + err.Error(),
			Data:  nil,
		})
		return
	}

	log.GetLogger().Info("Kova configuration updated")
	response.R(c, response.Response{
		Error: 0,
		Msg:   "Đã cập nhật cấu hình / Configuration updated",
		Data:  nil,
	})
}
