package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// todos.go implements the C6 §6.9 Todo surface from the A8 evidence:
//
//	GET /session/{id}/todo → ordered replacement list, items exactly
//	{content, status, priority} — NO server item ids exist.
//	SSE todo.updated → the same replacement shape.
//
// Ownership: todos are an explicit raw control-plane exception. Server order
// and fields are preserved verbatim; no ids are synthesized (no random/hash
// identity — A8 proves none exists), and the list never enters the
// SessionProjection timeline.

// rememberTodos stores the last observed replacement list (control plane).
func (a *Agent) rememberTodos(sessionID string, todos []core.Todo) {
	a.todoMu.Lock()
	defer a.todoMu.Unlock()
	if a.lastTodos == nil {
		a.lastTodos = make(map[string][]core.Todo)
	}
	a.lastTodos[sessionID] = todos
}

// FetchTodos implements core.TodoProvider: the official endpoint is the only
// read truth (A8). The live todo.updated snapshot exists for the SSE control
// plane only — it must never impersonate a fresh endpoint answer (a serve-
// side clear would otherwise resurrect dead items). Malformed rows fail
// loudly — content is never invented.
func (a *Agent) FetchTodos(ctx context.Context, sessionID string) ([]core.Todo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("opencode-web: fetch todos: empty session id")
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.fetchJSON(ctx, c.apiPath("/session/"+sessionID+"/todo"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("opencode-web: todo payload must be a bare array (generation-118 verified shape), got: %s", truncateForError(string(raw)))
	}
	todos, err := decodeTodoRows(raw)
	if err != nil {
		return nil, err
	}
	return todos, nil
}

// decodeTodoRows strictly parses the A8-proven replacement list: a bare
// array whose every row carries EXACTLY the verified {content,status,
// priority} truth — each field present, string-typed, non-empty. No `text`
// alias, no pending/normal defaults, no silent row skip: one malformed row
// fails the whole replacement (audit-008 W2.1).
func decodeTodoRows(raw []byte) ([]core.Todo, error) {
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("opencode-web: todo payload must be a bare array (generation-118 verified shape), got: %s", truncateForError(string(raw)))
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode-web: todo payload malformed: %w", err)
	}
	todos := make([]core.Todo, 0, len(rows))
	for i, row := range rows {
		if row == nil {
			return nil, fmt.Errorf("opencode-web: todo row %d malformed: not an object", i)
		}
		content, ok := row["content"].(string)
		if !ok || strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("opencode-web: todo row %d missing required content", i)
		}
		status, ok := row["status"].(string)
		if !ok || status == "" {
			return nil, fmt.Errorf("opencode-web: todo row %d missing required status", i)
		}
		priority, ok := row["priority"].(string)
		if !ok || priority == "" {
			return nil, fmt.Errorf("opencode-web: todo row %d missing required priority", i)
		}
		// Directive-010 tail: the verified row is EXACTLY
		// {content,status,priority} — an extra or alias key (e.g. a `text`
		// alias next to a real content) fails the whole replacement too; the
		// last-known snapshot stays untouched.
		if len(row) != 3 {
			keys := make([]string, 0, len(row))
			for key := range row {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			return nil, fmt.Errorf("opencode-web: todo row %d carries keys beyond the verified {content,status,priority} (alias/unknown keys rejected): %v", i, keys)
		}
		todos = append(todos, core.Todo{Content: content, Status: status, Priority: priority})
	}
	return todos, nil
}

var _ core.TodoProvider = (*Agent)(nil)
