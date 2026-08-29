package codexremote

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type remoteSession struct {
	agent    *Agent
	threadID string
	events   chan core.Event
	mu       sync.Mutex
	alive    bool
}

func (s *remoteSession) Send(prompt string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	s.agent.mu.Lock()
	cl := s.agent.client
	s.agent.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	params := map[string]any{
		"threadId": s.threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}
	_, rpcErr, err := cl.Request("turn/start", params)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	return nil
}

func (s *remoteSession) interrupt() error {
	return s.CancelTurn(context.Background())
}

func (s *remoteSession) CancelTurn(ctx context.Context) error {
	return s.agent.CancelTurnForThread(ctx, s.threadID)
}

func (s *remoteSession) RespondPermission(string, core.PermissionResult) error {
	return core.ErrNotSupported
}
func (s *remoteSession) Events() <-chan core.Event { return s.events }
func (s *remoteSession) CurrentSessionID() string  { return s.threadID }
func (s *remoteSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}
func (s *remoteSession) Close() error {
	s.mu.Lock()
	s.alive = false
	s.mu.Unlock()
	s.agent.dropListener(s.threadID, s.events)
	return nil
}
func (s *remoteSession) RespondQuestion(string, []string) error { return core.ErrNotSupported }
func (s *remoteSession) RejectQuestion(string) error            { return core.ErrNotSupported }

func (a *Agent) BindClient(cl *Client) {
	a.mu.Lock()
	old := a.client
	a.client = cl
	if a.codec == nil {
		a.codec = NewLiveCodec()
	}
	if a.listeners == nil {
		a.listeners = map[string]map[chan core.Event]struct{}{}
	}
	a.mu.Unlock()
	if old != nil && old != cl {
		_ = old.Close()
	}
	a.startPump(cl)
}

// startPump mirrors the official app-server-client worker invariant: unread
// events must never prevent a foreground response from being delivered. A
// pump belongs to one Client/connection epoch; replacing a Client always
// starts a new pump instead of sharing reconnect-fragile global state.
func (a *Agent) startPump(cl *Client) {
	if cl == nil {
		return
	}
	a.mu.Lock()
	codec := a.codec
	a.mu.Unlock()
	go func() {
		notifications := cl.Notifications()
		serverRequests := cl.ServerRequests()
		for notifications != nil || serverRequests != nil {
			select {
			case n, ok := <-notifications:
				if !ok {
					notifications = nil
					continue
				}
				for _, ev := range codec.Decode(n) {
					a.dispatch(ev)
				}
			case request, ok := <-serverRequests:
				if !ok {
					serverRequests = nil
					continue
				}
				// Phase 1 advertises no server-request interactions. Rejecting is
				// fail-closed and, unlike leaving the bounded queue unread, lets the
				// read loop continue to responses and liveness notifications.
				if err := cl.RejectServerRequest(request.RequestID, -32601,
					fmt.Sprintf("unsupported remote app-server request %q", request.Method)); err != nil {
					slog.Warn("codex-remote failed to reject unsupported server request",
						"method", request.Method, "error", err)
				}
			}
		}
	}()
}

func (a *Agent) dispatch(ev core.Event) {
	a.mu.Lock()
	set := a.listeners[ev.ThreadID]
	var chans []chan core.Event
	for ch := range set {
		chans = append(chans, ch)
	}
	a.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (a *Agent) addListener(threadID string, ch chan core.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.listeners == nil {
		a.listeners = map[string]map[chan core.Event]struct{}{}
	}
	set := a.listeners[threadID]
	if set == nil {
		set = map[chan core.Event]struct{}{}
		a.listeners[threadID] = set
	}
	set[ch] = struct{}{}
}

func (a *Agent) dropListener(threadID string, ch chan core.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if set := a.listeners[threadID]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(a.listeners, threadID)
		}
	}
}

func (a *Agent) CancelTurnForThread(ctx context.Context, threadID string) error {
	a.mu.Lock()
	cl := a.client
	codec := a.codec
	a.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	turnID := ""
	if codec != nil {
		turnID = codec.ActiveTurn(threadID)
	}
	if turnID == "" {
		turnID = a.inProgressTurn(ctx, threadID)
	}
	if turnID == "" {
		return fmt.Errorf("codex-remote: no active turn to interrupt")
	}
	_, rpcErr, err := cl.RequestContext(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	})
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	return nil
}

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	if sessionID == "" {
		raw, rpcErr, err := cl.RequestContext(ctx, "thread/start", map[string]any{"cwd": a.workDir})
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return nil, rpcErr
		}
		var res struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		sessionID = res.Thread.ID
	} else {
		_, rpcErr, err := cl.RequestContext(ctx, "thread/resume", map[string]any{
			"threadId":     sessionID,
			"excludeTurns": true,
		})
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return nil, rpcErr
		}
	}
	ch := make(chan core.Event, 64)
	a.addListener(sessionID, ch)
	return &remoteSession{agent: a, threadID: sessionID, events: ch, alive: true}, nil
}

var (
	_ core.TurnCanceler       = (*remoteSession)(nil)
	_ core.ThreadTurnCanceler = (*Agent)(nil)
)
