package party

import (
	"image"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

// The ordinary accept button is a full-size green check on a brown disk;
// the default-accept gear only has a small check in its lower-right corner.
func findInviteAccept(img image.Image, roi maa.Rect, defaultAccept bool) (maa.Rect, bool) {
	bounds := vision.ImageRect(roi).Intersect(img.Bounds())
	if bounds.Empty() {
		return maa.Rect{}, false
	}
	green := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if g >= 95<<8 && int(g)-int(r) >= 34<<8 && int(g)-int(b) >= 25<<8 {
				green.Pix[green.PixOffset(x, y)] = 1
			}
		}
	}
	var queue []image.Point
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			seed := image.Pt(x, y)
			if green.Pix[green.PixOffset(x, y)] != 1 {
				continue
			}
			green.Pix[green.PixOffset(x, y)] = 2
			queue = append(queue[:0], seed)
			mark := image.Rectangle{Min: seed, Max: seed.Add(image.Pt(1, 1))}
			for i := 0; i < len(queue); i++ {
				p := queue[i]
				mark = mark.Union(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))})
				for _, d := range [...]image.Point{{X: 1}, {X: -1}, {Y: 1}, {Y: -1}} {
					n := p.Add(d)
					if n.In(bounds) && green.Pix[green.PixOffset(n.X, n.Y)] == 1 {
						green.Pix[green.PixOffset(n.X, n.Y)] = 2
						queue = append(queue, n)
					}
				}
			}
			if defaultAccept {
				if mark.Dx() < 27 || mark.Dx() > 38 || mark.Dy() < 18 || mark.Dy() > 31 {
					continue
				}
			} else if mark.Dx() < 43 || mark.Dx() > 58 || mark.Dy() < 31 || mark.Dy() > 44 {
				continue
			}
			density := 100 * len(queue) / (mark.Dx() * mark.Dy())
			if density < 33 || density > 57 || !inviteCheckOnDisk(img, green, mark, defaultAccept) {
				continue
			}
			button := image.Rect(mark.Min.X-3, mark.Min.Y-11, mark.Max.X+9, mark.Max.Y+23)
			if defaultAccept {
				button = image.Rect(mark.Min.X-23, mark.Min.Y-32, mark.Max.X+5, mark.Max.Y+11)
			}
			if button.In(bounds) {
				return maa.Rect{button.Min.X, button.Min.Y, button.Dx(), button.Dy()}, true
			}
		}
	}
	return maa.Rect{}, false
}

func inviteCheckOnDisk(img image.Image, mask *image.Gray, mark image.Rectangle, defaultAccept bool) bool {
	at := func(x, y int) image.Point {
		return image.Pt(mark.Min.X+(mark.Dx()-1)*x/100, mark.Min.Y+(mark.Dy()-1)*y/100)
	}
	for _, sample := range [...]image.Point{{X: 15, Y: 50}, {X: 42, Y: 72}, {X: 80, Y: 30}, {X: 45, Y: 89}, {X: 74, Y: 55}} {
		p := at(sample.X, sample.Y)
		if mask.Pix[mask.PixOffset(p.X, p.Y)] == 0 {
			return false
		}
	}
	is := func(x, y int, match func(int, int, int) bool) bool {
		p := at(x, y)
		if !p.In(img.Bounds()) {
			return false
		}
		r, g, b, _ := img.At(p.X, p.Y).RGBA()
		return match(int(r>>8), int(g>>8), int(b>>8))
	}
	brown := func(r, g, b int) bool { return r >= 95 && r <= 170 && r-g >= 12 && g-b >= 12 }
	if !is(2, 95, brown) || !is(95, 70, brown) {
		return false
	}
	if defaultAccept {
		gold := func(r, g, b int) bool { return r >= 185 && g >= 145 && r-g >= 20 && g-b >= 28 }
		for _, sample := range [...]image.Point{{X: -45, Y: -45}, {X: -10, Y: -30}, {X: 75, Y: -33}, {X: 37, Y: 12}} {
			if !is(sample.X, sample.Y, gold) {
				return false
			}
		}
		return true
	}
	return is(37, 12, brown) && is(50, -17, brown) && is(75, 116, brown)
}
