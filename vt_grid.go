package main

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type Cell struct {
	Rune  rune
	Style string // ANSI SGR sequence, e.g. "\x1b[32m"
}

type VTGrid struct {
	mu           sync.Mutex
	Rows         int
	Cols         int
	CursorRow    int
	CursorCol    int
	SavedRow     int
	SavedCol     int
	CurrentStyle string
	Cells        [][]Cell
}

func NewVTGrid(rows, cols int) *VTGrid {
	if rows < 1 {
		rows = 24
	}
	if cols < 1 {
		cols = 80
	}

	grid := &VTGrid{
		Rows: rows,
		Cols: cols,
	}
	grid.initCells(rows, cols)
	return grid
}

func (g *VTGrid) initCells(rows, cols int) {
	g.Cells = make([][]Cell, rows)
	for r := 0; r < rows; r++ {
		g.Cells[r] = make([]Cell, cols)
		for c := 0; c < cols; c++ {
			g.Cells[r][c] = Cell{Rune: ' '}
		}
	}
}

func (g *VTGrid) Resize(newRows, newCols int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if newRows < 1 || newCols < 1 {
		return
	}
	if newRows == g.Rows && newCols == g.Cols {
		return
	}

	newCells := make([][]Cell, newRows)
	for r := 0; r < newRows; r++ {
		newCells[r] = make([]Cell, newCols)
		for c := 0; c < newCols; c++ {
			if r < g.Rows && c < g.Cols {
				newCells[r][c] = g.Cells[r][c]
			} else {
				newCells[r][c] = Cell{Rune: ' '}
			}
		}
	}

	g.Cells = newCells
	g.Rows = newRows
	g.Cols = newCols

	if g.CursorRow >= newRows {
		g.CursorRow = newRows - 1
	}
	if g.CursorCol >= newCols {
		g.CursorCol = newCols - 1
	}
}

func (g *VTGrid) scrollUp() {
	if g.Rows <= 0 {
		return
	}
	// Shift rows up by 1
	copy(g.Cells[0:], g.Cells[1:])
	// Clear the bottom row
	bottom := make([]Cell, g.Cols)
	for c := 0; c < g.Cols; c++ {
		bottom[c] = Cell{Rune: ' '}
	}
	g.Cells[g.Rows-1] = bottom
}

func (g *VTGrid) clearRow(r int, startCol, endCol int) {
	if r < 0 || r >= g.Rows {
		return
	}
	if startCol < 0 {
		startCol = 0
	}
	if endCol > g.Cols {
		endCol = g.Cols
	}
	for c := startCol; c < endCol; c++ {
		g.Cells[r][c] = Cell{Rune: ' '}
	}
}

func (g *VTGrid) Write(data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()

	i := 0
	n := len(data)

	for i < n {
		b := data[i]

		// Check for ESC sequence
		if b == 0x1b {
			if i+1 < n {
				next := data[i+1]
				if next == '[' { // CSI sequence
					// Read until command character (letter or ~)
					j := i + 2
					for j < n && ((data[j] >= '0' && data[j] <= '9') || data[j] == ';' || data[j] == '?' || data[j] == ' ' || data[j] == '>') {
						j++
					}
					if j < n {
						cmd := data[j]
						params := string(data[i+2 : j])
						g.handleCSI(cmd, params)
						i = j + 1
						continue
					} else {
						// Incomplete sequence at end of buffer
						break
					}
				} else if next == ']' { // OSC sequence: \x1b] ... \x07 or \x1b] ... \x1b\
					j := i + 2
					for j < n && data[j] != 0x07 && !(data[j] == 0x1b && j+1 < n && data[j+1] == '\\') {
						j++
					}
					if j < n {
						if data[j] == 0x07 {
							i = j + 1
						} else {
							i = j + 2
						}
						continue
					} else {
						break
					}
				} else if next == '(' || next == ')' { // Character set switch (e.g. \x1b(B)
					i += 3
					continue
				} else if next == '7' { // Save cursor
					g.SavedRow = g.CursorRow
					g.SavedCol = g.CursorCol
					i += 2
					continue
				} else if next == '8' { // Restore cursor
					g.CursorRow = g.SavedRow
					g.CursorCol = g.SavedCol
					i += 2
					continue
				} else if next == 'M' { // Reverse index (cursor up with scroll)
					if g.CursorRow > 0 {
						g.CursorRow--
					}
					i += 2
					continue
				}
			}
			// Unrecognized bare escape
			i++
			continue
		}

		// Control characters
		switch b {
		case '\r':
			g.CursorCol = 0
			i++
			continue
		case '\n':
			g.CursorRow++
			if g.CursorRow >= g.Rows {
				g.scrollUp()
				g.CursorRow = g.Rows - 1
			}
			i++
			continue
		case '\b':
			if g.CursorCol > 0 {
				g.CursorCol--
			}
			i++
			continue
		case '\t':
			nextTab := (g.CursorCol + 8) &^ 7
			if nextTab >= g.Cols {
				nextTab = g.Cols - 1
			}
			g.CursorCol = nextTab
			i++
			continue
		case 0x07: // Bell - ignore
			i++
			continue
		}

		// Printable character / UTF-8 rune
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}

		if g.CursorRow >= 0 && g.CursorRow < g.Rows && g.CursorCol >= 0 && g.CursorCol < g.Cols {
			g.Cells[g.CursorRow][g.CursorCol] = Cell{
				Rune:  r,
				Style: g.CurrentStyle,
			}
		}

		g.CursorCol++
		if g.CursorCol >= g.Cols {
			// Autowrap to next line
			g.CursorCol = 0
			g.CursorRow++
			if g.CursorRow >= g.Rows {
				g.scrollUp()
				g.CursorRow = g.Rows - 1
			}
		}

		i += size
	}
}

func (g *VTGrid) handleCSI(cmd byte, params string) {
	// Parse semicolon-separated integer arguments
	var args []int
	if params != "" && !strings.HasPrefix(params, "?") {
		parts := strings.Split(params, ";")
		for _, p := range parts {
			val, err := strconv.Atoi(p)
			if err != nil {
				val = 0
			}
			args = append(args, val)
		}
	}

	getArg := func(idx int, def int) int {
		if idx < len(args) && args[idx] > 0 {
			return args[idx]
		}
		return def
	}

	switch cmd {
	case 'H', 'f': // Cursor position
		row := getArg(0, 1) - 1
		col := getArg(1, 1) - 1
		if row < 0 {
			row = 0
		}
		if row >= g.Rows {
			row = g.Rows - 1
		}
		if col < 0 {
			col = 0
		}
		if col >= g.Cols {
			col = g.Cols - 1
		}
		g.CursorRow = row
		g.CursorCol = col

	case 'A': // Cursor Up
		n := getArg(0, 1)
		g.CursorRow -= n
		if g.CursorRow < 0 {
			g.CursorRow = 0
		}

	case 'B': // Cursor Down
		n := getArg(0, 1)
		g.CursorRow += n
		if g.CursorRow >= g.Rows {
			g.CursorRow = g.Rows - 1
		}

	case 'C': // Cursor Forward
		n := getArg(0, 1)
		g.CursorCol += n
		if g.CursorCol >= g.Cols {
			g.CursorCol = g.Cols - 1
		}

	case 'D': // Cursor Backward
		n := getArg(0, 1)
		g.CursorCol -= n
		if g.CursorCol < 0 {
			g.CursorCol = 0
		}

	case 'J': // Erase in Display
		mode := getArg(0, 0)
		switch mode {
		case 0: // Cursor to end of screen
			g.clearRow(g.CursorRow, g.CursorCol, g.Cols)
			for r := g.CursorRow + 1; r < g.Rows; r++ {
				g.clearRow(r, 0, g.Cols)
			}
		case 1: // Start of screen to cursor
			for r := 0; r < g.CursorRow; r++ {
				g.clearRow(r, 0, g.Cols)
			}
			g.clearRow(g.CursorRow, 0, g.CursorCol+1)
		case 2, 3: // Clear entire screen
			for r := 0; r < g.Rows; r++ {
				g.clearRow(r, 0, g.Cols)
			}
			g.CursorRow = 0
			g.CursorCol = 0
		}

	case 'K': // Erase in Line
		mode := getArg(0, 0)
		switch mode {
		case 0: // Cursor to end of line
			g.clearRow(g.CursorRow, g.CursorCol, g.Cols)
		case 1: // Start of line to cursor
			g.clearRow(g.CursorRow, 0, g.CursorCol+1)
		case 2: // Clear entire line
			g.clearRow(g.CursorRow, 0, g.Cols)
		}

	case 'm': // SGR (Select Graphic Rendition) styling
		if len(args) == 0 || (len(args) == 1 && args[0] == 0) {
			g.CurrentStyle = ""
		} else {
			g.CurrentStyle = "\x1b[" + params + "m"
		}

	case 's': // Save cursor
		g.SavedRow = g.CursorRow
		g.SavedCol = g.CursorCol

	case 'u': // Restore cursor
		g.CursorRow = g.SavedRow
		g.CursorCol = g.SavedCol
	}
}

// RenderRow returns the formatted string for row `r`, clipped and padded to `Cols` width.
// Opens ANSI styles and guarantees `\x1b[0m` reset at the end so styles never bleed!
func (g *VTGrid) RenderRow(r int) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	if r < 0 || r >= g.Rows {
		return strings.Repeat(" ", g.Cols)
	}

	var b strings.Builder
	lastStyle := ""

	row := g.Cells[r]
	for c := 0; c < g.Cols; c++ {
		cell := row[c]
		if cell.Style != lastStyle {
			if cell.Style == "" {
				b.WriteString("\x1b[0m")
			} else {
				b.WriteString(cell.Style)
			}
			lastStyle = cell.Style
		}

		if cell.Rune == 0 || cell.Rune == ' ' {
			b.WriteRune(' ')
		} else {
			b.WriteRune(cell.Rune)
		}
	}

	// Always reset style at the end of the line
	if lastStyle != "" {
		b.WriteString("\x1b[0m")
	}

	return b.String()
}
