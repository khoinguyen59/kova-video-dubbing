package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"kova/internal/service/dubbing"
)

func main() {
	var (
		planPath   = flag.String("plan", "", "existing dubbing_plan.json")
		segments   = flag.String("segments", "", "directory containing raw/<cue>.wav")
		inputVideo = flag.String("video", "", "video whose visual stream should be preserved")
		outputWAV  = flag.String("output-audio", "", "refitted PCM WAV output")
		outputMP4  = flag.String("output-video", "", "video with the refitted dub")
		ffmpegPath = flag.String("ffmpeg", "ffmpeg", "ffmpeg executable")
		reportPath = flag.String("report", "", "optional JSON report output")
	)
	flag.Parse()

	for label, value := range map[string]string{
		"plan":         *planPath,
		"segments":     *segments,
		"video":        *inputVideo,
		"output-audio": *outputWAV,
		"output-video": *outputMP4,
	} {
		if value == "" {
			fail(fmt.Errorf("-%s is required", label))
		}
	}

	var plan []dubbing.PlanItem
	data, err := os.ReadFile(*planPath)
	if err != nil {
		fail(err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		fail(err)
	}

	fitted, report, err := dubbing.FitCueTimeline(plan, dubbing.DefaultConfig())
	if err != nil {
		fail(err)
	}

	run := func(args []string) error {
		cmd := exec.Command(*ffmpegPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ffmpeg %v: %w\n%s", args, err, output)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(*outputWAV), 0o755); err != nil {
		fail(err)
	}
	if err := dubbing.AssembleAudio(fitted, *segments, *outputWAV, run); err != nil {
		fail(err)
	}

	if err := os.MkdirAll(filepath.Dir(*outputMP4), 0o755); err != nil {
		fail(err)
	}
	if err := run([]string{
		"-y",
		"-i", *inputVideo,
		"-i", *outputWAV,
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192k",
		"-af", "apad",
		"-shortest",
		*outputMP4,
	}); err != nil {
		fail(err)
	}

	if *reportPath != "" {
		if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
			fail(err)
		}
		reportData, err := json.MarshalIndent(struct {
			Report dubbing.Report     `json:"report"`
			Plan   []dubbing.PlanItem `json:"plan"`
		}{Report: report, Plan: fitted}, "", "  ")
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(*reportPath, append(reportData, '\n'), 0o644); err != nil {
			fail(err)
		}
	}

	fmt.Printf("refitted %d cues; max required speed %.3f\n", len(fitted), report.MaxSpeedFactor)
	fmt.Printf("audio: %s\nvideo: %s\n", *outputWAV, *outputMP4)
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
