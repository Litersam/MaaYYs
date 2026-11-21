//go:build windows

package main

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestOfflineBountyAndEncounterReview(t *testing.T) {
	root := testRepoRoot(t)
	if _, err := os.Stat(filepath.Join(resolveMaaLibDir(root), "MaaFramework.dll")); err != nil {
		t.Skipf("MaaFramework runtime not present: %v", err)
	}
	if err := initMaaFramework(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = maa.Release() }()
	res, err := loadAndRegisterResource([]string{filepath.Join(root, "resource_pack", "base")})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	ctrl := newOfflineController(t)
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("synthetic controller connect failed")
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
	post := func(t *testing.T, node string, overrides map[string]any, wantSuccess bool, want runstate.Status) {
		t.Helper()
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask(node, overrides).Wait()
		got := collector.Outcome()
		if job.Success() != wantSuccess || !got.Reported || got.Status != want {
			t.Fatalf("job=%v, outcome=%+v; want success=%v and status=%s", job.Status(), got, wantSuccess, want)
		}
	}
	t.Run("off hours vs malformed schedule", func(t *testing.T) {
		const node = "开始逢魔-检查时间"
		now := time.Now()
		overrides := func(start, end string) map[string]any {
			return map[string]any{node: map[string]any{
				"custom_action_param": map[string]string{"start": start, "end": end},
			}}
		}
		post(t, node, overrides(now.Add(time.Hour).Format("15:04"), now.Add(2*time.Hour).Format("15:04")), true, runstate.Deferred)
		post(t, node, overrides("25:00", "26:00"), false, runstate.Failed)
	})
	t.Run("unknown bounty cannot complete", func(t *testing.T) {
		// Force all positive recognition to miss, not the action nor the retry graph.
		miss := map[string]any{"recognition": "ColorMatch", "method": 4, "lower": []int{92, 180, 8}, "upper": []int{92, 180, 8}, "count": 5000, "roi": []int{20, 20, 40, 40}}
		var retries atomic.Int32
		sink := tasker.OnNodePipelineNodeInContext(func(_ *maa.Context, status maa.EventStatus, detail maa.NodePipelineNodeDetail) {
			if status == maa.EventStatusSucceeded && detail.Name == "悬赏-重试侧栏" {
				retries.Add(1)
			}
		})
		defer tasker.RemoveContextSink(sink)
		post(t, "悬赏-筛选未处理妖怪", map[string]any{
			"悬赏-探索主界面":           map[string]any{"recognition": "DirectHit"},
			"悬赏-侧栏未知线索":          miss,
			"悬赏封印-识别最下方第一个是不是妖怪": miss,
			"悬赏封印-识别最下方第二个是不是妖怪": miss,
			"悬赏封印-识别最下方第三个是不是妖怪": miss,
			"悬赏-确认三张悬赏已结束":       miss,
			"悬赏-回到庭院复核":          map[string]any{"enabled": false},
			"导航-庭院":              map[string]any{"enabled": false},
			"超时_切换御魂失败":          map[string]any{"enabled": false},
			"重启游戏":               map[string]any{"enabled": false},
			"关闭阴阳师":              map[string]any{"enabled": false},
			"启动游戏":               map[string]any{"enabled": false},
			"悬赏-复核完成状态":          map[string]any{"timeout": 25},
			"悬赏-重试侧栏":            map[string]any{"post_delay": 0},
		}, false, runstate.Failed)
		if n := retries.Load(); n != 2 {
			t.Fatalf("unknown frame visited the retry node %d times, want two", n)
		}
	})
	t.Run("task context owns co-battle data", func(t *testing.T) {
		const first, next, later = "悬赏-测试首轮记录", "悬赏-测试首轮查询", "悬赏-测试再次运行"
		const key, value = "bounty.private.co-battle-targets", `{"task_id":1,"names":["涂壁"]}`
		if err := res.RegisterCustomAction("ReviewTaskMemory", offlineAction(func(ctx *maa.Context, arg *maa.CustomActionArg) bool {
			switch arg.CurrentTaskName {
			case first:
				return ctx.SetAnchor(key, value) == nil
			case next:
				got, err := ctx.GetAnchor(key)
				return err == nil && got == value
			case later:
				got, err := ctx.GetAnchor(key)
				return err != nil && got == ""
			}
			return false
		})); err != nil {
			t.Fatal(err)
		}
		fixture := func(nexts []string) map[string]any {
			return map[string]any{
				"recognition": "DirectHit", "action": "Custom", "custom_action": "ReviewTaskMemory", "next": nexts,
			}
		}
		if job := tasker.PostTask(first, map[string]any{first: fixture([]string{next}), next: fixture([]string{})}).Wait(); !job.Success() {
			t.Fatalf("native task could not read its own co-battle data: %v", job.Status())
		}
		if job := tasker.PostTask(later, map[string]any{later: fixture([]string{})}).Wait(); !job.Success() {
			t.Fatalf("next task inherited a previous task's co-battle data: %v", job.Status())
		}
	})
	t.Run("invite panel needs both controls in known positions", func(t *testing.T) {
		const probe = "悬赏-测试协作面板"
		if err := res.RegisterCustomAction("ReviewInviteFrame", offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			for _, tc := range []struct {
				accept, reject image.Rectangle
				want           bool
			}{
				{image.Rect(857, 490, 888, 510), image.Rect(857, 573, 886, 601), true},
				{image.Rect(857, 490, 888, 510), image.Rectangle{}, false},
				{image.Rect(320, 490, 350, 510), image.Rect(857, 573, 886, 601), false},
			} {
				img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
				draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{225, 212, 186, 255}), image.Point{}, draw.Src)
				draw.Draw(img, tc.accept, image.NewUniform(color.RGBA{61, 153, 76, 255}), image.Point{}, draw.Src)
				draw.Draw(img, tc.reject, image.NewUniform(color.RGBA{229, 107, 44, 255}), image.Point{}, draw.Src)
				reco, err := ctx.RunRecognition("悬赏-协作邀请面板", img)
				if err != nil || reco == nil || reco.Hit != tc.want {
					return false
				}
			}
			return true
		})); err != nil {
			t.Fatal(err)
		}
		if job := tasker.PostTask(probe, map[string]any{
			probe: map[string]any{"recognition": "DirectHit", "action": "Custom", "custom_action": "ReviewInviteFrame"},
		}).Wait(); !job.Success() {
			t.Fatal("invite panel accepted the wrong shape/location or missed known anchors")
		}
	})
	t.Run("chest opens only on opt-in and wrong price never reaches purchase", func(t *testing.T) {
		const open, buy, cancel = "逢魔-打开宝箱灯笼", "逢魔-购买50勾玉宝箱蓝票", "逢魔-跳过宝箱订单"
		var state atomic.Int32
		if err := res.RegisterCustomAction("ReviewEncounterChest", offlineAction(func(_ *maa.Context, arg *maa.CustomActionArg) bool {
			switch arg.CurrentTaskName {
			case open:
				return state.CompareAndSwap(0, 1)
			case buy:
				return state.CompareAndSwap(1, 2)
			case cancel:
				return state.CompareAndSwap(1, 3)
			}
			return false
		})); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name       string
			enabled    bool
			exactPrice bool
			finalState int32
		}{
			{"default", false, true, 0},
			{"accepted order", true, true, 2},
			{"unknown or non-50 price", true, false, 3},
		} {
			t.Run(tc.name, func(t *testing.T) {
				state.Store(0)
				safeAction := func() map[string]any {
					return map[string]any{"action": "Custom", "custom_action": "ReviewEncounterChest", "post_delay": 0}
				}
				openNode, buyNode := safeAction(), safeAction()
				if tc.enabled {
					openNode["enabled"], buyNode["enabled"] = true, true
				}
				// Preserve real chest-edge ordering; replace only the later boss path.
				const bossEnd = "逢魔-测试首领入口"
				overrides := map[string]any{
					"自动逢魔9":        map[string]any{"next": []string{"识别集结挑战", "[JumpBack]" + open, bossEnd}},
					bossEnd:        map[string]any{"recognition": "DirectHit"},
					"识别集结挑战":       map[string]any{"enabled": false},
					"逢魔-识别现世地图":    map[string]any{"recognition": "DirectHit"},
					"逢魔-今日仍可挑战":    map[string]any{"recognition": "DirectHit"},
					"逢魔-宝箱灯笼图标":    map[string]any{"recognition": "DirectHit"},
					"逢魔-宝箱蓝票图标":    map[string]any{"recognition": "DirectHit"},
					"逢魔-宝箱勾玉图标":    map[string]any{"recognition": "DirectHit"},
					"逢魔-宝箱奖励后恢复地图": map[string]any{"enabled": false},
					"逢魔-宝箱流程已恢复地图": map[string]any{"recognition": "DirectHit"},
					open:           openNode,
					buy:            buyNode,
					cancel:         safeAction(),
				}
				if tc.exactPrice {
					overrides["逢魔-宝箱五十勾玉"] = map[string]any{"recognition": "DirectHit"}
				} else {
					overrides["逢魔-宝箱五十勾玉"] = map[string]any{"recognition": "OCR", "expected": "^NONMATCHING_SYNTHETIC_PRICE$"}
				}
				job := tasker.PostTask("自动逢魔9", overrides).Wait()
				if !job.Success() || state.Load() != tc.finalState {
					t.Fatalf("chest replay job=%v state=%d, want success and state=%d", job.Status(), state.Load(), tc.finalState)
				}
			})
		}
	})
}
