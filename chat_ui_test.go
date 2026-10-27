package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChatUIBasicsAndDeadlockFree(t *testing.T) {
	cfg := &Config{
		Model:  "test-model",
		APIKey: "test-key",
	}
	session := &Session{
		ID:       "test-session",
		Messages: []Message{},
	}
	opts := QueryOptions{}

	chat := NewChatUI(cfg, session, opts, "first line", 80, 24)

	// Verify initial prompt loaded
	if len(chat.PromptLines) != 1 || chat.PromptLines[0] != "first line" {
		t.Fatalf("unexpected prompt lines: %v", chat.PromptLines)
	}

	// Press Enter (13) to create second line
	chat.HandleKey([]byte{13})
	// Type "second line"
	for _, b := range []byte("second line") {
		chat.HandleKey([]byte{b})
	}

	if len(chat.PromptLines) != 2 || chat.PromptLines[1] != "second line" {
		t.Fatalf("expected 2 prompt lines, got: %v", chat.PromptLines)
	}

	// Test render callback during streaming without deadlocking
	renderCount := 0
	var renderMu sync.Mutex
	chat.OnNeedRender = func() {
		renderMu.Lock()
		defer renderMu.Unlock()
		renderCount++
		// Crucial: RenderRow and CursorPosition must acquire chat.mu without deadlock!
		for r := 0; r < 24; r++ {
			_ = chat.RenderRow(r)
		}
		_, _ = chat.CursorPosition()
	}

	// Simulate streaming tokens from background goroutine
	done := make(chan bool)
	go func() {
		for i := 0; i < 20; i++ {
			chat.mu.Lock()
			chat.StreamContent.WriteString(" token")
			chat.rebuildReversedLines()
			renderFn := chat.OnNeedRender
			chat.mu.Unlock()

			if renderFn != nil {
				renderFn()
			}
			time.Sleep(2 * time.Millisecond)
		}
		done <- true
	}()

	select {
	case <-done:
		// Success, no deadlock!
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK DETECTED during streaming token render!")
	}

	// Test mouse interactions
	chat.HandleClick(5, 5) // Click in history
	chat.HandleDrag(15, 6, 80)
	selected := chat.FinishSelection()
	_ = selected

	// Populate a turn in history for selection tests
	chat.Turns = append(chat.Turns, ChatTurn{Role: "assistant", Content: "line one\nline two"})
	chat.rebuildReversedLines()

	// Test Ctrl+X with selection active (copies selection, does not quit)
	chat.Selecting = true
	chat.SelStartR = 1 // line one
	chat.SelStartC = 0
	chat.SelEndR = 2 // line two
	chat.SelEndC = 8
	shouldExit := chat.HandleKey([]byte{0x18}) // Ctrl+X
	if shouldExit {
		t.Fatal("Ctrl+X should not exit")
	}

	// Verify \n included in multiline selection
	text := chat.GetSelectedText()
	if !strings.Contains(text, "\n") {
		t.Fatalf("expected multiline selection to contain newline, got: %q", text)
	}

	// Test Ctrl+C quits when not streaming
	shouldExit = chat.HandleKey([]byte{0x03}) // Ctrl+C
	if !shouldExit {
		t.Fatal("Ctrl+C should signal exit when not streaming")
	}
}
