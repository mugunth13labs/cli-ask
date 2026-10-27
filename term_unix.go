//go:build !windows

package main

import (
	"syscall"
	"unsafe"
)

type TermState struct {
	termios syscall.Termios
}

func makeRaw(fd uintptr) (*TermState, error) {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(tcgets), uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	if err != 0 {
		return nil, err
	}

	state := &TermState{termios: termios}
	raw := termios
	// Disable ICANON (canonical line buffer) and ECHO
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	_, _, err = syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(tcsets), uintptr(unsafe.Pointer(&raw)), 0, 0, 0)
	if err != 0 {
		return nil, err
	}
	return state, nil
}

func restoreTerm(fd uintptr, state *TermState) error {
	if state == nil {
		return nil
	}
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(tcsets), uintptr(unsafe.Pointer(&state.termios)), 0, 0, 0)
	if err != 0 {
		return err
	}
	return nil
}
