//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyWinch(c chan os.Signal) {
	signal.Notify(c, syscall.SIGWINCH)
}
