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
| 4 | Creating a Goal shows the banner, objective/status, budget, and usage. A Desktop-side update reaches the phone; clearing it and reopening still yields an authoritative empty Goal. | FAIL (row-4 first clause: banner never rendered) | 2026-09-10T15:33:46Z goal set from Mac on `01a07ede` (rollout `thread_goal_updated`, status=active, no tokenBudget); content synced to iPhone and task completed normally (screenshot 15:40:32Z), but the Codex goal banner never appeared at any point. Workflow card / background-task row absent — dsh-mode-only UI, out of Codex Remote scope (see round notes). |

## Round 1 result (2026-09-10, owner-reported, recorded by agent)

Owner tested row 4's Desktop-side goal path only; rows 1–3, 5, 6 remain untested. Forensics: `.build/P5-owner-matrix-20260910/` (bridge log window, both screenshots, rollout goal item).

Timeline (UTC):
- 15:33:38 relay respawn; attach goal hydration forwarded (goal still null).
- 15:33:41 iPhone full snapshot at headRev=9, then continuous full_stream scope.
- 15:33:46.574 goal set from Mac; 15:33:47.065 bridge forwarded `session_goal_record` (live goal/updated) to the subscribed phone.
- 15:33:47–15:35:56 goal task ran 4 turns; content patches streamed to the phone (headRev→315); relay events went quiet after the task completed (proven by idle=5m32s at 15:41:28 cleanup).
- 15:40:32 owner screenshot: content fully synced, no goal banner, no workflow card.
- 15:43:43 post-respawn hydration re-forwarded the goal record with NO projection rev advance (headRev stayed 315) — the record was already committed in the bridge projection.

Diagnosis: bridge adapter → relay → reducer → snapshot/patch serialization verified end-to-end against the exact rollout shape (threadId identical to session id; all int64 fields; status `active`). The 15:43:43 no-advance proves the goal record was committed bridge-side, and the content patches prove the delivery channel was alive. Defect localized to the iPhone-side live-patch → projection store → banner render path for `codexGoal`. Discriminating repro pending owner (relaunch app → reopen thread → banner from fresh snapshot, or not).

## Round 2 result (2026-09-11, owner relaunch test + device trace forensics)

Owner relaunched the app: goal bar renders correctly from the fresh snapshot. Device fg-trace pulled via devicectl (`.build/P5-owner-matrix-20260910/device-trace/`):

- Round-1 window (15:33:47Z): `[WS-RX] projection_patch` → `[Pump] patch-decoded baseRev=9 syncRev=10` → `[Apply] patch OK` → `[Replica] commit rev=10 changeset=present`. The goal patch was delivered, decoded, applied, mirrored and notified on the phone — the entire data path was healthy at the moment of the original failure.
- Round-2 window (16:49:19Z, instrumented build): goal-only patch 45→46 → mirror commit → `[GoalBanner] render … visible=1 renderable=1` — the live path renders the banner end-to-end.
- Temporary `[GoalBanner]` instrumentation added at the un-instrumented last mile (refreshGoalBanner backend-kind/projection lookup + GoalBannerView render outcome); regression tests added (CodexGoalPatchDeliveryTests, 3 passing; SlashCommandPanelTests re-run passing).

Conclusion: no reproducible defect remains in the delivery chain; the original failure's UI-layer trigger is no longer observable (same code path now renders live). Instrumentation stays until the owner re-verifies row 4 on the instrumented build; any recurrence will identify its exact gate in fg-trace. Note: bridge projection revs for this session were rebuilt overnight (315-era → 45-era renumbering after idle eviction) — unrelated to the banner and consistent with idle-session eviction semantics.

Scope note (CORRECTED 2026-09-11): the round-1 note claiming "Codex 0.153.4 Remote has no workflow/team item types" was wrong — it reflected only our C0–C4 capture set, which never exercised subagent spawning. Upstream v2 protocol (`app-server-protocol/src/protocol/v2/item.rs`) defines `collabAgentToolCall` (tool=spawnAgent/sendInput, receiverThreadIds, prompt, model, `agentsStates` per-agent status: pendingInit/running/interrupted/completed/errored/shutdown) and `subAgentActivity` (started/interacted/interrupted/completed, agentThreadId+agentPath); the official iOS app renders them as "已创建/已关闭 N 个智能体" sections plus the "正在推进目标 X分X秒" and "N 个智能体" chips (goal timeUsedSeconds already decoded by us). Our bridge codec's item dispatch has no case for either type and silently drops them (fail-closed default), so Codex-mode subagent structure never reaches the phone. Rendering them is a feature gap, not a protocol impossibility; the goal bar row-4 failure remains a separate issue.
| 5 | Session switch A→B→A, reconnect/failure, and capability withdrawal never mix state or show false success; disabled/unknown actions stay visibly unavailable. | TO VERIFY |  |
| 6 | Mac and iPhone concurrently modify Plan/Goal; final phone state converges to official readback rather than whichever client wrote locally. | TO VERIFY |  |
| 7 | (P5.7) During a goal task: subagent workflow card appears on iPhone as agents are created, member states follow official updates, goal bar shows 「N 个智能体运行中」 while running, and `/goal <任务>` interaction matches dsh/Grok Build (claim composer → send → goal bar authoritative). | TO VERIFY |  |

## Owner decision

- All six rows observed successfully: write `PASS`, UTC time, and any anomaly.
- Any mismatch, stuck state, or false success: write `FAIL`, stop further mutation, and preserve the screen/state.

This file is not a pass record until every row is explicitly completed by the owner.
