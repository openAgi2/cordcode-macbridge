package gobridge

import (
	"crypto/ecdh"
	"encoding/base64"

	"testing"
)

// ─── 离线集成测试 ────────────────────────────────────────────────────────

func newTestRelayEventRouter() *RelayEventRouter {
	observation := NewObservationManager()
	prekeys := NewPrekeyStore("brg_fixture")
	mailbox := NewMailboxService(NewRelayHub())
	outbox := NewOutboxManager(prekeys)
	presentation := NewPresentationManager()

	router := NewRelayEventRouter(observation, outbox, prekeys, mailbox, presentation)
	router.SetRouteID("route_offline_test")
	router.SetDeviceGenerationFunc(func(string) uint64 { return 1 })
	return router
}

// TestOfflineIntegrationDurableMilestoneQueued 验证 durable milestone 被队列化到离线设备。
func TestOfflineIntegrationDurableMilestoneQueued(t *testing.T) {
	router := newTestRelayEventRouter()
	defer router.observation.Stop()

	deviceID := "dev_offline_1"

	// 上传 prekey
	priv := generateTestPrekeyPrivate(t)
	pub := encodePubKeyHelper(priv)
	router.prekeys.SetIdentityAuthKeyFactory(testIdentityAuthKeyFactory(make([]byte, 32)))
	router.prekeys.UploadPrekeys(PrekeyUploadBatch{
		BatchID:  "batch_offline",
		DeviceID: deviceID,
		Prekeys: []PrekeyUploadItem{
			{PrekeyID: "pk_offline", PublicKey: pub},
		},
	})

	// 路由 durable milestone
	router.RouteEvent("sess_1", "codex", "turn_completed",
		map[string]interface{}{"done": true},
		[]string{},         // 无在线设备
		[]string{deviceID}, // 1 个离线设备
	)

	// 验证 outbox 有条目
	frames, bytes, overflowed := router.outbox.Stats(deviceID)
	if frames != 1 {
		t.Errorf("outbox frames = %d, want 1", frames)
	}
	if bytes == 0 {
		t.Error("outbox bytes should be > 0")
	}
	if overflowed {
		t.Error("should not be overflowed")
	}
}

// TestOfflineIntegrationNonDurableNotQueued 验证非 durable 事件不被队列化。
func TestOfflineIntegrationNonDurableNotQueued(t *testing.T) {
	router := newTestRelayEventRouter()
	defer router.observation.Stop()

	deviceID := "dev_offline_2"

	// 路由非 durable 事件
	router.RouteEvent("sess_1", "codex", "text_delta",
		map[string]interface{}{"delta": "hello"},
		[]string{},
		[]string{deviceID},
	)

	// Outbox 应为空（无 prekey 也无条目）
	frames, _, _ := router.outbox.Stats(deviceID)
	if frames != 0 {
		t.Errorf("outbox frames = %d, want 0 (non-durable should not queue)", frames)
	}
}

// TestOfflineIntegrationPrekeyExhaustionMarksPendingSync 验证 prekey 耗尽标记 pending sync。
func TestOfflineIntegrationPrekeyExhaustionMarksPendingSync(t *testing.T) {
	router := newTestRelayEventRouter()
	defer router.observation.Stop()

	deviceID := "dev_offline_3"
	router.prekeys.SetIdentityAuthKeyFactory(testIdentityAuthKeyFactory(make([]byte, 32)))

	// 不上传任何 prekey，直接路由 durable milestone
	router.RouteEvent("sess_1", "codex", "turn_completed",
		map[string]interface{}{"done": true},
		[]string{},
		[]string{deviceID},
	)

	// 设备应被标记为 pending sync
	if !false {
		// MarkPendingSync 标记的是 deviceID 作为 sessionID
		// 这里验证 pending 标记是否设置
		pending := router.presentation.GetAllPendingSync()
		found := false
		for _, s := range pending {
			if s == deviceID {
				found = true
			}
		}
		if !found {
			t.Error("device should be marked for pending sync after prekey exhaustion")
		}
	}
}

// TestOfflineIntegrationOnlineDeviceScopeControlled 验证在线设备的 scope 控制。
func TestOfflineIntegrationOnlineDeviceScopeControlled(t *testing.T) {
	router := newTestRelayEventRouter()
	defer router.observation.Stop()

	deviceID := "dev_online_1"

	// 设置 milestones_only scope
	router.observation.SetScope(deviceID, ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeMilestonesOnly,
		LeaseSeconds: 60,
	})

	// 非 durable 不应发送
	if router.ShouldSendToOnlineDevice(deviceID, "codex", "sess_1", "text_delta") {
		t.Error("milestones_only should not send text_delta")
	}

	// Durable 应发送
	if !router.ShouldSendToOnlineDevice(deviceID, "codex", "sess_1", "turn_completed") {
		t.Error("milestones_only should send turn_completed")
	}
}

// TestOfflineIntegrationFullStreamSendsAll 验证 full_stream 发送全部事件。
func TestOfflineIntegrationFullStreamSendsAll(t *testing.T) {
	router := newTestRelayEventRouter()
	defer router.observation.Stop()

	deviceID := "dev_online_2"

	router.observation.SetScope(deviceID, ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 60,
	})

	events := []string{"text_delta", "thinking_delta", "turn_completed", "todos_updated", "tool_started"}
	for _, e := range events {
		if !router.ShouldSendToOnlineDevice(deviceID, "codex", "sess_1", e) {
			t.Errorf("full_stream should send %q", e)
		}
	}
}

func encodePubKeyHelper(priv *ecdh.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
}
