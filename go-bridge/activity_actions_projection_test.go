package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestActivityActionsSurviveLiveFinishAndHydrate(t *testing.T) {
	actions := []any{map[string]any{"kind": "read", "command": "sed a.go", "name": "a.go", "path": "/tmp/a.go"}}
	name, data, done := mapAgentEvent(core.Event{
		Type:            core.EventToolUse,
		ToolName:        "Bash",
		ToolInput:       "sed a.go",
		RequestID:       "cmd1",
		ActivityActions: []core.ActivityAction{{Kind: "read", Command: "sed a.go", Name: "a.go", Path: "/tmp/a.go"}},
	})
	if done || name != "tool_started" {
		t.Fatalf("started name=%s done=%v", name, done)
	}
	payload, _ := data.(map[string]interface{})
	if _, ok := payload["activityActions"]; !ok {
		t.Fatalf("tool_started dropped activityActions: %#v", payload)
	}

	r := newTestReducer()
	r.Apply(ev(1, "codex-remote", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex-remote", "s1", "tool_started", map[string]interface{}{
		"itemId": "cmd1", "toolName": "Bash", "activityActions": actions,
	}))
	r.Apply(ev(3, "codex-remote", "s1", "tool_finished", map[string]interface{}{
		"itemId": "cmd1", "toolName": "Bash", "toolStatus": "completed", "toolResult": "ok",
	}))
	proj, ok := r.Snapshot("codex-remote", "s1")
	if !ok || len(proj.Turns) != 1 || proj.Turns[0].Assistant == nil || len(proj.Turns[0].Assistant.Parts) != 1 {
		t.Fatalf("projection=%+v", proj)
	}
	if proj.Turns[0].Assistant.Parts[0].ActivityActions == nil {
		t.Fatal("completed frame cleared started activityActions")
	}

	events := hydrateToolEventsFromStep(map[string]any{
		"id": "cmd1", "toolName": "Bash", "status": "completed", "activityActions": actions,
	})
	if len(events) != 2 || events[0].Data["activityActions"] == nil || events[1].Data["activityActions"] == nil {
		t.Fatalf("hydrate dropped activityActions: %+v", events)
	}
}
