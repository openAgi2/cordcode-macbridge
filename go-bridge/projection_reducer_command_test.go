package gobridge

import "testing"

// dsh-web host slash-command timeline rows + plan-mode snapshot reducer tests
// (official GenericCommandCard / plan projection parity; see bridge-v1.md
// 「Session command」and「Plan mode」).

// TestReducerSessionCommandFoldsRunningThenSettle：command/run → running 行
// （cmd:<commandId> 完成态 system turn + command part），done → 整体替换为
// settle 行（name/text 续接），execution 不被命令行武装。
func TestReducerSessionCommandFoldsRunningThenSettle(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "session_command", map[string]interface{}{
		"commandId": "c1", "name": "compact", "kind": "running",
	}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection after run")
	}
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "cmd:c1" {
		t.Fatalf("turns = %+v, want single cmd:c1", proj.Turns)
	}
	tu := proj.Turns[0]
	if tu.Status != "completed" || tu.System == nil || len(tu.System.Parts) != 1 {
		t.Fatalf("command turn shell: %+v", tu)
	}
	if tu.System.Role != "system" || tu.System.ID != "cmd:c1" {
		t.Fatalf("system message identity: %+v", tu.System)
	}
	part := tu.System.Parts[0]
	if part.Type != "command" || part.CommandID != "c1" || part.CommandName != "compact" || part.CommandKind != "running" {
		t.Fatalf("running part: %+v", part)
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("command rows must not arm execution: %+v", proj.Execution)
	}

	// settle：done 事件整体替换 System（wholesale merge），kind=success + text。
	r.Apply(ev(2, "dsh-web", "s1", "session_command", map[string]interface{}{
		"commandId": "c1", "name": "compact", "kind": "success",
		"text": "Compacted 20 history items (~11695 tokens).",
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "cmd:c1" {
		t.Fatalf("settle must fold in place, turns = %+v", proj.Turns)
	}
	part = proj.Turns[0].System.Parts[0]
	if part.CommandKind != "success" || part.CommandName != "compact" ||
		part.CommandText != "Compacted 20 history items (~11695 tokens)." {
		t.Fatalf("settle part: %+v", part)
	}

	// patch 面：upsertTurns 携带 cmd:c1 行。
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok {
		t.Fatal("expected patch after command rows")
	}
	if len(patch.UpsertTurns) != 1 || patch.UpsertTurns[0].TurnID != "cmd:c1" {
		t.Fatalf("patch upsertTurns = %+v", patch.UpsertTurns)
	}
	if patch.UpsertTurns[0].System.Parts[0].CommandKind != "success" {
		t.Fatalf("patch carries the settled row: %+v", patch.UpsertTurns[0])
	}
}

// TestReducerSessionCommandDoneOnlyKeepsEmptyName：未见 run 的 done（官方
// done-only CommandNode.name=null）→ 行 kind=success、name 空，客户端回落
// locale 标签。
func TestReducerSessionCommandDoneOnlyKeepsEmptyName(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "session_command", map[string]interface{}{
		"commandId": "d1", "kind": "error", "text": "Compaction could not produce a useful summary. The conversation is unchanged; the attempt is recorded in the session log.",
	}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok || len(proj.Turns) != 1 {
		t.Fatalf("done-only projection: %+v", proj)
	}
	part := proj.Turns[0].System.Parts[0]
	if part.CommandKind != "error" || part.CommandName != "" || part.CommandID != "d1" {
		t.Fatalf("done-only part: %+v", part)
	}
}

// TestReducerSessionCommandMalformedIgnored：缺 commandId / 词表外 kind 的
// 帧不 commit（不产生空 turn、不 bump SyncRev、无 patch）。
func TestReducerSessionCommandMalformedIgnored(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "session_command", map[string]interface{}{"name": "compact", "kind": "running"}))
	r.Apply(ev(2, "dsh-web", "s1", "session_command", map[string]interface{}{"commandId": "c9", "kind": "cancelled"}))
	proj, _ := r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 0 || proj.SyncRev != 0 {
		t.Fatalf("malformed command frames must not commit: turns=%d syncRev=%d", len(proj.Turns), proj.SyncRev)
	}
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("malformed command frames must not produce a patch: %+v", patch)
	}
}

// TestReducerSessionCommandGoalInputLine：goal 命令行携带官方输入行
// （ui-goal goalCommandText）→ part.CommandLine；settle 整体替换 System 时
// 输入行必须续接（否则气泡随 settle 消失）；非 goal 命令不带。
func TestReducerSessionCommandGoalInputLine(t *testing.T) {
	r := newTestReducer()
	const line = "/goal 创作钢铁侠故事10000字左右，并写入 /tmp/demo-plan102.txt"
	r.Apply(ev(1, "dsh-web", "s1", "session_command", map[string]interface{}{
		"commandId": "g1", "name": "goal", "kind": "running", "inputLine": line,
	}))
	proj, _ := r.Snapshot("dsh-web", "s1")
	part := proj.Turns[0].System.Parts[0]
	if part.CommandLine != line {
		t.Fatalf("running CommandLine = %q, want %q", part.CommandLine, line)
	}

	// settle 重携带 inputLine（live codec applyCommandDone 同式续接）。
	r.Apply(ev(2, "dsh-web", "s1", "session_command", map[string]interface{}{
		"commandId": "g1", "name": "goal", "kind": "success", "inputLine": line,
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	part = proj.Turns[0].System.Parts[0]
	if part.CommandKind != "success" || part.CommandLine != line {
		t.Fatalf("settle must keep the input line: %+v", part)
	}

	// 无 inputLine 的命令（plan/compact 官方无气泡）CommandLine 为空。
	r.Apply(ev(3, "dsh-web", "s1", "session_command", map[string]interface{}{
		"commandId": "p1", "name": "plan", "kind": "running",
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	for _, tu := range proj.Turns {
		if tu.TurnID == "cmd:p1" && tu.System.Parts[0].CommandLine != "" {
			t.Fatalf("plan command must not carry an input line: %+v", tu.System.Parts[0])
		}
	}
}

// TestReducerPlanModeSnapshotStaging：session_plan_mode 只在视图变化时
// commit；patch 携带 planMode；重复帧无新 patch；DropPendingPatch 清空。
func TestReducerPlanModeSnapshotStaging(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "session_plan_mode", map[string]interface{}{"active": true, "pending": false}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection after plan mode")
	}
	if proj.PlanMode == nil || !proj.PlanMode.Active || proj.PlanMode.Pending {
		t.Fatalf("snapshot planMode = %+v, want {active:true pending:false}", proj.PlanMode)
	}
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok || patch.PlanMode == nil || !patch.PlanMode.Active {
		t.Fatalf("patch planMode = %+v ok=%v", patch.PlanMode, ok)
	}
	// 同视图重复帧：不 commit、无新 patch。
	r.Apply(ev(2, "dsh-web", "s1", "session_plan_mode", map[string]interface{}{"active": true, "pending": false}))
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("unchanged plan view must not re-patch: %+v", patch)
	}
	// pending → settled 迁移：patch 携带新视图。
	r.Apply(ev(3, "dsh-web", "s1", "session_plan_mode", map[string]interface{}{"active": false, "pending": false}))
	patch, ok = r.FlushPatch("dsh-web", "s1")
	if !ok || patch.PlanMode == nil || patch.PlanMode.Active || patch.PlanMode.Pending {
		t.Fatalf("cleared plan view patch = %+v ok=%v", patch.PlanMode, ok)
	}
	// DropPendingPatch 路径：staged planMode 参与空性判定，drop 后无残留。
	r.Apply(ev(4, "dsh-web", "s1", "session_plan_mode", map[string]interface{}{"active": true, "pending": true}))
	if !r.DropPendingPatch("dsh-web", "s1") {
		t.Fatal("DropPendingPatch must observe staged planMode")
	}
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("dropped planMode must not flush: %+v", patch)
	}
	proj, _ = r.Snapshot("dsh-web", "s1")
	if proj.PlanMode == nil || !proj.PlanMode.Active {
		t.Fatalf("snapshot keeps committed head after drop: %+v", proj.PlanMode)
	}
}

// TestReducerGoalSnapshotStaging：session_goal 整值快照只在变化时 commit；
// patch 携带 goal；清除相位（"none"）与重复帧语义；未知 phase fail-closed 丢弃。
func TestReducerGoalSnapshotStaging(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "session_goal", map[string]interface{}{
		"id": "goal-r-1", "revision": 1, "objective": "写个封神榜故事", "phase": "active", "maxGoalRounds": 256,
	}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection after goal")
	}
	if proj.Goal == nil || proj.Goal.Phase != "active" || proj.Goal.Objective != "写个封神榜故事" || proj.Goal.Revision != 1 {
		t.Fatalf("snapshot goal = %+v", proj.Goal)
	}
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok || patch.Goal == nil || patch.Goal.Phase != "active" {
		t.Fatalf("patch goal = %+v ok=%v", patch.Goal, ok)
	}
	// 同视图重复帧（含 BlockedReason 内容等价）：不 commit、无新 patch。
	r.Apply(ev(2, "dsh-web", "s1", "session_goal", map[string]interface{}{
		"id": "goal-r-1", "revision": 1, "objective": "写个封神榜故事", "phase": "active", "maxGoalRounds": 256,
	}))
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("unchanged goal view must not re-patch: %+v", patch)
	}
	// Grok's live evaluator boundary is a first-class goal value change even
	// when phase/revision/objective remain unchanged.
	r.Apply(ev(3, "dsh-web", "s1", "session_goal", map[string]interface{}{
		"id": "goal-r-1", "revision": 1, "objective": "写个封神榜故事", "phase": "active",
		"maxGoalRounds": 256, "verifyingCompletion": true,
	}))
	patch, ok = r.FlushPatch("dsh-web", "s1")
	if !ok || patch.Goal == nil || !patch.Goal.VerifyingCompletion {
		t.Fatalf("verifying goal patch = %+v ok=%v", patch.Goal, ok)
	}
	// blocked 迁移（带 blockedReason）：值比较不受指针影响，patch 携带新视图。
	r.Apply(ev(4, "dsh-web", "s1", "session_goal", map[string]interface{}{
		"id": "goal-r-1", "revision": 2, "objective": "写个封神榜故事", "phase": "blocked",
		"blockedReason": map[string]interface{}{"code": "rounds-exhausted", "message": "goal rounds exhausted"},
	}))
	patch, ok = r.FlushPatch("dsh-web", "s1")
	if !ok || patch.Goal == nil || patch.Goal.Phase != "blocked" || patch.Goal.BlockedReason == nil ||
		patch.Goal.BlockedReason.Code != "rounds-exhausted" {
		t.Fatalf("blocked goal patch = %+v ok=%v", patch.Goal, ok)
	}
	// 清除：phase "none" 必须能 patch 出去（远端横条移除）。
	r.Apply(ev(5, "dsh-web", "s1", "session_goal", map[string]interface{}{"phase": "none"}))
	patch, ok = r.FlushPatch("dsh-web", "s1")
	if !ok || patch.Goal == nil || patch.Goal.Phase != "none" {
		t.Fatalf("cleared goal patch = %+v ok=%v", patch.Goal, ok)
	}
	// 未知 phase：fail-closed 丢弃（不 commit、不猜状态）。
	r.Apply(ev(6, "dsh-web", "s1", "session_goal", map[string]interface{}{"phase": "dreaming", "id": "g", "objective": "x"}))
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("unknown phase must be dropped fail-closed: %+v", patch)
	}
	proj, _ = r.Snapshot("dsh-web", "s1")
	if proj.Goal == nil || proj.Goal.Phase != "none" {
		t.Fatalf("snapshot keeps committed head after dropped unknown phase: %+v", proj.Goal)
	}
}

// TestReducerContextInjectionSettleRow：subagent-settled 注入行 → 一条
// ctx:<itemId> 完成态 system turn（context_injection part，官方
// ContextInjectionRow 对位字段）；重放同 id 幂等（整值替换，不重复行、不
// churn）；身份缺失 fail-closed；不武装 execution。
func TestReducerContextInjectionSettleRow(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "context_injection", map[string]interface{}{
		"itemId": "ctxinj:9", "kind": "subagent-settled", "form": "notice",
		"summary":         "Background subagent sess-bg finished after 1 round.",
		"text":            "Background subagent sess-bg finished after 1 round.\n已生成封神榜第一章。",
		"senderSessionId": "sess-bg",
	}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok || len(proj.Turns) != 1 {
		t.Fatalf("projection = %+v", proj)
	}
	tu := proj.Turns[0]
	if tu.TurnID != "ctx:ctxinj:9" || tu.Status != "completed" || tu.System == nil || len(tu.System.Parts) != 1 {
		t.Fatalf("injection turn shell: %+v", tu)
	}
	if tu.System.Role != "system" || tu.System.ID != "ctx:ctxinj:9" {
		t.Fatalf("system identity: %+v", tu.System)
	}
	part := tu.System.Parts[0]
	if part.Type != "context_injection" || part.ItemID != "ctxinj:9" ||
		part.ContextKind != "subagent-settled" || part.ContextForm != "notice" ||
		part.ContextSummary != "Background subagent sess-bg finished after 1 round." ||
		part.ContextText != "Background subagent sess-bg finished after 1 round.\n已生成封神榜第一章。" ||
		part.ContextSenderSession != "sess-bg" {
		t.Fatalf("injection part: %+v", part)
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("injection rows must not arm execution: %+v", proj.Execution)
	}

	// 冷拉重放同 id（live 已 upsert 过）→ 幂等整值替换：仍是一条行。
	rev := proj.SyncRev
	r.Apply(ev(2, "dsh-web", "s1", "context_injection", map[string]interface{}{
		"itemId": "ctxinj:9", "kind": "subagent-settled", "form": "notice",
		"summary":         "Background subagent sess-bg finished after 1 round.",
		"text":            "Background subagent sess-bg finished after 1 round.\n已生成封神榜第一章。",
		"senderSessionId": "sess-bg",
		"timestampMillis": int64(1722244000000),
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "ctx:ctxinj:9" {
		t.Fatalf("replay must fold in place, turns = %+v", proj.Turns)
	}
	if proj.SyncRev != rev+1 {
		t.Fatalf("replay commits a mutation (cold adds timestamps): rev %d → %d", rev, proj.SyncRev)
	}
	if proj.Turns[0].StartedAt != 1722244000000 {
		t.Fatalf("cold replay backfills StartedAt: %+v", proj.Turns[0])
	}

	// 身份缺失：静默跳过，不造行。
	r2 := newTestReducer()
	r2.Apply(ev(1, "dsh-web", "s2", "context_injection", map[string]interface{}{
		"kind": "subagent-settled", "summary": "无 id",
	}))
	r2.Apply(ev(2, "dsh-web", "s2", "context_injection", map[string]interface{}{
		"itemId": "ctxinj:1", "summary": "无 kind",
	}))
	if proj2, ok := r2.Snapshot("dsh-web", "s2"); ok && len(proj2.Turns) > 0 {
		t.Fatalf("identity-less rows must not create turns: %+v", proj2.Turns)
	}

	// patch 面携带注入行。
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok || len(patch.UpsertTurns) != 1 || patch.UpsertTurns[0].TurnID != "ctx:ctxinj:9" {
		t.Fatalf("patch = %+v", patch)
	}
	if p := patch.UpsertTurns[0].System.Parts[0]; p.ContextKind != "subagent-settled" {
		t.Fatalf("patch part: %+v", p)
	}
}
