//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

type DarwinPTY struct {
	master *os.File
	cmd    *exec.Cmd
}

const (
	ioctlTIOCPTYGNAME = 0x40807464 // Darwin ioctl to get slave path
	ioctlTIOCSWINSZ   = 0x80087467 // Darwin ioctl to set window size
)

func StartPTY(rows, cols int) (PTYSession, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/bash"
		}
	}
	return StartPTYCommand(rows, cols, shell)
}

func StartPTYCommand(rows, cols int, command string, args ...string) (PTYSession, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	var buf [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(ioctlTIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		master.Close()
		return nil, fmt.Errorf("ioctl TIOCPTYGNAME: %v", errno)
	}

	end := 0
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	ptsPath := string(buf[:end])

	slave, err := os.OpenFile(ptsPath, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open %s: %w", ptsPath, err)
	}
	defer slave.Close()

	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), uintptr(ioctlTIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))

	cmd := exec.Command(command, args...)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		fmt.Sprintf("LINES=%d", rows),
		fmt.Sprintf("COLUMNS=%d", cols),
	)

	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, fmt.Errorf("start %s: %w", command, err)
	}

	return &DarwinPTY{
		master: master,
		cmd:    cmd,
	}, nil
}

func (p *DarwinPTY) Read(b []byte) (int, error) {
	return p.master.Read(b)
}

func (p *DarwinPTY) Write(b []byte) (int, error) {
	return p.master.Write(b)
}

func (p *DarwinPTY) Close() error {
	_ = p.Kill()
	return p.master.Close()
}

func (p *DarwinPTY) Resize(rows, cols int) error {
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, p.master.Fd(), uintptr(ioctlTIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return errno
	}
	return nil
}

func (p *DarwinPTY) Wait() error {
	return p.cmd.Wait()
}

func (p *DarwinPTY) Kill() error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}
