package dshweb

// Fake dsh web API server for unit tests. Faithfully mirrors the carrier
// contract from the dsh source (packages/host/apiproxy/src/fetch/handler.ts +
// packages/client/connection/src/{index.ts,websocket-downlink.ts}), pinned at
// 47f9438:
//
//   - POST /api/<method>: content-type must be application/json (else 415),
//     body must parse as JSON (else 400), body must be a ClientRequest
//     envelope whose method matches the path (else HTTP 200 + bad-request
//     ServerResponse), business failures are ALWAYS HTTP 200 +
//     result:{ok:false,error}.
//   - POST /api/respond: ClientResponse envelope → receipt
//     {accepted:true} | {accepted:false,reason}.
//   - GET /api/events.mux | /api/events.host: without WS upgrade headers →
//     426 Upgrade Required; with upgrade → pushed server-request frames
//     (downlink only; any client message closes 1008).
//
// Method behavior is scripted per test via the handlers map.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// fakeRPCResponse is one scripted outcome for a unary method.
type fakeRPCResponse struct {
	// value, when non-nil, is served as result:{ok:true,value}.
	value any
	// err, when non-nil, is served as result:{ok:false,error} — the business
	// error branch (HTTP 200).
	err *RPCError
}

// fakeDSHServer is a scripted dsh web /api gateway.
type fakeDSHServer struct {
	t        testingT
	server   *httptest.Server
	handlers map[string]fakeRPCResponse
	// hooks, when set for a method, take precedence over handlers and see the
	// raw payload (pagination / create-id sequencing tests).
	hooks map[string]func(payload []byte) fakeRPCResponse

	// lastRequest records the most recent unary request envelope.
	lastRequest struct {
		mu      sync.Mutex
		method  string
		rpcID   string
		payload json.RawMessage
	}

	// requests records every unary request seen, in order.
	requests struct {
		mu   sync.Mutex
		list []recordedRequest
	}

	// lastRespond records the most recent /api/respond body.
	lastRespond struct {
		mu   sync.Mutex
		body []byte
	}

	// muxFrames are pushed to every events.mux subscriber on connect.
	muxFrames []any
	// hostFrames are pushed to every events.host subscriber on connect.
	hostFrames []any
	// closeAfterPush closes each stream socket after pushing its frames
	// (reconnect-path tests); false keeps streams open.
	closeAfterPush bool

	// upgradeRequests counts WS dials seen per path.
	upgradeSeen map[string]int

	mu sync.Mutex
}

// recordedRequest is one captured unary call.
type recordedRequest struct {
	method  string
	payload []byte
}

// testingT is the subset of testing.T the fake needs (keeps it usable from
// package tests without importing testing in non-test builds).
type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func newFakeDSHServer(t testingT) *fakeDSHServer {
	f := &fakeDSHServer{
		t:           t,
		handlers:    map[string]fakeRPCResponse{},
		hooks:       map[string]func(payload []byte) fakeRPCResponse{},
		upgradeSeen: map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", f.handleAPI)
	mux.HandleFunc("/api/respond", f.handleRespond)
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeDSHServer) URL() string { return f.server.URL }

func (f *fakeDSHServer) Close() { f.server.Close() }

// handleAPI routes both unary POSTs and the two event-stream GETs.
func (f *fakeDSHServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/events.mux" || r.URL.Path == "/api/events.host" {
		f.handleEvents(w, r)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	// Media-type fence (fetch/handler.ts: only application/json).
	if mt := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.Split(mt, ";")[0])), "application/json") {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var env struct {
		Type    string          `json:"type"`
		RPCID   string          `json:"rpcId"`
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Type != "client-request" {
		writeServerResponse(w, "invalid-request", rpcResultBody{
			OK:    false,
			Error: &RPCError{Code: "bad-request", Message: "invalid client-request message", Details: json.RawMessage(`{"issues":[]}`)},
		})
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	if env.Method != method {
		writeServerResponse(w, env.RPCID, rpcResultBody{
			OK:    false,
			Error: &RPCError{Code: "bad-request", Message: "method \"" + env.Method + "\" does not match path \"" + method + "\"", Details: json.RawMessage(`{"issues":[]}`)},
		})
		return
	}
	f.lastRequest.mu.Lock()
	f.lastRequest.method = env.Method
	f.lastRequest.rpcID = env.RPCID
	f.lastRequest.payload = env.Payload
	f.lastRequest.mu.Unlock()
	f.requests.mu.Lock()
	f.requests.list = append(f.requests.list, recordedRequest{method: env.Method, payload: env.Payload})
	f.requests.mu.Unlock()

	if hook, ok := f.hooks[env.Method]; ok {
		scripted := hook(env.Payload)
		if scripted.err != nil {
			writeServerResponse(w, env.RPCID, rpcResultBody{OK: false, Error: scripted.err})
			return
		}
		writeServerResponse(w, env.RPCID, rpcResultBody{OK: true, Value: mustJSON(scripted.value)})
		return
	}
	scripted, ok := f.handlers[method]
	if !ok {
		// Unknown-but-valid path: mirror the real registry's 404 for methods
		// outside RpcMethodMap (only used when a test dials an unmapped one).
		http.NotFound(w, r)
		return
	}
	if scripted.err != nil {
		writeServerResponse(w, env.RPCID, rpcResultBody{OK: false, Error: scripted.err})
		return
	}
	writeServerResponse(w, env.RPCID, rpcResultBody{OK: true, Value: mustJSON(scripted.value)})
}

func (f *fakeDSHServer) handleRespond(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	f.lastRespond.mu.Lock()
	f.lastRespond.body = append([]byte(nil), body...)
	f.lastRespond.mu.Unlock()

	var env struct {
		Type   string        `json:"type"`
		RPCID  string        `json:"rpcId"`
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Type != "client-response" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respondReceipt{Accepted: false, Reason: "bad-response"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(respondReceipt{Accepted: true})
}

// handleEvents mirrors index.ts: plain GET → 426; upgrade → push frames.
func (f *fakeDSHServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "upgrade required", http.StatusUpgradeRequired)
		return
	}
	f.mu.Lock()
	f.upgradeSeen[r.URL.Path]++
	var frames []any
	closeAfter := f.closeAfterPush
	if r.URL.Path == "/api/events.mux" {
		frames = f.muxFrames
	} else {
		frames = f.hostFrames
	}
	f.mu.Unlock()

	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	// Downlink-only: any client message violates the contract (close 1008).
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(1008, "downlink only"),
				time.Now().Add(2*time.Second))
			return
		}
	}()
	for _, frame := range frames {
		// Every wire frame is a server-request envelope: method = payload.type.
		env := map[string]any{
			"type":    "server-request",
			"rpcId":   "fake-rpc-" + r.URL.Path,
			"method":  frame.(map[string]any)["type"],
			"payload": frame,
		}
		if err := conn.WriteJSON(env); err != nil {
			return
		}
	}
	if closeAfter {
		// Simulate a server-side drop so the client exercises its reopen path.
		_ = conn.Close()
		return
	}
	// Hold the socket open until the client closes (real server keeps the
	// stream alive; tests close the stream themselves).
	select {}
}

// SetMuxFrames / SetHostFrames script the frames pushed on stream connect.
func (f *fakeDSHServer) SetMuxFrames(frames []any) {
	f.mu.Lock()
	f.muxFrames = frames
	f.mu.Unlock()
}

func (f *fakeDSHServer) SetHostFrames(frames []any) {
	f.mu.Lock()
	f.hostFrames = frames
	f.mu.Unlock()
}

func writeServerResponse(w http.ResponseWriter, rpcID string, result rpcResultBody) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(serverResponse{Type: "server-response", RPCID: rpcID, Result: result})
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage(nil)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
