# Phase 5 authorization gate

Phase 5 is an explicitly separate follow-up in the implementation plan. It may
start only after both backends have real end-to-end evidence, a stable observation
window, and an explicit owner authorization to extract a common core.

Current evidence establishes the implemented Remote surface, owner-confirmed
bidirectional projection, focused race coverage, and independent topology/rollback
boundaries. It does not establish the required long observation window or authorize
the transport-neutral `codex-appserver` extraction.

Therefore no Phase 5 implementation, refactor, or speculative duplication cleanup
is entered. The authorization/audit todo is recorded as **blocked** with the
concrete reason “explicit owner authorization after the stable observation window
is missing”; dependent Phase 5 todos remain pending. This preserves the working
`codex-web` and `codex-remote` identities and keeps rollback reversible.
