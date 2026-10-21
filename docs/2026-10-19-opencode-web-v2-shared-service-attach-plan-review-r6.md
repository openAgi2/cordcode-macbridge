# 评审报告 r6：OpenCode v2 独立接入与共享服务设计方案（v1.7 完整复审）

- 评审轮次：第 6 轮（**v1.7 完整方案复审**；非定向复核）
- 评审日期：2026-10-19
- verdict：**APPROVED（通过）**
- blockers：0；advisories：本轮新增 **2**（F-16、F-17，均为引用精度级）；随行维持
  r2 判定 4 条（F-10~F-13，实施期吸收）+ 附录 B 随 E-2 样本逐条校正（实施期）
- 契约版本：plan-contract-v1.1（plan-review skill，含 audit-plan 专项接入）
- 评审员模式披露：**新会话、新评审员**——完整读取方案 v1.7 全文与
  r1/r2/r2-meta/r3/r4/r5 六份历史报告（r5、r2-meta 全文，r1/r2 意见与锚点段），
  按「新评审员独立阅读后核对历史」模式执行。独立性缓解措施：本轮对 **v1.5/v1.6/v1.7
  三轮 owner 驱动修订的全部新增锚点（§3.0/§3.11/E-11/手动启动链）逐项亲核**——该部分
  在 r1~r5 中从未被评审（r5 通过链闭合于 v1.4；A.13 定向复审仅覆盖 v1.5 新增段且未公开
  锚点核验表；A.15 定向复核仅确认 E-11 依赖循环修复）；另对 r5 复用的承重锚抽样重核、
  来源/配套/运行身份链独立复测。

## 1. 结论与实施就绪

- verdict: **APPROVED**
- plan: `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.7
  （公共设施依赖与可独立退役约束补强版），SHA-256
  `37ddbaf09e1d4510fd9535232f845e844808f545168bfbd2bc2bf11ad0c7bd47`（本轮亲算）。
  本轮范围：**完整方案复审**——v1.4→v1.5→v1.6→v1.7 累积改动面（§3.0 独立实现与
  公共依赖边界、§3.11 身份重用接线与数据兼容门、E-11a/b/c 拆分、§3.2 手动启动、
  §3.7 桶隔离聚合）+ 全局门控/OD/E/S/验收回归重查 + 新增锚点全量亲核 + 承重锚抽样
  重核 + 来源/配套身份复测。
- 总体判断：
  1. **v1.5/v1.6/v1.7 的实质修改方向均有 owner 明确裁决背书**（A.12 手动启动+不
     fail-all、A.14 独立包/ID=`opencode`、A.16 公共依赖边界），方案头部与 OD-2/5/7
     如实记录裁决来源与原话，未把推荐伪装成 owner 决定（r1-F-9 的纪律维持）。
  2. **§3.11 身份接线的全部 23 处双仓锚点本轮亲核**：Go 13 处、Mac Swift 5 处、
     iOS 配套仓 5 处，其中 22 处定位+语义精确命中，1 处 +2 行号漂移（F-16，
     语义成立）。旧 ID 残留（deprecated init 注册、main.go 旧 proxy、五处 ID 专用
     分流、iOS `.openCode` 解码、checkpoint/pin 存储键缺 generation 维度）**全部
     经源码证实真实存在**，§3.11 的适配清单没有虚列或漏列关键分流点。
  3. **§3.0 装配方向在现有架构下可行**：core 已有 `EventSubscriber`/
     `LiveEventSubscriber`（`core/interfaces.go:1051/1060`）产出 `chan core.Event`、
     bridge 装配层消费的既有模式（`go-bridge/main.go:320-322,1017`）；现有
     `agent/opencode-web` 本就不 import go-bridge。「新 driver 经 core 接口接入、
     Kernel/EventPublisher 由 bridge 装配层同一实例持有」不是新发明的关系，与
     既有 backend 装配先例一致。
  4. **v1.7 依赖闭包约束自洽且可验证**：中立公共包方向（两代分别单向依赖、公共
     层不得反向依赖任一 backend）、bridge 过渡装配双 import 不计入新包闭包的豁免
     边界（§3.0 规则 6 与 E-11b 表述一致）、Go 工具链下 `go list -deps` 可执行
     传递依赖检查；「backend 语义（事件顺序/去重 identity/重连恢复/完成条件）不得
     按通用 helper 抽走」有效堵住了借公共层绕过样本门的路径。
  5. **门控一致性**：E-11a/b/c 拆分后依赖 DAG 无环（S-0→E-11a→S-7→E-11b→S-2b→
     S-8→E-11c→S-9，A.15 修复的循环未复发）；S-2b 放行前置在 §3.3/§4 Gate A/
     §5 切片表/§6 阻塞清单四处逐字一致；退役可分离标准在 §3.0/§3.12 第 5 条/
     S-9 验证列三处一致。唯一不精确处为 S-1 依赖列的引用对象（F-17，建议级）。
- 结论定性：r5 通过链（r2→r3→r4→r5）闭合于 v1.4；此后 v1.5~v1.7 为三轮 owner
  驱动的实质修改，头部已如实声明「旧版通过与定向闭合结论不代替本版完整评审」。
  本轮对 v1.7 完整复审：全部设计命题、门控与验收标准一致自洽，新增锚点亲核命中，
  未发现导致实现走错或主路径不可达的实质矛盾。通过结论覆盖 v1.7 全文（含 v1.5/
  v1.6/v1.7 累积改动），不再需要针对这三轮改动的单独复审。
- implementation_readiness（通过 ≠ 授权；方案 §6 已声明实施另需 owner 启动）：
  - 可开工（受既定门约束）：S-0（契约冻结，含 §3.0 helper 归属清单与依赖图基线）、
    S-1（resolver/手动启动/状态轴；E-1 verified、E-6/E-9 为完成门、OD-2/3 已裁决；
    F-10/12/13 实施期吸收）、S-2a（probe 骨架；E-8 边界已知）、S-7 组件部分
    （依赖 S-0 产出的 E-11a 契约）。
  - 仍受证据门阻塞：S-3（E-2）、S-4 映射固化（E-4）、S-5（E-3/E-5）、S-6 完成门
    （E-7/E-10）、**S-2b 放行（E-2/E-3/E-4/E-5 齐备＋S-3~S-6 就位＋E-11b 组件
    身份门通过）**、验收标准 4/5 的切换与轮换验收（E-6）、S-8（放行后）、S-9
    （§3.12 稳定门＋owner 明确退役指令）。
  - 产品决定门：OD-1/3/4/6/8 为工程决定/延续（方案已标注来源），owner 启动实施时
    顺带确认即可；OD-2/5/7 已 decided。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核；四仓 + 运行身份）

| 仓库/工作树 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 核对结果 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 追踪文件干净；未跟踪恰 **7** 个 docs 文件（方案 + r1/r2/r2-meta/r3/r4/r5 报告；本轮 porcelain 亲核，r6 报告将成第 8 个） | 与方案 §2.1/附录 C 一致 |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，`git describe` = `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净（porcelain 空） | 与方案 §2.1 一致 |
| opencode（旧 checkout，仅备注） | `/Users/jacklee/Projects/opencode` | `dev` | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | 干净（porcelain 空） | 与方案 §2.1 一致（不作为锚点） |
| cordcode-ios（配套工作树） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52` | 干净（porcelain 空） | 与方案 §2.1 v1.2 新增行一致；配套判定沿用 r5 亲核（两侧 worktree 唯一同族，本轮 HEAD 未变） |

- 方案 v1.7 SHA-256 本轮亲算 `37ddbaf0…`（见 §1）。A.16 所记 v1.6 修订前哈希
  `dfb1ada2…` 与 A.14/A.15 哈希链构成版本身份链，本轮不重演。
- 运行身份：本轮为纯文档评审，未复测 49374/4096 活体（r5 五度确认 + 方案 A.8.4
  双重复测在案；E-1 为 verified 状态，其证据效力不依赖本轮复测）。

### 2.2 本轮检查范围

- **完整重读方案 v1.7 全文**（1232 行），与 r5 所批 v1.4 的命题面比对，确认
  v1.5/v1.6/v1.7 三轮改动面与头部声明一致（§1/R-4/R-5/§3.2/§3.7/OD-2/3/5/E-6/
  7/9/10/S-1/S-6 ← v1.5；§3.0/§3.11/OD-7/R-7/R-8/E-11 拆分/S-0/S-2 拆分/S-7 ←
  v1.6；§3.0 依赖闭包补强/E-11b/S-9 ← v1.7）。
- **亲核 v1.6/v1.7 新增锚点全量**（历轮从未评审）：§3.11 表 Go 13 处 + Mac
  Swift 5 处 + iOS 配套仓 5 处；§3.0 架构可行性锚（core 事件接口、装配层消费
  先例、现有 driver import 边界、Kernel 归属）。
- **亲核上游 v1.5 新增手动启动链锚点**（A.13 定向复审未公开锚点表，本轮独立核）：
  serve 入口、ServerProcess、Service.ensure 的 terminate/replace 副作用、contender
  spawn 形状、0xC0DE 端口、官方并发/生命周期/安装测试、desktop bundled/staged、
  v2 安装文档与 v1 对照锚。
- **承重锚抽样重核**（r5 复用项防漂移）：SessionsQuery、location 双机制、prompt
  路由、think.md 五处。
- **门控一致性全局重查**：R→S→验收映射、OD×E×S 关系、S-2b 前置四处一致、
  E-11a/b/c 依赖方向、退役可分离标准三处一致、依赖 DAG 无环。
- **经验教训对照**（按评审任务要求）：think.md 五条相关复盘、四份历史接入设计
  文档的教训吸收情况（见 §4）。
- audit-plan 专项：**已运行**（延续）。v1.7 形状面相对 v1.4 无新增外部 wire 命题
  （v1.5~v1.7 改动为 Swift 生命周期/身份接线/依赖边界，均非外部形状声明）；既有
  形状断言（envelope 三分型、project 裸数组、cursor 分页、E-1 身份链）维持 r1~r5
  活体核验结论；E-2/E-3/E-4/E-5 维持 pending 且对应实现被 gate。
- 未核/低风险未核（结论不受影响）：附录 A 历史时间窗观测（过去时证据，r1~r3 按
  现存状态侧证）；`service-lifecycle.test.ts:59` 单行（文件与 :19 命中，:59 未逐行
  摘录）；`utils.ts:109`/`configure.ts:33`/`electron-builder.config.test.ts:73`
  三处安装旁证锚（同链 desktop-cli.ts:77/139 与 updater-install.test.ts:140/206
  已命中，同批低风险）；v1.4 字节级留档不存在（docs 未跟踪）——与 r3~r5 同一限制，
  以命题级核对替代。

### 2.3 锚点核验表（本轮亲核主干；原引用保持原样）

**A. §3.11 身份接线锚点（v1.6 新增，历轮未核，本轮全量）：**

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号，节选） | 定位/语义 | 亲核或复用 |
| --- | --- | --- | --- | --- |
| `deprecated/opencode/opencode.go:23` 归档 init 注册 | 同文件 `:23-25` | `:23 func init() {`；`:24 core.RegisterAgent("opencode", New)` | 定位准确（init 块）/ 语义成立：归档实现仍占 `opencode` 注册 | 本轮亲核 |
| `core/registry.go:12` 同名会覆盖 | 同文件 `:12-14` | `:12 func RegisterAgent(name string, factory AgentFactory) {`；`:13 agentFactories[name] = factory` | 定位准确 / 语义成立：map 赋值无条件覆盖 | 本轮亲核 |
| `go-bridge/main.go:326-329` 旧 proxy 注册 | 同文件 | `:327 if id == "opencode" && *ocBaseURL != "" {`；`:329 handlers.RegisterOpenCodeProxy(proxy)` | 定位准确 / 语义成立：新 ID 复用必须拆除此分流 | 本轮亲核 |
| `handlers.go:1264` 旧 dispatcher | 同文件 | `:1264 if msg.BackendID == "opencode" && h.isOC() {`；`:1265 h.handleOpenCodeRPC(conn, msg)` | 定位准确 / 语义成立：新 ID RPC 会误入旧 dispatcher | 本轮亲核 |
| `handlers.go:1272` catalog feature 门 | 同文件 | `:1272 func catalogCapabilityRequiredFor(agentName string) bool {`；`:1274 case "codex", "grokbuild", "opencode", …` | 定位准确 / 语义成立 | 本轮亲核 |
| `handlers.go:2697` hydrate/create 白名单 | 同文件 | `:2697 if agent.Name() == "codex" \|\| agent.Name() == "claudecode" \|\| agent.Name() == "opencode-web" {` | 定位准确 / 语义成立：白名单现含 opencode-web、需为新 ID 定分流 | 本轮亲核 |
| `handlers_opencode.go:422,437` 旧 catalog/overlay | 同文件 | `:422 func (h *Handlers) buildOpenCodeEnrichedSessions(…)`；`:437 func (h *Handlers) ocHandleListSessions(…)` | 定位准确 / 语义成立 | 本轮亲核 |
| `agent_descriptor.go:116,216` 旧 opencode 轮询/health | 同文件 | `:116 return id == "claude" \|\| id == "opencode" \|\| id == "grokbuild"`；**opencode health 分支实际在 `:218 case "opencode":`**（`:216` 为 `case "claude":`，函数 `detectAgentStatus` 自 `:214` 起） | **:116 定位准确；:216 定位偏差 +2（→F-16）/ 语义成立** | 本轮亲核 |
| `handlers_projection.go:664,1386` hydrate 白名单与 rich history 分流 | 同文件 | `:664 if backendID == "opencode-web" \|\| backendID == "codex-remote" {`；`:1386 case "opencode":`（`:1387 streamOpenCodeRichHistoryProjectionEvents`） | 定位准确 / 语义成立：`:1387` helper 按 name 解析旧 agent，新 ID 命中即错流 | 本轮亲核 |
| `projection_kernel.go:188-200` checkpoint 文件名只含 backend/session | 同文件 | `:189 sum := sha256.Sum256([]byte(backendID + "\x00" + sessionID))` | 定位准确 / 语义成立：无 generation 维度，新 ID 复用会与旧 checkpoint 碰撞——generation 子 namespace 的必要性成立 | 本轮亲核 |
| `pinstore/store.go:110` key 构造 + 跨 scope API | 同文件 `:110` + `:163/:185/:242` | `:110 return backendID + "\|" + scope + "\|" + sessionID`；`GetAnyScope(:163)`/`List(:185)`/`RemoveAll(:242)` 存在 | 定位准确 / 语义成立：跨 scope 操作真实存在，只加 scope 不够的判断成立 | 本轮亲核 |
| `handlers_session_pin.go:201` 新 ID pin 解析分流 | 同文件 | `:201 case "opencode":`；`:202 return h.resolveOpenCodePin(pin)` | 定位准确 / 语义成立：新 ID 命中旧 pin 解析路径 | 本轮亲核 |
| `hello_handler.go:15` capabilities[] 扩展点 | 同文件 | `:15 Capabilities []string \`json:"capabilities,omitempty"\`` | 定位准确 / 语义成立：可选协商能力的承载字段真实存在 | 本轮亲核 |
| Swift `ProductAgentCatalog.swift:17` / `AgentSelectionStore.swift:12,62` | 两文件 | `:17 Product(id: "opencode-web", …)`（产品白名单）；`:12 static let initial`；`:62 guard ProductAgentCatalog.ids.contains(id)…`（选择校验） | 定位准确 / 语义成立：产品 catalog 与选择白名单接线点真实 | 本轮亲核 |
| Swift `RuntimeManager.swift:1157,1778,1817` | 同文件 | `:1157 func resolveManagedOpenCodeIfNeeded()`（guard drivers.contains("opencode-web")）；`:1778 if !config.opencodeURL.isEmpty {`（旧 URL 注入）；`:1817 if !config.opencodeUser.isEmpty {`（旧凭据 env） | 定位准确 / 语义成立：「不把新 ID 传给旧双送 URL/凭据逻辑」的改造点真实 | 本轮亲核（:1157 r1~r5 亦核） |
| iOS `BackendModels.swift:69-76` opencode 解为旧 `.openCode` | 配套仓同文件 | `:69 static func fromWireKind(…)`；`:75 case "opencode": return .openCode` | 定位准确 / 语义成立：wire kind 旧映射真实存在 | 本轮亲核 |
| iOS `BackendModels.swift:309` cacheScopeKey | 同文件 | `:309 var cacheScopeKey: String {`；`:310 "\(backendKind.rawValue)\|\(endpoint)\|\(username)"` | 定位准确 / 语义成立：key 无 driver generation 维度 | 本轮亲核 |
| iOS `BridgeProvider.swift:985` 按 kind 解码 | 同文件 | `:985 guard let kind = BackendKind.fromWireKind(backend.kind) else {` | 定位准确 / 语义成立：hello ack backend 按旧 kind 映射 | 本轮亲核 |
| iOS `ServerViewModel.swift:885` 只比较 bridge+kind | 同文件 | `:885 private func bridgeTargetIndex(…)`；`:890 BridgeIdentity(server:) == bridgeIdentity && server.backendKind == backendKind` | 定位准确 / 语义成立：target 匹配无 generation | 本轮亲核 |
| iOS `CCCodeBridgeBackendClient.swift:155` sourceIdentity | 同文件 | `:155 var projectionSourceIdentity: String {`；`:156 "\(transport.connectionID):\(projectionConnectionGeneration):\(backendID)"` | 定位准确 / 语义成立：仅 transport 代际+ID，无 driver generation | 本轮亲核 |

**B. §3.0 架构可行性锚（v1.6/v1.7 核心，本轮亲核）：**

| 命题 | 实际来源及位置 | 摘录 | 判定 |
| --- | --- | --- | --- |
| core 已有 agent→bridge 事件通道接口 | `core/interfaces.go:1051-1052,1060-1061` | `:1052 Subscribe(ctx) (<-chan Event, error)`；LiveEventSubscriber 同形 | 「新 driver 经 core 接口产出事实」有既有模式，可行 |
| bridge 装配层消费事件先例 | `go-bridge/main.go:320-322,1017` | `:322 startPassiveSubscription(ctx, handlers, id, sub.Subscribe, …)` | Kernel/EventPublisher 由装配层持有并消费 agent 事件的模式既有 |
| 现有 driver 不 import go-bridge | `agent/opencode-web/` 全目录 grep | 无 `…/go-bridge` import 命中 | 「新 driver 不 import go-bridge」与既有架构约定一致，非新约束负担 |
| driver 注册先例 | `agent/opencode-web/opencodeweb.go:36-37` | `:37 core.RegisterAgent(BackendID, New)` | 新包经 main 显式 import/alias 独占注册的路径既有 |

**C. 上游 v1.5 新增手动启动链锚点（本轮亲核，A.13 未公开核验表的独立补核）：**

| 方案引用 | 实际位置 | 摘录（节选） | 判定 |
| --- | --- | --- | --- |
| `serve.ts:6` → server-process | `packages/cli/src/commands/handlers/serve.ts:5-9` | `:9 return yield* ServerProcess.run({`（`:8` 拒绝 `--service`+`--stdio` 组合） | 成立 |
| `server-process.ts:64,159` | 同文件 | `:64 const foreground = options.mode === "default"`（service 配置读取块）；`:159 const server = yield* launch.pipe(`（incumbent 竞争） | 成立 |
| `effect/service.ts:34` incumbent 探测 | 同文件 `:33-36` | `:34 export const incumbent = Effect.fn("service.incumbent")(…)` | 成立 |
| `effect/service.ts:75-87` 3 次超时 terminate | 同文件 | `:79 if (timeouts.count >= 3) {`；`:83 yield* terminate(info, options, timing)` | 成立——「ensure 会 terminate」的关键依据亲核命中 |
| `effect/service.ts:98-104` 版本替换 | 同文件 | `:99 if (decision._tag === "replace") {`；`:102 yield* stop({ file: options.file, pty: decision.pty })` | 成立 |
| `service-contender.ts:19` detached spawn | 同文件 `:19-22` | `:20 detached: true`；`:22 stdio: ["ignore", "ignore", "pipe"]`（`:12` stderrLimit = 8KiB） | 成立 |
| `service-config.ts:56` 0xC0DE | 同文件 | `:56 if (channel === "latest" \|\| …) return 0xc0de` | 成立 |
| `service/start.ts:11` 调 Service.ensure | 同文件 `:11` | `const transport = yield* Service.ensure(…)` | 成立——不采用 `service start` 的论证依据成立 |
| `service.test.ts:381,637` 并发测试 | 同文件 | `:381 test("concurrent service processes elect one server"…)`；`:637 test("port contender recognizes an incumbent registered during the bind race"…)` | 成立 |
| `service-lifecycle.test.ts:19,59` | 同文件 `:19-20` | `:19 serviceTest(` / `:20 "concurrent CLI and client starts converge on one server"` | 成立（:19 亲核；:59 同文件未逐行） |
| `desktop-cli.ts:77,139` bundled/staged | 同文件 | `:77 const resolveBundledCli = …`；`:139 const installCli = …` | 成立 |
| `updater-install.test.ts:140,206` | 同文件 | `:141 npm install --global --force @opencode/cli@2.3.4-beta.1`（固定版本 npm 安装先例）；`:206 curl V2 installer 测试` | 成立——`@opencode/cli@<版本>` 形态有官方先例 |
| `services/www/src/docs/content/index.mdx:14-18` v2 安装方式 | 同文件 | `:14-18` curl/brew/npm/bun/pnpm/yarn（npm = `npm install -g @opencode/cli`） | 成立（注意路径前缀为 `services/`，方案引用无误） |
| v1 对照 `packages/web/src/content/docs/index.mdx:37` | 同文件 | `:37 The easiest way to install OpenCode 1 is through the install script` | 成立——v1/v2 安装文档分离的论据成立 |

**D. 承重锚抽样重核（r5 复用项防漂移）+ think.md：**

| 引用 | 判定 | 依据 |
| --- | --- | --- |
| `session.ts:168-174` SessionsQuery（F-1 承重） | 命中 | `:170 directory:`/`:173 cursor:` 逐字命中 |
| `location.ts:71-79` requestRef（F-1 承重） | 命中 | `:74 query.get("location[directory]")`/`:75 header` 与 A.9.2 更正后引用一致 |
| `session.ts:396-407` prompt（payload 可选 `id`） | 命中 | `:399-401` payload 含 `id: SessionMessage.ID.pipe(Schema.optional)` |
| think.md `:334/:852/:1799/:2714-2716/:2779` | 全部命中 | 五处行号语义逐项吻合（目录挂死/锚点表待补/首回合闪烁/旧文档降级/排障教训） |

其余 r5 已核上游/本仓锚点（server.ts /api/info、event.ts、message.ts:68、CRUD 组、
permission.ts:23-124、project.ts:6,17、service-probe/registration 全组、probe.go、
events.go、sessions.go、management_api.go、OpenCodeEndpointResolver 等）：上游同
commit `843a6c51`、本仓同 HEAD `1fe9c65d`、v1.7 文本引用与 r5 所核一致，按命题/
锚点粒度复用（本轮抽样未见漂移）。

## 3. v1.4 → v1.7 改动面复核（本轮完整复审对象）

| 检查项 | 判定 | 证据 |
| --- | --- | --- |
| v1.5（A.12）：OD-2 手动启动/OD-5 桶隔离改判 | **一致且自洽** | §3.2 状态矩阵/四拍与 OD-2 owner 原话对应；§3.7 独立桶结算/`N=A+K` 对账/全失败不冒充空列表与 R-5、验收标准 6、U6/U7 对应；A.13 定向复审通过在案，本轮全量重读未发现其后的 v1.6/v1.7 修订破坏该部分（§3.2.1 仅增 v2 独立 source/seat 命名，与 §3.11 一致） |
| v1.6（A.14）：独立包/ID=`opencode` + §3.0/§3.11/E-11 拆分 | **接线完整、锚点真实** | §3.11 表 6 行边界锚点 23 处本轮亲核（§2.3 表 A）；deprecated 注册、main.go 旧 proxy、五处 ID 分流、iOS `.openCode`/cacheScopeKey/sourceIdentity、checkpoint/pin 键缺 generation——适配清单与源码实况一一对应，无虚列；「旧 ID 已不用 ≠ 消费者已无此字符串」的判断经亲核成立 |
| v1.6（A.15）：E-11 拆 a/b/c | **无环且门控正确** | S-0→E-11a（契约）→S-7→E-11b（组件）→S-2b 放行→S-8→E-11c（运行态）→S-9；E-11c 明确不作为 S-2b/S-7 前置——原依赖循环未复发 |
| v1.7（A.16）：公共依赖边界 | **自洽、可验证、与退役门扣合** | §3.0 规则 3/6 + 装配方向图 + 退役可分离标准；E-11b 闭包/fixture 检查、S-0 依赖图基线、S-9 零源码改动验证三处一致；「backend 语义不得按 helper 抽走」堵住绕样本门路径；bridge 过渡双 import 豁免边界两处表述一致 |
| 头部声明与实际改动面一致性 | **一致** | 「只补强依赖与退役可分离性，不改变身份/交互/协议设计」——本轮全文重读比对：v1.7 变化集中于 §3.0（规则 3/6、装配图、退役标准）、§4 E-11b、§5 S-0/S-7/S-9 行、§3.12 第 5 条；未见 R/OD（除表述）/验收标准被改动 |
| 「一库两服」断裂背景（附录 A）与动机链 | **成立** | A.4/A.6/A.7 过去时观测 + r1 活体更正（A.8）；背景动机不依赖本轮复测 |

## 4. 经验教训对照（按评审任务要求核验）

| 来源 | 教训 | 方案吸收情况 |
| --- | --- | --- |
| `docs/2026-08-16-dsh-web-backend-design.md` | 官方 web API 客户端模式；外部 turn 靠服务端广播无需轮询 | §2.2/§3.4 SSE 直播对齐（R-1）；attach 而非自建服务 |
| `docs/2026-08-20-opencode-web-source-first-convergence-plan.md` | 版本绑定样本纪律；旧 fail-all（directive-003） | source-first 纪律在 §3.0 规则 1 继承且明确「旧 1.18 字段不作 v2 依据」；fail-all 被 owner 新裁决替代并留痕（§3.7），不冲突 |
| `docs/2026-08-26-codex-remote-backend-implementation-plan.md` | 外部服务 attach/独立链路/懒加载边界 | attach-only、显式启动、不 fallback 的边界设计一致 |
| `docs/2026-08-28-grokbuild-leader-mode-design.md` + /compact 事故产物门 | 共享 daemon 不重写官方单例；行为修复须官方锚点+对账数字 | §2.2 直接复用官方 serve/registration 语义（ensure terminate 亲核在案）；§3.7 `N=A+K` 对账 + §3.12 稳定门第 1 条内容/目录对账数字交付 |
| think.md `:334`（目录查询挂死） | 有界请求/超时 | §3.7 fan-out 并发上限 4、单桶 deadline、前台不无限重试 |
| think.md `:1799`（首回合闪烁） | 首条消息可见性 | 附录 B `session.inbox.*` 映射 + §3.4 恢复链收敛点对齐 |
| think.md `:2714-2716`（旧文档降级） | 禁据旧文档施工 | §3.0 明确旧收敛计划仅约束 v1、不作为 v2 依据 |
| think.md `:852`（Backend 语义锚点表待补） | source-first 入口 | §2.3 已登记实施时补齐 v2 行锚点 |
| CLAUDE.md 数据真实性纪律 | 失败不得伪装为空/估算 | §3.7「失败目录只能写未读取，不能计作 0」「全失败不得冒充空会话」 |

## 5. 意见（本轮新增）

### F-16 [建议] `agent_descriptor.go:216` 行号 +2 漂移（opencode health 分支实际在 `:218`）

- 位置：§3.11 边界表第 4 行「`agent_descriptor.go:116,216` 旧opencode轮询/health」。
- 证据：本轮亲核 `go-bridge/agent_descriptor.go`——`:216` 为 `case "claude":`，
  `case "opencode":` 及其 URL 未配置/health 分支实际在 `:218-222`；函数
  `detectAgentStatus` 自 `:214` 起。`:116`（legacyRequiresPolling 的
  `id == "opencode"`）精确命中。
- 影响：仅引用精度。实施者按 `:216` 找 opencode 分支会先看到 claude case；语义
  命题（旧 opencode health/轮询 fallback 存在、需为 v2 提供结构化 readiness 替代）
  成立，不影响任何门控。
- 修订方向：§3.11 该行补注 `:218` 精确子行号（或改区间 `:216-222`）。
- 闭合标准：下版（或实施期 S-7 开工前）该行引用与实际行号一致即可；无需单独再轮
  评审（与 F-4/F-15 同级处置）。

### F-17 [建议] S-1 依赖列「选择/身份按S-7」应引用 E-11a 冻结契约而非 S-7 实现切片

- 位置：§5 切片表 S-1 行依赖列「S-0；OD-1/2/3；E-1/E-6/E-9；选择/身份按S-7」。
- 证据：§4 E-11a 行明确其获取方式为「S-0冻结；不依赖S-7实现或S-8」、未满足时
  阻塞「S-7及各跨端实现的前置输入」；S-7 自身依赖「S-0与E-11a冻结契约」且与
  S-1~S-6 并行。S-1 中涉及选择/身份语义（§3.2.1 互斥选择、独立 source、
  openCodeV2SeatAction）的真实前置输入是 E-11a（S-0 产出），不是 S-7 的实现完成。
- 影响：按字面，「按S-7」会把 S-1 的选择/身份部分绑到 S-7 实现进度上，弱化切片
  并行度；实际依赖链无环、S-0 必然先行，语义仍可达，不构成门控漏洞。
- 修订方向：S-1 依赖列改为「选择/身份按E-11a（S-0冻结契约）」一句话级修订。
- 闭合标准：下版或实施期 exec-plan 拆分时引用对象一致即可；无需单独再轮评审。

（说明：本轮对 v1.7 新增的依赖闭包约束、中立公共层方向、退役可分离标准、§3.11
身份接线、手动启动链均做了独立风险审查与全量/抽样亲核，未发现阻塞级问题；以上
两条均为引用精度级，不构成再轮修订的必要，可由实施期收编。）

## 6. 复审处置与剩余门

- 历轮意见闭合维持：
  - r1 F-1~F-9：闭合（r2 + r3~r5 维持）；本轮对 F-1 双机制承重锚抽样重核命中。
  - r2-meta 问题 1~4：闭合（v1.2 起维持；本轮 location.ts :74/:75 复核命中）。
  - r3 F-14：闭合（附录 B 两行包路径采纳态维持）。
  - r4 F-15：闭合（时点限定式表述维持；本轮 porcelain 亲核 7 个未跟踪文件与
    附录 C 逐项一致）。
  - r2 F-10~F-13：维持「实施期吸收」原判（v1.7 未触碰；F-10 防风暴 60s 速率界、
    F-12 XDG parity、F-13 attach 档位限定均已写入 §3.2 对应验收/组件测试行，
    实施期按 exec-plan 承接）。
- 新意见：F-16、F-17（建议级，实施期收编）。
- 仍待证据/决定/授权（与阻塞意见分列；通过不改变其状态）：
  - 证据门：E-2（SSE 帧，gate S-3/S-2b）、E-3（prompt+目录三态矩阵，gate S-5）、
    E-4（parts schema+分页对账，gate S-4）、E-5（permission/form wire，gate S-5）、
    E-6（停/起/轮换端到端，gate 验收标准 4/5）、E-7（桶隔离矩阵，gate S-6）、
    E-8（503 重试边界，gate S-2a）、E-9（手动启动/安装/detached，gate S-1 交付）、
    E-10（告警 codec，gate S-6）、E-11a/b/c（按切片产出）。
  - 产品决定门：OD-1/3/4/6/8（工程决定/延续，owner 启动实施时顺带确认）；
    OD-2/5/7 已 decided。
  - 授权边界：本通过不构成实施授权；S-0~S-8 由 owner 决定启动（方案 §6/阻塞清单
    已声明）。E-6/E-7/E-9 的真实停起/安装/受控环境操作须未来明确授权，不扰动
    现网 49374/4096。
- 外部格式专项（audit-plan）结论：**通过（就已声明形状而言）**——v1.7 无新增
  外部形状断言；E-1 verified（r5 活体五度 + 双重复测在案）；E-2~E-5 显式 pending
  且对应实现被 gate，符合待捕项裁定条件（样本路径、断言、负例、fail-closed 均已
  登记于 E 表）。

## 7. 结论

v1.7 是 r5 通过链（闭合于 v1.4）之后三轮 owner 驱动修订（v1.5 手动启动与桶隔离、
v1.6 独立包与 `opencode` 身份重用、v1.7 公共依赖边界）的累积版本，头部如实声明
需要完整复审。本轮以新评审员身份完整重读全文，对历轮从未评审的 §3.0/§3.11/
E-11 与手动启动链做了全量锚点亲核（双仓 23 处身份接线锚 + 4 处架构可行性锚 +
14 处上游手动启动链锚 + 3 处承重锚抽样 + think.md 5 处），除 1 处 +2 行号漂移
（F-16）外全部命中；门控全局重查无环、S-2b 前置四处一致、退役可分离标准三处
一致；三轮修订的方向均有 owner 裁决背书且未被后续修订破坏；经验教训（四份历史
接入设计 + think.md 复盘 + 数据真实性纪律）吸收情况逐项核实。

**verdict: APPROVED**——v1.7 设计层面通过（覆盖 v1.4→v1.7 全部累积改动）；实施
就绪度以 §1 所列门为准，逐片开工前照常校验对应 E/OD/授权；实施启动由 owner
决定。F-16/F-17 为引用精度级建议，实施期收编即可，无需为此再轮修订。
