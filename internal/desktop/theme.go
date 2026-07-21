package desktop

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type ThemeMode int

const (
	ThemeModeLight ThemeMode = iota
	ThemeModeDark
	ThemeModeAuto
)

type customTheme struct {
	baseTheme     fyne.Theme
	mode          ThemeMode
	forceDark     bool
	onThemeChange []func(ThemeMode)
}

func NewCustomTheme(mode ThemeMode) fyne.Theme {
	forceDark := mode == ThemeModeDark
	return &customTheme{
		baseTheme:     theme.DefaultTheme(),
		mode:          mode,
		forceDark:     forceDark,
		onThemeChange: make([]func(ThemeMode), 0),
	}
}

func (t *customTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.forceDark || (t.mode == ThemeModeAuto && variant == theme.VariantDark) || t.mode == ThemeModeDark {
		return t.darkColors(name)
	}
	return t.lightColors(name)
}

func (t *customTheme) lightColors(name fyne.ThemeColorName) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 59, G: 130, B: 246, A: 255}

	case theme.ColorNameBackground:
		return color.NRGBA{R: 248, G: 250, B: 252, A: 255}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 17, G: 24, B: 39, A: 255}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 156, G: 163, B: 175, A: 150}

	case theme.ColorNameButton:
		return color.NRGBA{R: 59, G: 130, B: 246, A: 255}
	case theme.ColorNameHover:
		return color.NRGBA{R: 37, G: 99, B: 235, A: 255}
	case theme.ColorNamePressed:
		return color.NRGBA{R: 29, G: 78, B: 216, A: 255}

	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 209, G: 213, B: 219, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 156, G: 163, B: 175, A: 200}

	case theme.ColorNameSelection:
		return color.NRGBA{R: 219, G: 234, B: 254, A: 180}
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 209, G: 213, B: 219, A: 200}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 25}

	case theme.ColorNameError:
		return color.NRGBA{R: 239, G: 68, B: 68, A: 255}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 245, G: 158, B: 11, A: 255}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 34, G: 197, B: 94, A: 255}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 59, G: 130, B: 246, A: 100}

	default:
		return t.baseTheme.Color(name, theme.VariantLight)
	}
}

func (t *customTheme) darkColors(name fyne.ThemeColorName) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 96, G: 165, B: 250, A: 255}

	case theme.ColorNameBackground:
		return color.NRGBA{R: 15, G: 23, B: 42, A: 255}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 248, G: 250, B: 252, A: 255}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 100, G: 116, B: 139, A: 150}

	case theme.ColorNameButton:
		return color.NRGBA{R: 30, G: 41, B: 59, A: 255}
	case theme.ColorNameHover:
		return color.NRGBA{R: 51, G: 65, B: 85, A: 255}
	case theme.ColorNamePressed:
		return color.NRGBA{R: 15, G: 23, B: 42, A: 255}

	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 30, G: 41, B: 59, A: 255}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 51, G: 65, B: 85, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 148, G: 163, B: 184, A: 200}

	case theme.ColorNameSelection:
		return color.NRGBA{R: 59, G: 130, B: 246, A: 180}
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 51, G: 65, B: 85, A: 200}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 50}

	case theme.ColorNameError:
		return color.NRGBA{R: 248, G: 113, B: 113, A: 255}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 251, G: 191, B: 36, A: 255}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 74, G: 222, B: 128, A: 255}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 96, G: 165, B: 250, A: 100}

	default:
		return t.baseTheme.Color(name, theme.VariantDark)
	}
}

func (t *customTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.baseTheme.Icon(name)
}

func (t *customTheme) Font(style fyne.TextStyle) fyne.Resource {
	return t.baseTheme.Font(style)
}

func (t *customTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 12
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameScrollBar:
		return 8
	case theme.SizeNameScrollBarSmall:
		return 4
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameText:
		return 14
	case theme.SizeNameInputBorder:
		return 1.5
	case theme.SizeNameInputRadius:
		return 6
	default:
		return t.baseTheme.Size(name)
	}
}

func (t *customTheme) GetThemeMode() ThemeMode {
	return t.mode
}

func (t *customTheme) SetThemeMode(mode ThemeMode) {
	if t.mode != mode {
		t.mode = mode
		t.forceDark = mode == ThemeModeDark
		for _, callback := range t.onThemeChange {
			callback(mode)
		}
	}
}

func (t *customTheme) AddThemeChangeCallback(callback func(ThemeMode)) {
	t.onThemeChange = append(t.onThemeChange, callback)
}
