//go:build !notray

package ui

import (
	_ "embed"

	"fyne.io/systray"
)

// The menu bar wants a template image: black pixels plus an alpha channel,
// which macOS recolours itself for light and dark menu bars and for the
// highlighted state. Handing it a coloured icon would leave it looking wrong in
// at least one of those.
//
//go:embed icon_template.png
var templateIcon []byte

func setIcon() {
	systray.SetTemplateIcon(templateIcon, templateIcon)
}
