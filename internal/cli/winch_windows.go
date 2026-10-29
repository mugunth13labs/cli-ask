//go:build windows

package cli

import (
	"os"
)

func notifyWinch(c chan os.Signal) {
	// Windows console resize is polled or handled via ReadConsoleInput
}
