package cli

import (
	"testing"
)

func TestWrapLogicalLinesToVisualAndPosMapping(t *testing.T) {
	lines := []string{
		"Hello world this is a test of visual line wrapping",
		"",
		"Another short line",
	}
	maxWidth := 15

	vis := WrapLogicalLinesToVisual(lines, maxWidth)
	if len(vis) < 3 {
		t.Fatalf("expected at least 3 visual lines, got %d", len(vis))
	}

	// First line should be wrapped into multiple segments
	if vis[0].LogicalRow != 0 || vis[1].LogicalRow != 0 {
		t.Errorf("expected first visual lines to map to LogicalRow 0")
	}

	// Verify forward and reverse coordinate mapping
	vRow, vCol := VisualPosFromLogical(vis, 0, 0)
	if vRow != 0 || vCol != 0 {
		t.Errorf("expected visual pos (0, 0), got (%d, %d)", vRow, vCol)
	}

	lRow, lCol := LogicalPosFromVisual(vis, vRow, vCol)
	if lRow != 0 || lCol != 0 {
		t.Errorf("expected logical pos (0, 0), got (%d, %d)", lRow, lCol)
	}

	// Empty line mapping
	emptyVisIdx := -1
	for i, v := range vis {
		if v.LogicalRow == 1 {
			emptyVisIdx = i
			break
		}
	}
	if emptyVisIdx == -1 {
		t.Fatalf("expected visual entry for empty logical line")
	}
	vRow, vCol = VisualPosFromLogical(vis, 1, 0)
	if vRow != emptyVisIdx || vCol != 0 {
		t.Errorf("expected empty line visual pos (%d, 0), got (%d, %d)", emptyVisIdx, vRow, vCol)
	}
}

func TestSubmitKeySequencesAndPrefix(t *testing.T) {
	// Alt+Right
	altRight := []byte("\x1b[1;3C")
	if !IsSubmitKeySequence(altRight) {
		t.Errorf("expected Alt+Right to match submit sequence")
	}

	// Ctrl+Enter CSI u
	ctrlEnter := []byte("\x1b[13;5u")
	if !IsSubmitKeySequence(ctrlEnter) {
		t.Errorf("expected Ctrl+Enter CSI u to match submit sequence")
	}

	// Alt+Enter
	altEnter := []byte("\x1b\r")
	if !IsSubmitKeySequence(altEnter) {
		t.Errorf("expected Alt+Enter to match submit sequence")
	}

	// Kitty Ctrl+S
	ctrlS := []byte("\x1b[115;5u")
	if !IsSubmitKeySequence(ctrlS) {
		t.Errorf("expected Ctrl+S to match submit sequence")
	}

	// HasSubmitPrefix
	dataWithPrefix := []byte("\x1b[1;3Cextra_data")
	hasPrefix, length := HasSubmitPrefix(dataWithPrefix)
	if !hasPrefix || length != len(altRight) {
		t.Errorf("expected HasSubmitPrefix true with length %d, got (%v, %d)", len(altRight), hasPrefix, length)
	}

	// Non-submit sequence
	upArrow := []byte("\x1b[A")
	if IsSubmitKeySequence(upArrow) {
		t.Errorf("Up arrow should not be submit sequence")
	}
	if ok, _ := HasSubmitPrefix(upArrow); ok {
		t.Errorf("Up arrow should not have submit prefix")
	}

	// StripSubmitKeySequences
	cookedInput := "hello world\x1b[1;3C"
	cleaned, found := StripSubmitKeySequences(cookedInput)
	if !found || cleaned != "hello world" {
		t.Errorf("expected cleaned 'hello world' and found=true, got (%q, %v)", cleaned, found)
	}
}

func TestParseEscapeSeqLengths(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected int
	}{
		{"Empty", []byte{}, 0},
		{"Bare ESC", []byte{0x1b}, 1},
		{"CSI Up Arrow", []byte("\x1b[A"), 3},
		{"CSI Modified Alt+Right", []byte("\x1b[1;3C"), 6},
		{"macOS Alt+Right", []byte("\x1b\x1b[C"), 4},
		{"SS3 Up Arrow", []byte("\x1bOA"), 3},
		{"OSC 52 Sequence", []byte("\x1b]52;c;dGVzdA==\x07"), 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseEscapeSeq(tt.input)
			if got != tt.expected {
				t.Errorf("ParseEscapeSeq(%q) = %d, expected %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestApplyAutocompleteCandidateHelper(t *testing.T) {
	// 1. File candidate without space
	lines := []string{"look at @mai"}
	newLines, newCol := ApplyAutocompleteCandidate(lines, 0, 12, 8, "main.go", false)
	expectedLine := "look at @main.go "
	if newLines[0] != expectedLine {
		t.Errorf("expected %q, got %q", expectedLine, newLines[0])
	}
	if newCol != len([]rune(expectedLine)) {
		t.Errorf("expected cursor at %d, got %d", len([]rune(expectedLine)), newCol)
	}

	// 2. Directory candidate (onion peeling: ends with /)
	lines = []string{"open @sr"}
	newLines, _ = ApplyAutocompleteCandidate(lines, 0, 8, 5, "src/", false)
	expectedDir := "open @src/"
	if newLines[0] != expectedDir {
		t.Errorf("expected %q, got %q", expectedDir, newLines[0])
	}

	// 3. File candidate with spaces (should quote)
	lines = []string{"check @doc"}
	newLines, _ = ApplyAutocompleteCandidate(lines, 0, 10, 6, "my doc.txt", false)
	expectedQuoted := "check @\"my doc.txt\" "
	if newLines[0] != expectedQuoted {
		t.Errorf("expected %q, got %q", expectedQuoted, newLines[0])
	}

	// 4. Slash command
	lines = []string{"/he"}
	newLines, _ = ApplyAutocompleteCandidate(lines, 0, 3, 0, "/help", true)
	expectedSlash := "/help "
	if newLines[0] != expectedSlash {
		t.Errorf("expected %q, got %q", expectedSlash, newLines[0])
	}
}

func TestBuildAPIMessagesHelper(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "First question"},
		{Role: "assistant", Content: "First answer"},
	}

	// Standard conversational query
	msgs := buildAPIMessages(false, history, "Second question")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != systemPrompt {
		t.Errorf("expected systemPrompt for conversational mode")
	}
	if msgs[3].Role != "user" || msgs[3].Content != "Second question" {
		t.Errorf("expected user prompt as last message")
	}

	// CodeGen mode
	cgMsgs := buildAPIMessages(true, nil, "Write a bash script")
	if len(cgMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(cgMsgs))
	}
	if cgMsgs[0].Content != codePrompt {
		t.Errorf("expected codePrompt for codegen mode")
	}
}
