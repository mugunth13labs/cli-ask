package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
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

func runChatUIStandalone(cfg *Config, initialPrompt string, opts QueryOptions) error {
	fd := os.Stdout.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to make raw: %w", err)
	}

	w, h := getTerminalSize(fd)
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

	chat := NewChatUI(cfg, session, opts, initialPrompt, w, h)

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = restoreTerm(fd, state)
			_, _ = os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l" + ansiLeaveAltScreen + ansiShowCursor)
			_ = os.Stdout.Sync()
		})
	}
	defer cleanup()

	render := func() {
		var sb strings.Builder
		sb.WriteString(ansiHideCursor)
		for r := 0; r < h; r++ {
			sb.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", r+1))
			sb.WriteString(chat.RenderRow(r))
		}
		cRow, cCol := chat.CursorPosition()
		sb.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", cRow+1, cCol+1, ansiShowCursor))
		_, _ = os.Stdout.WriteString(sb.String())
		_ = os.Stdout.Sync()
	}

	chat.OnNeedRender = render
	render()

	buf := make([]byte, 512)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil
		}
		if shouldExit := chat.HandleKey(buf[:n]); shouldExit {
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
