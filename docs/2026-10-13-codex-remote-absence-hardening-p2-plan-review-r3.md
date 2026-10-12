# codex-remote 缺席语义加固方案（P2）评审报告 r3

- 评审轮次：第 3 轮（复审；r1 REVISION_REQUIRED 阻塞 2/建议 4 → r2 APPROVED 阻塞 0/建议 3 → 本轮审 v3）
- 评审模式披露：延续模式（r1/r2 报告全文读入作回归清单，不作结论来源）。承重锚点处置：
  源码身份自 r2 起未变（`git diff 95cbb3e0 HEAD -- '*.go' '*.swift'` 为空 + 工作树仅未跟踪
  docs，本轮重新验证），r2 亲核锚点按命题/锚点粒度复用并逐项确认方案对应文本未变；
  **v3 新增/改写文本涉及的全部锚点本轮亲核**（含一项 r2 建议措辞本身携带、本轮新证据
  推翻的精度问题，见 F-10）。
- 评审技能/契约：plan-review（契约 plan-contract-v1.1）

## 1. 结论

- **verdict: APPROVED**
- blockers: **0**
- advisories: **1**（F-10，风险注记精度；不影响设计与验收，可在实施提交文档收口时顺手修正）
- 方案：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md`（v3，未跟踪新增文件）
- 方案 SHA-256：`95dabab6dcd9bc32e86535c94a45a50cc7eaa314d82e71b8afff3293c637839e`
  （与派发 SHA 一致，本轮评审对象即该快照）
- 本轮范围：**定向复核 + 全局门控重走**——r2 三条建议（F-7/F-8/F-9）闭合核验、v3 修订
  回归检查（含 §0.2 声明范围外的一致性校正：实施授权表述）、R→S→E/OD 门控一致性重走；
  r1/r2 已核锚点在源码身份复核通过后按命题粒度复用。

**总体判断**：r2 三条建议全部按其修订方向闭合且无驳回项——F-7 的时延限定链
（缓存命中跳过 Stat → lookupDesktopEnv → "no desktop environment" → 缓存过期/重启后才
Stat 早退）每处锚点本轮亲核成立；F-8 的五处校准与源码逐字对上（iOS 未安装/未登录共用
banner、Mac 独立文案与测试）；F-9 的来源清单更新与仓库实际状态一致（零代码差异、
.exec-plan state 已入库）。**§0.2 声明范围外的授权校正经独立核验为事实准确**：事故档案
状态行（@ 11ae0938，已入库）明文记录「owner 已预授权过审即实施」，owner 工作流入口
原始请求重申同一授权——r2 报告 §6 门 1「授权未发出」的框定确与仓内记录不符，v3 校正
有据。修订未引入设计回归。本轮新发现一条建议（F-10）：v3 采纳 r2 建议措辞写入的
「实践上界受 ~120min 兜底自动重启限制」在本部署目标机上不成立（本机
`autoRestartEnabled = 0`，事故 7.7h 单 pid 连续运行与此一致）——属 r2 建议措辞本身
携带的精度缺口（旧问题漏检 + 本轮新证据），不触及设计与验收。

**implementation_readiness**：

- 设计通过。**实施授权状态（本轮核实）**：仓内记录（事故档案状态行 @ 11ae0938
  「owner 已预授权过审即实施」）+ owner 工作流入口原始请求（「实施这个文档里的所有
  任务」）一致支持「过审即实施」预授权；按该记录，本 APPROVED 即满足实施启动条件。
  编排层如仍设 owner 终确认出口，属流程安排而非方案门。
- 授权后开工顺序：S-1 → S-2（依赖 S-1 分类器）→ S-3（独立）→ S-4（测试绿 + **owner
  拍板部署时机**）→ S-5。E-8 为 S-4 完成门（实施后验证，非前置）。
- kickoff 时按 CLAUDE.md P0 来源门（门 2）用当时真实 HEAD 重新生成 §2.1 清单（F-9
  闭合标准，v3 已保留该义务）。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 备注 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | HEAD=`11ae09386c5dfc926127629ed3014563d1fdf116`；方案锚点基线=`95cbb3e0bb4716df88c7482d57ca1f94d2470380` | 干净；未跟踪：方案 v3、r1/r2 评审报告（本轮新增 r3 报告） | 与方案 §2.1 逐项一致：`git diff 95cbb3e0 HEAD -- '*.go' '*.swift'` 为空（本轮重跑）；11ae0938 为 docs-only（git show --stat：.exec-plan state/完成报告/事故档案/think.md） |
| iOS（只读） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `507a1e23b8c38847f1433921dbe65557965a52e8` | 脏（porcelain 277 行，本轮复核 `wc -l`=277，与 §2.1 一致） | E-3 证据来源；只读 3 个 swift 文件 |
| 上游 | 不适用 | — | — | — | §2.1 论证 P2 三项为 bridge 私有运维语义；r1/r2 已复核，本轮无新证据推翻 |
| 本机运行态（F-10 证据） | `defaults read org.openagi.cordcode.link`（只读） | — | — | — | `autoRestartEnabled = 0`（owner 显式关闭；`autoRestartIntervalMinutes` 未设=默认 120） |

运行证据档案（存在性 + 内容复核）：`build/p1-reconcile-75543.log`（含 auth_probe 计数）、
`build/p1-reconcile-75543-offline-extended.log`（`runtime_pid=75543 lstart=01:26:55`、
`zombies_under_runtime=0`）均在——E-8 口径来源有效，r1 数字核验可复用。

### 2.2 亲核范围与复用

- **本轮亲核（v3 新增/改写文本的全部锚点 + 授权校正）**：
  - 授权记录：事故档案状态行（:3-9）@ 11ae0938；owner 工作流入口原始请求（harness
    转发原文）。
  - F-7 闭合链：`pairing.go:103-106`（无硬编码 TTL）、`:112-121`（:114 缓存命中即返回、
    不查 Stat）、`:124-133`（仅成功进缓存）；`pairing_persist.go:304-321`（:308
    chatGPTAuthCached 先行、:319 lookupDesktopEnv）；P1 方案 E-9 的 ~96h 观察记录；
    `RuntimeManager.swift:631-658`（:649 默认开、:651 默认 120min、:654 仅
    ready/readyNoAgents 计时、evaluateAutoRestart 每 3s 调用、lastLaunchedAt 仅启动时
    设置）、`management_api.go:462-466`（bridge 级 status 只看 agents 配置，backend
    失败不影响 .ready）；本机 `defaults read org.openagi.cordcode.link`；事故档案 §2
    时间线 + `git log -S autoRestartIntervalMinutes`（84a573ef，06-17 引入）。
  - F-8 闭合五处：`pairing_persist.go:242-251`（:244-245 未安装/未登录/超时→同一条
    banner）；iOS `BackendModels.swift:25-38`；Mac `BridgeStatusView.swift:339-340`、
    `ProductCopyTests.swift:60-64/:88`、`MacBridgeBehaviorTests.swift:164`。
  - F-9 闭合：`git diff`（空）、`git status`（未跟踪恰为方案+r1+r2）、`git show --stat
    11ae0938`（.exec-plan state 418 行入库）。
  - D1/D2 承重锚点抽核（r2 已核、本轮直接复核）：`pairing_persist.go:235-261`（分类器
    全文 + 三消费方注释 :240-241 + reconnectFromStore 单次调用）、`diagnostics.go:10-35`
    （三态常量、:19 seam、:28-32 非 available/pairing_required 分支）。
- **复用 r2（源码身份未变 + 方案对应文本未变）**：E-1..E-7、E-9 核心锚点
  （diagnostics.go、agent_descriptor.go 枚举/直通/ps 先例、pairing_persist.go 两循环与
  restoreOnce 返回路径、chatgpt_auth.go Stat 早退/spawn、pairing.go:565-568 与缓存注释、
  backoff.go、advertised_url_refresh.go:135、main.go:502、readiness 先例、protocol pack
  无封闭枚举、iOS raw String 解码）；r2 新查四项（Mac Swift 消费、protocol pack、ps
  格式、错误文本溯源）；r1/r2 的 P1 对账数字（本轮存在性复核）。
- **未核/不适用**：未运行测试或构建（方案阶段只读）；未核 iOS `output/` 下 baseline
  副本（非 E-3 来源）。

### 2.3 锚点核验表（本轮亲核部分；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 依据 |
| --- | --- | --- | --- | --- |
| 授权校正：「事故档案状态行（@ 11ae0938）记录 owner 已预授权过审即实施」 | 事故档案 `:3-9`（已入库 @ 11ae0938） | `:7-9 P2（缺席语义加固）方案 docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md 评审中，owner 已预授权过审即实施。` | 吻合 / 成立：仓内记录与 v3 引用逐字一致；owner 入口请求（「实施这个文档里的所有任务」）重申同一授权 | 亲核 |
| F-9：「相对锚点基线 95cbb3e0 零代码差异」 | `git diff 95cbb3e0 HEAD -- '*.go' '*.swift'` | （空输出，退出码 0） | — / 成立 | 亲核 |
| F-9：「.exec-plan state 已随 11ae0938 入库、不再未跟踪」 | `git show --stat 11ae0938` | `.exec-plan/state/plan-92731ca149b2.json \| 418 ++++…`（同提交仅 docs/state/think.md） | 吻合 / 成立：当前 `git status` 未跟踪恰为方案 v3 + r1/r2 报告 | 亲核 |
| F-7：「chatGPTAuthCached 缓存命中跳过 Stat（pairing.go:114）」 | `agent/codex-remote/pairing.go:112-121` | `:114 if c := p.auth; c != nil && (!c.hasExp \|\| time.Now().Before(c.expAt.Add(-authCacheExpiryMargin))) {` … `:120 return token, accountID, nil` | 吻合 / 成立：命中路径不触碰 os.Stat | 亲核 |
| F-7：「restoreOnce pairing_persist.go:308 先 chatGPTAuthCached 后 :319 lookupDesktopEnv」 | `agent/codex-remote/pairing_persist.go:304-321` | `:308 if _, _, err := p.chatGPTAuthCached(ctx); err != nil { return err }`；`:319 env, err := p.lookupDesktopEnv(ctx, rec.EnvID)` | 吻合 / 成立：缓存有效期内卸载 → 直接走到 "no desktop environment" | 亲核 |
| F-7：「缓存仅存成功、有效期仅由 token exp 决定 pairing.go:103-106/:124-130」 | `pairing.go:103-106`、`:124-133` | `:103-105 // authCacheExpiryMargin … a freshness margin on the token's claim, not a TTL constant (P1 r1 F-7: validity comes from the token's exp alone; no hardcoded TTLs)`；`:128-130 expAt, hasExp := authJWTExpiry(token); … p.auth = &authCache{…}`（仅 err==nil 后执行） | 吻合 / 成立 | 亲核 |
| F-7：「P1 一次观察量级 ~96h，非常量」 | P1 方案 `docs/2026-10-12-codex-remote-probe-economy-p1-plan.md:252`（E-9） | `exp 距今 **96h56m**…一次性取证、样本已销毁、不可复核…仅证明 exp 存在与量级，禁止当硬 TTL 或常量` | 吻合 / 成立：v3 表述与 P1 证据纪律一致 | 亲核 |
| F-7/F-10：「~120min 兜底自动重启（默认开启，RuntimeManager.swift:649/:651；重启清空内存缓存）」 | `MacBridge/MacBridge/Services/RuntimeManager.swift:648-658` | `:649 let enabled = UserDefaults.standard.object(forKey: "autoRestartEnabled") as? Bool ?? true`；`:651 let minutes = UserDefaults.standard.object(forKey: "autoRestartIntervalMinutes") as? Int ?? 120`；`:654 if status == .ready \|\| status == .readyNoAgents,` | 定位吻合 / **源码事实成立（默认开/120min/仅 ready 计时——缺席态 bridge 级 status 仍为 ready，`management_api.go:463-466` 亲核：status 只看 agents 配置），但「实践上界」推断在本机不成立，见 F-10** | 亲核 |
| F-10 证据 1：本机实际设置 | `defaults read org.openagi.cordcode.link`（只读） | `autoRestartEnabled = 0;`（autoRestartIntervalMinutes 未设） | — / 兜底重启在本部署目标上被 owner 关闭 | 亲核 |
| F-10 证据 2：事故 7.7h 单 pid 连续运行 | 事故档案 §2；`git log -S autoRestartIntervalMinutes` | `:78 10:26:25 bridge runtime 启动（pid 1917…）`…`:83 18:17 止血：kill 1917`（期间无兜底重启）；auto-restart 自 84a573ef（06-17）已存在 | — / 与 autoRestartEnabled=0 互相印证：缺席态下 120min 上界在本机至少失败过一次 | 亲核 |
| F-8：「未安装/未登录共用同一 banner，pairing_persist.go:244-245」 | `pairing_persist.go:242-251` | `:244 case "请打开并登录 ChatGPT Desktop", "请先安装并登录 ChatGPT Desktop", "ChatGPT 未登录", "读取 ChatGPT 登录态超时":` `:245 return "请打开并登录 ChatGPT Desktop"` | 吻合 / 成立：三类错误→同一条 banner；Desktop 离线（:246-247）另一条 | 亲核 |
| F-8：iOS 仅特判 available/pairing_required，其余通用分支显示 reason | iOS `BackendModels.swift:25-38` | `:25 guard descriptor.status == "available" else {`；`:26 if kind == .codexRemote, descriptor.status == "pairing_required" {`；`:32-36 …reason: trimmedReason.flatMap { $0.isEmpty ? nil : $0 } ?? "后端暂不可用"` | 吻合 / 成立：iPhone 用户可见文案不区分未安装/未登录 | 亲核 |
| F-8：Mac 独立文案 `BridgeStatusView.swift:339-340` + 测试 | `BridgeStatusView.swift:339-340`、`ProductCopyTests.swift:60-64/:88`、`MacBridgeBehaviorTests.swift:164` | `:339 case "not_detected": return L10n.statusNotFound`；`:340 case "not_logged_in": return L10n.statusLoginRequired`；ProductCopyTests `:60 XCTAssertEqual(Self.displayStatus("not_detected"), "Not Found")`、`:64 …("not_logged_in"), "Login Required")`、`:88 let allStatuses = ["available", "not_detected", "not_logged_in",`；BehaviorTests `:164 XCTAssertEqual(BackendStatusText.display("not_logged_in"), L10n.statusLoginRequired)` | 吻合 / 成立：枚举区分的 Mac 可见面与测试覆盖真实存在 | 亲核 |
| D1/D2 承重：分类器 + 三消费方 + 五态落点（r2 已核，本轮直接复核） | `pairing_persist.go:235-261`、`diagnostics.go:10-35` | `:240-241 // Shared by reconnectFromStore, restorePersistedPairing and watchBinding so every restore loop surfaces the same category through the readiness seam.`；diagnostics `:28-32 if paired \|\| … { if snap.Message != "" { return ReadinessServiceNotRunning, snap.Message } … }` | 吻合 / 成立：D1 单一写入点与映射落点、D2 双循环共享错误全集的源码依据不变 | 亲核 |

复用清单（r2 亲核 + 本轮源码身份复核通过，方案对应文本未变）：E-1 diagnostics 三态、
E-2 枚举全集/直通、E-4 两循环同 backoff、E-5 Stat 早退/spawn、E-6 ps 收尸先例
（:412 `Output()`）、E-7 ticker 先例、E-9 核心链（:372-423/:408/:418-421/:254/:353）、
pairing.go:565-568 "no desktop environment"、backoff.go、readiness 先例、protocol pack
无封闭枚举、iOS raw String 解码（CCCodeBridgeModels.swift:192）。运行态易变结论
（P1 对账数字）本轮做了档案存在性 + 关键字段复核。

## 3. 意见（本轮新发现，1 条建议）

### F-10［建议］「实践上界受 ~120min 兜底自动重启限制」在本部署目标机上不成立：本机 autoRestartEnabled=0，事故 7.7h 单 pid 运行与之一致

- **位置**：§6 风险（「实践上界受 ~120min 兜底自动重启（默认开启，
  RuntimeManager.swift:649/:651；重启清空内存缓存）限制」）、§0.2 F-7 行同族表述。
- **证据**：v3 该句采纳自 r2 F-7 的建议措辞（「实践受 ~120min 自动重启限制」），r2 当时
  未核本机实际设置。本轮核实：(1) `defaults read org.openagi.cordcode.link` →
  `autoRestartEnabled = 0`——owner 已显式关闭兜底重启（CLAUDE.md 亦记载「临时关
  autoRestartEnabled」为 owner 排障习惯），`autoRestartIntervalMinutes` 未设；
  (2) 事故档案 §2：pid 1917 于 10:26:25 启动、18:17 被 kill，**7.7h 连续运行、期间无
  兜底重启**，而 auto-restart 机制自 84a573ef（06-17）即存在且缺席态下 bridge 级
  status 仍为 ready（management_api.go:463-466 本轮亲核：status 只看 agents 配置，
  计时条件结构上满足）——与设置关闭互相印证。两线证据一致表明：在 S-4 的部署目标
  机上，卸载掩蔽窗口的实际实践上界不是 ~120min，而是 token exp（P1 观察量级 ~96h）。
- **影响**：仅风险注记精度——owner 按「实践上界 ~120min」读 §6 会低估该掩蔽窗口约
  一个量级以上。设计、切片与验收均不受影响：窗口内行为两种上界下都=现状（非回归）、
  主场景（冷启动/无缓存）不受影响、S-2 单测直接注入 notInstalled 错误、E-9 的时延
  限定结论反而被强化（进入 notInstalled 更晚）。
- **修订方向**（最小充分）：§6 该句改为条件化表述——「默认配置上界 ~120min（需
  autoRestartEnabled 开启）；auto-restart 被关闭时实践上界=token exp（~96h 量级），
  本机当前 autoRestartEnabled=0（事故 7.7h 单 pid 与此一致）」；§0.2 F-7 行如保留
  「~120min 兜底」字样同步加限定。实施提交的文档收口（S-5）一并落实即可，不要求
  再送一轮方案评审。
- **闭合标准**：实施交付文档（完成报告/事故档案回填）中的窗口上界表述不再无条件
  声称 ~120min 实践上界；如 §6 文本先行修订则与之一致。

## 4. 历轮意见闭合处置

### 4.1 r2 三条建议（本轮逐条核验）

| r2 意见 | 本轮核验 | 处置 |
| --- | --- | --- |
| F-7［建议］E-9 可达链省略 auth 缓存时延限定 | E-9 补「时延限定（r2 F-7）」：缓存命中跳过 Stat（pairing.go:114 亲核）→ restoreOnce 先 chatGPTAuthCached（:308 亲核）后 lookupDesktopEnv（:319 亲核）→ "no desktop environment"（pairing.go:565-568，r2 核+本轮零差异复用）→ 缓存过期/runtime 重启后才 Stat 早退；D2 覆盖依据段同步；§6 风险补窗口上界（理论上界 token exp、~96h 非常量纪律与 P1 E-9 记录一致、主场景豁免）。链条每环本轮亲核成立 | **闭合**（窗口上界的「实践 ~120min」标注精度另记 F-10） |
| F-8［建议］「iOS 已能显示的原始字符串」措辞强于实际机制 | §1 非目标、D1、S-1 三处按评审指定位置校准，另 OD 表注、§6 R→S 行两处同族表述一并对齐——均为「iOS 走通用不可用分支显示 reason 文案（未安装/未登录共用 banner，pairing_persist.go:244-245）；枚举级区分的可见面 = wire + Mac UI（BridgeStatusView.swift:339-340 独立文案）」。五处与源码逐字对上（本轮亲核 iOS/Mac 两侧及三处测试锚点） | **闭合** |
| F-9［建议］§2.1 来源清单滞后一个 docs-only 提交 | §2.1 更新至 11ae0938 并注零代码差异（本轮 `git diff` 重跑为空）与当前未跟踪状态（方案 v3 + r1/r2，`git status` 吻合）；.exec-plan state 已入库（`git show --stat 11ae0938` 亲核）；§2.2 标题同步；kickoff 重新生成清单义务保留 | **闭合** |

### 4.2 r1 六条意见（r2 已核闭合；本轮身份复核后维持）

源码身份自 r2 未变（零代码差异 + 工作树干净，本轮验证），方案对应文本（D3 armed 行、
D2 双循环、E-9、锚点校正、S-5 CHANGELOG、§6 notLoggedIn 风险）自 v2 未再改动——
F-1..F-6 维持 r2 判定的**闭合**，本轮未发现重开迹象。

### 4.3 范围外一致性校正（授权表述）核验记录

v3 §0.2 声明的校正（r2 报告 §6 门 1「授权未发出」与仓内记录不符）本轮独立核验：
(1) 事故档案状态行 @ 11ae0938（已入库）明文「owner 已预授权过审即实施」——逐字
核实；(2) owner 工作流入口原始请求「实施这个文档里的所有任务」重申同一授权（harness
转发原文）。r2 框定确与仓内记录不一致，v3 校正**事实准确**；且保留「S-4 部署时机仍
owner 拍板」与 kickoff 门 2，未引入门控矛盾。事故档案 10-12 r2 记录中「评审通过不
增加写代码授权」系 owner 10-13 全量授权之前的纪律表述，已被状态行的预授权记录更新
覆盖，无残留冲突。

## 5. 门控一致性复核（本轮重走）

- **R→S→验收**：R-1→S-1（五态单测含 fail-closed 负例/成功清除断言 + E-3 零 iOS 改动）、
  R-2→S-2（双循环节奏单测 + Warn 间隔对账）、R-3→S-3（判定纯函数边界 + 采样注入 +
  armed 行冒烟）、R-4→全部单测 + S-4 运行态；无悬空引用。
- **E 门**：Gate A（E-1..E-7、E-9）verified——r2 亲核 + 本轮源码身份复核复用，v3 改写
  文本（E-9 时延限定、E-5 限定语）本轮亲核锚点成立；E-8 正确登记为 S-4 完成门。Gate B：
  OD-1 decided（降频 120s，工程决定，事故 §5.3-2「降为低频探测或停轮」许可范围内）。
- **非目标与简报逐项一致**：零 iOS 改动（E-3 + F-8 校准后表述准确）、零新增 wire 枚举
  （E-2 + protocol pack 复用）、不动其他 backend 就绪面、不停轮、不熔断、P1 门控/缓存
  语义不变（D1/D2 只读错误分类与改 sleep 来源；chatGPTAuthCached/ctrl 门控路径
  pairing_persist.go:304-318 本轮读核未触碰设计）。
- **无矛盾允许/禁止**：watchdog「只观测不处置」与 S-3 失败处理一致；「armed 一次性行」
  与「采样期正常静默」不冲突；「超标 WARN 不节流」仅限异常态。
- **修订范围声明核实**：§0.2 声明「仅三条建议 + 必要一致性维护（头部状态行、§6 下一
  阶段入口）」——本轮对照 r2 所引 v2 文本，实际改动恰为 F-7/F-8/F-9 闭合点 + 授权
  校正 + 头部/§6 同步，未见越界设计改动；授权校正透明声明且事实准确。
- **回退边界**：纯观测/分类/节奏层，revert 即回退；与 P0/P1 已交付语义无冲突。

## 6. 剩余门（与阻塞分开列；通过不等于以下已满足）

1. **S-4 部署时机**：owner 拍板（方案已注明；CLAUDE.md 的「改完即构建覆盖安装」惯例
   在本方案被更保守的 owner 拍板门覆盖，属方案自设的更严门，非缺陷）。
2. **E-8 P1 口径复测**：S-4 完成门——部署后按 `build/p1-reconcile-75543.log` 口径复测
   探针/刷新节奏不劣化（含 P1 在线态观察收口）。
3. **watchdog armed 特征行运行态核验**：S-4 完成门（启动即出，pid/period/
   childrenThreshold 字段；超标 WARN 以单测为准、健康部署不出现）。
4. **未安装态运行态对账 N/A 记录义务**：本机 ChatGPT.app 在装，该态以单测为准，
   运行态 N/A 须如实记录（S-4 执行时落实）。
5. **实施 kickoff 来源清单再生成**：CLAUDE.md P0 来源门门 2（F-9 闭合标准，v3 已保留）。
6. **F-10 收口**：实施交付文档中窗口上界表述条件化（见 F-10 闭合标准；不构成实施
   前置）。
7. **实施启动确认（owner/编排层出口）**：仓内记录 + owner 入口请求均支持「过审即
   实施」预授权（本轮核实一致）；编排层如设 owner 终确认出口属流程安排，非方案门。

## 7. 结论复核

- 覆盖足够：r2 三条建议逐条闭合核验（全部新锚点本轮亲核）、r1 六条经身份复核维持
  闭合、授权校正独立核验为事实准确、全局门控重走无矛盾、修订范围声明与实际改动
  相符。
- 未发现：伪造已验证事实、待捕项伪装成事实、必需要求被可选范围卡死、与 P0/P1 交付
  语义冲突、或修订引入的设计回归。
- 新发现 F-10 为 r2 建议措辞携带的精度缺口（旧问题漏检 + 本轮新证据——本机
  defaults 实读 + 事故时间线交叉），属风险注记透明度，与 r2 对 F-7 的分级先例一致，
  不构成阻塞。
- 争议项：无。

**verdict: APPROVED（阻塞 0 / 建议 1：F-10）**。F-10 可在实施提交的文档收口中顺手
修正，不要求再送方案评审。下一步：按仓内预授权记录进入实施（S-1→S-2→S-3），S-4
部署时机 owner 拍板；kickoff 按门 2 重新生成来源清单。
