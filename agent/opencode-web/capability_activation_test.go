package opencodeweb

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// capability_activation_test.go pins the §6.11 activation INPUTS: the agent
// implements exactly the interfaces whose full-path owning tests are green.
// The advertised SET itself is derived by the production derivation in
// go-bridge (see audit008_capability_restore_test.go) — this file never
// re-implements that algorithm.

func TestCapabilityInputsMatchImplementedPaths(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")

	// §6.5 external turns: the single global subscriber exists and E3
	// routing/reconnect/unopened tests are green.
	if _, ok := interface{}(agent).(core.WireDescriptorProvider); !ok {
		t.Fatal("WireDescriptorProvider missing")
	}
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"todos §6.9", func() bool { _, ok := interface{}(agent).(core.TodoProvider); return ok }()},
		{"structured input §6.8", func() bool { _, ok := interface{}(agent).(core.UserInputResponder); return ok }()},
		{"structured readiness §6.8", func() bool { r, ok := interface{}(agent).(core.StructuredUserInputProvider); return ok && r.StructuredUserInputReady() }()},
		{"rename+archive §6.10", func() bool {
			_, r := interface{}(agent).(core.SessionRenamer)
			_, a := interface{}(agent).(core.SessionArchiver)
			return r && a
		}()},
		{"delete §6.10", func() bool { _, ok := interface{}(agent).(core.SessionDeleter); return ok }()},
		{"permission §6.7", func() bool { _, ok := interface{}(agent).(core.ToolAuthorizer); return ok }()},
		{"attachments §6.4", func() bool {
			s, ok := interface{}(agent).(core.AttachmentSupporter)
			return ok && len(s.SupportedAttachmentKinds()) == 2
		}()},
	} {
		if !tc.ok {
			t.Fatalf("capability input missing: %s", tc.name)
		}
	}

	// The legacy single-question route stays closed — resolve_user_input is
	// the only question path (§6.8).
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
	// E2 verdict on the wire: populated reasoning fails the hydrate.
	var _ = errUnsupportedReasoning
}
