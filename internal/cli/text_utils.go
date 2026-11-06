package cli

import (
	"strings"
	"unicode/utf8"
)

// VisLine represents a visually wrapped line segment originating from a logical line.
type VisLine struct {
	Text            string
	LogicalRow      int
	StartCol        int
	EndCol          int
	EndsWithNewline bool
}

// WrappedLine is preserved for backward compatibility with string-only text wrapping.
type WrappedLine struct {
	Text            string
	EndsWithNewline bool
}

// WrapLogicalLinesToVisual wraps a slice of logical lines into visual display lines
// fitting within maxWidth columns, recording logical coordinate bounds and soft-wrap flags.
func WrapLogicalLinesToVisual(lines []string, maxWidth int) []VisLine {
	if maxWidth < 4 {
		maxWidth = 4
	}

	var res []VisLine
	for lIdx, line := range lines {
		runes := []rune(line)
		lineLen := len(runes)

		if lineLen == 0 {
			res = append(res, VisLine{
				Text:            "",
				LogicalRow:      lIdx,
				StartCol:        0,
				EndCol:          0,
				EndsWithNewline: true,
			})
			continue
		}

		offset := 0
		for offset < lineLen {
			rem := lineLen - offset
			if rem <= maxWidth {
				res = append(res, VisLine{
					Text:            string(runes[offset:]),
					LogicalRow:      lIdx,
					StartCol:        offset,
					EndCol:          lineLen,
					EndsWithNewline: true,
				})
				break
			}

			// Find optimal word-wrap break point
			breakIdx := offset + maxWidth
			foundSpace := false
			for i := breakIdx; i > offset+maxWidth/2; i-- {
				if runes[i] == ' ' || runes[i] == '\t' {
					breakIdx = i
					foundSpace = true
					break
				}
			}

			if foundSpace {
				res = append(res, VisLine{
					Text:            string(runes[offset:breakIdx]),
					LogicalRow:      lIdx,
					StartCol:        offset,
					EndCol:          breakIdx,
					EndsWithNewline: false,
				})
				// Advance past whitespace delimiter
				offset = breakIdx
				for offset < lineLen && (runes[offset] == ' ' || runes[offset] == '\t') {
					offset++
				}
			} else {
				// Hard break on column limit
				res = append(res, VisLine{
					Text:            string(runes[offset:breakIdx]),
					LogicalRow:      lIdx,
					StartCol:        offset,
					EndCol:          breakIdx,
					EndsWithNewline: false,
				})
				offset = breakIdx
			}
		}
	}
	return res
}

// VisualPosFromLogical maps a 2D logical coordinate (cursorRow, cursorCol) to visual (visRow, visCol).
func VisualPosFromLogical(vis []VisLine, cursorRow, cursorCol int) (int, int) {
	if len(vis) == 0 {
		return 0, 0
	}

	for i, vl := range vis {
		if vl.LogicalRow == cursorRow {
			isLast := (i == len(vis)-1) || (vis[i+1].LogicalRow != cursorRow)
			if cursorCol >= vl.StartCol && (cursorCol < vl.EndCol || (isLast && cursorCol <= vl.EndCol)) {
				return i, cursorCol - vl.StartCol
			}
		}
	}

	// Fallback to last visual segment of that logical row, or bottom
	for i := len(vis) - 1; i >= 0; i-- {
		if vis[i].LogicalRow == cursorRow {
			return i, utf8.RuneCountInString(vis[i].Text)
		}
	}

	return len(vis) - 1, 0
}

// LogicalPosFromVisual maps a visual line and column to logical (row, col).
func LogicalPosFromVisual(vis []VisLine, targetVisRow, targetVisCol int) (int, int) {
	if len(vis) == 0 {
		return 0, 0
	}
	if targetVisRow < 0 {
		targetVisRow = 0
	}
	if targetVisRow >= len(vis) {
		targetVisRow = len(vis) - 1
	}

	vl := vis[targetVisRow]
	lineRunes := utf8.RuneCountInString(vl.Text)
	col := targetVisCol
	if col > lineRunes {
		col = lineRunes
	}
	return vl.LogicalRow, vl.StartCol + col
}

// wrapTextLines splits raw text into WrappedLine structs fitting within maxWidth columns.
func wrapTextLines(text string, maxWidth int) []WrappedLine {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	hasTrailingNewline := strings.HasSuffix(normalized, "\n")
	rawLines := strings.Split(normalized, "\n")
	if hasTrailingNewline && len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	vis := WrapLogicalLinesToVisual(rawLines, maxWidth)
	var result []WrappedLine
	for _, v := range vis {
		result = append(result, WrappedLine{
			Text:            v.Text,
			EndsWithNewline: v.EndsWithNewline,
		})
	}
	return result
}

// wrapText splits text into lines that fit within maxWidth columns (strings only).
func wrapText(text string, maxWidth int) []string {
	lines := wrapTextLines(text, maxWidth)
	var result []string
	for _, l := range lines {
		result = append(result, l.Text)
	}
	return result
}
