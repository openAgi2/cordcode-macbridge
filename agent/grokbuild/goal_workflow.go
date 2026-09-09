package grokbuild

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const grokGoalPlanWriterDescription = "goal plan writer"

type grokWorkflowMemberState struct {
	seq     int
	label   string
	phase   string
	childID string
	status  string
	turnID  string
}

// grokUpdateState folds Grok's incremental goal/subagent notifications into
// the existing whole-value goal/workflow projection contract used by dsh-web.
// A state belongs to exactly one event source (stdio, leader, or file tailer).
type grokUpdateState struct {
	mu      sync.Mutex
	goal    core.GoalEvent
	members map[string]*grokWorkflowMemberState
	order   []string
	// goalChanged is rotated after every authoritative goal update. Unlike the
	// per-turn dispatcher signal, it survives a prompt terminal arriving before
	// the subsequent goal_paused/goal_cleared notification.
	goalChanged chan struct{}
}

func newGrokUpdateState() *grokUpdateState {
	return &grokUpdateState{members: make(map[string]*grokWorkflowMemberState), goalChanged: make(chan struct{})}
}

func (s *grokUpdateState) goalUpdateSignal() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goalChanged == nil {
		s.goalChanged = make(chan struct{})
	}
	return s.goalChanged
}

func (s *grokUpdateState) goalPhase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goal.Phase
}

func grokGoalPhase(status string) (string, *core.GoalBlockedReason, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "running":
		return "active", nil, true
	case "user_paused", "paused":
		return "paused", nil, true
	case "completed", "complete", "achieved":
		return "complete", nil, true
	case "blocked", "budget_limited", "budget_exceeded", "failed", "infra_paused":
		return "blocked", &core.GoalBlockedReason{Code: strings.ToLower(strings.TrimSpace(status)), Message: strings.TrimSpace(status)}, true
	default:
		return "", nil, false
	}
}

// Grok currently reports planner/model failures as user_paused in some goal
// paths, with the actual failure preserved only in pause_message. Do not turn
// every user pause into a failure: promote only messages that explicitly carry
// a failed/error fact, and retain the backend text verbatim for diagnosis.
func promoteGrokPausedFailure(phase string, blocked *core.GoalBlockedReason, pauseMessage, lastEvent string) (string, *core.GoalBlockedReason) {
	if phase != "paused" {
		return phase, blocked
	}
	message := strings.TrimSpace(pauseMessage)
	lowerMessage := strings.ToLower(message)
	if message == "" || (!strings.Contains(lowerMessage, "failed") && !strings.Contains(lowerMessage, "error")) {
		return phase, blocked
	}
	code := strings.ToLower(strings.TrimSpace(lastEvent))
	if !strings.Contains(code, "fail") && !strings.Contains(code, "error") {
		if strings.HasPrefix(lowerMessage, "planning failed") {
			code = "planning_failed"
		} else {
			code = "goal_failed"
		}
	}
	return "blocked", &core.GoalBlockedReason{Code: code, Message: message}
}

// The goal harness pauses instead of completing when its hidden evaluator
// cannot produce the required JSON after both attempts. Give that terminal,
// non-user-actionable failure a stable code so clients can collapse the goal
// chrome without also hiding auth, planning, budget, or genuine blockers.
func normalizeGrokBlockedReason(blocked *core.GoalBlockedReason, pauseMessage string) {
	if blocked == nil {
		return
	}
	if message := strings.TrimSpace(pauseMessage); message != "" {
		blocked.Message = message
	}
	if blocked.Code == "infra_paused" && strings.HasPrefix(
		strings.ToLower(blocked.Message),
		"goal evaluation failed after a bounded retry:",
	) {
		blocked.Code = "goal_evaluation_failed"
	}
}

func grokGoalEvent(p sessionUpdatePayload, revision int64) *core.GoalEvent {
	if strings.TrimSpace(p.GoalID) == "" || strings.TrimSpace(p.Objective) == "" {
		return nil
	}
	phase, blocked, ok := grokGoalPhase(p.Status)
	if !ok {
		return nil
	}
	phase, blocked = promoteGrokPausedFailure(phase, blocked, p.PauseMessage, p.LastEvent)
	normalizeGrokBlockedReason(blocked, p.PauseMessage)
	return &core.GoalEvent{
		ID:                  p.GoalID,
		Revision:            revision,
		Objective:           p.Objective,
		Phase:               phase,
		BlockedReason:       blocked,
		VerifyingCompletion: p.VerifyingCompletion,
	}
}

func (s *grokUpdateState) observeGoal(goal core.GoalEvent) (core.WorkflowRunEvent, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goal.ID != "" && s.goal.ID != goal.ID {
		s.members = make(map[string]*grokWorkflowMemberState)
		s.order = nil
	}
	s.goal = goal
	if s.goalChanged == nil {
		s.goalChanged = make(chan struct{})
	} else {
		close(s.goalChanged)
		s.goalChanged = make(chan struct{})
	}
	return s.workflowSnapshotLocked("")
}

func grokGoalObjectiveFromReminder(text string) (string, bool) {
	const prefix = "<system-reminder>\nA goal has been set: "
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(text, prefix)
	if end := strings.Index(rest, "\n\nYou are working directly on this goal"); end >= 0 {
		rest = rest[:end]
	}
	objective := strings.TrimSpace(rest)
	return objective, objective != ""
}

func loadGrokGoalSnapshot(sessionDir string) *core.GoalEvent {
	raw, err := os.ReadFile(filepath.Join(sessionDir, "goal", "state.json"))
	if err != nil {
		return nil
	}
	var state struct {
		GoalID       string `json:"goal_id"`
		Objective    string `json:"objective"`
		Status       string `json:"status"`
		PauseMessage string `json:"pause_message"`
		History      []struct {
			Timestamp string `json:"timestamp"`
			Event     string `json:"event"`
			Detail    string `json:"detail"`
		} `json:"history"`
	}
	if json.Unmarshal(raw, &state) != nil || state.GoalID == "" || state.Objective == "" {
		return nil
	}
	phase, blocked, ok := grokGoalPhase(state.Status)
	if !ok {
		return nil
	}
	lastEvent := ""
	if n := len(state.History); n > 0 {
		lastEvent = state.History[n-1].Event
	}
	phase, blocked = promoteGrokPausedFailure(phase, blocked, state.PauseMessage, lastEvent)
	normalizeGrokBlockedReason(blocked, state.PauseMessage)
	revision := int64(0)
	if n := len(state.History); n > 0 {
		last := state.History[n-1]
		if at, err := time.Parse(time.RFC3339Nano, last.Timestamp); err == nil {
			revision = at.UnixMilli()
		}
		if blocked != nil && strings.TrimSpace(state.PauseMessage) == "" {
			blocked.Code = last.Event
			if strings.TrimSpace(last.Detail) != "" {
				blocked.Message = last.Detail
			}
		}
	}
	return &core.GoalEvent{ID: state.GoalID, Revision: revision, Objective: state.Objective, Phase: phase, BlockedReason: blocked}
}

// insertWorkflowPartAtSpawnAnchor mirrors the live card position: the first
// subagent_spawned notifications arrive right after the contiguous
// spawn_subagent tool batch (journal: intro text → spawn tool_calls ×N →
// subagent_spawned ×N → follow-up text), so live projection upserts the card
// between the spawn batch and the follow-up text. Anchor the cold card at the
// same spot — after the entry's first contiguous spawn_subagent batch. Entries
// without a spawn batch (recovered bodies without tools) keep the historic
// front prepend.
func insertWorkflowPartAtSpawnAnchor(parts []map[string]any, card map[string]any) []map[string]any {
	anchor := -1
	for k, part := range parts {
		if isSpawnSubagentToolPart(part) {
			anchor = k
		} else if anchor >= 0 {
			break
		}
	}
	if anchor < 0 {
		return append([]map[string]any{card}, parts...)
	}
	out := make([]map[string]any, 0, len(parts)+1)
	out = append(out, parts[:anchor+1]...)
	out = append(out, card)
	out = append(out, parts[anchor+1:]...)
	return out
}

func isSpawnSubagentToolPart(part map[string]any) bool {
	if strings.TrimSpace(fmt.Sprint(part["type"])) != "tool" {
		return false
	}
	step, _ := part["step"].(map[string]any)
	if step == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(fmt.Sprint(step["toolName"])))
	return strings.Contains(name, "spawn") && strings.Contains(name, "subagent")
}

func workflowPart(snapshot core.WorkflowRunEvent) map[string]any {	phases := make([]map[string]any, 0, len(snapshot.Phases))
	for _, phase := range snapshot.Phases {
		members := make([]map[string]any, 0, len(phase.Members))
		for _, member := range phase.Members {
			members = append(members, map[string]any{"seq": member.Seq, "label": member.Label, "childSessionId": member.ChildSessionID, "status": member.Status})
		}
		phases = append(phases, map[string]any{"phase": phase.Phase, "members": members})
	}
	return map[string]any{"type": "workflow", "workflowId": snapshot.RunID, "workflowName": snapshot.Name, "workflowStatus": snapshot.Status, "workflowPhases": phases}
}

func decorateGrokGoalHistory(sessionDir, sessionID string, entries []core.RichHistoryEntry) []core.RichHistoryEntry {
	updates, err := os.Open(filepath.Join(sessionDir, "updates.jsonl"))
	if err == nil {
		defer updates.Close()
		state := newGrokUpdateState()
		latest := make(map[string]core.WorkflowRunEvent)
		var runOrder []string
		sc := bufio.NewScanner(updates)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			var head struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(line, &head) != nil || !isSessionUpdateMethod(head.Method) {
				continue
			}
			for _, ev := range convertSessionUpdateWithState(extractParams(line), sessionID, state) {
				if ev.Type == core.EventWorkflowRun && ev.WorkflowRun != nil {
					if _, seen := latest[ev.WorkflowRun.RunID]; !seen {
						runOrder = append(runOrder, ev.WorkflowRun.RunID)
					}
					latest[ev.WorkflowRun.RunID] = *ev.WorkflowRun
				}
			}
		}
		var recovered []core.RichHistoryEntry
		for _, runID := range runOrder {
			snapshot := latest[runID]
			matched := false
			for i := range entries {
				if entries[i].Role != "system" || len(entries[i].Parts) == 0 || fmt.Sprint(entries[i].Parts[0]["type"]) != "command" || fmt.Sprint(entries[i].Parts[0]["name"]) != "goal" || strings.TrimSpace(fmt.Sprint(entries[i].Parts[0]["args"])) != strings.TrimSpace(snapshot.Name) {
					continue
				}
				matched = true
				attached := false
				for j := i + 1; j < len(entries); j++ {
					if entries[j].Role == "assistant" {
						entries[j].Parts = insertWorkflowPartAtSpawnAnchor(entries[j].Parts, workflowPart(snapshot))
						attached = true
						break
					}
					// Never attach a workflow to a later user/command turn.
					if entries[j].Role == "user" || entries[j].Role == "system" {
						break
					}
				}
				if !attached {
					workflowEntry := core.RichHistoryEntry{
						ID: sessionID + ":workflow:" + runID, Role: "assistant",
						Parts: []map[string]any{workflowPart(snapshot)},
					}
					entries = append(entries, core.RichHistoryEntry{})
					copy(entries[i+2:], entries[i+1:])
					entries[i+1] = workflowEntry
				}
				break
			}
			if !matched {
				// Manual compaction rewrites chat_history.jsonl to the surviving
				// context, but the official updates journal retains every goal and
				// subagent lifecycle. Rebuild the missing timeline pair from that
				// durable source so reconnecting does not erase prior goal cards.
				commandID := sessionID + ":goal-command:" + runID
				recovered = append(recovered,
					core.RichHistoryEntry{ID: commandID, Role: "system", Parts: []map[string]any{{
						"type": "command", "commandId": commandID, "name": "goal", "args": snapshot.Name,
						"kind": "success", "line": "/goal " + snapshot.Name,
					}}},
					core.RichHistoryEntry{ID: sessionID + ":workflow:" + runID, Role: "assistant", Parts: []map[string]any{workflowPart(snapshot)}},
				)
			}
		}
		entries = append(recovered, entries...)
	}
	goalPart := map[string]any{"type": "goal", "phase": "none"}
	if goal := loadGrokGoalSnapshot(sessionDir); goal != nil {
		goalPart = map[string]any{"type": "goal", "id": goal.ID, "revision": goal.Revision, "objective": goal.Objective, "phase": goal.Phase}
		if goal.BlockedReason != nil {
			goalPart["blockedReason"] = map[string]any{"code": goal.BlockedReason.Code, "message": goal.BlockedReason.Message}
		}
		return append(entries, core.RichHistoryEntry{ID: sessionID + ":goal", Role: "system", Parts: []map[string]any{goalPart}})
	}
	if st, err := os.Stat(filepath.Join(sessionDir, "goal")); err == nil && st.IsDir() {
		return append(entries, core.RichHistoryEntry{ID: sessionID + ":goal", Role: "system", Parts: []map[string]any{goalPart}})
	}
	return entries
}

func isInternalGrokSubagent(description string) bool {
	return strings.EqualFold(strings.TrimSpace(description), grokGoalPlanWriterDescription)
}

func grokWorkflowMemberPresentation(description string) (label, phase string) {
	if isInternalGrokSubagent(description) {
		return "制定执行计划", "规划"
	}
	return strings.TrimSpace(description), "执行"
}

func (s *grokUpdateState) observeSubagent(p sessionUpdatePayload) (core.WorkflowRunEvent, string, bool) {
	id := strings.TrimSpace(p.SubagentID)
	if id == "" {
		id = strings.TrimSpace(p.ChildSessionID)
	}
	if id == "" {
		return core.WorkflowRunEvent{}, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.members[id]
	if m == nil {
		// A finish can be the first frame after reconnect. Without the spawn
		// description there is no honest user-facing member label, so wait for
		// cold hydration instead of inventing one.
		if p.SessionUpdate == "subagent_finished" || strings.TrimSpace(p.Description) == "" {
			return core.WorkflowRunEvent{}, "", false
		}
		label, phase := grokWorkflowMemberPresentation(p.Description)
		m = &grokWorkflowMemberState{seq: len(s.order) + 1, label: label, phase: phase, childID: p.ChildSessionID, turnID: p.ParentPromptID}
		s.members[id] = m
		s.order = append(s.order, id)
	}
	if p.ParentPromptID != "" {
		m.turnID = p.ParentPromptID
	}
	if p.SessionUpdate == "subagent_finished" {
		switch p.Status {
		case "completed", "failed", "cancelled":
			m.status = p.Status
		default:
			m.status = "failed"
		}
	} else {
		m.status = "running"
	}

	return s.workflowSnapshotLocked(m.turnID)
}

func (s *grokUpdateState) workflowSnapshotLocked(fallbackTurnID string) (core.WorkflowRunEvent, string, bool) {
	if len(s.order) == 0 {
		return core.WorkflowRunEvent{}, "", false
	}
	runID := s.goal.ID
	if runID == "" {
		runID = fallbackTurnID
	}
	if runID == "" {
		return core.WorkflowRunEvent{}, "", false
	}
	status := core.WorkflowStatusCompleted
	hasFailed, hasCancelled := false, false
	phaseOrder := make([]string, 0, 2)
	membersByPhase := make(map[string][]core.WorkflowRunMember)
	turnID := fallbackTurnID
	for _, memberID := range s.order {
		member := s.members[memberID]
		if member.turnID != "" && turnID == "" {
			turnID = member.turnID
		}
		if member.status == "running" || member.status == "" {
			status = core.WorkflowStatusRunning
		}
		if member.status == "failed" {
			hasFailed = true
		}
		if member.status == "cancelled" {
			hasCancelled = true
		}
		if _, ok := membersByPhase[member.phase]; !ok {
			phaseOrder = append(phaseOrder, member.phase)
		}
		membersByPhase[member.phase] = append(membersByPhase[member.phase], core.WorkflowRunMember{Seq: member.seq, Label: member.label, ChildSessionID: member.childID, Status: member.status})
	}
	// A goal can spawn more workers after all currently-known members settle.
	// The goal phase is therefore the workflow terminal authority; member
	// completion alone must not flash a false completed card between phases.
	switch s.goal.Phase {
	case "active", "paused":
		status = core.WorkflowStatusRunning
	case "blocked":
		status = core.WorkflowStatusFailed
	case "complete":
		status = core.WorkflowStatusCompleted
	default:
		if status != core.WorkflowStatusRunning {
			if hasFailed {
				status = core.WorkflowStatusFailed
			} else if hasCancelled {
				status = core.WorkflowStatusCancelled
			}
		}
	}
	name := s.goal.Objective
	phases := make([]core.WorkflowRunPhase, 0, len(phaseOrder))
	for _, phaseName := range phaseOrder {
		phase := phaseName
		phases = append(phases, core.WorkflowRunPhase{Phase: &phase, Members: membersByPhase[phaseName]})
	}
	return core.WorkflowRunEvent{RunID: runID, Name: name, Status: status, Phases: phases}, turnID, true
}

func goalCreatedUpdate(params json.RawMessage) (string, bool) {
	var outer struct {
		Update sessionUpdatePayload `json:"update"`
	}
	if json.Unmarshal(params, &outer) != nil || outer.Update.SessionUpdate != "goal_updated" || outer.Update.LastEvent != "goal_created" {
		return "", false
	}
	objective := strings.TrimSpace(outer.Update.Objective)
	return objective, objective != ""
}

func isGoalUpdated(params json.RawMessage) bool {
	var outer struct {
		Update sessionUpdatePayload `json:"update"`
	}
	return json.Unmarshal(params, &outer) == nil && outer.Update.SessionUpdate == "goal_updated"
}
