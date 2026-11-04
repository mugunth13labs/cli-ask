package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// RunUninstallSelf removes ask from the system with interactive confirmation and explicit warnings.
func RunUninstallSelf() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to locate executable: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(execPath); evalErr == nil {
		execPath = resolved
	}

	profilePath, _, _ := detectShellProfilePath()
	cfgDir, _ := getConfigDir()

	fmt.Println("\x1b[1;38;5;214m[!] Warning: You are about to completely uninstall ask.\x1b[0m")
	fmt.Println("The following changes will be made to your system:")
	fmt.Printf("  • Remove binary:          %s\n", execPath)
	if profilePath != "" {
		fmt.Printf("  • Remove hook from profile: %s (preserves all your other settings)\n", profilePath)
	}
	home, _ := os.UserHomeDir()
	scriptsDir := filepath.Join(home, ".config", "ask", "scripts")
	fmt.Printf("  • Remove managed scripts: %s\n", scriptsDir)
	if runtime.GOOS != "windows" {
		fmt.Println("  • Remove man pages:       ask.1 (from standard man paths)")
	}
	fmt.Printf("  • User configuration:     %s (optional)\n", cfgDir)
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Proceed with uninstallation? [y/N]: ")
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	if ans != "y" && ans != "yes" {
		fmt.Println("Uninstallation canceled.")
		return nil
	}

	fmt.Print("Purge configuration and saved session history in ~/.config/ask as well? [y/N]: ")
	delConfAns, _ := reader.ReadString('\n')
	delConfAns = strings.TrimSpace(strings.ToLower(delConfAns))
	deleteConfig := (delConfAns == "y" || delConfAns == "yes")

	fmt.Println("\nUninstalling ask...")

	// 1. Remove shell ghost hooks
	_ = UninstallShellGhost()

	// 2. Remove man pages (Unix)
	if runtime.GOOS != "windows" {
		manLocations := []string{
			"/usr/local/share/man/man1/ask.1",
			"/usr/local/man/man1/ask.1",
			"/usr/share/man/man1/ask.1",
		}
		if home != "" {
			manLocations = append(manLocations, filepath.Join(home, ".local", "share", "man", "man1", "ask.1"))
		}

		for _, mPath := range manLocations {
			if _, statErr := os.Stat(mPath); statErr == nil {
				if remErr := os.Remove(mPath); remErr == nil {
					fmt.Printf("Removed man page: %s\n", mPath)
				}
			}
		}
	}

	// 3. Remove configuration and sessions if requested
	if deleteConfig {
		if cfgDir != "" {
			if _, statErr := os.Stat(cfgDir); statErr == nil {
				if remErr := os.RemoveAll(cfgDir); remErr == nil {
					fmt.Printf("Removed configuration and session history: %s\n", cfgDir)
				}
			}
		}
	}

	// 4. Remove binary executable
	if runtime.GOOS == "windows" {
		// On Windows, running binaries are locked. Rename then schedule deletion via detached cmd.
		delPath := execPath + ".del"
		_ = os.Remove(delPath)
		if renameErr := os.Rename(execPath, delPath); renameErr == nil {
			cmd := exec.Command("cmd.exe", "/C", fmt.Sprintf("timeout /t 1 /nobreak > nul & del /F /Q \"%s\" \"%s\"", execPath, delPath))
			_ = cmd.Start()
		} else {
			return fmt.Errorf("failed to remove %s: %w", execPath, renameErr)
		}
	} else {
		// Unix binary removal
		if remErr := os.Remove(execPath); remErr != nil {
			return fmt.Errorf("failed to remove %s (try running with sudo?): %w", execPath, remErr)
		}
	}

	fmt.Printf("Removed binary: %s\n", execPath)
	fmt.Println("\nask has been successfully uninstalled from your system.")
	return nil
}
