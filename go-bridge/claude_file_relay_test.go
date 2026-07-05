package gobridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func withFastClaudeFileRelay(t *testing.T) {
	t.Helper()
	prevPoll := claudeFileRelayPollInterval
	prevTTL := claudeFileRelayLiveIdleTTL
	prevDeathMisses := claudeFileRelayProcessDeathMisses
	claudeFileRelayPollInterval = 10 * time.Millisecond
	claudeFileRelayLiveIdleTTL = 300 * time.Millisecond
	claudeFileRelayProcessDeathMisses = 1
	t.Cleanup(func() {
		claudeFileRelayPollInterval = prevPoll
		claudeFileRelayLiveIdleTTL = prevTTL
		claudeFileRelayProcessDeathMisses = prevDeathMisses
	})
}

func writeClaudeFileRelayTranscript(t *testing.T, homeDir, sessionID string, lines ...string) string {
	t.Helper()
	projectDir := filepath.Join(homeDir, ".claude", "projects", "-tmp-claude-file-relay")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendClaudeFileRelayTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func waitClaudeFileRelayStopped(t *testing.T, handlers *Handlers, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("claude file relay still running for %s", sessionID)
}

func startClaudeFileRelayFixture(t *testing.T, sessionID string, live bool) (*Handlers, *fakeAgent, *websocketClient) {
	t.Helper()
	withFastClaudeFileRelay(t)
	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 4242, Live: live},
		},
		alivePIDs: map[int]bool{4242: live},
	}
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: sessionID})
	handlers.startClaudeSessionFileRelay(sessionID, serverConn, "claude")
	return handlers, agent, &websocketClient{conn: clientConn}
}

type websocketClient struct {
	conn interface {
		SetReadDeadline(time.Time) error
		ReadJSON(v interface{}) error
	}
}

func (c *websocketClient) readEvents(t *testing.T, count int) []map[string]any {
	t.Helper()
	messages := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		if err := c.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var payload map[string]any
		if err := c.conn.ReadJSON(&payload); err != nil {
			t.Fatalf("read event %d/%d: %v", i+1, count, err)
		}
		messages = append(messages, payload)
	}
	return messages
}

func TestClaudeFileRelayDeadPIDWithPartialUserExitsIdle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "dead-partial-user"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":"unfinished external prompt"}}`,
	)
	handlers, _, client := startClaudeFileRelayFixture(t, sessionID, false)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "session_state_changed" {
		t.Fatalf("event = %#v, want idle state change", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	if data["state"] != "idle" {
		t.Fatalf("state = %#v, want idle", data["state"])
	}
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}

func TestClaudeFileRelayDeadPIDWithNonFinalAssistantExitsIdle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "dead-non-final-assistant"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"bash"}]}}`,
	)
	handlers, _, client := startClaudeFileRelayFixture(t, sessionID, false)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "session_state_changed" {
		t.Fatalf("event = %#v, want idle state change", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	if data["state"] != "idle" {
		t.Fatalf("state = %#v, want idle", data["state"])
	}
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}

func TestClaudeFileRelayWarmStartUserEmitsTurnStarted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "warm-start-user"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":"external prompt during restart gap"}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event = %#v, want turn_started; messages=%v", got, messages)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":"done","stop_reason":"end_turn"}}`,
	)
	_ = client.readEvents(t, 2)
}

func TestClaudeFileRelayMetaOnlyGrowthDoesNotReemitTurnStarted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "meta-only-growth"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":"external prompt"}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event = %#v, want initial turn_started", got)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"No response requested."}],"stop_reason":"end_turn"}}`,
	)
	time.Sleep(80 * time.Millisecond)
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":"done","stop_reason":"end_turn"}}`,
	)
	messages = client.readEvents(t, 2)
	if got := messages[0]["event"]; got != "turn_completed" {
		t.Fatalf("event after meta-only growth = %#v, want turn_completed (no repeated turn_started); messages=%v", got, messages)
	}
}

func TestClaudeFileRelayLiveIdleSnapshotWatchesNextUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "live-idle-next-user"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"assistant","message":{"role":"assistant","content":"previous done","stop_reason":"end_turn"}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "session_state_changed" {
		t.Fatalf("initial event = %#v, want idle state", got)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"user","message":{"role":"user","content":"new external prompt"}}`,
	)
	messages = client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event after append = %#v, want turn_started", got)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":"done","stop_reason":"end_turn"}}`,
	)
	_ = client.readEvents(t, 2)
}

func TestClaudeFileRelayInterruptInitialScanKeepsWatching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "interrupt-continues-watch"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 2)
	if messages[0]["event"] != "turn_completed" || messages[1]["event"] != "session_state_changed" {
		t.Fatalf("initial events = %v, want turn_completed + idle", messages)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"user","message":{"role":"user","content":"prompt after interrupt"}}`,
	)
	messages = client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event after interrupt append = %#v, want turn_started", got)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":"done","stop_reason":"end_turn"}}`,
	)
	_ = client.readEvents(t, 2)
}

func TestClaudeFileRelayTickUsesCachedPID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "cached-pid"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"assistant","message":{"role":"assistant","content":"previous done","stop_reason":"end_turn"}}`,
	)
	handlers, agent, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1)

	waitClaudeFileRelayStopped(t, handlers, sessionID)
	if agent.liveProcessCalls != 1 {
		t.Fatalf("LiveSessionProcess calls = %d, want 1", agent.liveProcessCalls)
	}
	if agent.processAliveCalls == 0 {
		t.Fatal("IsProcessAlive was not called on poll ticks")
	}
	if agent.lastProcessAliveID != 4242 {
		t.Fatalf("last IsProcessAlive pid = %d, want cached pid 4242", agent.lastProcessAliveID)
	}
}

func TestClaudeFileRelayProcessDeathMidTurnBroadcastsIdleAndExits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "process-death-mid-turn"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":"external prompt"}}`,
	)
	handlers, agent, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event = %#v, want turn_started", got)
	}
	agent.alivePIDs[4242] = false
	messages = client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "session_state_changed" {
		t.Fatalf("event after process death = %#v, want idle state", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	if data["state"] != "idle" {
		t.Fatalf("state after process death = %#v, want idle", data["state"])
	}
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}
