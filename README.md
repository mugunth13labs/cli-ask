# ask

**The lean, zero-dependency AI companion for your command line.**

`ask` brings AI intelligence straight to your terminal. It starts instantly, streams answers in real time, and follows the Unix philosophy: do one thing, do it fast, and stay out of your way.

```bash
# Quick command lookups
$ ask tar command to extract without verbose
tar -xf archive.tar.gz

# Pipe logs or diffs directly into queries
$ git diff | ask "summarize these changes"
$ docker logs web-api --tail 50 | ask "what caused this 500 error?"

# Generate runnable code straight to a file
$ ask -o setup.sh "write a bash script to install and configure nginx"

# Open interactive chat
$ ask -i
```

---

## Highlights

- **Lightning Fast**: Single static binary written in pure Go. Starts in under 10ms with zero runtime dependencies.
- **Inline Multi-Line Composer**: Run `ask` with no arguments to type or paste multi-line prompts and scripts directly in your shell.
- **File References (`@filename`)**: Attach local files effortlessly. Typing `@` opens an interactive recursive search dropdown with `Up`/`Down` navigation and `Tab` completion. Missing files are detected instantly.
- **Interactive Chat (`ask -i`)**: Continuous back-and-forth dialogue with session memory, mouse text selection, and on-the-fly model switching.
- **High-Contrast Design**: Crisp Deep Midnight Navy message boxes with bright white text, electric cyan borders, and amber tags for all terminal themes.
- **Pure Code Generation (`--cg`, `-o`, `-a`)**: Generates raw, executable code without markdown code fences or conversational filler—ready to run or pipe.
- **Shell-Scoped Context**: Remembers conversation history within your active terminal tab without leaking across unrelated windows or tmux panes.
- **Unix Piping**: Seamlessly inspect piped output from `git`, `docker`, `journalctl`, `curl`, and other standard tools.
- **Free Models by Default**: Powered by OpenRouter, pre-configured with high-performance free models.
- **Self-Updating**: Keep up to date with a single command: `ask --update-self`.

---

## Installation

### Linux & macOS
```bash
curl -fsSL https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.sh | sh
```

### Windows (PowerShell)
```powershell
irm https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.ps1 | iex
```

### Manual Download
Pre-compiled standalone binaries are available on the [Releases](https://github.com/mugunth13labs/cli-ask/releases) page for Linux, macOS (Apple Silicon & Intel), and Windows.

---

## Quick Start

1. Get a free API key from [OpenRouter](https://openrouter.ai/settings/keys).
2. Save your key:
```bash
ask --set-key sk-or-v1-xxxxxxxx
```
*(Or set `export OPENROUTER_API_KEY="sk-or-v1-..."` in your shell).*

That's it! You're ready to use `ask`.

---

## Usage

### 1. One-Shot Queries
Ask questions with or without quotes:
```bash
ask "how to find files larger than 100MB in linux"
ask what does the -F flag do in awk
```

### 2. Multi-Line Composer
Run `ask` by itself to write or paste multi-line prompts, code snippets, or error traces:
```bash
$ ask
```
```text
─ ask prompt [ Ctrl+S: Send | Ctrl+C: Cancel ] ─────────────────────────
  Write a Python script that monitors disk usage
  and alerts via webhook when / exceeds 90%.
────────────────────────────────────────────────────────────────────────────
```
- Press `Enter` for a new line.
- Press `Ctrl+S` (or `Ctrl+Enter`) to submit.
- Press `Ctrl+C` to cancel.

### 3. File References & Live Autocomplete (`@filename`)
Seamlessly include code or config files in your prompts across one-shot queries, the composer, and interactive chat:
```bash
# Attach files in one-shot prompts
ask "explain the architecture in @main.go and @internal/cli/editor.go"

# Check code for bugs
ask --cg "refactor @src/utils.py to use async/await"
```
- **Live Search Dropdown**: In `ask` (empty composer) or `ask -i` (chat), typing `@` opens a live file search popup that recursively scans your workspace:
  - `Up` / `Down` arrows to navigate files.
  - `Tab` or `Enter` to autocomplete `@path/to/file `.
  - `Esc` to dismiss.
- **Missing File Protection**: If any referenced file does not exist, `ask` halts before making an API call and reports the missing file with exit code 1.
- **Smart 1MB Safety**: Files up to 1MB are transmitted in full. Files exceeding 1MB (up to 10MB) are truncated with a clear notice; files >10MB are rejected.
- **Lightweight History**: Only the clean prompt with `@filename` (or `@filename [truncated]`) is recorded in conversation history—never megabytes of repeated file dumps.

### 4. Interactive Chat (`ask -i`)
Launch continuous chat mode for deep technical discussions or iterative problem-solving:
```bash
$ ask -i
```
- **Session Memory**: Remembers context across turns.
- **Mouse Selection**: Click and drag to select text in chat history, then press `y`, `Enter`, or `Ctrl+Shift+C` to copy to clipboard.
- **Keyboard Shortcuts**:
  - `Ctrl+S` (or `Ctrl+Enter`) — Send prompt
  - `Enter` — Insert newline in prompt
  - `Up` / `Down` — Navigate history & prompt
  - `Shift+Arrows` — Highlight text in history
  - `y`, `Enter`, or `Ctrl+Shift+C` — Copy selection to clipboard
- **In-Chat Commands**:
  - `/model <name>` — Switch AI model (e.g. `/model anthropic/claude-3.5-sonnet`)
  - `/clear` — Reset conversation history
  - `/help` — View commands
  - `/exit` or `Ctrl+C` — Exit back to shell

### 5. Code Generation
Generate raw, executable scripts or configurations:
```bash
# Generate pure code to stdout
ask --cg "write a python function to compute fibonacci numbers"

# Save directly to a file
ask -o deploy.sh "create a bash deployment script with error handling"

# Append configuration to an existing file
ask -a docker-compose.yml "add redis with persistent storage"
```
*(Code generation mode activates automatically when outputting to code file extensions).*

### 6. Pipes & Logs
Feed command outputs directly into `ask`:
```bash
# Debug system logs
journalctl -u postgresql -n 50 --no-pager | ask "why did the service fail to start?"

# Review git changes
git diff HEAD~1 | ask "identify any security risks in this diff"

# Analyze network status
ss -tulpn | ask "which ports are exposed to the public internet?"
```

### 7. AI Shell Autocompletion (`Alt+RightArrow`)
Complete commands in-place right at your terminal prompt:
```bash
# Enable shell completion hook
ask --toggle-ghost
```
- Type any partial command or prompt (e.g. `get-proc` or `# find large files`).
- Press **`Alt+RightArrow`** to instantly complete the line with the AI recommendation.
- Press **`Enter`** once to execute.
- Supported shells: PowerShell, Bash, Zsh, Fish.

### 8. Changing Models
Check your settings or switch default models anytime:
```bash
# View current configuration
ask --config

# Switch default model
ask --set-model google/gemini-2.0-flash-exp:free
ask --set-model deepseek/deepseek-r1:free
ask --set-model anthropic/claude-3.5-sonnet
```

### 8. Session Management
```bash
# Clear session memory for current terminal tab
ask --clear

# Run a one-off query without reading or recording history
ask --no-context "what is the default ssh port?"
```

---

## Command Reference

| Command / Flag | Description |
| :--- | :--- |
| `ask <prompt>` | Run a one-shot query |
| `ask` | Open the inline multi-line prompt composer |
| `ask -i`, `ask --chatbot` | Launch interactive chat mode |
| `ask --cg`, `--codegen` | Force pure code output (no fences or commentary) |
| `ask -o <file>` | Save response directly to file (overwrites) |
| `ask -a <file>` | Append response directly to file |
| `ask --no-context` | Run query without session history |
| `ask --clear`, `ask -c` | Clear session context for current shell |
| `ask --set-key <key>` | Save OpenRouter API key |
| `ask --set-model <model>` | Set default AI model |
| `ask --config` | Display current configuration |
| `ask --update-self` | Update `ask` binary to the latest release |

---

## License

[MIT](LICENSE) © Mugunthan
