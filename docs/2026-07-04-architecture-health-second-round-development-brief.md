# 架构健康第二轮开发交接文档

日期：2026-07-04
输入来源：
- `docs/2026-07-04-architecture-health-goal-gap-analysis.md`
- `docs/2026-07-03-architecture-health-execution-plan完成情况.md`
- `docs/2026-07-04-architecture-health-completion-audit.md`
- `docs/2026-07-04-architecture-health-second-round-brief-review.md`
- `docs/2026-07-04-architecture-health-second-round-brief-review-r2.md`
- `docs/2026-07-04-architecture-health-second-round-brief-review-r3.md`
- 本轮讨论结论：第一轮完成了机制与第一波收口；web 组件存量重复仍在，`BridgeProvider` 是静态但无门禁的历史下沉点。

本文定位：给下一位开发 agent 的直接施工输入。它不是新的健康评估，也不是执行完成报告。

---

## 0. 核心判断

第一轮不是失败；它完成了自己承诺的第一波治理：

- capability 单源化与 `config/` 死重删除已达成；
- web shared renderer 机制已建立，但只迁了最容易的 2/5；
- god-object 只铺了 characterization 测试，未开始本体拆分；
- 工程宪法和 warning-only hygiene 脚本已存在，但尚未形成实际门禁。

第二轮的目标不是上来做大规模重构，而是先改变恶化方向：

1. 收敛 `message-web` / `remote-web` 的存量组件重复；
2. 阻止 `BridgeProvider` 继续净增长；
3. 降低下一轮拆分 `handlers.go` / iOS god-object 的导航和 review 成本。

一句话范围：**第二轮做 S1 + S2 + S5 试点，不做 BridgeProvider / ChatViewModel 大手术。**

---

## 1. 必读文件

开发前必须读：

1. `AGENTS.md` / 根目录项目指令中的 Build & test、Backend runtime model、CHANGELOG 规则。
2. `docs/2026-07-04-architecture-health-goal-gap-analysis.md`。
3. `docs/2026-07-03-web-renderer-shared-package-implementation-plan.md`。
4. `docs/engineering-constitution.md`。
5. 若修改 `go-bridge/handlers.go`：`GO_BRIDGE_ARCHITECTURE.md`。
6. 若修改相邻 iOS 仓 `../cordcode-ios`：`../cordcode-ios/CLAUDE.md` 与 `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`。

硬约束：

- 未经 owner 明确允许，不运行 UI tests、snapshot tests、simulator automation 或真机安装。
- 不在生产路径添加 fallback、mock、placeholder、假数据或缓存快照来制造“先跑起来”的假成功。
- 真实路径失败时保留真实错误并分析根因。
- protocol、pairing、relay、connection state、capability 字面契约不在本轮顺手修改。
- 每个施工单元都要有定向验证；验证失败不得靠跳过测试交付。

---

## 2. 第二轮范围

### P0：完成 web shared renderer 剩余 3 个组件迁移

目标：把 `../cordcode-ios/message-web` 与 `../cordcode-ios/remote-web` 中仍重复的 3 个组件迁入 `../cordcode-ios/shared-message-renderer`。

优先顺序：

1. `ReasoningBlock`：diff 最小，先验证 labels/host adapter 机制；
2. `ProcessGroup`：重复度高，源文件在 `turns/` 子目录，注意 turn/group 组合行为；
3. `NarrativeBlock`：产品差异最大，最后迁，重点处理 git directive summary 的宿主差异。

当前风险信号：

| 组件 | 原评估 diff | 结构位置 | 判断 |
|---|---:|---|---|
| `ReasoningBlock` | 2 行 | `components/blocks/` | diff 最小，适合先迁 |
| `ProcessGroup` | 43 行 | `components/turns/` | 结构性不同源，迁移前先决定共享包中是否新增 `components/turns/` |
| `NarrativeBlock` | 68 行 | `components/blocks/` | 差异最大，必须保留宿主语义 |

注意：评审在当前 iOS `main` clean tree 上复测三组件 diff 为 2 / 43 / 68，与原评估一致；gap analysis 中的 4 / 68 / 75 在该时点不可复现。因此本 brief 不再使用“漂移仍在扩大”作为 P0 论据。P0 仍然成立，因为存量重复真实存在、共享包机制已就绪，且继续双写会维持长期维护成本。

实施原则：

- 共享包只承载纯展示、纯解析和稳定类型；
- 宿主能力通过 `RendererHost` / labels / props 注入；
- 共享包不得 import 任一宿主私有 bridge、relay、WebKit、OpenCodeiOS 路径；
- 两个 app 可保留薄 wrapper，但 wrapper 只负责 host/labels/宿主差异注入；
- 不改 CSS className、DOM 结构、test id，除非有测试证明原结构已经错误；
- 对已有差异逐项归类：真实产品差异保留为 host/label 注入，意外漂移收敛到共享实现。
- 迁移每个组件前用统一 diff 计数法重测当前差异，并把实测数字写进完成报告；建议方法为 `diff -u | grep '^[+-]' | grep -v '^[+-][+-]'`，仅计真实变更行，排除文件头。
- `ProcessGroup` 迁移前必须先决定共享包结构：推荐新增 `shared-message-renderer/src/components/turns/ProcessGroup.tsx`，不要为了目录省事把 turn/group 语义塞进 `blocks/`。

建议每迁一个组件就完成一次最小闭环：

```bash
cd ../cordcode-ios/shared-message-renderer
npm run typecheck
npm run test

cd ../cordcode-ios/message-web
npm run typecheck
npm run build

cd ../cordcode-ios/remote-web
npm run typecheck
npm run build
```

如已有组件级 vitest，可补定向组件测试；没有现成测试时，不为绕过构建失败制造 fixture 假路径。

完成标准：

- `message-web` 和 `remote-web` 不再各自持有这 3 个组件的完整重复实现；
- 共享包 exports 覆盖 5/5 目标组件；
- 两个 app 的 typecheck/build 通过；
- 组件差异清单写入完成报告：哪些差异被统一，哪些通过 host/labels 保留。

---

### P1：`go-bridge/handlers.go` 物理分发

目标：降低 4559 行 `handlers.go` 的导航和 review 成本；本轮只做物理拆文件，不做行为解耦。

边界：

- 不改 RPC 行为；
- 不改 session registry、relay、agent driver、protocol 字段含义；
- 不引入新的 abstraction 层；
- 不把 unrelated helper 顺手重构到别处。

推荐拆分方式：

```text
go-bridge/
  handlers.go                    # Handler 类型、构造、核心共享工具、路由入口
  handlers_opencode.go           # OpenCode proxy / ocHandle* 整簇，当前最大可抽取块
  handlers_sessions.go           # list/get/send/resume/stop session 相关
  handlers_messages.go           # get_session_messages / transcript / pagination 相关
  handlers_agents.go             # backend/model/provider/capability/memory/diagnostics 相关
  handlers_files.go              # list_directory / file attachments / workspace helpers
  handlers_relay.go              # relay / session-file relay / transcript relay 相关
```

实际拆分以当前源码职责为准，不强行套上述名字。优先按现有 handler 方法群自然边界移动。评审复核显示 OpenCode proxy 簇约 14 个函数、约 800 行，是 `handlers.go` 内最大凝聚块，建议作为首拆目标；relay / session-file relay / transcript relay 也是约 800 行的凝聚块（约 2014–2803 行），适合作为第二目标。该簇除 13 个 relay-by-name 函数外，还有 4 个不带 relay 之名的 transcript 探测 helper（`detectClaudeTranscriptState` / `detectCodexTranscriptTaskState` / `scanCodexTranscriptTaskEvents` / `codexEventPayloadType`），它们是 session-file-relay 循环调用的状态探测函数，必须与 relay 簇整组搬到 `handlers_relay.go`，按名字留在 `handlers.go` 会留下反向依赖。pairing / device / revoke / prekey 已经独立在 `pairing_handler.go` / `pairing_session.go` / `pairing_hardening.go`，本轮不要把它们重新搅入 `handlers.go` 拆分。

实施原则：

- Go 同 package 多文件拆分即可，不改 public API；
- 只移动函数和紧邻 helper，尽量避免同时改函数体；
- 每完成一个主题拆分就跑一次定向编译或测试，方便定位移动过程中的遗漏；
- 对循环依赖不要新增接口绕开，先重新评估是否移动过头。

建议验证：

```bash
go test ./go-bridge -run 'Test.*Session|Test.*Message|Test.*Backend|Test.*Capability|Test.*Pagination' -count=1
go test ./go-bridge/... -count=1
go build ./go-bridge
```

完成标准：

- `go-bridge/handlers.go` 明显变薄，主题 handler 分布到多个同 package 文件；
- 行为测试无回归；
- 完成报告明确说明“物理分发，不是逻辑解耦”。

---

### P2：工程 hygiene gate 试点升级

目标：把第一轮 warning-only 的治理机制推进到“一条规则可执法”，同时避免让存量债务把 CI 永久打红。

推荐试点：**BridgeProvider 净增长 gate**。

原因：

- gate 的治理信号不是 `BridgeProvider` 1967 行本身，也不是已被 r2 评审证伪的“78 func → 88 func”增长 delta；真实信号是它是连接特性的历史下沉点（近 6 次相关提交均触碰它，2026-06-29 一次 +125 行），当前 88 func / 34 ForTesting 且无任何净增长门禁；
- 体积冻结比直接拆分成本低，能立刻改变默认写入路径；
- 不需要先完成 BridgeProvider 本体拆分。

建议 gate 语义：

- 不要求立刻把 `BridgeProvider.swift` 降到某个理想行数；
- 只阻止本轮之后继续净增长；
- 允许减少；
- 如确需增加，必须在同 PR 中说明为什么不能放进新职责文件，并同步更新基线。

实现方式建议：

1. 新增基线存储：可用 `scripts/hygiene-baseline.json`，也可在脚本内用集中常量记录 `BridgeProvider.swift` 当前 `lines / func / ForTesting`（当前评审复测为 1967 / 88 / 34）。如果选脚本内常量，必须保持更新条件清楚可审计。
2. 给 `scripts/check-architecture-hygiene.sh` 新增 `CORDCODE_HYGIENE_STRICT=1` 分支：只在 `BridgeProvider.swift` 任一指标净增时 `exit 1`；其它现有 hygiene 规则继续 warning-only。
3. 接入 CI 是独立步骤，不是脚本内部行为。可在 `.github/workflows/ci.yml` 的 macbridge job 增加 `CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh`；若担心直接 required 过猛，至少先接 warning 路径并在同轮说明 strict 接入阻塞点。
4. 后续第三轮再把拆完的文件纳入行数上限。

评审复核现状：当前 hygiene 脚本是纯清单报告，固定 `exit 0`，没有基线、对比、strict 模式；当前 CI 也未调用它。因此 P2 是一个小型 greenfield 治理任务，不是给已有逻辑加一个 env var。

注意：

- 如果 strict 模式会触发大量无关 warning，不要把所有 warning 一次性变 fail；
- 不要通过降低统计准确性来让 gate 变绿；
- 不要把真实增长藏进字符串、生成文件或测试-only path。

建议验证：

```bash
scripts/check-architecture-hygiene.sh
CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh
```

如接入 CI，再跑对应 workflow 的本地等价命令或至少验证 shell 脚本 exit code。

完成标准：

- 有一条可 repeat 的 strict gate 能防止 `BridgeProvider` 净增长；
- 默认 warning-only 路径仍保留，避免本地开发被未收敛债务全部阻塞；
- 文档说明基线更新条件。

---

## 3. 明确不做

本轮不做：

- `BridgeProvider` 本体拆分；
- `ChatViewModel+Generation.swift` / `ChatViewModel+CodexStreaming.swift` / `ChatViewModel+SessionManagement.swift` 大方法拆分；
- `ChatUIKitContainerView.swift` 大规模结构重组；
- Mac↔iOS 状态模型统一；
- protocol/capability 新能力设计；
- UI automation、snapshot tests、simulator automation 或真机自动安装，除非 owner 明确授权。

这些不是不重要，而是风险和验证成本都高于本轮目标。第二轮应先完成“止住恶化 + 降低第三轮拆分摩擦”。

---

## 4. 第三轮触发条件

不要让“测试保护还不够”变成永久延期。第三轮可以启动 god-object 本体拆分，但必须选择明确子域并采用 extract-and-test。

建议触发条件：

- web shared renderer 5/5 目标组件迁移完成；
- `BridgeProvider` 净增长 gate 已在 strict 模式下可跑；
- `handlers.go` 已完成物理分发，MacBridge 侧 handler 导航成本下降；
- 为 `BridgeProvider` 选定一个子域，并列出该子域的可执行不变量测试。

建议第三轮第一个子域：

- connection strategy；
- transport creation；
- recovery ownership；
- session/history synchronization；
- capability/backend descriptor mapping。

一次只拆一个子域。不要等所有 characterization 测试完美后才开始；边抽边补测试，但每次抽取都必须有行为不变量守护。

---

## 5. 交付报告要求

第二轮完成报告必须按下面结构写：

1. 已完成什么；
2. 为什么这样做；
3. 做了哪些取舍；
4. 验证命令与结果；
5. 未覆盖风险；
6. 下一轮应接手的具体入口。

必须诚实区分：

- 机制建立 vs 行为已迁移；
- 物理拆分 vs 逻辑解耦；
- warning-only vs required gate；
- owner-verified vs command-verified；
- 本仓验证 vs 相邻 iOS 仓验证。

P0 的代码产物在相邻 iOS 仓提交；按第一轮先例，MacBridge `CHANGELOG.md` 仍应记录一条跨仓架构治理进度，并在完成报告中明确哪些验证发生在 iOS 仓、哪些发生在 MacBridge 仓。

---

## 6. 评审采纳情况

本 brief 已按 `docs/2026-07-04-architecture-health-second-round-brief-review.md` 第五节的 8 条最小修订清单全部修订：

1. 已把“漂移仍在扩大”改为“存量重复”；
2. 已移除不可复现的 4 / 68 / 75 论据，并要求迁移时重测；
3. 已补充 `ProcessGroup` 位于 `turns/` 及共享包目录决策；
4. 已补充统一 diff 计数法；
5. 已补充 `handlers_opencode.go` 并把 `handlers_pairing_relay.go` 修正为 `handlers_relay.go`；
6. 已把 P2 改成基线存储、strict 分支、CI 接入三步；
7. 已解释推荐执行顺序中的 P0 → P2 → P1；
8. 已补充跨仓 CHANGELOG 记录边界。

未采纳意见：无。评审意见均为可逆文档修订，不改变第二轮范围，且能降低施工 agent 的事实误读风险。

r2 评审追加发现：P2 原先沿用“BridgeProvider 从 78 func 增到 88 func”的增长论据，但该差异同样是计数口径伪象，当前 brief 已改为“静态 god-object + 历史下沉点 + 无门禁”。该修订不改变 P2 的 gate 设计，只修正论据来源。

r3 评审为清理复核（无新发现）：确认 r2 的 F7 修订落地，并对 brief 全部量化论据做了可复现性总扫，全部通过（handlers.go 4559 行、BridgeProvider 1967 / 88 / 34、web 三组件 2 / 43 / 68、OpenCode 簇 15 函数 / 809 行、relay 簇 ~790 行、hygiene warning-only、CI 未接入、pairing 已独立）。唯一被采纳的施工提示已写入 P1：relay 簇携带 4 个不带 relay 之名的 transcript 探测 helper，须整组搬迁以防反向依赖。r3 明确结束评审循环，不建议再开 r4。

---

## 7. 推荐执行顺序

本节采用 P0 → P2 → P1，而不是章节顺序 P0 → P1 → P2：先把 web 重复迁移收口，再锁住 `BridgeProvider` 净增长基线，最后做 `handlers.go` 大文件物理分发。这样可以在机械拆分前先建立一个最小治理门禁，挡住下一次连接特性提交把 iOS god-object 悄悄带大。

1. 迁 `ReasoningBlock`，跑 shared/message-web/remote-web typecheck/build；
2. 迁 `ProcessGroup`，重复验证；
3. 迁 `NarrativeBlock`，重点记录宿主差异；
4. 做 `BridgeProvider` 净增长 strict gate；
5. 物理拆分 `go-bridge/handlers.go`；
6. 更新 `CHANGELOG.md` 与完成报告。

如果执行中发现 `NarrativeBlock` 的产品差异比预期更深，不要用 fallback 或双实现冒充迁移完成；保留失败现场，先交付前两个组件与 gate，再把 NarrativeBlock 的阻塞写成明确 follow-up。
