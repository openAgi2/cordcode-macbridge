# Phase 4 independent topology health

The health surfaces are intentionally split by fault domain:

* `GET /internal/topology/snapshot` is the read-only `codex-web` local-daemon
  monitor. It samples the shared transport identity, process/FD evidence,
  launchd/seat state, version compatibility, and legacy loopback dimensions.
  Its eight fixed dimensions are validated fail-closed by
  `TopologySnapshotV1.Validate`.
* `GET /internal/agents/codex-remote/remote-control/status` is the
  `codex-remote` Desktop-environment pairing/online surface. It reports only the
  redacted `PairingSnapshot` phase (`idle`, `authorizing`, `awaiting_code`,
  `ready`, `offline`, or `failed`), online bit, client type, and user-facing
  message—never tokens or pairing codes.
* `GET /internal/remote/status` remains the LAN/Tailscale/HPKE Relay connection
  diagnostic. `relay.connected` is true only when a configured provider reports a
  real connection; configuration alone is not treated as online.

This split means a Desktop private-stdio process plus a connected Remote
environment can be healthy for `codex-remote` even while the local-daemon snapshot
reports `split_present`, `partial`, or `not_applicable` for the independent
`codex-web` path. Conversely, a shared-daemon `codex-web` failure does not rewrite
the Remote pairing phase or stop its client.

`AggregateDesktop` treats `split_present` as a distinct evidence result, and
`DeriveSyncHealth` maps it to `degraded` only for the local-daemon health badge;
the value is never a global product-failure switch. Unknown/stale/permission
samples remain visible as unresolved/unknown and never default to healthy.

The monitor is enabled by default in `go-bridge/main.go`, but can be explicitly
disabled with `CODEX_TOPOLOGY_MONITOR=0`. A disabled monitor returns a valid 200
response with `state=disabled`; it does not claim healthy data.
