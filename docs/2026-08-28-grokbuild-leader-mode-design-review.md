# Grok Build Leader 模式开关设计评审报告

- 评审日期：2026-08-28
- 评审对象：`docs/2026-08-28-grokbuild-leader-mode-design.md`
- 评审方式：只读源码核查；未构建、未运行测试
- 总结论：**退回**

## 1. 来源核对结果

来源门通过，评审结束时未发生漂移：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅 ?? docs/2026-08-28-grokbuild-leader-mode-design.md
任务预期分支=main

配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净

预期产品特性=MacBridge 工作站 grok build 行内 Leader 模式开关；不新建 backend，
不实现 follower 可写方向，不删除或绕过 file tailer
```

本机评审时安装的是 `grok 1.0.12 (ece2b556c271)`，而配套源码
`xai-grok-version` crate 版本为 `1.0.10`。这不改变本次固定提交源码评审的来源，
但构成 Phase 0 必须记录和核验的实际安装版本差异。

## 2. 分级发现

### B（阻断）

#### B1. `[cli].use_leader` 的配置覆盖模型错误，UI 读取的用户文件不等于官方 effective config

**核查结论：不符。**

设计 §2.1-1/2 声称项目级 `.grok/config.toml` 可覆盖全局 `[cli].use_leader`，但引用的
`xai-grok-shell/src/util/config/mcp.rs:93-115` 是 MCP 专用加载器。官方 TUI 实际从
`xai-grok-pager/src/app/mod.rs:665-716` 调用 global effective config；
`ConfigLayers` 没有 project slot，源码测试还明确说明 repo `.grok/config.toml`
不能进入该层：

- `grok-build/crates/codegen/xai-grok-shell/src/util/config/mcp.rs:93-115`
- `grok-build/crates/codegen/xai-grok-pager/src/app/mod.rs:431-481,665-716`
- `grok-build/crates/codegen/xai-grok-config/src/config_layers.rs:20-75,177-197`
- `grok-build/crates/codegen/xai-grok-shell/src/config/tests.rs:3040-3043`

真实解析链还包括：`--no-leader` → `--leader` → eligibility → effective layered config
→ remote → 默认 false；随后 requested confinement/sandbox 还可以 veto leader。
effective config 包含 system-managed、managed、user、requirements 等层，而拟新增 manager
只读取用户 `config.toml`。

因此必须修正 §2.1、§3.4、§6-5、帮助文案和诊断逻辑。不能再把“配置 true 但永不生效”
仅归因于版本漂移，也不能把 project config 当成 `[cli].use_leader` 的覆盖来源。

#### B2. MacBridge observer 会续命 leader，并非“源码未完全确认”

**核查结论：不符，且源码已经足够裁决。**

MacBridge observer 会 register 并以 30 秒 ping 保持连接：

- `cordcode-macbridge/agent/grokbuild/leader_subscriber.go:154-175`

官方 leader 把接受到的连接加入 `clients`，注册后增加 client count；只有
`clients.is_empty()` 时才执行默认的“最后客户端断开即退出”：

- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:1620-1657`
- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:1697-1761`

MacBridge relay 又用 `context.Background()` 建立订阅：

- `cordcode-macbridge/go-bridge/handlers_relay.go:244`

关闭 TUI 后 observer 仍连接，会形成“leader 等 observer 断开、observer 等 leader 断开”的
续命关系。这直接推翻：

- §6-4“源码未完全确认”；
- §7.3 第 8 行“关闭 TUI → leader 退出 → F-7 中断”；
- §0.2 第 4 步直接删除键、重启 grok 即证明恢复 inline；
- §4.9 把关闭最后一个用户客户端描述成常规 leader-disconnect 场景。

设计必须先裁决是否接受 observer 续命。若坚持零 Go 改动，Phase 0 和回退验收必须显式断开
observer 或退出 CordCode Link 后再验证 leader 退出，并把生产期间的续命行为写入产品边界。

#### B3. `stat(leader.sock)` 不能证明 leader 活着或 push 已生效

**核查结论：当前状态语义和失败链不成立。**

现有 Go 逻辑只在每次订阅时执行一次 `os.Stat`。文件存在便选择 leader；dial/register/load
失败后 channel 关闭，不会在本次订阅自动切换到 tailer：

- `cordcode-macbridge/agent/grokbuild/grokbuild.go:174-198`
- `cordcode-macbridge/agent/grokbuild/leader_subscriber.go:137-212`

官方仅在正常 server 收尾时删除 socket；异常崩溃可留下 stale socket：

- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:2348-2351`

因此 §3.4 的“socket 存在 = 已生效”以及状态 #4“Leader 已在运行”都可能是假状态。
`false + socket` 还可能只是配置已被手改成 false、旧 leader 尚存，不能归因为 remote 推荐或
`--leader`。

另有路径身份缺口：runtime 支持 `GROK_LEADER_SOCKET`，官方支持 `--leader-socket`，但 Swift
manager 只计划观察 `GROK_HOME/leader.sock`：

- `cordcode-macbridge/agent/grokbuild/leader_subscriber.go:63-79`
- `grok-build/crates/codegen/xai-grok-pager/src/app/cli.rs:438-445`

“live 默认 socket 已存在时，下一次订阅自动走 push”确实无需 Go 改动；但“状态真实、stale
可恢复、custom socket 可诊断”不能由当前设计同时保证。

#### B4. “leader 事件流不含 session 列表变化通知”是事实错误

**核查结论：不符。**

官方 leader 明确把 `x.ai/sessions/changed` 作为 machine-wide notification 广播给所有客户端；
官方 Pager 也消费该通知的 roster upsert/remove：

- `grok-build/crates/codegen/xai-grok-shell/src/leader/server.rs:390-428`
- `grok-build/crates/codegen/xai-grok-pager/src/app/acp_handler/settings.rs:409-426`

MacBridge 列表继续保持 5 秒/60 秒 polling 的结论是对的，但真实原因是当前
`LeaderSubscriber` 只处理 session-update 方法并丢弃该 roster 通知：

- `cordcode-macbridge/agent/grokbuild/leader_subscriber.go:319-334`
- `cordcode-macbridge/go-bridge/session_discovery.go:42-57`

§4.1 必须修正归因，不能把“当前 MacBridge 未消费”写成“官方事件不存在”。

### M（必改）

#### M1. TOML 并发与写后失败回滚没有闭合

§3.3-6 的“检查 mtime 后 rename”仍存在检查与 rename 之间的 TOCTOU，可覆盖同期官方写入。
必须规定锁或内容身份比较，以及冲突时失败而非覆盖。

§3.3-5 写后校验失败仅提供手工恢复，没有自动回滚。应规定：只有目标仍等于本次写入内容时
才能原子恢复备份；否则保留现场并报告并发冲突，避免回滚再次覆盖第三方修改。

T5 的“只移除我们追加的空 `[cli]` 节头”也无法跨进程重启识别来源。必须有持久标记，或改为
始终保留空节头。

#### M2. TOML 支持语法和关键测试不足

评审时真实 `~/.grok/config.toml` 样本为 mode `0644`、LF、首行为普通 `[cli]`，没有
`use_leader`。官方源码 fixture 只证明 canonical：

```toml
[cli]
use_leader = true
```

以及 false 形态：

- `grok-build/crates/codegen/xai-grok-shell/src/util/config/mcp.rs:2256-2273`

当前没有真实样本支持文档对 CRLF、尾随注释和复杂等价 TOML 形状的处理主张。T1–T10 至少补：

1. config 文件和 `$GROK_HOME` 目录均不存在；
2. `[cli] # trailing comment`；
3. CRLF 全程保持；
4. 前导空格和行内注释；
5. `cli.use_leader = true`、quoted key、inline table：正确支持或明确 fail-visible，禁止误增第二份
   语义等价键；
6. 已存在 false、absent 时执行 OFF 的精确定义；
7. 无原文件时的备份路径和错误文案分支。

#### M3. 四态表不是闭合的运行状态机

至少遗漏：

- config 读取或解析失败；
- effective config 被 managed/requirements/sandbox 改写；
- socket 存在但不可连接或协议不兼容；
- custom socket；
- UI 缓存仍显示 active、socket 已消失；
- absent 与 explicit false 对 remote recommendation 的不同语义；
- `false + live socket` 的旧进程状态。

如果 Swift 不验证连接，应把“已生效”降级为“检测到 socket，实际订阅以 runtime 日志为准”。

#### M4. Phase 0 证明链不完整

必须增加：

1. 实际 `grok --version`、发行身份和 flag/key 支持；
2. TUI、Mac app/runtime 的 `GROK_HOME`、`GROK_LEADER_SOCKET` 和最终 socket 路径；
3. `leader subscriber live`，不能只依赖早于 initialize/load 的 `connected`；
4. 两个不同项目 cwd/session 的订阅验证；
5. 回退验证前先断开 observer，否则 B2 会阻止 leader 退出；
6. stale socket 和连接失败的真实行为；
7. sandbox、chat、custom socket 场景明确 supported 或 excluded。

#### M5. file:line 引用漂移

- `ClientCapabilities` 关键原文实际在 `leader/protocol.rs:158-177`，不是 170-183。
- `agent serve` 替换目标实际在 `agent/server.rs:501-555`，不是 545。
- chat 冲突定义从 `session_startup.rs:282` 开始。
- Workspace hint 实际在 `WorkspaceView.swift:506-513`，按钮在 `:528-545`，不是
  517-523/539-553。
- 默认 false 测试实际在 `mcp.rs:2230-2253`；1876-1891 只是解析函数。

测试 target `CordCodeLinkTests` 与源码目录 `MacBridgeTests` 已核实正确。

#### M6. 验收矩阵分级和可执行性需修订

§7.3 声明只有第 4–6 行为生产路径级，但第 8、9 行同样依赖生产 runtime 与日志。第 8 行因
B2 当前不可执行。第 4 行只要求 `connected` 不足以证明 `session/load` 成功，应要求：

- `leader subscriber starting`；
- `leader subscriber connected`；
- `leader subscriber live`；
- 真实 turn 事件到达。

DiagnosticsSheet 被列为可选，但 §2.1-6/§11 又依赖“诊断可见”处理版本漂移，二者矛盾。

#### M7. 内部引用与章节结论冲突

- §2.1-6 的“fail visible，见 §10”指向权威证据入口，应改指 §3.4/§11。
- §6.1 第 8 行回归建立在错误的 leader 生命周期前提上。
- §4.9、§6-4、§7.3 第 8 行在 observer 生命周期上相互矛盾。
- 诊断“可选”与 fail-visible 要求矛盾。

### S（建议）

#### S1. 区分启动和重启

状态 #2 文案建议改成“关闭并重新启动正在运行的 grok；若尚未运行则启动 grok”，避免所有场景
都写成“重启”。

#### S2. 统一三态/四态术语

§3.2 写“三态副文案”，§3.4 又列四态且 #4 可选。建议明确区分“核心三态”和“扩展观察态”，
并严格区分“检测到 socket”与“runtime 已订阅”。

## 3. 五个维度结论

### 3.1 事实核查

flags、默认 false、remote fallback、多客户端模型、`agent serve` 单目标替换、chat 互斥、
Mac `--no-leader` 自保护、只读 subscriber、共用 codec/relay loop、四条日志原文、测试 target
均已核实。配置项目覆盖、observer 生命周期、session-list 通知三个载荷性事实不符，另有多处
行号漂移。

### 3.2 设计闭合性

TOML 原子写的并发、校验失败回滚、节头归属识别没有闭合；状态机把 absent/false 和 socket
existence/liveness 混为一谈；Phase 0 缺版本、环境路径、observer 断开和多项目验证；T1–T10
未覆盖指定边界。

### 3.3 “零 go-bridge 改动”独立验证

对“live 默认 socket 已存在，下一次会话订阅自动走 leader push”这一狭义主张成立；对“中途
开启后状态真实、stale 自动恢复、关闭 TUI 触发 disconnect、custom socket 可诊断”不成立。
是否仍坚持零 Go 改动，必须在接受 observer 续命和 stale 无 fallback 的真实边界后重新裁决。

### 3.4 纪律一致性

§0.4 正确声明本设计不是新 backend；wire 样本冻结、新旧 backend 并存、follower 可写方向均
不适用，也正确保留 file tailer。遗漏的是参照设计同样强调的实际安装版本身份、宿主路径身份、
live 而非文件存在和失败路径不假绿。TOML 是本设计唯一新外部格式面，当前真实样本只覆盖普通
LF `[cli]`，复杂文本编辑规则仍缺样本或 fixture 证据。

### 3.5 内部一致性

§4.3 的共享 codec/relay 主张成立，但 §4.9、§6-4、§6.1 和 §7.3 第 8 行在 observer 生命周期
上互相冲突；诊断“可选”与 fail-visible 要求冲突；另有 §10 错链和验收分级遗漏。

## 4. 总结论

**退回。**

必须先修正 B1–B4 的源码事实和由此受影响的状态机、Phase 0、§4.1/§4.9、§6、§7.3。
尤其要先裁决 observer 续命是否是可接受产品行为，以及“socket 存在但不可用”是否允许继续
坚持零 Go 改动；在这两个核心问题闭合前，当前设计不能进入 Swift 实施。
