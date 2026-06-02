# Windows Click-to-Focus Design

## Issue

[Feature Request: Click-to-Focus support for Windows](https://github.com/777genius/claude-notifications-go/issues/36)

## Summary

Bring click-to-focus parity to Windows, matching the existing macOS and Linux behavior. When a user clicks a desktop notification on Windows, the originating terminal window (and ideally tab) is brought to the foreground.

## Goals

- Clicking a notification on Windows focuses the correct terminal window
- For Windows Terminal users: attempt precise tab-level focus
- Send terminal bell as additional visual cue
- Reuse existing `clickToFocus` config flag — no new config keys
- Graceful fallback at every layer (tab → window → nothing)
- No crash if matching window/tab is not found

## Non-Goals

- Support for every terminal emulator on day one (Windows Terminal + PowerShell 7 are primary)
- COM callback / in-process activation (hook is short-lived, not a daemon)
- Modifying `beeep` library upstream
- Changing macOS or Linux behavior

## User Environment

- OS: Windows 10/11
- Terminal: Windows Terminal + PowerShell 7
- This matches the primary environment of the feature requester

## Architecture

### New Files

| File | Purpose |
|------|---------|
| `internal/notifier/terminal_windows.go` | Windows platform notification entry point. Handles click-to-focus path when enabled, falls back to beeep. |
| `internal/notifier/focus_windows.go` | Win32 window enumeration, UI Automation tab detection, `wt.exe focus-tab` invocation. |
| `internal/notifier/bell_windows.go` | Windows terminal bell implementation (ConPTY BEL character). |

### Modified Files

| File | Changes |
|------|---------|
| `internal/notifier/notifier.go` | In `SendDesktop`, add Windows + `ClickToFocus` branch before the `sendWithBeeep` fallback. |
| `internal/notifier/ax_focus_stub.go` | Extend `!darwin` stub: Windows gets a real `FocusAppWindowWithOptions` that delegates to `focus_windows.go`. Non-Windows non-Darwin keeps the error stub. |
| `cmd/claude-notifications/main.go` | Add `focus-windows <cwd>` subcommand for Protocol Activation callback. |
| `internal/config/config.go` | Update `ClickToFocus` comment from "macOS only" to "macOS, Linux, Windows". |
| `docs/CLICK_TO_FOCUS.md` | Rewrite Windows section with setup, supported terminals, and troubleshooting. |

### Data Flow

```
Hook fires
  → notifier.SendDesktop(status, message, sessionID, cwd)
    → if Windows && ClickToFocus:
      1. sendWindowsNotification(title, body, cwd)
         - Build go-toast Notification with:
           ActivationType = Protocol
           ActivationArguments = "claude-notif://focus?cwd=<cwd>"
         - Register claude-notif:// protocol handler on first use (HKCU)
         - Push toast via go-toast (COM API or PowerShell fallback)
      2. sendWindowsBell()
         - Write BEL (\\a) to the ConPTY of the current process
    → else:
      - Existing beeep path (unchanged)

User clicks notification
  → Windows launches: claude-notifications.exe focus-windows <cwd>
    → focus_windows.go:
      1. findWindowsTerminalWindow(cwd)
         - Walk process tree from current process up to WindowsTerminal.exe
         - Fallback: EnumWindows + title matching with folder name
      2. getCurrentTabIndex(wtPID)
         - Invoke embedded PowerShell script using UI Automation
         - Script enumerates TabItem controls, finds IsSelected=true
         - Returns index or -1 on failure
      3. if index >= 0:
           exec.Command("wt.exe", "focus-tab", "--target", strconv.Itoa(index))
      4. ShowWindow(hwnd, SW_RESTORE) + SetForegroundWindow(hwnd)
    → If any step fails, log and continue to next fallback layer
```

## Components

### 1. Windows Notification (`terminal_windows.go`)

Builds on the existing `go-toast` dependency (already pulled in via `beeep`).

```go
func sendWindowsNotification(title, body, cwd string) error
```

**Protocol registration:**
- On first notification send, check if `HKCU\SOFTWARE\Classes\claude-notif` exists
- If not, write registry keys to register the protocol:
  - `HKCU\SOFTWARE\Classes\claude-notif` → `(Default)` = "URL:Claude Notification Protocol"
  - `HKCU\SOFTWARE\Classes\claude-notif` → `URL Protocol` = ""
  - `HKCU\SOFTWARE\Classes\claude-notif\shell\open\command` → `(Default)` = `"<exe>" focus-windows "%1"`
- Registration is idempotent (check before write)

**Toast construction:**
```go
n := toast.Notification{
    AppID:               "Claude Code Notifications",
    Title:               title,
    Body:                body,
    ActivationType:      toast.Protocol,
    ActivationArguments: "claude-notif://focus?cwd=" + url.QueryEscape(cwd),
}
```

**Note on PowerShell fallback:**
- `go-toast` falls back to PowerShell when COM is unavailable
- PowerShell fallback does NOT support Protocol activation callbacks
- In fallback mode, the notification still displays, but click-to-focus is unavailable
- Log a warning when this happens

### 2. Window Focus (`focus_windows.go`)

```go
func FocusWindowsTerminal(cwd string) error
```

**Step 1: Find Windows Terminal window**

Primary method: process tree walking.
- Start from `os.Getpid()`
- Walk up `ParentProcessId` via `NtQueryInformationProcess` or WMI
- Stop when parent is `WindowsTerminal.exe`
- Record the PID

Fallback method: `EnumWindows` + title match.
- `EnumWindows` callback checks each window
- `GetWindowThreadProcessId` to get owner PID
- `GetWindowText` to check if title contains `filepath.Base(cwd)`
- Also accept match if title contains the folder name from `extractSessionInfo`

**Step 2: Get current tab index**

```go
func getWindowsTerminalTabIndex(wtPID int) (int, error)
```

Implementation: invoke embedded PowerShell script.

```powershell
Add-Type -AssemblyName UIAutomationClient
$wt = [System.Windows.Automation.AutomationElement]::RootElement.FindFirst(
    [System.Windows.Automation.TreeScope]::Children,
    [System.Windows.Automation.PropertyCondition]::new(
        [System.Windows.Automation.AutomationElement]::ProcessIdProperty, WT_PID))
$tabs = $wt.FindAll([System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.PropertyCondition]::new(
        [System.Windows.Automation.AutomationElement]::ControlTypeProperty,
        [System.Windows.Automation.ControlType]::TabItem))
$prop = [System.Windows.Automation.SelectionItemPattern]::IsSelectedProperty
for ($i = 0; $i -lt $tabs.Count; $i++) {
    if ($tabs[$i].GetCurrentPropertyValue($prop) -eq $true) { return $i }
}
return -1
```

The script is embedded as a constant string in the Go binary and executed via:
```go
out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
```

**Step 3: Focus tab**

```go
if idx >= 0 {
    exec.Command("wt.exe", "focus-tab", "--target", strconv.Itoa(idx)).Run()
}
```

**Step 4: Raise window**

```go
user32 := windows.NewLazySystemDLL("user32.dll")
procShowWindow := user32.NewProc("ShowWindow")
procSetForegroundWindow := user32.NewProc("SetForegroundWindow")

procShowWindow.Call(uintptr(hwnd), 9) // SW_RESTORE
procSetForegroundWindow.Call(uintptr(hwnd))
```

### 3. Terminal Bell (`bell_windows.go`)

Windows does not have `/dev/tty`. The bell must be sent through the ConPTY.

```go
func sendWindowsBell() error
```

Implementation options (in order of preference):
1. Write `\a` to `CONOUT$` handle
2. Use `WriteConsole` with `ENABLE_VIRTUAL_TERMINAL_PROCESSING`
3. Fallback: `fmt.Print("\a")` to stdout

The existing `sendTerminalBell()` in `notifier.go` is Unix-only (`/dev/tty`). For Windows, we add a build-tagged `sendTerminalBell()` in `bell_windows.go` that uses the ConPTY approach.

### 4. CLI Subcommand (`main.go`)

```go
case "focus-windows":
    if len(os.Args) < 3 {
        fmt.Fprintln(os.Stderr, "focus-windows requires cwd argument")
        os.Exit(1)
    }
    cwd := os.Args[2]
    // Protocol activation passes the full URI, extract cwd query param
    if strings.HasPrefix(cwd, "claude-notif://") {
        cwd = extractCwdFromProtocolURI(cwd)
    }
    if err := notifier.FocusWindowsTerminal(cwd); err != nil {
        logging.Error("focus-windows failed: %v", err)
        os.Exit(1)
    }
```

## Build Tags

```
//go:build windows
```

All new files use the `windows` build tag. The existing `ax_focus_stub.go` uses `!darwin` — we need to refine it so that Windows gets the real implementation while Linux keeps the stub/error.

Approach: create `focus_stub.go` with `//go:build !darwin && !windows`.

## Configuration

No new config keys. Reuse existing:

```json
{
  "notifications": {
    "desktop": {
      "clickToFocus": true
    }
  }
}
```

Update `config.go` comment:
```go
ClickToFocus bool `json:"clickToFocus"` // Activate terminal on notification click (default: true)
```

## Fallback Strategy (Layered)

| Layer | Action | Failure Mode |
|-------|--------|-------------|
| L1 | Tab focus via `wt.exe focus-tab` | UI Automation fails, or wt.exe not found |
| L2 | Window focus via `SetForegroundWindow` | Window not found by PID/title |
| L3 | Notification still sent, no focus | Beeep fallback if toast fails |

Every layer logs the failure and proceeds to the next. No user-visible error.

## Error Handling

- Protocol registration failure → log warning, skip click-to-focus, still send notification
- `wt.exe` not found → skip tab focus, try window focus
- UI Automation script failure → skip tab focus, try window focus
- Window not found → log debug, exit silently
- All focus attempts fail → notification was already sent, user just doesn't get click-to-focus

## Testing Strategy

### Unit Tests

- `focus_windows_test.go` (build tag `windows`):
  - Mock `EnumWindows` / process tree walking with interface injection
  - Test fallback chain logic
  - Test protocol URI parsing

### Integration Tests

- Manual test matrix (see `docs/CLICK_TO_FOCUS.md`):
  1. Single Windows Terminal window, single tab
  2. Single Windows Terminal window, multiple tabs
  3. Multiple Windows Terminal windows
  4. Windows Terminal minimized
  5. Click-to-focus disabled in config
  6. wt.exe not in PATH

### CI

- Existing Windows CI runs `go test ./...` — new tests must pass
- No new CI dependencies

## Documentation Updates

### `docs/CLICK_TO_FOCUS.md`

Replace:
```
## Windows

Notifications only, no click-to-focus.
```

With:
```
## Windows

Uses Win32 API + Windows Terminal CLI for window/tab focus.

| Terminal | Focus method |
|----------|-------------|
| Windows Terminal | Precise tab focus via UI Automation + `wt.exe focus-tab`, falls back to window-level `SetForegroundWindow` |
| Other terminals | Window title matching via `EnumWindows` + `SetForegroundWindow` |

### First-time setup

The first time a notification is sent with click-to-focus enabled, the plugin registers a custom URL protocol (`claude-notif://`) in your user registry (`HKCU`). This requires no administrator privileges.

### Limitations

- Precise tab focus requires Windows Terminal and `wt.exe` in PATH
- If Windows Terminal is not detected, falls back to window title matching
- PowerShell UI Automation is used internally; it requires .NET Framework (included in Windows 10/11)
```

## Dependencies

No new Go module dependencies. All required packages are already in `go.mod`:
- `golang.org/x/sys/windows` (already present)
- `git.sr.ht/~jackmordaunt/go-toast` (already present via `beeep`)

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| UI Automation script is fragile across Windows Terminal versions | Fallback to window-level focus; script is simple (just enumerate TabItem + check IsSelected) |
| Protocol registration fails (registry permissions) | Check HKCU first (no admin needed); fallback to no click-to-focus |
| `wt.exe` not in PATH | Check `where wt.exe` or known paths; fallback to window focus |
| go-toast PowerShell fallback does not support Protocol activation | Log warning; notification still displays |
| Multiple Windows Terminal windows with same title | Use `WT_SESSION` env var + process tree to identify correct window |

## Future Work

- Support for other terminals (ConEmu, Cmder, etc.) via title matching
- Windows Terminal JSON API when it becomes available (eliminating UI Automation dependency)
- Focus to specific pane within a tab (Windows Terminal does not expose this)
