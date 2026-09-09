package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dsh-web parallel-subagent workflow card reducer tests (official
// WorkflowRunPanel parity; see bridge-v1.md 「Part vocabulary: workflow」).
// Wire samples mirror agent/dsh-web/testdata/workflow_runs.jsonl (real seat
// session-3eacd40e journal, d347e70).

func workflowWireEvent(turnID, runID, name, status string, phases []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"turnId":         turnID,
		"workflowId":     runID,
		"workflowName":   name,
		"workflowStatus": status,
		"workflowPhases": phases,
	}
}

func workflowPhaseWire(phase interface{}, members ...map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"phase": phase, "members": members}
}

// TestReducerWorkflowRunUpsertsInPlace：live 折叠——run-start（running，成员
// 空）→ agent-start 追加 → run-end 结算；同一 workflowId 原地 upsert（始终
// 一个 workflow part），patch 以 upsert_workflow op 携带。
func TestReducerWorkflowRunUpsertsInPlace(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "dshw-s1-t12"}))
	r.Apply(ev(2, "dsh-web", "s1", "text_delta", map[string]interface{}{"turnId": "dshw-s1-t12", "delta": "开始重写"}))

	// run-start：0 成员（官方 projectWorkflow 无成员 → 无 phase 分组）。
	r.Apply(ev(3, "dsh-web", "s1", "workflow_run", workflowWireEvent(
		"dshw-s1-t12", "afc59623", "plan109-subagent-rewrite", "running", nil)))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok || len(proj.Turns) != 1 {
		t.Fatalf("projection = %+v", proj)
	}
	wf := findWorkflowPartForTest(t, proj.Turns[0])
	if wf.WorkflowID != "afc59623" || wf.WorkflowStatus != "running" || len(wf.WorkflowPhases) != 0 {
		t.Fatalf("run-start part: %+v", wf)
	}

	// 4 agent-start 后的整值快照（真实 plan109 标签；phase null = 未分阶段）。
	r.Apply(ev(4, "dsh-web", "s1", "workflow_run", workflowWireEvent(
		"dshw-s1-t12", "afc59623", "plan109-subagent-rewrite", "running",
		[]interface{}{
			workflowPhaseWire(nil,
				map[string]interface{}{"seq": 1, "label": "写唐僧篇", "childSessionId": "f783ddb5", "status": "running"},
				map[string]interface{}{"seq": 2, "label": "写孙悟空篇", "childSessionId": "6fcf49ea", "status": "running"},
				map[string]interface{}{"seq": 3, "label": "写猪八戒篇", "childSessionId": "37d363b5", "status": "running"},
				map[string]interface{}{"seq": 4, "label": "写沙僧篇", "childSessionId": "a57c3c65", "status": "running"}),
		})))
	proj, _ = r.Snapshot("dsh-web", "s1")
	wf = findWorkflowPartForTest(t, proj.Turns[0])
	if len(wf.WorkflowPhases) != 1 || wf.WorkflowPhases[0].Phase != nil || len(wf.WorkflowPhases[0].Members) != 4 {
		t.Fatalf("agent-start part: %+v", wf)
	}
	if wf.WorkflowPhases[0].Members[1].ChildSessionID != "6fcf49ea" {
		t.Fatalf("member childId lost: %+v", wf.WorkflowPhases[0].Members[1])
	}

	// run-end：completed 结算，成员全 completed；part 数仍为 1（原地替换）。
	r.Apply(ev(5, "dsh-web", "s1", "workflow_run", workflowWireEvent(
		"dshw-s1-t12", "afc59623", "plan109-subagent-rewrite", "completed",
		[]interface{}{
			workflowPhaseWire(nil,
				map[string]interface{}{"seq": 1, "label": "写唐僧篇", "childSessionId": "f783ddb5", "status": "completed"},
				map[string]interface{}{"seq": 2, "label": "写孙悟空篇", "childSessionId": "6fcf49ea", "status": "completed"},
				map[string]interface{}{"seq": 3, "label": "写猪八戒篇", "childSessionId": "37d363b5", "status": "completed"},
				map[string]interface{}{"seq": 4, "label": "写沙僧篇", "childSessionId": "a57c3c65", "status": "completed"}),
		})))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if n := countWorkflowPartsForTest(proj.Turns[0]); n != 1 {
		t.Fatalf("workflow parts = %d, want 1 (in-place upsert)", n)
	}
	wf = findWorkflowPartForTest(t, proj.Turns[0])
	if wf.WorkflowStatus != "completed" || wf.WorkflowPhases[0].Members[3].Status != "completed" {
		t.Fatalf("run-end part: %+v", wf)
	}

	// patch 面：upsert_workflow op 锚定所属 turn 的 assistant message。
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok {
		t.Fatal("expected patch after workflow fold")
	}
	var sawOp *PartOp
	for i := range patch.PartOps {
		if patch.PartOps[i].Op == "upsert_workflow" {
			sawOp = &patch.PartOps[i]
		}
	}
	if sawOp == nil {
		t.Fatalf("patch lacks upsert_workflow: %+v", patch.PartOps)
	}
	if sawOp.TurnID != "dshw-s1-t12" || sawOp.MessageID != "dshw-s1-t12" ||
		sawOp.Part == nil || sawOp.Part.WorkflowID != "afc59623" || sawOp.Part.WorkflowStatus != "completed" {
		t.Fatalf("upsert_workflow op: %+v", sawOp)
	}
}

// TestReducerWorkflowTurnTerminalFixup：turn 终态（turn_completed / turn_aborted
// / turn_error）时仍 running 的 workflow part → interrupted（官方
// locationClosed；成员 running → interrupted）；已完成 part 不受影响；幂等
//（重复终态帧不再 churn）。
func TestReducerWorkflowTurnTerminalFixup(t *testing.T) {
	build := func() *ProjectionReducer {
		r := newTestReducer()
		r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "t-run"}))
		r.Apply(ev(2, "dsh-web", "s1", "workflow_run", workflowWireEvent(
			"t-run", "r-open", "still-running", "running",
			[]interface{}{workflowPhaseWire(nil,
				map[string]interface{}{"seq": 1, "label": "写武大郎篇", "childSessionId": "5232c28d", "status": "running"},
				map[string]interface{}{"seq": 2, "label": "done-early", "childSessionId": "9x", "status": "completed"})})))
		r.Apply(ev(3, "dsh-web", "s1", "workflow_run", workflowWireEvent(
			"t-run", "r-closed", "already-done", "completed",
			[]interface{}{workflowPhaseWire(nil,
				map[string]interface{}{"seq": 1, "label": "a", "childSessionId": "c1", "status": "completed"})})))
		return r
	}

	for _, terminal := range []string{"turn_completed", "turn_aborted", "turn_error"} {
		r := build()
		switch terminal {
		case "turn_completed":
			r.Apply(ev(4, "dsh-web", "s1", terminal, map[string]interface{}{"turnId": "t-run"}))
		case "turn_aborted":
			r.Apply(ev(4, "dsh-web", "s1", terminal, map[string]interface{}{"turnId": "t-run"}))
		case "turn_error":
			r.Apply(ev(4, "dsh-web", "s1", terminal, map[string]interface{}{"turnId": "t-run", "message": "x"}))
		}
		proj, _ := r.Snapshot("dsh-web", "s1")
		var open, closed *ProjectionPart
		for i := range proj.Turns[0].Assistant.Parts {
			p := &proj.Turns[0].Assistant.Parts[i]
			if p.Type != "workflow" {
				continue
			}
			if p.WorkflowID == "r-open" {
				open = p
			}
			if p.WorkflowID == "r-closed" {
				closed = p
			}
		}
		if open == nil || closed == nil {
			t.Fatalf("%s: parts missing open=%v closed=%v", terminal, open, closed)
		}
		if open.WorkflowStatus != "interrupted" {
			t.Fatalf("%s: open run status = %q, want interrupted", terminal, open.WorkflowStatus)
		}
		m := open.WorkflowPhases[0].Members
		if m[0].Status != "interrupted" || m[1].Status != "completed" {
			t.Fatalf("%s: member statuses = %+v (running→interrupted, settled unchanged)", terminal, m)
		}
		if closed.WorkflowStatus != "completed" {
			t.Fatalf("%s: settled run must stay completed: %+v", terminal, closed)
		}
	}

	// 幂等：终态后重复终态帧不再翻动 part（interrupted 稳定、part 不重复）。
	//（重复 turn_completed 自身会 bump SyncRev——reducer 既有行为，与 fixup 无关，
	// 故断言 part 面而非 rev。）
	workflowPartByID := func(tu TurnProjection, id string) *ProjectionPart {
		for i := range tu.Assistant.Parts {
			if tu.Assistant.Parts[i].Type == "workflow" && tu.Assistant.Parts[i].WorkflowID == id {
				return &tu.Assistant.Parts[i]
			}
		}
		return nil
	}
	r := build()
	r.Apply(ev(4, "dsh-web", "s1", "turn_completed", map[string]interface{}{"turnId": "t-run"}))
	proj, _ := r.Snapshot("dsh-web", "s1")
	first := *workflowPartByID(proj.Turns[0], "r-open")
	r.Apply(ev(5, "dsh-web", "s1", "turn_completed", map[string]interface{}{"turnId": "t-run"}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if n := countWorkflowPartsForTest(proj.Turns[0]); n != 2 {
		t.Fatalf("workflow parts = %d, want 2 (r-open + r-closed unchanged)", n)
	}
	second := *workflowPartByID(proj.Turns[0], "r-open")
	if second.WorkflowStatus != first.WorkflowStatus || second.WorkflowStatus != "interrupted" {
		t.Fatalf("repeated terminal must not un-fix: %+v → %+v", first, second)
	}
	if second.WorkflowPhases[0].Members[0].Status != "interrupted" {
		t.Fatalf("repeated terminal must not un-fix members: %+v", second.WorkflowPhases[0].Members)
	}
}

// TestReducerWorkflowFailClosed：所属 turn 不存在 → 不 commit 不 patch（不
// 造幽灵 turn）；缺 workflowId 同样丢弃。
func TestReducerWorkflowFailClosed(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "workflow_run", workflowWireEvent(
		"ghost-turn", "r1", "orphan", "running",
		[]interface{}{workflowPhaseWire(nil,
			map[string]interface{}{"seq": 1, "label": "a", "childSessionId": "c1", "status": "running"})})))
	r.Apply(ev(2, "dsh-web", "s1", "workflow_run", map[string]interface{}{
		"turnId": "ghost-turn", "workflowName": "no-id", "workflowStatus": "running"}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if ok && len(proj.Turns) > 0 {
		t.Fatalf("orphan workflow events must not create turns: %+v", proj.Turns)
	}
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("orphan workflow events must not patch: %+v", patch)
	}
}

// TestReducerWorkflowRestoreContinuesIdentity：Restore 从基线 parts 重建
// pending workflows 身份——重启后同 run 的新快照仍原地 upsert（不重复 part），
// turn 终态 fixup 仍可注入 interrupted。
func TestReducerWorkflowRestoreContinuesIdentity(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "t9"}))
	r.Apply(ev(2, "dsh-web", "s1", "workflow_run", workflowWireEvent(
		"t9", "rr", "retry", "running",
		[]interface{}{workflowPhaseWire(nil,
			map[string]interface{}{"seq": 1, "label": "写武大郎篇", "childSessionId": "5232c28d", "status": "running"})})))
	proj, _ := r.Snapshot("dsh-web", "s1")
	if _, ok := r.FlushPatch("dsh-web", "s1"); !ok {
		t.Fatal("baseline patch expected")
	}

	// Restore（跨重启基线）后：同 run 新快照 → 原地替换。
	r2 := newTestReducer()
	r2.Restore("dsh-web", "s1", proj)
	r2.Apply(ev(3, "dsh-web", "s1", "workflow_run", workflowWireEvent(
		"t9", "rr", "retry", "completed",
		[]interface{}{workflowPhaseWire(nil,
			map[string]interface{}{"seq": 1, "label": "写武大郎篇", "childSessionId": "5232c28d", "status": "completed"})})))
	proj2, _ := r2.Snapshot("dsh-web", "s1")
	if len(proj2.Turns) != 1 || countWorkflowPartsForTest(proj2.Turns[0]) != 1 {
		t.Fatalf("restored fold must upsert in place: %+v", proj2.Turns)
	}
	if wf := findWorkflowPartForTest(t, proj2.Turns[0]); wf.WorkflowStatus != "completed" {
		t.Fatalf("restored part: %+v", wf)
	}

	// 重启后仍有 running run → turn 终态 fixup 继续注入 interrupted。
	r3 := newTestReducer()
	r3.Restore("dsh-web", "s1", proj) // proj 仍带 running rr
	r3.Apply(ev(3, "dsh-web", "s1", "turn_completed", map[string]interface{}{"turnId": "t9"}))
	proj3, _ := r3.Snapshot("dsh-web", "s1")
	if wf := findWorkflowPartForTest(t, proj3.Turns[0]); wf.WorkflowStatus != "interrupted" {
		t.Fatalf("post-restore fixup: %+v", wf)
	}
}

// findWorkflowPartForTest returns the single workflow part of a turn's
// assistant message (fail if absent).
func findWorkflowPartForTest(t *testing.T, tu TurnProjection) *ProjectionPart {
	t.Helper()
	var found *ProjectionPart
	for i := range tu.Assistant.Parts {
		if tu.Assistant.Parts[i].Type == "workflow" {
			if found != nil {
				t.Fatalf("multiple workflow parts: %+v", tu.Assistant.Parts)
			}
			found = &tu.Assistant.Parts[i]
		}
	}
	if found == nil {
		t.Fatalf("no workflow part on turn %s: %+v", tu.TurnID, tu.Assistant.Parts)
	}
	return found
}

func countWorkflowPartsForTest(tu TurnProjection) int {
	n := 0
	for _, p := range tu.Assistant.Parts {
		if p.Type == "workflow" {
			n++
		}
	}
	return n
}

// TestHydrateWorkflowEventsFromPart：冷拉 part map（history.go 折叠键与
// ProjectionPart JSON 标签一致）→ 一条整值 workflow_run hydrate 事件，wire
// 形状与 live（events.go EventWorkflowRun）一致；缺 workflowId/phases
// fail-closed 返回 nil。
func TestHydrateWorkflowEventsFromPart(t *testing.T) {
	part := map[string]interface{}{
		"type":           "workflow",
		"workflowId":     "afc59623",
		"workflowName":   "plan109-subagent-rewrite",
		"workflowStatus": "completed",
		"workflowPhases": []interface{}{
			map[string]interface{}{
				"phase": nil,
				"members": []interface{}{
					map[string]interface{}{"seq": 1, "label": "写唐僧篇", "childSessionId": "f783ddb5", "status": "completed"},
				},
			},
		},
	}
	events := hydrateWorkflowEventsFromPart(part, "dshw-s1-t12")
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Event != "workflow_run" {
		t.Fatalf("event = %q", events[0].Event)
	}
	data := events[0].Data
	for _, k := range []string{"turnId", "workflowId", "workflowName", "workflowStatus", "workflowPhases"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("data missing %s: %+v", k, data)
		}
	}
	if data["turnId"] != "dshw-s1-t12" || data["workflowId"] != "afc59623" ||
		data["workflowStatus"] != "completed" {
		t.Fatalf("data = %+v", data)
	}
	// 冷热同形：hydrate 事件喂 reducer 与 live 事件等价（同一 case）。
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "dshw-s1-t12"}))
	r.Apply(ev(2, "dsh-web", "s1", "workflow_run", data))
	proj, _ := r.Snapshot("dsh-web", "s1")
	wf := findWorkflowPartForTest(t, proj.Turns[0])
	if wf.WorkflowID != "afc59623" || wf.WorkflowStatus != "completed" ||
		len(wf.WorkflowPhases) != 1 || wf.WorkflowPhases[0].Phase != nil ||
		len(wf.WorkflowPhases[0].Members) != 1 || wf.WorkflowPhases[0].Members[0].Label != "写唐僧篇" {
		t.Fatalf("hydrated part = %+v", wf)
	}

	// fail-closed：缺 workflowId / 缺 phases → nil。
	if hydrateWorkflowEventsFromPart(map[string]interface{}{"type": "workflow", "workflowStatus": "running"}, "t") != nil {
		t.Fatal("missing workflowId must yield no events")
	}
	if hydrateWorkflowEventsFromPart(map[string]interface{}{"type": "workflow", "workflowId": "x"}, "t") != nil {
		t.Fatal("missing phases must yield no events")
	}
	// 空 turnId 同样拒绝（无锚定）。
	if hydrateWorkflowEventsFromPart(part, "") != nil {
		t.Fatal("empty turnId must yield no events")
	}
}

// TestColdHydrateDefersWorkflowUntilTurnExists：冷拉 rich history 里 workflow
// part 被 decorate 前置（grok goal 历史，渲染顺序需要卡在正文前）时，
// hydrate 必须把 workflow_run 事件延迟到本 entry 内容事件之后——reducer 对
// workflow_run fail-closed（owning turn must already exist），事件先于 turn
// 创建会被静默丢弃（2026-09-09 实测：9 卡全灭，iPhone 上老 goal 只剩工具行
// 「任务已完成」）。延迟后卡与正文共存于同一 turn。
func TestColdHydrateDefersWorkflowUntilTurnExists(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{
			ID:   "cmd-1",
			Role: "system",
			Parts: []map[string]any{{
				"type": "command", "commandId": "cmd-1", "name": "goal",
				"kind": "success", "args": "写四个故事", "line": "/goal 写四个故事",
			}},
		},
		{
			ID:   "a-1",
			Role: "assistant",
			Parts: []map[string]any{
				// 卡 part 前置 = decorateGrokGoalHistory 的真实产物形状。
				{
					"type": "workflow", "workflowId": "run-1", "workflowName": "写四个故事",
					"workflowStatus": "completed",
					"workflowPhases": []any{
						map[string]any{"phase": nil, "members": []any{
							map[string]any{"seq": 1, "label": "写唐僧篇", "childSessionId": "c1", "status": "completed"},
						}},
					},
				},
				{"type": "text", "content": "四个故事已完成"},
			},
		},
	}
	r := newTestReducer()
	seq := 0
	if err := streamRichHistoryProjectionEntries(t.Context(), entries, true, func(ev projectionHydrateEvent) bool {
		seq++
		r.Apply(projectionReducerEvent("grokbuild", "s1", ev.Event, ev.Data, seq, ""))
		return true
	}); err != nil {
		t.Fatal(err)
	}
	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	for i := range proj.Turns {
		turn := &proj.Turns[i]
		if turn.Assistant == nil {
			continue
		}
		var wf *ProjectionPart
		for pi := range turn.Assistant.Parts {
			if turn.Assistant.Parts[pi].Type == "workflow" {
				wf = &turn.Assistant.Parts[pi]
				break
			}
		}
		if wf == nil {
			continue
		}
		if wf.WorkflowID != "run-1" || wf.WorkflowStatus != "completed" ||
			len(wf.WorkflowPhases) != 1 || len(wf.WorkflowPhases[0].Members) != 1 ||
			wf.WorkflowPhases[0].Members[0].Label != "写唐僧篇" {
			t.Fatalf("hydrated workflow part = %+v", wf)
		}
		return
	}
	t.Fatalf("workflow part missing from cold hydration: turns=%+v", proj.Turns)
}
