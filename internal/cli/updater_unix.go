//go:build !windows

package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func determineManTargetPath(execPath string) string {
	// 1. If an existing ask.1 exists in any standard location, prioritize updating it
	candidates := []string{
		"/usr/local/share/man/man1/ask.1",
		"/usr/share/man/man1/ask.1",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "man", "man1", "ask.1"))
	}
	if execPath != "" {
		if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
			prefix := filepath.Dir(filepath.Dir(resolved))
			customMan := filepath.Join(prefix, "share", "man", "man1", "ask.1")
			candidates = append(candidates, customMan)
		}
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	// 2. No existing ask.1 found -> choose installation target based on binary location and permissions
	if execPath != "" {
		if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
			execDir := filepath.Dir(resolved)
			if strings.HasPrefix(execDir, "/usr/local") {
				return "/usr/local/share/man/man1/ask.1"
			}
			if strings.HasPrefix(execDir, "/usr") {
				if os.Geteuid() == 0 {
					return "/usr/local/share/man/man1/ask.1"
				}
				if home, err := os.UserHomeDir(); err == nil && home != "" {
					return filepath.Join(home, ".local", "share", "man", "man1", "ask.1")
				}
				return "/usr/local/share/man/man1/ask.1"
			}
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				if strings.HasPrefix(resolved, home) {
					return filepath.Join(home, ".local", "share", "man", "man1", "ask.1")
				}
			}
			prefix := filepath.Dir(execDir)
			return filepath.Join(prefix, "share", "man", "man1", "ask.1")
		}
	}

	if os.Geteuid() == 0 {
		return "/usr/local/share/man/man1/ask.1"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "man", "man1", "ask.1")
	}
	return "/usr/local/share/man/man1/ask.1"
}

func updateManPage(client *http.Client, tag string) {
	execPath, _ := os.Executable()
	targetMan := determineManTargetPath(execPath)
	if targetMan == "" {
		return
	}

	manURL := fmt.Sprintf("https://raw.githubusercontent.com/mugunth13labs/cli-ask/%s/man/ask.1", tag)
	resp, err := client.Get(manURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	existed := false
	if _, err := os.Stat(targetMan); err == nil {
		existed = true
	}

	dir := filepath.Dir(targetMan)
	if err := os.MkdirAll(dir, 0755); err != nil {
		// If system directory cannot be created (e.g. not root), fallback to ~/.local/share/man/man1
		if home, herr := os.UserHomeDir(); herr == nil && home != "" {
			targetMan = filepath.Join(home, ".local", "share", "man", "man1", "ask.1")
			dir = filepath.Dir(targetMan)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return
			}
		} else {
			return
		}
	}

	tmpFile, err := os.CreateTemp(dir, "ask-man-*")
	if err != nil {
		// If creating in dir failed (e.g. dir not writable), try user directory
		if home, herr := os.UserHomeDir(); herr == nil && home != "" {
			targetMan = filepath.Join(home, ".local", "share", "man", "man1", "ask.1")
			dir = filepath.Dir(targetMan)
			_ = os.MkdirAll(dir, 0755)
			tmpFile, err = os.CreateTemp(dir, "ask-man-*")
			if err != nil {
				return
			}
		} else {
			return
		}
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return
	}
	tmpFile.Close()
	_ = os.Chmod(tmpName, 0644)

	if err := os.Rename(tmpName, targetMan); err == nil {
		if existed {
			fmt.Printf("Updated manual page at %s\n", targetMan)
		} else {
			fmt.Printf("Installed manual page at %s\n", targetMan)
		}
	}
}
