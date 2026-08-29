# Codex app-server common core

This directory contains only behavior proven identical across `codex-web` and
`codex-remote` after the Phase 5 authorization audit.

Currently shared:

- `rpc/`: request-id correlation, notification/server-request routing,
  JSON-RPC response/error framing, cancellation/timeout cleanup and bounded
  shutdown.

Intentionally backend-owned:

- transport dialing and Remote controller envelopes;
- lifecycle, authentication, reconnect and topology state;
- codecs, history mapping, sessions, interactions and model/config surfaces;
- wire identity, epochs as interpreted by supervision, capabilities and
  diagnostics.

The upstream behavioral anchors are the official Codex sources under
`codex-rs/app-server-client/src/lib.rs` and `remote.rs`, recorded in
`agent/codex-remote/testdata/phase5/authorization-audit.md`.
