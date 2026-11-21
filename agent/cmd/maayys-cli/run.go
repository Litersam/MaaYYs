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
	"slices"
	"strings"
	"time"

	"maa-yys-agent/actions/bonus_toggle"
	"maa-yys-agent/actions/switch_account"
	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/registry"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/adb"
)

func runStepsFromStdin(ctx context.Context, repoRoot string, in io.Reader, jsonlOut io.Writer) int {
	writer := &jsonlWriter{out: jsonlOut}
	failed := runstate.Outcome{Status: runstate.Failed}
	rawInput, err := io.ReadAll(in)
	if err != nil {
		writer.Result(failed, false, fmt.Sprintf("读取 stdin 请求失败: %v", err), false)
		return 1
	}
	var req RunRequest
	if err := json.Unmarshal(rawInput, &req); err != nil {
		writer.Result(failed, false, "解析 stdin JSON 请求失败", false)
		return 1
	}
	for _, s := range req.Steps {
		if pw, ok := s.Params["password"].(string); ok {
			writer.addSecret(pw)
		}
	}
	if strings.TrimSpace(req.AdbPath) == "" || strings.TrimSpace(req.AdbAddress) == "" || len(req.Steps) == 0 {
		writer.Result(failed, false, "adb_path、adb_address 与 steps 不能为空", false)
		return 1
	}
	if len(req.ResourcePaths) == 0 {
		req.ResourcePaths = defaultResourcePaths(repoRoot)
	}

	var outcome runstate.Outcome
	failRun := func(ctrl *maa.Controller, displaced bool, msg string) int {
		saved := ctrl != nil && !displaced && captureFailureScreenshot(ctrl, req.AdbPath, req.AdbAddress, req.FailureScreenshotPath)
		outcome.Status = runstate.Failed
		writer.Result(outcome, displaced, msg, saved)
		return 1
	}

	tInitStart := time.Now()
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
	resLoadDur := time.Since(tInitStart)

	tAdbStart := time.Now()
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
	adbConnDur := time.Since(tAdbStart)
	writer.Log("info", fmt.Sprintf("[CLI耗时] 资源加载=%.2fs, ADB连接=%.2fs", resLoadDur.Seconds(), adbConnDur.Seconds()))

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
		if ctx.Err() != nil {
			return failRun(ctrl, false, "执行已中止")
		}
		stepID := strings.TrimSpace(step.ID)
		if stepID == "" {
			stepID = fmt.Sprintf("step-%d", idx+1)
		}
		collector, release := runstate.Bind(tasker)
		bonuses, untrack := bonus_toggle.Track(tasker)
		displaced, detail, stepErr := executeStep(ctx, tasker, res, tasksByName, req, step, stepID, writer)
		if !displaced {
			cleanupErr := bonuses.Cleanup(tasker, func(tasker *maa.Tasker, entry string, override map[string]any) bool {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return waitTask(cleanupCtx, tasker, entry, override)
			})
			stepErr = errors.Join(stepErr, cleanupErr)
		}
		untrack()
		release()
		stepOutcome := collector.Outcome()
		stepErr = validateStepCompletion(outcomeTaskName(tasksByName, step.Task), stepOutcome, stepErr)
		mergeStepOutcome(&outcome, step.Task, stepOutcome)
		writer.EndStep(stepID, detail, stepOutcome, stepErr)
		if stepErr != nil {
			return failRun(ctrl, displaced, stepErr.Error())
		}
	}

	writer.Result(outcome, false, "", false)
	return 0
}

func bareTaskName(task string) string {
	name := strings.TrimSpace(task)
	if _, suffix, ok := strings.Cut(name, "/"); ok {
		return strings.TrimSpace(suffix)
	}
	return name
}

var guildActivityTasks = []string{"狭间暗域", "自动道馆突破", "寮三十捐材料", "寮宴会", "自动麒麟"}

var partyActivityTasks = []string{"组队刷本", "觉醒", "同心之兰", "契灵结契"}

func requiresStepOutcome(task string) bool {
	name := bareTaskName(task)
	if slices.Contains(guildActivityTasks, name) || slices.Contains(partyActivityTasks, name) {
		return true
	}
	switch name {
	case "好友寄养", "结界挂卡", "结界收菜", "自动悬赏", "自动悬赏封印":
		return true
	default:
		return false
	}
}

func outcomeTaskName(tasks map[string]resolvedTask, task string) string {
	if def, ok := lookupTask(tasks, task); ok {
		return def.Name
	}
	// A caller may use a declared task's raw Pipeline entry instead of its
	// catalog name. It still needs the same verified business outcome.
	entry := strings.TrimSpace(task)
	for _, def := range tasks {
		if def.Entry == entry && requiresStepOutcome(def.Name) {
			return def.Name
		}
	}
	return task
}

func validateStepCompletion(task string, outcome runstate.Outcome, err error) error {
	if err != nil {
		return err
	}
	if outcome.Status == runstate.Failed {
		return fmt.Errorf("任务 %s 报告失败", task)
	}
	if requiresStepOutcome(task) && !outcome.Reported {
		return fmt.Errorf("任务 %s 未确认业务终态", task)
	}
	return nil
}

func mergeStepOutcome(outcome *runstate.Outcome, task string, stepOutcome runstate.Outcome) {
	// Login/open/close shouldn't turn an otherwise skipped daily plan into a completed one.
	if stepOutcome.Status == runstate.Completed {
		switch bareTaskName(task) {
		case "账号登录", "打开游戏", "启动游戏", "关闭游戏", "关闭阴阳师":
			stepOutcome.Status = ""
		}
	}
	outcome.Merge(stepOutcome)
}

func waitTask(ctx context.Context, tasker *maa.Tasker, entry string, override map[string]any) bool {
	if ctx.Err() != nil {
		return false
	}
	job := tasker.PostTask(entry, override)
	done := make(chan bool, 1)
	go func() { done <- job.Wait().Success() }()
	select {
	case success := <-done:
		return success && ctx.Err() == nil
	case <-ctx.Done():
		tasker.PostStop().Wait()
		<-done
		return false
	}
}

func executeStep(ctx context.Context, tasker *maa.Tasker, res *maa.Resource, tasks map[string]resolvedTask, req RunRequest, step StepSpec, stepID string, writer *jsonlWriter) (bool, string, error) {
	taskName := strings.TrimSpace(step.Task)
	if taskName == "" {
		return false, "", errors.New("step.task 不能为空")
	}
	entry, override := taskName, make(map[string]any)
	if def, ok := lookupTask(tasks, taskName); ok {
		taskName = def.Name
		entry = def.Entry
		mergeMap(override, resolveTaskOverride(def, step.Params))
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

		writer.Step(stepID, "running", "正在准备切换账号", nil)
		registry.SwitchAccountRunner.SetRuntimeHook(&switch_account.RuntimeHook{
			SecretPassword: secretPwd, MumuIndex: req.MumuIndex, AdbPath: req.AdbPath, AdbAddress: req.AdbAddress,
			OnLog:    writer.Log,
			OnDetail: func(d string) { writer.Step(stepID, "running", d, nil) },
		})
		defer registry.SwitchAccountRunner.SetRuntimeHook(nil)

		success := waitTask(ctx, tasker, entry, override)
		outcome := registry.SwitchAccountRunner.LastOutcome()
		if !success || outcome.Displaced || outcome.LastError != "" {
			if outcome.LastError != "" {
				return outcome.Displaced, "", errors.New(outcome.LastError)
			}
			return outcome.Displaced, "", errors.New("账号登录执行失败")
		}
		return false, "已进入庭院", nil
	}

	writer.Step(stepID, "running", fmt.Sprintf("正在执行 %s", taskName), nil)
	if !waitTask(ctx, tasker, entry, override) {
		if ctx.Err() != nil {
			return false, "", errors.New("执行已中止")
		}
		return false, "", fmt.Errorf("任务 %s 执行失败", taskName)
	}
	return false, fmt.Sprintf("%s 已完成", taskName), nil
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
