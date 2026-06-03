//go:build windows

package notifier

import (
	"fmt"
	"net/url"
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
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
)

// FocusWindowsTerminal focuses the Windows Terminal window matching cwd,
// and attempts precise tab focus via wt.exe.
func FocusWindowsTerminal(cwd string) error {
	if cwd == "" {
		return fmt.Errorf("cwd is empty")
	}

	// Step 1: Find Windows Terminal PID.
	// When invoked from a notification click, the process tree no longer
	// connects to the originating terminal. Try the saved PID first.
	var wtPID int
	savedPID, err := loadSavedTerminalPID()
	if err == nil && savedPID > 0 {
		// Validate the saved PID still exists and is WindowsTerminal
		if name, _ := getProcessName(savedPID); strings.EqualFold(name, "WindowsTerminal.exe") {
			wtPID = savedPID
			logging.Debug("Using saved Windows Terminal PID: %d", wtPID)
		}
	}

	if wtPID == 0 {
		// Fallback: walk process tree (works when invoked from hook context)
		wtPID, err = findWindowsTerminalPID()
		if err != nil {
			logging.Debug("Could not find Windows Terminal via process tree: %v", err)
			// Final fallback: EnumWindows + title matching
			hwnd, found := findWindowByTitle(filepath.Base(cwd))
			if !found {
				return fmt.Errorf("Windows Terminal window not found for %s", cwd)
			}
			raiseWindow(hwnd)
			return nil
		}
	}

	// Step 2: Raise window via Win32 API
	// Note: wt.exe focus-tab is NOT used here because calling it from a
	// Protocol Activation process (outside any Windows Terminal window)
	// creates a NEW Windows Terminal window instead of focusing the existing
	// one. Window-level focus via SetForegroundWindow is reliable.
	hwnd, found := findWindowByPID(wtPID)
	if found {
		raiseWindow(hwnd)
		logging.Debug("Window focused for PID %d", wtPID)
	} else {
		logging.Debug("Window not found for PID %d", wtPID)
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
			logging.Debug("findWindowsTerminalPID step %d: getParentProcessID(%d) failed: %v", i, currentPID, err)
			return 0, err
		}
		if parentPID == 0 {
			logging.Debug("findWindowsTerminalPID step %d: reached root (currentPID=%d)", i, currentPID)
			return 0, fmt.Errorf("reached root process without finding WindowsTerminal")
		}
		name, err := getProcessName(parentPID)
		if err != nil {
			logging.Debug("findWindowsTerminalPID step %d: parent=%d name=UNKNOWN err=%v", i, parentPID, err)
		} else {
			logging.Debug("findWindowsTerminalPID step %d: parent=%d name=%s", i, parentPID, name)
			if strings.EqualFold(name, "WindowsTerminal.exe") {
				return parentPID, nil
			}
		}
		currentPID = parentPID
	}
	return 0, fmt.Errorf("process tree depth exceeded")
}

// getParentProcessID returns the parent PID of the given process using
// CreateToolhelp32Snapshot.
func getParentProcessID(pid int) (int, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return 0, err
	}

	for {
		if int(entry.ProcessID) == pid {
			return int(entry.ParentProcessID), nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return 0, fmt.Errorf("process %d not found", pid)
}

// getProcessName returns the executable name of the given PID using
// CreateToolhelp32Snapshot.
func getProcessName(pid int) (string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "", err
	}

	for {
		if int(entry.ProcessID) == pid {
			return windows.UTF16PtrToString(&entry.ExeFile[0]), nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return "", fmt.Errorf("process %d not found", pid)
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
// Uses AttachThreadInput to bypass the foreground-window restriction.
func raiseWindow(hwnd syscall.Handle) {
	procShowWindow.Call(uintptr(hwnd), 9) // SW_RESTORE

	// Get target window thread
	targetTID, _, _ := procGetWindowThreadProcessId.Call(uintptr(hwnd), 0)

	// Get current foreground window thread
	fgHwnd, _, _ := procGetForegroundWindow.Call()
	fgTID, _, _ := procGetWindowThreadProcessId.Call(fgHwnd, 0)

	// Attach threads so SetForegroundWindow works
	if targetTID != fgTID {
		procAttachThreadInput.Call(fgTID, targetTID, 1)
	}

	procSetForegroundWindow.Call(uintptr(hwnd))

	// Detach threads
	if targetTID != fgTID {
		procAttachThreadInput.Call(fgTID, targetTID, 0)
	}
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

// savedTerminalPIDPath returns the temp file path used to store the
// Windows Terminal PID between hook time and notification-click time.
func savedTerminalPIDPath() string {
	return filepath.Join(os.TempDir(), "claude-notif-terminal.pid")
}

// saveTerminalPID stores the given Windows Terminal PID to a temp file
// so that the click-to-focus handler can locate the window even though
// the Protocol Activation process has a different parent chain.
func saveTerminalPID(pid int) error {
	path := savedTerminalPIDPath()
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

// loadSavedTerminalPID reads the previously saved Windows Terminal PID.
func loadSavedTerminalPID() (int, error) {
	path := savedTerminalPIDPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

// FocusWindowOptions holds optional parameters for window focus.
// Defined on Windows for cross-platform interface compatibility.
type FocusWindowOptions struct {
	GhosttyTerminalID string
}

// FocusAppWindowWithOptions implements the cross-platform focus interface for Windows.
func FocusAppWindowWithOptions(bundleID, cwd string, opts FocusWindowOptions) error {
	return FocusWindowsTerminal(cwd)
}

// FocusAppWindow is a convenience wrapper.
func FocusAppWindow(bundleID, cwd string) error {
	return FocusWindowsTerminal(cwd)
}
