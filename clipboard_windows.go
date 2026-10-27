//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

var (
	modKernel32          = syscall.NewLazyDLL("kernel32.dll")
	modUser32            = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard    = modUser32.NewProc("OpenClipboard")
	procCloseClipboard   = modUser32.NewProc("CloseClipboard")
	procEmptyClipboard   = modUser32.NewProc("EmptyClipboard")
	procSetClipboardData = modUser32.NewProc("SetClipboardData")
	procGlobalAlloc      = modKernel32.NewProc("GlobalAlloc")
	procGlobalLock       = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock     = modKernel32.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

func copyNativeClipboard(text string) {
	// Standard Windows clipboard CF_UNICODETEXT requires CRLF (\r\n) line breaks
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\n", "\r\n")

	utf16, err := syscall.UTF16FromString(normalized)
	if err != nil {
		return
	}
	r1, _, _ := procOpenClipboard.Call(0)
	if r1 == 0 {
		return
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	bytes := len(utf16) * 2
	hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(bytes))
	if hMem == 0 {
		return
	}

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return
	}

	copy((*[1 << 30]byte)(unsafe.Pointer(ptr))[:bytes], (*[1 << 30]byte)(unsafe.Pointer(&utf16[0]))[:bytes])
	procGlobalUnlock.Call(hMem)

	procSetClipboardData.Call(cfUnicodeText, hMem)
}
