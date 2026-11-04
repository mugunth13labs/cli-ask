//go:build windows

package cli

import (
	"os"
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
	enableProcessedInput            = 0x0001
	enableLineInput                 = 0x0002
	enableEchoInput                 = 0x0004
	enableVirtualTerminalInput      = 0x0200
	enableProcessedOutput           = 0x0001
	enableVirtualTerminalProcessing = 0x0004
)

type TermState struct {
	fd   uintptr
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

func enableVT(fd uintptr) {
	var mode uint32
	r1, _, _ := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(&mode)))
	if r1 != 0 {
		_, _, _ = procSetConsoleMode.Call(fd, uintptr(mode|enableProcessedOutput|enableVirtualTerminalProcessing))
	}
}

func makeRaw(fd uintptr) (*TermState, error) {
	// Console input raw mode must be set on an input buffer handle (stdin)
	targetFd := fd
	var mode uint32
	r1, _, err := procGetConsoleMode.Call(targetFd, uintptr(unsafe.Pointer(&mode)))
	if r1 == 0 {
		// If fd is not an input handle (e.g. stdout was passed), try stdin
		targetFd = os.Stdin.Fd()
		r1, _, err = procGetConsoleMode.Call(targetFd, uintptr(unsafe.Pointer(&mode)))
		if r1 == 0 {
			return nil, err
		}
	}

	state := &TermState{fd: targetFd, mode: mode}

	// Disable line input and echo, enable virtual terminal processing on input
	rawMode := (mode &^ (enableLineInput | enableEchoInput | enableProcessedInput)) | enableVirtualTerminalInput
	r1, _, err = procSetConsoleMode.Call(targetFd, uintptr(rawMode))
	if r1 == 0 {
		// If setting input mode failed, try targetFd = os.Stdin.Fd()
		if targetFd != os.Stdin.Fd() {
			targetFd = os.Stdin.Fd()
			r1, _, err = procGetConsoleMode.Call(targetFd, uintptr(unsafe.Pointer(&mode)))
			if r1 != 0 {
				state = &TermState{fd: targetFd, mode: mode}
				rawMode = (mode &^ (enableLineInput | enableEchoInput | enableProcessedInput)) | enableVirtualTerminalInput
				r1, _, err = procSetConsoleMode.Call(targetFd, uintptr(rawMode))
			}
		}
		if r1 == 0 {
			return nil, err
		}
	}

	// Ensure stdout has Virtual Terminal Processing enabled for ANSI escape sequences
	enableVT(os.Stdout.Fd())

	return state, nil
}

func restoreTerm(fd uintptr, state *TermState) error {
	if state == nil {
		return nil
	}
	targetFd := state.fd
	if targetFd == 0 {
		targetFd = fd
	}
	r1, _, err := procSetConsoleMode.Call(targetFd, uintptr(state.mode))
	if r1 == 0 {
		return err
	}
	return nil
}

func getTerminalSize(fd uintptr) (width, height int) {
	var csbi consoleScreenBufferInfo
	// Console Screen Buffer Info MUST be queried from stdout or stderr (screen buffer handles)
	targetFd := fd
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(targetFd, uintptr(unsafe.Pointer(&csbi)))
	if r1 == 0 {
		// If fd was stdin, query stdout instead!
		targetFd = os.Stdout.Fd()
		r1, _, _ = procGetConsoleScreenBufferInfo.Call(targetFd, uintptr(unsafe.Pointer(&csbi)))
	}
	if r1 != 0 {
		w := int(csbi.Window.Right - csbi.Window.Left + 1)
		h := int(csbi.Window.Bottom - csbi.Window.Top + 1)
		if w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 24
}
