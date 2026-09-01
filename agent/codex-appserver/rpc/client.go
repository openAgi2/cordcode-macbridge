// Package rpc contains the transport-neutral JSON-RPC client shared by Codex
// app-server backends. It deliberately owns no lifecycle, authentication,
// capability, transport dialing, codec, session or diagnostics policy.
//
// The algorithm follows the official Codex app-server client:
//   - codex-rs/app-server-client/src/remote.rs:216-472 (pending request routing)
//   - codex-rs/app-server-client/src/remote.rs:493-606 (public RPC/event surface)
//   - codex-rs/app-server-client/src/lib.rs:333-409 (request/event independence)
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Transport is one already-established app-server JSON-RPC byte pipe.
type Transport interface {
	Send(payload []byte) error
	Recv() ([]byte, error)
	Close() error
}

// Epoch identifies one backend-owned transport generation.
type Epoch int64

// Request is an app-server JSON-RPC request before framing.
type Request struct {
	Method string
	Params any
}

// ServerRequest is an app-server-initiated request that must be answered with
// its original request id.
type ServerRequest struct {
	Epoch     Epoch
	RequestID json.Number
	ThreadID  string
	TurnID    string
	Method    string
	Params    json.RawMessage
}

// Notification is an id-less app-server notification.
type Notification struct {
	Epoch  Epoch
	Method string
	Params json.RawMessage
}

// Error preserves an official JSON-RPC error payload.
type Error struct {
	Code    int64
	Message string
	Data    json.RawMessage
}

// Options holds presentation policy only. Transport/lifecycle policy remains
// in the backend wrapper.
type Options struct {
	ErrorPrefix    string
	RequestTimeout time.Duration
}

const (
	defaultRequestTimeout = 60 * time.Second
	notificationCapacity  = 256
	serverRequestCapacity = 64
)

type outcome struct {
	result json.RawMessage
	rpcErr *Error
	err    error
}

// Client owns correlation and framing for one transport generation.
type Client struct {
	epoch       Epoch
	transport   Transport
	errorPrefix string
	timeout     time.Duration

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan outcome
	closed  bool

	notifications  chan Notification
	serverRequests chan ServerRequest
	readErr        error
	readErrOnce    sync.Once
	done           chan struct{}
}

// NewClient starts the single reader for an already-established transport.
func NewClient(transport Transport, epoch Epoch, options Options) *Client {
	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	prefix := options.ErrorPrefix
	if prefix == "" {
		prefix = "codex-appserver"
	}
	c := &Client{
		epoch:          epoch,
		transport:      transport,
		errorPrefix:    prefix,
		timeout:        timeout,
		pending:        make(map[int64]chan outcome),
		notifications:  make(chan Notification, notificationCapacity),
		serverRequests: make(chan ServerRequest, serverRequestCapacity),
		done:           make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) Epoch() Epoch { return c.epoch }

// IsLocallyClosed reports whether Close was called by this process. It exists
// so codex-web can preserve its pre-extraction topology semantics.
func (c *Client) IsLocallyClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// IsTerminated also reports remote reader death. codex-remote supervision uses
// this stronger signal to trigger reconnect.
func (c *Client) IsTerminated() bool {
	if c.IsLocallyClosed() {
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

// PendingCount is a diagnostic/test observation; it does not expose requests.
func (c *Client) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

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
		var envelope struct {
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
		if err := json.Unmarshal(payload, &envelope); err != nil {
			select {
			case c.notifications <- Notification{Epoch: c.epoch, Method: "__unparseable__", Params: json.RawMessage(payload)}:
			default:
			}
			continue
		}
		switch {
		case envelope.ID != nil && (envelope.Result != nil || envelope.Error != nil):
			id, err := envelope.ID.Int64()
			if err != nil {
				continue
			}
			c.mu.Lock()
			response := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if response == nil {
				continue
			}
			if envelope.Error != nil {
				response <- outcome{rpcErr: &Error{
					Code: envelope.Error.Code, Message: envelope.Error.Message, Data: envelope.Error.Data,
				}}
			} else {
				response <- outcome{result: envelope.Result}
			}
		case envelope.ID != nil && envelope.Method != "":
			request := ServerRequest{
				Epoch: c.epoch, RequestID: *envelope.ID, Method: envelope.Method, Params: envelope.Params,
			}
			populateServerRequestIDs(&request, envelope.Params)
			select {
			case c.serverRequests <- request:
			case <-c.done:
				return
			}
		case envelope.Method != "":
			select {
			case c.notifications <- Notification{Epoch: c.epoch, Method: envelope.Method, Params: envelope.Params}:
			case <-c.done:
				return
			}
		}
	}
}

func populateServerRequestIDs(request *ServerRequest, raw json.RawMessage) {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	_ = json.Unmarshal(raw, &params)
	request.ThreadID, request.TurnID = params.ThreadID, params.TurnID
}

func (c *Client) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, response := range c.pending {
		delete(c.pending, id)
		response <- outcome{err: fmt.Errorf("connection closed: %w", err)}
	}
}

func (c *Client) Request(method string, params any) (json.RawMessage, *Error, error) {
	return c.RequestContext(context.Background(), method, params)
}

func (c *Client) RequestContext(ctx context.Context, method string, params any) (json.RawMessage, *Error, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("%s: client closed", c.errorPrefix)
	}
	c.nextID++
	id := c.nextID
	response := make(chan outcome, 1)
	c.pending[id] = response
	c.mu.Unlock()

	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	payload, err := json.Marshal(request)
	if err != nil {
		c.dropPending(id)
		return nil, nil, err
	}
	if err := c.transport.Send(payload); err != nil {
		c.dropPending(id)
		return nil, nil, err
	}

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case result := <-response:
		return result.result, result.rpcErr, result.err
	case <-timer.C:
		c.dropPending(id)
		return nil, nil, fmt.Errorf("%s: request %s timed out after %s", c.errorPrefix, method, c.timeout)
	case <-ctx.Done():
		c.dropPending(id)
		return nil, nil, fmt.Errorf("%s: request %s canceled: %w", c.errorPrefix, method, ctx.Err())
	case <-c.done:
		return nil, nil, fmt.Errorf("%s: connection lost: %v", c.errorPrefix, c.readErr)
	}
}

func (c *Client) dropPending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) Notify(method string, params any) error {
	notification := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		notification["params"] = params
	}
	payload, err := json.Marshal(notification)
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

func (c *Client) RespondServerRequestError(id json.Number, code int64, message string) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	if err != nil {
		return err
	}
	return c.transport.Send(payload)
}

// Close is bounded: transport close ends the reader and drains pending calls.
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
