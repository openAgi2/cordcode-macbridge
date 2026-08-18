# dsh-web Backend 设计（官方 Web API 转发 + bridge-v1 翻译）

- 日期：2026-08-16（v2：一轮评审；v3：二轮 APPROVE+R2 尾项；v3.1：三轮 R3-1/R3-2 必改；v3.2：四轮 APPROVE 收口 + owner 三项指令落稿（iOS 改动量核验/新建 agent/dsh-web 目录/API 盲区兜底许可）+ S-1…S-3 收尾，见文末「评审采纳记录」）
- 状态：**已实施并合入 main，owner 真机验收通过**（2026-08-18）。exec-plan 队列 `plan-a46e4391b790` 全部 done。产品显示名现为 DeepSeek Harness（wire kind 仍是 `deepseek-web`）。完成报告见 `docs/2026-08-18-dsh-web-backend-design完成情况.md`。
- 背景：SDK stdio 路线收口暂停（`docs/2026-08-16-dsh-session-store-bridge-design完成情况.md` 收口节）；owner 裁决并行新增本 backend，旧 `deepSeek` 保留不删。后于产品入口退役，日常只用本路线。
- 不变约束：CordCode 初衷（探测-复用-未启动、零迁移、双向接力、永不自托管）+ SSV2 十二条护栏。

## 1. 路线定义（一句话）

dsh-web backend = **官方 Web API 的请求转发器 + bridge-v1 成熟格式（codex/claudecode/opencode/grokbuild 已验证）的翻译器**。所有数据格式转换在 MacBridge 端完成，iOS 几乎零改。MacBridge 不持有任何自己推导的 dsh 事实（模型名、编码、标题、归组全由官方 API 供给）——上一路线三轮真机故障的类别从架构上删除。

## 2. 前车之鉴：上一路线（SDK stdio）的原理、坑与经验（供评审员）

评审本设计前必读。上一路线的实现、三轮真机修复与收口全文见 `docs/2026-08-13-dsh-driver-design.md`（实现）与 `docs/2026-08-16-dsh-session-store-bridge-design完成情况.md`（含收口复盘）；本节是其浓缩。

### 2.1 上一路线基本原理（仍保留在两仓 dsh/driver 分支，真机可用）

- **进程模型**：每活跃 session spawn 一个 `dsh-jsonrpc-agent` 子进程（SDK JSON-RPC 2.0 over stdio：initialize / session/prompt / shutdown 三个请求 + 四个通知），driver 自组 cordis.yml 插件栈（权限栈 + `dsh-session-persistence-jsonl` + `dsh-llm-deepseek`）。
- **身份**：会话 id 由 driver 生成（`dsh-<nonce>`）；SDK 对未知 id 懒创建 agent+session 对。
- **数据面**：live 事件来自子进程 stdout（codec 映射到 core.Event）；列表/历史 = MacBridge 直接解析用户 `~/.dsh/sessions`（JSONL 明文/zstd 双格式、标题折叠自日志内 `session/title` 事件）；投影 = SSV2（live-only admission 起步，后演进为 file-backed pathless 全量重建）。
- **环境事实**：用户真实形态 = 全局 npm `@deepseek-ai/dsh@0.1.0-rc.6`（源码 checkout 仅为参考材料）；DSH 会话存储默认 `~/.dsh/sessions`，web 与 driver 共写。

### 2.2 踩过的坑（评审时按此清单核对）

| # | 坑 | 事实/根因 | 新路线是否结构性消除 |
|---|---|---|---|
| 1 | **路线锚定错面**：把「SDK 协议面没有 list/resume」当成「无法读 Mac 端会话」，为错误结论建造 live-only 投影、iOS 隐藏列表等整套补偿架构，后全部拆除 | dsh 暴露三个面：协议面（最窄）、文件面（store，claudecode 先例就在本仓）、web API 面（最全）；调查只查了第一个 | **是**（本设计即以 web API 面为基础）；过程纪律：否定性断言必须标注在哪几个层面查证过 |
| 2 | **存储编码冲突**（真机一轮）：driver 写明文，web 写 zstd；harness `checkRootEncoding` 扫整个 root，任一相反编码工件即在 materialize 时拒绝 → `turn_started` 后 1ms `turn_error` | 共写用户 store 必须对齐在位写者的编码（zstd）；修复=driver cordis.yml `compression: zstd` | **是**（本路线不触碰持久化配置，会话由 web 服务自己写） |
| 3 | **模型名越界**（真机二轮）：driver 默认 `deepseek-chat`，rc.6 官方路由仅支持 `deepseek-v4-pro/flash` | 运行时自己的报错就念出了白名单——模型事实应取自运行时（`llm.models`），不应手写 | **是**（`list_models` 直连官方目录；resume 会话用其自记录模型） |
| 4 | **投影基线优先级 + 尾封口**（真机二轮）：活会话被误推 file 重建与 live 流竞争 → `projection.hydrating` 循环；失败 turn（只有用户消息无回复）的尾部未答 turn 永不封口 → 基线永不提交 | 正确矩阵：live/kernel 会话一律实时基线，file 重建只服务死会话；死会话尾部未答 turn 需 `SessionActivityProbing` 如实封口 | **已闭环（v2）**——事件终态透传见 §4.3.3 红线；死会话尾封口经 `SessionActivityProbing` 设计收口（§4.3.2，评审 M1） |
| 5 | **「未分组」臆想**：未读 web 源码即断言「Chat 未注册 workspace，用户加上即可」，被双端截图纠正 | web 分组 = workspace.json 里每 workspace 显式记录的 sessionIds 名单（受两写恢复协议管理，**外部进程不可盲写**）；attach 只发生在 web 自己的 create/fork HTTP 流程 | **是**（本路线走 HTTP create，cwd 命中已注册 workspace 自动归组；且不写 workspace.json） |
| 6 | **materialize 防覆写**：对已存在 id 发 prompt，持久化拒绝重复物化（"refusing to materialize: a log already exists"） | SDK 面无 resume，死会话续聊只能诚实拒绝（`session_resume_not_supported`） | **是**（官方 HTTP 面有真 resume，见 §3.1） |
| 7 | **codec 折叠错误文本**：`turn/end reason=error` 被映射为泛化文案，底层错误（编码/模型）在日志不可见 | 排障时用裸 JSON-RPC 探针直连 runtime 取原始错误全文（`DSH_TURN_REPRO` 测试保留此手法） | 评审项：新适配器必须把官方 `RpcError` 的 message 透传到诊断/事件，不得折叠 |
| 8 | **iOS 失败 turn 卡「执行中」**：投影拉取循环 hydrating 时 iOS 消息页无终态可渲染 | 投影失败循环 + 事件终态缺一即卡；iOS 侧对 send RPC 错误有气泡文案兜底 | 评审项：映射须保证任何失败路径都产生终态事件或 send 错误（fail visibly） |
| 9 | **store 格式细节**（供任何直读 store 的评审参考）：projectKey 有损编码（UTF-16 码单元 `~XXXX`、分隔符运行合并、`--≤251>--` 包裹，真实 cwd 以头行为准）；子会话 `origin=subagent`/`delegationDepth>0` 应从列表过滤；标题=日志内 `session/title` 事件折叠+首条人类消息 fallback | 已在旧路线 store.go 落地并测试（projectKey 向量对照 TS） | 本路线不直读 store（`session.list`/`session.history` 供给），此项仅作旧件维护参考 |

### 2.3 过程纪律（从上述坑中提炼，评审员可用同尺检验本设计）

1. 对外部系统的任何行为断言，必须先读其源码/数据——「听起来合理的机制解释」≠ 证据（坑 5）。**纪律适用于一切写入文档的行为描述，包括落实评审建议时补的机制细节**（v3 的 R2-1/R2-2 即在修订动作里复发此病，三轮评审以 iOS question 存储/permission wire 载荷源码纠正）；
2. 否定性断言（「X 不可能」）必须标注在哪几个层面找过（坑 1）；
3. 事实（模型目录、编码、端口、能力）取自运行时自己的声明面，不手写复本（坑 2/3）；
4. 失败路径必须产生可见终态——禁止静默等待（坑 4/7/8）；
5. **物理载波类断言（传输协议、端口、格式）必须读实现代码并活体验证**——文档措辞可能描述的是逻辑象限而非物理现实（v2 教训：初稿把 WebSocket 载波写成 SSE，源头是 README 的「SSE 帧」象限措辞，评审以 426/101 活体实验纠正）。

## 3. 四项前置核实结论（源码 pin 47f9438 + 本机实测）

### 3.1 prompt 对既有会话 = 官方 resume ✅

`packages/host/apiproxy/src/api-proxy.ts:1653-1670`：`session.prompt` 对已存在会话走 `ctx.agents.resume({resumeSessionId, …})`（磁盘冷恢复，会话记录的 preset/模型选择随 resume 恢复），未知 id 走 `agents.create`。owner 已在 web 实证「对任意历史 session 发消息」。**模型选择三级解析**（进程内 > 会话日志 `request/header` > `agent-default-model`）——历史会话用它自己记录的模型，上轮「deepseek-chat 越界」类故障在这条路线上结构性不可能。

### 3.2 事件流契约 ✅（v2 更正：载波为 WebSocket，非 SSE）

**载波（评审 B1 活体实验纠正）**：两条事件流是 **WebSocket**（`websocket-downlink.ts` 为 `ws` 库 `WebSocketServer`；普通 GET 恒回 `426 Upgrade Required`，带 upgrade 头得 `101` 并立即推帧——本机 3080 实测）。初稿误写 SSE，源于 apiproxy README 把 ServerRequest 象限称「SSE 帧」的逻辑措辞；纪律固化见 §2.3 第 5 条。

三层线格式（实施必需，活体核对）：
- **unary**：`POST /api/<method>`，`application/json`（否则 415），请求体=`ClientRequest` 信封 `{type:"client-request", rpcId, method, payload}`；响应=`ServerResponse` `{type:"server-response", rpcId, result:{ok,value}|{ok:false,error}}`；业务错误恒 HTTP 200 + `RpcResult` error 分支（封闭错误码集 `RpcErrorDetailsMap`）；裸对象无信封会被 `bad-request` 拒；
- **流**：`GET /api/events.mux` 与 `GET /api/events.host`（均 WebSocket 升级）；每帧外层=`ServerRequest` 信封 `{type:"server-request", rpcId, method, payload}`，MuxFrame/HostFrame 位于 `payload` 槽；
- **反向请求**（审批/问题应答）：`POST /api/respond`（回传 `rpcId`——mux 帧信封里的 rpcId 原样回显）；
- 重连：`since` v1 未实现——重连=重开流+重拉 history（bridge 侧照做即可）。Go 侧需 WebSocket 客户端（非 opencode `sse_subscriber.go` 的 SSE 先例）。

MuxFrame（`api/events.schema.ts`，payload 判别联合）：
- `session/event`：**`event` 字段即 SessionEvent——与磁盘日志同构信封**（`{type,seq,time,data}`），现有 `agent/dsh/codec.go` §3.3 映射表可直接复用；另带可选 `view`（工具渲染意图）；
- `session/subscribed{lastSeq}`、`session/queue`（queued/steering/context 收件箱）、`session/jobs`、`session/projection{key,value,seq}`（高序覆盖的通用投影对）、`stream/error`；
- `approval/requested|resolved`、`question/requested|resolved`（应答走 `/api/respond`）。

HostFrame：`host/session-added`（含 parent/origin/cwd）、`session-removed`、`session-status{running}`、`agent-error`、`workspace-changed`。

### 3.3 托管启动形态 ✅（本机 `dsh web --help` 实测）

- `dsh --profile web --host <host> --port <port>`；**port 0 = OS 自选**；默认 host `127.0.0.1`（loopback）、默认端口 3080；
- 网关挂 `/api` 前缀；无自动开浏览器（help 无 open；`printUrl` 仅打印，可关）；
- **信任栅栏**（`dsh-client-connection/src/api-request-trust.ts`）：Host 头必须 loopback（或 `--trusted-host` 白名单）；**无 Origin 的非浏览器客户端放行**（「Absent Origin is fine」）；`sec-fetch-site: cross-site` 拒绝；特权方法（credentials/settings 等）钉死 loopback——bridge 从本机 loopback 访问天然全通，且该面永不对外暴露；
- 与用户自己的 3080 实例按端口共存。

### 3.4 API 稳定性 = 无版本协商，随版本整体演进 ⚠️（如实）

- 无版本字段/协商；方法集由 `RpcMethodMap` 编译期锁定（增删方法=类型错误）；契约层零 Node 依赖、可从浏览器 import，RFC 文档化（gui-layering-and-rpc-protocol）——设计上就是多客户端共享面；
- **应对**（同 opencode 先例）：启动时能力探活（`host.describe` + `session.list` 空调用 + `llm.providers`）；失败/字段缺失 → backend `not_configured` + 诊断如实，绝不猜。dsh 升级破坏契约 = 诊断可见，不静默。

### 3.5 关键方法形状（映射直接依据）

- `session.list`：`{cursor?}`（cursor 为保留位未实现→一次全量，bridge 自行分页）→ `items: [{sessionId, updatedAt(ms), running, blank, parentSessionId?, origin?, cwd?, agentPreset?, projections?}]`（标题直出：**`session-title` 投影 unit 实际存在**——评审活体证实 `projections.values.title` 即真实标题（`session-title/src/index.ts:309`），主路径成立；仅当部署未组 session-title 插件时退 `session.history` 尾读 fallback）；
- `session.create`：`{workspaceId? | cwd?, sessionId?, agentPreset?}`——**传 cwd（iOS 选定目录）**；注意 web 的 workspace attach 恰好发生在 HTTP create/fork 流程内（9ac8102 复盘），cwd 命中已注册 workspace 时新会话自动归组——旧路线的「未分组」问题在此路线对命中目录自动解决；
- `session.prompt`：`{sessionId, mode: "queue"|"steer", content:[parts], clientTimeZone?}`；
- `session.cancel`：`{sessionId}`；`session.history`：冷读（不 resume、不发布 Agent）+ `beforeSeq` 向后分页 + 尾页 projections 块；
- `llm.models`/`session.models`：官方模型目录（`list_models` 数据源）。

## 4. 架构

### 4.1 模块与身份

- 新 Go 包位于 **`agent/dsh-web/`**（**owner 指定目录名**，2026-08-16 四轮评审后指令；`agent/dsh` 一行不动，完全物理隔离。Go 目录名可含连字符而包标识符不能——包名 `dshweb`，import 路径 `…/agent/dsh-web`，注册名 `dsh-web`、wire kind `deepseek-web`，功能零影响，仅为本仓首个含连字符的 agent 目录；iOS `BackendKind.deepSeekWeb`，显示「DeepSeek Web」）；Mac App runtime 默认 drivers 增列（c71c692 先例）。
- **实施基线与代码关系（评审 M3）**：实施先在两仓 `dsh/web` 进行，**2026-08-17 已合入 main**（Mac `09d0089`，iOS `627c0e9`）。与旧件的关系：**不 import `agent/dsh` 包**；「复用 codec」的确切含义=把 §3.3 事件映射表（连同其 wire fixture 单测）**复制**进 `agent/dsh-web` 并归属其下——旧件已从产品入口退役，源码保留。
- iOS 改动量（评审 S10，如实）：不止「增一个 case」——`.deepSeek` 在 iOS 非测试代码 **11 个文件**存在穷举 switch（BackendModels、ChatUIKitContainerView×5、ChatViewModel+Generation×2、SelectionSheets、CCCodeBridgeBackendClient、SessionLifecycleDiagnosticPhase、ModelManagementService、ChatViewModel、ChatViewModel+CodexStreaming、ChatViewModel+DirectoryPreferences、ServerViewModel），新增 case 时 Swift 穷举检查**编译期强制**全部暴露，逐处做归组决策（不会静默漏）；行为相关的两处需显式决策：DirectoryPreferences:107（list_projects 通用路径，dsh-web 走 workspace.list 映射）、ChatUIKitContainerView:4335（context entry 显隐与 `get_usage` ⛔ 的联动）。

### 4.2 web 服务生命周期（探测复用优先，managed 兜底）

1. 探测：`POST 127.0.0.1:3080/api/host.describe` 探活（可扩展已知端口列表/用户配置）；命中 → 复用用户自己的实例；
2. 未命中 → managed：spawn `dsh --profile web --host 127.0.0.1 --port <3096..3196 自选>`，端口与**实例来源状态**写 data dir 的 `dsh-web-managed-server.json`（0600，opencode-managed-server.json 先例；dsh v1 无凭据面——trust fence 明示自身非认证层，故无凭据可记，评审 S11）；崩溃重启/sleep-wake 归 RuntimeManager 既有生命周期管理；
   - **双实例策略（评审 S3，如实标注不确定性）**：探测仅在启动时进行——用户此后自启 3080 实例时，managed 实例与用户实例共写同一 store（per-session 目录隔离下与「web+旧 driver 共存」同类；`session-persistence-jsonl` 源码检索未发现跨进程锁——**未做双实例写实验，不确定**，实施期以受控 sandbox（DSH_HOME 临时目录）双实例实验补证；周期重探+让位迁移列为二期）。
3. 两态都失败 → hello_ack 如实「未启动」（初衷：不代装）。

### 4.3 功能面完整映射（对照 bridge 全 RPC 面 × iOS 既有功能）

对照物=bridge wire 全部 RPC（`handlers.go` dispatch）+ iOS 在其他四 backend 上已跑熟的功能。标记：**✅一期映射**（直调官方 API）｜**♻️通用管线**（bridge 既有机制，无新 backend 代码）｜**2️⃣二期**（官方面已核实，一期不接）｜**⛔如实 not_supported**（无官方面，iOS 走既有空态/隐藏入口兜底——fa371a3 惯例，不报错横幅）。

#### 4.3.1 会话列表（iOS 侧栏：目录分组、标题、时间、运行徽标、置顶、下拉刷新）

- `list_sessions` ✅ `session.list`：sessionId→id、`updatedAt`(ms)→modifiedAt、`cwd`→directory（iOS 目录分组与旧件同构）、`running`→运行态 enrich、`origin=subagent`/`parentSessionId` 过滤（与 web 侧栏一致隐藏子代理与 blank 空会话）；官方 cursor 为未实现保留位 → 一次全量 + bridge 既有分页/fair-slice 复用。标题：`projections.values.title` 直出（`session-title` unit，评审活体证实，§3.5）；部署未组该插件时退 `session.history` 尾读。
- `get_session` ✅ 同源（列表缓存或 history 头信息）。
- 置顶 `set_session_pinned`/`list_pinned_sessions` ♻️ bridge 自有 pin 索引（AgentSessionInfo 解析依赖 ListSessions，已具备）。
- 下拉刷新 ✅ list 重拉 + 投影 forceCold 重建（既有机制）。
- **外部变更自动同步（Mac 端 dsh web 新建会话/外部 turn → iOS 列表及时更新，双层）**：
  1. **即时层（事件驱动，新接线）**：bridge 常驻第二条 WebSocket 流 `GET /api/events.host`（§3.2）——`host/session-added`/`session-removed`/`workspace-changed` 帧到达 → 立即触发该 backend 一次 `session.list` 重扫 → catalog 指纹 diff → 既有 `sessions_changed` 控制面广播（`event_publisher.go:773`，backend 级）→ iOS 自动刷新列表；`host/session-status{running}` → 运行徽标实时翻转（外部 turn 开始/结束）。
  2. **兜底层（零新代码）**：bridge 既有逐 backend session discovery watcher（`session_discovery.go:50`，周期 ListSessions→指纹 diff→`sessions_changed`）对 dsh-web 自动生效——host 流断线期间由它保底。
  - 现有会话新增 turn：mux `session/event` 全会话覆盖（4.3.3）——已打开的消息页实时收流；未打开会话的徽标/时间由 `session-status` + `sessions_changed` 刷新，打开时经 history/投影补齐全程。

#### 4.3.2 消息页加载（历史渲染：思考流、工具卡片、分页）

- `get_session_messages` ✅ `session.history`：`beforeSeq`↔cursor、`maxMessages`↔limit（官方按 append-origin 消息边界分页）；映射为既有 rich-history 形状（role/content/thinking/parts/steps）——官方 history 自带 `ToolEventView` 渲染意图与整日志统计，工具步骤信息比旧件直读 store 更富。
- `get_session_projection`（SSV2 消息页）✅+♻️：live 事件喂 kernel（见 4.3.3）；冷会话 pathless hydrate 的 `RichHistoryProvider` 数据源 = `session.history`。**投影语义零新增，但 backend-id 键控接线点需逐一加入（评审 M4——漏任一处对应机制静默失效）**，且归属 **opencode/grokbuild 的 pathless 家族**，**不进** deepseek 的 store-file 分支（dsh-web 无「store 未落盘窗口」语义，亦不需要 live-only admission 回退——会话在服务端常驻，mux 事件即时到达）：

  | 接线点 | 处置 |
  |---|---|
  | `backendSupportsProjectionHydrate`（handlers_projection.go:337） | 加入 `deepseek-web`（pathless 家族） |
  | `prepareProjectionHydrateSource` pathless 分支（:534 起，条件在 :629） | 加入（agentName=`dsh-web`） |
  | `produceProjectionHydrateRange` guard+case（:1003/:1059） | 加入 |
  | 两处 forceCold 集合（handleGetSessionProjection ~:111 + `ensureProjectionHydrated` ~:418 起 sourceChanged 判定） | 加入 |
  | deepseek 专属 store-file/live-only 分支（:432/:548） | **不进** |
  | `backendHasNoExternalEventSource` 剪枝（handlers.go:1039） | **不进**（mux 即外部事件源） |
  | `agent_descriptor.go:216`/`main.go:131`/`server.go:282` | 注册与 SSV2 能力广告 |

- **`SessionActivityProbing`（评审 M1，坑 4 后半闭环）**：dshweb 实现该接口——`IsSessionActive` 数据源=running 徽标（`session.list` 探活缓存 + `host/session-status` 帧实时更新）；**错误/未知 ⇒ 保守返回 active**（封口方向宁可等待）。不实现则死会话（尾部未答 turn）冷开永远 loading（`handlers_projection.go:1232-1238` commit gate 语义）。
- `read_file_v2`/`fetch_content_chunk` ♻️ bridge 通用 Mac 文件读取（与 backend 无关）。

#### 4.3.3 流式同步与运行态（isGenerating、text/思考增量、停止按钮、完成通知）

- **两条常驻 WebSocket 流**（§3.2）：`GET /api/events.mux`（会话事件）+ `GET /api/events.host`（会话生命周期，4.3.1 即时层）。mux 的 `session/event` 帧（SessionEvent 与磁盘日志同构）→ **复用 codec §3.3 映射（复制进 dshweb，§4.1）** → core.Event → 既有推送管线（relay/直连）→ iOS 既有渲染链，零新渲染逻辑。
- 运行态：turn 事件推导 + `host/session-status{running}` → `session_state_changed` 广播（既有）。
- **外部 turn 可见性（对旧 dsh 是新能力）**：mux 覆盖全部会话——用户在 Mac web 发起的 turn，iOS 同样收事件/投影与完成通知（对齐 claudecode 外部旁观）；dsh-web 不进 `backendHasNoExternalEventSource` 剪枝名单，observation/离线 mailbox ♻️ 全部照常。
- 断线：WS 重连=重开流+重拉 history（官方 v1 语义照搬）；断线窗口由重连后 history 重拉+投影 forceCold 补齐（§6 有专项测试）。
- 红线（坑 7/8）：`turn/end reason=error` 必须透传官方错误文本为终态事件——失败路径必现可见终态。

#### 4.3.4 发送消息

- 新会话 ✅ `session.create{cwd=iOS 选定目录}`（cwd 命中已注册 workspace 自动归组，§3.5）→ `session.prompt{mode:"queue", content:[text]}`。
- 既有会话 ✅ `session.prompt` 直接续（官方 resume，§3.1）——iOS「会话已结束」文案路径对 dsh-web 不适用，无守卫、无 `session_resume_not_supported`。
- steer 插话转向 2️⃣ `mode:"steer"`（一期 queue）。
- 附件/图片 2️⃣：官方 `session.attachment` + `imageLimits` 投影已核实；一期 descriptor `StaticCapabilities` 声明 text-only（既有两级附件校验照走）。
- `abort_generation` ✅ `session.cancel{sessionId}`。
- **审批/问答 = 一期必接（评审 M2 升格，非决定项）**：活体证实默认 `approval/policy="ask"`——不接则 iOS 发起的 turn 在首个需审批工具处**无限挂起**（无终态无错误），直接违反 fail-visibly 红线（坑 8 类别）；旧路线靠自组 cordis.yml 权限栈规避，本路线会话由官方 preset 组装，无此规避。映射：`approval/requested` → 既有 permission 请求事件（iOS 权限 UI 已有）；`question/requested`（含 options/multiSelect/plan-review intent）→ **既有 question 事件面**（`question_reply`/`question_reject` 承接；`resolve_user_input` 不用于 dsh-web 一期）；应答统一 `POST /api/respond` 回显帧信封 rpcId（approvals.ts:1-21 二轮加证）。**先答者得**语义如实：web 端若先应答，bridge 收 `approval/resolved` 帧关闭请求（resolved 帧广播全部 mux 消费者）。
- **审批选项与 wire 折叠事实（评审 R2-2 设计，三轮 R3-2 按源码修正）**：dsh 的 approval outcome 是二值集（仅 `allowed-once`|`rejected`，无 always 面）。**修正**：wire 的 permission_request 载荷并无选项声明字段（events.go:156-161 仅 requestId/toolName/toolInput/toolInputRaw），iOS 选项也是硬编码（CCCodeBridgeBackendClient.swift:1390-1395）——v3 的「按事件声明渲染」机制不存在，删除。**真实机制（三轮源码核验的好消息）**：iOS 的 always 变体在 wire 层本就折叠为二值（approveAlways→`behavior:"allow"`、rejectAlways→`"deny"`，CCCodeBridgeBackendClient.swift:787-802）——dshweb 映射 allow→allowed-once / deny→rejected 即正确工作，无语义反转；残余仅「Always Approve」**标签语义弱化**（按下后无持久化面，下次仍会问）——接受为如实降级。「隐藏 always 变体」列为 iOS 可选优化（若一期做，须把 CCCodeBridgeBackendClient.swift:1390 的选项构造补进 §4.1 归组决策清单——当前 11 文件穷举清单不含该行为点）。
- **问答整批作答语义（评审 R2-1 设计，三轮 R3-1 按源码修正）**：dsh 一次 ask 多题**整批作答**（questions.ts「one ask, many questions, one answer — never split」），而 bridge `question_reply` 是单题模型。聚合点在 dshweb，**id 键控按源码事实**：
  - **questionId = dsh 每题自带唯一 id**（events.schema.ts:21），一批 N 题 → N 个 question 事件各持自己的题 id——iOS question 存储按 id 替换式 upsert（同 id 替换、异 id 追加，ChatViewModel+CodexStreaming.swift:768/1826-1832），逐题 id 才能全部可见可答（v3 曾写「共享帧 rpcId」= 会被覆盖只剩最后一题，已废）；
  - 帧 rpcId 仅作 dshweb 内部**批 key**，不上 wire；逐题累积应答，**收齐整批后按题 id 组装一次 `POST /api/respond`**（应答形状 `{answers:[{id,selected,custom?}]}` 按题 id 键控，user-questions/types.ts:53-66）；
  - **中间态如实**：iOS 提交 RPC 成功不置 completed，状态由 `question_resolved` 事件驱动（ChatUIKitContainerView.swift:3443-3455）——dshweb **不合成**逐题 resolved（批未提交前合成=虚假乐观；若他题 reject 整批取消则先前「已提交」是谎言），各题保持 pending 直至 host 批 `question/resolved` 帧到达后统一映射；
  - **reject 不对称（防坑）**：question 的取消走 respond 的 **error 分支**（`ok:false, code:"cancelled"`），不是 value 载荷——与 approval（value outcome）不对称，实施时勿照抄；任一题 `question_reject` → 整批以 error-cancelled 应答。
  - **批 resolved 的展开机制（评审 S-1）**：host 批 `question/resolved` 帧载荷仅 `{sessionId, questionRpcId, outcome}`（events.schema.ts:52），**无逐题内容**——「映射为逐题 resolved」实为 dshweb 从批状态查 rpcId→题 id 集合**展开为 N 个** iOS `question_resolved{questionId, result}` 事件（勿预期帧内有逐题数据）。
  - **断线边界如实标注（评审 S-2）**：mux 重开会重放 still-pending 的 question/approval 帧（同 rpcId，官方 refresh-recovery）——重连后批状态可重建、iOS 幂等重收；但**断线窗口内已被 web 端答掉的批不重放**，iOS 该批 pending step 在 live 视图内不收口，自愈=会话重开冷加载（ask 的工具结果入 durable 日志，history 重建终态）——与既有 backend 的 transient question 同类，非永久挂起。
  - **重复提交幂等（评审 S-3）**：批 pending 期间 iOS 可对同一题重复提交——dshweb 批状态按题 id **覆盖式**累积，后答覆盖前答，无重复计数。
- **外部会话的审批帧路由（评审 S4；判据统一按 R2-3）**：mux 为 agent 级持有，外部（web 发起）会话的 approval/question 帧同样到达；surface 判据=**bridge registry 命中**（`h.getSession()` 有该会话对象——即 iOS 打开过该会话；observation 订阅而无 registry 对象**不**构成判据，两者是不同集合）；未命中的帧不进权限面（web 自己的 UI 在应答）——与「谁在看谁应答」的产品语义一致，避免为全量外部会话注册代理对象。

#### 4.3.5 provider / model（iOS 模型选择器、provider 切换）

- `list_providers` ✅ `llm.providers{}` → 用户 dsh 已配置的 provider 全集（如 opencode-go/zai——复用用户已配，初衷）。**过滤规则（评审 S1）**：活体证实响应混有大量休眠项（`active:false, declared:false`，如 amazon-bedrock/anthropic 等）——映射仅取 `active:true` 进 `list_providers`；全量与状态位进 run_diagnostics 输出。
- `list_models` ✅ `llm.models{}`（provider 分组目录）+ `session.models{sessionId}`（当前会话 effective 选择 + 可用集）。
- `switch_model` / backend 级 `set_provider` ✅ `session.selectModel{sessionId, provider, model, reasoningEffort?}`——官方语义=会话内立即生效并持久化为部署默认；无 backend 级全局写面，如实按此语义映射。
- **模型目录/白名单永远来自运行时**（坑 3 红线，不再手写复本）。
- `list_permission_modes`/`set_permission_mode` ⛔ 一期（评审 S13 修正理由）：**面存在但不一一对应**——settings 域有 `permission` ns、`permissions` 投影 unit 存在（loopback 特权方法，bridge 恰可调），但其语义（web 偏好/preset 组合）与 bridge 的 permission-mode 开关不同构；一期不接，二期评估 settings/preset 面的语义映射（`agentPreset.*` API 已存在，前缀以 rpc-map.ts:54-59 为准）。

#### 4.3.6 会话生命周期（增删改）

- 新建 ✅（4.3.4）；`resume_session`（重开）✅ = history+订阅（无进程语义）。
- `rename_session` ✅ `session.rename{sessionId, title}`（官方规范化后回 accepted title）。
- `delete_session` ⛔ **官方无 delete API** → 一期如实 not_supported（`workspace.archiveSession` 是归档非删除，语义不同；是否映射二期与 owner 定）；iOS 删除入口对该 backend 禁用。
- `archive_session` 2️⃣ `workspace.archiveSession{sessionId}`（归档集语义）。
- `share_session` ⛔（bridge 已通用 not_supported）；`compress_context` ⛔（compaction 属 preset 体系，无 RPC 面）。

#### 4.3.7 目录与环境（iOS 目录选择器、项目建议）

- `list_directory` ✅ `host.listDirectory{path?}`（官方目录浏览面，iOS 目录选择器数据源）；`host.createDirectory` 2️⃣。
- `list_projects` ✅ `workspace.list`（已注册 workspace=快速目录建议）；空则本地目录服务兜底（旧件同款）。
- git 面（`get_git_context`/PR 三件套/commit_and_push/branch/worktree）⛔ 无对应面，iOS 入口隐藏（与其他无 git 面 backend 同）。

#### 4.3.8 其余面

- `run_diagnostics` ✅ 探测结构化输出（实例来源 external/managed、端口、`llm.providers` 全量与状态位）。**版本字段语义（评审 S6）**：`host.describe` 的 `version:"0.0.1"` 是占位符非 npm 版本（活体证实）——诊断呈现标注为「API 版本标识」，npm 包版本另由探测（`dsh --version`/安装树）获取，不冒充。
- `list_memory_files`/`read_memory_file` ⛔（评审 S5 补行）：`MemoryFileReader` 类型断言自动 not_supported（handlers.go:1766-1771），无新代码；`cancel_request_v1` ♻️ 连接级通用面（handlers.go:4308-4310），backend 无关。
- `fetch_todos` ⛔ 一期（web 的 plan 属会话投影无 todo RPC；二期评估）；`get_usage` ⛔ 一期（token 计量在 context-meter 投影单元，二期按 `session/projection` 帧核对后决定接入或维持）；diff 三件套 ⛔。
- `list_agents` ✅ 2026-08-17：官方 `agentPreset.list`（标准/PTC/极简/创造）。iPhone 空白页可选，开聊后锁定。创建走 `session.create.agentPreset`。创作面（copy/remove）仍不做。见 [2026-08-17-dsh-web-agent-preset-picker.md](2026-08-17-dsh-web-agent-preset-picker.md)。
- `session.search` 2️⃣（评审 S9 消歧）：iOS 搜索现为本地实现已够，官方服务端搜索二期接入（面已核实存在）。
- `check_pending_notifications`/prekey/delivery ♻️ bridge/relay 通用，与 backend 无关。

### 4.4 安全模型

仅 loopback 访问（trust fence 无 Origin 放行）；`deepseek-web` 的 iOS 流量永远只走 Bridge（8777/relay，配对+加密）；managed 端口永不对外；特权方法由 fence 钉死 loopback——**禁止**任何 `--host 0.0.0.0`/`--trusted-host` 托管配置。**如实声明（评审 S11）**：dsh v1 服务本身无认证（trust fence 非 auth 层），managed 实例与本机其他进程可达的风险面与用户自启的 3080 实例同类——loopback-only 绑定 + Bridge 前置是全部防线，与 opencode managed（自生成 Basic Auth）的差异在诊断中如实呈现。

### 4.5 SSV2 真相清单（iOS CLAUDE.md 专项计划要求项，评审 S12）

- **真相 owner**：dsh web 服务（官方 host 上下文）——会话日志、投影、模型选择的唯一权威；MacBridge kernel 只持有本 epoch 事件镜像与投影缓存（checkpoint 可丢弃重建）。
- **唯一 writer**：dsh web 服务写 `~/.dsh/sessions` 与 workspace.json；MacBridge 经 dsh-web backend **零直接写**（prompt/selectModel/respond 走官方 API）。
- **受影响事务域**：kernel hydrate/live 事务域（既有，pathless 家族）；无新事务域。
- **新增数据路径**：仅 `dsh-web-managed-server.json`（端口/实例来源，data dir，0600）；无新会话数据路径。
- **active 下全部写入口**：`session.create/prompt/cancel/rename/selectModel` + `/api/respond`（审批问答）——全部经官方 API；无旁路。
- **失败呈现方式**：探活失败→backend not_configured+诊断；turn 失败→终态事件透传官方 RpcError 文本；审批超时→无超时裁判（iOS 不裁判，SSV2 护栏），等待用户或 web 端先答。
- **防双写测试**：单测断言 dshweb 包不含任何 store/workspace 文件写路径（go vet/代码审查项）+ 双实例受控实验（§4.2）。

## 5. 与旧 `deepSeek` backend 并存

同一份 `~/.dsh/sessions`；各自独立列表；跨 backend 打开行为差异如实：dsh-web 可 resume 旧件建的会话，旧件对 dsh-web 建的会话仍走「已结束」守卫；**旧件早期（模型修复前）会话记录的是 `default/deepseek-chat`**（评审 S2 活体证实）——dsh-web 续聊这些会话会在模型校验层得到**可见错误**（官方报错文本透传），修复路径=对该会话 `switch_model` 选合法模型或另起新会话，属历史数据残留而非本设计缺陷，真机矩阵加一行覆盖。附件一期 text-only 声明（与旧件一致；官方 `session.attachment`/`imageLimits` 面已核实存在，二期按 owner 需求接入）。

## 6. 测试与验收

- 单测：httptest 假 dsh 服务（**含 WebSocket 端点**；schema 样本 fixtures：ClientRequest/ServerRequest 信封 + list/history/prompt/models/providers 响应 + mux/host 帧序列）覆盖 §4.3 功能面总表**逐行**（含 ⛔ 项的 not_supported 断言）；生命周期（探测命中/managed spawn/双失败 not_configured）；探活失败诊断文案；
- 专项（评审补充）：**RpcError message 全文透传断言**（坑 7 测试面——构造 bad-request/业务错误帧，断言 iOS 侧拿到原始文本）；**WS 断线重连**（重开流+history 重拉+投影 forceCold 补齐）；**SessionActivityProbing 三态**（死会话尾封口/活会话不封口/探活失败保守 active）；**审批链路**（approval 帧→permission 事件→/api/respond 回显 rpcId→resolved 收口；外部会话不 surface；web 先答的先答者得）；双实例受控实验（sandbox DSH_HOME，§4.2）；providers `active:true` 过滤；
- 回归：bridge 全量 + 旧 deepSeek 套件零改动验证；
- iOS：`BackendKind` case 单测 + 定向 CCCodeTests；
- 真机验收（核心新增行）：**iOS 对 Mac web 创建的任意历史会话发消息→真实续聊（双向：web 续 iOS 建的会话）**；**旧件早期（模型修复前）会话续聊 → 官方模型校验错误可见透传 → switch_model 修复后可续**（S2 行，评审 R2-5 落表）；**审批链路**（ask 工具 → iPhone 权限弹窗 → 应答后 turn 继续；web 端先答则 iPhone 收 resolved 收口）；Mac web 新建会话/外部 turn → iOS 列表自动同步（无手动刷新）；列表/历史/新建/流式/停止对齐旧件既有验收行。

## 7. 边界与退役

- 硬边界：不 re-pin、不改 vendor（旧 backend 冻结依赖）；不写用户 `workspace.json`（归组由 web 自身 create/fork 收编）；`since` 续传 v1 未实现照官方语义重开流。
- **Owner 兜底许可（2026-08-16 指令）**：若实施中遇到官方 API 实现不了的功能点，可**复制**（不 import）`agent/dsh` 的已有成果（store.go/zstd_reader.go 等只读解析）作兜底。红线不变：**只读**——绝不写 `~/.dsh/sessions` 与 workspace.json（坑 2 教训：写路径才有编码冲突风险，读侧双后缀本就兼容）。现状核查（四轮评审功能对照表）：一期全部 ✅ 项均有 API 路径，⛔ 项（delete/git/diff/todos/usage）官方无面且旧件同样没有——此许可为兜底阀门而非既定需求。
- 旧 `deepSeek` 退役：2026-08-17 产品入口已去掉；iOS 后端切换只留 DeepSeek Harness。旧模式下建的会话仍可在 Harness 打开。

## 8. 实施拆分（已完成）

exec-plan 状态：`.exec-plan/state/plan-a46e4391b790.json`。§8-1…§8-8 及后续 review-fix 均 done；owner 真机矩阵（流式、双向旁观、审批/问答、工作区归组、长会话投影、预设芯片）2026-08-16～18 回报通过。

1. `agent/dsh-web` HTTP+WebSocket 客户端（三层信封）+ 探活 + 生命周期（探测/managed/json）；
2. 映射表逐行（list/**create**/history/prompt/cancel/**rename**/models/providers/selectModel）+ 单测；
3. **WS mux/host 双流管线**（codec 映射复制 + SessionActivityProbing + sessions_changed 即时层）；
4. **审批问答链路**（帧→事件→/api/respond；外部会话路由策略）；
5. 投影接线点清单逐项（§4.3.2 表）+ pathless 家族回归；
6. **list_directory/list_projects**（host.listDirectory/workspace.list）；
7. wire descriptor/canonical 协议 pack + iOS `BackendKind` case 与 ~11 处穷举归组 + mirror；
8. Mac App drivers 默认列 + Release 安装 + 真机验收矩阵。

## 9. 评审采纳记录（v2 对照 `docs/2026-08-16-dsh-web-backend-design-review.md`）

| 项 | 处置 | 落点 |
|---|---|---|
| B1 载波=WebSocket 非 SSE | **采纳** | §3.2 重写（WS+三层信封+426 行为+更正溯源）；§4.3.1/§4.3.3/§6/§8 同步改 WS |
| M1 SessionActivityProbing 缺失 | **采纳** | §4.3.2（running 徽标数据源+保守 active）+ §6 三态测试 |
| M2 审批分期与 fail-visibly 冲突 | **采纳**（升格一期必接） | §4.3.4（含 S4 外部会话路由+先答者得+question→question_reply 选型） |
| M3 复用 vs 不共享矛盾/分支依赖 | **部分采纳** | 矛盾修复+「复用=复制映射表进 dshweb」采纳（§4.1）；**「实施前置=dsh/driver 合回 main」不采纳**——理由：owner 已裁决 merge 时机另行决定（收口指令「无需 merge」），实施在 dsh/driver 分支进行，两包同树并存即满足复制来源，合 main 不构成技术前置 |
| M4 「投影零新增」不成立 | **采纳** | §4.3.2 接线点清单表（pathless 家族归属+deepseek 分支不进+剪枝不进）+ §8 拆分 5 |
| S1 providers 休眠项过滤 | 采纳 | §4.3.5（active:true 进列表，全量进诊断） |
| S2 旧件早期会话模型名残留 | 采纳 | §5 差异清单+真机矩阵行 |
| S3 双实例共存策略 | 采纳 | §4.2（启动时探测+不确定性如实+sandbox 受控实验）+ §6 |
| S4 外部会话审批路由/应答面选型 | 采纳 | §4.3.4（并入 M2 段落） |
| S5 总表补三行 | 采纳 | §4.3.8 |
| S6 host.describe 版本占位符 | 采纳 | §4.3.8 |
| S7 RpcError 透传断言+重连测试 | 采纳 | §6 专项 |
| S8 拆分与 ✅ 集对齐 | 采纳 | §8 重排为 8 项 |
| S9 search 标记歧义 | 采纳 | §4.3.8 改 2️⃣ |
| S10 iOS 改动量低估 | 采纳 | §4.1（11 文件清单+两处显式决策点） |
| S11 managed json「凭据」措辞 | 采纳 | §4.2/§4.4（无凭据面+暴露面如实） |
| S12 SSV2 真相清单 | 采纳 | §4.5 成段 |
| S13 permission modes ⛔ 理由过强 | 采纳 | §4.3.5（「有面但语义不对应，二期评估」） |
| S14 agentPreset 前缀笔误 | 采纳 | §4.3.5 |

**坑 4 闭环状态更新**：评审判「未闭环」→ v2 经 §4.3.2 SessionActivityProbing 设计收口；坑 7/8 测试面经 §6 专项补齐。

## 10. 二轮评审采纳记录（v3 对照 `docs/2026-08-16-dsh-web-backend-design-review-r2.md`）

二轮结论 **APPROVE**；B1/M1-M4/S1-S14 处置全部经独立复核有效（含 M3 部分不采纳理由核验成立）。6 条建议级尾项**全采纳**：

| 项 | 处置 | 落点 |
|---|---|---|
| R2-1 问答整批作答语义 | 采纳 | §4.3.4（dshweb 批状态聚合 + iOS 中间态如实 + reject→整批 cancel） |
| R2-2 approval outcome 二值集 | 采纳 | §4.3.4（「始终允许」类选项按声明隐藏） |
| R2-3 surface 判据统一 | 采纳 | §4.3.4（registry 命中为唯一判据；observation 订阅≠判据） |
| R2-4 agentPresets 残留前缀 | 采纳 | §4.3.8 更正 |
| R2-5 S2 真机矩阵行落 §6 | 采纳 | §6 验收行 |
| R2-6 接线点表 :468 归属 | 采纳 | §4.3.2 表（sourceChanged 判定归 `ensureProjectionHydrated` ~:418；pathless 条件在 :629） |

三项遗留评审项终态（二轮判定）：坑 4 后半 **闭环**（M1+三态测试）；坑 7 **闭环**（透传断言专项）；坑 8 **闭环**（审批升格一期必接消除挂起路径）。

## 11. 三轮评审采纳记录（v3.1 对照 `docs/2026-08-16-dsh-web-backend-design-review-r3.md`）

三轮结论：修改后通过（R3-1/R3-2 必改）；四条纯文字修订（R2-3/4/5/6）复核合格；M3 部分不采纳理由复核维持。两条必改**全采纳**——均为 v3 落实二轮建议时**未查源码臆造机制**（§2.3 纪律 1 在修订动作里复发，已在纪律条款中显式封堵）：

| 项 | 处置 | 要点 |
|---|---|---|
| R3-1 「N 题共享 rpcId」被 iOS 替换式存储覆盖 | 采纳 | questionId=每题自带 id（iOS 异 id 追加才全可见）；帧 rpcId 仅作 dshweb 内部批 key；收齐按题 id 组装一次 respond；中间态不合成 resolved（保持 pending 至 host 批 resolved 帧）；reject 走 error 分支（与 approval 不对称） |
| R3-2 「按事件声明渲染」机制臆造 | 采纳 | 删除该机制；如实写 wire 折叠事实（always→allow/deny 二值，映射即正确、无语义反转）；残余=标签语义弱化，接受；隐藏变体列为 iOS 可选优化（含 §4.1 补充决策点提示） |

§4.3.4 全段源码对照自查（评审建议）：其余断言（create/prompt queue/resume 语义、cancel、approval outcome 集、/api/respond rpcId 回显、resolved 帧全员广播先答者得、registry 判据）均有三轮内源码/活体证据在案，未再发现失实项。三项遗留评审项闭环判定维持（R3-1 修正消除了坑 8 的新击穿路径）。

## 12. 四轮评审采纳记录（v3.2 对照 `docs/2026-08-16-dsh-web-backend-design-review-r4.md`）

四轮结论 **APPROVE（可交付 owner 终审）**；R3-1/R3-2 修正逐点核验合格，纪律封堵与 §4.3.4 自查复核通过。本轮 3 条建议级收尾**全采纳**（§4.3.4 批作答段落补三条）：S-1 批 resolved 展开机制（帧无逐题数据，dshweb 按批状态展开 N 个）；S-2 断线边界如实（重放恢复成立 + web 已答批不重放、冷开自愈）；S-3 重复提交按题 id 覆盖幂等。

**Owner 三项指令落稿（同 commit）**：① iOS 改动量核验结论入 §4.1（纯枚举增量约 30-60 行 + 测试，权限/问答 UI 零改——两处本可能动 iOS 的点已在 wire 折叠与批聚合设计里消掉）；② 新建 `agent/dsh-web/` 目录（owner 指定），`agent/dsh` 一行不动、完全物理隔离，包名 `dshweb`/import `…/agent/dsh-web` 的连字符注记；③ §7 记录 API 盲区兜底许可（复制不 import 旧件只读成果；红线=绝不写 store/workspace.json；现状核查一期用不上，为兜底阀门）。

**四轮累计账目**：1 阻断（B1）+ 6 必改（M1-M4、R3-1/2）+ 23 条建议（S1-S14、R2-1…6、S-1…3）全部处置；owner 已裁决事项（SDK 路线暂停保留）四轮未重开，后已合 main。实施期挂账后续：双实例 sandbox 在 §8-1 测试已做；上下文占用/StatsLine 已按官方 `contextPressure`/`sessionStats`/`tokenUsage` 接到 iOS ⭕（超出原稿 `get_usage` ⛔ 的一期范围，属产品增量）。
