package opencodeweb

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Turn-level official net file diffs (owner round-4, 2026-09-22): upstream
// session/summary.ts computes per-turn net diffs from step-start/step-finish
// snapshots and stores them on the user message (info.summary.diffs); the
// official desktop renders exactly these (session-turn.tsx reads
// message()?.summary?.diffs). Real-sample shape (owner 1.18 desktop server,
// ses_f42fd21d…: 41/41 user messages carry summary; the screenshot turn's
// diffs = 4 files +85 −2, byte-identical to the desktop box).

// realShapeUserSummary mirrors the sanitized real user-message info shape.
func realShapeUserSummary() map[string]any {
	return map[string]any{
		"id":     "msg_user_1",
		"role":   "user",
		"parentID": nil,
		"summary": map[string]any{
			"diffs": []any{
				map[string]any{"file": "CHANGELOG.md", "additions": float64(1), "deletions": float64(1), "status": "modified", "patch": "Index: CHANGELOG.md\n…"},
				map[string]any{"file": "src/a.swift", "additions": float64(2), "deletions": float64(1), "status": "modified", "patch": "Index: src/a.swift\n…"},
				map[string]any{"file": "docs/new.md", "additions": float64(31), "deletions": float64(0), "status": "added", "patch": "Index: docs/new.md\n…"},
				map[string]any{"file": "src/old.swift", "additions": float64(0), "deletions": float64(5), "status": "deleted", "patch": "Index: src/old.swift\n…"},
			},
		},
	}
}

// TestTurnFileChangesFromSummaryRealShape: the screenshot turn's summary maps
// to one entry per file with official counts and status→kind vocabulary; the
// patch is deliberately NOT carried (no message-inline file diff; bounded
// turn-level bytes).
func TestTurnFileChangesFromSummaryRealShape(t *testing.T) {
	changes := turnFileChangesFromSummary(realShapeUserSummary())
	if len(changes) != 4 {
		t.Fatalf("changes = %d, want 4", len(changes))
	}
	want := []struct {
		path       string
		kind       string
		additions  int
		deletions  int
	}{
		{"CHANGELOG.md", "edit", 1, 1},
		{"src/a.swift", "edit", 2, 1},
		{"docs/new.md", "create", 31, 0},
		{"src/old.swift", "delete", 0, 5},
	}
	for i, w := range want {
		if changes[i]["path"] != w.path {
			t.Fatalf("changes[%d].path = %v, want %v", i, changes[i]["path"], w.path)
		}
		if changes[i]["kind"] != w.kind {
			t.Fatalf("changes[%d].kind = %v, want %v", i, changes[i]["kind"], w.kind)
		}
		if got, ok := changes[i]["additions"].(int); !ok || got != w.additions {
			t.Fatalf("changes[%d].additions = %#v, want int %d", i, changes[i]["additions"], w.additions)
		}
		if got, ok := changes[i]["deletions"].(int); !ok || got != w.deletions {
			t.Fatalf("changes[%d].deletions = %#v, want int %d", i, changes[i]["deletions"], w.deletions)
		}
		if _, present := changes[i]["diff"]; present {
			t.Fatalf("changes[%d] carries a patch — turn-level entries must stay patch-free", i)
		}
	}
}

// TestTurnFileChangesFromSummaryAbsentShapes: no summary / empty diffs /
// malformed entries → nil (fail closed, no fabricated rows).
func TestTurnFileChangesFromSummaryAbsentShapes(t *testing.T) {
	if got := turnFileChangesFromSummary(map[string]any{"role": "user"}); got != nil {
		t.Fatalf("no summary = %v, want nil", got)
	}
	if got := turnFileChangesFromSummary(map[string]any{"summary": map[string]any{"diffs": []any{}}}); got != nil {
		t.Fatalf("empty diffs = %v, want nil", got)
	}
	if got := turnFileChangesFromSummary(map[string]any{
		"summary": map[string]any{"diffs": []any{
			map[string]any{"additions": float64(1)}, // no file
			"not-a-map",
		}},
	}); got != nil {
		t.Fatalf("malformed entries = %v, want nil", got)
	}
}

// TestMapRichHistoryEntryUserCarriesTurnFileChanges: the cold-path entry for a
// user message carries the summary as TurnFileChanges; assistant rows do not.
func TestMapRichHistoryEntryUserCarriesTurnFileChanges(t *testing.T) {
	user := map[string]any{
		"info": realShapeUserSummary(),
		"parts": []any{
			map[string]any{"type": "text", "text": "fix the bubble truncation"},
		},
	}
	entry, err := mapRichHistoryEntry(user)
	if err != nil {
		t.Fatalf("mapRichHistoryEntry: %v", err)
	}
	if len(entry.TurnFileChanges) != 4 {
		t.Fatalf("user TurnFileChanges = %d, want 4", len(entry.TurnFileChanges))
	}
	if entry.TurnFileChanges[2]["kind"] != "create" {
		t.Fatalf("added file kind = %v, want create", entry.TurnFileChanges[2]["kind"])
	}

	assistant := map[string]any{
		"info": map[string]any{"id": "msg_a1", "role": "assistant"},
		"parts": []any{
			map[string]any{"type": "text", "text": "done"},
		},
	}
	assistantEntry, err := mapRichHistoryEntry(assistant)
	if err != nil {
		t.Fatalf("mapRichHistoryEntry(assistant): %v", err)
	}
	if assistantEntry.TurnFileChanges != nil {
		t.Fatalf("assistant TurnFileChanges = %v, want nil", assistantEntry.TurnFileChanges)
	}
}

// TestLiveSummaryDiffsEmitAndDedup: a user message.updated carrying a NEW
// summary emits exactly one EventTurnFileChanges (TurnID = user messageID);
// the same summary re-fired emits nothing; a CHANGED summary (the serve
// re-runs summarize after every step-finish) re-emits.
func TestLiveSummaryDiffsEmitAndDedup(t *testing.T) {
	sub := liveToolSubscriber(t)

	info := realShapeUserSummary()
	diffs := sub.summaryDiffsForEmit("msg_user_1", info)
	if diffs == nil {
		t.Fatal("first sight of a summary must return the diffs")
	}
	changes := coreFileChangesFromSummaryDiffs(diffs)
	if len(changes) != 4 {
		t.Fatalf("changes = %d, want 4", len(changes))
	}
	if changes[2].Kind != "create" || changes[2].Additions == nil || *changes[2].Additions != 31 {
		t.Fatalf("docs/new.md change = %+v, want create +31", changes[2])
	}

	// Same summary re-fired → nil (stable kernel revisions between steps).
	if again := sub.summaryDiffsForEmit("msg_user_1", info); again != nil {
		t.Fatal("identical summary must not re-emit")
	}

	// Changed summary (step 2 converged onto the full turn net diff) → re-emit.
	info["summary"].(map[string]any)["diffs"] = append(
		info["summary"].(map[string]any)["diffs"].([]any),
		map[string]any{"file": "src/step2.swift", "additions": float64(7), "deletions": float64(0), "status": "added"},
	)
	if updated := sub.summaryDiffsForEmit("msg_user_1", info); updated == nil {
		t.Fatal("changed summary must re-emit")
	}
}

// TestLiveHandleMessageUpdatedEmitsTurnFileChanges: the full live path — a
// message.updated frame for a user message with info.summary.diffs emits
// EventTurnFileChanges attributed to the user messageID (the same turn
// attribution noteUserPrompt uses).
func TestLiveHandleMessageUpdatedEmitsTurnFileChanges(t *testing.T) {
	sub := liveToolSubscriber(t)
	// message.updated properties carry the message under "info".
	sub.handleMessageUpdated(map[string]any{"info": realShapeUserSummary()}, "ses_live")

	var turnFC *core.Event
	for {
		select {
		case ev := <-sub.events:
			if ev.Type == core.EventTurnFileChanges {
				turnFC = &ev
			}
		default:
			goto drained
		}
	}
drained:
	if turnFC == nil {
		t.Fatal("no EventTurnFileChanges emitted for the user summary frame")
	}
	if turnFC.TurnID != "msg_user_1" {
		t.Fatalf("TurnID = %q, want msg_user_1 (user messageID)", turnFC.TurnID)
	}
	if len(turnFC.FileChanges) != 4 {
		t.Fatalf("FileChanges = %d, want 4", len(turnFC.FileChanges))
	}
	if turnFC.FileChanges[3].Deletions == nil || *turnFC.FileChanges[3].Deletions != 5 {
		t.Fatalf("src/old.swift deletions = %v, want *5", turnFC.FileChanges[3].Deletions)
	}
}

// TestCoreFileChangesFromSummaryDiffsZeroIsRealZero: a legitimate 0 rides as
// a non-nil pointer (real samples: additions=0 ×125, deletions=0 ×355).
func TestCoreFileChangesFromSummaryDiffsZeroIsRealZero(t *testing.T) {
	changes := coreFileChangesFromSummaryDiffs([]any{
		map[string]any{"file": "a.txt", "additions": float64(0), "deletions": float64(0), "status": "modified"},
	})
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	if changes[0].Additions == nil || *changes[0].Additions != 0 {
		t.Fatalf("Additions = %v, want non-nil *0", changes[0].Additions)
	}
	if changes[0].Deletions == nil || *changes[0].Deletions != 0 {
		t.Fatalf("Deletions = %v, want non-nil *0", changes[0].Deletions)
	}
	// Absent counts stay nil (nil≠0).
	partial := coreFileChangesFromSummaryDiffs([]any{
		map[string]any{"file": "b.txt", "status": "modified"},
	})
	if partial[0].Additions != nil || partial[0].Deletions != nil {
		t.Fatalf("absent counts = %v/%v, want nil/nil", partial[0].Additions, partial[0].Deletions)
	}
}
