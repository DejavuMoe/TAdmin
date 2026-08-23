package verify

import (
	"bytes"
	"image/png"
	"math"
	"testing"
)

func TestChallengeInvariantsAndPNG(t *testing.T) {
	challenge, err := NewChallenge(bytes.Repeat([]byte{0x5a}, 32), 1)
	if err != nil {
		t.Fatalf("NewChallenge() error = %v", err)
	}
	if challenge.targets[0] == challenge.targets[1] {
		t.Fatal("challenge targets must be distinct")
	}
	digits := make(map[int]bool)
	shapeCounts := make(map[shape]int)
	for _, value := range challenge.cells {
		digits[value.Digit] = true
		shapeCounts[value.Shape]++
	}
	if len(digits) != ChallengeCells {
		t.Fatalf("unique digits = %d, want %d", len(digits), ChallengeCells)
	}
	for _, value := range []shape{circle, triangle, square} {
		if shapeCounts[value] != 3 {
			t.Errorf("shape %d count = %d, want 3", value, shapeCounts[value])
		}
	}
	for _, language := range []string{"zh-CN", "en"} {
		if prompt := challenge.Prompt(language); prompt == "" {
			t.Errorf("Prompt(%q) is empty", language)
		}
	}

	contents, err := challenge.PNG()
	if err != nil {
		t.Fatalf("PNG() error = %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	if got := decoded.Bounds().Dx(); got != ImageSize {
		t.Errorf("PNG width = %d, want %d", got, ImageSize)
	}
	if got := decoded.Bounds().Dy(); got != ImageSize {
		t.Errorf("PNG height = %d, want %d", got, ImageSize)
	}
}

func TestChallengeChecksCoordinatesAndOrder(t *testing.T) {
	challenge, err := NewChallenge(bytes.Repeat([]byte{0x31}, 32), 7)
	if err != nil {
		t.Fatal(err)
	}
	correct := targetPoints(challenge)
	got, err := challenge.Check(correct)
	if err != nil || !got {
		t.Fatalf("Check(correct) = %v, %v", got, err)
	}
	reversed := []Point{correct[1], correct[0]}
	got, err = challenge.Check(reversed)
	if err != nil || got {
		t.Fatalf("Check(reversed) = %v, %v", got, err)
	}

	invalid := [][]Point{
		{{X: -0.01, Y: 0.5}, correct[1]},
		{{X: 1, Y: 0.5}, correct[1]},
		{{X: math.NaN(), Y: 0.5}, correct[1]},
		{{X: math.Inf(1), Y: 0.5}, correct[1]},
		{correct[0]},
		{correct[0], correct[1], correct[0]},
	}
	for index, points := range invalid {
		if _, err := challenge.Check(points); err != ErrInvalidCoordinates {
			t.Errorf("Check(invalid[%d]) error = %v, want ErrInvalidCoordinates", index, err)
		}
	}
}

func targetPoints(challenge Challenge) []Point {
	points := make([]Point, len(challenge.targets))
	for index, target := range challenge.targets {
		points[index] = Point{
			X: (float64(target%GridSize) + 0.5) / GridSize,
			Y: (float64(target/GridSize) + 0.5) / GridSize,
		}
	}
	return points
}
