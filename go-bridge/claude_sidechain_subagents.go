package gobridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// B4 child-stream (sync-only). This file implements the sidechain source-read pre-pass that
// runs during cold hydrate AFTER the mainstream Claude transcript is reduced into the hydrate
// transaction. It reads sibling subagents/agent-<id>.jsonl + .meta.json files, builds the
// nested subagent tree, and emits one subagent_part hydrate event per depth-1 agent. The caller
// (runProjectionHydrateTransaction) routes those events through ApplyHydrateEvent → tx.reducer
// — the same hydrate transaction domain as the mainstream scan (guardrail §5: hydrate
// source-read only; never IngestLive/EventBuffer/pending-live). On any failure the caller
// fail-opens: logs and proceeds without subagent parts, i.e. the current state (the parent
// Agent tool_result) is preserved — never a fabricated subagent (guardrail §10).

// maxClaudeSidechainDepth mirrors agent/claudecode.maxClaudeChildStreamDepth. The depth/cycle
// guard is reproduced here (in gobridge) rather than imported because child_streams.go's
// BuildClaudeChildStreamGraph joins via tool_result ResultAgentIDs (the async-stub path),
// whereas B4 joins sidechain files by the authoritative .meta.json edges (parentAgentId /
// toolUseId). The walk algorithm is the same; only the edges differ.
const maxClaudeSidechainDepth = 8

// claudeSidechainMeta decodes a sidechain subagents/agent-<id>.meta.json sidecar. Fields
// verified byte-for-byte against real Claude sidechain samples: depth-1 has toolUseId +
// spawnDepth=1 and NO parentAgentId; depth≥2 adds parentAgentId + spawnDepth≥2. agentId is NOT
// in the JSON — it is the <id> segment of the filename (agent-<id>.meta.json).
type claudeSidechainMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	ToolUseID     string `json:"toolUseId"`
	ParentAgentID string `json:"parentAgentId"`
	SpawnDepth    int    `json:"spawnDepth"`
	agentID       string // derived from filename, not JSON
}

// readClaudeSidechainMeta enumerates subagents/agent-*.meta.json in subagentsDir. A missing
// directory is not an error (Codex/OpenCode and Claude sessions without Agent have none); it
// returns an empty map so the caller no-ops. Per-file read/parse errors are skipped (fail-open
// per design §5: a single unreadable sidecar must not abort the whole hydrate).
func readClaudeSidechainMeta(subagentsDir string) (map[string]claudeSidechainMeta, error) {
	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make(map[string]claudeSidechainMeta)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		agentID := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		if agentID == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(subagentsDir, name))
		if err != nil {
			continue
		}
		var m claudeSidechainMeta
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		m.agentID = agentID
		out[agentID] = m
	}
	return out, nil
}

// sidechainAgentNode is the in-memory node while building the subagent tree: the .meta.json
// edge fields, the agent's own accumulated content blocks (from its sidechain .jsonl), the
// derived completion status, and any tree-walk diagnostic.
type sidechainAgentNode struct {
	meta       claudeSidechainMeta
	blocks     []ProjectionPart
	status     string
	diagnostic string
}

// buildSidechainAgentBlocks reduces one sidechain .jsonl (agent-<id>.jsonl) into the agent's
// own content blocks via a throwaway child ProjectionReducer, reusing the mainstream Claude
// transcript scanner streamClaudeTranscriptProjectionEventsRangeSeed. Faithful reuse: the same
// row→event mapping that builds mainstream turns builds the subagent's content (thinking/text/
// tools + intra-agent tool_results), so there is no parallel text/tool-accumulation logic. This
// child reducer's output is NEVER committed to the Kernel — it is only read to populate the
// SubagentBlocks of the single subagent part the Kernel anchors (guardrail §3: no second writer).
func buildSidechainAgentBlocks(ctx context.Context, agentPath string) (blocks []ProjectionPart, status string) {
	status = "completed"
	if _, err := os.Stat(agentPath); err != nil {
		return nil, status // .meta.json present but .jsonl missing → empty content, fail-open
	}
	child := NewProjectionReducer()
	seq := 0
	scanErr := streamClaudeTranscriptProjectionEventsRangeSeed(ctx, agentPath, 0, -1, "", func(ev projectionHydrateEvent) bool {
		seq++
		child.Apply(projectionReducerEvent("claude", "sidechain", ev.Event, ev.Data, seq, ""))
		return ctx.Err() == nil
	})
	if scanErr != nil || ctx.Err() != nil {
		return nil, status
	}
	snap, ok := child.Snapshot("claude", "sidechain")
	if !ok {
		return nil, status
	}
	running := false
	failed := false
	for _, turn := range snap.Turns {
		if turn.Assistant != nil {
			blocks = append(blocks, turn.Assistant.Parts...)
		}
		switch turn.Status {
		case "running", "pending":
			running = true
		case "error", "aborted":
			failed = true
		}
	}
	switch {
	case running:
		status = "running"
	case failed:
		status = "failed"
	}
	return blocks, status
}

// computeSidechainDiagnostic walks the parentAgentId chain from a depth≥2 node up to a depth-1
// root, detecting cycle / max_depth / orphan_parent (mirrors BuildClaudeChildStreamGraph's
// depth/cycle/orphan walk with meta-based edges). Depth-1 nodes return "" — their anchor is the
// mainstream turn, resolved (or fail-open-skipped) at emit time, not a diagnostic here.
func computeSidechainDiagnostic(start *sidechainAgentNode, nodes map[string]*sidechainAgentNode) string {
	if start.meta.SpawnDepth <= 1 {
		return ""
	}
	seen := map[string]struct{}{start.meta.agentID: {}}
	cur := start.meta.ParentAgentID
	depth := 0
	for cur != "" {
		depth++
		if depth > maxClaudeSidechainDepth {
			return "max_depth"
		}
		if _, ok := seen[cur]; ok {
			return "cycle"
		}
		seen[cur] = struct{}{}
		parent, ok := nodes[cur]
		if !ok {
			return "orphan_parent"
		}
		cur = parent.meta.ParentAgentID
	}
	return ""
}

// assembleSidechainSubagentParts builds the top-level (depth-1) subagent ProjectionParts with
// depth≥2 children nested recursively into their parent's SubagentBlocks. Each part is built
// once, bottom-up, so no slice is aliased across nesting. Depth-1 parts whose Agent tool_use id
// is not present in mainstreamToolUseTurn are dropped (fail-open: cannot anchor to a mainstream
// turn — current state preserved, no fabrication, guardrail §10).
func assembleSidechainSubagentParts(
	nodes map[string]*sidechainAgentNode,
	mainstreamToolUseTurn map[string]string,
) []ProjectionPart {
	childrenByParent := make(map[string][]string)
	var roots []string
	for aid, n := range nodes {
		n.diagnostic = computeSidechainDiagnostic(n, nodes)
		if n.meta.SpawnDepth <= 1 {
			roots = append(roots, aid)
		} else if n.meta.ParentAgentID != "" {
			childrenByParent[n.meta.ParentAgentID] = append(childrenByParent[n.meta.ParentAgentID], aid)
		}
	}

	var build func(aid string) ProjectionPart
	build = func(aid string) ProjectionPart {
		n := nodes[aid]
		p := ProjectionPart{
			Type:               "subagent",
			AgentID:            n.meta.agentID,
			ParentAgentID:      n.meta.ParentAgentID,
			SpawnToolUseID:     n.meta.ToolUseID,
			SpawnDepth:         n.meta.SpawnDepth,
			SubagentType:       n.meta.AgentType,
			SubagentStatus:     n.status,
			SubagentBlocks:     n.blocks,
			SubagentDiagnostic: n.diagnostic,
		}
		for _, childID := range childrenByParent[aid] {
			p.SubagentBlocks = append(p.SubagentBlocks, build(childID))
		}
		return p
	}

	var topLevel []ProjectionPart
	for _, aid := range roots {
		n := nodes[aid]
		if _, ok := mainstreamToolUseTurn[n.meta.ToolUseID]; !ok {
			slog.Warn("go-bridge: Claude sidechain depth-1 agent has no mainstream Agent tool_use anchor; skipping (fail-open)",
				"agentId", aid, "spawnToolUseId", n.meta.ToolUseID)
			continue
		}
		topLevel = append(topLevel, build(aid))
	}
	return topLevel
}

// produceClaudeSidechainSubagentEvents is the B4 sync-only sidechain source-read pre-pass. It
// reads subagents/agent-*.jsonl + .meta.json from subagentsDir, builds the nested subagent tree,
// and emits one subagent_part hydrate event per depth-1 agent through emit. mainstreamToolUseTurn
// maps a mainstream Agent/Task tool_use id (call_…) → owning mainstream turnId, used to anchor
// each depth-1 subagent to the turn that spawned it. Returns nil after emitting (or when there
// are no sidechain files); returns the read error for the caller to fail-open.
func produceClaudeSidechainSubagentEvents(
	ctx context.Context,
	subagentsDir string,
	mainstreamToolUseTurn map[string]string,
	emit func(projectionHydrateEvent) bool,
) error {
	metaMap, err := readClaudeSidechainMeta(subagentsDir)
	if err != nil {
		return err
	}
	if len(metaMap) == 0 {
		return nil // no sidechain (Codex/OpenCode or Claude session without Agent) — nothing to do
	}
	nodes := make(map[string]*sidechainAgentNode, len(metaMap))
	for agentID, meta := range metaMap {
		blocks, status := buildSidechainAgentBlocks(ctx, filepath.Join(subagentsDir, "agent-"+agentID+".jsonl"))
		nodes[agentID] = &sidechainAgentNode{meta: meta, blocks: blocks, status: status}
	}
	topLevel := assembleSidechainSubagentParts(nodes, mainstreamToolUseTurn)
	for _, part := range topLevel {
		ev := projectionHydrateEvent{
			Event: "subagent_part",
			Data: map[string]interface{}{
				"turnId":             mainstreamToolUseTurn[part.SpawnToolUseID],
				"agentId":            part.AgentID,
				"parentAgentId":      part.ParentAgentID,
				"spawnToolUseId":     part.SpawnToolUseID,
				"spawnDepth":         part.SpawnDepth,
				"subagentType":       part.SubagentType,
				"subagentStatus":     part.SubagentStatus,
				"subagentBlocks":     part.SubagentBlocks,
				"subagentError":      part.SubagentError,
				"subagentDiagnostic": part.SubagentDiagnostic,
			},
		}
		if !emit(ev) {
			return ctx.Err()
		}
	}
	return nil
}

// claudeSubagentsDir derives the per-session sidechain directory from a hydrate source
// descriptor. Claude writes sidechain files to <projectDir>/<sessionUUID>/subagents/ — a
// per-session subdirectory of the project dir (the transcript itself, main + continuations, is
// flat at <projectDir>/<segment>.jsonl, NOT alongside subagents/). projectDir is the directory
// of any transcript segment path; the session UUID is source.Identity. Returns "" when no
// transcript path or no Identity is available (pathless/test configs) so the caller no-ops
// (fail-open, guardrail §10).
func claudeSubagentsDir(source ProjectionSourceDescriptor) string {
	sessionID := strings.TrimSpace(source.Identity)
	if sessionID == "" {
		return ""
	}
	transcriptPath := ""
	if len(source.Segments) > 0 {
		for _, s := range source.Segments {
			if p := strings.TrimSpace(s.Path); p != "" {
				transcriptPath = p
				break
			}
		}
	} else {
		transcriptPath = strings.TrimSpace(source.Path)
	}
	if transcriptPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(transcriptPath), sessionID, "subagents")
}
