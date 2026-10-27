package main

import "strings"

// wrapText splits text into lines that fit within maxWidth columns
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
