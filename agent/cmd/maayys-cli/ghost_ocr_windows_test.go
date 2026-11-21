//go:build windows

package main

import (
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeGhostNameRecognition(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("native OCR runtime: MaaFramework %s", maa.Version())
		probe := &ghostOCRProbe{frame: syntheticGhostFrame(t)}
		if err := res.RegisterCustomAction("GhostOCRProbe", probe); err != nil {
			t.Fatal(err)
		}
		job := tasker.PostTask("GhostOCRProbe", map[string]any{
			"GhostOCRProbe": map[string]any{"recognition": "DirectHit", "action": "Custom", "custom_action": "GhostOCRProbe"},
		}).Wait()
		if !job.Success() {
			t.Fatalf("actual 地鬼-确认鬼王详情 recognition did not return 涂壁: %v detail=%+v", probe.err, probe.detail)
		}
	})
}

func TestNativeZeroSoulSkipsDefaultModeBeforeNavigation(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		_, tasks, err := loadTaskDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		task, ok := lookupTask(tasks, "自动御魂")
		if !ok {
			t.Fatal("自动御魂 not found in the catalog")
		}
		override := resolveTaskOverride(task, map[string]any{"挑战次数": 0})
		probe := &unexpectedSoulNavigation{}
		if err := res.RegisterCustomAction("UnexpectedSoulNavigation", probe); err != nil {
			t.Fatal(err)
		}
		override["自动御魂-进入副本"] = map[string]any{
			"action": "Custom", "custom_action": "UnexpectedSoulNavigation",
		}
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask(task.Entry, override).Wait()
		if outcome := collector.Outcome(); !job.Success() || probe.entered || outcome.Status != runstate.Skipped {
			t.Fatalf("count=0 must skip before the first navigation node: success=%v entered=%v outcome=%+v",
				job.Success(), probe.entered, outcome)
		}
	})
}

func TestNativeBattleEntryClicksAreBounded(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, _ *maa.Resource, tasker *maa.Tasker) {
		for _, tc := range []struct {
			entry, click, reason string
			unseen               []string
		}{
			{
				"开始自动结界", "结界突破-点击个人标签", "realm_quota_unconfirmed",
				[]string{"结界-关闭意外打开的奖励预览", "结界-关闭意外打开的好友面板", "结界-关闭意外打开的名片", "突破_领取奖励", "结界突破-策略"},
			},
			{
				"阴间之门5", "阴间之门7", "gate_entry_unconfirmed",
				[]string{"阴界之门-已挑战", "阴间之门8", "阴间之门26"},
			},
		} {
			t.Run(tc.reason, func(t *testing.T) {
				override := map[string]any{
					tc.entry: map[string]any{"recognition": "DirectHit", "timeout": 500, "post_delay": 0},
					tc.click: map[string]any{"recognition": "DirectHit", "action": "DoNothing", "post_delay": 0, "rate_limit": 0},
				}
				for _, name := range tc.unseen {
					override[name] = map[string]any{"enabled": false}
				}
				collector, release := runstate.Bind(tasker)
				defer release()
				start := time.Now()
				job := tasker.PostTask(tc.entry, override).Wait()
				if elapsed := time.Since(start); elapsed > 10*time.Second {
					t.Fatalf("unchanged entry hit a second timeout: %s", elapsed)
				}
				detail, err := job.GetDetail()
				if err != nil || detail == nil {
					t.Fatalf("fixture returned no task detail: %v", err)
				}
				n := 0
				for _, node := range detail.NodeDetails {
					if node != nil && node.RunCompleted && node.Name == tc.click {
						n++
					}
				}
				outcome := collector.Outcome()
				reason, _ := outcome.Metrics["battle_reason"].(json.RawMessage)
				if !job.Failure() || outcome.Status != runstate.Failed || string(reason) != `"`+tc.reason+`"` || n != 2 {
					t.Fatalf("want failed after exactly two clicks; got job=%v outcome=%+v clicks=%d", job.Status(), outcome, n)
				}
			})
		}
		t.Run("outside opening hours is deferred", func(t *testing.T) {
			knownPast := map[string]any{"type": "date", "value": "1970-01-01"}
			override := map[string]any{}
			for name, mode := range map[string]string{"阴界之门-在开放时段": "in", "阴界之门-不在开放时段": "out"} {
				override[name] = map[string]any{"custom_recognition_param": map[string]any{
					"date_rule": knownPast, "start": "17:00", "end": "23:00", "mode": mode,
				}}
			}
			collector, release := runstate.Bind(tasker)
			defer release()
			job := tasker.PostTask("阴界之门-时间检查", override).Wait()
			if outcome := collector.Outcome(); !job.Success() || outcome.Status != runstate.Deferred {
				t.Fatalf("valid closed schedule must defer: job=%v outcome=%+v", job.Status(), outcome)
			}
		})
	})
}

type unexpectedSoulNavigation struct{ entered bool }

func (p *unexpectedSoulNavigation) Run(_ *maa.Context, _ *maa.CustomActionArg) bool {
	p.entered = true
	return false
}

func withNativeBattleFixture(t *testing.T, run func(string, *maa.Resource, *maa.Tasker)) {
	t.Helper()
	root := testRepoRoot(t)
	libDir := resolveMaaLibDir(root)
	if _, err := os.Stat(filepath.Join(libDir, "MaaFramework.dll")); err != nil {
		t.Skipf("MaaFramework runtime not present at %s: %v", libDir, err)
	}
	if err := initMaaFramework(root); err != nil {
		t.Fatal(err)
	}
	defer maa.Release()

	res, err := loadAndRegisterResource([]string{filepath.Join(root, "resource_pack", "base")})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	ctrl := newOfflineController(t)
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("connect to hidden in-process fixture window")
	}
	tasker, err := maa.NewTasker()
	if err != nil {
		t.Fatal(err)
	}
	defer tasker.Destroy()
	if err := tasker.BindResource(res); err != nil {
		t.Fatal(err)
	}
	if err := tasker.BindController(ctrl); err != nil {
		t.Fatal(err)
	}
	run(root, res, tasker)
}

type ghostOCRProbe struct {
	frame  image.Image
	detail *maa.RecognitionDetail
	err    error
}

func (p *ghostOCRProbe) Run(ctx *maa.Context, _ *maa.CustomActionArg) bool {
	p.detail, p.err = ctx.RunRecognition("地鬼-确认鬼王详情", p.frame)
	if p.err != nil || p.detail == nil || !p.detail.Hit || p.detail.Results == nil || p.detail.Results.Best == nil {
		return false
	}
	custom, ok := p.detail.Results.Best.AsCustom()
	return ok && custom.Detail == "涂壁"
}

// The synthetic glyphs were rendered in Microsoft YaHei, 32 px. Embedding the
// monochrome mask makes the OCR fixture independent of locally installed fonts.
func syntheticGhostFrame(t *testing.T) image.Image {
	t.Helper()
	const glyphs = `
000000c0000000100000
008000c0000000380000
01e001e000fff01c0000
00f003b000fff01c0000
0078033000c037fff000
003c071800c037fff000
00080e0c00c030808000
00001c0e00c030000000
0000380780fff1c1c000
02007003c0fff0c30000
0780e001f0c030660000
03c3fffff8c007fff800
01e7bfff31fff7fff800
00f200c001bff0180000
002000c001b030180000
000000c001b030180000
000000c0033037fff000
0001ffffe73ff7fff000
0041ffffe63ff0180000
006000c0003030180000
00e000c000000c180000
00c040c100000c000000
00c0e0c380000c000000
00c0c0c180ffffffc000
0181c0c0c0ffffffc000
018380c0e0000c000000
018300c060000c000000
018700c070000c000000
03020fc023fffffff800
01000f8003fffffff800`
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for y, row := range strings.Fields(glyphs) {
		bytes, err := hex.DecodeString(row)
		if err != nil {
			t.Fatal(err)
		}
		for i, b := range bytes {
			for bit := range 8 {
				if b&(0x80>>bit) != 0 {
					img.Set(145+i*8+bit, 52+y, color.Black)
				}
			}
		}
	}
	return img
}
