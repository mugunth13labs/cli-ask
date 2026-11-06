package cli

import (
	"bytes"
	"strings"
)

// Standardized ANSI color palette
const (
	ColorNavyBg   = "\x1b[48;5;17m"
	ColorCyan     = "\x1b[38;5;39m"
	ColorAmber    = "\x1b[38;5;214m"
	ColorGreen    = "\x1b[38;5;46m"
	ColorWhite    = "\x1b[97m"
	ColorGray     = "\x1b[90m"
	AnsiReset     = "\x1b[0m"
	AnsiBold      = "\x1b[1m"
	AnsiInverse   = "\x1b[7m"
	AnsiClearLine = "\r\x1b[K"
)

// Standardized terminal control sequences
const (
	ansiEnterAltScreen  = "\x1b[?1049h\x1b[H\x1b[2J"
	ansiLeaveAltScreen  = "\x1b[?1049l"
	ansiEnablePaste     = "\x1b[?2004h"
	ansiDisablePaste    = "\x1b[?2004l"
	ansiHideCursor      = "\x1b[?25l"
	ansiShowCursor      = "\x1b[?25h"
	ansiClearScreen     = "\x1b[H\x1b[2J"
	ansiEnableKeyboard  = "\x1b[>1u\x1b[>4;2m"
	ansiDisableKeyboard = "\x1b[<u\x1b[>4;0m"
)

// SubmitKeySequences represents all recognized terminal escape sequences
// for Alt+RightArrow (primary), Ctrl+Enter, Alt+Enter, and Ctrl+S (fallbacks).
var SubmitKeySequences = [][]byte{
	[]byte("\x1b[1;3C"),    // Alt+Right (standard xterm/vt100)
	[]byte("\x1b\x1b[C"),   // Alt+Right (macOS Terminal / Terminal.app)
	[]byte("\x1b[13;5u"),   // Ctrl+Enter (CSI u)
	[]byte("\x1b[27;5;13~"), // Ctrl+Enter (xterm)
	[]byte("\x1b[10;5u"),   // Ctrl+Enter / Ctrl+J (CSI u)
	[]byte("\x1b[27;5;10~"), // Ctrl+Enter / Ctrl+J (xterm)
	[]byte("\x1b[10~"),     // Enter modified (xterm)
	[]byte("\x1b[13~"),     // Enter modified (xterm)
	[]byte("\x1b\r"),       // Alt+Enter (ESC CR)
	[]byte("\x1b\n"),       // Alt+Enter (ESC LF)
	[]byte("\x1b[115;5u"),  // Ctrl+S (Kitty CSI u)
	[]byte("\x1b[83;5u"),   // Ctrl+Shift+S (Kitty CSI u)
	[]byte("\x1b[27;5;115~"), // Ctrl+S (xterm)
	[]byte("\x1b[27;5;83~"),  // Ctrl+Shift+S (xterm)
}

// IsSubmitKeySequence checks whether an exact escape sequence matches any submit sequence.
func IsSubmitKeySequence(seq []byte) bool {
	for _, target := range SubmitKeySequences {
		if bytes.Equal(seq, target) {
			return true
		}
	}
	return false
}

// HasSubmitPrefix checks whether rem begins with any submit key sequence.
// Returns (true, length) if matched, or (false, 0) otherwise.
func HasSubmitPrefix(rem []byte) (bool, int) {
	for _, target := range SubmitKeySequences {
		if bytes.HasPrefix(rem, target) {
			return true, len(target)
		}
	}
	return false, 0
}

// StripSubmitKeySequences removes any embedded submit escape sequences from a cooked line.
// Returns the cleaned string and true if any submit sequence was found.
func StripSubmitKeySequences(s string) (string, bool) {
	found := false
	for _, target := range SubmitKeySequences {
		tStr := string(target)
		if strings.Contains(s, tStr) {
			found = true
			s = strings.ReplaceAll(s, tStr, "")
		}
	}
	return s, found
}

// ParseEscapeSeq determines the byte length of an ANSI escape sequence at the beginning of b.
func ParseEscapeSeq(b []byte) int {
	if len(b) == 0 || b[0] != 0x1b {
		return 0
	}
	if len(b) == 1 {
		return 1
	}
	if b[1] == 0x1b {
		// ESC ESC ... (e.g. \x1b\x1b[C for Alt+Right on macOS)
		if len(b) >= 3 && b[2] == '[' {
			for i := 3; i < len(b); i++ {
				if b[i] >= 0x40 && b[i] <= 0x7E {
					return i + 1
				}
			}
			return len(b)
		}
		return 2
	}
	if b[1] == '[' {
		// CSI sequence: \x1b[ ... finalByte (0x40-0x7E)
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7E {
				return i + 1
			}
		}
		return len(b)
	}
	if b[1] == 'O' {
		// SS3 sequence: \x1bO ... (e.g. \x1bOA, \x1bOB)
		if len(b) >= 3 {
			return 3
		}
		return 2
	}
	if b[1] == ']' {
		// OSC sequence: \x1b] ... (\x07 or \x1b\)
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2
			}
		}
		return len(b)
	}
	return 2
}
