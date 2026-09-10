# C5 failure, concurrency, and recovery matrix (2026-09-10)

This is a proof index for the Codex Remote native-action gate. It distinguishes live
observations from deterministic injection and source-defined behavior. It does not turn an
untested mutation into a live claim.

## Runtime observations

| Scenario | Evidence | Classification |
| --- | --- | --- |
| Compact empty acknowledgement and lifecycle | `native-actions-live-2026-09-10.json` records the empty `thread/compact/start` result, one official `contextCompaction` item, its owning turn/item IDs, and a completed terminal status. | Live, signed Desktop 26.903.61454 / Codex 0.153.4. The acknowledgement means submitted, not completed. |
| Plan competing/live update ordering | The same signed-runtime fixture records complete `thread/settings/updated` state. `plan-cold-live-2026-09-10.json` records Plan and Default after separate foreground-runtime restarts through `thread/settings/get`. | Live. A complete notification that races an older read wins through the codec version fence; restart state comes from the authoritative read, never an iOS cache. |
| Goal empty/set/clear and response/notification ordering | `native-actions-live-2026-09-10.json` records null get, set with full counters, clear, and null get after clear, including ordering. | Live, signed runtime. Response and notification feed the same versioned reducer. |
| Controller/runtime restart | `plan-cold-live-2026-09-10.json` records two process deaths/restarts, stable isolated environment identity, resume/readback convergence, revocation, rejected post-revocation refresh, key deletion, and isolated-home deletion. | Live, isolated patched runtime. No signed bundle modification. |

## Failures and concurrency

| Scenario | Owning contract/test | Result |
| --- | --- | --- |
| Invalid or empty action input | `collaboration_mode_test.go`, `goal_test.go`, and bridge handler validation tests | Rejected before mutation; no placeholder/default value is substituted. |
| Unknown thread, identity mismatch, attach failure, permission or parent-owned rejection | `prepareProjectionLiveSession` and native handlers preserve typed local admission errors; adapter `RPCError` is returned verbatim by Compact/Plan/Goal operations. | Explicit failure; no fallback route, fake success, privilege escalation, or automatic replay. Parent-owned rejection remains an official RPC error, not a separately repeated live mutation. |
| Timeout or disconnect after send | `Client.RequestContext` performs one request. Native handlers impose a single 25-second end-to-end context and make no second mutation call. | Outcome remains an error/unknown result. Readback may reconcile Plan/Goal after reconnect; the write is never retried automatically. |
| Same-thread double action and Send/approval race | `Handlers.tryBeginNativeSessionWrite` keys the non-queuing flight by `(backendID, sessionID)` and is shared by Send, Compact, Plan, Goal, and plan-review response paths. | The second write receives `session_action_in_progress`; different sessions/backends remain independent. |
| Desktop starts a turn after the idle check | Upstream `core/src/tasks/mod.rs` defines replacement through `abort_all_tasks(ReplacementReason::Replaced)` when the compact task starts. | Known non-atomic upstream boundary. CordCode checks authoritative activity before submit but does not claim a compare-and-set guarantee, interrupt Desktop, or compensate/replay. |
| Empty Compact ACK followed by unrelated activity | The adapter accepts only the strict empty object and does not assign a request-local turn ID. Codec/projection use official turn/item identities only. | Unrelated turn completion is not claimed as the phone request's success. |
| Stale response versus newer notification | Collaboration and Goal reducers capture the pre-request version and apply the response only if no newer notification advanced it. Connection rebind resets epoch-scoped state before attach readback. | Newer authoritative state wins; an old response cannot overwrite it or cross a controller epoch. |
| Older signed runtime lacks `thread/settings/get` | Attach tolerates only RPC `-32601`; no collaboration state is inserted. List/update continue to fail closed until a complete settings notification exists. | Compatibility without fabricated state. All other read errors fail attachment. |

## Deliberate non-claims

- There is no server CAS proving Compact can never replace a Desktop turn in the idle-check race.
- There is no mutation retry after a timeout, disconnect, or missing acknowledgement.
- A signed Codex 0.153.4 cold attach cannot reconstruct collaboration mode until a full settings
  notification arrives; the patched runtime is the authoritative cold-read target.
- Invalid/parent-owned/permission-error cases were not re-triggered as extra live writes merely to
  duplicate deterministic protocol and source evidence.
