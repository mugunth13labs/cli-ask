package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type ChatRegion int

const (
	RegionPrompt  ChatRegion = 0
	RegionHistory ChatRegion = 1
)

type ConfirmKind int

const (
	ConfirmNone  ConfirmKind = 0
	ConfirmExit  ConfirmKind = 1
	ConfirmClear ConfirmKind = 2
)

type ChatTurn struct {
	Role      string
	Content   string
	Timestamp string // "15:04:05"
	IsBTW     bool   // true for ephemeral aside queries
}

type HistLine struct {
	Text            string
	IsBanner        bool
	BannerKind      string // "USER", "AI", "STREAM", "END", "LEAN_LINE", "USER_LINE"
	EndsWithNewline bool
}

type VisPromptLine = VisLine

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

	// Autocomplete state
	Auto *AutocompleteState

	// Selection state
	Selecting bool
	SelStartR int
	SelStartC int
	SelEndR   int
	SelEndC   int

	IsStreaming    bool
	IsStreamingBTW bool
	ActiveCancel   context.CancelFunc
	StreamContent  strings.Builder

	// Confirmation modal state
	ConfirmState  ConfirmKind
	ConfirmChoice int // 0 = Yes, 1 = No

	// Toast notification
	ToastMessage string
	ToastTime    time.Time
}

func (ui *ChatUI) showToastLocked(msg string) {
	ui.ToastMessage = msg
	ui.ToastTime = time.Now().Add(2 * time.Second)
}

func (ui *ChatUI) ShowToast(msg string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.showToastLocked(msg)
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
		Auto:         NewAutocomplete(),
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
	return WrapLogicalLinesToVisual(ui.PromptLines, maxWidth)
}

func (ui *ChatUI) cursorToVis(vis []VisPromptLine) (visRow, visCol int) {
	return VisualPosFromLogical(vis, ui.PromptRow, ui.PromptCol)
}

func (ui *ChatUI) visToCursor(vis []VisPromptLine, targetVisRow, targetVisCol int) {
	ui.PromptRow, ui.PromptCol = LogicalPosFromVisual(vis, targetVisRow, targetVisCol)
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
		bannerKind := "LEAN_LINE"
		if ui.IsStreamingBTW {
			label = "btw generating"
			bannerKind = "BTW_LEAN_LINE"
		}
		rem := contentWidth - len([]rune(label)) - 1
		if rem < 0 {
			rem = 0
		}
		streamHeader := strings.Repeat("─", rem) + " " + label
		ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: streamHeader, IsBanner: true, BannerKind: bannerKind, EndsWithNewline: true})

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
			// Lean top bar: right-aligned timestamp "hh:mm:ss"
			ts := turn.Timestamp
			if ts == "" {
				ts = time.Now().Format("15:04:05")
			}
			headerLabel := ts
			bannerKind := "LEAN_LINE"
			if turn.IsBTW {
				headerLabel = "btw " + ts
				bannerKind = "BTW_LEAN_LINE"
			}
			rem := contentWidth - len([]rune(headerLabel)) - 1
			if rem < 0 {
				rem = 0
			}
			startHeader := strings.Repeat("─", rem) + " " + headerLabel
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: startHeader, IsBanner: true, BannerKind: bannerKind, EndsWithNewline: true})

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
			bannerKind := "USER_LINE"
			if turn.IsBTW {
				headerText = "─ btw " + ts + " "
				bannerKind = "BTW_USER_LINE"
			}
			rem := contentWidth - len([]rune(headerText))
			if rem < 0 {
				rem = 0
			}
			startLine := headerText + strings.Repeat("─", rem)
			ui.ReversedLines = append(ui.ReversedLines, HistLine{Text: startLine, IsBanner: true, BannerKind: bannerKind, EndsWithNewline: true})

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
		return "\x1b[48;5;17m\x1b[97m" + text + "\x1b[0m"
	}

	// 2. Divider Row between Prompt Box and Reversed History
	if r == dividerRow {
		title := "─ history "
		if ui.ToastMessage != "" && time.Now().Before(ui.ToastTime) {
			title = fmt.Sprintf("─ history \x1b[38;5;46m%s\x1b[38;5;39m ", ui.ToastMessage)
		} else if ui.ActiveRegion == RegionHistory {
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
		return "\x1b[38;5;39m" + title + strings.Repeat("─", rem) + "\x1b[0m"
	}

	// 2b. Autocomplete overlay directly below divider row
	if ui.Auto != nil && ui.Auto.Active {
		dl := FormatDropdownLines(ui.Auto, ui.Width)
		dropIdx := r - (dividerRow + 1)
		if dropIdx >= 0 && dropIdx < len(dl) {
			return dl[dropIdx]
		}
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
				// Lean horizontal rule in Electric cyan (\x1b[38;5;39m)
				sb.WriteString("\x1b[38;5;39m")
				sb.WriteString(string(rawRunes))
				sb.WriteString(strings.Repeat("─", rem))
				sb.WriteString("\x1b[0m")
			case "BTW_LEAN_LINE":
				// BTW tangent rule in soft purple (\x1b[38;5;141m)
				sb.WriteString("\x1b[38;5;141m")
				sb.WriteString(string(rawRunes))
				sb.WriteString(strings.Repeat("─", rem))
				sb.WriteString("\x1b[0m")
			case "USER_LINE":
				// User line in warm amber (\x1b[38;5;214m)
				sb.WriteString("\x1b[38;5;214m")
				sb.WriteString(string(rawRunes))
				sb.WriteString(strings.Repeat("─", rem))
				sb.WriteString("\x1b[0m")
			case "BTW_USER_LINE":
				// BTW tangent prompt line in soft purple (\x1b[38;5;141m)
				sb.WriteString("\x1b[38;5;141m")
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
	return ParseEscapeSeq(b)
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

func (ui *ChatUI) handleWordRight() {
	ui.Selecting = false
	if ui.ActiveRegion != RegionPrompt {
		return
	}
	runes := []rune(ui.PromptLines[ui.PromptRow])
	lineLen := len(runes)
	if ui.PromptCol >= lineLen {
		if ui.PromptRow < len(ui.PromptLines)-1 {
			ui.PromptRow++
			ui.PromptCol = 0
		}
		ui.adjustPromptScroll()
		return
	}
	col := ui.PromptCol
	for col < lineLen && unicode.IsSpace(runes[col]) {
		col++
	}
	for col < lineLen && !unicode.IsSpace(runes[col]) {
		col++
	}
	ui.PromptCol = col
	ui.adjustPromptScroll()
}

func (ui *ChatUI) handleWordLeft() {
	ui.Selecting = false
	if ui.ActiveRegion != RegionPrompt {
		return
	}
	if ui.PromptCol == 0 {
		if ui.PromptRow > 0 {
			ui.PromptRow--
			ui.PromptCol = len([]rune(ui.PromptLines[ui.PromptRow]))
		}
		ui.adjustPromptScroll()
		return
	}
	runes := []rune(ui.PromptLines[ui.PromptRow])
	col := ui.PromptCol
	if col > len(runes) {
		col = len(runes)
	}
	for col > 0 && unicode.IsSpace(runes[col-1]) {
		col--
	}
	for col > 0 && !unicode.IsSpace(runes[col-1]) {
		col--
	}
	ui.PromptCol = col
	ui.adjustPromptScroll()
}

func (ui *ChatUI) submitPrompt() (shouldExit bool) {
	fullPrompt := strings.TrimSpace(strings.Join(ui.PromptLines, "\n"))
	if fullPrompt == "" {
		return false
	}
	if fullPrompt == "/exit" || fullPrompt == "/quit" || fullPrompt == "/q" {
		ui.ConfirmState = ConfirmExit
		ui.ConfirmChoice = 0
		if ui.Auto != nil {
			ui.Auto.Dismiss()
		}
		return false
	}
	if fullPrompt == "/help" || fullPrompt == "/?" {
		ui.PromptLines = []string{""}
		ui.PromptRow = 0
		ui.PromptCol = 0
		ui.PromptScroll = 0
		helpContent := "ask chat commands:\n" +
			"  /btw <question>    Ask a tangent with context without saving to history\n" +
			"  /clear, /c         Clear chat history & session context\n" +
			"  /help, /?          Show this help message\n" +
			"  /exit, /quit, /q   Exit ask chat\n\n" +
			"keyboard shortcuts:\n" +
			"  Alt+→ (Alt+Right)  Send prompt (Ctrl+S / Ctrl+Enter also supported)\n" +
			"  Ctrl+Right / Left  Jump word forward / backward\n" +
			"  Enter              Insert newline in prompt\n" +
			"  Up / Down          Navigate history & prompt\n" +
			"  Shift+Arrows       Select text in history\n" +
			"  Ctrl+X / y / c     Copy selected text"
		nowTs := time.Now().Format("15:04:05")
		ui.Turns = append(ui.Turns,
			ChatTurn{Role: "user", Content: fullPrompt, Timestamp: nowTs},
			ChatTurn{Role: "assistant", Content: helpContent, Timestamp: nowTs},
		)
		ui.rebuildReversedLines()
		return false
	}
	if fullPrompt == "/clear" || fullPrompt == "/c" {
		ui.ConfirmState = ConfirmClear
		ui.ConfirmChoice = 0
		if ui.Auto != nil {
			ui.Auto.Dismiss()
		}
		return false
	}

	if ui.Auto != nil {
		ui.Auto.Dismiss()
	}

	isBTW := false
	if fullPrompt == "/btw" || strings.HasPrefix(fullPrompt, "/btw ") || strings.HasPrefix(fullPrompt, "/btw\n") {
		isBTW = true
		actualQuery := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(fullPrompt, "/btw"), "\n"))
		if actualQuery == "" {
			ui.PromptLines = []string{""}
			ui.PromptRow = 0
			ui.PromptCol = 0
			ui.PromptScroll = 0
			usageContent := "Usage: /btw <question>\nAsk a quick tangent using past context without saving to conversation history."
			nowTs := time.Now().Format("15:04:05")
			ui.Turns = append(ui.Turns,
				ChatTurn{Role: "user", Content: "/btw", Timestamp: nowTs, IsBTW: true},
				ChatTurn{Role: "assistant", Content: usageContent, Timestamp: nowTs, IsBTW: true},
			)
			ui.rebuildReversedLines()
			return false
		}
		fullPrompt = actualQuery
	}

	// Check for @filename references and resolve attachments
	refs := ExtractFileReferences(fullPrompt)
	var attachments []FileAttachment
	queryPayload := fullPrompt
	if len(refs) > 0 {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		atts, fileContext, err := ResolveFileContext(cwd, refs)
		if err != nil {
			nowTs := time.Now().Format("15:04:05")
			ui.Turns = append(ui.Turns,
				ChatTurn{Role: "user", Content: fullPrompt, Timestamp: nowTs, IsBTW: isBTW},
				ChatTurn{Role: "assistant", Content: err.Error(), Timestamp: nowTs, IsBTW: isBTW},
			)
			ui.PromptLines = []string{""}
			ui.PromptRow = 0
			ui.PromptCol = 0
			ui.PromptScroll = 0
			ui.rebuildReversedLines()
			return false
		}
		attachments = atts
		queryPayload = fullPrompt + fileContext
	}

	historyPrompt := FormatHistoryPrompt(fullPrompt, attachments)
	nowTs := time.Now().Format("15:04:05")
	ui.Turns = append(ui.Turns, ChatTurn{Role: "user", Content: historyPrompt, Timestamp: nowTs, IsBTW: isBTW})
	ui.PromptLines = []string{""}
	ui.PromptRow = 0
	ui.PromptCol = 0
	ui.PromptScroll = 0
	ui.rebuildReversedLines()
	go ui.executeChatQuery(historyPrompt, queryPayload, isBTW)
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

		// Modal Confirmation handling (for /exit, /clear, Ctrl+C)
		if ui.ConfirmState != ConfirmNone {
			// Ctrl+C -> force exit immediately
			if chunk[0] == 3 {
				return true
			}
			// Escape key -> cancel confirmation
			if chunk[0] == 27 && (len(chunk) == 1 || (len(chunk) > 1 && chunk[1] != '[' && chunk[1] != 'O')) {
				ui.ConfirmState = ConfirmNone
				offset++
				continue
			}
			// Arrow Left or Right (CSI or SS3) -> toggle choice
			if bytes.HasPrefix(chunk, []byte("\x1b[D")) || bytes.HasPrefix(chunk, []byte("\x1bOD")) ||
				bytes.HasPrefix(chunk, []byte("\x1b[C")) || bytes.HasPrefix(chunk, []byte("\x1bOC")) {
				ui.ConfirmChoice = 1 - ui.ConfirmChoice
				seqLen := parseEscapeSeq(chunk)
				offset += seqLen
				continue
			}
			// Enter key -> confirm choice
			if chunk[0] == 13 || chunk[0] == 10 {
				if ui.ConfirmChoice == 0 { // Yes
					if ui.ConfirmState == ConfirmExit {
						return true
					}
					if ui.ConfirmState == ConfirmClear {
						ui.Turns = []ChatTurn{}
						ui.PromptLines = []string{""}
						ui.PromptRow = 0
						ui.PromptCol = 0
						ui.rebuildReversedLines()
						if ui.Session != nil {
							ui.Session.Messages = []Message{}
							_ = saveSession(ui.Session)
						}
						ui.ConfirmState = ConfirmNone
						ui.showToastLocked("Chat history cleared")
						offset++
						if chunk[0] == 13 && offset < len(inBuf) && inBuf[offset] == 10 {
							offset++
						}
						continue
					}
				} else { // No
					ui.ConfirmState = ConfirmNone
					offset++
					if chunk[0] == 13 && offset < len(inBuf) && inBuf[offset] == 10 {
						offset++
					}
					continue
				}
			}
			// Ignore any other keys while confirmation modal is active
			if chunk[0] == 0x1b {
				offset += parseEscapeSeq(chunk)
			} else {
				offset++
			}
			continue
		}

		// When not in confirm modal, handle Ctrl+C (0x03)
		if chunk[0] == 3 {
			offset++
			if ui.IsStreaming && ui.ActiveCancel != nil {
				ui.ActiveCancel()
				ui.ActiveCancel = nil
				continue
			}
			return true
		}

		// 1. Escape Sequence Handler
		if chunk[0] == 0x1b {
			seqLen := parseEscapeSeq(chunk)
			seq := chunk[:seqLen]
			offset += seqLen

			// 1a. Arrow Keys: Up, Down, Left, Right (both standard CSI and SS3/DECCKM)
			if bytes.Equal(seq, []byte("\x1b[A")) || bytes.Equal(seq, []byte("\x1bOA")) {
				if ui.Auto != nil && ui.Auto.Active {
					ui.Auto.Prev()
					continue
				}
				ui.handleUpArrow()
				continue
			}
			if bytes.Equal(seq, []byte("\x1b[B")) || bytes.Equal(seq, []byte("\x1bOB")) {
				if ui.Auto != nil && ui.Auto.Active {
					ui.Auto.Next()
					continue
				}
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

			// 1b2. Ctrl+Arrows for word jump in prompt
			if bytes.Equal(seq, []byte("\x1b[1;5C")) || bytes.Equal(seq, []byte("\x1b[5C")) {
				ui.handleWordRight()
				continue
			}
			if bytes.Equal(seq, []byte("\x1b[1;5D")) || bytes.Equal(seq, []byte("\x1b[5D")) {
				ui.handleWordLeft()
				continue
			}

			// 1c. Alt+Right / Ctrl+Enter / Alt+Enter / Ctrl+S submit sequences
			if IsSubmitKeySequence(seq) {
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
						ui.showToastLocked("[Copied to clipboard]")
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

			// 1f. Esc sequence handling
			if bytes.Equal(seq, []byte("\x1b")) || (len(seq) == 1 && seq[0] == 27) {
				if ui.Auto != nil && ui.Auto.Active {
					ui.Auto.Dismiss()
					continue
				}
				if ui.Selecting {
					ui.Selecting = false
					continue
				}
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

		// STRICT: Ctrl+D (4) and Ctrl+Z (26) -> Ignore, never submit or insert
		if b == 4 || b == 26 {
			offset++
			continue
		}

		// Ctrl+S (19): Submit prompt (universal alternative to Ctrl+Enter)
		if b == 19 {
			offset++
			if ui.ActiveRegion == RegionPrompt {
				if ui.submitPrompt() {
					return true
				}
			}
			continue
		}

		// Ctrl+X (24): Copy selection
		if b == 24 {
			offset++
			if ui.Selecting {
				selected := ui.getSelectedTextLocked()
				if selected != "" {
					copyToClipboard(selected)
					ui.showToastLocked("[Copied to clipboard]")
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
					ui.showToastLocked("[Copied to clipboard]")
				}
			}
			continue
		}

		// In Prompt Region:
		// Tab (9) for Autocomplete selection or indentation
		if b == 9 {
			offset++
			if ui.Auto != nil && ui.Auto.Active {
				cand := ui.Auto.SelectedCandidate()
				if cand != "" && ui.PromptRow < len(ui.PromptLines) {
					isSlash := (ui.Auto.Mode == AutocompleteModeSlash)
					ui.PromptLines, ui.PromptCol = ApplyAutocompleteCandidate(
						ui.PromptLines, ui.PromptRow, ui.PromptCol, ui.Auto.TriggerCol, cand, isSlash,
					)
					if !isSlash && strings.HasSuffix(cand, "/") {
						ui.Auto.UpdateQuery(cand)
					} else {
						ui.Auto.Dismiss()
					}
					ui.adjustPromptScroll()
					continue
				}
				ui.Auto.Dismiss()
				continue
			}
			ui.insertText("  ")
			ui.adjustPromptScroll()
			continue
		}

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
			if ui.Auto != nil && ui.Auto.Active {
				cand := ui.Auto.SelectedCandidate()
				if cand != "" && ui.PromptRow < len(ui.PromptLines) {
					isSlash := (ui.Auto.Mode == AutocompleteModeSlash)
					if isSlash && cand != "/btw" {
						// Slash command selected with Enter (except /btw): dispatch immediately!
						ui.PromptLines[ui.PromptRow] = cand
						ui.PromptCol = len([]rune(cand))
						ui.Auto.Dismiss()
						if ui.submitPrompt() {
							return true
						}
						continue
					}
					ui.PromptLines, ui.PromptCol = ApplyAutocompleteCandidate(
						ui.PromptLines, ui.PromptRow, ui.PromptCol, ui.Auto.TriggerCol, cand, isSlash,
					)
					if !isSlash && strings.HasSuffix(cand, "/") {
						ui.Auto.UpdateQuery(cand)
					} else {
						ui.Auto.Dismiss()
					}
					ui.adjustPromptScroll()
					continue
				}
				ui.Auto.Dismiss()
				continue
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
			if ui.Auto != nil && ui.Auto.Active {
				if ui.PromptRow != ui.Auto.TriggerRow || ui.PromptCol <= ui.Auto.TriggerCol {
					ui.Auto.Dismiss()
				} else {
					lineRunes := []rune(ui.PromptLines[ui.PromptRow])
					if ui.PromptCol > ui.Auto.TriggerCol {
						ui.Auto.UpdateQuery(string(lineRunes[ui.Auto.TriggerCol+1 : ui.PromptCol]))
					} else {
						ui.Auto.UpdateQuery("")
					}
				}
			}
			ui.adjustPromptScroll()
			continue
		}

		// Collect contiguous text bytes up to next control/escape char
		end := offset
		for end < len(inBuf) && inBuf[end] != 0x1b && inBuf[end] != 3 && inBuf[end] != 4 && inBuf[end] != 19 && inBuf[end] != 26 && inBuf[end] != 24 && inBuf[end] != 10 && inBuf[end] != 13 && inBuf[end] != 127 && inBuf[end] != 8 && inBuf[end] != 9 {
			end++
		}
		textBytes := inBuf[offset:end]
		offset = end
		if len(textBytes) > 0 {
			// Trigger slash command autocomplete when user directly types single '/' at line start
			if len(textBytes) == 1 && textBytes[0] == '/' && (ui.Auto == nil || !ui.Auto.Active) {
				line := ui.PromptLines[ui.PromptRow]
				runes := []rune(line)
				prefix := strings.TrimSpace(string(runes[:ui.PromptCol]))
				if prefix == "" {
					ui.insertText("/")
					if ui.Auto != nil {
						ui.Auto.TriggerSlash(ui.PromptRow, ui.PromptCol-1)
					}
					ui.adjustPromptScroll()
					continue
				}
			}

			// Trigger autocomplete when user directly types single '@'
			if len(textBytes) == 1 && textBytes[0] == '@' && (ui.Auto == nil || !ui.Auto.Active) {
				ui.insertText("@")
				if ui.Auto != nil {
					ui.Auto.Trigger(ui.PromptRow, ui.PromptCol-1)
				}
				ui.adjustPromptScroll()
				continue
			}

			ui.insertText(string(textBytes))
			if ui.Auto != nil && ui.Auto.Active {
				if len(textBytes) == 1 && IsPathDelimiter(rune(textBytes[0])) {
					ui.Auto.Dismiss()
				} else if ui.PromptRow == ui.Auto.TriggerRow && ui.PromptCol > ui.Auto.TriggerCol {
					lineRunes := []rune(ui.PromptLines[ui.PromptRow])
					ui.Auto.UpdateQuery(string(lineRunes[ui.Auto.TriggerCol+1 : ui.PromptCol]))
				}
			}
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

func (ui *ChatUI) executeChatQuery(historyPrompt, queryPayload string, isBTW bool) {
	ui.mu.Lock()
	ui.IsStreaming = true
	ui.IsStreamingBTW = isBTW
	ui.StreamContent.Reset()

	var history []Message
	if ui.Session != nil {
		history = ui.Session.Messages
	}
	apiMessages := buildAPIMessages(ui.Opts.IsCodeGen, history, queryPayload)

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
	ui.IsStreamingBTW = false
	ui.ActiveCancel = nil

	nowTs := time.Now().Format("15:04:05")
	if response != "" {
		ui.Turns = append(ui.Turns, ChatTurn{Role: "assistant", Content: response, Timestamp: nowTs, IsBTW: isBTW})
		if !isBTW && ui.Session != nil {
			ui.Session.Messages = append(ui.Session.Messages,
				Message{Role: "user", Content: historyPrompt},
				Message{Role: "assistant", Content: response},
			)
			_ = saveSession(ui.Session)
		}
	} else if queryErr != nil && queryErr != context.Canceled {
		ui.Turns = append(ui.Turns, ChatTurn{Role: "assistant", Content: fmt.Sprintf("[Error: %v]", queryErr), Timestamp: nowTs, IsBTW: isBTW})
	}
	ui.StreamContent.Reset()
	ui.rebuildReversedLines()
	renderFn := ui.OnNeedRender
	ui.mu.Unlock()

	if renderFn != nil {
		renderFn()
	}
}
