package gobridge

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRecoveryProtocolContractIsHelloOnlyAndExactCutBased(t *testing.T) {
	schema, err := os.ReadFile("../docs/protocol/schema/bridge-v1.types.ts")
	if err != nil {
		t.Fatal(err)
	}
	text := string(schema)
	for _, required := range []string{
		`BridgeClientCapability = "recovery_v1" | "relay_gzip_v1" | "relay_chunks_v1"`,
		"lastSeenBySession?: BridgeSessionCutMap",
		`type: "recovery_applied"`,
		"appliedThroughBySession: BridgeSessionCutMap",
		`type: "recovery_complete"`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("schema missing %q", required)
		}
	}
	registerStart := strings.Index(text, "export interface BridgeRegister {")
	registerEnd := strings.Index(text[registerStart:], "\n}")
	if registerStart < 0 || registerEnd < 0 {
		t.Fatal("BridgeRegister declaration not found")
	}
	register := text[registerStart : registerStart+registerEnd]
	if strings.Contains(register, "lastBridgeEpoch") || strings.Contains(register, "lastSeenBySession") {
		t.Fatalf("legacy register still owns recovery fields: %s", register)
	}
}

func TestRecoveryProtocolSamplesParseAndShareTransaction(t *testing.T) {
	files := []string{
		"bridge-v1-hello-ack.json",
		"bridge-v1-recovery-barrier.json",
		"bridge-v1-recovery-applied.json",
		"bridge-v1-recovery-complete.json",
	}
	const wantID = "948624e8-5463-4628-bad8-40ba35b43a6f"
	for _, name := range files {
		data, err := os.ReadFile("../docs/protocol/samples/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		got := payload["recoveryId"]
		if recovery, ok := payload["recovery"].(map[string]any); ok {
			got = recovery["recoveryId"]
		}
		if got != wantID {
			t.Errorf("%s recoveryId = %#v, want %q", name, got, wantID)
		}
	}
}

func TestHelloWithoutRecoveryCapabilityOmitsRecovery(t *testing.T) {
	ack := HandleHello(
		&HelloMessage{Type: "hello", Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion}},
		nil, "bridge", "Mac", "dev", "ws://127.0.0.1/bridge", "", nil, "", nil, nil,
	)
	data, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"recovery"`) {
		t.Fatalf("legacy hello unexpectedly enabled recovery: %s", data)
	}
}
