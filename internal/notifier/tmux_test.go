package notifier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsTmuxControlMode_NotInTmux(t *testing.T) {
	old := os.Getenv("TMUX")
	os.Unsetenv("TMUX")
	t.Cleanup(func() {
		if old != "" {
			os.Setenv("TMUX", old)
		}
	})
	if IsTmuxControlMode() {
		t.Error("should be false when not in tmux")
	}
}

func TestGetTmuxPaneTarget_PrefersEnvVar(t *testing.T) {
	old := os.Getenv("TMUX_PANE")
	os.Setenv("TMUX_PANE", "%42")
	t.Cleanup(func() {
		if old != "" {
			os.Setenv("TMUX_PANE", old)
		} else {
			os.Unsetenv("TMUX_PANE")
		}
	})

	target, err := GetTmuxPaneTarget()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "%42" {
		t.Errorf("expected %%42, got %q", target)
	}
}

func TestGetTmuxPaneTarget_FallsBackWithoutEnvVar(t *testing.T) {
	oldPane := os.Getenv("TMUX_PANE")
	oldTmux := os.Getenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	os.Setenv("TMUX", filepath.Join(t.TempDir(), "missing.sock")+",12345,0")
	t.Cleanup(func() {
		if oldPane != "" {
			os.Setenv("TMUX_PANE", oldPane)
		} else {
			os.Unsetenv("TMUX_PANE")
		}
		if oldTmux != "" {
			os.Setenv("TMUX", oldTmux)
		} else {
			os.Unsetenv("TMUX")
		}
	})

	// Without $TMUX_PANE and without a real tmux socket, the fallback
	// should fail gracefully.
	_, err := GetTmuxPaneTarget()
	if err == nil {
		t.Error("expected error when TMUX_PANE is unset and no tmux server, got nil")
	}
}

func TestGetTmuxPaneTarget_IgnoresEmptyEnvVar(t *testing.T) {
	oldPane := os.Getenv("TMUX_PANE")
	oldTmux := os.Getenv("TMUX")
	os.Setenv("TMUX_PANE", "")
	os.Setenv("TMUX", filepath.Join(t.TempDir(), "missing.sock")+",12345,0")
	t.Cleanup(func() {
		if oldPane != "" {
			os.Setenv("TMUX_PANE", oldPane)
		} else {
			os.Unsetenv("TMUX_PANE")
		}
		if oldTmux != "" {
			os.Setenv("TMUX", oldTmux)
		} else {
			os.Unsetenv("TMUX")
		}
	})

	// Empty TMUX_PANE should fall through to the display-message fallback.
	_, err := GetTmuxPaneTarget()
	if err == nil {
		t.Error("expected error when TMUX_PANE is empty and no tmux server, got nil")
	}
}
