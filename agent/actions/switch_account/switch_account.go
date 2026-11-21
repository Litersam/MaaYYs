package switch_account

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"maa-yys-agent/internal/redact"
	"maa-yys-agent/internal/vision"

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
	errDisplaced      = errors.New("账号在其他设备登录，已中止换号以保护在线状态")
	errSecurityVerify = errors.New("触发网易登录安全验证（需短信验证或固定设备登录）")
	displacedExpr     = regexp.MustCompile(`其他设备登录|在其他设备|被迫下线|已在其他设备`)
	regexCache        sync.Map
)

const (
	fastPollInterval = 260 * time.Millisecond
	postClickSettle  = 240 * time.Millisecond
)

type Frame = vision.Frame

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
	if params.Password != "" {
		a.fail("密码只允许通过 CLI 的内存运行上下文提供", false, "")
		return false
	}
	if hook != nil {
		params.Password = hook.SecretPassword
		params.MumuIndex = hook.MumuIndex
		params.AdbPath = hook.AdbPath
		params.AdbAddr = hook.AdbAddress
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
		displaced := errors.Is(err, errDisplaced) || errors.Is(err, errSecurityVerify)
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

func (a *SwitchAccount) fail(msg string, displaced bool, secret string) {
	safe := redact.Secrets(msg, secret)
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
	msg := redact.Secrets(fmt.Sprintf(format, args...), secret)
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
