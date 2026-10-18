# Claude 长 session 打开方案复审 R17

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v21**
- 全文 SHA-256：`ef1f60cf5f0313c5017aff346a27211d4e9b5bde731a9e4c8d636f6c11a6a539`
- 正文 SHA-256：`876f7c1cd52b93f60ff85a2456dfb5c2f959fa9c199fbfff0753aa1caef8a2d4`，按交接块切分规则实测一致
- scope: R16 三项闭合复核 + v21 分层预算、默认 IO 路径与 mixed-patch token 的全局一致性检查
- blockers: **2**（F-48、F-49 仍未完全闭合）；advisories: **1**（F-51）
- implementation_readiness: F-50 已闭合。OD-9 仍为合法 pending，不阻塞 S-2 默认档。S-7 的响应预算冻结仍被 F-48 阻塞；S-2 的重启 IO 契约仍被 F-49 阻塞。

## 来源身份

### G1：源码分析前

按方案 §2.1 与 worktree 枚举选择 native-message-timeline 配对：

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | 见下；方案指定来源；配套 I 的下述路径、分支、完整提交 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `c2a59bf318ce154795a5e66f83d207c9780eccfa`（G2 交付时；G1 为 `e26cdec7a92df8e58c8f1ed2681b949880df0429`，见下） | 见下；方案指定来源；配套 M 的上述路径、分支、完整提交 |
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
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r16.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

I 的并发开发在 G1 时从 R16 的 `8b6e44f9bdfb5ad5e5b708de8450a1506c7c3adc` 推进到 `e26cdec7a92df8e58c8f1ed2681b949880df0429`，且仅 `OpenCodeiOS/OpenCodeiOSTests/NativeTimelineSmoothHeightTests.swift` 脏；本报告源码锚点在该身份下核对。报告校验期间并发开发继续提交并清理工作树，G2/交付时为 `c2a59bf318ce154795a5e66f83d207c9780eccfa` 且干净；上述五个方案锚点文件在 `e26cdec…→c2a59bf…` 的路径限定 diff 为空，源码结论不受影响。U 干净。

### G2：首次写报告前

M 仅比 G1 新增本报告；v21 全文/正文哈希未变，`git diff --check` 通过。I 如上所述在交付前推进到 `c2a59bf…`，方案锚点文件无相关 diff；最终复核时另有并发未跟踪 `docs/2026-10-18-native-timeline-late-stream-cumulative-cost-plan.md`，与本报告命题无关且未触碰。U 干净。未构建、安装、重启、写入真实会话或部署，因此不触发构建/部署 G3。

## 已完整重读 R16

已完整读取 `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r16.md`。本轮没有沿用 R16 判定作结论来源；三项修订均重新对照方案正文和源码锚点。

## 亲核锚点

| 方案命题 / 原引用 | 实际源码与摘录 | 判定 |
| --- | --- | --- |
| F-48：transport 解压上限 | I `Services/Bridge/BridgeWebSocketConnection.swift:12`：`maximumMessageBytes = 50 * 1024 * 1024`；`RelayBridgeFrameConnection.swift:823` 默认同值，`:859-862` 超限报 `relay.decompressed_too_large` | 命中。48MiB 总硬顶低于 50MiB，但分层填充还需闭合（见 F-48） |
| F-49：source-ledger 生产链 | M `go-bridge/handlers_relay.go:877` 安装 source ledger，`:882-884` 调 `Observe`；`claude_source_trace.go:99` 对当前 cursor 前 prefix 做 digest，`:75/:83` 增长场景还读取旧/新 prefix，`:122` 为 `io.CopyN` | 命中。v21 的四态汇总已修正，但组合路径仍残留零整读结论（见 F-49） |
| F-50：patch 应用顺序 | I `Models/ProjectionStore.swift:1241-1243` 按 `upsertTurns → turnStateOps → partOps` 应用；M `projection_types.go:377-379` 允许三类 ops 同 patch 出现 | 命中。v21 “token 绑定全 ops 后终态”与 mixed-patch 测试闭合 R16 建议 |

## R16 意见闭合判定

### F-48 [P1 / 仍阻塞] 分层预算概念正确，但 mandatory 字段与不可分修正项的填充/超限行为仍未定义，边界验收缺失

**已修好**：v21 将“全字段合计 ≤8MiB”改为“响应总硬顶 48MiB + 对账预算 8MiB（不含 anchorRefresh）”，并把 48MiB/8MiB 登记进 OD-7。20MiB anchor 不再同时触发 8MiB 判定，R16 指出的直接矛盾已消除。

**仍阻塞点 1：全字段硬顶与 anchor 独占额度没有给 mandatory 字段保留空间。** §3.8 写“响应总硬顶 = 48MiB（全字段序列化合计）”，又写 anchorRefresh ≤48MiB 且“总硬顶可被其单独占满”。但响应还有 envelope/window/syncRev/scalar/metadata 等非零字段。若 anchorRefresh 编码后恰好 48MiB，完整响应必然超过 48MiB；若 anchor 为 47MiB，剩余 1MiB 未必足够 mandatory reconciliation 字段。方案没有说明是降低 anchor 有效上限、总硬顶触发 typed oversized、省略哪些可选字段，还是允许 anchor 单独占满时如何返回最小合法响应。

**仍阻塞点 2：单个 changedTurn 超过 8MiB 但低于 48MiB 时不可分页。** `correctionsPending` 只能省略后续修正，不能把一个 turn 拆页。当前 typed oversized 只明确绑定在 anchorRefresh >48MiB；一个 20MiB changedTurn 不能塞进 8MiB 对账预算，也无法靠“不变 anchor 续拉”变小。它应触发 `reconciliation.oversized` / FallbackFull、独占一次较大预算，还是其他 typed 处理，方案未定。

**验收缺口**：R16 闭合标准要求 8MiB、20MiB、47MiB、48MiB、48MiB+1 以及 changedTurns 分页与 anchor 特例并存的边界测试。v21 只有通用“多条旧 turn 膨胀超预算”与“超大 anchor >4MiB”断言，未覆盖这些判定边界，也没有单条 changedTurn 不可分的测试。

**最小修订 / 闭合标准**：定义一个可实现的打包算法和不可分项规则：mandatory envelope/scalar 先预留；anchor 与 changedTurn 各自的不可分上限、触发 typed oversized 的条件、与 8MiB/48MiB 两层预算的关系明确；单条 changedTurn 超过 8MiB 的处置明确。测试按实际序列化字节覆盖 8MiB 边界、20MiB/47MiB/48MiB/48MiB+1 anchor、超大 changedTurn、分页与 mandatory 字段共存。OD-7 数值同步该最终算法。

### F-49 [P1 / 仍阻塞] 四态汇总已修正，但同节组合路径仍保留“append 后不整读 prefix”的 v19 结论

**已修好**：§3.2.3 末尾现在写明：重启默认 = prefix 校验整读 + suffix 重投影；OD-9 启用后才可跳过；全等 = stat；争议 = digest 裁决 + 重建。该汇总本身与 source-ledger 生产链一致。

**残留冲突**：下一项“组合路径”仍写：S-2 落地后，transcript append 后首次 `window_0` 的 IO 上界 = stat + 尾部 4KiB 扫描 + suffix 折叠，“仍不整读约 97MiB transcript”。这正是被 v20/v21 默认档推翻的结论。真实 owning 调用链包含 relay 启动 `Observe`，默认必须整读 prefix。该句与同节四态汇总、§7.1.2 “默认含 prefix 校验整读”和 §7.3 性能行冲突。

§7.1.2 早期“window 强制冷检组合路径 … 断言不整读 transcript”若只指 window handler 的 stat/tail 扫描、不含 source-ledger `Observe`，也必须显式限定测量范围；否则全链测试会把生产必需读量判失败。

**最小修订 / 闭合标准**：将组合路径改为“window handler 冷检本身不整读；完整恢复链默认另有 source prefix digest 整读”，或按实际设计列成两栏。删除所有未限定范围的“S-2 后 append 仍不整读 transcript”活断言。§3.2.3、§7.1.2、§7.3 对默认/OD-9 fast 的读量口径一致。

### F-50：闭合

v21 明确 token 只能绑定该 patch 全部 ops 应用后的最终 turn；若附带新 token，必须对应终态；未附 token 则置 null。§7.2.13 的 mixed-patch 测试覆盖同一 turn 上 `upsertTurns + turnStateOps + partOps`，足以防止“终态配中间态 token”。

### F-51 [P2 / 建议] §7.3 出现两行完全相同的 `Mac restart 首开` 指标

v21 在性能表中重复插入同一 `Mac restart 首开` 行。两行内容当前一致，暂不造成行为歧义，但会让后续只改其中一行时产生指标漂移。建议保留一行，或若原意是区分 default/OD-9 fast，则分别命名并给出各自读量与启用门。

## 覆盖与剩余门

- F-41~F-45、F-47 的设计主链在本轮目标范围内未发现新的回归；F-46/F-48 的方向正确但预算边界未闭合；F-49 汇总修正但组合路径残留冲突；F-50 闭合。
- OD-9 是唯一设计 pending 决定，且已精确限制 growth-trust；S-2 可按默认安全档设计，但不能按残留“零整读”组合路径实施。
- E-3 仍是 S-7 开工前证据门；S-7/S-8 冻结批次、E-4/E-5/E-6/E-7/E-10/E-11 和 owner 真机矩阵仍是实施/完成门。
- 本轮只新增本报告；未修改方案或业务代码，未运行 build/test/UI automation，未安装、重启或部署。
