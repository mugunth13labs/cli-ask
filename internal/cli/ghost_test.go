package cli

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCleanGhostSuggestion(t *testing.T) {
	tests := []struct {
		name      string
		inputLine string
		rawOutput string
		expected  string
	}{
		{
			name:      "case-insensitive full command completion get-proc",
			inputLine: "get-proc",
			rawOutput: "Get-Process",
			expected:  "Get-Process",
		},
		{
			name:      "markdown fences stripped",
			inputLine: "git st",
			rawOutput: "```bash\ngit status\n```",
			expected:  "git status",
		},
		{
			name:      "sub-word completion PowerShell Format-L direct prefix",
			inputLine: "Format-L",
			rawOutput: "Format-List",
			expected:  "Format-List",
		},
		{
			name:      "sub-word completion Get-ComputerInf",
			inputLine: "Get-ComputerInf",
			rawOutput: "Get-ComputerInfo",
			expected:  "Get-ComputerInfo",
		},
		{
			name:      "natural language intent comment",
			inputLine: "# list all files by size",
			rawOutput: "ls -lhS",
			expected:  "ls -lhS",
		},
		{
			name:      "overlap command after pipe",
			inputLine: "Get-Process | Where-",
			rawOutput: "Where-Object {$_.CPU -gt 10}",
			expected:  "Get-Process | Where-Object {$_.CPU -gt 10}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanGhostSuggestion(tt.inputLine, tt.rawOutput)
			if got != tt.expected {
				t.Errorf("CleanGhostSuggestion(%q, %q) = %q, expected %q", tt.inputLine, tt.rawOutput, got, tt.expected)
			}
		})
	}
}

func TestParseCLIArgsGhost(t *testing.T) {
	p1 := parseCLIArgs([]string{"--toggle-ghost"})
	if p1.Action != "toggle-ghost" {
		t.Errorf("expected action 'toggle-ghost', got %q", p1.Action)
	}

	p2 := parseCLIArgs([]string{"--complete", "Get-Proc"})
	if p2.Action != "complete" || p2.ActionArg != "Get-Proc" {
		t.Errorf("expected action 'complete' with arg 'Get-Proc', got %q (%q)", p2.Action, p2.ActionArg)
	}

	p3 := parseCLIArgs([]string{"--suggest", "ls -l"})
	if p3.Action != "complete" || p3.ActionArg != "ls -l" {
		t.Errorf("expected action 'complete' with arg 'ls -l', got %q (%q)", p3.Action, p3.ActionArg)
	}

	p4 := parseCLIArgs([]string{"--install-shell-ghost"})
	if p4.Action != "install-shell-ghost" {
		t.Errorf("expected action 'install-shell-ghost', got %q", p4.Action)
	}

	p5 := parseCLIArgs([]string{"--uninstall-shell-ghost"})
	if p5.Action != "uninstall-shell-ghost" {
		t.Errorf("expected action 'uninstall-shell-ghost', got %q", p5.Action)
	}
}

func TestDetectActiveShell(t *testing.T) {
	shell := DetectActiveShell()
	if shell == "" {
		t.Errorf("expected non-empty shell name")
	}
	t.Logf("Detected active shell: %q", shell)
}

func TestQueryGhostLive(t *testing.T) {
	if os.Getenv("ASK_LIVE_TEST") == "" {
		t.Skip("skipping live network test; set ASK_LIVE_TEST=1 to run")
	}
	cfg, err := loadConfig()
	if err != nil || cfg.APIKey == "" {
		t.Skip("skipping without config/key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	res, err := QueryGhostCompletion(ctx, cfg, "get-proc")
	t.Logf("Result: %q, Err: %v", res, err)
}
