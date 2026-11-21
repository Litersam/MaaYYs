package switch_account

import (
	"errors"
	"fmt"
	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"strings"
	"time"
)

func (a *SwitchAccount) ensureSurface(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook) (inGame bool, outFrame *Frame, coldStarted bool, cgTaps int, err error) {
	startPkg := "com.netease.onmyoji.wyzymnqsd_cps"
	if node, nErr := ctx.GetNode("启动游戏"); nErr == nil && node != nil && node.Action != nil {
		if sp, ok := node.Action.Param.(*maa.StartAppParam); ok && strings.TrimSpace(sp.Package) != "" {
			startPkg = strings.TrimSpace(sp.Package)
		}
	}
	pkgPrefix := strings.Split(startPkg, "/")[0]

	focused := false
	if job := ctrl.PostShell("dumpsys window | grep -E 'mCurrentFocus|mFocusedApp'", 4*time.Second); job.Wait().Success() {
		if out, sErr := ctrl.GetShellOutput(); sErr == nil && strings.Contains(out, pkgPrefix) {
			focused = true
		}
	}
	if !focused {
		coldStarted = true
		a.detail(hook, "正在启动游戏")
		a.logf(hook, "info", "", "游戏未在前台，调用 StartApp (%s)", startPkg)
		if !ctrl.PostStartApp(startPkg).Wait().Success() {
			return false, nil, true, 0, fmt.Errorf("StartApp 启动失败: %s", startPkg)
		}
		time.Sleep(900 * time.Millisecond)
	} else {
		a.detail(hook, "正在识别当前页面")
	}

	surfaceStart := time.Now()
	var lastCGTap time.Time
	idleFrames := 0

	deadline := time.Now().Add(135 * time.Second)
	for time.Now().Before(deadline) {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return false, nil, coldStarted, cgTaps, err
		}

		// Always clear foreground blocking modals BEFORE checking background login surface,
		// because bottom-left CADPA 8+ badge can still match >= 0.8 behind semi-transparent overlays.
		if a.dismissStartupPopups(ctx, ctrl, f) || (!a.hasNeteaseAccountDialog(ctx, f) && a.acceptAgreementModal(ctx, ctrl, f)) {
			idleFrames = 0
			time.Sleep(postClickSettle)
			continue
		}

		if a.isLoginSurface(ctx, f) {
			return false, f, coldStarted, cgTaps, nil
		}
		if a.isCourtyardOrProfile(ctx, f) {
			return true, f, coldStarted, cgTaps, nil
		}

		// Return from an inner game page back toward courtyard when game was already open.
		if !coldStarted && (a.clickRecognizedNode(ctx, ctrl, f.Image, "导航-庭院-点击返回按钮-模板匹配") ||
			a.clickRecognizedNode(ctx, ctrl, f.Image, "导航-庭院-点击快捷返回庭院按钮")) {
			idleFrames = 0
			time.Sleep(350 * time.Millisecond)
			continue
		}

		// Fast opening CG skip:
		// 1) Click explicit top-right "跳过" button if visible.
		if a.clickOCRExact(ctx, ctrl, f, maa.Rect{1040, 10, 230, 110}, "^跳过$|跳过动画|点击跳过", 0) {
			cgTaps++
			lastCGTap = time.Now()
			time.Sleep(postClickSettle)
			continue
		}
		// 2) Do not tap while bottom patch extraction/downloading text is actively shown.
		if f.FindText(maa.Rect{240, 560, 800, 155}, cachedRegexp("正在解压|资源更新|正在下载|版本检查")) != nil {
			time.Sleep(fastPollInterval)
			continue
		}
		// 3) Once past brief NetEase splash (~3.2s from cold StartApp, or >=2 idle frames), tap safe blank
		// coordinate (260, 140) — strictly clear of bottom-left 8+ badge, top-left return/avatar, right toolbar,
		// top-right close icons, central modals, and bottom server/enter bar.
		idleFrames++
		minCGWait := 3000 * time.Millisecond
		if coldStarted {
			minCGWait = 2300 * time.Millisecond
		}
		if (coldStarted || idleFrames >= 2) && time.Since(surfaceStart) >= minCGWait && time.Since(lastCGTap) >= 1600*time.Millisecond {
			cgTaps++
			a.detail(hook, "正在跳过开场动画")
			a.logf(hook, "info", "", "轻触安全空白区 (260, 140) 快速跳过开场动画 (第%d次)", cgTaps)
			ctrl.PostClick(260, 140).Wait()
			lastCGTap = time.Now()
			time.Sleep(300 * time.Millisecond)
			continue
		}
		time.Sleep(fastPollInterval)
	}
	return false, nil, coldStarted, cgTaps, errors.New("等待登录页或庭院超时")
}

func (a *SwitchAccount) exitCourtyardToLogin(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook) (*Frame, error) {
	a.detail(hook, "已在游戏内，正在切换账号退回登录页")
	deadline := time.Now().Add(45 * time.Second)
	var lastAvatarClick time.Time
	for time.Now().Before(deadline) {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return nil, err
		}
		if a.hasTpl(ctx, f.Image, tplNeteaseLogo, maa.Rect{500, 170, 300, 90}, 0.8) ||
			a.hasTpl(ctx, f.Image, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) {
			return f, nil
		}
		if a.clickTpl(ctx, ctrl, f.Image, tplSwitchAccountBtn, maa.Rect{930, 170, 160, 75}, 0.8, postClickSettle) ||
			a.clickOCRExact(ctx, ctrl, f, maa.Rect{880, 150, 240, 110}, "切换账号|切换用户", postClickSettle) {
			if nextF, ok, wErr := a.waitUntil(ctx, ctrl, 1400*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasTpl(ctx, cf.Image, tplNeteaseLogo, maa.Rect{500, 170, 300, 90}, 0.8) ||
					a.hasTpl(ctx, cf.Image, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8)
			}); wErr != nil {
				return nil, wErr
			} else if ok {
				return nextF, nil
			}
			continue
		}
		if a.clickTpl(ctx, ctrl, f.Image, tplUserCenterBtn, maa.Rect{190, 390, 330, 200}, 0.8, postClickSettle) ||
			(a.hasTpl(ctx, f.Image, tplUserCenterProfile, maa.Rect{590, 60, 90, 55}, 0.8) &&
				a.clickOCRExact(ctx, ctrl, f, maa.Rect{150, 350, 400, 260}, "用户中心", postClickSettle)) {
			_, _, err := a.waitUntil(ctx, ctrl, 1500*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasTpl(ctx, cf.Image, tplSwitchAccountBtn, maa.Rect{930, 170, 160, 75}, 0.8) ||
					cf.FindText(maa.Rect{880, 150, 240, 110}, cachedRegexp("切换账号|切换用户")) != nil ||
					a.hasNeteaseAccountDialog(ctx, cf)
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if a.dismissEntryPopups(ctx, ctrl, f) {
			time.Sleep(postClickSettle)
			continue
		}
		if time.Since(lastAvatarClick) >= 1300*time.Millisecond {
			clickRect(ctrl, maa.Rect{35, 35, 55, 55})
			lastAvatarClick = time.Now()
		}
		time.Sleep(fastPollInterval)
	}
	return nil, errors.New("从庭院退出至登录选号页超时")
}
