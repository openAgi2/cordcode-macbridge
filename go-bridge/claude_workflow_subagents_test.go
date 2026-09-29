package gobridge

// Claude workflow 子代理（方案 docs/2026-09-29-claude-workflow-subagent-scan-plan.md）
// 定向测试：E-W1（扫描/状态源/字段/RootSessionID）、E-W2（workflow part 生产/锚定/
// 聚合状态）、E-W5（指纹三态变化/digest 排除 archived/capability 门）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── fixture helpers ──────────────────────────────────────────────────────────

func writeWorkflowAgentMeta(t *testing.T, path, agentType, description, phase string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := map[string]any{"agentType": agentType, "description": description}
	if phase != "" {
		raw["workflowPhase"] = phase
	}
	b, _ := json.Marshal(raw)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

func writeWorkflowJournal(t *testing.T, runDir, lines string) {
	t.Helper()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "journal.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}
}

func writeAgentJSONL(t *testing.T, path, userText, assistantText, tag string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(minimalClaudeTranscript(userText, assistantText, tag)), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
}

// writeAgentInFlightJSONL writes an unsettled sidechain transcript (user row only)
// — the honest disk shape of a member that is still executing.
func writeAgentInFlightJSONL(t *testing.T, path, userText, tag string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	row := `{"uuid":"u-` + tag + `","type":"user","timestamp":"2026-09-30T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"` + userText + `"}]},"parentUuid":null}` + "\n"
	if err := os.WriteFile(path, []byte(row), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
}

// chtimes pins mtime for deterministic StartedAt/UpdatedAt assertions.
func chtimes(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// workflowSessionFixture lays out one session under root with a workflows run.
type workflowRunFixture struct {
	runID  string
	runDir string
}

func writeWorkflowSession(t *testing.T, root, projectKey, sessionID string) (string, []workflowRunFixture) {
	t.Helper()
	subagentsDir := filepath.Join(root, projectKey, sessionID, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return subagentsDir, nil // runs appended by caller via writeWorkflowRun
}

func writeWorkflowRun(t *testing.T, subagentsDir, runID string) string {
	t.Helper()
	runDir := filepath.Join(subagentsDir, "workflows", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return runDir
}

// ── E-W1：claudeBackgroundTasks 双布局扫描 ──────────────────────────────────

func TestClaudeBackgroundTasks_WorkflowLayoutRows(t *testing.T) {
	root := t.TempDir()
	subagentsDir, _ := writeWorkflowSession(t, root, "projkey", "sess-wf")

	// run1：settled 成员（journal started+result；reducer completed）+ running 成员。
	run1 := writeWorkflowRun(t, subagentsDir, "wf_run1")
	metaSpawn := time.Now().Add(-2 * time.Hour)
	jsonlActive := time.Now().Add(-1 * time.Hour)
	writeWorkflowAgentMeta(t, filepath.Join(run1, "agent-settled.meta.json"), "workflow-subagent", "pwd", "核对")
	writeAgentJSONL(t, filepath.Join(run1, "agent-settled.jsonl"), "pwd", "/tmp/x", "settled")
	writeWorkflowAgentMeta(t, filepath.Join(run1, "agent-live.meta.json"), "workflow-subagent", "plan-hash", "核对")
	writeAgentInFlightJSONL(t, filepath.Join(run1, "agent-live.jsonl"), "hash", "live")
	writeWorkflowJournal(t, run1,
		`{"type":"launched"}`+"\n"+
			`{"type":"started","key":"v2:k1","agentId":"settled","label":"pwd","phase":"核对"}`+"\n"+
			`{"type":"result","key":"v2:k1","agentId":"settled","result":{"dir":"/tmp/x"}}`+"\n"+
			`{"type":"started","key":"v2:k2","agentId":"live","label":"plan-hash","phase":"核对"}`+"\n")
	// mtime 钉死：meta=spawn 时刻、jsonl=activity 时刻（r1 F-6）。
	chtimes(t, filepath.Join(run1, "agent-settled.meta.json"), metaSpawn)
	chtimes(t, filepath.Join(run1, "agent-settled.jsonl"), jsonlActive)

	// run2：零 started（仅 launched）——不产任务行（r3 F-14）。
	run2 := writeWorkflowRun(t, subagentsDir, "wf_empty")
	writeWorkflowJournal(t, run2, `{"type":"launched"}`+"\n")

	// 旧布局对照组：直下 agent。
	legacyMeta := `{"agentType":"Explore","description":"legacy sub","toolUseId":"call_x","spawnDepth":1}`
	if err := os.WriteFile(filepath.Join(subagentsDir, "agent-legacy.meta.json"), []byte(legacyMeta), 0o644); err != nil {
		t.Fatalf("write legacy meta: %v", err)
	}
	writeAgentJSONL(t, filepath.Join(subagentsDir, "agent-legacy.jsonl"), "explore", "found", "legacy")

	tasks, err := claudeBackgroundTasks(root)
	if err != nil {
		t.Fatalf("claudeBackgroundTasks: %v", err)
	}
	byID := map[string]core.BackgroundTask{}
	for _, task := range tasks {
		byID[task.TaskID] = task
	}

	settled := byID["settled"]
	if settled.RootSessionID != "sess-wf" {
		t.Fatalf("workflow RootSessionID = %q, want sess-wf", settled.RootSessionID)
	}
	if settled.Status != "completed" {
		t.Fatalf("settled status = %q, want completed", settled.Status)
	}
	if settled.Title != "核对 · pwd" {
		t.Fatalf("workflow Title = %q, want 「核对 · pwd」(OD-W1 拼接)", settled.Title)
	}
	if settled.ParentTaskID != "" {
		t.Fatalf("workflow ParentTaskID = %q, want empty（不猜）", settled.ParentTaskID)
	}
	if !settled.StartedAt.Equal(metaSpawn) {
		t.Fatalf("StartedAt = %v, want meta mtime %v（spawn 时刻）", settled.StartedAt, metaSpawn)
	}
	if !settled.UpdatedAt.Equal(jsonlActive) {
		t.Fatalf("UpdatedAt = %v, want jsonl mtime %v（activity 时刻）", settled.UpdatedAt, jsonlActive)
	}

	live := byID["live"]
	if live.Status != "running" {
		t.Fatalf("live status = %q, want running（started 无 result）", live.Status)
	}

	legacy := byID["legacy"]
	if legacy.RootSessionID != "sess-wf" {
		t.Fatalf("legacy RootSessionID = %q, want sess-wf（旧布局同推导）", legacy.RootSessionID)
	}
	if legacy.Status != "completed" {
		t.Fatalf("legacy status = %q, want completed（现状 reducer 路径不回归）", legacy.Status)
	}
	if legacy.ParentTaskID != "" {
		t.Fatalf("legacy depth-1 ParentTaskID = %q, want 空（ParentTaskID 来自 parentAgentId，depth-1 无）", legacy.ParentTaskID)
	}

	if _, exists := byID["wf_empty"]; exists {
		// 空 run 无 agent meta，理论上不会出现；防御性确认没有以 runID 为 id 的行。
		t.Fatalf("empty run produced a task row")
	}
	if len(tasks) != 3 {
		t.Fatalf("task count = %d, want 3（空 run 不产行）", len(tasks))
	}
}

func TestClaudeBackgroundTasks_FailOpenBadMeta(t *testing.T) {
	root := t.TempDir()
	subagentsDir, _ := writeWorkflowSession(t, root, "projkey", "sess-bad")
	run := writeWorkflowRun(t, subagentsDir, "wf_bad")
	if err := os.WriteFile(filepath.Join(run, "agent-broken.meta.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeWorkflowAgentMeta(t, filepath.Join(run, "agent-good.meta.json"), "workflow-subagent", "ok", "")
	writeAgentJSONL(t, filepath.Join(run, "agent-good.jsonl"), "q", "a", "good")
	writeWorkflowJournal(t, run,
		`{"type":"started","key":"v2:g","agentId":"good","label":"ok","phase":""}`+"\n"+
			`{"type":"result","key":"v2:g","agentId":"good","result":{}}`+"\n")

	tasks, err := claudeBackgroundTasks(root)
	if err != nil {
		t.Fatalf("claudeBackgroundTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != "good" {
		t.Fatalf("expected only agent-good, got %+v", tasks)
	}
}

func TestClaudeRootSessionFromAgentPath(t *testing.T) {
	cases := map[string]string{
		filepath.Join("p", "sess-a", "subagents", "agent-x.meta.json"):                 "sess-a",
		filepath.Join("p", "sess-b", "subagents", "workflows", "wf_r", "agent-x.meta.json"): "sess-b",
	}
	for path, want := range cases {
		if got := claudeRootSessionFromAgentPath(path); got != want {
			t.Fatalf("claudeRootSessionFromAgentPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// 状态源合成（§3.2.3）纯函数锁定——含 r2 F-12 的误判防护。
func TestClaudeWorkflowTaskStatus_Composition(t *testing.T) {
	settled := map[string]claudeWorkflowAgentJournal{"a": {Started: true, Settled: true}}
	running := map[string]claudeWorkflowAgentJournal{"a": {Started: true, Settled: false}}

	cases := []struct {
		name       string
		journal    map[string]claudeWorkflowAgentJournal
		reducer    string
		hasTurns   bool
		want       string
	}{
		{"journal 未收口 + reducer completed → completed（transcript 已终）", running, "completed", true, "completed"},
		{"settled + reducer completed", settled, "completed", true, "completed"},
		{"settled + reducer failed（不被压成 completed，r1 F-5）", settled, "failed", true, "failed"},
		{"settled + reducer 无终态 → completed", settled, "running", true, "completed"},
		{"journal 缺席 → reducer", nil, "running", true, "running"},
		{"journal 缺席 + reducer 无证据 → running（r2 F-12）", nil, "completed", false, "running"},
		{"逐 agent 缺席（journal 有他行）→ 同构回落", map[string]claudeWorkflowAgentJournal{"other": {Started: true}}, "completed", false, "running"},
		// 中断修订（2026-09-30）：journal 未收口 + reducer 终态（aborted→failed）→
		// reducer 优先——被 workflow 重试弃置的成员不再永久「运行中」。
		{"journal 未收口 + reducer aborted/failed → failed（中断弃置）", running, "failed", true, "failed"},
		{"journal 未收口 + reducer completed → completed", running, "completed", true, "completed"},
		{"journal 未收口 + reducer running → running（真在跑）", running, "running", true, "running"},
		{"journal 未收口 + reducer 无证据 → running", running, "completed", false, "running"},
	}
	for _, c := range cases {
		if got := claudeWorkflowTaskStatus(c.journal, "a", c.reducer, c.hasTurns); got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// journal「同 agent 多次执行取末次 key」（未观测假设形状，r1 F-4）。
func TestParseClaudeWorkflowJournal_MultiExecutionLastKey(t *testing.T) {
	root := t.TempDir()
	run := filepath.Join(root, "wf_multi")
	writeWorkflowJournal(t, run,
		`{"type":"started","key":"v2:k1","agentId":"a","label":"l1","phase":"p1"}`+"\n"+
			`{"type":"result","key":"v2:k1","agentId":"a","result":{}}`+"\n"+
			`{"type":"started","key":"v2:k2","agentId":"a","label":"l2","phase":"p2"}`+"\n")
	_, states := parseClaudeWorkflowJournal(run)
	st := states["a"]
	if !st.Started || st.Settled {
		t.Fatalf("multi-exec last key: got %+v, want started+unsettled（末次 k2 无 result）", st)
	}
}

// 中断弃置端到端（2026-09-30 owner 真机验收修复）：复刻 wf_f5a59b21 的真实形状——
// 评审员实例被 workflow 重试中断：journal 只有 started 无 result；sidechain jsonl 尾部
// 是 user tool_result + "[Request interrupted by user]" 标记行。mapper 须把中断标记
// 转 turn_aborted（reducer 归 failed），合成层以 reducer 终态压过 journal 沉默——
// 不得永久「运行中」。
func TestWorkflowInterruptedMember_NotRunningForever(t *testing.T) {
	root := t.TempDir()
	subagentsDir := filepath.Join(root, "sess-int", "subagents")
	run := writeWorkflowRun(t, subagentsDir, "wf_int")

	writeWorkflowAgentMeta(t, filepath.Join(run, "agent-abandoned.meta.json"), "workflow-subagent", "评审员-第5轮", "评审")
	writeWorkflowAgentMeta(t, filepath.Join(run, "agent-retry.meta.json"), "workflow-subagent", "评审员-第5轮", "评审")
	writeAgentJSONL(t, filepath.Join(run, "agent-retry.jsonl"), "review", "approved", "retry")
	// 被弃置实例：真实尾部形状——user 行(tool_result) 后跟中断标记 user 行。
	abandonedJSONL := `{"uuid":"u-ab","type":"user","timestamp":"2026-09-30T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"review the plan"}]},"parentUuid":null}` + "\n" +
		`{"uuid":"a-ab","type":"assistant","timestamp":"2026-09-30T00:00:01.000Z","message":{"id":"m-ab","role":"assistant","content":[{"type":"text","text":"reviewing"}]},"parentUuid":"u-ab"}` + "\n" +
		`{"uuid":"u-ab2","type":"user","timestamp":"2026-09-30T00:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_x","content":"probe output"}]},"parentUuid":"a-ab"}` + "\n" +
		`{"uuid":"u-ab3","type":"user","timestamp":"2026-09-30T00:00:03.000Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"parentUuid":"a-ab"}` + "\n"
	if err := os.WriteFile(filepath.Join(run, "agent-abandoned.jsonl"), []byte(abandonedJSONL), 0o644); err != nil {
		t.Fatalf("write abandoned jsonl: %v", err)
	}
	writeWorkflowJournal(t, run,
		`{"type":"started","key":"v2:ab","agentId":"abandoned","label":"评审员-第5轮","phase":"评审"}`+"\n"+
			`{"type":"started","key":"v2:rt","agentId":"retry","label":"评审员-第5轮","phase":"评审"}`+"\n"+
			`{"type":"result","key":"v2:rt","agentId":"retry","result":{"verdict":"approved"}}`+"\n")

	// 1) mapper 层：中断标记 → turn_aborted（reducer 终态证据）。
	reducerStatus, hasTurns := claudeSidechainReducerEvidence(filepath.Join(run, "agent-abandoned.jsonl"))
	if !hasTurns {
		t.Fatalf("abandoned agent must have turns (hasTurns)")
	}
	if reducerStatus != "failed" {
		t.Fatalf("interrupted sidechain reducer status = %q, want failed（turn_aborted→aborted→failed）", reducerStatus)
	}

	// 2) 合成层：journal 未收口 + reducer failed → failed，不再永久 running。
	tasks, err := claudeBackgroundTasks(root)
	if err != nil {
		t.Fatalf("claudeBackgroundTasks: %v", err)
	}
	byID := map[string]core.BackgroundTask{}
	for _, task := range tasks {
		byID[task.TaskID] = task
	}
	if got := byID["abandoned"].Status; got != "failed" {
		t.Fatalf("abandoned member status = %q, want failed（中断弃置不残留运行中）", got)
	}
	if got := byID["retry"].Status; got != "completed" {
		t.Fatalf("retry member status = %q, want completed", got)
	}

	// 3) workflow 卡成员同合成（run 聚合：无 running、含 failed → failed）。
	anchors := map[string]claudeWorkflowAnchor{"wf_int": {TurnID: "turn-main"}}
	var events []projectionHydrateEvent
	if err := produceClaudeWorkflowRunEvents(context.Background(), subagentsDir, anchors, func(ev projectionHydrateEvent) bool {
		events = append(events, ev)
		return true
	}); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if got := events[0].Data["workflowStatus"]; got != "failed" {
		t.Fatalf("run status = %v, want failed（成员聚合：无 running、含 failed）", got)
	}
}

// ── E-W2：workflow part 生产 ─────────────────────────────────────────────────

func launchConfirmationText(runID string) string {
	return "Workflow launched in background. Task ID: " + runID + "\n" +
		"Summary: 评审 plan\n" +
		"Transcript dir: /Users/x/.claude/projects/p/s/subagents/workflows/" + runID + "\n" +
		"Script file: /Users/x/.claude/projects/p/s/workflows/scripts/review.js"
}

func workflowToolTurn(turnID, itemID, runID, name string) TurnProjection {
	input := map[string]any{"args": "…"}
	if name != "" {
		input["name"] = name
	}
	return TurnProjection{
		TurnID: turnID,
		Status: "completed",
		Assistant: &MessageProjection{
			ID: turnID, Role: "assistant",
			Parts: []ProjectionPart{{
				Type:       "tool",
				ItemID:     itemID,
				ToolName:   "Workflow",
				ToolInput:  input,
				ToolResult: launchConfirmationText(runID),
			}},
		},
	}
}

func TestBuildClaudeWorkflowAnchors_FirstWinsAndNameMapping(t *testing.T) {
	turns := []TurnProjection{
		workflowToolTurn("t1", "call_1", "wf_a", "plan-review-loop"),
		// resume：同 runId 第二锚（input 无 name）——first-wins 保 t1。
		workflowToolTurn("t2", "call_2", "wf_a", ""),
		// 无 name 的 launch（wf_dbdf025e 形态，r4 F-20）。
		workflowToolTurn("t3", "call_3", "wf_noname", ""),
	}
	anchors := buildClaudeWorkflowAnchors(turns)

	a := anchors["wf_a"]
	if a.TurnID != "t1" {
		t.Fatalf("wf_a anchor turn = %q, want t1（first-wins）", a.TurnID)
	}
	if a.Name != "plan-review-loop" {
		t.Fatalf("wf_a name = %q, want plan-review-loop（首锚 input.name）", a.Name)
	}
	nn := anchors["wf_noname"]
	if nn.TurnID != "t3" || nn.Name != "" {
		t.Fatalf("wf_noname anchor = %+v, want t3 + 空串 name（不合成回退）", nn)
	}
	// thinking 文本里的变体串不得产生锚（无 Workflow 工具行即无锚）。
	if _, ok := anchors["wf_a8d799a9-053"]; ok {
		t.Fatalf("variant runId leaked an anchor")
	}
}

func TestProduceClaudeWorkflowRunEvents_CardShapeAndAggregation(t *testing.T) {
	root := t.TempDir()
	subagentsDir := filepath.Join(root, "sess-x", "subagents")
	run := writeWorkflowRun(t, subagentsDir, "wf_card")

	writeWorkflowAgentMeta(t, filepath.Join(run, "agent-r.meta.json"), "workflow-subagent", "评审", "r1")
	writeAgentInFlightJSONL(t, filepath.Join(run, "agent-r.jsonl"), "review", "r")
	writeWorkflowAgentMeta(t, filepath.Join(run, "agent-s.meta.json"), "workflow-subagent", "修订", "r2")
	writeAgentJSONL(t, filepath.Join(run, "agent-s.jsonl"), "revise", "ok", "s")
	// meta-only 成员（journal 无记录，r3 F-16 → 归 meta.workflowPhase 组）。
	writeWorkflowAgentMeta(t, filepath.Join(run, "agent-m.meta.json"), "workflow-subagent", "meta-only", "r1")
	writeWorkflowJournal(t, run,
		`{"type":"started","key":"v2:1","agentId":"s","label":"修订","phase":"r2"}`+"\n"+
			`{"type":"result","key":"v2:1","agentId":"s","result":{}}`+"\n"+
			`{"type":"started","key":"v2:2","agentId":"r","label":"评审","phase":"r1"}`+"\n")

	anchors := map[string]claudeWorkflowAnchor{"wf_card": {TurnID: "turn-main", Name: "plan-review-loop"}}
	var events []projectionHydrateEvent
	emit := func(ev projectionHydrateEvent) bool { events = append(events, ev); return true }

	if err := produceClaudeWorkflowRunEvents(context.Background(), subagentsDir, anchors, emit); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1（每 run 单发）", len(events))
	}
	ev := events[0]
	if ev.Event != "workflow_run" {
		t.Fatalf("event = %q, want workflow_run", ev.Event)
	}
	if got := ev.Data["turnId"]; got != "turn-main" {
		t.Fatalf("turnId = %v, want turn-main（first-wins 锚点）", got)
	}
	if got := ev.Data["workflowId"]; got != "wf_card" {
		t.Fatalf("workflowId = %v", got)
	}
	if got := ev.Data["workflowName"]; got != "plan-review-loop" {
		t.Fatalf("workflowName = %v", got)
	}
	// run 级状态：agent-r running → running（成员聚合，r2 F-9）。
	if got := ev.Data["workflowStatus"]; got != "running" {
		t.Fatalf("workflowStatus = %v, want running", got)
	}

	phases, ok := ev.Data["workflowPhases"].([]map[string]interface{})
	if !ok {
		t.Fatalf("workflowPhases type = %T", ev.Data["workflowPhases"])
	}
	// 分组首现序：journal 首个 started 的 phase=r2 在前；meta-only 归 r1 组。
	if len(phases) != 2 {
		t.Fatalf("phases = %d, want 2（r2 首现 + r1）", len(phases))
	}
	if phases[0]["phase"] != "r2" || phases[1]["phase"] != "r1" {
		t.Fatalf("phase order = [%v, %v], want [r2, r1]", phases[0]["phase"], phases[1]["phase"])
	}
	type member struct {
		Seq            int
		Label          string
		ChildSessionID string
		Status         string
	}
	var r1members, r2members []map[string]interface{}
	for _, g := range phases {
		ms := g["members"].([]map[string]interface{})
		if g["phase"] == "r1" {
			r1members = ms
		} else {
			r2members = ms
		}
	}
	if len(r2members) != 1 || r2members[0]["childSessionId"] != "s" || r2members[0]["status"] != "completed" {
		t.Fatalf("r2 members = %+v（childSessionId=agentID、journal settled→completed）", r2members)
	}
	if len(r1members) != 2 {
		t.Fatalf("r1 members = %d, want 2（journal r + meta-only m）", len(r1members))
	}
	statuses := map[string]bool{}
	for _, m := range r1members {
		statuses[m["childSessionId"].(string)] = m["status"] == "running"
	}
	if !statuses["r"] {
		t.Fatalf("journal-started running member missing: %+v", r1members)
	}
}

func TestProduceClaudeWorkflowRunEvents_ZeroStartedAndUnanchoredSkip(t *testing.T) {
	root := t.TempDir()
	subagentsDir := filepath.Join(root, "sess-y", "subagents")
	// 空 run（仅 launched，r3 F-14）——有锚也不进投影。
	empty := writeWorkflowRun(t, subagentsDir, "wf_z")
	writeWorkflowJournal(t, empty, `{"type":"launched"}`+"\n")
	// journal 缺失的 run——同样零 started，不进。
	_ = writeWorkflowRun(t, subagentsDir, "wf_nojournal")
	// 有成员但无锚（r3 F-13 fail-open）。
	un := writeWorkflowRun(t, subagentsDir, "wf_un")
	writeWorkflowAgentMeta(t, filepath.Join(un, "agent-u.meta.json"), "workflow-subagent", "u", "")
	writeAgentJSONL(t, filepath.Join(un, "agent-u.jsonl"), "q", "a", "u")
	writeWorkflowJournal(t, un, `{"type":"started","key":"v2:u","agentId":"u","label":"u","phase":""}`+"\n")

	var count int
	emit := func(ev projectionHydrateEvent) bool { count++; return true }
	if err := produceClaudeWorkflowRunEvents(context.Background(), subagentsDir, map[string]claudeWorkflowAnchor{}, emit); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if count != 0 {
		t.Fatalf("events = %d, want 0（零 started / 无锚 / journal 缺失全部跳过）", count)
	}
}

func TestProduceClaudeWorkflowRunEvents_RunStatusAggregationForms(t *testing.T) {
	// 三形态：全员 settled 无 failed → completed；含 failed → failed；仍有 running → running。
	build := func(journal string) string {
		// 通过 sub-function 直接构造：借 produceClaudeWorkflowRunEvents 太重，直接
		// 用成员聚合等价逻辑在 produce 上跑三个 run。
		return journal
	}
	_ = build

	root := t.TempDir()
	subagentsDir := filepath.Join(root, "sess-z", "subagents")

	done := writeWorkflowRun(t, subagentsDir, "wf_done")
	writeWorkflowAgentMeta(t, filepath.Join(done, "agent-d.meta.json"), "workflow-subagent", "d", "p")
	writeAgentJSONL(t, filepath.Join(done, "agent-d.jsonl"), "q", "a", "d")
	writeWorkflowJournal(t, done,
		`{"type":"started","key":"v2:d","agentId":"d","label":"d","phase":"p"}`+"\n"+
			`{"type":"result","key":"v2:d","agentId":"d","result":{}}`+"\n")

	live := writeWorkflowRun(t, subagentsDir, "wf_live")
	writeWorkflowAgentMeta(t, filepath.Join(live, "agent-l.meta.json"), "workflow-subagent", "l", "p")
	writeAgentInFlightJSONL(t, filepath.Join(live, "agent-l.jsonl"), "q", "l")
	writeWorkflowJournal(t, live, `{"type":"started","key":"v2:l","agentId":"l","label":"l","phase":"p"}`+"\n")

	anchors := map[string]claudeWorkflowAnchor{
		"wf_done": {TurnID: "t-done"},
		"wf_live": {TurnID: "t-live"},
	}
	var statuses map[string]string
	statuses = map[string]string{}
	emit := func(ev projectionHydrateEvent) bool {
		statuses[ev.Data["workflowId"].(string)] = ev.Data["workflowStatus"].(string)
		return true
	}
	if err := produceClaudeWorkflowRunEvents(context.Background(), subagentsDir, anchors, emit); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if statuses["wf_done"] != "completed" {
		t.Fatalf("wf_done status = %q, want completed（≠running，终态不残留）", statuses["wf_done"])
	}
	if statuses["wf_live"] != "running" {
		t.Fatalf("wf_live status = %q, want running", statuses["wf_live"])
	}
	// failed 形态由 TestClaudeWorkflowTaskStatus_Composition 的 reducer-failed 用例
	// 覆盖合成逻辑（journal settled + reducer failed → failed → 聚合含 failed → failed）。
}

// ── E-W5：指纹三态变化 / digest 排除 archived / capability 门 ───────────────

func TestClaudeSubagentsAggregateMtime_ThreeChangeClasses(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "proj")
	sessionID := "sess-fp"
	subagentsDir := filepath.Join(projectPath, sessionID, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	base := claudeSubagentsAggregateMtime(projectPath, sessionID)
	if base == 0 {
		t.Fatalf("subagents dir exists → base must be >0 (dir mtime)")
	}

	// 变化①：新 run 创建（workflows 目录 mtime）。
	runDir := writeWorkflowRun(t, subagentsDir, "wf_new")
	afterRun := claudeSubagentsAggregateMtime(projectPath, sessionID)
	if afterRun <= base {
		t.Fatalf("new run creation must increase aggregate mtime: %d → %d", base, afterRun)
	}

	// 变化②：run 内 spawn（run 目录 mtime）。
	writeWorkflowAgentMeta(t, filepath.Join(runDir, "agent-spawn.meta.json"), "workflow-subagent", "s", "")
	afterSpawn := claudeSubagentsAggregateMtime(projectPath, sessionID)
	if afterSpawn <= afterRun {
		t.Fatalf("in-run spawn must increase aggregate mtime: %d → %d", afterRun, afterSpawn)
	}

	// 变化③：journal 状态翻转（journal mtime——用 chtimes 显式抬高，绕开文件系统
	// 时间粒度）。
	journal := filepath.Join(runDir, "journal.jsonl")
	writeWorkflowJournal(t, runDir, `{"type":"launched"}`+"\n")
	future := time.Now().Add(10 * time.Second)
	chtimes(t, journal, future)
	afterFlip := claudeSubagentsAggregateMtime(projectPath, sessionID)
	if afterFlip <= afterSpawn {
		t.Fatalf("journal flip must increase aggregate mtime: %d → %d", afterSpawn, afterFlip)
	}

	// 旧布局回归：直下 agent 文件增删仍改指纹（subagents 目录 mtime）。
	legacy := filepath.Join(subagentsDir, "agent-legacy.meta.json")
	if err := os.WriteFile(legacy, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 直下 agent 增删改写 subagents 目录 mtime（聚合输入）；先前 journal 的 future
	// chtimes 会压制后续自然 mtime，显式抬高目录时间到更高值。
	chtimes(t, subagentsDir, future.Add(10*time.Second))
	if got := claudeSubagentsAggregateMtime(projectPath, sessionID); got <= afterFlip {
		t.Fatalf("legacy direct-file change must increase aggregate mtime: %d → %d", afterFlip, got)
	}
}

func TestSubagentsAggregateDigest_ExcludesArchived(t *testing.T) {
	c := newClaudeSessionCatalog("")
	active := claudeSessionIndexEntry{Key: claudeSessionKey{ProjectKey: "p", SessionID: "sess-live"}}
	active.Fingerprint.SubagentsMtimeUnixNano = 111
	archived := claudeSessionIndexEntry{
		Key:        claudeSessionKey{ProjectKey: "p", SessionID: "sess-arch"},
		ArchivedAt: time.Now(),
	}
	archived.Fingerprint.SubagentsMtimeUnixNano = 222
	c.mu.Lock()
	c.snapshot = &claudeSessionSnapshot{
		ByKey:  map[claudeSessionKey]claudeSessionIndexEntry{active.Key: active, archived.Key: archived},
		Sorted: []claudeSessionIndexEntry{active, archived},
	}
	c.mu.Unlock()

	digest := c.subagentsAggregateDigest()
	if digest == "" || strings.Contains(digest, "sess-arch") {
		t.Fatalf("digest must hash only visible sessions: %q", digest)
	}
	// 改 active 条目的子代理指纹 → digest 变化；只改 archived 条目 → 不变。
	active2 := active
	active2.Fingerprint.SubagentsMtimeUnixNano = 333
	c.mu.Lock()
	c.snapshot.Sorted = []claudeSessionIndexEntry{active2, archived}
	c.mu.Unlock()
	d2 := c.subagentsAggregateDigest()
	if d2 == digest {
		t.Fatalf("active subagent change must alter digest")
	}
	archived2 := archived
	archived2.Fingerprint.SubagentsMtimeUnixNano = 999
	c.mu.Lock()
	c.snapshot.Sorted = []claudeSessionIndexEntry{active2, archived2}
	c.mu.Unlock()
	if d3 := c.subagentsAggregateDigest(); d3 != d2 {
		t.Fatalf("archived-only subagent change must NOT alter digest（r2 F-11）")
	}
}

func TestDeriveBackendCapabilities_ClaudeRegistrationKey(t *testing.T) {
	// G4：生产注册键 "claude" 的 descriptor 必须含 background_tasks 两项
	// （nil agent：claude 不经 provider 接口，走 isClaudeBackendID 门）。
	caps := deriveBackendCapabilities("claude", nil, "app_server")
	has := func(want string) bool {
		for _, c := range caps {
			if c == want {
				return true
			}
		}
		return false
	}
	if !has("background_tasks") || !has("background_task_details") {
		t.Fatalf("claude descriptor caps missing background tasks pair: %v", caps)
	}
	// alias 键回归。
	capsAlias := deriveBackendCapabilities("claudecode", nil, "app_server")
	found := 0
	for _, c := range capsAlias {
		if c == "background_tasks" || c == "background_task_details" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("claudecode alias caps missing pair: %v", capsAlias)
	}
	// 无关 backend 不因新门获得任务面。
	for _, id := range []string{"codex", "opencode", "grokbuild", "dsh-web"} {
		for _, c := range deriveBackendCapabilities(id, nil, "app_server") {
			if c == "background_tasks" || c == "background_task_details" {
				t.Fatalf("backend %q must not gain background_tasks via claude gate", id)
			}
		}
	}
}
