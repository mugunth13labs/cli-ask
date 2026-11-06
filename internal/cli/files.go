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
	"unicode/utf8"
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

var (
	fileRefPattern      = regexp.MustCompile(`(?:^|[\s\(\[\{"'<,])@([a-zA-Z0-9_\-\.\/\\]+)`)
	fileRefDoubleQuoted = regexp.MustCompile(`(?:^|[\s\(\[\{<,])@"((?:\\.|[^"\r\n])+)"`)
	fileRefSingleQuoted = regexp.MustCompile(`(?:^|[\s\(\[\{<,])@'((?:\\.|[^'\r\n])+)'`)
)

// ExtractFileReferences parses a prompt and extracts unique @filename tokens.
// It supports quoted paths (e.g. @"file with spaces.pdf") as well as unquoted paths.
// It ignores emails (user@domain.com) and punctuation around the token.
func ExtractFileReferences(prompt string) []string {
	seen := make(map[string]bool)
	var refs []string

	trailingTrim := func(r rune) bool {
		return r == '.' || r == ',' || r == '!' || r == '?' || r == ')' ||
			r == ']' || r == '}' || r == '"' || r == '\'' || r == ';' || r == ':'
	}

	// 1. Quoted references: @"path with spaces" or @'path with spaces'
	quotedMatches := append(
		fileRefDoubleQuoted.FindAllStringSubmatch(prompt, -1),
		fileRefSingleQuoted.FindAllStringSubmatch(prompt, -1)...,
	)
	for _, m := range quotedMatches {
		if len(m) >= 2 {
			raw := strings.TrimRightFunc(m[1], trailingTrim)
			if raw == "" {
				continue
			}
			raw = filepath.ToSlash(raw)
			if !seen[raw] {
				seen[raw] = true
				refs = append(refs, raw)
			}
		}
	}

	// 2. Unquoted references that may contain spaces if existing on disk
	cwd, _ := os.Getwd()
	runes := []rune(prompt)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' {
			continue
		}
		// Skip email addresses: preceding char is alphanumeric
		if i > 0 && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
			continue
		}
		// Skip quoted references already processed
		if i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\'' || runes[i+1] == '`') {
			continue
		}

		// Look ahead for potential multi-word path
		after := string(runes[i+1:])
		if idx := strings.IndexAny(after, "\r\n"); idx != -1 {
			after = after[:idx]
		}

		matchedPath := ""
		if cwd != "" && after != "" {
			words := strings.Fields(after)
			for k := len(words); k >= 1; k-- {
				cand := strings.Join(words[:k], " ")
				candClean := strings.TrimRightFunc(cand, trailingTrim)
				if candClean == "" {
					continue
				}
				candPath := filepath.FromSlash(candClean)
				if !filepath.IsAbs(candPath) {
					candPath = filepath.Join(cwd, candPath)
				}
				if info, err := os.Stat(candPath); err == nil && !info.IsDir() {
					matchedPath = filepath.ToSlash(candClean)
					break
				}
			}
		}

		if matchedPath != "" {
			if !seen[matchedPath] {
				seen[matchedPath] = true
				refs = append(refs, matchedPath)
			}
		}
	}

	// 3. Standard unquoted single tokens
	matches := fileRefPattern.FindAllStringSubmatch(prompt, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimRightFunc(m[1], trailingTrim)
		if raw == "" {
			continue
		}
		raw = filepath.ToSlash(raw)
		alreadyCovered := false
		for existing := range seen {
			if strings.HasPrefix(existing, raw) {
				alreadyCovered = true
				break
			}
		}
		if !alreadyCovered && !seen[raw] {
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
			replacement := "@" + att.Ref + " [truncated]"
			targets := []string{
				"@\"" + att.Ref + "\"",
				"@'" + att.Ref + "'",
				"@`" + att.Ref + "`",
				"@" + att.Ref,
			}
			replaced := false
			for _, target := range targets {
				if strings.Contains(result, target) {
					result = strings.Replace(result, target, replacement, 1)
					replaced = true
					break
				}
			}
			if !replaced && !strings.Contains(result, replacement) {
				result = strings.Replace(result, "@"+att.Ref, replacement, 1)
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

// AutocompleteMode represents whether the active completion is for files or slash commands.
type AutocompleteMode int

const (
	AutocompleteModeFiles AutocompleteMode = iota
	AutocompleteModeSlash
)

// SlashCommandItem defines a slash command and its descriptive label in the dropdown menu.
type SlashCommandItem struct {
	Name        string
	Description string
}

// DefaultSlashCommands lists the standard interactive chatbot slash commands.
var DefaultSlashCommands = []SlashCommandItem{
	{Name: "/help", Description: "Show available commands & keys"},
	{Name: "/btw", Description: "Ask a tangent with context without saving"},
	{Name: "/clear", Description: "Clear conversation history"},
	{Name: "/exit", Description: "Exit interactive chat session"},
}

func getSlashCommandDesc(name string) string {
	for _, item := range DefaultSlashCommands {
		if item.Name == name {
			return item.Description
		}
	}
	return ""
}

// ReadDirSingleLevel reads immediate entries inside relDir relative to cwd.
// Directories are returned with a trailing slash "/".
// Hidden entries and .git directories are skipped.
func ReadDirSingleLevel(cwd, relDir string) ([]string, error) {
	targetDir := cwd
	if relDir != "" && relDir != "." {
		targetDir = filepath.Join(cwd, filepath.FromSlash(relDir))
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, err
	}

	var results []string
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || (strings.HasPrefix(name, ".") && name != "." && name != "..") {
			continue
		}

		entryPath := name
		if relDir != "" && relDir != "." {
			entryPath = filepath.ToSlash(filepath.Join(relDir, name))
		}

		if entry.IsDir() {
			entryPath += "/"
		}
		results = append(results, filepath.ToSlash(entryPath))
	}
	sort.Strings(results)
	return results, nil
}

// AutocompleteState manages active file and slash command autocomplete state in prompt composers.
type AutocompleteState struct {
	Active        bool
	Mode          AutocompleteMode
	TriggerCol    int
	TriggerRow    int
	Query         string
	AllFiles      []string // Pre-seeded file list (e.g. for unit tests)
	DirCache      map[string][]string
	Candidates    []string
	SelectedIndex int
	Cwd           string
}

// NewAutocomplete returns an initialized AutocompleteState.
func NewAutocomplete() *AutocompleteState {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return &AutocompleteState{
		Active:   false,
		Mode:     AutocompleteModeFiles,
		DirCache: make(map[string][]string),
		Cwd:      cwd,
	}
}

// Trigger activates the file autocomplete menu at the specified cursor position.
func (a *AutocompleteState) Trigger(row, col int) {
	a.Active = true
	a.Mode = AutocompleteModeFiles
	a.TriggerRow = row
	a.TriggerCol = col
	a.Query = ""
	a.SelectedIndex = 0

	if len(a.AllFiles) > 0 {
		a.Candidates = a.AllFiles
		return
	}

	// Single-level onion peeling: read immediate directory entries of Cwd
	if a.Cwd == "" {
		if cwd, err := os.Getwd(); err == nil {
			a.Cwd = cwd
		} else {
			a.Cwd = "."
		}
	}
	if a.DirCache == nil {
		a.DirCache = make(map[string][]string)
	}

	entries, ok := a.DirCache["."]
	if !ok {
		var err error
		entries, err = ReadDirSingleLevel(a.Cwd, "")
		if err == nil {
			a.DirCache["."] = entries
		}
	}
	a.Candidates = entries
}

// TriggerSlash activates the slash command autocomplete menu at the specified cursor position.
func (a *AutocompleteState) TriggerSlash(row, col int) {
	a.Active = true
	a.Mode = AutocompleteModeSlash
	a.TriggerRow = row
	a.TriggerCol = col
	a.Query = ""
	a.SelectedIndex = 0

	var cands []string
	for _, item := range DefaultSlashCommands {
		cands = append(cands, item.Name)
	}
	a.Candidates = cands
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

	if a.Mode == AutocompleteModeSlash {
		qNorm := strings.ToLower(strings.TrimPrefix(q, "/"))
		var filtered []string
		for _, item := range DefaultSlashCommands {
			nameNoSlash := strings.TrimPrefix(item.Name, "/")
			if strings.HasPrefix(nameNoSlash, qNorm) || strings.Contains(nameNoSlash, qNorm) {
				filtered = append(filtered, item.Name)
			}
		}
		a.Candidates = filtered
		if a.SelectedIndex >= len(a.Candidates) {
			a.SelectedIndex = 0
		}
		return
	}

	// File mode: Pre-seeded list (backward compatibility for tests)
	if len(a.AllFiles) > 0 {
		a.Candidates = FilterFileCandidates(a.AllFiles, q)
		if a.SelectedIndex >= len(a.Candidates) {
			a.SelectedIndex = 0
		}
		return
	}

	// Progressive single-level directory peeling
	if a.Cwd == "" {
		if cwd, err := os.Getwd(); err == nil {
			a.Cwd = cwd
		} else {
			a.Cwd = "."
		}
	}
	if a.DirCache == nil {
		a.DirCache = make(map[string][]string)
	}

	cleanQ := filepath.ToSlash(q)
	lastSlash := strings.LastIndex(cleanQ, "/")
	relDir := "."
	filterPrefix := cleanQ
	if lastSlash >= 0 {
		relDir = cleanQ[:lastSlash]
		filterPrefix = cleanQ[lastSlash+1:]
	}

	entries, ok := a.DirCache[relDir]
	if !ok {
		var err error
		entries, err = ReadDirSingleLevel(a.Cwd, relDir)
		if err == nil {
			a.DirCache[relDir] = entries
		}
	}

	if filterPrefix == "" {
		a.Candidates = entries
	} else {
		lowerFilter := strings.ToLower(filterPrefix)
		var exactPrefix []string
		var contains []string
		for _, entry := range entries {
			baseName := filepath.Base(filepath.FromSlash(strings.TrimSuffix(entry, "/")))
			lowerBase := strings.ToLower(baseName)
			lowerEntry := strings.ToLower(entry)
			if strings.HasPrefix(lowerBase, lowerFilter) || strings.HasPrefix(lowerEntry, strings.ToLower(cleanQ)) {
				exactPrefix = append(exactPrefix, entry)
			} else if strings.Contains(lowerBase, lowerFilter) || strings.Contains(lowerEntry, strings.ToLower(cleanQ)) {
				contains = append(contains, entry)
			}
		}
		a.Candidates = append(exactPrefix, contains...)
	}

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

	innerW := w - 6 // leave space for "  > " (4 chars) and right margin (2 chars)
	if innerW < 10 {
		innerW = 10
	}

	var res []string

	titleKind := "files"
	if auto.Mode == AutocompleteModeSlash {
		titleKind = "commands"
	}

	// Header: ── files (X/Y) ────────────────────────────────
	headerTitle := fmt.Sprintf("── %s (%d/%d) ", titleKind, auto.SelectedIndex+1, total)
	hRunes := utf8.RuneCountInString(headerTitle)
	dashes := ""
	if hRunes < w {
		dashes = strings.Repeat("─", w-hRunes)
	}
	res = append(res, ColorCyan+headerTitle+dashes+AnsiReset)

	for i := start; i < end; i++ {
		cand := auto.Candidates[i]
		desc := ""
		if auto.Mode == AutocompleteModeSlash {
			desc = getSlashCommandDesc(cand)
		}

		display := cand
		if desc != "" && innerW > 30 {
			maxCand := 10
			candRunes := utf8.RuneCountInString(cand)
			if candRunes < maxCand {
				candPad := strings.Repeat(" ", maxCand-candRunes)
				display = cand + candPad + " " + desc
			} else {
				display = cand + " " + desc
			}
		}

		displayRunes := utf8.RuneCountInString(display)
		maxCandW := w - 4 // total space after 4-column prefix ("  > " or "    ")
		if displayRunes > maxCandW {
			runes := []rune(display)
			display = string(runes[:maxCandW-3]) + "..."
			displayRunes = utf8.RuneCountInString(display)
		}
		pad := ""
		if displayRunes < maxCandW {
			pad = strings.Repeat(" ", maxCandW-displayRunes)
		}

		if i == auto.SelectedIndex {
			// Amber highlighted item with navy background
			row := fmt.Sprintf("%s%s%s  > %s%s%s", ColorNavyBg, ColorAmber, AnsiBold, display, pad, AnsiReset)
			res = append(res, row)
		} else {
			// Bright white item with navy background
			row := fmt.Sprintf("%s%s    %s%s%s", ColorNavyBg, ColorWhite, display, pad, AnsiReset)
			res = append(res, row)
		}
	}

	// Bottom: ──────────────────────────────────────────────
	bottom := strings.Repeat("─", w)
	res = append(res, ColorCyan+bottom+AnsiReset)

	return res
}

// ApplyAutocompleteCandidate inserts an autocomplete selection into the lines buffer at triggerCol,
// returning the modified lines and new cursor column.
func ApplyAutocompleteCandidate(lines []string, cursorRow, cursorCol, triggerCol int, candidate string, isSlash bool) ([]string, int) {
	if cursorRow >= len(lines) {
		return lines, cursorCol
	}
	line := lines[cursorRow]
	lineRunes := []rune(line)
	if triggerCol > len(lineRunes) {
		triggerCol = len(lineRunes)
	}
	prefix := string(lineRunes[:triggerCol])
	suffix := ""
	if cursorCol < len(lineRunes) {
		suffix = string(lineRunes[cursorCol:])
	}

	var inserted string
	if isSlash {
		inserted = candidate + " "
	} else if strings.HasSuffix(candidate, "/") {
		inserted = "@" + candidate
		if strings.Contains(candidate, " ") {
			inserted = "@\"" + candidate + "\""
		}
	} else {
		inserted = "@" + candidate + " "
		if strings.Contains(candidate, " ") {
			inserted = "@\"" + candidate + "\" "
		}
	}

	newLines := make([]string, len(lines))
	copy(newLines, lines)
	newLines[cursorRow] = prefix + inserted + suffix
	newCol := triggerCol + utf8.RuneCountInString(inserted)
	return newLines, newCol
}

