package dshweb

import (
	"encoding/json"
	"testing"
)

func TestNormalizePermissionMode(t *testing.T) {
	cases := map[string]string{
		"":                    permissionModeWorkspaceWrite,
		"workspace-write":     permissionModeWorkspaceWrite,
		"Workspace Write":     permissionModeWorkspaceWrite,
		"read-only":           permissionModeReadOnly,
		"readonly":            permissionModeReadOnly,
		"danger-full-access":  permissionModeDangerFullAccess,
		"full-access":         permissionModeDangerFullAccess,
		"fullaccess":          permissionModeDangerFullAccess,
		"unknown":             permissionModeWorkspaceWrite,
	}
	for in, want := range cases {
		if got := normalizePermissionMode(in); got != want {
			t.Fatalf("normalizePermissionMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPermissionModesMatchOfficialWebLabels(t *testing.T) {
	a := &Agent{}
	modes := a.PermissionModes()
	if len(modes) != 3 {
		t.Fatalf("modes = %d, want 3", len(modes))
	}
	if modes[0].Key != permissionModeReadOnly || modes[0].Name != "Read Only" {
		t.Fatalf("read-only = %+v", modes[0])
	}
	if modes[1].Key != permissionModeWorkspaceWrite || modes[1].Name != "Workspace Write" {
		t.Fatalf("workspace-write = %+v", modes[1])
	}
	if modes[2].Key != permissionModeDangerFullAccess || modes[2].Name != "Full access" {
		t.Fatalf("full access = %+v", modes[2])
	}
	if a.GetMode() != permissionModeWorkspaceWrite {
		t.Fatalf("default GetMode = %q", a.GetMode())
	}
	a.SetMode("read-only")
	if a.GetMode() != permissionModeReadOnly {
		t.Fatalf("after set GetMode = %q", a.GetMode())
	}
}

func TestApplySessionPermissionUsesCommandsExecuteNotPrompt(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["settings.update"] = fakeRPCResponse{value: map[string]any{}}
	f.handlers["commands/execute"] = fakeRPCResponse{value: map[string]any{
		"commandId": "cmd-1",
		"result":    map[string]any{"kind": "success", "text": "preset read-only"},
	}}

	if err := a.applySessionPermission("sess-1", permissionModeReadOnly); err != nil {
		t.Fatalf("applySessionPermission: %v", err)
	}
	if n := len(methodCalls(f, "session.prompt")); n != 0 {
		t.Fatalf("session.prompt called %d times; slash command must not become a user turn", n)
	}
	calls := methodCalls(f, "commands/execute")
	if len(calls) != 1 {
		t.Fatalf("commands/execute calls = %d, want 1", len(calls))
	}
	var req commandsExecuteRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatalf("decode execute payload: %v", err)
	}
	if req.Args.AgentID != "sess-1" {
		t.Fatalf("agentId = %q, want sess-1", req.Args.AgentID)
	}
	if req.Args.Line != "/permission read-only" {
		t.Fatalf("line = %q, want /permission read-only", req.Args.Line)
	}
}

func TestSetModeDoesNotPromptTheModel(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	a.lastActiveSessionID = "sess-live"
	f.handlers["settings.update"] = fakeRPCResponse{value: map[string]any{}}
	f.handlers["commands/execute"] = fakeRPCResponse{value: map[string]any{
		"commandId": "cmd-2",
		"result":    map[string]any{"kind": "success", "text": "preset workspace-write"},
	}}

	a.SetMode("workspace-write")
	if n := len(methodCalls(f, "session.prompt")); n != 0 {
		t.Fatalf("SetMode used session.prompt %d times", n)
	}
	if len(methodCalls(f, "commands/execute")) != 1 {
		t.Fatalf("SetMode commands/execute = %d, want 1", len(methodCalls(f, "commands/execute")))
	}
	if len(methodCalls(f, "settings.update")) != 1 {
		t.Fatalf("SetMode settings.update = %d, want 1", len(methodCalls(f, "settings.update")))
	}
}
