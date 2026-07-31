# 第二轮代码走读级评审：MacBridge Claude list_sessions Runtime CPU Plan (r2)

Date: 2026-07-05
被评审文档：`../cordcode-ios/docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`（已按 r1 评审原文优化）
评审原文 r1：[2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review.md](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan-review.md)
评审方式：核实 r1 意见的落地质量，并对本轮新增内容做代码走读

## 第二轮结论

文档本轮修订质量高，r1 的全部 P1/P2/P3 意见都被准确吸收（见 §1 核实表）。可以进入实现。

但代码走读发现 **1 个 P1 遗漏 + 4 个 P2 新增点**，都集中在"实现者照文档动手时会踩"的地方：

- **【P1】文档对 current code path 的描述漏掉了第二个 transcript-scan 分支**（[handlers_opencode.go:145-156](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）。Fix 1 的"list-safe enrichment"必须同时封掉这个分支，否则 registry 里任何一个 stale-running session 仍会触发 per-session transcript 全扫，CPU 降不下来。
- **【P2】外部 turn 检测已经被 `GetRunningSessionIDs` 的 PID liveness 天然 bound 到 K（存活进程数）**，不需要新增 background scanner。文档 Fix 4 把"bounded external-turn detection"说成需要一个新的 background/TTL 路径，实现者可能造冗余 goroutine。
- **【P2】list-safe enrich 必须保留 `reasoningEffort` enrichment**（[handlers_opencode.go:114-121](../../cordcode-macbridge/go-bridge/handlers_opencode.go)），文档的 batch API shape 没提，容易被一起砍掉。
- **【P2】batch API 的 `cachedExternalState` 参数未定义**，要么定义要么删。
- **【P2】Fix 1/2 应明确覆盖 3 个 list 调用点（Claude / 非 Claude / OpenCode）**，不只 Claude 那一处。

这些都不改变文档的方向，只需在 §Fix Direction 与 §Test Plan 里补几句即可。

---

## 1. r1 意见的落地核实

| r1 意见 | 文档落地点 | 核实结论 |
| --- | --- | --- |
| P1 `/tmp/bridge-sessions.json` 调试写入 | Summary L11、Incident L50、Current Path L71、Fix 0 L138-145、Test L287/296、Acceptance L328 | ✅ 全面落地，且正确把它定位为"度量污染物"而非主因 |
| P2 读路径 `markIdle` 副作用 | Current Path L76、Principle L132、Fix 1 L168-170、Test L290、Acceptance L327 | ✅ 落地，且升级为"list 必须无副作用"的硬验收 |
| P2 外部 turn 与 owned state 冲突 | Principle L134、Fix 3 L200、Fix 6 标题 L238、Acceptance L333 | ✅ 落地，TTL 明确作为外部 turn 检测兜底 |
| P3 缺优先级 | Priority Order L262-269、Impact 表 L273-280 | ✅ 落地，Fix 1+2 列为主修，表清晰 |
| 多 call sites 重构 | L97、L151-158 | ✅ 方向对，但见 §2.4 关于覆盖范围的补强 |
| Fix 4 缓存边界（size+mtime 同比） | L233-236 | ✅ 落地，措辞准确 |
| catalog in-flight 去重边界 | L250-252 | ✅ 落地，cursor/limit 警告到位 |
| 测试硬验收 | L289/296/306/308 | ✅ 大幅强化，但见 §2.1 与 §2.5 的两处补强 |

r1 的"Not Adopted"三条（不彻底删 transcript inference / 不把 coalescing 当首选 / 不强制 list coalescing）都是文档自设的合理边界，与 r1 建议一致，无异议。

---

## 2. 第二轮新发现

### 2.1 【P1】current code path 描述漏掉第二个 transcript-scan 分支

文档 §Current Code Path（L72-76）与伪代码（L85-93）只描述了**一个** fallback 分支："if not running, fall back to transcript state lookup"。但实际代码里 `enrichSessionStateWithAgent` 有**两个**会触发 transcript 全扫的分支。第一个是文档描述的 idle 分支（[handlers_opencode.go:131-142](../../cordcode-macbridge/go-bridge/handlers_opencode.go)），第二个是紧随其后的 stale-running 校验分支（[handlers_opencode.go:145-156](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）：

```go
if !usedTranscriptFallback && state == "running" {
    // registry 说 running 但 GetRunningSessionIDs 出错/未命中，
    // 也用 transcript 校验。
    _, sessPath := findClaudeSessionFile(sessionID, "")
    if sessPath != "" {
        fileState := h.detectClaudeTranscriptState(sessPath)   // ← 第二处 per-session 全扫
        if fileState == "idle" {
            state = "idle"
            h.sessions.markIdle(sessionID)                     // ← 第二处 markIdle 副作用
        }
    }
}
```

该分支触发条件是 `state == "running"`（来自 `h.sessions.get(sessionID)` registry 命中）且未被前一个分支改写。在 list 路径里，只要 registry 里残留**一个** stale-running 的 session（例如进程崩溃后 registry 未被清理），该 session 就会在**每次 list 请求**触发一次 transcript 全扫 + 一次 markIdle 写。

**对实现的影响**：如果实现者只按文档 L166-170 的"must not"清单封掉 idle 分支（`detectClaudeTranscriptState` for idle session），而遗漏这个 stale-running 分支，那么只要 registry 里有一个 stale-running session，list CPU 就降不下来——而且这正是进程崩溃后最常见的状态。

**对文档的修正建议**：

1. §Current Code Path 增补第二分支的事实描述（建议在 L76 后加一条 bullet）。
2. §Fix 1 的"must not"清单（L166-170）把"idle session 不开 transcript"升级为"**任何 session 都不开 transcript**"，并显式点名 stale-running 分支也要封。
3. §Test Plan 的硬断言（L289）从"list does not open/read an **idle** session transcript"改为"list does not open/read **any** session transcript (idle, running, or stale-running)"，并补一个"registry 里放一个 stale-running session"的 fixture，确保该分支在 list 路径被短路。

### 2.2 【P2】外部 turn 检测已被 PID liveness 天然 bound，不需要新 background scanner

文档 §Principle（L134）与 Fix 4（L202-213）把"bounded external-turn detection"描述为需要一个"a bounded background/TTL path"或"a small TTL-limited set of externally observed active Claude processes"。这个措辞容易让实现者去新建一个后台 goroutine 周期扫 transcript。

但代码走读显示，外部 turn 检测**已经**在 `GetRunningSessionIDs` 内被 PID liveness 天然 bound：

```go
// agent/claudecode/claudecode.go:625-653
if state.SessionID != "" && isProcessRunning(state.Pid) {   // ← 仅存活 PID
    ...
    isExecuting := isSessionExecuting(sessionPath)            // ← 只对这 K 个存活进程扫 transcript
    if isExecuting { running[state.SessionID] = true }
}
```

`GetRunningSessionIDs` 遍历的是 `~/.claude/sessions/*.json`（小 stub 文件，记录每个 Claude CLI 进程的 pid/sessionId/cwd），只对**存活 PID** 调 `isSessionExecuting`。也就是说：

- 外部 turn 的"发现"靠 `~/.claude/sessions/*.json` 的 PID liveness，**不靠扫 transcript**；
- transcript 扫描只发生在 K 个存活进程上（K = 用户当前同时在跑的 Claude 进程数，通常 1–3）；
- 真正失控的是 N（列出 session 数，144）× K 的重复 + idle fallback 的 N 次全扫，**不是外部 turn 检测本身**。

因此 Fix 2（每 request 一次 `GetRunningSessionIDs`）+ Fix 3（TTL 缓存 `GetRunningSessionIDs`）已经把外部 turn 检测成本压到 K/request 再到 K/TTL，**不需要任何新的 background scanner**。Fix 4 列的"a small TTL-limited set of externally observed active Claude processes"其实就是 `GetRunningSessionIDs` 返回的存活 PID 集合本身。

**对文档的修正建议**：在 Fix 4 开头加一句数学说明：

> 外部 turn 检测的成本已经被 `GetRunningSessionIDs` 的 PID liveness 天然 bound 到 K（存活 Claude 进程数，通常 1–3），不是 N（列出 session 数）。Fix 2 把它从 N×K 降到 K/request，Fix 3 再降到 K/TTL。无需新增后台 transcript 扫描 goroutine；"bounded external-turn set"就是 `GetRunningSessionIDs` 的存活 PID 返回值。

这能防止实现者造一个冗余的周期扫 transcript 的 goroutine，反而引入新的 CPU 来源。

### 2.3 【P2】list-safe enrich 必须保留 reasoningEffort enrichment

`enrichSessionStateWithAgent` 的 claudecode 分支开头有一段与执行态无关的廉价 enrichment（[handlers_opencode.go:114-121](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）：

```go
if effort, _ := mapped["reasoningEffort"].(string); strings.TrimSpace(effort) == "" {
    if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
        if effort := normalizeClaudeRuntimeEffort(re.GetReasoningEffort()); effort != "" {
            mapped["reasoningEffort"] = effort
        }
    }
}
```

这是纯内存 getter（`GetReasoningEffort` 无 IO），属于 list-safe 范畴，必须保留。文档 §Fix 1 的 batch API shape（L153-156）只给出 `enrichSessionStatesForList(sessions, agent, runningMap, cachedExternalState)`，没有说明该函数要保留这段。实现者如果"最小化"地把 list-safe 实现成"只填 runtimeState"，会回归掉 reasoningEffort 字段。

**对文档的修正建议**：在 Fix 1 的"list-safe path may use"清单（L160-164）显式加一条"保留现有的 `reasoningEffort` 注入（无 IO，list-safe）"。

### 2.4 【P2】batch API 的 `cachedExternalState` 参数未定义

L154 的 shape 引入了一个未定义参数：

```go
enrichSessionStatesForList(sessions, agent, runningMap, cachedExternalState)
```

文档从未说明 `cachedExternalState` 是什么类型、由谁填充、与 `runningMap` 的关系。结合 §2.2 的结论——外部 turn 的存活 PID 集合就是 `runningMap` 本身——这个参数大概率是冗余的（`runningMap` 已经携带了外部存活进程的 running 状态）。

**对文档的修正建议**：二选一——

- 删掉 `cachedExternalState`，shape 改回 `enrichSessionStatesForList(sessions, agent, runningMap)`；或
- 定义清楚它承载什么（例如"transcript-state cache 的句柄，供 list 路径对 stale-running session 做廉价命中判断"），并说明它与 `runningMap` 的边界。

不定义会让实现者各凭猜测，产生分歧。

### 2.5 【P2】Fix 1/2 的覆盖范围要明确包含 3 个 list 调用点

`enrichSessionStateWithAgent` 共有 **12 处调用**（rg 结果），其中 **3 处是 list 路径**：

| 调用点 | 路径 | 是否 list |
| --- | --- | --- |
| [handlers.go:1921](../../cordcode-macbridge/go-bridge/handlers.go) | Claude `handleListSessions` | ✅ list |
| [handlers.go:1898](../../cordcode-macbridge/go-bridge/handlers.go) | 非 Claude `handleListSessions` | ✅ list |
| [handlers_opencode.go:211](../../cordcode-macbridge/go-bridge/handlers_opencode.go) | OpenCode `ocHandleListSessions` | ✅ list |
| handlers.go:1300/1828/1843/2577、handlers_opencode.go:97/248/330/349 | get/create/resume 等单 session 路径 | ❌ 保留富语义 |

文档 L158 提到"non-Claude list paths, OpenCode list"会复用，但没有把"3 个 list 调用点全部切到 batch API"列为 Fix 1 的显式要求。非 Claude 与 OpenCode 的 list 路径在 else 分支（[handlers_opencode.go:157-169](../../cordcode-macbridge/go-bridge/handlers_opencode.go)）不做 transcript 扫描，但仍是**每 session 一次 `GetRunningSessionIDs`**，属于 Fix 2 的 hoist 收益范围（且每 session 一次 `markIdle` 也是副作用）。

**对文档的修正建议**：Fix 1 / Fix 2 各加一句"适用于全部 3 个 list 调用点（Claude / 非 Claude / OpenCode），不只是 Claude 分支"，避免实现者只改 Claude 路径而留下 OpenCode list 的 `markIdle` 副作用与冗余 `GetRunningSessionIDs` 调用。

### 2.6 【P3】外部 turn fixture 需要 `isProcessRunning` 的可测 seam

§Test Plan 的外部 turn fixture（L310）要求"construct a fake live Claude PID/session stub"。但 `isProcessRunning` 是平台相关硬编码（[proc_unix.go:49](../../cordcode-macbridge/agent/claudecode/proc_unix.go)、[proc_windows.go:40](../../cordcode-macbridge/agent/claudecode/proc_windows.go)），无注入点。单测里"假造一个存活 PID"要么真的 spawn 一个子进程占用 PID，要么 mock `isProcessRunning`——后者目前没有 seam。

**对文档的修正建议**：在 §Test Plan 注明"外部 turn fixture 需要为 `isProcessRunning` 增加可测 seam（例如把 PID liveness 检查注入为 Agent 的一个函数字段），或在测试里 spawn 一个短生命周期子进程占用 PID"。这是实现该 fixture 的前置条件，不提前说实现者会卡住或退而用真进程（导致 CI 不稳定）。

---

## 3. 仍未解决的小点（不阻塞实现）

1. **"Not Adopted"第一条措辞**（L382"Removing transcript inference entirely: not adopted"）：这其实不是拒绝 r1 建议——r1 §3.3 明确反对彻底删除 transcript inference。文档把它列为"Not Adopted"是自设边界，无误，但读者可能误以为 r1 曾建议彻底删除。可在该条加一句"r1 同样不建议彻底删除"以正视听。属修辞，非实质。

2. **Fix 3 落地前的残余成本**：按文档优先级，Fix 1+2 先行，Fix 3（TTL）列为 follow-up。但对"同时开多个 Claude 终端"的用户，Fix 1+2 后单次 list 仍会做 K 次 transcript 全扫（存活进程的）。K 通常很小可接受，但文档 Impact 表（L273-280）未提这个残余，建议在 Fix 2 行注明"残余 K-per-request 成本由 Fix 3 收口"。

3. **Fix 0 的验收口径**（L144）：除了"不写 `/tmp/bridge-sessions.json`"，建议同时 grep 确认没有把调试写迁移到别的 `/tmp/*` 路径或 `os.Stdout`，避免调试代码搬家而非删除。

---

## 4. 落地实现时的代码走读清单

实现者动手时按此清单逐项确认（带 file:line，可点击）：

1. **删 `/tmp` 写入**：[handlers.go:1924-1926](../../cordcode-macbridge/go-bridge/handlers.go) 的 `MarshalIndent` + `os.WriteFile("/tmp/bridge-sessions.json", ...)` 整段移除。
2. **新增 batch/list-safe enrich**：签名形如 `enrichSessionStatesForList(sessions []map, agent, runningMap) []map`；保留 [handlers_opencode.go:114-121](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 的 `reasoningEffort` 注入；**禁止**走到 [handlers_opencode.go:131-142](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 与 [handlers_opencode.go:145-156](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 任一 transcript 分支；**禁止** `markIdle`。
3. **3 个 list 调用点全切**：[handlers.go:1898](../../cordcode-macbridge/go-bridge/handlers.go)（非 Claude）、[handlers.go:1921](../../cordcode-macbridge/go-bridge/handlers.go)（Claude）、[handlers_opencode.go:211](../../cordcode-macbridge/go-bridge/handlers_opencode.go)（OpenCode）。单 session 路径（handlers.go:1300/1828/1843/2577、handlers_opencode.go:97/248/330/349）保留原 `enrichSessionStateWithAgent`。
4. **hoist runningMap**：`GetRunningSessionIDs` 在每个 list handler 里调一次，传入 batch 函数；不要在 batch 函数内部再调。
5. **TTL 缓存（Fix 3）**：包在 `GetRunningSessionIDs` 外层，键全局 per Claude agent；TTL 同时是外部 turn 检测兜底。无需 background scanner。
6. **测试 seam**：为 `isProcessRunning` 增加可注入点，供外部 turn fixture 使用；为 `detectClaudeTranscriptState`/transcript open 增加计数 seam，硬断言"list 路径任何 session 的 transcript open 次数 = 0"。

---

## 一句话给 owner

文档 r2 已经可以进入实现；落地前只需补 5 处文字（封掉第二个 transcript 分支 / 澄清外部 turn 已被 PID liveness bound 不需新 scanner / 保留 reasoningEffort / 定义或删除 cachedExternalState / 3 个 list 调用点全覆盖），即可避免实现者照文档动手时漏掉 stale-running 分支或造冗余后台 goroutine。
