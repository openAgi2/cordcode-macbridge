package dshweb

// dshSession implements core.AgentSession for one official dsh web session.
// Unlike the stdio route there is NO child process per session: the session
// object is a thin binding (official session id + resolved instance client);
// live events arrive through the agent-level mux stream (§8-3) and
// approvals/questions through the §8-4 responders.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.AgentSession = (*dshSession)(nil)
var _ core.TurnCanceler = (*dshSession)(nil)
var _ core.LiveModeSwitcher = (*dshSession)(nil)

type dshSession struct {
	agent   *Agent
	client  *Client
	events  chan core.Event
	ctx     context.Context
	cancel  context.CancelFunc
	closed  atomic.Bool
	idValue atomic.Value // string
}

// StartSession binds or creates one official session (design §4.3.4/§4.3.6):
//
//   - sessionID == "" → session.create{workspaceId} when the iOS-selected
//     directory matches a registered workspace (official attach only runs
//     with workspaceId); otherwise session.create{cwd}.
//   - sessionID != "" → bind the existing session (official resume semantics;
//     no guard, no session_resume_not_supported — §3.1), verified by a light
//     history probe so an unknown id fails visibly with the official
//     session-not-found text.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	s := &dshSession{
		agent:  a,
		client: client,
		events: make(chan core.Event, 64),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)

	if sessionID == "" {
		created, err := s.create(ctx)
		if err != nil {
			s.cancel()
			return nil, err
		}
		s.idValue.Store(created)
	} else {
		// Light existence probe: history on an unknown id returns the official
		// session-not-found RpcError — surfaced verbatim (坑 7). The tail page
		// also carries contextPressure/contextBreakdown — seed the meter so
		// iOS does not open on "暂无上下文用量数据".
		max := 1
		var hist sessionHistoryValue
		probe := sessionHistoryRequest{SessionID: sessionID, MaxMessages: &max}
		if err := client.Call(ctx, "session.history", probe, &hist); err != nil {
			s.cancel()
			return nil, err
		}
		s.idValue.Store(sessionID)
		if usage := usageFromProjections(hist.Projections); usage != nil {
			a.rememberContextUsage(sessionID, usage)
			select {
			case s.events <- core.Event{Type: core.EventContextUsageUpdated, SessionID: sessionID, ContextUsage: usage}:
			default:
			}
		}
	}
	a.noteActiveSession(s.CurrentSessionID())
	a.bindings.put(s.CurrentSessionID(), s)
	return s, nil
}

// create performs session.create and applies any pending model selection
// (bridge-level switch_model before the first session — the only official
// surface is session-scoped selectModel, applied right after create).
//
// Official attach (workspace.sessionIds) runs ONLY when the payload carries
// workspaceId (apiproxy session.create). cwd alone sets the session header
// directory and leaves the row in 未分组 — the design's "cwd match auto-groups"
// claim was wrong. When the iOS-selected directory matches a registered
// workspace path, send workspaceId (schema: at most one of workspaceId|cwd).
func (s *dshSession) create(ctx context.Context) (string, error) {
	var val sessionCreateValue
	req := sessionCreateRequest{}
	if cwd := s.agent.GetWorkDir(); cwd != "" && !isUngroupedDirectory(cwd) {
		if wsID := workspaceIDForDirectory(ctx, s.client, cwd); wsID != "" {
			req.WorkspaceID = wsID
		} else {
			req.Cwd = cwd
		}
	}
	if preset := strings.TrimSpace(s.agent.pendingPreset); preset != "" {
		req.AgentPreset = preset
	}
	if err := s.client.Call(ctx, "session.create", req, &val); err != nil {
		return "", err
	}
	if val.SessionID == "" {
		return "", fmt.Errorf("dshweb: session.create returned empty sessionId")
	}
	s.agent.applyPendingModelSelection(ctx, s.client, val.SessionID)
	return val.SessionID, nil
}

func (s *dshSession) CurrentSessionID() string {
	id, _ := s.idValue.Load().(string)
	return id
}

// Send queues one user turn: session.prompt{mode:"queue"} with a single text
// part (phase 1 is text-only — images ride session.attachment in phase 2;
// the bridge's attachment gate already rejects them pre-StartSession since
// this driver does not declare AttachmentSupporter).
//
// Turn events (turn/start…turn/end) arrive on Events() via the mux stream
// (§8-3). Send failures return the official RpcError text verbatim — the
// iOS send-error bubble shows the real cause (fail visibly, 坑 8).
func (s *dshSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if len(images) > 0 || len(files) > 0 {
		return fmt.Errorf("dsh-web: attachments are not supported in phase 1 (text-only)")
	}
	if s.closed.Load() {
		return fmt.Errorf("dsh web: session closed")
	}
	// Canonical-seat grace (design §12.1-2): a bound session's client bypasses
	// Resolve, so surface the grace window explicitly — handlers map this to
	// backend_unavailable, and the in-flight turn died with the instance (the
	// terminal producer closes it; reconnect re-pulls history).
	if inGrace, until := s.agent.resolver.GraceState(); inGrace {
		return &ErrInstanceReconnecting{BaseURL: s.agent.resolver.seatURL(), Until: until}
	}
	req := sessionPromptRequest{
		SessionID: s.CurrentSessionID(),
		Mode:      "queue",
		Content:   []promptContentPart{{Type: "text", Text: prompt}},
	}
	return s.client.Call(s.ctx, "session.prompt", req, nil)
}

// CancelTurn maps abort_generation onto session.cancel.
func (s *dshSession) CancelTurn(ctx context.Context) error {
	return s.client.Call(ctx, "session.cancel", sessionCancelRequest{SessionID: s.CurrentSessionID()}, nil)
}

func (s *dshSession) Events() <-chan core.Event { return s.events }

// Alive: the session binding is alive until Close. The dsh web service owns
// the real runtime; there is no local process to monitor.
func (s *dshSession) Alive() bool { return !s.closed.Load() }

func (s *dshSession) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	if id := s.CurrentSessionID(); id != "" {
		s.agent.bindings.dropIf(id, s)
	}
	s.cancel()
	// Unblock relayEvents. Leaving the channel open after Close left a
	// zombie relay on the old session object (idle TTL evicted the
	// registry entry; dsh-web idle-timeout is disabled), so the next
	// StartSession could not attach a new relay and iOS missed approvals.
	close(s.events)
	return nil
}

// emit posts one event to the session channel (used by the §8-3 mux pump).
// Drops (never blocks) when no consumer keeps up — live deltas are
// lossy-tolerant, and the projection forceCold path re-syncs from history.
func (s *dshSession) emit(ev core.Event) {
	if s.closed.Load() {
		return
	}
	if ev.SessionID == "" {
		ev.SessionID = s.CurrentSessionID()
	}
	defer func() { _ = recover() }()
	select {
	case s.events <- ev:
	default:
	}
}

// RespondPermission answers an approval request (§8-4 wires the pending
// registry; until then the request id is unknown and the error is honest).
func (s *dshSession) RespondPermission(requestID string, result core.PermissionResult) error {
	return s.agent.respondApproval(s.ctx, s.CurrentSessionID(), requestID, result)
}

// RespondQuestion answers one question of an ask batch (§8-4: per-question
// ids accumulate; the batch answers once via /api/respond when complete).
func (s *dshSession) RespondQuestion(questionID string, optionIDs []string) error {
	return s.agent.respondQuestion(s.ctx, s.CurrentSessionID(), questionID, optionIDs, "")
}

// RejectQuestion rejects: any rejected question cancels the WHOLE batch
// (error branch `cancelled` — asymmetric with approvals by design, §4.3.4).
func (s *dshSession) RejectQuestion(questionID string) error {
	return s.agent.rejectQuestion(s.ctx, s.CurrentSessionID(), questionID)
}

// sessionBindings tracks live session objects for the §8-4 surface rule
// (bridge registry hit = the surface criterion) and §8-3 routing.
type sessionBindings struct {
	mu       sync.RWMutex
	sessions map[string]*dshSession
}

func (sb *sessionBindings) put(id string, s *dshSession) {
	sb.mu.Lock()
	if sb.sessions == nil {
		sb.sessions = map[string]*dshSession{}
	}
	prev := sb.sessions[id]
	sb.sessions[id] = s
	sb.mu.Unlock()
	if prev != nil && prev != s {
		_ = prev.Close()
	}
}

func (sb *sessionBindings) drop(id string) {
	sb.mu.Lock()
	delete(sb.sessions, id)
	sb.mu.Unlock()
}

func (sb *sessionBindings) dropIf(id string, s *dshSession) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.sessions[id] == s {
		delete(sb.sessions, id)
	}
}

func (sb *sessionBindings) get(id string) (*dshSession, bool) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	s, ok := sb.sessions[id]
	return s, ok
}

// snapshot copies the live session objects for edge consumers (seat-loss
// terminal producer, design §12 item 3).
func (sb *sessionBindings) snapshot() []*dshSession {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	out := make([]*dshSession, 0, len(sb.sessions))
	for _, s := range sb.sessions {
		out = append(out, s)
	}
	return out
}

// noteActiveSession records the most recently started session id — the
// target for a bridge-level switch_model (no official backend-global write
// surface; session.selectModel is session-scoped, design §4.3.5).
func (a *Agent) noteActiveSession(id string) {
	a.mu.Lock()
	a.lastActiveSessionID = id
	a.mu.Unlock()
}
