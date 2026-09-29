package captcha

import (
	"image/color"
	"strconv"
)

type mathOp int

const (
	opAdd mathOp = iota
	opSub
	opMul
)

func (o mathOp) symbol() rune {
	switch o {
	case opSub:
		return '−'
	case opMul:
		return '×'
	}
	return '+'
}

// mathPlan is the solvable content of an arithmetic challenge.
type mathPlan struct {
	a, b    int
	op      mathOp
	values  [mathOptions]int
	correct int
}

func (p mathPlan) result() int {
	switch p.op {
	case opSub:
		return p.a - p.b
	case opMul:
		return p.a * p.b
	}
	return p.a + p.b
}

func (p mathPlan) options() []string {
	out := make([]string, len(p.values))
	for i, v := range p.values {
		out[i] = strconv.Itoa(v)
	}
	return out
}

func planMath(s *stream) mathPlan {
	var p mathPlan
	p.op = mathOp(s.intn(3))
	switch p.op {
	case opAdd:
		p.a, p.b = s.between(2, 49), s.between(2, 49)
	case opSub:
		p.a = s.between(8, 60)
		p.b = s.between(1, p.a)
	case opMul:
		p.a, p.b = s.between(2, 9), s.between(2, 9)
	}
	ans := p.result()

	// Near-miss distractors: off-by-small, off-by-ten and wrong-operator.
	cands := []int{ans + 1, ans - 1, ans + 2, ans - 2, ans + 10, ans - 10, ans + 3, ans - 3}
	switch p.op {
	case opAdd:
		cands = append(cands, p.a*p.b, abs(p.a-p.b))
	case opSub:
		cands = append(cands, p.a+p.b)
	case opMul:
		cands = append(cands, p.a+p.b, (p.a+1)*p.b, p.a*(p.b-1))
	}
	used := map[int]bool{ans: true}
	pool := cands[:0:0]
	for _, v := range cands {
		if v >= 0 && v <= 99 && !used[v] {
			used[v] = true
			pool = append(pool, v)
		}
	}
	s.shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	distractors := pool[:min(len(pool), mathOptions-1)]
	for len(distractors) < mathOptions-1 {
		v := s.between(max(0, ans-15), min(99, ans+15))
		if !used[v] {
			used[v] = true
			distractors = append(distractors, v)
		}
	}

	p.correct = s.intn(mathOptions)
	for i, j := 0, 0; i < mathOptions; i++ {
		if i == p.correct {
			p.values[i] = ans
			continue
		}
		p.values[i] = distractors[j]
		j++
	}
	return p
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

const (
	mathImageW = 540
	mathImageH = 240
)

var mathInks = []color.NRGBA{
	{38, 50, 112, 255}, {92, 42, 128, 255}, {18, 96, 108, 255},
	{136, 36, 68, 255}, {34, 36, 48, 255},
}

func renderMath(p mathPlan, s *stream) ([]byte, error) {
	gl, err := loadGlyphs()
	if err != nil {
		return nil, err
	}
	c := newCanvas(mathImageW, mathImageH)
	tint := s.rangeF(0, 360)
	c.verticalGradient(hsl(tint, 0.5, 0.96), hsl(tint+40, 0.45, 0.9))
	c.dots(s, 60, 1, 3.5, noiseInks, 25, 55)

	// Background strokes pass behind the glyphs.
	for i := 0; i < 3; i++ {
		ink := mathInks[s.intn(len(mathInks))]
		c.squiggle(s, 50, mathImageH-50, s.rangeF(2, 3.2), withAlpha(ink, uint8(s.between(90, 140))))
	}

	type placed struct {
		g      glyph
		h, rot float64
		gap    float64
		dy     float64
	}
	tokens := [][]rune{
		[]rune(strconv.Itoa(p.a)), {p.op.symbol()}, []rune(strconv.Itoa(p.b)), {'='}, {'?'},
	}
	var line []placed
	total := 0.0
	for ti, tok := range tokens {
		for ri, r := range tok {
			digit := r >= '0' && r <= '9'
			pl := placed{g: gl[r], h: s.rangeF(62, 80), dy: s.rangeF(-14, 14)}
			if digit || r == '?' {
				pl.rot = s.rangeF(-0.32, 0.32)
			} else {
				pl.rot = s.rangeF(-0.12, 0.12)
			}
			switch {
			case ti == 0 && ri == 0:
				pl.gap = 0
			case ri == 0:
				pl.gap = s.rangeF(30, 40)
			default:
				pl.gap = s.rangeF(12, 18)
			}
			total += pl.gap + pl.g.width*pl.h
			line = append(line, pl)
		}
	}
	scale := 1.0
	if limit := float64(mathImageW) - 60; total > limit {
		scale = limit / total
	}
	x := (float64(mathImageW) - total*scale) / 2
	for _, pl := range line {
		w := pl.g.width * pl.h * scale
		x += pl.gap * scale
		ctr := vec{x + w/2, mathImageH/2 + pl.dy}
		ink := mathInks[s.intn(len(mathInks))]
		c.drawGlyph(pl.g, ctr, pl.h*scale, pl.rot, 2.4, ink, color.NRGBA{255, 255, 255, 230}, color.NRGBA{})
		x += w
	}

	// Foreground strokes cross the glyphs without hiding them.
	for i := 0; i < 2; i++ {
		ink := mathInks[s.intn(len(mathInks))]
		c.squiggle(s, 70, mathImageH-70, s.rangeF(2, 2.8), withAlpha(ink, uint8(s.between(110, 160))))
	}
	c.squiggle(s, 80, mathImageH-80, 2.2, color.NRGBA{255, 255, 255, 170})
	c.dots(s, 50, 0.8, 2, mathInks, 60, 110)
	return c.encode()
}
