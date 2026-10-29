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
		strings.HasPrefix(lower, "/model") {
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

		// Detect terminal escape sequences for Ctrl+Enter (CSI u and xterm formats)
		hasCtrlEnter := strings.Contains(line, "\x1b[13;5u") || strings.Contains(line, "\x1b[27;5;13~")
		if hasCtrlEnter {
			line = strings.ReplaceAll(line, "\x1b[13;5u", "")
			line = strings.ReplaceAll(line, "\x1b[27;5;13~", "")
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
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
	if confirmExit {
		prompt := " Exit ask chat?  "
		yes := " [ Yes ] "
		no := " [ No ] "
		if exitChoice == 0 {
			yes = "\x1b[30;47;1m Yes \x1b[0m\x1b[7;1m"
			no = "  No  "
		} else {
			yes = "  Yes  "
			no = "\x1b[30;47;1m No \x1b[0m\x1b[7;1m"
		}
		hints := " (←/→: Choose | Enter: Confirm | Esc: Cancel) "
		text := prompt + yes + "  " + no + hints
		rem := width - len([]rune(text))
		if rem < 0 {
			rem = 0
		}
		return "\x1b[7;1m" + text + strings.Repeat(" ", rem) + "\x1b[0m"
	}

	title := " ask chat"
	var hints string

	if chat != nil && chat.Selecting {
		hints = "[ Text Selected ]  y / Enter: Copy "
	} else if chat != nil && chat.ActiveRegion == RegionHistory {
		hints = "↑/↓: Browse | Shift+Arrows: Select | ↑: Prompt "
	} else {
		// In Prompt composer
		hints = "Ctrl+Enter: Send | /help /clear /model /exit | Ctrl+C: Exit "
		if width < 85 {
			hints = "Ctrl+Enter: Send | / commands | Ctrl+C: Exit "
		}
		if width < 60 {
			hints = "Ctrl+Enter: Send | Ctrl+C: Exit "
		}
	}

	titleRunes := len([]rune(title))
	hintsRunes := len([]rune(hints))
	rem := width - titleRunes - hintsRunes
	if rem < 0 {
		rem = 0
		hints = ""
	}
	return "\x1b[7;1m" + title + strings.Repeat(" ", rem) + hints + "\x1b[0m"
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
	_, _ = os.Stdout.WriteString(ansiEnterAltScreen + "\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	_ = os.Stdout.Sync()

	contentH := h - 2
	if contentH < 4 {
		contentH = 4
	}
	chat := NewChatUI(cfg, session, opts, initialPrompt, w, contentH)

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = restoreTerm(inFd, state)
			_, _ = os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l" + ansiLeaveAltScreen + ansiShowCursor)
			_ = os.Stdout.Sync()
		})
	}
	defer cleanup()

	var renderMu sync.Mutex
	curW, curH := w, h
	confirmExit := false
	exitChoice := 0 // 0 = Yes, 1 = No

	render := func() {
		renderMu.Lock()
		defer renderMu.Unlock()

		var sb strings.Builder
		sb.WriteString(ansiHideCursor)

		// 1. Row 1: Header bar
		sb.WriteString("\x1b[1;1H\x1b[K")
		sb.WriteString(renderStandaloneHeader(curW, confirmExit, exitChoice, chat))

		// 2. Row 2: Divider line
		sb.WriteString("\x1b[2;1H\x1b[K")
		sb.WriteString("\x1b[90m" + strings.Repeat("─", curW) + "\x1b[0m")

		// 3. Rows 3..curH: ChatUI rows
		for r := 0; r < contentH; r++ {
			sb.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", r+3))
			sb.WriteString(chat.RenderRow(r))
		}

		// Cursor positioning
		cRow, cCol := chat.CursorPosition()
		screenRow := cRow + 3
		screenCol := cCol + 1
		if screenRow >= 3 && screenRow <= curH && screenCol >= 1 && screenCol <= curW && !confirmExit {
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

		// Exit Confirmation Mode handling
		if confirmExit {
			// Left or Right arrow: toggle Yes / No
			if bytes.Equal(inBuf, []byte("\x1b[D")) || bytes.Equal(inBuf, []byte("\x1b[C")) {
				exitChoice = 1 - exitChoice
				render()
				continue
			}
			// Enter key: confirm choice
			isEnter := (len(inBuf) == 1 && inBuf[0] == 13) || bytes.Equal(inBuf, []byte("\r\n"))
			if isEnter {
				if exitChoice == 0 {
					// Confirmed Yes -> Exit
					isExiting = true
					return nil
				}
				// Confirmed No -> Cancel confirmation
				confirmExit = false
				render()
				continue
			}
			// Esc key: cancel confirmation
			if len(inBuf) == 1 && inBuf[0] == 27 {
				confirmExit = false
				render()
				continue
			}
			// Ctrl+C: force exit immediately
			if len(inBuf) == 1 && inBuf[0] == 3 {
				isExiting = true
				return nil
			}
			// Ignore other keys while in confirmation mode
			continue
		}

		// When NOT in confirmExit:
		// Intercept Ctrl+C (0x03)
		if len(inBuf) == 1 && inBuf[0] == 3 {
			if chat.IsStreaming && chat.ActiveCancel != nil {
				chat.ActiveCancel()
				chat.ActiveCancel = nil
				render()
				continue
			}
			// Prompt for exit!
			confirmExit = true
			exitChoice = 0 // default Yes
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
		fmt.Println("Send: Ctrl+Enter | Newline: Enter | Exit: /exit, /q | Cancel: Ctrl+C")
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

		var apiMessages []Message
		activePrompt := systemPrompt
		if opts.IsCodeGen {
			activePrompt = codePrompt
		}
		apiMessages = append(apiMessages, Message{Role: "system", Content: activePrompt})

		if len(session.Messages) > 0 {
			compacted := compactHistory(session.Messages)
			apiMessages = append(apiMessages, compacted...)
		}
		apiMessages = append(apiMessages, Message{Role: "user", Content: prompt})

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
			break
		}

		if lower == "/clear" || lower == "/c" {
			_ = clearSession(sessionID)
			session = &Session{
				ID:       sessionID,
				Messages: []Message{},
			}
			fmt.Printf("Session context cleared (%s).\n\n", sessionID)
			continue
		}

		if strings.HasPrefix(lower, "/model") {
			parts := strings.Fields(input)
			if len(parts) > 1 {
				cfg.Model = parts[1]
				fmt.Printf("Switched model to: %s\n\n", cfg.Model)
			} else {
				fmt.Printf("Current model: %s\n\n", cfg.Model)
			}
			continue
		}

		if lower == "/help" || lower == "/?" {
			fmt.Println(`Chat Mode Commands:
  /clear, /c        Clear conversation memory for this session
  /model <name>     Switch active model for this session
  /help             Show this help message
  /exit, /quit, /q  Exit chat mode`)
			fmt.Println()
			continue
		}

		executeQuery(input)
	}

	return nil
}
