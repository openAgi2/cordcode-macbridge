package gobridge

// Phase-3 bridge-side tests (design §3.6.3, §16 gates 5+6): delivery fault
// matrix classification in handleSendMessage and CAS eviction / Close-once
// ownership in the session registry.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── stubs ───────────────────────────────────────────────────────────────────

// deliveryStubSession is a fully controllable AgentSession: scripted Send
// results, toggleable liveness, counted Closes.
type deliveryStubSession struct {
	id         string
	events     chan core.Event
	sendErrs   []error // consumed in order; nil entry ⇒ success
	sendCalls  atomic.Int32
	prompts    []string
	alive      atomic.Bool
	closeCount atomic.Int32
	closeOnce  sync.Once
	mu         sync.Mutex
}

func (s *deliveryStubSession) Send(prompt string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	s.mu.Lock()
	s.prompts = append(s.prompts, prompt)
	var err error
	if len(s.sendErrs) > 0 {
		err = s.sendErrs[0]
		s.sendErrs = s.sendErrs[1:]
	}
	s.mu.Unlock()
	s.sendCalls.Add(1)
	return err
}

func (s *deliveryStubSession) RespondPermission(string, core.PermissionResult) error { return nil }
func (s *deliveryStubSession) Events() <-chan core.Event                             { return s.events }
func (s *deliveryStubSession) CurrentSessionID() string                              { return s.id }
func (s *deliveryStubSession) Alive() bool                                           { return s.alive.Load() }
func (s *deliveryStubSession) RespondQuestion(string, []string) error                { return nil }
func (s *deliveryStubSession) RejectQuestion(string) error                           { return nil }

func (s *deliveryStubSession) Close() error {
	s.closeOnce.Do(func() { s.closeCount.Store(1) })
	return nil
}

// deliveryStubAgent hands out scripted sessions in StartSession order.
type deliveryStubAgent struct {
	name     string
	sessions []*deliveryStubSession
	starts   atomic.Int32
}

func (a *deliveryStubAgent) Name() string { return a.name }
func (a *deliveryStubAgent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.starts.Add(1)
	if len(a.sessions) == 0 {
		return nil, errors.New("no scripted sessions left")
	}
	s := a.sessions[0]
	a.sessions = a.sessions[1:]
	return s, nil
}
func (a *deliveryStubAgent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	return nil, core.ErrNotSupported
}
func (a *deliveryStubAgent) Stop() error { return nil }

func newDeliveryStub(id string, sendErrs ...error) *deliveryStubSession {
	s := &deliveryStubSession{id: id, events: make(chan core.Event, 8)}
	s.alive.Store(true)
	s.sendErrs = sendErrs
	return s
}

func sendMsgParams(sessionID string) []byte {
	return []byte(`{"sessionId":"` + sessionID + `","content":"hello"}`)
}

// findResult scans frames already read for the RPC result entry.
func findResult(t *testing.T, frames []map[string]any) map[string]any {
	t.Helper()
	for _, f := range frames {
		if f["type"] == "result" {
			return f
		}
	}
	t.Fatalf("no result frame in %d frames: %#v", len(frames), frames)
	return nil
}

// ── §16 gate 6: CAS eviction / Close-once ───────────────────────────────────

func TestEvictSessionCASCloseOnce(t *testing.T) {
	h := newTestHandlers(t)
	sess := newDeliveryStub("ses-1")
	h.putSessionWithMeta("ses-1", "deepseek", "", sess)

	// First eviction wins: CAS hit, Close called exactly once.
	if !h.evictSessionCAS("ses-1", sess) {
		t.Fatal("first CAS eviction must win")
	}
	if got := sess.closeCount.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
	// Second eviction (loser: entry already gone) is a no-op.
	if h.evictSessionCAS("ses-1", sess) {
		t.Fatal("second CAS eviction must lose")
	}
	if got := sess.closeCount.Load(); got != 1 {
		t.Fatalf("close count after loser = %d, want 1", got)
	}
}

func TestEvictSessionCASStaleEvictCannotKillReplacement(t *testing.T) {
	h := newTestHandlers(t)
	old := newDeliveryStub("ses-1")
	h.putSessionWithMeta("ses-1", "deepseek", "", old)

	// A racing replacement swaps the registry entry…
	replacement := newDeliveryStub("ses-1")
	h.putSessionWithMeta("ses-1", "deepseek", "", replacement)

	// …the stale evictor (holding `old`) must lose the CAS.
	if h.evictSessionCAS("ses-1", old) {
		t.Fatal("stale evictor must lose the CAS")
	}
	if old.closeCount.Load() != 0 {
		t.Fatal("stale evictor must not Close")
	}
	if cur, ok := h.getSession("ses-1"); !ok || cur != replacement {
		t.Fatal("replacement session must still be in the registry")
	}
	// The correct owner evicts the replacement cleanly.
	if !h.evictSessionCAS("ses-1", replacement) {
		t.Fatal("current owner must win the CAS")
	}
	if _, ok := h.getSession("ses-1"); ok {
		t.Fatal("registry entry must be gone after eviction")
	}
}

// ── §16 gate 5: delivery matrix through handleSendMessage ───────────────────

// Pre-write: repair once (evict dead session → respawn → single resend).
func TestSendMessagePreWriteRepairsOnce(t *testing.T) {
	dead := newDeliveryStub("ses-repair",
		&core.DeliveryError{Stage: core.StagePreWrite, Cause: errors.New("not alive")})
	dead.alive.Store(false)
	alive := newDeliveryStub("ses-repair") // respawn: sends succeed

	agent := &deliveryStubAgent{name: "dsh", sessions: []*deliveryStubSession{dead, alive}}
	h := newTestHandlers(t)
	h.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: sendMsgParams("ses-repair"),
	}, agent)
	msgs := readJSONMaps(t, clientConn, 2)
	result := findResult(t, msgs)
	if result["ok"] != true {
		t.Fatalf("repaired send must succeed, got %#v", result)
	}

	if got := agent.starts.Load(); got != 2 {
		t.Fatalf("StartSession calls = %d, want 2 (initial + respawn)", got)
	}
	if got := dead.sendCalls.Load(); got != 1 {
		t.Fatalf("dead session send calls = %d, want 1", got)
	}
	if got := alive.sendCalls.Load(); got != 1 {
		t.Fatalf("respawned session send calls = %d, want exactly 1 (no second replay)", got)
	}
	if dead.closeCount.Load() != 1 {
		t.Fatalf("dead session must be Closed exactly once, got %d", dead.closeCount.Load())
	}
	if cur, ok := h.getSession("ses-repair"); !ok || cur != alive {
		t.Fatal("registry must hold the respawned session")
	}
}

// Awaiting response: prompt may already be enqueued — fail visibly, never
// replay, keep a live session in the registry.
func TestSendMessageAwaitingResponseNoReplay(t *testing.T) {
	sess := newDeliveryStub("ses-await",
		&core.DeliveryError{Stage: core.StageAwaitingResponse, Cause: errors.New("receipt lost")})

	agent := &deliveryStubAgent{name: "dsh", sessions: []*deliveryStubSession{sess}}
	h := newTestHandlers(t)
	h.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: sendMsgParams("ses-await"),
	}, agent)
	msgs := readJSONMaps(t, clientConn, 2)
	if result := findResult(t, msgs); result["ok"] == true {
		t.Fatal("awaiting_response must fail visibly, not report ok")
	}

	if got := sess.sendCalls.Load(); got != 1 {
		t.Fatalf("send calls = %d, want exactly 1 (replay forbidden)", got)
	}
	if sess.closeCount.Load() != 0 {
		t.Fatal("live session must NOT be evicted by an awaiting-response failure")
	}
	if cur, ok := h.getSession("ses-await"); !ok || cur != sess {
		t.Fatal("live session must stay in the registry")
	}
}

// Awaiting response with a DEAD process: fail visibly AND evict the corpse so
// the next DIFFERENT request rebuilds instead of re-hitting it.
func TestSendMessageDeadUncertainDeliveryEvictsForNextRequest(t *testing.T) {
	dead := newDeliveryStub("ses-dead-uncertain",
		&core.DeliveryError{Stage: core.StageAwaitingResponse, Cause: errors.New("process exited")})
	dead.alive.Store(false)
	// Seeded directly into the registry (as a crashed session would be).
	agent := &deliveryStubAgent{name: "dsh", sessions: []*deliveryStubSession{}}
	h := newTestHandlers(t)
	h.RegisterAgent("deepseek", agent)
	h.putSessionWithMeta("ses-dead-uncertain", "deepseek", "", dead)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: sendMsgParams("ses-dead-uncertain"),
	}, agent)
	msgs := readJSONMaps(t, clientConn, 2)
	if result := findResult(t, msgs); result["ok"] == true {
		t.Fatal("dead uncertain delivery must fail visibly")
	}

	if got := dead.sendCalls.Load(); got != 1 {
		t.Fatalf("send calls = %d, want 1 (no replay)", got)
	}
	if dead.closeCount.Load() != 1 {
		t.Fatal("dead session must be evicted+Closed for the next request")
	}
	if _, ok := h.getSession("ses-dead-uncertain"); ok {
		t.Fatal("registry must not keep the dead session")
	}
}

// Partial write: same fail-visibly/no-replay semantics as awaiting-response.
func TestSendMessagePartialWriteFailsVisibly(t *testing.T) {
	sess := newDeliveryStub("ses-partial",
		&core.DeliveryError{Stage: core.StagePartialWrite, Cause: errors.New("5 of 120 bytes")})

	agent := &deliveryStubAgent{name: "dsh", sessions: []*deliveryStubSession{sess}}
	h := newTestHandlers(t)
	h.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: sendMsgParams("ses-partial"),
	}, agent)
	msgs := readJSONMaps(t, clientConn, 2)
	if result := findResult(t, msgs); result["ok"] == true {
		t.Fatal("partial write must fail visibly")
	}
	if got := sess.sendCalls.Load(); got != 1 {
		t.Fatalf("send calls = %d, want 1 (partial write must not replay)", got)
	}
}

// Plain (non-delivery) errors keep today's behavior: send_failed, session
// untouched.
func TestSendMessagePlainErrorUnchanged(t *testing.T) {
	sess := newDeliveryStub("ses-plain", errors.New("definite server rejection"))

	agent := &deliveryStubAgent{name: "dsh", sessions: []*deliveryStubSession{sess}}
	h := newTestHandlers(t)
	h.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: sendMsgParams("ses-plain"),
	}, agent)
	msgs := readJSONMaps(t, clientConn, 2)
	if result := findResult(t, msgs); result["ok"] == true {
		t.Fatal("plain error must fail visibly")
	}
	if _, ok := h.getSession("ses-plain"); !ok {
		t.Fatal("session must remain in the registry on a definite rejection")
	}
	if agent.starts.Load() != 1 {
		t.Fatalf("StartSession calls = %d, want 1 (no respawn on plain error)", agent.starts.Load())
	}
}

// Abort path (§3.6.3 五场景1): abort evicts + Closes; a stale concurrent
// evictor cannot touch the replacement.
func TestAbortEvictsAndStaleEvictorCannotKillReplacement(t *testing.T) {
	sess := newDeliveryStub("ses-abort")
	agent := &deliveryStubAgent{name: "dsh", sessions: []*deliveryStubSession{sess}}
	h := newTestHandlers(t)
	h.RegisterAgent("deepseek", agent)
	h.putSessionWithMeta("ses-abort", "deepseek", "", sess)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "deepseek", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"ses-abort"}`),
	})
	_ = readJSONMaps(t, clientConn, 1)

	if _, ok := h.getSession("ses-abort"); ok {
		t.Fatal("abort must remove the session from the registry")
	}
	if sess.closeCount.Load() != 1 {
		t.Fatalf("abort must Close exactly once, got %d", sess.closeCount.Load())
	}

	// Replacement appears (next request rebuilt it); the stale evictor that
	// still holds the aborted session object must lose the CAS.
	replacement := newDeliveryStub("ses-abort")
	h.putSessionWithMeta("ses-abort", "deepseek", "", replacement)
	if h.evictSessionCAS("ses-abort", sess) {
		t.Fatal("stale evictor must lose after abort replacement")
	}
	if _, ok := h.getSession("ses-abort"); !ok {
		t.Fatal("replacement must survive the stale evictor")
	}
}

// Gate 6 CAS race: concurrent evictions — exactly one winner, one Close.
func TestEvictSessionCASConcurrentSingleClose(t *testing.T) {
	h := newTestHandlers(t)
	sess := newDeliveryStub("ses-race")
	h.putSessionWithMeta("ses-race", "deepseek", "", sess)

	const racers = 16
	var wins atomic.Int32
	start := make(chan struct{})
	done := make(chan struct{}, racers)
	for i := 0; i < racers; i++ {
		go func() {
			<-start
			if h.evictSessionCAS("ses-race", sess) {
				wins.Add(1)
			}
			done <- struct{}{}
		}()
	}
	close(start)
	for i := 0; i < racers; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("eviction racer hung")
		}
	}
	if wins.Load() != 1 {
		t.Fatalf("exactly one CAS winner expected, got %d", wins.Load())
	}
	if sess.closeCount.Load() != 1 {
		t.Fatalf("exactly one Close expected, got %d", sess.closeCount.Load())
	}
}
