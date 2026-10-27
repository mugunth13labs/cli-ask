package main

import (
	"strings"
	"testing"
	"time"
)

func TestStartPTY(t *testing.T) {
	pty, err := StartPTY(24, 80)
	if err != nil {
		t.Fatalf("StartPTY failed: %v", err)
	}
	defer pty.Close()

	// Wait 500ms for shell to initialize and show prompt
	time.Sleep(500 * time.Millisecond)

	// Send command to shell
	_, err = pty.Write([]byte("echo PTY_TEST_OK\r\n"))
	if err != nil {
		t.Fatalf("Write to PTY failed: %v", err)
	}

	readChan := make(chan string, 100)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := pty.Read(buf)
			if err != nil {
				t.Logf("pty.Read error: %v", err)
				return
			}
			if n > 0 {
				readChan <- string(buf[:n])
			}
		}
	}()

	var accumulated strings.Builder
	timeout := time.After(4 * time.Second)

	for {
		select {
		case chunk := <-readChan:
			accumulated.WriteString(chunk)
			if strings.Contains(accumulated.String(), "PTY_TEST_OK") {
				t.Logf("Success: PTY responded with command output!")
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for PTY output; accumulated: %q", accumulated.String())
		}
	}
}
