# Grok Build 斜杠命令面板与计划模式接入方案

> **v2.0 · as-built（2026-09-09）。** 本文按**最终代码状态**整体重写，是命令面板 / Plan 模式 / 计划审批的当前契约真值源；后续改这些链路前先读本文，再对照源码。v1.0–v1.5 的原始设计（含已废弃的短命 mode-only child 方案与 §5 旧事务机器）只在 [§8 已废弃设计索引](#8-已废弃设计索引防误读)留结论，全文见 git 历史（`7ef00c3` 起）。
>
> 完成情况报告：`docs/2026-09-07-grok-build-slash-command-panel-implementation完成情况.md`。执行队列：`.exec-plan/state/plan-69bf69d256c2.json`（34/34 done）。双仓提交：Mac `plan/approval-layer` `c38b16e`；iOS `plan/approval-layer-ios` `6a419322`。owner 真机最终验收 2026-09-09 通过。

## 0. as-built 来源清单

| 仓库/产物 | 路径 | 分支 | 提交 |
| --- | --- | --- | --- |
| Mac（本文实现） | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `c38b16e`（基线 `6b4a93b`） |
| iOS（chip/审批卡消费端） | `/Users/jacklee/Projects/cordcode-ios-plan-approval` | `plan/approval-layer-ios` | `6a419322`（基线 `20d8208f`） |
| 官方上游（只读取证） | `/Users/jacklee/Projects/grok-build` | `main` | `75810042` |
| 目标二进制 | `~/.grok/bin/grok` **1.0.24 (68e414c661e3)**，`--no-leader` stdio actor 与 catalog 单例实测同版本 | | |

协议行为与源码锚点冲突时，以目标二进制真实样本为准并登记 `scripts/grokbuild-phase0/EVIDENCE.md` 漂移表。

## 1. 现行产品行为（最终裁决）

| 项 | 最终行为 |
| --- | --- |
| 面板准入 | **compact + goal**（agent `_x.ai/commands/list` 真实广播）**+ /plan**（pager-local descriptor，由 CordCode 在产品目录层合并——不是 agent 命令）。hooks-\* 家族 owner 裁决移出（不是 excluded，单纯不用）；context/feedback/dream/flush/always-approve 排除（副作用未隔离/外发/pager-local）。空表诚实可见、失败 fail-closed 拒绝执行 |
| 模式键 | 只有 **plan / default** 两种会话模式；六键 legacy（acceptEdits/auto/dontAsk/bypassPermissions）已删除，永不恢复，旧 RPC 显式拒绝 |
| Plan chip | **仅 confirmed(plan) 显示**；`canSet=true` 时可点 × 退出（执行 `/plan off` → ACP `session/set_mode(default)`）；default/pending/unknown 全部隐藏。pending 不套 dsh 的 active 反转；dsh 自己的 `/plan` chip 不受影响 |
| 输入规则（移动端） | 非空 hint 认领输入框 `/name `，空参回车可执行；空 hint 点选即执行。已认领命令失败**绝不**转普通消息；仅主动手写且未认领的未知命令走普通发送 |
| 计划审批 | 官方写完 `plan.md` 后发 `_x.ai/exit_plan_mode` 等批复；iPhone 弹 `plan_review` 卡（approve / requestChanges / quit），两条轨（leader 广播 + driver 直连）同形 |

## 2. Mac 现行实现契约

### 2.1 命令目录 List

- 通道：进程级 catalog 单例（`grok agent --no-leader stdio`，基建 `catalog_session_list.go`，生产 `catalog_alive_procs=1` 常驻）上一次 `_x.ai/commands/list {cwd}` ext RPC——官方 grok-desktop 同款（session_admin.rs cwd 分支，不需把会话 load 进本进程）。`agent/grokbuild/catalog_commands_list.go`。
- 预算：`catalogCommandsListTimeout=12s` < bridge List 15s（`go-bridge/handlers_session_commands.go`）。隔离探针 warm **43ms**（冷启动只在单例首建付一次）。
- 不变量：**每次 List 都是一次真实官方拉取，无缓存无 TTL**；失败把该 `(sessionID, cwd)` 身份标记不可用（`acu.markListFailed`），Execute 拒绝旧表；cwd 来自 `resolveSessionCwd`（summary.json 权威，回退 agent workdir）。
- 目录合成：真实 agent 拉取成功后 append `/plan` descriptor（`Enter plan mode`，hint `[description]`）再过 `applyGrokAdmission`（`acu_state.go`：admitted={compact,goal,plan}，excluded={context,feedback,dream,flush,always-approve}）。拉取失败整体失败，**绝不**拿本地 descriptor 当 fallback。

### 2.2 命令执行 Execute

- 入口 `session_commands.go` `ExecuteSessionCommand`：三重门——`grokExcludedCommands` 拒绝 → `grokAdmittedCommands` 准入 → `acu.executeWhitelist(sessionID, cwd)`（该会话**当次真实 List 成功**的目录，无表拒绝「先打开命令列表」）→ 必须有活 actor（`liveSessionForCommand`，无 actor 报错「send a message in the session first」，不另起 child）。
- 执行语义：slash 行是 **prompt 语义**（`session/prompt`），必须经共用 turn dispatcher（`turn_dispatch.go`：settle-once + epoch、operation lease 单在飞、stopReason/_meta 保留、EOF 不伪成功）在该会话自己的 actor 上跑；官方反馈正文经 hostTurn `agent_message_chunk` 与聊天同轨流出；`execute_session_command` RPC 300s 预算，成功透传官方 settle（`resultKind`/`resultText`）。
- `/plan` 特判（`executePlanCommand`）：bare `/plan` → `session/set_mode(plan)` + `modeSide.observeLive` + emit `session_mode confirmed(plan)`，返回「Plan mode on.」；`/plan <desc>` → 先 set_mode，再只把描述当普通 prompt 发（镜像官方 pager `dispatch/modes.rs` 的 SetModeThenPrompt 顺序）；`/plan off` → `session/set_mode(default)`。**绝不把 `/plan` 文本喂给 session/prompt**。
- compact 特判：等待 host-side LLM 摘要，命令生命周期以 `session_command` 事件（running/settled）向 iOS 收口。

### 2.3 计划模式与 sessionMode

- 类型化状态 `sessionMode` 投影视图：`{status: confirmed|pending|unknown, mode?: plan|default, canSet, reason?}`（`core` + reducer + iOS wire model 全链；`docs/protocol/bridge-v1.md`）。
- **live 真值只属于 resident actor**：`session_mode.go` `modeSideState.live`——driver 轨 CMU（`session.go` handleNotification `observeLive`）与 set_mode ack 写入；actor 注销/替换即 `clearLive`，不把旧内存态泄漏给新 actor。live 存在时 confirmed 且 `canSet=true`。
- **冷读**：无 live 时按 `plan_mode.json` + 官方恢复映射（P8）：`Active→plan`，`Pending/ExitPending/Inactive→default`，缺失/损坏→`unknown`；冷读恒 `canSet=false`（reason `grok_mode_switch_blocked_official_recovery`）。CMU 历史**永不**充当确认源（P7 实证：零 turn 场景 CMU 根本不落盘）。
- 模式**写路径只有一个**：`execute_session_command` 的 `/plan [off|desc]`（§2.2）。core 故意**没有** `SetSessionMode` 接口、不走 `list/set_permission_mode` RPC（六键已废）。
- 读是拉式（projection 读取时 `GetSessionMode`，dirty 才重读文件），没有后台 watcher/周期重验线程——live 值由事件驱动，冷值由拉取兜底。

### 2.4 计划审批卡（`_x.ai/exit_plan_mode` 双轨）

- 官方流：写完 `plan.md` → 向当前 ACP 客户端发 `_x.ai/exit_plan_mode`（camelCase `{sessionId, toolCallId, planContent?}`）→ 等批复 `{outcome: approved|cancelled|abandoned, feedback?}`（`exit_plan_mode/types.rs`）。批复前 `plan_mode.json.awaiting_plan_approval=true`，agent 停住等。
- **leader 轨**（Mac TUI/外部 turn）：`leader_subscriber.go` `handlePlanBroadcast`——广播注册进 interactions（wire id 索引），emit `plan_review` 事件；TUI 先答则 `interaction_resolved` 广播收口。
- **driver 轨**（iOS 自己的 turn，`--no-leader` actor 是我们是唯一 ACP client）：`session.go` `handleExitPlanModeRequest`——`handleRequest` 分派（**不可删除该分支**：删了 grok 就停在 `awaiting_plan_approval`，iPhone 只有「正在执行工具」，2026-09-09 事故）；`planContent` 为空时回读该会话 `plan.md` 兜底；`pendingPlans` 登记原始 wire id。
- 卡面：`EventPermissionRequest{PermissionKind:"plan_review", PermissionActions:[approve,requestChanges,quit], PlanReview:{Content,Title}}`，Title = 计划首个非空行（`planApprovalTitle`）。iOS resolve 走既有 `resolve_permission`，`planAction` 三值；应答映射共用 `exitPlanModeResponse`（leader_subscriber.go）：approve→approved、requestChanges→cancelled+feedback、quit→abandoned；旧两键 allow/deny→approved/cancelled。`grokSession.RespondPermission` 同时支持 `pendingPerms`（普通权限）与 `pendingPlans`（计划）。
- 收口：bridge 对非 `OfficialResolutionSource` 承载方在 resolve 成功后乐观发布 `permission_resolved`（`handlers.go`）；真实工具完成由 tool_call_update 独立更新。

### 2.5 ACP 握手鉴权（2026-09-09 事故修复）

- `acp_auth.go` `selectEagerAuthMethod` 镜像官方 pager（`xai-grok-pager/src/acp/mod.rs:717-731` @75810042）：`initialize` 响应 `_meta.defaultAuthMethodId`（在 authMethods 内）→ `cached_token` → `authMethods[0]`。`acp_types.go` `initializeMeta.DefaultAuthMethodID` 解码。
- **禁止改回 `authMethods[0]` 无条件认证**：只要存在 BYOK 模型（config.toml 自定义 model 带自己的 api_key），未 pin 的 grok 就把 `xai.api_key` 排第一；选它 = 空 bearer（`auth_kind=none`）→ cli-chat-proxy 401 → iOS `session/prompt error -32603`，且官方 recovery 因「api-key auth」拒绝刷新。握手日志可观测：`grokbuild: authenticating method=… advertised_first=…`。

### 2.6 事件转发生命周期与投影收口（2026-09-09 事故修复）

- `go-bridge/handlers_relay.go`：**grokbuild ∈ `relaySurvivesTurnBoundary`（跨回合存活）且 ∈ `disablesRelayIdleTimeout`（无空闲超时）**。原因：grok actor 跨回合存活，turn 后仍有 usage/goal 残余事件 + 下一次审批卡在回合之间到达；relay 按回合退出会让 64 槽 events 通道被填满、readLoop 阻塞在 emit，下一条 send 的 `session/set_model` 响应永远读不到（15s 超时，2026-09-09 真机）。退出路径 = session Close 关闭 Events 通道。
- `go-bridge/projection_reducer.go`：`permissionCards` 集合记录控制面审批卡部件（ItemID=requestId，与真实工具部件 ItemID=toolCallId 不同源）；`settleResolvedPermissionCards` 在 `turn_completed` 把「已批准、无确认标记、仍 running/pending」的卡落 `completed`（未处理卡保持 pending、拒绝卡保持 rejected）。**不要删这个收口**，否则 iPhone 永远显示「权限已批准，等待执行」。
- `session/prompt` 硬错误带官方 message/data（`turn_dispatch.go` + `formatJSONRPCError`），不再只报裸错误码。

## 3. iOS 现行实现

- 面板：compact/expanded 菜单**每次实际打开**触发 `list_session_commands`（不做会话级预取缓存）；loading/error+retry/empty 三态；Grok 不回退 dsh 三条；认领/失败保留输入并弹错，绝不进 normalSend（`SlashCommandRouting` per-backend 白名单）。
- Plan chip：`SessionProjection.swift` 仅 `confirmed(plan)` 产出 chip target；`ChatInputAccessoryView` 渲染「Plan ×」，点击经 `ChatUIKitContainerView` 执行 `execute_session_command` line=`"/plan off"`，失败显示官方内联红字；执行中/只读态禁用。
- 计划审批卡：`PlanReviewSheets.swift` → `resolve_permission` 带 `planAction`（approve/requestChanges/quit + feedback），批准后本地乐观步骤「权限已批准，等待执行」由服务端 `turn_completed` 收口（依赖 §2.6 reducer settle）。
- DeepSeek 的 `/plan` chip、pending 反转、× 退出路径完全不变。

## 4. 协议面（canonical pack）

`docs/protocol/bridge-v1.md`、`unified-bridge-protocol.md`、`schema/bridge-v1.types.ts` 均已含：`session_commands` capability（grokbuild）、`list/execute_session_command`（execute 成功透传 `commandId/resultKind/resultText`，300s 预算）、`sessionMode` 投影视图（additive 字段）、`permission_request` 的 `permissionKind:"plan_review"` + `plan` payload + 三动作、`permission_resolved` 收口语义。iOS mirror 字节同步。

## 5. 证据与准入终表

| 命令 | 来源 | 准入证据 |
| --- | --- | --- |
| compact / goal | agent `_x.ai/commands/list` | P6 真实 turn 样本 + owner 真机执行验收（`scripts/grokbuild-phase0/`，ADMISSION.md §五） |
| /plan | pager-local registry（上游 `slash/commands/plan.rs`） | 源码锚点（EVIDENCE P10）+ set_mode/描述 prompt 分发镜像 + owner 真机验收（审批卡/退出/后续 prompt） |
| hooks-\* | agent 目录 | **移出**（owner 裁决 2026-09-07：不用、看不懂）；手写 `/hooks-*` 按未准入 fail-closed 保留输入提示 |
| context / feedback / dream / flush / always-approve | — | 排除：pager-local 无 shell 细节 / 外发 / 跨会话记忆 / 不可逆权限副作用，未隔离不准入 |

Phase 0 证据包：`scripts/grokbuild-phase0/`（P1–P9 + P10、漂移表、ADMISSION.md、隔离 home 复位记录）。P7 结论：**短命 mode-only child 冷恢复不成立**（Pending 恢复丢回 Inactive）；P10 结论：该否定只封短命 child，CordCode 持有的 resident actor 走官方 set_mode 路径（本文 §2.3）。

## 6. 验收记录

- 2026-09-07 owner 真机走查：面板/执行三条失败（齿轮开模型列表、compact 需二次发送、goal 二次发送+草稿未清）→ review-fix triplet 修复（`owner-failures-review-fix-*`）。
- 2026-09-07 owner 裁决：准入收敛 compact+goal（§9.1）；当夜 List 通道重做（§9.2）。
- 2026-09-09 校正与收口：`/plan` 补回面板 + chip 仅 confirmed(plan) + 计划审批卡双轨（driver 轨缺失是当日「卡住」事故根因，已修）+ 握手鉴权 + relay 生命周期 + 审批卡收口。owner 最终验收：「测试结果符合预期」（发消息、/plan 审批、批准后收口、普通发送全链）。

## 7. 回滚口径

- 关 `session_commands` readiness 门与模式 canSet 入口即可整体下线；additive 字段保留，旧客户端忽略。**绝不恢复六键 legacy 空转，也不退回 dsh `/plan` 内置菜单。**
- 若需回退 2026-09-09 的两个事故修复：`relaySurvivesTurnBoundary`/`disablesRelayIdleTimeout` 去掉 `"grokbuild"`、reducer 去掉 `settleResolvedPermissionCards`——但会复现「下一条消息 set_model 超时」与「权限已批准，等待执行」残留，不要在无新证据时回退。
- 鉴权回退（改回 `authMethods[0]`）在 BYOK 环境必然复现 `-32603`，禁止。

## 8. 已废弃设计索引（防误读）

| 已废弃设计 | 废弃原因 | 取代者 |
| --- | --- | --- |
| 专用 child `session/load` 读 ACU 的 List 通道（8-10s） | 全部耗时在点 ➕ 关键路径 | §2.1 catalog 单例 `_x.ai/commands/list`（P9，2026-09-07 夜） |
| 短命 mode-only child + 冷恢复切换（v1.5 §5 全套：pendingSwitch、operation lease、25s 事务预算、5s 周期重验、目录级监听、generation 读循环） | P7 实证 Pending 恢复丢回 Inactive（官方语义）；owner fork 禁令禁止 patch 官方 runtime | §2.3 resident actor live 真值（CMU/set_mode ack）+ 拉式冷读；P4 门随之取消（EVIDENCE P10） |
| permission 六键 + `list/set_permission_mode` 的 Grok sessionId 链 | D3 裁决：Grok 只有 plan/default 两键 | §2.3 sessionMode + `/plan`（off）写路径 |
| hooks-\* 准入（P6 首批五条） | owner 裁决不用 | §5 准入终表 |
| v1.5 D2「default chip 灰显可见 / pending 中性可见」 | 2026-09-09 校正：反向暗示 Plan | §1：仅 confirmed(plan) 显示 |
| 「模式切换入口永久禁用（canSet=false 只读 chip）」 | 2026-09-09 校正：resident actor 活跃期官方路径可用 | §1：canSet=true 时 × 可退出 |

## 9. 历史记录（保留）

### 9.1 owner 裁决（2026-09-07 22:4x，b57b3e5）

「暂时只做 compact，plan，goal 三个命令……hooks 那些命令我压根搞不懂是什么，也从来没用过。」落地：admitted 收敛、hooks-\* 移出面板与执行准入、手写 `/hooks-*` fail-closed；`/plan` 当时判定「不是 grok 斜杠命令」（仅保留名）——该结论被 2026-09-09 校正取代（plan 以 pager descriptor 合并进面板，模式切换走官方路径），其余裁决不变。细节见 `scripts/grokbuild-phase0/ADMISSION.md` §五。

### 9.2 List 通道重做（2026-09-07 夜）

owner 报障「点 ➕ 十几秒」。根因：旧通道每次打开起专用短命 child——initialize 1.2s + session/load 3.1s + ACU 波等待 + 1.5s 静默窗全在关键路径。官方证据（grok-build @72a6125，EVIDENCE P9）：grok-desktop 用 `_x.ai/commands/list {cwd}`（cwd 分支不需 load 会话）。重做为 catalog 单例单次 RPC（§2.1），warm 43ms；每 List 仍是真实官方拉取的红线不变；证据 `scripts/grokbuild-phase0/p9_cmdlist_sessionless.py` + `samples/p9-cmdlist-sessionless.json`。
