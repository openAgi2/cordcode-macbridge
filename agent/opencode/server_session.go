package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// opencodeServerSession drives an active OpenCode turn through the managed
// `opencode serve` HTTP API + /global/event SSE, so iOS sees incremental
// message.part.delta during generation. It replaces the batch
// `opencode run --format json` CLI path for active turns when a managed server
// URL is configured (see docs/2026-07-06-codex-opencode-streaming-fix-plan.md).
//
// It reuses sseSubscriber (with a sessionID filter) for ALL event parsing,
// dedup, and turn-lifecycle translation (message.part.delta→EventText,
// session.status idle→EventResult). This session only owns: ensuring the
// server-side session exists with the right model, POSTing prompt_async, and
// best-effort abort on close. It NEVER kills `opencode serve` — that process
// is global and Swift-managed (OpenCodeManagedServer.swift).
type opencodeServerSession struct {
	a          *Agent
	baseURL    string
	authHeader string
	workDir    string

	model  atomic.Value // *ocModel
	chatID atomic.Value // string — opencode server session id (ses_...)

	sub *sseSubscriber // dedicated, session-filtered

	ctx    context.Context
	cancel context.CancelFunc
	alive  atomic.Bool
}

type ocModel struct {
	providerID string
	id         string
}

func newOpencodeServerSession(ctx context.Context, a *Agent, sessionID, providerID, modelID string) (*opencodeServerSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &opencodeServerSession{
		a:          a,
		baseURL:    a.httpBaseURL,
		authHeader: a.httpAuthHeader,
		workDir:    a.workDir,
		ctx:        sessionCtx,
		cancel:     cancel,
	}
	s.model.Store(&ocModel{providerID: providerID, id: modelID})
	if sessionID != "" && sessionID != core.ContinueSession {
		s.chatID.Store(sessionID)
	}
	s.alive.Store(true)

	// Dedicated, session-filtered SSE subscriber. For resume (chatID known) the
	// filter is set immediately; for new sessions it's "pending" (drops all)
	// until ensureServerSession resolves the id on first Send and calls
	// setSessionFilter — so no other session's events leak to iOS meanwhile.
	sub := newFilteredSSESubscriber(sessionCtx, a, s.CurrentSessionID())
	if err := sub.connect(); err != nil {
		cancel()
		return nil, fmt.Errorf("opencode server session: SSE connect: %w", err)
	}
	s.sub = sub

	// On ctx cancel (Close / parent shutdown), close the subscriber cleanly so
	// its events channel closes only after the SSE readLoop has exited.
	go func() {
		<-sessionCtx.Done()
		_ = sub.Close()
	}()
	return s, nil
}

func (s *opencodeServerSession) CurrentSessionID() string {
	if v, ok := s.chatID.Load().(string); ok {
		return v
	}
	return ""
}

func (s *opencodeServerSession) Events() <-chan core.Event { return s.sub.events }
func (s *opencodeServerSession) Alive() bool                { return s.alive.Load() }

func (s *opencodeServerSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is closed")
	}

	if len(files) > 0 {
		filePaths := core.SaveFilesToDisk(s.workDir, files)
		prompt = core.AppendFileRefs(prompt, filePaths)
	}
	prompt, _, err := stageOpencodeImages(s.workDir, prompt, images)
	if err != nil {
		return err
	}

	chatID, err := s.ensureServerSession()
	if err != nil {
		return err
	}

	// prompt_async returns 204 (accepted) and the turn runs asynchronously;
	// streaming tokens arrive over the dedicated SSE subscriber as
	// message.part.delta → EventText, and session.status idle → EventResult.
	body := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": prompt}},
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	code, raw, err := s.a.doRequest(ctx, http.MethodPost, "/session/"+chatID+"/prompt_async", body)
	if err != nil {
		return fmt.Errorf("opencode prompt_async: %w", err)
	}
	if code == 204 || code == 200 {
		return nil
	}
	return fmt.Errorf("opencode prompt_async HTTP %d: %s", code, truncate(string(raw), 300))
}

// ensureServerSession returns the server-side session id, creating the session
// (with the configured model) on first Send when no resumeID was supplied, and
// locking the SSE subscriber's filter to it.
func (s *opencodeServerSession) ensureServerSession() (string, error) {
	if id := s.CurrentSessionID(); id != "" {
		return id, nil
	}

	body := map[string]any{}
	if m, ok := s.model.Load().(*ocModel); ok && m != nil && m.id != "" {
		mbody := map[string]any{"id": m.id}
		if m.providerID != "" {
			mbody["providerID"] = m.providerID
		}
		body["model"] = mbody
	}

	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	code, raw, err := s.a.doRequest(ctx, http.MethodPost, "/session", body)
	if err != nil {
		return "", fmt.Errorf("opencode create session: %w", err)
	}
	if code >= 300 {
		return "", fmt.Errorf("opencode create session HTTP %d: %s", code, truncate(string(raw), 300))
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.ID == "" {
		return "", fmt.Errorf("opencode create session: bad response: %s", truncate(string(raw), 300))
	}
	s.chatID.Store(resp.ID)
	s.sub.setSessionFilter(resp.ID)
	return resp.ID, nil
}

func (s *opencodeServerSession) RespondPermission(requestID string, result core.PermissionResult) error {
	chatID := s.CurrentSessionID()
	if chatID == "" || requestID == "" {
		return nil
	}
	behavior := result.Behavior
	if behavior == "" {
		behavior = "deny"
	}
	body := map[string]any{"response": behavior}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, raw, err := s.a.doRequest(ctx, http.MethodPost, "/session/"+chatID+"/permissions/"+requestID, body)
	if err != nil {
		return fmt.Errorf("opencode permission reply: %w", err)
	}
	if code >= 300 {
		slog.Debug("opencode server session: permission reply non-2xx", "code", code, "body", truncate(string(raw), 200))
	}
	return nil
}

func (s *opencodeServerSession) RespondQuestion(_ string, _ []string) error {
	return fmt.Errorf("opencode server session does not support question reply")
}

func (s *opencodeServerSession) RejectQuestion(_ string) error {
	return fmt.Errorf("opencode server session does not support question reject")
}

// Close best-effort aborts the active turn and tears down the SSE subscriber.
// Uses a detached context for the abort POST because s.ctx is about to cancel.
// Never kills `opencode serve` (shared, Swift-managed).
func (s *opencodeServerSession) Close() error {
	s.alive.Store(false)
	if chatID := s.CurrentSessionID(); chatID != "" {
		actx, acancel := context.WithTimeout(context.Background(), 5*time.Second)
		code, _, err := s.a.doRequest(actx, http.MethodPost, "/session/"+chatID+"/abort", map[string]any{})
		acancel()
		if err != nil {
			slog.Debug("opencode server session: abort failed", "error", err)
		} else if code >= 300 {
			slog.Debug("opencode server session: abort non-2xx", "code", code)
		}
	}
	s.cancel() // → sub.Close() via the goroutine started in newOpencodeServerSession
	return nil
}
