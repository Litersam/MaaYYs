package breakthrough

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGuildFirstPageExhaustedThenSecondPageTarget(t *testing.T) {
	nodes := loadGuildNodes(t)
	const (
		scan     = "寮突18_从上往下打"
		defeated = "寮突-识别第一个目标是否已击破"
		target   = "寮突18_第一个待选目标"
		ready    = "寮突-识别进攻按钮"
	)
	p1 := guildBoardFixture(color.Black)
	p2 := guildBoardFixture(color.White)
	pages := []struct {
		sample    []color.RGBA
		discovery string
	}{
		{p1, defeated},
		{p2, target},
	}
	page := 0
	var path []string
	for node, hops := guildPlan, 0; hops < 16; hops++ {
		path = append(path, node)
		switch node {
		case guildPlan:
			p := policy{mode: guild, limitCount: 6}
			if p.budget(0, 4) != attack {
				t.Fatal("fixture has four chances left; should scan again")
			}
			node = scan // PolicyAction's guild attack branch.
		case guildSwipe:
			if reason := advanceGuildPage(pages[page].sample, func() ([]color.RGBA, string) {
				page++
				return pages[page].sample, ""
			}); reason != "" {
				t.Fatal(reason)
			}
			node = nodes[guildSwipe].Next[0]
		case guildAttack:
			want := []string{
				guildPlan, scan, defeated, "寮突18_检查第二格", "寮突18_检查第三格", "寮突18_检查第四格",
				guildSwipe, guildPlan, scan, target, ready, "[JumpBack]寮突-记录最后一个名字并进攻", guildAttack,
			}
			if !reflect.DeepEqual(path, want) {
				t.Fatalf("guild fixture path = %v, want %v", path, want)
			}
			return
		default:
			hit := func(name string) bool {
				return strings.HasPrefix(name, "寮突18_检查") || name == pages[page].discovery || name == guildSwipe || page == 1 &&
					(name == ready || name == guildAttack || name == "[JumpBack]寮突-记录最后一个名字并进攻")
			}
			next := ""
			for _, candidate := range nodes[node].Next {
				if hit(candidate) {
					next = candidate
					break
				}
			}
			if next == "" {
				t.Fatalf("page %d: stopped at %s before finding its target: %v", page+1, node, path)
			}
			node = next
		}
	}
	t.Fatalf("never reached confirmed attack: %v", path)
}

func TestGuildUnchangedPageIsFailureAfterTwoSwipes(t *testing.T) {
	before := guildBoardFixture(color.Black)
	after := append([]color.RGBA(nil), before...)
	// Isolated render noise isn't a new board.
	after[10] = color.RGBA{255, 255, 255, 255}
	swipes := 0
	reason := advanceGuildPage(before, func() ([]color.RGBA, string) {
		swipes++
		return after, ""
	})
	if reason != "guild_page_no_progress" || swipes != 2 {
		t.Fatalf("reason=%s swipes=%d", reason, swipes)
	}
}

func guildBoardFixture(c color.Color) []color.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Gray{Y: 85}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(423, 141, 739, 265), image.NewUniform(c), image.Point{}, draw.Src)
	return guildPageSample(img)
}

func loadGuildNodes(t *testing.T) map[string]struct{ Next []string } {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "resource_pack", "base", "pipeline", "战斗", "寮突.json"))
	if err != nil {
		t.Fatal(err)
	}
	var nodes map[string]struct{ Next []string }
	if err := json.Unmarshal(raw, &nodes); err != nil {
		t.Fatal(err)
	}
	nodes["[JumpBack]寮突-记录最后一个名字并进攻"] = nodes["寮突-记录最后一个名字并进攻"]
	return nodes
}
