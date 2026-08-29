package codexremote

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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

func (s *remoteSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	return s.SendWithOptions(prompt, images, files, core.PromptOptions{})
}

func (s *remoteSession) SendWithOptions(prompt string, images []core.ImageAttachment, files []core.FileAttachment, opts core.PromptOptions) error {
	if len(images) != 0 || len(files) != 0 {
		return fmt.Errorf("codex-remote: image/file turn input is not sampled by the Remote app-server (fail closed)")
	}
	if opts.Agent != "" || opts.Variant != "" {
		return fmt.Errorf("codex-remote: official turn/start does not support agent/variant overrides")
	}
	model, effort, err := s.agent.validateTurnSelection(opts)
	if err != nil {
		return err
	}
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
	if model != "" {
		params["model"] = model
	}
	if effort != "" {
		params["effort"] = effort
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

// Steer appends a text input to the currently active official turn. It is an
// optional session surface (core.AgentSession has no steer method); callers
// must use the returned turn id for subsequent control if the server changes
// it. The active id comes from the Remote event codec first, then a fresh
// thread/read, never from an envelope sequence or a locally fabricated id.
func (s *remoteSession) Steer(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("codex-remote: turn/steer text is empty")
	}
	s.agent.mu.Lock()
	cl := s.agent.client
	codec := s.agent.codec
	s.agent.mu.Unlock()
	if cl == nil {
		return "", ErrNotConfigured
	}
	turnID := ""
	if codec != nil {
		turnID = codec.ActiveTurn(s.threadID)
	}
	if turnID == "" {
		turnID = s.agent.inProgressTurn(ctx, s.threadID)
	}
	if turnID == "" {
		return "", fmt.Errorf("codex-remote: no active turn to steer")
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "turn/steer", map[string]any{
		"threadId":       s.threadID,
		"expectedTurnId": turnID,
		"input":          []map[string]string{{"type": "text", "text": prompt}},
	})
	if err != nil {
		return "", err
	}
	if rpcErr != nil {
		return "", rpcErr
	}
	var response struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("codex-remote: turn/steer decode: %w", err)
	}
	if response.TurnID != "" && codec != nil {
		codec.setActiveTurn(s.threadID, response.TurnID)
	}
	return response.TurnID, nil
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
	a.attached = map[string]*Client{}
	observed := make([]string, 0, len(a.listeners))
	for threadID := range a.listeners {
		observed = append(observed, threadID)
	}
	a.mu.Unlock()
	if old != nil && old != cl {
		_ = old.Close()
	}
	a.startPump(cl)
	// A Remote envelope has no replay cursor. Re-establish official app-server
	// thread subscriptions on the new connection; thread/resume is the source
	// operation that atomically subscribes for subsequent updates.
	for _, threadID := range observed {
		threadID := threadID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := a.attachLiveThreadOn(ctx, cl, threadID); err != nil {
				slog.Warn("codex-remote failed to restore thread subscription", "thread", threadID, "error", err)
			}
		}()
	}
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
				if isCatalogRefreshNotification(n.Method) {
					a.signalCatalogRefresh()
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

func isCatalogRefreshNotification(method string) bool {
	switch method {
	case "thread/started", "thread/name/updated", "thread/archived", "thread/unarchived", "thread/deleted",
		"turn/started", "turn/completed":
		return true
	default:
		return false
	}
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
	workDir := a.workDir
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	ch := make(chan core.Event, 64)
	if sessionID == "" {
		raw, rpcErr, err := cl.RequestContext(ctx, "thread/start", map[string]any{"cwd": workDir})
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
			Model           string  `json:"model"`
			ModelProvider   string  `json:"modelProvider"`
			ReasoningEffort *string `json:"reasoningEffort"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		sessionID = res.Thread.ID
		a.rememberSessionSelection(sessionID, res.ModelProvider, res.Model, res.ReasoningEffort)
		a.addListener(sessionID, ch)
	} else {
		// Register before thread/resume. The app-server subscribes atomically
		// during resume and may deliver the first external notification as soon
		// as its response is emitted; registering afterward leaves a race where
		// the central pump receives and drops that first frame.
		a.addListener(sessionID, ch)
		if err := a.attachLiveThreadOn(ctx, cl, sessionID); err != nil {
			a.dropListener(sessionID, ch)
			return nil, err
		}
	}
	return &remoteSession{agent: a, threadID: sessionID, events: ch, alive: true}, nil
}

// AttachLiveThread implements the same official subscription step used by
// Codex's app-server session client. Repeated observation lease renewals are
// idempotent for one Remote connection; BindClient clears the epoch-local set.
func (a *Agent) AttachLiveThread(ctx context.Context, threadID string) error {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	return a.attachLiveThreadOn(ctx, cl, threadID)
}

func (a *Agent) attachLiveThreadOn(ctx context.Context, cl *Client, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || cl == nil {
		return ErrNotConfigured
	}
	// Observation lease renewal and projection hydration may race for the same
	// thread. Serialize the check+resume transaction so the Remote app-server
	// sees one official subscription request per connection epoch.
	a.attachMu.Lock()
	defer a.attachMu.Unlock()
	a.mu.Lock()
	if a.client != cl {
		a.mu.Unlock()
		return ErrNotConfigured
	}
	if a.attached[threadID] == cl {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/resume", map[string]any{
		"threadId":     threadID,
		"excludeTurns": true,
	})
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	var response struct {
		Model           string  `json:"model"`
		ModelProvider   string  `json:"modelProvider"`
		ReasoningEffort *string `json:"reasoningEffort"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("codex-remote: thread/resume decode: %w", err)
	}
	a.mu.Lock()
	if a.client == cl {
		if a.attached == nil {
			a.attached = map[string]*Client{}
		}
		a.attached[threadID] = cl
		if a.sessionSelections == nil {
			a.sessionSelections = map[string]core.SessionModelSelection{}
		}
		selection := core.SessionModelSelection{
			Provider: strings.TrimSpace(response.ModelProvider),
			Model:    strings.TrimSpace(response.Model),
		}
		if response.ReasoningEffort != nil {
			selection.ReasoningEffort = strings.TrimSpace(*response.ReasoningEffort)
		}
		if selection.Model != "" {
			a.sessionSelections[threadID] = selection
		}
	}
	a.mu.Unlock()
	return nil
}

func (a *Agent) rememberSessionSelection(threadID, provider, model string, effort *string) {
	selection := core.SessionModelSelection{Provider: strings.TrimSpace(provider), Model: strings.TrimSpace(model)}
	if effort != nil {
		selection.ReasoningEffort = strings.TrimSpace(*effort)
	}
	if threadID == "" || selection.Model == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionSelections == nil {
		a.sessionSelections = map[string]core.SessionModelSelection{}
	}
	a.sessionSelections[threadID] = selection
}

func (a *Agent) AttachProjectionLiveSession(ctx context.Context, threadID string) (core.AgentSession, error) {
	return a.StartSession(ctx, threadID)
}

func (a *Agent) UsesPromptOptions() bool { return true }

var (
	_ core.TurnCanceler                  = (*remoteSession)(nil)
	_ core.PromptOptionsSender           = (*remoteSession)(nil)
	_ core.ThreadTurnCanceler            = (*Agent)(nil)
	_ core.ThreadLiveAttacher            = (*Agent)(nil)
	_ core.ProjectionLiveSessionAttacher = (*Agent)(nil)
	_ core.PromptOptionsAgent            = (*Agent)(nil)
)
