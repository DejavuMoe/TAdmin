package captcha

import (
	"cmp"
	"image"
	"image/color"
	"slices"
)

// quantize reduces an opaque RGBA image to at most maxColors colours.
//
// The palette is seeded by population-weighted median cut and refined with
// one Lloyd iteration; every colour then maps to its nearest palette entry.
// The renders consist of flat fills plus anti-aliased edges, so a 256-colour
// palette is visually lossless while shrinking the PNG to roughly a third of
// its true-colour size. The result is deterministic.
func quantize(src *image.RGBA, maxColors int) *image.Paletted {
	b := src.Bounds()
	pixel := func(row []byte, i int) uint32 {
		return uint32(row[i])<<16 | uint32(row[i+1])<<8 | uint32(row[i+2])
	}

	// Histogram, counting runs of identical pixels with one map update.
	hist := make(map[uint32]int, 1<<14)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := src.Pix[src.PixOffset(b.Min.X, y):src.PixOffset(b.Max.X, y)]
		run, prev := 0, pixel(row, 0)
		for i := 0; i < len(row); i += 4 {
			if c := pixel(row, i); c != prev {
				hist[prev] += run
				run, prev = 0, c
			}
			run++
		}
		hist[prev] += run
	}

	type entry struct {
		rgb uint32
		n   int
	}
	entries := make([]entry, 0, len(hist))
	for k, n := range hist {
		entries = append(entries, entry{rgb: k, n: n})
	}
	// Map iteration order is random; sort so the output is deterministic.
	slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(a.rgb, b.rgb) })

	chanOf := func(rgb uint32, ch int) int { return int(rgb>>(16-8*ch)) & 0xff }
	type box struct {
		lo, hi int // entries[lo:hi]
		ch     int // widest channel
		spread int
		weight int
	}
	measure := func(lo, hi int) box {
		bx := box{lo: lo, hi: hi}
		mn, mx := [3]int{255, 255, 255}, [3]int{}
		for _, e := range entries[lo:hi] {
			bx.weight += e.n
			for ch := 0; ch < 3; ch++ {
				v := chanOf(e.rgb, ch)
				mn[ch], mx[ch] = min(mn[ch], v), max(mx[ch], v)
			}
		}
		for ch := 0; ch < 3; ch++ {
			if d := mx[ch] - mn[ch]; d > bx.spread {
				bx.spread, bx.ch = d, ch
			}
		}
		return bx
	}

	boxes := []box{measure(0, len(entries))}
	for len(boxes) < maxColors {
		// Split the box with the largest population-weighted spread.
		best, score := -1, 0
		for i, bx := range boxes {
			if bx.hi-bx.lo < 2 || bx.spread == 0 {
				continue
			}
			if s := bx.spread * bx.weight; s > score {
				best, score = i, s
			}
		}
		if best < 0 {
			break
		}
		bx := boxes[best]
		part := entries[bx.lo:bx.hi]
		// Weighted median of the widest channel via a 256-bin histogram,
		// then an in-place partition: O(n) per split and deterministic.
		var bins [256]int
		lo, hi := 255, 0
		for _, e := range part {
			v := chanOf(e.rgb, bx.ch)
			bins[v] += e.n
			lo, hi = min(lo, v), max(hi, v)
		}
		cut, acc := lo, 0
		for v := lo; v < hi; v++ {
			acc += bins[v]
			cut = v
			if 2*acc >= bx.weight {
				break
			}
		}
		// Entries with channel <= cut go left; both sides are non-empty
		// because lo <= cut < hi.
		mid := 0
		for i := range part {
			if chanOf(part[i].rgb, bx.ch) <= cut {
				part[i], part[mid] = part[mid], part[i]
				mid++
			}
		}
		boxes[best] = measure(bx.lo, bx.lo+mid)
		boxes = append(boxes, measure(bx.lo+mid, bx.hi))
	}

	means := make([][3]int, len(boxes))
	for i, bx := range boxes {
		var sum [3]int
		for _, e := range entries[bx.lo:bx.hi] {
			for ch := 0; ch < 3; ch++ {
				sum[ch] += chanOf(e.rgb, ch) * e.n
			}
		}
		for ch := 0; ch < 3; ch++ {
			means[i][ch] = (sum[ch] + bx.weight/2) / bx.weight
		}
	}

	// Map every colour to its nearest box mean. This avoids stray colours
	// from sparse boxes that span unrelated hues.
	lookup := make(map[uint32]uint8, len(entries))
	for _, e := range entries {
		r, g, bl := chanOf(e.rgb, 0), chanOf(e.rgb, 1), chanOf(e.rgb, 2)
		best, bestD := 0, 1<<30
		for i, m := range means {
			dr, dg, db := r-m[0], g-m[1], bl-m[2]
			if d := 3*dr*dr + 4*dg*dg + 2*db*db; d < bestD {
				best, bestD = i, d
			}
		}
		lookup[e.rgb] = uint8(best)
	}

	pal := make(color.Palette, len(means))
	for i, m := range means {
		pal[i] = color.RGBA{uint8(m[0]), uint8(m[1]), uint8(m[2]), 0xff}
	}
	dst := image.NewPaletted(b, pal)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := src.Pix[src.PixOffset(b.Min.X, y):src.PixOffset(b.Max.X, y)]
		out := dst.Pix[dst.PixOffset(b.Min.X, y):]
		prev := pixel(row, 0)
		idx := lookup[prev]
		for i, j := 0, 0; i < len(row); i, j = i+4, j+1 {
			if c := pixel(row, i); c != prev {
				prev, idx = c, lookup[c]
			}
			out[j] = idx
		}
	}
	return dst
}
