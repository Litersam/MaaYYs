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
	"unicode"

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
	regexCache    sync.Map
)

const (
	fastPollInterval = 260 * time.Millisecond
	postClickSettle  = 240 * time.Millisecond
)

// Frame wraps a captured 1280x720 screenshot with a lazy single-pass full-frame OCR cache,
// eliminating 15-20 redundant per-ROI PaddleOCR inferences per loop tick.
type Frame struct {
	img     image.Image
	ocrDone bool
	items   []maa.OCRResult
}

// NewFrame wraps img into a Frame for cached recognition.
func NewFrame(img image.Image) *Frame {
	return &Frame{img: img}
}

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

func normEmail(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "＠", "@")
	return strings.ToLower(s)
}

// MatchExactEmail checks whether OCR text matches the full target email without ellipsis truncation.
func MatchExactEmail(ocrText, targetEmail string) bool {
	got, want := normEmail(ocrText), normEmail(targetEmail)
	return got != "" && want != "" && got == want
}

// MatchEmail checks whether OCR text matches target email strictly, including NetEase's >19-char
// trailing-ellipsis truncation in stored-account cards (e.g. "lishennanus300@163.c..." for "lishennanus300@163.com").
func MatchEmail(ocrText, targetEmail string) bool {
	got, want := normEmail(ocrText), normEmail(targetEmail)
	if got == "" || want == "" {
		return false
	}
	if got == want {
		return true
	}
	if strings.HasSuffix(got, "..") || strings.HasSuffix(got, "…") {
		prefix := strings.TrimRight(got, ".…")
		atIdx := strings.IndexByte(prefix, '@')
		if len(prefix) >= 15 && atIdx > 0 && atIdx < len(prefix)-1 && strings.HasPrefix(want, prefix) {
			return true
		}
	}
	return false
}

// MatchServer checks whether OCR server text matches target server name.
func MatchServer(ocrText, targetServer string) bool {
	clean := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, " ", "")
		if idx := strings.Index(s, "切换"); idx > 0 {
			s = s[:idx]
		}
		return strings.TrimFunc(s, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
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
	totalStart := time.Now()

	// Step 1 & 2: Ensure game is running in foreground; fast-skip opening CG; if in courtyard, exit to login page.
	inGame, curFrame, coldStarted, cgTaps, err := a.ensureSurface(ctx, ctrl, hook)
	if err != nil {
		return err
	}
	if inGame {
		curFrame, err = a.exitCourtyardToLogin(ctx, ctrl, hook)
		if err != nil {
			return err
		}
	}
	surfaceDur := time.Since(totalStart)
	a.logf(hook, "info", p.Password, "[阶段耗时] 拉起至首帧登录页: %.2fs (冷启动=%v, 跳过CG=%d次, 原在庭院=%v)", surfaceDur.Seconds(), coldStarted, cgTaps, inGame)

	// Step 3 & 4: Select/authenticate NetEase email (with safe Win32 password fallback) & select platform.
	switchStart := time.Now()
	afterAuthFrame, err := a.loginAndSelectPlatform(ctx, ctrl, hook, p, curFrame)
	if err != nil {
		return err
	}
	authDur := time.Since(switchStart)

	// Step 5: Verify bottom server name; switch server via existing-characters panel if mismatched.
	svrStart := time.Now()
	if _, err := a.verifyAndSwitchServer(ctx, ctrl, hook, p, afterAuthFrame); err != nil {
		return err
	}
	svrDur := time.Since(svrStart)
	switchDur := time.Since(switchStart)
	a.logf(hook, "info", p.Password, "[阶段耗时] 账号切换交互: %.2fs (账号与平台=%.2fs, 区服核验=%.2fs)", switchDur.Seconds(), authDur.Seconds(), svrDur.Seconds())

	// Step 6: Enter game and require 2 consecutive courtyard detections >= 1s apart.
	enterStart := time.Now()
	if err := a.enterAndConfirmCourtyard(ctx, ctrl, hook); err != nil {
		return err
	}
	enterDur := time.Since(enterStart)
	totalDur := time.Since(totalStart)
	a.logf(hook, "info", p.Password, "[阶段耗时] 进入游戏至庭院确认: %.2fs", enterDur.Seconds())
	a.logf(hook, "info", p.Password, "[登录耗时汇总] 总计=%.2fs | 拉起至首帧登录页=%.2fs | 账号切换交互=%.2fs | 进入游戏至庭院确认=%.2fs",
		totalDur.Seconds(), surfaceDur.Seconds(), switchDur.Seconds(), enterDur.Seconds())
	return nil
}

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
		if !coldStarted && (a.clickRecognizedNode(ctx, ctrl, f.img, "导航-庭院-点击返回按钮-模板匹配") ||
			a.clickRecognizedNode(ctx, ctrl, f.img, "导航-庭院-点击快捷返回庭院按钮")) {
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
		if a.findTextBox(ctx, f, maa.Rect{240, 560, 800, 155}, "正在解压|资源更新|正在下载|版本检查") != nil {
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
		if a.hasTpl(ctx, f.img, tplNeteaseLogo, maa.Rect{500, 170, 300, 90}, 0.8) ||
			a.hasTpl(ctx, f.img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) {
			return f, nil
		}
		if a.clickTpl(ctx, ctrl, f.img, tplSwitchAccountBtn, maa.Rect{930, 170, 160, 75}, 0.8, postClickSettle) ||
			a.clickOCRExact(ctx, ctrl, f, maa.Rect{880, 150, 240, 110}, "切换账号|切换用户", postClickSettle) {
			if nextF, ok, wErr := a.waitUntil(ctx, ctrl, 1400*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasTpl(ctx, cf.img, tplNeteaseLogo, maa.Rect{500, 170, 300, 90}, 0.8) ||
					a.hasTpl(ctx, cf.img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8)
			}); wErr != nil {
				return nil, wErr
			} else if ok {
				return nextF, nil
			}
			continue
		}
		if a.clickTpl(ctx, ctrl, f.img, tplUserCenterBtn, maa.Rect{190, 390, 330, 200}, 0.8, postClickSettle) ||
			(a.hasTpl(ctx, f.img, tplUserCenterProfile, maa.Rect{590, 60, 90, 55}, 0.8) &&
				a.clickOCRExact(ctx, ctrl, f, maa.Rect{150, 350, 400, 260}, "用户中心", postClickSettle)) {
			_, _, _ = a.waitUntil(ctx, ctrl, 1500*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasTpl(ctx, cf.img, tplSwitchAccountBtn, maa.Rect{930, 170, 160, 75}, 0.8) ||
					a.findTextBox(ctx, cf, maa.Rect{880, 150, 240, 110}, "切换账号|切换用户") != nil ||
					a.hasNeteaseAccountDialog(ctx, cf)
			})
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
		if a.clickTpl(ctx, ctrl, f.img, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8, postClickSettle) ||
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
				a.hasTpl(ctx, cf.img, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8) ||
				a.findTextBox(ctx, cf, maa.Rect{850, 140, 300, 150}, "切换账号|切换用户") != nil
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
		if !a.clickTpl(ctx, ctrl, curFrame.img, tplAccountLoginBtn, maa.Rect{400, 400, 480, 90}, 0.8, postClickSettle) {
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
		appleBox := a.matchTemplate(ctx, f.img, tplPlatformApple, maa.Rect{475, 320, 165, 165}, 0.8)
		androidBox := a.matchTemplate(ctx, f.img, tplPlatformAndroid, maa.Rect{640, 320, 165, 165}, 0.8)
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
		if a.findTextBox(ctx, f, maa.Rect{360, 210, 560, 260}, "二维码|使用.*扫码|扫码登录") != nil &&
			!a.hasTpl(ctx, f.img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
			clickRect(ctrl, maa.Rect{355, 225, 36, 36})
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if a.matchSelectedEmail(ctx, f, p.Email) {
			return true, false, f, nil
		}
		if !a.clickTpl(ctx, ctrl, f.img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78, postClickSettle) {
			clickRect(ctrl, maa.Rect{850, 320, 30, 25})
			time.Sleep(postClickSettle)
		}
		listReady, _, wErr := a.waitUntil(ctx, ctrl, 800*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return !a.hasTpl(ctx, cf.img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) ||
				len(cf.itemsInROI(ctx, a, maa.Rect{460, 280, 440, 330})) >= 2
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
			items := listF.itemsInROI(ctx, a, maa.Rect{460, 280, 440, 330})
			if len(items) == 0 {
				items = a.ocrItems(ctx, listF.img, maa.Rect{460, 280, 440, 330})
			}
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
			if a.hasTpl(ctx, listF.img, tplDropdownAddAccount, maa.Rect{400, 540, 220, 60}, 0.8) ||
				a.findTextBox(ctx, listF, maa.Rect{400, 470, 480, 160}, "添加新账号|其他账号|其他方式") != nil {
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
	if a.clickTpl(ctx, ctrl, f.img, tplDropdownAddAccount, maa.Rect{400, 540, 220, 60}, 0.8, postClickSettle) ||
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
			if latest, snapErr := a.snapFrame(ctx, ctrl); snapErr == nil {
				f = latest
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
	if a.findTextBox(ctx, f, maa.Rect{380, 240, 520, 240}, "获取验证码|短信") != nil &&
		a.clickOCRExact(ctx, ctrl, f, maa.Rect{380, 180, 520, 380}, "密码登录|邮箱登录|网易邮箱", postClickSettle) {
		if f, _, err = a.waitUntil(ctx, ctrl, 800*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.findTextBox(ctx, cf, maa.Rect{380, 240, 520, 240}, "获取验证码|短信") == nil
		}); err != nil {
			return nil, err
		}
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
	}

	ctrl.PostClick(540, 348).Wait()
	time.Sleep(480 * time.Millisecond)
	if err := sendMuMuText(p.MumuIndex, p.AdbPath, p.AdbAddr, p.Password); err != nil {
		return nil, errors.New(RedactSecrets(fmt.Sprintf("发送登录凭证窗口消息失败: %v", err), p.Password))
	}
	time.Sleep(220 * time.Millisecond)

	submitLogin := func(cur *Frame) (*Frame, error) {
		if cur != nil && a.clickTpl(ctx, ctrl, cur.img, tplAccountLoginBtn, maa.Rect{380, 395, 520, 85}, 0.8, 0) {
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
	needAgree := a.findTextBox(ctx, postF, maa.Rect{320, 500, 460, 135}, "请先勾选同意") != nil
	if needAgree || a.isPasswordPrompt(ctx, postF) {
		if agreeLine := a.findTextBox(ctx, postF, maa.Rect{380, 410, 520, 175}, "我已详细阅读|我已阅读|阅读并同意"); agreeLine != nil {
			ctrl.PostClick(int32(agreeLine.X()-16), int32(agreeLine.Y()+agreeLine.Height()/2)).Wait()
			time.Sleep(280 * time.Millisecond)
			postF, _ = submitLogin(nil)
		} else if needAgree {
			ctrl.PostClick(464, 544).Wait()
			time.Sleep(280 * time.Millisecond)
			postF, _ = submitLogin(nil)
		}
		if postF != nil && a.acceptAgreementModal(ctx, ctrl, postF) {
			return nil, nil
		}
	}
	return postF, nil
}

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
			(a.hasTpl(ctx, f.img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) ||
				a.findTextBox(ctx, f, maa.Rect{480, 545, 320, 95}, "进入游戏") != nil ||
				a.findTextBox(ctx, f, maa.Rect{660, 495, 150, 52}, "切换") != nil) {
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
		if curF == nil {
			var err error
			if curF, err = a.snapFrame(ctx, ctrl); err != nil {
				return nil, err
			}
		}
		if a.hasTpl(ctx, curF.img, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8) {
			break
		}
		if a.hasTpl(ctx, curF.img, tplSelectSvr1, maa.Rect{195, 115, 220, 95}, 0.8) {
			clickRect(ctrl, maa.Rect{205, 570, 815, 40})
			time.Sleep(postClickSettle)
			curF, _, _ = a.waitUntil(ctx, ctrl, 1000*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
				return a.hasTpl(ctx, cf.img, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8)
			})
			continue
		}
		if !a.clickOCRExact(ctx, ctrl, curF, maa.Rect{200, 510, 220, 75}, "已有角色", postClickSettle) {
			break
		}
		curF, _, _ = a.waitUntil(ctx, ctrl, 1000*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.hasTpl(ctx, cf.img, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8)
		})
	}

	clickedServer := false
	var lastSig string
	for swipeIdx := 0; swipeIdx < 6 && !clickedServer; swipeIdx++ {
		f, err := a.snapFrame(ctx, ctrl)
		if err != nil {
			return nil, err
		}
		bottomItems := f.itemsInROI(ctx, a, maa.Rect{205, 520, 825, 65})
		if len(bottomItems) == 0 {
			bottomItems = a.ocrItems(ctx, f.img, maa.Rect{205, 520, 825, 65})
		}
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
			for _, it := range f.itemsInROI(ctx, a, maa.Rect{360, 130, 720, 480}) {
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
		if a.findTextBox(ctx, f, maa.Rect{500, 610, 280, 100}, "创建角色") != nil {
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
		if a.findTextBox(ctx, f, maa.Rect{0, 0, 180, 120}, "选择角色") != nil {
			clickRect(ctrl, maa.Rect{600, 240, 500, 400})
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if time.Since(lastEnterClick) >= 1600*time.Millisecond {
			if a.hasTpl(ctx, f.img, tplLoginForm, maa.Rect{1, 547, 241, 105}, 0.8) {
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

func (a *SwitchAccount) hasSelectServerUI(ctx *maa.Context, f *Frame) bool {
	return a.hasTpl(ctx, f.img, tplSelectSvr1, maa.Rect{195, 115, 220, 95}, 0.8) ||
		a.hasTpl(ctx, f.img, tplSelectSvr2, maa.Rect{195, 115, 220, 95}, 0.8) ||
		a.findTextBox(ctx, f, maa.Rect{200, 120, 220, 80}, "已有角色") != nil ||
		a.findTextBox(ctx, f, maa.Rect{200, 510, 220, 75}, "已有角色") != nil
}

func (a *SwitchAccount) hasPlatformDialog(ctx *maa.Context, f *Frame) bool {
	return f != nil && (a.hasTpl(ctx, f.img, tplPlatformApple, maa.Rect{475, 320, 165, 165}, 0.8) ||
		a.hasTpl(ctx, f.img, tplPlatformAndroid, maa.Rect{640, 320, 165, 165}, 0.8))
}

func (a *SwitchAccount) hasNeteaseAccountDialog(ctx *maa.Context, f *Frame) bool {
	if f == nil || a.hasPlatformDialog(ctx, f) {
		return false
	}
	return a.hasTpl(ctx, f.img, tplNeteaseLogo, maa.Rect{460, 145, 360, 130}, 0.8) ||
		a.hasTpl(ctx, f.img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) ||
		a.hasAgreementModal(ctx, f) ||
		a.isLoginMethodChooser(ctx, f) ||
		a.isPasswordPrompt(ctx, f) ||
		a.findTextBox(ctx, f, maa.Rect{400, 150, 480, 120}, "网易游戏|网易账号|邮箱账号|账号登录") != nil
}

func (a *SwitchAccount) hasAgreementModal(ctx *maa.Context, f *Frame) bool {
	return f != nil && (a.findTextBox(ctx, f, maa.Rect{450, 170, 380, 95}, "用户协议及隐私保护") != nil ||
		a.findTextBox(ctx, f, maa.Rect{420, 340, 470, 200}, "^(同意|同意并继续|同意并登录)$") != nil)
}

func (a *SwitchAccount) acceptAgreementModal(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	if f == nil {
		return false
	}
	if a.findTextBox(ctx, f, maa.Rect{420, 340, 470, 200}, "^(同意|同意并继续|同意并登录)$") != nil {
		if a.clickOCRExact(ctx, ctrl, f, maa.Rect{420, 340, 470, 200}, "^(同意|同意并继续|同意并登录)$", postClickSettle) {
			return true
		}
	}
	if a.findTextBox(ctx, f, maa.Rect{450, 170, 380, 95}, "用户协议及隐私保护") != nil {
		if !a.clickOCRExact(ctx, ctrl, f, maa.Rect{420, 340, 470, 200}, "^(同意|同意并继续|同意并登录)$", postClickSettle) {
			ctrl.PostClick(761, 487).Wait()
			time.Sleep(postClickSettle)
		}
		return true
	}
	return false
}

func (a *SwitchAccount) isLoginMethodChooser(ctx *maa.Context, f *Frame) bool {
	if f == nil || a.hasTpl(ctx, f.img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
		return false
	}
	if a.findTextBox(ctx, f, maa.Rect{360, 290, 560, 190}, "手机账号|扫码登录|快速游戏") != nil {
		return true
	}
	return a.findTextBox(ctx, f, maa.Rect{360, 290, 560, 190}, "网易邮箱") != nil &&
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
	box := a.findTextBox(ctx, f, roi, expr)
	if box == nil {
		box = a.findCroppedOCRBox(ctx, f.img, roi, expr)
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
	if !ctrl.PostScreencap().Wait().Success() {
		return nil, errors.New("设备截图失败")
	}
	img, err := ctrl.CacheImage()
	if err != nil || img == nil {
		return nil, fmt.Errorf("读取缓存帧失败: %w", err)
	}
	f := &Frame{img: img}
	// Global displaced-login guard (also warms the single-pass full-frame OCR cache for this frame).
	for _, it := range f.itemsInROI(ctx, a, maa.Rect{280, 180, 720, 320}) {
		if displacedExpr.MatchString(it.Text) {
			return nil, errDisplaced
		}
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
	return a.hasTpl(ctx, f.img, tplLoginForm, maa.Rect{1, 540, 250, 120}, 0.8) ||
		a.hasNeteaseAccountDialog(ctx, f) ||
		a.hasTpl(ctx, f.img, tplSwitchAccountBtn, maa.Rect{900, 155, 210, 100}, 0.8) ||
		a.hasPlatformDialog(ctx, f) ||
		a.hasSelectServerUI(ctx, f) ||
		a.findTextBox(ctx, f, maa.Rect{480, 545, 320, 95}, "进入游戏") != nil
}

func (a *SwitchAccount) isCourtyard(ctx *maa.Context, f *Frame) bool {
	// Check fast template/feature matches (~3ms) and cached OCR (~0ms) BEFORE YOLOv8 ONNX (~120ms).
	for _, node := range []string{
		"导航-庭院-识别是否位于庭院-模板匹配",
		"打开底部菜单_匹配",
	} {
		if detail, err := ctx.RunRecognition(node, f.img); err == nil && detail != nil && detail.Hit {
			return true
		}
	}
	if a.findTextBox(ctx, f, maa.Rect{0, 564, 1280, 156}, "^(图鉴|珍旅居|纳物库|闲庭)") != nil {
		return true
	}
	if len(f.allOCR(ctx, a)) == 0 {
		return false
	}
	if detail, err := ctx.RunRecognition("导航-庭院-识别是否位于庭院-模型识别", f.img); err == nil && detail != nil && detail.Hit {
		return true
	}
	return false
}

func (a *SwitchAccount) isCourtyardOrProfile(ctx *maa.Context, f *Frame) bool {
	if a.isCourtyard(ctx, f) {
		return true
	}
	return a.matchTemplate(ctx, f.img, tplUserCenterProfile, maa.Rect{590, 60, 90, 55}, 0.8) != nil ||
		a.matchTemplate(ctx, f.img, tplUserCenterBtn, maa.Rect{190, 390, 330, 200}, 0.8) != nil
}

func (a *SwitchAccount) isPasswordPrompt(ctx *maa.Context, f *Frame) bool {
	if f == nil || a.hasTpl(ctx, f.img, tplDropdownClosed, maa.Rect{830, 305, 65, 55}, 0.78) {
		return false
	}
	return a.findTextBox(ctx, f, maa.Rect{375, 210, 530, 335}, "请输入.*密码|输入.*密码|登录密码|密码登录|请输入.*邮箱|邮箱账号|获取验证码|立即注册|忘记密码") != nil
}

func (a *SwitchAccount) dismissStartupPopups(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	// 1. Engine update modal ("发现全新的引擎版本！"): strictly click left button "暂不更新" (never "后台下载").
	if a.findTextBox(ctx, f, maa.Rect{340, 200, 600, 220}, "发现全新的引擎版本|全新的引擎|引擎版本") != nil {
		if !a.clickOCRExact(ctx, ctrl, f, maa.Rect{360, 340, 560, 170}, "暂不更新|以后再说|暂不升级", 0) {
			clickRect(ctrl, maa.Rect{445, 412, 173, 47})
		}
		return true
	}
	if box := a.findTextBox(ctx, f, maa.Rect{360, 340, 560, 170}, "^(暂不更新|以后再说|暂不升级)$"); box != nil {
		clickRect(ctrl, *box)
		return true
	}

	// 2. Session expired modal ("用户登录失效。"): click centered red button "^重新登录$" to open NetEase login dialog.
	if a.findTextBox(ctx, f, maa.Rect{380, 240, 520, 160}, "用户登录失效|登录失效|登录已失效") != nil {
		if !a.clickOCRExact(ctx, ctrl, f, maa.Rect{450, 360, 380, 130}, "^重新登录$", 0) {
			clickRect(ctrl, maa.Rect{536, 403, 208, 62})
		}
		return true
	}
	if !a.hasNeteaseAccountDialog(ctx, f) {
		if box := a.findTextBox(ctx, f, maa.Rect{450, 360, 380, 130}, "^重新登录$"); box != nil {
			clickRect(ctrl, *box)
			return true
		}
	}

	// 3. Client update notice (handled via cached OCR + direct target click to avoid pipeline delay).
	if a.findTextBox(ctx, f, maa.Rect{493, 100, 314, 98}, "客.*端更新") != nil {
		clickRect(ctrl, maa.Rect{896, 127, 43, 41})
		return true
	}
	// 4. Cancel illustration / resource download or CG video.
	if a.findTextBox(ctx, f, maa.Rect{320, 170, 640, 270}, "下载插画|资源下载|观看视频|是否打开|加成") != nil {
		if cancel := a.findTextBox(ctx, f, maa.Rect{280, 330, 420, 220}, "取消|拒绝|不观看|暂不"); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
		if a.clickOCRExact(ctx, ctrl, f, maa.Rect{280, 330, 420, 220}, "取消|拒绝|不观看|暂不", 0) {
			return true
		}
	}
	if a.clickRecognizedNode(ctx, ctrl, f.img, "通用-拒绝下载-识别拒绝后点击") {
		return true
	}
	// 5. Announcements and ads (click recognized close box directly without 2000ms pipeline post_delay).
	if a.clickRecognizedNode(ctx, ctrl, f.img, "关闭公告") || a.clickRecognizedNode(ctx, ctrl, f.img, "关闭广告-模板匹配") {
		return true
	}
	return a.dismissPhoneBind(ctx, ctrl, f)
}

func (a *SwitchAccount) dismissPhoneBind(ctx *maa.Context, ctrl *maa.Controller, f *Frame) bool {
	if box := a.matchTemplate(ctx, f.img, tplCancelBindPhone, maa.Rect{420, 430, 220, 140}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if box := a.matchTemplate(ctx, f.img, tplGotoBindPhone, maa.Rect{900, 410, 230, 180}, 0.8); box != nil {
		clickRect(ctrl, *box)
		time.Sleep(postClickSettle)
		if after, _, err := a.waitUntil(ctx, ctrl, 900*time.Millisecond, fastPollInterval, func(cf *Frame) bool {
			return a.matchTemplate(ctx, cf.img, tplCancelBindPhone, maa.Rect{420, 430, 220, 140}, 0.8) != nil
		}); err == nil && after != nil {
			if cBox := a.matchTemplate(ctx, after.img, tplCancelBindPhone, maa.Rect{420, 430, 220, 140}, 0.8); cBox != nil {
				clickRect(ctrl, *cBox)
			}
		}
		return true
	}
	if a.findTextBox(ctx, f, maa.Rect{360, 180, 560, 260}, "绑定手机|手机绑定") != nil {
		if cancel := a.findTextBox(ctx, f, maa.Rect{380, 380, 300, 190}, "取消|暂不|关闭"); cancel != nil {
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
	if box := a.matchTemplate(ctx, f.img, tplRejectInvite, maa.Rect{5, 200, 120, 320}, 0.8); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	if a.dismissStartupPopups(ctx, ctrl, f) {
		return true
	}
	// Cancel battle resume prompt.
	if a.findTextBox(ctx, f, maa.Rect{360, 220, 560, 220}, "继续战斗|是否继续") != nil {
		if cancel := a.findTextBox(ctx, f, maa.Rect{400, 360, 260, 130}, "取消"); cancel != nil {
			clickRect(ctrl, *cancel)
			return true
		}
	}
	// Tap "点击屏幕继续" via cached frame OCR without 1000ms pipeline post_delay.
	if box := a.findTextBox(ctx, f, maa.Rect{393, 639, 469, 81}, "点击屏幕继续"); box != nil {
		clickRect(ctrl, *box)
		return true
	}
	// Close courtyard popups directly without 2000ms+2000ms pipeline pre_delay/post_delay.
	for _, node := range []string{
		"导航-庭院-关闭当前弹窗",
		"导航-庭院-关闭意外弹窗",
	} {
		if a.clickRecognizedNode(ctx, ctrl, f.img, node) {
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
	for _, it := range f.itemsInROI(ctx, a, roi) {
		if MatchEmail(it.Text, email) {
			return true
		}
	}
	for _, it := range a.ocrItems(ctx, f.img, roi) {
		if MatchEmail(it.Text, email) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) matchExactEmailInROI(ctx *maa.Context, f *Frame, roi maa.Rect, email string) bool {
	for _, it := range f.itemsInROI(ctx, a, roi) {
		if MatchExactEmail(it.Text, email) {
			return true
		}
	}
	for _, it := range a.ocrItems(ctx, f.img, roi) {
		if MatchExactEmail(it.Text, email) {
			return true
		}
	}
	return false
}

func (a *SwitchAccount) hasNonPlaceholderEmailText(ctx *maa.Context, f *Frame, roi maa.Rect) bool {
	items := f.itemsInROI(ctx, a, roi)
	if len(items) == 0 {
		items = a.ocrItems(ctx, f.img, roi)
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
		for _, it := range f.itemsInROI(ctx, a, roi) {
			if MatchServer(it.Text, server) {
				return true
			}
		}
	}
	for _, roi := range []maa.Rect{{460, 495, 255, 52}, {450, 495, 340, 55}} {
		for _, it := range a.ocrItems(ctx, f.img, roi) {
			if MatchServer(it.Text, server) {
				return true
			}
		}
	}
	return false
}

func (f *Frame) allOCR(ctx *maa.Context, a *SwitchAccount) []maa.OCRResult {
	if !f.ocrDone {
		f.items = a.ocrItems(ctx, f.img, maa.Rect{0, 0, 1280, 720})
		f.ocrDone = true
	}
	return f.items
}

func (f *Frame) itemsInROI(ctx *maa.Context, a *SwitchAccount, roi maa.Rect) []maa.OCRResult {
	all := f.allOCR(ctx, a)
	if len(all) == 0 {
		return nil
	}
	var out []maa.OCRResult
	for _, it := range all {
		if boxInROI(it.Box, roi) {
			out = append(out, it)
		}
	}
	return out
}

func boxInROI(box, roi maa.Rect) bool {
	cx := box.X() + max(1, box.Width())/2
	cy := box.Y() + max(1, box.Height())/2
	if cx >= roi.X() && cx <= roi.X()+roi.Width() && cy >= roi.Y() && cy <= roi.Y()+roi.Height() {
		return true
	}
	ix0, iy0 := max(box.X(), roi.X()), max(box.Y(), roi.Y())
	ix1, iy1 := min(box.X()+box.Width(), roi.X()+roi.Width()), min(box.Y()+box.Height(), roi.Y()+roi.Height())
	if ix1 <= ix0 || iy1 <= iy0 {
		return false
	}
	return (ix1-ix0)*(iy1-iy0)*2 >= max(1, box.Width()*box.Height())
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

// FindFrameTextBox matches expr against cached full-frame OCR items inside roi.
func (a *SwitchAccount) FindFrameTextBox(ctx *maa.Context, f *Frame, roi maa.Rect, expr string) *maa.Rect {
	return a.findTextBox(ctx, f, roi, expr)
}

func (a *SwitchAccount) findTextBox(ctx *maa.Context, f *Frame, roi maa.Rect, expr string) *maa.Rect {
	if f == nil {
		return nil
	}
	re := cachedRegexp(expr)
	if re == nil {
		return nil
	}
	for _, it := range f.itemsInROI(ctx, a, roi) {
		if re.MatchString(strings.TrimSpace(it.Text)) {
			b := it.Box
			return &b
		}
	}
	return nil
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

func (a *SwitchAccount) findCroppedOCRBox(ctx *maa.Context, img image.Image, roi maa.Rect, expr string) *maa.Rect {
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
