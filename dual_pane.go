package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"unicode/utf8"
)

type ActivePane int

const (
	PaneLeft  ActivePane = 0 // AI Dialogue & Reference View
	PaneRight ActivePane = 1 // Multiline Composer
)

type DualPaneWorkspace struct {
	Width       int
	Height      int
	ActivePane  ActivePane
	LeftLines   []string // rendered conversation lines for the left pane
	LeftScroll  int      // scroll offset for left pane
	Editor      *MicroEditor
	RightScroll int // scroll offset for right pane
	Session     *Session
	Config      *Config
	Opts        QueryOptions
	IsStreaming bool
}

func NewDualPaneWorkspace(cfg *Config, session *Session, opts QueryOptions, initialPrompt string) *DualPaneWorkspace {
	fd := os.Stdout.Fd()
	w, h := getTerminalSize(fd)
	if w < 40 {
		w = 80
	}
	if h < 10 {
		h = 24
	}

	dp := &DualPaneWorkspace{
		Width:      w,
		Height:     h,
		ActivePane: PaneRight, // Focus starts on composer so user can immediately type
		LeftLines:  []string{},
		LeftScroll: 0,
		Editor:     NewMicroEditor("", ""),
		Session:    session,
		Config:     cfg,
		Opts:       opts,
	}

	// Populate left pane with prior session messages if any
	if session != nil && len(session.Messages) > 0 {
		for _, m := range session.Messages {
			dp.appendMessageToLeft(m.Role, m.Content)
		}
	}

	if initialPrompt != "" {
		dp.Editor.InsertText(initialPrompt)
	}

	return dp
}

// wrapText splits a string into lines that fit within maxWidth columns
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
			// Find a whitespace break boundary within maxWidth
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
	colDivider := (dp.Width - 1) / 2
	leftWidth := colDivider - 2

	var header string
	if role == "user" {
		header = fmt.Sprintf("─── You ────────────────────────")
	} else {
		header = fmt.Sprintf("─── ask [%s] ───", dp.Config.Model)
	}
	if len(header) > leftWidth {
		header = header[:leftWidth]
	}

	dp.LeftLines = append(dp.LeftLines, header)
	wrapped := wrapText(content, leftWidth)
	dp.LeftLines = append(dp.LeftLines, wrapped...)
	dp.LeftLines = append(dp.LeftLines, "") // Blank spacing line

	// Auto-scroll to bottom of left pane
	contentHeight := dp.Height - 3
	if len(dp.LeftLines) > contentHeight {
		dp.LeftScroll = len(dp.LeftLines) - contentHeight
	} else {
		dp.LeftScroll = 0
	}
}

func (dp *DualPaneWorkspace) Render() {
	var b strings.Builder
	b.WriteString(ansiHideCursor)

	w := dp.Width
	h := dp.Height

	colDivider := (w - 1) / 2
	leftWidth := colDivider
	rightWidth := w - colDivider - 1
	contentHeight := h - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	// 1. Draw Header Bar (Row 1)
	leftHeaderTitle := "  AI Dialogue & History"
	if dp.ActivePane == PaneLeft {
		leftHeaderTitle = "● AI Dialogue [ACTIVE]"
	}
	rightHeaderTitle := "  Multiline Composer"
	if dp.ActivePane == PaneRight {
		rightHeaderTitle = "● Multiline Composer [ACTIVE]"
	}

	leftHeaderPadded := fmt.Sprintf("%-*s", leftWidth, leftHeaderTitle)
	if len(leftHeaderPadded) > leftWidth {
		leftHeaderPadded = leftHeaderPadded[:leftWidth]
	}
	rightHeaderPadded := fmt.Sprintf("%-*s", rightWidth, rightHeaderTitle)
	if len(rightHeaderPadded) > rightWidth {
		rightHeaderPadded = rightHeaderPadded[:rightWidth]
	}

	b.WriteString("\x1b[1;1H\x1b[7m") // Row 1, inverted colors
	b.WriteString(leftHeaderPadded)
	b.WriteString("│")
	b.WriteString(rightHeaderPadded)
	b.WriteString("\x1b[0m") // Reset color

	// 2. Draw Separator Line (Row 2)
	b.WriteString("\x1b[2;1H")
	b.WriteString(strings.Repeat("─", leftWidth))
	b.WriteString("┼")
	b.WriteString(strings.Repeat("─", rightWidth))

	// 3. Draw Body Rows (Rows 3 to 3 + contentHeight - 1)
	// Right scroll tracking: keep cursor in view
	if dp.Editor.CursorRow < dp.RightScroll {
		dp.RightScroll = dp.Editor.CursorRow
	} else if dp.Editor.CursorRow >= dp.RightScroll+contentHeight {
		dp.RightScroll = dp.Editor.CursorRow - contentHeight + 1
	}

	for r := 0; r < contentHeight; r++ {
		rowANSI := 3 + r
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", rowANSI))

		// Left Column Content
		leftIdx := dp.LeftScroll + r
		leftStr := ""
		if leftIdx >= 0 && leftIdx < len(dp.LeftLines) {
			leftStr = dp.LeftLines[leftIdx]
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

		// Right Column Content
		rightIdx := dp.RightScroll + r
		rightStr := ""
		if rightIdx >= 0 && rightIdx < len(dp.Editor.Lines) {
			prefix := "  "
			if rightIdx == dp.Editor.CursorRow {
				prefix = "> "
			}
			rightStr = prefix + dp.Editor.Lines[rightIdx]
		}
		// Pad or clip rightStr to rightWidth
		rightRunes := []rune(rightStr)
		if len(rightRunes) > rightWidth {
			rightStr = string(rightRunes[:rightWidth])
		} else {
			rightStr = rightStr + strings.Repeat(" ", rightWidth-len(rightRunes))
		}
		b.WriteString(rightStr)
	}

	// 4. Draw Status Footer (Row h)
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[7m", h))
	statusText := " [Ctrl+T] Switch Pane | [Ctrl+Enter] Send | [PgUp/Dn] Scroll Left | [/exit] Exit"
	statusPadded := fmt.Sprintf("%-*s", w, statusText)
	if len(statusPadded) > w {
		statusPadded = statusPadded[:w]
	}
	b.WriteString(statusPadded)
	b.WriteString("\x1b[0m")

	// 5. Place Cursor
	if dp.ActivePane == PaneRight && !dp.IsStreaming {
		targetRow := 3 + (dp.Editor.CursorRow - dp.RightScroll)
		targetCol := leftWidth + 1 + 2 + dp.Editor.CursorCol + 1 // divider(1) + prefix(2) + col + 1 (ANSI 1-based)
		if targetRow >= 3 && targetRow < 3+contentHeight {
			b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", targetRow, targetCol))
		}
	}

	_, _ = os.Stdout.WriteString(b.String())
	_ = os.Stdout.Sync()
}

func RunDualPaneWorkspace(cfg *Config, initialPrompt string, opts QueryOptions) error {
	sessionID := getSessionID()
	session, err := loadSession(sessionID)
	if err != nil {
		session = &Session{
			ID:       sessionID,
			Messages: []Message{},
		}
	}

	// Enter Alternate Screen Buffer
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + ansiEnablePaste)
	_ = os.Stdout.Sync()

	fd := os.Stdin.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiLeaveAltScreen)
		_ = os.Stdout.Sync()
		return err
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = restoreTerm(fd, state)
			_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiLeaveAltScreen)
			_ = os.Stdout.Sync()
		})
	}
	defer cleanup()

	// SIGINT handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)

	go func() {
		<-sigChan
		cleanup()
		os.Exit(130)
	}()

	dp := NewDualPaneWorkspace(cfg, session, opts, initialPrompt)
	dp.Render()

	buf := make([]byte, 1024)

	for {
		n, readErr := os.Stdin.Read(buf)
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
		if n == 0 {
			continue
		}

		data := buf[:n]
		input := string(data)

		// Handle bracketed paste blocks in composer
		if strings.Contains(input, "\x1b[2004~") {
			parts := strings.Split(input, "\x1b[2004~")
			for i, part := range parts {
				if i%2 == 1 {
					dp.Editor.InsertText(part)
				}
			}
			dp.Render()
			continue
		}

		// Process key events
		i := 0
		for i < len(data) {
			b := data[i]

			// Ctrl+C (ASCII 3) -> Exit
			if b == 3 {
				return nil
			}

			// Ctrl+T (ASCII 20) -> Toggle Active Pane
			if b == 20 {
				if dp.ActivePane == PaneLeft {
					dp.ActivePane = PaneRight
				} else {
					dp.ActivePane = PaneLeft
				}
				dp.Render()
				i++
				continue
			}

			// STRICT: Ctrl+D and Ctrl+Z do NOT submit
			if b == 4 || b == 26 {
				i++
				continue
			}

			// Check Ctrl+Enter submit (byte 10 or CSI u / xterm escape sequences)
			isCtrlEnter := (b == 10)
			remStr := string(data[i:])
			if strings.HasPrefix(remStr, "\x1b[13;5u") || strings.HasPrefix(remStr, "\x1b[27;5;13~") ||
				strings.HasPrefix(remStr, "\x1b[10;5u") || strings.HasPrefix(remStr, "\x1b[10~") ||
				strings.HasPrefix(remStr, "\x1b[13~") || strings.HasPrefix(remStr, "\x1b\r") ||
				strings.HasPrefix(remStr, "\x1b\n") {
				isCtrlEnter = true
			}

			if isCtrlEnter {
				prompt := dp.Editor.Content()
				lower := strings.ToLower(prompt)

				// Check exit commands
				if lower == "/exit" || lower == "/quit" || lower == "/q" {
					return nil
				}

				if lower == "/clear" || lower == "/c" {
					_ = clearSession(sessionID)
					dp.Session.Messages = []Message{}
					dp.LeftLines = []string{}
					dp.LeftScroll = 0
					dp.Editor = NewMicroEditor("", "")
					dp.Render()
					break
				}

				if prompt != "" {
					// Post user prompt to left pane
					dp.appendMessageToLeft("user", prompt)
					dp.Editor = NewMicroEditor("", "") // Reset composer for next turn
					dp.IsStreaming = true
					dp.Render()

					// Run OpenRouter streaming query
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

					var responseBuilder strings.Builder
					streamOpts := dp.Opts
					streamOpts.OnToken = func(token string) {
						responseBuilder.WriteString(token)
						// Update streaming text in left pane live
						// Remove previous assistant lines and re-render with updated text
						dp.updateStreamingLeft(responseBuilder.String())
						dp.Render()
					}

					resp, queryErr := queryOpenRouter(context.Background(), dp.Config, apiMessages, streamOpts)
					dp.IsStreaming = false

					if queryErr == nil && resp != "" {
						dp.Session.Messages = append(dp.Session.Messages,
							Message{Role: "user", Content: prompt},
							Message{Role: "assistant", Content: resp},
						)
						_ = saveSession(dp.Session)
					}
					dp.Render()
				}
				break
			}

			// Navigation and editing based on active pane
			if b == 27 {
				rem := data[i:]
				// Arrow keys in Left Pane -> Scroll Left Pane
				if len(rem) >= 3 && (rem[1] == '[' || rem[1] == 'O') {
					key := rem[2]
					if dp.ActivePane == PaneLeft {
						contentHeight := dp.Height - 3
						maxScroll := len(dp.LeftLines) - contentHeight
						if maxScroll < 0 {
							maxScroll = 0
						}
						switch key {
						case 'A': // Up
							if dp.LeftScroll > 0 {
								dp.LeftScroll--
							}
						case 'B': // Down
							if dp.LeftScroll < maxScroll {
								dp.LeftScroll++
							}
						}
						dp.Render()
						i += 3
						continue
					} else {
						// Right Pane arrow keys -> Editor movement
						switch key {
						case 'A':
							dp.Editor.MoveUp()
						case 'B':
							dp.Editor.MoveDown()
						case 'C':
							dp.Editor.MoveRight()
						case 'D':
							dp.Editor.MoveLeft()
						case 'H':
							dp.Editor.CursorCol = 0
						case 'F':
							dp.Editor.CursorCol = len([]rune(dp.Editor.Lines[dp.Editor.CursorRow]))
						}
						dp.Render()
						i += 3
						continue
					}
				}

				// PageUp (\x1b[5~) and PageDown (\x1b[6~)
				if len(rem) >= 4 && rem[1] == '[' && rem[3] == '~' {
					contentHeight := dp.Height - 3
					maxScroll := len(dp.LeftLines) - contentHeight
					if maxScroll < 0 {
						maxScroll = 0
					}
					if rem[2] == '5' { // PageUp
						dp.LeftScroll -= contentHeight / 2
						if dp.LeftScroll < 0 {
							dp.LeftScroll = 0
						}
						dp.Render()
						i += 4
						continue
					}
					if rem[2] == '6' { // PageDown
						dp.LeftScroll += contentHeight / 2
						if dp.LeftScroll > maxScroll {
							dp.LeftScroll = maxScroll
						}
						dp.Render()
						i += 4
						continue
					}
					if rem[2] == '3' { // Delete key
						if dp.ActivePane == PaneRight {
							dp.Editor.Delete()
							dp.Render()
						}
						i += 4
						continue
					}
				}

				i++
				continue
			}

			// Backspace (127 or 8)
			if b == 127 || b == 8 {
				if dp.ActivePane == PaneRight {
					dp.Editor.Backspace()
					dp.Render()
				}
				i++
				continue
			}

			// Enter (13 \r) -> Newline in composer
			if b == 13 {
				if dp.ActivePane == PaneRight {
					dp.Editor.InsertNewline()
					dp.Render()
				}
				i++
				continue
			}

			// Printable UTF-8 character insertion into composer
			r, size := utf8.DecodeRune(data[i:])
			if r != utf8.RuneError && r >= 32 {
				if dp.ActivePane == PaneRight {
					dp.Editor.InsertChar(r)
					dp.Render()
				}
				i += size
				continue
			}

			i++
		}
	}

	return nil
}

func (dp *DualPaneWorkspace) updateStreamingLeft(currentContent string) {
	colDivider := (dp.Width - 1) / 2
	leftWidth := colDivider - 2

	// Reconstruct LeftLines: preserve everything before the current streaming response
	// Find the last header "─── ask ["
	lastHeaderIdx := -1
	for idx := len(dp.LeftLines) - 1; idx >= 0; idx-- {
		if strings.HasPrefix(dp.LeftLines[idx], "─── ask [") {
			lastHeaderIdx = idx
			break
		}
	}

	header := fmt.Sprintf("─── ask [%s] ───", dp.Config.Model)
	if len(header) > leftWidth {
		header = header[:leftWidth]
	}

	if lastHeaderIdx == -1 {
		dp.LeftLines = append(dp.LeftLines, header)
	} else {
		dp.LeftLines = dp.LeftLines[:lastHeaderIdx+1]
	}

	wrapped := wrapText(currentContent, leftWidth)
	dp.LeftLines = append(dp.LeftLines, wrapped...)

	contentHeight := dp.Height - 3
	if len(dp.LeftLines) > contentHeight {
		dp.LeftScroll = len(dp.LeftLines) - contentHeight
	} else {
		dp.LeftScroll = 0
	}
}
