# OpenCode Web Backend 设计（官方 HTTP/SSE 转发 + bridge-v1 翻译）

> [!CAUTION]
> **历史文档，已于 2026-08-20 停止作为实施依据。** 本文保留当时设计与评审轨迹，不能再作为开发 agent 的“唯一真值”，也不得从本文继续排任务或补丁。后续工作以
> [source-parity audit](2026-08-20-opencode-web-source-parity-audit.md) 和
> [source-first convergence plan](2026-08-20-opencode-web-source-first-convergence-plan.md)
> 为准；当前产品代码实施仍由 owner 暂停。已知原因包括：本文允许复制 legacy HTTP/SSE/历史映射、若干请求键和事件顺序未经真实样本证明、以及 endpoint 存在性被误当成官方 Web 语义等价。

- 日期：2026-08-18（v2：实施真值；v3：一轮评审 M1-M3+S1-S9；v3.1：二轮评审 **APPROVE** 收口——diff 逐项核验通过、S7 亲核修正独立复核属实、audit-plan 背书盘点通过，三条实施期提示落稿，见文末「评审采纳记录」。中间稿/讨论轨迹见 [初稿](2026-08-18-opencode-web-backend-design-初稿.md)）
- 状态：**已实施过、现已被替代，仅供历史追溯**。原“开发 agent 的唯一真值”声明作废。
- 背景：旧 `opencode` 在 iOS 上占用圈空、部分会话发了没回；官方网页（本机 `opencode serve`）同会话有数。owner 要求 **新开独立 backend**，与旧包物理隔离，成熟后再摘旧入口、代码不删。
- 对照：结构、纪律、接线密度效仿 [2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md)。**不要**把 dsh 的 JSON-RPC 信封、双 WebSocket、`/api/respond` 批问答原样抄过来。本路线的载波是 **HTTP + SSE**，不是 WebSocket。
- 不变约束：探测-复用-未启动、零迁移、双向接力、SSV2 十二条护栏。不写用户 OpenCode 会话库。不 import `agent/opencode`。不删 `agent/opencode/`。

已拍板、实施不得回退：

1. 新包 + 新 iOS kind + 切换框「OpenCode Web」，旧「OpenCode」并存至成熟。
2. 新包是 **HTTP/SSE 客户端**，不 bind 任何端口、不 spawn `opencode run`、不调 `sqlite3`。
3. 旧管家继续管现网 serve（常见 4096，实际可能是 4096…4196 任一空闲口）。新包不要写死 4097。
4. 沙盒可用 **4096…4196 以外** 的口 + 独立数据目录；**成熟验收必须挂现网网页那份 serve**。
5. 成熟后：新包接管现网管家（沿用 `opencode-managed-server.json`）→ 两边入口去掉旧 OpenCode → 旧代码冻结不删。

---

## 1. 路线定义（一句话）

`opencode-web` = **官方 `opencode serve` 的 HTTP/SSE 客户端 + bridge-v1 翻译器**。列表、历史、占用、发送、模型、活跃态、审批全部来自官方 HTTP；MacBridge 不推导存储事实。

| 字段 | 值 | 依据 |
|---|---|---|
| Go 目录 | `agent/opencode-web/` | owner 指定独立目录；对照 `agent/dsh-web/` |
| 包名 | `opencodeweb` | Go 目录可含连字符，包标识符不能（dsh-web → `dshweb` 先例） |
| import | `github.com/openAgi2/cordcode-macbridge/agent/opencode-web` | |
| `core.RegisterAgent` / drivers id | `opencode-web` | `hello_ack.backends[].id` |
| wire `kind` | `opencode-web` | `hello_ack.backends[].kind`；iOS `fromWireKind` |
| 显示名 | `OpenCode Web` | `WireDescriptor.DisplayName` |
| iOS | `BackendKind.openCodeWeb` | 穷举 switch 编译期强制 |
| 图标 | 复用 `opencode_logo` | 不新做资产 |
| 旧包 | `agent/opencode/` **一行不改、不 import、不删** | |

对比 dsh-web：那条路是「从未接过的官方 web」。本路线是「官方 serve 已在、旧包 hybrid 没接干净」，所以 **新开包是为了归因和冻结旧实现**，不是因为 4096 上没有 API。

对比旧 OpenCode：旧包已经 HTTP 的部分（`prompt_async`、`/global/event`、`/session/:id/message`、`/session/status`、权限 POST）对照可复制进新包；脏面（CLI list / sqlite3 / 顶层 tokens / 发送不带 model）不得复制。

---

## 2. 前车之鉴（评审/实施按此清单核对）

### 2.1 旧 `opencode` 仍在用的面（本设计不改旧包）

产品默认 `managed_local`：Swift `OpenCodeManagedServer`（`MacBridge/MacBridge/Services/OpenCodeManagedServer.swift`）起 loopback `opencode serve`（端口 **4096…4196** 择空闲，Basic Auth，状态 `opencode-managed-server.json` 0600），把 `-opencode-url` 交给旧 runtime（`RuntimeManager.swift:1084-1086`）。

旧包 **已经** HTTP 的部分（复制对照，不 import）：

- `POST /session/:id/prompt_async` — `agent/opencode/server_session.go:116-128`
- `GET /global/event` SSE — `agent/opencode/sse_subscriber.go:100`
- `GET /session/:id/message` — `agent/opencode/providers.go:83-101`
- `GET /session/status` — `agent/opencode/providers.go:112-122`
- `POST /session/:id/permissions/:id` — `agent/opencode/server_session.go:168-187`

仍脏、且会在真机露馅（新包必须避开）：

| # | 坑 | 事实 / 源码 | 新包如何消除 |
|---|---|---|---|
| 1 | 列表走 CLI + sqlite3 | `ListSessions` → `opencode session list`（`opencode.go:538-540`）；`querySessionMessageCounts` spawn `sqlite3`（`opencode.go:707+`） | 只 `GET /session` |
| 2 | 模型走 CLI + 磁盘缓存 | `opencode models` + `*.opencode-models.json`（`opencode.go:99-106`） | 只 `/provider`（或探测到的 v2 `/api/provider`+`/api/model`） |
| 3 | 占用读错字段 | `GetSessionContextUsage` 读 `GET /session/:id` 顶层 `tokens`（`context_usage.go:86-107`）；本机 99/100 为 0。网页用最后一条 assistant ÷ `limit.context`（官方 `packages/app/src/components/session/session-context-metrics.ts`） | §3.3 公式；顶层 tokens=0 必须回落 `/message` |
| 4 | 发送不带 model | `prompt_async` body 只有 `parts`（`server_session.go:116-118`）；session.model 常 null → 默认 `zhipuai-coding-plan/glm-4.7`；目录挂了 **81ms 空转零 assistant**（`docs/2026-08-14-opencode-empty-turn-and-grokbuild-session-load-timeout-analysis.md`） | 发送必带 catalog 内模型；不在目录 → 立刻可见错误 |
| 5 | 目录头错了像空会话 | `x-opencode-directory` 不对：列表有标题、`/message` 0 条。网页 URL 带 `/server/<项目>/` | 所有读写带会话自己的 directory；**含读路径的 switchDir 特判（评审 M2-1，§4.1.5）——不加特判则四个读方法永不切目录、坑 5 原样复发** |
| 6 | 问答未接 | `RespondQuestion` 直接报不支持（`server_session.go:190-196`） | 1.18 路径未活体钉死前 ⛔，不臆造 |
| 7 | 权限字面量抄错 | v2 是 `once`/`always`/`reject`（`packages/schema/src/permission.ts`），不是 allow/deny。旧包发 `{response: behavior}` 且 behavior 来自 bridge 的 `allow`/`deny`（`server_session.go:173-177`） | §4.3.4 折叠表；1.18 活体钉死后再写死映射 |
| 8 | 把 checkout v2 当成唯一现网 | 本机 1.18.18 是无前缀 `/session`、`/global/event`、`/global/health` | §3.2 双代表；默认跟 1.18 |
| 9 | SSV2 漏 kind | dsh-web 真机行 2/3/4：Mac 已推 patch，iOS `sessionSyncV2ProjectionBackend` 漏 `.deepSeekWeb`（历史事实；**当前 main 已含 `.deepSeekWeb`，dsh-web 实施时修复——评审 S9 补注**） | §4.3.2 / §8-5：Mac 接线 + iOS **同期**列入（`.openCodeWeb` 同样必须加入） |
| 10 | 新包再抄一份管家抢端口 | 旧管家已在 4096…4196 择口；写死 4097 会撞（`OpenCodeManagedServerTests.swift` 即用 4097 作第二空闲口） | 客户端不 spawn；沙盒用 **4296…4396** |
| 11 | iOS 旧 OpenCode 自动批权限 | `ChatViewModel+CodexStreaming.swift:1969-1971`：`autoApproveBridgePermissionIfNeeded` **只对 `.openCode`** 自动 allow | 新 kind **禁止**进这个分支，否则审批 UI 永远不出现 |
| 12 | `detectAgentStatus` 默认 Available | `agent_descriptor.go:228-229`：未列 id 一律 `AgentStatusAvailable`。新 id 若不加 case，空 URL 也会在 hello_ack 里显示可用 | 必须加 `case "opencode-web"` + `InstanceStatus`（dsh-web 先例 `:225-226`） |
| 13 | `shouldStartPassiveSubscription` 默认 true | `main.go:778-788`：非 opencode/codex 一律启动 Subscribe。新 id 若不特判，空 URL 会无意义重连退避 | URL 空时 **不**启动 SSE |

### 2.2 过程纪律（同 dsh-web §2.3，本路线补一条）

1. 对 OpenCode 的行为断言必须读源码或打本机 serve，禁止「听起来合理」。
2. 否定性断言必须写在哪一代 API、哪个路径找过。
3. 模型目录、窗口、占用只来自运行时，不手写白名单。
4. 失败必须可见终态（send 错误或 `turn_error`），禁止 81ms 空转当成功。
5. 载波/路径必须活体：1.18 与 v2 表不一致时以**本机正在跑的 managed server**为准，checkout 只作次表。
6. **禁止 import `agent/opencode`。** 对照旧文件可以复制，复制后归属新包。旧 hybrid/proxy（`handlers_opencode.go`）按 `backendID=="opencode"` 门控，新包走通用 handler。
7. **禁止 silently 抄 `.openCode` 的 iOS 脏行为**（坑 11：自动批权限）。每处穷举必须显式归组。

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
| 消息 | `GET /session/:id/message` **裸数组** | 同路径加 `/api` 前缀 |
| 发送 | `POST /session/:id/prompt_async` `{parts, model?}` | `POST /api/session/:id/prompt` `{prompt, delivery?, resume?}` |
| 停止 | `POST /session/:id/abort` | `POST /api/session/:id/interrupt` |
| 活跃 | `GET /session/status`（key 在=busy，**缺席=idle**） | `GET /api/session/active`（**本进程** foreground drain，缺席≠全局 idle） |
| SSE | `GET /global/event`（payload 包一层） | `GET /api/event`（`V2Event`） |
| 模型 | `GET /provider`（`models.*.limit.context`） | `GET /api/provider` + `GET /api/model` |
| 权限答 | `POST /session/:id/permissions/:requestID` `{response}` | `POST /api/session/:id/permission/:requestID/reply` `{reply, message?}` |
| 项目建议 | `GET /project`（旧 proxy 已用） | `GET /api/location` **只解析 location**，不是项目列表 |
| Agent 列表 | `GET /agent` 裸数组（`providers.go:155-188`） | 探测到再切 |
| todos | `GET /session/:id/todo`（`providers.go:125-153`） | 探测到再切 |
| 压缩后消息 | （非占用） | `GET /api/session/:id/context` = 上次 compact 后仍在上下文的消息，**禁止当占用表** |

**双面共存事实（评审 S3 活体）**：本机 1.18.18 上 `/api/health` 也返回 200（`{"healthy":true}`）、`/api/session` 返回 `{data:[…]}` 信封——即「有 `/api` 前缀」不能证 v2。**探针互斥依据**=checkout v2 无 `/global/health` 路由（health 走 /api）；第三步「数组 vs `{data}`」仍是最终形状判据，维持。

启动探针（按序，记录用的是哪一代，写入 `InstanceStatus` 诊断）：

1. `GET {base}/global/health`（可 401，再带 Basic Auth）
2. 若 404/非 JSON：试 `GET {base}/api/health`
3. `GET …/session` 或 `/api/session` 看是数组还是 `{data}`
4. 失败 → `not_configured`，诊断写清试过的路径，不猜 64667

认证：与旧管家相同的 Basic Auth（`Authorization: Basic …`）。无密码 health 200 仍按现规拒绝（`legacy_64667` 除外）。

### 3.3 占用公式（官方网页，必须抄）✅

官方 `packages/app/src/components/session/session-context-metrics.ts`：

1. 从消息列表倒找最后一条 `tokenTotal > 0` 的 assistant；
2. `total = input + output + reasoning + cache.read + cache.write`；
3. `usage% = round(total / model.limit.context)`（无 limit 则网页也不画百分比）。

映射到 `core.ContextUsage`（`core/interfaces.go:441+`；wire 经 `handlers.go:3472+` `contextUsageToWire`）：

| 字段 | 值 |
|---|---|
| `UsedTokens` / `TotalTokens` | 上式 `total` |
| `InputTokens` | last assistant `input` |
| `OutputTokens` | last assistant `output` |
| `ReasoningOutputTokens` | last assistant `reasoning` |
| `CacheReadTokens` / `CachedInputTokens` | `cache.read` |
| `CacheWriteTokens` | `cache.write` |
| `ContextWindow` | `/provider` 里该模型的 `limit.context` |

红线：

- 顶层 `session.tokens` 只作加速。为 0 就拉 `/message`，不得整包丢掉。
- iOS `applyContextUsage` 要求 `contextWindow > 0`。窗口缺失 → 返回 nil（圆环「暂无」），**禁止谎报 200k**。
- 模型 id 用 last assistant 的 `providerID` + `modelID`（消息 `info` 上，见 `providers.go:310-312`），不要只用 session 顶层 `model`（常 null）。
- 禁止把 v2 `GET …/context` 当占用。

本机已核实：顶层 `tokens` 全 0 的会话，`/message` 里 last assistant 仍可有 `official_total=18457`。

### 3.4 权限 / 问答字面量 ✅ / ⚠️

- 权限 v2 官方值：`once` | `always` | `reject`。1.18 二进制 strings 三字面量齐在（评审 S4 取证，置信增强）——但二进制字面量不能证明唯一枚举，实施仍按「先探」策略。
- bridge `core.PermissionResult.Behavior` 只有 `"allow"` / `"deny"`（`core/interfaces.go:71-75`），**没有 always 字段**。iOS `approveAlways` 在 wire 层已折成 `behavior:"allow"`（dsh-web 设计 R3-2 同源）。
- 一期映射（1.18 活体钉死前按此实施，探针结果写入诊断）：

  | iOS / wire | 发给 1.18 `{response}` | 发给 v2 `{reply}` |
  |---|---|---|
  | allow（含 Always 标签） | 先探 `once`；若 4xx 再试 `allow` | `once`（Always 标签弱化，与 dsh-web 同类） |
  | deny | 先探 `reject`；若 4xx 再试 `deny` | `reject` |

- 问答 v2 reply：`{answers: string[][]}`（按题顺序的多选标签）。1.18 路径未在本机打通前一期 ⛔，iOS 走既有空态，不报「不支持」横幅，也不实现 `RespondQuestion`。

### 3.5 空转 ✅

2026-08-14：默认/陈旧 `zhipuai-coding-plan/...` 本地解析失败，server 81ms `exiting loop`，无 assistant、无 error 事件。旧 SSE 在 `emitResultOnce`（`sse_subscriber.go:716-747`）把「有 turn 无输出」收成：

```
EventResult{Error: "model produced no output (model or provider may be unavailable on the server)", Done: true}
```

wire 层映射为 `turn_error`。新包：**发送前 catalog 校验** + **零输出 idle = 同上 turn_error**，两条都要。只靠后者 iOS 仍像「没回」（用户先看到空等）。文案保留可诊断句，禁止折叠成泛化「出错了」。

### 3.6 关键 JSON 形状（1.18，映射直接依据）

`GET /session` 裸数组元素（字段以本机 + 旧 `opencodeSessionInfo` 为准）：

```json
{
  "id": "ses_…",
  "title": "…",
  "directory": "/Users/…/project",
  "time": { "created": 0, "updated": 0 },
  "model": { "id": "…", "providerID": "…" },
  "tokens": { "input": 0, "output": 0, "reasoning": 0, "cache": { "read": 0, "write": 0 } }
}
```

`time.updated` 是毫秒。`model` / `tokens` 经常全 0 或 null。**两级 tokens 形状不同（评审 S1 活体修正）**：顶层**无 `total` 字段**（100 会话采样）；**message 级 `info.tokens` 才有 `total`**（活体 18457 实例）——占用公式读 message 级，样板勿混。

`GET /session/:id/message` 裸数组元素（`providers.go:190-315`）：

```json
{
  "info": {
    "id": "msg_…",
    "role": "user|assistant",
    "agent": "…",
    "modelID": "…",
    "providerID": "…",
    "time": { "created": 0, "completed": 0 }
  },
  "parts": [
    { "type": "text", "text": "…" },
    { "type": "reasoning", "text": "…" },
    { "type": "tool", "tool": "…", "state": { "status": "completed|error|pending", "input": {}, "output": "…" } }
  ]
}
```

assistant 的 tokens 可能在 `info.tokens`（与 session 顶层同形）。占用公式读这里，不读 session 顶层。

`POST /session`（创建，`server_session.go:139-166`）：

```json
{ "directory": "/abs/path", "model": { "id": "glm-4.7", "providerID": "zhipuai-coding-plan" } }
```

响应 `{ "id": "ses_…" }`。`directory` 同时必须作为请求头 `x-opencode-directory`。

`POST /session/:id/prompt_async`（续聊，新包 **必须带 model**）：

```json
{
  "parts": [{ "type": "text", "text": "hello" }],
  "model": { "id": "glm-4.7", "providerID": "zhipuai-coding-plan" }
}
```

成功 = HTTP 204 或 200（`server_session.go:125`）。4xx/5xx 原文进 send 错误。

`GET /session/status`：

```json
{ "ses_abc": { "type": "busy" } }
```

缺席的 id = idle。解析失败或 HTTP 失败 → `IsSessionActive` 返回 **true**（保守）。

SSE `/global/event` 一帧（`sse_subscriber.go:186-188`）：

```json
{ "payload": { "type": "message.part.delta", "properties": { "sessionID": "ses_…", "delta": "…" } } }
```

也兼容无 `payload` 的顶层 `type`（CLI NDJSON）。新包只走 server 面，但仍应解开 `payload` 包装。

`GET /provider`：递归收集任意节点上同时有 `id` 与 `limit.context` 的对象（`context_usage.go:158-176`）。窗口 map 的 key 同时记 `id` 与 `providerID/id`。

`GET /agent` 裸数组：`{name|id, mode, description, hidden, native}`（`providers.go:167-186`）。

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

#### 4.1.1 新包文件清单（一期创建这些，不要先堆杂项）

对照 `agent/dsh-web/` 的职责切分，**不要**把所有东西塞进一个 `opencode.go`。

| 文件 | 职责 |
|---|---|
| `opencodeweb.go` | `init` 注册、`New(opts)`、`Agent` 结构、`Name`/`Stop`、`InstanceStatus` |
| `client.go` | HTTP 客户端：BaseURL、Basic Auth、`x-opencode-directory`、双代路径前缀、`doRequest`/`fetchJSON` |
| `probe.go` | §3.2 探针：选 1.18 / v2、health、信封形状；结果写入 generation |
| `sessions.go` | `ListSessions` / `get_session` HTTP 映射 |
| `history.go` | `GetRichSessionHistory` / `GetSessionHistory`；复制 `mapRichHistoryEntry` 语义 |
| `context_usage.go` | §3.3 公式 + `GetSessionContextUsage` |
| `session.go` | `StartSession` / `AgentSession`：create/resume、`Send`、`CancelTurn`、`RespondPermission` |
| `events.go` | SSE subscriber（对照复制 `sse_subscriber.go`，改占用公式、改路径按 generation） |
| `models.go` | `/provider` → `list_providers` / `list_models` / `SetModel` |
| `permissions.go` | 权限折叠；`SessionPermissionResponder` |
| `activity.go` | `IsSessionActive`（1.18 `/session/status`） |
| `projects.go` | `GET /project` → `list_projects` |
| `agents.go` | `GET /agent` → `list_agents` |
| `diagnostics.go` | `run_diagnostics` |
| `wire_descriptor.go` | `WireDescriptor` + `ToolAuthorizer`（点亮 permission_resolve） |
| `import_guard_test.go` | 禁止 import 旧包、禁止 sqlite3/CLI 字符串 |
| `client_test.go` / `sessions_test.go` / `history_test.go` / `context_usage_test.go` / `events_test.go` / `permissions_test.go` / `probe_test.go` | httptest 假 serve |

禁止出现的文件/符号：`opencode run`、`sqlite3`、`session list` CLI、`*.opencode-models.json`、64667 回落。

#### 4.1.2 `New(opts)` 键（不要复用旧包键名当唯一来源）

```
opts["work_dir"]             string
opts["opencode_web_url"]     string   // 已解析的 serve URL；空 = not_configured
opts["opencode_web_user"]    string
opts["opencode_web_pass"]    string
opts["pin_store"]            *pinstore.Store
opts["data_dir"]             string   // 仅诊断/沙盒 json；一期现网模式不写
```

`buildAgentOptions` 今天把 `opencode_url` 塞给 **所有** agent（`main.go:801-810`）。新包 **不要**读 `opencode_url`：另加 `opencode_web_url` / user / pass，由 `buildAgentOptions` 在 `id=="opencode-web"` 时写入。并存期 Swift 把**同一已解析 URL**同时传给 `-opencode-url` 和 `-opencode-web-url`。

URL 空：`InstanceStatus() = (false, "OpenCode Web endpoint not configured")`。`StartSession` / `ListSessions` 立刻失败，文案与诊断一致。禁止 LookPath(`opencode`) 作为 New() 失败条件（旧包 `opencode.go:107-109` 会在没 CLI 时整 backend 起不来——新包是纯 HTTP，不依赖 CLI）。

#### 4.1.3 必须实现的 core 接口

| 接口 | 为什么 | 不实现的后果 |
|---|---|---|
| `core.Agent` | 基座 | 无法注册 |
| `core.WireDescriptorProvider` | kind/显示名/live 模型 | 回落到 `agentKind` default=`id`（可用），但 polling 标志会走 `legacyRequiresPolling` 漏列 → 误报 |
| `core.EventSubscriber` | 常驻 SSE | 无旁观、无 live |
| `core.RichHistoryProvider` + `core.HistoryProvider` | 冷历史 / 投影基线 | pathless hydrate 失败 |
| `core.SessionActivityProbing` | 死会话尾封口 | 冷开永远 loading（`handlers_projection.go:1232-1238`） |
| `GetSessionContextUsage`（duck type，`handlers.go:3457-3464`） | ⭕ | 圆环暂无 |
| `core.ContextUsageReporter`（session 上） | live 占用 | 只靠 get_session 拉一次 |
| `core.ModelSwitcher` | 模型选择器 | 无 list_models / switch_model |
| `core.AgentLister` | 多 agent | 输入条 agent 空 |
| `core.ProjectLister` | 目录建议 | 无 `/project` |
| `core.DiagnosticsProvider` | 设置页诊断 | 空诊断 |
| `core.ToolAuthorizer` | 广告 `permission_resolve` | iOS 审批按钮不亮 |
| `core.SessionPermissionResponder` | 旁观时也能答审批 | 只有 StartSession 绑定才能答 |
| `core.TurnCanceler`（session 上） | 停止按钮 | abort 走 Close 误杀会话 |
| `core.WorkDirSwitcher` | `x-opencode-directory` 来源 | 目录头用错 |
| `core.SessionPinner` | 置顶元数据 | 置顶仍走 bridge pin store，但摘要解析弱 |
| `core.CatalogRefreshSignaler` | SSE `session.created/deleted` 立刻 `sessions_changed` | 只靠 discovery watcher 轮询 |

**一期不要实现：**

| 接口 | 原因 |
|---|---|
| `core.AttachmentSupporter` | 一期 text-only；声明 image/file 会让附件门打开后被静默丢（旧 server 模式丢图片，`wire_descriptor.go:37-44`） |
| `core.TodoProvider` | 有 `GET /todo`，但旧包广告 `todos` 却未实现接口。一期不广告、不实现，避免假能力。二期再接 |
| `core.UserInputResponder` / `SessionQuestionResponder` | 问答 ⛔ |
| `core.ModeSwitcher` | 与 bridge 三档不同构 |
| `core.MemoryFileProvider` | 旧包指 `OPENCODE.md`；新包不走磁盘记忆面 |
| `core.SessionDeleter` | 1.18 delete HTTP 未活体钉死前 ⛔ |
| `core.TranscriptLocator` | 无 JSONL |

#### 4.1.4 WireDescriptor（抄此字段，不要抄旧包 todos）

```go
Kind:                        "opencode-web"
DisplayName:                 "OpenCode Web"
LiveEventModel:              core.LiveEventBroadcast
RequiresExternalTurnPolling: false   // SSE 健康时；与旧包 true（watchdog）不同
StaticCapabilities:          []string{"external_turn_streaming"}
```

`permission_resolve` 由 `ToolAuthorizer` 类型断言推导，不要手写进 StaticCapabilities 又漏实现。不要广告 `todos`、`question_reply`、`structured_user_input_v1`。

#### 4.1.5 go-bridge（一期必须动、且只加不改旧门控）

行号以写本文时工作树为准；**v3 修订时的前一提交（Phase 5 任务通知）已使 handlers.go 行号漂移（二轮提示 1）**——实施时一律 `rg` 按符号复核，漏一处对应机制静默失效。

| 位置 | 处置 |
|---|---|
| `go-bridge/main.go:21-26` | `_ "…/agent/opencode-web"` |
| `go-bridge/main.go:35` | 默认 `-drivers` **追加** `opencode-web`（旧 `opencode` 仍在） |
| `go-bridge/main.go` flags | 新增 `-opencode-web-url` / `-opencode-web-user` / `-opencode-web-pass`（可与现网 URL 相同） |
| `buildAgentOptions` `main.go:801` | `id=="opencode-web"` 写入 `opencode_web_*`；**不要**让新包读 `opencode_url` |
| `shouldStartPassiveSubscription` `main.go:778` | `opencode-web` 在 URL 非空时才启动；空 URL 返回 false |
| `RegisterOpenCodeProxy` `main.go:197` | **不**为 `opencode-web` 注册 |
| `detectAgentStatus` `agent_descriptor.go:198` | `case "opencode-web": return detectLikeDSHWeb(agent)`（走 `instanceStatusProber`） |
| `dispatchRPC` switchDir 特判 `handlers.go:1236`（评审 M2-1） | `Name()=="opencode" \|\| shouldSwitchWorkDirForMethod()` 特判**扩含 `opencode-web`**——`shouldSwitchWorkDirForMethod` 对 list/get/messages/projection 四读方法返回 false，旧 opencode 全靠 Name 特判切目录；不加入则读路径 `x-opencode-directory` 恒为启动值，坑 5 复发 |
| `disablesRelayIdleTimeout` `handlers_relay.go:2421`（评审 M2-2） | **加入 case**（dsh-web 在列，注释即其真机故障：审批等待期无 text_delta，60s 空闲超时把已 surface 的权限卡收口）——opencode-web 一期必接审批，不加入必复发同型故障 |
| `resubscribeObservationSessions` `handlers.go:385`（评审 M3） | **加入名单**（现为硬编码五 backend，dsh-web 也不在——既有疑似缺口另报 owner，不在本设计内修）——外部 turn 旁观的 relay 重连 re-attach 依赖此名单；进=与 codex/claude 同待遇，§6 加重连用例 |
| `catalogCapabilityRequiredFor` `handlers.go:1030` | **不加入（评审 S7 修正）**：该门控的是 `list_sessions` 对**未协商 catalog_cursor_epoch_v2 的旧客户端**（显式 wire 错误，非静默、非 list_models）；dsh-web 不在列且工作正常——同判不加入，v1 客户端也可列。原行「list_models 失败被静默」描述失实，作废 |
| `backend_capabilities.go:59` permission_resolve 排除名单 | **无需改动**（评审 S5）：名单只排除 opencode/codex，新 id 自动放行；`ToolAuthorizer` 类型断言即广告 |
| `handlers_session_pin.go:255` directory-scope pin 查找 | **无需改动**（评审 S6）：新 backend 走 default any-scope——与旧 opencode 的 directory-scope 行为差异如实接受 |
| `backendKindForAgent` `handlers.go:4230` | Name()=`opencode-web` 时 default 已够；仍建议显式 case |
| `backendSupportsProjectionHydrate` `handlers_projection.go:339` | 加入 `"opencode-web"` |
| `prepareProjectionHydrateSource` `handlers_projection.go:561` | `case "opencode-web": agentName = "opencode-web"` |
| pathless 分支 `handlers_projection.go:653` | 加入 `opencode-web`（与 dsh-web 同组） |
| forceCold `handlers_projection.go:489-490` | 加入 `opencode-web` |
| `produceProjectionHydrateRange` 前置 `handlers_projection.go:1027` | 加入 |
| `produceProjectionHydrateRange` switch `handlers_projection.go:1086` | `case "opencode-web": return h.streamBackendRichHistoryProjectionEvents(ctx, "opencode-web", sessionID, emit)` —— **不要**复用 `streamOpenCodeRichHistoryProjectionEvents`（那条按 name=`opencode` 取 agent） |
| `pathlessRichHistoryBackend` `projection_kernel.go:691` | 加入 `"opencode-web"` |
| `advertiseSessionSyncV2Backend` `server.go:270-283` | `id == "opencode-web" \|\| kind == "opencode-web"` |
| `backendHasNoExternalEventSource` `handlers.go:1042` | **不进**（只有 `deepseek`） |
| `resolvePinWire` `handlers_session_pin.go:160` | **走 default** `resolveAgentListPin`，禁止进 `resolveOpenCodePin` |
| `handlers_opencode.go` | **不**为 `opencode-web` 开门 |

测试对齐：`go-bridge/handlers_projection_dshweb_test.go` 风格加 `TestOpenCodeWebProjectionWiring`；`main_test.go` 的 `shouldStartPassiveSubscription` 加空 URL / 有 URL 两行。

#### 4.1.6 Mac App

| 位置 | 处置 |
|---|---|
| `RuntimeConfig.drivers` 默认 `RuntimeManager.swift:117` | 追加 `"opencode-web"` |
| `processArguments` `RuntimeManager.swift:1067` | URL 非空时追加 `-opencode-web-url`（与 `-opencode-url` 同值） |
| `processEnvironment` | user/pass 走 env `OPENCODE_WEB_SERVER_USERNAME` / `PASSWORD`（或复用已有 OPENCODE_* 但 flag 仍分开） |
| `OpenCodeManagedServer` | **并存期一行不改**。现网模式只是第二个客户端连同一 URL |
| 沙盒（可选，独立任务） | 另起 serve，端口 **4296…4396**，状态 `opencode-web-managed-server.json` 0600，**独立数据目录** |

`MacBridgeBehaviorTests` 的 argv 测试加：drivers 含 `opencode-web`；有 URL 时两条 flag 都在。

#### 4.1.7 iOS 穷举与 if 比较型接线（评审 M1 修正：两类安全性不同）

- **switch 穷举类**：编译器强制，漏一处编不过；
- **`if ==` / `guard ==` 比较类**：**编译器不报错，静默漏**——必须 `rg '\.openCode\b'`（非测试）人工全量核对。评审 M1 全量扫描实得 18 文件：12 个 switch 类入下表；**3 文件 ≥12 处 if 比较类为行为分支必补**（v2 漏列，「编译器强制」的安全声明对它们不成立）；5 文件默认值类无需改动。

新增 `.openCodeWeb` 时打开这些文件逐处归组（`rg "case \\.openCode"` / `rg '\.openCode\b'` 复核）。写本文时至少：

| 文件 | 行号附近 | 归组决策 |
|---|---|---|
| `BackendModels.swift` | `fromWireKind` :24-31 | `"opencode-web"` / `"openCodeWeb"` → `.openCodeWeb`；`"opencode_web"` → **nil**（对照 deepseek-web 不认 `dsh-web` 下划线） |
| 同上 | `isDeprecated` :48-52 | **false**（退役期再改） |
| 同上 | `prefersComposerPermissionModes` :58 | **false**（不是 Harness 齿轮） |
| 同上 | `displayName` :71 | `"OpenCode Web"` |
| 同上 | `iconName` :92 | `"opencode_logo"` |
| 同上 | `isCustomAsset` :111 | 与 `.openCode` 同组 true |
| 同上 | `connectionSetupHelpText` :124 | 新文案：转发官方 `opencode serve` HTTP |
| 同上 | `inputBarModelTitle` :145 | `"Model"`（与 openCode 同组） |
| 同上 | `emptyModel*` :156-174 | 与 openCode 同组 |
| 同上 | `historyOnlySessionHelperText` :176 | **nil**（官方 HTTP 可续聊） |
| 同上 | `usesBackendLiveEventStream` :189 | **true** |
| 同上 | `usesRootOnlySessionCatalog` :196 | **false**（与旧 OpenCode 一样按目录，对齐网页 `/server/<项目>/`） |
| `ChatViewModel.swift` | `sessionSyncV2ProjectionBackend` :327 | **必须加入 `.openCodeWeb`**（坑 9） |
| `ChatViewModel+Generation.swift` | :2460 / :2490 | 与 `.openCode` 同组（`expectedProviderIDs=nil`，effort=nil） |
| `ChatViewModel+CodexStreaming.swift` | fallback name :1962 | 新 case 返回 `"opencode-web"` 或 `"assistant"`；**不要**只靠 `openCode` |
| 同上 | `autoApproveBridgePermissionIfNeeded` :1969 | **禁止**把 `.openCodeWeb` 算进去 |
| `ChatUIKitContainerView.swift` | agent title :4092 | `"OpenCode Web"` / 有 selectedAgent 则显示 agent 名 |
| 同上 | model 校验 :4425 | 与 openCode 同组 `break` |
| 同上 | timeline fallback :4498 | 有多个 agent 才显示 agent 名 |
| 同上 | 输入条目录 | 隐藏（与 Claude/Codex/Grok/Harness 一致；目录在更多设置里） |
| `SelectionSheets.swift` | :214 | 「OpenCode Web：工具审批经 Bridge 应答」 |
| `ServerViewModel.swift` | :990 | 超时文案：确认 Mac 上 `opencode serve` / CordCode Link 托管实例已就绪 |
| `SessionLifecycleDiagnosticPhase.swift` | :70 | 创建=HTTP create；历史=GET message；live=SSE；resume=session id+directory；模型=会话级 |
| `CCCodeBridgeBackendClient.swift` | :25 | 默认 backend id `"opencode-web"` |
| `ModelManagementService.swift` | :286 | 与 openCode 同组 |
| `BridgeTransportTests.swift` | fromWireKind / catalog / live stream | 加 `.openCodeWeb` 断言：`usesRootOnly=false`，`usesBackendLiveEventStream=true`，`fromWireKind("opencode-web")` |
| `ChatViewModelSessionSyncV2Tests.swift` | | 加 openCodeWeb 为 true |

**if 比较类行为分支（评审 M1 必补，3 文件 ≥12 处——不加入则对应行为静默缺失）：**

| 文件:行 | 语义 | 归组决策 |
|---|---|---|
| `SessionsView.swift:916` | 缓存阶段 OpenCode bucket 种子 | 与 `.openCode` 同组加入 |
| `SessionsView.swift:2117/2130/2134` | `fetchProjects`+去重+`sortedOpenCodeProjects` 项目合并 | **与 `.openCode` 同组加入**——不加则项目列表走通用路径（混入 manual 目录、无排序），击穿 `usesRootOnlySessionCatalog=false` 的「按目录对齐网页」承诺 |
| `SessionsView.swift:2663/2685/2715` | bucket 懒加载与分页路径选择（:2715 `if kind == .openCode { return nil }`） | 与 `.openCode` 同组加入（目录分组深挖） |
| `SidebarView.swift:91/281/388` | 项目分组侧栏显示条件 + 按目录 group 懒加载 | **与 `.openCode` 同组加入**——:91 `== .openCode && projectRoots` 为 false 则目录分组侧栏整体不显示 |
| `ChatViewModel+SessionManagement.swift:1134/1140` | agent 过滤（OpenCode 只显 primary agents） | 与 `.openCode` 同组加入（与旧入口行为一致） |
| `ChatViewModel+DirectoryPreferences.swift:107` 附近 | dsh-web 评审的通用路径点 | 实施时 `rg` 核对归组（评审 §5.3 提示项） |

**默认值类（评审 M1 注记，无需改动）**：`Server.swift`/`ServerConfig.swift`/`AddServerView.swift`/`BridgeOfflineSnapshotAdapter`/`BridgeDiscoveryService` 的 `= .openCode` 默认值与 decode 兜底——新 backend 不依赖默认值。

列表预设芯片：无（Harness 专用）。旧 `.openCode` 并存期保持。

#### 4.1.8 协议 pack

Mac 真值 `docs/protocol/bridge-v1.md` 在 dsh-web 节后增加 `Backend: opencode-web (kind opencode-web)`（形状对照 §4.3）。iOS mirror 同步。`docs/protocol/schema/bridge-v1.types.ts` 若有 kind 联合类型则追加。显示名「OpenCode Web」。非 breaking：新 kind，旧客户端忽略未知 backend。

### 4.2 生命周期（客户端 vs 管家）

**Agent 不 spawn、不 bind。** 只消费 opts 里的 URL。URL 空 → `not_configured`。

实现 `instanceStatusProber`（`agent_descriptor.go:191`）：

```
InstanceStatus() (available bool, detail string)
```

- URL 空 → `(false, "OpenCode Web endpoint not configured")`
- 探针失败 → `(false, "probe failed: GET /global/health …; GET /api/health …")`
- 成功 → `(true, "generation=1.18 url=http://127.0.0.1:4096")`

**并存期管家（Swift，旧逻辑尽量不动）：**

| 模式 | 旧 `opencode` | `opencode-web` |
|---|---|---|
| 现网（验收/日常） | 继续管 4096…4196 + `opencode-managed-server.json` | **只连**该已解析 URL（第二个客户端） |
| 沙盒（防冲掉） | 不动 | 可选：单独起 serve，端口 **4296…4396**，状态 `opencode-web-managed-server.json`（0600），**独立数据目录** |

禁止：新包再实现一套 4096…4196 择口。禁止写死 4097。禁止 `--host 0.0.0.0`。

**退役期管家（另立任务，不在本期完成定义）：**

1. 新包侧 Swift 接管「探现网口 → 没有则 spawn」，**改写/沿用** `opencode-managed-server.json`（一份，避免双 supervisor）。
2. drivers 去掉 `opencode`。
3. iOS `.openCode` 标 `isDeprecated`。
4. 摘入口前必须验证：旧管家不跑时，冷启动仍能探活/拉起 serve。

两态都失败：hello_ack 如实未启动，不代装、不猜 64667。

### 4.3 功能面完整映射

对照 `handlers.go` dispatch（`:1242-1341`）× iOS 已有功能。标记：✅一期 ｜ ♻️通用管线 ｜ 2️⃣二期 ｜ ⛔ not_supported（空态/隐藏，不报错横幅）。

#### 4.3.1 会话列表

- `list_sessions` ✅ `GET /session`（v2 解 `{data}`）。映射：`id`、`title`→Summary、`time.updated`→ModifiedAt、`directory`。**请求头 `x-opencode-directory` = 当前 workDir**（无则不要假装全局完整）。
- `get_session` ✅ `GET /session/:id`，directory 头用该会话 directory。
- 置顶 `set_session_pinned` / `list_pinned_sessions` ♻️ bridge pin；`resolvePinWire` 走 default list。
- 下拉刷新 ✅ list + 投影 forceCold。
- 外部列表变更 ✅ SSE `session.created` / `session.deleted` / `session.updated` → `CatalogRefreshSignaler` → 既有 `sessions_changed`；discovery watcher ♻️ 保底。
- 子 agent / 空会话：若官方字段可判别则过滤；未核实前不过滤，单测用夹具钉「未知字段忽略」。

#### 4.3.2 消息页与投影

- `get_session_messages` ✅ `GET /session/:id/message` → `RichHistoryProvider`。映射复制 `mapRichHistoryEntry`（`providers.go:190-315`）：`info` 下沉、`parts[].type` = text/reasoning/tool/file、tool 的 `state.status/output`。
- `GetSessionContextUsage` ✅ §3.3。挂 `get_session` / `get_session_messages`（既有 `getSessionContextUsage`，`handlers.go:3448+`）。
- `get_session_projection` ✅+♻️ pathless：冷基线 = HTTP 消息列表；live = SSE→kernel。

  | 接线点 | 处置 |
  |---|---|
  | `backendSupportsProjectionHydrate` :339 | 加入 `opencode-web` |
  | `prepareProjectionHydrateSource` :561 | `agentName=opencode-web` |
  | pathless 分支 :653 | 加入 |
  | forceCold :489 | 加入 |
  | `produceProjectionHydrateRange` :1027 / :1086 | 加入；hydrate 用 `streamBackendRichHistoryProjectionEvents(..., "opencode-web")` |
  | `pathlessRichHistoryBackend` :691 | 加入 |
  | deepseek store-file / live-only | **不进** |
  | `backendHasNoExternalEventSource` | **不进** |
  | `server.go` SSV2 广告 :270 | 加入 |
  | iOS `sessionSyncV2ProjectionBackend` :327 | **加入 `.openCodeWeb`** |

- `SessionActivityProbing` ✅：`IsSessionActive` = 1.18 `/session/status` 有该 id。错误/未知 → **active**。禁止只用 v2 `/session/active` 当全局 idle。
- `read_file_v2` / `fetch_content_chunk` ♻️。

#### 4.3.3 流式与旁观

常驻 SSE（按代选 `/global/event` 或 `/api/event`）。实现对照复制 `sse_subscriber.go`，事件表：

| SSE `payload.type` | core.Event | 备注 |
|---|---|---|
| `message.updated` role=user | `EventUserMessage` + 一次 `EventTurnStarted` | 见 `noteUserPrompt` |
| `message.part.delta` field=text | `EventText` | user 消息则累加 prompt，不进 assistant |
| `message.part.delta` field=reasoning | `EventThinking` | |
| `message.part.updated` type=text | `EventText` / `EventTextReplace` | snapshot vs delta |
| `message.part.updated` type=reasoning | `EventThinking` | |
| `message.part.updated` type=tool | `EventToolUse`；status completed/error → `EventToolResult` | `RequestID=part.id` |
| `session.status` type=running | 清 completion | 不要当 turn_completed |
| `session.status` type=idle | `emitResultOnce` | 唯一 turn 终态；零输出 → Error |
| `session.updated` status=idle/running | 同上 + **按 §3.3 重算占用** 发 `EventContextUsageUpdated` | 禁止再用顶层 tokens |
| `permission.asked` | `EventPermissionRequest` | `RequestID=id` |
| `todo.updated` | 一期忽略（不广告 todos） | 二期再 `EventPlan` |
| `session.created` / `deleted` | 不进 chat 流；打 `CatalogRefreshSignaler` | |
| `server.connected` / `session.diff` / `message.removed` | 忽略 | |

红线（从旧包必须保留）：

- **不要**在 `step_finish` 或 assistant `time.completed` 上发 `EventResult`（多步工具会提前 idle，`sse_subscriber.go:332-336, 421-424`）。
- 零输出 idle → `EventResult{Error, Done}` → wire `turn_error`。
- HTTP 4xx/5xx 原文进诊断，不折叠。
- 外部网页 turn：同一 serve 的 SSE 全量覆盖 → iOS 旁观。`RequiresExternalTurnPolling=false`。
- 断线：重开 SSE + 重拉 message + forceCold。
- surface 判据 = bridge registry 命中该 session（`h.getSession()` 有对象）。未打开的会话审批不进 iOS（网页自己答）。

#### 4.3.4 发送、停止、审批、问答

- `create_session` / 首次 `StartSession` ✅ `POST /session`：directory + **catalog 内 model** `{id, providerID}`。
- `send_message` ✅ 通用 `handleSendMessage` → `StartSession`/`Send`。续聊 `prompt_async` / v2 `prompt`：**同样带 model**。catalog 没有 → send RPC 错误，**不 POST**。
- 一期 StaticCapabilities / `AttachmentSupporter` **不声明** image/file。`Send` 收到非空 images/files → 返回错误，不要静默丢（旧 server 模式丢图片）。
- `abort_generation` ✅ 按代 `abort` 或 `interrupt`（`TurnCanceler`）。`Close` 只拆 SSE 绑定，不杀 serve。
- 审批 ✅（一期必接，否则工具卡死）：SSE `permission.asked` → 既有 permission 事件；回复按 §3.4。`SessionPermissionResponder` 覆盖旁观。网页先答 → 收 resolved 关卡（先答者得）。
- 问答 ⛔。`RespondQuestion` / `RejectQuestion` 返回 `core.ErrNotSupported`。不要写「比 dsh 简单所以一期随便做」。
- `resolve_user_input` ⛔（无 StructuredUserInputProvider）。

#### 4.3.5 provider / model / agent

- `list_providers` / `list_models` ✅ `/provider`（及 v2 表）。窗口字段留下给占用。`catalogCapabilityRequiredFor` 必须列入。
- `switch_model` / `set_provider` ✅ 会话级：v2 `POST …/model`；1.18 无独立口则记下 pending，下一次 prompt 带新 model，并在诊断注明。**UI 语义注记（评审 S8）**：1.18 的 pending 机制=「下次发送生效」，iOS 选择器的即时反馈与服务端延迟生效存在语义差——诊断与完成报告须写明，真机验收按「下一次回复用新模型」判定，避免争议。
- `list_agents` ✅ `GET /agent`。空数组合法；HTTP 失败 → ⛔。
- `set_agent_preset` ⛔（OpenCode agent ≠ dsh agentPreset）。
- `list_permission_modes` / `set_permission_mode` ⛔（除非活体证明与 bridge 三档同构）。旧包的 default/yolo **不要**复制。

#### 4.3.6 会话增删改

- `resume_session` ✅ = 对已有 id `StartSession` + 订 SSE（无进程语义）。
- `rename_session` ✅ 若对应 HTTP 存在（v2 有）；1.18 以活体为准，无则 ⛔。
- `delete_session` ⛔ 除非活体钉死 HTTP delete（禁止复制 `opencode session delete` CLI）。
- `archive_session` 2️⃣。
- `share_session` ⛔。
- `compress_context` 2️⃣ `POST …/compact`（有则接，无则 ⛔）。

#### 4.3.7 目录

- `list_projects` ✅ `GET /project`（不是 `/api/location`）。**字段映射（评审 S2 活体）**：元素是 `{id, worktree, vcs, time, sandboxes}`——目录建议取 **`worktree`** 字段（非 directory/path）。
- `list_directory` ♻️ 一期沿用 bridge 通用 FS。若 1.18 有可靠 fs.list 再 ✅ 并补测。完成报告必须写明走的是哪条。
- git 面（`get_git_context` / PR / `commit_and_push` / branch / worktree）⛔。

#### 4.3.8 其余 RPC（`handlers.go` 全表收口）

| RPC | 标记 | 说明 |
|---|---|---|
| `hello` | ♻️ | 与 backend 无关 |
| `run_diagnostics` | ✅ | 来源（现网/沙盒 URL）、API 代、health、默认模型是否在 catalog、loopback-only |
| `fetch_todos` | ⛔ | 二期 `GET /todo` |
| `get_usage` | ⛔ | 占用走 §4.3.2，不叫 get_usage |
| `list_memory_files` / `read_memory_file` | ⛔ | 类型断言自动 not_supported |
| `get_workspace_diff` / `get_turn_diff` / `get_full_thread_diff` | ⛔ | |
| `cancel_request_v1` | ♻️ | 连接级 |
| `check_pending_notifications` / prekey 三件套 | ♻️ | relay 通用 |
| `pin` 两件套 | ♻️ | §4.3.1 |
| 本地 search | ♻️ | iOS 本地，不接服务端 search |

### 4.4 安全

仅 loopback。凭据只在 data dir 0600 文件与进程环境，不进 git、不进 argv（对照 `RuntimeManager.processEnvironment`）。沙盒 json 与现网 json **文件名分开**（并存期）；退役后只留现网那一份。禁止 `--host 0.0.0.0`。

### 4.5 SSV2 真相清单

- **真相 owner**：`opencode serve`（会话、消息、模型、占用分母）。
- **唯一 writer**：serve 写自己的库；新包零直接写 sqlite/db。
- **事务域**：既有 pathless kernel；无新域。
- **新增路径**：可选 `opencode-web-managed-server.json`（仅沙盒）；退役后不再作为日常真值。
- **写入口**：create / prompt_async\|prompt / abort\|interrupt / permission reply。
- **失败**：无 URL→not_configured；模型不在目录→send 错误；空转→turn_error；权限超时不裁判。
- **防双写测试**：`import_guard_test.go` 扫新包源文件，禁止 `sqlite3`、`opencode session list`、`opencode run`、`opencode models`、`64667`。`go list` 断言 import 图不含 `agent/opencode`。

---

## 5. 与旧 `opencode` 并存

- 切换框两个入口。同一现网 URL 时列表是同一批 `ses_…`（两个客户端）。
- 旧入口脏行为（圈空、空转）**保持原样**，方便对照；不要顺手修旧包。
- 沙盒 URL 时两套会话，**不得**用沙盒绿替代 §6 现网矩阵。
- 用户从旧入口建的会话，新入口在现网模式下可打开（同一 serve）。
- 审批是同一把锁：网页或任一客户端先答即 resolved。

---

## 6. 测试与验收

**单测（httptest 假 serve，覆盖 1.18 裸数组 + 一组 v2 `{data}`）：**

| 用例 | 断言 |
|---|---|
| 探针选代 | `/global/health` 200 → gen=1.18；404 再 `/api/health` → gen=v2 |
| 401 + Basic Auth | 无密码失败；有密码通过 |
| 列表映射 | id/title/directory/updated；directory 头出现在请求 |
| 占用 tokens=0 | session.tokens 全 0 + message last assistant 有数 → used/window 仍出 |
| 占用无窗口 | last assistant 有 tokens 但 provider 无 limit → 返回 nil，不谎报 200k |
| 发送带 model | POST body 含 `model.id` + `providerID` |
| catalog 拦截 | 模型不在 `/provider` → 错误、**零** prompt POST |
| 零输出 idle | `session.status=idle` 且无 assistant part → `EventResult.Error` 含 `may be unavailable` |
| 多步不提前完成 | tool `time.completed` 不发 EventResult；只在 session.status idle 发 |
| 权限折叠 | allow→once（或活体钉死的 1.18 值）；deny→reject |
| import 守卫 | 新包 import 图不含 `agent/opencode`；源文件无 sqlite3/CLI 字符串 |
| InstanceStatus | 空 URL → not_configured；探针失败 → not_configured |
| relay 空闲超时（评审 M2-2） | `disablesRelayIdleTimeout("opencode-web")` 为 true（审批等待不被 60s 收口） |
| observation re-attach（评审 M3；可测形态二选一——二轮提示 2） | 名单含 opencode-web：**行为级测试**（重连后断言 re-subscribe 发生）或**将名单提为可测常量**再断言内容，实施时定 |
| switchDir 特判（评审 M2-1） | 四读方法携带 directory 头（httptest 断言请求头） |
| iOS if 比较类归组（评审 M1） | SessionsView/SidebarView/SessionManagement 的 `.openCodeWeb` 分支行为断言（项目合并、目录侧栏显示、agent 过滤） |

**回归：**

- `go test ./agent/opencode-web/... ./go-bridge/... -count=1`
- 旧 `./agent/opencode/...` **零失败增量**（`git diff --stat agent/opencode` 必须空）
- iOS `-only-testing:CCCodeTests/BridgeModelsTests` + `ChatViewModelSessionSyncV2Tests` 含 openCodeWeb
- `MacBridgeBehaviorTests` argv 含 `-opencode-web-url` 且 drivers 含 `opencode-web`

**真机（成熟 = 现网 serve，不是沙盒）：**

| # | 前提 | 动作 | 应看到 |
|---|---|---|---|
| 1 | 网页占用约十几万的会话 | iOS **OpenCode Web** 点 ⭕ | 有已用/窗口，不是暂无 |
| 2 | 同会话 | iOS 发短消息 | 有回复；坏模型明确失败 |
| 3 | 网页 `/server/.../session/...` | 对应目录打开 | 历史与网页一致 |
| 4 | Mac 网页打字 | iOS 已打开 | 旁观 |
| 5 | 切回旧 OpenCode | 同一 Mac | 旧入口仍可用（圈空等脏行为保持） |
| 6 | 工具触发审批 | iOS Allow/Deny | turn 继续；网页先答则 iOS 收口；**不会**自动 allow |
| 7 | （退役后，另任务）drivers 无 `opencode` | 冷启动 | 只有 OpenCode Web；现网口仍被拉起 |

---

## 7. 边界与退役

- 硬边界：不 import 旧包；不删旧包；不写用户 OpenCode db；agent 不 bind 端口；不把 location 当项目列表；不把 `…/context` 当占用；不把 `.openCode` 的自动批权限抄到新 kind。
- 兜底：可**复制**旧 `sse_subscriber` / `mapRichHistoryEntry` / HTTP helper 进新包。红线：复制后禁止两边改同一份；复制时删掉 CLI/sqlite/顶层 tokens/不带 model 的发送。
- 退役判据：§6 表 1–6 现网全 ✅，owner 书面确认后再摘入口（§4.2）。回滚 = drivers 加回 `opencode`。退役另立任务，不塞进本期默认完成定义。

---

## 8. 实施拆分（批准后走 exec-plan）

每项按 impl / tests / regression 三件套拆（与 dsh-web 队列同纪律）。完成定义写在该项测试栏，禁止「骨架里顺手做完发送」。

1. **骨架**：`agent/opencode-web` 注册、Client、双代探针、WireDescriptor、`InstanceStatus`/`not_configured`；`main.go` import + flags + `buildAgentOptions` + `detectAgentStatus` + `shouldStartPassiveSubscription`；`import_guard_test.go`。
2. **列表/历史/占用**：§4.3.1–4.3.2 HTTP 映射 + 占用公式单测（tokens=0 回落、无窗口不谎报）。
3. **SSE + SessionActivityProbing**：旁观、重连、零输出 turn_error、`/session/status` 三态、catalog 刷新信号。
4. **发送 + 模型**：create/prompt 带 catalog 模型；停止按代；catalog 校验；不声明附件。
5. **投影与 go-bridge 接线**：§4.3.2 表逐项 + §4.1.5 全表（含 M2-1 switchDir 特判、M2-2 relay 超时、M3 observation 名单）+ iOS `sessionSyncV2ProjectionBackend`；测试风格对齐 `TestDSHWeb*`。
6. **审批**：SSE→permission→折叠 reply；registry 判据；先答者得。问答保持 ⛔。
7. **iOS kind + protocol mirror**：§4.1.7 两类接线（switch 穷举 + **if 比较型 3 文件 ≥12 处**，M1）+ BridgeModelsTests + SSV2 测试；Mac drivers 并存；**不**删旧 kind。
8. **Release**：`/Applications` 覆盖安装；现网 URL 矩阵 §6 表 1–6；**不得**用沙盒绿宣称成熟。

---

## 9. 初稿对照（避免再退回调研结论）

| 初稿/讨论里出现过 | 正本 |
|---|---|
| 原地改 `agent/opencode`、不新开包 | **否**，新包新入口 |
| 再开包会「不能访问 4096」 | **否**，正是现网 URL 的客户端 |
| 新包自己 bind 4096/写死 4097 | **否** |
| 只抄 v2 `/api/*` | **否**，默认 1.18，v2 为次表 |
| 权限 allow/deny 当官方值 | **否**，v2 为 once/always/reject；1.18 先探 |
| `GET …/context` 或顶层 tokens 当占用 | **否**，抄网页 last assistant |
| 成熟 = 沙盒绿 | **否**，必须现网网页同一 serve |
| 退役删 `agent/opencode` | **否**，只摘入口 |
| 抄 `.openCode` 的 iOS 自动批权限 | **否**（坑 11） |
| 新包读 `opts["opencode_url"]` | **否**，读 `opencode_web_url` |
| New() 因 `opencode` CLI 不在 PATH 失败 | **否**，纯 HTTP |
| hydrate 复用 `streamOpenCodeRichHistoryProjectionEvents` | **否**，那条按 name=`opencode` 取 agent |
| `detectAgentStatus` 漏 case | **否**，默认会变成 Available |
| 广告 `todos` 却不实现 TodoProvider | **否**，一期不广告 |

给评审：先核 §2 坑表与 §3 双代表是否仍与本机 1.18 一致，再核 §4.1.5 / §4.1.7 接线点是否写全。不要在未读 1.18 活体的情况下把实施默认改回 checkout-only v2。

---

## 10. 评审采纳记录（v3 对照 `2026-08-18-opencode-web-backend-design-review.md`）

评审结论：修改后通过（3 必改 + 9 建议；无阻断）。§3 全部关键断言经活体+源码双重证实（载波 HTTP+SSE、双代 API 表 1.18 列、占用公式与回落、权限 v2 字面量、十三坑事实引用）。必改全为接线表补行——dsh-web M4 病的变体，且因 if 比较型无编译器兜底而更隐蔽。

| 项 | 处置 | 落点 |
|---|---|---|
| M1 iOS 表漏 if 比较型 ≥12 处/3 文件 | **采纳** | §4.1.7 重构为两类（switch 编译器强制 / **if 比较型须 rg 人工核对**）；补 SessionsView×7/SidebarView×3/SessionManagement×2 行为表（逐处归组=与 `.openCode` 同组）+ DirectoryPreferences 核对项 + 5 默认值文件注记；§6 加行为断言用例；§8-7 并入 |
| M2-1 dispatchRPC switchDir 特判 | **采纳** | §4.1.5 加行（特判扩含 opencode-web——四读方法切目录）；坑 5 行同步闭环；§6 加请求头断言 |
| M2-2 disablesRelayIdleTimeout | **采纳** | §4.1.5 加行（加入 case；注释即 dsh-web 真机同型故障）；§6 加用例 |
| M3 resubscribeObservationSessions | **采纳（决策：进名单）** | §4.1.5 加行（与 codex/claude 同待遇——外部 turn 旁观的 relay 重连 re-attach 依赖）；dsh-web 不在名单的既有疑似缺口如实上报 owner、不在本设计内修；§6 加重连用例 |
| S1 顶层 tokens 样板失真 | 采纳 | §3.6 修正（顶层无 `total`，message 级 `info.tokens` 才有；两级形状差异注明） |
| S2 /project 字段是 worktree | 采纳 | §4.3.7 字段映射 |
| S3 本机 1.18.18 双面共存 | 采纳 | §3.2 补注（`/api/*` 也 200；互斥依据=v2 无 `/global/health`；形状判据维持） |
| S4 1.18 二进制三字面量 | 采纳 | §3.4 置信注记（先探策略不变） |
| S5/S6 表外「无需改动」注记 | 采纳 | §4.1.5 两行注记（backend_capabilities 自动放行 / pin any-scope 差异如实） |
| S7 catalogCapabilityRequiredFor 危害描述失实 | **采纳（修正）** | 亲核源码：门控的是 `list_sessions` 对未协商 v2 的旧客户端（显式 wire 错误，非静默、非 list_models）；dsh-web 不在列且正常——同判**不加入**，原行作废 |
| S8 switch_model pending UI 语义 | 采纳 | §4.3.4 注记（下次发送生效；验收判定口径） |
| S9 坑 9 引用补注 | 采纳 | §2.1 坑 9（main 已修复 deepSeekWeb；openCodeWeb 同样需加） |

不采纳项：无。

---

## 11. 二轮评审采纳记录（v3.1 对照 `2026-08-18-opencode-web-backend-design-review-r2.md`）

二轮结论 **APPROVE（可交付 owner 终审）**：v3 diff 逐项核验通过；**S7 亲核修正经独立源码复核属实**（`catalogCapabilityRequiredFor` 唯一调用点 `handlers.go:1011`，门控 list_sessions 对未协商 v2 旧客户端、返回显式 `protocol.capability_required`——「落实修订时亲核源码」的正面示范）；audit-plan 背书盘点通过（v3 全部内容形状断言均有活体/源码证据，无「描述了但无样本」项）。十三坑全数完整闭环；一轮 3 必改 + 9 建议全部闭环。

三条实施期提示（不阻塞批准）处置：

| # | 提示 | 处置 |
|---|---|---|
| 1 | 行号基线漂移（Phase 5 提交已移动 handlers.go 行号） | §4.1.5 前言补注；实施按 `rg` 符号复核（设计本有此声明，强化提示） |
| 2 | M3 名单为函数内硬编码数组，§6 断言需可测形态 | §6 用例行改「二选一」：行为级测试 / 名单提为可测常量，实施时定 |
| 3 | dsh-web 既有缺口（resubscribe 名单缺 dsh-web）是否在其队列补一行 | **owner 决策项**，与本设计实施互不阻塞——已在 M3 行与本文记录，待 owner 裁决 |

实施期挂账（不变）：1.18 权限字面量先探、rename/delete 活体钉死、双代探针进诊断。
