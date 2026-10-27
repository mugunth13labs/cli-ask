package main

import (
	"strings"
	"testing"
)

func TestVTGridBasics(t *testing.T) {
	g := NewVTGrid(5, 10)

	g.Write([]byte("hello"))
	if g.CursorCol != 5 || g.CursorRow != 0 {
		t.Fatalf("expected cursor at (0, 5), got (%d, %d)", g.CursorRow, g.CursorCol)
	}

	row0 := g.RenderRow(0)
	if !strings.HasPrefix(row0, "hello") {
		t.Fatalf("expected row 0 to start with hello, got %q", row0)
	}

	// Test \r\n
	g.Write([]byte("\r\nworld"))
	if g.CursorRow != 1 || g.CursorCol != 5 {
		t.Fatalf("expected cursor at (1, 5), got (%d, %d)", g.CursorRow, g.CursorCol)
	}
	row1 := g.RenderRow(1)
	if !strings.HasPrefix(row1, "world") {
		t.Fatalf("expected row 1 to start with world, got %q", row1)
	}
}

func TestVTGridScrolling(t *testing.T) {
	g := NewVTGrid(3, 10)

	g.Write([]byte("line1\r\nline2\r\nline3\r\nline4"))
	if g.CursorRow != 2 {
		t.Fatalf("expected cursor at bottom row (2), got %d", g.CursorRow)
	}

	row0 := g.RenderRow(0)
	row1 := g.RenderRow(1)
	row2 := g.RenderRow(2)

	if !strings.HasPrefix(row0, "line2") {
		t.Fatalf("expected row 0 to be line2, got %q", row0)
	}
	if !strings.HasPrefix(row1, "line3") {
		t.Fatalf("expected row 1 to be line3, got %q", row1)
	}
	if !strings.HasPrefix(row2, "line4") {
		t.Fatalf("expected row 2 to be line4, got %q", row2)
	}
}

func TestVTGridCSIEscape(t *testing.T) {
	g := NewVTGrid(5, 10)

	// Move cursor to row 3, col 4 (1-based: \x1b[3;4H)
	g.Write([]byte("\x1b[3;4Htest"))
	if g.CursorRow != 2 || g.CursorCol != 7 {
		t.Fatalf("expected cursor at (2, 7), got (%d, %d)", g.CursorRow, g.CursorCol)
	}

	row2 := g.RenderRow(2)
	if !strings.HasPrefix(row2, "   test") {
		t.Fatalf("expected row 2 to have 3 leading spaces and test, got %q", row2)
	}

	// Test clear entire screen (\x1b[2J)
	g.Write([]byte("\x1b[2J"))
	if g.CursorRow != 0 || g.CursorCol != 0 {
		t.Fatalf("expected cursor at (0, 0) after clear screen, got (%d, %d)", g.CursorRow, g.CursorCol)
	}
	if g.RenderRow(2) != strings.Repeat(" ", 10) {
		t.Fatalf("expected empty row after clear screen")
	}
}
