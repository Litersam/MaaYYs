//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeBatch3Party(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, tc := range []struct {
			name, entry, role, reason string
			want                      runstate.Status
		}{
			{"missing name never sends or battles", "组队管控-定向勾选", "captain", "party_teammate_not_found", runstate.Failed},
			{"three player room with one empty seat", "组队管控-等待满员", "captain", "party_state_unconfirmed", runstate.Failed},
			{"member disband goes straight home", "组队管控-加入房间", "member", "party_room_disbanded", runstate.Deferred},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newGuildBatch2Replay(t, res, tasker)
				room := true
				f.node("组队管控-初始化")["next"] = []string{tc.entry}
				f.node("组队管控-初始化")["custom_action_param"] = map[string]any{
					"role": tc.role, "flow": "party", "teammate_names": "同伴甲,同伴乙",
					"captain_names": "队长甲", "limit_count": 3,
				}
				f.recognize("组队管控-房间", func() bool { return room })
				f.recognize("组队管控-庭院", func() bool { return !room })
				f.recognize("组队管控-解散证据", func() bool { return tc.role == "member" && !room })
				f.recognize("组队管控-邀请确认钮", func() bool { return tc.entry == "组队管控-定向勾选" })
				f.recognize("组队管控-关闭邀请", func() bool { return false })
				f.recognize("组队管控-确认退出房间", func() bool { return false })
				f.recognize("组队管控-胜利", func() bool { return false })
				f.recognize("组队管控-战败", func() bool { return false })
				f.recognize("组队管控-准备战斗", func() bool { return false })
				f.recognize("组队管控-挑战文字", func() bool { return true })
				f.recognize("通用弹窗-御魂溢出-知道了", func() bool { return false })
				f.act("组队管控-退出房间", func() { room = false })
				f.act("组队管控-发送邀请", func() { t.Error("sent to an incomplete recipient list") })
				f.act("组队管控-队长开战", func() { t.Error("started without a full room") })
				f.shorten("组队管控-等待满员", "组队管控-归庭", "组队管控-登记终态", "组队管控-已归庭",
					"组队管控-归庭确认", "组队管控-缺搭档", "组队管控-失败归庭")
				didClean := false
				if tc.role == "member" {
					key := t.Name() + "/own-bonus"
					if err := res.RegisterCustomAction(key, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
						room = false
						return ctx.SetAnchor("通用加成-本轮已开启-drop_soul", "通用加成-启用色") == nil
					})); err != nil {
						t.Fatal(err)
					}
					n := f.node("组队管控-加入房间")
					n["action"], n["custom_action"] = "Custom", key
					f.recognize("组队管控-关闭本轮加成", func() bool { return !room })
					f.act("组队管控-关闭本轮加成", func() { didClean = true })
					f.recognize("组队管控-归位收起", func() bool { return didClean })
					f.act("组队管控-归位收起", func() {})
					f.recognize("通用加成-确认收起", func() bool { return didClean })
					f.shorten("组队管控-战斗等待", "组队管控-归位本轮加成", "组队管控-收起后归庭")
				}
				if tc.entry == "组队管控-等待满员" {
					probe := f.t.Name() + "/three-slot-guard"
					if err := res.RegisterCustomRecognition(probe, nativePartyFullSlotProbe{threePlayerMissingSeat()}); err != nil {
						t.Fatal(err)
					}
					f.node("组队管控-满员证据")["custom_recognition"] = probe
				}
				f.run("组队管控-开局", tc.want, tc.reason)
				if room {
					t.Error("terminal did not verify courtyard")
				}
				if tc.role == "member" && !didClean {
					t.Error("member did not close a bonus owned by this task")
				}
			})
		}
	})
}

type nativePartyFullSlotProbe struct{ img image.Image }

func (p nativePartyFullSlotProbe) Run(ctx *maa.Context, _ *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	const node = "fixture-batch3-actual-glyph"
	data := map[string]any{node: map[string]any{"recognition": "Custom", "custom_recognition": "PartyGlyph",
		"custom_recognition_param": map[string]any{"kind": "full", "box": []int{1170, 580, 97, 110}}}}
	d, err := ctx.RunRecognition(node, p.img, data)
	if err != nil || d == nil || !d.Hit {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: d.Box}, true
}

func threePlayerMissingSeat() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	contrast := image.NewUniform(color.RGBA{175, 54, 90, 255})
	draw.Draw(img, image.Rect(990, 205, 1085, 313), contrast, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(590, 220, 705, 328), contrast, image.Point{}, draw.Src)
	white := image.NewUniform(color.White)
	draw.Draw(img, image.Rect(622, 257, 681, 265), white, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(648, 231, 655, 291), white, image.Point{}, draw.Src)
	return img
}

func TestNativeBatch3OCRExpressions(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("MaaFramework %s", maa.Version())
		var patterns []string
		seen := map[string]bool{}
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				for key, val := range v {
					if key == "expected" {
						add := func(s string) {
							if !seen[s] {
								seen[s], patterns = true, append(patterns, s)
							}
						}
						switch p := val.(type) {
						case string:
							add(p)
						case []any:
							for _, raw := range p {
								if text, ok := raw.(string); ok {
									add(text)
								}
							}
						}
					}
					walk(val)
				}
			case []any:
				for _, val := range v {
					walk(val)
				}
			}
		}
		for _, f := range []string{
			"resource_pack/base/pipeline/战斗/组队管控.json", "resource_pack/base/pipeline/战斗/组队刷本.json",
			"resource_pack/base/pipeline/战斗/觉醒.json", "resource_pack/base/pipeline/战斗/同心之兰.json",
			"resource_pack/base/pipeline/战斗/契灵结契.json", "tasks/standalone/组队刷本.json", "tasks/standalone/觉醒.json",
			"tasks/standalone/同心之兰.json", "tasks/standalone/契灵结契.json",
		} {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
			if err != nil {
				t.Fatal(err)
			}
			var def any
			if err := json.Unmarshal(data, &def); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			walk(def)
		}
		slices.Sort(patterns)
		if len(patterns) == 0 {
			t.Fatal("no expressions found to exercise")
		}
		const node = "fixture-batch3-regex"
		img := syntheticGhostFrame(t)
		var misses []string
		if err := res.RegisterCustomAction(node, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			for _, expr := range patterns {
				// Pass each pattern as a single expression so a syntax error
				// invalidates its known-matching alternative too.
				joined := "(?:" + expr + ")|^涂壁$"
				d, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{Expected: []string{joined}}, img)
				if err != nil || d == nil || !d.Hit {
					misses = append(misses, fmt.Sprintf("%q (%v)", expr, err))
				}
			}
			return len(misses) == 0
		})); err != nil {
			t.Fatal(err)
		}
		job := tasker.PostTask(node, map[string]any{node: map[string]any{"action": "Custom", "custom_action": node}}).Wait()
		if !job.Success() {
			t.Fatalf("native OCR pattern misses: %v", misses)
		}
		t.Logf("%d pipeline/option expressions ran through native OCR", len(patterns))
	})
}
