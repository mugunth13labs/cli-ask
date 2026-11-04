package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Compaction thresholds
	maxRecentMessages = 6    // keep last 6 messages (3 conversation turns) verbatim
	maxTotalMessages  = 12   // trigger compaction when exceeding this count
	maxCompactChars   = 8000 // trigger compaction if character count exceeds ~8k
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Session struct {
	ID        string    `json:"id"`
	Messages  []Message `json:"messages"`
	UpdatedAt time.Time `json:"updated_at"`
}

func getSessionsDir() (string, error) {
	dir, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

func getSessionID() string {
	ppid := os.Getppid()
	return fmt.Sprintf("session-%d", ppid)
}

func getSessionPath(sessionID string) (string, error) {
	sDir, err := getSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sDir, sessionID+".json"), nil
}

func loadSession(sessionID string) (*Session, error) {
	s := &Session{
		ID:        sessionID,
		Messages:  []Message{},
		UpdatedAt: time.Now(),
	}

	sPath, err := getSessionPath(sessionID)
	if err != nil {
		return s, err
	}

	data, err := os.ReadFile(sPath)
	if err != nil {
		return s, nil
	}

	if err := json.Unmarshal(data, s); err != nil {
		return s, nil
	}

	return s, nil
}

func saveSession(s *Session) error {
	sDir, err := getSessionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sDir, 0700); err != nil {
		return err
	}

	sPath, err := getSessionPath(s.ID)
	if err != nil {
		return err
	}

	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sPath, data, 0600)
}

func clearSession(sessionID string) error {
	sPath, err := getSessionPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(sPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func compactHistory(messages []Message) []Message {
	if len(messages) <= maxRecentMessages {
		return messages
	}

	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}

	if len(messages) < maxTotalMessages && totalChars < maxCompactChars {
		return messages
	}

	splitIdx := len(messages) - maxRecentMessages
	older := messages[:splitIdx]
	recent := messages[splitIdx:]

	var summaryBuilder strings.Builder
	summaryBuilder.WriteString("[Prior conversation context summary:\n")
	for _, m := range older {
		prefix := "User"
		if m.Role == "assistant" {
			prefix = "Assistant"
		}
		content := strings.TrimSpace(m.Content)
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")
		summaryBuilder.WriteString(fmt.Sprintf("- %s: %s\n", prefix, content))
	}
	summaryBuilder.WriteString("]")

	compactedMsg := Message{
		Role:    "system",
		Content: summaryBuilder.String(),
	}

	result := make([]Message, 0, len(recent)+1)
	result = append(result, compactedMsg)
	result = append(result, recent...)
	return result
}
