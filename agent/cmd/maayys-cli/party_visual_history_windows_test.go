//go:build windows

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type partyHistoryCrop struct {
	file string
	x, y int
}

func partyHistoryDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MAAYYS_PARTY_HISTORY_CROPS")
	if dir == "" {
		t.Skip("set MAAYYS_PARTY_HISTORY_CROPS to the review's party-lab\\hist directory")
	}
	return dir
}

func partyHistoryFrame(t *testing.T, dir string, pieces ...partyHistoryCrop) *image.RGBA {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(frame, frame.Bounds(), image.Black, image.Point{}, draw.Src)
	for _, piece := range pieces {
		crop, err := loadPNG(filepath.Join(dir, piece.file))
		if err != nil {
			t.Fatal(err)
		}
		pt := image.Pt(piece.x, piece.y)
		draw.Draw(frame, image.Rectangle{Min: pt, Max: pt.Add(crop.Bounds().Size())}, crop, crop.Bounds().Min, draw.Src)
	}
	return frame
}

func runPartyHistoryProbe(t *testing.T, res *maa.Resource, tasker *maa.Tasker, fn func(*maa.Context)) {
	t.Helper()
	name := t.Name() + "/history"
	if err := res.RegisterCustomAction(name, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
		fn(ctx)
		return true
	})); err != nil {
		t.Fatal(err)
	}
	if job := tasker.PostTask(name, map[string]any{
		name: map[string]any{"action": "Custom", "custom_action": name, "post_delay": 0},
	}).Wait(); !job.Success() {
		t.Fatalf("native history probe: %v", job.Status())
	}
}

func TestNativePartyHistoricalInviteColumns(t *testing.T) {
	dir := partyHistoryDir(t)
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("native party runtime: %s", maa.Version())
		normal := partyHistoryCrop{"native-invite-accept.png", 90, 200}
		def := partyHistoryCrop{"gi_i_accept_default.png", 205, 223}
		reject := partyHistoryCrop{"gi_i_reject.png", 12, 226}
		current := filepath.Join(root, "resource_pack", "base", "image", "组队刷本", "邀请-接受_1.png")
		type hits struct{ normal, def, reject bool }
		for _, tc := range []struct {
			name   string
			dir    string
			pieces []partyHistoryCrop
			want   hits
		}{
			{"historical ordinary accept", dir, []partyHistoryCrop{normal}, hits{normal: true}},
			{"historical no invitation", dir, []partyHistoryCrop{{"native-invite-no-invitation.png", 90, 200}}, hits{}},
			{"same card with all buttons", dir, []partyHistoryCrop{normal, def, reject}, hits{true, true, true}},
			{"ordinary accept in next row", dir, []partyHistoryCrop{{"gi_i_accept.png", 113, 335}}, hits{normal: true}},
			{"ordinary accept at y445", dir, []partyHistoryCrop{{"gi_i_accept.png", 113, 445}}, hits{normal: true}},
			{"default accept in next row", dir, []partyHistoryCrop{{def.file, def.x, def.y + 110}}, hits{def: true}},
			{"existing default button", filepath.Dir(current), []partyHistoryCrop{{filepath.Base(current), 201, 221}}, hits{def: true}},
			{"reject alone cannot enter accept routing", dir, []partyHistoryCrop{reject}, hits{reject: true}},
			{"wrong accept columns", dir, []partyHistoryCrop{{def.file, 113, 225}, {"gi_i_accept.png", 205, 225}}, hits{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				frame := partyHistoryFrame(t, tc.dir, tc.pieces...)
				runPartyHistoryProbe(t, res, tasker, func(ctx *maa.Context) {
					for _, check := range []struct {
						node       string
						want       bool
						minX, maxX int
					}{
						{"组队管控-收到邀请", tc.want.normal || tc.want.def, 106, 280},
						{"组队管控-邀请普通接受", tc.want.normal, 106, 186},
						{"组队管控-邀请默认接受", tc.want.def, 197, 281},
						{"组队管控-邀请拒绝", tc.want.reject, 5, 89},
					} {
						d, err := ctx.RunRecognition(check.node, frame)
						hit := d != nil && d.Hit
						if err != nil || hit != check.want {
							t.Errorf("%s: hit=%v want=%v detail=%+v err=%v", check.node, hit, check.want, d, err)
						} else if hit && (d.Box.X() < check.minX || d.Box.X()+d.Box.Width() > check.maxX) {
							t.Errorf("%s: hit outside its column: %v", check.node, d.Box)
						}
					}
				})
			})
		}
	})
}

func TestNativePartyHistoricalSoulTitles(t *testing.T) {
	dir := partyHistoryDir(t)
	const guard = "组队管控-确认御魂层数"
	type titleFrame struct {
		name, floor string
		image.Image
	}
	frames := []titleFrame{{"empty", "", blankPartyFrame()}}
	for n, y := range []int{123, 123, 116, 121} {
		floor := fmt.Sprintf("soul_%d", n+10)
		crop, err := loadPNG(filepath.Join(dir, fmt.Sprintf("o_orochi_check_%d.png", n+10)))
		if err != nil {
			t.Fatal(err)
		}
		for _, off := range []image.Point{{}, {-2, -2}, {2, 2}, {-4, -4}, {-4, 4}, {4, -4}, {4, 4}} {
			for _, shade := range []uint8{0, 169} {
				f := blankPartyFrame()
				draw.Draw(f, f.Bounds(), image.NewUniform(color.Gray{Y: shade}), image.Point{}, draw.Src)
				pastePartyCrop(f, crop, 929+off.X, y+off.Y)
				frames = append(frames, titleFrame{fmt.Sprintf("%s %v bg=%d", floor, off, shade), floor, f})
			}
		}
	}
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		_, tasks, err := loadTaskDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, want string
			overrides  map[string]any
		}{
			{"base", "soul_10", nil},
			{"bond", "soul_10", resolveTaskOverride(tasks["同心之兰"], map[string]any{"teammate_names": "同伴"})},
			{"default", "soul_10", resolveTaskOverride(tasks["组队刷本"], map[string]any{"party_role": "captain", "teammate_names": "同伴"})},
			{"soul_11", "soul_11", resolveTaskOverride(tasks["组队刷本"], map[string]any{"party_role": "captain", "dungeon": "soul_11", "teammate_names": "同伴"})},
			{"soul_12", "soul_12", resolveTaskOverride(tasks["组队刷本"], map[string]any{"party_role": "captain", "dungeon": "soul_12", "teammate_names": "同伴"})},
		} {
			t.Run(tc.name, func(t *testing.T) {
				runPartyHistoryProbe(t, res, tasker, func(ctx *maa.Context) {
					for _, frame := range frames {
						d, err := ctx.RunRecognition(guard, frame.Image, tc.overrides)
						if hit := d != nil && d.Hit; err != nil || hit != (frame.floor == tc.want) {
							t.Errorf("title %s: hit=%v want=%v detail=%+v err=%v", frame.name, hit, frame.floor == tc.want, d, err)
						}
					}
				})
			})
		}
	})
}

func TestNativePartyHistoricalInviteNicknames(t *testing.T) {
	dir := partyHistoryDir(t)
	glyphs := syntheticGhostFrame(t)
	for _, tc := range []struct {
		name, allow string
		autoAccept  bool
		click       image.Rectangle
	}{
		{"exact ordinary accept", "涂壁", false, image.Rect(114, 217, 178, 293)},
		{"exact default accept", "涂壁", true, image.Rect(197, 208, 281, 303)},
		{"whitelist prefix is not exact", "涂", false, image.Rect(5, 208, 89, 303)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := partyHistoryFrame(t, dir,
				partyHistoryCrop{"native-invite-accept.png", 90, 200},
				partyHistoryCrop{"gi_i_accept_default.png", 205, 223},
				partyHistoryCrop{"gi_i_reject.png", 12, 226},
			)
			// Synthetic name on the otherwise missing card header. No OCR
			// or visual guard is stubbed; clicks only reach our recorder.
			draw.Draw(frame, image.Rect(12, 158, 300, 198), image.White, image.Point{}, draw.Src)
			draw.Draw(frame, image.Rect(30, 162, 110, 192), glyphs, image.Pt(145, 52), draw.Src)
			input := &partyFrameController{frame: frame}
			withPartyFrameReplay(t, input, func(_ string, _ *maa.Resource, tasker *maa.Tasker) {
				override := map[string]any{
					"组队管控-初始化": map[string]any{
						"custom_action_param": map[string]any{"role": "member", "captain_names": tc.allow, "auto_accept": tc.autoAccept},
						"next":                []string{"组队管控-接邀判定"},
						"timeout":             1500,
						"post_delay":          0,
					},
				}
				job := tasker.PostTask("组队管控-初始化", override).Wait()
				clicks := input.recorded()
				if !job.Success() || len(clicks) != 1 || !clicks[0].In(tc.click) {
					t.Fatalf("want a single click inside %v, got %v, job=%v", tc.click, clicks, job.Status())
				}
			})
		})
	}
}
