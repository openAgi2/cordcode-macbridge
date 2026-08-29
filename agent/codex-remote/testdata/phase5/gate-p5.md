# Gate P5 adjudication

Checked at: 2026-08-29

Verdict: **PASS**. The final build/install/runtime regression is recorded in
`validation.txt`.

## Entry gate

- The owner confirmed real Desktop ↔ iPhone bidirectional session projection,
  send and reply before authorizing the remaining work.
- The installed Remote runtime remained `ready` and `online=true` over a
  53-minute observation window. Its one observed stream loss recovered in
  3.4 seconds and did not recur as a reconnect storm.
- The authorization audit restricts Phase 5 to the one proven duplicate:
  transport-neutral JSON-RPC correlation, routing, framing and shutdown.

## Preserved backend contracts

| Contract | `codex-web` | `codex-remote` |
| --- | --- | --- |
| Identity | Remains `codex-web`; no session/cache identity is shared. | Remains `codex-remote`; controller, environment, stream and epoch identity stay Remote-owned. |
| Transport/lifecycle | Keeps its local-daemon topology and local-close observation policy. | Keeps controller/stream supervision and treats reader termination as a reconnect signal. |
| Errors | Keeps the `codexweb` presentation prefix and existing RPC error shape. | Keeps the `codex-remote` presentation prefix and existing RPC error shape. |
| Capabilities | Existing codec/history/session/interaction/model surfaces remain backend-owned. | Continues to advertise only its sampled, implemented capability subset. |
| Diagnostics | Existing local-daemon diagnostics remain independent. | Existing Remote controller/environment/stream diagnostics remain independent. |

The shared package imports neither backend, `core`, nor a concrete websocket
transport. It contains no lifecycle, authentication, connection-state,
capability, codec, history, session, interaction, model or diagnostics policy.

## Rollback

Phase 5 is a source-only refactor with no persisted-data migration or external
state change. Reverting the Phase 5 commit restores each former backend-local
RPC implementation. Either backend can still be disabled or rolled back
independently because its enrollment, transport acquisition, lifecycle,
identity and capability wiring were not moved into the shared package.

`agent/codex/` is unchanged. The iOS companion worktree is clean and no iOS
source was changed by Phase 5, so no device installation is required for this
extraction.

## Evidence

- `agent/codex-remote/testdata/phase5/authorization-audit.md`
- `agent/codex-remote/testdata/phase5/common-core-extraction.md`
- `agent/codex-remote/testdata/phase5/validation.txt`
- `agent/codex-appserver/validate/phase5-authorization.mjs`
- `agent/codex-appserver/validate/phase5-boundary.mjs`

No UI test, snapshot test, simulator automation or physical-device automation
was run for this source refactor.
