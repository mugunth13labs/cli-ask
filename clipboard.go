package main

import (
	"encoding/base64"
	"fmt"
	"os"
)

// copyToClipboard copies text to the user's system clipboard using OSC 52,
// which works over SSH, in Windows Terminal, tmux, iTerm2, Kitty, Alacritty, etc.
func copyToClipboard(text string) {
	if text == "" {
		return
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
	_, _ = os.Stdout.WriteString(osc)
	_ = os.Stdout.Sync()

	// Platform specific native clipboard fallback
	copyNativeClipboard(text)
}

// readClipboard retrieves text from the system clipboard
func readClipboard() string {
	return readNativeClipboard()
}
