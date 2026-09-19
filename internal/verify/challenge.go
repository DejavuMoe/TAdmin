package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
)

const (
	GridSize       = 3
	ChallengeCells = GridSize * GridSize
	ImageSize      = 600
)

var ErrInvalidCoordinates = errors.New("invalid verification coordinates")

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type shape uint8

const (
	circle shape = iota
	triangle
	square
)

type cell struct {
	Shape shape
	Digit int
	Color int
}

type Challenge struct {
	cells   [ChallengeCells]cell
	targets [2]int
}

func NewChallenge(secret []byte, generation int) (Challenge, error) {
	if len(secret) < 16 || generation < 1 {
		return Challenge{}, errors.New("invalid challenge material")
	}

	random := newChallengeRandom(secret, generation)
	digits := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	shapes := []shape{circle, circle, circle, triangle, triangle, triangle, square, square, square}
	colors := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	shuffle(random, digits)
	shuffle(random, shapes)
	shuffle(random, colors)

	var challenge Challenge
	for index := range challenge.cells {
		challenge.cells[index] = cell{Shape: shapes[index], Digit: digits[index], Color: colors[index]}
	}
	targetOrder := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	shuffle(random, targetOrder)
	challenge.targets = [2]int{targetOrder[0], targetOrder[1]}
	return challenge, nil
}

func (c Challenge) Prompt(language string) string {
	first := c.cells[c.targets[0]]
	second := c.cells[c.targets[1]]
	if language == "en" {
		return "Tap the " + shapeName(first.Shape, language) + " with " + digitName(first.Digit, language) + ", then the " + shapeName(second.Shape, language) + " with " + digitName(second.Digit, language) + "."
	}
	return "先点击包含数字“" + digitName(first.Digit, language) + "”的" + shapeName(first.Shape, language) + "，再点击包含数字“" + digitName(second.Digit, language) + "”的" + shapeName(second.Shape, language) + "。"
}

func (c Challenge) Check(points []Point) (bool, error) {
	if len(points) != 2 {
		return false, ErrInvalidCoordinates
	}
	for _, point := range points {
		if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) ||
			point.X < 0 || point.X >= 1 || point.Y < 0 || point.Y >= 1 {
			return false, ErrInvalidCoordinates
		}
	}
	for index, point := range points {
		cellIndex := int(math.Floor(point.Y*GridSize))*GridSize + int(math.Floor(point.X*GridSize))
		if cellIndex != c.targets[index] {
			return false, nil
		}
	}
	return true, nil
}

func shapeName(value shape, language string) string {
	if language == "en" {
		switch value {
		case circle:
			return "circle"
		case triangle:
			return "triangle"
		default:
			return "square"
		}
	}
	switch value {
	case circle:
		return "圆形"
	case triangle:
		return "三角形"
	default:
		return "正方形"
	}
}

func digitName(value int, language string) string {
	if language == "en" {
		return string(rune('0' + value))
	}
	return string(rune('0' + value))
}

type challengeRandom struct {
	secret     []byte
	generation int
	counter    uint64
	buffer     []byte
}

func newChallengeRandom(secret []byte, generation int) *challengeRandom {
	return &challengeRandom{secret: secret, generation: generation}
}

func (r *challengeRandom) byte() byte {
	if len(r.buffer) == 0 {
		mac := hmac.New(sha256.New, r.secret)
		var input [16]byte
		binary.BigEndian.PutUint64(input[:8], uint64(r.generation))
		binary.BigEndian.PutUint64(input[8:], r.counter)
		_, _ = mac.Write(input[:])
		r.buffer = mac.Sum(nil)
		r.counter++
	}
	value := r.buffer[0]
	r.buffer = r.buffer[1:]
	return value
}

func shuffle[T any](random *challengeRandom, values []T) {
	for index := len(values) - 1; index > 0; index-- {
		other := int(random.byte()) % (index + 1)
		values[index], values[other] = values[other], values[index]
	}
}
