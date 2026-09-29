package captcha

import (
	"image/color"
	"math"
	"strconv"
	"strings"
)

type shape int

const (
	shapeCircle shape = iota
	shapeTriangle
	shapeSquare
	shapeStar
	shapeHeart
	shapeDiamond
	shapeHexagon
	shapeCount
)

// shapeOutline returns the unit outline of sh (fits in a radius-1 circle,
// visually centred on the origin) and where its label should be centred.
func shapeOutline(sh shape) (path, vec) {
	switch sh {
	case shapeCircle:
		return circlePath(vec{}, 0.9), vec{}
	case shapeTriangle:
		pts := []vec{polar(1.12, -math.Pi/2), polar(1.12, math.Pi/6), polar(1.12, 5*math.Pi/6)}
		for i := range pts {
			pts[i].y += 0.14
		}
		return roundedPolygon(pts, 0.2), vec{0, 0.2}
	case shapeSquare:
		return roundedPolygon([]vec{{-0.78, -0.78}, {0.78, -0.78}, {0.78, 0.78}, {-0.78, 0.78}}, 0.2), vec{}
	case shapeStar:
		pts := make([]vec, 10)
		for i := range pts {
			r := 1.08
			if i%2 == 1 {
				r = 0.5
			}
			pts[i] = polar(r, -math.Pi/2+float64(i)*math.Pi/5)
			pts[i].y += 0.06
		}
		return roundedPolygon(pts, 0.08), vec{0, 0.1}
	case shapeHeart:
		var p path
		p.moveTo(vec{0, -0.5})
		p.cubeTo(vec{-0.05, -0.85}, vec{-0.35, -0.98}, vec{-0.58, -0.98})
		p.cubeTo(vec{-0.88, -0.98}, vec{-1.05, -0.72}, vec{-1.05, -0.4})
		p.cubeTo(vec{-1.05, 0.12}, vec{-0.55, 0.5}, vec{0, 0.95})
		p.cubeTo(vec{0.55, 0.5}, vec{1.05, 0.12}, vec{1.05, -0.4})
		p.cubeTo(vec{1.05, -0.72}, vec{0.88, -0.98}, vec{0.58, -0.98})
		p.cubeTo(vec{0.35, -0.98}, vec{0.05, -0.85}, vec{0, -0.5})
		return p, vec{0, -0.12}
	case shapeDiamond:
		return roundedPolygon([]vec{{0, -1.08}, {0.8, 0}, {0, 1.08}, {-0.8, 0}}, 0.16), vec{}
	default: // shapeHexagon
		pts := make([]vec, 6)
		for i := range pts {
			pts[i] = polar(0.98, float64(i)*math.Pi/3)
		}
		return roundedPolygon(pts, 0.14), vec{}
	}
}

// gridPlan is the solvable content of an image challenge.
type gridPlan struct {
	shapes  [gridCells]shape
	digits  [gridCells]int
	targets [imagePicks]int
}

// Each grid uses a small palette of shapes and digits so that neither a
// digit reader nor a shape classifier alone can locate a target: every
// target shares its digit with another cell and its shape with another cell,
// while (shape, digit) pairs stay unique.
const (
	gridShapes = 4
	gridDigits = 4
)

func planGrid(s *stream) gridPlan {
	var p gridPlan
	shapes := make([]shape, shapeCount)
	for i := range shapes {
		shapes[i] = shape(i)
	}
	s.shuffle(len(shapes), func(i, j int) { shapes[i], shapes[j] = shapes[j], shapes[i] })
	digits := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	s.shuffle(len(digits), func(i, j int) { digits[i], digits[j] = digits[j], digits[i] })
	shapes, digits = shapes[:gridShapes], digits[:gridDigits]

	// All (shape, digit) combinations; sample 9 distinct ones per attempt.
	combos := make([][2]int, 0, gridShapes*gridDigits)
	for si := range shapes {
		for di := range digits {
			combos = append(combos, [2]int{si, di})
		}
	}
	for attempt := 0; ; attempt++ {
		s.shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })
		for i := 0; i < gridCells; i++ {
			p.shapes[i], p.digits[i] = shapes[combos[i][0]], digits[combos[i][1]]
		}
		cells := [gridCells]int{0, 1, 2, 3, 4, 5, 6, 7, 8}
		for i := range p.targets {
			j := i + s.intn(gridCells-i)
			cells[i], cells[j] = cells[j], cells[i]
			p.targets[i] = cells[i]
		}
		// The loop is deterministic for a given stream and practically always
		// succeeds within a few attempts; the bound only guards against bugs.
		if p.ambiguous() || attempt >= 64 {
			return p
		}
	}
}

// ambiguous reports whether every target's digit and shape each also occur
// in some other cell.
func (p gridPlan) ambiguous() bool {
	for _, t := range p.targets {
		sameDigit, sameShape := false, false
		for c := 0; c < gridCells; c++ {
			if c == t {
				continue
			}
			sameDigit = sameDigit || p.digits[c] == p.digits[t]
			sameShape = sameShape || p.shapes[c] == p.shapes[t]
		}
		if !sameDigit || !sameShape {
			return false
		}
	}
	return true
}

func gridPrompt(p gridPlan, l lang) string {
	t := &text[l]
	var b strings.Builder
	b.WriteString(t.tapInOrder)
	for i, c := range p.targets {
		if i > 0 {
			b.WriteString(t.arrow)
		}
		b.WriteString(t.shapes[p.shapes[c]])
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(p.digits[c]))
	}
	return b.String()
}

const (
	gridImageSize = 540
	gridMargin    = 15
	gridGap       = 12
	gridCellSize  = (gridImageSize - 2*gridMargin - (GridSize-1)*gridGap) / GridSize
)

// cardHues are decorative pastel hues; colour is never needed to solve.
var cardHues = [...]float64{0, 28, 48, 95, 150, 185, 210, 245, 280, 320}

var noiseInks = []color.NRGBA{
	{70, 82, 140, 255}, {120, 70, 150, 255}, {40, 120, 130, 255},
	{160, 80, 90, 255}, {90, 100, 110, 255},
}

func renderGrid(p gridPlan, s *stream) ([]byte, error) {
	gl, err := loadGlyphs()
	if err != nil {
		return nil, err
	}
	c := newCanvas(gridImageSize, gridImageSize)
	tint := s.rangeF(0, 360)
	c.verticalGradient(hsl(tint, 0.35, 0.965), hsl(tint+30, 0.3, 0.915))
	c.dots(s, 70, 1, 3.2, noiseInks, 18, 40)

	hues := cardHues
	s.shuffle(len(hues), func(i, j int) { hues[i], hues[j] = hues[j], hues[i] })

	const (
		cardRadius = 24.0
		shapeR     = 58.0
		digitH     = 52.0
	)
	type label struct {
		ctr         vec
		height, rot float64
	}
	var labels [gridCells]label
	for i := 0; i < gridCells; i++ {
		row, col := i/GridSize, i%GridSize
		x := float64(gridMargin + col*(gridCellSize+gridGap))
		y := float64(gridMargin + row*(gridCellSize+gridGap))
		w := float64(gridCellSize)
		hue := hues[i]

		// Soft layered shadow, hairline border and pastel card.
		for k := 3; k >= 1; k-- {
			e := float64(k) * 1.6
			c.fill(roundedRect(x-e, y-e+2.5+float64(k), w+2*e, w+2*e, cardRadius+e), color.NRGBA{30, 40, 80, 9})
		}
		c.fill(roundedRect(x, y, w, w, cardRadius), hsl(hue, 0.45, 0.84))
		c.fill(roundedRect(x+1.5, y+1.5, w-3, w-3, cardRadius-1.5), hsl(hue, 0.8, 0.94))
		// Gentle top highlight gives the card some depth.
		c.fill(roundedRect(x+6, y+5, w-12, w*0.42, cardRadius-5), color.NRGBA{255, 255, 255, 70})

		// Shape with jittered rotation, offset and scale.
		outline, lc := shapeOutline(p.shapes[i])
		scale := shapeR * s.rangeF(0.88, 1.05)
		rot := s.rangeF(-0.26, 0.26)
		ctr := vec{x + w/2 + s.rangeF(-9, 9), y + w/2 + s.rangeF(-9, 9)}
		shapeHue := hue + s.rangeF(140, 220)
		body := hsl(shapeHue, 0.62, 0.6)
		rim := hsl(shapeHue, 0.55, 0.45)
		m := xform(ctr.x, ctr.y, rot, scale)
		c.fill(outline.transformed(xform(ctr.x, ctr.y+5, rot, scale)), withAlpha(rim, 55))
		c.fill(outline.transformed(m), rim)
		c.fill(outline.transformed(xform(ctr.x, ctr.y, rot, scale-3.2)), body)

		labels[i] = label{
			ctr:    m.apply(lc).add(vec{s.rangeF(-3, 3), s.rangeF(-3, 3)}),
			height: digitH * s.rangeF(0.92, 1.08),
			rot:    s.rangeF(-0.12, 0.12),
		}
	}

	// Faint noise between the shapes and the digits.
	for i := 0; i < 7; i++ {
		ink := noiseInks[s.intn(len(noiseInks))]
		c.squiggle(s, 0, gridImageSize, s.rangeF(1.6, 2.6), withAlpha(ink, uint8(s.between(45, 70))))
	}
	c.dots(s, 90, 0.8, 2.2, noiseInks, 35, 70)

	for i, lb := range labels {
		g := gl[rune('0'+p.digits[i])]
		c.drawGlyph(g, lb.ctr, lb.height, lb.rot, 3.4,
			color.NRGBA{255, 255, 255, 255}, color.NRGBA{27, 32, 48, 255}, color.NRGBA{27, 32, 48, 70})
	}

	// A couple of very faint strokes over everything.
	for i := 0; i < 2; i++ {
		ink := noiseInks[s.intn(len(noiseInks))]
		c.squiggle(s, 0, gridImageSize, s.rangeF(1.2, 1.8), withAlpha(ink, 40))
	}
	return c.encode()
}

// Geometry describes the image challenge layout in pixels, so user
// interfaces can overlay tap targets that line up with the rendered cells.
type Geometry struct {
	Size   int `json:"size"`
	Margin int `json:"margin"`
	Gap    int `json:"gap"`
	Cell   int `json:"cell"`
	N      int `json:"n"`
}

// GridGeometry returns the image challenge layout.
func GridGeometry() Geometry {
	return Geometry{Size: gridImageSize, Margin: gridMargin, Gap: gridGap, Cell: gridCellSize, N: GridSize}
}
