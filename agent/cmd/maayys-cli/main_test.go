package main

import (
	"bytes"
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maa-yys-agent/actions/switch_account"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/win32"
)

func testRepoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return abs
}

func TestCatalogLoadsTasksIncludingLogin(t *testing.T) {
	root := testRepoRoot(t)
	var buf bytes.Buffer
	if err := runCatalog(root, &buf); err != nil {
		t.Fatalf("runCatalog failed: %v", err)
	}

	var out CatalogOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal catalog output: %v", err)
	}
	if !out.OK || len(out.Tasks) < 10 {
		t.Fatalf("unexpected catalog output: ok=%v count=%d", out.OK, len(out.Tasks))
	}

	want := map[string]string{
		"账号登录":   "账号登录",
		"打开游戏":   "启动游戏",
		"关闭游戏":   "关闭阴阳师",
		"日常奖励领取": "日常-开始执行日常任务",
	}
	got := make(map[string]string, len(out.Tasks))
	for _, item := range out.Tasks {
		got[item.Name] = item.Entry
	}
	for name, entry := range want {
		if got[name] != entry {
			t.Errorf("task %q entry = %q, want %q", name, got[name], entry)
		}
	}
}

func TestJSONLWriterFormatAndSecretRedaction(t *testing.T) {
	var buf bytes.Buffer
	w := &jsonlWriter{out: &buf}
	w.addSecret("my$ecretP@ss")

	w.Log("info", "login test with my$ecretP@ss")
	w.Step("step-login", "running", "正在切换账号 my$ecretP@ss")
	w.Step("step-login", "complete", "已进入庭院")
	w.Result(true, false, "", false)

	raw := buf.String()
	if strings.Contains(raw, "my$ecretP@ss") {
		t.Fatalf("secret leaked in JSONL output: %s", raw)
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines, got %d: %s", len(lines), raw)
	}
	if !strings.Contains(lines[3], `"type":"result"`) || !strings.Contains(lines[3], `"error":null`) {
		t.Fatalf("unexpected result JSONL line: %s", lines[3])
	}
}

func TestOfflineResourceLoadingWithBaseAndOfficial(t *testing.T) {
	root := testRepoRoot(t)
	libDir := resolveMaaLibDir(root)
	if _, err := os.Stat(filepath.Join(libDir, "MaaFramework.dll")); err != nil {
		t.Skipf("MaaFramework runtime not present at %s: %v", libDir, err)
	}

	if err := initMaaFramework(root); err != nil {
		t.Fatalf("initMaaFramework failed: %v", err)
	}
	defer func() { _ = maa.Release() }()

	res, err := loadAndRegisterResource([]string{
		filepath.Join(root, "resource_pack", "base"),
		filepath.Join(root, "resource_pack", "official"),
	})
	if err != nil {
		t.Fatalf("loadAndRegisterResource failed: %v", err)
	}
	defer res.Destroy()

	if !res.Loaded() {
		t.Fatal("expected resource.Loaded() == true")
	}

	loginNode, err := res.GetNode("账号登录")
	if err != nil || loginNode == nil {
		t.Fatalf("GetNode(账号登录) failed: %v", err)
	}

	startNode, err := res.GetNode("启动游戏")
	if err != nil || startNode == nil || startNode.Action == nil {
		t.Fatalf("GetNode(启动游戏) failed: %v", err)
	}
	sp, ok := startNode.Action.Param.(*maa.StartAppParam)
	if !ok || sp.Package != "com.netease.onmyoji.wyzymnqsd_cps" {
		t.Fatalf("unexpected official StartApp package: %#v", startNode.Action.Param)
	}

	// Verify 账号登录 node has max_hit=1 and empty on_error, and fails immediately without a 3-minute retry loop.
	if loginNode.MaxHit == nil || *loginNode.MaxHit != 1 || len(loginNode.OnError) != 0 {
		t.Fatalf("unexpected 账号登录 retry config: max_hit=%v on_error=%v", loginNode.MaxHit, loginNode.OnError)
	}

	windows, err := maa.FindDesktopWindows()
	if err != nil || len(windows) == 0 {
		t.Skip("no desktop window available for offline Tasker OCR test")
	}
	ctrl, err := maa.NewWin32Controller(windows[0].Handle, win32.ScreencapGDI, win32.InputNone, win32.InputNone)
	if err != nil {
		t.Skipf("Win32 GDI controller unavailable in headless CI: %v", err)
	}
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Skip("Win32 GDI controller connect skipped")
	}

	tasker, err := maa.NewTasker()
	if err != nil {
		t.Fatalf("NewTasker failed: %v", err)
	}
	defer tasker.Destroy()
	if err := tasker.BindResource(res); err != nil {
		t.Fatalf("BindResource failed: %v", err)
	}
	if err := tasker.BindController(ctrl); err != nil {
		t.Fatalf("BindController failed: %v", err)
	}

	start := time.Now()
	job := tasker.PostTask("账号登录", map[string]any{
		"账号登录": map[string]any{
			"custom_action_param": map[string]any{
				"email":    "",
				"platform": "android",
				"server":   "全平台互通",
			},
		},
	}).Wait()
	elapsed := time.Since(start)
	if !job.Failure() {
		t.Fatalf("expected immediate failure on empty email, got status=%v", job.Status())
	}
	if elapsed > 3*time.Second {
		t.Fatalf("账号登录 failure entered retry loop (elapsed=%v, want <3s)", elapsed)
	}

	// If live screenshots exist locally, verify exact OCR ROIs for engine update, session expired, and 6-char server name.
	engineShot := `D:\projs\game-desk-next\.data\screenshots\e63baece-ada4-462c-8db4-ef014478b0cc.png`
	if img, err := loadPNG(engineShot); err == nil {
		reco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{360, 340, 560, 170}),
			Expected: []string{"暂不更新|以后再说|暂不升级"},
		}, img).Wait()
		detail, _ := reco.GetDetail()
		if detail == nil || len(detail.NodeDetails) == 0 || detail.NodeDetails[0].Recognition == nil || !detail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 暂不更新 on live engine-update screenshot")
		}
		box := detail.NodeDetails[0].Recognition.Box
		if box.X()+box.Width()/2 >= 640 {
			t.Fatalf("暂不更新 box %v points to right side (后台下载)!", box)
		}

		svrReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI: maa.NewTargetRect(maa.Rect{460, 495, 255, 52}),
		}, img).Wait()
		svrDetail, _ := svrReco.GetDetail()
		matchedSvr := false
		if svrDetail != nil && len(svrDetail.NodeDetails) > 0 && svrDetail.NodeDetails[0].Recognition != nil && svrDetail.NodeDetails[0].Recognition.Results != nil {
			for _, item := range svrDetail.NodeDetails[0].Recognition.Results.All {
				if ocr, ok := item.AsOCR(); ok && switch_account.MatchServer(ocr.Text, "永远的平安京") {
					matchedSvr = true
					break
				}
			}
		}
		if !matchedSvr {
			t.Fatalf("failed to match 6-char server name 永远的平安京 in widened ROI {460, 495, 255, 52}: %+v", svrDetail)
		}
	}

	expiredShot := `D:\projs\game-desk-next\.data\screenshots\6b18c593-2377-4283-8c4d-e439dc81ec35.png`
	if img, err := loadPNG(expiredShot); err == nil {
		reco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{450, 360, 380, 130}),
			Expected: []string{"^重新登录$"},
		}, img).Wait()
		detail, _ := reco.GetDetail()
		if detail == nil || len(detail.NodeDetails) == 0 || detail.NodeDetails[0].Recognition == nil || !detail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize ^重新登录$ on live session-expired screenshot")
		}
		pwdReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{380, 200, 520, 290}),
			Expected: []string{"请输入密码|输入密码|密码登录|请输入邮箱|获取验证码"},
		}, img).Wait()
		pwdDetail, _ := pwdReco.GetDetail()
		if pwdDetail != nil && len(pwdDetail.NodeDetails) > 0 && pwdDetail.NodeDetails[0].Recognition != nil && pwdDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("session-expired popup falsely matched password prompt OCR!")
		}
	}
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
