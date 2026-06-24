package gobridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestHandleHello_MatchingVersion(t *testing.T) {
	hello := &HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "1.0",
			DeviceID: "test-device",
		},
		Protocol: HelloProtocol{
			Name:    BridgeProtocolName,
			Version: BridgeProtocolVersion,
		},
	}
	device := &TrustedDeviceRecord{
		DeviceID:    "test-device",
		DisplayName: "Test Device",
		Platform:    "ios",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}

	ack := HandleHello(hello, device, "bridge-1", "My Bridge", "0.1.0", "ws://localhost:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("协议版本匹配时应返回 ok=true, got error: %v", ack.Error)
	}
	if ack.Type != "hello_ack" {
		t.Errorf("Type 应为 hello_ack, got %q", ack.Type)
	}
	if ack.Bridge == nil {
		t.Fatal("Bridge 不应为 nil")
	}
	if ack.Bridge.BridgeID != "bridge-1" {
		t.Errorf("BridgeID 不匹配: got %q, want %q", ack.Bridge.BridgeID, "bridge-1")
	}
	if ack.Bridge.DisplayName != "My Bridge" {
		t.Errorf("DisplayName 不匹配: got %q, want %q", ack.Bridge.DisplayName, "My Bridge")
	}
	if ack.Bridge.RuntimeVersion != "0.1.0" {
		t.Errorf("RuntimeVersion 不匹配: got %q, want %q", ack.Bridge.RuntimeVersion, "0.1.0")
	}
	if ack.Bridge.CurrentURLs.Local != "ws://localhost:8777" {
		t.Errorf("CurrentURLs.Local 不匹配: got %q", ack.Bridge.CurrentURLs.Local)
	}
	if ack.Bridge.Protocol.Name != BridgeProtocolName {
		t.Errorf("Bridge.Protocol.Name 不匹配: got %q", ack.Bridge.Protocol.Name)
	}
	if ack.Bridge.Protocol.Version != BridgeProtocolVersion {
		t.Errorf("Bridge.Protocol.Version 不匹配: got %d", ack.Bridge.Protocol.Version)
	}
	if ack.BridgeStatus != "running" {
		t.Errorf("BridgeStatus 应为 running, got %q", ack.BridgeStatus)
	}
	if ack.RunningSessions == nil {
		t.Error("RunningSessions 不应为 nil（无 session 时应为空列表）")
	}
	if ack.Capabilities == nil {
		t.Fatal("Capabilities 不应为 nil")
	}
}

func TestHandleHello_WrongVersion(t *testing.T) {
	hello := &HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "1.0",
			DeviceID: "test-device",
		},
		Protocol: HelloProtocol{
			Name:    BridgeProtocolName,
			Version: BridgeProtocolVersion + 999,
		},
	}

	ack := HandleHello(hello, nil, "bridge-1", "My Bridge", "0.1.0", "ws://localhost:8777", "", nil, "exec", nil, nil)

	if ack.Ok {
		t.Fatal("协议版本不匹配时应返回 ok=false")
	}
	if ack.Error == nil {
		t.Fatal("Error 不应为 nil")
	}
	if ack.Error.Code != "protocol.unsupported_version" {
		t.Errorf("错误码应为 protocol.unsupported_version, got %q", ack.Error.Code)
	}
	if ack.Bridge != nil {
		t.Error("版本不匹配时 Bridge 应为 nil")
	}
}

// helloMockAgent 实现 core.Agent 最小接口用于 hello handler 测试。
type helloMockAgent struct {
	name string
}

func (m *helloMockAgent) Name() string { return m.name }
func (m *helloMockAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return nil, nil
}
func (m *helloMockAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (m *helloMockAgent) Stop() error { return nil }

func TestHandleHello_ContainsAgentDescriptors(t *testing.T) {
	hello := &HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "1.0",
			DeviceID: "test-device",
		},
		Protocol: HelloProtocol{
			Name:    BridgeProtocolName,
			Version: BridgeProtocolVersion,
		},
	}

	ack := HandleHello(hello, nil, "bridge-1", "My Bridge", "0.1.0", "ws://localhost:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("应成功: %v", ack.Error)
	}
	if ack.Backends == nil {
		t.Error("Backends 不应为 nil（应为空列表）")
	}
}

func TestHandleHello_WithAgents(t *testing.T) {
	coreAgents := map[string]core.Agent{
		"claude": &helloMockAgent{name: "Claude Code"},
	}

	hello := &HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "1.0",
			DeviceID: "test-device",
		},
		Protocol: HelloProtocol{
			Name:    BridgeProtocolName,
			Version: BridgeProtocolVersion,
		},
	}

	ack := HandleHello(hello, nil, "bridge-1", "My Bridge", "0.1.0", "ws://localhost:8777", "", coreAgents, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("应成功: %v", ack.Error)
	}
	if len(ack.Backends) != 1 {
		t.Fatalf("应有 1 个 backend, got %d", len(ack.Backends))
	}
	if ack.Backends[0].ID != "claude" {
		t.Errorf("Backend ID 应为 claude, got %q", ack.Backends[0].ID)
	}
	if ack.Backends[0].DisplayName != "Claude Code" {
		t.Errorf("DisplayName 不匹配, got %q", ack.Backends[0].DisplayName)
	}
}

func TestHelloMessage_JSONRoundTrip(t *testing.T) {
	original := HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "2.0",
			DeviceID: "device-abc",
		},
		Protocol: HelloProtocol{
			Name:                     "cccode-bridge",
			Version:                  1,
			SupportedSchemaRevisions: []string{"2026-05-07", "2026-05-01"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded HelloMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if decoded.Type != "hello" {
		t.Errorf("Type 不匹配: got %q", decoded.Type)
	}
	if decoded.Client.App != "CordCode" {
		t.Errorf("Client.App 不匹配: got %q", decoded.Client.App)
	}
	if decoded.Client.DeviceID != "device-abc" {
		t.Errorf("Client.DeviceID 不匹配: got %q", decoded.Client.DeviceID)
	}
	if decoded.Protocol.Version != 1 {
		t.Errorf("Protocol.Version 不匹配: got %d", decoded.Protocol.Version)
	}
	if len(decoded.Protocol.SupportedSchemaRevisions) != 2 {
		t.Errorf("SupportedSchemaRevisions 长度不匹配: got %d", len(decoded.Protocol.SupportedSchemaRevisions))
	}
}

func TestHelloAckMessage_JSONRoundTrip(t *testing.T) {
	original := HelloAckMessage{
		Type: "hello_ack",
		Ok:   true,
		Bridge: &HelloBridgeInfo{
			BridgeID:       "bridge-1",
			DisplayName:    "My Bridge",
			RuntimeVersion: "0.1.0",
			CurrentURLs: HelloURLs{
				Local:  "ws://localhost:8777",
				Remote: "",
			},
			Protocol: HelloAckProtocol{
				Name:           BridgeProtocolName,
				Version:        BridgeProtocolVersion,
				SchemaRevision: BridgeProtocolSchemaRevision,
			},
		},
		Capabilities: map[string]bool{
			"trustedDevices": true,
		},
		Backends: []AgentProviderDescriptor{
			{
				ID:          "claude",
				Kind:        "claude_code",
				DisplayName: "Claude Code",
			},
		},
		BridgeStatus:    "running",
		RunningSessions: []BridgeV1RunningSession{},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded HelloAckMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if !decoded.Ok {
		t.Error("Ok 应为 true")
	}
	if decoded.Bridge == nil {
		t.Fatal("Bridge 不应为 nil")
	}
	if decoded.Bridge.BridgeID != "bridge-1" {
		t.Errorf("BridgeID 不匹配: got %q", decoded.Bridge.BridgeID)
	}
	if len(decoded.Backends) != 1 {
		t.Errorf("Backends 长度不匹配: got %d", len(decoded.Backends))
	}
	if decoded.Capabilities["trustedDevices"] != true {
		t.Error("trustedDevices capability 应为 true")
	}
	if decoded.BridgeStatus != "running" {
		t.Errorf("BridgeStatus 应为 running, got %q", decoded.BridgeStatus)
	}
	if decoded.Bridge.Protocol.Name != BridgeProtocolName {
		t.Errorf("Bridge.Protocol.Name 不匹配: got %q", decoded.Bridge.Protocol.Name)
	}
}

func TestHelloAckMessage_Error_JSONRoundTrip(t *testing.T) {
	original := HelloAckMessage{
		Type: "hello_ack",
		Ok:   false,
		Error: &WireError{
			Code:    "protocol.unsupported_version",
			Message: "不支持的协议版本",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded HelloAckMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if decoded.Ok {
		t.Error("Ok 应为 false")
	}
	if decoded.Error == nil {
		t.Fatal("Error 不应为 nil")
	}
	if decoded.Error.Code != "protocol.unsupported_version" {
		t.Errorf("Error.Code 不匹配: got %q", decoded.Error.Code)
	}
}

func TestBuildRunningSessions_FiltersIdleAndDeduplicates(t *testing.T) {
	reg := newSessionRegistry()

	// 添加一个 idle session
	reg.put("ses-idle", "claude", "/tmp", &mockSession{})

	// 添加一个 running session
	reg.put("ses-running", "codex", "/tmp", &mockSession{})
	reg.markRunning("ses-running")

	// 模拟 rebind：同一个 trackedSession 出现在两个 ID 下
	reg.rebind("ses-running", "ses-real")
	reg.markRunning("ses-real")

	sessions := buildRunningSessions(reg)

	// 只应有 1 个 running session（去重后）
	if len(sessions) != 1 {
		t.Fatalf("应有 1 个 running session（去重后）, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].Status != "running" {
		t.Errorf("Status 应为 running, got %q", sessions[0].Status)
	}
	if sessions[0].BackendID != "codex" {
		t.Errorf("BackendID 应为 codex, got %q", sessions[0].BackendID)
	}
	// rebind 后应使用真实 ID（ses-real），而非 pending ID（ses-running）
	if sessions[0].SessionID != "ses-real" {
		t.Errorf("rebind 后 SessionID 应为真实 ID ses-real, got %q", sessions[0].SessionID)
	}
}

func TestBuildRunningSessions_NilRegistry(t *testing.T) {
	sessions := buildRunningSessions(nil)
	if sessions == nil {
		t.Fatal("nil registry 应返回空切片，不是 nil")
	}
	if len(sessions) != 0 {
		t.Errorf("nil registry 应返回空切片, got %d items", len(sessions))
	}
}

func TestBuildRunningSessions_NoRunningSessions(t *testing.T) {
	reg := newSessionRegistry()
	reg.put("ses-idle", "claude", "/tmp", &mockSession{})

	sessions := buildRunningSessions(reg)
	if len(sessions) != 0 {
		t.Errorf("没有 running session 时应返回空切片, got %d", len(sessions))
	}
}

// mockSession 实现 core.AgentSession 最小接口，供 registry 测试使用。
type mockSession struct{}

func (m *mockSession) Send(string, []core.ImageAttachment, []core.FileAttachment) error { return nil }
func (m *mockSession) RespondPermission(string, core.PermissionResult) error            { return nil }
func (m *mockSession) RespondQuestion(string, []string) error                           { return nil }
func (m *mockSession) RejectQuestion(string) error                                      { return nil }
func (m *mockSession) Events() <-chan core.Event                                        { return nil }
func (m *mockSession) CurrentSessionID() string                                         { return "" }
func (m *mockSession) Alive() bool                                                      { return true }
func (m *mockSession) Close() error                                                     { return nil }

// ── P3-4: remote URL tests ──────────────────────────────────────────────────

func TestHandleHello_RemoteURL_Propagated(t *testing.T) {
	hello := &HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "1.0",
			DeviceID: "test-device",
		},
		Protocol: HelloProtocol{
			Name:    BridgeProtocolName,
			Version: BridgeProtocolVersion,
		},
	}
	device := &TrustedDeviceRecord{
		DeviceID:    "test-device",
		DisplayName: "Test Device",
		Platform:    "ios",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}

	ack := HandleHello(hello, device, "bridge-1", "My Bridge", "0.1.0",
		"ws://192.168.1.100:8777", "wss://my-tailscale:8777", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge == nil {
		t.Fatal("Bridge should not be nil")
	}
	if ack.Bridge.CurrentURLs.Local != "ws://192.168.1.100:8777" {
		t.Errorf("Local URL = %q, want ws://192.168.1.100:8777", ack.Bridge.CurrentURLs.Local)
	}
	if ack.Bridge.CurrentURLs.Remote != "wss://my-tailscale:8777" {
		t.Errorf("Remote URL = %q, want wss://my-tailscale:8777", ack.Bridge.CurrentURLs.Remote)
	}
}

func TestHandleHello_RemoteURL_Empty(t *testing.T) {
	hello := &HelloMessage{
		Type: "hello",
		Client: HelloClient{
			App:      "CordCode",
			Version:  "1.0",
			DeviceID: "test-device",
		},
		Protocol: HelloProtocol{
			Name:    BridgeProtocolName,
			Version: BridgeProtocolVersion,
		},
	}

	ack := HandleHello(hello, nil, "bridge-1", "My Bridge", "0.1.0",
		"ws://localhost:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge.CurrentURLs.Remote != "" {
		t.Errorf("Remote URL should be empty when not configured, got %q", ack.Bridge.CurrentURLs.Remote)
	}
}

func TestHelloAckMessage_RemoteURL_JSONRoundTrip(t *testing.T) {
	original := HelloAckMessage{
		Type: "hello_ack",
		Ok:   true,
		Bridge: &HelloBridgeInfo{
			BridgeID:       "bridge-1",
			DisplayName:    "My Mac",
			RuntimeVersion: "0.2.0",
			CurrentURLs: HelloURLs{
				Local:  "ws://192.168.1.100:8777",
				Remote: "wss://my-tailscale.example.com:8777",
			},
			Protocol: HelloAckProtocol{
				Name:           BridgeProtocolName,
				Version:        BridgeProtocolVersion,
				SchemaRevision: BridgeProtocolSchemaRevision,
			},
		},
		BridgeStatus:    "running",
		RunningSessions: []BridgeV1RunningSession{},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded HelloAckMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Bridge.CurrentURLs.Remote != "wss://my-tailscale.example.com:8777" {
		t.Errorf("Remote URL round-trip = %q, want wss://my-tailscale.example.com:8777", decoded.Bridge.CurrentURLs.Remote)
	}
	if decoded.Bridge.CurrentURLs.Local != "ws://192.168.1.100:8777" {
		t.Errorf("Local URL round-trip = %q, want ws://192.168.1.100:8777", decoded.Bridge.CurrentURLs.Local)
	}
}

// ── Security profile in hello_ack ──────────────────────────────────────────

func TestHandleHello_SecurityProfile_LAN(t *testing.T) {
	ack := HandleHello(&HelloMessage{
		Type: "hello", Client: HelloClient{App: "CordCode", Version: "1.0", DeviceID: "dev"},
		Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
	}, nil, "bridge-1", "My Bridge", "0.1.0",
		"ws://192.168.1.100:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge == nil || ack.Bridge.Security == nil {
		t.Fatal("Bridge.Security should not be nil")
	}
	if ack.Bridge.Security.Level != "lan" {
		t.Errorf("Security.Level = %q, want lan", ack.Bridge.Security.Level)
	}
	if ack.Bridge.Security.HostCategory != "private" {
		t.Errorf("Security.HostCategory = %q, want private", ack.Bridge.Security.HostCategory)
	}
	if ack.Bridge.Security.IsPublicWS {
		t.Error("Security.IsPublicWS should be false for LAN")
	}
}

func TestHandleHello_SecurityProfile_Tailscale(t *testing.T) {
	ack := HandleHello(&HelloMessage{
		Type: "hello", Client: HelloClient{App: "CordCode", Version: "1.0", DeviceID: "dev"},
		Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
	}, nil, "bridge-1", "My Bridge", "0.1.0",
		"ws://100.100.50.20:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge.Security.Level != "tailscale_tunnel" {
		t.Errorf("Security.Level = %q, want tailscale_tunnel", ack.Bridge.Security.Level)
	}
	if !ack.Bridge.Security.IsTailscaleCGNAT {
		t.Error("Security.IsTailscaleCGNAT should be true")
	}
}

func TestHandleHello_SecurityProfile_Loopback(t *testing.T) {
	ack := HandleHello(&HelloMessage{
		Type: "hello", Client: HelloClient{App: "CordCode", Version: "1.0", DeviceID: "dev"},
		Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
	}, nil, "bridge-1", "My Bridge", "0.1.0",
		"ws://localhost:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge.Security.Level != "lan" {
		t.Errorf("Security.Level = %q, want lan", ack.Bridge.Security.Level)
	}
	if ack.Bridge.Security.HostCategory != "loopback" {
		t.Errorf("Security.HostCategory = %q, want loopback", ack.Bridge.Security.HostCategory)
	}
}

func TestHandleHello_SecurityProfile_WSS(t *testing.T) {
	ack := HandleHello(&HelloMessage{
		Type: "hello", Client: HelloClient{App: "CordCode", Version: "1.0", DeviceID: "dev"},
		Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
	}, nil, "bridge-1", "My Bridge", "0.1.0",
		"wss://example.com:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge.Security.Level != "encrypted" {
		t.Errorf("Security.Level = %q, want encrypted", ack.Bridge.Security.Level)
	}
}

func TestHandleHello_SecurityProfile_PublicWS(t *testing.T) {
	ack := HandleHello(&HelloMessage{
		Type: "hello", Client: HelloClient{App: "CordCode", Version: "1.0", DeviceID: "dev"},
		Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
	}, nil, "bridge-1", "My Bridge", "0.1.0",
		"ws://203.0.113.10:8777", "", nil, "exec", nil, nil)

	if !ack.Ok {
		t.Fatalf("should succeed: %v", ack.Error)
	}
	if ack.Bridge.Security.Level != "insecure" {
		t.Errorf("Security.Level = %q, want insecure", ack.Bridge.Security.Level)
	}
	if !ack.Bridge.Security.IsPublicWS {
		t.Error("Security.IsPublicWS should be true for public ws://")
	}
}

func TestHelloAckMessage_SecurityProfile_JSONRoundTrip(t *testing.T) {
	original := HelloAckMessage{
		Type: "hello_ack",
		Ok:   true,
		Bridge: &HelloBridgeInfo{
			BridgeID:       "bridge-1",
			DisplayName:    "My Mac",
			RuntimeVersion: "0.2.0",
			CurrentURLs:    HelloURLs{Local: "ws://192.168.1.100:8777"},
			Protocol: HelloAckProtocol{
				Name: BridgeProtocolName, Version: BridgeProtocolVersion,
				SchemaRevision: BridgeProtocolSchemaRevision,
			},
			Security: &BridgeV1SecurityProfile{
				Level:        "lan",
				Scheme:       "ws",
				HostCategory: "private",
			},
		},
		BridgeStatus:    "running",
		RunningSessions: []BridgeV1RunningSession{},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded HelloAckMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Bridge.Security == nil {
		t.Fatal("Security should not be nil after round-trip")
	}
	if decoded.Bridge.Security.Level != "lan" {
		t.Errorf("Security.Level = %q, want lan", decoded.Bridge.Security.Level)
	}
	if decoded.Bridge.Security.Scheme != "ws" {
		t.Errorf("Security.Scheme = %q, want ws", decoded.Bridge.Security.Scheme)
	}
}

func TestHelloAckMessage_SecurityProfile_Omitted(t *testing.T) {
	// 不设置 Security 时 JSON 中不应出现 security 字段
	original := HelloAckMessage{
		Type: "hello_ack",
		Ok:   true,
		Bridge: &HelloBridgeInfo{
			BridgeID:       "bridge-1",
			DisplayName:    "My Mac",
			RuntimeVersion: "0.2.0",
			CurrentURLs:    HelloURLs{Local: "ws://localhost:8777"},
			Protocol: HelloAckProtocol{
				Name: BridgeProtocolName, Version: BridgeProtocolVersion,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	bridge := raw["bridge"].(map[string]interface{})
	if _, exists := bridge["security"]; exists {
		t.Error("security field should be omitted when nil")
	}
}
