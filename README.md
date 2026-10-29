# ask — The Lean Terminal AI

> *"Fast, focused, and stays out of your way."*

`ask` is a lightweight, zero-dependency command-line AI companion built in pure Go. It delivers instant, high-quality answers directly in your shell—without heavy runtimes, sluggish full-screen TUIs, or bloated frameworks.

```bash
# 1. Quick command lookups
$ ask "tar command to extract without verbose"
tar -xf archive.tar.gz

# 2. Natural unquoted questions
$ ask how to kill process by port in linux
fuser -k <port>/tcp
# or
lsof -ti :<port> | xargs kill -9

# 3. Unix piping for logs and diffs
$ git diff | ask "review this diff for potential bugs"
$ journalctl -u nginx --no-pager -n 50 | ask "why is nginx failing?"

# 4. Pure code generation directly to file
$ ask -o backup.sh "write a bash script to rsync /var/data to remote storage"
```

---

## Why `ask`?

Most AI command-line tools make a fundamental mistake: **they get in your way.**

They trap you inside sluggish full-screen interfaces, demand Python/Node dependencies, start background agent loops you didn't ask for, or leak conversation context between unrelated terminal windows.

`ask` adheres strictly to the **Unix Philosophy**:

- ⚡ **Pure Go & Zero Dependencies**: Single static binary (~10MB), instant startup (<10ms). No Python venvs, no npm packages, no Docker daemon required.
- 📝 **Inline Micro-Editor**: Running `ask` with no prompt opens a clean, framed multi-line composer right inside your terminal. It expands up to a 5-line sliding viewport so it never covers your screen or traps your cursor. Press `Ctrl+Enter` to send, and your prompt drops cleanly into your terminal scrollback history.
- 💬 **Interactive Chat (`ask -i`)**: Continuous conversation mode with persistent session context, live model validation (`/model <name>`), mouse text selection, visual copy hints (`y`/`Enter`/`Ctrl+Shift+C`), and clean terminal restoration on exit.
- 💻 **Pure Code Generation (`--cg`, `--codegen`)**: Outputs raw, executable code starting directly with shebangs or declarations—zero markdown code fences, zero conversational fluff. Automatically activates when saving to `.sh`, `.py`, `.yaml`, etc.
- 📁 **Direct File Integration (`-o`, `-a`)**: Write or append AI responses directly to files with clean live progress reporting on stderr (`[streaming to output...] -> [done]`).
- 🧠 **Shell-Scoped Session Memory**: Context is strictly scoped to your active shell process (PPID). Two terminal tabs or tmux panes never bleed context into each other. Long sessions auto-compact without blowing out context windows.
- 🆓 **Free Tier by Default**: Pre-configured with top-performing free models on OpenRouter (e.g. `nvidia/nemotron-3.5-lightning:free`). Switch models anytime with `ask --set-model` or in-chat `/model`.
- 🔄 **Built-In Self-Updater**: Stay on the latest release with one command: `ask --update-self`.

---

## Installation

### Linux & macOS (One-line install)
```bash
curl -fsSL https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.sh | sh
```

### Windows (PowerShell one-line install)
```powershell
irm https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.ps1 | iex
```

### Updating `ask`
To update an existing installation to the latest version:
```bash
ask --update-self
```
*(On Linux/macOS, use `sudo ask --update-self` if installed in `/usr/local/bin`).*

### Manual Binary Download
Pre-compiled standalone binaries are available on the [Releases](https://github.com/mugunth13labs/cli-ask/releases) page:
- **Linux**: `ask-linux-amd64`, `ask-linux-arm64`
- **macOS**: `ask-darwin-arm64` (Apple Silicon), `ask-darwin-amd64` (Intel)
- **Windows**: `ask-windows-amd64.exe`

---

## Quick Setup

1. Get a free API key at [OpenRouter](https://openrouter.ai/settings/keys).
2. Save your key locally:
```bash
ask --set-key sk-or-v1-xxxxxxxx
```
*(Alternatively, set `export OPENROUTER_API_KEY="sk-or-v1-..."` in your shell environment).*

---

## Usage Guide

### 1. Direct Queries
Pass single-line queries with or without quotes:
```bash
ask "explain how to configure a systemd service"
ask what does the -F flag do in awk
```

### 2. Inline Multiline Composer
Run `ask` without prompt arguments to compose multi-line prompts directly in your terminal:
```bash
$ ask
```
```text
─ ask prompt [ Ctrl+Enter: Send | Ctrl+C: Cancel ] ─────────────────────────
  Write a Python script that monitors disk usage
  and alerts via webhook when / exceeds 90%.
────────────────────────────────────────────────────────────────────────────
```
- **Viewport Clamped**: Automatically restricted to 5 lines maximum with a sliding window so your existing terminal output remains visible.
- **Scroll Indicators**: Subtly indicates `[ ▲ N above ]` or `[ ▼ N more below ]` when lines exceed the visible viewport.
- **`Enter`**: Inserts a newline for multi-line prompts and code blocks.
- **`Ctrl+Enter`**: Sends the prompt.
- **`Ctrl+C`**: Cancels and closes the box cleanly.
- **Clean Submission**: When submitted, the box disappears and prints your full prompt directly into your terminal scrollback history.

### 3. Continuous Chatbot Mode (`ask -i`)
Launch interactive conversation mode when you need an ongoing back-and-forth dialogue:
```bash
$ ask -i
```
- **Top Status Bar**: Displays current mode and context-aware shortcuts.
- **Model Validation**: Switch models dynamically with `/model <name>`. Validates against OpenRouter models; if an invalid model is entered, it informs you and safely retains your previous model.
- **Mouse Selection & Quick Copy**: Click or drag with your mouse to highlight text in history. Press `y`, `Enter`, or `Ctrl+Shift+C` to copy the selected text straight to your system clipboard without prompt markers.
- **Smooth Navigation**: Scroll history with Mouse Wheel or `PageUp`/`PageDown`/`Up`/`Down`.
- **Clean Exit**: Press `Ctrl+C` or type `/exit` / `/quit` / `/q`. Restores your terminal buffer cleanly with zero mouse artifacts.

### 4. Pure Code Generation (`--cg`, `--codegen`)
When generating or refactoring scripts, configs, or code, use `--cg` to ensure the output contains strictly raw, executable code without conversational filler or markdown code fences:
```bash
# Explicit code generation
ask --cg "write a python function to compute fibonacci"

# Generate a script directly to a file (auto-activates codegen)
ask -o deploy.sh "create a bash deploy script with error trapping"

# Refactor an existing script via pipe
cat deploy.sh | ask "add slack notification on failure" > deploy_v2.sh
```
`ask` automatically enables codegen mode when saving to code extensions (`.sh`, `.py`, `.go`, `.rs`, `.yaml`, `.json`, `.sql`, etc.) or when piped input contains code.

### 5. Direct File Output (`-o`, `-a`)
Save or append responses directly to files:
```bash
# Overwrite or create file
ask -o docker-compose.yml "create a compose file for postgres and redis"

# Append to an existing notes file
ask -a docker-compose.yml "add healthchecks and volume mounts"
```

### 6. Standard Unix Piping
Feed command outputs, logs, or diffs directly into `ask`:
```bash
# Debug system logs
journalctl -xe --no-pager | tail -n 50 | ask "what caused this failure?"

# Review git diffs
git diff | ask "review this diff for potential performance regressions"

# Inspect active services
netstat -tulpn | ask "which ports are exposed publicly?"
```

### 7. Managing Models
Check configuration or switch your default AI model:
```bash
# View active configuration and model
ask --config

# Switch default model
ask --set-model google/gemini-2.0-flash-exp:free
ask --set-model deepseek/deepseek-r1:free
ask --set-model anthropic/claude-3.5-sonnet
```

### 8. Session Controls
```bash
# Clear session context for the current terminal tab
ask --clear   # or ask -c

# Run a one-off query without reading or recording session memory
ask --no-context "what is the default ssh port?"
```

---

## Keyboard Shortcuts Reference

| Context | Key / Command | Action |
| :--- | :--- | :--- |
| **Inline Composer** | `Ctrl+Enter` | Send multiline prompt |
| | `Enter` | Insert newline |
| | `Arrow Keys` | Move cursor across lines & columns |
| | `Backspace` | Character backspace (merges across lines at column 0) |
| | `Ctrl+C` | Cancel and exit composer |
| **Interactive Chat (`-i`)** | `Ctrl+Enter` | Send prompt turn |
| | `Enter` | Insert newline in prompt box |
| | `Mouse Wheel` | Scroll conversation history |
| | `Mouse Drag` | Select text in history |
| | `y` / `Enter` / `Ctrl+Shift+C` | Copy selected text to clipboard |
| | `/model <name>` | Switch active model on the fly |
| | `/clear` | Clear chat history & session context |
| | `/help` | Show command help |
| | `/exit` / `/quit` / `Ctrl+C` | Exit chat and restore terminal |

---

## Packaging & Debian

`cli-ask` complies with Debian Policy (§4.7.0) with zero external dependencies, standard roff manpage documentation (`man ask`), and full DFSG compliance. Packaging source files and ITP templates are located in [`debian/`](debian/).

---

## License

[MIT](LICENSE) © Mugunthan
