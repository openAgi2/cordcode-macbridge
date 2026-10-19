# 评审报告 r5：OpenCode Web 接入 v2 共享服务（generation-2 attach）设计方案

- 评审轮次：第 5 轮（复审；r4 APPROVED（对 v1.3）→ 修订者采纳 F-15 产出 v1.4 后的
  通过结论延续复核。本轮为 plan-review-loop 轮次上限轮。）
- 评审日期：2026-10-19
- verdict：**APPROVED（通过；r4 的通过结论对 v1.4 延续有效；r2→r3→r4→r5 通过链闭合）**
- blockers：0；advisories：本轮新增 **0**（无新发现问题，不凑数）；随行维持 r2 判定
  4 条（F-10~F-13，实施期吸收）+ 附录 B 随 E-2 样本逐条校正（实施期）
- 契约版本：plan-contract-v1.1（plan-review skill，含 audit-plan 专项接入）
- 评审员模式披露：派发简报标「延续」。实际情况与 r3/r4 相同：本轮为**新会话**，通过
  完整读取 r1/r2/r2-meta/r3/r4 五份报告重建上下文（非 r1/r2 同一进程），属共享约定
  推荐的「新评审员独立阅读后核对历史」模式。缓解措施：对 v1.4 新增内容（§1 头部、
  §2.1 一处单元格、附录 A.11）与 F-15 闭合逐项独立亲核；对 r2-meta 漏检类锚点
  （上游 TS 子行号）与两侧承重锚抽样重核；来源身份、配套工作树判定、活体身份
  全部本轮独立复测；其余未变命题按命题/锚点粒度复用并在 §2.3 标注。

## 1. 结论与实施就绪

- verdict: **APPROVED**
- plan: `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.4
  （r4 建议 F-15 采纳修订版），SHA-256
  `65c30de93549a7e5ef72a8968e1161e96e3be08f6940c79e16aad6ef814db843`
  （本轮派发哈希，收到后重算一致）。本轮范围：v1.4 改动面定向复核（头部改动声明
  + §2.1 本仓行一处单元格 + 附录 A.11）+ F-15 闭合判定 + 全局门控/OD/E/S/验收回归
  重查 + 承重锚抽样重核（防历轮同类漏检）+ 来源/配套工作树/活体身份独立复测。
- 总体判断：**v1.4 的改动严格限于其头部声明的三处**（§1 头部、§2.1 本仓行一处
  单元格、附录 A.11），本轮全文重读并按命题级与 r4 所引 v1.3 内容比对，未发现任何
  R/OD/S/E/Gate/验收标准被改动或引入回归。F-15 采纳核实成立：本轮 porcelain 亲核
  未跟踪 docs 文件恰 6 个（本方案 + r1/r2/r2-meta/r3/r4 报告），与 §2.1 改写后
  的时点限定式表述逐项一致（计数时点 03:52 有旁证：r4 报告 mtime 03:51:05 已落盘、
  其后至本轮检查无新增未跟踪文件）；A.11 更正记录含契约四要素、区分前后快照、
  r4 报告不回改以记录留痕。全局门控三处一致（S-2b 无法按切片表绕行），OD×E×S
  关系无悬空引用。本轮抽样重核的上游/本仓承重锚与活体身份链全部命中，未发现
  新问题。
- 结论定性：按共享约定「通过后纠错」——r4 对 v1.3 的 APPROVED 在 v1.4 上**延续
  有效**（修订仅收编 r4 自己的建议级意见 F-15，一句话级，非对 r4 判定的推翻；
  修订者头部已如实声明）。本轮零新意见：按复审纪律，同一快照经四轮评审后仅余
  记录精度级建议且已全部收编时，不为继续循环制造意见；方案通过链
  r2（v1.1，经 r2-meta 纠错恢复）→ r3（v1.2）→ r4（v1.3）→ r5（v1.4）闭合。
- implementation_readiness（通过 ≠ 授权，方案 §6 已声明实施另需 owner 启动 exec-plan）：
  - 可开工（受既定门约束）：S-1（resolver + 触发机制，E-1 verified、OD-1 推荐
    默认可追溯；含 F-10 抖动用例、F-12 XDG 路径 parity、F-13 attach-first 档位
    限定三项实施期吸收点）、S-2a（probe 端点替换，E-8 边界已知）、S-7 文档部分。
  - 仍受证据门阻塞：S-3（E-2）、S-4 映射固化（E-4）、S-5（E-3/E-5）、
    **S-2b 解除隔离（E-2/E-3/E-5 齐备 + S-3~S-5 代码就位；§3.3/§4/§5 三处一致，
    本轮重查无法按切片表绕行）**、S-6 分支（OD-5 裁决 + E-7）、验收标准 4 的
    切换/轮换验收（E-6）。
  - 产品决定门：OD-2b（ensure-start，owner 未裁决，仅 gate 可选切片）、OD-5
    （fail-all vs 跳过+告警，owner 未裁决，gate S-6 分支选择）；OD-1/3/4/6 推荐
    默认建议 owner 启动实施时顺带确认。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核；按 r2-meta/r3/r4 口径含全部四仓，含配套工作树）

| 仓库/工作树 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 核对结果 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 追踪文件干净；未跟踪恰 **6** 个 docs 文件（方案 + r1/r2/r2-meta/r3/r4 报告；本轮 porcelain 亲核，r5 报告将成第 7 个，方案 §2.1 已声明时点限定式表述不逐轮回改） | 提交/分支/未跟踪枚举与方案 §2.1 v1.4 改写后逐项一致 |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，`git describe` = `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净（porcelain 空） | 与方案 §2.1 一致 |
| opencode（旧 checkout，仅备注） | `/Users/jacklee/Projects/opencode` | `dev` | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | 干净（porcelain 空） | 与方案 §2.1 一致（不作为锚点） |
| cordcode-ios（配套工作树） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52` | 干净（porcelain 空） | 与方案 §2.1 v1.2 新增行一致 |

- 配套工作树判定本轮独立亲核：两侧 `git worktree list` 各仅存在一条
  `feat/claude-long-session-projection-loading` 分支族工作树（MacBridge 侧其余为
  main / native-message-timeline / plan-approval / widget-support 族及 prunable
  临时项；iOS 侧同类），唯一配套对应成立——与方案 §2.1/A.9.3 的判定记录一致。
- 运行身份（本轮活体复核，只读、凭据仅入进程内存与请求头、不落任何输出）：
  v2 共享服务 `127.0.0.1:49374` 在听（进程 PID 29415，与 r1/r2/r3/r4 各轮一致，
  服务未更替）；`~/.local/state/opencode/service.json` 白名单键
  `{version:2.0.32, pid:29415, url:http://127.0.0.1:49374}`，注册 pid == 监听
  进程；`GET /api/info` 无认证 → 401、带 Basic → 200 且响应 `{version:2.0.32,
  pid:29415, urls:[http://127.0.0.1:49374]}`——身份链三方一致（E-1 四度+本轮
  五度确认）。v1 managed `127.0.0.1:4096` 在听（PID 28164）。与方案 §2.1 运行
  身份及 A.1/A.8.4 一致。

### 2.2 本轮检查范围

- v1.4 改动面定向复核：§1 头部（版本链 v1.0→v1.4、状态链 r1→r4、本轮改动范围
  三项声明）；§2.1 本仓行「未提交状态」单元格（时点限定式改写）；附录 A.11
  更正记录逐项事实核（原错误描述、旧/新身份、6 个计数与 porcelain 对账、
  「追踪文件干净」声明、四要素格式、快照区分）。
- 全量回归重查：R→S→验收映射、OD×E×S 关系（OD-2/OD-5 pending 仅 gate 各自可选
  切片）、S-2a/S-2b 拆片后 §3.3/§4/§5 三处门控一致性、依赖 DAG 无环、阻塞清单
  与 §4/§5 一致性、OD 表六项状态、E 表八项状态、附录 A.8/A.9/A.10/A.11 完整性、
  附录 B（schema 包路径前缀两行维持 v1.3 采纳态）。
- 锚点独立重核（防 r2-meta 同类漏检，重点为上游 TS 子行号类与两侧承重锚）：
  上游 7 处（service.ts:27-35 含 `:29`/`:30`/`:31` 三行旁证、location.ts:71-79
  含 `:74`/`:75`/`:76-77` 构造行旁证、SessionsQuery `:168-174`、form 四路由
  `:796/:827/:842/:858`、schema/permission.ts `:45-47`、schema/form.ts
  `:173-175`、四文件行数旁证 135/26/67/177）；本仓 Go 8 处、Swift/管理层 6 处、
  活文档 6 处。全部命中（§2.3）。
- audit-plan 专项：**已运行**（延续）。v1.4 相对 v1.3 的形状面变化为零（改动仅
  来源清单记录单元格与更正附录），本轮对写成事实的外部形状维持 r1~r4 核验结论
  并对 E-1 身份链活体复测一致；E-2/E-3/E-4/E-5 维持 pending 且对应实现被 gate。
- 未核/低风险未核（结论不受影响）：附录 A 历史时间窗细节（A.2/A.5/A.6/A.7 过去
  时观测，r1~r3 已按现存状态侧证，本轮不复演）；OD-2b desktop staged 二进制解析
  顺序（owner 未裁决项）；InstalledView 文案现状（展示层）；v1.3 字节级留档不
  存在（docs 未跟踪、无历史）——与 r3/r4 同一限制，本轮以命题级核对替代（见 §3
  限制披露）。

### 2.3 锚点核验表（本轮亲核主干；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位/语义 | 亲核或复用 |
| --- | --- | --- | --- | --- |
| 〔r2-meta 问题 1 落点，v1.4 未动〕§2.2 ensure 行 `:29`/`:30` 补注 | opencode-v2 `packages/client/src/service.ts:27-35` | `:29 /** Service command and arguments. Defaults to \`opencode serve --service\`. */`；`:30 readonly command?: ReadonlyArray<string>`；`:31 /** Environment variables added to the inherited service process environment. */` | **定位准确 / 语义成立**（`:31` 确为 env 注释，A.9.1 旁证持续属实） | 本轮亲核（r3/r4 亦核） |
| 〔r2-meta 问题 2 落点，v1.4 未动〕§2.2 机制二行 `:74`/`:75` 补注 | `packages/server/src/location.ts:71-79` | `:74 query.get("location[directory]") \|\|`；`:75 (request.headers["x-opencode-directory"] ? decode(…) : process.cwd())`；`:76-77 return Location.Ref.make({ / directory: AbsolutePath.make(directory),` | **定位准确 / 语义成立**（`:76-77` 确为构造两行，A.9.2 旁证持续属实） | 本轮亲核（r3/r4 亦核） |
| §2.2 列表过滤 `SessionsQuery.directory`（`session.ts:168-174`，cursor `:173`） | `packages/protocol/src/groups/session.ts:168-174` | `:168 export const SessionsQuery = Schema.Struct({`；`:170 directory: AbsolutePath.pipe(Schema.optional),`；`:173 cursor: SessionsCursor.pipe(Schema.optional),` | **定位准确 / 语义成立**（F-1 承重锚） | 本轮亲核（r2/r3/r4 亦核） |
| §3.6/E-5 form 组 session 作用域路由 `:796/:827/:842/:858` | 同文件 | `:796 …get("session.form.list", "/api/session/:sessionID/form"`；`:842 …post("session.form.reply", "…/form/:formID/reply"`；`:858 …delete("session.form.cancel", "…/form/:formID"` | **四路由定位准确 / 语义成立**（F-6 承重锚） | 本轮亲核（r2/r3 亦核） |
| 附录 B permission/form 事件锚点（`packages/schema/src/permission.ts:45-47`、`packages/schema/src/form.ts:173-175`，v1.3/F-14 采纳态） | 两 schema 文件 | permission `:45 const Asked = ephemeral({ type: "permission.asked", … })`、`:47 type: "permission.replied"`；form `:173-175 form.created/form.replied/form.cancelled` 三行逐字命中 | **定位准确 / 语义成立**（F-14 采纳落点维持正确） | 本轮亲核（r4 亦核） |
| 〔A.10/A.11 行数旁证〕groups/permission.ts、groups/form.ts、schema/permission.ts、schema/form.ts | 四文件 `wc -l` | 135 / 26 / 67 / 177 | **逐项一致**（groups/form.ts 仅 26 行，`:173-175` 排除推理成立） | 本轮亲核（r4 亦核） |
| §2.3 probe `/api/health`（`probe.go:174`）；shape arbiter（`:196-216`） | 本仓 `agent/opencode-web/probe.go:174/:196-202` | `:174 v2Health, v2Detail := probeHealth(ctx, c, "/api/health")`；`:197-201 shapePath := "/session"… if candidate == generationV2 { shapePath = "/api/session"; expected = shapeDataEnvelope }` | **定位准确 / 语义成立**（必改判断与「arbiter 只看 data 键」描述均成立） | 本轮亲核（r1~r4 亦核） |
| §2.3/§3.3 quarantine 三处共用（`opencodeweb.go:328-336`） | 本仓同文件 | `:329-330 // unsupportedGenerationDetail is the single shared quarantine wording used by InstanceStatus, clientFor, and RunDiagnostics…` | **定位准确 / 语义成立** | 本轮亲核（r1~r4 亦核） |
| §2.3 directive-003（`sessions.go:137-143`）；unwrapDataEnvelope（`:260-262`） | 本仓同文件 | `:139-140 // Any bucket failure fails the whole global list — never a partial catalog (directive-003 OD-2).`；`:260-261 // unwrapDataEnvelope descends a v2 {"data": …} envelope when present…` | **定位准确 / 语义成立** | 本轮亲核（r1~r4 亦核） |
| §2.2 现行 client 目录参数行为（`client.go:111-117`）及旧注释措辞 | 本仓同文件 | `:111 if c.gen == generationV2 {`；`:114-116 // …the x-opencode-directory header is a 1.18 convention that stays off the v2 wire.`；`:117 appendQuery = …HasSuffix(url, "/api/session")` | **定位准确 / 语义成立**——现行注释仍是旧措辞，方案 §2.2 已把「注释措辞更正」登记为实施期必改项（未实施，符合零代码改动授权） | 本轮亲核（r2~r4 亦核） |
| §2.3 events.go C1（`:142-145`）；env 通道与 source flag（`main.go:104-110`）；session_discovery（`:412/:423`）；permissions.go:104 | 本仓各文件 | `:142 // C1: the verified 1.18.18 event stream is /global/event only…`；`:104-106 OPENCODE_WEB_BASE_URL/_SERVER_USERNAME/_SERVER_PASSWORD`；`:110 …(managed_local, external_http, legacy_64667, service_discovery_future, disabled)`；`:412 prev, hadPrev := seen[id]`/`:423 if current != prev {`；`:104 if c.Generation() == generationV2 {` | **全部定位准确 / 语义成立** | 本轮亲核（r1~r4 亦核） |
| §3.2 触发链承重锚：监控循环 `:709/:718-719/:722`；triggerAutoRestart `:757/:759/:765/:768`；stuck 清零 `:1070-1072`；fileReadHealth 先例 `:1015-1017`；resolve guard `:1157-1161`；syncDesktopConfig `:1198`；startOpenCodeManagedServer `:1264`；resolve 调用 `:536/:581` | 本仓 `RuntimeManager.swift` 逐行 | `:718 await self.pollManagementAPI()`；`:719 self.evaluateAutoRestart()`；`:722 try? await Task.sleep(nanoseconds: 3_000_000_000)`；`:757 private func triggerAutoRestart(reason: String)`；`:759 if stuckRestartCount >= maxStuckRestarts`；`:1070-1072 …if s == .ready \|\| s == .readyNoAgents { stuckRestartCount = 0`；`:1158 guard config.drivers.contains("opencode-web"), config.opencodeSource == .managedLocal else {`；`:1198 _ = openCodeManagedServer?.syncDesktopConfig(` | **全部定位准确 / 语义成立**（F-2 承重锚；source 置 attach 后 guard 自动停 managed、`:1198` 仅 managedLocal 分支可达） | 本轮亲核（r2~r4 亦核） |
| §3.2 信号面：结构化就绪 live 重读（`management_api.go:692-733`；`agent_descriptor.go:243-249`） | 本仓两文件 | management_api `:711-714 // 2026-10-06 方案 §3：opencode-web 的描述符状态面已切结构化就绪…`；descriptor `:243-248 case "opencode-web": … return detectStructuredInstanceReadiness("opencode-web", agent)` | **定位准确 / 语义成立**（descriptor 增补 source 的 seam 既有、零新增成立） | 本轮亲核（r2/r3 亦核） |
| §2.3 resolver 槽位（`OpenCodeEndpointResolver.swift:25-27`） | 本仓同文件 | `:25 case serviceDiscoveryFuture = "service_discovery_future"` | **定位准确 / 语义成立** | 本轮亲核（r1~r4 亦核） |
| §2.3 think.md `:334/:852/:1799/:2714-2716/:2779`；GO_BRIDGE_ARCHITECTURE.md:87 | 本仓两文档逐行 | 五处语义命中（`:334` 目录查询挂死复盘标题；`:852` 锚点表行含「opencode-web 待补」；`:2714-2716` 历史文档降级；`:1799` 首回合闪烁；`:2779` 排障教训）；架构文档 `:87 | opencode-web | … **待补**` | **全部成立**（活文档未漂移，本轮不复用历轮核验） | 本轮亲核（r1/r3 亦核；活文档每轮亲核） |
| 〔F-15 采纳落点〕§2.1 本仓行「未跟踪 6 个 docs 文件…计数截至 2026-10-19 03:52」 | 本仓 `git status --porcelain` + docs 文件 mtime | porcelain 恰列 6 个未跟踪 docs 文件（方案 + r1/r2/r2-meta/r3/r4），追踪文件干净；r4 报告 mtime 03:51:05（03:52 时点已存在），其后至本轮检查无新增未跟踪文件 | **计数与枚举逐项一致 / 时点声明自洽** | 本轮亲核 |

复用（命题/锚点粒度，两侧文本未变且源码身份未变——上游同 commit `843a6c51`、
本仓同 HEAD `1fe9c65d`，本轮抽样未见行号漂移）：r1~r4 已核的 server.ts /api/info
与 ServerInfo、event.ts SSE 端点与 volatile 契约、message.ts:68、CRUD/prompt/
interrupt/switch 路由组、groups permission.ts:23-124/reply :117、project.ts:6,17、
service-probe/service/service-registration/service-config/server-connection 全组、
v2 无 todo 路由组、OpenCodeManagedServer 三段、desktop_home_projects、
events_dedupe/sse_watchdog 文件存在性。以上均与 v1.4 文本引用一致，本轮未逐一
重摘（低风险命题抽样已覆盖同类）。

## 3. v1.3→v1.4 改动面与 F-15 闭合判定（本轮定向复核）

| 检查项 | 判定 | 证据 |
| --- | --- | --- |
| §2.1 本仓行改写（F-15 修订方向：时点限定式表述） | **完成且准确** | 本轮 porcelain 亲核恰 6 个未跟踪 docs 文件，与改写后单元格的枚举（本方案 + r1/r2/r2-meta/r3/r4）逐项一致；「计数截至 03:52」有 mtime 旁证（r4 03:51:05 已落盘、其后无新增）；「后续不逐轮回改」的声明使 r5 报告落盘（第 7 个）不产生新的滞后——F-15 的 r4 闭合标准（计数与快照时点一致即可）满足 |
| A.11 更正记录契约合规 | **合规** | 四要素（原错误→来源→旧/新身份→影响范围与处置）齐备；版本 v1.3→v1.4 区分快照；r4 报告不回改以本记录留痕；「不改动任何 R/OD/S/E/Gate/验收标准」声明与本轮全文重读结果一致 |
| 头部改动范围声明（仅头部、§2.1 一处单元格、A.11；设计命题逐字未动） | **命题级核实一致** | 全文重读 + 与 r4 所引 v1.3 内容逐命题比对：§2.2 双机制行、§2.3 全表、§3.1~§3.10、§4 E 表与 Gate 映射、§5 切片表、§6、附录 A.1~A.10、附录 B 均与 r2/r3/r4 所批内容一致；未检出任何设计命题变化 |
| 头部对 r4 判定的转述 | **准确** | 「r4 复审对 v1.3 给出 APPROVED（r3 通过结论延续有效，blockers=0）；r4 唯一新建议 F-15（建议级）」与 r4 报告逐项一致；「提前收编（修订量一句话级），非对 r4 判定的推翻」定性正确 |

**核验限制披露**（与 r3/r4 同源）：v1.3 无字节留档（docs 未跟踪、无历史），
「v1.4 与 r4 所批内容逐字一致」无法按字节独立复核；本轮以命题级核对替代——r4
闭合表与锚点表所引全部承重内容在 v1.4 中均在且一致。该限制不削弱本轮结论
（改动面声明与实际检出差异为零）。

## 4. 历轮意见处置汇总（最终闭合账）

- r1 F-1/F-2/F-3（阻塞）/F-4~F-9（建议）：r2 判定闭合；r3/r4 承重锚重核维持；
  本轮对 F-1（SessionsQuery/location 双机制）、F-2（触发链与信号面全锚）、
  F-3（三处门控）、F-5（unwrapDataEnvelope/directive-003）、F-6（form 四路由）、
  F-8（session_discovery 包路径）承重锚抽样重核，**维持闭合**。
- r2-meta 问题 1~4：v1.2 闭合（r3 核）；r4 与本轮对两处落点（service.ts :29-31、
  location.ts :74-77）复核命中，**维持闭合**。
- r3 F-14（建议）：r4 判定闭合；本轮对两处采纳锚点（schema/permission.ts
  :45-47、schema/form.ts :173-175）复核命中，**维持闭合**。
- r4 F-15（建议）：**本轮判定闭合**（§3 表）——采纳准确、无回归。
- r2 F-10~F-13（建议，实施期吸收）：v1.4 头部与 A.11 未触碰，维持 r2/r3/r4
  原判（实施期承接：F-10 抖动用例入 S-1 单测/E-6 断言；F-11 S-6 活体排序口径；
  F-12 resolver 镜像 XDG_STATE_HOME；F-13 attach-first 限 managedLocal 档）。
  **非未闭合缺陷**。

## 5. 意见（本轮新增）

无。本轮定向复核与抽样重核未发现新问题：v1.4 改动面与声明一致、全部承重锚
命中、来源/配套/活体身份复测一致、门控三处一致无绕行。按复审纪律（同一快照
经四轮评审、剩余建议已全部收编时，不为继续循环制造意见；没有发现问题时不得
编造意见凑数量），本轮零新意见。

## 6. 复审处置与剩余门

- 原意见闭合：r1 F-1~F-9 闭合（r2 + r3/r4 + 本轮抽样重核）；r2-meta 问题 1~4
  闭合（r3 + r4 + 本轮落点复核）；r3 F-14 闭合（r4 + 本轮复核）；r4 F-15 闭合
  （本轮）；r2 F-10~F-13 维持实施期吸收。
- 新意见：无。本轮无阻塞意见。
- 仍待证据/决定/授权（与阻塞意见分列；通过不改变其状态）：
  - 证据门（实施期捕获，方案登记完备）：E-2（SSE 帧，gate S-3 与 S-2b）、
    E-3（prompt+目录三态矩阵，gate S-5）、E-4（parts schema+分页对账，gate S-4）、
    E-5（permission/form wire，gate S-5）、E-6（轮换/双向切换端到端，gate 验收
    标准 4）、E-7（TCC 可达矩阵 desktop 起 vs 手动起，gate S-6 分支说明）、
    E-8（503 重试边界，gate S-2a 重试验收）。
  - 产品决定门：OD-2b（ensure-start，owner 未裁决，仅 gate 可选切片）、OD-5
    （fail-all vs 跳过+告警，owner 未裁决，gate S-6 分支选择）；OD-1/3/4/6 推荐
    默认建议 owner 启动实施时顺带确认。
  - 实施期吸收项：F-10（抖动用例）、F-11（S-6 活体排序口径）、F-12（XDG 路径
    parity）、F-13（attach-first 适用档位）、附录 B 随 E-2 样本逐条校正、
    client.go 旧注释措辞更正（§2.2 登记的实施期必改项）。
  - 授权边界：本通过不构成实施授权；S-1~S-8 由 owner 决定是否启动 exec-plan
    （方案 §6 已声明）。E-6/E-7 复现涉及本机 live 服务替换，实施期按方案登记
    的取证方式与清理纪律执行。
- 外部格式专项（audit-plan）结论：**通过（就已声明形状而言）**——v1.4 形状面
  相对 v1.3 零变化；E-1 身份链本轮活体五度确认（401 负例 + pid 三方一致）；
  E-2/E-3/E-4/E-5 为显式 pending 且对应实现被 gate，符合待捕项裁定。

## 7. 结论（评审循环终局）

v1.4 是对 r4 建议级意见 F-15 的一句话级收编：§2.1 时点限定式改写经本轮 porcelain
与 mtime 双重亲核逐项一致，A.11 更正记录符合契约证据更正纪律，头部声明的改动面
与实际检出差异为零，全部设计命题、门控与验收标准相对 r2/r3/r4 所批内容未变。
来源身份（四仓 + 唯一配套判定）与活体身份（49374/29415、4096/28164、401 负例、
pid 三方链）本轮独立复测全部一致；上游与本仓承重锚（含 r2-meta 漏检类子行号）
抽样重核全部命中。历轮全部阻塞与建议意见已闭合或按原判归入实施期吸收，本轮
零新意见。r4 的 APPROVED 对 v1.4 延续有效，评审循环以通过结论闭合（第 5 轮＝
轮次上限轮）。

**verdict: APPROVED**——设计层面通过；实施就绪度以 §1 所列门为准，逐片开工前
照常校验对应 E/OD/授权；实施启动由 owner 决定（本任务不含实施授权）。
