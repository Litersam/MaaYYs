package party

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestPartyValidatesRoleAndCount(t *testing.T) {
	for _, tc := range []struct {
		cfg  Config
		want bool
	}{
		{Config{Flow: "party", Role: "captain", TeammateNames: "甲，乙", LimitCount: 5}, true},
		{Config{Flow: "party", Role: "captain", TeammateNames: "甲, 甲", LimitCount: 5}, false},
		{Config{Flow: "party", Role: "captain", TeammateNames: "甲,,乙", LimitCount: 5}, false},
		{Config{Flow: "party", Role: "captain", TeammateNames: "甲,乙", Dungeon: "eternity_sea", LimitCount: 5}, false},
		{Config{Flow: "bond", Role: "member", CaptainNames: "甲,乙", LimitCount: 5}, false},
		{Config{Flow: "awake", Role: "member", CaptainNames: "甲,乙", LimitCount: 5}, true},
		{Config{Flow: "awake", Role: "member", LimitCount: 0}, true},
		{Config{Flow: "awake", Role: "captain", LimitCount: -1}, false},
	} {
		if _, err := newState(tc.cfg); (err == nil) != tc.want {
			t.Errorf("%+v: err=%v, wanted valid=%v", tc.cfg, err, tc.want)
		}
	}
}

func TestInviteMatchesEntireName(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{{"同伴", true}, {"同伴邀请你加入队伍", true}, {"同伴乙", false}, {"前同伴邀请你组队", false}, {"邀请你加入队伍", false}} {
		items := []maa.OCRResult{{Text: tc.text, Box: maa.Rect{10, 20, 99, 21}}}
		if got := invitationSender(items, []string{"同伴"}) != ""; got != tc.want {
			t.Errorf("invitationSender(%q) matched = %t, want %t", tc.text, got, tc.want)
		}
	}
	if box := exactName([]maa.OCRResult{{Text: "同伴A"}, {Text: "前同伴"}}, "同伴"); box != nil {
		t.Error("a substring is not an exact nickname")
	}
	if box := exactName([]maa.OCRResult{{Text: "同伴"}, {Text: "同伴"}}, "同伴"); box != nil {
		t.Error("duplicate OCR candidates are ambiguous")
	}
}

func TestRoomRejectsUnprovenSeats(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	paint := image.NewUniform(color.RGBA{175, 54, 90, 255})
	for _, slot := range []roomSeat{middleRoomSlot, rightRoomSlot, seaRoomSlot} {
		r := image.Rect(slot.portrait.X(), slot.portrait.Y(), slot.portrait.X()+slot.portrait.Width(), slot.portrait.Y()+slot.portrait.Height())
		if seatsOccupied(img, slot) {
			t.Errorf("%v: unknown slot without a plus must not count", r)
		}
		draw.Draw(img, r, paint, image.Point{}, draw.Src)
		if seatsOccupied(img, slot) {
			t.Errorf("%v: a flat color block is not a portrait", r)
		}
	}
}

func TestPartyDoesNotCountASettlementTwice(t *testing.T) {
	s := state{Config: Config{LimitCount: 1}, InRound: true}
	if !s.recordFight() || s.recordFight() || s.Fights != 1 {
		t.Fatalf("unexpected repeated settlement: %+v", s)
	}
}
