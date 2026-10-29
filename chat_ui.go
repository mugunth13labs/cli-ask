package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type ChatRegion int

const (
	RegionPrompt  ChatRegion = 0
	RegionHistory ChatRegion = 1
)

type ChatTurn struct {
	Role      string
	Content   string
	Timestamp string // "15:04:05"
}

type HistLine struct {
	Text            string
	IsBanner        bool
	BannerKind      string // "USER", "AI", "STREAM", "END", "LEAN_LINE", "USER_LINE"
	EndsWithNewline bool
}

type VisPromptLine struct {
	LogicalRow int
	StartCol   int
	EndCol     int
	Text       string
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
	ReversedLines []HistLine
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

func (ui *ChatUI) Resize(w, h int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	ui.Width = w
	ui.Height = h
	ui.rebuildReversedLines()
	ui.adjustPromptScroll()
	ui.adjustHistScroll()
}

func (ui *ChatUI) getVisualPromptLines(maxWidth int) []VisPromptLine {
	if maxWidth < 4 {
		maxWidth = 4
	}
	var vis []VisPromptLine

	for lIdx, line := range ui.PromptLines {
		runes := []rune(line)
		if len(runes) == 0 {
			vis = append(vis, VisPromptLine{LogicalRow: lIdx, StartCol: 0, EndCol: 0, Text: ""})
			continue
		}

		offset := 0
		for offset < len(runes) {
			rem := len(runes) - offset
			if rem <= maxWidth {
				vis = append(vis, VisPromptLine{
					LogicalRow: lIdx,
					StartCol:   offset,
					EndCol:     len(runes),
					Text:       string(runes[offset:]),
				})
				break
			}

			// Try to break at word boundary
			breakIdx := offset + maxWidth
			foundSpace := false
			for i := offset + maxWidth; i > offset+maxWidth/2; i-- {
				if runes[i] == ' ' || runes[i] == '\t' {
					breakIdx = i + 1
					foundSpace = true
					break
				}
			}

			if !foundSpace {
				breakIdx = offset + maxWidth
			}

			vis = append(vis, VisPromptLine{
				LogicalRow: lIdx,
				StartCol:   offset,
				EndCol:     breakIdx,
				Text:       string(runes[offset:breakIdx]),
			})
			offset = breakIdx
		}
	}
	if len(vis) == 0 {
		vis = append(vis, VisPromptLine{LogicalRow: 0, StartCol: 0, EndCol: 0, Text: ""})
	}
	return vis
}

func (ui *ChatUI) cursorToVis(vis []VisPromptLine) (visRow, visCol int) {
	for i, v := range vis {
		if v.LogicalRow == ui.PromptRow {
			isLast := (i == len(vis)-1) || (vis[i+1].LogicalRow != ui.PromptRow)
			if ui.PromptCol >= v.StartCol && (ui.PromptCol < v.EndCol || (isLast && ui.PromptCol <= v.EndCol)) {
				return i, ui.PromptCol - v.StartCol
			}
		}
	}
	if len(vis) > 0 {
		lastIdx := len(vis) - 1
		return lastIdx, len([]rune(vis[lastIdx].Text))
	}
	return 0, 0
}

func (ui *ChatUI) visToCursor(vis []VisPromptLine, targetVisRow, targetVisCol int) {
	if targetVisRow < 0 {
		targetVisRow = 0
	}
	if targetVisRow >= len(vis) {
		targetVisRow = len(vis) - 1
	}
	v := vis[targetVisRow]
	ui.PromptRow = v.LogicalRow
	lineRunes := len([]rune(v.Text))
	col := targetVisCol
	if col > lineRunes {
		col = lineRunes
	}
	ui.PromptCol = v.StartCol + col
	curLogicalLen := len([]rune(ui.PromptLines[ui.PromptRow]))
	if ui.PromptCol > curLogicalLen {
		ui.PromptCol = curLogicalLen
	}
}

func formatCenteredLine(title string, width int) string {
	titleWithSpaces := " " + title + " "
	tLen := len([]rune(titleWithSpaces))
	if tLen >= width {
		return string([]rune(titleWithSpaces)[:width])
	}
	leftDash := (width - tLen) / 2
	rightDash := width - tLen - leftDash
	return strings.Repeat("─", leftDash) + titleWithSpaces + strings.Repeat("─", rightDash)
}

func (ui *ChatUI) rebuildReversedLines() {
	ui.ReversedLines = []HistLine{}
	contentWidth := ui.Width - 2
	if contentWidth < 4 {
		contentWidth = 4
	}

	// 1. If currently streaming, place live assistant turn at the very top of history!
	if ui.IsStreaming {
		// Static right-aligned "generating" (no animated dots)
		label := "generating"
		rem := contentWidth - len([]rune(label)) - 1
		if rem < 0 {
			rem = 0
		}
		streamHeader := strings.Repeat("─", rem) + " " + label
		ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: streamHeader, IsBanner: true, BannerKind: "LEAN_LINE", EndsWithNewline: true})

		wrapped := wrapTextLines(ui.StreamContent.String(), contentWidth)
		for _, w := range wrapped {
			ui.ReversedLines = append(ui.ReversedLines, HistLine{
				Text:            w.Text,
				EndsWithNewline: w.EndsWithNewline,
			})
		}
		ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: "", EndsWithNewline: true}) // spacing
	}

	// 2. Iterate turns in REVERSE chronological order (newest turns appear first!)
	for i := len(ui.Turns) - 1; i >= 0; i-- {
		turn := ui.Turns[i]
		if turn.Role == "assistant" {
			// Lean top bar: right-aligned timestamp "hh:mm:ss" (strictly nothing more)
			ts := turn.Timestamp
			if ts == "" {
				ts = time.Now().Format("15:04:05")
			}
			rem := contentWidth - len([]rune(ts)) - 1
			if rem < 0 {
				rem = 0
			}
			startHeader := strings.Repeat("─", rem) + " " + ts
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: startHeader, IsBanner: true, BannerKind: "LEAN_LINE", EndsWithNewline: true})

			wrapped := wrapTextLines(turn.Content, contentWidth)
			for _, w := range wrapped {
				ui.ReversedLines = append(ui.ReversedLines, HistLine{
					Text:            w.Text,
					EndsWithNewline: w.EndsWithNewline,
				})
			}
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: "", EndsWithNewline: true}) // spacing
		} else {
			// User Turn: left-aligned "─ prompt hh:mm:ss ──────"
			ts := turn.Timestamp
			if ts == "" {
				ts = time.Now().Format("15:04:05")
			}
			headerText := "─ prompt " + ts + " "
			rem := contentWidth - len([]rune(headerText))
			if rem < 0 {
				rem = 0
			}
			startLine := headerText + strings.Repeat("─", rem)
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: startLine, IsBanner: true, BannerKind: "USER_LINE", EndsWithNewline: true})

			wrapped := wrapTextLines(turn.Content, contentWidth)
			for _, w := range wrapped {
				ui.ReversedLines = append(ui.ReversedLines, HistLine{
					Text:            w.Text,
					EndsWithNewline: w.EndsWithNewline,
				})
			}
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: "", EndsWithNewline: true}) // spacing
		}
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
	vis := ui.getVisualPromptLines(ui.Width)
	linesCount := len(vis)
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
	vis := ui.getVisualPromptLines(ui.Width)
	visRow, _ := ui.cursorToVis(vis)
	visCount := len(vis)
	maxH := ui.MaxPromptBoxHeight()
	visH := visCount
	if visH > maxH {
		visH = maxH
	}

	if visRow < ui.PromptScroll {
		ui.PromptScroll = visRow
	} else if visRow >= ui.PromptScroll+visH {
		ui.PromptScroll = visRow - visH + 1
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
		vis := ui.getVisualPromptLines(ui.Width)
		visIdx := ui.PromptScroll + r
		text := ""
		if visIdx >= 0 && visIdx < len(vis) {
			text = vis[visIdx].Text
		}
		runes := []rune(text)
		if len(runes) > ui.Width {
			text = string(runes[:ui.Width])
		} else {
			text = text + strings.Repeat(" ", ui.Width-len(runes))
		}
		return "\x1b[48;5;236m" + text + "\x1b[0m"
	}

	// 2. Divider Row between Prompt Box and Reversed History
	if r == dividerRow {
		title := "─ history "
		if ui.ActiveRegion == RegionHistory {
			if ui.Selecting {
				title = "─ history [ y/Enter: copy | Esc: clear ] "
			} else {
				title = "─ history [ ↑ to prompt | Shift+Arrow: select ] "
			}
		}
		rem := ui.Width - len([]rune(title))
		if rem < 0 {
			title = "─ history "
			rem = ui.Width - len([]rune(title))
			if rem < 0 {
				rem = 0
			}
		}
		return "\x1b[90m" + title + strings.Repeat("─", rem) + "\x1b[0m"
	}

	// 3. Reversed History Rows (r > dividerRow)
	histRowIdx := (r - dividerRow - 1) + ui.HistScroll
	if histRowIdx >= 0 && histRowIdx < len(ui.ReversedLines) {
		line := ui.ReversedLines[histRowIdx]
		if line.IsBanner {
			rawRunes := []rune(line.Text)
			if len(rawRunes) > ui.Width {
				rawRunes = rawRunes[:ui.Width]
			}
			rem := ui.Width - len(rawRunes)
			if rem < 0 {
				rem = 0
			}

			var sb strings.Builder
			switch line.BannerKind {
			case "LEAN_LINE":
				// Lean horizontal rule in dimmed gray (\x1b[90m)
				sb.WriteString("\x1b[90m")
				sb.WriteString(string(rawRunes))
				sb.WriteString(strings.Repeat("─", rem))
				sb.WriteString("\x1b[0m")
			case "USER_LINE":
				// Normal line with YOU START / YOU END in the middle
				sb.WriteString("\x1b[90m")
				sb.WriteString(string(rawRunes))
				sb.WriteString(strings.Repeat("─", rem))
				sb.WriteString("\x1b[0m")
			default:
				sb.WriteString("\x1b[7m")
				sb.WriteString(string(rawRunes))
				sb.WriteString(strings.Repeat(" ", rem))
				sb.WriteString("\x1b[0m")
			}
			return sb.String()
		}

		raw := line.Text
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
		vis := ui.getVisualPromptLines(ui.Width)
		visRow, visCol := ui.cursorToVis(vis)
		row = visRow - ui.PromptScroll
		col = visCol
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

func (ui *ChatUI) getSelectedTextLocked() string {
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
		rawLine := ui.ReversedLines[r].Text
		runes := []rune(rawLine)
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
			if ui.ReversedLines[r].EndsWithNewline {
				sb.WriteString("\n")
			} else {
				sb.WriteString(" ")
			}
		}
	}
	return sb.String()
}

func (ui *ChatUI) FinishSelection() string {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	if !ui.Selecting {
		return ""
	}
	return ui.getSelectedTextLocked()
}

func (ui *ChatUI) ScrollHistory(delta int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	histH := ui.Height - ui.PromptVisibleLines() - 2
	maxScroll := len(ui.ReversedLines) - histH
	if maxScroll < 0 {
		maxScroll = 0
	}
	ui.HistScroll += delta
	if ui.HistScroll < 0 {
		ui.HistScroll = 0
	}
	if ui.HistScroll > maxScroll {
		ui.HistScroll = maxScroll
	}
}

func (ui *ChatUI) HandleClick(col, row int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	visPrompt := ui.PromptVisibleLines()
	if row < visPrompt {
		ui.ActiveRegion = RegionPrompt
		vis := ui.getVisualPromptLines(ui.Width)
		targetVisRow := ui.PromptScroll + row
		ui.visToCursor(vis, targetVisRow, col)
		ui.Selecting = false
	} else if row > visPrompt {
		ui.ActiveRegion = RegionHistory
		histIdx := (row - visPrompt - 1) + ui.HistScroll
		if histIdx < 0 {
			histIdx = 0
		}
		if histIdx >= len(ui.ReversedLines) {
			histIdx = len(ui.ReversedLines) - 1
		}
		ui.HistRow = histIdx
		ui.HistCol = col
		ui.Selecting = true
		ui.SelStartR = histIdx
		ui.SelStartC = col
		ui.SelEndR = histIdx
		ui.SelEndC = col
	}
}

func (ui *ChatUI) HandleDrag(col, row, maxW int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	if !ui.Selecting {
		return
	}
	clampedCol := col
	if clampedCol < 0 {
		clampedCol = 0
	}
	if clampedCol >= maxW {
		clampedCol = maxW - 1
	}
	visPrompt := ui.PromptVisibleLines()
	histIdx := (row - visPrompt - 1) + ui.HistScroll
	if histIdx < 0 {
		histIdx = 0
	}
	if histIdx >= len(ui.ReversedLines) {
		histIdx = len(ui.ReversedLines) - 1
	}
	ui.SelEndR = histIdx
	ui.SelEndC = clampedCol
}

func (ui *ChatUI) GetSelectedText() string {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.getSelectedTextLocked()
}

func (ui *ChatUI) insertNewline() {
	curLine := ui.PromptLines[ui.PromptRow]
	runes := []rune(curLine)
	if ui.PromptCol > len(runes) {
		ui.PromptCol = len(runes)
	}
	before := string(runes[:ui.PromptCol])
	after := string(runes[ui.PromptCol:])

	ui.PromptLines[ui.PromptRow] = before
	ui.PromptLines = append(ui.PromptLines[:ui.PromptRow+1], append([]string{after}, ui.PromptLines[ui.PromptRow+1:]...)...)
	ui.PromptRow++
	ui.PromptCol = 0
}

func (ui *ChatUI) insertText(s string) {
	inBuf := []byte(s)
	i := 0
	for i < len(inBuf) {
		r, size := utf8.DecodeRune(inBuf[i:])
		if r == '\r' {
			if i+size < len(inBuf) && inBuf[i+size] == '\n' {
				size++
			}
			ui.insertNewline()
		} else if r == '\n' {
			ui.insertNewline()
		} else if r != utf8.RuneError && r >= 32 {
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
}

func parseEscapeSeq(b []byte) int {
	if len(b) == 0 || b[0] != 0x1b {
		return 0
	}
	if len(b) == 1 {
		return 1
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
		// SS3 sequence: \x1bO <finalByte>
		if len(b) >= 3 {
			return 3
		}
		return len(b)
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
	// 2-byte escape, e.g. \x1b1, \x1b2, etc.
	return 2
}

func (ui *ChatUI) handleUpArrow() {
	ui.Selecting = false
	if ui.ActiveRegion == RegionPrompt {
		vis := ui.getVisualPromptLines(ui.Width)
		visRow, visCol := ui.cursorToVis(vis)
		if visRow > 0 {
			ui.visToCursor(vis, visRow-1, visCol)
			ui.adjustPromptScroll()
		}
	} else { // In History
		if ui.HistRow > 0 {
			ui.HistRow--
			ui.adjustHistScroll()
		} else {
			// Transition back up into the Prompt Box!
			ui.ActiveRegion = RegionPrompt
			vis := ui.getVisualPromptLines(ui.Width)
			ui.visToCursor(vis, len(vis)-1, 0)
			ui.adjustPromptScroll()
		}
	}
}

func (ui *ChatUI) handleDownArrow() {
	ui.Selecting = false
	if ui.ActiveRegion == RegionPrompt {
		vis := ui.getVisualPromptLines(ui.Width)
		visRow, visCol := ui.cursorToVis(vis)
		if visRow < len(vis)-1 {
			ui.visToCursor(vis, visRow+1, visCol)
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
}

func (ui *ChatUI) handleLeftArrow() {
	ui.Selecting = false
	if ui.ActiveRegion == RegionPrompt {
		if ui.PromptCol > 0 {
			ui.PromptCol--
		} else if ui.PromptRow > 0 {
			ui.PromptRow--
			ui.PromptCol = len([]rune(ui.PromptLines[ui.PromptRow]))
		}
		ui.adjustPromptScroll()
	} else {
		if ui.HistCol > 0 {
			ui.HistCol--
		}
	}
}

func (ui *ChatUI) handleRightArrow() {
	ui.Selecting = false
	if ui.ActiveRegion == RegionPrompt {
		curLineLen := len([]rune(ui.PromptLines[ui.PromptRow]))
		if ui.PromptCol < curLineLen {
			ui.PromptCol++
		} else if ui.PromptRow < len(ui.PromptLines)-1 {
			ui.PromptRow++
			ui.PromptCol = 0
		}
		ui.adjustPromptScroll()
	} else {
		ui.HistCol++
	}
}

func (ui *ChatUI) submitPrompt() (shouldExit bool) {
	fullPrompt := strings.TrimSpace(strings.Join(ui.PromptLines, "\n"))
	if fullPrompt == "" {
		return false
	}
	if fullPrompt == "/exit" || fullPrompt == "/quit" || fullPrompt == "/q" {
		return true
	}
	if fullPrompt == "/help" || fullPrompt == "/?" {
		ui.PromptLines = []string{""}
		ui.PromptRow = 0
		ui.PromptCol = 0
		ui.PromptScroll = 0
		helpContent := "ask chat commands:\n" +
			"  /clear, /c         Clear chat history & session context\n" +
			"  /model <name>      Switch AI model on the fly (or view current)\n" +
			"  /help, /?          Show this help message\n" +
			"  /exit, /quit, /q   Exit ask chat"
		nowTs := time.Now().Format("15:04:05")
		ui.Turns = append(ui.Turns,
			ChatTurn{Role: "user", Content: fullPrompt, Timestamp: nowTs},
			ChatTurn{Role: "assistant", Content: helpContent, Timestamp: nowTs},
		)
		ui.rebuildReversedLines()
		return false
	}
	if strings.HasPrefix(fullPrompt, "/model") {
		parts := strings.Fields(fullPrompt)
		nowTs := time.Now().Format("15:04:05")
		ui.PromptLines = []string{""}
		ui.PromptRow = 0
		ui.PromptCol = 0
		ui.PromptScroll = 0

		var reply string
		if len(parts) == 1 {
			reply = fmt.Sprintf("Current model: %s", ui.Config.Model)
		} else {
			newModel := parts[1]
			ui.Config.Model = newModel
			reply = fmt.Sprintf("Model switched to: %s", newModel)
		}
		ui.Turns = append(ui.Turns,
			ChatTurn{Role: "user", Content: fullPrompt, Timestamp: nowTs},
			ChatTurn{Role: "assistant", Content: reply, Timestamp: nowTs},
		)
		ui.rebuildReversedLines()
		return false
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
	nowTs := time.Now().Format("15:04:05")
	ui.Turns = append(ui.Turns, ChatTurn{Role: "user", Content: fullPrompt, Timestamp: nowTs})
	ui.PromptLines = []string{""}
	ui.PromptRow = 0
	ui.PromptCol = 0
	ui.PromptScroll = 0
	ui.rebuildReversedLines()
	go ui.executeChatQuery(fullPrompt)
	return false
}

func (ui *ChatUI) HandleKey(inBuf []byte) (shouldExit bool) {
	// Guard against SGR mouse escape sequences reaching key handler
	if bytes.HasPrefix(inBuf, []byte("\x1b[<")) {
		return false
	}

	ui.mu.Lock()
	defer ui.mu.Unlock()

	offset := 0
	for offset < len(inBuf) {
		chunk := inBuf[offset:]

		// 1. Escape Sequence Handler
		if chunk[0] == 0x1b {
			seqLen := parseEscapeSeq(chunk)
			seq := chunk[:seqLen]
			offset += seqLen

			// 1a. Arrow Keys: Up, Down, Left, Right (both standard CSI and SS3/DECCKM)
			if bytes.Equal(seq, []byte("\x1b[A")) || bytes.Equal(seq, []byte("\x1bOA")) {
				ui.handleUpArrow()
				continue
			}
			if bytes.Equal(seq, []byte("\x1b[B")) || bytes.Equal(seq, []byte("\x1bOB")) {
				ui.handleDownArrow()
				continue
			}
			if bytes.Equal(seq, []byte("\x1b[D")) || bytes.Equal(seq, []byte("\x1bOD")) {
				ui.handleLeftArrow()
				continue
			}
			if bytes.Equal(seq, []byte("\x1b[C")) || bytes.Equal(seq, []byte("\x1bOC")) {
				ui.handleRightArrow()
				continue
			}

			// 1b. Shift+Arrows for selection in history
			if bytes.HasPrefix(seq, []byte("\x1b[1;2")) || bytes.HasPrefix(seq, []byte("\x1b[1;6")) {
				dir := seq[len(seq)-1]
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
				}
				continue
			}

			// 1c. Ctrl+Enter via CSI u: \x1b[13;5u or \x1b[27;5;13~
			if bytes.Equal(seq, []byte("\x1b[13;5u")) || bytes.Equal(seq, []byte("\x1b[27;5;13~")) {
				if ui.submitPrompt() {
					return true
				}
				continue
			}

			// 1d. Ctrl+Shift+C (CSI u)
			if bytes.Equal(seq, []byte("\x1b[67;6u")) || bytes.Equal(seq, []byte("\x1b[99;6u")) ||
				bytes.Equal(seq, []byte("\x1b[27;6;67~")) || bytes.Equal(seq, []byte("\x1b[27;6;99~")) {
				if ui.Selecting {
					selected := ui.getSelectedTextLocked()
					if selected != "" {
						copyToClipboard(selected)
					}
				}
				continue
			}

			// 1e. Ctrl+Shift+V (CSI u)
			if bytes.Equal(seq, []byte("\x1b[86;6u")) || bytes.Equal(seq, []byte("\x1b[118;6u")) ||
				bytes.Equal(seq, []byte("\x1b[27;6;86~")) || bytes.Equal(seq, []byte("\x1b[27;6;118~")) {
				if ui.ActiveRegion == RegionPrompt {
					clipText := readClipboard()
					if clipText != "" {
						ui.insertText(clipText)
						ui.adjustPromptScroll()
					}
				}
				continue
			}

			// Unrecognized escape sequences: safely consumed and discarded (never inserted as text!)
			continue
		}

		// 2. Non-Escape input
		b := chunk[0]

		// Ctrl+C (3): Cancel stream or exit
		if b == 3 {
			offset++
			if ui.IsStreaming && ui.ActiveCancel != nil {
				ui.ActiveCancel()
				ui.ActiveCancel = nil
				continue
			}
			return true
		}

		// Ctrl+X (24): Copy selection
		if b == 24 {
			offset++
			if ui.Selecting {
				selected := ui.getSelectedTextLocked()
				if selected != "" {
					copyToClipboard(selected)
				}
			}
			continue
		}

		// In History Region:
		if ui.ActiveRegion == RegionHistory {
			offset++
			// Vim-style 'y' (yank), 'c' (copy), or Enter: copy selection!
			if (b == 'y' || b == 'Y' || b == 'c' || b == 'C' || b == 13 || b == 10) && ui.Selecting {
				selected := ui.getSelectedTextLocked()
				if selected != "" {
					copyToClipboard(selected)
				}
			}
			continue
		}

		// In Prompt Region:
		// Standalone Ctrl+J / LF: Ctrl+Enter
		if b == 10 && len(inBuf) == 1 {
			offset++
			if ui.submitPrompt() {
				return true
			}
			continue
		}

		// Newline in prompt: Enter (13), CRLF (13, 10), or pasted LF (10)
		if b == 10 || b == 13 {
			offset++
			if b == 13 && offset < len(inBuf) && inBuf[offset] == 10 {
				offset++
			}
			ui.insertNewline()
			ui.adjustPromptScroll()
			continue
		}

		// Backspace (127 or 8)
		if b == 127 || b == 8 {
			offset++
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
			continue
		}

		// Collect contiguous text bytes up to next control/escape char
		end := offset
		for end < len(inBuf) && inBuf[end] != 0x1b && inBuf[end] != 3 && inBuf[end] != 24 && inBuf[end] != 10 && inBuf[end] != 13 && inBuf[end] != 127 && inBuf[end] != 8 {
			end++
		}
		textBytes := inBuf[offset:end]
		offset = end
		if len(textBytes) > 0 {
			ui.insertText(string(textBytes))
			ui.adjustPromptScroll()
		}
	}

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

	if ui.Session != nil && len(ui.Session.Messages) > 0 {
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
	var lastRender time.Time
	streamOpts.OnToken = func(tok string) {
		ui.mu.Lock()
		ui.StreamContent.WriteString(tok)
		ui.rebuildReversedLines()
		renderFn := ui.OnNeedRender

		now := time.Now()
		shouldRender := now.Sub(lastRender) >= 20*time.Millisecond
		if shouldRender {
			lastRender = now
		}
		ui.mu.Unlock()

		if shouldRender && renderFn != nil {
			renderFn()
		}
	}

	response, queryErr := queryOpenRouter(queryCtx, ui.Config, apiMessages, streamOpts)

	ui.mu.Lock()
	ui.IsStreaming = false
	ui.ActiveCancel = nil

	nowTs := time.Now().Format("15:04:05")
	if response != "" {
		ui.Turns = append(ui.Turns, ChatTurn{Role: "assistant", Content: response, Timestamp: nowTs})
		ui.Session.Messages = append(ui.Session.Messages,
			Message{Role: "user", Content: prompt},
			Message{Role: "assistant", Content: response},
		)
		_ = saveSession(ui.Session)
	} else if queryErr != nil && queryErr != context.Canceled {
		ui.Turns = append(ui.Turns, ChatTurn{Role: "assistant", Content: fmt.Sprintf("[Error: %v]", queryErr), Timestamp: nowTs})
	}
	ui.StreamContent.Reset()
	ui.rebuildReversedLines()
	renderFn := ui.OnNeedRender
	ui.mu.Unlock()

	if renderFn != nil {
		renderFn()
	}
}
