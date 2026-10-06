package dshweb

// Fake dsh web API server for unit tests. Faithfully mirrors the typert
// gateway carrier contract (upstream 0d1f50007f: packages/client/connection/
// src/{rpc.ts,rpc-host.ts} + packages/api/gateway/src/{index.ts,
// stream-protocol.ts}; live-probed against 0.1.7-alpha.1 2026-09-23):
//
//   - POST /api/<method>: content-type must be application/json (else 415),
//     body must parse as JSON (else 400), body must be a ClientRequest
//     envelope whose method matches the path (else HTTP 200 + bad-request
//     ServerResponse), business failures are ALWAYS HTTP 200 +
//     result:{ok:false,error}.
//   - POST /api/$events/result: ClientRequest envelope with method
//     "$events/result"; the args {clientId,eventId,outcome} are recorded and
//     answered result:{ok:true} (or a scripted error — the answered-elsewhere
//     path).
//   - GET /api/remote.mux: without WS upgrade headers → 404 (the gateway
//     registers an upgrade route); with upgrade → the logical-stream mux:
//     client `open` frames are routed by streamId ("events" → ready + the
//     scripted $events frames; "ws" → the workspace baseline; "f:<sid>" →
//     the session's follow snapshot + events), each pushed as one
//     {type:"item",streamId,value} frame.
//
// Method behavior is scripted per test via the handlers map (slash-method
// keys, e.g. "session/list").

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

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

// fakeFollowScript is one session's scripted follow stream: the opening
// snapshot plus its live event items (each a pageRecord map).
type fakeFollowScript struct {
	snapshot map[string]any
	items    []any
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

	// lastEventResult records the most recent /api/$events/result args.
	lastEventResult struct {
		mu   sync.Mutex
		args eventResultArgs
	}

	// eventResults records every $events/result args seen, in order.
	eventResults struct {
		mu   sync.Mutex
		list []eventResultArgs
	}

	// eventsFrames are pushed as $events items after the ready frame (each a
	// remoteEventFrame-shaped map: emit/waterfall/cancel).
	eventsFrames []any
	// eventResultErr, when set, is the business error answered to every
	// $events/result (the answered-elsewhere path).
	eventResultErr *RPCError
	// wsBaseline is pushed as the "ws" stream's baseline item.
	wsBaseline *workspaceBaseline
	// followScripts scripts per-session follow streams by session id.
	followScripts map[string]fakeFollowScript
	// followOpens records each follow open's payload by session id.
	followOpens struct {
		mu   sync.Mutex
		byID map[string]json.RawMessage
	}
	// closeAfterPush closes the mux socket after pushing a stream's frames
	// (reconnect-path tests); false keeps the socket open.
	closeAfterPush bool

	// upgradeRequests counts WS dials seen per path.
	upgradeSeen map[string]int

	// upload scripts the next raw uploadFileBinary response (S4); uploads
	// records every raw upload seen.
	upload  uploadScript
	uploads struct {
		mu   sync.Mutex
		list []recordedUpload
	}

	// file scripts the next /api/file response (session media tests); files
	// records every /api/file request seen.
	file  fileScript
	files struct {
		mu   sync.Mutex
		list []recordedFile
	}
	// fileByPath scripts per-path /api/file responses (dimension probe batch
	// tests); paths without an entry fall through to the single-shot script.
	fileByPath map[string]fileScript

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
		t:             t,
		handlers:      map[string]fakeRPCResponse{},
		hooks:         map[string]func(payload []byte) fakeRPCResponse{},
		upgradeSeen:   map[string]int{},
		followScripts: map[string]fakeFollowScript{},
		fileByPath:    map[string]fileScript{},
	}
	f.followOpens.byID = map[string]json.RawMessage{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", f.handleAPI)
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeDSHServer) URL() string { return f.server.URL }

func (f *fakeDSHServer) Close() { f.server.Close() }

// uploadScript scripts the raw upload route's next response (S4 tests).
type uploadScript struct {
	// receipt, when non-empty, is served as {ok:true, value:{receiptId, file}}.
	receipt string
	// err, when non-nil, is served as {ok:false, error}.
	err *RPCError
	// httpStatus, when non-200, is served with plainText as the body (the
	// raw route's carrier rejections: 415/400).
	httpStatus int
	plainText  string
}

// handleUploadFileBinary mirrors the official raw byte route
// (file-upload/src/http-route.ts:22-60): octet-stream only, sessionId query
// required, FileUploadHttpResult JSON envelope.
func (f *fakeDSHServer) handleUploadFileBinary(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	script := f.upload
	f.upload = uploadScript{}
	f.mu.Unlock()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if mt := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.Split(mt, ";")[0])), "application/octet-stream") {
		http.Error(w, "content type must be application/octet-stream", http.StatusUnsupportedMediaType)
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	f.uploads.mu.Lock()
	f.uploads.list = append(f.uploads.list, recordedUpload{
		sessionID: sessionID,
		name:      r.URL.Query().Get("name"),
		data:      append([]byte(nil), body...),
	})
	f.uploads.mu.Unlock()

	if script.httpStatus != 0 {
		http.Error(w, script.plainText, script.httpStatus)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if script.err != nil {
		_ = json.NewEncoder(w).Encode(fileUploadHttpResult{OK: false, Error: script.err})
		return
	}
	fileRef := struct {
		AttachmentID string `json:"attachmentId"`
		Name         string `json:"name"`
		Bytes        int64  `json:"bytes"`
	}{
		AttachmentID: "sha256:" + hex.EncodeToString([]byte("fake-digest-"+script.receipt)),
		Name:         "uploaded.bin",
		Bytes:        int64(len(body)),
	}
	_ = json.NewEncoder(w).Encode(fileUploadHttpResult{OK: true, Value: &fileUploadValue{ReceiptID: script.receipt, File: fileRef}})
}

// recordedUpload is one captured raw upload.
type recordedUpload struct {
	sessionID string
	name      string
	data      []byte
}

// fileScript scripts the /api/file route's next response (session media
// tests), mirroring media-references.ts serveFile.
type fileScript struct {
	// contentType + data are served as 200 when status == 0.
	contentType string
	data        []byte
	// status + body are served verbatim when status != 0: the body is the
	// FsError code text (FS_NOT_FOUND …) or the 400 pre-check text.
	status int
	body   string
}

// recordedFile is one captured /api/file request (the decoded path query).
type recordedFile struct {
	path string
}

// handleFile mirrors the official authenticated file route
// (media-references.ts): GET only, path query required and absolute.
func (f *fakeDSHServer) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	f.files.mu.Lock()
	f.files.list = append(f.files.list, recordedFile{path: path})
	f.files.mu.Unlock()
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	if strings.ContainsRune(path, 0) || !strings.HasPrefix(path, "/") {
		http.Error(w, "absolute path required", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	script, hasByPath := f.fileByPath[path]
	if !hasByPath {
		script = f.file
		f.file = fileScript{}
	}
	f.mu.Unlock()
	if script.status != 0 {
		http.Error(w, script.body, script.status)
		return
	}
	w.Header().Set("Content-Type", script.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(script.data)))
	_, _ = w.Write(script.data)
}

// handleAPI routes unary POSTs, the $events/result RPC, the raw
// uploadFileBinary byte route, and the remote.mux upgrade (all under /api/).
func (f *fakeDSHServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == remoteMuxPath {
		f.handleRemoteMux(w, r)
		return
	}
	if r.URL.Path == "/api/session/uploadFileBinary" {
		f.handleUploadFileBinary(w, r)
		return
	}
	if r.URL.Path == "/api/file" {
		f.handleFile(w, r)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	// Media-type fence (rpc-host.ts: only application/json).
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
			Error: &RPCError{Code: "gateway/bad-request", Message: "invalid client-request message", Details: json.RawMessage(`{"issues":[]}`)},
		})
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	if env.Method != method {
		writeServerResponse(w, env.RPCID, rpcResultBody{
			OK:    false,
			Error: &RPCError{Code: "gateway/bad-request", Message: `method "` + env.Method + `" does not match endpoint "` + method + `"`, Details: json.RawMessage(`{"issues":[]}`)},
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

	if method == "$events/result" {
		f.recordEventResult(env.Payload)
		if f.eventResultErr != nil {
			writeServerResponse(w, env.RPCID, rpcResultBody{OK: false, Error: f.eventResultErr})
			return
		}
		writeServerResponse(w, env.RPCID, rpcResultBody{OK: true})
		return
	}
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
		// Unknown-but-valid path: mirror the gateway's 404 for endpoints no
		// interceptor claims (claimsEndpoint: two slash segments or the
		// $events/result escape).
		http.NotFound(w, r)
		return
	}
	if scripted.err != nil {
		writeServerResponse(w, env.RPCID, rpcResultBody{OK: false, Error: scripted.err})
		return
	}
	writeServerResponse(w, env.RPCID, rpcResultBody{OK: true, Value: mustJSON(scripted.value)})
}

// recordEventResult captures one $events/result args body (the payload's
// single-args envelope, same fence as the real gateway).
func (f *fakeDSHServer) recordEventResult(payload json.RawMessage) {
	var envelope struct {
		Args json.RawMessage `json:"args"`
	}
	_ = json.Unmarshal(payload, &envelope)
	var args eventResultArgs
	if len(envelope.Args) > 0 {
		_ = json.Unmarshal(envelope.Args, &args)
	}
	f.lastEventResult.mu.Lock()
	f.lastEventResult.args = args
	f.lastEventResult.mu.Unlock()
	f.eventResults.mu.Lock()
	f.eventResults.list = append(f.eventResults.list, args)
	f.eventResults.mu.Unlock()
}

// handleRemoteMux mirrors the gateway's logical-stream mux: upgrade, then
// route client `open` frames by streamId and push scripted items.
func (f *fakeDSHServer) handleRemoteMux(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		// The gateway registers an upgrade route; a plain GET never matches.
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.upgradeSeen[r.URL.Path]++
	eventsFrames := f.eventsFrames
	wsBaseline := f.wsBaseline
	followScripts := f.followScripts
	closeAfter := f.closeAfterPush
	f.mu.Unlock()

	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	writeItem := func(streamID string, value any) bool {
		return conn.WriteJSON(muxServerMessage{Type: "item", StreamID: streamID, Value: mustJSON(value)}) == nil
	}
	// Reader: route open frames; anything else is ignored.
	go func() {
		for {
			var msg muxClientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type != "open" {
				continue
			}
			switch {
			case msg.StreamID == eventsStreamID:
				if !writeItem(eventsStreamID, map[string]any{
					"type": "ready", "clientId": "fake-client-1", "host": map[string]any{"home": "/Users/test"},
				}) {
					return
				}
				for _, frame := range eventsFrames {
					if !writeItem(eventsStreamID, frame) {
						return
					}
				}
				if closeAfter && len(followScripts) == 0 {
					// Drop-after-push for tests with no follow traffic; when
					// follow scripts exist the drop waits for their items so
					// the demand-driven follow opens are not cut short.
					_ = conn.Close()
					return
				}
			case msg.StreamID == wsStreamID:
				if wsBaseline != nil {
					if !writeItem(wsStreamID, map[string]any{"type": "baseline", "value": wsBaseline}) {
						return
					}
				}
			case strings.HasPrefix(msg.StreamID, "f:"):
				sid := strings.TrimPrefix(msg.StreamID, "f:")
				f.followOpens.mu.Lock()
				f.followOpens.byID[sid] = msg.Payload
				f.followOpens.mu.Unlock()
				script, ok := followScripts[sid]
				if !ok {
					_ = conn.WriteJSON(muxServerMessage{Type: "error", StreamID: msg.StreamID, Error: &RPCError{
						Code: "session/not-found", Message: `session "` + sid + `" not found`, Details: json.RawMessage(`{}`),
					}})
					continue
				}
				if script.snapshot != nil {
					if !writeItem(msg.StreamID, script.snapshot) {
						return
					}
				}
				for _, item := range script.items {
					if !writeItem(msg.StreamID, item) {
						return
					}
				}
				if closeAfter {
					_ = conn.Close()
					return
				}
			}
		}
	}()
	// Hold the socket open until the client closes (real server keeps the
	// mux alive; tests close the stream themselves).
	select {}
}

// SetEventsFrames scripts the $events items pushed after the ready frame.
func (f *fakeDSHServer) SetEventsFrames(frames []any) {
	f.mu.Lock()
	f.eventsFrames = frames
	f.mu.Unlock()
}

// SetEventResultErr scripts the business error answered to $events/result.
func (f *fakeDSHServer) SetEventResultErr(err *RPCError) {
	f.mu.Lock()
	f.eventResultErr = err
	f.mu.Unlock()
}

// SetWSBaseline scripts the workspace/follow baseline.
func (f *fakeDSHServer) SetWSBaseline(b *workspaceBaseline) {
	f.mu.Lock()
	f.wsBaseline = b
	f.mu.Unlock()
}

// SetFollowScript scripts one session's follow stream.
func (f *fakeDSHServer) SetFollowScript(sessionID string, script fakeFollowScript) {
	f.mu.Lock()
	f.followScripts[sessionID] = script
	f.mu.Unlock()
}

// FollowOpenPayload returns the recorded open payload of the session's most
// recent follow open (nil when none was seen).
func (f *fakeDSHServer) FollowOpenPayload(sessionID string) json.RawMessage {
	f.followOpens.mu.Lock()
	defer f.followOpens.mu.Unlock()
	return f.followOpens.byID[sessionID]
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
