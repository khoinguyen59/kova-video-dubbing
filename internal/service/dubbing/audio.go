package dubbing

import (
	"context"
	"fmt"
	"kova/internal/processutil"
	"kova/internal/storage"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// subtitleTimelineTolerance absorbs the sub-millisecond floating-point noise
// introduced when a chunk's fitted duration is divided by its tempo factor.
// SRT timestamps themselves are stored at millisecond precision, so a smaller
// discrepancy cannot be meaningfully rendered or heard. Material overlaps
// still fail before FFmpeg is invoked.
const subtitleTimelineTolerance = 0.001

func startsBeforeTimelineBoundary(start, previousEnd float64) bool {
	return start < previousEnd-subtitleTimelineTolerance
}

func hasTimelineGap(start, previousEnd float64) bool {
	return start > previousEnd+subtitleTimelineTolerance
}

func defaultFFmpegRunner(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, storage.FfmpegPath, args...)
	processutil.HideConsole(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg timed out after 60 seconds: %s", string(output))
		}
		return fmt.Errorf("ffmpeg error: %w, output: %s", err, string(output))
	}
	return nil
}

func WriteTinySilence(output string, run CommandRunner) error {
	if run == nil {
		run = defaultFFmpegRunner
	}
	return run([]string{
		"-y",
		"-f", "lavfi",
		"-i", "anullsrc=channel_layout=mono:sample_rate=44100",
		"-t", "0.100",
		"-ar", "44100",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		output,
	})
}

func buildAtempoFilter(speed float64) (string, error) {
	if speed <= 0 || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return "", fmt.Errorf("speed must be finite and > 0: %v", speed)
	}

	parts := []string{}
	for speed > 2.0 {
		parts = append(parts, "atempo=2.000")
		speed /= 2.0
	}
	for speed < 0.5 {
		parts = append(parts, "atempo=0.500")
		speed /= 0.5
	}
	parts = append(parts, fmt.Sprintf("atempo=%.3f", speed))
	return strings.Join(parts, ","), nil
}

func buildSlotFitFilter(speed, duration float64) (string, error) {
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return "", fmt.Errorf("slot duration must be finite and > 0: %v", duration)
	}
	atempo, err := buildAtempoFilter(speed)
	if err != nil {
		return "", err
	}
	// apad fills a naturally short utterance with silence; atrim keeps an
	// exceptional long utterance from drifting into the next subtitle slot.
	return fmt.Sprintf("%s,apad=whole_dur=%.3f,atrim=duration=%.3f", atempo, duration, duration), nil
}

func buildMuxArgs(inputVideo, inputAudio, outputVideo string) []string {
	return []string{
		"-y",
		"-i", inputVideo,
		"-i", inputAudio,
		"-c:v", "copy",
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:a", "aac",
		"-b:a", "192k",
		// Pad a short dubbed track so -shortest preserves the complete source video.
		"-af", "apad",
		"-shortest",
		outputVideo,
	}
}

// buildBackgroundMixArgs adds only the separated no-vocals stem under the
// clean TTS track.  The original source audio is never used here, otherwise
// the source speaker would leak back into the translated output.
func buildBackgroundMixArgs(dubbedAudio, backgroundAudio, outputAudio string, backgroundVolume float64) []string {
	if backgroundVolume <= 0 || backgroundVolume > 1 {
		backgroundVolume = 0.38
	}
	filter := fmt.Sprintf("[0:a]aresample=44100[kova_voice];[1:a]aresample=44100,volume=%.3f[kova_music];[kova_voice][kova_music]amix=inputs=2:duration=longest:dropout_transition=0[kova_mix]", backgroundVolume)
	return []string{
		"-y",
		"-i", dubbedAudio,
		"-i", backgroundAudio,
		"-filter_complex", filter,
		"-map", "[kova_mix]",
		"-ar", "44100",
		"-ac", "2",
		"-c:a", "pcm_s16le",
		outputAudio,
	}
}

func BuildDubCues(plan []PlanItem) []Cue {
	cues := make([]Cue, len(plan))
	for i, item := range plan {
		cues[i] = Cue{
			Index: i + 1,
			Start: item.NewStart,
			End:   item.NewEnd,
			Text:  item.SpokenText,
		}
	}
	return cues
}

func fittedSegmentPath(segmentsDir string, index int) string {
	return filepath.Join(segmentsDir, "fitted", fmt.Sprintf("%d.wav", index))
}

func rawChunkPath(segmentsDir string, id int) string {
	return filepath.Join(segmentsDir, "raw", fmt.Sprintf("chunk_%d.wav", id))
}

func fittedChunkPath(segmentsDir string, id int) string {
	return filepath.Join(segmentsDir, "fitted", fmt.Sprintf("chunk_%d.wav", id))
}

func AssembleAudio(plan []PlanItem, segmentsDir, outputAudio string, run CommandRunner) error {
	return assembleAudio(plan, segmentsDir, outputAudio, run, nil)
}

func assembleAudio(plan []PlanItem, segmentsDir, outputAudio string, run CommandRunner, progress ProgressReporter) error {
	if run == nil {
		run = defaultFFmpegRunner
	}

	_, err := validateAssemblePlan(plan, segmentsDir)
	if err != nil {
		return err
	}

	fittedDir := filepath.Join(segmentsDir, "fitted")
	if err := os.MkdirAll(fittedDir, 0755); err != nil {
		return err
	}

	concatLines := make([]string, 0, len(plan)*2)
	lastEnd := 0.0
	for itemIndex, item := range plan {
		if progress != nil {
			progress("fit", itemIndex, len(plan), fmt.Sprintf("Fitting speech segment %d/%d", itemIndex+1, len(plan)))
		}
		raw := filepath.Join(segmentsDir, "raw", fmt.Sprintf("%d.wav", item.Index))
		if err := ensureNonEmptyFile(raw, "raw segment"); err != nil {
			return err
		}

		fitted := fittedSegmentPath(segmentsDir, item.Index)
		slotDuration := item.NewEnd - item.NewStart
		slotFilter, err := buildSlotFitFilter(item.SpeedFactor, slotDuration)
		if err != nil {
			return err
		}
		if err := run([]string{
			"-y",
			"-i", raw,
			"-filter:a", slotFilter,
			"-ar", "44100",
			"-ac", "1",
			"-c:a", "pcm_s16le",
			fitted,
		}); err != nil {
			return fmt.Errorf("fit segment %d: %w", item.Index, err)
		}

		if hasTimelineGap(item.NewStart, lastEnd) {
			silence := filepath.Join(fittedDir, fmt.Sprintf("silence_%d.wav", item.Index))
			if err := run([]string{
				"-y",
				"-f", "lavfi",
				"-i", "anullsrc=channel_layout=mono:sample_rate=44100",
				"-t", fmt.Sprintf("%.3f", item.NewStart-lastEnd),
				"-ar", "44100",
				"-ac", "1",
				"-c:a", "pcm_s16le",
				silence,
			}); err != nil {
				return fmt.Errorf("write silence before segment %d: %w", item.Index, err)
			}
			concatLines = append(concatLines, fmt.Sprintf("file '%s'", filepath.Base(silence)))
		}

		concatLines = append(concatLines, fmt.Sprintf("file '%s'", filepath.Base(fitted)))
		lastEnd = item.NewEnd
		if progress != nil {
			progress("fit", itemIndex+1, len(plan), fmt.Sprintf("Speech segment %d/%d fitted", itemIndex+1, len(plan)))
		}
	}
	if progress != nil {
		progress("assemble", 0, 1, "Joining fitted speech into one audio track")
	}

	concatPath := filepath.Join(fittedDir, "concat.txt")
	if err := os.WriteFile(concatPath, []byte(strings.Join(concatLines, "\n")+"\n"), 0644); err != nil {
		return err
	}

	if err := run([]string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatPath,
		"-c", "copy",
		outputAudio,
	}); err != nil {
		return fmt.Errorf("concat fitted audio: %w", err)
	}
	if progress != nil {
		progress("assemble", 1, 1, "Dubbed audio track is ready")
	}

	return nil
}

func AssembleChunkAudio(plan []PlanItem, chunks []Chunk, segmentsDir, outputAudio string, run CommandRunner) error {
	return assembleChunkAudio(plan, chunks, segmentsDir, outputAudio, run, nil)
}

func assembleChunkAudio(plan []PlanItem, chunks []Chunk, segmentsDir, outputAudio string, run CommandRunner, progress ProgressReporter) error {
	if run == nil {
		run = defaultFFmpegRunner
	}
	filters, err := validateAssembleChunkPlan(plan, chunks, segmentsDir)
	if err != nil {
		return err
	}

	fittedDir := filepath.Join(segmentsDir, "fitted")
	if err := os.MkdirAll(fittedDir, 0755); err != nil {
		return err
	}

	concatLines := make([]string, 0, len(chunks)*2)
	lastEnd := 0.0
	for i, chunk := range chunks {
		if progress != nil {
			progress("fit", i, len(chunks), fmt.Sprintf("Fitting speech block %d/%d", i+1, len(chunks)))
		}
		raw := rawChunkPath(segmentsDir, chunk.ID)
		fitted := fittedChunkPath(segmentsDir, chunk.ID)
		if err := run([]string{
			"-y",
			"-i", raw,
			"-filter:a", filters[i],
			"-ar", "44100",
			"-ac", "1",
			"-c:a", "pcm_s16le",
			fitted,
		}); err != nil {
			return fmt.Errorf("fit chunk %d: %w", chunk.ID, err)
		}

		if hasTimelineGap(chunk.Start, lastEnd) {
			silence := filepath.Join(fittedDir, fmt.Sprintf("silence_chunk_%d.wav", chunk.ID))
			if err := run([]string{
				"-y",
				"-f", "lavfi",
				"-i", "anullsrc=channel_layout=mono:sample_rate=44100",
				"-t", fmt.Sprintf("%.3f", chunk.Start-lastEnd),
				"-ar", "44100",
				"-ac", "1",
				"-c:a", "pcm_s16le",
				silence,
			}); err != nil {
				return fmt.Errorf("write silence before chunk %d: %w", chunk.ID, err)
			}
			concatLines = append(concatLines, fmt.Sprintf("file '%s'", filepath.Base(silence)))
		}

		concatLines = append(concatLines, fmt.Sprintf("file '%s'", filepath.Base(fitted)))
		lastEnd = chunkFittedEnd(plan, chunk)
		if progress != nil {
			progress("fit", i+1, len(chunks), fmt.Sprintf("Speech block %d/%d fitted", i+1, len(chunks)))
		}
	}
	if progress != nil {
		progress("assemble", 0, 1, "Joining fitted speech into one audio track")
	}

	concatPath := filepath.Join(fittedDir, "concat.txt")
	if err := os.WriteFile(concatPath, []byte(strings.Join(concatLines, "\n")+"\n"), 0644); err != nil {
		return err
	}

	if err := run([]string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatPath,
		"-c", "copy",
		outputAudio,
	}); err != nil {
		return fmt.Errorf("concat fitted audio: %w", err)
	}
	if progress != nil {
		progress("assemble", 1, 1, "Dubbed audio track is ready")
	}
	return nil
}

func validateAssemblePlan(plan []PlanItem, segmentsDir string) ([]string, error) {
	if len(plan) == 0 {
		return nil, fmt.Errorf("plan is empty")
	}

	filters := make([]string, len(plan))
	lastEnd := 0.0
	for i, item := range plan {
		if item.NewEnd <= item.NewStart {
			return nil, fmt.Errorf("plan item %d new end must be greater than new start: start %.3f end %.3f", item.Index, item.NewStart, item.NewEnd)
		}
		if startsBeforeTimelineBoundary(item.NewStart, lastEnd) {
			return nil, fmt.Errorf("plan item %d starts before previous end: start %.6f lastEnd %.6f", item.Index, item.NewStart, lastEnd)
		}

		filter, err := buildAtempoFilter(item.SpeedFactor)
		if err != nil {
			return nil, err
		}
		filters[i] = filter

		raw := filepath.Join(segmentsDir, "raw", fmt.Sprintf("%d.wav", item.Index))
		if err := ensureNonEmptyFile(raw, "raw segment"); err != nil {
			return nil, err
		}

		lastEnd = item.NewEnd
	}
	return filters, nil
}

func validateAssembleChunkPlan(plan []PlanItem, chunks []Chunk, segmentsDir string) ([]string, error) {
	if len(plan) == 0 {
		return nil, fmt.Errorf("plan is empty")
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("chunks are empty")
	}

	filters := make([]string, len(chunks))
	lastEnd := 0.0
	for i, chunk := range chunks {
		if len(chunk.Items) == 0 {
			return nil, fmt.Errorf("chunk %d has no items", chunk.ID)
		}
		if startsBeforeTimelineBoundary(chunk.Start, lastEnd) {
			return nil, fmt.Errorf("chunk %d starts before previous end: start %.6f lastEnd %.6f", chunk.ID, chunk.Start, lastEnd)
		}
		end := chunkFittedEnd(plan, chunk)
		if end <= chunk.Start {
			return nil, fmt.Errorf("chunk %d end must be greater than start: start %.3f end %.3f", chunk.ID, chunk.Start, end)
		}
		filter, err := buildAtempoFilter(chunkSpeedFactor(plan, chunk))
		if err != nil {
			return nil, err
		}
		filters[i] = filter

		raw := rawChunkPath(segmentsDir, chunk.ID)
		if err := ensureNonEmptyFile(raw, "raw chunk"); err != nil {
			return nil, err
		}
		lastEnd = end
	}
	return filters, nil
}

func chunkSpeedFactor(plan []PlanItem, chunk Chunk) float64 {
	if chunk.SpeedFactor > 0 {
		return chunk.SpeedFactor
	}
	for _, idx := range chunk.Items {
		if idx >= 0 && idx < len(plan) && plan[idx].SpeedFactor > 0 {
			return plan[idx].SpeedFactor
		}
	}
	return 1
}

func chunkFittedEnd(plan []PlanItem, chunk Chunk) float64 {
	end := 0.0
	for _, idx := range chunk.Items {
		if idx >= 0 && idx < len(plan) && plan[idx].NewEnd > end {
			end = plan[idx].NewEnd
		}
	}
	if end > 0 {
		return end
	}
	end = chunk.Start
	if chunk.End > end {
		end = chunk.End
	}
	return end
}
