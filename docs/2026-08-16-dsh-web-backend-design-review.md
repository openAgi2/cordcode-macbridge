# dsh-web Backend 设计稿评审报告

- 评审对象：`docs/2026-08-16-dsh-web-backend-design.md`（设计稿，未实施）
- 评审日期：2026-08-16
- 评审方法：dsh 源码 pin 47f9438 逐条对照 + **本机 3080 运行实例活体采样**（owner 真实安装形态 @deepseek-ai/dsh@0.1.0-rc.6）+ bridge/iOS 源码核对
- 评审纪律：audit-plan（内容形状断言必须有真实 dump 背书；否定性断言标注查证层面）

## 1. 结论

**修改后通过。** 路线方向正确（官方 API 转发 + bridge-v1 翻译、MacBridge 端转换、iOS 近零改），§3 四项核实的**语义类结论**（prompt resume、无版本协商、trust fence、方法存在性）经源码与活体双重验证基本属实；§4.3 功能面映射对 bridge 全 RPC 面的覆盖率约 95%，⛔ 项全部诚实。

但存在 **1 项阻断级事实错误**（事件流载波是 WebSocket 不是 SSE——活体 426/101 实验证实）和 **4 项必改**（SessionActivityProbing 缺失致坑 4 后半复现；approval policy=ask 与「审批为决定项」的分期矛盾；codec「复用 vs 不共享」矛盾及 main 分支依赖；「投影代码零新增」与 ≥8 处 backend-id 键控接线点不符）。全部可在设计文档层修复，不需推翻路线。

## 2. 问题清单

### 阻断

**B1. 事件流载波是 WebSocket，不是 SSE（§3.2/§4.3.1/§4.3.3/§6/§8 全面失实）**

设计通篇按 SSE 写（「两条常驻 SSE」「SSE mux 管线」「SSE 帧判别联合」「SSE mux 帧序列 fixtures」）。证据链：

- `packages/client/connection/src/api-path.ts:8-14`：注释明写「Browser mux-frame **WebSocket** pathname」；
- `packages/client/connection/src/websocket-downlink.ts:1-52`：服务端载波是 `ws` 库的 `WebSocketServer`（upgrade + pump）；
- `packages/client/connection/src/index.ts:150-154`：对 events 路径的普通 GET 恒回 `426 Upgrade Required`；
- **活体实验**（本机 3080 实例）：`GET /api/events.mux` → `HTTP 426` + `upgrade: websocket`；带 WebSocket upgrade 头 → `HTTP 101 Switching Protocols` 并立即推送帧流。
- 误判来源可解释：apiproxy README 把 ServerRequest 象限描述为「SSE（Server-Sent Events）帧」，那是逻辑象限措辞，物理载波是 WebSocket。设计 §3.2 引用了该 README。

伴随缺失的线格式细节（实施必需）：

- 流帧外层还有一层 **ServerRequest 信封** `{type:"server-request", rpcId, method, payload}`，MuxFrame/HostFrame 是 `payload` 槽（活体帧原文核对）；
- unary 请求体是 **ClientRequest 信封** `{type:"client-request", rpcId, method, payload}`，响应是 `{type:"server-response", rpcId, result:{ok,value}|{ok:false,error}}`（活体：裸 `{}` POST 被 `bad-request` 拒绝并逐字段列出缺失）；
- Go 侧需要 WebSocket 客户端（非 opencode `sse_subscriber.go` 的 SSE 先例），§8 拆分 3 与 §6 fixtures 描述需随之改写。

**修改建议**：§3.2 重写载波小节（WebSocket + 信封三层结构 + 426 行为），§4.3.3「两条常驻 SSE」改为「两条常驻 WebSocket 下行流」，§6 fixtures 改为 WebSocket 帧序列 + 假服务需讲 WS，§8.3 改为「WS mux/host 双流管线」。

### 必改

**M1. SessionActivityProbing 未设计——坑 4 后半（死会话尾封口）将复现**

`go-bridge/handlers_projection.go:1232-1238`：冷 hydrate 只在 backend 实现 `core.SessionActivityProbing`（`core/interfaces.go:185-193`）时才封口尾部未答 turn，否则「commit gate waits rather than guessing」——即不实现该接口的死会话（尾部只有用户消息、无终态 turn）**冷开永远 loading**。这正是旧路线真机二轮的故障形态，§2.2 坑 4 标记「部分消除」并要求评审确认，但设计正文（§4.3.2「机制同旧件，仅换数据源；投影代码零新增」）全文未出现 SessionActivityProbing。dsh-web 有天然数据源：`session.list` 的 `running` 字段 / `host/session-status{running}` 帧（错误/未知 ⇒ active，保守方向正确）。

**修改建议**：§4.3.2 增加一段：dsh-web 实现 `SessionActivityProbing`，数据源为 running 徽标（探活缓存 + host 流），错误时保守返回 active；§6 增加对应单测（死会话尾封口 / 活会话不封口 / 探活失败保守）。

**M2. 默认 approval policy=「ask」——「审批为 owner 决定项」与 fail-visibly 红线冲突**

活体采样（真实会话日志头三事件）：`permission/preset=workspace-write`、`approval/policy="ask"`。若一期不接审批面（设计把它列为「owner 决定项，倾向一期」），iOS 经 dsh-web 发起的 turn 在首个需审批工具处会收到 `approval/requested` 帧而 iOS 无应答入口 → turn **无限挂起**（既非终态也无错误），直接违反 §4.3.3 自己的红线（坑 8 类别）。旧路线靠自组 cordis.yml 权限栈规避，本路线会话由官方 preset 组装，无此规避。

**修改建议**：把审批从「决定项」升格为一期必接（帧与 `/api/respond` 面已核实存在：events.schema.ts:46-52 + rpc-map.ts 头注释 + README），或在设计中明确写出一期不接时的政策后果与规避手段（例如 iOS 新建会话显式选非 ask 的 preset——需先核实 preset 面可行），二选一，不许悬空。

**M3. §4.1「不与旧 agent/dsh 共享代码」vs §4.3.3「复用 agent/dsh/codec.go §3.3 映射」矛盾 + 未声明的分支依赖**

`git ls-tree main agent/` 证实 **main 分支没有 `agent/dsh`**（只有 claudecode/codex/grokbuild/opencode/providerseedtest）——旧 backend 全部在未合并的 `dsh/driver` 分支（收口文档遗留项）。设计既说新包不共享旧代码，又说复用旧 codec 的映射表，且未声明实施基线。

**修改建议**：明确二选一并写入 §4.1：实施前置 = 两仓 `dsh/driver` 合回 main；「复用」的含义 = 把 codec §3.3 映射**复制**进 `agent/dshweb`（连同其单测），不做 import。

**M4. 「投影代码零新增」不成立——backend-id 键控接线点需逐一加入**

`"deepseek"` 在 go-bridge 至少 8 处按 backend id 键控：`agent_descriptor.go:216`、`handlers.go:1039`（剪枝名单，dsh-web 应**不**加入）、`handlers_projection.go:111/339/432/468/629/1003/1059`、`main.go:131`（driver 映射）、`server.go:282`。漏加任一处 = 对应机制对新 backend 静默不生效（例如不加 `backendSupportsProjectionHydrate:337-343` 则冷会话投影根本不启动）。且 dsh-web 的数据源是 HTTP rich history，应加入 **opencode/grokbuild 的 pathless 家族**（handlers_projection.go:468/629），而**不是** deepseek 的 store-file 分支（:432/:548）——「机制同旧件（deepseek）」字面照抄会接错分支。

**修改建议**：§4.3.2 改为「投影机制复用、零新增投影语义，但需按下表加入 backend-id 接线点」并逐点列出（含 pathless vs store-backed 的归属选择）；§8 拆分 2 或 4 增加对应工作项。

### 建议

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| S1 | `llm.providers` 混有大量休眠项（活体：amazon-bedrock/ant-ling/anthropic 等均 `active:false, declared:false`） | 活体采样 | §4.3.5 写明映射过滤规则（建议仅 `active:true` 进 `list_providers`，或如实全量但标注状态） |
| S2 | 跨 backend 差异清单缺一条：旧件**早期**会话记录 `default/deepseek-chat`（活体 session.models 采样），dsh-web 续聊会到模型校验层报错（可见错误，可 switch_model 修复） | 活体采样 `session.models{dsh-*}` | §5 补入；§6 真机矩阵可加一行 |
| S3 | managed 实例与用户**后来**自启的 3080 实例共存：同一 store、同一会话可能被两个 host 进程先后 resume；`session-persistence-jsonl` 未发现 flock/lockfile（源码检索层面；未做双实例写实验——避免污染真实 store，**不确定**） | grep 无锁证据 | 设计写明双实例策略（探测仅启动时 or 周期重探+迁移让位），残余风险如实标注；实施期用受控实验补证 |
| S4 | 审批/问答应答的桥内路由：`resolve_permission`/`question_reply`/`question_reject`/`resolve_user_input` 都要求 `h.getSession()` 命中活会话对象（handlers.go:3625-3660 区段）；**外部**（Mac web 发起）会话的 approval 帧到达时无桥内 session，路由行为未定义；另 dsh 的 question（含 plan-review intent、multiSelect）映射到 question_reply 还是 resolve_user_input 未指明 | handlers.go 源码 + events.schema.ts:20-32 | 设计补一段：外部会话 approval/question 帧的处置（只广播不进权限 UI / 或注册轻量 session 代理）；question 面映射选择写明 |
| S5 | 功能总表缺三行：`list_memory_files`/`read_memory_file`（`MemoryFileReader` 类型断言自动 not_supported，handlers.go:1766-1771）、`cancel_request_v1`（连接级通用面，handlers.go:4308-4310）——机制上无需新代码，但按「基准=功能全集」应入表 | handlers.go | 总表补三行 ♻️/⛔ |
| S6 | `host.describe` 的 `version:"0.0.1"` 是占位符（活体实测），不是 npm 包版本（0.1.0-rc.6） | 活体采样 | §4.3.8 diagnostics 写明版本字段语义，不冒充 dsh 版本 |
| S7 | §6 测试计划缺：RpcError message 全文透传断言（坑 7 闭环的测试面）、断线重连行为（重开流+history 重拉） | — | §6 补两行 |
| S8 | §8 拆分枚举与 §4.3 ✅ 集不对齐：`session.create`、`rename_session`、`list_directory`/`list_projects`、events.host 常驻流均未出现在拆分项里 | 对照 | §8 各项补全 |
| S9 | `session.search` 标 ✅ 但正文说「iOS 搜索现为本地实现已够；官方 search 可作二期」——实为一期不接 | §4.3.8 | 标记改 2️⃣，消除歧义 |
| S10 | 「iOS 仅增 case + 列表项」低估：`.deepSeek` 在 iOS 非测试代码 **11 个文件**有穷举 switch（BackendModels/ChatUIKitContainerView×5/ChatViewModel+Generation×2/SelectionSheets/CCCodeBridgeBackendClient/SessionLifecycleDiagnosticPhase/ModelManagementService/ChatViewModel/ChatViewModel+CodexStreaming/ChatViewModel+DirectoryPreferences/ServerViewModel），新增 case 时 Swift 穷举检查会全部编译报错，每处需归组决策；行为相关的如 DirectoryPreferences:107（list_projects 通用路径）、ChatUIKitContainerView:4335（context entry 显隐与 get_usage ⛔ 的联动） | iOS 仓 grep | §4.1 措辞改为「iOS 增 case + ~11 处穷举 switch 归组（编译器强制，不会静默漏）」并附文件清单 |
| S11 | §4.2「dsh-web-managed-server.json（端口与凭据状态）」——dsh v1 **无凭据面**（trust fence 明示 not an auth layer，api-request-trust.ts:13） | 源码 | 措辞改「端口与实例来源状态」，或在 §4.4 如实写明 managed 实例 loopback 无认证的暴露面（与用户自启 3080 同类） |
| S12 | iOS CLAUDE.md 要求专项计划「必须显式写出：真相 owner、唯一 writer、受影响事务域、是否新增数据路径、active 下全部写入口、失败呈现方式和防双写测试」——设计散落各节但无成段清单 | iOS CLAUDE.md SSV2 节 | 增设一小节逐项写明（大部分答案已在文中，成本低） |
| S13 | `list_permission_modes` ⛔ 的理由「无 bridge 级 mode 写面」过强：settings 域有 `permission` ns（README.md:61 明列 Web preferences allowlist）+ `permissions` 投影 unit 存在（permission-presets/src/index.ts:244），且 settings.* 是 loopback 特权方法、bridge 恰在 loopback 可调 | 源码 | ⛔ 处置保留（一期不接合理），理由改为「有面但语义与 bridge permission-mode 不一一对应，二期评估」 |
| S14 | `agentPresets.*` 实际方法前缀是 `agentPreset.*`（rpc-map.ts:54-59） | rpc-map.ts | §4.3.5/§4.3.8 措辞更正 |

## 3. 功能面覆盖对照表（A 维度交付物）

对照基准：`go-bridge/handlers.go:1238-1335` dispatch 全集 + delivery 面（1171-1216）+ iOS 既有用户可见功能。「独立核实」列的证据来源：源码 file:line 或活体采样（标 🧪）。

### 3.1 RPC 面（handlers.go dispatch 全集）

| RPC | 设计处置 | 独立核实 | 缺口判定 |
|---|---|---|---|
| hello | （协议握手，非 backend 面，未列） | 与 backend 无关 | 无缺口 |
| list_providers | ✅ `llm.providers` | 🧪 存在；响应含大量 `active:false` 休眠项 | 🟡 过滤规则未指定（S1） |
| set_provider | ✅ `session.selectModel`（会话级，如实） | selectModel handler api-proxy.ts:2282-2331，`saveDefaultModelSelection` best-effort 持久化默认 | 🟢（语义描述准确） |
| list_models | ✅ `llm.models` + `session.models` | 🧪 两者形状与设计一致（groups/current/routable/failures） | 🟢 |
| list_agents | ⛔ 一期（`agentPreset.*` 二期） | rpc-map.ts:54-59 存在 agentPreset.list/select/… | 🟢（前缀笔误 S14） |
| list_permission_modes | ⛔ 一期 | 无对应 RPC ✓；但 settings `permission` ns 与 `permissions` 投影存在 | 🟢（理由需修正 S13） |
| set_permission_mode | ⛔ 一期 | 同上 | 🟢（同上） |
| create_session | ✅ `session.create{cwd}` | sessions.schema.ts:101-116；attach 在 create 流程内 api-proxy.ts:2220（`workspace.attachSession`）🧪 | 🟢 |
| send_message | ✅ `session.prompt{mode:"queue"}` | sessions.schema.ts:287-302；resume 语义 api-proxy.ts:1657/1670（ensureSession） | 🟢（审批依赖见 M2） |
| abort_generation | ✅ `session.cancel` | sessions.schema.ts:345-353 | 🟢 |
| get_session | ✅ 列表缓存/history 头 | list 返回全字段 🧪 | 🟢 |
| get_session_messages | ✅ `session.history` | 🧪 events+hasMore+尾页 projections；DEFAULT_MAX_MESSAGES=50（api-proxy.ts:114）；冷读不 resume（history 走 detached source，2242-2245） | 🟢 |
| get_session_projection | ✅+♻️ SSV2 通用管线 | 机制在 bridge；**id 键控集合需加入**（handlers_projection.go:337 等） | 🔴 M4 |
| delete_session | ⛔ 官方无 delete | rpc-map.ts 全表无 session 删除方法（workspace.delete 是工作区删除）——**查证层面：RpcMethodMap 全量 51 方法 + schema 目录 + README** | 🟢 诚实 |
| resume_session | ✅ history+订阅 | 同上 | 🟢 |
| switch_model | ✅ `session.selectModel` | 🧪 形状一致 | 🟢 |
| resolve_permission | 4.3.4 审批决定项（声明制） | approval 帧+`/api/respond` 存在（events.schema.ts:46-47）🧪；桥内路由依赖活会话对象 | 🔴 M2 + 🟡 S4 |
| list_sessions | ✅ `session.list` | 🧪 10 项实测：字段全、subagent 混返需过滤、**title 投影 unit 实际存在**（session-title/src/index.ts:309，`projections.values.title` 直出真实标题） | 🟢（设计的主路径成立，fallback 仅在无 session-title 插件时触发） |
| list_projects | ✅ `workspace.list` | 🧪 items{workspaceId,path,title,sessionIds,…} | 🟢 |
| fetch_todos | ⛔ 一期 | 无 todo RPC ✓；`todos` 投影 unit 存在（tool-todo/src/index.ts:136）🧪 | 🟢（二期有真实数据面） |
| get_workspace_diff / get_turn_diff / get_full_thread_diff | ⛔ diff 三件套 | 无 diff 方法（RpcMethodMap 全量核查） | 🟢 诚实 |
| get_usage | ⛔ 一期（token-meter 投影） | 无 usage RPC ✓；tokenUsage/contextPressure/contextBreakdown 投影存在（token-meter/src/index.ts:88-90）🧪 values 有真实值 | 🟢（iOS 显隐联动见 S10） |
| run_diagnostics | ✅ 探测结构化输出 | 🧪 host.describe 可用；version="0.0.1" 为占位 | 🟢（S6） |
| list_memory_files / read_memory_file | **未提及** | handlers.go:1766-1771 `MemoryFileReader` 断言自动 not_supported | 🟡 总表补行（S5） |
| fetch_content_chunk / read_file_v2 | ♻️ bridge 通用 | handlers.go:1292-1295，backend 无关 | 🟢 |
| cancel_request_v1 | **未提及** | handlers.go:4308-4310 连接级 read_file_v2 批量取消面，backend 无关 | 🟡 总表补行（S5） |
| list_directory | ✅ `host.listDirectory` | rpc-map.ts:43 + host.schema 存在 | 🟢 |
| get_git_context / PR 三件套 / commit_and_push / branch×2 / worktree | ⛔ 无对应面 | RpcMethodMap 全量无 git 域方法 | 🟢 诚实 |
| rename_session | ✅ `session.rename` | sessions.schema.ts:118-128（规范化 accepted title 回传） | 🟢 |
| share_session | ⛔ bridge 通用 | handlers.go:1316-1320 | 🟢 |
| archive_session | 2️⃣ `workspace.archiveSession` | rpc-map.ts:52 存在 | 🟢 |
| set_session_pinned / list_pinned_sessions | ♻️ bridge pin 索引 | handlers.go:112-116 `pinStore` 为 bridge 级通用 | 🟢 |
| compress_context | ⛔ | 无 compaction RPC | 🟢 诚实 |
| check_pending_notifications | ♻️ | bridge/relay 通用 | 🟢 |
| question_reply / question_reject / resolve_user_input | 4.3.4 决定项 | question 帧 schema 含 options/multiSelect/plan-review intent（events.schema.ts:20-32）；桥内路由同 resolve_permission | 🔴 M2 + 🟡 S4（两个应答面选型未指明） |
| delivery prekey ×3 | ♻️ | handlers.go:1171-1216，relay 通用 | 🟢 |

### 3.2 iOS 既有用户可见功能

| 功能 | 设计处置 | 独立核实 | 缺口判定 |
|---|---|---|---|
| 会话列表（目录分组/标题/时间/运行徽标） | ✅ 4.3.1 | 🧪 cwd/title/running 全供给 | 🟢 |
| 置顶 | ♻️ | pinStore 通用 | 🟢 |
| 下拉刷新 | ✅ + forceCold | 既有机制（两处 forceCold 集合需加 id，收口文档先例） | 🟡 并入 M4 |
| 列表自动同步（双层） | ✅ host 流即时 + discovery 兜底 | events.host 帧 schema 证实（events.schema.ts:70-86）；`session_discovery.go:50` watcher、`event_publisher.go:773` sessions_changed 控制面均核实；mux 全会话覆盖 🧪 | 🟢（host 流载波随 B1 改 WS） |
| 消息页（思考流/工具卡片/分页） | ✅ history+投影 | 🧪 history 形状；codec 映射依赖 M3 | 🟢（M3/M4 修复后） |
| 流式同步/isGenerating/停止/完成通知 | ✅ mux→codec→既有管线 | 🧪 mux 帧流实测；载波为 WS（B1） | 🔴 B1 |
| 外部 turn 可见 | ✅（不进 `backendHasNoExternalEventSource`） | handlers.go:1038 现仅 `deepseek`，新 id 默认不进 ✓ | 🟢 |
| 发送（新会话/既有会话） | ✅ create+prompt / 直接续 | §3.1 语义源码+活体核实（resume 三级模型解析 api-proxy.ts:1142-1160） | 🟢（审批 M2） |
| steer 插话 | 2️⃣ | `mode:"steer"` 面存在 | 🟢 |
| 附件/图片 | 2️⃣（声明 text-only） | prompt content 含 image part + `session.attachment` 面存在（sessions.schema.ts:282-327） | 🟢 |
| 模型/provider 切换 | ✅ | 🧪 | 🟢 |
| 权限审批/问答 UI | 决定项（声明制） | 帧与应答面存在；policy=ask 实测 | 🔴 M2 |
| 目录选择器/项目建议 | ✅ listDirectory+workspace.list | 🧪 | 🟢 |
| 重命名 | ✅ rename | 🧪 schema | 🟢 |
| 删除 | ⛔（iOS 禁用入口） | 官方无面 ✓ | 🟢 |
| 归档 | 2️⃣ | archiveSession 存在 | 🟢 |
| 通知 | ✅ 事件管线 ♻️ | — | 🟢 |
| 使用量 | ⛔ 一期 | 投影面存在（二期真实） | 🟢 |
| 搜索 | ✅备用（标记混乱） | session.search 存在 🧪 | 🟡 S9 改 2️⃣ |
| todos | ⛔ 一期 | 投影面存在 | 🟢 |
| diff/git 面入口 | ⛔ 隐藏 | 官方无面 ✓ | 🟢 |

**覆盖结论**：对照基准共 53 个 RPC + 20 项用户功能，设计给出明确处置 50/53（94%）；未处置 3 项（memory files ×2、cancel_request_v1）均为机制自动兜底，无功能损失（S5 补表即可）。处置诚实性抽验全部通过——所有 ✅/2️⃣ 均在源码+活体找到对应面，所有 ⛔ 均在 RpcMethodMap 全量（51 方法）+ schema 目录 + README 三个层面查证无对应面。分期集合构成可用产品，例外是 M2（审批）这一处分期与红线冲突。

## 4. 三项遗留评审项闭环判定（C 维度）

| 遗留项 | 判定 | 依据 |
|---|---|---|
| ① 投影尾封口（坑 4） | **未闭环** | 设计红线只覆盖了「turn/end reason=error 透传为终态」这半边；**死会话尾部未答 turn 的封口**需要 backend 实现 `core.SessionActivityProbing`（`handlers_projection.go:1232-1238` 明写无该接口则 commit gate 永远等待），设计正文零提及。不补必复现「冷开 endless loading」。→ M1 |
| ② RpcError 文本透传（坑 7） | **文本闭环** | §4.3.3 红线明写「turn/end reason=error 必须透传官方错误文本为终态事件」，RpcError 封闭码集与 message 字段源码核实（rpc.schema.ts）。缺口仅在测试面：§6 未列透传断言行。→ S7 |
| ③ 失败路径必现终态（坑 8） | **文本闭环，分期矛盾** | 红线段落成立（事件终态 + send 错误气泡兜底）；但 M2 场景（policy=ask 且不接审批）产生「挂起等待审批」的非终态路径，红线与分期决定互相打架，设计未处理。→ M2 |

§2.3 四条过程纪律反检：①外部断言先读源码——本设计大部分达标，但载波断言（SSE）恰恰是引用 README 措辞而未读载波实现/未做活体验证的反例（B1）；②否定性断言标注查证层面——⛔ 项均经得起三层查证，达标；③事实取自运行时声明面——达标（模型目录/标题/归组全由 API 供给，title 投影活体证实）；④失败路径可见终态——文本达标、分期未达标（M2）。

## 5. 附：证据索引

- 活体采样（本机 127.0.0.1:3080，pin 47f9438 环境）：426/101 载波实验；host.describe；session.list（10 项，title/tokenUsage/contextPressure 投影实测）；llm.providers/models；workspace.list；session.models（旧件会话 default/deepseek-chat）；session.history（信封+尾页 projections+approval/policy=ask）；mux 帧流（session/subscribed×全部会话、queue、jobs）。
- dsh 源码：`packages/host/apiproxy/src/api/{rpc-map,sessions.schema,events.schema,rpc.schema}.ts`、`api-proxy.ts`（1618-1707 ensureSession / 2242 history / 2461 prompt / 2282 selectModel / 1292-1316 投影注册）、`packages/client/connection/src/{api-path,api-request-trust,index,websocket-downlink,client/web-api-client}.ts`、`packages/api/remotes/src/agent-lookup.ts`、`packages/{session/session-title,plan/plan-mode,todo/tool-todo,llm/token-meter,interaction/permission-presets}/src/index.ts`。
- bridge 源码：`go-bridge/handlers.go`（dispatch 1238-1335、memory 1766、剪枝 1038、pin 112-116、cancel 4308）、`go-bridge/handlers_projection.go`（111/333-345/432/468/629/1003/1059/1232-1238）、`go-bridge/{event_publisher.go:773,session_discovery.go:50}`、`core/interfaces.go:185-193`、`main.go:131`、`server.go:282`。
- iOS 源码：`OpenCodeiOS/Services/Backend/BackendModels.swift`、`Services/Bridge/CCCodeBridgeBackendClient.swift:25-38` 及 §S10 所列 11 文件。
- git：`ls-tree main agent/`（无 dsh 目录）。
