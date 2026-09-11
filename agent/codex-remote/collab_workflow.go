package codexremote

// Codex collabAgentToolCall does not carry the runId used by DeepSeek
// Harness' tool-workflow protocol. Its item id identifies one operation
// (spawn/wait/close), not one user-visible workflow. Fold the operations in an
// owning turn into one keyed workflow snapshot, matching the existing DSH
// workflowFold contract: first spawn anchors the card, later operations update
// the same members in place.

import (
	"strconv"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const codexCollabRunPrefix = "codex-collab:"

type codexCollabMemberState struct {
	seq     int
	label   string
	childID string
	status  string
}

type codexCollabWorkflowFold struct {
	runID   string
	members []codexCollabMemberState
	byChild map[string]int
}

func newCodexCollabWorkflowFold(turnID string) *codexCollabWorkflowFold {
	return &codexCollabWorkflowFold{
		runID:   codexCollabRunPrefix + turnID,
		byChild: map[string]int{},
	}
}

func normalizeCollabTool(tool string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tool)), "_", "")
}

func collabMemberLabel(prompt string, seq int) string {
	label := strings.TrimSpace(prompt)
	if runes := []rune(label); len(runes) > 80 {
		label = string(runes[:80])
	}
	if label == "" {
		return "Agent-" + strconv.Itoa(seq)
	}
	return label
}

// observe returns false until a real spawned child exists. wait/close calls
// for agents created by older turns therefore cannot pollute the current card.
func (f *codexCollabWorkflowFold) observe(item remoteThreadItem) bool {
	if f == nil || f.runID == "" {
		return false
	}
	tool := normalizeCollabTool(item.CollabTool)
	spawn := tool == "spawnagent"
	changed := false
	if spawn {
		for _, receiver := range item.CollabReceivers {
			childID := strings.TrimSpace(receiver)
			if childID == "" {
				continue
			}
			if _, exists := f.byChild[childID]; exists {
				continue
			}
			seq := len(f.members) + 1
			initialStatus := core.WorkflowStatusRunning
			if item.CollabStatus == "failed" {
				initialStatus = core.WorkflowStatusFailed
			}
			f.byChild[childID] = len(f.members)
			f.members = append(f.members, codexCollabMemberState{
				seq: seq, label: collabMemberLabel(item.CollabPrompt, seq),
				childID: childID, status: initialStatus,
			})
			changed = true
		}
	}

	for childID, state := range item.CollabAgentStates {
		idx, exists := f.byChild[strings.TrimSpace(childID)]
		if !exists {
			// Only spawn establishes membership. This is important for Goal
			// turns that first close stale agents left by an earlier run.
			continue
		}
		status := remoteCollabMemberStatus(state.Status, f.members[idx].status)
		if status != f.members[idx].status {
			f.members[idx].status = status
			changed = true
		}
	}
	return changed
}

func (f *codexCollabWorkflowFold) snapshot() (core.WorkflowRunEvent, bool) {
	if f == nil || len(f.members) == 0 {
		return core.WorkflowRunEvent{}, false
	}
	status := core.WorkflowStatusCompleted
	hasFailed, hasCancelled, hasInterrupted := false, false, false
	members := make([]core.WorkflowRunMember, 0, len(f.members))
	for _, member := range f.members {
		memberStatus := member.status
		if memberStatus == "" {
			memberStatus = core.WorkflowStatusRunning
		}
		switch memberStatus {
		case core.WorkflowStatusRunning, "pending":
			status = core.WorkflowStatusRunning
		case core.WorkflowStatusFailed:
			hasFailed = true
		case core.WorkflowStatusCancelled:
			hasCancelled = true
		case core.WorkflowStatusInterrupted:
			hasInterrupted = true
		}
		members = append(members, core.WorkflowRunMember{
			Seq: member.seq, Label: member.label,
			ChildSessionID: member.childID, Status: memberStatus,
		})
	}
	if status != core.WorkflowStatusRunning {
		switch {
		case hasFailed:
			status = core.WorkflowStatusFailed
		case hasCancelled:
			status = core.WorkflowStatusCancelled
		case hasInterrupted:
			status = core.WorkflowStatusInterrupted
		}
	}
	return core.WorkflowRunEvent{
		RunID: f.runID, Name: "Subagents", Status: status,
		Phases: []core.WorkflowRunPhase{{Phase: nil, Members: members}},
	}, true
}

func (c *LiveCodec) foldCollabWorkflow(params remoteItemNotification, item remoteThreadItem) []core.Event {
	if params.ThreadID == "" || params.TurnID == "" || item.ID == "" {
		return nil
	}
	key := params.ThreadID + "\x00" + params.TurnID
	c.mu.Lock()
	fold := c.collabByTurn[key]
	if fold == nil {
		fold = newCodexCollabWorkflowFold(params.TurnID)
		c.collabByTurn[key] = fold
	}
	changed := fold.observe(item)
	snapshot, ok := fold.snapshot()
	c.mu.Unlock()
	if !changed || !ok {
		return nil
	}
	return []core.Event{{
		Type: core.EventWorkflowRun, SessionID: params.ThreadID,
		ThreadID: params.ThreadID, TurnID: params.TurnID, WorkflowRun: &snapshot,
	}}
}
