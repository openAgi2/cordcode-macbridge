package gobridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectCodexTranscriptTaskState(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rollout-session.jsonl")
	h := &Handlers{}

	writeTranscript := func(t *testing.T, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
	}

	writeTranscript(t, `{"timestamp":"2026-07-01T07:37:47.626Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`+"\n")
	if got := h.detectCodexTranscriptTaskState(path); got != "running" {
		t.Fatalf("state after task_started = %q, want running", got)
	}

	writeTranscript(t,
		`{"timestamp":"2026-07-01T07:37:47.626Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`+"\n"+
			`{"timestamp":"2026-07-01T07:39:17.071Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`+"\n",
	)
	if got := h.detectCodexTranscriptTaskState(path); got != "idle" {
		t.Fatalf("state after task_complete = %q, want idle", got)
	}
}
