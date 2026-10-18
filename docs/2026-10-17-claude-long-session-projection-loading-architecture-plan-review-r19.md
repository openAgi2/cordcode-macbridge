# Claude 长 session 打开方案复审 R19

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v23**
- 全文 SHA-256：`ad1dc6a670d66b2fddd18dda6a17665cc5d3e6b7386d25e511c6e1b7af9a6769`
- 正文 SHA-256：`825db3e36e90ecb98b77f6222e45827920223543c1bd7c6ad2bb16a54e3933b8`，按交接块切分规则实测一致
- scope: R18 F-48 闭合复核 + v23 确定性打包、修正续页、同 cut 绑定与 held/采用事务的全局一致性检查
- blockers: **3**（F-48 仍未完全闭合；新增 F-52/F-53）；advisories: **0**
- implementation_readiness: R18 的 47MiB 边界二义、续页重复 anchorRefresh 和大 anchor + 大 changedTurn 的简单组合已闭合。但 S-7 仍不能冻结：后继 turns 的分页路径、同 cut 数据来源和超限恢复路径还需设计层修订。OD-9 pending 仍不阻塞 S-2 默认档。

## 来源身份

### G1：源码分析前

按方案 §2.1 与两仓 worktree 枚举选择 native-message-timeline 配对：

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | 见下；方案指定来源；配套 I 的下述路径、分支、完整提交 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `bb2f72a3a193c7d2bd87f83196ad43d86845766c` | 见下；方案指定来源；配套 M 的上述路径、分支、完整提交 |
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
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r17.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r18.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

I 当前为另一条 native-timeline 开发线的四个脏文件：

```text
 M OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/LiveTailFollowController.swift
 M OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift
 M OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/TimelineStreamingMarkdownCell.swift
 M OpenCodeiOS/OpenCodeiOSTests/NativeTimelineSmoothHeightTests.swift
```

本轮依赖的 `ProjectionReplica.swift`、`ProjectionStore.swift`、`SessionProjectionWindow.swift`、`BridgeWebSocketConnection.swift`、`RelayBridgeFrameConnection.swift` 在 I 从 R18 的 `847b9b32ff5fb2c259e9bbba6725a5473e5fc56a` 到本轮 `bb2f72a3a193c7d2bd87f83196ad43d86845766c` 的路径限定 diff 为空，且不在脏清单内。U 干净。未构建、安装、重启、写入真实会话或部署，不触发构建/部署 G3。

### G2：首次写报告前

M 仅比 G1 多本报告；v23 全文哈希不变，`git diff --check` 通过。I 分支/提交与脏清单未变。U 干净。R18 报告已完整重读，其结论未作为本轮判断来源。

## 亲核锚点

| 方案命题 / 原引用 | 实际来源与摘录 | 判定 |
| --- | --- | --- |
| §3.8 不变量 6：Mac 对客户端进度无状态 | 方案 `:428`：Mac 不存 per-device offset/记忆，禁止将来添加 per-device 状态 | 定位准确；同 cut 续页必须由此推导实现方式 |
| §3.2.2 materialization schema | 方案 `:469-477`：`head`、`turns/<ordinal+turn_id>`、`items/<turn_id+ordinal+item_id>` 均为当前行；无按 `cutRev` 的历史版本 bucket | 现拟议存储不能直接服务历史 cut |
| §3.8 修正续页 | 方案 `:780-783`：首包空间可为 0；续页不携带 anchorRefresh/turns；续页与首包同 kernel snapshot cut；请求只带 anchor + cursor | 支持本轮 F-48/F-52 判定 |
| M 当前 snapshot/fence 机制 | `projection_kernel.go:1887-1902` `Snapshot` 返回当前 reducer snapshot；`projection_snapshot_fence.go:112` 取当前 `kernel.Snapshot`；`projection_window_handler.go:168-210` 同请求内 Begin→slice→Complete | 现有机制没有跨请求历史 cut 取数；fence 完成后即释放 |
| I 50MiB 硬顶 | `BridgeWebSocketConnection.swift:12` `maximumMessageBytes = 50 * 1024 * 1024`；`RelayBridgeFrameConnection.swift:823/:859-862` 解压输出同样受 50MiB 限制 | gzip 不消除 full projection 的 50MiB 逻辑输出上限 |

## R18 意见闭合判定与新意见

### 已闭合部分

v23 正确消除了 R18 点名的三个直接问题：

- 47MiB anchor 不再被写成合法；46MiB 恰满、46MiB+1 oversized 的规则与验收一致。
- 修正续页不再重复 anchorRefresh，`46+9`、`20+46`、`46+46` 在只考虑 anchor 与 changedTurns 时均有 ≤48MiB 的页序列。
- held 注册、全部续页到齐后一次提交、typed stale 整组 FallbackFull 的语义已登记。

F-49/F-50/F-51 维持闭合。

### F-48 [P1 / 仍阻塞] 大 anchor 把首包对账空间压为 0 后，后继 turns 没有任何交付路径

**位置**：§3.8 `:780-781`、§7.1.5 `:916-918`、§3.4.2 `:617-620`。

v23 写明：46MiB anchor 时“首包对账空间 = 0，turns/changedTurns 全部 correctionsPending”。但下一句又规定修正续页“**不携带 anchorRefresh、不携带 turns**，只含 changedTurns 剩余片”。这使 `46MiB anchor + 至少一个后继 turn` 不可实现：

1. 首包没有空间放 turns；
2. correctionsPending 续页明确不放 turns；
3. anchor 水位线只有在本页 `anchorRefresh ∪ turns` 的最后一个 turn 之后才能前进；turn 未交付则后续 anchor-newer 请求仍以同一个 46MiB anchor 开始，空间永远为 0；
4. “hasNewer 且 ≤2 页 → 串行续拉 append”也无法启动，因为第一页没有交付任何 turn，页面尾仍是原 anchor。

这不是只含 changedTurns 的边界反例；它是水位线前进的正常主路径。R18 组合验收漏掉了“46MiB anchor + 后继 turn（如 1KiB 或 9MiB）”。

**最小修订**：为后继 turns 定义专属出口。可选：

- 首包对账空间不足以容纳下一个严格后继 turn 时，返回 typed oversized/pagination 状态并 FallbackFull；
- 或允许一种不含 anchorRefresh 的 **turns 续页/turn 独占页**，先交付严格后继 turns 并推进水位线，再处理 changedTurns；
- 或在一个更大的对账事务中定义两阶段游标（turn phase → correction phase），明确每页形状、前进规则与 R7 顺序。

**闭合标准**：增加 `46MiB anchor + 1KiB successor turn`、`46MiB anchor + 9MiB successor turn`、`42MiB anchor + 6MiB successor turn` 等验收；断言每一页 ≤48MiB、turn chain 不跳过、水位线最终前进、syncing 只在 turns 与 corrections 全部到齐后清除。

### F-52 [P1 / 新增阻塞] “同 kernel snapshot cut” 续页缺少无状态数据来源，现有 Kernel/fence/materialization 均不能提供

**位置**：§3.8 `:783`；对照不变量 6（`:428`）与 §3.2.2 schema（`:469-477`）。

v23 要求续页与首包同 `BeginProjectionSnapshot` 的 cutRev，iOS 全部到齐后一次提交；若 cut 失效则 typed stale。这作为一致性语义是正确的，但没有说明 Mac 后续请求如何取回该历史 cut：

- 现有 `BeginProjectionSnapshot` 每次调用 `kernel.Snapshot`，而 `ProjectionKernel.Snapshot` 返回当前 reducer snapshot，不是指定 cutRev；
- window fence 在当前响应 `CompleteProjectionSnapshot*` 后释放，不跨 correctionsPending 请求持有；
- 拟议 bbolt `turns/items` key 是当前行 upsert，没有 `(cutRev, turnId)` 历史版本；
- 修正 cursor 只写“anchorTurnId + cursor”，没有定义 cursor 是否携带完整内容/计划、共享 cut artifact ID，或Mac如何在校验后从不可变存储重建同 cut。

如果实现者改为每次续页取当前 head，则破坏“同 cut”和 iOS一次性提交；如果为每个连接保存首包 snapshot，又违反不变量 6 的 per-device 状态禁令。共享、可寻址、不可变的 cut artifact 可以兼容无 per-device 状态，但它的生成、去重、容量、TTL、失效、崩溃恢复和协议 cursor 形状均未设计。

**最小修订 / 闭合标准**：明确同 cut 的拥有机制。可行方向包括共享不可变 cut/snapshot artifact（按 backend/session/cut/digest-set 寻址，非 per-device）、版本化 materialization rows，或完全自包含 cursor。无论选择哪种，都要写清生成、持久化、寻址、容量、失效、并发 head 前进时的 typed stale 判定、崩溃恢复和测试。若选择放宽为当前 head，则必须重新证明无部分采用/覆盖，不能继续宣称同 cut。

### F-53 [P1 / 新增阻塞] 超限项 “typed oversized → FallbackFull” 可能落入同一个 50MiB 硬顶而失败，缺少可执行恢复语义

**位置**：§3.8 `:775-776`、§7.1.5 `:916`、§7.3 `:965`。

方案对 `anchorRefresh >46MiB` 或 `changedTurn >46MiB` 返回 typed `reconciliation.oversized`，然后 **FallbackFull**。但 I 的逻辑消息与 gunzip 输出硬顶均为 50MiB。一个超过 46MiB 的 turn 常常出现在还有其他内容的 session 中；此时 full projection 未压缩 JSON 很容易超过 50MiB。gzip 只减少网络输入，`RelayGzip.decompress` 仍按解压后输出拒绝超过 50MiB 的消息。Direct 侧同样以 `maximumMessageBytes=50MiB` 累计逻辑消息。

因此 FallbackFull 不是普适恢复路径：它可能在触发 oversized 的同一大小事实上失败。方案目前没有说明这是显式终态失败、分片 full、提高/按能力协商更大消息上限，还是启用 contingency window。不能把一个不可传输的 full 响应写成恢复成功路径。

**最小修订 / 闭合标准**：为 `fullSize > transport logical cap` 定义明确行为与验收：显式 typed `full.oversized` 失败、chunked full、按能力协商更高上限，或 bounded fallback。若提高上限，必须同步 S-8/transport/memory 安全预算与 iOS 解压上限测试。增加“46MiB+1 turn + 其他内容使 full >50MiB”的反例，禁止把 transport 解压失败冒充对账回退成功。

## 覆盖与剩余门

- F-41~F-47、F-49~F-51 的既有结论本轮未发现新回归；F-48 的新增问题来自 v23 “首包空间为 0 + 续页不带 turns”的组合。
- E-3 仍是 S-7 开工前证据门；S-7/S-8 冻结批次、E-4/E-5/E-6/E-7/E-10/E-11 和 owner 真机矩阵仍是实施/完成门。
- OD-9 pending 仅限制 growth-trust；S-2 可按默认整读档设计。
- 本轮只新增本报告，不修改方案或业务代码，不运行 build/test/UI automation，不安装、重启或部署。
