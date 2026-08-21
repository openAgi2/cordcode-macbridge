package codexweb

// lifecycle_test.go —— 生命周期单元测试（§13.1 lifecycle：external daemon / cordcode-started
// daemon / managed WS / 双失败 / incompatible / 就绪逐步失败）。
//
// 证据边界：fakePeer 只模拟官方 app-server 的 JSON-RPC 应答形状（内部逻辑测试）；
// 官方 wire 真值来自 testdata/official-0.149.0-alpha.4 fixture 与 e2e（lifecycle_e2e_test.go）。

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"context"
	"sync"
	"testing"
	"time"
)

// fakePeer 模拟一条 transport + 一个按 method 应答的 JSON-RPC 对端。
type fakePeer struct {
	mu       sync.Mutex
	handlers map[string]func(id int64, params json.RawMessage) (any, *fakeRPCError)
	out      chan []byte
	closed   chan struct{}
	closeErr error
}

type fakeRPCError struct {
	Code    int64
	Message string
}

func newFakePeer() *fakePeer {
	return &fakePeer{
		handlers: map[string]func(int64, json.RawMessage) (any, *fakeRPCError){},
		out:      make(chan []byte, 256),
		closed:   make(chan struct{}),
	}
}

func (f *fakePeer) on(method string, h func(int64, json.RawMessage) (any, *fakeRPCError)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

func happyHandlers() map[string]func(int64, json.RawMessage) (any, *fakeRPCError) {
	return map[string]func(int64, json.RawMessage) (any, *fakeRPCError){
		"initialize": func(int64, json.RawMessage) (any, *fakeRPCError) {
			return map[string]any{
				"userAgent":  "cordcode-codex-web/0.149.0-alpha.4 (Mac OS 27.0.0; arm64)",
				"codexHome":  "/tmp/cw-fake-home",
				"platformOs": "macos",
			}, nil
		},
		"thread/list": func(int64, json.RawMessage) (any, *fakeRPCError) {
			return map[string]any{"data": []any{}, "nextCursor": nil}, nil
		},
		"model/list": func(int64, json.RawMessage) (any, *fakeRPCError) {
			return map[string]any{"data": []any{map[string]any{"id": "m1"}}}, nil
		},
	}
}

func (f *fakePeer) install(handlers map[string]func(int64, json.RawMessage) (any, *fakeRPCError)) {
	for m, h := range handlers {
		f.on(m, h)
	}
}

func (f *fakePeer) Send(payload []byte) error {
	var req struct {
		ID     *int64           `json:"id"`
		Method string           `json:"method"`
		Params json.RawMessage  `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	var id int64
	if req.ID != nil {
		id = *req.ID
	}
	f.mu.Lock()
	h := f.handlers[req.Method]
	f.mu.Unlock()
	if h == nil {
		h = func(int64, json.RawMessage) (any, *fakeRPCError) { return map[string]any{}, nil }
	}
	result, rpcErr := h(id, req.Params)
	var frame map[string]any
	if rpcErr != nil {
		frame = map[string]any{"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": rpcErr.Code, "message": rpcErr.Message}}
	} else {
		frame = map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	}
	b, _ := json.Marshal(frame)
	select {
	case f.out <- b:
		return nil
	case <-f.closed:
		return errors.New("fakePeer closed")
	}
}

func (f *fakePeer) Recv() ([]byte, error) {
	select {
	case b := <-f.out:
		return b, nil
	case <-f.closed:
		return nil, errors.New("fakePeer closed")
	}
}

func (f *fakePeer) Close() error {
	select {
	case <-f.closed:
		return nil
	default:
		close(f.closed)
	}
	return f.closeErr
}

// fakeDeps 组装可编排的 LifecycleDeps。
type fakeDeps struct {
	peer        *fakePeer
	socketNow   bool
	daemonCalls int
	daemonErr   error
	udsDials    int
	tcpDials    int
	mp          *managedProcess
	mpURL       string
}

func (fd *fakeDeps) deps() LifecycleDeps {
	fd.peer = newFakePeer()
	fd.peer.install(happyHandlers())
	return LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		RunDaemonStart: func(bin, home string) (string, error) {
			fd.daemonCalls++
			if fd.daemonErr != nil {
				return "", fd.daemonErr
			}
			fd.socketNow = true
			return `{"status":"started"}`, nil
		},
		SocketExists: func(string) bool { return fd.socketNow },
		DialUDS: func(_ context.Context, _ string) (Transport, error) {
			fd.udsDials++
			return fd.peer, nil
		},
		DialTCP: func(_ context.Context, _ string) (Transport, error) {
			fd.tcpDials++
			return fd.peer, nil
		},
		StartManagedWS: func(bin, home, wd string) (string, *managedProcess, error) {
			if fd.mp == nil {
				return "", nil, errors.New("managed start unavailable（standalone 缺失）")
			}
			return fd.mpURL, fd.mp, nil
		},
		HTTPHealth: func(string) error { return nil },
	}
}

func statusErr(t *testing.T, err error) *StatusError {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("期望 *StatusError，得到 %v", err)
	}
	return se
}

func TestProbeExternalDaemonReused(t *testing.T) {
	fd := &fakeDeps{socketNow: true}
	ep, err := ProbeWith(fd.deps(), ProbeOptions{CodexHome: "/tmp/cw-fake-home"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer ep.Close()
	if ep.Source != SourceExternalDaemonReused {
		t.Fatalf("source = %s，期望 external-daemon-reused", ep.Source)
	}
	if ep.StartedByCordCode {
		t.Fatal("外部 daemon 不得标记 StartedByCordCode")
	}
	if fd.daemonCalls != 0 {
		t.Fatalf("外部 daemon 存在时不得调用 daemon start（调用 %d 次）", fd.daemonCalls)
	}
	if ep.CLIVersion != "0.149.0-alpha.4" {
		t.Fatalf("版本解析 = %q", ep.CLIVersion)
	}
	if ep.Client() == nil {
		t.Fatal("就绪后必须持有 Client")
	}
}

func TestProbeCordCodeStartedDaemon(t *testing.T) {
	fd := &fakeDeps{socketNow: false}
	ep, err := ProbeWith(fd.deps(), ProbeOptions{CodexHome: "/tmp/cw-fake-home"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer ep.Close()
	if ep.Source != SourceCordCodeStartedDaemon || !ep.StartedByCordCode {
		t.Fatalf("source=%s started=%v，期望 cordcode-started-daemon/true", ep.Source, ep.StartedByCordCode)
	}
	if fd.daemonCalls != 1 {
		t.Fatalf("daemon start 调用 %d 次", fd.daemonCalls)
	}
}

func TestProbeManagedLoopbackFallback(t *testing.T) {
	// 托管子进程用真实 sleep 进程占位，Close 后应被回收（§6.3 独占回收）。
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Skipf("无法启动 sleep: %v", err)
	}
	fd := &fakeDeps{
		socketNow: false,
		daemonErr: errors.New("managed standalone Codex install not found"),
		mp:        &managedProcess{cmd: sleep, port: 45678},
		mpURL:     "ws://127.0.0.1:45678",
	}
	ep, err := ProbeWith(fd.deps(), ProbeOptions{CodexHome: "/tmp/cw-fake-home"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if ep.Source != SourceManagedLoopbackWS {
		t.Fatalf("source = %s，期望 managed-loopback-ws", ep.Source)
	}
	if err := ep.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if sleep.ProcessState == nil {
		t.Fatal("Close 必须回收托管 app-server 进程")
	}
}

func TestProbeDoubleFailure(t *testing.T) {
	fd := &fakeDeps{socketNow: false, daemonErr: errors.New("daemon start failed")}
	_, err := ProbeWith(fd.deps(), ProbeOptions{CodexHome: "/tmp/cw-fake-home"})
	se := statusErr(t, err)
	if se.Status != StatusNotConfigured {
		t.Fatalf("status = %s，期望 not_configured", se.Status)
	}
	if !strings.Contains(se.Error(), "daemon start failed") {
		t.Fatalf("错误须保留真实原因：%v", se)
	}
}

func TestProbeIncompatibleInitialize(t *testing.T) {
	fd := &fakeDeps{socketNow: true}
	d := fd.deps()
	fd.peer.on("initialize", func(int64, json.RawMessage) (any, *fakeRPCError) {
		return nil, &fakeRPCError{Code: -32600, Message: "client too old"}
	})
	_, err := ProbeWith(d, ProbeOptions{CodexHome: "/tmp/cw-fake-home"})
	se := statusErr(t, err)
	if se.Status != StatusIncompatible || se.Step != "initialize" {
		t.Fatalf("status=%s step=%s", se.Status, se.Step)
	}
	if !strings.Contains(se.Official, "client too old") || !strings.Contains(se.Official, "-32600") {
		t.Fatalf("官方错误原文丢失：%s", se.Official)
	}
}

func TestProbeReadinessStepFailures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*fakePeer)
		status  string
		step    string
		contain string
	}{
		{
			name: "thread-list-rpc-error",
			mutate: func(p *fakePeer) {
				p.on("thread/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
					return nil, &fakeRPCError{Code: -32601, Message: "method not found"}
				})
			},
			status: StatusIncompatible, step: "thread-list", contain: "method not found",
		},
		{
			name: "model-list-rpc-error",
			mutate: func(p *fakePeer) {
				p.on("model/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
					return nil, &fakeRPCError{Code: -32601, Message: "model/list 不存在（旧版本）"}
				})
			},
			status: StatusIncompatible, step: "model-list", contain: "旧版本",
		},
		{
			name: "thread-list-shape-mismatch",
			mutate: func(p *fakePeer) {
				p.on("thread/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
					return map[string]any{"threads": "oops"}, nil
				})
			},
			status: StatusIncompatible, step: "contract-thread-list", contain: "data",
		},
		{
			name: "model-list-shape-mismatch",
			mutate: func(p *fakePeer) {
				p.on("model/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
					return map[string]any{"data": "not-array"}, nil
				})
			},
			status: StatusIncompatible, step: "contract-model-list", contain: "data",
		},
		{
			name: "initialize-shape-mismatch",
			mutate: func(p *fakePeer) {
				p.on("initialize", func(int64, json.RawMessage) (any, *fakeRPCError) {
					return map[string]any{"unexpected": true}, nil
				})
			},
			status: StatusIncompatible, step: "initialize-shape", contain: "userAgent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := &fakeDeps{socketNow: true}
			d := fd.deps()
			tc.mutate(fd.peer)
			_, err := ProbeWith(d, ProbeOptions{CodexHome: "/tmp/cw-fake-home"})
			se := statusErr(t, err)
			if se.Status != tc.status || se.Step != tc.step {
				t.Fatalf("status=%s step=%s，期望 %s/%s", se.Status, se.Step, tc.status, tc.step)
			}
			if !strings.Contains(se.Error(), tc.contain) {
				t.Fatalf("错误应含 %q：%v", tc.contain, se)
			}
		})
	}
}

func TestControlSocketPathSUNLenGuard(t *testing.T) {
	longHome := strings.Repeat("a", 80) + "/deep/home"
	if _, err := ControlSocketPath(longHome); err == nil {
		t.Fatal("超长路径必须报 SUN_LEN 错误")
	}
	p, err := ControlSocketPath("/tmp/cw-home")
	if err != nil {
		t.Fatalf("短路径不应报错: %v", err)
	}
	if !strings.HasSuffix(p, "app-server-control/app-server-control.sock") {
		t.Fatalf("路径形状错误: %s", p)
	}
}

func TestExplicitURLFailureIsVisible(t *testing.T) {
	fd := &fakeDeps{}
	d := fd.deps()
	fd.peer.closeErr = nil
	// 显式 URL 但对端 initialize 失败：不得静默降级到 daemon/托管路径
	fd.peer.on("initialize", func(int64, json.RawMessage) (any, *fakeRPCError) {
		return nil, &fakeRPCError{Code: -32000, Message: "explicit service broken"}
	})
	_, err := ProbeWith(d, ProbeOptions{ExplicitURL: "ws://127.0.0.1:9", CodexHome: "/tmp/cw-fake-home"})
	se := statusErr(t, err)
	if !strings.Contains(se.Official, "explicit service broken") {
		t.Fatalf("显式 URL 失败必须可见: %v", se)
	}
	if fd.daemonCalls != 0 {
		t.Fatal("显式 URL 失败不得触发 daemon start 降级")
	}
}

// 编译期锚定：closeOnce 结构被使用（Close 幂等）。
var _ = time.Second
