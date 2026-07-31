# Event Recovery Protocol RFC

Status: frozen for implementation. This document defines recovery semantics for bridge protocol v1 optional extensions. The canonical wire types remain in `docs/protocol/schema/bridge-v1.types.ts`. Recovery is not advertised until the server and a client complete the Phase 3 gates.

## 1. Handshake selection

Recovery extends `hello` / `hello_ack`; it does not use `register` / `register_ack` and does not depend on the retired iOS `Backend/BridgeTransport.swift` path. Direct, Relay, iOS, and remote-web use this one state machine.

The client opts in by including `"recovery_v1"` in `hello.capabilities`. The optional hello fields are `lastBridgeEpoch`, `lastSeenBySession`, and the compatibility-only `lastEventId`. `lastSeenBySession` is the recovery decision truth. `lastEventId` never overrides or fills a missing per-session cut.

An opted-in server returns root-level `hello_ack.bridgeEpoch` and `hello_ack.recovery`. Every recovery transaction has a fresh random 128-bit `recoveryId`. If either peer does not advertise `recovery_v1`, the server omits `recovery`, performs no isolation or replay work, and preserves pre-recovery behavior.

## 2. Replay correctness invariant

The protocol adopts scheme A: if a disconnect window crosses any non-replayable event, including `text_delta`, `reasoning_delta`, or `tool_output_delta`, that session is `snapshot_required`. Replaying only a terminal milestone such as `turn_completed` can never establish recovery success.

The gap ledger is authoritative even when the terminal event is replayable. If coverage cannot prove a complete interval from the client's cut to the admission fence, the server returns `snapshot_required`; if an epoch changed, it returns `full_resync`. The mandatory terminal test disconnects mid-body, completes text/reasoning/tool work offline, reconnects, and compares the final client state field-for-field with authoritative history.

## 3. Cursor persistence and acknowledgement

The client persists each session HWM at `cordcode-v2:event-cursor:<bridgeId>:<deviceId>:<backendId>:<sessionId>` as `{ eventId, seq, appliedAt }`. A cursor advances only after the event was applied and both the updated store and cursor were persisted. Background sessions whose events are not applied do not advance.

Because UI/store mutation and localStorage are not one transaction, every replayable reducer is idempotent by `eventId`. The durable inbox state machine is `received -> applying -> applied`. A crash in `applying`, a reducer failure, or a persistence failure marks that session dirty and forces snapshot reconciliation on reconnect. Duplicate replay is safe. Tests must inject persist failure, apply failure, post-apply tab crash, and duplicate replay.

Tabs coordinate through a bridge/device-scoped leader lease plus `BroadcastChannel`; only the leader owns the socket and durable inbox. Followers consume committed state. Lease takeover starts a new connection and recovers from durable cursors. Unpairing or device revocation clears all keys for that bridge/device. Epoch change clears all bridge cursors after recording sessions dirty. A normal tab close retains cursors. Per-session cuts always win over `lastEventId`.

## 4. Ordering barrier

Isolation lives in the common `EventPublisher`/connection broadcaster used by direct `server.Conn` and Relay `RelayDeviceConn`. Before sending a recovery hello, the client installs its persistent inbound listener and buffer.

`EventPublisher.BeginRecovery(conn, recoveryId)` atomically registers a recovering sink and returns an admission fence. The connection is not live pass-through until a validated `recovery_applied`. `recovery_barrier` ends replay input only. The client applies and persists through the declared cuts, sends `recovery_applied`, remains recovering, and enters live only after the matching `recovery_complete`.

## 5. Compatibility matrix and capability negotiation

| Client | MacBridge | Frozen behavior |
| --- | --- | --- |
| New | Old | New optional hello fields are ignored; absence of recovery uses existing reconnect behavior. |
| Old | New | No explicit `recovery_v1`; server omits recovery and performs no isolation/replay work. |
| New opted-in | New opted-in, same epoch and complete coverage | Replay transaction. |
| New opted-in | New opted-in, buffer eviction/TTL/gap | `snapshot_required`. |
| New opted-in | New opted-in, epoch mismatch or absent/invalid prior epoch | `full_resync`. |

`recovery_v1` is client-to-server negotiation in hello. It is enabled only when both peers implement it. Phase 2 code does not advertise it from any client or server.

## 6. Recovery transaction and atomic flush-to-live

The single model is a per-connection `pendingLiveQueue`, mandatory `recovery_applied`, a per-session cut vector, and atomic flush-to-live:

1. The server creates a non-reusable `recoveryId`.
2. In the ordered publisher, `BeginRecovery` atomically installs the recovering sink, creates the bounded queue, and captures `admissionFence`.
3. Subsequent envelopes for that connection enter only its pending queue.
4. Replay produces `replayThroughBySession`; snapshot/full resync produces authoritative HWM entries. Their exact union is stored as `cutBySession`.
5. All recovery control frames carry the same `recoveryId`; replay ends with `recovery_barrier`.
6. After apply and persistence, the client sends `recovery_applied { recoveryId, appliedThroughBySession }`.
7. The server accepts only the current transaction in `awaiting_client_apply` and an exact map match. Missing, extra, lower, or higher entries fail the connection.
8. Pending events with `seq` above the corresponding cut are flushed. Sessions first seen after admission and connection-scoped events are post-cut and flush completely.
9. In one publisher critical section, the server reserves the whole outbound batch, enqueues `recovery_complete` first, then the ordered pending envelopes, and switches to live. Socket I/O occurs outside the lock.
10. The client enters live only on the matching completion frame, including when the pending queue was empty.

The batch transfer is all-or-fail. If the bounded outbound queue cannot accept the whole batch, the connection terminates before any completion frame is visible. Limits are 1,000 envelopes, 2 MiB charged bytes, and 30 seconds. Overflow, timeout, disconnect, cut-proof failure, or ack mismatch unregisters the recovering sink and reconnects; it never drops the oldest event. The transaction states are `isolating -> awaiting_client_apply -> flushing -> live`, with failures from any pre-live state. A recently completed duplicate ack is ignored idempotently; stale IDs and old-connection acks are rejected.

Known sessions with zero replay events still have a cut entry using the confirmed cursor. Existing affected snapshot/full-resync sessions have an authoritative HWM entry. There is no scalar/global cut fallback.

Required concurrency tests delay the ack while text/tool/completion events publish, recover two sessions with different cuts, and race publication before/inside/after recovery admission.

## 7. Snapshot atomic cut and backend proof

All four providers adopt scheme A as the required implementation target: go-bridge maintains a per-session materialized snapshot updated in the same ordered publisher transaction that stamps the corresponding event. Snapshot bytes and HWM are read under the same session lock. Existing transcript or provider-history reads alone are not an atomic cut and cannot be used to advertise recovery.

| Backend | Current history source | Required truth-cut proof | Eligibility before proof |
| --- | --- | --- | --- |
| Claude | Transcript/provider reconstruction | Materialized snapshot updated with stamped Claude events; terminal persistence must be reconciled into the same state. | Must not advertise. |
| Codex | Driver/provider session history | Materialized snapshot updated with stamped Codex items/deltas and terminal state. | Must not advertise. |
| OpenCode | Provider HTTP/SSE history | Materialized snapshot updated with stamped SSE events; provider watermark is insufficient unless separately proven. | Must not advertise. |
| Grok | Driver/provider reconstructed history | Materialized snapshot with stable message/tool identity updated with stamped events. | Must not advertise. |

For every backend, the snapshot HWM must cover through the admission fence, or a provably complete buffer must cover `(HWM, admissionFence]`. Otherwise the server waits/retries conservatively or fails recovery; it never invents an HWM. Per-backend capability becomes eligible only after the implementation and race tests prove this obligation.

Each snapshot response carries the transaction `recoveryId` and HWM. The client discards inbox events at or below HWM, atomically replaces/persists authoritative history, and acks exactly that HWM. Receiving a pre-ack live event above HWM proves isolation was bypassed and fails the connection. The minimum test publishes text/tool/completion while snapshot RPC runs and verifies exact final history with every delta applied once.

## 8. Event identity, gap ledger, and EventPublisher

`EventPublisher.Publish` is the only stamping exit. It assigns `{ bridgeEpoch, seq, eventId, replayable, timestamp }` once, then routes the same envelope to direct, Relay, and all subscribers. Allocation, replay-buffer/gap append, materialized-snapshot update, and destination queueing share one ordered pipeline; per-connection ID issuance is forbidden. Delta batching occurs before stamping, so a published batch is one immutable sequenced envelope.

Non-replayable coverage uses compressed intervals where possible and otherwise tombstones charged at a fixed 96-byte metadata cost (`payloadBytes=0`, `chargedBytes>=96`). Event count, 2 MiB byte cap, and TTL include gaps/tombstones. Eviction of gap metadata conservatively yields `snapshot_required`. Pending session IDs are rebound atomically with buffer, gap, cursor, and snapshot indexes when the real session ID arrives.

`BeginRecovery` and `Publish` share the ordered critical section. `CompleteRecovery` validates the transaction and exact cut vector, reserves and enqueues completion plus flush, then enables live in that same section. Tests cover concurrent publishers, slow connections, and interleaved sessions.

## 9. Client apply protocol and full resync

Inbox transitions are `received -> applying -> applied`, and every reducer deduplicates by `eventId`. Persist-after-apply failure is safe under replay; apply failure marks the session dirty and requires snapshot reconciliation. Recovery times out after 30 seconds and reconnects. The ack is always `{ recoveryId, appliedThroughBySession }`; the client stays recovering until the matching `recovery_complete` and ignores stale completion IDs.

On `full_resync`, the foreground session is immediately authoritatively replaced. All cursor/cache entries for that bridge are cleared and background sessions marked dirty. An active generation remains visibly active-but-dirty until replacement re-evaluates its server state. Backends refresh concurrently by `backendId` and do not block each other. Only after every affected session is replaced and persisted does the client send the complete exact cut map. The server then atomically emits completion and releases the pending queue.

