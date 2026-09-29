package captcha

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// vec is a 2D point in pixel space (y grows downwards).
type vec struct{ x, y float64 }

func (a vec) add(b vec) vec           { return vec{a.x + b.x, a.y + b.y} }
func (a vec) sub(b vec) vec           { return vec{a.x - b.x, a.y - b.y} }
func (a vec) mul(k float64) vec       { return vec{a.x * k, a.y * k} }
func (a vec) length() float64         { return math.Hypot(a.x, a.y) }
func polar(r, theta float64) vec      { return vec{r * math.Cos(theta), r * math.Sin(theta)} }
func lerpVec(a, b vec, t float64) vec { return a.add(b.sub(a).mul(t)) }

// affine maps (x, y) to (m0*x + m1*y + m2, m3*x + m4*y + m5).
type affine [6]float64

// xform scales by s, rotates by theta (radians, clockwise on screen) and
// then translates to (tx, ty).
func xform(tx, ty, theta, s float64) affine {
	c, sn := math.Cos(theta)*s, math.Sin(theta)*s
	return affine{c, -sn, tx, sn, c, ty}
}

func (m affine) apply(p vec) vec {
	return vec{m[0]*p.x + m[1]*p.y + m[2], m[3]*p.x + m[4]*p.y + m[5]}
}

type segOp uint8

const (
	opMove segOp = iota
	opLine
	opQuad
	opCube
)

type seg struct {
	op segOp
	p  [3]vec
}

// path is a sequence of closed sub-paths; every moveTo starts a new one.
type path []seg

func (p *path) moveTo(a vec)       { *p = append(*p, seg{op: opMove, p: [3]vec{a}}) }
func (p *path) lineTo(a vec)       { *p = append(*p, seg{op: opLine, p: [3]vec{a}}) }
func (p *path) quadTo(a, b vec)    { *p = append(*p, seg{op: opQuad, p: [3]vec{a, b}}) }
func (p *path) cubeTo(a, b, c vec) { *p = append(*p, seg{op: opCube, p: [3]vec{a, b, c}}) }
func (p *path) extend(q path)      { *p = append(*p, q...) }
func (p path) transformed(m affine) path {
	out := make(path, len(p))
	for i, s := range p {
		out[i].op = s.op
		for j := range s.p {
			out[i].p[j] = m.apply(s.p[j])
		}
	}
	return out
}

func (s seg) points() int {
	switch s.op {
	case opQuad:
		return 2
	case opCube:
		return 3
	}
	return 1
}

// bezierK approximates a quarter circle with a cubic Bézier curve.
const bezierK = 0.5522847498

func circlePath(c vec, r float64) path {
	k := r * bezierK
	var p path
	p.moveTo(vec{c.x + r, c.y})
	p.cubeTo(vec{c.x + r, c.y + k}, vec{c.x + k, c.y + r}, vec{c.x, c.y + r})
	p.cubeTo(vec{c.x - k, c.y + r}, vec{c.x - r, c.y + k}, vec{c.x - r, c.y})
	p.cubeTo(vec{c.x - r, c.y - k}, vec{c.x - k, c.y - r}, vec{c.x, c.y - r})
	p.cubeTo(vec{c.x + k, c.y - r}, vec{c.x + r, c.y - k}, vec{c.x + r, c.y})
	return p
}

func roundedRect(x, y, w, h, r float64) path {
	r = math.Min(r, math.Min(w, h)/2)
	k := r * (1 - bezierK)
	x1, y1 := x+w, y+h
	var p path
	p.moveTo(vec{x + r, y})
	p.lineTo(vec{x1 - r, y})
	p.cubeTo(vec{x1 - k, y}, vec{x1, y + k}, vec{x1, y + r})
	p.lineTo(vec{x1, y1 - r})
	p.cubeTo(vec{x1, y1 - k}, vec{x1 - k, y1}, vec{x1 - r, y1})
	p.lineTo(vec{x + r, y1})
	p.cubeTo(vec{x + k, y1}, vec{x, y1 - k}, vec{x, y1 - r})
	p.lineTo(vec{x, y + r})
	p.cubeTo(vec{x, y + k}, vec{x + k, y}, vec{x + r, y})
	return p
}

// roundedPolygon closes the polygon pts with corners rounded by radius r.
func roundedPolygon(pts []vec, r float64) path {
	n := len(pts)
	var p path
	for i := range pts {
		v := pts[i]
		prev, next := pts[(i+n-1)%n], pts[(i+1)%n]
		dp, dn := prev.sub(v), next.sub(v)
		rp := math.Min(r, dp.length()/2)
		rn := math.Min(r, dn.length()/2)
		a := v.add(dp.mul(rp / dp.length()))
		b := v.add(dn.mul(rn / dn.length()))
		if i == 0 {
			p.moveTo(a)
		} else {
			p.lineTo(a)
		}
		p.quadTo(v, b)
	}
	return p
}

// strokeCurve builds a tapered brush stroke along the polyline pts. The
// stroke is split into short abutting pieces so that each fill only touches
// a small bounding box instead of the whole canvas.
func strokeCurve(pts []vec, width float64) []path {
	n := len(pts)
	if n < 2 {
		return nil
	}
	left := make([]vec, n)
	right := make([]vec, n)
	for i := range pts {
		a, b := pts[max(i-1, 0)], pts[min(i+1, n-1)]
		d := b.sub(a)
		l := d.length()
		if l == 0 {
			l = 1
		}
		t := float64(i) / float64(n-1)
		hw := width / 2 * (0.35 + 0.65*math.Sin(math.Pi*t))
		nrm := vec{-d.y / l * hw, d.x / l * hw}
		left[i] = pts[i].add(nrm)
		right[i] = pts[i].sub(nrm)
	}
	const piece = 8
	var out []path
	for lo := 0; lo < n-1; lo += piece {
		hi := min(lo+piece, n-1)
		var p path
		p.moveTo(left[lo])
		for _, q := range left[lo+1 : hi+1] {
			p.lineTo(q)
		}
		for i := hi; i >= lo; i-- {
			p.lineTo(right[i])
		}
		out = append(out, p)
	}
	return out
}

// cubicPoints samples a cubic Bézier curve.
func cubicPoints(a, b, c, d vec, samples int) []vec {
	pts := make([]vec, samples+1)
	for i := range pts {
		t := float64(i) / float64(samples)
		ab, bc, cd := lerpVec(a, b, t), lerpVec(b, c, t), lerpVec(c, d, t)
		pts[i] = lerpVec(lerpVec(ab, bc, t), lerpVec(bc, cd, t), t)
	}
	return pts
}

// canvas is an opaque RGBA image with an anti-aliased path filler.
type canvas struct {
	img *image.RGBA
	z   *vector.Rasterizer
}

func newCanvas(w, h int) *canvas {
	return &canvas{img: image.NewRGBA(image.Rect(0, 0, w, h)), z: vector.NewRasterizer(1, 1)}
}

// verticalGradient paints the whole canvas with a top-to-bottom gradient.
func (c *canvas) verticalGradient(top, bottom color.NRGBA) {
	b := c.img.Bounds()
	h := float64(b.Dy() - 1)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		t := float64(y-b.Min.Y) / h
		r := uint8(float64(top.R) + (float64(bottom.R)-float64(top.R))*t + 0.5)
		g := uint8(float64(top.G) + (float64(bottom.G)-float64(top.G))*t + 0.5)
		bl := uint8(float64(top.B) + (float64(bottom.B)-float64(top.B))*t + 0.5)
		row := c.img.Pix[c.img.PixOffset(b.Min.X, y):c.img.PixOffset(b.Max.X, y)]
		for i := 0; i < len(row); i += 4 {
			row[i], row[i+1], row[i+2], row[i+3] = r, g, bl, 0xff
		}
	}
}

// fill paints p (non-zero winding, sub-paths unioned) with col.
func (c *canvas) fill(p path, col color.NRGBA) {
	if len(p) == 0 || col.A == 0 {
		return
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, s := range p {
		for _, q := range s.p[:s.points()] {
			minX, maxX = math.Min(minX, q.x), math.Max(maxX, q.x)
			minY, maxY = math.Min(minY, q.y), math.Max(maxY, q.y)
		}
	}
	r := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(c.img.Bounds())
	if r.Empty() {
		return
	}
	c.z.Reset(r.Dx(), r.Dy())
	ox, oy := float64(r.Min.X), float64(r.Min.Y)
	pt := func(v vec) (float32, float32) { return float32(v.x - ox), float32(v.y - oy) }
	for _, s := range p {
		switch s.op {
		case opMove:
			c.z.MoveTo(pt(s.p[0]))
		case opLine:
			c.z.LineTo(pt(s.p[0]))
		case opQuad:
			bx, by := pt(s.p[0])
			cx, cy := pt(s.p[1])
			c.z.QuadTo(bx, by, cx, cy)
		case opCube:
			bx, by := pt(s.p[0])
			cx, cy := pt(s.p[1])
			dx, dy := pt(s.p[2])
			c.z.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	c.z.ClosePath()
	c.z.Draw(c.img, r, image.NewUniform(col), image.Point{})
}

// dots scatters n small translucent dots over the canvas.
func (c *canvas) dots(s *stream, n int, rMin, rMax float64, palette []color.NRGBA, aMin, aMax int) {
	b := c.img.Bounds()
	var p path
	for i := 0; i < n; i++ {
		p = p[:0]
		ctr := vec{s.rangeF(0, float64(b.Dx())), s.rangeF(0, float64(b.Dy()))}
		p.extend(circlePath(ctr, s.rangeF(rMin, rMax)))
		col := palette[s.intn(len(palette))]
		col.A = uint8(s.between(aMin, aMax))
		c.fill(p, col)
	}
}

// squiggle draws a random tapered cubic stroke across the canvas.
func (c *canvas) squiggle(s *stream, yMin, yMax, width float64, col color.NRGBA) {
	w := float64(c.img.Bounds().Dx())
	a := vec{s.rangeF(-20, w*0.2), s.rangeF(yMin, yMax)}
	d := vec{s.rangeF(w*0.8, w+20), s.rangeF(yMin, yMax)}
	b := vec{s.rangeF(w*0.2, w*0.5), s.rangeF(yMin-60, yMax+60)}
	cc := vec{s.rangeF(w*0.5, w*0.8), s.rangeF(yMin-60, yMax+60)}
	if s.intn(2) == 0 {
		a, d = d, a
	}
	for _, p := range strokeCurve(cubicPoints(a, b, cc, d, 64), width) {
		c.fill(p, col)
	}
}

func (c *canvas) encode() ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(64 << 10)
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, quantize(c.img, 256)); err != nil {
		return nil, fmt.Errorf("captcha: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// glyph is a font outline normalised so that its bounding box is centred on
// the origin; size is expressed relative to the digit height.
type glyph struct {
	outline path
	width   float64 // bounding-box width in the same units
}

var (
	glyphOnce sync.Once
	glyphs    map[rune]glyph
	glyphErr  error
)

// glyphRunes lists every rune the renderers may draw.
const glyphRunes = "0123456789+−×=?-x"

// loadGlyphs parses Go Bold once and caches outlines scaled so that the
// digit '8' is exactly 1 unit tall.
func loadGlyphs() (map[rune]glyph, error) {
	glyphOnce.Do(func() {
		f, err := opentype.Parse(gobold.TTF)
		if err != nil {
			glyphErr = fmt.Errorf("captcha: parse font: %w", err)
			return
		}
		var buf sfnt.Buffer
		const ppem = 512
		raw := make(map[rune]path)
		for _, r := range glyphRunes {
			idx, err := f.GlyphIndex(&buf, r)
			if err != nil || idx == 0 {
				continue
			}
			segs, err := f.LoadGlyph(&buf, idx, fixed.I(ppem), nil)
			if err != nil {
				continue
			}
			raw[r] = segmentsPath(segs)
		}
		ref, ok := raw['8']
		if !ok {
			glyphErr = fmt.Errorf("captcha: font lacks digits")
			return
		}
		_, y0, _, y1 := bounds(ref)
		unit := 1 / (y1 - y0)
		glyphs = make(map[rune]glyph, len(raw))
		for r, p := range raw {
			x0, py0, x1, py1 := bounds(p)
			m := affine{unit, 0, -(x0 + x1) / 2 * unit, 0, unit, -(py0 + py1) / 2 * unit}
			glyphs[r] = glyph{outline: p.transformed(m), width: (x1 - x0) * unit}
		}
		// Fall back to ASCII look-alikes if typographic operators are absent.
		for want, alt := range map[rune]rune{'−': '-', '×': 'x'} {
			if _, ok := glyphs[want]; !ok {
				glyphs[want] = glyphs[alt]
			}
		}
	})
	return glyphs, glyphErr
}

func segmentsPath(segs sfnt.Segments) path {
	f := func(p fixed.Point26_6) vec { return vec{float64(p.X) / 64, float64(p.Y) / 64} }
	var p path
	for _, s := range segs {
		switch s.Op {
		case sfnt.SegmentOpMoveTo:
			p.moveTo(f(s.Args[0]))
		case sfnt.SegmentOpLineTo:
			p.lineTo(f(s.Args[0]))
		case sfnt.SegmentOpQuadTo:
			p.quadTo(f(s.Args[0]), f(s.Args[1]))
		case sfnt.SegmentOpCubeTo:
			p.cubeTo(f(s.Args[0]), f(s.Args[1]), f(s.Args[2]))
		}
	}
	return p
}

// bounds returns the control-point bounding box of p.
func bounds(p path) (x0, y0, x1, y1 float64) {
	x0, y0 = math.Inf(1), math.Inf(1)
	x1, y1 = math.Inf(-1), math.Inf(-1)
	for _, s := range p {
		for _, q := range s.p[:s.points()] {
			x0, x1 = math.Min(x0, q.x), math.Max(x1, q.x)
			y0, y1 = math.Min(y0, q.y), math.Max(y1, q.y)
		}
	}
	return
}

// drawGlyph renders g centred at ctr with the given height and rotation,
// surrounded by a halo of radius halo and a soft drop shadow.
func (c *canvas) drawGlyph(g glyph, ctr vec, height, theta, halo float64, fill, haloCol, shadow color.NRGBA) {
	if halo > 0 {
		const taps = 16
		var ring, drop path
		for i := 0; i < taps; i++ {
			o := polar(halo, 2*math.Pi*float64(i)/taps)
			ring.extend(g.outline.transformed(xform(ctr.x+o.x, ctr.y+o.y, theta, height)))
			if shadow.A > 0 {
				drop.extend(g.outline.transformed(xform(ctr.x+o.x, ctr.y+o.y+halo*0.9, theta, height)))
			}
		}
		c.fill(drop, shadow)
		c.fill(ring, haloCol)
	}
	c.fill(g.outline.transformed(xform(ctr.x, ctr.y, theta, height)), fill)
}

// hsl converts hue (degrees), saturation and lightness to an opaque colour.
func hsl(h, s, l float64) color.NRGBA {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to := func(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, v+m)) * 255)) }
	return color.NRGBA{to(r), to(g), to(b), 0xff}
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}
