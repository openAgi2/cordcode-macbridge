# Relay Outbound Classes

All authenticated Relay application data is submitted to the single Relay data writer. Direct connections keep their existing independent write path.

| Class | Sources | Scheduling |
|---|---|---|
| `control` | handshake transition, heartbeat/ping, abort/reconnect, recovery barrier/complete/ack | Highest; ingress FIFO with burst cap. |
| `interactive` | send-message, permission/question responses, token/turn delta events | Weighted ahead of metadata and bulk. |
| `metadata` | models, modes, git branch, todos and session-state events | Weighted ahead of bulk. |
| `normal` | Unclassified results/events | Safe default with diagnostic; never raw-write. |
| `bulk` | history, recovery snapshots and complete large lists | Lowest; only class eligible for chunking. |

Result/error class comes from the inbound requestId registry. Recovery is always control. Events carry a typed hint assigned by this inventory: text/thinking/tool/message deltas, turns, permission and question events are interactive; session status, todos, model/mode/branch changes are metadata; recovery barrier/complete is control; unknown events are normal with diagnostics.

After writer-ready, application data cannot bypass the writer. Only WebSocket `WriteControl` and the narrow pre-writer hello acknowledgement/error transition are exempt; application ping/heartbeat is not.

`relayUnifiedWriterV1` and its legacy data-write branch are temporary rollout controls sampled only when a new secure epoch is created. Remove both after one complete release observation window has passed with owner limited-bandwidth acceptance complete, `relay_result_bypass_writer == 0`, `relay.chunk_unnegotiated == 0`, and no counter, reassembly, Direct, mailbox, recovery, or reconnect regression. A rollback before removal changes only new epochs; established epochs must reconnect rather than switching writers in place.
