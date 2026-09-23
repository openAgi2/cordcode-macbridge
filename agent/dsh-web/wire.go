// Package dshweb implements the dsh-web backend: a forwarder onto the official
// DeepSeek Harness Web API plus a translator into the mature bridge-v1 formats
// (design docs/2026-08-16-dsh-web-backend-design.md v3.2).
//
// Wire facts below are pinned against the typert-gateway generation of the
// dsh Web API (upstream checkout 0d1f50007f: packages/client/connection/src/
// {rpc.ts,rpc-host.ts,api-request-trust.ts}, packages/api/gateway/src/
// {index.ts,stream-protocol.ts}; live probes of the local 0.1.7-alpha.1
// instance 2026-09-23). Every installable registry version (latest
// 0.1.5-rc.2, next 0.1.5-rc.3, alpha 0.1.7-rc.*) serves this generation; the
// pre-2026-08-27 ApiProxy dot-endpoint surface (`host.describe`, `session.list`
// as method names) is gone and must not be reintroduced.
//
// This package is the designated successor workspace for the dsh-web route.
// It deliberately does NOT import agent/dsh: the §3.3 SessionEvent→core.Event
// codec is COPIED into this package (design §4.1/M3) so the legacy stdio
// driver can retire without dragging this one along.
//
// Package name note: the directory is agent/dsh-web (owner-mandated name with
// a hyphen); Go package identifiers cannot contain hyphens, so the package is
// dshweb, the registration name is "dsh-web", and the wire kind is
// "deepseek-web".
package dshweb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// dialTimeout bounds one WS downlink dial (stream reconnects apply their own
// backoff on top).
const dialTimeout = 10 * time.Second

// ── Wire full forms (client/connection/src/rpc.ts) ───────────────────────────
//
// Three envelope shapes exist on the wire; dshweb uses all of them:
//
//	client-request  {type, rpcId, method, payload}                    — POST /api/<method>
//	server-response {type, rpcId, result:{ok,value}|{ok:false,error}} — unary reply (HTTP 200 always for business errors)
//	remote.mux WS   {type:"item"|"error"|"end", streamId, value?}     — Host→client logical-stream frames
//
// Endpoint naming: methods are SLASH-separated (`session/list`, never
// `session.list`); rpc-host.ts endpointFromPath splits on '/' and the gateway
// claimsEndpoint rejects anything that is not exactly two non-empty segments.
// The envelope's `method` must equal the path endpoint verbatim
// (rpc-host.ts rpcFetchHandler: mismatch → gateway/bad-request).
//
// Payload shape: the payload is EXACTLY ONE plain-object `args` field
// (gateway dispatchRpc: "Remote payload must contain exactly one
// plain-object args field") whose keys are the descriptor's parameter wire
// names — `request` for most session/* methods, `_request` for session/list,
// `ns`/`patch` for settings/update, and no fields for zero-parameter methods
// (session/modelCatalog, llm/*, settings/describe, agentPresets/list,
// workspace/follow). Client.Call wraps the caller's param-keyed object in
// the args envelope; callers never build the wrapper themselves.

// clientRequest is the ClientRequest full form sent on every unary POST.
type clientRequest struct {
	Type    string          `json:"type"` // always "client-request"
	RPCID   string          `json:"rpcId"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// serverResponse is the ServerResponse full form parsed from unary replies.
type serverResponse struct {
	Type   string        `json:"type"` // "server-response"
	RPCID  string        `json:"rpcId"`
	Result rpcResultBody `json:"result"`
}

// eventResultArgs is the $events/result payload (gateway/src/index.ts
// parseRemoteEventResultPayload: exactly one plain-object `args` field with
// clientId + eventId + outcome). The clientId comes from the $events stream's
// ready frame; the eventId from the waterfall frame being answered.
type eventResultArgs struct {
	ClientID string           `json:"clientId"`
	EventID  string           `json:"eventId"`
	Outcome  eventOutcomeBody `json:"outcome"`
}

// eventOutcomeBody is the closed outcome union (stream-protocol.ts
// parseRemoteEventResult): result carries the answer value; rejected carries
// the wire-safe error fields. `next` is the official delegation outcome —
// this client always claims or rejects, never delegates.
type eventOutcomeBody struct {
	Kind  string          `json:"kind"` // "result" | "rejected"
	Value json.RawMessage `json:"value,omitempty"`
	Error *eventRejection `json:"error,omitempty"`
}

// eventRejection is the wire-safe error shape retained when a client listener
// rejects a Host waterfall (stream-protocol.ts RemoteEventRejection).
type eventRejection struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// rpcResultBody is the RpcResult slot of server-response/client-response.
// The value slot serializes with no field at all when absent (void business
// results omit it; api/rpc.schema.ts serverResponseSchema).
type rpcResultBody struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error *RPCError       `json:"error,omitempty"`
}

// RPCError is the closed RpcError code set's wire body (gateway failure
// vocabulary). Message is preserved VERBATIM through every mapping — the
// 坑 7 red line: collapsing or rewriting official error text hides the actual
// failure cause (encoding conflicts, model whitelist misses) from logs and
// from iOS error bubbles.
type RPCError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

// Error renders the official code+message without paraphrasing.
func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("dsh rpc error %s: %s", e.Code, e.Message)
}

// respondReceipt was the POST /api/respond reply of the retired ApiProxy
// generation; the typert gateway has no equivalent — $events/result failures
// arrive as ordinary result.error RPCErrors.

// carrierError marks HTTP-layer (carrier) failures: non-200 statuses from the
// /api gateway — 404 unknown path, 415 non-JSON content type, 400 non-JSON
// body, 500 handler crash, plus dial/transport errors. Business failures are
// NEVER carrier errors; they arrive as HTTP 200 + result.error (RPCError).
type carrierError struct {
	Op      string
	Status  int // 0 = transport failure (no status)
	Detail  string
	Wrapped error
}

func (e *carrierError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("dsh api carrier error (%s): HTTP %d: %s", e.Op, e.Status, e.Detail)
	}
	return fmt.Sprintf("dsh api carrier error (%s): %v", e.Op, e.Wrapped)
}

func (e *carrierError) Unwrap() error { return e.Wrapped }

// ── Client: HTTP unary + respond + WS downlinks ─────────────────────────────

// Client talks to one resolved dsh web instance over its /api gateway.
// BaseURL is an http:// URL on loopback (the trust fence — Host header must be
// loopback; requests from this process carry no Origin, which the fence
// explicitly allows).
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:3080 (no trailing slash)

	httpClient *http.Client
	rpcSeq     atomic.Int64
	rpcPrefix  string

	// auth, when set, supplies the browser-session cookie dsh ≥0.1.6-alpha
	// requires on every /api request (unary + WS upgrade) and refreshes it
	// (single-flight) on 401. Nil = pre-auth dsh / tests — no cookie, no
	// retry, behavior identical to before the auth integration.
	auth *seatAuth
}

// SetAuth attaches the seat's browser-session auth (2026-09-23 plan §4.2).
func (c *Client) SetAuth(a *seatAuth) { c.auth = a }

// applyCookie adds the session cookie header when one exists for this seat.
func (c *Client) applyCookie(h http.Header) {
	if c.auth == nil {
		return
	}
	if value, ok := c.auth.cookieHeaderFor(c.BaseURL); ok {
		h.Set("Cookie", value)
	}
}

// retryOnAuth refreshes the cookie after a 401 and reports whether the caller
// should retry once. Only one refresh+retry per call — no loops.
func (c *Client) retryOnAuth(ctx context.Context, err error) bool {
	if c.auth == nil {
		return false
	}
	var ce *carrierError
	if !errors.As(err, &ce) || ce.Status != http.StatusUnauthorized {
		return false
	}
	if rerr := c.auth.ensureCookie(ctx, c.BaseURL, true); rerr != nil {
		return false
	}
	// The refresh may have produced nothing new (e.g. minted but still
	// rejected) — only retry when the cookie actually changed.
	_, ok := c.auth.cookieHeaderFor(c.BaseURL)
	return ok
}

// defaultUnaryTimeout bounds one unary /api call when the caller's context
// carries no deadline. Var so tests can shrink it (rework ⑨ regression).
var defaultUnaryTimeout = 30 * time.Second

// unaryCtx picks the deadline for a unary call: the caller's own context when
// it already carries one, else the package default above. The shared default
// client deliberately sets NO http.Client.Timeout — that field is a hard cap
// per-request contexts can only shorten, never extend, and it silently clipped
// commands/execute at 30s while the handler allowed 90s+ (owner 2026-09-06
// 02:17 /compact: Mac aborted the POST at exactly 30s "Client.Timeout exceeded
// while awaiting headers", official execute's request-owned signal then killed
// the compaction seat-side "This operation was aborted"; rework ⑨).
func unaryCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultUnaryTimeout)
}

// NewClient builds a client for baseURL. A nil httpClient gets a default with
// no blanket timeout; unary calls are bounded per-call (see unaryCtx).
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		rpcPrefix:  randomID("c"),
	}
}

func randomID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure leaves ids collision-prone but functional.
		return fmt.Sprintf("%s-fallback", prefix)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// nextRPCID mints an opaque echo token (rpcIdSchema: any string).
func (c *Client) nextRPCID() string {
	return fmt.Sprintf("%s-%d", c.rpcPrefix, c.rpcSeq.Add(1))
}

// Call performs one unary RPC: POST /api/<method> with the ClientRequest
// envelope. On success (result.ok) the business value is unmarshaled into out
// (out may be nil to discard). Business failures return *RPCError verbatim;
// transport/non-200 failures return *carrierError. A 401 triggers one
// cookie refresh + retry (2026-09-23 plan §4.5) when auth is attached.
func (c *Client) Call(ctx context.Context, method string, payload any, out any) error {
	err := c.callOnce(ctx, method, payload, out)
	if c.retryOnAuth(ctx, err) {
		err = c.callOnce(ctx, method, payload, out)
	}
	return err
}

func (c *Client) callOnce(ctx context.Context, method string, payload any, out any) error {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dshweb: marshal payload for %s: %w", method, err)
		}
		raw = b
	} else {
		// Zero-parameter methods ride an empty args object.
		raw = json.RawMessage("{}")
	}
	// The gateway's single-args fence: wrap the param-keyed object in exactly
	// one plain-object `args` field (live-probed 2026-09-23: a bare
	// {"request":…} payload is rejected with gateway/internal).
	body, err := json.Marshal(clientRequest{
		Type:    "client-request",
		RPCID:   c.nextRPCID(),
		Method:  method,
		Payload: mustWrapArgs(raw),
	})
	if err != nil {
		return fmt.Errorf("dshweb: marshal request for %s: %w", method, err)
	}

	ctx, cancel := unaryCtx(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/"+method, bytes.NewReader(body))
	if err != nil {
		return &carrierError{Op: method, Wrapped: err}
	}
	// 415 fence: only application/json is accepted (fetch/handler.ts media check).
	req.Header.Set("Content-Type", "application/json")
	c.applyCookie(req.Header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &carrierError{Op: method, Wrapped: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, unaryResponseLimit+1))
	if err != nil {
		return &carrierError{Op: method, Status: resp.StatusCode, Wrapped: err}
	}
	if int64(len(respBody)) > unaryResponseLimit {
		return &carrierError{Op: method, Status: resp.StatusCode, Detail: unaryOversizeDetail}
	}
	if resp.StatusCode != http.StatusOK {
		// Carrier layer speaks plain text bodies ("content type must be
		// application/json", "body is not JSON", "not found", handler crash).
		return &carrierError{Op: method, Status: resp.StatusCode, Detail: strings.TrimSpace(string(respBody))}
	}

	var sr serverResponse
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return &carrierError{Op: method, Status: resp.StatusCode, Detail: fmt.Sprintf("unparsable server-response: %v", err)}
	}
	if sr.Type != "server-response" {
		return &carrierError{Op: method, Status: resp.StatusCode, Detail: fmt.Sprintf("unexpected envelope type %q", sr.Type)}
	}
	if !sr.Result.OK {
		if sr.Result.Error == nil {
			return &carrierError{Op: method, Status: resp.StatusCode, Detail: "ok:false without error body"}
		}
		// 坑 7: pass the official RpcError through untouched.
		return sr.Result.Error
	}
	if out != nil && len(sr.Result.Value) > 0 {
		if err := json.Unmarshal(sr.Result.Value, out); err != nil {
			return fmt.Errorf("dshweb: decode %s value: %w", method, err)
		}
	}
	return nil
}

// fileUploadValue mirrors the official FileUploadValue (file-upload/src/
// types.ts:15-19): the opaque per-agent-scope receipt plus the durable
// content-addressed file reference.
type fileUploadValue struct {
	ReceiptID string `json:"receiptId"`
	File      struct {
		AttachmentID string `json:"attachmentId"`
		Name         string `json:"name"`
		Bytes        int64  `json:"bytes"`
	} `json:"file"`
}

// fileUploadHttpResult is the raw route's JSON envelope (file-upload/src/
// http-route.ts FileUploadHttpResult) — NOT the client-request server-response
// envelope: {ok:true, value} | {ok:false, error:{code,message,details}}.
type fileUploadHttpResult struct {
	OK    bool            `json:"ok"`
	Value *fileUploadValue `json:"value,omitempty"`
	Error *RPCError        `json:"error,omitempty"`
}

// UploadFileBinary stages one file on the session via the official raw-byte
// route: POST /api/session/uploadFileBinary?sessionId=&name= with an
// application/octet-stream body (file-upload/src/protocol.ts:2
// FILE_UPLOAD_PATH; http-route.ts:22-60 — streaming, NOT the JSON RPC
// envelope). Returns the staged receipt; the prompt's {type:'file',receiptId}
// part resolves it seat-side (commands.ts:582-601). Business failures return
// *RPCError verbatim; a 401 triggers one cookie refresh + retry.
func (c *Client) UploadFileBinary(ctx context.Context, sessionID, name string, data []byte) (*fileUploadValue, error) {
	val, err := c.uploadFileBinaryOnce(ctx, sessionID, name, data)
	if c.retryOnAuth(ctx, err) {
		val, err = c.uploadFileBinaryOnce(ctx, sessionID, name, data)
	}
	return val, err
}

func (c *Client) uploadFileBinaryOnce(ctx context.Context, sessionID, name string, data []byte) (*fileUploadValue, error) {
	u := c.BaseURL + "/api/session/uploadFileBinary?sessionId=" + url.QueryEscape(sessionID)
	if name != "" {
		u += "&name=" + url.QueryEscape(name)
	}
	ctx, cancel := unaryCtx(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return nil, &carrierError{Op: "uploadFileBinary", Wrapped: err}
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.applyCookie(req.Header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &carrierError{Op: "uploadFileBinary", Wrapped: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, unaryResponseLimit+1))
	if err != nil {
		return nil, &carrierError{Op: "uploadFileBinary", Status: resp.StatusCode, Wrapped: err}
	}
	if int64(len(body)) > unaryResponseLimit {
		return nil, &carrierError{Op: "uploadFileBinary", Status: resp.StatusCode, Detail: unaryOversizeDetail}
	}
	if resp.StatusCode != http.StatusOK {
		// The raw route speaks plain text on media/query rejections
		// ("content type must be application/octet-stream", "sessionId is required").
		return nil, &carrierError{Op: "uploadFileBinary", Status: resp.StatusCode,
			Detail: strings.TrimSpace(string(body))}
	}
	var fr fileUploadHttpResult
	if err := json.Unmarshal(body, &fr); err != nil {
		return nil, &carrierError{Op: "uploadFileBinary", Status: resp.StatusCode,
			Detail: fmt.Sprintf("unparsable upload result: %v", err)}
	}
	if !fr.OK {
		if fr.Error == nil {
			return nil, &carrierError{Op: "uploadFileBinary", Status: resp.StatusCode,
				Detail: "ok:false without error body"}
		}
		return nil, fr.Error // official RpcError verbatim (坑 7)
	}
	if fr.Value == nil || fr.Value.ReceiptID == "" {
		return nil, &carrierError{Op: "uploadFileBinary", Status: resp.StatusCode,
			Detail: "ok:true without receiptId"}
	}
	return fr.Value, nil
}

// RespondEventResult answers one pending Host waterfall (approval/question)
// via POST /api/$events/result, correlating by the $events generation's
// clientId + the waterfall's eventId (stream-protocol.ts RemoteEventResult).
// value is the official answer payload (approval: the outcome string;
// question: {answers:[…]}); a nil value with reject=true sends the rejected
// outcome branch. A result.error naming "no active event stream" means the
// waterfall was already answered/cancelled elsewhere (first-writer-wins) —
// returned as *RPCError for the caller to treat as settled-elsewhere.
func (c *Client) RespondEventResult(ctx context.Context, clientID, eventID string, value any, reject bool) error {
	err := c.respondEventOnce(ctx, clientID, eventID, value, reject)
	if c.retryOnAuth(ctx, err) {
		err = c.respondEventOnce(ctx, clientID, eventID, value, reject)
	}
	return err
}

func (c *Client) respondEventOnce(ctx context.Context, clientID, eventID string, value any, reject bool) error {
	outcome := eventOutcomeBody{Kind: "result"}
	if reject {
		outcome = eventOutcomeBody{
			Kind:  "rejected",
			Error: &eventRejection{Name: "Error", Message: "cancelled by client"},
		}
	} else if value != nil {
		b, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("dshweb: marshal event result value: %w", err)
		}
		outcome.Value = b
	}
	args, err := json.Marshal(eventResultArgs{ClientID: clientID, EventID: eventID, Outcome: outcome})
	if err != nil {
		return fmt.Errorf("dshweb: marshal $events/result args: %w", err)
	}
	body, err := json.Marshal(clientRequest{
		Type:    "client-request",
		RPCID:   c.nextRPCID(),
		Method:  "$events/result",
		Payload: mustWrapArgs(args),
	})
	if err != nil {
		return fmt.Errorf("dshweb: marshal $events/result: %w", err)
	}

	ctx, cancel := unaryCtx(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/$events/result", bytes.NewReader(body))
	if err != nil {
		return &carrierError{Op: "$events/result", Wrapped: err}
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyCookie(req.Header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &carrierError{Op: "$events/result", Wrapped: err}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return &carrierError{Op: "$events/result", Status: resp.StatusCode, Wrapped: err}
	}
	if resp.StatusCode != http.StatusOK {
		return &carrierError{Op: "$events/result", Status: resp.StatusCode, Detail: strings.TrimSpace(string(respBody))}
	}
	var sr serverResponse
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return &carrierError{Op: "$events/result", Status: resp.StatusCode, Detail: fmt.Sprintf("unparsable server-response: %v", err)}
	}
	if !sr.Result.OK {
		if sr.Result.Error == nil {
			return &carrierError{Op: "$events/result", Status: resp.StatusCode, Detail: "ok:false without error body"}
		}
		return sr.Result.Error
	}
	return nil
}

// unaryResponseLimit bounds one unary reply read. session.history pages are
// the largest payloads; 32MiB stops a corrupted stream from exhausting
// memory. History paging must keep each page under this (official
// maxMessages=50 default; a 200-message Exec-plan page was already 38MiB).
// Tests may lower this to prove oversize retry.
var unaryResponseLimit int64 = 32 << 20

const unaryOversizeDetail = "unary response exceeded size limit"

// mustWrapArgs wraps one param-keyed object in the gateway's single-args
// envelope. A marshal failure of the pre-marshaled raw bytes cannot occur.
func mustWrapArgs(raw json.RawMessage) json.RawMessage {
	b, err := json.Marshal(map[string]json.RawMessage{"args": raw})
	if err != nil {
		return json.RawMessage(`{"args":{}}`)
	}
	return b
}

func isUnaryOversize(err error) bool {
	var ce *carrierError
	return err != nil && errors.As(err, &ce) && ce.Detail == unaryOversizeDetail
}

// ── WS remote.mux (gateway/src/stream-protocol.ts) ───────────────────────────
//
// GET /api/remote.mux is ONE WebSocket upgrade carrying every logical stream
// (REMOTE_STREAM_MUX_PATH). The socket is BIDIRECTIONAL: the client opens
// logical streams and the Host frames their items on the same socket.
//
//	Client → Host: {type:"open", streamId, endpoint, payload} | {type:"cancel", streamId}
//	Host → Client: {type:"item", streamId, value?} | {type:"error", streamId, error} | {type:"end", streamId}
//
// The payload of one `open` is the endpoint's ordinary args object (the same
// shape as a unary Call payload). parseRemoteStreamClientMessage enforces
// exact key sets on every frame — no extra fields.

// muxClientMessage is one client→Host control message.
type muxClientMessage struct {
	Type     string          `json:"type"` // "open" | "cancel"
	StreamID string          `json:"streamId"`
	Endpoint string          `json:"endpoint,omitempty"` // open only
	Payload  json.RawMessage `json:"payload,omitempty"`  // open only
}

// muxServerMessage is one Host→client frame.
type muxServerMessage struct {
	Type     string          `json:"type"` // "item" | "error" | "end"
	StreamID string          `json:"streamId"`
	Value    json.RawMessage `json:"value,omitempty"`
	Error    *RPCError       `json:"error,omitempty"`
}

// Stream is one remote.mux WebSocket carrying many logical streams.
type Stream struct {
	conn *websocket.Conn
	// name labels logs ("mux").
	name string

	readMu  sync.Mutex
	writeMu sync.Mutex
}

// wsURL converts the instance base URL into the ws:// dial URL for path
// (/api/remote.mux).
func (c *Client) wsURL(path string) string {
	return "ws://" + strings.TrimPrefix(strings.TrimPrefix(c.BaseURL, "http://"), "https://") + path
}

// OpenStream dials the remote.mux socket. The upgrade carries the
// browser-session cookie (the trust fence + browser auth cover WS upgrades of
// /api routes too); a 401 refreshes once and retries.
func (c *Client) OpenStream(ctx context.Context, name, path string) (*Stream, error) {
	stream, err := c.openStreamOnce(ctx, name, path)
	if err != nil && c.retryOnAuth(ctx, err) {
		stream, err = c.openStreamOnce(ctx, name, path)
	}
	return stream, err
}

func (c *Client) openStreamOnce(ctx context.Context, name, path string) (*Stream, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	header := http.Header{}
	c.applyCookie(header)
	conn, resp, err := websocket.DefaultDialer.DialContext(dialCtx, c.wsURL(path), header)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, &carrierError{Op: "dial " + path, Status: resp.StatusCode, Detail: "unauthorized"}
		}
		return nil, &carrierError{Op: "dial " + path, Wrapped: err}
	}
	return &Stream{conn: conn, name: name}, nil
}

// Send writes one client→Host control message (stream open/cancel).
func (s *Stream) Send(m muxClientMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(m)
}

// Next reads the next Host frame. It blocks until a frame arrives, the socket
// errors, or ctx ends. Errors are terminal: the caller's recovery is reopen +
// re-open every logical stream + re-pull history (the streams carry no resume
// cursor; session/follow's opening snapshot is the reconcile).
func (s *Stream) Next(ctx context.Context) (*muxServerMessage, error) {
	// gorilla lacks context-aware reads; approximate by failing fast on ctx
	// and closing the socket underneath a blocked read.
	type readResult struct {
		frame *muxServerMessage
		err   error
	}
	done := make(chan readResult, 1)
	go func() {
		s.readMu.Lock()
		defer s.readMu.Unlock()
		var frame muxServerMessage
		if err := s.conn.ReadJSON(&frame); err != nil {
			done <- readResult{nil, err}
			return
		}
		done <- readResult{&frame, nil}
	}()
	select {
	case <-ctx.Done():
		// Unblock the reader by closing the socket; the goroutine's write to
		// the buffered channel is dropped.
		_ = s.conn.Close()
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return nil, &carrierError{Op: "read " + s.name, Wrapped: r.err}
		}
		return r.frame, nil
	}
}

// Close tears the socket down.
func (s *Stream) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}
