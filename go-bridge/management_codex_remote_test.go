package gobridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type remotePairStub struct {
	mgmtFakeAgent
	snap codexremote.PairingSnapshot
}

func (s *remotePairStub) StartRemoteControl(context.Context, string, string) (codexremote.PairingSnapshot, error) {
	s.snap = codexremote.PairingSnapshot{Phase: codexremote.PairPhaseAwaitingCode, Message: "enter computer code"}
	return s.snap, nil
}

func (s *remotePairStub) SubmitRemoteControlCode(_ context.Context, code string) (codexremote.PairingSnapshot, error) {
	if strings.TrimSpace(code) == "" {
		return s.snap, errPairCode
	}
	s.snap = codexremote.PairingSnapshot{Phase: codexremote.PairPhaseReady, Message: "connected", Online: true}
	return s.snap, nil
}

func (s *remotePairStub) RemoteControlStatus() codexremote.PairingSnapshot { return s.snap }

var errPairCode = errSentinel("empty code")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

func TestMgmtCodexRemotePairingRoutes(t *testing.T) {
	stub := &remotePairStub{mgmtFakeAgent: mgmtFakeAgent{name: "codex-remote"}}
	srv := newTestMgmtServer(map[string]core.Agent{"codex-remote": stub})

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/agents/codex-remote/remote-control/start")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}
	var snap codexremote.PairingSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil || snap.Phase != "awaiting_code" {
		t.Fatalf("start snap = %+v err=%v", snap, err)
	}

	rec = httptest.NewRecorder()
	req = authJSONRequest(http.MethodPost, "/internal/agents/codex-remote/remote-control/pair", `{"manualPairingCode":"ABCD"}`)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair status = %d body=%s", rec.Code, rec.Body.String())
	}
}
