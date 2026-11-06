package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
)

// isChatCommand checks if a line is an explicit slash command.
// STRICT: Words without a leading slash (like "exit", "quit", "clear")
// are treated as literal text/code and never triggered as commands!
func isChatCommand(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "/exit" || lower == "/quit" || lower == "/q" ||
		lower == "/clear" || lower == "/c" ||
		lower == "/help" || lower == "/?" ||
		lower == "/btw" || strings.HasPrefix(lower, "/btw ") {
		return true
	}
	return false
}

func readChatPromptCooked(reader *bufio.Reader) (string, error) {
	var lines []string

	for {
		rawLine, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if len(lines) > 0 {
					break
				}
				return "", io.EOF
			}
			return "", err
		}

		// Strip trailing CR and LF
		line := strings.TrimRight(rawLine, "\r\n")

		// If first line and it's an explicit slash command (/exit, /clear, /model, /help), submit immediately
		if len(lines) == 0 {
			trimmed := strings.TrimSpace(line)
			if isChatCommand(trimmed) {
				return trimmed, nil
			}
		}

		// Detect terminal escape sequences for Alt+Right, Ctrl+Enter, Alt+Enter, Ctrl+S
		cleanedLine, hasSubmitSeq := StripSubmitKeySequences(line)
		if strings.Contains(cleanedLine, "\x13") {
			hasSubmitSeq = true
			cleanedLine = strings.ReplaceAll(cleanedLine, "\x13", "")
		}
		if hasSubmitSeq {
			if strings.TrimSpace(cleanedLine) != "" {
				lines = append(lines, cleanedLine)
			}
			break
		}

		// Ignore blank Enter on initial prompt if nothing entered yet
		if len(lines) == 0 && strings.TrimSpace(line) == "" {
			continue
		}

		// Blank lines inside prompt are preserved as newlines
		lines = append(lines, line)
	}

	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func readChatPrompt(reader *bufio.Reader, isInteractive bool) (string, error) {
	if isInteractive {
		content, isCancelled, err := runInlineTurnEditor()
		if err != nil {
			return "", err
		}
		if isCancelled {
			return "/exit", nil
		}
		return content, nil
	}

	return readChatPromptCooked(reader)
}

func renderStandaloneHeader(width int, confirmExit bool, exitChoice int, chat *ChatUI) string {
	navyBg := "\x1b[48;5;17m"
	cyan := "\x1b[38;5;39m"
	white := "\x1b[97m"
	amber := "\x1b[38;5;214m"
	reset := "\x1b[0m"

	isConfirm := confirmExit
	choice := exitChoice
	prompt := " Exit ask chat?  "
	if chat != nil && chat.ConfirmState != ConfirmNone {
		isConfirm = true
		choice = chat.ConfirmChoice
		if chat.ConfirmState == ConfirmClear {
			prompt = " Clear chat history?  "
		}
	}

	if isConfirm {
		yes := " [ Yes ] "
		no := " [ No ] "
		if choice == 0 {
			yes = amber + "\x1b[7;1m Yes " + reset + navyBg + white
			no = "  No  "
		} else {
			yes = "  Yes  "
			no = amber + "\x1b[7;1m No " + reset + navyBg + white
		}
		hints := " (←/→: Choose | Enter: Confirm | Esc: Cancel) "
		text := prompt + yes + "  " + no + hints
		rem := width - len([]rune(text))
		if rem < 0 {
			rem = 0
		}
		return navyBg + white + text + strings.Repeat(" ", rem) + reset
	}

	title := " ask chat"
	var hints string

	if chat != nil && chat.Selecting {
		hints = "[ Text Selected ]  y / Enter: Copy "
	} else if chat != nil && chat.ActiveRegion == RegionHistory {
		hints = "↑/↓: Browse | Shift+Arrows: Select | ↑: Prompt "
	} else {
		// In Prompt composer
		hints = "Alt+→: Send | /help /btw /clear /exit | Ctrl+C: Exit "
		if width < 85 {
			hints = "Alt+→: Send | / commands | Ctrl+C: Exit "
		}
		if width < 60 {
			hints = "Alt+→: Send | Ctrl+C: Exit "
		}
	}

	titleRunes := len([]rune(title))
	hintsRunes := len([]rune(hints))
	rem := width - titleRunes - hintsRunes
	if rem < 0 {
		rem = 0
		hints = ""
	}
	return navyBg + cyan + "\x1b[1m" + title + reset + navyBg + strings.Repeat(" ", rem) + white + hints + reset
}

func runChatUIStandalone(cfg *Config, initialPrompt string, opts QueryOptions) error {
	inFd := os.Stdin.Fd()
	outFd := os.Stdout.Fd()
	state, err := makeRaw(inFd)
	if err != nil {
		return fmt.Errorf("failed to make raw: %w", err)
	}

	w, h := getTerminalSize(outFd)
	if w < 40 {
		w = 80
	}
	if h < 10 {
		h = 24
	}

	sessionID := getSessionID()
	session, err := loadSession(sessionID)
	if err != nil {
		session = &Session{
			ID:       sessionID,
			Messages: []Message{},
		}
	}

	// Enter Alternate Screen
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + "\x1b[?1000h\x1b[?1002h\x1b[?1006h" + ansiEnableKeyboard)
	_ = os.Stdout.Sync()

	contentH := h - 2
	if contentH < 4 {
		contentH = 4
	}
	chat := NewChatUI(cfg, session, opts, initialPrompt, w, contentH)

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_, _ = os.Stdout.WriteString("\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1004l\x1b[?1005l\x1b[?1006l\x1b[?2004l" + ansiDisableKeyboard + ansiLeaveAltScreen + ansiShowCursor + "\x1b[0m\r")
			_ = os.Stdout.Sync()
			_ = restoreTerm(inFd, state)
		})
	}
	defer cleanup()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)

	go func() {
		<-sigChan
		cleanup()
		os.Exit(0)
	}()

	var renderMu sync.Mutex
	curW, curH := w, h

	render := func() {
		renderMu.Lock()
		defer renderMu.Unlock()

		var sb strings.Builder
		sb.WriteString(ansiHideCursor)

		// 1. Row 1: Header bar
		sb.WriteString("\x1b[1;1H\x1b[K")
		sb.WriteString(renderStandaloneHeader(curW, false, 0, chat))

		// 2. Row 2: Divider line
		sb.WriteString("\x1b[2;1H\x1b[K")
		sb.WriteString("\x1b[38;5;39m" + strings.Repeat("─", curW) + "\x1b[0m")

		// 3. Rows 3..curH: ChatUI rows
		for r := 0; r < contentH; r++ {
			sb.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", r+3))
			sb.WriteString(chat.RenderRow(r))
		}

		// Cursor positioning
		cRow, cCol := chat.CursorPosition()
		screenRow := cRow + 3
		screenCol := cCol + 1
		if screenRow >= 3 && screenRow <= curH && screenCol >= 1 && screenCol <= curW && chat.ConfirmState == ConfirmNone {
			sb.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", screenRow, screenCol, ansiShowCursor))
		}

		_, _ = os.Stdout.WriteString(sb.String())
		_ = os.Stdout.Sync()
	}

	chat.OnNeedRender = render
	render()

	// Resize poller and SIGWINCH listener
	isExiting := false
	winchChan := make(chan os.Signal, 2)
	notifyWinch(winchChan)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	handleResize := func() {
		newW, newH := getTerminalSize(outFd)
		if newW > 0 && newH > 0 && (newW != curW || newH != curH) {
			renderMu.Lock()
			curW, curH = newW, newH
			contentH = curH - 2
			if contentH < 4 {
				contentH = 4
			}
			chat.Resize(curW, contentH)
			renderMu.Unlock()

			// Clear screen to avoid ghost lines when zooming in/out
			_, _ = os.Stdout.WriteString("\x1b[2J")
			_ = os.Stdout.Sync()
			render()
		}
	}

	go func() {
		for {
			select {
			case <-ticker.C:
				if isExiting {
					return
				}
				handleResize()
			case <-winchChan:
				if isExiting {
					return
				}
				handleResize()
			}
		}
	}()

	buf := make([]byte, 512)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			isExiting = true
			return nil
		}

		inBuf := buf[:n]

		// Mouse SGR: \x1b[<btn;x;yM or \x1b[<btn;x;ym
		if bytes.HasPrefix(inBuf, []byte("\x1b[<")) {
			s := string(inBuf)
			if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m") {
				isRelease := strings.HasSuffix(s, "m")
				raw := s[3 : len(s)-1]
				parts := strings.Split(raw, ";")
				if len(parts) == 3 {
					btn, _ := strconv.Atoi(parts[0])
					x, _ := strconv.Atoi(parts[1])
					y, _ := strconv.Atoi(parts[2])

					row := y - 3 // 0-based content row in standalone mode (rows 1 & 2 are header)
					col := x - 1

					if btn == 64 { // Wheel Up
						chat.ScrollHistory(-3)
						render()
						continue
					} else if btn == 65 { // Wheel Down
						chat.ScrollHistory(3)
						render()
						continue
					} else if btn == 0 && !isRelease && row >= 0 { // Left Click in content
						chat.HandleClick(col, row)
						render()
						continue
					} else if btn == 32 && !isRelease && row >= 0 { // Left Drag
						chat.HandleDrag(col, row, curW)
						render()
						continue
					} else if isRelease { // Mouse Release: keep selection active without auto-copying
						render()
						continue
					}
				}
			}
			continue
		}

		// When not in confirm modal, Ctrl+C prompts for exit confirmation
		if len(inBuf) == 1 && inBuf[0] == 3 && chat.ConfirmState == ConfirmNone {
			if chat.IsStreaming && chat.ActiveCancel != nil {
				chat.ActiveCancel()
				chat.ActiveCancel = nil
				render()
				continue
			}
			chat.ConfirmState = ConfirmExit
			chat.ConfirmChoice = 0
			render()
			continue
		}

		if shouldExit := chat.HandleKey(inBuf); shouldExit {
			isExiting = true
			return nil
		}
		render()
	}
}

func runChatbot(cfg *Config, initialPrompt string, opts QueryOptions) error {
	if opts.IsStdoutTerminal && isCharDevice(os.Stdin) {
		return runChatUIStandalone(cfg, initialPrompt, opts)
	}

	sessionID := getSessionID()
	session, err := loadSession(sessionID)
	if err != nil {
		session = &Session{
			ID:       sessionID,
			Messages: []Message{},
		}
	}

	// Enter Alternate Screen Buffer in interactive terminals
	if opts.IsStdoutTerminal {
		_, _ = os.Stdout.WriteString(ansiEnterAltScreen + ansiEnablePaste)
		_ = os.Stdout.Sync()
	}

	var restoreOnce sync.Once
	restoreScreen := func() {
		restoreOnce.Do(func() {
			if opts.IsStdoutTerminal {
				_, _ = os.Stdout.WriteString(ansiDisablePaste + ansiLeaveAltScreen)
				_ = os.Stdout.Sync()
			}
		})
	}
	defer restoreScreen()

	if opts.IsStdoutTerminal {
		fmt.Printf("ask v%s — Continuous Chat Mode\n", version)
		fmt.Printf("Model: %s | Session: %s\n", cfg.Model, sessionID)
		fmt.Println("Send: Alt+→ (or Ctrl+S) | Newline: Enter | Exit: /exit, /q | Cancel: Ctrl+C")
		fmt.Println()
	}

	// Setup Ctrl+C signal handling
	var mu sync.Mutex
	var activeCancel context.CancelFunc

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)

	go func() {
		for range sigChan {
			mu.Lock()
			if activeCancel != nil {
				activeCancel()
				activeCancel = nil
				fmt.Print("\n[Cancelled]\n")
			} else {
				restoreScreen()
				os.Exit(0)
			}
			mu.Unlock()
		}
	}()

	reader := bufio.NewReader(os.Stdin)

	executeQuery := func(prompt string) {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return
		}

		var history []Message
		if session != nil {
			history = session.Messages
		}
		apiMessages := buildAPIMessages(opts.IsCodeGen, history, prompt)

		queryCtx, cancel := context.WithCancel(context.Background())
		mu.Lock()
		activeCancel = cancel
		mu.Unlock()

		response, queryErr := queryOpenRouter(queryCtx, cfg, apiMessages, opts)

		mu.Lock()
		activeCancel = nil
		mu.Unlock()
		cancel()

		if queryErr != nil && queryErr != context.Canceled {
			fmt.Fprintf(os.Stderr, "Error: %v\n", queryErr)
		}

		if response != "" {
			session.Messages = append(session.Messages,
				Message{Role: "user", Content: prompt},
				Message{Role: "assistant", Content: response},
			)
			_ = saveSession(session)
			if opts.IsStdoutTerminal {
				fmt.Println()
			}
		}
	}

	// If initial prompt was supplied, run it first
	if strings.TrimSpace(initialPrompt) != "" {
		if opts.IsStdoutTerminal {
			fmt.Printf(">>> %s\n", initialPrompt)
		}
		executeQuery(initialPrompt)
	}

	isInteractive := opts.IsStdoutTerminal && isCharDevice(os.Stdin)

	for {
		input, readErr := readChatPrompt(reader, isInteractive)
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}

		if input == "" {
			continue
		}

		// STRICT: Only commands starting with '/' are chat commands.
		// Plain words like "exit" or "quit" without slash are passed to the model as literal text!
		lower := strings.ToLower(input)
		if lower == "/exit" || lower == "/quit" || lower == "/q" {
			fmt.Print("Exit ask chat? [y/N]: ")
			ans, _ := reader.ReadString('\n')
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "y") {
				break
			}
			continue
		}

		if lower == "/clear" || lower == "/c" {
			fmt.Print("Clear conversation memory? [y/N]: ")
			ans, _ := reader.ReadString('\n')
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "y") {
				_ = clearSession(sessionID)
				session = &Session{
					ID:       sessionID,
					Messages: []Message{},
				}
				fmt.Printf("Session context cleared (%s).\n\n", sessionID)
			}
			continue
		}

		if lower == "/help" || lower == "/?" {
			fmt.Println(`Chat Mode Commands:
  /btw <question>   Ask a tangent using past context without saving to history
  /clear, /c        Clear conversation memory for this session
  /help             Show this help message
  /exit, /quit, /q  Exit chat mode`)
			fmt.Println()
			continue
		}

		if strings.HasPrefix(lower, "/btw") {
			btwQuery := strings.TrimSpace(input[len("/btw"):])
			if btwQuery == "" {
				fmt.Println("Usage: /btw <question> (Ask a quick tangent using past context without saving to history)")
				fmt.Println()
				continue
			}
			// Execute with past context, but do not save to session
			var history []Message
			if session != nil {
				history = session.Messages
			}
			apiMessages := buildAPIMessages(opts.IsCodeGen, history, btwQuery)
			queryCtx := context.Background()
			response, queryErr := queryOpenRouter(queryCtx, cfg, apiMessages, opts)
			if queryErr != nil {
				fmt.Printf("\nError: %v\n", queryErr)
			}
			_ = response
			continue
		}

		executeQuery(input)
	}

	return nil
}
