// Package dshweb implements the dsh-web backend: a forwarder onto the official
// DeepSeek Harness Web API plus a translator into the mature bridge-v1 formats
// (design docs/2026-08-16-dsh-web-backend-design.md v3.2).
//
// Wire facts below are pinned against the dsh source checkout at
// 47f9438 (packages/host/apiproxy/src/fetch/handler.ts, api/rpc.schema.ts,
// api/events.schema.ts; packages/client/connection/src/{index.ts,
// websocket-downlink.ts, api-request-trust.ts}) and live probes of a local
// rc.6 instance.
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// dialTimeout bounds one WS downlink dial (stream reconnects apply their own
// backoff on top).
const dialTimeout = 10 * time.Second

// ── Wire full forms (api/rpc.schema.ts) ─────────────────────────────────────
//
// Four envelope shapes exist on the wire; dshweb uses three of them (it never
// receives client-requests):
//
//	client-request  {type, rpcId, method, payload}                    — POST /api/<method>
//	server-response {type, rpcId, result:{ok,value}|{ok:false,error}} — unary reply (HTTP 200 always for business errors)
//	server-request  {type, rpcId, method, payload}                   — WS downlink frame; method == payload.type
//	client-response {type, rpcId, result}                             — POST /api/respond

// clientRequest is the ClientRequest full form sent on every unary POST.
type clientRequest struct {
	Type    string          `json:"type"` // always "client-request"
	RPCID   string          `json:"rpcId"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// clientResponse is the ClientResponse full form sent to POST /api/respond.
// The rpcId is echoed verbatim from the server-request frame being answered.
type clientResponse struct {
	Type   string        `json:"type"` // always "client-response"
	RPCID  string        `json:"rpcId"`
	Result rpcResultBody `json:"result"`
}

// rpcResultBody is the RpcResult slot of server-response/client-response.
// The value slot serializes with no field at all when absent (void business
// results omit it; api/rpc.schema.ts serverResponseSchema).
type rpcResultBody struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error *RPCError       `json:"error,omitempty"`
}

// serverResponse is the ServerResponse full form parsed from unary replies.
type serverResponse struct {
	Type   string        `json:"type"` // "server-response"
	RPCID  string        `json:"rpcId"`
	Result rpcResultBody `json:"result"`
}

// serverRequest is the ServerRequest full form of every WS downlink frame
// (websocket-downlink.ts serverRequest(): method = frame payload type).
type serverRequest struct {
	Type    string          `json:"type"` // "server-request"
	RPCID   string          `json:"rpcId"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// RPCError is the closed RpcError code set's wire body (api/rpc.schema.ts
// rpcErrorSchema). Message is preserved VERBATIM through every mapping — the
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

// respondReceipt is the POST /api/respond reply (rpcReceiptSchema): accepted,
// or refused with the carrier's reason.
type respondReceipt struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"` // "not-pending" | "bad-response"
}

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
}

// NewClient builds a client for baseURL. A nil httpClient gets a default with
// a short timeout; per-call contexts bound the actual deadlines.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
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
// transport/non-200 failures return *carrierError.
func (c *Client) Call(ctx context.Context, method string, payload any, out any) error {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dshweb: marshal payload for %s: %w", method, err)
		}
		raw = b
	} else {
		// Empty-object payloads still ride the envelope (zod object({}) accepts {}).
		raw = json.RawMessage("{}")
	}
	body, err := json.Marshal(clientRequest{
		Type:    "client-request",
		RPCID:   c.nextRPCID(),
		Method:  method,
		Payload: raw,
	})
	if err != nil {
		return fmt.Errorf("dshweb: marshal request for %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/"+method, bytes.NewReader(body))
	if err != nil {
		return &carrierError{Op: method, Wrapped: err}
	}
	// 415 fence: only application/json is accepted (fetch/handler.ts media check).
	req.Header.Set("Content-Type", "application/json")

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

// Respond answers one pending server-request (approval/question) via
// POST /api/respond, echoing the frame's rpcId verbatim. ok=false with a nil
// rpcErr encodes the cancelled path (question reject rides the error branch —
// asymmetric with approvals by design; questions.schema.ts / approvals.schema.ts).
// The bool return is the carrier receipt's accepted flag; a not-pending
// refusal (already answered elsewhere — first-writer-wins) returns
// (false, nil).
func (c *Client) Respond(ctx context.Context, rpcID string, ok bool, value any, rpcErr *RPCError) (bool, error) {
	cr := clientResponse{Type: "client-response", RPCID: rpcID}
	if ok {
		cr.Result.OK = true
		if value != nil {
			b, err := json.Marshal(value)
			if err != nil {
				return false, fmt.Errorf("dshweb: marshal respond value: %w", err)
			}
			cr.Result.Value = b
		}
	} else {
		if rpcErr == nil {
			rpcErr = &RPCError{Code: "cancelled", Message: "cancelled by client", Details: json.RawMessage("{}")}
		}
		cr.Result.OK = false
		cr.Result.Error = rpcErr
	}
	body, err := json.Marshal(cr)
	if err != nil {
		return false, fmt.Errorf("dshweb: marshal respond: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/respond", bytes.NewReader(body))
	if err != nil {
		return false, &carrierError{Op: "respond", Wrapped: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, &carrierError{Op: "respond", Wrapped: err}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return false, &carrierError{Op: "respond", Status: resp.StatusCode, Wrapped: err}
	}
	if resp.StatusCode != http.StatusOK {
		return false, &carrierError{Op: "respond", Status: resp.StatusCode, Detail: strings.TrimSpace(string(respBody))}
	}
	var receipt respondReceipt
	if err := json.Unmarshal(respBody, &receipt); err != nil {
		return false, &carrierError{Op: "respond", Status: resp.StatusCode, Detail: fmt.Sprintf("unparsable receipt: %v", err)}
	}
	return receipt.Accepted, nil
}

// unaryResponseLimit bounds one unary reply read. session.history pages are
// the largest payloads; 32MiB stops a corrupted stream from exhausting
// memory. History paging must keep each page under this (official
// maxMessages=50 default; a 200-message Exec-plan page was already 38MiB).
// Tests may lower this to prove oversize retry.
var unaryResponseLimit int64 = 32 << 20

const unaryOversizeDetail = "unary response exceeded size limit"

func isUnaryOversize(err error) bool {
	var ce *carrierError
	return err != nil && errors.As(err, &ce) && ce.Detail == unaryOversizeDetail
}

// ── WS downlink streams ─────────────────────────────────────────────────────
//
// GET /api/events.mux and GET /api/events.host are WebSocket upgrades
// (client/connection/src/index.ts registers them as upgrade routes; a plain
// GET answers 426). Streams are DOWNLINK ONLY: the server closes with 1008
// "downlink only" on any client message (websocket-downlink.ts), so this
// client never writes after the handshake. Every frame is a server-request
// envelope whose payload is the MuxFrame/HostFrame.

// Stream is one WS downlink (mux or host).
type Stream struct {
	conn *websocket.Conn
	// name labels logs ("mux"/"host").
	name string

	readMu sync.Mutex
}

// wsURL converts the instance base URL into the ws:// dial URL for path
// (/api/events.mux or /api/events.host).
func (c *Client) wsURL(path string) string {
	return "ws://" + strings.TrimPrefix(strings.TrimPrefix(c.BaseURL, "http://"), "https://") + path
}

// OpenStream dials one downlink stream. The returned Stream must be Closed.
func (c *Client) OpenStream(ctx context.Context, name, path string) (*Stream, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, c.wsURL(path), nil)
	if err != nil {
		return nil, &carrierError{Op: "dial " + path, Wrapped: err}
	}
	return &Stream{conn: conn, name: name}, nil
}

// Next reads the next server-request frame. It blocks until a frame arrives,
// the stream errors, or ctx ends. Errors are terminal: the official v1 has no
// `since` resume, so the caller's recovery is reopen + re-pull history
// (design §3.2).
func (s *Stream) Next(ctx context.Context) (*serverRequest, error) {
	// gorilla lacks context-aware reads; approximate by failing fast on ctx
	// and closing the socket underneath a blocked read.
	type readResult struct {
		frame *serverRequest
		err   error
	}
	done := make(chan readResult, 1)
	go func() {
		s.readMu.Lock()
		defer s.readMu.Unlock()
		var frame serverRequest
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
