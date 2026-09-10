# Codex Remote slash-panel source manifest (2026-09-09)

This file records the fresh P0 source identity captured before the first product-code edit for
`docs/2026-09-07-codex-remote-slash-command-panel-implementation.md`. It is provenance evidence,
not proof that Compact, Plan, or Goal is ready.

## Expected product and worktree pairing

- Product: `codex-remote` / Codex Desktop over the existing Remote Control controller and official relay.
- Mac implementation worktree: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`, expected branch `plan/approval-layer`.
- iOS implementation worktree: `/Users/jacklee/Projects/cordcode-ios-plan-approval`, expected branch `plan/approval-layer-ios`.
- Read-only upstream checkout: `/Users/jacklee/Projects/codex`, branch `main`; target implementation reference is the `rust-v0.153.4` tag object.
- Required native actions: `context_compaction`, `session_collaboration_mode`, and `session_goal`. `session_commands` is not a substitute.

## Fresh pre-read and pre-edit Git identity

Captured twice on 2026-09-09 before this evidence file was created. The second capture completed at
`2026-09-09T13:44:08Z`.

| Role | Absolute worktree | Branch | Full HEAD | Dirty set before exec-plan state/evidence creation |
| --- | --- | --- | --- | --- |
| Mac | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `700f071b70de79c7bebce4885aa48ecf4fa29987` | `?? docs/2026-09-07-codex-remote-slash-command-panel-implementation-audit.md`; `?? docs/2026-09-07-codex-remote-slash-command-panel-implementation.md`; `?? handoffs/handoff-20260907-2127.md`; `?? handoffs/handoff-20260907-2319.md`; `?? handoffs/handoff-20260909-1331.md` |
| iOS | `/Users/jacklee/Projects/cordcode-ios-plan-approval` | `plan/approval-layer-ios` | `6a419322f165a99a0e722a93b5de0ded03831f47` | `?? message-web/public/repro-items.json` |
| Upstream | `/Users/jacklee/Projects/codex` | `main` | `9e868bd9dc007c05e84a98e0b1f4e31dc98c5e6a` | clean |

The Mac HEAD differs from the older design-document snapshot (`c38b16e…`); this manifest is the
source for the current implementation run. Existing untracked files above belong to the user and
must not be overwritten, adopted, stashed, reset, or removed.

## Target Desktop and upstream tag identity

Read-only checks on 2026-09-09:

| Item | Observed value |
| --- | --- |
| `/Applications/ChatGPT.app` version/build | `26.901.51231` / `8109` |
| Embedded binary version | `codex-cli 0.153.4` |
| Embedded binary SHA-256 | `a30ec314bbd0e3721632234d07db7c99855db3b9f1e32dbe8c791947f07e7629` |
| Upstream `rust-v0.153.4^{}` | `3d2ee51ca2d5db578f328aa75e20aa22c0197c9a` |
| Running ChatGPT PID/start | PID `66269`, started `2026-09-08 13:17:18 +0800` |
| Running embedded app-server PID/start | PID `66377`, started `2026-09-08 13:17:20 +0800` |
| Remote initialize user agent observed by installed bridge | `Codex Desktop/0.153.4 (Mac OS 27.0.0; arm64) ... (codex_remote; 0)` |

The version/tag match is a source anchor only. It does not prove that the private Desktop binary is
byte-equivalent to the open-source tag, nor does it replace live Remote request/response/event/cold-read samples.

## Installed CordCode runtime identity at capture time

| Item | Observed value |
| --- | --- |
| `/Applications/CordCodeLink.app` version/build | `0.1.0` / `1` |
| Embedded runtime version | `cordcode-bridge-runtime 0.1.0` |
| Embedded runtime commit/build | `6b4a93b088ad` / `2026-09-09T10:13:50Z` |
| Running runtime PID/start | PID `39743`, started `2026-09-09 18:14:41 +0800` |
| Runtime driver identity | `claude,codex-remote,grokbuild,dsh-web,opencode-web` |
| Bridge epoch | `441a5546-052c-4296-be96-4e74589c687c` |
| Remote Control management status | `phase=ready`, `online=true`, `clientType=CODEX_DESKTOP_APP` |

The installed runtime commit is not the feature-worktree HEAD. Any later live conclusion must state
whether it came from this installed runtime or from a newly built and identity-verified feature artifact.

## Readiness at this checkpoint

- C0 is only partially established: the live controller is connected to a Desktop announcing 0.153.4,
  but a sanitized controller connection-generation artifact and full target-chain probe are still required.
- C1 has not yet been captured for a controlled no-registry read-only attach.
- C2 Compact, C3 Plan, and C4 Goal live writes have not been run and remain closed.
- No standalone app-server, rollout file, source schema, mock, fixture, slash text, normal Send,
  `command/exec`, fallback, or placeholder is accepted as live readiness proof.
