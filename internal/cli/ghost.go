package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var (
	// ErrSuggestionIncomplete indicates a truncated or unfinished command suggestion.
	ErrSuggestionIncomplete = errors.New("suggestion incomplete or truncated")
	// ErrSuggestionInvalid indicates a command suggestion with unbalanced or broken syntax.
	ErrSuggestionInvalid = errors.New("suggestion has invalid syntax or unbalanced quotes/brackets")
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
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
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

	sysCtx := GetActiveSystemContext()
	elevated := isProcessElevated()
	envPrompt := sysCtx.FormatPromptContext(elevated)

	sysPrompt := fmt.Sprintf(`You are a high-speed shell command autocompletion engine for %s running on %s.
%s
The user is currently typing in their terminal prompt.
Current prompt input: %q

Instructions:
1. Predict the full intended shell command or script block based on what the user has typed so far.
2. Return ONLY the complete, executable command or script block (maximum 5 lines).
3. Do NOT wrap output in markdown fences or quotes.
4. Do NOT include explanations, commentary, or multiple options.
5. If the input is already a complete command, return it as is.`, sysCtx.Shell, sysCtx.OSName, envPrompt, inputLine)

	userPrompt := fmt.Sprintf("Complete this %s command: %s", sysCtx.Shell, inputLine)

	reqBody := ghostRequest{
		Model: cfg.Model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1,
		MaxTokens:   256,
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

	choice := parsed.Choices[0]
	if choice.FinishReason == "length" {
		return "", fmt.Errorf("%w: API response truncated at max token limit", ErrSuggestionIncomplete)
	}

	raw := choice.Message.Content
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
			trimmedLine := strings.TrimSpace(l)
			if strings.HasPrefix(trimmedLine, "```") {
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

	// Normalize CRLF to LF
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.TrimSpace(output)

	// Strip single backticks or quotes if they wrap the entire snippet
	if (strings.HasPrefix(output, "`") && strings.HasSuffix(output, "`")) ||
		(strings.HasPrefix(output, "\"") && strings.HasSuffix(output, "\"")) ||
		(strings.HasPrefix(output, "'") && strings.HasSuffix(output, "'")) {
		if len(output) >= 2 {
			output = output[1 : len(output)-1]
		}
	}

	// If input starts with a comment, strip comment marker from output
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

// ValidateCommandCompleteness verifies that a suggested command is structurally complete,
// does not exceed the maximum allowed line count (5 lines), has balanced quotes/brackets,
// does not end in a dangling pipe or continuation operator, and does not contain cross-OS foreign commands.
func ValidateCommandCompleteness(inputLine, candidate string) error {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return fmt.Errorf("%w: empty candidate", ErrSuggestionIncomplete)
	}

	// Enforce maximum 5 lines
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 5 {
		return fmt.Errorf("%w: exceeds maximum 5 lines (got %d)", ErrSuggestionIncomplete, len(lines))
	}

	// Reject if candidate is identical to trimmed input
	if trimmed == strings.TrimSpace(inputLine) {
		return fmt.Errorf("%w: candidate identical to input", ErrSuggestionIncomplete)
	}

	// Trailing dangling operators check (incomplete statements)
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	trailingDangling := []string{"|", "&&", "||", "\\", ","}
	for _, op := range trailingDangling {
		if strings.HasSuffix(lastLine, op) {
			return fmt.Errorf("%w: ends with incomplete operator %q", ErrSuggestionIncomplete, op)
		}
	}

	// Cross-OS foreign artifact checks
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "sudo ") || strings.HasPrefix(lower, "export ") || strings.Contains(lower, "apt install") {
			return fmt.Errorf("%w: unix-specific command proposed on windows", ErrSuggestionInvalid)
		}
	} else {
		// Unix: reject Windows drive letters (e.g. C:\ or C:/)
		if len(trimmed) >= 3 && trimmed[1] == ':' && (trimmed[2] == '\\' || trimmed[2] == '/') {
			return fmt.Errorf("%w: windows drive path proposed on unix", ErrSuggestionInvalid)
		}
	}

	// Quote and bracket balance checking
	runes := []rune(trimmed)
	var (
		inSingleQuote bool
		inDoubleQuote bool
		inBacktick    bool
		bracketStack  []rune
		escaped       bool
	)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if escaped {
			escaped = false
			continue
		}

		if r == '\\' && !inSingleQuote {
			escaped = true
			continue
		}

		if inSingleQuote {
			if r == '\'' {
				inSingleQuote = false
			}
			continue
		}

		if inDoubleQuote {
			if r == '"' {
				inDoubleQuote = false
			}
			continue
		}

		if inBacktick {
			if r == '`' {
				inBacktick = false
			}
			continue
		}

		switch r {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '`':
			inBacktick = true
		case '(', '{', '[':
			bracketStack = append(bracketStack, r)
		case ')':
			if len(bracketStack) == 0 || bracketStack[len(bracketStack)-1] != '(' {
				return fmt.Errorf("%w: unmatched closing parenthesis ')'", ErrSuggestionInvalid)
			}
			bracketStack = bracketStack[:len(bracketStack)-1]
		case '}':
			if len(bracketStack) == 0 || bracketStack[len(bracketStack)-1] != '{' {
				return fmt.Errorf("%w: unmatched closing brace '}'", ErrSuggestionInvalid)
			}
			bracketStack = bracketStack[:len(bracketStack)-1]
		case ']':
			if len(bracketStack) == 0 || bracketStack[len(bracketStack)-1] != '[' {
				return fmt.Errorf("%w: unmatched closing bracket ']'", ErrSuggestionInvalid)
			}
			bracketStack = bracketStack[:len(bracketStack)-1]
		}
	}

	if inSingleQuote {
		return fmt.Errorf("%w: unclosed single quote", ErrSuggestionIncomplete)
	}
	if inDoubleQuote {
		return fmt.Errorf("%w: unclosed double quote", ErrSuggestionIncomplete)
	}
	if inBacktick {
		return fmt.Errorf("%w: unclosed backtick", ErrSuggestionIncomplete)
	}
	if len(bracketStack) > 0 {
		return fmt.Errorf("%w: unclosed bracket or parenthesis %q", ErrSuggestionIncomplete, string(bracketStack[len(bracketStack)-1]))
	}

	return nil
}

// HandleGhostCompleteCLI executes headless autocomplete query and writes the clean command to stdout.
// Exit codes:
//   0: Valid, complete single or multiline command printed to stdout.
//   2: Suggestion rejected (incomplete, syntax error, length cutoff, or > 5 lines).
//   1: Network, API key, or general runtime failure.
func HandleGhostCompleteCLI(cfg *Config, inputLine string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completed, err := QueryGhostCompletion(ctx, cfg, inputLine)
	if err != nil {
		if errors.Is(err, ErrSuggestionIncomplete) || errors.Is(err, ErrSuggestionInvalid) {
			os.Exit(2)
		}
		os.Exit(1)
	}

	if err := ValidateCommandCompleteness(inputLine, completed); err != nil {
		os.Exit(2)
	}

	// Write raw completed command directly to stdout for shell capture
	_, _ = os.Stdout.WriteString(completed)
	os.Exit(0)
}
