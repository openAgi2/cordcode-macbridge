package gobridge

import (
	"context"
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type restoringProjectionAgent struct {
	fakeAgent
}

func (a *restoringProjectionAgent) WaitForRestore(context.Context) error {
	return core.ErrRestoreInProgress
}

func (a *restoringProjectionAgent) GetRichSessionHistory(context.Context, string, int) ([]core.RichHistoryEntry, error) {
	return nil, nil
}

func TestProjectionSourceMapsRestoreInProgressToHydrating(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex-remote", &restoringProjectionAgent{fakeAgent: fakeAgent{name: "codex-remote"}})
	_, err := handlers.prepareProjectionHydrateSource(context.Background(), "codex-remote", "session-restore", "")
	if !errors.Is(err, errProjectionHydrating) {
		t.Fatalf("err = %v, want projection hydrating", err)
	}
}

func TestProjectionRPCReturnsHydratingDuringRestore(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex-remote", &restoringProjectionAgent{fakeAgent: fakeAgent{name: "codex-remote"}})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex-remote", Method: "get_session_projection", RequestID: "restore-projection",
		Params: mustJSONRaw(t, map[string]any{"sessionId": "session-restore"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	if messages[0]["ok"] != false {
		t.Fatalf("restore window should be retryable, got %#v", messages[0])
	}
	errObject, _ := messages[0]["error"].(map[string]any)
	if errObject["code"] != "projection.hydrating" {
		t.Fatalf("error = %#v", errObject)
	}
}
