package main

// Scanline filling with the non-zero winding rule, which is what SVG defaults
// to and what Phosphor's artwork is drawn for: a hole is a contour wound the
// other way round.
//
// Coverage is exact horizontally — a span contributes its real width to the
// pixels it partly covers — and sampled vertically. Sixteen sub-scanlines is
// far more than a 16 pixel icon needs and still costs milliseconds.

import (
	"image"
	"image/color"
	"math"
	"sort"
)

const subScanlines = 16

type edge struct {
	x0, y0, x1, y1 float64
	// winding is +1 when the edge runs downwards, -1 when it runs upwards.
	winding int
}

// bounds returns the tightest box containing every contour.
func (d drawing) bounds() (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, poly := range d.polys {
		for _, p := range poly {
			minX = math.Min(minX, p.X)
			minY = math.Min(minY, p.Y)
			maxX = math.Max(maxX, p.X)
			maxY = math.Max(maxY, p.Y)
		}
	}
	return
}

// edges maps the contours into a square of the given size, scaling the drawing
// to fill it with the given margin left free on the tightest side. Fitting the
// ink rather than the viewBox means a swapped-in icon keeps its weight,
// whatever padding its author left around it.
func (d drawing) edges(size int, margin float64) []edge {
	minX, minY, maxX, maxY := d.bounds()
	inkW, inkH := maxX-minX, maxY-minY
	if inkW <= 0 || inkH <= 0 {
		return nil
	}

	span := float64(size) * (1 - 2*margin)
	scale := math.Min(span/inkW, span/inkH)
	offX := (float64(size) - inkW*scale) / 2
	offY := (float64(size) - inkH*scale) / 2

	var edges []edge
	for _, poly := range d.polys {
		n := len(poly)
		for i := 0; i < n; i++ {
			a, b := poly[i], poly[(i+1)%n] // the last segment closes the contour
			ax := (a.X-minX)*scale + offX
			ay := (a.Y-minY)*scale + offY
			bx := (b.X-minX)*scale + offX
			by := (b.Y-minY)*scale + offY
			if ay == by {
				continue // horizontal edges never cross a scanline
			}
			e := edge{x0: ax, y0: ay, x1: bx, y1: by, winding: 1}
			if ay > by {
				e = edge{x0: bx, y0: by, x1: ax, y1: ay, winding: -1}
			}
			edges = append(edges, e)
		}
	}
	return edges
}

type crossing struct {
	x       float64
	winding int
}

// render fills the drawing at the given size in the given colour, using
// coverage as the alpha channel.
func render(d drawing, size int, margin float64, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	edges := d.edges(size, margin)
	if len(edges) == 0 {
		return img
	}

	row := make([]float64, size)
	crossings := make([]crossing, 0, 64)

	for py := 0; py < size; py++ {
		for i := range row {
			row[i] = 0
		}

		for s := 0; s < subScanlines; s++ {
			y := float64(py) + (float64(s)+0.5)/subScanlines

			crossings = crossings[:0]
			for _, e := range edges {
				if y < e.y0 || y >= e.y1 {
					continue
				}
				t := (y - e.y0) / (e.y1 - e.y0)
				crossings = append(crossings, crossing{x: e.x0 + t*(e.x1-e.x0), winding: e.winding})
			}
			if len(crossings) < 2 {
				continue
			}
			sort.Slice(crossings, func(i, j int) bool { return crossings[i].x < crossings[j].x })

			winding, spanStart := 0, 0.0
			for _, cr := range crossings {
				was := winding
				winding += cr.winding
				switch {
				case was == 0 && winding != 0:
					spanStart = cr.x
				case was != 0 && winding == 0:
					addSpan(row, spanStart, cr.x)
				}
			}
		}

		for px := 0; px < size; px++ {
			coverage := row[px] / subScanlines
			if coverage <= 0 {
				continue
			}
			if coverage > 1 {
				coverage = 1
			}
			img.SetNRGBA(px, py, color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(coverage*float64(c.A) + 0.5)})
		}
	}
	return img
}

// addSpan accumulates the horizontal coverage of one filled interval, giving
// the two partly covered end pixels their exact fraction.
func addSpan(row []float64, x0, x1 float64) {
	if x0 < 0 {
		x0 = 0
	}
	if max := float64(len(row)); x1 > max {
		x1 = max
	}
	if x1 <= x0 {
		return
	}

	first, last := int(x0), int(x1)
	if last >= len(row) {
		last = len(row) - 1
	}
	if first == last {
		row[first] += x1 - x0
		return
	}
	row[first] += float64(first+1) - x0
	for i := first + 1; i < last; i++ {
		row[i] += 1
	}
	row[last] += x1 - float64(last)
}
