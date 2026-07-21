package dubbing

import (
	"context"
	"kova/internal/types"
)

const (
	DubbingDirName       = "dubbing"
	DubbingInputFileName = "dubbing_input.srt"
	DubbingPlanFileName  = "dubbing_plan.json"
	DubbingReportName    = "dubbing_report.json"
	DubSubtitleFileName  = "dub.srt"
)

type Config struct {
	MinSubtitleDuration float64
	MaxChunkSize        int
	GapTolerance        float64
	SpeedMin            float64
	SpeedAccept         float64
	SpeedMax            float64
	EnableTextRewrite   bool
	RewriteMaxAttempts  int
	Estimator           string
}

func DefaultConfig() Config {
	return Config{
		MinSubtitleDuration: 2.5,
		MaxChunkSize:        5,
		GapTolerance:        1.5,
		SpeedMin:            0.95,
		// Keep the naturalness boundary deliberately conservative. Anything
		// above 1.08x is shown for review; anything above 1.12x is a hard
		// rewrite-before-approval signal, never silently accepted as normal.
		SpeedAccept:        1.08,
		SpeedMax:           1.12,
		EnableTextRewrite:  true,
		RewriteMaxAttempts: 2,
		Estimator:          "statistical",
	}
}

type Cue struct {
	Index int
	Start float64
	End   float64
	Text  string
}

func (c Cue) Duration() float64 {
	return c.End - c.Start
}

type PlanItem struct {
	Index              int     `json:"index"`
	OriginalStart      float64 `json:"original_start"`
	OriginalEnd        float64 `json:"original_end"`
	NewStart           float64 `json:"new_start"`
	NewEnd             float64 `json:"new_end"`
	OriginalText       string  `json:"original_text"`
	CleanText          string  `json:"clean_text"`
	SpokenText         string  `json:"spoken_text"`
	EstimatedDuration  float64 `json:"estimated_duration"`
	EstimateConfidence float64 `json:"estimate_confidence"`
	ActualDuration     float64 `json:"actual_duration"`
	SpeedFactor        float64 `json:"speed_factor"`
	ChunkID            int     `json:"chunk_id"`
	RewriteAttempts    int     `json:"rewrite_attempts"`
	Warning            string  `json:"warning,omitempty"`
}

type Chunk struct {
	ID             int
	Items          []int
	Start          float64
	End            float64
	ActualDuration float64
	SpeedFactor    float64
}

type Report struct {
	Warnings          []string `json:"warnings"`
	FailedIndexes     []int    `json:"failed_indexes"`
	OverLimitIndexes  []int    `json:"over_limit_indexes"`
	MaxSpeedFactor    float64  `json:"max_speed_factor"`
	RewriteCount      int      `json:"rewrite_count"`
	RequiresAttention bool     `json:"requires_attention"`
}

type CommandRunner func(args []string) error
type DurationProbe func(path string) (float64, error)

// DurationAwareTTS can generate directly into a subtitle-sized slot. Native
// duration control is preferred over stretching a completed sentence later.
type DurationAwareTTS interface {
	types.Ttser
	Text2SpeechWithDuration(text, voice, outputFile string, duration float64) error
}

type Dependencies struct {
	TTS         types.Ttser
	Chat        types.ChatCompleter
	Language    types.StandardLanguageCode
	Voice       string
	Workdir     string
	InputSRT    string
	InputVideo  string
	OutputAudio string
	OutputVideo string
	Config      Config
	FFmpeg      CommandRunner
	Duration    DurationProbe
}

type TextOptimizer interface {
	Optimize(ctx context.Context, text string, availableSeconds float64, reason string) (string, error)
}

// LanguageAwareTextOptimizer is optional to keep adapters written for the
// older interface working. KOVA's own LLM optimizer uses it so a shortening
// pass cannot silently switch the Vietnamese script back to another language.
type LanguageAwareTextOptimizer interface {
	OptimizeForLanguage(ctx context.Context, text string, availableSeconds float64, reason string, language types.StandardLanguageCode) (string, error)
}
