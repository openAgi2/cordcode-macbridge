# 评审报告 r3：OpenCode Web 接入 v2 共享服务（generation-2 attach）设计方案

- 评审轮次：第 3 轮（复审；r2 APPROVED → r2-meta confirm=false → v1.2 纠错修订后的通过结论恢复复核）
- 评审日期：2026-10-19
- verdict：**APPROVED（通过；r2 的通过结论对 v1.2 恢复有效）**
- blockers：0；advisories：本轮新增 1（F-14），随行 4（F-10~F-13，r2 判定实施期吸收）
- 契约版本：plan-contract-v1.1（plan-review skill，含 audit-plan 专项接入）
- 评审员模式披露：派发简报标「延续」。实际情况：本轮为**新会话**，通过完整读取
  r1/r2/r2-meta 三份报告重建上下文（非 r1/r2 同一进程）。这与共享约定推荐的
  「新评审员独立阅读后再核对历史」模式一致——r2-meta 已发现 r2 摘录存在漏检漂移，
  恰是该模式的适用场景。缓解措施：本轮对全部承重锚点与两处更正行号重新亲核
  （不依赖历轮摘录），纠错落点、来源身份、活体身份全部独立复测；对 r1/r2 已核
  且 v1.2 文本未变的低风险命题按命题粒度复用并在 §2.3 标注。

## 1. 结论与实施就绪

- verdict: **APPROVED**
- plan: `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.2
  （r2-meta 纠错修订版），SHA-256
  `63421a8271e420aa78602940d35c5bd16827b227bbf64c385d25fb7bfc13abec`
  （本轮派发时重算一致）。本轮范围：r2-meta 四项问题的纠错落点定向复核 + 全局
  门控/OD/E/S/验收回归重查 + 承重锚点独立重核（防同类漏检）。
- 总体判断：**r2-meta 四项问题全部按契约正确闭合**（§4 表）。v1.2 的改动严格限于
  §1 头部声明、§2.1 来源表、§2.2 两处行内子行号补注与附录 A.9 更正记录；本轮对
  全文重读，未发现任何 R/OD/S/E/Gate/验收标准被改动或引入回归。本轮独立重核约
  45 个锚点引用单元（上游 25、本仓 Go 12、Swift/文档 8），承重锚全部命中；
  仅发现 1 条建议级新问题（F-14，附录 B 两处 basename 缺包路径，属旧问题漏检，
  非本轮修订引入）。
- 结论定性：按共享约定「通过后纠错」第 5 条——r2 的 APPROVED 通过结论对 v1.2
  **恢复有效**；F-14 不影响任何设计命题、门控或验收（行号与语义在正确文件中均
  亲核成立，仅路径歧义），属建议级，与 r2 对 F-10~F-13 的处置同级。
- implementation_readiness（通过 ≠ 授权，方案 §6 已声明实施另需 owner 启动 exec-plan）：
  - 可开工（受既定门约束）：S-1（resolver + 触发机制，E-1 verified、OD-1 推荐
    默认可追溯）、S-2a（probe 端点替换，E-8 边界已知）、S-7 文档部分。
  - 仍受证据门阻塞：S-3（E-2）、S-4 映射固化（E-4）、S-5（E-3/E-5）、
    **S-2b 解除隔离（E-2/E-3/E-5 齐备 + S-3~S-5 代码就位；§3.3/§4/§5 三处一致，
    本轮重查无法按切片表绕行）**、S-6 分支（OD-5 裁决 + E-7）、验收标准 4 的
    切换/轮换验收（E-6）。
  - 产品决定门：OD-2b（ensure-start，owner 未裁决，仅 gate 可选切片）、OD-5
    （fail-all vs 跳过+告警，owner 未裁决，gate S-6 分支选择）；OD-1/3/4/6 推荐
    默认建议 owner 启动实施时顺带确认。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核；按 r2-meta 口径含全部四仓，含配套工作树）

| 仓库/工作树 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 核对结果 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 干净；未跟踪恰 4 个 docs 文件（方案 + r1/r2/r2-meta 报告；本轮 porcelain 亲核） | 与方案 §2.1 一致 |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32`（`git describe` 亲核） | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净（porcelain 空） | 与方案 §2.1 一致 |
| opencode（旧 checkout，仅备注） | `/Users/jacklee/Projects/opencode` | `dev` | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | 干净（porcelain 空） | 与方案 §2.1 一致（不作为锚点） |
| cordcode-ios（配套工作树） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52` | 干净（porcelain 空） | 与方案 §2.1 v1.2 新增行一致 |

- 配套工作树判定亲核：两侧 `git worktree list` 各仅存在一条
  `feat/claude-long-session-projection-loading` 分支族工作树（其余 worktree 均为
  main/native-message-timeline/plan-approval/widget-support 等别的分支族），
  唯一配套对应成立——与方案 §2.1/A.9.3 的判定记录一致。
- 运行身份（本轮活体复核，只读、凭据不落输出）：v2 共享服务 `127.0.0.1:49374`
  在听（PID 29415）；`~/.local/state/opencode/service.json` 的
  `{version:2.0.32, pid:29415, url:http://127.0.0.1:49374}`（仅打印此三键），
  注册 pid == 监听进程；`GET /api/info` 无认证 → 401。v1 managed `127.0.0.1:4096`
  在听（PID 28164）。与方案 §2.1 运行身份及 A.1/A.8.4 一致。

### 2.2 本轮检查范围

- 纠错落点定向复核（v1.1→v1.2 声明的改动面）：§1 头部、§2.2 ensure 行与机制二行
  子行号补注、§2.1 来源表（含 iOS 新增行、MacBridge/旧 checkout 未提交状态刷新）、
  附录 A.9 四条更正记录的逐项事实核（含 A.9 对「:31 是 env 注释」「:76-77 是构造
  两行」的旁证描述，本轮亲核属实）。
- 全量回归重查：R→S→验收映射、OD×E×S 关系（OD-2/OD-5 pending 仅 gate 各自可选
  切片）、S-2a/S-2b 拆片后 §3.3/§4/§5 三处门控一致性、依赖 DAG 无环、阻塞清单
  与 §4/§5 一致性、OD 表六项状态未变。
- 锚点独立重核（防 r2-meta 同类漏检）：上游 §2.2/§2.3/§3 与附录 B 全部带行号引用
  逐项重核或抽样重核；本仓 Go/Swift 承重锚全部重核（见 §2.3）；think.md 五处与
  GO_BRIDGE_ARCHITECTURE.md:87 重核（活文档，行号可能漂移，未复用）。
- audit-plan 专项：**已运行**（延续）。本轮对 v1.2 写成事实的外部形状未发现变化
  （纠错未触及），E-1 活体轻量复测一致；E-2/E-3/E-4/E-5 维持 pending 且对应实现
  被 gate。附录 B 发现 1 条包路径歧义（F-14）。
- 未核/低风险未核：附录 A 历史时间窗细节（A.2/A.5/A.6/A.7 过去时观测，r1/r2 已
  按现存状态侧证，本轮不复演）；OD-2b desktop staged 二进制解析顺序（owner 未
  裁决项）；InstalledView 文案现状（展示层）。均不影响本轮结论。

### 2.3 锚点核验表（本轮亲核主干；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位/语义 | 亲核或复用 |
| --- | --- | --- | --- | --- |
| 〔r2-meta 问题 1 落点〕§2.2 ensure 行：默认命令注释 `:29`、`command?` 字段 `:30` | opencode-v2 `packages/client/src/service.ts:29/:30` | `:29 /** Service command and arguments. Defaults to \`opencode serve --service\`. */`；`:30 readonly command?: ReadonlyArray<string>` | **定位准确 / 语义成立**（`:31` 确为 env 注释，A.9.1 旁证属实；EnsureOptions 区间 :27-35、Info :46-57 同核成立） | 本轮亲核 |
| 〔r2-meta 问题 2 落点〕§2.2 机制二行：query `:74`、header 三元 `:75` | `packages/server/src/location.ts:74/:75` | `:74 query.get("location[directory]") \|\|`；`:75 (request.headers["x-opencode-directory"] ? decode(…) : process.cwd())` | **定位准确 / 语义成立**（`:76-77` 确为 `return Location.Ref.make({`/`directory: AbsolutePath.make(directory),` 构造两行，A.9.2 旁证属实；requestRef 区间 :71-79 成立） | 本轮亲核 |
| §2.2 列表过滤 `SessionsQuery.directory` `session.ts:168-174`（cursor `:173`） | `packages/protocol/src/groups/session.ts:168-174` | `:168 export const SessionsQuery = Schema.Struct({`；`:170 directory: AbsolutePath.pipe(Schema.optional),`；`:173 cursor: SessionsCursor.pipe(Schema.optional),` | **定位准确 / 语义成立**（F-1 承重锚） | 本轮亲核 |
| §2.2/§3.6/E-5 form 组 session 作用域路由 `session.ts:796/:827/:842/:858` | 同文件 | `:796 …get("session.form.list", "/api/session/:sessionID/form"`；`:842 …post("session.form.reply", "…/form/:formID/reply"`；`:858 …delete("session.form.cancel", "…/form/:formID"` | **定位准确 / 语义成立**（F-6 承重锚；:827 form.get 同核命中） | 本轮亲核 |
| §2.2 会话 CRUD `session.ts:182,221,287,300,362`；prompt `:396-407`；interrupt `:746`；switchAgent/Model `:330/:346` | 同文件逐行 grep | `:182 get("session.list", "/api/session"`；`:221 post("session.create"`；`:287 get("session.get", …:sessionID"`；`:300 delete`；`:362 patch("session.update"`；`:399 id: SessionMessage.ID.pipe(Schema.optional)`（payload 含 metadata/delivery/resume :400-403）；`:746 post("session.interrupt"` | **全部定位准确 / 语义成立** | 本轮亲核（r1/r2 亦核） |
| §2.2 消息 `message.ts:68`；permission `:23-124`、reply `:117`；全局 form `form.ts:12`；project `project.ts:6,17`；`server.ts:43`（/api/info）、ServerInfo `:5-19` | 各对应文件 | `message.ts:68 get("session.messages", "…/message"`；`permission.ts:117 post("session.permission.reply", "…/permission/:requestID/reply"`；`form.ts:12 get("form.list", "/api/form"`；`project.ts:6 const root = "/api/project"`、`:17 get("project.list"`；`server.ts:43 get("server.info", "/api/info"` | **全部定位准确 / 语义成立** | 本轮亲核（r1/r2 亦核） |
| §2.2 事件流 `event.ts:44-52`（SSE `:44`、volatile `:51`）；Event.ID `:9-13` | `packages/protocol/src/groups/event.ts` | `:9 id: Event.ID,`；`:44 …get("event.subscribe", "/api/event"`；`:51 "…Volatile by contract: a slow consumer overflows and fails the stream, and events during disconnection are missed."` | **定位准确 / 语义成立** | 本轮亲核 |
| §2.2 服务发现 `service-probe.ts:49-90`（pid `:78`、version `:79`、404 `:65`）、fallback `:39-41` | `packages/client/src/service-probe.ts` | `:40 join(process.env["XDG_STATE_HOME"] ?? join(homedir(), ".local", "state"), "opencode", "service.json")`；`:55 fetch(new URL("/api/info", info.url)…)`；`:78/:79` 逐字命中 pid/version 不符分支 | **定位准确 / 语义成立**（F-12 建议的 XDG 覆盖锚点同核成立） | 本轮亲核（r1/r2 亦核） |
| §2.2 注册写/仲裁 `service-registration.ts:39`（0600+rename）、5s 回读 `:63`、退出删 `:69`；固定端口 `service-config.ts:56`；resolve `server-connection.ts:21-51` | 各对应文件 | `:39 …writeFileString(temp, encoded, { mode: 0o600 })…rename`；`:63 Effect.repeat(Schedule.spaced("5 seconds"))`；`:69 …owns(found) ? fs.remove…`；`:56 …return 0xc0de`；`:21 export const resolve = Effect.fn("cli.server-connection.resolve")…` | **定位准确 / 语义成立** | 本轮亲核（r1/r2 亦核） |
| §2.3 awaitPlugins 5s 门 `location.ts:36-47` | `packages/server/src/location.ts:36-47` | `:37 Effect.timeoutOrElse({`；`:38 duration: "5 seconds",` → ServiceUnavailableError | **定位准确 / 语义成立** | 本轮亲核 |
| v2 无 todo 路由组（OD-6 依据） | `packages/protocol/src/groups/` 目录列举 | 31 个组文件无 todo*（grep 无命中） | **成立** | 本轮亲核（r1 亦核） |
| 附录 B 事件字面量（message.content.updated/reasoning.started/inbox.delivered+enqueued/execution.started+interrupted/model.selected/agent.selected/text.started/tool.input.started；session.compacted `:7`；filesystem.changed `:9`） | `packages/schema/src/session-event.ts:144/431/209/216/240/254/81/70/393/477`；`session-compaction-event.ts:7`；`filesystem.ts:9` | 逐名 grep 命中（例 `:144 type: "session.message.content.updated"`） | **语义成立**；permission/form 两处路径歧义见 F-14 | 本轮亲核 |
| 附录 B `permission.ts:45-47`、`form.ts:173-175` | 实际在 **`packages/schema/src/`permission.ts:45-47、form.ts:173-175** | schema/permission.ts `:45 const Asked = ephemeral({ type: "permission.asked"…`、`:47 type: "permission.replied"`；schema/form.ts `:173-175 form.created/form.replied/form.cancelled` 三行 | 行号+语义在 schema 包**成立**；但方案未写包路径，且 §2.2 同名引用的是 protocol groups 文件（groups/form.ts 仅 26 行，:173-175 不可能） | 本轮亲核（→F-14） |
| §2.3 probe `/api/health` `probe.go:174`、shape arbiter `:196-216` | 本仓 `agent/opencode-web/probe.go:174/:196-202` | `:174 v2Health, v2Detail := probeHealth(ctx, c, "/api/health")`；`:197-201 shapePath := "/session"… if candidate == generationV2 { shapePath = "/api/session"; expected = shapeDataEnvelope }` | **定位准确 / 语义成立** | 本轮亲核（r1/r2 亦核） |
| §2.3 events.go C1 `:142-145`；quarantine 三处共用 `opencodeweb.go:328-336`；directive-003 `sessions.go:137-143`；unwrapDataEnvelope `:260-262`；client.go v2 query `:111-117`；permissions.go:104；main.go `:104-110`；session_discovery.go `:412/:423`；desktop_home_projects | 各对应文件 | `:142 // C1: the verified 1.18.18 event stream is /global/event only…`；`:329-333 // unsupportedGenerationDetail is the single shared quarantine wording used by InstanceStatus, clientFor, and RunDiagnostics…`；`:139-140 // Any bucket failure fails the whole global list — never a partial catalog (directive-003 OD-2).`；`:260-261 // unwrapDataEnvelope descends a v2 {"data": …} envelope when present…`；`:111 if c.gen == generationV2 {` + `:117 appendQuery = …HasSuffix(url, "/api/session")`；`:104 if c.Generation() == generationV2 {`；`:104-106 OPENCODE_WEB_*`；`:110 -opencode-source …(managed_local, …, service_discovery_future, disabled)`；`:412 prev, hadPrev := seen[id]`/`:423 if current != prev {` | **全部定位准确 / 语义成立**（「三处同步」注释与源码一致；client.go 旧注释措辞按 F-1 修订要求更正——方案 §2.2 已如实记录） | 本轮亲核（r1/r2 亦核） |
| §2.3/§3.2 触发链承重锚：监控循环 `RuntimeManager.swift:708-721`；triggerAutoRestart `:757-772`（stuck `:759/:765/:768`，maxStuckRestarts=5 `:417`）；stuck 清零 `:1070-1072`；fileReadHealth 先例 `:1015-1017`；agents detached 刷新 `:1019-1022`；resolve guard `:1157-1163`；syncDesktopConfig `:1198`；startOpenCodeManagedServer `:1264`；resolve 调用 `:536/:581` | 本仓同文件逐行 | `:709 private func startMonitoring()`；`:718 await self.pollManagementAPI()`；`:719 self.evaluateAutoRestart()`；`:722 try? await Task.sleep(nanoseconds: 3_000_000_000)`；`:757 private func triggerAutoRestart(reason: String)`；`:759 if stuckRestartCount >= maxStuckRestarts`；`:1070-1072 …if s == .ready \|\| s == .readyNoAgents { stuckRestartCount = 0`；`:1015-1017 fileReadHealth.restartRecommended == true → triggerAutoRestart`；`:1158 guard config.drivers.contains("opencode-web"), config.opencodeSource == .managedLocal else {`；`:1198 _ = openCodeManagedServer?.syncDesktopConfig(` | **语义全部成立**；监控循环区间边界注：实际函数体 :709-725、3s sleep 在 :722，方案写 :708-721，核心行 :718-719 命中、边界差 1-4 行，定位无歧义，不作意见 | 本轮亲核（F-2 承重锚；r2 亦核） |
| §2.3 resolver 槽位 `OpenCodeEndpointResolver.swift:25-27`；managed server `:238-296/:318-344/:385-388` | 本仓同文件 | `:25 case serviceDiscoveryFuture = "service_discovery_future"`；`:239 private func ensureRunningOnQueue`；`:321 func syncDesktopConfig(url:username:password:)`；`:385 private func adoptExistingProcessIfHealthy` | **定位准确（函数头差 1 行，区间含目标）/ 语义成立** | 本轮亲核（r1 亦核） |
| §2.3 think.md `:334/:852/:1799/:2714-2716/:2779`；GO_BRIDGE_ARCHITECTURE.md:87 | 本仓两文档逐行 | 五处标题/行文语义命中（:852 锚点表行「opencode-web 待补」；:2714-2716 历史文档降级；:87 表行**待补**） | **全部成立**（活文档未漂移） | 本轮亲核（r1 亦核；活文档不复用） |

## 3. r2-meta 四项问题的闭合判定（本轮定向复核）

| r2-meta 问题 | 闭合判定 | 闭合证据 |
| --- | --- | --- |
| 问题 1：r2 摘录 `service.ts:31`（+2 漂移） | **闭合** | 方案 §2.2 ensure 行补注 `:29`/`:30`，本轮亲核 `:29` 注释、`:30` command 字段逐字命中；`:31` 为 env 注释（A.9.1 旁证属实）。r2 报告按「轮次证据不回改」以 A.9 留痕，符合契约证据更正节 |
| 问题 2：r2 摘录 `location.ts:76/:77`（+2 漂移） | **闭合** | 方案 §2.2 机制二行补注 `:74`/`:75`，本轮亲核逐字命中；`:76-77` 为构造两行（A.9.2 旁证属实） |
| 问题 3：配套 iOS 工作树缺席 | **闭合** | §2.1 新增末行（路径/分支/完整提交/未提交状态/配套判定/零改动定性六要素齐备）；本轮独立亲核：工作树存在、`89f0a369…`、分支族正确、porcelain 空、两侧 worktree list 各仅一条本分支族——唯一配套判定成立 |
| 问题 4：r2 来源表缺旧 checkout 行 | **闭合（方案侧升级 + A.9.4 留痕）** | 方案 §2.1 该行本就存在（无回退），未提交状态由「未逐一核对」升级为「干净（纠错轮亲核）」；本轮亲核 dev @ `22e771f5…`、porcelain 空。r2 报告侧缺口以 A.9.4 留痕不回改，符合契约 |

附带核验：A.9 更正记录四条均含「原错误→来源→旧/新身份→影响范围与处置」四要素，
版本号 v1.1→v1.2 与新 SHA（`63421a82…`）已区分前后快照，符合契约「通过后纠错」
第 2/4 条；A.9 声明「不改动任何 R/OD/S/E/Gate/验收标准」与本轮全文重读结果一致。

**核验限制披露**：v1.1 文本无留档（docs 文件未跟踪、无历史），A.9 的「v1.2 与 r2
所批内容逐字一致」无法按字节独立复核。本轮以命题级核对替代：r2 闭合表与 §2.3
所引的全部承重内容（拆片/触发机制/envelope 分型/form 路由/OD 状态/Gate 映射/
E-3/E-5/E-7 行/附录 A.8/附录 B）在 v1.2 中均在且与 r2 描述一致；该限制不削弱
本轮结论（r2-meta 亦明示四项问题不推翻实质判断，纠错未触及实质内容）。

## 4. 历轮意见处置汇总

- r1 F-1~F-3（阻塞）/F-4~F-9（建议）：r2 判定全部闭合；本轮对其承重锚
  （SessionsQuery、location 双机制、form 四路由、触发链、监控循环、directive-003、
  unwrapDataEnvelope、probe/arbiter）全部重新亲核确认，**维持闭合**。
- r2 F-10~F-13（建议，实施期吸收）：v1.2 头部与 A.9 明示「维持 r2 判定，本轮不
  吸收，由实施期承接」——与 r2 各自闭合标准（实施期单测清单/S-1 实施时落定/
  exec-plan 拆分时落入）一致，**维持原判，非未闭合缺陷**。吸收点归档：
  - F-10（防风暴测试口径：stuck 计数 ready 即清零，`RuntimeManager.swift:1070-1072`
    本轮再核命中）→ S-1 触发判定单测 + E-6 断言补抖动用例；
  - F-11（S-6 活体验证排序标记）→ 实施排期口径；
  - F-12（resolver 镜像 XDG_STATE_HOME 覆盖）→ S-1 路径解析与官方
    `service-probe.ts:40` 对齐或记录分歧（锚点本轮再核命中）；
  - F-13（attach-first 仅限 managedLocal 档，不劫持 external_http/legacy_64667）→
    S-1 实施前在 §3.1/§3.2 落限定条件。

## 5. 意见（本轮新增）

### F-14 [建议] 附录 B 两处事件锚点缺包路径，与 §2.2 同名 protocol groups 文件冲突

- 位置：附录 B「permission.asked / permission.replied（`permission.ts:45-47`）」与
  「form.created / form.replied / form.cancelled（`form.ts:173-175`）」两行。
- 证据：`permission.ts` 与 `form.ts` 在上游**两个包中重名**——
  `packages/protocol/src/groups/`（135 行 / 26 行）与 `packages/schema/src/`
  （67 行 / 177 行）。方案 §2.2 同名引用的是 groups 文件（路由锚点
  `permission.ts:23-124`、`form.ts:12`，本轮亲核均正确）；而附录 B 的
  `:45-47`/`:173-175` 只能在 schema 包命中（本轮亲核：schema/permission.ts `:45`
  asked、`:47` replied；schema/form.ts `:173-175` 三个事件字面量逐行命中）。
  groups/form.ts 仅 26 行，`:173-175` 在 §2.2 所指文件中不可能存在——实施者按
  §2.2 的文件路径找附录 B 行号必然扑空。
- 影响：附录 B 是 S-3/E-2 映射的起草底表。行号与语义本身全部正确，设计命题与
  E-2 gate 不受影响；仅引用可核验性受损（与 r1-F-8「缺包路径致定位歧义」同类，
  旧问题漏检，非 v1.2 引入——v1.1 附录 B 未在纠错范围内，r1/r2 均未标注）。
- 修订方向：两行补注 `packages/schema/src/` 前缀（一句话级）；或统一在附录 B 表头
  注明「未写全路径的事件文件均在 packages/schema/src/」。
- 闭合标准：实施期 E-2 样本校正附录 B 时顺带补齐即可，无需再轮评审。

## 6. 复审处置与剩余门

- 原意见闭合：r1 F-1~F-9 闭合（r2 + 本轮承重锚重核）；r2-meta 问题 1~4 闭合（§3）；
  r2 F-10~F-13 维持实施期吸收（§4）。
- 新意见：F-14（建议）。本轮无阻塞意见。
- 仍待证据/决定/授权（与阻塞意见分列；通过不改变其状态）：
  - 证据门（实施期捕获，方案登记完备）：E-2（SSE 帧，gate S-3 与 S-2b）、
    E-3（prompt+目录三态矩阵，gate S-5）、E-4（parts schema+分页对账，gate S-4）、
    E-5（permission/form wire，gate S-5）、E-6（轮换/双向切换端到端，gate 验收
    标准 4）、E-7（TCC 可达矩阵，gate S-6 分支说明）、E-8（503 重试边界，gate
    S-2a 重试验收）。
  - 产品决定门：OD-2b（ensure-start）、OD-5（聚合语义修订）；OD-1/3/4/6 推荐
    默认建议 owner 启动实施时顺带确认。
  - 授权边界：本通过不构成实施授权；S-1~S-8 由 owner 决定是否启动 exec-plan。
    E-6/E-7 复现涉及本机 live 服务替换，实施期按方案登记的取证方式与清理纪律执行。
- 外部格式专项（audit-plan）结论：**通过（就已声明形状而言）**——本轮活体轻量
  复测（E-1 身份链、服务在听、401 负例）与方案一致；E-2/E-3/E-4/E-5 为显式
  pending 且实现被 gate，符合待捕项裁定；F-14 为引用精度问题，不改判。

## 7. 结论

v1.2 对 r2-meta 四项问题的纠错全部落地且经本轮独立亲核成立：两处子行号补注与
上游源码逐字吻合（含 A.9 旁证描述），来源表补齐配套 iOS 工作树（六要素齐备、
唯一配套判定亲核成立）与旧 checkout 亲核升级，A.9 更正记录符合契约证据更正
纪律。全文重读确认纠错未触及任何设计命题、门控、切片或验收标准；承重锚点独立
重核全部命中，仅余 1 条建议级包路径歧义（F-14，实施期顺带更正）。r2 的 APPROVED
通过结论对 v1.2 恢复有效。**verdict: APPROVED**——设计层面通过；实施就绪度以
§1 所列门为准，逐片开工前照常校验对应 E/OD/授权。
