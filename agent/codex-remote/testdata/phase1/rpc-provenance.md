# rpc.go whitelist provenance

- Source repository: this MacBridge worktree
- Source path: `agent/codex-web/rpc.go`
- Source commit: `c6fa9b853843e8682e94fd3f167d2e998cd2d0ce`
- Copied: 2026-08-28
- Destination: `agent/codex-remote/rpc.go`
- Kept: JSON-RPC id correlation, single reader, server-request channel, bounded Close
- Removed: shared-daemon / UDS / connection-#2 broadcast assumptions
- Transport: Remote Control environment stream envelopes, not daemon WebSocket-over-UDS
- Parity tests: `rpc_test.go`, `stream_test.go`, `session_test.go`
