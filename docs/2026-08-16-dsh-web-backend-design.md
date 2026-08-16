# dsh-web Backend 设计（官方 Web API 转发 + bridge-v1 翻译）

- 日期：2026-08-16
- 状态：**设计稿，待 owner 过目**（批准后才实施）
- 背景：SDK stdio 路线收口暂停（`docs/2026-08-16-dsh-session-store-bridge-design完成情况.md` 收口节）；owner 裁决并行新增本 backend，旧 `deepSeek` 保留不删。
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
| 4 | **投影基线优先级 + 尾封口**（真机二轮）：活会话被误推 file 重建与 live 流竞争 → `projection.hydrating` 循环；失败 turn（只有用户消息无回复）的尾部未答 turn 永不封口 → 基线永不提交 | 正确矩阵：live/kernel 会话一律实时基线，file 重建只服务死会话；死会话尾部未答 turn 需 `SessionActivityProbing` 如实封口 | **部分**——此坑在 SSV2 通用管线层（本设计 §4.3 事件管线沿用），评审时需确认映射后事件含终态（官方 turn/end reason=error 必须透传为终态事件） |
| 5 | **「未分组」臆想**：未读 web 源码即断言「Chat 未注册 workspace，用户加上即可」，被双端截图纠正 | web 分组 = workspace.json 里每 workspace 显式记录的 sessionIds 名单（受两写恢复协议管理，**外部进程不可盲写**）；attach 只发生在 web 自己的 create/fork HTTP 流程 | **是**（本路线走 HTTP create，cwd 命中已注册 workspace 自动归组；且不写 workspace.json） |
| 6 | **materialize 防覆写**：对已存在 id 发 prompt，持久化拒绝重复物化（"refusing to materialize: a log already exists"） | SDK 面无 resume，死会话续聊只能诚实拒绝（`session_resume_not_supported`） | **是**（官方 HTTP 面有真 resume，见 §3.1） |
| 7 | **codec 折叠错误文本**：`turn/end reason=error` 被映射为泛化文案，底层错误（编码/模型）在日志不可见 | 排障时用裸 JSON-RPC 探针直连 runtime 取原始错误全文（`DSH_TURN_REPRO` 测试保留此手法） | 评审项：新适配器必须把官方 `RpcError` 的 message 透传到诊断/事件，不得折叠 |
| 8 | **iOS 失败 turn 卡「执行中」**：投影拉取循环 hydrating 时 iOS 消息页无终态可渲染 | 投影失败循环 + 事件终态缺一即卡；iOS 侧对 send RPC 错误有气泡文案兜底 | 评审项：映射须保证任何失败路径都产生终态事件或 send 错误（fail visibly） |
| 9 | **store 格式细节**（供任何直读 store 的评审参考）：projectKey 有损编码（UTF-16 码单元 `~XXXX`、分隔符运行合并、`--≤251>--` 包裹，真实 cwd 以头行为准）；子会话 `origin=subagent`/`delegationDepth>0` 应从列表过滤；标题=日志内 `session/title` 事件折叠+首条人类消息 fallback | 已在旧路线 store.go 落地并测试（projectKey 向量对照 TS） | 本路线不直读 store（`session.list`/`session.history` 供给），此项仅作旧件维护参考 |

### 2.3 过程纪律（从上述坑中提炼，评审员可用同尺检验本设计）

1. 对外部系统的任何行为断言，必须先读其源码/数据——「听起来合理的机制解释」≠ 证据（坑 5）；
2. 否定性断言（「X 不可能」）必须标注在哪几个层面找过（坑 1）；
3. 事实（模型目录、编码、端口、能力）取自运行时自己的声明面，不手写复本（坑 2/3）；
4. 失败路径必须产生可见终态——禁止静默等待（坑 4/7/8）。

## 3. 四项前置核实结论（源码 pin 47f9438 + 本机实测）

### 3.1 prompt 对既有会话 = 官方 resume ✅

`packages/host/apiproxy/src/api-proxy.ts:1653-1670`：`session.prompt` 对已存在会话走 `ctx.agents.resume({resumeSessionId, …})`（磁盘冷恢复，会话记录的 preset/模型选择随 resume 恢复），未知 id 走 `agents.create`。owner 已在 web 实证「对任意历史 session 发消息」。**模型选择三级解析**（进程内 > 会话日志 `request/header` > `agent-default-model`）——历史会话用它自己记录的模型，上轮「deepseek-chat 越界」类故障在这条路线上结构性不可能。

### 3.2 事件流契约 ✅

载波（`dsh-host-apiproxy` README + `dsh-client-connection/src/api-path.ts`）：
- unary：`POST /api/<method>`，`application/json`（否则 415）；业务错误恒 HTTP 200 + `RpcResult` error 分支（封闭错误码集 `RpcErrorDetailsMap`）；
- 流：`GET /api/events.mux`（SSE）与 `GET /api/events.host`（SSE）；服务端反向请求（审批/问题应答）`POST /api/respond`；
- 重连：`since` v1 未实现——重连=重开流+重拉 history（bridge 侧照做即可）。

MuxFrame（`api/events.schema.ts`，SSE 帧判别联合）：
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

- `session.list`：`{cursor?}`（cursor 为保留位未实现→一次全量，bridge 自行分页）→ `items: [{sessionId, updatedAt(ms), running, blank, parentSessionId?, origin?, cwd?, agentPreset?, projections?}]`（标题等元数据在 projections 块，实现时按 `sessionProjectionsBlockSchema` 落定具体 unit；无标题 unit 则退 `session.history` 尾读）；
- `session.create`：`{workspaceId? | cwd?, sessionId?, agentPreset?}`——**传 cwd（iOS 选定目录）**；注意 web 的 workspace attach 恰好发生在 HTTP create/fork 流程内（9ac8102 复盘），cwd 命中已注册 workspace 时新会话自动归组——旧路线的「未分组」问题在此路线对命中目录自动解决；
- `session.prompt`：`{sessionId, mode: "queue"|"steer", content:[parts], clientTimeZone?}`；
- `session.cancel`：`{sessionId}`；`session.history`：冷读（不 resume、不发布 Agent）+ `beforeSeq` 向后分页 + 尾页 projections 块；
- `llm.models`/`session.models`：官方模型目录（`list_models` 数据源）。

## 4. 架构

### 4.1 模块与身份

- 新 Go 包 `agent/dshweb`（注册名 `dsh-web`；wire kind `deepseek-web`；iOS `BackendKind.deepSeekWeb`，显示「DeepSeek Web」）；Mac App runtime 默认 drivers 增列（c71c692 先例）；iOS 仅增 case + 列表项。
- 不与旧 `agent/dsh` 共享代码；旧 backend 一行不动。

### 4.2 web 服务生命周期（探测复用优先，managed 兜底）

1. 探测：`POST 127.0.0.1:3080/api/host.describe` 探活（可扩展已知端口列表/用户配置）；命中 → 复用用户自己的实例；
2. 未命中 → managed：spawn `dsh --profile web --host 127.0.0.1 --port <3096..3196 自选>`，端口与凭据状态写 data dir 的 `dsh-web-managed-server.json`（0600，opencode-managed-server.json 先例）；崩溃重启/sleep-wake 归 RuntimeManager 既有生命周期管理；
3. 两态都失败 → hello_ack 如实「未启动」（初衷：不代装）。

### 4.3 功能面完整映射（对照 bridge 全 RPC 面 × iOS 既有功能）

对照物=bridge wire 全部 RPC（`handlers.go` dispatch）+ iOS 在其他四 backend 上已跑熟的功能。标记：**✅一期映射**（直调官方 API）｜**♻️通用管线**（bridge 既有机制，无新 backend 代码）｜**2️⃣二期**（官方面已核实，一期不接）｜**⛔如实 not_supported**（无官方面，iOS 走既有空态/隐藏入口兜底——fa371a3 惯例，不报错横幅）。

#### 4.3.1 会话列表（iOS 侧栏：目录分组、标题、时间、运行徽标、置顶、下拉刷新）

- `list_sessions` ✅ `session.list`：sessionId→id、`updatedAt`(ms)→modifiedAt、`cwd`→directory（iOS 目录分组与旧件同构）、`running`→运行态 enrich、`origin=subagent`/`parentSessionId` 过滤（与 web 侧栏一致隐藏子代理与 blank 空会话）；官方 cursor 为未实现保留位 → 一次全量 + bridge 既有分页/fair-slice 复用。标题：projections 块（`sessionListMetadata`）优先，无标题 unit 则 `session.history` 尾读一次（实现时按 §3.5 落定）。
- `get_session` ✅ 同源（列表缓存或 history 头信息）。
- 置顶 `set_session_pinned`/`list_pinned_sessions` ♻️ bridge 自有 pin 索引（AgentSessionInfo 解析依赖 ListSessions，已具备）。
- 下拉刷新 ✅ list 重拉 + 投影 forceCold 重建（既有机制）。
- **外部变更自动同步（Mac 端 dsh web 新建会话/外部 turn → iOS 列表及时更新，双层）**：
  1. **即时层（事件驱动，新接线）**：bridge 常驻第二条 SSE `GET /api/events.host`——`host/session-added`/`session-removed`/`workspace-changed` 帧到达 → 立即触发该 backend 一次 `session.list` 重扫 → catalog 指纹 diff → 既有 `sessions_changed` 控制面广播（`event_publisher.go:773`，backend 级）→ iOS 自动刷新列表；`host/session-status{running}` → 运行徽标实时翻转（外部 turn 开始/结束）。
  2. **兜底层（零新代码）**：bridge 既有逐 backend session discovery watcher（`session_discovery.go:50`，周期 ListSessions→指纹 diff→`sessions_changed`）对 dsh-web 自动生效——host 流断线期间由它保底。
  - 现有会话新增 turn：mux `session/event` 全会话覆盖（4.3.3）——已打开的消息页实时收流；未打开会话的徽标/时间由 `session-status` + `sessions_changed` 刷新，打开时经 history/投影补齐全程。

#### 4.3.2 消息页加载（历史渲染：思考流、工具卡片、分页）

- `get_session_messages` ✅ `session.history`：`beforeSeq`↔cursor、`maxMessages`↔limit（官方按 append-origin 消息边界分页）；映射为既有 rich-history 形状（role/content/thinking/parts/steps）——官方 history 自带 `ToolEventView` 渲染意图与整日志统计，工具步骤信息比旧件直读 store 更富。
- `get_session_projection`（SSV2 消息页）✅+♻️：live 事件喂 kernel（见 4.3.3）；冷会话 pathless hydrate 的 `RichHistoryProvider` 数据源 = `session.history`（机制同旧件，仅换数据源；投影代码零新增）。
- `read_file_v2`/`fetch_content_chunk` ♻️ bridge 通用 Mac 文件读取（与 backend 无关）。

#### 4.3.3 流式同步与运行态（isGenerating、text/思考增量、停止按钮、完成通知）

- **两条常驻 SSE**：`GET /api/events.mux`（会话事件）+ `GET /api/events.host`（会话生命周期，4.3.1 即时层）。mux 的 `session/event` 帧（SessionEvent 与磁盘日志同构）→ **复用 `agent/dsh/codec.go` §3.3 映射** → core.Event → 既有推送管线（relay/直连）→ iOS 既有渲染链，零新渲染逻辑。
- 运行态：turn 事件推导 + `host/session-status{running}` → `session_state_changed` 广播（既有）。
- **外部 turn 可见性（对旧 dsh 是新能力）**：mux 覆盖全部会话——用户在 Mac web 发起的 turn，iOS 同样收事件/投影与完成通知（对齐 claudecode 外部旁观）；dsh-web 不进 `backendHasNoExternalEventSource` 剪枝名单，observation/离线 mailbox ♻️ 全部照常。
- 断线：SSE 重连=重开流+重拉 history（官方 v1 语义照搬）；断线窗口由重连后 history 重拉+投影 forceCold 补齐。
- 红线（坑 7/8）：`turn/end reason=error` 必须透传官方错误文本为终态事件——失败路径必现可见终态。

#### 4.3.4 发送消息

- 新会话 ✅ `session.create{cwd=iOS 选定目录}`（cwd 命中已注册 workspace 自动归组，§3.5）→ `session.prompt{mode:"queue", content:[text]}`。
- 既有会话 ✅ `session.prompt` 直接续（官方 resume，§3.1）——iOS「会话已结束」文案路径对 dsh-web 不适用，无守卫、无 `session_resume_not_supported`。
- steer 插话转向 2️⃣ `mode:"steer"`（一期 queue）。
- 附件/图片 2️⃣：官方 `session.attachment` + `imageLimits` 投影已核实；一期 descriptor `StaticCapabilities` 声明 text-only（既有两级附件校验照走）。
- `abort_generation` ✅ `session.cancel{sessionId}`。
- 审批/问题应答（owner 决定项，倾向一期）：`approval/requested`/`question/requested` 帧 → 既有 permission/question 事件（iOS 权限 UI 已有），应答 `POST /api/respond`；不接则 descriptor 不声明 capability、iOS 隐藏入口（声明制）。

#### 4.3.5 provider / model（iOS 模型选择器、provider 切换）

- `list_providers` ✅ `llm.providers{}` → 用户 dsh 已配置的 provider 全集（如 opencode-go/zai——复用用户已配，初衷）。
- `list_models` ✅ `llm.models{}`（provider 分组目录）+ `session.models{sessionId}`（当前会话 effective 选择 + 可用集）。
- `switch_model` / backend 级 `set_provider` ✅ `session.selectModel{sessionId, provider, model, reasoningEffort?}`——官方语义=会话内立即生效并持久化为部署默认；无 backend 级全局写面，如实按此语义映射。
- **模型目录/白名单永远来自运行时**（坑 3 红线，不再手写复本）。
- `list_permission_modes`/`set_permission_mode` ⛔ 一期：dsh 权限形态由 agent preset 承载，无 bridge 级 mode 写面；二期评估 preset 面（`agentPresets.*` API 已存在）。

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

- `run_diagnostics` ✅ 探测结构化输出（实例来源 external/managed、端口、`host.describe` 版本、`llm.providers` 状态）。
- `fetch_todos` ⛔ 一期（web 的 plan 属会话投影无 todo RPC；二期评估）；`get_usage` ⛔ 一期（token 计量在 context-meter 投影单元，二期按 `session/projection` 帧核对后决定接入或维持）；diff 三件套 ⛔；`list_agents` ⛔ 一期（单一 preset；`agentPresets` API 已存在，二期）。
- `session.search` ✅ 备用（iOS 搜索现为本地实现已够；官方 search 可作二期服务端搜索）。
- `check_pending_notifications`/prekey/delivery ♻️ bridge/relay 通用，与 backend 无关。

### 4.4 安全模型

仅 loopback 访问（trust fence 无 Origin 放行）；`deepseek-web` 的 iOS 流量永远只走 Bridge（8777/relay，配对+加密）；managed 端口永不对外；特权方法由 fence 钉死 loopback——**禁止**任何 `--host 0.0.0.0`/`--trusted-host` 托管配置。

## 5. 与旧 `deepSeek` backend 并存

同一份 `~/.dsh/sessions`；各自独立列表；跨 backend 打开行为差异如实：dsh-web 可 resume 旧件建的会话，旧件对 dsh-web 建的会话仍走「已结束」守卫。附件一期 text-only 声明（与旧件一致；官方 `session.attachment`/`imageLimits` 面已核实存在，二期按 owner 需求接入）。

## 6. 测试与验收

- 单测：httptest 假 dsh 服务（schema 样本 fixtures：list/history/prompt/models/providers 响应、SSE mux 帧序列）覆盖 §4.3 功能面总表**逐行**（含 ⛔ 项的 not_supported 断言）；生命周期（探测命中/managed spawn/双失败 not_configured）；探活失败诊断文案；
- 回归：bridge 全量 + 旧 deepSeek 套件零改动验证；
- iOS：`BackendKind` case 单测 + 定向 CCCodeTests；
- 真机验收（核心新增行）：**iOS 对 Mac web 创建的任意历史会话发消息→真实续聊（双向：web 续 iOS 建的会话）**；列表/历史/新建/流式/停止对齐旧件既有验收行。

## 7. 边界与退役

- 硬边界：不 re-pin、不改 vendor（旧 backend 冻结依赖）；不写用户 `workspace.json`（归组由 web 自身 create/fork 收编）；`since` 续传 v1 未实现照官方语义重开流。
- 旧 `deepSeek` 退役判据：owner 日常真机使用 dsh-web 全绿若干天后另行裁决（不在无证据时预判）；退役时同步清理 iOS `session_resume_not_supported` 文案映射。

## 8. 实施拆分（批准后走 exec-plan）

1. `agent/dshweb` HTTP 客户端 + 探活 + 生命周期（探测/managed）；
2. 映射表逐行（list/history/prompt/cancel/models）+ 单测；
3. SSE mux 管线（codec 复用 + approval/question 决定项）；
4. wire descriptor/canonical 协议 pack + iOS `BackendKind` case + mirror；
5. Mac App drivers 默认列 + Release 安装 + 真机验收矩阵。
