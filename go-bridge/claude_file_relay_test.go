package gobridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
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

func unsubscribeAllClaudeFileRelayClients(handlers *Handlers) {
	handlers.broadcaster.mu.Lock()
	connections := make([]Connection, 0, len(handlers.broadcaster.allConns))
	for conn := range handlers.broadcaster.allConns {
		connections = append(connections, conn)
	}
	handlers.broadcaster.mu.Unlock()
	for _, conn := range connections {
		handlers.broadcaster.UnsubscribeAll(conn)
	}
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
	// Process not live still watches while subscribed. Once the client leaves, the
	// live-idle TTL may reclaim the watcher.
	unsubscribeAllClaudeFileRelayClients(handlers)
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
	unsubscribeAllClaudeFileRelayClients(handlers)
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
	for {
		calls, _ := agent.ProcessAliveStats()
		if calls > 0 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if agent.LiveProcessCallCount() != 1 {
		t.Fatalf("LiveSessionProcess calls = %d, want 1", agent.LiveProcessCallCount())
	}
	calls, lastPID := agent.ProcessAliveStats()
	if calls == 0 {
		t.Fatal("IsProcessAlive was not called on poll ticks")
	}
	if lastPID != 4242 {
		t.Fatalf("last IsProcessAlive pid = %d, want cached pid 4242", lastPID)
	}
	if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
		t.Fatal("file relay exited while process is still live")
	}

	// Once the client leaves, process death is an exit path when transcript stays quiet.
	unsubscribeAllClaudeFileRelayClients(handlers)
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
	unsubscribeAllClaudeFileRelayClients(handlers)
	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()
	waitClaudeFileRelayStopped(t, handlers, sessionID)
}

func TestClaudeFileRelaySubscribedSessionSurvivesProcessDeathAndProjectsLaterQuestion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "subscribed-process-replacement-question"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"assistant","uuid":"previous-assistant","message":{"id":"msg_previous","role":"assistant","content":[{"type":"text","text":"old"}],"stop_reason":"end_turn"}}`,
	)
	handlers, agent, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1) // initial idle

	// The worker that was live when iOS opened the session exits. The subscription
	// remains, so the transcript watcher must outlive this specific PID.
	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
		t.Fatal("subscribed Claude transcript watcher exited with the old process")
	}

	appendClaudeFileRelayTranscript(t, path,
		`{"type":"user","uuid":"question-user","message":{"role":"user","content":"ask before changing the build script"}}`,
		`{"type":"assistant","uuid":"question-assistant","parentUuid":"question-user","timestamp":"2026-08-02T08:16:12.813Z","message":{"id":"msg_question","role":"assistant","content":[{"type":"tool_use","id":"call-question","name":"AskUserQuestion","input":{"questions":[{"header":"构建失败策略","multiSelect":false,"options":[{"label":"自动重试 3 次"},{"label":"立即失败并报告"}],"question":"构建失败时，你希望脚本如何处理？"}]}}]}}`,
	)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		projection, _ := handlers.projectionKernel.reducer.Snapshot("claude", sessionID)
		for _, turn := range projection.Turns {
			if turn.Assistant == nil {
				continue
			}
			for _, part := range turn.Assistant.Parts {
				if part.Type == "user_input" &&
					part.UserInputInteractionID == claudecode.DeriveStructuredUserInputInteractionID("call-question") &&
					part.UserInputStatus == "pending" && !part.UserInputCanRespond {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	projection, _ := handlers.projectionKernel.reducer.Snapshot("claude", sessionID)
	t.Fatalf("later AskUserQuestion was not projected after process replacement: %+v", projection)
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
	agent.SetLiveProcess(sessionID, core.LiveSessionProcess{SessionID: sessionID, PID: 4242, Live: true})
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

// M5 (§5.1, Phase 2): process death with a non-terminal transcript tail must synthesize
// turn_aborted — the only closure path for a crashed Claude session (sourceIsLive flips to
// false at admission, so the commit gate does not release it; §3.3 rule #2 / D6 producer-layer
// semantics, mirroring the codex producer). The synthesized terminal event feeds the reducer so
// the in-flight turn settles to aborted instead of staying hydrating forever.
func TestClaudeProcessDeathSynthesizesTurnAborted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "process-death-synthesized-abort"
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

	// Kill the live process mid-turn (no final stop_reason in the transcript). The relay must
	// synthesize turn_aborted before broadcasting idle.
	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()

	messages = client.readEvents(t, 2)
	if messages[0]["event"] != "turn_aborted" || messages[1]["event"] != "session_state_changed" {
		t.Fatalf("events after process death = %v, want [turn_aborted, session_state_changed]", eventNames(messages))
	}
	abortData, _ := messages[0]["data"].(map[string]any)
	if abortData["turnId"] != "death-user-1" || abortData["reason"] != "process_death" {
		t.Fatalf("turn_aborted data = %#v, want turnId=death-user-1 reason=process_death", abortData)
	}

	// The synthesized terminal event must have settled the in-flight turn in the reducer —
	// this is what releases the non-live cold-hydrate commit gate (all armed turns terminal).
	snap, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("claude", sessionID)
	if !ok {
		t.Fatal("no projection snapshot after synthesized abort")
	}
	if len(snap.Turns) != 1 || snap.Turns[0].Status != "aborted" {
		t.Fatalf("projection after synthesized abort = %+v, want single aborted turn", snap.Turns)
	}
}

// M5b: process death after an already-terminal transcript tail must NOT synthesize a second
// abort (the turn is closed; synthesis would be a duplicate terminal event).
func TestClaudeProcessDeathTerminalTailDoesNotSynthesizeAbort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "process-death-terminal-tail"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"done-user-1","message":{"role":"user","content":"external prompt"}}`,
		`{"type":"assistant","uuid":"done-asst-1","message":{"id":"done-asst-1","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	_, agent, client := startClaudeFileRelayFixture(t, sessionID, true)

	// Terminal tail → initial scan is idle only (no replay of the completed turn).
	messages := client.readEvents(t, 1)
	if messages[0]["event"] != "session_state_changed" {
		t.Fatalf("first event = %#v, want initial idle", messages[0])
	}

	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()

	// Terminal tail: only idle is emitted, never a synthesized turn_aborted.
	time.Sleep(60 * time.Millisecond)
	messages = client.readEvents(t, 1)
	if messages[0]["event"] != "session_state_changed" {
		t.Fatalf("event after process death with terminal tail = %#v, want idle only (no synthesized abort)", messages[0])
	}
}

// TestClaudeNormalizedUserText_LocalCommandEchoes：CLI 斜杠命令注入
// （/model 等 set_model 直达会写入 transcript）不得以原始 XML 进投影——
// caveat/stdout 丢弃，command-name/args 收敛为紧凑一行（与冷历史同渲染）。
// owner 真机 2026-09-04 验收 #2：三条 <local-command-*> 气泡原文泄漏。
func TestClaudeNormalizedUserText_LocalCommandEchoes(t *testing.T) {
	blocks := []claudeRelayContentBlock{
		{Type: "text", Text: "<local-command-caveat>Caveat: The messages below were generated by the user while in local commands. DO NOT respond.</local-command-caveat>"},
		{Type: "text", Text: "<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>haiku</command-args>"},
		{Type: "text", Text: "<local-command-stdout>Set model to haiku (glm-4.7)</local-command-stdout>"},
	}
	got := claudeNormalizedUserText(blocks)
	if strings.Contains(got, "<") {
		t.Fatalf("raw XML leaked into projection text: %q", got)
	}
	if got != "/model haiku" {
		t.Fatalf("normalized text = %q, want \"/model haiku\"", got)
	}
}

// 纯 local-command 行（caveat/stdout-only）归一化后为空 → 不建立新回合、
// 不产生 user_message 事件（图形归因回落父链）。
func TestClaudeNormalizedUserText_PureEchoIsEmpty(t *testing.T) {
	blocks := []claudeRelayContentBlock{
		{Type: "text", Text: "<local-command-stdout>ok</local-command-stdout>"},
	}
	if got := claudeNormalizedUserText(blocks); strings.TrimSpace(got) != "" {
		t.Fatalf("pure echo must normalize to empty, got %q", got)
	}
}

// TestClaudeFileRelayModelSwitchAppendKeepsProjecting：owner 真机 2026-09-04
// 23:50 复现（93cd4a10）：file-relay watching 中，transcript 追加
// queue-operation×2 → caveat(meta) → /model → stdout → 问题 B → 回复 B。
// 预期：新回合内容继续投影（text_delta 到达）；回归形态=追加后循环不再
// 产出任何事件（投影冻结 rev45）。
func TestClaudeFileRelayModelSwitchAppendKeepsProjecting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withFastClaudeFileRelay(t)
	const sessionID = "model-switch-append"
	user := `{"type":"user","uuid":"u-a","message":{"id":"turn-a","role":"user","content":"问题A"}}`
	assistant := `{"type":"assistant","uuid":"as-a","message":{"id":"msg-a","role":"assistant","content":[{"type":"text","text":"回复A"}],"stop_reason":"end_turn"}}`
	path := writeClaudeFileRelayTranscript(t, home, sessionID, user, assistant)

	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 5555, Live: true},
		},
		alivePIDs: map[int]bool{5555: true},
	}
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: sessionID})
	handlers.startClaudeSessionFileRelayAt(sessionID, serverConn, "claude", nil)
	client := &websocketClient{conn: clientConn}

	// 初次 idle（初始扫描：终态 assistant + 活进程 → 仅 idle 一条）
	_ = client.readEvents(t, 1)

	// 生产同形追加（owner 23:50 窗口 40 行的骨架）
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"queue-operation"}`,
		`{"type":"queue-operation"}`,
		`{"type":"user","uuid":"cav-1","parentUuid":"sys-1","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"<local-command-caveat>Caveat: noise</local-command-caveat>"}]}}`,
		`{"type":"user","uuid":"cmd-1","parentUuid":"cav-1","message":{"role":"user","content":[{"type":"text","text":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>haiku</command-args>"}]}}`,
		`{"type":"user","uuid":"so-1","parentUuid":"cmd-1","message":{"role":"user","content":[{"type":"text","text":"<local-command-stdout>Set model to haiku</local-command-stdout>"}]}}`,
		`{"type":"user","uuid":"u-b","parentUuid":"so-1","message":{"role":"user","content":"问题B"}}`,
		`{"type":"assistant","uuid":"as-b","parentUuid":"u-b","message":{"id":"msg-b","role":"assistant","content":[{"type":"text","text":"回复B"}],"stop_reason":"end_turn"}}`,
	)

	// 宽松收集：直到 turn_completed（≤4s），断言回复 B 文本到达且无 XML 泄漏。
	var sawReplyB, sawCompleted, sawXML bool
	var names []string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && !sawCompleted {
		_ = client.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		var ev map[string]any
		if err := client.conn.ReadJSON(&ev); err != nil {
			continue
		}
		names = append(names, fmt.Sprintf("%v", ev["event"]))
		data, _ := ev["data"].(map[string]any)
		delta, _ := data["delta"].(string)
		if delta != "" {
			names[len(names)-1] = fmt.Sprintf("text_delta:%q", delta[:min(40, len(delta))])
		}
		if delta == "回复B" {
			sawReplyB = true
		}
		if strings.Contains(delta, "<") {
			sawXML = true
		}
		if ev["event"] == "turn_completed" {
			sawCompleted = true
		}
	}
	if !sawReplyB || !sawCompleted || sawXML {
		t.Fatalf("appended turn projection: replyB=%v completed=%v xmlLeak=%v events=%v",
			sawReplyB, sawCompleted, sawXML, names)
	}
}

// TestClaudeFileRelayRealTranscript429Sequence：直接喂 owner 真实会话文件
// （93cd4a10，含 synthetic assistant、interrupt 标记、429 system error 行、
// 429 重试后的回复）——复现生产投影冻结（headRev 卡 45）。
func TestClaudeFileRelayRealTranscript429Sequence(t *testing.T) {
	if _, err := os.Stat("/Users/jacklee/.claude/projects/-Users-jacklee-Projects-Chat/93cd4a10-8011-449c-be77-e1ad9ed82edd.jsonl"); err != nil {
		t.Skip("real transcript not present on this machine")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	withFastClaudeFileRelay(t)
	const sessionID = "93cd4a10-8011-449c-be77-e1ad9ed82edd"
	src := "/Users/jacklee/.claude/projects/-Users-jacklee-Projects-Chat/93cd4a10-8011-449c-be77-e1ad9ed82edd.jsonl"
	projectDir := filepath.Join(home, ".claude", "projects", "-Users-jacklee-Projects-Chat")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// 截到 15:50:36 之前（发消息前状态），其余作为「watching 中追加」
	lines := strings.Split(string(data), "\n")
	var base, tail []string
	cut := false
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if strings.Contains(ln, "2026-09-04T15:50:2") || strings.Contains(ln, "2026-09-04T15:50:1") && strings.Contains(ln, "queue-operation") {
			// 保留到 15:50:2x 为止
		}
		if !cut && (strings.Contains(ln, "2026-09-04T15:50:36")) {
			cut = true
		}
		if cut {
			tail = append(tail, ln)
		} else {
			base = append(base, ln)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(base, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 84078, Live: true},
		},
		alivePIDs: map[int]bool{84078: true},
	}
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: sessionID})
	handlers.startClaudeSessionFileRelayAt(sessionID, serverConn, "claude", nil)
	client := &websocketClient{conn: clientConn}

	// 先等初始扫描完成（读走初始 idle 事件），消除「append 先于初扫被当基线」竞态
	_ = client.readEvents(t, 1)

	// 完整时序复现：真实 429 tail（queue-op/synthetic/429 system/user/assistant）
	_ = tail
	appendClaudeFileRelayTranscript(t, path, tail...)

	// 单一截止时间读取（gorilla 读超时即连接终态，不能 continue 复读）
	var sawReply, sawCompleted bool
	var names []string
	_ = client.conn.SetReadDeadline(time.Now().Add(6 * time.Second))
	for !sawCompleted {
		var ev map[string]any
		if err := client.conn.ReadJSON(&ev); err != nil {
			break
		}
		names = append(names, fmt.Sprintf("%v", ev["event"]))
		data, _ := ev["data"].(map[string]any)
		delta, _ := data["delta"].(string)
		if strings.Contains(delta, "耳机") {
			sawReply = true
		}
		if ev["event"] == "turn_completed" {
			sawCompleted = true
		}
	}
	stillRunning := handlers.relayKindIs(sessionID, relayKindClaudeFile)
	// 正文经 batch 事务走投影面（v1 只出 lifecycle）——真实行集的价值在
	// 「消费发生不卡 cursor」：断言新 turn 的收口到达。
	_ = sawReply
	if !sawCompleted {
		t.Fatalf("real-transcript append not consumed: relayStillRunning=%v events(%d)=%v",
			stillRunning, len(names), names)
	}
}
