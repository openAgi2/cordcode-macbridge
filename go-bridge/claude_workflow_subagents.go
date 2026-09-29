package gobridge

// Claude Code workflow subagents（方案 docs/2026-09-29-claude-workflow-subagent-scan-plan.md，
// r5 APPROVED v6）。
//
// 上游磁盘布局（本机实测契约，无官方源码 checkout）：
//
//	<project>/<session-uuid>.jsonl                       主 transcript
//	<project>/<session-uuid>/subagents/agent-*.jsonl+meta  旧布局（Agent-tool sidechain）
//	<project>/<session-uuid>/subagents/workflows/wf_<runId>/
//	    agent-*.jsonl + agent-*.meta.json + journal.jsonl   新布局（workflow 子代理）
//
// workflow meta 字段并集：agentType（恒 "workflow-subagent"）、description、model、
// requestNonInteractive、requestShape、spawnDepth（恒 1）、workflowPhase——无
// parentAgentId/toolUseId/stoppedByUser（旧布局字段）。
//
// journal.jsonl 事件词表（3 类）：{"type":"launched"} / {"type":"started","key","agentId",
// "label","phase"} / {"type":"result","key","agentId","result"}。key 配对 started↔result；
// result 对象无错误信号（只证明收口）——失败信号唯一来源是 sidechain reducer。
//
// 锚点（S2）：主 transcript 的 Workflow tool_result 是启动确认纯文本（无结构化 runId
// 字段），runId 唯一可靠提取 = 解析 "Transcript dir:" 行取 basename，与磁盘 wf_<runId>
// 目录名精确等值匹配（不做前缀——实测变体串位于 thinking 文本内）。

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isClaudeBackendID matches the claude agent under EITHER registration key. Production
// registers the agent under "claude" (main.go drivers list / RegisterAgent store the raw
// id; the "claudecode" alias only feeds CreateAgent), while several gates historically
// checked `id == "claudecode"` and therefore never fired (G3/G4, plan §2.4).
func isClaudeBackendID(id string) bool {
	return id == "claude" || id == "claudecode"
}

// ── journal 解析（fail-open：缺失/坏行跳过）───────────────────────────────────

type claudeWorkflowJournalEntry struct {
	Type    string
	Key     string
	AgentID string
	Label   string
	Phase   string
}

// claudeWorkflowAgentJournal is one agent's lifecycle projection from the journal:
// whether it ever started, and whether its LAST execution key has a result.
type claudeWorkflowAgentJournal struct {
	Started bool
	Settled bool
}

// parseClaudeWorkflowJournal reads journal.jsonl in append order. Per agent, the last
// seen started-key decides settledness (same agent executing under multiple keys is an
// UNOBSERVED upstream hypothesis — plan §2.3; last-key semantics keeps the behavior
// deterministic either way). Missing/blank journal → (nil, nil), never an error.
func parseClaudeWorkflowJournal(runDir string) ([]claudeWorkflowJournalEntry, map[string]claudeWorkflowAgentJournal) {
	f, err := os.Open(filepath.Join(runDir, "journal.jsonl"))
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var entries []claudeWorkflowJournalEntry
	lastKey := map[string]string{}
	states := map[string]claudeWorkflowAgentJournal{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Type    string `json:"type"`
			Key     string `json:"key"`
			AgentID string `json:"agentId"`
			Label   string `json:"label"`
			Phase   string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue // fail-open per line
		}
		if raw.AgentID == "" {
			if raw.Type == "launched" {
				entries = append(entries, claudeWorkflowJournalEntry{Type: raw.Type})
			}
			continue
		}
		entries = append(entries, claudeWorkflowJournalEntry{
			Type: raw.Type, Key: raw.Key, AgentID: raw.AgentID, Label: raw.Label, Phase: raw.Phase,
		})
		st := states[raw.AgentID]
		switch raw.Type {
		case "started":
			st.Started = true
			if raw.Key != "" {
				lastKey[raw.AgentID] = raw.Key
				st.Settled = false // new execution opened
			}
		case "result":
			if raw.Key != "" && lastKey[raw.AgentID] == raw.Key {
				st.Settled = true
			}
		}
		states[raw.AgentID] = st
	}
	return entries, states
}

// ── 布局扫描（S1）────────────────────────────────────────────────────────────

type claudeWorkflowLocatedAgent struct {
	Meta        claudeSidechainMeta
	RunID       string // "" = 旧布局（直下）
	RunDir      string // workflow run 目录；旧布局为 ""
	MetaPath    string
	JsonlPath   string
	MetaModTime time.Time
}

// scanClaudeWorkflowRunAgents enumerates agent-*.meta.json directly inside one workflow
// run directory. Read/parse errors are skipped (fail-open sidechain §5).
func scanClaudeWorkflowRunAgents(runDir string) []claudeWorkflowLocatedAgent {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil
	}
	runID := filepath.Base(runDir)
	var found []claudeWorkflowLocatedAgent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		agentID := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		meta, ok := readClaudeSidechainMetaFile(filepath.Join(runDir, name), agentID)
		if !ok {
			continue
		}
		found = append(found, claudeWorkflowLocatedAgent{
			Meta:        meta,
			RunID:       runID,
			RunDir:      runDir,
			MetaPath:    filepath.Join(runDir, name),
			JsonlPath:   filepath.Join(runDir, "agent-"+agentID+".jsonl"),
			MetaModTime: info.ModTime(),
		})
	}
	return found
}

// claudeRootSessionFromAgentPath walks up from an agent meta file path past the
// subagents / workflows / wf_* ancestors; the first remaining ancestor directory name
// is the session-uuid directory. Both layouts resolve there:
//
//	<uuid>/subagents/agent-x.meta.json                       → <uuid>
//	<uuid>/subagents/workflows/wf_r/agent-x.meta.json        → <uuid>
func claudeRootSessionFromAgentPath(agentMetaPath string) string {
	dir := filepath.Dir(agentMetaPath)
	for {
		base := filepath.Base(dir)
		if base == "subagents" || base == "workflows" || strings.HasPrefix(base, "wf_") {
			parent := filepath.Dir(dir)
			if parent == dir {
				return ""
			}
			dir = parent
			continue
		}
		if base == "" || base == "." || base == string(filepath.Separator) {
			return ""
		}
		return base
	}
}

// claudeWorkflowTaskStatus composes one workflow agent's background-task status per the
// two-source grading (plan §3.2.3): journal decides lifecycle (running vs settled);
// once settled the terminal quality comes from the sidechain reducer (the only source
// of "failed"); a journal-absent agent falls back to the reducer entirely, and with no
// reducer evidence either it stays running (never guessed completed — r2 F-12).
func claudeWorkflowTaskStatus(
	journalStates map[string]claudeWorkflowAgentJournal,
	agentID string,
	reducerStatus string,
	reducerHasTurns bool,
) string {
	st, recorded := journalStates[agentID]
	if !recorded {
		// journal missing entirely OR no records for this agent: reducer fallback.
		if !reducerHasTurns {
			return "running" // no execution evidence either way — stay honest
		}
		return reducerStatus
	}
	if !st.Settled {
		return "running" // last execution has no result yet
	}
	// Settled: terminal quality from the reducer; a reducer still mid-scan (no terminal)
	// counts as completed — journal result closed the execution with no error signal.
	if reducerStatus == "failed" {
		return "failed"
	}
	return "completed"
}

// claudeWorkflowTaskTitle renders the OD-W1 flat-row title: 「<phase> · <description>」.
// Missing phase degrades to the bare description.
func claudeWorkflowTaskTitle(meta claudeSidechainMeta) string {
	phase := strings.TrimSpace(meta.WorkflowPhase)
	desc := strings.TrimSpace(meta.Description)
	switch {
	case phase != "" && desc != "":
		return phase + " · " + desc
	case desc != "":
		return desc
	default:
		return phase
	}
}

// ── S2：workflow part 生产（cold hydrate 事务内，与 B4 同域）──────────────────

type claudeWorkflowAnchor struct {
	TurnID string
	Name   string // first-wins launch tool_use input.name；缺席为空串（r4 F-20）
}

// buildClaudeWorkflowAnchors scans hydrated mainstream turns for Workflow tool calls.
// Each tool result's launch-confirmation text carries a "Transcript dir:" line whose
// basename is the run directory name (exact equality — never prefix matching). First
// anchor per runId wins (resume re-invocations keep the card position stable).
func buildClaudeWorkflowAnchors(turns []TurnProjection) map[string]claudeWorkflowAnchor {
	anchors := map[string]claudeWorkflowAnchor{}
	for i := range turns {
		turn := turns[i]
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type != "tool" || part.ToolName != "Workflow" {
				continue
			}
			runID := runIDFromTranscriptDirLine(claudeToolPayloadString(part.ToolResult))
			if runID == "" {
				continue
			}
			if _, seen := anchors[runID]; seen {
				continue // first-wins
			}
			anchor := claudeWorkflowAnchor{TurnID: turn.TurnID}
			if input, ok := part.ToolInput.(map[string]interface{}); ok {
				if name, ok := input["name"].(string); ok {
					anchor.Name = strings.TrimSpace(name)
				}
			}
			anchors[runID] = anchor
		}
	}
	return anchors
}

// runIDFromTranscriptDirLine extracts the wf_<runId> from the launch confirmation's
// "Transcript dir: <path>" line (basename, exact). Empty when absent.
func runIDFromTranscriptDirLine(resultText string) string {
	for _, line := range strings.Split(resultText, "\n") {
		line = strings.TrimRight(line, "\r")
		if idx := strings.Index(line, "Transcript dir:"); idx >= 0 {
			path := strings.TrimSpace(line[idx+len("Transcript dir:"):])
			if path == "" {
				return ""
			}
			base := filepath.Base(path)
			if strings.HasPrefix(base, "wf_") {
				return base
			}
			return ""
		}
	}
	return ""
}

// claudeToolPayloadString tolerantly flattens a tool input/result payload to text for
// anchor extraction. Handles the shapes the claude mapper produces (string, content
// blocks list, {content|text:…} maps); anything else yields "".
func claudeToolPayloadString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		var b strings.Builder
		for _, item := range t {
			b.WriteString(claudeToolPayloadString(item))
			b.WriteString("\n")
		}
		return b.String()
	case map[string]interface{}:
		if s, ok := t["text"].(string); ok {
			return s
		}
		if s, ok := t["content"].(string); ok {
			return s
		}
		return ""
	default:
		return ""
	}
}

// produceClaudeWorkflowRunEvents emits ONE workflow_run hydrate event per anchored run
// with ≥1 journal-started member (zero-started runs — including journal-missing ones —
// produce no card, r3 F-14; unanchored runs likewise fail-open skip). Members keep
// journal append order; journal-absent meta-only members join afterwards grouped by
// their meta.workflowPhase (missing → nil-phase group, r3 F-16). The run-level status
// aggregates members: any running → running, else any failed → failed, else completed
// (r2 F-9). childSessionId carries the agentID — the consumer-side detail taskId
// (r1 F-3, zero wire change).
func produceClaudeWorkflowRunEvents(
	ctx context.Context,
	subagentsDir string,
	anchors map[string]claudeWorkflowAnchor,
	emit func(projectionHydrateEvent) bool,
) error {
	workflowsDir := filepath.Join(subagentsDir, "workflows")
	runEntries, err := os.ReadDir(workflowsDir)
	if err != nil {
		return nil // no workflows layer (legacy-only session) — nothing to do
	}
	for _, runEntry := range runEntries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !runEntry.IsDir() || !strings.HasPrefix(runEntry.Name(), "wf_") {
			continue
		}
		runID := runEntry.Name()
		anchor, ok := anchors[runID]
		if !ok {
			runLog := runID
			if len(runLog) > 16 {
				runLog = runLog[:16]
			}
			slog.Debug("claude workflow run has no mainstream anchor; skipping card", "run", runLog)
			continue
		}
		runDir := filepath.Join(workflowsDir, runID)
		journalEntries, journalStates := parseClaudeWorkflowJournal(runDir)
		metas := scanClaudeWorkflowRunAgents(runDir)

		// Member order: journal started-append order first, then meta-only agents.
		type memberRow struct {
			agentID string
			label   string
			phase   string // journal phase; meta-only members fall back to meta.workflowPhase
			status  string
		}
		byAgent := map[string]claudeSidechainMeta{}
		for _, a := range metas {
			byAgent[a.Meta.agentID] = a.Meta
		}

		hasStarted := false
		var members []memberRow
		seen := map[string]bool{}
		for _, e := range journalEntries {
			if e.Type != "started" || e.AgentID == "" || seen[e.AgentID] {
				continue
			}
			seen[e.AgentID] = true
			hasStarted = true
			meta := byAgent[e.AgentID]
			label := strings.TrimSpace(e.Label)
			if label == "" {
				label = strings.TrimSpace(meta.Description)
			}
			members = append(members, memberRow{
				agentID: e.AgentID,
				label:   label,
				phase:   e.Phase,
				status:  workflowMemberStatus(journalStates, metas, e.AgentID),
			})
		}
		if !hasStarted {
			// Zero started members (journal only "launched", or journal missing): no
			// card — a dead empty run must never render a permanent fake-running card.
			continue
		}
		for _, a := range metas {
			if seen[a.Meta.agentID] {
				continue
			}
			seen[a.Meta.agentID] = true
			members = append(members, memberRow{
				agentID: a.Meta.agentID,
				label:   strings.TrimSpace(a.Meta.Description),
				phase:   strings.TrimSpace(a.Meta.WorkflowPhase),
				status:  workflowMemberStatus(journalStates, metas, a.Meta.agentID),
			})
		}

		// Run status: member aggregation (any running → running; any failed → failed;
		// else completed).
		runStatus := "completed"
		for _, m := range members {
			switch m.status {
			case "running":
				runStatus = "running"
			case "failed":
				if runStatus != "running" {
					runStatus = "failed"
				}
			}
		}

		// Phase groups in first-appearance order (journal members already appended in
		// journal order; meta-only members append their own groups afterwards).
		type phaseGroup struct {
			phase   *string
			members []map[string]interface{}
		}
		var groups []phaseGroup
		groupIndex := map[string]int{}
		groupFor := func(phase string, missing bool) *phaseGroup {
			key := "\x00missing"
			if !missing {
				key = phase
			}
			if idx, ok := groupIndex[key]; ok {
				return &groups[idx]
			}
			g := phaseGroup{}
			if !missing {
				p := phase
				g.phase = &p
			}
			groups = append(groups, g)
			groupIndex[key] = len(groups) - 1
			return &groups[len(groups)-1]
		}
		for _, m := range members {
			missing := m.phase == ""
			g := groupFor(m.phase, missing)
			g.members = append(g.members, map[string]interface{}{
				"label":          m.label,
				"childSessionId": m.agentID,
				"status":         m.status,
			})
		}
		// Assign seq by overall member order within each group (1-based).
		for gi := range groups {
			for mi := range groups[gi].members {
				groups[gi].members[mi]["seq"] = mi + 1
			}
		}

		phasesWire := make([]map[string]interface{}, 0, len(groups))
		for _, g := range groups {
			phaseWire := map[string]interface{}{"members": g.members}
			if g.phase != nil {
				phaseWire["phase"] = *g.phase
			}
			phasesWire = append(phasesWire, phaseWire)
		}

		ev := projectionHydrateEvent{
			Event: "workflow_run",
			Data: map[string]interface{}{
				"turnId":         anchor.TurnID,
				"workflowId":     runID,
				"workflowName":   anchor.Name,
				"workflowStatus": runStatus,
				"workflowPhases": phasesWire,
			},
		}
		if !emit(ev) {
			return ctx.Err()
		}
	}
	return nil
}

// workflowMemberStatus derives one member's status with the same two-source grading as
// the background-task rows (§3.2.3) so the card and the task center never disagree.
func workflowMemberStatus(
	journalStates map[string]claudeWorkflowAgentJournal,
	agents []claudeWorkflowLocatedAgent,
	agentID string,
) string {
	reducerStatus, reducerHasTurns := claudeSidechainReducerEvidence(agentJSONLPath(agents, agentID))
	return claudeWorkflowTaskStatus(journalStates, agentID, reducerStatus, reducerHasTurns)
}

func agentJSONLPath(agents []claudeWorkflowLocatedAgent, agentID string) string {
	for _, a := range agents {
		if a.Meta.agentID == agentID {
			return a.JsonlPath
		}
	}
	return ""
}
