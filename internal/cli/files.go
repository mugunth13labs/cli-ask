package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	MaxFileSizeCeiling = 10 * 1024 * 1024 // 10MB hard ceiling
	MaxFileReadLimit   = 1 * 1024 * 1024  // 1MB read/truncation limit
)

// FileAttachment holds metadata and content for an attached file.
type FileAttachment struct {
	Ref       string // Original token referenced, e.g. "main.go" or "src/app.py"
	RelPath   string // Clean relative path
	Content   string // Code block formatted content
	Truncated bool   // Whether content exceeded 1MB
}

var fileRefPattern = regexp.MustCompile(`(?:^|[\s\(\[\{"'<,])@([a-zA-Z0-9_\-\.\/\\]+)`)

// ExtractFileReferences parses a prompt and extracts unique @filename tokens.
// It ignores emails (user@domain.com) and punctuation around the token.
func ExtractFileReferences(prompt string) []string {
	matches := fileRefPattern.FindAllStringSubmatch(prompt, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var refs []string

	trailingTrim := func(r rune) bool {
		return r == '.' || r == ',' || r == '!' || r == '?' || r == ')' ||
			r == ']' || r == '}' || r == '"' || r == '\'' || r == ';' || r == ':'
	}

	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimRightFunc(m[1], trailingTrim)
		if raw == "" {
			continue
		}
		// Normalize separators
		raw = filepath.ToSlash(raw)
		if !seen[raw] {
			seen[raw] = true
			refs = append(refs, raw)
		}
	}
	return refs
}

// isBinary inspects the first 512 bytes of data for null bytes (0x00).
func isBinary(data []byte) bool {
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	return bytes.Contains(data[:checkLen], []byte{0})
}

// ResolveFileContext reads and formats all referenced files relative to cwd.
// If any file does not exist, is binary, or exceeds 10MB, it returns a descriptive error.
func ResolveFileContext(cwd string, refs []string) ([]FileAttachment, string, error) {
	if len(refs) == 0 {
		return nil, "", nil
	}

	var attachments []FileAttachment
	var contextBuilder strings.Builder

	for _, ref := range refs {
		cleanRef := filepath.Clean(filepath.FromSlash(ref))
		fullPath := cleanRef
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(cwd, cleanRef)
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, "", fmt.Errorf("Error: File not found: @%s (in current working directory)", ref)
			}
			return nil, "", fmt.Errorf("Error reading @%s: %v", ref, err)
		}

		if info.IsDir() {
			return nil, "", fmt.Errorf("Error: @%s is a directory, not a file", ref)
		}

		if info.Size() > MaxFileSizeCeiling {
			return nil, "", fmt.Errorf("Error: @%s exceeds 10MB limit (%d bytes)", ref, info.Size())
		}

		file, err := os.Open(fullPath)
		if err != nil {
			return nil, "", fmt.Errorf("Error opening @%s: %v", ref, err)
		}

		// Read up to 1MB + 1 byte to detect truncation
		readBuf := make([]byte, MaxFileReadLimit+1)
		n, readErr := io.ReadFull(file, readBuf)
		_ = file.Close()

		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, "", fmt.Errorf("Error reading content of @%s: %v", ref, readErr)
		}

		data := readBuf[:n]
		if isBinary(data) {
			return nil, "", fmt.Errorf("Error: @%s appears to be a binary file", ref)
		}

		truncated := false
		if n > MaxFileReadLimit {
			data = data[:MaxFileReadLimit]
			truncated = true
		}

		ext := strings.TrimPrefix(filepath.Ext(cleanRef), ".")
		if ext == "" {
			ext = "text"
		}

		fences := "```"
		var fileBlock strings.Builder
		fileBlock.WriteString("\n\n---\n[File: " + filepath.ToSlash(cleanRef) + "]\n" + fences + ext + "\n")
		fileBlock.WriteString(string(data))
		if truncated {
			fileBlock.WriteString("\n\n[...truncated at 1MB...]")
		}
		fileBlock.WriteString("\n" + fences)

		attachment := FileAttachment{
			Ref:       ref,
			RelPath:   filepath.ToSlash(cleanRef),
			Content:   fileBlock.String(),
			Truncated: truncated,
		}
		attachments = append(attachments, attachment)
		contextBuilder.WriteString(fileBlock.String())
	}

	return attachments, contextBuilder.String(), nil
}

// FormatHistoryPrompt returns a version of originalPrompt where any truncated
// attachments are noted as "@filename [truncated]".
func FormatHistoryPrompt(originalPrompt string, attachments []FileAttachment) string {
	result := originalPrompt
	for _, att := range attachments {
		if att.Truncated {
			// Replace @ref with @ref [truncated]
			target := "@" + att.Ref
			replacement := "@" + att.Ref + " [truncated]"
			if !strings.Contains(result, replacement) {
				result = strings.Replace(result, target, replacement, 1)
			}
		}
	}
	return result
}

// ScanWorkspaceFiles recursively scans cwd and returns relative paths to all files.
// It skips .git directories and hidden subfolders.
func ScanWorkspaceFiles(cwd string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" {
				return filepath.SkipDir
			}
			// Skip other hidden directories if needed, but allow normal folders
			if strings.HasPrefix(name, ".") && path != cwd {
				return filepath.SkipDir
			}
			return nil
		}
		// Regular file
		rel, relErr := filepath.Rel(cwd, path)
		if relErr == nil && rel != "." {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// FilterFileCandidates filters candidate files matching a search query.
func FilterFileCandidates(candidates []string, query string) []string {
	if query == "" {
		return candidates
	}
	q := strings.ToLower(query)
	var exactPrefix []string
	var contains []string

	for _, c := range candidates {
		lower := strings.ToLower(c)
		base := strings.ToLower(filepath.Base(c))
		if strings.HasPrefix(base, q) || strings.HasPrefix(lower, q) {
			exactPrefix = append(exactPrefix, c)
		} else if strings.Contains(lower, q) {
			contains = append(contains, c)
		}
	}

	return append(exactPrefix, contains...)
}

// IsPathDelimiter returns true if rune indicates word boundary for @ references
func IsPathDelimiter(r rune) bool {
	return unicode.IsSpace(r) || r == '\n' || r == '\r' || r == '\t' || r == '"' || r == '\'' || r == '`'
}

// AutocompleteState manages active file autocomplete state in prompt composers.
type AutocompleteState struct {
	Active        bool
	TriggerCol    int
	TriggerRow    int
	Query         string
	AllFiles      []string
	Candidates    []string
	SelectedIndex int
}

// NewAutocomplete returns an initialized AutocompleteState.
func NewAutocomplete() *AutocompleteState {
	return &AutocompleteState{
		Active: false,
	}
}

// Trigger activates the autocomplete menu at the specified cursor position.
func (a *AutocompleteState) Trigger(row, col int) {
	if len(a.AllFiles) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		files, err := ScanWorkspaceFiles(cwd)
		if err == nil {
			a.AllFiles = files
		}
	}
	a.Active = true
	a.TriggerRow = row
	a.TriggerCol = col
	a.Query = ""
	a.Candidates = a.AllFiles
	a.SelectedIndex = 0
}

// Dismiss closes the autocomplete menu.
func (a *AutocompleteState) Dismiss() {
	a.Active = false
	a.Query = ""
	a.Candidates = nil
	a.SelectedIndex = 0
}

// UpdateQuery filters candidates by search query.
func (a *AutocompleteState) UpdateQuery(q string) {
	a.Query = q
	a.Candidates = FilterFileCandidates(a.AllFiles, q)
	if a.SelectedIndex >= len(a.Candidates) {
		a.SelectedIndex = 0
	}
}

// Next moves selection down.
func (a *AutocompleteState) Next() {
	if len(a.Candidates) > 0 && a.SelectedIndex < len(a.Candidates)-1 {
		a.SelectedIndex++
	}
}

// Prev moves selection up.
func (a *AutocompleteState) Prev() {
	if a.SelectedIndex > 0 {
		a.SelectedIndex--
	}
}

// SelectedCandidate returns currently highlighted candidate.
func (a *AutocompleteState) SelectedCandidate() string {
	if a.Active && len(a.Candidates) > 0 && a.SelectedIndex < len(a.Candidates) {
		return a.Candidates[a.SelectedIndex]
	}
	return ""
}

// FormatDropdownLines renders an autocomplete dropdown box.
func FormatDropdownLines(auto *AutocompleteState, boxWidth int) []string {
	if !auto.Active || len(auto.Candidates) == 0 {
		return nil
	}

	w := boxWidth
	if w < 30 {
		w = 30
	}
	if w > 100 {
		w = 100
	}

	const maxItems = 5
	total := len(auto.Candidates)

	start := 0
	if auto.SelectedIndex >= maxItems {
		start = auto.SelectedIndex - (maxItems - 1)
	}
	end := start + maxItems
	if end > total {
		end = total
	}

	cyan := "\x1b[38;5;39m"
	amber := "\x1b[38;5;214m"
	white := "\x1b[97m"
	navyBg := "\x1b[48;5;17m"
	reset := "\x1b[0m"

	innerW := w - 4 // between │ and │ (2 spaces on borders)
	if innerW < 10 {
		innerW = 10
	}

	var res []string

	// Header: ┌─ files (X/Y) ───┐
	headerTitle := fmt.Sprintf("┌─ files (%d/%d) ", auto.SelectedIndex+1, total)
	hRunes := utf8RuneCount(headerTitle)
	dashes := ""
	if hRunes < w-1 {
		dashes = strings.Repeat("─", (w-1)-hRunes)
	}
	res = append(res, cyan+headerTitle+dashes+"┐"+reset)

	for i := start; i < end; i++ {
		cand := auto.Candidates[i]
		candRunes := []rune(cand)
		maxCandW := innerW - 3 // leave room for ' > ' or '   '
		if len(candRunes) > maxCandW {
			cand = "..." + string(candRunes[len(candRunes)-(maxCandW-3):])
		}
		candRunes = []rune(cand)
		pad := ""
		if len(candRunes) < maxCandW {
			pad = strings.Repeat(" ", maxCandW-len(candRunes))
		}

		if i == auto.SelectedIndex {
			// Amber highlighted item with navy background
			row := fmt.Sprintf("%s│%s%s > %s%s %s%s│%s", cyan, reset, navyBg+amber+"\x1b[1m", cand, pad, reset, cyan, reset)
			res = append(res, row)
		} else {
			// Bright white item with navy background
			row := fmt.Sprintf("%s│%s%s   %s%s %s%s│%s", cyan, reset, navyBg+white, cand, pad, reset, cyan, reset)
			res = append(res, row)
		}
	}

	// Bottom: └───────────────┘
	bottom := fmt.Sprintf("└%s┘", strings.Repeat("─", w-2))
	res = append(res, cyan+bottom+reset)

	return res
}

func utf8RuneCount(s string) int {
	return len([]rune(s))
}

