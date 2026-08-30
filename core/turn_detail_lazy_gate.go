package core

// TurnDetailLazyProductionEnabled is THE single production gate for
// turn_detail_lazy_v1 (unified-bridge-protocol §11.7 / lazy-history plan
// Phase 3 flip, 2026-08-30). It governs BOTH advertisement surfaces at once:
//
//   - hello_ack echo: go-bridge main.go wires it into
//     Server.SetTurnDetailLazyEnabled (direct + relay negotiation);
//   - backend descriptor: agent/codex-remote WireDescriptor gates its
//     StaticCapabilities entry on this const — and the iOS「加载详细过程」
//     entry gates on THAT descriptor claim, not on the echo.
//
// Gating both surfaces with one const is what makes rollback a true one-liner:
// set false → the echo stops AND the descriptor stops listing the capability →
// iOS hides the entry before any request can be made (no click → no
// capability_required round trip). Peers are byte-identical to pre-flip.
//
// Tests exercise the runtime switch through Server.SetTurnDetailLazyEnabled
// (default false on a bare Server); production truth lives here only.
const TurnDetailLazyProductionEnabled = true
