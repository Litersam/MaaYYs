//go:build windows

package guild_donation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/win32"
)

func TestNativeDonationPaths(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	lib := os.Getenv("MAAFW_LIB_DIR")
	if lib == "" {
		lib, _ = filepath.Abs(filepath.Join(root, "maafw"))
	}
	if _, err := os.Stat(filepath.Join(lib, "MaaFramework.dll")); err != nil {
		t.Skipf("offline MaaFramework unavailable: %v", err)
	}
	if err := maa.Init(maa.WithLibDir(lib), maa.WithLogDir(filepath.Join(root, "debug", "guild-donation-test"))); err != nil {
		t.Fatal(err)
	}
	defer maa.Release()

	// Native pipeline replay on an isolated blank window, with synthetic
	// recognitions and intercepted click actions. No live game input.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user32 := syscall.NewLazyDLL("user32.dll")
	class, _ := syscall.UTF16PtrFromString("Static")
	hwnd, _, errno := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000,
		0, 0, 1280, 720, 0, 0, 0, 0)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		t.Fatalf("create hidden fixture window: %v", errno)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	ctrl, err := maa.NewWin32Controller(unsafe.Pointer(hwnd), win32.ScreencapGDI, win32.InputNone, win32.InputNone)
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("connect to hidden fixture")
	}
	res, err := maa.NewResource()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	for _, pack := range []string{"base", "official"} {
		if !res.PostBundle(filepath.Join(root, "resource_pack", pack)).Wait().Success() {
			t.Fatalf("offline resource load: %s", pack)
		}
	}
	state := &donationPathFixture{}
	if err := res.RegisterCustomAction("DonationFixtureDismiss", state); err != nil {
		t.Fatal(err)
	}
	if err := res.RegisterCustomRecognition("DonationFixtureAnchor", donationPathAnchor{state}); err != nil {
		t.Fatal(err)
	}
	if err := res.RegisterCustomAction("TaskOutcome", &runstate.TerminalAction{}); err != nil {
		t.Fatal(err)
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

	miss := state.recognition("never")
	t.Run("legacy entry requires panel progress", func(t *testing.T) {
		for _, hasProgress := range []bool{false, true} {
			progress := miss
			if hasProgress {
				progress = state.recognition("always")
			}
			collector, release := runstate.Bind(tasker)
			job := tasker.PostTask("寮三十-开始任务", map[string]any{
				"寮三十-开始任务":   map[string]any{"timeout": 50, "post_delay": 0},
				panelNode:    state.recognition("always"),
				progressNode: progress,
				"寮捐-执行": map[string]any{
					"action": "Custom", "custom_action": "TaskOutcome", "custom_action_param": map[string]string{"status": "skipped"},
				},
				"寮捐-奖励标题":   miss,
				"寮捐-打开集体任务": miss,
				"寮捐-打开神社":   miss,
				"寮捐-进入阴阳寮":  miss,
				"寮捐-展开庭院菜单": miss,
			}).Wait()
			got := collector.Outcome()
			release()
			want := runstate.Failed
			if hasProgress {
				want = runstate.Skipped
			}
			if job.Status().Success() != hasProgress || !got.Reported || got.Status != want {
				t.Errorf("progress=%v -> %s %+v; want %s", hasProgress, job.Status(), got, want)
			}
		}
	})
	for _, tc := range []struct {
		name     string
		rewards  int
		progress bool
		want     bool
		dismiss  int
	}{
		{"normal reward", 1, true, true, 1},
		{"double reward", 2, true, true, 2},
		{"reward without progress", 1, false, false, 1},
		{"bounded even if a third popup appears", 3, true, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*state = donationPathFixture{rewards: tc.rewards, progress: tc.progress}
			overrides := map[string]any{
				"寮捐-提交后等待面板": map[string]any{"post_delay": 0, "timeout": 50},
				"寮捐-奖励标题":    state.recognition("reward"),
				"寮捐-确认30次":   state.recognition("progress"),
			}
			for _, node := range []string{"寮捐-关闭首层奖励", "寮捐-关闭第二层奖励"} {
				overrides[node] = map[string]any{
					"action": "Custom", "custom_action": "DonationFixtureDismiss", "post_delay": 0, "timeout": 50,
				}
			}
			job := tasker.PostTask("寮捐-提交后等待面板", overrides).Wait()
			if got := job.Status().Success(); got != tc.want || state.dismissed != tc.dismiss {
				t.Errorf("path = %s, dismissals = %d; want success=%v, dismissals=%d",
					job.Status(), state.dismissed, tc.want, tc.dismiss)
			}
		})
	}
	t.Run("courtyard gates outcome", func(t *testing.T) {
		for _, tc := range []struct {
			courtyard, panel bool
			want             runstate.Status
		}{
			{false, false, ""}, {true, true, runstate.Failed}, {true, false, runstate.Skipped},
		} {
			collector, release := runstate.Bind(tasker)
			court := miss
			if tc.courtyard {
				court = state.recognition("always")
			}
			panel := miss
			if tc.panel {
				panel = state.recognition("always")
			}
			job := tasker.PostTask("寮捐-离开面板", map[string]any{
				"寮捐-关闭面板": map[string]any{
					"recognition": "DirectHit", "action": "DoNothing", "next": []string{"寮捐-返回受阻", terminalNode},
					"post_delay": 0, "timeout": 50,
				},
				panelNode:     panel,
				submitNode:    miss,
				"寮捐-奖励标题":     miss,
				"寮捐-神社集体任务入口": miss,
				"导航-阴阳寮-识别是否位于阴阳寮": miss,
				"寮捐-庭院":      court,
				terminalNode: map[string]any{"custom_action_param": map[string]string{"status": "skipped", "reason": "already_at_daily_limit"}},
			}).Wait()
			got := collector.Outcome()
			release()
			if job.Status().Success() != (tc.want == runstate.Skipped) || got.Reported != (tc.want != "") ||
				(got.Reported && got.Status != tc.want) {
				t.Errorf("courtyard=%v panel=%v -> %s %+v", tc.courtyard, tc.panel, job.Status(), got)
			}
		}
	})
}

type donationPathFixture struct {
	rewards, dismissed int
	progress           bool
}

func (f *donationPathFixture) Run(*maa.Context, *maa.CustomActionArg) bool {
	f.dismissed++
	return f.dismissed <= f.rewards
}

func (*donationPathFixture) recognition(param string) map[string]any {
	return map[string]any{
		"recognition": "Custom", "custom_recognition": "DonationFixtureAnchor",
		"custom_recognition_param": map[string]string{"kind": param},
	}
}

type donationPathAnchor struct{ state *donationPathFixture }

func (r donationPathAnchor) Run(_ *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	f := r.state
	pending := f.dismissed < f.rewards
	hit := p.Kind == "always" || (p.Kind == "reward" && pending) || (p.Kind == "progress" && f.progress)
	return &maa.CustomRecognitionResult{Box: maa.Rect{500, 100, 100, 50}}, hit
}
