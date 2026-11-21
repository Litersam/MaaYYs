package party

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"maa-yys-agent/internal/vision"
)

func historicalSeatMark(t *testing.T, name string) image.Image {
	t.Helper()
	root := os.Getenv("MAAYYS_AWAKE_REFERENCE")
	if root == "" {
		t.Skip("set MAAYYS_AWAKE_REFERENCE to the local historical fixture checkout")
	}
	const revision = "00252fc48e483191c83402c5cf8fd14f924f9043"
	data, err := exec.Command("git", "-C", root, "--no-pager", "show", revision+":tasks/Component/GeneralInvite/gi/"+name).Output()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestRoomSeatEmptyMarks(t *testing.T) {
	for _, tc := range []struct {
		name string
		slot roomSeat
		at   image.Point
		plus bool
	}{
		{"gi_add_1.png", middleRoomSlot, image.Pt(596, 241), false},
		{"gi_add_1.png", middleRoomSlot, image.Pt(602, 244), false},
		{"gi_add_1.png", rightRoomSlot, image.Pt(1009, 229), false},
		{"gi_add_2.png", middleRoomSlot, image.Pt(600, 217), true},
		{"gi_add_2.png", rightRoomSlot, image.Pt(1013, 203), true},
		{"gi_add_sea.png", seaRoomSlot, image.Pt(836, 231), true},
	} {
		crop := historicalSeatMark(t, tc.name)
		img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
		draw.Draw(img, crop.Bounds().Add(tc.at), crop, crop.Bounds().Min, draw.Src)
		_, plus := findPlus(img, tc.slot.emptySearch)
		pendant := hasEmptySeatPendant(img, tc.slot.pendantAt)
		if plus != tc.plus || (!tc.plus && !pendant) {
			t.Errorf("%s at %v: plus=%t pendant=%t; want plus=%t or legacy veto", tc.name, tc.at, plus, pendant, tc.plus)
		}
	}
}

func TestRoomPortraitArtwork(t *testing.T) {
	path := filepath.Join("..", "..", "..", "resource_pack", "base", "image", "非战斗", "式神委派", "式神委派-识别神乐_1.png")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	kagura, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		slot roomSeat
		at   image.Point
	}{
		{middleRoomSlot, image.Pt(597, 228)},
		{rightRoomSlot, image.Pt(1003, 223)},
		{seaRoomSlot, image.Pt(833, 229)},
	} {
		frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
		draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{64, 60, 56, 255}), image.Point{}, draw.Src)
		for y := range 108 {
			for x := range 108 {
				frame.Set(tc.at.X+x, tc.at.Y+y, kagura.At(7+x*145/108, y*145/108))
			}
		}
		if !seatsOccupied(frame, tc.slot) {
			_, plus := findPlus(frame, tc.slot.emptySearch)
			t.Errorf("%v: plus=%t legacy=%t portrait=%t", tc.at, plus,
				hasEmptySeatPendant(frame, tc.slot.pendantAt),
				hasSeatPortrait(frame, vision.ImageRect(tc.slot.portrait)))
		}
		if tc.slot == middleRoomSlot {
			t.Run("empty_mark_over_portrait", func(t *testing.T) {
				crop := historicalSeatMark(t, "gi_add_1.png")
				// Keep the ornament and enough of the underlying portrait to pass
				// the positive check, then assert that the empty vote wins.
				draw.Draw(frame, image.Rect(596, 237, 710, 260), crop, crop.Bounds().Min, draw.Src)
				if !hasSeatPortrait(frame, vision.ImageRect(tc.slot.portrait)) {
					t.Fatal("fixture must preserve positive portrait evidence")
				}
				if !hasEmptySeatPendant(frame, tc.slot.pendantAt) || seatsOccupied(frame, tc.slot) {
					t.Fatal("legacy empty mark must veto otherwise sufficient portrait evidence")
				}
			})
		}
	}
}
