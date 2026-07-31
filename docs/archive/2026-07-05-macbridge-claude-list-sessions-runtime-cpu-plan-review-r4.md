# 第四轮代码走读级评审：MacBridge Claude list_sessions Runtime CPU Plan (r4)

Date: 2026-07-05
被评审文档：`../cordcode-ios/docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`（已按 r3 方案 A 优化）
评审原文：[r1](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review.md) / [r2](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review-r2.md) / [r3](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review-r3.md)
评审方式：核实 r3 方案 A 落地的内部一致性 + 全文术语/口径漂移扫描

## 第四轮结论

**文档已实现就绪（implementation-ready）。** 方案 A 在全文 8 处 K/transcript-open 相关表述里口径一致，r3 的三项采纳准确无误，未发现新的 P1/P2 问题。剩下的只有 4 条 P3 措辞/术语级打磨，全部不阻塞实现，可在编码时顺手收敛，无需再开一轮评审。

可以进入实现。建议按 [r2 §4 代码走读清单](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review-r2.md) 从 Step 0 起步。

---

## 1. r3 方案 A 落地核实

| r3 意见 | 文档落地点 | 核实 |
| --- | --- | --- |
| P1 零 transcript-open 矛盾（方案 A） | Principle L140、Test L309/310/327/329、Acceptance L345/348、Disposition L411/421 | ✅ 全文一致地区分了"per-row 零 open"与"running-map 冷缓存允许 K 次 live-PID open" |
| P2 非 Claude 事实错误 | L198、Disposition L412 | ✅ 准确修正为"仅 claudecode 实现 RunningSessionLister；Codex/OpenCode 迁 batch 是为结构与防回归" |
| P3 命名/形状统一 | L161、L192-193、L196、Disposition L413 | ✅ 统一为 `getRunningMap(ctx, agent)` + `enrichSessionStatesForList(...)`，并注明 Fix 3 前的无缓存薄包装阶段 |

### K/transcript-open 口径全文交叉核对（8 处全部自洽）

| 位置 | 表述 | 一致性 |
| --- | --- | --- |
| L140 Principle | "may open at most K transcripts while computing the shared running map on a cold TTL window" | ✅ |
| L200 Fix 2 | "K transcript checks per request on running-map cache miss" | ✅ |
| L219 Fix 4 | "cost is K, the live Claude process count, usually 1-3, not N" | ✅ |
| L294 Impact | "residual K/request cost is closed by Fix 3" | ✅ |
| L310 Test | "cold cache may ... open at most K transcripts ... cache hit should open 0" | ✅ |
| L327 Test | "<200ms ... only on the double-cache-hit path ... Do not apply [it] to the running-map cold-cache path" | ✅ |
| L329 Test | "running-map cold-cache fixture, the allowed transcript-open count is at most K" | ✅ |
| L348 Acceptance | "cold cache permits at most K transcript opens ... cache hit permits 0" | ✅ |

方案 A 的"per-row 零 open / 冷缓存 K / 命中 0"三档口径在 Principle、Fix、Test、Acceptance、Disposition 五个章节里完全对齐，没有遗漏的旧表述。

---

## 2. 剩余 P3 打磨项（不阻塞实现）

以下均为措辞/术语级，不影响正确性与可验收性，编码时顺手处理即可：

### 2.1 术语漂移：session row 的限定词在"listed / displayed / any / per-row"间摆动

全文出现 `listed session rows`（L138/156/185/315）、`displayed session rows`（L309）、`any session`（L181）、`per-row`（L198/329）、`any session row`（L347）等多种说法。

值得注意的精度问题：`displayed`（L309）字面指**分页后展示**的子集，但 enrichment 实际跑在**分页前**的全集上（[handlers.go:1919-1923](../../cordcode-macbridge/go-bridge/handlers.go) 先 enrich 全部 `allSessions` 再 `paginateSessionList`）。因为硬验收是"零 open"，全集/子集不影响断言成败（任何一次 open 都判失败），但术语不统一会让 fixture 设计者犹豫"该数哪一批"。

建议加一句术语注脚，例如："本文中 session row 指 catalog 返回、进入 enrichment 的会话条目（分页前全集）；zero-transcript-open 断言覆盖该全集。" 然后把 L309 的 `displayed` 统一成 `listed`。

### 2.2 L304 测试断言锚点建议从 `GetRunningSessionIDs` 改到 `getRunningMap`

L304："`handleListSessions` calls `GetRunningSessionIDs` at most once per request."

Fix 2 之后真正的 list 路径调用点是 `getRunningMap(ctx, agent)`（[新 seam](../../cordcode-macbridge/go-bridge/handlers.go)），`GetRunningSessionIDs` 是它内部（无缓存阶段）转调的下层方法。在非 Claude 路径上 `GetRunningSessionIDs` 永不被调（类型断言失败），该断言对 Codex/OpenCode 是空真。

建议改为"`handleListSessions` 调用 `getRunningMap` 恰好一次，且 `agent.GetRunningSessionIDs` 在该请求内至多被触达一次（Claude 路径）或零次（非 Claude 路径）"，避免实现者在错的层挂计数 hook。

### 2.3 Fix 5 未显式连接"大 K 冷缓存"场景的兜底

L200/219 把 K 描述为"usually 1-3"。但 K 无硬上限——用户若同时开很多终端跑 Claude，K 可能两位数。此时即使 Fix 3 把冷缓存收敛到 K/TTL，每个 TTL 窗口仍要做 K 次 transcript 全扫。

Fix 5 的 `size+mtime` 缓存（L233-254）其实是这个场景的天然兜底：同一 live PID 的 transcript 若自上次扫描后未变（size+mtime 不变），命中缓存、跳过全扫。但文档没有把 Fix 5 与"大 K 冷缓存"显式连起来。

建议在 Fix 5 开头加一句："Fix 5 也是 running-map 冷缓存在大 K（用户同时开多个 Claude 终端）场景下的兜底：同一 live PID 的 transcript 未变则命中、跳过重复全扫。" 这让 Fix 5 的优先级在"用户 K 偏大"时上升，便于实现排序。

### 2.4 L181 的"must not"建议加 row 限定，避免被误读为"整个 list handler 不能 open transcript"

Fix 1 的"must not"清单（L179-183）里有一条："open or parse transcript files for any session"。这是对 `enrichSessionStatesForList`（per-row enrichment 函数）的约束，正确。但字面读"any session"可能被误读为"整个 list handler 不能 open 任何 transcript"，与方案 A 的"list handler 内的 `getRunningMap` 允许 K 次 live-PID open"冲突。

L309 已经用了谨慎的 "for displayed session rows" 限定。建议把 L181 也对齐成 "open or parse transcript files for any session row during per-row enrichment"，与 L309 / L315 / L347 的口径统一，消除"per-row enrichment 不开"与"list handler 整体可能开 K 次"的表面张力。

---

## 3. r1-r3 之外本轮未发现的新增实质问题

下列维度本轮重新走读，均未发现新的 P1/P2：

- catalog fingerprint 缓存与 cache_hit 语义（[claude_session_catalog.go:192](../../cordcode-macbridge/go-bridge/claude_session_catalog.go)）；
- 12 处 `enrichSessionStateWithAgent` 调用点（3 list + 9 单 session）；
- 两处 transcript-scan 分支（idle fallback + stale-running 校验，[handlers_opencode.go:131-156](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）；
- `reasoningEffort` 注入位置（[handlers_opencode.go:114-121](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）；
- `/tmp/bridge-sessions.json` 写入（[handlers.go:1924-1926](../../cordcode-macbridge/go-bridge/handlers.go)）；
- `GetRunningSessionIDs` 的 PID liveness + transcript 承重判定（[claudecode.go:625-653](../../cordcode-macbridge/agent/claudecode/claudecode.go)）；
- `isProcessRunning` 平台相关实现（[proc_unix.go:49](../../cordcode-macbridge/agent/claudecode/proc_unix.go)）；
- 方案 A 下外部 turn 的正确性（live PID + isSessionExecuting 双判定 → runningMap → enrich 正确标 running）。

四轮评审到此，文档的诊断、修复方向、优先级、测试与验收已形成闭环且与代码逐行对齐。

---

## 一句话给 owner

文档已实现就绪，方案 A 在全文口径自洽，r3 三项全部准确采纳，本轮无新的 P1/P2；4 条 P3 措辞打磨可在编码时顺手收敛。可以进入实现——我建议直接按 r2 §4 的代码走读清单从 Step 0（删 `/tmp/bridge-sessions.json`）起步，需要我现在开工就说一声。
