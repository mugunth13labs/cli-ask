package main

import "strings"

type WrappedLine struct {
	Text            string
	EndsWithNewline bool
}

// wrapTextLines splits text into lines fitting within maxWidth columns,
// recording whether each line ends with an original hard newline.
func wrapTextLines(text string, maxWidth int) []WrappedLine {
	if maxWidth <= 4 {
		maxWidth = 4
	}

	var result []WrappedLine
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	hasTrailingNewline := strings.HasSuffix(normalized, "\n")
	rawLines := strings.Split(normalized, "\n")
	if hasTrailingNewline && len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	for lIdx, line := range rawLines {
		isLastRawLine := (lIdx == len(rawLines)-1) && !hasTrailingNewline

		if line == "" {
			result = append(result, WrappedLine{Text: "", EndsWithNewline: true})
			continue
		}

		runes := []rune(line)
		for len(runes) > 0 {
			if len(runes) <= maxWidth {
				result = append(result, WrappedLine{
					Text:            string(runes),
					EndsWithNewline: !isLastRawLine,
				})
				break
			}

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
				result = append(result, WrappedLine{
					Text:            string(runes[:breakIdx]),
					EndsWithNewline: false, // soft-wrapped continuation!
				})
				runes = []rune(strings.TrimLeft(string(runes[breakIdx:]), " \t"))
			} else {
				result = append(result, WrappedLine{
					Text:            string(runes[:maxWidth]),
					EndsWithNewline: false, // soft-wrapped continuation!
				})
				runes = runes[maxWidth:]
			}
		}
	}
	return result
}

// wrapText splits text into lines that fit within maxWidth columns (strings only)
func wrapText(text string, maxWidth int) []string {
	lines := wrapTextLines(text, maxWidth)
	var result []string
	for _, l := range lines {
		result = append(result, l.Text)
	}
	return result
}
