//go:build !windows

package main

func copyNativeClipboard(text string) {
	// Over SSH and Linux terminals, OSC 52 copies directly to the user's host OS clipboard
}
