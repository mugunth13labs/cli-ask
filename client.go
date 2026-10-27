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

type QueryOptions struct {
	OutputFile       string
	AppendFile       string
	IsStdoutTerminal bool
	IsStderrTerminal bool
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

func queryOpenRouter(ctx context.Context, cfg *Config, messages []Message, opts QueryOptions) (string, error) {
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

	if opts.IsStderrTerminal {
		fmt.Fprintf(os.Stderr, "[%s thinking...]", cfg.Model)
	}

	// Prepare file target if specified
	var targetFile *os.File
	var targetPath string
	var fileActionDesc string

	if opts.OutputFile != "" {
		targetPath = opts.OutputFile
		fileActionDesc = "saved"
		f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			if opts.IsStderrTerminal {
				fmt.Fprint(os.Stderr, "\r\033[K")
			}
			return "", fmt.Errorf("failed to open output file: %w", err)
		}
		defer f.Close()
		targetFile = f
	} else if opts.AppendFile != "" {
		targetPath = opts.AppendFile
		fileActionDesc = "appended"
		f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			if opts.IsStderrTerminal {
				fmt.Fprint(os.Stderr, "\r\033[K")
			}
			return "", fmt.Errorf("failed to open append file: %w", err)
		}
		defer f.Close()
		targetFile = f
	}

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if opts.IsStderrTerminal {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if opts.IsStderrTerminal {
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
				if opts.IsStderrTerminal && !clearedStatus {
					fmt.Fprint(os.Stderr, "\r\033[K")
					clearedStatus = true
				}
				return fullResponseBuilder.String(), fmt.Errorf("API error: %s", streamResp.Error.Message)
			}

			if len(streamResp.Choices) > 0 {
				chunk := streamResp.Choices[0].Delta.Content
				if chunk != "" {
					if opts.IsStderrTerminal && !clearedStatus {
						if targetFile != nil {
							fmt.Fprintf(os.Stderr, "\r\033[K[%s saving to %s...]\n", cfg.Model, targetPath)
						} else if opts.IsStdoutTerminal {
							// Interactive terminal: clear status line so AI response begins cleanly
							fmt.Fprint(os.Stderr, "\r\033[K")
						} else {
							// Redirection / piping: keep user informed on stderr so screen is never blank
							fmt.Fprintf(os.Stderr, "\r\033[K[%s streaming to output...]\n", cfg.Model)
						}
						clearedStatus = true
					}

					if targetFile != nil {
						// Write chunk directly to file
						_, _ = targetFile.WriteString(chunk)
					} else {
						// Print chunk to stdout
						if !hasOutput {
							printSplitter(cfg.Model, opts.IsStdoutTerminal)
						}
						fmt.Print(chunk)
					}

					fullResponseBuilder.WriteString(chunk)
					hasOutput = true
				}
			}
		}
	}

	if opts.IsStderrTerminal && !clearedStatus {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fullResponseBuilder.String(), fmt.Errorf("stream read error: %w", err)
	}

	if hasOutput {
		if targetFile != nil {
			_, _ = targetFile.WriteString("\n")
			_ = targetFile.Sync()
			if opts.IsStderrTerminal {
				fmt.Fprintf(os.Stderr, "[%s to %s]\n", fileActionDesc, targetPath)
			}
		} else {
			fmt.Println()
			_ = os.Stdout.Sync()
			if !opts.IsStdoutTerminal && opts.IsStderrTerminal {
				fmt.Fprintf(os.Stderr, "[done]\n")
			}
		}
	}

	return fullResponseBuilder.String(), nil
}
