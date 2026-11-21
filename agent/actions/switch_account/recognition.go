package switch_account

import (
	"errors"
	"fmt"
	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"image"
	"maa-yys-agent/internal/vision"
	"regexp"
	"strings"
	"time"
)

func (a *SwitchAccount) hasSelectServerUI(ctx *maa.Context, f *Frame) bool {
	return a.hasTpl(ctx, f.Image, tplSelectSvr1, maa.Rect{195, 115, 220, 95}, 0.8) ||
		a.hasTpl(ctx, f.Image, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8) ||
		f.FindText(maa.Rect{200, 120, 220, 80}, cachedRegexp("已有角色")) != nil ||
		f.FindText(maa.Rect{200, 510, 220, 75}, cachedRegexp("已有角色")) != nil
}

func (a *SwitchAccount) hasPlatformDialog(ctx *maa.Context, f *Frame) bool {
	return f != nil && (a.hasTpl(ctx, f.Image, tplPlatformApple, maa.Rect{475, 320, 165, 165}, 0.8) ||
		a.hasTpl(ctx, f.Image, tplPlatformAndroid, maa.Rect{640, 320, 165, 165}, 0.8))
}

func (a *SwitchAccount) hasNeteaseAccountDialog(ctx *maa.Context, f *Frame) bool {
	if f == nil || a.hasPlatformDialog(ctx, f) {
		return false
	}
	return a.hasTpl(ctx, f.Image, tplNeteaseLogo, maa.Rect{460, 145, 360, 130}, 0.8) ||
		a.hasTpl(ctx, f.Image, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) ||
		a.hasAgreementModal(ctx, f) ||
		a.isLoginMethodChooser(ctx, f) ||
		a.isPasswordPrompt(ctx, f) ||
		f.FindText(maa.Rect{400, 150, 480, 120}, cachedRegexp("网易游戏|网易账号|邮箱账号|账号登录")) != nil
}

func frameBlocker(f *Frame) error {
	for _, it := range f.ItemsInROI(maa.Rect{280, 180, 720, 320}) {
		if displacedExpr.MatchString(it.Text) {
			return errDisplaced
		}
	}
	if f.FindText(maa.Rect{420, 15, 440, 95}, cachedRegexp("^(登录)?安全验证$")) != nil ||
		f.FindText(maa.Rect{90, 260, 1100, 290}, cachedRegexp("账号存在安全风险|发送.*进行验证|原固定设备登录")) != nil {
		return errSecurityVerify
	}
	return nil
}

func (a *SwitchAccount) hasAgreementModal(ctx *maa.Context, f *Frame) bool {
	return f != nil && (f.FindText(maa.Rect{450, 170, 380, 95}, cachedRegexp("用户协议及隐私保护")) != nil ||
		f.FindText(maa.Rect{420, 340, 470, 200}, cachedRegexp("^(同意|同意并继续|同意并登录)$")) != nil)
}

func (a *SwitchAccount) acceptAgreementModal(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	if f == nil {
		return false
	}
	if f.FindText(maa.Rect{420, 340, 470, 200}, cachedRegexp("^(同意|同意并继续|同意并登录)$")) != nil {
		if a.clickOCRExact(ctx, ctrl, f, maa.Rect{420, 340, 470, 200}, "^(同意|同意并继续|同意并登录)$", postClickSettle) {
			return true
		}
	}
	if f.FindText(maa.Rect{450, 170, 380, 95}, cachedRegexp("用户协议及隐私保护")) != nil {
		if !a.clickOCRExact(ctx, ctrl, f, maa.Rect{420, 340, 470, 200}, "^(同意|同意并继续|同意并登录)$", postClickSettle) {
			ctrl.PostClick(761, 487).Wait()
			time.Sleep(postClickSettle)
		}
		return true
	}
	return false
}

func (a *SwitchAccount) isLoginMethodChooser(ctx *maa.Context, f *Frame) bool {
	if f == nil || a.hasTpl(ctx, f.Image, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
		return false
	}
	if f.FindText(maa.Rect{360, 290, 560, 190}, cachedRegexp("手机账号|扫码登录|快速游戏")) != nil {
		return true
	}
	return f.FindText(maa.Rect{360, 290, 560, 190}, cachedRegexp("网易邮箱")) != nil &&
		!a.isPasswordPrompt(ctx, f)
}

func (a *SwitchAccount) hasTpl(ctx *maa.Context, img image.Image, tpl string, roi maa.Rect, thr float64) bool {
	return a.matchTemplate(ctx, img, tpl, roi, thr) != nil
}

func (a *SwitchAccount) clickTpl(ctx *maa.Context, ctrl *maa.Controller, img image.Image, tpl string, roi maa.Rect, thr float64, wait time.Duration) bool {
	box := a.matchTemplate(ctx, img, tpl, roi, thr)
	if box == nil {
		return false
	}
	clickRect(ctrl, *box)
	if wait > 0 {
		time.Sleep(wait)
	}
	return true
}

func (a *SwitchAccount) clickOCRExact(ctx *maa.Context, ctrl *maa.Controller, f *Frame, roi maa.Rect, expr string, wait time.Duration) bool {
	if f == nil {
		return false
	}
	box := f.FindText(roi, cachedRegexp(expr))
	if box == nil {
		box = vision.FindCroppedText(ctx, f.Image, roi, expr)
	}
	if box == nil {
		return false
	}
	clickRect(ctrl, *box)
	if wait > 0 {
		time.Sleep(wait)
	}
	return true
}

func (a *SwitchAccount) snapFrame(ctx *maa.Context, ctrl *maa.Controller) (*Frame, error) {
	if ctx.GetTasker().Stopping() {
		return nil, errors.New("账号切换已中止")
	}
	if !ctrl.PostScreencap().Wait().Success() {
		return nil, errors.New("设备截图失败")
	}
	img, err := ctrl.CacheImage()
	if err != nil || img == nil {
		return nil, fmt.Errorf("读取缓存帧失败: %w", err)
	}
	f := vision.CaptureFrame(ctx, img)
	if err := frameBlocker(f); err != nil {
		return nil, err
	}
	return f, nil
}

func (a *SwitchAccount) waitUntil(ctx *maa.Context, ctrl *maa.Controller, timeout, interval time.Duration, cond func(*Frame) bool) (*Frame, bool, error) {
	deadline := time.Now().Add(timeout)
	var last *Frame
	for {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return last, false, err
		}
		last = f
		if cond(f) {
			return f, true, nil
		}
		rem := time.Until(deadline)
		if rem <= 0 {
			return last, false, nil
		}
		sleepDur := interval
		if rem < sleepDur {
			sleepDur = rem
		}
		time.Sleep(sleepDur)
	}
}

func (a *SwitchAccount) isLoginSurface(ctx *maa.Context, f *Frame) bool {
	return a.hasTpl(ctx, f.Image, tplLoginForm, maa.Rect{1, 540, 250, 120}, 0.8) ||
		a.hasNeteaseAccountDialog(ctx, f) ||
		a.hasTpl(ctx, f.Image, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8) ||
		a.hasPlatformDialog(ctx, f) ||
		a.hasSelectServerUI(ctx, f) ||
		f.FindText(maa.Rect{480, 545, 320, 95}, cachedRegexp("进入游戏")) != nil
}

func (a *SwitchAccount) isCourtyard(ctx *maa.Context, f *Frame) bool {
	// Check fast template/feature matches (~3ms) and cached OCR (~0ms) BEFORE YOLOv8 ONNX (~120ms).
	for _, node := range []string{
		"导航-庭院-识别是否位于庭院-模板匹配",
		"打开底部菜单_匹配",
	} {
		if detail, err := ctx.RunRecognition(node, f.Image); err == nil && detail != nil && detail.Hit {
			return true
		}
	}
	if f.FindText(maa.Rect{0, 564, 1280, 156}, cachedRegexp("^(图鉴|珍旅居|纳物库|闲庭)")) != nil {
		return true
	}
	if len(f.ItemsInROI(maa.Rect{0, 0, 1280, 720})) == 0 {
		return false
	}
	if detail, err := ctx.RunRecognition("导航-庭院-识别是否位于庭院-模型识别", f.Image); err == nil && detail != nil && detail.Hit {
		return true
	}
	return false
}

func (a *SwitchAccount) isCourtyardOrProfile(ctx *maa.Context, f *Frame) bool {
	if a.isCourtyard(ctx, f) {
		return true
	}
	return a.matchTemplate(ctx, f.Image, tplUserCenterProfile, maa.Rect{590, 60, 90, 55}, 0.8) != nil ||
		a.matchTemplate(ctx, f.Image, tplUserCenterBtn, maa.Rect{190, 390, 330, 200}, 0.8) != nil
}

func (a *SwitchAccount) isPasswordPrompt(ctx *maa.Context, f *Frame) bool {
	if f == nil || a.hasTpl(ctx, f.Image, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
		return false
	}
	return f.FindText(maa.Rect{375, 210, 530, 335}, cachedRegexp("请输入.*密码|输入.*密码|登录密码|密码登录|请输入.*邮箱|邮箱账号|获取验证码|立即注册|忘记密码")) != nil
}

func (a *SwitchAccount) dismissStartupPopups(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	// 1. Engine update modal ("发现全新的引擎版本！"): strictly click left button "暂不更新" (never "后台下载").
	if f.FindText(maa.Rect{340, 200, 600, 220}, cachedRegexp("发现全新的引擎版本|全新的引擎|引擎版本")) != nil {
		if !a.clickOCRExact(ctx, ctrl, f, maa.Rect{360, 340, 560, 170}, "暂不更新|以后再说|暂不升级", 0) {
			clickRect(ctrl, maa.Rect{445, 412, 173, 47})
		}
		return true
	}
	if box := f.FindText(maa.Rect{360, 340, 560, 170}, cachedRegexp("^(暂不更新|以后再说|暂不升级)$")); box != nil {
		clickRect(ctrl, *box)
		return true
	}

	// 2. Session expired modal ("用户登录失效。"): click centered red button "^重新登录$" to open NetEase login dialog.
	if f.FindText(maa.Rect{380, 240, 520, 160}, cachedRegexp("用户登录失效|登录失效|登录已失效")) != nil {
		if !a.clickOCRExact(ctx, ctrl, f, maa.Rect{450, 360, 380, 130}, "^重新登录$", 0) {
			clickRect(ctrl, maa.Rect{536, 403, 208, 62})
		}
		return true
	}
	if !a.hasNeteaseAccountDialog(ctx, f) {
		if box := f.FindText(maa.Rect{450, 360, 380, 130}, cachedRegexp("^重新登录$")); box != nil {
			clickRect(ctrl, *box)
			return true
		}
	}

	// 3. Client update notice (handled via cached OCR + direct target click to avoid pipeline delay).
	if f.FindText(maa.Rect{493, 100, 314, 98}, cachedRegexp("客.*端更新")) != nil {
		clickRect(ctrl, maa.Rect{896, 127, 43, 41})
		return true
	}
	// 4. Cancel illustration / resource download or CG video.
	if f.FindText(maa.Rect{320, 170, 640, 270}, cachedRegexp("下载插画|资源下载|观看视频|是否打开|加成")) != nil {
		if cancel := f.FindText(maa.Rect{280, 330, 420, 220}, cachedRegexp("取消|拒绝|不观看|暂不")); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
		if a.clickOCRExact(ctx, ctrl, f, maa.Rect{280, 330, 420, 220}, "取消|拒绝|不观看|暂不", 0) {
			return true
		}
	}
	if a.clickRecognizedNode(ctx, ctrl, f.Image, "通用-拒绝下载-识别拒绝后点击") {
		return true
	}
	// 5. Announcements and ads (click recognized close box directly without 2000ms pipeline post_delay).
	if a.clickRecognizedNode(ctx, ctrl, f.Image, "关闭公告") || a.clickRecognizedNode(ctx, ctrl, f.Image, "关闭广告-模板匹配") {
		return true
	}
	return a.dismissPhoneBind(ctx, ctrl, f)
}

func (a *SwitchAccount) dismissPhoneBind(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	if box := a.matchTemplate(ctx, f.Image, tplCancelBindPhone, maa.Rect{420, 430, 220, 140}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if box := a.matchTemplate(ctx, f.Image, tplGotoBindPhone, maa.Rect{900, 410, 230, 180}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if f.FindText(maa.Rect{360, 180, 560, 260}, cachedRegexp("绑定手机|手机绑定")) != nil {
		if cancel := f.FindText(maa.Rect{380, 380, 300, 190}, cachedRegexp("取消|暂不|关闭")); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
		if a.clickOCRExact(ctx, ctrl, f, maa.Rect{380, 380, 300, 190}, "取消|暂不|关闭", 0) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) dismissEntryPopups(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	// Reject co-op / foster invites on left banner.
	if box := a.matchTemplate(ctx, f.Image, tplRejectInvite, maa.Rect{5, 200, 120, 320}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if a.dismissStartupPopups(ctx, ctrl, f) {
		return true
	}
	// Cancel battle resume prompt.
	if f.FindText(maa.Rect{360, 220, 560, 220}, cachedRegexp("继续战斗|是否继续")) != nil {
		if cancel := f.FindText(maa.Rect{400, 360, 260, 130}, cachedRegexp("取消")); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
	}
	// Tap "点击屏幕继续" via cached frame OCR without 1000ms pipeline post_delay.
	if box := f.FindText(maa.Rect{393, 639, 469, 81}, cachedRegexp("点击屏幕继续")); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	// Close courtyard popups directly without 2000ms+2000ms pipeline pre_delay/post_delay.
	for _, node := range []string{
		"导航-庭院-关闭当前弹窗",
		"导航-庭院-关闭意外弹窗",
	} {
		if a.clickRecognizedNode(ctx, ctrl, f.Image, node) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) clickRecognizedNode(ctx *maa.Context, ctrl *maa.Controller, img image.Image, node string) bool {
	detail, err := ctx.RunRecognition(node, img)
	if err != nil || detail == nil || !detail.Hit {
		return false
	}
	clickRect(ctrl, detail.Box)
	return true
}

func (a *SwitchAccount) matchSelectedEmail(ctx *maa.Context, f *Frame, email string) bool {
	roi := maa.Rect{460, 280, 370, 50}
	for _, it := range f.ItemsInROI(roi) {
		if MatchEmail(it.Text, email) {
			return true
		}
	}
	for _, it := range vision.OCRItems(ctx, f.Image, roi) {
		if MatchEmail(it.Text, email) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) matchExactEmailInROI(ctx *maa.Context, f *Frame, roi maa.Rect, email string) bool {
	for _, it := range f.ItemsInROI(roi) {
		if MatchExactEmail(it.Text, email) {
			return true
		}
	}
	for _, it := range vision.OCRItems(ctx, f.Image, roi) {
		if MatchExactEmail(it.Text, email) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) hasNonPlaceholderEmailText(ctx *maa.Context, f *Frame, roi maa.Rect) bool {
	items := f.ItemsInROI(roi)
	if len(items) == 0 {
		items = vision.OCRItems(ctx, f.Image, roi)
	}
	for _, it := range items {
		txt := strings.TrimSpace(it.Text)
		if txt == "" || strings.Contains(txt, "请输入") || strings.Contains(txt, "邮箱账号") || strings.Contains(txt, "输入邮箱") {
			continue
		}
		return true
	}
	return false
}

func (a *SwitchAccount) readAndMatchServer(ctx *maa.Context, f *Frame, server string) bool {
	for _, roi := range []maa.Rect{{460, 495, 255, 52}, {450, 495, 340, 55}} {
		for _, it := range f.ItemsInROI(roi) {
			if MatchServer(it.Text, server) {
				return true
			}
		}
	}
	for _, roi := range []maa.Rect{{460, 495, 255, 52}, {450, 495, 340, 55}} {
		for _, it := range vision.OCRItems(ctx, f.Image, roi) {
			if MatchServer(it.Text, server) {
				return true
			}
		}
	}
	return false
}

func cachedRegexp(expr string) *regexp.Regexp {
	if v, ok := regexCache.Load(expr); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	regexCache.Store(expr, re)
	return re
}

func (a *SwitchAccount) matchTemplate(ctx *maa.Context, img image.Image, tpl string, roi maa.Rect, threshold float64) *maa.Rect {
	detail, err := ctx.RunRecognitionDirect(maa.RecognitionTypeTemplateMatch, &maa.TemplateMatchParam{
		ROI:       maa.NewTargetRect(roi),
		Template:  []string{tpl},
		Threshold: []float64{threshold},
	}, img)
	if err != nil || detail == nil || !detail.Hit {
		return nil
	}
	b := detail.Box
	return &b
}

func clickRect(ctrl *maa.Controller, r maa.Rect) {
	x := int32(r.X() + max(1, r.Width())/2)
	y := int32(r.Y() + max(1, r.Height())/2)
	ctrl.PostClick(x, y).Wait()
}
