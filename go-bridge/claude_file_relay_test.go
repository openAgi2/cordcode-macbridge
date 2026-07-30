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

func eventNames(messages []map[string]any) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		out = append(out, m["event"])
	}
	return out
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
	// Process not live still watches; live-idle TTL eventually exits with no growth.
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}

func TestClaudeFileRelayInheritedCursorConsumesPreStartAppend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withFastClaudeFileRelay(t)
	const sessionID = "inherited-cursor-append"
	user := `{"type":"user","uuid":"cursor-user","message":{"id":"cursor-turn","role":"user","content":"prompt"}}`
	path := writeClaudeFileRelayTranscript(t, home, sessionID, user)
	inheritedCursor := int64(len(user) + 1)
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"cursor-assistant","message":{"id":"cursor-response","role":"assistant","content":[{"type":"text","text":"after cut"}],"stop_reason":"end_turn"}}`,
	)

	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 4242, Live: true},
		},
		alivePIDs: map[int]bool{4242: true},
	}
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: sessionID})
	handlers.startClaudeSessionFileRelayAt(sessionID, serverConn, "claude", &inheritedCursor)
	client := &websocketClient{conn: clientConn}

	messages := client.readEvents(t, 4)
	names := eventNames(messages)
	if names[0] != "session_state_changed" ||
		names[1] != "text_delta" ||
		names[2] != "turn_completed" ||
		names[3] != "session_state_changed" {
		t.Fatalf("events = %v, want initial idle then inherited-cursor assistant exactly once", names)
	}
	data, _ := messages[1]["data"].(map[string]any)
	if data["delta"] != "after cut" {
		t.Fatalf("delta = %#v, want after cut", data["delta"])
	}
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
		`{"type":"user","uuid":"warm-user-1","message":{"role":"user","content":"external prompt during restart gap"}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event = %#v, want turn_started; messages=%v", got, messages)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"warm-asst-1","message":{"id":"msg_warm","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	messages = client.readEvents(t, 3) // text_delta + turn_completed + idle
	if messages[0]["event"] != "text_delta" || messages[1]["event"] != "turn_completed" || messages[2]["event"] != "session_state_changed" {
		t.Fatalf("completion events = %v", eventNames(messages))
	}
}

func TestClaudeFileRelayMetaOnlyGrowthDoesNotReemitTurnStarted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "meta-only-growth"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"meta-user-1","message":{"role":"user","content":"external prompt"}}`,
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
		`{"type":"assistant","uuid":"meta-asst-1","message":{"id":"msg_meta","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	messages = client.readEvents(t, 3)
	// Phase 1：assistant "done" 现在会先发 text_delta（content streaming），再 turn_completed + idle。
	// 关键校验：meta-only growth 没有重复触发 turn_started。
	if messages[0]["event"] != "text_delta" || messages[1]["event"] != "turn_completed" || messages[2]["event"] != "session_state_changed" {
		t.Fatalf("events after meta-only growth = %v, want [text_delta, turn_completed, idle] (no repeated turn_started)", eventNames(messages))
	}
	textData, _ := messages[0]["data"].(map[string]any)
	if textData["itemId"] != "meta-user-1" {
		t.Fatalf("meta growth text_delta itemId = %#v, want meta-user-1", textData["itemId"])
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
		`{"type":"user","uuid":"live-user-2","message":{"role":"user","content":"new external prompt"}}`,
	)
	messages = client.readEvents(t, 3) // turn_started + user_message + running
	if messages[0]["event"] != "turn_started" || messages[1]["event"] != "user_message" || messages[2]["event"] != "session_state_changed" {
		t.Fatalf("events after user append = %v, want [turn_started user_message session_state_changed]", eventNames(messages))
	}
	userData, _ := messages[1]["data"].(map[string]any)
	if userData["turnId"] != "live-user-2" || userData["itemId"] != "live-user-2" || userData["text"] != "new external prompt" {
		t.Fatalf("user_message data = %#v", userData)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"live-asst-2","message":{"id":"msg_live_2","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	messages = client.readEvents(t, 3) // text_delta + turn_completed + idle
	if messages[0]["event"] != "text_delta" || messages[1]["event"] != "turn_completed" || messages[2]["event"] != "session_state_changed" {
		t.Fatalf("events after assistant append = %v, want [text_delta turn_completed session_state_changed]", eventNames(messages))
	}
	textData, _ := messages[0]["data"].(map[string]any)
	if textData["itemId"] != "live-user-2" || textData["delta"] != "done" {
		t.Fatalf("text_delta must carry user turn itemId: %#v", textData)
	}
	compData, _ := messages[1]["data"].(map[string]any)
	if compData["turnId"] != "live-user-2" {
		t.Fatalf("turn_completed turnId = %#v, want live-user-2", compData["turnId"])
	}
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
		`{"type":"user","uuid":"after-interrupt-user","message":{"role":"user","content":"prompt after interrupt"}}`,
	)
	messages = client.readEvents(t, 3)
	if messages[0]["event"] != "turn_started" || messages[1]["event"] != "user_message" {
		t.Fatalf("events after interrupt append = %v, want turn_started+user_message...", eventNames(messages))
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"after-interrupt-asst","message":{"id":"msg_ai","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	_ = client.readEvents(t, 3)
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

	// Process is still live: file-relay must keep watching (not exit on live-idle TTL),
	// and poll ticks must reuse the cached PID via IsProcessAlive.
	deadline := time.Now().Add(2 * time.Second)
	for agent.processAliveCalls == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if agent.liveProcessCalls != 1 {
		t.Fatalf("LiveSessionProcess calls = %d, want 1", agent.liveProcessCalls)
	}
	if agent.processAliveCalls == 0 {
		t.Fatal("IsProcessAlive was not called on poll ticks")
	}
	if agent.lastProcessAliveID != 4242 {
		t.Fatalf("last IsProcessAlive pid = %d, want cached pid 4242", agent.lastProcessAliveID)
	}
	if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
		t.Fatal("file relay exited while process is still live")
	}

	// Process death is the real exit path when transcript stays quiet.
	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}

func TestClaudeFileRelayProcessDeathMidTurnBroadcastsIdleAndExits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "process-death-mid-turn"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"death-user-1","message":{"role":"user","content":"external prompt"}}`,
	)
	handlers, agent, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event = %#v, want turn_started", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	if data["turnId"] != "death-user-1" {
		t.Fatalf("warm-start turnId = %#v, want death-user-1", data["turnId"])
	}
	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()
	messages = client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "session_state_changed" {
		t.Fatalf("event after process death = %#v, want idle state", got)
	}
	idleData, _ := messages[0]["data"].(map[string]any)
	if idleData["state"] != "idle" {
		t.Fatalf("state after process death = %#v, want idle", idleData["state"])
	}
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}

// Process not live at open must still watch transcript growth (owner A2 multi-session):
// open idle B → Mac later sends message 3 → relay must emit identity-bearing frames.
func TestClaudeFileRelayProcessNotLiveStillWatchesGrowth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "not-live-then-growth"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"assistant","uuid":"prev","message":{"id":"msg_prev","role":"assistant","content":[{"type":"text","text":"old"}],"stop_reason":"end_turn"}}`,
	)
	handlers, agent, client := startClaudeFileRelayFixture(t, sessionID, false)
	_ = client.readEvents(t, 1) // idle
	if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
		t.Fatal("relay exited immediately when process not live; must keep watching")
	}
	// Later the process becomes live and a new user turn is appended.
	agent.liveProcesses[sessionID] = core.LiveSessionProcess{SessionID: sessionID, PID: 4242, Live: true}
	agent.processMu.Lock()
	agent.alivePIDs[4242] = true
	agent.processMu.Unlock()
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"user","uuid":"growth-user","message":{"role":"user","content":"message 3"}}`,
	)
	// Drain until we see the identity-bearing user path (may include a late-bind idle/running frame).
	var sawTurnStarted, sawUser bool
	var userData map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !(sawTurnStarted && sawUser) {
		batch := client.readEvents(t, 1)
		switch batch[0]["event"] {
		case "turn_started":
			sawTurnStarted = true
			d, _ := batch[0]["data"].(map[string]any)
			if d["turnId"] != "growth-user" {
				t.Fatalf("turn_started turnId = %#v", d["turnId"])
			}
		case "user_message":
			sawUser = true
			userData, _ = batch[0]["data"].(map[string]any)
		}
	}
	if !sawTurnStarted || !sawUser {
		t.Fatalf("did not see turn_started+user_message from process-not-live growth (started=%v user=%v)", sawTurnStarted, sawUser)
	}
	if userData["turnId"] != "growth-user" || userData["text"] != "message 3" {
		t.Fatalf("user_message = %#v", userData)
	}
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"growth-asst","message":{"id":"msg_g","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}}`,
	)
	var sawText, sawCompleted bool
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !(sawText && sawCompleted) {
		batch := client.readEvents(t, 1)
		switch batch[0]["event"] {
		case "text_delta":
			sawText = true
			d, _ := batch[0]["data"].(map[string]any)
			if d["itemId"] != "growth-user" || d["delta"] != "ok" {
				t.Fatalf("text_delta = %#v", d)
			}
		case "turn_completed":
			sawCompleted = true
		}
	}
	if !sawText || !sawCompleted {
		t.Fatalf("completion missing text=%v completed=%v", sawText, sawCompleted)
	}
}
