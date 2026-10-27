package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
)

const (
	ansiEnterAltScreen = "\x1b[?1049h\x1b[H\x1b[2J"
	ansiLeaveAltScreen = "\x1b[?1049l"
	ansiEnablePaste    = "\x1b[?2004h"
	ansiDisablePaste   = "\x1b[?2004l"
	ansiHideCursor     = "\x1b[?25l"
	ansiShowCursor     = "\x1b[?25h"
	ansiClearScreen    = "\x1b[H\x1b[2J"
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

	// Insert new row after current row
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
		newRunes := append(runes[:e.CursorCol-1], runes[e.CursorCol:]...)
		e.Lines[e.CursorRow] = string(newRunes)
		e.CursorCol--
	} else if e.CursorRow > 0 {
		// Cross-line backspace: merge current line into previous line
		prevLine := e.Lines[e.CursorRow-1]
		currLine := e.Lines[e.CursorRow]

		prevRunes := []rune(prevLine)
		newCursorCol := len(prevRunes)

		e.Lines[e.CursorRow-1] = prevLine + currLine

		// Remove current line
		e.Lines = append(e.Lines[:e.CursorRow], e.Lines[e.CursorRow+1:]...)
		e.CursorRow--
		e.CursorCol = newCursorCol
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

// runComposer executes the micro-editor in the alternate screen buffer
// Returns (prompt, isCancelled, error)
func runComposer(title, subtitle string) (string, bool, error) {
	// Enable alternate screen and bracketed paste
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + ansiEnablePaste)
	_ = os.Stdout.Sync()

	fd := os.Stdin.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		// Cleanup alternate screen on error
		_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiLeaveAltScreen)
		_ = os.Stdout.Sync()
		return "", false, err
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
				break
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
					// Handle any characters before or after paste marker
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
			if lower == "exit" || lower == "quit" || lower == "/q" || lower == "/exit" || lower == "/quit" {
				return "", true, nil
			}
			return content, false, nil
		}

		editor.Render()
	}

	content := editor.Content()
	return content, false, nil
}

// processInput handles incoming bytes in raw mode
// Returns (shouldSubmit, shouldCancel)
func processInput(editor *MicroEditor, data []byte) (bool, bool) {
	i := 0
	for i < len(data) {
		b := data[i]

		// Ctrl+C (ASCII 3)
		if b == 3 {
			return false, true
		}

		// Ctrl+D (ASCII 4) -> Submit
		if b == 4 {
			return true, false
		}

		// Backspace (ASCII 127 or ASCII 8)
		if b == 127 || b == 8 {
			editor.Backspace()
			i++
			continue
		}

		// Enter (ASCII 13 \r)
		if b == 13 {
			editor.InsertNewline()
			i++
			continue
		}

		// Linefeed (ASCII 10 \n)
		// In raw mode, byte 10 when sent independently is Ctrl+Enter!
		if b == 10 {
			return true, false
		}

		// ANSI Escape Sequences
		if b == 27 {
			rem := data[i:]
			// Check for Ctrl+Enter sequences: \x1b[13;5u or \x1b[27;5;13~ or \x1b[10~
			if strings.HasPrefix(string(rem), "\x1b[13;5u") {
				return true, false
			}
			if strings.HasPrefix(string(rem), "\x1b[27;5;13~") {
				return true, false
			}
			if strings.HasPrefix(string(rem), "\x1b[10~") {
				return true, false
			}

			// Arrow keys
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

				// Check Delete key: \x1b[3~
				if len(rem) >= 4 && rem[2] == '3' && rem[3] == '~' {
					line := editor.Lines[editor.CursorRow]
					runes := []rune(line)
					if editor.CursorCol < len(runes) {
						newRunes := append(runes[:editor.CursorCol], runes[editor.CursorCol+1:]...)
						editor.Lines[editor.CursorRow] = string(newRunes)
					} else if editor.CursorRow < len(editor.Lines)-1 {
						// Merge next line
						editor.Lines[editor.CursorRow] = line + editor.Lines[editor.CursorRow+1]
						editor.Lines = append(editor.Lines[:editor.CursorRow+1], editor.Lines[editor.CursorRow+2:]...)
					}
					i += 4
					continue
				}
			}

			// Unrecognized escape: skip ESC
			i++
			continue
		}

		// Printable UTF-8 runes
		if b >= 32 {
			editor.InsertChar(rune(b))
		}

		i++
	}

	return false, false
}
