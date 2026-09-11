package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// P5.7: the Codex collab cold part (produced by agent/codex-remote
// mapRemoteHistoryItem) must hydrate into a workflow part on the owning turn
// through the same dsh cold pipeline (hydrateWorkflowEventsFromPart →
// workflow_run → reducer upsert by workflowId).
func TestCollabColdPartHydratesInBridge(t *testing.T) {
	part := map[string]any{
		"type": "workflow", "itemId": "call-1",
		"workflowId": "call-1", "workflowName": "写故事", "workflowStatus": "completed",
		"workflowPhases": []map[string]any{
			{"phase": nil, "members": []map[string]any{
				{"seq": 1, "label": "Agent-1", "childSessionId": "agent-a", "status": "completed"},
			}},
		},
	}
	events := hydrateWorkflowEventsFromPart(part, "turn", -1)
	if len(events) != 1 || events[0].Event != "workflow_run" {
		t.Fatalf("hydrate events = %+v", events)
	}
	if events[0].Data["workflowId"] != "call-1" || events[0].Data["turnId"] != "turn" {
		t.Fatalf("hydrate data = %+v", events[0].Data)
	}

	reducer := NewProjectionReducer()
	seed := []projectionHydrateEvent{
		{Event: "user_message", Data: map[string]any{"itemId": "u1", "turnId": "turn", "text": "goal 任务"}},
		{Event: "text_delta", Data: map[string]any{"itemId": "a1", "turnId": "turn", "delta": "启动子代理"}},
		events[0],
		{Event: "turn_completed", Data: map[string]any{"turnId": "turn", "done": true, "reason": "official_turn_status"}},
	}
	for i, ev := range seed {
		reducer.Apply(projectionReducerEvent("codex-remote", "th", ev.Event, ev.Data, i+1, ""))
	}
	proj, ok := reducer.Snapshot("codex-remote", "th")
	if !ok || len(proj.Turns) == 0 {
		t.Fatalf("snapshot ok=%v turns=%d", ok, len(proj.Turns))
	}
	var card *ProjectionPart
	for i := range proj.Turns {
		if proj.Turns[i].TurnID != "turn" || proj.Turns[i].Assistant == nil {
			continue
		}
		for j := range proj.Turns[i].Assistant.Parts {
			if proj.Turns[i].Assistant.Parts[j].Type == "workflow" {
				card = &proj.Turns[i].Assistant.Parts[j]
			}
		}
	}
	if card == nil {
		t.Fatal("workflow part missing from owning assistant turn")
	}
	if card.WorkflowID != "call-1" || card.ItemID != "call-1" || card.WorkflowName != "写故事" || card.WorkflowStatus != "completed" {
		t.Fatalf("card header = %+v", card)
	}
	if len(card.WorkflowPhases) != 1 || card.WorkflowPhases[0].Phase != nil || len(card.WorkflowPhases[0].Members) != 1 {
		t.Fatalf("card phases = %+v", card.WorkflowPhases)
	}
	member := card.WorkflowPhases[0].Members[0]
	if member.Label != "Agent-1" || member.ChildSessionID != "agent-a" || member.Status != "completed" {
		t.Fatalf("member = %+v", member)
	}
}

// The live Codex fold emits core.EventWorkflowRun; the bridge must map it to
// the same workflow_run wire event the dsh adapters use (no new event type).
func TestCollabLiveEventMapsToWorkflowRunWire(t *testing.T) {
	agentEvent := core.Event{
		Type: core.EventWorkflowRun, SessionID: "th", ThreadID: "th", TurnID: "turn",
		WorkflowRun: &core.WorkflowRunEvent{
			RunID: "call-1", Name: "写故事", Status: "running",
			Phases: []core.WorkflowRunPhase{{Phase: nil, Members: []core.WorkflowRunMember{
				{Seq: 1, Label: "Agent-1", ChildSessionID: "agent-a", Status: "running"},
			}}},
		},
	}
	ev := core.Event{Type: core.EventWorkflowRun, WorkflowRun: agentEvent.WorkflowRun, TurnID: agentEvent.TurnID}
	name, rawData, done := mapAgentEvent(ev)
	if name != "workflow_run" || done {
		t.Fatalf("mapped = %q done=%v", name, done)
	}
	data, dataOk := rawData.(map[string]interface{})
	if !dataOk {
		t.Fatalf("wire data type = %T", rawData)
	}
	if data["workflowId"] != "call-1" || data["turnId"] != "turn" || data["itemId"] != "call-1" {
		t.Fatalf("wire data = %+v", data)
	}
}

func TestWorkflowCardItemIDAllowsDetailClassify(t *testing.T) {
	reducer := NewProjectionReducer()
	reducer.Apply(projectionReducerEvent("codex-remote", "th", "user_message", map[string]any{
		"itemId": "u1", "turnId": "turn", "text": "goal",
	}, 1, ""))
	reducer.Apply(projectionReducerEvent("codex-remote", "th", "workflow_run", map[string]any{
		"turnId": "turn", "workflowId": "codex-collab:turn", "workflowName": "Subagents",
		"workflowStatus": "running", "workflowPhases": []map[string]any{
			{"phase": nil, "members": []map[string]any{
				{"seq": 1, "label": "Agent-1", "childSessionId": "a", "status": "running"},
			}},
		},
	}, 2, ""))
	proj, ok := reducer.Snapshot("codex-remote", "th")
	if !ok || len(proj.Turns) == 0 || proj.Turns[0].Assistant == nil {
		t.Fatalf("snapshot = %+v", proj)
	}
	entries, err := classifyDetailParts(proj.Turns[0].Assistant.Parts)
	if err != nil {
		t.Fatalf("classifyDetailParts: %v", err)
	}
	if len(entries) == 0 || entries[0].ItemID != "codex-collab:turn" {
		t.Fatalf("entries = %+v", entries)
	}
}
