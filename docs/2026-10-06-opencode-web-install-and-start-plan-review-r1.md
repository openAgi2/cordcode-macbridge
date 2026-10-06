# OpenCode Web 安装/启动方案评审报告（第 1 轮）

评审日期：2026-10-06
评审员模式：fresh（未见历轮内容，先独立形成风险清单；本轮为首轮，无历史意见回归项）
契约版本：plan-contract-v1.1（plan-review SKILL.md + references/plan-contract.md + references/review-guide.md；audit-plan 接入约定已读，专项未启动，理由见「来源与覆盖」）

## 结论

- **verdict: REVISION_REQUIRED**（不通过，需修订后送第 2 轮）
- 方案：`docs/2026-10-06-opencode-web-install-and-start-plan.md`，v1，SHA-256
  `cde3566976cd8b83ac5c6872dbfa5390f0f2df122ce1b105464fe6d50cf06a22`
  （本轮读取时 `shasum -a 256` 实测一致，评审对象身份成立）
- blockers：**2**（F-1、F-2）；advisories：**3**（F-3、F-4、F-5）
- 范围：full（简报「评审范围」七项重点全部覆盖，round_complete=true）
- implementation_readiness：方案阶段，无切片可开工——评审未通过，且简报明确本轮不构成实施授权。修订通过后：§9 条目 4（installer argv 与测试断言）仍受 **OD-2** 裁决门；条目 2/3/5 的冷启动语义带 **OD-1** A/B 分叉（A=现状保留，B 需改 `resolveManagedOpenCodeIfNeeded` 语义与 §2.2 场景）；**E-7** 为实施后 owner 真机验收门。

总体判断：方案的**事实底座非常扎实**——§1 现状链路的全部行号锚点经逐项亲核无一失真（见锚点核验表），dsh 参照的四拍/状态表/安装器约束镜像成立，§6 的结构差异论证（动作面在 Swift、状态面在 go-bridge）与两侧源码事实吻合，E-3/E-4 本轮重跑复现，E-5 的运行推断标注诚实，iOS 零改动声明经 iOS 仓源码与全仓 grep 核实成立。四拍交互完整可走查，无「参数二期/展示另做」腰斩。需要修订的是两处设计层缺陷：§3 行文本决策函数与状态表在 external_http/disabled 行上自相矛盾（F-1），§5.2 的 Task.detached 线程设计打破了 OpenCodeManagedServer 现有的 MainActor 串行不变量而未给出替代并发设计（F-2）。两处都是小改动可闭合，但按契约属于必须修订的方案缺陷。

## 来源与覆盖

### 来源身份表（本轮亲核）

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 核验方式 |
| --- | --- | --- | --- | --- | --- |
| Mac（被评审方案所在） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | f23124125cb6f967227887665932029680dd81c3 | 与方案 §0 清单逐项吻合（见下） | `git rev-parse HEAD` / `git status --porcelain` / `git show --stat` |
| iOS | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | 4666b5a5bdb23552472523db9171ed22c9369390 | 有 session-markdown-image 任务的未提交文件（与本方案无重叠，方案 §0 已声明） | `git rev-parse HEAD` |
| 上游 opencode | /Users/jacklee/Projects/opencode | dev（2fa3363c92） | 2fa3363c924c5c3e367b84a87ae478296a0ed59b | 未检查（只读 README 与 tag 列表，不参与源码结论） | `git rev-parse HEAD` / `git tag` |
| dsh 上游 | /Users/jacklee/Projects/deepseek-harness | — | — | — | 只读 README.zh.md:20-30（npx 命令行核验） |

**HEAD 说明**：方案 §0 记 HEAD=`2e39e805f51ab21a201f2f732ad9bda57c99690c`；本轮评审时实际 HEAD=`f2312412`。经 `git log`/`git show --stat` 核实：`f2312412` 是 `2e39e805` 之上**仅含本方案文档一个文件**的提交（385 insertions，无代码变更）。因此方案全部源码锚点在两个提交下内容一致，锚点核验有效；方案文档自身的来源身份自洽。

**未提交状态对拍**：方案 §0 列出的 M/?? 清单与 `git status --porcelain` 实测完全一致（M CHANGELOG.md、agent/dsh-web/fakedsh_test.go、docs/protocol/*（3）、go-bridge/{backend_capabilities,handlers,rpc_scopes,rpc_scopes_test,types}.go；?? agent/dsh-web/session_media{,_test}.go、core/session_media.go、go-bridge/handlers_session_media{,_test}.go、session_media_protocol_test.go、docs/protocol/samples/session-media/）。本方案引用的 dsh 参照文件（readiness.go/installer.go/binary.go/resolver.go/WorkspaceView/agent_descriptor 等）均不在脏文件清单内，核验不受污染。

**本轮亲核范围**：
- §1 全部行号锚点（RuntimeManager / OpenCodeManagedServer / opencodeweb.go / agent_descriptor.go / BridgeStatusView / WorkspaceView / probe.go / OpenCodeEndpointResolver / iOS BackendModels）；
- §3/§4/§5/§6 引用的 dsh 参照实现（dsh 方案文档全文、readiness.go、installer.go、binary.go、resolver.go StartSeat、management_api.go 端点、WorkspaceView deepSeekSeatControls/deepSeekSeatHint、DeepSeekSeatActionTests、L10n `dsh_web_*` 键）；
- E-3（opencode README.md:50/53/56-57）、E-4（本轮重跑 `npm view opencode-ai version`→1.18.34、`opencode --version`→1.18.34、`ls -la /opt/homebrew/bin/opencode` 符号链接、`git tag | grep -c v1.18`→0）、E-6（iOS BackendModels.swift:25-37 + iOS 全仓 grep status 字符串使用）；
- OD-2 前提（`clientFor` fail-closed 于 generation118；`npm view opencode-ai@1.18 version` 列出 1.18.x 系列、dist-tags.latest=1.18.34）；
- 辅助事实：`resolveManagedOpenCodeIfNeeded` 唯一调用点（RuntimeManager.swift:466）、120 分钟兜底重启（:625）、60 秒 5 次熔断（OpenCodeManagedServer.swift:554-558）、`applyConfigAndRestart`（:428）、`handleAgentTest` 只重建描述符（management_api.go:696-710）、BackendStatusViewModel 的 3 秒轮询仅覆盖 codex-remote 恢复（:55-68）与 dsh 行动轮询→loadAgents 收口（:231-245）。

**未核范围与影响**：未深核上游 opencode dev 分支的 v2 形态本身（方案仅用它解释 npm 线与 dev 线的差异，判定门在本仓 probe.go 源码内，已核）；未运行任何构建/测试（方案阶段，§8 为过审后验证计划，不适用）；未读 dsh 方案的 r1/r2 历史评审报告（OpenCode 方案引用的是 dsh 方案文档与已实施代码，二者均已亲核）。

**audit-plan 专项**：未启动。理由：方案不新增外部内容形状声明——wire 枚举值（not_configured/service_not_running/available）为既有值并在双仓源码核验；探针形状判定为既有代码（probe.go:144-209）；唯一涉及上游形状的命题（1.18.34 与 1.18.18 同形状）已被方案自标为「运行推断，实施门复核」，未写成已验证事实，符合待捕项纪律。若实施期要把该推断升级为事实，需按实施门补真实样本。

### 锚点核验表（方案原引用 → 实际位置，全部本轮亲核）

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 |
| --- | --- | --- |
| RuntimeManager.swift:457 `launchBridgeProcess()` | :457 `private func launchBridgeProcess() {`；:466 `resolveManagedOpenCodeIfNeeded()` | 准确 / 成立（启动时调用一次） |
| RuntimeManager.swift:1015 `resolveManagedOpenCodeIfNeeded()` | :1015 `private func resolveManagedOpenCodeIfNeeded() {`，:1016 `guard config.opencodeSource == .managedLocal` | 准确 / 成立 |
| OpenCodeManagedServer.swift:185 `ensureRunning(timeout: 5.0)` | :185 `func ensureRunning(timeout: TimeInterval = 5.0) -> OpenCodeManagedEndpoint? {` | 准确 / 成立 |
| OpenCodeManagedServer.swift:304 端口选择 | :304 `private func selectPort(preferred: Int, state: PersistedState) -> Int?`；:308 `for port in 4096...4196 {` | 准确 / 成立（持久端口优先 :305） |
| OpenCodeManagedServer.swift:222 spawn argv | :222 `arguments: ["serve", "--hostname", "127.0.0.1", "--port", "\(port)", "--print-logs"],` | 准确 / 成立 |
| RuntimeManager.swift:1028-1030 失败置空 URL | :1028 `guard let endpoint = openCodeManagedServer?.ensureRunning(timeout: 5.0) else {` :1029 `config.opencodeURL = ""` :1030 `return` | 准确 / 成立 |
| RuntimeManager.swift:1490-1496 argv | :1490 `if !config.opencodeURL.isEmpty {` … :1495 `arguments += ["-opencode-web-url", config.opencodeURL]` | 准确 / 成立 |
| RuntimeManager.swift:1530-1543 env 凭据 | :1533 `environment["OPENCODE_WEB_SERVER_USERNAME"] = config.opencodeUser`；:1538 `environment["OPENCODE_WEB_SERVER_PASSWORD"] = config.opencodePass` | 准确 / 成立 |
| opencodeweb.go:340 `InstanceStatus()` | :340 `func (a *Agent) InstanceStatus() (available bool, detail string) {`；:341-342 URL 空→NotConfiguredDetail；:355-356 err→原文 | 准确 / 成立 |
| agent_descriptor.go:243-246 布尔折叠接线 | :243 `case "opencode-web":` :246 `return detectInstanceStatusProber("opencode-web", agent)` | 准确 / 成立 |
| agent_descriptor.go:273-283 折叠实现 | :278-282 `available, detail := prober.InstanceStatus()` … `return AgentStatusNotConfigured, detail` | 准确 / 成立（available=false 一律 not_configured） |
| BridgeStatusView.swift:338 | :338 `case "not_configured": return L10n.notConfigured` | 准确 / 成立 |
| WorkspaceView.swift:658-666 重新检查 | :658 `if !agent.isAvailable {` :659 `Button(L10n.workspaceRecheck) {` | 准确 / 成立 |
| agent_descriptor.go:22-25 枚举 | :22 `AgentStatusNotDetected AgentStatus = "not_detected"` :24 `AgentStatusServiceNotRunning AgentStatus = "service_not_running"` | 准确 / 成立（opencode-web 现未用） |
| iOS BackendModels.swift:25-37 | :25 `guard descriptor.status == "available" else {`；:26 pairing_required 特判；:32-35 reason 透传 | 准确 / 成立（路径为 OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift，方案未写全路径但全仓唯一） |
| OpenCodeManagedServer.swift:321-345 catalog 降级守卫 | :321 `private func canAdoptPersistedProcess(...)`；:339-343 `if adoptedProcessHasCatalogDegradation(pid: pid) { kill(pid, SIGTERM) … return false }` | 准确 / 成立 |
| RuntimeManager.swift:440-444 shutdownForExit | :440 `func shutdownForExit() {` :444 `openCodeManagedServer?.stop()` | 准确 / 成立 |
| opencodeweb.go:345 err 缓存不命中 TTL | :345 `if a.probe == nil \|\| a.probe.err != nil \|\| time.Since(a.probe.at) > instanceStatusProbeTTL {` | 准确 / 成立（失败探针每次重探） |
| WorkspaceView.swift:546-579 dsh 纯函数 | :546 `enum DeepSeekSeatAction`；:555 `static func deepSeekSeatAction(...)`；:573 `static func deepSeekRowStatusText(...)`（:577 default→BackendStatusText.display） | 准确 / 成立（注意 default 分支保留全局映射——与 F-1 相关） |
| RuntimeManager.swift:253-276 defaultCLISearchPath | :253 `private static func defaultCLISearchPath() -> [String]`（含 ~/.bun/bin、/opt/homebrew/bin、/usr/local/bin、~/Library/pnpm、~/.volta/bin、~/.npm-global/bin） | 准确 / 成立 |
| OpenCodeManagedServer.swift:51-67 CLI resolver | :51 `struct DefaultOpenCodeCLIResolver`（搜索路径 + 3 个硬编码兜底） | 准确 / 成立 |
| OpenCodeManagedServer.swift:102-129 AppKit | :104 `NSRunningApplication.runningApplications(...)`；:124 `NSWorkspace.shared.openApplication(...)` | 准确 / 成立 |
| opencodeweb.go:335-339 探针只读 | :335-339 注释 `The probe is a read-only GET sequence; it never spawns, binds, or writes.` | 准确 / 成立 |
| management_api.go:775-839 dsh 动作端点 | :775 注释 `── POST /internal/agents/{id}/install · /start · GET action-state`；:796 handleAgentInstall；:812 handleAgentStart；:828 handleAgentActionState | 准确 / 成立 |
| probe.go:144-209 generation 门 | :146 `probeInstance`；:150-166 /global/health→/api/health 分流；:170-193 /session bare array vs /api/session data envelope 形状仲裁 | 准确 / 成立（按 API 形状判定，非版本号比对） |
| OpenCodeEndpointResolver.swift:14-16 | :16 `case externalHttp = "external_http"`（注释 bring-your-own-server） | 准确 / 成立 |
| agent_descriptor_test.go:965 | :965 `status, reason := detectInstanceStatusProber("opencode-web", a)` | 准确 / 成立 |
| opencodeweb_test.go:19-79 | :19-35 空 URL→NotConfiguredDetail；:37-59 探针失败→probe failed 原文；:61-79 成功携带 generation | 准确 / 成立 |
| WorkspaceView.swift:591 codex 先例 | :591 `if agent.kind.lowercased() == "codex-web", runtimeManager.codexDaemonConfigChanged {` | 准确 / 成立 |
| opencode README.md:53 / :50 / :56-57 | :50 `curl -fsSL https://opencode.ai/install \| bash`；:53 `npm i -g opencode-ai@latest`；:56-57 两个 brew formula | 准确 / 成立（E-3） |
| dsh README.zh.md:26 | :26 `npx @deepseek-ai/dsh web` | 准确 / 成立 |
| dsh binary.go `findDSHBinary` record-first | binary.go:44 `findDSHBinary`（:46-52 安装记录→PATH→nvm）；:24 `installRecordFile = "dsh-web-install-record.json"` | 准确 / 成立 |
| dsh installer.go 约束 | :241 `bin = filepath.Join(prefix, "node_modules", ".bin", "dsh")`；:340 `AtomicWriteFile(..., 0o600)`；:33-37 10 分钟超时；单飞 seatActionMu | 准确 / 成立（§4 镜像逐项成立） |
| dsh resolver.go `StartSeat` | :696 `func (r *Resolver) StartSeat(ctx context.Context) (*ResolvedInstance, error) {` | 准确 / 成立（§6 spawn 在 go-bridge 成立） |

E-1/E-2 按上表全部亲核通过；E-3/E-4 本轮命令重跑复现；E-5 机制部分（形状门）亲核通过、运行推断部分维持方案自标的待复核状态；E-6 亲核通过（另经 iOS 全仓 grep：生产代码中无任何按 backend wire status 字符串分支的代码，仅 codex-remote pairing_required 特判，`not_detected` 命中均为 task-dock 测试态，与 backend 无关）。

## 意见

### F-1［阻塞］§3 行文本决策函数与 §3 状态表（及 §7）在 external_http 未配置 / disabled 两行上互相矛盾

- **位置**：方案 §3 决策函数（「行文本（OpenCode 行）: wire available → 就绪 / source==managedLocal && cliFound==false → 未安装 / **其余非 available（not_configured / service_not_running）→ 未启动**」）对 §3 状态表第 6、8 行（`source=external_http 未配置 URL → 未配置`；`source=disabled → 未配置`）。
- **证据**：状态表（方案 :169、:171）明确 external_http 未配置 URL 与 disabled 两行显示「未配置」；§7（:300-301）承诺「不改 external_http 用户的行为……bring-your-own-server 语义不变」；现状代码 BridgeStatusView.swift:338 将 `not_configured` 映射为「未配置」，OpenCode 行今天经 WorkspaceView.swift:611-613 的 `agent.displayStatus` 走同一全局映射。而决策函数的兜底分支把一切非 available（含这两行的 `not_configured`）写成「未启动」。被镜像的 dsh 实现（WorkspaceView.swift:573-579 `deepSeekRowStatusText`）恰恰用 `default: return BackendStatusText.display(status)` 保留全局映射。
- **影响**：§3 的纯函数是 §8「决策矩阵纯函数测试」的实施与断言依据；照它实现会把 external_http 未配置/disabled 用户的行文案从「未配置」改成「未启动」——这既违背状态表，也违背 §7 的零行为变更承诺，且会被矩阵测试锁死成错误行为。按钮矩阵（source != managedLocal → 无按钮）本身与表格一致，问题只在行文本函数。
- **修订方向**：最小改动——行文本函数补一条 `source != managedLocal → 沿用全局映射（not_configured → 未配置）` 分支（即镜像 `deepSeekRowStatusText` 的 default→`BackendStatusText.display`），使函数与 8 行表格逐行对齐。
- **闭合标准**：下一版 §3 中状态表与决策函数在全部 8 行上输出一致；§8 矩阵测试清单补 external_http/disabled 行文本断言（现有清单只写了「external_http 无按钮」）。

### F-2［阻塞］§5.2 的 Task.detached 设计打破 OpenCodeManagedServer 现有 MainActor 串行不变量，未给出并发/序列化设计

- **位置**：方案 §5.2「新增显式 `startOpenCodeManagedServer()`（RuntimeManager，`@MainActor` 入口 + `Task.detached` 执行，避免 `ensureRunning` 的 `Thread.sleep` 卡主线程 5 秒）」。
- **证据**：`OpenCodeManagedServer` 是普通 `final class`，可变状态（`process`/`stderrPipe`/`stderrHandle`/`consecutiveFailures`/`state`，OpenCodeManagedServer.swift:157-161）**无任何锁、非 actor**；今天它的全部入口都来自 `@MainActor` 的 RuntimeManager（`resolveManagedOpenCodeIfNeeded` :1015，唯一调用点 launchBridgeProcess :466；`shutdownForExit` :440 调 `stop()`），即隐式依赖「只在主线程访问」这一未成文不变量。方案新增后台线程调用者后，两个现实并发窗口出现：(a) 启动动作在 detached 任务中跑 `ensureRunning` 期间，用户改任意配置触发 `applyConfigAndRestart`（RuntimeManager.swift:428）→ `launchBridgeProcess` → 主线程再次 `ensureRunning`；(b) 启动动作进行中 App 退出 → 主线程 `shutdownForExit` → `stop()`/`stopOwnedProcess` 与后台 spawn 并发。两处都会对 `process`/`state`/`consecutiveFailures`/端口选择/收养判定做无同步并发读写。§8 测试清单也没有对应不变量。
- **影响**：数据竞争与状态损坏（进程句柄丢失、熔断计数错乱、双 spawn）；这是 §5.2 自己引入的线程模型变更，属于设计层缺口而非实现细节——按共享约定「写入所有者与受影响不变量必须清楚」，方案必须写明替代的串行化策略。
- **修订方向**：最小改动——在 §5.2 补一段执行上下文设计：所有 `OpenCodeManagedServer` 入口（`ensureRunning`/`stop`/未来的熔断重置）串行化到单一执行域（如专用串行队列或 actor；MainActor 调用方 hop 过去），并写明 `shutdownForExit` 与进行中启动动作的收口关系（等待/取消/互斥）。§8 增加一条对应不变量或测试（启动进行中 + stop/restart 交错的串行化断言）。
- **闭合标准**：下一版 §5.2 写明串行化策略与 shutdown 交互；§8 出现对应测试条目；`consecutiveFailures` 重置所需的内部 API 一并点名（现为 private，RuntimeManager 无法直接重置）。

### F-3［建议］§7/OD-1 对 dsh 冷启动裁决的出处表述与 dsh 方案文档自身记录不一致

- **位置**：方案 §7（:302-303）「dsh 移除是 owner 2026-09-22 的明确改令」。
- **证据**：dsh 方案文档（docs/2026-09-22-dsh-web-install-and-start-plan.md）明确区分两者：「**冷启动缺位自动补拉 → 等用户点「启动」。这是方案推导，不是另一句 owner 裁决原文**」；owner 改令原文只覆盖代装。任务简报字段 3 也把两者合并表述，但仓内更接近源头的记录是 dsh 方案文档。
- **影响**：出处失真不影响 OD-1 的 pending 状态与推荐 A 的独立理由（owner 本次只要求补恢复路径），但会误导后续读者对 dsh 先例的权重判断（若把它当 owner 裁决，会更有理由在 OpenCode 复制移除；当方案推导则更没有理由延伸）。
- **修订方向**：一句话改写为「dsh 移除自动补拉是该方案的推导结论（其文档自标非 owner 改令原文），OpenCode 无对应指令」。
- **闭合标准**：§7/OD-1 表述与 dsh 方案文档的裁决出处记录一致。

### F-4［建议］§5.5 动作状态面字段清单缺 `source`，动作收口后的行刷新触发未写明

- **位置**：§5.5 状态字段清单（cliFound/npmFound/installing/starting/lastInstallError/lastStartError/lastInstallNote）与 §2.2「行刷新即绿」。
- **证据**：§3 决策函数需要 `source != managedLocal`（按钮矩阵）与 `source==managedLocal`（行文本），但 §5.5 清单不含 source；dsh 的 SeatActionSnapshot 含 NpmPath/BinPath，本方案清单未列（可接受的最小化，但 source 是决策输入，不是可选项）。行刷新：现有 3 秒可用性轮询只覆盖 codex-remote 恢复（BackendStatusViewModel.swift:55-68），dsh 行变绿靠其行动轮询收口后调 `loadAgents`（:231-245）；OpenCode 无 action-state 端点可轮询，wire 状态来自 GET /internal/agents，动作收口后必须有人重新拉描述符——§5.2 只写了「URL 未变 → 只刷新行状态」，未写由谁、经何路径触发。
- **影响**：实现者需自行补两处接线决定；不影响可行性（探针对失败每次重探已核，opencodeweb.go:345），但属于方案应写清的接口面。
- **修订方向**：§5.5 清单补 `source`（或写明读 `runtimeManager.config.opencodeSource`）；§5.2/§5.5 写明启动/安装收口后由动作本身触发一次描述符重取（`loadAgents`/`testAgent`）。
- **闭合标准**：下一版 §5.5 的行输入集合覆盖 §3 决策函数的全部输入；收口刷新路径有明确一句。

### F-5［建议］§5.1 失败持久化 URL 与 Desktop sidecar 同步的交互未分析

- **位置**：§5.1「ensureRunning 失败时……照常写入 config.opencodeURL/User/Pass」。
- **证据**：`launchBridgeProcess` 在 `resolveManagedOpenCodeIfNeeded`（:466）之后立即调 `configureOpenCodeDesktopServerIfNeeded`（:467），后者把 `config.opencodeURL` 写进 OpenCode Desktop 的 sidecar 设置（RuntimeManager.swift:1003-1012）。今天冷启动失败时 URL 被置空（:1029），Desktop 得到空 URL；方案改为持久 URL 后，冷启动失败场景下 Desktop sidecar 会收到一个死 URL。
- **影响**：相邻路径的未声明行为变化（Desktop 指向暂时不可达的 loopback 地址；服务被「启动」拉起后自然恢复，但方案未表态这是接受还是应跳过）。
- **修订方向**：§5.1 补一句 Desktop sidecar 的预期行为（接受死 URL 等恢复，或 ensureRunning 失败路径跳过 Desktop 同步）；若选择后者，§8 补一条断言。
- **闭合标准**：§5.1 明示该交互的处理选择。

## 复审处置与剩余门

首轮评审，无历史意见处置项。上述 F-1/F-2 为阻塞项，需作者修订方案文档后送第 2 轮；F-3/F-4/F-5 为建议项，可随修订一并处理（不采纳需逐项写明理由，按项目方案评审循环纪律）。

**仍待满足的门（与阻塞意见分列，不因方案通过而自动满足）**：

| 门 | 类型 | 状态 | 说明 |
| --- | --- | --- | --- |
| OD-1 冷启动自动拉起去留 | 决定门 | pending | A=保留（现状，方案按 A 设计）；B=移除（需改 §9 条目 2/3/5 语义与 §2.2 场景）。两选项各有独立验收 |
| OD-2 安装版本 spec | 决定门 | pending | 精确挡住 §9 条目 4 的 installer argv 与测试断言（方案全文以 `<OD-2 选定的版本 spec>` 参数化，无法绕过）。推荐 A=`1.18` 的前提（clientFor 只认 generation118、npm range 解析到最高 1.18.x）本轮已核成立 |
| E-5 运行推断部分 | 证据门 | 待实施门复核 | 形状门机制（probe.go:144-209）已核；「1.18.34 与 1.18.18 同形状」为方案自标运行推断，实施期需以真实服务复核 |
| E-7 真实 npm 安装 + 真实 4096 启动 | 证据门 | pending | 实施后 owner 在工作站行点一次安装/启动；agent 核对本次独有日志，不用旧截图 |

**授权边界确认**：本轮仅评审与写本报告，未修改方案/业务代码/其他文件，未 commit，未部署，未运行构建或测试。实施仅在方案通过且 owner 另行启动后开始。

## 评审员交接块

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-06-opencode-web-install-and-start-plan.md
plan_sha256: cde3566976cd8b83ac5c6872dbfa5390f0f2df122ce1b105464fe6d50cf06a22
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-06-opencode-web-install-and-start-plan-review-r1.md
scope: full
blockers: 2
advisories: 3
open_gates: [OD-1, OD-2, E-5(运行推断部分,实施门复核), E-7]
round_complete: true
contract_version: plan-contract-v1.1
```
