package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

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
	if !out.OK || len(out.Tasks) < 40 {
		t.Fatalf("unexpected catalog output: ok=%v count=%d", out.OK, len(out.Tasks))
	}

	type wantMeta struct {
		entry    string
		category string
		file     string
	}
	want := map[string]wantMeta{
		"账号登录":    {"账号登录", "standalone", "tasks/standalone/账号登录.json"},
		"打开游戏":    {"启动游戏", "daily", "tasks/daily/打开游戏.json"},
		"关闭游戏":    {"关闭阴阳师", "daily", "tasks/daily/关闭游戏.json"},
		"日常奖励领取":  {"日常-开始执行日常任务", "daily", "tasks/daily/日常奖励领取.json"},
		"自动逢魔":    {"开始逢魔-检查时间", "standalone", "tasks/standalone/逢魔.json"},
		"契灵结契":    {"契灵结契-开始", "standalone", "tasks/standalone/契灵结契.json"},
		"组队刷本":    {"组队刷本-开局", "standalone", "tasks/standalone/组队刷本.json"},
		"超鬼王低星清票": {"超鬼王", "promotion", "tasks/promotion/超鬼王.json"},
	}

	got := make(map[string]CatalogTask, len(out.Tasks))
	validCategories := map[string]bool{"daily": true, "standalone": true, "promotion": true}
	for _, item := range out.Tasks {
		got[item.Name] = item
		if !validCategories[item.Category] {
			t.Errorf("task %q has unexpected category %q (file=%s)", item.Name, item.Category, item.File)
		}
		expectedPrefix := "tasks/" + item.Category + "/"
		if !strings.HasPrefix(item.File, expectedPrefix) {
			t.Errorf("orphan or miscategorized task %q: file=%q want prefix %q", item.Name, item.File, expectedPrefix)
		}
	}
	for name, w := range want {
		item, ok := got[name]
		if !ok {
			t.Errorf("missing expected task %q in catalog", name)
			continue
		}
		if item.Entry != w.entry || item.Category != w.category || item.File != w.file {
			t.Errorf("task %q = (entry=%q, category=%q, file=%q), want (%q, %q, %q)",
				name, item.Entry, item.Category, item.File, w.entry, w.category, w.file)
		}
	}

	// Ensure interface.json imports exist on disk and contain no root orphan tasks/*.json paths.
	ifaceRaw, err := os.ReadFile(filepath.Join(root, "interface.json"))
	if err != nil {
		t.Fatalf("read interface.json: %v", err)
	}
	var iface struct {
		Import []string `json:"import"`
	}
	if err := json.Unmarshal(ifaceRaw, &iface); err != nil {
		t.Fatalf("parse interface.json: %v", err)
	}
	foundLoginImport := false
	for _, imp := range iface.Import {
		if imp == "tasks/standalone/账号登录.json" {
			foundLoginImport = true
		}
		if strings.HasPrefix(imp, "tasks/") && strings.Count(imp, "/") < 2 {
			t.Errorf("interface.json still imports root orphan task path %q", imp)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(imp))); err != nil {
			t.Errorf("interface.json import %q missing on disk: %v", imp, err)
		}
	}
	if !foundLoginImport {
		t.Error("interface.json missing tasks/standalone/账号登录.json import")
	}
}

func TestV413NestedOptionsAndBackwardCompatLookup(t *testing.T) {
	root := testRepoRoot(t)
	_, byName, err := loadTaskDefinitions(root)
	if err != nil {
		t.Fatalf("loadTaskDefinitions failed: %v", err)
	}

	// Backward-compatible bare task name lookup + category/name lookup.
	loginBare, ok := lookupTask(byName, "账号登录")
	if !ok || loginBare.Category != "standalone" {
		t.Fatalf("bare lookup 账号登录 failed: ok=%v task=%+v", ok, loginBare)
	}
	loginQual, ok := lookupTask(byName, "standalone/账号登录")
	if !ok || loginQual.Name != "账号登录" {
		t.Fatalf("qualified lookup standalone/账号登录 failed: ok=%v task=%+v", ok, loginQual)
	}
	if _, ok := lookupTask(byName, "promotion/账号登录"); ok {
		t.Fatal("mismatched category lookup promotion/账号登录 should fail")
	}

	// CLI flag normalization (--catalog / catalog, --check-resources / check-resources).
	for raw, want := range map[string]string{
		"catalog":           "catalog",
		"--catalog":         "catalog",
		"-catalog":          "catalog",
		"check-resources":   "check-resources",
		"--check-resources": "check-resources",
		"run":               "run",
		"--run":             "run",
	} {
		if got := normalizeSubcommand(raw); got != want {
			t.Errorf("normalizeSubcommand(%q) = %q, want %q", raw, got, want)
		}
	}

	// Nested conditional options in 逢魔.json (SwitchSoul vs SwitchSoulByWeek).
	fengmo, ok := lookupTask(byName, "自动逢魔")
	if !ok {
		t.Fatal("missing 自动逢魔 task")
	}
	defOv := resolveTaskOverride(fengmo, nil)
	presetNode, _ := defOv["现世逢魔_装备式神预设"].(map[string]any)
	if presetNode["custom_action"] != "SwitchSoul" {
		t.Fatalf("default 自动逢魔 custom_action = %v, want SwitchSoul", presetNode["custom_action"])
	}

	weekOv := resolveTaskOverride(fengmo, map[string]any{
		"逢魔-每日不同分组":    "Yes",
		"default_group": "逢魔组",
		"monday_group":  "歌伎组",
	})
	weekNode, _ := weekOv["现世逢魔_装备式神预设"].(map[string]any)
	if weekNode["custom_action"] != "SwitchSoulByWeek" {
		t.Fatalf("weekly 自动逢魔 custom_action = %v, want SwitchSoulByWeek", weekNode["custom_action"])
	}
	weekParam, _ := weekNode["custom_action_param"].(map[string]any)
	monCfg, _ := weekParam["monday"].(map[string]any)
	if monCfg["group_name"] != "歌伎组" {
		t.Fatalf("weekly monday.group_name = %v, want 歌伎组", monCfg["group_name"])
	}

	disabledOv := resolveTaskOverride(fengmo, map[string]any{
		"逢魔-切换御魂阵容": false,
	})
	if _, exists := disabledOv["现世逢魔_装备式神预设"]; exists {
		t.Fatalf("disabled 逢魔-切换御魂阵容 should not evaluate nested soul preset option: %+v", disabledOv)
	}

	// Typed int placeholder coercion & 2-level nested switches in 契灵结契.json.
	qiling, ok := lookupTask(byName, "契灵结契")
	if !ok {
		t.Fatal("missing 契灵结契 task")
	}
	qlOv := resolveTaskOverride(qiling, map[string]any{
		"挑战次数":            25,
		"契灵结契-自定义点击策略设置": "Yes",
		"short_max":       15,
	})
	cntNode, _ := qlOv["契灵结契-计数"].(map[string]any)
	cntParam, _ := cntNode["custom_recognition_param"].(map[string]any)
	if cntParam["target_count"] != 25 {
		t.Fatalf("target_count = %#v (%T), want int 25", cntParam["target_count"], cntParam["target_count"])
	}
	waitNode, _ := qlOv["契灵结契-先等待"].(map[string]any)
	waitParam, _ := waitNode["custom_action_param"].(map[string]any)
	if waitParam["short_wait_max"] != 15 {
		t.Fatalf("short_wait_max = %#v (%T), want int 15", waitParam["short_wait_max"], waitParam["short_wait_max"])
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
	var safeHandle unsafe.Pointer
	for _, w := range windows {
		sig := strings.ToLower(w.WindowName + " " + w.ClassName)
		if strings.Contains(sig, "mumu") || strings.Contains(sig, "nemu") {
			continue
		}
		if w.Handle != nil {
			safeHandle = w.Handle
			break
		}
	}
	if err != nil || safeHandle == nil {
		t.Skip("no non-emulator desktop window available for offline Tasker OCR test")
	}
	ctrl, err := maa.NewWin32Controller(safeHandle, win32.ScreencapGDI, win32.InputNone, win32.InputNone)
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

		tFull := time.Now()
		fullReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI: maa.NewTargetRect(maa.Rect{0, 0, 1280, 720}),
		}, img).Wait()
		fullDur := time.Since(tFull)
		fullDetail, _ := fullReco.GetDetail()
		hitSkipInFull := false
		if fullDetail != nil && len(fullDetail.NodeDetails) > 0 && fullDetail.NodeDetails[0].Recognition != nil && fullDetail.NodeDetails[0].Recognition.Results != nil {
			for _, item := range fullDetail.NodeDetails[0].Recognition.Results.All {
				if ocr, ok := item.AsOCR(); ok && strings.Contains(ocr.Text, "暂不更新") {
					cx := ocr.Box.X() + ocr.Box.Width()/2
					if cx < 640 {
						hitSkipInFull = true
						break
					}
				}
			}
		}
		if !hitSkipInFull {
			t.Fatal("single-pass full-frame OCR {0,0,1280,720} failed to isolate left 暂不更新 button on after_cg_tap_4.png")
		}
		t.Logf("single-pass full-frame OCR latency on login modal frame: %v", fullDur)
	}

	if img, err := loadPNG(cgShot); err == nil {
		tCG := time.Now()
		cgFull := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI: maa.NewTargetRect(maa.Rect{0, 0, 1280, 720}),
		}, img).Wait()
		t.Logf("single-pass full-frame OCR latency on textless CG frame: %v (status=%v)", time.Since(tCG), cgFull.Status())
	}

	secShot := `D:\projs\game-desk-next\.data\screenshots\3adfdcf4-2301-461a-84ef-c06f7cb5e8dd.png`
	if img, err := loadPNG(secShot); err == nil {
		// Warm up once, then measure single-pass full-frame detection of 登录安全验证 / 账号存在安全风险.
		_ = tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{ROI: maa.NewTargetRect(maa.Rect{0, 0, 1280, 720})}, img).Wait()
		tSec := time.Now()
		secReco := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
			ROI: maa.NewTargetRect(maa.Rect{0, 0, 1280, 720}),
		}, img).Wait()
		secDur := time.Since(tSec)
		secDetail, _ := secReco.GetDetail()
		hitTitle, hitBody := false, false
		if secDetail != nil && len(secDetail.NodeDetails) > 0 && secDetail.NodeDetails[0].Recognition != nil && secDetail.NodeDetails[0].Recognition.Results != nil {
			for _, item := range secDetail.NodeDetails[0].Recognition.Results.All {
				if ocr, ok := item.AsOCR(); ok {
					cx := ocr.Box.X() + ocr.Box.Width()/2
					cy := ocr.Box.Y() + ocr.Box.Height()/2
					if strings.Contains(ocr.Text, "安全验证") && cx >= 420 && cx <= 860 && cy >= 15 && cy <= 110 {
						hitTitle = true
					}
					if strings.Contains(ocr.Text, "安全风险") && cx >= 90 && cx <= 1190 && cy >= 260 && cy <= 550 {
						hitBody = true
					}
				}
			}
		}
		if !hitTitle || !hitBody {
			t.Fatalf("failed to detect security verify page via full-frame OCR (hitTitle=%v, hitBody=%v)", hitTitle, hitBody)
		}
		t.Logf("security-verify page full-frame OCR latency: %v (hitTitle=%v, hitBody=%v)", secDur, hitTitle, hitBody)
	}

	if dir := strings.TrimSpace(os.Getenv("MAAYYS_BENCH_IMAGES_DIR")); dir != "" {
		entries, _ := os.ReadDir(dir)
		for _, ent := range entries {
			if !strings.HasSuffix(strings.ToLower(ent.Name()), ".png") {
				continue
			}
			img, err := loadPNG(filepath.Join(dir, ent.Name()))
			if err != nil {
				continue
			}
			t0 := time.Now()
			r := tasker.PostRecognition(maa.RecognitionTypeOCR, &maa.OCRParam{
				ROI: maa.NewTargetRect(maa.Rect{0, 0, 1280, 720}),
			}, img).Wait()
			d1 := time.Since(t0)
			var texts []string
			if det, _ := r.GetDetail(); det != nil && len(det.NodeDetails) > 0 && det.NodeDetails[0].Recognition != nil && det.NodeDetails[0].Recognition.Results != nil {
				for _, item := range det.NodeDetails[0].Recognition.Results.All {
					if ocr, ok := item.AsOCR(); ok {
						texts = append(texts, fmt.Sprintf("%s@(%d,%d)", ocr.Text, ocr.Box.X()+ocr.Box.Width()/2, ocr.Box.Y()+ocr.Box.Height()/2))
					}
				}
			}
			t.Logf("bench %s -> 1x full OCR=%v (%d boxes): %s", ent.Name()[:12], d1, len(texts), strings.Join(texts, " | "))
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
