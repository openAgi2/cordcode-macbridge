# Gate P2 adjudication

Status: **PASS for the implemented, advertised Phase 2 surface; explicit
fail-closed gaps remain unadvertised.**

## Advertised capabilities

| Capability | Source / target evidence | Remote implementation | Proof |
| --- | --- | --- | --- |
| `session_state` | Remote Client/Stream lifecycle and `thread/status/changed` source method | bridge derives from the connected Agent | `TestCodexRemoteDescriptorOnlyAdvertisesImplementedCapabilities` |
| `session_history` | official `ThreadRead` + `Thread`/`Turn`/`ThreadItem` source; schema replay fixture | `history.go` maps official turn/item identity into `TurnScopedHistoryTurn` | `history_test.go`, `history-ssv2-provenance.md`, focused bridge hydrate tests |

## Intentionally unadvertised

Model/provider selection, approvals, structured input, MCP elicitation,
archive/rename/delete/fork, pagination, compression and question reply have no
payload-preserving target Remote sample. The event pump rejects unsupported
server requests with the original JSON-RPC id and `-32601`; no local fallback,
fake success or optimistic resolution is used. See
`capabilities-provenance.md` and `live-interactions-provenance.md`.

## Boundary checks

* `thread/read` is the only history source; no rollout, SQLite, JSONL, daemon
  cache or codex-web import exists in the Remote package.
* Official `turn.id` and item ids are retained; malformed completion frames and
  unknown item/method shapes fail closed.
* The Remote envelope is unwrapped before app-server decode and its sequence
  id is never used as a projection identity.
* `git diff --exit-code -- agent/codex-web agent/codex` is required and passed
  for this gate.
* The latest real live observation remains owner-attested and redacted; its
  payload omission means schema fixtures are replay evidence only, not a claim
  that unobserved Remote capabilities work in production.
