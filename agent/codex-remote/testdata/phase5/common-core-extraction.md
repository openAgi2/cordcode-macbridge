# Phase 5 common-core extraction

## Extracted surface

`agent/codex-appserver/rpc` now owns the algorithm that was duplicated in
`codex-web/rpc.go` and `codex-remote/rpc.go`:

- monotonically assigned JSON-RPC request ids;
- concurrent response/error correlation by original id;
- ordered notification and server-request routing;
- preservation of epoch, request id, thread id, turn id and raw params;
- request/notification/response/error framing;
- timeout/cancellation pending cleanup;
- bounded transport close and pending failure propagation.

The two backend files are policy adapters. They retain their historical error
text and deliberately different liveness semantics: codex-web topology observes
explicit local close, while codex-remote supervision also treats reader death as
closed so it can reconnect.

## Not extracted

Codec, history, session, interaction and model logic remain separate. Similar
method names do not establish identical contracts: Remote has a sampled subset,
different lifecycle/transport ownership, and deliberately omits interaction and
model capabilities.

## Official source anchors

- `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/remote.rs:216-472`
- `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/remote.rs:493-606`
- `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/lib.rs:333-409`

The extraction changes no `agent/codex/` file and does not merge backend ids,
transport epochs, lifecycle, capabilities or diagnostics.
