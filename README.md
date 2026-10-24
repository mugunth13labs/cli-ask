# ask — The Non-Intrusive Terminal AI

> *"Keeping it simple is harder than you think."*

`ask` is a lightweight, zero-dependency command-line utility designed for engineers, system administrators, and terminal purists who want succinct AI answers without disrupting their shell flow.

```bash
$ ask "tar command to extract without verbose"
tar -xf archive.tar.gz

$ ask how to kill process by port in linux
fuser -k <port>/tcp
# or
lsof -ti :<port> | xargs kill -9
```

---

## The Philosophy

Most modern "AI CLI" tools make a fundamental mistake: **they get in your way.**

They trap you in full-screen interactive TUIs, demand bloated runtimes, spawn autonomous loops you didn't ask for, or leak conversation context across unrelated terminal tabs. For an engineer troubleshooting production infrastructure or debugging an active service, that friction kills productivity.

`ask` is built on a different principle: **The Unix Philosophy.**

- **A Filter, Not a Framework**: `ask` behaves like `grep`, `awk`, or `curl`. You run a command, it streams the concise answer straight to standard output, and exits immediately. Your prompt is ready for the next task.
- **Session-Scoped by Design**: Memory is strictly scoped to the parent shell process (PPID). Two terminal tabs or tmux panes on the same machine never contaminate each other's context. Your Kubernetes debugging in Tab 1 never bleeds into your Terraform work in Tab 2.
- **Auto-Compacting, Not Discarding**: Rather than dropping context abruptly with an arbitrary timer, long multi-turn sessions automatically roll into concise summaries behind the scenes. Zero latency overhead, zero token window blowouts.
- **Zero Baggage**: Written in pure Go with zero external dependencies. Under 10ms startup, no Python virtual environments, no node_modules, no compiler needed to run.

---

## Features

- ⚡ **Instant Execution**: Native standalone binary. Starts immediately with near-zero latency.
- 🆓 **Free Tier by Default**: Pre-configured with top-performing free models on OpenRouter (e.g. `nvidia/nemotron-3.5-lightning:free`).
- 🌊 **Token Streaming**: Real-time token streaming straight to stdout.
- 💡 **Visual Feedback**: Shows `[<model> thinking...]` on stderr as soon as you submit, erasing cleanly once tokens begin streaming.
- 🧠 **Context Awareness**: Natural follow-ups within the same terminal session (`ask "now filter by status running"`).
- 📝 **Interactive Multiline Buffer**: Type `ask` alone to paste multiline logs, scripts, or pipelines byte-for-byte until OS EOF (`Ctrl+D` / `Ctrl+Z`).
- 🔌 **Standard Unix Piping**: Feed command outputs or diffs directly (`git diff | ask "summarize changes"`).
- 🧹 **Zero Configuration Friction**: Auto-prompts for your OpenRouter key on first run and saves locally to `~/.config/ask/config.json`.

---

## Installation

### 1. Ubuntu / Debian (`apt`)
Add the repository and install natively via `apt`:
```bash
# Add repository source
echo "deb [trusted=yes] https://mugunth13labs.github.io/cli-ask/ stable main" | sudo tee /etc/apt/sources.list.d/cli-ask.list

# Update and install
sudo apt update
sudo apt install cli-ask
```

### 2. Linux & macOS (One-line shell script)
```bash
curl -fsSL https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.sh | sh
```

### 3. Windows (PowerShell one-line install)
```powershell
irm https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.ps1 | iex
```

### 4. Manual Download
Pre-compiled native binaries and `.deb` packages are available directly on the [Releases](https://github.com/mugunth13labs/cli-ask/releases) page:
- **Debian / Ubuntu Packages**: `cli-ask_*_amd64.deb`, `cli-ask_*_arm64.deb` (`sudo apt install ./cli-ask_*.deb`)
- **Linux Binaries**: `ask-linux-amd64`, `ask-linux-arm64`
- **macOS Binaries**: `ask-darwin-arm64` (Apple Silicon), `ask-darwin-amd64` (Intel)
- **Windows Binaries**: `ask-windows-amd64.exe`

---

## Quick Setup

1. Get a free API key at [OpenRouter](https://openrouter.ai/settings/keys).
2. Set your key:
```bash
ask --set-key sk-or-v1-xxxxxxxx
```
*(Or set `export OPENROUTER_API_KEY="sk-or-v1-..."` in your environment).*

---

## Usage Guide & How to Handle Edge Cases

### 1. Direct Queries & Unquoted Words
You can pass single-line queries with or without quotes:
```bash
ask "explain how to configure a systemd service"
ask what does the -F flag do in awk
```

### 2. Literal Command Flags (Preserving Option Flags)
`ask` recognizes that commands often contain their own flags. Unrecognized flags are preserved as prompt text:
```bash
ask ps -eo pid,cmd --sort
ask tar -czvf archive.tar.gz
```

### 3. Edge Case: Shell Operators (`|`, `>`, `&`, `;`)
> [!IMPORTANT]
> **Why `ask ps aux | grep root` doesn't work as a single query**:
> In all operating systems (Bash, Zsh, PowerShell), the shell intercepts pipe (`|`) and redirection (`>`, `&`, `;`) operators **before** passing arguments to any program.
>
> If you run:
> ```bash
> ask ps aux | grep root   # ❌ The shell runs ask, then pipes ask's output into grep!
> ```
>
> **The Two Proper Ways to Query Complex Command Chains:**
>
> **Method A: Wrap in single quotes**:
> ```bash
> ask 'ps -eo pid,ppid,stat,cmd | awk "NR==1 || \$3==\"Z\"" | sort -r'
> ```
>
> **Method B: Interactive Multiline Mode (Recommended for long snippets)**:
> Simply run `ask` with no arguments, paste the entire pipeline verbatim, and press `Ctrl+D` (`Ctrl+Z` then `Enter` on Windows):
> ```bash
> $ ask
> Type or paste prompt (press Ctrl+D to submit, Ctrl+C to cancel):
> ps -eo pid,ppid,stat,cmd | awk 'NR==1 || $3=="Z" || $3=="D"' | sort -r -k3,3 | nl -v 0
> explain what this does
> [Ctrl+D]
> ```
> Inside multiline mode, the shell never intercepts or interprets any characters.

### 4. Edge Case: Flag Disambiguation & The `--` Separator
What if you want to ask a question about an `ask` flag itself, like asking *"what does --config do?"*

- **Standalone Tool Actions**: Flags like `--config`, `--help`, `--version`, and `--clear` only trigger tool actions when entered **alone** (`ask --config`).
- **If Extra Arguments Are Present**: If you type `ask --config ps --help`, `ask` treats it as a **question** for the AI, rather than dumping your settings.
- **The Absolute Escape Hatch (`--`)**: If you want to guarantee that zero tokens are interpreted as `ask` options, use standard POSIX `--`:
  ```bash
  ask -- --clear
  ask -- --config
  ```
  Everything following `--` is treated strictly as literal prompt text.

### 5. Conversational Follow-ups (Scoped to Active Shell)
Context is remembered within the **same terminal tab**:
```bash
$ ask "how to check listening ports with ss?"
ss -tulpn

$ ask "filter only tcp sockets"
ss -tulpn | grep tcp
```

### 6. Standard Input Piping
Feed file outputs, diffs, or logs directly into `ask`:
```bash
# Debug system logs
journalctl -xe --no-pager | tail -n 50 | ask "what caused this failure?"

# Review git diffs
git diff | ask "review this diff for potential performance regressions"

# Inspect active services
netstat -tulpn | ask "which ports are exposed publicly?"
```

### 7. Session Controls
```bash
# Clear conversation history for the current terminal window
ask --clear   # or ask -c

# Run a one-off query without reading or recording session memory
ask --no-context "what is the default ssh port?"
```

### 8. Switching Models
By default, `ask` uses top-performing free models like `nvidia/nemotron-3.5-lightning:free`. You can switch to any free or paid model on OpenRouter:
```bash
# Check current config & active session
ask --config

# Switch models
ask --set-model google/gemma-4-31b-it:free
ask --set-model deepseek/deepseek-r1:free
ask --set-model anthropic/claude-3.5-sonnet
```

---

## Packaging & Debian

`cli-ask` complies with Debian Policy (§4.7.0) with zero external dependencies, standard roff manpage documentation (`man ask`), and full DFSG compliance.

Packaging source files and ITP templates are located in [`debian/`](debian/).

---

## License

[MIT](LICENSE) © Mugunthan
