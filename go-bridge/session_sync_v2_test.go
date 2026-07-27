package gobridge

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSessionSyncV2ProductionGateIsShadowEnabled(t *testing.T) {
	if !sessionSyncV2ProductionEnabled {
		t.Fatal("K3 production must permit explicitly opted-in Codex shadow clients")
	}
}

func TestSessionSyncV2K3AdmissionPolicyIsVersionedAndBounded(t *testing.T) {
	if SessionSyncV2PolicyVersion != "k3-v1" {
		t.Fatalf("policy version = %q", SessionSyncV2PolicyVersion)
	}
	policy := DefaultProjectionRetryPolicy()
	if policy.Initial != time.Second ||
		policy.Maximum != 30*time.Second ||
		policy.JitterFraction != 0.20 {
		t.Fatalf("retry policy drifted: %+v", policy)
	}
	if got := policy.delay(1, 0); got != 800*time.Millisecond {
		t.Fatalf("lower jitter bound = %s", got)
	}
	if got := policy.delay(99, 1); got != 30*time.Second {
		t.Fatalf("maximum jittered delay = %s", got)
	}
	if projectionHydrateMaxConcurrent != 4 {
		t.Fatalf("hydrate concurrency = %d", projectionHydrateMaxConcurrent)
	}
	handlers := NewHandlers()
	if cap(handlers.projectionHydrateSlots) != projectionHydrateMaxConcurrent {
		t.Fatalf("hydrate semaphore capacity = %d", cap(handlers.projectionHydrateSlots))
	}
	if projectionCheckpointHitP95SLO != 2*time.Second ||
		projectionColdOpenP50SLO != 5*time.Second ||
		projectionColdOpenP95SLO != 15*time.Second ||
		projectionColdOpenMaximumSLO != 30*time.Second {
		t.Fatal("F2 SLO anchors drifted")
	}
	if !backendSupportsProjectionHydrate("codex") ||
		!backendSupportsProjectionHydrate("claude") ||
		!backendSupportsProjectionHydrate("claudecode") ||
		backendSupportsProjectionHydrate("opencode") {
		t.Fatal("K5 migration boundary drifted: codex+claude hydrate, opencode still not migrated")
	}
}

func TestSessionSyncV2CapabilityScopedToMigratedBackend(t *testing.T) {
	backends := []AgentProviderDescriptor{
		{ID: "claude", Kind: "claude_code", Capabilities: []string{"session_mutation"}},
		{ID: "codex", Kind: "codex", Capabilities: []string{"todos"}},
		{ID: "opencode", Kind: "opencode", Capabilities: nil},
	}
	advertiseSessionSyncV2Backend(backends)
	for _, backend := range backends {
		hasV2 := false
		for _, capability := range backend.Capabilities {
			if capability == "session_sync_v2" {
				hasV2 = true
			}
		}
		migrated := backend.ID == "codex" || backend.ID == "claude"
		if migrated && !hasV2 {
			t.Fatalf("migrated backend %q did not advertise session_sync_v2", backend.ID)
		}
		if !migrated && hasV2 {
			t.Fatalf("unmigrated backend %q inherited session_sync_v2", backend.ID)
		}
	}
	advertiseSessionSyncV2Backend(backends)
	if got := len(backends[1].Capabilities); got != 2 {
		t.Fatalf("backend capability append was not idempotent: %v", backends[1].Capabilities)
	}
}

func TestProjectionLifecycleWireErrorRoundTrip(t *testing.T) {
	retryable := true
	retryAfter := int64(250)
	wire := WireError{
		Code:             "projection.hydrating",
		Message:          "projection hydration is still in progress",
		Retryable:        &retryable,
		RetryAfterMillis: &retryAfter,
		Attempts:         2,
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WireError
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Code != wire.Code || decoded.Retryable == nil || !*decoded.Retryable ||
		decoded.RetryAfterMillis == nil || *decoded.RetryAfterMillis != retryAfter ||
		decoded.Attempts != 2 {
		t.Fatalf("projection lifecycle error did not round-trip: %+v", decoded)
	}
}

// TestHelloSupportsSessionSyncV2 covers the client opt-in detector (mirrors helloSupportsRecovery).
func TestHelloSupportsSessionSyncV2(t *testing.T) {
	cases := []struct {
		name string
		caps []string
		want bool
	}{
		{"opted in", []string{"recovery_v1", "session_sync_v2"}, true},
		{"only session_sync_v2", []string{"session_sync_v2"}, true},
		{"no opt-in", []string{"recovery_v1", "relay_chunks_v1"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hello := &HelloMessage{Capabilities: c.caps}
			if got := helloSupportsSessionSyncV2(hello); got != c.want {
				t.Fatalf("helloSupportsSessionSyncV2(%v) = %v, want %v", c.caps, got, c.want)
			}
		})
	}
}

// TestSessionSyncV2CapabilityAdvertisedOnOptIn drives the LAN hello flow end-to-end and asserts
// hello_ack echoes capabilities["session_sync_v2"]=true when the server flag is on AND the client
// opted in (the same negotiation shape as recovery_v1).
func TestSessionSyncV2CapabilityAdvertisedOnOptIn(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)
	server.SetSessionSyncV2Enabled(true)

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
		Capabilities: []string{"session_sync_v2"},
	}
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack HelloAckMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if !ack.Capabilities["session_sync_v2"] || ack.BridgeEpoch != epoch {
		t.Fatalf("hello_ack lost v2 capability/epoch: %+v", ack)
	}
	if !ack.Ok {
		t.Fatalf("hello_ack not ok: %+v", ack)
	}
	if !ack.Capabilities["session_sync_v2"] {
		t.Fatalf("hello_ack did not echo session_sync_v2; capabilities=%+v", ack.Capabilities)
	}
}

// TestSessionSyncV2CapabilityOmittedWithoutOptIn asserts the capability is NOT advertised when
// the client did not opt in (legacy client must see no behavior change).
func TestSessionSyncV2CapabilityOmittedWithoutOptIn(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)
	server.SetSessionSyncV2Enabled(true)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Legacy client: no session_sync_v2 capability.
	hello := HelloMessage{
		Type: "hello", Client: HelloClient{DeviceID: "device"},
		Protocol:     HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
		Capabilities: []string{"recovery_v1"},
	}
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack HelloAckMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if _, ok := ack.Capabilities["session_sync_v2"]; ok {
		t.Fatalf("legacy hello_ack unexpectedly advertised session_sync_v2: %+v", ack.Capabilities)
	}
}

func TestSessionSyncV2DirectTransportResultPrecedesLiveProjectionPatch(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-fencefence001"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)
	server.SetSessionSyncV2Enabled(true)
	server.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "wire-order", Event: "turn_started",
		Data: map[string]interface{}{"turnId": "T1"},
	})
	server.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "wire-order", Event: "text_delta",
		Data: map[string]interface{}{"itemId": "T1", "delta": "base"},
	})
	if err := handlers.ensureProjectionHydrated("codex", "wire-order"); err != nil {
		t.Fatal(err)
	}
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(HelloMessage{
		Type: "hello", Client: HelloClient{DeviceID: "device"},
		Protocol:     HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
		Capabilities: []string{"session_sync_v2"},
	}); err != nil {
		t.Fatal(err)
	}
	var ack HelloAckMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}

	params, _ := json.Marshal(map[string]interface{}{"sessionId": "wire-order"})
	if err := conn.WriteJSON(WireMessage{
		Type: "request", RequestID: "direct-rpc", BackendID: "codex",
		Method: "get_session_projection", Params: params,
	}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := conn.ReadJSON(&result); err != nil {
		t.Fatal(err)
	}
	if result["type"] != "result" || result["requestId"] != "direct-rpc" || result["ok"] != true {
		t.Fatalf("first post-request LAN frame = %#v", result)
	}
	if !handlers.broadcaster.HasSessionSubscriber("codex", "wire-order") {
		t.Fatal("get_session_projection did not subscribe the logical connection")
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("projection result data = %#v", result["data"])
	}
	projection, ok := data["projection"].(map[string]interface{})
	if !ok || projection["syncRev"] != float64(2) || projection["bridgeEpoch"] != epoch {
		t.Fatalf("committed projection result lost head/epoch: %#v", data["projection"])
	}

	server.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "wire-order", Event: "text_delta",
		Data:      map[string]interface{}{"itemId": "T1", "delta": "live"},
		Broadcast: true,
	})
	for {
		var frame map[string]interface{}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame["event"] == "projection_patch" {
			patch, ok := frame["data"].(map[string]interface{})
			if !ok ||
				patch["baseRev"] != float64(2) ||
				patch["syncRev"] != float64(3) ||
				frame["perSessionSeq"] != float64(3) ||
				frame["bridgeEpoch"] != epoch {
				t.Fatalf("live patch lost ordered head/epoch evidence: %#v", frame)
			}
			break
		}
	}
}

// TestSessionSyncV2RelayOutboundClassification asserts the new event/RPC names traverse the relay
// with the correct outbound class so they survive the relay path (Phase 2 #8 Relay scenario).
func TestSessionSyncV2RelayOutboundClassification(t *testing.T) {
	eventCases := []struct {
		event string
		class relayOutboundClass
	}{
		{"projection_patch", relayOutboundInteractive},
		{"projection_snapshot", relayOutboundInteractive},
		{"sync_invalidate", relayOutboundControl},
	}
	for _, c := range eventCases {
		t.Run("event/"+c.event, func(t *testing.T) {
			if got := classifyRelayEvent(c.event); got != c.class {
				t.Fatalf("classifyRelayEvent(%q) = %v, want %v", c.event, got, c.class)
			}
		})
	}
	if got := classifyRelayRequest("get_session_projection"); got != relayOutboundBulk {
		t.Fatalf("classifyRelayRequest(get_session_projection) = %v, want bulk", got)
	}
}
