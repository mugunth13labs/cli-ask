package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ActivePane int

const (
	PaneLeft  ActivePane = 0 // ask chat (ChatUI)
	PaneRight ActivePane = 1 // Live Shell Terminal
)

type DualPaneWorkspace struct {
	mu         sync.Mutex
	Width      int
	Height     int
	ActivePane ActivePane

	// Left Pane: ChatUI with top expanding prompt & reversed history
	Chat *ChatUI

	// Right Pane: Shell PTY + VTGrid
	RightPTY       PTYSession
	RightVT        *VTGrid
	RightSelecting bool

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

	leftWidth := (w - 1) / 2
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

	chat := NewChatUI(cfg, session, opts, initialPrompt, leftWidth, contentHeight)

	dp := &DualPaneWorkspace{
		Width:      w,
		Height:     h,
		ActivePane: PaneLeft,
		Chat:       chat,
		RightPTY:   rightPTY,
		RightVT:    rightVT,
	}

	chat.OnNeedRender = func() {
		dp.Render()
	}

	return dp, nil
}

func (dp *DualPaneWorkspace) Render() {
	dp.mu.Lock()
	defer dp.mu.Unlock()

	if dp.IsExiting {
		return
	}

	w := dp.Width
	h := dp.Height
	leftWidth := (w - 1) / 2
	rightWidth := w - leftWidth - 1
	contentHeight := h - 2
	if contentHeight < 4 {
		contentHeight = 4
	}

	var b strings.Builder
	b.WriteString(ansiHideCursor)

	// 1. Draw Inverted 50/50 Tab Bar (Row 1)
	var leftTab string
	tabText := " [1] ask chat"
	tabRunes := len([]rune(tabText))
	remLeft := leftWidth - tabRunes
	if remLeft < 0 {
		remLeft = 0
	}
	if dp.ActivePane == PaneLeft {
		// Active ask chat: Full reverse video across leftWidth
		leftTab = "\x1b[7;1m" + tabText + strings.Repeat(" ", remLeft) + "\x1b[0m"
	} else {
		// Inactive ask chat: Dimmed
		leftTab = "\x1b[90m" + tabText + strings.Repeat(" ", remLeft) + "\x1b[0m"
	}

	divider := "\x1b[90m│\x1b[0m"

	var rightTab string
	hint := "Ctrl+W | y/Ctrl+X "
	if rightWidth >= 55 {
		hint = "Ctrl+W: Switch | y/Ctrl+X: Copy "
	}
	title := " [2] Terminal"
	titleRunes := len([]rune(title))
	remRight := rightWidth - titleRunes - len(hint)
	if remRight < 1 {
		hint = "Ctrl+W "
		remRight = rightWidth - titleRunes - len(hint)
	}
	if remRight < 0 {
		remRight = 0
		hint = ""
	}

	if dp.ActivePane == PaneRight {
		// Active Terminal: Full reverse video across rightWidth
		rightTab = "\x1b[7;1m" + title + strings.Repeat(" ", remRight) + "\x1b[2m" + hint + "\x1b[0m"
	} else {
		// Inactive Terminal: Dimmed
		rightTab = "\x1b[90m" + title + strings.Repeat(" ", remRight) + hint + "\x1b[0m"
	}

	headerRow := leftTab + divider + rightTab

	b.WriteString("\x1b[1;1H\x1b[K")
	b.WriteString(headerRow)

	// 2. Draw Separator Line (Row 2)
	b.WriteString("\x1b[2;1H\x1b[K")
	b.WriteString(strings.Repeat("─", leftWidth))
	b.WriteString("┼")
	b.WriteString(strings.Repeat("─", rightWidth))

	// 3. Draw Body Rows (Rows 3 to 3 + contentHeight - 1)
	for r := 0; r < contentHeight; r++ {
		rowANSI := 3 + r
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", rowANSI))

		// Left Column Content: rendered from ChatUI
		leftFormatted := dp.Chat.RenderRow(r)
		b.WriteString(leftFormatted)

		// Divider
		b.WriteString("│")

		// Right Column Content: rendered from RightVT
		rightFormatted := dp.RightVT.RenderRow(r)
		b.WriteString(rightFormatted)
	}

	// 4. Place Cursor based on ActivePane
	if dp.ActivePane == PaneLeft {
		cRow, cCol := dp.Chat.CursorPosition()
		targetRow := 3 + cRow
		targetCol := 1 + cCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	} else {
		targetRow := 3 + dp.RightVT.CursorRow
		targetCol := leftWidth + 2 + dp.RightVT.CursorCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	}

	_, _ = os.Stdout.WriteString(b.String())
	_ = os.Stdout.Sync()
}

func (dp *DualPaneWorkspace) handleInput(inBuf []byte) bool {
	// 1. Pane Tab Switching:
	// Ctrl+W (0x17) window/pane cycle
	if bytes.Contains(inBuf, []byte{0x17}) {
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
			if b != 0x17 {
				filtered = append(filtered, b)
			}
		}
		inBuf = filtered
		if len(inBuf) == 0 {
			return false
		}
	}

	// Ctrl+1: Focus PaneLeft (ask chat)
	isCtrl1 := bytes.Equal(inBuf, []byte("\x1b[49;5u")) ||
		bytes.Equal(inBuf, []byte("\x1b[27;5;49~")) ||
		bytes.Equal(inBuf, []byte("\x1b1")) // Alt+1 fallback

	// Ctrl+2: Focus PaneRight (Terminal)
	isCtrl2 := bytes.Equal(inBuf, []byte("\x1b[50;5u")) ||
		bytes.Equal(inBuf, []byte("\x1b[27;5;50~")) ||
		(len(inBuf) == 1 && inBuf[0] == 0) || // Ctrl+2 classic NUL
		bytes.Equal(inBuf, []byte("\x1b2")) // Alt+2 fallback

	if isCtrl1 {
		dp.mu.Lock()
		dp.ActivePane = PaneLeft
		dp.mu.Unlock()
		dp.Render()
		return false
	}
	if isCtrl2 {
		dp.mu.Lock()
		dp.ActivePane = PaneRight
		dp.mu.Unlock()
		dp.Render()
		return false
	}

	// 1a. Universal Pane Toggle: Ctrl+T (0x14)
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

	// 1b. Universal Copy: Ctrl+X (0x18) or Ctrl+Shift+C
	isCopy := bytes.Contains(inBuf, []byte{0x18}) ||
		bytes.Equal(inBuf, []byte("\x1b[67;6u")) ||
		bytes.Equal(inBuf, []byte("\x1b[99;6u")) ||
		bytes.Equal(inBuf, []byte("\x1b[27;6;67~")) ||
		bytes.Equal(inBuf, []byte("\x1b[27;6;99~"))

	if isCopy {
		if dp.ActivePane == PaneLeft && dp.Chat.Selecting {
			selected := dp.Chat.GetSelectedText()
			if selected != "" {
				copyToClipboard(selected)
			}
			return false
		} else if dp.ActivePane == PaneRight && dp.RightVT.HasSelection {
			selected := dp.RightVT.GetSelectedText()
			if selected != "" {
				copyToClipboard(selected)
			}
			return false
		}
	}

	// 2. Mouse SGR Events: \x1b[<btn;x;yM / \x1b[<btn;x;ym
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

				leftWidth := (dp.Width - 1) / 2
				row := y - 3 // 0-based content row

				// Click on Tab Bar (Row 1)
				if y == 1 && btn == 0 && !isRelease {
					if x <= leftWidth {
						dp.ActivePane = PaneLeft
					} else if x > leftWidth+1 {
						dp.ActivePane = PaneRight
					}
					dp.Render()
					return false
				}

				// Mouse Wheel
				if btn == 64 { // Wheel Up
					if x <= leftWidth {
						dp.Chat.ScrollHistory(-3)
					} else {
						dp.RightVT.ScrollPageUp(3)
					}
					dp.Render()
					return false
				} else if btn == 65 { // Wheel Down
					if x <= leftWidth {
						dp.Chat.ScrollHistory(3)
					} else {
						dp.RightVT.ScrollPageDown(3)
					}
					dp.Render()
					return false
				}

				// Left Mouse Press (btn == 0, M)
				if btn == 0 && !isRelease {
					if x <= leftWidth { // Left Pane
						dp.ActivePane = PaneLeft
						dp.RightSelecting = false
						dp.RightVT.ClearSelection()
						dp.Chat.HandleClick(x-1, row)
					} else if x > leftWidth+1 { // Right Pane
						dp.ActivePane = PaneRight
						dp.Chat.FinishSelection() // clear selection in chat

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
					if dp.ActivePane == PaneLeft && dp.Chat.Selecting {
						dp.Chat.HandleDrag(x-1, row, leftWidth)
						dp.Render()
						return false
					} else if dp.ActivePane == PaneRight && dp.RightSelecting {
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

				// Mouse Release (m): Maintain selection highlight; do not auto-copy
				if isRelease {
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

	// 4. When focused on Left Pane (Chat), dispatch to ChatUI
	shouldExit := dp.Chat.HandleKey(inBuf)
	if shouldExit {
		dp.IsExiting = true
		return true
	}
	dp.Render()
	return false
}

func RunDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) error {
	fd := os.Stdout.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to initialize raw terminal: %w", err)
	}

	// Enter alternate screen & enable SGR mouse tracking for bounded drag selection
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

	// Terminal resize watcher with SIGWINCH and poller
	winchChan := make(chan os.Signal, 2)
	notifyWinch(winchChan)

	resizeTicker := time.NewTicker(250 * time.Millisecond)
	defer resizeTicker.Stop()

	handleResize := func() {
		w, h := getTerminalSize(fd)
		if w > 0 && h > 0 && (w != dp.Width || h != dp.Height) {
			dp.mu.Lock()
			dp.Width = w
			dp.Height = h
			leftWidth := (w - 1) / 2
			rightWidth := w - leftWidth - 1
			contentHeight := h - 2
			if contentHeight < 4 {
				contentHeight = 4
			}
			dp.Chat.Resize(leftWidth, contentHeight)
			dp.RightVT.Resize(contentHeight, rightWidth)
			_ = dp.RightPTY.Resize(contentHeight, rightWidth)
			dp.mu.Unlock()

			// Clear entire screen to eliminate ghost rows/cols when zooming in/out
			_, _ = os.Stdout.WriteString("\x1b[2J")
			_ = os.Stdout.Sync()
			dp.Render()
		}
	}

	go func() {
		for {
			select {
			case <-resizeTicker.C:
				if dp.IsExiting {
					return
				}
				handleResize()
			case <-winchChan:
				if dp.IsExiting {
					return
				}
				handleResize()
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
