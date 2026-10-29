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

func TestPromptComposerWordWrappingAndNoPrefix(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "hello world", 20, 10)

	// Verify no ">>> " or "... " prefix rendered
	renderedRow0 := chat.RenderRow(0)
	if strings.Contains(renderedRow0, ">>>") || strings.Contains(renderedRow0, "...") {
		t.Fatalf("expected prompt row to NOT contain '>>>' or '...', got: %q", renderedRow0)
	}
	if !strings.Contains(renderedRow0, "hello world") {
		t.Fatalf("expected prompt row to contain 'hello world', got: %q", renderedRow0)
	}

	// Verify cursor starts at column 11 with no +4 offset
	row, col := chat.CursorPosition()
	if row != 0 || col != 11 {
		t.Fatalf("expected cursor at (0, 11), got (%d, %d)", row, col)
	}

	// Type a long sentence that should soft word wrap at width 20
	chat.PromptLines = []string{"the quick brown fox jumps over the lazy dog"}
	chat.PromptCol = len([]rune(chat.PromptLines[0]))
	vis := chat.getVisualPromptLines(20)
	if len(vis) < 2 {
		t.Fatalf("expected visual wrapping into >= 2 lines, got %d lines: %+v", len(vis), vis)
	}

	// Cursor on visual line
	visRow, visCol := chat.cursorToVis(vis)
	if visRow != len(vis)-1 {
		t.Fatalf("expected cursor on last visual row %d, got %d", len(vis)-1, visRow)
	}
	_ = visCol
}

func TestPromptComposerMultilinePasteAndNewlines(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "", 40, 10)

	// Simulate pressing Enter via \r\n (2-byte sequence)
	chat.HandleKey([]byte("line1"))
	chat.HandleKey([]byte("\r\n"))
	chat.HandleKey([]byte("line2"))
	if len(chat.PromptLines) != 2 || chat.PromptLines[0] != "line1" || chat.PromptLines[1] != "line2" {
		t.Fatalf("expected 2 lines after \\r\\n Enter, got: %v", chat.PromptLines)
	}

	// Simulate multiline paste buffer with embedded \n and \r\n
	pasteBuf := []byte("pasted1\npasted2\r\npasted3")
	chat.PromptLines = []string{""}
	chat.PromptRow = 0
	chat.PromptCol = 0
	chat.HandleKey(pasteBuf)

	if len(chat.PromptLines) != 3 {
		t.Fatalf("expected 3 lines after multiline paste, got %d: %v", len(chat.PromptLines), chat.PromptLines)
	}
	if chat.PromptLines[0] != "pasted1" || chat.PromptLines[1] != "pasted2" || chat.PromptLines[2] != "pasted3" {
		t.Fatalf("unexpected pasted lines content: %v", chat.PromptLines)
	}
}

func TestModelResponseBarLeanAndTimestampOnly(t *testing.T) {
	cfg := &Config{Model: "my-custom-model", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "", 50, 15)

	chat.Turns = append(chat.Turns, ChatTurn{
		Role:      "assistant",
		Content:   "Here is the answer.",
		Timestamp: "16:45:00",
	})
	chat.rebuildReversedLines()

	if len(chat.ReversedLines) == 0 {
		t.Fatal("expected reversed lines to be populated")
	}

	// Check top banner line
	topBanner := chat.ReversedLines[0]
	if !topBanner.IsBanner || topBanner.BannerKind != "LEAN_LINE" {
		t.Fatalf("expected top banner to be LEAN_LINE, got: %+v", topBanner)
	}
	if !strings.Contains(topBanner.Text, "16:45:00") {
		t.Fatalf("expected banner to contain timestamp '16:45:00', got: %q", topBanner.Text)
	}
	if strings.Contains(topBanner.Text, "my-custom-model") || strings.Contains(topBanner.Text, "ask [") {
		t.Fatalf("expected model name to NOT appear in response bar, got: %q", topBanner.Text)
	}

	// Verify no end line exists in the turn
	for _, l := range chat.ReversedLines {
		if l.IsBanner && l.Text == strings.Repeat("─", 48) {
			t.Fatalf("expected NO end line in response turn, but found: %+v", l)
		}
	}
}

func TestStreamingStaticGeneratingLabel(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "", 50, 15)

	chat.IsStreaming = true
	chat.StreamContent.WriteString("Hello stream")
	chat.rebuildReversedLines()

	if len(chat.ReversedLines) == 0 {
		t.Fatal("expected reversed lines to be populated during streaming")
	}

	topBanner := chat.ReversedLines[0]
	if !topBanner.IsBanner || topBanner.BannerKind != "LEAN_LINE" {
		t.Fatalf("expected top banner to be LEAN_LINE, got: %+v", topBanner)
	}
	if !strings.Contains(topBanner.Text, "generating") {
		t.Fatalf("expected banner to contain 'generating', got: %q", topBanner.Text)
	}
	if strings.Contains(topBanner.Text, "generating .") {
		t.Fatalf("expected NO animated dots in generating text, got: %q", topBanner.Text)
	}

	// Verify no bottom border exists
	for _, l := range chat.ReversedLines {
		if l.IsBanner && l.Text == strings.Repeat("─", 48) {
			t.Fatalf("expected NO end line during streaming, but found: %+v", l)
		}
	}
}

func TestUserPromptLeftAlignedAndNoEndLines(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "", 50, 15)

	chat.Turns = append(chat.Turns, ChatTurn{
		Role:      "user",
		Content:   "What is Go?",
		Timestamp: "19:00:00",
	})
	chat.rebuildReversedLines()

	if len(chat.ReversedLines) == 0 {
		t.Fatal("expected reversed lines for user turn")
	}

	topBanner := chat.ReversedLines[0]
	if !topBanner.IsBanner || topBanner.BannerKind != "USER_LINE" {
		t.Fatalf("expected top banner to be USER_LINE, got: %+v", topBanner)
	}
	if !strings.HasPrefix(topBanner.Text, "─ prompt 19:00:00 ") {
		t.Fatalf("expected left-aligned prompt header '─ prompt 19:00:00 ', got: %q", topBanner.Text)
	}

	// Verify absence of YOU START and YOU END
	for _, l := range chat.ReversedLines {
		if strings.Contains(l.Text, "YOU START") || strings.Contains(l.Text, "YOU END") {
			t.Fatalf("expected no YOU START / YOU END in lines, found: %q", l.Text)
		}
	}
}

func TestPromptBoxSubtleTint(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "tint test", 40, 10)

	row0 := chat.RenderRow(0)
	if !strings.Contains(row0, "\x1b[48;5;236m") {
		t.Fatalf("expected prompt box row 0 to contain background tint \\x1b[48;5;236m, got: %q", row0)
	}
}

func TestStandaloneHeaderBarAndExitConfirmation(t *testing.T) {
	// Normal header
	header := renderStandaloneHeader(80, false, 0)
	if !strings.Contains(header, "ask chat") {
		t.Fatalf("expected header to contain 'ask chat', got: %q", header)
	}
	if !strings.Contains(header, "Ctrl+Enter") || !strings.Contains(header, "Ctrl+Shift+C/V") {
		t.Fatalf("expected header to contain shortcuts, got: %q", header)
	}

	// Exit confirmation header: Yes selected
	confYes := renderStandaloneHeader(80, true, 0)
	if !strings.Contains(confYes, "Exit ask chat?") || !strings.Contains(confYes, "Yes") || !strings.Contains(confYes, "No") {
		t.Fatalf("expected exit confirmation header, got: %q", confYes)
	}

	// Exit confirmation header: No selected
	confNo := renderStandaloneHeader(80, true, 1)
	if !strings.Contains(confNo, "Exit ask chat?") {
		t.Fatalf("expected exit confirmation header, got: %q", confNo)
	}
}

func TestDualPaneCtrl12TabSwitching(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	dp, err := NewDualPaneWorkspace(cfg, "", QueryOptions{})
	if err != nil {
		t.Fatalf("failed to create dual pane workspace: %v", err)
	}
	defer func() {
		if dp.RightPTY != nil {
			_ = dp.RightPTY.Close()
		}
	}()

	// Starts at PaneLeft
	if dp.ActivePane != PaneLeft {
		t.Fatalf("expected initial PaneLeft, got: %v", dp.ActivePane)
	}

	// Send Ctrl+2 (CSI u)
	dp.handleInput([]byte("\x1b[50;5u"))
	if dp.ActivePane != PaneRight {
		t.Fatalf("expected PaneRight after Ctrl+2, got: %v", dp.ActivePane)
	}

	// Send Ctrl+1 (CSI u)
	dp.handleInput([]byte("\x1b[49;5u"))
	if dp.ActivePane != PaneLeft {
		t.Fatalf("expected PaneLeft after Ctrl+1, got: %v", dp.ActivePane)
	}

	// Send Ctrl+2 (Alt+2 fallback)
	dp.handleInput([]byte("\x1b2"))
	if dp.ActivePane != PaneRight {
		t.Fatalf("expected PaneRight after Alt+2, got: %v", dp.ActivePane)
	}
}

func TestChatUICtrlShiftCCopy(t *testing.T) {
	cfg := &Config{Model: "gpt-4", APIKey: "test-key"}
	chat := NewChatUI(cfg, nil, QueryOptions{}, "", 50, 15)

	chat.Turns = append(chat.Turns, ChatTurn{Role: "assistant", Content: "copy test line"})
	chat.rebuildReversedLines()

	// Select text in history
	chat.Selecting = true
	chat.SelStartR = 0
	chat.SelStartC = 0
	chat.SelEndR = 0
	chat.SelEndC = 10

	// Send Ctrl+Shift+C (CSI u \x1b[67;6u)
	shouldExit := chat.HandleKey([]byte("\x1b[67;6u"))
	if shouldExit {
		t.Fatal("Ctrl+Shift+C should not trigger exit")
	}
}
