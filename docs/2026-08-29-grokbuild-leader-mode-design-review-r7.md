# Grok Build Leader 模式开关设计：第七轮评审报告

- 日期：2026-08-29
- 对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v7（实测 1304 行）
- 范围：按第六轮约定，仅复核 R6-B1 unknown 收口与 R6-M1 计时锚点；其余已闭合项不重审
- 方式：只读 pin 源码与脱敏配置结构核查；未修改设计稿，未构建、未运行测试

## 1. 来源核对结果

评审前与写报告前各复核一次，结果一致：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅以下七个预期未跟踪文档：
  docs/2026-08-28-grokbuild-leader-mode-design.md
  docs/2026-08-28-grokbuild-leader-mode-design-review.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r2.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r3.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r4.md
  docs/2026-08-29-grokbuild-leader-mode-design-review-r5.md
  docs/2026-08-29-grokbuild-leader-mode-design-review-r6.md
任务预期分支=main
配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
预期产品特性=既有 grokbuild leader 观察链路的 Mac 配置开关；保留 file tailer；不实现 follower 写方向
```

两仓 HEAD 精确匹配，无来源漂移。评审按 §0.4 的既有观察链路定位执行，没有要求补 API 客户端、codec、事件泵、协议翻译或 follower 写方向。

## 2. 两项定向复核摘要

| R6 项 | v7 结论 | 核查证据 |
| --- | --- | --- |
| B1 registry sticky running | 方向正确，但共享状态所有权与第三态消费者未闭合 | unknown 能让 catalog 不亮徽标；但 `claimedRunning` 不是 CAS 所有权，且现有 `!isIdle`/cleanup 对 unknown 的解释与正文冲突，见 R7-B1 |
| M1 grace 锚点 | 已闭合 | §3.5.2:628-636 改为首次连续负样本 `firstNegativeAt`；转正清零。首负样本相对实际消失延迟 `[0,10s)`，再等待 60s，名义取消窗口正确为 `[60s,70s)`；G5 从实际消失时刻断言并覆盖 59.9s/转正重置 |

真实 TOML 样本本轮重新脱敏核验：`~/.grok/config.toml` 为 regular file、0644、558B、30 LF/0 CRLF，`[cli]` 仅 `installer` / `auto_update` / `channel`，SHA-256 仍为 `c78c74d2b63b801c6f591c291a5df2425fc83d7edd7c5d82f928b29b4d8ea464`。该项只是简单顶层节/键结构核对，未读取或记录配置值。

## 3. 分级发现

### B（阻断）

#### R7-B1：`claimedRunning + markUnknown` 不能证明“只收回本 relay 的 claim”，且 generic unknown 态尚未完成消费者审计

**核查结论：设计仍未闭合。** v7 解决了 catalog 表面上的 sticky badge，但当前方案会引入新的共享状态竞态和生命周期泄漏。

第一，§3.5.2:672-675 只规定本 relay 调用 `markRunning` 后把本地 `claimedRunning` 置 true，主动取消时无条件 `markUnknown`。该 bool 只证明“过去调用过一次 markRunning”，不能证明 registry 当前 running 仍属于这个 relay：

- 文档没有规定在本 relay 收到正常 `turn_completed/error` 并 `markIdle` 后清除 `claimedRunning`。因此会出现“已经正确 idle → grace 到期 → defer 又覆盖成 unknown”；
- 即便补清零，其他路径也可能在 claim 之后更新同一 registry 条目。registry 是共享的，`markRunning/markIdle` 没有 owner/generation；本 relay 的过期 defer 仍可能把较新的 running/idle 覆盖为 unknown。现仓已有 relay generation/CAS 所有权先例（如 `handlers_relay.go:2680-2702`），而单个局部 bool 不具备同等保证。

第二，§3.5.2:669-681 把 `sessionStateUnknown` 加进通用 `sessionRegistry`，却只审计 catalog 与 `onStateChange`。现有 `isIdle` 是 `!ok || state==idle`（`types.go:373-377`），所以 unknown 会返回 false；`handlers_relay.go:449,457,905,2728,2864` 多处用 `!isIdle` 作为“仍 active/running”的判据。v7 同时声称 unknown“非 running、非 idle”，但不修改这些消费者，后续共享 relay 路径可能把 unknown 当 active，合成 completed/aborted 或广播 idle。新增第三态后，所有以 `!isIdle` 代替 `isRunning` 的逻辑都必须重新定型，不能只证明 catalog 能打印字符串。

第三，`cleanupIdleSessions` 仅清理 `state==idle`（`handlers.go:857-875`）。v7 又要求 unknown 条目“从不删除”，所以每个 passive external session 在 observer 回收后都会留下永久 registry 条目；这把 D-G2 要消除的按 session 无界累积从连接/goroutine 转移成了 registry map 累积。外部 relay 通过 `markRunning` 新建的常见条目本来就是 `session=nil` 的 passive synthetic row（`types.go:321-335`），对这类条目做有条件删除不会丢真实 `AgentSession`，而 wire 层无记录本来就输出 unknown。

**必须修改：**

1. 把 passive running claim 做成 registry 级 ownership token/generation，而不是局部 bool。claim 建立返回 token；正常 terminal 清除本 relay 的 token；self-cancel 只能在 token 仍匹配当前 generation 时释放，不能覆盖其他路径的更新。
2. 定义 release 的两类落点：若 token 对应的是本 relay 创建且仍无真实 `AgentSession` 的 synthetic row，可 CAS 删除（catalog 无记录自然为 unknown，并避免积累）；若条目承载真实 session，则仅在 token 未被后续状态更新取代时转 unknown，保留句柄。
3. 增加显式 `isRunning` 语义并审计/替换把 `!isIdle` 当 active 的消费者，或给出等价的第三态安全方案。unknown 不得触发任何 running-only 自动终态。
4. 扩充 G7：正常 terminal 后再 self-cancel 不得把 idle 改 unknown；claim 后发生较新的 running/idle 更新时，过期 cancel 不得覆盖；synthetic row 释放后不积累；real-session row 不删除；unknown 不触发 `!isIdle` 路径的虚假终态。若这些改动需要 `handlers.go`，须同步扩展授权 diff，不能为维持文件白名单留下永久记录。

### M（必改）

本轮无独立 M 项。R6-M1 已闭合；registry 的剩余问题共同构成阻断项，不能拆成文案级修补。

### S（建议）

#### R7-S1：`[60s,70s)` 应注明为 ticker 正常调度下的名义窗口

首次负样本算法的数学口径已经正确。Go scheduler 长时间停顿时 tick 处理可能晚于 70s，生产 wall-clock 并非硬实时上界。建议把“恰为/全程 <70s”写成“正常调度下 `[60s,70s)`；另计进程暂停/调度延迟”，避免验收把系统暂停误判为算法错误。假时钟单测仍可严格断言 `[60s,70s)`。

## 4. 五个维度结论

### 事实核查

首次负样本锚点与区间推导已核实。F-8 的 wire unknown 事实也正确，但“wire 可表达 unknown”不等于把第三态加入共享 registry 就自动安全；源码显示 `isIdle`、relay 自动收口和 idle cleanup 仍建立在二态假设上。

### 设计闭合性

计时项已闭合。unknown 收口仍缺状态所有权、正常 terminal 清 claim、第三态消费者语义和 passive row 回收，因此不能按当前 §3.5.2 直接实施。

### Go 改动独立验证

D-G2 可继续保持为小范围 Go 改动，但正确边界不应由预设文件数决定。实现必须携带 registry claim ownership，并让 unknown 不进入 running-only 分支；必要时把 `handlers.go` 的 cleanup/consumer 调整纳入同一 D3 独立提交。D-G1、file tailer、codec、protocol 与 follower 方向仍不需要改动。

### 纪律一致性

v7 对“不知道就不亮灯”的产品语义选择正确，也没有用 idle/abort 猜 source 状态。但局部 bool 冒充共享 claim 所有权违反并发可逆纪律，永久 unknown row 又与 D-G2 的有界资源目标冲突。

### 内部一致性

§0.2 Phase 0 门、首次负样本正文、G5、§4/§6/§8/§11 已统一。剩余矛盾集中在 §3.5.2：“收回本 relay 自己的 claim”没有 token/CAS；“unknown 非 running”却仍由 `!isIdle` 判为 active；“不再按 session 无界累积”与 unknown 条目永不删除并存。

## 5. 总结论

**修改后通过。**

唯一阻断项：把 D-G2 registry 收口升级为 ownership-aware、第三态安全且可回收的设计，并补齐上述竞态/terminal/consumer/retention 测试。首次负样本计时项已通过，不需再改算法；仅建议补充非硬实时说明。

下一轮只需复核该 registry 阻断项；R6-M1 及此前已闭合项无需重审。
