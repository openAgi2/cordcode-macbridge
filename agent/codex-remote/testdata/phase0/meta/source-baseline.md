# Phase 0 source and binary baseline

Classification: **STATIC-SOURCE-BASELINE-ONLY**. This artifact does not pass Gate P0 and does not authorize a product backend or iOS wiring.

## Dual-repository P0 source manifest

```text
MacBridge repository path=/Users/jacklee/Projects/cordcode-macbridge-codex-remote
branch=codex/codex-remote-backend
commit at source gate=94000a9c480daa8cd74dc07873fbf3a49cefd78a
uncommitted state at source gate=clean after the plan-baseline commit and before exec-plan state creation
task expected branch/worktree=the same dedicated worktree and branch
paired repository=/Users/jacklee/Projects/cordcode-ios-codex-remote / codex/codex-remote-backend-ios / d0762cb9a05997b615ef4589f39afad8f4b4db04
expected product capability=an independent codex-remote controller through the official Remote Control relay into the selected ChatGPT Desktop private app-server; codex-web remains independent

iOS repository path=/Users/jacklee/Projects/cordcode-ios-codex-remote
branch=codex/codex-remote-backend-ios
commit=d0762cb9a05997b615ef4589f39afad8f4b4db04
uncommitted state=clean
task expected branch/worktree=the same paired dedicated worktree; no product source changes before Phase 3
paired repository=/Users/jacklee/Projects/cordcode-macbridge-codex-remote / codex/codex-remote-backend / 94000a9c480daa8cd74dc07873fbf3a49cefd78a
expected product capability=BackendKind.codexRemote with independent wire/backend/cache/SSV2 identity and capability-driven UI
```

The preserved implementation-plan revision is commit `94000a9c480daa8cd74dc07873fbf3a49cefd78a`. No merge, rebase, cherry-pick, reset, branch switch, or main modification was performed.

## Current target binary freeze

The installed target changed after the plan's earlier audit baseline:

| Item | Earlier plan record | Current target |
|---|---|---|
| ChatGPT Desktop | `26.820.60940` | `26.825.32147` (`CFBundleVersion=7303`) |
| Embedded Codex | `0.150.0-alpha.8` | `codex-cli 0.150.0-alpha.12.2` |
| Upstream HEAD | `25a6e316c81fb7600d1d75f3e63ffe26be10b7c8` | `94311d447587411789533c47601fd8bc9d81eb48` |

Current immutable hashes:

- `app.asar`: `0462b03e878f0e78b223b849ee14cbba0de043f2c16acebee163cb95daa622ef`
- embedded `codex`: `67ea03c98e7726eeebd161bc3bc92d8937f412f1899790a28e4ee9b80803c4d7`

The old alpha.8 result remains historical evidence only. It is not used as the current binary contract.

## Upstream source baseline

```text
repository=/Users/jacklee/Projects/codex
branch=main
HEAD=94311d447587411789533c47601fd8bc9d81eb48
status=clean; aligned with origin/main
target tag=rust-v0.150.0-alpha.12.2
annotated tag object=3a8123623648bbe638adcac805e332352a51d275
peeled commit=a9802304f60ab14c0b07e3ee0db9a9c105ab0cb3
HEAD ahead of target=97 commits
target-to-HEAD diff in remote-control transport/processor/CLI/daemon scope=no files changed
```

From alpha.8 to alpha.12.2, the remote-control transport production implementation is unchanged; the scoped diff contains two test-fixture `bedrock_access_keys` additions and an unrelated app-server code-mode-host cleanup. This narrows host-source drift only. It does not prove the closed controller or relay behavior.

Host/server source index:

- `codex-rs/app-server-transport/src/transport/remote_control/protocol.rs`: server enroll/refresh/pair targets; host `ClientEnvelope`/`ServerEnvelope`; allowed host normalization.
- `.../server_api.rs`: host enroll/refresh request flow, installation header and 24–36 second host refresh-failure backoff.
- `.../enroll.rs`: host enrollment state, 5-minute proactive refresh skew, pairing calls and redacted response handling.
- `.../websocket.rs`: host WSS, `x-codex-subscribe-cursor`, ACK/replay, token refresh and stream lifecycle.
- `.../segment.rs`: host 100 KiB target, 150 KiB wire cap, 100 MiB reassembly cap, 1024 segments, 128 concurrent assemblies.
- `.../client_tracker.rs`: `initialize` opens `ConnectionOrigin::RemoteControl`, subsequent messages enter the normal app-server transport, Ping/Pong and ClientClosed, 10-minute idle/30-second sweep.
- `codex-rs/app-server/src/request_processors/remote_control_processor.rs`: enable/disable/status/pair/list/revoke app-server RPC surface.
- `codex-rs/cli/src/remote_control_cmd.rs` and `codex-rs/app-server-daemon/`: experimental CLI/daemon lifecycle.

## Target ChatGPT App controller call-site index

The `app.asar` was read-only extracted to a disposable directory. No ChatGPT App file was modified or resigned.

| Logical ASAR path | SHA-256 | Indexed responsibility |
|---|---|---|
| `.vite/build/main-BvHpyFqC.js` | `cf1e5f9637b925a5a7454bd313176f64af930bfb837cf0e0a7ce7c59f81124ad` | controller environment list/detail, WSS construction, enroll/refresh and device-key proof expected paths |
| `.vite/build/src-4lLVrYxe.js` | `c1749327bc2ac13ea0a4c9d2dc24afb9012e2253d8be28997e836547a563eb91` | controller protocol v3 transport, envelope validation, env/stream routing, cursor header, ACK/replay/chunk/Pong/ClientClosed |
| `webview/assets/app-initial-DJrCTPoN.js` | `27618295c2da9ccf6959e93427e34b75a6d1b4ccd7d4a9f6a18a3974b61803e5` | `/wham` pairing, controller list/delete and MFA requirement UI calls |

Indexed `/codex` controller paths:

- `GET /codex/remote/control/environments`
- `GET /codex/remote/control/clients/{client_id}/environments`
- `GET /codex/remote/control/environments/{environment_id}`
- `WSS /codex/remote/control/client`
- `POST /codex/remote/control/client/enroll/start`
- `POST /codex/remote/control/client/enroll/finish`
- `POST /codex/remote/control/client/refresh/start`
- `POST /codex/remote/control/client/refresh/finish`

Indexed `/wham` product-control paths:

- `POST /wham/remote/control/client/pair`
- `GET /wham/remote/control/clients`
- `DELETE /wham/remote/control/clients/{client_id}`
- `GET /wham/remote/control/mfa_requirement`

The two path families are recorded separately. Their actual resolved origins, authorization behavior and response shapes remain pending real target-version fixture capture.

## New static controller facts and explicit non-conclusions

The current target bundle statically shows:

- protocol version `3`;
- WSS headers `x-codex-client-id`, `x-codex-protocol-version`, optional `x-codex-subscribe-cursor`, and the short-lived `x-codex-client-session-token`;
- device-key challenge validation of target origin/path, token hash, account user, client, expiry and the single `remote_control_controller_websocket` scope;
- controller envelopes containing `env_id` together with `client_id`, `stream_id` and `seq_id`; the selected environment is passed to `connectStream({envId})` and emitted on `client_message`, `client_closed`, ACK/Pong/server-message parsing and reassembly keys;
- controller static segment constants of 100 KiB target, 150 KiB wire cap and **1 GiB** reassembly cap, with the segment-count ceiling derived from those values.

The `env_id` result corrects only the earlier **controller-side unknown**. It does not alter the open-source host `ClientEnvelope` field set, which lacks `env_id`; relay transformation/binding remains private and requires a real fixture. The controller 1 GiB limit also differs from the host's 100 MiB/1024/128 limits. No implementation may choose either side's values by analogy; Phase 0 fixture/replay must freeze the observed controller contract and enforce the smaller safe interoperability boundary where required.

None of the static findings prove enrollment authorization, target Desktop identity, live turn delivery, interrupt arbitration, multi-controller ownership, HTTP 409 behavior, revoke or reconnect semantics. Those remain required real Gate P0 evidence.
