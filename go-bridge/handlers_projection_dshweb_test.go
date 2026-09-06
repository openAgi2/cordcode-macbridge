package gobridge

// dsh-web projection wiring tests (dsh-web design §4.3.2/§8-5): the backend
// joins the pathless hydrate family (HTTP rich-history baseline via
// session.history), the two forceCold sets include it, the deepseek
// store-file/live-only branch does NOT apply (no store semantics for this
// backend), it stays out of the no-external-event-source pruning list (mux
// IS the external source), and it advertises session_sync_v2.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dshWebPull performs one get_session_projection for the dsh-web backend.
func dshWebPull(h *Handlers, sessionID string, sinceRev int) (*readFileCaptureConn, WireMessage) {
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": sinceRev})
	msg := WireMessage{RequestID: "r-dshweb-" + sessionID, BackendID: "dsh-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, nil)
	return conn, msg
}

// dshWebProbeAgent lets a test force the SessionActivityProbing verdict for
// the trailing-unanswered seal test.
type dshWebProbeAgent struct {
	*fakeAgent
	active bool
}

func (a *dshWebProbeAgent) IsSessionActive(context.Context, string) bool { return a.active }

// T-wiring：五处接线点 + 两个不进清单。
func TestDSHWebProjectionWiringPoints(t *testing.T) {
	if !backendSupportsProjectionHydrate("dsh-web") {
		t.Fatal("dsh-web must be a projection hydrate backend (pathless family, design §4.3.2)")
	}
	// 不进 deepseek 的 store-file/live-only 分支：无 store 语义（会话在服务端常驻）。
	h := newDshProjectionHandlers(t)
	agent := &fakeAgent{name: "dsh-web", richHistory: nil}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()
	// dsh-web 不依赖 DSH_HOME store fixture：源准备仅要求注册 agent（与
	// opencode/grokbuild 同构），此处不断言错误即通过路径选择。
	if _, err := h.prepareProjectionHydrateSource(context.Background(), "dsh-web", "s", ""); err != nil {
		t.Fatalf("registered dsh-web agent must prepare a pathless source: %v", err)
	}
	// 不进 backendHasNoExternalEventSource 剪枝（mux 即外部事件源）。
	if backendHasNoExternalEventSource("dsh-web") {
		t.Fatal("dsh-web must NOT be in the no-external-event-source pruning list (mux covers all sessions)")
	}
	// SSV2 能力广告（id/kind 双形态）。
	backends := []AgentProviderDescriptor{
		{ID: "dsh-web", Kind: "dsh-web"},
		{ID: "dsh-web", Kind: "deepseek-web"},
	}
	advertiseSessionSyncV2Backend(backends)
	for i, b := range backends {
		found := false
		for _, c := range b.Capabilities {
			if c == "session_sync_v2" {
				found = true
			}
		}
		if !found {
			t.Fatalf("descriptor[%d] must advertise session_sync_v2: %+v", i, b)
		}
	}
}

// 主路径：注册 dsh-web agent（RichHistoryProvider）→ 冷 pull pathless 全量
// 重建（session.history 基线），kernel Ready（grokbuild/deepseek 同款断言）。
func TestDSHWebProjectionHydrateFromRichHistory(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-hist-1"
	agent := &fakeAgent{
		name: "dsh-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "web 建的会话，iOS 续聊"},
			{
				ID: "a1", Role: "assistant", Content: "好的，接上上下文",
				Thinking: "先读历史",
				Parts: []map[string]any{
					{"type": "reasoning", "content": "先读历史"},
					{"type": "text", "content": "好的，接上上下文"},
				},
			},
		},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("pathless hydrate error: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	for _, want := range []string{"web 建的会话，iOS 续聊", "好的，接上上下文", "先读历史"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("projection missing %q: %s", want, string(raw))
		}
	}
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready", st.Phase)
	}
}

// 死会话尾封口（M1 commit gate）：尾部未答 user turn，SessionActivityProbing
// 确认 idle → 如实封口；探活失败/active → 保持等待（保守）。
func TestDSHWebProjectionTrailingUnansweredSettlesWhenIdle(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-dead-1"
	agent := &dshWebProbeAgent{
		fakeAgent: &fakeAgent{
			name: "dsh-web",
			richHistory: []core.RichHistoryEntry{
				{ID: "u1", Role: "user", Content: "最后一问"}, // 尾部未答：死会话
			},
		},
		active: false,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("dead-session hydrate error: %+v", conn.err)
	}
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("idle trailing turn must settle (phase=%q)", st.Phase)
	}
	raw, _ := json.Marshal(conn.data)
	if !strings.Contains(string(raw), "最后一问") {
		t.Fatalf("sealed trailing turn content missing: %s", string(raw))
	}
}

// 活会话不封口：探活 active → commit gate 等待（不猜完成）。
func TestDSHWebProjectionTrailingUnansweredWaitsWhenActive(t *testing.T) {
	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 200 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-live-1"
	agent := &dshWebProbeAgent{
		fakeAgent: &fakeAgent{
			name: "dsh-web",
			richHistory: []core.RichHistoryEntry{
				{ID: "u1", Role: "user", Content: "正在跑的 turn"},
			},
		},
		active: true,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err == nil {
		// 活跃尾部要么 hydrating（budget 内等待）——绝不能以完成态成功。
		raw, _ := json.Marshal(conn.data)
		if strings.Contains(string(raw), "aborted") || strings.Contains(string(raw), "error") {
			t.Fatalf("active session must not settle as terminal: %s", string(raw))
		}
	}
	// kernel 不得 Ready（活跃尾 turn 无终态可提交）。
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase == ProjectionHydrateReady {
		t.Fatal("active trailing turn must NOT commit as ready")
	}
}

// ── 真机矩阵修复回归（2026-08-16，坑 4 同类）───────────────────────────────

// TestDSHWebColdPullDuringLiveTurnKeepsKernelBaseline：本代内出生的会话
// （live 状态覆盖 t1，身份 dshwTurnID 同源可解析）在 live turn 期间冷拉
// （sinceRev=0）必须走 live-only 快路径，服务 kernel 现有状态，不得 pathless
// 重建——保 rev/身份（2026-08-16 真机：fence 回退 464→10 + live 补丁身份脱节）。
// 断言：拉取后 running turn 与 deltas 保留、headRev 不回退、coldBaseline 不被
// live-only 提交置位。
func TestDSHWebColdPullDuringLiveTurnKeepsKernelBaseline(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-live-race-1"
	// t1 即本会话首个 turn（dshw-<prefix>-t1 可解析 → coverage 命中快路径）。
	t1 := "dshw-liverace-t1"

	// Live turn 进行中：turn_started + 两条 delta，无终端（running）。
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "turn_started", Data: map[string]interface{}{"turnId": t1}})
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "text_delta", Data: map[string]interface{}{"itemId": t1, "delta": "月亮"}})
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "text_delta", Data: map[string]interface{}{"itemId": t1, "delta": "笑话"}})

	prePullRev := h.eventPublisher.ProjectionTurnCount("dsh-web", sessionID)
	if prePullRev == 0 {
		t.Fatal("live events must have committed kernel state")
	}
	preSnap, ok := h.projectionKernel.CommittedSnapshot("dsh-web", sessionID)
	if !ok {
		t.Fatal("precondition: committed kernel state must exist")
	}

	// 注册 live session（registry 命中=live 判据）+ 冷拉（forceColdInspection 路径）。
	fakeSess := &fakeAgentSession{id: sessionID, events: make(chan core.Event)}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": &fakeAgent{name: "dsh-web", richHistory: []core.RichHistoryEntry{
		{ID: "stale-u", Role: "user", Content: "旧内容（重建基线不得采用）"},
	}}}
	h.putSession(sessionID, fakeSess)
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("cold pull during live turn: %+v", conn.err)
	}
	proj := liveOnlyProjectionOf(t, conn)

	// Running turn 必须保留且含已流式内容——不得被落后重建覆盖。
	if proj.Execution.ActiveTurnID != t1 {
		t.Fatalf("active turn lost: execution=%+v", proj.Execution)
	}
	foundText := false
	var runningStatus string
	for _, turn := range proj.Turns {
		if turn.TurnID == t1 {
			runningStatus = turn.Status
			for _, p := range turn.Assistant.Parts {
				if p.Type == "text" {
					foundText = foundText || strings.Contains(p.Text, "月亮")
				}
			}
		}
	}
	if runningStatus != "running" {
		t.Fatalf("in-flight turn must stay running, got %q", runningStatus)
	}
	if !foundText {
		t.Fatalf("live deltas lost after cold pull: %+v", proj.Turns)
	}
	// 无「旧内容」污染：重建基线不得被采用。
	raw, _ := json.Marshal(proj)
	if strings.Contains(string(raw), "旧内容（重建基线不得采用）") {
		t.Fatal("pathless rebuild must NOT replace kernel state during a live turn")
	}
	// headRev 不回退（2026-08-16 fence 464→10 同类防线）。
	if proj.SyncRev < preSnap.SyncRev {
		t.Fatalf("live-only admission must not roll back SyncRev: pre=%d post=%d", preSnap.SyncRev, proj.SyncRev)
	}
	// live-only 提交不得置位 coldBaseline（残缺/完整由覆盖判据另行判定）。
	if h.projectionKernel.HasColdBaseline("dsh-web", sessionID) {
		t.Fatal("live-only admission commit must NOT mark coldBaseline")
	}
}

// TestDSHWebRestartMidTurnColdPullRebuildsHistory：2026-09-06 真机事故回归——
// runtime 在 turn 进行中重启，live mux 只把 kernel 播种出在飞 turn（dshw-…-t15，
// 从未冷基线化）。此时冷拉（forceCold）必须落回 pathless 重建：空 reducer 重放
// 全量 journal 历史 + commit 时 union 回在飞 live turn（含已流式内容、running
// 状态），turn 序保持 journal 真值、SyncRev 不回退。旧矩阵 hasKernel 即 live-only，
// 会把残缺状态提交成权威基线——iOS 打开只剩最后一个回复。
func TestDSHWebRestartMidTurnColdPullRebuildsHistory(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-restart-mid-1"
	t15 := "dshw-restartmid-t15"

	// 重启后 14 秒的 kernel 状态：只有 turn 15 在飞（t1..t14 的历史全不在）。
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "turn_started", Data: map[string]interface{}{"turnId": t15}})
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "text_delta", Data: map[string]interface{}{"itemId": t15, "delta": "月亮"}})
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "text_delta", Data: map[string]interface{}{"itemId": t15, "delta": "笑话"}})

	preSnap, ok := h.projectionKernel.CommittedSnapshot("dsh-web", sessionID)
	if !ok {
		t.Fatal("precondition: live-seeded kernel state must exist")
	}
	if h.projectionKernel.HasColdBaseline("dsh-web", sessionID) {
		t.Fatal("precondition: live-only seeding must not carry coldBaseline")
	}

	// registry live（seat 会话在跑）+ 冷拉：richHistory 返回 t1/t2 两轮已结
	// turn 的历史（session.history cut 语义——在飞 t15 不入冷源）。
	settled := func(n int, user, reply string) []core.RichHistoryEntry {
		id := fmt.Sprintf("dshw-restartmid-t%d", n)
		return []core.RichHistoryEntry{
			{ID: id, Role: "user", Content: user},
			{ID: id, Role: "assistant", Content: reply, Parts: []map[string]any{{"type": "text", "content": reply}}},
		}
	}
	fakeSess := &fakeAgentSession{id: sessionID, events: make(chan core.Event)}
	entries := append(settled(1, "第一个问题", "第一个回答"), settled(2, "第二个问题", "第二个回答")...)
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": &fakeAgent{name: "dsh-web", richHistory: entries}}
	h.putSession(sessionID, fakeSess)
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("restart-mid-turn cold pull: %+v", conn.err)
	}
	proj := liveOnlyProjectionOf(t, conn)

	// 历史回来了，且在飞 turn 保住：journal 序 [t1, t2, t15]。
	wantOrder := []string{"dshw-restartmid-t1", "dshw-restartmid-t2", t15}
	if len(proj.Turns) != len(wantOrder) {
		var got []string
		for _, turn := range proj.Turns {
			got = append(got, turn.TurnID)
		}
		t.Fatalf("turns = %v, want %v（历史丢失=残缺基线被提交；live turn 丢失=union 未生效）", got, wantOrder)
	}
	for i, want := range wantOrder {
		if proj.Turns[i].TurnID != want {
			var got []string
			for _, turn := range proj.Turns {
				got = append(got, turn.TurnID)
			}
			t.Fatalf("turn order broken at %d: got %v, want %v（冷重放必须空起跑，t15 不得跑到历史前面）", i, got, wantOrder)
		}
	}
	// 在飞 turn：running + 已流式内容保留。
	if proj.Execution.ActiveTurnID != t15 {
		t.Fatalf("active turn lost: execution=%+v", proj.Execution)
	}
	last := proj.Turns[len(proj.Turns)-1]
	if last.Status != "running" {
		t.Fatalf("in-flight turn must stay running, got %q", last.Status)
	}
	foundText := false
	for _, p := range last.Assistant.Parts {
		if p.Type == "text" && strings.Contains(p.Text, "月亮") {
			foundText = true
		}
	}
	if !foundText {
		t.Fatalf("pre-admission live deltas lost by rebuild: %+v", last.Assistant)
	}
	// 历史内容确实来自冷源。
	raw, _ := json.Marshal(proj)
	for _, want := range []string{"第一个问题", "第二个回答"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("cold history missing %q: %s", want, string(raw))
		}
	}
	// SyncRev 不回退（fence 防线），且重建提交置位 coldBaseline。
	if proj.SyncRev < preSnap.SyncRev {
		t.Fatalf("rebuild commit rolled back SyncRev: pre=%d post=%d", preSnap.SyncRev, proj.SyncRev)
	}
	if !h.projectionKernel.HasColdBaseline("dsh-web", sessionID) {
		t.Fatal("cold rebuild commit must mark coldBaseline")
	}
}

// 脱活会话 + forceCold 仍走重建（dsh web 增长可见 / 断线窗口补齐），确认
// 修复没有把重建路径整体堵死。
func TestDSHWebDeadSessionForceColdStillRebuilds(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-dead-rebuild-1"
	agent := &fakeAgent{
		name: "dsh-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "已完成的旧 turn"},
			{ID: "a1", Role: "assistant", Content: "回复", Parts: []map[string]any{{"type": "text", "content": "回复"}}},
		},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	// 首次冷拉（无 kernel 状态、无 live）→ pathless 重建。
	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("first-open rebuild: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	if !strings.Contains(string(raw), "已完成的旧 turn") {
		t.Fatalf("first-open rebuild content missing: %s", string(raw))
	}
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready", st.Phase)
	}
}

// StartSession 把会话写入 registry 之后、mux 事件尚未入核时，冷拉必须走
// pathless history 播种，不得对空 kernel 做 live-only admission（真机
// 16:26:16 snapshot turnCount=1，历史被丢掉）。
func TestDSHWebLiveRegistryWithoutKernelSeedsFromHistory(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-live-nokernel-1"
	fakeSess := &fakeAgentSession{id: sessionID, events: make(chan core.Event)}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": &fakeAgent{
		name: "dsh-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "u-hist", Role: "user", Content: "历史用户句"},
			{ID: "a-hist", Role: "assistant", Content: "历史回复", Parts: []map[string]any{{"type": "text", "content": "历史回复"}}},
		},
	}}
	h.putSession(sessionID, fakeSess)
	h.mu.Unlock()

	if h.projectionKernel.HasReducerState("dsh-web", sessionID) {
		t.Fatal("precondition: kernel must be empty")
	}

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("live+!hasKernel pull: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	if !strings.Contains(string(raw), "历史用户句") {
		t.Fatalf("history seed missing from first-admission snapshot: %s", string(raw))
	}
}

// TestDSHWebProjectionHydrateInterleavesGoalRounds：goal 轮冷拉交错回归
// （owner 2026-09-06 01:49 rework ⑧）。官方 journal 里 command/done 恒落在
// turn/end 与下一 turn/start 之间，goal 轮无 user 行。冷基线的 projection.Turns
// 必须保持交错序——每轮 assistant 各自成 turn，命令卡夹在轮次之间；不得把多轮
// 输出折进最后一个 user turn（输出首尾相接成一条消息）、命令卡聚到尾部。
func TestDSHWebProjectionHydrateInterleavesGoalRounds(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-goalround-interleave"
	// 模拟修复后 history.go 的 entry 流（身份=官方 turn 号铸的 dshw id）：
	// user 轮（提问+输出1）→ 命令g1 → goal 轮输出2 → 命令g2 → goal 轮输出3。
	asst := func(id, text string) core.RichHistoryEntry {
		return core.RichHistoryEntry{
			ID: id, Role: "assistant", Content: text,
			Parts: []map[string]any{{"type": "text", "content": text}},
		}
	}
	cmd := func(id, name, line string) core.RichHistoryEntry {
		return core.RichHistoryEntry{
			ID: sessionID + ":cmd:" + id, Role: "system",
			Parts: []map[string]any{{
				"type": "command", "commandId": id, "name": name, "kind": "success", "line": line,
			}},
		}
	}
	agent := &fakeAgent{
		name: "dsh-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "dshw-dshweb-goal-t1", Role: "user", Content: "开始"},
			asst("dshw-dshweb-goal-t1", "输出1"),
			cmd("g1", "goal", "/goal 创作浩克故事1000字左右"),
			asst("dshw-dshweb-goal-t2", "输出2"),
			cmd("g2", "goal", "/goal 创作雷神故事2000字左右"),
			asst("dshw-dshweb-goal-t3", "输出3"),
		},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	proj := liveOnlyProjectionOf(t, conn)

	wantTurns := []string{
		"dshw-dshweb-goal-t1",
		"cmd:g1",
		"dshw-dshweb-goal-t2",
		"cmd:g2",
		"dshw-dshweb-goal-t3",
	}
	if len(proj.Turns) != len(wantTurns) {
		var got []string
		for _, t := range proj.Turns {
			got = append(got, t.TurnID)
		}
		t.Fatalf("turns = %v, want %v", got, wantTurns)
	}
	for i, want := range wantTurns {
		if proj.Turns[i].TurnID != want {
			var got []string
			for _, t := range proj.Turns {
				got = append(got, t.TurnID)
			}
			t.Fatalf("turn order broken at %d: got %v, want %v（命令卡聚簇/输出合并，rework ⑧）", i, got, wantTurns)
		}
	}
	// 每轮输出独立：t1 的 assistant 只含「输出1」，不得吸收后轮文本。
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, p := range turn.Assistant.Parts {
			if p.Type == "text" && p.Text != "" && len(p.Text) > 20 {
				t.Fatalf("turn %s merged multi-round output: %q", turn.TurnID, p.Text)
			}
		}
	}
	// user 行归属 t1（User+Assistant 同 turn），命令卡为 system turn。
	if proj.Turns[0].User == nil || proj.Turns[0].Assistant == nil {
		t.Fatalf("t1 must pair user+assistant: %+v", proj.Turns[0])
	}
	if proj.Turns[1].System == nil || len(proj.Turns[1].System.Parts) != 1 ||
		proj.Turns[1].System.Parts[0].CommandLine != "/goal 创作浩克故事1000字左右" {
		t.Fatalf("cmd:g1 system part: %+v", proj.Turns[1].System)
	}
}
