//go:build windows

package dokan

import (
	"encoding/json"
	"fmt"
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

	type scenario struct {
		name, page string
		seconds    int
		want       runstate.Status
		enters     int
		exits      int
		breakExit  bool
		expireTrip bool
		opens      bool
		smallWin   bool
	}
	var mu sync.Mutex
	var tc scenario
	var page string
	var now time.Duration
	var enters, exits, unexpected int
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
			page = "assembly"
		}
	}
	recognitions := map[string]string{
		"道馆-庭院": "yard", "道馆-阴阳寮": "guild", "道馆-神社": "shrine", "道馆-地图标题": "closed",
		"道馆-未开馆证据": "closed", "道馆-寮境": "settled", "道馆-今日机会": "settled",
		"道馆-寮境活动": "assembly", "道馆-战斗运行中": "battle", "道馆-资金提示": "spoils",
		"道馆-挑战按钮": "assembly", "道馆-集结倒数": "assembly",
		"导航-阴阳寮-点击阴阳寮": "yard", "导航-阴阳寮神社-点击神社": "guild", "回放-神社道馆按钮": "shrine",
		"道馆-已集结地图": "never", "道馆-地图成功标志": "never", "道馆-次数清零": "never",
		"道馆-备战面板": "battle", "道馆-排名提示": "never", "道馆-进攻阶段": "assembly", "道馆-馆主旗": "never",
		"道馆-关闭小战结算": "smallWin", "道馆-关闭继续提示": "never", "道馆-展开庭院菜单": "never",
		"道馆-战报": "assembly",
	}
	r := replayRecognition(func(node string) bool {
		mu.Lock()
		defer mu.Unlock()
		return (node == "道馆-寮境" && page == "assembly") || recognitions[node] == page
	})
	transitions := map[string][2]string{
		"道馆-退出未开列表": {"closed", "shrine"},
		"道馆-退出神社":   {"shrine", "guild"},
		"道馆-退出阴阳寮":  {"guild", "yard"},
		"道馆-从庭院进入寮": {"yard", "guild"},
		"道馆-从寮进入神社": {"guild", "shrine"},
		"道馆-从神社进入":  {"shrine", "closed"},
		"道馆-胜利资金":   {"spoils", "settled"},
		"道馆-退出结算寮境": {"settled", "shrine"},
		"道馆-关闭小战结算": {"smallWin", "unknown"},
	}
	c := replayClick(func(node string) bool {
		mu.Lock()
		defer mu.Unlock()
		if (node == "道馆-集结等待" && page == "assembly") || (node == "道馆-在战等待" && page == "battle") {
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
		case "道馆-退出未开列表":
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
		}
		return true
	})
	for name, action := range map[string]maa.CustomActionRunner{
		"DokanBudget": &BudgetAction{now: clock, sleep: sleep}, "DokanReplayClick": c,
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
		{name: "fresh assembly evidence cancels due refresh", page: "closed", seconds: 45, want: runstate.Completed, opens: true},
		{name: "unknown page fails", page: "unknown", want: runstate.Failed},
		{name: "unverified cleanup fails instead of skipping", page: "closed", want: runstate.Failed, exits: 1, breakExit: true},
		{name: "assembly resumes without starting", page: "assembly", want: runstate.Completed},
		{name: "running battle resumes without starting", page: "battle", want: runstate.Completed},
		{name: "small battle win alone cannot complete", page: "battle", want: runstate.Failed, smallWin: true},
	} {
		t.Run(s.name, func(t *testing.T) {
			mu.Lock()
			tc, page, now, enters, exits, unexpected = s, s.page, 0, 0, 0, 0
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
				if node["action"] == "Click" || name == "道馆-集结等待" || name == "道馆-在战等待" {
					o["action"], o["custom_action"] = "Custom", "DokanReplayClick"
				}
			}
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
				enters != s.enters || exits != s.exits || unexpected != 0 || (s.want != runstate.Failed && page != "yard") {
				var visited []string
				if detail, err := job.GetDetail(); err == nil {
					for _, node := range detail.NodeDetails {
						visited = append(visited, node.Name)
					}
				}
				t.Fatalf("job=%v outcome=%+v page=%s enters=%d exits=%d unexpected=%d; visited: %s",
					job.Status(), got, page, enters, exits, unexpected, strings.Join(visited, " → "))
			}
		})
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
