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

	chooserShot := `D:\projs\game-desk-next\.data\screenshots\d0a724d1-0f75-495c-ad19-0aa918caf010.png`
	if img, err := loadPNG(chooserShot); err == nil {
		tilesReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{360, 290, 560, 190}),
			Expected: []string{"手机账号|扫码登录|快速游戏"},
		}, img).Wait()
		tilesDetail, _ := tilesReco.GetDetail()
		if tilesDetail == nil || len(tilesDetail.NodeDetails) == 0 || tilesDetail.NodeDetails[0].Recognition == nil || !tilesDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 4-tile login chooser in ROI {360, 290, 560, 190}")
		}

		emailTileReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{370, 365, 280, 105}),
			Expected: []string{"网易邮箱"},
		}, img).Wait()
		emailTileDetail, _ := emailTileReco.GetDetail()
		if emailTileDetail == nil || len(emailTileDetail.NodeDetails) == 0 || emailTileDetail.NodeDetails[0].Recognition == nil || !emailTileDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 网易邮箱 button in ROI {370, 365, 280, 105}")
		}
		eb := emailTileDetail.NodeDetails[0].Recognition.Box
		cx, cy := eb.X()+eb.Width()/2, eb.Y()+eb.Height()/2
		if cx < 398 || cx > 622 || cy < 390 || cy > 452 {
			t.Fatalf("网易邮箱 click center (%d, %d) outside button [398, 390, 224, 62] (box=%v)", cx, cy, eb)
		}

		tipReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{320, 545, 360, 85}),
			Expected: []string{"请先勾选同意"},
		}, img).Wait()
		tipDetail, _ := tipReco.GetDetail()
		if tipDetail == nil || len(tipDetail.NodeDetails) == 0 || tipDetail.NodeDetails[0].Recognition == nil || !tipDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 请先勾选同意 tooltip in ROI {320, 545, 360, 85}")
		}

		agreeReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{380, 410, 520, 175}),
			Expected: []string{"我已详细阅读|我已阅读|阅读并同意"},
		}, img).Wait()
		agreeDetail, _ := agreeReco.GetDetail()
		if agreeDetail == nil || len(agreeDetail.NodeDetails) == 0 || agreeDetail.NodeDetails[0].Recognition == nil || !agreeDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 我已详细阅读 in ROI {380, 410, 520, 175}")
		}
		ab := agreeDetail.NodeDetails[0].Recognition.Box
		chkX, chkY := ab.X()-16, ab.Y()+ab.Height()/2
		if chkX < 450 || chkX > 478 || chkY < 530 || chkY > 560 {
			t.Fatalf("computed checkbox click (%d, %d) missed [452, 533, 24, 24] (agreeBox=%v)", chkX, chkY, ab)
		}
	}

	mailFormShot := `C:\Users\liter\.copilot\session-state\58469ce6-1f77-483d-ba40-c5eb660c2ede\files\netease_mail_form.png`
	if img, err := loadPNG(mailFormShot); err == nil {
		pwdReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{375, 210, 530, 335}),
			Expected: []string{"请输入.*密码|输入.*密码|登录密码|密码登录|请输入.*邮箱|邮箱账号|获取验证码|立即注册|忘记密码"},
		}, img).Wait()
		pwdDetail, _ := pwdReco.GetDetail()
		if pwdDetail == nil || len(pwdDetail.NodeDetails) == 0 || pwdDetail.NodeDetails[0].Recognition == nil || !pwdDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize NetEase mail form as password prompt")
		}
	}

	wmcharShot := `C:\Users\liter\.copilot\session-state\58469ce6-1f77-483d-ba40-c5eb660c2ede\files\wmchar_test3.png`
	if img, err := loadPNG(wmcharShot); err == nil {
		emailReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI: maa.NewTargetRect(maa.Rect{380, 225, 510, 70}),
		}, img).Wait()
		emailDetail, _ := emailReco.GetDetail()
		foundTyped := false
		if emailDetail != nil && len(emailDetail.NodeDetails) > 0 && emailDetail.NodeDetails[0].Recognition != nil && emailDetail.NodeDetails[0].Recognition.Results != nil {
			for _, item := range emailDetail.NodeDetails[0].Recognition.Results.All {
				if ocr, ok := item.AsOCR(); ok && switch_account.MatchExactEmail(ocr.Text, "caborder_check_123@163.com") {
					foundTyped = true
					break
				}
			}
		}
		if !foundTyped {
			t.Fatal("failed to read full typed email inside ROI {380, 225, 510, 70} on wmchar_test3.png")
		}
	}

	truncShot := `C:\Users\liter\.copilot\session-state\58469ce6-1f77-483d-ba40-c5eb660c2ede\files\check_mumu5_clean.png`
	if img, err := loadPNG(truncShot); err == nil {
		selReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI: maa.NewTargetRect(maa.Rect{460, 280, 370, 50}),
		}, img).Wait()
		selDetail, _ := selReco.GetDetail()
		matchedTrunc := false
		if selDetail != nil && len(selDetail.NodeDetails) > 0 && selDetail.NodeDetails[0].Recognition != nil && selDetail.NodeDetails[0].Recognition.Results != nil {
			for _, item := range selDetail.NodeDetails[0].Recognition.Results.All {
				if ocr, ok := item.AsOCR(); ok && switch_account.MatchEmail(ocr.Text, "lishennanus300@163.com") {
					matchedTrunc = true
					break
				}
			}
		}
		if !matchedTrunc {
			t.Fatal("failed to match NetEase truncated email lishennanus300@163.c... in stored account ROI {460, 280, 370, 50}")
		}
	}

	agreeModalShot := `D:\projs\game-desk-next\.data\screenshots\13ab5319-940a-4590-8f2e-866fca85e783.png`
	if img, err := loadPNG(agreeModalShot); err == nil {
		btnReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{420, 340, 470, 200}),
			Expected: []string{"^(同意|同意并继续|同意并登录)$"},
		}, img).Wait()
		btnDetail, _ := btnReco.GetDetail()
		if btnDetail == nil || len(btnDetail.NodeDetails) == 0 || btnDetail.NodeDetails[0].Recognition == nil || !btnDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 同意并登录 in ROI {420, 340, 470, 200}")
		}
		bb := btnDetail.NodeDetails[0].Recognition.Box
		bx, by := bb.X()+bb.Width()/2, bb.Y()+bb.Height()/2
		if bx < 658 || bx > 864 || by < 456 || by > 518 {
			t.Fatalf("同意并登录 click center (%d, %d) outside button [658..864, 456..518] (box=%v)", bx, by, bb)
		}

		titleReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{450, 170, 380, 95}),
			Expected: []string{"用户协议及隐私保护"},
		}, img).Wait()
		titleDetail, _ := titleReco.GetDetail()
		if titleDetail == nil || len(titleDetail.NodeDetails) == 0 || titleDetail.NodeDetails[0].Recognition == nil || !titleDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 用户协议及隐私保护 title in ROI {450, 170, 380, 95}")
		}
	}

	cgShot := `D:\projs\game-desk-next\.data\screenshots\6fa1527b-2d6b-4f7e-9126-dc4b1cbc19ef.png`
	if img, err := loadPNG(cgShot); err == nil {
		cadpaReco := tasker.PostRecognition(maa.RecognitionTypeTemplateMatch, &maa.TemplateMatchParam{
			Template:  []string{"账号登录/check_login_form.png"},
			ROI:       maa.NewTargetRect(maa.Rect{1, 540, 250, 120}),
			Threshold: []float64{0.8},
		}, img).Wait()
		cadpaDetail, _ := cadpaReco.GetDetail()
		if cadpaDetail != nil && len(cadpaDetail.NodeDetails) > 0 && cadpaDetail.NodeDetails[0].Recognition != nil && cadpaDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("cold-start opening CG falsely matched check_login_form.png!")
		}
	}

	afterCGShot := `C:\Users\liter\.copilot\session-state\58469ce6-1f77-483d-ba40-c5eb660c2ede\files\after_cg_tap_4.png`
	if img, err := loadPNG(afterCGShot); err == nil {
		cadpaAfter := tasker.PostRecognition(maa.RecognitionTypeTemplateMatch, &maa.TemplateMatchParam{
			Template:  []string{"账号登录/check_login_form.png"},
			ROI:       maa.NewTargetRect(maa.Rect{1, 540, 250, 120}),
			Threshold: []float64{0.8},
		}, img).Wait()
		cadpaAfterDetail, _ := cadpaAfter.GetDetail()
		if cadpaAfterDetail == nil || len(cadpaAfterDetail.NodeDetails) == 0 || cadpaAfterDetail.NodeDetails[0].Recognition == nil || !cadpaAfterDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("expected check_login_form.png to match on after_cg_tap_4.png")
		}

		skipReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI:      maa.NewTargetRect(maa.Rect{360, 340, 560, 170}),
			Expected: []string{"暂不更新|以后再说|暂不升级"},
		}, img).Wait()
		skipDetail, _ := skipReco.GetDetail()
		if skipDetail == nil || len(skipDetail.NodeDetails) == 0 || skipDetail.NodeDetails[0].Recognition == nil || !skipDetail.NodeDetails[0].Recognition.Hit {
			t.Fatal("failed to recognize 暂不更新 after CG skip tap on after_cg_tap_4.png")
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
