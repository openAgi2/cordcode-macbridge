# History/SSV2 provenance

| Boundary | Evidence | Implementation / assertion |
| --- | --- | --- |
| Official request/response | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/common.rs` (`ThreadRead`) and `protocol/v2/thread_data.rs` (`Thread`, `Turn`, `TurnError`), source inspected at `25a6e316c81fb7600d1d75f3e63ffe26be10b7c8` | `readThreadWithTurns` sends only `threadId` and optional `includeTurns`, validates the returned official thread id, and preserves official turn status/time/error. |
| Official item variants | `protocol/v2/item.rs` (`ThreadItem`, `FileUpdateChange`, `McpToolCall`, `DynamicToolCall`) at the same source commit | `decodeRemoteThreadItem` whitelists user/agent/reasoning/command/file/MCP/dynamic/plan/web/context items; all other types go to `SkippedTypes`. |
| Remote envelope | `phase0/live/attempt-008-thread-resume-live-turn-stream.json` proves the target envelope delivered the ordinary `thread/resume` and turn/item methods; payload values were redacted. `thread-read-remote-envelope.json` is a schema replay, not live evidence. | Envelope routing is decoded by `stream.go`/`rpc.go`; the history mapper sees only the unwrapped app-server JSON and never uses `seqId` as a projection identity. |
| Bridge mapping | `core.TurnScopedHistoryTurn` and `go-bridge/handlers_projection.go` pathless `codex-remote` family | `TurnID` is official `turn.id`; item ids are preserved in `itemId`; pathless hydrate dispatches through `streamTurnScopedRichHistoryProjectionEvents`; no rollout/cache fallback. |
| Activity truth | `protocol/v2/thread_data.rs` `ThreadStatus` | `IsSessionActive` reads `thread/read` without turns and returns active for the official `active` status or any error/unknown value. |

The positive decoder tests intentionally use the schema fixture only. Until a
new owner-authorized Remote capture preserves item payloads, this evidence
does not advertise approvals, structured input, MCP elicitation, or any other
unsampled server-request capability.
