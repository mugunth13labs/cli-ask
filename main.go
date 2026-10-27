package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	version = "0.3.2"
)

type ParsedArgs struct {
	Action     string // "help", "version", "config", "clear", "set-key", "set-model", "update-self", "chatbot", "query"
	ActionArg  string // value for --set-key or --set-model
	Prompt     string // collected prompt tokens
	UseContext bool   // true unless --no-context is provided
}

func parseCLIArgs(args []string) ParsedArgs {
	result := ParsedArgs{
		Action:     "query",
		UseContext: true,
	}

	if len(args) == 0 {
		return result
	}

	// 1. Check for standalone commands (len == 1)
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help", "help":
			result.Action = "help"
			return result
		case "-v", "--version", "version":
			result.Action = "version"
			return result
		case "-c", "--clear":
			result.Action = "clear"
			return result
		case "--config":
			result.Action = "config"
			return result
		case "--update-self", "--update":
			result.Action = "update-self"
			return result
		case "-i", "--chatbot":
			result.Action = "chatbot"
			return result
		}
	}

	// 2. Check for parametric commands (--set-key / --set-model)
	if args[0] == "--set-key" {
		result.Action = "set-key"
		if len(args) > 1 {
			result.ActionArg = args[1]
		}
		return result
	}
	if args[0] == "--set-model" {
		result.Action = "set-model"
		if len(args) > 1 {
			result.ActionArg = args[1]
		}
		return result
	}

	// 3. Check for leading chatbot mode flag: ask -i <prompt> or ask --chatbot <prompt>
	startIndex := 0
	if args[0] == "-i" || args[0] == "--chatbot" {
		result.Action = "chatbot"
		startIndex = 1
	}

	// 4. Process prompt tokens, handling `--` separator & `--no-context`
	var promptTokens []string
	literalMode := false

	for i := startIndex; i < len(args); i++ {
		arg := args[i]

		if literalMode {
			promptTokens = append(promptTokens, arg)
			continue
		}

		if arg == "--" {
			literalMode = true
			continue
		}

		if arg == "--no-context" {
			result.UseContext = false
			continue
		}

		promptTokens = append(promptTokens, arg)
	}

	result.Prompt = strings.Join(promptTokens, " ")
	return result
}

func readInteractivePrompt() (string, error) {
	eofHint := "Ctrl+D"
	if runtime.GOOS == "windows" {
		eofHint = "Ctrl+Z then Enter"
	}

	fmt.Fprintf(os.Stderr, "Type or paste prompt (press %s to submit, Ctrl+C to cancel):\n", eofHint)

	bytesRead, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(bytesRead)), nil
}

func readPipedStdinWithTimeout(maxWait time.Duration) string {
	type chunk struct {
		data []byte
		err  error
	}
	ch := make(chan chunk, 1)

	go func() {
		b, err := io.ReadAll(os.Stdin)
		ch <- chunk{data: b, err: err}
	}()

	select {
	case res := <-ch:
		if res.err == nil && len(res.data) > 0 {
			return strings.TrimSpace(string(res.data))
		}
		return ""
	case <-time.After(maxWait):
		return ""
	}
}

func printHelp() {
	eofHint := "Ctrl+D"
	if runtime.GOOS == "windows" {
		eofHint = "Ctrl+Z then Enter"
	}

	fmt.Printf(`ask v%s — Lightning-fast terminal AI query tool with session context

Usage:
  ask <prompt>                  Ask any question (remembers terminal session)
  ask "your prompt here"        Ask with quotes
  ask                           Interactive multiline entry (ends with %s)
  ask --chatbot, -i             Start continuous interactive chat session
  echo "code" | ask "explain"   Pipe stdin to ask
  ask -- <prompt>               Treat everything after -- as literal prompt

Chatbot Mode:
  ask --chatbot, -i             Continuous multi-turn chat session with REPL
  ask -i <prompt>               Start chat mode with an initial prompt

Session Memory:
  ask --clear, -c               Clear current terminal's conversation history
  ask --no-context <prompt>     Run query without reading or updating session history

Configuration:
  ask --set-key <key>           Save OpenRouter API key
  ask --set-model <model>       Set default model (default: %s)
  ask --config                  Show current configuration file and settings
  ask --update-self, --update   Check for updates and update in-place
  ask --version, -v             Show version
  ask --help, -h                Show this help message

Environment:
  OPENROUTER_API_KEY            Overrides configured API key
`, version, eofHint, defaultModel)
}

func main() {
	parsed := parseCLIArgs(os.Args[1:])

	switch parsed.Action {
	case "help":
		printHelp()
		return
	case "version":
		fmt.Printf("ask v%s\n", version)
		return
	case "update-self":
		if err := selfUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "Update error: %v\n", err)
			os.Exit(1)
		}
		return
	case "clear":
		sessionID := getSessionID()
		if err := clearSession(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "Error clearing session: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Session context cleared (%s).\n", sessionID)
		return
	case "config":
		cfg, _ := loadConfig()
		cfgPath, _ := getConfigPath()
		fmt.Printf("Config file: %s\n", cfgPath)
		maskedKey := "None"
		if len(cfg.APIKey) > 8 {
			maskedKey = cfg.APIKey[:4] + "..." + cfg.APIKey[len(cfg.APIKey)-4:]
		} else if cfg.APIKey != "" {
			maskedKey = "****"
		}
		fmt.Printf("Model:   %s\n", cfg.Model)
		fmt.Printf("API Key: %s\n", maskedKey)
		fmt.Printf("Session: %s\n", getSessionID())
		return
	case "set-key":
		if parsed.ActionArg == "" {
			fmt.Println("Error: Missing key argument. Usage: ask --set-key <YOUR_API_KEY>")
			os.Exit(1)
		}
		cfg, _ := loadConfig()
		cfg.APIKey = parsed.ActionArg
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("API key saved successfully.")
		return
	case "set-model":
		if parsed.ActionArg == "" {
			fmt.Println("Error: Missing model argument. Usage: ask --set-model <MODEL_NAME>")
			os.Exit(1)
		}
		cfg, _ := loadConfig()
		cfg.Model = parsed.ActionArg
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Default model set to: %s\n", cfg.Model)
		return
	}

	// Detect terminal capabilities
	isStdinTerminal := isCharDevice(os.Stdin)
	isStdoutTerminal := isCharDevice(os.Stdout)
	isStderrTerminal := isCharDevice(os.Stderr)

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if cfg.APIKey == "" {
		key, err := promptAPIKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg.APIKey = key
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save config file: %v\n", err)
		} else {
			cfgPath, _ := getConfigPath()
			fmt.Printf("Saved API key to %s\n\n", cfgPath)
		}
	}

	if parsed.Action == "chatbot" {
		if err := runChatbot(cfg, parsed.Prompt, isStdoutTerminal, isStderrTerminal); err != nil {
			fmt.Fprintf(os.Stderr, "Chat error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	userPrompt := parsed.Prompt

	// If no prompt in args:
	if strings.TrimSpace(userPrompt) == "" {
		if isStdinTerminal {
			// Interactive multiline entry terminated by OS EOF
			prompt, err := readInteractivePrompt()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
				os.Exit(1)
			}
			userPrompt = prompt
		} else {
			// Piped stdin with no args
			stdinBytes, err := io.ReadAll(os.Stdin)
			if err == nil {
				userPrompt = strings.TrimSpace(string(stdinBytes))
			}
		}
	} else if !isStdinTerminal {
		// Prompt given in args + piped stdin potentially attached
		stdinText := readPipedStdinWithTimeout(150 * time.Millisecond)
		if stdinText != "" {
			userPrompt = userPrompt + "\n\nContext:\n" + stdinText
		}
	}

	if strings.TrimSpace(userPrompt) == "" {
		printHelp()
		return
	}

	sessionID := getSessionID()
	var currentSession *Session
	if parsed.UseContext {
		currentSession, _ = loadSession(sessionID)
	}

	var apiMessages []Message
	apiMessages = append(apiMessages, Message{Role: "system", Content: systemPrompt})

	if parsed.UseContext && currentSession != nil && len(currentSession.Messages) > 0 {
		compacted := compactHistory(currentSession.Messages)
		apiMessages = append(apiMessages, compacted...)
	}

	apiMessages = append(apiMessages, Message{Role: "user", Content: userPrompt})

	ctx := context.Background()
	response, queryErr := queryOpenRouter(ctx, cfg, apiMessages, isStdoutTerminal, isStderrTerminal)
	if queryErr != nil {
		fmt.Fprintf(os.Stderr, "\nQuery error: %v\n", queryErr)
		os.Exit(1)
	}

	if parsed.UseContext && currentSession != nil && response != "" {
		currentSession.Messages = append(currentSession.Messages,
			Message{Role: "user", Content: userPrompt},
			Message{Role: "assistant", Content: response},
		)
		_ = saveSession(currentSession)
	}
}
