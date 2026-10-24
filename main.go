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
	"strings"
	"time"
)

const (
	version       = "0.2.0"
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
		return s, nil // no session file yet
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

// compactHistory performs token-aware auto-compaction
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

	// Split into older messages to compact and recent messages to keep verbatim
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
		// Truncate long past responses to 200 characters each in summary
		content := strings.TrimSpace(m.Content)
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		// Single-line format
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

func printHelp() {
	fmt.Printf(`ask v%s — Lightning-fast terminal AI query tool with session context

Usage:
  ask <prompt>                  Ask any question (remembers terminal session)
  ask "your prompt here"        Ask with quotes
  echo "code" | ask "explain"   Pipe stdin to ask

Session Memory:
  ask --clear, -c               Clear current terminal's conversation history
  ask --no-context <prompt>     Run query without reading or updating session history

Configuration:
  ask --set-key <key>           Save OpenRouter API key
  ask --set-model <model>       Set default model (default: %s)
  ask --config                  Show current configuration file and settings
  ask --version, -v             Show version
  ask --help, -h                Show this help message

Environment:
  OPENROUTER_API_KEY            Overrides configured API key
`, version, defaultModel)
}

func main() {
	if len(os.Args) < 2 {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			printHelp()
			return
		}
	}

	firstArg := ""
	if len(os.Args) > 1 {
		firstArg = os.Args[1]
	}

	switch firstArg {
	case "-v", "--version", "version":
		fmt.Printf("ask v%s\n", version)
		return
	case "-h", "--help", "help":
		printHelp()
		return
	case "-c", "--clear":
		sessionID := getSessionID()
		if err := clearSession(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "Error clearing session: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Session context cleared (%s).\n", sessionID)
		return
	case "--config":
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
	case "--set-key":
		if len(os.Args) < 3 {
			fmt.Println("Error: Missing key argument. Usage: ask --set-key <YOUR_API_KEY>")
			os.Exit(1)
		}
		cfg, _ := loadConfig()
		cfg.APIKey = os.Args[2]
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("API key saved successfully.")
		return
	case "--set-model":
		if len(os.Args) < 3 {
			fmt.Println("Error: Missing model argument. Usage: ask --set-model <MODEL_NAME>")
			os.Exit(1)
		}
		cfg, _ := loadConfig()
		cfg.Model = os.Args[2]
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Default model set to: %s\n", cfg.Model)
		return
	}

	// Parse arguments, check for --no-context
	useContext := true
	var promptArgs []string
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--no-context" {
			useContext = false
		} else {
			promptArgs = append(promptArgs, arg)
		}
	}

	userPrompt := strings.Join(promptArgs, " ")

	// Read piped input if available
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		stdinBytes, err := io.ReadAll(os.Stdin)
		if err == nil && len(stdinBytes) > 0 {
			stdinText := strings.TrimSpace(string(stdinBytes))
			if userPrompt != "" {
				userPrompt = userPrompt + "\n\nContext:\n" + stdinText
			} else {
				userPrompt = stdinText
			}
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
	if useContext {
		currentSession, _ = loadSession(sessionID)
	}

	// Prepare messages array
	var apiMessages []Message
	apiMessages = append(apiMessages, Message{Role: "system", Content: systemPrompt})

	if useContext && currentSession != nil && len(currentSession.Messages) > 0 {
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

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Network error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "OpenRouter API error (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	hasOutput := false
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
				fmt.Fprintf(os.Stderr, "\nAPI Error: %s\n", streamResp.Error.Message)
				break
			}

			if len(streamResp.Choices) > 0 {
				chunk := streamResp.Choices[0].Delta.Content
				if chunk != "" {
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

	// Save to session history if enabled
	if useContext && currentSession != nil && hasOutput {
		currentSession.Messages = append(currentSession.Messages,
			Message{Role: "user", Content: userPrompt},
			Message{Role: "assistant", Content: fullResponseBuilder.String()},
		)
		_ = saveSession(currentSession)
	}
}
