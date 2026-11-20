package switch_account

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"regexp"
	"strings"
	"sync"
	"time"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/rs/zerolog/log"
)

const (
	tplLoginForm          = "账号登录/check_login_form.png"
	tplUserCenterProfile  = "账号登录/sa_user_center_profile.png"
	tplUserCenterBtn      = "账号登录/sa_user_center.png"
	tplSwitchAccountBtn   = "账号登录/sa_switch_account_btn.png"
	tplNeteaseLogo        = "账号登录/sa_netease_game_logo.png"
	tplDropdownClosed     = "账号登录/sa_account_drop_down_closed.png"
	tplDropdownAddAccount = "账号登录/sa_account_drop_down_add_account.png"
	tplAccountLoginBtn    = "账号登录/sa_account_login_btn.png"
	tplPlatformApple      = "账号登录/res_sa_login_form_apple.png"
	tplPlatformAndroid    = "账号登录/res_sa_login_form_android.png"
	tplSelectSvr1         = "账号登录/res_sa_check_select_svr_1.png"
	tplSelectSvr2         = "账号登录/res_sa_check_select_svr_2.png"
	tplRejectInvite       = "账号登录/gi_i_reject.png"
	tplGotoBindPhone      = "账号登录/goto_bind_phone.png"
	tplCancelBindPhone    = "账号登录/cancel_bind_phone.png"
)

var (
	errDisplaced  = errors.New("账号在其他设备登录，已中止换号以保护在线状态")
	displacedExpr = regexp.MustCompile(`其他设备登录|在其他设备|被迫下线|已在其他设备`)
)

// Params defines the parameters accepted by SwitchAccount.
// Password is never logged or echoed in errors.
type Params struct {
	Email     string `json:"email"`
	Password  string `json:"password,omitempty"`
	Platform  string `json:"platform"`
	Server    string `json:"server"`
	Character string `json:"character,omitempty"`
	MumuIndex *int   `json:"mumu_index,omitempty"`
	AdbPath   string `json:"adb_path,omitempty"`
	AdbAddr   string `json:"adb_address,omitempty"`
}

func (p Params) String() string {
	return fmt.Sprintf("Params{email:%q, platform:%q, server:%q}", p.Email, p.Platform, p.Server)
}

func (p Params) GoString() string {
	return p.String()
}

// RuntimeHook allows in-process callers (such as maayys-cli) to inject sensitive credentials
// without writing them to pipeline_override, stream structured step/log events, and read outcome state.
type RuntimeHook struct {
	SecretPassword string
	MumuIndex      *int
	AdbPath        string
	AdbAddress     string
	OnLog          func(level, message string)
	OnDetail       func(detail string)
}

// Outcome records the result of the most recent SwitchAccount execution.
type Outcome struct {
	Displaced bool
	LastError string
}

// SwitchAccount implements maa.CustomActionRunner for exact multi-account login & switching.
type SwitchAccount struct {
	mu      sync.Mutex
	hook    *RuntimeHook
	outcome Outcome
}

func (a *SwitchAccount) SetRuntimeHook(h *RuntimeHook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hook = h
	a.outcome = Outcome{}
}

func (a *SwitchAccount) LastOutcome() Outcome {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.outcome
}

// NormalizeParams validates and normalizes SwitchAccount parameters.
func NormalizeParams(p Params) (Params, error) {
	p.Email = strings.TrimSpace(p.Email)
	p.Platform = strings.ToLower(strings.TrimSpace(p.Platform))
	p.Server = strings.TrimSpace(p.Server)
	p.Character = strings.TrimSpace(p.Character)
	if p.Email == "" {
		return p, errors.New("缺少目标账号 email")
	}
	if p.Platform != "ios" && p.Platform != "android" {
		return p, fmt.Errorf("无效平台 %q，仅支持 ios 或 android", p.Platform)
	}
	if p.Server == "" {
		return p, errors.New("缺少目标区服 server")
	}
	return p, nil
}

// RedactSecrets strips any occurrence of secret from message so passwords never enter logs.
func RedactSecrets(message string, secrets ...string) string {
	out := message
	for _, s := range secrets {
		if s != "" {
			out = strings.ReplaceAll(out, s, "[REDACTED]")
		}
	}
	return out
}

// MatchEmail checks whether OCR text matches target email strictly (allowing only fullwidth @ or whitespace noise).
func MatchEmail(ocrText, targetEmail string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, " ", "")
		s = strings.ReplaceAll(s, "＠", "@")
		return strings.ToLower(s)
	}
	got, want := norm(ocrText), norm(targetEmail)
	return got != "" && want != "" && got == want
}

// MatchServer checks whether OCR server text matches target server name.
func MatchServer(ocrText, targetServer string) bool {
	clean := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, " ", "")
		s = strings.Trim(s, "<>[]【】()（）·-—>🌸*")
		s = strings.TrimSuffix(s, "切换")
		return strings.Trim(s, "<>[]【】()（）·-—>🌸*")
	}
	got, want := clean(ocrText), clean(targetServer)
	return got != "" && want != "" && got == want
}

func (a *SwitchAccount) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	a.mu.Lock()
	hook := a.hook
	a.outcome = Outcome{}
	a.mu.Unlock()

	var params Params
	if arg != nil && strings.TrimSpace(arg.CustomActionParam) != "" {
		if err := json.Unmarshal([]byte(arg.CustomActionParam), &params); err != nil {
			a.fail("解析账号登录参数失败", false, "")
			return false
		}
	}
	if hook != nil {
		if hook.SecretPassword != "" {
			params.Password = hook.SecretPassword
		}
		if params.MumuIndex == nil && hook.MumuIndex != nil {
			params.MumuIndex = hook.MumuIndex
		}
		if params.AdbPath == "" {
			params.AdbPath = hook.AdbPath
		}
		if params.AdbAddr == "" {
			params.AdbAddr = hook.AdbAddress
		}
	}

	secret := params.Password
	normParams, err := NormalizeParams(params)
	if err != nil {
		a.fail(err.Error(), false, secret)
		return false
	}
	params = normParams
	a.populateControllerInfo(ctx, &params)

	a.logf(hook, "info", secret, "开始账号切换: email=%s platform=%s server=%s", params.Email, params.Platform, params.Server)

	if err := a.execute(ctx, hook, params); err != nil {
		displaced := errors.Is(err, errDisplaced)
		a.fail(err.Error(), displaced, secret)
		a.logf(hook, "error", secret, "账号切换中止: %s", err.Error())
		return false
	}

	a.detail(hook, "已进入庭院")
	a.logf(hook, "info", secret, "账号 %s (%s / %s) 已确认进入庭院", params.Email, params.Platform, params.Server)
	return true
}

func (a *SwitchAccount) populateControllerInfo(ctx *maa.Context, p *Params) {
	if p.AdbPath != "" && p.AdbAddr != "" {
		return
	}
	ctrl := ctx.GetTasker().GetController()
	infoStr, err := ctrl.GetInfo()
	if err != nil || infoStr == "" {
		return
	}
	var info struct {
		AdbPath string `json:"adb_path"`
		Address string `json:"address"`
	}
	if json.Unmarshal([]byte(infoStr), &info) == nil {
		if p.AdbPath == "" {
			p.AdbPath = info.AdbPath
		}
		if p.AdbAddr == "" {
			p.AdbAddr = info.Address
		}
	}
}

func (a *SwitchAccount) execute(ctx *maa.Context, hook *RuntimeHook, p Params) error {
	ctrl := ctx.GetTasker().GetController()

	// Step 1 & 2: Ensure game is running in foreground; if in courtyard, exit to login page.
	inGame, err := a.ensureSurface(ctx, ctrl, hook)
	if err != nil {
		return err
	}
	if inGame {
		if err := a.exitCourtyardToLogin(ctx, ctrl, hook); err != nil {
			return err
		}
	}

	// Step 3 & 4: Select/authenticate NetEase email (with safe Win32 password fallback) & select platform.
	if err := a.loginAndSelectPlatform(ctx, ctrl, hook, p); err != nil {
		return err
	}

	// Step 5: Verify bottom server name; switch server via existing-characters panel if mismatched.
	if err := a.verifyAndSwitchServer(ctx, ctrl, hook, p); err != nil {
		return err
	}

	// Step 6: Enter game and require 2 consecutive courtyard detections >= 1s apart.
	return a.enterAndConfirmCourtyard(ctx, ctrl, hook)
}

func (a *SwitchAccount) ensureSurface(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook) (inGame bool, err error) {
	startPkg := "com.netease.onmyoji.wyzymnqsd_cps"
	if node, nErr := ctx.GetNode("启动游戏"); nErr == nil && node != nil && node.Action != nil {
		if sp, ok := node.Action.Param.(*maa.StartAppParam); ok && strings.TrimSpace(sp.Package) != "" {
			startPkg = strings.TrimSpace(sp.Package)
		}
	}
	pkgPrefix := strings.Split(startPkg, "/")[0]

	focused := false
	if job := ctrl.PostShell("dumpsys window | grep -E 'mCurrentFocus|mFocusedApp'", 5*time.Second); job.Wait().Success() {
		if out, sErr := ctrl.GetShellOutput(); sErr == nil && strings.Contains(out, pkgPrefix) {
			focused = true
		}
	}
	if !focused {
		a.detail(hook, "正在启动游戏")
		a.logf(hook, "info", "", "游戏未在前台，调用 StartApp (%s)", startPkg)
		if !ctrl.PostStartApp(startPkg).Wait().Success() {
			return false, fmt.Errorf("StartApp 启动失败: %s", startPkg)
		}
		time.Sleep(3 * time.Second)
	} else {
		a.detail(hook, "正在识别当前页面")
	}

	deadline := time.Now().Add(135 * time.Second)
	for time.Now().Before(deadline) {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return false, err
		}

		// Always clear foreground blocking modals BEFORE checking background login surface,
		// because bottom-left CADPA 8+ badge can still match >= 0.8 behind semi-transparent overlays.
		if a.dismissStartupPopups(ctx, ctrl, img) {
			time.Sleep(1000 * time.Millisecond)
			continue
		}

		if a.isLoginSurface(ctx, img) {
			return false, nil
		}
		if a.isCourtyardOrProfile(ctx, img) {
			return true, nil
		}

		// Return from an inner game page back toward courtyard if applicable.
		if a.runNodeIfHit(ctx, img, "导航-庭院-点击返回按钮-模板匹配") || a.runNodeIfHit(ctx, img, "导航-庭院-点击快捷返回庭院按钮") {
			time.Sleep(1200 * time.Millisecond)
			continue
		}
		time.Sleep(800 * time.Millisecond)
	}
	return false, errors.New("等待登录页或庭院超时")
}

func (a *SwitchAccount) exitCourtyardToLogin(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook) error {
	a.detail(hook, "已在游戏内，正在切换账号退回登录页")
	deadline := time.Now().Add(50 * time.Second)
	for time.Now().Before(deadline) {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		if a.hasTpl(ctx, img, tplNeteaseLogo, maa.Rect{500, 170, 300, 90}, 0.8) ||
			a.hasTpl(ctx, img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) {
			return nil
		}
		if a.clickTpl(ctx, ctrl, img, tplSwitchAccountBtn, maa.Rect{930, 170, 160, 75}, 0.8, 2*time.Second) ||
			a.clickOCR(ctx, ctrl, img, maa.Rect{880, 150, 240, 110}, "切换账号|切换用户", 2*time.Second) ||
			a.clickTpl(ctx, ctrl, img, tplUserCenterBtn, maa.Rect{190, 390, 330, 200}, 0.8, 2500*time.Millisecond) {
			continue
		}
		if a.hasTpl(ctx, img, tplUserCenterProfile, maa.Rect{590, 60, 90, 55}, 0.8) &&
			a.clickOCR(ctx, ctrl, img, maa.Rect{150, 350, 400, 260}, "用户中心", 2500*time.Millisecond) {
			continue
		}
		if a.dismissEntryPopups(ctx, ctrl, img) {
			time.Sleep(800 * time.Millisecond)
			continue
		}
		clickRect(ctrl, maa.Rect{35, 35, 55, 55})
		time.Sleep(2 * time.Second)
	}
	return errors.New("从庭院退出至登录选号页超时")
}

func (a *SwitchAccount) loginAndSelectPlatform(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params) error {
	a.detail(hook, "正在切换账号")
	openDeadline := time.Now().Add(45 * time.Second)
	panelOpened := false
	toolbarClicks := 0
	for time.Now().Before(openDeadline) {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		// Clear foreground engine-update / session-expired modals first.
		if a.dismissStartupPopups(ctx, ctrl, img) {
			time.Sleep(1000 * time.Millisecond)
			continue
		}
		if a.hasSelectServerUI(ctx, img) {
			clickRect(ctrl, maa.Rect{50, 70, 60, 360})
			time.Sleep(time.Second)
			continue
		}
		if a.hasNeteaseAccountDialog(ctx, img) {
			panelOpened = true
			break
		}
		if a.clickTpl(ctx, ctrl, img, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8, 1500*time.Millisecond) ||
			a.clickOCR(ctx, ctrl, img, maa.Rect{850, 140, 300, 150}, "切换账号|切换用户", 1500*time.Millisecond) {
			continue
		}
		// Click right-side User Center [1210, 235, 45, 50]; alternate with Account icon [1210, 315, 45, 50] if unresponsive.
		if toolbarClicks%2 == 1 {
			clickRect(ctrl, maa.Rect{1210, 315, 45, 50})
		} else {
			clickRect(ctrl, maa.Rect{1210, 235, 45, 50})
		}
		toolbarClicks++
		time.Sleep(1500 * time.Millisecond)
	}
	if !panelOpened {
		return errors.New("未能打开网易账号登录面板")
	}

	accountVerified, needPassword, err := a.selectStoredAccount(ctx, ctrl, hook, p)
	if err != nil {
		return err
	}
	if needPassword {
		if err := a.passwordFallback(ctx, ctrl, hook, p); err != nil {
			return err
		}
	} else if accountVerified {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		if !a.clickTpl(ctx, ctrl, img, tplAccountLoginBtn, maa.Rect{400, 400, 480, 90}, 0.8, 1500*time.Millisecond) {
			clickRect(ctrl, maa.Rect{535, 420, 240, 60})
			time.Sleep(1500 * time.Millisecond)
		}
	}

	a.detail(hook, "正在选择登录平台")
	platDeadline := time.Now().Add(35 * time.Second)
	passwordRetried := needPassword
	for time.Now().Before(platDeadline) {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		appleBox := a.matchTemplate(ctx, img, tplPlatformApple, maa.Rect{475, 320, 165, 165}, 0.8)
		androidBox := a.matchTemplate(ctx, img, tplPlatformAndroid, maa.Rect{640, 320, 165, 165}, 0.8)
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
			time.Sleep(1200 * time.Millisecond)
			afterImg, err := a.snap(ctx, ctrl)
			if err != nil {
				return err
			}
			if !a.hasPlatformDialog(ctx, afterImg) {
				return nil
			}
			continue
		}
		if a.clickOCR(ctx, ctrl, img, maa.Rect{420, 350, 440, 170}, "^(同意|同意并继续)$", time.Second) {
			continue
		}
		if !passwordRetried && a.isPasswordPrompt(ctx, img) {
			passwordRetried = true
			if err := a.passwordFallback(ctx, ctrl, hook, p); err != nil {
				return err
			}
			platDeadline = time.Now().Add(25 * time.Second)
			continue
		}
		if a.dismissStartupPopups(ctx, ctrl, img) {
			time.Sleep(800 * time.Millisecond)
			continue
		}
		time.Sleep(600 * time.Millisecond)
	}
	return fmt.Errorf("未出现或未能完成 %s 平台选择弹窗", p.Platform)
}

func (a *SwitchAccount) selectStoredAccount(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params) (bool, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return false, false, err
		}
		if a.isPasswordPrompt(ctx, img) {
			return false, true, nil
		}
		if a.matchSelectedEmail(ctx, img, p.Email) {
			return true, false, nil
		}
		if !a.clickTpl(ctx, ctrl, img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78, time.Second) {
			clickRect(ctrl, maa.Rect{850, 320, 30, 25})
			time.Sleep(time.Second)
		}

		var prevSnapshot string
		for page := 0; page < 6; page++ {
			listImg, err := a.snap(ctx, ctrl)
			if err != nil {
				return false, false, err
			}
			items := a.ocrItems(ctx, listImg, maa.Rect{460, 280, 440, 330})
			var texts []string
			var hitBox *maa.Rect
			for _, it := range items {
				texts = append(texts, it.Text)
				if MatchEmail(it.Text, p.Email) {
					b := normalizeOCRBox(it.Box, maa.Rect{460, 280, 440, 330})
					hitBox = &b
					break
				}
			}
			if hitBox != nil {
				clickRect(ctrl, *hitBox)
				time.Sleep(time.Second)
				verifyImg, err := a.snap(ctx, ctrl)
				if err != nil {
					return false, false, err
				}
				if a.matchSelectedEmail(ctx, verifyImg, p.Email) {
					a.logf(hook, "info", p.Password, "已选中并核对历史账号: %s", p.Email)
					return true, false, nil
				}
				break
			}
			if a.hasTpl(ctx, listImg, tplDropdownAddAccount, maa.Rect{400, 540, 220, 60}, 0.8) ||
				a.findOCRBox(ctx, listImg, maa.Rect{400, 470, 480, 160}, "添加新账号|其他账号|其他方式") != nil {
				return false, true, nil
			}
			snapshot := strings.Join(texts, "|")
			if snapshot != "" && snapshot == prevSnapshot {
				return false, true, nil
			}
			prevSnapshot = snapshot
			ctrl.PostSwipe(615, 520, 615, 285, 800*time.Millisecond).Wait()
			time.Sleep(1100 * time.Millisecond)
		}
	}
	return false, true, nil
}

func (a *SwitchAccount) passwordFallback(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params) error {
	if strings.TrimSpace(p.Password) == "" {
		return fmt.Errorf("历史列表未找到账号 %s 或登录已过期，需要密码但未传入 password", p.Email)
	}
	a.detail(hook, "正在执行安全密码登录兜底")
	a.logf(hook, "info", p.Password, "使用 Win32 窗口消息为账号 %s 执行密码兜底登录", p.Email)

	img, err := a.snap(ctx, ctrl)
	if err != nil {
		return err
	}
	if a.clickTpl(ctx, ctrl, img, tplDropdownAddAccount, maa.Rect{400, 540, 220, 60}, 0.8, 1200*time.Millisecond) ||
		a.clickOCR(ctx, ctrl, img, maa.Rect{390, 450, 500, 190}, "添加新账号|其他账号|使用其他账号", 1200*time.Millisecond) {
		if img, err = a.snap(ctx, ctrl); err != nil {
			return err
		}
	}
	if a.findOCRBox(ctx, img, maa.Rect{380, 240, 520, 240}, "获取验证码|短信") != nil &&
		a.clickOCR(ctx, ctrl, img, maa.Rect{380, 180, 520, 380}, "密码登录|邮箱登录|网易邮箱", time.Second) {
		if img, err = a.snap(ctx, ctrl); err != nil {
			return err
		}
	}
	if !a.matchEmailInROI(ctx, img, maa.Rect{410, 250, 450, 85}, p.Email) {
		emailField := maa.Rect{520, 285, 220, 36}
		if box := a.findOCRBox(ctx, img, maa.Rect{410, 245, 450, 90}, "请输入邮箱|邮箱账号|网易邮箱|输入账号|账号"); box != nil {
			emailField = *box
		}
		clickRect(ctrl, emailField)
		time.Sleep(500 * time.Millisecond)
		if err := sendMuMuText(p.MumuIndex, p.AdbPath, p.AdbAddr, p.Email, 32); err != nil {
			return fmt.Errorf("发送邮箱字符失败: %w", err)
		}
		time.Sleep(400 * time.Millisecond)
	}

	if img, err = a.snap(ctx, ctrl); err != nil {
		return err
	}
	pwdField := maa.Rect{520, 350, 220, 36}
	if box := a.findOCRBox(ctx, img, maa.Rect{410, 315, 450, 95}, "请输入密码|输入密码|密码"); box != nil {
		pwdField = *box
	}
	clickRect(ctrl, pwdField)
	time.Sleep(500 * time.Millisecond)
	if err := sendMuMuText(p.MumuIndex, p.AdbPath, p.AdbAddr, p.Password, 24); err != nil {
		return errors.New(RedactSecrets(fmt.Sprintf("发送登录凭证窗口消息失败: %v", err), p.Password))
	}
	time.Sleep(500 * time.Millisecond)

	submitLogin := func(frame image.Image) {
		if !a.clickTpl(ctx, ctrl, frame, tplAccountLoginBtn, maa.Rect{400, 380, 480, 140}, 0.8, 1500*time.Millisecond) &&
			!a.clickOCR(ctx, ctrl, frame, maa.Rect{440, 380, 400, 130}, "^登录$|立即登录", 1500*time.Millisecond) {
			clickRect(ctrl, maa.Rect{535, 420, 240, 60})
			time.Sleep(1500 * time.Millisecond)
		}
	}
	if img, err = a.snap(ctx, ctrl); err != nil {
		return err
	}
	submitLogin(img)

	postImg, err := a.snap(ctx, ctrl)
	if err != nil {
		return err
	}
	if a.clickOCR(ctx, ctrl, postImg, maa.Rect{420, 340, 440, 180}, "^(同意|同意并继续)$", 1500*time.Millisecond) {
		return nil
	}
	if agreeLine := a.findOCRBox(ctx, postImg, maa.Rect{400, 410, 480, 120}, "我已阅读|阅读并同意|服务条款"); agreeLine != nil &&
		a.isPasswordPrompt(ctx, postImg) {
		ctrl.PostClick(int32(agreeLine.X()-16), int32(agreeLine.Y()+agreeLine.Height()/2)).Wait()
		time.Sleep(500 * time.Millisecond)
		submitLogin(postImg)
	}
	return nil
}

func (a *SwitchAccount) verifyAndSwitchServer(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook, p Params) error {
	a.detail(hook, "正在核验区服")
	var loginImg image.Image
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		// Always dismiss foreground modals before evaluating login home / server bar.
		if a.dismissStartupPopups(ctx, ctrl, img) {
			time.Sleep(time.Second)
			continue
		}
		if !a.hasNeteaseAccountDialog(ctx, img) &&
			(a.hasTpl(ctx, img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) ||
				a.findOCRBox(ctx, img, maa.Rect{480, 545, 320, 95}, "进入游戏") != nil ||
				a.findOCRBox(ctx, img, maa.Rect{660, 495, 150, 52}, "切换") != nil) {
			loginImg = img
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	if loginImg == nil {
		return errors.New("未返回登录首页，无法核验区服")
	}
	if a.readAndMatchServer(ctx, loginImg, p.Server) {
		a.logf(hook, "info", p.Password, "当前区服已确认为: %s", p.Server)
		return nil
	}

	a.detail(hook, "正在切换目标区服")
	svrSelectOpened := false
	curImg := loginImg
	for i := 0; i < 4; i++ {
		if !a.clickOCR(ctx, ctrl, curImg, maa.Rect{660, 495, 150, 52}, "切换", 1500*time.Millisecond) {
			clickRect(ctrl, maa.Rect{550, 505, 250, 40})
			time.Sleep(1500 * time.Millisecond)
		}
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		curImg = img
		if a.hasSelectServerUI(ctx, img) {
			svrSelectOpened = true
			break
		}
	}
	if !svrSelectOpened {
		return errors.New("未能打开区服选择界面")
	}

	for i := 0; i < 3; i++ {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		if a.hasTpl(ctx, img, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8) {
			break
		}
		if a.hasTpl(ctx, img, tplSelectSvr1, maa.Rect{195, 115, 220, 95}, 0.8) {
			clickRect(ctrl, maa.Rect{205, 570, 815, 40})
			time.Sleep(1500 * time.Millisecond)
			continue
		}
		if !a.clickOCR(ctx, ctrl, img, maa.Rect{200, 510, 220, 75}, "已有角色", 1500*time.Millisecond) {
			break
		}
	}

	clickedServer := false
	var lastSig string
	for swipeIdx := 0; swipeIdx < 6 && !clickedServer; swipeIdx++ {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		bottomItems := a.ocrItems(ctx, img, maa.Rect{205, 520, 825, 65})
		var sigParts []string
		for _, it := range bottomItems {
			sigParts = append(sigParts, it.Text)
			if MatchServer(it.Text, p.Server) {
				clickRect(ctrl, normalizeOCRBox(it.Box, maa.Rect{205, 520, 825, 65}))
				clickedServer = true
				break
			}
		}
		if !clickedServer {
			for _, it := range a.ocrItems(ctx, img, maa.Rect{360, 130, 720, 480}) {
				sigParts = append(sigParts, it.Text)
				if (p.Character != "" && strings.TrimLeft(it.Text, "0123456789 []【】()（）") == p.Character) || MatchServer(it.Text, p.Server) {
					b := normalizeOCRBox(it.Box, maa.Rect{360, 130, 720, 480})
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
			ctrl.PostSwipe(730, 550, 450, 550, 800*time.Millisecond).Wait()
		} else {
			ctrl.PostSwipe(600, 520, 600, 270, 800*time.Millisecond).Wait()
		}
		time.Sleep(1500 * time.Millisecond)
	}
	if !clickedServer {
		clickRect(ctrl, maa.Rect{50, 70, 60, 360})
		return fmt.Errorf("未在区服/已有角色列表中找到目标区服: %s", p.Server)
	}

	for verifyDeadline := time.Now().Add(15 * time.Second); time.Now().Before(verifyDeadline); {
		time.Sleep(1200 * time.Millisecond)
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		if a.hasSelectServerUI(ctx, img) {
			clickRect(ctrl, maa.Rect{50, 70, 60, 360})
			continue
		}
		if a.readAndMatchServer(ctx, img, p.Server) {
			a.logf(hook, "info", p.Password, "切换后已二次核对区服一致: %s", p.Server)
			return nil
		}
	}
	return fmt.Errorf("切换后底部区服名仍与目标 %s 不一致", p.Server)
}

func (a *SwitchAccount) enterAndConfirmCourtyard(ctx *maa.Context, ctrl *maa.Controller, hook *RuntimeHook) error {
	a.detail(hook, "正在进入庭院")
	clickRect(ctrl, maa.Rect{570, 580, 130, 35})
	lastEnterClick := time.Now()
	courtyardHits := 0
	var lastCourtyardAt time.Time

	for deadline := time.Now().Add(95 * time.Second); time.Now().Before(deadline); {
		img, err := a.snap(ctx, ctrl)
		if err != nil {
			return err
		}
		if a.findOCRBox(ctx, img, maa.Rect{500, 610, 280, 100}, "创建角色") != nil {
			return errors.New("检测到创建角色界面，目标区服无已有角色，已中止进入")
		}
		if a.dismissEntryPopups(ctx, ctrl, img) {
			courtyardHits = 0
			time.Sleep(900 * time.Millisecond)
			continue
		}
		if a.isCourtyard(ctx, img) {
			if courtyardHits == 0 {
				courtyardHits, lastCourtyardAt = 1, time.Now()
				time.Sleep(1200 * time.Millisecond)
				continue
			}
			if time.Since(lastCourtyardAt) >= time.Second {
				return nil
			}
			time.Sleep(time.Second - time.Since(lastCourtyardAt) + 100*time.Millisecond)
			continue
		}
		courtyardHits = 0

		if a.hasSelectServerUI(ctx, img) {
			ctrl.PostClick(106, 535).Wait()
			time.Sleep(time.Second)
			continue
		}
		if a.findOCRBox(ctx, img, maa.Rect{0, 0, 180, 120}, "选择角色") != nil {
			clickRect(ctrl, maa.Rect{600, 240, 500, 400})
			time.Sleep(1500 * time.Millisecond)
			continue
		}
		if time.Since(lastEnterClick) >= 3500*time.Millisecond {
			if a.hasTpl(ctx, img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) {
				clickRect(ctrl, maa.Rect{570, 580, 130, 35})
				lastEnterClick = time.Now()
				time.Sleep(1500 * time.Millisecond)
				continue
			}
			if a.clickOCR(ctx, ctrl, img, maa.Rect{450, 545, 380, 100}, "进入游戏|点击屏幕进入游戏", 1500*time.Millisecond) {
				lastEnterClick = time.Now()
				continue
			}
		}
		time.Sleep(900 * time.Millisecond)
	}
	return errors.New("等待进入庭院超时")
}

func (a *SwitchAccount) hasSelectServerUI(ctx *maa.Context, img image.Image) bool {
	return a.hasTpl(ctx, img, tplSelectSvr1, maa.Rect{195, 115, 220, 95}, 0.8) ||
		a.hasTpl(ctx, img, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8) ||
		a.findOCRBox(ctx, img, maa.Rect{200, 120, 220, 80}, "已有角色") != nil ||
		a.findOCRBox(ctx, img, maa.Rect{200, 510, 220, 75}, "已有角色") != nil
}

func (a *SwitchAccount) hasPlatformDialog(ctx *maa.Context, img image.Image) bool {
	return a.hasTpl(ctx, img, tplPlatformApple, maa.Rect{475, 320, 165, 165}, 0.8) ||
		a.hasTpl(ctx, img, tplPlatformAndroid, maa.Rect{640, 320, 165, 165}, 0.8)
}

func (a *SwitchAccount) hasNeteaseAccountDialog(ctx *maa.Context, img image.Image) bool {
	if a.hasPlatformDialog(ctx, img) {
		return false
	}
	return a.hasTpl(ctx, img, tplNeteaseLogo, maa.Rect{460, 145, 360, 130}, 0.8) ||
		a.hasTpl(ctx, img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) ||
		a.isPasswordPrompt(ctx, img) ||
		a.findOCRBox(ctx, img, maa.Rect{400, 150, 480, 120}, "网易游戏|网易账号|邮箱账号|账号登录") != nil
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

func (a *SwitchAccount) clickOCR(ctx *maa.Context, ctrl *maa.Controller, img image.Image, roi maa.Rect, expr string, wait time.Duration) bool {
	box := a.findOCRBox(ctx, img, roi, expr)
	if box == nil {
		return false
	}
	clickRect(ctrl, *box)
	if wait > 0 {
		time.Sleep(wait)
	}
	return true
}

func (a *SwitchAccount) snap(ctx *maa.Context, ctrl *maa.Controller) (image.Image, error) {
	if !ctrl.PostScreencap().Wait().Success() {
		return nil, errors.New("设备截图失败")
	}
	img, err := ctrl.CacheImage()
	if err != nil || img == nil {
		return nil, fmt.Errorf("读取缓存帧失败: %w", err)
	}
	// Global displaced-login guard: never click reconnect if another device logged in.
	for _, it := range a.ocrItems(ctx, img, maa.Rect{280, 180, 720, 320}) {
		if displacedExpr.MatchString(it.Text) {
			return nil, errDisplaced
		}
	}
	return img, nil
}

func (a *SwitchAccount) isLoginSurface(ctx *maa.Context, img image.Image) bool {
	return a.hasTpl(ctx, img, tplLoginForm, maa.Rect{1, 540, 250, 120}, 0.8) ||
		a.hasNeteaseAccountDialog(ctx, img) ||
		a.hasTpl(ctx, img, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8) ||
		a.hasPlatformDialog(ctx, img) ||
		a.hasSelectServerUI(ctx, img) ||
		a.findOCRBox(ctx, img, maa.Rect{480, 545, 320, 95}, "进入游戏") != nil
}

func (a *SwitchAccount) isCourtyard(ctx *maa.Context, img image.Image) bool {
	for _, node := range []string{
		"导航-庭院-识别是否位于庭院-模型识别",
		"导航-庭院-识别是否位于庭院",
		"导航-庭院-识别是否位于庭院-模板匹配",
		"打开底部菜单_匹配",
	} {
		if detail, err := ctx.RunRecognition(node, img); err == nil && detail != nil && detail.Hit {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) isCourtyardOrProfile(ctx *maa.Context, img image.Image) bool {
	if a.isCourtyard(ctx, img) {
		return true
	}
	return a.matchTemplate(ctx, img, tplUserCenterProfile, maa.Rect{590, 60, 90, 55}, 0.8) != nil ||
		a.matchTemplate(ctx, img, tplUserCenterBtn, maa.Rect{190, 390, 330, 200}, 0.8) != nil
}

func (a *SwitchAccount) isPasswordPrompt(ctx *maa.Context, img image.Image) bool {
	if a.hasTpl(ctx, img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
		return false
	}
	return a.findOCRBox(ctx, img, maa.Rect{380, 200, 520, 290}, "请输入密码|输入密码|密码登录|请输入邮箱|获取验证码") != nil
}

func (a *SwitchAccount) dismissStartupPopups(ctx *maa.Context, ctrl *maa.Controller, img image.Image) bool {
	// 1. Engine update modal ("发现全新的引擎版本！"): strictly click left button "暂不更新" (never "后台下载").
	if a.findOCRBox(ctx, img, maa.Rect{340, 200, 600, 220}, "发现全新的引擎版本|全新的引擎|引擎版本") != nil {
		if !a.clickOCR(ctx, ctrl, img, maa.Rect{360, 340, 560, 170}, "暂不更新|以后再说|暂不升级", 0) {
			clickRect(ctrl, maa.Rect{445, 412, 173, 47})
		}
		return true
	}
	if a.clickOCR(ctx, ctrl, img, maa.Rect{360, 340, 560, 170}, "^(暂不更新|以后再说|暂不升级)$", 0) {
		return true
	}

	// 2. Session expired modal ("用户登录失效。"): click centered red button "^重新登录$" to open NetEase login dialog.
	if a.findOCRBox(ctx, img, maa.Rect{380, 240, 520, 160}, "用户登录失效|登录失效|登录已失效") != nil {
		if !a.clickOCR(ctx, ctrl, img, maa.Rect{450, 360, 380, 130}, "^重新登录$", 0) {
			clickRect(ctrl, maa.Rect{536, 403, 208, 62})
		}
		return true
	}
	if !a.hasNeteaseAccountDialog(ctx, img) && a.clickOCR(ctx, ctrl, img, maa.Rect{450, 360, 380, 130}, "^重新登录$", 0) {
		return true
	}

	// 3. Client update notice.
	if a.runNodeIfHit(ctx, img, "客户端更新提示处理") {
		return true
	}
	// 4. Cancel illustration / resource download or CG video.
	if a.findOCRBox(ctx, img, maa.Rect{320, 170, 640, 270}, "下载插画|资源下载|观看视频|是否打开|加成") != nil {
		if cancel := a.findOCRBox(ctx, img, maa.Rect{280, 330, 420, 220}, "取消|拒绝|不观看|暂不"); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
	}
	if a.runNodeIfHit(ctx, img, "通用-拒绝下载-识别拒绝后点击") {
		return true
	}
	// 5. Announcements and ads.
	if a.runNodeIfHit(ctx, img, "关闭公告") || a.runNodeIfHit(ctx, img, "关闭广告-模板匹配") {
		return true
	}
	return a.dismissPhoneBind(ctx, ctrl, img)
}

func (a *SwitchAccount) dismissPhoneBind(ctx *maa.Context, ctrl *maa.Controller, img image.Image) bool {
	if box := a.matchTemplate(ctx, img, tplCancelBindPhone, maa.Rect{420, 430, 220, 140}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if box := a.matchTemplate(ctx, img, tplGotoBindPhone, maa.Rect{900, 410, 230, 180}, 0.8); box != nil {
		clickRect(ctrl, *box)
		time.Sleep(800 * time.Millisecond)
		if after, err := a.snap(ctx, ctrl); err == nil {
			if cBox := a.matchTemplate(ctx, after, tplCancelBindPhone, maa.Rect{420, 430, 220, 140}, 0.8); cBox != nil {
				clickRect(ctrl, *cBox)
			}
		}
		return true
	}
	if a.findOCRBox(ctx, img, maa.Rect{360, 180, 560, 260}, "绑定手机|手机绑定") != nil {
		if cancel := a.findOCRBox(ctx, img, maa.Rect{380, 380, 300, 190}, "取消|暂不|关闭"); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
	}
	return false
}

func (a *SwitchAccount) dismissEntryPopups(ctx *maa.Context, ctrl *maa.Controller, img image.Image) bool {
	// Reject co-op / foster invites on left banner.
	if box := a.matchTemplate(ctx, img, tplRejectInvite, maa.Rect{5, 200, 120, 320}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if a.dismissStartupPopups(ctx, ctrl, img) {
		return true
	}
	// Cancel battle resume prompt.
	if a.findOCRBox(ctx, img, maa.Rect{360, 220, 560, 220}, "继续战斗|是否继续") != nil {
		if cancel := a.findOCRBox(ctx, img, maa.Rect{400, 360, 260, 130}, "取消"); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
	}
	for _, node := range []string{
		"导航-庭院-关闭当前弹窗",
		"导航-庭院-关闭意外弹窗",
		"导航-关闭点击屏幕继续的界面",
	} {
		if a.runNodeIfHit(ctx, img, node) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) runNodeIfHit(ctx *maa.Context, img image.Image, node string) bool {
	detail, err := ctx.RunRecognition(node, img)
	if err != nil || detail == nil || !detail.Hit {
		return false
	}
	act, err := ctx.RunAction(node, detail.Box, detail.DetailJson)
	return err == nil && act != nil && act.Success
}

func (a *SwitchAccount) matchSelectedEmail(ctx *maa.Context, img image.Image, email string) bool {
	return a.matchEmailInROI(ctx, img, maa.Rect{460, 280, 370, 50}, email)
}

func (a *SwitchAccount) matchEmailInROI(ctx *maa.Context, img image.Image, roi maa.Rect, email string) bool {
	for _, it := range a.ocrItems(ctx, img, roi) {
		if MatchEmail(it.Text, email) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) readAndMatchServer(ctx *maa.Context, img image.Image, server string) bool {
	for _, roi := range []maa.Rect{{460, 495, 255, 52}, {450, 495, 340, 55}} {
		for _, it := range a.ocrItems(ctx, img, roi) {
			if MatchServer(it.Text, server) {
				return true
			}
		}
	}
	return false
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

func (a *SwitchAccount) findOCRBox(ctx *maa.Context, img image.Image, roi maa.Rect, expr string) *maa.Rect {
	detail, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{
		ROI:      maa.NewTargetRect(roi),
		Expected: []string{expr},
	}, img)
	if err != nil || detail == nil || !detail.Hit {
		return nil
	}
	b := normalizeOCRBox(detail.Box, roi)
	return &b
}

func (a *SwitchAccount) ocrItems(ctx *maa.Context, img image.Image, roi maa.Rect) []maa.OCRResult {
	detail, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{
		ROI: maa.NewTargetRect(roi),
	}, img)
	if err != nil || detail == nil {
		return nil
	}
	var out []maa.OCRResult
	if detail.Results != nil {
		for _, r := range detail.Results.All {
			if r == nil {
				continue
			}
			if ocr, ok := r.AsOCR(); ok && ocr != nil && strings.TrimSpace(ocr.Text) != "" {
				out = append(out, *ocr)
			}
		}
	}
	if len(out) == 0 {
		for _, txt := range ocrtext.Texts(detail) {
			out = append(out, maa.OCRResult{Box: detail.Box, Text: txt})
		}
	}
	return out
}

func normalizeOCRBox(box, roi maa.Rect) maa.Rect {
	if box.X() < roi.X() && box.Y() < roi.Y() && roi.X() >= 150 && roi.Y() >= 100 {
		return maa.Rect{roi.X() + box.X(), roi.Y() + box.Y(), box.Width(), box.Height()}
	}
	return box
}

func clickRect(ctrl *maa.Controller, r maa.Rect) {
	x := int32(r.X() + max(1, r.Width())/2)
	y := int32(r.Y() + max(1, r.Height())/2)
	ctrl.PostClick(x, y).Wait()
}

func (a *SwitchAccount) fail(msg string, displaced bool, secret string) {
	safe := RedactSecrets(msg, secret)
	a.mu.Lock()
	a.outcome = Outcome{Displaced: displaced, LastError: safe}
	a.mu.Unlock()
}

func (a *SwitchAccount) detail(hook *RuntimeHook, d string) {
	if hook != nil && hook.OnDetail != nil {
		hook.OnDetail(d)
	}
}

func (a *SwitchAccount) logf(hook *RuntimeHook, level, secret, format string, args ...any) {
	msg := RedactSecrets(fmt.Sprintf(format, args...), secret)
	if hook != nil && hook.OnLog != nil {
		hook.OnLog(level, msg)
		return
	}
	if level == "error" {
		log.Error().Msg(msg)
	} else {
		log.Info().Msg(msg)
	}
}
