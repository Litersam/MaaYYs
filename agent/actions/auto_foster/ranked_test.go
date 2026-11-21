package auto_foster

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"slices"
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

func TestSelectedTabIsCheckedAfterClick(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for _, tc := range []struct {
		same, cross bool
		want        string
	}{
		{true, false, sameTab}, // A no-op click on cross leaves this frame.
		{false, true, crossTab},
		{false, false, ""}, // Covered/transitional.
		{true, true, ""},   // Ambiguous.
	} {
		draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
		for x, active := range map[int]bool{223: tc.same, 344: tc.cross} {
			if active {
				draw.Draw(img, image.Rect(x, 106, x+10, 115), image.NewUniform(color.RGBA{221, 166, 129, 255}), image.Point{}, draw.Src)
				draw.Draw(img, image.Rect(x, 141, x+10, 148), image.NewUniform(color.RGBA{234, 200, 157, 255}), image.Point{}, draw.Src)
			}
		}
		if got := selectedTab(img); got != tc.want {
			t.Errorf("same=%t cross=%t: %q, want %q", tc.same, tc.cross, got, tc.want)
		}
	}
}

func TestFosterScanAndMode(t *testing.T) {
	low := candidate{host{sameTab, "same name"}, 3, RewardInfo{"勾玉", 180}}
	same := candidate{low.host, 5, RewardInfo{"勾玉", 90}}
	cross := candidate{host{crossTab, "same name"}, 4, RewardInfo{"勾玉", 80}}
	recognitionError := errors.New("recognizer failed")
	for _, tc := range []struct {
		name, params string
		cards        []candidate
		scanErr      error
		wantAnchor   string
		wantSubmits  []host
	}{
		{"original params", `{"FosterTarget":1}`, []candidate{low}, nil, "", nil},
		{"legacy alias", `{"FosterTarget":1,"DirectSelectThreshold":76,"legacy_select_only":true}`, []candidate{low}, nil, "", nil},
		{"legacy false does not opt in", `{"legacy_select_only":false}`, []candidate{low}, nil, "", nil},
		{"ranked rejects readable low stars", `{"mode":"ranked_submit"}`, []candidate{low}, nil, "好友寄养_无候选", nil},
		{"ranked submits across tabs after stolen slot", `{"mode":"ranked_submit"}`, []candidate{same, cross}, nil, "好友寄养_返回后核验", []host{same.host, cross.host}},
		{"ranked reports proven empty", `{"mode":"ranked_submit"}`, nil, nil, "好友寄养_无候选", nil},
		{"ranked propagates scan failure instead of skipping", `{"mode":"ranked_submit"}`, nil, recognitionError, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseParams(tc.params)
			if err != nil {
				t.Fatal(err)
			}
			scan := &fosterWorkflowFixture{cards: tc.cards, scanErr: tc.scanErr}
			anchor, err := selectFoster(scan, p)
			if anchor != tc.wantAnchor || !errors.Is(err, tc.scanErr) || !slices.Equal(scan.submissions, tc.wantSubmits) {
				t.Fatalf("result %q %v submitted %v; want %q %v %v", anchor, err, scan.submissions, tc.wantAnchor, tc.scanErr, tc.wantSubmits)
			}
			if p.Mode == selectOnly && scan.selected != low.host {
				t.Fatalf("legacy did not highlight the low-star host: %v", scan.selected)
			}
		})
	}
	// Exercise the actual built-in legacy entry rather than only literal params.
	path := filepath.Join("..", "..", "..", "resource_pack", "base", "pipeline", "日常", "阴阳寮奖励.json")
	pipeline, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if p := builtInParams(t, pipeline, "阴阳寮奖励领取_结界奖励领取_寄养式神_选择最优寄养"); p.Mode != selectOnly {
		t.Fatalf("built-in legacy entry can submit: %+v", p)
	}
	split, err := os.ReadFile(filepath.Join(filepath.Dir(path), "好友寄养.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p := builtInParams(t, split, "好友寄养_扫描选择"); p.Mode != rankedSubmit {
		t.Fatalf("split flow must opt into its own ranked submit: %+v", p)
	}
}

func builtInParams(t *testing.T, pipeline []byte, node string) FosterParams {
	t.Helper()
	// Many unrelated nodes store different custom params, so decode only this one.
	var nodes map[string]json.RawMessage
	var n struct {
		Params json.RawMessage `json:"custom_action_param"`
	}
	if err := json.Unmarshal(pipeline, &nodes); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(nodes[node], &n); err != nil {
		t.Fatal(err)
	}
	p, err := parseParams(string(n.Params))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRankedFriendPagingDoesNotSkipWhenBudgetRunsOut(t *testing.T) {
	page := 0
	seen := []string{}
	for _, complete := range []bool{false, true} {
		page, seen = 0, nil
		_, err := walkFriendPages(true, func() ([]friendRow, error) {
			rows := []friendRow{{name: fmt.Sprintf("page-%d", page)}}
			if complete && page == 3 {
				rows = append(rows, friendRow{name: "page-3-extra"})
			}
			if complete {
				rows = append(rows, friendRow{name: fmt.Sprintf("page-%d-second", page)}, friendRow{name: fmt.Sprintf("page-%d-third", page)})
			}
			return rows, nil
		}, func() error { page++; return nil }, func(r friendRow) (bool, error) {
			seen = append(seen, r.name)
			return false, nil
		})
		if complete && (err != nil || len(seen) != 10) || !complete && (err == nil || len(seen) != maxFriendPages) {
			t.Fatalf("complete=%t: %v after %v", complete, err, seen)
		}
	}
}

type fosterWorkflowFixture struct {
	cards       []candidate
	scanErr     error
	selected    host
	submissions []host
}

func (f *fosterWorkflowFixture) visitTab(tab string, _ bool, visit func(friendRow) (bool, error)) (bool, error) {
	if f.scanErr != nil {
		return false, f.scanErr
	}
	for _, c := range f.cards {
		if c.tab == tab {
			if ok, err := visit(friendRow{name: c.name, stars: c.stars}); ok || err != nil {
				return ok, err
			}
		}
	}
	return false, nil
}

func (f *fosterWorkflowFixture) readCandidate(tab string, row friendRow, _ bool) (candidate, error) {
	f.selected = host{tab, row.name}
	for _, c := range f.cards {
		if c.host == f.selected {
			return c, nil
		}
	}
	return candidate{}, errors.New("unexpected host")
}

func (f *fosterWorkflowFixture) submit() (bool, error) {
	f.submissions = append(f.submissions, f.selected)
	return f.selected.tab == crossTab, nil
}
