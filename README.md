# ask

**The lean, zero-dependency AI companion for your command line.**

`ask` brings AI intelligence straight to your terminal. It starts instantly, streams answers in real time, and follows the Unix philosophy: do one thing, do it fast, and stay out of your way.

```bash
# Quick command lookups
$ ask tar command to extract without verbose
tar -xf archive.tar.gz

# Ghost-Complete: Complete commands or plain-English intent right at your prompt
$ get-proc [Alt+→]
$ Get-Process | Where-Object { $_.CPU -gt 20 } | Sort-Object CPU -Descending

$ # find files over 100MB and sort by size [Alt+→]
$ find . -type f -size +100M -exec ls -lh {} + | sort -k 5 -hr

# Pipe logs or diffs directly into queries
$ git diff | ask "summarize these changes"
$ docker logs web-api --tail 50 | ask "what caused this 500 error?"

# Generate runnable code straight to a file
$ ask -o setup.sh "write a bash script to install and configure nginx"

# Ephemeral tangent using past context without saving
$ ask --btw "why did we use nginx instead of caddy earlier?"

# Open interactive chat
$ ask -i
```

---

## Highlights

- **Ghost-Complete Autocomplete**: Instant in-place command autocomplete right at your terminal prompt (`Alt+RightArrow`). Build-aware intelligence predicts complex pipelines, commands, and multi-line loops dynamically customized to your exact OS distribution, shell, and elevation status (`sudo` / `Administrator`).
- **Lightning Fast**: Single static binary written in pure Go. Starts in under 10ms with zero runtime dependencies.
- **Inline Multi-Line Composer**: Run `ask` with no arguments to type or paste multi-line prompts and scripts directly in your shell.
- **Progressive `@filename` Navigation**: Attach local files effortlessly. Typing `@` activates lazy, single-level directory peeling (`@src/`) so even massive repos and `node_modules` never freeze.
- **Interactive Chat (`ask -i`)**: Continuous back-and-forth dialogue with session memory, mouse text selection, and quick-access `/` slash command dropdowns.
- **Tangent Queries (`--btw` & `/btw`)**: Ask side questions that read from past session memory without contaminating future conversation turns or disk files.
- **High-Contrast Design**: Crisp Deep Midnight Navy message boxes with bright white text, electric cyan borders, and amber tags for all terminal themes.
- **Clipboard Feedback**: Visual toast indicators (`[Copied to clipboard]`) when yanking text with `y`, `Enter`, or `Ctrl+Shift+C`.
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
─ ask prompt [ Alt+→: Send | Ctrl+C: Cancel ] ──────────────────────────────
  Write a Python script that monitors disk usage
  and alerts via webhook when / exceeds 90%.
────────────────────────────────────────────────────────────────────────────
```
- Press `Enter` for a new line.
- Press `Alt+→` (`Alt+RightArrow`, or `Ctrl+S` / `Ctrl+Enter`) to submit.
- Press `Ctrl+Right` / `Ctrl+Left` to jump words forward / backward.
- Press `Ctrl+C` to cancel.

### 3. File References & Progressive Onion-Peeling (`@filename`)
Seamlessly include code or config files in your prompts across one-shot queries, the composer, and interactive chat:
```bash
# Attach files in one-shot prompts
ask "explain the architecture in @main.go and @internal/cli/editor.go"

# Check code for bugs
ask --cg "refactor @src/utils.py to use async/await"
```
- **Lazy Single-Level "Onion-Peeling"**: In `ask` (empty composer) or `ask -i` (chat), typing `@` lists immediate directory entries first. Selecting a directory (e.g. `@src/`) drills down on-demand, preventing terminal freezes on large repos or `node_modules`.
- **Keyboard Navigation**:
  - `Up` / `Down` arrows to navigate candidates.
  - `Tab` or `Enter` to autocomplete.
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
- **Interactive Slash Commands (`/`)**: Typing `/` opens an auto-filtering dropdown menu (`/help`, `/btw`, `/clear`, `/exit`) navigating with `Up`/`Down` and dispatching on `Enter`.
- **Conversational Tangents (`/btw`)**: Type `/btw <question>` to branch into a side topic using past memory without adding the tangent to future conversation context.
- **Confirmation Modals**: Safeguards against accidental exits or history purges (`/clear` and `/exit` prompt for confirmation).
- **Mouse Selection**: Click and drag to select text in chat history, then press `y`, `Enter`, or `Ctrl+Shift+C` to copy to clipboard (with green toast feedback).
- **Keyboard Shortcuts**:
  - `Alt+→` (`Alt+RightArrow`, or `Ctrl+S` / `Ctrl+Enter`) — Send prompt
  - `Ctrl+Right` / `Ctrl+Left` — Jump word forward / backward
  - `Enter` — Insert newline in prompt
  - `Up` / `Down` — Navigate history & prompt
  - `Shift+Arrows` — Highlight text in history
  - `y`, `Enter`, or `Ctrl+Shift+C` — Copy selection to clipboard

### 5. Tangent Queries (`--btw`)
Want to ask a quick side question without permanently affecting your terminal session's ongoing conversation?
```bash
# Uses past conversation history for context, but does NOT save the tangent
ask --btw "why did you recommend using systemd instead of supervisord in the previous step?"
```
- **Read-Only Context**: Reads past turns to understand your environment and code.
- **Zero Pollution**: Neither the query nor the answer is stored in future session memory.

### 6. Code Generation
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

### 7. Pipes & Logs
Feed command outputs directly into `ask`:
```bash
# Debug system logs
journalctl -u postgresql -n 50 --no-pager | ask "why did the service fail to start?"

# Review git changes
git diff HEAD~1 | ask "identify any security risks in this diff"

# Analyze network status
ss -tulpn | ask "which ports are exposed to the public internet?"
```

### 8. Ghost-Complete: AI Shell Autocompletion (`Alt+RightArrow`)
Complete shell commands in-place right at your terminal prompt:
```bash
# Install and enable Ghost-Complete in your shell profile
ask --summon-ghost
# (Alias: ask --raise-ghost)

# Disable and cleanly remove Ghost-Complete from your shell profile
ask --slay-ghost
```
- Type any partial command or natural language intent (e.g. `get-proc` or `# find large files`).
- Press **`Alt+RightArrow`** to instantly complete the line with the AI recommendation.
- Press **`Enter`** to execute.
- **Build-Aware Environment**: Injects your exact OS distribution, kernel build, active shell version, installed package managers, and live process elevation (`sudo` / `Administrator`) into the autocomplete prompt.
- Supported shells: PowerShell, Bash, Zsh, Fish.

### 9. Changing Models
Check your settings or switch default models anytime:
```bash
# View current configuration and system context
ask --config

# Switch default model
ask --set-model google/gemini-2.0-flash-exp:free
ask --set-model deepseek/deepseek-r1:free
ask --set-model anthropic/claude-3.5-sonnet
```

### 10. Session Management
```bash
# Clear session memory for current terminal tab
ask --clear

# Run a one-off query with total amnesia (neither reads nor saves history)
ask --no-context "what is the default ssh port?"

# Inspect build-aware context & live elevation
ask --system-context
```

---

## Command Reference

| Command / Flag | Description |
| :--- | :--- |
| `ask <prompt>` | Run a one-shot query |
| `ask` | Open the inline multi-line prompt composer |
| `ask -i`, `ask --chatbot` | Launch interactive chat mode |
| `ask --btw <prompt>` | Run tangent query using past context without updating session |
| `ask --no-context <prompt>` | Run query with total amnesia (neither reads nor saves history) |
| `ask --cg`, `--codegen` | Force pure code output (no fences or commentary) |
| `ask -o <file>` | Save response directly to file (overwrites) |
| `ask -a <file>` | Append response directly to file |
| `ask --clear`, `ask -c` | Clear session context for current shell |
| `ask --summon-ghost`, `--raise-ghost` | Install and enable Ghost-Complete in shell profile (`Alt+RightArrow`) |
| `ask --slay-ghost` | Disable and remove Ghost-Complete from shell profile |
| `ask --complete <line>` | Headless single-line command completion query for shell hooks |
| `ask --set-key <key>` | Save OpenRouter API key |
| `ask --set-model <model>` | Set default AI model |
| `ask --config` | Display current configuration and status |
| `ask --system-context`, `--env` | Display build-aware system context, shell version, and privilege status |
| `ask --update-self` | Update `ask` binary and man page to the latest release |
| `ask --uninstall-self` | Cleanly remove `ask` binary, man pages, and shell hooks with confirmation |

---

## License

[MIT](LICENSE) © Mugunthan
