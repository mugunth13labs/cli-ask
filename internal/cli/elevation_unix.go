//go:build !windows

package cli

import "os"

// isProcessElevated checks if the current process on Unix/Linux/macOS is running as root (UID 0).
func isProcessElevated() bool {
	return os.Geteuid() == 0
}
