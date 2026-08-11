package gobridge

import (
	"testing"
)

// TestCapabilityPolicyAllowsFileScopedByDefault 验证 v2 policy 层接入且默认放行。
func TestCapabilityPolicyAllowsFileScopedByDefault(t *testing.T) {
	handlers := NewHandlers()
	conn := &readFileCaptureConn{}
	msg := WireMessage{RequestID: "req_cap", Method: "read_file_v2", BackendID: "codex"}

	// AuthorizeRPC 对 fileScopedMethods 默认返回 nil（放行），由下游 handler 校验。
	if perr := handlers.capabilityPolicy.AuthorizeRPC(conn, msg); perr != nil {
		t.Fatalf("AuthorizeRPC read_file_v2 默认应放行, got %#v", perr)
	}
}

// TestCapabilityPolicyIgnoresNonFileMethod 验证非文件方法不进入 policy 分支。
func TestCapabilityPolicyIgnoresNonFileMethod(t *testing.T) {
	p := NewCapabilityPolicy()
	conn := &readFileCaptureConn{}
	if perr := p.AuthorizeRPC(conn, WireMessage{Method: "list_sessions"}); perr != nil {
		t.Fatalf("list_sessions 不在 fileScopedMethods，应放行, got %#v", perr)
	}
}

// TestCapabilityPolicyNilSafe 验证 nil policy 不 panic（防御未来注入遗漏）。
func TestCapabilityPolicyNilSafe(t *testing.T) {
	var p *CapabilityPolicy
	conn := &readFileCaptureConn{}
	if perr := p.AuthorizeRPC(conn, WireMessage{Method: "read_file_v2"}); perr != nil {
		t.Fatalf("nil policy 应放行, got %#v", perr)
	}
}

// TestHandleRPCHooksCapabilityPolicy 验证 HandleRPC 集成了 v2 policy 与 capability gate。
func TestHandleRPCHooksCapabilityPolicy(t *testing.T) {
	handlers := NewHandlers()
	agent := &fakeAgent{name: "codex"}
	handlers.RegisterAgent("codex", agent)
	conn := &readFileCaptureConn{}
	handlers.eventPublisher.SetConnReadFileV2(conn, true)
	// 发送一个空参数 read_file_v2 → 走 policy/capability gate → strict codec 返回 invalid_params。
	handlers.HandleRPC(conn, WireMessage{
		Type: "request", RequestID: "req_hook", BackendID: "codex", Method: "read_file_v2",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	if conn.err == nil {
		t.Fatal("read_file_v2 空参数应返回错误（证明 policy hook 后正常 dispatch）")
	}
	if conn.err.Code != "invalid_params" {
		t.Fatalf("err code = %q, want invalid_params（policy 默认放行）", conn.err.Code)
	}
}

func TestRemovedReadFileReturnsMethodNotFound(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})
	conn := &readFileCaptureConn{}
	handlers.HandleRPC(conn, WireMessage{
		Type: "request", RequestID: "legacy-read", BackendID: "codex", Method: "read_file",
		Params: mustJSONRaw(t, map[string]any{"path": "/tmp/never-read"}),
	})
	if conn.err == nil || conn.err.Code != "method_not_found" {
		t.Fatalf("removed read_file returned data=%#v err=%+v, want method_not_found", conn.data, conn.err)
	}
}
