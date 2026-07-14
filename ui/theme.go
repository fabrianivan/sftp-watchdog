package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// WatchdogTheme is a custom dark theme for SFTP Watchdog.
type WatchdogTheme struct{}

var _ fyne.Theme = (*WatchdogTheme)(nil)

func (t *WatchdogTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 18, G: 18, B: 24, A: 255}
	case theme.ColorNameButton:
		return color.NRGBA{R: 40, G: 40, B: 56, A: 255}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 30, G: 30, B: 42, A: 255}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 80, G: 80, B: 100, A: 255}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 224, G: 224, B: 240, A: 255}
	case theme.ColorNameHover:
		return color.NRGBA{R: 50, G: 50, B: 72, A: 255}
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 28, G: 28, B: 40, A: 255}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 60, G: 60, B: 80, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 100, G: 100, B: 130, A: 255}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 99, G: 102, B: 241, A: 255} // Indigo-500
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 60, G: 60, B: 80, A: 200}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 40, G: 40, B: 56, A: 255}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 80}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 34, G: 197, B: 94, A: 255} // Green-500
	case theme.ColorNameWarning:
		return color.NRGBA{R: 245, G: 158, B: 11, A: 255} // Amber-500
	case theme.ColorNameError:
		return color.NRGBA{R: 239, G: 68, B: 68, A: 255} // Red-500
	case theme.ColorNameHeaderBackground:
		return color.NRGBA{R: 24, G: 24, B: 32, A: 255}
	case theme.ColorNameMenuBackground:
		return color.NRGBA{R: 28, G: 28, B: 40, A: 255}
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 24, G: 24, B: 32, A: 240}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 99, G: 102, B: 241, A: 60}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 99, G: 102, B: 241, A: 180}
	default:
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}
}

func (t *WatchdogTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *WatchdogTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *WatchdogTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 17
	case theme.SizeNameInputBorder:
		return 1.5
	case theme.SizeNameSeparatorThickness:
		return 1
	default:
		return theme.DefaultTheme().Size(name)
	}
}

// Color constants used across the UI.
var (
	ColorGreen  = color.NRGBA{R: 34, G: 197, B: 94, A: 255}
	ColorRed    = color.NRGBA{R: 239, G: 68, B: 68, A: 255}
	ColorYellow = color.NRGBA{R: 245, G: 158, B: 11, A: 255}
	ColorIndigo = color.NRGBA{R: 99, G: 102, B: 241, A: 255}
	ColorMuted  = color.NRGBA{R: 100, G: 100, B: 130, A: 255}
)
