# Business Event Egress Inventory

This is the completed Phase 3a inventory for every go-bridge frame whose top-level `type` is `event`. Producers now submit an unstamped `LogicalEvent` to the process-scoped `EventPublisher`; only that publisher constructs the final `EventMessage` and assigns identity.

| Existing source | Route | Current construction | Consolidation requirement |
| --- | --- | --- | --- |
| `server.go: Conn.SendEvent` | one direct client | constructs `EventMessage` | Remove construction; point-to-point producers call publisher. |
| `relay_conn.go: RelayDeviceConn.SendEvent` | one Relay client | constructs `EventMessage` | Remove construction; same point-to-point publisher path as direct. |
| `handlers.go: sendSessionEvent` | subscribers/direct+Relay | increments `Handlers.seq`, constructs and broadcasts | Replace with publisher broadcast. |
| `handlers.go: broadcastIdleState` | subscribers/direct+Relay | constructs and broadcasts without seq | Replace with publisher broadcast. |
| `handlers.go: send_message running` | requester plus subscribers | constructs twice, one without seq | Publish once to the target set, with publisher deduping the requester. |
| `handlers.go: abort terminal/idle` | subscribers/direct+Relay | constructs two messages with inconsistent seq | Publish both through ordered publisher. |
| `handlers.go: diagnostics/create/switch permission` | one requesting connection | `Connection.SendEvent` | Point-to-point publisher path. |
| `handlers_opencode.go` | one requesting connection | `Connection.SendEvent` | Point-to-point publisher path. |
| `handlers_relay.go: relayEvents` | subscribers + offline mailbox | increments `Handlers.seq`, batches, then independently reconstructs offline event | Batch logical deltas before stamping; route the one stamped envelope to online and offline destinations. |
| `handlers_relay.go: synthetic terminal paths` | subscribers | independently construct terminal events | Submit logical terminal events to the batcher/publisher. |
| `main.go: passive subscription` | subscribers/direct+Relay | increments `Handlers.seq`, constructs, batches | Submit logical event to batcher, then publisher. |
| `delta_batcher.go: emit` | subscribers/direct+Relay | reconstructs merged event and reuses last upstream seq | Merge unstamped logical deltas; publisher stamps the merged event once and marks deltas non-replayable. |
| `relay_offline.go: RouteEvent` | encrypted outbox/mailbox | independently reconstructs `EventMessage` | Accept the publisher's stamped envelope; never construct another identity. |
| `device_conn_registry.go: device_revoked` | one or more device connections | raw event map | Publish a connection-scoped event with empty backend/session. |

Static guard scope: outside `event_publisher.go` and protocol/test fixtures, production Go files may not contain `EventMessage{`, `"type": "event"`, `Type: "event"`, direct business `SendEvent`, or `broadcaster.Send` with a business event. `EventPublisher` owns `eventId`, `seq`, `bridgeEpoch`, `timestamp`, and `replayable`; one logical event is stamped once regardless of destination count.

Completion evidence: the static guard finds no production bypass; concurrent publishers produce one global monotonic sequence; a blocked connection does not hold a fast connection; interleaved sessions retain publisher order; direct and Relay subscribers receive the same stamped shape; offline mailbox routing receives that same envelope; and `BeginRecovery` races with publication at one mutex-protected admission fence. `CompleteRecovery` reserves an all-or-fail outbound batch and enqueues `recovery_complete` before pending events. The complete suite and `go test -race ./go-bridge/...` pass.
