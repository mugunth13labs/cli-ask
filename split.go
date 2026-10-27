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

// tryLaunchSplitSession attempts to open a side-by-side terminal split
// using available multiplexers (tmux, zellij, wt.exe).
// Returns true if a multiplexer split was successfully created.
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

	// 1. Inside existing tmux session
	if os.Getenv("TMUX") != "" {
		// Split window horizontally (left/right) with new pane placed to the left (-b) at 50% width (-p 50)
		cmd := exec.Command("tmux", "split-window", "-h", "-b", "-p", "50", askCmd)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("tmux split failed: %w", err)
		}

		// Bind Ctrl+T to toggle between panes in tmux
		_ = exec.Command("tmux", "bind-key", "-n", "C-t", "select-pane", "-t", ":.+").Run()
		_ = exec.Command("tmux", "select-pane", "-L").Run()

		fmt.Println("[Split pane opened on left. Press Ctrl+T to toggle between terminal and ask | Type /exit to close]")
		return true, nil
	}

	// 2. Inside existing zellij session
	if os.Getenv("ZELLIJ") != "" {
		cmd := exec.Command("zellij", "action", "new-pane", "-d", "left", "--", exe, "-i")
		if parsed.Prompt != "" {
			cmd.Args = append(cmd.Args, parsed.Prompt)
		}
		cmd.Env = append(os.Environ(), "ASK_IN_SPLIT=1")
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("zellij split failed: %w", err)
		}
		return true, nil
	}

	// 3. Inside Windows Terminal (wt.exe)
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

	// 4. Outside tmux, but tmux is installed on Linux/macOS
	if tmuxPath, err := exec.LookPath("tmux"); err == nil {
		sessionName := fmt.Sprintf("ask-%d", os.Getpid())

		// Create detached session running ask
		if err := exec.Command(tmuxPath, "new-session", "-d", "-s", sessionName, askCmd).Run(); err == nil {
			// Split window horizontally with shell on the right
			_ = exec.Command(tmuxPath, "split-window", "-t", sessionName, "-h", "-p", "50").Run()
			// Bind Ctrl+T to toggle between panes
			_ = exec.Command(tmuxPath, "bind-key", "-n", "C-t", "select-pane", "-t", ":.+").Run()
			// Select left pane (ask)
			_ = exec.Command(tmuxPath, "select-pane", "-t", sessionName+":0.0").Run()

			// Attach to session
			attachCmd := exec.Command(tmuxPath, "attach-session", "-t", sessionName)
			attachCmd.Stdin = os.Stdin
			attachCmd.Stdout = os.Stdout
			attachCmd.Stderr = os.Stderr
			if err := attachCmd.Run(); err == nil {
				return true, nil
			}
		}
	}

	// Fallback when no multiplexer is found
	fmt.Fprintf(os.Stderr, "Notice: Split mode requires a terminal multiplexer (tmux, zellij, or Windows Terminal).\nFalling back to standard immersive mode...\n\n")
	return false, nil
}
