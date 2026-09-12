# Codex Desktop（codex-remote）斜杠命令面板接入方案

> **状态更新 2026-09-12：一期完成（proved-complete）。** Compact/Plan/Goal 三项原生动作 + P5.7 智能体可见性与 /goal 对齐全部通过 owner 真机验收（「goal，plan，compact 命令都正常执行✅」；「workflow 卡片展示正常」）。50/50 队列项 done（24 re-verified / 26 self-attested），完成报告见 [2026-09-09-2026-09-07-codex-remote-slash-command-panel-implementation完成情况.md](2026-09-09-2026-09-07-codex-remote-slash-command-panel-implementation完成情况.md)。已知交付后遗留（切回 goal 卡丢失、composer 面板失效埋点待复现、collab 冷路径失明）与矩阵第⑥行并发 gap 均在报告 §1/§5 如实记录；Review/Skills 仍未实施（§9 边界不变，不声称全量 slash 支持）。
>
> **v1.2 · 2026-09-09 · 审计后有条件可执行，未实施。** 本案将 Codex 的客户端动作接入 iPhone `＋` 菜单，复用现有官方 RPC 适配、投影与 UI，不创造通用 slash 执行协议。**一期必须完整交付 Compact、Plan、Goal 三项原生动作**；任何一项缺失都不能宣告一期完成。本文可直接交给下一开发 agent **从 P0 取证开始执行**，但不能跳过 C0–C4 直接实现或广告能力；目标 Remote Control 链路的三项完整实证尚未采集。
>
> 这是 Codex 客户端动作子集方案，不是“官方命令目录全量透传”。没有找到官方通用 slash list/execute 方法；`command/exec` 是 OS 命令执行，不能拿来执行 `/compact`。文件名沿用同组方案命名，产品入口仍是 `＋ → 命令`。

## 0. 来源清单、版本与工作边界

### 0.1 双仓配对与只读来源

| 仓库绝对路径 | 分支 | 完整提交 | 读取/首次编辑前状态 | 用途 |
| --- | --- | --- | --- | --- |
| `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `c38b16e9b0d46adf2b1447c111d984749f78e390` | 未跟踪本文及 `handoffs/handoff-20260907-2127.md`、`handoffs/handoff-20260907-2319.md`、`handoffs/handoff-20260909-1331.md` | 用户当前工作树；本轮只修改本文并新增同目录审计报告 |
| `/Users/jacklee/Projects/cordcode-ios-plan-approval` | `plan/approval-layer-ios` | `6a419322f165a99a0e722a93b5de0ded03831f47` | 未跟踪 `message-web/public/` | 按 dsh 方案 §0/§12 和计划审批方案 §0 明确指定的配套功能工作树；不是 iOS main |
| `/Users/jacklee/Projects/codex` | `main` | `9e868bd9dc007c05e84a98e0b1f4e31dc98c5e6a` | 干净 | 官方上游只读 checkout；涉及版本差异时改用下述 tag 对象读取，未切分支 |

上述未跟踪 handoff 与 iOS `message-web/public/` 都是既存用户文件，本案不修改、覆盖或纳入。iOS 源码锚点以 `6a419322` 为准；实施前仍须重新登记双仓 HEAD 与 dirty set，不能复制旧文件覆盖新工作。

预期产品身份：`codex-remote` / **Codex Desktop**，独立 Remote Control controller → 官方 relay → Desktop 私有 app-server；iPhone 只连接 CordCode bridge/Relay。不得改接 legacy `codex`/`codex-web`、额外启动 app-server、读取本机 rollout 文件冒充远程历史，或修改 Desktop 安装包。配对依据来自当前参考方案，不由路径名称猜测。

### 0.2 本机版本实测与证据等级

| 项 | 本轮只读结果 |
| --- | --- |
| Desktop bundle | `/Applications/ChatGPT.app`；Info.plist：`26.901.51231`，build `8109` |
| 内嵌 binary | `/Applications/ChatGPT.app/Contents/Resources/codex --version` → `codex-cli 0.153.4` |
| binary SHA-256 | `a30ec314bbd0e3721632234d07db7c99855db3b9f1e32dbe8c791947f07e7629` |
| 对应官方源码 | `rust-v0.153.4` → `3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`，使用 `git show` 读取，不修改 checkout |
| live 运行目标 | 尚未证明当前 controller 实际连接的 Desktop 与上述磁盘 binary 是同一代际；必须由 C0 收口 |

版本字符串与 tag 对应不等于证明私有 Desktop 构建与开源产物字节等价。本文区分：**目标 tag 源码事实 / 本仓实现事实 / 已归档样本的真实出处 / 本次新增设计 / 待 Remote 活体证据**。不能把 checkout HEAD、旧调研的 `0.153.0-alpha.5` 或旧 Phase 0 的 `0.150.0-alpha.12.2` 混作当前目标。

本轮没有运行真实 turn、压缩、Plan/Goal 写入、审批、权限变更、Remote enrollment 或 UI 自动化；只执行版本查询、源码/文件读取、既有脱敏样本提取及文档编辑/检查。没有创建新的 exec-plan 状态 JSON，没有修改产品代码、安装、提交或 push。

### 0.3 参考材料及适用范围

- [dsh 面板方案](2026-09-04-dsh-slash-command-panel-implementation.md)：借鉴 §11 四拍、冷热身份、失败原文与超时教训，复用 §12 的 `＋` 菜单组件；不复制 dsh host command 协议。
- [Grok 面板方案 v1.5](2026-09-07-grok-build-slash-command-panel-implementation.md)：借鉴证据门、per-backend 策略、会话代际与失败不转普通发送；不复制 prompt 执行和 mode-only actor。
- [计划审批方案](2026-09-04-plan-approval-implementation.md)：复用统一 `permission_request` / `resolve_permission` / plan_review 卡的思路与现有实现，不再新建审批事件族。该文 §8 的 Codex 未支持结论属于历史时点，现代码已实现 `RespondSessionPermission`，不能照抄为现状。
- [Codex iOS Plan 入口另案](2026-09-04-codex-ios-plan-mode-entry.md)：提供既有 Plan 研究与约束。**2026-09-07 用户明确要求本案一期至少实现 Compact、Plan、Goal，已取代该文“主动入口先不做”的旧范围裁决。**可复用其证据，不能继续用旧裁决排除 Plan。
- [跨 backend 调研 §6](2026-09-04-slash-command-skill-cross-backend-survey.md)：三集合区分仍适用；其中“不可行”指官方目录/执行透传，本案选择其 §6.5 C 的客户端动作映射，未声称发现了新官方 slash 接口。
- 根目录 [GO_BRIDGE_ARCHITECTURE.md](../GO_BRIDGE_ARCHITECTURE.md)、双仓 CLAUDE.md 和 think.md；实施前再读实际配对 iOS 的 `IOS_MAC_INTERACTION_FLOW.md`。
- [官方 App Server 文档](https://learn.chatgpt.com/docs/app-server)：确认压缩请求立即应答、过程经 turn/item 通知。官方 [Commands 页面](https://learn.chatgpt.com/docs/reference/commands) 是产品操作参考，不能当目标 Desktop 私有 JS 菜单或 Remote RPC 的样本证明。读取日期 2026-09-07。
- [本案样本审计报告](2026-09-07-codex-remote-slash-command-panel-implementation-audit.md)：登记本轮实际 dump、目标代际缺口与修订优先级；它是判断能否跳过 P0 的证据索引。

### 0.4 交接判定与下一 agent 启动合同

**结论：可直接交接为一份从 P0 开始的实施队列；不可当作已完成协议取证、可直接从 P2/P3/P4 编码的规格。** 下一 agent 无需重新发明架构或 slash 字符串 fallback，但第一动作必须重新登记来源清单并执行 P0。C2、C3、C4 各自的目标 Remote 请求、response、notification、冷读样本未归档前，对应能力不得进入 readiness/广告；源码实现和静态 fixture 可以并行准备，完成声明不可以提前。

本轮审计发现并已修正一项 P0 级歧义：Plan/Default payload 的 `collaborationMode.settings` 必须保留当前 thread 的 `model`，按 preset mask 处理 `reasoning_effort`，并显式发送 snake_case `developer_instructions: null` 以选择服务端内置模式指令。不能把 `collaborationMode/list` 省略该字段误解为 update 也应省略。其余未证实字段形状集中列在审计报告，不得凭 tag schema 升级为 Remote 活体事实。

## 1. 架构选择：复用已有动作通道

| 已有轮子 | 本案复用方式 | 必须补的缺口 |
| --- | --- | --- |
| `core.ContextCompactingSession.CompactContext(ctx)` | 实现到 `remoteSession`；合同已规定 nil 只代表 accepted | Remote 官方 `thread/compact/start` 适配与真实样本 |
| `compress_context {sessionId}` RPC | iOS 菜单 compact 直接调用；保留 `{accepted:true}` | handler 当前只查现存 registry、无 backend 所有权验证且传 Background ctx，需完善 |
| `ProjectionLiveSessionAttacher`、`startProjectionLiveRelay`、现存 registry/relay | 只读打开的远程任务也先 attach/resume 并建立广播投影，再发压缩 | 从现有 helper 提取可返回 error、受 ctx 约束的共用入口；禁止复制一套“压缩专用 session 管理” |
| `LiveCodec` 的 contextCompaction、`EventContextCompressing/Compressed` | 保留官方 thread/turn/item 身份 | wire 当前丢 turn/item；SSV2 屏蔽 raw 但 reducer 未消费，必须补完整投影 |
| `agent/codex-remote/plan_review.go` 的 `thread/settings/update` 与 collaborationMode payload | 抽出目标 tag 对齐的 settings adapter，供 Plan 入口和 plan_review 共用 | 现 helper 用 agent 级 selected/default model，不是 per-thread 权威设置；需增加查询、通知投影与竞争收口 |
| 既有 `SessionGoalController`、`GoalView`、goal banner 与 `session_goal` capability | 复用 UI、projection patch 和 mutation handler 形状，扩展为 Codex 官方 goal 生命周期 | 当前接口与字段是 dsh 专用且只支持 pause/resume/edit/clear；需支持 get/create、官方状态/用量/时间及来源区分 |
| iOS `AttachMenuPlanner`、`ChatInputAccessoryView` 两种布局与异步 scope | 同一个命令分组容纳 Compact、Plan、Goal 三个 typed action | Codex 明确选 native action 策略，不落默认 dsh fallback |
| 既有 `session_projection` / patch / window / 懒加载历史 | 唯一可见状态与恢复通道 | 映射无 user message 的压缩 turn，保留步骤状态；不能另起第二个 timeline writer |

**一期不建立 operation 数据库、不引入通用命令执行器，也不新增 exec-plan JSON。** Compact 复用现有 `compress_context`。Plan 与 Goal 没有可安全冒用的现成跨 bridge 写接口，允许新增最小 typed RPC，或在实施审计确认现有接口语义完全覆盖后扩展它；禁止经 `execute_session_command` 或普通 Send 绕行。`SessionCommandCatalog` / `session_commands` 继续服务已有目录后端。菜单动作枚举是 CordCode UI 路由，不是协议模拟或运行期 fallback。

三项能力分别命名 **`context_compaction`、`session_collaboration_mode`、`session_goal`**，都是 bridge additive capability，不冒充 OpenAI 上游字段。分别按 `WireDescriptor.StaticCapabilities`、目标 feature/readiness 与编译期接口断言广告；不支持其中一项时只撤下该项，但一期总体验收仍失败。handler 即使被旧客户端直接调用也重新验证。`session_commands` 对 Codex 仍为 false。

## 2. 官方动作全景与一期范围

本表是本案范围决定，不冒充官方 Desktop 菜单全集；版本/feature 可能改变 TUI 可见项。不逐条搬运终端枚举。

| 动作 | 官方归属/通道 | 本案处理 |
| --- | --- | --- |
| `/compact` | 独立 `thread/compact/start`，异步 turn/item 生命周期 | **一期**：`＋ → 压缩上下文`，走已有 `compress_context`，全过程与恢复必须一起交付 |
| `/model`、effort | 官方 model/list、设置/发送选择；本仓已有模型与 effort 接线 | **已存在入口复用**，不在命令菜单复制第二套模型选择器，不硬编码模型名 |
| `/plan` / Default | `collaborationMode/list` + `thread/settings/update` + settings notification | **一期**：`＋ → 方案模式`，展示并切换当前 thread 的官方 collaboration mode；不用 `/plan` 文本，也不把它映射成 permission mode |
| Goal | `thread/goal/set|get|clear` + updated/cleared notification | **一期**：`＋ → 追求目标`，创建/查看/更新/清除 thread goal，并将官方状态与用量投影到既有 goal UI |
| `/review` | TUI 先弹目标选择；`review/start`，不是普通 prompt | **后续独立单元**，完整设计边界见 §9；未过证据门不显示占位行 |
| `/skills` / `$skill` | skills/list + 结构化 Skill input；与 slash command 不同 | **后续独立技能面**；不扫描 Bridge 本机目录代替远程 cwd，不把 SKILL.md 文本塞进 prompt 冒充官方 Skill input |
| `/rename`、archive、new、fork、resume | 线程管理与导航 | 使用现有任务管理入口；不在本案复制，尤其不把删除/清空藏在 compact 后面 |
| `/status`、`/diff`、`/recap`、终端显示/退出类 | 客户端呈现或各自专用通道 | **本期不做**；无等价 surface 就不展示，不以 RPC 成功替代用户可见内容 |
| `/init`、自定义 prompt 类 | TUI 本地模板/用户输入编排 | **本期不做**；不能凭同名字段推断 Desktop/Remote 模板一致 |
| `command/exec`、`thread/shellCommand` | OS 进程/用户 shell 执行 | **不是 slash 通道**，禁止作为兜底 |

一期以三项完整闭环为最小范围：Compact 是异步 turn/item 动作，Plan 是 thread setting，Goal 是持久 thread 元数据。三者共享菜单、身份、投影与错误基础设施，但必须各走官方 typed API；不能伪装成 `hint.isEmpty → execute` 的同一种命令。

## 3. Phase 0 证据门：代码事实不代替 Remote 活体

新增证据归档建议放 `agent/codex-remote/testdata/slash-panel/`，沿用该 backend 的 provenance 与脱敏格式。原始请求/response/events/冷拉数据分别保存，正文只摘最小脱敏样本；不保存 JWT、device key、账号/route 标识或真实用户内容。

| 门 | 需要的证据 | 是否模型/副作用 | 放行什么 |
| --- | --- | --- | --- |
| C0 来源与链路 | 实际 controller epoch、对应 Desktop 运行代际、目标 binary/配置、官方 tag；确认仍是独立 Remote Control 链路 | 只读；另建 controller 按既有 probe 规则单独报明，勿自动撤销用户 controller | 后续所有运行结论；本机磁盘版本不单独放行 |
| C1 attach 与状态 | 只读打开远程任务后 resume、订阅、thread 元数据/当前 turn；无本地 registry 情况；归属 backend/host/cwd | 零模型可做；resume 可能改变订阅，不称纯文件读取 | 受控 attach、输入门与冷恢复字段 |
| C2 Compact 主路径 | 真实 `thread/compact/start` ack、contextCompaction started/completed、终态与错误、用量、无 user 行；事件可能先于 response | **会调用模型/消耗额度并不可逆改变会话上下文**；仅隔离可弃 thread，执行前报成本并获同意 | Compact adapter、投影与能力广告 |
| C3 Plan 主路径 | `collaborationMode/list` 的目标 preset；当前 thread 设置来源；Plan/Default 的 update response 与 `thread/settings/updated`；切换前后 model/effort/developerInstructions | 会改变后续 turn 行为；用隔离 thread，真实写入前报明 | Plan typed contract、per-thread 状态和能力广告 |
| C4 Goal 主路径 | feature 可用性；`thread/goal/get` 空/非空；set response、updated；clear response、cleared；完整 goal 字段与 response/notification 顺序 | 会持久修改 thread 元数据，可能触发 goal runtime/accounting；用隔离 thread，真实写入前报明 | Goal typed contract、投影与能力广告 |
| C5 失败、并发与恢复 | 三项的未知/空/parent-owned/权限不足、超时/断线、Desktop 同时操作、bridge 重启；Compact 另测 active turn/Replaced 与冷历史 | 静态/内部注入先行；真实变更场景逐项列成本 | 错误、双客户端权威状态、冷热恢复；不假设本地写赢 |
| C6 Mobile 链 | 三项真实状态→bridge journal→snapshot/patch→Swift model；菜单与 A→B→A scope 对齐 | 复用已取证 thread；不默认跑 UI automation | 产品验收 |

历史 `testdata/phase2/README.md` 明确其中 contextCompaction 文件是 **schema-derived replay fixture**，不是新 live Desktop capture。只能用来先验 decoder/reducer，不能把它贴到 C2 当通过证据。官方 tag `app-server/tests/suite/v2/compaction.rs` 的测试同样只证明该源码版本行为。

C0/C1 可先准备；C2–C5 中未授权的真实写入保持对应证据项 blocked，同时继续源码适配、fixture 和定向单测。设计期发现目标版本不支持某项则撤下该项能力并如实报告一期未完成；运行期失败不换 standalone server、不改成 slash 文本、不重发以试探。

## 4. 已核验源码事实与复用位置

上游 U 路径相对 `/Users/jacklee/Projects/codex/`，除注明 HEAD 外均按 **rust-v0.153.4 / 3d2ee51c**；Mac M 相对 §0 Mac 根；iOS I 相对配套仓 `OpenCodeiOS/OpenCodeiOS/`。

| 锚点 | 事实/对实施的约束 |
| --- | --- |
| U `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:1113` / `:1120` | 请求是 camelCase threadId，响应空对象；没有官方 commandId/turnId 可从 ack 取得 |
| U `codex-rs/app-server/src/request_processors/thread_processor.rs:2347` | load thread → ensure_direct_input_allowed → submit Op::Compact → 返回空对象；不是等模型压缩完成 |
| U `codex-rs/core/src/session/handlers.rs:246`；`core/src/tasks/mod.rs:271` | CompactTask 经 spawn_task，后者会 abort_all_tasks(Replaced)；**不可假设并发 compact 只会 busy** |
| U `codex-rs/core/src/tasks/compact.rs` | 由官方 provider/features 选择压缩实现；部分错误需结合 error 事件判定，不能仅看 turn 结束就宣告成功；不复制压缩算法/提示词 |
| U `codex-rs/tui/src/chatwidget/slash_dispatch.rs:263` / `:285` / `:304` | compact 是本地动作派发，review 打开选择器，Plan 切协作模式；不能统一发送字符串 |
| U `codex-rs/app-server-protocol/src/protocol/common.rs:633` / `:1110` / `:1865`；`v2/thread.rs:226` | Plan 依赖 experimental `thread/settings/update.collaborationMode`、`collaborationMode/list` 与完整 `thread/settings/updated`；initialize 已开 experimentalApi 仍须验证目标可用性 |
| U `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:771`–`:865` / `:1987` | Goal 的 status 为 active/paused/blocked/usageLimited/budgetLimited/complete；set 支持 objective/status/double-optional tokenBudget，get 可返回 null，clear 返回 cleared，通知携带完整 goal 或清除身份 |
| U `codex-rs/app-server/src/request_processors/thread_goal_processor.rs:123`–`:276`；`ext/goal/src/api.rs` | goal 写入有 feature、thread 所有权和持久化约束；set 先 response 再 ordered notification，clear 仅在 `cleared:true` 时通知，不能用本地 banner 乐观伪造 |
| U `codex-rs/app-server/tests/suite/v2/compaction.rs:247` | 专项测试验证空 ack 与 contextCompaction item 生命周期；另有 invalid/unknown thread 用例 |
| U `codex-rs/app-server-protocol/src/protocol/v2/review.rs`；`tui/src/chatwidget/review_popups.rs` | review 有 target 与返回 turn/reviewThreadId；缺输入选择就不是完整 review 功能 |
| M `core/interfaces.go:686`；`go-bridge/handlers.go:3397` | 专用压缩接口/RPC已存在，accepted 合同已对齐；handler 未做全面身份、订阅与 ctx 收口 |
| M `agent/codex-remote/session.go:308` / `:356` / `:482` | StartSession 对已有 thread 先 addListener 再 resume；ProjectionLiveSessionAttacher 复用同路径。传空 ID 会新建 thread，因此压缩必须先拒绝空 ID |
| M `go-bridge/handlers_projection.go:startProjectionLiveRelay` | 已有只读打开场景的 attach/registry/relay 路径，可抽取返回结果；勿复制成第二条监听链 |
| M `agent/codex-remote/rpc.go:58`；`agent/codex-appserver/rpc/client.go:253` | 复用 RequestContext 与共享请求路由；底层独立 request timer 存在，ctx 只能在实际代码里正确约束整个发送/等待路径 |
| M `agent/codex-remote/codec.go:256` / `:311`；`go-bridge/events.go:228` | codec 已带 thread/turn/item ID，wire 压缩事件目前只放 sessionId；必须保住身份 |
| M `go-bridge/projection_delivery.go:74`；`projection_reducer.go:798` | syncV2 屏蔽原始压缩事件；reducer 尚无两个压缩分支，且无正文的 turn_started 仅 persist 不 publish。只接 RPC 会缺过程 UI |
| M `agent/codex-remote/history.go:438`；`history_paginated.go:154` | full-history 只放 system note，paginated 白名单包含 compaction；不是已经拥有冷热同一可见 compaction part |
| M `agent/codex-remote/session.go:dispatch` | 非阻塞 listener 发送有丢事件分支；新功能不能把“未收到结束”当作“还在跑”无限等待，须纳入现有恢复/失效机制 |
| M `agent/codex-remote/plan_review.go:194` / `:227` | 已有 Plan review 切 Default 的 typed update 与 payload builder，且已显式发 `developer_instructions: null`；可抽 adapter，但其模型仍来自 agent selection，主动入口必须先取得 thread/preset 权威值 |
| M `core/interfaces.go` SessionGoalController；`go-bridge/handlers_session_goal.go` / `projection_types.go:234` | 既有 Goal 合同明确是 dsh mutate/view；Codex 若复用需 additive 扩展 get/create 与官方字段，不能改变 dsh 现有 JSON 含义 |
| I `Views/Chat/SlashCommandRouting.swift:policy(for:)` | Grok 已有独立策略，其余默认 dsh；Codex 不能只放宽 capability 后落该默认值 |
| I `App/ChatUIKitContainerView.swift:pullSlashCommandsForMenuOpen`、`:executeSlashCommand` | 复用 scope/generation 和菜单入口；新增三项明确 native action 分派，不经过 session command line adjudicator |
| I `Models/SessionProjection.swift:84`；`App/GoalBannerView.swift` | 已有 SessionGoalView 和 banner 可复用视觉层；新增 Codex 字段时保持旧 dsh decode，不能把两种 backend 的 revision/phase 强行等同 |
| I `Services/Bridge/CCCodeBridgeBackendClient.swift:1739` | raw context_compressing/compressed 已解码，但 syncV2 不靠 raw；不能直接解封 raw 事件制造双写 |

上游 HEAD 对 tag 的 review 文档/部分类型已有变化，因此后续 review 以目标 tag/新活体重新对齐；不得混用 HEAD 的 deprecated 描述当作 0.153.4 的实测行为。Desktop 私有 UI 与 TUI 不是同一实现；本案镜像有源码的动作合同，菜单布局是明确的移动端差异。

## 5. 用户交互：三项动作各自闭环

### 5.1 打开输入的地方

已打开真实 Codex Remote thread → 输入框 `＋` → 同一“命令”分组中的 **压缩上下文、方案模式、追求目标**。每行分别取决于对应 capability、已建立/可建立的 session 观察与当前状态；新建但没有真实 threadId 的草稿会话不显示三项动作。

Codex 的三行由已验证 capability 映射，不发 `list_session_commands`，不维护“官方目录缓存”。连接未就绪或状态未知时禁用并使用现有状态说明；Compact 在已知 active turn 时禁用，Plan/Goal 的写入按各自官方约束判定。两个输入栏布局均接同一路径。模型/effort 选择器继续用于普通配置；Plan 行只负责 collaboration mode。

### 5.2 输入

compact 无参数；点按后立即收菜单，保留现有草稿文本与附件，不认领 `/compact ` 输入框、不清空 draft。面板动作不携带附件，附件也不参与压缩请求。

用户手写且整行仅 `/compact`（允许首尾空白）可作为同一 native action 别名；已保留该名称但含额外参数或附件时明确提示不支持并保留草稿，**不落普通 Send**。其他未知 slash 继续普通文本输入语义，本案不声称完整 TUI parser。未来 `/review` 的输入不能通过这个无参规则偷渡。

### 5.3 发出去

`BackendClient` 的专用 `compactContext(sessionId)` → bridge **现有** `compress_context` → 正确 backend/thread 的 remoteSession → `thread/compact/start {threadId}`。不经 `send_message`、`execute_session_command`、`command/exec`；不创建 user echo、command 气泡或新的 thread。

点击至 RPC 应答仅显示“提交压缩请求…”；`accepted:true` 后显示“已提交，等待会话状态”。它不是“压缩完成”，不能弹成功提示或清空上下文用量。

### 5.4 把过程展示出来

contextCompaction 官方 item started → SSV2 的压缩步骤/运行提示；item completed → 步骤结束；**同一官方 turn 的完成状态与 error 一并判定**后才可显示完成或失败。复用现有 system/context 提示样式；若现有投影无结构载体，在同一 projection part 体系新增最小 `context_compaction` 类型，不另建命令卡系统。

无 user message 的 turn 也必须及时 publish。界面描述的是“这个会话的上下文压缩”，不宣称无 request ID 的事件就是手机这次点击所触发；自动压缩或 Desktop 触发也以同一真实 item 展示。没有官方可显示摘要就不生成摘要。

拒绝/网络错误原文经现有错误提示展示，用户留在原会话，draft 完整保留。断线/状态无法确认显示“状态待确认”，不显示成功、不无限挂“执行中”。完成重开按官方历史还原；不造 `/compact` user 行占位。用户要取消只能用既有停止当前 turn 入口并确认目标身份，不能因 ACK 超时自动 interrupt 当前别的 turn。

### 5.5 Plan：方案模式四拍

1. **打开**：点 **方案模式** 后显示当前 thread 的 collaboration mode，并列出目标 `collaborationMode/list` 返回的可用 preset。Phase 1 至少提供 Plan 与 Default 往返；标签由产品本地化，payload 使用官方 mode 值。未取得列表或当前值时显示加载失败，不用硬编码列表冒充发现结果。
2. **输入**：选择 Plan 即确认将该 thread 后续 turn 切入计划协作模式。无自由文本，不清空草稿和附件。若已经是 Plan，则显示当前状态并禁用重复写入；切回 Default 也走同一界面和 typed API。
3. **发出**：bridge 以 `(backendId, threadId, expected epoch, selected preset)` 调 `thread/settings/update`。payload 以官方 preset mask 和当前 thread 的有效设置组装：保留当前 thread `model`，按 mask 设置/保留 `reasoning_effort`，并在嵌套 `settings` 中显式发送 snake_case `developer_instructions: null` 以采用该模式的服务端内置指令；不得取全局默认模型覆盖 thread，不借用 `permission_mode`，不发送 `/plan`。空 RPC response 只表示 update 已入队；只有 `thread/settings/updated` 或一次权威读回证明 UI 已收敛。
4. **展示**：会话级 Plan 状态进入 SSV2 snapshot/patch，输入区持续显示当前模式；Desktop 同时修改时以后到的官方权威状态为准。设置失败保留旧权威值并展示原错误；响应丢失显示待确认，重连后读回，不自动重发。进入 Plan 后产生的 plan item 继续走既有 plan_review 卡；切模式本身不生成聊天消息。

冷启动不能只靠 iOS 上次缓存。attach/resume 后必须从目标提供的 thread settings 响应或通知恢复；若目标版本没有直接 read 方法，C3 必须用真实协议确认 `thread/resume`、`thread/settings/update` response 或其他官方返回中哪个字段可作为冷源，再确定实现。没有可证明冷源时 capability 保持关闭，不能用本地持久缓存猜当前模式。

### 5.6 Goal：追求目标四拍

1. **打开**：点 **追求目标**。先以 projection 中的官方 goal 展示当前目标；冷状态未知时调用 `thread/goal/get`。没有目标时打开创建表单；已有目标时打开查看/编辑界面，提供更新状态与清除操作。不能把“无缓存”显示成“无目标”。
2. **输入**：创建至少要求非空 objective。Phase 1 可不暴露 token budget 输入；省略字段表示让官方采用无显式预算，不能发送 `0` 代替未设置。编辑时只发送用户实际修改的 objective/status/tokenBudget；`tokenBudget` 的“不改、清除、设值”必须保持 double-optional 三态。替换已有目标前在同一表单明确显示现值和将覆盖的字段。
3. **发出**：创建/更新用 `thread/goal/set`，清除用 `thread/goal/clear`，查看用 `thread/goal/get`。请求固定绑定 backend/thread/epoch，不经 dsh `execute_session_command`，不生成 user message。set response 的完整 `goal` 与 updated notification 进入同一 reducer；clear 只有明确 `cleared:true` 或 cleared notification 才移除。
4. **展示**：复用既有 Goal banner 的位置与交互，扩展呈现 objective、官方 status、tokenBudget/tokensUsed、timeUsedSeconds 和更新时间。状态值按官方枚举传输并本地化；未知未来值保留可解码的 unknown 状态与原值。response 可先于 notification，reducer 按 thread、epoch 与 `updatedAt`/请求 generation 去重，旧响应不能覆盖 Desktop 的新状态。

Goal 的 `complete`、`blocked`、usage/budget limited 是服务端状态，不由客户端根据文字、token 或时间自行推断。parent-owned/ephemeral 等目标拒绝写入时原文报错；读取若官方允许仍可显示。断线后先 get 校准，未知写结果不自动重放。Codex 与 dsh 可以共享 banner 组件和 projection 槽位，但 wire 必须带 `source/backendKind` 或等价 discriminant，使 Codex 官方 status/用量与 dsh phase/revision 不互相伪装。

### 5.7 Codex 智能体可见性与 /goal 命令对齐（P5.7，2026-09-11 owner 批准追加）

owner 官方 iOS 取证（2026-09-11 截图，同一 `Respond to greeting` 线程）证明 Codex Remote 本身携带子代理结构化状态；此前"Codex 协议无智能体条目"的结论系 C0–C4 取样未触发 subagent 场景所致，已更正。目标不是复刻官方样式，而是把 dsh/Grok Build 已有的三件套带给 Codex desktop 模式：goal 命令的 `/goal` 输入交互、后台任务指示、workflow 卡。

**源码事实（上游 tag rust-v0.153.4，只读核对）**：

- `app-server-protocol/src/protocol/v2/item.rs:362` `ThreadItem::CollabAgentToolCall`：`{id, tool: spawnAgent|sendInput|closeAgent, status: inProgress|completed|failed, senderThreadId, receiverThreadIds[], prompt?, model?, reasoningEffort?, agentsStates: Map<threadId,{status: pendingInit|running|interrupted|completed|errored|shutdown, message?}>}`；serde tag=`type`，camelCase。`:385` `SubAgentActivity`：`{id, kind: started|interacted|interrupted|completed, agentThreadId, agentPath}`。
- live 路径：`item/started` / `item/completed` 通知携带上述条目（同 turn 归属，upsert 按 `id`）；冷水路径 `thread/items/list` 同型。核心事件集 `protocol.rs:4199-4260`：SpawnBegin/End、InteractionBegin/End（end 带新线程 id、`agentNickname`/`agentRole`）。
- 官方 UI 归因：时间线"已创建/已关闭 N 个智能体"折叠段 = CollabAgentToolCall 条目；"N 个智能体"chip = agentsStates 中 running 计数；"正在推进目标 X分X秒" = goal 记录 `timeUsedSeconds`（P4 已解码）。agent 昵称在 core 事件有 `agentNickname`，但 **v2 条目序列化不含昵称**——官方端名（Anscombe 等）来自子线程元数据解析；一期以序号标签 + prompt 作说明，昵称缺口如实记录。
- 本仓现状：`agent/codex-remote/codec.go` decodeItemStarted/decodeItemCompleted 的条目分派无 `collabAgentToolCall`/`subAgentActivity` case，落入 default 静默丢弃；history.go 冷拉同样未映射。

**三件套设计**：

1. **/goal 命令对齐（对齐 dsh/Grok Build claim 式输入，SlashCommandRouting.swift:119-126 模式）**：attach 菜单点 Goal 不再直接开表单，改为 claim 输入框（token `/goal ` 带尾随空格 + hint）；发送时客户端拦截：`/goal <objective>` → `thread/goal/set {objective}`（status/tokenBudget 省略走服务端默认，不伪造 0 值）；`/goal clear` → `thread/goal/clear`；`/goal pause` / `/goal resume` → set status `paused`/`active`。拦截后不发送普通消息，不落 user 气泡；无子命令且空 objective 时行内提示，不裸发。goal bar 现有查看/编辑/暂停/清除入口保留。
2. **workflow 卡（复用 dsh 槽位）**：桥 codec item/started+item/completed 解码 `collabAgentToolCall`，按 `id` upsert 到所属 turn 的 assistant workflow part（`upsert_workflow`，与 dsh 同一 K4 seal 通道）；映射：workflowName=首个 prompt 截断（或 "Subagents"），members=receiverThreadIds × agentsStates → `{seq, label:"Agent-N", childSessionId:threadId, status}`，状态映射 pendingInit→pending、running→running、completed→completed、errored→failed、interrupted/shutdown→interrupted（dsh 状态词汇，iOS 渲染器已支持）；cold history `thread/items/list` 同映射。`subAgentActivity` 一期只消费 kind 作成员状态刷新佐证，不单独成卡。wire 保留 Codex 判别（part 来源标记），dsh 卡渲染不回归。
3. **后台任务指示（复用现有 goal bar 槽位）**：Codex 模式 execution.phase==running 期间，在 goal bar 追加由最新 running workflow 卡 `agentsStates` 派生的「N 个智能体运行中」徽标；计数只来自官方已投影的 running 成员，跟随 execution/会话切换的现有隐藏规则，不在无活跃 turn 时常驻。`TaskDockView` 当前是权限请求面，不能承载这类会话级智能体状态，因此本期采用 goal bar 扩展并记录该设计偏差。

**失败语义**：/goal 拦截发送失败按现有 Codex native 错误通道显示（requiresReadback 语义不变）；未知 collab 条目字段走 additive 解码，未知 tool/status 原文保留可显示；不伪造运行中状态——agentsStates 缺席时成员显示未知态而非推断。

**验收（owner 矩阵追加第 ⑦ 行）**：goal 任务运行期间，iPhone 上可见 subagent workflow 卡（成员逐个出现、状态随官方更新）、后台任务指示随 turn 起止出现/消失、goal bar 显示进行中目标；`/goal <任务>` 输入交互与 dsh/Grok Build 一致；任务完成后卡片终态与官方一致，无假运行中。

## 6. Mac 接线与失败语义

### 6.1 能力与 session 准备

1. 在 remoteSession 实现 `ContextCompactingSession`，发送固定 typed threadId 请求，解码目标规定的 response；RPC error 透传。只实现官方调用，不复制 core 压缩逻辑。
2. handler 在做任何动作前解析并验证 sessionId、backendId、session 归属与连接可用性；不只凭一个可能跨 backend 重名的 registry key。新路径经过现有 `ScopeSessionWrite`，不新增权限旁路。
3. 无 registry session 时，复用 projection attach/live relay 的共用方法，并使 attach 错误返回调用者；订阅/admission 与唯一 relay 必须在发送压缩前就绪。当前仅 log-and-return 的 helper 不能直接当作“准备成功”。ctx 必须传进 load/resume，不使用 Background 偷延长。
4. attach 时不得携带本机 workDir/model/effort/collaborationMode 覆盖官方 thread 当前设置，不触发 turn；已有真实 session 必须复用，不能新建并把 compact 发到另一 thread。
5. capability 后端级表示动作可支持，不表示每个 thread 可执行。parent-owned、未加载、未知/无权限、活跃状态都按官方与本地 admission 判定，失败不升级权限。

### 6.2 Plan 设置 adapter 与权威状态

1. 在 codex-remote 增加专用 collaboration mode controller：list presets、读取/水合当前 mode、update mode。优先把 `plan_review.go` 的低层 `thread/settings/update` 抽为共用 adapter；plan_review 的“批准后切 Default”继续调用它。
2. adapter 接受 thread 当前权威 settings 和所选官方 preset mask，输出目标 tag 的完整 collaborationMode。目标 tag 源码确认 built-in Plan mask 为 medium effort、model 为空；实现仍以目标 Remote 的 list 样本为准，不能硬编码目录。model 使用当前 thread 值，mask 缺省字段表示保留当前值；`settings.developer_instructions` 显式为 null，让服务端选择内置指令。若 preset 不适用当前模型，呈现官方拒绝，不悄悄换全局默认模型。
3. codec 解码 `thread/settings/updated` 并投影完整有效 settings。projection 对 iOS 暴露 backend-neutral 的 `collaborationMode {mode, model, reasoningEffort, ...}` 或更小但可证明充分的 typed view；不要复用 Grok `sessionMode` 字符串而丢失官方设置语义。
4. bridge 新增最小 typed list/update RPC 与 `session_collaboration_mode` capability。写 handler 沿用 ScopeSessionWrite、attach、epoch 与 deadline；列表/当前值读取沿用 read scope。无官方冷读源时 capability readiness 不通过。

### 6.3 Goal controller 与数据模型

1. codex-remote 增加 typed `GetGoal/SetGoal/ClearGoal` controller，忠实编码 set 的三个 optional 字段，尤其保持 tokenBudget double-optional。返回完整官方结构，不把 set/clear 压成 `{ok:true}` 后再乐观猜状态。
2. bridge 可 additive 扩展现有 goal handler，或增加 Codex 专用 typed RPC 后在 projection 层汇合；选择标准是保持 dsh `SessionGoalController` 行为和已有客户端兼容。不得让 Codex 被迫伪造 dsh 的 `id/revision/phase/blockedReason`。
3. wire Goal view 至少保留 `source`、threadId、objective、status、tokenBudget、tokensUsed、timeUsedSeconds、createdAt、updatedAt。旧 dsh 字段继续可解码；schema 使用 additive optional/版本 discriminant，canonical pack 与 iOS mirror 同步。
4. codec 处理 goal updated/cleared，attach 后执行一次 get 作为冷基线；response 和通知调用同一 reducer。事件丢失、bridge 重启或写结果未知时再 get 校准。set/clear handler 执行所有权与 feature 检查，错误原文透传。

### 6.4 并发与已知非原子边界

CordCode 同一 `(backendId, threadId)` 的同类写请求单飞。Compact 与普通 Send/批准实施入口共享现有 admission 或小范围共用 guard；已知在跑的 thread 默认拒绝 compact，不排队。Plan/Goal 写入分别串行化，并通过 generation + 官方回读处理 Desktop 同时写；跨会话不互锁。等待 compact started 的本地 pending 状态阻止重复点击。

**官方 Op::Compact 可以替换现有 task**；本地 idle 检查与上游请求之间没有已证明的 compare-and-set。Desktop 在检查之后同时开始工作仍有竞态，不能承诺“绝不中断 Desktop”。C5 必须记录目标行为；如产品要求绝对不抢占，须等官方可证明的原子拒绝机制，不能靠本地 mutex 声称解决。不得通过终止 Desktop、独占官方 controller 或补偿 turn 处理竞争。

空 ACK 没有 turnId：不采用“请求后第一个 turn_started 就是我的操作”的算法，不把 Desktop 无关完成或自动 compaction 当作该请求成功。按 **会话状态**观察官方压缩 item；仅在官方身份已确认的范围内关联其 turn。取消时仍未能确认目标 turn，则禁止猜一个 active turn 去 interrupt。

Plan 与现有 plan_review 都可能写 collaborationMode，必须使用同一 adapter 和 thread 级写序列。plan_review 的批准流程若已消费 pending plan，其切 Default 与发 `Implement the plan.` 的既有次序保持；手机同时切 Plan 的结果由官方 settings 回读决定，客户端不声称 first-answer-wins。Goal set/clear 同理：旧 response 不覆盖更新的 notification；没有服务端 CAS 时不承诺本地选择必胜。

### 6.5 时间预算与不确定结果

- 本次 `compress_context` bridge 总预算 **25s**，iOS **30s**；预算覆盖 attach/resume/发送/等待 ACK。底层独立 RPC timer 和 transport send 必须一起核验，不能让 Background/send 阻塞绕过 deadline。
- **压缩执行不受这个 ACK 预算限制**：服务端 ack 后操作独立运行，不复制 dsh/Grok 的 290/300s 同步命令等待器，不以客户端超时伪造服务端 cancel。
- 请求明确未发出可报未提交；发出后丢 ACK/断线只能报结果未确认。无幂等证据不自动重发；重连不重放写操作。
- accepted 后 30s 仍无可确认状态，撤销“提交中”提示，显示待确认并发起一次现有官方状态/分页读取用于校准；读取失败就保留真实未知与重试入口。该时间是观察预算，不是后端失败判定。不能把没有事件、历史没拉到或 thread idle 当作压缩成功。
- live gap/队列丢事件按现有 recovery/invalidation 通道触发重基线；不自建全局定时历史 polling。重复点击在未知未校准期间禁用；校准成功只恢复操作可用性，不把旧请求改判为成功。
- Plan list/update、Goal get/set/clear 使用同一 **25s bridge / 30s iOS** 交互预算。超时后若请求可能已发出，显示待确认并各做一次官方 settings/goal 读回；不能自动重发写请求。只有读回的权威状态与意图一致时才显示已生效。

## 7. SSV2、历史与 iOS 必改全链

| 层 | 具体工作 | 不能省略的验收 |
| --- | --- | --- |
| Remote codec | 复用 contextCompaction started/completed；保留 error、turn status、epoch 与 ID | `decodeTurnCompleted` 当前仅 failed 特判，非 failed 默认 Result；新步骤结算不得因此把 interrupted/未知当成功；不影响 plan_review 既有动作 |
| wire event | 在现有两个 context 事件 additive 保留 turnId/itemId；不要依赖从 active map 猜 ID | 重复/迟到/跨 turn 不误结算；旧客户端可忽略新增字段 |
| reducer/kernel | 新增两个 context 分支，按官方 turnId/itemId 维护步骤；开始即生成完整可见 part 并 commit | 原 turn_started persist-only 策略不能让无 user 行压缩消失；无需合成 user 行；raw seal 保持 |
| snapshot/patch/window | 复用 partOps/turnStateOps、syncRev/bridgeEpoch | 同一状态经 snapshot 与 patch 一致；断线旧 epoch 不覆盖；步骤状态不会被晚到 started 翻回 |
| cold history | full-read 与 paginated summary/detail 的同一 item 映射；沿用 turn_detail_lazy/chunks_v1 | compaction item 不只剩字符串 system note；无正文 turn 被保留；分页未加载详情不代表完成/空历史 |
| collaboration state | codec 解 settings updated，attach 冷水合；snapshot/patch 带 typed mode/preset identity | Plan/Default 往返不覆盖 thread model/effort；旧 epoch/update response 不回滚 Desktop 新值 |
| goal state | get 基线 + updated/cleared + set/clear response 汇入单 reducer | dsh 与 Codex discriminant 明确；完整 status/usage/time 冷热一致；response/notification 去重 |
| iOS model/store | 加 context part、collaboration mode、Codex goal 解码/呈现，沿已有 mapping/store | raw 事件不绕过 SSV2；保留 Grok sessionMode 和 dsh Goal 的既有语义 |
| 菜单 | 复用 AttachMenuPlanner 与两种布局；增加三项 typed action case | 不通过空 hint 误走 execute_session_command；不默认套 dsh；各 capability 独立隐藏 |
| RPC client | BackendClient + conformer/transport 加三项 typed 方法；Compact 解析 accepted，Plan/Goal 解析完整状态 | 失败不 normalSend；draft 不丢；回调捕获 backend/session/host/epoch/generation，A→B→A 不串 |
| protocol pack | canonical 与 iOS mirror 同步三项 capability、RPC、状态、context 身份与 part | Compact 保持现有 RPC 名；Plan/Goal 只加最小方法；都不写成 session_commands |

`ContextCompaction` item 本身不提供完整业务错误状态，使用目标官方 error/turn 生命周期一起还原。对于无法证明的冷路径状态显示“压缩记录/状态未知”，不得从用户可见文字或 token 数下降推断操作成功。

## 8. Plan 与计划审批的复用及保护

现有 `agent/codex-remote/plan_review.go` 已实现：Plan item → turn/completed 生成 plan_review 卡，批准代发官方 `Implement the plan.` + Default collaborationMode；requestChanges、quit 另有实现。主动 Plan 入口必须复用其低层 settings adapter、`permission_request`、`resolve_permission`、正文渲染与 pending 消费，不重写审批事件族。

本案不把 `permission_mode=plan` 加到 Codex，不接 `/plan` 文本，不将 Grok sessionMode 直接复用为 Codex collaborationMode。手机选择 Plan 只写 thread collaboration settings；Mac 已在 Plan 时压缩不得本地补 Default 或改模型/effort。Plan review 批准后的既有 Default 切换属于审批行为，须回归确认其权威 settings 及时投影到手机。

TUI 的“Implement this plan?”是客户端编排，不是一个上游 wire approval request；现有 bridge 卡与 Desktop 自己的选择器也不是天然跨客户端 first-answer-wins。不能把第一批 grok/dsh 审批协议的这一保证套给 Codex。回归覆盖三项菜单与待审批卡共存、外部 settings 变化、外部新 turn 使旧卡失效、approve 仍走原通道；未授权真实 turn 时只做既有定向 fixture 回归，产品验收待执行。

## 9. 后续动作的完整边界（Review 与 Skills）

### 9.1 Review

官方 `review/start` 已有结构化 target：uncommittedChanges、baseBranch、commit、custom；inline/detached 的返回都带 reviewThreadId 与 turn。**本案后续推荐 inline，未取证不开放**；不为“像 dsh”把 review 变成字符串 parser。

四拍：`＋ → Review` → 官方语义的目标选择（未提交修改可直接选；分支/commit 必须来自目标 thread 工作区的真实列表；custom 必须有非空输入，取消保留原 draft）→ typed target 调 `review/start` → 按 response 的 reviewThreadId/turn.id 订阅并显示审查过程、结果、失败和冷历史。不显示无输入控件的 `/review` 占位，不通过普通聊天冒充专用 review。

实施门：Remote 每种拟支持 target 的真实样本、目标权限/失败/取消、enteredReviewMode/exitedReviewMode 或目标实际 item 形状、结果展示与冷拉闭环。当前 codec/懒加载类型准入未覆盖所有 review item，必须逐族补齐。分支/commit 目录无远程可信来源则移出该子项，不用 Bridge 本机 Git 猜测。只有新增专用调用确有必要时才复用现有会话操作结构 additive 扩展；不先造通用 SessionAction 执行平台。

### 9.2 Skills

四拍：`＋ → 技能` → 用目标 cwd 的 `skills/list` 显示可用项、选择技能后保留输入并补任务 → 现有 `turn/start` 的正式 Skill input 与用户文本共同发送 → 既有 turn/item 直播和冷历史。需新增的是当前 SendWithOptions 尚未承载的 typed input，不是把 `$name` 当魔法字符串或本地展开文件。

每次真实刷新按目标 `forceReload` 字段样本，skills/changed 仅失效信号；同名技能以官方 path/身份区分。目录错误不读本机技能兜底；禁用/缺失技能不能偷换成正文。独立技能文档和证据门完成前不广告。

## 10. 开发队列与证据收口

实施时使用 exec-plan skill：`/exec-plan docs/2026-09-07-codex-remote-slash-command-panel-implementation.md start`。由实施 agent 创建/恢复规范状态，每单元 impl/tests/regression；本轮仅文档，不初始化 JSON。

| 单元 | 依赖与工作 | 完成证明 |
| --- | --- | --- |
| P0 来源/Remote 样本 | C0–C5，读目标 tag 官方测试与本仓旧样本 provenance；三项真实写入分别报明副作用 | 三项脱敏请求/响应/事件/冷拉，版本与运行来源、成本记录；未取证明确 blocked |
| P1 共用 identity/readiness | C0/C1；抽 attach、ctx、backend/thread/epoch 校验与唯一 relay | 只读打开无 registry 可准备，错误可返回，各 capability 独立且 handler 二次验证 |
| P2 Compact adapter/投影 | C2/C5；复用 CompactContext，补事件身份、SSV2 与冷恢复 | 正确 method/accepted；无 user turn 冷热 identity；错误/中断/ACK 未知不假成功 |
| P3 Plan adapter/投影 | C3/C5；抽现有 settings update，增加 list/current/update 与 typed projection | 官方 preset、Plan/Default、per-thread model/effort 保留、冷启动与 Desktop 竞争收敛；plan_review 不回归 |
| P4 Goal adapter/投影 | C4/C5；扩展或并列现有 goal controller，增加 get/set/clear 与 Codex view | 空/创建/更新/清除、double-optional budget、全状态/用量、冷热/并发；dsh 不回归 |
| P5 iOS 菜单与三项 action | 接 P2–P4；先重新核对 iOS HEAD/dirty set | 两种布局、三项 typed RPC、表单/banner、草稿保留、A/B scope、各 capability 隐藏与禁用 |
| P5.7 智能体可见性与 /goal 对齐（2026-09-11 追加） | P5；codec 解码 collabAgentToolCall/subAgentActivity → 复用 dsh workflow part 槽位；iOS /goal claim 式输入拦截；goal bar 承载 Codex execution 期间 running-agents 徽标（TaskDock 当前为权限面） | goal 任务运行期卡片/成员状态/后台指示可见，/goal 交互与 dsh 一致，dsh/Grok 卡与 plan_review 不回归，owner 矩阵第 ⑦ 行 |
| P6 协议/文档/交付 | P1–P5 required 验证均完成，C6 实际产品验收 | canonical/mirror、CHANGELOG、双仓构建、安装身份、owner 真机结果 |

Review/Skills 不加入一期 required 队列；Compact/Plan/Goal 全部 required。不能用其中一项的 UI 占位、slash 文本或 capability 假广告代替完整闭环，也不能删除错误/恢复/过程状态来制造完成。源码/结构准备与真实运行取证可按依赖分开，不因等待真实写入授权停止所有独立工作。

## 11. 定向测试与 owner 验收

默认只跑受改范围的 Go 单测、Swift 非 UI unit test、定向 build；目标官方测试作为 fixture/不变量参考，**不默认编译运行整个 Rust workspace**。超 5 分钟异常止损。未获明确授权不跑 UI/snapshot/simulator automation；改 iOS 后，P0 配对及产物身份通过、连接 iPhone 时按用户要求安装。

| 测试面 | 必须覆盖 |
| --- | --- |
| 接线 | sessionId 空/未知/跨 backend、只读打开无 registry、attach 失败、既有 listener 复用、capability 关闭仍拒绝 RPC、不会 thread/start 或 Send `/compact` |
| 时间与错误 | 25s/30s ACK 预算贯穿 transport；ACK 丢失不重发；accepted 不提前完成；item 完成但 turn/error 失败不报成功；interrupted/未知终态保留 |
| 并发 | 同会话双点单飞、A/B 独立、普通 Send/approve 竞争、Desktop 同时开始 turn 的官方 Replaced 行为；无请求关联时不误认领/误取消 |
| 投影 | 无 user 行也 publish；官方 itemID dedup；完成先到/旧 started 迟到；raw seal 不破；压缩和自动压缩同语义；full/paginated/window/detail 无重复或消失 |
| 恢复 | ACK 前后断线、reader 丢事件、bridge epoch 变化、重连冷读失败；不把空白/idle/token 下降当成功；未确认可恢复为实际会话状态 |
| Plan | list preset 缺失/未知值；当前 Default/Plan 冷水合；update error/超时读回；per-thread model/effort 不被全局值覆盖；Desktop 同时切换；plan_review 批准切 Default 仍投影 |
| Goal | get null/完整；set 三态 budget；六种 status/未知未来值；clear false/true；response 与通知乱序；断线 get 校准；parent-owned 拒写；dsh Goal JSON 与 banner 不回归 |
| iOS | Codex native policy 不落 dsh；两布局三项齐全；文本/附件不丢；Compact 无参别名和非法参数；Plan/Goal 表单；A→B→A 旧响应/错误/执行标记不串；Grok/dsh 菜单与 plan_review 不回归 |

Owner 手工矩阵：①Desktop 先打开的任务，手机只读打开后三项均可见；②带草稿/附件点 Compact，过程可见，完成后重开仍有官方记录且草稿不丢；③切 Plan 后下一 turn 产生计划，审批后 Default 状态回到手机，原审批路径可用；④创建目标、看到 banner/用量，Desktop 更新后手机同步，清除后重开仍为空；⑤三项在切会话、断线、失败时不出现假成功；⑥Mac 与手机同时修改 Plan/Goal 后以官方读回一致；⑦（P5.7）goal 任务运行期间，iPhone 可见 subagent workflow 卡（成员随官方创建出现、状态随官方更新）、后台任务指示随 turn 起止出现/消失，`/goal <任务>` 输入交互与 dsh/Grok Build 一致。实际操作、日志和来源逐项记录；本文件的列表不是测试已通过声明。

## 12. 回滚与交付口径

回滚按能力独立撤下 `context_compaction`、`session_collaboration_mode`、`session_goal` 的菜单与写 handler；保留已发布的 additive 解码，使旧客户端/历史仍能读取真实 compaction/settings/goal 状态。Remote adapter 明确返回不支持，不改回 slash prompt。其他 backend `session_commands`、模型选择、计划审批与 Remote enrollment 不受影响。若任一 required 能力回滚，交付状态恢复为一期未完成。

交付报告分别列：文档、三项目标 Remote 证据、代码/单测、双仓 build、安装来源、owner 验收。只有 Compact、Plan、Goal 的 required 门全部通过才能称“Codex Remote 三项命令面板完成”。不能写“全量 Codex slash commands 已支持”，也不能把 Review/Skills 暂未实施隐去。
