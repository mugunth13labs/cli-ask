package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProbeSystemContext(t *testing.T) {
	ctx := ProbeSystemContext()
	if ctx == nil {
		t.Fatal("expected non-nil SystemContext from ProbeSystemContext")
	}

	if ctx.OSName != runtime.GOOS {
		t.Errorf("expected OSName %q, got %q", runtime.GOOS, ctx.OSName)
	}

	if ctx.Architecture != runtime.GOARCH {
		t.Errorf("expected Architecture %q, got %q", runtime.GOARCH, ctx.Architecture)
	}

	if ctx.Shell == "" {
		t.Errorf("expected non-empty Shell")
	}

	if ctx.ShellVersion == "" {
		t.Errorf("expected non-empty ShellVersion")
	}

	t.Logf("Probed System Context:\n  OS: %s (%s, %s)\n  Shell: %s\n  Package Managers: %v",
		ctx.OSDistribution, ctx.OSBuild, ctx.Architecture, ctx.ShellVersion, ctx.PackageManagers)
}

func TestFormatPromptContext(t *testing.T) {
	ctx := &SystemContext{
		OSName:          "linux",
		OSDistribution:  "Debian GNU/Linux 13 (trixie)",
		OSBuild:         "Linux 6.12.115",
		Architecture:    "amd64",
		Shell:           "bash",
		ShellVersion:    "Bash 5.2.37",
		PackageManagers: []string{"apt"},
		DetectedAt:      time.Now(),
	}

	// Non-root / non-elevated prompt format
	nonRootPrompt := ctx.FormatPromptContext(false)
	if !strings.Contains(nonRootPrompt, "Debian GNU/Linux 13 (trixie)") {
		t.Errorf("expected prompt to contain distro name")
	}
	if !strings.Contains(nonRootPrompt, "sudo") {
		t.Errorf("expected non-root prompt to instruct using sudo for administrative commands")
	}
	if !strings.Contains(nonRootPrompt, "Package managers: apt") {
		t.Errorf("expected prompt to contain package manager list")
	}

	// Root / elevated prompt format
	rootPrompt := ctx.FormatPromptContext(true)
	if !strings.Contains(rootPrompt, "Running as root") {
		t.Errorf("expected root prompt to indicate running as root")
	}
	if !strings.Contains(rootPrompt, "do NOT include 'sudo'") {
		t.Errorf("expected root prompt to instruct not including sudo")
	}

	// Windows context format
	winCtx := &SystemContext{
		OSName:          "windows",
		OSDistribution:  "Windows 10 Home (26H2)",
		OSBuild:         "Build 26300",
		Architecture:    "amd64",
		Shell:           "powershell",
		ShellVersion:    "PowerShell 5.1.26100",
		PackageManagers: []string{"winget"},
		DetectedAt:      time.Now(),
	}

	winPrompt := winCtx.FormatPromptContext(false)
	if !strings.Contains(winPrompt, "windows/amd64") {
		t.Errorf("expected windows prompt to contain windows/amd64")
	}
	if !strings.Contains(winPrompt, "Standard non-elevated user") {
		t.Errorf("expected windows prompt to indicate non-elevated state")
	}
}

func TestGetActiveSystemContextCaching(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ask-sysctx-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mockCtx := SystemContext{
		OSName:          "linux",
		OSDistribution:  "Arch Linux",
		OSBuild:         "Linux 6.10",
		Architecture:    "amd64",
		Shell:           DetectActiveShell(),
		ShellVersion:    "Bash 5.2",
		PackageManagers: []string{"pacman"},
		DetectedAt:      time.Now(),
	}

	data, err := json.Marshal(mockCtx)
	if err != nil {
		t.Fatal(err)
	}

	cacheFile := filepath.Join(tmpDir, "system-context.json")
	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Verify unmarshaling cached file
	readData, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	var loaded SystemContext
	if err := json.Unmarshal(readData, &loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.OSDistribution != "Arch Linux" {
		t.Errorf("expected 'Arch Linux', got %q", loaded.OSDistribution)
	}
}

func TestValidateCrossOSArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Unix command proposed on Windows should fail validation
		err := ValidateCommandCompleteness("git st", "sudo apt install git")
		if err == nil {
			t.Errorf("expected error for 'sudo apt install git' on Windows")
		}
		if !strings.Contains(err.Error(), "unix-specific command") {
			t.Errorf("unexpected error message: %v", err)
		}
	} else {
		// Windows drive path proposed on Unix should fail validation
		err := ValidateCommandCompleteness("git st", "C:\\Users\\foo\\.local\\bin\\ask")
		if err == nil {
			t.Errorf("expected error for Windows drive path on Unix")
		}
		if !strings.Contains(err.Error(), "windows drive path") {
			t.Errorf("unexpected error message: %v", err)
		}
	}
}

func TestIsProcessElevated(t *testing.T) {
	// Should execute without panicking or erroring
	elevated := isProcessElevated()
	t.Logf("Current process elevation: %v", elevated)
}
