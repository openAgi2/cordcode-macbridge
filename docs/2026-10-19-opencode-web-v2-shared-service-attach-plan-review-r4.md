# 评审报告 r4：OpenCode Web 接入 v2 共享服务（generation-2 attach）设计方案

- 评审轮次：第 4 轮（复审；r3 APPROVED（对 v1.2）→ 修订者采纳 F-14 产出 v1.3 后的通过结论延续复核）
- 评审日期：2026-10-19
- verdict：**APPROVED（通过；r3 的通过结论对 v1.3 延续有效）**
- blockers：0；advisories：本轮新增 1（F-15），随行维持 r2 判定 4 条（F-10~F-13，实施期吸收）
- 契约版本：plan-contract-v1.1（plan-review skill，含 audit-plan 专项接入）
- 评审员模式披露：派发简报标「延续」。实际情况与 r3 相同：本轮为**新会话**，通过完整
  读取 r1/r2/r2-meta/r3 四份报告重建上下文（非 r1/r2 同一进程）；共享约定推荐的
  「新评审员独立阅读后核对历史」模式。缓解措施：对 v1.3 新增内容（附录 B 两行、
  A.10）与 F-14 采纳锚点全部独立亲核；对承重锚按 r2-meta 漏检类（上游 TS 子行号）
  抽样重核；来源身份、配套工作树、活体身份全部本轮独立复测；其余未变命题按
  命题/锚点粒度复用并在 §2.3 标注。

## 1. 结论与实施就绪

- verdict: **APPROVED**
- plan: `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.3
  （r3 建议 F-14 采纳修订版），SHA-256
  `78ea429895a9c8a859d94a93fa24dec20f3c14f41268a2f6b59b9cac38d000c7`
  （本轮派发哈希，收到后重算一致）。本轮范围：v1.3 改动面定向复核
  （头部改动声明 + 附录 B 两行 + 附录 A.10）+ F-14 采纳锚点独立亲核 + 全局
  门控/OD/E/S/验收回归重查 + 承重锚抽样重核（防历轮同类漏检）+ 来源/活体身份
  独立复测。
- 总体判断：**v1.3 的改动严格限于其头部声明的三处**（§1 头部、附录 B 两行补
  `packages/schema/src/` 前缀、新增附录 A.10 更正记录），本轮对全文重读并按命题级
  与 r3 所引 v1.2 内容比对，未发现任何 R/OD/S/E/Gate/验收标准被改动或引入回归。
  F-14 采纳的两处锚点经本轮独立亲核逐字命中（含 135/26/67/177 行数旁证与
  「groups/form.ts 仅 26 行故 `:173-175` 不可能」的排除推理核实）。A.10 更正记录
  含契约四要素、区分前后快照、r3 报告不回改以记录留痕，符合证据更正纪律。
  全局门控三处一致（S-2b 无法按切片表绕行），OD×E×S 关系无悬空引用。仅发现
  1 条建议级新问题（F-15：§2.1 本仓未跟踪文件计数较 v1.3 快照滞后 1 个），不
  影响任何设计命题、门控或验收。
- 结论定性：按共享约定「通过后纠错」——r3 对 v1.2 的 APPROVED 在 v1.3 上**延续
  有效**（修订仅收编 r3 自己的建议级意见 F-14，非对 r3 判定的推翻；修订者头部
  已如实声明）。F-15 为记录精度级，与 F-10~F-13 处置同级，不构成再轮修订的必要。
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

### 2.1 来源身份表（本轮亲核；按 r2-meta/r3 口径含全部四仓，含配套工作树）

| 仓库/工作树 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 核对结果 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 追踪文件干净；未跟踪恰 **5** 个 docs 文件（方案 + r1/r2/r2-meta/r3 报告；本轮 porcelain 亲核，r4 报告将成第 6 个） | 提交/分支与方案 §2.1 一致；未跟踪计数与方案 §2.1 所写「4 个」不符（→F-15） |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，`git describe` = `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净（porcelain 空） | 与方案 §2.1 一致 |
| opencode（旧 checkout，仅备注） | `/Users/jacklee/Projects/opencode` | `dev` | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | 干净（porcelain 空） | 与方案 §2.1 一致（不作为锚点） |
| cordcode-ios（配套工作树） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52` | 干净（porcelain 空） | 与方案 §2.1 v1.2 新增行一致 |

- 配套工作树判定本轮独立亲核：两侧 `git worktree list` 各仅存在一条
  `feat/claude-long-session-projection-loading` 分支族工作树（MacBridge 侧其余为
  main/native-message-timeline/plan-approval/widget-support 族；iOS 侧同类），
  唯一配套对应成立——与方案 §2.1/A.9.3 的判定记录一致。
- 运行身份（本轮活体复核，只读、凭据不落输出）：v2 共享服务 `127.0.0.1:49374`
  在听（进程 PID 29415，与 r1/r2/r3 各轮一致，服务未更替）；
  `~/.local/state/opencode/service.json` 的 `{version:2.0.32, pid:29415,
  url:http://127.0.0.1:49374}`（仅打印此三键），注册 pid == 监听进程；
  `GET /api/info` 无认证 → 401。v1 managed `127.0.0.1:4096` 在听（PID 28164）。
  与方案 §2.1 运行身份及 A.1/A.8.4 一致。

### 2.2 本轮检查范围

- v1.3 改动面定向复核：§1 头部（版本链 v1.0→v1.3、状态链 r1→r3、本轮改动范围
  声明）；附录 B 两行（permission/form 事件锚点包路径前缀）；附录 A.10 更正记录
  逐项事实核（锚点、行数旁证、排除推理、四要素格式、快照区分）。
- 全量回归重查：R→S→验收映射、OD×E×S 关系（OD-2/OD-5 pending 仅 gate 各自可选
  切片）、S-2a/S-2b 拆片后 §3.3/§4/§5 三处门控一致性、依赖 DAG 无环、阻塞清单
  与 §4/§5 一致性、OD 表六项状态、E 表八项状态、附录 A.8/A.9 完整性。
- 锚点独立重核（防 r2-meta 同类漏检，重点覆盖上游 TS 子行号类）：上游 14 处
  （含 F-14 两处采纳锚点 + 文件唯一性枚举 + r2-meta 两处落点复核 + F-1/F-6 承重
  锚抽样）；本仓 Go 6 处、Swift 6 处承重锚抽样。全部命中。
- audit-plan 专项：**已运行**（延续）。本轮对 v1.3 写成事实的外部形状重读：v1.3
  相对 v1.2 的形状面变化仅在附录 B 两条引用路径，其行号/字面量本轮亲核吻合；
  E-1 活体轻量复测一致（401 负例 + 身份链）；E-2/E-3/E-4/E-5 维持 pending 且
  对应实现被 gate。
- 未核/低风险未核（结论不受影响）：附录 A 历史时间窗细节（A.2/A.5/A.6/A.7 过去
  时观测，r1~r3 已按现存状态侧证，本轮不复演）；OD-2b desktop staged 二进制
  解析顺序（owner 未裁决项）；InstalledView 文案现状（展示层）；v1.2 字节级
  留档不存在（docs 未跟踪、无历史）——与 r3 同一限制，本轮以命题级核对替代
  （见 §3 限制披露）。

### 2.3 锚点核验表（本轮亲核主干；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位/语义 | 亲核或复用 |
| --- | --- | --- | --- | --- |
| 〔F-14 采纳落点 1〕附录 B「permission.asked / permission.replied（`packages/schema/src/permission.ts:45-47`）」 | opencode-v2 `packages/schema/src/permission.ts:45/:47` | `:45 const Asked = ephemeral({ type: "permission.asked", schema: Request.fields })`；`:46 const Replied = ephemeral({`；`:47   type: "permission.replied",` | **定位准确 / 语义成立** | 本轮亲核 |
| 〔F-14 采纳落点 2〕附录 B「form.created / form.replied / form.cancelled（`packages/schema/src/form.ts:173-175`）」 | `packages/schema/src/form.ts:173-175` | `:173 const Created = ephemeral({ type: "form.created", schema: { form: Info } })`；`:174 const Replied = ephemeral({ type: "form.replied", … })`；`:175 const Cancelled = ephemeral({ type: "form.cancelled", … })` | **定位准确 / 语义成立**（三行逐字命中） | 本轮亲核 |
| 〔A.10 行数旁证〕groups/permission.ts 135 行、groups/form.ts 26 行、schema/permission.ts 67 行、schema/form.ts 177 行 | 四文件 `wc -l` | 135 / 26 / 67 / 177 | **与 A.10 所写逐项一致**；groups/form.ts 仅 26 行故 `:173-175` 在 §2.2 所指 groups 文件不可能存在（排除推理成立） | 本轮亲核 |
| 〔A.10 唯一性声明〕session-event / event-manifest / session-compaction-event 上游唯一 | `find packages -name …` 枚举 | 三文件各仅 1 处命中，均在 `packages/schema/src/` | **成立** | 本轮亲核 |
| 〔A.10 filesystem.ts 表述〕「与 §2.2 无同名引用冲突」（注意：A.10 对此文件**未**声称上游唯一） | 同上 find 枚举 + 三文件 `:1-12` 对读 | 上游有 3 个 `filesystem.ts`（core/src、schema/src、schema/src/v1）；仅 `schema/src/filesystem.ts:9` 为 `type: "filesystem.changed",`（v1 变体声明 `file.edited`，core 为实现再导出）；§2.2 无任何 filesystem.ts 引用 | **成立**——A.10 措辞精确（deliberately 未写「唯一」），附录 B `filesystem.ts:9` 的裸 basename 虽三处重名，但 `filesystem.changed` 字面量+行号唯一解析到 schema 文件，且与 §2.2 无交叉冲突；不作意见 | 本轮亲核 |
| §2.2 列表过滤 `SessionsQuery.directory`（`session.ts:168-174`，cursor `:173`） | `packages/protocol/src/groups/session.ts:168-174` | `:168 export const SessionsQuery = Schema.Struct({`；`:170 directory: AbsolutePath.pipe(Schema.optional),`；`:173 cursor: SessionsCursor.pipe(Schema.optional),` | **定位准确 / 语义成立** | 本轮亲核（F-1 承重锚；r2/r3 亦核） |
| §3.6 form reply/cancel（`session.ts:842/:858`） | 同文件 | `:842 HttpApiEndpoint.post("session.form.reply", "/api/session/:sessionID/form/:formID/reply", {`；`:858 HttpApiEndpoint.delete("session.form.cancel", "/api/session/:sessionID/form/:formID", {` | **定位准确 / 语义成立** | 本轮亲核（F-6 承重锚；r2/r3 亦核） |
| 〔r2-meta 问题 1 落点，v1.3 未动〕§2.2 ensure 行 `:29`/`:30` 补注 | `packages/client/src/service.ts:29-31` | `:29 /** Service command and arguments. Defaults to \`opencode serve --service\`. */`；`:30 readonly command?: ReadonlyArray<string>`；`:31 /** Environment variables added to the inherited service process environment. */` | **定位准确 / 语义成立**（`:31` 确为 env 注释，A.9.1 旁证持续属实） | 本轮亲核（r3 亦核） |
| 〔r2-meta 问题 2 落点，v1.3 未动〕§2.2 机制二行 `:74`/`:75` 补注 | `packages/server/src/location.ts:74-75` | `:74 query.get("location[directory]") \|\|`；`:75 (request.headers["x-opencode-directory"] ? decode(request.headers["x-opencode-directory"]) : process.cwd())` | **定位准确 / 语义成立** | 本轮亲核（r3 亦核） |
| 附录 B `session.message.content.updated`（session-event.ts） | `packages/schema/src/session-event.ts:144` | `:144   type: "session.message.content.updated",` | **定位准确 / 语义成立** | 本轮亲核（r2/r3 亦核） |
| 附录 B `session.compacted`（`session-compaction-event.ts:7`） | 同文件 `:7` | `:7   type: "session.compacted",` | **定位准确 / 语义成立** | 本轮亲核（r1/r3 亦核） |
| §3.4 `event-manifest.ts:41-72`（事件清单声明区） | `packages/schema/src/event-manifest.ts:41-52`（区间头段） | `:41 const coreDefinitions = Event.inventory(...SessionEvent.Definitions)`；`:43-52 foundationDefinitions = Event.inventory(...LocationEvent/ModelsDev/Credential/Integration/Provider/Model/Agent...coreDefinitions)` | 区间命中 / 语义成立 | 本轮亲核（r1/r3 亦核区间） |
| §2.2 全局 form `form.ts:12`（`GET /api/form`） | `packages/protocol/src/groups/form.ts:12`；对照 `packages/schema/src/form.ts:12`（空行） | `:12 HttpApiEndpoint.get("form.list", "/api/form", {` | **定位准确 / 语义成立**——内容锚（`GET /api/form` 路由字面量）在两同名文件中唯一解析到 groups 文件，无 F-14 类冲突 | 本轮亲核（r1/r2/r3 亦核） |
| §2.3 probe `/api/health`（`probe.go:174`）；shape arbiter | 本仓 `agent/opencode-web/probe.go:174` | `:174 v2Health, v2Detail := probeHealth(ctx, c, "/api/health")` | **定位准确 / 语义成立**（必改判断成立） | 本轮亲核（r1/r2/r3 亦核） |
| §2.3 directive-003（`sessions.go:137-143`）；unwrapDataEnvelope（`:260-262`） | 本仓同文件 | `:139-140 // Any bucket failure fails the whole global list — never a partial catalog (directive-003 OD-2).`；`:260-261 // unwrapDataEnvelope descends a v2 {"data": …} envelope when present…` | **定位准确 / 语义成立** | 本轮亲核（r1/r2/r3 亦核） |
| §2.3/§3.3 quarantine 三处共用（`opencodeweb.go:328-336`） | 本仓同文件 | `:329-330 // unsupportedGenerationDetail is the single shared quarantine wording used by InstanceStatus, clientFor, and RunDiagnostics…` | **定位准确 / 语义成立** | 本轮亲核（r1/r2/r3 亦核） |
| §2.2 现行 client 目录参数行为（`client.go:111-117`）及其旧注释措辞 | 本仓 `agent/opencode-web/client.go:111-117` | `:111 if c.gen == generationV2 {`；`:114-116 // … the x-opencode-directory header is a 1.18 convention that stays off the v2 wire.`；`:117 appendQuery = req.Method == http.MethodGet && strings.HasSuffix(url, "/api/session")` | **定位准确 / 语义成立**——仓库现行注释仍是旧措辞，方案 §2.2 已如实把「注释措辞更正」登记为实施期必改项（未实施，符合本任务零代码改动授权） | 本轮亲核（r2/r3 亦核） |
| §2.3 env 通道 + source flag（`main.go:104-110`） | 本仓 `go-bridge/main.go:104-110` | `:104-106 OPENCODE_WEB_BASE_URL/_SERVER_USERNAME/_SERVER_PASSWORD`；`:110 flag.String("opencode-source", "", "…(managed_local, external_http, legacy_64667, service_discovery_future, disabled)")` | **定位准确 / 语义成立** | 本轮亲核（r1/r2 亦核） |
| §3.2 触发链承重锚：监控循环 `:718-722`；triggerAutoRestart `:757-768`；resolve guard `:1157-1161`；syncDesktopConfig `:1198` | 本仓 `RuntimeManager.swift` 逐行 | `:718 await self.pollManagementAPI()`；`:719 self.evaluateAutoRestart()`；`:722 try? await Task.sleep(nanoseconds: 3_000_000_000)`；`:757 private func triggerAutoRestart(reason: String)`；`:759 if stuckRestartCount >= maxStuckRestarts`；`:765 stuckRestartCount += 1`；`:768 scheduleRestart(trigger: .automatic)`；`:1158 guard config.drivers.contains("opencode-web"), config.opencodeSource == .managedLocal else {`；`:1198 _ = openCodeManagedServer?.syncDesktopConfig(` | **全部定位准确 / 语义成立** | 本轮亲核（r2/r3 亦核） |
| §2.3 resolver 槽位（`OpenCodeEndpointResolver.swift:25-27`） | 本仓同文件 | `:25 case serviceDiscoveryFuture = "service_discovery_future"` | **定位准确 / 语义成立** | 本轮亲核（r1 亦核） |
| §3.4 dedupe/watchdog 机制引用（events_dedupe / sse_watchdog） | 本仓 `agent/opencode-web/events_dedupe_test.go`、`sse_watchdog_test.go`（文件存在性） | 两测试文件在列 | **成立**（机制引用非虚构） | 本轮亲核 |

复用（命题/锚点粒度，两侧文本未变且源码身份未变——上游同 commit `843a6c51`、
本仓同 HEAD `1fe9c65d`，行号无漂移）：r1/r2/r3 已核的 server.ts /api/info 与
ServerInfo、event.ts SSE 端点与 volatile 契约、message.ts:68、CRUD 路由组
（session.ts:182/221/287/300/362/396-407/746/330/346）、permission 路由
（groups permission.ts:23-124、reply :117）、project.ts:6,17、service-probe/
service/service-registration/service-config/server-connection 全组、v2 无 todo
路由组、OpenCodeManagedServer 三段、desktop_home_projects、permissions.go:104、
session_discovery.go:403-433、think.md 五处、GO_BRIDGE_ARCHITECTURE.md:87。
以上均与 v1.3 文本引用一致，本轮未逐一重摘（低风险命题抽样已覆盖同类）。

## 3. v1.2→v1.3 改动面与 F-14 闭合判定（本轮定向复核）

| 检查项 | 判定 | 证据 |
| --- | --- | --- |
| 附录 B 两行补注 `packages/schema/src/` 前缀 | **完成且准确** | §2.3 前四行：`:45/:47`、`:173-175` 逐字命中；行数旁证 135/26/67/177 一致 |
| 其余附录 B 行不动（session-event/event-manifest/session-compaction-event 唯一；filesystem.ts 无 §2.2 冲突） | **决策正确** | find 枚举 + 三 filesystem.ts 对读：三事件文件上游唯一；filesystem.ts 虽三处重名但字面量+行号唯一解析且无跨节冲突（A.10 对此措辞精确，未虚称唯一） |
| 头部改动范围声明（仅头部、附录 B 两行、A.10；R/OD/S/E/Gate/验收逐字未动） | **命题级核实一致** | 全文重读 + 与 r3 所引 v1.2 内容逐命题比对：§2.2 两处子行号补注、§2.1 四行来源表、§3.2 触发机制、§3.3 拆片、§3.5 分型、§3.10 OD 六项、§4 Gate 映射、§5 切片表、附录 A.8/A.9 均与 r2/r3 所批内容一致；未检出任何设计命题变化 |
| A.10 更正记录契约合规 | **合规** | 四要素（原错误→来源→旧/新身份→影响范围与处置）齐备；版本 v1.2→v1.3 区分快照；r3 报告不回改以本记录留痕 |
| F-14 的 r3 闭合标准（实施期顺带补齐）被提前收编 | **合法** | 修订量一句话级、非推翻 r3 判定；头部已声明采纳理由 |

**核验限制披露**（与 r3 同源）：v1.2 无字节留档（docs 未跟踪、无历史），
「v1.3 与 r2/r3 所批内容逐字一致」无法按字节独立复核；本轮以命题级核对替代——
r2 闭合表与 r2/r3 锚点表所引全部承重内容在 v1.3 中均在且一致。该限制不削弱
本轮结论（改动面声明与实际检出差异为零）。

## 4. 历轮意见处置汇总

- r1 F-1~F-3（阻塞）/F-4~F-9（建议）：r2 判定闭合；r3 承重锚重核维持；本轮对
  F-1（SessionsQuery/location 双机制）、F-2（触发链五锚）、F-3（三处门控）、
  F-5（unwrapDataEnvelope/directive-003）、F-6（form 四路由中 reply/cancel）
  承重锚抽样重核，**维持闭合**。
- r2-meta 问题 1~4：v1.2 闭合（r3 核）；本轮对两处落点（service.ts :29-31、
  location.ts :74-75）复核命中，**维持闭合**。
- r3 F-14（建议）：**本轮判定闭合**（§3 表）——采纳准确、无回归。
- r2 F-10~F-13（建议，实施期吸收）：v1.3 头部与 A.9/A.10 未触碰，维持 r2/r3
  原判（实施期承接：F-10 抖动用例入 S-1 单测/E-6 断言；F-11 S-6 活体排序口径；
  F-12 resolver 镜像 XDG_STATE_HOME；F-13 attach-first 限 managedLocal 档）。
  **非未闭合缺陷**。

## 5. 意见（本轮新增）

### F-15 [建议] §2.1 本仓未跟踪文件计数较 v1.3 快照滞后（4 → 实际 5）

- 位置：§2.1 来源表第一行「未提交状态=干净；未跟踪 4 个 docs 文件（本方案 +
  r1/r2/r2-meta 报告；2026-10-19 纠错轮亲核）」。
- 证据：本轮 porcelain 亲核，工作树未跟踪 docs 文件实为 **5** 个（r3 报告在
  v1.3 修订写作时点已存在，成为第 5 个；r4 报告将成第 6 个）。计数停留在 v1.2
  纠错轮时点，v1.3 修订未刷新该行。
- 影响：仅来源清单记录精度（P0 模板「未提交状态逐项列出」的时效性）；不涉及
  任何设计命题、门控或验收——被列四仓的路径/分支/完整提交均准确，滞后的只是
  本仓未跟踪文件枚举数。
- 修订方向：下次修订（或实施启动时的来源门复核）顺带刷新计数，或改写为
  「未跟踪 N 个 docs 文件（方案 + 已落盘历轮评审报告；计数截至 YYYY-MM-DD HH:MM）」
  的时点限定式表述。一句话级。
- 闭合标准：§2.1 该行计数与快照时点一致即可；无需为本条单独再轮评审。

## 6. 复审处置与剩余门

- 原意见闭合：r1 F-1~F-9 闭合（r2 + r3 + 本轮抽样重核）；r2-meta 问题 1~4 闭合
  （r3 + 本轮落点复核）；r3 F-14 闭合（本轮）；r2 F-10~F-13 维持实施期吸收。
- 新意见：F-15（建议）。本轮无阻塞意见。
- 仍待证据/决定/授权（与阻塞意见分列；通过不改变其状态）：
  - 证据门（实施期捕获，方案登记完备）：E-2（SSE 帧，gate S-3 与 S-2b）、
    E-3（prompt+目录三态矩阵，gate S-5）、E-4（parts schema+分页对账，gate S-4）、
    E-5（permission/form wire，gate S-5）、E-6（轮换/双向切换端到端，gate 验收
    标准 4）、E-7（TCC 可达矩阵 desktop 起 vs 手动起，gate S-6 分支说明）、
    E-8（503 重试边界，gate S-2a 重试验收）。
  - 产品决定门：OD-2b（ensure-start，owner 未裁决，仅 gate 可选切片）、OD-5
    （聚合语义修订，gate S-6 分支选择）；OD-1/3/4/6 推荐默认建议 owner 启动
    实施时顺带确认。
  - 实施期吸收项：F-10（抖动用例）、F-11（S-6 活体排序口径）、F-12（XDG 路径
    parity）、F-13（attach-first 适用档位）、F-15（来源计数刷新）、附录 B 随
    E-2 样本逐条校正。
  - 授权边界：本通过不构成实施授权；S-1~S-8 由 owner 决定是否启动 exec-plan
    （方案 §6 已声明）。E-6/E-7 复现涉及本机 live 服务替换，实施期按方案登记
    的取证方式与清理纪律执行。
- 外部格式专项（audit-plan）结论：**通过（就已声明形状而言）**——v1.3 的形状
  面变化仅附录 B 两条引用路径，本轮亲核吻合；E-1 活体轻量复测一致（401 负例 +
  pid 链）；E-2/E-3/E-4/E-5 为显式 pending 且对应实现被 gate，符合待捕项裁定。

## 7. 结论

v1.3 是对 r3 建议级意见 F-14 的一句话级收编：附录 B 两处包路径补注经本轮独立
亲核逐字命中（含行数与排除推理旁证），A.10 更正记录符合契约证据更正纪律，头部
声明的改动面与实际检出差异为零，全部设计命题、门控与验收标准相对 r2/r3 所批
内容未变。来源身份（四仓 + 配套判定）与活体身份（49374/29415、4096/28164、
401 负例）本轮独立复测全部一致。仅余 1 条建议级记录精度问题（F-15）与历轮
实施期吸收项。r3 的 APPROVED 对 v1.3 延续有效。**verdict: APPROVED**——设计
层面通过；实施就绪度以 §1 所列门为准，逐片开工前照常校验对应 E/OD/授权。
