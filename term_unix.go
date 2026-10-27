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

	// Disable ICRNL (mapping CR to NL on input), INLCR, IGNCR, IXON (software flow control)
	// This is critical: ICRNL was causing regular Enter (\r) to be translated to \n (10),
	// which falsely triggered Ctrl+Enter submission on Linux!
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON

	// Disable canonical mode (ICANON), echo (ECHO, ECHONL), signal generation (ISIG), and extended input processing (IEXTEN)
	// With ISIG disabled, Ctrl+C sends byte 3, Ctrl+D sends byte 4, and Ctrl+Z sends byte 26 directly
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN

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
