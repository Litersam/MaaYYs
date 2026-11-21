// Package vision contains screenshot geometry and color helpers shared by runners.
package vision

import (
	"image"
	"image/color"
	"image/draw"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func ImageRect(roi maa.Rect) image.Rectangle {
	return image.Rectangle{
		Min: image.Pt(roi.X(), roi.Y()),
		Max: image.Pt(roi.X()+roi.Width(), roi.Y()+roi.Height()),
	}
}

// Crop returns a zero-origin crop clipped to the image bounds, or nil for an empty region.
func Crop(img image.Image, rect image.Rectangle) image.Image {
	if img == nil {
		return nil
	}
	rect = rect.Intersect(img.Bounds())
	if rect.Empty() {
		return nil
	}
	out := image.NewRGBA(image.Rectangle{Max: rect.Size()})
	draw.Draw(out, out.Bounds(), img, rect.Min, draw.Src)
	return out
}

// HSV returns hue in degrees and saturation and value in [0,1].
func HSV(c color.Color) (h, s, v float64) {
	r, g, b, _ := c.RGBA()
	hi, lo := max(r, g, b), min(r, g, b)
	d := float64(hi - lo)
	if d == 0 {
		return 0, 0, float64(hi) / 65535
	}
	switch hi {
	case r:
		h = 60 * (float64(g) - float64(b)) / d
	case g:
		h = 60 * (2 + (float64(b)-float64(r))/d)
	case b:
		h = 60 * (4 + (float64(r)-float64(g))/d)
	}
	if h < 0 {
		h += 360
	}
	return h, d / float64(hi), float64(hi) / 65535
}

func MeanSaturation(img image.Image) float64 {
	if img == nil || img.Bounds().Empty() {
		return 0
	}
	b := img.Bounds()
	var sum float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, s, _ := HSV(img.At(x, y))
			sum += s
		}
	}
	return sum / float64(b.Dx()*b.Dy())
}
