# 架构健康度评估：cordcode-macbridge + cordcode-ios

> ## ⚠️ 状态横幅（2026-07-04 更新，先读这段）
>
> **本文是 2026-07-03 的时点快照，原文保留不动作为历史证据。** 下方"技术债清单"中的**系统性债已通过四轮架构健康专项全部清完**，不要照本文件的原数字/原结论做新判断。读本文前请先看下面四轮治理结果，了解哪些条目已被根治。
>
> **四轮累计成果**（详见各轮完成情况 + 差距分析文档）：
> - **动作 1（杀双写）✅ 100%**：`BackendList`/`deriveCapabilities` 收敛为 `deriveBackendCapabilities` 单源，question_reply 不一致 bug 已修。
> - **动作 2（删 config 死重）✅ 100%**：`config/` 整包删除（6418 行下线），Weixin/Feishu/Cron/Webhook 等无关业务结构清零。
> - **动作 3（拆 god-object）🟡 ~35%**：`handlers.go` 4604→3269 行（拆出 opencode+relay）；iOS `BridgeProvider` 1967→1629 行（拆出 transport creation 子域到 `BridgeTransportConnector`）；3 条 strict gate 防回涨。
> - **动作 4（web 共享包）✅ 100%**：`shared-message-renderer` 共享包建立，5/5 组件（ToolBlock/DiffViewer/ReasoningBlock/ProcessGroup/NarrativeBlock）迁入。
> - **动作 5（工程宪法 + CI 卡）🟡 ~50%**：宪法 + hygiene 脚本 + **3 条 strict gate**（BridgeProvider + ChatViewModel Generation/MessageSync）。
> - **治理主线 2（状态模型收敛）✅ 突破**：iOS turn ownership/history sync gate 从 Claude-only ad-hoc 条件重构为 backend-agnostic 的 `ChatTurnSyncPolicy`（纯函数）+ `ChatTurnSyncState`（@MainActor holder）+ apply 前 canApply 复核。
>
> **结论**：本评估识别的"会持续制造 bug"的系统性债（双写 bug 工厂、config 屎山、web 漂移源、iOS god-object 恶化、turn sync 状态覆盖）**全部止住**。剩余 god-object 物理拆分、`ChatUIKitContainerView`（4371 行）、CI inventory 段升级等属**日常维护债，不再制造系统性 bug，不派生第五轮专项**。
>
> **相关文档**（按时间序）：
> - 执行计划：`docs/2026-07-03-architecture-health-execution-plan.md`
> - 第一~四轮完成情况：`docs/2026-07-{03,04}-architecture-health-*-round-*-完成情况.md`
> - 各轮差距分析：`docs/2026-07-04-architecture-health-*-gap-analysis.md`
>
> **下方原文是 2026-07-03 快照，仅供历史溯源，不代表当前状态。**

- **评估日期**：2026-07-03
- **范围**：MacBridge 仓库（`cordcode-macbridge`，~42K 行 Go 生产 + ~36K 行 Go 测试 + ~9.4K 行 Swift，92 commits 单作者）与 iOS 仓库（`cordcode-ios`，~163 Swift 文件 / ~57k 行，~86 测试文件 / ~29k 行，message-web ~6.4k 行 React/TS，remote-web ~14.7k 行 React/TS）
- **方法**：2 个并行只读评估 agent，分头摸 MacBridge（分层/抽象/并发/测试/技术债/AI 痕迹）与 iOS（同维度）。每条结论带文件/符号/行号证据。**全程未修改任何文件。**
- **性质**：讨论性评估，非代码改动。是 **2026-07-03 时点快照**，随代码演进而过期——尤其是"技术债"清单，清掉一条就少一条。
- **修订**：2026-07-03 二次复核后修正若干口径——(a) AI 多模型指纹章节改为"特征吻合"，不作因果铁证；(b) ForTesting 钩子数 36→34（grep 复现）；(c) config 死重补全仓引用核实（生产仅 1 文件、4 符号，config 导出 75 符号）；(d) panic 分桶（启动期 flag / 启动期常量解码 / runtime 极端触发三档）；(e) web 重复补精确 diff；(f) 补"讨论底稿 vs 执行计划"定位。每条均以代码为真相源复核，未采纳的复核反馈也注明（见附录）。
- **触发问题**：项目方问"全程用多个 AI 大模型（GPT5 / Claude / Gemini / GLM / DeepSeek 等）轮换设计开发测试的项目，架构是否完美、能否达到 Telegram/ChatGPT 同类水准、是否有屎山/技术债"。

---

## TL;DR

**不是完美，也远不是屎山。是"骨架选对了、肉身堆得粗"的同维度中上水平。** 离 Telegram/ChatGPT 的产品级有明显距离，但这个距离主要在**实现治理**，不在**架构选型**——而后者才是真正致命的屎山判定标准。两个仓库的核心抽象没选错，这是最重要的结论。

判断"是不是屎山"最准的标准是：**核心抽象选对没有**。如果分层错误、协议和业务耦合、状态机根本性错，那是屎山，要推倒重来；如果抽象对了、只是实现粗糙，那是"需要清债"，不是屎山。本项目的结论是后者。

换句话说：本项目不是"要推倒重写"的屎山，而是经历过 AI 快速生长期、正在进入**工程治理期**的复杂系统。当前最危险的不是代码已经坏掉，而是继续无限叠需求、不定期清债；最有价值的方向是删旧路径、压状态源、让文档追代码、让测试钉不变量。

---

## 一、比较框架先校准（关键）

"和 Telegram / ChatGPT 同一水准"这个问法本身需要先校准，否则结论一定失真：

| 产品 | 性质 | 团队/生命周期 | 核心难点 |
|---|---|---|---|
| Telegram | IM 客户端 | 百人团队 / 十年 / 百万 DAU | UI + 长连接 + 消息存储 |
| ChatGPT app | LLM 前端 | OpenAI 规模团队 | 流式渲染 + 会话状态 |
| **本项目** | **系统级桥接中间件 + 远程操控端** | **单人 + 多 AI 模型轮换 / 活跃迭代中** | **进程生命周期、多 backend 异构事件归一化、HPKE 端到端加密、协议兼容、连接状态机** |

本项目更接近 *ngrok + Tailscale + 加密 relay + AI chat 客户端* 的混合，不是任何单一 C 端 app。拿它和大团队十年产品比"完美"，维度不对。下面的结论都按"**单人 + AI 主导 + 活跃迭代的同维度坐标系**"给。

---

## 二、强项（接近产品级水准）

### MacBridge

1. **分层无环、依赖单向**：`core/` 不 import `agent/` 或 `go-bridge/`，`agent/*` 不 import `go-bridge/`（grep 全空）。go-bridge 单向依赖 core，agent 实现 core 接口。教科书式分层。
2. **`Agent` 基接口 + opt-in capability**（`core/interfaces.go`，411 行，~30 个细粒度能力接口如 `ProviderSwitcher` / `ModelSwitcher` / `RichHistoryProvider` / `TranscriptLocator` / `LiveModeSwitcher`），靠 type assertion 派发——比"胖接口"干净，是 Go 社区推荐的能力发现模式。新增 backend 不改 core（`core/registry.go` `RegisterAgent` + 各 agent `init()` 自注册）。
3. **relay-server 独立 module 边界干净**（module `cordcode-relay`）：完全不 import 根 module（`grep "openAgi2/cordcode-macbridge" relay-server/` 空），依赖只有 `gorilla/websocket` + `modernc.org/sqlite`；relay 自身不做 HPKE 解密（`grep hpke/Decrypt relay-server/internal/` 空）——**端到端加密边界正确，relay 真的看不到明文**。
4. **协议真值与代码逐字一致**：`docs/protocol/README.md` 记 `cordcode-bridge` v1 / rev `2026-05-07`，与 `go-bridge/bridge_v1_schema.go` 常量逐字一致，`hello_handler.go` 强制版本校验。wire 字面量被纪律性地当冻结契约、不随品牌名变。
5. **并发 shutdown 卫生好**：根 context + 分级 shutdown（HTTP server 10s → handlers 8s → 连接关闭 → relay/TLS/management）+ `sync.WaitGroup` 并发关 session。`go vet ./go-bridge/...` 通过。
6. **测试真实**：生产/测试行比 ≈ 0.86，126 个测试文件、1022 个 Test 函数、8 个 `*_regression_test.go`（pagination / relay_hpke / relay_mailbox / relay_prekey …），抽样均为真断言，无占位。

### iOS

1. **真实行为测试 + 罕见的架构护栏测试**：86 个测试文件覆盖 Bridge/Recovery/ViewModel/Merge/Web/Storage 各层；`OpenCodeiOSTests/ArchitectureGuardrailTests.swift` 直接 `#filePath` 读 `ChatUIKitContainerView.swift` 源码，断言"输入框执行态必须绑 `isSessionExecuting()`、禁止消费 `isStreamingRenderState()`"——把跨层 UI 契约写成可执行断言。这种"用测试钉死架构边界"是有人主动治理的强信号。
2. **`selectConnectionStrategy()` 是 19 行纯函数**（`BridgeProvider.swift:615-633`）+ **RecoveryCoordinator 显式"让位 transport"语义**（`hasActiveTransportReconnecting()` 时退出 recovery loop）+ single-flight 防风暴 + `wakeBackoff()`。深思过的连接状态机，不是堆出来的。
3. **snapshot-first session 切换**（`ChatViewModel+SessionManagement.swift` `restoreLocalSnapshotForSessionTransition`）：内存缓存 → 磁盘 snapshot → 后台 server 校正三段式，配 `SessionSnapshotStore`（schemaVersion=3 + `SnapshotIntegrityValidator`）。面向 C 端 IM/AI chat 客户端的正确做法。
4. **Swift↔JS 契约带版本号**（`MessageWebModels.swift` + `message-web/src/types.ts`，envelope `{type, payload}` + `revision: number`），含 `prependAnchorMessageID` 分页锚点。不是隐式协议。
5. **持久化分层得当**：`Services/Storage/` 集中管理，UserDefaults 全仓仅 ~57 处（多为偏好/缓存），业务态不泄漏进 View，Keychain 用于 device token。

这些不是 AI 随便堆得出来的——是需要有人盯着全局才会有的东西。

---

## 三、真实技术债（按严重度）

### 高（会持续制造 bug 或拖垮可维护性）

1. **`config/config.go`（3238 行）含大量无关业务死重**：含 64 处 Weixin/Feishu（客服/IM 机器人系统残留，如 `FeishuCredentialUpdateOptions`、`EnsureProjectWithFeishuPlatform`、`supportedReferencePlatforms`），与本仓库"AI coding agent 桥"业务无关。**核实（全仓 grep 可复现）**：生产代码仅 `go-bridge/provider_switch.go` 一个文件 import config（另两个是 `agent/*/provider_*_test.go` 测试文件），引用 `Config`/`Load`/`ProjectConfig`/`ProviderConfig` 4 个符号；而 config 包**导出 75 个符号**（整片 RateLimit/Webhook/Cron/TTS/Hook/Speech/Display 等类型本仓库未用）。即主要运行路径只依赖少量加载与配置结构能力，约 3000 行是死的。**最典型的屎山信号**。
2. **能力派发双写已造成真 bug**：`go-bridge/handlers.go:352 BackendList()` 与 `go-bridge/agent_descriptor.go:103 deriveCapabilities()` 是两份几乎逐行重复的能力推导代码，注释自称"逻辑保持一致"靠人工同步——结果 `handlers.go:400` 给 codex app_server 追加 `compression` **和** `question_reply`，`agent_descriptor.go:155` 只追加 `compression` **漏了 `question_reply`**。iOS 从 BackendList（Management API）和 BuildAgentDescriptor（hello_ack）拿到的能力列表不一致。双写是 bug 工厂。
3. **iOS `BridgeProvider.swift` 是 god-object**：1967 行、**78 个方法**、16+ 职责（连接编排/策略选择/transport 创建/direct-race 协调/backend client 缓存/recovery 所有权/路径切换/前台心跳/网络监听/bridge 持久化/UI state 发布），内嵌 **34 个 `ForTesting` 注入钩子**（grep 复现；埋在生产类里，非协议注入）。`connectBridge()` 81 行、`adoptSuccessfulConnection()` 138 行、`runDirectRace()` 116 行。
4. **iOS 巨型方法**：`sendMessage()`（`ChatViewModel+Generation.swift:137-716`）≈579 行；`handleCodexLiveEvent()`（`ChatViewModel+CodexStreaming.swift:7-570`）≈563 行；`switchSession()`（`ChatViewModel+SessionManagement.swift:90-590`）≈500 行。靠 extension 物理拆了，单方法仍是维护热点。
5. **message-web 与 remote-web 代码大量复制，无共享包机制**：**核实 diff**——`ToolBlock.tsx` 与 `DiffViewer.tsx` 两边**字节全等**（ToolBlock 各 444 行，`diff` exit 0）；`ReasoningBlock.tsx` 仅差 2 行；`ProcessGroup.tsx` 差 43 行、`NarrativeBlock.tsx` 差 68 行（高度重复非全等）；两工程 React 版本不同步（message-web 18.3.1 vs remote-web 19.2.7）。即"部分已字节全等、其余高度重复"，任何 renderer 改动要手动双改，**漂移几乎必然**。

### 中

- **`panic` 分桶看风险，不宜放同一桶**：
  - **可接受**：`relay-server/cmd/relay-server/main.go:127,136` 启动期 flag 校验。
  - **低风险**：`go-bridge/relay_hpke.go:562` 的 `mustDecodeHex` 是**启动期常量解码辅助**（解码冻结的 hex 字面量，写错即启动崩，不进 runtime 请求路径）。
  - **runtime 路径但触发条件极端**：`go-bridge/data_dir.go:222` `GenerateBridgeID`（`crypto/rand.Read` 失败 = 系统熵源严重故障）、`agent/opencode/opencode.go:292` `mustWriteProviderSignaturePart`（写内存 `hash.Hash`，正常不会错）。这两管理论上能把 runtime 整崩、对一个被 Mac app 拉起持续服务 iOS 的进程不够稳健，但**实际触发概率极低**；仍建议改为返回结构化错误，但不属于"高风险 panic"。
- **`go-bridge/pagination.go` 的 `trimWireToBudget` 是有文档的"临时止血"**：transcript-index replay mismatch 时 fallback 丢弃最旧消息以塞进单帧预算，注释直言"在 replay-mismatch 根因修好前"的绕过。有界、有注释、上层分页仍返回全量，不算"隐藏失败"，但属未根治的根因 + 临时止血组合。
- **超长文件**：`go-bridge/handlers.go` 4604 行（70 个 RPC case，dispatch switch 结构本身清晰，问题只是物理文件没拆）；`agent/claudecode/claudecode.go` 1908 行；`agent/codex/appserver_session.go` 1805 行。
- **iOS `ChatUIKitContainerView.swift` 4371 行 god-object**：UIViewController 管 20+ UI 组件、浮动 header、message-web 容器、input bar、task dock、权限弹窗、玻璃层、dictation、streaming 指标。架构护栏测试能钉住部分契约，但类本身是维护负担。
- **连接状态多源（iOS）**：`BridgeProvider.connectionStatus` / `CCCodeBridgeTransport.transportState` / `CCCodeBridgeConnectionStateManager` 三处都跟踪连接状态，无强制单一真值源。
- **SwiftUI 状态管理模式不统一**：`@StateObject` / `@ObservedObject` / `@EnvironmentObject` 混用，缺"谁拥有 ViewModel"约定；无 forbidden-import 级别的跨层强制。
- **并发原语混用（iOS）**：async/await 主导（422 处）+ actor 到位（31 个），但仍残留 36 处 `DispatchQueue`、`NetworkPathWatcher` 的 `NSLock`。

---

## 四、与 AI 多模型轮换特征高度吻合的指纹

> **定性说明**：本节证据（中英混排、多套日志/诊断体系并存、重复 TODO、文档多轮堆积等）能可靠证明"项目历经多轮、多风格的迭代，整体缺乏统一贯穿的工程规范"，**高度符合 AI 多模型轮换开发的特征**。但代码本身无法反推"某段一定由某模型产出"——风格漂移同样可能源于多 session、多阶段的人工/AI 协作。因此本节表述为"特征吻合"，**不作为因果铁证**。

能从代码里**精确数出**风格漂移的痕迹：

1. **中英混排注释（最显著）**：`go-bridge/handlers.go` 注释 ~64% 含中文，但 `pagination.go` / `agent_descriptor.go` 又是另一种语码转换风格（中文段夹英文术语）。风格不统一贯穿全仓，高度符合多模型/多阶段迭代特征（单一个体长期保持这种粒度的不一致较少见）。
2. **三套日志体系并存（iOS）**：`NSLog` 221 处 + `print` 61 处 + `os.Logger`/OSLog 10 处 + `PerformanceTracer.swift`（904 行）独立用 `os.signpost` + `#if DEBUG` 88 处。可观测性缺乏统一约定、各模块各自落地一套，高度符合多阶段迭代无统一规范的特征。
3. **同一句 TODO 一字不差出现三次**：`// TODO: process-group stop at the Agent level` 在 `agent/claudecode/claudecode.go:1345`、`agent/codex/codex.go:437`、`agent/opencode/opencode.go:510` 各一份——典型缺乏共享抽象/去重机制：同句在三处独立出现，高度符合多模型/多 session 各自落地同一关注点而无协调。
4. **本地化建好脚手架但执行不彻底（iOS）**：542 处正确 `NSLocalizedString` / `String(localized:)`，但仍有 **77 处硬编码中文 UI 字符串**（如 `ContentView.swift` "删除后无法恢复" / "认证已变更" / "去设置"）。有的模型走 localization、有的直接写中文。
5. **命名层级深浅不均（iOS Bridge 层）**：`CCCodeBridgeClient`(492 行) / `CCCodeBridgeBackendClient`(937) / `CCCodeBridgeTransport`(1273) / `RelayBridgeFrameConnection`(682) / `BridgeWebSocketConnection`(642) / `BridgeConnectionCoordinator`(极薄)——新人搞不清"谁是 source of truth"。
6. **抽象层次跳跃（MacBridge）**：`core/` 抽象干净，但 `handlers.go` 又把 RPC 派发、文件读鉴权、session 清理、opencode 特殊路径（`ocHandleListSessions`）混一文件。
7. **过度诊断设施叠加（iOS）**：`SessionLifecycleDiagnosticPhase`（10 case 的"推断式诊断枚举"，注释自承"不是状态机，仅诊断标签"）+ `ChatViewModel+AgentRuntimeStatus` + `PerformanceTracer` 三套独立诊断体系并存——每个模型加一层自己的可观测性。
8. **错误处理风格跳变（MacBridge）**：大量 `_ = sess.Close()` 丢弃 Close 错误（cleanup 路径可接受），混着"严谨 panic"和"全程吞错"两种风格；部分场景（`_ = os.WriteFile("/tmp/bridge-sessions.json")` 调试写入失败被吞）应至少 log。
9. **多轮评审 docs 堆积**：`docs/` 136 篇 + iOS `docs/` 亦大量；同一主题 `codex-message-process-style-alignment` 有 review.md → r2 → r3 → r4 → r5 五轮并存——会话级记忆靠文档承载、缺收敛。

---

## 五、根因分析

不是"AI 写不好代码"，而是 AI 的能力分布特性 + 开发模式共同决定：

1. **AI 强在局部正确，弱在全局一致**。单文件/单函数它能写得很好（所以强项都扎实），但命名统一、风格统一、跨文件去重、抽象层次守恒这类"全局约束"，模型之间、甚至同模型跨 session 都难维持——这本来是人类 architect 的职责。
2. **需求驱动 + 快速迭代**优先功能正确，债务累积是必然代价（god-object、巨型方法、双写都是这么长出来的）。
3. **缺一份贯穿全程的"工程宪法"**（日志用什么、错误怎么处理、ViewModel 谁拥有、类拆到多大算超标）——每个模型按自己那一套填一格，风格漂移。
4. **但同样的原因也带来强项**：测试覆盖率高、文档密集、防御性强（虽然过度）、抽象意识在关键节点（分层、capability 接口、连接状态机、snapshot 模型、HPKE 边界）到位。说明关键架构决策是有人盯着的，只是没盯到每个实现细节。

---

## 六、改进方向（按性价比排序，仅讨论）

架构选型不用动（已经对了），要做的是实现治理：

治理主线先于具体 task：

1. **架构收敛**：把 legacy path、compat mode、旧命名、旧端口、旧协议入口列成清单，明确保留 / 迁移 / 删除 / 仅保留 wire compatibility 的边界。
2. **状态模型收敛**：尤其是 Mac ↔ iOS 之间的连接态、session 列表、turn 流、history polling、Relay reconnect、capability，需要形成更明确的状态机与测试不变量。
3. **质量门禁收敛**：协议、连接、agent 行为一旦变动，必须同步 Mac protocol pack、iOS mirror、定向测试与 living docs。这个规则比单纯增加测试数量更重要。

| 优先级 | 动作 | 收益 | 成本 |
|---|---|---|---|
| 1 | **杀双写**：`BackendList` / `deriveCapabilities` 收敛成单一 capability 源，顺带修 `question_reply` 不下发 bug | 高（止血 bug 工厂） | 低 |
| 2 | **切 `config/config.go` 死重**：把真正用到的 ~200 行抽进 go-bridge，删整块 Weixin/Feishu | 高（去最显眼屎山） | 低-中 |
| 3 | **拆 god-object**：BridgeProvider / ChatViewModel 巨型方法 / ChatUIKitContainerView / handlers.go，按职责拆 | 高（可维护性） | 高 |
| 4 | **抽 message-web / remote-web 共享包**：止住最大漂移源 | 中-高（防未来腐化） | 中 |
| 5 | **立"工程宪法" + CI 卡**：日志统一 wrapper、本地化统一、并发原语统一、类/方法行数上限 + CI lint | 中（从根上遏制 AI 多模型轮换的风格漂移） | 中 |

做完 1-2，两个仓库的架构健康度能稳稳进"高质量单人/小团队系统软件"档；做完 3-5，可维护性接近产品级。

### 本报告的定位（2026-07-03 二次复核后补）

- **作为讨论底稿**：可采信。强项与技术债的主判断、性价比排序均经二次复核成立。
- **作为执行计划**：**不能直接照做**。每条技术债需先拆成独立 issue，再按三个维度排序：① 是否影响产品行为（影响者优先）、② 是否有测试保护（有保护则低风险重构、无保护则先补测试）、③ 是否可小步回滚（可回滚者优先）。第一波（杀双写、切 config）属"低成本 / 高收益 / 不动 UI 与大状态机"，适合先做；第二波（拆 god-object、抽 web 共享包）价值大但更易牵出回归，需配套测试保护后再动。可施工版本见 [2026-07-03-architecture-health-execution-plan.md](2026-07-03-architecture-health-execution-plan.md)。

---

## 附录：评估方法与证据来源

- **两个评估 agent 的领域划分**：
  - MacBridge：分层/依赖方向（`core` ↔ `agent` ↔ `go-bridge`）、Agent 抽象质量（`core/interfaces.go`）、relay module 边界、并发与 shutdown（`go-bridge/main.go`、`handlers.go`）、测试覆盖（126 文件 / 1022 Test / 8 回归）、技术债信号（双写、panic、长文件、命名残留）、AI 多模型痕迹。
  - iOS：分层与依赖方向、状态管理一致性（snapshot-first、merge）、抽象质量（`BridgeProvider` / `RecoveryCoordinator` / Swift↔JS 契约）、并发与生命周期、测试覆盖（86 文件）、技术债信号、AI 多模型痕迹。
- **量化数据来源**：行数/文件数/Test 数由 `wc -l` / `grep -c` / `find` 统计；日志风格、本地化、TODO 重复由全仓 grep 计数；模块边界由 `grep import` 反向验证。
- **关联文档**：活文档差距审计见 `docs/2026-07-03-living-docs-gap-audit.md`（聚焦文档与代码一致性，本文件聚焦架构健康度，两者互补）。
- **评估局限**：是时点快照，未做性能/压测、未做安全审计（relay HPKE 的密码学正确性靠其专属回归测试与 `relay_crypto_vectors_test.go` 保证，不在本次评估范围）、未评估真机 UX。
- **非正式直觉评分**：以下不是量化审计结果，只是帮助校准"完美 / 屎山 / 大厂级"这些词的主观坐标：功能复杂度约 8/10，架构方向约 7/10，当前一致性约 6-7/10，测试与验证体系约 6/10，长期可维护性约 6.5/10，大厂级成熟度约 4-5/10。它说明的是"明显高于普通 AI 生成项目，但还没有达到大型成熟产品工程水准"。
- **未采纳的复核反馈及理由**（2026-07-03 二次复核反馈中，以代码为准未照单全收的）：
  - ForTesting 数量复核反馈建议"30+" → 报告写精确 **34**（`grep -c ForTesting BridgeProvider.swift` 可复现，精确值比范围值更准且不弱）。
  - web 重复复核反馈记忆"其他几个不全等" → 代码核实 `DiffViewer.tsx` 字节全等（diff exit 0）、`ReasoningBlock.tsx` 仅差 2 行；只有 ProcessGroup（差 43）/NarrativeBlock（差 68）非全等。按代码写，未采纳"都不全等"。
  - config "只用 4 个符号" 复核反馈建议模糊成"少量" → **部分采纳**：保留可核实的精确事实（4 符号、生产仅 1 文件、config 导出 75），同时结论措辞稳健化为"主要运行路径只依赖少量加载与配置结构能力"，避免边缘反例打穿。
