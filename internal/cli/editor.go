package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	ansiEnterAltScreen = "\x1b[?1049h\x1b[H\x1b[2J"
	ansiLeaveAltScreen = "\x1b[?1049l"
	ansiEnablePaste    = "\x1b[?2004h"
	ansiDisablePaste   = "\x1b[?2004l"
	ansiHideCursor     = "\x1b[?25l"
	ansiShowCursor     = "\x1b[?25h"
	ansiClearScreen     = "\x1b[H\x1b[2J"
	ansiEnableKeyboard  = "\x1b[>1u\x1b[>4;2m"
	ansiDisableKeyboard = "\x1b[<u\x1b[>4;0m"
)

type MicroEditor struct {
	Lines     []string
	CursorRow int
	CursorCol int
	Title     string
	Subtitle  string
}

func NewMicroEditor(title, subtitle string) *MicroEditor {
	return &MicroEditor{
		Lines:     []string{""},
		CursorRow: 0,
		CursorCol: 0,
		Title:     title,
		Subtitle:  subtitle,
	}
}

func (e *MicroEditor) Content() string {
	return strings.TrimSpace(strings.Join(e.Lines, "\n"))
}

func (e *MicroEditor) Render() {
	var b strings.Builder
	b.WriteString(ansiHideCursor)
	b.WriteString(ansiClearScreen)

	// Draw Header
	b.WriteString(fmt.Sprintf("%s\n", e.Title))
	if e.Subtitle != "" {
		b.WriteString(fmt.Sprintf("%s\n", e.Subtitle))
	}
	b.WriteString("──────────────────────────────────────────────────────────────────────────────\n")

	// Draw Lines with line indicators
	for i, line := range e.Lines {
		prefix := "  "
		if i == e.CursorRow {
			prefix = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", prefix, line))
	}

	// Position cursor at active editing point
	// Row offset: Title(1) + Subtitle(1 if exists) + Separator(1) + CursorRow(0-based) + 1 (1-based ANSI)
	headerRows := 2
	if e.Subtitle != "" {
		headerRows = 3
	}
	targetRow := headerRows + e.CursorRow + 1
	targetCol := 3 + e.CursorCol // prefix is 2 chars + 1 for 1-based ANSI

	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", targetRow, targetCol))
	b.WriteString(ansiShowCursor)

	_, _ = os.Stdout.WriteString(b.String())
	_ = os.Stdout.Sync()
}

func (e *MicroEditor) InsertChar(ch rune) {
	line := e.Lines[e.CursorRow]
	runes := []rune(line)
	if e.CursorCol > len(runes) {
		e.CursorCol = len(runes)
	}

	newRunes := make([]rune, 0, len(runes)+1)
	newRunes = append(newRunes, runes[:e.CursorCol]...)
	newRunes = append(newRunes, ch)
	newRunes = append(newRunes, runes[e.CursorCol:]...)

	e.Lines[e.CursorRow] = string(newRunes)
	e.CursorCol++
}

func (e *MicroEditor) InsertText(text string) {
	// Normalize newlines
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	parts := strings.Split(text, "\n")
	for i, part := range parts {
		if i > 0 {
			e.InsertNewline()
		}
		for _, ch := range part {
			e.InsertChar(ch)
		}
	}
}

func (e *MicroEditor) InsertNewline() {
	line := e.Lines[e.CursorRow]
	runes := []rune(line)
	if e.CursorCol > len(runes) {
		e.CursorCol = len(runes)
	}

	left := string(runes[:e.CursorCol])
	right := string(runes[e.CursorCol:])

	e.Lines[e.CursorRow] = left

	// Insert new row after current row safely
	newLines := make([]string, 0, len(e.Lines)+1)
	newLines = append(newLines, e.Lines[:e.CursorRow+1]...)
	newLines = append(newLines, right)
	newLines = append(newLines, e.Lines[e.CursorRow+1:]...)

	e.Lines = newLines
	e.CursorRow++
	e.CursorCol = 0
}

func (e *MicroEditor) Backspace() {
	if e.CursorCol > 0 {
		line := e.Lines[e.CursorRow]
		runes := []rune(line)
		if e.CursorCol > len(runes) {
			e.CursorCol = len(runes)
		}
		newRunes := make([]rune, 0, len(runes)-1)
		newRunes = append(newRunes, runes[:e.CursorCol-1]...)
		newRunes = append(newRunes, runes[e.CursorCol:]...)
		e.Lines[e.CursorRow] = string(newRunes)
		e.CursorCol--
	} else if e.CursorRow > 0 {
		// Cross-line backspace: merge current line into previous line
		prevLine := e.Lines[e.CursorRow-1]
		currLine := e.Lines[e.CursorRow]

		prevRunes := []rune(prevLine)
		newCursorCol := len(prevRunes)

		e.Lines[e.CursorRow-1] = prevLine + currLine

		// Remove current line safely
		newLines := make([]string, 0, len(e.Lines)-1)
		newLines = append(newLines, e.Lines[:e.CursorRow]...)
		newLines = append(newLines, e.Lines[e.CursorRow+1:]...)
		e.Lines = newLines

		e.CursorRow--
		e.CursorCol = newCursorCol
	}
}

func (e *MicroEditor) Delete() {
	line := e.Lines[e.CursorRow]
	runes := []rune(line)
	if e.CursorCol < len(runes) {
		newRunes := make([]rune, 0, len(runes)-1)
		newRunes = append(newRunes, runes[:e.CursorCol]...)
		newRunes = append(newRunes, runes[e.CursorCol+1:]...)
		e.Lines[e.CursorRow] = string(newRunes)
	} else if e.CursorRow < len(e.Lines)-1 {
		// Merge next line into current line
		nextLine := e.Lines[e.CursorRow+1]
		e.Lines[e.CursorRow] = line + nextLine

		newLines := make([]string, 0, len(e.Lines)-1)
		newLines = append(newLines, e.Lines[:e.CursorRow+1]...)
		newLines = append(newLines, e.Lines[e.CursorRow+2:]...)
		e.Lines = newLines
	}
}

func (e *MicroEditor) MoveLeft() {
	if e.CursorCol > 0 {
		e.CursorCol--
	} else if e.CursorRow > 0 {
		e.CursorRow--
		e.CursorCol = len([]rune(e.Lines[e.CursorRow]))
	}
}

func (e *MicroEditor) MoveRight() {
	lineLen := len([]rune(e.Lines[e.CursorRow]))
	if e.CursorCol < lineLen {
		e.CursorCol++
	} else if e.CursorRow < len(e.Lines)-1 {
		e.CursorRow++
		e.CursorCol = 0
	}
}

func (e *MicroEditor) MoveUp() {
	if e.CursorRow > 0 {
		e.CursorRow--
		lineLen := len([]rune(e.Lines[e.CursorRow]))
		if e.CursorCol > lineLen {
			e.CursorCol = lineLen
		}
	}
}

func (e *MicroEditor) MoveDown() {
	if e.CursorRow < len(e.Lines)-1 {
		e.CursorRow++
		lineLen := len([]rune(e.Lines[e.CursorRow]))
		if e.CursorCol > lineLen {
			e.CursorCol = lineLen
		}
	}
}

type visLine struct {
	text      string
	lineIdx   int
	runeStart int
	runeEnd   int
}

func getVisualLines(lines []string, maxContentWidth int) []visLine {
	if maxContentWidth <= 10 {
		maxContentWidth = 10
	}
	var res []visLine
	for lIdx, l := range lines {
		runes := []rune(l)
		if len(runes) == 0 {
			res = append(res, visLine{text: "", lineIdx: lIdx, runeStart: 0, runeEnd: 0})
			continue
		}
		start := 0
		for start < len(runes) {
			end := start + maxContentWidth
			if end > len(runes) {
				end = len(runes)
			}
			res = append(res, visLine{
				text:      string(runes[start:end]),
				lineIdx:   lIdx,
				runeStart: start,
				runeEnd:   end,
			})
			start = end
		}
	}
	return res
}

func getCursorVisPos(visLines []visLine, cursorRow, cursorCol int) (int, int) {
	visRow := 0
	visCol := 0
	found := false

	for i, vl := range visLines {
		if vl.lineIdx == cursorRow {
			if cursorCol >= vl.runeStart && cursorCol <= vl.runeEnd {
				visRow = i
				visCol = cursorCol - vl.runeStart
				found = true
				if cursorCol == vl.runeEnd && i+1 < len(visLines) && visLines[i+1].lineIdx == cursorRow {
					visRow = i + 1
					visCol = 0
				}
				break
			}
		}
	}

	if !found {
		if len(visLines) > 0 {
			visRow = len(visLines) - 1
			visCol = len([]rune(visLines[visRow].text))
		}
	}
	return visRow, visCol
}

// formatInlineComposerBox renders the framed box for the inline multiline prompt composer,
// restricting visible content lines to maxVisibleLines (default 5) and maintaining a sliding viewport.
func formatInlineComposerBox(editor *MicroEditor, boxWidth int, maxVisibleLines int, scrollOffset int, targetDesc string) (string, []string, string, int, int, int) {
	if boxWidth <= 0 || boxWidth < 30 {
		boxWidth = 80
	}
	if maxVisibleLines <= 0 {
		maxVisibleLines = 5
	}

	maxTextWidth := boxWidth - 4
	if maxTextWidth < 10 {
		maxTextWidth = 10
	}

	visLines := getVisualLines(editor.Lines, maxTextWidth)
	visRow, visCol := getCursorVisPos(visLines, editor.CursorRow, editor.CursorCol)

	// Adjust scrollOffset to keep visRow inside [scrollOffset, scrollOffset + maxVisibleLines - 1]
	if visRow < scrollOffset {
		scrollOffset = visRow
	} else if visRow >= scrollOffset+maxVisibleLines {
		scrollOffset = visRow - maxVisibleLines + 1
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if len(visLines) > 0 && scrollOffset >= len(visLines) {
		scrollOffset = len(visLines) - 1
	}

	end := scrollOffset + maxVisibleLines
	if end > len(visLines) {
		end = len(visLines)
	}
	windowLines := visLines[scrollOffset:end]
	cursorRowInWindow := visRow - scrollOffset

	// Top border with scroll up indicator if scrolled down
	header := "─ ask prompt [ Ctrl+Enter: Send | Ctrl+C: Cancel ] "
	if scrollOffset > 0 {
		if targetDesc != "" {
			header = fmt.Sprintf("─ ask prompt [ Target: %s | ▲ %d above | Ctrl+Enter: Send | Ctrl+C: Cancel ] ", targetDesc, scrollOffset)
		} else {
			header = fmt.Sprintf("─ ask prompt [ ▲ %d above | Ctrl+Enter: Send | Ctrl+C: Cancel ] ", scrollOffset)
		}
	} else if targetDesc != "" {
		header = fmt.Sprintf("─ ask prompt [ Target: %s | Ctrl+Enter: Send | Ctrl+C: Cancel ] ", targetDesc)
	}

	headerRunes := utf8.RuneCountInString(header)
	if headerRunes > boxWidth {
		header = string([]rune(header)[:boxWidth])
		headerRunes = boxWidth
	}
	dashes := ""
	if headerRunes < boxWidth {
		dashes = strings.Repeat("─", boxWidth-headerRunes)
	}
	topBorder := fmt.Sprintf("\x1b[90m%s%s\x1b[0m", header, dashes)

	contentLines := make([]string, 0, len(windowLines))
	for _, vl := range windowLines {
		lineRunes := utf8.RuneCountInString(vl.text)
		padLen := maxTextWidth - lineRunes
		pad := ""
		if padLen > 0 {
			pad = strings.Repeat(" ", padLen)
		}
		contentLines = append(contentLines, fmt.Sprintf("\x1b[48;5;236m  %s%s  \x1b[0m", vl.text, pad))
	}

	// Bottom border with scroll down indicator if lines remain below
	linesBelow := len(visLines) - (scrollOffset + len(windowLines))
	var bottomBorder string
	if linesBelow > 0 {
		bottomText := fmt.Sprintf("─── [ ▼ %d more below ] ", linesBelow)
		bRunes := utf8.RuneCountInString(bottomText)
		if bRunes > boxWidth {
			bottomText = string([]rune(bottomText)[:boxWidth])
			bRunes = boxWidth
		}
		bDashes := ""
		if bRunes < boxWidth {
			bDashes = strings.Repeat("─", boxWidth-bRunes)
		}
		bottomBorder = fmt.Sprintf("\x1b[90m%s%s\x1b[0m", bottomText, bDashes)
	} else {
		bottomBorder = fmt.Sprintf("\x1b[90m%s\x1b[0m", strings.Repeat("─", boxWidth))
	}

	return topBorder, contentLines, bottomBorder, cursorRowInWindow, visCol, scrollOffset
}

// runInlineComposer executes the micro-editor inline on stdout without entering the alternate screen buffer.
// It draws a framed box with subtle background tint, and on submit clears the box and prints
// clean prompt lines directly into the terminal scrollback history.
// Returns (prompt, isCancelled, error)
func runInlineComposer(targetDesc string) (string, bool, error) {
	fd := os.Stdin.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return "", false, err
	}

	_, _ = os.Stdout.WriteString(ansiEnablePaste + ansiEnableKeyboard)
	_ = os.Stdout.Sync()

	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiDisableKeyboard)
			_ = os.Stdout.Sync()
			_ = restoreTerm(fd, state)
		})
	}
	defer restore()

	editor := NewMicroEditor("", "")
	prevCursorRowInWindow := 0
	scrollOffset := 0
	prevRendered := false

	clearBox := func() {
		if !prevRendered {
			return
		}
		linesUp := prevCursorRowInWindow + 1
		var b strings.Builder
		if linesUp > 0 {
			b.WriteString(fmt.Sprintf("\x1b[%dA", linesUp))
		}
		b.WriteString("\r\x1b[J")
		_, _ = os.Stdout.WriteString(b.String())
		_ = os.Stdout.Sync()
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)

	go func() {
		<-sigChan
		clearBox()
		restore()
		os.Exit(130)
	}()

	renderBox := func() {
		termW, termH := getTerminalSize(os.Stdout.Fd())
		boxWidth := termW - 2
		if boxWidth < 30 {
			boxWidth = 30
		}
		if boxWidth > 100 {
			boxWidth = 100
		}

		maxVisibleLines := 5
		if termH > 0 && termH < 8 {
			maxVisibleLines = termH - 3
			if maxVisibleLines < 1 {
				maxVisibleLines = 1
			}
		}

		var b strings.Builder
		b.WriteString(ansiHideCursor)

		if prevRendered {
			linesUp := prevCursorRowInWindow + 1
			if linesUp > 0 {
				b.WriteString(fmt.Sprintf("\x1b[%dA", linesUp))
			}
			b.WriteString("\r\x1b[J")
		}

		var top string
		var lines []string
		var bottom string
		var cursorRowInWindow, visCol int
		top, lines, bottom, cursorRowInWindow, visCol, scrollOffset = formatInlineComposerBox(editor, boxWidth, maxVisibleLines, scrollOffset, targetDesc)

		b.WriteString(top + "\r\n")
		for _, l := range lines {
			b.WriteString(l + "\r\n")
		}
		b.WriteString(bottom + "\r\n")

		linesUp := len(lines) + 1 - cursorRowInWindow
		targetCol := 3 + visCol
		if targetCol > boxWidth {
			targetCol = boxWidth
		}

		b.WriteString(fmt.Sprintf("\x1b[%dA\r\x1b[%dG", linesUp, targetCol))
		b.WriteString(ansiShowCursor)

		_, _ = os.Stdout.WriteString(b.String())
		_ = os.Stdout.Sync()

		prevCursorRowInWindow = cursorRowInWindow
		prevRendered = true
	}

	renderBox()

	buf := make([]byte, 1024)
	for {
		n, readErr := os.Stdin.Read(buf)
		if readErr != nil {
			if readErr == io.EOF {
				clearBox()
				return "", true, nil
			}
			return "", false, readErr
		}
		if n == 0 {
			continue
		}

		input := string(buf[:n])

		// Handle bracketed paste blocks: \x1b[2004~ ... \x1b[2004~
		if strings.Contains(input, "\x1b[2004~") {
			parts := strings.Split(input, "\x1b[2004~")
			for i, part := range parts {
				if i%2 == 1 {
					// Inside paste payload: insert all characters verbatim
					editor.InsertText(part)
				} else if part != "" {
					processInput(editor, []byte(part))
				}
			}
			renderBox()
			continue
		}

		shouldSubmit, shouldCancel := processInput(editor, buf[:n])
		if shouldCancel {
			clearBox()
			return "", true, nil
		}
		if shouldSubmit {
			content := editor.Content()
			lower := strings.ToLower(content)
			if lower == "/exit" || lower == "/quit" || lower == "/q" {
				clearBox()
				return "", true, nil
			}

			// Clear the composer box and print clean prompt lines into terminal history
			clearBox()
			for _, line := range editor.Lines {
				_, _ = os.Stdout.WriteString(line + "\r\n")
			}
			_ = os.Stdout.Sync()

			return content, false, nil
		}

		renderBox()
	}
}

// runComposer executes the micro-editor in the alternate screen buffer
// Returns (prompt, isCancelled, error)
func runComposer(title, subtitle string) (string, bool, error) {
	// Enable alternate screen, bracketed paste, and enhanced keyboard
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + ansiEnablePaste + ansiEnableKeyboard)
	_ = os.Stdout.Sync()

	fd := os.Stdin.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		// Cleanup alternate screen on error
		_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiDisableKeyboard + ansiLeaveAltScreen)
		_ = os.Stdout.Sync()
		return "", false, err
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiDisableKeyboard + ansiLeaveAltScreen + ansiShowCursor + "\x1b[0m\r")
			_ = os.Stdout.Sync()
			_ = restoreTerm(fd, state)
		})
	}
	defer cleanup()

	// Handle SIGINT to restore terminal cleanly
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)

	go func() {
		<-sigChan
		cleanup()
		os.Exit(130)
	}()

	editor := NewMicroEditor(title, subtitle)
	editor.Render()

	buf := make([]byte, 1024)

	for {
		n, readErr := os.Stdin.Read(buf)
		if readErr != nil {
			if readErr == io.EOF {
				return "", true, nil
			}
			return "", false, readErr
		}
		if n == 0 {
			continue
		}

		input := string(buf[:n])

		// Handle bracketed paste blocks: \x1b[2004~ ... \x1b[2004~
		if strings.Contains(input, "\x1b[2004~") {
			parts := strings.Split(input, "\x1b[2004~")
			for i, part := range parts {
				if i%2 == 1 {
					// Inside paste payload: insert all characters verbatim
					editor.InsertText(part)
				} else if part != "" {
					processInput(editor, []byte(part))
				}
			}
			editor.Render()
			continue
		}

		shouldSubmit, shouldCancel := processInput(editor, buf[:n])
		if shouldCancel {
			return "", true, nil
		}
		if shouldSubmit {
			content := editor.Content()
			lower := strings.ToLower(content)
			// STRICT: Only commands with leading slash exit (/exit, /quit, /q).
			// Words without slash (like "exit" or "quit") are treated as literal prompt text!
			if lower == "/exit" || lower == "/quit" || lower == "/q" {
				return "", true, nil
			}
			return content, false, nil
		}

		editor.Render()
	}
}

// runInlineTurnEditor executes the micro-editor inline on stdout for chatbot turns
// Returns (prompt, isCancelled, error)
func runInlineTurnEditor() (string, bool, error) {
	fd := os.Stdin.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return "", false, err
	}

	_, _ = os.Stdout.WriteString(ansiEnableKeyboard)
	_ = os.Stdout.Sync()

	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_, _ = os.Stdout.WriteString(ansiDisableKeyboard)
			_ = os.Stdout.Sync()
			_ = restoreTerm(fd, state)
		})
	}
	defer restore()

	editor := NewMicroEditor("", "")
	prevCursorRow := 0
	prevLineCount := 0

	renderInline := func() {
		var b strings.Builder
		b.WriteString(ansiHideCursor)

		// Move cursor back to the start of the first prompt line
		if prevLineCount > 0 {
			if prevCursorRow > 0 {
				b.WriteString(fmt.Sprintf("\x1b[%dA", prevCursorRow))
			}
			b.WriteString("\r\x1b[J") // clear from top prompt line down
		}

		// Draw all prompt lines
		for i, line := range editor.Lines {
			prefix := ">>> "
			if i > 0 {
				prefix = "... "
			}
			b.WriteString(prefix)
			b.WriteString(line)
			b.WriteString("\r\n")
		}

		// Position cursor at (editor.CursorRow, editor.CursorCol)
		linesUp := len(editor.Lines) - editor.CursorRow
		targetCol := 4 + editor.CursorCol + 1 // 1-based ANSI column

		b.WriteString(fmt.Sprintf("\x1b[%dA\r\x1b[%dG", linesUp, targetCol))
		b.WriteString(ansiShowCursor)

		_, _ = os.Stdout.WriteString(b.String())
		_ = os.Stdout.Sync()

		prevCursorRow = editor.CursorRow
		prevLineCount = len(editor.Lines)
	}

	renderInline()

	buf := make([]byte, 1024)
	for {
		n, readErr := os.Stdin.Read(buf)
		if readErr != nil {
			if readErr == io.EOF {
				return "", true, nil
			}
			return "", false, readErr
		}
		if n == 0 {
			continue
		}

		input := string(buf[:n])

		// Handle bracketed paste blocks
		if strings.Contains(input, "\x1b[2004~") {
			parts := strings.Split(input, "\x1b[2004~")
			for i, part := range parts {
				if i%2 == 1 {
					editor.InsertText(part)
				} else if part != "" {
					processInput(editor, []byte(part))
				}
			}
			renderInline()
			continue
		}

		shouldSubmit, shouldCancel := processInput(editor, buf[:n])
		if shouldCancel {
			linesDown := len(editor.Lines) - editor.CursorRow
			if linesDown > 0 {
				_, _ = os.Stdout.WriteString(fmt.Sprintf("\x1b[%dB", linesDown))
			}
			_, _ = os.Stdout.WriteString("\r\n")
			_ = os.Stdout.Sync()
			return "", true, nil
		}

		if shouldSubmit {
			linesDown := len(editor.Lines) - editor.CursorRow
			if linesDown > 0 {
				_, _ = os.Stdout.WriteString(fmt.Sprintf("\x1b[%dB", linesDown))
			}
			_, _ = os.Stdout.WriteString("\r\n")
			_ = os.Stdout.Sync()

			content := editor.Content()
			return content, false, nil
		}

		renderInline()
	}
}

// processInput handles incoming bytes in raw mode
// Returns (shouldSubmit, shouldCancel)
func processInput(editor *MicroEditor, data []byte) (bool, bool) {
	i := 0
	for i < len(data) {
		b := data[i]

		// Ctrl+C (ASCII 3) -> Cancel
		if b == 3 {
			return false, true
		}

		// STRICT: Ctrl+D (ASCII 4) -> Do NOT submit!
		if b == 4 {
			i++
			continue
		}

		// STRICT: Ctrl+Z (ASCII 26) -> Do NOT submit!
		if b == 26 {
			i++
			continue
		}

		// Ctrl+S (ASCII 19) -> Submit prompt
		if b == 19 {
			return true, false
		}

		// Backspace (ASCII 127 or ASCII 8)
		if b == 127 || b == 8 {
			editor.Backspace()
			i++
			continue
		}

		// Enter (ASCII 13 \r) -> Always insert a newline
		if b == 13 {
			editor.InsertNewline()
			i++
			continue
		}

		// Linefeed (ASCII 10 \n)
		// In raw mode, byte 10 when sent independently is Ctrl+Enter (or Ctrl+J)!
		if b == 10 {
			if i > 0 && data[i-1] == 13 {
				// Trailing \n of \r\n from Enter or paste
				i++
				continue
			}
			if len(data) == 1 {
				return true, false
			}
			// Multiline paste without bracketed paste
			editor.InsertNewline()
			i++
			continue
		}

		// ANSI Escape Sequences
		if b == 27 {
			rem := data[i:]
			remStr := string(rem)

			// 1. Check for Ctrl+Enter, Alt+Enter, or Ctrl+S sequences:
			// \x1b[13;5u (CSI u), \x1b[27;5;13~ (xterm), \x1b[10;5u, \x1b[27;5;10~, \x1b[10~, \x1b[13~, \x1b\r, \x1b\n,
			// \x1b[115;5u / \x1b[83;5u (Kitty Ctrl+S), \x1b[27;5;115~ / \x1b[27;5;83~ (xterm Ctrl+S)
			if strings.HasPrefix(remStr, "\x1b[13;5u") ||
				strings.HasPrefix(remStr, "\x1b[27;5;13~") ||
				strings.HasPrefix(remStr, "\x1b[10;5u") ||
				strings.HasPrefix(remStr, "\x1b[27;5;10~") ||
				strings.HasPrefix(remStr, "\x1b[10~") ||
				strings.HasPrefix(remStr, "\x1b[13~") ||
				strings.HasPrefix(remStr, "\x1b\r") ||
				strings.HasPrefix(remStr, "\x1b\n") ||
				strings.HasPrefix(remStr, "\x1b[115;5u") ||
				strings.HasPrefix(remStr, "\x1b[83;5u") ||
				strings.HasPrefix(remStr, "\x1b[27;5;115~") ||
				strings.HasPrefix(remStr, "\x1b[27;5;83~") {
				return true, false
			}

			// 2. Check Arrow keys, Home, End, Delete, PageUp, PageDown
			// Check CSI sequences: \x1b[...
			if len(rem) >= 3 && rem[1] == '[' {
				switch rem[2] {
				case 'A': // Up
					editor.MoveUp()
					i += 3
					continue
				case 'B': // Down
					editor.MoveDown()
					i += 3
					continue
				case 'C': // Right
					editor.MoveRight()
					i += 3
					continue
				case 'D': // Left
					editor.MoveLeft()
					i += 3
					continue
				case 'H': // Home
					editor.CursorCol = 0
					i += 3
					continue
				case 'F': // End
					editor.CursorCol = len([]rune(editor.Lines[editor.CursorRow]))
					i += 3
					continue
				}

				// Check modified keys or tilde sequences: \x1b[...~
				// Delete key: \x1b[3~
				if len(rem) >= 4 && rem[2] == '3' && rem[3] == '~' {
					editor.Delete()
					i += 4
					continue
				}
				// Home: \x1b[1~ or \x1b[7~
				if len(rem) >= 4 && (rem[2] == '1' || rem[2] == '7') && rem[3] == '~' {
					editor.CursorCol = 0
					i += 4
					continue
				}
				// End: \x1b[4~ or \x1b[8~
				if len(rem) >= 4 && (rem[2] == '4' || rem[2] == '8') && rem[3] == '~' {
					editor.CursorCol = len([]rune(editor.Lines[editor.CursorRow]))
					i += 4
					continue
				}

				// Modified arrow keys like \x1b[1;5A (Ctrl+Up), etc.
				if len(rem) >= 6 && rem[2] == '1' && rem[3] == ';' {
					switch rem[5] {
					case 'A':
						editor.MoveUp()
						i += 6
						continue
					case 'B':
						editor.MoveDown()
						i += 6
						continue
					case 'C':
						editor.MoveRight()
						i += 6
						continue
					case 'D':
						editor.MoveLeft()
						i += 6
						continue
					}
				}
			}

			// Check SS3 sequences: \x1bO... (Application mode cursor keys)
			if len(rem) >= 3 && rem[1] == 'O' {
				switch rem[2] {
				case 'A': // Up
					editor.MoveUp()
					i += 3
					continue
				case 'B': // Down
					editor.MoveDown()
					i += 3
					continue
				case 'C': // Right
					editor.MoveRight()
					i += 3
					continue
				case 'D': // Left
					editor.MoveLeft()
					i += 3
					continue
				case 'H': // Home
					editor.CursorCol = 0
					i += 3
					continue
				case 'F': // End
					editor.CursorCol = len([]rune(editor.Lines[editor.CursorRow]))
					i += 3
					continue
				}
			}

			// Unrecognized escape: consume full sequence to prevent bleed!
			seqLen := parseEscapeSeq(rem)
			if seqLen > 0 {
				i += seqLen
			} else {
				i++
			}
			continue
		}

		// Printable characters (decode full UTF-8 rune)
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError && r >= 32 {
			editor.InsertChar(r)
			i += size
			continue
		}

		i++
	}

	return false, false
}
