package desktop

import (
	"fmt"
	"image/color"
	"kova/config"
	"kova/internal/deps"
	"kova/internal/server"
	"kova/log"
	"kova/pkg/omnivoice"
	"kova/pkg/util"
	"kova/static"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.uber.org/zap"
)

// The Kova notebook is published in the Kova product repository so one click
// can open the exact notebook in Google Colab. It is intentionally separate
// from the short-lived worker tunnel URL that the notebook creates at runtime.
const kovaOmniVoiceColabNotebookURL = "https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb"

// openKovaColabNotebook prefers Chrome because the Colab flow is intended to
// continue in the user's existing Chrome/Google session. It falls back to the
// operating system's default browser only when Chrome cannot be located.
func openKovaColabNotebook() error {
	url := kovaOmniVoiceColabNotebookURL
	candidates := []string{"chrome.exe", "chrome"}
	for _, root := range []string{
		os.Getenv("PROGRAMFILES"),
		os.Getenv("PROGRAMFILES(X86)"),
		os.Getenv("LOCALAPPDATA"),
	} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if err := exec.Command(path, "--new-window", url).Start(); err != nil {
			return fmt.Errorf("không thể mở Chrome: %w", err)
		}
		return nil
	}
	if app := fyne.CurrentApp(); app != nil {
		app.OpenURL(parseURL(url))
		return nil
	}
	return fmt.Errorf("không tìm thấy Chrome và không có trình duyệt mặc định để mở Colab")
}

// CreateConfigTab builds Kova's application, service, transcription and TTS settings.
func CreateConfigTab(window fyne.Window) fyne.CanvasObject {
	pageTitle := TitleText("Cài đặt Kova / Kova settings")

	appGroup := createAppConfigGroup()
	serverGroup := createServerConfigGroup()
	transcribeGroup := createTranscribeConfigGroup()
	ttsGroup := createTtsConfigGroup(window)
	creatorGroup := createCreatorToolingConfigGroup(window)

	var background *canvas.LinearGradient
	if GetCurrentThemeIsDark() {
		background = canvas.NewLinearGradient(
			color.NRGBA{R: 15, G: 23, B: 42, A: 255},
			color.NRGBA{R: 30, G: 41, B: 59, A: 255},
			0.0,
		)
	} else {
		background = canvas.NewLinearGradient(
			color.NRGBA{R: 248, G: 250, B: 252, A: 255},
			color.NRGBA{R: 241, G: 245, B: 249, A: 255},
			0.0,
		)
	}

	spacer1 := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	spacer1.SetMinSize(fyne.NewSize(0, 15))
	spacer2 := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	spacer2.SetMinSize(fyne.NewSize(0, 15))

	configContainer := container.NewVBox(
		container.NewPadded(pageTitle),
		spacer1,
		container.NewPadded(appGroup),
		container.NewPadded(serverGroup),
		container.NewPadded(transcribeGroup),
		container.NewPadded(ttsGroup),
		container.NewPadded(creatorGroup),
		spacer2,
	)

	scroll := container.NewScroll(configContainer)

	configStack := container.NewStack(background, scroll)

	return container.NewPadded(configStack)
}

// Shared configuration controls let the Kova provider presets update the form.
var llmProviderSelectRef *widget.Select
var llmBaseUrlEntryRef *widget.Entry
var llmModelEntryRef *widget.Entry
var llmModelSelectRef *widget.Select

func CreateLlmTab() fyne.CanvasObject {
	pageTitle := TitleText("Model dịch và API Gateway")

	llmConfigCard := createLlmConfigGroup()
	providersCard := createApiProvidersCard()
	guideCard := createLlmGuideCard()

	var background *canvas.LinearGradient
	if GetCurrentThemeIsDark() {
		background = canvas.NewLinearGradient(
			color.NRGBA{R: 15, G: 23, B: 42, A: 255},
			color.NRGBA{R: 30, G: 41, B: 59, A: 255},
			0.0,
		)
	} else {
		background = canvas.NewLinearGradient(
			color.NRGBA{R: 248, G: 250, B: 252, A: 255},
			color.NRGBA{R: 241, G: 245, B: 249, A: 255},
			0.0,
		)
	}

	spacer1 := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	spacer1.SetMinSize(fyne.NewSize(0, 15))
	spacer2 := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	spacer2.SetMinSize(fyne.NewSize(0, 15))

	llmContainer := container.NewVBox(
		container.NewPadded(pageTitle),
		spacer1,
		container.NewPadded(providersCard),
		container.NewPadded(llmConfigCard),
		container.NewPadded(guideCard),
		spacer2,
	)

	scroll := container.NewScroll(llmContainer)
	llmStack := container.NewStack(background, scroll)

	return container.NewPadded(llmStack)
}

// createApiProvidersCard exposes Kova-owned presets instead of inherited vendor marketing.
func createApiProvidersCard() *fyne.Container {
	setProvider := func(provider, baseURL string, models []string) {
		config.Conf.Llm.Provider = provider
		if llmProviderSelectRef != nil {
			llmProviderSelectRef.SetSelected(provider)
		}
		if llmBaseUrlEntryRef != nil {
			llmBaseUrlEntryRef.SetText(baseURL)
		}
		if llmModelSelectRef != nil {
			llmModelSelectRef.Options = models
			llmModelSelectRef.Refresh()
			if len(models) > 0 {
				llmModelSelectRef.SetSelected(models[0])
				if llmModelEntryRef != nil {
					llmModelEntryRef.SetText(models[0])
				}
			} else {
				if llmModelEntryRef != nil {
					llmModelEntryRef.SetText("")
				}
			}
		}
	}
	kovaGatewayCard := createProviderCard(
		"Kova API Gateway",
		"Gateway free-tier đã kiểm chứng cho KOVA",
		"",
		color.NRGBA{R: 99, G: 54, B: 231, A: 255},
		"kova",
		func() {
			models := config.GatewayFreeLLMModels()
			choices := make([]string, 0, len(models))
			for _, model := range models {
				choices = append(choices, model.ID)
			}
			setProvider("openai-compatible", config.KOVAGatewayBaseURL, choices)
		},
	)

	providersGrid := container.New(
		layout.NewGridLayoutWithColumns(1),
		kovaGatewayCard,
	)

	return GlassmorphismCard(
		"Preset model/API của Kova",
		"KOVA dùng gateway đã cấu hình sẵn; danh sách dịch chỉ gồm các model free đã xác minh. API key lấy từ biến môi trường hoặc config cục bộ bị Git bỏ qua.",
		providersGrid,
		GetCurrentThemeIsDark(),
	)
}

func getProviderIcon(provider string) fyne.CanvasObject {
	var pngPath string
	switch provider {
	case "openai":
		pngPath = "source/openai.png"
	case "ollama", "kova":
		pngPath = "source/deepseek-color.png"
	default:
		return container.NewWithoutLayout()
	}

	data, err := static.EmbeddedFiles.ReadFile(pngPath)
	if err != nil {
		log.GetLogger().Error("Failed to load PNG icon", zap.String("path", pngPath), zap.Error(err))
		return container.NewWithoutLayout()
	}

	res := fyne.NewStaticResource(pngPath, data)
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(24, 24))
	img.Resize(fyne.NewSize(24, 24))
	return img
}

func createProviderCard(name, description, url string, accentColor color.Color, provider string, onTap func()) *fyne.Container {
	isDark := GetCurrentThemeIsDark()

	var bgColor color.Color
	var textColor color.Color
	var descColor color.Color
	var shadowColor color.Color
	var hoverBgColor color.Color

	if isDark {
		bgColor = color.NRGBA{R: 51, G: 65, B: 85, A: 120}
		hoverBgColor = color.NRGBA{R: 71, G: 85, B: 105, A: 150}
		textColor = color.NRGBA{R: 248, G: 250, B: 252, A: 255}
		descColor = color.NRGBA{R: 148, G: 163, B: 184, A: 255}
		shadowColor = color.NRGBA{R: 0, G: 0, B: 0, A: 60}
	} else {
		bgColor = color.NRGBA{R: 255, G: 255, B: 255, A: 200}
		hoverBgColor = color.NRGBA{R: 245, G: 247, B: 250, A: 220}
		textColor = color.NRGBA{R: 17, G: 24, B: 39, A: 255}
		descColor = color.NRGBA{R: 107, G: 114, B: 128, A: 255}
		shadowColor = color.NRGBA{R: 0, G: 0, B: 0, A: 30}
	}

	shadow := canvas.NewRectangle(shadowColor)
	shadow.CornerRadius = 12
	shadow.Move(fyne.NewPos(2, 2))

	background := canvas.NewRectangle(bgColor)
	background.CornerRadius = 12
	background.StrokeColor = accentColor
	background.StrokeWidth = 2

	icon := getProviderIcon(provider)
	topPadding := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	topPadding.SetMinSize(fyne.NewSize(0, 12))
	iconContainer := container.NewCenter(icon)

	nameLabel := canvas.NewText(name, textColor)
	nameLabel.TextSize = 16
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	nameLabel.Alignment = fyne.TextAlignCenter

	descLabel := canvas.NewText(description, descColor)
	descLabel.TextSize = 12
	descLabel.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(
		topPadding,
		iconContainer,
		container.NewPadded(nameLabel),
		container.NewPadded(descLabel),
	)

	card := container.NewStack(shadow, background, content)
	card.Resize(fyne.NewSize(200, 100))

	clickableArea := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	clickableArea.Resize(fyne.NewSize(200, 100))

	tappable := &tappableObject{
		rect: clickableArea,
		onTap: func() {
			originalPos := card.Position()
			originalShadowPos := shadow.Position()

			card.Move(fyne.NewPos(originalPos.X+1, originalPos.Y+1))
			shadow.Move(fyne.NewPos(originalShadowPos.X+1, originalShadowPos.Y+1))

			background.FillColor = hoverBgColor
			background.Refresh()

			if onTap != nil {
				onTap()
			} else {
				if app := fyne.CurrentApp(); app != nil && url != "" {
					app.OpenURL(parseURL(url))
				}
			}

			go func() {
				time.Sleep(150 * time.Millisecond)
				card.Move(fyne.NewPos(0, 0))
				shadow.Move(fyne.NewPos(2, 2))
				background.FillColor = bgColor
				background.Refresh()
			}()
		},
		onHover: func(hovering bool) {
			if hovering {
				background.FillColor = hoverBgColor
				background.StrokeWidth = 3
				shadow.Move(fyne.NewPos(3, 3))
				background.Refresh()
			} else {
				background.FillColor = bgColor
				background.StrokeWidth = 2
				shadow.Move(fyne.NewPos(2, 2))
				background.Refresh()
			}
		},
	}

	finalContainer := container.NewStack(card, tappable)

	return finalContainer
}

// createLlmGuideCard documents the Kova API contract.
func createLlmGuideCard() *fyne.Container {
	guideText := `# Thiết kế API của Kova / Kova API design

## LLM dịch phụ đề
- **KOVA API Gateway**: endpoint OpenAI-compatible mặc định http://3.27.172.90/v1.
- Chỉ dùng các model free đã xác minh; mặc định: **oc/deepseek-v4-flash-free**.
- Máy không cần cài Ollama hoặc tải model local.

## TTS
- **KOVA Voice Studio (OmniVoice)**: chỉ worker GPU Google Colab qua URL HTTPS, dùng một audio tham chiếu có quyền sử dụng để giữ giọng cố định.
- **KOVA API Gateway preset TTS**: endpoint /v1/audio/speech, chọn Google TTS hoặc Edge TTS bằng danh sách xổ xuống.

## Bảo mật / Security
- Không ghi API key thật vào repository hoặc report.
- Ưu tiên biến môi trường; cấu hình trong app chỉ dành cho gateway riêng của người dùng.`

	guideLabel := widget.NewRichTextFromMarkdown(guideText)
	guideLabel.Wrapping = fyne.TextWrapWord

	return GlassmorphismCard(
		"Luồng kết nối / Connection guide",
		"Hợp đồng model, gateway và bảo mật của Kova",
		guideLabel,
		GetCurrentThemeIsDark(),
	)
}

func parseURL(urlStr string) *url.URL {
	u, err := url.Parse(urlStr)
	if err != nil {
		log.GetLogger().Error("Failed to parse URL", zap.Error(err))
		return nil
	}
	return u
}

// WorkflowTabs owns the persistent pages and the one shared job controller.
// Keeping the controller separate from page 05 means the primary action is
// available on the source page and in the fixed bottom action bar as well.
type WorkflowTabs struct {
	Pages  []fyne.CanvasObject
	runner *workflowRunner
	stage  *stagedWorkflowRunner
}

type workflowRunner struct {
	window  fyne.Window
	sm      *SubtitleManager
	buttons []*widget.Button
	status  *widget.Label
	mu      sync.Mutex
	running bool
}

func newWorkflowRunner(window fyne.Window, sm *SubtitleManager) *workflowRunner {
	runner := &workflowRunner{
		window: window,
		sm:     sm,
		status: widget.NewLabel("Sẵn sàng. Dán URL hoặc chọn video, rồi bấm Bắt đầu xử lý."),
	}
	runner.status.Wrapping = fyne.TextWrapWord
	sm.SetTaskFinishedCallback(runner.finish)
	return runner
}

func (runner *workflowRunner) newActionButton(label string) *widget.Button {
	button := widget.NewButtonWithIcon(label, theme.MediaPlayIcon(), runner.Start)
	button.Importance = widget.HighImportance
	runner.mu.Lock()
	runner.buttons = append(runner.buttons, button)
	runner.mu.Unlock()
	return button
}

func (runner *workflowRunner) setRunState(running bool, message string) {
	runner.mu.Lock()
	runner.running = running
	buttons := append([]*widget.Button(nil), runner.buttons...)
	runner.mu.Unlock()

	for _, button := range buttons {
		if running {
			button.Disable()
		} else {
			button.Enable()
		}
	}
	if strings.TrimSpace(message) != "" {
		runner.status.SetText(message)
	}
}

// Start is intentionally shared by every visible call-to-action. It never
// hides the buttons: a failed preflight or job simply re-enables them so users
// can correct the configuration and retry from the same screen.
func (runner *workflowRunner) Start() {
	if strings.TrimSpace(runner.sm.GetVideoUrl()) == "" {
		dialog.ShowError(fmt.Errorf("hãy chọn video hoặc dán URL YouTube/youtu.be trước khi bắt đầu"), runner.window)
		runner.status.SetText("Chưa có nguồn video. Dán URL rồi bấm Bắt đầu xử lý.")
		return
	}

	runner.mu.Lock()
	if runner.running {
		runner.mu.Unlock()
		return
	}
	runner.running = true
	runner.mu.Unlock()
	runner.setRunState(true, "Đang kiểm tra cấu hình và chuẩn bị pipeline…")
	runner.sm.PrepareRun()

	go runner.run()
}

func (runner *workflowRunner) run() {
	if err := config.CheckConfig(); err != nil {
		log.GetLogger().Error("Invalid Kova configuration", zap.Error(err))
		runner.finish(fmt.Errorf("cấu hình chưa hợp lệ: %w", err))
		return
	}
	if err := runner.validateRequestedVoice(); err != nil {
		runner.finish(err)
		return
	}
	// A public YouTube URL uses the platform VTT path in this desktop workflow;
	// do not block it on a local FasterWhisper binary/model that will not run.
	// Non-YouTube inputs keep the full ASR dependency check.
	dependencyCheck := deps.CheckDependency
	if util.IsYouTubeURL(strings.TrimSpace(runner.sm.videoUrl)) {
		dependencyCheck = deps.CheckPlatformSubtitleDependency
	}
	if err := dependencyCheck(); err != nil {
		log.GetLogger().Error("Failed to prepare dependencies", zap.Error(err))
		runner.finish(fmt.Errorf("không chuẩn bị được dependency: %w", err))
		return
	}

	if config.ConfigBackup != config.Conf {
		if err := server.StopBackend(); err != nil {
			runner.finish(fmt.Errorf("không thể dừng dịch vụ Kova: %w", err))
			return
		}
		go func() {
			if err := server.StartBackend(); err != nil {
				log.GetLogger().Error("Failed to restart Kova service", zap.Error(err))
			}
		}()
		if err := waitForKovaBackend(config.Conf.Server.Host, config.Conf.Server.Port, 8*time.Second); err != nil {
			runner.finish(err)
			return
		}
		config.ConfigBackup = config.Conf
	}

	if err := runner.sm.StartTask(); err != nil {
		runner.finish(err)
		return
	}
	runner.status.SetText("Job đã gửi. Kova đang tạo artifact; tiến trình xuất hiện ở bước 05.")
}

// validateRequestedVoice keeps remote voice cloning explicit before a job is
// submitted. It only reads the Colab worker health endpoint; it never uploads
// a reference clip or creates a profile on this desktop.
func (runner *workflowRunner) validateRequestedVoice() error {
	if !runner.sm.voiceoverEnabled || !strings.EqualFold(strings.TrimSpace(config.Conf.Tts.Provider), "omnivoice") {
		return nil
	}
	if strings.TrimSpace(runner.sm.voiceoverAudioPath) == "" {
		return fmt.Errorf("chọn audio mẫu ở bước 03 trước khi lồng tiếng bằng OmniVoice")
	}
	if !runner.sm.voiceCloneConsent {
		return fmt.Errorf("xác nhận quyền sử dụng audio mẫu ở bước 03 trước khi clone giọng")
	}
	if err := config.ValidateRemoteOmniVoiceWorker(); err != nil {
		return err
	}
	if config.Conf.Tts.Omnivoice.RequireCUDA {
		if _, err := omnivoice.ProbeColabGPUWithAPIKey(config.Conf.Tts.Omnivoice.BaseUrl, config.Conf.Tts.Omnivoice.SessionApiKey, 12*time.Second); err != nil {
			return fmt.Errorf("worker OmniVoice Colab chưa sẵn sàng: %w", err)
		}
	}
	return nil
}

func waitForKovaBackend(host string, port int, timeout time.Duration) error {
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 400*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("dịch vụ Kova chưa sẵn sàng sau %s", timeout)
}

func (runner *workflowRunner) finish(err error) {
	if err != nil {
		runner.setRunState(false, "Job chưa hoàn tất: "+err.Error()+". Bạn có thể chỉnh cấu hình và bấm Bắt đầu xử lý lại.")
		dialog.ShowError(err, runner.window)
		return
	}
	runner.setRunState(false, "Hoàn tất. Các nút lưu từng artifact nằm ở bước 05.")
}

// stagedWorkflowRunner is the native, review-first workflow controller.  The
// legacy workflowRunner above remains for integrations that still call the old
// endpoint, but the Kova desktop does not mount any control that invokes it.
// Every expensive operation below is initiated by its own visible button and
// stops at a user-review state.
type stagedWorkflowRunner struct {
	window fyne.Window
	sm     *SubtitleManager

	mu           sync.Mutex
	busy         bool
	taskID       string
	snapshot     WorkflowSnapshot
	voiceSkipped bool

	status            *widget.Label
	sourceStatus      *widget.Label
	translationStatus *widget.Label
	voiceStatus       *widget.Label
	renderStatus      *widget.Label

	sourceEditor        *widget.Entry
	translatedEditor    *widget.Entry
	sourceLoadedURL     string
	translatedLoadedURL string

	sourceStartButton         *widget.Button
	sourceReviewButton        *widget.Button
	sourceSaveButton          *widget.Button
	sourceApproveButton       *widget.Button
	translationStartButton    *widget.Button
	translationReviewButton   *widget.Button
	translationSaveButton     *widget.Button
	translationApproveButton  *widget.Button
	dubbingStartButton        *widget.Button
	dubbingSkipButton         *widget.Button
	dubbingApproveButton      *widget.Button
	dubbingVideoStartButton   *widget.Button
	dubbingVideoApproveButton *widget.Button
	renderStartButton         *widget.Button
	restoreLatestButton       *widget.Button
	newJobButton              *widget.Button

	// These cards are intentionally hidden while their corresponding review is
	// waiting.  This keeps the review CTA above the fold on a normal desktop
	// window instead of forcing the user to hunt for an editor below a large
	// source/configuration card.
	sourceInputSection         fyne.CanvasObject
	translationSettingsSection fyne.CanvasObject
}

func newStagedWorkflowRunner(window fyne.Window, sm *SubtitleManager) *stagedWorkflowRunner {
	runner := &stagedWorkflowRunner{
		window: window,
		sm:     sm,
		status: widget.NewLabel("Sẵn sàng. Mỗi bước chỉ chạy khi bạn bấm nút Bắt đầu của bước đó."),
	}
	for _, label := range []*widget.Label{runner.status} {
		label.Wrapping = fyne.TextWrapWord
	}
	runner.sourceStatus = runner.newStageStatus("Chưa tạo SRT nguồn.")
	runner.translationStatus = runner.newStageStatus("Chờ bạn duyệt SRT nguồn trước khi dịch.")
	runner.voiceStatus = runner.newStageStatus("Tùy chọn: chỉ chạy khi bạn yêu cầu lồng tiếng.")
	runner.renderStatus = runner.newStageStatus("Chờ SRT bản dịch đã được duyệt.")

	runner.sourceEditor = widget.NewMultiLineEntry()
	runner.sourceEditor.SetPlaceHolder("Sau bước 01, SRT/script nguồn sẽ hiển thị ở đây để bạn kiểm tra và sửa.")
	runner.sourceEditor.Wrapping = fyne.TextWrapWord
	runner.sourceEditor.SetMinRowsVisible(12)
	runner.sourceEditor.Disable()
	runner.translatedEditor = widget.NewMultiLineEntry()
	runner.translatedEditor.SetPlaceHolder("Sau khi bấm Bắt đầu dịch, SRT tiếng Việt sẽ hiển thị ở đây để bạn kiểm tra và sửa.")
	runner.translatedEditor.Wrapping = fyne.TextWrapWord
	runner.translatedEditor.SetMinRowsVisible(12)
	runner.translatedEditor.Disable()

	runner.sourceStartButton = widget.NewButtonWithIcon("Bắt đầu bước 01: tạo SRT nguồn", theme.MediaPlayIcon(), runner.StartSource)
	runner.sourceStartButton.Importance = widget.HighImportance
	runner.sourceReviewButton = widget.NewButtonWithIcon("Kiểm tra & duyệt SRT nguồn", theme.ConfirmIcon(), runner.OpenSourceReview)
	runner.sourceReviewButton.Importance = widget.HighImportance
	runner.sourceSaveButton = widget.NewButton("Lưu bản SRT nguồn đã sửa", runner.SaveSource)
	runner.sourceApproveButton = widget.NewButtonWithIcon("Lưu & duyệt SRT nguồn", theme.ConfirmIcon(), runner.ApproveSource)
	runner.sourceApproveButton.Importance = widget.HighImportance

	runner.translationStartButton = widget.NewButtonWithIcon("Bắt đầu bước 02: dịch SRT", theme.MediaPlayIcon(), runner.StartTranslation)
	runner.translationStartButton.Importance = widget.HighImportance
	runner.translationReviewButton = widget.NewButtonWithIcon("Kiểm tra & duyệt bản dịch", theme.ConfirmIcon(), runner.OpenTranslationReview)
	runner.translationReviewButton.Importance = widget.HighImportance
	runner.translationSaveButton = widget.NewButton("Lưu bản dịch đã sửa", runner.SaveTranslation)
	runner.translationApproveButton = widget.NewButtonWithIcon("Lưu & duyệt bản dịch", theme.ConfirmIcon(), runner.ApproveTranslation)
	runner.translationApproveButton.Importance = widget.HighImportance

	runner.dubbingStartButton = widget.NewButtonWithIcon("Bắt đầu bước 03a: tạo audio lồng tiếng", theme.MediaPlayIcon(), runner.StartDubbing)
	runner.dubbingStartButton.Importance = widget.HighImportance
	runner.dubbingSkipButton = widget.NewButton("Bỏ qua lồng tiếng cho job này", runner.SkipDubbing)
	runner.dubbingApproveButton = widget.NewButtonWithIcon("Duyệt audio lồng tiếng", theme.ConfirmIcon(), runner.ApproveDubbing)
	runner.dubbingApproveButton.Importance = widget.HighImportance
	runner.dubbingVideoStartButton = widget.NewButtonWithIcon("Bắt đầu bước 03b: ghép audio vào video", theme.MediaPlayIcon(), runner.StartDubbingVideo)
	runner.dubbingVideoStartButton.Importance = widget.HighImportance
	runner.dubbingVideoApproveButton = widget.NewButtonWithIcon("Duyệt video lồng tiếng", theme.ConfirmIcon(), runner.ApproveDubbingVideo)
	runner.dubbingVideoApproveButton.Importance = widget.HighImportance

	runner.renderStartButton = widget.NewButtonWithIcon("Bắt đầu bước 04: xuất MP4 cuối", theme.MediaPlayIcon(), runner.StartRender)
	runner.renderStartButton.Importance = widget.HighImportance
	runner.restoreLatestButton = widget.NewButton("Khôi phục job gần nhất / Restore latest job", runner.RestoreLatestWorkflow)
	runner.newJobButton = widget.NewButtonWithIcon("Job mới / New job", theme.ContentAddIcon(), runner.NewJob)
	runner.refreshControls()
	return runner
}

func (runner *stagedWorkflowRunner) newStageStatus(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}

func (runner *stagedWorkflowRunner) SourceReviewPanel() fyne.CanvasObject {
	return ModernCard("Kiểm tra SRT / script nguồn", container.NewVBox(
		widget.NewLabel("Bước 01 dừng tại đây. Mở kiểm tra để xem, sửa, lưu và duyệt SRT/script nguồn trước khi dịch."),
		runner.sourceReviewButton,
	), GetCurrentThemeIsDark())
}

func (runner *stagedWorkflowRunner) TranslationReviewPanel() fyne.CanvasObject {
	return ModernCard("Kiểm tra SRT / script bản dịch", container.NewVBox(
		widget.NewLabel("Bước 02 dừng tại đây. Mở kiểm tra để chỉnh tiếng Việt, tên riêng và thời lượng đọc trước khi duyệt sang bước lồng tiếng/xuất MP4."),
		runner.translationReviewButton,
	), GetCurrentThemeIsDark())
}

// OpenSourceReview and OpenTranslationReview keep an editable review surface
// available even on a compact desktop window.  The staged workflow still owns
// all state transitions: this dialog merely exposes the existing Save/Approve
// actions after their respective stage has stopped for user review.
func (runner *stagedWorkflowRunner) OpenSourceReview() {
	runner.openReviewDialog(
		"Kiểm tra & duyệt SRT nguồn",
		"Kiểm tra mốc thời gian và nội dung. Bạn có thể sửa trực tiếp, lưu bản sửa, rồi chỉ duyệt khi đã hài lòng.",
		runner.sourceEditor,
		runner.sourceSaveButton,
		runner.sourceApproveButton,
	)
}

func (runner *stagedWorkflowRunner) OpenTranslationReview() {
	runner.openReviewDialog(
		"Kiểm tra & duyệt bản dịch",
		"Kiểm tra tiếng Việt, tên riêng và thời lượng đọc. Bạn có thể sửa trực tiếp, lưu bản sửa, rồi chỉ duyệt khi đã hài lòng.",
		runner.translatedEditor,
		runner.translationSaveButton,
		runner.translationApproveButton,
	)
}

func (runner *stagedWorkflowRunner) openReviewDialog(title, hint string, editor *widget.Entry, saveButton, approveButton *widget.Button) {
	if editor == nil || editor.Disabled() {
		return
	}
	hintLabel := widget.NewLabel(hint)
	hintLabel.Wrapping = fyne.TextWrapWord
	actions := container.NewHBox(layout.NewSpacer(), saveButton, approveButton)
	content := container.NewBorder(hintLabel, actions, nil, nil, editor)
	reviewDialog := dialog.NewCustom(title, "Đóng", content, runner.window)
	reviewDialog.Resize(fyne.NewSize(980, 700))
	reviewDialog.Show()
}

func (runner *stagedWorkflowRunner) bindReviewSections(sourceInput, translationSettings fyne.CanvasObject) {
	runner.sourceInputSection = sourceInput
	runner.translationSettingsSection = translationSettings
	runner.refreshControls()
}

func (runner *stagedWorkflowRunner) StatusBar() fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, nil, container.NewPadded(runner.status))
}

func (runner *stagedWorkflowRunner) setBusy(busy bool, message string) {
	runner.mu.Lock()
	runner.busy = busy
	runner.mu.Unlock()
	if strings.TrimSpace(message) != "" {
		runner.status.SetText(message)
	}
	runner.refreshControls()
}

func (runner *stagedWorkflowRunner) current() (WorkflowSnapshot, string, bool, bool) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.snapshot, runner.taskID, runner.busy, runner.voiceSkipped
}

// RestoreLatestWorkflow deliberately restores state only. It never resumes a
// running worker or skips a review gate: after a desktop restart the user must
// explicitly choose the next available action again.
func (runner *stagedWorkflowRunner) RestoreLatestWorkflow() {
	_, _, busy, _ := runner.current()
	if busy || !runner.begin("Đang khôi phục job gần nhất…") {
		return
	}
	taskID, err := latestWorkflowTaskID("tasks")
	if err != nil {
		runner.fail("Không thể khôi phục job", err)
		return
	}
	go func() {
		snapshot, err := runner.sm.GetWorkflowSnapshot(taskID)
		if err != nil {
			runner.fail("Không thể khôi phục job", err)
			return
		}
		runner.mu.Lock()
		runner.voiceSkipped = false
		runner.mu.Unlock()
		runner.applySnapshot(snapshot)
		runner.setBusy(false, fmt.Sprintf("Đã khôi phục job %s. %s", taskID, strings.TrimSpace(snapshot.Message)))
	}()
}

// NewJob clears only the desktop controller state.  It never deletes an
// existing task or its artifacts: a user may return to a prior output through
// Restore latest job, while immediately regaining the explicit Start button
// for a different URL (or for a deliberate fresh run of the same URL).
func (runner *stagedWorkflowRunner) NewJob() {
	_, _, busy, _ := runner.current()
	if busy {
		return
	}
	runner.mu.Lock()
	runner.taskID = ""
	runner.snapshot = WorkflowSnapshot{}
	runner.voiceSkipped = false
	runner.sourceLoadedURL = ""
	runner.translatedLoadedURL = ""
	runner.mu.Unlock()
	runner.sourceEditor.SetText("")
	runner.translatedEditor.SetText("")
	runner.sourceStatus.SetText("Chưa tạo SRT nguồn. Dán/chọn nguồn rồi bấm Bắt đầu bước 01.")
	runner.translationStatus.SetText("Chờ bạn duyệt SRT nguồn trước khi dịch.")
	runner.voiceStatus.SetText("Tùy chọn: chỉ chạy khi bạn yêu cầu lồng tiếng.")
	runner.renderStatus.SetText("Chờ SRT bản dịch đã được duyệt.")
	runner.status.SetText("Đã sẵn sàng cho job mới. Các job cũ và output của chúng vẫn được giữ nguyên.")
	runner.updateProgress(0)
	runner.refreshControls()
}

func latestWorkflowTaskID(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("chưa có job đã lưu để khôi phục")
		}
		return "", fmt.Errorf("không thể đọc danh sách job: %w", err)
	}

	var latestID string
	var latestModified time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statePath := filepath.Join(root, entry.Name(), "workflow_state.json")
		info, err := os.Stat(statePath)
		if err != nil || info.IsDir() {
			continue
		}
		if latestID == "" || info.ModTime().After(latestModified) {
			latestID = entry.Name()
			latestModified = info.ModTime()
		}
	}
	if latestID == "" {
		return "", fmt.Errorf("chưa có workflow_state.json để khôi phục")
	}
	return latestID, nil
}

func (runner *stagedWorkflowRunner) applySnapshot(snapshot WorkflowSnapshot) {
	runner.mu.Lock()
	if strings.TrimSpace(snapshot.TaskID) != "" {
		runner.taskID = snapshot.TaskID
	}
	if snapshot.TaskID == "" {
		snapshot.TaskID = runner.taskID
	}
	runner.snapshot = snapshot
	runner.mu.Unlock()
	if strings.TrimSpace(snapshot.SourceURL) != "" {
		runner.sm.SetVideoUrl(snapshot.SourceURL)
	}

	stageMessage := strings.TrimSpace(snapshot.Message)
	if stageMessage == "" {
		stageMessage = workflowStageLabel(snapshot.CurrentStage)
	}
	if snapshot.FailureReason != "" {
		stageMessage += ": " + snapshot.FailureReason
	}
	runner.status.SetText(stageMessage)
	runner.updateProgress(snapshot.ProcessPercent)

	switch snapshot.CurrentStage {
	case "awaiting_source_review":
		runner.sourceStatus.SetText("SRT nguồn đã sẵn sàng để kiểm tra. Hãy sửa/lưu nếu cần, rồi bấm “Lưu & duyệt SRT nguồn”.")
		runner.loadEditor("source", snapshot.SourceSRTURL)
	case "source_approved":
		runner.sourceStatus.SetText("SRT nguồn đã được bạn duyệt. Bước 02 đã mở, nhưng chưa tự chạy.")
	case "translation_running":
		runner.translationStatus.SetText("Đang dịch SRT. Khi hoàn tất, Kova sẽ dừng để bạn kiểm tra bản dịch.")
	case "awaiting_translation_review":
		runner.translationStatus.SetText("Bản dịch đã sẵn sàng để kiểm tra. Hãy sửa/lưu nếu cần, rồi bấm “Lưu & duyệt bản dịch”.")
		runner.loadEditor("translated", snapshot.TranslatedSRTURL)
	case "translation_approved":
		runner.translationStatus.SetText("Bản dịch đã được bạn duyệt. Bạn có thể chọn tạo lồng tiếng hoặc xuất MP4 phụ đề.")
	case "dubbing_audio_running":
		runner.voiceStatus.SetText("Đang tạo audio lồng tiếng trên worker đã chọn. Kova sẽ dừng để bạn nghe và duyệt audio.")
	case "awaiting_dubbing_audio_review":
		runner.voiceStatus.SetText("Audio lồng tiếng đã sẵn sàng. Mở tab 05 để lưu/nghe audio, sau đó bấm Duyệt audio. Video chưa được ghép.")
	case "dubbing_audio_approved":
		runner.voiceStatus.SetText("Audio đã được bạn duyệt. Bước 03b đã mở, nhưng Kova chưa tự ghép video.")
	case "dubbing_video_running":
		runner.voiceStatus.SetText("Đang ghép audio đã duyệt vào video nguồn. Kova sẽ dừng để bạn kiểm tra video lồng tiếng.")
	case "awaiting_dubbing_video_review":
		runner.voiceStatus.SetText("Video lồng tiếng đã sẵn sàng. Mở tab 05 để lưu/kiểm tra video, sau đó bấm Duyệt video lồng tiếng.")
	case "dubbing_video_approved":
		runner.voiceStatus.SetText("Video lồng tiếng đã được bạn duyệt. Bước xuất MP4 đã mở, nhưng chưa tự chạy.")
	case "render_running":
		runner.renderStatus.SetText("Đang xuất MP4 cuối. Bạn vẫn có thể xem tiến độ ở bước 05.")
	case "completed":
		runner.renderStatus.SetText("MP4 cuối đã hoàn tất. Dùng các nút Lưu output ở bước 05.")
	case "failed":
		runner.status.SetText("Job dừng vì lỗi: " + strings.TrimSpace(snapshot.FailureReason))
	}
	if len(snapshot.Artifacts) > 0 {
		runner.sm.displayWorkflowArtifacts(snapshot)
	}
	runner.refreshControls()
}

func (runner *stagedWorkflowRunner) updateProgress(percent int) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	if runner.sm.progressBar != nil {
		runner.sm.progressBar.SetValue(float64(percent) / 100)
		runner.sm.progressBar.Show()
	}
	if runner.sm.progressLabel != nil {
		runner.sm.progressLabel.SetText(fmt.Sprintf("%d%%", percent))
		runner.sm.progressLabel.Show()
	}
	if runner.sm.progressPanel != nil {
		runner.sm.progressPanel.Show()
	}
}

func (runner *stagedWorkflowRunner) loadEditor(kind, location string) {
	location = strings.TrimSpace(location)
	if location == "" {
		return
	}
	runner.mu.Lock()
	alreadyLoaded := false
	switch kind {
	case "source":
		alreadyLoaded = runner.sourceLoadedURL == location
		if !alreadyLoaded {
			runner.sourceLoadedURL = location
		}
	case "translated":
		alreadyLoaded = runner.translatedLoadedURL == location
		if !alreadyLoaded {
			runner.translatedLoadedURL = location
		}
	}
	runner.mu.Unlock()
	if alreadyLoaded {
		return
	}
	go func() {
		content, err := runner.sm.ReadWorkflowText(location)
		if err != nil {
			runner.fail("Không thể tải SRT để kiểm tra", err)
			return
		}
		if kind == "source" {
			runner.sourceEditor.SetText(content)
		} else {
			runner.translatedEditor.SetText(content)
		}
	}()
}

func (runner *stagedWorkflowRunner) refreshControls() {
	snapshot, taskID, busy, voiceSkipped := runner.current()
	stage := snapshot.CurrentStage
	sourceReview := stage == "awaiting_source_review"
	translationReview := stage == "awaiting_translation_review"
	audioReview := stage == "awaiting_dubbing_audio_review"
	videoReview := stage == "awaiting_dubbing_video_review"
	sourceApproved := workflowStageReached(stage, "source")
	translationApproved := workflowStageReached(stage, "translation")
	audioApproved := workflowStageReached(stage, "dubbing_audio")
	videoApproved := workflowStageReached(stage, "dubbing_video")

	setEnabled(runner.sourceStartButton, !busy && (taskID == "" || stage == "failed"))
	setEnabled(runner.restoreLatestButton, !busy)
	setEnabled(runner.newJobButton, !busy)
	setEnabled(runner.sourceReviewButton, !busy && sourceReview)
	setEnabled(runner.sourceSaveButton, !busy && sourceReview)
	setEnabled(runner.sourceApproveButton, !busy && sourceReview)
	// A failed translation/dubbing/render retains the prior approved input on
	// the server. In that case CanStart explicitly re-opens only the failed
	// stage, even though CurrentStage is "failed" rather than an approved
	// milestone.
	translationReady := sourceApproved || runner.canStart(snapshot, "translation", false)
	setEnabled(runner.translationStartButton, !busy && translationReady && runner.canStart(snapshot, "translation", translationReady))
	setEnabled(runner.translationReviewButton, !busy && translationReview)
	setEnabled(runner.translationSaveButton, !busy && translationReview)
	setEnabled(runner.translationApproveButton, !busy && translationReview)

	translationAvailable := translationApproved || runner.canStart(snapshot, "dubbing_audio", false) || runner.canStart(snapshot, "render", false)
	voiceAllowed := translationAvailable && runner.sm.voiceoverEnabled && !voiceSkipped
	setEnabled(runner.dubbingStartButton, !busy && voiceAllowed && runner.canStart(snapshot, "dubbing_audio", voiceAllowed))
	// The service owns whether an optional dub can be skipped. In particular it
	// refuses a skip while a dubbing worker is actively writing files. Older
	// servers did not return dubbing_skip, so retain the conservative previous
	// rule as a fallback when that key is absent.
	skipFallback := translationAvailable && !videoApproved && !audioReview && !videoReview && stage != "dubbing_audio_running" && stage != "dubbing_video_running"
	setEnabled(runner.dubbingSkipButton, !busy && runner.canStart(snapshot, "dubbing_skip", skipFallback))
	setEnabled(runner.dubbingApproveButton, !busy && runner.canStart(snapshot, "dubbing_audio_approve", audioReview))
	videoStartFallback := audioApproved && !videoApproved
	setEnabled(runner.dubbingVideoStartButton, !busy && runner.canStart(snapshot, "dubbing_video", videoStartFallback))
	setEnabled(runner.dubbingVideoApproveButton, !busy && runner.canStart(snapshot, "dubbing_video_approve", videoReview))

	renderFallback := (translationApproved && (!runner.sm.voiceoverEnabled || voiceSkipped || videoApproved)) || runner.canStart(snapshot, "render", false)
	setEnabled(runner.renderStartButton, !busy && renderFallback && runner.canStart(snapshot, "render", true))

	if !busy && sourceReview {
		runner.sourceEditor.Enable()
	} else {
		runner.sourceEditor.Disable()
	}
	if !busy && translationReview {
		runner.translatedEditor.Enable()
	} else {
		runner.translatedEditor.Disable()
	}
	setVisible(runner.sourceInputSection, !sourceReview)
	setVisible(runner.translationSettingsSection, !translationReview)
}

func setVisible(object fyne.CanvasObject, visible bool) {
	if object == nil {
		return
	}
	if visible {
		object.Show()
		return
	}
	object.Hide()
}

func setEnabled(button *widget.Button, enabled bool) {
	if button == nil {
		return
	}
	if enabled {
		button.Enable()
	} else {
		button.Disable()
	}
}

func (runner *stagedWorkflowRunner) canStart(snapshot WorkflowSnapshot, stage string, fallback bool) bool {
	if len(snapshot.CanStart) == 0 {
		return fallback
	}
	allowed, exists := snapshot.CanStart[stage]
	if !exists {
		return fallback
	}
	return allowed
}

func workflowStageReached(current, checkpoint string) bool {
	stages := map[string][]string{
		"source":        {"source_approved", "translation_running", "awaiting_translation_review", "translation_approved", "dubbing_audio_running", "awaiting_dubbing_audio_review", "dubbing_audio_approved", "dubbing_video_running", "awaiting_dubbing_video_review", "dubbing_video_approved", "render_running", "completed"},
		"translation":   {"translation_approved", "dubbing_audio_running", "awaiting_dubbing_audio_review", "dubbing_audio_approved", "dubbing_video_running", "awaiting_dubbing_video_review", "dubbing_video_approved", "render_running", "completed"},
		"dubbing_audio": {"dubbing_audio_approved", "dubbing_video_running", "awaiting_dubbing_video_review", "dubbing_video_approved", "render_running", "completed"},
		"dubbing_video": {"dubbing_video_approved", "render_running", "completed"},
		// "dubbing" is retained for old UI tests/integrations and now means the
		// fully approved video half, never merely generated audio.
		"dubbing": {"dubbing_video_approved", "render_running", "completed"},
	}
	for _, stage := range stages[checkpoint] {
		if current == stage {
			return true
		}
	}
	return false
}

func workflowStageLabel(stage string) string {
	switch stage {
	case "source_running":
		return "Đang tạo SRT nguồn"
	case "awaiting_source_review":
		return "Chờ bạn kiểm tra SRT nguồn"
	case "source_approved":
		return "SRT nguồn đã được duyệt; chờ bạn bắt đầu dịch"
	case "translation_running":
		return "Đang dịch SRT"
	case "awaiting_translation_review":
		return "Chờ bạn kiểm tra bản dịch"
	case "translation_approved":
		return "Bản dịch đã được duyệt; chờ bạn chọn lồng tiếng/xuất MP4"
	case "dubbing_audio_running":
		return "Đang tạo audio lồng tiếng"
	case "awaiting_dubbing_audio_review":
		return "Chờ bạn kiểm tra audio lồng tiếng"
	case "dubbing_audio_approved":
		return "Audio đã duyệt; chờ bạn bắt đầu ghép video"
	case "dubbing_video_running":
		return "Đang ghép audio vào video"
	case "awaiting_dubbing_video_review":
		return "Chờ bạn kiểm tra video lồng tiếng"
	case "dubbing_video_approved":
		return "Video lồng tiếng đã duyệt; chờ bạn xuất MP4"
	case "render_running":
		return "Đang xuất MP4 cuối"
	case "completed":
		return "Hoàn tất"
	case "failed":
		return "Job thất bại"
	default:
		return "Đang chờ thao tác của bạn"
	}
}

func (runner *stagedWorkflowRunner) StartSource() {
	if strings.TrimSpace(runner.sm.GetVideoUrl()) == "" {
		dialog.ShowError(fmt.Errorf("hãy chọn video hoặc dán URL trước khi bắt đầu bước 01"), runner.window)
		runner.sourceStatus.SetText("Chưa có nguồn video. Dán URL/chọn file rồi bấm Bắt đầu bước 01.")
		return
	}
	if !runner.begin("Đang kiểm tra cấu hình và chuẩn bị bước 01…") {
		return
	}
	runner.sm.PrepareRun()
	runner.mu.Lock()
	runner.taskID = ""
	runner.snapshot = WorkflowSnapshot{}
	runner.voiceSkipped = false
	runner.sourceLoadedURL = ""
	runner.translatedLoadedURL = ""
	runner.mu.Unlock()
	runner.sourceEditor.SetText("")
	runner.translatedEditor.SetText("")
	go func() {
		if err := runner.prepareSourceStage(); err != nil {
			runner.fail("Không thể chuẩn bị bước 01", err)
			return
		}
		snapshot, err := runner.sm.StartSourceWorkflow()
		if err != nil {
			runner.fail("Không thể bắt đầu bước 01", err)
			return
		}
		runner.applySnapshot(snapshot)
		runner.watchUntil("awaiting_source_review")
	}()
}

func (runner *stagedWorkflowRunner) SaveSource() {
	runner.saveSRT("source", "Đã lưu SRT nguồn. Bạn vẫn có thể sửa tiếp hoặc bấm Duyệt.")
}

func (runner *stagedWorkflowRunner) ApproveSource() {
	runner.saveAndApprove("source", "SRT nguồn đã được duyệt. Bước 02 đã mở; Kova sẽ không tự dịch.")
}

func (runner *stagedWorkflowRunner) StartTranslation() {
	if err := validateTranslationStageConfig(); err != nil {
		runner.fail("Không thể bắt đầu dịch", err)
		return
	}
	runner.startStage("translation", "Đang bắt đầu bước 02: dịch SRT…", "awaiting_translation_review")
}

func (runner *stagedWorkflowRunner) SaveTranslation() {
	runner.saveSRT("translated", "Đã lưu bản dịch. Bạn vẫn có thể sửa tiếp hoặc bấm Duyệt.")
}

func (runner *stagedWorkflowRunner) ApproveTranslation() {
	runner.saveAndApprove("translation", "Bản dịch đã được duyệt. Chọn lồng tiếng hoặc xuất MP4 ở các bước tiếp theo.")
}

func (runner *stagedWorkflowRunner) StartDubbing() {
	if !runner.sm.voiceoverEnabled {
		dialog.ShowInformation("Lồng tiếng chưa bật", "Bật lồng tiếng và chọn engine/profile ở bước 03 trước khi bắt đầu tạo audio.", runner.window)
		return
	}
	if err := runner.validateRequestedVoice(); err != nil {
		runner.fail("Không thể bắt đầu lồng tiếng", err)
		return
	}
	runner.startStage("dubbing_audio", "Đang bắt đầu bước 03a: tạo audio lồng tiếng…", "awaiting_dubbing_audio_review")
}

func (runner *stagedWorkflowRunner) SkipDubbing() {
	snapshot, taskID, busy, _ := runner.current()
	translationAvailable := workflowStageReached(snapshot.CurrentStage, "translation") || runner.canStart(snapshot, "dubbing_audio", false) || runner.canStart(snapshot, "render", false)
	skipFallback := translationAvailable && !workflowStageReached(snapshot.CurrentStage, "dubbing_video") && snapshot.CurrentStage != "awaiting_dubbing_audio_review" && snapshot.CurrentStage != "awaiting_dubbing_video_review" && snapshot.CurrentStage != "dubbing_audio_running" && snapshot.CurrentStage != "dubbing_video_running"
	if busy || taskID == "" || !runner.canStart(snapshot, "dubbing_skip", skipFallback) || !runner.begin("Đang ghi nhận lựa chọn bỏ qua lồng tiếng…") {
		return
	}
	go func() {
		updated, err := runner.sm.SkipWorkflowDubbing(taskID)
		if err != nil {
			runner.fail("Không thể bỏ qua lồng tiếng", err)
			return
		}
		runner.mu.Lock()
		runner.voiceSkipped = true
		runner.mu.Unlock()
		runner.applySnapshot(updated)
		runner.voiceStatus.SetText("Bạn đã chọn không tạo lồng tiếng cho job này. Bước xuất MP4 phụ đề đã mở.")
		runner.setBusy(false, "Lồng tiếng được bỏ qua theo lựa chọn của bạn; chưa có render nào tự chạy.")
	}()
}

func (runner *stagedWorkflowRunner) ApproveDubbing() {
	runner.approveOnly("dubbing_audio", "Audio đã được duyệt. Bước 03b đã mở; Kova chưa tự ghép video.")
}

func (runner *stagedWorkflowRunner) StartDubbingVideo() {
	runner.startStage("dubbing_video", "Đang bắt đầu bước 03b: ghép audio vào video…", "awaiting_dubbing_video_review")
}

func (runner *stagedWorkflowRunner) ApproveDubbingVideo() {
	runner.approveOnly("dubbing_video", "Video lồng tiếng đã được duyệt. Bước xuất MP4 đã mở; chưa tự chạy.")
}

func (runner *stagedWorkflowRunner) StartRender() {
	runner.startStage("render", "Đang bắt đầu bước 04: xuất MP4 cuối…", "completed")
}

func (runner *stagedWorkflowRunner) saveSRT(kind, successMessage string) {
	_, taskID, busy, _ := runner.current()
	if busy || taskID == "" {
		return
	}
	content := runner.sourceEditor.Text
	if kind == "translated" {
		content = runner.translatedEditor.Text
	}
	if !runner.begin("Đang lưu SRT để bạn tiếp tục kiểm tra…") {
		return
	}
	go func() {
		snapshot, err := runner.sm.SaveWorkflowSRT(taskID, kind, content)
		if err != nil {
			runner.fail("Không thể lưu SRT", err)
			return
		}
		runner.applySnapshot(snapshot)
		runner.setBusy(false, successMessage)
	}()
}

func (runner *stagedWorkflowRunner) saveAndApprove(stage, successMessage string) {
	_, taskID, busy, _ := runner.current()
	if busy || taskID == "" {
		return
	}
	kind := "source"
	content := runner.sourceEditor.Text
	if stage == "translation" {
		kind = "translated"
		content = runner.translatedEditor.Text
	}
	if !runner.begin("Đang lưu bản bạn đã kiểm tra và chờ duyệt…") {
		return
	}
	go func() {
		if _, err := runner.sm.SaveWorkflowSRT(taskID, kind, content); err != nil {
			runner.fail("Không thể lưu SRT trước khi duyệt", err)
			return
		}
		snapshot, err := runner.sm.ApproveWorkflowStage(taskID, stage)
		if err != nil {
			runner.fail("Không thể duyệt bước", err)
			return
		}
		runner.applySnapshot(snapshot)
		runner.setBusy(false, successMessage)
	}()
}

func (runner *stagedWorkflowRunner) approveOnly(stage, successMessage string) {
	_, taskID, busy, _ := runner.current()
	if busy || taskID == "" || !runner.begin("Đang ghi nhận duyệt output…") {
		return
	}
	go func() {
		snapshot, err := runner.sm.ApproveWorkflowStage(taskID, stage)
		if err != nil {
			runner.fail("Không thể duyệt output", err)
			return
		}
		runner.applySnapshot(snapshot)
		runner.setBusy(false, successMessage)
	}()
}

func (runner *stagedWorkflowRunner) startStage(stage, message, terminalStage string) {
	_, taskID, busy, _ := runner.current()
	if busy || taskID == "" || !runner.begin(message) {
		return
	}
	go func() {
		snapshot, err := runner.sm.StartWorkflowStage(taskID, stage)
		if err != nil {
			runner.fail("Không thể bắt đầu bước", err)
			return
		}
		runner.applySnapshot(snapshot)
		runner.watchUntil(terminalStage)
	}()
}

func (runner *stagedWorkflowRunner) begin(message string) bool {
	runner.mu.Lock()
	if runner.busy {
		runner.mu.Unlock()
		return false
	}
	runner.busy = true
	runner.mu.Unlock()
	runner.status.SetText(message)
	runner.refreshControls()
	return true
}

func (runner *stagedWorkflowRunner) watchUntil(terminalStage string) {
	for {
		time.Sleep(1200 * time.Millisecond)
		_, taskID, _, _ := runner.current()
		if taskID == "" {
			runner.fail("Không thể theo dõi job", fmt.Errorf("job không có mã theo dõi"))
			return
		}
		snapshot, err := runner.sm.GetWorkflowSnapshot(taskID)
		if err != nil {
			runner.fail("Không thể lấy trạng thái workflow", err)
			return
		}
		runner.applySnapshot(snapshot)
		if snapshot.CurrentStage == "failed" {
			runner.fail("Job thất bại", fmt.Errorf("%s", strings.TrimSpace(snapshot.FailureReason)))
			return
		}
		if snapshot.CurrentStage == terminalStage {
			runner.setBusy(false, workflowStageLabel(terminalStage)+". Kova đang chờ quyết định của bạn.")
			return
		}
	}
}

func (runner *stagedWorkflowRunner) fail(prefix string, err error) {
	message := prefix
	if err != nil {
		message += ": " + err.Error()
	}
	runner.setBusy(false, message)
	if err != nil {
		dialog.ShowError(err, runner.window)
	}
}

func (runner *stagedWorkflowRunner) prepareSourceStage() error {
	// Source extraction from a public YouTube VTT does not need a TTS key,
	// remote OmniVoice URL, or a local ASR model.  Validating the entire legacy
	// configuration here used to block the very first review step because an
	// optional later-stage provider was not configured yet.
	parsedProxy, err := url.Parse(config.Conf.App.Proxy)
	if err != nil {
		return fmt.Errorf("proxy chưa hợp lệ: %w", err)
	}
	config.Conf.App.ParsedProxy = parsedProxy
	dependencyCheck := deps.CheckDependency
	if util.IsYouTubeURL(strings.TrimSpace(runner.sm.videoUrl)) {
		dependencyCheck = deps.CheckPlatformSubtitleDependency
	}
	if err := dependencyCheck(); err != nil {
		return fmt.Errorf("không chuẩn bị được dependency: %w", err)
	}
	if config.ConfigBackup != config.Conf {
		if err := server.StopBackend(); err != nil {
			return fmt.Errorf("không thể dừng dịch vụ Kova: %w", err)
		}
		go func() {
			if err := server.StartBackend(); err != nil {
				log.GetLogger().Error("Failed to restart Kova service", zap.Error(err))
			}
		}()
		if err := waitForKovaBackend(config.Conf.Server.Host, config.Conf.Server.Port, 8*time.Second); err != nil {
			return err
		}
		config.ConfigBackup = config.Conf
	}
	return nil
}

func validateTranslationStageConfig() error {
	provider := strings.ToLower(strings.TrimSpace(config.Conf.Llm.Provider))
	if provider == "" {
		return fmt.Errorf("chọn provider AI ở tab Cấu hình model và API trước khi dịch")
	}
	switch provider {
	case "openai-compatible":
		if strings.TrimSpace(config.Conf.Llm.BaseUrl) == "" {
			return fmt.Errorf("thiếu KOVA API Gateway / base URL trước khi dịch")
		}
		if !config.IsKOVAGatewayURL(config.Conf.Llm.BaseUrl) {
			return fmt.Errorf("KOVA hiện chỉ dùng API Gateway đã cấu hình")
		}
		if !config.IsGatewayFreeLLMModel(config.Conf.Llm.Model) {
			return fmt.Errorf("chọn một model free trong danh sách KOVA trước khi dịch")
		}
		if config.ResolveLLMAPIKey() == "" {
			return fmt.Errorf("nhập API key phiên hoặc đặt biến môi trường %s trước khi dịch", config.Conf.Llm.ApiKeyEnv)
		}
	default:
		return fmt.Errorf("provider AI không được hỗ trợ: %s", config.Conf.Llm.Provider)
	}
	return nil
}

func isLoopbackLLMHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

func (runner *stagedWorkflowRunner) validateRequestedVoice() error {
	if !runner.sm.voiceoverEnabled {
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(config.Conf.Tts.Provider))
	if provider == "gateway" {
		if strings.TrimSpace(config.Conf.Tts.Gateway.Endpoint) == "" || config.ResolveGatewayTTSAPIKey() == "" || strings.TrimSpace(config.Conf.Tts.Gateway.Model) == "" {
			return fmt.Errorf("chọn Google/Edge Gateway cần endpoint, API key và model TTS ở tab Cài đặt Kova")
		}
		endpoint, err := url.ParseRequestURI(config.Conf.Tts.Gateway.Endpoint)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
			return fmt.Errorf("endpoint Google/Edge Gateway phải là URL http/https hợp lệ")
		}
		return nil
	}
	if !strings.EqualFold(provider, "omnivoice") {
		return nil
	}
	if strings.TrimSpace(runner.sm.voiceoverAudioPath) == "" {
		return fmt.Errorf("chọn audio mẫu ở bước 03 trước khi lồng tiếng bằng OmniVoice")
	}
	if !runner.sm.voiceCloneConsent {
		return fmt.Errorf("xác nhận quyền sử dụng audio mẫu ở bước 03 trước khi clone giọng")
	}
	if err := config.ValidateRemoteOmniVoiceWorker(); err != nil {
		return err
	}
	if config.Conf.Tts.Omnivoice.RequireCUDA {
		if _, err := omnivoice.ProbeColabGPUWithAPIKey(config.Conf.Tts.Omnivoice.BaseUrl, config.Conf.Tts.Omnivoice.SessionApiKey, 12*time.Second); err != nil {
			return fmt.Errorf("worker OmniVoice Colab chưa sẵn sàng: %w", err)
		}
	}
	return nil
}

// CreateWorkflow creates five persistent Kova workflow pages. Controls are
// built once and share one manager, so switching sidebar tabs never loses the
// selected source, translation, voice, or export configuration.
func CreateWorkflow(window fyne.Window) *WorkflowTabs {
	sm := NewSubtitleManager(window)
	// Keep the legacy controller allocated for backwards-compatible embedding,
	// but do not expose any of its one-click controls in the Kova desktop.
	runner := newWorkflowRunner(window, sm)
	stage := newStagedWorkflowRunner(window, sm)

	// Enter in the URL field only completes text entry.  It must not start a
	// network job; step 01 has its own explicit button below.
	videoInputContainer := createVideoInputContainer(sm, nil)
	subtitleSettingsCard := createSubtitleSettingsCard(sm)
	voiceSettingsCard := createVoiceSettingsCard(sm)
	embedSettingsCard := createEmbedSettingsCard(sm)
	stage.bindReviewSections(videoInputContainer, subtitleSettingsCard)

	progressPanel, downloadPanel, tipsPanel := createProgressAndDownloadArea(sm)
	sm.SetStageOutputCallback("source", func(message string) { stage.sourceStatus.SetText("Output nguồn: " + message) })
	sm.SetStageOutputCallback("translation", func(message string) { stage.translationStatus.SetText("Output dịch: " + message) })
	sm.SetStageOutputCallback("voice", func(message string) { stage.voiceStatus.SetText("Output giọng: " + message) })
	sm.SetStageOutputCallback("export", func(message string) { stage.renderStatus.SetText("Output MP4: " + message) })
	sm.reportTranslationConfiguration()
	sm.reportStageOutput("voice", "Chọn bật lồng tiếng nếu cần tạo audio dub cố định.")
	sm.reportStageOutput("export", "MP4 có phụ đề ngang được bật mặc định; bạn có thể đổi tỷ lệ hoặc tắt ở bước 04.")

	outputHint := widget.NewLabel("Output được tạo theo từng bước: SRT nguồn → bản dịch đã duyệt → audio/video lồng tiếng (nếu chọn) → MP4 cuối. Mỗi bước dừng để bạn kiểm tra trước khi mở bước tiếp theo.")
	outputHint.Wrapping = fyne.TextWrapWord

	return &WorkflowTabs{Pages: []fyne.CanvasObject{
		createWorkflowPage(
			"01 · Nguồn video / Video source",
			"Chọn file từ máy hoặc dán URL công khai. Bước này chỉ tạo SRT/script nguồn để bạn kiểm tra; không tự dịch hoặc render.",
			videoInputContainer,
			container.NewHBox(layout.NewSpacer(), stage.newJobButton, stage.sourceStartButton, stage.restoreLatestButton, layout.NewSpacer()),
			stage.sourceStatus,
			stage.SourceReviewPanel(),
		),
		createWorkflowPage(
			"02 · Dịch và phụ đề / Translation",
			"Chọn ngôn ngữ nguồn/đích và tên riêng cần giữ nguyên. Nút dịch chỉ mở sau khi bạn đã duyệt SRT nguồn.",
			subtitleSettingsCard,
			container.NewHBox(layout.NewSpacer(), stage.translationStartButton, layout.NewSpacer()),
			stage.translationStatus,
			stage.TranslationReviewPanel(),
		),
		createWorkflowPage(
			"03 · Giọng lồng tiếng cố định / Fixed voice",
			"Tùy chọn. Một engine và một profile/giọng clone được giữ cố định trong toàn bộ video. Audio và video được tạo, kiểm tra và duyệt ở hai bước riêng.",
			voiceSettingsCard,
			ModernCard("03a · Tạo và kiểm tra audio", container.NewVBox(
				widget.NewLabel("Bấm tạo audio, mở tab 05 để lưu/nghe output, rồi mới duyệt audio. Kova không ghép video ở bước này."),
				container.NewHBox(stage.dubbingStartButton, stage.dubbingSkipButton),
				stage.dubbingApproveButton,
			), GetCurrentThemeIsDark()),
			ModernCard("03b · Ghép và kiểm tra video", container.NewVBox(
				widget.NewLabel("Chỉ mở sau khi bạn duyệt audio. Bấm ghép video, kiểm tra video ở tab 05, rồi duyệt video lồng tiếng."),
				stage.dubbingVideoStartButton,
				stage.dubbingVideoApproveButton,
			), GetCurrentThemeIsDark()),
			stage.voiceStatus,
		),
		createWorkflowPage(
			"04 · Xuất hình và tinh chỉnh / Video output",
			"Chọn khung hình và cách ghép phụ đề. Kova chỉ tạo MP4 cuối khi bạn bấm nút Bắt đầu bước 04.",
			embedSettingsCard,
			container.NewHBox(layout.NewSpacer(), stage.renderStartButton, layout.NewSpacer()),
			stage.renderStatus,
		),
		createWorkflowPage(
			"05 · Chạy và nhận output / Run",
			"Theo dõi trạng thái của bước bạn đã tự bắt đầu và tải từng artifact đã tạo. Tab này không có nút chạy toàn bộ pipeline.",
			stage.StatusBar(),
			progressPanel,
			downloadPanel,
			tipsPanel,
			outputHint,
		),
	}, runner: runner, stage: stage}
}

// CreateWorkflowTabs is retained for integrations and tests that only need
// page objects. The desktop uses CreateWorkflow so it can mount the persistent
// action bar too.
func CreateWorkflowTabs(window fyne.Window) []fyne.CanvasObject {
	return CreateWorkflow(window).Pages
}

// PersistentActionBar is deliberately informational.  The prior global Start
// button could launch every pipeline phase at once, so each workflow page now
// owns its own explicit Start action instead.
func (workflow *WorkflowTabs) PersistentActionBar() fyne.CanvasObject {
	if workflow.stage == nil {
		return widget.NewLabel("Mỗi bước cần được bắt đầu và duyệt riêng.")
	}
	hint := widget.NewLabel("Quy trình kiểm tra theo bước · Mỗi bước có nút Bắt đầu và điểm duyệt riêng")
	hint.Wrapping = fyne.TextWrapWord
	return container.NewCenter(hint)
}

// CreateSubtitleTab is kept as a compatibility entry point for integrations
// that previously embedded the single-page workbench.
func CreateSubtitleTab(window fyne.Window) fyne.CanvasObject {
	return CreateWorkflow(window).Pages[0]
}

func createWorkflowPage(title, description string, objects ...fyne.CanvasObject) fyne.CanvasObject {
	var background *canvas.LinearGradient
	if GetCurrentThemeIsDark() {
		background = canvas.NewLinearGradient(
			color.NRGBA{R: 15, G: 23, B: 42, A: 255},
			color.NRGBA{R: 30, G: 41, B: 59, A: 255},
			0.0,
		)
	} else {
		background = canvas.NewLinearGradient(
			color.NRGBA{R: 248, G: 250, B: 252, A: 255},
			color.NRGBA{R: 241, G: 245, B: 249, A: 255},
			0.0,
		)
	}

	titleLabel := TitleText(title)
	descriptionLabel := widget.NewLabel(description)
	descriptionLabel.Wrapping = fyne.TextWrapWord
	body := container.NewVBox(container.NewPadded(titleLabel), container.NewPadded(descriptionLabel))
	for _, object := range objects {
		body.Add(container.NewPadded(object))
	}
	mainContent := container.NewPadded(body)
	// Workflow pages should fit the desktop pane horizontally. A bidirectional
	// scroll preserved an over-wide form's minimum width, pushing the page title
	// and voice controls off-screen. Only vertical scrolling is useful here.
	scroll := container.NewVScroll(mainContent)
	contentStack := container.NewStack(background, scroll)
	return container.NewPadded(contentStack)
}

// createAppConfigGroup configures Kova's pipeline limits.
func createAppConfigGroup() *fyne.Container {
	appSegmentDurationEntry := StyledEntry("Thời lượng mỗi đoạn / Segment minutes")
	appSegmentDurationEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.App.SegmentDuration)))
	appSegmentDurationEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 30 {
			return fmt.Errorf("Giá trị phải từ 1 đến 30 / Value must be 1-30")
		}
		return nil
	}

	appTranscribeParallelNumEntry := StyledEntry("Số luồng nhận dạng / Transcribe workers")
	appTranscribeParallelNumEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.App.TranscribeParallelNum)))
	appTranscribeParallelNumEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 10 {
			return fmt.Errorf("Giá trị phải từ 1 đến 10 / Value must be 1-10")
		}
		return nil
	}

	appTranslateParallelNumEntry := StyledEntry("Số luồng dịch / Translation workers")
	appTranslateParallelNumEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.App.TranslateParallelNum)))
	appTranslateParallelNumEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 20 {
			return fmt.Errorf("Giá trị phải từ 1 đến 20 / Value must be 1-20")
		}
		return nil
	}

	appTranscribeMaxAttemptsEntry := StyledEntry("Số lần thử nhận dạng / Transcribe retries")
	appTranscribeMaxAttemptsEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.App.TranscribeMaxAttempts)))
	appTranscribeMaxAttemptsEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 10 {
			return fmt.Errorf("Giá trị phải từ 1 đến 10 / Value must be 1-10")
		}
		return nil
	}

	appTranslateMaxAttemptsEntry := StyledEntry("Số lần thử dịch / Translation retries")
	appTranslateMaxAttemptsEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.App.TranslateMaxAttempts)))
	appTranslateMaxAttemptsEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 20 {
			return fmt.Errorf("Giá trị phải từ 1 đến 20 / Value must be 1-20")
		}
		return nil
	}

	appMaxSentenceLengthEntry := StyledEntry("Ký tự tối đa mỗi câu / Max sentence length")
	appMaxSentenceLengthEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.App.MaxSentenceLength)))
	appMaxSentenceLengthEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 200 {
			return fmt.Errorf("Giá trị phải từ 1 đến 200 / Value must be 1-200")
		}
		return nil
	}

	appProxyEntry := StyledEntry("Địa chỉ proxy / Network proxy")
	appProxyEntry.Bind(binding.BindString(&config.Conf.App.Proxy))

	form := widget.NewForm(
		widget.NewFormItem("Thời lượng đoạn / Segment duration", appSegmentDurationEntry),
		widget.NewFormItem("Luồng nhận dạng / Transcribe workers", appTranscribeParallelNumEntry),
		widget.NewFormItem("Luồng dịch / Translation workers", appTranslateParallelNumEntry),
		widget.NewFormItem("Thử lại nhận dạng / Transcribe retries", appTranscribeMaxAttemptsEntry),
		widget.NewFormItem("Thử lại dịch / Translation retries", appTranslateMaxAttemptsEntry),
		widget.NewFormItem("Độ dài câu / Sentence length", appMaxSentenceLengthEntry),
		widget.NewFormItem("Proxy mạng / Network proxy", appProxyEntry),
	)

	return GlassmorphismCard("Cấu hình ứng dụng / App config", "Thông số pipeline cơ bản của Kova", form, GetCurrentThemeIsDark())
}

// createServerConfigGroup configures Kova's loopback API service.
func createServerConfigGroup() *fyne.Container {
	serverHostEntry := StyledEntry("Địa chỉ dịch vụ / Server address")
	serverHostEntry.Bind(binding.BindString(&config.Conf.Server.Host))

	serverPortEntry := StyledEntry("Cổng dịch vụ / Server port")
	serverPortEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.Server.Port)))
	serverPortEntry.Validator = func(s string) error {
		val, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("Vui lòng nhập số / Please enter a number")
		}
		if val < 1 || val > 65535 {
			return fmt.Errorf("Cổng phải từ 1 đến 65535 / Port must be 1-65535")
		}
		return nil
	}

	form := widget.NewForm(
		widget.NewFormItem("Địa chỉ / Address", serverHostEntry),
		widget.NewFormItem("Cổng / Port", serverPortEntry),
	)

	return GlassmorphismCard("Dịch vụ Kova / Kova service", "API nội bộ dùng để chạy pipeline desktop", form, GetCurrentThemeIsDark())
}

// createLlmConfigGroup configures KOVA's fixed free-model translation gateway.
func createLlmConfigGroup() *fyne.Container {
	providerSelect := widget.NewSelect([]string{"openai-compatible"}, nil)
	llmProviderSelectRef = providerSelect
	config.Conf.Llm.Provider = "openai-compatible"
	config.Conf.Llm.BaseUrl = config.KOVAGatewayBaseURL
	if !config.IsGatewayFreeLLMModel(config.Conf.Llm.Model) {
		config.Conf.Llm.Model = "oc/deepseek-v4-flash-free"
	}

	baseUrlEntry := StyledEntry("API Base URL")
	baseUrlEntry.Bind(binding.BindString(&config.Conf.Llm.BaseUrl))
	llmBaseUrlEntryRef = baseUrlEntry

	apiKeyEntry := StyledPasswordEntry("API key phiên / Session API key")
	apiKeyEnvEntry := StyledEntry("Biến môi trường API Gateway")
	apiKeyEnvEntry.Bind(binding.BindString(&config.Conf.Llm.ApiKeyEnv))
	settingKeyEntry := false
	setKeyEntryForProvider := func() {
		settingKeyEntry = true
		apiKeyEntry.SetText(config.Conf.Llm.SessionApiKey)
		settingKeyEntry = false
	}
	apiKeyEntry.OnChanged = func(value string) {
		if settingKeyEntry {
			return
		}
		config.Conf.Llm.SessionApiKey = value
	}
	providerSelect.OnChanged = func(value string) {
		config.Conf.Llm.Provider = value
		setKeyEntryForProvider()
	}
	providerSelect.SetSelected(config.Conf.Llm.Provider)
	providerSelect.Disable()
	setKeyEntryForProvider()

	modelEntry := StyledEntry("Tên model / Model name")
	modelEntry.Bind(binding.BindString(&config.Conf.Llm.Model))
	llmModelEntryRef = modelEntry

	modelSelect := StyledSelect([]string{}, func(v string) {
		if v != "" && llmModelEntryRef != nil {
			llmModelEntryRef.SetText(v)
		}
	})
	modelSelect.PlaceHolder = "Chọn model gợi ý / Suggested model"
	llmModelSelectRef = modelSelect

	form := widget.NewForm(
		widget.NewFormItem("Nhà cung cấp / Provider", providerSelect),
		widget.NewFormItem("API Base URL", baseUrlEntry),
		widget.NewFormItem("API key phiên (không lưu vào file)", apiKeyEntry),
		widget.NewFormItem("Biến môi trường dự phòng / Key env", apiKeyEnvEntry),
		widget.NewFormItem("Tên model / Model name", modelEntry),
		widget.NewFormItem("Model gợi ý / Suggested models", modelSelect),
	)
	return GlassmorphismCard("Model dịch của Kova / Translation model", "KOVA API Gateway dùng endpoint OpenAI-compatible cố định và chỉ hiển thị model free đã xác minh.", form, GetCurrentThemeIsDark())
}

// createTranscribeConfigGroup configures speech-to-text.
func createTranscribeConfigGroup() *fyne.Container {
	providerOptions := []string{"openai", "fasterwhisper", "whisperkit", "whispercpp", "aliyun"}
	providerSelect := widget.NewSelect(providerOptions, func(value string) {
		config.Conf.Transcribe.Provider = value
	})
	providerSelect.SetSelected(config.Conf.Transcribe.Provider)

	openaiBaseUrlEntry := StyledEntry("API Base URL")
	openaiBaseUrlEntry.Bind(binding.BindString(&config.Conf.Transcribe.Openai.BaseUrl))
	openaiApiKeyEntry := StyledPasswordEntry("API Key")
	openaiApiKeyEntry.Bind(binding.BindString(&config.Conf.Transcribe.Openai.ApiKey))
	openaiModelEntry := StyledEntry("Tên model / Model name")
	openaiModelEntry.Bind(binding.BindString(&config.Conf.Transcribe.Openai.Model))

	fasterWhisperModelEntry := StyledEntry("Tên model / Model name")
	fasterWhisperModelEntry.Bind(binding.BindString(&config.Conf.Transcribe.Fasterwhisper.Model))

	whisperKitModelEntry := StyledEntry("Tên model / Model name")
	whisperKitModelEntry.Bind(binding.BindString(&config.Conf.Transcribe.Whisperkit.Model))

	whisperCppModelEntry := StyledEntry("Tên model / Model name")
	whisperCppModelEntry.Bind(binding.BindString(&config.Conf.Transcribe.Whispercpp.Model))

	aliyunOssKeyIdEntry := StyledEntry("Aliyun Access Key ID")
	aliyunOssKeyIdEntry.Bind(binding.BindString(&config.Conf.Transcribe.Aliyun.Oss.AccessKeyId))
	aliyunOssKeySecretEntry := StyledPasswordEntry("Aliyun Access Key Secret")
	aliyunOssKeySecretEntry.Bind(binding.BindString(&config.Conf.Transcribe.Aliyun.Oss.AccessKeySecret))
	aliyunOssBucketEntry := StyledEntry("Aliyun OSS Bucket name")
	aliyunOssBucketEntry.Bind(binding.BindString(&config.Conf.Transcribe.Aliyun.Oss.Bucket))

	aliyunSpeechKeyIdEntry := StyledEntry("Aliyun Speech Access Key ID")
	aliyunSpeechKeyIdEntry.Bind(binding.BindString(&config.Conf.Transcribe.Aliyun.Speech.AccessKeyId))
	aliyunSpeechKeySecretEntry := StyledPasswordEntry("Aliyun Speech Access Key Secret")
	aliyunSpeechKeySecretEntry.Bind(binding.BindString(&config.Conf.Transcribe.Aliyun.Speech.AccessKeySecret))
	aliyunSpeechAppKeyEntry := StyledEntry("Aliyun Speech App Key")
	aliyunSpeechAppKeyEntry.Bind(binding.BindString(&config.Conf.Transcribe.Aliyun.Speech.AppKey))

	form := widget.NewForm(
		widget.NewFormItem("Nhà cung cấp / Provider", providerSelect),
		widget.NewFormItem("Tăng tốc GPU / GPU acceleration", widget.NewCheckWithData("Bật / Enable", binding.BindBool(&config.Conf.Transcribe.EnableGpuAcceleration))),

		widget.NewFormItem("OpenAI Base URL", openaiBaseUrlEntry),
		widget.NewFormItem("OpenAI API Key", openaiApiKeyEntry),
		widget.NewFormItem("OpenAI model", openaiModelEntry),

		widget.NewFormItem("FasterWhisper model", fasterWhisperModelEntry),

		widget.NewFormItem("WhisperKit model", whisperKitModelEntry),

		widget.NewFormItem("WhisperCpp model", whisperCppModelEntry),

		widget.NewFormItem("Aliyun OSS Access Key ID", aliyunOssKeyIdEntry),
		widget.NewFormItem("Aliyun OSS Access Key Secret", aliyunOssKeySecretEntry),
		widget.NewFormItem("Aliyun OSS Bucket Name", aliyunOssBucketEntry),

		widget.NewFormItem("Aliyun Speech Access Key ID", aliyunSpeechKeyIdEntry),
		widget.NewFormItem("Aliyun Speech Access Key Secret", aliyunSpeechKeySecretEntry),
		widget.NewFormItem("Aliyun Speech App Key", aliyunSpeechAppKeyEntry),
	)

	return GlassmorphismCard("Nhận dạng giọng nói / Transcription", "Chọn API hoặc engine nhận dạng cục bộ", form, GetCurrentThemeIsDark())
}

// createCreatorToolingConfigGroup keeps all local Auto-Builder/OCR
// dependencies explicit. No executable is downloaded or started from this
// page; Kova only stores the paths that a user has reviewed and selected.
func createCreatorToolingConfigGroup(window fyne.Window) *fyne.Container {
	backend := widget.NewSelect([]string{"pycapcut", "capcut-cli"}, func(value string) {
		config.Conf.Creator.CompilerBackend = value
	})
	if strings.TrimSpace(config.Conf.Creator.CompilerBackend) == "" {
		config.Conf.Creator.CompilerBackend = "pycapcut"
	}
	backend.SetSelected(config.Conf.Creator.CompilerBackend)

	ffprobe := StyledEntry("ffprobe")
	ffprobe.SetText(config.Conf.Creator.FFprobePath)
	ffprobe.OnChanged = func(value string) { config.Conf.Creator.FFprobePath = value }
	node := StyledEntry("node")
	node.SetText(config.Conf.Creator.NodePath)
	node.OnChanged = func(value string) { config.Conf.Creator.NodePath = value }
	capcutCLI := StyledEntry("C:\\tools\\capcut-cli\\dist\\index.js")
	capcutCLI.SetText(config.Conf.Creator.CapCutCLIPath)
	capcutCLI.OnChanged = func(value string) { config.Conf.Creator.CapCutCLIPath = value }
	python := StyledEntry("python")
	python.SetText(config.Conf.Creator.PythonPath)
	python.OnChanged = func(value string) { config.Conf.Creator.PythonPath = value }
	bridge := StyledEntry("scripts/kova_pycapcut_builder.py")
	bridge.SetText(config.Conf.Creator.PyCapCutBridgePath)
	bridge.OnChanged = func(value string) { config.Conf.Creator.PyCapCutBridgePath = value }
	draftRoot := StyledEntry("Thư mục Draft của CapCut")
	draftRoot.SetText(config.Conf.Creator.CapCutDraftRoot)
	draftRoot.OnChanged = func(value string) { config.Conf.Creator.CapCutDraftRoot = value }
	output := StyledEntry("output")
	output.SetText(config.Conf.Creator.DefaultOutputDir)
	output.OnChanged = func(value string) { config.Conf.Creator.DefaultOutputDir = value }

	ocrPython := StyledEntry("python")
	ocrPython.SetText(config.Conf.VisualOCR.PythonPath)
	ocrPython.OnChanged = func(value string) { config.Conf.VisualOCR.PythonPath = value }
	ocrScript := StyledEntry("scripts/kova_visual_ocr.py")
	ocrScript.SetText(config.Conf.VisualOCR.ScriptPath)
	ocrScript.OnChanged = func(value string) { config.Conf.VisualOCR.ScriptPath = value }
	ocrInterval := StyledEntry("250")
	ocrInterval.SetText(strconv.Itoa(config.Conf.VisualOCR.SampleIntervalMS))
	ocrInterval.OnChanged = func(value string) {
		if interval, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && interval >= 40 && interval <= 5000 {
			config.Conf.VisualOCR.SampleIntervalMS = interval
		}
	}

	capcutPicker := GhostButton("Chọn capcut-cli", theme.FolderOpenIcon(), func() { chooseFile(window, []string{".js"}, capcutCLI) })
	bridgePicker := GhostButton("Chọn bridge Python", theme.DocumentIcon(), func() { chooseFile(window, []string{".py"}, bridge) })
	draftRootPicker := GhostButton("Chọn CapCut Draft Root", theme.FolderOpenIcon(), func() { chooseFolder(window, draftRoot) })
	outputPicker := GhostButton("Chọn output mặc định", theme.FolderOpenIcon(), func() { chooseFolder(window, output) })
	ocrScriptPicker := GhostButton("Chọn OCR bridge", theme.DocumentIcon(), func() { chooseFile(window, []string{".py"}, ocrScript) })

	form := widget.NewForm(
		widget.NewFormItem("Compiler / Compiler", backend),
		widget.NewFormItem("ffprobe", ffprobe),
		widget.NewFormItem("Node.js", node),
		widget.NewFormItem("capcut-cli (không dùng cho Blur Mask)", container.NewBorder(nil, nil, nil, capcutPicker, capcutCLI)),
		widget.NewFormItem("Python cho pycapcut", python),
		widget.NewFormItem("Kova pycapcut bridge", container.NewBorder(nil, nil, nil, bridgePicker, bridge)),
		widget.NewFormItem("CapCut Draft Root", container.NewBorder(nil, nil, nil, draftRootPicker, draftRoot)),
		widget.NewFormItem("Output mặc định", container.NewBorder(nil, nil, nil, outputPicker, output)),
		widget.NewFormItem("OCR Python", ocrPython),
		widget.NewFormItem("OCR bridge", container.NewBorder(nil, nil, nil, ocrScriptPicker, ocrScript)),
		widget.NewFormItem("OCR interval (ms)", ocrInterval),
		widget.NewFormItem("OCR ưu tiên CUDA", widget.NewCheckWithData("GPU trước, CPU fallback", binding.BindBool(&config.Conf.VisualOCR.PreferGPU))),
	)
	copy := "pycapcut là lựa chọn dùng cho Circle/Rectangle Blur Mask: cài pycapcut trong Python đã chọn và chỉ định đúng thư mục Draft của CapCut. capcut-cli là backend khác, chỉ hợp lệ nếu project không có mask. Kova luôn xuất kova-capcut-draft-spec.json để bạn duyệt trước."
	return GlassmorphismCard("Auto-Builder, CapCut và Visual OCR", copy, form, GetCurrentThemeIsDark())
}

type gatewayTTSModelChoice struct {
	label string
	model string
}

// kovaGatewayTTSModels is intentionally a small, ordered dropdown rather
// than a free-text model box.  The model identifiers are the values sent to a
// 9Router/OpenAI-compatible TTS gateway; availability still depends on the
// user's gateway account.
var kovaGatewayTTSModels = []gatewayTTSModelChoice{
	{label: "Google TTS tiếng Việt", model: "google-tts/vi"},
	{label: "Google TTS tiếng Anh", model: "google-tts/en"},
	{label: "Microsoft Edge TTS tiếng Việt · Hoài My", model: "edge-tts/vi-VN-HoaiMyNeural"},
	{label: "Microsoft Edge TTS tiếng Việt · Nam Minh", model: "edge-tts/vi-VN-NamMinhNeural"},
}

func gatewayTTSModelLabel(model string) string {
	model = strings.TrimSpace(model)
	for _, choice := range kovaGatewayTTSModels {
		if choice.model == model {
			return choice.label
		}
	}
	if model == "" {
		return kovaGatewayTTSModels[0].label
	}
	return "Model gateway đã lưu: " + model
}

// createTtsConfigGroup configures Kova's TTS engines and Colab worker.
func createTtsConfigGroup(window fyne.Window) *fyne.Container {
	providerOptions := []string{"openai", "aliyun", "edge-tts", "minimax", "omnivoice", "gateway"}
	providerSelect := widget.NewSelect(providerOptions, func(value string) {
		config.Conf.Tts.Provider = value
	})
	providerSelect.SetSelected(config.Conf.Tts.Provider)

	openaiBaseUrlEntry := StyledEntry("API Base URL")
	openaiBaseUrlEntry.Bind(binding.BindString(&config.Conf.Tts.Openai.BaseUrl))
	openaiApiKeyEntry := StyledPasswordEntry("API Key")
	openaiApiKeyEntry.Bind(binding.BindString(&config.Conf.Tts.Openai.ApiKey))
	openaiModelEntry := StyledEntry("Tên model / Model name")
	openaiModelEntry.Bind(binding.BindString(&config.Conf.Tts.Openai.Model))

	minimaxBaseUrlEntry := StyledEntry("API Base URL")
	minimaxBaseUrlEntry.Bind(binding.BindString(&config.Conf.Tts.Minimax.BaseUrl))
	minimaxApiKeyEntry := StyledPasswordEntry("API Key")
	minimaxApiKeyEntry.Bind(binding.BindString(&config.Conf.Tts.Minimax.ApiKey))
	minimaxModelEntry := StyledEntry("Tên model / Model name")
	minimaxModelEntry.Bind(binding.BindString(&config.Conf.Tts.Minimax.Model))

	gatewayEndpointEntry := StyledEntry("https://gateway.example/v1/audio/speech")
	gatewayEndpointEntry.Bind(binding.BindString(&config.Conf.Tts.Gateway.Endpoint))
	gatewayAPIKeyEntry := StyledPasswordEntry("Gateway Bearer API Key")
	gatewayAPIKeyEntry.Bind(binding.BindString(&config.Conf.Tts.Gateway.ApiKey))
	gatewayAPIKeyEnvEntry := StyledEntry("KOVA_API_GATEWAY_API_KEY")
	gatewayAPIKeyEnvEntry.Bind(binding.BindString(&config.Conf.Tts.Gateway.ApiKeyEnv))
	// These identifiers are passed through unchanged to the user's
	// OpenAI-compatible gateway.  Keep the choice explicit: selecting the
	// Vietnamese Google entry does not pretend that it is a cloned voice.
	gatewayModelOptions := make(map[string]string, len(kovaGatewayTTSModels)+1)
	gatewayModelLabels := make([]string, 0, len(kovaGatewayTTSModels)+1)
	for _, choice := range kovaGatewayTTSModels {
		gatewayModelOptions[choice.label] = choice.model
		gatewayModelLabels = append(gatewayModelLabels, choice.label)
	}
	currentGatewayModel := gatewayTTSModelLabel(config.Conf.Tts.Gateway.Model)
	if _, known := gatewayModelOptions[currentGatewayModel]; !known {
		// Do not overwrite an existing gateway-specific model simply because
		// Kova does not ship it in the preset dropdown yet.
		gatewayModelOptions[currentGatewayModel] = strings.TrimSpace(config.Conf.Tts.Gateway.Model)
		gatewayModelLabels = append(gatewayModelLabels, currentGatewayModel)
	}
	gatewayModelSelect := widget.NewSelect(gatewayModelLabels, func(value string) {
		config.Conf.Tts.Gateway.Model = gatewayModelOptions[value]
	})
	gatewayModelSelect.SetSelected(currentGatewayModel)
	gatewayFormatOptions := map[string]string{
		"Gateway mặc định (MP3 binary)": "",
		"WAV (nếu gateway hỗ trợ)":      "wav",
		"MP3":                           "mp3",
	}
	gatewayResponseFormat := widget.NewSelect([]string{"Gateway mặc định (MP3 binary)", "WAV (nếu gateway hỗ trợ)", "MP3"}, func(value string) {
		config.Conf.Tts.Gateway.ResponseFormat = gatewayFormatOptions[value]
	})
	currentGatewayFormat := "Gateway mặc định (MP3 binary)"
	for label, value := range gatewayFormatOptions {
		if value == strings.TrimSpace(config.Conf.Tts.Gateway.ResponseFormat) {
			currentGatewayFormat = label
			break
		}
	}
	gatewayResponseFormat.SetSelected(currentGatewayFormat)
	gatewayHint := widget.NewLabel("API Gateway TTS (9Router/OpenAI-compatible): endpoint theo ảnh là /v1/audio/speech. Chọn Google TTS tiếng Việt để yêu cầu model google-tts/vi; Kova gửi nguyên model + input tới gateway và nhận MP3 binary. Chọn WAV khi gateway hỗ trợ để ghép timeline ổn định. Đây là giọng preset của gateway; muốn clone giọng từ audio thì chọn OmniVoice.")
	gatewayHint.Wrapping = fyne.TextWrapWord

	omniVoiceBaseUrlEntry := StyledEntry("https://…trycloudflare.com")
	omniVoiceBaseUrlEntry.Bind(binding.BindString(&config.Conf.Tts.Omnivoice.BaseUrl))
	omniVoiceSessionTokenEntry := StyledPasswordEntry("Session token from Colab Run all")
	omniVoiceSessionTokenEntry.Bind(binding.BindString(&config.Conf.Tts.Omnivoice.SessionApiKey))
	colabConnectionStatus := widget.NewLabel("Chưa kết nối Colab GPU. Bấm Mở notebook Kova trên Colab; Chrome sẽ mở sẵn notebook. Sau đó chọn GPU, Run all, rồi dán URL HTTPS và Session token mà cell cuối in ra.")
	colabConnectionStatus.Wrapping = fyne.TextWrapWord
	openColabNotebookButton := PrimaryButton("Mở notebook Kova trên Colab", theme.FolderOpenIcon(), func() {
		if err := openKovaColabNotebook(); err != nil {
			colabConnectionStatus.SetText("Không thể mở notebook Colab: " + err.Error())
			dialog.ShowError(err, window)
			return
		}
		colabConnectionStatus.SetText("Đã mở notebook Kova trong Chrome/Google Colab. Chọn GPU và Run all; URL worker và Session token xuất hiện ở cell cuối.")
	})
	probeColabWorkerButton := SecondaryButton("Kiểm tra URL Colab", theme.FolderOpenIcon(), func() {
		workerURL := strings.TrimSpace(omniVoiceBaseUrlEntry.Text)
		if workerURL == "" {
			colabConnectionStatus.SetText("Hãy dán URL HTTPS mà cell cuối của Colab in ra.")
			return
		}
		workerToken := strings.TrimSpace(omniVoiceSessionTokenEntry.Text)
		if workerToken == "" {
			colabConnectionStatus.SetText("Hãy dán Session token tạm thời mà cell cuối của Colab in ra. Token này không được lưu vào config.")
			return
		}
		colabConnectionStatus.SetText("Đang kiểm tra worker Colab…")
		go func() {
			health, err := omnivoice.ProbeColabGPUWithAPIKey(workerURL, workerToken, 12*time.Second)
			if err != nil {
				colabConnectionStatus.SetText("Không kết nối được: " + err.Error())
				return
			}
			device := health.Device
			if device == "" {
				device = "không rõ thiết bị"
			}
			colabConnectionStatus.SetText("Đã kết nối worker Colab GPU. Thiết bị: " + device + "; audio mẫu chỉ được upload một lần sau khi bạn xác nhận ở tab 03.")
		}()
	})
	colabWorkflow := container.NewVBox(
		widget.NewLabel("Google Colab (GPU từ xa, bắt buộc): 1) Bấm Mở notebook Kova trên Colab; Chrome mở đúng file đã publish. 2) Runtime → Change runtime type → GPU. 3) Run all. 4) Copy URL tunnel HTTPS và Session token in ở cell cuối. 5) Dán cả hai và kiểm tra. Token chỉ tồn tại trong phiên; Kova từ chối worker CPU/local."),
		container.NewHBox(openColabNotebookButton, probeColabWorkerButton),
		colabConnectionStatus,
	)
	omniVoiceLanguageEntry := StyledEntry("OmniVoice Language")
	omniVoiceLanguageEntry.Bind(binding.BindString(&config.Conf.Tts.Omnivoice.Language))
	perJobReferenceNote := widget.NewLabel("Chọn audio mẫu theo từng job ở tab 03. Kova không lưu hoặc tái dùng audio tham chiếu toàn cục, và không clone giọng trên desktop.")
	perJobReferenceNote.Wrapping = fyne.TextWrapWord
	omniVoiceReferenceTextEntry := StyledEntry("OmniVoice Reference Text")
	omniVoiceReferenceTextEntry.Bind(binding.BindString(&config.Conf.Tts.Omnivoice.ReferenceText))
	omniVoiceInstructEntry := StyledEntry("OmniVoice Voice Style")
	omniVoiceInstructEntry.Bind(binding.BindString(&config.Conf.Tts.Omnivoice.Instruct))
	omniVoiceSpeedEntry := StyledEntry("OmniVoice Speed")
	omniVoiceSpeedEntry.Bind(binding.FloatToString(binding.BindFloat(&config.Conf.Tts.Omnivoice.Speed)))
	omniVoiceNumStepEntry := StyledEntry("OmniVoice Steps")
	omniVoiceNumStepEntry.Bind(binding.IntToString(binding.BindInt(&config.Conf.Tts.Omnivoice.NumStep)))

	aliyunOssKeyIdEntry := StyledEntry("Aliyun Access Key ID")
	aliyunOssKeyIdEntry.Bind(binding.BindString(&config.Conf.Tts.Aliyun.Oss.AccessKeyId))
	aliyunOssKeySecretEntry := StyledPasswordEntry("Aliyun Access Key Secret")
	aliyunOssKeySecretEntry.Bind(binding.BindString(&config.Conf.Tts.Aliyun.Oss.AccessKeySecret))
	aliyunOssBucketEntry := StyledEntry("Aliyun OSS Bucket name")
	aliyunOssBucketEntry.Bind(binding.BindString(&config.Conf.Tts.Aliyun.Oss.Bucket))

	aliyunSpeechKeyIdEntry := StyledEntry("Aliyun Speech Access Key ID")
	aliyunSpeechKeyIdEntry.Bind(binding.BindString(&config.Conf.Tts.Aliyun.Speech.AccessKeyId))
	aliyunSpeechKeySecretEntry := StyledPasswordEntry("Aliyun Speech Access Key Secret")
	aliyunSpeechKeySecretEntry.Bind(binding.BindString(&config.Conf.Tts.Aliyun.Speech.AccessKeySecret))
	aliyunSpeechAppKeyEntry := StyledEntry("Aliyun Speech App Key")
	aliyunSpeechAppKeyEntry.Bind(binding.BindString(&config.Conf.Tts.Aliyun.Speech.AppKey))

	form := widget.NewForm(
		widget.NewFormItem("Nhà cung cấp / Provider", providerSelect),

		widget.NewFormItem("OpenAI Base URL", openaiBaseUrlEntry),
		widget.NewFormItem("OpenAI API Key", openaiApiKeyEntry),
		widget.NewFormItem("OpenAI model", openaiModelEntry),

		widget.NewFormItem("MiniMax Base URL", minimaxBaseUrlEntry),
		widget.NewFormItem("MiniMax API Key", minimaxApiKeyEntry),
		widget.NewFormItem("MiniMax model", minimaxModelEntry),

		widget.NewFormItem("API Gateway endpoint", gatewayEndpointEntry),
		widget.NewFormItem("API Gateway key", gatewayAPIKeyEntry),
		widget.NewFormItem("Gateway key env (optional)", gatewayAPIKeyEnvEntry),
		widget.NewFormItem("API Gateway model", gatewayModelSelect),
		widget.NewFormItem("API Gateway output", gatewayResponseFormat),
		widget.NewFormItem("API Gateway TTS", gatewayHint),

		widget.NewFormItem("OmniVoice Worker URL", omniVoiceBaseUrlEntry),
		widget.NewFormItem("OmniVoice Session token (không lưu)", omniVoiceSessionTokenEntry),
		widget.NewFormItem("Luồng Google Colab", colabWorkflow),
		widget.NewFormItem("OmniVoice Language", omniVoiceLanguageEntry),
		widget.NewFormItem("Audio tham chiếu OmniVoice", perJobReferenceNote),
		widget.NewFormItem("OmniVoice Reference Text", omniVoiceReferenceTextEntry),
		widget.NewFormItem("OmniVoice Voice Style", omniVoiceInstructEntry),
		widget.NewFormItem("OmniVoice Speed", omniVoiceSpeedEntry),
		widget.NewFormItem("OmniVoice Steps", omniVoiceNumStepEntry),

		widget.NewFormItem("Aliyun OSS Access Key ID", aliyunOssKeyIdEntry),
		widget.NewFormItem("Aliyun OSS Access Key Secret", aliyunOssKeySecretEntry),
		widget.NewFormItem("Aliyun OSS Bucket", aliyunOssBucketEntry),

		widget.NewFormItem("Aliyun Speech Access Key ID", aliyunSpeechKeyIdEntry),
		widget.NewFormItem("Aliyun Speech Access Key Secret", aliyunSpeechKeySecretEntry),
		widget.NewFormItem("Aliyun Speech App Key", aliyunSpeechAppKeyEntry),
	)

	return GlassmorphismCard("Lồng tiếng / Text to speech", "KOVA Voice Studio (OmniVoice), Google/Edge Gateway và các API tương thích", form, GetCurrentThemeIsDark())
}

func createVideoInputContainer(sm *SubtitleManager, onStart func()) *fyne.Container {
	inputTypeRadio := widget.NewRadioGroup([]string{"File từ máy này", "URL công khai"}, nil)
	inputTypeRadio.Horizontal = true
	inputTypeContainer := container.NewHBox(
		inputTypeRadio,
	)

	urlEntry := StyledEntry("Dán URL YouTube/youtu.be, Douyin, TikTok, Vimeo hoặc HTTP(S) trực tiếp")
	urlEntry.Hide()
	urlEntry.OnChanged = func(text string) {
		sm.SetVideoUrl(text)
	}
	urlEntry.OnSubmitted = func(_ string) {
		// Enter only confirms text entry.  Starting work must always be a
		// separate, visible decision at the stage button below.
		if onStart != nil {
			onStart()
		}
	}

	selectButton := PrimaryButton("Chọn video từ máy", theme.FolderOpenIcon(), sm.ShowFileDialog)

	selectedVideoLabel := widget.NewLabel("")
	selectedVideoLabel.Hide()

	sm.SetVideoSelectedCallback(func(path string) {
		if path != "" {
			sm.SetVideoUrl(path)
			selectedVideoLabel.SetText("Đã chọn / Chosen: " + filepath.Base(path))
			selectedVideoLabel.Show()
		} else {
			selectedVideoLabel.Hide()
		}
	})

	sm.SetVideosSelectedCallback(func(paths []string) {
		if len(paths) > 0 {
			sm.SetVideoUrl(paths[0])

			fileNames := make([]string, 0, len(paths))
			for _, path := range paths {
				fileNames = append(fileNames, filepath.Base(path))
			}

			displayText := fmt.Sprintf("Đã chọn %d file:\n", len(paths))
			for i, name := range fileNames {
				displayText += fmt.Sprintf("%d. %s\n", i+1, name)
			}

			selectedVideoLabel.SetText(displayText)
			selectedVideoLabel.Show()
		} else {
			selectedVideoLabel.Hide()
		}
	})

	videoInputContainer := container.NewVBox()
	videoInputContainer.Objects = []fyne.CanvasObject{selectButton, selectedVideoLabel}

	inputTypeRadio.SetSelected("File từ máy này")
	inputTypeRadio.OnChanged = func(value string) {
		if value == "File từ máy này" {
			urlEntry.Hide()
			selectButton.Show()
			selectedVideoLabel.Show()
			videoInputContainer.Objects = []fyne.CanvasObject{selectButton, selectedVideoLabel}
			sm.SetVideoUrl("")
		} else {
			selectButton.Hide()
			selectedVideoLabel.Hide()
			urlEntry.Show()
			videoInputContainer.Objects = []fyne.CanvasObject{urlEntry}
		}
		videoInputContainer.Refresh()
	}

	content := container.NewVBox(
		container.NewPadded(inputTypeContainer),
		container.NewPadded(videoInputContainer),
		widget.NewLabel("URL: dán link rồi bấm Bắt đầu bước 01 bên dưới. Nhấn Enter không tự chạy job. Kova hỗ trợ cả youtube.com và youtu.be."),
	)

	return GlassmorphismCard("Chọn nguồn video", "File mở bằng hộp thoại native; URL chỉ nhận link công khai có bridge/downloader hỗ trợ.", content, GetCurrentThemeIsDark())
}

func createSubtitleSettingsCard(sm *SubtitleManager) *fyne.Container {
	positionSelect := widget.NewSelect([]string{
		"Bản dịch ở trên", "Bản dịch ở dưới",
	}, func(value string) {
		if value == "Bản dịch ở trên" {
			sm.SetBilingualPosition(1)
		} else {
			sm.SetBilingualPosition(2)
		}
	})
	positionSelect.SetSelected("Bản dịch ở trên")

	bilingualCheck := widget.NewCheck("Hiện song ngữ (tắt để output phụ đề chỉ có tiếng đích)", func(checked bool) {
		sm.SetBilingualEnabled(checked)
		if checked {
			positionSelect.Enable()
		} else {
			positionSelect.Disable()
		}
	})
	bilingualCheck.SetChecked(false)

	// Each item shows Vietnamese, the language's own spelling, and English.
	// The displayed string is distinct from the compact code sent to the API.
	type languageChoice struct{ label, code string }
	languages := []languageChoice{
		{"Tiếng Việt — Tiếng Việt (Vietnamese) · vi", "vi"},
		{"Tiếng Anh — English (English) · en", "en"},
		{"Tiếng Trung giản thể (Chinese, Simplified) · zh_cn", "zh_cn"},
		{"Tiếng Trung phồn thể (Chinese, Traditional) · zh_tw", "zh_tw"},
		{"Tiếng Nhật (Japanese) · ja", "ja"},
		{"Tiếng Hàn — 한국어 (Korean) · ko", "ko"},
		{"Tiếng Thái — ภาษาไทย (Thai) · th", "th"},
		{"Tiếng Indonesia — Bahasa Indonesia (Indonesian) · id", "id"},
		{"Tiếng Pháp — Français (French) · fr", "fr"},
		{"Tiếng Đức — Deutsch (German) · de", "de"},
		{"Tiếng Tây Ban Nha — Español (Spanish) · es", "es"},
		{"Tiếng Bồ Đào Nha — Português (Portuguese) · pt", "pt"},
		{"Tiếng Nga — Русский язык (Russian) · ru", "ru"},
		{"Tiếng Ả Rập — اَلْعَرَبِيَّةُ (Arabic) · ar", "ar"},
	}
	labels := make([]string, 0, len(languages))
	codeByLabel := make(map[string]string, len(languages))
	labelByCode := make(map[string]string, len(languages))
	for _, language := range languages {
		labels = append(labels, language.label)
		codeByLabel[language.label] = language.code
		labelByCode[language.code] = language.label
	}
	sourceLangSelector := StyledSelect(labels, func(value string) { sm.SetSourceLang(codeByLabel[value]) })
	targetLangSelector := StyledSelect(labels, func(value string) { sm.SetTargetLang(codeByLabel[value]) })

	langContainer := container.NewVBox(
		container.NewHBox(
			widget.NewLabel("Ngôn ngữ nguồn:"), sourceLangSelector,
		),
		container.NewHBox(
			widget.NewLabel("Dịch sang:"), targetLangSelector,
		),
	)

	// Default to English input and Vietnamese output, matching the desktop app's
	// primary use case while leaving every supported language explicit.
	sourceLangSelector.SetSelected(labelByCode["en"])
	targetLangSelector.SetSelected(labelByCode["vi"])

	fillerCheck := widget.NewCheck("Lọc từ đệm", func(checked bool) {
		sm.SetFillerFilter(checked)
	})
	fillerCheck.SetChecked(true)

	properNamesEntry := widget.NewMultiLineEntry()
	properNamesEntry.SetPlaceHolder("Tên riêng không dịch (không bắt buộc) — mỗi dòng một tên, ví dụ:\nAlice Nguyen\nKova")
	properNamesEntry.OnChanged = func(value string) {
		terms := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' })
		clean := make([]string, 0, len(terms))
		for _, term := range terms {
			if term = strings.TrimSpace(term); term != "" {
				clean = append(clean, term)
			}
		}
		sm.SetProtectedTerms(clean)
	}

	content := container.NewVBox(
		container.NewHBox(bilingualCheck, fillerCheck),
		langContainer,
		positionSelect,
		widget.NewLabel("Tên riêng giữ nguyên khi dịch (không bắt buộc):"),
		properNamesEntry,
	)

	return ModernCard("Dịch và phụ đề", content, GetCurrentThemeIsDark())
}

func createVoiceSettingsCard(sm *SubtitleManager) *fyne.Container {
	type engineChoice struct {
		label    string
		provider string
		model    string
	}
	engines := []engineChoice{
		{"KOVA Voice Studio (OmniVoice clone giọng)", "omnivoice", ""},
		{"Google TTS tiếng Việt qua API Gateway", "gateway", "google-tts/vi"},
		{"Google TTS tiếng Anh qua API Gateway", "gateway", "google-tts/en"},
		{"Microsoft Edge TTS qua API Gateway", "gateway", "edge-tts"},
		{"OpenAI TTS", "openai", ""},
		{"MiniMax TTS", "minimax", ""},
		{"Aliyun TTS", "aliyun", ""},
	}
	engineByLabel := make(map[string]engineChoice, len(engines))
	engineLabels := make([]string, 0, len(engines))
	for _, engine := range engines {
		engineByLabel[engine.label] = engine
		engineLabels = append(engineLabels, engine.label)
	}

	voiceProfileSelect := widget.NewSelect([]string{"Tự động / profile mặc định"}, func(value string) {
		if value == "Clone từ audio tham chiếu" || value == "Giọng preset của model" || value == "Tự động / profile mặc định" {
			sm.SetTtsVoiceCode("auto")
			return
		}
		sm.SetTtsVoiceCode(value)
	})
	voiceProfileSelect.SetSelected("Tự động / profile mặc định")

	// The picker is intentionally native: users select an authorized reference
	// file rather than typing an unstable absolute path into the UI. It is not
	// an inference action: the file is sent only to the remote Colab worker for
	// this job after the user explicitly confirms consent below.
	audioSampleButton := SecondaryButton("Chọn audio clone từ máy", theme.MediaMusicIcon(), sm.ShowAudioFileDialog)
	audioSampleButton.Disable()
	voiceoverEnabled := false
	currentEngine := engines[0]
	referenceSelected := false
	selectedAudioLabel := widget.NewLabel("Chưa chọn audio tham chiếu cho job này")
	selectedAudioLabel.Wrapping = fyne.TextWrapWord
	consentCheck := widget.NewCheck("Tôi xác nhận có quyền sử dụng audio mẫu này để clone giọng", func(checked bool) {
		sm.SetVoiceCloneConsent(checked)
	})
	consentCheck.Disable()

	var refreshEngineControls func()
	refreshEngineControls = func() {
		options := []string{"Tự động / profile mặc định"}
		switch currentEngine.provider {
		case "omnivoice":
			options = []string{"Clone từ audio tham chiếu"}
		case "gateway":
			options = []string{"Giọng preset của model"}
		case "openai":
			options = []string{"alloy", "ash", "ballad", "coral", "echo", "fable", "nova", "onyx", "sage", "shimmer", "verse"}
		}
		voiceProfileSelect.Options = options
		voiceProfileSelect.Refresh()
		voiceProfileSelect.SetSelected(options[0])
		if voiceoverEnabled {
			voiceProfileSelect.Enable()
		} else {
			voiceProfileSelect.Disable()
		}
		cloneMode := voiceoverEnabled && currentEngine.provider == "omnivoice"
		if cloneMode {
			audioSampleButton.Enable()
		} else {
			audioSampleButton.Disable()
		}
		if cloneMode && referenceSelected {
			consentCheck.Enable()
		} else {
			consentCheck.SetChecked(false)
			consentCheck.Disable()
		}
	}
	sm.SetAudioSelectedCallback(func(path string) {
		referenceSelected = strings.TrimSpace(path) != ""
		if referenceSelected {
			selectedAudioLabel.SetText("Audio mẫu của job: " + filepath.Base(path) + " (chỉ gửi tới Colab khi bấm Bắt đầu)")
		}
		refreshEngineControls()
	})

	ttsEngineSelect := widget.NewSelect(engineLabels, func(value string) {
		engine, ok := engineByLabel[value]
		if !ok {
			return
		}
		currentEngine = engine
		config.Conf.Tts.Provider = engine.provider
		if engine.provider == "gateway" {
			config.Conf.Tts.Gateway.Model = engine.model
		}
		refreshEngineControls()
	})
	selectedEngineLabel := engines[0].label
	for _, engine := range engines {
		if engine.provider == config.Conf.Tts.Provider && (engine.provider != "gateway" || engine.model == config.Conf.Tts.Gateway.Model) {
			selectedEngineLabel = engine.label
			currentEngine = engine
			break
		}
	}

	voiceoverCheck := widget.NewCheck("Bật lồng tiếng bằng một giọng cố định", func(checked bool) {
		voiceoverEnabled = checked
		sm.SetVoiceoverEnabled(checked)
		refreshEngineControls()
	})
	ttsEngineSelect.SetSelected(selectedEngineLabel)
	refreshEngineControls()

	grid := container.NewVBox(
		container.NewHBox(voiceoverCheck),
		container.NewGridWithColumns(2, widget.NewLabel("Công cụ TTS:"), ttsEngineSelect),
		container.NewGridWithColumns(2, widget.NewLabel("Giọng/profile:"), voiceProfileSelect),
		container.NewHBox(audioSampleButton),
		selectedAudioLabel,
		consentCheck,
	)

	return ModernCard("Giọng đọc", container.NewVBox(widget.NewLabel("Chọn engine và profile hoàn toàn bằng danh sách xổ xuống. Google TTS tiếng Việt gửi model google-tts/vi tới API Gateway và dùng giọng preset của gateway. OmniVoice clone chỉ chạy trên GPU Google Colab; Kova không tạo hoặc clone giọng trên máy desktop."), grid), GetCurrentThemeIsDark())
}

func createEmbedSettingsCard(sm *SubtitleManager) *fyne.Container {
	embedCheck := widget.NewCheck("Xuất video có phụ đề", nil)

	embedTypeSelect := StyledSelect([]string{
		"Ngang (16:9)", "Dọc (9:16)", "Cả ngang và dọc",
	}, nil)
	embedTypeSelect.Disable()

	mainTitleEntry := StyledEntry("Tiêu đề chính (tùy chọn)")
	subTitleEntry := StyledEntry("Tiêu đề phụ (tùy chọn)")
	mainTitleEntry.OnChanged = func(value string) {
		sm.SetVerticalTitles(value, subTitleEntry.Text)
	}
	subTitleEntry.OnChanged = func(value string) {
		sm.SetVerticalTitles(mainTitleEntry.Text, value)
	}

	titleInputContainer := container.NewVBox(
		container.NewGridWithColumns(2,
			widget.NewLabel("Tiêu đề chính:"),
			mainTitleEntry,
		),
		container.NewGridWithColumns(2,
			widget.NewLabel("Tiêu đề phụ:"),
			subTitleEntry,
		),
	)
	titleInputContainer.Hide()

	embedCheck.OnChanged = func(checked bool) {
		if checked {
			embedTypeSelect.Enable()
			embedTypeSelect.SetSelected("Ngang (16:9)")
		} else {
			embedTypeSelect.Disable()
			sm.SetEmbedSubtitle("none")
		}
	}

	embedTypeSelect.OnChanged = func(value string) {
		switch value {
		case "Ngang (16:9)":
			titleInputContainer.Hide()
			sm.SetEmbedSubtitle("horizontal")
		case "Dọc (9:16)":
			titleInputContainer.Show()
			sm.SetEmbedSubtitle("vertical")
		case "Cả ngang và dọc":
			titleInputContainer.Show()
			sm.SetEmbedSubtitle("all")
		}
	}
	// A normal Kova run should produce an MP4 rather than only loose subtitle
	// files. Users can still untick this before starting if they only need SRT.
	embedCheck.SetChecked(true)
	// SetSelected normally invokes its callback, but keep the model state
	// explicit as well so UI tests and older Fyne implementations preserve the
	// same default output contract.
	sm.SetEmbedSubtitle("horizontal")

	topContainer := container.NewHBox(embedCheck, embedTypeSelect)

	mainContainer := container.NewVBox(
		topContainer,
		container.NewPadded(titleInputContainer),
	)

	return ModernCard("Xuất hình", container.NewVBox(widget.NewLabel("Bật xuất video để nhận MP4 có phụ đề sau khi job hoàn tất."), mainContainer), GetCurrentThemeIsDark())
}

func createProgressAndDownloadArea(sm *SubtitleManager) (*fyne.Container, *fyne.Container, *fyne.Container) {
	progress := widget.NewProgressBar()

	percentLabel := widget.NewLabel("0%")
	percentLabel.Alignment = fyne.TextAlignTrailing

	progressContainer := container.NewBorder(nil, nil, nil, percentLabel, progress)

	progressBg := canvas.NewRectangle(color.NRGBA{R: 240, G: 245, B: 250, A: 230})
	progressBg.SetMinSize(fyne.NewSize(0, 40))
	progressBg.CornerRadius = 8

	progressShadow := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 20})
	progressShadow.Move(fyne.NewPos(2, 2))
	progressShadow.SetMinSize(fyne.NewSize(0, 40))
	progressShadow.CornerRadius = 8

	progressWithBg := container.NewStack(
		progressShadow,
		progressBg,
		container.NewPadded(progressContainer),
	)
	progressWithBg.Hide()

	sm.SetProgressBar(progress)
	sm.SetProgressLabel(percentLabel)
	sm.SetProgressPanel(progressWithBg)

	downloadBg := canvas.NewRectangle(color.NRGBA{R: 240, G: 250, B: 255, A: 230})
	downloadBg.CornerRadius = 10

	downloadContainer := container.NewVBox()
	downloadContainer.Hide()
	sm.SetDownloadContainer(downloadContainer)

	downloadWithBg := container.NewStack(
		downloadBg,
		container.NewPadded(downloadContainer),
	)
	downloadWithBg.Hide()
	sm.SetDownloadPanel(downloadWithBg)

	tipsLabel := widget.NewLabel("")
	tipsLabel.Alignment = fyne.TextAlignCenter
	tipsLabel.Wrapping = fyne.TextWrapWord
	sm.SetTipsLabel(tipsLabel)

	tipsBg := canvas.NewRectangle(color.NRGBA{R: 255, G: 250, B: 230, A: 200})
	tipsBg.CornerRadius = 6

	tipsWithBg := container.NewStack(
		tipsBg,
		container.NewPadded(tipsLabel),
	)
	tipsWithBg.Hide()
	sm.SetTipsPanel(tipsWithBg)

	return progressWithBg, downloadWithBg, tipsWithBg
}
