package cli

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseCLIArgsStandalone(t *testing.T) {
	cases := []struct {
		input    []string
		expected string
	}{
		{[]string{"--help"}, "help"},
		{[]string{"-h"}, "help"},
		{[]string{"help"}, "help"},
		{[]string{"--version"}, "version"},
		{[]string{"-v"}, "version"},
		{[]string{"--config"}, "config"},
		{[]string{"--clear"}, "clear"},
		{[]string{"-c"}, "clear"},
		{[]string{"--update-self"}, "update-self"},
		{[]string{"--update"}, "update-self"},
		{[]string{"--chatbot"}, "chatbot"},
		{[]string{"-i"}, "chatbot"},
	}

	for _, c := range cases {
		p := parseCLIArgs(c.input)
		if p.Action != c.expected {
			t.Errorf("expected action %q for %v, got %q", c.expected, c.input, p.Action)
		}
	}
}

func TestParseCLIArgsChatbotWithPrompt(t *testing.T) {
	p := parseCLIArgs([]string{"-i", "explain", "goroutines"})
	if p.Action != "chatbot" {
		t.Errorf("expected action 'chatbot', got %q", p.Action)
	}
	if p.Prompt != "explain goroutines" {
		t.Errorf("expected prompt 'explain goroutines', got %q", p.Prompt)
	}

	p2 := parseCLIArgs([]string{"--chatbot", "--no-context", "hello"})
	if p2.Action != "chatbot" {
		t.Errorf("expected action 'chatbot', got %q", p2.Action)
	}
	if p2.Prompt != "hello" {
		t.Errorf("expected prompt 'hello', got %q", p2.Prompt)
	}
	if p2.UseContext != false {
		t.Errorf("expected UseContext to be false")
	}
}

func TestParseCLIArgsCodeGen(t *testing.T) {
	p1 := parseCLIArgs([]string{"--cg", "fibonacci", "in", "python"})
	if !p1.CodeGen {
		t.Errorf("expected CodeGen true for --cg")
	}
	if p1.Prompt != "fibonacci in python" {
		t.Errorf("expected prompt 'fibonacci in python', got %q", p1.Prompt)
	}

	p2 := parseCLIArgs([]string{"--codegen", "-o", "fib.py", "fibonacci"})
	if !p2.CodeGen {
		t.Errorf("expected CodeGen true for --codegen")
	}
	if p2.OutputFile != "fib.py" {
		t.Errorf("expected OutputFile 'fib.py', got %q", p2.OutputFile)
	}
	if p2.Prompt != "fibonacci" {
		t.Errorf("expected prompt 'fibonacci', got %q", p2.Prompt)
	}
}

func TestParseCLIArgsFileOutput(t *testing.T) {
	p1 := parseCLIArgs([]string{"-o", "out.txt", "explain", "kubernetes"})
	if p1.OutputFile != "out.txt" {
		t.Errorf("expected OutputFile 'out.txt', got %q", p1.OutputFile)
	}
	if p1.Prompt != "explain kubernetes" {
		t.Errorf("expected prompt 'explain kubernetes', got %q", p1.Prompt)
	}

	p2 := parseCLIArgs([]string{"--output", "file.log", "hello"})
	if p2.OutputFile != "file.log" {
		t.Errorf("expected OutputFile 'file.log', got %q", p2.OutputFile)
	}
	if p2.Prompt != "hello" {
		t.Errorf("expected prompt 'hello', got %q", p2.Prompt)
	}

	p3 := parseCLIArgs([]string{"-a", "notes.md", "more", "context"})
	if p3.AppendFile != "notes.md" {
		t.Errorf("expected AppendFile 'notes.md', got %q", p3.AppendFile)
	}
	if p3.Prompt != "more context" {
		t.Errorf("expected prompt 'more context', got %q", p3.Prompt)
	}
}

func TestParseCLIArgsDisambiguation(t *testing.T) {
	// First non-option argument "find" stops option parsing; -name and -type stay in prompt
	p1 := parseCLIArgs([]string{"find", ".", "-name", "*.go", "-type", "f"})
	if p1.Action != "query" {
		t.Errorf("expected action 'query', got %q", p1.Action)
	}
	if p1.Prompt != "find . -name *.go -type f" {
		t.Errorf("expected prompt 'find . -name *.go -type f', got %q", p1.Prompt)
	}

	// Normal command with flags (e.g. grep -i pattern)
	p2 := parseCLIArgs([]string{"grep", "-i", "pattern"})
	if p2.Action != "query" {
		t.Errorf("expected action 'query', got %q", p2.Action)
	}
	if p2.Prompt != "grep -i pattern" {
		t.Errorf("expected prompt 'grep -i pattern', got %q", p2.Prompt)
	}

	p3 := parseCLIArgs([]string{"ps", "-ef", "--sort", "-p"})
	if p3.Action != "query" {
		t.Errorf("expected action 'query', got %q", p3.Action)
	}
	if p3.Prompt != "ps -ef --sort -p" {
		t.Errorf("expected prompt 'ps -ef --sort -p', got %q", p3.Prompt)
	}

	// gcc -o main main.c must NOT be parsed as ask -o output file!
	p4 := parseCLIArgs([]string{"gcc", "-o", "main", "main.c"})
	if p4.OutputFile != "" {
		t.Errorf("expected OutputFile to be empty for gcc command, got %q", p4.OutputFile)
	}
	if p4.Prompt != "gcc -o main main.c" {
		t.Errorf("expected prompt 'gcc -o main main.c', got %q", p4.Prompt)
	}
}

func TestParseCLIArgsEscapeSeparator(t *testing.T) {
	p := parseCLIArgs([]string{"--", "--clear", "-c", "--config", "-i", "-o", "file.txt", "--cg"})
	if p.Action != "query" {
		t.Errorf("expected action 'query', got %q", p.Action)
	}
	if p.OutputFile != "" {
		t.Errorf("expected OutputFile empty when after --, got %q", p.OutputFile)
	}
	if p.CodeGen {
		t.Errorf("expected CodeGen false when after --")
	}
	if p.Prompt != "--clear -c --config -i -o file.txt --cg" {
		t.Errorf("expected prompt '--clear -c --config -i -o file.txt --cg', got %q", p.Prompt)
	}
}

func TestParseCLIArgsNoContext(t *testing.T) {
	p := parseCLIArgs([]string{"--no-context", "explain", "this"})
	if p.UseContext != false {
		t.Errorf("expected UseContext to be false")
	}
	if p.Prompt != "explain this" {
		t.Errorf("expected prompt 'explain this', got %q", p.Prompt)
	}
}

func TestDetectCodeGen(t *testing.T) {
	// 1. Explicit flag
	p1 := ParsedArgs{CodeGen: true}
	if !detectCodeGen(&p1, "hello", "") {
		t.Errorf("expected detectCodeGen true for explicit flag")
	}

	// 2. Output file extension (.pbs)
	p2 := ParsedArgs{OutputFile: "test.pbs"}
	if !detectCodeGen(&p2, "generate a pbs script template", "") {
		t.Errorf("expected detectCodeGen true for .pbs output")
	}

	// 3. Stdin with shebang and script keywords
	p3 := ParsedArgs{}
	if !detectCodeGen(&p3, "remove all the alien lines from this script", "#!/bin/bash\n#PBS -N job\n") {
		t.Errorf("expected detectCodeGen true for shebang + script prompt")
	}

	// 4. Normal prompt without code cues
	p4 := ParsedArgs{}
	if detectCodeGen(&p4, "what is the capital of France?", "") {
		t.Errorf("expected detectCodeGen false for non-code prompt")
	}
}

func TestStripMarkdownFences(t *testing.T) {
	input1 := "```bash\n#!/bin/bash\necho hello\n```"
	stripped1 := stripMarkdownFences(input1)
	expected1 := "#!/bin/bash\necho hello"
	if stripped1 != expected1 {
		t.Errorf("expected %q, got %q", expected1, stripped1)
	}

	input2 := "#!/bin/bash\necho hello"
	stripped2 := stripMarkdownFences(input2)
	if stripped2 != input2 {
		t.Errorf("expected %q, got %q", input2, stripped2)
	}
}

func TestReadChatPromptCooked(t *testing.T) {
	// 1. Multiline buffer with blank lines preserved
	input1 := "line 1\n\nline 2\n"
	reader1 := bufio.NewReader(strings.NewReader(input1))
	res1, err1 := readChatPromptCooked(reader1)
	if err1 != nil {
		t.Fatalf("unexpected error: %v", err1)
	}
	expected1 := "line 1\n\nline 2"
	if res1 != expected1 {
		t.Errorf("expected %q, got %q", expected1, res1)
	}

	// 2. Immediate slash command execution on first line
	input2 := "/exit\n"
	reader2 := bufio.NewReader(strings.NewReader(input2))
	res2, err2 := readChatPromptCooked(reader2)
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	if res2 != "/exit" {
		t.Errorf("expected '/exit', got %q", res2)
	}

	// 3. Plain words like "exit" without slash must NOT trigger command!
	if isChatCommand("exit") {
		t.Errorf("expected isChatCommand('exit') to be false (only slash commands allowed)")
	}
	if isChatCommand("quit") {
		t.Errorf("expected isChatCommand('quit') to be false (only slash commands allowed)")
	}
	if isChatCommand("clear") {
		t.Errorf("expected isChatCommand('clear') to be false (only slash commands allowed)")
	}
	if !isChatCommand("/exit") {
		t.Errorf("expected isChatCommand('/exit') to be true")
	}
	if !isChatCommand("/clear") {
		t.Errorf("expected isChatCommand('/clear') to be true")
	}

	// 4. Ctrl+Enter escape sequence submit
	input3 := "single line\x1b[13;5u\n"
	reader3 := bufio.NewReader(strings.NewReader(input3))
	res3, err3 := readChatPromptCooked(reader3)
	if err3 != nil {
		t.Fatalf("unexpected error: %v", err3)
	}
	if res3 != "single line" {
		t.Errorf("expected 'single line', got %q", res3)
	}
}

func TestMicroEditorOperations(t *testing.T) {
	ed := NewMicroEditor("Test", "Subtitle")

	// Insert text across multiple lines
	ed.InsertText("hello\nworld")
	if len(ed.Lines) != 2 || ed.Lines[0] != "hello" || ed.Lines[1] != "world" {
		t.Fatalf("unexpected editor content: %v", ed.Lines)
	}

	// Cross-line backspace test:
	// Move cursor to start of line 1 (col 0) and press backspace
	ed.CursorRow = 1
	ed.CursorCol = 0
	ed.Backspace()

	// Should merge into 1 line: "helloworld"
	if len(ed.Lines) != 1 || ed.Lines[0] != "helloworld" {
		t.Errorf("expected 'helloworld' after cross-line backspace, got %v", ed.Lines)
	}
	if ed.CursorCol != 5 {
		t.Errorf("expected cursorCol at merge boundary 5, got %d", ed.CursorCol)
	}

	// Arrow navigation:
	// Left from col 5 -> col 4
	ed.MoveLeft()
	if ed.CursorCol != 4 {
		t.Errorf("expected cursorCol 4 after MoveLeft, got %d", ed.CursorCol)
	}

	// Right from col 4 -> col 5
	ed.MoveRight()
	if ed.CursorCol != 5 {
		t.Errorf("expected cursorCol 5 after MoveRight, got %d", ed.CursorCol)
	}

	// Enter: insert newline between "hello" and "world"
	ed.InsertNewline()
	if len(ed.Lines) != 2 || ed.Lines[0] != "hello" || ed.Lines[1] != "world" {
		t.Errorf("expected split lines, got %v", ed.Lines)
	}

	// Arrow Up: moves to line 0
	ed.MoveUp()
	if ed.CursorRow != 0 {
		t.Errorf("expected CursorRow 0 after MoveUp, got %d", ed.CursorRow)
	}

	// Arrow Down: moves to line 1
	ed.MoveDown()
	if ed.CursorRow != 1 {
		t.Errorf("expected CursorRow 1 after MoveDown, got %d", ed.CursorRow)
	}

	// SS3 Arrow keys (\x1bOA, \x1bOB, \x1bOC, \x1bOD)
	processInput(ed, []byte("\x1bOA")) // Up
	if ed.CursorRow != 0 {
		t.Errorf("expected CursorRow 0 after SS3 Up arrow, got %d", ed.CursorRow)
	}
	processInput(ed, []byte("\x1bOB")) // Down
	if ed.CursorRow != 1 {
		t.Errorf("expected CursorRow 1 after SS3 Down arrow, got %d", ed.CursorRow)
	}

	// STRICT SUBMISSION RULES:
	// 1. Ctrl+Enter (byte 10) -> MUST submit
	submit, cancel := processInput(ed, []byte{10})
	if !submit || cancel {
		t.Errorf("expected submit on byte 10 (Ctrl+Enter)")
	}

	// 2. CSI u Ctrl+Enter (\x1b[13;5u) -> MUST submit
	submit, cancel = processInput(ed, []byte("\x1b[13;5u"))
	if !submit || cancel {
		t.Errorf("expected submit on CSI u Ctrl+Enter")
	}

	// 3. xterm Ctrl+Enter (\x1b[27;5;13~) -> MUST submit
	submit, cancel = processInput(ed, []byte("\x1b[27;5;13~"))
	if !submit || cancel {
		t.Errorf("expected submit on xterm Ctrl+Enter")
	}

	// 3a. Ctrl+S (byte 19) -> MUST submit
	submit, cancel = processInput(ed, []byte{19})
	if !submit || cancel {
		t.Errorf("expected submit on byte 19 (Ctrl+S)")
	}

	// 3b. Alt+Enter (\x1b\r and \x1b\n) -> MUST submit
	submit, cancel = processInput(ed, []byte("\x1b\r"))
	if !submit || cancel {
		t.Errorf("expected submit on Alt+Enter CR")
	}
	submit, cancel = processInput(ed, []byte("\x1b\n"))
	if !submit || cancel {
		t.Errorf("expected submit on Alt+Enter LF")
	}

	// 3c. Kitty / xterm Ctrl+S (\x1b[115;5u, \x1b[27;5;115~) -> MUST submit
	submit, cancel = processInput(ed, []byte("\x1b[115;5u"))
	if !submit || cancel {
		t.Errorf("expected submit on Kitty Ctrl+S")
	}
	submit, cancel = processInput(ed, []byte("\x1b[27;5;115~"))
	if !submit || cancel {
		t.Errorf("expected submit on xterm Ctrl+S")
	}

	// 4. STRICT: Ctrl+D (byte 4) -> MUST NEVER submit!
	submit, cancel = processInput(ed, []byte{4})
	if submit || cancel {
		t.Errorf("STRICT RULE BROKEN: Ctrl+D must NOT submit or cancel, got submit=%v cancel=%v", submit, cancel)
	}

	// 5. STRICT: Ctrl+Z (byte 26) -> MUST NEVER submit!
	submit, cancel = processInput(ed, []byte{26})
	if submit || cancel {
		t.Errorf("STRICT RULE BROKEN: Ctrl+Z must NOT submit or cancel, got submit=%v cancel=%v", submit, cancel)
	}

	// 6. Ctrl+C (byte 3) -> MUST cancel
	submit, cancel = processInput(ed, []byte{3})
	if submit || !cancel {
		t.Errorf("expected cancel on byte 3 (Ctrl+C)")
	}

	// 7. Regular Enter (byte 13) -> MUST insert newline, NOT submit
	linesBefore := len(ed.Lines)
	submit, cancel = processInput(ed, []byte{13})
	if submit || cancel {
		t.Errorf("Enter must not submit or cancel")
	}
	if len(ed.Lines) != linesBefore+1 {
		t.Errorf("expected Enter to insert newline, lines before: %d, after: %d", linesBefore, len(ed.Lines))
	}
}

func TestCompactHistoryUnderLimit(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you"},
		{Role: "assistant", Content: "good"},
	}

	compacted := compactHistory(messages)
	if len(compacted) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(compacted))
	}
}

func TestCompactHistoryExceedingLimit(t *testing.T) {
	var messages []Message
	for i := 0; i < 7; i++ {
		messages = append(messages,
			Message{Role: "user", Content: "query"},
			Message{Role: "assistant", Content: "response"},
		)
	}

	compacted := compactHistory(messages)
	expectedLen := maxRecentMessages + 1
	if len(compacted) != expectedLen {
		t.Fatalf("expected %d compacted messages, got %d", expectedLen, len(compacted))
	}

	if compacted[0].Role != "system" {
		t.Errorf("expected first message to be system summary, got role %s", compacted[0].Role)
	}

	if !strings.Contains(compacted[0].Content, "Prior conversation context summary") {
		t.Errorf("expected summary text in compacted message, got: %s", compacted[0].Content)
	}
}

func TestSessionIDGeneration(t *testing.T) {
	id := getSessionID()
	if !strings.HasPrefix(id, "session-") {
		t.Errorf("expected session ID to start with 'session-', got: %s", id)
	}
}

func TestFormatSplitter(t *testing.T) {
	line := formatSplitter("nvidia/nemotron-3.5-lightning:free")
	if !strings.HasPrefix(line, "─── ask [nvidia/nemotron-3.5-lightning:free]") {
		t.Errorf("unexpected splitter format: %s", line)
	}
	if !strings.HasSuffix(line, "─") {
		t.Errorf("expected splitter to end with horizontal bar: %s", line)
	}
}

func TestCrossLineBackspaceExtensive(t *testing.T) {
	ed := NewMicroEditor("Test", "Subtitle")
	ed.InsertText("alpha\nbeta\ngamma")
	if len(ed.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(ed.Lines))
	}

	// Move cursor to start of "beta" (line 1, col 0)
	ed.CursorRow = 1
	ed.CursorCol = 0
	ed.Backspace()

	// Line 1 should merge into line 0: "alphabeta"
	if len(ed.Lines) != 2 {
		t.Fatalf("expected 2 lines after merge, got %d", len(ed.Lines))
	}
	if ed.Lines[0] != "alphabeta" {
		t.Errorf("expected line 0 'alphabeta', got %q", ed.Lines[0])
	}
	if ed.Lines[1] != "gamma" {
		t.Errorf("expected line 1 'gamma', got %q", ed.Lines[1])
	}
	if ed.CursorRow != 0 || ed.CursorCol != 5 {
		t.Errorf("expected cursor at (0, 5), got (%d, %d)", ed.CursorRow, ed.CursorCol)
	}

	// Now move to line 1 ("gamma"), col 0, and backspace again
	ed.CursorRow = 1
	ed.CursorCol = 0
	ed.Backspace()

	// Should merge into 1 line: "alphabetagamma"
	if len(ed.Lines) != 1 || ed.Lines[0] != "alphabetagamma" {
		t.Errorf("expected 1 line 'alphabetagamma', got %v", ed.Lines)
	}
	if ed.CursorRow != 0 || ed.CursorCol != 9 {
		t.Errorf("expected cursor at (0, 9), got (%d, %d)", ed.CursorRow, ed.CursorCol)
	}
}

func TestStrictSlashCommands(t *testing.T) {
	// Plain words without slash MUST NOT be chat commands
	plainWords := []string{"exit", "quit", "clear", "model", "help", "q", "c", "exit 1", "quit()"}
	for _, word := range plainWords {
		if isChatCommand(word) {
			t.Errorf("word %q without slash must NOT be a chat command", word)
		}
	}

	// Slash commands MUST be recognized
	validCommands := []string{"/exit", "/quit", "/q", "/clear", "/c", "/model", "/model meta/llama-3", "/help", "/?"}
	for _, cmd := range validCommands {
		if !isChatCommand(cmd) {
			t.Errorf("command %q with slash MUST be recognized as a chat command", cmd)
		}
	}
}

func TestIncompatibleChatbotFileOutput(t *testing.T) {
	// ask -i -o main.c -> error
	p1 := parseCLIArgs([]string{"-i", "-o", "main.c"})
	if err := validateArgs(&p1); err == nil {
		t.Errorf("expected validation error for -i -o")
	}

	// ask -i -a main.c -> error
	p2 := parseCLIArgs([]string{"-i", "-a", "main.c"})
	if err := validateArgs(&p2); err == nil {
		t.Errorf("expected validation error for -i -a")
	}

	// ask --chatbot --output out.txt -> error
	p3 := parseCLIArgs([]string{"--chatbot", "--output", "out.txt"})
	if err := validateArgs(&p3); err == nil {
		t.Errorf("expected validation error for --chatbot --output")
	}

	// ask -o main.c "write hello world" -> VALID (standalone query, not chatbot)
	p4 := parseCLIArgs([]string{"-o", "main.c", "write hello world"})
	if err := validateArgs(&p4); err != nil {
		t.Errorf("expected valid query with -o, got error: %v", err)
	}
}

func TestWrapText(t *testing.T) {
	input := "The quick brown fox jumps over the lazy dog"
	wrapped := wrapText(input, 15)
	if len(wrapped) < 3 {
		t.Fatalf("expected at least 3 wrapped lines, got %d: %v", len(wrapped), wrapped)
	}
	for _, line := range wrapped {
		if len(line) > 15 {
			t.Errorf("line exceeded max width 15: %q", line)
		}
	}
}

func TestFormatInlineComposerBox(t *testing.T) {
	ed := NewMicroEditor("", "")
	ed.Lines = []string{"how do I check open ports in linux?", "preferably with ss"}
	top, lines, bottom, _, _, _ := formatInlineComposerBox(ed, 80, 5, 0, "")

	if !strings.Contains(top, "─ ask prompt [ Ctrl+Enter: Send | Ctrl+C: Cancel ]") {
		t.Errorf("expected header in top border, got: %q", top)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 content lines, got %d", len(lines))
	}
	for _, l := range lines {
		if !strings.Contains(l, "\x1b[48;5;236m  ") {
			t.Errorf("expected subtle tint and 2 spaces margin in content line, got: %q", l)
		}
	}
	if !strings.Contains(bottom, "───") {
		t.Errorf("expected bottom border dashes, got: %q", bottom)
	}

	// With target description
	topTarget, _, _, _, _, _ := formatInlineComposerBox(ed, 80, 5, 0, "main.go")
	if !strings.Contains(topTarget, "Target: main.go") {
		t.Errorf("expected Target: main.go in top border, got: %q", topTarget)
	}
}

func TestFormatInlineComposerBoxViewportRestrictionAndScrolling(t *testing.T) {
	ed := NewMicroEditor("", "")
	ed.Lines = []string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
		"line 10",
	}

	// 1. Initial view: cursor at line 0, 5 visible lines maximum
	ed.CursorRow = 0
	top, lines, bottom, rowInWin, col, scrollOffset := formatInlineComposerBox(ed, 80, 5, 0, "")
	if len(lines) != 5 {
		t.Fatalf("expected 5 visible lines, got %d", len(lines))
	}
	if scrollOffset != 0 {
		t.Errorf("expected scrollOffset 0, got %d", scrollOffset)
	}
	if rowInWin != 0 || col != 0 {
		t.Errorf("expected cursor at (0, 0), got (%d, %d)", rowInWin, col)
	}
	if !strings.Contains(bottom, "▼ 5 more below") {
		t.Errorf("expected bottom to indicate 5 more below, got: %q", bottom)
	}

	// 2. Cursor moved down to line 9 (last line)
	ed.CursorRow = 9
	top, lines, bottom, rowInWin, col, scrollOffset = formatInlineComposerBox(ed, 80, 5, scrollOffset, "")
	if len(lines) != 5 {
		t.Fatalf("expected 5 visible lines, got %d", len(lines))
	}
	if scrollOffset != 5 {
		t.Errorf("expected scrollOffset 5, got %d", scrollOffset)
	}
	if rowInWin != 4 {
		t.Errorf("expected cursor row in window 4, got %d", rowInWin)
	}
	if !strings.Contains(top, "▲ 5 above") {
		t.Errorf("expected top to indicate 5 above, got: %q", top)
	}
	if strings.Contains(bottom, "more below") {
		t.Errorf("expected bottom to have no more below indicator, got: %q", bottom)
	}

	// 3. Cursor moved back up to line 3
	ed.CursorRow = 3
	top, lines, bottom, rowInWin, col, scrollOffset = formatInlineComposerBox(ed, 80, 5, scrollOffset, "")
	if scrollOffset != 3 {
		t.Errorf("expected scrollOffset 3, got %d", scrollOffset)
	}
	if rowInWin != 0 {
		t.Errorf("expected cursor row in window 0, got %d", rowInWin)
	}
	if !strings.Contains(top, "▲ 3 above") {
		t.Errorf("expected top to indicate 3 above, got: %q", top)
	}
	if !strings.Contains(bottom, "▼ 2 more below") {
		t.Errorf("expected bottom to indicate 2 more below, got: %q", bottom)
	}
}

func TestProcessInputLinefeedAndEscape(t *testing.T) {
	ed := NewMicroEditor("", "")

	// 1. Standalone byte 10 (Ctrl+Enter) submits
	submit, cancel := processInput(ed, []byte{10})
	if !submit || cancel {
		t.Errorf("expected standalone byte 10 to submit, got submit=%v cancel=%v", submit, cancel)
	}

	// 2. \r\n from Enter or paste does NOT submit
	ed2 := NewMicroEditor("", "")
	submit, cancel = processInput(ed2, []byte{13, 10})
	if submit || cancel {
		t.Errorf("expected \\r\\n to NOT submit, got submit=%v cancel=%v", submit, cancel)
	}
	if len(ed2.Lines) != 2 {
		t.Errorf("expected 2 lines after \\r\\n, got %d", len(ed2.Lines))
	}

	// 3. Batched escape sequence does NOT bleed
	ed3 := NewMicroEditor("", "")
	submit, cancel = processInput(ed3, []byte("\x1b[A\x1b[B"))
	if submit || cancel {
		t.Errorf("unexpected submit/cancel for arrow keys")
	}
	if ed3.Lines[0] != "" {
		t.Errorf("expected empty line without bleed, got %q", ed3.Lines[0])
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"v0.4.0", "0.4.0"},
		{"v0.4", "0.4.0"},
		{"0.4", "0.4.0"},
		{"0.4.0", "0.4.0"},
		{"v1", "1.0.0"},
		{"v1.2.3", "1.2.3"},
	}
	for _, c := range cases {
		got := normalizeVersion(c.input)
		if got != c.expected {
			t.Errorf("expected %q for %q, got %q", c.expected, c.input, got)
		}
	}
}
