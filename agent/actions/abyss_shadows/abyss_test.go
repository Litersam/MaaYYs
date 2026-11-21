package abyss_shadows

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestWindowUsesShanghaiNotHostClock(t *testing.T) {
	for _, tc := range []struct{ instant, want string }{
		{"2026-11-19T11:00:00Z", "closed"}, // Thursday in Shanghai.
		{"2026-11-20T10:54:59Z", "before"},
		{"2026-11-20T10:55:00Z", "open"},
		{"2026-11-21T11:00:00Z", "open"},
		{"2026-11-22T13:15:00Z", "open"},
		{"2026-11-22T13:15:01Z", "closed"},
		{"2026-11-22T16:00:00Z", "closed"}, // Already Monday in Shanghai.
	} {
		now, err := time.Parse(time.RFC3339, tc.instant)
		if err != nil {
			t.Fatal(err)
		}
		if got := windowState(now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.instant, got, tc.want)
		}
	}
}

func TestProgressUsesVerifiedTierCounters(t *testing.T) {
	counts := map[tier]uint64{boss: 2, general: 3, elite: 6}
	count := func(t tier) (uint64, error) { return counts[t], nil }
	for state, want := range map[string]bool{
		"boss_full": true, "general_full": false, "elite_full": true, "worked": true, "empty": false, "other_full": false,
	} {
		if got := matchesProgress(state, count); got != want {
			t.Errorf("%s: got %v, want %v", state, got, want)
		}
	}
	broken := func(tier) (uint64, error) { return 0, errors.New("missing counter") }
	if matchesProgress("empty", broken) {
		t.Fatal("a counter error must not mean no work")
	}
}

func TestUnknownTargetsOnlyClearedByTheirTierCapOrVerifiedEnd(t *testing.T) {
	values := map[string]uint64{counterNodes[boss]: 2, unresolvedNodes[general]: 1}
	count := func(node string) (uint64, error) { return values[node], nil }
	if scanResolved(count) {
		t.Fatal("a different tier's cap cannot resolve an unverified target")
	}
	values[counterNodes[general]] = 4
	if !scanResolved(count) {
		t.Fatal("a verified tier cap supersedes unknown slots in that tier")
	}
	values[counterNodes[general]] = 1
	values["狭间暗域-确认战报全歼"] = 1
	if !scanResolved(count) {
		t.Fatal("an authoritative end supersedes ordinary scan uncertainty")
	}
}

func TestParseDamageRejectsPartialOrAmbiguousValues(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
		ok   bool
	}{
		{"30000000", 30000000, true}, {" 20,000，000 ", 20000000, true}, {"0", 0, true},
		{"-1", 0, false}, {"1.2", 0, false}, {"2,00,000", 0, false}, {"3000万", 0, false},
		{"伤害30000000", 0, false}, {"3000 0000", 0, false}, {"１０", 0, false}, {"9223372036854775808", 0, false},
	} {
		n, ok := parseDamage(tc.text)
		if ok != tc.ok || (ok && n != tc.want) {
			t.Errorf("%q = (%d, %v), want (%d, %v)", tc.text, n, ok, tc.want, tc.ok)
		}
	}
}

func TestDamageImageKeepsOnlyConnectedDigitComponents(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	yellow := color.RGBA{254, 192, 65, 255}
	fill := func(x, y, w, h int, c color.Color) {
		r := image.Rect(100+x, 110+y, 100+x+w, 110+y+h)
		draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(2, 2, 19, 3, yellow)  // Wide enough, too short.
	fill(24, 2, 1, 23, yellow) // Tall enough, not enough area.
	fill(30, 2, 2, 9, yellow)  // 18 pixels, diagonally adjoining the lower half.
	fill(32, 11, 2, 9, yellow) // Combined: exactly height 18, area 36.
	fill(45, 3, 4, 22, color.RGBA{0, 255, 0, 255})
	fill(55, 3, 4, 22, color.RGBA{145, 105, 15, 255})
	fill(65, 3, 4, 22, color.RGBA{200, 190, 167, 255})

	got := damageImage(img)
	if got == nil || got.Bounds() != image.Rect(0, 0, 280, 50) {
		t.Fatalf("invalid crop: %v", got)
	}
	for _, tc := range []struct{ x, y, value int }{
		{2, 2, 255}, {24, 2, 255}, {30, 2, 0}, {32, 11, 0},
		{31, 11, 255}, {45, 3, 255}, {55, 3, 255}, {65, 3, 255}, {271, 35, 255},
	} {
		if v := got.GrayAt(tc.x, tc.y).Y; int(v) != tc.value {
			t.Errorf("pixel (%d,%d) = %d, want %d", tc.x, tc.y, v, tc.value)
		}
	}
	if damageImage(image.NewRGBA(image.Rect(0, 0, 379, 200))) != nil {
		t.Fatal("must not OCR a clipped damage counter")
	}
}

func TestMonitorBattleTransitions(t *testing.T) {
	type frame struct {
		label  string
		damage int64
	}
	for _, tc := range []struct {
		name                    string
		frames                  []frame
		want                    string
		captures, reads, clicks int
		activeAtEntry           bool
	}{
		{"settlement before OCR", []frame{{"settlement", 99}}, "natural_settlement_seen", 1, 0, 0, false},
		{"settlement on fresh pre-click frame", []frame{{"battle", 20}, {"battle", 21}, {"settlement", 0}},
			"natural_settlement_seen", 3, 2, 0, false},
		{"misses and low reads reset the streak", []frame{
			{"battle", 22}, {"read_miss", 0}, {"battle", 20}, {"battle", 19}, {"capture_miss", 0},
			{"battle", 22}, {"battle", 21}, {"battle", 0}},
			"retreat_requested", 8, 6, 1, false},
		{"failed pre-click verification requires two new reads", []frame{
			{"battle", 20}, {"battle", 21}, {"unknown", 0}, {"battle", 20}, {"settlement", 0}},
			"natural_settlement_seen", 5, 3, 0, false},
		{"map before any fight is not evidence", []frame{{"map", 0}, {"map", 0}}, "unverified", 3, 0, 0, false},
		{"low damage then two maps", []frame{{"battle", 1}, {"map", 0}, {"map", 0}}, naturalMapReturn, 3, 1, 0, false},
		{"map immediately after active entry", []frame{{"map", 0}, {"map", 0}}, naturalMapReturn, 2, 0, 0, true},
		{"pre-battle map does not count toward the streak", []frame{{"map", 0}, {"battle", 1}, {"map", 0}},
			"unverified", 4, 1, 0, false},
		{"unknown, failed capture and active HUD each break the map streak", []frame{
			{"read_miss", 0}, {"map", 0}, {"unknown", 0}, {"map", 0}, {"capture_miss", 0},
			{"map", 0}, {"battle_map", 1}, {"map", 0}, {"map", 0}},
			naturalMapReturn, 9, 2, 0, false},
		{"pre-click map starts the streak without clicking", []frame{{"battle", 20}, {"battle", 21}, {"map", 0}, {"map", 0}},
			naturalMapReturn, 4, 2, 0, false},
		{"retreat request cannot then consume maps", []frame{{"battle", 20}, {"battle", 21}, {"battle", 0}, {"map", 0}, {"map", 0}},
			"retreat_requested", 3, 2, 1, false},
		{"failed retreat attempt cannot then consume maps", []frame{{"battle", 20}, {"battle", 21}, {"retreat_failure", 0}, {"map", 0}, {"map", 0}},
			"retreat_click_failed", 3, 2, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, reads, clicks := 0, 0, 0
			obs := observation{DamageTarget: 20}
			got := monitorBattle(&obs, tc.activeAtEntry, time.Second, 0, func() bool { return i >= len(tc.frames)+1 },
				func() image.Image {
					i++
					if i > len(tc.frames) || tc.frames[i-1].label == "capture_miss" {
						return nil
					}
					return image.NewGray(image.Rect(i, 0, i+1, 1))
				},
				func(node string, img image.Image) *maa.RecognitionDetail {
					if img == nil {
						return nil
					}
					f := tc.frames[img.Bounds().Min.X-1]
					if (node == settlementNode && f.label == "settlement") ||
						(node == activeNode && (f.label == "battle" || f.label == "read_miss" || f.label == "battle_map" || f.label == "retreat_failure")) ||
						(node == mapNode && (f.label == "map" || f.label == "battle_map")) {
						return &maa.RecognitionDetail{Hit: true}
					}
					return nil
				},
				func(img image.Image) (int64, bool) {
					reads++
					f := tc.frames[img.Bounds().Min.X-1]
					return f.damage, f.label != "read_miss"
				},
				func(*maa.RecognitionDetail) bool { clicks++; return tc.frames[i-1].label != "retreat_failure" })
			if got != tc.want || i != tc.captures || reads != tc.reads || clicks != tc.clicks {
				t.Fatalf("result=%s, captures=%d, reads=%d, clicks=%d, obs=%+v", got, i, reads, clicks, obs)
			}
		})
	}
}
