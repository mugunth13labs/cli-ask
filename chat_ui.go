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
	Text       string
	IsBanner   bool
	BannerKind string // "USER", "AI", "STREAM", "END", "LEAN_LINE", "USER_LINE"
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
		// Animated dots loop right-aligned: generating . -> .. -> ... -> .. -> .
		dots := []string{".", "..", "...", ".."}
		dot := dots[(time.Now().UnixMilli()/300)%4]
		streamLabel := "generating " + dot
		rem := contentWidth - len([]rune(streamLabel)) - 1
		if rem < 0 {
			rem = 0
		}
		streamHeader := strings.Repeat("─", rem) + " " + streamLabel
		ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: streamHeader, IsBanner: true, BannerKind: "LEAN_LINE"})

		wrapped := wrapText(ui.StreamContent.String(), contentWidth)
		for _, w := range wrapped {
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: w})
		}

		endHeader := strings.Repeat("─", contentWidth)
		ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: endHeader, IsBanner: true, BannerKind: "LEAN_LINE"})
		ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: ""}) // spacing
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
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: startHeader, IsBanner: true, BannerKind: "LEAN_LINE"})

			wrapped := wrapText(turn.Content, contentWidth)
			for _, w := range wrapped {
				ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: w})
			}

			// Lean bottom bar
			endHeader := strings.Repeat("─", contentWidth)
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: endHeader, IsBanner: true, BannerKind: "LEAN_LINE"})
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: ""}) // spacing
		} else {
			// User Turn: normal line above and below with YOU START / YOU END in the middle
			startLine := formatCenteredLine("YOU START", contentWidth)
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: startLine, IsBanner: true, BannerKind: "USER_LINE"})

			wrapped := wrapText(turn.Content, contentWidth)
			for _, w := range wrapped {
				ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: w})
			}

			endLine := formatCenteredLine("YOU END", contentWidth)
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: endLine, IsBanner: true, BannerKind: "USER_LINE"})
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: ""}) // spacing
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
			return string(runes[:ui.Width])
		}
		return text + strings.Repeat(" ", ui.Width-len(runes))
	}

	// 2. Divider Row between Prompt Box and Reversed History
	if r == dividerRow {
		title := "─── History (Down Arrow to Browse) "
		if ui.ActiveRegion == RegionHistory {
			title = "──● History [Browsing / Ctrl+X to Copy] "
			rem := ui.Width - len([]rune(title))
			if rem < 0 {
				rem = 0
			}
			return "\x1b[7;1m" + title + strings.Repeat("─", rem) + "\x1b[0m"
		}
		rem := ui.Width - len([]rune(title))
		if rem < 0 {
			rem = 0
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
		if r < eR || (eC >= len(runes) && len(runes) > 0) {
			sb.WriteString("\n")
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

func (ui *ChatUI) HandleKey(inBuf []byte) (shouldExit bool) {
	// Guard against SGR mouse escape sequences reaching key handler
	if bytes.HasPrefix(inBuf, []byte("\x1b[<")) {
		return false
	}

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

	// 2. Ctrl+C: Cancel generation if streaming, else quit
	if len(inBuf) == 1 && inBuf[0] == 3 {
		if ui.IsStreaming && ui.ActiveCancel != nil {
			ui.ActiveCancel()
			ui.ActiveCancel = nil
			return false
		}
		return true
	}

	// 2b. Ctrl+X: Copy active selection to clipboard
	if len(inBuf) == 1 && inBuf[0] == 24 {
		if ui.Selecting {
			selected := ui.getSelectedTextLocked()
			if selected != "" {
				copyToClipboard(selected)
			}
			return false
		}
	}

	// 3. Arrow Keys: Up, Down, Left, Right
	if bytes.Equal(inBuf, []byte("\x1b[A")) { // Up Arrow
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
		return false
	}

	if bytes.Equal(inBuf, []byte("\x1b[B")) { // Down Arrow
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
		return false
	}

	if bytes.Equal(inBuf, []byte("\x1b[D")) { // Left Arrow
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
		return false
	}

	if bytes.Equal(inBuf, []byte("\x1b[C")) { // Right Arrow
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

	// 5. In History: ignore typing characters (user must arrow Up into prompt to type)
	if ui.ActiveRegion == RegionHistory {
		return false
	}

	// 6. Enter key (13 or \r\n): Newline in prompt!
	if (len(inBuf) == 1 && inBuf[0] == 13) || bytes.Equal(inBuf, []byte("\r\n")) {
		ui.insertNewline()
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

	// 8. Text insertion into prompt line (handles pasted text and newlines)
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
