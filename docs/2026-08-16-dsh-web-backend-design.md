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

### 4.3 映射表（bridge RPC → dsh API）

| bridge 面 | dsh API | 备注 |
|---|---|---|
| `list_sessions` | `session.list` | 过滤 `origin=subagent`；running→运行态 enrich；mtime=updatedAt；directory=cwd |
| `get_session` / `get_session_messages` | `session.history` | `beforeSeq`↔cursor、`maxMessages`↔limit；projections 块按需取标题 |
| `send_message`（新会话） | `session.create{cwd}` → `session.prompt{mode:"queue"}` | cwd=iOS 选定目录；命中 workspace 自动归组 |
| `send_message`（既有会话） | `session.prompt{mode:"queue"}` | **官方 resume——跨进程续聊达成，无守卫、无 `session_resume_not_supported`** |
| `abort_generation` | `session.cancel` | |
| `list_models` | `llm.models`（+`session.models`） | 官方目录；不再手写白名单 |
| `rename_session` | `session.rename` | 可选二期 |
| 事件 | `GET /api/events.mux`（SSE 长连） | `session/event` 帧→复用 `codec.go` §3.3 映射→现有通用事件管线→kernel→SSV2；重连=重开流+重拉 history；`approval/question` 帧→现有 permission/question 事件、应答 `POST /api/respond`（**一期是否接入=owner 决定项**，不接则 MacBridge 如实声明能力缺位） |
| 冷历史投影 | `RichHistoryProvider` ← `session.history` | 走既有 pathless hydrate（同现机制，仅数据源换 HTTP）；无新投影代码 |

`start_session` 不再 spawn 子进程：bridge 侧每会话状态仅为「SSE 订阅 + 会话句柄」，进程模型收敛为「常驻 web 服务 + 单条 SSE」。

### 4.4 安全模型

仅 loopback 访问（trust fence 无 Origin 放行）；`deepseek-web` 的 iOS 流量永远只走 Bridge（8777/relay，配对+加密）；managed 端口永不对外；特权方法由 fence 钉死 loopback——**禁止**任何 `--host 0.0.0.0`/`--trusted-host` 托管配置。

## 5. 与旧 `deepSeek` backend 并存

同一份 `~/.dsh/sessions`；各自独立列表；跨 backend 打开行为差异如实：dsh-web 可 resume 旧件建的会话，旧件对 dsh-web 建的会话仍走「已结束」守卫。附件一期 text-only 声明（与旧件一致；官方 `session.attachment`/`imageLimits` 面已核实存在，二期按 owner 需求接入）。

## 6. 测试与验收

- 单测：httptest 假 dsh 服务（schema 样本 fixtures：list/history/prompt 响应、SSE mux 帧序列）覆盖映射表逐行；生命周期（探测命中/managed spawn/双失败 not_configured）；探活失败诊断文案；
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
