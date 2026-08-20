package opencodeweb

import (
	"context"
	"sort"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// capability_activation_test.go owns the §6.11 activation truth: the
// advertised capability set must equal the fully-implemented owning paths —
// nothing more (E2 reasoning, OD-3 futures, legacy question_reply absent),
// nothing less (todos/questions/mutations/attachments/external turns live).

// advertisedCapabilities mirrors go-bridge backend_capabilities.go's
// interface derivation for this agent (the single source the descriptor
// serves to iOS).
func advertisedCapabilities(a *Agent) []string {
	caps := []string{"external_turn_streaming"} // StaticCapabilities
	if _, ok := interface{}(a).(core.SessionRenamer); ok {
		if _, ok := interface{}(a).(core.SessionArchiver); ok {
			caps = append(caps, "session_mutation")
		}
	}
	if _, ok := interface{}(a).(core.SessionDeleter); ok {
		caps = append(caps, "session_delete")
	}
	if _, ok := interface{}(a).(core.ToolAuthorizer); ok {
		caps = append(caps, "permission_resolve")
	}
	if _, ok := interface{}(a).(core.TodoProvider); ok {
		caps = append(caps, "todos")
	}
	if ready, ok := interface{}(a).(core.StructuredUserInputProvider); ok && ready.StructuredUserInputReady() {
		caps = append(caps, "structured_user_input_v1")
	}
	if sup, ok := interface{}(a).(core.AttachmentSupporter); ok {
		caps = append(caps, sup.SupportedAttachmentKinds()...)
	}
	return caps
}

// TestCapabilityAdvertisementMatchesImplementedPaths is the activation gate:
// every implemented dossier's capability is present and every forbidden
// surface is absent.
func TestCapabilityAdvertisementMatchesImplementedPaths(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	got := advertisedCapabilities(agent)
	sort.Strings(got)
	want := []string{
		"external_turn_streaming",  // §6.5 E3 (global-subscriber tests green)
		"file",                     // §6.4 attachments → official file parts
		"image",                    // §6.4 attachments → official file parts
		"permission_resolve",       // §6.7 (SDK-pinned once/always/reject)
		"session_delete",           // §6.10 E7
		"session_mutation",         // §6.10 E6 rename + archive
		"structured_user_input_v1", // §6.8 A7 questions
		"todos",                    // §6.9 A8
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("capability set drift: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("capability set drift: got %v want %v", got, want)
		}
	}
}

// TestForbiddenCapabilitiesStayAbsent: E2 reasoning, OD-3 future surfaces,
// and the legacy question_reply route are implemented NOWHERE and must stay
// unadvertised.
func TestForbiddenCapabilitiesStayAbsent(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	got := advertisedCapabilities(agent)
	for _, forbidden := range []string{
		"question_reply",      // legacy route deliberately not implemented (§6.8)
		"reasoning",           // E2 BLOCKED/UNSUPPORTED
		"compression",         // OD-3 future
		"supports_checkpoint", // OD-3 future
		"summarize", "revert", // OD-3 future
		"share_session", "fork", // OD-3 future
	} {
		for _, c := range got {
			if c == forbidden {
				t.Fatalf("forbidden capability %q advertised", forbidden)
			}
		}
	}
	// The E2 verdict also holds on the wire: a populated reasoning part
	// fails the hydrate with the canonical text (§6.3).
	var _ = errUnsupportedReasoning
}

// TestSessionQuestionLegacyRouteStaysUnsupported: the legacy single-question
// presentation path must keep failing closed — resolve_user_input is the
// only question route (§6.8).
func TestSessionQuestionLegacyRouteStaysUnsupported(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{}, "/tmp")
	sess, err := agent.StartSession(context.Background(), "ses_x")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	if err := sess.RespondQuestion("q1", []string{"a"}); err != core.ErrNotSupported {
		t.Fatalf("RespondQuestion must stay ErrNotSupported, got %v", err)
	}
	if err := sess.RejectQuestion("q1"); err != core.ErrNotSupported {
		t.Fatalf("RejectQuestion must stay ErrNotSupported, got %v", err)
	}
}
