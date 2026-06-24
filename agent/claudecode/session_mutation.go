package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const claudeSessionMetaDirName = ".cc-connect-session-meta"

type claudeSessionMetadata struct {
	Title        string
	MessageCount int
	ArchivedAt   time.Time
}

type claudeSessionSidecar struct {
	ArchivedAtMillis int64 `json:"archivedAtMillis,omitempty"`
}

func (a *Agent) RenameSession(ctx context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("session title cannot be empty")
	}

	projectDir, sessionPath, err := a.resolveClaudeSessionPath(sessionID)
	if err != nil {
		return nil, err
	}
	if err := appendJSONLRecord(sessionPath, map[string]any{
		"type":        "custom-title",
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
		"sessionId":   sessionID,
		"customTitle": title,
	}); err != nil {
		return nil, err
	}

	info, err := os.Stat(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("claudecode: stat session file: %w", err)
	}
	sessionInfo, err := a.buildClaudeSessionInfo(projectDir, sessionID, sessionPath, info.ModTime())
	if err != nil {
		return nil, err
	}
	return &sessionInfo, nil
}

func (a *Agent) ArchiveSession(ctx context.Context, sessionID string, archivedAt time.Time) (*core.AgentSessionInfo, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	}

	projectDir, sessionPath, err := a.resolveClaudeSessionPath(sessionID)
	if err != nil {
		return nil, err
	}
	if err := writeClaudeSessionSidecar(projectDir, sessionID, claudeSessionSidecar{
		ArchivedAtMillis: archivedAt.UTC().UnixMilli(),
	}); err != nil {
		return nil, err
	}

	info, err := os.Stat(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("claudecode: stat session file: %w", err)
	}
	sessionInfo, err := a.buildClaudeSessionInfo(projectDir, sessionID, sessionPath, info.ModTime())
	if err != nil {
		return nil, err
	}
	if sessionInfo.ArchivedAt.IsZero() {
		sessionInfo.ArchivedAt = archivedAt.UTC()
	}
	if sessionInfo.ArchivedAt.After(sessionInfo.ModifiedAt) {
		sessionInfo.ModifiedAt = sessionInfo.ArchivedAt
	}
	return &sessionInfo, nil
}

func (a *Agent) resolveClaudeProjectDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudecode: cannot determine home dir: %w", err)
	}
	absWorkDir, err := filepath.Abs(a.workDir)
	if err != nil {
		return "", fmt.Errorf("claudecode: resolve work_dir: %w", err)
	}
	projectDir := findProjectDir(homeDir, absWorkDir)
	if projectDir == "" {
		return "", fmt.Errorf("claudecode: project dir not found")
	}
	return projectDir, nil
}

func (a *Agent) resolveClaudeSessionPath(sessionID string) (projectDir string, sessionPath string, err error) {
	projectDir, err = a.resolveClaudeProjectDir()
	if err != nil {
		return "", "", err
	}
	sessionPath = filepath.Join(projectDir, sessionID+".jsonl")
	if _, err := os.Stat(sessionPath); err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("session file not found: %s", sessionID)
		}
		return "", "", fmt.Errorf("claudecode: stat session file %q: %w", sessionPath, err)
	}
	return projectDir, sessionPath, nil
}

func (a *Agent) buildClaudeSessionInfo(projectDir, sessionID, sessionPath string, modifiedAt time.Time) (core.AgentSessionInfo, error) {
	meta, err := scanClaudeSessionMeta(sessionPath, projectDir, sessionID)
	if err != nil {
		return core.AgentSessionInfo{}, err
	}
	if meta.ArchivedAt.After(modifiedAt) {
		modifiedAt = meta.ArchivedAt
	}
	return core.AgentSessionInfo{
		ID:           sessionID,
		Summary:      meta.Title,
		MessageCount: meta.MessageCount,
		ModifiedAt:   modifiedAt,
		ArchivedAt:   meta.ArchivedAt,
	}, nil
}

func scanClaudeSessionMeta(path, projectDir, sessionID string) (claudeSessionMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return claudeSessionMetadata{}, fmt.Errorf("claudecode: open session file: %w", err)
	}
	defer f.Close()

	scanner := newTranscriptScanner(f)
	assistantIDs := make(map[string]struct{})
	var title string
	var summary string
	userCount := 0
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		var entry struct {
			Type        string                    `json:"type"`
			CustomTitle string                    `json:"customTitle"`
			Message     *transcriptHistoryMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type == "custom-title" {
			trimmed := strings.TrimSpace(entry.CustomTitle)
			if trimmed != "" {
				title = trimmed
			}
			continue
		}
		if entry.Message == nil {
			continue
		}
		switch entry.Type {
		case "assistant":
			messageID := strings.TrimSpace(entry.Message.ID)
			if messageID == "" {
				messageID = fmt.Sprintf("assistant-line-%d", lineNo)
			}
			if _, exists := assistantIDs[messageID]; exists {
				continue
			}
			assistantIDs[messageID] = struct{}{}
		case "user":
			visibleText := strings.TrimSpace(extractTextContent(entry.Message.Content))
			if visibleText == "" {
				continue
			}
			userCount++
			summary = visibleText
		default:
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return claudeSessionMetadata{}, fmt.Errorf("claudecode: scan session file: %w", err)
	}

	if title == "" {
		title = stripXMLTags(summary)
		title = strings.TrimSpace(title)
		if utf8.RuneCountInString(title) > 40 {
			title = string([]rune(title)[:40]) + "..."
		}
	}
	if title == "" {
		title = sessionID
	}

	archivedAt, err := readClaudeSessionArchivedAt(projectDir, sessionID)
	if err != nil {
		return claudeSessionMetadata{}, err
	}

	return claudeSessionMetadata{
		Title:        title,
		MessageCount: len(assistantIDs) + userCount,
		ArchivedAt:   archivedAt,
	}, nil
}

func appendJSONLRecord(path string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("claudecode: marshal session mutation record: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("claudecode: open session file for append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("claudecode: append session record: %w", err)
	}
	return nil
}

func claudeSessionSidecarPath(projectDir, sessionID string) string {
	return filepath.Join(projectDir, claudeSessionMetaDirName, sessionID+".json")
}

func readClaudeSessionArchivedAt(projectDir, sessionID string) (time.Time, error) {
	path := claudeSessionSidecarPath(projectDir, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("claudecode: read session meta: %w", err)
	}
	var meta claudeSessionSidecar
	if err := json.Unmarshal(data, &meta); err != nil {
		return time.Time{}, fmt.Errorf("claudecode: decode session meta: %w", err)
	}
	if meta.ArchivedAtMillis <= 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(meta.ArchivedAtMillis).UTC(), nil
}

func writeClaudeSessionSidecar(projectDir, sessionID string, meta claudeSessionSidecar) error {
	dir := filepath.Join(projectDir, claudeSessionMetaDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("claudecode: create session meta dir: %w", err)
	}
	path := claudeSessionSidecarPath(projectDir, sessionID)
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("claudecode: marshal session meta: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("claudecode: write session meta tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("claudecode: replace session meta: %w", err)
	}
	return nil
}

func removeClaudeSessionSidecar(projectDir, sessionID string) error {
	path := claudeSessionSidecarPath(projectDir, sessionID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("claudecode: remove session meta: %w", err)
	}
	return nil
}

var _ core.SessionRenamer = (*Agent)(nil)
var _ core.SessionArchiver = (*Agent)(nil)
