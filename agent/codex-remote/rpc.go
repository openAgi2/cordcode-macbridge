package codexremote

// rpc.go — JSON-RPC correlation on a Transport.
//
// Provenance (plan §5.3 whitelist):
//   source repo: cordcode-macbridge
//   source path: agent/codex-web/rpc.go
//   source commit: c6fa9b853843e8682e94fd3f167d2e998cd2d0ce
//   copied: 2026-08-28
// Algorithm kept: single reader, pending request-id map, server-request
// channel, bounded Close. Transport is the Remote environment stream, not a
// daemon UDS socket. Comments about shared-daemon broadcast were removed.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Transport is the JSON-RPC byte pipe (Stream unwraps controller envelopes).
type Transport interface {
	Send(payload []byte) error
	Recv() ([]byte, error)
	Close() error
}

// ConnectionEpoch identifies one initialize/stream generation.
type ConnectionEpoch int64

// ServerRequest is an app-server-initiated request.
type ServerRequest struct {
	Epoch     ConnectionEpoch
	RequestID json.Number
	ThreadID  string
	TurnID    string
	Method    string
	Params    json.RawMessage
}

// Notification is an id-less server notification.
type Notification struct {
	Epoch  ConnectionEpoch
	Method string
	Params json.RawMessage
}

// RPCError keeps official JSON-RPC error code/message/data.
type RPCError struct {
	Code    int64
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("codex-remote app-server error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("codex-remote app-server error %d: %s", e.Code, e.Message)
}

const requestTimeout = 60 * time.Second

// Client is one JSON-RPC client on one Transport (reconnect = new Client + epoch).
type Client struct {
	epoch     ConnectionEpoch
	transport Transport

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcOutcome
	closed  bool

	notifications  chan Notification
	serverRequests chan ServerRequest
	readErr        error
	readErrOnce    sync.Once
	done           chan struct{}
}

type rpcOutcome struct {
	result json.RawMessage
	rpcErr *RPCError
	err    error
}

func NewClient(t Transport, epoch ConnectionEpoch) *Client {
	c := &Client{
		epoch:          epoch,
		transport:      t,
		pending:        make(map[int64]chan rpcOutcome),
		notifications:  make(chan Notification, 256),
		serverRequests: make(chan ServerRequest, 64),
		done:           make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) Epoch() ConnectionEpoch { return c.epoch }

// IsClosed must cover both local Close and read-loop death. A transport that
// died remotely leaves c.closed unset; supervision (watchBinding,
// keepStreamAlive) reconnects only when this flips, so reporting a dead
// client as open strands the backend until process restart (真机
// 2026-08-29 09:14：stream closed 80 分钟无重连).
func (c *Client) IsClosed() bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Client) Notifications() <-chan Notification { return c.notifications }

func (c *Client) ServerRequests() <-chan ServerRequest { return c.serverRequests }

func (c *Client) readLoop() {
	defer func() {
		close(c.notifications)
		close(c.serverRequests)
	}()
	for {
		payload, err := c.transport.Recv()
		if err != nil {
			c.readErrOnce.Do(func() { c.readErr = err })
			c.failAllPending(err)
			close(c.done)
			return
		}
		if len(payload) == 0 {
			continue
		}
		var env struct {
			ID     *json.Number    `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int64           `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			} `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			select {
			case c.notifications <- Notification{Epoch: c.epoch, Method: "__unparseable__", Params: json.RawMessage(payload)}:
			default:
			}
			continue
		}
		switch {
		case env.ID != nil && (env.Result != nil || env.Error != nil):
			id, err := env.ID.Int64()
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if env.Error != nil {
				ch <- rpcOutcome{rpcErr: &RPCError{Code: env.Error.Code, Message: env.Error.Message, Data: env.Error.Data}}
			} else {
				ch <- rpcOutcome{result: env.Result}
			}
		case env.ID != nil && env.Method != "":
			sr := ServerRequest{Epoch: c.epoch, RequestID: *env.ID, Method: env.Method, Params: env.Params}
			var p struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			_ = json.Unmarshal(env.Params, &p)
			sr.ThreadID, sr.TurnID = p.ThreadID, p.TurnID
			select {
			case c.serverRequests <- sr:
			case <-c.done:
				return
			}
		case env.Method != "":
			select {
			case c.notifications <- Notification{Epoch: c.epoch, Method: env.Method, Params: env.Params}:
			case <-c.done:
				return
			}
		}
	}
}

func (c *Client) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- rpcOutcome{err: fmt.Errorf("connection closed: %w", err)}
	}
}

func (c *Client) Request(method string, params any) (json.RawMessage, *RPCError, error) {
	return c.RequestContext(context.Background(), method, params)
}

func (c *Client) RequestContext(ctx context.Context, method string, params any) (json.RawMessage, *RPCError, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("codex-remote: client closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcOutcome, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	payload, err := json.Marshal(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, nil, err
	}
	if err := c.transport.Send(payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, nil, err
	}

	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	select {
	case out := <-ch:
		return out.result, out.rpcErr, out.err
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("codex-remote: request %s timed out after %s", method, requestTimeout)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("codex-remote: request %s canceled: %w", method, ctx.Err())
	case <-c.done:
		return nil, nil, fmt.Errorf("codex-remote: connection lost: %v", c.readErr)
	}
}

func (c *Client) Notify(method string, params any) error {
	n := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		n["params"] = params
	}
	payload, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return c.transport.Send(payload)
}

func (c *Client) RespondServerRequest(id json.Number, result any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		return err
	}
	return c.transport.Send(payload)
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	err := c.transport.Close()
	select {
	case <-c.done:
	case <-time.After(6 * time.Second):
	}
	return err
}
