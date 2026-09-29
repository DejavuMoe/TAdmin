// Package captcha generates join-verification challenges.
//
// Challenges are derived deterministically from secret material: the same
// (kind, seed, generation) always yields the same challenge, so the server
// only needs to persist the seed and the generation counter and never the
// answer itself. A wrong answer is handled by bumping the generation, which
// yields a fresh, unrelated challenge.
package captcha

import (
	"errors"
	"strings"
)

// Kind identifies a challenge type.
type Kind string

// Supported challenge kinds.
const (
	KindImage  Kind = "image"
	KindMath   Kind = "math"
	KindButton Kind = "button"
)

// GridSize is the number of rows and columns of the image challenge grid.
// Cells are indexed 0..GridSize*GridSize-1 in row-major order.
const GridSize = 3

const (
	gridCells     = GridSize * GridSize
	imagePicks    = 3
	mathOptions   = 6
	minSeedLength = 16
)

// Errors returned by Generate.
var (
	ErrUnknownKind       = errors.New("captcha: unknown challenge kind")
	ErrShortSeed         = errors.New("captcha: seed must be at least 16 bytes")
	ErrInvalidGeneration = errors.New("captcha: generation must be >= 1")
)

// ParseKind parses a kind name case-insensitively.
func ParseKind(s string) (Kind, bool) {
	switch k := Kind(strings.ToLower(strings.TrimSpace(s))); k {
	case KindImage, KindMath, KindButton:
		return k, true
	}
	return "", false
}

// Challenge is a rendered challenge ready to be shown to a user.
type Challenge struct {
	Kind    Kind
	Prompt  string   // localized instruction; never reveals cell positions
	Image   []byte   // PNG; nil for KindButton
	Options []string // button labels for math/button; nil for image
	Picks   int      // image: cells to tap in order; math/button: 1

	answer []int
}

// Generate builds the challenge for (kind, seed, generation, lang).
//
// seed must hold at least 16 bytes of secret randomness and generation must
// be at least 1. lang only affects the prompt and labels ("zh*" or empty
// selects zh-CN, anything else selects en); the challenge content and answer
// are identical across languages.
func Generate(kind Kind, seed []byte, generation int, lang string) (*Challenge, error) {
	if len(seed) < minSeedLength {
		return nil, ErrShortSeed
	}
	if generation < 1 {
		return nil, ErrInvalidGeneration
	}
	l := normalizeLang(lang)
	switch kind {
	case KindImage:
		s := newStream(seed, kind, generation)
		p := planGrid(s)
		img, err := renderGrid(p, s)
		if err != nil {
			return nil, err
		}
		return &Challenge{
			Kind:   kind,
			Prompt: gridPrompt(p, l),
			Image:  img,
			Picks:  imagePicks,
			answer: append([]int(nil), p.targets[:]...),
		}, nil
	case KindMath:
		s := newStream(seed, kind, generation)
		p := planMath(s)
		img, err := renderMath(p, s)
		if err != nil {
			return nil, err
		}
		return &Challenge{
			Kind:    kind,
			Prompt:  text[l].mathPrompt,
			Image:   img,
			Options: p.options(),
			Picks:   1,
			answer:  []int{p.correct},
		}, nil
	case KindButton:
		return &Challenge{
			Kind:    kind,
			Prompt:  text[l].buttonPrompt,
			Options: []string{text[l].buttonLabel},
			Picks:   1,
			answer:  []int{0},
		}, nil
	}
	return nil, ErrUnknownKind
}

// Check reports whether answer solves the challenge.
//
// For KindImage the answer must list exactly Picks distinct cell indices
// (0..8, row-major) in the requested order. For KindMath and KindButton it
// must be a single option index.
func (c *Challenge) Check(answer []int) bool {
	if c == nil || len(c.answer) == 0 || len(answer) != len(c.answer) {
		return false
	}
	limit := len(c.Options)
	if c.Kind == KindImage {
		limit = gridCells
	}
	var seen [gridCells]bool
	ok := true
	for i, v := range answer {
		if v < 0 || v >= limit {
			return false
		}
		if v < gridCells {
			if seen[v] {
				return false
			}
			seen[v] = true
		}
		// Keep comparing after a mismatch so the running time does not
		// reveal the length of the correct prefix.
		if v != c.answer[i] {
			ok = false
		}
	}
	return ok
}

type lang int

const (
	langZH lang = iota
	langEN
)

func normalizeLang(s string) lang {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || strings.HasPrefix(s, "zh") {
		return langZH
	}
	return langEN
}

type locale struct {
	tapInOrder   string
	arrow        string
	mathPrompt   string
	buttonPrompt string
	buttonLabel  string
	shapes       [shapeCount]string
}

var text = [...]locale{
	langZH: {
		tapInOrder:   "依次点击：",
		arrow:        " → ",
		mathPrompt:   "请计算图中算式，选择正确答案",
		buttonPrompt: "点击下方按钮完成验证",
		buttonLabel:  "✅ 我不是机器人",
		shapes: [shapeCount]string{
			shapeCircle:   "圆形",
			shapeTriangle: "三角形",
			shapeSquare:   "方形",
			shapeStar:     "星形",
			shapeHeart:    "心形",
			shapeDiamond:  "菱形",
			shapeHexagon:  "六边形",
		},
	},
	langEN: {
		tapInOrder:   "Tap in order: ",
		arrow:        " → ",
		mathPrompt:   "Solve the expression in the image and pick the answer",
		buttonPrompt: "Tap the button below to verify",
		buttonLabel:  "✅ I'm human",
		shapes: [shapeCount]string{
			shapeCircle:   "circle",
			shapeTriangle: "triangle",
			shapeSquare:   "square",
			shapeStar:     "star",
			shapeHeart:    "heart",
			shapeDiamond:  "diamond",
			shapeHexagon:  "hexagon",
		},
	},
}
