# MacBridge Claude list_sessions Runtime CPU — 完成情况审计报告

Date: 2026-07-05
审计对象: [docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan完成情况.md](2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan完成情况.md)
被审计 commit: `aec16b8`（main HEAD）
审计范围: 逐项核实完成情况报告中的结构、测试、构建、文档与 scope-boundary 声明；独立复跑关键测试与产物核查。
审计人: 独立 agent（非实现该计划的 agent）

## ⚠️ 时效更正（2026-07-05 owner 校正后核实）

> 本报告初稿把 `getAgent→getFirstAgentByName` 的 cache 接线修复写成"r2 计划未提交工作区"。**这是审计人的时效误判**，特此更正：
>
> - 该修复已提交在 **`16b0e79` "Fix Claude file relay external turn lifecycle"**（位于 `aec16b8` 与 HEAD `0460da8` 之间），同时含生产 key 回归测试 `TestGetRunningMap_ProductionClaudeRegistrationFindsClaudeCodeAgent`（注册 `"claude"`）。
> - **HEAD（`0460da8`）的 `runningMapCache` 在生产接线良好**，`aec16b8` 的空转缺陷已不复存在于 main。
> - 下文「建议 #2」担心的"测试全绿但生产空转被 cherry-pick"中间态风险 **已不存在**（修复在 main 上）。建议 #2 仅对"若有人脱离 main 单独 cherry-pick `aec16b8`"这种非主线场景才有意义，可作为历史记录看待。
> - 凡下文出现"r2（未提交工作区）""r2 工作区改动未提交"等措辞，应读作"r2，已提交于 `16b0e79`"。
> - 误判根因：审计人用 `git diff aec16b8`（aec16b8→工作树的**累积** diff）推断提交状态，未先跑 `git log` 确认修复 commit 是否已 land，也未标注 git 快照时间点。本审计对 `aec16b8` 空转缺陷本身的分析（因果链、为何测试没发现、为何 CPU 主目标仍成立）不受此更正影响，依然成立。
>
> owner 已在完成情况报告加 "Audit Addendum" 节、并在 exec-plan state 记录 `reports.audit`（fix_commit=16b0e79）。完成情况报告与 exec-plan 已闭环；本审计报告仅做上述时效更正。

## TL;DR（结论先行）

- **计划主目标（消除 list_sessions 高 CPU）达成且在生产有效。** 真正驱动 9.5–11.8s → 4–16ms 改善的是 Fix 1+2（list 路径零 per-row transcript 解析、不再 `markIdle`、删 `/tmp` dump），它们在生产确实生效，且有定向测试与已安装二进制核查背书。
- **但 Fix 3（`runningMapCache`）在 `aec16b8` 生产态存在接线缺陷：cache 闭包按注册 map key `"claudecode"` 查找 agent，而生产 `-drivers claude` 把 agent 注册在 key `"claude"` 下 → 闭包永远找不到 agent → recompute 永远返回 nil → cache 完全空转、`GetRunningSessionIDs` 在 list 路径根本没被调用。** 这意味着报告"External turns still detected within one TTL window via the bounded live-PID `GetRunningSessionIDs` path"在生产 **不成立**。
- 该接线缺陷被紧随其后的 r2 计划（`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan`，未提交工作区）发现并修复（`getAgent("claudecode")` → `getFirstAgentByName("claudecode")`），并补了生产 key 回归测试。本计划的完成情况报告未承认"r2 修了本计划 cache 的生产接线"。
- 报告的自我标注（self-attested vs re-verified）本身诚实；测试也都通过——但几乎所有测试都把 agent 注册在 key `"claudecode"`，与生产注册 key `"claude"` 不一致，**测试/生产分歧**掩盖了该 bug。
- 综合定级：**计划目标达成（CPU 修复有效），但带一个中等严重度的潜在缺陷（Fix 3 生产接线），该缺陷已被 r2 修复。** 报告对 Fix 3 生产效果的描述偏乐观，建议下发给 r2 完成情况报告一并归档。

## 逐项核查

### ✅ 核实通过（与报告一致）

| 报告声明 | 核查结果 |
|---|---|
| Step 0：`/tmp/bridge-sessions.json` dump 已从 `handleListSessions` 移除 | 生产代码 0 引用；唯一残留在 [handlers_test.go:2898,2902](go-bridge/handlers_test.go) 作为断言目标（`TestClaudeListSessionsDoesNotWriteTmpDump`），符合预期 |
| Fix 1+2：`enrichSessionStatesForList` / `getRunningMap` / `applyListRuntimeState` / `injectClaudeReasoningEffort` 已新增 | 4 个函数均定义在 [handlers_opencode.go:181,216,233,257](go-bridge/handlers_opencode.go) |
| 3 个 list 调用点全部迁移 | Claude：[handlers.go:1934,1955](go-bridge/handlers.go)；非 Claude：[handlers.go:1934](go-bridge/handlers.go)；OpenCode：[handlers_opencode.go:308](go-bridge/handlers_opencode.go)（含 `enrichSessionStatesForList` + `getRunningMap`） |
| list 路径零 per-row transcript 打开、不 `markIdle`、不写 `/tmp` | `enrichSessionStatesForList` 仅做 registry + runningMap 查询与 `injectClaudeReasoningEffort`（cheap getter），无 `findClaudeSessionFile` / `markIdle` / 写文件；语义符合 |
| detail 路径保留更深检视 | `enrichSessionStateWithAgent` 仍在 `get_session` / `resume_session` 等单 session 路径使用（[handlers.go:1864,1879,2607](go-bridge/handlers.go) 等），未变 |
| Fix 3：2s TTL `runningMapCache` | `runningMapCacheTTL = 2 * time.Second`（[running_map_cache.go:14](go-bridge/running_map_cache.go)）；`onStateChange` 失效回调接在 [handlers.go:134-142](go-bridge/handlers.go) |
| Fix 5：`isSessionExecuting` 按 `sessionID+path+size+mtime` 缓存 | [transcript_exec_cache.go](agent/claudecode/transcript_exec_cache.go)：`transcriptExecKey` 四字段；`MtimeAloneForbidden` 子测试覆盖 size/mtime 同时比较 |
| 测试可注入性 seam | [agent/claudecode/proc_seam.go](agent/claudecode/proc_seam.go)（`procAlive` 包级变量）、[go-bridge/transcript_probe.go](go-bridge/transcript_probe.go)（`transcriptStateProbe` no-op） |
| 全套测试通过（exit-audit 声明） | 独立复跑 `go test ./go-bridge/... ./agent/claudecode/... ./core/... -count=1` → 全 ok（go-bridge 13.9s / claudecode / core） |
| 定向测试存在并通过 | 复跑报告列出的全部定向测试：`go-bridge` 套件 `ok 0.918s`；`agent/claudecode` 的 `TranscriptExecCache*` + `TestGetRunningSessionIDs_ExternalTurnViaInjectableSeam` 均 PASS（含 `MtimeAloneForbidden` 两个子用例） |
| 144-session fixture 性能 | 测试存在并 PASS（`ListSessionsClaude_144SessionPerfFixture`，在定向 run 范围内）；具体 cold=56ms/cache-hit=42ms 数值为 self-attested，未逐字复核 |
| CHANGELOG `[Unreleased]` 2026-07-05 CPU 条目 | 存在，内容与改动一致（含改动范围说明） |
| `GO_BRIDGE_ARCHITECTURE.md` list-boundary bullet + 不变式 | 存在于第 123–124、134 行，明确 list-safe 路径不得对任何行打开 transcript、`injectClaudeReasoningEffort` 必须保留 |
| 构建安装：已安装二进制含新符号、无 `/tmp` dump 字符串 | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`：`strings | rg 'runningMapCache|enrichSessionStatesForList|transcriptExecCache|isSessionExecutingCached'` → 6 命中；`bridge-sessions.json` → 0 命中 |
| Scope boundary：`handlers_relay.go` 在 `aec16b8` 仅 +1 行 no-op probe | 属实：`git show aec16b8 -- go-bridge/handlers_relay.go` 净 +1 行（`transcriptStateProbe()`），未触碰 file-relay 业务逻辑；file-relay 一轮滞后确为独立的预存在问题 |

### ⚠️ 中等严重度发现：Fix 3 在 `aec16b8` 生产态接线缺陷

**现象**：`aec16b8` 的 `runningMapCache` recompute 闭包按**注册 map key** 查找 agent：

```go
// handlers.go @ commit aec16b8（已被 r2 改掉的那一行）
h.runningMap = newRunningMapCache(func(ctx context.Context) (map[string]bool, error) {
    agent, ok := h.getAgent("claudecode")   // ← h.agents["claudecode"]，按 key 查
    if !ok { return nil, nil }
    lister, ok := agent.(core.RunningSessionLister)
    if !ok { return nil, nil }
    return lister.GetRunningSessionIDs(ctx)
})
```

**生产链路**：[main.go:104-153](go-bridge/main.go) 遍历 `-drivers` 原始值作为 `id`，`handlers.RegisterAgent(id, agent)`。`-drivers claude` 时 `id="claude"`（别名 `"claude"→"claudecode"` 只用于 `core.CreateAgent(agentName, ...)` 选 agent 类型，不改变 `id`）。所以生产里 `h.agents["claude"] = agent`，而 `agent.Name() == "claudecode"`（[claudecode.go:256](agent/claudecode/claudecode.go)）。

**后果链**：`h.getAgent("claudecode")` → 找不到 → recompute 返回 `(nil, nil)` → `runningMapCache.get` 命中 `running == nil` 分支，直接返回 nil 不写缓存 → `getRunningMap` 对 Claude 永远返回 nil → `enrichSessionStatesForList` 走 registry 回填兜底 → **`GetRunningSessionIDs` 在生产 list 路径从未被调用**。

**对报告声明的影响**：

1. "External turns still detected within one TTL window via the bounded live-PID `GetRunningSessionIDs` path"（What Shipped §Fix 3、commit message）——**生产不成立**。外部 turn（用户在另一个 Terminal 发起）的 running 状态在 list 视图无法被反映，直到 r2 修好接线。
2. phase2-ttlcache-impl 行"getRunningMap routes Claude through cache"——路由代码存在，但生产里 cache 永远空转，从未 collapse 过任何 burst，也从未缓存过任何 `GetRunningSessionIDs` 结果。
3. phase5 行"external-turn executing-state reaches iOS"作为 CPU 计划验收项——存疑。list 路径的外部 turn 检测在生产是死的；file-relay 路径有独立机制但有文档记录的"一轮滞后"。该项被标为 owner-validated pass 与同报告 Scope Boundary 节自相矛盾。

**为什么 CPU 修复仍然有效**：主导成本是 per-row transcript 解析（Fix 1+2 的目标），它在生产确实被消除。cache 空转反而让 list 路径更便宜（连 `GetRunningSessionIDs` 都不调）。owner 实测 CPU 0.0% 与 Fix 1+2 单独生效完全一致，因此 CPU 主结论不受影响。

**为什么测试没发现**：list/running-map 测试几乎全部用 `handlers.RegisterAgent("claudecode", agent)` 注册（如 [list_enrich_test.go:153,294,369,412](go-bridge/list_enrich_test.go)），key 恰好等于 `"claudecode"`，匹配了有 bug 的 `getAgent("claudecode")` 查找。生产用 key `"claude"`，测试用 key `"claudecode"`，**测试/生产注册分歧**直接掩盖 bug。

**谁修的**：r2 计划（未提交工作区）两处改动：(a) [handlers.go](go-bridge/handlers.go) 把 `getAgent("claudecode")` 改成新增的 `getFirstAgentByName("claudecode")`（按 `agent.Name()` 查，对生产 key 无感）；(b) [list_enrich_test.go:428-445](go-bridge/list_enrich_test.go) 新增回归测试 `TestGetRunningMap_ProductionClaudeRegistrationFindsClaudeCodeAgent`（注册 key `"claude"`，断言 `getRunningMap` 命中）。该回归测试在 `aec16b8` 不存在（`git show aec16b8:go-bridge/list_enrich_test.go` 无 `"claude"` key 注册行）。r2 的 CHANGELOG 条目也明确写"同步修复了 2026-07-05 CPU 修复中暴露的生产注册问题"。

### 诚实性评价

- **Attestation 标注准确**：报告明确区分 re-verified 与 self-attested，并坦承"authored by the same agent that did the work"。
- **exit-audit 声明可复现**：全套测试与 `go vet` 复跑通过——但这是跑在含 r2 fix 的工作区；在 `aec16b8` 干净 checkout 上同样通过（因为测试注册 key 与 bug 查找一致），所以"exit audit passed"为真但不能反映生产接线问题。
- **未承认的依赖**：报告把 r2 描述为"独立的 file-relay 问题"，技术上 file-relay 一轮滞后确实是独立问题；但 r2 同时修了本计划 cache 的生产接线，报告对此只字未提，使 Fix 3 的生产效果看起来比实际更完整。

## 给 owner 的建议

1. **CPU 修复本身可信，无需回滚或返工。** 主路径（list 路径零 transcript 解析）在生产有效，`wire_mapping_ms` 改善与 CPU 0.0% 可信。
2. **Fix 3 的生产接线缺陷已由 r2 工作区改动修复，但尚未提交。** 建议把 r2 的 `getFirstAgentByName` 改动 + 生产 key 回归测试一并提交（即便 r2 的 file-relay 部分还要继续），确保 `aec16b8` 之后任一 commit 的 `runningMapCache` 都能在生产生效；否则若有人只 cherry-pick `aec16b8` 而不带 r2 接线修复，会得到一个"测试全绿但生产 cache 空转"的中间态。
3. **修订本完成情况报告（可选）**：在 Fix 3 段或 Scope Boundary 节加一行，说明"`aec16b8` 的 cache 在 `-drivers claude` 下生产空转，由 r2 的 `getFirstAgentByName` 改动修复"，避免后续读者误以为 Fix 3 在 `aec16b8` 已完整生效。
4. **测试治理**：list/running-map 相关测试应至少包含一个生产 key（`"claude"`）注册样例——r2 已补；后续涉及"按 agent 查找"的新代码应默认用 `agent.Name()` 而非注册 key。

## 复核命令

```bash
cd /Users/jacklee/Projects/cordcode-macbridge

# 1. 复现 aec16b8 的生产接线 bug（关键）
git show aec16b8:go-bridge/handlers.go | rg -n 'getAgent."claudecode"'   # 命中那行有 bug 的查找
git show aec16b8:go-bridge/list_enrich_test.go | rg 'RegisterAgent."claude"'  # 无输出 → 当时尚无生产 key 回归测试

# 2. 确认 r2 工作区已修
git diff aec16b8 -- go-bridge/handlers.go | rg 'getFirstAgentByName|getAgent'   # 看到替换

# 3. 全套测试（含 r2 fix 的工作区）
go test ./go-bridge/... ./agent/claudecode/... ./core/... -count=1

# 4. 定向 CPU 测试
go test ./go-bridge -run 'EnrichSessionStatesForList|GetRunningMap|ApplyListRuntimeState|ListSessionsClaude_144SessionPerfFixture|ListSessionsClaude_RunningMapComputedOncePerRequest|ListSessionsClaude_StateChangeInvalidatesRunningMap|RunningMapCache|ListSessionsOpenCode_NoTranscript|TestClaudeListSessionsDoesNotWriteTmpDump' -count=1
go test ./agent/claudecode -run 'TranscriptExecCache|TestGetRunningSessionIDs_ExternalTurnViaInjectableSeam' -count=1

# 5. 已安装二进制含新符号、无 /tmp dump
BIN=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime
strings "$BIN" | rg -c 'runningMapCache|enrichSessionStatesForList|transcriptExecCache|isSessionExecutingCached'  # 期望 >0
strings "$BIN" | rg -c 'bridge-sessions.json'   # 期望 0
```

## 被审计报告的自评对齐

被审计报告自称"16/16 todos proven done; all 4 owner real-device acceptance items pass"。本次审计结论：**todo 完成度与测试通过度属实；CPU 主目标达成；但 Fix 3 在 `aec16b8` 生产态的实际效果被高估，需依赖 r2 的未提交接线修复才能完整生效。** 建议把"Fix 3 生产接线缺陷 + r2 修复"作为补充事实记入该报告或其下一版。
