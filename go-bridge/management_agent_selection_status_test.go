package gobridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestStatusReportsAgentSelectionAssemblyIdentity(t *testing.T) {
	srv := newTestMgmtServer(map[string]core.Agent{})
	revision := uint64(9)
	srv.cfg.RequestedDrivers = []string{}
	srv.cfg.AgentSelectionRevision = &revision

	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, authRequest(http.MethodGet, "/internal/status"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["requestedDrivers"]; len(got.([]interface{})) != 0 {
		t.Fatalf("requestedDrivers=%v, want empty array", got)
	}
	if got := payload["registeredDrivers"]; len(got.([]interface{})) != 0 {
		t.Fatalf("registeredDrivers=%v, want empty array", got)
	}
	if payload["agentSelectionRevision"] != float64(9) {
		t.Fatalf("agentSelectionRevision=%v, want 9", payload["agentSelectionRevision"])
	}
}
