package gobridge

// turn_detail_lazy_v1 production flip gate (lazy-history plan Phase 3, 2026-08-30).
//
// Canonical authority: docs/protocol/unified-bridge-protocol.md §11.7 +
// docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md. The flip
// followed the frozen client-first release ordering: the iOS Phase 3 client
// (descriptor-gated entry, session_turn_items state machine, completion =
// appliedRev >= ack.syncRev) shipped and its suite went green BEFORE this
// server-side enable.
//
// These source assertions pin the gate discipline the owner required at
// proof-closure review: the hello echo AND the backend descriptor must ride the
// SAME production const (core.TurnDetailLazyProductionEnabled), so rollback is
// a true one-liner that hides the iOS entry before any request instead of
// leaving a click → capability_required round trip behind.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestTurnDetailLazyProductionFlipGate(t *testing.T) {
	if !core.TurnDetailLazyProductionEnabled {
		t.Fatal("core.TurnDetailLazyProductionEnabled must be true (Phase 3 flip, 2026-08-30); rollback is a deliberate owner decision, not a regression")
	}
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSrc := string(raw)
	// Exactly one deliberate enable, wired to the shared const (not a literal) so
	// echo and descriptor can never drift apart.
	enableCalls := strings.Count(mainSrc, "SetTurnDetailLazyEnabled(core.TurnDetailLazyProductionEnabled)")
	if enableCalls != 1 {
		t.Fatalf("main.go must contain exactly one SetTurnDetailLazyEnabled(core.TurnDetailLazyProductionEnabled) (found %d)", enableCalls)
	}
	if !strings.Contains(mainSrc, "turn_detail_lazy_v1 release gate") || !strings.Contains(mainSrc, "Rollback = set that one const false") {
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

// TestTurnDetailLazyDescriptorRidesSameGate pins the second advertisement
// surface: the codex-remote WireDescriptor StaticCapabilities (which the iOS
// entry gates on, CCCodeBridgeBackendClient.supportsTurnDetailLazy) must be
// derived from the SAME const the hello echo uses — not an independent literal.
func TestTurnDetailLazyDescriptorRidesSameGate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "agent", "codex-remote", "wire_descriptor.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "core.TurnDetailLazyProductionEnabled") {
		t.Fatal("wire_descriptor.go must gate StaticCapabilities on core.TurnDetailLazyProductionEnabled (shared gate; independent literal would break one-line rollback)")
	}
	// No ungated literal declaration may sneak back in.
	if strings.Contains(src, `[]string{"turn_detail_lazy_v1"}`) && !strings.Contains(src, "if core.TurnDetailLazyProductionEnabled") {
		t.Fatal("StaticCapabilities must not declare turn_detail_lazy_v1 via an ungated literal")
	}
}
