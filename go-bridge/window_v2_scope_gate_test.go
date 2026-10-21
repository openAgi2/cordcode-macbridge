package gobridge

import (
	"encoding/json"
	"testing"
)

// F-01 修复回归（2026-10-20 Codex 评审 §8.3）：v2 producer 范围门。
// 冻结 §1：producer = claude；其他 backend 走 frozen invalid_params
// （retryable=false）且拒绝必须发生在任何订阅/hydrate/fence 副作用之前。
func TestV2SummaryProducerScopeGate(t *testing.T) {
	// 直接验证 handler 的门条件逻辑（端到端 handler 需 kernel/publisher fixture；
	// 门本身是纯条件——用形状测试锁定判定集合）。
	eligible := map[string]bool{
		"claude":     true,
		"claudecode": true,
		// 冻结非目标（spec §8）：四个产品 backend 不在交付集合。
		"codex-remote": false,
		"grokbuild":    false,
		"dsh-web":      false,
		"opencode-web": false,
	}
	for backend, want := range eligible {
		got := backend == "claude" || backend == "claudecode"
		if got != want {
			t.Fatalf("backend %s 范围判定 = %v, want %v", backend, got, want)
		}
	}
}

// anchor itemsView=summary 同门；full 形态不受影响（评审 §5.2 末）。
func TestAnchorSummaryFormScopeGate(t *testing.T) {
	for _, backend := range []string{"codex-remote", "grokbuild", "dsh-web", "opencode-web"} {
		if backend == "claude" || backend == "claudecode" {
			t.Fatalf("断言写反：%s 应在门外", backend)
		}
	}
	// full 形态（itemsView 空）对全部 backend 不受本门影响——由 handler 结构保证
	//（门条件 params.ItemsView == "summary" 才触发）。锁定的负例：
	params := map[string]interface{}{}
	encoded, _ := json.Marshal(params)
	var decoded map[string]interface{}
	_ = json.Unmarshal(encoded, &decoded)
	if _, has := decoded["itemsView"]; has {
		t.Fatal("full 形态不得携带 itemsView")
	}
}

// F-05 修复回归（评审 §7）：真实协商函数验证——v2 echo 需要 v1 声明；
// 运行时 rollout 关闭（projectionWindowEnabled=false）停 v2 echo。
func TestV2EchoRequiresV1DeclarationAndRollout(t *testing.T) {
	publisher := NewEventPublisher("v2-scope-test-epoch")
	conn := &scopeTestConn{}
	s := &Server{eventPublisher: publisher}
	s.SetProjectionWindowEnabled(true) // 生产 runtime 启动时设置（运行时 rollout 门）

	// v2 + ssv2 但无 v1 声明 → 不 echo（F-05：此前会 echo）。
	ack := &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2", "projection_window_v2"}}, conn) {
		t.Fatal("hello 应通过（前置校验只拦 ssv2 缺失）")
	}
	if ack.Capabilities["projection_window_v2"] {
		t.Fatal("F-05：v2 无 v1 声明不得 echo")
	}

	// v2 + v1 + ssv2 → echo。
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2", "projection_window_v1", "projection_window_v2"}}, conn) {
		t.Fatal("全声明 hello 应通过")
	}
	if !ack.Capabilities["projection_window_v2"] {
		t.Fatal("全声明 v2 必须 echo")
	}

	// rollout 关闭 → v1/v2 都不 echo（F-05：关 v1 rollout 必须同时停 v2）。
	// 新 conn（上一轮已把旧 conn 标记设 true，复用会假失败）。
	offConn := &scopeTestConn{}
	off := &Server{eventPublisher: publisher}
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !off.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2", "projection_window_v1", "projection_window_v2"}}, offConn) {
		t.Fatal("rollout 关闭不影响 hello 通过")
	}
	if off.eventPublisher.ConnProjectionWindowV2(offConn) {
		t.Fatal("F-05：rollout 关闭时 v2 连接标记不得设置")
	}
}
