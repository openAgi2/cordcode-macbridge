# 评审报告 r2：OpenCode Web 接入 v2 共享服务（generation-2 attach）设计方案

- 评审轮次：第 2 轮（复审；resume 场景第 1 轮起跑，历轮 r1）
- 评审员模式：延续（与 r1 同一评审员身份，持有 r1 全部上下文；独立性限制见 §2.4）
- 评审日期：2026-10-19
- verdict：**APPROVED（通过）**
- blockers：0；advisories：4（F-10 ~ F-13，均为实施期吸收项，不构成通过条件）
- 契约版本：plan-contract-v1.1（plan-review skill，含 audit-plan 专项接入）

## 1. 结论与实施就绪

- verdict: **APPROVED**
- plan: `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.1（r1 修订版），
  SHA-256 `ceb463b079e08e57efc442c25b02c47338351e39380c8d1e8c5cd4826d0c7ecb`
  （与派发哈希一致；本轮为修订段定向复核 + 全局门控/引用全量重查，见 §2.2）
- 总体判断：**r1 三项阻塞（F-1/F-2/F-3）全部闭合且证据充分**；六项建议（F-4~F-9）
  全部闭合。修订未引入回归：S-2a/S-2b 拆片后 §3.3/§4/§5 三处门控一致，无法按切片表
  在样本未齐时放行 v2；OD×E×S 关系重查无悬空引用、无互相矛盾。本轮新增活体复测
  （只读 GET）与 MacBridge 侧新锚点亲核全部吻合。架构方向、复用策略、证据门框架
  维持 r1 的正面判断。
- implementation_readiness（通过 ≠ 授权，方案 §6 已声明实施另需 owner 启动）：
  - 可开工（受既定门约束）：S-1（resolver + 触发机制，E-1 已 verified、OD-1 已定）、
    S-2a（probe 端点替换，E-8 边界已知）、S-7 文档部分。
  - 仍受证据门阻塞：S-3（E-2）、S-4 映射固化（E-4）、S-5（E-3/E-5）、
    **S-2b 解除隔离（E-2/E-3/E-5 齐备 + S-3~S-5 代码就位，三处一致）**、
    S-6 分支（OD-5 裁决 + E-7）、验收标准 4 的切换/轮换验收（E-6）。
  - 产品决定门：OD-2b（ensure-start）、OD-5；OD-1/3/4/6 的推荐默认建议 owner
    在启动实施时顺带确认（F-9 处置已可追溯）。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 核对结果 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 仅未跟踪 r1 报告 + 方案两个 docs 文件 | 与方案 §2.1 一致 |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净 | 与方案 §2.1 一致（`git describe` = v2.0.32 亲核） |

运行身份（本轮活体复核，2026-10-19）：v2 共享服务 `127.0.0.1:49374` 在听
（进程 PID 29415）；`~/.local/state/opencode/service.json` 存在，`version=2.0.32`、
`url=http://127.0.0.1:49374`、`pid=29415`（注册 ↔ 监听进程一致）。v1 managed
`127.0.0.1:4096` 在听（PID 28164，与 A.1 一致）。与方案 §2.1 运行身份一致。

### 2.2 本轮检查范围

- 定向复核修订段（v1.0→v1.1 diff 所及）：§2.2 目录路由行、§2.3 新增 Swift 监控/重启
  现状行、§3.2 触发机制重写、§3.3 拆片、§3.5 envelope 分型、§3.6 form 路由、
  §3.10 OD 状态、§4 Gate 映射、§5 切片表、E-3/E-5/E-7 行、附录 A.2/A.4/A.8、附录 B。
- 全量重查（复审纪律）：R→S→验收映射、OD 选项×切片×证据关系、切片依赖 DAG
  （含 S-2a/S-2b 循环无死锁核验：S-2a→S-3/4/5 代码→S-2b→活体验证，序一致）、
  阻塞清单与 §4/§5 的一致性。
- 本轮亲核锚点：MacBridge 侧 §3.2 全部新锚点（监控循环/触发链/descriptor seam/
  resolve guard）；上游全部新更正锚点（SessionsQuery、form 组四路由、filesystem/
  compaction 事件字面量、service-probe pid/version 校验、EnsureOptions）；附录 B
  事件字面量补齐亲核（text/tool/execution/interrupted/inbox/model/agent/moved/
  skill 共 10 名逐一 grep 命中）。复用 r1 核验的命题见 §2.3 复用列。
- 本轮活体复核（只读 GET，凭据不落输出）：§3。
- audit-plan 专项：**已运行**（延续 r1），本轮对方案写为事实的全部外部形状逐项重读：
  service.json 键集、/api/info 401/200+字段、/api/health 404、目录参数矩阵（含
  baseline md5 全量比对）、三路由 envelope 形状、事件声明字面量。全部吻合；
  E-2/E-3/E-4/E-5 维持 pending 且实现被 gate（未把未采样形状写成事实）。

### 2.3 锚点核验表（本轮亲核的主干；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位/语义 | 亲核或复用 |
| --- | --- | --- | --- | --- |
| §2.2 列表过滤＝`SessionsQuery.directory`（`session.ts:168-174`，cursor `:173`） | opencode-v2 `packages/protocol/src/groups/session.ts:168-174` | `:168 export const SessionsQuery = Schema.Struct({`；`:170 directory: AbsolutePath.pipe(Schema.optional),`；`:173 cursor: SessionsCursor.pipe(Schema.optional),` | **定位准确 / 语义成立** | 本轮亲核（F-1 修订落点） |
| §2.2 scoped 路由 location 上下文＝`location.ts:71-79` | `packages/server/src/location.ts:71-79` | `:76 query.get("location[directory]") \|\|`；`:77 (request.headers["x-opencode-directory"] ? decode(…) : process.cwd())` | **定位准确 / 语义成立**（两机制拆分后各自成立） | 本轮亲核（r1 亦核） |
| §2.2/§3.6/E-5 form 组：list `session.ts:796`、detail `:827`、reply `:842`、cancel `:858` | 同文件 :796/:827/:842/:858（grep 行号） | `:796 HttpApiEndpoint.get("session.form.list", "/api/session/:sessionID/form"`；`:842 …post("session.form.reply", "…/form/:formID/reply"`；`:858 …delete("session.form.cancel", "…/form/:formID"` | **四路由全部定位准确 / 语义成立** | 本轮亲核（F-6 修订落点） |
| §3.1 探活校验「响应 pid/version 与注册一致（对齐 service-probe.ts:78-79）」 | `packages/client/src/service-probe.ts:78-79` | `:78 if (serverInfo.pid !== info.pid) return { service: undefined…}`；`:79 if (info.version !== undefined && serverInfo.version !== info.version) return { service: undefined…}` | **定位准确 / 语义成立**（404→compatible:false 在 `:65-73`，方案回退语义等效） | 本轮亲核 |
| §2.2 ensure 形状 `service.ts:27-35`（默认 `opencode serve --service`） | `packages/client/src/service.ts:27-35` | `:30 readonly command?: …`；`:31 /** Service command and arguments. Defaults to \`opencode serve --service\`. */` | **定位准确 / 语义成立** | 本轮亲核 |
| §2.2 注册写 `service-registration.ts:39`；5s 回读 `:40-67`（spaced `:63`）；退出删 `:68-71` | 同文件 :39/:63/:68-71 | `:39 …writeFileString(temp, encoded, { mode: 0o600 })…rename`；`:63 Effect.repeat(Schedule.spaced("5 seconds")),`；`:69-70 owns(found) ? fs.remove(options.file) : Effect.void` | **定位准确 / 语义成立** | 本轮亲核（r1 亦核） |
| §2.2 固定端口 `service-config.ts:56`（latest 0xC0DE=49374） | 同文件 :56 | `:56 if (channel === "latest" \|\| channel === "dev" \|\| … ) return 0xc0de` | **定位准确 / 语义成立**（实测 dev/beta/next 同值，方案表述保守方向成立） | 本轮亲核 |
| 附录 B 事件字面量（agent.selected/model.selected/moved/message.content.updated/inbox.enqueued/execution.started/execution.interrupted/skill.activated/text.started/tool.input.started） | `packages/schema/src/session-event.ts:70/81/92/144/216/240/254/293/393/477` | 逐名 grep 命中（例 `:144 type: "session.message.content.updated"`、`:393 type: "session.text.started"`、`:477 type: "session.tool.input.started"`） | **语义成立**（补齐 r1 未逐一列名的 text/tool 行） | 本轮亲核 |
| 附录 B `session.compacted`（`session-compaction-event.ts:7`）、`filesystem.changed`（`filesystem.ts:9`）、form 事件（`form.ts:173-175`） | schema 包对应文件 | `:7 type: "session.compacted"`；`filesystem.ts:9 type: "filesystem.changed"`；`form.ts:173-175 form.created/form.replied/form.cancelled` | **定位准确 / 语义成立** | 本轮亲核（F-4 修订落点） |
| §3.4 全局事件 id（`event.ts:9-13`）与 volatile 契约（`:51`） | `packages/protocol/src/groups/event.ts:9/:51` | `:9 id: Event.ID,`；`:51 "…Volatile by contract: a slow consumer overflows and fails the stream, and events during disconnection are missed."` | 基本准确（fields 块 :8-12，引用区间含目标行）/ **语义成立** | 本轮亲核 |
| §2.2 awaitPlugins 5s 门（`location.ts:36-47`） | `packages/server/src/location.ts:37-45` | `:38 Effect.timeoutOrElse({ duration: "5 seconds"` → `ServiceUnavailableError` | **定位准确 / 语义成立** | 本轮亲核（r1 亦核） |
| §2.3 监控循环 3s（`RuntimeManager.swift:708-721`，pollManagementAPI+evaluateAutoRestart） | 本仓 `MacBridge/MacBridge/Services/RuntimeManager.swift:709-722` | `:718 await self.pollManagementAPI()`；`:719 self.evaluateAutoRestart()`；`:722 try? await Task.sleep(nanoseconds: 3_000_000_000)` | **定位准确 / 语义成立** | 本轮亲核（F-2 新锚点） |
| §3.2 触发链 `triggerAutoRestart(reason:)`（`:760-772`，stuckRestartCount/maxStuckRestarts `:760-768`） | 同文件 :757-768（函数头 :757） | `:759 if stuckRestartCount >= maxStuckRestarts {`；`:765 stuckRestartCount += 1`；`:768 scheduleRestart(trigger: .automatic)`；`:417 maxStuckRestarts = 5` | **定位准确（区间落在函数体内）/ 语义成立**（护栏=连续失败 5 次停自动重启并如实报错） | 本轮亲核（F-2 新锚点） |
| §2.3 既有先例 `fileReadHealth.restartRecommended`（`:1015-1017`）→triggerAutoRestart | 同文件 :1015-1016 | `:1015 if resp.v1?.fileReadHealth.restartRecommended == true {`；`:1016 triggerAutoRestart(reason: "文件读取运行时已退化…")` | **定位准确 / 语义成立**（新机制同构先例真实存在） | 本轮亲核 |
| §2.3 每轮 status 后 detached 刷新 agents（`:1019` 一带） | 同文件 :1019-1022 | `:1019 // agents 刷新独立低优先级 task…`；`:1020 Task.detached(priority: .utility) { … client.getAgents()` | **定位准确 / 语义成立** | 本轮亲核 |
| §2.3 结构化就绪 live 重读（`management_api.go:692-733`；状态面 not_configured/service_not_running/available，`agent_descriptor.go:243-249`） | 本仓 `go-bridge/management_api.go:692-733`（handleAgents :693、liveAgentDescriptors :704）；`go-bridge/agent_descriptor.go:243-249` | management_api `:711-714 if descs[i].ID == "opencode-web" { … status, reason := detectStructuredInstanceReadiness(…)`；descriptor `:244-248 // 2026-10-06 方案 §3：结构化就绪（not_configured / service_not_running / available）… return detectStructuredInstanceReadiness("opencode-web", agent)` | **定位准确 / 语义成立**（信号面既有、零新增成立） | 本轮亲核（F-2 新锚点） |
| §3.2 重启即重跑 resolve（`launchBridgeProcess:581`）；apply 路径 `:536` | 同文件 :536/:581 | `:581 resolveManagedOpenCodeIfNeeded()`（launchBridgeProcess 内，P2-8 目录创建后） | **定位准确 / 语义成立** | 本轮亲核（r1 亦核） |
| §3.2 resolve guard 结构（:1157 guard 之前挂 attach 判定） | 同文件 :1157-1163 | `:1158 guard config.drivers.contains("opencode-web"), config.opencodeSource == .managedLocal else {`；`:1159-1160 openCodeManagedServer?.stop() …`（非 managedLocal 即停 managed） | **定位准确 / 语义成立**——source 置为 attach 后，既有 guard 自动停 managed spawn 且 :1198 syncDesktopConfig 仅在 managedLocal 分支可达，「单一产品面+跳过 desktop 同步」由现有结构直接承载 | 本轮亲核 |
| source 传入 Go 的通道（§2.3 `main.go:110` flag 枚举） | `go-bridge/main.go:110`；`RuntimeManager.swift:1791` | `:110 flag.String("opencode-source", "", "…(managed_local, external_http, legacy_64667, service_discovery_future, disabled)")`；`:1791 arguments += ["-opencode-source", config.opencodeSource.rawValue]` | **定位准确 / 语义成立**——descriptor 增补 source 所需的 Swift→Go 通道已存在，加枚举值即可 | 本轮亲核 |
| §3.3 probe `/api/health`（`probe.go:174`）与 shape arbiter（`probe.go:196-216`） | `agent/opencode-web/probe.go:174/:196-216` | `:174 v2Health, v2Detail := probeHealth(ctx, c, "/api/health")`；`:197-201 shapePath := "/session" … if candidate == generationV2 { shapePath = "/api/session"; expected = shapeDataEnvelope }` | **定位准确 / 语义成立**（必改判断与「arbiter 只看 data 键」描述均成立） | 本轮亲核（r1 亦核） |
| §3.5 `unwrapDataEnvelope` descends-when-present（`sessions.go:260-262`）；directive-003（`:137-143`） | `agent/opencode-web/sessions.go:260-262/:137-143` | `:260-261 // unwrapDataEnvelope descends a v2 {"data": …} envelope when present, returning the inner payload (v1 flat payloads pass through unchanged).`；`:139-140 // Any bucket failure fails the whole global list — never a partial catalog (directive-003 OD-2).` | **定位准确 / 语义成立** | 本轮亲核（F-5 修订落点） |
| §3.4 fingerprint/seen（`go-bridge/session_discovery.go:403-433`，比较 `:412-433`） | 本仓 `go-bridge/session_discovery.go`（根包） | `:412 prev, hadPrev := seen[id]`；`:423 if current != prev { … sessions_changed` | **定位准确 / 语义成立**（包路径已按 F-8 写全） | 本轮亲核 |
| §2.3 现行 client 目录参数行为（`client.go:97-124`） | `agent/opencode-web/client.go:97-124` | `:111-117 if c.gen == generationV2 { // … ONLY V2SessionListData declares query.directory … appendQuery = req.Method == http.MethodGet && strings.HasSuffix(url, "/api/session") }` | **定位准确 / 语义成立**——现行实现与机制一（plain query）吻合；注释「header stays off the v2 wire」的措辞更正要求与 F-1 修订一致 | 本轮亲核 |
| events.go C1 / quarantine 三处共用 | `agent/opencode-web/events.go:142-145`；`opencodeweb.go:328-336` | `:142-144 // C1: … the former v2 /api/event branch is deleted`；`:329-333 // unsupportedGenerationDetail is the single shared quarantine wording used by InstanceStatus, clientFor, and RunDiagnostics…` | **定位准确 / 语义成立**（「三处同步」与源码注释一致） | 本轮亲核（r1 亦核） |
| 消息端点 `message.ts:68`；prompt `session.ts:396-407`；全局 form `form.ts:12`；resolver 槽位 `:25-27` | 各对应文件 | `:68 HttpApiEndpoint.get("session.messages", "/api/session/:sessionID/message"`；`:396 …post("session.prompt", … "/api/session/:sessionID/prompt"` + `:399 id: SessionMessage.ID.pipe(Schema.optional)`；`:12 HttpApiEndpoint.get("form.list", "/api/form"`；resolver `:25 case serviceDiscoveryFuture = "service_discovery_future"` | **全部定位准确 / 语义成立** | 本轮亲核（r1 亦核） |

未核/低风险未核项（结论不受影响）：InstalledView 文案现状（展示层）；§3.9 括注
「官方 remote 开启时 url 仍为 loopback 代理」未逐字核上游（注册 url 来自
`HttpServer.formatAddress(options.address)`，无论取值如何，方案行为=强制 loopback
校验失败即回退，fail-closed 方向安全，属 R-6 自身要求）；附录 A 历史时间窗
（A.5/A.6/A.7）为过去时观测，r1 已按现存状态侧证，本轮未复演。

### 2.4 跨轮核验复用与独立性披露

- 复用单元为命题/锚点粒度，非整份文档：r1 已核且 v1.1 相关文本未变的命题
  （server.ts /api/info、event.ts SSE 端点、CRUD 路由组、permission 路由、
  project.ts、无 todo 组、OpenCodeManagedServer 三段、main.go env 通道、
  desktop_home_projects、think.md 五处、架构文档锚点表）本轮标**复用**；
  其中 load-bearing 的多数已在本轮重新亲核（见 §2.3「亲核（r1 亦核）」标注）。
  两侧源码身份未变（上游同 commit；本仓同 HEAD），行号未漂移。
- 独立性限制：延续模式，同一评审员身份。缓解：本轮对全部修订段与新锚点重新亲核、
  活体重测独立执行（结果与 r1 及 A.8 修订者复测三方一致），且闭合判定均落在
  可对账证据（锚点原文、md5、pid 链）上，不依赖对 r1 的信任。

## 3. 活体复核（audit-plan 专项证据；本轮 2026-10-19 只读探测，脱敏）

凭据从 service.json 读入后仅入请求头，不出现在任何输出。

1. **E-1 三度确认**：`/api/info` no-auth → 401；Basic → 200，body 键集
   `{capabilities,paths,pid,urls,version}`，version 2.0.32、pid 29415 ==
   service.json 注册 pid == 监听进程 pid。`/api/health`（带认证）→ 404。
   与 A.3 / A.8.4 逐字段一致。
2. **目录参数矩阵复测**（`/api/session`，md5 对账）：baseline、
   `?location[directory]=<TCC>`、`?x-opencode-directory=<TCC>`（query）、
   header `x-opencode-directory: <TCC>` 四者 md5 均为
   `db90e583f8b1cd1e744736c4ec843291`（与 A.8.1 记录的全量 md5 逐字一致）；
   唯一生效形式 `?directory=<TCC>` → n=1，全部会话 location 均为
   `~/Documents/todo-app-sisyphus`。A.4/E-7 更正后的探测形式成立。
3. **envelope 三形状复测**：`/api/session` → `{data:50, cursor:{next,previous}}`；
   `/api/model` → `{location, data:157}`；`/api/project` → 裸数组 n=27，
   键集 `{canonical,icon,id,sandboxes,time,vcs}`。与 A.8.3 完全一致。
4. 4096 v1 managed 在听（PID 28164），与 A.1 一致。

## 4. 原意见闭合表（复审处置）

| 原意见 | 严重性 | 闭合判定 | 闭合证据 |
| --- | --- | --- | --- |
| F-1 目录路由锚点混用 + A.4/E-7 no-op 探测 | 阻塞 | **闭合** | §2.2 拆为两机制并各给正确锚点（`session.ts:168-174` 与 `location.ts:71-79` 均本轮亲核成立）；client.go 注释更正要求写入正文；A.4 更正记录在 A.8.1（含全量 md5，本轮活体逐字复现）；E-7 改用正确探测形式；E-3 补 scoped 写目录三态矩阵（带/不带 header/不带上下文） |
| F-2 attach↔回退缺真实触发机制 | 阻塞 | **闭合** | §3.2 重写：信号面（liveAgentDescriptors 结构化 seam，`management_api.go:692-733`+`agent_descriptor.go:243-249` 本轮亲核）、attach→回退（3s 循环 `:709-722` + `triggerAutoRestart` `:757-768` + 重启重跑 resolve `:581` 均亲核）、回退→回附（60s 冷却复用同一 resolve）、防风暴（跃迁触发+共享 stuck 上限）；验收标准 4 与 E-6 断言已改写为与机制一致的时延契约（3s+一次重启 / ≤60s+一次重启） |
| F-3 S-2 依赖与 Gate A 矛盾 | 阻塞 | **闭合** | 拆为 S-2a（probe 替换，依赖 S-1/E-8，完成后仍 quarantine）/S-2b（解除隔离，依赖 S-2a+E-2/E-3/E-5+S-3~S-5 代码就位）；§3.3、§4 Gate 映射、§5 切片表三处逐字一致；S-3~S-6 依赖列均指 S-2a，活体验证列均标注「需 S-2b 已放行」（S-6 见 F-11 建议补齐措辞）；依赖 DAG 无环无死锁 |
| F-4 上游行号漂移清单 | 建议 | **闭合** | 全部按 r1 核验落回正文（SessionsQuery/form 组/filesystem/compaction/service-probe :78-79/EnsureOptions 等本轮亲核对上）；「全仓 grep 无 /api/health」已改为「serve 服务端无此路由，唯一命中在独立 stats web app」；旧 checkout 分支名已更正为 dev |
| F-5 统一 envelope 表述 | 建议 | **闭合** | §3.5 按路由分型三形状 + unwrapDataEnvelope descends-when-present 语义（`sessions.go:260-262` 亲核）；S-4/S-6 验证列补裸数组与 cursor 分页断言；本轮活体复测三形状吻合 |
| F-6 form 应答锚点缺失 | 建议 | **闭合** | §2.2/§3.6/E-5 补 session 作用域 form 四路由（:796/:827/:842/:858 本轮亲核）；E-5 断言含 `form.created → reply → form.replied` 完整流 |
| F-7 样本数字两处矛盾 | 建议 | **闭合** | §2.4/A.2/A.8.2 统一为「首页 277,979 B/50 条、跨页 4 页/132 条」；旧数字按证据更正纪律标注作废及原因，未无声删除 |
| F-8 session_discovery 缺包路径 | 建议 | **闭合** | §3.4 写全 `go-bridge/session_discovery.go:403-433` 并显式注明根包、比较段 :412-433（本轮亲核） |
| F-9 decided 无决定来源 | 建议 | **闭合** | OD-1/3/4/6 状态列均标「decided（方案推荐默认；owner 评审/实施前可推翻）」，来源可追溯 |

## 5. 意见（本轮新增；F-10 ~ F-11 为旧问题漏检的补全，F-12/F-13 为修订段引入语义面后的新观察）

### F-10 [建议] 防风暴测试口径：stuck 计数在重启成功后清零，抖动场景的实际速率界来自 60s 冷却

- 位置：§3.2「防风暴」段；§1 验收标准 4；E-6 断言；§5 S-1 单测列、§6 触发判定单测。
- 证据：`RuntimeManager.swift:1070-1072`——`setStatus` 进入 `.ready/.readyNoAgents`
  即 `stuckRestartCount = 0`（本轮亲核）。服务抖动且每次重启都能成功就绪的场景下，
  stuck 计数每轮清零，`maxStuckRestarts=5` 永不触顶；E-6「抖动场景不突破
  maxStuckRestarts」会空洞通过。真正的速率界是「仅状态跃迁触发 + 回附 60s 冷却」
  （每抖动周期约一次 attach 重启 + 一次 3s 回退重启，周期 ≥60s）。
- 影响：若单测只断言 stuck 上限，可能漏测「重启均成功的抖动」这一最常见风暴形态。
- 修订方向：S-1 触发判定单测与 E-6 断言补一条显式用例：N 个「服务重现→消亡」抖动
  周期（每次重启均成功就绪）下，重启次数/速率符合 60s 冷却推出的上界，并观察
  stuck 计数清零行为。纯测试口径补充，设计本身（冷却+跃迁门）已能限速。
- 闭合标准：实施期单测清单含该用例即可（可在 exec-plan 任务拆分时落入，无需再轮评审）。

### F-11 [建议] S-6 活体验证的排序标记与 S-3/S-4/S-5 不齐

- 位置：§5 S-6 行「活体：含 todo-app-sisyphus 的聚合不再每分钟失败」。
- 证据：attach 模式下聚合经 driver clientFor，v2 在 S-2b 放行前被 quarantine——
  attach 模式的活体验证同样需 S-2b 已放行；S-3/S-4/S-5 行均有此限定语，S-6 没有。
  v1 回退模式（OD-5 两分支）的活体验证不受影响，S-6 依赖列本身无误。
- 影响：仅表格一致性；按字面排期可能在 S-2b 前安排 attach 模式活体聚合验证而扑空。
- 修订方向：S-6 活体列补「attach 模式需 S-2b 已放行；v1 回退模式不受此限」。
- 闭合标准：实施排期时口径明确即可，无需再轮评审。

### F-12 [建议] resolver 注册文件路径应镜像官方 XDG_STATE_HOME 覆盖

- 位置：§3.1 步骤 1「读 ~/.local/state/opencode/service.json」；§3.9。
- 证据：官方定位逻辑 `service-probe.ts:40`——
  `join(process.env["XDG_STATE_HOME"] ?? join(homedir(), ".local", "state"), "opencode", "service.json")`；
  §2.2 复用表已如实记录该覆盖，但 §3.1 的 Swift resolver 步骤写死默认路径。
  用户环境设置 XDG_STATE_HOME 时，desktop/CLI 写入与 CordCode 读取将分叉，
  attach 永不命中（fail-safe 降级到 4096，但 R-1 静默失效）。
- 影响：小众环境下的发现 parity 缺口；方向安全（回退），不破坏架构。
- 修订方向：S-1 resolver 镜像官方路径解析（XDG_STATE_HOME ?? ~/.local/state），
  或在方案显式记录该分歧与降级取舍。一句话级修订。
- 闭合标准：实施 S-1 时路径解析与官方 fallback() 对齐或分歧被记录。

### F-13 [建议] attach 优先的适用来源范围未写明，按字面可劫持显式 external_http/legacy_64667 配置

- 位置：§3.2「解析时机：挂接在 resolveManagedOpenCodeIfNeeded() 之前的独立步骤
  （:1157 的 guard 之前判定）」；§3.1 流程；R-1 表述（「v2 在场时优先 attach」）。
- 证据：`:1158` guard 以 `config.opencodeSource == .managedLocal` 为门；把 attach
  判定挂在 guard 之前且无条件执行，则 external_http/legacy_64667 显式配置的用户在
  v2 服务在场时也会被切到 shared_service_v2，覆盖用户显式选择。回附循环条件
  （source==managed_local）本身安全，问题只在 attach-first 的适用范围。
- 影响：产品语义边界（用户显式配置 vs 自动 attach）未定义；当前默认用户
  （managedLocal 自动）不受影响，核心验收不受影响。
- 修订方向：写明 attach-first 仅在来源为 managedLocal（或新增的自动档）时生效，
  不覆盖 external_http/legacy_64667/显式选择；一句话级修订。
- 闭合标准：§3.1/§3.2 含该限定条件（实施前落即可，无需再轮评审）。

## 6. 复审处置与剩余门

- 原意见闭合：F-1~F-9 全部闭合（§4 表）；本轮无被驳回后复争议项。
- 新意见来源标注：F-10/F-11 为旧问题漏检的补全（r1 未展开测试口径与 S-6 排序
  标记）；F-12/F-13 为修订段引入新语义面（resolver 步骤具体化）后的新观察。
  四条均为建议，不影响通过。
- 仍待证据/决定/授权（与阻塞意见分列；通过不改变其状态）：
  - 证据门（实施期捕获，方案登记完备）：E-2（SSE 帧，gate S-3 与 S-2b）、
    E-3（prompt+目录三态矩阵，gate S-5）、E-4（parts schema+分页对账，gate S-4
    映射固化）、E-5（permission/form wire，gate S-5）、E-6（轮换/双向切换端到端，
    gate 验收标准 4）、E-7（TCC 可达矩阵 desktop 起 vs 手动起，gate S-6 分支说明）、
    E-8（503 重试边界，gate S-2a 重试验收）。
  - 产品决定门：OD-2b（ensure-start，owner 未裁决，仅 gate 可选切片）、
    OD-5（fail-all vs 跳过+告警，owner 未裁决，gate S-6 分支选择）；
    OD-1/3/4/6 推荐默认建议 owner 启动实施时顺带确认。
  - 授权边界：本通过不构成实施授权；S-1~S-8 由 owner 决定是否启动 exec-plan
    （方案 §6 已声明）。E-6/E-7 复现动作涉及本机 live 服务替换，实施期按方案
    登记的取证方式与清理纪律执行。
- 外部格式专项（audit-plan）结论：**通过（就已声明形状而言）**——本轮对方案写成
  事实的全部外部形状重读吻合（§3）；E-2/E-3/E-4/E-5 为显式 pending 且对应实现被
  gate（样本未齐前 S-2b 不放行、S-4 映射不固化、S-5 不可实施），符合共享约定的
  待捕项裁定。

## 7. 结论

方案 v1.1 对 r1 全部九条意见完成有证据的修订：三处阻塞的闭合都落在可对账的锚点、
md5 与代码结构上（本轮全部重新亲核），门控三处一致无绕行，证据更正按契约留痕。
剩余四条建议均为一句话到单测口径级别的实施期吸收项，不构成再轮修订的必要。
**verdict: APPROVED**——设计层面通过；实施就绪度以 §1 所列门为准，逐片开工前
照常校验对应 E/OD/授权。
