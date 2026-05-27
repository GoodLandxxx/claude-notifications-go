//go:build linux

// ABOUTME: Window focus methods for Linux desktop environments.
// ABOUTME: Implements a fallback chain to focus windows on GNOME, KDE, Sway, and other compositors.
package daemon

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// FocusMethod represents a method for focusing a window
type FocusMethod struct {
	Name string
	Fn   func(terminalName, folderName string) error
}

// GetFocusMethods returns the ordered list of focus methods to try
func GetFocusMethods() []FocusMethod {
	return []FocusMethod{
		{"activate-window-by-title extension", TryActivateWindowByTitle},
		{"GNOME Shell Eval (by window title)", TryGnomeShellEvalByTitle},
		{"GNOME Shell Eval (by app)", TryGnomeShellEval},
		{"GNOME Shell FocusApp", TryGnomeFocusApp},
		{"wlrctl", TryWlrctl},
		{"kdotool", TryKdotool},
		{"xdotool", TryXdotool},
	}
}

// TryFocus attempts to focus a window using available tools.
// folderName is the project folder name used for title-based window search (may be empty).
// It tries each method in order until one succeeds.
func TryFocus(terminalName, folderName string) error {
	return TryFocusWithHints(terminalName, folderName, "", "", "", "")
}

// TryFocusWithWindowID preserves the previous API for callers that only have an exact X11 window ID.
func TryFocusWithWindowID(terminalName, folderName, windowID string) error {
	return TryFocusWithHints(terminalName, folderName, windowID, "", "", "")
}

// TryFocusWithHints attempts exact focus using hook-time hints first, then falls back to
// compositor-specific methods.
// wezTermPaneID and wezTermSocket enable tab-level focus for WezTerm.
func TryFocusWithHints(terminalName, folderName, windowID, windowTitle, wezTermPaneID, wezTermSocket string) error {
	// WezTerm pane focus: highest priority — switches to the exact tab.
	if strings.TrimSpace(wezTermPaneID) != "" {
		if err := TryWezTermPane(wezTermPaneID, wezTermSocket); err == nil {
			return nil
		}
	}

	var exactErr error
	if strings.TrimSpace(windowID) != "" {
		if err := tryX11WindowID(windowID); err == nil {
			return nil
		} else {
			exactErr = err
		}
	}
	if strings.TrimSpace(windowTitle) != "" {
		if err := tryWindowTitle(windowTitle); err == nil {
			return nil
		} else if exactErr != nil {
			exactErr = fmt.Errorf("%v; exact title focus failed: %v", exactErr, err)
		} else {
			exactErr = fmt.Errorf("exact title focus failed: %v", err)
		}
	}

	methods := GetFocusMethods()

	var lastErr error
	for _, method := range methods {
		if err := method.Fn(terminalName, folderName); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	if exactErr != nil && lastErr != nil {
		return fmt.Errorf("%v; fallback focus failed, last error: %v", exactErr, lastErr)
	}
	if exactErr != nil {
		return exactErr
	}
	return fmt.Errorf("all focus methods failed, last error: %v", lastErr)
}

// TryWezTermPane activates a specific WezTerm pane by ID using the WezTerm CLI.
// This switches to the exact tab/pane where Claude is running.
func TryWezTermPane(paneID, socketPath string) error {
	if _, err := exec.LookPath("wezterm"); err != nil {
		return fmt.Errorf("wezterm not installed")
	}

	args := []string{"cli", "activate-pane", "--pane-id", paneID}
	if strings.TrimSpace(socketPath) != "" {
		args = append(args, "--unix-socket", socketPath)
	}

	cmd := exec.Command("wezterm", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wezterm cli activate-pane failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func tryX11WindowID(windowID string) error {
	normalizedID, err := normalizeX11WindowID(windowID)
	if err != nil {
		return fmt.Errorf("invalid X11 window id %q: %w", windowID, err)
	}

	var errs []string

	if err := activateWindowIDWithXdotool(normalizedID); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	if err := activateWindowIDWithWmctrl(normalizedID); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	return fmt.Errorf("exact X11 focus failed: %s", strings.Join(errs, "; "))
}

func activateWindowIDWithXdotool(windowID string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	cmd := exec.Command("xdotool", "windowactivate", "--sync", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xdotool windowactivate failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func activateWindowIDWithWmctrl(windowID string) error {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return fmt.Errorf("wmctrl not installed")
	}

	cmd := exec.Command("wmctrl", "-i", "-a", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmctrl -i -a failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func normalizeX11WindowID(windowID string) (string, error) {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return "", fmt.Errorf("empty window id")
	}

	id, err := strconv.ParseUint(windowID, 0, 64)
	if err != nil {
		return "", err
	}

	return strconv.FormatUint(id, 10), nil
}

func tryWindowTitle(windowTitle string) error {
	windowTitle = strings.TrimSpace(windowTitle)
	if windowTitle == "" {
		return fmt.Errorf("empty window title")
	}

	var errs []string

	if err := activateWindowTitleWithWmctrl(windowTitle); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	if err := activateWindowTitleWithXdotool(windowTitle); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	return fmt.Errorf("exact title focus failed: %s", strings.Join(errs, "; "))
}

func activateWindowTitleWithWmctrl(windowTitle string) error {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return fmt.Errorf("wmctrl not installed")
	}

	cmd := exec.Command("wmctrl", "-F", "-a", windowTitle)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmctrl -F -a failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func activateWindowTitleWithXdotool(windowTitle string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	exactRegex := "^" + regexp.QuoteMeta(windowTitle) + "$"
	windowIDs, err := runXdotoolSearch("search", "--name", exactRegex)
	if err != nil {
		return err
	}
	if len(windowIDs) == 0 {
		return fmt.Errorf("no windows found for exact title")
	}

	for i := len(windowIDs) - 1; i >= 0; i-- {
		windowID := windowIDs[i]
		if err := activateWindowIDWithXdotool(windowID); err == nil {
			return nil
		}
	}

	return fmt.Errorf("xdotool could not activate any exact-title match")
}

// TryActivateWindowByTitle uses the activate-window-by-title GNOME extension.
// https://extensions.gnome.org/extension/5021/activate-window-by-title/
// This method does NOT require unsafe_mode and works on GNOME 42+.
//
// It tries activateByWmClass first (reliable for Wayland-native terminals whose
// WM class is a reverse-domain app ID, e.g. com.mitchellh.ghostty), then falls
// back to activateBySubstring on the window title.
func TryActivateWindowByTitle(terminalName, folderName string) error {
	// Try activateByWmClass first — more reliable than title substring search
	// for Wayland-native terminals that use reverse-domain app IDs as WM class.
	wmClass := GetGnomeWmClass(terminalName)
	if wmClass != "" {
		cmd := exec.Command("busctl", "--user", "call",
			"org.gnome.Shell",
			"/de/lucaswerkmeister/ActivateWindowByTitle",
			"de.lucaswerkmeister.ActivateWindowByTitle",
			"activateByWmClass", "s", wmClass,
		)
		output, err := cmd.CombinedOutput()
		if err == nil {
			outputStr := strings.TrimSpace(string(output))
			if strings.Contains(outputStr, "true") {
				return nil
			}
		}
	}

	// Fall back to substring title search.
	searchTerm := GetSearchTermWithFolder(terminalName, folderName)
	cmd := exec.Command("busctl", "--user", "call",
		"org.gnome.Shell",
		"/de/lucaswerkmeister/ActivateWindowByTitle",
		"de.lucaswerkmeister.ActivateWindowByTitle",
		"activateBySubstring", "s", searchTerm,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("activate-window-by-title extension not available: %w, output: %s", err, string(output))
	}
	// busctl can succeed (exit code 0) even when no window was activated.
	// The extension returns a boolean; ensure we only treat "true" as success.
	outputStr := strings.TrimSpace(string(output))
	if strings.Contains(outputStr, "false") || outputStr == "" {
		return fmt.Errorf("activate-window-by-title: no window activated for %q (output: %s)", searchTerm, outputStr)
	}
	return nil
}

// TryGnomeShellEvalByTitle uses GNOME Shell's Eval to find and focus window by title.
// Requires unsafe_mode or development-tools enabled.
func TryGnomeShellEvalByTitle(terminalName, folderName string) error {
	searchTerm := escapeJS(GetSearchTermWithFolder(terminalName, folderName))

	// JavaScript to find window by title and activate it
	js := fmt.Sprintf(`
		(function() {
			let start = Date.now();
			let found = false;
			global.get_window_actors().forEach(function(actor) {
				let win = actor.get_meta_window();
				let title = win.get_title() || '';
				if (title.indexOf('%s') !== -1) {
					win.activate(start);
					found = true;
				}
			});
			return found ? 'activated' : 'no matching window';
		})()
	`, searchTerm)

	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.Eval",
		js,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdbus Eval failed: %w, output: %s", err, string(output))
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "no matching window") {
		return fmt.Errorf("no window with title containing %q", searchTerm)
	}
	if strings.Contains(outputStr, "false") && !strings.Contains(outputStr, "activated") {
		return fmt.Errorf("Shell.Eval blocked (GNOME 41+ security) - install unsafe-mode-menu extension or activate-window-by-title extension")
	}

	return nil
}

// TryGnomeShellEval uses GNOME Shell's Eval method to activate an app.
// Requires unsafe_mode or development-tools enabled.
func TryGnomeShellEval(terminalName, folderName string) error {
	appID := escapeJS(GetAppID(terminalName))

	// JavaScript to find and activate the app's windows
	js := fmt.Sprintf(`
		(function() {
			let app = Shell.AppSystem.get_default().lookup_app('%s');
			if (app) {
				app.activate();
				return 'activated';
			}
			return 'app not found';
		})()
	`, appID)

	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.Eval",
		js,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdbus Eval failed: %w, output: %s", err, string(output))
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "app not found") {
		return fmt.Errorf("app not found via Shell.Eval")
	}
	if strings.Contains(outputStr, "false") && !strings.Contains(outputStr, "activated") {
		return fmt.Errorf("Shell.Eval blocked (GNOME 41+ security) - install unsafe-mode-menu extension or activate-window-by-title extension")
	}

	return nil
}

// TryGnomeFocusApp uses GNOME Shell's FocusApp method (available since GNOME 45).
func TryGnomeFocusApp(terminalName, folderName string) error {
	appID := GetAppID(terminalName)

	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.FocusApp",
		appID,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdbus FocusApp failed: %w, output: %s", err, string(output))
	}
	outputStr := strings.TrimSpace(string(output))
	if strings.Contains(outputStr, "false") || outputStr == "" {
		return fmt.Errorf("gdbus FocusApp reported no activation for %q (output: %s)", appID, outputStr)
	}
	return nil
}

// TryWlrctl uses wlrctl for wlroots-based compositors (Sway, etc.).
func TryWlrctl(terminalName, folderName string) error {
	if _, err := exec.LookPath("wlrctl"); err != nil {
		return fmt.Errorf("wlrctl not installed")
	}

	// Try app_id first (more reliable)
	appID := GetWlrctlAppID(terminalName)
	cmd := exec.Command("wlrctl", "toplevel", "focus", "app_id:"+appID)
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Fallback to title
	searchTerm := GetSearchTermWithFolder(terminalName, folderName)
	cmd = exec.Command("wlrctl", "toplevel", "focus", "title:"+searchTerm)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wlrctl failed: %w, output: %s", err, string(output))
	}
	return nil
}

// TryKdotool uses kdotool for KDE Plasma.
func TryKdotool(terminalName, folderName string) error {
	if _, err := exec.LookPath("kdotool"); err != nil {
		return fmt.Errorf("kdotool not installed")
	}

	// Search by class
	className := GetKdotoolClass(terminalName)
	searchCmd := exec.Command("kdotool", "search", "--class", className)
	output, err := searchCmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err != nil || outputStr == "" {
		return fmt.Errorf("no windows found via kdotool")
	}

	windowIDs := strings.Split(outputStr, "\n")

	cmd := exec.Command("kdotool", "windowactivate", windowIDs[0])
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kdotool windowactivate failed: %w", err)
	}
	return nil
}

// TryXdotool uses xdotool for X11-based desktop environments
// (XFCE, MATE, Cinnamon, i3, bspwm, and X11 sessions of GNOME/KDE).
func TryXdotool(terminalName, folderName string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	searches := buildXdotoolSearches(terminalName, folderName)
	seenIDs := make(map[string]struct{})
	foundMatch := false
	var errs []string

	for _, search := range searches {
		windowIDs, err := runXdotoolSearch(search.args...)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", search.label, err))
			continue
		}
		if len(windowIDs) == 0 {
			continue
		}

		foundMatch = true
		windowIDs = prioritizeXdotoolCandidates(windowIDs, search.label, folderName)

		// xdotool returns bottom-most windows first; prefer the top-most candidate.
		for i := len(windowIDs) - 1; i >= 0; i-- {
			windowID := windowIDs[i]
			if _, exists := seenIDs[windowID]; exists {
				continue
			}
			seenIDs[windowID] = struct{}{}

			if err := activateWindowIDWithXdotool(windowID); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("%s (%s): %v", search.label, windowID, err))
			}
		}
	}

	if !foundMatch {
		return fmt.Errorf("no windows found via xdotool")
	}

	return fmt.Errorf("xdotool could not activate any matching window: %s", strings.Join(errs, "; "))
}

type xdotoolSearch struct {
	label string
	args  []string
}

func buildXdotoolSearches(terminalName, folderName string) []xdotoolSearch {
	className := GetXdotoolClass(terminalName)
	searchTerm := GetSearchTermWithFolder(terminalName, folderName)

	searches := []xdotoolSearch{
		{
			label: "visible class search",
			args:  []string{"search", "--onlyvisible", "--class", className},
		},
		{
			label: "class search",
			args:  []string{"search", "--class", className},
		},
	}

	if searchTerm != "" {
		searches = append(searches,
			xdotoolSearch{
				label: "visible name search",
				args:  []string{"search", "--onlyvisible", "--name", searchTerm},
			},
			xdotoolSearch{
				label: "name search",
				args:  []string{"search", "--name", searchTerm},
			},
		)
	}

	return searches
}

func runXdotoolSearch(args ...string) ([]string, error) {
	cmd := exec.Command("xdotool", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w, output: %s", err, strings.TrimSpace(string(output)))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, nil
	}

	return splitWindowIDs(outputStr), nil
}

func splitWindowIDs(output string) []string {
	lines := strings.Split(output, "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}

func prioritizeXdotoolCandidates(windowIDs []string, searchLabel, folderName string) []string {
	if folderName == "" {
		return windowIDs
	}
	if !strings.Contains(searchLabel, "class search") {
		return windowIDs
	}

	matching := make([]string, 0, len(windowIDs))
	nonMatching := make([]string, 0, len(windowIDs))
	for _, windowID := range windowIDs {
		title := getXdotoolWindowName(windowID)
		if title != "" && strings.Contains(title, folderName) {
			matching = append(matching, windowID)
			continue
		}
		nonMatching = append(nonMatching, windowID)
	}

	if len(matching) == 0 {
		return windowIDs
	}

	return append(matching, nonMatching...)
}

func getXdotoolWindowName(windowID string) string {
	cmd := exec.Command("xdotool", "getwindowname", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// DetectFocusTools returns a map of available focus tools.
func DetectFocusTools() map[string]bool {
	tools := map[string]bool{}

	// Check command-line tools
	for _, tool := range []string{"wlrctl", "kdotool", "xdotool", "wmctrl", "gdbus", "busctl"} {
		_, err := exec.LookPath(tool)
		tools[tool] = err == nil
	}

	// Check GNOME activate-window-by-title extension
	cmd := exec.Command("busctl", "--user", "introspect",
		"org.gnome.Shell",
		"/de/lucaswerkmeister/ActivateWindowByTitle",
	)
	output, err := cmd.CombinedOutput()
	tools["activate-window-by-title"] = err == nil && strings.Contains(string(output), "activateBySubstring")

	return tools
}
