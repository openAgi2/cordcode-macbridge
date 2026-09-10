# Codex Remote native-action delivery source manifest (2026-09-10)

Captured immediately before the P6 delivery builds. This supplements the immutable pre-edit
manifest; it does not rewrite that historical baseline.

| Role | Worktree / target | Branch or version | Source identity |
| --- | --- | --- | --- |
| Mac product | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | HEAD `700f071b70de79c7bebce4885aa48ecf4fa29987`; feature worktree dirty (60 paths, including pre-existing owner files and this plan) |
| iOS product | `/Users/jacklee/Projects/cordcode-ios-plan-approval` | `plan/approval-layer-ios` | HEAD `6a419322f165a99a0e722a93b5de0ded03831f47`; feature worktree dirty (19 paths, including pre-existing owner files and this plan) |
| Upstream patch | `/Users/jacklee/Projects/codex` | `main` | baseline HEAD `9e868bd9dc007c05e84a98e0b1f4e31dc98c5e6a`; 11 patched paths implementing/restoring/testing `thread/settings/get` |
| Patched runtime | `/Users/jacklee/Projects/codex/codex-rs/target/debug/codex` | `codex-cli 0.0.0` | SHA-256 `c41400b00d320b102093d126cb7fb706bb105ec5ec35c94012b34ab40cb34976` |
| Signed Desktop compatibility target | `/Applications/ChatGPT.app` | version `26.903.61454`, build `8378`; embedded Codex 0.153.4 | Signed bundle was not modified; Plan cold-read compatibility tolerates only RPC `-32601` without synthesizing state |

The upstream patch contains 177 insertions and 8 deletions across protocol registration/types,
request dispatch/processing, persisted resume settings, schema export, and focused app-server
tests. The live restart fixture identifies this exact upstream baseline and patched binary hash.

## Delivery boundaries

- Canonical Mac `docs/protocol/bridge-v1.md` and `schema/bridge-v1.types.ts` are byte-identical to
  the iOS mirror before build.
- `agent/codex-web` and `agent/codex` have no delivery diff from the frozen Mac HEAD.
- Compact, collaboration mode, and Goal are independent capabilities. Rollback withdraws only
  the matching readiness/capability and write handler; additive decoding remains readable.
- Review, Skills, and full Codex slash-command parity are not implemented or claimed.
- UI/snapshot/simulator automation is outside the authorized verification scope. Physical-device
  installation requires an online iPhone; both known devices were offline at this capture.
