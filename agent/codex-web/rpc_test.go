package codexweb

// rpc_test.go —— JSON-RPC 客户端核心测试（§13.1 transport：request id correlation、
// 并发响应、unknown notification、断线 epoch、server request response、ordered events、
// bounded shutdown）+ 官方真实帧回放（testdata fixture，非手写形状）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// scriptedTransport 按脚本投递帧；Send 只记录（可选 onSend 触发应答）。
type scriptedTransport struct {
	mu     sync.Mutex
	sent   [][]byte
	onSend func(payload []byte)
	frames chan []byte
	closed chan struct{}
}

func newScripted() *scriptedTransport {
	return &scriptedTransport{frames: make(chan []byte, 256), closed: make(chan struct{})}
}

func (s *scriptedTransport) push(frame string) {
	b := []byte(frame)
	select {
	case s.frames <- b:
	case <-s.closed:
	}
}

func (s *scriptedTransport) Send(payload []byte) error {
	s.mu.Lock()
	s.sent = append(s.sent, append([]byte(nil), payload...))
	onSend := s.onSend
	s.mu.Unlock()
	if onSend != nil {
		onSend(payload)
	}
	return nil
}

func (s *scriptedTransport) Recv() ([]byte, error) {
	select {
	case b := <-s.frames:
		return b, nil
	case <-s.closed:
		return nil, errors.New("scripted closed")
	}
}

func (s *scriptedTransport) Close() error {
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	return nil
}

func (s *scriptedTransport) sentFrames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.sent))
	for _, b := range s.sent {
		out = append(out, string(b))
	}
	return out
}




// autoResponder 让 scripted 按 method+id 生成结果（延迟可注入以制造乱序；多次调用组合生效）。
func autoResponder(s *scriptedTransport, method string, result any, delay time.Duration) {
	s.mu.Lock()
	prev := s.onSend
	s.onSend = func(payload []byte) {
		if prev != nil {
			prev(payload)
		}
		go func() {
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(payload, &req)
			if req.Method != method {
				return
			}
			time.Sleep(delay)
			frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			s.push(string(frame))
		}()
	}
	s.mu.Unlock()
}

func TestRPCOutofOrderCorrelation(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()

	autoResponder(s, "slow", map[string]any{"v": "slow"}, 150*time.Millisecond)
	autoResponder(s, "fast", map[string]any{"v": "fast"}, 0)

	var wg sync.WaitGroup
	results := make(map[string]string)
	var mu sync.Mutex
	for _, m := range []string{"slow", "fast"} {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			raw, rpcErr, err := c.Request(m, nil)
			if err != nil || rpcErr != nil {
				t.Errorf("%s: %v %v", m, err, rpcErr)
				return
			}
			var r struct {
				V string `json:"v"`
			}
			_ = json.Unmarshal(raw, &r)
			mu.Lock()
			results[m] = r.V
			mu.Unlock()
		}(m)
	}
	wg.Wait()
	if results["slow"] != "slow" || results["fast"] != "fast" {
		t.Fatalf("乱序响应相关性错乱：%v", results)
	}
}

func TestRPCUnknownNotificationTolerated(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()

	s.push(`{"jsonrpc":"2.0","method":"some/future/notification","params":{"x":1}}`)
	s.push(`{"jsonrpc":"2.0","method":"another/one"}`)

	deadline := time.After(2 * time.Second)
	got := 0
	for got < 2 {
		select {
		case n := <-c.Notifications():
			got++
			if got == 1 && n.Method != "some/future/notification" {
				t.Fatalf("未知通知应按原文记录，得到 %s", n.Method)
			}
		case <-deadline:
			t.Fatalf("未知通知未被投递（%d/2）", got)
		}
	}
	// 连接不崩：后续请求仍可用
	autoResponder(s, "ping", map[string]any{"ok": true}, 0)
	if _, _, err := c.Request("ping", nil); err != nil {
		t.Fatalf("未知通知后连接应存活：%v", err)
	}
}

func TestRPCUnparseableFrameTolerated(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()

	s.push(`not-json-at-all`)
	select {
	case n := <-c.Notifications():
		if n.Method != "__unparseable__" {
			t.Fatalf("坏帧应标记 __unparseable__：%s", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("坏帧未投递")
	}
	autoResponder(s, "ping", map[string]any{}, 0)
	if _, _, err := c.Request("ping", nil); err != nil {
		t.Fatalf("坏帧后连接应存活：%v", err)
	}
}

func TestRPCServerRequestRespondsToOriginalID(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 7)
	defer c.Close()

	s.push(`{"jsonrpc":"2.0","id":0,"method":"item/commandExecution/requestApproval","params":{"threadId":"th1","turnId":"tu1","itemId":"it1"}}`)
	select {
	case sr := <-c.ServerRequests():
		if sr.Epoch != 7 || sr.RequestID != "0" || sr.ThreadID != "th1" || sr.TurnID != "tu1" {
			t.Fatalf("server request 字段错乱：%+v", sr)
		}
		if err := c.RespondServerRequest(sr.RequestID, map[string]any{"decision": "accept"}); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server request 未投递")
	}
	frames := s.sentFrames()
	var found bool
	for _, f := range frames {
		if contains(f, `"id":0`) && contains(f, `"decision":"accept"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("响应未回原 id： %v", frames)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestRPCDisconnectFailsPending(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	// 不注册 responder → 请求挂起
	done := make(chan error, 1)
	go func() {
		_, _, err := c.Request("hang", nil)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	_ = s.Close()
	select {
	case err := <-done:
		if err == nil || !contains(err.Error(), "connection") {
			t.Fatalf("断线后 pending 应失败并指明连接： %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("断线后 pending 未失败")
	}
	// 断线后新请求立即失败；新连接 epoch 递增
	if _, _, err := c.Request("after", nil); err == nil {
		t.Fatal("断线后请求应失败")
	}
	c2 := NewClient(newScripted(), 2)
	if c2.Epoch() <= c.Epoch() {
		t.Fatalf("epoch 应单调：%d vs %d", c2.Epoch(), c.Epoch())
	}
	_ = c2.Close()
	_ = c.Close()
}

func TestRPCOrderedEventDelivery(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	const n = 20
	for i := 0; i < n; i++ {
		s.push(fmt.Sprintf(`{"jsonrpc":"2.0","method":"ev/%02d"}`, i))
	}
	for i := 0; i < n; i++ {
		select {
		case got := <-c.Notifications():
			if got.Method != fmt.Sprintf("ev/%02d", i) {
				t.Fatalf("顺序破坏：期望 ev/%02d 得到 %s", i, got.Method)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("第 %d 条通知未到", i)
		}
	}
}

func TestRPCBoundedIdempotentClose(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	for i := 0; i < 8; i++ {
		s.push(fmt.Sprintf(`{"jsonrpc":"2.0","method":"n/%d"}`, i))
	}
	done := make(chan struct{})
	go func() { _ = c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(7 * time.Second):
		t.Fatal("Close 必须有界返回")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("二次 Close 应幂等：%v", err)
	}
}

// TestRPCOfficialFrameReplay 用官方真实帧（Phase 0 dumps/catalog）回放：
// 全部帧可解析、无 __unparseable__、通知顺序与录制顺序一致。
func TestRPCOfficialFrameReplay(t *testing.T) {
	path := filepath.Join("testdata", "official-0.149.0-alpha.4", "dumps", "catalog", "raw.jsonl")
	data, err := readFileLines(path)
	if err != nil {
		t.Skipf("fixture 不存在（%v），跳过回放", err)
	}
	s := newScripted()
	c := NewClient(s, 1)

	var order []string
	for _, e := range data {
		if e["dir"] != "server" {
			continue
		}
		msg := e["msg"].(map[string]any)
		b, _ := json.Marshal(msg)
		s.push(string(b))
		if m, ok := msg["method"].(string); ok {
			order = append(order, m)
		}
	}
	unparseable := 0
	consumed := 0
	timeout := time.After(5 * time.Second)
	for consumed < len(order) {
		select {
		case n := <-c.Notifications():
			if n.Method == "__unparseable__" {
				unparseable++
			} else {
				if n.Method != order[consumed] {
					t.Fatalf("回放顺序破坏：期望 %s 得到 %s", order[consumed], n.Method)
				}
				consumed++
			}
		case <-c.ServerRequests():
			// fixture 中含 server request（审批），计入顺序忽略
		case <-timeout:
			t.Fatalf("回放中断（%d/%d）", consumed, len(order))
		}
	}
	if unparseable > 0 {
		t.Fatalf("官方帧出现不可解析：%d", unparseable)
	}
	_ = c.Close()
}

func readFileLines(path string) ([]map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, line := range splitLines(string(b)) {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
