package desktop

import (
	"fmt"
	"image/color"
	"kova/config"
	"kova/log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.uber.org/zap"
)

func createNavButton(text string, icon fyne.Resource, isSelected bool, onTap func()) *widget.Button {
	btn := widget.NewButtonWithIcon(text, icon, onTap)
	if isSelected {
		btn.Importance = widget.HighImportance
	} else {
		btn.Importance = widget.LowImportance
	}
	btn.Alignment = widget.ButtonAlignLeading
	return btn
}

// createKovaDesktopShell keeps the workflow navigation in a fixed left
// sidebar. Every actual workflow action is rendered in the center pane; the
// bottom area is status-only. Keeping this layout in one small helper makes
// its width contract independently testable without opening a native window.
const kovaSidebarWidth float32 = 320

type kovaSidebarLayout struct{}

func (kovaSidebarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		if object == nil || !object.Visible() {
			continue
		}
		object.Move(fyne.NewPos(0, 0))
		object.Resize(fyne.NewSize(kovaSidebarWidth, size.Height))
	}
}

func (kovaSidebarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var minHeight float32
	for _, object := range objects {
		if object != nil && object.MinSize().Height > minHeight {
			minHeight = object.MinSize().Height
		}
	}
	return fyne.NewSize(kovaSidebarWidth, minHeight)
}

func createKovaDesktopShell(navigation *fyne.Container, content, bottom fyne.CanvasObject) *fyne.Container {
	var sidebar fyne.CanvasObject
	if navigation != nil {
		sidebar = container.New(kovaSidebarLayout{}, navigation)
	}
	return container.NewBorder(nil, bottom, sidebar, nil, container.NewPadded(content))
}

// Show launches the Kova desktop application.
func Show() {
	myApp := app.NewWithID("vn.kova.desktop")
	myWindow := myApp.NewWindow("Kova · Video Localization Studio")

	themeManager := NewThemeManager(myApp, myWindow)
	SetGlobalThemeManager(themeManager)

	logo := canvas.NewText("KOVA", color.NRGBA{R: 59, G: 130, B: 246, A: 255})
	logo.TextSize = 30
	logo.TextStyle = fyne.TextStyle{Bold: true}
	logo.Alignment = fyne.TextAlignCenter

	separator := canvas.NewRectangle(color.NRGBA{R: 209, G: 213, B: 219, A: 255})
	separator.SetMinSize(fyne.NewSize(0, 2))

	slogan := canvas.NewText("Dịch video · Phụ đề · Lồng tiếng", color.NRGBA{R: 107, G: 114, B: 128, A: 255})
	slogan.TextSize = 12
	slogan.Alignment = fyne.TextAlignCenter
	logoContainer := container.NewVBox(logo, separator, slogan)

	workflow := CreateWorkflow(myWindow)
	// The creator studio is Kova-native: it owns the Auto-Builder, offline
	// Visual OCR and subtitle-style workflow rather than exposing a legacy
	// compatibility screen or a browser-only companion tool.
	pages := append(workflow.Pages, CreateCreatorStudioPage(myWindow), CreateLlmTab(), CreateConfigTab(myWindow))

	navItemsVI := []string{
		"01 · Nguồn video",
		"02 · Dịch và phụ đề",
		"03 · Giọng lồng tiếng cố định",
		"04 · Xuất hình và tinh chỉnh",
		"05 · Chạy và nhận output",
		"06 · CapCut Auto-Builder & OCR",
		"Cấu hình model và API",
		"Cài đặt Kova",
	}
	navItemsEN := []string{
		"01 · Video source",
		"02 · Translation and subtitles",
		"03 · Fixed dubbing voice",
		"04 · Video output and tuning",
		"05 · Run and receive outputs",
		"06 · CapCut Auto-Builder & OCR",
		"Model and API configuration",
		"Kova settings",
	}
	navIcons := []fyne.Resource{
		theme.MediaVideoIcon(),
		theme.DocumentIcon(),
		theme.MediaMusicIcon(),
		theme.ContentAddIcon(),
		theme.MediaPlayIcon(),
		theme.FolderNewIcon(),
		theme.ComputerIcon(),
		theme.SettingsIcon(),
	}

	currentSelectedIndex := 0
	// Add pages to the stack before changing visibility. Hiding them before
	// they are attached lets Fyne re-show child canvases during stack creation,
	// which caused settings pages to overlap the workflow and collapse the
	// sidebar labels vertically.
	contentStack := container.NewStack(pages...)
	for i, page := range pages {
		if i == currentSelectedIndex {
			page.Show()
		} else {
			page.Hide()
		}
	}

	var navButtons []*widget.Button
	navContainer := container.NewVBox()
	for i, item := range navItemsVI {
		index := i
		navBtn := createNavButton(item, navIcons[i], i == currentSelectedIndex, func() {
			if currentSelectedIndex == index {
				return
			}
			if err := config.SaveConfig(); err != nil {
				dialog.ShowError(fmt.Errorf("Không thể lưu cấu hình Kova: %w", err), myWindow)
				return
			}
			for pageIndex, page := range pages {
				if pageIndex == index {
					page.Show()
					navButtons[pageIndex].Importance = widget.HighImportance
				} else {
					page.Hide()
					navButtons[pageIndex].Importance = widget.LowImportance
				}
				navButtons[pageIndex].Refresh()
			}
			currentSelectedIndex = index
			contentStack.Refresh()
		})
		navButtons = append(navButtons, navBtn)
		// Fyne may otherwise collapse a leading-aligned button down to its icon
		// width inside the split pane, which makes Vietnamese labels appear one
		// character per line. Keep every left-nav row visibly wide and tappable.
		navContainer.Add(container.NewGridWrap(fyne.NewSize(300, 42), navBtn))
	}

	languageLabel := widget.NewLabel("Giao diện / Interface")
	languageLabel.Alignment = fyne.TextAlignCenter
	languageSelect := widget.NewSelect([]string{"Tiếng Việt", "English"}, nil)
	languageSelect.SetSelected("Tiếng Việt")
	var statusText *canvas.Text
	languageSelect.OnChanged = func(language string) {
		items := navItemsVI
		if language == "English" {
			items = navItemsEN
			slogan.Text = "Translate · Subtitle · Dub video"
			if statusText != nil {
				statusText.Text = "Kova ready"
			}
		} else {
			slogan.Text = "Dịch video · Phụ đề · Lồng tiếng"
			if statusText != nil {
				statusText.Text = "Kova sẵn sàng · Ready"
			}
		}
		slogan.Refresh()
		for index, item := range items {
			navButtons[index].SetText(item)
		}
		if statusText != nil {
			statusText.Refresh()
		}
	}

	navBottomContainer := container.NewVBox(
		layout.NewSpacer(),
		languageLabel,
		languageSelect,
		themeManager.CreateThemeToggleButton(),
	)
	navBackground := themeManager.CreateGlassmorphismBackground()
	navWithBackground := container.NewStack(
		navBackground,
		container.NewBorder(
			container.NewPadded(logoContainer),
			container.NewPadded(navBottomContainer),
			nil,
			nil,
			container.NewPadded(navContainer),
		),
	)
	// Keep the left navigation at a real, stable width. A fractional HSplit
	// allowed Fyne to negotiate against the very different minimum sizes of the
	// workflow, model and settings pages. On high-DPI/fullscreen windows that
	// negotiation could collapse the active page and the status hint to a few
	// pixels, making labels appear one character per line and hiding every
	// stage button. The Kova workflow is deliberately a fixed sidebar UI, so a
	// Border layout is both clearer and reliable at every supported window size.

	statusTextColor := color.NRGBA{R: 107, G: 114, B: 128, A: 220}
	statusBgColor := color.NRGBA{R: 255, G: 255, B: 255, A: 150}
	if themeManager.IsDarkMode() {
		statusTextColor = color.NRGBA{R: 148, G: 163, B: 184, A: 220}
		statusBgColor = color.NRGBA{R: 30, G: 41, B: 59, A: 150}
	}
	statusText = canvas.NewText("Kova sẵn sàng · Ready", statusTextColor)
	statusText.TextSize = 12
	statusBarBackground := canvas.NewRectangle(statusBgColor)
	statusBarBackground.CornerRadius = 8
	statusBar := container.NewStack(
		statusBarBackground,
		container.NewHBox(layout.NewSpacer(), statusText),
	)

	// Stage-specific actions live inside their corresponding left-nav page. Do
	// not mount a second global action bar here: it used to be squeezed by the
	// outer split and looked like a broken vertical label on large displays.
	bottomArea := container.NewPadded(statusBar)
	finalContainer := createKovaDesktopShell(navWithBackground, contentStack, bottomArea)
	myWindow.SetContent(finalContainer)
	myWindow.Resize(fyne.NewSize(1280, 820))
	myWindow.CenterOnScreen()
	myWindow.ShowAndRun()

	if err := config.SaveConfig(); err != nil {
		log.GetLogger().Error("Failed to save Kova configuration", zap.Error(err))
		return
	}
	log.GetLogger().Info("Kova configuration saved")
}
