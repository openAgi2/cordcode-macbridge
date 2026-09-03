package grokbuild

// D-G0 (2026-09-02 owner 裁决「Mac 侧修」): v2 projection 开启路径的 get_session_projection
// 不携带 directory（iOS ProjectionStore 硬编码 nil），handlers 回落 GetWorkDir() 得到与
// session 无关的 runtime 工作目录，grok leader 校验 session/load cwd 不符即拒
// （session/load: Path not found.，实测 37ms 静默退出）。修复 = 按 sessionID 从 grok 自己
// 的 sessions 树反查权威项目目录。本文件验证：纯解析函数三态（命中/缺失/歧义）+
// SubscribeSessionEvents 在 leader 路径上把反查结果送进 session/load 的 cwd。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveSessionCwd(t *testing.T) {
	home := t.TempDir()
	sid := "01a06048-0fc7-7e83-b84b-f569fd7ad4b2"
	proj := "/Users/x/proj-a"
	if err := os.MkdirAll(filepath.Join(home, "sessions", url.PathEscape(proj), sid), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveSessionCwd(home, sid); got != proj {
		t.Fatalf("resolveSessionCwd = %q, want %q", got, proj)
	}
	if got := resolveSessionCwd(home, "no-such-session"); got != "" {
		t.Fatalf("missing session: resolveSessionCwd = %q, want empty", got)
	}
	if got := resolveSessionCwd(t.TempDir(), sid); got != "" {
		t.Fatalf("missing sessions tree: resolveSessionCwd = %q, want empty", got)
	}

	// Ambiguity: two project dirs claim the same session → fail closed (caller keeps its value).
	proj2 := "/Users/x/proj-b"
	if err := os.MkdirAll(filepath.Join(home, "sessions", url.PathEscape(proj2), sid), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveSessionCwd(home, sid); got != "" {
		t.Fatalf("ambiguous: resolveSessionCwd = %q, want empty", got)
	}
}

// TestSubscribeSessionEventsResolvesCwdFromSessionStore mirrors the production
// failure shape: caller passes an unrelated cwd (runtime work dir), the session
// store knows the true project — the leader must receive session/load with the
// store-resolved cwd, not the caller's.
func TestSubscribeSessionEventsResolvesCwdFromSessionStore(t *testing.T) {
	// macOS sun_path limit: keep the socket path short.
	home := filepath.Join("/tmp", fmt.Sprintf("cc-grok-cwd-%d.sock.d", time.Now().UnixNano()))
	defer os.RemoveAll(home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}

	proj := "/tmp/grok-resolved-proj"
	sid := "01bbbbbbbb-cwd-resolve"
	if err := os.MkdirAll(filepath.Join(home, "sessions", url.PathEscape(proj), sid), 0o755); err != nil {
		t.Fatal(err)
	}

	sock := filepath.Join(home, "leader.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	loadCwd := make(chan string, 1)
	serverErr := make(chan error, 1)
	// Accept loop like the real leader: the subscribe path's reclaim probe
	// dials and closes without registering — tolerate those connections.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return // listener closed (teardown)
			}
			go func(c net.Conn) {
				defer c.Close()
				serveCwdCaptureLeader(c, loadCwd, serverErr)
			}(c)
		}
	}()

	// Mirror production: caller cwd is the runtime work dir (unrelated to the session).
	a := &Agent{grokHome: home, workDir: "/Users/rt/work-dir"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := a.SubscribeSessionEvents(ctx, sid, "/Users/rt/work-dir")
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}
	defer func() { cancel(); for range events { } }()

	select {
	case got := <-loadCwd:
		if got != proj {
			t.Fatalf("session/load cwd = %q, want store-resolved %q", got, proj)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session/load never reached the leader")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}
}

// serveCwdCaptureLeader performs the register → initialize → session/load
// handshake, capturing the load cwd. A connection that dies before register
// (the reclaim probe) surfaces as EOF and is tolerated.
func serveCwdCaptureLeader(c net.Conn, loadCwd chan<- string, serverErr chan<- error) {
	reg, err := readClientMsg(c)
	if err != nil {
		return // probe connection (dial + close, no register) — tolerate
	}
	if reg.Type != "register" || reg.ClientType != leaderClientType {
		serverErr <- fmt.Errorf("want register/%s, got %s/%s", leaderClientType, reg.Type, reg.ClientType)
		return
	}
	rr, _ := json.Marshal(leaderServerMsg{Type: "registered", Ready: true})
	if err := writeTestFrame(c, rr); err != nil {
		serverErr <- err
		return
	}

	init, err := readClientMsg(c)
	if err != nil {
		serverErr <- err
		return
	}
	if init.Type != "acp" {
		serverErr <- fmt.Errorf("want acp initialize, got %s", init.Type)
		return
	}
	if err := writeACPResponse(c, acpPayloadID(init.Payload), map[string]any{"protocolVersion": "1"}); err != nil {
		serverErr <- err
		return
	}

	load, err := readClientMsg(c)
	if err != nil {
		serverErr <- err
		return
	}
	if load.Type != "acp" {
		serverErr <- fmt.Errorf("want acp session/load, got %s", load.Type)
		return
	}
	var req struct {
		Params struct {
			Cwd string `json:"cwd"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(load.Payload), &req); err != nil {
		serverErr <- fmt.Errorf("parse session/load params: %w", err)
		return
	}
	select {
	case loadCwd <- req.Params.Cwd:
	default:
	}
	if err := writeACPResponse(c, acpPayloadID(load.Payload), map[string]any{}); err != nil {
		serverErr <- err
		return
	}
	// Hold the connection briefly so Run stays in the live loop, then close.
	time.Sleep(120 * time.Millisecond)
	select {
	case serverErr <- nil:
	default:
	}
}
