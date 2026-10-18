# Claude 长 session 打开方案复审 R18

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v22**
- 全文 SHA-256：`6a85455fc0ed0bd9ce21fa4509f2526b08f01f7cc15c9acdb7b4b2629880d4cd`
- 正文 SHA-256：`0130437424963ee25eb9cc509bea1996a69ae4ea4786b1771a4a18483c0dc4f7`，按交接块切分规则实测一致
- scope: R17 三项闭合复核 + v22 预算边界、重启 IO 口径与性能指标去重的全局回归
- blockers: **1**（F-48 仍有边界/打包矛盾）；advisories: **0**
- implementation_readiness: F-49/F-50/F-51 闭合。OD-9 仍为合法 pending，不阻塞 S-2 默认档。S-7 的对账响应冻结仍被 F-48 阻塞。

## 来源身份

### G1：源码分析前；G2：首次写报告前

按方案 §2.1 与两仓 worktree 枚举选择 native-message-timeline 配对：

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | 见下；方案指定来源；配套 I 的下述路径、分支、完整提交 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `847b9b32ff5fb2c259e9bbba6725a5473e5fc56a` | 干净；方案指定来源；配套 M 的上述路径、分支、完整提交 |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897` | 干净；只读架构参考，不代表目标运行二进制 |

M 的 G1/G2 状态一致，另仅新增本报告：

```text
 M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r14.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r15.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r16.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r17.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

I 已由并发开发推进到 `847b9b32ff5fb2c259e9bbba6725a5473e5fc56a`，本轮干净。相对 R17 分析身份 `c2a59bf318ce154795a5e66f83d207c9780eccfa`，本方案锚点文件 `ProjectionReplica.swift`、`ProjectionStore.swift`、`SessionProjectionWindow.swift`、`BridgeWebSocketConnection.swift`、`RelayBridgeFrameConnection.swift` 路径限定 diff 为空；R17 对 50MiB transport/gunzip 上限与 patch 顺序的源码核验可按命题复用，本轮仍亲核 v22 相关条款。U 干净。未构建、安装、重启、写入真实会话或部署，不触发构建/部署 G3。

已完整重读 R17 报告；其判定未作为本轮结论来源，三项修订均重新对照 v22 正文。

## 亲核锚点

| 方案命题 / 原引用 | 实际来源与摘录 | 判定 |
| --- | --- | --- |
| §3.8：transport 解压上限 50MiB，原引 I `BridgeWebSocketConnection.swift:12`、`RelayBridgeFrameConnection.swift:823/:859-862` | I 当前提交这些文件相对 R17 分析身份路径限定 diff 为空；R17 已核 `maximumMessageBytes = 50 * 1024 * 1024` 与 `relay.decompressed_too_large` | 50MiB 上限事实成立；v22 的 48MiB 总硬顶方向正确 |
| §3.8：anchorRefresh 独占额度 ≤46MiB | 方案 `:763`：`≤46MiB … 超 46MiB → typed reconciliation.oversized` | 规则明确 |
| §7.1.5：47MiB anchor 预期 | 方案 `:898`：`47MiB anchor（总硬顶内合法）` | 与 ≤46MiB 规则直接冲突（F-48） |
| §3.8：changedTurn 修正页 | 方案 `:764-766`：changedTurn never-split、可独占 ≤46MiB 修正页；`correctionsPending` 后以不变 anchor 续拉 | 未说明修正页是否重复 anchorRefresh；大 anchor + 大 changedTurn 组合不可判定（F-48） |
| §3.2.3 / §7.1.2 / §7.3：重启 IO | 方案 `:513` 明确默认 full chain = source prefix digest 整读 + 冷检 + suffix；§7.1.2 将“不整读”限定为 handler 冷检；§7.3 仅一行 Mac restart | F-49/F-51 闭合 |

## R17 意见闭合判定

### F-49：闭合

v22 的组合路径现在明确：S-2 后 append 首次 `window_0` 的完整生产链默认包含 source-ledger prefix 校验整读、window handler 冷检和 suffix 重投影；只有 OD-9 启用后才可跳过 prefix 校验。§7.1.2 也明确“不整读”只限定 handler 冷检，不能豁免 source ledger `Observe`。§3.2.3、§7.1.2 与 §7.3 的默认 IO 口径一致。

### F-50：闭合（维持 R17 判定）

token 绑定 patch 全部 ops 后终态，mixed-patch 测试保留。

### F-51：闭合

`rg '^\| Mac restart 首开'` 实测仅一行，内容为 v20 默认整读口径，无重复指标。

### F-48 [P1 / 仍阻塞] 46MiB 独占额度与边界验收、修正页打包仍矛盾

**已修好**：v22 给 anchorRefresh 收紧到 ≤46MiB，为 envelope/scalar/metadata 留出 ≥2MiB；changedTurn 明确 never-split，可按整 turn 独占修正页，>46MiB typed oversized；并登记了多条边界验收。R17 指出的“48MiB 字段额度等于总硬顶”和“9MiB changedTurn 无法分页”两个主问题方向已解决。

**仍阻塞点 1：47MiB anchor 边界与 ≤46MiB 规则直接冲突。** §3.8/OD-7 明确 anchorRefresh 超 46MiB 即 typed oversized；§7.1.5 却写 `47MiB anchor（总硬顶内合法）`。47MiB 同时满足“>46MiB → oversized”和“总硬顶 48MiB 内”，实现与测试无法判定。该行大概率是把“总硬顶内”误当成“独占额度内”；若确要允许 47MiB，则必须放宽独占上限并证明 mandatory 字段仍 ≤48MiB。

**仍阻塞点 2：修正续页是否重复 anchorRefresh 未定义，导致两个合法字段组合不可实现。** 响应契约写“anchor 在保留区即返回”，状态机又写 `correctionsPending` 后“以不变 anchor 续拉修正页”。如果每个修正页仍重复携带 anchorRefresh，则 20MiB anchor + 46MiB changedTurn、甚至 46MiB anchor + 9MiB changedTurn 都超过 48MiB 总硬顶；`correctionsPending` 不能解决，因为每一页都重复同一大 anchor。若修正页可省略/引用已交付 anchorRefresh，则需明确该例外、其 cut/token 有效性以及首个已交付 anchor 的重放规则。当前方案没有给出可实现打包算法。

**最小修订**：

1. 将边界用例与独占上限统一：46MiB 恰满合法、46MiB+1 oversized；若保留 47MiB 合法，则同步修改额度与预留说明。
2. 定义首包与修正页的响应形状：修正页是否省略 anchorRefresh/turns，只携带 changedTurns、removedTurnIds、scalar/metadata 和 head；若省略，说明如何绑定/校验首个 anchorRefresh 的 cut 与 token。
3. 用一个确定性打包算法表达优先级和硬顶：mandatory 字段预留 → anchorRefresh（首包）→ turns/changedTurns 按 8MiB 普通预算 → 单个超大 changedTurn 独占修正页 ≤46MiB → 全字段序列化 ≤48MiB。
4. 增加组合验收：46MiB anchor + 9MiB changedTurn、20MiB anchor + 46MiB changedTurn、46MiB anchor + 46MiB changedTurn（应通过分页或明确 typed fallback）、修正页不重复 anchor 的 head/token 一致性。

**闭合标准**：每个输入只有一个合法/oversized/分页判定；边界测试与规则完全一致；大 anchor 与大 changedTurn 的组合有可实现响应序列，且所有页的全字段序列化均 ≤48MiB。

## 剩余门

- E-3 仍是 S-7 开工前证据门；S-7/S-8 冻结批次、E-4/E-5/E-6/E-7/E-10/E-11 和 owner 真机矩阵仍是实施/完成门。
- OD-9 pending 仅限制 growth-trust；S-2 可按默认整读档设计。
- 本轮只新增本报告，不修改方案或业务代码，不运行 build/test/UI automation，不安装、重启或部署。
