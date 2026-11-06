package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// SystemContext encapsulates build-aware operating system, shell, architecture,
// and package management metadata. This subsystem is designed to serve both the
// autocomplete ghost engine and scale across standalone queries, codegen mode,
// and interactive chat sessions in future updates.
type SystemContext struct {
	OSName          string    `json:"os_name"`          // e.g. "windows", "linux", "darwin"
	OSDistribution  string    `json:"os_distro"`        // e.g. "Debian GNU/Linux 13 (trixie)", "Windows 10 Home (26H2)"
	OSBuild         string    `json:"os_build"`         // e.g. "Build 26300", "Linux 6.12.115"
	Architecture    string    `json:"architecture"`     // e.g. "amd64", "arm64"
	Shell           string    `json:"shell"`            // e.g. "powershell", "bash", "zsh", "fish"
	ShellVersion    string    `json:"shell_version"`    // e.g. "PowerShell 5.1.26100", "Bash 5.2.37"
	PackageManagers []string  `json:"package_managers"` // e.g. ["apt"], ["winget", "choco"], ["brew"]
	DetectedAt      time.Time `json:"detected_at"`
}

var (
	cachedContext *SystemContext
	contextMutex  sync.RWMutex
)

// GetActiveSystemContext retrieves the system environment context, reading from the fast
// disk cache (~/.config/ask/system-context.json) when available and valid, or dynamically probing
// and writing the cache if missing, expired (> 7 days), or shell mismatch.
func GetActiveSystemContext() *SystemContext {
	contextMutex.RLock()
	if cachedContext != nil {
		res := cachedContext
		contextMutex.RUnlock()
		return res
	}
	contextMutex.RUnlock()

	contextMutex.Lock()
	defer contextMutex.Unlock()

	// Double-check after acquiring write lock
	if cachedContext != nil {
		return cachedContext
	}

	activeShell := DetectActiveShell()
	cfgDir, err := getConfigDir()
	if err == nil {
		cachePath := filepath.Join(cfgDir, "system-context.json")
		if data, readErr := os.ReadFile(cachePath); readErr == nil {
			var loaded SystemContext
			if jsonErr := json.Unmarshal(data, &loaded); jsonErr == nil {
				// Cache valid if under 7 days old and matching active shell
				if time.Since(loaded.DetectedAt) < 7*24*time.Hour && strings.EqualFold(loaded.Shell, activeShell) {
					cachedContext = &loaded
					return cachedContext
				}
			}
		}
	}

	// Dynamic probe
	probed := ProbeSystemContext()
	cachedContext = probed

	// Save to disk cache asynchronously or synchronously
	if cfgDir != "" {
		_ = os.MkdirAll(cfgDir, 0755)
		cachePath := filepath.Join(cfgDir, "system-context.json")
		if data, marshalErr := json.MarshalIndent(probed, "", "  "); marshalErr == nil {
			_ = os.WriteFile(cachePath, data, 0644)
		}
	}

	return cachedContext
}

// ProbeSystemContext inspects the host machine directly using standard library calls and native queries.
func ProbeSystemContext() *SystemContext {
	var distro, build string

	switch runtime.GOOS {
	case "windows":
		distro, build = probeWindowsOS()
	case "linux":
		distro, build = probeLinuxOS()
	case "darwin":
		distro, build = probeDarwinOS()
	default:
		distro = runtime.GOOS
		build = runtime.GOARCH
	}

	shell := DetectActiveShell()
	shellVer := probeShellVersion(shell)
	pkgManagers := probePackageManagers()

	return &SystemContext{
		OSName:          runtime.GOOS,
		OSDistribution:  distro,
		OSBuild:         build,
		Architecture:    runtime.GOARCH,
		Shell:           shell,
		ShellVersion:    shellVer,
		PackageManagers: pkgManagers,
		DetectedAt:      time.Now(),
	}
}

func probeWindowsOS() (distro, build string) {
	out, err := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "/v", "ProductName").Output()
	productName := ""
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, l := range lines {
			if strings.Contains(l, "ProductName") {
				fields := strings.Fields(l)
				if len(fields) >= 3 {
					productName = strings.Join(fields[2:], " ")
				}
			}
		}
	}

	buildNum := ""
	outBuild, errBuild := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "/v", "CurrentBuildNumber").Output()
	if errBuild == nil {
		lines := strings.Split(string(outBuild), "\n")
		for _, l := range lines {
			if strings.Contains(l, "CurrentBuildNumber") {
				fields := strings.Fields(l)
				if len(fields) >= 3 {
					buildNum = fields[2]
				}
			}
		}
	}

	displayVer := ""
	outVer, errVer := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "/v", "DisplayVersion").Output()
	if errVer == nil {
		lines := strings.Split(string(outVer), "\n")
		for _, l := range lines {
			if strings.Contains(l, "DisplayVersion") {
				fields := strings.Fields(l)
				if len(fields) >= 3 {
					displayVer = fields[2]
				}
			}
		}
	}

	if productName != "" {
		if displayVer != "" {
			distro = fmt.Sprintf("%s (%s)", productName, displayVer)
		} else {
			distro = productName
		}
	} else {
		distro = "Windows"
	}

	if buildNum != "" {
		build = "Build " + buildNum
	} else {
		if verOut, verErr := exec.Command("cmd.exe", "/c", "ver").Output(); verErr == nil {
			build = strings.TrimSpace(string(verOut))
		}
	}
	return distro, build
}

func probeLinuxOS() (distro, build string) {
	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				distro = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	if distro == "" {
		distro = "Linux"
	}

	if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		build = "Linux " + strings.TrimSpace(string(data))
	} else {
		if out, err := exec.Command("uname", "-r").Output(); err == nil {
			build = "Linux " + strings.TrimSpace(string(out))
		}
	}
	return distro, build
}

func execProbe(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func probeDarwinOS() (distro, build string) {
	distro = "macOS"
	if out, err := execProbe(1500*time.Millisecond, "sw_vers", "-productVersion"); err == nil {
		distro = "macOS " + strings.TrimSpace(string(out))
	}
	if out, err := execProbe(1500*time.Millisecond, "sw_vers", "-buildVersion"); err == nil {
		build = "Build " + strings.TrimSpace(string(out))
	}
	return distro, build
}

func probeShellVersion(shell string) string {
	switch shell {
	case "powershell":
		out, err := execProbe(1500*time.Millisecond, "powershell", "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()")
		if err == nil {
			return "PowerShell " + strings.TrimSpace(string(out))
		}
	case "bash":
		out, err := execProbe(1500*time.Millisecond, "bash", "-c", `echo "$BASH_VERSION"`)
		if err == nil {
			ver := strings.TrimSpace(string(out))
			if idx := strings.Index(ver, "("); idx != -1 {
				ver = ver[:idx]
			}
			return "Bash " + ver
		}
	case "zsh":
		out, err := execProbe(1500*time.Millisecond, "zsh", "-c", `echo "$ZSH_VERSION"`)
		if err == nil {
			return "Zsh " + strings.TrimSpace(string(out))
		}
	case "fish":
		out, err := execProbe(1500*time.Millisecond, "fish", "-c", `echo "$FISH_VERSION"`)
		if err == nil {
			return "Fish " + strings.TrimSpace(string(out))
		}
	}
	return shell
}

func probePackageManagers() []string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{"winget", "choco", "scoop"}
	case "darwin":
		candidates = []string{"brew", "port"}
	case "linux":
		candidates = []string{"apt", "dnf", "pacman", "zypper", "apk", "brew", "yum"}
	}

	var found []string
	for _, cmd := range candidates {
		if _, err := exec.LookPath(cmd); err == nil {
			found = append(found, cmd)
		}
	}
	return found
}

// FormatPromptContext formats the environment metadata along with the dynamic, live
// process elevation status into a compact, model-readable context string.
func (s *SystemContext) FormatPromptContext(isElevated bool) string {
	var envParts []string
	if s.OSDistribution != "" {
		envParts = append(envParts, s.OSDistribution)
	}
	if s.OSBuild != "" {
		envParts = append(envParts, s.OSBuild)
	}
	envParts = append(envParts, fmt.Sprintf("%s/%s", s.OSName, s.Architecture))
	if s.ShellVersion != "" {
		envParts = append(envParts, s.ShellVersion)
	} else if s.Shell != "" {
		envParts = append(envParts, s.Shell)
	}
	if len(s.PackageManagers) > 0 {
		envParts = append(envParts, "Package managers: "+strings.Join(s.PackageManagers, ", "))
	}

	envLine := "Environment: " + strings.Join(envParts, " | ")

	var privLine string
	if isElevated {
		if s.OSName == "windows" {
			privLine = "User Privilege: Running as Administrator (elevated)."
		} else {
			privLine = "User Privilege: Running as root (elevated, do NOT include 'sudo')."
		}
	} else {
		if s.OSName == "windows" {
			privLine = "User Privilege: Standard non-elevated user."
		} else {
			privLine = "User Privilege: Standard non-root user (always include 'sudo' for commands requiring root or administrative privileges, such as package management with apt/dnf/pacman, systemctl service management, mounting, or modifying /etc/)."
		}
	}

	return envLine + "\n" + privLine
}
