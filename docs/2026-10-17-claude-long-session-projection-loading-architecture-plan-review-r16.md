# Claude 长 session 打开方案复审 R16

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v20**
- 全文 SHA-256：`ec8904b3f5d2e2dc96dc3b37f06ee525b375fda075c3cc67efce4f9060fce35b`
- 正文 SHA-256：`7b1c84141a9367c8d0db5dfd1886583e64a3a32569e354f724a31435d9a40a4e`，按交接块切分规则实测，与自报前缀一致
- scope: R15 五项闭合复核 + v20 新增 token 生命周期、总预算、恢复事务与 source-ledger 生产链的全局一致性检查
- blockers: **2**（F-48、F-49，均为 P1）；advisories: **1**（F-50）
- implementation_readiness: F-42/F-44 的设计主链可进入对应冻结/实施准备；F-45 以 OD-9 pending 精确挡住 growth-trust，默认档可支撑 S-2 设计。F-46/F-47 各有一处会直接改变实现或验收的矛盾，需先修订。

## 来源身份

### G1：源码分析前

按方案 §2.1 与两仓 worktree 枚举选择 native-message-timeline 配对：

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | 见下；方案指定来源；配套 I 的下述路径、分支、完整提交 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `8b6e44f9bdfb5ad5e5b708de8450a1506c7c3adc` | 见下；方案指定来源；配套 M 的上述路径、分支、完整提交 |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897` | 干净；只读架构参考，不代表目标运行二进制 |

M 的 G1 状态：

```text
 M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r14.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r15.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

本轮使用的 replica/store/window/transport/presentation 源码在 I 从 `e52daa275946ee0d41749aa7146ec8bbc8b05c29` 到 `8b6e44f9bdfb5ad5e5b708de8450a1506c7c3adc` 的路径限定 diff 为空，且不在下列并发编辑清单内。I 的并发开发在评审期间继续推进：G2 前后可见 `NativeTimelineViewController.swift`、`NativeTimelineSmoothHeightTests.swift`、`StreamingHeightPresentation.swift`、`StreamingHeightPresentationTests.swift` 脏；本报告不依赖这些文件，也不触碰。M/U 分支和提交保持不变。

### G2：首次写报告前

M 状态仅比 G1 多本报告；方案哈希仍为 `ec8904…`，`git diff --check` 通过。I 仍为上述并发编辑，提交/分支不变。U 干净。无构建、安装、重启、真实会话写入或部署，因此不触发构建/部署 G3。

## 已完整重读 R15

已按用户要求完整读取 `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r15.md`。本轮没有把 R15 的“已闭合/未闭合”当作结论来源；每个影响判定的锚点均重新打开核对。

## 亲核锚点

| 方案命题 / 原引用 | 实际源码与摘录 | 判定 |
| --- | --- | --- |
| F-44：`ProjectionStore.swift:612-619/:1180/:1197-1203` 的 await 重入点 | I `Models/ProjectionStore.swift:612-619`：`await replica.applyWindowPage(...)` 后 `await commitIfObserved(...)`；`:1180`：`await presentationEngine.apply(...)`；`:1197-1203` 之后才写 store State | 命中。v20 的 per-session 恢复门是必要设计，不再依赖“@MainActor 自动串行”的错误论证 |
| F-42：patch 局部修改来源 | M `go-bridge/projection_types.go:373-379` 有 `UpsertTurns/PartOps/TurnStateOps`；I `Models/ProjectionStore.swift:1241-1243` 按顺序应用 upserts/stateOps/partOps | 命中。v20 的同源 token / null 失效规则覆盖 R15 反例；混合同一 patch 的边界见 F-50 |
| F-44：空页与非空页旧语义 | I `Models/SessionProjectionWindow.swift:123-126`：空页 `return self`；`:173`：非空 `max(syncRev, page.syncRev)` | 命中。v20 明确恢复专用 apply 无条件写响应 head，不再复用这两条分支 |
| F-47：source-ledger 生产链 | M `go-bridge/handlers_relay.go:877` 安装 ledger；`:882-884` 调 `Observe`；`claude_source_trace.go:99` 计算当前 prefix digest；`:75/:83` 增长时还读旧/新 prefix；`:122` 用 `io.CopyN` | 命中。v20 将其纳入 S-2 是必要修订；但 §3.2.3 结尾仍留矛盾（F-49） |
| F-47：持久 ledger 对账 | M `projection_source_transaction.go:284-287` 校验 SegmentGeneration；`claude_source_state.go:163-166` 对 gap/generation mismatch fail-closed | 现有 source ledger 确有防重写语义；v20 需在 S-2 中保持该护栏并计入生产 IO |
| F-46：transport 上限 | I `Services/Bridge/BridgeWebSocketConnection.swift:12`：`maximumMessageBytes = 50 * 1024 * 1024`；`RelayBridgeFrameConnection.swift:823` 默认同值，`:859-862` 超限报 `relay.decompressed_too_large` | 命中。gzip 不取消逻辑解压上限；v20 必须定义与 50MiB 自洽的总响应预算（F-48） |

## R15 意见闭合判定

### F-42：闭合

v20 的五条 token 生命周期规则解决了 R15 的核心反例：full/对账响应同帧配对；局部 patch 后 token 置 null；null 必须换取当前终态；写盘保存 `tokenValid`；schema 迁移缺失按无效处理。E-11 也加入 patch 后写盘与 Mac 回退旧版本两组反例。

### F-44：闭合

v20 删除了“空页保持 0 直到下一条 patch”的错误语义，规定恢复专用 apply 无论 turns 是否为空都显式发布响应 epoch/head/scalar/修正集；并增加 per-session 恢复门，禁止早到 push 按 rev=0 校验。§7.2.12 覆盖每个 await 边界和零新增进 head，可构造确定性验收。

### F-45：闭合为条件设计

v20 正确撤回“baseDigests 与 growth-trust 互相兜底”，承认二者共享 Kernel 错误源；growth-trust 变为 OD-9 pending，并把默认路径改为 source prefix 校验。该 pending 有明确选项、风险、影响切片和默认档，不阻塞 S-2 按安全默认实施，符合方案门规则。

### F-46：未闭合，见 F-48

changedTurns 分页、部分修正不清 syncing、单 turn 超限 typed fallback 的方向正确；但总预算条款内部互相矛盾，且 OD-7 决策行未真正收录 8MiB/48MiB 数值。

### F-47：未完全闭合，见 F-49

S-2 范围扩展和真实 owning 调用链测试正确；但同一小节残留 v19 的 IO 上界，与默认 prefix 整读直接冲突。

## 新意见

### F-48 [P1 / 阻塞] 对账响应预算同时声明 8MiB 总上限与 48MiB 单 turn 上限，且 OD-7 未登记该数值决定

**位置**：§3.8 对账响应总预算、§7.3 `anchor-newer 增量页 bytes`、§4 OD-7。

**证据**：

- §3.8 写：`turns + anchorRefresh + changedTurns + scalar/metadata 合计 ≤ 拟议 8MiB`。
- 同一条又写：填充优先级中 `anchorRefresh` 为 never-split 特例，`单 turn 上限 48MiB`；§7.3 也同时写“合计 ≤8MiB”和“单 turn ≤48MiB”。
- OD-7 决策行状态仍是 v15 的 4MiB/256、128MB、2 页、100 turns，没有 8MiB 总预算或 48MiB 特例；但正文称该数值“OD-7 一并裁决”。

**影响**：一个 20MiB anchorRefresh 同时满足“单 turn ≤48MiB”并违反“合计 ≤8MiB”。实施者无法选择确定算法：要么在 8MiB 处转 typed oversized/fallback，要么允许 anchorRefresh 特例到 48MiB。测试 likewise 无法同时断言；OD-7 的“已定案”状态也没有覆盖正文新数值。

**最小修订**：明确普通预算和 never-split 特例的关系。例如：

- `turns + changedTurns + scalar + metadata ≤ 8MiB`，且 anchorRefresh 独立受 48MiB 绝对上限；或
- 全部字段合计 ≤8MiB，任何单字段超过即 typed oversized；或
- 定义普通总预算 8MiB，anchorRefresh 唯一例外可至 48MiB，并给出总响应绝对上限、耗时目标与 fallback。

同时把最终数值和例外写入 OD-7 状态，或将其列为新的 pending 决定并精确挡住 S-7 冻结。补验收：8MiB 边界、20MiB anchor、47MiB anchor、48MiB 边界、48MiB+1、changedTurns 分页与 anchor 特例并存。

**闭合标准**：同一输入只有一个可满足的预算判定；OD 状态与正文数值一致；总响应绝对上限低于 50MiB 并留协议封装余量；测试覆盖普通预算与超大 anchor 特例。

### F-49 [P1 / 阻塞] §3.2.3 结尾仍保留 v19 的“重启不整读 prefix”IO 上界，推翻 v20 默认档

**位置**：§3.2.3 四态分类末尾 `各态 IO 上界：... (ii) = stat + 尾部 4KiB 扫描 + suffix`；对照同节 v20 默认档和 §7.1.2/§7.3。

**证据**：同节上方已经写明 v20 默认 `(ii) = source-ledger prefix digest 校验（整读 prefix）+ suffix 折叠`，并给出 97MiB 约 0.3–1s 的成本；§7.1.2 要求测试读量“默认含 prefix 校验整读”；§7.3 也写“prefix 校验整读是生产默认”。但末尾汇总仍写 `(i)/(ii) = stat + 尾部 4KiB 扫描 + suffix`，等于保留被 v20 否决的 growth-trust IO 结论。

**影响**：S-2 实施者可以按末尾汇总跳过 prefix 校验，直接违反 OD-9 默认档；测试也可以把生产必需的 97MiB 读判为失败。该冲突正处在同一小节，不能靠 §3.9 路径总表解决。

**最小修订**：删除或重写该汇总行，区分：

- 默认 `(ii)`：source prefix digest 整读 + stat/尾部扫描 + suffix 重投影；
- OD-9 启用后的可选 `(ii-fast)`：跳过 prefix 校验，仅 stat/尾部扫描 + suffix，并显式接受未检测重写风险；
- `(iii)` 全等快速匹配是否真的能跳过 source-ledger `Observe`，需说明其与持久 generation 对账的关系。

**闭合标准**：同一重启输入在 OD-9 未裁决前只有一个默认 IO 行为；§3.2.3、S-2 测试和 §7.3 的读量断言一致；OD-9 启用档单独列示。

### F-50 [P2 / 建议] token 规则应补同一 patch 内 upsert 与局部 ops 的顺序反例

**位置**：§3.8 token 生命周期第 2/3 条、§7.1.5 对账断言、E-11。

v20 已规定 `partOps/turnStateOps` 后 token 置 null，总体不变量成立。但现有 patch 应用顺序是 upserts → turnStateOps → partOps，同一 patch 可以同时携带 `upsertTurns` 与后续局部 ops。若实现把 token 绑定到 upsert 时的内容，再应用同 patch 的 state/part ops，仍会得到“最终内容 + 中间版本 token”。可选优化“patch 附带新 token”更要求 token 在全部 ops 应用后的最终 turn 上计算。

建议冻结文案明确：token 归属以 patch 应用完成后的最终 turn 为准；任一同 patch 局部 op 使 upsert 自带 token 失效，除非新 token 是按最终内容计算。E-11/Mac-iOS 深度相等测试加入 mixed patch（upsert + turnStateOp + partOp）反例。

## 覆盖与剩余门

- R15 F-42/F-44 闭合；F-45 以 OD-9 pending 加安全默认档闭合；F-46/F-47 尚未完全闭合。
- E-3 仍是 S-7 开工前证据门；S-7/S-8 协议冻结仍是实施前置；E-4/E-5/E-6/E-7/E-10/E-11 与 owner 真机矩阵仍是实施完成门。
- OD-9 pending 不阻塞 S-2 默认档，但启用 growth-trust 前必须 owner 裁决。
- 本轮未运行 build/test/UI automation，不改方案和业务代码；只读源码、重读报告与哈希校验。
