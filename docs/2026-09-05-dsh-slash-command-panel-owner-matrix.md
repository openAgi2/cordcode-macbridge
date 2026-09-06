# DSH「/」命令面板 — owner 真机验收矩阵执行说明（§8 落地版）

对应方案 [docs/2026-09-04-dsh-slash-command-panel-implementation.md](2026-09-04-dsh-slash-command-panel-implementation.md) §8。
本文档是发给 owner 的执行说明 + agent 侧核验方案（矩阵 #3/#6 的「不 reset」必须日志证明，不能只看 RPC 绿灯）。

## 0. 环境状态（agent 侧已完成，2026-09-05）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| Mac runtime | ✅ 已覆盖安装并重启 | Release 构建（commit 6197da5b8f89，built 2026-09-05T07:15:55Z）→ killall 主 app + pkill runtime → `/Applications/CordCodeLink.app` 重装；8777 新 PID 71759（旧 20120），lstart 15:16:56 晚于构建；磁盘二进制 sha256 与构建产物一致 |
| session_commands 能力广告 | ✅ Management API 活体核验 | dsh-web 含 `session_commands`；claude / codex-remote / grokbuild / opencode-web 均不含（hello_ack 同一派生路径 BuildAllAgentDescriptors） |
| 启动日志 | ✅ 无报错 | runtime_ready pid=71759，五 driver 注册含 dsh-web，外部 3080 座位 adopted |
| iPhone App | ✅ 已装 iPhone 16 Pro（2026-09-05 15:29 自动安装） | 按 iOS 仓 CLAUDE.md 既定 runbook：`scripts/run.sh device --device <UDID>`（run.sh 自动探测空手：xctrace 把 WiFi 配对设备标 Offline，CLAUDE.md 规定以 devicectl 为准）；Debug 带签构建+安装+启动 exit=0。身份门：来源 `plan/approval-layer-ios`（HEAD b94289df + 16 项功能改动），产物含 session_commands/双 RPC/双本地化键/2026-09-05 全部特性字符串。装后 15:30:15 设备重连（原配对保留，hello_ack sent） |

## 1. owner 操作步骤（产品语言，共八步）

环境：本机 dsh 0.1.1-rc.2 座位；iPhone 连已覆盖安装的 CordCode Link；DeepSeek Harness 会话。
每步做完打个时间戳（告诉 agent「第 N 步 done」即可，agent 按日志时间窗取证）。

> **2026-09-05 20:0x 返工③（官方化）后本表为最新验收面**：反馈形态从「提示卡」改为
> 官方 Web 的**时间线持久命令行** + **输入框 Plan × 状态 chip**；面板只保留
> compact/goal/plan 三条（owner 裁决）；不再有任何成功弹窗。

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | 打开 DSH 会话；再打开一个 Claude（或 Codex/Grok/OpenCode Web）会话 | DSH 会话输入框出现「/」按钮；其他 backend 会话不出现 |
| 2 | 点「/」 | 出现 **3 条命令**：plan / compact / goal（官方英文描述+参数提示）；没有 permission/export/feedback，无技能组、无 model |
| 3 | 点 `plan` | 面板收起，**输入框被认领**：出现 `/plan `（带尾随空格，光标在空格后）+ 暗色幽灵提示「描述你的任务以生成计划」，开始打字提示即消失；直接发送 = 进计划模式（时间线出现 `plan` 命令行 + 橙色 Plan × chip，占位符切换）；也可以接着打 `off` 或一段说明再发送（发送的是整行 `/plan …`，走命令执行，不是聊天）。手动在输入框打 `/plan …` 发送走同一判决链 |
| 4 | 点 Plan × chip | 计划模式退出（时间线出现对应 `/plan off` 命令行 settle；chip 消失，占位符复原）；之后让模型产出计划再测既有计划审批卡仍能批准执行 |
| 5 | 输入框既有权限入口选预设 | 现有权限预设菜单（Read Only / Workspace Write / Full access）行为不变（permission 已不在面板，走输入框既有入口） |
| 6 | 点「/」→ `compact`：立即执行；点 `goal`：认领输入框 | **compact（无参数）**：点击立即执行，时间线出现 `compact` 命令行 + 官方文案，**不是一条用户气泡；不 reset 解码器**。**goal（带参数）**：点击后输入框变为 `/goal ` + 幽灵提示「输入目标，智能体将持续执行」（已有进行中目标时为「当前目标进行中。可输入 edit 修改 / pause 暂停 / resume 继续 / clear 清除」），打任务文字后发送整行 `/goal …` 执行；结果出现在时间线，多行可点开，失败整行红色+红点 |
| 6b | （返工 ⑤ 新增）goal 认领状态下挂附件再发送 | 提示「/goal 不接受附件，请先移除附件」（官方 refuseAttachments 文案）；输入框整行与附件原地保留，命令不执行、消息不发。另：未知 `/xxx` 或 compact 带参数发送 = 当普通聊天消息发（官方 default sink） |
| 7 | 刷新/重开会话 | 历史命令行仍在时间线（持久节点，非一次性提示）；Mac web 端同会话看到同批命令行 |
| 8 | （2026-09-05 返工④ 新增）在 Mac web 端用 `/goal` 发起一个目标（输入任务并启动） | iPhone 同会话**输入框上方出现「进行中的目标 ×××」横条**（官方 GoalBar 形态）；文本区 tool call 行可见 subagent 描述（与 Mac web 一致）；点横条上的暂停/恢复/编辑/清除，动作生效且错误（如有）以官方 `消息 (code)` 形式内联显示；目标完成后横条消失 |

> 已删除的旧期望：#3/#6/#7 的「命令已执行」提示卡（返工②产物，返工③按 owner 官方化裁决移除）；
> #2 的 6 条命令列表；#7 的 export 步骤（owner：下载 zip 不需要在 iOS 做）。
> 真值：官方 Web 截图（18.49.44）显示时间线 8 条命令行（裸名 + settle 原文、error 红行）+ Plan × 橙 chip。

## 2. agent 侧核验方案（日志/Management，不碰设备 UI）

日志源：`"$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"`。
RPC 轨迹来自通用访问日志（handlers.go:1109 `go-bridge: RPC request method=… backendId=… requestId=…`）；
解码器重置来自 codec（codec.go:772 `dsh-web: session codec reset`，reason 携事件名）。

按 owner 报的时间戳切窗（`time=2026-09-05T…` 前缀），逐步核验：

| # | 正向证据（必须出现） | 负向证据（必须不出现） |
| --- | --- | --- |
| 1 | Management capability 已活体核验（环境项）；按钮出现=设备 UI，owner 目视 | — |
| 2 | `method=list_session_commands backendId=dsh-web` 一次 | 无 `execute_session_command`（此时只列表未执行） |
| 3 | `method=execute_session_command backendId=dsh-web`（点 plan 那次） | **窗口内无 `session codec reset` 且 reason 含 `plan/mode`**；无 `session/prompt`/send_message 轨迹把 `/plan` 当聊天发出 |
| 4 | 计划评审事件照常流转（无新日志面） | 无 `session codec reset`（plan 评审为既有受支持事件） |
| 5 | `method=set_permission_mode`（菜单选择生效，走既有写路径） | **无 `method=execute_session_command` 对应 permission 点击**（D1：面板 permission 分流菜单，不发命令） |
| 6 | `method=execute_session_command`（compact/goal 那次） | **窗口内无 `session codec reset` 且 reason 含 `compaction/start`/`compaction/prune`/`compaction/summary`/`compaction/end`/`goal/change` 任一** |
| 7 | `method=execute_session_command`（export 那次） | 无 RPC 错误（`execute_failed`）；提示卡出现=owner 目视（返工后 export 不再静默，见 §2.4） |

取证据命令（agent 执行，窗口起止代入实际时间）：

```bash
LOG="$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"
# RPC 轨迹（全矩阵）
grep "go-bridge: RPC request" "$LOG" | grep -E "session_command|set_permission_mode"
# 解码器重置（#3/#6 的 P0 负向证据：修复前命中七事件之一即整条流重置）
grep "dsh-web: session codec reset" "$LOG" | grep -E "plan/mode|compaction/|goal/change|feedback/record"
# execute 失败轨迹（#6/#7 官方错误可见时的对应面）
grep -E "list_failed|execute_failed" "$LOG"
```

判定：#3/#6 的负向 grep 在对应窗口**零命中** + 会话后续事件正常滚动（owner 确认能继续对话）= codec 不 reset 成立；任一命中 = P0 失败，按方案 §9 回滚预案排查（但 Class ② 扩容本身有单测锁定，命中更可能是新事件形状，须回官方源码对锚点）。

### 2.1 首批活体轨迹（2026-09-05 15:30，安装后 owner 自发开测）

| 时间 | 轨迹 | 对应矩阵 |
| --- | --- | --- |
| 15:30:15 | 设备重连 `client connected` + `hello_ack sent`（dev_c5ad…，原配对保留） | 环境 |
| 15:30:27 | 打开 dsh-web 会话（set_observation_scope sessions=1 + projection hydrate） | #1 前置 |
| 15:30:33 | `method=list_session_commands backendId=dsh-web` | #1 正向（按钮出现且可点）+ #2（面板拉目录） |
| 15:30:41 | `method=execute_session_command backendId=dsh-web` | 首条命令执行 |
| 全程 | `session codec reset` 计数 = 0 | #3/#6 负向证据累积中 |

### 2.2 返工：execute 载荷缺 images（2026-09-05 17:47 owner 报障 → 18:03 修复上线）

owner 真机点 `/compact`、`/plan` 均弹官方网关错误 `commands/execute: args fields do not match the descriptor: missing "images"`（截图两张 + 桥日志 17:45:31 execute 一次）。诊断与修复：

- **根因**：dsh-web 适配器 execute 载荷只发 `{agentId, line}`。部署座位的网关按描述符强制 `images` 必填数组（rc.2 官方 fixture 允许省略、当前 HEAD 已改名 `submittedAttachments`——座位版本在两者之间；以 3080 活体探测为准：缺→精确复现报错、`[]`→成功、`null`→boundary 拒绝）。
- **修复**：`images: []` 无条件携带（唯一双代网关兼容形；空数组=官方「无附件普通调用」语义）。纯 Mac runtime 侧改动，**iPhone 无需重装**。单测锁形（args.images 精确 `[]`）+ 双包定向测试全绿 + Release 重建覆盖安装（8777 新 PID 76729）。
- **附带修复**：phase2 schemaRevision 升级时 hello-ack wire fixture 未再生（既有失败测试），按既定 `CCCODEGEN_FIXTURES=1` 再生，diff 仅 schemaRevision 一处。
- **owner 操作**：直接在 iPhone 上重试矩阵 #3（/plan）与 #6（/compact 或 /goal）即可；其余步骤不受影响。#5（/permission 走 set_permission_mode 不发命令）与报障无关，无需重试。

### 2.3 images 修复后首批重试轨迹（2026-09-05 18:05，takeover 会话取证）

runtime PID 76729（lstart 18:03:21，含 images 修复）窗口内：

| 时间 | 轨迹 | 判定 |
| --- | --- | --- |
| 18:05:32 | 设备重连（dev_c5ad…，connection generation 2，hello_ack sent）+ projection hydrate（session-7db18991…，headRev=25） | 环境前置 |
| 18:05:38 | `method=list_session_commands backendId=dsh-web`（req_16） | 面板拉目录（#2 复现） |
| 18:05:40 | `method=execute_session_command backendId=dsh-web`（req_17） | 一次命令执行 |
| 18:05:40 后 | `execute_failed` / `list_failed` / `level=ERROR` / `missing` 全零命中；`session codec reset` 计数 = 0（整个 PID 76729 窗口） | **images 修复在真机 wire 上验证通过**（对照 17:47 报障：同操作此前必弹 `missing "images"` 官方错误） |

注：本窗口仅一次 execute（#3 或 #6 之一；INFO 访问日志不含 `line` 参数，无法从日志区分命令名）；无 `set_permission_mode` 轨迹（#5 未做）、无第二条 execute（#7 未做）。owner 未报步骤时间戳，七步矩阵仍待 owner 走完；blocked 三项维持 external。

### 2.4 二次返工：执行成功无反馈（2026-09-05 18:2x owner 报「6条命令，点击任意一条都没反应」→ 18:39 修复上线）

**报障与诊断**：owner 点面板命令全部「没反应」。取证证实 RPC 与官方执行**均成功**（18:29 req_117/118、req_121/122 到桥；dsh session JSONL：compact 三次 18:05:40/18:29:25/18:29:42、plan/mode 18:30:43——owner 会话当时已进入计划模式、18:30:52 另一 run+done 为 goal 或 export；零 execute_failed/ERROR/codec reset）。根因是 **UX 反馈缺失**：iOS 成功路径只收面板；命令副产物事件按方案 §3 有意 known-drop 不进 iPhone 时间线，官方 settle `result.text` 也被丢弃——「成功」与「没反应」在 iPhone 上无法区分。方案未设计前端成功反馈属设计缺口（owner 批评成立）。

**官方源码核验（source-first，/Users/jacklee/Projects/deepseek-harness，HEAD dsh-v0.1.3-alpha.1 = d347e70）**——官方 Web 对命令反馈的真实呈现：

- `packages/client/ui-commands/src/client/service.ts` `execute()`/`runDetached()`：官方 **composer 从不回显命令结果**；成功反馈的唯一通道是 host 持久化的 `command/run`+`command/done` 生命周期对经 mux 广播，**在每个 tab 渲染为持久 flow node**；仅 admission 失败（未知命令/拒收/attachment 拒绝）落 composer 错误通知。
- `packages/client/ui-chat/src/client/conversation-nodes/command.ts`：flow node 按 commandId 折叠 run/done 为 `CommandNode{name, args, outcome{kind, text}}`。
- `packages/client/ui-chat/src/client/chat/GenericCommandCard.tsx`：卡片标题=命令名，正文摘要=**`outcome.text` 官方文案逐字**（无文案回退本地化「完成/失败」；未决=运行中；多行可展开；error 红点）。
- `packages/interaction/commands/src/types.ts`：`CommandResult{kind, text?}` / `CommandExecution{commandId, result}`——桥透传的就是这份同源真值（同一 `normalizeResult` 产物）。export 官方本就返回有意义成功文案，非静默命令。

**修复（双仓 settle 透传）**：Mac——`core.SessionCommandResult{CommandID,ResultKind,ResultText}` + catalog 签名扩展；dsh-web 全失败路径 zero+error、成功路径透传 settle；`execute_session_command` 响应加可选 `commandId/resultKind/resultText`（零值省键，additive，schemaRevision 维持 2026-09-05）。iOS——解码透传字段，成功后弹「命令已执行」提示卡，**正文=官方 resultText 原文**（空回退命令行本身）；permission（#5）仍走菜单无提示卡。提示卡正文与官方卡片的 `outcome.text` 是同一份官方文案。

**有意差异（recorded）**：官方以持久时间线 flow node 呈现；CordCode 按方案 §3 有意把 `command/run|done` 列入 codec 类② known-drop（不进 iPhone 时间线，streams_test.go 锁定），故用执行后提示卡承载同一文案。若要求时间线节点完全对齐官方，需 codec 解禁+新节点类型，属独立后续任务（owner 裁决另案）。

**验证与部署**：Mac `go test ./agent/dsh-web ./go-bridge` 双包 ok（含 `TestExecuteSessionCommandHandlerSettlePassthrough`：settle 全字段+静默零值省键）；协议文档（bridge-v1.md + types.ts）双仓同步一致。iOS `SlashCommandPanelTests` 8/8 绿（含 `testExecuteResultDecodesOfficialSettle`）。部署：Mac Release 重建（built 2026-09-05T10:39:13Z，二进制含 `resultText` 特征串）→ 纪律覆盖安装，8777 新 PID 57181（lstart 18:39:57），启动零 ERROR、dsh-web 座位 adopted；iPhone 16 Pro 经 `scripts/run.sh device` 重装并启动。

**owner 操作**：在新装的两端上重跑七步矩阵（§1 期望已更新：#3/#6/#7 现在应弹「命令已执行」提示卡）。注意 owner 的 dsh 会话在 18:30 已被切进**计划模式**（plan/mode 事件），若在其中测试命令行为，先点 `/plan`（面板）退出计划模式再测，或直接开新 dsh 会话。

### 2.5 三次返工：反馈形态官方化——时间线命令行 + Plan chip（2026-09-05 19:0x owner 提供官方截图 → 20:04 修复上线）

**报障与裁决**：owner 指出前两轮的「提示卡/各说各话」与官方 Web 不对齐，提供官方真机截图（18.47.48 计划模式输入框、18.49.44 连点 /plan /compact 后界面）。glm-vision 读图确认官方形态：**时间线持久命令行**（裸命令名标题 + settle 原文摘要，error 行红色红点，多行可展开）+ **输入框 Plan × 橙色 chip**（点按退出计划模式）+ 占位符「描述你的任务以生成计划」。owner 同时裁定范围：**面板只做 compact/goal/plan 三条**（permission 输入框已有入口；export/feedback 不属于 iPhone 面）。

**官方源码核验（source-first，同 checkout d347e70）**：

- `ui-chat/.../GenericCommandCard.tsx`：state = running|ok|error（`stateOf`：outcome null→running；kind error→error）；标题 `node.name ?? t('command.title')` **裸名无斜杠**；摘要 = settle text ?? locale（执行中…/指令失败/已完成）；正文仅当 text 含 `\n`（可展开，keepContentWhenOpen）；error 摘要/正文红 + 红点图标。
- `ui-plan/.../PlanModeControl.tsx`（PlanChip）：`plan===undefined→null`；`target = plan.pending ? !plan.active : plan.active`，`!target→null`；off() = leaving → exitPlanMode() → 失败 inline 红字「退出 plan mode 失败」（**非弹窗**）；disabled 半透明。
- `ui-chat/.../InputBar.tsx`：占位符同 target 公式切换 `placeholder.plan`。
- `ui-commands/.../records.ts`：CommandNode name/args 在 run 落窗外为 null（done-only 行），outcome{kind,text?}。

**修复（双仓）**：Mac——codec 解禁 command/run|done：折叠为 CommandRow{name,kind,text} 进 projection system turn part（词表 running|success|error，未知 kind fail-closed 丢 part 不猜状态）；`planMode{active,pending}` 进快照/patch（官方 mode 事件映射）。iOS——message-web 新 `CommandTurn.tsx` 官方卡片镜像（含官方 figma SVG、sweep 动画、reduced-motion、locale 注入链）；UIKit Plan chip（ChatInputAccessoryView，官方 off 公式 + inline 错误）+ 占位符切换；planMode-only patch 经 ProjectionStore NotificationCenter 广播驱动（空 changeset 不触发 $messages）；面板白名单恰三条；**删除成功弹窗**（错误弹窗保留）。touch 面：MessagePart.command 新 arm 补齐 SessionSearchIndex（索引裸名+settle）/DetailSheets/AssistantTimelineRenderSpec。

**验证与部署**：web `npm test` 313/313 绿（新增 CommandTurn 6 例）；iOS 定向 14/14 + blast 9 类 144/144 绿；Mac `go test ./agent/dsh-web ./go-bridge` ok（codec 解禁后 streams_test 同步修订）。部署：Mac Release 重建（built 2026-09-05T12:03:17Z）→ 纪律覆盖安装，8777 新 PID 35873（lstart 20:04:25），上线即服务 `list_session_commands` RPC（req_41+）；iPhone 16 Pro 经 `scripts/run.sh device` 重装启动（20:03）。队列：`rework-official-timeline-plan-chip-{impl,tests}` done，`-regression` = 本矩阵 §1 新七步。

### 2.6 四次返工：goal 横条与 subagent 显示缺失（2026-09-05 20:19 owner 报障 → 21:30 修复上线）

**报障**：owner 在 Mac dsh 用 `/goal` 起任务，Mac web 输入框上方出现「进行中的目标 ×××」横条、文本区有 tool call subagent 显示；iPhone 同会话**两者都看不到**。

**官方源码核验（source-first，同 checkout d347e70）**：

- `packages/client/ui-goal/src/client/GoalBar.tsx`（全文逐行）：`goal === undefined || null || phase==='complete' || id===clearedGoalId → 渲染 null`；phase 标签 进行中的目标/已暂停的目标/受阻的目标；动作 pause（仅 active）/resume（仅 paused）/edit+clear（恒有）；edit = 行内输入条（draft=objective，Enter 保存、Esc 取消、trim 后非空才可保存）；`runAction` pendingRef 同帧防重复；goalId 变化复位 editing/error/clearedGoalId；clear 成功记 clearedGoalId（抑制到不同 id 到达）；actionError 行内 `${message} (${code})` role=alert；blocked tooltip = blockedReason.message；图标 IconGoalOutline16。
- `packages/goal`：投影 whole-snapshot 语义；`goal/change` 不是时间线行。

**修复（双仓）**：

- Mac（前窗完成，`go test ./agent/dsh-web ./go-bridge` ok）：goal 投影折叠进 session 级 `goal` 快照/patch（clear 以 `{phase:"none"}` 编码、字段缺席）；`mutate_session_goal` RPC（action pause|resume|edit|clear，session.write，goals/<verb> 官方透传，失败 `goal_failed` 原文）；`session_goal` capability（SessionGoalController 推导，仅 dsh-web）；subagent tool 行标题改带 description（owner 报障第二项）。
- 协议（本窗）：bridge-v1.md + types.ts 增加 `session_goal` 事件、`goal` 投影字段、`mutate_session_goal` RPC、`session_goal` capability 四节；`BridgeGoalView` id/revision/objective 对 `phase:"none"` 缺席标 optional；同步补齐 schema BridgeEventName 的 session_command/session_plan_mode/session_goal 组（②③遗留 gap）；双仓 mirror 逐字节一致。
- iOS（本窗）：`SessionGoalView`（含 phase none 解码：字段缺席→占位、渲染层与 nil 同等）+ ProjectionStore goal merge（present 替换/absent 保留）；`GoalBannerView` 官方镜像（四短路、clearedGoalId 抑制、goalId 变化复位、pendingRef 防重、edit 行内条、`${message} (${code})` 内联错误、官方尺寸 token 36/12/0.5/4/12/10/14/26/6）；挂载 ChatInputAccessoryView（横条在卡上方，visible 时 36+6）；ChatUIKitContainerView 双驱动（$messages sink + sessionProjectionDidChange goal-only patch）+ `mutateSessionGoal` wire（objective omitempty）；capability 只门动作按钮（横条状态对全体客户端投影驱动）；zh/en 11 本地化键。

**验证与部署**：iOS 定向 `GoalBannerProjectionTests` 8/8 + 邻近 5 类（SlashCommandPanel/AttachmentStrip/GeneratingGlow/LiveOnlyProjectionState/ChatViewModelSessionSyncV2）全绿；期间发现并顺手修复 §2.7 所述 SSV2 测试 fake 的既有断裂。部署：Mac Release 重建覆盖安装 + iPhone `scripts/run.sh device` 重装（见 §3 时间戳）。

### 2.7 顺手修复：SSV2 发送测试 fake 断裂（既有问题，与本任务无关）

`ChatViewModelSessionSyncV2Tests.testSSV2SendWaitsForProjectionWithoutOptimisticTimelineWriter` 在本任务回归运行中稳定失败（`sentContents` 空）。取证：生产 wrapper `CCCodeBridgeBackendClient.sendMessage` 调 `bridgeClient.sendMessageWithOptions`（HEAD 既有代码），而测试 fake 只实现 `sendMessage` → 落协议扩展默认抛错 → 永不记录。本任务对 CCCodeBridgeBackendClient/CCCodeBridgeClient 的 diff 纯增量（零删除），断裂在 b94289df 即存在。修复：fake 改为实现 `sendMessageWithOptions`（协议默认 `sendMessage` 会转发到它），套件全绿。测试专用改动，无生产行为变化。

### 2.8 五次返工：带参数命令点选即裸发——改为官方认领输入框（2026-09-05 23:0x owner 报障 → 23:13 修复上线）

**报障与根因**：owner 真机点 `goal` 后直接发出裸 `/goal`，回显官方 usage 文案，没有输入任务的机会——「人类没法用」。owner 裁决完全成立：方案 **D3/§6.2 明文规定「点选一律 execute line="/"+name」是自造交互**；实现 agent 按文档实现，源码优先门当时只读了 list/execute/handler/事件，**没读 ui-commands 的 `dispatch()`**——官方交互判决表在源码里早就写好，方案文档把它改成了另一套。owner 同轮指示：幽灵提示文案也不能贴语法原文，正式 UI 用文案覆盖（官方 locales）。

**官方源码核验（source-first，同 checkout d347e70）**：

- `ui-commands/.../service.ts dispatch()`：点选只看一条——命令有没有声明 `input`。声明（goal/plan）→ `leadingClaim` **认领输入框**（token `/name ` 带尾随空格），不执行；未声明（compact）→ `runDetached` 立即执行。`matchEnter()`：提交 `/`-开头整行 → 带 input 命令整行执行（args-tolerant）；无 input 裸 token 执行；无 input 带参数/未知命令 → default sink 普通聊天发送；命令路由 + 附件 → `refuseAttachments`（草稿与附件保留）。
- 裸 `/goal` 在官方 handler 是 **show（查看）**：无目标时回 usage 文案——旧实现正是把用户放进了这条死胡同；官方菜单根本不产生这条路。
- `ui-conversation/.../skeleton/InputBar.tsx:356-374` + `locales.ts`：幽灵提示算法 = `hint.<name>` 动态查词典（goal 且 `hasGoal`（goal 投影非 null）→ `hint.goal.active` 变体），查到用译文、查不到回退机器 hint；zh `hint.goal`「输入目标，智能体将持续执行」、`hint.goal.active`「当前目标进行中。可输入 edit 修改 / pause 暂停 / resume 继续 / clear 清除」、`hint.plan`「描述你的任务以生成计划」（与 plan 占位符同串）；附件拒绝文案 `command.attachmentsUnsupported`「/{command} 不接受附件，请先移除附件」。
- `ui-conversation/.../facade.ts beginCommand`：claim token **原位替换**触发 span，span 后参数保留（连续换选命令不叠加）。

**修复（纯 iOS 单仓，Mac/协议零改动——execute wire 本就透传整行）**：

- `SlashCommandSelection.route(for:)`：permission → 菜单；hint 空（compact）→ 立即执行；hint 非空（goal/plan）→ `.claim(token: "/name ", hint:)`。
- `ChatInputAccessoryView.claimSlashCommand`：token 原位替换（官方 span 语义，连续换选不叠加）+ 光标落 token 后 + 拉起键盘；幽灵提示按官方文案算法（词典译文胜出 / goal.active 变体 / 回退机器 hint）；draft 丢前缀即释放 claim（官方 draft-changed watch）；发送即消耗。
- `ChatUIKitContainerView.handleSend`：`/`-开头整行 → `SlashCommandLineAdjudicator`（matchEnter 镜像）判决：整行执行 / 普通发送；附件在场 → 官方拒绝文案 + 草稿附件保留；执行失败还原整行草稿。目录拉取失败落普通发送（default sink，不吞输入）。
- 面板 footer 文案同步（不再写「无参数输入」）。

**验证与部署**：定向 `SlashCommandPanelTests`（+4 判决表/claim 生命周期用例）+ `GoalBannerProjectionTests` 18/18 绿，邻近 `ChatInputAccessoryGeneratingGlowTests` + `ChatViewModelSessionSyncV2Tests`（67/67）绿；首跑暴露 claim 原位替换缺口（连续 claim 叠加 `/goal /goal `），按官方 beginCommand 修正后全绿。iPhone 16 Pro 经 `scripts/run.sh device` 重装启动（23:13，装后 23:13:22 设备重连 hello_ack sent；产物含 claim/幽灵提示/判决表特性）。Mac runtime 无改动，维持 21:29 部署（PID 630）。

**有意差异（recorded）**：官方 Lexical 编辑器把 `/goal ` token 本身渲染为警告色 token——owner 裁决 iPhone 不必复刻 Lexical 着色，必须复刻的是「点 goal 之后人还能打字、发送的是整行 `/goal …`」；该不变量已由测试锁定。

### 2.9 六次返工：goal 轮 live 流被 codec reset 拆流 + 输入行不显示 + 页面跳动（2026-09-05 23:27 owner 报障 → 2026-09-06 00:xx 修复上线）

**报障**：owner 真机发起 `/goal 创作钢铁侠故事10000字左右，并写入 /tmp/demo-plan102.txt`（23:25:18 execute req_58），交互流程与 Mac 3080 网页一致 ✅，但两处 bug：(a) iOS 没把发送的任务显示成命令行——输出直接在上一个回复后继续流式；(b) iOS 消息页持续上下跳动（虽已置底）。GLM 读图确认：goal 横条 ✓、时间线仍停在上一个任务（demo-plan101）、仅折叠「正在思考」+活动行，无命令卡/输入行/上下文注入行。

**根因（座位日志 + 官方源码双证，session-3eacd40e 共 21K 事件、5 次 reset）**：

1. **seq 1481 `user/message source.kind "goal"`（P0）**：goal_round 注入帧。我们的 codec 对未知 `source.kind` 一律 reset——但官方 `ui-chat message.ts`：`kind != "user"` 全部进 ContextMessageNode（合并扩展，从不拆流）。reset 即 `newSessionCodec` 整值替换：commands/plan/goal 投影状态全丢、turn 3 流被拆、后续帧重新采纳。
2. **seq 16586 `assistant/chunk block-end`**：todo_write 的 delta 流是带空格 JSON、block-end 是归一化紧凑 JSON——我们的「settle 与 delta 累积比对」必然分歧 → reset。官方 `partial.ts`：block-end 无条件 `blocks[index] = block` 整值替换，零校验（sparse on purpose）。
3. **seq 16589 `assistant/message`**：settlement 与 delta 并存同理（官方：finish 后的 assistant/message supersedes the partial，从不比对）。
4. **seq 9 `agent-instructions` / seq 11 `skill-catalog`**：基线注入，座位每次收编回放都触发 reset（22:38 即有一次）。
5. **症状 (a) 的另一半**：官方 goal 显示有四件套——横条（④已做）+ **`/goal …` 右对齐用户气泡**（`ui-goal goal-command-input.ts`：name==="goal" 专属，`goalCommandText = "/"+name+args.TrimRight`，`anchorSeq: seq-0.1` 卡上方）+ GenericCommandCard + 上下文注入行；我们一、二都没做，座位的 goal 注入帧又被当成 reset 帧丢了 → 时间线上这条命令完全不可见。
6. **症状 (b) 跳动**：每次 reset → 新 codec 重新 adoptTurn → turn 重发 TurnStarted → reducer upsert 整 turn → iOS 全行重排；goal 轮内 5 次 reset（23:25 一次 + 23:32 两次 + 基线两次）在 1 万字流式期间反复触发。修复即消除该 churn 源。

**修复（Mac codec 三类 reset 全部按官方语义移除 + 输入行全链路补齐）**：

- `applyUserMessage`：`source.kind != "user"` → 已知丢弃（ContextMessageNode 语义；history.go 冷拉同形已知丢弃）；不再 reset。
- `applyAssistantChunk` block-end / `applyAssistantMessage` / `applyToolCall`：全部改官方整值替换语义（block-end 无 open 校验、无类型校验；tool/call 是权威执行事件）；删除 delta 累积比对（`toolArgsAccum`/`stepText`/`stepReasoning`/`blockAccum` 死状态全清，openBlocks 简化为 index→blockType 标记）。
- 输入行链路：codec `applyCommandRun/Done` 携带 `InputLine`（goal 专属 goalCommandText；settle 续接——reducer part 整体替换不带则气泡消失）→ core `SessionCommandEvent.InputLine` → wire `inputLine` → reducer part `commandLine` → hydrate `part["line"]→inputLine` 同形。
- iOS：mirror `commandLine` + `MessageCommand.inputLine`（可选解码不破旧快照）+ web 臂 `GoalCommandInput` 气泡（右对齐、行首 `/goal` 命令 chip code 字体 accent 色、objective 普通文本、无消息操作；官方 GoalCommandInputView.module.css + user-text.module.css slashChip 逐项映射）+ UIKit 降级面前置输入行 + 搜索索引自动覆盖；MessageWeb bundle 重建入 Resources。

**验证**：真实座位回放测试 `streams_goalround_test.go`（fixture = 三个连续 seq 窗口原样：基线 1-14 / goal 1469-1492 / 权威替换 16578-16596，跨窗口以 mid-log join 语义衔接）4/4 绿——零 reset、输入行 run+settle 双携带、turn 3 执行链完整、基线注入不拆流；reducer `TestReducerSessionCommandGoalInputLine` + hydrate `inputLine` 转换用例绿；`agent/dsh-web`+`go-bridge` 全量绿；message-web 10/10（气泡语义门禁）+ 全套 317 绿；iOS `CCCodeTests/SlashCommandTimelineAndPlanModeTests` 7/7 绿（2026-09-06 00:32，模拟器 erase 恢复安装后执行；含 goal 输入行四段——`commandLine` 解码/缺键 nil、映射 `inputLine`、降级面前缀、web 契约编码+旧载荷解码；期间补修 `MessageCommand` 显式 init 默认 nil，仅测试编译面，真机 00:27 产物行为不受影响）。

**有意差异（recorded，待 owner 裁决）**：官方第四件套「上下文注入 · goal」行（`ContextInjectionRow`，locale `message.contextInjection`）本轮未实现——它是 goal_round 注入帧的可见化（「goal 注入过上下文」这一事实的披露行），官方锚点 message.ts + ContextInjectionRow.tsx 已记档；若 owner 需要可作下轮增量。plan/compact 无输入行气泡（官方 goal 专属）。

**部署**：Mac Release 覆盖安装（2026-09-06 00:23，见 §3）+ iPhone 16 Pro `scripts/run.sh device` 重装。

### 2.10 七次返工：goal 任务气泡发送后 1 秒消失、任务完成才归位（2026-09-06 01:2x owner 报障 → 01:5x 修复上线，纯 iOS）

**报障**：§2.9 修复后 owner 真机复测——消息页上下跳动已解决 ✅；但发起 `/goal 创作浩克故事1000字左右，并写入 /tmp/demo-plan103.txt`（01:19:46，goal103）后，任务气泡出现约 1 秒即消失，流式正文在上一个回复后继续输出；任务完成（01:21:31）后气泡重新出现，位置正好在已完成任务前面。

**取证（设备 fg-trace.log 双段轮转日志，01:19:45-01:21:31 全窗口）**：

- 01:19:46 补丁 2249/2250/2251 应用，msgCount 9→10，`[PIPE] snapshot items=10`——气泡正常上屏（全量分组路径）。
- 01:19:50 流式首帧到达：`[Render] msgCount=10/11` → `snapshot items=11 reason=is-generating`（第一帧仍全量）→ 紧接 `snapshot items=5 reason=is-generating`，其后整段流式期间 **317 次快照全部 items=5**——发往 WebView 的时间线从 10 项塌缩到 5 项，指令卡不在其中。
- 01:21:30-31 任务完成时刻仍 items=5（增量 upsert）；随后 is-generating 结束 → `isStreamingRenderState` 翻 false → 全量重建 → 气泡归位。

**根因（iOS 消息页流式渲染优化裁掉了指令卡；Mac 数据面逐层核验全部无辜——codec run/settle 双携带 inputLine、wire 事件、reducer `commandLine`、patch/snapshot 序列化、hydrate 冷拉、dsh-web 内核准入均正确）**：`ChatTimelineAdapterUIKit.incrementalGroupedMessagesDuringStreaming`（流式期间只重算尾段以保帧率）把时间线切成「最后一条 user 消息之前的缓存前缀 + assistant 尾组」。而 `/goal` 命令卡是 **system 消息**（官方投影 `Role: "system"`），恰好落在「最后一条 user 之后、流式 assistant 之前」——既不在前缀里（前缀止于最后 user 组），也不在尾组里（尾组只取 `.assistant`），**整组被丢弃**；同时尾段既有 settled assistant 也被并入尾组（10 项→5 项的差额由此而来）。流式结束回到全量分组，指令卡自然重现且顺序正确——这正是「完成才出现、位置在完成任务前面」的机制。发送后约 1 秒消失 = 首帧流式内容到达触发增量路径的时刻。

**修复（iOS 单点）**：尾段分组改为逐消息建组——非 assistant 消息（指令卡等 system 行）按全量 builder 同语义各自成组保留，assistant run 仍合并为单个流式尾组（保留「只重算尾段」的流式优化与分组不变量）。Mac / 协议零改动。

**验证**：iOS `CCCodeTests/SlashCommandTimelineAndPlanModeTests` 9/9 绿（2026-09-06 01:45，模拟器；新增两条回归用例锁定两条进入路径——指令卡先于流式入缓存、指令卡与流式 assistant 同帧到达——旧实现下前者 items 塌缩至 2 且指令卡消失）。

**部署**：iPhone 16 Pro `scripts/run.sh device` 重装（01:46 启动，01:46:45 设备重连 hello_ack，dev_c5ad…0646）；Mac 无改动不重发。

**回归要点（§1 #6b 基础上追加）**：goal 气泡发送后**全程驻留**（流式期间不再消失），位置保持在流式正文之前；任务完成后无跳变。

### 2.11 八次返工：重开 goal 会话后命令卡聚簇、多轮输出合并成一条消息（2026-09-06 01:49 owner 报障 → 02:10 修复上线，纯 Mac）

**报障**：owner 重开此前测试 goal 的 session（截图 2026-09-06 01.49.21.jpg）——期望按「goal 命令1 → 执行情况1 → goal 命令2 → 执行情况2 → …」交错展示；实际是「执行情况1+2+3+4 首尾相接成一条消息，然后 goal 命令 1-4（截图实为 5 张卡）聚簇其后」。

**取证（三层证据链，全部指向 Mac 冷拉归属）**：

- 真值层：seat journal（session-3eacd40e…，21K 行）全局序——`command/done` 恒落在 `turn/end(N)` 与 `turn/start(N+1)` 之间，goal 轮**没有** kind=user 的 user/message（kind=goal 注入行按官方语义不进时间线）→ `mapHistoryEvents` 折叠出的 entries 本身**交错正确**（appendCommandEntry 内联在 done 位置、flushTurn 在 turn/end 位置）。
- 重建层：dsh-web 无持久化 checkpoint，01:23-01:48 之间投影被逐出后于 01:48:01 冷重建（go-bridge.log req_10 `sinceRev=0 headRev=166 → outcome=snapshot`，而 live 会话已到 syncRev 3067+）——错序时间线来自冷拉重建。
- 代码层：`openCodeRichHistoryEntryToProjectionEvents` 的 assistant 归属沿用 OpenCode 平坦折叠（`turnID := *currentTurnID`——只被 **user 行**设置）。goal 轮无 user 行 → 全部 goal 轮 assistant entry 折进**最后一个 user 行**的 turn（输出 1-4 首尾相接成一条消息）；命令行各自成 `cmd:<id>` turn 追加在该 user turn 之后（命令卡聚簇）——与 owner 症状逐项吻合。iOS 侧无任何排序（`SessionProjectionMapping` 按投影 turns 原序渲染），无辜。

**修复（Mac 两点，冷/热身份同源化）**：

1. `agent/dsh-web`：冷拉身份与 live `adoptTurn` 同式——turn 号取自官方 `turn/start {"turn": N}`，assistant/user entry 铸 `dshw-<prefix>-t<N>`（共享 `dshwTurnID`/`dshwSessionPrefix`）；turn 号缺失保持 `sessionID:seq` fallback。goal 轮输出从此各自成 turn，且冷基线与 live 事件在一个身份上合并。
2. `go-bridge`：斜杠命令行是官方 turn 边界——converter 在 command 部件行发射 `session_command` 后重置平坦归属指针，命令后的首个 assistant 行自持 entry 身份，不再折回命令前最后一个 user turn。plan_mode/goal 快照是会话级状态，不构成边界、不重置。

**验证**（组件级，2026-09-06 02:0x）：`agent/dsh-web` 全包绿（含新增 `TestMapHistoryEventsGoalRoundInterleave`——按真 journal 形状 run/change/done 三连 + 无 user 行的 goal 轮，断言 entries 交错序与三轮独立身份；既有 `TestRichHistoryMapsTurnsToolsAndReasoning` 的 assistant ID 断言随身份对齐更新为 `dshw-s-hist-t1`）；`go-bridge` 全包绿（含新增 `TestDSHWebProjectionHydrateInterleavesGoalRounds`——走真 hydrate 管线断言最终 `projection.Turns` = `[t1, cmd:g1, t2, cmd:g2, t3]` 交错；临时禁用修复复跑即失败，失败形态精确复现 owner 症状 `[t1, cmd:g1, cmd:g2]` 输出全并+命令聚尾；converter 级补命令行 reset / plan_mode 不 reset 双断言）。

**部署**：Mac Release 覆盖安装（02:10，PID 28573，lstart 02:10:08 晚于构建，8777 监听者为 /Applications 内嵌 runtime，日志 02:10:13 起正常滚动）；iPhone 无改动不重发。kernel 已随 runtime 重启清空——owner 重开 goal 会话即触发新代码冷重建。

**回归要点**：重开此前 goal 测试 session——命令卡与各轮输出**交错**展示（命令 N 在其执行输出 N 之前），各轮输出独立成段不再首尾相接；新发一条 `/goal` 全流程（横条/气泡/流式/§2.10 气泡驻留）不回归。

### 2.12 九次返工：/compact 30 秒整报错「This operation was aborted」（2026-09-06 02:17 owner 报障 → 02:34 修复上线，Mac+iOS）

**报障**：owner 复测确认返工⑧通过（「goal命令任务已经测试符合预期了✅」）后执行 `/compact`，30 秒整弹错误（截图 2026-09-06 02.23.19.jpg）：iOS 弹窗「dsh api carrier error (commands/execute): Post "http://127.0.0.1:3080/api/commands/execute": context deadline exceeded (Client.Timeout exceeded while awaiting headers)」，其后命令卡红字 "This operation was aborted"。

**取证（三层证据链，全部指向 30s 硬顶）**：

- 座位层：journal（session-3eacd40e…）cmd-21 `compact` 02:17:38 `command/run` → `compaction/start` → **恰好 30s 后**（02:18:08）`command/done kind=error "This operation was aborted"` + `compaction/end error "Request was aborted"`；02:23:19 无新 command/run（截图为 02:18 弹窗滞留）。官方源码（dsh-v0.1.3-alpha.1）：`commands/execute` 的取消 signal 归派发请求所有——调用方断开即中止压缩；`command-compact` expectedFailure 表（busy/cancelled/…）不含该错误，属调用方人为掐断的非预期中止。
- 代码层：`agent/dsh-web/wire.go` 默认共享 `http.Client{Timeout: 30 * time.Second}`——Go 语义中该字段是**硬顶**，per-request ctx 只能缩短不能延长；`handleExecuteSessionCommand` 的 90s 预算从未生效，POST 在 30s 整被掐（iOS 弹窗的 "Client.Timeout exceeded while awaiting headers" 即该字段专属错误文案）。客户端层：iOS `CCCodeBridgeTransport` RPC 默认超时同为 30s，也会先于任何 Mac 预算触发。
- 语义层：`/goal` 等 execute 秒回（轮次异步流式）故此前 30s 无感；`/compact` 在 host 侧同步等一次 LLM 摘要（分钟级），30s 必炸。

**修复（三处协同，调用方预算成为真边界）**：

1. Mac `agent/dsh-web/wire.go`：默认 client 撤总超时；`Call`/`Respond` 经 `unaryCtx` 在 ctx 自带期限时用调用方期限、无期限时兜底 30s（既有短调用语义不变，probe 等自带期限路径不受影响）。
2. Mac `go-bridge/handlers_session_commands.go`：execute 预算 90s→300s（覆盖大会话压缩走网关的分钟级耗时；官方 execute 无人为上限）。
3. iOS `CCCodeBridgeTransport.swift`：`requestTimeoutNanoseconds(for:)` 给 `execute_session_command` 挂 300s（与 Mac 同预算，其余 RPC 30s 不变）。

**验证**（组件级，2026-09-06 02:32-02:33）：新增 `TestCallCallerDeadlineBeatsDefaultUnaryTimeout`——默认 client `Timeout==0` tripwire；默认兜底缩至 120ms 时调用方 3s 期限必须等到 350ms 延迟响应（官方压缩即此形）；无期限 ctx 仍被兜底掐断。负向验证：临时恢复 30s 硬顶，测试即红（tripwire 命中），复原后绿。`agent/dsh-web` 全包 13.8s ok、`go-bridge` 命令 handler 定向 ok。

**部署**：Mac Release 覆盖安装（02:34，PID 73947 lstart 02:34:02 晚于 runtime 构建 02:33:28，8777 监听者为 /Applications 内嵌 runtime，日志 02:34:05 起滚动）；iPhone 16 Pro `scripts/run.sh device` 同步重装（transport 300s 生效）。

**回归要点**：在长会话执行 `/compact`——命令卡运行至 success、无 30s 报错弹窗、压缩生效（后续对话正常）；`/goal` `/plan` 等既有命令秒回行为不回归。

### 2.13 十次返工：命令面板点选 compact 不收起、执行中不置灰（2026-09-06 02:3x owner 报障 → 02:42 修复上线，纯 iOS）

**报障**：owner 在命令面板点 `/compact` 后面板不收起——「很容易让用户误以为点击 compact 无效从而重复点击」；且第一次 compact 执行完毕前，面板里的 compact 应置灰 disabled。

**取证（官方源码语义 + iOS 现状）**：

- 官方（ui-commands `service.ts dispatch()`）：菜单点选 bare 命令 → `consumeVia`（**菜单即关**）+ `runDetached` **分离执行不等结算**（失败走 composer 内联 notice，不绑菜单生命周期）；官方菜单**没有**运行中禁用态，并发 compact 靠服务器 busy 错误拒绝。
- iOS 现状：`executeHostCommandLine` 的 `.execute` 分支要等整个 RPC 完成（compact = 压缩全程，300s 预算下最长 5 分钟）才回调收面板——正是 owner 看到的「点了没收起」；且 `isSlashCommandExecuting` 布尔锁把 claim（goal/plan 补参数）、Plan chip 退出等异名操作一并锁死，执行中连面板都不让重开。

**修复（纯 iOS，宿主 ChatUIKitContainerView + SlashCommandPanelSheet）**：

1. **点选即收**：`.execute` 分支先 `dismiss` 再发 `execute_session_command`（官方 consumeVia 镜像）——进度/结果全走时间线命令卡（运行卡 → success/error）；RPC 失败仍弹官方原文 alert（面板已收，无二次 dismiss）。
2. **按命令名置灰**：`isSlashCommandExecuting: Bool` → `slashCommandExecutingNames: Set<String>`（同名单飞防连点，异名不互斥）；sheet 行绑定 `executingCommandNames`，正在执行的命令行 `.disabled` + 名称灰化 + 「执行中…」尾标（本地化键 `chat.slashCommand.executing`），其余命令照常可选；执行中可随时重开面板看到置灰行。
3. claim 分支（goal/plan）不再被全局锁阻塞；Plan chip `/plan off` 改按 `plan` 名单飞。

**验证**：真机构建装机通过（首构建暴露一个 SwiftUI 三元 ShapeStyle 类型错，改 `.opacity` 修饰符后过）；面板路由/判决枚举逻辑未动，SlashCommandPanelTests 既有断言面不变。owner 真机复测为回归门。

**部署**：iPhone 16 Pro `scripts/run.sh device` 重装并启动（02:42，设备 02:42:21 重连 hello_ack）；Mac 无改动不重发。

**回归要点**：点 `/compact` → 面板立即收起、时间线出现运行中命令卡并跑到 success/error；执行期间重开「/」面板，compact 行置灰带「执行中…」，goal/plan 行仍可选可 claim；compact 完成后置灰解除。

## 3. 交付物状态对照（截至本文档落档）

- Phase 1–4 agent 侧全部完成：codec 七事件收编、SessionCommandCatalog、bridge 双 RPC、协议 schemaRevision 2026-09-05、iOS 面板（按钮/列表/execute/permission 分流）、双仓 CHANGELOG、Mac Release 覆盖安装+活体核验。十次返工均已上线：§2.2 execute 载荷 images（18:03，PID 76729）、§2.4 成功反馈 settle 透传（18:39，PID 57181，已被 §2.5 取代）、§2.5 官方化时间线命令行 + Plan chip + 三命令面板（20:04，PID 35873，iOS 同步重装）、§2.6 goal 横条 + subagent 行显示（21:29，PID 630：Release built 2026-09-05T13:29:05Z、lstart 21:29:55，Management 活体核验 dsh-web 含 `session_commands`+`session_goal`；iPhone 16 Pro 21:30 重装启动，产物含 `chat.goal.phaseActive`/`mutate_session_goal` 特征串，21:30:39 设备重连 hello_ack）、§2.8 官方认领输入框（23:13，纯 iOS 重装，Mac 无改动；iPhone 16 Pro 23:13 重装启动，23:13:22 设备重连 hello_ack）、§2.9 codec 零 reset + goal 输入行气泡（00:23，PID 32663：Release built 2026-09-05T16:11:00Z、lstart 00:23:23 晚于构建，8777 监听者为 /Applications 内嵌 runtime，二进制含 `inputLine`/`commandLine` 特征串，Management 活体核验通过；iPhone 16 Pro 00:4x `scripts/run.sh device` 重装启动）、§2.10 流式分组不裁指令卡（01:46，纯 iOS 重装，Mac 无改动；iPhone 16 Pro `scripts/run.sh device` 重装，含流式尾段分组修复 + 两条回归测试）、§2.11 冷拉 goal 轮交错（02:10，纯 Mac 重发；PID 28573 lstart 02:10:08，8777=/Applications 内嵌 runtime；dsh-web+go-bridge 两包全绿含三条新回归；**owner 02:2x 复测通过**「goal命令任务已经测试符合预期了✅」，`-regression` 关账）、§2.12 /compact 30s 硬顶（02:34，Mac+iOS 双端重发；PID 73947 lstart 02:34:02；dsh-web 全包绿含 tripwire 新回归；**owner 03:0x 复测通过**，`-regression` 关账）、§2.13 面板点选即收 + 执行中置灰（02:42，纯 iOS 重装，Mac 无改动；**owner 03:0x 同轮复测通过**，`-regression` 关账）。
- **全部关账（2026-09-06 03:1x）**：owner 十轮真机返工逐项复测通过；③④⑤⑥⑦ regression 与 phase3/phase4 blocked 项凭累积验收升级 done，§8 七步矩阵覆盖（#7 export 已被 2026-09-05 白名单裁决取代）。完成报告：`docs/2026-09-06-dsh-slash-command-panel-implementation完成情况.md`（proved-complete）。
- 队列：`.exec-plan/state/plan-4112d14c1d2b.json`（owner 执行期间 agent 按上表取证后更新对应 proof；返工④ `rework-goal-banner-subagent-*`、返工⑤ `rework-official-claim-composer-*`、返工⑥ `rework-goal-round-*`、返工⑦ `rework-stream-grouping-command-card-*` triplet 已入列，`-regression` 对应 §1 各步；返工⑥ iOS 单测 7/7 绿（00:32）+ 返工⑦ iOS 单测 9/9 绿（01:45，含流式分组两条新回归）；返工⑧ `rework-cold-interleave-goalround-*` 已关账（owner 复测通过）；返工⑨ `rework-compact-timeout-*`、返工⑩ `rework-panel-dismiss-running-*` 均已关账（owner 2026-09-06 03:0x 复测通过「compact和plan命令测试基本符合预期✅」）。
