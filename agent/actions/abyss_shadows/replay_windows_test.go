//go:build windows

package abyss_shadows

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/win32"
)

type fixtureRecognition func() bool

func (f fixtureRecognition) Run(_ *maa.Context, _ *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if !f() {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: maa.Rect{10, 10, 40, 40}}, true
}

type fixtureAction func(*maa.CustomActionArg) bool

func (f fixtureAction) Run(_ *maa.Context, a *maa.CustomActionArg) bool { return f(a) }

func replayController(t *testing.T) *maa.Controller {
	t.Helper()
	// The v4 custom controller callback ABI is incompatible with the bundled v5 DLL.
	// Use only a hidden test-owned window and disable all actual input.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	user32 := syscall.NewLazyDLL("user32.dll")
	class, _ := syscall.UTF16PtrFromString("Static")
	hwnd, _, lastErr := user32.NewProc("CreateWindowExW").Call(
		0, uintptr(unsafe.Pointer(class)), 0, 0x80000000,
		0, 0, 1280, 720, 0, 0, 0, 0,
	)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		t.Fatalf("create synthetic window: %v", lastErr)
	}
	t.Cleanup(func() { _, _, _ = user32.NewProc("DestroyWindow").Call(hwnd) })
	ctrl, err := maa.NewWin32Controller(unsafe.Pointer(hwnd), win32.ScreencapGDI, win32.InputNone, win32.InputNone)
	if err != nil {
		t.Fatal(err)
	}
	return ctrl
}

// Replay the production transitions against an in-process controller and
// synthetic recognition states. No external window or device is involved.
func TestNativeAbyssReplay(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lib := os.Getenv("MAAFW_LIB_DIR")
	if lib == "" {
		lib = filepath.Join(root, "maafw")
	}
	if _, err := os.Stat(filepath.Join(lib, "MaaFramework.dll")); err != nil {
		t.Skipf("no offline MaaFramework runtime: %v", err)
	}
	if err := maa.Init(maa.WithLibDir(lib), maa.WithLogDir(filepath.Join(root, "debug", "abyss-replay"))); err != nil {
		t.Fatal(err)
	}
	defer maa.Release()
	res, err := maa.NewResource()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	for _, bundle := range []string{"base", "official"} {
		if !res.PostBundle(filepath.Join(root, "resource_pack", bundle)).Wait().Success() {
			t.Fatalf("load %s resource bundle", bundle)
		}
	}
	if err := res.RegisterCustomRecognition("AbyssProgress", &Progress{}); err != nil {
		t.Fatal(err)
	}
	if err := res.RegisterCustomAction("TaskOutcome", &runstate.TerminalAction{}); err != nil {
		t.Fatal(err)
	}
	ctrl := replayController(t)
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("start in-process fixture controller")
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
	data, err := os.ReadFile(filepath.Join(root, "resource_pack", "base", "pipeline", "战斗", "狭间暗域.json"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &nodes); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		stopAt     string
		wantStatus runstate.Status
	}{
		{"tier priority and caps", "", runstate.Completed},
		{"failed travel never enters battle", "travel", runstate.Failed},
		{"missing retreat confirmation is not settlement", "confirm", runstate.Failed},
		{"missing courtyard confirmation fails", "courtyard", runstate.Failed},
		{"partial work with unknown tiers fails at courtyard", "unresolved", runstate.Failed},
		{"authoritative all defeated", "all_defeated", runstate.Skipped},
		{"authoritative not open", "not_open", runstate.Deferred},
		{"delayed soul replacement handled before returning", "soul", runstate.Skipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const prefix = "狭间暗域-"
			overrides := make(map[string]map[string]any)
			for node := range nodes {
				overrides[node] = map[string]any{"timeout": 140, "rate_limit": 10, "pre_delay": 0, "post_delay": 0}
			}
			edit := func(name string) map[string]any {
				node := prefix + name
				if overrides[node] == nil {
					t.Fatalf("unknown fixture node %s", node)
				}
				return overrides[node]
			}
			rec := func(name string, f fixtureRecognition) {
				key := tc.name + "/" + name + "/rec"
				if err := res.RegisterCustomRecognition(key, f); err != nil {
					t.Fatal(err)
				}
				p := edit(name)
				p["recognition"], p["custom_recognition"] = "Custom", key
			}
			act := func(name string, f fixtureAction) {
				key := tc.name + "/" + name + "/act"
				if err := res.RegisterCustomAction(key, f); err != nil {
					t.Fatal(err)
				}
				p := edit(name)
				p["action"], p["custom_action"] = "Custom", key
			}
			stage, region, tries := "field", 0, 0
			if tc.stopAt == "not_open" {
				stage = "lobby_not_open"
			}
			selected, defeated := false, false
			soulBacks, soulConfirms := 0, 0
			var fought []tier
			var returns []string
			on := func(s string) fixtureRecognition { return func() bool { return stage == s } }
			to := func(s string) fixtureAction {
				return func(*maa.CustomActionArg) bool { stage = s; return true }
			}
			rec("开放", func() bool { return true })
			rec("领域", on("field"))
			rec("可进攻领域", on("field"))
			rec("战报按钮", on("field"))
			rec("领域选择", on("selector"))
			rec("战报面板", on("board"))
			rec("前往按钮", func() bool { return stage == "board" && selected })
			rec("返回箭头", on("field"))
			rec("庭院", on("courtyard"))
			rec("活动未开弹窗", on("lobby_not_open"))
			act("活动未开弹窗", to("courtyard"))
			rec("确认战报全歼", func() bool {
				return (tc.stopAt == "all_defeated" || tc.stopAt == "soul") && stage == "board"
			})
			act("确认战报全歼", to("field"))
			if tc.stopAt == "soul" {
				edit("请求御魂切换")["enabled"] = true
				rec("打开式神录", on("field"))
				act("打开式神录", to("souls"))
				rec("装备御魂", on("souls"))
				act("装备御魂", to("replacement"))
				rec("处理延迟换装确认", on("replacement"))
				act("处理延迟换装确认", func(*maa.CustomActionArg) bool {
					soulConfirms++
					stage = "presets"
					return true
				})
				rec("从式神录返回", func() bool { return stage == "replacement" || stage == "presets" || stage == "souls" })
				act("从式神录返回", func(*maa.CustomActionArg) bool {
					soulBacks++
					switch stage {
					case "presets":
						stage = "souls"
					case "souls":
						stage = "field"
					default:
						return false
					}
					return true
				})
			}
			act("更换领域", to("selector"))
			for i, name := range []string{"神龙", "孔雀", "白藏主", "黑豹"} {
				rec("入口"+name, on("selector"))
				rec("抵达"+name, func() bool { return stage == "field" && region == i })
				act("点"+name, func(*maa.CustomActionArg) bool { stage, region = "field", i; return true })
			}
			for _, name := range []string{"初次打开战报", "续查战报"} {
				act(name, func(*maa.CustomActionArg) bool { stage, selected = "board", false; return true })
			}
			for _, name := range []string{"点击首领", "点击副将一", "点击副将二", "点击精英一", "点击精英二", "点击精英三"} {
				act(name, func(*maa.CustomActionArg) bool {
					// Dragon boss is deliberately unselectable; exhaust only
					// its three probes and then advance to Peacock.
					if name == "点击首领" && region == 0 {
						tries++
					} else if tc.stopAt == "unresolved" && len(fought) > 0 {
						defeated = true
					} else {
						selected = true
					}
					return true
				})
			}
			rec("已击败提示", func() bool { return stage == "board" && defeated })
			act("已击败提示", func(*maa.CustomActionArg) bool { defeated = false; return true })
			act("本域扫描完", to("field"))
			act("确认前往", func(*maa.CustomActionArg) bool {
				if tc.stopAt != "travel" {
					stage = "at_target"
				}
				return true
			})
			rec("挑战选定目标", on("at_target"))
			act("挑战选定目标", to("ready"))
			rec("准备按钮", on("ready"))
			act("点准备", to("battle"))
			rec("战斗中", on("battle"))
			rec("结算标识", on("settlement"))
			for _, name := range []string{"首领限伤", "副将限伤", "精英限伤"} {
				act(name, func(a *maa.CustomActionArg) bool {
					var p battleParams
					if err := json.Unmarshal([]byte(a.CustomActionParam), &p); err != nil || budgets[p.Tier] == 0 {
						t.Errorf("battle parameters: %#v, %v", a, err)
						return false
					}
					fought = append(fought, p.Tier)
					switch {
					case tc.stopAt == "confirm":
					case len(fought) == 1:
						stage = "settlement"
					default:
						stage = "retreat_confirm"
					}
					return true
				})
			}
			rec("确认退出战斗", on("retreat_confirm"))
			act("确认退出战斗", func(*maa.CustomActionArg) bool {
				returns = append(returns, "retreat")
				stage = "field"
				return true
			})
			act("处理结算", func(*maa.CustomActionArg) bool {
				returns = append(returns, "natural")
				stage = "field"
				return true
			})
			act("退领域", func(*maa.CustomActionArg) bool {
				if tc.stopAt != "courtyard" {
					stage = "courtyard"
				}
				return true
			})

			c, release := runstate.Bind(tasker)
			defer release()
			job := tasker.PostTask("狭间暗域", overrides).Wait()
			out := c.Outcome()
			if !out.Reported || out.Status != tc.wantStatus || job.Success() != (tc.wantStatus != runstate.Failed) {
				detail, _ := job.GetDetail()
				var path []string
				if detail != nil {
					for _, n := range detail.NodeDetails {
						if n != nil && n.RunCompleted {
							path = append(path, n.Name)
						}
					}
				}
				t.Fatalf("job=%s outcome=%+v fought=%v stage=%s path=%s", job.Status(), out, fought, stage, strings.Join(path, " → "))
			}
			switch tc.stopAt {
			case "", "courtyard":
				want := []tier{boss, boss, general, general, general, general, elite, elite, elite, elite, elite, elite}
				if !slices.Equal(fought, want) || len(returns) != len(want) || returns[0] != "natural" || tries != 3 {
					t.Fatalf("fights=%v, returns=%v, defeated probes=%d", fought, returns, tries)
				}
			case "confirm":
				if len(fought) != 1 || len(returns) != 0 {
					t.Fatalf("unconfirmed retreat advanced: %v / %v", fought, returns)
				}
			case "unresolved":
				reason, _ := out.Metrics["task_reason"].(json.RawMessage)
				if len(fought) != 1 || stage != "courtyard" || string(reason) != `"abyss_scan_unresolved"` {
					t.Fatalf("fights=%v stage=%s outcome=%+v", fought, stage, out)
				}
			case "soul":
				if len(fought) != 0 || soulConfirms != 1 || soulBacks != 2 {
					t.Fatalf("fights=%v confirmations=%d backs=%d", fought, soulConfirms, soulBacks)
				}
			default:
				if len(fought) != 0 {
					t.Fatalf("unexpected fights: %v", fought)
				}
			}
		})
	}
}
