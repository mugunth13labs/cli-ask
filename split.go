package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\"'$()&;|<>`") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}

// tryLaunchSplitSession attempts to open a native side-by-side terminal split
// using the user's active GUI terminal emulator or existing multiplexer.
// Returns true if a split was successfully created.
func tryLaunchSplitSession(parsed *ParsedArgs) (bool, error) {
	// Guard against recursive splitting
	if os.Getenv("ASK_IN_SPLIT") == "1" {
		return false, nil
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "ask"
	} else if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	askCmd := fmt.Sprintf("ASK_IN_SPLIT=1 %s -i", shellQuote(exe))
	if parsed.Prompt != "" {
		askCmd += fmt.Sprintf(" %s", shellQuote(parsed.Prompt))
	}

	// 1. Inside Windows Terminal (wt.exe)
	if os.Getenv("WT_SESSION") != "" {
		wtPath, err := exec.LookPath("wt.exe")
		if err == nil {
			// Windows Terminal: split pane vertically (-V), size 50% (-s 0.5)
			cmd := exec.Command(wtPath, "-w", "0", "sp", "-V", "-s", "0.5", exe, "-i")
			if parsed.Prompt != "" {
				cmd.Args = append(cmd.Args, parsed.Prompt)
			}
			cmd.Env = append(os.Environ(), "ASK_IN_SPLIT=1")
			if err := cmd.Start(); err == nil {
				return true, nil
			}
		}
	}

	// 2. Inside WezTerm (WEZTERM_PANE environment variable)
	if os.Getenv("WEZTERM_PANE") != "" {
		if weztermPath, err := exec.LookPath("wezterm"); err == nil {
			cmd := exec.Command(weztermPath, "cli", "split-pane", "--horizontal", "--percent", "50", "--", exe, "-i")
			if parsed.Prompt != "" {
				cmd.Args = append(cmd.Args, parsed.Prompt)
			}
			cmd.Env = append(os.Environ(), "ASK_IN_SPLIT=1")
			if err := cmd.Run(); err == nil {
				return true, nil
			}
		}
	}

	// 3. Inside Kitty (KITTY_WINDOW_ID environment variable)
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		if kittyPath, err := exec.LookPath("kitty"); err == nil {
			cmd := exec.Command(kittyPath, "@", "launch", "--location=vsplit", "--cwd=current", exe, "-i")
			if parsed.Prompt != "" {
				cmd.Args = append(cmd.Args, parsed.Prompt)
			}
			cmd.Env = append(os.Environ(), "ASK_IN_SPLIT=1")
			if err := cmd.Run(); err == nil {
				return true, nil
			}
		}
	}

	// 4. Inside Tilix (TILIX_ID environment variable)
	if os.Getenv("TILIX_ID") != "" {
		if tilixPath, err := exec.LookPath("tilix"); err == nil {
			args := []string{"-a", "session-add-right", "-e", askCmd}
			cmd := exec.Command(tilixPath, args...)
			cmd.Env = append(os.Environ(), "ASK_IN_SPLIT=1")
			if err := cmd.Run(); err == nil {
				return true, nil
			}
		}
	}

	// 5. Inside an active tmux session (only if user explicitly already runs inside tmux)
	if os.Getenv("TMUX") != "" {
		cmd := exec.Command("tmux", "split-window", "-h", "-b", "-p", "50", askCmd)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			_ = exec.Command("tmux", "bind-key", "-n", "C-t", "select-pane", "-t", ":.+").Run()
			_ = exec.Command("tmux", "select-pane", "-L").Run()
			return true, nil
		}
	}

	// 6. Inside an active zellij session (only if user explicitly already runs inside zellij)
	if os.Getenv("ZELLIJ") != "" {
		cmd := exec.Command("zellij", "action", "new-pane", "-d", "left", "--", exe, "-i")
		if parsed.Prompt != "" {
			cmd.Args = append(cmd.Args, parsed.Prompt)
		}
		cmd.Env = append(os.Environ(), "ASK_IN_SPLIT=1")
		if err := cmd.Run(); err == nil {
			return true, nil
		}
	}

	// Fallback when no supported native split terminal is detected
	// Note: We deliberately do NOT spawn external tmux sessions to avoid hijacking user terminals!
	fmt.Fprintf(os.Stderr, "Notice: Split mode requires a terminal with native split support (Windows Terminal, WezTerm, Kitty, Tilix) or an active tmux session.\nFalling back to standard immersive mode...\n\n")
	return false, nil
}
