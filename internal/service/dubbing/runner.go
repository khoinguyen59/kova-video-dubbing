package dubbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"kova/internal/types"
	"kova/pkg/util"
	"os"
	"path/filepath"
)

type Result struct {
	Plan   []PlanItem
	Chunks []Chunk
	Report Report
	DubSRT string
	Audio  string
	Video  string
}

// AudioResult is the reviewable half of a dubbing run.  It intentionally
// stops after synthesis and timeline fitting so callers can let a user listen
// to the generated track before any source video is modified or muxed.
//
// Result is retained for the legacy one-call pipeline; its audio fields are
// populated from AudioResult and it adds the optional muxed video path.
type AudioResult struct {
	Plan   []PlanItem
	Chunks []Chunk
	Report Report
	DubSRT string
	Audio  string
}

type Runner struct {
	deps Dependencies
}

func NewRunner(deps Dependencies) *Runner {
	if deps.Config.MaxChunkSize <= 0 {
		deps.Config = DefaultConfig()
	}
	if deps.FFmpeg == nil {
		deps.FFmpeg = defaultFFmpegRunner
	}
	if deps.Duration == nil {
		deps.Duration = util.GetAudioDuration
	}
	if deps.OutputAudio == "" && deps.Workdir != "" {
		deps.OutputAudio = filepath.Join(deps.Workdir, types.TtsResultAudioFileName)
	}
	if deps.OutputVideo == "" && deps.Workdir != "" {
		deps.OutputVideo = filepath.Join(deps.Workdir, types.SubtitleTaskVideoWithTtsFileName)
	}
	return &Runner{deps: deps}
}

// Synthesize creates the fitted dubbed audio and its review artifacts only.
// It deliberately does not require InputVideo: a staged workflow can now
// pause at this point and require explicit user approval before muxing.
func (r *Runner) Synthesize(ctx context.Context) (AudioResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateSynthesis(); err != nil {
		return AudioResult{}, err
	}

	cues, err := ParseSRTFile(r.deps.InputSRT)
	if err != nil {
		return AudioResult{}, err
	}
	if len(cues) == 0 {
		return AudioResult{}, errors.New("input srt has no cues")
	}

	dubbingDir := filepath.Join(r.deps.Workdir, DubbingDirName)
	segmentsDir := filepath.Join(dubbingDir, "segments")
	if err := os.MkdirAll(segmentsDir, 0755); err != nil {
		return AudioResult{}, err
	}

	cleanedCues := cleanCuesForSpeech(cues)
	if err := WriteSRTFile(filepath.Join(dubbingDir, DubbingInputFileName), cleanedCues); err != nil {
		return AudioResult{}, err
	}

	planner := NewPlanner(r.deps.Config, NewStatisticalEstimator(), NewLLMOptimizer(r.deps.Chat))
	plan, chunks, err := planner.Plan(cleanedCues, r.deps.Language)
	if err != nil {
		return AudioResult{}, err
	}

	var fitted []PlanItem
	var fittedChunks []Chunk
	var report Report
	_, durationAware := r.deps.TTS.(DurationAwareTTS)
	if durationAware {
		plan, err = GenerateRawSegments(ctx, r.deps.TTS, plan, r.deps.Voice, segmentsDir, r.deps.FFmpeg, r.deps.Duration)
		if err != nil {
			return AudioResult{}, err
		}
		fitted, report, err = FitCueTimeline(plan, r.deps.Config)
		if err != nil {
			return AudioResult{}, err
		}
	} else {
		plan, chunks, err = GenerateRawChunkSegments(ctx, r.deps.TTS, plan, chunks, r.deps.Voice, segmentsDir, r.deps.FFmpeg, r.deps.Duration)
		if err != nil {
			return AudioResult{}, err
		}
		fitted, fittedChunks, report, err = FitTimeline(plan, chunks, r.deps.Config)
		if err != nil {
			return AudioResult{}, err
		}
	}

	dubSRT := filepath.Join(dubbingDir, DubSubtitleFileName)
	if err := WriteSRTFile(dubSRT, BuildDubCues(fitted)); err != nil {
		return AudioResult{}, err
	}
	if err := writeJSON(filepath.Join(dubbingDir, DubbingPlanFileName), fitted); err != nil {
		return AudioResult{}, err
	}
	if err := writeJSON(filepath.Join(dubbingDir, DubbingReportName), report); err != nil {
		return AudioResult{}, err
	}

	if err := ensureParentDir(r.deps.OutputAudio); err != nil {
		return AudioResult{}, err
	}
	if durationAware {
		if err := AssembleAudio(fitted, segmentsDir, r.deps.OutputAudio, r.deps.FFmpeg); err != nil {
			return AudioResult{}, err
		}
	} else {
		if err := AssembleChunkAudio(fitted, fittedChunks, segmentsDir, r.deps.OutputAudio, r.deps.FFmpeg); err != nil {
			return AudioResult{}, err
		}
	}
	if err := ensureNonEmptyFile(r.deps.OutputAudio, "output audio"); err != nil {
		return AudioResult{}, err
	}

	return AudioResult{
		Plan:   fitted,
		Chunks: fittedChunks,
		Report: report,
		DubSRT: dubSRT,
		Audio:  r.deps.OutputAudio,
	}, nil
}

// Mux combines a previously reviewed audio artifact with the source video.
// It does not create speech, contact a TTS provider, or alter audio review
// artifacts.  This gives callers a second explicit workflow boundary.
func (r *Runner) Mux() (string, error) {
	if err := r.validateMux(); err != nil {
		return "", err
	}
	if err := ensureParentDir(r.deps.OutputVideo); err != nil {
		return "", err
	}
	if err := r.deps.FFmpeg(buildMuxArgs(r.deps.InputVideo, r.deps.OutputAudio, r.deps.OutputVideo)); err != nil {
		return "", err
	}
	if err := ensureNonEmptyFile(r.deps.OutputVideo, "output video"); err != nil {
		return "", err
	}
	return r.deps.OutputVideo, nil
}

// Run preserves the existing one-call behavior for legacy callers.  The
// native staged Kova workflow calls Synthesize and Mux separately.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	audio, err := r.Synthesize(ctx)
	if err != nil {
		return Result{}, err
	}
	video, err := r.Mux()
	if err != nil {
		return Result{}, err
	}
	return Result{
		Plan:   audio.Plan,
		Chunks: audio.Chunks,
		Report: audio.Report,
		DubSRT: audio.DubSRT,
		Audio:  audio.Audio,
		Video:  video,
	}, nil
}

func (r *Runner) validateSynthesis() error {
	if r.deps.Workdir == "" {
		return errors.New("workdir is required")
	}
	if r.deps.InputSRT == "" {
		return errors.New("input srt is required")
	}
	if r.deps.TTS == nil {
		return errors.New("tts is required")
	}
	return nil
}

func (r *Runner) validateMux() error {
	if r.deps.InputVideo == "" {
		return errors.New("input video is required")
	}
	if err := ensureNonEmptyFile(r.deps.InputVideo, "input video"); err != nil {
		return err
	}
	if r.deps.OutputAudio == "" {
		return errors.New("output audio is required")
	}
	if err := ensureNonEmptyFile(r.deps.OutputAudio, "output audio"); err != nil {
		return err
	}
	if r.deps.OutputVideo == "" {
		return errors.New("output video is required")
	}
	return nil
}

func cleanCuesForSpeech(cues []Cue) []Cue {
	cleaned := make([]Cue, len(cues))
	copy(cleaned, cues)
	for i := range cleaned {
		cleaned[i].Text = CleanTextForSpeech(cleaned[i].Text)
	}
	return cleaned
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

func ensureNonEmptyFile(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s %s: %w", label, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s %s is a directory", label, path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s %s is empty", label, path)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
