//go:build darwin

package main

const (
	tcgets     = 0x40487413
	tcsets     = 0x80487414
	tiocgwinsz = 0x40087468
)
