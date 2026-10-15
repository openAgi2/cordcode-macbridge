package gobridge

import (
	"sync"
	"testing"
)

// fakeRevokableConn 是用于测试的 Connection 实现，记录收到的消息与 Close 调用。
// 验证场景4：DeviceConnRegistry 现在存 Connection 接口，direct 与 relay（RelayDeviceConn）
// 都能注册，且 DisconnectDevice 对二者都下发 device_revoked 事件并 Close。
type fakeRevokableConn struct {
	mu         sync.Mutex
	sent       []map[string]interface{}
	closed     bool
	device     *TrustedDeviceRecord
	remoteAddr string
	metadata   DeviceConnectionMetadata
}

func (f *fakeRevokableConn) SendJSON(v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := v.(map[string]interface{}); ok {
		f.sent = append(f.sent, m)
	} else if event, ok := v.(EventMessage); ok {
		f.sent = append(f.sent, map[string]interface{}{"type": event.Type, "event": event.Event, "message": event.Message})
	}
}
func (f *fakeRevokableConn) SendResult(string, interface{}, *WireError)            {}
func (f *fakeRevokableConn) SendEvent(string, string, string, interface{})         {}
func (f *fakeRevokableConn) AuthedDevice() *TrustedDeviceRecord                    { return f.device }
func (f *fakeRevokableConn) RemoteAddr() string                                    { return f.remoteAddr }
func (f *fakeRevokableConn) ConnectionTransportMetadata() DeviceConnectionMetadata { return f.metadata }
func (f *fakeRevokableConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// 场景4 核心回归：registry 接受任意 Connection（非仅 *Conn），
// DisconnectDevice 下发 device_revoked 事件并 Close。
func TestDeviceConnRegistry_DisconnectDevice_SendsEventAndCloses(t *testing.T) {
	reg := &DeviceConnRegistry{conns: make(map[string][]deviceConnEntry)}
	reg.SetEventPublisher(NewEventPublisher("test-epoch"))
	conn := &fakeRevokableConn{remoteAddr: "fake:1"}
	reg.Register("dev_fake", conn)

	if len(reg.conns["dev_fake"]) != 1 {
		t.Fatalf("注册后应有 1 个连接，实际 %d", len(reg.conns["dev_fake"]))
	}

	reg.DisconnectDevice("dev_fake")

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.closed {
		t.Error("DisconnectDevice 后连接应被 Close")
	}
	if len(conn.sent) != 1 {
		t.Fatalf("应下发 1 条消息，实际 %d", len(conn.sent))
	}
	if conn.sent[0]["event"] != "device_revoked" {
		t.Errorf("事件类型应为 device_revoked，实际 %v", conn.sent[0]["event"])
	}
	if _, ok := reg.conns["dev_fake"]; ok {
		t.Error("DisconnectDevice 后该 deviceID 应从 registry 移除")
	}
}

// 场景4：多个连接（模拟 direct + relay 并存）都被撤销。
func TestDeviceConnRegistry_DisconnectDevice_AllConnectionsOfDevice(t *testing.T) {
	reg := &DeviceConnRegistry{conns: make(map[string][]deviceConnEntry)}
	reg.SetEventPublisher(NewEventPublisher("test-epoch"))
	c1 := &fakeRevokableConn{remoteAddr: "direct:1"}
	c2 := &fakeRevokableConn{remoteAddr: "relay:1"}
	reg.Register("dev_multi", c1)
	reg.Register("dev_multi", c2)

	reg.DisconnectDevice("dev_multi")

	for i, c := range []*fakeRevokableConn{c1, c2} {
		c.mu.Lock()
		if !c.closed {
			t.Errorf("连接 %d 应被 Close", i)
		}
		if len(c.sent) != 1 || c.sent[0]["event"] != "device_revoked" {
			t.Errorf("连接 %d 应收到 device_revoked 事件", i)
		}
		c.mu.Unlock()
	}
}

// Unregister 不影响其他连接。
func TestDeviceConnRegistry_Unregister_PreservesOthers(t *testing.T) {
	reg := &DeviceConnRegistry{conns: make(map[string][]deviceConnEntry)}
	keep := &fakeRevokableConn{remoteAddr: "keep"}
	remove := &fakeRevokableConn{remoteAddr: "remove"}
	reg.Register("dev_two", keep)
	reg.Register("dev_two", remove)

	reg.Unregister("dev_two", remove)

	if len(reg.conns["dev_two"]) != 1 {
		t.Fatalf("Unregister 一个后应剩 1 个，实际 %d", len(reg.conns["dev_two"]))
	}
}

func TestDeviceConnRegistryPresenceSnapshot(t *testing.T) {
	reg := &DeviceConnRegistry{conns: make(map[string][]deviceConnEntry)}
	direct := &fakeRevokableConn{
		remoteAddr: "192.168.1.20:5173",
		metadata:   DeviceConnectionMetadata{Kind: DeviceConnectionTransportDirect, DirectClass: "private"},
	}
	relay := &fakeRevokableConn{
		remoteAddr: "relay:dev-b",
		metadata:   DeviceConnectionMetadata{Kind: DeviceConnectionTransportRelay},
	}
	directDuplicate := &fakeRevokableConn{
		remoteAddr: "192.168.1.20:5174",
		metadata:   DeviceConnectionMetadata{Kind: DeviceConnectionTransportDirect, DirectClass: "private"},
	}
	reg.Register("dev-b", relay)
	reg.Register("dev-b", direct)
	reg.Register("dev-b", directDuplicate)
	reg.Register("dev-a", &fakeRevokableConn{
		metadata: DeviceConnectionMetadata{Kind: DeviceConnectionTransportUnknown, DirectClass: "unknown"},
	})

	presence := reg.PresenceSnapshot()
	if len(presence) != 2 {
		t.Fatalf("presence devices=%d, want 2: %+v", len(presence), presence)
	}
	if presence[0].DeviceID != "dev-a" || presence[1].DeviceID != "dev-b" {
		t.Fatalf("presence is not sorted by device ID: %+v", presence)
	}
	if len(presence[0].Transports) != 1 || presence[0].Transports[0].Kind != DeviceConnectionTransportUnknown {
		t.Fatalf("unknown transport missing: %+v", presence[0])
	}
	want := []DevicePresenceTransport{
		{Kind: DeviceConnectionTransportDirect, DirectClass: "private"},
		{Kind: DeviceConnectionTransportRelay},
	}
	if len(presence[1].Transports) != 2 {
		t.Fatalf("duplicate direct transport was not de-duplicated: %+v", presence[1])
	}
	for i := range want {
		if presence[1].Transports[i] != want[i] {
			t.Fatalf("transport[%d]=%+v, want %+v", i, presence[1].Transports[i], want[i])
		}
	}

	reg.Unregister("dev-b", directDuplicate)
	presence = reg.PresenceSnapshot()
	if len(presence[1].Transports) != 2 {
		t.Fatalf("removing duplicate should preserve both transport kinds: %+v", presence[1])
	}
	reg.Unregister("dev-b", relay)
	presence = reg.PresenceSnapshot()
	if len(presence[1].Transports) != 1 || presence[1].Transports[0].Kind != DeviceConnectionTransportDirect {
		t.Fatalf("removing relay should preserve direct: %+v", presence[1])
	}
}

func TestConnectionMetadataNormalizesMissingDirectClass(t *testing.T) {
	metadata := connectionMetadata(&fakeRevokableConn{
		metadata: DeviceConnectionMetadata{Kind: DeviceConnectionTransportDirect},
	})
	if metadata.Kind != DeviceConnectionTransportDirect || metadata.DirectClass != "unknown" {
		t.Fatalf("metadata=%+v, want direct/unknown", metadata)
	}
}
