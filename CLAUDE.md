# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目定位

这是 `claude-notifications-go`，一个 Claude Code 通知插件的 Go 实现。插件通过 Claude Code hooks 判断任务状态，并发送桌面通知、声音和 webhook。Go module 为 `github.com/777genius/claude-notifications`，`go.mod` 声明 Go `1.21.5`；CI 在 Go `1.21` 和 `1.22` 上运行。

## 常用命令

### 构建

```powershell
# 开发构建：生成 bin/claude-notifications、bin/sound-preview、bin/list-sounds（Windows 下为 .exe）
make build

# 不依赖 make 的主二进制构建（Windows PowerShell）
go build -o bin/claude-notifications.exe ./cmd/claude-notifications

# 辅助工具构建（Windows PowerShell）
go build -o bin/sound-preview.exe ./cmd/sound-preview
go build -o bin/list-sounds.exe ./cmd/list-sounds

# 跨平台 release 构建到 dist/
make build-all
```

### 测试与 lint

```powershell
# 全量测试，带 coverage
make test
# 等价核心命令
go test -v -cover ./...

# race 测试（CI 使用类似命令）
make test-race
go test -v -race -cover ./...

# 生成 coverage.html
make test-coverage

# go vet + go fmt（注意：该目标会直接执行 go fmt ./...）
make lint
```

运行单个 package 或单个测试：

```powershell
go test ./internal/analyzer -v
go test ./internal/hooks -v
go test ./internal/config -v
go test ./internal/dedup -v -race

go test -run TestStateMachine ./internal/analyzer -v
```

带 build tag 的 hook 集成测试位于 `internal/hooks/integration_test.go`：

```powershell
go test -tags=integration -v ./internal/hooks/
go test -tags=integration -v -run TestE2E_WebhookRetry ./internal/hooks/
go test -tags=integration -v ./...
```

### 本地运行与手动验证

```powershell
# 查看 CLI 帮助和版本
.\bin\claude-notifications.exe help
.\bin\claude-notifications.exe version

# 直接模拟 PreToolUse hook，验证通知链路
'{"session_id":"win-debug","tool_name":"ExitPlanMode"}' | .\bin\claude-notifications.exe handle-hook PreToolUse

# 声音相关工具
.\bin\list-sounds.exe
.\bin\list-sounds.exe --json
.\bin\list-sounds.exe --play task-complete --volume 0.3
.\bin\sound-preview.exe --volume 0.3 .\sounds\task-complete.mp3
.\bin\list-devices.exe
```

项目文档中的插件/E2E 工作流主要是 shell scripts：

```text
scripts/dev-local-plugin.sh install|bootstrap|status|update|reset
scripts/e2e-real-claude.sh status|smoke-plugin-dir|smoke-installed|manual-click-plugin-dir|manual-click-installed
scripts/dev-real-plugin.sh local|remote|toggle|status
```

这些脚本用于隔离 Claude 配置、本机真实 Claude hook 冒烟测试、以及把真实 Claude marketplace 在本地/远程源之间切换。`docs/LOCAL_DEVELOPMENT.md` 明确说明 real-Claude harness 主要面向本地 macOS/Linux；Windows 不是该 harness 的支持目标。当前用户全局规则禁止通过 Bash 工具执行命令，因此在本 Claude Code 会话内优先使用 PowerShell 和直接 `go` 命令；需要运行上述 shell scripts 时，让用户确认并自行在合适环境执行。

## 高层架构

### 插件入口与 hook 注册

- `.claude-plugin/plugin.json` 定义插件名、版本和可用 slash commands：`commands/init.md`、`commands/settings.md`、`commands/notifications-init.md`、`commands/notifications-settings.md`。
- `hooks/hooks.json` 注册 Claude Code hook：
  - `PreToolUse` 匹配 `ExitPlanMode|AskUserQuestion`
  - `Notification` 匹配 `permission_prompt`
  - `Stop`
  - `SubagentStop`
  - `TeammateIdle`
- 非 Windows hook 通过 `${CLAUDE_PLUGIN_ROOT}/bin/hook-wrapper.sh handle-hook <Event>` 调用主二进制；Windows hook JSON 可由 `claude-notifications windows-hooks` 生成，直接调用 `.exe handle-hook <Event>`。

### CLI 二进制

主入口在 `cmd/claude-notifications/main.go`，支持这些子命令：

- `handle-hook <Event>`：核心 hook 处理入口。
- `focus-window <bundleID> <cwd>`：点击通知后聚焦窗口相关入口。
- `play-sound`：播放声音。
- `daemon` / `--daemon`：Linux click-to-focus daemon。
- `windows-hooks`：生成 Windows 原生 hook 配置。
- `version` / `help`。

辅助 CLI：

- `cmd/sound-preview`：按路径预览 MP3/WAV/FLAC/OGG/AIFF，可选音量和音频设备。
- `cmd/list-sounds`：发现内置和系统声音，支持 `--json`、`--play`。
- `cmd/list-devices`：列出音频输出设备名，供 `audioDevice` 配置使用。

### hook 处理主链路

`cmd/claude-notifications/main.go` 的 `handleHook` 负责确定 plugin root、初始化 logging、执行 Windows lazy update 检查，并创建 `internal/hooks.Handler`。

`internal/hooks` 是编排层，`Handler.HandleHook` 的关键顺序是：

1. 读取配置并初始化服务：`config.LoadFromPluginRoot`、`dedup.Manager`、`state.Manager`、`teamstate.Manager`、`notifier.New`、`webhook.New`。
2. 如果 `CLAUDE_HOOK_JUDGE_MODE=true` 且配置允许，则静默跳过，用于兼容会启动后台 Claude 的插件。
3. 解析 hook stdin JSON 为 `HookData`（`session_id`、`transcript_path`、`cwd`、`tool_name`、team 字段等）。
4. 对 `PreToolUse` / `Notification` 捕获 Ghostty terminal ID，用于后续精确聚焦。
5. 执行第一阶段去重检查。
6. 按 hook 类型确定状态：
   - `PreToolUse`：`ExitPlanMode` → `plan_ready`，`AskUserQuestion` → `question`，并提前写 session state。
   - `Notification`：始终按 `question` 处理。
   - `Stop`：可按 subagent/team mode 抑制；否则读取 transcript 并由 analyzer 分类。
   - `SubagentStop`：先看 `suppressForSubagents` 和 `notifyOnSubagentStop`，启用后复用 Stop 分析。
   - `TeammateIdle`：仅在 `teamMode == "wait-all"` 时记录队友空闲并在全员完成后通知。
7. 状态未知则跳过；否则应用 `suppressFilters`（按 status、git branch、folder）。
8. 第二阶段获取 per-session/per-hook lock，再进行 question cooldown、task_complete state 更新。
9. 用 `internal/summary` 生成正文和动作摘要，使用 content lock 做 3 分钟内容级去重。
10. 调用桌面通知和 webhook，并在退出前关闭 notifier、等待 webhook 最多 5 秒完成。

### 状态分析与 transcript 解析

`internal/analyzer` 只负责从 Claude Code transcript 推断通知状态。它先检查 session limit 和 API error，再只分析最后一个 user message 之后的 assistant 消息，并限制在最近 15 条消息内，避免旧 `ExitPlanMode` 造成误判。主要状态机规则：

- 最后工具是 `ExitPlanMode` → `plan_ready`
- 最后工具是 `AskUserQuestion` → `question`
- `ExitPlanMode` 后又使用过工具 → `task_complete`
- 只有 `Read`/`Grep`/`Glob` 等读类工具、没有 active tool、且最近文本超过 200 字 → `review_complete`
- 最后工具属于 active tools（`Write`、`Edit`、`Bash`、`NotebookEdit`、`SlashCommand`、`KillShell`）→ `task_complete`
- 没有工具但 `notifyOnTextResponse` 开启时 → `task_complete`

`pkg/jsonl` 是 transcript JSONL parser。它使用 `bufio.Reader` 支持超长行，跳过坏 JSON 行，并兼容 Claude Code message content 既可能是字符串也可能是数组的格式；API error 通过 `isApiErrorMessage` 和 `error` 字段辅助识别。

### 配置、状态和去重

`internal/config` 的稳定配置目录是用户目录下的 `~/.claude/claude-notifications-go/config.json`（Windows PowerShell 对应 `$env:USERPROFILE\.claude\claude-notifications-go\config.json`）。`LoadFromPluginRoot` 优先读稳定路径，旧的 `pluginRoot/config/config.json` 作为 fallback，并会迁移；配置损坏时按代码注释设计为非致命，回退到后续来源或默认配置。

默认状态包括：`task_complete`、`review_complete`、`question`、`plan_ready`、`session_limit_reached`、`api_error`、`api_error_overloaded`。配置还控制 desktop、webhook、question cooldown、subagent/team mode、text-only response、suppress filters 等。

`internal/state` 在系统临时目录写 per-session JSON state，记录最近 interactive tool、task complete 时间、最近通知时间/内容、Ghostty terminal ID 和 cwd。`internal/dedup` 使用临时目录 lock 文件做两阶段去重：先快速检查新鲜 lock，再用原子创建 lock；另有 content lock 防止 Stop 与 Notification 等不同 hook 对同一内容并发重复通知。

### 通知、声音、聚焦和 webhook

`internal/notifier` 负责桌面通知和声音：

- macOS 优先走 ClaudeNotifier / terminal-notifier，以支持原生通知归属、time sensitive 状态和 click-to-focus；失败时回退到 beeep，权限拒绝则返回明确错误。
- Linux 在 `clickToFocus` 开启时优先走 daemon，并按桌面环境/窗口管理器尝试 GNOME、KDE、Sway/wlroots、X11 等聚焦方式；失败时回退到 beeep。
- Windows 支持通知和声音，但 README 明确说明没有 click-to-focus。
- 多路复用器相关逻辑分布在 `internal/notifier/tmux*.go`、`zellij.go`、`wezterm.go`、`kitty.go` 等文件，通知点击时尽量切到正确 session/pane/tab。
- 声音播放由 `internal/audio` 和 `internal/sounds` 支撑，支持内置声音、系统声音、音量和音频设备选择。

`internal/webhook` 负责 webhook 发送。它在 `webhook.New` 中组合 HTTP client、retry、circuit breaker、rate limiter、metrics 和 formatter；内置 Slack、Discord、Telegram、Lark/Feishu formatter，自定义 endpoint 可用 JSON 或 text 格式。发送路径会校验 URL、套用 headers/payloadFields 模板，并用 request ID 记录日志。

## 重要文档位置

- `README.md`：用户安装、功能、平台支持、配置和手动测试入口。
- `CONTRIBUTING.md`：贡献者命令、测试方式、CI 要求。
- `docs/ARCHITECTURE.md`：更详细的组件说明和数据流。
- `docs/LOCAL_DEVELOPMENT.md`：本地 marketplace、真实 Claude E2E、click-to-focus 手动验证矩阵。
- `docs/CLICK_TO_FOCUS.md`：macOS/Linux 聚焦行为与配置。
- `docs/webhooks/README.md` 及同目录文档：webhook preset、配置、监控和排错。
- `internal/hooks/INTEGRATION_TESTS.md`：`integration` build tag 测试说明。

## 已检查的外部指令文件

当前仓库根目录没有已有 `CLAUDE.md`，也没有发现 `.cursor/rules/`、`.cursorrules` 或 `.github/copilot-instructions.md`。