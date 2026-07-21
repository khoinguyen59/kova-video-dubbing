package desktop

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type ThemeManager struct {
	app        fyne.App
	window     fyne.Window
	theme      *customTheme
	isDarkMode bool
	callbacks  []func(bool)
}

func NewThemeManager(app fyne.App, window fyne.Window) *ThemeManager {
	theme := NewCustomTheme(ThemeModeLight).(*customTheme)
	app.Settings().SetTheme(theme)

	return &ThemeManager{
		app:        app,
		window:     window,
		theme:      theme,
		isDarkMode: false,
		callbacks:  make([]func(bool), 0),
	}
}

func (tm *ThemeManager) IsDarkMode() bool {
	return tm.isDarkMode
}

func (tm *ThemeManager) ToggleTheme() {
	tm.isDarkMode = !tm.isDarkMode

	if tm.isDarkMode {
		tm.theme.SetThemeMode(ThemeModeDark)
	} else {
		tm.theme.SetThemeMode(ThemeModeLight)
	}

	tm.app.Settings().SetTheme(tm.theme)

	for _, callback := range tm.callbacks {
		callback(tm.isDarkMode)
	}

	tm.window.Canvas().Refresh(tm.window.Content())
}

func (tm *ThemeManager) AddThemeChangeCallback(callback func(bool)) {
	tm.callbacks = append(tm.callbacks, callback)
}

func (tm *ThemeManager) CreateGlassmorphismCard(title, subtitle string, content fyne.CanvasObject) *fyne.Container {
	return GlassmorphismCard(title, subtitle, content, tm.isDarkMode)
}

func (tm *ThemeManager) CreateModernCard(title string, content fyne.CanvasObject) *fyne.Container {
	return ModernCard(title, content, tm.isDarkMode)
}

func (tm *ThemeManager) CreateTransparentCard(content fyne.CanvasObject) *fyne.Container {
	return TransparentCard(content, tm.isDarkMode)
}

func (tm *ThemeManager) CreateThemeToggleButton() *fyne.Container {
	var themeToggleBtn *widget.Button

	updateButton := func() {
		if tm.isDarkMode {
			themeToggleBtn.SetText("Chế độ sáng / Light")
		} else {
			themeToggleBtn.SetText("Chế độ tối / Dark")
		}
		themeToggleBtn.Refresh()
	}

	themeToggleBtn = ThemeToggleButton(tm.isDarkMode, func() {
		tm.ToggleTheme()
		updateButton()
	})

	tm.AddThemeChangeCallback(func(isDark bool) {
		updateButton()
	})

	return container.NewPadded(themeToggleBtn)
}

func (tm *ThemeManager) CreateGlassmorphismBackground() *canvas.Rectangle {
	var bgColor color.Color
	var borderColor color.Color

	if tm.isDarkMode {
		bgColor = color.NRGBA{R: 30, G: 41, B: 59, A: 180}
		borderColor = color.NRGBA{R: 51, G: 65, B: 85, A: 100}
	} else {
		bgColor = color.NRGBA{R: 255, G: 255, B: 255, A: 180}
		borderColor = color.NRGBA{R: 209, G: 213, B: 219, A: 100}
	}

	background := canvas.NewRectangle(bgColor)
	background.CornerRadius = 16
	background.StrokeColor = borderColor
	background.StrokeWidth = 1

	return background
}

func (tm *ThemeManager) CreateTransparentBackground() *canvas.Rectangle {
	var bgColor color.Color
	var borderColor color.Color

	if tm.isDarkMode {
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

	return background
}

func (tm *ThemeManager) CreateGradientBackground() *canvas.LinearGradient {
	var startColor, endColor color.Color

	if tm.isDarkMode {
		startColor = color.NRGBA{R: 15, G: 23, B: 42, A: 255}
		endColor = color.NRGBA{R: 30, G: 41, B: 59, A: 255}
	} else {
		startColor = color.NRGBA{R: 248, G: 250, B: 252, A: 255}
		endColor = color.NRGBA{R: 241, G: 245, B: 249, A: 255}
	}

	return canvas.NewLinearGradient(startColor, endColor, 0.0)
}

func (tm *ThemeManager) UpdateAllComponents() {
	tm.window.Canvas().Refresh(tm.window.Content())
}
