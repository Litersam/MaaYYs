package switch_account

import (
	"errors"
	"fmt"
	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"maa-yys-agent/internal/vision"
	"strings"
	"time"
)

func (a *SwitchAccount) verifyAndSwitchServer(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params, initial *Frame) (*Frame, error) {
	a.detail(hook, "正在核验区服")
	var loginF *Frame
	reuse := initial
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		var f *Frame
		var err error
		if reuse != nil {
			f, reuse = reuse, nil
		} else {
			f, err = a.snapFrame(ctx, ctrl)
			if err != nil {
				return nil, err
			}
		}
		// Always dismiss foreground modals before evaluating login home / server bar.
		if a.dismissStartupPopups(ctx, ctrl, f) {
			time.Sleep(postClickSettle)
			continue
		}
		if !a.hasNeteaseAccountDialog(ctx, f) && !a.hasPlatformDialog(ctx, f) &&
			(a.hasTpl(ctx, f.Image, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) ||
				f.FindText(maa.Rect{480, 545, 320, 95}, cachedRegexp("进入游戏")) != nil ||
				f.FindText(maa.Rect{660, 495, 150, 52}, cachedRegexp("切换")) != nil) {
			loginF = f
			break
		}
		time.Sleep(fastPollInterval)
	}
	if loginF == nil {
		return nil, errors.New("未返回登录首页，无法核验区服")
	}
	if a.readAndMatchServer(ctx, loginF, p.Server) {
		a.logf(hook, "info", p.Password, "当前区服已确认为: %s", p.Server)
		return loginF, nil
	}

	a.detail(hook, "正在切换目标区服")
	svrSelectOpened := false
	curF := loginF
	for i := 0; i < 4; i++ {
		if !a.clickOCRExact(ctx, ctrl, curF, maa.Rect{660, 495, 150, 52}, "切换", postClickSettle) {
			clickRect(ctrl, maa.Rect{550, 505, 250, 40})
			time.Sleep(postClickSettle)
		}
		nextF, ok, err := a.waitUntil(ctx, ctrl, 1200*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.hasSelectServerUI(ctx, cf)
		})
		if err != nil {
			return nil, err
		}
		curF = nextF
		if ok {
			svrSelectOpened = true
			break
		}
	}
	if !svrSelectOpened {
		return nil, errors.New("未能打开区服选择界面")
	}

	for i := 0; i < 3; i++ {
		var err error
		if curF == nil {
			if curF, err = a.snapFrame(ctx, ctrl); err != nil {
				return nil, err
			}
		}
		if a.hasTpl(ctx, curF.Image, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8) {
			break
		}
		if a.hasTpl(ctx, curF.Image, tplSelectSvr1, maa.Rect{195, 115, 220, 95}, 0.8) {
			clickRect(ctrl, maa.Rect{205, 570, 815, 40})
			time.Sleep(postClickSettle)
			curF, _, err = a.waitUntil(ctx, ctrl, 1000*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasTpl(ctx, cf.Image, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8)
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if !a.clickOCRExact(ctx, ctrl, curF, maa.Rect{200, 510, 220, 75}, "已有角色", postClickSettle) {
			break
		}
		curF, _, err = a.waitUntil(ctx, ctrl, 1000*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.hasTpl(ctx, cf.Image, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8)
		})
		if err != nil {
			return nil, err
		}
	}

	clickedServer := false
	var lastSig string
	for swipeIdx := 0; swipeIdx < 6 && !clickedServer; swipeIdx++ {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return nil, err
		}
		bottomItems := f.ItemsInROI(maa.Rect{205, 520, 825, 65})
		if len(bottomItems) == 0 {
			bottomItems = vision.OCRItems(ctx, f.Image, maa.Rect{205, 520, 825, 65})
		}
		var sigParts []string
		for _, it := range bottomItems {
			sigParts = append(sigParts, it.Text)
			if MatchServer(it.Text, p.Server) {
				clickRect(ctrl, it.Box)
				clickedServer = true
				break
			}
		}
		if !clickedServer {
			for _, it := range f.ItemsInROI(maa.Rect{360, 130, 720, 480}) {
				sigParts = append(sigParts, it.Text)
				if (p.Character != "" && strings.TrimLeft(it.Text, "0123456789 []【】()（）") == p.Character) || MatchServer(it.Text, p.Server) {
					b := it.Box
					if p.Character != "" && !MatchServer(it.Text, p.Server) {
						b[1] = max(0, b[1]-30)
					}
					clickRect(ctrl, b)
					clickedServer = true
					break
				}
			}
		}
		if clickedServer {
			break
		}
		sig := strings.Join(sigParts, "|")
		if swipeIdx > 0 && sig == lastSig {
			break
		}
		lastSig = sig
		if len(bottomItems) > 0 {
			ctrl.PostSwipe(730, 550, 450, 550, 500*time.Millisecond).Wait()
		} else {
			ctrl.PostSwipe(600, 520, 600, 270, 500*time.Millisecond).Wait()
		}
		time.Sleep(600 * time.Millisecond)
	}
	if !clickedServer {
		clickRect(ctrl, maa.Rect{50, 70, 60, 360})
		return nil, fmt.Errorf("未在区服/已有角色列表中找到目标区服: %s", p.Server)
	}

	time.Sleep(postClickSettle)
	for verifyDeadline := time.Now().Add(15 * time.Second); time.Now().Before(verifyDeadline); {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return nil, err
		}
		if a.hasSelectServerUI(ctx, f) {
			clickRect(ctrl, maa.Rect{50, 70, 60, 360})
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if a.readAndMatchServer(ctx, f, p.Server) {
			a.logf(hook, "info", p.Password, "切换后已二次核对区服一致: %s", p.Server)
			return f, nil
		}
		time.Sleep(fastPollInterval)
	}
	return nil, fmt.Errorf("切换后底部区服名仍与目标 %s 不一致", p.Server)
}

func (a *SwitchAccount) enterAndConfirmCourtyard(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook) error {
	a.detail(hook, "正在进入庭院")
	clickRect(ctrl, maa.Rect{570, 580, 130, 35})
	lastEnterClick := time.Now()
	time.Sleep(300 * time.Millisecond)
	courtyardHits := 0
	var lastCourtyardAt time.Time

	for deadline := time.Now().Add(95 * time.Second); time.Now().Before(deadline); {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return err
		}
		if f.FindText(maa.Rect{500, 610, 280, 100}, cachedRegexp("创建角色")) != nil {
			return errors.New("检测到创建角色界面，目标区服无已有角色，已中止进入")
		}
		if a.dismissEntryPopups(ctx, ctrl, f) {
			courtyardHits = 0
			time.Sleep(postClickSettle)
			continue
		}
		if a.isCourtyard(ctx, f) {
			if courtyardHits == 0 {
				courtyardHits, lastCourtyardAt = 1, time.Now()
				time.Sleep(1020 * time.Millisecond)
				continue
			}
			if time.Since(lastCourtyardAt) >= time.Second {
				return nil
			}
			time.Sleep(time.Second - time.Since(lastCourtyardAt) + 30*time.Millisecond)
			continue
		}
		courtyardHits = 0

		if a.hasSelectServerUI(ctx, f) {
			ctrl.PostClick(106, 535).Wait()
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if f.FindText(maa.Rect{0, 0, 180, 120}, cachedRegexp("选择角色")) != nil {
			clickRect(ctrl, maa.Rect{600, 240, 500, 400})
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if time.Since(lastEnterClick) >= 1600*time.Millisecond {
			if a.hasTpl(ctx, f.Image, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) {
				clickRect(ctrl, maa.Rect{570, 580, 130, 35})
				lastEnterClick = time.Now()
				time.Sleep(350 * time.Millisecond)
				continue
			}
			if a.clickOCRExact(ctx, ctrl, f, maa.Rect{450, 545, 380, 100}, "进入游戏|点击屏幕进入游戏", 350*time.Millisecond) {
				lastEnterClick = time.Now()
				continue
			}
		}
		time.Sleep(fastPollInterval)
	}
	return errors.New("等待进入庭院超时")
}
