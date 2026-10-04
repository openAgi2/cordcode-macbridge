package gobridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// DHCP LAN 广播地址刷新 + bridge_current_urls 推送定向测试。
// 覆盖:payload 形状(primary 排除出 locals)、变化/不变分支、显式 advertise host sticky、
// 广播经 DeviceConnRegistry 到达已注册连接、relay 出站归类为 control。
// SSV2:bridge_current_urls 是 top-level control-plane 帧,不进 EventMessage/timeline/replay。

func newAdvertiseRefreshServer(t *testing.T) *Server {
	t.Helper()
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-advertise-refresh")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handlers.Shutdown(ctx)
	})
	server := NewServerWithEpoch(handlers, "epoch-advertise-refresh")
	server.SetBridgeIdentity("brg-refresh", "Mac", "0.1-fixture", "ws://192.168.1.5:8777/bridge", "")
	server.SetLocalCandidateURLs([]string{"ws://192.168.1.5:8777/bridge", "ws://192.168.1.9:8777/bridge"})
	return server
}

func injectAdvertisedURLs(t *testing.T, local string, locals []string) {
	t.Helper()
	orig := resolveAdvertisedLocalURLsFn
	resolveAdvertisedLocalURLsFn = func(int) (string, []string) { return local, locals }
	t.Cleanup(func() { resolveAdvertisedLocalURLsFn = orig })
}

func TestBuildBridgeCurrentURLsPayload_Shape(t *testing.T) {
	payload := buildBridgeCurrentURLsPayload(
		"ws://192.168.1.2:8777/bridge",
		[]string{"ws://192.168.1.2:8777/bridge", "ws://192.168.1.7:8777/bridge"},
	)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type":"bridge_current_urls"`) {
		t.Errorf("missing top-level type: %s", s)
	}
	if !strings.Contains(s, `"local":"ws://192.168.1.2:8777/bridge"`) {
		t.Errorf("missing currentURLs.local: %s", s)
	}
	// locals 必须排除 primary(与 hello_ack.currentURLs.locals 同语义)。
	if !strings.Contains(s, `"locals":["ws://192.168.1.7:8777/bridge"]`) {
		t.Errorf("locals must carry secondary candidates only: %s", s)
	}
}

func TestServerRefreshAdvertisedLocalURLs_ChangeThenNoOp(t *testing.T) {
	server := newAdvertiseRefreshServer(t)
	injectAdvertisedURLs(t,
		"ws://192.168.1.2:8777/bridge",
		[]string{"ws://192.168.1.2:8777/bridge", "ws://192.168.1.7:8777/bridge"},
	)
	if !server.RefreshAdvertisedLocalURLs(8777) {
		t.Fatal("first refresh after DHCP change must report changed=true")
	}
	id := server.helloIdentitySnapshot()
	if id.localURL != "ws://192.168.1.2:8777/bridge" {
		t.Fatalf("hello identity localURL = %q, want new .2 address", id.localURL)
	}
	if len(id.localCandidateURLs) != 2 || id.localCandidateURLs[0] != "ws://192.168.1.2:8777/bridge" {
		t.Fatalf("localCandidateURLs = %v, want primary first then secondary", id.localCandidateURLs)
	}
	if server.RefreshAdvertisedLocalURLs(8777) {
		t.Fatal("refresh with unchanged addresses must be a no-op (changed=false)")
	}
}

func TestServerRefreshAdvertisedLocalURLs_ExplicitHostSticky(t *testing.T) {
	server := newAdvertiseRefreshServer(t)
	t.Setenv("GO_BRIDGE_ADVERTISE_HOST", "192.168.1.99")
	injectAdvertisedURLs(t, "ws://10.9.9.9:8777/bridge", []string{"ws://10.9.9.9:8777/bridge"})
	if server.RefreshAdvertisedLocalURLs(8777) {
		t.Fatal("explicit advertise host must stay sticky; refresh must no-op")
	}
	local, _ := server.AdvertisedLocalURLs()
	if local != "ws://192.168.1.5:8777/bridge" {
		t.Fatalf("explicit host refresh must not touch identity; localURL = %q", local)
	}
}

func TestRefreshBroadcastsBridgeCurrentURLsToRegisteredConns(t *testing.T) {
	server := newAdvertiseRefreshServer(t)
	conn := &bridgeWireFixtureConn{}
	globalDeviceConnRegistry.Register("dev-refresh-broadcast", conn)
	t.Cleanup(func() { globalDeviceConnRegistry.Unregister("dev-refresh-broadcast", conn) })

	injectAdvertisedURLs(t,
		"ws://192.168.1.2:8777/bridge",
		[]string{"ws://192.168.1.2:8777/bridge", "ws://192.168.1.7:8777/bridge"},
	)
	if !server.RefreshAdvertisedLocalURLs(8777) {
		t.Fatal("refresh must detect change")
	}
	if len(conn.frames) != 1 {
		t.Fatalf("registered conn must receive exactly one frame, got %d", len(conn.frames))
	}
	var frame struct {
		Type        string    `json:"type"`
		CurrentURLs HelloURLs `json:"currentURLs"`
	}
	if err := json.Unmarshal(conn.frames[0], &frame); err != nil {
		t.Fatalf("decode broadcast frame: %v (%s)", err, conn.frames[0])
	}
	if frame.Type != "bridge_current_urls" {
		t.Fatalf("frame type = %q, want bridge_current_urls", frame.Type)
	}
	if frame.CurrentURLs.Local != "ws://192.168.1.2:8777/bridge" {
		t.Fatalf("currentURLs.local = %q, want refreshed .2 address", frame.CurrentURLs.Local)
	}
	if len(frame.CurrentURLs.Locals) != 1 || frame.CurrentURLs.Locals[0] != "ws://192.168.1.7:8777/bridge" {
		t.Fatalf("currentURLs.locals = %v, want secondary only", frame.CurrentURLs.Locals)
	}
}

func TestClassifyRelayPayload_BridgeCurrentURLsControl(t *testing.T) {
	payload := []byte(`{"type":"bridge_current_urls","currentURLs":{"local":"ws://192.168.1.2:8777/bridge"}}`)
	if got := classifyRelayPayload(payload); got != relayOutboundControl {
		t.Fatalf("classifyRelayPayload(bridge_current_urls) = %v, want control", got)
	}
}
