package verify

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

var challengeColors = []color.RGBA{
	{R: 244, G: 173, B: 181, A: 255},
	{R: 250, G: 202, B: 128, A: 255},
	{R: 241, G: 224, B: 132, A: 255},
	{R: 165, G: 218, B: 171, A: 255},
	{R: 142, G: 214, B: 213, A: 255},
	{R: 143, G: 196, B: 235, A: 255},
	{R: 171, G: 173, B: 235, A: 255},
	{R: 207, G: 165, B: 228, A: 255},
	{R: 224, G: 176, B: 207, A: 255},
}

func (c Challenge) PNG() ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, ImageSize, ImageSize))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 248, G: 250, B: 252, A: 255}}, image.Point{}, draw.Src)

	cellSize := ImageSize / GridSize
	for index, value := range c.cells {
		column := index % GridSize
		row := index / GridSize
		bounds := image.Rect(column*cellSize, row*cellSize, (column+1)*cellSize, (row+1)*cellSize)
		drawCell(canvas, bounds, value)
	}
	gridColor := color.RGBA{R: 197, G: 207, B: 218, A: 255}
	for index := 1; index < GridSize; index++ {
		drawRect(canvas, image.Rect(index*cellSize-2, 0, index*cellSize+2, ImageSize), gridColor)
		drawRect(canvas, image.Rect(0, index*cellSize-2, ImageSize, index*cellSize+2), gridColor)
	}
	drawBorder(canvas, canvas.Bounds(), 4, color.RGBA{R: 134, G: 149, B: 166, A: 255})

	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func drawCell(canvas *image.RGBA, bounds image.Rectangle, value cell) {
	cx, cy := bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2
	fill := challengeColors[value.Color%len(challengeColors)]
	outline := color.RGBA{R: 47, G: 61, B: 76, A: 255}
	switch value.Shape {
	case circle:
		fillCircle(canvas, cx, cy, 69, fill)
		strokeCircle(canvas, cx, cy, 69, 5, outline)
	case triangle:
		points := [3]image.Point{{X: cx, Y: cy - 77}, {X: cx - 78, Y: cy + 64}, {X: cx + 78, Y: cy + 64}}
		fillTriangle(canvas, points, fill)
		strokeLine(canvas, points[0], points[1], 5, outline)
		strokeLine(canvas, points[1], points[2], 5, outline)
		strokeLine(canvas, points[2], points[0], 5, outline)
	case square:
		shapeBounds := image.Rect(cx-66, cy-66, cx+67, cy+67)
		drawRect(canvas, shapeBounds, fill)
		drawBorder(canvas, shapeBounds, 5, outline)
	}
	drawSevenSegment(canvas, cx, cy, value.Digit)
}

var digitSegments = map[int][7]bool{
	1: {false, true, true, false, false, false, false},
	2: {true, true, false, true, true, false, true},
	3: {true, true, true, true, false, false, true},
	4: {false, true, true, false, false, true, true},
	5: {true, false, true, true, false, true, true},
	6: {true, false, true, true, true, true, true},
	7: {true, true, true, false, false, false, false},
	8: {true, true, true, true, true, true, true},
	9: {true, true, true, true, false, true, true},
}

func drawSevenSegment(canvas *image.RGBA, cx, cy, digit int) {
	segments := digitSegments[digit]
	segmentBounds := [7]image.Rectangle{
		image.Rect(cx-27, cy-50, cx+28, cy-40),
		image.Rect(cx+22, cy-45, cx+32, cy),
		image.Rect(cx+22, cy, cx+32, cy+45),
		image.Rect(cx-27, cy+40, cx+28, cy+50),
		image.Rect(cx-32, cy, cx-22, cy+45),
		image.Rect(cx-32, cy-45, cx-22, cy),
		image.Rect(cx-27, cy-5, cx+28, cy+5),
	}
	shadow := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	ink := color.RGBA{R: 23, G: 32, B: 42, A: 255}
	for index, enabled := range segments {
		if !enabled {
			continue
		}
		expanded := segmentBounds[index].Inset(-3)
		drawRect(canvas, expanded, shadow)
		drawRect(canvas, segmentBounds[index], ink)
	}
}

func drawRect(canvas *image.RGBA, bounds image.Rectangle, fill color.Color) {
	draw.Draw(canvas, bounds.Intersect(canvas.Bounds()), &image.Uniform{C: fill}, image.Point{}, draw.Src)
}

func drawBorder(canvas *image.RGBA, bounds image.Rectangle, width int, fill color.Color) {
	drawRect(canvas, image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+width), fill)
	drawRect(canvas, image.Rect(bounds.Min.X, bounds.Max.Y-width, bounds.Max.X, bounds.Max.Y), fill)
	drawRect(canvas, image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+width, bounds.Max.Y), fill)
	drawRect(canvas, image.Rect(bounds.Max.X-width, bounds.Min.Y, bounds.Max.X, bounds.Max.Y), fill)
}

func fillCircle(canvas *image.RGBA, cx, cy, radius int, fill color.Color) {
	radiusSquared := radius * radius
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= radiusSquared && image.Pt(x, y).In(canvas.Bounds()) {
				canvas.Set(x, y, fill)
			}
		}
	}
}

func strokeCircle(canvas *image.RGBA, cx, cy, radius, width int, fill color.Color) {
	outer := radius * radius
	inner := (radius - width) * (radius - width)
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx, dy := x-cx, y-cy
			distance := dx*dx + dy*dy
			if distance <= outer && distance >= inner && image.Pt(x, y).In(canvas.Bounds()) {
				canvas.Set(x, y, fill)
			}
		}
	}
}

func fillTriangle(canvas *image.RGBA, points [3]image.Point, fill color.Color) {
	minY, maxY := points[0].Y, points[1].Y
	for y := minY; y <= maxY; y++ {
		progress := float64(y-minY) / float64(maxY-minY)
		left := int(float64(points[0].X) + progress*float64(points[1].X-points[0].X))
		right := int(float64(points[0].X) + progress*float64(points[2].X-points[0].X))
		for x := left; x <= right; x++ {
			if image.Pt(x, y).In(canvas.Bounds()) {
				canvas.Set(x, y, fill)
			}
		}
	}
}

func strokeLine(canvas *image.RGBA, start, end image.Point, width int, fill color.Color) {
	dx := end.X - start.X
	dy := end.Y - start.Y
	steps := abs(dx)
	if abs(dy) > steps {
		steps = abs(dy)
	}
	if steps == 0 {
		return
	}
	radius := width / 2
	for index := 0; index <= steps; index++ {
		x := start.X + dx*index/steps
		y := start.Y + dy*index/steps
		drawRect(canvas, image.Rect(x-radius, y-radius, x+radius+1, y+radius+1), fill)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
