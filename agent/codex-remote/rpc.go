package codexremote

// rpc.go keeps Remote lifecycle/reconnect policy around the shared app-server
// RPC core. Controller envelopes remain exclusively in Stream.

import (
	"context"
	"encoding/json"
	"fmt"

	appserverrpc "github.com/openAgi2/cordcode-macbridge/agent/codex-appserver/rpc"
)

type Transport = appserverrpc.Transport
type ConnectionEpoch = appserverrpc.Epoch
type ServerRequest = appserverrpc.ServerRequest
type Notification = appserverrpc.Notification

// RPCError retains the Remote-specific user/diagnostic presentation.
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

// Client preserves the Remote API and its stronger reconnect liveness policy.
type Client struct {
	inner *appserverrpc.Client
}

func NewClient(transport Transport, epoch ConnectionEpoch) *Client {
	return &Client{inner: appserverrpc.NewClient(transport, epoch, appserverrpc.Options{
		ErrorPrefix: "codex-remote",
	})}
}

func (c *Client) Epoch() ConnectionEpoch { return c.inner.Epoch() }

// IsClosed includes remote reader death because supervision reconnects from
// this signal.
func (c *Client) IsClosed() bool { return c.inner.IsTerminated() }

func (c *Client) Notifications() <-chan Notification { return c.inner.Notifications() }

func (c *Client) ServerRequests() <-chan ServerRequest { return c.inner.ServerRequests() }

func (c *Client) Request(method string, params any) (json.RawMessage, *RPCError, error) {
	return c.RequestContext(context.Background(), method, params)
}

func (c *Client) RequestContext(ctx context.Context, method string, params any) (json.RawMessage, *RPCError, error) {
	result, rpcErr, err := c.inner.RequestContext(ctx, method, params)
	return result, remoteRPCError(rpcErr), err
}

func remoteRPCError(err *appserverrpc.Error) *RPCError {
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

func (c *Client) RejectServerRequest(id json.Number, code int64, message string) error {
	return c.inner.RespondServerRequestError(id, code, message)
}

func (c *Client) Close() error { return c.inner.Close() }
