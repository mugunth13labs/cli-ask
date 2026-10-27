//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

type LinuxPTY struct {
	master *os.File
	cmd    *exec.Cmd
}

const (
	ioctlTIOCGPTN   = 0x80045430 // ioctl to get pts number
	ioctlTIOCSPTLCK = 0x40045431 // ioctl to unlock slave pts
	ioctlTIOCSWINSZ = 0x5414     // ioctl to set window size
)

func StartPTY(rows, cols int) (PTYSession, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/sh"
		}
	}
	return StartPTYCommand(rows, cols, shell, "-i")
}

func StartPTYCommand(rows, cols int, command string, args ...string) (PTYSession, error) {
	// 1. Open master /dev/ptmx
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	// 2. Retrieve pts number
	var ptn uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(ioctlTIOCGPTN), uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		master.Close()
		return nil, fmt.Errorf("ioctl TIOCGPTN: %v", errno)
	}

	// 3. Unlock slave pts
	var unlock int32 = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(ioctlTIOCSPTLCK), uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, fmt.Errorf("ioctl TIOCSPTLCK: %v", errno)
	}

	// 4. Open slave device
	ptsPath := fmt.Sprintf("/dev/pts/%d", ptn)
	slave, err := os.OpenFile(ptsPath, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open %s: %w", ptsPath, err)
	}
	defer slave.Close()

	// 5. Set terminal window dimensions
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), uintptr(ioctlTIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))

	// 6. Launch process
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

	return &LinuxPTY{
		master: master,
		cmd:    cmd,
	}, nil
}

func (p *LinuxPTY) Read(b []byte) (int, error) {
	return p.master.Read(b)
}

func (p *LinuxPTY) Write(b []byte) (int, error) {
	return p.master.Write(b)
}

func (p *LinuxPTY) Close() error {
	_ = p.Kill()
	return p.master.Close()
}

func (p *LinuxPTY) Resize(rows, cols int) error {
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, p.master.Fd(), uintptr(ioctlTIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return errno
	}
	return nil
}

func (p *LinuxPTY) Wait() error {
	return p.cmd.Wait()
}

func (p *LinuxPTY) Kill() error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}
