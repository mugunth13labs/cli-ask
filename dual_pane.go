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
	PaneLeft  ActivePane = 0 // AI Chatbot (ChatUI)
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

	hint := "Ctrl+T: Switch | Enter: Newline | Ctrl+Enter: Send | Down: History "
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

				leftWidth := int(float64(dp.Width) * 0.40)
				row := y - 3 // 0-based content row

				// Mouse Wheel
				if btn == 64 { // Wheel Up
					if x <= leftWidth {
						dp.Chat.mu.Lock()
						dp.Chat.HistScroll -= 3
						if dp.Chat.HistScroll < 0 {
							dp.Chat.HistScroll = 0
						}
						dp.Chat.mu.Unlock()
					} else {
						dp.RightVT.ScrollPageUp(3)
					}
					dp.Render()
					return false
				} else if btn == 65 { // Wheel Down
					if x <= leftWidth {
						dp.Chat.mu.Lock()
						histH := dp.Chat.Height - dp.Chat.PromptVisibleLines() - 2
						maxScroll := len(dp.Chat.ReversedLines) - histH
						if maxScroll < 0 {
							maxScroll = 0
						}
						dp.Chat.HistScroll += 3
						if dp.Chat.HistScroll > maxScroll {
							dp.Chat.HistScroll = maxScroll
						}
						dp.Chat.mu.Unlock()
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

						visPrompt := dp.Chat.PromptVisibleLines()
						dp.Chat.mu.Lock()
						if row < visPrompt {
							dp.Chat.ActiveRegion = RegionPrompt
							dp.Chat.PromptRow = dp.Chat.PromptScroll + row
							if dp.Chat.PromptRow >= len(dp.Chat.PromptLines) {
								dp.Chat.PromptRow = len(dp.Chat.PromptLines) - 1
							}
							dp.Chat.PromptCol = x - 1 - 4
							if dp.Chat.PromptCol < 0 {
								dp.Chat.PromptCol = 0
							}
							dp.Chat.Selecting = false
						} else if row > visPrompt {
							dp.Chat.ActiveRegion = RegionHistory
							histIdx := (row - visPrompt - 1) + dp.Chat.HistScroll
							dp.Chat.HistRow = histIdx
							dp.Chat.HistCol = x - 1
							dp.Chat.Selecting = true
							dp.Chat.SelStartR = histIdx
							dp.Chat.SelStartC = x - 1
							dp.Chat.SelEndR = histIdx
							dp.Chat.SelEndC = x - 1
						}
						dp.Chat.mu.Unlock()
					} else if x > leftWidth+1 { // Right Pane
						dp.ActivePane = PaneRight
						dp.Chat.mu.Lock()
						dp.Chat.Selecting = false
						dp.Chat.mu.Unlock()

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
						clampedX := x
						if clampedX < 1 {
							clampedX = 1
						}
						if clampedX > leftWidth {
							clampedX = leftWidth
						}
						visPrompt := dp.Chat.PromptVisibleLines()
						histIdx := (row - visPrompt - 1) + dp.Chat.HistScroll
						if histIdx < 0 {
							histIdx = 0
						}
						dp.Chat.mu.Lock()
						dp.Chat.SelEndR = histIdx
						dp.Chat.SelEndC = clampedX - 1
						dp.Chat.mu.Unlock()
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

				// Mouse Release (m)
				if isRelease {
					if dp.ActivePane == PaneLeft && dp.Chat.Selecting {
						selected := dp.Chat.GetSelectedText()
						if selected != "" {
							copyToClipboard(selected)
						}
					} else if dp.ActivePane == PaneRight && dp.RightSelecting {
						selected := dp.RightVT.GetSelectedText()
						if selected != "" {
							copyToClipboard(selected)
						}
					}
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
				dp.Chat.Width = leftWidth
				dp.Chat.Height = contentHeight
				dp.Chat.rebuildReversedLines()
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
