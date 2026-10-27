package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	openRouterURL = "https://openrouter.ai/api/v1/chat/completions"
	defaultModel  = "nvidia/nemotron-3.5-lightning:free"
	systemPrompt  = "You are a concise, helpful terminal CLI assistant. Output strictly clean, human-readable PLAIN TEXT designed for standard terminal displays. DO NOT use markdown formatting (no # headers, no **bold**, no triple backticks ``` for code fences). Instead, use clear uppercase section titles, ASCII underlines (--- or ===), simple indentation (2 to 4 spaces) for code or commands, and clean hyphen (-) bullet points. Provide direct, succinct answers without unnecessary pleasantries or verbose commentary."
)

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

func formatSplitter(model string) string {
	prefix := fmt.Sprintf("─── ask [%s] ", model)
	totalWidth := 60
	rem := totalWidth - len(prefix)
	if rem < 5 {
		rem = 5
	}
	return prefix + strings.Repeat("─", rem)
}

func printSplitter(model string, isStdoutTerminal bool) {
	if !isStdoutTerminal {
		return
	}
	fmt.Println(formatSplitter(model))
}

func isCharDevice(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func queryOpenRouter(ctx context.Context, cfg *Config, messages []Message, isStdoutTerminal bool, isStderrTerminal bool) (string, error) {
	reqBody := ChatRequest{
		Model:    cfg.Model,
		Messages: messages,
		Stream:   true,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openRouterURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error creating HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/mugunth13labs/cli-ask")
	req.Header.Set("X-Title", "ask-cli")

	if isStderrTerminal {
		fmt.Fprintf(os.Stderr, "[%s thinking...]", cfg.Model)
	}

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if isStderrTerminal {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if isStderrTerminal {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenRouter API error (HTTP %d): %s", resp.StatusCode, string(body))
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
				if isStderrTerminal && !clearedStatus {
					fmt.Fprint(os.Stderr, "\r\033[K")
					clearedStatus = true
				}
				return fullResponseBuilder.String(), fmt.Errorf("API error: %s", streamResp.Error.Message)
			}

			if len(streamResp.Choices) > 0 {
				chunk := streamResp.Choices[0].Delta.Content
				if chunk != "" {
					if isStderrTerminal && !clearedStatus {
						// Erase the thinking indicator line before streaming content starts
						fmt.Fprint(os.Stderr, "\r\033[K")
						clearedStatus = true
					}
					if !hasOutput {
						printSplitter(cfg.Model, isStdoutTerminal)
					}
					fmt.Print(chunk)
					fullResponseBuilder.WriteString(chunk)
					hasOutput = true
				}
			}
		}
	}

	if isStderrTerminal && !clearedStatus {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fullResponseBuilder.String(), fmt.Errorf("stream read error: %w", err)
	}

	if hasOutput {
		fmt.Println()
	}

	return fullResponseBuilder.String(), nil
}
