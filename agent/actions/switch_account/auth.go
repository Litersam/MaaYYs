package switch_account

import (
	"errors"
	"fmt"
	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"maa-yys-agent/internal/redact"
	"maa-yys-agent/internal/vision"
	"strings"
	"time"
)

func (a *SwitchAccount) loginAndSelectPlatform(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params, initialFrame *Frame) (*Frame, error) {
	a.detail(hook, "正在切换账号")
	openDeadline := time.Now().Add(45 * time.Second)
	var panelFrame *Frame
	toolbarClicks := 0
	reuseInitial := initialFrame
	for time.Now().Before(openDeadline) {
		var f *Frame
		var err error
		if reuseInitial != nil {
			f, reuseInitial = reuseInitial, nil
		} else {
			f, err = a.snapFrame(ctx, ctrl)
			if err != nil {
				return nil, err
			}
		}
		// Clear foreground engine-update / session-expired modals first.
		if a.dismissStartupPopups(ctx, ctrl, f) {
			time.Sleep(postClickSettle)
			continue
		}
		if a.hasSelectServerUI(ctx, f) {
			clickRect(ctrl, maa.Rect{50, 70, 60, 360})
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if a.hasNeteaseAccountDialog(ctx, f) {
			panelFrame = f
			break
		}
		if a.clickTpl(ctx, ctrl, f.Image, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8, postClickSettle) ||
			a.clickOCRExact(ctx, ctrl, f, maa.Rect{850, 140, 300, 150}, "切换账号|切换用户", postClickSettle) {
			if nextF, ok, wErr := a.waitUntil(ctx, ctrl, 1000*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasNeteaseAccountDialog(ctx, cf) || a.dismissStartupPopups(ctx, ctrl, cf)
			}); wErr != nil {
				return nil, wErr
			} else if ok && a.hasNeteaseAccountDialog(ctx, nextF) {
				panelFrame = nextF
				break
			}
			continue
		}
		// Click right-side User Center [1210, 235, 45, 50]; alternate with Account icon [1210, 315, 45, 50] if unresponsive.
		if toolbarClicks%2 == 1 {
			clickRect(ctrl, maa.Rect{1210, 315, 45, 50})
		} else {
			clickRect(ctrl, maa.Rect{1210, 235, 45, 50})
		}
		toolbarClicks++
		time.Sleep(postClickSettle)
		if nextF, ok, wErr := a.waitUntil(ctx, ctrl, 1050*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.hasNeteaseAccountDialog(ctx, cf) ||
				a.hasTpl(ctx, cf.Image, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8) ||
				cf.FindText(maa.Rect{850, 140, 300, 150}, cachedRegexp("切换账号|切换用户")) != nil
		}); wErr != nil {
			return nil, wErr
		} else if ok {
			if a.hasNeteaseAccountDialog(ctx, nextF) {
				panelFrame = nextF
				break
			}
			reuseInitial = nextF
		}
	}
	if panelFrame == nil {
		return nil, errors.New("未能打开网易账号登录面板")
	}

	accountVerified, needPassword, curFrame, err := a.selectStoredAccount(ctx, ctrl, hook, p, panelFrame)
	if err != nil {
		return nil, err
	}
	if needPassword {
		if curFrame, err = a.passwordFallback(ctx, ctrl, hook, p, curFrame); err != nil {
			return nil, err
		}
	} else if accountVerified {
		if curFrame == nil {
			if curFrame, err = a.snapFrame(ctx, ctrl); err != nil {
				return nil, err
			}
		}
		if !a.clickTpl(ctx, ctrl, curFrame.Image, tplAccountLoginBtn, maa.Rect{400, 400, 480, 90}, 0.8, postClickSettle) {
			clickRect(ctrl, maa.Rect{535, 420, 240, 60})
			time.Sleep(postClickSettle)
		}
		curFrame = nil
	}

	a.detail(hook, "正在选择登录平台")
	platDeadline := time.Now().Add(35 * time.Second)
	passwordRetried := needPassword
	reusePlatFrame := curFrame
	for time.Now().Before(platDeadline) {
		var f *Frame
		if reusePlatFrame != nil {
			f, reusePlatFrame = reusePlatFrame, nil
		} else {
			f, err = a.snapFrame(ctx, ctrl)
			if err != nil {
				return nil, err
			}
		}
		appleBox := a.matchTemplate(ctx, f.Image, tplPlatformApple, maa.Rect{475, 320, 165, 165}, 0.8)
		androidBox := a.matchTemplate(ctx, f.Image, tplPlatformAndroid, maa.Rect{640, 320, 165, 165}, 0.8)
		if appleBox != nil || androidBox != nil {
			targetROI := maa.Rect{671, 353, 100, 100}
			if p.Platform == "ios" {
				targetROI = maa.Rect{508, 354, 100, 100}
				if appleBox != nil {
					targetROI = *appleBox
				}
			} else if androidBox != nil {
				targetROI = *androidBox
			}
			clickRect(ctrl, targetROI)
			time.Sleep(postClickSettle)
			afterF, ok, wErr := a.waitUntil(ctx, ctrl, 1050*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return !a.hasPlatformDialog(ctx, cf)
			})
			if wErr != nil {
				return nil, wErr
			}
			if ok {
				return afterF, nil
			}
			continue
		}
		if a.acceptAgreementModal(ctx, ctrl, f) {
			continue
		}
		if !passwordRetried && (a.isPasswordPrompt(ctx, f) || a.isLoginMethodChooser(ctx, f) || a.hasAgreementModal(ctx, f)) {
			passwordRetried = true
			if reusePlatFrame, err = a.passwordFallback(ctx, ctrl, hook, p, f); err != nil {
				return nil, err
			}
			platDeadline = time.Now().Add(25 * time.Second)
			continue
		}
		if a.dismissStartupPopups(ctx, ctrl, f) {
			time.Sleep(postClickSettle)
			continue
		}
		time.Sleep(fastPollInterval)
	}
	return nil, fmt.Errorf("未出现或未能完成 %s 平台选择弹窗", p.Platform)
}

func (a *SwitchAccount) selectStoredAccount(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params, initial *Frame) (bool, bool, *Frame, error) {
	reuse := initial
	for attempt := 0; attempt < 3; attempt++ {
		var f *Frame
		var err error
		if reuse != nil {
			f, reuse = reuse, nil
		} else {
			f, err = a.snapFrame(ctx, ctrl)
			if err != nil {
				return false, false, nil, err
			}
		}
		// Never blind-click [850, 320, 30, 25] on the 4-tile login-method chooser or foreground agreement modal.
		if a.hasAgreementModal(ctx, f) || a.isLoginMethodChooser(ctx, f) || a.isPasswordPrompt(ctx, f) {
			return false, true, f, nil
		}
		// Self-heal if a prior click opened the QR scan subpage: click top-left "<" back arrow to return to 4-tile chooser.
		if f.FindText(maa.Rect{360, 210, 560, 260}, cachedRegexp("二维码|使用.*扫码|扫码登录")) != nil &&
			!a.hasTpl(ctx, f.Image, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
			clickRect(ctrl, maa.Rect{355, 225, 36, 36})
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if a.matchSelectedEmail(ctx, f, p.Email) {
			return true, false, f, nil
		}
		if !a.clickTpl(ctx, ctrl, f.Image, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78, postClickSettle) {
			clickRect(ctrl, maa.Rect{850, 320, 30, 25})
			time.Sleep(postClickSettle)
		}
		listReady, _, wErr := a.waitUntil(ctx, ctrl, 800*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return !a.hasTpl(ctx, cf.Image, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) ||
				len(cf.ItemsInROI(maa.Rect{460, 280, 440, 330})) >= 2
		})
		if wErr != nil {
			return false, false, nil, wErr
		}

		var prevSnapshot string
		reuseList := listReady
		for page := 0; page < 6; page++ {
			var listF *Frame
			if reuseList != nil {
				listF, reuseList = reuseList, nil
			} else {
				listF, err = a.snapFrame(ctx, ctrl)
				if err != nil {
					return false, false, nil, err
				}
			}
			items := listF.ItemsInROI(maa.Rect{460, 280, 440, 330})
			if len(items) == 0 {
				items = vision.OCRItems(ctx, listF.Image, maa.Rect{460, 280, 440, 330})
			}
			var texts []string
			var hitBox *maa.Rect
			for _, it := range items {
				texts = append(texts, it.Text)
				if MatchEmail(it.Text, p.Email) {
					b := it.Box
					hitBox = &b
					break
				}
			}
			if hitBox != nil {
				clickRect(ctrl, *hitBox)
				time.Sleep(postClickSettle)
				verifyF, ok, vErr := a.waitUntil(ctx, ctrl, 900*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
					return a.matchSelectedEmail(ctx, cf, p.Email)
				})
				if vErr != nil {
					return false, false, nil, vErr
				}
				if ok {
					a.logf(hook, "info", p.Password, "已选中并核对历史账号: %s", p.Email)
					return true, false, verifyF, nil
				}
				break
			}
			if a.hasTpl(ctx, listF.Image, tplDropdownAddAccount, maa.Rect{400, 540, 220, 60}, 0.8) ||
				listF.FindText(maa.Rect{400, 470, 480, 160}, cachedRegexp("添加新账号|其他账号|其他方式")) != nil {
				return false, true, listF, nil
			}
			snapshot := strings.Join(texts, "|")
			if snapshot != "" && snapshot == prevSnapshot {
				return false, true, listF, nil
			}
			prevSnapshot = snapshot
			ctrl.PostSwipe(615, 520, 615, 285, 500*time.Millisecond).Wait()
			time.Sleep(550 * time.Millisecond)
		}
	}
	return false, true, nil, nil
}

func (a *SwitchAccount) passwordFallback(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params, initial *Frame) (*Frame, error) {
	if strings.TrimSpace(p.Password) == "" {
		return nil, fmt.Errorf("历史列表未找到账号 %s 或登录已过期，需要密码但未传入 password", p.Email)
	}
	a.detail(hook, "正在执行安全密码登录兜底")
	a.logf(hook, "info", p.Password, "使用 Win32 窗口消息为账号 %s 执行密码兜底登录", p.Email)

	f := initial
	var err error
	if f == nil {
		if f, err = a.snapFrame(ctx, ctrl); err != nil {
			return nil, err
		}
	}
	if a.clickTpl(ctx, ctrl, f.Image, tplDropdownAddAccount, maa.Rect{400, 540, 220, 60}, 0.8, postClickSettle) ||
		a.clickOCRExact(ctx, ctrl, f, maa.Rect{390, 450, 500, 190}, "添加新账号|其他账号|使用其他账号", postClickSettle) {
		if f, _, err = a.waitUntil(ctx, ctrl, 900*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.isLoginMethodChooser(ctx, cf) || a.hasAgreementModal(ctx, cf) || a.isPasswordPrompt(ctx, cf)
		}); err != nil {
			return nil, err
		}
	}
	if a.acceptAgreementModal(ctx, ctrl, f) {
		if f, err = a.snapFrame(ctx, ctrl); err != nil {
			return nil, err
		}
	}
	// Handle NetEase 4-tile login-method chooser idempotently so we never uncheck an already-checked agreement box.
	if a.isLoginMethodChooser(ctx, f) {
		clickEmailTile := func(cur *Frame) {
			if !a.clickOCRExact(ctx, ctrl, cur, maa.Rect{370, 365, 280, 105}, "网易邮箱", 0) {
				ctrl.PostClick(510, 419).Wait()
			}
		}
		clickEmailTile(f)
		time.Sleep(postClickSettle)
		if f, _, err = a.waitUntil(ctx, ctrl, 850*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.hasAgreementModal(ctx, cf) || a.isPasswordPrompt(ctx, cf)
		}); err != nil {
			return nil, err
		}
		if a.acceptAgreementModal(ctx, ctrl, f) {
			if f, _, err = a.waitUntil(ctx, ctrl, 850*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.isPasswordPrompt(ctx, cf)
			}); err != nil {
				return nil, err
			}
		} else if a.isLoginMethodChooser(ctx, f) {
			ctrl.PostClick(464, 544).Wait()
			time.Sleep(260 * time.Millisecond)
			if f, err = a.snapFrame(ctx, ctrl); err != nil {
				return nil, err
			}
			clickEmailTile(f)
			time.Sleep(postClickSettle)
			if f, _, err = a.waitUntil(ctx, ctrl, 900*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasAgreementModal(ctx, cf) || a.isPasswordPrompt(ctx, cf)
			}); err != nil {
				return nil, err
			}
			if a.acceptAgreementModal(ctx, ctrl, f) {
				if f, err = a.snapFrame(ctx, ctrl); err != nil {
					return nil, err
				}
			}
		}
	}
	if f.FindText(maa.Rect{380, 240, 520, 240}, cachedRegexp("获取验证码|短信")) != nil &&
		a.clickOCRExact(ctx, ctrl, f, maa.Rect{380, 180, 520, 380}, "密码登录|邮箱登录|网易邮箱", postClickSettle) {
		if f, _, err = a.waitUntil(ctx, ctrl, 800*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return cf.FindText(maa.Rect{380, 240, 520, 240}, cachedRegexp("获取验证码|短信")) == nil
		}); err != nil {
			return nil, err
		}
	}

	if !a.isPasswordPrompt(ctx, f) {
		return nil, errors.New("未能确认网易邮箱密码登录表单")
	}
	emailROI := maa.Rect{380, 225, 510, 70}
	clearEmailField := func() {
		ctrl.PostClick(540, 260).Wait()
		time.Sleep(250 * time.Millisecond)
		ctrl.PostClick(882, 260).Wait()
		time.Sleep(250 * time.Millisecond)
	}
	if !a.matchExactEmailInROI(ctx, f, emailROI, p.Email) {
		if a.hasNonPlaceholderEmailText(ctx, f, maa.Rect{380, 225, 475, 70}) {
			clearEmailField()
		}
		for attempt := 0; attempt < 2; attempt++ {
			ctrl.PostClick(540, 260).Wait()
			time.Sleep(480 * time.Millisecond)
			if err := sendMuMuText(p.MumuIndex, p.AdbPath, p.AdbAddr, p.Email); err != nil {
				return nil, fmt.Errorf("发送邮箱字符失败: %w", err)
			}
			time.Sleep(260 * time.Millisecond)
			if f, err = a.snapFrame(ctx, ctrl); err != nil {
				return nil, err
			}
			if a.matchExactEmailInROI(ctx, f, emailROI, p.Email) {
				break
			}
			if attempt == 0 {
				a.logf(hook, "info", p.Password, "邮箱输入框校验未命中目标邮箱，点击右侧清空按钮重试输入")
				clearEmailField()
			}
		}
		if !a.matchExactEmailInROI(ctx, f, emailROI, p.Email) {
			return nil, errors.New("输入邮箱未通过核验，未发送密码")
		}
	}

	ctrl.PostClick(540, 348).Wait()
	time.Sleep(480 * time.Millisecond)
	if err := sendMuMuText(p.MumuIndex, p.AdbPath, p.AdbAddr, p.Password); err != nil {
		return nil, errors.New(redact.Secrets(fmt.Sprintf("发送登录凭证窗口消息失败: %v", err), p.Password))
	}
	time.Sleep(220 * time.Millisecond)

	submitLogin := func(cur *Frame) (*Frame, error) {
		if cur != nil && a.clickTpl(ctx, ctrl, cur.Image, tplAccountLoginBtn, maa.Rect{380, 395, 520, 85}, 0.8, 0) {
			// clicked via template
		} else {
			ctrl.PostClick(640, 435).Wait()
		}
		time.Sleep(postClickSettle)
		nextF, _, wErr := a.waitUntil(ctx, ctrl, 1050*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.hasPlatformDialog(ctx, cf) || a.hasAgreementModal(ctx, cf) || !a.isPasswordPrompt(ctx, cf)
		})
		return nextF, wErr
	}
	postF, err := submitLogin(f)
	if err != nil {
		return nil, err
	}
	if a.hasPlatformDialog(ctx, postF) {
		return postF, nil
	}
	if a.acceptAgreementModal(ctx, ctrl, postF) {
		return nil, nil
	}
	needAgree := postF.FindText(maa.Rect{320, 500, 460, 135}, cachedRegexp("请先勾选同意")) != nil
	if needAgree || a.isPasswordPrompt(ctx, postF) {
		if agreeLine := postF.FindText(maa.Rect{380, 410, 520, 175}, cachedRegexp("我已详细阅读|我已阅读|阅读并同意")); agreeLine != nil {
			ctrl.PostClick(int32(agreeLine.X()-16), int32(agreeLine.Y()+agreeLine.Height()/2)).Wait()
			time.Sleep(280 * time.Millisecond)
			postF, err = submitLogin(nil)
		} else if needAgree {
			ctrl.PostClick(464, 544).Wait()
			time.Sleep(280 * time.Millisecond)
			postF, err = submitLogin(nil)
		}
		if err != nil {
			return nil, err
		}
		if postF != nil && a.acceptAgreementModal(ctx, ctrl, postF) {
			return nil, nil
		}
	}
	return postF, nil
}
