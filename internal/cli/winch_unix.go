//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyWinch(c chan os.Signal) {
	signal.Notify(c, syscall.SIGWINCH)
}
