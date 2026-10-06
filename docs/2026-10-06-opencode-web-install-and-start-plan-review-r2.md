# OpenCode Web 安装/启动方案评审报告（第 2 轮）

评审日期：2026-10-06
评审员模式：fresh（未见历轮结论，先独立形成风险清单并完成亲核，再读 r1 报告作回归比对）
契约版本：plan-contract-v1.1（plan-review SKILL.md + references/plan-contract.md + references/review-guide.md；audit-plan 专项未启动，理由同 r1：方案不新增外部内容形状声明，wire 枚举为双仓既有值，唯一上游形状命题已被方案自标「运行推断，实施门复核」）

## 结论

- **verdict: APPROVED**（方案通过；2 条建议项不构成阻塞，可随下次文档触碰一并处理）
- 方案：`docs/2026-10-06-opencode-web-install-and-start-plan.md`，v2。派发哈希
  `6fd983ce86b7966003df3d77b2a25632420a1bc39b3e3b68900b5edbcd8fedd2`，
  本轮读取时 `shasum -a 256` 实测一致，评审对象身份成立；方案交接块自声明的正文哈希
  （前 537 行）`673318c7923830fab95ad44ca115dc91ae2667696787b4a95a18a69ce05e81fe`
  亦经 `head -n 537 | shasum -a 256` 复算一致。
- blockers：**0**；advisories：**2**（F-6、F-7，均为本轮新发现的文档准确性问题）
- 范围：full（简报「评审范围」七项重点全部覆盖，round_complete=true）
- implementation_readiness：方案层面通过。实施仍被以下门挡住，与阻塞意见分列：
  **OD-1**（冷启动自动拉起去留，pending）——方案按推荐 A（=现状保留）设计，B 分叉的
  切片影响已在 OD 表映射；**OD-2**（安装版本 spec，pending）——installer argv 与测试
  断言全文以 `<OD-2 选定的版本 spec>` 参数化，无法绕过裁决直接落笔；**E-5 运行推断
  部分**（1.18.34 与 1.18.18 同形状）——实施门需以真实服务复核；**E-7**（真实 npm
  安装 + 真实 4096 启动）——实施后 owner 验收。本评审不构成实施授权。

总体判断：v2 对 r1 五条意见（2 阻塞 + 3 建议）的处置全部成立且经本轮逐项亲核——
F-1 的行文本决策函数重写后与 8 行状态表逐行一致（本轮按 8 行逐行推演对拍）；
F-2 的串行执行域设计事实底座准确、并发窗口分析与源码吻合、无死锁路径；F-3 的
dsh 出处更正与 dsh 方案文档 :44/:84 原文一致；F-4 的 source 字段与 testAgent 收口
刷新路径接线成立；F-5 的 Desktop sidecar 交互处理选择明确且理由可核。§1 现状链路
全部行号锚点本轮重新逐项亲核无一失真；四拍交互完整可走查、无腰斩；iOS 零改动声明
经 iOS 仓源码与全仓 grep 复核成立；§8 测试计划引用的全部测试基础设施（fake
server、stub 注入、DeepSeekSeatActionTests、agent_descriptor_test.go:965、L10n
`dsh_web_*` 键、CHANGELOG `[Unreleased]`）均存在且形态相符；§9 接线顺序依赖关系
成立。本轮新发现的两条建议均为文档准确性问题（§0 来源清单滞后于实际提交链、
§5.1 Desktop sidecar 守卫描述不完整），不影响任何设计结论与验收路径。

## 来源与覆盖

### 来源身份表（本轮亲核）

| 仓库 | 路径 | 分支 | 实际提交（本轮 `git rev-parse HEAD`） | 方案 §0 记录 | 核验方式 |
| --- | --- | --- | --- | --- | --- |
| Mac（被评审方案所在） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | `bd5e772d5fca88501e2335fdda753be75af0dffc` | `f2312412…`（滞后，见 F-6） | `git rev-parse HEAD` / `git status --porcelain` / `git log` / `git diff --stat f2312412..HEAD` |
| iOS | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | `4666b5a5bdb23552472523db9171ed22c9369390` | 同左（一致） | `git rev-parse HEAD` / `git status --porcelain -- BackendModels.swift`（干净） |
| 上游 opencode | /Users/jacklee/Projects/opencode | dev | `2fa3363c924c5c3e367b84a87ae478296a0ed59b`（describe: github-v1.2.25-2107-g2fa3363c92） | `2fa3363c92`（缩写，见 F-6） | `git rev-parse HEAD` / `git describe --tags` / `git tag` |
| dsh 上游 | /Users/jacklee/Projects/deepseek-harness | — | — | — | 只读 README.zh.md:26（`npx @deepseek-ai/dsh web` 行核验） |

**提交链说明（与 F-6 相关）**：方案 §0 记 HEAD=`f2312412`（v1 落点）并声明「v2
修订基于 f2312412，修订后另行提交」。本轮实测提交链为
`f2312412`（v1 方案）→ `46cfb34b`（v2 修订）→ `bd5e772d`（r1 报告归档，当前
HEAD）。`git diff --stat f2312412..HEAD` 证实两跳**只动本方案文档与 r1 报告两个
docs 文件（+218/+156），零源码变更**——因此方案全部源码锚点在 §0 记录提交与实际
HEAD 下内容一致，锚点核验有效；评审对象（v2 全文）哈希与派发记录一致。§0 的滞后
是文档准确性问题（F-6），不是评审对象身份问题。

**未提交状态对拍**：方案 §0 列出的 session-media 任务 M/?? 清单与
`git status --porcelain` 实测逐项一致（M CHANGELOG.md、agent/dsh-web/fakedsh_test.go、
docs/protocol/*（3）、go-bridge/{backend_capabilities,handlers,rpc_scopes,
rpc_scopes_test,types}.go；?? agent/dsh-web/session_media{,_test}.go、
core/session_media.go、go-bridge/handlers_session_media{,_test}.go、
session_media_protocol_test.go、docs/protocol/samples/session-media/）。本方案引用
的 dsh 参照文件（readiness.go/installer.go/binary.go/resolver.go/WorkspaceView/
agent_descriptor 等）均不在脏文件清单内，核验不受污染。§0 中「??
docs/…-review-r1.md」一条现已提交（bd5e772d），归入 F-6。

**本轮亲核范围**（fresh 模式，未复用 r1 结论，全部本轮重做）：

- §1 现状链路全部行号锚点：RuntimeManager（:457/:466/:467/:1015/:1016-1028/
  :1028-1030/:1490-1496/:1530-1543/:440-444/:445-452/:428/:253-276/:993-1012/
  :994-997/:1029/:1035/:625）、OpenCodeManagedServer（:185/:222/:228-229/:304/
  :321-345/:131/:157-161/:160/:554-557/:185-245/:395-408/:534-552/:51-67/:102-129/
  :252）、opencodeweb.go（:325-360 含 :331/:335-339/:340/:341-342/:345/:358）、
  agent_descriptor.go（:18-34/:212-256/:243-246/:273-283/:331-360）、probe.go
  （:144-209）、BridgeStatusView（:334-348）、WorkspaceView（:541-579/:591/
  :654-666）、BackendStatusViewModel（:54-68/:229-248）、management_api.go
  （:694-710/:775-839）、OpenCodeEndpointResolver（:10-20）、iOS
  BackendModels.swift（:18-40）；
- v2 新增锚点（r1 F-2/F-4/F-5 的核实依据）全部亲核：并发形态（final class
  :131、可变状态 :157-161、consecutiveFailures private :160/:554-557、
  ensureRunning 不触 AppKit :185-245、waitUntilReady 阻塞 :395-408、
  stopOwnedProcess ≤2s :534-552）、3 秒轮询仅覆盖 codex-remote（:55-68）、dsh
  行动轮询收口（:231-248）、Desktop sidecar 写入链（:467/:993-1012/:994-997/
  :1029、saveState 先于 waitUntilReady :228-229、OpenCodeManagedServer.
  syncDesktopConfig :252）；
- dsh 参照实现：installer.go（npm install -g、no sudo/no npx、prefix 回退、
  0600 记录、10 分钟超时杀进程组、单飞、--version 验证、node_missing）、
  readiness.go（:91 StructuredInstanceReadiness seam）、resolver.go（:696
  StartSeat）、binary.go（:24 dsh-web-install-record.json、:44 findDSHBinary
  record-first）、management_api.go（:775-839 install/start/action-state 三
  端点）、dsh 方案文档（:40-46/:80-90 裁决出处、:117/:140 四拍与密钥边界）、
  DeepSeekSeatActionTests（矩阵/进行中态/全局映射保留断言）、
  OpenCodeManagedServerTests（StubCLIResolver/StubPortProber/StubHealthProbe/
  processFactory 注入）、L10n `dshWebStatusNotInstalled` 键、CHANGELOG
  `[Unreleased]`（:9）；
- E-3（README.md:50/:53/:56-57 @ 2fa3363c92）、E-4（本轮命令重跑：`npm view
  opencode-ai version`→1.18.34、`/opt/homebrew/bin/opencode --version`→1.18.34、
  `ls -la /opt/homebrew/bin/opencode`→符号链接指向
  ../lib/node_modules/opencode-ai/bin/opencode.exe、`git tag | grep "1\.18"`→
  仅 v0.1.18x/v1.1.18 命名空间，无 npm 线 v1.18.x tag，方案「本 checkout 无
  v1.18.x tag」声明成立）、E-5 机制部分（probe.go:144-209 形状门 + clientFor
  :396/:413 generation118 fail-closed + InstanceStatus :358 隔离）、E-6（iOS
  BackendModels.swift:25-37 + iOS 全仓 grep：生产代码无任何按 not_configured/
  service_not_running/not_detected 分支的代码，唯一 status 字符串特判是
  codex-remote 的 pairing_required）；
- 横切事实：`resolveManagedOpenCodeIfNeeded` 唯一调用点（:466）、120 分钟兜底
  重启（:625）、60 秒 5 次熔断（:554-557）、`handleAgentTest` 只重建描述符且探针
  GET-only（management_api.go:694-710）、描述符统一构建路径
  BuildAgentDescriptor→detectAgentStatus→:243-246 切换点（Mac 行 GET
  /internal/agents 与 hello 共用，单点切换即双面一致）、legacy `opencode` case
  吃 cfg.OpenCodeURL（:216-229，detectOpenCodeService 死 URL→
  AgentStatusServiceNotRunning :353-354）、默认 drivers 列表不含 "opencode"
  （RuntimeManager.swift:206，与 F-7 相关）。

**未核范围与影响**：未深核上游 dev 分支 v2 形态本身（方案仅用其解释 npm 线与
dev 线差异，判定门在本仓 probe.go 源码内，已核）；未运行构建/测试（方案阶段，
§8 为过审后验证计划）；未读 dsh 方案自身的历史评审报告（OpenCode 方案引用的是
dsh 方案文档与已实施代码，二者已亲核）。以上不影响本轮结论。

### 锚点核验表（方案原引用 → 实际位置，全部本轮亲核）

| 方案原引用 | 实际位置与原文摘录（节选） | 定位 / 语义 |
| --- | --- | --- |
| RuntimeManager.swift:457 `launchBridgeProcess()` | :457 `private func launchBridgeProcess() {`；:466 `resolveManagedOpenCodeIfNeeded()`；:467 `configureOpenCodeDesktopServerIfNeeded()` | 准确 / 成立 |
| RuntimeManager.swift:1015 resolve | :1015 `private func resolveManagedOpenCodeIfNeeded() {`；:1016 `guard config.opencodeSource == .managedLocal`；:1028-1030 失败置空 `config.opencodeURL = ""` | 准确 / 成立 |
| OpenCodeManagedServer.swift:185 ensureRunning | :185 `func ensureRunning(timeout: TimeInterval = 5.0) -> OpenCodeManagedEndpoint? {` | 准确 / 成立 |
| OpenCodeManagedServer.swift:304 端口段 | :304 `selectPort(preferred:state:)`；:305 持久端口优先；:308 `for port in 4096...4196` | 准确 / 成立 |
| OpenCodeManagedServer.swift:222 spawn argv | :222 `arguments: ["serve", "--hostname", "127.0.0.1", "--port", "\(port)", "--print-logs"]` | 准确 / 成立 |
| RuntimeManager.swift:1490-1496 argv | :1490 `if !config.opencodeURL.isEmpty {` … :1491 `-opencode-url`、:1495 `-opencode-web-url` | 准确 / 成立 |
| RuntimeManager.swift:1530-1543 env | :1533 `OPENCODE_WEB_SERVER_USERNAME`、:1538 `OPENCODE_WEB_SERVER_PASSWORD` | 准确 / 成立 |
| opencodeweb.go:340 InstanceStatus | :340 签名；:341-342 URL 空→NotConfiguredDetail；:345 `if a.probe == nil \|\| a.probe.err != nil \|\| time.Since(...) > TTL`（err 不缓存，失败每次重探） | 准确 / 成立 |
| opencodeweb.go:335-339 探针只读 | :335-339 注释 `read-only GET sequence; it never spawns, binds, or writes` | 准确 / 成立 |
| agent_descriptor.go:243-246 折叠接线 | :243 `case "opencode-web":` :246 `return detectInstanceStatusProber("opencode-web", agent)` | 准确 / 成立（切换点；dsh-web :242 已走 structured，镜像主张成立） |
| agent_descriptor.go:273-283 折叠实现 | :278-282 `available, detail := prober.InstanceStatus()` … `return AgentStatusNotConfigured, detail` | 准确 / 成立 |
| agent_descriptor.go:22-25/:22-33 枚举 | :22 `not_detected`、:24 `service_not_running`、:33 `not_configured` | 准确 / 成立（opencode-web 现未用 service_not_running） |
| BridgeStatusView.swift:338/:341/:334-348 | :338 `not_configured`→未配置；:341 `service_not_running`→未启动；:334-348 `BackendStatusText.display` 全表 | 准确 / 成立 |
| WorkspaceView.swift:658-666 重新检查 | :658 `if !agent.isAvailable {` :659-660 `Button(L10n.workspaceRecheck) { Task { await backendViewModel.testAgent(id: agent.id) } }` | 准确 / 成立（testAgent 先例） |
| WorkspaceView.swift:546-579/:577 dsh 纯函数 | :546 `enum DeepSeekSeatAction`；:555 `deepSeekSeatAction`；:573 `deepSeekRowStatusText`，:577 `default: return BackendStatusText.display(status)` | 准确 / 成立（F-1 修复的镜像依据） |
| WorkspaceView.swift:591 codex 先例 | :591 `if agent.kind.lowercased() == "codex-web", runtimeManager.codexDaemonConfigChanged {` | 准确 / 成立 |
| BackendStatusViewModel.swift:55-68/:231-248 | :55-68 3 秒轮询仅 codex-remote 恢复；:231-248 dsh 行动轮询收口→loadAgents | 准确 / 成立 |
| OpenCodeManagedServer.swift:131/:157-161/:160/:554-557 | :131 `final class OpenCodeManagedServer`；:157-161 `process/stderrPipe/stderrHandle/consecutiveFailures/state` 无锁；:160 private；:554-557 60 秒 5 次熔断 | 准确 / 成立（F-2 事实底座） |
| OpenCodeManagedServer.swift:185-245/:395-408/:534-552 | ensureRunning 全程无 AppKit；:396 `Thread.sleep(forTimeInterval: 1.0)` 阻塞等待；:544 stopOwnedProcess 2 秒 deadline + SIGKILL | 准确 / 成立 |
| OpenCodeManagedServer.swift:321-345 收养守卫 | :321 `canAdoptPersistedProcess`；:328-330 noAuth 401 + authed 200；:339-343 catalog 降级守卫 kill+respawn | 准确 / 成立 |
| OpenCodeManagedServer.swift:51-67 CLI resolver | :51 `struct DefaultOpenCodeCLIResolver`（搜索路径 + 3 硬编码兜底） | 准确 / 成立 |
| OpenCodeManagedServer.swift:102-129 AppKit | :104 `NSRunningApplication.runningApplications(...)`；:124 `NSWorkspace.shared.openApplication(...)` | 准确 / 成立（§6 论证依据） |
| RuntimeManager.swift:253-276 搜索路径 | :253 `defaultCLISearchPath()`（bun/homebrew//usr/local/pnpm/volta/~/.npm-global；**无 nvm**，方案「再加 nvm」成立） | 准确 / 成立 |
| RuntimeManager.swift:440-444/:445-452/:428 | :440 `shutdownForExit` :444 `openCodeManagedServer?.stop()`；:452 `semaphore.wait(timeout: .now() + 5)`；:428 `applyConfigAndRestart` | 准确 / 成立 |
| RuntimeManager.swift:467/:993-1012/:994-997/:1029/:1035 | :467 resolve 后立即调 Desktop 同步；:993-1012 sidecar 写入；:994-997 四条件守卫（**含 `config.drivers.contains("opencode")`**，见 F-7）；:1029 置空行；:1035 成功路径 syncDesktopConfig 主线程 | 定位准确 / 语义部分成立（守卫描述不完整 → F-7） |
| OpenCodeManagedServer.swift:228 saveState 先于健康等待 | :228 `saveState(persisted)` → :229 `guard waitUntilReady(...)` | 准确 / 成立（F-5 论证依据） |
| probe.go:144-209 generation 门 | :146 `probeInstance`；:149-171 /global/health→/api/health 分流；:173-202 /session bare array vs /api/session envelope 形状仲裁 | 准确 / 成立（按形状判定，非版本号） |
| management_api.go:775-839 dsh 动作端点 | :775 注释；:796 handleAgentInstall；:812 handleAgentStart；:828 handleAgentActionState | 准确 / 成立 |
| management_api.go:696-710 handleAgentTest | :706-708 只 BuildAgentDescriptor + updateAgentDescriptor，探针 GET-only | 准确 / 成立（「重新检查」只读） |
| iOS BackendModels.swift:25-37 | :25 `guard descriptor.status == "available" else {`；:26 pairing_required 特判；:32-35 reason 透传；全仓 grep 无其他 status 字符串分支 | 准确 / 成立（E-6/iOS 零改动） |
| OpenCodeEndpointResolver.swift:14-16 | :16 `case externalHttp = "external_http"`（bring-your-own-server） | 准确 / 成立 |
| agent_descriptor_test.go:965 | :965 `status, reason := detectInstanceStatusProber("opencode-web", a)` | 准确 / 成立（§8 切换断言对象存在） |
| opencodeweb_test.go:19-79 | :19-35 空 URL；:37-57 探针失败；:59-79 成功携带 generation（startFake/fakeServe 注入式构造） | 准确 / 成立 |
| opencode README.md:53/:50/:56-57 | :50 curl 脚本；:53 `npm i -g opencode-ai@latest`；:56-57 两个 brew formula | 准确 / 成立（E-3） |
| dsh README.zh.md:26 | :26 `npx @deepseek-ai/dsh web` | 准确 / 成立 |
| dsh binary.go/installer.go/resolver.go/readiness.go | binary.go:24 记录文件名、:44 findDSHBinary record-first；installer.go:33-36 npm/no-sudo/no-npx/10min、prefix 回退、0600；resolver.go:696 StartSeat；readiness.go:91 StructuredInstanceReadiness | 准确 / 成立（§4/§6 镜像与差异论证依据） |
| dsh 方案 :44/:84 | :44「这是方案推导，不是另一句 owner 裁决原文」；:84「内容：只改代装…不是这句改令的原文」 | 准确 / 成立（F-3 更正与源头一致） |

E-1/E-2 按上表全部亲核通过；E-3/E-4 本轮命令重跑复现；E-5 机制部分亲核通过、
运行推断部分维持方案自标的待复核状态；E-6 亲核通过。

## 意见（本轮新发现，均为建议级）

### F-6［建议］§0 来源清单滞后于实际提交链，且上游提交为缩写哈希

- **位置**：方案 §0「提交=f23124125cb6f967227887665932029680dd81c3（…v2 修订基于
  f2312412，修订后另行提交）」与未提交状态清单中「?? docs/…-review-r1.md（…随
  v2 一并提交）」；「上游提交=2fa3363c92」。
- **证据**：本轮 `git rev-parse HEAD` 实测 `bd5e772d5fca…`；`git log` 证实提交链
  f2312412（v1）→ 46cfb34b（v2 修订，已提交）→ bd5e772d（r1 报告归档，已提交）；
  `git diff --stat f2312412..HEAD` 仅两个 docs 文件（+218/+156），零源码变更。
  上游完整哈希为 `2fa3363c924c5c3e367b84a87ae478296a0ed59b`（本轮解析无歧义），
  §0 记的是 9 位缩写；工作树 P0 来源门要求「完整提交哈希」。
- **影响**：不影响任何源码锚点与设计结论（两跳均为 docs-only，锚点在两提交下
  内容一致；评审对象哈希与派发记录一致）。但方案获批后进入实施时，实施者初读
  §0 会得到错误的仓库身份（以为 v2 未提交、r1 报告未跟踪、HEAD 在 f2312412），
  与 P0「三个独立门点重新生成来源清单」的纪律形成不必要的摩擦；上游缩写哈希
  不满足来源清单的完整性字面要求。
- **修订方向**：下次触碰方案文档时（登记 OD 裁决或实施启动前）更新 §0：提交
  字段改为实际基线（bd5e772d 或届时 HEAD）、删除已兑现的「修订后另行提交」与
  r1-untracked 两条、上游提交写完整 40 位哈希。
- **闭合标准**：§0 的提交与未提交状态字段与当时 `git rev-parse HEAD` /
  `git status --porcelain` 实测一致；上游提交为完整哈希。

### F-7［建议］§5.1 Desktop sidecar 段的守卫描述不完整，F-5 场景的实际适用面比方案所述更窄

- **位置**：方案 §5.1 Desktop sidecar 交互段「后者在 URL/User/Pass 非空时把
  config.opencodeURL 写进 OpenCode Desktop 的 sidecar 设置（:993-1012）」及该段
  的接受性论证。
- **证据**：`configureOpenCodeDesktopServerIfNeeded` 的实际守卫（
  RuntimeManager.swift:994-997）是**四**条件：`config.drivers.contains("opencode")`
  **且** URL/User/Pass 非空。默认 drivers 列表（RuntimeManager.swift:206）为
  `["claude", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]`——**不含
  legacy "opencode"**。因此默认配置下冷启动失败路径根本不写 sidecar（:467 被
  drivers 守卫挡住；:1035 的 `syncDesktopConfig` 只在 resolve 成功路径执行），
  方案所述「冷启动失败场景 sidecar 会收到一个暂时不可达的 loopback URL」仅在
  legacy opencode 驱动被显式启用时出现。方案虽写明「现有守卫（:994-997）不动」
  （行为不变），但散文描述漏掉第一条件，使 F-5 的场景与「跳过同步会让 sidecar
  停在旧端口」的论证适用面被放宽。附带事实：默认配置下若端口在失败冷启动中
  迁移（状态文件存了新端口）且显式启动成功时 URL 未变（§5.2 不触发
  syncDesktopConfig），sidecar 会停在旧端口直到下一次冷启动成功（:1035 路径）
  收敛——自愈、瞬态，与方案「接受暂时不可达」的取向一致，但方案未注明此边界。
- **影响**：设计决策（守卫不动、接受而非跳过）不受影响——无论场景宽窄，「接受」
  都是保守正确的选择。影响在于文档准确性：实施者按方案散文预期「默认配置下
  失败冷启动会写 sidecar」会测不到该行为；对 §5.1 论证的前提（旧端口收敛）也
  会误判其适用范围。§8 无依赖此段的断言（修订处置表已声明无需新增），无验收
  路径受损。
- **修订方向**：§5.1 该段守卫描述补全四条件；F-5 场景标注「仅 legacy opencode
  驱动启用时出现」；可加一句默认配置下 sidecar 收敛依赖下次冷启动成功路径
  （:1035）。
- **闭合标准**：§5.1 的守卫描述与 :993-1012 实际条件一致；F-5 场景注明适用面。

## 复审处置（r1 → v2，本轮逐项独立核验闭合）

| 原意见 | 严重性 | 处置声明 | 本轮闭合判定 | 闭合依据（本轮亲核） |
| --- | --- | --- | --- | --- |
| F-1 §3 决策函数与状态表第 6/8 行矛盾 | 阻塞 | 采纳 | **闭合** | v2 §3 函数补 `source != managedLocal → 沿用全局映射` 分支；本轮按 8 行状态表逐行推演对拍（第 1-8 行全部一致，含 external_http 未配置/disabled→「未配置」、external_http 服务没起→「未启动」）；镜像依据 WorkspaceView.swift:577 default→BackendStatusText.display 与 BridgeStatusView.swift:334-348 均亲核；§8 矩阵测试补行文本断言 |
| F-2 Task.detached 打破 MainActor 串行不变量 | 阻塞 | 采纳 | **闭合** | v2 §5「执行上下文与串行化」专节：内部串行执行域、公开签名不变、调用方线程画像不变（冷启动/shutdown 仍 MainActor 同步，阻塞量级同今天）、冷启动互斥（starting 时跳过 resolve）、shutdown 有界等待（timeout + stopOwnedProcess ≤2s，接受与既有 5 秒信号量的有界叠加）、.disabled 不发布就绪、AppKit 边界保留（syncDesktopConfig 仍 MainActor :1035，ensureRunning 无 AppKit :185-245）。事实底座逐项亲核（:131/:157-161/:160/:554-557/:395-408/:534-552/:428/:445-452）。设计无死锁路径（串行域不回调主线程）；§8 补串行化不变量测试、§9 条目 3 纳入 |
| F-3 dsh 冷启动裁决出处失真 | 建议 | 采纳 | **闭合** | v2 §7/OD-1 改写为「方案推导，非 owner 改令原文」；与 dsh 方案文档 :44/:84 原文逐字比对一致（本轮亲核） |
| F-4 §5.5 缺 source、收口刷新未写明 | 建议 | 采纳 | **闭合** | v2 §5.5 清单补 **source**（读 runtimeManager.config.opencodeSource）；§5.2「收口刷新」子项：动作返回后由按钮 Task 调 `backendViewModel.testAgent(id: "opencode-web")`——先例 WorkspaceView.swift:658-660 亲核，handleAgentTest 只读亲核（management_api.go:694-710）；3 秒轮询仅覆盖 codex-remote（:55-68）与 dsh 轮询收口（:231-248）的前提亲核；§8/§9 相应条目落位 |
| F-5 Desktop sidecar 交互未分析 | 建议 | 采纳 | **闭合**（附 F-7 精化） | v2 §5.1 明示选择「接受暂时不可达」并给出理由（持久 endpoint 即服务将被拉起的地址；saveState :228 先于 waitUntilReady :229，跳过同步会让 sidecar 停旧端口且 §5.2 URL 未变分支不补同步）——理由的事实依据本轮亲核成立。r1 闭合标准「明示处理选择」已满足；本轮新发现该段守卫描述不完整（F-7），属精化非推翻 |

**回归检查**：v2 修订未引入新的设计矛盾——§3 函数与 8 行表格、§5 串行化与
§9 条目 3、§5.2 收口刷新与 §8/§9 条目 5、§5.5 字段与 §3 决策输入的引用关系
全部对拍一致；全局门控关系（OD-1/OD-2/E-5/E-7）经本轮重查未变。F-6/F-7 均为
本轮新发现（前者是作者提交 v2/r1 后 §0 未随之更新的滞后，后者是 v1 即存在、
r1 与 v2 均未覆盖的守卫描述缺口），非 v2 修订引入的回归。

## 剩余门（与阻塞意见分列，不因方案通过而自动满足）

| 门 | 类型 | 状态 | 说明 |
| --- | --- | --- | --- |
| OD-1 冷启动自动拉起去留 | 决定门 | pending | A=保留（现状，方案按 A 设计，§5 条目 4/§7）；B=移除（OD 表已映射需改 §9 条目 2/3/5 语义与 §2.2 场景）。两选项各有独立验收。方案按 A 设计不构成绕过：A 即现状、无新语义，OD 表与 r1/r2 的 implementation_readiness 均显式登记分叉 |
| OD-2 安装版本 spec | 决定门 | pending | §2.1/§4/§8 全文以 `<OD-2 选定的版本 spec>` 参数化，installer argv 与测试断言无法在未裁决下落笔。推荐 A=`1.18` 的前提（clientFor 只认 generation118 fail-closed :396/:413、npm range 解析到最高 1.18.x）本轮亲核成立 |
| E-5 运行推断部分 | 证据门 | 待实施门复核 | 形状门机制（probe.go:144-209）已核；「1.18.34 与 1.18.18 同形状」为方案自标运行推断，实施期需以真实服务复核 |
| E-7 真实 npm 安装 + 真实 4096 启动 | 证据门 | pending | 实施后 owner 在工作站行点一次安装/启动；agent 核对本次独有日志，不用旧截图 |

**授权边界确认**：本轮仅评审与写本报告（任务指定的唯一写入文件），未修改方案/
业务代码/其他文件，未 commit，未部署，未运行构建或测试。实施仅在 owner 另行
启动后开始；OD-1/OD-2 裁决与 E-7 验收归 owner。

## 评审员交接块

```text
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-06-opencode-web-install-and-start-plan.md
plan_sha256: 6fd983ce86b7966003df3d77b2a25632420a1bc39b3e3b68900b5edbcd8fedd2
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-06-opencode-web-install-and-start-plan-review-r2.md
scope: full
blockers: 0
advisories: 2
open_gates: [OD-1, OD-2, E-5(运行推断部分,实施门复核), E-7]
round_complete: true
contract_version: plan-contract-v1.1
```
