package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestMapAndReduceCollaborationModeWholeSnapshot(t *testing.T) {
	effort := "medium"
	name, data, done := mapAgentEvent(core.Event{
		Type:              core.EventSessionCollaborationMode,
		CollaborationMode: &core.SessionCollaborationMode{Mode: "plan", Model: "thread-model", ReasoningEffort: &effort},
	})
	if done || name != "session_collaboration_mode" {
		t.Fatalf("logical = %q done=%v", name, done)
	}
	payload := data.(map[string]interface{})
	r := NewProjectionReducer()
	r.Apply(ev(1, "codex-remote", "thread", name, payload))
	projection, ok := r.Snapshot("codex-remote", "thread")
	if !ok || projection.CollaborationMode == nil || projection.CollaborationMode.Mode != "plan" || projection.CollaborationMode.Model != "thread-model" || projection.CollaborationMode.ReasoningEffort == nil || *projection.CollaborationMode.ReasoningEffort != "medium" {
		t.Fatalf("projection = %+v ok=%v", projection.CollaborationMode, ok)
	}
	patch, ok := r.FlushPatch("codex-remote", "thread")
	if !ok || patch.CollaborationMode == nil || patch.CollaborationMode.Mode != "plan" {
		t.Fatalf("patch = %+v ok=%v", patch.CollaborationMode, ok)
	}
	before := projection.SyncRev
	r.Apply(ev(2, "codex-remote", "thread", name, payload))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if projection.SyncRev != before {
		t.Fatalf("same value must dedupe: %d -> %d", before, projection.SyncRev)
	}
	r.Apply(ev(3, "codex-remote", "thread", name, map[string]interface{}{"mode": "default", "model": "desktop-model"}))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if projection.CollaborationMode.Mode != "default" || projection.CollaborationMode.Model != "desktop-model" || projection.CollaborationMode.ReasoningEffort != nil {
		t.Fatalf("last official settings did not win: %+v", projection.CollaborationMode)
	}
	if patch, ok := r.FlushPatch("codex-remote", "thread"); !ok || patch.CollaborationMode == nil || patch.CollaborationMode.Mode != "default" {
		t.Fatalf("convergence patch = %+v ok=%v", patch.CollaborationMode, ok)
	}
}

func TestReducerCollaborationModeRejectsUnknownVocabulary(t *testing.T) {
	r := NewProjectionReducer()
	r.Apply(ev(1, "codex-remote", "thread", "session_collaboration_mode", map[string]interface{}{"mode": "future", "model": "m"}))
	projection, ok := r.Snapshot("codex-remote", "thread")
	if !ok || projection.CollaborationMode != nil || projection.SyncRev != 0 {
		t.Fatalf("unknown mode mutated projection: %+v ok=%v", projection, ok)
	}
}
