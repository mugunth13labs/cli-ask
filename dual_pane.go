package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
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

	hint := "Ctrl+T: Switch | PgUp/Dn: Scroll | Mouse: Native Select/Copy "
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
		targetRow := 3 + cursorRow
		targetCol := 1 + cursorCol
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", targetRow, targetCol, ansiShowCursor))
	} else {
		cursorRow := dp.RightVT.CursorRow
		cursorCol := dp.RightVT.CursorCol
		targetRow := 3 + cursorRow
		targetCol := leftWidth + 2 + cursorCol
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

		// Filter out all 0x14 bytes
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

	// 2. Check for PgUp / PgDn scrollback
	if bytes.Equal(inBuf, []byte("\x1b[5~")) {
		if dp.ActivePane == PaneLeft {
			dp.LeftVT.ScrollPageUp(5)
		} else {
			dp.RightVT.ScrollPageUp(5)
		}
		dp.Render()
		return false
	}
	if bytes.Equal(inBuf, []byte("\x1b[6~")) {
		if dp.ActivePane == PaneLeft {
			dp.LeftVT.ScrollPageDown(5)
		} else {
			dp.RightVT.ScrollPageDown(5)
		}
		dp.Render()
		return false
	}

	// 3. If currently scrolled up and user starts typing, snap back to bottom
	if dp.ActivePane == PaneLeft {
		if dp.LeftVT.ScrollOffset > 0 {
			dp.LeftVT.SnapToBottom()
			dp.Render()
		}
		// Forward atomic chunk to Left PTY (ask -i)
		_, _ = dp.LeftPTY.Write(inBuf)
	} else {
		if dp.RightVT.ScrollOffset > 0 {
			dp.RightVT.SnapToBottom()
			dp.Render()
		}
		// Forward atomic chunk to Right PTY (Shell)
		_, _ = dp.RightPTY.Write(inBuf)
	}

	return false
}

func RunDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) error {
	fd := os.Stdout.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to initialize raw terminal: %w", err)
	}

	// Enter alternate screen buffer (leave mouse to native terminal selection!)
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen)
	_ = os.Stdout.Sync()

	dp, err := NewDualPaneWorkspace(initialPrompt)
	if err != nil {
		_ = restoreTerm(fd, state)
		_, _ = os.Stdout.WriteString(ansiLeaveAltScreen + ansiShowCursor)
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
			_, _ = os.Stdout.WriteString(ansiLeaveAltScreen + ansiShowCursor)
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
