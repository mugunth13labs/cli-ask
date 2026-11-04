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
		{
			name:      "multiline loop preservation",
			inputLine: "for f in",
			rawOutput: "for f in *.log; do\n    echo \"Processing $f\"\n    gzip \"$f\"\ndone",
			expected:  "for f in *.log; do\n    echo \"Processing $f\"\n    gzip \"$f\"\ndone",
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

func TestValidateCommandCompleteness(t *testing.T) {
	validTests := []struct {
		name      string
		inputLine string
		candidate string
	}{
		{
			name:      "valid simple command",
			inputLine: "git st",
			candidate: "git status",
		},
		{
			name:      "valid pipeline with balanced braces",
			inputLine: "Get-Proc",
			candidate: "Get-Process | Where-Object { $_.CPU -gt 10 }",
		},
		{
			name:      "valid multiline loop up to 5 lines",
			inputLine: "for i in",
			candidate: "for i in 1 2 3; do\n  echo $i\ndone",
		},
		{
			name:      "valid quotes and parentheses",
			inputLine: "echo",
			candidate: `echo "file (1).txt" 'single quote'`,
		},
	}

	for _, tt := range validTests {
		t.Run("valid_"+tt.name, func(t *testing.T) {
			if err := ValidateCommandCompleteness(tt.inputLine, tt.candidate); err != nil {
				t.Errorf("expected candidate to be valid, got error: %v", err)
			}
		})
	}

	invalidTests := []struct {
		name      string
		inputLine string
		candidate string
	}{
		{
			name:      "empty candidate",
			inputLine: "git",
			candidate: "   ",
		},
		{
			name:      "candidate identical to input",
			inputLine: "git status",
			candidate: "git status",
		},
		{
			name:      "exceeds 5 lines limit",
			inputLine: "for i in",
			candidate: "line1\nline2\nline3\nline4\nline5\nline6",
		},
		{
			name:      "trailing pipe operator",
			inputLine: "Get-Process",
			candidate: "Get-Process |",
		},
		{
			name:      "trailing logical AND",
			inputLine: "git add",
			candidate: "git add . &&",
		},
		{
			name:      "trailing logical OR",
			inputLine: "test",
			candidate: "test -f foo ||",
		},
		{
			name:      "trailing backslash continuation",
			inputLine: "echo",
			candidate: "echo hello \\",
		},
		{
			name:      "unclosed single quote",
			inputLine: "echo",
			candidate: "echo 'unclosed string",
		},
		{
			name:      "unclosed double quote",
			inputLine: "echo",
			candidate: `echo "unclosed string`,
		},
		{
			name:      "unclosed brace",
			inputLine: "Where-",
			candidate: "Where-Object { $_.CPU -gt 10",
		},
		{
			name:      "unmatched closing parenthesis",
			inputLine: "echo",
			candidate: "echo )",
		},
	}

	for _, tt := range invalidTests {
		t.Run("invalid_"+tt.name, func(t *testing.T) {
			if err := ValidateCommandCompleteness(tt.inputLine, tt.candidate); err == nil {
				t.Errorf("expected candidate to be invalid, but validation passed: %q", tt.candidate)
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

	p6 := parseCLIArgs([]string{"--uninstall-self"})
	if p6.Action != "uninstall-self" {
		t.Errorf("expected action 'uninstall-self', got %q", p6.Action)
	}

	p7 := parseCLIArgs([]string{"--uninstall"})
	if p7.Action != "uninstall-self" {
		t.Errorf("expected action 'uninstall-self', got %q", p7.Action)
	}

	p8 := parseCLIArgs([]string{"--system-context"})
	if p8.Action != "system-context" {
		t.Errorf("expected action 'system-context', got %q", p8.Action)
	}

	p9 := parseCLIArgs([]string{"--env"})
	if p9.Action != "system-context" {
		t.Errorf("expected action 'system-context', got %q", p9.Action)
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
		t.Skip("skipping live test: missing config or api key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := QueryGhostCompletion(ctx, cfg, "git st")
	if err != nil {
		t.Fatalf("QueryGhostCompletion failed: %v", err)
	}
	t.Logf("Live completion result: %q", res)
}
