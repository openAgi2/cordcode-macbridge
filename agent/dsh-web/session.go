package dshweb

// dshSession implements core.AgentSession for one official dsh web session.
// Unlike the stdio route there is NO child process per session: the session
// object is a thin binding (official session id + resolved instance client);
// live events arrive through the agent-level mux stream (§8-3) and
// approvals/questions through the §8-4 responders.

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dshLeafName mirrors the official attachment displayName rule
// (attachment-local/src/store.ts:29-36): take the leaf after the last
// separator of either style, strip control characters, trim, cap at 255;
// empty becomes "" (omitted on the wire). Keeps local paths off the wire —
// the seat applies the same rule server-side.
func dshLeafName(name string) string {
	leaf := name
	if i := strings.LastIndexAny(leaf, `/\\`); i >= 0 && i+1 < len(leaf) {
		leaf = leaf[i+1:]
	} else if i >= 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range leaf {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

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
		// Light existence probe: session/projections on an unknown id returns
		// the official session/not-found RpcError — surfaced verbatim (坑 7).
		// The block also carries contextPressure/contextBreakdown — seed the
		// meter so iOS does not open on "暂无上下文用量数据".
		var proj sessionProjectionsValue
		probe := sessionProjectionsRequest{SessionID: sessionID}
		if err := client.Call(ctx, "session/projections", map[string]any{"request": probe}, &proj); err != nil {
			s.cancel()
			return nil, err
		}
		s.idValue.Store(sessionID)
		block := apiSessionProjectionsBlock{AsOfSeq: proj.AsOfSeq, Values: proj.Values}
		if usage := usageFromProjections(&block); usage != nil {
			a.rememberContextUsage(sessionID, usage)
			select {
			case s.events <- core.Event{Type: core.EventContextUsageUpdated, SessionID: sessionID, ContextUsage: usage}:
			default:
			}
		}
	}
	a.noteActiveSession(s.CurrentSessionID())
	a.bindings.put(s.CurrentSessionID(), s)
	// Bound sessions join the live follow set (streams.go): their external
	// turns must stream without waiting for an api-session/activity poke.
	a.ensureFollow(s.CurrentSessionID())
	return s, nil
}

// create performs session.create and applies any pending model selection
// (bridge-level switch_model before the first session — the only official
// surface is session-scoped selectModel, applied right after create).
//
// Official attach (workspace.sessionIds) runs ONLY when the payload carries
// workspaceId (session/create schema). cwd alone sets the session header
// directory and leaves the row in 未分组 — the design's "cwd match
// auto-groups" claim was wrong. When the iOS-selected directory matches a
// registered workspace path (workspace/follow baseline), send workspaceId
// (schema: at most one of workspaceId|cwd).
func (s *dshSession) create(ctx context.Context) (string, error) {
	var val sessionCreateValue
	req := sessionCreateRequest{}
	if cwd := s.agent.GetWorkDir(); cwd != "" && !isUngroupedDirectory(cwd) {
		if wsID := s.agent.workspaceIDForDirectory(cwd); wsID != "" {
			req.WorkspaceID = wsID
		} else {
			req.Cwd = cwd
		}
	}
	if preset := strings.TrimSpace(s.agent.pendingPreset); preset != "" {
		req.AgentPreset = preset
	}
	if err := s.client.Call(ctx, "session/create", map[string]any{"request": req}, &val); err != nil {
		return "", err
	}
	if val.SessionID == "" {
		return "", fmt.Errorf("dshweb: session/create returned empty sessionId")
	}
	s.agent.applyPendingModelSelection(ctx, s.client, val.SessionID)
	return val.SessionID, nil
}

func (s *dshSession) CurrentSessionID() string {
	id, _ := s.idValue.Load().(string)
	return id
}

// Send queues one user turn with the default delivery mode (queue). Kept for
// the plain AgentSession surface; the bridge's send path routes through
// SendWithOptions (PromptOptionsSender).
func (s *dshSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	return s.SendWithOptions(prompt, images, files, core.PromptOptions{})
}

// SendWithOptions carries the official prompt delivery mode per request
// (OD-2a=A, A7 live evidence): "" or "queue" → mode:"queue" (the official
// composer default — busy-Enter stays queue, submission-policy.ts:29-38 +
// DEFAULT_BUSY_ENTER_BEHAVIOR='queue'); "steer" → mode:"steer" (the explicit
// steering gesture: agent.steer splices into the running turn's next-step,
// agent-loop/agent.ts:166; on an idle agent the wake makes it the next
// turn — live-confirmed accepted, NOT the source-mapped agent-busy
// rejection). Other option axes (agent/model/variant/effort) are applied
// agent-global by the bridge before this call and are not re-carried here.
func (s *dshSession) SendWithOptions(prompt string, images []core.ImageAttachment, files []core.FileAttachment, opts core.PromptOptions) error {
	mode := strings.TrimSpace(opts.Mode)
	switch mode {
	case "", "queue":
		mode = "queue"
	case "steer":
		// official gesture
	default:
		return fmt.Errorf("dsh-web: unsupported prompt mode %q (want queue|steer)", opts.Mode)
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
	content := make([]promptContentPart, 0, 1+len(images)+len(files))
	if prompt != "" {
		content = append(content, promptContentPart{Type: "text", Text: prompt})
	}
	for _, img := range images {
		content = append(content, promptContentPart{
			Type:      "image",
			MediaType: img.MimeType,
			Data:      base64.StdEncoding.EncodeToString(img.Data),
			Name:      dshLeafName(img.FileName),
		})
	}
	for _, f := range files {
		uploaded, err := s.client.UploadFileBinary(s.ctx, s.CurrentSessionID(),
			dshLeafName(f.FileName), f.Data)
		if err != nil {
			return err
		}
		content = append(content, promptContentPart{Type: "file", ReceiptID: uploaded.ReceiptID})
	}
	req := sessionPromptRequest{
		RequestID: randomID("req"),
		SessionID: s.CurrentSessionID(),
		Mode:      mode,
		Content:   content,
	}
	return s.client.Call(s.ctx, "session/prompt", map[string]any{"request": req}, nil)
}

// CancelTurn maps abort_generation onto session/cancel.
func (s *dshSession) CancelTurn(ctx context.Context) error {
	req := sessionCancelRequest{SessionID: s.CurrentSessionID()}
	return s.client.Call(ctx, "session/cancel", map[string]any{"request": req}, nil)
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

// RespondPermission answers an approval request or a plan-review question
// (plan approval layer: plan_review cards answer through the question
// machinery — routing in respondPermissionRouted).
func (s *dshSession) RespondPermission(requestID string, result core.PermissionResult) error {
	return s.agent.respondPermissionRouted(s.ctx, s.CurrentSessionID(), requestID, result)
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

var _ core.PromptOptionsSender = (*dshSession)(nil)
