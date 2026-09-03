package grokbuild

// session_admin_test.go covers RenameSession/DeleteSession against a re-exec
// fake ACP subprocess (same pattern as the catalog lifetime helper). The fake
// only answers the underscore-prefixed wire methods, so a regression to bare
// method names fails as "backend did not confirm success".

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const grokSessionAdminHelperArg = "--grok-session-admin-test-helper"

// Re-exec this test binary as a tiny ACP process answering the session-admin
// ext methods. Rename of a session id starting with "missing" answers with the
// official error shape (message generic, detail in data) so error-data
// passthrough is asserted.
func init() {
	for _, arg := range os.Args[1:] {
		if arg != grokSessionAdminHelperArg {
			continue
		}
		scanner := bufio.NewScanner(os.Stdin)
		encoder := json.NewEncoder(os.Stdout)
		for scanner.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					SessionID string `json:"sessionId"`
				} `json:"params"`
			}
			if json.Unmarshal(scanner.Bytes(), &req) != nil {
				continue
			}
			if req.Method == "initialize" {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocolVersion": 1,
						"agentCapabilities": map[string]any{
							"sessionCapabilities": map[string]any{"list": true},
						},
					},
				})
				continue
			}
			switch req.Method {
			case grokExtRenameMethod:
				if strings.HasPrefix(req.Params.SessionID, "missing") {
					_ = encoder.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error": map[string]any{
							"code":    -32600,
							"message": "Invalid request",
							"data":    "session not found: " + req.Params.SessionID,
						},
					})
					continue
				}
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"success": true},
				})
			case grokExtDeleteMethod:
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"success": true},
				})
			default:
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{},
				})
			}
		}
		os.Exit(0)
	}
}

func newSessionAdminTestAgent(t *testing.T) *Agent {
	t.Helper()
	a := &Agent{
		cliBin:         os.Args[0],
		cliExtraArgs:   []string{grokSessionAdminHelperArg},
		workDir:        t.TempDir(),
		catalogRefresh: make(chan struct{}, 1),
	}
	t.Cleanup(func() {
		if a.catalogClient != nil {
			_ = a.catalogClient.Close()
		}
	})
	return a
}

func assertCatalogRefreshSignaled(t *testing.T, a *Agent, want bool) {
	t.Helper()
	select {
	case <-a.catalogRefresh:
		if !want {
			t.Fatal("catalog refresh signaled, want none")
		}
	default:
		if want {
			t.Fatal("catalog refresh not signaled after successful admin RPC")
		}
	}
}

func TestRenameSessionSuccess(t *testing.T) {
	a := newSessionAdminTestAgent(t)
	info, err := a.RenameSession(t.Context(), "session-1", "new title")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if info == nil || info.ID != "session-1" || info.Summary != "new title" {
		t.Fatalf("info = %+v", info)
	}
	if info.ModifiedAt.IsZero() {
		t.Fatal("ModifiedAt not set")
	}
	assertCatalogRefreshSignaled(t, a, true)
}

func TestRenameSessionErrorCarriesData(t *testing.T) {
	a := newSessionAdminTestAgent(t)
	_, err := a.RenameSession(t.Context(), "missing-1", "t")
	if err == nil {
		t.Fatal("rename of missing session must fail")
	}
	if !strings.Contains(err.Error(), "session not found: missing-1") {
		t.Fatalf("error missing official data detail: %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid request") {
		t.Fatalf("error missing JSON-RPC message: %v", err)
	}
	assertCatalogRefreshSignaled(t, a, false)
}

func TestRenameSessionEmptyInputs(t *testing.T) {
	a := newSessionAdminTestAgent(t)
	if _, err := a.RenameSession(t.Context(), "  ", "t"); err == nil {
		t.Fatal("empty session id accepted")
	}
	if _, err := a.RenameSession(t.Context(), "s", "   "); err == nil {
		t.Fatal("blank title accepted")
	}
}

func TestDeleteSessionSuccess(t *testing.T) {
	a := newSessionAdminTestAgent(t)
	if err := a.DeleteSession(t.Context(), "session-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertCatalogRefreshSignaled(t, a, true)
}

func TestDeleteSessionEmptyID(t *testing.T) {
	a := newSessionAdminTestAgent(t)
	if err := a.DeleteSession(t.Context(), ""); err == nil {
		t.Fatal("empty session id accepted")
	}
}

// TestSessionAdminWireMethodIsUnderscoreForm locks the probe-established wire
// shape: the fake only answers _-prefixed methods, so a bare method name falls
// through to the default arm, decodes success=false, and the call errors.
func TestSessionAdminWireMethodIsUnderscoreForm(t *testing.T) {
	if !strings.HasPrefix(grokExtRenameMethod, "_x.ai/") || !strings.HasPrefix(grokExtDeleteMethod, "_x.ai/") {
		t.Fatalf("ext methods must stay underscore-prefixed on the wire: %s / %s", grokExtRenameMethod, grokExtDeleteMethod)
	}
}

// TestSessionAdminCallFormatsDataError pins extRPCError's string/detail data
// extraction against the two real data shapes the official handlers emit.
func TestSessionAdminCallFormatsDataError(t *testing.T) {
	c := &grokCatalogClient{}
	err := c.extRPCError(grokExtRenameMethod, &jsonrpcError{
		Code:    -32600,
		Message: "Invalid request",
		Data:    json.RawMessage(`"session not found: abc"`),
	})
	if err == nil || !strings.Contains(err.Error(), "x.ai/session/rename error -32600: Invalid request: session not found: abc") {
		t.Fatalf("string data render: %v", err)
	}
	err = c.extRPCError(grokExtDeleteMethod, &jsonrpcError{
		Code:    -32603,
		Message: "Internal error",
		Data:    json.RawMessage(`{"detail":"boom"}`),
	})
	if err == nil || !strings.Contains(err.Error(), `{"detail":"boom"}`) {
		t.Fatalf("object data render: %v", err)
	}
	err = c.extRPCError(grokExtRenameMethod, &jsonrpcError{Code: -32601, Message: "Method not found"})
	if err == nil || strings.Contains(err.Error(), ": :") {
		t.Fatalf("no-data render: %v", err)
	}
}

// TestAgentImplementsSessionAdminInterfaces guards the capability derivation:
// SessionDeleter flips session_delete on; SessionRenamer alone does not flip
// session_mutation (archive stays unimplemented by design).
func TestAgentImplementsSessionAdminInterfaces(t *testing.T) {
	var agent interface{} = &Agent{}
	if _, ok := agent.(core.SessionRenamer); !ok {
		t.Fatal("Agent must implement core.SessionRenamer")
	}
	if _, ok := agent.(core.SessionDeleter); !ok {
		t.Fatal("Agent must implement core.SessionDeleter")
	}
	if _, ok := agent.(core.SessionArchiver); ok {
		t.Fatal("Agent must NOT implement core.SessionArchiver (official has no archive; design §23.3)")
	}
}
