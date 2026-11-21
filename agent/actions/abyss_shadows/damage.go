package abyss_shadows

import (
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"maa-yys-agent/internal/vision"
)

var (
	damageROI     = image.Rect(100, 110, 380, 160)
	damagePattern = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:[,，][0-9]{3})+)$`)
)

func parseDamage(text string) (int64, bool) {
	text = strings.TrimSpace(text)
	if !damagePattern.MatchString(text) {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.Map(func(r rune) rune {
		if r == ',' || r == '，' {
			return -1
		}
		return r
	}, text), 10, 64)
	return n, err == nil
}

// Match OpenCV's 8-bit RGB-to-HSV convention, including its half-degree hue and rounding.
func gold(c color.Color) bool {
	r16, g16, b16, _ := c.RGBA()
	r, g, b := int(r16>>8), int(g16>>8), int(b16>>8)
	v := max(r, g, b)
	d := v - min(r, g, b)
	if v < 155 || d == 0 {
		return false
	}
	const shift = 12
	s := (d*(((255<<shift)+v/2)/v) + (1 << (shift - 1))) >> shift
	if s < 65 {
		return false
	}
	var n int
	switch v {
	case r:
		n = g - b
	case g:
		n = b - r + 2*d
	default:
		n = r - g + 4*d
	}
	h := (n*(((180<<shift)+3*d)/(6*d)) + (1 << (shift - 1))) >> shift
	if h < 0 {
		h += 180
	}
	return h >= 10 && h <= 40
}

func damageImage(frame image.Image) *image.Gray {
	if frame == nil || !damageROI.In(frame.Bounds()) {
		return nil
	}
	crop := vision.Crop(frame, damageROI)
	w, h := crop.Bounds().Dx(), crop.Bounds().Dy()
	mask := make([]bool, w*h)
	for y := range h {
		for x := range w {
			mask[y*w+x] = gold(crop.At(x, y))
		}
	}
	out := image.NewGray(crop.Bounds())
	for i := range out.Pix {
		out.Pix[i] = 255
	}
	var found bool
	var component []int
	for i, on := range mask {
		if !on {
			continue
		}
		mask[i] = false
		component = append(component[:0], i)
		top, bottom := i/w, i/w
		for j := 0; j < len(component); j++ {
			x, y := component[j]%w, component[j]/w
			top, bottom = min(top, y), max(bottom, y)
			for ny := max(0, y-1); ny <= min(h-1, y+1); ny++ {
				for nx := max(0, x-1); nx <= min(w-1, x+1); nx++ {
					k := ny*w + nx
					if mask[k] {
						mask[k] = false
						component = append(component, k)
					}
				}
			}
		}
		if bottom-top+1 >= 18 && len(component) >= 35 {
			found = true
			for _, k := range component {
				out.Pix[k] = 0
			}
		}
	}
	if !found {
		return nil
	}
	return out
}
