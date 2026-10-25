package main

import (
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
	}

	for _, c := range cases {
		p := parseCLIArgs(c.input)
		if p.Action != c.expected {
			t.Errorf("expected action %q for %v, got %q", c.expected, c.input, p.Action)
		}
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

	// Normal command with flags
	p2 := parseCLIArgs([]string{"ps", "-ef", "--sort", "-p"})
	if p2.Action != "query" {
		t.Errorf("expected action 'query', got %q", p2.Action)
	}
	if p2.Prompt != "ps -ef --sort -p" {
		t.Errorf("expected prompt 'ps -ef --sort -p', got %q", p2.Prompt)
	}
}

func TestParseCLIArgsEscapeSeparator(t *testing.T) {
	p := parseCLIArgs([]string{"--", "--clear", "-c", "--config"})
	if p.Action != "query" {
		t.Errorf("expected action 'query', got %q", p.Action)
	}
	if p.Prompt != "--clear -c --config" {
		t.Errorf("expected prompt '--clear -c --config', got %q", p.Prompt)
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
