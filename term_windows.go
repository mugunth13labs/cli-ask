//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableLineInput       = 0x0002
	enableEchoInput       = 0x0004
	enableProcessedInput  = 0x0001
	enableVirtualTerminal = 0x0200
)

type TermState struct {
	mode uint32
}

type coord struct {
	X int16
	Y int16
}
type smallRect struct {
	Left   int16
	Top    int16
	Right  int16
	Bottom int16
}
type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

func makeRaw(fd uintptr) (*TermState, error) {
	var mode uint32
	r1, _, err := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(&mode)))
	if r1 == 0 {
		return nil, err
	}
	state := &TermState{mode: mode}
	// Disable line input and echo, enable virtual terminal processing
	rawMode := (mode &^ (enableLineInput | enableEchoInput)) | enableVirtualTerminal | enableProcessedInput
	r1, _, err = procSetConsoleMode.Call(fd, uintptr(rawMode))
	if r1 == 0 {
		return nil, err
	}
	return state, nil
}

func restoreTerm(fd uintptr, state *TermState) error {
	if state == nil {
		return nil
	}
	r1, _, err := procSetConsoleMode.Call(fd, uintptr(state.mode))
	if r1 == 0 {
		return err
	}
	return nil
}

func getTerminalSize(fd uintptr) (width, height int) {
	var csbi consoleScreenBufferInfo
	// Note: Console Screen Buffer Info should be queried from stdout
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(fd, uintptr(unsafe.Pointer(&csbi)))
	if r1 != 0 {
		w := int(csbi.Window.Right - csbi.Window.Left + 1)
		h := int(csbi.Window.Bottom - csbi.Window.Top + 1)
		if w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 24
}
