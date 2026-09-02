package gobridge

import (
	"encoding/json"
	"testing"
)

// 2026-09-02 owner 验收 row 12 事故：set_observation_scope 切换会话只加不减订阅键，
// App 保持连接期间 HasSessionSubscriber 恒真 → grok leader D-G2 永不触发。本文件
// 钉住「订阅语义 = 当前打开，不是曾经打开」。

func TestReconcileObservationSubscriptionsPrunesStaleScopeKeys(t *testing.T) {
	broadcaster := NewBroadcaster()
	conn1 := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev_1"}}
	conn2 := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev_2"}}

	broadcaster.Subscribe(conn1, SubscriptionKey{BackendID: "grokbuild", SessionID: "sessA"})
	broadcaster.Subscribe(conn1, SubscriptionKey{BackendID: "grokbuild", SessionID: "sessB"})
	broadcaster.Subscribe(conn1, SubscriptionKey{BackendID: "grokbuild", SessionID: "sessOwn", Directory: "/w"})
	broadcaster.Subscribe(conn1, SubscriptionKey{BackendID: "claude", SessionID: "sessA"})
	broadcaster.Subscribe(conn2, SubscriptionKey{BackendID: "grokbuild", SessionID: "sessA"})

	keep := map[string]struct{}{"sessB": {}}
	broadcaster.ReconcileObservationSubscriptions(conn1, "grokbuild", keep)

	// conn1 切到 sessB：sessA 观察键退订，但 conn2 仍在观察 sessA。
	if !broadcaster.HasSessionSubscriber("grokbuild", "sessA") {
		t.Fatal("sessA must still have conn2 subscribed")
	}
	if !broadcaster.HasSessionSubscriber("grokbuild", "sessB") {
		t.Fatal("sessB is in keep set, must stay subscribed")
	}
	// 带目录的自有会话键与其他 backend 的键不受影响。
	if !broadcaster.HasSessionSubscriber("grokbuild", "sessOwn") {
		t.Fatal("dir-ful own-session key must survive scope reconcile")
	}
	if !broadcaster.HasSessionSubscriber("claude", "sessA") {
		t.Fatal("other-backend key must survive scope reconcile")
	}

	// conn2 也切走后：sessA 彻底无订阅者（D-G2 锚点从此可累计）。
	broadcaster.ReconcileObservationSubscriptions(conn2, "grokbuild", map[string]struct{}{"sessB": {}})
	if broadcaster.HasSessionSubscriber("grokbuild", "sessA") {
		t.Fatal("sessA must have no subscriber after both conns switched away")
	}
}

func TestObservationScopeSwitchDropsStaleSessionSubscriber(t *testing.T) {
	h := newTestHandlers(t)
	conn := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev_row12"}}

	scopeA := json.RawMessage(`{"backendId":"grokbuild","sessionIds":["sessA"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-a", BackendID: "grokbuild", Params: scopeA})
	if !h.broadcaster.HasSessionSubscriber("grokbuild", "sessA") {
		t.Fatal("sessA must be subscribed while in scope")
	}

	// iPhone 自有会话键（send_message/resume 带目录）在切换后必须幸存。
	h.broadcaster.Subscribe(conn, SubscriptionKey{BackendID: "grokbuild", SessionID: "sessOwn", Directory: "/w"})

	scopeB := json.RawMessage(`{"backendId":"grokbuild","sessionIds":["sessB"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-b", BackendID: "grokbuild", Params: scopeB})

	if h.broadcaster.HasSessionSubscriber("grokbuild", "sessA") {
		t.Fatal("sessA must lose its subscriber after scope switched to sessB (row 12 incident)")
	}
	if !h.broadcaster.HasSessionSubscriber("grokbuild", "sessB") {
		t.Fatal("sessB must be subscribed after switch")
	}
	if !h.broadcaster.HasSessionSubscriber("grokbuild", "sessOwn") {
		t.Fatal("own-session dir key must survive scope switch")
	}

	// 其他 backend 的观察不受本次切换影响。
	scopeClaude := json.RawMessage(`{"backendId":"claude","sessionIds":["sessA"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-c", BackendID: "claude", Params: scopeClaude})
	if !h.broadcaster.HasSessionSubscriber("claude", "sessA") {
		t.Fatal("claude scope must subscribe independently")
	}

	// 空 scope（客户端回到无观察状态）：该 backend 观察键全部退订。
	scopeEmpty := json.RawMessage(`{"backendId":"grokbuild","sessionIds":[],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-e", BackendID: "grokbuild", Params: scopeEmpty})
	if h.broadcaster.HasSessionSubscriber("grokbuild", "sessB") {
		t.Fatal("empty scope must drop all observation keys for the backend")
	}
	if !h.broadcaster.HasSessionSubscriber("grokbuild", "sessOwn") {
		t.Fatal("own-session dir key must survive empty scope")
	}
}
