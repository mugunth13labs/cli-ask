package main

import (
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
	PaneLeft  ActivePane = 0 // ask -i (Chat)
	PaneRight ActivePane = 1 // Live Shell Terminal
)

type DualPaneWorkspace struct {
	mu         sync.Mutex
	Width      int
	Height     int
	ActivePane ActivePane

	LeftPTY  PTYSession
	RightPTY PTYSession

	LeftVT  *VTGrid
	RightVT *VTGrid

	IsExiting bool
}

func NewDualPaneWorkspace(initialPrompt string) (*DualPaneWorkspace, error) {
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

	// 1. Determine current executable for Pane 1 (ask -i)
	execPath, err := os.Executable()
	if err != nil {
		execPath = os.Args[0]
	}

	askArgs := []string{"-i"}
	if initialPrompt != "" {
		askArgs = append(askArgs, initialPrompt)
	}

	leftPTY, err := StartPTYCommand(contentHeight, leftWidth, execPath, askArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to start ask -i PTY: %w", err)
	}

	// 2. Start default shell for Pane 2
	rightPTY, err := StartPTY(contentHeight, rightWidth)
	if err != nil {
		_ = leftPTY.Close()
		return nil, fmt.Errorf("failed to start shell PTY: %w", err)
	}

	leftVT := NewVTGrid(contentHeight, leftWidth)
	rightVT := NewVTGrid(contentHeight, rightWidth)

	dp := &DualPaneWorkspace{
		Width:      w,
		Height:     h,
		ActivePane: PaneLeft, // Starts focused on ask -i
		LeftPTY:    leftPTY,
		RightPTY:   rightPTY,
		LeftVT:     leftVT,
		RightVT:    rightVT,
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

	hint := "Ctrl+T: Switch | PgUp/Dn: Scroll | Shift+Arrows: Select | Ctrl+C: Copy "
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

		// Left Column Content: rendered from LeftVT
		leftFormatted := dp.LeftVT.RenderRow(r)
		b.WriteString(leftFormatted)

		// Divider
		b.WriteString("│")

		// Right Column Content: rendered from RightVT
		rightFormatted := dp.RightVT.RenderRow(r)
		b.WriteString(rightFormatted)
	}

	// 4. Place Cursor based on ActivePane
	if dp.ActivePane == PaneLeft {
		cursorRow := dp.LeftVT.CursorRow
		cursorCol := dp.LeftVT.CursorCol
		if dp.LeftVT.IsNavMode {
			cursorRow = dp.LeftVT.NavRow
			cursorCol = dp.LeftVT.NavCol
		}
		targetRow := 3 + cursorRow
		targetCol := 1 + cursorCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	} else {
		cursorRow := dp.RightVT.CursorRow
		cursorCol := dp.RightVT.CursorCol
		if dp.RightVT.IsNavMode {
			cursorRow = dp.RightVT.NavRow
			cursorCol = dp.RightVT.NavCol
		}
		targetRow := 3 + cursorRow
		targetCol := leftWidth + 2 + cursorCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	}

	_, _ = os.Stdout.WriteString(b.String())
	_ = os.Stdout.Sync()
}

func (dp *DualPaneWorkspace) handleInput(inBuf []byte) bool {
	i := 0
	n := len(inBuf)

	for i < n {
		b := inBuf[i]

		// 1. Universal Pane Toggle: Ctrl+T (0x14)
		if b == 0x14 {
			dp.mu.Lock()
			if dp.ActivePane == PaneLeft {
				dp.ActivePane = PaneRight
			} else {
				dp.ActivePane = PaneLeft
			}
			dp.mu.Unlock()
			dp.Render()
			i++
			continue
		}

		// 2. Check for Escape sequences: Arrows, Shift+Arrows, PgUp/PgDn, Mouse
		if b == 0x1b && i+1 < n {
			rest := inBuf[i+1:]

			// Mouse reporting sequence: \x1b[<64;X;YM or \x1b[<65;X;YM
			if len(rest) >= 5 && rest[0] == '[' && rest[1] == '<' {
				endIdx := 2
				for endIdx < len(rest) && (rest[endIdx] == ';' || (rest[endIdx] >= '0' && rest[endIdx] <= '9')) {
					endIdx++
				}
				if endIdx < len(rest) && (rest[endIdx] == 'M' || rest[endIdx] == 'm') {
					parts := strings.Split(string(rest[2:endIdx]), ";")
					if len(parts) == 3 {
						btn, _ := strconv.Atoi(parts[0])
						x, _ := strconv.Atoi(parts[1])
						leftWidth := int(float64(dp.Width) * 0.40)

						// 64 = Wheel Up, 65 = Wheel Down
						if btn == 64 {
							if x <= leftWidth {
								dp.LeftVT.ScrollPageUp(3)
							} else {
								dp.RightVT.ScrollPageUp(3)
							}
							dp.Render()
						} else if btn == 65 {
							if x <= leftWidth {
								dp.LeftVT.ScrollPageDown(3)
							} else {
								dp.RightVT.ScrollPageDown(3)
							}
							dp.Render()
						}
					}
					i += 2 + endIdx
					continue
				}
			}

			// PgUp: \x1b[5~
			if len(rest) >= 3 && rest[0] == '[' && rest[1] == '5' && rest[2] == '~' {
				if dp.ActivePane == PaneLeft {
					dp.LeftVT.ScrollPageUp(5)
				} else {
					dp.RightVT.ScrollPageUp(5)
				}
				dp.Render()
				i += 4
				continue
			}

			// PgDn: \x1b[6~
			if len(rest) >= 3 && rest[0] == '[' && rest[1] == '6' && rest[2] == '~' {
				if dp.ActivePane == PaneLeft {
					dp.LeftVT.ScrollPageDown(5)
				} else {
					dp.RightVT.ScrollPageDown(5)
				}
				dp.Render()
				i += 4
				continue
			}

			// Shift+Arrows / Ctrl+Shift+Arrows for text selection in Left Pane
			if dp.ActivePane == PaneLeft && len(rest) >= 5 && rest[0] == '[' && rest[1] == '1' && rest[2] == ';' {
				mod := rest[3] // '2' = Shift, '6' = Ctrl+Shift
				dir := rest[4] // 'A'=Up, 'B'=Down, 'C'=Right, 'D'=Left

				if mod == '2' || mod == '6' {
					dp.LeftVT.mu.Lock()
					if !dp.LeftVT.HasSelection {
						dp.LeftVT.HasSelection = true
						dp.LeftVT.SelStartRow = dp.LeftVT.CursorRow
						dp.LeftVT.SelStartCol = dp.LeftVT.CursorCol
						dp.LeftVT.NavRow = dp.LeftVT.CursorRow
						dp.LeftVT.NavCol = dp.LeftVT.CursorCol
						dp.LeftVT.IsNavMode = true
					}

					step := 1
					if mod == '6' {
						step = 5 // word jump
					}

					switch dir {
					case 'A': // Up
						dp.LeftVT.NavRow -= step
						if dp.LeftVT.NavRow < 0 {
							dp.LeftVT.NavRow = 0
						}
					case 'B': // Down
						dp.LeftVT.NavRow += step
						if dp.LeftVT.NavRow >= dp.LeftVT.Rows {
							dp.LeftVT.NavRow = dp.LeftVT.Rows - 1
						}
					case 'C': // Right
						dp.LeftVT.NavCol += step
						if dp.LeftVT.NavCol >= dp.LeftVT.Cols {
							dp.LeftVT.NavCol = dp.LeftVT.Cols - 1
						}
					case 'D': // Left
						dp.LeftVT.NavCol -= step
						if dp.LeftVT.NavCol < 0 {
							dp.LeftVT.NavCol = 0
						}
					}
					dp.LeftVT.SelEndRow = dp.LeftVT.NavRow
					dp.LeftVT.SelEndCol = dp.LeftVT.NavCol
					dp.LeftVT.mu.Unlock()

					dp.Render()
					i += 6
					continue
				}
			}
		}

		// 3. Ctrl+C Copy action if Left Pane has text selected
		if dp.ActivePane == PaneLeft && b == 3 {
			if dp.LeftVT.HasSelection {
				selected := dp.LeftVT.GetSelectedText()
				if selected != "" {
					copyToClipboard(selected)
				}
				dp.LeftVT.SnapToBottom()
				dp.Render()
				i++
				continue
			}
		}

		// 4. Forward input to focused pane's PTY
		if dp.ActivePane == PaneLeft {
			// If Left Pane was scrolled or in selection mode, typing any character snaps to bottom
			if dp.LeftVT.ScrollOffset > 0 || dp.LeftVT.IsNavMode || dp.LeftVT.HasSelection {
				dp.LeftVT.SnapToBottom()
				dp.Render()
			}
			_, _ = dp.LeftPTY.Write([]byte{b})
		} else {
			if dp.RightVT.ScrollOffset > 0 {
				dp.RightVT.SnapToBottom()
				dp.Render()
			}
			_, _ = dp.RightPTY.Write([]byte{b})
		}

		i++
	}

	return false
}

func RunDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) error {
	fd := os.Stdout.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to initialize raw terminal: %w", err)
	}

	// Enter alternate screen buffer & enable mouse reporting
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + "\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	_ = os.Stdout.Sync()

	dp, err := NewDualPaneWorkspace(initialPrompt)
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

			if dp.LeftPTY != nil {
				_ = dp.LeftPTY.Close()
			}
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

	// Background reader for Left PTY (ask -i)
	leftDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := dp.LeftPTY.Read(buf)
			if n > 0 {
				dp.LeftVT.Write(buf[:n])
				dp.Render()
			}
			if err != nil {
				break
			}
		}
		close(leftDone)
	}()

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
				dp.LeftVT.Resize(contentHeight, leftWidth)
				dp.RightVT.Resize(contentHeight, rightWidth)
				_ = dp.LeftPTY.Resize(contentHeight, leftWidth)
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
		case <-leftDone:
			// ask -i exited (e.g. /exit) -> cleanly close workspace!
			return nil
		case <-rightDone:
			// Shell exited (e.g. user typed 'exit' in terminal) -> cleanly close workspace!
			return nil
		case <-sigChan:
			return nil
		case inBuf, ok := <-inputChan:
			if !ok {
				return nil
			}
			if shouldExit := dp.handleInput(inBuf); shouldExit {
				return nil
			}
		}
	}
}
