package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type ActivePane int

const (
	PaneLeft  ActivePane = 0 // AI Chatbot (-i mode)
	PaneRight ActivePane = 1 // Live Shell Terminal
)

type DualPaneWorkspace struct {
	mu            sync.Mutex
	Width         int
	Height        int
	ActivePane    ActivePane
	LeftLines     []string // formatted conversation history
	LeftScroll    int      // scroll offset for history
	InputBuffer   []rune   // user typing buffer for chat
	CursorPos     int      // cursor position in InputBuffer
	History       []string // prompt history for Up/Down recall
	HistIdx       int      // index in prompt history
	Session       *Session
	Config        *Config
	Opts          QueryOptions
	VTGrid        *VTGrid
	PTY           PTYSession
	IsStreaming   bool
	IsExiting     bool
	ActiveCancel  context.CancelFunc
}

func NewDualPaneWorkspace(cfg *Config, session *Session, opts QueryOptions, initialPrompt string) (*DualPaneWorkspace, error) {
	fd := os.Stdout.Fd()
	w, h := getTerminalSize(fd)
	if w < 60 {
		w = 80
	}
	if h < 12 {
		h = 24
	}

	leftWidth := int(float64(w) * 0.40)
	rightWidth := w - leftWidth - 1
	contentHeight := h - 3
	if contentHeight < 4 {
		contentHeight = 4
	}

	// Initialize PTY and Shell
	ptySession, err := StartPTY(contentHeight, rightWidth)
	if err != nil {
		return nil, fmt.Errorf("failed to start terminal PTY: %w", err)
	}

	grid := NewVTGrid(contentHeight, rightWidth)

	dp := &DualPaneWorkspace{
		Width:        w,
		Height:       h,
		ActivePane:   PaneLeft, // Starts focused on Left Pane (AI Chat)
		LeftLines:    []string{},
		LeftScroll:   0,
		InputBuffer:  []rune{},
		CursorPos:    0,
		History:      []string{},
		HistIdx:      -1,
		Session:      session,
		Config:       cfg,
		Opts:         opts,
		VTGrid:       grid,
		PTY:          ptySession,
	}

	// Populate left pane with prior session messages
	if session != nil && len(session.Messages) > 0 {
		for _, m := range session.Messages {
			dp.appendMessageToLeft(m.Role, m.Content)
		}
	}

	if initialPrompt != "" {
		dp.InputBuffer = []rune(initialPrompt)
		dp.CursorPos = len(dp.InputBuffer)
	}

	return dp, nil
}

// wrapText splits text into lines that fit within maxWidth columns
func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 4 {
		maxWidth = 4
	}

	var result []string
	rawLines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	for _, line := range rawLines {
		if line == "" {
			result = append(result, "")
			continue
		}

		runes := []rune(line)
		for len(runes) > 0 {
			if len(runes) <= maxWidth {
				result = append(result, string(runes))
				break
			}
			breakIdx := maxWidth
			foundSpace := false
			for i := maxWidth; i >= maxWidth/2; i-- {
				if runes[i] == ' ' || runes[i] == '\t' {
					breakIdx = i
					foundSpace = true
					break
				}
			}
			if foundSpace {
				result = append(result, string(runes[:breakIdx]))
				runes = []rune(strings.TrimLeft(string(runes[breakIdx:]), " \t"))
			} else {
				result = append(result, string(runes[:maxWidth]))
				runes = runes[maxWidth:]
			}
		}
	}
	return result
}

func (dp *DualPaneWorkspace) appendMessageToLeft(role, content string) {
	leftWidth := int(float64(dp.Width) * 0.40)
	contentWidth := leftWidth - 2
	if contentWidth < 4 {
		contentWidth = 4
	}

	var header string
	if role == "user" {
		header = "─── You ────────────────────────"
	} else {
		header = fmt.Sprintf("─── ask [%s] ───", dp.Config.Model)
	}
	if len(header) > contentWidth {
		header = header[:contentWidth]
	}

	dp.LeftLines = append(dp.LeftLines, header)
	wrapped := wrapText(content, contentWidth)
	dp.LeftLines = append(dp.LeftLines, wrapped...)
	dp.LeftLines = append(dp.LeftLines, "") // Blank spacing

	// Auto-scroll to bottom of conversation
	historyHeight := (dp.Height - 3) - 2
	if historyHeight < 1 {
		historyHeight = 1
	}
	if len(dp.LeftLines) > historyHeight {
		dp.LeftScroll = len(dp.LeftLines) - historyHeight
	} else {
		dp.LeftScroll = 0
	}
}

func (dp *DualPaneWorkspace) Render() {
	dp.mu.Lock()
	defer dp.mu.Unlock()

	if dp.IsExiting {
		return
	}

	w := dp.Width
	h := dp.Height
	leftWidth := int(float64(w) * 0.40)
	rightWidth := w - leftWidth - 1
	contentHeight := h - 3
	if contentHeight < 4 {
		contentHeight = 4
	}
	historyHeight := contentHeight - 2

	var b strings.Builder
	b.WriteString(ansiHideCursor)

	// 1. Draw Header Bar (Row 1)
	leftHeader := "  AI CHATBOT (-i mode)"
	if dp.ActivePane == PaneLeft {
		leftHeader = "● AI CHATBOT [ACTIVE]"
	}
	rightHeader := "  LIVE SHELL"
	if dp.ActivePane == PaneRight {
		rightHeader = "● LIVE SHELL [ACTIVE]"
	}

	leftHeaderPadded := fmt.Sprintf("%-*s", leftWidth, leftHeader)
	if len(leftHeaderPadded) > leftWidth {
		leftHeaderPadded = leftHeaderPadded[:leftWidth]
	}
	rightHeaderPadded := fmt.Sprintf("%-*s", rightWidth, rightHeader)
	if len(rightHeaderPadded) > rightWidth {
		rightHeaderPadded = rightHeaderPadded[:rightWidth]
	}

	b.WriteString("\x1b[1;1H\x1b[7m")
	b.WriteString(leftHeaderPadded)
	b.WriteString("│")
	b.WriteString(rightHeaderPadded)
	b.WriteString("\x1b[0m")

	// 2. Draw Separator Line (Row 2)
	b.WriteString("\x1b[2;1H")
	b.WriteString(strings.Repeat("─", leftWidth))
	b.WriteString("┼")
	b.WriteString(strings.Repeat("─", rightWidth))

	// 3. Draw Body Rows (Rows 3 to 3 + contentHeight - 1)
	for r := 0; r < contentHeight; r++ {
		rowANSI := 3 + r
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", rowANSI))

		// Left Column Content
		var leftStr string
		if r < historyHeight {
			// Conversation history
			lineIdx := dp.LeftScroll + r
			if lineIdx >= 0 && lineIdx < len(dp.LeftLines) {
				leftStr = dp.LeftLines[lineIdx]
			}
		} else if r == historyHeight {
			// Input divider
			leftStr = strings.Repeat("─", leftWidth)
		} else {
			// Input prompt line: ">>> ..."
			promptStr := ">>> " + string(dp.InputBuffer)
			leftRunes := []rune(promptStr)
			if len(leftRunes) > leftWidth {
				// Scroll horizontally to keep cursor in view
				offset := dp.CursorPos + 4 - leftWidth + 2
				if offset < 0 {
					offset = 0
				}
				if offset < len(leftRunes) {
					leftStr = string(leftRunes[offset:])
				}
			} else {
				leftStr = promptStr
			}
		}

		// Pad or clip leftStr to leftWidth
		leftRunes := []rune(leftStr)
		if len(leftRunes) > leftWidth {
			leftStr = string(leftRunes[:leftWidth])
		} else {
			leftStr = leftStr + strings.Repeat(" ", leftWidth-len(leftRunes))
		}
		b.WriteString(leftStr)

		// Divider
		b.WriteString("│")

		// Right Column Content: Rendered from VTGrid
		rightFormatted := dp.VTGrid.RenderRow(r)
		b.WriteString(rightFormatted)
	}

	// 4. Draw Status Footer (Row h)
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[7m", h))
	statusText := " [Ctrl+T] Switch Pane | [Ctrl+Enter] Send | [PgUp/Dn] Scroll History | [/exit / exit] Quit"
	statusPadded := fmt.Sprintf("%-*s", w, statusText)
	if len(statusPadded) > w {
		statusPadded = statusPadded[:w]
	}
	b.WriteString(statusPadded)
	b.WriteString("\x1b[0m")

	// 5. Place Cursor according to ActivePane
	if dp.ActivePane == PaneLeft {
		targetRow := 3 + (contentHeight - 1)
		inputVisualPos := 4 + dp.CursorPos
		if inputVisualPos >= leftWidth {
			inputVisualPos = leftWidth - 1
		}
		targetCol := 1 + inputVisualPos
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	} else {
		targetRow := 3 + dp.VTGrid.CursorRow
		targetCol := leftWidth + 2 + dp.VTGrid.CursorCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	}

	_, _ = os.Stdout.WriteString(b.String())
	_ = os.Stdout.Sync()
}

func (dp *DualPaneWorkspace) handleInputByte(b byte, rest []byte) (consumed int, shouldExit bool) {
	// Universal toggle key: Ctrl+T (0x14)
	if b == 0x14 {
		if dp.ActivePane == PaneLeft {
			dp.ActivePane = PaneRight
		} else {
			dp.ActivePane = PaneLeft
		}
		dp.Render()
		return 1, false
	}

	// When focused on Right Pane (Live Shell), send all bytes directly to PTY master
	if dp.ActivePane == PaneRight {
		all := append([]byte{b}, rest...)
		_, _ = dp.PTY.Write(all)
		return len(all), false
	}

	// When focused on Left Pane (AI Chat):
	// Check for Ctrl+C
	if b == 3 {
		if dp.IsStreaming && dp.ActiveCancel != nil {
			dp.ActiveCancel()
			dp.ActiveCancel = nil
			return 1, false
		}
		return 1, true
	}

	// Check for Ctrl+D (EOF on empty input -> exit)
	if b == 4 {
		if len(dp.InputBuffer) == 0 {
			return 1, true
		}
		return 1, false
	}

	// Check for Submit: Ctrl+Enter (LF=10 or CSI u \x1b[13;5u or \x1b[27;5;13~) or regular Enter (CR=13)
	if b == 10 || b == 13 {
		prompt := strings.TrimSpace(string(dp.InputBuffer))
		dp.InputBuffer = []rune{}
		dp.CursorPos = 0

		if prompt == "" {
			dp.Render()
			return 1, false
		}

		// Check slash commands
		if prompt == "/exit" || prompt == "/quit" || prompt == "/q" {
			return 1, true
		}
		if prompt == "/clear" || prompt == "/c" {
			dp.LeftLines = []string{}
			dp.LeftScroll = 0
			if dp.Session != nil {
				dp.Session.Messages = []Message{}
				_ = saveSession(dp.Session)
			}
			dp.Render()
			return 1, false
		}
		if prompt == "/help" || prompt == "/?" {
			dp.LeftLines = append(dp.LeftLines,
				"─── Help ───────────────────────",
				"Commands:",
				"  /exit, /q    Exit dual-pane workspace",
				"  /clear, /c   Clear conversation history",
				"  Ctrl+T       Toggle focus between Chat & Shell",
				"  Ctrl+Enter   Submit query to AI",
				"  PgUp / PgDn  Scroll history",
				"",
			)
			dp.Render()
			return 1, false
		}

		// Record in prompt history
		dp.History = append(dp.History, prompt)
		dp.HistIdx = len(dp.History)

		// Append user turn and trigger streaming
		dp.appendMessageToLeft("user", prompt)
		dp.Render()

		go dp.executeChatQuery(prompt)
		return 1, false
	}

	// Backspace
	if b == 127 || b == 8 {
		if dp.CursorPos > 0 {
			dp.InputBuffer = append(dp.InputBuffer[:dp.CursorPos-1], dp.InputBuffer[dp.CursorPos:]...)
			dp.CursorPos--
			dp.Render()
		}
		return 1, false
	}

	// Escape sequences: Arrow keys, PgUp, PgDn, CSI u Ctrl+Enter, etc.
	if b == 0x1b {
		fullSeq := string(append([]byte{b}, rest...))
		if strings.HasPrefix(fullSeq, "\x1b[13;5u") || strings.HasPrefix(fullSeq, "\x1b[27;5;13~") {
			prompt := strings.TrimSpace(string(dp.InputBuffer))
			dp.InputBuffer = []rune{}
			dp.CursorPos = 0
			if prompt != "" {
				if prompt == "/exit" || prompt == "/quit" || prompt == "/q" {
					return len(fullSeq), true
				}
				dp.History = append(dp.History, prompt)
				dp.HistIdx = len(dp.History)
				dp.appendMessageToLeft("user", prompt)
				dp.Render()
				go dp.executeChatQuery(prompt)
			}
			return len(fullSeq), false
		}
		if len(rest) >= 2 && rest[0] == '[' {
			switch rest[1] {
			case 'A': // Up arrow: previous prompt history
				if len(dp.History) > 0 {
					if dp.HistIdx > 0 {
						dp.HistIdx--
						dp.InputBuffer = []rune(dp.History[dp.HistIdx])
						dp.CursorPos = len(dp.InputBuffer)
						dp.Render()
					}
				}
				return 3, false
			case 'B': // Down arrow: next prompt history
				if len(dp.History) > 0 {
					if dp.HistIdx < len(dp.History)-1 {
						dp.HistIdx++
						dp.InputBuffer = []rune(dp.History[dp.HistIdx])
						dp.CursorPos = len(dp.InputBuffer)
						dp.Render()
					} else {
						dp.HistIdx = len(dp.History)
						dp.InputBuffer = []rune{}
						dp.CursorPos = 0
						dp.Render()
					}
				}
				return 3, false
			case 'C': // Right arrow
				if dp.CursorPos < len(dp.InputBuffer) {
					dp.CursorPos++
					dp.Render()
				}
				return 3, false
			case 'D': // Left arrow
				if dp.CursorPos > 0 {
					dp.CursorPos--
					dp.Render()
				}
				return 3, false
			case '5': // PgUp: scroll conversation history up
				if len(rest) >= 3 && rest[2] == '~' {
					dp.LeftScroll -= 5
					if dp.LeftScroll < 0 {
						dp.LeftScroll = 0
					}
					dp.Render()
					return 4, false
				}
			case '6': // PgDn: scroll conversation history down
				if len(rest) >= 3 && rest[2] == '~' {
					contentHeight := dp.Height - 3
					historyHeight := contentHeight - 2
					maxScroll := len(dp.LeftLines) - historyHeight
					if maxScroll < 0 {
						maxScroll = 0
					}
					dp.LeftScroll += 5
					if dp.LeftScroll > maxScroll {
						dp.LeftScroll = maxScroll
					}
					dp.Render()
					return 4, false
				}
			case 'H': // Home
				dp.CursorPos = 0
				dp.Render()
				return 3, false
			case 'F': // End
				dp.CursorPos = len(dp.InputBuffer)
				dp.Render()
				return 3, false
			case '3': // Delete
				if len(rest) >= 3 && rest[2] == '~' {
					if dp.CursorPos < len(dp.InputBuffer) {
						dp.InputBuffer = append(dp.InputBuffer[:dp.CursorPos], dp.InputBuffer[dp.CursorPos+1:]...)
						dp.Render()
					}
					return 4, false
				}
			}
		}
		return 1, false
	}

	// Printable UTF-8 characters
	all := append([]byte{b}, rest...)
	r, size := utf8.DecodeRune(all)
	if r != utf8.RuneError || size > 1 {
		dp.InputBuffer = append(dp.InputBuffer[:dp.CursorPos], append([]rune{r}, dp.InputBuffer[dp.CursorPos:]...)...)
		dp.CursorPos++
		dp.Render()
		return size, false
	}

	return 1, false
}

func (dp *DualPaneWorkspace) executeChatQuery(prompt string) {
	dp.mu.Lock()
	dp.IsStreaming = true

	var apiMessages []Message
	activePrompt := systemPrompt
	if dp.Opts.IsCodeGen {
		activePrompt = codePrompt
	}
	apiMessages = append(apiMessages, Message{Role: "system", Content: activePrompt})

	if len(dp.Session.Messages) > 0 {
		compacted := compactHistory(dp.Session.Messages)
		apiMessages = append(apiMessages, compacted...)
	}
	apiMessages = append(apiMessages, Message{Role: "user", Content: prompt})

	queryCtx, cancel := context.WithCancel(context.Background())
	dp.ActiveCancel = cancel

	leftWidth := int(float64(dp.Width) * 0.40)
	contentWidth := leftWidth - 2
	if contentWidth < 4 {
		contentWidth = 4
	}

	// Header for assistant response
	header := fmt.Sprintf("─── ask [%s] ───", dp.Config.Model)
	if len(header) > contentWidth {
		header = header[:contentWidth]
	}
	dp.LeftLines = append(dp.LeftLines, header)
	assistantLineStart := len(dp.LeftLines)
	dp.LeftLines = append(dp.LeftLines, "") // placeholder for response

	var accumulated strings.Builder
	dp.mu.Unlock()

	streamOpts := dp.Opts
	streamOpts.OnToken = func(tok string) {
		dp.mu.Lock()
		defer dp.mu.Unlock()

		accumulated.WriteString(tok)
		wrapped := wrapText(accumulated.String(), contentWidth)

		// Replace assistant lines
		newLeft := append([]string{}, dp.LeftLines[:assistantLineStart]...)
		newLeft = append(newLeft, wrapped...)
		dp.LeftLines = newLeft

		// Auto-scroll to keep latest tokens in view
		historyHeight := (dp.Height - 3) - 2
		if historyHeight < 1 {
			historyHeight = 1
		}
		if len(dp.LeftLines) > historyHeight {
			dp.LeftScroll = len(dp.LeftLines) - historyHeight
		}

		dp.Render()
	}

	response, queryErr := queryOpenRouter(queryCtx, dp.Config, apiMessages, streamOpts)

	dp.mu.Lock()
	dp.IsStreaming = false
	dp.ActiveCancel = nil
	if response != "" {
		dp.Session.Messages = append(dp.Session.Messages,
			Message{Role: "user", Content: prompt},
			Message{Role: "assistant", Content: response},
		)
		_ = saveSession(dp.Session)
		dp.LeftLines = append(dp.LeftLines, "") // blank line after assistant turn
	} else if queryErr != nil && queryErr != context.Canceled {
		dp.LeftLines = append(dp.LeftLines, fmt.Sprintf("[Error: %v]", queryErr), "")
	}
	dp.mu.Unlock()

	dp.Render()
}

func RunDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) error {
	fd := os.Stdout.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to initialize raw terminal: %w", err)
	}

	// Enter alternate screen buffer
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + ansiEnablePaste)
	_ = os.Stdout.Sync()

	sessionID := getSessionID()
	session, err := loadSession(sessionID)
	if err != nil {
		session = &Session{
			ID:       sessionID,
			Messages: []Message{},
		}
	}

	dp, err := NewDualPaneWorkspace(cfg, session, opts, initialPrompt)
	if err != nil {
		_ = restoreTerm(fd, state)
		_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiLeaveAltScreen + ansiShowCursor)
		_ = os.Stdout.Sync()
		return err
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			dp.mu.Lock()
			dp.IsExiting = true
			if dp.ActiveCancel != nil {
				dp.ActiveCancel()
			}
			dp.mu.Unlock()

			if dp.PTY != nil {
				_ = dp.PTY.Close()
			}
			_ = restoreTerm(fd, state)
			_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiLeaveAltScreen + ansiShowCursor)
			_ = os.Stdout.Sync()
		})
	}
	defer cleanup()

	// Initial render
	dp.Render()

	// Background reader for PTY shell output
	shellDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := dp.PTY.Read(buf)
			if n > 0 {
				dp.VTGrid.Write(buf[:n])
				dp.Render()
			}
			if err != nil {
				if err != io.EOF && !dp.IsExiting {
					// Shell closed or exited
				}
				break
			}
		}
		close(shellDone)
	}()

	// Terminal resize watcher (checks size periodically or upon input)
	resizeTicker := time.NewTicker(250 * time.Millisecond)
	defer resizeTicker.Stop()

	go func() {
		for range resizeTicker.C {
			if dp.IsExiting {
				return
			}
			w, h := getTerminalSize(fd)
			if w > 0 && h > 0 && (w != dp.Width || h != dp.Height) {
				dp.mu.Lock()
				dp.Width = w
				dp.Height = h
				leftWidth := int(float64(w) * 0.40)
				rightWidth := w - leftWidth - 1
				contentHeight := h - 3
				if contentHeight < 4 {
					contentHeight = 4
				}
				dp.VTGrid.Resize(contentHeight, rightWidth)
				_ = dp.PTY.Resize(contentHeight, rightWidth)
				dp.mu.Unlock()
				dp.Render()
			}
		}
	}()

	// Signal handling for clean exit on SIGINT/SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Async stdin reader loop
	inputChan := make(chan []byte, 32)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(inputChan)
				return
			}
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			inputChan <- chunk
		}
	}()

	for {
		select {
		case <-shellDone:
			// Shell exited (e.g. user typed `exit` in shell) -> cleanly close workspace immediately!
			return nil
		case <-sigChan:
			return nil
		case inBuf, ok := <-inputChan:
			if !ok {
				return nil
			}
			i := 0
			n := len(inBuf)
			for i < n {
				consumed, shouldExit := dp.handleInputByte(inBuf[i], inBuf[i+1:n])
				if shouldExit {
					return nil
				}
				i += consumed
			}
		}
	}
}
