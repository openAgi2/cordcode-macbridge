package claudecode

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// S1 regression (MacBridge side): claudeSession.RespondPermission must treat
// ONLY behavior=="allow" as allow. Legacy iOS snake_case values (approve,
// approve_always, reject, reject_always) and any unknown value must NOT be
// silently treated as allow — they must produce a deny control_response.
//
// This is the MacBridge half of the iOS S1 wire-behavior fix: even if a stale
// client mistakenly sends legacy values, Claude must not interpret them as
// permission to proceed. claudeSession.RespondPermission (session.go) only
// branches on `result.Behavior == "allow"`.
func TestRespondPermission_OnlyAllowIsAllow(t *testing.T) {
	allowCases := []string{"allow"}
	denyCases := []string{
		"deny", "approve", "approve_always", "reject", "reject_always",
		"", "ACCEPT", "yes", "permitted",
	}

	for _, behavior := range allowCases {
		cs, stdin := newAskTestSession(t)
		if err := cs.RespondPermission("req", core.PermissionResult{Behavior: behavior}); err != nil {
			t.Fatalf("RespondPermission(%q): %v", behavior, err)
		}
		resp := nested(t, stdin.lastJSONLine(t), "response", "response")
		if got, _ := resp["behavior"].(string); got != "allow" {
			t.Errorf("behavior=%q produced wire %q, want allow", behavior, got)
		}
	}
	for _, behavior := range denyCases {
		cs, stdin := newAskTestSession(t)
		if err := cs.RespondPermission("req", core.PermissionResult{Behavior: behavior}); err != nil {
			t.Fatalf("RespondPermission(%q): %v", behavior, err)
		}
		resp := nested(t, stdin.lastJSONLine(t), "response", "response")
		if got, _ := resp["behavior"].(string); got != "deny" {
			t.Errorf("behavior=%q produced wire %q, want deny (legacy/unknown must NOT be silently treated as allow)", behavior, got)
		}
	}
}
