# AGENTS.md

## Overview
`MaaYYs` is the Go + MaaFramework v4/v5 automation executor for Onmyoji (`阴阳师`). It supports both MXU ProjectInterface v2 GUI mode (`agent\agent.exe`) and headless control-plane subprocess execution (`bin\maayys-cli.exe`).

## Repository Structure
- `agent/`: Go 1.24 module (`maa-yys-agent`) built on `github.com/MaaXYZ/maa-framework-go/v4`.
  - `main.go`, `register.go`: MXU AgentServer socket entrypoint (`agent\agent.exe`).
  - `registry/`: Shared registration list of all `CustomActions` and `CustomRecognitions` for both `agent.exe` and `maayys-cli.exe`.
  - `actions/`: Custom action runners (including `actions/switch_account` for exact multi-account switching and login).
  - `recognition/`: Custom recognition runners.
  - `cmd/maayys-cli/`: Single-binary in-process headless CLI (`bin\maayys-cli.exe`).
- `resource_pack/`: Pipeline JSONs (`base/pipeline/**`), channel overrides (`official/`, `bilibili/`, `taptap/`, etc.), templates (`base/image/**`), and OCR/ONNX models (`base/model/**`).
- `tasks/`: ProjectInterface v2 task definitions (`tasks/*.json`).
- `deps/tools/`: JSON schemas validated by `go test ./...` (`custom.action.schema.json`, `custom.recognition.schema.json`).
- `maafw/` & `bin/`: Ignored runtime library (`MaaFramework.dll`, `MaaAgentBinary/`, etc.) and compiled CLI output directories.

## Extending Custom Actions & Pipelines
1. Implement `maa.CustomActionRunner` under `agent/actions/<snake_name>/` or `maa.CustomRecognitionRunner` under `agent/recognition/<snake_name>/`.
2. Register the runner in `agent/registry/registry.go` (`CustomActions` or `CustomRecognitions`).
3. Update `deps/tools/custom.action.schema.json` (or `custom.recognition.schema.json`) so `TestCustomSchemasMatchRegistrations` passes.
4. Add pipeline nodes under `resource_pack/base/pipeline/` (1280x720 coordinate space) and expose user/CLI tasks in `tasks/*.json` (and `interface.json` `import` when applicable).
5. **Security & Isolation Rules**:
   - Never log, echo, or pass account passwords through `MaaControllerPostInputText`, `pipeline_override`, clipboard, or subprocess `argv`.
   - Use OS build tags (`//go:build windows` and `//go:build !windows`) for any Win32-specific syscalls so cross-platform builds remain green.
   - Never probe or touch unrelated emulator instances; bind strictly to the requested `adb_address` / `mumu_index`.

## `SwitchAccount` (`账号登录`) Specification
- Registered as `SwitchAccount`, exposed via `resource_pack/base/pipeline/通用/账号登录.json` and `tasks/账号登录.json`.
- Parameters: `{"email": "...", "password": "...", "platform": "ios"|"android", "server": "...", "character": "..."}`.
- Flow:
  1. Checks foreground focus and runs `StartApp` using the merged `启动游戏` package (`com.netease.onmyoji.wyzymnqsd_cps` on `official`) while dismissing startup popups without prematurely entering the previous account.
  2. Exits courtyard (`[35, 35, 55, 55]` -> `用户中心` -> `切换账号`) when already logged in.
  3. Opens the NetEase stored-account selector, strictly matches `email` via OCR, and falls back to Win32 `WM_CHAR` UTF-16 messages sent directly to the target MuMu render window (`mumu_index`) when password login is required.
  4. Explicitly selects `"ios"` or `"android"` platform icon, verifies/switches bottom `server`, clicks `进入游戏`, and requires 2 consecutive courtyard detections >= 1s apart.
  5. **Displaced-login guard**: Immediately aborts with `displaced: true` without reconnecting if `账号在其他设备登录` appears at any stage.

## `maayys-cli.exe` Subprocess Protocol
Build from `agent/`:
```powershell
go test ./...
go build -o agent.exe .
go build -o ..\bin\maayys-cli.exe .\cmd\maayys-cli
```

### Subcommands
1. `bin\maayys-cli.exe catalog`: Reads `tasks/*.json` and writes `{"ok": true, "tasks": [...]}` to `stdout`.
2. `bin\maayys-cli.exe check-resources [paths...]`: Offline-loads `resource_pack/base` + `resource_pack/official` via `MaaFramework` and verifies core pipeline nodes.
3. `bin\maayys-cli.exe run`: Reads a single UTF-8 JSON request from `stdin`:
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
   - Streams line-delimited JSONL events to `stdout` (immediately flushed; framework/internal logs go to `stderr`):
     - `{"type":"log","time":"05:30:01","level":"info","message":"..."}`
     - `{"type":"step","id":"step-login","status":"running"|"complete"|"error","detail":"..."}`
     - `{"type":"result","ok":true,"displaced":false,"error":null,"screenshot_saved":false}`
   - On any failure (exit code `1`), captures the last frame to `failure_screenshot_path` via controller cache or `adb -s <adb_address> exec-out screencap -p` and sets `"screenshot_saved": true`. Exit code `0` indicates all steps succeeded.
