// Package visualocr runs Kova's local PaddleOCR bridge. The bridge is kept
// outside the Go process because Paddle/PaddleOCR ship GPU-specific native
// wheels; Kova controls the job, artifacts and CPU fallback without bundling a
// second Python runtime into the desktop executable.
package visualocr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Request struct {
	VideoPath        string
	OutputSRTPath    string
	Region           Region
	Language         string
	SampleIntervalMS int
	PreferGPU        bool
	MergeGapMS       int
}

type Config struct {
	PythonPath string
	ScriptPath string
	Timeout    time.Duration
}

type Result struct {
	SRTPath       string `json:"srt_path"`
	Device        string `json:"device"`
	FrameCount    int    `json:"frame_count"`
	CueCount      int    `json:"cue_count"`
	DroppedFrames int    `json:"dropped_frames"`
	NormalizedCJK bool   `json:"normalized_cjk"`
	FallbackToCPU bool   `json:"fallback_to_cpu"`
}

// PreflightResult describes whether the selected Python environment can load
// the bridge dependencies. It intentionally does not initialize PaddleOCR, so
// it cannot download model files or consume GPU memory.
type PreflightResult struct {
	Ready         bool   `json:"ready"`
	CUDAAvailable bool   `json:"cuda_available"`
	Python        string `json:"python"`
}

type Runner struct{ Config Config }

// InstallDependencies installs the CPU-capable Python packages required by
// the local OCR bridge. It is intentionally opt-in: downloading PaddleOCR is
// substantial, so KOVA only performs it after a user presses the install
// control in the source stage.
func (r Runner) InstallDependencies(parent context.Context) (PreflightResult, error) {
	python, _, err := r.resolveBridge()
	if err != nil {
		return PreflightResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python,
		"-m", "pip", "install", "--disable-pip-version-check", "--upgrade",
		"opencv-python-headless", "paddlepaddle", "paddleocr",
	)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return PreflightResult{}, errors.New("cài OCR local vượt quá 20 phút; hãy kiểm tra Python/pip và thử lại")
	}
	if err != nil {
		return PreflightResult{}, fmt.Errorf("không thể cài OpenCV/Paddle/PaddleOCR: %w\n%s", err, trimLog(string(output)))
	}
	return r.Preflight(parent)
}

func (r Runner) resolveBridge() (python, script string, err error) {
	python = strings.TrimSpace(r.Config.PythonPath)
	if python == "" {
		python = "python"
	}
	script = strings.TrimSpace(r.Config.ScriptPath)
	if script == "" {
		script = filepath.Join("scripts", "kova_visual_ocr.py")
	}

	candidates := []string{script}
	if !filepath.IsAbs(script) {
		if executable, executableErr := os.Executable(); executableErr == nil {
			base := filepath.Base(script)
			candidates = append(candidates,
				filepath.Join(filepath.Dir(executable), script),
				filepath.Join(filepath.Dir(executable), "scripts", base),
			)
		}
	}
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate)
		if statErr == nil && !info.IsDir() {
			return python, candidate, nil
		}
	}
	return "", "", fmt.Errorf("OCR bridge is missing: %q", script)
}

// Preflight verifies the Python runtime and package imports before a source
// video is downloaded. It is deliberately a short, dependency-only check:
// the actual OCR model is constructed later in the visible OCR phase.
func (r Runner) Preflight(parent context.Context) (PreflightResult, error) {
	python, script, err := r.resolveBridge()
	if err != nil {
		return PreflightResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, python, script, "--preflight").CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return PreflightResult{}, errors.New("OCR bridge preflight exceeded 40 seconds")
	}
	if err != nil {
		return PreflightResult{}, fmt.Errorf("cannot load OpenCV/Paddle/PaddleOCR: %w\n%s", err, trimLog(string(output)))
	}
	result, err := decodePreflightResult(string(output))
	if err != nil {
		return PreflightResult{}, fmt.Errorf("OCR bridge preflight returned invalid data: %w\n%s", err, trimLog(string(output)))
	}
	if !result.Ready {
		return PreflightResult{}, errors.New("OCR bridge reported not ready")
	}
	return result, nil
}

func (r Runner) Extract(ctx context.Context, request Request) (Result, error) {
	if err := validateRequest(request); err != nil {
		return Result{}, err
	}
	python := strings.TrimSpace(r.Config.PythonPath)
	if python == "" {
		python = "python"
	}
	script := strings.TrimSpace(r.Config.ScriptPath)
	if script == "" {
		script = filepath.Join("scripts", "kova_visual_ocr.py")
	}
	if !filepath.IsAbs(script) {
		if _, statErr := os.Stat(script); os.IsNotExist(statErr) {
			if executable, executableErr := os.Executable(); executableErr == nil {
				portableScript := filepath.Join(filepath.Dir(executable), "scripts", filepath.Base(script))
				if info, portableErr := os.Stat(portableScript); portableErr == nil && !info.IsDir() {
					script = portableScript
				}
			}
		}
	}
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		return Result{}, fmt.Errorf("không tìm thấy OCR bridge %q; cài PaddleOCR rồi chọn script này trong Cài đặt Kova", script)
	}

	device := "cpu"
	if request.PreferGPU {
		device = "gpu"
	}
	result, log, err := r.run(ctx, python, script, request, device)
	if err == nil {
		return result, nil
	}
	if !request.PreferGPU {
		return Result{}, fmt.Errorf("Kova Visual OCR thất bại: %w\n%s", err, trimLog(log))
	}

	// The job remains local and restarts with the same ROI. A CUDA/Paddle
	// install mismatch must not force a user to reselect the video or region.
	result, cpuLog, cpuErr := r.run(ctx, python, script, request, "cpu")
	if cpuErr != nil {
		return Result{}, fmt.Errorf("Kova Visual OCR thất bại trên GPU và CPU: %w\nGPU: %s\nCPU: %s", cpuErr, trimLog(log), trimLog(cpuLog))
	}
	result.FallbackToCPU = true
	return result, nil
}

func (r Runner) run(parent context.Context, python, script string, request Request, device string) (Result, string, error) {
	timeout := r.Config.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	region := request.Region
	args := []string{
		script,
		"--input", request.VideoPath,
		"--output", request.OutputSRTPath,
		"--roi", fmt.Sprintf("%.6f,%.6f,%.6f,%.6f", region.X, region.Y, region.Width, region.Height),
		"--lang", request.Language,
		"--device", device,
		"--interval-ms", fmt.Sprintf("%d", request.SampleIntervalMS),
		"--merge-gap-ms", fmt.Sprintf("%d", request.MergeGapMS),
	}
	command := exec.CommandContext(ctx, python, args...)
	output, err := command.CombinedOutput()
	log := string(output)
	if err != nil {
		return Result{}, log, err
	}
	result, err := decodeResult(log)
	if err != nil {
		return Result{}, log, err
	}
	if result.SRTPath == "" {
		result.SRTPath = request.OutputSRTPath
	}
	if _, err := os.Stat(result.SRTPath); err != nil {
		return Result{}, log, fmt.Errorf("OCR bridge không tạo SRT: %w", err)
	}
	return result, log, nil
}

func validateRequest(request Request) error {
	info, err := os.Stat(strings.TrimSpace(request.VideoPath))
	if err != nil || info.IsDir() {
		return errors.New("chọn một file video hợp lệ cho Visual OCR")
	}
	if strings.TrimSpace(request.OutputSRTPath) == "" {
		return errors.New("chọn đường dẫn output SRT")
	}
	region := request.Region
	if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 || region.X+region.Width > 1 || region.Y+region.Height > 1 {
		return errors.New("vùng OCR phải nằm trong khung video, dùng tọa độ 0 đến 1")
	}
	if request.SampleIntervalMS < 40 || request.SampleIntervalMS > 5000 {
		return errors.New("khoảng quét OCR phải từ 40 đến 5000 ms")
	}
	if request.MergeGapMS < 0 || request.MergeGapMS > 10000 {
		return errors.New("merge gap OCR phải từ 0 đến 10000 ms")
	}
	return nil
}

func decodeResult(log string) (Result, error) {
	lines := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(line), &result); err == nil {
			return result, nil
		}
	}
	return Result{}, errors.New("OCR bridge không trả JSON kết quả")
}

func decodePreflightResult(log string) (PreflightResult, error) {
	lines := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		var result PreflightResult
		if err := json.Unmarshal([]byte(line), &result); err == nil {
			return result, nil
		}
	}
	return PreflightResult{}, errors.New("OCR bridge did not return preflight JSON")
}

func trimLog(log string) string {
	log = strings.TrimSpace(log)
	if len(log) > 1800 {
		return log[len(log)-1800:]
	}
	return log
}
