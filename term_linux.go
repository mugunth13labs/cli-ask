//go:build linux

package main

const (
	tcgets     = 0x5401
	tcsets     = 0x5402
	tiocgwinsz = 0x5413
)
