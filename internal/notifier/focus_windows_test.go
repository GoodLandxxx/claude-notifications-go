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
