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
	version       = "0.1.0"
	openRouterURL = "https://openrouter.ai/api/v1/chat/completions"
	defaultModel  = "nvidia/nemotron-3.5-lightning:free"
	systemPrompt  = "You are a concise, helpful terminal CLI assistant. Provide direct, succinct answers without unnecessary fluff, pleasantries, or verbose commentary unless explicitly asked. Format code or commands clearly."
)

type Config struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

func getConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ask", "config.json"), nil
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		Model: defaultModel,
	}

	// Environment variable takes precedence for API Key
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
	fmt.Printf(`ask v%s — Lightning-fast terminal AI query tool powered by OpenRouter

Usage:
  ask <prompt>                  Ask any question
  ask "your prompt here"        Ask with quotes
  echo "code" | ask "explain"   Pipe stdin to ask

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
		// Check if data is piped via stdin
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

	// Read prompt from arguments
	var promptParts []string
	if len(os.Args) > 1 {
		promptParts = os.Args[1:]
	}
	userPrompt := strings.Join(promptParts, " ")

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

	reqBody := ChatRequest{
		Model: cfg.Model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream: true,
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
}
