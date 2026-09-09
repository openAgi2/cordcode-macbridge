# Grok Build 斜杠命令面板与计划模式接入方案

> **v1.5 · 2026-09-07 · 开发执行契约，尚未实施。** 第五轮完整评审已直接回填本版；发现与证据见[第五轮报告](2026-09-07-grok-build-slash-command-panel-implementation-review-r5.md)。开发者从 §8 执行，不需要再做一轮产品裁决。Phase 0 可启动；目录、执行与 iOS 可以按各自证据门推进。**短命 mode-only 子进程的 Plan 恢复假设在 1.0.16 源码上不成立，Phase 2 开启仍取决于目标 1.0.13 的 P4/P7/P8 实证，不能以文件写成功代替。**

> **2026-09-09 实施校正（取代本文“Plan 无面板入口 / 只读 chip”的产品结论，不改写历史证据）：** 后续 source-first 复核确认 `/plan` 是 Grok 官方 pager-local 命令，不属于 agent `_x.ai/commands/list`；pager 对其 dispatch 使用当前 resident actor 的 ACP `session/set_mode`，带描述时再发送描述 prompt。P7 否定的只是“另起短命 mode-only child 后冷恢复”路径，不否定 CordCode 已持有 resident actor 时的官方控制路径。当前实现因此在真实 agent 目录拉取成功后合并 Plan 描述符，resident actor 存活期 `canSet=true`，注销后回到本文既有冷读规则。iOS 仅为 confirmed(plan) 显示可退出 chip；default/pending/unknown 不显示。
>
> 本文整体替换 v1.4，避免历史响应表与当前契约互相覆盖。R1–R8、S1–S6、T1–T3、U1–U2 的历史响应保存在 Git `7ef00c3` 的本文及[第一轮](2026-09-07-grok-build-slash-command-panel-implementation-review.md)、[第二轮](2026-09-07-grok-build-slash-command-panel-implementation-review-r2.md)、[第三轮](2026-09-07-grok-build-slash-command-panel-implementation-review-r3.md)、[第四轮](2026-09-07-grok-build-slash-command-panel-implementation-review-r4.md)报告。当前行为只按本版正文实施。

## 0. 来源、范围与开工纪律

| 仓库绝对路径 | 分支 | 完整提交 | 首次编辑前状态 | 用途 |
| --- | --- | --- | --- | --- |
| `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `7ef00c3ebe1f573fd6f6eea6247ff6ee36cfe0e2` + 本 v1.5 修订提交（产品源码与 `de17e6f` 零差异） | 干净（评审者编辑前；本文档与第五轮评审报告除外） | 本任务指定工作树；只修订方案与评审报告 |
| `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 | v1.4 明确指定的只读 iOS 评审基线 |
| `/Users/jacklee/Projects/grok-build` | `main`（编辑前复核；此前记录为 detached，同一提交） | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9` | 干净 | 1.0.16 上游只读源码 |

Mac 产品源码与 `de17e6f782ecc1d72a29f7d4e81e6aed2405251d` 无差异。预期产品 = Grok Build 命令面板、官方模式通道、既有 plan_review 审批；其他 backend 行为保持其原有契约。目标二进制沿用调研记录 `~/.grok/bin/grok` 1.0.13、自报 `5e9a58528b76`，本轮没有运行它；Phase 0 必须重新核验路径、版本与哈希。

**配对说明**：本次沿用 v1.4 已指定的 Mac 功能工作树 + iOS main 只读来源组合，不把新发现的 `/Users/jacklee/Projects/cordcode-ios-plan-approval`（`plan/approval-layer-ios`）源码混入结论。产品实施前必须重新登记实际双仓配对并复核差异，不能拿本次 main 评审来源当成该功能 iOS 工作树的构建证明。读源码、首次编辑、构建/安装三个门点遵守双仓 CLAUDE.md P0；不自动合并、切分支或提交到 main。

本次仅文档评审与修订，不运行真实 turn、付费 API、UI/snapshot test 或 simulator automation，不修改产品代码、不安装、不提交、不 push。后续真实 turn 的费用、状态修改与复位范围在执行前向 owner 说明并取得确认；零模型协议取证可先行。iOS 产品代码改完后，按用户要求在配对来源门通过且连接 iPhone 时自动构建安装；自动安装不等于获准跑 UI tests。定向检查超 5 分钟异常止损。

前置阅读：[跨 backend 调研 §7](2026-09-04-slash-command-skill-cross-backend-survey.md)、dsh 面板方案 §11 返工教训、双仓 CLAUDE.md。协议字段必须来自目标原始样本或已标版本的官方 fixture；内部故障注入只证明本地行为。

## 1. 已裁决产品行为

| 项 | 最终裁决 |
| --- | --- |
| D1 目录 | 官方可执行目录 ∩ 已验证反馈类型准入集合；排除 `context` 和未接入的 pager-local surface；待样本项不展示。新命令不得只因出现在目录就自动获准 |
| D2 Plan | 输入框 chip：confirmed(default) 灰、confirmed(plan) 橙、pending/unknown 中性且禁用；归属或技术能力未知也禁用；点击走官方模式 RPC |
| D3 权限键 | Grok 只保留 `plan`、`default` 两种会话模式；移除 `acceptEdits/auto/dontAsk/bypassPermissions` 四键，旧调用明确拒绝；default 只退出 Plan，不代证 YOLO 权限 |
| D4 输入 | 移动端规则：非空 hint 认领输入框 `/name `，空参回车可执行；空 hint 点选即执行。不是对官方 pager 行为的同构声明 |

命令正文业务失败原样进入 transcript；RPC/终态失败沿用弹窗。无输出命令仅展示官方命令行 echo，不合成成功 toast。执行期间按命令名置灰。手写未知 `/foo` 可按普通消息发送；已经从面板认领的命令失败时绝不能转普通消息。

不做：prompt `_meta.mode`、`/plan` 文本代替模式切换、直接写官方模式文件、用假 turn 激活 Plan、旧目录/内置命令运行期兜底、按正文 NLP 猜测业务错误、`ask` 模式、其他四个权限键接真、skills/workflow 深层 UI、`/context` 本地面、跨 backend 错误 inline 化。

## 2. Phase 0：证据门与停止条件

每项归档：二进制身份、请求/response/notification 原始脱敏样本、会话/cwd/actor 生命周期、结果与失败、实际副作用、复位记录。证据原文与源码推论分开。活体与 checkout 不同，以目标证据为准并记录漂移。探针是官方协议测试客户端，不是正式运行路径里的 mock。

| 门 | 必须证明的内容 | 模型/副作用 | 放行范围 |
| --- | --- | --- | --- |
| P1 | initialize availableCommands 形状；仅诊断 | 无模型；启动进程仍记录运行目录和配置 | 中间解码类型 |
| P2 | initialize → session/load 的 ACU 完整形状，空表、input.hint、skills/workflows；握手阶段不能丢目录 | 无模型；隔离测试会话 | ACU 解码与目录缓存 |
| P3 | catalog 进程 `x.ai/commands/list {cwd}` 每次实际调用；目标 cwd、项目插件、skills、availability 与会话 ACU 的等价范围 | 无模型；插件发现可能读配置/写信任缓存，不声称零副作用 | List 主通道。与实际会话不等价则设计期改定为专用 child load 后读取会话目录；两者不可行则阻断 List。运行期不得自动互相降级 |
| P4 | (a) idle set plan → 等持久化 → 关闭并 reap → 新 actor load → **不带 mode 的真实 prompt 按 Plan 工作**；(b) 已有 CordCode idle actor/活跃 turn、外部 TUI 打开同会话时分别验证路由、写序与后续 prompt | (a) 至少一条真实 turn；(b) 另列实际 turn 数与 owner TUI 配合；测试会话可弃 | Phase 2 的恢复与 actor 归属门；不能只看文件或旧 CMU |
| P5 | stdout/gateway/jsonl 的 CMU 形状、身份及 replay/live 标记；缺序号也必须满足 §5.3 | 无模型，顺带 P7 收集 | 三路触发解码；序号仅优化，不拿通知声称值当待完成目标 |
| P6 | §7 反馈类型样本、官方 user echo/正文身份、实际 prompt response stopReason、取消/错误、权限请求与 usage 更新 | compact 可调模型且压缩不可逆；always-approve 恢复**原值**；dream/flush 可能修改跨会话记忆，必须先证明隔离 GROK_HOME/配置/记忆存储；feedback 会外发，不得未经明确授权发送 | Execute 终态/反馈、逐类型 D1 准入。可先用无外发的 hooks 样例；危险副作用未隔离则不取该样本、不准入该类型 |
| P7 | ack 与持久化分离；幂等；EOF 优雅关闭和强杀差异；**Pending/Active/ExitPending 恢复后的 effective mode**；读取成功后 child 关闭能否破坏承诺；外部 actor 写序 | set_mode 本身无模型；隔离会话状态变化；故障注入与真实协议样本分开 | 与 P4/P8 一起决定 Phase 2，文件字节正确不放行；有限样本不证明任意写盘延迟上界 |
| P8 | 选择唯一权威读源与映射：官方 snapshot 经恢复规则后的有效模式；missing/corrupt/readError 分开；summary 必须证明等价才可选；CMU 历史永不充当确认源 | 只读样本 + 官方恢复源码；隔离异常测试 | 冷 hydrate、热验证、事务共用读取契约 |

**决定性失败条件**：1.0.16 的 idle set plan 进入 Pending，`from_snapshot` 把 Pending 恢复为 Inactive。若 1.0.13 相同，短命 mode-only 方案停止；不得靠加长读盘等待、把 Pending 映射成 plan、补 `_meta.mode`、写文件或隐式 prompt 造成功。Phase 2 必须另有经证明的官方 actor 生命周期方案才能开通；不擅自引入常驻 actor 兼容分支。目录与执行按独立证据门继续。

## 3. 源码事实与实施锚点

Mac 路径相对 §0 Mac 仓根；iOS 路径相对该仓 `OpenCodeiOS/OpenCodeiOS/`；上游简写文件位于 `crates/codegen/xai-grok-shell/src/`，其中 `spawn.rs` 指 `session/acp_session_impl/spawn.rs`。行号只属于 §0 对应提交。

| 路径/符号 | 核验事实与实现含义 |
| --- | --- |
| 上游 `crates/codegen/xai-grok-shell/src/session/plan_mode.rs:96`、`:145`；`session/acp_session_impl/session_mode.rs:44`；`spawn.rs:625` | Pending/ExitPending 恢复成 Inactive，只有 Active 的 session_prompt_mode 为 Plan；idle set plan 的持久化不是跨 child Plan 保证 |
| 上游 `session_mode.rs:393`、`persistence.rs:1945` | persistence 入队返回与写盘独立；CMU/response 不是 durable ack |
| 上游 `extensions/session_admin.rs:859`、`:885`、`:906` | cwd pull 和 resident sessionId pull 是不同分支，后者要求该 catalog 进程已 load；不得把另一个进程的 sessionId 直接塞入单例就假定可用 |
| 上游 `session/slash_commands.rs:852`、`session/acp_session_impl/slash_exec.rs:60`、`acp_session.rs:1365` | list 参数/返回、shell 执行、正文反馈；Grok 命令走 prompt；官方 pager-local plan/context 不等于 shell 可执行目录 |
| Mac `go-bridge/handlers_session_commands.go:52` 对比 `handlers.go:2572` 起的 Send 路径 | Execute 目前只调 catalog 接口，缺会话注册、admission、subscription、relay；“复用 Send”必须实际接完整生命周期 |
| Mac `agent/grokbuild/session.go:214`、`:511`、`:1018`、`:1073` | 握手会 drain Events；Send 写完即返回；prompt response 非 error 一律成功且丢 stopReason；cancel 通知也走成功形状 |
| Mac `go-bridge/handlers_relay.go:2795`、`:2945`；Send 的 registry 复用分支 | Events 是消费通道，relay 结束不等于 child 必然退出；后续 Send 可复用 alive session，不能假设每 turn 自动重载磁盘 |
| Mac `updates_file_tailer.go:60`、`leader_subscriber.go:573` | EOF tail + 丢 isReplay 无法冷 hydrate；必须新增权威读取 |
| Mac `handlers.go:4505`、`:4532`；`agent/grokbuild/grokbuild.go:573` | 当前权限读写是 agent 级，六键含空转；需 typed session 状态与明确路由，不能只新增一个接口 |
| iOS `Services/Backend/BackendClient.swift:132`；`Services/Bridge/CCCodeBridgeBackendClient.swift:961`、`:978`；`Services/Backend/BackendModels.swift:449` | fetchPermissionModes 无 sessionId；set 响应缺 appliesTo 会默认 newSessions，能触发另开会话；必须修全链与响应 |
| iOS `Models/SessionProjection.swift:32`；`App/ChatInputAccessoryView.swift:1126`、`:1641` | 现有 planMode active/pending 是 dsh 切换语义，pending 会反转展示；default chip 还被隐藏，不能直接套 Grok 三态 |
| iOS `App/ChatUIKitContainerView.swift:1680`、`:6193`、`:6583`；`App/ChatInputAccessoryView.swift:382`、`:1076` | slash List 失败退普通 Send；菜单是静态构建，预取不是每次打开；权限和执行异步需会话 fencing；compact/expanded 两种菜单入口都要接 |

## 4. Mac 命令目录与执行契约

### 4.1 List、缓存与准入

- 每次面板打开/重试发 List，bridge 每次实际官方 pull；失败返回错误，空表就是空表，绝不返回旧表冒充刷新。
- P3 在设计期固定一种通道。catalog 单例生命周期独立于单次请求；请求 ctx 必须约束等待启动、锁、写 RPC、等回复的全部过程。List bridge 预算 15s 内，iOS 30s；不能被 catalog 既有 60s 等待覆盖，失败必须及时释放 waiter。（2026-09-07 通道重做：固定通道从「专用 child load 后读 ACU」改为 catalog 单例 `_x.ai/commands/list {cwd}`，见 §12 与 EVIDENCE P9。）
- 身份键至少 `(backend, sessionID, cwd)`，并记录 backend 实例/配置代际；cwd、binary/config 或 session 重建时失效，bridge 重启全清。每轮操作一次性捕获 cwd/model，不读可被其他会话修改的 agent 全局临时值。
- ACU 在握手 drain 之前更新 side-state，不靠稍后消费 Events 才存目录；stdout/gateway 的 ACU 同一缓存入口、整表替换含空表。用本地请求/actor 代际隔离过时结果；无法排序的 ACU 标记待刷新，不能覆盖较新 List 成功值或污染另一 session。
- 缓存只作 Execute 白名单和诊断，不作 List 展示返回源；无 TTL，不每次 Execute 二次拉取。失败的 List 将该身份标记不可用，不能随后拿旧成功缓存执行；下一次 Execute 先实拉，失败即拒绝。
- Name/Description 透传；Hint 从可选 input.hint 映射，缺席为空串。执行白名单也必须取 D1 交集，不能 List 排除了 context 而 Execute 接受它。
- 保留官方非原子边界：List 后命令被删除，执行时官方 resolver 可能当普通 prompt；不承诺目录与执行原子。但客户端自己遇到网络/目录错误不得主动降级成聊天。

### 4.2 Execute 是一个完整 turn

保留 `SessionCommandCatalog` 与既有两个 wire RPC；Grok 走 prompt，dsh 保持 host action。不要让通用接口抽象掩盖不同生命周期。

1. 校验 sessionId、backend 身份与 line：首字符 `/`、拒绝 CR/LF、多行及空命令；按官方样本定义 token 提取，未命中 D1 白名单 fail closed；缺缓存先走 §4.1。不注入附件、mode 或合成 user echo。
2. **在 load/spawn 之前取得 per-session operation lease**。Send、Execute、SetSessionMode 都经过同一个协调入口；不能只锁 Execute 的 wrapper。lease 覆盖 admission、registry、订阅建立、load、prompt、实际终态及该操作需完成的取消清理。等待受 ctx 约束。A/B 会话独立。
3. 抽出 Send/Execute 共用的 turn dispatch，复用 registry/admission/session subscription/relay、权限及 ask 回答路由、投影写入。Grok catalog 实现通过该协作路径执行，不能私下创建不在 registry 的 child。dsh 不被强制改成 prompt。
4. **Events 只有一个消费方**：现有 relay/dispatcher。Execute 等待该 dispatcher 根据当前 prompt/request ID 结算的独立 terminal future；不能两个 goroutine 分读 Events。审批请求必须在 registry 可找到对应 child，流式正文及 usage 同时正常进入投影。
5. 在 `handleResponse` 保留真实 `result.stopReason`，并区分 JSON-RPC error；不能只改 `convertSessionUpdate`。P6 核定正常终态允许集合，`cancelled`/`error`/未知或缺失必需 stopReason 均显式失败，未知保留原值。重复终态只结算一次，replay/旧 prompt 的 Done 不结算新 turn；EOF/crash 不能挂起或成功。
6. Execute bridge 总预算 290s，iOS 300s；取消/超时发送官方 cancel 并进行有界清理，必要时关闭并 reap CordCode 自有 child。取消不等于成功；release 前确保旧 actor 不会继续与下次操作竞争。清理超时则该 session 标 unavailable，保持失败现场与诊断，禁止并发重启造成功。
7. `ResultKind: success` 只代表官方 turn 正常结束，业务失败正文原样呈现。ResultText 仅在 P6 证明可透传时填写，不从正文猜测。iOS 不把已在 transcript 的相同反馈再造一个气泡。

必须明确 child 策略：Send 的现存 registry 可以复用 alive actor。此次协调器对每个操作记录 actor ownership；Phase 2 若依赖 spawn 恢复，须在 lease 内关闭并 reap 原有 CordCode idle actor后再 load，且在切换事务结束前按已证明的生命周期清理。进程关闭不是 `Done` 的同义词。不能关闭外部 TUI actor。

## 5. 模式状态与切换契约

### 5.1 typed 状态、能力与权限路由

新增 session 专属状态结构（Go/协议/Swift 一致）：

```text
SessionModeState {
  status: confirmed | pending | unknown
  mode?: plan | default       // 仅 confirmed 必须有；其余省略
  canSet: bool               // 技术门 + 归属 + 状态允许共同决定
  reason?: string            // 稳定原因码；详细错误单独诊断
}
```

通过现有 session snapshot/patch additive 字段 `sessionMode` 传递，携带既有 session 身份与有序 revision；若既有 envelope 缺少跨重连的 epoch/revision，则一并 additive 增加并测试。不要把 pending/unknown 塞进 string mode，也不要重用 dsh `planMode {active,pending}`。

core 接口返回 typed 状态和 error：`GetSessionMode(ctx, sessionID)`、`SetSessionMode(ctx, sessionID, mode)`。handler 验证会话归属与输入，Grok 永不落 legacy ModeSwitcher。两键菜单的当前值来自同一状态；unknown/pending 不默认选中任何键。四个删除键、空 sessionId、未知 mode 均拒绝，不改 agent 内存、不广播成功。

复用 `list_permission_modes`/`set_permission_mode`，前者添加可选 sessionId（Grok 必填，其他 backend 保留原合同）并返回 typed 状态。Grok 成功响应显式 `appliesTo: current_session`（沿用 wire 实际枚举拼写），不能让 iOS 缺字段默认 newSessions。响应只确认动作完成，不是 UI 模式值写者；状态由唯一投影路径发出。

能力不能仅靠 Go 类型断言：增加明确 readiness 查询/门控，`session_commands` 只在目录+执行门通过时广告；模式按 canSet 和有效权限项的 enabled 状态开放。旧版本/未知归属/门失败不能广告两键可执行，不复活六键。可保留禁用 chip 说明原因；不新增 RPC 名。

### 5.2 actor 与事务

- idle 且已证明无外部竞争：才可按 P4/P7 选定的官方路径切换。
- CordCode 活跃 turn：切换等待同一 operation lease，ctx 到期报错。重复切换在登记 pendingSwitch 后立即 busy，不排队重放。
- 外部 resident 在场或无法确定归属：**canSet=false，拒绝写入**，直到 P4(b)/P7(e) 证明支持方式并更新实施证据。没有探测能力就不能把“未发现”当作“没有”。不承诺 TUI 热更新。
- operation lease 与状态短 mutex 分开：前者跨 I/O 生命周期，后者仅保护状态/代际与序号。CMU/读循环不等待 operation lease，避免事务等读而读等事务的死锁。

事务总预算不超过 25s（iOS 30s），含等 lease、启动/load、set_mode、读回与清理；预算不足即真实错误。pendingSwitch 保存 requested、操作 ID、deadline。RPC response 只证明 actor 接受；之后统一权威读必须确认 **恢复后有效模式**等于 requested，且已验证的关闭/恢复承诺成立，才能成功。初始读回窗口 2s 是可失败预算，不是上游写盘延迟保证；P7 可据测量调整总预算以内的分配。

Pending snapshot 不是 confirmed(plan)。任何一项失败返回错误「模式未确认」及最后有效读值（若有），不自动再发 set_mode、不补偿写。超时动作保持已结束，迟到 response 不改结果；后续只读观察即使确认新状态，也不回头把该请求改成成功。

### 5.3 唯一读取、失效与冷恢复

权威读 `readAuthoritativeMode(session)` 在 P7/P8 固定唯一来源和官方恢复语义。返回有效值或 missing/corrupt/readError；未知状态枚举不得强转 default。missing 只有证明官方 default 语义才映射 default；否则 unknown。summary 未证明等价就不使用；updates.jsonl 最后一条 CMU 绝不充当确认来源。

**通知只表示 dirty，不携带“必须等到”的目标值。** 非自有切换事务期间，一次成功权威读在其 generation 未被新触发超过时即可 confirmed(读值)，即使与通知声称值不同。旧 plan 通知迟到、文件有效值 default，应读后 confirmed(default)，不能等不存在的 plan 耗尽预算变 unknown。只有仍在期限内的自有 pendingSwitch 才等 requested。

统一读循环：在短 mutex 下递增 dirtyGeneration/建立读取任务；锁外 I/O；回锁比较 session/订阅 epoch 与 generation；有新触发则继续一轮，不把期间新增 dirty 清掉。每 session 至多一个读取任务，发射 revision 单调；连接重建与会话切换丢旧 epoch，hydrate 不得回写覆盖更新的 live read。同值 CMU 也触发至少一次合并后读取；不依赖跨源序号。

读入口：stdout/leader/jsonl CMU、**目录级监听**（覆盖原子 rename）、session 打开/重订阅/bridge 重启首读、自有事务验证，以及**有观察者时的周期重验**。默认周期 5s、单次读取最多 2s（P7 校准）；因此监听丢事件也有独立重读。切后台/取消订阅停止监视并失效，重新观察先 hydrate。watcher 失败记录 health 并触发重读；周期读取失败/超出 freshness 预算必须 unknown，不能无限期保留历史 confirmed。

读取在途显示 pending；真实读取失败立即 unknown，可按 0.5/1/2/4/8s 有界只读重试（总约 15.5s）。成功读取有效当前值可恢复 confirmed；失败预算耗尽保持 unknown，周期重验仍可恢复。结束了的自有事务不再拿 requested 控制显示。任何重试都不写模式。UI confirmed 含义是最近成功验证的有效模式，不承诺所有外部活 actor 热同步，也不承诺任意未来写盘成功。

## 6. iOS 与协议全链改动

| 改动面 | 开发要求 |
| --- | --- |
| 面板入口 | `ChatUIKitContainerView` capability 门控扩 Grok；`ChatInputAccessoryView` compact/expanded 菜单的**每次实际打开**异步回调触发 List；不能只使用 session 首次预取。返回后刷新当前菜单；明确 loading/error+retry/empty，Grok 不回退 dsh 三条 |
| routing | `SlashCommandRouting` 白名单 per-backend；dsh 保留原规则，Grok 用服务端准入目录；保留 hint 移动端判决 |
| 认领与发送 | 保存 claimed command 身份和原 draft；List/Execute 失败保留输入并弹错，绝不进 normalSend；仅主动手写且未认领的未知命令走普通发送 |
| 异步隔离 | List、Execute、模式读写都捕获 `(backend, sessionID, cwd, generation)`；A→B→A、关闭重开、重连后旧结果丢弃；只能清空属于本操作且未被用户继续修改的 draft；错误/执行中状态也按身份隔离 |
| 权限读取 | `BackendClient` 协议、各 conformer、`CCCodeBridgeBackendClient`、解码模型、调用者全链加 sessionId；Grok 强制有会话。避免新增协议参数后只改一个实现导致编译失败 |
| 模式写响应 | 明确解析 current_session；禁止缺失 appliesTo 的 Grok 响应触发 newSessions；删除 Grok 乐观 chip 翻转及旧 response 本地覆盖。其他 backend 既有行为保留 |
| 状态投影 | Mac core event/state、projection reducer、snapshot/window/patch 编码 → iOS wire model、SessionProjectionMapping、SessionProjection、SessionProjectionWindow、消费 store 全链新增 sessionMode；snapshot 与 patch 的相同状态保持一致，省略字段按兼容规则处理，不能默认为 default |
| chip | Grok default 也必须可见；confirmed/pending/unknown 按 §1 呈现；点击 setPermissionMode(sessionId)，禁用时不能执行。不要借 dsh pending 反转 active；dsh `/plan off` 仅留在 dsh |
| protocol pack | canonical pack 更新两个 permission RPC 的 session 参数/typed state/appliesTo、snapshot/patch optional 字段与 revision、能力 readiness 语义；注释按 dsh host action / Grok prompt 分开 |

UI 路径四拍：打开面板 → 真实 List 与三态 → 点命令认领或执行 → 实际 turn 过程及官方反馈。Plan 路径：default chip 可见 → 点按 pending → response+有效恢复证明 → confirmed(plan) → 后续无 mode prompt → 既有 plan_review 审批 → 权威读确认 default。技术门未过时后半链不得写成已支持。

## 7. 反馈证据准入表

| 组 | 请求/官方反馈 | iPhone 承接 | 准入证据 |
| --- | --- | --- | --- |
| 正文组：hooks 等 | `/name args` prompt；AgentMessageChunk 成功或失败文字，正常终态 | 原正文，不弹业务错误窗 | P6 一条无外发成功样例 + hooks-add 非法路径失败样例；echo/正文/终态身份贯通 |
| 无输出组：dream/flush skip | 官方无正文，可能有副作用 | 只官方 echo，执行状态结束 | 隔离全局记忆后的实际样例；没隔离就不准入对应类型 |
| 本地呈现组：context | pager ShowContextInfo；shell 分支无详情 | 第一批排除 | 不需为排除专门执行真实 turn |
| 状态组：compact、always-approve | 压缩/权限变化，可能有简短正文 | usage/权限真实投影；不能拿 Plan default 代证权限状态 | 每种状态通道必须证明手机收到；未接通则该类型不准入 |
| skills/workflows/goal 等 | 由官方目录与样本定义 | 按已证明反馈类型承接 | 未取证不能泛称“目录透传即完成”；新增反馈形态先补矩阵再准入 |

准入表记录实际名称或明确可验证的类型判据、样本路径、排除原因、二进制版本；测试 fixture 与运行逻辑严格分离。每种新副作用独立说明，不用同一个可弃 session 宣称全局状态可复位。

## 8. 开发队列与完成证明

| 顺序 | 工作单元 | 前置/停止点 | 必须交付的证明 |
| --- | --- | --- | --- |
| 0a | 刷新双仓配对与目标 binary 身份；零模型 P1/P2/P3/P5/P7/P8 | 对应真实形状取得前只做源码与测试结构准备 | 原始样本、漂移表、P7 Pending 恢复判定 |
| 0b | 报明 P4/P6 费用/副作用后取得 owner 确认并取证 | 未授权不调真实 turn；P7 已否定短命路径则不浪费 P4 去证明假成功 | 每组样本、准入终表、模式可行/阻断结论 |
| 1a | ACU side-state、权威 List、身份缓存、D1 交集 | P2/P3；List 可独立开发，能力仍不提前广告 | 真实样本解码、空/失败/缺失、每次打开刷新、并发旧值覆盖测试 |
| 1b | 共用 turn dispatch、Events 单消费者、terminal future、取消与清理 | P6 终态样本；不依赖 Phase 2 | Go 定向测试证明流/权限/终态/lease；Grok capability 门通过才开启 |
| 2 | typed 模式状态、读循环、权限全链、模式切换 | **P4/P7/P8 有效恢复与归属全部通过**；失败则只交付明确禁用与诊断，不称模式功能完成 | §9 模式测试 + 实际重启/后续 prompt/审批证据 |
| 3 | iOS 面板、异步 fencing、typed projection、chip 与菜单 | 可按 1/2 分别接入，不要求模式先过门才开发目录 | 定向非 UI unit test + 定向 build；连接 iPhone 时按用户要求安装 |
| 4 | protocol pack、双仓 CHANGELOG、交付说明 | 来源/能力/测试一致 | 记录安装来源和二进制身份；owner 真机操作矩阵待实际完成才打勾 |

不再把整个 Phase 0 一概写为所有编码的阻断点；每个依赖按表放行。独立目录/执行开发不是绕过模式门。每个完成项必须给真实证据路径和结果，未执行写未执行；不要以“报告已写”替代协议、build 或产品验收。

## 9. 定向验收与真机边界

默认代码阅读、静态检查、定向 `go test ./agent/grokbuild ./go-bridge` 及实际受改 core 包测试；Swift 选择对应非 UI unit test 与定向 build。内部 mock/故障注入允许且与产品路径隔离；UI/snapshot/simulator 自动化须另获明确授权。

| 组 | 必测关闭条件 |
| --- | --- |
| 目录 | 首开 A→外部变 B→无 turn/cwd/重启→再开 B 或真实错；空表替换；失败不能执行旧表；context 不可 List/Execute；握手 ACU drain 前存储；跨 session/cwd/config 隔离，迟到 ACU 不覆盖新表 |
| 生命周期 | Send/Execute/SetMode 争同会话不并发 load；跨会话不互堵；先装订阅再 prompt；一个 Events 消费者；正文/usage/权限请求可见并可回答；终态只结算本 prompt 一次；旧/replay Done 不结算；EOF/取消/未知 stopReason 不成功；ctx 到期、清理超时不泄漏 waiter/lease 或并发 actor |
| 模式恢复 | **Pending 文件存在+关闭后默认不算 Plan 成功**；Active/Inactive/ExitPending 按官方恢复；无 `_meta.mode`；idle actor 复用不会绕开恢复门；外部归属未知禁切 |
| 读状态 | CMU 先到不提前成功；无 CMU 幂等可完成；自有写超时真实错；旧 plan 通知+当前 default → confirmed(default)；同值通知仍读；读中新增 generation 再读；W+ε 写入无通知由目录/周期重读发现；模拟 watcher 丢事件仍周期发现或 unknown；读取失败/快照损坏不借 CMU 历史；旧 hydrate/response/epoch 不覆盖新状态 |
| 协议/UI model | 六键×Grok/legacy 分派；Grok 两键当前值 per-session；A/B 和 A→B→A 旧请求不串；current_session 不新建会话；snapshot/patch/window 相同结果；Grok default chip 可见，pending 不套 dsh 反转；claimed List 错不 normalSend、不误清新 draft；其他 backend 回归 |

Owner 真机手工（agent 可读日志；不自动点击）：①两种输入布局每次打开目录三态；②compact claim+空参执行、正文失败原样显示；③未知手写 slash 与认领失败分离；④Plan pending→确认→重启→真实计划 prompt→plan_review 审批回 default（模式门通过才做）；⑤A/B 会话切换和权限菜单不串；⑥Mac 外部切换仅在已证明范围观察，未知归属不可写。每项记录实际结果；安装成功不代替这些产品行为证据。

## 10. 风险、回滚与交付口径

- 短命 mode-only 恢复不成立：明确阻断 Phase 2；没有官方方案证据不得自造兼容层。此时产品可交付目录/执行，不能宣称本方案两个目标全部完成。
- 文件状态与热 actor 内存不等价：来源/归属不确定即 unknown/canSet=false；不宣称手机改变了外部 TUI。
- 目录与执行非原子：保留官方 resolver 行为与实际错误，客户端不加入隐式重试或旧缓存成功路径。
- 回滚分别关闭 Grok session_commands readiness 与模式 canSet/权限入口；旧 RPC 明确不支持。**绝不恢复六键 legacy 空转，也不退回 dsh `/plan` 内置菜单。** additive 字段可保留，旧客户端忽略；失去权威源就 unknown，不能留下可写 chip。
- 交付报告分别列文档完成、Phase 0 取证、各功能实现、测试、安装与 owner 验收；未完成的协议门逐项标明。第五轮文档闭合不等于模式可行性已经证明。

## 11. 交付后 owner 裁决（2026-09-07 22:4x，b57b3e5）

owner 真机走查后裁决：「暂时只做 compact，plan，goal 三个命令……hooks 那些命令我压根
搞不懂是什么，也从来没用过」。落地（细节见 `scripts/grokbuild-phase0/ADMISSION.md` §五）：

- 准入表 `grokAdmittedCommands`：5×hooks-* → **{compact, goal}**（两条均在官方握手目录
  真实广播，`samples/p2-handshake-notifications.jsonl` 零模型样本含 hint）。
- **plan 不是 grok 斜杠命令**（仅保留名；会话模式 = sessionMode）——模式切换维持 §10
  P7 阻断裁决，iPhone 只读 chip 呈现。面板无 plan 行是官方事实，不是本方案遗漏。
- hooks-* 同步移出执行准入：手写 `/hooks-*` 按「未准入命令」fail-closed（保留输入并
  提示），不当普通消息发出。
- §9 owner 矩阵中引用 hooks 命令的步骤（②compact claim/③未知命令）改为以 compact/goal
  为对象；⑤⑥（会话隔离 / Mac 外部切换）不变。iOS 零改动（compact/goal 词典与图标已有，
  Grok 策略吃服务端目录原样）；protocol pack 描述已同步。

## 12. List 通道重做（2026-09-07 夜，owner 报障「点 ➕ 十几秒」）

owner 报障：grok build 会话点 ＋ 要十几秒才出 goal/compact。owner 指令：先读
grok-build 官方源码（`/Users/jacklee/Projects/grok-build`）与成熟 dsh-web 模式，
不要又一轮自建轮子。

- **根因（生产日志 req_8/req_15，2026-09-07 22:42）**：旧 List 每次打开都起专用
  短命 child——initialize 1206ms + session/load 3118ms + MCP 初始化的 ACU 波等待 +
  1500ms settle 静默窗 ≈ 8-10s，全部在用户点 ➕ 的关键路径上。
- **官方证据**（grok-build @ 72a6125，EVIDENCE P9）：grok-desktop 在 session start
  后用 `_x.ai/commands/list {cwd}` 拉目录（session_admin.rs cwd 分支，不需要把会话
  load 进本进程；sessionId 分支才要求 `session_handle_waiting_for_load`——旧通道
  慢在选了 sessionId 语义）；官方 pager 斜杠菜单读被动 ACU 状态，每次打开零 RPC；
  dsh-web `ListSessionCommands` = 常驻连接单次 RPC。
- **重做**：`ListSessionCommands` 改为进程级 catalog 单例（`catalog_session_list.go`
  既有基建，生产 `catalog_alive_procs=1` 常驻）上的 `_x.ai/commands/list {cwd}`
  ext RPC（新文件 `agent/grokbuild/catalog_commands_list.go`；RPC 预算
  `catalogCommandsListTimeout=12s` < bridge List 15s）。隔离探针 warm **43ms**
  （冷启动 initialize 2976ms 只在单例首建付一次）。`session.go` 删除 per-session
  ACU 观察 observer 与 settle 静默窗（目录不再来自会话波）；`acu_state.go` 只保留
  Execute 白名单/失败禁用语义。
- **不变量**：无缓存——每次 List 仍是一次真实官方拉取（红线原文不变）；D1 准入
  {compact, goal} 不变（两条均在 catalog 官方目录内，P3 不等价结论对准入集无影响）；
  空表诚实可见、失败标记该身份不可用、Execute fail-closed 不变；测试改写为 fake
  catalog e2e（initialize/authenticate/ext list × table/empty/exterr 三模式）。
- **证据与文档**：`scripts/grokbuild-phase0/p9_cmdlist_sessionless.py` +
  `samples/p9-cmdlist-sessionless.json`；EVIDENCE.md P9（含漂移表处置行更新）；
  protocol pack `docs/protocol/bridge-v1.md` grok-build List 节已更新并字节同步
  iOS mirror。iOS 零改动。
