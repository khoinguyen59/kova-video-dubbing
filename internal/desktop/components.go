package desktop

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var globalThemeManager *ThemeManager

func SetGlobalThemeManager(tm *ThemeManager) {
	globalThemeManager = tm
}

func GetCurrentThemeIsDark() bool {
	if globalThemeManager != nil {
		return globalThemeManager.IsDarkMode()
	}
	return false
}

func FadeAnimation(content fyne.CanvasObject, duration time.Duration, startOpacity, endOpacity float64) {
	rect := canvas.NewRectangle(color.NRGBA{R: 240, G: 246, B: 252, A: 0})
	rect.FillColor = color.NRGBA{R: 240, G: 246, B: 252, A: uint8(startOpacity * 255)}

	anim := canvas.NewColorRGBAAnimation(
		color.NRGBA{R: 240, G: 246, B: 252, A: uint8(startOpacity * 255)},
		color.NRGBA{R: 240, G: 246, B: 252, A: uint8(endOpacity * 255)},
		duration,
		func(c color.Color) {
			rect.FillColor = c
			content.Refresh()
		})

	anim.Start()
}

func GlassmorphismCard(title, subtitle string, content fyne.CanvasObject, isDark bool) *fyne.Container {
	var bgColor color.Color
	var titleColor color.Color
	var subtitleColor color.Color
	var borderColor color.Color

	if isDark {
		bgColor = color.NRGBA{R: 30, G: 41, B: 59, A: 180}
		titleColor = color.NRGBA{R: 248, G: 250, B: 252, A: 255}
		subtitleColor = color.NRGBA{R: 148, G: 163, B: 184, A: 200}
		borderColor = color.NRGBA{R: 51, G: 65, B: 85, A: 100}
	} else {
		bgColor = color.NRGBA{R: 255, G: 255, B: 255, A: 180}
		titleColor = color.NRGBA{R: 17, G: 24, B: 39, A: 255}
		subtitleColor = color.NRGBA{R: 107, G: 114, B: 128, A: 200}
		borderColor = color.NRGBA{R: 209, G: 213, B: 219, A: 100}
	}

	glassBackground := canvas.NewRectangle(bgColor)
	glassBackground.CornerRadius = 16
	glassBackground.StrokeColor = borderColor
	glassBackground.StrokeWidth = 1

	titleLabel := canvas.NewText(title, titleColor)
	titleLabel.TextSize = 18
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	var subtitleLabel *canvas.Text
	if subtitle != "" {
		subtitleLabel = canvas.NewText(subtitle, subtitleColor)
		subtitleLabel.TextSize = 14
	}

	var headerContainer *fyne.Container
	if subtitleLabel != nil {
		headerContainer = container.NewVBox(titleLabel, subtitleLabel)
	} else {
		headerContainer = container.NewVBox(titleLabel)
	}

	dividerColor := color.NRGBA{R: 209, G: 213, B: 219, A: 100}
	if isDark {
		dividerColor = color.NRGBA{R: 51, G: 65, B: 85, A: 100}
	}
	divider := canvas.NewLine(dividerColor)
	divider.StrokeWidth = 1

	contentWithPadding := container.NewPadded(content)

	cardContent := container.NewBorder(
		container.NewVBox(container.NewPadded(headerContainer), divider),
		nil, nil, nil,
		contentWithPadding,
	)

	shadow1 := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 10})
	shadow1.Move(fyne.NewPos(2, 2))
	shadow1.CornerRadius = 16

	shadow2 := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 5})
	shadow2.Move(fyne.NewPos(4, 4))
	shadow2.CornerRadius = 16

	return container.NewStack(shadow2, shadow1, glassBackground, cardContent)
}

func TransparentCard(content fyne.CanvasObject, isDark bool) *fyne.Container {
	var bgColor color.Color
	var borderColor color.Color

	if isDark {
		bgColor = color.NRGBA{R: 30, G: 41, B: 59, A: 120}
		borderColor = color.NRGBA{R: 51, G: 65, B: 85, A: 80}
	} else {
		bgColor = color.NRGBA{R: 255, G: 255, B: 255, A: 120}
		borderColor = color.NRGBA{R: 209, G: 213, B: 219, A: 80}
	}

	background := canvas.NewRectangle(bgColor)
	background.CornerRadius = 12
	background.StrokeColor = borderColor
	background.StrokeWidth = 1

	return container.NewStack(background, container.NewPadded(content))
}

func PrimaryButton(text string, icon fyne.Resource, action func()) *widget.Button {
	btn := widget.NewButtonWithIcon(text, icon, action)
	btn.Importance = widget.HighImportance
	return btn
}

func SecondaryButton(text string, icon fyne.Resource, action func()) *widget.Button {
	btn := widget.NewButtonWithIcon(text, icon, action)
	btn.Importance = widget.MediumImportance
	return btn
}

func GhostButton(text string, icon fyne.Resource, action func()) *widget.Button {
	btn := widget.NewButtonWithIcon(text, icon, action)
	btn.Importance = widget.LowImportance
	return btn
}

func TitleText(text string) *canvas.Text {
	title := canvas.NewText(text, theme.Color(theme.ColorNamePrimary))
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	return title
}

func SubtitleText(text string) *canvas.Text {
	subtitle := canvas.NewText(text, theme.Color(theme.ColorNameForeground))
	subtitle.TextSize = 16
	subtitle.TextStyle = fyne.TextStyle{Italic: true}
	subtitle.Alignment = fyne.TextAlignCenter
	return subtitle
}

func createShadowRectangle(fillColor color.Color, cornerRadius float32) *canvas.Rectangle {
	rect := canvas.NewRectangle(fillColor)
	rect.CornerRadius = cornerRadius
	return rect
}

func GlassCard(title, subtitle string, content fyne.CanvasObject) *fyne.Container {
	return GlassmorphismCard(title, subtitle, content, false)
}

func StyledCard(title string, content fyne.CanvasObject) *fyne.Container {
	bg := createShadowRectangle(color.NRGBA{R: 250, G: 251, B: 254, A: 255}, 12)

	titleLabel := canvas.NewText(title, color.NRGBA{R: 17, G: 24, B: 39, A: 255})
	titleLabel.TextSize = 16
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	divider := canvas.NewRectangle(color.NRGBA{R: 229, G: 231, B: 235, A: 255})
	divider.SetMinSize(fyne.NewSize(0, 1))

	contentContainer := container.NewBorder(
		container.NewVBox(
			container.NewPadded(titleLabel),
			divider,
		),
		nil, nil, nil,
		container.NewPadded(content),
	)

	shadow := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 15})
	shadow.Move(fyne.NewPos(2, 2))
	shadow.SetMinSize(fyne.NewSize(contentContainer.Size().Width+4, contentContainer.Size().Height+4))
	shadow.CornerRadius = 12

	return container.NewStack(shadow, bg, contentContainer)
}

func StyledSelect(options []string, selected func(string)) *widget.Select {
	sel := widget.NewSelect(options, selected)

	for _, option := range options {
		if len(option) > 8 {
			extraOptions := make([]string, len(options))
			copy(extraOptions, options)

			maxOption := ""
			for _, opt := range options {
				if len(opt) > len(maxOption) {
					maxOption = opt
				}
			}

			padding := "                          "
			if len(maxOption) < 20 {
				maxOption = maxOption + padding
			}

			sel = widget.NewSelect(extraOptions, selected)
			break
		}
	}

	return sel
}

func StyledEntry(placeholder string) *widget.Entry {
	entry := widget.NewEntry()
	entry.SetPlaceHolder(placeholder)
	return entry
}

func StyledPasswordEntry(placeholder string) *widget.Entry {
	entry := widget.NewPasswordEntry()
	entry.SetPlaceHolder(placeholder)
	return entry
}

func DividedContainer(vertical bool, items ...fyne.CanvasObject) *fyne.Container {
	if len(items) <= 1 {
		if len(items) == 1 {
			return container.NewPadded(items[0])
		}
		return container.NewPadded()
	}

	var dividers []fyne.CanvasObject
	for i := 0; i < len(items)-1; i++ {
		dividers = append(dividers, createDivider(vertical))
	}

	var objects []fyne.CanvasObject
	for i, item := range items {
		objects = append(objects, item)
		if i < len(dividers) {
			objects = append(objects, dividers[i])
		}
	}

	if vertical {
		return container.New(layout.NewVBoxLayout(), objects...)
	}
	return container.New(layout.NewHBoxLayout(), objects...)
}

func createDivider(vertical bool) fyne.CanvasObject {
	divider := canvas.NewRectangle(color.NRGBA{R: 209, G: 213, B: 219, A: 255})
	if vertical {
		divider.SetMinSize(fyne.NewSize(0, 1))
	} else {
		divider.SetMinSize(fyne.NewSize(1, 0))
	}
	return divider
}

func ProgressWithLabel(initial float64) (*widget.ProgressBar, *widget.Label, *fyne.Container) {
	progress := widget.NewProgressBar()
	progress.SetValue(initial)

	label := widget.NewLabel("0%")

	container := container.NewBorder(nil, nil, nil, label, progress)

	return progress, label, container
}

func UpdateProgressLabel(progress *widget.ProgressBar, label *widget.Label) {
	percentage := int(progress.Value * 100)
	label.SetText(fmt.Sprintf("%d%%", percentage))
}

func AnimatedContainer() *fyne.Container {
	return container.NewStack()
}

func SwitchContent(container *fyne.Container, content fyne.CanvasObject, duration time.Duration) {
	if container == nil || content == nil {
		return
	}

	if len(container.Objects) > 0 {
		oldContent := container.Objects[0]
		FadeAnimation(oldContent, duration/2, 1.0, 0.0)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("Kova content switch error:", r)
				}
			}()

			time.Sleep(duration / 2)
			container.Objects = []fyne.CanvasObject{content}
			container.Refresh()
			FadeAnimation(content, duration/2, 0.0, 1.0)
		}()
	} else {
		container.Objects = []fyne.CanvasObject{content}
		container.Refresh()
		FadeAnimation(content, duration/2, 0.0, 1.0)
	}
}

func ModernCard(title string, content fyne.CanvasObject, isDark bool) *fyne.Container {
	var bgColor color.Color
	var titleColor color.Color
	var borderColor color.Color

	if isDark {
		bgColor = color.NRGBA{R: 30, G: 41, B: 59, A: 255}
		titleColor = color.NRGBA{R: 248, G: 250, B: 252, A: 255}
		borderColor = color.NRGBA{R: 51, G: 65, B: 85, A: 255}
	} else {
		bgColor = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		titleColor = color.NRGBA{R: 17, G: 24, B: 39, A: 255}
		borderColor = color.NRGBA{R: 209, G: 213, B: 219, A: 255}
	}

	background := canvas.NewRectangle(bgColor)
	background.CornerRadius = 12
	background.StrokeColor = borderColor
	background.StrokeWidth = 1

	titleLabel := canvas.NewText(title, titleColor)
	titleLabel.TextSize = 16
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	divider := canvas.NewRectangle(color.NRGBA{R: 229, G: 231, B: 235, A: 255})
	if isDark {
		divider.FillColor = color.NRGBA{R: 51, G: 65, B: 85, A: 255}
	}
	divider.SetMinSize(fyne.NewSize(0, 1))

	contentContainer := container.NewBorder(
		container.NewVBox(
			container.NewPadded(titleLabel),
			divider,
		),
		nil, nil, nil,
		container.NewPadded(content),
	)

	shadow := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 10})
	shadow.Move(fyne.NewPos(2, 2))
	shadow.CornerRadius = 12

	return container.NewStack(shadow, background, contentContainer)
}

func ThemeToggleButton(isDark bool, onToggle func()) *widget.Button {
	var icon fyne.Resource
	var text string

	if isDark {
		icon = theme.ColorPaletteIcon()
		text = "Chế độ sáng / Light"
	} else {
		icon = theme.ColorPaletteIcon()
		text = "Chế độ tối / Dark"
	}

	btn := widget.NewButtonWithIcon(text, icon, onToggle)
	btn.Importance = widget.MediumImportance
	return btn
}

type tappableObject struct {
	widget.BaseWidget
	rect    *canvas.Rectangle
	onTap   func()
	onHover func(bool)
}

func (t *tappableObject) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.rect)
}

func (t *tappableObject) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *tappableObject) TappedSecondary(*fyne.PointEvent) {}

func (t *tappableObject) MouseIn(*desktop.MouseEvent) {
	if t.onHover != nil {
		t.onHover(true)
	}
}

func (t *tappableObject) MouseOut() {
	if t.onHover != nil {
		t.onHover(false)
	}
}

func (t *tappableObject) MouseMoved(*desktop.MouseEvent) {}

func (t *tappableObject) Resize(size fyne.Size) {
	t.BaseWidget.Resize(size)
	if t.rect != nil {
		t.rect.Resize(size)
	}
}

func (t *tappableObject) Move(pos fyne.Position) {
	t.BaseWidget.Move(pos)
	if t.rect != nil {
		t.rect.Move(pos)
	}
}
