package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type ghostRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	Reasoning   *struct {
		Effort string `json:"effort,omitempty"`
	} `json:"reasoning,omitempty"`
}

type ghostResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// DetectActiveShell inspects environment variables and parent process hierarchy
// to accurately determine the active shell (powershell, bash, zsh, fish).
func DetectActiveShell() string {
	if runtime.GOOS == "windows" {
		if os.Getenv("PSModulePath") != "" {
			return "powershell"
		}
		ppid := os.Getppid()
		out, err := exec.Command("powershell", "-NoProfile", "-Command",
			fmt.Sprintf("(Get-Process -Id %d -ErrorAction SilentlyContinue).ProcessName", ppid)).Output()
		if err == nil {
			pname := strings.ToLower(strings.TrimSpace(string(out)))
			if strings.Contains(pname, "powershell") || strings.Contains(pname, "pwsh") {
				return "powershell"
			}
			if strings.Contains(pname, "cmd") {
				return "cmd"
			}
		}
		return "powershell"
	}

	ppid := os.Getppid()
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", ppid)
	if data, err := os.ReadFile(cmdlinePath); err == nil {
		fields := strings.Split(string(data), "\x00")
		if len(fields) > 0 && fields[0] != "" {
			base := strings.ToLower(fields[0])
			if idx := strings.LastIndex(base, "/"); idx != -1 {
				base = base[idx+1:]
			}
			base = strings.TrimPrefix(base, "-")
			switch base {
			case "bash", "zsh", "fish":
				return base
			}
		}
	}

	if shell := os.Getenv("SHELL"); shell != "" {
		lower := strings.ToLower(shell)
		switch {
		case strings.Contains(lower, "zsh"):
			return "zsh"
		case strings.Contains(lower, "fish"):
			return "fish"
		case strings.Contains(lower, "bash"):
			return "bash"
		}
	}

	return "bash"
}

// QueryGhostCompletion requests a single high-speed command completion from OpenRouter.
func QueryGhostCompletion(ctx context.Context, cfg *Config, inputLine string) (string, error) {
	trimmed := strings.TrimSpace(inputLine)
	if trimmed == "" {
		return "", fmt.Errorf("empty input")
	}

	if cfg == nil || cfg.APIKey == "" {
		return "", fmt.Errorf("missing api key")
	}

	shell := DetectActiveShell()
	osName := runtime.GOOS

	sysPrompt := fmt.Sprintf(`You are a high-speed shell command autocompletion engine for %s running on %s.
The user is currently typing a command in their terminal prompt.
Current prompt input: %q

Instructions:
1. Predict the full intended shell command based on what the user has typed so far.
2. Return ONLY the single complete, executable command line.
3. Do NOT wrap output in markdown fences or quotes.
4. Do NOT include explanations, commentary, or multiple options.
5. If the input is already a complete command, return it as is.`, shell, osName, inputLine)

	userPrompt := fmt.Sprintf("Complete this %s command: %s", shell, inputLine)

	reqBody := ghostRequest{
		Model: cfg.Model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1,
		MaxTokens:   128,
		Reasoning: &struct {
			Effort string `json:"effort,omitempty"`
		}{Effort: "none"},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/mugunth13labs/cli-ask")
	httpReq.Header.Set("X-Title", "ask-autocomplete")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("query openrouter: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api status: %d", resp.StatusCode)
	}

	respBodyBytes, _ := io.ReadAll(resp.Body)
	var parsed ghostResponse
	if err := json.Unmarshal(respBodyBytes, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w (body: %s)", err, string(respBodyBytes))
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices returned (body: %s)", string(respBodyBytes))
	}

	raw := parsed.Choices[0].Message.Content
	cleaned := CleanGhostSuggestion(inputLine, raw)
	if cleaned == "" {
		return "", fmt.Errorf("empty suggestion after cleaning (raw: %q, body: %s)", raw, string(respBodyBytes))
	}

	return cleaned, nil
}

// CleanGhostSuggestion strips formatting, markdown, and glue artifacts from raw AI output.
func CleanGhostSuggestion(inputLine, rawOutput string) string {
	output := strings.TrimSpace(rawOutput)
	if output == "" {
		return ""
	}

	// Strip markdown code fences
	if strings.HasPrefix(output, "```") {
		lines := strings.Split(output, "\n")
		var codeLines []string
		inBlock := false
		for _, l := range lines {
			if strings.HasPrefix(l, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock {
				codeLines = append(codeLines, l)
			}
		}
		if len(codeLines) > 0 {
			output = strings.TrimSpace(strings.Join(codeLines, "\n"))
		} else {
			output = strings.Trim(output, "`")
		}
	}

	// Strip single backticks or quotes
	output = strings.Trim(output, "`")
	output = strings.Trim(output, "\"")
	output = strings.Trim(output, "'")

	// If multi-line, take only the first non-empty line
	if idx := strings.Index(output, "\n"); idx != -1 {
		output = strings.TrimSpace(output[:idx])
	}

	// If input starts with a comment or prompt prefix, strip it
	trimmedInput := strings.TrimSpace(inputLine)
	if strings.HasPrefix(trimmedInput, "#") {
		output = strings.TrimPrefix(output, "#")
		output = strings.TrimSpace(output)
	}

	// Case-insensitive direct prefix replacement (e.g. get-proc -> Get-Process)
	if len(output) >= len(trimmedInput) {
		if strings.EqualFold(output[:len(trimmedInput)], trimmedInput) {
			return output
		}
	}

	// Sub-word hyphenated prefix completion (e.g. Format-L -> Format-List)
	if idx := strings.LastIndex(trimmedInput, "-"); idx != -1 {
		prefixPart := trimmedInput[:idx+1]
		if strings.HasPrefix(output, prefixPart) {
			return output
		}
	}

	// Sub-word boundary overlap check
	for i := len(trimmedInput) - 1; i >= 2; i-- {
		suffix := trimmedInput[i:]
		if strings.HasPrefix(output, suffix) {
			return trimmedInput[:i] + output
		}
	}

	return output
}

// HandleGhostCompleteCLI executes headless autocomplete query and writes the clean command to stdout.
func HandleGhostCompleteCLI(cfg *Config, inputLine string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completed, err := QueryGhostCompletion(ctx, cfg, inputLine)
	if err != nil || completed == "" {
		os.Exit(1)
	}

	// Write raw completed command directly to stdout for shell capture
	_, _ = os.Stdout.WriteString(completed)
	os.Exit(0)
}
