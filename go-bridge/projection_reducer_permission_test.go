package gobridge

import "testing"

func TestReducerPermissionRequestProjectsPendingToolAndRequiresAction(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "dsh-web", "s1", "permission_request", map[string]interface{}{
		"requestId": "appr-write",
		"toolName":  "write",
	}))

	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.Execution.Phase != "requires_action" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("execution = %+v, want requires_action/T1", proj.Execution)
	}
	if len(proj.Turns) == 0 || proj.Turns[0].Assistant == nil {
		t.Fatalf("missing assistant: %+v", proj.Turns)
	}
	found := false
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "appr-write" {
			found = true
			if p.ToolName != "write" || p.ToolStatus != "pending" || !p.RequiresPermissionConfirmation {
				t.Fatalf("permission tool part = %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("missing permission tool part: %+v", proj.Turns[0].Assistant.Parts)
	}
}
