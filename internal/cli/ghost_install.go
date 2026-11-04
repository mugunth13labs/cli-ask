package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ghostMarker = "# ASK GHOST"
)

func getZshHookScript() string {
	return `_ask_ghost_complete() {
    if [[ -n "$BUFFER" ]]; then
        local chosen exit_code
        chosen=$(ask --complete "$BUFFER")
        exit_code=$?
        if [[ $exit_code -eq 0 && -n "$chosen" ]]; then
            if zsh -n -c "$chosen" 2>/dev/null; then
                BUFFER="$chosen"
                CURSOR=${#BUFFER}
                zle reset-prompt
                return
            else
                print -n "\e[90m [suggestion rejected: invalid syntax]\e[0m" >&2
                sleep 0.6
                zle reset-prompt
            fi
        elif [[ $exit_code -eq 2 ]]; then
            print -n "\e[90m [suggestion rejected: incomplete]\e[0m" >&2
            sleep 0.6
            zle reset-prompt
        elif [[ $exit_code -eq 1 ]]; then
            print -n "\e[90m [autocomplete: network error]\e[0m" >&2
            sleep 0.6
            zle reset-prompt
        fi
    fi
}
zle -N _ask_ghost_complete
bindkey '^[[1;3C' _ask_ghost_complete
bindkey '^[^[[C' _ask_ghost_complete
`
}

func getBashHookScript() string {
	return `_ask_ghost_complete() {
    if [[ -n "$READLINE_LINE" ]]; then
        local chosen exit_code
        chosen=$(ask --complete "$READLINE_LINE")
        exit_code=$?
        if [[ $exit_code -eq 0 && -n "$chosen" ]]; then
            if bash -n <<< "$chosen" 2>/dev/null; then
                READLINE_LINE="$chosen"
                READLINE_POINT=${#READLINE_LINE}
                return
            else
                printf "\e[90m [suggestion rejected: invalid syntax]\e[0m" >&2
                sleep 0.6
            fi
        elif [[ $exit_code -eq 2 ]]; then
            printf "\e[90m [suggestion rejected: incomplete]\e[0m" >&2
            sleep 0.6
        elif [[ $exit_code -eq 1 ]]; then
            printf "\e[90m [autocomplete: network error]\e[0m" >&2
            sleep 0.6
        fi
    fi
}
bind -x '"\e[1;3C": _ask_ghost_complete'
bind -x '"\e\e[C": _ask_ghost_complete'
`
}

func getPowerShellHookScript() string {
	return `if (Get-Module -Name PSReadLine -ListAvailable) {
    $askGhost = {
        param($key, $arg)
        $line = $null
        $cursor = $null
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)
        if ($line -and $line.Trim().Length -gt 0) {
            $chosen = (& ask --complete "$line")
            $code = $LASTEXITCODE
            if ($code -eq 0 -and $chosen) {
                $text = (($chosen -join [char]10) -replace [char]13, "").Trim()
                $t = $null
                $errs = $null
                [void][System.Management.Automation.Language.Parser]::ParseInput($text, [ref]$t, [ref]$errs)
                if (-not $errs -or $errs.Count -eq 0) {
                    try {
                        [Microsoft.PowerShell.PSConsoleReadLine]::RevertLine($key, $arg)
                        [Microsoft.PowerShell.PSConsoleReadLine]::BeginningOfLine($key, $arg)
                        [Microsoft.PowerShell.PSConsoleReadLine]::DeleteToEnd($key, $arg)
                        [Microsoft.PowerShell.PSConsoleReadLine]::Insert($text)
                        [Microsoft.PowerShell.PSConsoleReadLine]::EndOfLine($key, $arg)
                    } catch {}
                    return
                } else {
                    [Console]::Write([char]27 + "[90m [suggestion rejected: invalid syntax]" + [char]27 + "[0m")
                    Start-Sleep -Milliseconds 600
                }
            } elseif ($code -eq 2) {
                [Console]::Write([char]27 + "[90m [suggestion rejected: incomplete]" + [char]27 + "[0m")
                Start-Sleep -Milliseconds 600
            } elseif ($code -eq 1) {
                [Console]::Write([char]27 + "[90m [autocomplete: network error]" + [char]27 + "[0m")
                Start-Sleep -Milliseconds 600
            }
            try {
                [Microsoft.PowerShell.PSConsoleReadLine]::InvokePrompt($key, $arg)
            } catch {}
        }
    }
    Set-PSReadLineKeyHandler -Chord 'Alt+RightArrow' -ScriptBlock $askGhost
}
`
}

func getFishHookScript() string {
	return `function _ask_ghost_complete
    set -l buf (commandline)
    if test -n "$buf"
        set -l chosen (ask --complete "$buf")
        set -l exit_code $status
        if test $exit_code -eq 0 -a -n "$chosen"
            if fish --no-execute -c "$chosen" 2>/dev/null
                commandline -r "$chosen"
                commandline -C (string length -- "$chosen")
                commandline -f repaint
                return
            else
                echo -en "\e[90m [suggestion rejected: invalid syntax]\e[0m" >&2
                sleep 0.6
                commandline -f repaint
            end
        else if test $exit_code -eq 2
            echo -en "\e[90m [suggestion rejected: incomplete]\e[0m" >&2
            sleep 0.6
            commandline -f repaint
        else if test $exit_code -eq 1
            echo -en "\e[90m [autocomplete: network error]\e[0m" >&2
            sleep 0.6
            commandline -f repaint
        end
    end
end
bind \e\[1\;3C _ask_ghost_complete
bind \e\e\[C _ask_ghost_complete
`
}

func detectShellProfilePath() (profilePath string, shellType string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("unable to determine user home directory: %w", err)
	}

	shellType = DetectActiveShell()

	switch shellType {
	case "powershell":
		if runtime.GOOS == "windows" {
			out, psErr := exec.Command("powershell", "-NoProfile", "-Command", "$PROFILE.CurrentUserCurrentHost").Output()
			if psErr == nil {
				target := strings.TrimSpace(string(out))
				if target != "" {
					return target, "powershell", nil
				}
			}
			docsDir := filepath.Join(home, "Documents")
			profilePath = filepath.Join(docsDir, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
		} else {
			profilePath = filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
		}
		return profilePath, "powershell", nil

	case "zsh":
		return filepath.Join(home, ".zshrc"), "zsh", nil

	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), "fish", nil

	case "bash":
		fallthrough
	default:
		bashrc := filepath.Join(home, ".bashrc")
		if _, statErr := os.Stat(bashrc); statErr == nil {
			return bashrc, "bash", nil
		}
		bashProfile := filepath.Join(home, ".bash_profile")
		if _, statErr := os.Stat(bashProfile); statErr == nil {
			return bashProfile, "bash", nil
		}
		return bashrc, "bash", nil
	}
}

func getManagedScriptInfo(shellType string) (filename, content, profileLine string) {
	switch shellType {
	case "powershell":
		filename = "ask-ghost.ps1"
		content = getPowerShellHookScript()
		profileLine = fmt.Sprintf(`if (Test-Path "%s") { . "%s" } %s`,
			filepath.Join("~", ".config", "ask", "scripts", filename),
			filepath.Join("~", ".config", "ask", "scripts", filename),
			ghostMarker)
	case "zsh":
		filename = "ask-ghost.zsh"
		content = getZshHookScript()
		profileLine = fmt.Sprintf(`[ -f "$HOME/.config/ask/scripts/%s" ] && source "$HOME/.config/ask/scripts/%s" %s`, filename, filename, ghostMarker)
	case "fish":
		filename = "ask-ghost.fish"
		content = getFishHookScript()
		profileLine = fmt.Sprintf(`test -f "$HOME/.config/ask/scripts/%s" && source "$HOME/.config/ask/scripts/%s" %s`, filename, filename, ghostMarker)
	case "bash":
		fallthrough
	default:
		filename = "ask-ghost.bash"
		content = getBashHookScript()
		profileLine = fmt.Sprintf(`[ -f "$HOME/.config/ask/scripts/%s" ] && source "$HOME/.config/ask/scripts/%s" %s`, filename, filename, ghostMarker)
	}
	return filename, content, profileLine
}

func printSafetyWarning() {
	fmt.Println("\x1b[1;38;5;214m[!] Safety Warning:\x1b[0m AI-generated commands can be unpredictable or destructive.")
	fmt.Println("    Always inspect the completed command before pressing Enter to execute.")
}

// InstallShellGhost writes the hook script and appends a single reference line to the shell profile.
func InstallShellGhost() error {
	profilePath, shellType, err := detectShellProfilePath()
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	scriptsDir := filepath.Join(home, ".config", "ask", "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return fmt.Errorf("failed to create scripts directory %s: %w", scriptsDir, err)
	}

	filename, content, profileLine := getManagedScriptInfo(shellType)
	scriptFilePath := filepath.Join(scriptsDir, filename)

	if err := os.WriteFile(scriptFilePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write hook script %s: %w", scriptFilePath, err)
	}

	_ = cleanProfileEntries(profilePath)

	profileContent := ""
	if data, readErr := os.ReadFile(profilePath); readErr == nil {
		profileContent = string(data)
	}

	var sb strings.Builder
	sb.WriteString(profileContent)
	if len(profileContent) > 0 && !strings.HasSuffix(profileContent, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString(profileLine + "\n")

	if err := os.MkdirAll(filepath.Dir(profilePath), 0755); err != nil {
		return fmt.Errorf("failed to create profile directory: %w", err)
	}
	if err := os.WriteFile(profilePath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to update profile %s: %w", profilePath, err)
	}

	printSafetyWarning()
	fmt.Println("\x1b[1;32m[✓] Ghost autocompletion: Enabled\x1b[0m (Trigger: Alt+RightArrow)")
	if shellType == "powershell" {
		fmt.Println("Restart your PowerShell session or run: . $PROFILE")
	} else {
		fmt.Printf("Restart your shell or run: source %s\n", profilePath)
	}
	return nil
}

func cleanProfileEntries(profilePath string) error {
	data, err := os.ReadFile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	insideBlock := false

	for _, line := range lines {
		if strings.Contains(line, "# BEGIN ASK GHOST") {
			insideBlock = true
			continue
		}
		if insideBlock {
			if strings.Contains(line, "# END ASK GHOST") {
				insideBlock = false
			}
			continue
		}
		if strings.Contains(line, ghostMarker) {
			continue
		}
		result = append(result, line)
	}

	cleaned := strings.TrimRight(strings.Join(result, "\n"), "\n")
	if len(cleaned) > 0 {
		cleaned += "\n"
	}
	return os.WriteFile(profilePath, []byte(cleaned), 0644)
}

// UninstallShellGhost cleanly removes the ghost integration.
func UninstallShellGhost() error {
	profilePath, shellType, err := detectShellProfilePath()
	if err != nil {
		return err
	}

	if err := cleanProfileEntries(profilePath); err != nil {
		return fmt.Errorf("failed to update %s: %w", profilePath, err)
	}

	home, _ := os.UserHomeDir()
	filename, _, _ := getManagedScriptInfo(shellType)
	scriptFilePath := filepath.Join(home, ".config", "ask", "scripts", filename)
	_ = os.Remove(scriptFilePath)

	fmt.Println("[○] Ghost autocompletion: Disabled")
	fmt.Println("Changes will take effect in new shells, or restart your current session.")
	return nil
}

// IsShellGhostInstalled checks if the shell ghost widget reference is in the profile.
func IsShellGhostInstalled() (bool, string, error) {
	profilePath, _, err := detectShellProfilePath()
	if err != nil {
		return false, "", err
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, profilePath, nil
		}
		return false, profilePath, err
	}

	content := string(data)
	installed := strings.Contains(content, ghostMarker) || strings.Contains(content, "# BEGIN ASK GHOST")
	return installed, profilePath, nil
}

// ToggleShellGhost toggles the ghost integration on or off.
func ToggleShellGhost() error {
	installed, _, err := IsShellGhostInstalled()
	if err != nil {
		return err
	}
	if installed {
		return UninstallShellGhost()
	}
	return InstallShellGhost()
}
