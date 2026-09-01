# Live lifecycle and interaction provenance

The target live evidence (`phase0/live/attempt-008-thread-resume-live-turn-stream.json`)
contains only ordinary notifications: `turn/started`, `item/started`,
`item/agentMessage/delta`, `item/completed`, `thread/tokenUsage/updated`, and
`turn/completed`. It contains no `turn/steer` response and no app-server
server-request payload. Consequently this phase implements only the
source-backed text steer/interrupt path and a strict fail-closed boundary for
approval, `requestUserInput`, and MCP elicitation.

| Capability | Source evidence | Current Remote behavior |
| --- | --- | --- |
| `turn/steer` | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/v2/turn.rs` (`TurnSteerParams`) | `remoteSession.Steer` sends `threadId`, `expectedTurnId`, and a text-only input; the active id comes from live events or an authoritative history read. |
| `turn/interrupt` | `protocol/v2/turn.rs` (`TurnInterruptParams`); existing Phase 1 request path | `CancelTurnForThread` uses the official turn id and never fabricates one. |
| reconnect | Remote stream epoch and stable stream-id fixes in Phase 1 | Each reconnect creates a new Client epoch and a fresh event pump. No cursor is advertised because the target capture did not deliver a usable cursor. |
| approval / structured input / MCP elicitation | No payload-preserving target Remote server-request sample; upstream shapes are not sufficient to prove controller delivery | The pump drains and rejects each request with JSON-RPC `-32601`, preserving the original request id. No capability is advertised, and no local optimistic resolution is emitted. |

This is deliberate fail-closed behavior, not a fallback or fake interaction.
An owner-authorized payload capture is required before enabling any positive
server-request capability.
