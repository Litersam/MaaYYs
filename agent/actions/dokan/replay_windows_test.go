//go:build windows

package dokan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/win32"

	"maa-yys-agent/internal/runstate"
)

type replayRecognition func(string) bool

func (f replayRecognition) Run(_ *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	return &maa.CustomRecognitionResult{Box: arg.Roi}, f(arg.CurrentTaskName)
}

type replayClick func(string) bool

func (f replayClick) Run(_ *maa.Context, arg *maa.CustomActionArg) bool {
	return f(arg.CurrentTaskName)
}

type replayAction func(*maa.Context, *maa.CustomActionArg) bool

func (f replayAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	return f(ctx, arg)
}

func TestDokanNativeReplay(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lib := os.Getenv("MAAFW_LIB_DIR")
	if lib == "" {
		lib = filepath.Join(root, "maafw")
	}
	if _, err := os.Stat(filepath.Join(lib, "MaaFramework.dll")); err != nil {
		t.Skipf("offline MaaFramework runtime unavailable: %v", err)
	}
	logDir, err := filepath.Abs(fmt.Sprintf(".dokan-replay-%d", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(logDir) })
	if err := maa.Init(maa.WithLibDir(lib), maa.WithLogDir(logDir), maa.WithStdoutLevel(maa.LoggingLevelOff)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = maa.Release() }()

	res, err := maa.NewResource()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	base := filepath.Join(root, "resource_pack", "base")
	raw, err := os.ReadFile(filepath.Join(base, "pipeline", "战斗", "道馆.json"))
	if err != nil {
		t.Fatal(err)
	}
	var nodes map[string]map[string]any
	if err := json.Unmarshal(raw, &nodes); err != nil {
		t.Fatal(err)
	}
	if err := res.RegisterCustomAction("TaskOutcome", &runstate.TerminalAction{}); err != nil {
		t.Fatal(err)
	}
	if !res.PostBundle(base).Wait().Success() {
		t.Fatal("resource bundle did not load")
	}

	ctrl := newDokanReplayController(t)
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("test-owned controller did not connect")
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

	t.Run("real HUD-only recognizer", func(t *testing.T) {
		testDokanHUD(t, root, res, tasker)
	})

	type scenario struct {
		name, page        string
		seconds           int
		want              runstate.Status
		enters, mapEnters int
		exits             int
		breakExit         bool
		expireTrip        bool
		expireBeforeClick bool
		opens             bool
		battle            string
		smallWin          bool
	}
	var mu sync.Mutex
	var tc scenario
	var page string
	var now time.Duration
	var enters, mapEnters, exits, unexpected int
	clock := func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	sleep := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now += d
		if tc.opens && page == "closed" && now >= refreshInterval {
			page = "assemblyMap"
		}
	}
	recognitions := map[string]string{
		"道馆-庭院": "yard", "道馆-阴阳寮": "guild", "道馆-神社": "shrine", "道馆-地图标题": "closed",
		"道馆-未开馆证据": "closed", "道馆-寮境": "settled", "道馆-今日机会": "settled",
		"道馆-寮境活动": "assembly", "道馆-本人战斗HUD": "hud", "道馆-观战加油": "viewer", "道馆-资金提示": "spoils",
		"道馆-挑战按钮": "challenge", "道馆-集结倒数": "assembly",
		"导航-阴阳寮-点击阴阳寮": "yard", "导航-阴阳寮神社-点击神社": "guild", "回放-神社道馆按钮": "shrine",
		"道馆-已集结地图": "assemblyMap", "道馆-地图赏金": "assemblyMap", "道馆-地图成功标志": "never", "道馆-次数清零": "never",
		"道馆-备战面板": "prepare", "道馆-排名提示": "never", "道馆-进攻阶段": "challenge", "道馆-馆主旗": "never",
		"道馆-关闭小战结算": "smallWin", "道馆-关闭继续提示": "never", "道馆-展开庭院菜单": "never",
		"道馆-战报": "assembly",
	}
	r := replayRecognition(func(node string) bool {
		mu.Lock()
		defer mu.Unlock()
		if page == "challenge" && (node == "道馆-寮境活动" || node == "道馆-寮境" || node == "道馆-战报") {
			return true
		}
		if node == "道馆-备战面板" && page == "prepare" {
			// The preparation frame was verified, then lock-team automatically started combat.
			page = tc.battle
			return true
		}
		return (node == "道馆-寮境" && page == "assembly") || recognitions[node] == page
	})
	transitions := map[string][2]string{
		"道馆-退出未开列表":  {"closed", "shrine"},
		"道馆-退出已集结地图": {"assemblyMap", "shrine"},
		"道馆-退出神社":    {"shrine", "guild"},
		"道馆-退出阴阳寮":   {"guild", "yard"},
		"道馆-从庭院进入寮":  {"yard", "guild"},
		"道馆-从寮进入神社":  {"guild", "shrine"},
		"道馆-从神社进入":   {"shrine", "closed"},
		"道馆-进入已集结":   {"assemblyMap", "assembly"},
		"道馆-胜利资金":    {"spoils", "settled"},
		"道馆-退出结算寮境":  {"settled", "shrine"},
		"道馆-关闭小战结算":  {"smallWin", "unknown"},
	}
	c := replayClick(func(node string) bool {
		mu.Lock()
		defer mu.Unlock()
		if node == "道馆-入场点击" {
			node = map[string]string{"yard": "道馆-从庭院进入寮", "guild": "道馆-从寮进入神社", "shrine": "道馆-从神社进入"}[page]
		} else if node == "道馆-集结入场点击" {
			node = "道馆-进入已集结"
		}
		if node == "道馆-场内挑战" && page == "challenge" {
			page = tc.battle
			return true
		}
		if (node == "道馆-集结等待" && page == "assembly") || (node == "道馆-在战等待" && (page == "hud" || page == "viewer")) {
			page = "spoils"
			if tc.smallWin {
				page = "smallWin"
			}
			return true
		}
		want, ok := transitions[node]
		if !ok || page != want[0] {
			unexpected++
			return false
		}
		page = want[1]
		switch node {
		case "道馆-退出未开列表", "道馆-退出已集结地图":
			exits++
			if tc.breakExit {
				page = "unknown"
			}
		case "道馆-从庭院进入寮":
			if tc.expireTrip {
				now = time.Duration(tc.seconds) * time.Second
			}
		case "道馆-从神社进入":
			enters++
		case "道馆-进入已集结":
			mapEnters++
		}
		return true
	})
	budgetAction := &BudgetAction{now: clock, sleep: sleep}
	for name, action := range map[string]maa.CustomActionRunner{
		"DokanBudget": replayAction(func(ctx *maa.Context, arg *maa.CustomActionArg) bool {
			mu.Lock()
			if tc.expireBeforeClick && arg.CurrentTaskName == "道馆-进入已集结" {
				now = time.Duration(tc.seconds) * time.Second
			}
			mu.Unlock()
			return budgetAction.Run(ctx, arg)
		}),
		"DokanReplayClick": c,
	} {
		if err := res.RegisterCustomAction(name, action); err != nil {
			t.Fatal(err)
		}
	}
	for name, recognition := range map[string]maa.CustomRecognitionRunner{
		"DokanBudgetGate": &BudgetGate{now: clock}, "DokanReplayRecognition": r,
	} {
		if err := res.RegisterCustomRecognition(name, recognition); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []scenario{
		{name: "closed refreshes once then verifies skipped", page: "closed", seconds: 45, want: runstate.Skipped, enters: 1, exits: 2},
		{name: "navigation consumes deadline", page: "closed", seconds: 20, want: runstate.Skipped, exits: 1, expireTrip: true},
		{name: "assembly before cutoff enters", page: "closed", seconds: 45, want: runstate.Completed, opens: true, mapEnters: 1},
		{name: "first assembled frame at cutoff only exits", page: "closed", seconds: 15, want: runstate.Skipped, opens: true, exits: 1},
		{name: "cutoff between recognition and action only exits", page: "closed", seconds: 16, want: runstate.Skipped, opens: true, exits: 1, expireBeforeClick: true},
		{name: "assembled-map exit still needs courtyard confirmation", page: "closed", seconds: 15, want: runstate.Failed, opens: true, exits: 1, breakExit: true},
		{name: "unknown page fails", page: "unknown", want: runstate.Failed},
		{name: "unverified cleanup fails instead of skipping", page: "closed", want: runstate.Failed, exits: 1, breakExit: true},
		{name: "assembly resumes without starting", page: "assembly", want: runstate.Completed},
		{name: "challenge to HUD-only battle skips prep and reaches settlement", page: "challenge", battle: "hud", want: runstate.Completed},
		{name: "verified prep to HUD-only battle reaches settlement", page: "prepare", battle: "hud", want: runstate.Completed},
		{name: "challenge to viewer cheer reaches settlement", page: "challenge", battle: "viewer", want: runstate.Completed},
		{name: "HUD alone does not claim prior Dokan context", page: "hud", want: runstate.Failed},
		{name: "viewer cheer alone does not claim prior Dokan context", page: "viewer", want: runstate.Failed},
		{name: "small battle win alone cannot complete", page: "challenge", battle: "hud", want: runstate.Failed, smallWin: true},
	} {
		t.Run(s.name, func(t *testing.T) {
			mu.Lock()
			tc, page, now, enters, mapEnters, exits, unexpected = s, s.page, 0, 0, 0, 0, 0
			mu.Unlock()
			overrides := map[string]map[string]any{}
			ensure := func(name string) map[string]any {
				if overrides[name] == nil {
					overrides[name] = map[string]any{}
				}
				return overrides[name]
			}
			for name, node := range nodes {
				o := ensure(name)
				o["post_delay"], o["timeout"], o["rate_limit"] = 0, 450, 10
				if node["action"] == "Click" || name == "道馆-集结等待" || name == "道馆-在战等待" || name == "道馆-备战切队" {
					o["action"], o["custom_action"] = "Custom", "DokanReplayClick"
				}
			}
			ensure("道馆-备战切队")["enabled"] = true
			ensure("道馆-打开馆主预设")["enabled"] = true
			for name := range recognitions {
				o := ensure(name)
				o["recognition"], o["custom_recognition"] = "Custom", "DokanReplayRecognition"
			}
			ensure("道馆-返回箭头")["recognition"] = "DirectHit"
			ensure("道馆-从神社进入")["all_of"] = []string{"道馆-神社", "回放-神社道馆按钮", "道馆-可进馆"}
			ensure("自动道馆")["custom_action_param"] = map[string]any{"op": "init", "wait_seconds": s.seconds, "refresh_attempts": 1}
			collector, release := runstate.Bind(tasker)
			defer release()
			job := tasker.PostTask("自动道馆", overrides).Wait()
			got := collector.Outcome()
			if s.want == runstate.Skipped {
				var reason string
				raw, ok := got.Metrics["task_reason"].(json.RawMessage)
				if !ok || json.Unmarshal(raw, &reason) != nil || reason != "dokan_opening_timeout" {
					t.Errorf("missing explicit opening timeout reason: %+v", got)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if got.Status != s.want || !got.Reported || job.Success() != (s.want != runstate.Failed) ||
				enters != s.enters || mapEnters != s.mapEnters || exits != s.exits || unexpected != 0 || (s.want != runstate.Failed && page != "yard") {
				var visited []string
				if detail, err := job.GetDetail(); err == nil {
					for _, node := range detail.NodeDetails {
						visited = append(visited, node.Name)
					}
				}
				t.Fatalf("job=%v outcome=%+v page=%s shrine entries=%d assembly entries=%d exits=%d unexpected=%d; visited: %s",
					job.Status(), got, page, enters, mapEnters, exits, unexpected, strings.Join(visited, " → "))
			}
		})
	}
}

func testDokanHUD(t *testing.T, root string, res *maa.Resource, tasker *maa.Tasker) {
	t.Helper()
	// Optional read-only fixture from yys-automation@00252fc4. Never bundle it.
	path := os.Getenv("MAAYYS_DOKAN_HUD_CROP")
	explicit := path != ""
	if !explicit {
		path = filepath.Join(filepath.Dir(root), "yys-automation", "tasks", "Component", "GeneralBattle", "gb", "gb_battle_info.png")
	}
	raw, err := os.ReadFile(path)
	if !explicit && os.IsNotExist(err) {
		t.Skip("pinned Dokan HUD crop unavailable; set MAAYYS_DOKAN_HUD_CROP to its local path")
	}
	if err != nil {
		t.Fatal(err)
	}
	const pinned = "55d36e1938b961ffd01ee639ff9cd9fc8471b9b5ae71c9c927325ea78a5512f7"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != pinned {
		if !explicit {
			t.Skipf("default HUD crop checksum %s differs from pinned %s", got, pinned)
		}
		t.Fatalf("HUD crop checksum %s differs from pinned %s", got, pinned)
	}
	crop, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	withoutSmallArrow := image.NewRGBA(frame.Bounds())
	draw.Draw(withoutSmallArrow, frame.Bounds(), frame, image.Point{}, draw.Src)
	dst := crop.Bounds().Add(image.Pt(230, 12))
	draw.Draw(frame, dst, crop, crop.Bounds().Min, draw.Src)
	draw.Draw(withoutSmallArrow, dst, crop, crop.Bounds().Min, draw.Src)
	draw.Draw(withoutSmallArrow, image.Rect(230, 27, 244, 44), image.NewUniform(color.RGBA{82, 51, 31, 255}), image.Point{}, draw.Src)

	type probe struct {
		name  string
		frame image.Image
		hit   bool
	}
	var result error
	err = res.RegisterCustomAction("DokanHUDProbe", replayAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
		for _, p := range []probe{
			{"道馆-战斗运行中", frame, true}, {"道馆-本人战斗HUD", frame, true},
			{"道馆-观战加油", frame, false}, {"道馆-备战按钮", frame, false}, {"道馆-已在场内", frame, false},
			{"道馆-战斗运行中", withoutSmallArrow, false},
		} {
			got, err := ctx.RunRecognition(p.name, p.frame)
			if err != nil || got == nil || got.Hit != p.hit {
				result = fmt.Errorf("%s: recognition = %+v, %v; want hit=%v", p.name, got, err, p.hit)
				return false
			}
		}
		return true
	}))
	if err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"action": "Custom", "custom_action": "DokanHUDProbe", "pre_delay": 0, "post_delay": 0}
	job := tasker.PostTask("DokanHUDProbe", map[string]any{"DokanHUDProbe": entry}).Wait()
	if !job.Success() || result != nil {
		t.Fatalf("HUD probe: %v, %v", job.Status(), result)
	}
}

// v5.14's custom controller callback ABI does not match the v4 Go binding.
// Only capture the hidden window this test owns; inputs are disabled.
func newDokanReplayController(t *testing.T) *maa.Controller {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	user32 := syscall.NewLazyDLL("user32.dll")
	class, _ := syscall.UTF16PtrFromString("Static")
	hwnd, _, errno := user32.NewProc("CreateWindowExW").Call(
		0, uintptr(unsafe.Pointer(class)), 0, 0x80000000,
		0, 0, 1280, 720, 0, 0, 0, 0,
	)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		t.Fatalf("create hidden replay window: %v", errno)
	}
	t.Cleanup(func() { _, _, _ = user32.NewProc("DestroyWindow").Call(hwnd) })
	ctrl, err := maa.NewWin32Controller(unsafe.Pointer(hwnd), win32.ScreencapGDI, win32.InputNone, win32.InputNone)
	if err != nil {
		t.Fatal(err)
	}
	return ctrl
}
