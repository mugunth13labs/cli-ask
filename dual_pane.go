package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type ActivePane int

const (
	PaneLeft  ActivePane = 0 // AI Chatbot (Unified Document)
	PaneRight ActivePane = 1 // Live Shell Terminal
)

type DualPaneWorkspace struct {
	mu         sync.Mutex
	Width      int
	Height     int
	ActivePane ActivePane

	// Left Pane: Unified Chat History + Prompt Editor
	ChatHistory []string // formatted conversation lines
	PromptLines []string // active prompt lines (multiline)
	CursorLine  int      // cursor line in unified document
	CursorCol   int      // cursor column on CursorLine
	LeftScroll  int      // scroll offset (top line index)

	// Left selection state
	LeftSelecting bool
	LeftSelStartL int
	LeftSelStartC int
	LeftSelEndL   int
	LeftSelEndC   int

	// Right Pane: Shell PTY + VTGrid
	RightPTY       PTYSession
	RightVT        *VTGrid
	RightSelecting bool

	// Session & streaming
	Session      *Session
	Config       *Config
	Opts         QueryOptions
	IsStreaming  bool
	ActiveCancel context.CancelFunc

	IsExiting bool
}

func NewDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) (*DualPaneWorkspace, error) {
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
	contentHeight := h - 2 // Row 1: Tab Pills, Row 2: Divider Line
	if contentHeight < 4 {
		contentHeight = 4
	}

	// 1. Start shell PTY for Right Pane
	rightPTY, err := StartPTY(contentHeight, rightWidth)
	if err != nil {
		return nil, fmt.Errorf("failed to start shell PTY: %w", err)
	}

	rightVT := NewVTGrid(contentHeight, rightWidth)

	// 2. Load session
	sessionID := getSessionID()
	session, err := loadSession(sessionID)
	if err != nil {
		session = &Session{
			ID:       sessionID,
			Messages: []Message{},
		}
	}

	dp := &DualPaneWorkspace{
		Width:       w,
		Height:      h,
		ActivePane:  PaneLeft,
		ChatHistory: []string{},
		PromptLines: []string{""},
		RightPTY:    rightPTY,
		RightVT:     rightVT,
		Session:     session,
		Config:      cfg,
		Opts:        opts,
	}

	// Header banner in chat history
	dp.ChatHistory = append(dp.ChatHistory,
		fmt.Sprintf("ask v%s — Continuous Chat Mode", version),
		fmt.Sprintf("Model: %s | Session: %s", cfg.Model, sessionID),
		"Send: Ctrl+Enter | Newline: Enter | Exit: /exit | Switch: Ctrl+T",
		"",
	)

	// Populate prior session messages
	if session != nil && len(session.Messages) > 0 {
		for _, m := range session.Messages {
			dp.appendMessageToHistory(m.Role, m.Content)
		}
	}

	if initialPrompt != "" {
		dp.PromptLines = strings.Split(strings.ReplaceAll(initialPrompt, "\r\n", "\n"), "\n")
	}

	// Place cursor at prompt line
	dp.CursorLine = dp.promptStartLine()
	dp.CursorCol = len(dp.PromptLines[0])
	dp.adjustLeftScroll()

	return dp, nil
}

func (dp *DualPaneWorkspace) promptStartLine() int {
	return len(dp.ChatHistory)
}

func (dp *DualPaneWorkspace) appendMessageToHistory(role, content string) {
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

	dp.ChatHistory = append(dp.ChatHistory, header)
	wrapped := wrapText(content, contentWidth)
	dp.ChatHistory = append(dp.ChatHistory, wrapped...)
	dp.ChatHistory = append(dp.ChatHistory, "") // Blank spacing
}

// allLeftLines returns the rendered lines of the Left Pane
func (dp *DualPaneWorkspace) allLeftLines() []string {
	lines := make([]string, len(dp.ChatHistory))
	copy(lines, dp.ChatHistory)

	for i, pl := range dp.PromptLines {
		prefix := ">>> "
		if i > 0 {
			prefix = "... "
		}
		lines = append(lines, prefix+pl)
	}
	return lines
}

func (dp *DualPaneWorkspace) adjustLeftScroll() {
	contentHeight := dp.Height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	allCount := len(dp.allLeftLines())

	// Ensure cursor is visible
	if dp.CursorLine < dp.LeftScroll {
		dp.LeftScroll = dp.CursorLine
	} else if dp.CursorLine >= dp.LeftScroll+contentHeight {
		dp.LeftScroll = dp.CursorLine - contentHeight + 1
	}

	maxScroll := allCount - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if dp.LeftScroll > maxScroll {
		dp.LeftScroll = maxScroll
	}
	if dp.LeftScroll < 0 {
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
	contentHeight := h - 2
	if contentHeight < 4 {
		contentHeight = 4
	}

	var b strings.Builder
	b.WriteString(ansiHideCursor)

	// 1. Draw Tab Pills Bar (Row 1)
	leftPill := "  [ 1: ask -i (Chat) ]  "
	if dp.ActivePane == PaneLeft {
		leftPill = "\x1b[7m\x1b[1m [ 1: ask -i (Chat) ] \x1b[0m"
	}
	rightPill := "  [ 2: Terminal ]  "
	if dp.ActivePane == PaneRight {
		rightPill = "\x1b[7m\x1b[1m [ 2: Terminal ] \x1b[0m"
	}

	hint := "Ctrl+T: Switch | PgUp/Dn: Scroll | Mouse: Select & Copy "
	tabsLine := leftPill + " │ " + rightPill
	visibleTabLen := 24 + 3 + 19
	remaining := w - visibleTabLen - len(hint)
	if remaining < 0 {
		remaining = 0
	}
	headerRow := tabsLine + strings.Repeat(" ", remaining) + "\x1b[90m" + hint + "\x1b[0m"

	b.WriteString("\x1b[1;1H\x1b[K")
	b.WriteString(headerRow)

	// 2. Draw Separator Line (Row 2)
	b.WriteString("\x1b[2;1H\x1b[K")
	b.WriteString(strings.Repeat("─", leftWidth))
	b.WriteString("┼")
	b.WriteString(strings.Repeat("─", rightWidth))

	// 3. Draw Body Rows (Rows 3 to 3 + contentHeight - 1)
	allLines := dp.allLeftLines()

	for r := 0; r < contentHeight; r++ {
		rowANSI := 3 + r
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", rowANSI))

		// Left Column Content
		docLineIdx := dp.LeftScroll + r
		var leftFormatted string
		if docLineIdx >= 0 && docLineIdx < len(allLines) {
			rawText := allLines[docLineIdx]
			runes := []rune(rawText)

			var lineBuilder strings.Builder
			for c := 0; c < leftWidth; c++ {
				var char rune = ' '
				if c < len(runes) {
					char = runes[c]
				}

				if dp.isLeftSelected(docLineIdx, c) {
					lineBuilder.WriteString("\x1b[7m")
					lineBuilder.WriteRune(char)
					lineBuilder.WriteString("\x1b[0m")
				} else {
					lineBuilder.WriteRune(char)
				}
			}
			leftFormatted = lineBuilder.String()
		} else {
			leftFormatted = strings.Repeat(" ", leftWidth)
		}
		b.WriteString(leftFormatted)

		// Divider
		b.WriteString("│")

		// Right Column Content: rendered from RightVT
		rightFormatted := dp.RightVT.RenderRow(r)
		b.WriteString(rightFormatted)
	}

	// 4. Place Cursor based on ActivePane
	if dp.ActivePane == PaneLeft {
		targetRow := 3 + (dp.CursorLine - dp.LeftScroll)
		displayCol := dp.CursorCol
		if dp.CursorLine >= dp.promptStartLine() {
			displayCol += 4 // add prompt prefix ">>> " or "... "
		}
		if displayCol >= leftWidth {
			displayCol = leftWidth - 1
		}
		targetCol := 1 + displayCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	} else {
		targetRow := 3 + dp.RightVT.CursorRow
		targetCol := leftWidth + 2 + dp.RightVT.CursorCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	}

	_, _ = os.Stdout.WriteString(b.String())
	_ = os.Stdout.Sync()
}

func (dp *DualPaneWorkspace) isLeftSelected(line, col int) bool {
	if !dp.LeftSelecting {
		return false
	}
	sL, sC := dp.LeftSelStartL, dp.LeftSelStartC
	eL, eC := dp.LeftSelEndL, dp.LeftSelEndC
	if sL > eL || (sL == eL && sC > eC) {
		sL, eL = eL, sL
		sC, eC = eC, sC
	}

	if line < sL || line > eL {
		return false
	}
	if line == sL && line == eL {
		return col >= sC && col <= eC
	}
	if line == sL {
		return col >= sC
	}
	if line == eL {
		return col <= eC
	}
	return true
}

func (dp *DualPaneWorkspace) getLeftSelectedText() string {
	if !dp.LeftSelecting {
		return ""
	}
	sL, sC := dp.LeftSelStartL, dp.LeftSelStartC
	eL, eC := dp.LeftSelEndL, dp.LeftSelEndC
	if sL > eL || (sL == eL && sC > eC) {
		sL, eL = eL, sL
		sC, eC = eC, sC
	}

	allLines := dp.allLeftLines()
	var sb strings.Builder

	for l := sL; l <= eL; l++ {
		if l < 0 || l >= len(allLines) {
			continue
		}
		runes := []rune(allLines[l])
		start := 0
		if l == sL {
			start = sC
		}
		end := len(runes) - 1
		if l == eL {
			end = eC
		}
		if start < 0 {
			start = 0
		}
		if end >= len(runes) {
			end = len(runes) - 1
		}

		if start <= end && start < len(runes) {
			sb.WriteString(string(runes[start : end+1]))
		}
		if l < eL {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (dp *DualPaneWorkspace) handleInput(inBuf []byte) bool {
	// 1. Universal Pane Toggle: Ctrl+T (0x14)
	if bytes.Contains(inBuf, []byte{0x14}) {
		dp.mu.Lock()
		if dp.ActivePane == PaneLeft {
			dp.ActivePane = PaneRight
		} else {
			dp.ActivePane = PaneLeft
		}
		dp.mu.Unlock()
		dp.Render()

		var filtered []byte
		for _, b := range inBuf {
			if b != 0x14 {
				filtered = append(filtered, b)
			}
		}
		inBuf = filtered
		if len(inBuf) == 0 {
			return false
		}
	}

	// 2. Mouse SGR Events: \x1b[<btn;x;yM (press/drag) or \x1b[<btn;x;ym (release)
	if bytes.HasPrefix(inBuf, []byte("\x1b[<")) {
		s := string(inBuf)
		if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m") {
			isRelease := strings.HasSuffix(s, "m")
			raw := s[3 : len(s)-1]
			parts := strings.Split(raw, ";")
			if len(parts) == 3 {
				btn, _ := strconv.Atoi(parts[0])
				x, _ := strconv.Atoi(parts[1])
				y, _ := strconv.Atoi(parts[2])

				leftWidth := int(float64(dp.Width) * 0.40)
				row := y - 3 // 0-based content row

				// Mouse Wheel
				if btn == 64 { // Wheel Up
					if x <= leftWidth {
						dp.LeftScroll -= 3
						if dp.LeftScroll < 0 {
							dp.LeftScroll = 0
						}
					} else {
						dp.RightVT.ScrollPageUp(3)
					}
					dp.Render()
					return false
				} else if btn == 65 { // Wheel Down
					if x <= leftWidth {
						contentHeight := dp.Height - 2
						maxScroll := len(dp.allLeftLines()) - contentHeight
						if maxScroll < 0 {
							maxScroll = 0
						}
						dp.LeftScroll += 3
						if dp.LeftScroll > maxScroll {
							dp.LeftScroll = maxScroll
						}
					} else {
						dp.RightVT.ScrollPageDown(3)
					}
					dp.Render()
					return false
				}

				// Left Mouse Press (btn == 0, M)
				if btn == 0 && !isRelease {
					if x <= leftWidth { // Click in Left Pane
						dp.ActivePane = PaneLeft
						dp.RightSelecting = false
						dp.RightVT.ClearSelection()

						docLine := dp.LeftScroll + row
						allLines := dp.allLeftLines()
						if docLine >= 0 && docLine < len(allLines) {
							dp.LeftSelecting = true
							dp.LeftSelStartL = docLine
							dp.LeftSelStartC = x - 1
							dp.LeftSelEndL = docLine
							dp.LeftSelEndC = x - 1

							dp.CursorLine = docLine
							promptStart := dp.promptStartLine()
							if docLine >= promptStart {
								col := x - 1 - 4 // account for ">>> "
								if col < 0 {
									col = 0
								}
								pIdx := docLine - promptStart
								if col > len(dp.PromptLines[pIdx]) {
									col = len(dp.PromptLines[pIdx])
								}
								dp.CursorCol = col
							} else {
								dp.CursorCol = x - 1
							}
						}
					} else if x > leftWidth+1 { // Click in Right Pane
						dp.ActivePane = PaneRight
						dp.LeftSelecting = false

						dp.RightSelecting = true
						dp.RightVT.HasSelection = true
						rCol := x - leftWidth - 2
						dp.RightVT.SelStartRow = row
						dp.RightVT.SelStartCol = rCol
						dp.RightVT.SelEndRow = row
						dp.RightVT.SelEndCol = rCol
					}
					dp.Render()
					return false
				}

				// Left Mouse Drag (btn == 32, M)
				if btn == 32 && !isRelease {
					if dp.LeftSelecting {
						// Clamp within Left Pane bounds [1, leftWidth]
						clampedX := x
						if clampedX < 1 {
							clampedX = 1
						}
						if clampedX > leftWidth {
							clampedX = leftWidth
						}
						docLine := dp.LeftScroll + row
						if docLine < 0 {
							docLine = 0
						}
						allLines := dp.allLeftLines()
						if docLine >= len(allLines) {
							docLine = len(allLines) - 1
						}
						dp.LeftSelEndL = docLine
						dp.LeftSelEndC = clampedX - 1
						dp.Render()
						return false
					} else if dp.RightSelecting {
						// Clamp within Right Pane bounds [leftWidth + 2, Width]
						clampedX := x
						if clampedX < leftWidth+2 {
							clampedX = leftWidth + 2
						}
						if clampedX > dp.Width {
							clampedX = dp.Width
						}
						rCol := clampedX - leftWidth - 2
						dp.RightVT.SelEndRow = row
						dp.RightVT.SelEndCol = rCol
						dp.Render()
						return false
					}
				}

				// Mouse Release (isRelease)
				if isRelease {
					if dp.LeftSelecting {
						selected := dp.getLeftSelectedText()
						if selected != "" {
							copyToClipboard(selected)
						}
					} else if dp.RightSelecting {
						selected := dp.RightVT.GetSelectedText()
						if selected != "" {
							copyToClipboard(selected)
						}
					}
					// Keep visual selection intact so user sees what was copied
					return false
				}
			}
		}
	}

	// 3. When focused on Right Pane (Terminal), forward all input directly to PTY!
	if dp.ActivePane == PaneRight {
		_, _ = dp.RightPTY.Write(inBuf)
		return false
	}

	// 4. When focused on Left Pane (AI Chat): Unified Buffer Editing & Navigation
	dp.handleLeftInput(inBuf)
	return false
}

func (dp *DualPaneWorkspace) handleLeftInput(inBuf []byte) {
	promptStart := dp.promptStartLine()
	inPrompt := dp.CursorLine >= promptStart
	promptIdx := dp.CursorLine - promptStart

	// Check PgUp / PgDn
	if bytes.Equal(inBuf, []byte("\x1b[5~")) {
		dp.LeftScroll -= 5
		if dp.LeftScroll < 0 {
			dp.LeftScroll = 0
		}
		dp.Render()
		return
	}
	if bytes.Equal(inBuf, []byte("\x1b[6~")) {
		contentHeight := dp.Height - 2
		maxScroll := len(dp.allLeftLines()) - contentHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		dp.LeftScroll += 5
		if dp.LeftScroll > maxScroll {
			dp.LeftScroll = maxScroll
		}
		dp.Render()
		return
	}

	// Check Shift+Arrows (Text Selection in Left Document)
	if bytes.HasPrefix(inBuf, []byte("\x1b[1;2")) || bytes.HasPrefix(inBuf, []byte("\x1b[1;6")) {
		dir := inBuf[len(inBuf)-1]
		if !dp.LeftSelecting {
			dp.LeftSelecting = true
			dp.LeftSelStartL = dp.CursorLine
			dp.LeftSelStartC = dp.CursorCol
		}
		switch dir {
		case 'A': // Up
			if dp.CursorLine > 0 {
				dp.CursorLine--
			}
		case 'B': // Down
			if dp.CursorLine < len(dp.allLeftLines())-1 {
				dp.CursorLine++
			}
		case 'C': // Right
			dp.CursorCol++
		case 'D': // Left
			if dp.CursorCol > 0 {
				dp.CursorCol--
			}
		}
		dp.LeftSelEndL = dp.CursorLine
		dp.LeftSelEndC = dp.CursorCol
		dp.adjustLeftScroll()
		dp.Render()
		return
	}

	// Check Ctrl+C: Copy selected text if selection active, else cancel query
	if len(inBuf) == 1 && inBuf[0] == 3 {
		if dp.LeftSelecting {
			selected := dp.getLeftSelectedText()
			if selected != "" {
				copyToClipboard(selected)
			}
			dp.LeftSelecting = false
			dp.Render()
			return
		}
		if dp.IsStreaming && dp.ActiveCancel != nil {
			dp.ActiveCancel()
			dp.ActiveCancel = nil
			return
		}
		return
	}

	// Check Arrow Keys (Up, Down, Left, Right)
	if bytes.Equal(inBuf, []byte("\x1b[A")) { // Up Arrow
		dp.LeftSelecting = false
		if dp.CursorLine > 0 {
			dp.CursorLine--
			dp.clampCol()
			dp.adjustLeftScroll()
			dp.Render()
		}
		return
	}
	if bytes.Equal(inBuf, []byte("\x1b[B")) { // Down Arrow
		dp.LeftSelecting = false
		if dp.CursorLine < len(dp.allLeftLines())-1 {
			dp.CursorLine++
			dp.clampCol()
			dp.adjustLeftScroll()
			dp.Render()
		}
		return
	}
	if bytes.Equal(inBuf, []byte("\x1b[D")) { // Left Arrow
		dp.LeftSelecting = false
		if dp.CursorCol > 0 {
			dp.CursorCol--
			dp.Render()
		}
		return
	}
	if bytes.Equal(inBuf, []byte("\x1b[C")) { // Right Arrow
		dp.LeftSelecting = false
		lineLen := dp.currentLineLen()
		if dp.CursorCol < lineLen {
			dp.CursorCol++
			dp.Render()
		}
		return
	}
	if bytes.Equal(inBuf, []byte("\x1b[H")) { // Home
		dp.CursorCol = 0
		dp.Render()
		return
	}
	if bytes.Equal(inBuf, []byte("\x1b[F")) { // End
		dp.CursorCol = dp.currentLineLen()
		dp.Render()
		return
	}

	// Check Submit: Ctrl+Enter (LF=10, or CSI u \x1b[13;5u / \x1b[27;5;13~)
	isCtrlEnter := (len(inBuf) == 1 && inBuf[0] == 10) ||
		bytes.Equal(inBuf, []byte("\x1b[13;5u")) ||
		bytes.Equal(inBuf, []byte("\x1b[27;5;13~"))

	if isCtrlEnter && inPrompt {
		fullPrompt := strings.TrimSpace(strings.Join(dp.PromptLines, "\n"))
		if fullPrompt == "" {
			return
		}

		// Check slash commands
		if fullPrompt == "/exit" || fullPrompt == "/quit" || fullPrompt == "/q" {
			dp.IsExiting = true
			if dp.RightPTY != nil {
				_ = dp.RightPTY.Close()
			}
			return
		}
		if fullPrompt == "/clear" || fullPrompt == "/c" {
			dp.ChatHistory = []string{}
			dp.PromptLines = []string{""}
			dp.CursorLine = 0
			dp.CursorCol = 0
			dp.LeftScroll = 0
			if dp.Session != nil {
				dp.Session.Messages = []Message{}
				_ = saveSession(dp.Session)
			}
			dp.Render()
			return
		}
		if fullPrompt == "/help" || fullPrompt == "/?" {
			dp.appendMessageToHistory("assistant", "Commands:\n  /exit, /q    Exit dual-pane workspace\n  /clear, /c   Clear conversation history\n  Ctrl+T       Toggle focus between Chat & Terminal\n  Enter        Newline in prompt\n  Ctrl+Enter   Send prompt to AI\n  PgUp/Dn      Scroll history\n  Mouse Drag   Select & Copy text")
			dp.PromptLines = []string{""}
			dp.CursorLine = dp.promptStartLine()
			dp.CursorCol = 0
			dp.adjustLeftScroll()
			dp.Render()
			return
		}

		// Execute chat query
		dp.PromptLines = []string{""}
		dp.appendMessageToHistory("user", fullPrompt)
		dp.CursorLine = dp.promptStartLine()
		dp.CursorCol = 0
		dp.adjustLeftScroll()
		dp.Render()

		go dp.executeChatQuery(fullPrompt)
		return
	}

	// If cursor is navigated up in history, typing characters is prevented until user navigates to >>> line
	if !inPrompt {
		return
	}

	// Editing inside prompt:
	// Enter key (13): Newline in prompt!
	if len(inBuf) == 1 && inBuf[0] == 13 {
		curLine := dp.PromptLines[promptIdx]
		runes := []rune(curLine)
		before := string(runes[:dp.CursorCol])
		after := string(runes[dp.CursorCol:])

		dp.PromptLines[promptIdx] = before
		dp.PromptLines = append(dp.PromptLines[:promptIdx+1], append([]string{after}, dp.PromptLines[promptIdx+1:]...)...)
		dp.CursorLine++
		dp.CursorCol = 0
		dp.adjustLeftScroll()
		dp.Render()
		return
	}

	// Backspace (127 or 8)
	if len(inBuf) == 1 && (inBuf[0] == 127 || inBuf[0] == 8) {
		curLine := dp.PromptLines[promptIdx]
		runes := []rune(curLine)

		if dp.CursorCol > 0 {
			dp.PromptLines[promptIdx] = string(append(runes[:dp.CursorCol-1], runes[dp.CursorCol:]...))
			dp.CursorCol--
		} else if promptIdx > 0 {
			// Merge with previous line
			prevLine := dp.PromptLines[promptIdx-1]
			prevRunes := []rune(prevLine)
			dp.CursorCol = len(prevRunes)
			dp.PromptLines[promptIdx-1] = prevLine + curLine
			dp.PromptLines = append(dp.PromptLines[:promptIdx], dp.PromptLines[promptIdx+1:]...)
			dp.CursorLine--
		}
		dp.adjustLeftScroll()
		dp.Render()
		return
	}

	// Printable characters / text insertion
	i := 0
	for i < len(inBuf) {
		r, size := utf8.DecodeRune(inBuf[i:])
		if r != utf8.RuneError && r >= 32 {
			curLine := dp.PromptLines[promptIdx]
			runes := []rune(curLine)
			if dp.CursorCol > len(runes) {
				dp.CursorCol = len(runes)
			}
			dp.PromptLines[promptIdx] = string(append(runes[:dp.CursorCol], append([]rune{r}, runes[dp.CursorCol:]...)...))
			dp.CursorCol++
		}
		i += size
	}
	dp.adjustLeftScroll()
	dp.Render()
}

func (dp *DualPaneWorkspace) currentLineLen() int {
	promptStart := dp.promptStartLine()
	if dp.CursorLine >= promptStart {
		pIdx := dp.CursorLine - promptStart
		if pIdx < len(dp.PromptLines) {
			return len([]rune(dp.PromptLines[pIdx]))
		}
	} else if dp.CursorLine < len(dp.ChatHistory) {
		return len([]rune(dp.ChatHistory[dp.CursorLine]))
	}
	return 0
}

func (dp *DualPaneWorkspace) clampCol() {
	maxCol := dp.currentLineLen()
	if dp.CursorCol > maxCol {
		dp.CursorCol = maxCol
	}
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

	header := fmt.Sprintf("─── ask [%s] ───", dp.Config.Model)
	if len(header) > contentWidth {
		header = header[:contentWidth]
	}
	dp.ChatHistory = append(dp.ChatHistory, header)
	assistantStartIdx := len(dp.ChatHistory)
	dp.ChatHistory = append(dp.ChatHistory, "")

	var accumulated strings.Builder
	dp.mu.Unlock()

	streamOpts := dp.Opts
	streamOpts.OnToken = func(tok string) {
		dp.mu.Lock()
		defer dp.mu.Unlock()

		accumulated.WriteString(tok)
		wrapped := wrapText(accumulated.String(), contentWidth)

		newHist := append([]string{}, dp.ChatHistory[:assistantStartIdx]...)
		newHist = append(newHist, wrapped...)
		dp.ChatHistory = newHist

		dp.CursorLine = dp.promptStartLine()
		dp.adjustLeftScroll()
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
		dp.ChatHistory = append(dp.ChatHistory, "") // blank spacing
	} else if queryErr != nil && queryErr != context.Canceled {
		dp.ChatHistory = append(dp.ChatHistory, fmt.Sprintf("[Error: %v]", queryErr), "")
	}

	dp.CursorLine = dp.promptStartLine()
	dp.CursorCol = 0
	dp.adjustLeftScroll()
	dp.mu.Unlock()

	dp.Render()
}

func RunDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) error {
	fd := os.Stdout.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to initialize raw terminal: %w", err)
	}

	// Enter alternate screen & enable SGR mouse tracking for pane-bounded drag selection
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + "\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	_ = os.Stdout.Sync()

	dp, err := NewDualPaneWorkspace(cfg, initialPrompt, opts)
	if err != nil {
		_ = restoreTerm(fd, state)
		_, _ = os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l" + ansiLeaveAltScreen + ansiShowCursor)
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

			if dp.RightPTY != nil {
				_ = dp.RightPTY.Close()
			}
			_ = restoreTerm(fd, state)
			_, _ = os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l" + ansiLeaveAltScreen + ansiShowCursor)
			_ = os.Stdout.Sync()
		})
	}
	defer cleanup()

	// Initial render
	dp.Render()

	// Background reader for Right PTY (Shell)
	rightDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := dp.RightPTY.Read(buf)
			if n > 0 {
				dp.RightVT.Write(buf[:n])
				dp.Render()
			}
			if err != nil {
				break
			}
		}
		close(rightDone)
	}()

	// Terminal resize watcher
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
				contentHeight := h - 2
				if contentHeight < 4 {
					contentHeight = 4
				}
				dp.RightVT.Resize(contentHeight, rightWidth)
				_ = dp.RightPTY.Resize(contentHeight, rightWidth)
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
		case <-rightDone:
			// Shell exited (e.g. user typed 'exit' in terminal) -> cleanly close workspace!
			return nil
		case <-sigChan:
			return nil
		case inBuf, ok := <-inputChan:
			if !ok {
				return nil
			}
			if dp.IsExiting {
				return nil
			}
			if shouldExit := dp.handleInput(inBuf); shouldExit || dp.IsExiting {
				return nil
			}
		}
	}
}
