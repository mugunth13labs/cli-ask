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

func runChatbot(cfg *Config, initialPrompt string, isStdoutTerminal bool, isStderrTerminal bool) error {
	sessionID := getSessionID()
	session, err := loadSession(sessionID)
	if err != nil {
		session = &Session{
			ID:        sessionID,
			Messages:  []Message{},
		}
	}

	if isStdoutTerminal {
		fmt.Printf("ask v%s — Continuous Chat Mode\n", version)
		fmt.Printf("Model: %s | Session: %s\n", cfg.Model, sessionID)
		fmt.Println("Type your message below. Commands: /clear, /model <name>, /help, exit")
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
				if isStdoutTerminal {
					fmt.Print("\nGoodbye!\n")
				}
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
		apiMessages = append(apiMessages, Message{Role: "system", Content: systemPrompt})
		if len(session.Messages) > 0 {
			compacted := compactHistory(session.Messages)
			apiMessages = append(apiMessages, compacted...)
		}
		apiMessages = append(apiMessages, Message{Role: "user", Content: prompt})

		queryCtx, cancel := context.WithCancel(context.Background())
		mu.Lock()
		activeCancel = cancel
		mu.Unlock()

		response, queryErr := queryOpenRouter(queryCtx, cfg, apiMessages, isStdoutTerminal, isStderrTerminal)

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
			if isStdoutTerminal {
				fmt.Println()
			}
		}
	}

	// If initial prompt was supplied, run it first
	if strings.TrimSpace(initialPrompt) != "" {
		if isStdoutTerminal {
			fmt.Printf(">>> %s\n", initialPrompt)
		}
		executeQuery(initialPrompt)
	}

	for {
		if isStdoutTerminal {
			fmt.Print(">>> ")
		}

		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr == io.EOF {
				if isStdoutTerminal {
					fmt.Println()
				}
				break
			}
			return readErr
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check REPL commands
		lower := strings.ToLower(line)
		if lower == "exit" || lower == "quit" || lower == "/q" || lower == ":q" {
			if isStdoutTerminal {
				fmt.Println("Goodbye!")
			}
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
			parts := strings.Fields(line)
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

		executeQuery(line)
	}

	return nil
}
