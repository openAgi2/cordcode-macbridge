package codexremote

// mutations.go maps the bridge's session mutation interfaces directly onto
// the official Codex app-server v2 thread methods. Rename and archive re-read
// the thread after the mutation; requested values are never treated as truth.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (a *Agent) FetchSessionInfo(ctx context.Context, sessionID string) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("codex-remote: get session: empty session id")
	}
	thread, err := a.readThreadWithTurns(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	info := mapRemoteThreadInfo(thread)
	return &info, nil
}

func (a *Agent) RenameSession(ctx context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID == "" {
		return nil, fmt.Errorf("codex-remote: rename session: empty session id")
	}
	if title == "" {
		return nil, fmt.Errorf("codex-remote: rename session: empty title")
	}
	if err := a.requestVoidThreadOperation(ctx, "thread/name/set", map[string]string{
		"threadId": sessionID,
		"name":     title,
	}); err != nil {
		return nil, err
	}
	return a.FetchSessionInfo(ctx, sessionID)
}

func (a *Agent) ArchiveSession(ctx context.Context, sessionID string, _ time.Time) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("codex-remote: archive session: empty session id")
	}
	if err := a.requestVoidThreadOperation(ctx, "thread/archive", map[string]string{"threadId": sessionID}); err != nil {
		return nil, err
	}
	return a.FetchSessionInfo(ctx, sessionID)
}

func (a *Agent) DeleteSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("codex-remote: delete session: empty session id")
	}
	if err := a.requestVoidThreadOperation(ctx, "thread/delete", map[string]string{"threadId": sessionID}); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.sessionSelections, sessionID)
	a.mu.Unlock()
	return nil
}

func (a *Agent) requestVoidThreadOperation(ctx context.Context, method string, params any) error {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	raw, rpcErr, err := cl.RequestContext(ctx, method, params)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	var result map[string]json.RawMessage
	if len(raw) == 0 {
		return fmt.Errorf("codex-remote: %s empty response", method)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("codex-remote: %s response decode: %w", method, err)
	}
	return nil
}

func mapRemoteThreadInfo(thread *remoteThread) core.AgentSessionInfo {
	if thread == nil {
		return core.AgentSessionInfo{}
	}
	summary := thread.Preview
	if thread.Name != nil {
		summary = *thread.Name
	}
	info := core.AgentSessionInfo{
		ID:        thread.ID,
		Summary:   summary,
		Directory: thread.Cwd,
	}
	if thread.UpdatedAt > 0 {
		info.ModifiedAt = time.Unix(thread.UpdatedAt, 0).UTC()
	}
	return info
}
