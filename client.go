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
	systemPrompt  = "You are a concise, helpful terminal CLI assistant. Output strictly clean, human-readable PLAIN TEXT designed for standard terminal displays. DO NOT use markdown formatting (no # headers, no **bold**, no triple backticks ``` for code fences). When explaining concepts, you may use clear uppercase section titles, ASCII underlines (--- or ===), simple indentation, and hyphen (-) bullet points. When providing or editing code, scripts, or configurations, DO NOT add artificial title banners or ASCII underlines—output the code cleanly."
	codePrompt    = "You are a direct code generation engine. You must output ONLY valid, executable, raw code, script, or configuration text. DO NOT include any conversational introduction, English commentary, concluding notes, markdown headers (#), ASCII section titles, or markdown backtick code blocks (```). All comments must be native code comments (e.g. # or //) within the script itself. Start immediately with the first character of code or shebang and end with the last line of code."
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
	IsCodeGen        bool
	IsStdoutTerminal bool
	IsStderrTerminal bool
	OnToken          func(token string)
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

func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = strings.TrimSpace(s[idx+1:])
		}
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	}
	return s
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

	if opts.IsStderrTerminal && opts.OnToken == nil {
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
			if opts.IsStderrTerminal && opts.OnToken == nil {
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
			if opts.IsStderrTerminal && opts.OnToken == nil {
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
		if opts.IsStderrTerminal && opts.OnToken == nil {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if opts.IsStderrTerminal && opts.OnToken == nil {
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

	// Fence stripping state for codegen mode
	headerChecked := false
	var headerBuf strings.Builder

	writeChunk := func(chunk string) {
		if chunk == "" {
			return
		}
		if opts.OnToken != nil {
			opts.OnToken(chunk)
		} else if targetFile != nil {
			_, _ = targetFile.WriteString(chunk)
		} else {
			if !hasOutput && !opts.IsCodeGen {
				printSplitter(cfg.Model, opts.IsStdoutTerminal)
			}
			fmt.Print(chunk)
		}
		fullResponseBuilder.WriteString(chunk)
		hasOutput = true
	}

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
				if opts.IsStderrTerminal && !clearedStatus && opts.OnToken == nil {
					fmt.Fprint(os.Stderr, "\r\033[K")
					clearedStatus = true
				}
				return fullResponseBuilder.String(), fmt.Errorf("API error: %s", streamResp.Error.Message)
			}

			if len(streamResp.Choices) > 0 {
				chunk := streamResp.Choices[0].Delta.Content
				if chunk != "" {
					if opts.IsStderrTerminal && !clearedStatus && opts.OnToken == nil {
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

					if opts.IsCodeGen && !headerChecked {
						headerBuf.WriteString(chunk)
						text := headerBuf.String()
						if strings.Contains(text, "\n") {
							headerChecked = true
							// Check if the first line is a code fence like ```python or ```
							lines := strings.SplitN(text, "\n", 2)
							firstLine := strings.TrimSpace(lines[0])
							remainder := lines[1]
							if strings.HasPrefix(firstLine, "```") {
								// Drop the opening code fence
								writeChunk(remainder)
							} else {
								// First line is code, write entire buffer
								writeChunk(text)
							}
						}
					} else {
						writeChunk(chunk)
					}
				}
			}
		}
	}

	// Flush any remaining header buffer if no newline was ever seen
	if opts.IsCodeGen && !headerChecked && headerBuf.Len() > 0 {
		text := headerBuf.String()
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			text = stripMarkdownFences(text)
		}
		writeChunk(text)
	}

	if opts.IsStderrTerminal && !clearedStatus && opts.OnToken == nil {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fullResponseBuilder.String(), fmt.Errorf("stream read error: %w", err)
	}

	if hasOutput {
		if targetFile != nil {
			_, _ = targetFile.WriteString("\n")
			_ = targetFile.Sync()
			if opts.IsStderrTerminal && opts.OnToken == nil {
				fmt.Fprintf(os.Stderr, "[%s to %s]\n", fileActionDesc, targetPath)
			}
		} else if opts.OnToken == nil {
			fmt.Println()
			_ = os.Stdout.Sync()
			if !opts.IsStdoutTerminal && opts.IsStderrTerminal {
				fmt.Fprintf(os.Stderr, "[done]\n")
			}
		}
	}

	finalResponse := fullResponseBuilder.String()
	if opts.IsCodeGen {
		finalResponse = stripMarkdownFences(finalResponse)
	}

	return finalResponse, nil
}
