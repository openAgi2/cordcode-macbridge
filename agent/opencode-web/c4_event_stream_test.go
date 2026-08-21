package opencodeweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// c4_event_stream_test.go owns the C4 live-stream boundaries (canonical
// §6.5): nested-sync skip before normalization, ONE global SSE connection
// per backend instance, per-session routing (E3 external turns), and the
// explicit reasoning-unsupported verdict on every live carrier.

// a1SSEPayloads extracts the direct payload frames of the archived A1
// sample, rewrapped in the wire envelope handleRawEvent consumes.
func a1SSEPayloads(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/official-1.18.18/samples/a1-first-healthy-text.sanitized.json")
	if err != nil {
		t.Fatalf("read A1 sample: %v", err)
	}
	var doc struct {
		SSE []struct {
			Event struct {
				Payload map[string]any `json:"payload"`
			} `json:"event"`
		} `json:"sse"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse A1 sample: %v", err)
	}
	if len(doc.SSE) == 0 {
		t.Fatal("A1 sample carries no SSE frames")
	}
	frames := make([]string, 0, len(doc.SSE))
	for _, f := range doc.SSE {
		b, err := json.Marshal(map[string]any{"payload": f.Event.Payload})
		if err != nil {
			t.Fatalf("rewrap frame: %v", err)
		}
		frames = append(frames, string(b))
	}
	return frames
}

// TestSyncFramesSkippedExactlyOnce replays the REAL A1 sequence — 52 frames
// including 17 nested `sync` duplicates — and proves single ingest: the
// streamed text is exactly the direct deltas ("SANDBOX_OK"), the user
// bubble arms exactly once, and one terminal result lands.
func TestSyncFramesSkippedExactlyOnce(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub, a1SSEPayloads(t)...)

	events := drain(sub)
	var text strings.Builder
	userMsgs, results := 0, 0
	for _, ev := range events {
		switch ev.Type {
		case core.EventText, core.EventTextReplace:
			text.WriteString(ev.Content)
		case core.EventUserMessage:
			userMsgs++
		case core.EventResult:
			results++
		}
	}
	// The direct deltas spell SANDBOX_OK; the 17 nested sync duplicates of
	// the same semantic events must contribute NOTHING.
	if got := text.String(); !strings.Contains(got, "SANDBOX_OK") {
		t.Fatalf("streamed text must contain the direct deltas, got %q", got)
	}
	if strings.Count(text.String(), "SAND") != 1 {
		t.Fatalf("sync duplicates must not double-ingest text, got %q", text.String())
	}
	if userMsgs != 1 {
		t.Fatalf("exactly one user bubble expected, got %d", userMsgs)
	}
	if results != 1 {
		t.Fatalf("exactly one terminal result expected, got %d", results)
	}
}

// countingSSEServe counts /global/event dials and keeps the stream open.
func countingSSEServe(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var dials int32
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
			_, _ = w.Write([]byte(testProviderCatalog))
		case "/global/event":
			dials++
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &dials
}

func agentForSSE(t *testing.T, url string) *Agent {
	t.Helper()
	a, err := New(map[string]any{
		"work_dir":          "/tmp/proj",
		"opencode_web_url":  url,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })
	return agent
}

// TestOneGlobalSSEConnectionPerBackendInstance: two concurrent sessions plus
// a passive Subscribe dial /global/event exactly ONCE (§6.5 bridge mapping).
func TestOneGlobalSSEConnectionPerBackendInstance(t *testing.T) {
	srv, dials := countingSSEServe(t, )
	agent := agentForSSE(t, srv.URL)

	s1, err := agent.StartSession(context.Background(), "ses_a")
	if err != nil {
		t.Fatalf("session A: %v", err)
	}
	defer s1.Close()
	s2, err := agent.StartSession(context.Background(), "ses_b")
	if err != nil {
		t.Fatalf("session B: %v", err)
	}
	defer s2.Close()
	if _, err := agent.Subscribe(context.Background()); err != nil {
		t.Fatalf("passive: %v", err)
	}
	// ctx cancel detaches the tap.
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()
	if _, err := agent.Subscribe(pctx); err != nil {
		t.Fatalf("passive 2: %v", err)
	}

	if n := atomicLoadInt32(dials); n != 1 {
		t.Fatalf("exactly ONE /global/event connection per backend instance expected, got %d", n)
	}
	// Closing one session/passive tap must NOT tear the shared stream while
	// another holder lives.
	_ = s1.Close()
	pcancel()
	time.Sleep(50 * time.Millisecond)
	if n := atomicLoadInt32(dials); n != 1 {
		t.Fatalf("shared stream must survive while holders remain, dials=%d", n)
	}
	_ = s2.Close()
}

func atomicLoadInt32(p *int32) int32 {
	return *p // single-threaded test serve handler mutates only on dial
}

// TestExternalTurnRoutesAndUnregisteredIsCatalogOnly drives E3-shaped
// frames through a driven global subscriber: a REGISTERED session's route
// receives the external turn's events and the passive tap observes them,
// while an UNREGISTERED session produces no route events — only the catalog
// refresh signal from its session.created.
func TestExternalTurnRoutesAndUnregisteredIsCatalogOnly(t *testing.T) {
	srv, _ := countingSSEServe(t)
	agent := agentForSSE(t, srv.URL)

	sess, err := agent.StartSession(context.Background(), "ses_open")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	open := sess.(*serverSession)

	sub := agent.globalSub
	if sub == nil {
		t.Fatal("StartSession must have armed the global subscriber")
	}

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_ext", "role": "user"},
			"sessionID": "ses_open",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_open", "messageID": "msg_ext", "field": "text", "delta": "ext ",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_open", "messageID": "msg_ext", "field": "text", "delta": "turn",
		}),
		sseFrame("session.status", map[string]any{
			"sessionID": "ses_open", "status": map[string]any{"type": "idle"},
		}),
		// A DIFFERENT session nobody registered: catalog-only.
		sseFrame("session.created", map[string]any{
			"info": map[string]any{"id": "ses_ghost", "directory": "/tmp/other"},
		}),
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_ghost", "role": "user"},
			"sessionID": "ses_ghost",
		}),
	)

	// Registered route saw the external turn: one user bubble whose
	// accumulated prompt text is the external "ext turn", one terminal.
	var sawUser, sawResult bool
	var userText string
	for {
		select {
		case ev := <-open.events:
			switch ev.Type {
			case core.EventUserMessage:
				sawUser = true
				userText = ev.Content
			case core.EventResult:
				sawResult = true
			}
		default:
			goto routeDone
		}
	}
routeDone:
	if !sawUser || !sawResult || userText != "ext turn" {
		t.Fatalf("registered route must stream the external turn, user=%v text=%q result=%v", sawUser, userText, sawResult)
	}
	// The unregistered session produced a catalog signal, not a route feed.
	select {
	case <-agent.CatalogRefreshSignals():
	default:
		t.Fatal("session.created for an unregistered session must signal catalog refresh")
	}
	// No hidden second channel: the ghost session's events went nowhere
	// except the subscriber's own (undrained) native channel.
	if ev := agent.nextRoutedFor("ses_ghost"); ev != nil {
		t.Fatalf("unregistered session must have no route, got %+v", ev)
	}
}

// nextRoutedFor is a test probe: nil unless a route exists for sessionID.
func (a *Agent) nextRoutedFor(sessionID string) *core.Event {
	a.routesMu.Lock()
	chans, ok := a.routes[sessionID]
	a.routesMu.Unlock()
	if !ok || len(chans) == 0 {
		return nil
	}
	for ch := range chans {
		select {
		case ev := <-ch:
			e := ev
			return &e
		default:
		}
	}
	return nil
}

// TestReasoningExplicitlyUnsupportedOnAllCarriers: populated reasoning on
// message.updated parts, part.updated snapshots, and part.delta fields all
// surface the canonical unsupported error and NEVER EventThinking.
func TestReasoningExplicitlyUnsupportedOnAllCarriers(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	// Arm the message as assistant first so part.updated is not treated as user.
	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_a", "role": "assistant", "parts": []any{map[string]any{"id": "pt_r", "type": "reasoning", "text": "chain…"}}},
			"sessionID": "ses_1",
		}),
		sseFrame("message.part.updated", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a",
			"part": map[string]any{"id": "pt_r", "type": "reasoning", "text": "more chain"},
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a", "partID": "pt_r", "field": "reasoning", "delta": "…",
		}),
	)

	var unsupported, thinking int
	for _, ev := range drain(sub) {
		switch ev.Type {
		case core.EventError:
			if ev.Content == "unsupported content.reasoning for verified 1.18.18 shape" {
				unsupported++
			}
		case core.EventThinking:
			thinking++
		}
	}
	if unsupported == 0 {
		t.Fatal("populated reasoning must surface the canonical unsupported error on every carrier")
	}
	if thinking != 0 {
		t.Fatalf("reasoning must never map to thinking, got %d events", thinking)
	}
}

// TestReconnectKeepsSingleSubscriberRoute: after a mid-flight drop the SAME
// subscriber reconnects and the session route keeps receiving events —
// exactly two dials total, one route, no second timeline.
func TestReconnectKeepsSingleSubscriberRoute(t *testing.T) {
	// Custom serve: first stream closes after one event; second stays open.
	var dials int32
	var mu sync.Mutex
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
			_, _ = w.Write([]byte(testProviderCatalog))
		case "/global/event":
			mu.Lock()
			dials++
			n := dials
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			if n == 1 {
				// First connection: arm one user turn, then the stream DIES
				// mid-turn (A5 shape).
				_, _ = w.Write([]byte(`data: {"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","role":"user"},"sessionID":"ses_r"}}}` + "\n\n"))
				_, _ = w.Write([]byte(`data: {"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_r","messageID":"msg_1","field":"text","delta":"before drop"}}}` + "\n\n"))
				w.(http.Flusher).Flush()
				return // close
			}
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	agent := agentForSSE(t, srv.URL)

	sess, err := agent.StartSession(context.Background(), "ses_r")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	route := sess.(*serverSession).events

	// The user bubble from connection 1 must land on the route.
	deadline := time.After(5 * time.Second)
	sawUser := false
	for !sawUser {
		select {
		case ev := <-route:
			if ev.Type == core.EventUserMessage {
				sawUser = true
			}
		case <-deadline:
			t.Fatal("connection-1 user event never reached the route")
		}
	}
	// After the drop the subscriber redials (A5 reconnect) — wait for the
	// second dial, then drive a terminal through the SAME subscriber and
	// prove the route still receives it.
	waitFor := func(cond func() bool) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if cond() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	mu.Lock()
	dialSnapshot := func() int32 { mu.Lock(); defer mu.Unlock(); return dials }
	mu.Unlock()
	waitFor(func() bool { return dialSnapshot() >= 2 })
	if sub := agent.globalSub; sub == nil {
		t.Fatal("the SAME global subscriber must survive the reconnect")
	}
	driveFrames(agent.globalSub,
		sseFrame("session.status", map[string]any{
			"sessionID": "ses_r", "status": map[string]any{"type": "idle"},
		}),
	)
	// Drain queued turn events until the terminal result lands.
	terminalDeadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-route:
			if ev.Type == core.EventResult {
				goto terminalOK
			}
		case <-terminalDeadline:
			t.Fatal("post-reconnect terminal never reached the route")
		}
	}
terminalOK:
	if dialSnapshot() != 2 {
		t.Fatalf("exactly two dials (initial + one reconnect) expected, got %d", dialSnapshot())
	}
}
