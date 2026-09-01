//go:build !notray

package ui

import (
	_ "embed"

	"fyne.io/systray"
)

// Windows wants an ICO, and picks the size it needs from the ones packed
// inside. The glyph is tinted rather than monochrome because the notification
// area may be light or dark and does not recolour icons.
//
//go:embed icon.ico
var windowsIcon []byte

func setIcon() {
	systray.SetIcon(windowsIcon)
}
