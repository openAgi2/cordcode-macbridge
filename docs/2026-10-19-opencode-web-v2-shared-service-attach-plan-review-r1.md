# 评审报告 r1：OpenCode Web 接入 v2 共享服务（generation-2 attach）设计方案

- 评审轮次：第 1 轮（首轮；resume 场景无历轮意见）
- 评审员模式：延续（本轮即首任，无前序上下文需要复用）
- 评审日期：2026-10-19
- verdict：**REVISION_REQUIRED（不通过，需修订）**
- blockers：3（F-1、F-2、F-3）；advisories：6（F-4 ~ F-9）
- 契约版本：plan-contract-v1.1（plan-review skill，含 audit-plan 专项接入）

## 1. 结论与实施就绪

- verdict: REVISION_REQUIRED
- plan: `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.0，SHA-256
  `fb79e6af86481a0677eccfa300a99bd3299918279152f7a309b9a7ed26c82e44`（与派发哈希一致；本轮全量评审）
- 总体判断：**架构方向成立、复用调查扎实、证据门框架正确**。上游 2.0.32 的
  服务发现/注册/单例仲裁/端点契约锚点绝大多数亲核成立；E-1 经本轮活体复测再次
  确认。但存在三个必须修订的问题：目录路由锚点混用且 A.4/E-7 初始证据链是
  no-op 探测（F-1）；attach→回退/轮换缺失真实触发机制、验收标准 4 按现设计
  不可达（F-2）；S-2 依赖列与 Gate A 自相矛盾、隔离放行可被切片表绕过（F-3）。
  三者修复都不动架构主干，属文档级修订。
- implementation_readiness（修订通过后）：
  - 可开工（受既定门约束）：S-1 的 resolver 单测部分（E-1 verified + OD-1）、
    S-2 的 probe 端点替换部分（依赖拆分后）、S-7 文档部分。
  - 仍受证据/决定门阻塞：S-3（E-2）、S-4（E-4）、S-5（E-3/E-5）、S-6（OD-5 + E-7）、
    v2 解除隔离整体（E-2/E-3/E-5）、OD-2b（ensure-start，owner 未裁决）。
  - 评审通过本身不构成实施授权（方案 §6 已如实声明）。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 核对结果 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 仅未跟踪 `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`（即被审方案） | 与方案 §2.1 一致 |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净（0 项） | 与方案 §2.1 一致 |
| opencode（旧 checkout） | `/Users/jacklee/Projects/opencode` | `dev`（HEAD→dev, origin/HEAD） | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | 未核对（方案声明不作为锚点） | 提交前缀一致；**分支名与方案 §2.1 所写 "main pr-56106-…" 不符**（见 F-4 备注，不影响任何结论，该行本就声明不用） |

运行身份（本轮活体复核，2026-10-19）：
- v2 共享服务 `127.0.0.1:49374` 在听（进程名 `opencode-…`，PID 29415）；
  `~/.local/state/opencode/service.json` 存在，键 `{id,password,pid,url,version}`，
  `version=2.0.32`、`url=http://127.0.0.1:49374`、`pid=29415`（与监听进程一致）。
- v1 managed `127.0.0.1:4096` 在听（PID 28164，与方案 A.1 的 PID 一致）。

### 2.2 检查范围

- 全量评审（简报未限定范围）。上游锚点逐条亲核（§2.2 复用表全部行、§2.3 现状表
  全部行、§3 各节引用、附录 B 事件名抽样核对声明源码）；think.md 五处、
  GO_BRIDGE_ARCHITECTURE.md 锚点表行、四份被引 docs 存在性均核。
- 本轮新增的活体复核（只读 GET，不打印密码）见 §3。未重做/无法重做：
  附录 A 的历史事件细节（A.2 的 507 条会话内容、A.5/A.6/A.7 的过去时间窗）——
  属过去时观测，本轮以架构与现存状态侧证，未逐条复演；`/api/event` SSE 实流
  捕获未做（需发起真实 turn，属 E-2 实施期捕获，本轮不越权制造）；OD-2b 的
  desktop staged 二进制解析顺序未核（owner 未裁决项）。
- audit-plan 专项：**已运行**。逐内容类型核对了方案的外部形状声明（service.json、
  /api/info、/api/health 缺席、目录路由参数、各读路径 envelope、prompt payload
  可选 id、permission/form 路由、事件名），其中两项发现证据问题（F-1、F-5）；
  E-2/E-3/E-4/E-5 保持 pending 且实现被正确 gate（方案未把未采样形状写成事实，
  附录 B 自标「源码声明版」）。

### 2.3 锚点核验表（本轮亲核的主干；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位/语义 | 备注 |
| --- | --- | --- | --- | --- |
| 服务发现协议 `service-probe.ts:44-80`：读注册→GET /api/info→校验 {version,pid}；404=协议不兼容 | opencode-v2 `packages/client/src/service-probe.ts:49-90` | `:52 auth: … username: "opencode", password: info.password`；`:55 fetch(new URL("/api/info", info.url)…)`；`:65 if (response.status === 404) return {…compatible: false…}`；`:78 if (serverInfo.pid !== info.pid) return {service: undefined…}` | 定位漂移（44-80 vs 49-90）/ **语义成立** | 行号差记入 F-4 |
| `fallback()` `:53-55` 定位注册文件（XDG_STATE_HOME 可覆盖） | 同文件 `:39-41` | `:40 join(process.env["XDG_STATE_HOME"] ?? join(homedir(), ".local", "state"), "opencode", "service.json")` | 定位漂移 / 语义成立 | F-4 |
| 注册文件契约 `service.ts:43-51`（Info：id/version/url/pid/password） | `packages/client/src/service.ts:46-57` | `:46-57 export type Info = { id?… version?… url: string; pid: number; password?… }` | 定位漂移 / 语义成立 | 活体 service.json 键集一致 |
| 0600、temp+rename 原子写 `service-registration.ts:23-33` | `packages/cli/src/services/service-registration.ts:39` | `:39 yield* fs.writeFileString(temp, encoded, { mode: 0o600 }).pipe(Effect.andThen(fs.rename(temp, options.file)))` | 定位漂移（实际 :39）/ 语义成立 | F-4 |
| 每 5s 回读被替换即自杀 `:44-56`；正常退出删文件 `:57-61` | 同文件 watch `:40-67`（`Schedule.spaced("5 seconds")` 在 `:63`，`Effect.filterOrFail(owns)` `:62`，shutdown `:65`）；退出删文件 `:68-71` | `:63 Effect.repeat(Schedule.spaced("5 seconds"))`；`:69 owns(found) ? fs.remove(options.file) : Effect.void` | 定位漂移 / 语义成立 | F-4 |
| 固定端口 `service-config.ts:56`：latest 默认 0xC0DE=49374 | `packages/cli/src/services/service-config.ts:56` | `:56 if (channel === "latest" || … ) return 0xc0de` | **定位准确 / 语义成立** | |
| 身份端点 `server.ts:43-49`、ServerInfo `:5-19` | `packages/protocol/src/groups/server.ts:43-51`、`:5-19` | `:43 HttpApiEndpoint.get("server.info", "/api/info", { success: ServerInfo })`；`:5-19 version/pid/urls/paths/capabilities` | **定位准确 / 语义成立** | 活体响应逐字段吻合 A.3 |
| 目录路由 `location.ts:74-75`：`location[directory]` query 或 `x-opencode-directory` header | `packages/server/src/location.ts:71-79` | `:74 query.get("location[directory]") ||` `:75 (request.headers["x-opencode-directory"] ? decode(…) : process.cwd())` | **定位准确 / 但方案用它背书了另一个机制（见 F-1）** | /api/session 列表过滤另有独立字段 |
| （本评审补充的正确锚点）/api/session 列表 directory 过滤 | `packages/protocol/src/groups/session.ts:168-176` | `:168 export const SessionsQuery = Schema.Struct({` `:170 directory: AbsolutePath.pipe(Schema.optional),` | 方案未引用此行 | F-1 修订落点 |
| 事件流 `event.ts:44-56`：/api/event SSE、跨 location、volatile | `packages/protocol/src/groups/event.ts:44-52` | `:44 HttpApiEndpoint.get("event.subscribe", "/api/event", { success: HttpApiSchema.StreamSse(…) })`；`:51 "…across all server locations. Volatile by contract: a slow consumer overflows and fails the stream, and events during disconnection are missed."` | **定位准确 / 语义成立** | |
| 事件全局 id `event.ts:9-13` | 同文件 `:8-12` | `:9 id: Event.ID`（rpcEvent 另有 `:15-21`） | 基本准确 / 语义成立 | |
| prompt `session.ts:396-410`：payload 含可选 caller `id` | `packages/protocol/src/groups/session.ts:396-407` | `:396 HttpApiEndpoint.post("session.prompt", "/api/session/:sessionID/prompt"`；`:399 id: SessionMessage.ID.pipe(Schema.optional)`；`:401-403 metadata / delivery / resume` | **定位准确 / 语义成立**（stable messageID 复用官方能力的声明成立） | |
| interrupt `session.ts:746` | 同文件 `:746` | `:746 HttpApiEndpoint.post("session.interrupt", "/api/session/:sessionID/interrupt"` | **定位准确 / 语义成立** | |
| switchModel/switchAgent `session.ts:330-346` | 同文件 `:330`/`:346` | `:330 …"/api/session/:sessionID/agent"`；`:346 …"/api/session/:sessionID/model"` | **定位准确 / 语义成立** | |
| 会话 CRUD `session.ts:182,221,287,300,362`；messages `message.ts:68` | 同文件 `:182/:221/:287/:300/:362`；`message.ts:68` | `:182 GET /api/session`、`:221 POST /api/session`、`:287 GET /api/session/:sessionID`、`:300 DELETE`、`:362 PATCH`；`message.ts:68 GET /api/session/:sessionID/message` | **全部定位准确 / 语义成立** | |
| permission `permission.ts:23-124`、reply `:117` | `permission.ts:23-124`，reply 在 `:117` | `:117 HttpApiEndpoint.post("session.permission.reply", "/api/session/:sessionID/permission/:requestID/reply"` | **定位准确 / 语义成立** | |
| question/form `form.ts:12`（GET /api/form） | `form.ts:12`（全局列表）；**应答路由在 session 组** `session.ts:796-870` | `form.ts:12 GET /api/form`；`session.ts:842 HttpApiEndpoint.post("session.form.reply", "/api/session/:sessionID/form/:formID/reply"` | 定位准确但**不覆盖应答路径** | F-6 |
| project registry `/api/project` | `project.ts:6,17` | `:6 const root = "/api/project"`；`:17 HttpApiEndpoint.get("project.list", root` | **定位准确 / 语义成立** | 活体返回**裸数组**（F-5） |
| v2 无 todo 路由组 | `packages/protocol/src/groups/` 目录列举 | 目录无 todo.ts（29 个组文件逐一列举核对） | **成立** | OD-6 依据成立 |
| v2.0.32 无 /api/health | 全仓 grep + 活体 404 | 服务端 `packages/server/src` grep 无命中；唯一命中在 `packages/stats/app`（独立 stats web app，非 serve 路由）；活体 `/api/health`→404 | **结论成立**（「全仓 grep 无」表述略过强，F-4 备注） | |
| awaitPlugins 5s 门 `location.ts` | `packages/server/src/location.ts:36-47` | `:37-38 Effect.timeoutOrElse({ duration: "5 seconds"` → ServiceUnavailableError | **定位准确 / 语义成立** | E-8 依据成立 |
| 附录 B 事件名（session.created/renamed/deleted/metadata.updated、message.content.updated、reasoning.started/delta/ended、execution.started/succeeded/failed/interrupted、inbox.enqueued/delivered/cancelled、model.selected/agent.selected、permission.asked/replied、form.created/replied/cancelled） | `packages/schema/src/session-event.ts:49-455`、`permission.ts:45-47`、`form.ts:173-175` | 逐名 grep 命中（如 `:144 type: "session.message.content.updated"`、`:431 "session.reasoning.started"`、`:209 "session.inbox.delivered"`） | **语义成立**；两处名称不精确：实际为 `session.compacted`（compaction-event.ts:7，非 `session.compaction.*`）与 `filesystem.changed`（filesystem.ts:9，非 `FileSystem.Event.*` 字面量） | 附录 B 自标源码声明版+E-2 gate，仅记 F-4 备注 |
| probe v2 探测依赖 /api/health `probe.go:174` | 本仓 `agent/opencode-web/probe.go:174` | `:174 v2Health, v2Detail := probeHealth(ctx, c, "/api/health")` | **定位准确 / 语义成立**（改动必要性成立：活体两健康路由皆 404 时现探测必然失败） | |
| shape arbiter `probe.go:196-214`；probe 框架 `:130-229` | `probe.go:196-225`；`:125-232` | `:197-201 shapePath := "/session" … "/api/session"` | 基本准确 / 语义成立 | |
| v2 SSE 分支已删、只留 /global/event `events.go:142-145` | `agent/opencode-web/events.go:142-145` | `:142-144 // C1: the verified 1.18.18 event stream is /global/event only; the former v2 /api/event branch is deleted` | **定位准确 / 语义成立** | |
| 订阅器生命周期 `events.go:126-200`（heal/recover） | `events.go:125-203` | `:182 s.healArmedTurnsAfterDrop()`；`:198 s.recoverPendingAfterReconnect()` | 基本准确 / 语义成立 | |
| quarantine 文案 `opencodeweb.go:332` | `opencodeweb.go:328-336` | `:331-333 unsupportedGenerationDetail … "unsupported-generation (quarantined)…"` | 基本准确 / 语义成立 | |
| Swift 槽位 `OpenCodeEndpointResolver.swift:25-27` | 同文件 `:23-28` | `:25 case serviceDiscoveryFuture = "service_discovery_future"` | **定位准确 / 语义成立** | |
| flag 枚举 `main.go:110` | `go-bridge/main.go:110` | `:110 flag.String("opencode-source", "", "…(managed_local, external_http, legacy_64667, service_discovery_future, disabled)")` | **定位准确 / 语义成立** | |
| env 通道 `main.go:97-103` | `go-bridge/main.go:104-106` | `:104-106 OPENCODE_WEB_BASE_URL / OPENCODE_WEB_SERVER_USERNAME / OPENCODE_WEB_SERVER_PASSWORD` | 定位漂移 / 语义成立（三个 env 名与 §3.1 完全一致） | F-4 |
| resolve guard `RuntimeManager.swift:1157-1161`；startOpenCodeManagedServer `:1264`；syncDesktopConfig 调用 `:1198` | 逐行核对 | `:1157 func resolveManagedOpenCodeIfNeeded()`；`:1198 _ = openCodeManagedServer?.syncDesktopConfig(`；`:1264 func startOpenCodeManagedServer() async` | **三处定位准确 / 语义成立** | |
| managed spawn/adopt `OpenCodeManagedServer.swift:238-296/:318-344/:385-388` | `:239 ensureRunningOnQueue`、`:321 syncDesktopConfig`、`:385-388 adoptExistingProcessIfHealthy` | 逐段核对 | 基本准确 / 语义成立 | |
| catalog 单目录失败整轮失败 `sessions.go:137-142` | `agent/opencode-web/sessions.go:137-143` | `:139-141 // Any bucket failure fails the whole global list — never a partial catalog (directive-003 OD-2).` | **定位准确 / 语义成立**（directive-003 引用同源核实） | |
| `.dat` persist `desktop_home_projects.go:26-32` | `:24-33` | `:30-31 …ai.opencode.desktop/opencode.global.dat…` | 基本准确 / 语义成立 | |
| stable messageID `session.go:161-230/:223-230` | `:161-163 Send`、`:169-175 newMessageID`、`:223-229 SendWithOptions` | 逐段核对 | **定位准确 / 语义成立** | |
| 旧 v2 permission 分支 `permissions.go:104` | `permissions.go:104` | `:104 if c.Generation() == generationV2 {` | **定位准确 / 语义成立** | |
| fingerprint/seen `session_discovery.go:403-418` | **`go-bridge/session_discovery.go:403-433`**（根包，非 agent/opencode-web） | `:412 prev, hadPrev := seen[id]`；`:423 if current != prev { … sessions_changed` | 语义成立 / **缺包路径致定位歧义** | F-8 |
| think.md `:334/:852/:1799/:2714-2716/:2779` | 逐行核对 | 五处标题/行文与方案引用语义一致（`:852` 行含「opencode-web 待补」） | **全部成立** | |
| GO_BRIDGE_ARCHITECTURE.md 锚点表 opencode-web 行待补 | `GO_BRIDGE_ARCHITECTURE.md:87` | `:87 | opencode-web | 官方 serve Web API / SSE | **待补**…` | **成立** | |

未核/低风险未核项：InstalledView 行文案现状（展示层，S-1 实现细节）、附录 A 历史时间窗
细节（见 §2.2）。均不影响本轮结论。

## 3. 活体复核（audit-plan 专项证据；本轮 2026-10-19 只读探测，脱敏）

所有请求为只读 GET；Basic 凭据从 service.json 读入后仅入请求头，不出现在任何输出。

1. **E-1 复测通过**：`/api/info` 无认证 → `401`；带 Basic（`opencode:<password>`）→ `200`，
   body：
   `{"version":"2.0.32","pid":29415,"urls":["http://127.0.0.1:49374"],"paths":{"tmp":"/private/var/folders/…/T/opencode"},"capabilities":{"persistentPty":true}}`
   ——与附录 A.3 脱敏样本逐字段一致；pid 与 service.json 及监听进程一致。
   `/api/health`（带认证）→ `404`（佐证 v2.0.32 无该路由）。
2. **目录参数矩阵**（`/api/session`，全部 200，body 长度与 md5 对账）：

   | 探测形式 | 结果 |
   | --- | --- |
   | `?location[directory]=<TCC>` | md5 `db90e583f8`，n=50 |
   | `?x-opencode-directory=<TCC>`（A.4 字面形式） | md5 `db90e583f8`，n=50 |
   | header `x-opencode-directory: <TCC>` | md5 `db90e583f8`，n=50 |
   | 无任何目录参数 | md5 `db90e583f8`，n=50 |

   四者**字节级相同**；会话 location 直方图显示 50 条来自 10 个不同目录 ——
   **该端点对上述三种形式一律忽略目录、返回全局列表**（首屏 50 条为分页默认上限）。
3. **正确的列表过滤形式**：`?directory=<TCC>` → 200，n=1，全部会话 location 均为
   `~/Documents/todo-app-sisyphus`；`?directory=<macbridge-worktree>` → n=0；
   `?directory=/不存在` → 200 n=0（不报错）。与 `SessionsQuery.directory`
   （session.ts:168-176）一致。
4. **TCC 目录经 v2 服务可达（E-7 结论本身成立，但需换正确探测形式，见 F-1）**：
   `?directory=<TCC>` 返回该目录真实会话 1 条。
5. **scoped 路由不带目录上下文可用**：`GET /api/session/<TCC 会话 id>` 与
   `/api/session/<id>/message` 无目录参数/带 header 均为 200（同 body）。
6. **envelope 按路由分型**：`/api/session` → `{data:[…], cursor:{…}}`（无 location）；
   `/api/model` → `{location:{directory:"/Users/jacklee"}, data:[…157 项]}`；
   `/api/project` → **裸数组**（27 项，`[{"id":…,"canonical":"/Users/jacklee/Projects/cordcode-ios",…}]`）。

## 4. 意见

### F-1 [阻塞] 目录路由锚点混用，且 A.4/E-7 初始证据是 no-op 探测（错误归因）

- 位置：§2.2「目录路由」行；§2.3 第 2 行引用 client.go 注释；附录 A.4；E-7 行。
- 证据：
  - 方案用 `location.ts:74-75`（location 中间件：`location[directory]` query /
    `x-opencode-directory` header）背书 client.go「v2 仅 /api/session 列表带
    query.directory……与官方 2.0.32 源码一致，保留」。实际 /api/session 的目录
    过滤是另一机制：协议声明 `SessionsQuery.directory`（session.ts:168-176），
    与中间件互不相关。client.go 发 plain `directory` 恰好命中后者，行为结论碰巧
    正确，锚点张冠李戴。
  - 同行背书「header 是 1.18 惯例不上 v2 wire」——官方 2.0.32 **服务端**源码
    location.ts:75 明确读取 `request.headers["x-opencode-directory"]`。「官方
    client SDK 不发送」是客户端行为事实，不是「不在 v2 wire 上」的服务端契约
    事实；方案在同一行内自相矛盾。
  - A.4「同一目录经 v2 服务 `/api/session?x-opencode-directory=…` → 200（E-7
    初证）」：本评审 §3.2 矩阵证明该 query 形式被服务端忽略（与无参 baseline
    字节级同 md5），200 只证明全局列表可读，**不证明 TCC 目录可达**。这是
    audit-plan 定义的典型错误归因（数字对、故事错）。
- 影响：S-4/S-5/S-6 的目录语义锚点基础；E-7 作为 S-6 / OD-5 分支 gate 的初始
  证据条目无效；按「header 不上 v2 wire」的信念实现 scoped 写路径时会丢失一个
  官方支持的上下文通道（本轮实测 scoped GET 不带目录也可用，风险被降低但不为零：
  写路径未实测）。
- 修订方向（最小充分）：
  1. §2.2 该行拆成两条机制并各给正确锚点：列表过滤 =
     `session.ts:168-176 SessionsQuery.directory`（plain `directory` query）；
     scoped 路由 location 上下文 = `location.ts:71-79 requestRef()`
     （`location[directory]` query 或 `x-opencode-directory` header，缺省
     process.cwd()）。改写「header 不上 v2 wire」为「官方 v2 client SDK 不发送
     header；服务端在 location 中间件中接受它；CordCode 跟随官方 client 形状」。
  2. 按共享约定「证据更正」节登记 A.4/E-7 更正：原条目标注探测形式无效及原因
     （no-op 参数、与 baseline 同 md5），替换为正确形式
     （`?directory=<TCC>` → 200 n=1 全为该目录会话；本评审 §3 数据可复用，
     作者复核后入档并标注捕获时间/版本）。
  3. E-5/S-5 的写路径样本捕获计划中补一条：scoped 写（prompt/interrupt/permission
     reply）在带与不带目录上下文两种形式下各采一例，确定最小必带参数。
- 闭合标准：锚点并排可核（列表过滤与中间件两机制各自成行）；A.4 更正记录在案
  且 E-7 断言引用正确探测形式；写路径目录上下文进入 E-3/E-5 捕获断言。

### F-2 [阻塞] attach→回退/自动回附缺少真实触发机制，验收标准 4 按现设计不可达

- 位置：§3.2「服务替换/凭据轮换」段；§1 验收标准 4；§5 S-1 验证列；E-6。
- 证据：方案声称「driver 侧探活失败 → backend 报 not_configured/unavailable
  （既有语义）→ Swift 的 runtime 监控重启 runtime 时重新解析（复用既有 restart
  路径，不新增轮询器）」。亲核 RuntimeManager.swift：现有重启触发只有
  (a) crash 重试（`:687-690`，maxCrashRetries=3）；(b) 120 分钟兜底定时重启
  （`:745-766`，autoRestartIntervalMinutes 默认 120）；(c) 显式 `restart()`
  （`:458`，配置变更/UI 动作）；(d) `fileReadHealth.restartRecommended`
  （`:1015-1017`，仅文件读取健康，来源 management_api.go:529，与 backend
  可用性无关）。`resolveManagedOpenCodeIfNeeded()` 仅在 start()/apply 路径
  调用（`:536`/`:581`），无周期调用。backend 报 unavailable 不触发任何上述
  路径；endpoint 经 env 注入 runtime 后（main.go:104-106），不重启 runtime
  就无法切换服务（方案自己的不变量也禁止 Go 侧自行切换）。
- 影响：desktop 退出后，attach 模式 runtime 携带死 URL 运行最长 120 分钟，
  验收标准 4「退出 desktop → MacBridge 回退 4096 并如实显示；重启 desktop →
  自动回到 attach」无机制可达；S-1 验证列「desktop 开/关两态 来源切换正确」
  同样悬空。E-6 只 gate「轮换自动恢复」验收，未覆盖回退时延契约。
- 修订方向（二选一，均最小改动）：
  1. 补一个真实触发器并给锚点：Swift 已周期轮询 management status
     （RuntimeManager.swift:1005 一带 `latestManagementStatus = resp`）——在该
     状态面暴露 opencode-web backend 的 `source` + not_configured/unavailable
     （方案 §3.2 本就计划暴露 source），Swift 判定「attach 模式下 backend 探活
     失败」→ 走既有 `triggerAutoRestart`/`restart()` 链路重新解析；或
  2. 与 owner 明确回退时延契约（「最长 120 分钟兜底或手动重启后切换」），把
     验收标准 4 与 S-1 验证列改写成与契约一致的可验收表述（明示「重启
     runtime/点击刷新后」而非「自动」）。
- 闭合标准：触发机制有本仓锚点可依并写入 §3.2 与 S-1，或时延契约明确落入
  验收标准 4；E-6 的验收范围与该机制对齐。

### F-3 [阻塞] S-2 依赖列与 §3.3/§4 Gate A 矛盾，隔离放行可被切片表绕过

- 位置：§5 S-2 行（依赖/前置列）vs §3.3 第三段、§4 Gate 映射第一条。
- 证据：§3.3「放行开关与 E-2/E-3/E-5 样本 capture 绑定（Gate A），未齐样本前
  v2 仍隔离」；§4「v2 解除隔离整体依赖 E-2/E-3/E-5 齐备（样本未齐前 quarantine
  保持）」；但 §5 S-2（其交付物就含「v2 条件解除隔离」）依赖列只写
  「S-1（source 可达）；E-8 重试边界」。
- 影响：按切片表字面实施可在 E-2/E-3/E-5 未齐时放行 v2 —— 正是 §3.3 要防的
  「探测到 v2 但映射未验证」半成品产品面；门控一致性被切片表打穿。
- 修订方向：S-2 依赖列补 E-2/E-3/E-5；或拆片——S-2a「probe 换 /api/info」
  （依赖 S-1/E-8）+ S-2b「解除隔离」（依赖 S-2a + E-2/E-3/E-5），后续切片
  对 S-2 的引用相应改指 S-2a。
- 闭合标准：§3.3/§4/§5 三处一致，无法按切片表在样本未齐时放行。

### F-4 [建议] 上游行号漂移清单（语义均已核实成立）

- 位置：§2.2/§2.3/§3.9 多处。
- 证据与修订：`service-probe.ts:44-80`→实际 `:49-90`；`fallback() :53-55`→`:39-41`；
  pid 校验 `:72-76`→`:78-79`；`service.ts Info :43-51`→`:46-57`；
  `service-registration.ts:23-33`（0600+rename 实际 `:39`）、`:44-56`（watch
  实际 `:40-67`）、`:57-61`（退出删文件实际 `:68-71`）；
  `server-connection.ts:34-58`（resolve 实际 `:21-51`，ensure 三态判定另见
  service-probe.ts `decide()` `:22-32`）；`main.go:97-103`→实际 `:104-106`。
  另两处表述微调：「全仓 grep 无 /api/health」实际有一处命中于独立 stats web
  app（packages/stats/app），应写「serve 服务端无该路由」；附录 B 事件名对齐
  声明字面量（`session.compacted`、`filesystem.changed`）。§2.1 旧 checkout
  分支名与实际不符（实际 `dev`，非 "main pr-56106-…"），该行声明不作锚点，
  顺手更正即可。
- 闭合标准：修订版锚点行号与摘录可直接对上；不改变任何结论。

### F-5 [建议] 「{location, data} 统一解包…扩展到全部 v2 读路径」与真实形状不符

- 位置：§3.5 第一条。
- 证据：本轮活体（§3.6）：`/api/session` → `{data,cursor}`（无 location）；
  `/api/model` → `{location,data}`；`/api/project` → **裸数组**（27 项）。
  `unwrapDataEnvelope` 是「descends when present」（sessions.go:260-262），
  裸数组路径行为需在实现中明确。
- 影响：S-6 以 /api/project 为 membership 源，按「统一 envelope」预期实现会解析
  失败；session 列表的 cursor 分页字段也未在方案提及（列表分页是本仓另一条
  既有纪律线）。
- 修订方向：§3.5 改为按路由分型描述三类形状并附本轮活体证据；S-6 验证列补
  「project 裸数组 + session 列表 cursor 分页」两条断言。
- 闭合标准：§3.5 与活体形状一致，S-6 断言覆盖裸数组与分页。

### F-6 [建议] form/question 应答锚点不完整

- 位置：§2.2「permission/question」行、§3.6、E-5。
- 证据：方案引 `form.ts:12`（全局 `GET /api/form`）；实际应答路径是 session 作用域
  form 组：`session.ts:796-870`（`GET/POST /api/session/:sessionID/form`、
  `…/form/:formID/reply`、`DELETE …/form/:formID`）。
- 影响：E-5 只按 form.ts:12 捕获会漏掉 reply wire；S-5 实现会找错路由。
- 修订方向：§2.2/§3.6/E-5 补 session 作用域 form 路由锚点，捕获计划加入
  `form.created → reply → form.replied` 一条完整流。
- 闭合标准：E-5 断言覆盖 session 作用域 form reply。

### F-7 [建议] §2.4 与 A.2 同一样本数字不一致（1.97MB vs 27MB，均称 507 条）

- 位置：§2.4「消息历史读取」行 vs 附录 A.2。
- 证据：§2.4 写「附录 A：1.97MB/507 条」，A.2 写「507 条/27MB 量级」。
- 影响：同一样本两处大小差 13 倍；若是响应体 vs 库表两个口径，未写明。
- 修订方向：统一口径（响应体字节数 / 会话条数 / 库表规模分别标注），或删一处。
- 闭合标准：两处数字一致或口径注明。

### F-8 [建议] session_discovery.go 锚点缺包路径

- 位置：§3.4「复用 session discovery 的 fingerprint/seen 机制，
  `session_discovery.go:403-418`」。
- 证据：该文件在 `go-bridge/session_discovery.go`（根包），机制实际在
  `:403-433`；§3.4 邻近引用全是 agent/opencode-web 文件，易误导实施者在错误
  包里找。
- 修订方向：补全路径 `go-bridge/session_discovery.go:403-433`。
- 闭合标准：锚点含包路径且行号覆盖 fingerprint 比较（:412-433）。

### F-9 [建议] OD-1/OD-3/OD-4/OD-6 的「decided」应标明决定来源是方案推荐

- 位置：§3.10 OD 表状态列。
- 证据：四个 OD 标 decided，来源是作者推荐（「owner 可在评审中改」）；任务简报
  也只把 OD-2/OD-5 列为 pending 待 owner。产品语义级决定（OD-6 能力下调、
  OD-3 4096 去留、OD-5 语义修订）按共享约定应有明确决定来源。
- 修订方向：状态列改写为「decided（方案推荐默认；owner 评审/实施前可推翻）」
  或在本轮评审后由 owner 表态回填；不阻塞设计本身。
- 闭合标准：每个 decided OD 的决定来源可追溯。

## 5. 复审处置与剩余门

- 本轮为首轮，无原意见闭合表。F-1/F-2/F-3 为阻塞，F-4~F-9 为建议（不构成
  通过条件，但建议随阻塞项一并修订以省轮次）。
- 仍待证据/决定/授权（与阻塞意见分列）：
  - 证据门（方案已正确登记为实施期捕获）：E-2（SSE 帧形状）、E-3（prompt 形状，
    含目录上下文矩阵——F-1 扩充）、E-4（parts schema）、E-5（permission/form
    wire，含 session 作用域 form——F-6 扩充）、E-6（注册轮换复现）、E-7（TCC
    可达矩阵，证据形式更正后）、E-8（503 重试边界）。
  - 产品决定门：OD-2b（ensure-start，owner 未裁决）、OD-5（fail-all vs 跳过+
    告警，owner 未裁决）；F-9 的四个推荐默认建议 owner 顺带确认。
  - 授权边界：本方案任务仅写方案+评审；实施（S-1~S-8）需 owner 另行启动
    exec-plan（方案 §6 已声明）。E-6/E-7 的复现动作涉及替换本机 live 服务，
    实施期执行时按方案登记的取证方式与清理纪律进行。
- 外部格式专项（audit-plan）结论：**未完全通过**——service.json、/api/info、
  /api/health 缺席、目录参数语义、project/model/session envelope、prompt 可选
  id、事件声明名均已核实（本报告 §2.3/§3）；A.4 一项证据归因错误（F-1），
  E-2/E-3/E-4/E-5 依约 pending 且实现被 gate。S-3/S-4/S-5 与 v2 解除隔离在
  对应 E 满足前不可实施（与方案 §4 一致）。

## 6. 结论

方案的方向、复用策略、证据门框架与来源纪律都显著高于本仓历史首版方案的
水准；三处阻塞均为可定位、可最小修订的文档级缺陷（锚点/证据更正、触发机制
补锚点或改契约、切片依赖一致性），不需要推翻任何架构决定。修订后预期可进入
下一轮复核；复核范围建议：F-1/F-2/F-3 的修订段落 + 全局门控关系重查
（OD/E/S 引用未受修订影响的按本轮核验复用，复用单元为命题/锚点粒度）。
