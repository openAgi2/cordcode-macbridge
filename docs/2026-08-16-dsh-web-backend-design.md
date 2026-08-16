# dsh-web Backend 设计（官方 Web API 转发 + bridge-v1 翻译）

- 日期：2026-08-16
- 状态：**设计稿，待 owner 过目**（批准后才实施）
- 背景：SDK stdio 路线收口暂停（`docs/2026-08-16-dsh-session-store-bridge-design完成情况.md` 收口节）；owner 裁决并行新增本 backend，旧 `deepSeek` 保留不删。
- 不变约束：CordCode 初衷（探测-复用-未启动、零迁移、双向接力、永不自托管）+ SSV2 十二条护栏。

## 1. 路线定义（一句话）

dsh-web backend = **官方 Web API 的请求转发器 + bridge-v1 成熟格式（codex/claudecode/opencode/grokbuild 已验证）的翻译器**。所有数据格式转换在 MacBridge 端完成，iOS 几乎零改。MacBridge 不持有任何自己推导的 dsh 事实（模型名、编码、标题、归组全由官方 API 供给）——上一路线三轮真机故障的类别从架构上删除。

## 2. 四项前置核实结论（源码 pin 47f9438 + 本机实测）

### 2.1 prompt 对既有会话 = 官方 resume ✅

`packages/host/apiproxy/src/api-proxy.ts:1653-1670`：`session.prompt` 对已存在会话走 `ctx.agents.resume({resumeSessionId, …})`（磁盘冷恢复，会话记录的 preset/模型选择随 resume 恢复），未知 id 走 `agents.create`。owner 已在 web 实证「对任意历史 session 发消息」。**模型选择三级解析**（进程内 > 会话日志 `request/header` > `agent-default-model`）——历史会话用它自己记录的模型，上轮「deepseek-chat 越界」类故障在这条路线上结构性不可能。

### 2.2 事件流契约 ✅

载波（`dsh-host-apiproxy` README + `dsh-client-connection/src/api-path.ts`）：
- unary：`POST /api/<method>`，`application/json`（否则 415）；业务错误恒 HTTP 200 + `RpcResult` error 分支（封闭错误码集 `RpcErrorDetailsMap`）；
- 流：`GET /api/events.mux`（SSE）与 `GET /api/events.host`（SSE）；服务端反向请求（审批/问题应答）`POST /api/respond`；
- 重连：`since` v1 未实现——重连=重开流+重拉 history（bridge 侧照做即可）。

MuxFrame（`api/events.schema.ts`，SSE 帧判别联合）：
- `session/event`：**`event` 字段即 SessionEvent——与磁盘日志同构信封**（`{type,seq,time,data}`），现有 `agent/dsh/codec.go` §3.3 映射表可直接复用；另带可选 `view`（工具渲染意图）；
- `session/subscribed{lastSeq}`、`session/queue`（queued/steering/context 收件箱）、`session/jobs`、`session/projection{key,value,seq}`（高序覆盖的通用投影对）、`stream/error`；
- `approval/requested|resolved`、`question/requested|resolved`（应答走 `/api/respond`）。

HostFrame：`host/session-added`（含 parent/origin/cwd）、`session-removed`、`session-status{running}`、`agent-error`、`workspace-changed`。

### 2.3 托管启动形态 ✅（本机 `dsh web --help` 实测）

- `dsh --profile web --host <host> --port <port>`；**port 0 = OS 自选**；默认 host `127.0.0.1`（loopback）、默认端口 3080；
- 网关挂 `/api` 前缀；无自动开浏览器（help 无 open；`printUrl` 仅打印，可关）；
- **信任栅栏**（`dsh-client-connection/src/api-request-trust.ts`）：Host 头必须 loopback（或 `--trusted-host` 白名单）；**无 Origin 的非浏览器客户端放行**（「Absent Origin is fine」）；`sec-fetch-site: cross-site` 拒绝；特权方法（credentials/settings 等）钉死 loopback——bridge 从本机 loopback 访问天然全通，且该面永不对外暴露；
- 与用户自己的 3080 实例按端口共存。

### 2.4 API 稳定性 = 无版本协商，随版本整体演进 ⚠️（如实）

- 无版本字段/协商；方法集由 `RpcMethodMap` 编译期锁定（增删方法=类型错误）；契约层零 Node 依赖、可从浏览器 import，RFC 文档化（gui-layering-and-rpc-protocol）——设计上就是多客户端共享面；
- **应对**（同 opencode 先例）：启动时能力探活（`host.describe` + `session.list` 空调用 + `llm.providers`）；失败/字段缺失 → backend `not_configured` + 诊断如实，绝不猜。dsh 升级破坏契约 = 诊断可见，不静默。

### 2.5 关键方法形状（映射直接依据）

- `session.list`：`{cursor?}`（cursor 为保留位未实现→一次全量，bridge 自行分页）→ `items: [{sessionId, updatedAt(ms), running, blank, parentSessionId?, origin?, cwd?, agentPreset?, projections?}]`（标题等元数据在 projections 块，实现时按 `sessionProjectionsBlockSchema` 落定具体 unit；无标题 unit 则退 `session.history` 尾读）；
- `session.create`：`{workspaceId? | cwd?, sessionId?, agentPreset?}`——**传 cwd（iOS 选定目录）**；注意 web 的 workspace attach 恰好发生在 HTTP create/fork 流程内（9ac8102 复盘），cwd 命中已注册 workspace 时新会话自动归组——旧路线的「未分组」问题在此路线对命中目录自动解决；
- `session.prompt`：`{sessionId, mode: "queue"|"steer", content:[parts], clientTimeZone?}`；
- `session.cancel`：`{sessionId}`；`session.history`：冷读（不 resume、不发布 Agent）+ `beforeSeq` 向后分页 + 尾页 projections 块；
- `llm.models`/`session.models`：官方模型目录（`list_models` 数据源）。

## 3. 架构

### 3.1 模块与身份

- 新 Go 包 `agent/dshweb`（注册名 `dsh-web`；wire kind `deepseek-web`；iOS `BackendKind.deepSeekWeb`，显示「DeepSeek Web」）；Mac App runtime 默认 drivers 增列（c71c692 先例）；iOS 仅增 case + 列表项。
- 不与旧 `agent/dsh` 共享代码；旧 backend 一行不动。

### 3.2 web 服务生命周期（探测复用优先，managed 兜底）

1. 探测：`POST 127.0.0.1:3080/api/host.describe` 探活（可扩展已知端口列表/用户配置）；命中 → 复用用户自己的实例；
2. 未命中 → managed：spawn `dsh --profile web --host 127.0.0.1 --port <3096..3196 自选>`，端口与凭据状态写 data dir 的 `dsh-web-managed-server.json`（0600，opencode-managed-server.json 先例）；崩溃重启/sleep-wake 归 RuntimeManager 既有生命周期管理；
3. 两态都失败 → hello_ack 如实「未启动」（初衷：不代装）。

### 3.3 映射表（bridge RPC → dsh API）

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

### 3.4 安全模型

仅 loopback 访问（trust fence 无 Origin 放行）；`deepseek-web` 的 iOS 流量永远只走 Bridge（8777/relay，配对+加密）；managed 端口永不对外；特权方法由 fence 钉死 loopback——**禁止**任何 `--host 0.0.0.0`/`--trusted-host` 托管配置。

## 4. 与旧 `deepSeek` backend 并存

同一份 `~/.dsh/sessions`；各自独立列表；跨 backend 打开行为差异如实：dsh-web 可 resume 旧件建的会话，旧件对 dsh-web 建的会话仍走「已结束」守卫。附件一期 text-only 声明（与旧件一致；官方 `session.attachment`/`imageLimits` 面已核实存在，二期按 owner 需求接入）。

## 5. 测试与验收

- 单测：httptest 假 dsh 服务（schema 样本 fixtures：list/history/prompt 响应、SSE mux 帧序列）覆盖映射表逐行；生命周期（探测命中/managed spawn/双失败 not_configured）；探活失败诊断文案；
- 回归：bridge 全量 + 旧 deepSeek 套件零改动验证；
- iOS：`BackendKind` case 单测 + 定向 CCCodeTests；
- 真机验收（核心新增行）：**iOS 对 Mac web 创建的任意历史会话发消息→真实续聊（双向：web 续 iOS 建的会话）**；列表/历史/新建/流式/停止对齐旧件既有验收行。

## 6. 边界与退役

- 硬边界：不 re-pin、不改 vendor（旧 backend 冻结依赖）；不写用户 `workspace.json`（归组由 web 自身 create/fork 收编）；`since` 续传 v1 未实现照官方语义重开流。
- 旧 `deepSeek` 退役判据：owner 日常真机使用 dsh-web 全绿若干天后另行裁决（不在无证据时预判）；退役时同步清理 iOS `session_resume_not_supported` 文案映射。

## 7. 实施拆分（批准后走 exec-plan）

1. `agent/dshweb` HTTP 客户端 + 探活 + 生命周期（探测/managed）；
2. 映射表逐行（list/history/prompt/cancel/models）+ 单测；
3. SSE mux 管线（codec 复用 + approval/question 决定项）；
4. wire descriptor/canonical 协议 pack + iOS `BackendKind` case + mirror；
5. Mac App drivers 默认列 + Release 安装 + 真机验收矩阵。
