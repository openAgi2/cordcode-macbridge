package codexweb

// transport_identity_test.go —— S0 provider 定向测试。
// 覆盖：main-only / observer-only / 两者 shared / 断线重连（closed client 不冒充
// attached，epoch 随新连接递增）/ 探测未完成 → unknown / 停止 → 未附着 /
// endpoint 与 PeerKey 组合。provider 快照本身无失败路径（恒返回快照）；
// 「provider 错误 → unresolved」的消费侧映射在 collector/聚合测试覆盖。

import (
	"io"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type fakeIdentityTransport struct{ closed bool }

func (f *fakeIdentityTransport) Send(_ []byte) error   { return nil }
func (f *fakeIdentityTransport) Recv() ([]byte, error) { return nil, io.EOF }
func (f *fakeIdentityTransport) Close() error          { f.closed = true; return nil }

func identityTestAgent() *Agent {
	a := New(nil)
	done := make(chan struct{})
	close(done)
	a.probeDone = done
	a.endpoint = &ServiceEndpoint{UnixSocket: "/tmp/test-app-server-control.sock"}
	return a
}

func setIdentityMain(a *Agent, client *Client) {
	a.endpoint.client = client
}

var _ core.CodexWebTransportIdentityProvider = (*Agent)(nil)

func TestTransportIdentityMainOnly(t *testing.T) {
	a := identityTestAgent()
	setIdentityMain(a, NewClient(&fakeIdentityTransport{}, ConnectionEpoch(100)))
	snap, err := a.TransportIdentitySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Main.Attached || snap.Main.Epoch != 100 {
		t.Fatalf("main role: %+v", snap.Main)
	}
	if want := "/tmp/test-app-server-control.sock#100"; snap.Main.PeerKey != want {
		t.Fatalf("peer key = %q want %q", snap.Main.PeerKey, want)
	}
	if snap.Observer.Attached || snap.Observer.ErrorCode != "none" {
		t.Fatalf("observer must be absent: %+v", snap.Observer)
	}
	if snap.Epoch != 100 || snap.Endpoint == "" {
		t.Fatalf("epoch/endpoint: %+v", snap)
	}
}

func TestTransportIdentityObserverOnly(t *testing.T) {
	a := identityTestAgent()
	a.obsClient = NewClient(&fakeIdentityTransport{}, ConnectionEpoch(200))
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if snap.Observer.Attached != true || snap.Observer.Epoch != 200 {
		t.Fatalf("observer role: %+v", snap.Observer)
	}
	if snap.Main.Attached || snap.Main.ErrorCode != "none" {
		t.Fatalf("main must be absent: %+v", snap.Main)
	}
	if snap.Epoch != 200 {
		t.Fatalf("epoch = %d want 200", snap.Epoch)
	}
}

func TestTransportIdentityBothShared(t *testing.T) {
	a := identityTestAgent()
	setIdentityMain(a, NewClient(&fakeIdentityTransport{}, ConnectionEpoch(100)))
	a.obsClient = NewClient(&fakeIdentityTransport{}, ConnectionEpoch(200))
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if !snap.Main.Attached || !snap.Observer.Attached {
		t.Fatalf("both roles must attach: %+v %+v", snap.Main, snap.Observer)
	}
	if snap.Epoch != 200 {
		t.Fatalf("epoch = %d want max 200", snap.Epoch)
	}
	if snap.Main.PeerKey == "" || snap.Observer.PeerKey == "" {
		t.Fatalf("attached roles need peer keys: %+v", snap)
	}
}

func TestTransportIdentityReconnectNoStaleAttach(t *testing.T) {
	a := identityTestAgent()
	old := NewClient(&fakeIdentityTransport{}, ConnectionEpoch(100))
	setIdentityMain(a, old)
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if !snap.Main.Attached {
		t.Fatalf("fresh client must attach: %+v", snap.Main)
	}
	// 断线：旧 client 关闭后仍被引用，不得冒充 attached。
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	// 重连：新连接 epoch 递增。
	setIdentityMain(a, NewClient(&fakeIdentityTransport{}, ConnectionEpoch(300)))
	snap, _ = a.TransportIdentitySnapshot(t.Context())
	if !snap.Main.Attached || snap.Main.Epoch != 300 {
		t.Fatalf("reconnected role: %+v", snap.Main)
	}
	if snap.Epoch != 300 {
		t.Fatalf("epoch must advance: %+v", snap)
	}
}

func TestTransportIdentityClosedClientNotAttached(t *testing.T) {
	a := identityTestAgent()
	c := NewClient(&fakeIdentityTransport{}, ConnectionEpoch(150))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	setIdentityMain(a, c)
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if snap.Main.Attached {
		t.Fatalf("closed client must not be attached: %+v", snap.Main)
	}
	if snap.Main.ErrorCode != "none" || snap.Main.Epoch != 150 {
		t.Fatalf("closed role semantics: %+v", snap.Main)
	}
}

func TestTransportIdentityProbePendingUnknown(t *testing.T) {
	a := New(nil)
	a.probing = true
	a.probeDone = nil
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if snap.Main.ErrorCode != "unknown" || snap.Observer.ErrorCode != "unknown" {
		t.Fatalf("probe pending must be unknown: %+v %+v", snap.Main, snap.Observer)
	}
}

func TestTransportIdentityStoppedDetaches(t *testing.T) {
	a := identityTestAgent()
	setIdentityMain(a, NewClient(&fakeIdentityTransport{}, ConnectionEpoch(100)))
	a.stopped = true
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if snap.Main.Attached {
		t.Fatalf("stopped backend must detach: %+v", snap.Main)
	}
}

func TestTransportIdentityEndpointPrecedence(t *testing.T) {
	a := identityTestAgent()
	a.endpoint = &ServiceEndpoint{TCPEndpoint: "ws://127.0.0.1:4096/v1"}
	setIdentityMain(a, NewClient(&fakeIdentityTransport{}, ConnectionEpoch(100)))
	snap, _ := a.TransportIdentitySnapshot(t.Context())
	if snap.Endpoint != "ws://127.0.0.1:4096/v1" {
		t.Fatalf("endpoint: %+v", snap.Endpoint)
	}
	if !strings.HasPrefix(snap.Main.PeerKey, "ws://127.0.0.1:4096/v1#") {
		t.Fatalf("peer key prefix: %+v", snap.Main.PeerKey)
	}
}
