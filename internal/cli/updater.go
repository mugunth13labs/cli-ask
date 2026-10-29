package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type GitHubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type GitHubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []GitHubReleaseAsset `json:"assets"`
}

func selfUpdate() error {
	repo := "mugunth13labs/cli-ask"
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	fmt.Fprintln(os.Stderr, "Checking for latest release...")
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ask-cli")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error checking for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API error: HTTP %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse release info: %w", err)
	}

	if normalizeVersion(release.TagName) == normalizeVersion(version) {
		fmt.Printf("ask is already up to date (v%s).\n", version)
		return nil
	}

	// Determine matching asset name for current OS and architecture
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	expectedAsset := fmt.Sprintf("ask-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == expectedAsset {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no matching binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("unable to resolve symlink: %w", err)
	}

	fmt.Printf("Updating ask: v%s -> %s...\n", version, release.TagName)
	fmt.Printf("Downloading %s...\n", downloadURL)

	dlResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", dlResp.StatusCode)
	}

	execDir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(execDir, "ask-update-*"+ext)
	if err != nil {
		return fmt.Errorf("failed to create temporary update file (check directory write permissions): %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, dlResp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write update file: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic replacement: on Windows rename existing running binary first, then move new one
	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("failed to rename current executable: %w", err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			_ = os.Rename(oldPath, execPath) // rollback
			return fmt.Errorf("failed to replace executable: %w", err)
		}
		_ = os.Remove(oldPath)
	} else {
		// Unix atomic swap
		if err := os.Rename(tmpPath, execPath); err != nil {
			return fmt.Errorf("failed to replace executable at %s (try running with sudo?): %w", execPath, err)
		}
	}

	fmt.Printf("Successfully updated ask to %s!\n", release.TagName)
	return nil
}

func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	return strings.Join(parts, ".")
}
