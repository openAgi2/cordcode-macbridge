package codexweb

// rpc.go —— JSON-RPC 相关性与服务端请求（设计 §5.2/§7.2）。
//
// 移植母本（算法层，注释记录上游 file/commit）：codex-rs/app-server-client/src/lib.rs
// （request/notify/resolve_server_request/reject_server_request/next_event/shutdown）、
// remote.rs（RemoteAppServerClient）。
//
// 纪律（§3.4）：官方已有 request queue/event queue/server request registry/reconnect 算法，
// 不得重新发明。

// Request 一次 JSON-RPC 请求（id 相关性由 rpcClient 维护）。
type Request struct {
	Method string
	Params any
}

// ServerRequest 是 app-server 发起的请求（审批/提问/elicitation）；registry key 至少
// 含 connection epoch + request id + threadId + turnId（§7.2），response 回原 id；
// serverRequest/resolved 或 item completed 才是 UI 收口信号；断线清理旧 epoch pending，
// 不向新连接重放（Phase 0 dumps/reconnect 已证实）。
type ServerRequest struct {
	Epoch     ConnectionEpoch
	RequestID int64
	ThreadID  string
	TurnID    string
	Method    string
	Params    any
}
