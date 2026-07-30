package gobridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// B4 child-stream (sync-only) — G3 owning-repo test. Verifies the sidechain source-read
// pre-pass against fixture subagents/ files: depth-1 anchors via spawnToolUseId ↔ mainstream
// Agent tool_use id; depth≥2 nests via parentAgentId; non-orphan depth-2 carries no diagnostic;
// depth-1 without a mainstream anchor is dropped (fail-open). Join keys + nesting are the audit's
// (32efbc4) concern: content is missing-not-flattened, multi-level, keyed by agentId/toolUseId.

func writeSidechainAgentFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// minimalClaudeTranscript returns a one-turn user→assistant jsonl that the child reducer turns
// into a single assistant text part. Reuses the real row→event mapping (no parallel logic).
func minimalClaudeTranscript(userText, assistantText, tag string) string {
	return `{"uuid":"u-` + tag + `","type":"user","timestamp":"2026-07-30T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"` + userText + `"}]},"parentUuid":null}` + "\n" +
		`{"uuid":"a-` + tag + `","type":"assistant","timestamp":"2026-07-30T00:00:01.000Z","message":{"id":"m-` + tag + `","role":"assistant","content":[{"type":"text","text":"` + assistantText + `"}],"stop_reason":"end_turn"},"parentUuid":"u-` + tag + `"}` + "\n"
}

func writeSidechainFixture(t *testing.T) string {
	t.Helper()
	sub := filepath.Join(t.TempDir(), "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// depth-1 agent "aaa" — spawned by mainstream Agent tool_use call_abc.
	writeSidechainAgentFile(t, filepath.Join(sub, "agent-aaa.meta.json"),
		`{"agentType":"general-purpose","description":"root sub","toolUseId":"call_abc","spawnDepth":1}`)
	writeSidechainAgentFile(t, filepath.Join(sub, "agent-aaa.jsonl"),
		minimalClaudeTranscript("do work", "aaa did it", "aaa"))
	// depth-2 agent "bbb" — parent is aaa (nested via parentAgentId, non-orphan).
	writeSidechainAgentFile(t, filepath.Join(sub, "agent-bbb.meta.json"),
		`{"agentType":"researcher","description":"nested sub","parentAgentId":"aaa","spawnDepth":2}`)
	writeSidechainAgentFile(t, filepath.Join(sub, "agent-bbb.jsonl"),
		minimalClaudeTranscript("research please", "bbb found it", "bbb"))
	return sub
}

func TestProduceClaudeSidechainSubagentEvents_Depth2NonOrphanCorrectJoinKey(t *testing.T) {
	sub := writeSidechainFixture(t)
	mainstream := map[string]string{"call_abc": "turn-main-1"}

	var got []projectionHydrateEvent
	emit := func(ev projectionHydrateEvent) bool { got = append(got, ev); return true }

	if err := produceClaudeSidechainSubagentEvents(context.Background(), sub, mainstream, emit); err != nil {
		t.Fatalf("produce error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 depth-1 subagent_part event, got %d", len(got))
	}
	ev := got[0]
	if ev.Event != "subagent_part" {
		t.Fatalf("event = %q, want subagent_part", ev.Event)
	}
	// depth-1 join key: agentId + spawnToolUseId, anchored to the mainstream turn.
	if ev.Data["agentId"] != "aaa" {
		t.Fatalf("agentId = %v, want aaa", ev.Data["agentId"])
	}
	if ev.Data["spawnToolUseId"] != "call_abc" {
		t.Fatalf("spawnToolUseId = %v, want call_abc", ev.Data["spawnToolUseId"])
	}
	if ev.Data["spawnDepth"] != 1 {
		t.Fatalf("spawnDepth = %v, want 1", ev.Data["spawnDepth"])
	}
	if ev.Data["turnId"] != "turn-main-1" {
		t.Fatalf("turnId = %v, want turn-main-1 (mainstream anchor)", ev.Data["turnId"])
	}
	if ev.Data["subagentType"] != "general-purpose" {
		t.Fatalf("subagentType = %v, want general-purpose", ev.Data["subagentType"])
	}

	// subagentBlocks: aaa's own content + nested depth-2 bbb.
	blocks, ok := ev.Data["subagentBlocks"].([]ProjectionPart)
	if !ok {
		t.Fatalf("subagentBlocks type = %T, want []ProjectionPart", ev.Data["subagentBlocks"])
	}
	var nested *ProjectionPart
	for i := range blocks {
		if blocks[i].AgentID == "bbb" {
			nested = &blocks[i]
			break
		}
	}
	if nested == nil {
		t.Fatalf("missing nested depth-2 bbb in %d blocks", len(blocks))
	}
	if nested.ParentAgentID != "aaa" {
		t.Fatalf("nested parentAgentId = %q, want aaa", nested.ParentAgentID)
	}
	if nested.SpawnDepth != 2 {
		t.Fatalf("nested spawnDepth = %d, want 2", nested.SpawnDepth)
	}
	if nested.SubagentType != "researcher" {
		t.Fatalf("nested subagentType = %q, want researcher", nested.SubagentType)
	}
	// parent aaa is in the tree → non-orphan → empty diagnostic.
	if nested.SubagentDiagnostic != "" {
		t.Fatalf("nested diagnostic = %q, want empty (parent present, non-orphan)", nested.SubagentDiagnostic)
	}
}

func TestProduceClaudeSidechainSubagentEvents_Depth1WithoutMainstreamAnchorDropped(t *testing.T) {
	sub := writeSidechainFixture(t)
	// No mainstream anchor for call_abc → depth-1 aaa cannot be anchored → fail-open (dropped).
	mainstream := map[string]string{}

	var got []projectionHydrateEvent
	emit := func(ev projectionHydrateEvent) bool { got = append(got, ev); return true }

	if err := produceClaudeSidechainSubagentEvents(context.Background(), sub, mainstream, emit); err != nil {
		t.Fatalf("produce error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events (depth-1 anchor missing, fail-open), got %d", len(got))
	}
}

func TestProduceClaudeSidechainSubagentEvents_NoSidechainDirIsNoop(t *testing.T) {
	// Missing/empty subagents dir (Codex/OpenCode or Claude session without Agent) → no error, no events.
	empty := t.TempDir()
	var got []projectionHydrateEvent
	emit := func(ev projectionHydrateEvent) bool { got = append(got, ev); return true }
	if err := produceClaudeSidechainSubagentEvents(context.Background(), empty, map[string]string{"call_x": "t"}, emit); err != nil {
		t.Fatalf("produce error on empty dir: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events for empty sidechain dir, got %d", len(got))
	}
}
