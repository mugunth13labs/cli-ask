package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

type ChatRegion int

const (
	RegionPrompt  ChatRegion = 0
	RegionHistory ChatRegion = 1
)

type ChatTurn struct {
	Role    string
	Content string
}

type ChatUI struct {
	mu           sync.Mutex
	Width        int
	Height       int
	Config       *Config
	Session      *Session
	Opts         QueryOptions
	OnNeedRender func()

	// Prompt Box state
	PromptLines  []string
	PromptRow    int
	PromptCol    int
	PromptScroll int

	// History state
	Turns         []ChatTurn
	ReversedLines []string
	HistRow       int
	HistCol       int
	HistScroll    int

	ActiveRegion ChatRegion

	// Selection state
	Selecting bool
	SelStartR int
	SelStartC int
	SelEndR   int
	SelEndC   int

	IsStreaming   bool
	ActiveCancel  context.CancelFunc
	StreamContent strings.Builder
}

func NewChatUI(cfg *Config, session *Session, opts QueryOptions, initialPrompt string, w, h int) *ChatUI {
	ui := &ChatUI{
		Width:        w,
		Height:       h,
		Config:       cfg,
		Session:      session,
		Opts:         opts,
		PromptLines:  []string{""},
		PromptRow:    0,
		PromptCol:    0,
		ActiveRegion: RegionPrompt,
		Turns:        []ChatTurn{},
	}

	if session != nil && len(session.Messages) > 0 {
		for _, m := range session.Messages {
			ui.Turns = append(ui.Turns, ChatTurn{Role: m.Role, Content: m.Content})
		}
	}

	if initialPrompt != "" {
		ui.PromptLines = strings.Split(strings.ReplaceAll(initialPrompt, "\r\n", "\n"), "\n")
		ui.PromptRow = len(ui.PromptLines) - 1
		ui.PromptCol = len(ui.PromptLines[ui.PromptRow])
	}

	ui.rebuildReversedLines()
	return ui
}

func (ui *ChatUI) rebuildReversedLines() {
	ui.ReversedLines = []string{}
	contentWidth := ui.Width - 2
	if contentWidth < 4 {
		contentWidth = 4
	}

	// 1. If currently streaming, place live assistant turn at the very top of history!
	if ui.IsStreaming {
		header := fmt.Sprintf("─── ask [%s] (generating...) ───", ui.Config.Model)
		if len(header) > contentWidth {
			header = header[:contentWidth]
		}
		ui.ReversedLines = append(ui.ReversedLines, header)
		wrapped := wrapText(ui.StreamContent.String(), contentWidth)
		ui.ReversedLines = append(ui.ReversedLines, wrapped...)
		ui.ReversedLines = append(ui.ReversedLines, "")
	}

	// 2. Iterate turns in REVERSE chronological order (newest turns appear first!)
	for i := len(ui.Turns) - 1; i >= 0; i-- {
		turn := ui.Turns[i]
		var header string
		if turn.Role == "user" {
			header = "─── You ────────────────────────"
		} else {
			header = fmt.Sprintf("─── ask [%s] ───", ui.Config.Model)
		}
		if len(header) > contentWidth {
			header = header[:contentWidth]
		}
		ui.ReversedLines = append(ui.ReversedLines, header)
		wrapped := wrapText(turn.Content, contentWidth)
		ui.ReversedLines = append(ui.ReversedLines, wrapped...)
		ui.ReversedLines = append(ui.ReversedLines, "") // spacing
	}
}

func (ui *ChatUI) MaxPromptBoxHeight() int {
	maxH := 8
	capH := ui.Height * 35 / 100
	if capH < 3 {
		capH = 3
	}
	if maxH > capH {
		maxH = capH
	}
	return maxH
}

func (ui *ChatUI) PromptVisibleLines() int {
	linesCount := len(ui.PromptLines)
	if linesCount < 1 {
		linesCount = 1
	}
	maxH := ui.MaxPromptBoxHeight()
	if linesCount > maxH {
		return maxH
	}
	return linesCount
}

func (ui *ChatUI) adjustPromptScroll() {
	vis := ui.PromptVisibleLines()
	if ui.PromptRow < ui.PromptScroll {
		ui.PromptScroll = ui.PromptRow
	} else if ui.PromptRow >= ui.PromptScroll+vis {
		ui.PromptScroll = ui.PromptRow - vis + 1
	}
	if ui.PromptScroll < 0 {
		ui.PromptScroll = 0
	}
}

func (ui *ChatUI) adjustHistScroll() {
	histH := ui.Height - ui.PromptVisibleLines() - 2
	if histH < 1 {
		histH = 1
	}
	if ui.HistRow < ui.HistScroll {
		ui.HistScroll = ui.HistRow
	} else if ui.HistRow >= ui.HistScroll+histH {
		ui.HistScroll = ui.HistRow - histH + 1
	}
	maxScroll := len(ui.ReversedLines) - histH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if ui.HistScroll > maxScroll {
		ui.HistScroll = maxScroll
	}
	if ui.HistScroll < 0 {
		ui.HistScroll = 0
	}
}

// RenderRow formats row r (0 <= r < Height) for the Chat UI
func (ui *ChatUI) RenderRow(r int) string {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	visPromptLines := ui.PromptVisibleLines()
	dividerRow := visPromptLines // row right below prompt box

	// 1. Inside Prompt Box (Row 0 .. visPromptLines - 1)
	if r < visPromptLines {
		lineIdx := ui.PromptScroll + r
		prefix := ">>> "
		if lineIdx > 0 {
			prefix = "... "
		}
		text := ""
		if lineIdx >= 0 && lineIdx < len(ui.PromptLines) {
			text = ui.PromptLines[lineIdx]
		}
		raw := prefix + text
		runes := []rune(raw)
		if len(runes) > ui.Width {
			return string(runes[:ui.Width])
		}
		return raw + strings.Repeat(" ", ui.Width-len(runes))
	}

	// 2. Divider Row between Prompt Box and Reversed History
	if r == dividerRow {
		title := "─── History (Down Arrow to Browse) "
		if ui.ActiveRegion == RegionHistory {
			title = "──● History [Browsing / Ctrl+C to Copy] "
		}
		rem := ui.Width - len([]rune(title))
		if rem < 0 {
			rem = 0
		}
		return title + strings.Repeat("─", rem)
	}

	// 3. Reversed History Rows (r > dividerRow)
	histRowIdx := (r - dividerRow - 1) + ui.HistScroll
	if histRowIdx >= 0 && histRowIdx < len(ui.ReversedLines) {
		raw := ui.ReversedLines[histRowIdx]
		runes := []rune(raw)

		var sb strings.Builder
		for c := 0; c < ui.Width; c++ {
			var char rune = ' '
			if c < len(runes) {
				char = runes[c]
			}
			if ui.isHistSelected(histRowIdx, c) {
				sb.WriteString("\x1b[7m")
				sb.WriteRune(char)
				sb.WriteString("\x1b[0m")
			} else {
				sb.WriteRune(char)
			}
		}
		return sb.String()
	}

	return strings.Repeat(" ", ui.Width)
}

func (ui *ChatUI) CursorPosition() (row, col int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	if ui.ActiveRegion == RegionPrompt {
		row = ui.PromptRow - ui.PromptScroll
		col = ui.PromptCol + 4 // prefix ">>> " or "... "
		if col >= ui.Width {
			col = ui.Width - 1
		}
		return row, col
	}

	// RegionHistory
	dividerRow := ui.PromptVisibleLines()
	row = dividerRow + 1 + (ui.HistRow - ui.HistScroll)
	col = ui.HistCol
	if col >= ui.Width {
		col = ui.Width - 1
	}
	return row, col
}

func (ui *ChatUI) isHistSelected(r, c int) bool {
	if !ui.Selecting {
		return false
	}
	sR, sC := ui.SelStartR, ui.SelStartC
	eR, eC := ui.SelEndR, ui.SelEndC
	if sR > eR || (sR == eR && sC > eC) {
		sR, eR = eR, sR
		sC, eC = eC, sC
	}

	if r < sR || r > eR {
		return false
	}
	if r == sR && r == eR {
		return c >= sC && c <= eC
	}
	if r == sR {
		return c >= sC
	}
	if r == eR {
		return c <= eC
	}
	return true
}

func (ui *ChatUI) GetSelectedText() string {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	if !ui.Selecting {
		return ""
	}
	sR, sC := ui.SelStartR, ui.SelStartC
	eR, eC := ui.SelEndR, ui.SelEndC
	if sR > eR || (sR == eR && sC > eC) {
		sR, eR = eR, sR
		sC, eC = eC, sC
	}

	var sb strings.Builder
	for r := sR; r <= eR; r++ {
		if r < 0 || r >= len(ui.ReversedLines) {
			continue
		}
		runes := []rune(ui.ReversedLines[r])
		start := 0
		if r == sR {
			start = sC
		}
		end := len(runes) - 1
		if r == eR {
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
		if r < eR {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (ui *ChatUI) HandleKey(inBuf []byte) (shouldExit bool) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	// 1. Shift+Arrows for text selection in history
	if bytes.HasPrefix(inBuf, []byte("\x1b[1;2")) || bytes.HasPrefix(inBuf, []byte("\x1b[1;6")) {
		dir := inBuf[len(inBuf)-1]
		if ui.ActiveRegion == RegionHistory {
			if !ui.Selecting {
				ui.Selecting = true
				ui.SelStartR = ui.HistRow
				ui.SelStartC = ui.HistCol
			}
			switch dir {
			case 'A': // Up
				if ui.HistRow > 0 {
					ui.HistRow--
				}
			case 'B': // Down
				if ui.HistRow < len(ui.ReversedLines)-1 {
					ui.HistRow++
				}
			case 'C': // Right
				ui.HistCol++
			case 'D': // Left
				if ui.HistCol > 0 {
					ui.HistCol--
				}
			}
			ui.SelEndR = ui.HistRow
			ui.SelEndC = ui.HistCol
			ui.adjustHistScroll()
			return false
		}
	}

	// 2. Ctrl+C: Copy selection if active, cancel generation if streaming, else quit
	if len(inBuf) == 1 && inBuf[0] == 3 {
		if ui.Selecting {
			selected := ui.GetSelectedText()
			if selected != "" {
				copyToClipboard(selected)
			}
			ui.Selecting = false
			return false
		}
		if ui.IsStreaming && ui.ActiveCancel != nil {
			ui.ActiveCancel()
			ui.ActiveCancel = nil
			return false
		}
		return true
	}

	// 3. Arrow Keys: Up, Down, Left, Right
	if bytes.Equal(inBuf, []byte("\x1b[A")) { // Up Arrow
		ui.Selecting = false
		if ui.ActiveRegion == RegionPrompt {
			if ui.PromptRow > 0 {
				ui.PromptRow--
				ui.clampPromptCol()
				ui.adjustPromptScroll()
			}
		} else { // In History
			if ui.HistRow > 0 {
				ui.HistRow--
				ui.adjustHistScroll()
			} else {
				// Transition back up into the Prompt Box!
				ui.ActiveRegion = RegionPrompt
				ui.PromptRow = len(ui.PromptLines) - 1
				ui.clampPromptCol()
				ui.adjustPromptScroll()
			}
		}
		return false
	}

	if bytes.Equal(inBuf, []byte("\x1b[B")) { // Down Arrow
		ui.Selecting = false
		if ui.ActiveRegion == RegionPrompt {
			if ui.PromptRow < len(ui.PromptLines)-1 {
				ui.PromptRow++
				ui.clampPromptCol()
				ui.adjustPromptScroll()
			} else {
				// Transition down into Reversed History!
				if len(ui.ReversedLines) > 0 {
					ui.ActiveRegion = RegionHistory
					ui.HistRow = 0
					ui.HistCol = 0
					ui.HistScroll = 0
				}
			}
		} else { // In History
			if ui.HistRow < len(ui.ReversedLines)-1 {
				ui.HistRow++
				ui.adjustHistScroll()
			}
		}
		return false
	}

	if bytes.Equal(inBuf, []byte("\x1b[D")) { // Left Arrow
		ui.Selecting = false
		if ui.ActiveRegion == RegionPrompt {
			if ui.PromptCol > 0 {
				ui.PromptCol--
			}
		} else {
			if ui.HistCol > 0 {
				ui.HistCol--
			}
		}
		return false
	}

	if bytes.Equal(inBuf, []byte("\x1b[C")) { // Right Arrow
		ui.Selecting = false
		if ui.ActiveRegion == RegionPrompt {
			curLineLen := len([]rune(ui.PromptLines[ui.PromptRow]))
			if ui.PromptCol < curLineLen {
				ui.PromptCol++
			}
		} else {
			ui.HistCol++
		}
		return false
	}

	// 4. Submit: Ctrl+Enter (LF=10, or CSI u \x1b[13;5u / \x1b[27;5;13~)
	isCtrlEnter := (len(inBuf) == 1 && inBuf[0] == 10) ||
		bytes.Equal(inBuf, []byte("\x1b[13;5u")) ||
		bytes.Equal(inBuf, []byte("\x1b[27;5;13~"))

	if isCtrlEnter && ui.ActiveRegion == RegionPrompt {
		fullPrompt := strings.TrimSpace(strings.Join(ui.PromptLines, "\n"))
		if fullPrompt == "" {
			return false
		}

		// Slash commands
		if fullPrompt == "/exit" || fullPrompt == "/quit" || fullPrompt == "/q" {
			return true
		}
		if fullPrompt == "/clear" || fullPrompt == "/c" {
			ui.Turns = []ChatTurn{}
			ui.PromptLines = []string{""}
			ui.PromptRow = 0
			ui.PromptCol = 0
			ui.rebuildReversedLines()
			if ui.Session != nil {
				ui.Session.Messages = []Message{}
				_ = saveSession(ui.Session)
			}
			return false
		}

		// Add turn to history
		ui.Turns = append(ui.Turns, ChatTurn{Role: "user", Content: fullPrompt})
		ui.PromptLines = []string{""}
		ui.PromptRow = 0
		ui.PromptCol = 0
		ui.PromptScroll = 0
		ui.rebuildReversedLines()

		go ui.executeChatQuery(fullPrompt)
		return false
	}

	// 5. In History: ignore typing characters (user must arrow Up into prompt to type)
	if ui.ActiveRegion == RegionHistory {
		return false
	}

	// 6. Enter key (13): Newline in prompt!
	if len(inBuf) == 1 && inBuf[0] == 13 {
		curLine := ui.PromptLines[ui.PromptRow]
		runes := []rune(curLine)
		before := string(runes[:ui.PromptCol])
		after := string(runes[ui.PromptCol:])

		ui.PromptLines[ui.PromptRow] = before
		ui.PromptLines = append(ui.PromptLines[:ui.PromptRow+1], append([]string{after}, ui.PromptLines[ui.PromptRow+1:]...)...)
		ui.PromptRow++
		ui.PromptCol = 0
		ui.adjustPromptScroll()
		return false
	}

	// 7. Backspace (127 or 8)
	if len(inBuf) == 1 && (inBuf[0] == 127 || inBuf[0] == 8) {
		curLine := ui.PromptLines[ui.PromptRow]
		runes := []rune(curLine)

		if ui.PromptCol > 0 {
			ui.PromptLines[ui.PromptRow] = string(append(runes[:ui.PromptCol-1], runes[ui.PromptCol:]...))
			ui.PromptCol--
		} else if ui.PromptRow > 0 {
			prevLine := ui.PromptLines[ui.PromptRow-1]
			prevRunes := []rune(prevLine)
			ui.PromptCol = len(prevRunes)
			ui.PromptLines[ui.PromptRow-1] = prevLine + curLine
			ui.PromptLines = append(ui.PromptLines[:ui.PromptRow], ui.PromptLines[ui.PromptRow+1:]...)
			ui.PromptRow--
		}
		ui.adjustPromptScroll()
		return false
	}

	// 8. Text insertion into prompt line
	i := 0
	for i < len(inBuf) {
		r, size := utf8.DecodeRune(inBuf[i:])
		if r != utf8.RuneError && r >= 32 {
			curLine := ui.PromptLines[ui.PromptRow]
			runes := []rune(curLine)
			if ui.PromptCol > len(runes) {
				ui.PromptCol = len(runes)
			}
			ui.PromptLines[ui.PromptRow] = string(append(runes[:ui.PromptCol], append([]rune{r}, runes[ui.PromptCol:]...)...))
			ui.PromptCol++
		}
		i += size
	}
	ui.adjustPromptScroll()
	return false
}

func (ui *ChatUI) clampPromptCol() {
	if ui.PromptRow < 0 {
		ui.PromptRow = 0
	}
	if ui.PromptRow >= len(ui.PromptLines) {
		ui.PromptRow = len(ui.PromptLines) - 1
	}
	curLineLen := len([]rune(ui.PromptLines[ui.PromptRow]))
	if ui.PromptCol > curLineLen {
		ui.PromptCol = curLineLen
	}
}

func (ui *ChatUI) executeChatQuery(prompt string) {
	ui.mu.Lock()
	ui.IsStreaming = true
	ui.StreamContent.Reset()

	var apiMessages []Message
	activePrompt := systemPrompt
	if ui.Opts.IsCodeGen {
		activePrompt = codePrompt
	}
	apiMessages = append(apiMessages, Message{Role: "system", Content: activePrompt})

	if len(ui.Session.Messages) > 0 {
		compacted := compactHistory(ui.Session.Messages)
		apiMessages = append(apiMessages, compacted...)
	}
	apiMessages = append(apiMessages, Message{Role: "user", Content: prompt})

	queryCtx, cancel := context.WithCancel(context.Background())
	ui.ActiveCancel = cancel
	ui.rebuildReversedLines()
	ui.mu.Unlock()

	if ui.OnNeedRender != nil {
		ui.OnNeedRender()
	}

	streamOpts := ui.Opts
	streamOpts.OnToken = func(tok string) {
		ui.mu.Lock()
		defer ui.mu.Unlock()

		ui.StreamContent.WriteString(tok)
		ui.rebuildReversedLines()

		if ui.OnNeedRender != nil {
			ui.OnNeedRender()
		}
	}

	response, queryErr := queryOpenRouter(queryCtx, ui.Config, apiMessages, streamOpts)

	ui.mu.Lock()
	ui.IsStreaming = false
	ui.ActiveCancel = nil

	if response != "" {
		ui.Turns = append(ui.Turns, ChatTurn{Role: "assistant", Content: response})
		ui.Session.Messages = append(ui.Session.Messages,
			Message{Role: "user", Content: prompt},
			Message{Role: "assistant", Content: response},
		)
		_ = saveSession(ui.Session)
	} else if queryErr != nil && queryErr != context.Canceled {
		ui.Turns = append(ui.Turns, ChatTurn{Role: "assistant", Content: fmt.Sprintf("[Error: %v]", queryErr)})
	}
	ui.StreamContent.Reset()
	ui.rebuildReversedLines()
	ui.mu.Unlock()

	if ui.OnNeedRender != nil {
		ui.OnNeedRender()
	}
}
