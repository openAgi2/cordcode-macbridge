# Grok Build 斜杠命令面板方案评审

评审日期：2026-09-07。结论：**当前版本不通过实施评审；可继续修订方案及设计 Phase 0 探针，不宜按现稿开始 Phase 1–3 编码。**

评审对象：[方案 v1.0，384 行](/Users/jacklee/Projects/cordcode-macbridge/docs/2026-09-07-grok-build-slash-command-panel-implementation.md)。原文在主工作树，报告按本任务工作目录保存。已阅读 dsh 方案 §11 全节及 §11.4 迁移清单，对照 Grok shell、pager、Mac bridge 与 iOS 实现。

本次是只读源码评审加报告交付，未运行 Grok prompt、付费 API、UI tests、snapshot tests、模拟器或真机自动化；未改产品代码。下文“确认”只指已核对的源码与方案矛盾，不代表安装版 1.0.13 的 wire 已验证。

## 1. 核验基线

| 对象 | 实际核验版本 | 说明 |
| --- | --- | --- |
| grok-build | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9` | 与方案一致 |
| cordcode-macbridge 主工作树 | `de17e6f782ecc1d72a29f7d4e81e6aed2405251d` | 相对方案 `fbb4940` 仅新增本方案文档，产品源码没有变化 |
| cordcode-ios 主工作树 | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 与方案一致 |
| 安装版 1.0.13 | 本轮未活体核验 | 版本及行为暂按原文标为待证，不以 1.0.16 代证 |

下文 Grok 路径前缀为 `/Users/jacklee/Projects/grok-build/crates/codegen/`；Mac 与 iOS 均指上表主工作树。

## 2. 阻断问题

### R1 · P1：现有 SetMode 接口不能承载指定会话的可失败异步切换

**位置：方案 §5.1、§6.2.1、§7.2。**

方案将 `set_permission_mode → grokbuild.SetMode → load 指定会话 → set_mode → 官方 ack` 写成直接接线，但现有 `ModeSwitcher.SetMode(mode string)` 没有 sessionID、context 或 error 返回。`handleSetPermissionMode` 在检查 `params.SessionID` 之前就调用该方法，之后读 `GetMode()` 并广播成功。Grok 的 `a.mode` 是 agent 级字段，不是 per-session 状态。

证据：[core/interfaces.go:897](/Users/jacklee/Projects/cordcode-macbridge/core/interfaces.go:897)、[handlers.go:4532](/Users/jacklee/Projects/cordcode-macbridge/go-bridge/handlers.go:4532)、[grokbuild.go:573](/Users/jacklee/Projects/cordcode-macbridge/agent/grokbuild/grokbuild.go:573)。

按现稿，SetMode 无法可靠确定该 load 哪个会话，也无法让官方失败变成 RPC 失败；两个 Grok 会话会共享模式变量。所谓“chip 只来自官方回流”与当前 handler 的本地成功广播也不一致。

**修订要求：**定义带 `ctx/sessionID/mode` 和错误返回的会话模式接口及 handler 分支，明确冷会话和活跃会话的路由、超时、取消、同会话串行规则。可以复用 RPC 名称，但必须列出 core/handler 的实际改动；官方失败不得先改内存再广播成功。补 A/B 两会话隔离、冷会话切换、官方拒绝和超时测试。

### R2 · P1：仅放宽 iOS 门控会暴露不存在的 Grok /plan，并隐藏大部分真实目录

**位置：方案 §5.2、§7“唯一结构性 iOS 改动”。**

当前 `refreshSlashCommandCatalog` 无条件按 dsh 的 `compact/goal/plan` 白名单过滤；`AttachMenuPlanner.effectiveCommands` 对未加载、失败及空目录返回内置三条，其中包含 `/plan`。因此去掉 backend 硬门后：

- 冷启动或 List 失败时，Grok 菜单会出现 dsh `/plan`；这正是方案要求禁止发送的文本。
- List 成功时，其他 builtins、skills、workflows 被固定白名单裁掉，D1“默认全上”无法实现。
- Plan chip 的刷新仍限定 deepSeekWeb，点击仍发送 `/plan off`；只换状态数据源不足以完成切换。
- 通用执行失败当前调用 `presentSlashCommandErrorAlert`，也不是 §7 声称的“全部复用即可 inline”。

证据：[SlashCommandRouting.swift:11](/Users/jacklee/Projects/cordcode-ios/OpenCodeiOS/OpenCodeiOS/Views/Chat/SlashCommandRouting.swift:11)（白名单及同文件 AttachMenuPlanner）、[ChatUIKitContainerView.swift:6583](/Users/jacklee/Projects/cordcode-ios/OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift:6583)、[同文件:6682](/Users/jacklee/Projects/cordcode-ios/OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift:6682)。

**修订要求：**把目录过滤、空目录/加载失败、chip 显隐与 action、错误呈现纳入 iOS 清单。Grok 不得继承 dsh 静态回退目录。验收必须覆盖“首次打开，List 尚未返回”“List 报错”“官方空目录”，而非只有成功目录。

### R3 · P1：input 有无与 dsh 交互“完全同构”的前提被官方 pager 源码否定

**位置：方案 §4.1.1、§5.2、§7.1、§9 #2。**

`xai-grok-pager/src/slash/acp_command.rs:147–164` 明确把所有 ACP command 的 `has_args` 设为 `true`，注释说明 input 只决定 placeholder，不决定是否允许参数；`args_required()` 返回 false。发送完整性按 takes_args/args_required 两个属性判断，见 `slash/mod.rs:1407–1435`。这不能推出 dsh 的“input 缺席就点选执行”。

更直接的反例：`xai-grok-shell/src/session/slash_commands.rs:66–76` 的 **compact 自带 `argument_hint: Some("optional context about what to preserve")`**。按方案自己的映射和复用路由，它会 claim 输入框，而 §7.1、§9 #2 却要求当作无参命令点选立即执行。

**修订要求：**撤销“零改动复用已被官方证明”的结论。分别核对 pager 的点选/补全/Enter，再决定移动端沿用哪种交互。若点 compact 即执行是产品选择，应明确为移动端规则，而非官方 input 推导。测试至少覆盖 compact 的真实非空 hint、无 hint 的 ACP 命令、带参数提交；目标版真实样本仍由 P1/P2 补齐。

### R4 · P1：以 current_mode_update 作为唯一 ack 会使幂等切换挂起；收到通知也不等于持久化完成

**位置：方案 §6.2.1、§7.2.3、§10。**

`xai-grok-shell/src/session/acp_session_impl/session_mode.rs:44–113` 只在真实 Plan 状态转换时 enqueue CMU。已在 plan 再 set plan、已在 default 再 set default，可以成功而没有新的通知。`agent/mvp_agent/acp_agent.rs:2229–2250` 有独立的 SetSessionModeResponse；`session/acp_session_impl/run_loop.rs:737` 在处理命令后回复该请求。

此外，`persist_plan_mode_state()` 只是向 persistence 队列 send；真正写盘在 `session/persistence.rs:1945` 异步执行。文档的“等 CMU → 退出子进程 → 下次必恢复”缺少持久化屏障或已验证的优雅退出契约。不能把“已排队持久化”提升成“通知到达即安全终止进程”。

**修订要求：**将 RPC response、状态观察和 durable completion 分开定义。明确幂等无通知时如何完成、session/load 的旧 replay 如何不被误当本次 ack、退出如何保证写盘。P7 必须包含重复 on/off、重试、正常退出后重新 load 验证，而不只是截一条 CMU。

### R5 · P1：目录缺失放行与静默降级破坏方案唯一的命令防线

**位置：方案 §6.1、§10 的缓存风险行。**

原文同时规定“仅目录内命令允许 execute”和“缓存缺失时允许放行”。后者让直接 RPC、重启后重试等路径绕过目录校验；shell 对未命中命令会继续普通 prompt，这是 `resolve_human_intent` 的正常行为，不能代替 bridge 的 fail-closed 校验。旧目录中的 skill 被移除或 availability 变化，也说明“曾在目录内”不等于执行时必命中。

initialize builtins 静默替代全目录、两条全目录通道失败仍向 UI 返回成功，亦会掩盖真实失败，违背本任务禁止新增 fallback 的约束。文档引用的 wire 层还只验证 sessionID/line 非空，并没有它声称可沿用的 `/` 前缀和单行检查，见 [handlers_session_commands.go:52](/Users/jacklee/Projects/cordcode-macbridge/go-bridge/handlers_session_commands.go:52)。

**修订要求：**缓存缺失必须获取指定会话的权威目录或返回可见错误，不得直接发 prompt；补目录有效期、cwd/session/backend 身份、失效与执行期间变更策略，并明确无法原子保证目录与执行一致的边界。自行实现并测试前缀/单行校验。删除未获要求的静默降级设计。

### R6 · P1：双模式通道的“幂等”不能解决陈旧状态覆盖

**位置：方案 §6.2.2、§6.3、§10“双通道竞争”。**

幂等仅保证重复同值不会重复转换，不能保证不同来源同一会话的写入顺序。典型情况：手机缓存 plan，Mac 或审批流已切回 default，CMU 尚未到手机/bridge，下一条 prompt 携带旧 plan，`reconcile_plan_mode_with_prompt` 就会重新进入计划模式；反向也会退出刚进入的 plan。

证据：`session/acp_session_impl/session_mode.rs:191–216` 对传入值直接修改模式。另一方面，`agent/mvp_agent/acp_agent.rs:1113–1129` 在 prompt 没有 mode 时会向 resident actor 查询当前模式；`session/acp_session_impl/spawn.rs:625–648` 从持久化 tracker 恢复 current_prompt_mode。方案需要论证何时必须覆盖这条官方继承路径。

**修订要求：**明确 authoritative/desired/observed 三者关系、外部切换与审批的优先级、发送前如何收敛、三路 replay/live 的去重与排序。不要用同值幂等注释作为多写者竞争证明。P4/P5 要包含“外部退出后立刻发送”“审批批准后立刻发送”“旧 replay 晚于 live 到达”。这仍须在 1.0.13 验证。

## 3. 其他必须修订的事项

### R7 · P2：将“shell 没有 /plan”扩大成“Grok 没有 /plan”，且混淆 ACP 目录与 TUI 目录

**位置：方案 §0.2.2、§4.2.3–4、§9 #1。**

`xai-grok-pager/src/slash/commands/plan.rs:1–31` 实现了真正的 pager-local `/plan [description]`：裸命令触发 SetPlanMode(On)，带描述触发 EnterPlanMode。因而正确结论是“shell ACP 命令目录不提供 /plan，不能原样发送到 session/prompt；官方 pager 有 /plan，需转换为模式动作”。

这不推翻选择 `session/set_mode`，但推翻“官方仅按键/菜单而非 slash”的事实描述。TUI 还包含 pager-local 命令，§9 不应要求 ACP 目录与整个 TUI `/` 补全清单一致。D1 说仅 builtins，§6.1/§11 又说 skills/workflows 全部展示可执行，也需统一第一期集合。

**修订要求：**用“shell 可执行目录 / pager-local 动作 / iPhone 产品白名单”三个明确集合定义范围。L3、D1、§4.1 禁止客户端过滤与既有移动端白名单也应统一。每条最终纳入命令补齐四拍及副产物，不可仅以 compact/goal 两例代表全目录。

### R8 · P2：Phase 0 的零 API 方法覆盖不了 P4/P6，settle 也不能按所有终态映射成功

**位置：方案 §0.1、§6.1 settle、§8、§11。**

原文将方法限定为 initialize/session/load、不真跑 turn，但 P4 要验证 prompt 的 Active 注入与退出、P6 要执行 compact/always-approve。fake client 只表示客户端可控，不能阻止真实后端调用模型；已有 think.md 的零 API 证据明确是不 prompt 的模型设置探针。compact 可能调用模型完成压缩。

此外，现有 `acp_codec.go:331–360` 区分 error、cancelled 与其他终态；不能把“收到了终态”一律映射成命令 success。官方业务失败的文字反馈与 RPC/turn 成功也需分开取证。§7 承诺的命令行、压缩副产物和错误呈现，仍需要命令级冷热样本支持。

**修订要求：**给 P1–P8 各列前置状态、真实方法序列、是否调用模型/修改权限/持久状态、所需 fixture、恢复步骤和完成标准。将无需模型的探针与真实 turn 取证分开；未取证依赖项保持阻断，不能仅移到开放项后继续编码。终态至少区分成功、取消、RPC 错误、执行错误；对仍未知的 stop reason 保留原值并明确处理，避免假成功。

## 4. 对核心判断的裁定

| 判断 | 本轮裁定 |
| --- | --- |
| shell 通过 session/prompt 确定性解析 slash | 1.0.16 源码支持；不能据此保证陈旧目录或缓存缺失时也确定性命中 |
| 不应照搬 dsh“命令绝不经 prompt” | 同意；core 与 handler 的 dsh 专属注释也应同步改为分 backend 契约 |
| Grok 没有 /plan | 表述错误；shell 无、pager 有，模式通道选择本身合理 |
| plan_mode.json 支持跨进程恢复 | 源码支持；不足以证明 CMU 到达后立即退出安全，目标版仍待证 |
| hint.isEmpty 与官方 input 完全同构、iOS 只改门控 | 不成立，见 R2/R3 |
| permission plan 当前只写内存 | 确认；但接真必须补 session-scoped 接口与 handler，不能仅改 SetMode 方法体 |
| P1–P8 全部零成本且覆盖开工前提 | 不成立，需拆分探针能力与真实 turn 验证 |

## 5. 复审门槛

1. 先修 R1–R6，给出可实现的接口、状态时序和 iOS 修改清单；尤其删除 dsh fallback 泄漏和缓存缺失放行。
2. 修正 `/plan`、compact hint、目录范围和 pager 交互事实，再进行 D1–D3 产品裁决；本报告不代替 owner 作白名单或权限键决策。
3. 将 Phase 0 改成可执行的证据矩阵；1.0.13 样本不足的依赖项不得宣告已定稿。
4. 定向单测覆盖：跨会话模式隔离、无通知幂等切换、失败不广播成功、重启恢复、外部切换后发送、陈旧 replay、目录空/失败/缺失、compact claim、取消不算成功。

当前无需运行 UI tests 来确认上述问题；源码与现稿已有直接矛盾。修订后再按明确范围完成目标版取证与实施验收。
