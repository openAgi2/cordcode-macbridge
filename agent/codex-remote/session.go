package codexremote

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

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
	s.agent.mu.Lock()
	cl := s.agent.client
	codec := s.agent.codec
	s.agent.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	turnID := codec.ActiveTurn(s.threadID)
	if turnID == "" {
		return fmt.Errorf("codex-remote: no active turn to interrupt")
	}
	_, rpcErr, err := cl.Request("turn/interrupt", map[string]any{
		"threadId": s.threadID,
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
	a.client = cl
	if a.codec == nil {
		a.codec = NewLiveCodec()
	}
	if a.listeners == nil {
		a.listeners = map[string]map[chan core.Event]struct{}{}
	}
	a.mu.Unlock()
	a.ensurePump()
}

func (a *Agent) ensurePump() {
	a.mu.Lock()
	cl := a.client
	if cl == nil || a.pumpRunning {
		a.mu.Unlock()
		return
	}
	a.pumpRunning = true
	codec := a.codec
	a.mu.Unlock()
	go func() {
		for n := range cl.Notifications() {
			for _, ev := range codec.Decode(n) {
				a.dispatch(ev)
			}
		}
		a.mu.Lock()
		a.pumpRunning = false
		a.mu.Unlock()
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

func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/list", map[string]any{
		"limit":         50,
		"sortKey":       "recency_at",
		"sortDirection": "desc",
	})
	if err != nil {
		return nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	var parsed struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			UpdatedAt int64  `json:"updatedAt"`
			Cwd       string `json:"cwd"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]core.AgentSessionInfo, 0, len(parsed.Data))
	for _, row := range parsed.Data {
		info := core.AgentSessionInfo{ID: row.ID, Summary: row.Name, Directory: row.Cwd}
		if row.UpdatedAt > 0 {
			info.ModifiedAt = time.Unix(row.UpdatedAt, 0)
		}
		out = append(out, info)
	}
	return out, nil
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
