package grokbuild

// D-G1（§3.5.1，owner 批准的受控 Go 改动 1/2）：leader 订阅建立失败回退
// updates.jsonl tailer。分界 = 是否已向下游转发任何 leader event（r4-B1）。
//   G1 stale socket（拨不通）→ 回退 tailer + INFO + 不删 socket
//   G2 握手/live 零事件即断开 → 同回退（updates.jsonl 是真相文件可补齐）
//   G3 已转发 ≥1 事件后断开 → 不回退，channel 照常关闭（relay 层 F-7 收口）
//   G4 ctx 取消（模拟 D-G2 主动取消）→ 不回退（relay 已退出，不得拉起
//      无人消费的 tailer）
//   G5 leader 回归探测（D-G3）：fallback tailer 期间 leader 重新 listen →
//      自动重订阅，attach 事件继续送达同一 channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// leaderFallbackFrozenMessage is the stable log anchor for "the fallback
// engaged" (grep anchor for think.md / live-log triage).
const leaderFallbackFrozenMessage = "falling back to updates.jsonl tailer"

// syncLogBuffer collects slog output from concurrent goroutines.
type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSlog(t *testing.T) *syncLogBuffer {
	t.Helper()
	buf := &syncLogBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// setupFallbackFixture builds a /tmp grokHome (sun_path limit) with
// sessions/<encoded-cwd>/<sid>/updates.jsonl (empty, tailer attaches at EOF)
// and returns home, socket path, updates path.
func setupFallbackFixture(t *testing.T, sid string) (home, sock, updatesPath string) {
	t.Helper()
	home = filepath.Join("/tmp", fmt.Sprintf("cc-grok-fb-%d.d", time.Now().UnixNano()))
	t.Cleanup(func() { os.RemoveAll(home) })
	if err := os.MkdirAll(filepath.Join(home, "sessions", "encoded-cwd", sid), 0o755); err != nil {
		t.Fatalf("mkdir sessions tree: %v", err)
	}
	updatesPath = filepath.Join(home, "sessions", "encoded-cwd", sid, "updates.jsonl")
	if err := os.WriteFile(updatesPath, nil, 0o644); err != nil {
		t.Fatalf("create empty updates.jsonl: %v", err)
	}
	return home, filepath.Join(home, "leader.sock"), updatesPath
}

// waitTailerAttached blocks until the fallback tailer logs its start line —
// appending before attach would be skipped as history (tail starts at EOF).
func waitTailerAttached(t *testing.T, logs *syncLogBuffer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains([]byte(logs.String()), []byte("updates file tailer starting")) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fallback tailer never started")
}

// expectTailerEvent appends one live agent_message_chunk line and asserts the
// fallback tailer delivers it on the subscribe channel.
func expectTailerEvent(t *testing.T, logs *syncLogBuffer, events <-chan core.Event, sid, updatesPath, marker string) {
	t.Helper()
	waitTailerAttached(t, logs)
	appendUpdates(t, updatesPath, updatesLine("session/update", sid,
		map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": marker}},
		nil))
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("subscribe channel closed before the tailer delivered the event")
		}
		if ev.Type != core.EventText || ev.Content != marker {
			t.Fatalf("tailer event = %+v, want EventText %q", ev, marker)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback tailer never delivered the appended updates.jsonl event")
	}
}

// runMockLeader runs a mock leader server whose behavior after completing the
// full handshake (register → registered → acp initialize → response →
// session/load → response) is delegated to afterLoad. Like the real leader
// (server.rs accept loop), it keeps accepting: probe connections from the
// reclaim path (dial + immediate close, no register frame) are tolerated
// silently. It signals serverErr once session/load has been answered.
func runMockLeader(t *testing.T, sock string, afterLoad func(c net.Conn)) <-chan error {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	serverErr := make(chan error, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return // listener closed (teardown)
			}
			go func(c net.Conn) {
				defer c.Close()
				if err := serveMockLeaderConn(c, afterLoad); err != nil {
					if err == io.EOF || strings.Contains(err.Error(), "closed") {
						return // probe connection (dial + close, no register) — tolerate
					}
					select {
					case serverErr <- err:
					default:
					}
					return
				}
				select {
				case serverErr <- nil:
				default:
				}
			}(c)
		}
	}()
	return serverErr
}

// serveMockLeaderConn performs the register → initialize → session/load
// handshake, then hands the connection to afterLoad. A connection that dies
// before register (the reclaim probe) surfaces as EOF.
func serveMockLeaderConn(c net.Conn, afterLoad func(c net.Conn)) error {
	reg, err := readClientMsg(c)
	if err != nil {
		return err
	}
	if reg.Type != "register" || reg.ClientType != leaderClientType {
		return fmt.Errorf("want register/%s, got %s/%s", leaderClientType, reg.Type, reg.ClientType)
	}
	rr, _ := json.Marshal(leaderServerMsg{Type: "registered", Ready: true})
	if err := writeTestFrame(c, rr); err != nil {
		return err
	}

	init, err := readClientMsg(c)
	if err != nil {
		return err
	}
	if init.Type != "acp" {
		return fmt.Errorf("want acp initialize, got %s", init.Type)
	}
	if err := writeACPResponse(c, acpPayloadID(init.Payload), map[string]any{"protocolVersion": "1"}); err != nil {
		return err
	}

	load, err := readClientMsg(c)
	if err != nil {
		return err
	}
	if load.Type != "acp" {
		return fmt.Errorf("want acp session/load, got %s", load.Type)
	}
	if err := writeACPResponse(c, acpPayloadID(load.Payload), map[string]any{}); err != nil {
		return err
	}

	if afterLoad != nil {
		afterLoad(c)
	}
	return nil
}

// G1: socket file exists (stat → leader path) but nothing listens → dial fails,
// zero events → fallback tailer on the same channel + INFO + socket NOT deleted.
func TestLeaderFallbackG1StaleSocketFallsBackToTailer(t *testing.T) {
	fastTailKnobs()
	logs := captureSlog(t)
	sid := "01fb-g1-stale-socket"
	home, sock, updatesPath := setupFallbackFixture(t, sid)

	// Stale socket: a file at the socket path that no leader is serving.
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatalf("create stale socket file: %v", err)
	}

	a := &Agent{grokHome: home, workDir: "/tmp"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := a.SubscribeSessionEvents(ctx, sid, "/tmp")
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	expectTailerEvent(t, logs, events, sid, updatesPath, "tailer-alive-g1")

	if got := logs.String(); !bytes.Contains([]byte(got), []byte(leaderFallbackFrozenMessage)) {
		t.Fatalf("missing fallback INFO %q in log:\n%s", leaderFallbackFrozenMessage, got)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket must NOT be deleted on fallback, stat: %v", err)
	}

	cancel()
	for range events {
	}
}

// G2: handshake completes but the connection drops before any event is
// forwarded (live 但无事件即断开) → same fallback; updates.jsonl 补齐不丢事件.
func TestLeaderFallbackG2HandshakeDisconnectFallsBack(t *testing.T) {
	fastTailKnobs()
	logs := captureSlog(t)
	sid := "01fb-g2-handshake-drop"
	home, sock, updatesPath := setupFallbackFixture(t, sid)

	serverErr := runMockLeader(t, sock, func(c net.Conn) {
		// Zero events, then drop the connection.
		_ = c.Close()
	})

	a := &Agent{grokHome: home, workDir: "/tmp"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := a.SubscribeSessionEvents(ctx, sid, "/tmp")
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	expectTailerEvent(t, logs, events, sid, updatesPath, "tailer-alive-g2")

	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}
	if got := logs.String(); !bytes.Contains([]byte(got), []byte(leaderFallbackFrozenMessage)) {
		t.Fatalf("missing fallback INFO %q in log:\n%s", leaderFallbackFrozenMessage, got)
	}

	cancel()
	for range events {
	}
}

// G3: ≥1 event forwarded before the disconnect → NO fallback: the channel
// closes normally and the relay layer's F-7 path owns the收口.
func TestLeaderFallbackG3ForwardedEventsNoFallback(t *testing.T) {
	logs := captureSlog(t)
	sid := "01fb-g3-events-then-drop"
	home, sock, _ := setupFallbackFixture(t, sid)

	serverErr := runMockLeader(t, sock, func(c net.Conn) {
		if err := writeACPNotification(c, "session/update", map[string]any{
			"sessionId": sid,
			"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "live-before-drop"}},
		}); err != nil {
			t.Errorf("write live notification: %v", err)
		}
		_ = c.Close()
	})

	a := &Agent{grokHome: home, workDir: "/tmp"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := a.SubscribeSessionEvents(ctx, sid, "/tmp")
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Type != core.EventText || ev.Content != "live-before-drop" {
			t.Fatalf("leader event = %+v, want EventText live-before-drop", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leader live event never arrived")
	}

	// Channel must close (no fallback tailer keeps it open).
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected channel close after single event, got another event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after leader disconnect with forwarded events (fallback wrongly engaged?)")
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}
	if got := logs.String(); bytes.Contains([]byte(got), []byte(leaderFallbackFrozenMessage)) {
		t.Fatalf("fallback INFO must NOT appear after events were forwarded:\n%s", got)
	}
}

// G4: ctx cancelled (the D-G2 shape) while the leader connection is healthy →
// NO fallback: nobody consumes a resurrected tailer.
func TestLeaderFallbackG4CtxCancelNoFallback(t *testing.T) {
	logs := captureSlog(t)
	sid := "01fb-g4-ctx-cancel"
	home, sock, _ := setupFallbackFixture(t, sid)

	handshakeDone := make(chan struct{})
	serverErr := runMockLeader(t, sock, func(c net.Conn) {
		close(handshakeDone)
		// Hold the connection until the test tears it down via ctx cancel.
		buf := make([]byte, 4096)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	})

	a := &Agent{grokHome: home, workDir: "/tmp"}
	ctx, cancel := context.WithCancel(context.Background())
	events, err := a.SubscribeSessionEvents(ctx, sid, "/tmp")
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	select {
	case <-handshakeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handshake never completed")
	}
	cancel()

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected channel close after ctx cancel, got an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after ctx cancel")
	}

	if got := logs.String(); bytes.Contains([]byte(got), []byte(leaderFallbackFrozenMessage)) {
		t.Fatalf("fallback INFO must NOT appear on ctx cancel:\n%s", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}
}

// G5 (D-G3 reclaim): with the fallback tailer running (no leader), a leader
// that starts listening again is probed, the tailer stops, and the subscriber
// re-attaches — its post-load events keep flowing on the SAME channel. This
// is the reconnect-recovery path for pending questions: the leader's
// attach-time interaction replay rides the re-subscription.
func TestLeaderFallbackG5ReclaimWhenLeaderReturns(t *testing.T) {
	fastTailKnobs()
	oldProbe := leaderReclaimProbeInterval
	leaderReclaimProbeInterval = 20 * time.Millisecond
	t.Cleanup(func() { leaderReclaimProbeInterval = oldProbe })
	logs := captureSlog(t)
	sid := "01fb-g5-reclaim"
	home, sock, updatesPath := setupFallbackFixture(t, sid)

	a := &Agent{grokHome: home, workDir: "/tmp"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := a.SubscribeSessionEvents(ctx, sid, "/tmp")
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	// Phase 1: no leader — the tailer delivers appended updates.
	expectTailerEvent(t, logs, events, sid, updatesPath, "tailer-before-reclaim")

	// Phase 2: the leader comes back and pushes a live event after load.
	serverErr := runMockLeader(t, sock, func(c net.Conn) {
		if err := writeACPNotification(c, "session/update", map[string]any{
			"sessionId": sid,
			"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "leader-live-after-reclaim"}},
		}); err != nil {
			t.Errorf("write live notification: %v", err)
		}
		// Hold the connection until teardown.
		buf := make([]byte, 4096)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	})

	// Phase 3: the reclaim probe re-subscribes; the leader event arrives on
	// the same channel.
	select {
	case ev := <-events:
		if ev.Type != core.EventText || ev.Content != "leader-live-after-reclaim" {
			t.Fatalf("post-reclaim event = %+v, want EventText leader-live-after-reclaim", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("leader never re-attached (reclaim probe did not fire?)")
	}
	if got := logs.String(); !bytes.Contains([]byte(got), []byte("leader reclaim: socket accepting again, re-subscribing")) {
		t.Fatalf("missing reclaim INFO in log:\n%s", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}

	cancel()
	for range events {
	}
}
