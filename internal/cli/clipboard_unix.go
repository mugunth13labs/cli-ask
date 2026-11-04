//go:build !windows

package cli

import (
	"bytes"
	"os/exec"
)

func copyNativeClipboard(text string) {
	// Over SSH and Linux terminals, OSC 52 copies directly to the user's host OS clipboard
}

func readNativeClipboard() string {
	// Try wl-paste (Wayland)
	if path, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command(path, "--no-newline")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.String()
		}
	}
	// Try xclip (X11)
	if path, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command(path, "-selection", "clipboard", "-o")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.String()
		}
	}
	// Try pbpaste (macOS)
	if path, err := exec.LookPath("pbpaste"); err == nil {
		cmd := exec.Command(path)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.String()
		}
	}
	return ""
}
