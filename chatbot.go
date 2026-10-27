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

func isChatCommand(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "exit" || lower == "quit" || lower == "/q" || lower == ":q" ||
		lower == "/exit" || lower == "/quit" ||
		lower == "/clear" || lower == "/c" ||
		lower == "/help" || lower == "/?" ||
		strings.HasPrefix(lower, "/model") {
		return true
	}
	return false
}

func readChatPrompt(reader *bufio.Reader, isStdoutTerminal bool) (string, error) {
	var lines []string

	for {
		if isStdoutTerminal {
			if len(lines) == 0 {
				fmt.Print(">>> ")
			} else {
				fmt.Print("... ")
			}
		}

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

		// Detect terminal escape sequences for Ctrl+Enter (CSI u and xterm formats)
		hasCtrlEnter := strings.Contains(line, "\x1b[13;5u") || strings.Contains(line, "\x1b[27;5;13~")
		if hasCtrlEnter {
			line = strings.ReplaceAll(line, "\x1b[13;5u", "")
			line = strings.ReplaceAll(line, "\x1b[27;5;13~", "")
		}

		// If first line and it's an immediate command (exit, /clear, /model, /help), submit immediately
		if len(lines) == 0 {
			trimmed := strings.TrimSpace(line)
			if isChatCommand(trimmed) {
				return trimmed, nil
			}
		}

		// If Ctrl+Enter was detected, submit immediately
		if hasCtrlEnter {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
			break
		}

		// Ignore blank Enter on initial prompt if nothing entered yet
		if len(lines) == 0 && strings.TrimSpace(line) == "" {
			continue
		}

		// Blank lines inside prompt are PRESERVED as newlines (Bug 2 fix: never submit on empty lines!)
		lines = append(lines, line)
	}

	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func runChatbot(cfg *Config, initialPrompt string, opts QueryOptions) error {
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
		if opts.OutputFile != "" {
			fmt.Printf("Saving chat to: %s\n", opts.OutputFile)
		} else if opts.AppendFile != "" {
			fmt.Printf("Appending chat to: %s\n", opts.AppendFile)
		}
		fmt.Println("Send: Ctrl+Enter (or Ctrl+D) | Newline: Enter | Exit: exit, /q, /exit | Cancel: Ctrl+C")
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

	// In chatbot mode, if writing to file, subsequent turns should append
	chatQueryOpts := opts
	if chatQueryOpts.OutputFile != "" {
		chatQueryOpts.AppendFile = chatQueryOpts.OutputFile
		chatQueryOpts.OutputFile = ""
	}

	executeQuery := func(prompt string) {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return
		}

		var apiMessages []Message
		activePrompt := systemPrompt
		if chatQueryOpts.IsCodeGen {
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

		response, queryErr := queryOpenRouter(queryCtx, cfg, apiMessages, chatQueryOpts)

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

	for {
		input, readErr := readChatPrompt(reader, opts.IsStdoutTerminal)
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}

		if input == "" {
			continue
		}

		// Check REPL commands
		lower := strings.ToLower(input)
		if lower == "exit" || lower == "quit" || lower == "/q" || lower == ":q" || lower == "/exit" || lower == "/quit" {
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
  exit, quit, /q    Exit chat mode`)
			fmt.Println()
			continue
		}

		executeQuery(input)
	}

	return nil
}
