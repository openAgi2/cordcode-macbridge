package gobridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readyIdleClaudeProjection(t *testing.T, h *Handlers, sessionID string) {
	t.Helper()
	source := ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/" + sessionID + ".jsonl", Cursor: 1}
	if _, err := h.projectionKernel.BeginHydrateTransaction("claude", sessionID, source, false, false, false); err != nil {
		t.Fatalf("begin hydrate: %v", err)
	}
	events := []struct {
		name string
		data map[string]interface{}
	}{
		{"turn_started", map[string]interface{}{"turnId": "cold-turn"}},
		{"user_message", map[string]interface{}{"turnId": "cold-turn", "itemId": "cold-user", "text": "hello"}},
		{"text_delta", map[string]interface{}{"turnId": "cold-turn", "itemId": "cold-turn", "delta": "done"}},
		{"turn_completed", map[string]interface{}{"turnId": "cold-turn"}},
	}
	for _, event := range events {
		// turn_started is persist-only and intentionally does not advance rev;
		// the following content and terminal events prove the batch was accepted.
		_ = h.projectionKernel.ApplyHydrateEvent("claude", sessionID, "test", event.name, event.data)
	}
	h.projectionKernel.MarkHydrateSourceIngestComplete("claude", sessionID)
	if err := h.projectionKernel.WaitHydrateCommitReady(context.Background(), "claude", sessionID); err != nil {
		t.Fatalf("hydrate ready: %v", err)
	}
	if _, err := h.projectionKernel.CommitHydrateTransaction("claude", sessionID); err != nil {
		t.Fatalf("commit hydrate: %v", err)
	}
}

func TestExternalExecutionStateDoesNotCreateOrOverrideSourceTurn(t *testing.T) {
	h := NewHandlers()
	const sid = "external-execution"

	if h.projectionKernel.ApplyExternalExecutionState("claude", sid, "running") {
		t.Fatal("external execution state must not create reducer state")
	}
	readyIdleClaudeProjection(t, h, sid)

	if !h.projectionKernel.ApplyExternalExecutionState("claude", sid, "running") {
		t.Fatal("idle projection should accept external running state")
	}
	projection, ok := h.projectionKernel.Snapshot("claude", sid)
	if !ok || projection.Execution.Phase != "running" || projection.Execution.ActiveTurnID != "" {
		t.Fatalf("external running execution = %#v, ok=%v", projection.Execution, ok)
	}
	if h.projectionKernel.ApplyExternalExecutionState("claude", sid, "running") {
		t.Fatal("identical external running state must be idempotent")
	}
	if !h.projectionKernel.ApplyExternalExecutionState("claude", sid, "idle") {
		t.Fatal("identity-less external running state should clear")
	}

	h.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "claude", SessionID: sid, Event: "turn_started",
		Data: map[string]interface{}{"turnId": "real-turn"},
	})
	h.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "claude", SessionID: sid, Event: "text_delta",
		Data: map[string]interface{}{"turnId": "real-turn", "itemId": "real-turn", "delta": "x"},
	})
	projection, ok = h.projectionKernel.Snapshot("claude", sid)
	if !ok || projection.Execution.ActiveTurnID != "real-turn" {
		t.Fatalf("source turn not active: %#v, ok=%v", projection.Execution, ok)
	}
	if h.projectionKernel.ApplyExternalExecutionState("claude", sid, "idle") {
		t.Fatal("external idle must not settle a source-proven active turn")
	}
}

func TestClaudeStubPollerSyncsReadyProjectionExecution(t *testing.T) {
	h := NewHandlers()
	const sid = "stub-projection"
	conn := newPublisherCaptureConn(nil)
	h.broadcaster.Subscribe(conn, SubscriptionKey{BackendID: "claude", SessionID: sid})
	h.eventPublisher.SetConnSyncV2(conn, true)
	readyIdleClaudeProjection(t, h, sid)

	dir := t.TempDir()
	stub := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/tmp","status":"busy"}`, os.Getpid(), sid)
	if err := os.WriteFile(filepath.Join(dir, "stub.json"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &claudeStubStatusPoller{
		h:             h,
		backendID:     "claude",
		sessionsDir:   dir,
		lastPublished: nil,
		stop:          make(chan struct{}),
	}

	p.pollOnce()
	patches := waitForProjectionPatches(t, conn, 1)
	busyPatch := patches[0].Data.(ProjectionPatch)
	if busyPatch.Execution == nil || busyPatch.Execution.Phase != "running" {
		t.Fatalf("busy stub projection patch = %#v", busyPatch.Execution)
	}
	projection, ok := h.projectionKernel.Snapshot("claude", sid)
	if !ok || projection.Execution.Phase != "running" {
		t.Fatalf("busy stub projection = %#v, ok=%v", projection.Execution, ok)
	}

	idle := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/tmp","status":"idle"}`, os.Getpid(), sid)
	if err := os.WriteFile(filepath.Join(dir, "stub.json"), []byte(idle), 0o644); err != nil {
		t.Fatal(err)
	}
	p.pollOnce()
	patches = waitForProjectionPatches(t, conn, 2)
	idlePatch := patches[1].Data.(ProjectionPatch)
	if idlePatch.Execution == nil || idlePatch.Execution.Phase != "idle" {
		t.Fatalf("idle stub projection patch = %#v", idlePatch.Execution)
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(projectionPatchFrames(conn.snapshot())); got != 2 {
		t.Fatalf("unchanged stub status republished projection patches: %d", got)
	}
}
