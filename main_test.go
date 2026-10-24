package main

import (
	"strings"
	"testing"
)

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
	// Create 14 messages (7 turns)
	for i := 0; i < 7; i++ {
		messages = append(messages,
			Message{Role: "user", Content: "query"},
			Message{Role: "assistant", Content: "response"},
		)
	}

	compacted := compactHistory(messages)

	// Should have 1 system summary message + maxRecentMessages (6) = 7 messages total
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
