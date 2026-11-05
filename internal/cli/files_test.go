package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractFileReferences(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		expected []string
	}{
		{
			name:     "single file",
			prompt:   "explain @main.go please",
			expected: []string{"main.go"},
		},
		{
			name:     "multiple files with paths",
			prompt:   "compare @internal/cli/editor.go with @cmd/ask/main.go",
			expected: []string{"internal/cli/editor.go", "cmd/ask/main.go"},
		},
		{
			name:     "email should not be extracted",
			prompt:   "send email to user@example.com and check @file.txt",
			expected: []string{"file.txt"},
		},
		{
			name:     "trailing punctuation stripped",
			prompt:   "what is in (@README.md)? also check @go.mod, @pkg/util.go!",
			expected: []string{"README.md", "go.mod", "pkg/util.go"},
		},
		{
			name:     "deduplicate references",
			prompt:   "check @main.go and again @main.go please",
			expected: []string{"main.go"},
		},
		{
			name:     "quoted file with spaces",
			prompt:   `please read @"Downloads/4050materials/Week09b-Matrix-Matrix Multiplication.pdf" and explain`,
			expected: []string{"Downloads/4050materials/Week09b-Matrix-Matrix Multiplication.pdf"},
		},
		{
			name:     "no references",
			prompt:   "how do I sort an array in Go?",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractFileReferences(tc.prompt)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

func TestExtractFileReferencesWithSpacesOnDisk(t *testing.T) {
	tempDir := t.TempDir()
	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(origWd) }()

	dir := filepath.Join("Downloads", "4050materials")
	_ = os.MkdirAll(dir, 0755)
	filePath := filepath.Join(dir, "Week09b-Matrix-Matrix Multiplication.pdf")
	_ = os.WriteFile(filePath, []byte("matrix content"), 0644)

	prompt := "@Downloads/4050materials/Week09b-Matrix-Matrix Multiplication.pdf what does it say?"
	refs := ExtractFileReferences(prompt)
	expected := "Downloads/4050materials/Week09b-Matrix-Matrix Multiplication.pdf"
	if len(refs) != 1 || refs[0] != expected {
		t.Fatalf("expected [%s], got %v", expected, refs)
	}
}

func TestResolveFileContext(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Valid file
	testFile := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("hello world from sample.txt"), 0644); err != nil {
		t.Fatal(err)
	}

	attachments, block, err := ResolveFileContext(tempDir, []string{"sample.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Truncated {
		t.Errorf("expected Truncated=false")
	}
	if !strings.Contains(block, "hello world from sample.txt") {
		t.Errorf("expected content in block: %s", block)
	}

	// 2. Missing file
	_, _, err = ResolveFileContext(tempDir, []string{"nonexistent.go"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "File not found: @nonexistent.go (in current working directory)") {
		t.Errorf("unexpected error format: %v", err)
	}

	// 3. Binary file
	binFile := filepath.Join(tempDir, "test.bin")
	binData := []byte("plain text\x00with null byte")
	if err := os.WriteFile(binFile, binData, 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = ResolveFileContext(tempDir, []string{"test.bin"})
	if err == nil {
		t.Fatal("expected error for binary file, got nil")
	}
	if !strings.Contains(err.Error(), "appears to be a binary file") {
		t.Errorf("unexpected binary error: %v", err)
	}

	// 4. Large file truncation (1MB + 100KB)
	largeFile := filepath.Join(tempDir, "large.txt")
	largeData := make([]byte, MaxFileReadLimit+100*1024)
	for i := range largeData {
		largeData[i] = 'A'
	}
	if err := os.WriteFile(largeFile, largeData, 0644); err != nil {
		t.Fatal(err)
	}
	attachments, block, err = ResolveFileContext(tempDir, []string{"large.txt"})
	if err != nil {
		t.Fatalf("unexpected error for large file: %v", err)
	}
	if !attachments[0].Truncated {
		t.Errorf("expected attachment to be truncated")
	}
	if !strings.Contains(block, "[...truncated at 1MB...]") {
		t.Errorf("expected truncation notice in formatted block")
	}

	// 5. FormatHistoryPrompt
	historyPrompt := FormatHistoryPrompt("look at @large.txt and @sample.txt", attachments)
	if !strings.Contains(historyPrompt, "@large.txt [truncated]") {
		t.Errorf("expected '@large.txt [truncated]' in history prompt, got: %s", historyPrompt)
	}
	if strings.Contains(historyPrompt, "@sample.txt [truncated]") {
		t.Errorf("sample.txt should not be truncated")
	}
}

func TestScanWorkspaceFiles(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tempDir, "src"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, ".git", "objects"), 0755)

	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "src", "app.go"), []byte("package app"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, ".git", "config"), []byte("git config"), 0644)

	files, err := ScanWorkspaceFiles(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range files {
		if strings.Contains(f, ".git") {
			t.Fatalf("expected .git to be excluded, got: %s", f)
		}
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files (main.go, src/app.go), got %d: %v", len(files), files)
	}
}

func TestFilterFileCandidates(t *testing.T) {
	candidates := []string{
		"cmd/ask/main.go",
		"internal/cli/editor.go",
		"internal/cli/files.go",
		"README.md",
	}

	res := FilterFileCandidates(candidates, "ed")
	if len(res) == 0 || !strings.Contains(res[0], "editor.go") {
		t.Errorf("expected editor.go to match 'ed', got: %v", res)
	}

	resAll := FilterFileCandidates(candidates, "")
	if len(resAll) != len(candidates) {
		t.Errorf("expected all candidates for empty query")
	}
}

func TestAutocompleteStateLifecycle(t *testing.T) {
	auto := NewAutocomplete()
	auto.AllFiles = []string{"cmd/ask/main.go", "internal/cli/editor.go", "internal/cli/files.go", "README.md"}

	if auto.Active {
		t.Errorf("expected initially inactive")
	}

	// Trigger
	auto.Trigger(0, 5)
	if !auto.Active || auto.TriggerCol != 5 || len(auto.Candidates) != 4 {
		t.Fatalf("unexpected trigger state: %+v", auto)
	}

	// Navigation
	auto.Next()
	if auto.SelectedIndex != 1 || auto.SelectedCandidate() != "internal/cli/editor.go" {
		t.Errorf("expected index 1, got %d (%s)", auto.SelectedIndex, auto.SelectedCandidate())
	}
	auto.Prev()
	if auto.SelectedIndex != 0 {
		t.Errorf("expected index 0 after prev")
	}

	// Filtering
	auto.UpdateQuery("files")
	if len(auto.Candidates) != 1 || auto.Candidates[0] != "internal/cli/files.go" {
		t.Errorf("expected only files.go to match 'files', got: %v", auto.Candidates)
	}

	// Formatting dropdown
	lines := FormatDropdownLines(auto, 60)
	if len(lines) == 0 {
		t.Fatalf("expected dropdown lines, got none")
	}
	if !strings.Contains(lines[0], "files (1/1)") {
		t.Errorf("expected header with count in %s", lines[0])
	}
	if !strings.Contains(lines[1], "files.go") {
		t.Errorf("expected candidate in %s", lines[1])
	}

	// Dismiss
	auto.Dismiss()
	if auto.Active {
		t.Errorf("expected dismissed")
	}
}

func TestReadDirSingleLevel(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tempDir, "src", "sub"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, ".git"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, ".hidden"), 0755)

	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "src", "app.go"), []byte("package app"), 0644)

	// Level 1: root
	entries, err := ReadDirSingleLevel(tempDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedRoot := []string{"main.go", "src/"}
	if len(entries) != len(expectedRoot) {
		t.Fatalf("expected entries %v, got %v", expectedRoot, entries)
	}
	for i := range expectedRoot {
		if entries[i] != expectedRoot[i] {
			t.Errorf("at index %d: expected %q, got %q", i, expectedRoot[i], entries[i])
		}
	}

	// Level 2: inside src
	srcEntries, err := ReadDirSingleLevel(tempDir, "src")
	if err != nil {
		t.Fatalf("unexpected error reading src: %v", err)
	}
	expectedSrc := []string{"src/app.go", "src/sub/"}
	if len(srcEntries) != len(expectedSrc) {
		t.Fatalf("expected src entries %v, got %v", expectedSrc, srcEntries)
	}
	for i := range expectedSrc {
		if srcEntries[i] != expectedSrc[i] {
			t.Errorf("at index %d: expected %q, got %q", i, expectedSrc[i], srcEntries[i])
		}
	}
}

func TestProgressiveOnionPeeling(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tempDir, "pkg", "util"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "root.go"), []byte("package root"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "pkg.go"), []byte("package pkg"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "util", "helper.go"), []byte("package util"), 0644)

	auto := NewAutocomplete()
	auto.Cwd = tempDir

	// Trigger: lists immediate root entries only
	auto.Trigger(0, 0)
	if !auto.Active {
		t.Fatal("expected auto to be active")
	}
	if len(auto.Candidates) != 2 || auto.Candidates[0] != "pkg/" || auto.Candidates[1] != "root.go" {
		t.Fatalf("expected immediate root entries [pkg/ root.go], got %v", auto.Candidates)
	}

	// Enter directory 'pkg/'
	auto.UpdateQuery("pkg/")
	if len(auto.Candidates) != 2 || auto.Candidates[0] != "pkg/pkg.go" || auto.Candidates[1] != "pkg/util/" {
		t.Fatalf("expected immediate pkg/ entries, got %v", auto.Candidates)
	}

	// Filter inside 'pkg/'
	auto.UpdateQuery("pkg/ut")
	if len(auto.Candidates) != 1 || auto.Candidates[0] != "pkg/util/" {
		t.Fatalf("expected only pkg/util/ to match, got %v", auto.Candidates)
	}

	// Enter 'pkg/util/'
	auto.UpdateQuery("pkg/util/")
	if len(auto.Candidates) != 1 || auto.Candidates[0] != "pkg/util/helper.go" {
		t.Fatalf("expected pkg/util/helper.go, got %v", auto.Candidates)
	}
}

func TestSlashCommandDropdownAutocomplete(t *testing.T) {
	auto := NewAutocomplete()
	auto.TriggerSlash(0, 0)

	if !auto.Active || auto.Mode != AutocompleteModeSlash {
		t.Fatalf("expected active slash autocomplete, got active=%v mode=%v", auto.Active, auto.Mode)
	}

	if len(auto.Candidates) != 3 {
		t.Fatalf("expected 3 standard slash commands (/help, /clear, /exit), got %d (%v)", len(auto.Candidates), auto.Candidates)
	}

	// Filter query: 'h' -> matches /help
	auto.UpdateQuery("h")
	if len(auto.Candidates) != 1 || auto.Candidates[0] != "/help" {
		t.Fatalf("expected '/help' to match 'h', got %v", auto.Candidates)
	}

	// Formatting dropdown
	lines := FormatDropdownLines(auto, 60)
	if len(lines) == 0 {
		t.Fatal("expected dropdown lines for slash command")
	}
	if !strings.Contains(lines[0], "commands (1/1)") {
		t.Errorf("expected commands count header in %s", lines[0])
	}
	if !strings.Contains(lines[1], "/help") {
		t.Errorf("expected /help in %s", lines[1])
	}
}
