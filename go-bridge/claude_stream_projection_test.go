package gobridge

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// claude_stream_projection_test.go —— 官方流式对齐（owner 2026-09-04 复测
// 「无流式输出/回复重复」）的两块拼图：
//
//	① backfillClaudeStreamTurnID：stdout 流式增量补 ActiveTurn 身份 → 进投影
//	② agentRelayActive 时 file-relay assistant 行 cursor-only → 不双份
//
// 官方模型参照：Claude CLI stream-json 的 stream_event 逐 token 流式 +
// 完成帧差量收口（Agent SDK 消费者行为）；transcript 行只做冷启动基线。

// ① 无身份流式 delta 补 turnId 后，经**生产全链路**（deltaBatcher 攒批 →
// EventPublisher → IngestLive）把正文 append 进 active turn 的 assistant item。
// d5f5e30 假绿教训（owner 2026-09-05 复盘）：当时直连 kernel.IngestLive 绕过了
// deltaBatcher，而 emit() 重组 payload 丢 turnId——测试绿、生产全程无流式。
// 本测试从 batcher 入口驱动，锁住「身份必须穿过攒批层」。
func TestBackfillClaudeStreamTurnID_AppendsToActiveTurn(t *testing.T) {
	handlers := newTestHandlers(t)
	const sessionID = "stream-backfill"
	// file-relay user 行建 turn（模拟 batch 事务效果：直接经 kernel 事件面）
	// 用 deliverClaudeLegacyRow 太重；直接走 publisher 的 hydrate 事件入口最贴近
	// 生产（IngestLive 与 batch 都汇入同一 reducer）。此处用 batch 路径建 turn。
	correlation := claudeSourceCorrelation{SegmentStableKey: "seg", SegmentGeneration: "gen"}
	state := ClaudeSourceState{
		SchemaVersion: ClaudeSourceStateSchemaVersion, SourceGeneration: "sg",
		CursorVector:   []ClaudeSourceCursor{{SegmentStableKey: "seg", SegmentGeneration: "gen", MembershipDigest: "md"}},
		GraphNodes:     map[string][]ClaudeGraphOccurrence{},
		LogicalRecords: map[string]ClaudeLogicalRecord{},
	}
	if err := handlers.projectionKernel.InstallClaudeSourceState("claude", sessionID, state); err != nil {
		t.Fatal(err)
	}
	userRow := `{"type":"user","uuid":"u-b","message":{"role":"user","content":"问题B"}}`
	scan, err := scanCompleteClaudeRelayEntriesFromReader(strings.NewReader(userRow+"\n"), 0, &claudeRelayScanState{})
	if err != nil || len(scan.Records) == 0 {
		t.Fatalf("scan: err=%v records=%d", err, len(scan.Records))
	}
	current, _ := handlers.projectionKernel.ClaudeSourceStateSnapshot("claude", sessionID)
	batch, err := buildClaudeSourceRecordBatch(current, scan.Records[0], "claude", sessionID, "e", correlation, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handlers.projectionKernel.ApplyClaudeSourceRecordBatch(batch); err != nil {
		t.Fatal(err)
	}

	// ActiveTurn 已置位（user_message → markRunning）
	active := handlers.projectionKernel.ActiveTurnID("claude", sessionID)
	if active == "" {
		t.Fatalf("ActiveTurnID empty after user row batch")
	}

	// 无身份流式 delta（session stdout 路径的 wire 形状）→ 补全 → **经生产链路**
	// （relayEvents 的 deltaBatcher.Send；data 形状 = mapAgentEvent 输出 + backfill）
	raw := map[string]interface{}{"delta": "回复B前半"}
	handlers.backfillClaudeStreamTurnID("claude", sessionID, "text_delta", raw)
	if got, _ := raw["turnId"].(string); got != active {
		t.Fatalf("backfilled turnId = %q, want %q", got, active)
	}
	handlers.deltaBatcher.Send(LogicalEvent{
		BackendID: "claude", SessionID: sessionID,
		Event: "text_delta", Data: raw,
	})
	handlers.deltaBatcher.FlushAll()

	// 已有身份/非流式事件不动
	data2 := map[string]interface{}{"delta": "x", "itemId": "other"}
	handlers.backfillClaudeStreamTurnID("claude", sessionID, "text_delta", data2)
	if _, has := data2["turnId"]; has {
		t.Fatalf("must not overwrite itemId-bearing delta")
	}

	// 投影：turn 的 assistant 文本含流式增量（穿过了 batcher 的身份透传）
	projection, ok := handlers.projectionKernel.reducer.Snapshot("claude", sessionID)
	if !ok {
		t.Fatal("no projection")
	}
	var found bool
	for _, turn := range projection.Turns {
		if turn.TurnID != active || turn.Assistant == nil {
			continue
		}
		var text string
		for _, p := range turn.Assistant.Parts {
			text += p.Text
		}
		if strings.Contains(text, "回复B前半") {
			found = true
		}
	}
	if !found {
		t.Fatalf("streamed delta missing from projection after batcher/publisher/kernel chain; turns=%+v", projection.Turns)
	}
}

// ② agentRelayActive 时 assistant 完成态行 cursor-only：内容不进投影（stdout
// 权威），后续 user 行 batch 不产生 cursor gap。
func TestFileRelayAssistantRowCursorOnlyWhenAgentRelayActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withFastClaudeFileRelay(t)
	const sessionID = "stdout-owns-content"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"u-a","message":{"role":"user","content":"问题A"}}`,
	)
	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 4242, Live: true},
		},
		alivePIDs: map[int]bool{4242: true},
	}
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: sessionID})
	handlers.startClaudeSessionFileRelayAt(sessionID, serverConn, "claude", nil)
	client := &websocketClient{conn: clientConn}
	_ = client.readEvents(t, 1) // 初始 idle，确保初扫完成

	// agent relay 接管（模拟 send_message 后 StartSession → ensureAgentRelay）
	handlers.mu.Lock()
	handlers.agentRelayRunning[sessionID] = true
	handlers.mu.Unlock()

	// assistant 完成态行 + 后续 user 行（新 turn）
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"as-a","parentUuid":"u-a","message":{"id":"msg-a","role":"assistant","content":[{"type":"text","text":"回复A(stdout已发)"}],"stop_reason":"end_turn"}}`,
		`{"type":"user","uuid":"u-b","parentUuid":"as-a","message":{"role":"user","content":"问题B"}}`,
	)

	// 宽松收集 ≤4s：期望 file-relay 不产 assistant 正文（stdout 权威），
	// user B 行仍建新 turn（turn_started 到达）；无 "回复A(stdout已发)" 文本。
	var sawUserBTurn, sawStaleAssistantText bool
	var names []string
	_ = client.conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		var ev map[string]any
		if err := client.conn.ReadJSON(&ev); err != nil {
			break
		}
		data, _ := ev["data"].(map[string]any)
		delta, _ := data["delta"].(string)
		if strings.Contains(delta, "stdout已发") {
			sawStaleAssistantText = true
		}
		if ev["event"] == "turn_started" {
			if tid, _ := data["turnId"].(string); strings.Contains(tid, "u-b") {
				sawUserBTurn = true
				break
			}
		}
		names = append(names, fmt.Sprintf("%v", ev["event"]))
	}
	if sawStaleAssistantText {
		t.Fatalf("assistant content must stay stdout-owned while agent relay active")
	}
	if !sawUserBTurn {
		t.Fatalf("user B turn not established after cursor-only assistant row (gap?): events=%v", names)
	}
}

// ③ agent relay 不活跃（外部会话）：assistant 行照常进投影（现状保持）。
func TestFileRelayAssistantRowStillProjectsWhenNoAgentRelay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withFastClaudeFileRelay(t)
	const sessionID = "external-keeps-content"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"u-a","message":{"role":"user","content":"问题A"}}`,
	)
	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 4242, Live: true},
		},
		alivePIDs: map[int]bool{4242: true},
	}
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: sessionID})
	handlers.startClaudeSessionFileRelayAt(sessionID, serverConn, "claude", nil)
	client := &websocketClient{conn: clientConn}
	_ = client.readEvents(t, 1)

	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"as-a","parentUuid":"u-a","message":{"id":"msg-a","role":"assistant","content":[{"type":"text","text":"外部回复A"}],"stop_reason":"end_turn"}}`,
	)
	var sawText bool
	_ = client.conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		var ev map[string]any
		if err := client.conn.ReadJSON(&ev); err != nil {
			break
		}
		data, _ := ev["data"].(map[string]any)
		if delta, _ := data["delta"].(string); strings.Contains(delta, "外部回复A") {
			sawText = true
			break
		}
	}
	if !sawText {
		t.Fatalf("external session assistant content must still project")
	}
}
