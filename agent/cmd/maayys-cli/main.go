package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"maa-yys-agent/actions/switch_account"
	"maa-yys-agent/registry"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/adb"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type RunRequest struct {
	AdbPath               string     `json:"adb_path"`
	AdbAddress            string     `json:"adb_address"`
	MumuIndex             *int       `json:"mumu_index,omitempty"`
	ResourcePaths         []string   `json:"resource_paths"`
	Steps                 []StepSpec `json:"steps"`
	FailureScreenshotPath string     `json:"failure_screenshot_path,omitempty"`
}

type StepSpec struct {
	ID               string         `json:"id"`
	Task             string         `json:"task"`
	Params           map[string]any `json:"params,omitempty"`
	PipelineOverride map[string]any `json:"pipeline_override,omitempty"`
}

type jsonlWriter struct {
	mu      sync.Mutex
	out     io.Writer
	secrets []string
}

func (w *jsonlWriter) addSecret(s string) {
	if s != "" {
		w.mu.Lock()
		w.secrets = append(w.secrets, s)
		w.mu.Unlock()
	}
}

func (w *jsonlWriter) redact(s string) string {
	w.mu.Lock()
	secrets := append([]string(nil), w.secrets...)
	w.mu.Unlock()
	return switch_account.RedactSecrets(s, secrets...)
}

func (w *jsonlWriter) emit(v any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if data, err := json.Marshal(v); err == nil {
		_, _ = w.out.Write(append(data, '\n'))
		if f, ok := w.out.(*os.File); ok {
			_ = f.Sync()
		}
	}
}

func (w *jsonlWriter) Log(level, msg string) {
	w.emit(struct {
		Type    string `json:"type"`
		Time    string `json:"time"`
		Level   string `json:"level"`
		Message string `json:"message"`
	}{"log", time.Now().Format("15:04:05"), level, w.redact(msg)})
}

func (w *jsonlWriter) Step(id, status, detail string) {
	w.emit(struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}{"step", id, status, w.redact(detail)})
}

func (w *jsonlWriter) Result(ok, displaced bool, errMsg string, screenshotSaved bool) {
	var errPtr *string
	if !ok && errMsg != "" {
		clean := w.redact(errMsg)
		errPtr = &clean
	}
	w.emit(struct {
		Type            string  `json:"type"`
		OK              bool    `json:"ok"`
		Displaced       bool    `json:"displaced"`
		Error           *string `json:"error"`
		ScreenshotSaved bool    `json:"screenshot_saved"`
	}{"result", ok, displaced, errPtr, screenshotSaved})
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: maayys-cli <catalog|run|check-resources>")
		os.Exit(2)
	}
	repoRoot := resolveRepoRoot()
	switch strings.ToLower(strings.TrimSpace(os.Args[1])) {
	case "catalog":
		if err := runCatalog(repoRoot, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "catalog error: %v\n", err)
			os.Exit(1)
		}
	case "check-resources":
		if err := runCheckResources(repoRoot, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "check-resources error: %v\n", err)
			os.Exit(1)
		}
	case "run":
		jsonlOut := os.Stdout
		os.Stdout = os.Stderr
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		log.Logger = log.Output(os.Stderr)
		os.Exit(runStepsFromStdin(repoRoot, os.Stdin, jsonlOut))
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runCatalog(repoRoot string, out io.Writer) error {
	list, _, err := loadTaskDefinitions(repoRoot)
	if err != nil {
		return err
	}
	items := make([]CatalogTask, len(list))
	for i, t := range list {
		items[i] = t.CatalogTask
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(CatalogOutput{OK: true, Tasks: items})
}

func runCheckResources(repoRoot string, paths []string) error {
	if len(paths) == 0 {
		paths = defaultResourcePaths(repoRoot)
	}
	if err := initMaaFramework(repoRoot); err != nil {
		return err
	}
	defer func() { _ = maa.Release() }()
	res, err := loadAndRegisterResource(paths)
	if err != nil {
		return err
	}
	defer res.Destroy()
	for _, n := range []string{"账号登录", "启动游戏", "关闭阴阳师"} {
		if _, err := res.GetNodeJSON(n); err != nil {
			return fmt.Errorf("missing node %q: %w", n, err)
		}
	}
	data, _ := json.Marshal(map[string]any{"ok": true, "loaded": res.Loaded(), "resource_paths": paths})
	fmt.Println(string(data))
	return nil
}

func runStepsFromStdin(repoRoot string, in io.Reader, jsonlOut io.Writer) int {
	writer := &jsonlWriter{out: jsonlOut}
	rawInput, err := io.ReadAll(in)
	if err != nil {
		writer.Result(false, false, fmt.Sprintf("读取 stdin 请求失败: %v", err), false)
		return 1
	}
	var req RunRequest
	if err := json.Unmarshal(rawInput, &req); err != nil {
		writer.Result(false, false, "解析 stdin JSON 请求失败", false)
		return 1
	}
	for _, s := range req.Steps {
		if pw, ok := s.Params["password"].(string); ok {
			writer.addSecret(pw)
		}
	}
	if strings.TrimSpace(req.AdbPath) == "" || strings.TrimSpace(req.AdbAddress) == "" || len(req.Steps) == 0 {
		writer.Result(false, false, "adb_path、adb_address 与 steps 不能为空", false)
		return 1
	}
	if len(req.ResourcePaths) == 0 {
		req.ResourcePaths = defaultResourcePaths(repoRoot)
	}

	failRun := func(ctrl *maa.Controller, displaced bool, msg string) int {
		saved := captureFailureScreenshot(ctrl, req.AdbPath, req.AdbAddress, req.FailureScreenshotPath)
		writer.Result(false, displaced, msg, saved)
		return 1
	}

	_, tasksByName, err := loadTaskDefinitions(repoRoot)
	if err != nil {
		return failRun(nil, false, fmt.Sprintf("加载任务目录失败: %v", err))
	}
	if err := initMaaFramework(repoRoot); err != nil {
		return failRun(nil, false, fmt.Sprintf("初始化 MaaFramework 失败: %v", err))
	}
	defer func() { _ = maa.Release() }()

	writer.Log("info", "正在加载资源包...")
	res, err := loadAndRegisterResource(req.ResourcePaths)
	if err != nil {
		return failRun(nil, false, err.Error())
	}
	defer res.Destroy()

	agentBinDir := filepath.Join(resolveMaaLibDir(repoRoot), "MaaAgentBinary")
	writer.Log("info", fmt.Sprintf("正在连接设备 %s...", req.AdbAddress))
	ctrl, err := maa.NewAdbController(req.AdbPath, req.AdbAddress, adb.ScreencapDefault, adb.InputDefault, "{}", agentBinDir)
	if err != nil {
		return failRun(nil, false, fmt.Sprintf("创建 ADB 控制器失败: %v", err))
	}
	defer ctrl.Destroy()

	_ = ctrl.SetScreenshot(maa.WithScreenshotTargetShortSide(720))
	if !ctrl.PostConnect().Wait().Success() {
		return failRun(ctrl, false, fmt.Sprintf("ADB 连接设备失败: %s", req.AdbAddress))
	}

	tasker, err := maa.NewTasker()
	if err != nil {
		return failRun(ctrl, false, fmt.Sprintf("创建 Tasker 失败: %v", err))
	}
	defer tasker.Destroy()

	if err := tasker.BindResource(res); err != nil {
		return failRun(ctrl, false, fmt.Sprintf("绑定 Resource 失败: %v", err))
	}
	if err := tasker.BindController(ctrl); err != nil {
		return failRun(ctrl, false, fmt.Sprintf("绑定 Controller 失败: %v", err))
	}
	tasker.OnNodePipelineNodeInContext(func(_ *maa.Context, status maa.EventStatus, d maa.NodePipelineNodeDetail) {
		if status == maa.EventStatusStarting && d.Name != "" {
			writer.Log("info", fmt.Sprintf("执行节点: %s", d.Name))
		}
	})

	for idx, step := range req.Steps {
		stepID := strings.TrimSpace(step.ID)
		if stepID == "" {
			stepID = fmt.Sprintf("step-%d", idx+1)
		}
		displaced, detail, stepErr := executeStep(tasker, res, tasksByName, req, step, stepID, writer)
		if stepErr != nil {
			writer.Step(stepID, "error", stepErr.Error())
			return failRun(ctrl, displaced, stepErr.Error())
		}
		writer.Step(stepID, "complete", detail)
	}

	writer.Result(true, false, "", false)
	return 0
}

func executeStep(tasker *maa.Tasker, res *maa.Resource, tasks map[string]resolvedTask, req RunRequest, step StepSpec, stepID string, writer *jsonlWriter) (bool, string, error) {
	taskName := strings.TrimSpace(step.Task)
	if taskName == "" {
		return false, "", errors.New("step.task 不能为空")
	}
	entry, override := taskName, make(map[string]any)
	if def, ok := tasks[taskName]; ok {
		entry = def.Entry
		mergeMap(override, def.BaseOverride)
		applyOptionParams(override, def, step.Params)
	} else if _, err := res.GetNodeJSON(taskName); err != nil {
		return false, "", fmt.Errorf("未知任务 %q", taskName)
	}
	mergeMap(override, step.PipelineOverride)

	if taskName == "账号登录" || entry == "账号登录" {
		rawParams, _ := json.Marshal(step.Params)
		var loginParams switch_account.Params
		if len(rawParams) > 0 && string(rawParams) != "null" && json.Unmarshal(rawParams, &loginParams) != nil {
			return false, "", errors.New("解析账号登录参数失败")
		}
		secretPwd := loginParams.Password
		loginParams.Password = ""
		mergeMap(override, map[string]any{"账号登录": map[string]any{"custom_action_param": loginParams}})

		writer.Step(stepID, "running", "正在准备切换账号")
		registry.SwitchAccountRunner.SetRuntimeHook(&switch_account.RuntimeHook{
			SecretPassword: secretPwd, MumuIndex: req.MumuIndex, AdbPath: req.AdbPath, AdbAddress: req.AdbAddress,
			OnLog:    writer.Log,
			OnDetail: func(d string) { writer.Step(stepID, "running", d) },
		})
		defer registry.SwitchAccountRunner.SetRuntimeHook(nil)

		job := tasker.PostTask(entry, override).Wait()
		outcome := registry.SwitchAccountRunner.LastOutcome()
		if !job.Success() || outcome.Displaced || outcome.LastError != "" {
			if outcome.LastError != "" {
				return outcome.Displaced, "", errors.New(outcome.LastError)
			}
			return outcome.Displaced, "", errors.New("账号登录执行失败")
		}
		return false, "已进入庭院", nil
	}

	writer.Step(stepID, "running", fmt.Sprintf("正在执行 %s", taskName))
	if !tasker.PostTask(entry, override).Wait().Success() {
		return false, "", fmt.Errorf("任务 %s 执行失败", taskName)
	}
	return false, fmt.Sprintf("%s 已完成", taskName), nil
}

func defaultResourcePaths(repoRoot string) []string {
	return []string{filepath.Join(repoRoot, "resource_pack", "base"), filepath.Join(repoRoot, "resource_pack", "official")}
}

func initMaaFramework(repoRoot string) error {
	if maa.IsInited() {
		return nil
	}
	logDir := filepath.Join(repoRoot, "debug")
	_ = os.MkdirAll(logDir, 0o755)
	if err := maa.Init(maa.WithLibDir(resolveMaaLibDir(repoRoot)), maa.WithLogDir(logDir), maa.WithStdoutLevel(maa.LoggingLevelOff)); err != nil {
		return err
	}
	_ = maa.ConfigInitOption(repoRoot, "{}")
	return nil
}

func loadAndRegisterResource(paths []string) (*maa.Resource, error) {
	res, err := maa.NewResource()
	if err != nil {
		return nil, err
	}
	if err := registry.RegisterResource(res); err != nil {
		res.Destroy()
		return nil, err
	}
	for _, p := range paths {
		if !res.PostBundle(p).Wait().Success() {
			res.Destroy()
			return nil, fmt.Errorf("加载资源包失败: %s", p)
		}
	}
	return res, nil
}

func captureFailureScreenshot(ctrl *maa.Controller, adbPath, adbAddress, destPath string) bool {
	if destPath = strings.TrimSpace(destPath); destPath == "" || os.MkdirAll(filepath.Dir(destPath), 0o755) != nil {
		return false
	}
	if ctrl != nil {
		_ = ctrl.PostScreencap().Wait()
		if img, err := ctrl.CacheImage(); err == nil && img != nil {
			var buf bytes.Buffer
			if png.Encode(&buf, img) == nil && buf.Len() > 100 && os.WriteFile(destPath, buf.Bytes(), 0o644) == nil {
				return true
			}
		}
	}
	if strings.TrimSpace(adbPath) != "" && strings.TrimSpace(adbAddress) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, adbPath, "-s", adbAddress, "exec-out", "screencap", "-p").Output(); err == nil &&
			bytes.HasPrefix(out, []byte("\x89PNG\r\n\x1a\n")) && os.WriteFile(destPath, out, 0o644) == nil {
			return true
		}
	}
	return false
}

func resolveRepoRoot() string {
	if env := strings.TrimSpace(os.Getenv("MAAYYS_ROOT")); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		for _, cand := range []string{filepath.Dir(filepath.Dir(exe)), filepath.Dir(exe)} {
			if dirExists(filepath.Join(cand, "tasks")) && dirExists(filepath.Join(cand, "resource_pack")) {
				return cand
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			if dirExists(filepath.Join(dir, "tasks")) && dirExists(filepath.Join(dir, "resource_pack")) {
				return dir
			}
			if next := filepath.Dir(dir); next == dir {
				break
			}
		}
	}
	return "."
}

func resolveMaaLibDir(repoRoot string) string {
	if override := strings.TrimSpace(os.Getenv("MAAFW_LIB_DIR")); override != "" {
		return override
	}
	return filepath.Join(repoRoot, "maafw")
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
