# Grok Build Leader 模式开关设计：第四轮评审报告

- 日期：2026-08-28
- 对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v4（实测 1060 行）
- 方式：只读源码、上游 package 与脱敏真实样本核查；未修改设计稿，未构建、未运行测试

## 1. 来源核对结果

评审前与写报告前各复核一次，结果一致：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅以下四个预期未跟踪文档：
  docs/2026-08-28-grokbuild-leader-mode-design.md
  docs/2026-08-28-grokbuild-leader-mode-design-review.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r2.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r3.md
任务预期分支=main
配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
预期产品特性=既有 grokbuild leader 观察链路的 Mac 配置开关；保留 file tailer；不实现 follower 写方向
```

两仓 HEAD 精确匹配，无来源漂移。评审继续按 §0.4 基线执行；D-G1 是既有 source 选路的恢复补丁，不是新 backend adapter。

## 2. R3 十一项处置复核

| R3 项 | v4 结论 | 核查证据 |
| --- | --- | --- |
| B1 TOML 依赖/职责/接线 | 已实质闭合 | `swift-toml` tag 2.0.0 实为 `827506c90475e82d5a7f191f950fb3025cbdc0d6`；`Package.swift:1-40` 为 tools 6.0、macOS 10.15、产品 TOML、内嵌 C++17 target、无 package dependency；MIT 与 toml++ MIT 已核实。§3.3 已拆开 semantic parser 与 locator，§3.6 覆盖 project.yml、target、pbxproj、Package.resolved |
| B2 effective 证据 | 已闭合 | `debug_log.rs:8,81-90,360-425` 证明 `GROK_LOG_FILE` 单文件与默认 DEBUG；`app/mod.rs:701-726` 确实输出最终 `use_leader`、policy、sandbox 字段；inspect 已正确降级为 layer 证据 |
| B3 owner 门/stale | 方向闭合，待本报告签署 | §15 已拆成四项；§3.5 D-G1 方向正确，但建立/live 分界还需修正，见 R4-B1 |
| M1 D4 定级 | 已闭合 | 头部、§3.6、§7.4、Phase 1 一致 |
| M2 备份凭据 | 基本闭合 | 0700/0600、完整凭据披露、创建失败 fail-closed、T25/T26 已补；轮转“无害”表述仍需修正，见 R4-M4 |
| M3 symlink | 已闭合 | 相对路径、8 级、普通文件、身份复核、初始目标回滚、属性边界和 T21-T24 均已明确 |
| M4 `--no-leader` | 已闭合 | §0.2、§2.1、§6、§11 均纳入 |
| M5 样本分级 | 已闭合 | 真实/官方 fixture/synthetic 三级 manifest 清楚；本轮真实样本 hash 再次一致 |
| M6 行号 | 已闭合 | `grokbuild.go:174-198` 已修正 |
| S1/S2 元数据与 owner gate | 已闭合 | 不写死行数；四项可独立签署 |

上游依赖核查以 [mattt/swift-toml 2.0.0](https://github.com/mattt/swift-toml/tree/2.0.0) 的 tag 原文为准；TOMLKit 只是备选，没有参与当前设计证明。

## 3. 分级发现

### B（阻断）

#### R4-B1：D-G1 在限定 diff 内无法按正文区分“握手建立失败”和“live 后断开”

**核查结论：不闭合。**

§3.5:533-543 把回退范围定义为 dial/register/initialize/session-load 完成前，并承诺不改 `leader_subscriber.go`；但当前 `grokbuild.go:189-195` 只能拿到 `LeaderSubscriber.Run` 的最终 error。真正的 live 时点只存在于 `leader_subscriber.go:190-199` 内部，既没有 callback/status channel，也没有返回带 phase 的 error。`grokbuild.go` 因而无法知道 error 发生在 session/load 前还是后。

G3 又把分界写成“已收到首事件”，这与正文的“session/load 完成”不是同一条件：订阅可能已经 live、尚无任何 session update 就断开。

**必须修改：二选一并统一正文/G1-G3/diff 范围。**

1. 推荐最小方案：把 D-G1 分界定义为“尚未向下游转发任何 leader event”；在 `grokbuild.go` 的 forward wrapper 记录 first-event。无事件即断开时回退 tailer，有事件后断开则保持 F-7。该方案可维持仅改 `grokbuild.go`；或
2. 若必须以 `session/load` 为真实分界，则让 `LeaderSubscriber` 暴露 ready callback/channel，并把 `leader_subscriber.go` 及其测试纳入授权 diff。

在此修正前，D-1 可以批准“实施 D-G1 的产品方向”，但不能按现有 §3.5 直接开工。

#### R4-B2：Owner D-2 不应接受现状；它不仅续命 leader，还会按会话无界积累 observer

**核查结论：源码已核实，owner 裁决为 reject current behavior。**

`startGrokLeaderSessionRelay` 以 `grok-leader:<sessionID>` 为 key（`handlers_relay.go:154-185`）；每个打开过的 session 都可能建立独立 relay。订阅使用 `context.Background()`（`:244`），只有 leader/channel 自身结束才从 `relayRunning` 删除（`:225-243`），不随该 session 的 iOS subscriber 消失。故 §15 D-2 的真实代价不只是“leader 活到 Link 退出”，还包括长寿命 Link 进程中已打开 session 的连接/goroutine/subscription 按 session 累积。

**Owner decision：不同意接受现状。** 替代为 Go 侧 subscriber-aware cancellation：参照 `codexSessionHasSubscriber`（`handlers_relay.go:365-374`），在 session 已无客户端订阅时取消对应 grok observer；允许短 idle grace 以免快速切页抖动，但必须有确定上限。该需求与 D-G1 可同属 D3，但独立测试、独立行为判据，不得借 D-G1 顺手混写。

### M（必改）

#### R4-M1：Phase 0 requirements 负例可能覆盖并删除用户已有策略文件

§0.2:110-114 直接要求“临时创建 `$GROK_HOME/requirements.toml`，随后删除”。若文件已存在，这会破坏用户或组织策略。应先 `lstat`：存在则不得覆盖；优先用独立临时 `GROK_HOME` 完成层负例，或做逐字节备份、身份比较后原子恢复。恢复失败必须停线并保留现场。固定 `/tmp/grok-p0.log` 也应改成每个子例独立 `mktemp` 路径。

#### R4-M2：`GROK_LOG_FILE` 是 append，不隔离日志会误读旧的 effective 行

`xai-grok-telemetry/src/appender.rs:13-25` 明确以 append 模式打开日志。Phase 0 正例、requirements 负例、confinement 负例若复用 `/tmp/grok-p0.log`，会同时存在多条 `pager TUI leader mode resolved`。应每次使用唯一空文件，或记录 PID/启动时间并只取该进程最新行；验收判据需包含 PID/时间窗，不能对整文件做首次匹配。

#### R4-M3：canonical locator 宣称跟踪多行结构，却没有对应测试

§3.3-3:414-416 明确 locator 跟踪多行 basic/literal string 与多行 array，这是避免把字符串内 `[cli]` / `use_leader` 误当语法的安全边界；T1-T26 没有该类用例。应增加 synthetic 测试：三引号 basic/literal string 内伪节头/伪赋值、数组跨行且字符串含 `]`/`#`、注释中的伪 token、未闭合结构 → F2/F1。这里 synthetic 正合适，因为验证的是仓内 locator 的保守行为，不宣称现场普遍性。

#### R4-M4：“轮转失败保留多余备份无害”与凭据披露、三份留存承诺冲突

§3.3-10:475-483 已承认备份可能含 token，却把轮转删除失败称为“无害”；§5 又称“仅本地留存 3 份”。多余 secret 副本不是无害，且会打破数量承诺。建议在写前先完成轮转；失败则删除本轮新备份并 fail-closed，或至少改成“best-effort 最多 3 份，轮转失败会增加凭据留存面”的高可见警告。T26 应断言选定语义。

### S（建议）

#### R4-S1：dependency 审计记录应固定 source identity，而非只写结论

§3.6 已足够实施。建议在完成报告中保存 tag commit、`Package.swift` hash、主 LICENSE 与内嵌 `toml.hpp` SPDX/版本（v3.4.0），这样未来升级不必重新猜当前基线。

## 4. §15 Owner 签署结论

以下是本轮可直接回填的 owner decision：

| 项 | 决定 | 理由/替代 |
| --- | --- | --- |
| **D-1 stale socket** | **同意 D-G1** | 接受 Go 侧建立期失败回退 tailer；先按 R4-B1 明确分界。不得清 socket、不热切、不周期重试 |
| **D-2 observer 常驻** | **不同意接受现状** | 立案并纳入本设计的 Go subscriber-aware cancellation；无订阅后在有界 grace 内取消对应 session observer，避免无界累积与 leader 被 Link 长期续命 |
| **D-3 interaction 等待** | **同意 + 帮助文案** | 保持只读边界；明确 pending permission/question/plan 时不要关闭唯一可应答客户端。follower 应答仍另案 |
| **D-4 best-effort config 并发** | **同意** | 内容身份比较 + 0600 备份 + 受限回滚足够；保留残余 TOCTOU 的诚实披露，不引入不互认的锁 |

签署日期：2026-08-28。D-2 的 reject 会按 §15 规则增加 Go 需求并重新评估实施拆分；这不是要求 follower 可写。

## 5. TOML 样本核查

| 内容形态 | 本轮结果 | 评级 |
| --- | --- | --- |
| 真实安装 config | regular file、0644、558B、30 LF/0 CRLF；`[cli]` 仅 installer/auto_update/channel；SHA-256 `c78c74d2b63b801c6f591c291a5df2425fc83d7edd7c5d82f928b29b4d8ea464` | 🟢 已核实 |
| 官方 true/false/absent | `mcp.rs:2208-2293` | 🟢 官方 fixture |
| dotted/quoted/inline | synthetic；semantic parser 负责有无值、locator 不确定即 F2 | 🟢 安全策略闭合，不冒充现场样本 |
| CRLF/注释/symlink | 明确标为 synthetic | 🟢 分级诚实 |
| multiline string/array locator | 文档声称支持但无测试样本 | 🟡 补 R4-M3 |

本轮没有嵌套记录计数或跨类型去重，不需要两套统计脚本；真实样本只输出结构和 key 名，未输出配置值。

## 6. 五个维度结论

### 事实核查

R3 的 package、effective 日志、requirements 自由 TOML、凭据、symlink 与行号修正均与 pin/上游源码吻合。新增事实缺口是 D-G1 的 phase 信号不存在于 `grokbuild.go`；另外 `GROK_LOG_FILE` 是 append，必须收窄验收时间窗。

### 设计闭合性

semantic parser + conservative locator + 四格矩阵已经比 v3 闭合，SPM/XcodeGen 接线可实施。剩余必改是 D-G1 分界、Phase 0 文件保护/日志隔离、locator multiline 测试和备份轮转隐私语义。

### Go 改动独立验证

健康 leader 主链仍不需要重写；批准 D-G1 是小范围、合理的恢复改动。D-2 经独立核查不宜继续接受：per-session background observer 会无界累积，应增加 subscriber-aware cancellation。两项都不改变 codec、relay protocol、catalog、history、capability 或 follower 写方向，file tailer继续保留。

### 纪律一致性

§0.4 对“不适用项”的声明正确；source-first、fail-visible、可逆、D4 依赖门与样本分级已落实。Phase 0 直接创建/删除 requirements 文件违反保护用户数据纪律，备份轮转“无害”也与 secret 最小留存不一致，需修正。

### 内部一致性

§4.9、§6、§7.3 对 stale/F-7/D-G1 基本一致；主要内部矛盾是 §3.5 以 session/load 为 live 分界而 G3 以首事件为分界，以及“最多三份”与“轮转失败继续且无害”并存。

## 7. 总结论

**修改后通过。**

通过所需必改项：

1. 按 R4-B1 统一 D-G1 的 establishment/live 分界与授权 diff；
2. 按本报告签署 §15：D-1 accept D-G1、D-2 reject 并加入 subscriber-aware cancellation、D-3 accept、D-4 accept；同步重定级和测试；
3. Phase 0 保护既有 requirements 文件并为每个日志负例使用唯一文件/时间窗；
4. 增加 multiline locator 测试；
5. 修正备份轮转失败的 secret 留存语义。

完成上述文档修订后无需再次重审已闭合的 R1-R3 事实项；下一轮只需检查这五项及 §15 回填。
