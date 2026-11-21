package auto_foster

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestRankedFosterExcludesStolenHostAndKeepsFishFallback(t *testing.T) {
	drum := candidate{host{"same", "drum"}, 4, RewardInfo{"勾玉", 67}}
	lesserDrum := candidate{host{"cross", "drum"}, 6, RewardInfo{"勾玉", 59}}
	fish := candidate{host{"cross", "fish"}, 6, RewardInfo{"体力", 151}}
	highYield5 := candidate{host{"cross", "high-yield-5"}, 5, RewardInfo{"勾玉", 99}}
	highYield6 := candidate{host{"cross", "high-yield-6"}, 6, RewardInfo{"勾玉", 100}}
	short := candidate{host{"same", "short"}, 3, RewardInfo{"勾玉", 150}}
	cards := []candidate{short, fish, lesserDrum, drum}
	for _, tc := range []struct {
		name      string
		cards     []candidate
		excluded  map[host]bool
		preferred string
		want      candidate
	}{
		{"eligible 4-star drum beats 6-star fish", cards, nil, "勾玉", drum},
		{"stolen host is tab-specific", cards, map[host]bool{drum.host: true}, "勾玉", lesserDrum},
		{"fish fallback after all eligible drums are gone", cards, map[host]bool{drum.host: true, lesserDrum.host: true}, "勾玉", fish},
		{"legacy stamina preference", cards, nil, "体力", fish},
		{"higher-yield 5-star beats 6-star of same type", []candidate{lesserDrum, highYield5}, nil, "勾玉", highYield5},
		{"higher-yield 6-star beats 5-star of same type", []candidate{highYield5, highYield6}, nil, "勾玉", highYield6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ranked := rankedCandidates(tc.cards, tc.preferred, tc.excluded)
			if len(ranked) == 0 || ranked[0] != tc.want {
				t.Fatalf("ranked %+v; expected %+v first", ranked, tc.want)
			}
			for _, c := range ranked {
				if c == short || tc.excluded[c.host] {
					t.Fatalf("ineligible card ranked: %+v", c)
				}
			}
		})
	}
}

func TestFriendNameRejectsOrangeGuildSubtitle(t *testing.T) {
	rect := image.Rect(12, 24, 48, 40)
	bg := color.RGBA{244, 225, 199, 255}
	for _, tc := range []struct {
		name string
		ink  color.Color
		want bool
	}{
		{"friend", color.RGBA{72, 62, 55, 255}, true},
		{"guild title", color.RGBA{234, 126, 36, 255}, false},
		{"background", bg, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(rect)
			draw.Draw(img, rect, image.NewUniform(bg), image.Point{}, draw.Src)
			draw.Draw(img, image.Rect(12, 25, 30, 31), image.NewUniform(tc.ink), image.Point{}, draw.Src)
			if got := isFriendName(img); got != tc.want {
				t.Fatalf("name=%t, want %t", got, tc.want)
			}
		})
	}
}
