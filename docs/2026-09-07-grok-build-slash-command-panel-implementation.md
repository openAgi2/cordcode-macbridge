# Grok Build 斜杠命令面板 + 计划模式官方通道 接入方案

> 状态：**方案 v1.1（2026-09-07，未实施）**。v1.0 经实施评审**不通过**（6 P1 +
> 2 P2），本版按评审报告
> [2026-09-07-grok-build-slash-command-panel-implementation-review.md](2026-09-07-grok-build-slash-command-panel-implementation-review.md)
> 修订；逐项响应见下方「评审响应与修订记录」。dsh 方案 §11 的十轮返工教训
> （尤其 §11.2 ⑤⑧⑨⑩与 §11.3 四拍纪律、§11.4 迁移检查单）仍是本方案的
> **前置必读**。事实基线：调研
> [2026-09-04-slash-command-skill-cross-backend-survey.md](2026-09-04-slash-command-skill-cross-backend-survey.md)
> §7（两轮独立评审通过）。

## 评审响应与修订记录（v1.0 → v1.1）

评审结论 8 项（R1-R8）**全部吸收，无整项不采纳**；其中两处**部分采纳**，理由
随行注明。v1.0 的错误事实（「input 与 dsh 完全同构」「Grok 没有 /plan」
「缓存缺失放行」「CMU 作 ack」「wire 层沿用前缀/单行校验」）均已更正并落文。

| 评审项 | 级别 | 响应 | 落点 |
| --- | --- | --- | --- |
| R1 `SetMode` 接口无 sessionID/ctx/error，handler 先切后查、失败也广播成功 | P1 | **采纳**：新增 core 可选接口 `SessionModeSwitcher`（ctx/sessionID/mode/error）+ handler 优先分支（先校验后调用、失败即 RPC error 不广播）；grokbuild 侧 per-session 状态与互斥串行、超时/取消；测试含跨会话隔离、失败不广播成功 | §5.1、§6.2.1、§8 |
| R2 仅放宽门控会泄漏 dsh `/plan` 回退目录、白名单裁掉真实目录、chip 仍发 `/plan off` | P1 | **采纳**：iOS 改动从「唯一门控」扩为六项清单（白名单 per-backend、回退三态、chip 数据源+action、错误呈现沿现有弹窗）；验收覆盖「List 未返回/报错/空目录」三态 | §5.2、§7 |
| R3 「input 有无与 dsh 完全同构、零改动已被官方证明」被 pager 源码否定（compact 自带 hint；ACP 恒 `has_args:true`） | P1 | **采纳**：撤回「官方同构」结论；`hint.isEmpty` 判决重新定性为 **CordCode 移动端规则**（新增决策点 D4）；grok 上 compact/always-approve 等 hint 非空命令一律 claim（空参直接回车即执行）；矩阵 #2 期望更新 | §4.1.1、§3.2 D4、§7.1、§9 |
| R4 CMU 仅真实转换才发（幂等 set 无通知会挂起）；CMU 到达 ≠ 已落盘 | P1 | **采纳**：settle = `session/set_mode` 的 **RPC response**（`run_loop.rs:737` 处理后必发）；CMU 降级为纯观测；持久化屏障由 P7 定夺（response 后立即退出→reload 是否恢复）；测试含幂等无通知切换 | §4.3、§6.2.2、§8 |
| R5 「缓存缺失放行」让直接 RPC/重启重试绕过防线；initialize builtins 静默冒充全目录；wire 层并无前缀/单行校验 | P1 | **采纳**：List/Execute 双 fail-closed（缓存缺失→拉取→失败即 RPC error，无兜底）；initialize `_meta.availableCommands` 退出 List 来源（仅诊断日志）；`/` 前缀+单行校验由 grok 侧自实现（`handlers_session_commands.go:52` 只查非空）；目录快照与执行的非原子边界明示 | §4.4、§5.1、§6.1 |
| R6 幂等 ≠ 多写者写入顺序；手机陈旧 plan 会经 `_meta.mode` 覆盖外部退出 | P1 | **采纳（方案级重构）**：**删除「每条 prompt 携带 `_meta.mode`」**（移入 §2 不做）；改为单写者模型——写入唯一通道 = set_mode，prompt 恒不带模式信号、直接使用官方继承链（`acp_agent.rs:1113-1129` prompt 无 mode 查 resident + `spawn.rs:625-648` 从持久化 tracker 恢复）；observed（CMU 三路）只展示永不回写；定义 replay/live 去重排序；P4 改证「无 mode 继承」这一承重事实 | §2、§4.3、§6.2.3、§0.1 P4 |
| R7 「shell 无 /plan」被扩大成「Grok 无 /plan」；ACP 目录与 TUI `/` 补全混淆；D1 与 §6.1 范围不一 | P2 | **采纳**：更正为「shell ACP 目录无 `/plan`、不得当文本发；官方 pager **有** pager-local `/plan`（`plan.rs:1-31`，SetPlanMode/EnterPlanMode），需转换为模式动作」；定义三个集合（shell 可执行目录 / pager-local 动作 / iPhone 产品白名单）；D1 与 §6.1 统一为「ACU/list 官方全目录，D1 只裁决是否再收窄」；§9 #1 口径更正。**部分采纳**：逐命令四拍展开不执行，理由见下 | §0.2、§3、§4.2、§9 |
| R8 零 API fake client 覆盖不了 P4/P6（需真实 turn、可能调模型）；「收到终态」≠ 成功 | P2 | **采纳**：§0.1 改为证据矩阵（前置状态/方法序列/模型调用/副作用与复位/完成标准），P4/P6 标真实 turn；settle 四分法（成功/取消/RPC 错误/执行错误，未知 stopReason 保留原值 fail visible）；样本不足的依赖保持阻断 | §0.1、§4.4、§6.1、§8 |

**R7 部分采纳说明（不逐命令展开四拍）**：评审要求「每条最终纳入命令补齐四拍及
副产物」。本方案不执行逐命令展开，理由：CordCode 侧对全目录命令**没有
per-command 分支**（唯一特化命令 `loop` 已在 §2 明确不做特殊面），所有命令的
交互形态只分 claim / 点选执行两类，且这两类已由 D4 移动端规则 + §7.1 统一四拍
走查完整覆盖；逐命令展开是同一模板重复 16+ 次，不产生新信息。副产物类（compact
的上下文用量变化等）在 §7.1 第 4 拍统一覆盖，P6 对 compact/always-approve 两条
做真实取证。若 D1 终表纳入出现**新交互形态**的命令（如 workflow 触发），届时按
四拍补该单条走查——这是对评审要求的范围收窄，不是免除。

**R2 错误呈现的处置（事实采纳 + 范围声明）**：评审确认现状是
`presentSlashCommandErrorAlert` 弹窗，v1.0「inline 不弹窗」声称**撤回**。处置 =
grok 沿用现有弹窗机器（dsh 已交付、owner 已验收的形态）；把错误反馈改成 inline
是跨 backend 的统一 UI 改动，不属于本项，另案。

## 0. 来源清单（P0）与开工门

| 仓库 | 工作树 | 分支 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| grok-build（上游，只读） | `/Users/jacklee/Projects/grok-build` | detached @ `72a61251` | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9`（1.0.16） | 干净 |
| **目标运行版本**（本机安装二进制） | `~/.grok/bin/grok` | — | **1.0.13（自报 `5e9a58528b76`，不在 checkout 历史中）** | — |
| cordcode-macbridge | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（本方案工作树） | `plan/approval-layer` | `73dd653`（= `de17e6f` + 本方案文档提交，产品源码与 `de17e6f` 零差异） | 干净（本文档与评审报告除外） |
| cordcode-ios | `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 |

来源注记：v1.0 调研期锚点在 `a04095e`（本 worktree）读取、`fbb4940`（main
checkout）复核零差异。**v1.1 修订轮（2026-09-07）**：评审报告引用的全部锚点已
在本轮逐条复核属实——macbridge `core/interfaces.go:897`、
`go-bridge/handlers.go:4532`、`handlers_session_commands.go:52`；iOS
`SlashCommandRouting.swift:11`/`:32-38`、`ChatUIKitContainerView.swift:6583`/
`:6682`；grok `slash_commands.rs:66-76`、pager `acp_command.rs:147-164`、
`slash/mod.rs:1407-1435`、pager `slash/commands/plan.rs:1-31`、
`session_mode.rs:44-113`、`run_loop.rs:737`、`persistence.rs:1945`、
`acp_agent.rs:1113-1129`、`spawn.rs:625-648`（iOS 锚点读自 main checkout，实施
若换 iOS 工作树须按门点规则重新登记）。

执行本方案前，三个门点（读源码分析前 / 首次改文件前 / 构建前）必须按
CLAUDE.md P0 重新生成本清单；本文锚点行号以 grok-build @ `72a61251` 与
macbridge @ `de17e6f` 为准，换基线须逐条复核。

### 0.1 版本漂移门：Phase 0 证据矩阵（本方案特有的 P0 前置）

think.md 2026-09-02 条目已证明：**checkout ≠ 目标二进制**（安装版 1.0.13 的
commit 不在 checkout 历史）。本方案锚点在 1.0.16 源码上核验；**编码开始前必须
先在安装版 1.0.13 上活体取证**。零 API 探针方法（fake client：`grok agent
stdio` + 只发 initialize/session/load 等非 turn 方法，不产生模型调用）**只能
覆盖 P1/P2/P3/P5/P7/P8**；**P4/P6 必须真实 turn**（fake client 无法阻止后端调
模型；compact 的压缩本身可能调用模型）——评审 R8，成本与副作用逐项列出，执行
前向 owner 报备。**样本不足的依赖项保持阻断，不得移到 §11 开放项后继续编码。**

| # | 取证项 | 前置与方法序列 | 模型调用 | 副作用与复位 | 决定什么 |
| --- | --- | --- | --- | --- | --- |
| P1 | initialize `_meta.availableCommands` 真实形状（字段名/`input` 嵌套/camelCase） | fake client：`grok agent stdio` → initialize | 无 | 无 | 中间解码类型。注：R5 后它**已不是 List 来源**，取证降级为解码正确性验证 |
| P2 | session/load 后 `available_commands_update` 全形状（builtins+skills+workflows 顺序、`_meta`、skills 的 `qualified_name`/`plugin_name` 类字段） | fake client → initialize → session/load | 无 | 无 | 全目录解码与展示字段 |
| P3 | catalog 单例子进程上调 `x.ai/commands/list {cwd}` 的响应与 cwd scoping | 进程级 catalog 子进程（`--no-leader stdio`）→ initialize → ext list | 无 | 无 | 冷会话/缓存缺失时 List 通道是否成立（fail-closed 的拉取腿） |
| P4 | **（真实 turn）prompt 不带 `_meta.mode` 时的模式继承**：置 plan → 退出子进程 → 新子进程 load → 发一条不带 mode 的普通 prompt | 依赖 P7 先置 plan；观察该 turn 是否按 plan 行为（先出计划）且不意外退出 | **有**（一条 turn） | 会话内多一条 turn；用可弃会话 | **单写者设计的承重事实**（R6）：官方继承链（spawn 恢复 + prompt 无 mode 查 resident）在 1.0.13 成立，prompt 永不携带 mode 也不会丢模式 |
| P5 | `current_mode_update` 在子进程 stdout / leader gateway 广播 / `updates.jsonl` 三路的形状，**含冷重放条目是否带 replay/live 标记** | P7 顺带捕获 stdout 路；leader 路订阅 gateway；冷重放直接读 `updates.jsonl` 文件 | 无 | 无 | 观测三路消费 + replay/live 去重排序规则定稿（§6.2.3） |
| P6 | **（真实 turn）builtin 执行的 settle 证据**：`/compact`、`/always-approve` 各执行一次，记录终态 stopReason、是否产生 user message 行、行形状、可透传反馈文本 | 真实会话中发命令行 | **有**（compact 压缩可能调模型） | **always-approve 改权限状态，测后必须切回**；compact 压缩不可逆，用可弃会话 | settle 四分法映射（§6.1）+ 命令行投影形状 |
| P7 | mode-only 子进程 `session/load` + `session/set_mode` 的 ack 与持久化：记 **RPC response** → 立即退出 → 新子进程 load 读回模式；重复 plan→plan（幂等）、plan→default→default | mode-only 短命子进程若干轮 | 无 | `plan_mode.json` 变化；测后切回 default | response 即 settle（幂等无 CMU 也能完成，R4）+ **response 后立即退出是否存在持久化丢失窗口**（决定退出前是否等待，§6.2.2） |
| P8 | `summary.json` 是否持久化当前 session mode（已知有 `current_model_id`/`current_effort_id`） | 直接读会话目录文件 | 无 | 无 | 冷启动 chip 恢复是否需要额外拉 `updates.jsonl` 尾部 |

任何一项活体形状与 1.0.16 源码冲突时，**以 1.0.13 活体为准**并在实现说明里记录
漂移（dsh §11.2 ① 教训：声明契约会漂，请求形状以活体探测收口）。

### 0.2 开工源码优先门

1. 上游只读 checkout `72a61251`；目标二进制 1.0.13。动手改 `agent/grokbuild` /
   `go-bridge` / iOS **之前**，至少读并在实现说明里写下符号锚点（§4 已给全，逐条
   对号）。
2. 官方 UI 层（pager）交互判决必须读。**事实更正（v1.0 表述有误，评审 R7）**：
   官方 pager **有** pager-local `/plan [description]`（`plan.rs:1-31`：裸命令 →
   `SetPlanMode(On)`、带描述 → `EnterPlanMode`）；TUI 的 `/` 补全清单 = shell
   可执行目录 **∪ pager-local 命令**，与 ACP 目录**不逐一等同**（§9 #1 对齐
   口径）。交互语义在 UI 源码不在 RPC schema（dsh §11.2 ⑤ 教训）。
3. CordCode 只做 bridge 接线：官方 JSON 用中间类型解码再映射；`core.SessionCommandCatalog`
   的 dsh 实现是**先例不是真值**——dsh 的「命令绝不经 SendMessage」「input 缺席
   点选即执行」都是 dsh 官方语义，在 grok 分别不成立/不成立（grok 命令就是
   prompt 文本；input 只决定 placeholder）。实施时把 core 与 handler 里的 dsh
   专属注释同步改为分 backend 契约（评审 §4 裁定，Phase 1 交付物）。
4. 测试 fixture 必须来自 §0.1 活体样本或官方仓 fixture；手写 fake server 只验证
   内部行为。
5. 违反本门 = 本方案未执行，评审按未通过。

## 1. 目标

1. iPhone **Grok Build 会话**的 `＋` 菜单出现「命令」节（与 dsh 同入口形态，
   dsh 方案 §12 已把 `/` 按钮合并进 ＋ 菜单）：列出该会话官方**可执行目录**
   （builtins + skills + workflows，availability 已被官方过滤），点选按 D4 移动端
   规则分流执行。
2. **Plan 模式 iOS 入口接官方通道**：think.md「Grok iOS Plan 只写 agent 内存」
   的现状终结——模式**写入唯一通道** = 官方 `session/set_mode`（经新增
   `SessionModeSwitcher` 接口，§6.2）；**prompt 恒不携带 `_meta.mode`**（§2/R6，
   直接使用官方继承链）；模式变化经官方 `current_mode_update` 三路回流 iPhone
   展示（Plan chip，对齐 dsh 已交付形态）。
3. 已交付的 plan 审批卡（`plan_review`，leader 广播 `x.ai/exit_plan_mode`，
   think.md §25）继续工作，本项不改审批语义。

## 2. 明确不做（第一期）

| 项 | 理由 |
| --- | --- |
| **prompt 携带 `_meta.mode`**（v1.0 曾作「收敛兜底」） | R6：手机缓存陈旧时携带 mode 会经官方 `reconcile_plan_mode_with_prompt`（`session_mode.rs:191-216`，对传入值直接 set）覆盖外部切换——Mac TUI/审批流刚切回 default，手机旧 plan 又把会话拉回 plan。官方已有完整继承链（`plan_mode.json` + `spawn.rs:625-648` 恢复 + `acp_agent.rs:1113-1129` prompt 无 mode 查 resident），**单写者（仅 set_mode）+ 纯观测（CMU）**消除整类多写者竞争 |
| **initialize `_meta.availableCommands` 作 List 兜底**（v1.0 曾作 builtins 兜底） | R5：builtins-only 冒充全目录是静默降级，掩盖两条全目录通道的真实失败；List 改 fail-closed（§6.1），该字段解码仅留诊断日志 |
| 键入 `/` 自动补全弹层 | owner dsh 裁决沿用：只要菜单入口 |
| prompt-only 命令 `loop` 的 loop_fire_mode 特殊面 | `PROMPT_COMMANDS` 特例重写 blocks + displayText，产品面窄；目录展示但走通用通道，特化另案 |
| workflow 进度投影（`workflow_projection`） | 命令进目录、可执行；workflow 专属过程投影是独立官方 surface，另案（对齐 dsh §11.2 ④ 教训，不合并塞进本项） |
| skills 的 `kind:"chat"` 产品通道 | `x.ai/commands/list {kind:"chat"}` 是 Grok Chat 产品 lane，CordCode 不消费 |
| MCP elicit / `x.ai/queue/*` interjection | think.md：interjection 后置 Phase B |
| 合成全 backend Plan 按钮 | think.md「iOS 进入计划模式」行 owner 明令禁止；Grok Plan 入口必须是 grok 官方通道 |
| 客户端自行合并/去重三条目录通道的结果 | 官方顺序（builtins-first）与 availability 过滤都是官方事实；客户端合并=自造目录（§4.1） |
| `permission_mode` 其余 5 键接真 | Phase 2 只接 `plan`↔SessionMode，其余 permission 键的官方通道（ACP permission 与 SessionMode 是两套体系）另案裁决 |
| 目录刷新 UI（下拉重拉等） | 冷拉一次 + turn 期 ACU 增量已覆盖官方语义；手动刷新自造 |

## 3. Owner 决策

### 3.1 已锁定（既往裁决，直接生效）

| # | 来源 | 裁决 |
| --- | --- | --- |
| L1 | think.md「iOS 进入计划模式」行 | 三条 Plan 入口分别接各自官方通道，**禁止合成全 backend 按钮**；Codex 入口挂起、DSH 已交付、Grok 即本方案 |
| L2 | think.md §25 | plan 审批两键卡程度可接受；完整体验属跨 backend 通用另案，本项不动 |
| L3 | dsh 方案 §11.1 #3（2026-09-05 owner 裁决） | 面板白名单收窄**是 dsh 专属裁决**（dsh 只做 compact/goal/plan 三条）；对 grok **不是既成事实**——grok 的产品白名单是本方案 D1 的待裁决项，iOS 白名单机制必须 per-backend（§5.2），不得把 dsh 三条套到 grok |
| L4 | dsh 方案 §12 | 命令入口 = ＋ 菜单「命令」节（不是独立 `/` 按钮） |

### 3.2 本方案待 owner 裁决（开工前问一次，一次问完）

| # | 决策点 | 推荐 | 备选 |
| --- | --- | --- | --- |
| D1 | iPhone 产品白名单（三集合中的 C 集，作用于官方全目录的**展示**过滤） | **不收窄**：面板全上官方可执行目录（ACU/list 的 builtins+skills+workflows，availability 已官方过滤）；与 v1.0「仅 builtins」表述统一为本行——v1.0 的 D1 与 §6.1 范围不一致是评审 R7 指出的矛盾，本版统一 | 对齐 dsh 只留少数命令的最小面（须逐条点名） |
| D2 | Plan 入口位置与动作 | **输入框 Plan chip**（对齐 dsh §11.1 #1 已交付形态）；点击 = `set_permission_mode`（模式通道，**不发 `/plan` 文本**——v1.0 的 chip 点击沿 dsh 机器发 `/plan off`，R2 指出对 grok 是禁发文本，Phase 3 必须改 action） | ＋ 菜单「计划模式」行 |
| D3 | `permission_mode` capability 的空转处置 | Phase 2 落地时把 `plan` 键从 `PermissionModes()` 挪到 SessionMode 通道，其余 5 键**暂留现状**（既有行为不变） | 全部摘除直到各自接真 |
| D4 | 带 hint 命令（含 compact、always-approve 等绝大多数 builtins）的移动端交互 | **统一 claim**：点选 → 认领输入框 `/name ` + 官方 hint 幽灵提示，空参直接回车即执行（官方 optional-args Enter=Executes 语义）；零 per-command 特判 | 对个别「常用无参」命令点选即执行（per-command 硬编码特判，不推荐：协议层无法区分参数必需/可选——`args_required` 不在 ACP 目录下发） |

## 4. 官方不变量（上游核验 @ `72a61251`，实施必须镜像）

### 4.1 命令目录：三条官方通道，各司其职

| 通道 | 时机 | 内容 | 锚点 |
| --- | --- | --- | --- |
| `initialize` 响应 `_meta.availableCommands` | 握手后、session 前 | **仅 builtins**（availability 过滤；pre-session 只能算 config 类 gate） | `acp_agent.rs:543` + `slash_commands.rs:837-851`（`AvailableCommand::new(name, description).input(UnstructuredCommandInput::new(hint))`，`input` 为 Option） |
| `sessionUpdate.available_commands_update` | session/new 或 session/load 后 | **builtins + skills + workflows**，builtins 在前，cwd-scoped | `session_setup.rs:247`；`slash_commands.rs:445` ACU `_meta` 带已注册工具名 |
| ext 方法 `x.ai/commands/list` | pull | `{sessionId?, cwd?, kind?}` → `{commands:[AvailableCommand], tools?}`，camelCase；`kind:"chat"` 是产品 lane 不用 | `slash_commands.rs:852-871` |

不变量：

1. **`input`/hint 的官方语义只是 placeholder**（v1.0 表述更正，评审 R3）：pager
   对所有 ACP 目录命令恒 `has_args: true`，注释原文「The `input` field only
   determines the placeholder hint, not whether args are allowed」
   （`acp_command.rs:147-164`）；发送完整性按 `takes_args`/`args_required`
   两 bit 判断（`slash/mod.rs:1407-1435`：optional args 时空参 Enter=Executes），
   而 `args_required` **不在 ACP 目录下发**。因此官方**不存在**「input 缺席 →
   点选执行」的交互判决——那是 dsh 的官方语义。CordCode 的
   `hint.isEmpty → execute / 否则 claim` 是**自定的移动端规则**（D4），在 grok
   上的实际效果：compact（官方 hint「optional context about what to preserve」，
   `slash_commands.rs:66-76`）、always-approve（「on|off」）等 hint 非空命令
   **一律 claim**，空参直接回车即执行；仅当目录中存在真正无 input 的命令才
   点选即执行。iOS `SlashCommandSelection.route` **代码零改动**，但其依据从
   「官方同构」改标「移动端规则」，单测断言同步改注释。
2. availability 过滤是**官方做的**（`builtin_commands(availability)` filter gate；
   ACU 同理）。CordCode 拿到什么列什么，**不得**客户端再过滤或合并三条通道
   （D1 若收窄是 owner 产品裁决的 C 集，性质不同且须记录终表）。
3. CordCode 现状：`initializeMeta` 只解 `modelState`（`acp_types.go:102-115`），
   ACU 在 codec **known-drop** 清单里（`acp_codec.go:326-330`）——两条通道都已
   在收包路径上，只是没解。

### 4.2 执行 = 命令行作为 prompt 文本（与 dsh 相反的官方语义，全方案最关键差异）

grok 官方执行通道就是 `session/prompt` 发 `/cmd args` 文本；shell 侧
`resolve_human_intent`（`slash_commands.rs:1574-1644`）**确定性**解析（非模型
中介），四层判决：

```text
HumanIntent 前缀解析 → ① PROMPT_COMMANDS(loop) 特化重写
                    → ② builtin（name|alias 命中 + gate 允许）→ BuiltinAction
                    → ③ skill 引用解析 → InvokeSkill 信封
                    → ④ workflow 目录命中 → WorkflowLaunch
                    → 都不中 → Ok(prompt_blocks)：作为普通 prompt 交给模型
```

不变量：

1. **目录内命令的执行是确定性的**——这就是 grok 官方对「模型把 /plan 当聊天回」
   的防法。core `SessionCommandCatalog` 注释里「绝不经 SendMessage」的 dsh 理由
   在 grok 不成立；grok 的等价防线是**只允许目录内命令走 execute 通道**（白名单
   语义），自由文本 `/` 整行仍走普通发送（落入 ④ 是官方预期行为，不是 bug）。
2. **命令在官方投影里就是 user message**（dsh 的「命令不是 user message」在
   grok 相反）。CordCode 不合成 user 气泡、不合成 CommandRow——transcript 由
   官方历史/polling 自然出现命令行（P6 取证确认 1.0.13 上的实际行形状）。
3. **三个命令集合必须分开**（评审 R7）：**A = shell 可执行目录**（上文三条
   通道下发，`session/prompt` 文本确定性解析）；**B = pager-local 动作**（如
   `/plan [description]`——`plan.rs:1-31`：裸 → `SetPlanMode(On)`、带描述 →
   `EnterPlanMode`；`/view-plan` 等。它们**不在 A**，`session/prompt` 收到
   `/plan` 文本落入 ④ 当普通聊天，**官方 fail closed 于「不进 Plan」**）；
   **C = iPhone 产品白名单**（D1 裁决，作用于 A 的展示过滤）。iPhone 的 Plan
   入口必须转换为模式动作（§6.2），**绝不能把 B 类命令当文本发**；模型/effort
   走既有 catalog 通道（`_meta` on session/new）。
4. BUILTIN_COMMANDS 16 条（`slash_commands.rs:66`）：compact、always-approve
   （yolo）、flush、dream、memory、context、hooks-*、plugins、reload-plugins、
   session-info、feedback、deep-research、workflow、goal 等——**shell 目录没有
   `plan`**：plan 在官方是 SessionMode + pager-local 命令，两者都不经
   `session/prompt` 文本。这是与 dsh 最大的表面差异（dsh 的 plan 是命令，grok
   的 plan 是模式）。TUI `/` 补全清单 = A ∪ B，**不与 A 逐一等同**（§9 #1）。

### 4.3 模式（SessionMode）：独立于 permission 的官方体系

| 事实 | 锚点 |
| --- | --- |
| SessionMode 枚举 `default`/`plan`/`ask`（snake_case，未知→Default） | `xai-grok-tools/src/types/session_mode.rs` |
| `session/set_mode` 需要 resident session handle；**RPC response 在处理完后必发**（`handle_session_mode` 后 `responds_to.send(())`）——这是与 CMU 无关的独立 ack | `acp_agent.rs:2229-2250`、`run_loop.rs:737` |
| **CMU 仅在真实转换时 enqueue**：set plan 时 `enter_pending()` 返回 `entered=false`（已在 plan）则不 enqueue 也不 persist——**幂等 set 成功但无 CMU**（R4：等 CMU 作 ack 会挂起） | `session_mode.rs:44-113` |
| **持久化是异步的**：`persist_plan_mode_state()` 只向 persistence 队列 send，写盘在另一任务执行——「response 到达」≠「已落盘」 | `session_mode.rs`（persist 入队）、`persistence.rs:1945` |
| **prompt 无 mode 的官方继承链**：`_meta.mode` 缺席时向 resident actor 查当前模式；子进程 spawn 时从持久化 plan_mode tracker 恢复 `current_prompt_mode`——客户端不携带 mode 也不会丢模式（R6 单写者设计的承重事实，P4 在 1.0.13 验证） | `acp_agent.rs:1113-1129`、`spawn.rs:625-648` |
| `session/prompt._meta.mode` 存在但**是双刃剑**：`reconcile_plan_mode_with_prompt` 注释自称「the only signal the client sends…idempotent」，但对传入值**直接 set**（`session_mode.rs:191-216`）——陈旧值会覆盖外部切换。本方案不用它（§2/R6） | `session_mode.rs` |
| PlanModeState 机：Inactive→Pending→Active→ExitPending；**持久化到会话目录 `plan_mode.json`，resume 恢复，`awaiting_plan_approval` 同样持久化** | `plan_mode.rs` |
| `CurrentModeUpdate(SessionModeId)` 通知在真实转换时发出；持久化进 `updates.jsonl`（session replay）并转发 gateway | `updates.rs:348`、`notification_bridge.rs:201` |
| `x.ai/exit_plan_mode` 批准 → notification_bridge 回发 CurrentModeUpdate 恢复 default | `notification_bridge.rs`（§25 已交付消费端 `leader_subscriber.go:680`） |

推论（对 CordCode per-turn 子进程模型至关重要，v1.1 重写）：**模式跨进程持久**
（plan_mode.json）+ **官方继承链完备**（prompt 无 mode 自动继承持久化模式）→
CordCode 不需要常驻子进程就能**切换**（mode-only 子进程 load 后 set_mode）与
**观测**（CMU 三路）；也**不需要**在 prompt 上携带 mode——单写者（set_mode）+
官方继承 + 纯观测即可，且消除多写者竞争（§6.2.3）。

### 4.4 codec：fail-open + 终态三分

grok codec 对未知 sessionUpdate 是 fail-open（`acp_codec.go:362-369`：Debug 日志
+ return nil，不 reset、不报错）。dsh 方案 Phase 1 的「未知事件 P0 发布阻断」在
grok **结构性不存在**；本方案的 codec 工作是把 `available_commands_update` /
`current_mode_update` 从 known-drop **升级为消费**（§6.3），fail-open 默认保持。

另（评审 R8）：codec 现有 `turn_completed` 终态处理是**三分**的（
`acp_codec.go:331-360`：error → EventError、cancelled → EventResult"cancelled"、
其他 → EventResult）——命令 settle 映射必须沿用这个区分，**cancelled 不算成功**，
未知 stopReason 保留原值（§6.1）。

## 5. CordCode 现状与差距（macbridge `de17e6f` + iOS `c3b1d5b0`）

### 5.1 Mac（`agent/grokbuild/`）

| 现状 | 锚点 | 差距 |
| --- | --- | --- |
| `SetMode(mode string)` 只写 agent 级内存，无 sessionID/ctx/error；无任何 wire 调用 | `core/interfaces.go:897`、`grokbuild.go:573-583` | **三重缺口**（R1）：接口无法指定会话/无法报官方失败；handler `handleSetPermissionMode` **先 SetMode 后查 SessionID**、随后读 `GetMode()` 广播成功（`handlers.go:4532` 邻域）——官方失败也会广播成功；`a.mode` 是 agent 级，两会话共享。Phase 2 新增 `SessionModeSwitcher` 接口 + handler 优先分支（§6.2.1） |
| `PermissionModes()` 返回 6 条，含 `{Key:"plan"}` | `grokbuild.go:585-593` | `permission_mode` capability 已广告（`backend_capabilities.go:44-46`）但 plan 键无效——诚实缺口，Phase 2 接真（D3 迁移） |
| wire 层 `execute_session_command` 的 line 校验**只查非空**（sessionID/line 两项） | `handlers_session_commands.go:52` 邻域 | v1.0 误称「沿用 wire 层 `/` 前缀+单行校验」——**该校验不存在**；grok 侧须自实现并单测（§6.1，评审 R5） |
| `sessionPromptParams` 仅 `{sessionId, prompt}`，无 `_meta` | `acp_types.go:240-243` | **保持无 `_meta`**（§2/R6：prompt 恒不携带模式）——v1.0 曾计划加 `_meta.mode`，已撤销 |
| codec 把 `session_info_update`/`current_mode_update`/`available_commands_update`/`config_option_update` 归入 known-drop | `acp_codec.go:326-330` | ACU/CMU 需升级为捕获与解码（§6.3） |
| `initializeMeta` 只解 `modelState` | `acp_types.go:102-115` | `availableCommands` 解码仅作**诊断日志**，不作 List 来源（R5，§6.1） |
| plan 审批卡已交付（`plan_review`，actions approve/requestChanges/quit） | `leader_subscriber.go:680` | 无差距，复用 |
| 进程级单例 catalog 子进程现用于 session/list + 模型目录 | `catalog_session_list.go` | P3 取证后复用作 `x.ai/commands/list` 拉取通道 |
| core `SessionCommandCatalog` 接口 + `SessionCommand{Name,Description,Hint}` / `SessionCommandResult{CommandID,ResultKind,ResultText}` | `core/session_commands.go:10-40` | grokbuild 实现之（映射见 §6.1）；接口注释的 dsh 专属理由改分 backend 契约（§0.2.3） |
| wire RPC `list_session_commands` / `execute_session_command` 已在 handlers（dsh 期交付） | `go-bridge/handlers.go:1642-1644`、`bridge_v1_schema.go:46` | 零新增 RPC |

### 5.2 iOS（v1.1 重写——评审 R2：改动不是「唯一门控」，共六项）

| # | 现状 | 锚点 | 差距 |
| --- | --- | --- | --- |
| 1 | ＋菜单「命令」节门控**硬编码** `currentInputBackendKind == .deepSeekWeb` + `supportsSessionCommands` + 有 session | `ChatUIKitContainerView.swift:4565-4571` | 放宽为 capability 驱动（去掉 kind 硬门） |
| 2 | `SlashCommandPanel.commandWhitelist`（compact/goal/plan）**无条件**过滤所有 backend 的目录 | `SlashCommandRouting.swift:11` | 白名单改 **per-backend**：仅 deepSeekWeb 套用（L3 是 dsh 专属裁决）；grok 按 D1（推荐不套用）。否则 grok 被 dsh 三条裁掉全目录，D1「不收窄」无法实现 |
| 3 | `AttachMenuPlanner.effectiveCommands` 对未加载/失败/空目录回退**内置三条（含 `/plan`）** | 同文件 | 回退三条**仅对 deepSeekWeb 生效**；grok 三态真实呈现：加载中（占位/禁用）、List 失败（错误提示+可重试）、官方空目录（空态文案）。**绝不显示 dsh `/plan`**——那正是方案禁止发送的文本 |
| 4 | Plan chip 刷新**仅限 deepSeekWeb**；点击 action = `executeHostCommandLine("/plan off")` | `ChatUIKitContainerView.swift:6583`、`:6682` | chip 数据源扩到 grok 模式状态（§6.3 会话状态通道）；grok 会话点击 = `set_permission_mode`（模式通道），**不发文本**；dsh 路径不动 |
| 5 | 通用执行失败走 `presentSlashCommandErrorAlert` **弹窗** | `ChatUIKitContainerView.swift:6670` 邻域 | **沿用现有弹窗**（v1.0「inline」声称撤回，见评审响应表）；改 inline 是跨 backend 另案 |
| 6 | `SlashCommandSelection.route(for:)` generic over `BackendSessionCommand`：`hint.isEmpty` → execute；否则 claim | `SlashCommandRouting.swift:32-38` | **代码零改动**；判决依据重新定性为移动端规则（D4）：grok 上 compact 等 hint 非空命令全部 claim，单测注释同步 |

## 6. Wire 设计

### 6.1 `SessionCommandCatalog` 的 grok 实现（Mac）

**List（fail-closed，两条官方通道，不合并、无兜底）**：

- 通道 1（turn 期）：codec 捕获 `available_commands_update`（§6.3）写入 agent
  per-session 目录缓存。缓存身份 = `(backend, sessionID, cwd, fetchedAt)`；ACU
  到达即**整表替换**（官方语义：ACU 就是该时刻的全目录）。
- 通道 2（冷/缓存缺失）：进程级 catalog 单例子进程带会话 cwd 调
  `x.ai/commands/list {cwd}`（P3 取证；不可行则退化为 per-turn 子进程
  handshake + session/load 后读 ACU）。
- **两通道都失败 → RPC error**（iOS 呈现 §5.2-3 的错误态）。**无兜底**：v1.0
  「initialize builtins 兜底 + UI 不区分来源」删除（R5：builtins-only 冒充全
  目录是静默降级，掩盖真实失败）。initialize `_meta.availableCommands` 解码仅留
  Debug 日志供诊断。
- 映射：`Name`/`Description` 顶层；`Hint` ← `input.hint`（`input` 缺席 → `""`）；
  解码走中间 wire 类型（`input` 用指针/omitempty）。skills/workflows 条目的
  额外字段（P2 取证）第一阶段丢弃。
- **非原子边界（明示，不掩盖）**：目录快照与执行不原子——快照后 skill 被删/
  availability 变化，execute 白名单仍命中但官方 resolve 落 ④ 普通 prompt。这是
  官方行为（「曾在目录」≠「执行时命中」），不是 CordCode 失败；不做「执行时
  二次拉取」（那同样非原子，徒增延迟）。

**Execute（官方 prompt 通道，§4.2 差异已声明）**：

- `ExecuteSessionCommand(ctx, sessionID, "/compact")` = 复用现有 per-turn
  `session/prompt` 管道发送该行（与 SendMessage 同 child 生命周期、同 turn-end
  检测），**不**本地合成 user 气泡、**不**挂 `_meta.mode`、不带附件。
- **line 校验（grok 侧自实现，wire 层只有非空检查）**：以 `/` 开头、单行；
  未过 → RPC error「invalid command line」。
- **白名单（fail-closed，v1.0「缓存缺失放行」删除）**：命令名必须命中该会话
  fresh 目录缓存；**缓存缺失 → 先走通道 2 拉取；拉不到 → RPC error
  「catalog unavailable」**，不直接发 prompt（R5：放行会让直接 RPC / bridge
  重启后重试绕过唯一防线；官方确定性解析只对目录内命令成立）。
- **settle 四分法（R8，P6 定稿）**，沿用 codec 现有终态三分（§4.4）：
  `turn_completed` stopReason = `error` → Go error（`ResultKind:"error"`）；
  = `cancelled` → Go error「command cancelled」（**不算成功**）；其他已知正常
  终态 → `ResultKind:"success"` + `ResultText`（P6 证实有可透传文本则映射，
  否则空串——官方 builtin 的用户可见反馈走会话投影）；**未知 stopReason →
  保留原值进错误信息**，fail visible，不映射 success。禁止把成功折叠成无反馈
  （dsh §11.2 ② 教训）——「过程展示」由 §7 第 4 拍的官方形态承接。

### 6.2 模式通道（Phase 2，v1.1 重设计——评审 R1/R4/R6）

1. **core 新增可选接口**（R1；现有 `ModeSwitcher` 保留给其他 backend）：

   ```go
   // SessionModeSwitcher is an optional interface for agents whose permission
   // modes are per-session state driven through the backend's official
   // channel. Implementations must fail the call on official rejection
   // (no memory-first mutation + fake success broadcast).
   type SessionModeSwitcher interface {
       SetSessionMode(ctx context.Context, sessionID, mode string) error
       GetSessionMode(ctx context.Context, sessionID string) (string, error)
   }
   ```

   handler `handleSetPermissionMode` 新增**优先分支**（type-assert 顺序：
   `SessionModeSwitcher` → legacy `ModeSwitcher`）：先校验 sessionID 非空、mode ∈
   {plan, default}，再调用（**顺序与现状相反**——现状 `handlers.go:4532` 邻域先
   `SetMode` 后查 SessionID）；返回 error → RPC error，**不广播成功**；成功 →
   以请求值下发会话模式 patch（这是官方 resident actor 已处理完成的真实状态，
   非本地假造），CMU 观测到达后同值幂等覆盖。grokbuild 实现：per-session 模式
   缓存 map + per-session 互斥（同会话切换串行，跨会话并行）；agent 级 `a.mode`
   不再被新路径读写。冷启动 `GetSessionMode` 缓存未命中 → 空（iOS 视为未知，
   由 P8/观测收敛；不伪造）。
2. **切换执行**：mode-only 短命子进程：handshake → `session/load`（既有路径，
   cwd 必带）→ `session/set_mode {modeId:"plan"|"default"}` → **settle =
   RPC response**（`run_loop.rs:737`：处理完必发；幂等 set 无 CMU 也由 response
   settle，R4）→ 退出。超时 ctx（初稿 30s，与现有子进程预算对齐）；ctx 取消即
   杀子进程。**持久化屏障**：response ≠ 已落盘（persistence 异步，
   `persistence.rs:1945`）——P7 验证「response → 立即退出 → 新子进程 load」是否
   恢复；若证实有丢失窗口，退出前等待 CMU（仅真实转换有）或短延迟，P7 结果
   定稿。残差（response OK 但写盘失败）：**软失败**——模式未生效，chip 由观测
   收敛，无状态污染（单写者模型下 prompt 不携带 mode，不会用陈旧值回写）；
   不做自动重试补偿。
3. **单写者 + 观测模型**（R6）：
   - **authoritative** = shell 持久化模式（`plan_mode.json`；spawn 恢复；prompt
     无 mode 时官方查 resident 继承）。
   - **写入唯一通道** = 本节 set_mode；**prompt 恒不带 `_meta.mode`**（§2）→
     CordCode 永不覆盖官方继承 → 外部切换（Mac TUI、审批 approve/quit）不可能
     被手机陈旧缓存回写。「何时必须覆盖官方继承路径」的答案：**从不**——我们
     直接使用官方继承路径（这正是它存在的意义）。
   - **observed** = CMU 三路（§6.3）→ chip 展示，**永不回写** authoritative。
   - **外部切换优先级**：任何外部写入都是 authoritative；手机只收敛显示。
   - **replay/live 去重排序**：同一会话内 last-wins（按到达序）；`updates.jsonl`
     冷重放条目若带 replay 标记则不覆盖 live 已观测值（P5 取证标记形状后定稿；
     若 1.0.13 无标记，冷重放仅作初始值——启动后 live 优先）。
4. **`ask` 模式 / `plan` 键迁移**：ask 不暴露（D2/L2 另案）；`PermissionModes()`
   去掉 plan 条目，其余 5 键维持现状（D3）。

### 6.3 codec 升级（Mac，Phase 1/2 各一半）

| update 类型 | 现状 | 改为 |
| --- | --- | --- |
| `available_commands_update` | known-drop | 解码（P2 样本形状）→ **整表替换** per-session 目录缓存（身份与刷新见 §6.1）；不产 timeline 事件 |
| `current_mode_update` | known-drop | 解码 SessionModeId → 会话级模式状态（observed）→ 经既有会话状态通道下发 iOS（对齐 dsh `planMode` 快照/patch 先例）；三路去重排序见 §6.2.3；不产 timeline 事件 |
| `config_option_update` | known-drop | 维持（第一阶段无消费面） |
| 未知类型 | fail-open Debug | 维持 fail-open（§4.4） |

外部 turn（Mac TUI 切模式/跑命令）可观测性：`CurrentModeUpdate` 官方会转发
gateway（§4.3）→ leader_subscriber 增 `current_mode_update` 分支汇入同一状态；
冷重放走 `updates.jsonl` tailer 既有路径。三路汇同一身份（dsh §11.2 ⑧ 冷热
同源教训）。

### 6.4 协议同步

零新 RPC、零新 capability（`session_commands` 复用 dsh 期定义）。`docs/protocol/`
canonical pack 更新两处文字：`session_commands` 的实现者清单加 grokbuild；
grok 模式状态的快照/patch 字段（若新增）按既有 optional-field 规则 additive。
hello_ack backends[].capabilities 由类型断言自动出现，无需手写。另（评审 §4
裁定）：`core/session_commands.go` 与 `handlers_session_commands.go` 中 dsh 专属
注释（「绝不经 SendMessage」等）同步改为**分 backend 契约**表述，Phase 1 交付。

## 7. iOS 设计与四拍走查

**改动清单（v1.1，评审 R2——不再是「唯一结构性改动」）**，逐项对应 §5.2：

1. 门控放宽（唯一代码块级改动）：

   ```swift
   // ChatUIKitContainerView.swift:4568（现）
   let slashCommandsAvailable = currentInputBackendKind == .deepSeekWeb && ...
   // 改为 capability 驱动（kind 硬门删除；capability + session 两条件保留）
   let slashCommandsAvailable = (viewModel.backendClient?.supportsSessionCommands ?? false)
       && !(viewModel.currentSessionId ?? "").isEmpty
   ```

2. 白名单 per-backend 化：`SlashCommandPanel.commandWhitelist` 仅对 deepSeekWeb
   生效；grok 按 D1（推荐不套用）。
3. 回退三态：`AttachMenuPlanner` 的内置三条回退仅对 deepSeekWeb 生效；grok
   加载中/失败/空目录三态真实呈现（§5.2-3）。
4. Plan chip：数据源扩 grok 模式状态；grok 会话点击 = `set_permission_mode`
   （模式通道），**不再发 `/plan` 文本**；dsh 路径不动。
5. 错误呈现沿用现有弹窗（`presentSlashCommandErrorAlert`）。
6. `route(hint.isEmpty)` 判决保留，依据改标移动端规则（D4）。

复用不变：＋菜单命令节、列表、claim 幽灵提示、点选即收、按命令名置灰、命令行
投影、plan 审批卡机器。

### 7.1 四拍走查：命令面板（统一适用于全目录命令，评审 R7 部分采纳声明见文首）

1. **打开输入的地方**：Grok Build 会话（有 sessionId + `session_commands`
   capability）→ 输入框 `＋` → 「命令」节 → 官方可执行目录列表（name +
   description；加载中/失败/空目录按 §5.2-3 三态呈现，**不回退 dsh 三条**）。
2. **输入**（D4 移动端规则）：hint 非空（grok 绝大多数 builtins，含 compact/
   always-approve）→ 认领输入框 `/name ` + 官方 hint 幽灵提示，键盘拉起，人补
   参数回车执行；**空参直接回车即执行**（官方 optional-args Enter=Executes）；
   无 input 命令（若目录中存在）→ 点选即收面板立即执行。自由文本误触 `/` 未
   命中目录 → 普通发送（官方 ④ 分支预期行为）。
3. **发出去**：`execute_session_command` → Mac line 校验（`/` 前缀+单行）→
   fresh 目录白名单（缺失先拉，拉不到 fail closed）→ per-turn 子进程
   `session/prompt`（官方确定性 resolve）→ turn 终态按四分法 settle。
4. **把过程展示出来**：命令行作为 user message 经官方 transcript/polling 自然
   上屏（**不合成**）；builtin 副产物（如 compact 的上下文用量变化）经既有
   `usage_update`/`auto_compact_*` 事件（codec 已有 case）呈现；执行中按命令名
   置灰「执行中…」；失败按现有机器弹窗呈现官方错误文本（§5.2-5）。

### 7.2 四拍走查：Plan 切换

1. **打开**：Grok 会话输入框 Plan chip（当前模式指示；default 态灰、plan 态橙）。
2. **输入**：无参数（plan↔default 两态点按，对齐官方 toggle 语义）。
3. **发出去**：`set_permission_mode {mode:"plan"|"default"}` → handler
   `SessionModeSwitcher` 分支 → mode-only 子进程 `session/set_mode`，**settle =
   RPC response**（幂等无 CMU 亦完成）；失败 → RPC error，chip 不翻转。
4. **过程展示**：chip 随 observed 翻转——response 成功后先以请求值下发 patch
   （官方 actor 已处理的真实状态），CMU 观测到达后同值幂等覆盖；外部（Mac
   TUI/审批流）切换经三路观测同步到同一 chip；plan 态下模型产出计划 → 已交付
   `plan_review` 审批卡（§25）→ 批准后模式回 default（官方 CMU，chip 跟随）。
   不在本地假造「已在计划模式」状态——chip 状态只来自官方回流/官方 response
   （dsh §6.3「不要本地假徽章」红线沿用）。

## 8. 分期落地

| Phase | 内容 | 验收 |
| --- | --- | --- |
| 0 证据矩阵 | §0.1 P1-P8 全部样本归档（`docs/fixtures/` 或实现说明内联，脱敏）；P4/P6 真实 turn 成本与副作用先报 owner | 每项有真实样本 + 与 1.0.16 源码差异记录；**样本不足的依赖项保持阻断**（不静默按源码形状编码，也不降级为开放项后继续） |
| 1 Mac 目录+执行 | codec ACU 捕获、`SessionCommandCatalog` 实现（List fail-closed 两通道、execute line 校验+白名单、settle 四分法）、capability 出现、core/handler dsh 注释改分 backend 契约 | `go test ./agent/grokbuild ./go-bridge` 定向，须含：目录**空/失败/缺失**三态、`/` 前缀+单行校验、白名单 fail-closed（缓存缺失不放行）、**cancelled 不算成功**、未知 stopReason 保留原值、**compact（hint 非空）claim 判决**；capability 断言 dsh-web **与** grokbuild 有 `session_commands`、claude/codex-remote/opencode-web 无；fixture 用 Phase 0 样本 |
| 2 模式通道 | `SessionModeSwitcher` 接口 + handler 优先分支（先校验后调用）、mode-only set_mode turn（response settle）、CMU 三路消费、`plan` 键迁移（D3） | 定向单测须含：**跨会话模式隔离**（A/B 两 grok 会话互不影响）、**幂等无 CMU 切换**（plan→plan 第二次仍 settle 不挂起）、**官方失败不广播成功**（set_mode error → RPC error 无成功事件）、**重启恢复**（bridge 重启后缓存/观测收敛）、**外部切换后发送**（CMU 更新 observed；prompt 无 mode 不覆盖）、**陈旧 replay**（旧 CMU 晚到按 last-wins/replay 规则不回退）；真机 §9 #5-#7；plan 审批回归（§25 矩阵不回退） |
| 3 iOS 面板 | §7 六项改动清单 | 定向单测：门控、**白名单 per-backend**、**回退仅 dsh**、chip action 走模式通道、routing 判决（注释改移动端规则）；三态验收（**首次打开 List 未返回 / List 报错 / 官方空目录**）；真机 §9 |
| 4 收尾 | protocol pack 文字同步、双仓 CHANGELOG、Release 覆盖安装 `/Applications`（killall 主 app + `cordcode-bridge-runtime` 两进程）、owner 真机 | §9 全矩阵 |

按仓库纪律：Phase 1/2 不装真机不跑全量；任何定向 build/test 超 5 分钟异常止损。

## 9. Owner 真机矩阵（产品语言）

环境：Mac 安装版 grok 1.0.13；iPhone 连已覆盖安装的 CordCode Link；Grok Build
会话（含至少一个带 project skills 的目录，验证目录差异）。

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | 打开 Grok 会话 → `＋` | 出现「命令」节，列官方**可执行目录**（builtins+skills+workflows，含 compact/goal 等）；与 Mac TUI `/` 补全清单**不必逐一等同**（TUI 另含 pager-local 命令如 /plan）；首次打开 List 未返回/报错时显示加载中/错误态，**不出现 dsh 的 /plan**；Claude/Codex/OpenCode 会话维持 dsh 期形态不回归 |
| 2 | 点 compact（官方 hint「optional context about what to preserve」） | 认领输入框 `/compact ` + hint 幽灵提示；**直接回车（空参）即执行**；面板即收；命令行以用户消息上屏；上下文用量随后更新；无 toast、无「模型把命令当聊天回」 |
| 3 | 点带参命令（goal） | 认领输入框 `/goal ` + hint 提示；补参回车执行；不补直接换普通消息 = 草稿还原 |
| 4 | 在输入框手打 `/compact` 普通发送（非面板） | 与官方一致：确定性执行（官方语义，不是面板专属）；手打目录外 `/foo` = 模型普通回复（官方 ④ 分支）；手打 `/plan` = 模型普通回复且**不进计划模式**（官方 fail closed，这正是 chip 必须走模式通道的原因） |
| 5 | 点 Plan chip 进入计划模式 | chip 变橙；Mac TUI 同会话可见已进 plan（plan_mode.json 持久化）；杀掉 iPhone 重连 chip 仍橙（冷恢复） |
| 6 | 计划模式下发消息 | 模型按计划模式行为（先计划后执行）；消息**不携带模式信号**（官方从持久化继承）；无可见异常。外部（Mac TUI）刚切回 default 后手机立即发消息：**仍是 default**（单写者，无陈旧回写） |
| 7 | 模型产出计划 → 审批卡批准 | 既有两键卡出现并可批准；批准后 chip 回灰（官方 CMU 回流） |
| 8 | 在 **Mac TUI** 切 plan / 跑 `/compact` | iPhone 无操作时 chip/命令行随后同步（leader 广播 + polling 兜底） |

真机点击须 owner；agent 只做日志/Management 核验。#4 是「官方语义 vs 自造防线」
的验收锚点；#5/#8 是三路可观测的验收锚点；#6 后半句是单写者模型（R6）的验收
锚点。

## 10. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 1.0.13 活体形状与 1.0.16 源码漂移（P1-P8 任一） | §0.1 门：活体优先，漂移记录；**样本不足的依赖项保持阻断**，不猜 |
| 目录快照与执行**非原子**（快照后 skill 被删/availability 变化） | 官方 resolve 落 ④ 普通 prompt，官方行为非 CordCode 失败；§6.1 明示边界，不掩盖、不加「执行时二次拉取」（同样非原子） |
| set_mode response ≠ 已落盘（persistence 异步） | P7 验证 response→退出→reload 是否恢复；有丢失窗口则退出前等 CMU/短延迟（§6.2.2）；残差 = 软失败（模式未生效），观测收敛，无状态污染 |
| 陈旧 replay 晚于 live 到达 | last-wins + replay 标记规则（§6.2.3）；P5 取证标记形状后定稿；1.0.13 无标记则冷重放仅作初始值 |
| mode-only 子进程成本（每次切换一个短命 spawn） | 与一次空 turn 同量级；切换是低频操作 |
| 外部切换后手机陈旧显示 | observed 只展示永不回写；CMU 三路收敛；prompt 不携带 mode（§6.2.3）——显示陈旧无害，写入永不陈旧 |
| `permission_mode` capability 语义变化影响既有 iOS 权限菜单 | D3 只迁 plan 键；其余 5 键行为零变化；回归矩阵 #1 |
| iOS 门控放宽后其他 backend 误显示 | `supportsSessionCommands` 仅 dsh-web/grokbuild 广告；capability 测试断言三家无；白名单/回退 per-backend（§5.2） |
| 命令行 user message 与既有 transcript polling 投影冲突 | P6 取证行形状；若 1.0.13 命令行有 displayText 等 meta 差异，映射层处理，不改投影身份公式 |

回滚：grokbuild 去掉 `SessionCommandCatalog`/`SessionModeSwitcher` 实现 →
capability 消失/`handleSetPermissionMode` 落回 legacy `ModeSwitcher` 分支（现状
行为）→ iOS 门控与菜单自动退回（dsh 期形态）；codec ACU/CMU 消费即使回滚面板也
保留（状态捕获无副作用）。

## 11. 与后续案的边界 + 开放项

边界：本项关闭 think.md「Grok iOS Plan 只写 agent 内存」与调研 §7 的 Grok 命令
面板两个口子。不关闭：skills 面板深面（目录展示即可执行，但 skills 的
`kind:"chat"` lane、skill 管理不做）、workflow 进度投影、`ask` 模式、permission
其余键接真、MCP elicit、interjection（Phase B）、prompt-only `loop` 特化、
错误反馈 inline 化（跨 backend 另案）。

开放项（Phase 0 取证定夺，不猜；**取证完成前对应能力保持阻断**）：

1. P3 catalog 单例子进程调 `x.ai/commands/list` 的可行性（cwd 传递方式）——
   不成立则冷通道退化为 per-turn 子进程 ACU。
2. P5 `updates.jsonl` 冷重放条目的 replay/live 标记形状——决定去重排序终稿
   （§6.2.3）。
3. P6 builtin settle 的可透传文本是否存在——不存在则 `ResultText` 恒空，反馈全
   靠投影（诚实映射，不造文案）。
4. P7 response 后立即退出的持久化屏障结论——决定退出前是否等待（§6.2.2）。
5. P8 `summary.json` 是否带 mode——决定冷启动 chip 恢复是否需要额外拉
   `updates.jsonl` 尾部。
6. D1/D4 owner 裁决终表（裁决后写入实现说明；评审门槛 2：事实更正已完成，
   现在可以裁决）。
