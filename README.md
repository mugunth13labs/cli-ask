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
- 🧠 **Context Awareness**: Natural follow-ups within the same terminal session (`ask "now filter by status running"`).
- 🔌 **Standard Unix Piping**: Feed command outputs or diffs directly (`git diff | ask "summarize changes"`).
- 🧹 **Zero Configuration Friction**: Auto-prompts for your OpenRouter key on first run and saves locally to `~/.config/ask/config.json`.

---

## Installation

### 1. Ubuntu / Debian (`apt`)
Add repository and install natively via `apt`:
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

### 4. Direct `.deb` / Binary Download
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

## Usage & Workflows

### Direct Queries
```bash
ask "explain how to configure a systemd service"
ask what does the -F flag do in awk
```

### Conversational Follow-ups (Scoped to Active Shell)
```bash
$ ask "how to check listening ports with ss?"
ss -tulpn

$ ask "filter only tcp sockets"
ss -tulpn | grep tcp
```

### Stdin Piping
```bash
# Debug system logs
journalctl -xe --no-pager | tail -n 50 | ask "what caused this failure?"

# Review git changes
git diff | ask "review this diff for potential performance regressions"

# Inspect network state
netstat -tulpn | ask "which ports are exposed publicly?"
```

### Session Controls
```bash
# Clear conversation history for the current terminal window
ask --clear   # or ask -c

# Run a one-off query without reading or recording session memory
ask --no-context "what is the default ssh port?"
```

### Model Switching
```bash
# Check current config & active session
ask --config

# Switch to any model on OpenRouter (free or paid)
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
