# 方案评审报告 r1：设备—服务—Agent 拓扑与接入状态管理

日期：2026-10-15（Asia/Taipei）。评审员：plan-reviewer（延续模式，本轮为首轮，无历轮报告可复用，全部结论为本轮亲核）。评审契约：plan-contract-v1.1（plan-review skill + audit-plan 专项接入）。

## 结论

- **verdict: REVISION_REQUIRED（不通过，需修订后复审）**
- 方案：`docs/2026-10-15-agent-management-topology-and-state-machines.md`，v2
  评审时内容 SHA-256：`031de767bfc00bc94b5ae2763d4d4c5915c425c7196fe5af87c6bc8bdd5b0118`（与派发记录一致，本轮 `shasum -a 256` 复算）
- 本轮范围：full（简报字段 7：§2–§11 全量）
- blockers: **1**（F1）；advisories: **3**（F2–F4）
- 一句话：方案来源冻结扎实、锚点与样本核验几乎全部命中、需求/四拍/门控/切片/验收自洽；唯一阻塞是四项被写成"本轮已捕获"的命令语义观察没有任何归档样本，违反方案自身承诺的证据纪律（§2.3"CLI 安装／更新元数据实际来源见 §7 与伴随样本审计"），而其中 Claude 为闭源、样本是唯一可行证据路径。
- implementation_readiness：方案未通过前 S0–S7 均不可开工（仓库级阶段门：方案通过是唯一开发入口）。F1 闭合后，S0 冻结可直接开始；E3/E5/E6 维持为实施期/条件门，见"剩余门"。

## 来源与覆盖

### 来源身份（本轮亲核，2026-10-15）

| 别名 | 路径 | 分支 | 提交 | 未提交状态 | 与简报/方案一致性 |
| --- | --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `a97fee82877068e095bb16d67c2d364a86786728` | 干净 | 一致 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | 同分支 | `e479378f8c88338219ad734a7571e41ade356575` | 274 项，全部在 `docs/` 与 `.exec-plan/`（逐条核对无非 docs/.exec-plan 脏项） | 一致 |
| G | `/Users/jacklee/Projects/magpie` | `main` | `a36319d607d97e4754e025b82070a32d1d1a58ac` | 仅 7 个未跟踪 `.DS_Store` | 一致 |
| U-Codex | `/Users/jacklee/Projects/codex` | `main` | `cc7ba33601286a751477832b01de7b6f8de0dd9c` | 干净 | 一致 |
| U-Grok | `/Users/jacklee/Projects/grok-build` | `main` | `2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8` | 干净 | 一致 |
| U-DSH | `/Users/jacklee/Projects/deepseek-harness` | `master` | `d743267388641bc76f17c45ce8b4c231aed1d32c` | 干净 | 一致 |
| U-OpenCode | `/Users/jacklee/Projects/opencode` | `dev` | `0d61729b3fa005cec9aa754c075a809663c736a4` | 干净 | 一致 |

命令：各仓 `git branch --show-current`、`git rev-parse HEAD`、`git status --porcelain`；M 另跑 `git worktree list --porcelain` 确认本任务树与 `main` 树（`cordcode-macbridge` @ main/0508191a）分离。

**锚点漂移核对（简报指定项）**：`git diff --name-only f7dee495..a97fee828 | grep -cv '^docs/'` = **0**——v1 源码基线到当前 HEAD 只有 docs 变更，§2.3 产品源码锚点在 HEAD 仍有效。v2 冻结点 `2d983624` 到 HEAD 仅一个提交 `a97fee82`（8 个文件全在 docs/）。

### 本轮实际执行的核验

1. **P0 来源门**：上表全部身份重新生成，与简报字段 5 一致。
2. **§2.3/§2.5 锚点亲核**：M 仓 24 组、I 仓 7 组、上游 4 仓 9 组、G（Magpie）20 组，全部定位＋语义双核（见下表，摘录为代表样本）。
3. **样本核验（audit-plan 维度）**：`docs/assets/2026-10-15-agent-management/` 下 11 个 raw 样本逐一重算 SHA-256 并与索引对账（全部一致），逐类型验证形状断言（见专项节）。
4. **manifest 复核**：v1 `source-manifest.json` 记录的 20 个 fileHashes 逐文件重算——20/20 一致；v2 `magpie-reuse-source-manifest.json` 的 21 个 referenceFileSHA256——21/21 一致（脚本：python hashlib 逐文件比对，输出 ok=20 bad=0 / ok=21 bad=0）。
5. **设计面**：R1–R10 → 章节映射、交互四拍、OD/E 对账、S0–S7 依赖与验收、§11 三层验证。

未核范围与影响：`topology-concept.png` 未读（本会话模型无视觉；方案 §1.2 已声明"本文状态规则高于示意图"，图为布局示意，不承载正确性断言，不影响结论）。`source-manifest.json`/`magpie-reuse-source-manifest.json` 中的 I 仓 274 项脏文件逐条清单未逐项展开比对文件名（抽查 + 计数 + 非 docs/.exec-plan 过滤为空已覆盖关键命题）。

### 锚点核验表（代表条目；其余同法核验，未列者结论相同）

| 方案原引用 | 实际位置 | 源码原文摘录（带行号） | 定位/语义 | 依据 |
| --- | --- | --- | --- | --- |
| M `WorkspaceView.swift:14,73` | 同 | `:14 struct WorkspaceView: View {`；`:73 VStack(alignment: .leading, spacing: 24)` | 准确/支持 | 本轮亲核 |
| M `LayoutConstants.swift` workspaceFocusedContainerWidth=1016 | `LayoutConstants.swift:44` | `:44 static let workspaceFocusedContainerWidth: CGFloat = 1016` | 准确/支持 | 亲核 |
| M `RuntimeManager.swift:158,206,1644` | 同 | `:158 struct RuntimeConfig {`；`:206` 附近 `drivers: [String] = ["claude", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]`；`:1644 internal nonisolated static func processArguments(...)` | 准确/支持（Swift 默认恰为 5 产品，无 legacy） | 亲核 |
| M `AppDependencies.swift:126` 未传用户选择 | `:126` 附近 RuntimeConfig 构造 | 构造处无 drivers 字段传入 | 准确/支持 | 亲核 |
| M `go-bridge/main.go:202,232,274,286` | 同 | `:202 for _, id := range strings.Split(*drivers, ",")`；`:232 agent, err := core.CreateAgent(...)`；`:274 go startPassiveSubscription(...)`；`:286 if len(handlers.BackendList()) == 0 { ... os.Exit(1) }` | 准确/支持（空 BackendList 现状 fail-fast 属实） | 亲核 |
| M `agent/codex-remote/agent.go:76` 自动恢复 pairing | 同 | `:76 func New(...)` 内 `if !skipRestore { go a.restorePersistedPairing() }` | 准确/支持（构造即后台恢复） | 亲核 |
| M `management_api.go:462` ready_no_agents | 同 | `:464 status = "ready_no_agents"`（:462 起 handleStatus） | 准确/支持 | 亲核 |
| M `RuntimeManager.swift:412,431,463,677` | :412/:431/:677 准确；**:463 偏移** | `:412 func restart()`；`:431 guard gen == self.launchGeneration`；`:677 private func prepareCooperativeRestart(`；**applyConfigAndRestart 实际在 :454，:463 是 shutdownForExit** | :463 定位不覆盖所述符号；quiesce/commit/healthEpoch/token 机制在 :718-768 亲核存在，语义成立 | 亲核（见 F2） |
| M `device_conn_registry.go:14,35,67` ＋ "`:381` 清掉死连接" | :14 `type DeviceConnRegistry struct`；:35-36 Register 持锁 append；:67 起持锁复制；**:381 属 relay_bridge_client.go** | `relay_bridge_client.go:380-385 // pruneDeadDevice 清理一个被判死的 device 连接…` | 三锚点准确；`:381` 未写文件名，按上下文可解析为 relay_bridge_client.go，语义成立（见 F3） | 亲核 |
| M `server.go:626` / `relay_bridge_client.go:551` | 同 | `:626 globalDeviceConnRegistry.Register(authedDevice.DeviceID, directConnection)`；`:551 globalDeviceConnRegistry.Register(deviceID, rc)` | 准确/支持（direct 与 Relay 双注册点属实） | 亲核 |
| M `hello_handler.go:44,168`；`hello_handler_test.go:126` | 同 | `:44 Backends []AgentProviderDescriptor \`json:"backends,omitempty"\``；`:168 backends := BuildAllAgentDescriptors(...)`；test `:136 if ack.Backends == nil { t.Error("Backends 不应为 nil（应为空列表）") }` | 准确/支持——现状确实只测内存非 nil；且 omitempty 下非 nil 空 slice 仍会被 omit，"补实际 JSON 序列化测试"的缺口判断精确正确 | 亲核 |
| I `BridgeProvider.swift:948,1204` | 同 | `:948 guard let backends = ack.backends, !backends.isEmpty else { throw CCCodeBridgeError(code: "no_backends"...) }`；`:1204 guard ... !backends.isEmpty else { ... return }`（transport 重连） | 准确/支持（初连与重连两处拒绝点属实，§5.4"必须同步修复"必要） | 亲核 |
| I `OpenCodeiOSApp.swift:566` | 同 | `:566 private func syncBridgeTargets(...)`；body `guard !recognizedBackends.isEmpty else { NSLog("…无法创建 server"); return }` | 准确/支持（recognized 空集直接 return 属实） | 亲核 |
| I web `AppController.ts:1079,1094`；`backend-selection.ts:14,19` | 同 | `:1079 const backends = (ack.backends ?? []) as DiscoveredBackend[]`；`:1094 const selected = selectAvailableBackend(...)`；`:14 export function isBackendAvailable`（仅排除 unavailable/disabled）；`:19 export function selectAvailableBackend`（空集在 :31 `throw new Error("MacBridge 未返回任何后端")`） | 准确/支持 | 亲核 |
| U-Codex `desired_state.rs:12` / `remote_control_processor.rs:63` | 同 | `:12 pub(super) enum RemoteControlDesiredState`（注释：`Durable disable writes 0 before entering Disabled`）；`:63 pub(crate) async fn disable(` | 准确/支持（官方 disable 写持久配置属实；"CordCode 接入关闭不调用官方 disable"的边界成立） | 亲核 |
| U-Grok `auto_update.rs:167,228` / `cli.rs:89` | 同 | `:167 pub struct UpdateStatus`（serde camelCase：current_version/latest_version:Option/update_available/installer:Option/channel）；`:228 pub async fn check_update_status(...)`；cli.rs `Update { --check: Check for updates without installing }` | 准确/支持（样本 7 字段与结构体一一对应） | 亲核 |
| U-OpenCode `upgrade.ts:18` / `test/upgrade.test.ts:19` | `packages/cli/test/upgrade.test.ts`（路径按 packages/cli 相对成立） | `:18 const method = Option.getOrUndefined(input.method) ?? (yield* updater.method())`；test `:18 detects the installation method and resolves the latest version` | 准确/支持 | 亲核 |
| U-DSH `bin.ts:27` / `args.ts:146` / `args.spec.ts:92` | 同 | `:27 export async function runCli(...)`；`:146 export function parseDshArgs(...)`；spec `:92 it.each(['-V','--version'])` | 准确/支持 | 亲核 |
| G `cliupdate.go:199/258/303/409/438/471/524/549/895` 等 20 组 | 全部行号命中 | 如 `:199 func howInstalled(spec cliSpec, bin string) *updater`；`:471 memo.get` 注释 `a failure for fail (the answer before kept meanwhile)`；`:409 func Newer`（宽松比较）；`:895 func (a *Agent) UpdateCLI()`；app.js/routing.js/version.go/probe.go/install.go/applied.go/agent.go/desktopapp.go/shim_test 各行号逐一命中；`LICENSE` MIT `Copyright (c) 2026 yetone` | 全部准确/支持（复用调查的"不采纳项"事实核——memo 失败保留旧字符串、Newer 宽松、UpdateCLI 自动执行、npmInstall 拼 Node 安装——均与源码一致） | 亲核 |

另核（方案设计依赖的现状事实，均成立）：`main.go:35 defaultDrivers = "claude,codex,codex-web,grokbuild,dsh-web,opencode-web"`（六驱动含 legacy，§2.4 枚举线属实）；`RuntimeManager.swift:970` 现状把 ready_no_agents 翻译成"请配置至少一个 AI 工具"（§5.4 改造目标真实存在）；`prepareCooperativeRestart` 旧分支 `:700-701 if trigger == .automatic { throw ... } try await client.shutdown()`（§5.2 所述"非automatic＋无V1→直接shutdown"属实）；`resolveManagedOpenCodeIfNeeded` 在 `RuntimeManager.swift:1042`（§5.3 门目标存在）；最小窗口 1280×840 于 `LayoutConstants.swift:12,15`（§3.1"沿用"属实）；`-opencode-installed-cli`、`-agent-selection-revision` 全仓 grep 无匹配（确为拟新增）；Grok `0920c8169e27` 在 U-Grok `git cat-file -t` 报 `Not a valid object name`（§2.2"无可解析提交"属实）；`docs/protocol/bridge-v1-schema.md:93` `backends: [BridgeV1BackendInfo]?` 为 optional（§5.4 protocol pack 同步需求成立）。

## 样本与 audit-plan 专项

三类外部内容形状逐一按索引重读 raw 并验证（本轮 `shasum -a 256` + python json 解析）：

| 内容类型 | 声明 | raw 复核结果 | 评级 |
| --- | --- | --- | --- |
| npm latest 文档 ×3 | 根对象、顶层 `version` 非空 string | claude `2.1.299`/dsh `0.2.0-rc.2`/opencode `1.19.0`，root=dict，version=str，SHA 与索引一致 | 🟢 |
| npm 404 负例 | 错误体不是成功版本文档 | HTTP404，JSON 根是字符串 `"Not Found"`（rootType=str），无 version 可误取 | 🟢 |
| Grok update-check JSON | currentVersion/latestVersion/updateAvailable/installer/channel/error 等显式字段 | 平面对象 7 字段全在，current==latest、updateAvailable=false、error=null、stderr 空文件（SHA e3b0c4…）；与 U-Grok `UpdateStatus` camelCase 结构体逐字段对应 | 🟢 |
| 本地 package.json ×3 | 根 name/version 非空 string、精确 name 白名单 | claude `2.1.296`/opencode `1.19.0`/dsh `0.2.0-rc.2`，SHA 与索引一致；与 npm latest 对照可见 claude 存在 2.1.296→2.1.299 真实更新场景 | 🟢 |

索引完备性（捕获时间/URL 或命令/SHA/HTTP 状态或退出码）：`version-sample-index.json` 与 `magpie-reuse-package-sample-index.json` 均齐备。**未通过项**：四项命令语义观察无任何归档（见 F1）——这是本轮专项在"doc describes it, no sample attached"检查框上的唯一命中。脚本交叉验证：npm/Grok/package.json 均为单层根字段读取（audit-plan 允许单脚本），flat 对象逐键类型核对与索引一致，无嵌套/多态/跨记录声明需要双策略。

## 门控一致性

- **OD2（简报指定必审）——显式审查结论：推荐语义成立，本评审认可 opt-in 空集迁移。**理由：(a) 任务原始问题即"启动 App 后默认注册全部驱动"——把旧隐式五驱动迁移为全开等于保留被否定的现状，直接与 R8 目的冲突；(b) 全关迁移的一次性代价已由 §5.4 的跨仓客户端 noAgents 状态、§5.4 交付顺序（先客户端后 Mac 默认）与升级提示覆盖，且 §11.1 首行将"旧无偏好升级→初始空"列为定向验收；(c) 互斥替代（迁移全开）已在 OD2/§5.1 声明为需重写 §5.1 与验收的整体替换，无并存形态。方案对"这是拟议语义而非用户逐字裁决"的披露诚实（§5.1 第二段）。
- OD1（用户已决定项）与 §3/§6 呈现一致；OD3 与 §5.2 的 admission/generation/quiesce 机制、§5.4 全设备重连代价声明一致；OD4 与 §7.2/§7.3 及 E5 边界一致（无任何自动执行路径残留，Magpie 的 UpdateCLI/npmInstall 已明确列入不采纳）；OD5 与 §2.3 双端反证锚点、S3 及 §5.4 客户端三态 advertisement 一致。
- E1 本轮复审通过（来源身份全对账）；E2 复审通过（§2.3 锚点 + f7dee495..HEAD 零产品代码漂移 + manifest 哈希 41/41）；E3/E6 为实施后完成门，登记形态正确（"实施后…不声称已有API"）；E4 通过（上表）；E5 为合规 pending 条件门：未支持发行源、自动更新、Grok 不同 generation 均被挡，且不阻塞本版准确指引——符合共享约定待捕项四条件。
- R→S→验收链完整：R1–R10 全部映射到 §3–§7 的具体契约并挂到 §11.1/§11.3 的可观察验收；交互四拍（M 仓 AGENTS.md 硬要求）在 §6.1（7 操作）、§7.2（更新 pill 四拍含失败态）、§7.3（安装四拍）完整成故事，无"先接通 RPC 展示另做"的腰斩形态。
- 切片 S0–S7 依赖有序（S0 冻结→S1 选择/inventory→S2 应用闭环→S3 跨仓空集→S4 设备快照→S5 版本交互→S6 UI→S7 交付），每片有失败处理；§11.2 的构建/运行态身份门（agent_selection_applied 特征日志、新 PID 晚于构建、8777 归属正式 bundle）与 M 仓 P0 部署后运行态验证规则逐条对齐；§11.3 U1–U10 符合 agent-device 永久停用后的人工矩阵形态。

## 意见

### F1［阻塞］四项"本轮已捕获"的命令语义观察无归档样本

- **位置**：§2.2（ChatGPT Info.plist 版本 `26.1007.30912`/build `20156`）；§2.3 末段（`claude install --help` 输出 `install [options] [target]`、target 可为 stable/latest/具体版本；OpenCode 1.19.0 `upgrade --help` 证明 `[target]`、`--method`、`--major`）；§7.1 OpenCode 行（"help 已捕获"）；伴随审计（grok `update --help` 的 --check 不安装语义）。
- **证据**：`grep -rn "install \[options\]|stable/latest|--major|26.1007|upgrade \[target\]"` 于三份 2026-10-15 文档与 assets/*.json——命中仅在方案/审计正文散文中，`docs/assets/2026-10-15-agent-management/` 18 个文件（ls 全列）无任何 help 输出或 plist 探针归档，两个索引（version-sample-index、magpie-reuse-package-sample-index）均无对应条目。
- **影响**：这些声明直接支撑用户可见行为——§7.3 未安装行的 Claude 官方 native 安装命令、§7.1 OpenCode"其他官方方式"的更新入口与"本方案不传 `--major`"决策、§7.1 Codex Desktop 行的本地 bundle 版本读取。Claude 为闭源（本仓 source-first 门明确"无开源源码"，样本是唯一证据路径）；若 help 实际形状与散文描述不符，S5 冻结的命令 catalog 会把错误命令复制给用户。同时违反方案自身纪律（§2.3"CLI 安装／更新元数据实际来源见 §7 与伴随样本审计"——而伴随审计不含这四项）与 plan-contract"原始运行证据须可复核"及 audit-plan"每个内容形状声明须有真实样本"规则。
- **修订方向（最小充分）**：补采并归档四个 raw 输出（捕获时间、完整命令、退出码、SHA、索引条目；plist 读取记录 bundle 路径与读取方式），或把对应句子降级为"假设待取证"并挂 E5 式条件门（未取证前对应命令入口只给官方文档链接）。归档属运行安全只读取证，在修订轮授权内。
- **闭合标准**：样本目录出现四项归档且索引可对账，或方案文本改写为待验证假设并相应收紧 §7.1/§7.3 的命令承诺。

### F2［建议］§2.3 RuntimeManager 锚点 :463 行号漂移

- **位置**：§2.3 安全重启行 `Services/RuntimeManager.swift:412,431,463,677`。
- **证据**：`applyConfigAndRestart` 实际在 `:454`（`grep -n "func applyConfigAndRestart"`），`:463` 是 `func shutdownForExit()`；quiesce/commit/healthEpoch/token 在 `:718-768` 亲核存在。
- **影响**：行号不可定位到所述符号；语义无恙（机制齐备），仅影响下一轮/实施期按行号复核的效率。
- **修订方向**：`:463` 改为 `:454`，或补 `:718`（quiesce/commit 主体）。
- **闭合标准**：修订版行号与符号对上。

### F3［建议］§2.3 设备连接行 `:381` 未写文件名

- **位置**：§2.3 "当前在线连接"行末尾"：`381` 清掉死连接"。
- **证据**：`device_conn_registry.go` 全文件仅 132 行（`wc -l`）；死连接清理实际在 `relay_bridge_client.go:380-385`（`pruneDeadDevice` 注释＋函数）。
- **影响**：读者按前半句文件名归属会得到"行号越界"的误读；语义本身成立。
- **修订方向**：补写 `relay_bridge_client.go:381`。
- **闭合标准**：锚点文件名完整。

### F4［建议］安装命令 catalog 的官方来源锚点应在 S0 显式冻结

- **位置**：§7.3（"Grok 官方安装脚本、Claude 官方 native/已支持 npm…"）与 §10 S0（冻结清单未列安装命令 catalog 的来源）。
- **证据**：Grok 官方安装命令在 U-Grok `README.md:46`（`curl -fsSL https://x.ai/cli/install.sh | bash`）有可锚定来源，方案未引用；Claude native 依赖 F1 的归档；npm 三个 package 名已有 package.json 样本支撑（已核）。
- **影响**：S0 冻结若不指定命令来源，实施期可能凭印象写命令文本，重演"示意版本当真值"风险。
- **修订方向**：S0 冻结清单加一行"安装/更新命令 catalog 逐条登记官方来源锚点（上游 README/help 归档/npm 样本）"。
- **闭合标准**：S0 行含命令来源登记项；Grok 命令锚到 U-Grok README。

## 复审处置与剩余门

- 本轮为首轮，无历史意见闭合表。F1–F4 为本轮新发现（非修订引入）。
- **open_gates（与 blockers 分列，不因方案通过自动消解）**：
  - **E5**（pending 条件门）：未支持发行源的新 latest provider、自动更新、Grok 不同输出 generation——待目标版本真实样本＋官方锚点；不阻塞本版。
  - **E3**（实施完成门）：snapshot/rescan codec 与空 ACK JSON producer/consumer 测试——S3/S4 完成前置。
  - **E6**（实施完成门）：disabled 零后台、空集端到端、撤销、direct+Relay 观测的定向单测＋部署后真实日志/进程＋owner 矩阵。
  - F1 闭合本身不触发新门；修订轮按仓库纪律只改方案与证据文档，不动产品代码。
- 评审通过 ≠ 实施授权；S0 开工前须按 E1 fresh 重新生成来源清单。

## 评审员交接块

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-15-agent-management-topology-and-state-machines.md
plan_sha256: 031de767bfc00bc94b5ae2763d4d4c5915c425c7196fe5af87c6bc8bdd5b0118
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-15-agent-management-topology-and-state-machines-review-r1.md
scope: full
blockers: 1
advisories: 3
open_gates: [E5, E3, E6]
round_complete: true
contract_version: plan-contract-v1.1
```
