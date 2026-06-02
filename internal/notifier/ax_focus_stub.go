//go:build !darwin && !windows

package notifier

import "fmt"

// FocusAppWindow is not supported on non-darwin platforms.
func FocusAppWindow(bundleID, cwd string) error {
	return fmt.Errorf("focus-window not supported on this platform")
}

// FocusAppWindowWithOptions is not supported on non-darwin platforms.
func FocusAppWindowWithOptions(bundleID, cwd string, opts FocusWindowOptions) error {
	return fmt.Errorf("focus-window not supported on this platform")
}

type FocusWindowOptions struct {
	GhosttyTerminalID string
}
