package gobridge

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestDirectRecoveryReplayBarrierAckCompleteAndPendingFlush(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)
	server.SetRecoveryEnabled(true)
	server.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "turn_started"})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := HelloMessage{
		Type: "hello", Client: HelloClient{DeviceID: "device"},
		Protocol:     HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
		Capabilities: []string{"recovery_v1"}, LastBridgeEpoch: epoch,
		LastSeenBySession: BridgeSessionCutMap{"codex": {"s": {EventID: epoch + ":0", Seq: 0}}},
	}
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack HelloAckMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.Recovery == nil || ack.Recovery.Mode != "replay" || !ack.Capabilities["recovery_v1"] {
		t.Fatalf("ack recovery = %+v", ack.Recovery)
	}
	var replay EventMessage
	if err := conn.ReadJSON(&replay); err != nil {
		t.Fatal(err)
	}
	if replay.Seq != 1 || replay.EventID != epoch+":1" {
		t.Fatalf("replay = %+v", replay)
	}
	var barrier struct {
		Type, RecoveryID       string
		ReplayThroughBySession BridgeSessionCutMap `json:"replayThroughBySession"`
	}
	if err := conn.ReadJSON(&barrier); err != nil {
		t.Fatal(err)
	}
	if barrier.Type != "recovery_barrier" || barrier.RecoveryID != ack.Recovery.RecoveryID {
		t.Fatalf("barrier = %+v", barrier)
	}

	server.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "turn_completed", Targets: []Connection{serverConnectionForTest(server)}})
	// The real connection is a private adapter registered by ServeHTTP. Locate it
	// through broadcaster targets so the pending event uses the same sink.
	targets := handlers.broadcaster.Targets("codex", "s", "")
	if len(targets) != 1 {
		t.Fatalf("targets=%d", len(targets))
	}
	server.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "turn_completed", Targets: targets})
	if err := conn.WriteJSON(map[string]interface{}{"type": "recovery_applied", "recoveryId": ack.Recovery.RecoveryID, "appliedThroughBySession": ack.Recovery.ReplayThroughBySession}); err != nil {
		t.Fatal(err)
	}
	var complete map[string]interface{}
	if err := conn.ReadJSON(&complete); err != nil {
		t.Fatal(err)
	}
	if complete["type"] != "recovery_complete" {
		t.Fatalf("complete=%#v", complete)
	}
	var pending EventMessage
	if err := conn.ReadJSON(&pending); err != nil {
		t.Fatal(err)
	}
	if pending.Event != "turn_completed" || pending.Seq != 3 {
		t.Fatalf("pending=%+v", pending)
	}
}

// serverConnectionForTest deliberately returns nil; it stamps one unrelated
// event before the real pending publish to prove global sequence continuity.
func serverConnectionForTest(*Server) Connection { return nil }

func TestRelayRecoveryUsesSameTransactionAndInboundAckPath(t *testing.T) {
	const epoch = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)
	server.SetRecoveryEnabled(true)
	conn := newPublisherCaptureConn(nil)
	handlers.registerConnection(conn)
	server.eventPublisher.PublishLogical(LogicalEvent{BackendID: "claude", SessionID: "s", Event: "turn_started"})
	hello := &HelloMessage{Capabilities: []string{"recovery_v1"}, LastBridgeEpoch: epoch, LastSeenBySession: BridgeSessionCutMap{"claude": {"s": {EventID: epoch + ":0"}}}}
	plan, replay, err := server.prepareRecovery(conn, hello)
	if err != nil {
		t.Fatal(err)
	}
	conn.SendJSON(&HelloAckMessage{Type: "hello_ack", Ok: true, BridgeEpoch: epoch, Recovery: plan})
	server.emitRecoveryFrames(conn, plan, replay)
	conn.waitCount(t, 3)
	server.eventPublisher.PublishLogical(LogicalEvent{BackendID: "claude", SessionID: "s", Event: "turn_completed", Targets: []Connection{conn}})
	raw, _ := json.Marshal(WireMessage{Type: "recovery_applied", RecoveryID: plan.RecoveryID, AppliedThroughBySession: plan.ReplayThroughBySession})
	handlers.HandleRelayInbound(conn, raw)
	conn.waitCount(t, 5)
	frames := conn.snapshot()
	if frames[1].(EventMessage).Seq != 1 {
		t.Fatalf("replay=%#v", frames[1])
	}
	if frames[2].(map[string]interface{})["type"] != "recovery_barrier" {
		t.Fatalf("barrier=%#v", frames[2])
	}
	if frames[3].(map[string]interface{})["type"] != "recovery_complete" {
		t.Fatalf("complete=%#v", frames[3])
	}
	if frames[4].(EventMessage).Seq != 2 {
		t.Fatalf("pending=%#v", frames[4])
	}
}

func TestRecoveryPlanningSnapshotAndFullResync(t *testing.T) {
	const epoch = "cccccccc-dddd-4eee-8fff-000000000000"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)
	conn := newPublisherCaptureConn(nil)
	server.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "text_delta"})
	base := &HelloMessage{Capabilities: []string{"recovery_v1"}, LastBridgeEpoch: epoch, LastSeenBySession: BridgeSessionCutMap{"codex": {"s": {EventID: epoch + ":0"}}}}
	plan, _, err := server.prepareRecovery(conn, base)
	if err != nil || plan.Mode != "snapshot_required" || plan.CutBySession["codex"]["s"].Seq != 1 {
		t.Fatalf("snapshot plan=%+v err=%v", plan, err)
	}
	server.eventPublisher.FailRecovery(conn, plan.RecoveryID)
	base.LastBridgeEpoch = "old-epoch"
	plan, _, err = server.prepareRecovery(conn, base)
	if err != nil || plan.Mode != "full_resync" {
		t.Fatalf("full plan=%+v err=%v", plan, err)
	}
}

func TestRecoveryCapabilityAbsentPreservesLegacyHello(t *testing.T) {
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, "dddddddd-eeee-4fff-8000-111111111111")
	server.SetRecoveryEnabled(true)
	ack := HandleHello(&HelloMessage{Protocol: HelloProtocol{Version: BridgeProtocolVersion}}, nil, "b", "m", "v", "ws://127.0.0.1", "", nil, "", nil, handlers.sessions)
	if ack.Recovery != nil || ack.Capabilities["recovery_v1"] {
		t.Fatalf("legacy hello advertised recovery: %+v", ack)
	}
}
