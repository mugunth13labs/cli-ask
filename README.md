# ask — The Instant Terminal AI

`ask` is a zero-dependency, lightning-fast terminal AI assistant powered by [OpenRouter](https://openrouter.ai). Get direct, fluff-free command explanations, code fixes, and answers without leaving your terminal.

```bash
$ ask "tar command to extract without verbose"
tar -xf archive.tar.gz

$ ask how to kill process by port in linux
fuser -k <port>/tcp
# or
lsof -ti :<port> | xargs kill -9
```

---

## Why `ask`?

- ⚡ **Instant & Lightweight**: Single native binary written in Go. Under 10ms startup, no runtime or interpreter overhead.
- 🆓 **Free Tier by Default**: Defaults to high-performance free models on OpenRouter (e.g. `nvidia/nemotron-3.5-lightning:free`).
- 🌊 **Streaming**: Real-time token streaming straight to stdout.
- 🔌 **Unix Philosophy**: Full stdin piping support (`git diff | ask "summarize"`).
- 📦 **Zero Dependencies**: No Python, Node.js, or complex environment setups required.

---

## Installation

### 1. Linux & macOS (One-line install)
```bash
curl -fsSL https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.sh | sh
```

### 2. Windows (PowerShell one-line install)
Run in PowerShell:
```powershell
irm https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.ps1 | iex
```

### 3. Manual Download
Download the standalone binary for your OS directly from [Releases](https://github.com/mugunth13labs/cli-ask/releases):
- **Windows (x64)**: `ask-windows-amd64.exe` (rename to `ask.exe` and place in your PATH)
- **Linux (x64 / arm64)**: `ask-linux-amd64`, `ask-linux-arm64`
- **macOS (Apple Silicon & Intel)**: `ask-darwin-arm64`, `ask-darwin-amd64`

Make it executable and move to `/usr/local/bin` (or your `$PATH`):
```bash
chmod +x ask-*
sudo mv ask-* /usr/local/bin/ask
```

---

## Quick Setup

1. Get a free API key at [OpenRouter](https://openrouter.ai/settings/keys).
2. Set your key:
```bash
ask --set-key sk-or-v1-xxxxxxxx
```
*(Or set `export OPENROUTER_API_KEY="sk-or-v1-..."` in your shell profile).*

---

## Usage

### Direct Queries
```bash
ask "explain how to configure a systemd service"
ask what does the -F flag do in awk
```

### Piped Stdin Context
```bash
# Debug errors in logs
journalctl -xe --no-pager | tail -n 50 | ask "what caused the crash?"

# Review code / diffs
git diff | ask "review this diff for potential issues"

# Explain terminal output
netstat -tulpn | ask "which services are exposed publicly?"
```

### Configuration & Switching Models
```bash
# Check current config
ask --config

# Switch to any model on OpenRouter (free or paid)
ask --set-model google/gemma-4-31b-it:free
ask --set-model anthropic/claude-3.5-sonnet
ask --set-model deepseek/deepseek-r1:free
```

---

## License

[MIT](LICENSE)
