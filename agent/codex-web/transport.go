package codexweb

// transport.go —— 字节传输与重连（设计 §5.2/§6.1）。
//
// 协议事实（Phase 0 实测 + unix_socket.rs accept_async）：daemon control socket 为
// WebSocket over Unix socket；每个 JSON-RPC 消息一个 WS text 帧。裸 newline JSON 仅适用于
// app-server stdio transport。官方 `app-server proxy --sock` 是纯字节中继，客户端仍需讲 WS。
//
// 安全（§11.1）：托管 WS 只绑 127.0.0.1；不把 control socket 暴露进 Relay。

// Transport 是与官方 app-server 的传输抽象（stdio newline JSON / WS-over-UDS / loopback WS）。
type Transport interface {
	// Send 写一个 JSON-RPC 消息帧；Recv 读下一帧；Close 关闭并递增连接 epoch。
	Send(payload []byte) error
	Recv() ([]byte, error)
	Close() error
}

// errNotImplemented 占位：骨架阶段行为未落地（Phase 1 起逐文件实现）。
var errNotImplemented = &notImplementedError{}

type notImplementedError struct{}

func (e *notImplementedError) Error() string { return "codexweb: not implemented (phase skeleton)" }
