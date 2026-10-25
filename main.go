package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	version       = "0.3.1"
	openRouterURL = "https://openrouter.ai/api/v1/chat/completions"
	defaultModel  = "nvidia/nemotron-3.5-lightning:free"
	systemPrompt  = "You are a concise, helpful terminal CLI assistant. Provide direct, succinct answers without unnecessary fluff, pleasantries, or verbose commentary unless explicitly asked. Format code or commands clearly."

	// Compaction thresholds
	maxRecentMessages = 6    // keep last 6 messages (3 conversation turns) verbatim
	maxTotalMessages  = 12   // trigger compaction when exceeding this count
	maxCompactChars   = 8000 // trigger compaction if character count exceeds ~8k
)

type Config struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Session struct {
	ID        string    `json:"id"`
	Messages  []Message `json:"messages"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type StreamDelta struct {
	Content string `json:"content"`
}

type StreamChoice struct {
	Delta StreamDelta `json:"delta"`
}

type StreamResponse struct {
	Choices []StreamChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

type ParsedArgs struct {
	Action     string // "help", "version", "config", "clear", "set-key", "set-model", "query"
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

	// 3. Process query tokens and handle `--` separator & `--no-context`
	var promptTokens []string
	literalMode := false

	for i := 0; i < len(args); i++ {
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

func getConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ask"), nil
}

func getConfigPath() (string, error) {
	dir, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func getSessionsDir() (string, error) {
	dir, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

func getSessionID() string {
	ppid := os.Getppid()
	return fmt.Sprintf("session-%d", ppid)
}

func getSessionPath(sessionID string) (string, error) {
	sDir, err := getSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sDir, sessionID+".json"), nil
}

func loadSession(sessionID string) (*Session, error) {
	s := &Session{
		ID:        sessionID,
		Messages:  []Message{},
		UpdatedAt: time.Now(),
	}

	sPath, err := getSessionPath(sessionID)
	if err != nil {
		return s, err
	}

	data, err := os.ReadFile(sPath)
	if err != nil {
		return s, nil
	}

	if err := json.Unmarshal(data, s); err != nil {
		return s, nil
	}

	return s, nil
}

func saveSession(s *Session) error {
	sDir, err := getSessionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sDir, 0700); err != nil {
		return err
	}

	sPath, err := getSessionPath(s.ID)
	if err != nil {
		return err
	}

	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sPath, data, 0600)
}

func clearSession(sessionID string) error {
	sPath, err := getSessionPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(sPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func compactHistory(messages []Message) []Message {
	if len(messages) <= maxRecentMessages {
		return messages
	}

	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}

	if len(messages) < maxTotalMessages && totalChars < maxCompactChars {
		return messages
	}

	splitIdx := len(messages) - maxRecentMessages
	older := messages[:splitIdx]
	recent := messages[splitIdx:]

	var summaryBuilder strings.Builder
	summaryBuilder.WriteString("[Prior conversation context summary:\n")
	for _, m := range older {
		prefix := "User"
		if m.Role == "assistant" {
			prefix = "Assistant"
		}
		content := strings.TrimSpace(m.Content)
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")
		summaryBuilder.WriteString(fmt.Sprintf("- %s: %s\n", prefix, content))
	}
	summaryBuilder.WriteString("]")

	compactedMsg := Message{
		Role:    "system",
		Content: summaryBuilder.String(),
	}

	result := make([]Message, 0, len(recent)+1)
	result = append(result, compactedMsg)
	result = append(result, recent...)
	return result
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		Model: defaultModel,
	}

	if envKey := os.Getenv("OPENROUTER_API_KEY"); envKey != "" {
		cfg.APIKey = envKey
	}

	cfgPath, err := getConfigPath()
	if err != nil {
		return cfg, nil
	}

	data, err := os.ReadFile(cfgPath)
	if err == nil {
		var fileCfg Config
		if err := json.Unmarshal(data, &fileCfg); err == nil {
			if cfg.APIKey == "" && fileCfg.APIKey != "" {
				cfg.APIKey = fileCfg.APIKey
			}
			if fileCfg.Model != "" {
				cfg.Model = fileCfg.Model
			}
		}
	}

	return cfg, nil
}

func saveConfig(cfg *Config) error {
	cfgPath, err := getConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cfgPath, data, 0600)
}

func promptAPIKey() (string, error) {
	fmt.Print("OpenRouter API Key not configured.\nPlease enter your OpenRouter API key: ")
	reader := bufio.NewReader(os.Stdin)
	key, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("API key cannot be empty")
	}
	return key, nil
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


type GitHubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type GitHubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []GitHubReleaseAsset `json:"assets"`
}

func selfUpdate() error {
	repo := "mugunth13labs/cli-ask"
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	fmt.Fprintln(os.Stderr, "Checking for latest release...")
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ask-cli")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error checking for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API error: HTTP %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse release info: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentVersion := strings.TrimPrefix(version, "v")

	if latestVersion == currentVersion {
		fmt.Printf("ask is already up to date (v%s).\n", version)
		return nil
	}

	// Determine matching asset name for current OS and architecture
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	expectedAsset := fmt.Sprintf("ask-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == expectedAsset {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no matching binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("unable to resolve symlink: %w", err)
	}

	fmt.Printf("Updating ask: v%s -> %s...\n", version, release.TagName)
	fmt.Printf("Downloading %s...\n", downloadURL)

	dlResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", dlResp.StatusCode)
	}

	execDir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(execDir, "ask-update-*"+ext)
	if err != nil {
		return fmt.Errorf("failed to create temporary update file (check directory write permissions): %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, dlResp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write update file: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic replacement: on Windows rename existing running binary first, then move new one
	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("failed to rename current executable: %w", err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			_ = os.Rename(oldPath, execPath) // rollback
			return fmt.Errorf("failed to replace executable: %w", err)
		}
		_ = os.Remove(oldPath)
	} else {
		// Unix atomic swap
		if err := os.Rename(tmpPath, execPath); err != nil {
			return fmt.Errorf("failed to replace executable at %s (try running with sudo?): %w", execPath, err)
		}
	}

	fmt.Printf("Successfully updated ask to %s!\n", release.TagName)
	return nil
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
  echo "code" | ask "explain"   Pipe stdin to ask
  ask -- <prompt>               Treat everything after -- as literal prompt

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

	userPrompt := parsed.Prompt

	// Check stdin state
	stat, _ := os.Stdin.Stat()
	isTerminal := (stat.Mode() & os.ModeCharDevice) != 0

	// If no prompt in args:
	if strings.TrimSpace(userPrompt) == "" {
		if isTerminal {
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
	} else if !isTerminal {
		// Prompt given in args + piped stdin attached
		stdinBytes, err := io.ReadAll(os.Stdin)
		if err == nil && len(stdinBytes) > 0 {
			stdinText := strings.TrimSpace(string(stdinBytes))
			userPrompt = userPrompt + "\n\nContext:\n" + stdinText
		}
	}

	if strings.TrimSpace(userPrompt) == "" {
		printHelp()
		return
	}

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

	reqBody := ChatRequest{
		Model:    cfg.Model,
		Messages: apiMessages,
		Stream:   true,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	req, err := http.NewRequest("POST", openRouterURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating HTTP request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/mugunth13labs/cli-ask")
	req.Header.Set("X-Title", "ask-cli")

	// Universal visual feedback: print thinking status to stderr
	if isTerminal {
		fmt.Fprintf(os.Stderr, "[%s thinking...]", cfg.Model)
	}

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if isTerminal {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		fmt.Fprintf(os.Stderr, "Network error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if isTerminal {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "OpenRouter API error (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	hasOutput := false
	clearedStatus := false
	var fullResponseBuilder strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if strings.TrimSpace(data) == "[DONE]" {
				break
			}

			var streamResp StreamResponse
			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				continue
			}

			if streamResp.Error != nil {
				if isTerminal && !clearedStatus {
					fmt.Fprint(os.Stderr, "\r\033[K")
				}
				fmt.Fprintf(os.Stderr, "\nAPI Error: %s\n", streamResp.Error.Message)
				break
			}

			if len(streamResp.Choices) > 0 {
				chunk := streamResp.Choices[0].Delta.Content
				if chunk != "" {
					if isTerminal && !clearedStatus {
						// Erase the thinking indicator line before streaming content starts
						fmt.Fprint(os.Stderr, "\r\033[K")
						clearedStatus = true
					}
					fmt.Print(chunk)
					fullResponseBuilder.WriteString(chunk)
					hasOutput = true
				}
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "\nStream read error: %v\n", err)
	}

	if hasOutput {
		fmt.Println()
	}

	if parsed.UseContext && currentSession != nil && hasOutput {
		currentSession.Messages = append(currentSession.Messages,
			Message{Role: "user", Content: userPrompt},
			Message{Role: "assistant", Content: fullResponseBuilder.String()},
		)
		_ = saveSession(currentSession)
	}
}
