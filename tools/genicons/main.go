// Command genicons rasterises icon.svg into the formats the platforms want: a
// template PNG for the macOS menu bar, which the system recolours for light and
// dark, a multi-size ICO for the Windows notification area and for the
// executable's own icon, and a colour PNG for the macOS bundle icon.
//
// Run it with `go generate ./internal/ui` after changing the artwork. Dropping
// in a different SVG is enough; nothing here is specific to the current glyph.
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
)

// accent is Apple Music's red. The notification area may be light or dark and
// does not recolour what it is given, so the Windows glyph is tinted rather
// than monochrome.
var accent = color.NRGBA{R: 0xFA, G: 0x2D, B: 0x48, A: 255}

// icoSizes covers what Windows asks for, from the notification area at 16 to
// the large icon view at 256.
var icoSizes = []int{16, 20, 24, 32, 48, 64, 128, 256}

// margin is the fraction of the square left empty around the glyph, so it does
// not touch the edges of a menu bar or a taskbar button.
const margin = 0.06

func main() {
	dir := "internal/ui"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	source := filepath.Join(dir, "icon.svg")
	svg, err := os.ReadFile(source)
	if err != nil {
		log.Fatalf("reading %s: %v", source, err)
	}
	art, err := parseSVG(string(svg))
	if err != nil {
		log.Fatalf("parsing %s: %v", source, err)
	}

	// macOS template images must be black with an alpha channel; the system
	// inverts them for a dark menu bar. 44px covers Retina at 22pt.
	write(filepath.Join(dir, "icon_template.png"),
		encodePNG(render(art, 44, margin, color.NRGBA{A: 255})))

	pngs := make(map[int][]byte, len(icoSizes))
	for _, size := range icoSizes {
		pngs[size] = encodePNG(render(art, size, margin, accent))
	}
	write(filepath.Join(dir, "icon.ico"), encodeICO(pngs, icoSizes))

	// A colour PNG is also kept for platforms that want one, and for the macOS
	// application bundle's own icon.
	write(filepath.Join(dir, "icon.png"), pngs[256])
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatalf("encoding a PNG: %v", err)
	}
	return buf.Bytes()
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatalf("writing %s: %v", path, err)
	}
	log.Printf("wrote %s (%d bytes)", path, len(data))
}
