# Grok Build Leader 模式开关设计第二轮评审报告

- 评审日期：2026-08-28
- 评审对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v2（713 行）
- 对照报告：`docs/2026-08-28-grokbuild-leader-mode-design-review.md`
- 评审方式：只读源码与真实本机配置样本核查；未构建、未运行测试
- 总结论：**退回**

## 1. 来源核对结果

评审开始、首次写入本报告前均重新核对来源，未发生漂移：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=
  ?? docs/2026-08-28-grokbuild-leader-mode-design.md
  ?? docs/2026-08-28-grokbuild-leader-mode-design-review.md
任务预期分支=main

配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净

预期产品特性=在既有 grokbuild leader 观察链路上新增 Mac 配置开关；
不新建 backend、不实现 follower 可写方向、不删除或绕过 file tailer
```

本机安装版本仍为 `grok 1.0.12 (ece2b556c271)`；pin 源码
`xai-grok-version` crate 为 `1.0.10`。

## 2. 首轮 13 项处置复核

| 首轮项 | R2 结论 | 说明 |
| --- | --- | --- |
| B1 配置覆盖模型 | **部分闭合，仍有 B** | 已删除 project config 错误，但把 managed 写成可覆盖 user，且把 confinement veto 合并进 eligibility；均与 pin 源码不符。 |
| B2 observer 续命 | **部分闭合，仍有 B** | 已确认 observer 计入 clients，但仍错误声称“iOS 关闭会话”会断 observer，并把 turn 继续完成写成已裁决事实。 |
| B3 stat 不足证活 | **部分闭合，仍有 B** | UI 措辞已降级；但 stale socket 在下次 session-open 仍会再次被 `stat` 命中，不能恢复，也不与正常 leader 真死同边界。 |
| B4 roster 通知 | **闭合** | 官方广播、Mac 过滤、列表仍轮询三层归因已修正。 |
| M1 TOML 并发/回滚 | **未闭合，升 B** | 内容比较与 rename 之间仍有 TOCTOU，不能支持“绝不覆盖”“达到同等保护”。 |
| M2 TOML 语法/测试 | **部分闭合** | 已扩 T11–T17 并对等价形态 fail closed；但没有指定可靠 TOML 解析机制，也遗漏 symlink 配置文件。 |
| M3 状态机 | **大部闭合，仍有 M** | socket/订阅措辞已分离；absent 与 explicit false 仍被合并，实际 remote fallback 语义不同。 |
| M4 Phase 0 | **部分闭合** | 已扩十步，但路径、observer 断开、stale 恢复和 effective layer 核查仍不可执行或断言错误。 |
| M5 行号漂移 | **闭合** | 首轮指出的行号均已修正。 |
| M6 验收分级/证据链 | **闭合** | 第 4–9 行已统一为生产路径级，三行日志链与 DiagnosticsSheet 必做已落实。 |
| M7 内部一致性 | **部分闭合** | 首轮矛盾多数消除，但 v2 新增了 Phase 0 第 0 步、七/八条规则、F-7 落点等新错链。 |
| S1 启动/重启 | **闭合** | 文案已区分尚未启动与正在运行。 |
| S2 状态术语 | **闭合** | 核心态、扩展观察态、失败态已分组。 |

## 3. 分级发现

### B（阻断）

#### R2-B1. effective config 优先级仍写错，sandbox/confinement 位置也不准确

**核查结论：不符。**

v2 §2.1-2、§6-5 和 §12 仍声称 managed 层可以覆盖 user 层。实际 merge 顺序是：

```text
system_managed → managed → user → env_overlay → user_requirements
→ system_requirements → mdm_requirements
```

后合并者覆盖前者，因此 **user 覆盖 managed/system_managed**；能在 user 之后覆盖 `[cli]`
的是 requirements/MDM 层。env overlay 的 allowlist 又明确排除 `cli`：

- `grok-build/crates/codegen/xai-grok-config/src/config_layers.rs:20-75`
- `grok-build/crates/codegen/xai-grok-config/src/config_layers.rs:177-197`

另一个错位是把 sandbox/confinement veto 写在 eligibility。真实代码先处理
`--no-leader`、`--leader`、eligibility、config、remote/default，随后才用
`requested_confinement` 对已经解析出的 leader 结果做最终 veto：

- `grok-build/crates/codegen/xai-grok-pager/src/app/mod.rs:431-481`

应把两个概念分开：普通 eligibility 位于 flag 与 config 之间；requested confinement 是整条
policy chain 之后的最终 veto。否则会误导 `--leader + sandbox` 的诊断。

受影响位置：设计稿 `:201-220`、`:484-488`、`:692`。

#### R2-B2. observer 的真实生命周期仍写错；“iOS 关闭会话”不会断开它

**核查结论：不符。**

Grok relay 以 `context.Background()` 建立订阅，并且没有按 iOS session subscriber 数量取消
observer 的路径：

- `cordcode-macbridge/go-bridge/handlers_relay.go:158-185`
- `cordcode-macbridge/go-bridge/handlers_relay.go:206-250`

relay 只在 leader/source channel 关闭时退出并删除 `relayRunning`。这与 Codex file relay
显式查询 `HasSessionSubscriber` 的实现形成直接对照：

- `cordcode-macbridge/go-bridge/handlers_relay.go:365-374`

因此 iOS 只是关闭/切换会话不会让 observer 断开；只要 CordCode Link runtime 仍运行，observer
就会继续续命 leader。v2 以下结论错误：

- §2.1-3“直到 iOS 关闭会话 / CordCode Link 退出”；
- §4.3“观察窗口 = iOS 会话打开期间”；
- §4.9、§6-4 和 §7.3 第 8 行把“关闭会话”与“退出 Link”写成等价动作。

若接受零 Go 改动，产品边界必须诚实改成：**会话一旦被打开并成功建立 observer，leader 最长
可被续命到 CordCode Link runtime 退出或 leader 自身崩溃**。如果 owner 不接受该常驻语义，
就必须把订阅取消纳入 Go 改动，而不能留给 Phase 0 文案处理。

同一节把“TUI 关闭后 turn 继续完成”写成源码已裁决事实也过强。官方 interaction request
是广播给所有 subscriber、first-answer-wins；observer 会收到但明确不回答：

- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:491-500`
- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:2197-2277`
- `cordcode-macbridge/agent/grokbuild/leader_subscriber.go:319-334`

无 interaction 的 turn 可能继续完成；出现 permission/question/plan approval 时则可能长期等待。
这应是 Phase 0 待证假设，而不是接受续命的既成收益。文档所写“driver transfer 导致 permission
路由给 observer”也不是精确归因：interaction 本来就是共享广播，driver transfer 主要影响
driver-only 消息。

#### R2-B3. stale socket 不会因“下一次 session-open 重选”而恢复

**核查结论：不符。**

每次订阅都只执行：

1. `os.Stat(socketPath)`；
2. 存在则启动 `LeaderSubscriber`；
3. dial/register/load 失败后 channel 关闭；
4. 不删除 stale socket，也不 fallback。

证据：

- `cordcode-macbridge/agent/grokbuild/grokbuild.go:174-198`
- `cordcode-macbridge/agent/grokbuild/leader_subscriber.go:137-212`

所以 stale 文件仍存在时，下次 session-open 只会再次选择同一个失效 socket，再次失败；不会
自动回到 tailer。只有 socket 被外部清除，或新的 leader 在该路径正常启动，才可能恢复。

这与正常 leader 退出不同：正常收尾会删除 socket，下一次订阅才能 fallback：

- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:2348-2351`

因此 §12 “与 leader 真死同边界”以及 §2.2-1、§6-7 的“观察停止至下次 session-open”都淡化
了实际故障窗口。若坚持零 Go，必须写成“观察持续失败，直到 socket 被官方新 leader 替换或
人工/官方流程清除”，Phase 0 第 8 步也必须验证第二次打开仍失败，不能只验证首个 ended 日志。

#### R2-B4. 内容比较后再 rename 仍有 TOCTOU，不能保证“绝不覆盖”

**核查结论：设计安全保证不成立。**

§3.3-5 的顺序是“重读比较一致 → rename”。第三方仍可在比较完成后、rename 发生前写入文件；
随后 CordCode rename 会覆盖该新内容。内容比较缩小了竞态窗口，但不是原子 compare-and-swap。

官方自身至少有 process-wide `SAVE_LOCK` 串行化同一进程内的 config 读改写；它不能协调
CordCode 这个外部进程，但也说明“官方自身不加锁”的表述不准确：

- `grok-build/crates/codegen/xai-grok-shell/src/util/config/persist.rs:7-13`
- `grok-build/crates/codegen/xai-grok-shell/src/util/config/persist.rs:91-95`
- `grok-build/crates/codegen/xai-grok-shell/src/util/config/persist.rs:206-216`

不采用跨进程锁可以是产品取舍，但必须把保证降级为“best-effort 冲突检测，仍有 compare→rename
残余窗口”，并保留备份恢复。当前 §3.3-5、§5、§11、§12 使用“绝不覆盖”“同等保护”“最坏
情形不会覆盖”的绝对表述，会让实施和安全验收得出假结论。

### M（必改）

#### R2-M1. Phase 0 的配置路径与 effective-layer 核查步骤不可直接执行

第 1 步要求解析 `GROK_HOME`，第 2 步却仍硬编码写 `~/.grok/config.toml`；应写实际解析后的
`$GROK_HOME/config.toml`。第 2 步“确认无 requirements/MDM 覆盖”还需列出具体检查命令或
官方 effective-config 证据入口，不能只写结论。

custom `--leader-socket` 只会在 grok TUI 自己的进程中转成 `GROK_LEADER_SOCKET`；CordCode
Link 不会自动继承另一个进程的 flag。既然该场景 excluded，状态 #5 应明确只反映 **Link
进程实际继承的 env**，不能暗示能发现任意 TUI `--leader-socket`。

#### R2-M2. T11 仍把完成标准留成“二选一冻结”

“创建目录与文件或失败可见（二选一冻结）”不是测试期望。设计必须现在裁决：建议创建
`$GROK_HOME` 时使用 owner-only 目录权限，并原子创建 config；或者明确目录不存在即拒绝。
测试不能等实现时选择产品行为。

#### R2-M3. fail-visible TOML 检测缺少解析机制，并遗漏 symlink 文件语义

当前 Mac app 没有 TOML parser 依赖。要可靠识别 dotted key、quoted key、quoted table 与
inline table，实际上需要 TOML parser 或明确受支持的词法子集；简单正则无法证明“没有等价键”。
§3.3 应指定读取/检测机制以及 parser 失败如何进入 F1/F2。

另一个关键文件形态是 symlink。用户常用 dotfile 管理器把 `config.toml` 链接到别处；同目录
temp + rename 会替换链接本身，而不是更新目标。应新增 symlink 的 fail-visible 或明确解析目标
策略及测试。当前真实样本是普通文件，不能证明 symlink 安全。

#### R2-M4. absent 与 explicit false 仍被合并，T16 的 false→OFF 路径无法从当前 Toggle 触发

状态 #1 把 absent/false 合并，但 absent 允许 remote fallback，false 会明确阻止 remote；二者
不是同一配置状态。当前 Toggle 在 false 时已经显示 OFF，用户没有“再次关闭”动作触发 T16 的
删键逻辑，除非先开再关。

至少应在 DiagnosticsSheet 区分 `absent` 与 `explicit false`，并裁决是否保留用户既有 false、
提供“恢复默认”动作，或在首次管理时迁移。不能依靠一个 UI 不可达的 setter 分支证明 OFF=删键。

#### R2-M5. F-7 验证落点仍指向不会断线的正常 TUI 关闭场景

§6.1 把 F-7 验证落点写为 §7.3 第 8 行 + Phase 0 第 6 步，但这两项都是关闭 TUI；在 accepted
observer 续命模型下 leader 不退出，正常情况下不会触发 F-7。F-7 应由 Phase 0 第 8 步的
leader crash/kill 场景验证，并在生产验收矩阵中增加对应行，检查 `turn_aborted` + idle。

#### R2-M6. 内部编号与来源清单仍有新错误

- 文档 `:33` 引用不存在的“Phase 0 第 0 步”，实际是第 1 步。
- 文档 `:158` 写“§3.3 七条规则”，实际 §3.3 有八条。
- 来源清单把工作树写成“干净（本文与评审报告纯新增）”，但 P0 要求逐项列出未跟踪路径；应与
  本报告 §1 的形式一致。
- §0 的“唯一拦路条件”与后文 requirements/confinement/stale/interaction 多个边界冲突，建议改成
  “默认用户首要前置条件”。

### S（建议）

#### R2-S1. 修正“不做 Swift 拨测”的理由

官方 leader 是多客户端架构，Swift 连接并不是与 LeaderSubscriber “竞争单活连接”。更准确的
理由是：额外连接会扰动 leader client count/lifecycle，若做完整握手还会复制协议职责；因此本
设计选择不拨测。结论可保留，理由应准确。

## 4. TOML 样本审计

| 内容形态 | 本轮证据 | 结论 |
| --- | --- | --- |
| 普通 `[cli]`、无 `use_leader` | 本机真实 `$HOME/.grok/config.toml`：mode 0644、ASCII/LF、首行 `[cli]` | 已核实 |
| canonical true/false | 官方 `mcp.rs:2256-2273` fixture | 已核实源码 fixture；仍待 Phase 0 真实写入 |
| CRLF / trailing comment / leading whitespace | 无真实样本；仅规划 T12–T14 | 待实现测试，不得宣称现场兼容已证明 |
| dotted/quoted/inline equivalent | 无真实样本；规划 fail closed | 方向可接受，但缺可靠检测机制 |
| symlink config | 未覆盖 | 必须补策略和测试 |

本轮没有嵌套记录计数或跨类型等价提取，因此不涉及双脚本数量交叉验证；关键风险是文件形态和
解析覆盖面，而不是样本计数。

## 5. 五个维度结论

### 5.1 事实核查

B4、首轮行号、日志链、UI 槽位和测试 target 已修正。配置 merge precedence、confinement
位置、observer 取消条件、stale 恢复以及 interaction 路由仍有事实错误；这些错误都来自 pin
源码可直接裁决，不应留给实现期猜测。

### 5.2 设计闭合性

状态机与测试覆盖明显改善，但 T11 仍未裁决，TOML 等价形态检测没有实现策略，symlink 未覆盖，
absent/false 的产品语义仍未闭合。内容比较方案只能 best-effort，不能承担绝对并发安全承诺。

### 5.3 “零 go-bridge 改动”独立验证

狭义主链仍成立：默认 socket 正常、下一次订阅时存在 live leader，则现有 Go 自动走 push。
但零 Go 的真实代价比 v2 写得更大：observer 不随 iOS 关闭会话取消；stale socket 会导致每次
重开都重复失败；interaction 可能无人应答。接受这些边界后仍可坚持零 Go，但必须用真实语义
重新取得 owner 裁决，不能基于“只续命到会话关闭”或“下次重开恢复”的错误前提。

### 5.4 纪律一致性

§0.4 仍正确限制了评审范围：不应要求新 API 客户端、codec、catalog、follower 可写或删除
tailer。source-first 纪律在 B4 和大部分 B1 修订中得到落实，但新加入的 stale/续命收益归因又
超出了源码能证明的范围。TOML 对未知等价形态 fail closed 是正确方向，仍需把解析与文件类型
边界写实。

### 5.5 内部一致性

首轮主要交叉引用已修复，但 observer 生命周期同时被写成“到 iOS 会话关闭”和实际
`context.Background()` 常驻；F-7 验证仍指向不会断线的场景；另有第 0 步、七/八条规则和来源
状态表述错误。

## 6. 总结论

**退回。**

进入 Phase 0 前至少必须修正 R2-B1–B4，并重新让 owner 基于以下真实代价裁决零 Go 路线：

1. observer 建立后不会随 iOS 关闭会话取消，可能续命到 CordCode Link runtime 退出；
2. stale socket 不会在下次 session-open 自动恢复，会持续阻断观察；
3. 无人可答 interaction 时 turn 可能等待，不能把“TUI 关闭后继续完成”作为无条件收益；
4. 非协作进程间的 config 写入只能 best-effort 检测冲突，不能承诺绝不覆盖。

这些修正完成后，其余 M 项属于可局部闭合的设计与测试缺口，不要求改变“不是新 backend、
不做 follower 可写、不删除 tailer”的既定边界。
