package main

// A reader for the subset of SVG an icon needs: the viewBox and the filled
// outlines of every <path>. Phosphor's icons are exactly that, and a reader
// this small is cheaper than a dependency for what amounts to a few hundred
// numbers.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type point struct{ X, Y float64 }

// polygon is one closed contour, already flattened to line segments.
type polygon []point

// drawing is a whole icon: its contours and the box they are expressed in.
type drawing struct {
	polys                     []polygon
	minX, minY, width, height float64
}

var (
	viewBoxRe = regexp.MustCompile(`viewBox\s*=\s*"([^"]*)"`)
	pathRe    = regexp.MustCompile(`<path\b[^>]*?\bd\s*=\s*"([^"]*)"`)
)

func parseSVG(src string) (drawing, error) {
	var d drawing

	m := viewBoxRe.FindStringSubmatch(src)
	if m == nil {
		return d, fmt.Errorf("no viewBox attribute")
	}
	fields := strings.FieldsFunc(m[1], func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
	if len(fields) != 4 {
		return d, fmt.Errorf("viewBox %q does not have four numbers", m[1])
	}
	nums := make([]float64, 4)
	for i, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return d, fmt.Errorf("viewBox %q: %w", m[1], err)
		}
		nums[i] = v
	}
	d.minX, d.minY, d.width, d.height = nums[0], nums[1], nums[2], nums[3]
	if d.width <= 0 || d.height <= 0 {
		return d, fmt.Errorf("viewBox %q has no area", m[1])
	}

	paths := pathRe.FindAllStringSubmatch(src, -1)
	if len(paths) == 0 {
		return d, fmt.Errorf("no <path> with a d attribute")
	}
	for _, p := range paths {
		polys, err := flattenPath(p[1])
		if err != nil {
			return d, err
		}
		d.polys = append(d.polys, polys...)
	}
	return d, nil
}

// scanner walks a path's d attribute. Numbers, commands and the single-digit
// arc flags each have their own reader because the grammar allows them to run
// together without separators.
type scanner struct {
	d   string
	pos int
}

func (s *scanner) skipSeparators() {
	for s.pos < len(s.d) {
		switch s.d[s.pos] {
		case ' ', ',', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *scanner) done() bool {
	s.skipSeparators()
	return s.pos >= len(s.d)
}

// peekCommand reports the next command letter without consuming it.
func (s *scanner) peekCommand() (byte, bool) {
	s.skipSeparators()
	if s.pos >= len(s.d) {
		return 0, false
	}
	c := s.d[s.pos]
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return c, true
	}
	return 0, false
}

func (s *scanner) number() (float64, error) {
	s.skipSeparators()
	start := s.pos
	digits := func() {
		for s.pos < len(s.d) && s.d[s.pos] >= '0' && s.d[s.pos] <= '9' {
			s.pos++
		}
	}
	sign := func() {
		if s.pos < len(s.d) && (s.d[s.pos] == '+' || s.d[s.pos] == '-') {
			s.pos++
		}
	}

	sign()
	digits()
	if s.pos < len(s.d) && s.d[s.pos] == '.' {
		s.pos++
		digits()
	}
	if s.pos < len(s.d) && (s.d[s.pos] == 'e' || s.d[s.pos] == 'E') {
		s.pos++
		sign()
		digits()
	}
	if start == s.pos {
		return 0, fmt.Errorf("expected a number at offset %d of %q", start, s.d)
	}
	return strconv.ParseFloat(s.d[start:s.pos], 64)
}

// flag reads an arc flag, which the grammar allows to be a bare digit glued to
// the number that follows it, as in "a1,1,0,011.5,2".
func (s *scanner) flag() (bool, error) {
	s.skipSeparators()
	if s.pos >= len(s.d) {
		return false, fmt.Errorf("expected an arc flag at the end of %q", s.d)
	}
	switch s.d[s.pos] {
	case '0':
		s.pos++
		return false, nil
	case '1':
		s.pos++
		return true, nil
	}
	return false, fmt.Errorf("expected an arc flag at offset %d of %q", s.pos, s.d)
}

// flattener turns path commands into closed polygons.
type flattener struct {
	polys   []polygon
	current polygon
	cur     point // current point
	start   point // start of the current subpath
	// lastCubic and lastQuad are the control points that S and T reflect.
	lastCubic, lastQuad point
	hadCubic, hadQuad   bool
}

func flattenPath(d string) ([]polygon, error) {
	f := &flattener{}
	s := &scanner{d: d}

	var cmd byte
	for !s.done() {
		if c, ok := s.peekCommand(); ok {
			cmd = c
			s.pos++
		} else {
			switch cmd {
			case 0:
				return nil, fmt.Errorf("path does not start with a command: %q", d)
			case 'M':
				// Coordinates repeated after a moveto are implicit linetos.
				cmd = 'L'
			case 'm':
				cmd = 'l'
			}
		}

		if err := f.step(s, cmd); err != nil {
			return nil, err
		}
	}
	f.endSubpath()
	return f.polys, nil
}

func (f *flattener) step(s *scanner, cmd byte) error {
	rel := cmd >= 'a' && cmd <= 'z'
	var err error
	n := func() float64 {
		if err != nil {
			return 0
		}
		var v float64
		v, err = s.number()
		return v
	}
	// coord reads a coordinate pair, made absolute when the command is relative.
	coord := func() point {
		x, y := n(), n()
		if rel {
			return point{f.cur.X + x, f.cur.Y + y}
		}
		return point{x, y}
	}

	switch cmd | 0x20 { // fold to lower case; rel already carries the case
	case 'm':
		p := coord()
		if err != nil {
			return err
		}
		f.endSubpath()
		f.cur, f.start = p, p
		f.current = polygon{p}
		f.hadCubic, f.hadQuad = false, false

	case 'l':
		p := coord()
		if err != nil {
			return err
		}
		f.lineTo(p)
		f.hadCubic, f.hadQuad = false, false

	case 'h':
		x := n()
		if err != nil {
			return err
		}
		if rel {
			x += f.cur.X
		}
		f.lineTo(point{x, f.cur.Y})
		f.hadCubic, f.hadQuad = false, false

	case 'v':
		y := n()
		if err != nil {
			return err
		}
		if rel {
			y += f.cur.Y
		}
		f.lineTo(point{f.cur.X, y})
		f.hadCubic, f.hadQuad = false, false

	case 'c':
		c1, c2, p := coord(), coord(), coord()
		if err != nil {
			return err
		}
		f.cubicTo(c1, c2, p)

	case 's':
		reflected := f.reflectedCubic()
		c2, p := coord(), coord()
		if err != nil {
			return err
		}
		f.cubicTo(reflected, c2, p)

	case 'q':
		c, p := coord(), coord()
		if err != nil {
			return err
		}
		f.quadTo(c, p)

	case 't':
		reflected := f.reflectedQuad()
		p := coord()
		if err != nil {
			return err
		}
		f.quadTo(reflected, p)

	case 'a':
		rx, ry, rotation := n(), n(), n()
		if err != nil {
			return err
		}
		largeArc, flagErr := s.flag()
		if flagErr != nil {
			return flagErr
		}
		sweep, flagErr := s.flag()
		if flagErr != nil {
			return flagErr
		}
		p := coord()
		if err != nil {
			return err
		}
		f.arcTo(rx, ry, rotation, largeArc, sweep, p)
		f.hadCubic, f.hadQuad = false, false

	case 'z':
		f.closeSubpath()

	default:
		return fmt.Errorf("unsupported path command %q", string(cmd))
	}
	return err
}

func (f *flattener) lineTo(p point) {
	if f.current == nil {
		f.current = polygon{f.cur}
	}
	f.current = append(f.current, p)
	f.cur = p
}

// endSubpath files the contour being built away. Filling treats every contour
// as closed, so an unclosed subpath needs no extra segment.
func (f *flattener) endSubpath() {
	if len(f.current) > 1 {
		f.polys = append(f.polys, f.current)
	}
	f.current = nil
}

// closeSubpath handles Z: the contour ends and the pen returns to its start,
// ready for whatever follows without an intervening moveto.
func (f *flattener) closeSubpath() {
	f.endSubpath()
	f.cur = f.start
	f.current = polygon{f.start}
}

func (f *flattener) reflectedCubic() point {
	if !f.hadCubic {
		return f.cur
	}
	return point{2*f.cur.X - f.lastCubic.X, 2*f.cur.Y - f.lastCubic.Y}
}

func (f *flattener) reflectedQuad() point {
	if !f.hadQuad {
		return f.cur
	}
	return point{2*f.cur.X - f.lastQuad.X, 2*f.cur.Y - f.lastQuad.Y}
}

// segmentsFor picks a subdivision count from the length of the control
// polygon, so a long curve is not flattened as coarsely as a short one. The
// lengths are in viewBox units; one segment per unit stays well below a pixel
// at every size rendered here.
func segmentsFor(pts ...point) int {
	length := 0.0
	for i := 1; i < len(pts); i++ {
		length += math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
	}
	n := int(math.Ceil(length))
	if n < 8 {
		return 8
	}
	if n > 96 {
		return 96
	}
	return n
}

func (f *flattener) cubicTo(c1, c2, p point) {
	p0 := f.cur
	n := segmentsFor(p0, c1, c2, p)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		f.lineTo(point{
			X: u*u*u*p0.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*p.X,
			Y: u*u*u*p0.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*p.Y,
		})
	}
	f.cur = p
	f.lastCubic, f.hadCubic = c2, true
	f.hadQuad = false
}

func (f *flattener) quadTo(c, p point) {
	p0 := f.cur
	n := segmentsFor(p0, c, p)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		f.lineTo(point{
			X: u*u*p0.X + 2*u*t*c.X + t*t*p.X,
			Y: u*u*p0.Y + 2*u*t*c.Y + t*t*p.Y,
		})
	}
	f.cur = p
	f.lastQuad, f.hadQuad = c, true
	f.hadCubic = false
}

// arcTo implements the endpoint-to-centre conversion from the SVG
// specification's elliptical arc implementation notes, then walks the sweep in
// steps of at most three degrees.
func (f *flattener) arcTo(rx, ry, rotationDeg float64, largeArc, sweep bool, p point) {
	p0 := f.cur
	if p0 == p {
		return
	}
	if rx == 0 || ry == 0 {
		f.lineTo(p)
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)

	phi := rotationDeg * math.Pi / 180
	cosPhi, sinPhi := math.Cos(phi), math.Sin(phi)

	dx, dy := (p0.X-p.X)/2, (p0.Y-p.Y)/2
	x1 := cosPhi*dx + sinPhi*dy
	y1 := -sinPhi*dx + cosPhi*dy

	// Scale the radii up when they are too small to join the two endpoints.
	if lambda := (x1*x1)/(rx*rx) + (y1*y1)/(ry*ry); lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}

	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	if num < 0 {
		num = 0
	}
	coef := math.Sqrt(num / den)
	if largeArc == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1 / ry
	cyp := -coef * ry * x1 / rx
	cx := cosPhi*cxp - sinPhi*cyp + (p0.X+p.X)/2
	cy := sinPhi*cxp + cosPhi*cyp + (p0.Y+p.Y)/2

	theta := angleBetween(1, 0, (x1-cxp)/rx, (y1-cyp)/ry)
	delta := angleBetween((x1-cxp)/rx, (y1-cyp)/ry, (-x1-cxp)/rx, (-y1-cyp)/ry)
	switch {
	case !sweep && delta > 0:
		delta -= 2 * math.Pi
	case sweep && delta < 0:
		delta += 2 * math.Pi
	}

	n := int(math.Ceil(math.Abs(delta) / (3 * math.Pi / 180)))
	if n < 2 {
		n = 2
	}
	for i := 1; i <= n; i++ {
		t := theta + delta*float64(i)/float64(n)
		cosT, sinT := math.Cos(t), math.Sin(t)
		f.lineTo(point{
			X: cx + rx*cosT*cosPhi - ry*sinT*sinPhi,
			Y: cy + rx*cosT*sinPhi + ry*sinT*cosPhi,
		})
	}
	// Land exactly on the endpoint the path asked for rather than on the
	// rounded result of the last step.
	f.cur = p
	f.current[len(f.current)-1] = p
}

func angleBetween(ux, uy, vx, vy float64) float64 {
	norm := math.Hypot(ux, uy) * math.Hypot(vx, vy)
	if norm == 0 {
		return 0
	}
	c := (ux*vx + uy*vy) / norm
	if c > 1 {
		c = 1
	} else if c < -1 {
		c = -1
	}
	a := math.Acos(c)
	if ux*vy-uy*vx < 0 {
		return -a
	}
	return a
}
