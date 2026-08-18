# OpenCode Web Backend 初稿（调研 / 方向 / 中间稿，不作实施真值）

> **不要按本文实施。** 给开发 agent 的真值是同目录  
> [`2026-08-18-opencode-web-backend-design.md`](2026-08-18-opencode-web-backend-design.md)。  
> 本文保留：v1.0–v1.2 讨论轨迹，以及后来写成「可实施正本」但仍偏方向清单的中间稿。

- 日期：2026-08-18（初稿。v1.0 新开包只写 v2；v1.1 改成原地收口；v1.2 折中为新包+旧包冻结；其后一版补了坑表/双代表/§8，但仍缺文件行号、请求体形状、core 接口清单和 handlers.go 全 RPC 表）
- 状态：**初稿 / 调研收口。不作实施依据。**
- 背景：旧 `opencode` 在 iOS 上占用圈空、部分会话发了没回；官方网页（本机 `opencode serve`）同会话有数。owner 要求 **新开独立 backend**，与旧包物理隔离，成熟后再摘旧入口、代码不删。
- 对照：结构意图效仿 [2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md)，但本文件密度不够，开发 agent 仍会猜。
- 不变约束：探测-复用-未启动、零迁移、双向接力、SSV2 十二条护栏。不写用户 OpenCode 会话库。不 import `agent/opencode`。不删 `agent/opencode/`。

调研/方向讨论的压缩结论（已拍板，实施不得回退）：

1. 新包 + 新 iOS kind + 切换框「OpenCode Web」，旧「OpenCode」并存至成熟。
2. 新包是 **HTTP/SSE 客户端**，不 bind 任何端口。
3. 旧管家继续管现网 serve（常见 4096，实际可能是 4096…4196 任一空闲口）。新包不要写死 4097。
4. 沙盒可用 **4096…4196 以外** 的口 + 独立数据目录；**成熟验收必须挂现网网页那份 serve**。
5. 成熟后：新包接管现网管家（沿用 `opencode-managed-server.json`）→ 两边入口去掉旧 OpenCode → 旧代码冻结不删。

---

## 1. 路线定义（一句话）

`opencode-web` = **官方 `opencode serve` 的 HTTP/SSE 客户端 + bridge-v1 翻译器**。列表、历史、占用、发送、模型、活跃态、审批（及核实后的问答）全部来自官方 HTTP，MacBridge 不推导存储事实、不 spawn `opencode run`、不调 `sqlite3`。

| 字段 | 值 |
|---|---|
| Go 目录 | `agent/opencode-web/` |
| 包名 | `opencodeweb`（目录可含连字符，包名不能） |
| import | `github.com/openAgi2/cordcode-macbridge/agent/opencode-web` |
| `core.RegisterAgent` / drivers id | `opencode-web` |
| wire `kind` | `opencode-web` |
| iOS | `BackendKind.openCodeWeb`，`fromWireKind("opencode-web")` |
| 显示名 | OpenCode Web |
| 图标 | 复用 `opencode_logo` |
| 旧包 | `agent/opencode/` **一行不改、不 import、不删** |

对比 dsh-web：那条路是「从未接过的官方 web」。本路线是「官方 serve 已在、旧包 hybrid 没接干净」，所以 **新开包是为了归因和冻结旧实现**，不是因为 4096 上没有 API。

---

## 2. 前车之鉴（评审/实施按此清单核对）

### 2.1 旧 `opencode` 仍在用的面（本设计不改旧包）

产品默认 `managed_local`：Swift `OpenCodeManagedServer` 起 loopback `opencode serve`（端口 **4096…4196** 择空闲，Basic Auth，状态 `opencode-managed-server.json` 0600），把 `-opencode-url` 交给旧 runtime。

旧包 **已经** HTTP 的部分：`POST /session/:id/prompt_async`、`GET /global/event`、`GET /session/:id/message`、`GET /session/status`、`POST /session/:id/permissions/:id`。有 URL 时发送走 `server_session.go`，无 URL 才 `opencode run`。

仍脏、且会在真机露馅（新包必须避开）：

| # | 坑 | 事实 | 新包如何消除 |
|---|---|---|---|
| 1 | 列表走 CLI + sqlite3 | `ListSessions` → `opencode session list`；`querySessionMessageCounts` spawn `sqlite3` 查 `opencode.db` | 只 `GET /session` |
| 2 | 模型走 CLI + 磁盘缓存 | `opencode models` + `*.opencode-models.json` | 只 `/provider`（或探测到的 v2 `/api/provider`+`/api/model`） |
| 3 | 占用读错字段 | `GET /session/:id` 顶层 `tokens` 本机 99/100 为 0；网页用最后一条 assistant ÷ `limit.context`（`packages/app/src/components/session/session-context-metrics.ts`） | §4.3.2 公式；顶层 tokens=0 必须回落 `/message` |
| 4 | 发送不带 model | `prompt_async` body 只有 `parts`；session.model 常 null → 默认 `zhipuai-coding-plan/glm-4.7`；目录挂了 **81ms 空转零 assistant**（2026-08-14 `docs/2026-08-14-opencode-empty-turn-and-grokbuild-session-load-timeout-analysis.md`） | 发送必带 catalog 内模型；不在目录 → 立刻可见错误 |
| 5 | 目录头错了像空会话 | `x-opencode-directory` 不对：列表有标题、`/message` 0 条。网页 URL 带 `/server/<项目>/` | 所有读写带会话自己的 directory |
| 6 | 问答未接 | `RespondQuestion` 直接报不支持 | 1.18 路径未活体钉死前 ⛔ 或 fail-visibly，不臆造 |
| 7 | 权限字面量抄错 | v2 是 `once`/`always`/`reject`，不是 allow/deny（`packages/schema/src/permission.ts`） | §4.3.4 折叠表；单测钉死 |
| 8 | 把 checkout v2 当成唯一现网 | 本机 1.18.18 是无前缀 `/session`、`/global/event`、`/global/health` | §3 双代表；默认跟 1.18 |
| 9 | SSV2 漏 kind | dsh-web 真机行 2/3/4：Mac 已推 patch，iOS `sessionSyncV2ProjectionBackend` 漏 `.deepSeekWeb` | §4.3.2 / §8-5：Mac 接线 + iOS 列入同步做 |
| 10 | 新包再抄一份管家抢端口 | 旧管家已在 4096…4196 择口；写死 4097 会撞 | 客户端不 spawn；沙盒用 **4296…4396** |

### 2.2 过程纪律（同 dsh-web §2.3，本路线补一条）

1. 对 OpenCode 的行为断言必须读源码或打本机 serve，禁止「听起来合理」。
2. 否定性断言必须写在哪一代 API、哪个路径找过。
3. 模型目录、窗口、占用只来自运行时，不手写白名单。
4. 失败必须可见终态（send 错误或 `turn_error`），禁止 81ms 空转当成功。
5. 载波/路径必须活体：1.18 与 v2 表不一致时以**本机正在跑的 managed server**为准，checkout 只作次表。
6. **禁止 import `agent/opencode`。** 对照旧文件可以复制，复制后归属新包。旧 hybrid/proxy（`handlers_opencode.go`）按 `backendID=="opencode"` 门控，新包走通用 handler。

---

## 3. 前置核实（源码 + 2026-08-18 活体）

### 3.1 官方网页就是这份 serve ✅

浏览器 `http://127.0.0.1:4096/server/.../session/ses_…` 与 CordCode `managed_local` 是同一进程。没有第二套「公网 OpenCode Web」。新包对齐网页 = 对齐这份 HTTP，不是再转发一层浏览器。

### 3.2 两代 API 必须并列 ✅

| 能力 | 1.18 本机（**默认实施面**） | checkout v2（探测到再切） |
|---|---|---|
| 探活 | `GET /global/health` | `GET /api/health` |
| 列表 | `GET /session` **裸数组** | `GET /api/session` → `{data, cursor}` |
| 详情 | `GET /session/:id` | `GET /api/session/:id` → `{data}` |
| 消息 | `GET /session/:id/message` | 同路径加 `/api` 前缀 |
| 发送 | `POST /session/:id/prompt_async` `{parts:[{type,text}]}` | `POST /api/session/:id/prompt` `{prompt, delivery?, resume?}` |
| 停止 | `POST /session/:id/abort` | `POST /api/session/:id/interrupt` |
| 活跃 | `GET /session/status`（key 在=busy，**缺席=idle**） | `GET /api/session/active`（**本进程** foreground drain，缺席≠全局 idle） |
| SSE | `GET /global/event`（payload 包一层） | `GET /api/event`（`V2Event`） |
| 模型 | `GET /provider`（`models.*.limit.context`） | `GET /api/provider` + `GET /api/model` |
| 权限答 | `POST /session/:id/permissions/:requestID` `{response}` | `POST /api/session/:id/permission/:requestID/reply` `{reply, message?}` |
| 项目建议 | `GET /project`（旧 proxy 已用） | `GET /api/location` **只解析 location**，不是项目列表 |
| 压缩后消息 | （非占用） | `GET /api/session/:id/context` = 上次 compact 后仍在上下文的消息，**禁止当占用表** |

启动探针（按序，记录用的是哪一代）：

1. `GET {base}/global/health`（可 401，再带 Basic Auth）
2. 若 404/非 JSON：试 `GET {base}/api/health`
3. `GET …/session` 或 `/api/session` 看是数组还是 `{data}`
4. 失败 → `not_configured`，诊断写清试过的路径，不猜 64667

认证：与旧管家相同的 Basic Auth。无密码 health 200 仍按现规拒绝（`legacy_64667` 除外）。

### 3.3 占用公式（官方网页，必须抄）✅

`packages/app/src/components/session/session-context-metrics.ts`：

1. 从消息列表倒找最后一条 `tokenTotal > 0` 的 assistant；
2. `total = input + output + reasoning + cache.read + cache.write`；
3. `usage% = round(total / model.limit.context)`（无 limit 则网页也不画百分比）。

本机：顶层 `tokens` 全 0 的会话，`/message` 里 last assistant 仍可有 `official_total=18457`。旧实现整包丢掉 → iOS「暂无」。

### 3.4 权限 / 问答字面量 ✅ / ⚠️

- 权限 v2：`once` | `always` | `reject`。折到 bridge：`once`→allow，`always`→allow+always，`reject`→deny。1.18 `{response}` 以活体为准，可能已是 allow/deny；**探针钉死再写死映射**，禁止文档先写 allow/deny 当 v2 官方值。
- 问答 v2 reply：`{answers: string[][]}`（按题顺序的多选标签），一次仍可能是一组题。1.18 路径未在本机打通前一期 ⛔，iOS 走既有空态，不报「不支持」横幅。

### 3.5 空转 ✅

2026-08-14：默认/陈旧 `zhipuai-coding-plan/...` 本地解析失败，server 81ms `exiting loop`，无 assistant、无 error 事件。旧 SSE 后来把「有 turn 无输出」收成 `turn_error`。新包：**发送前 catalog 校验** + **零输出 idle = turn_error**，两条都要，只靠后者 iOS 仍像「没回」。

---

## 4. 架构

### 4.1 模块、注册、与旧包边界

```
iPhone BackendKind.openCodeWeb
        │  kind=opencode-web
        ▼
go-bridge handlers（通用 send/list/get_session/projection）
        │  禁止进入 handlers_opencode.go / OpenCodeProxy
        ▼
agent/opencode-web  (package opencodeweb)
        │  HTTP + SSE
        ▼
opencode serve   ← 客户端，不由本包 listen
```

**go-bridge（一期必须动、且只加不改旧门控）：**

| 位置 | 处置 |
|---|---|
| `go-bridge/main.go` | `_ "…/agent/opencode-web"`；默认 `-drivers` **追加** `opencode-web`（旧 `opencode` 仍在） |
| `buildAgentOptions` | 新 id 读 `-opencode-web-url` / user / pass（可与现网 URL 相同，见 §4.2）；**不要**复用「仅当 id==opencode 才注册 proxy」的分支 |
| `shouldStartPassiveSubscription` | `opencode-web` 在 URL 非空时启动 agent 级 SSE |
| `backendSupportsProjectionHydrate` | case 加入 `"opencode-web"` |
| `prepareProjectionHydrateSource` / `produceProjectionHydrateRange` | pathless 家族加入（agentName=`opencode-web`） |
| 两处 forceCold / sourceChanged | 加入 |
| `server.go` SSV2 广告 | `id == "opencode-web" \|\| kind == "opencode-web"` |
| `backendHasNoExternalEventSource` | **不进**（SSE 即外部事件源；现在也只有 `deepseek` 在名单里） |
| `handlers_opencode.go` / `RegisterOpenCodeProxy` | **不**为 `opencode-web` 注册 |

**Mac App：** `RuntimeManager` 默认 `drivers` 追加 `"opencode-web"`。现网模式把**已解析**的 serve URL/凭据同时传给 `-opencode-url`（旧）和 `-opencode-web-url`（新）。沙盒模式只给新包 `-opencode-web-url=http://127.0.0.1:<4296-4396>`。

**iOS 穷举（编译器强制，漏一处编不过）。** 新增 `.openCodeWeb` 时至少打开这些文件逐处归组（与 dsh-web 的 11 处同类；以当时 `rg "case \\.openCode"` 为准）：

- `BackendModels.swift`（fromWireKind / displayName / icon / isCustomAsset / 文案 / `usesRootOnlySessionCatalog` / `usesBackendLiveEventStream`）
- `ChatUIKitContainerView.swift`（输入条 Agent/目录显隐、模型标题、权限齿轮、⭕ 表）
- `ChatViewModel.swift`（**`sessionSyncV2ProjectionBackend` 必须列入**，坑 9）
- `ChatViewModel+Generation.swift`、`+CodexStreaming.swift`、`+DirectoryPreferences.swift`
- `SelectionSheets.swift`、`ServerViewModel.swift`、`SessionLifecycleDiagnosticPhase.swift`
- `ModelManagementService.swift`、`CCCodeBridgeBackendClient.swift`、`BridgeDiscoveryService.swift`

行为决策（写进归组注释，禁止 silently 抄 `.openCode` 的脏行为）：

| 点 | 决策 |
|---|---|
| `usesRootOnlySessionCatalog` | **false**（与旧 OpenCode 一样按目录，对齐网页 `/server/<项目>/`） |
| 输入条目录名 | 隐藏（与 Claude/Codex/Grok/Harness 一致，更多设置里看文件） |
| 输入条 Agent 标签 | 有多个 agent 才显示（OpenCode 有多 agent） |
| ⭕ | 走占用公式；无窗口但有 used 仍尽量显示；不要只给 Harness 官方拆分表 |
| 列表预设芯片 | 无（Harness 专用） |
| 旧 `.openCode` | 并存期保持；退役期 `isDeprecated` |

`docs/protocol/` canonical + iOS mirror 增加 `opencode-web` kind。显示名「OpenCode Web」。

### 4.2 生命周期（客户端 vs 管家）

**Agent 不 spawn、不 bind。** 只消费 opts 里的 URL。URL 空 → `not_configured`。

**并存期管家（Swift，旧逻辑尽量不动）：**

| 模式 | 旧 `opencode` | `opencode-web` |
|---|---|---|
| 现网（验收/日常） | 继续管 4096…4196 + `opencode-managed-server.json` | **只连**该已解析 URL（第二个客户端） |
| 沙盒（防冲掉） | 不动 | 可选：单独起 serve，端口 **4296…4396**，状态 `opencode-web-managed-server.json`（0600），**独立数据目录** |

禁止：新包再实现一套 4096…4196 择口。禁止写死 4097。

**退役期管家：**

1. 新包侧 Swift 接管「探现网口 → 没有则 spawn」，**改写/沿用** `opencode-managed-server.json`（一份，避免双 supervisor）。
2. drivers 去掉 `opencode`。
3. iOS `.openCode` 退役隐藏。
4. 摘入口前必须验证：旧管家不跑时，冷启动仍能探活/拉起 serve。

两态都失败：hello_ack 如实未启动，不代装、不猜 64667。

### 4.3 功能面映射

对照 `handlers.go` dispatch × iOS 已有功能。标记：✅一期 ｜ ♻️通用管线 ｜ 2️⃣二期 ｜ ⛔ not_supported（空态/隐藏，不报错横幅）。

#### 4.3.1 会话列表

- `list_sessions` ✅ `GET /session`（v2 解 `{data}`）。映射：`id`、`title`、`time.updated`→modifiedAt、`directory`。**请求头 `x-opencode-directory` = 当前目录**（无则不要假装全局完整）。
- `get_session` ✅ `GET /session/:id`，directory 头用该会话 directory。
- 置顶 ♻️ bridge pin。
- 下拉刷新 ✅ list + 投影 forceCold。
- 外部列表变更 ✅ SSE `session.created/deleted/updated`（及 1.18 同名）→ catalog 指纹 → `sessions_changed`；discovery watcher ♻️ 保底。
- 子 agent / 空会话：若官方字段可判别则过滤；未核实前不过滤，单测用夹具钉「未知字段忽略」。

#### 4.3.2 消息页与投影

- `get_session_messages` ✅ `GET /session/:id/message` → `RichHistoryProvider`（role / text / thinking / tool parts）。
- `GetSessionContextUsage` ✅ §3.3 公式。`contextUsage` 挂 `get_session` / `get_session_messages`（既有 `getSessionContextUsage`）。iOS `applyContextUsage` 要求 `contextWindow > 0`——窗口必须从 `/provider` 的 `limit.context` 来；模型 id 用 last assistant 的 `providerID`+`modelID`。
- `get_session_projection` ✅+♻️ pathless：冷基线 = 上条 HTTP 消息列表；live = SSE→kernel。

  | 接线点 | 处置 |
  |---|---|
  | `backendSupportsProjectionHydrate` | 加入 `opencode-web` |
  | `prepareProjectionHydrateSource` pathless | 加入（agentName=`opencode-web`） |
  | `produceProjectionHydrateRange` | 加入 |
  | 两处 forceCold / sourceChanged | 加入 |
  | deepseek store-file / live-only | **不进** |
  | `backendHasNoExternalEventSource` | **不进** |
  | `server.go` SSV2 广告 | 加入 |
  | iOS `sessionSyncV2ProjectionBackend` | **加入 `.openCodeWeb`** |

- `SessionActivityProbing` ✅：`IsSessionActive` = 1.18 `/session/status` 有该 id。错误/未知 → **active**。禁止只用 v2 `/session/active` 当全局 idle。
- `read_file_v2` ♻️。

#### 4.3.3 流式与旁观

- 常驻 SSE（按代选 `/global/event` 或 `/api/event`）。映射至少：`message.part.delta`→`EventText`；reasoning→`EventThinking`；tool→`EventToolUse`/`EventToolResult`；`session.status` idle/running→运行态；permission/question 帧→§4.3.4。
- 实现放在新包（对照 `sse_subscriber.go` 复制，不 import）。
- 外部网页 turn：同一 serve 的 SSE 全量覆盖 → iOS 旁观。`RequiresExternalTurnPolling=false`（SSE 健康时）。
- 断线：重开 SSE + 重拉 message + forceCold。
- 红线：零输出 idle → `EventResult{Error, Done}` → wire `turn_error`，文案保留「model/provider may be unavailable」类可诊断句。HTTP 4xx/5xx 原文进诊断，不折叠。

#### 4.3.4 发送、停止、审批、问答

- 新建 ✅ `POST /session`：directory + **catalog 内 model**（`{id, providerID}`）。
- 续聊 ✅ `prompt_async` / v2 `prompt`：**同样带 model**。不在 catalog → send RPC 错误，不进空转。
- `abort_generation` ✅ 按代 `abort` 或 `interrupt`。
- 附件/图片 2️⃣；一期 StaticCapabilities **不声明** image（与 dsh-web 一期 text-only 同）。
- 审批 ✅（一期必接，否则工具卡死）：SSE `permission.*` → 既有 permission 事件；回复按 §3.4 折叠。surface 判据 = bridge registry 命中该 session（与 dsh-web 一致）。网页先答 → 收 resolved 关卡（先答者得）。
- 问答：1.18 未钉死 → ⛔；v2 核实后 2️⃣/`question_reply`。禁止写「比 dsh 简单所以一期随便做」。

#### 4.3.5 provider / model / agent

- `list_providers` / `list_models` ✅ `/provider`（及 v2 表）。窗口字段留下给占用。
- `switch_model` ✅ 会话级：v2 `POST …/model`；1.18 无独立口则下一次 prompt 带新 model，并在诊断注明。
- `list_agents` ✅ `GET /agent`（若 1.18 有）。空则 ⛔。
- `list_permission_modes` ⛔（除非活体证明与 bridge 三档同构）。

#### 4.3.6 会话增删改

- rename / delete ✅ 若对应 HTTP 存在（v2 有 delete；1.18 以活体为准）。无则 ⛔，iOS 隐藏删除。
- archive 2️⃣。
- share ⛔。
- `compress_context` 2️⃣ `POST …/compact`（有则接，无则 ⛔）。

#### 4.3.7 目录

- `list_projects` ✅ `GET /project`（不是 `/api/location`）。
- `list_directory` ♻️ 一期沿用 bridge 通用 FS；若 1.18 有可靠 fs.list 再 ✅ 并补测。完成报告必须写明走的是哪条。
- git 面 ⛔。

#### 4.3.8 其余

- `run_diagnostics` ✅：来源（现网 URL / 沙盒 URL）、API 代、health、默认模型是否在 catalog。
- memory / todos / diff / get_usage RPC ⛔（占用走 §4.3.2，不叫 get_usage）。
- pin ♻️。
- search ♻️ 本地。

### 4.4 安全

仅 loopback。凭据只在 data dir 0600 文件与进程环境，不进 git。沙盒 json 与现网 json **文件名分开**（并存期）；退役后只留现网那一份。禁止 `--host 0.0.0.0`。

### 4.5 SSV2 真相清单

- **真相 owner**：`opencode serve`（会话、消息、模型、占用分母）。
- **唯一 writer**：serve 写自己的库；新包零直接写 sqlite/db。
- **事务域**：既有 pathless kernel；无新域。
- **新增路径**：可选 `opencode-web-managed-server.json`（仅沙盒）；退役后不再作为日常真值。
- **写入口**：create / prompt_async|prompt / abort|interrupt / permission reply /（核实后的）question reply。
- **失败**：无 URL→not_configured；模型不在目录→send 错误；空转→turn_error；权限超时不裁判。
- **防双写测试**：新包 grep 禁止 `sqlite3`、`opencode session list`、`opencode run`、`opencode models`。

---

## 5. 与旧 `opencode` 并存

- 切换框两个入口。同一现网 URL 时列表是同一批 `ses_…`（两个客户端）。
- 旧入口脏行为（圈空、空转）**保持原样**，方便对照；不要顺手修旧包。
- 沙盒 URL 时两套会话，**不得**用沙盒绿替代 §6 现网矩阵。
- 用户从旧入口建的会话，新入口在现网模式下可打开（同一 serve）。

---

## 6. 测试与验收

**单测（httptest 假 serve，覆盖 1.18 裸数组 + 一组 v2 `{data}`）：**

- 探针选代；401+Basic Auth。
- 列表映射；directory 头传递。
- 占用：顶层 tokens=0 + message 里 last assistant 有数 → 仍出 used/window；窗口缺失不谎报 200k。
- 发送 body 含 model；catalog 没有该模型 → 错误、不 POST prompt。
- 零输出 idle → EventResult.Error。
- 权限折叠表。
- 包内禁止 sqlite3/CLI 字符串（或测试扫源文件）。
- `git grep` / `go list` 断言新包 import 图不含 `agent/opencode`。

**回归：**

- `go test ./agent/opencode-web/... ./go-bridge/... -count=1`；旧 `./agent/opencode/...` **零失败增量**（不改旧包则应与基线一致）。
- iOS `-only-testing:CCCodeTests/BridgeModelsTests` + `sessionSyncV2ProjectionBackend` 含 openCodeWeb。
- `git diff --stat agent/opencode` 必须空。

**真机（成熟 = 现网 serve，不是沙盒）：**

| # | 前提 | 动作 | 应看到 |
|---|---|---|---|
| 1 | 网页占用约十几万的会话 | iOS **OpenCode Web** 点 ⭕ | 有已用/窗口 |
| 2 | 同会话 | iOS 发短消息 | 有回复；坏模型明确失败 |
| 3 | 网页 `/server/.../session/...` | 对应目录打开 | 历史与网页一致 |
| 4 | Mac 网页打字 | iOS 已打开 | 旁观 |
| 5 | 切回旧 OpenCode | 同一 Mac | 旧入口仍可用 |
| 6 | 工具触发审批 | iOS Allow/Deny | turn 继续；网页先答则 iOS 收口 |
| 7 | （退役后）drivers 无 `opencode` | 冷启动 | 只有 OpenCode Web；现网口仍被拉起 |

---

## 7. 边界与退役

- 硬边界：不 import 旧包；不删旧包；不写用户 OpenCode db；agent 不 bind 端口；不把 location 当项目列表；不把 `…/context` 当占用。
- 兜底：可**复制**旧 `sse_subscriber` / HTTP helper 进新包。红线：复制后禁止两边改同一份。
- 退役判据：§6 表 1–6 现网全 ✅，owner 书面确认后再摘入口（§4.2）。回滚 = drivers 加回 `opencode`。

---

## 8. 实施拆分（批准后走 exec-plan）

每项按 impl / tests / regression 三件套拆（与 dsh-web 队列同纪律）。

1. **骨架**：`agent/opencode-web` 注册、Client、双代探针、WireDescriptor、`not_configured`；`main.go` import + drivers 追加；禁止 import 旧包的测试。
2. **列表/历史/占用**：§4.3.1–4.3.2 HTTP 映射 + 占用公式单测（tokens=0 回落）。
3. **SSE + SessionActivityProbing**：旁观、重连、零输出 turn_error、`/session/status` 三态。
4. **发送 + 模型**：create/prompt 带 catalog 模型；停止按代；catalog 校验。
5. **投影接线**：§4.3.2 表逐项 + iOS `sessionSyncV2ProjectionBackend`；Test 风格对齐 `TestDSHWeb*`。
6. **审批**：SSE→permission→折叠 reply；registry 判据；先答者得。问答未核实保持 ⛔。
7. **iOS kind + protocol mirror**：穷举归组 + BridgeModelsTests + 真机安装（不删旧 kind）。
8. **Release**：`/Applications` 覆盖安装；现网 URL 矩阵 §6；**不得**用沙盒绿宣称成熟。退役另立任务，不塞进本期默认完成定义。

---

## 9. 初稿对照（避免再退回调研结论）

| 初稿/讨论里出现过 | 正本 |
|---|---|
| 原地改 `agent/opencode`、不新开包 | **否**，新包新入口 |
| 再开包会「不能访问 4096」 | **否**，正是 4096（现网 URL）的客户端 |
| 新包自己 bind 4096/写死 4097 | **否** |
| 只抄 v2 `/api/*` | **否**，默认 1.18，v2 为次表 |
| 权限 allow/deny 当官方值 | **否**，v2 为 once/always/reject |
| `GET …/context` 或顶层 tokens 当占用 | **否**，抄网页 last assistant |
| 成熟 = 沙盒绿 | **否**，必须现网网页同一 serve |
| 退役删 `agent/opencode` | **否**，只摘入口 |

给评审：先核 §2 坑表与 §3 双代表是否仍与本机 1.18 一致，再核 §8 接线点是否写全。不要在未读 1.18 活体的情况下把实施默认改回 checkout-only v2。

---

## 10. 更早讨论轨迹（v1.0–v1.2）

- **v1.0**：新 backend + 只写 v2 `/api`，形状有误。
- **v1.1**：改成原地收口、不新开包。技术对照（双代、占用公式、权限字面量）保留。
- **v1.2**：owner 拍板——新包新入口、旧包冻结；并存验证后摘旧入口、代码不删；客户端不 bind 端口；沙盒可用隔离口，成熟必须以现网网页那份 serve 为准；退役时新包接管现网管家。
- **其后中间稿（本文主体）**：按 dsh-web 目录骨架补了坑表、双代表、RPC 标记和 §8 拆分，但仍是方向清单：没有包文件职责、没有 `New()` opts、没有 `handlers.go` 全 RPC 表、没有 go-bridge/iOS 行号、没有请求/响应 JSON 形状。开发 agent 按本文仍会猜。
