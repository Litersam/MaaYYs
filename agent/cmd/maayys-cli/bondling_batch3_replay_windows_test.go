//go:build windows

package main

import (
	"encoding/json"
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"

	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeBondlingBatch3Navigation(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, start := range []string{"courtyard", "exploration", "realm", "challenge", "pending-confirmation", "unknown"} {
			t.Run(start, func(t *testing.T) {
				page := start
				f := newGuildBatch2Replay(t, res, tasker)
				f.recognize("契灵结契-在地图", func() bool { return page == "realm" || page == "pending-confirmation" })
				f.recognize("契灵结契-召唤确认文案", func() bool { return page == "pending-confirmation" })
				f.recognize("契灵结契-关闭召唤窗", func() bool { return page == "pending-confirmation" })
				f.recognize("契灵结契-庭院点探索", func() bool { return page == "courtyard" })
				f.recognize("契灵结契-点契灵之境", func() bool { return page == "exploration" })
				f.recognize("契灵结契-退出挑战", func() bool { return page == "challenge" })
				f.act("契灵结契-庭院点探索", func() { page = "exploration" })
				f.act("契灵结契-点契灵之境", func() { page = "realm" })
				f.act("契灵结契-退出挑战", func() { page = "realm" })
				f.act("契灵结契-关闭召唤窗", func() { page = "realm" })
				for _, node := range []string{"点地图路牌", "展开探索入口", "低式盘提示"} {
					f.node("契灵结契-" + node)["enabled"] = false
				}
				f.shorten("契灵结契-前置导航")
				job := tasker.PostTask("契灵结契-前置导航", f.override).Wait()
				detail, err := job.GetDetail()
				hits := completedFixtureNodes(t, detail, err)
				wantMap := start != "pending-confirmation" && start != "unknown"
				if job.Success() != (start != "unknown") || hits["契灵结契-在地图"] != boolIntBondling(wantMap) ||
					hits["契灵结契-关闭召唤窗"] != 0 || hits["契灵结契-召唤确认文案"] != boolIntBondling(start == "pending-confirmation") ||
					hits["契灵结契-庭院点探索"] != boolIntBondling(start == "courtyard") ||
					hits["契灵结契-点契灵之境"] != boolIntBondling(start == "courtyard" || start == "exploration") ||
					hits["契灵结契-退出挑战"] != boolIntBondling(start == "challenge") {
					t.Fatalf("navigation %s: %v; hits %v", start, job.Status(), hits)
				}
			})
		}
	})
}

func TestNativeBondlingBatch3UsesSubgraphOutcome(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		f := newGuildBatch2Replay(t, res, tasker)
		f.node("契灵结契-计数")["custom_recognition_param"] = map[string]any{"target_count": 1}
		f.recognize("契灵结契-在地图", func() bool { return true })
		f.recognize("契灵结契-在契忆商店", func() bool { return false })
		f.recognize("契灵结契-点契忆商店", func() bool { return false })
		f.shorten("契灵结契-查看契忆")
		c, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask("契灵结契-开始", f.override).Wait()
		got := c.Outcome()
		reason, _ := got.Metrics["task_reason"].(json.RawMessage)
		if !job.Failure() || !got.Reported || got.Status != runstate.Failed ||
			string(reason) != `"bondling_memory_shop_unconfirmed"` {
			t.Fatalf("subgraph outcomes not propagated: %v, %+v", job.Status(), got)
		}
	})
}

func TestNativeBondlingBatch3Battle(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, result := range []string{"bond", "abandon", "low-discs"} {
			t.Run(result, func(t *testing.T) {
				page := "ready"
				f := newGuildBatch2Replay(t, res, tasker)
				for node, condition := range map[string]func() bool{
					"低式盘提示":         func() bool { return page == "low-discs" },
					"挑战可点击":         func() bool { return page == "ready" },
					"点击挑战":          func() bool { return page == "ready" },
					"战斗中点准备":        func() bool { return page == "prepare" },
					"识别点击屏幕继续后随机等待": func() bool { return page == "result" },
					"退出战斗界面":        func() bool { return page == "result" },
					"结契成功":          func() bool { return page == "bond" },
					"放弃结契":          func() bool { return page == "abandon" },
					"结算已返回":         func() bool { return page == "map" },
				} {
					f.recognize("契灵结契-"+node, condition)
				}
				f.act("契灵结契-点击挑战", func() {
					page = "prepare"
					if result == "low-discs" {
						page = result
					}
				})
				f.act("契灵结契-战斗中点准备", func() { page = "result" })
				f.act("契灵结契-退出战斗界面", func() { page = result })
				f.act("契灵结契-结契成功", func() { page = "map" })
				f.act("契灵结契-放弃结契", func() { page = "map" })
				f.node("契灵结契-先等待")["enabled"] = false
				f.shorten("契灵结契-试一场", "契灵结契-挑战可点击", "契灵结契-点击挑战", "契灵结契-识别点击屏幕继续后随机等待")
				job := tasker.PostTask("契灵结契-试一场", f.override).Wait()
				detail, err := job.GetDetail()
				hits := completedFixtureNodes(t, detail, err)
				if !job.Success() || hits["契灵结契-点击挑战"] != 1 ||
					hits["契灵结契-结契成功"] != boolIntBondling(result == "bond") ||
					hits["契灵结契-放弃结契"] != boolIntBondling(result == "abandon") ||
					hits["契灵结契-结算已返回"] != boolIntBondling(result != "low-discs") {
					t.Fatalf("battle %s: %v; hits %v", result, job.Status(), hits)
				}
			})
		}
	})
}

func TestNativeBondlingBatch3ReferenceOCR(t *testing.T) {
	dir := os.Getenv("MAAYYS_BONDLING_REFERENCE_FIXTURES")
	if dir == "" {
		t.Skip("set MAAYYS_BONDLING_REFERENCE_FIXTURES to the offline reference fixtures directory")
	}
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, c := range []struct {
			file         string
			expand       bool
			hits, misses []string
		}{
			{"bondling_summon_confirm_A_20261011.png", false, []string{"召唤确认文案", "召唤窗石库存", "在召唤数量窗", "地图石库存"}, []string{"单石确认窗", "唤起单次确认"}},
			{"bondling_low_discs_notice_A_20261011.png", false, []string{"低式盘提示", "盘上剩余", "式盘一库存", "式盘二库存", "式盘三库存"}, nil},
			{"bondling_shop_receipt_B_20261011.png", false, []string{"在契忆商店", "契忆库存", "商店式盘一库存", "商店式盘二库存", "商店式盘三库存"}, nil},
			{"bondling_entry_holdout.png", true, []string{"点地图路牌"}, nil},
		} {
			t.Run(c.file, func(t *testing.T) {
				file, err := os.Open(filepath.Join(dir, c.file))
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				frame, _, err := image.Decode(file)
				if err != nil {
					t.Fatal(err)
				}
				if c.expand {
					full := image.NewRGBA(image.Rect(0, 0, 1280, 720))
					draw.Draw(full, image.Rect(510, 220, 650, 400), frame, frame.Bounds().Min, draw.Src)
					frame = full
				}
				name := t.Name() + "/probe"
				if err := res.RegisterCustomAction(name, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
					for i, group := range [][]string{c.hits, c.misses} {
						for _, node := range group {
							d, err := ctx.RunRecognition("契灵结契-"+node, frame)
							if hit := d != nil && d.Hit; err != nil || hit != (i == 0) {
								t.Errorf("%s: expected hit=%v, got hit=%v text=%v err=%v", node, i == 0, hit, ocrtext.Texts(d), err)
							}
						}
					}
					return true
				})); err != nil {
					t.Fatal(err)
				}
				f := newGuildBatch2Replay(t, res, tasker)
				if c.expand {
					f.recognize("契灵结契-探索地图", func() bool { return true })
				}
				const probe = "Batch3BondlingOCRFixture"
				f.override[probe] = map[string]any{"action": "Custom", "custom_action": name}
				if !tasker.PostTask(probe, f.override).Wait().Success() {
					t.Fatal("reference recognition failed")
				}
			})
		}
	})
}

func boolIntBondling(v bool) int {
	if v {
		return 1
	}
	return 0
}
