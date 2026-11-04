//go:build !windows

package cli

import (
	"syscall"
	"unsafe"
)

type TermState struct {
	termios syscall.Termios
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
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

func getTerminalSize(fd uintptr) (width, height int) {
	var ws winsize
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(tiocgwinsz), uintptr(unsafe.Pointer(&ws)))
	if err == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	return 80, 24
}
