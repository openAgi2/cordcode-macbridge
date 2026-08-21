package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ocweb "github.com/openAgi2/cordcode-macbridge/agent/opencode-web"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit008_fullpath_test.go holds the directive-009 Wave-0 full-path
// reproducers for audit-008's two hard vetoes. They wire the REAL adapter
// (opencode-web Agent over a wire-level SSE serve), the REAL Handlers relay
// (startRelayIfNotRunning → relayEvents → deltaBatcher → EventPublisher) AND
// the REAL passive subscription (startPassiveSubscription) simultaneously,
// then assert PROJECTION state — not adapter events, channel counts, or dial
// counts.

// ssePushServe is a wire-level serve whose /global/event stream the test
// feeds frame-by-frame (the frames travel the real HTTP/SSE decode path).
type ssePushServe struct {
	t      *testing.T
	server *httptest.Server

	mu     sync.Mutex
	frames chan string
	subbed chan struct{} // closed on first SSE dial
	dials  int
	// Directive-010 recovery surfaces: the official pending-question list and
	// the per-session message history the cold/gap recovery reads. Defaults
	// (empty list / empty history) keep directive-009 behavior untouched.
	pendingQuestionsJSON string
	historyBySession     map[string]string
	questionFetches      int
	// questionGate / historyGate park the corresponding recovery responses
	// until closed — the directive-011 barrier control for real interleavings
	// (nil = open).
	questionGate chan struct{}
	historyGate  chan struct{}
	// replyBroadcasts/rejectBroadcasts toggle the official POST side effects:
	// POST /question/{id}/reply|reject answers `true` AND broadcasts the
	// question.replied/rejected frame on the SSE stream (real server order).
	replyBroadcasts   bool
	rejectBroadcasts  bool
	questionPOSTs     []recordedQuestionPOST
	// connDrops holds one done-channel per live SSE connection; drop() closes
	// them all to simulate a mid-flight stream gap.
	connDrops []chan struct{}
}

type recordedQuestionPOST struct {
	Path string
	Body string
}

func newSSEPushServe(t *testing.T) *ssePushServe {
	t.Helper()
	s := &ssePushServe{
		t:                    t,
		frames:               make(chan string, 256),
		subbed:               make(chan struct{}),
		pendingQuestionsJSON: "[]",
		historyBySession:     map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case "/session":
			_, _ = w.Write([]byte(`[]`))
		case "/agent":
			_, _ = w.Write([]byte(`[{"name":"build","mode":"primary","native":true,"description":"general coding"}]`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[{"id":"localmock","models":{"echo":{"id":"echo","variants":{"high":{},"low":{}}}}}],"default":{"localmock":"echo"},"connected":["localmock"]}`))
		case "/config":
			_, _ = w.Write([]byte(`{}`))
		case "/question":
			s.mu.Lock()
			s.questionFetches++
			body := s.pendingQuestionsJSON
			gate := s.questionGate
			s.mu.Unlock()
			if gate != nil {
				<-gate
			}
			_, _ = w.Write([]byte(body))
		case "/global/event":
			drop := make(chan struct{})
			s.mu.Lock()
			s.dials++
			first := s.dials == 1
			s.connDrops = append(s.connDrops, drop)
			s.mu.Unlock()
			if first {
				close(s.subbed)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			for {
				select {
				case frame := <-s.frames:
					_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
					w.(http.Flusher).Flush()
				case <-drop:
					return
				case <-r.Context().Done():
					return
				}
			}
		default:
			// Official question reply/reject POSTs: answer `true` and, when
			// the test enables broadcasting, publish the server frame on the
			// live stream (the real serve's POST→broadcast order).
			if strings.HasPrefix(r.URL.Path, "/question/") && r.Method == http.MethodPost {
				bodyBytes, _ := io.ReadAll(r.Body)
				s.mu.Lock()
				s.questionPOSTs = append(s.questionPOSTs, recordedQuestionPOST{Path: r.URL.Path, Body: string(bodyBytes)})
				broadcast := false
				if strings.HasSuffix(r.URL.Path, "/reply") && s.replyBroadcasts {
					broadcast = true
				}
				if strings.HasSuffix(r.URL.Path, "/reject") && s.rejectBroadcasts {
					broadcast = true
				}
				s.mu.Unlock()
				_, _ = w.Write([]byte(`true`))
				if broadcast {
					id := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/question/"), "/reply"), "/reject")
					kind := "question.replied"
					if strings.HasSuffix(r.URL.Path, "/reject") {
						kind = "question.rejected"
					}
					props := map[string]any{"sessionID": "ses_ocw1", "requestID": id}
					if kind == "question.replied" {
						props["answers"] = [][]string{{"red"}}
					}
					s.push(map[string]any{"type": kind, "properties": props})
				}
				return
			}
			// Per-session message history (the authoritative recovery truth).
			if strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message") {
				sid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/message")
				s.mu.Lock()
				body := s.historyBySession[sid]
				gate := s.historyGate
				s.mu.Unlock()
				if gate != nil {
					<-gate
				}
				if body == "" {
					body = "[]"
				}
				_, _ = w.Write([]byte(body))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// setPendingQuestions installs the official GET /question answer.
func (s *ssePushServe) setPendingQuestions(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingQuestionsJSON = body
}

// setHistory installs the per-session GET /session/{id}/message answer.
func (s *ssePushServe) setHistory(sessionID, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.historyBySession[sessionID] = body
}

// drop closes every live SSE connection — the subscriber must heal, redial,
// and reconcile pending questions for its routed sessions (directive-010).
func (s *ssePushServe) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, drop := range s.connDrops {
		close(drop)
	}
	s.connDrops = nil
}

// recordedQuestionPOSTs snapshots the official reply/reject POSTs.
func (s *ssePushServe) recordedQuestionPOSTs() []recordedQuestionPOST {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedQuestionPOST, len(s.questionPOSTs))
	copy(out, s.questionPOSTs)
	return out
}

func (s *ssePushServe) push(payload map[string]any) {
	s.t.Helper()
	b, err := json.Marshal(map[string]any{"payload": payload})
	if err != nil {
		s.t.Fatalf("marshal frame: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames <- string(b)
}

func (s *ssePushServe) dialCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// newAuditHarness wires the full production path: real adapter, real
// Handlers (deltaBatcher + EventPublisher + ProjectionKernel), one active
// relay for the session, and the passive subscription — all at once, exactly
// like the running bridge.
func newAuditHarness(t *testing.T) (*Handlers, *ssePushServe) {
	h, serve, _ := newAuditHarnessWithOptions(t, func(*ssePushServe) {})
	return h, serve
}

// newAuditHarnessWithOptions lets a test shape the serve (pending questions,
// history) BEFORE the adapter connects — the directive-010 recovery reads
// them during StartSession. It also returns the live harness agent so
// directive-011 barrier tests can open a SECOND session on the SAME agent
// (shared lifecycle gate + routes).
func newAuditHarnessWithOptions(t *testing.T, configure func(*ssePushServe)) (*Handlers, *ssePushServe, core.Agent) {
	t.Helper()
	serve := newSSEPushServe(t)
	configure(serve)
	agentAny, err := ocweb.New(map[string]any{
		"work_dir":          "/tmp/audit008",
		"opencode_web_url":  serve.server.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("ocweb.New: %v", err)
	}
	agent := agentAny.(core.Agent)
	subscriber := agentAny.(core.EventSubscriber)
	probe := agentAny.(interface{ HasPassiveTaps() bool })
	h := NewHandlers()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()

	sess, err := agent.StartSession(context.Background(), "ses_ocw1")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	h.putSessionWithMeta("ses_ocw1", "opencode-web", "/tmp/audit008", sess)
	conn := newPublisherCaptureConn(nil)
	h.startRelayIfNotRunning("ses_ocw1", sess, conn, "opencode-web")

	// The passive subscription is the second consumer the audit convicted.
	pctx, pcancel := context.WithCancel(context.Background())
	t.Cleanup(pcancel)
	go startPassiveSubscription(pctx, h, "opencode-web", subscriber)

	// Determinism: wait until the passive tap is attached before injecting.
	deadline := time.After(5 * time.Second)
	for {
		if probe.HasPassiveTaps() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("passive subscription never attached its tap")
		case <-time.After(10 * time.Millisecond):
		}
	}
	return h, serve, agent
}

// snapshotText concatenates the assistant text of every turn part (the
// projection-level view iOS would render).
func snapshotText(t *testing.T, h *Handlers) (string, int) {
	t.Helper()
	proj, ok := h.projectionKernel.reducer.Snapshot("opencode-web", "ses_ocw1")
	if !ok {
		return "", 0
	}
	var b strings.Builder
	parts := 0
	for _, turn := range proj.Turns {
		for _, msg := range []*MessageProjection{turn.User, turn.Assistant, turn.System} {
			if msg == nil {
				continue
			}
			for _, part := range msg.Parts {
				if part.Type == "text" {
					b.WriteString(part.Text)
					parts++
				}
			}
		}
	}
	return b.String(), parts
}

func snapshotUserInputParts(t *testing.T, h *Handlers) int {
	t.Helper()
	proj, ok := h.projectionKernel.reducer.Snapshot("opencode-web", "ses_ocw1")
	if !ok {
		return 0
	}
	count := 0
	for _, turn := range proj.Turns {
		for _, msg := range []*MessageProjection{turn.User, turn.Assistant, turn.System} {
			if msg == nil {
				continue
			}
			for _, part := range msg.Parts {
				if part.Type == "user_input" {
					count++
				}
			}
		}
	}
	return count
}

// TestAudit008_SingleIngestOneTextFactOneProjection is the C4 full-path
// reproducer: with BOTH the active relay and the passive subscription
// consuming the same backend stream, ONE assistant text fact must land in the
// projection exactly once. Audit-008 proved the current emit() fan-out feeds
// deltaBatcher twice; this test must fail on that implementation and pass
// only under a single timeline-ingest owner.
func TestAudit008_SingleIngestOneTextFactOneProjection(t *testing.T) {
	h, serve := newAuditHarness(t)

	// One minimal turn, exactly once on the wire.
	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_u1", "role": "user"}, "sessionID": "ses_ocw1"}})
	serve.push(map[string]any{"type": "message.part.delta", "properties": map[string]any{
		"sessionID": "ses_ocw1", "messageID": "msg_a1", "partID": "pt_1", "field": "text", "delta": "ONCE"}})
	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_a1", "role": "assistant",
			"parts": []any{map[string]any{"id": "pt_1", "type": "text", "text": "ONCE"}}},
		"sessionID": "ses_ocw1"}})
	serve.push(map[string]any{"type": "session.status", "properties": map[string]any{
		"sessionID": "ses_ocw1", "status": map[string]any{"type": "idle"}}})

	deadline := time.After(8 * time.Second)
	for {
		text, parts := snapshotText(t, h)
		if parts > 0 {
			if strings.Count(text, "ONCE") != 1 {
				t.Fatalf("AUDIT-008 C4 double ingest reproduced: assistant text %q contains the fact %d times (want exactly 1)", text, strings.Count(text, "ONCE"))
			}
			if text != "ONCE" {
				t.Fatalf("assistant text = %q, want exactly ONCE", text)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("the turn never reached the projection")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestAudit008_QuestionReachesProjection is the C6 full-path reproducer: the
// A7 asked frame carries real source identity (tool.messageID/callID) and
// must surface as exactly ONE pending user_input part in the projection.
// Audit-008 proved the current adapter emits an identityless event that the
// reducer drops; this test must fail on that implementation.
func TestAudit008_QuestionReachesProjection(t *testing.T) {
	h, serve := newAuditHarness(t)

	// Arm the owning turn first (the question rides an assistant message of
	// THIS turn — A7: tool.messageID is the assistant message of the turn
	// started by the user echo).
	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_u1", "role": "user"}, "sessionID": "ses_ocw1"}})
	// Directive-010: the assistant message fact (info.parentID = owning turn)
	// precedes question.asked on the real stream (A7 frames 14→77) — the
	// correlation is messageID-proven, not activeTurn-assumed.
	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_a7_tool", "role": "assistant", "parentID": "msg_u1"},
		"sessionID": "ses_ocw1"}})
	// The real A7 asked frame shape (sanitized sample, identities preserved).
	serve.push(map[string]any{"type": "question.asked", "properties": map[string]any{
		"id": "que_a7", "sessionID": "ses_ocw1",
		"questions": []any{map[string]any{
			"question": "Which fixture color?", "header": "Color",
			"options": []any{
				map[string]any{"label": "red", "description": "Stop"},
				map[string]any{"label": "green", "description": "Go"},
			},
			"multiple": false,
		}},
		"tool": map[string]any{"messageID": "msg_a7_tool", "callID": "call_a7"}}})

	deadline := time.After(5 * time.Second)
	for {
		if n := snapshotUserInputParts(t, h); n > 0 {
			if n != 1 {
				t.Fatalf("question must project exactly ONE user_input part, got %d", n)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("AUDIT-008 C6 reproduced: the A7 question never reached the projection (identityless drop)")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestAudit008_UnopenedSessionCatalogOnlyNoTimeline: a fact for a session
// with NEITHER a registered route NOR a live subscriber must not create
// timeline state (§6.5: unopened external sessions are catalog-only).
func TestAudit008_UnopenedSessionCatalogOnlyNoTimeline(t *testing.T) {
	h, serve := newAuditHarness(t)

	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_ext", "role": "user"}, "sessionID": "ses_UNOPENED"}})
	serve.push(map[string]any{"type": "message.part.delta", "properties": map[string]any{
		"sessionID": "ses_UNOPENED", "messageID": "msg_ext_a", "partID": "pt_e", "field": "text", "delta": "GHOST"}})

	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case <-deadline:
			goto settled
		case <-time.After(50 * time.Millisecond):
		}
	}
settled:
	if _, ok := h.projectionKernel.reducer.Snapshot("opencode-web", "ses_UNOPENED"); ok {
		t.Fatal("an unopened external session must not gain hidden timeline/projection state (§6.5 catalog-only)")
	}
}
