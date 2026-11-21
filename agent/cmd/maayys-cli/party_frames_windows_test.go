//go:build windows

package main

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const partyHistory = "00252fc48e483191c83402c5cf8fd14f924f9043"

func historicalPartyCrop(t *testing.T, path string) image.Image {
	t.Helper()
	root := os.Getenv("MAAYYS_AWAKE_REFERENCE")
	if root == "" {
		t.Skip("set MAAYYS_AWAKE_REFERENCE to the local yys-automation repository with historical fixtures")
	}
	data, err := exec.Command("git", "-C", root, "--no-pager", "show", partyHistory+":"+path).Output()
	if err != nil {
		t.Fatalf("read historical crop %s: %v", path, err)
	}
	crop, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return crop
}

func pastePartyCrop(frame draw.Image, crop image.Image, x, y int) {
	p := image.Pt(x, y)
	draw.Draw(frame, image.Rectangle{Min: p, Max: p.Add(crop.Bounds().Size())}, crop, crop.Bounds().Min, draw.Over)
}

func blankPartyFrame() *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{64, 60, 56, 255}), image.Point{}, draw.Src)
	return frame
}

// The native task runs its production recognizers and click actions against
// images in memory. The controller never sends any OS input.
type partyFrameController struct {
	mu           sync.Mutex
	frame        image.Image
	clicks       []image.Point
	step         func(image.Point) image.Image
	afterCapture []image.Image
}

func (c *partyFrameController) screencap() image.Image {
	c.mu.Lock()
	defer c.mu.Unlock()
	frame := c.frame
	if len(c.afterCapture) > 0 {
		c.frame = c.afterCapture[0]
		c.afterCapture = c.afterCapture[1:]
	}
	return frame
}

func (c *partyFrameController) Click(x, y int32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := image.Pt(int(x), int(y))
	c.clicks = append(c.clicks, p)
	if c.step != nil {
		if next := c.step(p); next != nil {
			c.frame = next
		}
	}
	return true
}

func (c *partyFrameController) set(frame image.Image) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frame = frame
}

func (c *partyFrameController) recorded() []image.Point {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.clicks)
}

func withPartyFrameReplay(t *testing.T, c *partyFrameController, run func(root string, res *maa.Resource, tasker *maa.Tasker)) {
	t.Helper()
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		ctrl, destroy := newMaa514FrameController(t, root, c)
		defer destroy()
		if !ctrl.PostConnect().Wait().Success() {
			t.Fatal("offline memory controller failed to connect")
		}
		previous := tasker.GetController()
		if err := tasker.BindController(ctrl); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tasker.BindController(previous); err != nil {
				t.Error(err)
			}
		}()
		run(root, res, tasker)
	})
}

func newMaa514FrameController(t *testing.T, root string, c *partyFrameController) (*maa.Controller, func()) {
	t.Helper()
	if maa.Version() != "v5.14.2" {
		t.Skipf("frame replay targets the MaaFramework v5.14.2 callback ABI, got %s", maa.Version())
	}
	// The v4 Go binding's callback struct does not include v5.14's
	// relative_move or shell slots. Keep this adapter test-only and pinned.
	type callbacks struct {
		connect, connected, uuid, features         uintptr
		startApp, stopApp, screencap, click, swipe uintptr
		touchDown, touchMove, touchUp, clickKey    uintptr
		inputText, keyDown, keyUp, scroll          uintptr
		relativeMove, shell, inactive, info        uintptr
	}
	dll := syscall.NewLazyDLL(filepath.Join(resolveMaaLibDir(root), "MaaFramework.dll"))
	strSet := dll.NewProc("MaaStringBufferSet")
	imgSet := dll.NewProc("MaaImageBufferSetEncoded")
	create := dll.NewProc("MaaCustomControllerCreate")
	destroy := dll.NewProc("MaaControllerDestroy")
	for _, proc := range []*syscall.LazyProc{strSet, imgSet, create, destroy} {
		if err := proc.Find(); err != nil {
			t.Fatal(err)
		}
	}
	ok := syscall.NewCallback(func(uintptr) uintptr { return 1 })
	failApp := syscall.NewCallback(func(uintptr, uintptr) uintptr { return 0 })
	cb := &callbacks{
		connect:   ok,
		connected: ok,
		uuid: syscall.NewCallback(func(_ uintptr, buf uintptr) uintptr {
			id := []byte("batch3-native-replay\x00")
			r, _, _ := strSet.Call(buf, uintptr(unsafe.Pointer(&id[0])))
			return r
		}),
		features: syscall.NewCallback(func(uintptr) uintptr { return 0 }),
		startApp: failApp,
		stopApp:  failApp,
		screencap: syscall.NewCallback(func(_ uintptr, buf uintptr) uintptr {
			var data bytes.Buffer
			if frame := c.screencap(); frame == nil || png.Encode(&data, frame) != nil {
				return 0
			}
			raw := data.Bytes()
			r, _, _ := imgSet.Call(buf, uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw)))
			return r
		}),
		click: syscall.NewCallback(func(x, y, _ uintptr) uintptr {
			if c.Click(int32(x), int32(y)) {
				return 1
			}
			return 0
		}),
		inactive: ok,
	}
	handle, _, err := create.Call(uintptr(unsafe.Pointer(cb)), 0)
	if handle == 0 {
		t.Fatalf("create in-process replay controller: %v", err)
	}
	// The pinned binding stores only its native handle in Controller; its
	// event registry is not used here, and destruction stays with the C API.
	if unsafe.Sizeof(maa.Controller{}) != unsafe.Sizeof(handle) {
		t.Fatal("unexpected Go controller layout")
	}
	return (*maa.Controller)(unsafe.Pointer(&handle)), func() {
		destroy.Call(handle)
		runtime.KeepAlive(cb)
	}
}

func registerPartyProbe(t *testing.T, res *maa.Resource, name string, run func(*maa.Context, *maa.CustomActionArg) bool) {
	t.Helper()
	if err := res.RegisterCustomAction(name, offlineAction(run)); err != nil {
		t.Fatal(err)
	}
}

func runPartyProbe(t *testing.T, tasker *maa.Tasker, name string) {
	t.Helper()
	job := tasker.PostTask(name, map[string]any{name: map[string]any{"action": "Custom", "custom_action": name}}).Wait()
	if !job.Success() {
		t.Fatalf("native probe %s failed: %v", name, job.Status())
	}
}

func partyProbeHit(t *testing.T, ctx *maa.Context, node string, frame image.Image, want bool) {
	t.Helper()
	d, err := ctx.RunRecognition(node, frame)
	if got := d != nil && d.Hit; err != nil || got != want {
		t.Errorf("%s: hit=%t, want %t, detail=%+v, err=%v", node, got, want, d, err)
	}
}

func TestNativePartyMissingTeammateNeverSends(t *testing.T) {
	const gi = "tasks/Component/GeneralInvite/gi/"
	ensure := historicalPartyCrop(t, gi+"gi_invite_ensure.png")
	roomTitle := historicalPartyCrop(t, gi+"gi_gi_in_room.png")
	tabNames := []string{"好友", "跨区", "最近"}
	frames := make([]*image.RGBA, len(tabNames)+1)
	for i := range frames {
		frame := blankPartyFrame()
		pastePartyCrop(frame, ensure, 517, 553)
		for j, name := range tabNames {
			x := 353 + j*118
			drawPartyLabel(t, frame, name, x, 101, i == j+1)
		}
		if i <= 1 {
			draw.Draw(frame, image.Rect(442, 214, 522, 244), syntheticGhostFrame(t), image.Pt(145, 52), draw.Src)
		}
		frames[i] = frame
	}
	c := &partyFrameController{frame: frames[0]}
	withPartyFrameReplay(t, c, func(root string, _ *maa.Resource, tasker *maa.Tasker) {
		court, err := loadPNG(filepath.Join(root, "resource_pack", "base", "image", "导航", "导航-庭院-识别是否位于庭院-模板匹配_1.png"))
		if err != nil {
			t.Fatal(err)
		}
		room := drawReviewCrop(roomTitle, image.Pt(92, 17))
		home := drawReviewCrop(court, image.Pt(1200, 640))
		targets := []image.Rectangle{
			image.Rect(353, 101, 425, 148), image.Rect(471, 101, 543, 148), image.Rect(589, 101, 661, 148),
			image.Rect(1008, 77, 1057, 122), image.Rect(19, 13, 77, 68),
		}
		c.mu.Lock()
		c.step = func(p image.Point) image.Image {
			i := len(c.clicks) - 1
			if i >= len(targets) || !p.In(targets[i]) {
				return nil
			}
			if i < len(tabNames) {
				return frames[i+1]
			}
			if i == len(tabNames) {
				return room
			}
			return home
		}
		c.mu.Unlock()
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask("组队管控-初始化", map[string]any{
			"组队管控-初始化": map[string]any{
				"custom_action_param": map[string]any{"role": "captain", "teammate_names": "涂壁甲,另一名", "limit_count": 3},
				"next":                []string{"组队管控-定向勾选"},
				"timeout":             1500,
			},
			"组队管控-安全归庭": map[string]any{"timeout": 3000, "rate_limit": 30},
		}).Wait()
		out, clicks := collector.Outcome(), c.recorded()
		if job.Success() || !out.Reported || out.Status != runstate.Failed || !partyMetricEquals(out, "task_reason", `"party_teammate_not_found"`) ||
			!partyMetricEquals(out, "party_battles", "0") || len(clicks) != len(targets) {
			t.Fatalf("missing teammate: job=%v outcome=%+v clicks=%v", job.Status(), out, clicks)
		}
		for i, click := range clicks {
			if !click.In(targets[i]) {
				t.Errorf("click %d: %v outside %v", i, click, targets[i])
			}
		}
	})
}

func TestNativePartyRoomLayoutAndMemberGates(t *testing.T) {
	const gi = "tasks/Component/GeneralInvite/gi/"
	roomTitle := historicalPartyCrop(t, gi+"gi_gi_in_room.png")
	plus := historicalPartyCrop(t, gi+"gi_add_2.png")
	legacy := historicalPartyCrop(t, gi+"gi_add_1.png")
	seaPlus := historicalPartyCrop(t, gi+"gi_add_sea.png")
	challenge := historicalPartyCrop(t, gi+"gi_fire.png")
	seaChallenge := historicalPartyCrop(t, gi+"gi_fire_sea.png")
	prepare := historicalPartyCrop(t, "tasks/Component/GeneralBattle/gb/gb_prepare_highlight.png")
	// Tracked Kagura character artwork, composed into a seat for replay.
	// This is not an archived occupied-room screenshot.
	root := testRepoRoot(t)
	readPNG := func(path string) image.Image {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, err := png.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	kagura := readPNG(filepath.Join(root, "resource_pack", "base", "image", "非战斗", "式神委派", "式神委派-识别神乐_1.png"))
	portrait := image.NewRGBA(image.Rect(0, 0, 108, 108))
	for y := range 108 {
		for x := range 108 {
			portrait.Set(x, y, kagura.At(7+x*145/108, y*145/108))
		}
	}
	put := func(frame draw.Image, crop image.Image, at image.Point) {
		if crop != nil {
			pastePartyCrop(frame, crop, at.X, at.Y)
		}
	}
	stripe := func(frame draw.Image) {
		draw.Draw(frame, image.Rect(604, 226, 710, 240), image.NewUniform(color.RGBA{158, 103, 78, 255}), image.Point{}, draw.Src)
	}
	c := &partyFrameController{frame: blankPartyFrame()}
	withPartyFrameReplay(t, c, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("seat replay MaaFramework=%s", maa.Version())
		const probe = "Batch3RoomLayoutAndMemberGates"
		registerPartyProbe(t, res, probe, func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			config := map[string]any{"flow": "party", "role": "captain", "teammate_names": "甲", "limit_count": 1}
			init := func() bool {
				d, err := ctx.RunAction("组队管控-初始化", maa.Rect{}, "", map[string]any{
					"组队管控-初始化": map[string]any{"custom_action_param": config},
				})
				return err == nil && d != nil && d.Success
			}
			if !init() {
				return false
			}
			assertStart := func(t *testing.T, frame image.Image, full bool) {
				t.Helper()
				const wait, start = "组队管控-等待满员", "组队管控-队长开战"
				buttonRegion := image.Rect(1131, 565, 1280, 708)
				if full {
					hit, err := ctx.RunRecognition(start, frame)
					if err != nil || hit == nil || !hit.Hit {
						t.Fatalf("expected a recognized challenge target: detail=%+v err=%v", hit, err)
					}
					box := hit.Box
					button := image.Rect(box.X(), box.Y(), box.X()+box.Width(), box.Y()+box.Height())
					if button.Empty() || !button.In(buttonRegion) {
						t.Fatalf("challenge target outside lower-right button region: %v", button)
					}
				}
				c.set(frame)
				before := len(c.recorded())
				d, err := ctx.RunTask(wait, map[string]any{
					wait:  map[string]any{"timeout": 450, "rate_limit": 50, "on_error": []string{}},
					start: map[string]any{"next": []string{}, "post_delay": 0, "on_error": []string{}},
				})
				clicks := c.recorded()[before:]
				t.Logf("native wait-for-full start=%t clicks=%v", d != nil && d.Status.Success(), clicks)
				if full {
					if err != nil || d == nil || !d.Status.Success() || len(clicks) != 1 || !clicks[0].In(buttonRegion) {
						t.Errorf("expected exactly one challenge click in %v: task=%+v err=%v clicks=%v", buttonRegion, d, err, clicks)
					}
				} else if d == nil || d.Status.Success() || len(clicks) != 0 {
					t.Errorf("expected wait timeout and zero clicks: task=%+v err=%v clicks=%v", d, err, clicks)
				}
			}
			for _, tc := range []struct {
				name    string
				middle  image.Image
				midAt   image.Point
				right   image.Image
				rightAt image.Point
				paint   func(draw.Image)
				title   bool
				full    bool
			}{
				{"middle_plus", plus, image.Pt(600, 217), portrait, image.Pt(1000, 220), nil, true, false},
				{"right_plus", portrait, image.Pt(598, 230), plus, image.Pt(1013, 203), nil, true, false},
				{"legacy_middle_reviewer_106x14", legacy, image.Pt(596, 241), portrait, image.Pt(1000, 220), stripe, true, false},
				{"legacy_middle_shifted", legacy, image.Pt(602, 244), portrait, image.Pt(1000, 220), stripe, true, false},
				{"legacy_right", portrait, image.Pt(598, 230), legacy, image.Pt(1009, 229), nil, true, false},
				{"middle_unknown_without_glyph", nil, image.Point{}, portrait, image.Pt(1000, 220), stripe, true, false},
				{"portrait_outside_middle", portrait, image.Pt(475, 230), portrait, image.Pt(1000, 220), nil, true, false},
				{"both_standard_seats_with_artwork", portrait, image.Pt(597, 228), portrait, image.Pt(1003, 223), nil, true, true},
				{"unknown_layout_with_artwork", portrait, image.Pt(598, 230), portrait, image.Pt(1000, 220), nil, false, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					frame := blankPartyFrame()
					if tc.title {
						put(frame, roomTitle, image.Pt(92, 17))
					}
					if tc.paint != nil {
						tc.paint(frame)
					}
					put(frame, tc.middle, tc.midAt)
					put(frame, tc.right, tc.rightAt)
					put(frame, challenge, image.Pt(1179, 602))
					// A plus in the sea-only slot does not invalidate a standard room.
					if tc.full {
						put(frame, seaPlus, image.Pt(836, 231))
					}
					partyProbeHit(t, ctx, "组队管控-标准房间", frame, tc.title)
					partyProbeHit(t, ctx, "组队管控-挑战可用", frame, true)
					partyProbeHit(t, ctx, "组队管控-满员证据", frame, tc.full)
					partyProbeHit(t, ctx, "组队管控-队长开战", frame, tc.full)
					if tc.full || tc.name == "legacy_middle_reviewer_106x14" {
						assertStart(t, frame, tc.full)
					}
				})
			}
			t.Run("sea_layout", func(t *testing.T) {
				captions := os.Getenv("MAAYYS_PARTY_REVIEW_CAPTIONS")
				if captions == "" {
					t.Skip("set MAAYYS_PARTY_REVIEW_CAPTIONS to the reviewer directory with synthetic 永生之海 and 发言 OCR crops")
				}
				title := readPNG(filepath.Join(captions, "永生之海.png"))
				speech := readPNG(filepath.Join(captions, "发言.png"))
				for _, tc := range []struct {
					name, dungeon string
					seat          image.Image
					at            image.Point
					footer        bool
					full          bool
				}{
					{"one_partner", "eternity_sea", portrait, image.Pt(833, 229), true, true},
					{"empty_plus", "eternity_sea", seaPlus, image.Pt(836, 231), true, false},
					{"unknown_seat", "eternity_sea", nil, image.Point{}, true, false},
					{"wrong_dungeon", "soul_10", portrait, image.Pt(833, 229), true, false},
					{"missing_layout_footer", "eternity_sea", portrait, image.Pt(833, 229), false, false},
				} {
					t.Run(tc.name, func(t *testing.T) {
						config["dungeon"] = tc.dungeon
						if !init() {
							t.Fatal("init sea layout probe")
						}
						frame := blankPartyFrame()
						put(frame, title, image.Pt(89, 12))
						if tc.footer {
							put(frame, speech, image.Pt(25, 510))
						}
						put(frame, legacy, image.Pt(596, 241))
						put(frame, plus, image.Pt(1013, 203))
						put(frame, tc.seat, tc.at)
						put(frame, seaChallenge, image.Pt(1160, 586))
						partyProbeHit(t, ctx, "组队管控-永生房间", frame, tc.footer)
						partyProbeHit(t, ctx, "组队管控-挑战可用", frame, true)
						partyProbeHit(t, ctx, "组队管控-满员证据", frame, tc.full)
						partyProbeHit(t, ctx, "组队管控-队长开战", frame, tc.full)
						if tc.full || tc.name == "empty_plus" {
							assertStart(t, frame, tc.full)
						}
					})
				}
			})
			config = map[string]any{"flow": "party", "role": "member", "captain_names": "涂壁", "limit_count": 1}
			if !init() {
				return false
			}
			partyProbeHit(t, ctx, "组队管控-加入房间", drawReviewCrop(roomTitle, image.Pt(92, 17)), false)
			partyProbeHit(t, ctx, "组队管控-队员直接备战", drawReviewCrop(prepare, image.Pt(1128, 536)), false)
			return !t.Failed()
		})
		runPartyProbe(t, tasker, probe)
	})
}

func TestNativePartySameParentBuildRoomTwice(t *testing.T) {
	build := drawReviewCrop(historicalPartyCrop(t, "tasks/Component/GeneralRoom/gr/gr_create_room.png"), image.Pt(985, 600))
	c := &partyFrameController{frame: build}
	withPartyFrameReplay(t, c, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		const probe = "Batch3SameParentTwoBuilds"
		registerPartyProbe(t, res, probe, func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			const init = "组队管控-初始化"
			const open = "组队管控-打开建房"
			for round := 0; round < 2; round++ {
				// Keep the production recognizer, action and hit cap. End the
				// child at the click so the replay isolates the shared hit count.
				d, err := ctx.RunTask(init, map[string]any{
					init: map[string]any{
						"custom_action_param": map[string]any{"role": "captain", "flow": "bond", "teammate_names": "涂壁", "limit_count": 1},
						"next":                []string{open},
					},
					open: map[string]any{"post_delay": 1},
				})
				if err != nil || d == nil || !d.Status.Success() || len(c.recorded()) != round+1 {
					t.Errorf("captain round %d: detail=%+v err=%v clicks=%v", round+1, d, err, c.recorded())
					return false
				}
				blocked, _ := ctx.RunTask(open, map[string]any{open: map[string]any{"timeout": 100, "post_delay": 1}})
				if blocked != nil && blocked.Status.Success() || len(c.recorded()) != round+1 {
					t.Errorf("extra same-round click: detail=%+v clicks=%v", blocked, c.recorded())
					return false
				}
			}
			return true
		})
		runPartyProbe(t, tasker, probe)
	})
}

func TestNativePartyAcceptedInvitationDisbandsAndRestoresOwnedBonus(t *testing.T) {
	const gi = "tasks/Component/GeneralInvite/gi/"
	const gb = "tasks/Component/GeneralBuff/gb/"
	accept := historicalPartyCrop(t, "local-workspace/tests/fixtures/native-invite-accept.png")
	prepareCrop := historicalPartyCrop(t, "tasks/Component/GeneralBattle/gb/gb_prepare_highlight.png")
	roomTitle := historicalPartyCrop(t, gi+"gi_gi_in_room.png")
	reject := historicalPartyCrop(t, gi+"gi_i_reject.png")
	bonusOn := historicalPartyCrop(t, gb+"gb_open_yellow.png")
	bonusOff := historicalPartyCrop(t, gb+"gb_close_red.png")
	panel := func(soul image.Image) image.Image {
		frame := blankPartyFrame()
		drawPartyCaption(t, frame, "觉醒副本掉落额外的觉醒材料", 430, 133)
		drawPartyCaption(t, frame, "八岐大蛇掉落额外的御魂材料", 430, 201)
		pastePartyCrop(frame, bonusOn, 725, 143)
		pastePartyCrop(frame, soul, 725, 211)
		return frame
	}
	onPanel, offPanel := panel(bonusOn), panel(bonusOff)
	c := &partyFrameController{frame: blankPartyFrame()}
	withPartyFrameReplay(t, c, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		images := filepath.Join(root, "resource_pack", "base", "image")
		court, err := loadPNG(filepath.Join(images, "导航",
			"导航-庭院-识别是否位于庭院-模板匹配_1.png"))
		if err != nil {
			t.Fatal(err)
		}
		bonusButton, err := loadPNG(filepath.Join(images, "通用图标", "加成.png"))
		if err != nil {
			t.Fatal(err)
		}
		home := drawReviewCrop(court, image.Pt(1200, 640))
		pastePartyCrop(home, bonusButton, 349, 32)
		invite := drawReviewCrop(court, image.Pt(1200, 640))
		pastePartyCrop(invite, accept, 90, 200)
		pastePartyCrop(invite, reject, 12, 226)
		draw.Draw(invite, image.Rect(12, 158, 300, 198), image.White, image.Point{}, draw.Src)
		draw.Draw(invite, image.Rect(30, 162, 110, 192), syntheticGhostFrame(t), image.Pt(145, 52), draw.Src)
		prepare := drawReviewCrop(prepareCrop, image.Pt(1128, 536))
		room := drawReviewCrop(roomTitle, image.Pt(92, 17))
		const probe = "Batch3PrepareWithoutRoom"
		var roomAuthorized, prepareAuthorized, cleared, bonusRestored bool
		registerPartyProbe(t, res, probe, func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			testHit := func(node string, frame image.Image) bool {
				d, err := ctx.RunRecognition(node, frame)
				return err == nil && d != nil && d.Hit
			}
			roomAuthorized = testHit("组队管控-加入房间", room)
			prepareAuthorized = testHit("组队管控-队员直接备战", prepare)
			return true
		})
		const after = "Batch3PrepareAfterDisband"
		registerPartyProbe(t, res, after, func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			d, err := ctx.RunRecognition("组队管控-加入房间", room)
			cleared = err == nil && d != nil && !d.Hit
			d, err = ctx.RunRecognition("组队管控-归位本轮加成", home)
			bonusRestored = err == nil && d != nil && !d.Hit
			actual, actErr := ctx.RunAction("组队管控-登记终态", maa.Rect{}, "")
			return actErr == nil && actual != nil && actual.Success
		})
		transitions := []struct {
			area  image.Rectangle
			frame image.Image
		}{
			{image.Rect(815, 204, 831, 236), onPanel},
			{image.Rect(1032, 239, 1110, 494), invite},
			{image.Rect(104, 210, 186, 308), prepare},
			{image.Rect(1115, 535, 1255, 630), home},
			{image.Rect(349, 32, 425, 98), onPanel},
			{image.Rect(815, 204, 831, 236), offPanel},
			{image.Rect(1032, 239, 1110, 494), home},
		}
		c.mu.Lock()
		c.frame = offPanel
		c.step = func(point image.Point) image.Image {
			i := len(c.clicks) - 1
			if i < len(transitions) && point.In(transitions[i].area) {
				return transitions[i].frame
			}
			return nil
		}
		c.mu.Unlock()
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask("组队管控-初始化", map[string]any{
			"组队管控-初始化": map[string]any{
				"custom_action_param": map[string]any{"role": "member", "captain_names": "涂壁", "limit_count": 1},
			},
			"组队管控-开启加成": map[string]any{"next": []string{"组队管控-加成入口"}},
			"组队管控-拨动加成": map[string]any{"custom_action_param": map[string]bool{"drop_soul": true, "drop_awake": true}},
			"组队管控-庭院等待": map[string]any{"timeout": 2500, "rate_limit": 30, "post_delay": 1},
			"组队管控-接邀判定": map[string]any{"post_delay": 1, "next": []string{probe}},
			probe:       map[string]any{"action": "Custom", "custom_action": probe},
			"组队管控-战斗等待": map[string]any{"timeout": 2500, "rate_limit": 30},
			"组队管控-准备战斗": map[string]any{"post_delay": 1},
			"组队管控-安全归庭": map[string]any{"timeout": 2500, "rate_limit": 30},
			"组队管控-已归庭":  map[string]any{"next": []string{after}},
			after:       map[string]any{"action": "Custom", "custom_action": after},
		}).Wait()
		out := collector.Outcome()
		if !job.Success() || !out.Reported || out.Status != runstate.Deferred || !partyMetricEquals(out, "task_reason", `"party_room_disbanded"`) ||
			!roomAuthorized || !prepareAuthorized || !cleared || !bonusRestored || len(c.recorded()) != len(transitions) {
			t.Fatalf("member invitation: job=%v outcome=%+v room=%t prepare=%t cleared=%t bonus restored=%t clicks=%v",
				job.Status(), out, roomAuthorized, prepareAuthorized, cleared, bonusRestored, c.recorded())
		}
		for i, click := range c.recorded() {
			if !click.In(transitions[i].area) {
				t.Errorf("click %d: %v outside %v", i, click, transitions[i].area)
			}
		}
	})
}

func TestNativePartyUnknownRecoveryNeverRestarts(t *testing.T) {
	c := &partyFrameController{frame: blankPartyFrame()}
	withPartyFrameReplay(t, c, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		const guard = "Batch3NoRestart"
		restarted := false
		registerPartyProbe(t, res, guard, func(*maa.Context, *maa.CustomActionArg) bool {
			restarted = true
			return false
		})
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask("组队管控-初始化", map[string]any{
			"组队管控-初始化": map[string]any{
				"custom_action_param": map[string]any{"role": "captain", "flow": "party", "teammate_names": "涂壁", "limit_count": 1},
				"next":                []string{"组队管控-失败归庭"},
			},
			"组队管控-安全归庭": map[string]any{"timeout": 300, "rate_limit": 25},
			"重启游戏":      map[string]any{"action": "Custom", "custom_action": guard},
		}).Wait()
		if out := collector.Outcome(); job.Success() || out.Status != runstate.Failed || !out.Reported ||
			restarted || len(c.recorded()) != 0 {
			t.Fatalf("unknown recovery: job=%v outcome=%+v restart=%t clicks=%v", job.Status(), out, restarted, c.recorded())
		}
	})
}

func TestNativePartyManualDoesNotCountDisabledChallenge(t *testing.T) {
	crop := historicalPartyCrop(t, "tasks/Component/GeneralInvite/gi/gi_fire_fail_sea.png")
	c := &partyFrameController{frame: drawReviewCrop(crop, image.Pt(1160, 585))}
	withPartyFrameReplay(t, c, func(root string, _ *maa.Resource, tasker *maa.Tasker) {
		_, tasks, err := loadTaskDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		task := tasks["组队刷本"]
		overrides := resolveTaskOverride(task, map[string]any{"挑战次数": 1, "最短时间": "0", "最长时间": "0"})
		overrides["组队刷本-房主-循环"] = map[string]any{"timeout": 350, "rate_limit": 25}
		overrides["组队管控-安全归庭"] = map[string]any{"timeout": 300, "rate_limit": 25}
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask(task.Entry, overrides).Wait()
		if out := collector.Outcome(); job.Success() || !out.Reported || out.Status != runstate.Failed || !partyMetricEquals(out, "party_battles", "0") ||
			len(c.recorded()) != 0 {
			t.Fatalf("disabled challenge: job=%v outcome=%+v clicks=%v", job.Status(), out, c.recorded())
		}
	})
}

func TestNativePartyFinalSettlementCancelsReinvite(t *testing.T) {
	const gi = "tasks/Component/GeneralInvite/gi/"
	const ui = "tasks/GlobalGame/ui/"
	toggle := historicalPartyCrop(t, ui+"ui_ui_check.png")
	confirm := historicalPartyCrop(t, ui+"ui_ui_confirm.png")
	roomTitle := historicalPartyCrop(t, gi+"gi_gi_in_room.png")
	victory := blankPartyFrame()
	drawPartyCaption(t, victory, "本次通关时间", 530, 575)
	prompt := blankPartyFrame()
	drawPartyCaption(t, prompt, "默认邀请队友", 440, 246)
	drawPartyCaption(t, prompt, "取消", 500, 420)
	pastePartyCrop(prompt, toggle, 564, 341)
	pastePartyCrop(prompt, confirm, 664, 407)
	// The settlement and popup can overlap on the first post-fight frame.
	pastePartyCrop(victory, prompt.SubImage(image.Rect(408, 232, 900, 490)), 408, 232)
	c := &partyFrameController{frame: victory}
	withPartyFrameReplay(t, c, func(root string, _ *maa.Resource, tasker *maa.Tasker) {
		court, err := loadPNG(filepath.Join(root, "resource_pack", "base", "image", "导航",
			"导航-庭院-识别是否位于庭院-模板匹配_1.png"))
		if err != nil {
			t.Fatal(err)
		}
		home := drawReviewCrop(court, image.Pt(100, 565))
		room := drawReviewCrop(roomTitle, image.Pt(92, 17))
		cancelArea := image.Rect(495, 413, 567, 469)
		backArea := image.Rect(19, 13, 77, 68)
		c.mu.Lock()
		c.step = func(point image.Point) image.Image {
			switch {
			case point.In(cancelArea):
				return room
			case point.In(backArea):
				return home
			}
			return nil
		}
		c.mu.Unlock()
		collector, release := runstate.Bind(tasker)
		defer release()
		job := tasker.PostTask("组队管控-初始化", map[string]any{
			"组队管控-初始化": map[string]any{
				"custom_action_param": map[string]any{"role": "captain", "teammate_names": "涂壁", "limit_count": 1},
				"next":                []string{"组队管控-开战记账"},
			},
			"组队管控-战斗等待": map[string]any{"timeout": 2000, "rate_limit": 30},
			"组队管控-战后去向": map[string]any{"timeout": 2500, "rate_limit": 30},
			"组队管控-安全归庭": map[string]any{"timeout": 2500, "rate_limit": 30},
		}).Wait()
		out, clicks := collector.Outcome(), c.recorded()
		if !job.Success() || out.Status != runstate.Completed || !out.Reported || !partyMetricEquals(out, "party_battles", "1") ||
			len(clicks) != 2 || !clicks[0].In(cancelArea) || !clicks[1].In(backArea) {
			t.Fatalf("final re-invite: job=%v outcome=%+v clicks=%v", job.Status(), out, clicks)
		}
	})
}

func partyMetricEquals(out runstate.Outcome, name, wantJSON string) bool {
	raw, ok := out.Metrics[name].(json.RawMessage)
	return ok && string(raw) == wantJSON
}

// Independent Microsoft YaHei glyph masks for text not present in the historic crops.
func drawPartyCaption(t *testing.T, frame draw.Image, text string, x, y int) {
	t.Helper()
	drawPartyLabel(t, frame, text, x, y, false)
}

func drawPartyLabel(t *testing.T, frame draw.Image, text string, x, y int, selected bool) {
	t.Helper()
	fixtures := map[string]struct {
		stride int
		mask   string
	}{
		"好友":       {10, "vc47CgIxEAbgPwlsCgPbbrdXiJ2dHiVH2FJwkRSWHsDOq0TsPUOOYLlFYMyMrgi24lTfME/gJ5EAFUUXmr6U4N/KRFRhi0qzTDRSVAeDl476Q5YmlptlWio2rlgLKi56Vqfh0sA6VW0CT9w1upB4zVC1FG01fC/PjFWtHLlphP75gUbecZ9Bo66ZrzXYq3NcS+MIV/N/xwM="},
		"跨区":       {10, "pdAxCsJAEAXQj4JGSGFpIWoQrGzsLNR4lK8R7ETIAbJHm4NYbG6QMoW47s5qZWemejD7Z4YFulT6o36bonKvoGTy1ci1jF2vMxba3Zgr7ipKYaxmhSeRoMw1rE2ILKsHSw2vkPAQ0PMDOFVlfvJMtffKVTuCpdF3R7AWlbOF2I/iXj+FN2yD1uAF47D3CcZLhw0Yrx8glyhgbjp915/1Bg=="},
		"最近":       {10, "Y2CgDDjAWQ8YeEBU/f8DCcx/oCyD+g8QRQckahigYgIJMBZDAtQIGAshVv//4If6/xCDGSEs9v///xgkJKCwGBobGKCs/0CWA4S1/4PBAYgsnPV/hgGclWDYAJHtOQBjPW8ohlj8/3HDTwaIHf0P74GtZ2f8U9v+H+wWxg0SjP//kRtUZAMA"},
		"默认邀请队友":   {23, "5dIxaxRBFAfw/947syu83C3YDHjcBgtbXxRiBJNbSGvpB1i59C5YiHiQOaawSaPVVfoV/AbZOGCa1NYr9wUUmwjCc3YuLIJfQHCYYvjx+PPezAD//CLshG0MIECGxEZUV9YEyc0QQr/+4GqfYDQfzXTd8yB3Mgm8lDQw9/zO5SMCL2feCgVW1Y6/ODMkZDo7L3Rten7qumawnGPDhE322VXkdWiQxFwz6dnPb+GoR8WJ/pBJzy4JvHh755pTvdo0CEmxMHdDGfWMnDrmbI/G6k1gu78JCWy39X3HPAVbifzGyg3LFRPD8x64qWNIaoUjd9X3wWXV8a3PVk4bPplGfgFTNUFTvWx2VyUf3Y4hc5jdyN+5eWgq9g86Xny1UsQrvLmqDkW23TN3T/2r80bGcconq/pxLZOidc/VJ7apisjHH0Vaczgt6RKebNW+7rIHx7VJmtFLCzqFH+ORb7vht3baCdl0rnrBhRvh4IOdBR6izBIMwqN8CpwhY2z9/WX+r/Ub"},
		"取消":       {9, "Y2CgGDgwAwn2/w0MD5lZQIz/P4qb+RgYGJvrf1gclgExDAQEki0gDAZlASDjv4BBA0gXkGF48P8fkJSEwUMoQ6GQgQXMSFCEMDgSGMGM/zwOjGA1/3kOKEIUsx2AKmZvSIQwmGGMxwyJEMXPG8AMdqCugyApdmaJBDCDgbnA4iADD4jxQOYg4x8Qw4HjIOMPysOECAAA"},
		"陌生人邀请你组队": {30, "7dMxaxsxFADgd35G54B8vmwKTeML7Q+42wwN1dgMha4dL+QPuGTx1BMYmg6F5CcJrjSGGtLRkCEHHbLKZHGp4fVJlyYOdOla+gYJ8cF7Tw8J4J8M5ZeeEAASuikIf8z8gj9pLVkFyK6IWBMFYkAD90gTEPozntFK5iAfVEnYVT6lyF5Hl6yaej2U+b2qoJi9cQ0fKlKzTdW0Zu3oS/eKVsp2ctay1VzdKWRvbUyr3Ir9j/dakBuHzJBNLG/FVFSstgy6T65h3QPQ3y6uaT1GWQ1I2nHQI1PYoMPnh6etikdqWJ/AUGwZC+AQunyjOy1M7if5ArDTNzUtz5+lkrVsdafVArC6CXqilJ/G5Lf6GxUGFUINRXqstJ/GolVyXo9aJZceS31GMp8F3SLnMzcWhxfGay1H3HMxD9pv69oS0y+2hjytBXDd4tOGRnaM+dzr9tRr//bDRs9oRlguyq/ktqcYzbB/Ewf9ETSG3TheZVes2BlyzwKDvguaQDI4PYdbrovwnusmkfEa0ZKIDiB+2gB8D5pVND+A5kEngAuiZR03qQZgHYELypn9+4yMJjdlfekLyqx9sn8O+Re/4X+E+AU="},
		"本次通关时间":   {23, "1dIxTsMwFAbg341JQAptRyNFLUfwmAHRHsUSF+gGQ6VYqtSJQz1kqWLgAGyNxAUYO0R92I7bwMwCXmx/kd57/hXgz67btBOgMfUHK8NNf+e6YVAZboqPia2G8myqrPPfzkwGJcao68BA7rFgPpjmmJc1pjqTIDqzbo6yIH9yF9z1DNFC+WYNJ3a8jfwJ7AIjQyiSOHuH2FYw7sQicv7qWa0TL09cvuEqV+u+SGfOvEOVoxy4r60c1ASlcXPmD6lPrJ3VP7gvYl5IX9tYxGZSJRZErbmJXHieJs4sPRn/TD9gTsPcxYLvSEcuzfDK0YIvyZjAahWCTZOAhG11ZfZYxbxdn6APfkP1PWaiHQ95By7s7NF3oWIj5994jMkD8z5kPLckusQSIzA/A8vY6vDrf+2fry8="},
		"八岐大蛇掉落额外的御魂材料": {36, "7dJBaxNBFAfw2Z3pZopb96URmZBqdkGiN3eNQsXWJFV6KiierIKMCD25YI89SCcEmi0pNhUEPfkVBL/AYkE85OBR6CWCFA+CGzwYJM36dlOxVI8ePPR/nPnx3mPmEfLfxuWHTyiB0Y0yNaIpShQasHVCBNdUyEJCJBqhKULsXyZ0re9xXLcGwDVNmfWIVP8w1bROJcJ6md6Q72W/xQ0CL5zUTKz20VjDAaeTtuuiYVk974Duvfw63PHQyNBP6hQ/G178cY1c9FQlYJN6OTEN4t499dsUzhmekjeGptWXATupy5F5dGds39A43rEJmilmfeHVgJ0wTAcoznPznnagTol48acLXCy8qz1hWePYXu6H01TvRTJztepXopFRD5xXM52g3mLZNeHAnNOU7Xg5MdIHl6KZuqTuq+7ZjWBrvBm9RfPG2fRA5NGEaGwaFkruZSVpRDfW8/w6zE878DpuURDjibF9IfVqcTfsxd3jp82H6xY/A0srDjyttXS4ijNryvV5aMhChniBdGl7JjA4g9LibLZ9K8fgCo1Sw5Rp03mGf7E5ON9JjZF3suJZjsPiY4XfL/wMES5dMlyuBYwvBOa+WdmiAB+01KxmiCtoySjHPc54LeQjU24Vt214jsYgpjYRSpPuYi+aGClSMxsZcG20Gyaq6eWQFktodGvA5wB3I2KFHjFhGxug4dhNFvAJyv0ux2mtDg+xTl9oBIoN2k137FD47TGV7DWaNH8zRznKv85P"},
		"觉醒副本掉落额外的觉醒材料": {36, "7dLPaxNBFAfwNzvT5LUO7qQRmWCtKYjedNcfoFhsK+KpYk/FHyAjBQ/FQ8WLCJKJkWRLg23x0lKw/4J/Qkqh9JCDJ2nwkBQ8Cm7owQphx5lt9eBd8NB3WZj98H0zvAfwnxaHAP8+oyDch2D6h+jUiKIHIJHoBo6Pp0YSDZTLUnvPbDjj/zCm7PcEEqKl2kxM5cBANRiQAgq/c8ZiHWC2m7BQTzWsWR2x5k1x4EwMhXXT6yEdLAaBNSznsRxMmfXvyZdQg28Sm2NW9RCEZrcKV0I9FrFBr2RilxM8Oq3tc7zSjiiv6mEItbqXcH9fReykpyCfWDP3sC81s+3YmgvODDH/G45H7ETGmml756nH5MA0XK+7ttfXSygntyfesVxqoKY/SXtnYuLZFticpg71zMjH0WZUXmC5qm8q09tZtWSeW6PFqxZAoVxvXNVPdOd8PVrur8VbHCqft/1QyII1b8WLtn17havrWtGY1ucLeFvc4cXKB7NAhexPzVyXmFaVF7umc3yYP5v38ax4ak0tOuaJmyQ1apfIVp3LMFIBXRqNMsjEOWuymGfiBo2B1ESp3ZWtFc7tLBZ7F5upyfBrlcS8R/Hgtc1ZzKsN22vZbAVIIoaTEU/Ny7UMUiF2XK81Z6Sb+2XTRYYTDUzNjjNFseLmnqelQxMgdUZJZ2DlVIKH+wMznjI/D/YHPb+Ht4TdjZhRmc+aTQicIeVENQi07VA6aG/rN7Fhc/a5v5cBSjsyndeffU4L7/dpt73q8DjtdVRH9Y/rFw=="},
	}
	spec := fixtures[text]
	z := flate.NewReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(spec.mask)))
	defer z.Close()
	mask, err := io.ReadAll(z)
	if err != nil || spec.stride == 0 || len(mask) != spec.stride*42 {
		t.Fatalf("invalid caption mask %q: %v", text, err)
	}
	var background image.Image = image.White
	if selected {
		background = image.NewUniform(color.RGBA{238, 202, 148, 255})
	}
	draw.Draw(frame, image.Rect(x, y, x+8*spec.stride, y+42), background, image.Point{}, draw.Src)
	for i, b := range mask {
		for bit := 0; bit < 8; bit++ {
			if b&(0x80>>bit) != 0 {
				frame.Set(x+8*(i%spec.stride)+bit, y+i/spec.stride, image.Black)
			}
		}
	}
}
