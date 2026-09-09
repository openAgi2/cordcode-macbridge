package grokbuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeActivityJournal(t *testing.T, home, sessionID, journal string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "cwd", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(journal), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrokSessionActivityUsesDurableTurnTerminal(t *testing.T) {
	home := t.TempDir()
	const sessionID = "session-activity"
	writeActivityJournal(t, home, sessionID,
		`{"method":"session/update","params":{"sessionId":"session-activity","update":{"sessionUpdate":"user_message_chunk"}}}`+"\n"+
			`{"method":"_x.ai/session/update","params":{"sessionId":"session-activity","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn"}}}`+"\n"+
			`{"method":"_x.ai/session/update","params":{"sessionId":"session-activity","update":{"sessionUpdate":"goal_updated","status":"infra_paused"}}}`+"\n")
	a := &Agent{grokHome: home}
	if a.IsSessionActive(context.Background(), sessionID) {
		t.Fatal("turn_completed must make the session idle despite later non-turn updates")
	}
}

func TestGrokSessionActivityDetectsUnfinishedTurnAndKeepsUnknownConservative(t *testing.T) {
	home := t.TempDir()
	const sessionID = "session-active"
	writeActivityJournal(t, home, sessionID,
		`{"method":"_x.ai/session/update","params":{"sessionId":"session-active","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn"}}}`+"\n"+
			`{"method":"session/update","params":{"sessionId":"session-active","update":{"sessionUpdate":"user_message_chunk"}}}`+"\n")
	a := &Agent{grokHome: home}
	if !a.IsSessionActive(context.Background(), sessionID) {
		t.Fatal("user_message_chunk after the last terminal must remain active")
	}
	if !a.IsSessionActive(context.Background(), "missing") {
		t.Fatal("missing activity truth must remain conservatively active")
	}
}
