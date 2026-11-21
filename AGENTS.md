# AGENTS.md

## Overview
`MaaYYs` is the Go + MaaFramework v4/v5 automation executor for Onmyoji (`阴阳师`). It supports both MXU ProjectInterface v2 GUI mode (`agent\agent.exe`) and headless control-plane subprocess execution (`bin\maayys-cli.exe`).

## Git Remotes & Branch Workflow (`main` + `custom`)
- **Dual-Remote Convention**:
  - `upstream` -> `https://github.com/TanyaShue/MaaYYs.git`
  - `origin` -> `https://github.com/Litersam/MaaYYs.git`
- **Branch Roles**:
  - `main`: Pure mirror tracking `upstream/main` (synced verbatim to `origin/main`). Never commit custom changes directly to `main`.
  - `custom`: Primary integration branch (`origin/custom`), rebased cleanly onto upstream releases (currently `v4.1.3`). All custom actions, CLI enhancements, and custom task definitions live on `custom` (or short-lived topic branches rebased onto `custom`).

## Repository & Task Architecture
- `agent/`: Go 1.24 module (`maa-yys-agent`) built on `github.com/MaaXYZ/maa-framework-go/v4`.
  - `main.go`, `register.go`: MXU AgentServer socket entrypoint (`agent\agent.exe`).
  - `registry/`: Shared registration list of `CustomActions` and `CustomRecognitions` for both `agent.exe` and `maayys-cli.exe`.
  - `actions/` & `recognition/`: Custom action and recognition runners (including `actions/switch_account` and upstream `actions/switch_soul_by_week`).
  - `cmd/maayys-cli/`: Single-binary in-process headless CLI (`bin\maayys-cli.exe`).
- `resource_pack/`: 1280x720 declarative pipelines (`base/pipeline/**`), channel overrides (`official/`, `bilibili/`, `taptap/`, etc.), templates (`base/image/**`), and OCR/ONNX models (`base/model/**`).
- `tasks/`: ProjectInterface v2 task definitions with **strict permanent vs. event isolation** (no root-level `tasks/*.json` orphan files):
  - `tasks/daily/*.json` (`category: "daily"`): Permanent daily routine tasks.
  - `tasks/standalone/*.json` (`category: "standalone"`): Permanent standalone dungeons and utility tasks (including `tasks/standalone/账号登录.json`).
  - `tasks/promotion/*.json` (`category: "promotion"`): Time-limited / seasonal event tasks, strictly isolated from permanent `daily`/`standalone` definitions and pipelines (`base/pipeline/活动/限定/**`).
  - Register GUI-exposed tasks in `interface.json` `import` ordered `daily` -> `promotion` -> `standalone` (pinyin ascending per group).
- `deps/tools/`: JSON schemas (`custom.action.schema.json`, `custom.recognition.schema.json`) validated by `TestCustomSchemasMatchRegistrations`.
- `maafw/` & `bin/`: Git-ignored runtime library (`MaaFramework.dll`, `MaaAgentBinary/`) and compiled CLI output directories.

## Design, Maintainability & Security Guidelines
1. **Declarative Pipeline First**: Express UI navigation, ROI OCR/template matching, state transitions, and retry/timeout rules declaratively in Pipeline JSON (`resource_pack/base/pipeline/`) whenever possible.
2. **Thin, Single-Responsibility Go Runners**: Introduce Go `CustomAction` / `CustomRecognition` runners only for stateful orchestration, dynamic calculations, or OS/Win32 integration that declarative nodes cannot cleanly express. Keep each package focused, small, unit-tested, and registered in both `agent/registry/registry.go` and `deps/tools/custom.*.schema.json`.
3. **Security & Emulator Isolation**:
   - Never log, echo, or pass account passwords through `MaaControllerPostInputText`, `pipeline_override`, clipboard, or subprocess `argv`.
   - `SwitchAccount` (`账号登录`) injects passwords exclusively via in-memory `RuntimeHook` + Win32 `WM_CHAR` UTF-16 messages to the target MuMu render window (`mumu_index`), requires 2 consecutive courtyard detections >= 1s apart, and immediately aborts with `displaced: true` upon `账号在其他设备登录` or risk-control verification prompts.
   - Gate Win32 syscalls behind `//go:build windows` and `//go:build !windows` stubs so cross-platform builds stay green.
   - Bind strictly to the requested `adb_address` / `mumu_index`; never probe or touch unrelated emulator instances.

## `maayys-cli.exe` Headless Subprocess Protocol
Build and verify offline from `agent/`:
```powershell
go test ./...
go build -o agent.exe .
go build -o ..\bin\maayys-cli.exe .\cmd\maayys-cli
```

### Subcommands
Both bare subcommands and `--` flag forms are accepted:
1. `bin\maayys-cli.exe catalog` (`--catalog`): Recursively scans `tasks/{daily,standalone,promotion}`, resolves ProjectInterface v2 options (including `v4.1.3` nested `case.option` trees and typed `pipeline_type` placeholders), and writes `{"ok": true, "tasks": [{"name": "...", "category": "daily"|"standalone"|"promotion", ...}]}` to `stdout`.
2. `bin\maayys-cli.exe check-resources [paths...]` (`--check-resources`): Offline-loads `resource_pack/base` + `resource_pack/official` via `MaaFramework` and verifies core pipeline nodes without connecting to any emulator.
3. `bin\maayys-cli.exe run` (`--run`): Reads a single UTF-8 JSON request from `stdin` (tasks may be referenced by bare `name` such as `"账号登录"` for backward compatibility, or `<category>/<name>`):
   ```json
   {
     "adb_path": "D:\\tools\\MuMuPlayer\\nx_main\\adb.exe",
     "adb_address": "127.0.0.1:16384",
     "mumu_index": 4,
     "resource_paths": [
       "D:\\projs\\MaaYYs\\resource_pack\\base",
       "D:\\projs\\MaaYYs\\resource_pack\\official"
     ],
     "steps": [
       {
         "id": "step-login",
         "task": "账号登录",
         "params": {
           "email": "user@163.com",
           "password": "secret",
           "platform": "android",
           "server": "全平台互通"
         }
       }
     ],
     "failure_screenshot_path": "D:\\projs\\game-desk-next\\.data\\screenshots\\run-123.png"
   }
   ```
   - Streams flushed JSONL events to `stdout` (secrets redacted; framework/internal logs go to `stderr`):
     - `{"type":"log","time":"05:30:01","level":"info","message":"..."}`
     - `{"type":"step","id":"step-login","status":"running"|"complete"|"error","detail":"..."}`
     - `{"type":"result","ok":true,"displaced":false,"error":null,"screenshot_saved":false}`
   - Exit code `0` indicates all steps succeeded. On failure (exit code `1`), captures the last frame to `failure_screenshot_path` via controller cache or `adb -s <adb_address> exec-out screencap -p` and sets `"screenshot_saved": true`.
