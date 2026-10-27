package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseCLIArgsStandalone(t *testing.T) {
	cases := []struct {
		input    []string
		expected string
	}{
		{[]string{"--help"}, "help"},
		{[]string{"-h"}, "help"},
		{[]string{"help"}, "help"},
		{[]string{"--version"}, "version"},
		{[]string{"-v"}, "version"},
		{[]string{"--config"}, "config"},
		{[]string{"--clear"}, "clear"},
		{[]string{"-c"}, "clear"},
		{[]string{"--update-self"}, "update-self"},
		{[]string{"--update"}, "update-self"},
		{[]string{"--chatbot"}, "chatbot"},
		{[]string{"-i"}, "chatbot"},
	}

	for _, c := range cases {
		p := parseCLIArgs(c.input)
		if p.Action != c.expected {
			t.Errorf("expected action %q for %v, got %q", c.expected, c.input, p.Action)
		}
	}
}

func TestParseCLIArgsChatbotWithPrompt(t *testing.T) {
	p := parseCLIArgs([]string{"-i", "explain", "goroutines"})
	if p.Action != "chatbot" {
		t.Errorf("expected action 'chatbot', got %q", p.Action)
	}
	if p.Prompt != "explain goroutines" {
		t.Errorf("expected prompt 'explain goroutines', got %q", p.Prompt)
	}

	p2 := parseCLIArgs([]string{"--chatbot", "--no-context", "hello"})
	if p2.Action != "chatbot" {
		t.Errorf("expected action 'chatbot', got %q", p2.Action)
	}
	if p2.Prompt != "hello" {
		t.Errorf("expected prompt 'hello', got %q", p2.Prompt)
	}
	if p2.UseContext != false {
		t.Errorf("expected UseContext to be false")
	}
}

func TestParseCLIArgsFileOutput(t *testing.T) {
	p1 := parseCLIArgs([]string{"-o", "out.txt", "explain", "kubernetes"})
	if p1.OutputFile != "out.txt" {
		t.Errorf("expected OutputFile 'out.txt', got %q", p1.OutputFile)
	}
	if p1.Prompt != "explain kubernetes" {
		t.Errorf("expected prompt 'explain kubernetes', got %q", p1.Prompt)
	}

	p2 := parseCLIArgs([]string{"--output", "file.log", "hello"})
	if p2.OutputFile != "file.log" {
		t.Errorf("expected OutputFile 'file.log', got %q", p2.OutputFile)
	}
	if p2.Prompt != "hello" {
		t.Errorf("expected prompt 'hello', got %q", p2.Prompt)
	}

	p3 := parseCLIArgs([]string{"-a", "notes.md", "more", "context"})
	if p3.AppendFile != "notes.md" {
		t.Errorf("expected AppendFile 'notes.md', got %q", p3.AppendFile)
	}
	if p3.Prompt != "more context" {
		t.Errorf("expected prompt 'more context', got %q", p3.Prompt)
	}

	p4 := parseCLIArgs([]string{"-i", "-o", "session.txt"})
	if p4.Action != "chatbot" {
		t.Errorf("expected action 'chatbot', got %q", p4.Action)
	}
	if p4.OutputFile != "session.txt" {
		t.Errorf("expected OutputFile 'session.txt', got %q", p4.OutputFile)
	}
}

func TestParseCLIArgsDisambiguation(t *testing.T) {
	// If extra args exist, --config or --help should NOT trigger action, but be treated as prompt
	p := parseCLIArgs([]string{"--config", "ps", "--help"})
	if p.Action != "query" {
		t.Errorf("expected action 'query', got %q", p.Action)
	}
	if p.Prompt != "--config ps --help" {
		t.Errorf("expected prompt '--config ps --help', got %q", p.Prompt)
	}

	// Normal command with flags (e.g. grep -i pattern)
	p2 := parseCLIArgs([]string{"grep", "-i", "pattern"})
	if p2.Action != "query" {
		t.Errorf("expected action 'query', got %q", p2.Action)
	}
	if p2.Prompt != "grep -i pattern" {
		t.Errorf("expected prompt 'grep -i pattern', got %q", p2.Prompt)
	}

	p3 := parseCLIArgs([]string{"ps", "-ef", "--sort", "-p"})
	if p3.Action != "query" {
		t.Errorf("expected action 'query', got %q", p3.Action)
	}
	if p3.Prompt != "ps -ef --sort -p" {
		t.Errorf("expected prompt 'ps -ef --sort -p', got %q", p3.Prompt)
	}

	// gcc -o main main.c must NOT be parsed as ask -o output file!
	p4 := parseCLIArgs([]string{"gcc", "-o", "main", "main.c"})
	if p4.OutputFile != "" {
		t.Errorf("expected OutputFile to be empty for gcc command, got %q", p4.OutputFile)
	}
	if p4.Prompt != "gcc -o main main.c" {
		t.Errorf("expected prompt 'gcc -o main main.c', got %q", p4.Prompt)
	}
}

func TestParseCLIArgsEscapeSeparator(t *testing.T) {
	p := parseCLIArgs([]string{"--", "--clear", "-c", "--config", "-i", "-o", "file.txt"})
	if p.Action != "query" {
		t.Errorf("expected action 'query', got %q", p.Action)
	}
	if p.OutputFile != "" {
		t.Errorf("expected OutputFile empty when after --, got %q", p.OutputFile)
	}
	if p.Prompt != "--clear -c --config -i -o file.txt" {
		t.Errorf("expected prompt '--clear -c --config -i -o file.txt', got %q", p.Prompt)
	}
}

func TestParseCLIArgsNoContext(t *testing.T) {
	p := parseCLIArgs([]string{"--no-context", "explain", "this"})
	if p.UseContext != false {
		t.Errorf("expected UseContext to be false")
	}
	if p.Prompt != "explain this" {
		t.Errorf("expected prompt 'explain this', got %q", p.Prompt)
	}
}

func TestReadChatPromptMultiline(t *testing.T) {
	// 1. Multiline buffer submitted with double Enter (blank line)
	input1 := "line 1\nline 2\n\n"
	reader1 := bufio.NewReader(strings.NewReader(input1))
	res1, err1 := readChatPrompt(reader1, false)
	if err1 != nil {
		t.Fatalf("unexpected error: %v", err1)
	}
	expected1 := "line 1\nline 2"
	if res1 != expected1 {
		t.Errorf("expected %q, got %q", expected1, res1)
	}

	// 2. Immediate command execution
	input2 := "exit\n"
	reader2 := bufio.NewReader(strings.NewReader(input2))
	res2, err2 := readChatPrompt(reader2, false)
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	if res2 != "exit" {
		t.Errorf("expected 'exit', got %q", res2)
	}

	// 3. Ctrl+Enter escape sequence submit
	input3 := "single line\x1b[13;5u\n"
	reader3 := bufio.NewReader(strings.NewReader(input3))
	res3, err3 := readChatPrompt(reader3, false)
	if err3 != nil {
		t.Fatalf("unexpected error: %v", err3)
	}
	if res3 != "single line" {
		t.Errorf("expected 'single line', got %q", res3)
	}
}

func TestCompactHistoryUnderLimit(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you"},
		{Role: "assistant", Content: "good"},
	}

	compacted := compactHistory(messages)
	if len(compacted) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(compacted))
	}
}

func TestCompactHistoryExceedingLimit(t *testing.T) {
	var messages []Message
	for i := 0; i < 7; i++ {
		messages = append(messages,
			Message{Role: "user", Content: "query"},
			Message{Role: "assistant", Content: "response"},
		)
	}

	compacted := compactHistory(messages)
	expectedLen := maxRecentMessages + 1
	if len(compacted) != expectedLen {
		t.Fatalf("expected %d compacted messages, got %d", expectedLen, len(compacted))
	}

	if compacted[0].Role != "system" {
		t.Errorf("expected first message to be system summary, got role %s", compacted[0].Role)
	}

	if !strings.Contains(compacted[0].Content, "Prior conversation context summary") {
		t.Errorf("expected summary text in compacted message, got: %s", compacted[0].Content)
	}
}

func TestSessionIDGeneration(t *testing.T) {
	id := getSessionID()
	if !strings.HasPrefix(id, "session-") {
		t.Errorf("expected session ID to start with 'session-', got: %s", id)
	}
}

func TestFormatSplitter(t *testing.T) {
	line := formatSplitter("nvidia/nemotron-3.5-lightning:free")
	if !strings.HasPrefix(line, "─── ask [nvidia/nemotron-3.5-lightning:free]") {
		t.Errorf("unexpected splitter format: %s", line)
	}
	if !strings.HasSuffix(line, "─") {
		t.Errorf("expected splitter to end with horizontal bar: %s", line)
	}
}
