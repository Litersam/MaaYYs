//go:build windows

package realm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/win32"
)

func TestNativeRealmTerminals(t *testing.T) {
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
	if err := maa.Init(maa.WithLibDir(lib), maa.WithLogDir(filepath.Join(root, "debug", "realm-test"))); err != nil {
		t.Fatal(err)
	}
	defer maa.Release()
	t.Logf("MaaFramework %s", maa.Version())

	ctrl := newRealmTestController(t)
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("connect to hidden realm fixture")
	}
	res, err := maa.NewResource()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	for _, name := range []string{"base", "official"} {
		if !res.PostBundle(filepath.Join(root, "resource_pack", name)).Wait().Success() {
			t.Fatalf("load %s resource bundle", name)
		}
	}
	if err := res.RegisterCustomAction("RealmFinish", &Finish{}); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC).Unix()
	fixture := &realmTimerFixture{observedAt: observedAt}
	if err := res.RegisterCustomRecognition("FixtureRealmTimer", fixture); err != nil {
		t.Fatal(err)
	}
	inventory := &realmInventoryFixture{}
	if err := res.RegisterCustomAction("FixtureInventorySwipe", inventory); err != nil {
		t.Fatal(err)
	}
	if err := res.RegisterCustomRecognition("FixtureInventoryCandidate", &realmInventoryCandidate{inventory}); err != nil {
		t.Fatal(err)
	}
	harvest := &realmHarvestFixture{}
	if err := res.RegisterCustomAction("FixtureHarvestProbe", harvest); err != nil {
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
	run := func(entry string, overrides map[string]any) (maa.Status, runstate.Outcome) {
		c, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask(entry, overrides).Wait()
		return job.Status(), c.Outcome()
	}
	miss := map[string]any{"recognition": "DirectHit", "inverse": true, "pre_wait_freezes": 0}
	hit := map[string]any{"recognition": "DirectHit", "inverse": false, "pre_wait_freezes": 0}
	t.Run("navigation miss never reports a business terminal", func(t *testing.T) {
		for _, entry := range []string{"好友寄养", "结界挂卡", "结界收菜"} {
			status, outcome := run(entry, map[string]any{
				entry:        map[string]any{"timeout": 5},
				"好友寄养_结界就绪":  miss,
				"结界挂卡_结算自家卡": miss,
				"结界挂卡_进入卡页":  miss,
				"结界收菜_结界就绪":  miss,
				"结界批次_进入结界":  map[string]any{"next": []string{"结界批次_自家结界"}, "timeout": 5},
				"结界批次_自家结界":  miss,
			})
			if !status.Failure() || outcome.Reported {
				t.Errorf("%s: navigation miss = %s %+v", entry, status, outcome)
			}
		}
	})
	t.Run("native StopTask is success but not courtyard confirmation", func(t *testing.T) {
		stop := map[string]any{"action": "StopTask", "recognition": "DirectHit"}
		if status, outcome := run("FixtureNativeStop", map[string]any{"FixtureNativeStop": stop}); !status.Success() || outcome.Reported {
			t.Fatalf("baseline StopTask = %s %+v", status, outcome)
		}
		// The nested StopTask stops the outer job too; the underlying native
		// status can still be success, so the explicit failed report is decisive.
		status, outcome := run("好友寄养_无候选", map[string]any{returnToCourtyard: stop})
		if !outcome.Reported || outcome.Status != runstate.Failed || len(outcome.Metrics) != 0 {
			t.Errorf("stop without courtyard anchor = %s %+v", status, outcome)
		}
	})
	t.Run("inventory page budget does not look like a proven empty list", func(t *testing.T) {
		for _, tc := range []struct {
			swipeToCandidate int
			want             runstate.Status
		}{
			{4, runstate.Completed}, // Fifth page is still in the search.
			{5, runstate.Failed},    // Another page may exist; cannot claim uneventful skip.
		} {
			inventory.swipes, inventory.candidateAfter = 0, tc.swipeToCandidate
			overrides := map[string]any{
				"结界挂卡_卡页标题":  hit,
				"结界挂卡_卡筛选类型": hit,
				"结界挂卡_无可挂卡":  miss,
				"结界挂卡_确认到底":  miss,
				"结界挂卡_筛选已生效": map[string]any{"timeout": 5},
				"结界挂卡_翻页": map[string]any{
					"action": "Custom", "custom_action": "FixtureInventorySwipe", "post_delay": 0,
				},
				"结界挂卡_点库存卡": map[string]any{
					"recognition": "Custom", "custom_recognition": "FixtureInventoryCandidate",
					"action": "Custom", "custom_action": "RealmFinish", "post_delay": 0, "next": []string{},
					"custom_action_param": map[string]string{"status": "completed"},
				},
				courtyardConfirmed: hit,
			}
			_, outcome := run("结界挂卡_筛选已生效", overrides)
			if inventory.swipes != 4 || !outcome.Reported || outcome.Status != tc.want {
				t.Errorf("candidate after %d swipes: swiped %d, got %+v; want %s",
					tc.swipeToCandidate, inventory.swipes, outcome, tc.want)
			}
		}
	})
	t.Run("selected harvest tab and entire OCR count", func(t *testing.T) {
		for _, tc := range []struct {
			name          string
			exp, positive bool
			want          [3]bool // Stamina empty, XP empty, stamina collect.
		}{
			{"XP with zero cannot skip stamina", true, false, [3]bool{false, true, false}},
			{"stamina with zero", false, false, [3]bool{true, false, false}},
			{"stamina with ten", false, true, [3]bool{false, false, true}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				harvest.frame, harvest.want, harvest.err = harvestFrame(t, tc.exp, tc.positive), tc.want, nil
				status, _ := run("FixtureHarvestProbe", map[string]any{
					"FixtureHarvestProbe": map[string]any{"action": "Custom", "custom_action": "FixtureHarvestProbe", "timeout": 5},
				})
				if !status.Success() || harvest.err != nil {
					t.Fatalf("recognition = %s, %v", status, harvest.err)
				}
			})
		}
	})
	for _, tc := range []struct {
		entry, timer, text string
		kind               timerKind
		status             runstate.Status
		remaining          int64
	}{
		{"好友寄养_已寄养", "好友寄养_计时", "06:02:00", "foster", runstate.Skipped, 21720},
		{"好友寄养_寄养成功", "好友寄养_计时", "00:10:00", "foster", runstate.Completed, 600},
		{"结界挂卡_已有生效卡", "结界挂卡_计时", "21:00:00", "realm_card", runstate.Skipped, 75600},
		{"结界挂卡_挂卡成功", "结界挂卡_计时", "21:00:00", "realm_card", runstate.Completed, 75600},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			fixture.kind, fixture.text = tc.kind, tc.text
			status, outcome := run(tc.entry, map[string]any{
				"好友寄养_育成标题": hit,
				"结界挂卡_卡页标题": hit,
				"结界挂卡_邀请按钮": hit,
				tc.timer: map[string]any{
					"recognition": "Custom", "custom_recognition": "FixtureRealmTimer",
				},
				courtyardConfirmed: hit,
			})
			if !status.Success() || !outcome.Reported || outcome.Status != tc.status {
				t.Fatalf("terminal = %s %+v", status, outcome)
			}
			for key, want := range map[string]int64{
				string(tc.kind) + "_remaining_sec": tc.remaining,
				string(tc.kind) + "_due_at":        observedAt + tc.remaining,
			} {
				got, ok := outcome.Metrics[key].(json.RawMessage)
				var value int64
				if !ok || json.Unmarshal(got, &value) != nil || value != want {
					t.Errorf("%s = %s; want %d", key, got, want)
				}
			}
		})
	}
}

type realmHarvestFixture struct {
	frame image.Image
	want  [3]bool
	err   error
}

func (f *realmHarvestFixture) Run(ctx *maa.Context, _ *maa.CustomActionArg) bool {
	hit := map[string]any{"recognition": "DirectHit"}
	overrides := map[string]any{
		// The synthetic screencap supplies active color and numeric text. The actual
		// panel close template and tab labels are stable and matched by this fixture.
		"阴阳寮奖励领取_结界奖励领取_关闭食盒界面": hit,
		"结界收菜_体力标签文字":           hit,
		"结界收菜_经验标签文字":           hit,
		"结界收菜_取出按钮":             hit,
	}
	nodes := []string{"结界收菜_体力未到", "结界收菜_经验未到", "结界收菜_取体力"}
	for i, node := range nodes {
		d, err := ctx.RunRecognition(node, f.frame, overrides)
		if err != nil || d == nil || d.Hit != f.want[i] {
			f.err = fmt.Errorf("%s recognition: want hit=%t, got %+v, %v", node, f.want[i], d, err)
			return false
		}
	}
	return true
}

func harvestFrame(t *testing.T, exp, positive bool) image.Image {
	t.Helper()
	// Synthetic 10/100 in Microsoft YaHei. Crop out 1 for the zero fixture.
	// Embedding the raster lets native OCR run without depending on host fonts.
	const ten = "iVBORw0KGgoAAAANSUhEUgAAAHQAAAArCAYAAACpQqwFAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAJOSURBVHhe7ZFbbsUwCAWz/023upUiUURgDvF91GUk/5gz4ITja9iKw18Mf5tZ6GbMQjdjFroZs9DNmIVuxix0M2ahm4EXehzHz1E5PX/u0uml5k/829/dJwN16w73D49Oh47fcR7490aH4J3orCDt4gcqQzOn2/NEcf0s4pxkjtIzyyl9CGEHP0QdSPIkcwVx/buJYyH5V2YovzpcfXh0d8WzsieV80nfkNU8SjYjXKhHGfas7EnlXNUqz7Iqm9U8SjYD2cqwZ2UfqHmL4q7KZjWPks1ANh1GcxbFUbIe6tKcJXKiu4qO40EmHURzFsVRsh7q0pwlcqK7io7jQSYdRHMW6tDcFdSnOUvkRHcVHceDTDqI5izUobkrqE9zlsiJ7io6jgeZdBDNWYhDMhW0B81ZIie6q+g4HmTSQTRnIQ7JVNAeNGeJnOiuouN4kEkH0ZyFOCRTQXvQnCVyoruKjuNBJh1Ec5bKqeoU2ofmLJET3VV0HA8y6SCas1ROVafQPjRniZzorqLjeJBJB9GcpXKqOoX2oTlL5ER3FR3Hg0xl0MpsVVdQeq3KZjWPks1AtjJsZbaqKyi9VmWzmkfJZiBbGbYqm9U6KP1WZbOaR8lmIFsdRvJVpqqrqP1I/pUZCuqgDrT5yMlqJySjoPZb8Q0PVvWhoC6dof5DonMFyah0evr3RofgneisAnW6M9g/nPShOYU7Pf3b390nY223BTzrQ/8LH/fXZqH3+Li/Ngu9x0f9tVnmfebPbcYsdDNmoZsxC92MWehmzEI34xs2pWhiqPj6hAAAAABJRU5ErkJggg=="

	b, err := base64.StdEncoding.DecodeString(ten)
	if err != nil {
		t.Fatal(err)
	}
	glyph, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(frame, frame.Bounds(), image.White, image.Point{}, draw.Src)
	source := glyph.Bounds()
	if !positive {
		source.Min.X += 18
	}
	draw.Draw(frame, image.Rect(670, 527, 670+source.Dx(), 527+source.Dy()), glyph, source.Min, draw.Src)
	x := 609
	if exp {
		x = 861
	}
	draw.Draw(frame, image.Rect(x, 637, x+13, 673), image.NewUniform(color.RGBA{246, 194, 138, 255}), image.Point{}, draw.Src)
	return frame
}

type realmTimerFixture struct {
	kind       timerKind
	text       string
	observedAt int64
}

type realmInventoryFixture struct {
	swipes, candidateAfter int
}

func (f *realmInventoryFixture) Run(_ *maa.Context, _ *maa.CustomActionArg) bool {
	f.swipes++
	return true
}

type realmInventoryCandidate struct {
	inventory *realmInventoryFixture
}

func (f *realmInventoryCandidate) Run(*maa.Context, *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if f.inventory.swipes < f.inventory.candidateAfter {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: maa.Rect{100, 100, 25, 20}}, true
}

func (f *realmTimerFixture) Run(*maa.Context, *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	seconds, valid := remainingSeconds(f.kind, f.text)
	if !valid {
		return nil, false
	}
	data, err := json.Marshal(timerObservation{f.kind, seconds, f.observedAt})
	return &maa.CustomRecognitionResult{Box: maa.Rect{100, 100, 25, 20}, Detail: string(data)}, err == nil
}

func newRealmTestController(t *testing.T) *maa.Controller {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	user32 := syscall.NewLazyDLL("user32.dll")
	class, _ := syscall.UTF16PtrFromString("Static")
	hwnd, _, errno := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000,
		0, 0, 1280, 720, 0, 0, 0, 0)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		t.Fatalf("create hidden fixture window: %v", errno)
	}
	t.Cleanup(func() { user32.NewProc("DestroyWindow").Call(hwnd) })
	ctrl, err := maa.NewWin32Controller(unsafe.Pointer(hwnd), win32.ScreencapGDI, win32.InputNone, win32.InputNone)
	if err != nil {
		t.Fatal(err)
	}
	return ctrl
}
