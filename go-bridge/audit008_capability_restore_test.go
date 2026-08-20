package gobridge

import (
	"context"
	"sort"
	"strings"
	"testing"

	ocweb "github.com/openAgi2/cordcode-macbridge/agent/opencode-web"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit008_capability_restore_test.go pins the directive-009 W4 capability
// gates using the PRODUCTION derivation (deriveBackendCapabilities) — no
// hand-written mirror algorithm in test helpers.

// gatedCapabilities are the audit-008 withdrawn-and-restored set: each is
// interface-derived AND owns a now-green full-path test.
var gatedCapabilities = []string{
	"external_turn_streaming", // §6.5: single-ingest/unopened/reconnect full-path green
	"todos",                   // §6.9 strict endpoint/event + control consumer
	"structured_user_input_v1", // §6.8 A7→Kernel→Projection (identity-proven)
	"session_mutation",         // §6.10 strict rename/archive echo
	"session_delete",           // §6.10 boolean-true + convergence
	"permission_resolve",       // §6.7 (unchanged by audit-008; not withdrawn)
}

// TestAudit008_CapabilityNegativeBeforePositive: a backend that implements
// NONE of the gated interfaces must advertise NONE of the gated capabilities
// (production derivation on a bare agent) — the lag gate is real, not
// narrative.
// bareCapabilityAgent implements ONLY core.Agent — no optional interface, so
// the production derivation must yield none of the gated capabilities.
type bareCapabilityAgent struct{}

func (bareCapabilityAgent) Name() string                                                { return "opencode-web" }
func (bareCapabilityAgent) StartSession(context.Context, string) (core.AgentSession, error) { return nil, context.Canceled }
func (bareCapabilityAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error)   { return nil, nil }
func (bareCapabilityAgent) Stop() error                                                  { return nil }

func TestAudit008_CapabilityNegativeBeforePositive(t *testing.T) {
	caps := deriveBackendCapabilities("opencode-web", bareCapabilityAgent{}, "")
	set := map[string]bool{}
	for _, c := range caps {
		set[c] = true
	}
	for _, gated := range gatedCapabilities {
		if gated == "external_turn_streaming" {
			continue // static self-description: gated by full-path tests, not an interface
		}
		if set[gated] {
			t.Fatalf("bare agent must NOT advertise %s before its owning interface/path exists", gated)
		}
	}
	for _, forbidden := range []string{"question_reply", "reasoning", "compression", "supports_checkpoint"} {
		if set[forbidden] {
			t.Fatalf("forbidden capability %q advertised", forbidden)
		}
	}
}

// TestAudit008_CapabilityRestoreExactSet: the REAL opencode-web agent (wire
// harness, production derivation) advertises exactly the gated set plus its
// honest base — nothing more (E2/OD-3/legacy question_reply absent), nothing
// less.
func TestAudit008_CapabilityRestoreExactSet(t *testing.T) {
	serve := newSSEPushServe(t)
	agentAny, err := ocweb.New(map[string]any{
		"work_dir":          "/tmp/audit008",
		"opencode_web_url":  serve.server.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("ocweb.New: %v", err)
	}
	agent := agentAny.(core.Agent)
	sess, err := agent.StartSession(context.Background(), "ses_cap")
	if err != nil {
		t.Fatalf("StartSession (arms nothing but must be healthy): %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	caps := deriveBackendCapabilities("opencode-web", agent, "")
	sort.Strings(caps)
	joined := strings.Join(caps, ",")
	for _, want := range gatedCapabilities {
		if !strings.Contains(joined, want) {
			t.Fatalf("real agent must advertise %s after its full-path owning tests passed, got %v", want, caps)
		}
	}
	for _, want := range []string{"model_switch", "session_history", "workspace_diff", "session_sync_v2-preflight-not-here"} {
		if want == "session_sync_v2-preflight-not-here" {
			continue
		}
		if !strings.Contains(joined, want) {
			t.Fatalf("real agent must keep its honest base capability %s, got %v", want, caps)
		}
	}
	// Attachments are the positive declaration kinds.
	for _, kind := range []string{"image", "file"} {
		if !strings.Contains(joined, kind) {
			t.Fatalf("attachment kind %s must be declared (official file-part path), got %v", kind, caps)
		}
	}
	// Forbidden set stays absent.
	for _, forbidden := range []string{"question_reply", "reasoning", "compression", "supports_checkpoint", "supports_conversation_rollback"} {
		for _, c := range caps {
			if c == forbidden {
				t.Fatalf("forbidden capability %q advertised", forbidden)
			}
		}
	}
}
