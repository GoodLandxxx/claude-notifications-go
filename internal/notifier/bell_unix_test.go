//go:build !windows

package notifier

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSendTmuxPaneBell_NoTmuxEnv_NoOp(t *testing.T) {
	// Should silently no-op when TMUX env is not set, without invoking tmux.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

	called := false
	originalExecCommand := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command("true")
	}
	defer func() { execCommand = originalExecCommand }()

	sendTmuxPaneBell()

	if called {
		t.Error("tmux should not be invoked when TMUX env is empty")
	}
}

func TestSendTmuxPaneBell_NoPaneEnv_NoOp(t *testing.T) {
	// Should silently no-op when TMUX_PANE is missing even if TMUX is set.
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "")

	called := false
	originalExecCommand := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command("true")
	}
	defer func() { execCommand = originalExecCommand }()

	sendTmuxPaneBell()

	if called {
		t.Error("tmux should not be invoked when TMUX_PANE is empty")
	}
}

func TestSendTmuxPaneBell_WritesBELToPaneTTY(t *testing.T) {
	// Create a temp file that stands in for the pane tty. Writing BEL into it
	// should succeed and produce a single 0x07 byte.
	tmpDir := t.TempDir()
	fakePaneTTY := filepath.Join(tmpDir, "fake-pane-tty")
	if err := os.WriteFile(fakePaneTTY, nil, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%42")

	var capturedArgs []string
	originalExecCommand := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string{name}, args...)
		cmdArgs := []string{"-test.run=TestHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT="+fakePaneTTY+"\n",
			"GO_HELPER_EXIT_CODE=0",
		)
		return cmd
	}
	defer func() { execCommand = originalExecCommand }()

	sendTmuxPaneBell()

	// Verify the underlying tmux call used the expected arguments.
	wantArgs := []string{"tmux", "display-message", "-p", "-t", "%42", "#{pane_tty}"}
	if !equalStringSlices(capturedArgs, wantArgs) {
		t.Errorf("tmux invocation mismatch:\n  got:  %v\n  want: %v", capturedArgs, wantArgs)
	}

	// Verify BEL was written to the pane tty.
	contents, err := os.ReadFile(fakePaneTTY)
	if err != nil {
		t.Fatalf("read fake pane tty: %v", err)
	}
	if string(contents) != "\a" {
		t.Errorf("expected BEL byte (0x07) in pane tty, got: %q (% x)", contents, contents)
	}
}

func TestSendTmuxPaneBell_TmuxFailureDoesNotPanic(t *testing.T) {
	// When tmux exits non-zero, the fallback should log and return cleanly.
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%42")

	originalExecCommand := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_EXIT_CODE=1",
		)
		return cmd
	}
	defer func() { execCommand = originalExecCommand }()

	sendTmuxPaneBell()
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
