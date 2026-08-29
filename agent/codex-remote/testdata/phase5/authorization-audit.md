# Phase 5 authorization and duplication audit

Checked at: 2026-08-29T07:42:28Z

## Entry evidence

- Real E2E: owner-confirmed Desktop ↔ iPhone bidirectional projection and send/reply.
- Observation window: installed runtime PID 15670 was `ready`, `online=true`, and
  had 53m56s uptime when sampled.
- Recovery behavior: the stream bound at 14:48:25, disconnected once at 14:49:27,
  rebound at 14:49:31, and did not enter a reconnect loop. Three old stream ids
  were rejected at 14:50:41; no later stale-stream or stream-loss entry appeared
  during the remaining observation window.
- Authorization: after the Phase 5 boundary was reported, the owner repeatedly
  instructed the task to continue executing the remaining work.

## Upstream contract

The common RPC core follows the official Codex app-server client rather than a
backend-specific transport guess:

- `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/remote.rs:216-472`
  owns the pending-request map and routes responses/errors by original request id.
- `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/remote.rs:493-606`
  exposes request, notification, server-request resolve/reject, ordered events and
  bounded shutdown.
- `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/lib.rs:333-409`
  keeps request handling independent from event consumption.

## Duplication decision

| Area | Evidence | Decision |
| --- | --- | --- |
| RPC | `agent/codex-web/rpc.go` and `agent/codex-remote/rpc.go` share the single reader, pending-id map, notification/server-request routing, framing, timeout/cancel and bounded close algorithm. | Extract into `agent/codex-appserver/rpc`; retain thin backend policy wrappers. |
| Codec | Similar method names, but different item schemas, supported notifications and capability claims. | Keep separate. |
| History | Remote uses its sampled `thread/read` projection; codex-web has a broader mature history surface. | Keep separate. |
| Sessions | Lifecycle, ownership, transport acquisition and reconnect semantics differ. | Keep separate. |
| Interactions | codex-web resolves sampled interactions; Remote deliberately rejects and does not advertise them. | No common extraction. |
| Models | codex-web advertises sampled model/config surfaces; Remote does not. | No common extraction. |

The extraction must not merge wire identities, lifecycle, transport epochs,
capabilities or diagnostics. `agent/codex/` remains unchanged.
