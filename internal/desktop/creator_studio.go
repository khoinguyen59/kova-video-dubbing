package desktop

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"kova/config"
	"kova/internal/capcutstudio"
	subtitlestyle "kova/internal/subtitle_style"
	"kova/internal/visualocr"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// creatorStudioState is intentionally separate from the dubbing workflow. A
// CapCut draft can be built from local media without starting translation or
// voice cloning, while imported SRT files can still use both Kova services.
type creatorStudioState struct {
	window fyne.Window

	sourceMode        string
	videoPath         string
	imageDirectory    string
	voiceInputs       string
	backgroundInputs  string
	watermarkPath     string
	sourceSRT         string
	targetSRT         string
	outputDirectory   string
	motions           map[capcutstudio.Motion]bool
	transitionEnabled bool
	watermarkX        float64
	watermarkY        float64
	watermarkScale    float64
	watermarkOpacity  float64
	mask              capcutstudio.BlurMask
	maskEnabled       bool
	ocrRegion         visualocr.Region
	ocrVideoPath      string

	sourceStyle     capcutstudio.TextStyle
	targetStyle     capcutstudio.TextStyle
	sourceSubtitleY float64
	targetSubtitleY float64
	sourceSRTField  *widget.Entry
	preview         *logoPositionPad
	status          *widget.Label
	lastSpecPath    string
	compileButton   *widget.Button
}

func newCreatorStudioState(window fyne.Window) *creatorStudioState {
	return &creatorStudioState{
		window: window, sourceMode: "Thư mục ảnh", outputDirectory: config.Conf.Creator.DefaultOutputDir,
		motions:           map[capcutstudio.Motion]bool{capcutstudio.MotionZoomIn: true, capcutstudio.MotionZoomOut: true, capcutstudio.MotionPanLeft: true, capcutstudio.MotionPanRight: true},
		transitionEnabled: true,
		watermarkX:        0.72, watermarkY: -0.72, watermarkScale: 0.22, watermarkOpacity: 0.85,
		mask:        capcutstudio.BlurMask{Shape: capcutstudio.MaskRectangle, X: 0.1, Y: 0.7, Width: 0.8, Height: 0.2},
		maskEnabled: true,
		ocrRegion:   visualocr.Region{X: 0.1, Y: 0.7, Width: 0.8, Height: 0.2},
		sourceStyle: capcutstudio.DefaultSourceStyle(), targetStyle: capcutstudio.DefaultTargetStyle(), sourceSubtitleY: -0.54, targetSubtitleY: -0.72,
	}
}

// CreateCreatorStudioPage mirrors the three-column Kova creation workflow in
// the supplied reference: local media/configuration on the left, a preview in
// the centre, and style/OCR/review controls on the right. It does not start a
// job until a visible user button is pressed.
func CreateCreatorStudioPage(window fyne.Window) fyne.CanvasObject {
	state := newCreatorStudioState(window)
	state.status = widget.NewLabel("Chọn media, kiểm tra preview rồi bấm Tạo spec Kova để review.")
	state.status.Wrapping = fyne.TextWrapWord

	left := createCreatorSourcePanel(state)
	center := createCreatorPreviewPanel(state)
	right := createCreatorRightPanel(state)
	createButton := PrimaryButton("01 · Tạo spec để review", theme.DocumentCreateIcon(), func() { runKovaAutoBuilder(state) })
	createButton.Importance = widget.HighImportance
	compileButton := SecondaryButton("02 · Compile draft đã duyệt", theme.FolderNewIcon(), func() { compileKovaAutoBuilder(state) })
	compileButton.Disable()
	state.compileButton = compileButton

	leftWrap := container.NewGridWrap(fyne.NewSize(315, 760), container.NewVScroll(left))
	rightWrap := container.NewGridWrap(fyne.NewSize(340, 760), container.NewVScroll(right))
	body := container.NewBorder(nil, container.NewPadded(container.NewVBox(state.status, container.NewCenter(container.NewHBox(createButton, compileButton)))), leftWrap, rightWrap, center)
	header := container.NewBorder(nil, nil, nil, widget.NewLabel("Kova Studio · Auto‑Builder / Visual OCR / Style"), TitleText("Dựng project CapCut tự động"))
	return container.NewBorder(header, nil, nil, nil, body)
}

func createCreatorSourcePanel(state *creatorStudioState) fyne.CanvasObject {
	sourceMode := widget.NewRadioGroup([]string{"Thư mục ảnh", "Video đơn"}, nil)
	sourceMode.Horizontal = true

	imageEntry := StyledEntry("Chưa chọn thư mục ảnh")
	videoEntry := StyledEntry("Chưa chọn video nguồn")
	voiceEntry := StyledEntry("Tệp/thư mục voiceover; nhiều mục ngăn cách bằng ;")
	backgroundEntry := StyledEntry("Tệp/thư mục nhạc nền; nhiều mục ngăn cách bằng ;")
	watermarkEntry := StyledEntry("Logo/watermark (tùy chọn)")
	sourceSRTEntry := StyledEntry("SRT gốc (tùy chọn)")
	targetSRTEntry := StyledEntry("SRT dịch (tùy chọn, track riêng)")
	outputEntry := StyledEntry("Thư mục output")
	imageEntry.OnChanged = func(value string) { state.imageDirectory = value }
	videoEntry.OnChanged = func(value string) {
		state.videoPath = value
		if state.ocrVideoPath == "" {
			state.ocrVideoPath = value
		}
	}
	voiceEntry.OnChanged = func(value string) { state.voiceInputs = value }
	backgroundEntry.OnChanged = func(value string) { state.backgroundInputs = value }
	watermarkEntry.OnChanged = func(value string) {
		state.watermarkPath = value
		if state.preview != nil {
			state.preview.SetLogoLabel(filepath.Base(strings.TrimSpace(value)))
		}
	}
	sourceSRTEntry.OnChanged = func(value string) { state.sourceSRT = value }
	targetSRTEntry.OnChanged = func(value string) { state.targetSRT = value }
	outputEntry.OnChanged = func(value string) { state.outputDirectory = value }
	outputEntry.SetText(state.outputDirectory)
	state.sourceSRTField = sourceSRTEntry

	imagePicker := SecondaryButton("Chọn thư mục ảnh", theme.FolderOpenIcon(), func() { chooseFolder(state.window, imageEntry) })
	videoPicker := SecondaryButton("Chọn video nguồn", theme.MediaVideoIcon(), func() { chooseFile(state.window, []string{".mp4", ".mov", ".mkv", ".avi", ".webm"}, videoEntry) })
	voiceFilePicker := GhostButton("Tệp voice", theme.MediaMusicIcon(), func() {
		appendFileSelection(state.window, []string{".wav", ".mp3", ".m4a", ".flac", ".ogg", ".aac"}, voiceEntry)
	})
	voiceFolderPicker := GhostButton("Thư mục voice", theme.FolderOpenIcon(), func() { appendFolderSelection(state.window, voiceEntry) })
	backgroundFilePicker := GhostButton("Tệp nhạc", theme.MediaMusicIcon(), func() {
		appendFileSelection(state.window, []string{".wav", ".mp3", ".m4a", ".flac", ".ogg", ".aac"}, backgroundEntry)
	})
	backgroundFolderPicker := GhostButton("Thư mục nhạc", theme.FolderOpenIcon(), func() { appendFolderSelection(state.window, backgroundEntry) })
	watermarkPicker := GhostButton("Chọn logo", theme.FolderOpenIcon(), func() { chooseFile(state.window, []string{".png", ".jpg", ".jpeg", ".webp", ".bmp"}, watermarkEntry) })
	sourceSRTPicker := GhostButton("Chọn SRT gốc", theme.DocumentIcon(), func() { chooseFile(state.window, []string{".srt"}, sourceSRTEntry) })
	targetSRTPicker := GhostButton("Chọn SRT dịch", theme.DocumentIcon(), func() { chooseFile(state.window, []string{".srt"}, targetSRTEntry) })
	outputPicker := GhostButton("Chọn output", theme.FolderOpenIcon(), func() { chooseFolder(state.window, outputEntry) })
	imageSourceBox := container.NewVBox(imageEntry, imagePicker)
	videoSourceBox := container.NewVBox(videoEntry, videoPicker)
	updateSourceMode := func() {
		if strings.Contains(state.sourceMode, "Video") {
			imageSourceBox.Hide()
			videoSourceBox.Show()
			return
		}
		videoSourceBox.Hide()
		imageSourceBox.Show()
	}
	sourceMode.OnChanged = func(value string) {
		state.sourceMode = value
		updateSourceMode()
		state.status.SetText("Nguồn Auto-Builder: " + value)
	}
	sourceMode.SetSelected(state.sourceMode)
	updateSourceMode()

	motionRows := make([]fyne.CanvasObject, 0, 4)
	for _, motion := range []capcutstudio.Motion{capcutstudio.MotionZoomIn, capcutstudio.MotionZoomOut, capcutstudio.MotionPanLeft, capcutstudio.MotionPanRight} {
		current := motion
		check := widget.NewCheck(string(current), func(checked bool) { state.motions[current] = checked })
		check.SetChecked(true)
		motionRows = append(motionRows, check)
	}
	transitionCheck := widget.NewCheck("Blur transition giữa các ảnh", func(checked bool) { state.transitionEnabled = checked })
	transitionCheck.SetChecked(true)

	cliEntry := StyledEntry("Đường dẫn capcut-cli dist/index.js")
	cliEntry.SetText(config.Conf.Creator.CapCutCLIPath)
	cliEntry.OnChanged = func(value string) { config.Conf.Creator.CapCutCLIPath = value }
	cliPicker := GhostButton("Chọn capcut-cli", theme.FolderOpenIcon(), func() { chooseFile(state.window, []string{".js"}, cliEntry) })
	backend := widget.NewSelect([]string{"pycapcut", "capcut-cli"}, func(value string) { config.Conf.Creator.CompilerBackend = value })
	if config.Conf.Creator.CompilerBackend == "" {
		config.Conf.Creator.CompilerBackend = "pycapcut"
	}
	backend.SetSelected(config.Conf.Creator.CompilerBackend)
	backendHint := widget.NewLabel("pycapcut tạo blur mask Circle/Rectangle thật; cần Python, pycapcut và CapCut Draft Root ở Cài đặt Kova. capcut-cli chỉ dùng khi không có mask.")
	backendHint.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		widget.NewLabel("Nguồn media"), sourceMode,
		imageSourceBox, videoSourceBox,
		widget.NewSeparator(), widget.NewLabel("Voiceover & nhạc nền"),
		voiceEntry, container.NewHBox(voiceFilePicker, voiceFolderPicker),
		backgroundEntry, container.NewHBox(backgroundFilePicker, backgroundFolderPicker),
		widget.NewSeparator(), widget.NewLabel("Logo, SRT & output"),
		watermarkEntry, watermarkPicker, sourceSRTEntry, sourceSRTPicker, targetSRTEntry, targetSRTPicker, outputEntry, outputPicker,
		widget.NewSeparator(), widget.NewLabel("Chuyển động và chuyển cảnh"), container.NewGridWithColumns(2, motionRows...), transitionCheck,
		widget.NewSeparator(), widget.NewLabel("CapCut compiler (bước 02 sau review)"), backend, backendHint, cliEntry, cliPicker,
	)
	return ModernCard("Nguồn & cấu hình", content, GetCurrentThemeIsDark())
}

func createCreatorPreviewPanel(state *creatorStudioState) fyne.CanvasObject {
	preview := newLogoPositionPad(state.watermarkX, state.watermarkY, func(x, y float64) {
		state.watermarkX, state.watermarkY = x, y
		state.status.SetText(fmt.Sprintf("Vị trí logo: X %.2f · Y %.2f", x, y))
	})
	state.preview = preview
	preview.SetMaskChangeCallback(func(mask capcutstudio.BlurMask) {
		state.mask = mask
		state.ocrRegion = visualocr.Region{X: mask.X, Y: mask.Y, Width: mask.Width, Height: mask.Height}
		state.status.SetText(fmt.Sprintf("Vùng OCR / blur mask: X %.2f · Y %.2f · %.2f×%.2f", mask.X, mask.Y, mask.Width, mask.Height))
	})
	preview.SetSubtitlePreview(state.targetStyle, state.targetSubtitleY, "Bản dịch / Vietnamese · Kova preview")

	maskShape := widget.NewRadioGroup([]string{"Tròn", "Vuông"}, func(value string) {
		if value == "Tròn" {
			state.mask.Shape = capcutstudio.MaskCircle
		} else {
			state.mask.Shape = capcutstudio.MaskRectangle
		}
		preview.SetMask(state.mask)
	})
	maskShape.Horizontal = true
	maskShape.SetSelected("Vuông")
	maskX := widget.NewSlider(0, 1)
	maskX.SetValue(state.mask.X)
	maskX.OnChanged = func(value float64) {
		state.mask.X = math.Min(value, 1-state.mask.Width)
		state.ocrRegion.X = state.mask.X
		preview.SetMask(state.mask)
	}
	maskY := widget.NewSlider(0, 1)
	maskY.SetValue(state.mask.Y)
	maskY.OnChanged = func(value float64) {
		state.mask.Y = math.Min(value, 1-state.mask.Height)
		state.ocrRegion.Y = state.mask.Y
		preview.SetMask(state.mask)
	}
	maskW := widget.NewSlider(0.05, 1)
	maskW.SetValue(state.mask.Width)
	maskW.OnChanged = func(value float64) {
		state.mask.Width = math.Min(value, 1-state.mask.X)
		state.ocrRegion.Width = state.mask.Width
		preview.SetMask(state.mask)
	}
	maskH := widget.NewSlider(0.05, 1)
	maskH.SetValue(state.mask.Height)
	maskH.OnChanged = func(value float64) {
		state.mask.Height = math.Min(value, 1-state.mask.Y)
		state.ocrRegion.Height = state.mask.Height
		preview.SetMask(state.mask)
	}
	logoScale := widget.NewSlider(0.02, 1)
	logoScale.SetValue(state.watermarkScale)
	logoScale.OnChanged = func(value float64) { state.watermarkScale = value; preview.SetLogoScale(value) }
	logoOpacity := widget.NewSlider(0, 1)
	logoOpacity.SetValue(state.watermarkOpacity)
	logoOpacity.OnChanged = func(value float64) { state.watermarkOpacity = value; preview.SetLogoOpacity(value) }
	maskEnabled := widget.NewCheck("Dùng vùng này làm Blur Mask khi compile", func(value bool) { state.maskEnabled = value })
	maskEnabled.SetChecked(state.maskEnabled)
	editMode := widget.NewRadioGroup([]string{"Kéo logo", "Vẽ ROI/mask"}, func(value string) { preview.SetMaskEditMode(value == "Vẽ ROI/mask") })
	editMode.Horizontal = true
	editMode.SetSelected("Kéo logo")

	controls := container.NewVBox(
		widget.NewLabel("Chọn Kéo logo hoặc Vẽ ROI/mask, sau đó thao tác trực tiếp trên preview. Vùng đỏ dùng chung cho OCR và blur mask."), editMode,
		container.NewGridWithColumns(2, widget.NewLabel("Logo scale"), logoScale, widget.NewLabel("Logo opacity"), logoOpacity),
		container.NewHBox(widget.NewLabel("Vùng làm mờ / OCR"), maskShape),
		container.NewGridWithColumns(2, widget.NewLabel("X"), maskX, widget.NewLabel("Y"), maskY, widget.NewLabel("Rộng"), maskW, widget.NewLabel("Cao"), maskH),
		maskEnabled,
	)
	return container.NewBorder(widget.NewLabel("MÀN HÌNH XEM TRƯỚC · 16:9"), controls, nil, nil, preview)
}

func createCreatorRightPanel(state *creatorStudioState) fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem("Phong cách", createSubtitleStylePanel(state)),
		container.NewTabItem("Trích xuất OCR", createVisualOCRPanel(state)),
		container.NewTabItem("Review", createCreatorReviewPanel(state)),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return tabs
}

func createSubtitleStylePanel(state *creatorStudioState) fyne.CanvasObject {
	trackSelect := widget.NewSelect([]string{"Bản dịch", "Bản gốc"}, nil)
	trackSelect.SetSelected("Bản dịch")
	fonts := subtitlestyle.SystemFontFamilies()
	fontSelect := widget.NewSelect(fonts, nil)
	fontSize := StyledEntry("Cỡ chữ")
	primaryColor := StyledEntry("#RRGGBB")
	outlineColor := StyledEntry("#RRGGBB")
	outlineWidth := StyledEntry("Độ dày viền")
	backgroundColor := StyledEntry("#RRGGBB")
	backgroundAlpha := widget.NewSlider(0, 1)
	shadowDistance := StyledEntry("Khoảng bóng")
	shadowAlpha := widget.NewSlider(0, 1)
	alignment := widget.NewSelect([]string{"left", "center", "right"}, nil)
	verticalPosition := widget.NewSlider(-1, 1)
	bold := widget.NewCheck("Đậm", nil)
	italic := widget.NewCheck("Nghiêng", nil)
	presetName := StyledEntry("Tên preset")

	activeStyle := func() *capcutstudio.TextStyle {
		if trackSelect.Selected == "Bản gốc" {
			return &state.sourceStyle
		}
		return &state.targetStyle
	}
	updatePreview := func() {
		style := activeStyle()
		if state.preview != nil {
			label, vertical := "Bản dịch / Vietnamese · Kova preview", state.targetSubtitleY
			if trackSelect.Selected == "Bản gốc" {
				label, vertical = "Bản gốc / Original · Kova preview", state.sourceSubtitleY
			}
			state.preview.SetSubtitlePreview(*style, vertical, label)
		}
	}
	refresh := func() {
		style := activeStyle()
		fontSelect.SetSelected(style.FontFamily)
		fontSize.SetText(strconv.Itoa(style.FontSize))
		primaryColor.SetText(style.Color)
		outlineColor.SetText(style.OutlineColor)
		outlineWidth.SetText(fmt.Sprintf("%.2f", style.OutlineWidth))
		backgroundColor.SetText(style.Background)
		backgroundAlpha.SetValue(style.BackgroundAlpha)
		shadowDistance.SetText(fmt.Sprintf("%.2f", style.ShadowDistance))
		shadowAlpha.SetValue(style.ShadowAlpha)
		alignment.SetSelected(style.Alignment)
		if trackSelect.Selected == "Bản gốc" {
			verticalPosition.SetValue(state.sourceSubtitleY)
		} else {
			verticalPosition.SetValue(state.targetSubtitleY)
		}
		bold.SetChecked(style.Bold)
		italic.SetChecked(style.Italic)
		updatePreview()
	}
	trackSelect.OnChanged = func(string) { refresh() }
	fontSelect.OnChanged = func(value string) { activeStyle().FontFamily = value; updatePreview() }
	fontSize.OnChanged = func(value string) {
		if number, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && number >= 8 && number <= 200 {
			activeStyle().FontSize = number
			updatePreview()
		}
	}
	primaryColor.OnChanged = func(value string) {
		if strings.HasPrefix(value, "#") && len(value) == 7 {
			activeStyle().Color = strings.ToUpper(value)
			updatePreview()
		}
	}
	outlineColor.OnChanged = func(value string) {
		if strings.HasPrefix(value, "#") && len(value) == 7 {
			activeStyle().OutlineColor = strings.ToUpper(value)
			updatePreview()
		}
	}
	outlineWidth.OnChanged = func(value string) {
		if number, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && number >= 0 && number <= 30 {
			activeStyle().OutlineWidth = number
			updatePreview()
		}
	}
	backgroundColor.OnChanged = func(value string) {
		if strings.HasPrefix(value, "#") && len(value) == 7 {
			activeStyle().Background = strings.ToUpper(value)
			updatePreview()
		}
	}
	backgroundAlpha.OnChanged = func(value float64) { activeStyle().BackgroundAlpha = value; updatePreview() }
	shadowDistance.OnChanged = func(value string) {
		if number, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && number >= 0 && number <= 30 {
			activeStyle().ShadowDistance = number
			updatePreview()
		}
	}
	shadowAlpha.OnChanged = func(value float64) { activeStyle().ShadowAlpha = value; updatePreview() }
	alignment.OnChanged = func(value string) { activeStyle().Alignment = value; updatePreview() }
	verticalPosition.OnChanged = func(value float64) {
		if trackSelect.Selected == "Bản gốc" {
			state.sourceSubtitleY = value
		} else {
			state.targetSubtitleY = value
		}
		updatePreview()
	}
	bold.OnChanged = func(value bool) { activeStyle().Bold = value; updatePreview() }
	italic.OnChanged = func(value bool) { activeStyle().Italic = value; updatePreview() }

	presetDirectory := filepath.Join("config", "subtitle-presets")
	savePreset := SecondaryButton("Lưu preset", theme.DocumentSaveIcon(), func() {
		set := styleSetFromCreatorState(state)
		path, err := subtitlestyle.SavePreset(presetDirectory, presetName.Text, set)
		if err != nil {
			dialog.ShowError(err, state.window)
			return
		}
		state.status.SetText("Đã lưu preset phụ đề: " + path)
	})
	loadPreset := GhostButton("Áp dụng preset", theme.FolderOpenIcon(), func() {
		presets, err := subtitlestyle.ListPresets(presetDirectory)
		if err != nil {
			dialog.ShowError(err, state.window)
			return
		}
		if len(presets) == 0 {
			dialog.ShowInformation("Preset", "Chưa có preset nào trong config/subtitle-presets.", state.window)
			return
		}
		selected := ""
		selector := widget.NewSelect(presets, func(value string) { selected = value })
		dialog.NewForm("Chọn preset", "Áp dụng", "Hủy", []*widget.FormItem{
			widget.NewFormItem("Preset", selector),
		}, func(confirmed bool) {
			if !confirmed || selected == "" {
				return
			}
			set, loadErr := subtitlestyle.LoadPreset(presetDirectory, selected)
			if loadErr != nil {
				dialog.ShowError(loadErr, state.window)
				return
			}
			applyStyleSetToCreatorState(state, set)
			refresh()
			state.status.SetText("Đã áp dụng preset: " + selected)
		}, state.window).Show()
	})
	refresh()
	return container.NewVBox(
		widget.NewLabel("Live preview ở giữa cập nhật ngay khi bạn đổi style. Font được liệt kê từ máy Windows; tên font được lưu vào draft Kova."),
		trackSelect, fontSelect, fontSize, primaryColor, outlineColor, outlineWidth, backgroundColor,
		container.NewGridWithColumns(2, widget.NewLabel("Nền alpha"), backgroundAlpha, widget.NewLabel("Bóng alpha"), shadowAlpha),
		shadowDistance, alignment, container.NewGridWithColumns(2, widget.NewLabel("Vị trí dọc"), verticalPosition), container.NewHBox(bold, italic), presetName, container.NewHBox(savePreset, loadPreset),
	)
}

func createVisualOCRPanel(state *creatorStudioState) fyne.CanvasObject {
	videoEntry := StyledEntry("Video OCR (mặc định video nguồn)")
	videoEntry.OnChanged = func(value string) { state.ocrVideoPath = value }
	language := widget.NewSelect([]string{"en", "vi", "ch", "japan", "korean", "fr", "de", "es", "ru"}, nil)
	language.SetSelected("en")
	interval := StyledEntry("Khoảng quét ms")
	interval.SetText(strconv.Itoa(config.Conf.VisualOCR.SampleIntervalMS))
	preferGPU := widget.NewCheck("Ưu tiên NVIDIA CUDA, tự fallback CPU", func(value bool) { config.Conf.VisualOCR.PreferGPU = value })
	preferGPU.SetChecked(config.Conf.VisualOCR.PreferGPU)
	picker := SecondaryButton("Chọn video OCR", theme.MediaVideoIcon(), func() { chooseFile(state.window, []string{".mp4", ".mov", ".mkv", ".avi", ".webm"}, videoEntry) })
	start := PrimaryButton("Bắt đầu OCR và tạo SRT", theme.DocumentCreateIcon(), func() {
		video := strings.TrimSpace(videoEntry.Text)
		if video == "" {
			video = strings.TrimSpace(state.ocrVideoPath)
		}
		if video == "" {
			video = strings.TrimSpace(state.videoPath)
		}
		if video == "" {
			dialog.ShowError(fmt.Errorf("chọn video nguồn trước khi OCR"), state.window)
			return
		}
		intervalMS, err := strconv.Atoi(strings.TrimSpace(interval.Text))
		if err != nil {
			dialog.ShowError(fmt.Errorf("khoảng quét OCR không hợp lệ"), state.window)
			return
		}
		base := strings.TrimSpace(state.outputDirectory)
		if base == "" {
			base = config.Conf.Creator.DefaultOutputDir
		}
		output := filepath.Join(base, fmt.Sprintf("ocr-%d.srt", time.Now().UnixNano()))
		request := visualocr.Request{VideoPath: video, OutputSRTPath: output, Region: state.ocrRegion, Language: language.Selected, SampleIntervalMS: intervalMS, PreferGPU: preferGPU.Checked, MergeGapMS: 450}
		state.status.SetText("Đang chạy Visual OCR local…")
		go func() {
			result, runErr := (visualocr.Runner{Config: visualocr.Config{PythonPath: config.Conf.VisualOCR.PythonPath, ScriptPath: config.Conf.VisualOCR.ScriptPath}}).Extract(context.Background(), request)
			if runErr != nil {
				dialog.ShowError(runErr, state.window)
				state.status.SetText("OCR thất bại")
				return
			}
			state.sourceSRT = result.SRTPath
			if state.sourceSRTField != nil {
				state.sourceSRTField.SetText(result.SRTPath)
			}
			state.status.SetText(fmt.Sprintf("OCR hoàn tất: %d cue · %s · %s", result.CueCount, result.Device, result.SRTPath))
			dialog.ShowInformation("Visual OCR hoàn tất", "SRT: "+result.SRTPath, state.window)
		}()
	})
	return container.NewVBox(widget.NewLabel("Vùng đỏ trong preview là ROI OCR. Chỉnh X/Y/rộng/cao ở giữa trước khi chạy."), videoEntry, picker, language, interval, preferGPU, start)
}

func createCreatorReviewPanel(state *creatorStudioState) fyne.CanvasObject {
	return container.NewVBox(
		widget.NewLabel("Checklist trước khi tạo draft"),
		widget.NewLabel("1. Chọn video hoặc thư mục ảnh, không chọn cả hai."),
		widget.NewLabel("2. Voiceover/BGM có thể là file hoặc thư mục; Kova chọn ngẫu nhiên một BGM và lưu random seed."),
		widget.NewLabel("3. SRT gốc và SRT dịch sẽ thành hai text track độc lập."),
		widget.NewLabel("4. Kiểm tra vị trí logo/mask trong preview, sau đó bấm 01 để tạo spec."),
		widget.NewSeparator(),
		widget.NewLabel("Mở và review kova-capcut-draft-spec.json trước. Chỉ sau đó bấm 02 để compile; pycapcut bắt buộc cho blur mask, còn capcut-cli chỉ dùng cho project không có mask."),
	)
}

func runKovaAutoBuilder(state *creatorStudioState) {
	motions := make([]capcutstudio.Motion, 0, len(state.motions))
	for _, motion := range []capcutstudio.Motion{capcutstudio.MotionZoomIn, capcutstudio.MotionZoomOut, capcutstudio.MotionPanLeft, capcutstudio.MotionPanRight} {
		if state.motions[motion] {
			motions = append(motions, motion)
		}
	}
	if len(motions) == 0 {
		dialog.ShowError(fmt.Errorf("chọn ít nhất một motion effect"), state.window)
		return
	}
	source := capcutstudio.Source{}
	if strings.Contains(state.sourceMode, "Video") {
		source.VideoPath = state.videoPath
	} else {
		source.ImageDirectory = state.imageDirectory
	}
	blurMasks := []capcutstudio.BlurMask(nil)
	if state.maskEnabled {
		blurMasks = []capcutstudio.BlurMask{state.mask}
	}
	request := capcutstudio.BuildRequest{
		Name: "Kova Auto Builder", Source: source,
		VoiceoverInputs: splitCreatorInputs(state.voiceInputs), BackgroundInputs: splitCreatorInputs(state.backgroundInputs),
		Motions: motions, TransitionDuration: 0.35, DefaultImageDuration: 3, VoiceoverVolume: 1, BackgroundVolume: 0.35, DuckingRatio: 0.28,
		SourceSRT: state.sourceSRT, TargetSRT: state.targetSRT, SourceLanguage: "source", TargetLanguage: "vi", SourceSubtitleStyle: state.sourceStyle, TargetSubtitleStyle: state.targetStyle, SourceSubtitleY: state.sourceSubtitleY, TargetSubtitleY: state.targetSubtitleY,
		BlurMasks: blurMasks, OutputDir: state.outputDirectory, CompileDraft: false,
	}
	if state.transitionEnabled {
		request.Transition = "blur"
	} else {
		request.Transition = "none"
	}
	if strings.TrimSpace(state.watermarkPath) != "" {
		request.Watermark = &capcutstudio.Watermark{Path: state.watermarkPath, X: state.watermarkX, Y: state.watermarkY, Scale: state.watermarkScale, Opacity: state.watermarkOpacity}
	}
	state.status.SetText("Đang lập timeline Kova và tạo spec để review…")
	go func() {
		result, err := kovaCapCutBuilder().Build(context.Background(), request)
		if err != nil {
			state.status.SetText("Auto‑Builder dừng: " + err.Error())
			dialog.ShowError(fmt.Errorf("%w\nSpec đã tạo (nếu có): %s", err, result.SpecPath), state.window)
			return
		}
		state.lastSpecPath = result.SpecPath
		if state.compileButton != nil {
			state.compileButton.Enable()
		}
		state.status.SetText(fmt.Sprintf("Spec đã sẵn sàng để review: %s · %.2fs · %d ảnh · %d track phụ đề", result.SpecPath, result.TimelineDuration, result.ImageCount, result.SourceSubtitleCount+result.TargetSubtitleCount))
		dialog.ShowInformation("Bước 01 hoàn tất", fmt.Sprintf("Hãy mở và kiểm tra spec trước khi compile:\n%s\n\nSau khi duyệt, bấm 02 · Compile draft đã duyệt.", result.SpecPath), state.window)
	}()
}

func kovaCapCutBuilder() capcutstudio.Builder {
	return capcutstudio.Builder{Config: capcutstudio.Config{
		FFprobePath: config.Conf.Creator.FFprobePath, NodePath: config.Conf.Creator.NodePath, CapCutCLIPath: config.Conf.Creator.CapCutCLIPath,
		CompilerBackend: config.Conf.Creator.CompilerBackend, PythonPath: config.Conf.Creator.PythonPath,
		PyCapCutBridgePath: config.Conf.Creator.PyCapCutBridgePath, CapCutDraftRoot: config.Conf.Creator.CapCutDraftRoot,
	}}
}

func compileKovaAutoBuilder(state *creatorStudioState) {
	specPath := strings.TrimSpace(state.lastSpecPath)
	if specPath == "" {
		dialog.ShowError(fmt.Errorf("tạo và review Kova spec ở bước 01 trước khi compile"), state.window)
		return
	}
	dialog.ShowConfirm("Compile CapCut draft", "Bạn đã kiểm tra SRT, vị trí logo/ROI và timeline trong Kova spec chưa? Compiler sẽ dùng đúng file spec hiện tại, không tạo lại timeline.", func(approved bool) {
		if !approved {
			return
		}
		state.status.SetText("Đang compile đúng spec đã duyệt sang CapCut draft…")
		go func() {
			result, err := kovaCapCutBuilder().CompileSpec(context.Background(), specPath)
			if err != nil {
				state.status.SetText("Compile dừng: " + err.Error())
				dialog.ShowError(fmt.Errorf("%w\nSpec giữ nguyên tại: %s", err, specPath), state.window)
				return
			}
			state.status.SetText("Đã compile CapCut draft: " + result.DraftDirectory)
			dialog.ShowInformation("Bước 02 hoàn tất", fmt.Sprintf("CapCut draft: %s\nBackend: %s", result.DraftDirectory, result.CompilerBackend), state.window)
		}()
	}, state.window)
}

func chooseFile(window fyne.Window, extensions []string, entry *widget.Entry) {
	open := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		if reader == nil {
			return
		}
		path := reader.URI().Path()
		_ = reader.Close()
		entry.SetText(path)
	}, window)
	open.SetFilter(storage.NewExtensionFileFilter(extensions))
	open.Show()
}

func chooseFolder(window fyne.Window, entry *widget.Entry) {
	dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		if uri != nil {
			entry.SetText(uri.Path())
		}
	}, window).Show()
}
func appendFileSelection(window fyne.Window, extensions []string, entry *widget.Entry) {
	open := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		if reader == nil {
			return
		}
		path := reader.URI().Path()
		_ = reader.Close()
		if strings.TrimSpace(entry.Text) != "" {
			path = entry.Text + ";" + path
		}
		entry.SetText(path)
	}, window)
	open.SetFilter(storage.NewExtensionFileFilter(extensions))
	open.Show()
}
func appendFolderSelection(window fyne.Window, entry *widget.Entry) {
	dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		if uri == nil {
			return
		}
		path := uri.Path()
		if strings.TrimSpace(entry.Text) != "" {
			path = entry.Text + ";" + path
		}
		entry.SetText(path)
	}, window).Show()
}
func splitCreatorInputs(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool { return character == ';' || character == '\n' || character == '\r' })
}

func styleSetFromCreatorState(state *creatorStudioState) *subtitlestyle.StyleSet {
	set := subtitlestyle.DefaultStyleSet()
	applyCreatorTextStyle(&set.Horizontal.Major, state.targetStyle)
	applyCreatorTextStyle(&set.Horizontal.Minor, state.sourceStyle)
	set.Horizontal.Major.VerticalPosition = subtitlestyle.Float(state.targetSubtitleY)
	set.Horizontal.Minor.VerticalPosition = subtitlestyle.Float(state.sourceSubtitleY)
	return set
}
func applyStyleSetToCreatorState(state *creatorStudioState, set *subtitlestyle.StyleSet) {
	if set == nil {
		return
	}
	state.targetStyle = creatorTextStyle(set.Horizontal.Major, capcutstudio.DefaultTargetStyle())
	state.sourceStyle = creatorTextStyle(set.Horizontal.Minor, capcutstudio.DefaultSourceStyle())
	if set.Horizontal.Major.VerticalPosition != nil {
		state.targetSubtitleY = *set.Horizontal.Major.VerticalPosition
	}
	if set.Horizontal.Minor.VerticalPosition != nil {
		state.sourceSubtitleY = *set.Horizontal.Minor.VerticalPosition
	}
}
func applyCreatorTextStyle(target *subtitlestyle.Style, source capcutstudio.TextStyle) {
	target.FontName = source.FontFamily
	target.FontSize = subtitlestyle.Int(source.FontSize)
	target.PrimaryColor = source.Color
	target.OutlineColor = source.OutlineColor
	target.BackColor = source.Background
	target.Outline = subtitlestyle.Float(source.OutlineWidth)
	target.Shadow = subtitlestyle.Float(source.ShadowDistance)
	target.Bold = subtitlestyle.Bool(source.Bold)
	target.Italic = subtitlestyle.Bool(source.Italic)
	target.BackgroundAlpha = subtitlestyle.Float(source.BackgroundAlpha)
	target.ShadowAlpha = subtitlestyle.Float(source.ShadowAlpha)
	target.AlignmentValue = subtitlestyle.Int(creatorAlignmentValue(source.Alignment))
}
func creatorTextStyle(source subtitlestyle.Style, fallback capcutstudio.TextStyle) capcutstudio.TextStyle {
	style := fallback
	if source.FontName != "" {
		style.FontFamily = source.FontName
	}
	if source.FontSize != nil {
		style.FontSize = *source.FontSize
	}
	if source.PrimaryColor != "" {
		style.Color = source.PrimaryColor
	}
	if source.OutlineColor != "" {
		style.OutlineColor = source.OutlineColor
	}
	if source.BackColor != "" {
		style.Background = source.BackColor
	}
	if source.Outline != nil {
		style.OutlineWidth = *source.Outline
	}
	if source.Shadow != nil {
		style.ShadowDistance = *source.Shadow
	}
	if source.Bold != nil {
		style.Bold = *source.Bold
	}
	if source.Italic != nil {
		style.Italic = *source.Italic
	}
	if source.BackgroundAlpha != nil {
		style.BackgroundAlpha = *source.BackgroundAlpha
	}
	if source.ShadowAlpha != nil {
		style.ShadowAlpha = *source.ShadowAlpha
	}
	if source.AlignmentValue != nil {
		style.Alignment = creatorAlignmentName(*source.AlignmentValue)
	}
	return style
}
func creatorAlignmentValue(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "left":
		return 1
	case "right":
		return 3
	default:
		return 2
	}
}
func creatorAlignmentName(value int) string {
	switch value {
	case 1, 4, 7:
		return "left"
	case 3, 6, 9:
		return "right"
	default:
		return "center"
	}
}
func parsePreviewColor(value string) color.Color {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return color.White
	}
	red, _ := strconv.ParseUint(value[0:2], 16, 8)
	green, _ := strconv.ParseUint(value[2:4], 16, 8)
	blue, _ := strconv.ParseUint(value[4:6], 16, 8)
	return color.NRGBA{R: uint8(red), G: uint8(green), B: uint8(blue), A: 255}
}

func parsePreviewColorWithAlpha(value string, alpha float64) color.NRGBA {
	base := parsePreviewColor(value)
	rgba, ok := base.(color.NRGBA)
	if !ok {
		return color.NRGBA{R: 255, G: 255, B: 255, A: uint8(255 * clampFloat(alpha, 0, 1))}
	}
	rgba.A = uint8(255 * clampFloat(alpha, 0, 1))
	return rgba
}

// logoPositionPad is a small direct-manipulation preview. It is deliberately
// independent from a source-video decoder: dragging the blue logo changes the
// normalized coordinates Kova will write into the CapCut draft, even before a
// large video needs to be loaded for a full preview.
type logoPositionPad struct {
	widget.BaseWidget
	x, y               float64
	logoScale          float64
	logoOpacity        float64
	mask               capcutstudio.BlurMask
	onPosition         func(float64, float64)
	onMaskChange       func(capcutstudio.BlurMask)
	maskEditMode       bool
	maskDragStart      *fyne.Position
	background         *canvas.Rectangle
	logo               *canvas.Rectangle
	maskOutline        *canvas.Rectangle
	maskCircle         *canvas.Circle
	logoText           *canvas.Text
	subtitleBackground *canvas.Rectangle
	subtitleOutline    [8]*canvas.Text
	subtitleShadow     *canvas.Text
	sampleCaption      *canvas.Text
	fontCaption        *canvas.Image
	fontName           *canvas.Text
	subtitleStyle      capcutstudio.TextStyle
	subtitleY          float64
	content            *fyne.Container
}

func newLogoPositionPad(x, y float64, onPosition func(float64, float64)) *logoPositionPad {
	pad := &logoPositionPad{x: x, y: y, logoScale: 0.22, logoOpacity: 0.85, mask: capcutstudio.BlurMask{X: 0.1, Y: 0.7, Width: 0.8, Height: 0.2}, onPosition: onPosition}
	pad.ExtendBaseWidget(pad)
	pad.background = canvas.NewRectangle(color.NRGBA{R: 20, G: 25, B: 38, A: 255})
	pad.logo = canvas.NewRectangle(color.NRGBA{R: 56, G: 189, B: 248, A: 220})
	pad.logo.CornerRadius = 8
	pad.maskOutline = canvas.NewRectangle(color.Transparent)
	pad.maskOutline.StrokeColor = color.NRGBA{R: 239, G: 68, B: 68, A: 255}
	pad.maskOutline.StrokeWidth = 3
	pad.maskCircle = canvas.NewCircle(color.Transparent)
	pad.maskCircle.StrokeColor = color.NRGBA{R: 239, G: 68, B: 68, A: 255}
	pad.maskCircle.StrokeWidth = 3
	pad.maskCircle.Hide()
	pad.logoText = canvas.NewText("LOGO", color.White)
	pad.logoText.TextSize = 12
	pad.logoText.Alignment = fyne.TextAlignCenter
	pad.sampleCaption = canvas.NewText("Live preview subtitle", color.White)
	pad.sampleCaption.TextSize = 22
	pad.sampleCaption.TextStyle = fyne.TextStyle{Bold: true}
	pad.sampleCaption.Alignment = fyne.TextAlignCenter
	pad.fontCaption = canvas.NewImageFromImage(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	pad.fontCaption.FillMode = canvas.ImageFillContain
	pad.fontCaption.Hide()
	pad.subtitleBackground = canvas.NewRectangle(color.Transparent)
	pad.subtitleBackground.CornerRadius = 7
	pad.subtitleShadow = canvas.NewText("Live preview subtitle", color.NRGBA{R: 0, G: 0, B: 0, A: 150})
	pad.subtitleShadow.Alignment = fyne.TextAlignCenter
	for index := range pad.subtitleOutline {
		pad.subtitleOutline[index] = canvas.NewText("Live preview subtitle", color.Black)
		pad.subtitleOutline[index].Alignment = fyne.TextAlignCenter
	}
	pad.fontName = canvas.NewText("Arial", color.NRGBA{R: 203, G: 213, B: 225, A: 220})
	pad.fontName.TextSize = 11
	pad.fontName.Alignment = fyne.TextAlignCenter
	objects := []fyne.CanvasObject{pad.background, pad.maskOutline, pad.maskCircle, pad.logo, pad.logoText, pad.subtitleBackground}
	for _, outline := range pad.subtitleOutline {
		objects = append(objects, outline)
	}
	objects = append(objects, pad.subtitleShadow, pad.sampleCaption, pad.fontCaption, pad.fontName)
	pad.content = container.NewWithoutLayout(objects...)
	return pad
}
func (pad *logoPositionPad) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(pad.content)
}
func (pad *logoPositionPad) MinSize() fyne.Size            { return fyne.NewSize(520, 292) }
func (pad *logoPositionPad) Resize(size fyne.Size)         { pad.BaseWidget.Resize(size); pad.reflow() }
func (pad *logoPositionPad) Tapped(event *fyne.PointEvent) { pad.setFromPoint(event.Position) }
func (pad *logoPositionPad) Dragged(event *fyne.DragEvent) {
	if pad.maskEditMode {
		if pad.maskDragStart == nil {
			start := fyne.NewPos(event.Position.X-event.Dragged.DX, event.Position.Y-event.Dragged.DY)
			pad.maskDragStart = &start
		}
		pad.setMaskFromPoints(*pad.maskDragStart, event.Position)
		return
	}
	pad.setFromPoint(event.Position)
}
func (pad *logoPositionPad) DragEnd() { pad.maskDragStart = nil }
func (pad *logoPositionPad) SetMaskChangeCallback(callback func(capcutstudio.BlurMask)) {
	pad.onMaskChange = callback
}
func (pad *logoPositionPad) SetMaskEditMode(enabled bool) {
	pad.maskEditMode = enabled
	pad.maskDragStart = nil
}
func (pad *logoPositionPad) SetMask(mask capcutstudio.BlurMask) {
	pad.mask = mask
	if mask.Shape == capcutstudio.MaskCircle {
		pad.maskOutline.Hide()
		pad.maskCircle.Show()
	} else {
		pad.maskCircle.Hide()
		pad.maskOutline.Show()
	}
	pad.reflow()
}
func (pad *logoPositionPad) SetLogoScale(scale float64) { pad.logoScale = scale; pad.reflow() }
func (pad *logoPositionPad) SetLogoLabel(label string) {
	label = strings.TrimSpace(label)
	if label == "" || label == "." {
		label = "LOGO"
	}
	pad.logoText.Text = label
	canvas.Refresh(pad.logoText)
	pad.reflow()
}
func (pad *logoPositionPad) SetLogoOpacity(opacity float64) {
	pad.logoOpacity = opacity
	pad.logo.FillColor = color.NRGBA{R: 56, G: 189, B: 248, A: uint8(255 * opacity)}
	pad.reflow()
}
func (pad *logoPositionPad) SetSubtitlePreview(style capcutstudio.TextStyle, y float64, text string) {
	pad.subtitleStyle = style
	pad.subtitleY = clampFloat(y, -1, 1)
	if strings.TrimSpace(text) == "" {
		text = "Kova subtitle preview"
	}
	textSize := float32(clampFloat(float64(style.FontSize)/2, 12, 72))
	alignment := fyne.TextAlignCenter
	switch strings.ToLower(strings.TrimSpace(style.Alignment)) {
	case "left":
		alignment = fyne.TextAlignLeading
	case "right":
		alignment = fyne.TextAlignTrailing
	}
	textStyle := fyne.TextStyle{Bold: style.Bold, Italic: style.Italic}
	pad.sampleCaption.Text = text
	pad.sampleCaption.Color = parsePreviewColor(style.Color)
	pad.sampleCaption.TextSize = textSize
	pad.sampleCaption.TextStyle = textStyle
	pad.sampleCaption.Alignment = alignment
	canvas.Refresh(pad.sampleCaption)
	if previewImage, err := renderSystemFontCaption(style.FontFamily, text, textSize, parsePreviewColor(style.Color)); err == nil {
		pad.fontCaption.Image = previewImage
		pad.fontCaption.Show()
		pad.fontCaption.Refresh()
		pad.sampleCaption.Hide()
	} else {
		pad.fontCaption.Hide()
		pad.sampleCaption.Show()
	}
	pad.subtitleShadow.Text = text
	pad.subtitleShadow.Color = parsePreviewColorWithAlpha(style.ShadowColor, style.ShadowAlpha)
	pad.subtitleShadow.TextSize = textSize
	pad.subtitleShadow.TextStyle = textStyle
	pad.subtitleShadow.Alignment = alignment
	canvas.Refresh(pad.subtitleShadow)
	pad.subtitleBackground.FillColor = parsePreviewColorWithAlpha(style.Background, style.BackgroundAlpha)
	canvas.Refresh(pad.subtitleBackground)
	for _, outline := range pad.subtitleOutline {
		outline.Text = text
		outline.Color = parsePreviewColor(style.OutlineColor)
		outline.TextSize = textSize
		outline.TextStyle = textStyle
		outline.Alignment = alignment
		canvas.Refresh(outline)
	}
	pad.fontName.Text = "Font: " + valueOrPreview(style.FontFamily, "Arial") + " · viền " + fmt.Sprintf("%.1f", style.OutlineWidth)
	canvas.Refresh(pad.fontName)
	pad.reflow()
}
func (pad *logoPositionPad) setFromPoint(point fyne.Position) {
	if pad.maskEditMode {
		pad.setMaskFromPoints(point, point)
		return
	}
	size := pad.Size()
	if size.Width <= 0 || size.Height <= 0 {
		return
	}
	pad.x = clampFloat(2*float64(point.X/size.Width)-1, -1, 1)
	pad.y = clampFloat(1-2*float64(point.Y/size.Height), -1, 1)
	pad.reflow()
	if pad.onPosition != nil {
		pad.onPosition(pad.x, pad.y)
	}
}
func (pad *logoPositionPad) setMaskFromPoints(start, end fyne.Position) {
	size := pad.Size()
	if size.Width <= 0 || size.Height <= 0 {
		return
	}
	left := clampFloat(math.Min(float64(start.X), float64(end.X))/float64(size.Width), 0, 1)
	top := clampFloat(math.Min(float64(start.Y), float64(end.Y))/float64(size.Height), 0, 1)
	right := clampFloat(math.Max(float64(start.X), float64(end.X))/float64(size.Width), 0, 1)
	bottom := clampFloat(math.Max(float64(start.Y), float64(end.Y))/float64(size.Height), 0, 1)
	// A tap selects a practical default area; a drag overwrites it with the
	// actual user-drawn rectangle. This avoids an invalid zero-sized OCR ROI.
	if right-left < 0.02 || bottom-top < 0.02 {
		width, height := math.Min(0.35, 1-left), math.Min(0.16, 1-top)
		pad.mask.X, pad.mask.Y, pad.mask.Width, pad.mask.Height = left, top, width, height
	} else {
		pad.mask.X, pad.mask.Y, pad.mask.Width, pad.mask.Height = left, top, right-left, bottom-top
	}
	pad.SetMask(pad.mask)
	if pad.onMaskChange != nil {
		pad.onMaskChange(pad.mask)
	}
}
func (pad *logoPositionPad) reflow() {
	if pad.content == nil {
		return
	}
	size := pad.Size()
	if size.Width <= 0 || size.Height <= 0 {
		return
	}
	pad.background.Resize(size)
	maskX := float32(pad.mask.X) * size.Width
	maskY := float32(pad.mask.Y) * size.Height
	maskW := float32(pad.mask.Width) * size.Width
	maskH := float32(pad.mask.Height) * size.Height
	pad.maskOutline.Move(fyne.NewPos(maskX, maskY))
	pad.maskOutline.Resize(fyne.NewSize(maskW, maskH))
	pad.maskCircle.Move(fyne.NewPos(maskX, maskY))
	pad.maskCircle.Resize(fyne.NewSize(maskW, maskH))
	logoW := float32(clampFloat(pad.logoScale, 0.02, 1)) * size.Width
	logoH := logoW * 0.55
	centerX := float32((pad.x+1)/2) * size.Width
	centerY := float32((1-pad.y)/2) * size.Height
	pad.logo.Move(fyne.NewPos(clampFloat32(centerX-logoW/2, 0, size.Width-logoW), clampFloat32(centerY-logoH/2, 0, size.Height-logoH)))
	pad.logo.Resize(fyne.NewSize(logoW, logoH))
	pad.logoText.Move(pad.logo.Position().Add(fyne.NewPos(0, logoH/2-8)))
	pad.logoText.Resize(fyne.NewSize(logoW, 16))
	captionW := size.Width * 0.9
	captionH := float32(clampFloat(float64(pad.sampleCaption.TextSize)*1.35, 26, 104))
	captionX := (size.Width - captionW) / 2
	centerCaptionY := float32((1-pad.subtitleY)/2) * size.Height
	captionY := clampFloat32(centerCaptionY-captionH/2, 4, size.Height-captionH-4)
	pad.subtitleBackground.Move(fyne.NewPos(captionX-8, captionY-4))
	pad.subtitleBackground.Resize(fyne.NewSize(captionW+16, captionH+8))
	outlineOffset := float32(clampFloat(pad.subtitleStyle.OutlineWidth, 0, 7))
	offsets := [][2]float32{{-outlineOffset, 0}, {outlineOffset, 0}, {0, -outlineOffset}, {0, outlineOffset}, {-outlineOffset, -outlineOffset}, {-outlineOffset, outlineOffset}, {outlineOffset, -outlineOffset}, {outlineOffset, outlineOffset}}
	for index, outline := range pad.subtitleOutline {
		offset := offsets[index]
		outline.Move(fyne.NewPos(captionX+offset[0], captionY+offset[1]))
		outline.Resize(fyne.NewSize(captionW, captionH))
	}
	shadowOffset := float32(clampFloat(pad.subtitleStyle.ShadowDistance, 0, 16))
	pad.subtitleShadow.Move(fyne.NewPos(captionX+shadowOffset, captionY+shadowOffset))
	pad.subtitleShadow.Resize(fyne.NewSize(captionW, captionH))
	pad.sampleCaption.Move(fyne.NewPos(captionX, captionY))
	pad.sampleCaption.Resize(fyne.NewSize(captionW, captionH))
	if pad.fontCaption.Visible() && pad.fontCaption.Image != nil {
		bounds := pad.fontCaption.Image.Bounds()
		imageH := captionH
		imageW := imageH
		if bounds.Dy() > 0 {
			imageW = imageH * float32(bounds.Dx()) / float32(bounds.Dy())
		}
		imageW = clampFloat32(imageW, 1, captionW)
		imageX := captionX + (captionW-imageW)/2
		switch strings.ToLower(strings.TrimSpace(pad.subtitleStyle.Alignment)) {
		case "left":
			imageX = captionX
		case "right":
			imageX = captionX + captionW - imageW
		}
		pad.fontCaption.Move(fyne.NewPos(imageX, captionY))
		pad.fontCaption.Resize(fyne.NewSize(imageW, imageH))
	}
	pad.fontName.Move(fyne.NewPos(captionX, clampFloat32(captionY-17, 0, size.Height-16)))
	pad.fontName.Resize(fyne.NewSize(captionW, 14))
	pad.Refresh()
}
func valueOrPreview(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// renderSystemFontCaption uses the same Windows font file that appeared in
// Kova's selector. Fyne canvas.Text has no arbitrary-family field, therefore
// this small raster layer is what makes the live preview's selected font real
// instead of merely displaying its name in a label.
func renderSystemFontCaption(family, text string, size float32, foreground color.Color) (image.Image, error) {
	path, exists := subtitlestyle.FindSystemFontFile(family)
	if !exists {
		return nil, fmt.Errorf("không tìm thấy font %q", family)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	collection, err := opentype.ParseCollectionReaderAt(file)
	if err != nil {
		return nil, err
	}
	var faceFont *opentype.Font
	for index := 0; index < collection.NumFonts(); index++ {
		candidate, candidateErr := collection.Font(index)
		if candidateErr != nil {
			continue
		}
		name, nameErr := candidate.Name(nil, sfnt.NameIDFamily)
		if nameErr == nil && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(family)) {
			faceFont = candidate
			break
		}
		if faceFont == nil {
			faceFont = candidate
		}
	}
	if faceFont == nil {
		return nil, fmt.Errorf("font collection không có face")
	}
	face, err := opentype.NewFace(faceFont, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	metrics := face.Metrics()
	drawer := &font.Drawer{Face: face}
	width := drawer.MeasureString(text).Ceil() + 8
	height := metrics.Height.Ceil() + 8
	if width < 1 || height < 1 {
		return nil, fmt.Errorf("font preview có kích thước không hợp lệ")
	}
	bitmap := image.NewNRGBA(image.Rect(0, 0, width, height))
	drawer.Dst = bitmap
	drawer.Src = image.NewUniform(foreground)
	drawer.Dot = fixed.P(4, metrics.Ascent.Ceil()+4)
	drawer.DrawString(text)
	return bitmap, nil
}
func clampFloat(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
func clampFloat32(value, minimum, maximum float32) float32 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
