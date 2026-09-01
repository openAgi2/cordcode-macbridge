package codexweb

// rpc.go keeps codex-web policy around the transport-neutral app-server RPC
// core. The algorithm is anchored to official codex-rs/app-server-client
// remote.rs and lib.rs. Lifecycle, transport acquisition, epoch ownership and
// diagnostics stay in this backend.

import (
	"context"
	"encoding/json"
	"fmt"

	appserverrpc "github.com/openAgi2/cordcode-macbridge/agent/codex-appserver/rpc"
)

type Request = appserverrpc.Request
type ServerRequest = appserverrpc.ServerRequest
type Notification = appserverrpc.Notification

// RPCError retains the historical codex-web presentation while the shared
// core owns correlation and framing.
type RPCError struct {
	Code    int64
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("codex app-server error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

// Client preserves the codex-web API and local-close topology policy.
type Client struct {
	inner *appserverrpc.Client
}

func NewClient(transport Transport, epoch ConnectionEpoch) *Client {
	return &Client{inner: appserverrpc.NewClient(transport, epoch, appserverrpc.Options{
		ErrorPrefix: "codexweb",
	})}
}

func (c *Client) Epoch() ConnectionEpoch { return c.inner.Epoch() }

// IsClosed intentionally preserves the pre-extraction codex-web role-state
// contract: only an explicit local Close marks this client closed.
func (c *Client) IsClosed() bool { return c.inner.IsLocallyClosed() }

func (c *Client) Notifications() <-chan Notification { return c.inner.Notifications() }

func (c *Client) ServerRequests() <-chan ServerRequest { return c.inner.ServerRequests() }

func (c *Client) pendingCount() int { return c.inner.PendingCount() }

func (c *Client) Request(method string, params any) (json.RawMessage, *RPCError, error) {
	return c.RequestContext(context.Background(), method, params)
}

func (c *Client) RequestContext(ctx context.Context, method string, params any) (json.RawMessage, *RPCError, error) {
	result, rpcErr, err := c.inner.RequestContext(ctx, method, params)
	return result, codexWebRPCError(rpcErr), err
}

func codexWebRPCError(err *appserverrpc.Error) *RPCError {
	if err == nil {
		return nil
	}
	return &RPCError{Code: err.Code, Message: err.Message, Data: err.Data}
}

func (c *Client) Notify(method string, params any) error {
	return c.inner.Notify(method, params)
}

func (c *Client) RespondServerRequest(id json.Number, result any) error {
	return c.inner.RespondServerRequest(id, result)
}

func (c *Client) RespondServerRequestError(id json.Number, code int64, message string) error {
	return c.inner.RespondServerRequestError(id, code, message)
}

func (c *Client) Close() error { return c.inner.Close() }
