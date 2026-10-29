package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	version = "0.4.0"
)

var codeFileExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".pbs": true, ".slurm": true,
	".py": true, ".go": true, ".rs": true, ".js": true, ".ts": true,
	".c": true, ".cpp": true, ".cc": true, ".h": true, ".hpp": true,
	".java": true, ".rb": true, ".php": true, ".lua": true, ".sql": true,
	".r": true, ".yaml": true, ".yml": true, ".json": true, ".toml": true,
	".ini": true, ".conf": true, ".xml": true, ".html": true, ".css": true,
}

type ParsedArgs struct {
	Action     string // "help", "version", "config", "clear", "set-key", "set-model", "update-self", "chatbot", "query"
	ActionArg  string // value for --set-key or --set-model
	OutputFile string // path for -o / --output
	AppendFile string // path for -a / --append
	CodeGen    bool   // pure code generation mode (--cg / --codegen)
	Split      bool   // split screen mode (-s / --split)
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
		case "-s", "--split":
			result.Action = "chatbot"
			result.Split = true
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

	// 3. Process leading options and prompt tokens
	var promptTokens []string
	literalMode := false
	flagsAllowed := true

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if literalMode {
			promptTokens = append(promptTokens, arg)
			continue
		}

		if arg == "--" {
			literalMode = true
			flagsAllowed = false
			continue
		}

		if flagsAllowed {
			if arg == "-i" || arg == "--chatbot" {
				result.Action = "chatbot"
				continue
			}
			if arg == "-s" || arg == "--split" {
				result.Split = true
				if result.Action == "query" {
					result.Action = "chatbot"
				}
				continue
			}
			if arg == "--no-context" {
				result.UseContext = false
				continue
			}
			if arg == "-cg" || arg == "--cg" || arg == "--codegen" {
				result.CodeGen = true
				continue
			}
			if arg == "-o" || arg == "--output" {
				if i+1 < len(args) {
					result.OutputFile = args[i+1]
					i++
				}
				continue
			}
			if arg == "-a" || arg == "--append" {
				if i+1 < len(args) {
					result.AppendFile = args[i+1]
					i++
				}
				continue
			}

			// First non-option argument encountered: subsequent flags belong to the user's prompt
			flagsAllowed = false
		}

		promptTokens = append(promptTokens, arg)
	}

	result.Prompt = strings.Join(promptTokens, " ")
	return result
}

func validateArgs(parsed *ParsedArgs) error {
	if parsed.Action == "chatbot" && (parsed.OutputFile != "" || parsed.AppendFile != "") {
		return fmt.Errorf("interactive chatbot mode (-i/--chatbot) is incompatible with direct file output (-o/--output, -a/--append)")
	}
	return nil
}

func detectCodeGen(parsed *ParsedArgs, userPrompt string, stdinText string) bool {
	if parsed.CodeGen {
		return true
	}

	// Check destination file name / extension
	target := parsed.OutputFile
	if target == "" {
		target = parsed.AppendFile
	}
	if target != "" {
		base := strings.ToLower(filepath.Base(target))
		if base == "dockerfile" || base == "makefile" || base == "containerfile" {
			return true
		}
		ext := strings.ToLower(filepath.Ext(target))
		if codeFileExts[ext] {
			return true
		}
	}

	// Check shebang or script markers in stdin or prompt
	combined := strings.ToLower(stdinText + " " + userPrompt)
	if strings.Contains(stdinText, "#!/") || strings.Contains(stdinText, "\n#PBS") || strings.Contains(stdinText, "\n#SBATCH") {
		return true
	}

	// Check prompt keywords for code/script intent
	codeTriggers := []string{
		"generate a script", "generate script", "write a script", "write script",
		"write code", "generate code", "remove alien lines", "remove all the alien lines",
		"refactor this script", "refactor script", "fix this script", "fix the script",
		"pbs script", "slurm script", "bash script", "shell script", "python script",
		"dockerfile", "makefile", "kubernetes manifest", "systemd service unit",
	}
	for _, trigger := range codeTriggers {
		if strings.Contains(combined, trigger) {
			return true
		}
	}

	return false
}

func readInteractivePrompt(targetDesc string) (string, error) {
	content, isCancelled, err := runInlineComposer(targetDesc)
	if err != nil {
		return "", err
	}
	if isCancelled {
		os.Exit(0)
	}
	return strings.TrimSpace(content), nil
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
	fmt.Printf(`ask v%s — Lightning-fast terminal AI query tool with session context

Usage:
  ask <prompt>                  Ask any question (remembers terminal session)
  ask "your prompt here"        Ask with quotes
  ask                           Inline multiline composer (Ctrl+Enter to submit)
  ask --cg, --codegen <prompt>  Pure code generation mode (no extra fluff or titles)
  ask --chatbot, -i             Start immersive continuous interactive chat session
  echo "code" | ask "explain"   Pipe stdin to ask
  ask -- <prompt>               Treat everything after -- as literal prompt

File Output:
  ask -o, --output <file>       Write AI response directly to file (overwrites)
  ask -a, --append <file>       Append AI response directly to file

Code Generation:
  ask --cg, --codegen           Generate pure executable code/scripts without commentary
  ask -o script.sh "prompt"     Auto-enables codegen mode for script/code file extensions

Chatbot Mode:
  ask --chatbot, -i             Immersive multi-turn chat session in alternate screen
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
`, version, defaultModel)
}

func main() {
	parsed := parseCLIArgs(os.Args[1:])

	if err := validateArgs(&parsed); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

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

	queryOpts := QueryOptions{
		OutputFile:       parsed.OutputFile,
		AppendFile:       parsed.AppendFile,
		IsStdoutTerminal: isStdoutTerminal,
		IsStderrTerminal: isStderrTerminal,
	}

	// Dispatch chatbot immediately before reading stdin prompts
	if parsed.Action == "chatbot" {
		if parsed.Split {
			if !isStdinTerminal || !isStdoutTerminal {
				fmt.Fprintln(os.Stderr, "Error: dual pane workspace requires an interactive terminal.")
				os.Exit(1)
			}
			if err := RunDualPaneWorkspace(cfg, parsed.Prompt, queryOpts); err != nil {
				fmt.Fprintf(os.Stderr, "Dual pane error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if err := runChatbot(cfg, parsed.Prompt, queryOpts); err != nil {
			fmt.Fprintf(os.Stderr, "Chat error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	userPrompt := parsed.Prompt
	var pipedStdinText string

	// If no prompt in args:
	if strings.TrimSpace(userPrompt) == "" {
		if isStdinTerminal {
			// Interactive composer page in alternate screen
			targetDesc := parsed.OutputFile
			if targetDesc == "" {
				targetDesc = parsed.AppendFile
			}
			prompt, err := readInteractivePrompt(targetDesc)
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
		pipedStdinText = readPipedStdinWithTimeout(150 * time.Millisecond)
		if pipedStdinText != "" {
			userPrompt = userPrompt + "\n\nContext:\n" + pipedStdinText
		}
	}

	isCodeGen := detectCodeGen(&parsed, userPrompt, pipedStdinText)
	queryOpts.IsCodeGen = isCodeGen

	if strings.TrimSpace(userPrompt) == "" {
		printHelp()
		return
	}

	sessionID := getSessionID()
	var currentSession *Session
	if parsed.UseContext {
		currentSession, _ = loadSession(sessionID)
	}

	// Select system prompt based on codegen mode
	activeSystemPrompt := systemPrompt
	if isCodeGen {
		activeSystemPrompt = codePrompt
	}

	var apiMessages []Message
	apiMessages = append(apiMessages, Message{Role: "system", Content: activeSystemPrompt})

	if parsed.UseContext && currentSession != nil && len(currentSession.Messages) > 0 {
		compacted := compactHistory(currentSession.Messages)
		apiMessages = append(apiMessages, compacted...)
	}

	apiMessages = append(apiMessages, Message{Role: "user", Content: userPrompt})

	ctx := context.Background()
	response, queryErr := queryOpenRouter(ctx, cfg, apiMessages, queryOpts)
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
