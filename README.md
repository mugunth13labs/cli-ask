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

They trap you in bloated full-screen TUIs, demand heavy runtimes, spawn autonomous loops you didn't ask for, or leak conversation context across unrelated terminal tabs. For an engineer troubleshooting production infrastructure or debugging an active service, that friction kills productivity.

`ask` is built on a different principle: **The Unix Philosophy.**

- **A Filter, Not a Framework**: `ask` behaves like `grep`, `awk`, or `curl`. You run a command, it streams the concise answer straight to standard output, and exits immediately. Your prompt is ready for the next task.
- **Isolated Alternate Screen Composer**: Running `ask` with no prompt opens a temporary composer in the Alternate Screen Buffer (like `man` or `less`). You can edit, navigate with arrows, and backspace across lines. Submitting exits the alternate screen, restores your shell, and prints the AI response cleanly to stdout.
- **Pure Code When You Need Code**: Generates raw, syntactically valid code starting directly with shebangs or declarations—zero conversational preambles, zero markdown code fences, and zero alien ASCII title banners breaking your scripts.
- **Session-Scoped by Design**: Memory is strictly scoped to the parent shell process (PPID). Two terminal tabs or tmux panes on the same machine never contaminate each other's context. Your Kubernetes debugging in Tab 1 never bleeds into your Terraform work in Tab 2.
- **Auto-Compacting, Not Discarding**: Rather than dropping context abruptly with an arbitrary timer, long multi-turn sessions automatically roll into concise summaries behind the scenes. Zero latency overhead, zero token window blowouts.
- **Terminal-Native Plain Text**: Outputs clean, readable plain text with ASCII underlines and clean indentation—no unrendered web markdown fences or raw hash symbols cluttering your terminal.
- **Direct File Integration**: Write or append AI responses directly to files with `-o` or `-a` with live terminal progress.
- **Zero Baggage**: Written in pure Go with zero external dependencies. Under 10ms startup, no Python virtual environments, no node_modules, no compiler needed to run.

---

## Features

- ⚡ **Instant Execution**: Native standalone binary. Starts immediately with near-zero latency.
- 📝 **Alternate Screen Micro-Editor**: Isolated prompt composer with full arrow-key navigation, cross-line Backspace, and bracketed paste safety.
- 💻 **Pure Code Generation (`--cg`, `--codegen`)**: Output exact, runnable code or scripts without title headers or markdown fences.
- 🤖 **Smart Codegen Auto-Detection**: Automatically activates codegen mode when saving to script/code files (`.sh`, `.py`, `.pbs`, `.yaml`, etc.) or piping existing scripts.
- 💬 **Immersive Chatbot Mode**: Run `ask -i` or `ask --chatbot` for persistent conversational interaction in the Alternate Screen Buffer.
- 📁 **Direct File Output**: Save responses directly using `ask -o <file>` (overwrite) or `ask -a <file>` (append).
- 🆓 **Free Tier by Default**: Pre-configured with top-performing free models on OpenRouter (e.g. `nvidia/nemotron-3.5-lightning:free`).
- 🌊 **Token Streaming**: Real-time token streaming straight to stdout or files.
- 💡 **Smart Progress & Splitters**: Shows live progress on stderr during file redirection (`[streaming to output...] -> [done]`); interactive sessions display a subtle divider line (`─── ask [model] ──────────`).
- 🔄 **Seamless Updates**: Built-in self-updater (`ask --update-self`) to stay on the latest version across all platforms.
- 🧠 **Context Awareness**: Natural follow-ups within the same terminal session (`ask "now filter by status running"`).
- 🔌 **Standard Unix Piping**: Feed command outputs or diffs directly (`git diff | ask "summarize changes"`).
- 🧹 **Zero Configuration Friction**: Auto-prompts for your OpenRouter key on first run and saves locally to `~/.config/ask/config.json`.

---

## Installation

### 1. Linux & macOS (One-line shell script)
```bash
curl -fsSL https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.sh | sh
```

### 2. Windows (PowerShell one-line install)
```powershell
irm https://raw.githubusercontent.com/mugunth13labs/cli-ask/main/install.ps1 | iex
```

### 3. Updating to Latest Version
Once installed, you can update `ask` directly in-place from anywhere in your terminal:
```bash
ask --update-self
```
*(On Windows, `install.ps1` safely replaces locked executables; on Linux, run with `sudo ask --update-self` if installed in `/usr/local/bin`).*

### 4. Manual Download
Pre-compiled native binaries are available directly on the [Releases](https://github.com/mugunth13labs/cli-ask/releases) page:
- **Linux**: `ask-linux-amd64`, `ask-linux-arm64`
- **macOS**: `ask-darwin-arm64` (Apple Silicon), `ask-darwin-amd64` (Intel)
- **Windows**: `ask-windows-amd64.exe`

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

### 2. Interactive Multiline Composer (Alternate Screen)
Simply run `ask` with no prompt arguments to open the micro-editor in a temporary Alternate Screen Buffer (like `man` or `less`):
```bash
$ ask
```
Inside the composer:
- **Full Navigation**: Use Arrow Keys ($\uparrow, \downarrow, \leftarrow, \rightarrow$) to move around.
- **Cross-Line Backspace**: Pressing Backspace on column 0 pulls the line up and merges it into the line above.
- **Paste Safe**: Paste multi-paragraph text or code with blank lines safely (Bracketed Paste Mode).
- **Literal Characters**: Characters like `;`, `>`, `|`, `&`, `$`, `"`, `(`, `)` are treated 100% literally without shell interference.
- **Submit**: Press `Ctrl+Enter` (or `Ctrl+D`).
- **Cancel**: Press `Ctrl+C` or type `/q` / `/exit`.

Upon pressing `Ctrl+Enter`, the composer exits immediately, cleanly restores your terminal screen, and streams the AI answer to `stdout`!

### 3. Pure Code Generation Mode (`--cg`, `--codegen`)
When generating or refactoring scripts, configs, or code, use `--cg` to ensure the output contains strictly raw, executable code without any title headers, ASCII underlines, conversational filler, or markdown code fences:
```bash
# Explicit code generation
ask --cg "write a python function to compute fibonacci"

# Generate a PBS batch script directly to file (auto-activates codegen)
ask -o job.pbs "generate a pbs script template"

# Refactor an existing script via pipe without unwanted alien headers
cat job.pbs | ask "remove alien lines and request 2 nodes with 64gb memory" > job2.pbs
```
`ask` automatically detects code generation intent when:
- The `--cg` or `--codegen` flag is supplied.
- Outputting to code/script extensions (`.sh`, `.py`, `.pbs`, `.slurm`, `.yaml`, `.json`, etc.) with `-o` or `-a`.
- The input stream starts with a shebang (`#!/`) or contains script keywords.

### 4. Continuous Chatbot Mode (`ask -i`, `ask --chatbot`)
When you want an uninterrupted back-and-forth dialogue without exiting to the shell between each turn:
```bash
$ ask -i
ask v0.3.5 — Continuous Chat Mode
Model: nvidia/nemotron-3.5-lightning:free | Session: session-12345
Send: Ctrl+Enter (or Ctrl+D) | Newline: Enter | Exit: exit, /q | Cancel: Ctrl+C

>>> how do I inspect docker container network interfaces?
─── ask [nvidia/nemotron-3.5-lightning:free] ───────────────────────────
Use docker inspect:

  docker inspect -f '{{json .NetworkSettings.Networks}}' <container_id>

>>> now show the ip address only
─── ask [nvidia/nemotron-3.5-lightning:free] ───────────────────────────
Run:

  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' <container_id>

>>> exit
```
Inside chatbot mode:
- `Enter`: Inserts a newline so you can type paragraphs and code freely.
- `Ctrl+Enter` or `Ctrl+D`: Submits your prompt to the AI.
- `/clear` or `/c`: Reset conversation history for the session.
- `/model <name>`: Switch active model on the fly.
- `/help`: Display in-chat help.
- `exit`, `quit`, `/q`, `/exit`, or `Ctrl+C`: Exit chatbot mode and restore main terminal screen cleanly.

### 5. Direct File Output (`-o`, `-a`)
Save answers directly to files without worrying about shell pipe syntax:
```bash
# Overwrite or create file
ask -o docker_setup.md "create a compose file for postgres and redis"

# Append to an existing notes file
ask -a docker_setup.md "now add healthchecks for both services"

# Launch composer and save result to file
ask -o script.py
```
The terminal displays live progress (`[saving to docker_setup.md...]` -> `[saved to docker_setup.md]`), keeping the output clean and uncorrupted.

### 6. Literal Command Flags (Preserving Option Flags)
`ask` recognizes that commands often contain their own flags. Unrecognized flags are preserved as prompt text:
```bash
ask ps -eo pid,cmd --sort
ask tar -czvf archive.tar.gz
ask gcc -o main main.c
```

### 7. Standard Redirection & Stderr Progress
When redirecting standard output to a file or downstream command, `ask` keeps you informed on `stderr` so your terminal never looks frozen:
```bash
$ ask "summarize kubernetes cgroups" >> notes.txt
[nvidia/nemotron-3.5-lightning:free thinking...]
[nvidia/nemotron-3.5-lightning:free streaming to output...]
[done]
```
The redirected file receives pure, clean plain text without any escape sequences or status lines.

### 8. Flag Disambiguation & The `--` Separator
- **Standalone Tool Actions**: Flags like `--config`, `--help`, `--version`, `--clear`, and `--update-self` only trigger tool actions when entered **alone** (`ask --config`).
- **If Extra Arguments Are Present**: If you type `ask --config ps --help`, `ask` treats it as a **question** for the AI, rather than dumping your settings.
- **The Absolute Escape Hatch (`--`)**: If you want to guarantee that zero tokens are interpreted as `ask` options, use standard POSIX `--`:
  ```bash
  ask -- --clear
  ask -- --config
  ask -- -o file.txt
  ```
  Everything following `--` is treated strictly as literal prompt text.

### 9. Conversational Follow-ups (Scoped to Active Shell)
Context is remembered within the **same terminal tab**:
```bash
$ ask "how to check listening ports with ss?"
ss -tulpn

$ ask "filter only tcp sockets"
ss -tulpn | grep tcp
```

### 10. Standard Input Piping
Feed file outputs, diffs, or logs directly into `ask`:
```bash
# Debug system logs
journalctl -xe --no-pager | tail -n 50 | ask "what caused this failure?"

# Review git diffs
git diff | ask "review this diff for potential performance regressions"

# Inspect active services
netstat -tulpn | ask "which ports are exposed publicly?"
```

### 11. Session Controls
```bash
# Clear conversation history for the current terminal window
ask --clear   # or ask -c

# Run a one-off query without reading or recording session memory
ask --no-context "what is the default ssh port?"
```

### 12. Switching Models
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
