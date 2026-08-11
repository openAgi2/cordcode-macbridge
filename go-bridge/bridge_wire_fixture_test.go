package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type bridgeWireFixtureConn struct {
	frames [][]byte
	data   interface{}
	err    *WireError
}

func (c *bridgeWireFixtureConn) SendJSON(value interface{}) {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	c.frames = append(c.frames, raw)
}
func (c *bridgeWireFixtureConn) SendResult(_ string, data interface{}, wireErr *WireError) {
	c.data, c.err = data, wireErr
}
func (c *bridgeWireFixtureConn) AuthedDevice() *TrustedDeviceRecord { return nil }
func (c *bridgeWireFixtureConn) RemoteAddr() string                 { return "fixture" }
func (c *bridgeWireFixtureConn) Close() error                       { return nil }

func cleanupBridgeWireFixtureHandlers(t *testing.T, handlers *Handlers) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handlers.Shutdown(ctx)
	})
}

func observedBridgeWireFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "bridge-epoch-fixture")
	cleanupBridgeWireFixtureHandlers(t, handlers)
	server := NewServerWithEpoch(handlers, "bridge-epoch-fixture")
	server.SetBridgeIdentity("bridge-fixture", "CordCode Link", "1.0.0-fixture", "ws://127.0.0.1:8777", "")

	client, _ := json.Marshal(HelloClient{App: "CordCode", Version: "1.0-fixture", DeviceID: "device-fixture"})
	protocol, _ := json.Marshal(HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion, SupportedSchemaRevisions: []string{BridgeProtocolSchemaRevision}})
	conn := &bridgeWireFixtureConn{}
	server.handleHello(&Conn{}, conn, &WireMessage{
		Type: "hello", Client: client, Protocol: protocol,
		Capabilities: []string{"read_file_v2"},
	})
	if len(conn.frames) != 1 || !server.eventPublisher.ConnReadFileV2(conn) {
		t.Fatal("real hello handler did not emit and record read_file_v2 negotiation")
	}

	encode := func(value interface{}) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return map[string][]byte{
		"hello-ack-read-file-v2.json": conn.frames[0],
		"relay-chunk-base.json": encode(RelayChunkMetadata{
			GroupID: "grp-0001-test", Index: 0, Count: 3,
		}),
		"relay-chunk-correlated.json": encode(RelayChunkMetadata{
			GroupID: "grp-0001-test", Index: 1, Count: 3,
			BulkCorrelationID: "00112233445566778899aabbccddeeff",
		}),
		"error-runtime-quiescing.json": encode(resultEnvelope("req-0001-test", nil, &WireError{
			Code: "runtime.quiescing", Message: "Bridge runtime is quiescing",
		})),
		"error-file-read-degraded.json": encode(resultEnvelope("req-0002-test", nil, &WireError{
			Code: "file.read_degraded", Message: "file read service is degraded; runtime restart required",
		})),
	}
}

func bridgeWireFixturePath(name string) string {
	return filepath.Join("..", "docs", "protocol", "samples", "read-file-v2", name)
}

func TestBridgeWireObservedFixturesRoundTrip(t *testing.T) {
	for name, produced := range observedBridgeWireFixtures(t) {
		committed, err := os.ReadFile(bridgeWireFixturePath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(produced, committed) {
			t.Errorf("%s is not exact production producer bytes", name)
		}
	}
}

func TestGenerateBridgeWireObservedFixtures(t *testing.T) {
	if os.Getenv("CCCODEGEN_FIXTURES") != "1" {
		t.Skip("set CCCODEGEN_FIXTURES=1 to write fixtures")
	}
	for name, produced := range observedBridgeWireFixtures(t) {
		if err := os.WriteFile(bridgeWireFixturePath(name), produced, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestReadFileV2RequiresNegotiatedCapability(t *testing.T) {
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "bridge-epoch-capability-test")
	cleanupBridgeWireFixtureHandlers(t, handlers)
	conn := &bridgeWireFixtureConn{}
	message := WireMessage{Type: "request", RequestID: "req-test", BackendID: "missing", Method: "read_file_v2"}
	handlers.HandleRPC(conn, message)
	if conn.err == nil || conn.err.Code != "capability_not_negotiated" {
		t.Fatalf("unnegotiated v2 got %+v", conn.err)
	}
	handlers.eventPublisher.SetConnReadFileV2(conn, true)
	handlers.HandleRPC(conn, message)
	if conn.err == nil || conn.err.Code != "backend_not_found" {
		t.Fatalf("negotiated request did not pass capability gate: %+v", conn.err)
	}
}

func TestFrozenProtocolInventoriesMatchFiles(t *testing.T) {
	for _, root := range []string{"read-file-v2", "management-file-read"} {
		dir := filepath.Join("..", "docs", "protocol", "samples", root)
		raw, err := os.ReadFile(filepath.Join(dir, "inventory.json"))
		if err != nil {
			t.Fatal(err)
		}
		var inventory struct {
			Root struct {
				Status string `json:"status"`
			} `json:"root"`
			Variants []struct {
				EvidenceStatus string `json:"evidenceStatus"`
				ExpectedFile   string `json:"expectedFile"`
			} `json:"variants"`
		}
		if err := json.Unmarshal(raw, &inventory); err != nil {
			t.Fatalf("%s inventory: %v", root, err)
		}
		if inventory.Root.Status != "frozen" {
			t.Errorf("%s status=%s", root, inventory.Root.Status)
		}
		expected := make(map[string]bool)
		for _, variant := range inventory.Variants {
			if variant.EvidenceStatus != "observed" {
				t.Errorf("%s/%s evidence=%s", root, variant.ExpectedFile, variant.EvidenceStatus)
			}
			if expected[variant.ExpectedFile] {
				t.Errorf("%s duplicate expectedFile %s", root, variant.ExpectedFile)
			}
			expected[variant.ExpectedFile] = true
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		actual := make(map[string]bool)
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() != "inventory.json" {
				actual[entry.Name()] = true
			}
		}
		for name := range expected {
			if !actual[name] {
				t.Errorf("%s missing fixture %s", root, name)
			}
		}
		for name := range actual {
			if !expected[name] {
				t.Errorf("%s unlisted fixture %s", root, name)
			}
		}
	}
}
