//go:build windows

package main

import (
	"io"
	"os/exec"
)

type WindowsPTY struct {
	stdinPipe  io.WriteCloser
	stdoutPipe io.ReadCloser
	cmd        *exec.Cmd
}

func StartPTY(rows, cols int) (PTYSession, error) {
	shellPath, err := exec.LookPath("powershell.exe")
	if err != nil {
		shellPath, _ = exec.LookPath("cmd.exe")
	}
	return StartPTYCommand(rows, cols, shellPath)
}

func StartPTYCommand(rows, cols int, command string, args ...string) (PTYSession, error) {
	cmd := exec.Command(command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &WindowsPTY{
		stdinPipe:  stdin,
		stdoutPipe: stdout,
		cmd:        cmd,
	}, nil
}

func (p *WindowsPTY) Read(b []byte) (int, error) {
	return p.stdoutPipe.Read(b)
}

func (p *WindowsPTY) Write(b []byte) (int, error) {
	return p.stdinPipe.Write(b)
}

func (p *WindowsPTY) Resize(rows, cols int) error {
	return nil
}

func (p *WindowsPTY) Close() error {
	_ = p.Kill()
	if p.stdinPipe != nil {
		p.stdinPipe.Close()
	}
	if p.stdoutPipe != nil {
		p.stdoutPipe.Close()
	}
	return nil
}

func (p *WindowsPTY) Wait() error {
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
}

func (p *WindowsPTY) Kill() error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}
