package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openAgi2/cccode-macbridge/core"
)

type codexPlanEntry struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

var codexTodoHomeResolver = resolveCodexHomeDir

func (a *Agent) FetchTodos(ctx context.Context, sessionID string) ([]core.Todo, error) {
	a.mu.RLock()
	home := a.codexHome
	a.mu.RUnlock()
	return fetchCodexTodos(ctx, sessionID, home)
}

func fetchCodexTodos(ctx context.Context, sessionID, explicitCodexHome string) ([]core.Todo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}

	codexHome := strings.TrimSpace(codexTodoHomeResolver(explicitCodexHome))
	if codexHome == "" {
		return nil, core.ErrNotSupported
	}

	path := findSessionFileInCodexHome(codexHome, sessionID)
	if path == "" {
		return nil, fmt.Errorf("session file not found for %s", sessionID)
	}

	todos, err := readTodosFromRollout(path)
	if err != nil {
		return nil, err
	}
	return todos, nil
}

func readTodosFromRollout(path string) ([]core.Todo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if todos, found, err := readTodosFromRolloutTail(f); err != nil {
		return nil, fmt.Errorf("read latest plan snapshot from %s: %w", path, err)
	} else if found {
		return todos, nil
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	todos, found, err := scanTodosFromRollout(f)
	if err != nil {
		return nil, fmt.Errorf("scan plan snapshots from %s: %w", path, err)
	}
	if !found {
		return []core.Todo{}, nil
	}
	return todos, nil
}

func readTodosFromRolloutTail(f *os.File) ([]core.Todo, bool, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.Size() <= 0 {
		return nil, false, nil
	}

	start := int64(0)
	if info.Size() > codexRolloutTailBytes {
		start = info.Size() - codexRolloutTailBytes
	}
	buf := make([]byte, int(info.Size()-start))
	n, err := f.ReadAt(buf, start)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	buf = buf[:n]
	if start > 0 {
		if idx := bytes.IndexByte(buf, '\n'); idx >= 0 {
			buf = buf[idx+1:]
		}
	}
	return parseTodoSnapshotFromRolloutBytes(buf)
}

func parseTodoSnapshotFromRolloutBytes(data []byte) ([]core.Todo, bool, error) {
	lines := bytes.Split(data, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		if isCodexTaskBoundary(line) {
			return []core.Todo{}, true, nil
		}
		todos, found, err := parseTodoSnapshotFromRolloutLine(line)
		if err != nil {
			return nil, false, err
		}
		if found {
			return todos, true, nil
		}
	}
	return nil, false, nil
}

func scanTodosFromRollout(r io.Reader) ([]core.Todo, bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var (
		last  []core.Todo
		found bool
	)
	for scanner.Scan() {
		if isCodexTaskBoundary(scanner.Bytes()) {
			last = []core.Todo{}
			found = true
			continue
		}
		todos, matched, err := parseTodoSnapshotFromRolloutLine(scanner.Bytes())
		if err != nil {
			return nil, false, err
		}
		if matched {
			last = todos
			found = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return last, found, nil
}

func isCodexTaskBoundary(line []byte) bool {
	if !bytes.Contains(line, []byte(`"task_`)) {
		return false
	}

	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "event_msg" {
		return false
	}
	switch entry.Payload.Type {
	case "task_started", "task_complete":
		return true
	default:
		return false
	}
}

func parseTodoSnapshotFromRolloutLine(line []byte) ([]core.Todo, bool, error) {
	if !bytes.Contains(line, []byte(`"update_plan"`)) {
		return nil, false, nil
	}

	var entry struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, false, nil
	}
	if entry.Type != "response_item" {
		return nil, false, nil
	}

	var payload struct {
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return nil, false, nil
	}
	if payload.Type != "function_call" || payload.Name != "update_plan" {
		return nil, false, nil
	}

	var args struct {
		Explanation *string          `json:"explanation"`
		Plan        []codexPlanEntry `json:"plan"`
	}
	if err := decodeCodexFunctionCallArguments(payload.Arguments, &args); err != nil {
		return nil, false, fmt.Errorf("parse update_plan arguments: %w", err)
	}
	return codexPlanEntriesToTodos(args.Plan), true, nil
}

func decodeCodexFunctionCallArguments(raw json.RawMessage, out any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return err
		}
		return json.Unmarshal([]byte(encoded), out)
	}
	return json.Unmarshal(trimmed, out)
}

func codexPlanEntriesToTodos(entries []codexPlanEntry) []core.Todo {
	todos := make([]core.Todo, 0, len(entries))
	for _, entry := range entries {
		step := strings.TrimSpace(entry.Step)
		if step == "" {
			continue
		}
		todos = append(todos, core.Todo{
			Content:  step,
			Status:   normalizeCodexTodoStatus(entry.Status),
			Priority: "normal",
		})
	}
	return todos
}

func normalizeCodexTodoStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "completed", "complete", "done", "finished":
		return "completed"
	case "inprogress", "in_progress", "in-progress", "running", "active":
		return "in_progress"
	case "cancelled", "canceled", "aborted":
		return "cancelled"
	case "pending", "queued", "todo", "not_started", "not-started":
		return "pending"
	default:
		return "pending"
	}
}
