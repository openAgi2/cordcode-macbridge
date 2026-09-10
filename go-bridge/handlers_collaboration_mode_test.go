package gobridge

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type collaborationModeFakeAgent struct {
	*fakeAgent
	mu         sync.Mutex
	ready      bool
	catalog    core.SessionCollaborationModeCatalog
	updates    []string
	updateHook func()
	updateErr  error
}

func (a *collaborationModeFakeAgent) CollaborationModeReady() bool { return a.ready }
func (a *collaborationModeFakeAgent) CurrentSessionCollaborationMode(string) (core.SessionCollaborationMode, bool) {
	return a.catalog.Current, a.catalog.Current.Mode != ""
}
func (a *collaborationModeFakeAgent) ListSessionCollaborationModes(context.Context, string) (core.SessionCollaborationModeCatalog, error) {
	return a.catalog, nil
}
func (a *collaborationModeFakeAgent) UpdateSessionCollaborationMode(_ context.Context, sessionID, preset string) error {
	a.mu.Lock()
	a.updates = append(a.updates, sessionID+":"+preset)
	hook := a.updateHook
	a.mu.Unlock()
	if hook != nil {
		hook()
	}
	return a.updateErr
}

func readyCollaborationFake() *collaborationModeFakeAgent {
	effort := "medium"
	return &collaborationModeFakeAgent{
		fakeAgent: &fakeAgent{name: "codex-remote"}, ready: true,
		catalog: core.SessionCollaborationModeCatalog{
			Current: core.SessionCollaborationMode{Mode: "plan", Model: "thread-model", ReasoningEffort: &effort},
			Presets: []core.SessionCollaborationModePreset{{Name: "Plan", Mode: "plan"}, {Name: "Default", Mode: "default"}},
		},
	}
}

func registerCollaborationSession(h *Handlers, agent *collaborationModeFakeAgent, sessionID string) {
	h.RegisterAgent("codex-remote", agent)
	h.mu.Lock()
	h.putSessionWithMeta(sessionID, "codex-remote", "", &fakeAgentSession{id: sessionID, events: make(chan core.Event, 1)})
	h.mu.Unlock()
}

func TestCollaborationModeListAndUpdateHandlers(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h := newTestHandlers(t)
	agent := readyCollaborationFake()
	registerCollaborationSession(h, agent, "thread")
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "list_collaboration_modes", RequestID: "list", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread"})})
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "update_collaboration_mode", RequestID: "update", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread", "preset": "Default"})})
	messages := readJSONMaps(t, clientConn, 2)
	byID := map[string]map[string]any{}
	for _, message := range messages {
		byID[message["requestId"].(string)] = message
	}
	listData := byID["list"]["data"].(map[string]any)
	if len(listData["presets"].([]any)) != 2 || listData["current"].(map[string]any)["model"] != "thread-model" {
		t.Fatalf("list data = %#v", listData)
	}
	if byID["update"]["data"].(map[string]any)["accepted"] != true {
		t.Fatalf("update = %#v", byID["update"])
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.updates) != 1 || agent.updates[0] != "thread:Default" {
		t.Fatalf("updates = %v", agent.updates)
	}
}

func TestCollaborationModeReadinessAndWriteGuardFailClosed(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h := newTestHandlers(t)
	agent := readyCollaborationFake()
	agent.ready = false
	registerCollaborationSession(h, agent, "thread")
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "list_collaboration_modes", RequestID: "closed", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread"})})
	agent.ready = true
	release, ok := h.tryBeginNativeSessionWrite("codex-remote", "thread")
	if !ok {
		t.Fatal("initial guard acquisition failed")
	}
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "update_collaboration_mode", RequestID: "busy", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread", "preset": "Plan"})})
	release()
	messages := readJSONMaps(t, clientConn, 2)
	codes := map[string]string{}
	for _, message := range messages {
		codes[message["requestId"].(string)] = message["error"].(map[string]any)["code"].(string)
	}
	if codes["closed"] != "unsupported_capability" || codes["busy"] != "session_action_in_progress" {
		t.Fatalf("codes = %v", codes)
	}
	if len(agent.updates) != 0 {
		t.Fatalf("closed/busy path mutated: %v", agent.updates)
	}
}

func TestCollaborationModeUpdateErrorDoesNotReturnAccepted(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h := newTestHandlers(t)
	agent := readyCollaborationFake()
	agent.updateErr = fmt.Errorf("official rejection")
	registerCollaborationSession(h, agent, "thread")
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "update_collaboration_mode", RequestID: "rejected", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread", "preset": "Plan"})})
	message := readJSONMaps(t, clientConn, 1)[0]
	if message["data"] != nil || message["error"].(map[string]any)["code"] != "collaboration_update_failed" {
		t.Fatalf("rejected update = %#v", message)
	}
}

func TestCollaborationCapabilityRequiresControllerAndReadiness(t *testing.T) {
	if caps := deriveBackendCapabilities("codex-remote", &closedCompactionAgent{projectionAttachAgent: &projectionAttachAgent{}}, ""); containsString(caps, "session_collaboration_mode") {
		t.Fatalf("readiness alone advertised capability: %v", caps)
	}
	agent := readyCollaborationFake()
	if caps := deriveBackendCapabilities("codex-remote", agent, ""); !containsString(caps, "session_collaboration_mode") {
		t.Fatalf("ready controller did not advertise capability: %v", caps)
	}
	agent.ready = false
	if caps := deriveBackendCapabilities("codex-remote", agent, ""); containsString(caps, "session_collaboration_mode") {
		t.Fatalf("closed controller advertised capability: %v", caps)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
