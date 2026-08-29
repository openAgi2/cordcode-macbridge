# Phase 4 coexistence and rollback contract

`codex-web` and `codex-remote` remain separate registered agents, descriptors,
connection epochs, session IDs, cache scopes, and diagnostics. The MacBridge
topology monitor obtains its identity from `codex-web` only; Remote pairing and
environment state are read through the Remote agent's own management endpoint.
No path merges sessions by a bare thread ID.

Rollback is additive and reversible:

1. stop advertising/starting `codex-remote` through its own feature/registration
   change;
2. revoke/delete only MacBridge's Remote controller enrollment and device key;
3. leave `codex-web`, its local daemon/UDS state, Desktop settings, Codex auth,
   sessions, and SQLite untouched;
4. keep `codex-web` and its existing management/topology endpoint available during
   the observation window.

The implementation has no auto-retirement path and no Remote failure handler that
stops or restarts the shared daemon. A failed Remote phase is exposed through its
own pairing/status and descriptor state. A failed local-daemon sample is exposed in
the local topology dimensions and does not mutate Remote state.

This document records the rollback boundary; it is not a live proof that every
controller ownership/revoke scenario has been exercised.
