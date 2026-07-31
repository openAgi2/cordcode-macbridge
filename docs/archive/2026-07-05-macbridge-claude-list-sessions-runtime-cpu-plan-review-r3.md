# 第三轮代码走读级评审：MacBridge Claude list_sessions Runtime CPU Plan (r3)

Date: 2026-07-05
被评审文档：`../cordcode-ios/docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`（已按 r2 优化）
评审原文：[r1](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review.md) / [r2](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review-r2.md)
评审方式：核实 r2 落地质量 + 对本轮新引入/仍存在的逻辑做代码走读

## 第三轮结论

r2 的 6 条意见（含 `isProcessRunning` seam）全部被准确采纳（见 §1）。文档已基本具备实现条件。

但代码走读发现 **1 个 P1 逻辑矛盾 + 1 个 P2 事实错误 + 3 个 P3 措辞**，都源自文档把"`GetRunningSessionIDs` 在 list 路径里被调用一次"与"list 路径零 transcript open"两条同时写成硬约束，而二者不可兼得：

- **【P1】L307 的硬验收"list 路径零 transcript open"与 Fix 2 在 list 路径调用 `GetRunningSessionIDs` 直接冲突**。`GetRunningSessionIDs` 在冷缓存下会对每个存活 Claude PID 调 `isSessionExecuting` → `os.Open` transcript（K 次，承重不可删）。文档需要要么放宽硬验收口径，要么在 list 路径改用"仅 PID liveness"的 running map。这是一个需要 owner 拍板的设计取舍，不是文字微调。
- **【P2】L196 关于非 Claude/OpenCode list 路径的事实错误**：只有 `claudecode` 实现 `RunningSessionLister`，codex/opencode 都没实现，所以它们的 list 路径当前**既不调 `GetRunningSessionIDs` 也不 `markIdle`**。Fix 1/2 覆盖三处调用点仍正确（结构一致 + 防回归），但"非 Claude 今天就有 per-session 成本"的动机表述是错的。
- **【P3】命名/形状三处小不一致**（见 §3.3）。

P1 必须在实现前定调，否则硬验收测试会卡在"为什么 K>0"上无法签收。

---

## 1. r2 落地核实

| r2 意见 | 文档落地点 | 核实 |
| --- | --- | --- |
| P1 stale-running 第二分支 | L76-79、L96、L154、L179、L307、L326、L344、L399 | ✅ 全面落地，current-path/fix/test/acceptance 四处一致 |
| P2 外部 turn 由 PID liveness bound | L138、L183、L198、L215-219、L400、L411 | ✅ 数学清晰（K 而非 N），显式禁止 background scanner |
| P2 保留 reasoningEffort | L175、L305、L401 | ✅ 落地 |
| P2 删 cachedExternalState | L159、L402 | ✅ 落地 |
| P2 三个 list 调用点 | L163-167、L196、L304、L403 | ✅ 落地（但 L196 动机有事实错误，见 §3.2） |
| P3 isProcessRunning seam | L328、L404 | ✅ 落地，优先注入式 seam、次选用 spawned 子进程 |

r2 的"Not Adopted"新增"不新增 background scanner"（L411）与 r2 §2.2 完全一致，无异议。

---

## 2. 关键代码事实（本轮评审依据）

```text
# 谁实现了 RunningSessionLister
$ rg -n "func.*GetRunningSessionIDs" agent/*/*.go
agent/claudecode/claudecode.go:587   ← 唯一实现；codex/opencode 都没有

# GetRunningSessionIDs 内部对存活 PID 扫 transcript（承重，不可删）
agent/claudecode/claudecode.go:625   if state.SessionID != "" && isProcessRunning(state.Pid) {
agent/claudecode/claudecode.go:636       isExecuting = isSessionExecuting(sessionPath)   ← os.Open
agent/claudecode/claudecode.go:651   if isExecuting { running[state.SessionID] = true }

# 非 Claude 的 else 分支：lister 类型断言对 codex/opencode 必然失败
go-bridge/handlers_opencode.go:158   if lister, ok := agent.(core.RunningSessionLister); ok {
                                     ← ok=false 对 codex/opencode → 整块（含 markIdle）不执行
```

---

## 3. 第二轮之外的新发现

### 3.1 【P1】"list 零 transcript open"硬验收与 list 路径调用 `GetRunningSessionIDs` 矛盾

文档同时存在两条不可兼得的约束：

- **Fix 2（L190）** 把 running map 取值放进 list 路径：`runningMap := getCachedClaudeRunningMap(ctx, agent)`。
- **Test Plan L307**："`list_sessions` does not open/read **any** session transcript on the catalog-cache-hit path, including idle, running, and stale-running registry entries."

但 `GetRunningSessionIDs`（[claudecode.go:625-653](../../cordcode-macbridge/agent/claudecode/claudecode.go)）对每个存活 Claude PID 调 `isSessionExecuting`（[claudecode.go:501](../../cordcode-macbridge/agent/claudecode/claudecode.go) → `os.Open`），且这一步**承重**——它区分"进程存活但 turn 间空闲"与"进程存活且正在生成"，删不掉。所以：

- **running-map 缓存冷启动（每个 TTL 窗口首次）**：list 路径会 open K 次 transcript（K = 存活 Claude 进程数）。
- **running-map 缓存命中**：0 次 transcript open。

L307 把硬验收锚定在"catalog-cache-hit path"，但 **catalog 缓存与 running-map 缓存是两套独立缓存**（catalog 在 [claude_session_catalog.go](../../cordcode-macbridge/go-bridge/claude_session_catalog.go)，running-map 缓存是 Fix 3 新增）。catalog 命中**不蕴含** running-map 命中。因此 L307 的"零 transcript open"在 running-map 冷缓存时不可达。

L306 措辞其实是对的（"does not call ... for listed session **rows**"——限定了 N 行），但 L307 把口径放大到"any session transcript"，与 L306 自相矛盾。

**影响**：实现者按 L307 写硬断言测试，跑到 running-map 冷缓存用例时会看到 K>0 次 transcript open，无法判断是 bug 还是预期，签收卡住。`request_total_ms < 200ms`（L324）同理：冷缓存下 K 次 transcript 全扫可能超 200ms（取决于 transcript 大小），该阈值只在两套缓存都命中时成立。

**修正建议（二选一，需 owner 拍板）**：

- **方案 A（放宽验收口径，推荐）**：L307 改为"list 路径对 N 个 listed session rows 零 transcript open；`GetRunningSessionIDs` 在冷缓存下允许至多 K 次 transcript open（K = 存活 Claude 进程数），running-map 缓存命中时为 0"。`<200ms` 阈值明确限定为"两套缓存均命中"路径。这是最小改动，保留现有 `GetRunningSessionIDs` 语义。
- **方案 B（list 路径用 PID-only running map）**：新增一个 list 专用的 running map，只用 `isProcessRunning`（PID 存活即 running），不调 `isSessionExecuting`。这样 list 路径真正零 transcript open，但 sidebar 会把"进程存活但 turn 间空闲"也显示成 running——是产品语义降级，需 owner 接受。

文档当前在两条之间摇摆（L183 既说 list 调 `GetRunningSessionIDs`，L307 又要求零 open），必须显式选一条。

### 3.2 【P2】L196 关于非 Claude/OpenCode list 的事实错误

L196："Non-Claude and OpenCode list paths may not scan Claude transcripts, but they still suffer from per-session `GetRunningSessionIDs` calls and `markIdle` side effects today."

代码核实（§2）：只有 `claudecode.Agent` 实现 `RunningSessionLister`。codex/opencode agent 都没实现，因此 [handlers_opencode.go:158](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 的 `agent.(core.RunningSessionLister)` 类型断言对它们**必然失败**（`ok=false`），整块 `if` 不执行——**既不调 `GetRunningSessionIDs`，也不 `markIdle`**。非 Claude list 路径当前唯一的开销是每 session 一次 `h.sessions.get()`（加锁 map 读，廉价）。

所以"非 Claude/OpenCode list 今天就有 per-session `GetRunningSessionIDs` + `markIdle` 副作用"不成立。

**影响**：
- Impact 表 Fix 2 行（L292"removes per-session repeated active-process scanning"）对 Claude 成立，对非 Claude 是空炮。
- 实现者给非 Claude list 做 before/after 度量会找不到差异，产生困惑，甚至误以为改动没生效。

**修正建议**：L196 改为"非 Claude/OpenCode list 路径今天已是廉价（仅 registry 读，未实现 RunningSessionLister 所以无 `GetRunningSessionIDs`/`markIdle`）；将它们迁到 batch API 是为了结构一致，并防止未来 codex/opencode 实现 RunningSessionLister 时 per-session 调用静默回潮"。Fix 1/2 覆盖三处调用点的**结论**不变，只是动机从"修今天的 bug"改为"防回归 + 一致性"。

### 3.3 【P3】命名/形状三处小不一致

1. **`getCachedClaudeRunningMap` 名字 Claude-specific，却用于全部三处 list 路径**（L190、L196）。对非 Claude backend，一个叫 `...Claude...` 的函数语义错误。建议改为通用名 `getCachedRunningMap(agent)`——它内部做一次类型断言，非 lister agent 直接返回 nil/空 map。
2. **batch API 形状前后不一致**：Fix 1（L159）给的是 `enrichSessionStatesForList(sessions, agent, runningMap)`（复数 batch），Fix 2（L190-193）给的是循环调 `enrichSessionStateWithKnownRunningMap(session, runningMap)`（单数 per-session）。两者功能等价，但实现者会困惑该写哪个。建议统一成一个 batch 函数签名。
3. **`getCachedClaudeRunningMap` 在 Fix 3 之前不存在**：按 Priority Order，Fix 1+2 先落地、Fix 3 是 follow-up。Fix-1+2-only 版本里这个名字指向的缓存还不存在。建议在 Fix 2 注明"先以无缓存薄包装 `agent.GetRunningSessionIDs(ctx)` 实现 `getCachedRunningMap`，Fix 3 在同一调用点替换为 TTL 缓存实现"。

---

## 4. r1/r2 之外未发现的新增问题

下列维度本轮重新走读，均未发现新问题：

- catalog fingerprint 缓存（[claude_session_catalog.go:192](../../cordcode-macbridge/go-bridge/claude_session_catalog.go)）：cache_hit 语义与文档一致。
- 12 处 `enrichSessionStateWithAgent` 调用点分类（3 list + 9 单 session）：与文档 L163-167、L169 一致。
- `reasoningEffort` 注入位置（[handlers_opencode.go:114-121](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）：list-safe 保留要求已落地。
- `/tmp/bridge-sessions.json` 写入（[handlers.go:1924-1926](../../cordcode-macbridge/go-bridge/handlers.go)）：Step 0 已要求删除并防搬家。
- 外部 turn fixture 的 `isProcessRunning` seam（[proc_unix.go:49](../../cordcode-macbridge/agent/claudecode/proc_unix.go)）：已要求可注入。

---

## 5. 需 owner 拍板的一个设计取舍

§3.1 的方案 A vs 方案 B 是产品语义选择，不应由实现 agent 代决：

- **方案 A**：保留 `GetRunningSessionIDs` 现有精度（PID 存活 + transcript 末态双判定），接受 list 路径每个 TTL 窗口有 K 次 transcript open；硬验收口径放宽到"对 N 行零 open，对 K 个存活进程允许"。
- **方案 B**：list 路径降级为 PID-only 判定（进程存活即 running），换取 list 真正零 transcript open；代价是 sidebar 会把"进程存活但 turn 间空闲"显示为 running。

推荐方案 A（最小改动、不降语义、K 通常 1–3 可接受）；但需 owner 明确接受"list 在 running-map 冷缓存时仍有 K 次 transcript open"这一事实，并据此修正 L307/L324 的硬验收口径。

---

## 一句话给 owner

r2 全部采纳、文档已可实现；落地前只差一个 owner 决策：list 路径的 running map 是保留 `GetRunningSessionIDs` 现有精度（方案 A，接受冷缓存下 K 次 transcript open，同步把 L307 硬验收口径放宽到"对 N 行零 open"），还是降级为 PID-only（方案 B，list 真正零 open 但 sidebar 语义变粗）。建议选 A，定调后我即可按 r2 的代码走读清单从 Step 0 起步实现。
