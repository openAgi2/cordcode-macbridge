# Grok Build Leader 模式开关设计：第六轮评审报告

- 日期：2026-08-29
- 对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v6（实测 1227 行）
- 范围：按第五轮约定，仅复核 R5 四项必改；R1–R4 已闭合项不重审
- 方式：只读 pin 源码与脱敏配置结构核查；未修改设计稿，未构建、未运行测试

## 1. 来源核对结果

评审前与写报告前各复核一次，结果一致：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅以下六个预期未跟踪文档：
  docs/2026-08-28-grokbuild-leader-mode-design.md
  docs/2026-08-28-grokbuild-leader-mode-design-review.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r2.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r3.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r4.md
  docs/2026-08-29-grokbuild-leader-mode-design-review-r5.md
任务预期分支=main
配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
预期产品特性=既有 grokbuild leader 观察链路的 Mac 配置开关；保留 file tailer；不实现 follower 写方向
```

两仓 HEAD 精确匹配，无来源漂移。评审继续遵守 §0.4：没有要求补 API 客户端、codec、事件泵、协议翻译或 follower 写方向。

## 2. R5 四项复核摘要

| R5 项 | v6 结论 | 核查证据 |
| --- | --- | --- |
| B1 D-G2 主动取消误写 durable abort | 部分闭合 | §3.5.2:634-647 已正确区分 self-cancel 与 source disconnect，G7/验收 12 已覆盖 Projection/offline 负断言；但只清 `relayRunning` 会遗留独立 `sessionRegistry=running`，见 R6-B1 |
| M1 grace 精确上界 | 未闭合 | §3.5.2:621-628 同时写“最后正样本”与 `[60s,70s)`；两者数学上不相容，见 R6-M1 |
| M2 Phase 0 日志与 requirements 身份 | 已闭合 | §0.2:110-140 已用 `LOG=$(mktemp)` 保存并登记路径；real-home 新建文件删除前核对 inode/device + 内容，身份变化停线 |
| M3 备份 crash-safe 轮转 | 已闭合 | §3.3:519-533 已先收敛旧集合到 ≤2，再创建新备份；T10/T26 覆盖初始三份、收敛失败、创建失败和创建后中断；任一阶段 ≤3 |

真实 TOML 样本本轮重新脱敏核验：`~/.grok/config.toml` 为 regular file、0644、558B、30 LF/0 CRLF，`[cli]` 仅 `installer` / `auto_update` / `channel`，SHA-256 仍为 `c78c74d2b63b801c6f591c291a5df2425fc83d7edd7c5d82f928b29b4d8ea464`。该结构为简单顶层节/键提取，无嵌套计数；未读取或记录配置值。

## 3. 分级发现

### B（阻断）

#### R6-B1：D-G2 虽不再伪造 durable abort，但会把独立 session registry 永久留在 running

**核查结论：设计仍未闭合。**

§3.5.2:640-645 规定主动取消只做 `relayRunning` 清理 + INFO，不写任何终态，并称重开冷拉可恢复真值。现有 relay 在首内容事件处调用 `h.sessions.markRunning`（`handlers_relay.go:266-280`）；正常终态在 `:290-305` 调 `markIdle`，source 断开 defer 在 `:225-238` 也先 `markIdle` 再发布 abort。v6 主动取消分支跳过后两者，就没有任何路径把 registry 从 running 收回。

这不是 Projection Kernel 的同一状态。`sessionRegistry` 独立定义于 `types.go:243-377`；Grok catalog 经过 `buildGrokEnrichedSessions`（`handlers_grok_catalog.go:75-81`）调用 `enrichSessionStatesForList`。`grokbuild.Agent` 不实现 `RunningSessionLister`，因此 `getRunningMap` 返回 nil，`applyListRuntimeState` 会直接采用 registry 的 last-known running（`handlers_opencode.go:238-297`）。冷拉/Projection hydrate 不调用 `sessions.markIdle`，所以“重开冷拉恢复真值”不能修复这个状态。结果是主动取消后，侧栏运行徽标可能无限期保持 running，即使 observer 已退出、leader/turn 已结束。

**必须修改：**明确 D-G2 intentional cancellation 对非 durable registry 状态的收口语义，同时不能用虚假 `turn_aborted` 或未经证明的 source-idle 掩盖未知状态。推荐把“观察已结束、source 状态未知”表示为 unknown/移除 passive synthetic registry 状态；若现有 `sessionRegistry` 接口无法安全表达，应如实扩展授权 diff，而不是在 `handlers_relay.go` 内直接删除可能承载真实 `AgentSession` 的记录。G7 必须增加 registry/catalog 断言：主动取消后不得 sticky running，也不得产生 durable aborted；重开后真实 running/idle 能重新建立。§7.3 第 12 行应同时检查侧栏状态不会永久卡在运行中。

### M（必改）

#### R6-M1：“最后正样本 ≥60s”不会得到从订阅消失起算的 `[60s,70s)`

§3.5.2:621-628 定义每 10 秒采样、记录“最后一次见到订阅者”的时间，并在 tick 上以 `now-lastPositive >= 60s` 取消，却把从订阅者消失到取消写为 `[60s,70s)`；G5 也沿用该结论。

设最后一次正样本 tick 为 `L`，订阅实际在下一采样周期内的 `D∈(L,L+10]` 消失。按 `>=60s`，取消发生在 `L+60` 的 tick，因此从实际消失到取消约为 `L+60-D∈[50s,60s)`，不是 `[60s,70s)`。只有把**首次负样本**记为 `N`，再在 `now-N >= 60s` 时取消，实际消失到取消才是约 `[60s,70s)`。

应二选一并统一正文/G5/验收：

1. 若产品确实要“至少 60 秒 grace”，记录首次连续负样本时间，恢复正样本就清零；以该时间 +60 秒判定，窗口写 `[60s,70s)`；或
2. 保留 last-positive 算法，把实际窗口改为约 `[50s,60s]`（另计调度抖动），并让假时钟测试按“实际消失时刻”而非 last-positive 时刻断言。

当前 G5 的“59s 不取消”若从 last-positive 起算，只能证明阈值实现，不能证明文档声称的用户关闭后 grace。

### S（建议）

#### R6-S1：Phase 0 门仍有一处旧版本口径

§0.2:92-93 仍写“v5 落实四轮必改后即可执行 Phase 0”，但第五轮随后发现了阻断项，v6 头部又要求本轮复核通过后才能进入 Phase 0。建议改为“v6 经第六轮定向复核通过后可执行”，避免实施者跳过当前门。

## 4. 五个维度结论

### 事实核查

v6 对 durable `turn_aborted` 的发布/投影链归因正确，Phase 0 文件保护和备份顺序也与目标相符。新增事实缺口是把 Projection 冷拉当成能够恢复独立 `sessionRegistry`；源码证明两者没有该联动。计时区间则是算法推导错误，不依赖运行测试即可裁决。

### 设计闭合性

R5-M2/M3 已闭合。R5-B1 只解决了 durable false terminal，尚未解决主动取消留下的非 durable sticky running；R5-M1 的时间口径仍未与算法一致。因此 v6 还不能进入 Phase 0/实施。

### Go 改动独立验证

self-cancel/source-disconnect 分流方向正确，真 source 断开仍保留 F-7，file tailer 也未被绕过。但 D-G2 退出需同时处理 relayRunning、订阅连接和 registry 三类状态。若 unknown 语义要求改 `sessionRegistry`，必须扩展 §0.3/§3.5 的授权文件范围并独立测试，不能为守住“只改 handlers_relay.go”而留下假运行态。

### 纪律一致性

日志路径登记、requirements 身份保护和 secret 备份三份硬上限符合 proof-carrying、可逆与最小留存纪律。sticky running 与错误时间承诺违反 fail-visible/可观察语义；它们不是可用帮助文案消解的边界。

### 内部一致性

§17 对 R5 的采纳记录与主要章节大体同步，T10/T26 和 requirements 分支一致。剩余矛盾是“只清 relayRunning”与“重开恢复全部真值”、last-positive 算法与 `[60s,70s)`、以及 §0.2 的 v5 Phase 0 旧门槛。

## 5. 总结论

**修改后通过。**

通过所需必改项：

1. 为 D-G2 intentional cancellation 定义并测试 sessionRegistry/catalog 的 unknown/收口语义：不得 sticky running、不得 durable aborted、不得伪造 source idle；必要时扩展授权 diff；
2. 让计时锚点与区间一致：首次负样本对应 `[60s,70s)`，或保留 last-positive 并改写实际窗口及 G5。

建议同步修正 §0.2 的 Phase 0 版本门槛。完成后只需定向复核以上两项；已闭合的日志/requirements 与备份顺序无需再次重审。
