//go:build windows

package cli

import "syscall"

// isProcessElevated checks if the current process on Windows has Administrator privileges.
func isProcessElevated() bool {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("IsUserAnAdmin")
	ret, _, _ := proc.Call()
	return ret != 0
}
