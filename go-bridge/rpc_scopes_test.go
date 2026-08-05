package gobridge

import (
	"strings"
	"testing"
)

// dispatchedRPCMethods 是 dispatchRPC（handlers.go）里每个 case 处理的方法名清单。
// 新增 dispatchRPC case 时必须同步：① 此清单 ② rpcScopeTable 的 scope 声明。
// TestEveryDispatchedRPCHasScope 会强制两者一致。
//
// 这是 §6.3 的 CI 门：任何被分发处理的 RPC 都必须在 scope 表里声明 scope
// （空串 = 无条件也算声明），否则一个新 RPC 可能悄悄绕过按方法鉴权。
var dispatchedRPCMethods = []string{
	"hello",
	"list_providers", "set_provider", "list_models", "list_agents",
	"list_permission_modes", "set_permission_mode",
	"create_session", "send_message", "abort_generation",
	"get_session", "get_session_messages", "get_session_projection",
	"delete_session", "resume_session", "switch_model", "resolve_permission",
	"list_sessions", "list_projects", "fetch_todos",
	"get_workspace_diff", "get_turn_diff", "get_full_thread_diff",
	"get_usage", "run_diagnostics", "list_memory_files", "read_memory_file",
	"fetch_content_chunk", "read_file", "list_directory", "get_git_context",
	"checkout_git_branch", "create_git_branch", "create_git_worktree",
	"rename_session", "share_session", "archive_session", "set_session_pinned",
	"list_pinned_sessions", "compress_context", "check_pending_notifications",
	"question_reply", "question_reject", "resolve_user_input",
}

// outOfSwitchRPCMethods 是 HandleRPC 里在 dispatchRPC 之前路由的方法（不走 dispatchRPC 的
// switch，但同样经过 AuthorizeRPC 单一漏斗）。它们也必须在 rpcScopeTable 声明 scope。
var outOfSwitchRPCMethods = []string{
	"set_observation_scope",      // handlers.go:837
	"get_delivery_prekey_status", // handleDeliveryRPC
	"upload_delivery_prekeys",    // handleDeliveryRPC
	"get_delivery_chain_head",    // handleDeliveryRPC
	"enable_relay_pairing",       // handleRelayUpgradeRPC (relay_upgrade.go)
}

func TestEveryDispatchedRPCHasScope(t *testing.T) {
	for _, m := range dispatchedRPCMethods {
		if _, ok := rpcScopeTable[m]; !ok {
			t.Errorf("dispatchRPC 方法 %q 未在 rpcScopeTable 声明 scope；新增 case 必须同时更新 dispatchedRPCMethods 与 rpcScopeTable（§6.3）", m)
		}
	}
	for _, m := range outOfSwitchRPCMethods {
		if _, ok := rpcScopeTable[m]; !ok {
			t.Errorf("switch 外 RPC 方法 %q 未在 rpcScopeTable 声明 scope（§6.3）", m)
		}
	}
}

func TestScopeTableCoversAllMethods(t *testing.T) {
	known := make(map[string]bool, len(dispatchedRPCMethods)+len(outOfSwitchRPCMethods))
	for _, m := range dispatchedRPCMethods {
		known[m] = true
	}
	for _, m := range outOfSwitchRPCMethods {
		known[m] = true
	}
	for method := range rpcScopeTable {
		if !known[method] {
			t.Errorf("rpcScopeTable 含僵尸条目 %q：既不在 dispatchRPC case 也不在 switch 外方法清单；方法被删时必须同步删 scope 条目（§6.3）", method)
		}
	}
}

func TestDefaultGrantedScopesHasAllSeven(t *testing.T) {
	got := DefaultGrantedScopes()
	want := map[string]bool{
		ScopeSessionRead: true, ScopeSessionWrite: true, ScopeConfigRead: true,
		ScopeConfigWrite: true, ScopeWorkspaceRead: true, ScopeWorkspaceMutate: true,
		ScopeDeliveryManage: true,
	}
	if len(got) != len(want) {
		t.Fatalf("DefaultGrantedScopes 应有 %d 个 scope，got %d (%v)", len(want), len(got), got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("DefaultGrantedScopes 含意外 scope %q", s)
		}
	}
}

func TestDeviceHasScope(t *testing.T) {
	// 空串 = 无条件
	if !deviceHasScope(nil, "") {
		t.Error("空 scope 应无条件放行")
	}
	// nil 设备（dev mode）→ 放行
	if !deviceHasScope(nil, ScopeSessionWrite) {
		t.Error("nil device（dev mode）应放行")
	}
	// GrantedScopes 空 = 全权（向后兼容旧持久化记录）
	if !deviceHasScope(&TrustedDeviceRecord{}, ScopeWorkspaceMutate) {
		t.Error("空 GrantedScopes 应回退为全权")
	}
	// 显式授予
	readOnly := &TrustedDeviceRecord{GrantedScopes: []string{ScopeSessionRead}}
	if !deviceHasScope(readOnly, ScopeSessionRead) {
		t.Error("显式授予 session.read 应放行")
	}
	if deviceHasScope(readOnly, ScopeSessionWrite) {
		t.Error("未授予 session.write 应拒绝")
	}
}

// scopeTestConn 只为 scope 单测提供 AuthedDevice；AuthorizeRPC 不应触碰其它方法。
type scopeTestConn struct {
	device *TrustedDeviceRecord
}

func (c *scopeTestConn) SendJSON(any)                               {}
func (c *scopeTestConn) SendResult(string, interface{}, *WireError) {}
func (c *scopeTestConn) AuthedDevice() *TrustedDeviceRecord         { return c.device }
func (c *scopeTestConn) RemoteAddr() string                         { return "test:scope" }
func (c *scopeTestConn) Close() error                               { return nil }

func TestAuthorizeRPCScopeGate(t *testing.T) {
	policy := NewCapabilityPolicy()

	// nil 设备（dev mode）→ 放行
	if err := policy.AuthorizeRPC(&scopeTestConn{device: nil}, WireMessage{Method: "read_file"}); err != nil {
		t.Fatalf("nil device 应放行（dev mode），got %v", err)
	}

	// 配对设备默认全权（GrantedScopes 空 = 全部 scope）
	full := &scopeTestConn{device: &TrustedDeviceRecord{}}
	if err := policy.AuthorizeRPC(full, WireMessage{Method: "send_message"}); err != nil {
		t.Fatalf("默认全权设备应放行 session.write（send_message），got %v", err)
	}
	if err := policy.AuthorizeRPC(full, WireMessage{Method: "read_file"}); err != nil {
		t.Fatalf("默认全权设备应放行 workspace.read（read_file），got %v", err)
	}

	// 受限设备：只读 iPad 场景，只授予 session.read
	readOnly := &scopeTestConn{device: &TrustedDeviceRecord{GrantedScopes: []string{ScopeSessionRead}}}
	if err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "list_sessions"}); err != nil {
		t.Fatalf("session.read 设备应放行 list_sessions，got %v", err)
	}

	// 写操作应被拒，错误码 forbidden 稳定
	err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "send_message"})
	if err == nil {
		t.Fatal("session.read-only 设备应被拒 send_message")
	}
	if err.Code != "forbidden" {
		t.Errorf("错误码应=forbidden，got %q（msg=%s）", err.Code, err.Message)
	}
	if !strings.Contains(err.Message, ScopeSessionWrite) {
		t.Errorf("错误信息应含被拒 scope %q，got %q", ScopeSessionWrite, err.Message)
	}

	// workspace.read 也应被拒（只读 iPad 不能读文件）
	if err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "read_file"}); err == nil || err.Code != "forbidden" {
		t.Errorf("session.read-only 设备读文件应 forbidden，got %v", err)
	}

	// 无条件方法（hello）任何设备都放行
	if err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "hello"}); err != nil {
		t.Errorf("hello 无条件应放行，got %v", err)
	}
	// 表外未知方法放行（由 dispatchRPC default → method_not_found 处理，不归 scope 门）
	if err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "no_such_method"}); err != nil {
		t.Errorf("表外方法应放行（method_not_found 由 dispatch 处理），got %v", err)
	}
}

func TestGrantedScopesForEcho(t *testing.T) {
	// nil device → 默认全集
	got := grantedScopesForEcho(nil)
	if len(got) != len(DefaultGrantedScopes()) {
		t.Errorf("nil device 应回显默认全集，got %v", got)
	}
	// 空 GrantedScopes → 默认全集（向后兼容）
	got = grantedScopesForEcho(&TrustedDeviceRecord{})
	if len(got) != len(DefaultGrantedScopes()) {
		t.Errorf("空 GrantedScopes 应回显默认全集，got %v", got)
	}
	// 显式受限 → 原样回显（且是副本，改不影响原 record）
	restricted := &TrustedDeviceRecord{GrantedScopes: []string{ScopeSessionRead}}
	got = grantedScopesForEcho(restricted)
	if len(got) != 1 || got[0] != ScopeSessionRead {
		t.Errorf("显式受限应原样回显 [session.read]，got %v", got)
	}
	got[0] = "tampered"
	if restricted.GrantedScopes[0] == "tampered" {
		t.Error("grantedScopesForEcho 应返回副本，不能让调用方改到原 record")
	}
}
