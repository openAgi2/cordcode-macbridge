package gobridge

// turn_detail_lazy_v1 production flip gate (lazy-history plan Phase 3, 2026-08-30).
//
// Canonical authority: docs/protocol/unified-bridge-protocol.md §11.7 +
// docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md. The flip
// followed the frozen client-first release ordering: the iOS Phase 3 client
// (descriptor-gated entry, session_turn_items state machine, completion =
// appliedRev >= ack.syncRev) shipped and its suite went green BEFORE this
// server-side enable. These source assertions keep the flip a conscious,
// greppable act with a one-line rollback — the same discipline as
// TestProjectionWindowProductionRolloutStaysOff.

import (
	"os"
	"strings"
	"testing"
)

func TestTurnDetailLazyProductionFlipGate(t *testing.T) {
	if !turnDetailLazyProductionEnabled {
		t.Fatal("turnDetailLazyProductionEnabled must be true (Phase 3 flip, 2026-08-30); rollback is a deliberate owner decision, not a regression")
	}
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSrc := string(raw)
	// Exactly one deliberate enable, wired to the const (not a literal) so the
	// tripwire above and the rollout stay in lockstep.
	enableCalls := strings.Count(mainSrc, "SetTurnDetailLazyEnabled(turnDetailLazyProductionEnabled)")
	if enableCalls != 1 {
		t.Fatalf("main.go must contain exactly one SetTurnDetailLazyEnabled(turnDetailLazyProductionEnabled) (found %d)", enableCalls)
	}
	if !strings.Contains(mainSrc, "turn_detail_lazy_v1 release gate") || !strings.Contains(mainSrc, "Rollback = set turnDetailLazyProductionEnabled false") {
		t.Fatal("the turn_detail_lazy_v1 rollout enable must carry the release-gate + rollback comment")
	}
	if strings.Contains(mainSrc, "SetTurnDetailLazyEnabled(false)") {
		t.Fatal("a disabled call site would mask the rollout; flip the const back instead")
	}
	// Direct + relay hello paths must both route through negotiateTurnDetailLazyV1
	// (§11.7 echo rules apply identically over relay — the iPhone's production path).
	serverRaw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(serverRaw), "negotiateTurnDetailLazyV1"); got < 2 {
		t.Fatalf("server.go must define and call negotiateTurnDetailLazyV1 (found %d occurrences)", got)
	}
	if !strings.Contains(mainSrc, "negotiateTurnDetailLazyV1") {
		t.Fatal("relay hello path (main.go) must mirror the turn_detail_lazy_v1 negotiation")
	}
}
