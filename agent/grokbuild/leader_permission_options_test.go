package grokbuild

// Permission options pass-through tests (design §25 second block).
//
// The vocabulary maps are source-first against grok-build permission/
// prompter.rs @72a61251: the options a broadcast carries depend on the
// session creator's client tier (TUI full set incl. dynamic always/reject-
// always inserts, generic web set, edit/agent-message subsets), so the
// bridge must translate WHAT ARRIVED — never hardcode per access kind.
// Wire kind values are agent_client_protocol 0.10.4 PermissionOptionKind
// (snake_case: allow_once/allow_always/reject_once/reject_always).

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// funcScript builds a runLeaderSubscriber script that injects raw frames with
// a settle delay after each.
func funcScript(frames ...string) func(c net.Conn) error {
	return func(c net.Conn) error {
		for _, f := range frames {
			if err := writeACPRequestRaw(c, f); err != nil {
				return err
			}
			time.Sleep(150 * time.Millisecond)
		}
		return nil
	}
}

func opts(kinds ...string) []permissionOption {
	out := make([]permissionOption, 0, len(kinds))
	for i, k := range kinds {
		out = append(out, permissionOption{OptionID: fmt.Sprintf("opt_%d", i), Name: k, Kind: k})
	}
	return out
}

func actionsEqual(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TUI bash tier order (prompter.rs build_options_inner): dynamic
// always-allow-command first, then the base bash set, then dynamic
// reject-always-command.
func TestPermissionOptionActions(t *testing.T) {
	if got := permissionOptionActions(opts("allow_always", "allow_once", "reject_once", "reject_always")); !actionsEqual(got, "approve", "approveAlways", "reject") {
		t.Errorf("tui bash tier = %v, want [approve approveAlways reject] (reject_always unmapped)", got)
	}
	// Generic web set: always-allow, allow-once, reject-once, reject-always.
	if got := permissionOptionActions(opts("allow_always", "allow_once", "reject_once", "reject_always")); !actionsEqual(got, "approve", "approveAlways", "reject") {
		t.Errorf("generic tier = %v", got)
	}
	// Edit set: allow_always (allow-edits-for-session), allow_once, reject_once.
	if got := permissionOptionActions(opts("allow_always", "allow_once", "reject_once")); !actionsEqual(got, "approve", "approveAlways", "reject") {
		t.Errorf("edit tier = %v", got)
	}
	// Agent-message set: allow_once, reject_once only.
	if got := permissionOptionActions(opts("allow_once", "reject_once")); !actionsEqual(got, "approve", "reject") {
		t.Errorf("agent-message tier = %v", got)
	}
	// Degraded/replay order — canonical order regardless of arrival order.
	if got := permissionOptionActions(opts("reject_once", "allow_once", "reject_once", "allow_once")); !actionsEqual(got, "approve", "reject") {
		t.Errorf("order = %v, want canonical [approve reject]", got)
	}
	// Non-permission kinds (exit_plan_mode vocabulary) skipped.
	if got := permissionOptionActions(opts("followup", "cancelled", "error")); len(got) != 0 {
		t.Errorf("non-permission kinds = %v, want empty", got)
	}
	if got := permissionOptionActions(nil); len(got) != 0 {
		t.Errorf("nil options = %v, want empty", got)
	}
}

func TestGrokPermissionKind(t *testing.T) {
	for kind, want := range map[string]string{
		"execute": "bash",
		"read":    "read",
		"fetch":   "fetch",
		"edit":    "edit",
		"":        "grok",
		"custom":  "grok",
	} {
		if got := grokPermissionKind(kind); got != want {
			t.Errorf("grokPermissionKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestPermissionOutcomeKindPrecise: "always" selects allow_always when
// offered and degrades to allow_once when not; deny prefers reject_once over
// reject_always (persistent deny is a stronger side effect than asked for);
// §24 compat holds (no reject at all → cancelled).
func TestPermissionOutcomeKindPrecise(t *testing.T) {
	tuiBash := opts("allow_always", "allow_once", "reject_once", "reject_always")
	o, err := permissionOutcome(tuiBash, "always")
	if err != nil || o.OptionID != "opt_0" || o.Outcome != "selected" {
		t.Errorf("always on tui set = %+v (%v), want selected opt_0 (allow_always)", o, err)
	}
	o, err = permissionOutcome(tuiBash, "allow")
	if err != nil || o.OptionID != "opt_1" {
		t.Errorf("allow on tui set = %+v (%v), want opt_1 (allow_once preferred)", o, err)
	}
	o, err = permissionOutcome(tuiBash, "deny")
	if err != nil || o.OptionID != "opt_2" {
		t.Errorf("deny on tui set = %+v (%v), want opt_2 (reject_once preferred over reject_always)", o, err)
	}
	// agent-message tier: no allow_always — always degrades to allow_once.
	agentMsg := opts("allow_once", "reject_once")
	o, err = permissionOutcome(agentMsg, "always")
	if err != nil || o.OptionID != "opt_0" {
		t.Errorf("always degrade = %+v (%v), want allow_once opt_0", o, err)
	}
	// reject_always only — still better than cancelled.
	rejectAlwaysOnly := opts("allow_once", "reject_always")
	o, err = permissionOutcome(rejectAlwaysOnly, "deny")
	if err != nil || o.OptionID != "opt_1" || o.Outcome != "selected" {
		t.Errorf("deny reject_always-only = %+v (%v), want selected opt_1", o, err)
	}
	// §24 compat: no reject option → cancelled.
	if o, _ = permissionOutcome(opts("allow_once"), "deny"); o.Outcome != "cancelled" {
		t.Errorf("deny without reject = %+v, want cancelled", o)
	}
	if _, err = permissionOutcome(opts("reject_once"), "allow"); err == nil {
		t.Error("allow without allow option must error")
	}
}

// TUI-tier broadcast fixture: execute kind, full four-option set (dynamic
// always/reject-always command inserts included, prompter.rs order).
const fixtureRequestPermissionTUIBash = `{"jsonrpc":"2.0","id":11,"method":"session/request_permission","params":{"sessionId":"sess-1","toolCall":{"toolCallId":"call_tui_bash","title":"rm -rf build","kind":"execute","status":"pending"},"options":[{"optionId":"always-allow-command","name":"Always allow","kind":"allow_always"},{"optionId":"allow-once","name":"Yes, proceed","kind":"allow_once"},{"optionId":"reject-once","name":"No","kind":"reject_once"},{"optionId":"reject-always-command","name":"No, and don't ask again","kind":"reject_always"}]}}`

// TestLeaderSubscriberPermissionOptionsPassedThrough: the surfaced
// permission_request carries the offered actions (allow_always → always
// button) and the mapped official kind (execute → bash).
func TestLeaderSubscriberPermissionOptionsPassedThrough(t *testing.T) {
	got, _ := runLeaderSubscriber(t, funcScript(fixtureRequestPermissionTUIBash))
	perms := filterEvents(got, core.EventPermissionRequest)
	if len(perms) != 1 {
		t.Fatalf("permission_request count = %d, want 1: %+v", len(perms), got)
	}
	if !actionsEqual(perms[0].PermissionActions, "approve", "approveAlways", "reject") {
		t.Errorf("actions = %v, want [approve approveAlways reject]", perms[0].PermissionActions)
	}
	if perms[0].PermissionKind != "bash" {
		t.Errorf("kind = %q, want bash", perms[0].PermissionKind)
	}
}

// TestLeaderSubscriberPlanActionsBinary: the plan card advertises exactly
// approve/reject with no official kind (generic style; a plan has no tool
// category).
func TestLeaderSubscriberPlanActionsBinary(t *testing.T) {
	got, _ := runLeaderSubscriber(t, funcScript(fixtureExitPlanMode))
	perms := filterEvents(got, core.EventPermissionRequest)
	if len(perms) != 1 {
		t.Fatalf("permission_request count = %d, want 1", len(perms))
	}
	if !actionsEqual(perms[0].PermissionActions, "approve", "reject") {
		t.Errorf("plan actions = %v, want [approve reject]", perms[0].PermissionActions)
	}
	if perms[0].PermissionKind != "" {
		t.Errorf("plan kind = %q, want empty", perms[0].PermissionKind)
	}
}
