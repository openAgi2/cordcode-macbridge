# Grok Build Leader 模式开关设计：第五轮评审报告

- 日期：2026-08-29
- 对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v5（实测 1176 行）
- 范围：按第四轮约定，仅复核 R4 五项必改与 §15 Owner 回填；不重审 R1–R3 已闭合项
- 方式：只读 pin 源码与脱敏配置结构核查；未修改设计稿，未构建、未运行测试

## 1. 来源核对结果

评审前与写报告前各复核一次，结果一致：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅以下五个预期未跟踪文档：
  docs/2026-08-28-grokbuild-leader-mode-design.md
  docs/2026-08-28-grokbuild-leader-mode-design-review.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r2.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r3.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r4.md
任务预期分支=main
配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
预期产品特性=既有 grokbuild leader 观察链路的 Mac 配置开关；保留 file tailer；不实现 follower 写方向
```

两仓 HEAD 精确匹配，无来源漂移。评审按 §0.4 的“既有观察链路加开关”定位继续；没有要求补 API 客户端、事件泵、协议翻译或 follower 写方向。

## 2. R4 五项与 §15 复核摘要

| R4 项 | v5 结论 | 核查证据 |
| --- | --- | --- |
| B1 D-G1 分界 | 已闭合 | §3.5.1:570-588 统一为“是否向下游转发过事件”，回退三条件含非 ctx 取消；`grokbuild.go:163-172,189-195` 确实允许只在该文件包装 `forward`；G1–G4 一致 |
| B2 D-G2 subscriber-aware cancellation | 方向正确，但退出语义未闭合 | `handlers_relay.go:225-243` 的 defer 不区分主动取消与 leader 真断开；§3.5.2:619-622 把其 `turn_aborted` 称为无害，与事件发布/投影源码冲突，见 R5-B1 |
| M1 requirements 文件保护 | 基本闭合 | §0.2:118-130 已首选临时 `GROK_HOME`，真实 home 已有 lstat/备份/身份比较/原子恢复；但新文件删除分支仍需身份钉扎，见 R5-M2 |
| M2 独立日志 | 规则已写入，命令未闭合 | append 事实、独立文件原则和 Phase 0 留存要求均已写；示例把 `mktemp` 直接塞给 env，未保存路径，无法执行后续断言与留档，见 R5-M2 |
| M3 locator 多行测试 | 已闭合 | §3.3:458-460、manifest `:502`、T27–T30 `:866-869` 覆盖 basic/literal 多行字符串、数组、注释诱饵和未闭合结构，且明确 synthetic 分级 |
| M4 备份轮转 | 部分闭合 | 已改成写原文件前轮转、失败 fail-closed；但“先创建第 4 份再轮转”仍有 crash 后永久留下 4 份的窗口，和硬不变量矛盾，见 R5-M3 |
| S1 依赖 source identity | 已闭合 | §3.6 已要求完成报告留 tag commit、`Package.swift` hash、LICENSE、内嵌 toml.hpp SPDX/版本 |
| §15 回填 | 已闭合 | §15:1144-1157 已逐项签署 D-1 accept D-G1、D-2 reject 现状并采用 D-G2、D-3 accept + 文案、D-4 accept；签署效力及实施落点齐全 |

真实 TOML 样本本轮重新脱敏核验：`~/.grok/config.toml` 为 regular file、0644、558B、30 LF/0 CRLF，`[cli]` 仅 `installer` / `auto_update` / `channel`，SHA-256 仍为 `c78c74d2b63b801c6f591c291a5df2425fc83d7edd7c5d82f928b29b4d8ea464`。未读取或记录配置值。

## 3. 分级发现

### B（阻断）

#### R5-B1：D-G2 主动取消会被现有 defer 误判为 leader 崩溃，并写入虚假的 durable `turn_aborted`

**核查结论：不符；会让实施走偏。**

§3.5.2:619-622 说 D-G2 在 turn armed 时取消 observer，现有 defer 向“空订阅者集合”广播 `turn_aborted` 是无害的。但 `handlers_relay.go:225-237` 的 defer 在 `turnArmed` 时无条件调用 `sendSessionEvent`；`handlers.go:2923-2935` 把该事件交给统一 `PublishLogical`，并以 `IsDurableMilestone` 决定 offline 路由。`event_publisher.go:780-883` 明确 timeline event 在解析目标连接之前先进入 Projection Kernel；`projection_reducer.go:1222-1245` 会把 `turn_aborted` 持久化为 aborted 终态。因此“当前没有订阅者”只意味着没有在线 raw target，**不意味着事件没有副作用**。

D-G2 的退出原因是产品主动停止观察，不是 leader 断开；外部 turn 可能仍在 leader 上继续。按现设计实施会把仍在运行或后来完成的 turn 先写成 `leader_disconnect`，污染 projection/replay/offline 语义，违反 source-first 与“未知不能猜终态”的 F-7 原则。

**必须修改：**在 `handlers_relay.go` 内显式区分 D-G2 intentional cancellation 与 source disconnect。主动因无订阅取消时不得合成 `turn_aborted(leader_disconnect)`；应只做 observer/relayRunning 的生命周期清理，并明确 session registry / projection 在重开冷拉时如何恢复真值。G5–G7 至少增加“armed turn 被 D-G2 取消”用例，断言没有 `turn_aborted`、没有 durable/offline 虚假终态；§7.3 第 12 行也应观察该负断言。真正 source channel 断开仍保留现有 F-7 行为。

### M（必改）

#### R5-M1：10 秒轮询与“≤60 秒 grace”没有给出能同时成立的计时定义

§3.5.2:607-613 规定每 10 秒查询一次、记录“最后一次见到订阅者”的时间，并在无订阅超过 60 秒后取消；§4.3、§4.9、§6-4 又承诺 `≤60s grace`，验收第 12 行只等待 `>60s`。若实现按“首次负样本开始计时”，实际从用户关闭到取消可接近 70 秒；若按“最后一次正样本 + `>` 60 秒”在 ticker 上判定，也可能到下一 tick 才取消。当前文字不足以冻结实现和验收。

应二选一：用 last-positive deadline/timer 并在 deadline 再查一次 subscriber，从而把实际最大值定义清楚；或诚实把窗口写成“60 秒 grace + 最多一个 10 秒采样周期（<70 秒）”，同步 §4/§6/G5/验收第 12 行。G5 应覆盖边界 tick，而不只写“注入短常量”。

#### R5-M2：Phase 0 负例命令没有保留日志身份，real-home 新文件分支也未明确身份比较后删除

§0.2:123 使用 `GROK_LOG_FILE=$(mktemp) grok`，但没有把路径保存到变量；紧接着 `:124` 要求核对日志，§8:930-932 又要求留存每个独立日志文件。该命令执行后操作者无法从 shell 状态可靠取回文件名，证明链不可执行。应先保存并打印/登记路径，例如 `LOG=$(mktemp)`，再以 `GROK_LOG_FILE="$LOG"` 启动并从同一路径取证；正例、requirements、confinement 三个子例都应如此。

此外 §0.2:128-130 对“原来不存在”的 real-home requirements 分支写成“测试结束即删除”。并发期间其他进程可能替换该新文件；必须像已有文件分支一样，在删除前比较 inode/device 或逐字节身份，只删除本轮创建且身份未变的文件，否则停线保留现场。这样才与随后“任何情况下不得无条件删除”一致。

#### R5-M3：“最多 3 份”仍不是 crash-safe 硬不变量

§3.3-10:513-515 的顺序是“创建本轮新备份 → 轮转到 3 份”。当已有 3 份时，创建动作先产生第 4 份；若进程在轮转前崩溃/被 kill，清理分支不会运行，第 4 份会永久保留。§5:769-770 和 T26 又把 `≤3` 声明为硬不变量，二者不一致。

若坚持硬不变量，应在复制新 secret 之前先把旧集合收敛到至多 2 份，轮转失败就 fail-closed，之后才创建本轮 0600 备份；并让 T10/T26 覆盖“初始已有 3 份”及中断点/失败点。若不做 crash-safe 顺序，则必须降级为 best-effort 上限并披露启动清理策略，不能继续称硬不变量。

### S（建议）

本轮无新增建议项。上述三项均影响可执行性或已承诺安全语义，不能降为文案建议。

## 4. 五个维度结论

### 事实核查

D-G1 的首事件锚点、ctx 互锁、`HasSessionSubscriber` 先例、TOML 多行测试与 §15 决策均与 pin 源码/文档一致。新增不符集中在 D-G2：现有 defer 的 `turn_aborted` 并非只广播给在线客户端，而会先进入 authoritative projection；“空集合所以无害”是事实错误。

### 设计闭合性

五项中 D-G1、locator、依赖留档和 owner gate 已闭合。D-G2 缺退出原因分流及精确 grace 时钟；Phase 0 日志路径没有形成可保存证据；备份顺序不能满足其自称的 crash-safe 三份硬上限。因此 v5 仍不能直接进入 Phase 0/实施。

### Go 改动独立验证

D-G1 可以按当前设计实施，且 file tailer 保留。D-G2 仍可严格限制在 `handlers_relay.go`，无需修改 codec、protocol、catalog、history 或 follower 写方向；但必须在同一文件把 intentional cancellation 与 source disconnect 分流。否则小范围 diff 也会产生错误的 durable turn 语义。

### 纪律一致性

§0.4 对 wire 样本冻结、新旧 backend 并存等不适用项声明正确；source-first、fail-visible、可逆和受控 Go diff 纪律总体落实。虚假 abort 违反“不能把未知猜成终态”，未保存的 mktemp 路径违反 proof-carrying 验收，先制造第 4 份 secret 再轮转违反最小留存承诺。

### 内部一致性

§15、§3.5、§7.4、§8 对 D-G1/D-G2 的授权与定级一致；T27–T30 引用一致。剩余矛盾是 §3.5.2 的“主动取消”与 §4.9/F-7 的“source 断开”共用终态、`≤60s` 与 10 秒采样未对齐，以及 §3.3 的创建顺序与 §5/T26 的 `≤3` 硬不变量冲突。

## 5. 总结论

**修改后通过。**

通过所需必改项：

1. D-G2 主动无订阅取消不得合成或持久化 `turn_aborted(leader_disconnect)`；补 armed-turn 负断言与重开真值恢复测试；
2. 明确并测试 grace 的精确上界，或把文档/验收统一为 60 秒加一个采样周期；
3. Phase 0 保存每个 `mktemp` 日志路径，并给 real-home 新 requirements 文件增加删除前身份比较；
4. 把备份轮转改为创建新副本前先收敛旧集合，或撤销“最多 3 份硬不变量”的表述并披露真实边界。

上述修订完成后，只需定向复核这四项；R1–R4 已闭合项无需重审。
