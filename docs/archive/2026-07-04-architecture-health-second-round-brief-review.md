# 第二轮开发交接 brief 评审报告

- **日期**：2026-07-04
- **被评审对象**：[`docs/2026-07-04-architecture-health-second-round-development-brief.md`](../2026-07-04-architecture-health-second-round-development-brief.md)
- **评审方式**：独立 agent 对 brief 逐条核对。所有量化声明用可复现命令重测；4 份输入文档（gap-analysis / 完成审计 / web-renderer plan / engineering-constitution）与第一轮评估逐份比对。**全程未修改任何文件。**
- **性质**：文档评审，非代码改动；时点快照，随代码演进而过期。

---

## 一、评审结论（先给结论）

**brief 整体方向正确、可作为施工输入，但有 1 条核心论据不可复现、3 条施工路径与代码现状有偏差，需在开工前修订。** 否则下一个 agent 会：在 P0 拿到与表格不符的 diff 数字、在 P1 找不到 brief 描述的 handler 簇（反而漏掉最大那块）、在 P2 低估 gate 工作量。

- ✅ 范围取舍（做 S1+S2+S5、不做 god-object 大手术、第三轮触发条件）合理且与 gap-analysis 一致。
- ✅ god-object 指标（BridgeProvider **1967 行 / 88 func / 34 ForTesting**）、`handlers.go` **4559 行**、迁移进度 **2/5** —— 全部精确复现。
- ✅ 第 5 节"完成报告要求"的诚实性口径（机制建立 vs 行为迁移 / 物理拆分 vs 逻辑解耦 / warning-only vs required / owner-verified vs command-verified / 本仓 vs iOS）是对抗 AI 自我拔高的有效纪律，**必须保留**。
- ❌ **P0 的"漂移仍在扩大"论据不可复现**：实测三组件 diff = **2 / 43 / 68**，与 2026-07-03 原评估完全一致，未扩大；brief 表里的 **4 / 68 / 75 在当前 iOS `main`（clean tree）上测不出来**。
- ⚠️ ProcessGroup 在 `turns/` 子目录而非 `blocks/`，与另两个组件不同源；brief 未提示。
- ⚠️ `handlers.go` 拆分建议漏掉最大凝聚簇（OpenCode `ocHandle*` ~14 函数 ~800 行），且 `handlers_pairing_relay.go` 命名错误 —— pairing 早已独立在 `pairing_handler.go`，不在 handlers.go。
- ⚠️ P2 低估工作量：hygiene 脚本目前是纯清单报告（无基线存储 / 对比 / strict 模式任何代码），CI（ci.yml）也根本没调用它。

按第五节的最小修订清单改完后即可施工。

---

## 二、事实核对（可复现）

| brief 声明 | 复现命令 | 结果 |
|---|---|---|
| `handlers.go` 4559 行 | `wc -l go-bridge/handlers.go` | ✅ 4559（精确） |
| BridgeProvider 1967 行 / 88 func / 34 ForTesting | `wc -l` + `grep -c 'func '` + `grep -c ForTesting` 于 `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift` | ✅ 1967 / 88 / 34（精确） |
| 已迁 2/5（DiffViewer + ToolBlock） | `ls ../cordcode-ios/shared-message-renderer/src/components/blocks/` | ✅ 仅 DiffViewer + ToolBlock（+ 各自 `.test.tsx`） |
| hygiene 脚本 warning-only 存在 | `ls scripts/check-architecture-hygiene.sh` | ✅ 存在，3602 字节，固定 `exit 0` |
| 三组件 diff = **2 / 43 / 68**（原评估列） | 见 F1 复现命令 | ✅ 精确复现 |
| 三组件 diff = **4 / 68 / 75**（gap-analysis 列） | 见 F1 复现命令 | ❌ **不可复现**，实测仍为 2 / 43 / 68 |

> 注：brief 第 1 节把 BridgeProvider god-object 写进"必读文件"但未给路径；实测路径是 `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`（在 `Services/Bridge/`，非第一轮评估暗示的 `Bridge/`）。

---

## 三、关键发现（按影响排序）

### F1【高】P0"漂移仍在扩大"的核心论据不可复现

brief 第 0 节把"止住 **仍在扩大** 的组件漂移"列为第二轮首要目标，并在 P0 表格用 gap-analysis 实测 **4 / 68 / 75**（vs 原评估 2 / 43 / 68）论证漂移在扩大。

复现命令（统一方法：`diff -u | grep '^[+-]' | grep -v '^[+-][+-]'`，仅计真实变更行，排除 `+++`/`---` 文件头与 `@@` hunk 头）：

```
ReasoningBlock:  变更行=2,  hunk=1     # brief 表称 4
ProcessGroup:    变更行=43, hunk=5     # brief 表称 68
NarrativeBlock:  变更行=68, hunk=4     # brief 表称 75
```

实测值与 2026-07-03 原评估**完全一致**（2 / 43 / 68），gap-analysis 声称的"扩大"在当前 iOS `main`（clean tree）上测不出来。可能原因：gap-analysis 测量时 iOS 仓还在 `codex/web-renderer-shared-c1` 分支且有未提交改动（完成审计第 129 行记录），而该批改动在合并到 main 前被回退或未带入。

**影响**：不影响 P0 是否值得做（重复真实存在，2 / 43 / 68 仍是该共享的重复），但"扩大中、越拖越贵"的紧迫性论据失效。若下一个 agent 在完成报告里照抄 4 / 68 / 75，会被任何复核立即打穿 —— 这恰好违反 brief 第 5 节自己立的"诚实区分"纪律。

**建议**：

1. brief 第 0 节改为"止住 message-web / remote-web 的**存量组件重复**"（去掉"仍在扩大"）。
2. P0 表格删掉"gap analysis 实测 diff"列，或加一句"diff 数值依赖计数法（变更行 / 含上下文的 `-u` 总行数）和分支状态，须以迁移时实测为准"。
3. 让下一个 agent 在每个组件迁移前用统一方法重测，并写进完成报告。

### F2【高】ProcessGroup 在 `turns/` 而非 `blocks/`，结构性差异被掩盖

brief 把 ReasoningBlock / ProcessGroup / NarrativeBlock 当作同质的"剩余 3 个组件"，但实际路径：

```
message-web/src/components/blocks/{ReasoningBlock,NarrativeBlock}.tsx
message-web/src/components/turns/ProcessGroup.tsx          ← 不同子目录
remote-web/src/renderer/components/blocks/{ReasoningBlock,NarrativeBlock}.tsx
remote-web/src/renderer/components/turns/ProcessGroup.tsx  ← 不同子目录
```

已迁的 DiffViewer / ToolBlock 与 ReasoningBlock / NarrativeBlock 都在 `blocks/`，ProcessGroup 在 `turns/`。这暗示它承担 turn/group 组合语义，可能与 block 组件有不同的 import 面与 host 交互；而共享包目前只有 `components/blocks/`，web-renderer plan（brief 的必读文档之一）第 50–68 行的目录设计里也没有 `turns/`。

**影响**：下一个 agent 按"剩余 3 个 blocks 组件"心智模型迁 ProcessGroup 时，会发现源路径不对、共享包没有对应子目录，可能临时塞进 `blocks/` 留下结构债。

**建议**：

1. brief P0 明确 ProcessGroup 源在 `turns/`，并预先决定它在共享包的位置（新设 `components/turns/` 还是归入 `blocks/`，迁移前必须定）。
2. 把 ProcessGroup 顺序风险评级上调：它不只是 diff 大，而是**结构性不同源**，验证成本高于 ReasoningBlock。建议迁移顺序仍是 ReasoningBlock → ProcessGroup → NarrativeBlock，但 ProcessGroup 单独留出一格"先验证 turn/group 行为可注入"的前置。

### F3【中】handlers.go 拆分建议漏掉最大凝聚簇，且 pairing 命名错误

实测 `handlers.go` 的职责分布（按函数群 + 行号）：

| 簇 | 代表函数 | 行号区间 | 规模 |
|---|---|---|---|
| **OpenCode proxy**（brief 未列） | `ensureOpenCodeSession` / `handleOpenCodeRPC` / `ocHandleListSessions/GetSession/GetSessionMessages/ListModels/ListAgents/ListProjects/CreateSession/ResumeSession/SendMessage/AbortGeneration/DeleteSession/FetchTodos` | 396–1195 | **~14 函数 / ~800 行 —— 最大可抽取凝聚块** |
| 通用 agent/capability/model/provider | `handleListModels/ListProviders/SetProvider/ListAgents/ListProjects/FetchTodos/GetUsage/RunDiagnostics/ListMemoryFiles/ReadMemoryFile` | 1195–1700 | ~10 函数（对应 brief 的 `handlers_agents.go` ✓） |
| session lifecycle | `handleCreateSession/SendMessage/RenameSession/ArchiveSession` | 1726–1900+ | （对应 brief 的 `handlers_sessions.go` ✓） |
| **Relay / session-file relay**（brief 误并入 pairing） | `startRelayIfNotRunning` / `startClaudeSessionFileRelay` / `startCodexSessionFileRelay` / `codexSessionFileRelayLoop` / `claudeSessionFileRelayLoop` / `claudeTranscriptRelay*` / `relayEvents` / `routeRelayOfflineEvent` / `FlushRelayOutboxes` / `disablesRelayIdleTimeout` | 2014–2803 | **~12 函数 / ~800 行** |

而 pairing / device / revoke / prekey **根本不在 handlers.go** —— 它们已经独立在 `go-bridge/pairing_handler.go` / `pairing_session.go` / `pairing_hardening.go`（handlers.go 里 13 处相关字样全是注释/引用，无函数定义）。

**影响**：

1. brief 推荐的 5 个目标文件里没有 `handlers_opencode.go`，而 OpenCode proxy 是单文件内**最大**的可抽取块 —— 不点它的名，下一个 agent 会先去拆更碎的 session/message 小簇，单位收益更低。
2. `handlers_pairing_relay.go` 名字暗示"把 pairing 也并进来"，但 pairing 早已自成文件；下一个 agent 照做可能把已独立的 `pairing_handler.go` 反而搅乱、或花时间在 handlers.go 里找不到 pairing handler。

**建议**：

1. brief 推荐文件清单加 `handlers_opencode.go`（OpenCode proxy 整簇），并标注它是最大一块、可作为第一个拆分目标（单步即可让 handlers.go 减重 ~800 行）。
2. 把 `handlers_pairing_relay.go` 改名 `handlers_relay.go`，明确只装 relay / session-file-relay / transcript-relay 簇；注明 pairing/device 已在独立文件、本轮不动。

### F4【中】P2 低估工作量：hygiene 脚本无基线机制，CI 也未调用

读 `scripts/check-architecture-hygiene.sh` 全文（3602 字节）：它目前是**纯存量清单**（NSLog/print/Logger 计数、ForTesting 计数、长文件扫描、protocol 文件计数），用 `count_rg` 和 `line_count_over` 两个辅助函数聚合输出；**没有任何基线存储、没有 per-file 指标对比、没有 strict 模式 env、脚本内不出现 `BridgeProvider` 任何字样**，最后固定 `exit 0`。

同时 `.github/workflows/ci.yml` **完全没有调用** hygiene 脚本（`grep hygiene .github/workflows/ci.yml` 为空）。

brief P2 的"实现方式建议"写"在 hygiene 脚本中记录 BridgeProvider 基线 / 新增可选 strict 模式 / CI 试点只开启净增长检查"—— 这三步**全是 greenfield**，且第三步需要先把脚本接入 ci.yml（brief 只用"如接入 CI"一笔带过）。

**影响**：下一个 agent 按字面理解可能以为只是"加一个 env var"，实际要写：基线文件格式 + 读取 + 对比逻辑 + strict 分支 + ci.yml 接入点。工时被低估，且容易把 strict 写成"全规则一刀切 fail"，违反 brief 自己"不要把所有 warning 一次性变 fail"的约束。

**建议**：brief P2 显式列为三步、并给出最小落地形态：

1. 新增基线存储（如 `scripts/hygiene-baseline.json` 或脚本内常量），记 BridgeProvider 当前 `lines / func / ForTesting`；
2. 脚本加 `CORDCODE_HYGIENE_STRICT=1` 分支：**仅当 BridgeProvider 任一指标净增时** `exit 1`，其余规则维持 warning；
3. 在 ci.yml 的 macbridge job 加 `CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh`（或先只跑 warning 路径证明接入、下一轮再升 strict）。

明确"第 3 步是接 CI，不是脚本内部行为"。

### F5【低】执行顺序与 P0/P1/P2 标签不一致且未解释

- 第 2 节按 **P0（web）→ P1（handlers.go）→ P2（gate）** 展开。
- 第 6 节"推荐执行顺序"按 ReasoningBlock → ProcessGroup → NarrativeBlock → **BridgeProvider gate** → **handlers.go 拆分** → CHANGELOG，即 **P0 → P2 → P1**。

重排本身合理（先把 gate 立起来锁基线，再做最大的机械重构，避免拆分期间 god-object 偷偷长肉），但 brief 没解释为什么要换序，下一个 agent 可能困惑两处顺序谁优先。

**建议**：第 6 节开头加一句解释，例如"先做 gate 再拆 handlers.go，是为了在机械重构前先锁定 BridgeProvider 基线"。

### F6【低】跨仓工作与本仓 CHANGELOG 的记录边界未说明

P0 全部在相邻 iOS 仓施工，但 brief 第 6 步要求"更新 `CHANGELOG.md` 与完成报告"。MacBridge 的 CHANGELOG 是否应记录 iOS 侧的组件迁移，存在边界模糊 —— 第一轮 C 批次同样是 iOS 侧，CHANGELOG 把它写进了"2026-07-04 — 第一轮整体完成"小节，可作为先例。

**建议**：brief 显式写"P0 产物在 iOS 仓提交，按第一轮先例在 MacBridge CHANGELOG 记一条跨仓进度"。

---

## 四、brief 做对的部分（建议保留）

- **范围聚焦** S1+S2+S5、明确不做 god-object 大手术、给第三轮设触发条件 —— 与 gap-analysis 第四节完全对齐，是稳健的"止住恶化 + 降低下一轮摩擦"路径。
- **"明确不做"清单**（第 3 节）诚实且边界清晰。
- **第 5 节"完成报告要求"**强制区分 5 组概念（机制建立 vs 行为迁移 / 物理拆分 vs 逻辑解耦 / warning-only vs required / owner-verified vs command-verified / 本仓 vs iOS）—— 这套口径直接照抄自第一轮完成审计的诚实性方法，是对抗"AI 自我拔高"的有效纪律，**必须保留**。
- **第 6 节给 NarrativeBlock 写了失败降级路径**（"差异比预期更深时不要用 fallback / 双实现冒充，先交付前两个 + gate"）—— 正确的工程诚实度，与项目宪法一致。
- **P0 / P1 实施原则**反复强调"不伪造 fixture、不改 CSS className / DOM / test id、循环依赖不新增接口绕开"—— 与工程宪法原则 1（不用 fallback / mock 掩盖真实失败）一致。

---

## 五、对 brief 的最小修订清单（agent 可直接改）

1. **第 0 节**："仍在扩大"→"存量重复"；删漂移增长论据。
2. **P0 表格**：删"gap analysis 实测 diff"列，或加"计数法说明 + 以迁移时实测为准"。
3. **P0 加一条**：ProcessGroup 源在 `turns/`，迁移前先定它在共享包的位置；其顺序风险高于 ReasoningBlock。
4. **P0 实施原则加**："迁移前用统一 diff 计数法重测当前 diff，写进完成报告。"
5. **P1 推荐文件清单**：加 `handlers_opencode.go`（最大凝聚簇，建议作首拆目标）；`handlers_pairing_relay.go` 改 `handlers_relay.go`，注明 pairing 已在独立文件、不动。
6. **P2 实现方式**：显式三步（基线文件 + strict 分支 + ci.yml 接入），第 3 步标明"接 CI 不是脚本内部行为"。
7. **第 6 节**：开头一句解释 P0 → P2 → P1 的换序理由。
8. **第 5 或 6 节**：补"P0 产物在 iOS 仓提交、按第一轮先例在 MacBridge CHANGELOG 记跨仓进度"。

---

## 六、评审边界

- 本评审**未**改动任何代码或文档；仅读 brief、4 份输入文档与代码现状。
- diff 计数为 2026-07-04 当天 iOS `main`（clean）实测；若 iOS 仓后续切换分支或恢复未提交改动，数字会变 —— 这正是 F1 建议"迁移时重测"的原因。
- 未评审 brief 的"产品 / UX 正确性"（如 NarrativeBlock 的 git directive summary 宿主差异是否真能用 labels 注入收敛）—— 这要等真正迁移时验证；brief 自己也把它标为最高风险、留了降级路径，处理得当。
- 未复核 BridgeProvider func 数"78→88"中"78"的历史口径（第一轮评估原计数定义可能略不同），但当前 **88 与 gap-analysis 一致**，gate 以"当前 88 为基线、禁止净增"成立，与历史口径无关。
- 本报告遵循 `.gitignore` 现状（`/docs/*` 默认忽略，仅特定 `!` 放行）—— 是否纳入版本控制属 owner 治理决策（见第一轮完成审计建议 #2），本评审不擅自改变。
