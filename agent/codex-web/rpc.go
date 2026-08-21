package codexweb

// rpc.go —— JSON-RPC 相关性与服务端请求（设计 §5.2/§7.2）。
//
// 移植母本（算法层）：codex-rs/app-server-client/src/lib.rs（request/notify/
// resolve_server_request/reject_server_request/next_event/shutdown）、remote.rs。
// 纪律（§3.4）：官方已有 request queue/event queue/server request registry 算法，不重新发明：
//   - 单 reader goroutine 按 arrival 顺序分发（ordered event queue）；
//   - pending 以 request id 关联（concurrent response 归位）；
//   - server-initiated request 进独立通道，响应必须回原 id；
//   - Close 有界（复用 transport 的有界关闭）。

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Request 一次 JSON-RPC 请求（id 相关性由 Client 维护）。
type Request struct {
	Method string
	Params any
}

// ServerRequest 是 app-server 发起的请求（审批/提问/elicitation）。registry key 至少
// 含 connection epoch + request id + threadId + turnId（§7.2）；response 回原 id；
// serverRequest/resolved 或 item completed 才是 UI 收口信号；断线清理旧 epoch pending，
// 不向新连接重放（Phase 0 dumps/reconnect 已证实）。
type ServerRequest struct {
	Epoch     ConnectionEpoch
	RequestID json.Number
	ThreadID  string
	TurnID    string
	Method    string
	Params    json.RawMessage
}

// Notification 是无 id 的服务端通知（events.go 的泵输入）。
type Notification struct {
	Epoch  ConnectionEpoch
	Method string
	Params json.RawMessage
}

// RPCError 保留官方 JSON-RPC error 的 code/message/data 原文（§7.1：面向 iOS 可本地化，
// 但不能丢原文）。
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

const requestTimeout = 60 * time.Second

// Client 是一条 transport 上的 JSON-RPC 客户端（一个连接一个实例；重连=新实例+新 epoch）。
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

// NewClient 在 transport 上启动 reader 分发循环；epoch 标识本连接代际。
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

// Epoch 返回本连接代际。
func (c *Client) Epoch() ConnectionEpoch { return c.epoch }

// Notifications 返回有序通知通道（reader 按到达顺序投递）。
func (c *Client) Notifications() <-chan Notification { return c.notifications }

// ServerRequests 返回服务端请求通道。
func (c *Client) ServerRequests() <-chan ServerRequest { return c.serverRequests }

func (c *Client) readLoop() {
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
			// 未识别帧：记录不崩溃（§7.1 红线）；此处无 logger 依赖，进入通知通道供诊断层观察。
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
			populateServerRequestIDs(&sr, env.Params)
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

func populateServerRequestIDs(sr *ServerRequest, raw json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	_ = json.Unmarshal(raw, &p)
	sr.ThreadID, sr.TurnID = p.ThreadID, p.TurnID
}

func (c *Client) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- rpcOutcome{err: fmt.Errorf("connection closed: %w", err)}
	}
}

// Request 发送 id 请求并等待响应（相关性与并发归位由 pending map 保证）。
func (c *Client) Request(method string, params any) (json.RawMessage, *RPCError, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("codexweb: client closed")
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

	select {
	case out := <-ch:
		return out.result, out.rpcErr, out.err
	case <-time.After(requestTimeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("codexweb: request %s timed out after %s", method, requestTimeout)
	case <-c.done:
		return nil, nil, fmt.Errorf("codexweb: connection lost: %v", c.readErr)
	}
}

// Notify 发送无 id 通知。
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

// RespondServerRequest 回复原 id 的服务端请求（§7.2：response 必须回原 JSON-RPC request id）。
func (c *Client) RespondServerRequest(id json.Number, result any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		return err
	}
	return c.transport.Send(payload)
}

// Close 有界关闭：关 transport → reader 结束 → 清空 pending。
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
