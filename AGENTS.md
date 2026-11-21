# AGENTS.md

## Overview
`MaaYYs` is the Go + MaaFramework v4/v5 automation executor for Onmyoji (`阴阳师`). It supports both MXU ProjectInterface v2 GUI mode (`agent\agent.exe`) and headless control-plane subprocess execution (`bin\maayys-cli.exe`).

## Git Remotes & Branch Workflow (`main` + `custom`)
- **Dual-Remote Convention**:
  - `origin` -> `https://github.com/TanyaShue/MaaYYs.git` (upstream mirror)
  - `fork` -> `https://github.com/Litersam/MaaYYs.git` (personal fork; `remote.pushDefault=fork`)
- **Branch Roles**:
  - `main`: Pure mirror tracking `origin/main`. Never commit custom changes directly to `main`.
  - `custom`: Primary integration branch (`fork/custom`), rebased cleanly onto upstream releases (currently `v4.1.3`). Develop changes on topic branches based on `custom`.

## Repository & Task Architecture
- `agent/`: Go 1.24 module (`maa-yys-agent`) built on `github.com/MaaXYZ/maa-framework-go/v4`.
  - `main.go`, `register.go`: MXU AgentServer socket entrypoint (`agent\agent.exe`).
  - `registry/`: Shared registration list of `CustomActions` and `CustomRecognitions` for both `agent.exe` and `maayys-cli.exe`.
  - `internal/`: Shared frame/ROI/color utilities and task-scoped runtime outcome reporting for the CLI.
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

For new daily plans, use `好友寄养`, `结界挂卡`, and `结界收菜` separately. `结界奖励领取` and its original option keys remain accepted for existing plans; it does not return the split tasks' timer metrics. The three split tasks must report a verified business outcome. A valid timer emits `foster_remaining_sec` with `foster_due_at`, or `realm_card_remaining_sec` with `realm_card_due_at`, including when the slot was already active (`skipped`). The `*_due_at` values are UTC Unix seconds based on when the timer was read, not when the task or plan finished.

`结界收菜` proceeds to experience only after the selected stamina tab confirms `0/N`. An unchanged positive count after two withdrawal clicks is a failure, even if a reward popup was dismissed.

`狭间暗域`, `自动道馆突破`, `寮三十捐材料`, `寮宴会`, and `自动麒麟` also require explicit task outcomes. Use `TaskOutcome` only after verifying the corresponding business terminal; a successful native `StopTask` from navigation or recovery cannot complete these tasks.

`组队刷本`, `觉醒`, `同心之兰`, and `契灵结契` require explicit outcomes as well. For automatic parties, pass `party_role: "captain"` with `teammate_names`, or `"member"` with `captain_names`. Both name fields are comma-separated strings of exact in-game nicknames, not JSON arrays. The captain provides one or two nicknames (one for 永生之海 and 同心之兰). `组队刷本` defaults to `manual` and keeps its legacy 房主/房客 options; `觉醒` defaults to `solo`. `limit_count` sets the managed battle budget. `契灵结契` defaults to `max_summon_stones: 0` and reports `bondling_stones_spent` and `bondling_verified_bonds`. Native offline replays do not establish end-to-end compatibility with a current game client's visual layout.

The legacy `组队刷本` option `挑战次数` counts confirmed wins, not attempts to click 挑战. For `契灵结契`, the CLI accepts both `limit_count` and the legacy `挑战次数`; when both are valid, the lower explicit cap wins. A stone purchase requires a positive disc balance for the actual active tier, not a sum across tiers.

## `maayys-cli.exe` Headless Subprocess Protocol
Build and verify offline from `agent/`:
```powershell
go test ./...
go build -o agent.exe .
go build -o ..\bin\maayys-cli.exe .\cmd\maayys-cli
```

### Subcommands
Both bare subcommands and `--` flag forms are accepted:
1. `bin\maayys-cli.exe catalog` (`--catalog`): Recursively scans `tasks/{daily,standalone,promotion}`, resolves ProjectInterface v2 options (including checkbox `default_case` arrays, nested options and typed `pipeline_type` placeholders), and writes `{"ok": true, "tasks": [{"name": "...", "category": "daily"|"standalone"|"promotion", ...}]}` to `stdout`. Checkbox selections apply in `cases` declaration order; an explicit empty array selects none.
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
     - `{"type":"step","id":"step-login","status":"running"|"complete"|"skipped"|"deferred"|"error","detail":"..."}`; terminal events also carry that step's `metrics` when non-empty.
     - `{"type":"result","ok":true,"status":"completed","metrics":{},"displaced":false,"error":null,"screenshot_saved":false}`
   - Overall status is `failed` on an execution error, otherwise `deferred` if any step asks to retry, `completed` if any work step completed, or `skipped` if all work was skipped. Login/open/close alone do not mask a skipped plan. Later observations replace earlier metrics with the same key.
   - `completed`, `skipped`, and `deferred` exit with code `0`. On failure (exit code `1`), attempts to capture the last frame to `failure_screenshot_path` only when a controller exists and no displacement/risk prompt was detected.
   - Automatic bonus cleanup never disables a bonus already on before the task. On failure or a catchable interrupt, the CLI tries to close only its own bonuses on the current recognized screen; it never restarts the client to reach a switch. Failed verification is reported as an error. A forced process kill cannot execute cleanup.
