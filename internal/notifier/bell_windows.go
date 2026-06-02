//go:build windows

package notifier

import (
	"fmt"
	"os"

	"github.com/777genius/claude-notifications/internal/logging"
)

// platformSendTerminalBell writes a BEL character to stdout for Windows Terminal
// ConPTY tab indicators. Windows Terminal renders BEL as a taskbar flash.
func platformSendTerminalBell() {
	_, err := fmt.Fprint(os.Stdout, "\a")
	if err != nil {
		logging.Debug("Could not write BEL to stdout: %v", err)
	}
}
