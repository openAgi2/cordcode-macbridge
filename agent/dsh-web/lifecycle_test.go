package dshweb

// §8-1 unit tests: wire envelope contract, carrier error classification,
// RpcError passthrough (坑 7), WS downlink frames, /api/respond echo, and the
// resolver's three lifecycle outcomes (external probe hit / managed spawn /
// both fail) per design docs/2026-08-16-dsh-web-backend-design.md §3.2/§4.2/§6.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Wire: unary envelope ─────────────────────────────────────────────────────

func TestUnaryCallSendsEnvelopeAndDecodesValue(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	f.handlers["host.describe"] = fakeRPCResponse{value: map[string]any{
		"version": "0.0.1", "cwd": "/tmp/x", "attachedSessions": 2, "canOpenPath": true,
	}}

	c := NewClient(f.URL(), nil)
	var out describeValue
	if err := c.Call(context.Background(), "host.describe", map[string]any{}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Version != "0.0.1" || out.AttachedSessions != 2 {
		t.Fatalf("decoded value mismatch: %+v", out)
	}

	// Envelope fidelity: the server must have seen a client-request with the
	// method matching the path and a JSON payload slot.
	f.lastRequest.mu.Lock()
	method, rpcID, payload := f.lastRequest.method, f.lastRequest.rpcID, f.lastRequest.payload
	f.lastRequest.mu.Unlock()
	if method != "host.describe" {
		t.Fatalf("server saw method %q", method)
	}
	if rpcID == "" {
		t.Fatal("rpcId missing")
	}
	if strings.TrimSpace(string(payload)) != "{}" {
		t.Fatalf("payload slot not carried: %s", payload)
	}
}

func TestUnaryBusinessErrorPassesRPCErrorVerbatim(t *testing.T) {
	// 坑 7 red line: the official RpcError message must survive mapping
	// untouched — collapsed error text hid the true cause in the stdio route.
	f := newFakeDSHServer(t)
	defer f.Close()
	original := `model "deepseek-chat" is not routable: allowed models are deepseek-v4-pro, deepseek-v4-flash (provider deepseek)`
	f.handlers["session.prompt"] = fakeRPCResponse{err: &RPCError{
		Code:    "model-unavailable",
		Message: original,
		Details: json.RawMessage(`{"provider":"deepseek","model":"deepseek-chat"}`),
	}}

	c := NewClient(f.URL(), nil)
	err := c.Call(context.Background(), "session.prompt", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != "model-unavailable" {
		t.Fatalf("code mismatch: %s", rpcErr.Code)
	}
	if rpcErr.Message != original {
		t.Fatalf("message was rewritten:\n got: %s\nwant: %s", rpcErr.Message, original)
	}
	if !strings.Contains(err.Error(), original) {
		t.Fatalf("Error() must embed the official text: %s", err.Error())
	}
}

func TestUnaryCarrierStatuses(t *testing.T) {
	// Carrier-layer failures (non-200) surface as *carrierError with the
	// status; business failures never do (they are 200 + RPCError).
	cases := []struct {
		status int
		body   string
	}{
		{http.StatusUnsupportedMediaType, "content type must be application/json"},
		{http.StatusBadRequest, "body is not JSON"},
		{http.StatusNotFound, "not found"},
		{http.StatusInternalServerError, "handler failure: boom"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, tc.body, tc.status)
		}))
		c := NewClient(srv.URL, nil)
		err := c.Call(context.Background(), "session.list", map[string]any{}, nil)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected error", tc.status)
		}
		ce, ok := err.(*carrierError)
		if !ok {
			t.Fatalf("status %d: expected *carrierError, got %T", tc.status, err)
		}
		if ce.Status != tc.status {
			t.Fatalf("status %d: carrierError.Status=%d", tc.status, ce.Status)
		}
		if !strings.Contains(ce.Error(), tc.body) {
			t.Fatalf("status %d: detail lost: %v", tc.status, ce)
		}
	}
}

func TestBareObjectBodyGetsBadRequestBranch(t *testing.T) {
	// Documents the carrier contract: valid JSON without the ClientRequest
	// envelope is answered HTTP 200 + bad-request ServerResponse (the real
	// fetch/handler.ts behavior). Asserted against the fake to keep it honest
	// with the pinned dsh source.
	f := newFakeDSHServer(t)
	defer f.Close()
	resp, err := http.Post(f.URL()+"/api/session.list", "application/json",
		strings.NewReader(`{"hello":"world"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var sr serverResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatal(err)
	}
	if sr.Type != "server-response" || sr.Result.OK || sr.Result.Error == nil || sr.Result.Error.Code != "bad-request" {
		t.Fatalf("expected bad-request branch, got %+v", sr)
	}
}

// ── Wire: WS downlinks ───────────────────────────────────────────────────────

func TestEventsEndpointsRequireUpgrade(t *testing.T) {
	// Plain GET → 426 with Upgrade headers (client/connection/src/index.ts).
	resp, err := http.Get(newFakeDSHServer(t).URL() + "/api/events.mux")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("expected 426, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Upgrade") != "websocket" {
		t.Fatal("missing Upgrade: websocket header")
	}
}

func TestOpenStreamReceivesServerRequestFrames(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	f.SetMuxFrames([]any{
		map[string]any{"type": "session/subscribed", "sessionId": "s1", "lastSeq": 42},
		map[string]any{"type": "session/event", "sessionId": "s1",
			"event": map[string]any{"type": "turn/start", "seq": 1, "time": 1.0, "data": map[string]any{}}},
	})

	c := NewClient(f.URL(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := c.OpenStream(ctx, "mux", "/api/events.mux")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer st.Close()

	frame, err := st.Next(ctx)
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	// Envelope contract: type=server-request, method = payload's type tag.
	if frame.Type != "server-request" {
		t.Fatalf("frame type %q", frame.Type)
	}
	if frame.Method != "session/subscribed" {
		t.Fatalf("method %q (must equal payload.type)", frame.Method)
	}
	var payload struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		LastSeq   int    `json:"lastSeq"`
	}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.SessionID != "s1" || payload.LastSeq != 42 {
		t.Fatalf("mux frame payload mismatch: %+v", payload)
	}

	frame2, err := st.Next(ctx)
	if err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if frame2.Method != "session/event" {
		t.Fatalf("second frame method %q", frame2.Method)
	}
}

// ── Wire: /api/respond ───────────────────────────────────────────────────────

func TestRespondEchoesRPCIDAndCarriesValue(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	c := NewClient(f.URL(), nil)

	accepted, err := c.Respond(context.Background(), "frame-rpc-7", true, map[string]any{
		"sessionId": "s1", "approvalId": "a1", "outcome": "allowed-once",
	}, nil)
	if err != nil || !accepted {
		t.Fatalf("Respond: accepted=%v err=%v", accepted, err)
	}
	f.lastRespond.mu.Lock()
	body := f.lastRespond.body
	f.lastRespond.mu.Unlock()
	var sent struct {
		Type   string        `json:"type"`
		RPCID  string        `json:"rpcId"`
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Type != "client-response" || sent.RPCID != "frame-rpc-7" {
		t.Fatalf("envelope/rpcId echo mismatch: %+v", sent)
	}
	if !sent.Result.OK {
		t.Fatalf("expected ok:true value branch: %+v", sent.Result)
	}
	var val map[string]any
	_ = json.Unmarshal(sent.Result.Value, &val)
	if val["outcome"] != "allowed-once" {
		t.Fatalf("value mismatch: %s", sent.Result.Value)
	}
}

func TestRespondRejectEncodesCancelledErrorBranch(t *testing.T) {
	// Question reject rides the error branch (ok:false) — asymmetric with
	// approvals by design (questions.schema.ts); the client must not send a
	// value payload there.
	f := newFakeDSHServer(t)
	defer f.Close()
	c := NewClient(f.URL(), nil)

	if _, err := c.Respond(context.Background(), "q-rpc-1", false, nil, nil); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	f.lastRespond.mu.Lock()
	body := f.lastRespond.body
	f.lastRespond.mu.Unlock()
	var sent struct {
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Result.OK {
		t.Fatal("reject must be the ok:false branch")
	}
	if sent.Result.Error == nil || sent.Result.Error.Code != "cancelled" {
		t.Fatalf("expected cancelled error branch: %+v", sent.Result)
	}
}

// ── Resolver lifecycle (§4.2) ────────────────────────────────────────────────

// describeHandler answers host.describe with a valid server-response — enough
// for probeInstance.
func describeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/host.describe" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	writeServerResponse(w, "probe", rpcResultBody{OK: true, Value: mustJSON(map[string]any{
		"version": "0.0.1", "cwd": "/tmp", "attachedSessions": 0, "canOpenPath": false,
	})})
}

func TestResolveExternalProbeHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(describeHandler))
	defer srv.Close()

	r := NewResolver(WithProbeURLs([]string{srv.URL}))
	inst, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if inst.Source != SourceExternal {
		t.Fatalf("expected external, got %s", inst.Source)
	}
	if inst.BaseURL != srv.URL {
		t.Fatalf("baseURL mismatch: %s", inst.BaseURL)
	}
	if inst.PID != 0 {
		t.Fatalf("external instance must not carry a pid, got %d", inst.PID)
	}
}

// countingStarter wraps a real HTTP server lifecycle as a managedStarter so
// the resolver's spawn→boot-wait→adopt path runs against real sockets.
type countingStarter struct {
	ln     net.Listener
	starts int
	fail   bool
	pid    int // reported child pid; defaults to the test process (alive)
}

func (s *countingStarter) Start(ctx context.Context, port int) (int, error) {
	s.starts++
	if s.fail {
		return 0, fmt.Errorf("dsh binary not found")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return 0, err
	}
	s.ln = ln
	go func() { _ = http.Serve(ln, http.HandlerFunc(describeHandler)) }()
	if s.pid != 0 {
		return s.pid, nil
	}
	return os.Getpid(), nil // "our child": alive from the resolver's viewpoint
}

func (s *countingStarter) Stop() error {
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

func TestResolveColdStartSpawnsOnSeatAndSeatIdentityAdopts(t *testing.T) {
	// Canonical-seat model (08-19 design §3.1): cold start spawns ON the seat
	// (never a private port range); a second resolver finds the same instance
	// through the seat itself — port = identity, no state-file adoption.
	seat := freeLoopbackSeat(t)
	dataDir := t.TempDir()
	starter := &countingStarter{}
	r1 := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(dataDir),
		withManagedStarter(starter),
	)
	inst, err := r1.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if inst.Source != SourceManaged {
		t.Fatalf("expected managed, got %s", inst.Source)
	}
	if inst.BaseURL != seat {
		t.Fatalf("spawn must bind the seat %s, got %s", seat, inst.BaseURL)
	}
	if starter.starts != 1 {
		t.Fatalf("starter ran %d times", starter.starts)
	}

	statePath := filepath.Join(dataDir, managedStateFile)
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %v, want 0600", info.Mode().Perm())
	}
	var st managedState
	b, _ := os.ReadFile(statePath)
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if st.Source != "managed" || st.Port != inst.Port || st.URL != seat {
		t.Fatalf("state mismatch: %+v vs %+v", st, inst)
	}

	// Second resolver (fresh process simulation): the seat is the identity —
	// it adopts the still-live instance without spawning.
	starter2 := &countingStarter{}
	r2 := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(dataDir),
		withManagedStarter(starter2),
	)
	inst2, err := r2.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve(2): %v", err)
	}
	if inst2.Source != SourceExternal || inst2.BaseURL != seat {
		t.Fatalf("seat-identity adoption mismatch: %+v", inst2)
	}
	if starter2.starts != 0 {
		t.Fatalf("second resolver spawned instead of adopting via seat (%d starts)", starter2.starts)
	}

	// Cached fast path: resolving again does not re-spawn.
	if _, err := r1.Resolve(context.Background()); err != nil {
		t.Fatalf("cached Resolve: %v", err)
	}
	if starter.starts != 1 {
		t.Fatalf("cached resolve re-spawned (%d starts)", starter.starts)
	}

	_ = r1.Stop()
	_ = r2.Stop()
	_ = starter.Stop()
}

// freeLoopbackSeat reserves an OS-chosen loopback port and releases it so a
// test's fake starter (or fake dsh server) can bind the seat for real.
func freeLoopbackSeat(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
}

func TestResolveBothFailReturnsError(t *testing.T) {
	r := NewResolver(
		WithProbeURLs([]string{"http://127.0.0.1:1"}),
		withManagedStarter(&countingStarter{fail: true}),
	)
	_, err := r.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error when probe misses and managed spawn fails")
	}
	if !strings.Contains(err.Error(), "dsh binary not found") {
		t.Fatalf("spawn failure detail lost: %v", err)
	}
}

func TestResolveExternalWinsOverManaged(t *testing.T) {
	// The user's own instance (probe hit) is preferred; the starter must
	// never run.
	srv := httptest.NewServer(http.HandlerFunc(describeHandler))
	defer srv.Close()
	starter := &countingStarter{}
	r := NewResolver(
		WithProbeURLs([]string{srv.URL}),
		withManagedStarter(starter),
	)
	inst, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inst.Source != SourceExternal || starter.starts != 0 {
		t.Fatalf("external must win without spawning: %+v starts=%d", inst, starter.starts)
	}
}

// ── Managed argv red lines (§4.4) ───────────────────────────────────────────

func TestManagedStartArgsLoopbackOnly(t *testing.T) {
	s := &execManagedStarter{}
	args := s.startArgs(3099)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--profile web", "--host 127.0.0.1", "--port 3099"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %v missing %q", args, want)
		}
	}
	if strings.Contains(joined, "--trusted-host") {
		t.Fatal("managed argv must never contain --trusted-host (§4.4 red line)")
	}
	if strings.Contains(joined, "0.0.0.0") {
		t.Fatal("managed argv must never bind 0.0.0.0 (§4.4 red line)")
	}
}

func TestManagedStateFileStaysInDataDir(t *testing.T) {
	if got := (&Resolver{dataDir: "/tmp/xyz"}).statePath(); got != "/tmp/xyz"+string(os.PathSeparator)+managedStateFile {
		t.Fatalf("statePath: %s", got)
	}
	if got := (&Resolver{}).statePath(); got != "" {
		t.Fatalf("empty dataDir must disable persistence, got %s", got)
	}
}
