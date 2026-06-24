package gobridge

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type fakeAgentSession struct {
	id          string
	events      chan core.Event
	sentPrompts []string
	sendErr     error
	sendHook    func(*fakeAgentSession, string)
	liveMode    string
	liveModeOK  bool
	closeOnce   sync.Once
	closed      bool
}

func (f *fakeAgentSession) Send(prompt string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	f.sentPrompts = append(f.sentPrompts, prompt)
	if f.sendHook != nil {
		f.sendHook(f, prompt)
	}
	return f.sendErr
}

func (f *fakeAgentSession) RespondPermission(string, core.PermissionResult) error {
	return nil
}

func (f *fakeAgentSession) RespondQuestion(string, []string) error {
	return nil
}

func (f *fakeAgentSession) RejectQuestion(string) error {
	return nil
}

func (f *fakeAgentSession) SetLiveMode(mode string) bool {
	if !f.liveModeOK {
		return false
	}
	f.liveMode = mode
	return true
}

func (f *fakeAgentSession) Events() <-chan core.Event {
	return f.events
}

func (f *fakeAgentSession) CurrentSessionID() string {
	return f.id
}

func (f *fakeAgentSession) Alive() bool {
	return true
}

func (f *fakeAgentSession) Close() error {
	f.closeOnce.Do(func() {
		f.closed = true
		close(f.events)
	})
	return nil
}

func openTestConn(t *testing.T) (*Conn, *websocket.Conn, func()) {
	t.Helper()
	serverConnCh := make(chan *Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		serverConnCh <- newConn(ws)
	}))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial failed: %v", err)
	}

	serverConn := <-serverConnCh
	cleanup := func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		server.Close()
	}
	return serverConn, clientConn, cleanup
}

func readEventNames(t *testing.T, clientConn *websocket.Conn, count int) []string {
	t.Helper()
	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline failed: %v", err)
	}
	var events []string
	for range count {
		var payload map[string]interface{}
		if err := clientConn.ReadJSON(&payload); err != nil {
			t.Fatalf("read json failed: %v", err)
		}
		eventName, _ := payload["event"].(string)
		events = append(events, eventName)
	}
	return events
}

func TestMapAgentEventTextDelta(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{Type: core.EventText, Content: "hello"})
	if name != "text_delta" {
		t.Fatalf("event name = %q, want text_delta", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload, ok := data.(map[string]interface{})
	if !ok {
		t.Fatalf("payload type = %T, want map[string]interface{}", data)
	}
	if got := payload["delta"]; got != "hello" {
		t.Fatalf("delta = %#v, want hello", got)
	}
}

func TestMapAgentEventToolStarted(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:         core.EventToolUse,
		ToolName:     "bash",
		ToolInput:    "ls",
		ToolInputRaw: map[string]any{"cmd": "ls"},
	})
	if name != "tool_started" {
		t.Fatalf("event name = %q, want tool_started", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	if got := payload["toolName"]; got != "bash" {
		t.Fatalf("toolName = %#v, want bash", got)
	}
	if got := payload["toolInput"]; got != "ls" {
		t.Fatalf("toolInput = %#v, want ls", got)
	}
}

func TestMapAgentEventToolStartedOmitsFileChanges(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:     core.EventToolUse,
		ToolName: "Patch",
		FileChanges: []core.FileChange{{
			Path: "OpenCodeiOS/App.swift",
			Kind: "edit",
			Diff: "@@",
		}},
	})
	if name != "tool_started" {
		t.Fatalf("event name = %q, want tool_started", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	if _, ok := payload["fileChanges"]; ok {
		t.Fatal("tool_started should not expose fileChanges before completion")
	}
}

func TestMapAgentEventToolFinishedFallsBackToContent(t *testing.T) {
	success := true
	name, data, done := mapAgentEvent(core.Event{
		Type:        core.EventToolResult,
		ToolName:    "bash",
		Content:     "stdout line",
		ToolSuccess: &success,
	})
	if name != "tool_finished" {
		t.Fatalf("event name = %q, want tool_finished", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	if got := payload["toolResult"]; got != "stdout line" {
		t.Fatalf("toolResult = %#v, want stdout line", got)
	}
	if got := payload["toolStatus"]; got != "completed" {
		t.Fatalf("toolStatus = %#v, want completed", got)
	}
}

func TestMapAgentEventToolFinishedIncludesToolInput(t *testing.T) {
	success := true
	name, data, done := mapAgentEvent(core.Event{
		Type:        core.EventToolResult,
		ToolName:    "Bash",
		ToolInput:   "ls -la src/",
		ToolResult:  "file1.go\nfile2.go\n",
		ToolSuccess: &success,
	})
	if name != "tool_finished" {
		t.Fatalf("event name = %q, want tool_finished", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	if got := payload["toolInput"]; got != "ls -la src/" {
		t.Fatalf("toolInput = %#v, want 'ls -la src/'", got)
	}
	if got := payload["toolName"]; got != "Bash" {
		t.Fatalf("toolName = %#v, want Bash", got)
	}
}

func TestMapAgentEventToolFinishedEmptyToolInput(t *testing.T) {
	success := true
	name, data, _ := mapAgentEvent(core.Event{
		Type:        core.EventToolResult,
		ToolName:    "Read",
		ToolResult:  "contents",
		ToolSuccess: &success,
	})
	if name != "tool_finished" {
		t.Fatalf("event name = %q, want tool_finished", name)
	}
	payload := data.(map[string]interface{})
	if got := payload["toolInput"]; got != "" {
		t.Fatalf("toolInput = %#v, want empty string for non-Bash tools", got)
	}
}

func TestMapAgentEventToolFinishedIncludesFileChanges(t *testing.T) {
	success := true
	name, data, done := mapAgentEvent(core.Event{
		Type:        core.EventToolResult,
		ToolName:    "Patch",
		ToolResult:  "main.go\nREADME.md",
		ToolSuccess: &success,
		FileChanges: []core.FileChange{
			{Path: "main.go", Kind: "edit"},
			{Path: "README.md", Kind: "create"},
		},
	})
	if name != "tool_finished" {
		t.Fatalf("event name = %q, want tool_finished", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	changes, ok := payload["fileChanges"].([]map[string]interface{})
	if !ok {
		t.Fatalf("payload[fileChanges] type = %T, want []map[string]interface{}", payload["fileChanges"])
	}
	if len(changes) != 2 {
		t.Fatalf("len(fileChanges) = %d, want 2", len(changes))
	}
	if got := changes[1]["path"]; got != "README.md" {
		t.Fatalf("path = %#v, want README.md", got)
	}
	if got := changes[1]["kind"]; got != "create" {
		t.Fatalf("kind = %#v, want create", got)
	}
}

func TestMapAgentEventPlanTodosUpdated(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type: core.EventPlan,
		Plan: []core.Todo{{Content: "wire provider support", Status: "in_progress"}},
	})
	if name != "todos_updated" {
		t.Fatalf("event name = %q, want todos_updated", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	todos, ok := payload["todos"].([]map[string]interface{})
	if !ok {
		t.Fatalf("payload[todos] type = %T, want []map[string]interface{}", payload["todos"])
	}
	if len(todos) != 1 {
		t.Fatalf("len(todos) = %d, want 1", len(todos))
	}
	if got := todos[0]["priority"]; got != "normal" {
		t.Fatalf("priority = %#v, want normal", got)
	}
}

func TestMapAgentEventTurnCompleted(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:         core.EventResult,
		Done:         true,
		Content:      "final",
		InputTokens:  12,
		OutputTokens: 3,
	})
	if name != "turn_completed" {
		t.Fatalf("event name = %q, want turn_completed", name)
	}
	if !done {
		t.Fatal("done = false, want true")
	}
	payload := data.(map[string]interface{})
	if got := payload["text"]; got != "final" {
		t.Fatalf("text = %#v, want final", got)
	}
	if got := payload["inputTokens"]; got != 12 {
		t.Fatalf("inputTokens = %#v, want 12", got)
	}
	if got := payload["outputTokens"]; got != 3 {
		t.Fatalf("outputTokens = %#v, want 3", got)
	}
}

func TestMapAgentEventContextUsageUpdated(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type: core.EventContextUsageUpdated,
		ContextUsage: &core.ContextUsage{
			UsedTokens:            41061,
			BaselineTokens:        12000,
			TotalTokens:           41061,
			InputTokens:           40849,
			CachedInputTokens:     36864,
			OutputTokens:          212,
			ReasoningOutputTokens: 32,
			ContextWindow:         258400,
		},
	})
	if name != "context_usage_updated" {
		t.Fatalf("event name = %q, want context_usage_updated", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	contextPayload, ok := payload["context"].(map[string]interface{})
	if !ok {
		t.Fatalf("payload[context] type = %T, want map[string]interface{}", payload["context"])
	}
	if got := contextPayload["usedTokens"]; got != 41061 {
		t.Fatalf("usedTokens = %#v, want 41061", got)
	}
	if got := contextPayload["contextWindow"]; got != 258400 {
		t.Fatalf("contextWindow = %#v, want 258400", got)
	}
}

func TestMapAgentEventError(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:  core.EventError,
		Error: errors.New("boom"),
	})
	if name != "error" {
		t.Fatalf("event name = %q, want error", name)
	}
	if !done {
		t.Fatal("done = false, want true")
	}
	payload := data.(map[string]interface{})
	if got := payload["message"]; got != "boom" {
		t.Fatalf("message = %#v, want boom", got)
	}
}

func TestRelayEventsRoutesDurableMilestoneToOfflineRelayDevice(t *testing.T) {
	handlers := NewHandlers()
	defer handlers.observation.Stop()

	bridgeID := "brg_offline_route"
	routeID := "route_offline_route"
	deviceID := "dev_offline_route"
	channelGeneration := uint64(7)

	macIdentityKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	relayIdentity := &RelayCryptoIdentity{
		privateKey: macIdentityKey,
		publicKey:  macIdentityKey.PublicKey(),
	}
	deviceIdentityKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	store := NewMemoryDeviceStore()
	if err := store.AddDevice(TrustedDeviceRecord{
		DeviceID:               deviceID,
		DisplayName:            "Offline Test iPhone",
		Platform:               "ios",
		TokenHash:              "token-hash",
		IdentityPublicKey:      base64.StdEncoding.EncodeToString(deviceIdentityKey.PublicKey().Bytes()),
		RelayEnabled:           true,
		RelayChannelGeneration: channelGeneration,
		CreatedAt:              time.Now(),
		LastSeenAt:             time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	handlers.SetBridgeID(bridgeID)
	handlers.ConfigureRelayUpgrade(store, relayIdentity, nil)

	var sent []json.RawMessage
	handlers.ConfigureRelayDelivery(routeID, func(envelope json.RawMessage) error {
		sent = append(sent, append(json.RawMessage(nil), envelope...))
		return nil
	})

	prekeyPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	upload := handlers.deliveryPrekeys.UploadPrekeys(PrekeyUploadBatch{
		BatchID:  "batch_offline_route",
		DeviceID: deviceID,
		Prekeys: []PrekeyUploadItem{{
			PrekeyID:  "pk_offline_route",
			PublicKey: base64.StdEncoding.EncodeToString(prekeyPrivate.PublicKey().Bytes()),
		}},
	})
	if upload.Error != "" {
		t.Fatalf("UploadPrekeys error = %q", upload.Error)
	}

	// A relay device connection can remain registered after the phone has
	// dropped off the relay path. Durable mailbox routing must not trust that
	// broadcaster state as proof that the device received the milestone.
	handlers.broadcaster.RegisterConn(&relayBroadcastCaptureConn{
		device: &TrustedDeviceRecord{DeviceID: deviceID},
	})

	session := &fakeAgentSession{
		id:     "ses_offline_route",
		events: make(chan core.Event, 1),
	}
	session.events <- core.Event{Type: core.EventResult, Done: true, Content: "done"}
	close(session.events)

	handlers.relayEvents(&relayBroadcastCaptureConn{}, session, "ses_offline_route", "codex")

	if len(sent) != 1 {
		t.Fatalf("offline envelopes = %d, want 1", len(sent))
	}
	var envelope RelayEnvelope
	if err := json.Unmarshal(sent[0], &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.RouteID != routeID || envelope.DestinationID != deviceID || envelope.ChannelGeneration != channelGeneration {
		t.Fatalf("unexpected envelope route/destination/generation: %+v", envelope)
	}
	if envelope.PrekeyID == nil || *envelope.PrekeyID != "pk_offline_route" {
		t.Fatalf("prekeyID = %#v, want pk_offline_route", envelope.PrekeyID)
	}
	if envelope.EpochIndex == nil || envelope.KeyEpochID != "mailbox:0" {
		t.Fatalf("epoch metadata = %s/%#v, want mailbox:0/0", envelope.KeyEpochID, envelope.EpochIndex)
	}
	if envelope.EpochEphemeralPublicKey == nil || envelope.EpochAuthTag == nil {
		t.Fatal("mailbox envelope missing epoch auth metadata")
	}
	macEpochPublic, err := base64.StdEncoding.DecodeString(*envelope.EpochEphemeralPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := DeriveMailboxKeyFromPrekey(prekeyPrivate, macEpochPublic, bridgeID, deviceID, "pk_offline_route", 0)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := envelope.EncodeAAD()
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := OpenEnvelope(key, envelope.Counter, aad, envelope.Ciphertext)
	if err != nil {
		t.Fatalf("OpenEnvelope: %v", err)
	}
	var event EventMessage
	if err := json.Unmarshal(plaintext, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "event" || event.SessionID != "ses_offline_route" || event.BackendID != "codex" || event.Event != "turn_completed" {
		t.Fatalf("event = %+v, want offline turn_completed", event)
	}
}

func TestRelayEventsSendsIdleAfterTurnCompleted(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "opencode",
		SessionID: "ses_test",
	})
	session := &fakeAgentSession{
		id:     "ses_test",
		events: make(chan core.Event, 2),
	}
	session.events <- core.Event{Type: core.EventText, Content: "hello"}
	session.events <- core.Event{Type: core.EventResult, Done: true, Content: "done"}
	close(session.events)

	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_test", "opencode")
		close(done)
	}()

	events := readEventNames(t, clientConn, 3)
	if want := []string{"text_delta", "turn_completed", "session_state_changed"}; len(events) != len(want) || events[0] != want[0] || events[1] != want[1] || events[2] != want[2] {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not finish after turn completion")
	}
}

func TestRelayEventsSendsIdleAfterError(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "opencode",
		SessionID: "ses_test",
	})
	session := &fakeAgentSession{
		id:     "ses_test",
		events: make(chan core.Event, 1),
	}
	session.events <- core.Event{Type: core.EventError, Error: errors.New("boom")}
	close(session.events)

	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_test", "opencode")
		close(done)
	}()

	events := readEventNames(t, clientConn, 2)
	if want := []string{"error", "session_state_changed"}; len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not finish after error")
	}
}

func TestRelayEventsClaudeDoesNotExitOnResultOrError(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "claude",
		SessionID: "ses_test",
	})
	session := &fakeAgentSession{
		id:     "ses_test",
		events: make(chan core.Event, 4),
	}
	session.events <- core.Event{Type: core.EventText, Content: "hello"}
	session.events <- core.Event{Type: core.EventResult, Done: true, Content: "done"}
	// It should continue running, so we can send another event after EventResult!
	session.events <- core.Event{Type: core.EventText, Content: "hello2"}
	session.events <- core.Event{Type: core.EventError, Error: errors.New("boom")}
	close(session.events)

	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_test", "claude")
		close(done)
	}()

	// The events read should include text_delta, turn_completed, session_state_changed (from first result completion),
	// followed by text_delta (hello2), error, and session_state_changed (from second error completion).
	events := readEventNames(t, clientConn, 6)
	want := []string{"text_delta", "turn_completed", "session_state_changed", "text_delta", "error", "session_state_changed"}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(events), len(want), events)
	}
	for i, name := range want {
		if events[i] != name {
			t.Errorf("at index %d: got event %q, want %q", i, events[i], name)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not exit even after events channel closed")
	}
}

func TestRelayEventsSendsTurnCompletedOnChannelClose(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "claudecode",
		SessionID: "ses_test",
	})
	session := &fakeAgentSession{
		id:     "ses_test",
		events: make(chan core.Event, 1),
	}
	handlers.putSessionWithMeta("ses_test", "claudecode", "", session)
	handlers.sessions.markRunning("ses_test")

	session.events <- core.Event{Type: core.EventText, Content: "some output"}
	close(session.events)

	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_test", "claudecode")
		close(done)
	}()

	events := readEventNames(t, clientConn, 3)
	if want := []string{"text_delta", "turn_completed", "session_state_changed"}; len(events) != len(want) || events[0] != want[0] || events[1] != want[1] || events[2] != want[2] {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not finish after channel closure")
	}
}

func TestMapAgentEventContextCompressing(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:      core.EventContextCompressing,
		SessionID: "ses_1",
	})
	if name != "context_compressing" {
		t.Fatalf("event name = %q, want context_compressing", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	if got := payload["sessionId"]; got != "ses_1" {
		t.Fatalf("sessionId = %#v, want ses_1", got)
	}
}

func TestMapAgentEventContextCompressed(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:      core.EventContextCompressed,
		SessionID: "ses_1",
	})
	if name != "context_compressed" {
		t.Fatalf("event name = %q, want context_compressed", name)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	payload := data.(map[string]interface{})
	if got := payload["sessionId"]; got != "ses_1" {
		t.Fatalf("sessionId = %#v, want ses_1", got)
	}
}

// ── Task 0: 事实基线回归测试 ─────────────────────────────────────────────────

func TestConnSendJSONSerializesConcurrentWrites(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	var wg sync.WaitGroup
	writers := 50
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(seq int) {
			defer wg.Done()
			serverConn.SendJSON(map[string]interface{}{
				"type": "event",
				"seq":  seq,
			})
		}(i)
	}
	wg.Wait()

	if err := clientConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	received := 0
	for i := 0; i < writers; i++ {
		var payload map[string]interface{}
		if err := clientConn.ReadJSON(&payload); err != nil {
			t.Fatalf("read json failed after %d messages: %v", received, err)
		}
		received++
	}
	if received != writers {
		t.Fatalf("received %d messages, want %d", received, writers)
	}
}

func TestRelayEventsAndDiagnosticsCanWriteConcurrently(t *testing.T) {
	handlers := NewHandlers()
	agent := &fakeAgent{
		name: "claudecode",
		diagnosticProgress: []core.DiagnosticProgress{{
			CheckID: "cli",
			Status:  "running",
			Message: "checking",
		}},
		diagnosticReport: &core.DiagnosticReport{OverallStatus: "healthy"},
	}
	handlers.RegisterAgent("claudecode", agent)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "claudecode",
		SessionID: "ses_concurrent",
	})

	session := &fakeAgentSession{id: "ses_concurrent", events: make(chan core.Event, 8)}
	session.events <- core.Event{Type: core.EventText, Content: "hello"}
	session.events <- core.Event{Type: core.EventResult, Done: true, Content: "done"}
	close(session.events)

	relayDone := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_concurrent", "claudecode")
		close(relayDone)
	}()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "run_diagnostics",
		RequestID: "diag-concurrent",
	})

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for i := 0; i < 6; i++ {
		var payload map[string]interface{}
		if err := clientConn.ReadJSON(&payload); err != nil {
			t.Fatalf("read json failed at message %d: %v", i+1, err)
		}
	}

	select {
	case <-relayDone:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not finish during concurrent writes")
	}
}

func TestBackendListSessionDeleteCapability(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	found := false
	for _, cap := range backends[0].Capabilities {
		if cap == "session_delete" {
			found = true
		}
	}
	if found {
		t.Fatal("codex without SessionDeleter should not advertise session_delete")
	}
}

func TestBackendListSessionHistoryCapabilityForHistoryProvider(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", history: []core.HistoryEntry{}})

	backends := handlers.BackendList()
	found := false
	for _, cap := range backends[0].Capabilities {
		if cap == "session_history" {
			found = true
		}
	}
	if !found {
		t.Fatal("codex with HistoryProvider should advertise session_history")
	}
}

func TestBackendListDoesNotAdvertiseSessionMutationWithoutBothInterfaces(t *testing.T) {
	// unsupportedMutationAgent doesn't implement SessionRenamer or SessionArchiver
	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &unsupportedMutationAgent{name: "codex"})

	backends := handlers.BackendList()
	for _, cap := range backends[0].Capabilities {
		if cap == "session_mutation" {
			t.Fatal("codex without SessionRenamer+SessionArchiver should not advertise session_mutation")
		}
	}
}

func TestGetSessionMessagesFallsBackToGenericHistoryWhenRichUnsupported(t *testing.T) {
	agent := &fakeAgent{
		name:           "codex",
		richHistoryErr: core.ErrNotSupported,
		history: []core.HistoryEntry{{
			Role:      "user",
			Content:   "generic history path",
			Timestamp: time.Unix(1710000000, 0).UTC(),
		}},
	}

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "generic-hist-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_generic"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 1 {
		t.Fatalf("message count = %d, want 1", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if got := entry["content"]; got != "generic history path" {
		t.Fatalf("content = %#v, want generic history path", got)
	}
}

// ── Task 1: WebSocket ping/pong 与死连接清理 ────────────────────────────────

func TestConnCloseIdempotent(t *testing.T) {
	serverConn, _, cleanup := openTestConn(t)
	defer cleanup()

	var cleanupCalls int
	serverConn.onCleanup = func() { cleanupCalls++ }

	if err := serverConn.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestConnCloseCallsCleanupHook(t *testing.T) {
	serverConn, _, cleanup := openTestConn(t)
	defer cleanup()

	called := false
	serverConn.onCleanup = func() { called = true }
	_ = serverConn.Close()
	if !called {
		t.Fatal("cleanup hook not called on Close")
	}
}

func TestConnSendJSONAfterCloseIsNoop(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	_ = serverConn.Close()
	serverConn.SendJSON(map[string]string{"type": "should_not_arrive"})

	if err := clientConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var v map[string]interface{}
	err := clientConn.ReadJSON(&v)
	if err == nil {
		t.Fatal("expected read error after conn closed, got message")
	}
}

func TestServerCloseAllConnectionsClosesActiveWebSockets(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode"})
	server := NewServer(handlers)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientConn.Close()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if server.ActiveConnectionCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := server.ActiveConnectionCount(); got != 1 {
		t.Fatalf("active connection count = %d, want 1", got)
	}

	closed := server.CloseAllConnections("test shutdown")
	if closed != 1 {
		t.Fatalf("closed count = %d, want 1", closed)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err = clientConn.ReadMessage()
	if err == nil {
		t.Fatal("expected connection close after CloseAllConnections")
	}

	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if server.ActiveConnectionCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := server.ActiveConnectionCount(); got != 0 {
		t.Fatalf("active connection count after close = %d, want 0", got)
	}
}

func TestPingTickerClosesOnTimeout(t *testing.T) {
	// 使用 httptest server + Server.ServeHTTP 验证 ping 超时后连接关闭
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode"})
	server := NewServer(handlers)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer ws.Close()

	if err := ws.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err = ws.ReadMessage()
	if err == nil {
		t.Fatal("expected connection close")
	}
}

// ── Task 3: Session idle cleanup ────────────────────────────────────────────

func TestCleanupIdleSessionsRemovesExpired(t *testing.T) {
	handlers := NewHandlers()
	sess := &fakeAgentSession{id: "ses_idle", events: make(chan core.Event)}
	handlers.putSession("ses_idle", sess)

	// 手动设置 idle 状态和过期时间
	tt, _ := handlers.sessions.get("ses_idle")
	tt.state = sessionStateIdle
	tt.backendID = "claudecode"
	tt.lastEventAt = time.Now().Add(-400 * time.Second) // 超过 300s TTL

	handlers.cleanupIdleSessions()

	if _, ok := handlers.getSession("ses_idle"); ok {
		t.Fatal("idle expired session should have been removed")
	}
	if !sess.closed {
		t.Fatal("idle session should have been closed")
	}
}

func TestCleanupDoesNotRemoveRunningSessions(t *testing.T) {
	handlers := NewHandlers()
	sess := &fakeAgentSession{id: "ses_running", events: make(chan core.Event)}
	handlers.putSession("ses_running", sess)

	tt, _ := handlers.sessions.get("ses_running")
	tt.state = sessionStateRunning
	tt.backendID = "claudecode"
	tt.lastEventAt = time.Now().Add(-400 * time.Second)

	handlers.cleanupIdleSessions()

	if _, ok := handlers.getSession("ses_running"); !ok {
		t.Fatal("running session should not be removed")
	}
}

func TestCleanupDoesNotRemovePendingSessions(t *testing.T) {
	handlers := NewHandlers()
	sess := &fakeAgentSession{id: "pending-abc", events: make(chan core.Event)}
	handlers.putSession("pending-abc", sess)

	tt, _ := handlers.sessions.get("pending-abc")
	tt.state = sessionStateIdle
	tt.backendID = "codex"
	tt.lastEventAt = time.Now().Add(-700 * time.Second)

	handlers.cleanupIdleSessions()

	if _, ok := handlers.getSession("pending-abc"); !ok {
		t.Fatal("pending session should not be removed")
	}
}

func TestCleanupDoesNotRemoveRecentIdleSessions(t *testing.T) {
	handlers := NewHandlers()
	sess := &fakeAgentSession{id: "ses_recent", events: make(chan core.Event)}
	handlers.putSession("ses_recent", sess)

	tt, _ := handlers.sessions.get("ses_recent")
	tt.state = sessionStateIdle
	tt.backendID = "claudecode"
	tt.lastEventAt = time.Now().Add(-10 * time.Second) // 未过期

	handlers.cleanupIdleSessions()

	if _, ok := handlers.getSession("ses_recent"); !ok {
		t.Fatal("recent idle session should not be removed")
	}
}

// ── Task 4: Broadcaster tests ───────────────────────────────────────────────

func TestBroadcasterSendsToSubscribedConn(t *testing.T) {
	b := NewBroadcaster()
	conn1Server, conn1Client, cleanup1 := openTestConn(t)
	defer cleanup1()
	_, conn2Client, cleanup2 := openTestConn(t)
	defer cleanup2()

	key := SubscriptionKey{BackendID: "codex", SessionID: "ses_1"}
	b.Subscribe(conn1Server, key)

	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "ses_1",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	if err := conn1Client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := conn1Client.ReadJSON(&msg); err != nil {
		t.Fatalf("subscribed conn should receive: %v", err)
	}
	if got := msg["event"]; got != "text_delta" {
		t.Fatalf("event = %#v, want text_delta", got)
	}

	if err := conn2Client.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var unwanted map[string]interface{}
	err := conn2Client.ReadJSON(&unwanted)
	if err == nil {
		t.Fatal("unsubscribed conn should not receive events")
	}
}

func TestBroadcasterUnsubscribeAll(t *testing.T) {
	b := NewBroadcaster()
	conn1Server, conn1Client, cleanup1 := openTestConn(t)
	defer cleanup1()

	key := SubscriptionKey{BackendID: "codex", SessionID: "ses_1"}
	b.Subscribe(conn1Server, key)
	b.UnsubscribeAll(conn1Server)

	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "ses_1",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	if err := conn1Client.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	err := conn1Client.ReadJSON(&msg)
	if err == nil {
		t.Fatal("unsubscribed conn should not receive events")
	}
}

func TestBroadcasterRebind(t *testing.T) {
	b := NewBroadcaster()
	conn1Server, conn1Client, cleanup1 := openTestConn(t)
	defer cleanup1()

	key := SubscriptionKey{BackendID: "codex", SessionID: "pending-abc"}
	b.Subscribe(conn1Server, key)
	b.Rebind("pending-abc", "real-thread-1", "codex", "")

	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "real-thread-1",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	if err := conn1Client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := conn1Client.ReadJSON(&msg); err != nil {
		t.Fatalf("rebound conn should receive events on new session ID: %v", err)
	}
}

func TestTwoConnsSubscribeSameSession(t *testing.T) {
	b := NewBroadcaster()
	conn1Server, conn1Client, cleanup1 := openTestConn(t)
	defer cleanup1()
	conn2Server, conn2Client, cleanup2 := openTestConn(t)
	defer cleanup2()

	key := SubscriptionKey{BackendID: "codex", SessionID: "ses_1"}
	b.Subscribe(conn1Server, key)
	b.Subscribe(conn2Server, key)

	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "ses_1",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	for i, clientConn := range []*websocket.Conn{conn1Client, conn2Client} {
		if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var msg map[string]interface{}
		if err := clientConn.ReadJSON(&msg); err != nil {
			t.Fatalf("conn%d should receive broadcast: %v", i+1, err)
		}
	}
}

func TestDifferentSessionsDoNotCrossTalk(t *testing.T) {
	b := NewBroadcaster()
	conn1Server, conn1Client, cleanup1 := openTestConn(t)
	defer cleanup1()
	conn2Server, conn2Client, cleanup2 := openTestConn(t)
	defer cleanup2()

	b.Subscribe(conn1Server, SubscriptionKey{BackendID: "codex", SessionID: "ses_1"})
	b.Subscribe(conn2Server, SubscriptionKey{BackendID: "codex", SessionID: "ses_2"})

	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "ses_1",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	if err := conn1Client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := conn1Client.ReadJSON(&msg); err != nil {
		t.Fatalf("ses_1 conn should receive: %v", err)
	}

	if err := conn2Client.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var unwanted map[string]interface{}
	err := conn2Client.ReadJSON(&unwanted)
	if err == nil {
		t.Fatal("ses_2 conn should not receive ses_1 events")
	}
}

// TestBroadcasterPassiveEventMatchesSubscriptionWithDirectory 验证被动订阅场景：
// iOS 通过 get_session_messages 订阅 session，带 Directory。
// 被动订阅者（Codex/OpenCode）广播事件时不带 Directory。
// Broadcaster.Send 的 fallback 逻辑应该匹配成功。
func TestBroadcasterPassiveEventMatchesSubscriptionWithDirectory(t *testing.T) {
	b := NewBroadcaster()
	connServer, connClient, cleanup := openTestConn(t)
	defer cleanup()

	// iOS 调 get_session_messages 时 subscribeConnToSession 的行为：
	// SubscriptionKey 带有 directory（来自 iOS 的请求参数）
	subKey := SubscriptionKey{
		BackendID: "codex",
		SessionID: "thread_abc123",
		Directory: "/Users/developer/Projects/myproject",
	}
	b.Subscribe(connServer, subKey)

	// Passive Subscriber 广播事件：没有 Directory
	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "thread_abc123",
		// Directory 为空——被动订阅者不知道 iOS 的 directory
		Message: map[string]string{"type": "event", "event": "todos_updated"},
	})

	if err := connClient.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := connClient.ReadJSON(&msg); err != nil {
		t.Fatalf("passive event (no dir) should match subscription with dir: %v", err)
	}
	if got := msg["event"]; got != "todos_updated" {
		t.Fatalf("event = %#v, want todos_updated", got)
	}
}

// TestBroadcasterFallbackNoCrossBackend 验证 fallback 不跨 backend 泄露。
func TestBroadcasterFallbackNoCrossBackend(t *testing.T) {
	b := NewBroadcaster()
	codexServer, _, cleanup1 := openTestConn(t)
	defer cleanup1()
	opencodeServer, opencodeClient, cleanup2 := openTestConn(t)
	defer cleanup2()

	// 只有 opencode conn 有订阅
	b.Subscribe(opencodeServer, SubscriptionKey{BackendID: "opencode", SessionID: "ses_1"})
	// codex conn 也有订阅
	b.Subscribe(codexServer, SubscriptionKey{BackendID: "codex", SessionID: "ses_A"})

	// 广播 opencode 事件给 session_B（opencode conn 没订阅 session_B）
	// fallback 应该发给 opencode conn（同 backend），不应该发给 codex conn
	b.Send(BroadcastEvent{
		BackendID: "opencode",
		SessionID: "session_B",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	if err := opencodeClient.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := opencodeClient.ReadJSON(&msg); err != nil {
		t.Fatalf("opencode conn should receive fallback from same backend: %v", err)
	}
}

// TestBroadcasterFallbackToAllBackendConns 验证当没有 session 精确匹配时，
// 事件仍然广播给该 backend 的所有连接。
// 这是 MacBridge 模式下被动事件到达 iOS 的关键 fallback。
func TestBroadcasterFallbackToAllBackendConns(t *testing.T) {
	b := NewBroadcaster()
	conn1Server, conn1Client, cleanup1 := openTestConn(t)
	defer cleanup1()

	// iOS 订阅了 session_A（比如通过之前的 get_session_messages）
	subKeyA := SubscriptionKey{BackendID: "codex", SessionID: "session_A", Directory: "/project"}
	b.Subscribe(conn1Server, subKeyA)

	// 被动订阅者广播 session_B 的事件（Mac 新建的 session，iOS 没订阅过）
	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "session_B",
		Message:   map[string]string{"type": "event", "event": "text_delta", "sessionId": "session_B"},
	})

	if err := conn1Client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := conn1Client.ReadJSON(&msg); err != nil {
		t.Fatalf("fallback should deliver event to all backend conns even without session match: %v", err)
	}
	if got := msg["event"]; got != "text_delta" {
		t.Fatalf("event = %#v, want text_delta", got)
	}
}

func TestBroadcasterFallbackToRegisteredConnWithoutSubscription(t *testing.T) {
	b := NewBroadcaster()
	connServer, connClient, cleanup := openTestConn(t)
	defer cleanup()

	b.RegisterConn(connServer)

	b.Send(BroadcastEvent{
		BackendID: "opencode",
		SessionID: "session_from_desktop",
		Message:   map[string]string{"type": "event", "event": "text_delta", "sessionId": "session_from_desktop"},
	})

	if err := connClient.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := connClient.ReadJSON(&msg); err != nil {
		t.Fatalf("registered conn without session subscription should receive passive event: %v", err)
	}
	if got := msg["event"]; got != "text_delta" {
		t.Fatalf("event = %#v, want text_delta", got)
	}
}

// TestBroadcasterNoFallbackToOtherBackend 验证 fallback 不跨 backend 泄露。
func TestBroadcasterNoFallbackToOtherBackend(t *testing.T) {
	b := NewBroadcaster()
	codexServer, codexClient, cleanup1 := openTestConn(t)
	defer cleanup1()
	opencodeServer, _, cleanup2 := openTestConn(t)
	defer cleanup2()

	// OpenCode conn 订阅了某个 session
	b.Subscribe(opencodeServer, SubscriptionKey{BackendID: "opencode", SessionID: "ses_1"})
	// Codex conn 订阅了另一个 session
	b.Subscribe(codexServer, SubscriptionKey{BackendID: "codex", SessionID: "ses_A"})

	// 广播一个 codex session_B 事件（codex conn 没订阅 session_B）
	// fallback 应该把事件发给 codex conn（同 backend 的所有连接）
	b.Send(BroadcastEvent{
		BackendID: "codex",
		SessionID: "session_B",
		Message:   map[string]string{"type": "event", "event": "text_delta"},
	})

	if err := codexClient.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg map[string]interface{}
	if err := codexClient.ReadJSON(&msg); err != nil {
		t.Fatalf("codex conn should receive codex fallback event: %v", err)
	}
}
