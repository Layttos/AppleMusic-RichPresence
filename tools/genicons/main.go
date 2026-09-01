// Command genicons draws the tray icon and writes the two formats the
// platforms want: a template PNG for the macOS menu bar, which the system
// recolours for light and dark, and a multi-size ICO for the Windows
// notification area.
//
// Run it with `go generate ./internal/ui` after changing the artwork.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// The glyph is a pair of beamed eighth notes, described in a unit square and
// rasterised with supersampling so the curves stay smooth at 16 pixels.
const supersample = 8

type note struct {
	// headA and headB are the note heads, headB the taller right-hand one.
	headA, headB ellipse
	stemA, stemB rect
	beam         beam
}

type ellipse struct{ cx, cy, rx, ry, rot float64 }
type rect struct{ x0, y0, x1, y1 float64 }
type beam struct{ x0, y0, x1, y1, thickness float64 }

func glyph() note {
	return note{
		headA: ellipse{cx: 0.235, cy: 0.760, rx: 0.180, ry: 0.135, rot: -0.32},
		headB: ellipse{cx: 0.700, cy: 0.660, rx: 0.180, ry: 0.135, rot: -0.32},
		stemA: rect{x0: 0.383, y0: 0.190, x1: 0.446, y1: 0.775},
		stemB: rect{x0: 0.848, y0: 0.090, x1: 0.911, y1: 0.675},
		beam:  beam{x0: 0.383, y0: 0.190, x1: 0.911, y1: 0.090, thickness: 0.135},
	}
}

// covers reports whether a point in the unit square is inside the glyph.
func (n note) covers(x, y float64) bool {
	return n.headA.covers(x, y) || n.headB.covers(x, y) ||
		n.stemA.covers(x, y) || n.stemB.covers(x, y) || n.beam.covers(x, y)
}

func (e ellipse) covers(x, y float64) bool {
	dx, dy := x-e.cx, y-e.cy
	sin, cos := math.Sin(e.rot), math.Cos(e.rot)
	rx := dx*cos + dy*sin
	ry := -dx*sin + dy*cos
	return (rx*rx)/(e.rx*e.rx)+(ry*ry)/(e.ry*e.ry) <= 1
}

func (r rect) covers(x, y float64) bool {
	return x >= r.x0 && x <= r.x1 && y >= r.y0 && y <= r.y1
}

// covers treats the beam as a thick segment: inside when the point projects
// onto the segment and sits within half a thickness of it vertically.
func (b beam) covers(x, y float64) bool {
	if x < b.x0 || x > b.x1 {
		return false
	}
	t := (x - b.x0) / (b.x1 - b.x0)
	centre := b.y0 + t*(b.y1-b.y0)
	return math.Abs(y-centre) <= b.thickness/2
}

// render draws the glyph at the given size in the given colour, with coverage
// from supersampling used as the alpha channel.
func render(size int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	g := glyph()

	// Inset the glyph slightly so it does not touch the edges.
	const margin = 0.06
	span := 1 - 2*margin

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					fx := (float64(px) + (float64(sx)+0.5)/supersample) / float64(size)
					fy := (float64(py) + (float64(sy)+0.5)/supersample) / float64(size)
					if g.covers((fx-margin)/span, (fy-margin)/span) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			alpha := float64(hits) / float64(supersample*supersample)
			img.SetNRGBA(px, py, color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(alpha * float64(c.A))})
		}
	}
	return img
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeICO packs PNG images into an ICO container. Windows has accepted
// PNG-compressed entries since Vista, so no BMP encoding is needed.
func encodeICO(images map[int][]byte, sizes []int) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0))          // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))          // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(sizes))) // image count

	offset := 6 + 16*len(sizes)
	for _, size := range sizes {
		data := images[size]
		dim := byte(size)
		if size >= 256 {
			dim = 0 // 0 means 256 in the ICO header
		}
		buf.WriteByte(dim)                                         // width
		buf.WriteByte(dim)                                         // height
		buf.WriteByte(0)                                           // palette size
		buf.WriteByte(0)                                           // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))         // colour planes
		binary.Write(&buf, binary.LittleEndian, uint16(32))        // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(data))) // size in bytes
		binary.Write(&buf, binary.LittleEndian, uint32(offset))    // offset
		offset += len(data)
	}
	for _, size := range sizes {
		buf.Write(images[size])
	}
	return buf.Bytes()
}

func main() {
	outDir := "internal/ui"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	// macOS template images must be black with an alpha channel; the system
	// inverts them for a dark menu bar. 44px covers Retina at 22pt.
	template, err := encodePNG(render(44, color.NRGBA{A: 255}))
	if err != nil {
		log.Fatalf("rendering the template icon: %v", err)
	}
	write(filepath.Join(outDir, "icon_template.png"), template)

	// Windows draws the icon over a taskbar that may be light or dark, so the
	// glyph is tinted rather than monochrome.
	accent := color.NRGBA{R: 0xFA, G: 0x2D, B: 0x48, A: 255}
	sizes := []int{16, 20, 24, 32, 48, 64}
	pngs := make(map[int][]byte, len(sizes))
	for _, size := range sizes {
		data, err := encodePNG(render(size, accent))
		if err != nil {
			log.Fatalf("rendering the %dpx icon: %v", size, err)
		}
		pngs[size] = data
	}
	write(filepath.Join(outDir, "icon.ico"), encodeICO(pngs, sizes))

	// A colour PNG is also kept for platforms that want one, and for the
	// application bundle's own icon.
	colourPNG, err := encodePNG(render(256, accent))
	if err != nil {
		log.Fatalf("rendering the colour icon: %v", err)
	}
	write(filepath.Join(outDir, "icon.png"), colourPNG)
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatalf("writing %s: %v", path, err)
	}
	log.Printf("wrote %s (%d bytes)", path, len(data))
}
