package gobridge

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

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
