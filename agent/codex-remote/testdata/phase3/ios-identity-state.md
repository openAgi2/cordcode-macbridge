# Phase 3 iOS identity, cache, and lifecycle state

`codexRemote` has four independent identity dimensions:

1. wire kind/backend ID (`codex-remote`);
2. `BackendKind.codexRemote` product identity;
3. `BackendServerIdentity.cacheScopeKey`, which includes backend kind, endpoint,
   and username;
4. the SSV2 backend capability gate (`session_sync_v2`) and bridge connection
   generation.

The cache key therefore cannot merge a `codex-web` thread with a Desktop Remote
thread even when their visible thread IDs happen to be equal. The iOS client also
normalizes an empty descriptor ID to `codex-remote`, so a malformed descriptor does
not become a shared/empty cache namespace.

SSV2 is enabled per backend client from the advertised descriptor. Remote sessions
are admitted to the projection path only when that per-backend flag is present;
the global transport capability alone is not enough. Pagination is separately
disabled, so the history request is a full authoritative projection rather than a
synthetic cursor merge.

Lifecycle diagnostics distinguish an enrolled/paired Desktop backend from an
offline environment, revoked pairing, and protocol-incompatible descriptor. The
diagnostic copy is state-only: it does not synthesize a session or retain a stale
projection when the authoritative read fails.

Evidence:

* `OpenCodeiOS/OpenCodeiOSTests/BridgeTransportTests.swift` verifies wire aliases,
  display identity, creation-flow exclusion, and cache-scope separation.
* `OpenCodeiOS/OpenCodeiOSTests/CCCodeBridgePhase2Tests.swift` verifies the stable
  normalized backend ID and bridge mapping.
* `OpenCodeiOS/OpenCodeiOSTests/ChatViewModelSessionSyncV2Tests.swift` exercises
  Remote projection admission and non-SSV2 behavior.

These are source/replay checks. They do not claim that a revoked or offline Desktop
environment was observed live in this turn.
