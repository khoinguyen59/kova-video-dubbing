package capcutstudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var imageExtensions = map[string]bool{
	".bmp": true, ".jpeg": true, ".jpg": true, ".png": true, ".webp": true,
}

var audioExtensions = map[string]bool{
	".aac": true, ".flac": true, ".m4a": true, ".mp3": true, ".ogg": true, ".wav": true,
}

type Builder struct{ Config Config }

// CompileSpec compiles a previously generated, user-reviewable Kova spec. It
// never regenerates the timeline, random BGM selection, SRT text or mask
// coordinates, so an approval step between Build and compilation is real.
func (b Builder) CompileSpec(ctx context.Context, specPath string) (BuildResult, error) {
	specPath = strings.TrimSpace(specPath)
	if err := requireFile(specPath); err != nil {
		return BuildResult{}, fmt.Errorf("Kova draft spec: %w", err)
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return BuildResult{}, err
	}
	var spec DraftSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return BuildResult{}, fmt.Errorf("Kova draft spec không hợp lệ: %w", err)
	}
	maskCount := metadataListLength(spec.Metadata, "blur_masks")
	request := BuildRequest{Name: spec.Name, BlurMasks: make([]BlurMask, maskCount)}
	result := BuildResult{SpecPath: specPath, CompilerBackend: b.compilerBackend(), BlurMaskCount: maskCount}
	draftDir, log, compileErr := b.compile(ctx, specPath, filepath.Dir(specPath), request)
	if compileErr != nil {
		return result, compileErr
	}
	result.DraftDirectory, result.Compiled, result.CompileLog = draftDir, true, log
	return result, nil
}

func metadataListLength(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	value, exists := metadata[key]
	if !exists {
		return 0
	}
	if list, ok := value.([]any); ok {
		return len(list)
	}
	// A manually edited spec might deserialize through a different concrete
	// slice type. Re-marshal only this field rather than assuming its type.
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	var list []json.RawMessage
	if json.Unmarshal(encoded, &list) != nil {
		return 0
	}
	return len(list)
}

func (b Builder) Build(ctx context.Context, request BuildRequest) (BuildResult, error) {
	request = normalizedRequest(request)
	if err := validateRequest(request); err != nil {
		return BuildResult{}, err
	}

	images, video, err := resolveSource(request.Source)
	if err != nil {
		return BuildResult{}, err
	}
	voiceovers, err := expandMediaInputs(request.VoiceoverInputs, audioExtensions)
	if err != nil {
		return BuildResult{}, fmt.Errorf("voiceover: %w", err)
	}
	backgrounds, err := expandMediaInputs(request.BackgroundInputs, audioExtensions)
	if err != nil {
		return BuildResult{}, fmt.Errorf("nhạc nền: %w", err)
	}

	seed := request.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))

	voiceDurations := make([]float64, len(voiceovers))
	voiceTotal := 0.0
	for index, path := range voiceovers {
		duration, probeErr := b.probeDuration(ctx, path)
		if probeErr != nil {
			return BuildResult{}, probeErr
		}
		voiceDurations[index] = duration
		voiceTotal += duration
	}

	selectedBackground := ""
	backgroundDuration := 0.0
	if len(backgrounds) > 0 {
		selectedBackground = backgrounds[rng.Intn(len(backgrounds))]
		backgroundDuration, err = b.probeDuration(ctx, selectedBackground)
		if err != nil {
			return BuildResult{}, err
		}
	}

	timelineDuration := 0.0
	sourceMode := "images"
	if video != "" {
		sourceMode = "video"
		timelineDuration, err = b.probeDuration(ctx, video)
		if err != nil {
			return BuildResult{}, err
		}
		if voiceTotal > timelineDuration+0.05 {
			return BuildResult{}, fmt.Errorf("voiceover dài %.2fs vượt video nguồn %.2fs", voiceTotal, timelineDuration)
		}
	} else {
		timelineDuration = voiceTotal
		if timelineDuration == 0 {
			timelineDuration = backgroundDuration
		}
		if timelineDuration == 0 {
			timelineDuration = request.DefaultImageDuration * float64(len(images))
		}
	}
	if timelineDuration <= 0 {
		return BuildResult{}, errors.New("không xác định được thời lượng timeline")
	}

	tracks := make([]DraftTrack, 0, 6)
	operations := make([]map[string]any, 0)
	visualItems, visualOps := makeVisualTimeline(images, video, timelineDuration, request.Motions, request.Transition, request.TransitionDuration, rng)
	tracks = append(tracks, DraftTrack{Type: "video", Name: "kova_source", Items: visualItems})
	operations = append(operations, visualOps...)

	voiceItems, voiceRanges := makeVoiceTrack(voiceovers, voiceDurations, request.VoiceoverVolume)
	if len(voiceItems) > 0 {
		tracks = append(tracks, DraftTrack{Type: "audio", Name: "voiceover", Items: voiceItems})
	}

	backgroundItems := make([]map[string]any, 0)
	if selectedBackground != "" {
		backgroundItems = loopAudio(selectedBackground, backgroundDuration, timelineDuration, request.BackgroundVolume)
		tracks = append(tracks, DraftTrack{Type: "audio", Name: "background_music", Items: backgroundItems})
		operations = append(operations, duckingOperations(backgroundItems, voiceRanges, request.BackgroundVolume, request.BackgroundVolume*request.DuckingRatio)...)
	}

	if request.Watermark != nil {
		tracks = append(tracks, DraftTrack{Type: "video", Name: "watermark", Items: []map[string]any{{
			"ref": "watermark", "path": request.Watermark.Path, "type": "photo", "start": 0.0, "duration": round(timelineDuration),
			"x": request.Watermark.X, "y": request.Watermark.Y, "scale": request.Watermark.Scale, "opacity": request.Watermark.Opacity,
		}}})
	}

	maskOps, err := maskOperations(request.BlurMasks, timelineDuration)
	if err != nil {
		return BuildResult{}, err
	}
	operations = append(operations, maskOps...)

	sourceCues, err := loadSRT(request.SourceSRT)
	if err != nil {
		return BuildResult{}, fmt.Errorf("SRT gốc: %w", err)
	}
	targetCues, err := loadSRT(request.TargetSRT)
	if err != nil {
		return BuildResult{}, fmt.Errorf("SRT bản dịch: %w", err)
	}
	if len(sourceCues) > 0 {
		tracks = append(tracks, DraftTrack{Type: "text", Name: "subtitles_source_" + safeLanguage(request.SourceLanguage, "source"), Items: textItems(sourceCues, "source", request.SourceSubtitleStyle, request.SourceSubtitleY)})
	}
	if len(targetCues) > 0 {
		tracks = append(tracks, DraftTrack{Type: "text", Name: "subtitles_target_" + safeLanguage(request.TargetLanguage, "vi"), Items: textItems(targetCues, "target", request.TargetSubtitleStyle, request.TargetSubtitleY)})
	}

	spec := DraftSpec{
		Name: request.Name, Width: request.Width, Height: request.Height, FPS: request.FPS, Tracks: tracks, Operations: operations,
		Metadata: map[string]any{
			"producer": "Kova Desktop", "source_mode": sourceMode, "random_seed": seed,
			"timeline_duration": round(timelineDuration), "dual_subtitle_tracks": len(sourceCues) > 0 && len(targetCues) > 0,
			"source_language": request.SourceLanguage, "target_language": request.TargetLanguage,
			// Blur masks deliberately remain Kova metadata instead of being emitted
			// as an undocumented capcut-cli operation. The pycapcut compiler reads
			// these instructions and creates a duplicated, blurred, masked visual
			// layer. capcut-cli is refused when masks are requested.
			"blur_masks": request.BlurMasks, "blur_masks_require_backend": "pycapcut",
		},
	}

	outputDir, err := prepareOutputDir(request.OutputDir)
	if err != nil {
		return BuildResult{}, err
	}
	specPath := filepath.Join(outputDir, "kova-capcut-draft-spec.json")
	encoded, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.WriteFile(specPath, encoded, 0o644); err != nil {
		return BuildResult{}, err
	}

	result := BuildResult{
		SpecPath: specPath, TimelineDuration: round(timelineDuration), SourceMode: sourceMode, ImageCount: len(images), VoiceoverCount: len(voiceovers),
		BackgroundPath: selectedBackground, BackgroundLoops: len(backgroundItems), WatermarkApplied: request.Watermark != nil, BlurMaskCount: len(request.BlurMasks),
		SourceSubtitleCount: len(sourceCues), TargetSubtitleCount: len(targetCues), RandomSeed: seed,
		CompilerBackend: b.compilerBackend(),
	}
	for _, operation := range operations {
		switch operation["op"] {
		case "transition":
			result.TransitionCount++
		case "keyframe":
			if operation["property"] == "volume" {
				result.DuckingKeyframes++
			} else {
				result.MotionOperationCount++
			}
		}
	}
	if request.CompileDraft {
		draftDir, log, compileErr := b.compile(ctx, specPath, outputDir, request)
		if compileErr != nil {
			return result, compileErr
		}
		result.DraftDirectory, result.Compiled, result.CompileLog = draftDir, true, log
	}
	return result, nil
}

func normalizedRequest(request BuildRequest) BuildRequest {
	if strings.TrimSpace(request.Name) == "" {
		request.Name = "Kova Auto Builder"
	}
	if request.DefaultImageDuration <= 0 {
		request.DefaultImageDuration = 3
	}
	if request.VoiceoverVolume <= 0 {
		request.VoiceoverVolume = 1
	}
	if request.BackgroundVolume <= 0 {
		request.BackgroundVolume = 0.35
	}
	if request.DuckingRatio <= 0 || request.DuckingRatio > 1 {
		request.DuckingRatio = 0.28
	}
	if len(request.Motions) == 0 {
		request.Motions = []Motion{MotionZoomIn, MotionZoomOut, MotionPanLeft, MotionPanRight}
	}
	if strings.TrimSpace(request.Transition) == "" {
		request.Transition = "blur"
	}
	if request.TransitionDuration <= 0 {
		request.TransitionDuration = 0.35
	}
	if request.Width <= 0 {
		request.Width = 1920
	}
	if request.Height <= 0 {
		request.Height = 1080
	}
	if request.FPS <= 0 {
		request.FPS = 30
	}
	if request.SourceSubtitleStyle.FontFamily == "" {
		request.SourceSubtitleStyle = DefaultSourceStyle()
	}
	if request.TargetSubtitleStyle.FontFamily == "" {
		request.TargetSubtitleStyle = DefaultTargetStyle()
	}
	if request.SourceSubtitleY == 0 {
		request.SourceSubtitleY = -0.54
	}
	if request.TargetSubtitleY == 0 {
		request.TargetSubtitleY = -0.72
	}
	return request
}

func validateRequest(request BuildRequest) error {
	if request.DefaultImageDuration < 0.25 || request.DefaultImageDuration > 120 {
		return errors.New("thời lượng ảnh mặc định phải từ 0.25 đến 120 giây")
	}
	if request.BackgroundVolume < 0 || request.BackgroundVolume > 2 || request.VoiceoverVolume < 0 || request.VoiceoverVolume > 2 {
		return errors.New("âm lượng phải nằm trong khoảng 0 đến 2")
	}
	if request.DuckingRatio < 0 || request.DuckingRatio > 1 {
		return errors.New("ducking ratio phải nằm trong khoảng 0 đến 1")
	}
	if request.TransitionDuration < 0 || request.TransitionDuration > 3 {
		return errors.New("thời lượng transition phải từ 0 đến 3 giây")
	}
	if math.Abs(request.SourceSubtitleY-request.TargetSubtitleY) < 0.05 {
		return errors.New("hai track phụ đề phải có vị trí dọc khác nhau")
	}
	if request.SourceSubtitleY < -1 || request.SourceSubtitleY > 1 || request.TargetSubtitleY < -1 || request.TargetSubtitleY > 1 {
		return errors.New("vị trí dọc phụ đề phải nằm trong khoảng -1 đến 1")
	}
	if watermark := request.Watermark; watermark != nil {
		if err := requireFile(watermark.Path); err != nil {
			return fmt.Errorf("watermark: %w", err)
		}
		if !imageExtensions[strings.ToLower(filepath.Ext(watermark.Path))] {
			return errors.New("watermark phải là PNG/JPG/WEBP/BMP")
		}
		if watermark.X < -1 || watermark.X > 1 || watermark.Y < -1 || watermark.Y > 1 || watermark.Scale < 0.02 || watermark.Scale > 2 || watermark.Opacity < 0 || watermark.Opacity > 1 {
			return errors.New("tọa độ hoặc scale/opacity watermark không hợp lệ")
		}
	}
	for _, motion := range request.Motions {
		if motion != MotionZoomIn && motion != MotionZoomOut && motion != MotionPanLeft && motion != MotionPanRight && motion != MotionNone {
			return fmt.Errorf("motion không hỗ trợ: %s", motion)
		}
	}
	return nil
}

func resolveSource(source Source) (images []string, video string, err error) {
	if strings.TrimSpace(source.VideoPath) != "" {
		if source.ImageDirectory != "" || len(source.ImagePaths) != 0 {
			return nil, "", errors.New("chọn một video hoặc ảnh, không chọn cả hai")
		}
		if err := requireFile(source.VideoPath); err != nil {
			return nil, "", err
		}
		return nil, source.VideoPath, nil
	}
	if source.ImageDirectory != "" {
		paths, readErr := expandDirectory(source.ImageDirectory, imageExtensions)
		if readErr != nil {
			return nil, "", readErr
		}
		images = append(images, paths...)
	}
	for _, image := range source.ImagePaths {
		if err := requireFile(image); err != nil {
			return nil, "", err
		}
		if !imageExtensions[strings.ToLower(filepath.Ext(image))] {
			return nil, "", fmt.Errorf("ảnh không hỗ trợ: %s", image)
		}
		images = append(images, image)
	}
	if len(images) == 0 {
		return nil, "", errors.New("chọn một video hoặc thư mục ảnh")
	}
	sort.Strings(images)
	return images, "", nil
}

func expandMediaInputs(inputs []string, allowed map[string]bool) ([]string, error) {
	paths := make([]string, 0)
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		info, err := os.Stat(input)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			items, readErr := expandDirectory(input, allowed)
			if readErr != nil {
				return nil, readErr
			}
			paths = append(paths, items...)
			continue
		}
		if !allowed[strings.ToLower(filepath.Ext(input))] {
			return nil, fmt.Errorf("định dạng audio không hỗ trợ: %s", input)
		}
		paths = append(paths, input)
	}
	sort.Strings(paths)
	return paths, nil
}

func expandDirectory(directory string, allowed map[string]bool) ([]string, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("không phải thư mục: %s", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && allowed[strings.ToLower(filepath.Ext(entry.Name()))] {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("không tìm thấy media hợp lệ trong %s", directory)
	}
	sort.Strings(paths)
	return paths, nil
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("cần file, không phải thư mục: %s", path)
	}
	return nil
}

func (b Builder) probeDuration(ctx context.Context, path string) (float64, error) {
	ffprobe := strings.TrimSpace(b.Config.FFprobePath)
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	command := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("không đọc được thời lượng %s bằng ffprobe: %w", filepath.Base(path), err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("thời lượng media không hợp lệ: %s", path)
	}
	return duration, nil
}

func makeVisualTimeline(images []string, video string, duration float64, motions []Motion, transition string, transitionDuration float64, rng *rand.Rand) ([]map[string]any, []map[string]any) {
	if video != "" {
		return []map[string]any{{"ref": "source-video", "path": video, "start": 0.0, "duration": round(duration), "type": "video"}}, nil
	}
	items, operations := make([]map[string]any, 0, len(images)), make([]map[string]any, 0)
	cursor, perImage := 0.0, duration/float64(len(images))
	for index, image := range images {
		itemDuration := perImage
		if index == len(images)-1 {
			itemDuration = duration - cursor
		}
		ref := fmt.Sprintf("scene-%03d", index+1)
		items = append(items, map[string]any{"ref": ref, "path": image, "start": round(cursor), "duration": round(itemDuration), "type": "photo"})
		operations = append(operations, motionOperations(ref, itemDuration, motions[rng.Intn(len(motions))])...)
		if index < len(images)-1 && transition != "" && transition != "none" && transitionDuration > 0 {
			operations = append(operations, map[string]any{"op": "transition", "target": ref, "slug": transition, "duration": round(math.Min(transitionDuration, itemDuration/2))})
		}
		cursor += itemDuration
	}
	return items, operations
}

func motionOperations(ref string, duration float64, motion Motion) []map[string]any {
	if motion == MotionNone {
		return nil
	}
	points := [][3]any{}
	switch motion {
	case MotionZoomIn:
		points = append(points, [3]any{"uniform_scale", 0.0, 1.0}, [3]any{"uniform_scale", duration, 1.14})
	case MotionZoomOut:
		points = append(points, [3]any{"uniform_scale", 0.0, 1.14}, [3]any{"uniform_scale", duration, 1.0})
	case MotionPanLeft:
		points = append(points, [3]any{"uniform_scale", 0.0, 1.1}, [3]any{"uniform_scale", duration, 1.1}, [3]any{"position_x", 0.0, 0.06}, [3]any{"position_x", duration, -0.06})
	case MotionPanRight:
		points = append(points, [3]any{"uniform_scale", 0.0, 1.1}, [3]any{"uniform_scale", duration, 1.1}, [3]any{"position_x", 0.0, -0.06}, [3]any{"position_x", duration, 0.06})
	}
	operations := make([]map[string]any, 0, len(points))
	for _, point := range points {
		operations = append(operations, map[string]any{"op": "keyframe", "target": ref, "property": point[0], "time": round(point[1].(float64)), "value": point[2], "easing": "ease-in-out"})
	}
	return operations
}

func makeVoiceTrack(paths []string, durations []float64, volume float64) ([]map[string]any, [][2]float64) {
	items, ranges := make([]map[string]any, 0, len(paths)), make([][2]float64, 0, len(paths))
	cursor := 0.0
	for index, path := range paths {
		duration := durations[index]
		items = append(items, map[string]any{"ref": fmt.Sprintf("voiceover-%03d", index+1), "path": path, "start": round(cursor), "duration": round(duration), "volume": volume})
		ranges = append(ranges, [2]float64{cursor, cursor + duration})
		cursor += duration
	}
	return items, ranges
}

func loopAudio(path string, sourceDuration, timelineDuration, volume float64) []map[string]any {
	items := make([]map[string]any, 0)
	for cursor, index := 0.0, 1; cursor < timelineDuration-0.001; index++ {
		duration := math.Min(sourceDuration, timelineDuration-cursor)
		items = append(items, map[string]any{"ref": fmt.Sprintf("background-%03d", index), "path": path, "start": round(cursor), "duration": round(duration), "sourceStart": 0.0, "volume": volume})
		cursor += duration
	}
	return items
}

func duckingOperations(background []map[string]any, ranges [][2]float64, normal, ducked float64) []map[string]any {
	operations := make([]map[string]any, 0)
	const attack, release = 0.08, 0.25
	for _, item := range background {
		start, duration := item["start"].(float64), item["duration"].(float64)
		end := start + duration
		points := map[float64]float64{0: normal, round(duration): normal}
		for _, itemRange := range ranges {
			left, right := math.Max(start, itemRange[0]), math.Min(end, itemRange[1])
			if right <= left {
				continue
			}
			localLeft, localRight := left-start, right-start
			points[round(math.Max(0, localLeft-attack))] = normal
			points[round(localLeft)] = ducked
			points[round(localRight)] = ducked
			points[round(math.Min(duration, localRight+release))] = normal
		}
		keys := make([]float64, 0, len(points))
		for key := range points {
			keys = append(keys, key)
		}
		sort.Float64s(keys)
		for _, key := range keys {
			operations = append(operations, map[string]any{"op": "keyframe", "target": item["ref"], "property": "volume", "time": key, "value": points[key], "easing": "ease-in-out"})
		}
	}
	return operations
}

func maskOperations(masks []BlurMask, duration float64) ([]map[string]any, error) {
	for index, mask := range masks {
		if mask.Shape != MaskCircle && mask.Shape != MaskRectangle {
			return nil, fmt.Errorf("mask %d phải là circle hoặc rectangle", index+1)
		}
		if mask.X < 0 || mask.Y < 0 || mask.Width <= 0 || mask.Height <= 0 || mask.X+mask.Width > 1 || mask.Y+mask.Height > 1 {
			return nil, fmt.Errorf("tọa độ mask %d không hợp lệ", index+1)
		}
		start, end := mask.Start, mask.End
		if start < 0 || start >= duration {
			return nil, fmt.Errorf("thời điểm bắt đầu mask %d không hợp lệ", index+1)
		}
		if end == 0 {
			end = duration
		}
		if end <= start || end > duration+0.05 {
			return nil, fmt.Errorf("thời điểm kết thúc mask %d không hợp lệ", index+1)
		}
	}
	// ``capcut-cli compile`` has no mask operation in its public contract.
	// Returning no generic operations keeps a capcut-cli-only spec valid; the
	// fully described masks are retained in kova_metadata for pycapcut.
	return nil, nil
}

type subtitleCue struct {
	ID         int
	Start, End float64
	Text       string
}

func loadSRT(path string) ([]subtitleCue, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blocks := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n")
	cues := make([]subtitleCue, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 3 {
			continue
		}
		timing := strings.Split(lines[1], "-->")
		if len(timing) != 2 {
			return nil, fmt.Errorf("mốc thời gian không hợp lệ: %s", lines[1])
		}
		start, e1 := parseSRTTime(strings.TrimSpace(timing[0]))
		end, e2 := parseSRTTime(strings.TrimSpace(timing[1]))
		if e1 != nil || e2 != nil || end <= start {
			return nil, fmt.Errorf("mốc thời gian không hợp lệ: %s", lines[1])
		}
		id, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
		text := strings.TrimSpace(strings.Join(lines[2:], "\n"))
		if text != "" {
			cues = append(cues, subtitleCue{ID: id, Start: start, End: end, Text: text})
		}
	}
	return cues, nil
}
func parseSRTTime(raw string) (float64, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ':' || r == ',' || r == '.' })
	if len(parts) != 4 {
		return 0, errors.New("sai định dạng")
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	s, e3 := strconv.Atoi(parts[2])
	ms, e4 := strconv.Atoi(parts[3])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return 0, errors.New("sai số")
	}
	return float64(h*3600+m*60+s) + float64(ms)/1000, nil
}
func textItems(cues []subtitleCue, prefix string, style TextStyle, y float64) []map[string]any {
	items := make([]map[string]any, 0, len(cues))
	for _, cue := range cues {
		items = append(items, map[string]any{"ref": fmt.Sprintf("%s-subtitle-%04d", prefix, cue.ID), "text": cue.Text, "start": round(cue.Start), "duration": round(cue.End - cue.Start), "x": 0.0, "y": y, "fontFamily": style.FontFamily, "fontSize": style.FontSize, "color": style.Color, "bold": style.Bold, "italic": style.Italic, "borderWidth": style.OutlineWidth, "borderColor": style.OutlineColor, "bgColor": style.Background, "bgAlpha": style.BackgroundAlpha, "shadow": style.ShadowDistance, "shadowColor": style.ShadowColor, "shadowAlpha": style.ShadowAlpha, "alignment": style.Alignment})
	}
	return items
}
func safeLanguage(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}
func prepareOutputDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join("output", fmt.Sprintf("kova-auto-%d", time.Now().UnixNano()))
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}
func (b Builder) compilerBackend() string {
	backend := strings.ToLower(strings.TrimSpace(b.Config.CompilerBackend))
	if backend == "" {
		return "capcut-cli"
	}
	return backend
}

func (b Builder) compile(ctx context.Context, specPath, outputDir string, request BuildRequest) (string, string, error) {
	backend := b.compilerBackend()
	if backend == "pycapcut" {
		return b.compilePyCapCut(ctx, specPath, request.Name)
	}
	if backend != "capcut-cli" {
		return "", "", fmt.Errorf("compiler backend không hỗ trợ: %s (chọn pycapcut hoặc capcut-cli)", backend)
	}
	if len(request.BlurMasks) > 0 {
		return "", "", errors.New("blur mask Circle/Rectangle cần backend pycapcut. Kova đã lưu spec nhưng không compile một draft thiếu censor; vào Cài đặt Kova, chọn pycapcut và đặt thư mục CapCut Draft Root")
	}
	node, cli := strings.TrimSpace(b.Config.NodePath), strings.TrimSpace(b.Config.CapCutCLIPath)
	if node == "" {
		node = "node"
	}
	if cli == "" {
		return "", "", errors.New("chưa cấu hình capcut_cli_path; Kova đã tạo spec nhưng chưa thể compile draft")
	}
	if err := requireFile(cli); err != nil {
		return "", "", fmt.Errorf("capcut_cli_path: %w", err)
	}
	draftDir := filepath.Join(outputDir, "capcut-draft")
	timeout := b.Config.CompileTimeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	compileCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(compileCtx, node, cli, "compile", specPath, "--out", draftDir)
	out, err := command.CombinedOutput()
	if err != nil {
		return "", string(out), fmt.Errorf("capcut-cli compile thất bại: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return draftDir, strings.TrimSpace(string(out)), nil
}

type pyCapCutCompileResult struct {
	DraftDirectory string   `json:"draft_directory"`
	Warnings       []string `json:"warnings"`
}

func (b Builder) compilePyCapCut(ctx context.Context, specPath, draftName string) (string, string, error) {
	python := strings.TrimSpace(b.Config.PythonPath)
	if python == "" {
		python = "python"
	}
	bridge := strings.TrimSpace(b.Config.PyCapCutBridgePath)
	if bridge == "" {
		bridge = filepath.Join("scripts", "kova_pycapcut_builder.py")
	}
	if err := requireFile(bridge); err != nil {
		return "", "", fmt.Errorf("pycapcut bridge: %w", err)
	}
	draftRoot := strings.TrimSpace(b.Config.CapCutDraftRoot)
	if draftRoot == "" {
		return "", "", errors.New("chưa cấu hình capcut_draft_root cho pycapcut; chọn đúng thư mục Draft của CapCut trong Cài đặt Kova trước khi compile")
	}
	info, err := os.Stat(draftRoot)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("capcut_draft_root không phải thư mục hợp lệ: %s", draftRoot)
	}
	timeout := b.Config.CompileTimeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	compileCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(compileCtx, python, bridge, "--spec", specPath, "--draft-root", draftRoot, "--draft-name", draftName)
	out, err := command.CombinedOutput()
	log := strings.TrimSpace(string(out))
	if err != nil {
		return "", log, fmt.Errorf("pycapcut compile thất bại: %w: %s", err, log)
	}
	result, decodeErr := decodePyCapCutResult(log)
	if decodeErr != nil {
		return "", log, decodeErr
	}
	if strings.TrimSpace(result.DraftDirectory) == "" {
		return "", log, errors.New("pycapcut không trả đường dẫn draft CapCut")
	}
	return result.DraftDirectory, log, nil
}

func decodePyCapCutResult(log string) (pyCapCutCompileResult, error) {
	for _, line := range strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		var result pyCapCutCompileResult
		if err := json.Unmarshal([]byte(line), &result); err == nil && result.DraftDirectory != "" {
			return result, nil
		}
	}
	return pyCapCutCompileResult{}, errors.New("pycapcut bridge không trả JSON kết quả")
}
func round(value float64) float64 { return math.Round(value*1000000) / 1000000 }
