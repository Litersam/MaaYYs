package party

import (
	"image"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

// The ornate gold titles differ in width and in the placement of their
// heavier strokes. Measure broad bands, not individual glyph pixels: a
// small layout shift or a different background must not change the floor.
func soulTitle(img image.Image, roi maa.Rect) string {
	if img == nil || roi != (maa.Rect{896, 100, 269, 84}) ||
		!image.Rect(896, 100, 1165, 184).In(img.Bounds()) {
		return ""
	}
	ink := func(x, y, width, height int) int {
		total := 0
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				r, g, b, _ := img.At(col, row).RGBA()
				r, g, b = r>>8, g>>8, b>>8
				if r > 185 && g > 158 && b > 96 && r >= b+16 {
					total++
				}
			}
		}
		return total
	}
	upperLeft := ink(925, 115, 52, 21)
	middle := ink(966, 124, 85, 36)
	right := ink(1051, 132, 91, 34)
	// 神罚 has two wide characters; the other titles have four.
	if right <= 45 && upperLeft >= 185 && middle >= 290 && ink(934, 135, 52, 32) >= 270 {
		return "soul_12"
	}
	if right < 330 {
		return ""
	}
	switch {
	case upperLeft <= 75 && middle >= 380 && middle <= 645:
		return "soul_10"
	case upperLeft >= 105 && middle >= 800 && middle <= 1020:
		return "soul_11"
	default:
		return ""
	}
}
