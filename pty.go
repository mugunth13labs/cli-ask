package main

import "io"

// PTYSession abstracts the pseudo-terminal session across Linux, macOS, and Windows.
type PTYSession interface {
	io.ReadWriteCloser
	Resize(rows, cols int) error
	Wait() error
	Kill() error
}
