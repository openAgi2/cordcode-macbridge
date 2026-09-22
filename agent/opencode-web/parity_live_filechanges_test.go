package opencodeweb

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Parity plan v2.12 R34 live 补齐（2026-09-22 owner round-3 批准）：live 终态
// EventToolResult 携带官方 per-tool fileChanges。准入证据：owner 1.18 desktop
// 服务器真实 history 拉取（2026-09-22，ses_f42fd21d…）976 tool parts 中 960 带
// state.metadata，202/202 edit filediff 字段完整（file/patch/additions/deletions
// 均为 JSON number）；上游 stream.transport.ts 证实 message.part.updated 携带
// properties.part（与 history API 同一 part 对象）。fixture 形状取自该真实样本
// （路径/补丁已替换为无害值）。

// realShapeEditPart mirrors the sanitized real completed-edit part shape.
func realShapeEditPart() map[string]any {
	return map[string]any{
		"type": "tool",
		"id":   "part_edit_1",
		"tool": "edit",
		"state": map[string]any{
			"status": "completed",
			"input":  map[string]any{"filePath": "src/main.go", "oldString": "a", "newString": "b"},
			"output": "Edit applied successfully.",
			"metadata": map[string]any{
				"diagnostics": map[string]any{},
				"diff":       "Index: src/main.go\n--- src/main.go\n+++ src/main.go\n@@ -1,1 +1,2 @@\n-a\n+b\n",
				"filediff": map[string]any{
					"file":       "src/main.go",
					"patch":      "--- src/main.go\n+++ src/main.go\n@@ -1,1 +1,2 @@\n-a\n+b\n",
					"additions":  float64(2),
					"deletions":  float64(1),
				},
				"truncated": false,
			},
			"time":        map[string]any{"start": float64(1789000000000), "end": float64(1789000001200)},
			"durationMs":  float64(1200),
		},
	}
}

func liveToolSubscriber(t *testing.T) *sseSubscriber {
	t.Helper()
	a, err := New(map[string]any{
		"work_dir":         "/tmp/proj",
		"opencode_web_url": "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })
	sub := newSSESubscriber(context.Background(), agent, newClient("http://127.0.0.1:1", "", ""))
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}

// drainToolEvents reads the events a single handleToolPart call produced.
func drainToolEvents(t *testing.T, sub *sseSubscriber) []core.Event {
	t.Helper()
	var events []core.Event
	for {
		select {
		case ev := <-sub.events:
			events = append(events, ev)
		default:
			return events
		}
	}
}

// TestLiveToolResultCarriesFileChanges: the terminal EventToolResult of a
// completed edit carries the official filediff as core.FileChange — the live
// counterpart of the cold path's step["fileChanges"].
func TestLiveToolResultCarriesFileChanges(t *testing.T) {
	sub := liveToolSubscriber(t)
	sub.handleToolPart(realShapeEditPart(), "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (tool_use + tool_result)", len(events))
	}
	if events[0].Type != core.EventToolUse {
		t.Fatalf("first event type = %v, want tool_use", events[0].Type)
	}
	res := events[1]
	if res.Type != core.EventToolResult {
		t.Fatalf("second event type = %v, want tool_result", res.Type)
	}
	if len(res.FileChanges) != 1 {
		t.Fatalf("FileChanges = %d, want 1", len(res.FileChanges))
	}
	fc := res.FileChanges[0]
	if fc.Path != "src/main.go" {
		t.Fatalf("Path = %q, want src/main.go", fc.Path)
	}
	if fc.Kind != "edit" {
		t.Fatalf("Kind = %q, want edit", fc.Kind)
	}
	if fc.Diff == "" {
		t.Fatal("Diff empty, want the official patch")
	}
	if fc.Additions == nil || *fc.Additions != 2 {
		t.Fatalf("Additions = %v, want *2", fc.Additions)
	}
	if fc.Deletions == nil || *fc.Deletions != 1 {
		t.Fatalf("Deletions = %v, want *1", fc.Deletions)
	}
}

// TestLiveToolResultZeroCountsAreRealZeros: a legitimate 0 must survive as a
// non-nil pointer to 0 (real samples: additions=0 ×125, deletions=0 ×355).
func TestLiveToolResultZeroCountsAreRealZeros(t *testing.T) {
	part := realShapeEditPart()
	filediff := part["state"].(map[string]any)["metadata"].(map[string]any)["filediff"].(map[string]any)
	filediff["additions"] = float64(0)
	filediff["deletions"] = float64(0)

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 || events[1].Type != core.EventToolResult {
		t.Fatalf("events = %v, want tool_use + tool_result", events)
	}
	fc := events[1].FileChanges[0]
	if fc.Additions == nil || *fc.Additions != 0 {
		t.Fatalf("Additions = %v, want non-nil *0", fc.Additions)
	}
	if fc.Deletions == nil || *fc.Deletions != 0 {
		t.Fatalf("Deletions = %v, want non-nil *0", fc.Deletions)
	}
}

// TestLiveToolResultRunningEmitsNoResult: a running edit (no metadata yet)
// emits only EventToolUse — FileChanges attach at the terminal frame only.
func TestLiveToolResultRunningEmitsNoResult(t *testing.T) {
	part := realShapeEditPart()
	part["state"].(map[string]any)["status"] = "running"

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 1 || events[0].Type != core.EventToolUse {
		t.Fatalf("events = %v, want a single tool_use", events)
	}
}

// TestLiveToolResultWriteToolNoFileChanges: the real write-tool metadata shape
// (diagnostics/filepath/exists — upstream write.ts returns no diff) must NOT
// produce fileChanges (fail closed, no fabricated stats).
func TestLiveToolResultWriteToolNoFileChanges(t *testing.T) {
	part := map[string]any{
		"type": "tool",
		"id":   "part_write_1",
		"tool": "write",
		"state": map[string]any{
			"status": "completed",
			"output": "Wrote file successfully.",
			"metadata": map[string]any{
				"diagnostics": map[string]any{},
				"filepath":   "src/new.go",
				"exists":     false,
				"truncated":  false,
			},
		},
	}

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 || events[1].Type != core.EventToolResult {
		t.Fatalf("events = %v, want tool_use + tool_result", events)
	}
	if len(events[1].FileChanges) != 0 {
		t.Fatalf("FileChanges = %d, want 0 (write has no official diff)", len(events[1].FileChanges))
	}
}

// TestLiveToolResultMalformedFilediffOmitted: filediff without a file path is
// dropped, not guessed (fail closed).
func TestLiveToolResultMalformedFilediffOmitted(t *testing.T) {
	part := realShapeEditPart()
	filediff := part["state"].(map[string]any)["metadata"].(map[string]any)["filediff"].(map[string]any)
	delete(filediff, "file")

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 || events[1].Type != core.EventToolResult {
		t.Fatalf("events = %v, want tool_use + tool_result", events)
	}
	if len(events[1].FileChanges) != 0 {
		t.Fatalf("FileChanges = %d, want 0 (malformed filediff dropped)", len(events[1].FileChanges))
	}
}

// TestLiveToolResultNestedToolObject: the verified live fallback shape (tool
// nested object carrying toolName + state) maps identically.
func TestLiveToolResultNestedToolObject(t *testing.T) {
	flat := realShapeEditPart()
	state := flat["state"].(map[string]any)
	part := map[string]any{
		"type": "tool",
		"id":   "part_edit_2",
		"tool": map[string]any{
			"toolName": "edit",
			"state":    state,
		},
	}

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 || events[1].Type != core.EventToolResult {
		t.Fatalf("events = %v, want tool_use + tool_result", events)
	}
	if len(events[1].FileChanges) != 1 || events[1].FileChanges[0].Path != "src/main.go" {
		t.Fatalf("FileChanges = %v, want the nested-shape edit change", events[1].FileChanges)
	}
}

// TestLiveToolResultApplyPatchFilesMultiEntry: metadata.files (apply_patch)
// maps one fileChange per entry with official kinds — same as the cold path.
func TestLiveToolResultApplyPatchFilesMultiEntry(t *testing.T) {
	part := map[string]any{
		"type": "tool",
		"id":   "part_patch_1",
		"tool": "apply_patch",
		"state": map[string]any{
			"status": "completed",
			"metadata": map[string]any{
				"files": []any{
					map[string]any{"filePath": "a.go", "type": "update", "patch": "+a", "additions": float64(1), "deletions": float64(1)},
					map[string]any{"filePath": "b.go", "type": "add", "patch": "+b", "additions": float64(3), "deletions": float64(0)},
				},
			},
		},
	}

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 || events[1].Type != core.EventToolResult {
		t.Fatalf("events = %v, want tool_use + tool_result", events)
	}
	changes := events[1].FileChanges
	if len(changes) != 2 {
		t.Fatalf("FileChanges = %d, want 2", len(changes))
	}
	if changes[0].Kind != "update" || changes[1].Kind != "add" {
		t.Fatalf("kinds = %q/%q, want update/add", changes[0].Kind, changes[1].Kind)
	}
	if changes[1].Additions == nil || *changes[1].Additions != 3 {
		t.Fatalf("b.go additions = %v, want *3", changes[1].Additions)
	}
	if changes[1].Deletions == nil || *changes[1].Deletions != 0 {
		t.Fatalf("b.go deletions = %v, want non-nil *0", changes[1].Deletions)
	}
}

// TestLiveToolResultFailedStatusNoMetadata: a failed tool (no metadata) emits
// the terminal result without fileChanges.
func TestLiveToolResultFailedStatusNoMetadata(t *testing.T) {
	part := map[string]any{
		"type": "tool",
		"id":   "part_edit_3",
		"tool": "edit",
		"state": map[string]any{
			"status": "error",
			"error":  "File src/missing.go not found",
		},
	}

	sub := liveToolSubscriber(t)
	sub.handleToolPart(part, "ses_live", "msg_1")

	events := drainToolEvents(t, sub)
	if len(events) != 2 || events[1].Type != core.EventToolResult {
		t.Fatalf("events = %v, want tool_use + tool_result", events)
	}
	if events[1].ToolStatus != "failed" {
		t.Fatalf("ToolStatus = %q, want failed", events[1].ToolStatus)
	}
	if len(events[1].FileChanges) != 0 {
		t.Fatalf("FileChanges = %d, want 0", len(events[1].FileChanges))
	}
}

// TestCoreFileChangesMatchesWireCarrier: the two carriers of the single parse
// must agree — coreFileChangesFromToolState (live) and
// fileChangesFromToolState (history step maps) describe identical data.
func TestCoreFileChangesMatchesWireCarrier(t *testing.T) {
	state := realShapeEditPart()["state"].(map[string]any)

	wire := fileChangesFromToolState(state)
	coreChanges := coreFileChangesFromToolState(state)
	if len(wire) != 1 || len(coreChanges) != 1 {
		t.Fatalf("wire = %d, core = %d, want 1/1", len(wire), len(coreChanges))
	}
	if wire[0]["path"] != coreChanges[0].Path ||
		wire[0]["kind"] != coreChanges[0].Kind ||
		wire[0]["diff"] != coreChanges[0].Diff {
		t.Fatalf("carriers disagree: wire=%v core=%v", wire[0], coreChanges[0])
	}
	if wireAdd, _ := wire[0]["additions"].(int); coreChanges[0].Additions == nil || wireAdd != *coreChanges[0].Additions {
		t.Fatalf("additions disagree: wire=%v core=%v", wire[0]["additions"], coreChanges[0].Additions)
	}
	if wireDel, _ := wire[0]["deletions"].(int); coreChanges[0].Deletions == nil || wireDel != *coreChanges[0].Deletions {
		t.Fatalf("deletions disagree: wire=%v core=%v", wire[0]["deletions"], coreChanges[0].Deletions)
	}

	// Absent metadata → both carriers empty.
	if got := coreFileChangesFromToolState(map[string]any{"status": "completed"}); len(got) != 0 {
		t.Fatalf("absent metadata core changes = %v, want empty", got)
	}
}
