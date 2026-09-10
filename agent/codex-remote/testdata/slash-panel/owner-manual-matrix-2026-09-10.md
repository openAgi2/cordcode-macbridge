# Owner manual matrix — Codex Remote native actions (2026-09-10)

Environment under test:

- Mac app: `/Applications/CordCodeLink.app`, ad-hoc release, runtime `700f071b70de`,
  built `2026-09-10T15:14:24Z`, runtime PID `44933`, port `8777`.
- iPhone: iPhone 16 Pro, iOS 27.0, signed Debug `org.openagi.cordcode` version `1.0.0`,
  installed and launched from the paired feature worktree, launch PID `3862`.
- Post-fix live log: no `unknown variant thread/settings/get` and no projection prepare failure;
  observation scope attached to the current Codex Remote thread and projection hydrated at `headRev=7`.

No automated UI interaction was used. The owner performs and records each result directly.

| # | Required owner observation | Result | Notes / time |
| --- | --- | --- | --- |
| 1 | A thread already open in Desktop becomes visible read-only on iPhone, and Compact, Plan, and Goal surfaces are independently visible when their capabilities are advertised. | TO VERIFY |  |
| 2 | With draft text and an attachment present, invoking Compact preserves the draft/attachment, shows the official compaction lifecycle, and after reopening the thread still shows the official record without a fake user bubble. | TO VERIFY |  |
| 3 | Switching to Plan, starting the next turn, receiving the plan-review card, and approving it returns the phone to Default while the original approval flow remains usable. | TO VERIFY |  |
| 4 | Creating a Goal shows the banner, objective/status, budget, and usage. A Desktop-side update reaches the phone; clearing it and reopening still yields an authoritative empty Goal. | TO VERIFY |  |
| 5 | Session switch A→B→A, reconnect/failure, and capability withdrawal never mix state or show false success; disabled/unknown actions stay visibly unavailable. | TO VERIFY |  |
| 6 | Mac and iPhone concurrently modify Plan/Goal; final phone state converges to official readback rather than whichever client wrote locally. | TO VERIFY |  |

## Owner decision

- All six rows observed successfully: write `PASS`, UTC time, and any anomaly.
- Any mismatch, stuck state, or false success: write `FAIL`, stop further mutation, and preserve the screen/state.

This file is not a pass record until every row is explicitly completed by the owner.
