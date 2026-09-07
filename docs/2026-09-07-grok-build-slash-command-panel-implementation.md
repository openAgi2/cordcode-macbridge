# Grok Build 斜杠命令面板 + 计划模式官方通道 接入方案

> 状态：**方案 v1.0（2026-09-07，未实施）**。仿
> [2026-09-04-dsh-slash-command-panel-implementation.md](2026-09-04-dsh-slash-command-panel-implementation.md)
> 结构编写；dsh 方案 §11 的十轮返工教训（尤其 §11.2 ⑤⑧⑨⑩与 §11.3 四拍纪律、
> §11.4 迁移检查单）是本方案的**前置必读**——本方案多处设计与「不做」直接来自那些
> 教训，不再逐条复述理由。事实基线：调研
> [2026-09-04-slash-command-skill-cross-backend-survey.md](2026-09-04-slash-command-skill-cross-backend-survey.md)
> §7（两轮独立评审通过）。

## 0. 来源清单（P0）与开工门

| 仓库 | 工作树 | 分支 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| grok-build（上游，只读） | `/Users/jacklee/Projects/grok-build` | detached @ `72a61251` | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9`（1.0.16） | 干净 |
| **目标运行版本**（本机安装二进制） | `~/.grok/bin/grok` | — | **1.0.13（自报 `5e9a58528b76`，不在 checkout 历史中）** | — |
| cordcode-macbridge | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（本方案工作树） | `plan/approval-layer` | `de17e6f782ecc1d72a29f7d4e81e6aed2405251d` | 干净 |
| cordcode-ios | `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 |

来源注记：调研期 macbridge 锚点在 `a04095e`（本 worktree）读取、在
`fbb4940`（main checkout，merge 提交）复核，所引文件零差异；`de17e6f` = 本方案
文档入库提交，除本文档外与 `fbb4940` 无差异。iOS 锚点在 main checkout
`c3b1d5b0` 读取；实施若改用 iOS 工作树，按下方门点规则重新登记。

执行本方案前，三个门点（读源码分析前 / 首次改文件前 / 构建前）必须按
CLAUDE.md P0 重新生成本清单；本文的锚点行号以 grok-build @ `72a61251` 与
macbridge @ `de17e6f` 为准，换基线须逐条复核。

### 0.1 版本漂移门（本方案特有的 P0 前置）

think.md 2026-09-02 条目已证明：**checkout ≠ 目标二进制**（安装版 1.0.13 的
commit 不在 checkout 历史）。本方案锚点在 1.0.16 源码上核验；**编码开始前必须
先在安装版 1.0.13 上活体取证**（方法：think.md 2026-09-02 的零 API 消耗 fake
client——`grok agent stdio` + 只发 initialize/session/load，不真跑 turn）：

| # | 取证项 | 决定什么 |
| --- | --- | --- |
| P1 | initialize `_meta.availableCommands` 真实形状（字段名/`input` 嵌套/camelCase） | List 解码中间类型 |
| P2 | session/load 后 `available_commands_update` 全形状（builtins+skills+workflows 顺序、`_meta`、skills 的 `qualified_name`/`plugin_name` 类字段） | 全目录解码与展示字段 |
| P3 | catalog 单例子进程上调 `x.ai/commands/list {cwd}` 的响应与 cwd scoping | 冷会话 List 通道是否成立 |
| P4 | prompt `_meta.mode:"plan"` 活体（→Active 系统注入、再切回、ExitPending） | Phase 2 模式携带 |
| P5 | `current_mode_update` 在子进程 stdout / leader gateway 广播 / `updates.jsonl` 三路的形状 | 模式可观测三路消费 |
| P6 | builtin（`/compact`、`/always-approve`）执行的 settle 证据：哪条 update / 是否产生 user message 行 / stopReason 形状 | `SessionCommandResult` 映射与「过程展示」 |
| P7 | mode-only 短命子进程 `session/load` + `session/set_mode` 的 ack 形状 + `plan_mode.json` 落盘 | Phase 2 切换通道 |
| P8 | `summary.json` 是否持久化当前 session mode（已知有 `current_model_id`/`current_effort_id`） | 冷启动模式恢复路径 |

任何一项活体形状与 1.0.16 源码冲突时，**以 1.0.13 活体为准**并在实现说明里记录
漂移（dsh §11.2 ① 教训：声明契约会漂，请求形状以活体探测收口）。

### 0.2 开工源码优先门

1. 上游只读 checkout `72a61251`；目标二进制 1.0.13。动手改 `agent/grokbuild` /
   `go-bridge` / iOS **之前**，至少读并在实现说明里写下符号锚点（§4 已给全，逐条
   对号）。
2. 官方 UI 层（pager）交互判决必须读：命令目录如何进 autocomplete、plan 切换在
   TUI 里是按键/菜单而非 slash 命令——**交互语义在 UI 源码不在 RPC schema**
   （dsh §11.2 ⑤ 教训）。
3. CordCode 只做 bridge 接线：官方 JSON 用中间类型解码再映射；`core.SessionCommandCatalog`
   的 dsh 实现是**先例不是真值**——本方案 §6.1 会写明 grok 与 dsh 的两处语义差异，
   不得把 dsh 语义反当 grok 协议证据。
4. 测试 fixture 必须来自 §0.1 活体样本或官方仓 fixture；手写 fake server 只验证
   内部行为。
5. 违反本门 = 本方案未执行，评审按未通过。

## 1. 目标

1. iPhone **Grok Build 会话**的 `＋` 菜单出现「命令」节（与 dsh 同入口形态，
   dsh 方案 §12 已把 `/` 按钮合并进 ＋ 菜单）：列出该会话官方命令目录
   （builtins，availability 已被官方过滤），点选按官方交互分流执行。
2. **Plan 模式 iOS 入口接官方通道**：think.md「Grok iOS Plan 只写 agent 内存」
   的现状终结——iOS 切 Plan 走官方 `session/set_mode`（+ 每次发送携带官方一等
   信号 `_meta.mode`），模式变化经官方 `current_mode_update` 回流 iPhone 展示
   （Plan chip，对齐 dsh 已交付形态）。
3. 已交付的 plan 审批卡（`plan_review`，leader 广播 `x.ai/exit_plan_mode`，
   think.md §25）继续工作，本项不改审批语义。

## 2. 明确不做（第一期）

| 项 | 理由 |
| --- | --- |
| 键入 `/` 自动补全弹层 | owner dsh 裁决沿用：只要菜单入口 |
| prompt-only 命令 `loop` 的 loop_fire_mode 特殊面 | `PROMPT_COMMANDS` 特例重写 blocks + displayText，产品面窄；目录展示但走通用通道，特化另案 |
| workflow 进度投影（`workflow_projection`） | 命令进目录、可执行；workflow 专属过程投影是独立官方 surface，另案（对齐 dsh §11.2 ④「每类投影各有独立 UI 组件」教训，不合并塞进本项） |
| skills 的 `kind:"chat"` 产品通道 | `x.ai/commands/list {kind:"chat"}` 是 Grok Chat 产品 lane，CordCode 不消费 |
| MCP elicit / `x.ai/queue/*` interjection | think.md：interjection 后置 Phase B |
| 合成全 backend Plan 按钮 | think.md「iOS 进入计划模式」行 owner 明令禁止；Grok Plan 入口必须是 grok 官方通道 |
| 客户端自行合并/去重三条目录通道的结果 | 官方顺序（builtins-first）与 availability 过滤都是官方事实；客户端合并=自造目录（§4.1） |
| `permission_mode` 其余 5 键接真 | 现状 SetMode 全空转（§5.1）；Phase 2 只接 `plan`↔SessionMode，其余 permission 键的官方通道（ACP permission 与 SessionMode 是两套体系）另案裁决 |
| 目录刷新 UI（下拉重拉等） | 冷拉一次 + turn 期 ACU 增量已覆盖官方语义；手动刷新自造 |

## 3. Owner 决策

### 3.1 已锁定（既往裁决，直接生效）

| # | 来源 | 裁决 |
| --- | --- | --- |
| L1 | think.md「iOS 进入计划模式」行 | 三条 Plan 入口分别接各自官方通道，**禁止合成全 backend 按钮**；Codex 入口挂起、DSH 已交付、Grok 即本方案 |
| L2 | think.md §25 | plan 审批两键卡（标题行+允许/拒绝）程度可接受；完整体验（计划全文+全按钮集）属跨 backend 通用另案，本项不动 |
| L3 | dsh 方案 §11.1 #3 | 面板白名单收窄先例：面板只放 iPhone 面真正需要的命令（Grok 16 条 builtins 不是全上都对，见 §6.4 白名单裁决点） |
| L4 | dsh 方案 §12 | 命令入口 = ＋ 菜单「命令」节（不是独立 `/` 按钮） |

### 3.2 本方案待 owner 裁决（开工前问一次，一次问完）

| # | 决策点 | 推荐 | 备选 |
| --- | --- | --- | --- |
| D1 | 面板白名单 | **默认全上**官方 availability 过滤后的 builtins（官方给什么列什么，最诚实）；仅 `feedback`/`session-info` 这类 Mac TUI 反馈类可斟酌 | 对齐 dsh 只留 compact/goal/plan 三条的最小面 |
| D2 | Plan 入口位置 | **输入框 Plan chip**（对齐 dsh §11.1 #1 已交付形态；点按切换 on/off） | ＋ 菜单「计划模式」行 |
| D3 | `permission_mode` capability 的空转处置 | Phase 2 落地时把 `plan` 键从 `PermissionModes()` 挪到 SessionMode 通道，其余 5 键**暂留现状**（既有行为不变） | 全部摘除直到各自接真 |

## 4. 官方不变量（上游核验 @ `72a61251`，实施必须镜像）

### 4.1 命令目录：三条官方通道，各司其职

| 通道 | 时机 | 内容 | 锚点 |
| --- | --- | --- | --- |
| `initialize` 响应 `_meta.availableCommands` | 握手后、session 前 | **仅 builtins**（availability 过滤；pre-session 只能算 config 类 gate） | `acp_agent.rs:543` + `slash_commands.rs:837-851`（`AvailableCommand::new(name, description).input(UnstructuredCommandInput::new(hint))`，`input` 为 Option） |
| `sessionUpdate.available_commands_update` | session/new 或 session/load 后 | **builtins + skills + workflows**，builtins 在前，cwd-scoped | `session_setup.rs:247` send_available_commands_update；`slash_commands.rs:445` ACU `_meta` 带已注册工具名 |
| ext 方法 `x.ai/commands/list` | pull | `{sessionId?, cwd?, kind?}` → `{commands:[AvailableCommand], tools?}`，camelCase；`kind:"chat"` 是产品 lane 不用 | `slash_commands.rs:852-871` ListCommandsRequest/Response |

不变量：

1. **`input` 的有无就是官方交互判决表**：`input` 缺席 → 官方点选即分离执行；
   `input` 存在（`{hint}`）→ 官方认领输入框。与 dsh 的
   `desc.input === undefined → runDetached / claim` 完全同构——iOS
   `SlashCommandRouting.route`（`hint.isEmpty` 判决）零改动复用。
2. availability 过滤是**官方做的**（`builtin_commands(availability)` filter gate；
   ACU 同理）。CordCode 拿到什么列什么，**不得**客户端再过滤或合并三条通道。
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
3. **pager-local 功能绝不能当文本发**：`/plan`、`/model`、`/effort` 不在命令
   目录（BUILTIN_COMMANDS 16 条无此三者）；把它们当 prompt 发 = 落入 ④ 普通
   聊天，**官方 fail closed 于「不进 Plan」**。Plan 走 §6.2 模式通道，模型/
   effort 走既有 catalog 通道（`_meta` on session/new）。
4. BUILTIN_COMMANDS 16 条（`slash_commands.rs:66`）：compact、always-approve
   （yolo）、flush、dream、memory、context、hooks-*、plugins、reload-plugins、
   session-info、feedback、deep-research、workflow、goal 等——**目录里没有
   `plan`**，这是与 dsh 目录最大的表面差异（dsh 的 plan 是命令，grok 的 plan
   是模式）。

### 4.3 模式（SessionMode）：独立于 permission 的官方体系

| 事实 | 锚点 |
| --- | --- |
| SessionMode 枚举 `default`/`plan`/`ask`（snake_case，未知→Default） | `xai-grok-tools/src/types/session_mode.rs` |
| `session/prompt._meta.mode` 是官方一等模式信号：`reconcile_plan_mode_with_prompt` 注释原文「**the only signal the client sends**. Both transitions are idempotent」；PromptMode agent(默认)/ask/plan | `acp_session_impl/session_mode.rs`（`reconcile_plan_mode_with_prompt` / `prompt_mode_from_session_mode_id` / `resolve_turn_prompt_mode`） |
| `session/set_mode` 需要 resident session handle | `acp_agent.rs:2229-2250` |
| ext `x.ai/toggle_plan_mode` 同样 resident_handle 门控；无 resident → warn `session not found` 并 **no-op** | `acp_agent.rs:2756-2793` |
| PlanModeState 机：Inactive→Pending→Active→ExitPending；**持久化到会话目录 `plan_mode.json`，resume 恢复，`awaiting_plan_approval` 同样持久化** | `plan_mode.rs`（PlanModeState/PlanModeTracker/PlanModeSnapshot） |
| `CurrentModeUpdate(SessionModeId)` 通知在真实转换时发出；**持久化进 `updates.jsonl`（session replay 重放模式）并转发 gateway** | `acp_session_impl/updates.rs:348` enqueue；`notification_bridge.rs:201` emit_current_mode_update |
| `x.ai/exit_plan_mode` 批准 → notification_bridge 回发 CurrentModeUpdate 恢复 default | `notification_bridge.rs`（§25 已交付的消费端在 `leader_subscriber.go:680`） |

推论（对 CordCode per-turn 子进程模型至关重要）：**模式跨进程持久**
（plan_mode.json）+ **prompt `_meta.mode` 无需 resident handle**——CordCode 不
需要常驻子进程就能驱动与观测模式。

### 4.4 codec：fail-open，无 dsh 式 reset 风险

grok codec 对未知 sessionUpdate 是 fail-open（`acp_codec.go:362-369`：Debug 日志
+ return nil，不 reset、不报错）。dsh 方案 Phase 1 的「未知事件 P0 发布阻断」在
grok **结构性不存在**；本方案的 codec 工作是把 `available_commands_update` /
`current_mode_update` 从 known-drop **升级为消费**（§6.3），fail-open 默认保持。

## 5. CordCode 现状与差距（macbridge `de17e6f` + iOS `c3b1d5b0`）

### 5.1 Mac（`agent/grokbuild/`）

| 现状 | 锚点 | 差距 |
| --- | --- | --- |
| `SetMode` 只写内存（`a.mode = normalizePermissionMode(mode)`），**无任何 wire 调用**；`a.mode` 仅被 `GetMode` 读回 | `grokbuild.go:573-583` | 整个 SetMode 空转；think.md「Grok iOS Plan 只写 agent 内存」即此 |
| `PermissionModes()` 返回 6 条，含 `{Key:"plan"}` | `grokbuild.go:585-593` | `permission_mode` capability 已广告（`backend_capabilities.go:44-46` ModeSwitcher 断言）但 plan 键无效——诚实缺口，Phase 2 接真 |
| `sessionPromptParams` 仅 `{sessionId, prompt}`，**无 `_meta`** | `acp_types.go:240-243` | Phase 2 增加可选 `_meta.mode` |
| codec 把 `session_info_update`/`current_mode_update`/`available_commands_update`/`config_option_update` 归入「internal state updates — not forwarded」直接丢 | `acp_codec.go:326-330` | ACU/CMU 需升级为捕获与解码 |
| `initializeMeta` 只解 `modelState` | `acp_types.go:102-115` | 增解 `availableCommands` |
| plan 审批卡已交付（`plan_review`，actions approve/requestChanges/quit） | `leader_subscriber.go:680` handlePlanBroadcast | 无差距，复用 |
| 进程级单例 catalog 子进程（`grok agent --no-leader stdio`）现用于 session/list + 模型目录 | `catalog_session_list.go` | P3 取证后可复用作冷会话 `x.ai/commands/list` 通道 |
| core `SessionCommandCatalog` 接口 + `SessionCommand{Name,Description,Hint}` / `SessionCommandResult{CommandID,ResultKind,ResultText}` | `core/session_commands.go:10-40` | grokbuild 实现之（映射见 §6.1） |
| wire RPC `list_session_commands` / `execute_session_command` 已在 handlers（dsh 期交付，scope session.read/write） | `go-bridge/handlers.go:1642-1644`、`bridge_v1_schema.go:46` | 零新增 RPC |

### 5.2 iOS

| 现状 | 锚点 | 差距 |
| --- | --- | --- |
| ＋菜单「命令」节门控**硬编码** `currentInputBackendKind == .deepSeekWeb` + `supportsSessionCommands` + 有 session | `ChatUIKitContainerView.swift:4565-4571` | 放宽为 capability 驱动（去掉 kind 硬门） |
| `SlashCommandSelection.route(for:)` 已 generic over `BackendSessionCommand`：`hint.isEmpty` → execute；否则 claim 输入框 | `Views/Chat/SlashCommandRouting.swift:32-38` | 零改动（§4.1 #1 同构） |
| claim 幽灵提示 / 点选即收面板 / 按命令名置灰 / Plan chip / 命令卡机器（dsh §11/§12 交付物） | `ChatInputAccessoryView.swift:152-164` 等 | 复用；Plan chip 接 grok 模式数据源 |

## 6. Wire 设计

### 6.1 `SessionCommandCatalog` 的 grok 实现（Mac）

**List（两条官方通道，不合并）**：

- 冷/近期无 turn 会话：进程级 catalog 单例子进程带会话 cwd 调
  `x.ai/commands/list {cwd}`（P3 取证 cwd scoping 与 catalog 子进程可行性；
  不可行则退化为 per-turn 子进程 handshake + session/load 后读 ACU）。
- per-turn 子进程存活期：codec 捕获 `available_commands_update`（§6.3）写入
  agent 内存 per-session 缓存（含 builtins-first 顺序）；List 命中缓存即返回。
- initialize `_meta.availableCommands`（builtins-only）仅作**兜底**（两条全通道
  不可用时至少给 builtins），并在 UI 不区分来源。
- 映射：`Name`/`Description` 顶层；`Hint` ← `input.hint`（`input` 缺席 → `""`）；
  解码走中间 wire 类型（`input` 用指针/omitempty，先例：dsh「裸数组映射」单测
  覆盖无 input 条目）。skills/workflows 条目的额外字段（P2 取证）第一阶段丢弃。

**Execute（官方 prompt 通道，§4.2 差异已声明）**：

- `ExecuteSessionCommand(ctx, sessionID, "/compact")` = 复用现有 per-turn
  `session/prompt` 管道发送该行（与 SendMessage 同 child 生命周期、同
  turn-end 检测），**不**本地合成 user 气泡、**不**挂 `_meta.mode`（模式另走
  §6.2）、不带附件（grok 命令无附件面）。
- `line` 校验沿用 wire 层：以 `/` 开头、单行；**再加白名单**：line 的命令名必须
  命中最近一次 List 缓存目录（官方确定性解析只对目录内命令成立，§4.2 #1；
  未命中 → RPC 失败「command not in catalog」，fail closed）。缓存缺失时允许
  放行并在实现说明记录（冷启动首个 turn 前的面板本就依赖 List 成功）。
- settle 映射（P6 取证后定稿）：初稿 = prompt 被接受且收到该 turn 终态
  （`turn_completed` stopReason，`acp_codec.go:336` 邻域既有处理）→
  `ResultKind:"success"`；`ResultText` 初稿空串（官方 builtin 的用户可见反馈
  走会话投影而非 ACP result payload；若 P6 证实有可透传文本则映射之）。禁止
  把成功折叠成无反馈（dsh §11.2 ② 教训）——「过程展示」由 §7 的官方形态承接。

### 6.2 模式通道（Phase 2）

1. **切换**：iOS Plan chip 点按 → 既有 `set_permission_mode` RPC（plan↔default
   两键，对齐 dsh chip off 公式）→ grokbuild `SetMode` 真实现 = **mode-only 短命
   子进程**：handshake → `session/load`（既有路径，cwd 必带）→
   `session/set_mode {modeId:"plan"|"default"}` → 等 `current_mode_update`
   notification 作 ack（P7 取证形状）→ 退出。持久化由官方 `plan_mode.json`
   承接，下一条 turn（任何子进程）按新模式恢复。
2. **收敛兜底**：每次发送的 `session/prompt` 增可选 `_meta:{mode}`（当前会话
   模式；`sessionPromptParams` 加 omitempty 字段）。官方注释「the only signal
   the client sends…idempotent」保证幂等——切换 turn 丢失时下一条消息自愈。
3. **`ask` 模式**：SessionMode 有 ask，但 CordCode 面第一期只暴露
   plan↔default（D2）；ask 属权限类语义，与 permission 键纠缠，留 L2 另案。
4. **`plan` 键迁移**：`PermissionModes()` 去掉 plan 条目（或改由 SessionMode
   数据源驱动 chip），其余 5 键维持现状（D3）。

### 6.3 codec 升级（Mac，Phase 1/2 各一半）

| update 类型 | 现状 | 改为 |
| --- | --- | --- |
| `available_commands_update` | known-drop | 解码（P2 样本形状）→ 更新 per-session 目录缓存；**不**产 timeline 事件 |
| `current_mode_update` | known-drop | 解码 SessionModeId → 会话级模式状态 → 经既有会话状态通道下发 iOS（对齐 dsh `planMode` 快照/patch 先例）；**不**产 timeline 事件 |
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
hello_ack backends[].capabilities 由类型断言自动出现，无需手写。

## 7. iOS 设计与四拍走查

门控放宽（唯一结构性 iOS 改动）：

```swift
// ChatUIKitContainerView.swift:4568（现）
let slashCommandsAvailable = currentInputBackendKind == .deepSeekWeb && ...
// 改为 capability 驱动（kind 硬门删除；capability + session 两条件保留）
let slashCommandsAvailable = (viewModel.backendClient?.supportsSessionCommands ?? false)
    && !(viewModel.currentSessionId ?? "").isEmpty
```

其余（＋菜单命令节、列表、claim 幽灵提示、点选即收、按命令名置灰、命令行投影）
全部复用 dsh 机器。Plan chip 数据源从 grok 模式状态（§6.3）取，形态对齐 dsh
（× 橙色点按切换）。

### 7.1 四拍走查：命令面板（以 `/compact` 与带参数命令为例）

1. **打开输入的地方**：Grok Build 会话（有 sessionId + `session_commands`
   capability）→ 输入框 `＋` → 「命令」节 → 官方目录列表（name + description）。
2. **输入**：`input` 缺席（如 compact）→ 无需输入，点选即收面板立即执行；
   `input` 存在 → 认领输入框 `/name ` + 官方 hint 幽灵提示，键盘拉起，人补
   参数回车（判决整行走 execute，对齐 dsh 返工⑤；自由文本误触 `/` 未命中目录
   → 普通发送，官方 ④ 分支预期行为）。
3. **发出去**：`execute_session_command` → Mac 白名单校验 → per-turn 子进程
   `session/prompt`（官方确定性 resolve）→ turn 终态 = settle。
4. **把过程展示出来**：命令行作为 user message 经官方 transcript/polling 自然
   上屏（**不合成**）；builtin 副产物（如 compact 的上下文用量变化）经既有
   `usage_update`/`auto_compact_*` 事件（codec 已有 case）呈现；执行中按命令名
   置灰「执行中…」；失败 inline 展示官方错误文本（不弹窗、不 toast——dsh
   §11.2 ③ 教训：反馈形态照抄官方）。

### 7.2 四拍走查：Plan 切换

1. **打开**：Grok 会话输入框 Plan chip（当前模式指示；default 态灰、plan 态橙）。
2. **输入**：无参数（plan↔default 两态点按，对齐官方 toggle 语义与 dsh chip
   off 公式）。
3. **发出去**：`set_permission_mode {mode:"plan"|"default"}` → mode-only 子进程
   `session/set_mode` + `current_mode_update` ack。
4. **过程展示**：chip 随 ack 翻转；外部（Mac TUI）切模式经 leader 广播/冷重放
   同步到同一 chip；plan 态下模型产出计划 → 已交付 `plan_review` 审批卡（§25）
   → 批准后模式回 default（官方 emit CurrentModeUpdate，chip 跟随）。不在本地
   假造「已在计划模式」状态——chip 状态只来自官方回流（dsh §6.3「不要本地假
   徽章」红线沿用）。

## 8. 分期落地

| Phase | 内容 | 验收 |
| --- | --- | --- |
| 0 活体取证 | §0.1 P1-P8 全部样本归档（`docs/fixtures/` 或实现说明内联，脱敏） | 每项有真实样本 + 与 1.0.16 源码差异记录；无样本的项进入 §11 开放项，不得静默按源码形状编码 |
| 1 Mac 目录+执行 | codec ACU 捕获、initialize `_meta.availableCommands` 解码、`SessionCommandCatalog` 实现 + capability 出现、execute 白名单 + per-turn prompt 管道、P6 定稿 settle 映射 | `go test ./agent/grokbuild ./go-bridge` 定向；capability 测试断言 dsh-web **与** grokbuild 有 `session_commands`、claude/codex-remote/opencode-web 无；fixture 用 Phase 0 样本 |
| 2 模式通道 | `_meta.mode` 携带、mode-only set_mode turn、CMU 三路消费（codec/leader/冷重放）、`plan` 键迁移（D3） | 模式往返真机矩阵（§9 #5-#7）；plan 审批回归（§25 矩阵不回退） |
| 3 iOS 面板 | 门控放宽 + Plan chip 数据源接 grok | 定向单测（门控、routing 复用零改动断言）；真机 §9 |
| 4 收尾 | protocol pack 文字同步、双仓 CHANGELOG、Release 覆盖安装 `/Applications`（killall 主 app + `cordcode-bridge-runtime` 两进程）、owner 真机 | §9 全矩阵 |

按仓库纪律：Phase 1/2 不装真机不跑全量；任何定向 build/test 超 5 分钟异常止损。

## 9. Owner 真机矩阵（产品语言）

环境：Mac 安装版 grok 1.0.13；iPhone 连已覆盖安装的 CordCode Link；Grok Build
会话（含至少一个带 project skills 的目录，验证目录差异）。

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | 打开 Grok 会话 → `＋` | 出现「命令」节，列官方命令（含 compact/goal 等；与 Mac TUI `/` 补全清单一致）；Claude/Codex/OpenCode 会话维持 dsh 期形态不回归 |
| 2 | 点无参命令（compact） | 面板即收；命令行以用户消息上屏；上下文用量随后更新；无 toast、无「模型把命令当聊天回」 |
| 3 | 点带参命令（goal） | 认领输入框 `/goal ` + hint 提示；补参回车执行；不补直接换普通消息 = 草稿还原 |
| 4 | 在输入框手打 `/compact` 普通发送（非面板） | 与官方一致：确定性执行（这是官方语义，不是面板专属）；手打目录外 `/foo` = 模型普通回复（官方 ④ 分支） |
| 5 | 点 Plan chip 进入计划模式 | chip 变橙；Mac TUI 同会话可见已进 plan（plan_mode.json 持久化）；杀掉 iPhone 重连 chip 仍橙 |
| 6 | 计划模式下发消息 | 模型按计划模式行为（先计划后执行）；每条消息带 `_meta.mode` 收敛兜底不产生可见异常 |
| 7 | 模型产出计划 → 审批卡批准 | 既有两键卡出现并可批准；批准后 chip 回灰（官方 CMU 回流） |
| 8 | 在 **Mac TUI** 切 plan / 跑 `/compact` | iPhone 无操作时 chip/命令行随后同步（leader 广播 + polling 兜底） |

真机点击须 owner；agent 只做日志/Management 核验。#4 是「官方语义 vs 自造防线」
的验收锚点；#5/#8 是三路可观测的验收锚点。

## 10. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 1.0.13 活体形状与 1.0.16 源码漂移（P1-P8 任一） | §0.1 门：活体优先，漂移记录；无法取证项移 §11 开放项，不猜 |
| execute 白名单依赖目录缓存，冷启动缓存缺失 | 放行 + 记录（§6.1）；fail 点在官方 resolve（目录外命令本就落入 ④，官方行为） |
| mode-only 子进程成本（每次切换一个短命 spawn） | 与一次空 turn 同量级；切换是低频操作；`_meta.mode` 兜底使丢失可容忍 |
| `_meta.mode` 与 set_mode 双通道竞争 | 官方幂等注释背书（reconcile idempotent）；两通道同值无害 |
| `permission_mode` capability 语义变化影响既有 iOS 权限菜单 | D3 只迁 plan 键；其余 5 键行为零变化；回归矩阵 #1 |
| iOS 门控放宽后其他 backend 误显示 | `supportsSessionCommands` 仅 dsh-web/grokbuild 广告；capability 测试断言三家无 |
| 命令行 user message 与既有 transcript polling 投影冲突 | P6 取证行形状；若 1.0.13 命令行有 displayText 等 meta 差异，映射层处理，不改投影身份公式 |

回滚：grokbuild 去掉 `SessionCommandCatalog`/`SetMode` 真实现 → capability 消失
→ iOS 门控自动退回（dsh 期形态）；`_meta.mode` 为 omitempty additive，旧客户端
零影响；codec ACU/CMU 消费即使回滚面板也保留（状态捕获无副作用）。

## 11. 与后续案的边界 + 开放项

边界：本项关闭 think.md「Grok iOS Plan 只写 agent 内存」与调研 §7 的 Grok 命令
面板两个口子。不关闭：skills 面板深面（目录展示即可执行，但 skills 的
`kind:"chat"` lane、skill 管理不做）、workflow 进度投影、`ask` 模式、permission
其余键接真、MCP elicit、interjection（Phase B）、prompt-only `loop` 特化。

开放项（Phase 0 取证定夺，不猜）：

1. P3 catalog 单例子进程调 `x.ai/commands/list` 的可行性（cwd 传递方式）——
   不成立则冷通道退化为 per-turn 子进程 ACU。
2. P6 builtin settle 的可透传文本是否存在——不存在则 `ResultText` 恒空，反馈全
   靠投影（诚实映射，不造文案）。
3. P8 `summary.json` 是否带 mode——决定冷启动 chip 恢复是否需要额外拉
   `updates.jsonl` 尾部。
4. D1 白名单终表（owner 裁决后写入实现说明）。
