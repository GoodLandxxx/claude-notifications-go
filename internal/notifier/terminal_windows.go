//go:build windows

package notifier

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"git.sr.ht/~jackmordaunt/go-toast"
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

	// go-toast's Windows Runtime COM API fails to parse XML containing emoji
	// characters (supplementary plane runes). Strip them while preserving CJK.
	cleanTitle := stripEmoji(title)
	cleanBody := stripEmoji(body)

	activationArgs := fmt.Sprintf("%s://focus?cwd=%s", protocolScheme, url.QueryEscape(cwd))

	n := toast.Notification{
		AppID:               "Claude Code Notifications",
		Title:               cleanTitle,
		Body:                cleanBody,
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

// stripEmoji removes supplementary-plane characters (mostly emoji) from a
// string while preserving Basic Multilingual Plane text including CJK.
// go-toast's underlying Windows Runtime XML parser fails on emoji in CDATA.
func stripEmoji(s string) string {
	var out []rune
	for _, r := range s {
		if r > 0xFFFF {
			continue
		}
		out = append(out, r)
	}
	return string(out)
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

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable: %w", err)
	}
	exe, _ = filepath.Abs(exe)

	cmdValue := fmt.Sprintf(`"%s" focus-windows "%%1"`, exe)
	if err := cmdKey.SetStringValue("", cmdValue); err != nil {
		return fmt.Errorf("set command value: %w", err)
	}

	logging.Debug("Registered protocol handler: %s -> %s", protocolScheme, exe)
	return nil
}
