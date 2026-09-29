package userbot

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"rsc.io/qr"
)

const (
	// qrQuietZone is the quiet zone width in modules required by the spec.
	qrQuietZone = 4
	// qrTargetSize is the approximate edge length of rendered QR images.
	qrTargetSize = 512
)

// RenderQR encodes text as a QR code PNG (~512px square, with a 4-module
// quiet zone), suitable for sending as a Telegram photo.
func RenderQR(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, fmt.Errorf("userbot: encode qr: %w", err)
	}
	modules := code.Size + 2*qrQuietZone
	scale := (qrTargetSize + modules/2) / modules
	if scale < 4 {
		scale = 4
	}
	side := modules * scale

	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{color.White, color.Black})
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if !code.Black(x, y) {
				continue
			}
			x0 := (x + qrQuietZone) * scale
			y0 := (y + qrQuietZone) * scale
			for py := y0; py < y0+scale; py++ {
				row := img.Pix[py*img.Stride+x0 : py*img.Stride+x0+scale]
				for i := range row {
					row[i] = 1
				}
			}
		}
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("userbot: encode png: %w", err)
	}
	return buf.Bytes(), nil
}
