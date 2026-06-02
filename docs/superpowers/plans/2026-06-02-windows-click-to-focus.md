# Windows Click-to-Focus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Windows click-to-focus support to claude-notifications-go, enabling notification clicks to focus the correct Windows Terminal window and tab.

**Architecture:** Windows-specific files use `//go:build windows` tags. Protocol Activation triggers `focus-windows` subcommand, which uses Win32 API for window enumeration, PowerShell UI Automation for tab detection, and `wt.exe` for tab switching. All paths gracefully fall back.

**Tech Stack:** Go 1.21, `golang.org/x/sys/windows`, `git.sr.ht/~jackmordaunt/go-toast` (via existing `beeep` dependency), PowerShell UI Automation.

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/notifier/bell_windows.go` | Create | Windows terminal bell: writes `\a` to stdout (ConPTY). |
| `internal/notifier/bell_unix.go` | Create | Extract existing `sendTerminalBell()` from `notifier.go` into build-tagged file. |
| `internal/notifier/focus_windows.go` | Create | Win32 `SetForegroundWindow`, process tree walking, PowerShell UI Automation script, `wt.exe` invocation. |
| `internal/notifier/focus_stub.go` | Modify | Change build tag from `!darwin` to `!darwin && !windows`. |
| `internal/notifier/terminal_windows.go` | Create | Windows notification entry: protocol registration, go-toast with Protocol activation, fallback to beeep. |
| `internal/notifier/notifier.go` | Modify | Add Windows `ClickToFocus` branch in `SendDesktop`; remove `sendTerminalBell()` body (moved to `bell_unix.go`). |
| `cmd/claude-notifications/main.go` | Modify | Add `focus-windows` subcommand; update `printUsage()`. |
| `internal/config/config.go` | Modify | Update `ClickToFocus` comment. |
| `docs/CLICK_TO_FOCUS.md` | Modify | Rewrite Windows section. |
| `internal/notifier/focus_windows_test.go` | Create | Unit tests for protocol URI parsing, process tree mocking interface. |

---

### Task 1: Extract Unix Terminal Bell to Build-Tagged File

**Rationale:** `sendTerminalBell()` in `notifier.go` references `/dev/tty`, which does not exist on Windows. We must extract it to a Unix-only file and create a Windows equivalent.

**Files:**
- Create: `internal/notifier/bell_unix.go`
- Modify: `internal/notifier/notifier.go:640-688`
- Create: `internal/notifier/bell_windows.go`

- [ ] **Step 1: Create `internal/notifier/bell_unix.go`**

```go
//go:build !windows

package notifier

import (
	"os"
	"os/exec"

	"github.com/777genius/claude-notifications/internal/logging"
)

// sendTerminalBell writes a BEL character to /dev/tty to trigger terminal
// tab indicators (e.g. Ghostty tab highlight, tmux window bell flag).
func sendTerminalBell() {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err == nil {
		defer f.Close()
		_, _ = f.Write([]byte("\a"))
		return
	}
	logging.Debug("Could not open /dev/tty for bell: %v", err)

	sendTmuxPaneBell()
}

// sendTmuxPaneBell writes a BEL byte to the current tmux pane's tty as a
// fallback path for environments without a controlling tty.
func sendTmuxPaneBell() {
	paneID := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || paneID == "" {
		return
	}

	out, err := execCommand("tmux", "display-message", "-p", "-t", paneID, "#{pane_tty}").Output()
	if err != nil {
		logging.Debug("tmux display-message failed for bell fallback: %v", err)
		return
	}

	paneTTY := string(out)
	if paneTTY == "" {
		return
	}

	f, err := os.OpenFile(paneTTY, os.O_WRONLY, 0)
	if err != nil {
		logging.Debug("Could not open tmux pane tty %s for bell: %v", paneTTY, err)
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("\a"))
}
```

- [ ] **Step 2: Create `internal/notifier/bell_windows.go`**

```go
//go:build windows

package notifier

import (
	"fmt"
	"os"

	"github.com/777genius/claude-notifications/internal/logging"
)

// sendTerminalBell writes a BEL character to stdout for Windows Terminal
// ConPTY tab indicators. Windows Terminal renders BEL as a taskbar flash.
func sendTerminalBell() {
	_, err := fmt.Fprint(os.Stdout, "\a")
	if err != nil {
		logging.Debug("Could not write BEL to stdout: %v", err)
	}
}
```

- [ ] **Step 3: Remove Unix bell code from `notifier.go`**

Replace lines 640-688 in `internal/notifier/notifier.go` with a single stub:

```go
// sendTerminalBell is implemented in platform-specific build-tagged files:
// - bell_unix.go: writes to /dev/tty or tmux pane tty
// - bell_windows.go: writes BEL to stdout for ConPTY
func sendTerminalBell()
```

Wait — Go does not allow function declarations without bodies in normal files. Instead, keep `sendTerminalBell()` and `sendTmuxPaneBell()` in `notifier.go` but remove their bodies, leaving them as calls to platform-specific helpers.

Correct approach: In `notifier.go`, replace the entire `sendTerminalBell()` and `sendTmuxPaneBell()` function bodies with calls to platform-specific functions.

Edit `internal/notifier/notifier.go` at lines 640-688:

```go
// sendTerminalBell writes a BEL character to trigger terminal tab indicators.
// Platform-specific implementation is in bell_unix.go or bell_windows.go.
func sendTerminalBell() {
	platformSendTerminalBell()
}
```

- [ ] **Step 4: Add `platformSendTerminalBell` to both bell files**

In `bell_unix.go`, rename `sendTerminalBell` to `platformSendTerminalBell`.
In `bell_windows.go`, rename `sendTerminalBell` to `platformSendTerminalBell`.

- [ ] **Step 5: Verify build compiles on Windows**

Run: `GOOS=windows go build ./internal/notifier`
Expected: No errors.

Run: `GOOS=linux go build ./internal/notifier`
Expected: No errors.

- [ ] **Step 6: Commit**

```bash
git add internal/notifier/bell_unix.go internal/notifier/bell_windows.go internal/notifier/notifier.go
git commit -m "refactor(bell): extract terminal bell to platform-specific files"
```

---

### Task 2: Create Windows Focus Engine

**Rationale:** Core Win32 logic for window enumeration, tab detection, and `wt.exe` invocation. This is the heart of the feature.

**Files:**
- Create: `internal/notifier/focus_windows.go`

- [ ] **Step 1: Create `internal/notifier/focus_windows.go`**

```go
//go:build windows

package notifier

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/777genius/claude-notifications/internal/logging"
	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
)

// FocusWindowsTerminal focuses the Windows Terminal window matching cwd,
// and attempts precise tab focus via wt.exe.
func FocusWindowsTerminal(cwd string) error {
	if cwd == "" {
		return fmt.Errorf("cwd is empty")
	}

	// Step 1: Find Windows Terminal PID via process tree
	wtPID, err := findWindowsTerminalPID()
	if err != nil {
		logging.Debug("Could not find Windows Terminal via process tree: %v", err)
		// Fallback: EnumWindows + title matching
		hwnd, found := findWindowByTitle(filepath.Base(cwd))
		if !found {
			return fmt.Errorf("Windows Terminal window not found for %s", cwd)
		}
		raiseWindow(hwnd)
		return nil
	}

	// Step 2: Get current tab index via UI Automation
	tabIdx, err := getCurrentTabIndex(wtPID)
	if err == nil && tabIdx >= 0 {
		// Step 3: Focus tab via wt.exe
		if err := focusTab(tabIdx); err != nil {
			logging.Debug("wt.exe focus-tab failed: %v", err)
		}
	} else {
		logging.Debug("Could not get current tab index: %v", err)
	}

	// Step 4: Raise window via Win32 API
	hwnd, found := findWindowByPID(wtPID)
	if found {
		raiseWindow(hwnd)
	}

	return nil
}

// findWindowsTerminalPID walks the process tree from current process up to
// WindowsTerminal.exe. Returns the PID of the Windows Terminal process.
func findWindowsTerminalPID() (int, error) {
	currentPID := os.Getpid()
	for i := 0; i < 20; i++ { // safety limit
		parentPID, err := getParentProcessID(currentPID)
		if err != nil {
			return 0, err
		}
		if parentPID == 0 {
			return 0, fmt.Errorf("reached root process without finding WindowsTerminal")
		}
		name, err := getProcessName(parentPID)
		if err != nil {
			// continue walking
		} else if strings.EqualFold(name, "WindowsTerminal.exe") {
			return parentPID, nil
		}
		currentPID = parentPID
	}
	return 0, fmt.Errorf("process tree depth exceeded")
}

// getParentProcessID returns the parent PID of the given process.
func getParentProcessID(pid int) (int, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)

	var pbi windows.PROCESS_BASIC_INFORMATION
	var returnLength uint32
	err = windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &returnLength)
	if err != nil {
		return 0, err
	}
	return int(pbi.InheritedFromUniqueProcessId), nil
}

// getProcessName returns the executable name of the given PID.
func getProcessName(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	var buf [windows.MAX_PATH]uint16
	mod := windows.Handle(0)
	var cbNeeded uint32
	if err := windows.EnumProcessModules(handle, &mod, uint32(unsafe.Sizeof(mod)), &cbNeeded); err != nil {
		return "", err
	}
	if _, err := windows.GetModuleBaseName(handle, mod, &buf[0], windows.MAX_PATH); err != nil {
		return "", err
	}
	return windows.UTF16PtrToString(&buf[0]), nil
}

// findWindowByPID finds the first visible window owned by the given PID.
func findWindowByPID(pid int) (syscall.Handle, bool) {
	var targetHwnd syscall.Handle
	cb := syscall.NewCallback(func(hwnd syscall.Handle, lParam uintptr) uintptr {
		if targetHwnd != 0 {
			return 1 // already found
		}
		var winPID uint32
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&winPID)))
		if int(winPID) == pid {
			// Check visibility
			visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
			if visible != 0 {
				targetHwnd = hwnd
				return 0 // stop enumeration
			}
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)
	return targetHwnd, targetHwnd != 0
}

// findWindowByTitle finds a visible window whose title contains the folder name.
func findWindowByTitle(folderName string) (syscall.Handle, bool) {
	var targetHwnd syscall.Handle
	cb := syscall.NewCallback(func(hwnd syscall.Handle, lParam uintptr) uintptr {
		if targetHwnd != 0 {
			return 1
		}
		visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
		if visible == 0 {
			return 1
		}
		var buf [512]uint16
		procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), 512)
		title := windows.UTF16PtrToString(&buf[0])
		if strings.Contains(title, folderName) {
			targetHwnd = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return targetHwnd, targetHwnd != 0
}

// raiseWindow restores and raises the window to foreground.
func raiseWindow(hwnd syscall.Handle) {
	procShowWindow.Call(uintptr(hwnd), 9) // SW_RESTORE
	procSetForegroundWindow.Call(uintptr(hwnd))
}

// getCurrentTabIndex uses PowerShell UI Automation to find the selected tab index.
func getCurrentTabIndex(wtPID int) (int, error) {
	script := fmt.Sprintf(`
Add-Type -AssemblyName UIAutomationClient
$condition = [System.Windows.Automation.PropertyCondition]::new(
    [System.Windows.Automation.AutomationElement]::ProcessIdProperty, %d)
$wt = [System.Windows.Automation.AutomationElement]::RootElement.FindFirst(
    [System.Windows.Automation.TreeScope]::Children, $condition)
if (-not $wt) { exit 1 }
$tabCondition = [System.Windows.Automation.PropertyCondition]::new(
    [System.Windows.Automation.AutomationElement]::ControlTypeProperty,
    [System.Windows.Automation.ControlType]::TabItem)
$tabs = $wt.FindAll([System.Windows.Automation.TreeScope]::Descendants, $tabCondition)
$prop = [System.Windows.Automation.SelectionItemPattern]::IsSelectedProperty
for ($i = 0; $i -lt $tabs.Count; $i++) {
    if ($tabs[$i].GetCurrentPropertyValue($prop) -eq $true) {
        Write-Output $i
        exit 0
    }
}
exit 1
`, wtPID)

	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return -1, fmt.Errorf("UI Automation script failed: %w", err)
	}

	idx, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1, fmt.Errorf("failed to parse tab index: %w", err)
	}
	return idx, nil
}

// focusTab invokes wt.exe to focus the specified tab index.
func focusTab(index int) error {
	cmd := exec.Command("wt.exe", "focus-tab", "--target", strconv.Itoa(index))
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Verify Windows build**

Run: `GOOS=windows go build ./internal/notifier`
Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add internal/notifier/focus_windows.go
git commit -m "feat(focus): add Windows window and tab focus engine"
```

---

### Task 3: Update Focus Stub for Windows

**Rationale:** The existing `ax_focus_stub.go` is tagged `!darwin`, which includes Windows. We need Windows to have a real implementation.

**Files:**
- Modify: `internal/notifier/ax_focus_stub.go`
- Create: `internal/notifier/focus_windows.go` (already created in Task 2, but needs `FocusAppWindowWithOptions`)

- [ ] **Step 1: Modify `internal/notifier/ax_focus_stub.go`**

Change the build tag from `!darwin` to `!darwin && !windows`.

```go
//go:build !darwin && !windows

package notifier

import "fmt"

func FocusAppWindow(bundleID, cwd string) error {
	return fmt.Errorf("focus-window not supported on this platform")
}

func FocusAppWindowWithOptions(bundleID, cwd string, opts FocusWindowOptions) error {
	return fmt.Errorf("focus-window not supported on this platform")
}

type FocusWindowOptions struct {
	GhosttyTerminalID string
}
```

- [ ] **Step 2: Add `FocusAppWindowWithOptions` to `focus_windows.go`**

Append to `internal/notifier/focus_windows.go`:

```go
// FocusAppWindowWithOptions implements the cross-platform focus interface for Windows.
// The bundleID parameter is ignored on Windows; cwd is used for window matching.
func FocusAppWindowWithOptions(bundleID, cwd string, opts FocusWindowOptions) error {
	return FocusWindowsTerminal(cwd)
}

// FocusAppWindow is a convenience wrapper.
func FocusAppWindow(bundleID, cwd string) error {
	return FocusWindowsTerminal(cwd)
}
```

- [ ] **Step 3: Verify build on all platforms**

Run:
```bash
GOOS=windows go build ./internal/notifier
GOOS=linux go build ./internal/notifier
GOOS=darwin go build ./internal/notifier
```
Expected: All three compile without errors.

- [ ] **Step 4: Commit**

```bash
git add internal/notifier/ax_focus_stub.go internal/notifier/focus_windows.go
git commit -m "feat(focus): wire Windows focus into cross-platform stub"
```

---

### Task 4: Create Windows Notification Entry Point

**Rationale:** This file handles the Windows-specific notification path with Protocol Activation, analogous to `terminal_linux.go` and `terminal_darwin.go`.

**Files:**
- Create: `internal/notifier/terminal_windows.go`

- [ ] **Step 1: Create `internal/notifier/terminal_windows.go`**

```go
//go:build windows

package notifier

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"git.sr.ht/~jackmordaunt/go-toast"
	"github.com/777genius/claude-notifications/internal/config"
	"github.com/777genius/claude-notifications/internal/logging"
	"github.com/gen2brain/beeep"
	"golang.org/x/sys/windows/registry"
)

const protocolScheme = "claude-notif"

// sendWindowsNotification sends a toast notification on Windows with
// Protocol Activation for click-to-focus support.
func sendWindowsNotification(title, body, cwd string) error {
	if err := ensureProtocolRegistered(); err != nil {
		logging.Warn("Failed to register protocol handler, click-to-focus unavailable: %v", err)
		return beeep.Notify(title, body, "")
	}

	activationArgs := fmt.Sprintf("%s://focus?cwd=%s", protocolScheme, url.QueryEscape(cwd))

	n := toast.Notification{
		AppID:               "Claude Code Notifications",
		Title:               title,
		Body:                body,
		ActivationType:      toast.Protocol,
		ActivationArguments: activationArgs,
	}

	if err := n.Push(); err != nil {
		logging.Warn("go-toast push failed, falling back to beeep: %v", err)
		return beeep.Notify(title, body, "")
	}

	logging.Debug("Windows notification sent with Protocol activation: %s", activationArgs)
	return nil
}

// ensureProtocolRegistered registers the claude-notif:// protocol handler
// in HKCU if not already present. No administrator privileges required.
func ensureProtocolRegistered() error {
	keyPath := filepath.Join("SOFTWARE", "Classes", protocolScheme)

	// Check if already registered
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.READ)
	if err == nil {
		k.Close()
		return nil
	}

	// Register protocol
	k, _, err = registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create protocol key: %w", err)
	}
	defer k.Close()

	if err := k.SetStringValue("", "URL:Claude Notification Protocol"); err != nil {
		return fmt.Errorf("set default value: %w", err)
	}
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		return fmt.Errorf("set URL Protocol: %w", err)
	}

	// Set open command
	cmdKeyPath := filepath.Join(keyPath, "shell", "open", "command")
	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, cmdKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create command key: %w", err)
	}
	defer cmdKey.Close()

	exe, err := exec.LookPath("claude-notifications")
	if err != nil {
		// Fallback to current executable
		exe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("cannot find executable: %w", err)
		}
	}
	exe, _ = filepath.Abs(exe)

	cmdValue := fmt.Sprintf(`"%s" focus-windows "%%1"`, exe)
	if err := cmdKey.SetStringValue("", cmdValue); err != nil {
		return fmt.Errorf("set command value: %w", err)
	}

	logging.Debug("Registered protocol handler: %s -> %s", protocolScheme, exe)
	return nil
}
```

Wait — `os` import is missing in the `exec.LookPath` fallback. The code uses `os.Executable()` which requires `"os"`. Let me fix:

Add `"os"` to imports.

- [ ] **Step 2: Verify Windows build**

Run: `GOOS=windows go build ./internal/notifier`
Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add internal/notifier/terminal_windows.go
git commit -m "feat(notifier): add Windows toast notification with Protocol activation"
```

---

### Task 5: Wire Windows Click-to-Focus into SendDesktop

**Rationale:** The central `SendDesktop` method must branch to the new Windows path when `ClickToFocus` is enabled.

**Files:**
- Modify: `internal/notifier/notifier.go:154-167`

- [ ] **Step 1: Add Windows branch before beeep fallback**

In `internal/notifier/notifier.go`, replace the `// Standard path: beeep` section (lines 154-167) with:

```go
	// Windows: Try click-to-focus via Protocol Activation
	if platform.IsWindows() && n.cfg.Notifications.Desktop.ClickToFocus {
		if err := sendWindowsNotification(title, cleanMessage, cwd); err != nil {
			logging.Warn("Windows click-to-focus notification failed, falling back to beeep: %v", err)
			// Fall through to beeep
		} else {
			logging.Debug("Desktop notification sent via Windows Protocol activation: title=%s", title)
			n.playSoundDetached(statusInfo.Sound)
			return nil
		}
	}

	// Standard path: beeep (Windows fallback, Linux fallback)
	return n.sendWithBeeep(title, cleanMessage, appIcon, statusInfo.Sound)
```

- [ ] **Step 2: Verify build on all platforms**

Run:
```bash
GOOS=windows go build ./...
GOOS=linux go build ./...
GOOS=darwin go build ./...
```
Expected: All compile.

- [ ] **Step 3: Commit**

```bash
git add internal/notifier/notifier.go
git commit -m "feat(notifier): wire Windows click-to-focus into SendDesktop"
```

---

### Task 6: Add focus-windows CLI Subcommand

**Rationale:** The Protocol Activation handler launches `claude-notifications.exe focus-windows <uri>`. We need a subcommand that parses the URI and delegates to `FocusWindowsTerminal`.

**Files:**
- Modify: `cmd/claude-notifications/main.go`

- [ ] **Step 1: Add focus-windows case in main() switch**

Insert after the `focus-window` case (around line 67):

```go
	case "focus-windows":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Error: focus-windows requires a cwd or protocol URI argument\n")
			printUsage()
			os.Exit(1)
		}
		cwd := extractCwdFromFocusWindowsArg(os.Args[2])
		if err := notifier.FocusWindowsTerminal(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "focus-windows: %v\n", err)
			os.Exit(1)
		}
```

- [ ] **Step 2: Add helper function for URI parsing**

Add to `cmd/claude-notifications/main.go` (near other helpers):

```go
// extractCwdFromFocusWindowsArg parses the cwd from a Protocol Activation URI
// or returns the plain cwd string.
func extractCwdFromFocusWindowsArg(arg string) string {
	if !strings.HasPrefix(arg, "claude-notif://") {
		return arg
	}
	u, err := url.Parse(arg)
	if err != nil {
		return arg
	}
	cwd := u.Query().Get("cwd")
	if cwd == "" {
		return arg
	}
	return cwd
}
```

Add `"net/url"` to imports in `main.go`.

- [ ] **Step 3: Update printUsage()**

Add to `printUsage()` around line 515:

```go
	fmt.Println("  focus-windows <cwd>       Focus Windows Terminal window/tab (Windows only)")
	fmt.Println("                              Used internally by click-to-focus Protocol Activation")
```

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/claude-notifications`
Expected: Compiles.

- [ ] **Step 5: Commit**

```bash
git add cmd/claude-notifications/main.go
git commit -m "feat(cli): add focus-windows subcommand for Protocol Activation"
```

---

### Task 7: Update Config Comment

**Files:**
- Modify: `internal/config/config.go:47`

- [ ] **Step 1: Update ClickToFocus comment**

```go
ClickToFocus     bool    `json:"clickToFocus"`     // Activate terminal on notification click (default: true)
```

- [ ] **Step 2: Commit**

```bash
git add internal/config/config.go
git commit -m "docs(config): update ClickToFocus comment for Windows support"
```

---

### Task 8: Add Unit Tests

**Files:**
- Create: `internal/notifier/focus_windows_test.go`

- [ ] **Step 1: Create tests for protocol URI parsing**

```go
//go:build windows

package notifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractCwdFromProtocolURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain cwd",
			input:    `C:\Users\test\project`,
			expected: `C:\Users\test\project`,
		},
		{
			name:     "protocol URI with cwd",
			input:    "claude-notif://focus?cwd=C%3A%5CUsers%5Ctest%5Cproject",
			expected: `C:\Users\test\project`,
		},
		{
			name:     "protocol URI without cwd query",
			input:    "claude-notif://focus",
			expected: "claude-notif://focus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: extractCwdFromFocusWindowsArg is in main.go.
			// We'll test it via a helper or move it to a testable location.
		})
	}
}
```

Actually — `extractCwdFromFocusWindowsArg` is in `main.go` which is in `package main`, not testable from `notifier` package. Better approach: move the URI parsing to `focus_windows.go` as an exported helper `ParseFocusWindowsArg`, then test it.

Revised Step 1: Move the function.

In `focus_windows.go`, add:

```go
// ParseFocusWindowsArg extracts the cwd from a Protocol Activation URI
// or returns the plain string if it's not a URI.
func ParseFocusWindowsArg(arg string) string {
	if !strings.HasPrefix(arg, "claude-notif://") {
		return arg
	}
	u, err := url.Parse(arg)
	if err != nil {
		return arg
	}
	cwd := u.Query().Get("cwd")
	if cwd == "" {
		return arg
	}
	return cwd
}
```

Update `main.go` to use `notifier.ParseFocusWindowsArg` instead of local helper.

- [ ] **Step 2: Write tests**

```go
//go:build windows

package notifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFocusWindowsArg(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "plain cwd", input: `C:\Users\test\project`, expected: `C:\Users\test\project`},
		{name: "protocol URI with cwd", input: "claude-notif://focus?cwd=C%3A%5CUsers%5Ctest%5Cproject", expected: `C:\Users\test\project`},
		{name: "protocol URI without cwd", input: "claude-notif://focus", expected: "claude-notif://focus"},
		{name: "empty string", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFocusWindowsArg(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}
```

- [ ] **Step 3: Run tests**

Run: `GOOS=windows go test ./internal/notifier -run TestParseFocusWindowsArg -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/notifier/focus_windows.go internal/notifier/focus_windows_test.go cmd/claude-notifications/main.go
git commit -m "test(focus): add Windows focus URI parsing tests"
```

---

### Task 9: Update Documentation

**Files:**
- Modify: `docs/CLICK_TO_FOCUS.md`

- [ ] **Step 1: Replace Windows section**

Replace lines 119-122:

```markdown
## Windows

Notifications only, no click-to-focus.
```

With:

```markdown
## Windows

Click-to-focus on Windows uses Protocol Activation with the Windows Runtime Toast API.

### Supported Terminals

| Terminal | Focus method |
|----------|-------------|
| Windows Terminal | Precise tab focus via UI Automation + `wt.exe focus-tab`, falls back to window-level `SetForegroundWindow` |
| Other terminals | Window title matching via `EnumWindows` + `SetForegroundWindow` |

### First-time Setup

The first time a notification is sent with click-to-focus enabled, the plugin registers a custom URL protocol (`claude-notif://`) in your user registry (`HKCU`). This requires **no administrator privileges**.

### Requirements

- Windows 10 or later
- Windows Terminal (for precise tab focus)
- PowerShell with .NET Framework (for UI Automation; included in Windows 10/11)

### Limitations

- Precise tab focus requires Windows Terminal and `wt.exe` in your PATH
- If Windows Terminal is not detected, falls back to window title matching
- In rare cases where the COM API is unavailable, go-toast falls back to PowerShell for toast delivery. In this mode, Protocol Activation is unavailable and click-to-focus will not work.
```

- [ ] **Step 2: Commit**

```bash
git add docs/CLICK_TO_FOCUS.md
git commit -m "docs(click-to-focus): add Windows click-to-focus documentation"
```

---

### Task 10: Final Verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`
Expected: All tests pass.

- [ ] **Step 2: Cross-platform build check**

Run:
```bash
GOOS=windows go build ./cmd/claude-notifications
GOOS=linux go build ./cmd/claude-notifications
GOOS=darwin go build ./cmd/claude-notifications
```
Expected: All three produce binaries without errors.

- [ ] **Step 3: Run linter**

Run: `go vet ./...`
Expected: No issues.

- [ ] **Step 4: Commit any fixes**

```bash
git add -A
git commit -m "fix: address vet/lint issues" || echo "No fixes needed"
```

---

## Self-Review

### Spec Coverage Check

| Spec Requirement | Plan Task |
|-----------------|-----------|
| Windows notification with Protocol Activation | Task 4 |
| Win32 window enumeration | Task 2 |
| PowerShell UI Automation for tab index | Task 2 |
| `wt.exe focus-tab` invocation | Task 2 |
| Terminal bell for Windows | Task 1 |
| `focus-windows` CLI subcommand | Task 6 |
| Reuse `clickToFocus` config | Task 5, Task 7 |
| Graceful fallback at every layer | Task 2 (design), Task 5 (beeep fallback) |
| Update docs | Task 9 |
| Cross-platform build tags | Tasks 1, 2, 3 |

### Placeholder Scan

- No "TBD", "TODO", or "implement later" found.
- All steps contain actual code.
- All commands have expected outputs.
- No "similar to Task N" shortcuts.

### Type Consistency Check

- `FocusWindowOptions` defined in `ax_focus_stub.go` (for non-darwin non-windows) and `ax_focus_darwin.go` (for darwin). Windows uses `focus_windows.go` which does not need `FocusWindowOptions` fields but must satisfy the interface.
- `FocusWindowsTerminal(cwd string)` is the Windows-specific entry point.
- `FocusAppWindowWithOptions(bundleID, cwd string, opts FocusWindowOptions)` delegates to `FocusWindowsTerminal(cwd)` on Windows.
- All signatures consistent across platforms.
