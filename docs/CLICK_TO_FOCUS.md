# Click-to-Focus

Clicking a notification activates your terminal window — no more hunting for the right window.

## Configuration

In `~/.claude/claude-notifications-go/config.json`:

```json
{
  "notifications": {
    "desktop": {
      "clickToFocus": true,
      "terminalBundleId": ""
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `clickToFocus` | `true` | Enable click-to-focus on macOS and Linux |
| `terminalBundleId` | `""` | macOS only: override auto-detected terminal. Use bundle ID like `com.googlecode.iterm2` |

## macOS

Auto-detects your terminal via `TERM_PROGRAM` / `__CFBundleIdentifier`. Uses `terminal-notifier` (auto-installed via `/claude-notifications-go:init`).

| Terminal | Focus method |
|----------|-------------|
| Ghostty | Exact tab focus via Ghostty AppleScript, with AXDocument retry fallback |
| VS Code / Insiders / Cursor | AXTitle via focus-window subcommand |
| iTerm2 | Exact tab/pane targeting via iTerm2 Python API when available, otherwise app-level iTerm activation |
| Warp, kitty, WezTerm, Alacritty, Hyper, Apple Terminal | AXTitle via focus-window subcommand |
| Any other (custom `terminalBundleId`) | AXTitle via focus-window subcommand |

To find your terminal's bundle ID: `osascript -e 'id of app "YourTerminal"'`

### Permissions

All terminals with click-to-focus may require up to two permissions for window-level focus:

- **Accessibility** — to enumerate and raise the correct window via the AX API
- **Screen Recording** — to read window titles across Spaces (macOS 10.15+)

Screen Recording is requested automatically via system prompt on first use.
Accessibility is prompted via a one-time notification with a link to System Settings.

Without these permissions, clicking a notification still activates the terminal app,
but raises whichever window was last active rather than the project-specific one.

## Linux

Uses a background D-Bus daemon. Auto-detects terminal and compositor.

| Terminal | Supported compositors |
|----------|----------------------|
| VS Code | GNOME, KDE, Sway, X11 |
| GNOME Terminal, Konsole, Alacritty, kitty, WezTerm, Tilix, Terminator, XFCE4 Terminal, MATE Terminal | GNOME, KDE, Sway, X11 |
| Any other | Fallback by name |

Focus methods (tried in order):

1. **GNOME**: `activate-window-by-title` extension, Shell Eval, FocusApp (GNOME 45+)
2. **Sway / wlroots**: `wlrctl`
3. **KDE Plasma**: `kdotool`
4. **X11** (XFCE, MATE, Cinnamon, i3, bspwm): `xdotool`

Falls back to standard notifications if no focus tool is available.

### Diagnostics

If Linux click-to-focus focuses the wrong window, run the diagnostic script immediately after reproducing the failed click:

```bash
curl -fsSL https://raw.githubusercontent.com/777genius/claude-notifications-go/main/scripts/linux-focus-debug.sh | bash
```

It writes a report file in the current directory with:

- session type and terminal environment variables
- available focus tools (`xdotool`, `wmctrl`, `remotinator`, etc.)
- current window information and window lists
- installed plugin metadata and recent `notification-debug.log` lines

Review the file before sharing it publicly, because it may include local paths and window titles.

## Multiplexers

On both macOS and Linux, click-to-focus supports **tmux**, **zellij**, **WezTerm**, and **kitty** — clicking a notification switches to the correct session/pane/tab.

### iTerm2 + tmux Control Mode (-CC)

When using iTerm2's tmux integration (`tmux -CC`), standard `tmux select-window` doesn't switch iTerm2 tabs. The plugin detects control mode automatically and uses the iTerm2 Python API instead.

**Requirements:**
1. Python 3 installed
2. iTerm2 → Settings → General → Magic → **Enable Python API**
3. iterm2 venv (set up automatically by `bootstrap.sh` / `install.sh`)

**Manual setup** (if automatic setup failed):
```bash
python3 -m venv ~/.claude/claude-notifications-go/iterm2-venv
~/.claude/claude-notifications-go/iterm2-venv/bin/pip install iterm2
```

**Diagnostics:**
```bash
# Show the plugin root path (run inside Claude Code hook context)
echo "$CLAUDE_PLUGIN_ROOT"

# List all iTerm2 tabs with tmux pane mappings
~/.claude/claude-notifications-go/iterm2-venv/bin/python3 \
  "$CLAUDE_PLUGIN_ROOT/scripts/iterm2-select-tab.py" --list
```

If the Python API is not available, the plugin falls back to standard `tmux select-window` (which may not switch iTerm2 tabs in -CC mode). If you just toggled the setting, restart iTerm2 once. For plain iTerm2, the fallback is app-level activation instead of exact tab targeting.

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
