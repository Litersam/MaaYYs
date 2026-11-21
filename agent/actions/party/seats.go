package party

import (
	"image"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type roomSeat struct {
	emptySearch maa.Rect
	portrait    maa.Rect // Interior of the seat only, not its surrounding art.
	pendantAt   image.Point
}

var (
	middleRoomSlot = roomSeat{maa.Rect{569, 196, 186, 161}, maa.Rect{606, 235, 92, 96}, image.Pt(652, 253)}
	rightRoomSlot  = roomSeat{maa.Rect{970, 151, 193, 220}, maa.Rect{1008, 224, 94, 96}, image.Pt(1063, 240)}
	seaRoomSlot    = roomSeat{maa.Rect{836, 231, 100, 100}, maa.Rect{844, 238, 84, 86}, image.Point{}}
)

func seatsOccupied(img image.Image, slots ...roomSeat) bool {
	for _, slot := range slots {
		if img == nil || !vision.ImageRect(slot.emptySearch).In(img.Bounds()) {
			return false
		}
		if _, empty := findPlus(img, slot.emptySearch); empty || hasEmptySeatPendant(img, slot.pendantAt) {
			return false
		}
		if !hasSeatPortrait(img, vision.ImageRect(slot.portrait)) {
			return false
		}
	}
	return len(slots) != 0
}

func findPlus(img image.Image, roi maa.Rect) (maa.Rect, bool) {
	if img == nil {
		return maa.Rect{}, false
	}
	r := vision.ImageRect(roi).Intersect(img.Bounds())
	bright := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r >= 210<<8 && g >= 205<<8 && b >= 201<<8 && max(r, g, b)-min(r, g, b) < 28<<8
	}
	for y := r.Min.Y + 22; y < r.Max.Y-22; y += 2 {
		for x := r.Min.X + 22; x < r.Max.X-22; x += 2 {
			if !bright(x, y) || !bright(x-17, y) || !bright(x+17, y) || !bright(x, y-17) || !bright(x, y+17) ||
				bright(x-12, y-12) || bright(x+12, y+12) {
				continue
			}
			return maa.Rect{x - 20, y - 20, 40, 40}, true
		}
	}
	return maa.Rect{}, false
}

// Some standard rooms show a broken ring with a hanging cream-colored knot
// instead of a plus. Look for that local shape: bright knot and tail, separated
// from two raised shoulders. No pixel template or room background is needed.
func hasEmptySeatPendant(img image.Image, at image.Point) bool {
	if at == (image.Point{}) {
		return false
	}
	warm := func(r, g, b int) bool { return r >= 162 && r >= g && g >= b && r-b <= 54 }
	cream := func(r, g, b int) bool { return r >= 188 && g >= 174 && warm(r, g, b) }
	for y := at.Y - 17; y <= at.Y+17; y++ {
		for x := at.X - 23; x <= at.X+23; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if !cream(int(r>>8), int(g>>8), int(b>>8)) {
				continue
			}
			if colorFraction(img, maa.Rect{x - 4, y - 3, 9, 7}, cream) < .50 ||
				colorFraction(img, maa.Rect{x - 3, y + 4, 7, 7}, cream) < .24 ||
				colorFraction(img, maa.Rect{x - 7, y - 12, 15, 6}, warm) > .10 ||
				colorFraction(img, maa.Rect{x - 17, y + 4, 11, 10}, warm) > .10 ||
				colorFraction(img, maa.Rect{x + 7, y + 4, 11, 10}, warm) > .10 {
				continue
			}
			if colorFraction(img, maa.Rect{x - 32, y - 11, 22, 12}, warm) >= .08 &&
				colorFraction(img, maa.Rect{x + 11, y - 11, 22, 12}, warm) >= .045 {
				return true
			}
		}
	}
	return false
}

// Require detailed foreground over the body of the seat. A flat fill,
// isolated badge, or a band crossing the seat cannot supply distributed
// two-dimensional detail. This is visual presence evidence, not identity.
func hasSeatPortrait(img image.Image, core image.Rectangle) bool {
	const columns, rows = 3, 4
	var across [columns]int
	var down [rows]int
	cells := 0
	for row := range rows {
		for col := range columns {
			cell := image.Rect(
				core.Min.X+core.Dx()*col/columns, core.Min.Y+core.Dy()*row/rows,
				core.Min.X+core.Dx()*(col+1)/columns, core.Min.Y+core.Dy()*(row+1)/rows,
			)
			if hasPortraitDetail(img, cell) {
				across[col]++
				down[row]++
				cells++
			}
		}
	}
	coveredRows := 0
	for _, count := range down {
		if count > 0 {
			coveredRows++
		}
	}
	return cells >= 5 && coveredRows >= 3 && across[1] >= 2 && (across[0] >= 2 || across[2] >= 2)
}

func hasPortraitDetail(img image.Image, cell image.Rectangle) bool {
	rgb := func(x, y int) [3]int {
		r, g, b, _ := img.At(x, y).RGBA()
		return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
	}
	edge := func(a, b [3]int) bool {
		for i := range a {
			if a[i]-b[i] >= 30 || b[i]-a[i] >= 30 {
				return true
			}
		}
		return false
	}
	dx, dy, lit, dark, n := 0, 0, 0, 0, 0
	for y := cell.Min.Y; y+2 < cell.Max.Y; y += 2 {
		for x := cell.Min.X; x+2 < cell.Max.X; x += 2 {
			p := rgb(x, y)
			luma := (p[0] + 2*p[1] + p[2]) / 4
			if luma >= 116 {
				lit++
			}
			if luma <= 91 {
				dark++
			}
			if edge(p, rgb(x+2, y)) {
				dx++
			}
			if edge(p, rgb(x, y+2)) {
				dy++
			}
			n++
		}
	}
	return n > 0 && dx*16 >= n && dy*16 >= n && lit*8 >= n && dark*8 >= n
}
