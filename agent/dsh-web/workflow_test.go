package dshweb

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 2026-09-06 并行子代理 workflow 卡回放测试。
//
// Fixture：testdata/workflow_runs.jsonl —— 座位
// session-3eacd40e（dsh-v0.1.3-alpha.1 = d347e70，官方 web 同源）真实事件
// 导出，做两处不改变语义的整理：事件体逐字保留但 seq 重排为连续（codec
// 连续投递模型；真实 seq 差值区间是 chunk/推理流，与本折叠无关），
// tool/call 与 tool/result 的超长文本字段截断加标记；末尾 turn/start
//（turn 16）为合成边界。三段：
//
//   - turn 12：user/message → step/start(12) → tool/call(workflow) →
//     plan109-subagent-rewrite（4 成员无 phase，乱序 agent-end 全 completed，
//     run-end completed）→ tool/result → step/end → turn/end；
//   - turn 14：goal_round user/message → plan110-subagent-four（seq3/1/4
//     completed、seq2 failed，run-end completed）→ tool/result/step/end →
//     step/start(3) → plan110-wudalang-retry 的 tool/call + run-start +
//     agent-start——journal 就此截断（torn tail：step 未闭合、run 无终态，
//     冷拉走 EOF flush 提交前缀）。
//
// 官方锚点：ui-workflow-run workflowRunDefinition / invariant.ts / WorkflowRunPanel.tsx
//（run-start 建档 {name, members:[]}、agent-start 追加、agent-end 按 seq 结算、
// run-end 结算 stopReason；无 start 不建节点）。2026-09-06 前 codec.apply 没有
// tool-workflow case，每条 workflow 事件都走 default resetf 拆 live 流（P0）。

// loadWorkflowRuns 解析 fixture 为原始 envelope 行（live mux 连续投递模型）。
func loadWorkflowRuns(t *testing.T) []sessionEventWire {
	t.Helper()
	raw, err := os.ReadFile("testdata/workflow_runs.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var frames []sessionEventWire
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var env sessionEventWire
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("unmarshal fixture frame: %v", err)
		}
		frames = append(frames, env)
	}
	if len(frames) == 0 {
		t.Fatal("empty fixture")
	}
	return frames
}

// replayWorkflowFrames feeds the whole fixture through ONE codec (live mux
// subscription from turn start; fixture seqs are contiguous) and returns
// folded events plus the codec-reset count.
func replayWorkflowFrames(t *testing.T) ([]core.Event, int) {
	t.Helper()
	var events []core.Event
	resets := 0
	codec := newSessionCodec("session-3e")
	for _, env := range loadWorkflowRuns(t) {
		e := env
		out, err := codec.apply(&e)
		if err != nil {
			if _, ok := err.(*codecReset); ok {
				resets++
				codec = newSessionCodec("session-3e")
				continue
			}
			t.Fatalf("non-reset error at seq %d: %v", e.Seq, err)
		}
		events = append(events, out...)
	}
	return events, resets
}

// workflowEventsFor filters whole-value snapshots of one run id (chronological).
func workflowEventsFor(events []core.Event, runID string) []*core.WorkflowRunEvent {
	var out []*core.WorkflowRunEvent
	for i := range events {
		e := &events[i]
		if e.Type != core.EventWorkflowRun || e.WorkflowRun == nil || e.WorkflowRun.RunID != runID {
			continue
		}
		out = append(out, e.WorkflowRun)
	}
	return out
}

// TestWorkflowCodecReplayNoReset：真实 workflow 事件全程零 codec reset。
// 修复前每条 tool-workflow 事件都命中 default resetf（P0：子代理运行期间拆流）。
func TestWorkflowCodecReplayNoReset(t *testing.T) {
	_, resets := replayWorkflowFrames(t)
	if resets != 0 {
		t.Fatalf("codec resets = %d, want 0 (tool-workflow events must fold without teardown)", resets)
	}
}

// TestWorkflowCodecReplayPlan109Progression：plan109 run 的整值快照链——
// run-start 建档（running，0 成员）→ 4 次 agent-start 递增 → agent-end 逐个
// completed → run-end completed。全部快照锚定所属 turn。
func TestWorkflowCodecReplayPlan109Progression(t *testing.T) {
	events, _ := replayWorkflowFrames(t)
	const runID = "afc59623-35df-4d1e-85ea-1d9f0d790f7e"
	const turnID = "dshw-session-3e-t12"
	snaps := workflowEventsFor(events, runID)
	// run-start + 4 agent-start + 4 agent-end + run-end = 10 snapshots
	if len(snaps) != 10 {
		t.Fatalf("plan109 snapshots = %d, want 10", len(snaps))
	}
	first := snaps[0]
	if first.Name != "plan109-subagent-rewrite" || first.Status != core.WorkflowStatusRunning ||
		len(first.Phases) != 0 {
		// 官方 projectWorkflow：成员为空 → 无 phase 分组（渲染层「没有启动成员」）。
		t.Fatalf("run-start snapshot = %+v (want running, 0 phases)", first)
	}
	// after 4 agent-starts: 1 nil phase, 4 members, all running, journal order seq 1..4
	mid := snaps[4]
	if len(mid.Phases) != 1 || mid.Phases[0].Phase != nil || len(mid.Phases[0].Members) != 4 {
		t.Fatalf("after 4 agent-starts phases = %+v, want 1 nil-phase with 4 members", mid.Phases)
	}
	wantLabels := []string{"写唐僧篇", "写孙悟空篇", "写猪八戒篇", "写沙僧篇"}
	for i, m := range mid.Phases[0].Members {
		if m.Seq != i+1 || m.Label != wantLabels[i] || m.Status != core.WorkflowStatusRunning {
			t.Fatalf("member[%d] = %+v (label order/status wrong)", i, m)
		}
		if m.ChildSessionID == "" {
			t.Fatalf("member[%d] lost childSessionId", i)
		}
	}
	last := snaps[len(snaps)-1]
	if last.Status != core.WorkflowStatusCompleted {
		t.Fatalf("run-end status = %q, want completed", last.Status)
	}
	for i, m := range last.Phases[0].Members {
		if m.Status != core.WorkflowStatusCompleted {
			t.Fatalf("member[%d] = %+v, want completed (乱序 agent-end 按 seq 结算)", i, m)
		}
	}
	for _, e := range events {
		if e.Type == core.EventWorkflowRun && e.WorkflowRun != nil && e.WorkflowRun.RunID == runID &&
			e.TurnID != turnID {
			t.Fatalf("plan109 snapshot anchored to %q, want %q", e.TurnID, turnID)
		}
	}
}

// TestWorkflowCodecReplayPlan110FailedMemberAndTornRun：seq2 failed 的成员
// 结算 + run-end completed（官方 statusFromOutcome/statusFromStopReason）；
// turn 14 内的 torn run（run-start + agent-start，无 end）快照保持 running
//（interrupted 由 reducer turn 终态注入，不在 codec）。
func TestWorkflowCodecReplayPlan110FailedMemberAndTornRun(t *testing.T) {
	events, _ := replayWorkflowFrames(t)
	const runID = "f041ca5c-3479-4709-abc5-f3d417351233"
	snaps := workflowEventsFor(events, runID)
	if len(snaps) != 10 { // run-start + 4 start + 4 end + run-end
		t.Fatalf("plan110 snapshots = %d, want 10", len(snaps))
	}
	last := snaps[len(snaps)-1]
	if last.Status != core.WorkflowStatusCompleted {
		t.Fatalf("plan110 run status = %q, want completed", last.Status)
	}
	bySeq := map[int]string{}
	for _, m := range last.Phases[0].Members {
		bySeq[m.Seq] = m.Status
	}
	if bySeq[2] != core.WorkflowStatusFailed {
		t.Fatalf("plan110 seq2 = %q, want failed", bySeq[2])
	}
	for _, s := range []int{1, 3, 4} {
		if bySeq[s] != core.WorkflowStatusCompleted {
			t.Fatalf("plan110 seq%d = %q, want completed", s, bySeq[s])
		}
	}

	const tornID = "1ea8d606-ea71-4d37-9254-7f958234bbcf"
	torn := workflowEventsFor(events, tornID)
	if len(torn) != 2 { // run-start + agent-start only
		t.Fatalf("torn run snapshots = %d, want 2", len(torn))
	}
	lastTorn := torn[len(torn)-1]
	if lastTorn.Status != core.WorkflowStatusRunning || len(lastTorn.Phases[0].Members) != 1 ||
		lastTorn.Phases[0].Members[0].Status != core.WorkflowStatusRunning {
		t.Fatalf("torn run snapshot = %+v, want run+member running (interrupted is reducer-injected)", lastTorn)
	}
}

// TestWorkflowCodecHoldsWhenNoTurn：turn 未开时折叠继续但不发事件（官方
// 「无 run-start 不建节点」同族：无锚定 turn 的卡不上时间线），也不 reset。
// 过滤掉非 workflow 事件后重排 seq 保持连续（codec 连续投递模型）。
func TestWorkflowCodecHoldsWhenNoTurn(t *testing.T) {
	var envs []sessionEventWire
	for _, e := range loadWorkflowRuns(t) {
		if e.Type != "tool-workflow/run-start" && e.Type != "tool-workflow/agent-start" &&
			e.Type != "tool-workflow/agent-end" && e.Type != "tool-workflow/run-end" {
			continue
		}
		e.Seq = int64(len(envs) + 1)
		envs = append(envs, e)
	}
	if len(envs) == 0 {
		t.Fatal("fixture yielded no workflow events")
	}
	codec := newSessionCodec("session-3e")
	events, err := collectAllowReset(t, codec, envs)
	if err != nil {
		t.Fatalf("workflow events without a turn must fold (no reset): %v", err)
	}
	for _, e := range events {
		if e.Type == core.EventWorkflowRun {
			t.Fatalf("workflow snapshot emitted without an active turn: %+v", e)
		}
	}
}

// collectAllowReset drains envelopes through one codec, failing on resets too.
func collectAllowReset(t *testing.T, codec *sessionCodec, envs []sessionEventWire) ([]core.Event, error) {
	t.Helper()
	var out []core.Event
	for i := range envs {
		events, err := codec.apply(&envs[i])
		if err != nil {
			return out, err
		}
		out = append(out, events...)
	}
	return out, nil
}

// TestWorkflowFoldInvariants：官方 invariant.ts 逐项——重复 run、无 run 的
// agent-start、重复 seq、未知 outcome、非法 stopReason、空 runId/name。
func TestWorkflowFoldInvariants(t *testing.T) {
	mk := func(typ string, seq int64, data any) []byte {
		raw, _ := json.Marshal(data)
		_ = seq
		return raw
	}
	runStart := map[string]any{"runId": "r1", "name": "demo"}
	agentStart := map[string]any{"runId": "r1", "seq": 1, "label": "a", "childId": "c1"}

	fold := &workflowFold{}
	if err := foldWorkflowJournalEvent(fold, "tool-workflow/run-start", mk("", 1, runStart)); err != nil {
		t.Fatalf("run-start: %v", err)
	}
	if err := foldWorkflowJournalEvent(fold, "tool-workflow/run-start", mk("", 2, runStart)); err == nil {
		t.Fatal("repeat run-start must violate")
	}
	fold2 := &workflowFold{}
	if err := foldWorkflowJournalEvent(fold2, "tool-workflow/agent-start", mk("", 1, agentStart)); err == nil {
		t.Fatal("agent-start without run must violate")
	}
	fold3 := &workflowFold{}
	_ = foldWorkflowJournalEvent(fold3, "tool-workflow/run-start", mk("", 1, runStart))
	if err := foldWorkflowJournalEvent(fold3, "tool-workflow/agent-start", mk("", 2, agentStart)); err != nil {
		t.Fatalf("first agent-start: %v", err)
	}
	if err := foldWorkflowJournalEvent(fold3, "tool-workflow/agent-start", mk("", 3, agentStart)); err == nil {
		t.Fatal("repeat seq must violate")
	}
	if err := foldWorkflowJournalEvent(fold3, "tool-workflow/agent-end", mk("", 4, map[string]any{"runId": "r1", "seq": 1, "outcome": "exploded"})); err == nil {
		t.Fatal("unknown outcome must violate")
	}
	if err := foldWorkflowJournalEvent(fold3, "tool-workflow/agent-end", mk("", 5, map[string]any{"runId": "r1", "seq": 1, "outcome": "completed"})); err != nil {
		t.Fatalf("agent-end: %v", err)
	}
	if err := foldWorkflowJournalEvent(fold3, "tool-workflow/run-end", mk("", 6, map[string]any{"runId": "r1", "stopReason": "paused"})); err == nil {
		t.Fatal("unknown stopReason must violate")
	}
	if err := foldWorkflowJournalEvent(fold3, "tool-workflow/run-end", mk("", 7, map[string]any{"runId": "r1", "stopReason": "error"})); err != nil {
		t.Fatalf("run-end error: %v", err)
	}
	snap, ok := fold3.snapshot("r1")
	if !ok || snap.Status != core.WorkflowStatusFailed {
		t.Fatalf("stopReason error must map to failed: %+v", snap)
	}
	fold4 := &workflowFold{}
	if err := foldWorkflowJournalEvent(fold4, "tool-workflow/run-start", mk("", 8, map[string]any{"runId": "", "name": "x"})); err == nil {
		t.Fatal("empty runId must violate")
	}
	if err := foldWorkflowJournalEvent(fold4, "tool-workflow/run-start", mk("", 9, map[string]any{"runId": "r9", "name": ""})); err == nil {
		t.Fatal("empty name must violate")
	}
	// 非 tool-workflow 事件不是折叠输入（caller 只对四个 workflow 事件类型分派）。
	if err := foldWorkflowJournalEvent(&workflowFold{}, "user/message", mk("", 10, map[string]any{})); err == nil {
		t.Fatal("non-workflow event must be rejected by the fold dispatcher")
	}
}

// TestWorkflowFoldPhaseIdentity：phase 身份——absent（未分阶段，nil）与空串
//（空阶段名）是两个独立分组，按首现顺序；同 phase 归并同组。
func TestWorkflowFoldPhaseIdentity(t *testing.T) {
	raw := func(d map[string]any) []byte { b, _ := json.Marshal(d); return b }
	fold := &workflowFold{}
	steps := []struct {
		typ  string
		data map[string]any
	}{
		{"tool-workflow/run-start", map[string]any{"runId": "rp", "name": "phased"}},
		{"tool-workflow/agent-start", map[string]any{"runId": "rp", "seq": 1, "label": "a", "childId": "ca"}},
		{"tool-workflow/agent-start", map[string]any{"runId": "rp", "seq": 2, "label": "b", "phase": "", "childId": "cb"}},
		{"tool-workflow/agent-start", map[string]any{"runId": "rp", "seq": 3, "label": "c", "phase": "write", "childId": "cc"}},
		{"tool-workflow/agent-start", map[string]any{"runId": "rp", "seq": 4, "label": "d", "phase": "write", "childId": "cd"}},
		{"tool-workflow/agent-start", map[string]any{"runId": "rp", "seq": 5, "label": "e", "phase": "", "childId": "ce"}},
	}
	for _, s := range steps {
		if err := foldWorkflowJournalEvent(fold, s.typ, raw(s.data)); err != nil {
			t.Fatalf("%s: %v", s.typ, err)
		}
	}
	snap, ok := fold.snapshot("rp")
	if !ok {
		t.Fatal("missing snapshot")
	}
	if len(snap.Phases) != 3 {
		t.Fatalf("phases = %d, want 3 (nil, \"\", \"write\" first-appearance)", len(snap.Phases))
	}
	if snap.Phases[0].Phase != nil || len(snap.Phases[0].Members) != 1 ||
		snap.Phases[0].Members[0].Label != "a" {
		t.Fatalf("phase[0] = %+v, want nil-phase member a", snap.Phases[0])
	}
	if snap.Phases[1].Phase == nil || *snap.Phases[1].Phase != "" || len(snap.Phases[1].Members) != 2 {
		t.Fatalf("phase[1] = %+v, want empty-string phase with members b,e", snap.Phases[1])
	}
	if snap.Phases[2].Phase == nil || *snap.Phases[2].Phase != "write" || len(snap.Phases[2].Members) != 2 {
		t.Fatalf("phase[2] = %+v, want phase write with members c,d", snap.Phases[2])
	}
}

// TestWorkflowHistoryColdFoldPlacement：冷拉折叠——workflow part 锚定
// run-start 的 journal 原位（官方 keyed chat 节点），落在所属 assistant turn
// 的 parts；plan110 turn 含两个 run 的 part（seq2 failed + torn running）；
// turn 外 run 整跳过。goal_round user 行非回归。
func TestWorkflowHistoryColdFoldPlacement(t *testing.T) {
	var evs []apiHistoryEntry
	for _, env := range loadWorkflowRuns(t) {
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal env: %v", err)
		}
		var e apiHistoryEntry
		wrapped := []byte(`{"event":` + string(b) + `}`)
		if err := json.Unmarshal(wrapped, &e); err != nil {
			t.Fatalf("unmarshal entry: %v", err)
		}
		evs = append(evs, e)
	}
	entries := mapHistoryEvents("session-3e", evs)

	workflowParts := func(entryID string) []map[string]any {
		for _, entry := range entries {
			// user 行与 assistant turn 共用 dshw turn id（live applyUserMessage
			// 同式）；workflow part 只在 assistant 行上。
			if entry.ID != entryID || entry.Role != "assistant" {
				continue
			}
			var parts []map[string]any
			for _, p := range entry.Parts {
				if p["type"] == "workflow" {
					parts = append(parts, p)
				}
			}
			return parts
		}
		return nil
	}

	t12 := workflowParts("dshw-session-3e-t12")
	if len(t12) != 1 {
		t.Fatalf("turn 12 workflow parts = %d, want 1", len(t12))
	}
	p := t12[0]
	if p["workflowId"] != "afc59623-35df-4d1e-85ea-1d9f0d790f7e" ||
		p["workflowName"] != "plan109-subagent-rewrite" ||
		p["workflowStatus"] != core.WorkflowStatusCompleted {
		t.Fatalf("turn 12 part = %+v", p)
	}
	phases, ok := p["workflowPhases"].([]map[string]any)
	if !ok || len(phases) != 1 {
		t.Fatalf("turn 12 phases malformed: %+v", p["workflowPhases"])
	}
	if phases[0]["phase"] != nil {
		t.Fatalf("phase must be nil (未分阶段), got %v", phases[0]["phase"])
	}
	members, ok := phases[0]["members"].([]map[string]any)
	if !ok || len(members) != 4 {
		t.Fatalf("turn 12 members malformed: %+v", phases[0]["members"])
	}
	if members[1]["label"] != "写孙悟空篇" || members[1]["status"] != core.WorkflowStatusCompleted ||
		members[1]["childSessionId"] != "6fcf49ea-2b91-4902-9302-1d658397d96f" {
		t.Fatalf("members[1] = %+v", members[1])
	}

	t14 := workflowParts("dshw-session-3e-t14")
	if len(t14) != 2 {
		t.Fatalf("turn 14 workflow parts = %d, want 2 (plan110 + torn retry)", len(t14))
	}
	failedSeen, tornSeen := false, false
	for _, p := range t14 {
		switch p["workflowId"] {
		case "f041ca5c-3479-4709-abc5-f3d417351233":
			ph, _ := p["workflowPhases"].([]map[string]any)
			ms, _ := ph[0]["members"].([]map[string]any)
			for _, m := range ms {
				if m["seq"] == 2 && m["status"] == core.WorkflowStatusFailed {
					failedSeen = true
				}
			}
			if p["workflowStatus"] != core.WorkflowStatusCompleted {
				t.Fatalf("plan110 part status = %v", p["workflowStatus"])
			}
		case "1ea8d606-ea71-4d37-9254-7f958234bbcf":
			if p["workflowStatus"] == core.WorkflowStatusRunning {
				tornSeen = true
			}
		}
	}
	if !failedSeen || !tornSeen {
		t.Fatalf("plan110 turn parts: failedSeen=%v tornSeen=%v", failedSeen, tornSeen)
	}

	// goal_round 注入行（source.kind "goal"）非用户气泡（官方语义），非回归
	// 点在于 turn 14 结构完好：两个 workflow part 已在上文断言。
}
