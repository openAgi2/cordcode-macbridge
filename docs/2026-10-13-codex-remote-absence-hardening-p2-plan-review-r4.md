# codex-remote 缺席语义加固方案（P2）评审报告 r4

- 评审轮次：第 4 轮（复审；r1 REVISION_REQUIRED 阻塞 2/建议 4 → r2 APPROVED 阻塞 0/建议 3 → r3 APPROVED 阻塞 0/建议 1 → 本轮审 v4）
- 评审模式披露：延续模式（r1/r2/r3 报告全文读入作回归清单，不作结论来源）。本轮为
  r3 APPROVED 后 v4 勘误（F-10 风险注记精度）的**定向验证轮**：重点是 v4 相对 r3 已审
  v3 的差异面（勘误不引入新设计）+ 通过分支七条件机械校验 + F-10 闭合核验 + 全局门控
  重走。独立性限制：延续模式未经未见历轮结论的评审员重审；本轮缓解因素为 v4 差异是
  勘误级（§0.3 自declared 范围），差异面与 F-10 事实链本轮全部亲核，非差异面按命题粒度
  复用 r2/r3 亲核（源码身份未变已复核），且额外做了元审核级锚点抽查（§2.3）。
- 评审技能/契约：plan-review（契约 plan-contract-v1.1）

## 1. 结论

- **verdict: APPROVED**
- blockers: **0**
- advisories: **0**（F-10 已在 v4 闭合，无新发现）
- 方案：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md`（v4，未跟踪新增文件）
- 方案 SHA-256：`97a97624efda8d0e9c823b9d952cad2941b6b6d7c533deaaf0a4f327caf9ecf3`
  （与派发 SHA 一致，本轮评审对象即该快照；评审期间方案只读未变）
- 本轮范围：**full（简报范围全案：Mac 仓 agent/codex-remote 错误分类器/就绪五态/循环节奏
  + go-bridge zombie_watchdog 新文件/启动接线 + 定向单测 + Release 部署验证含 E-8 复测 +
  零 iOS 改动主张）。本轮执行 = v4 勘误差异面定向复核 + F-10 闭合核验 + 全局门控重走；
  承 r1-r3 全量锚点亲核按命题粒度复用（源码身份未变，本轮复核），另做元审核级锚点抽查。**

**总体判断**：v4 是 r3 APPROVED（v3）之后的勘误版，修订范围与 §0.3 声明完全一致——
F-10 的「~120min 实践上界」按本机 autoRestartEnabled=0 条件化（§6 风险、§0.2 F-7 行、
D2 覆盖依据段三处同步加限定），加必要一致性维护（头部状态行、§2.1 未跟踪清单、§6 评审
状态同步）。**逐项核验确认：设计、验收口径、E/S/R/OD 门控均未变化**；F-10 闭合文本与
r3 修订方向逐字对应，其事实链（本机 defaults 实读、RuntimeManager 缺省值、management_api
status 口径、事故 7.7h 时间线、84a573ef 引入时间、P1 ~96h 量级纪律）本轮全部亲核成立。
全文扫描确认无残留的无条件「~120min 兜底」表述；120s（降频间隔）各处表述与 F-10 无关
且口径一致。无新阻塞、无新建议。

**implementation_readiness**：

- 设计通过（r3 对 v3 全案 APPROVED；v4 勘误未触及设计，本轮复核确认）。**实施授权状态
  （本轮再核）**：事故档案状态行（@ 11ae0938，已入库）明文「owner 已预授权过审即实施」，
  owner 工作流入口原始请求（「实施这个文档里的所有任务」）重申同一授权——本 APPROVED 即
  满足实施启动条件（与 r3 核验一致）。
- 开工顺序：S-1 → S-2（依赖 S-1 分类器）→ S-3（独立）→ S-4（测试绿 + **owner 拍板部署
  时机**）→ S-5。E-8 为 S-4 完成门（实施后验证，非前置）。
- kickoff 时按 CLAUDE.md P0 来源门（门 2）用当时真实 HEAD 与未跟踪状态重新生成 §2.1
  清单（r2 F-9 闭合标准，v4 已保留该义务）。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 备注 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | HEAD=`11ae09386c5dfc926127629ed3014563d1fdf116`；方案锚点基线=`95cbb3e0bb4716df88c7482d57ca1f94d2470380` | 干净；未跟踪恰为：方案 v4 + r1/r2/r3 评审报告（本轮 `git status --porcelain` 逐项复核） | 与方案 §2.1 逐项一致：`git diff 95cbb3e0 HEAD -- '*.go' '*.swift'` 为空（本轮重跑）——方案全部代码锚点在 HEAD 逐字节有效，r2/r3 亲核可按命题粒度复用 |
| iOS（只读） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `507a1e23b8c38847f1433921dbe65557965a52e8`（本轮 `rev-parse` 复核未变） | 脏（porcelain 277 行，本轮复核 `wc -l`=277，与 §2.1 一致） | E-3 证据来源；只读 3 个 swift 文件；§2.1 清单在本轮评审时点仍准确 |
| 上游 | 不适用 | — | — | — | §2.1 论证 P2 三项为 bridge 私有运维语义；r1/r2 已复核，本轮无新证据推翻 |
| 本机运行态（F-10 证据，易变结论不复用、本轮重读） | `defaults read org.openagi.cordcode.link`（只读） | — | — | — | `autoRestartEnabled = 0;`（`autoRestartIntervalMinutes` 未设）——v4 §6「本机当前 autoRestartEnabled=0」在评审时点仍成立 |

运行证据档案（存在性经 r3 复核，本轮未重开）：`build/p1-reconcile-75543.log`、
`build/p1-reconcile-75543-offline-extended.log`——E-8 口径来源有效。

### 2.2 本轮亲核 vs 复用范围

- **本轮亲核（v4 新增/改写文本涉及的全部事实 + 元审核级锚点抽查）**：
  - v4 差异面：全文 `120|autoRestart|兜底|重启` 扫描（逐条归类：~120min 兜底三处均已
    条件化；120s 降频间隔十处与 F-10 无关）；§0.3 声明范围与实际改动对照；D1/D2/D3/
    OD-1/E 表/S 表/R 列表与 r2/r3 评审记录的文本对照（设计未变验证）。
  - F-10 事实链：`defaults read`（重读）；`RuntimeManager.swift:648-658`；`management_api.go:462-466`；
    事故档案状态行（:3-9）与 §2 时间线（pid 1917 10:26:25→18:17）；`git log -S autoRestartIntervalMinutes`
    → 84a573ef（2026-06-17）；P1 方案 E-9（:252，~96h 量级 + 禁止当常量纪律）；
    CLAUDE.md「临时关 autoRestartEnabled」记载。
  - 锚点抽查（承重设计锚点 8 处，含行号与原文）：`pairing_persist.go:240-251`（分类器 +
    三消费方注释）、`:405-423`（watchBinding :408/:411/:418-421）；`diagnostics.go:10-35`；
    `agent_descriptor.go:20-34`、`:253-270`；`backoff.go:16-17`；
    `advertised_url_refresh.go:130-140` + `main.go:502`；`chatgpt_auth.go:53-57/:66`；
    `go-bridge/zombie_watchdog.go` 不存在（确为新文件）。
- **复用 r2/r3（源码身份未变 + 方案对应文本未变，本轮逐项确认）**：E-1..E-9 核心锚点
  （diagnostics 三态、枚举全集/直通、两循环 backoff、Stat 早退/spawn、ps 收尸先例、ticker
  先例、E-9 可达链、pairing.go 缓存/desktopOffline 生成点、backoff、readiness 先例、
  protocol pack 无封闭枚举、iOS raw String 解码与通用分支、Mac BackendStatusText 独立
  文案与测试）；r2 新查四项（Mac Swift 消费、protocol pack、ps 格式、错误文本溯源）。
- **未核/不适用**：未运行测试或构建（方案阶段只读）；未核 iOS `output/` 下 baseline 副本
  （非 E-3 来源）；v3 字节快照不可得（方案文件未入 git、无备份），v3→v4 差异以 §0.3 声明
  范围 + r3 报告对 v3 的逐处引文 + r2/r3 对设计文本的详尽记录三方对照核验（见 §3.1
  方法说明与限制）。

### 2.3 锚点核验表（本轮亲核部分；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 依据 |
| --- | --- | --- | --- | --- |
| F-10：「本机当前 autoRestartEnabled=0」（§6） | `defaults read org.openagi.cordcode.link`（本轮重读） | `autoRestartEnabled = 0;` | — / 成立：易变运行态结论本轮重新实读，非复用 r3 | 亲核 |
| F-10：「缺省 true/120min，RuntimeManager.swift:649/:651」 | `MacBridge/MacBridge/Services/RuntimeManager.swift:648-658` | `:649 let enabled = UserDefaults.standard.object(forKey: "autoRestartEnabled") as? Bool ?? true`；`:650 guard enabled else { return }`；`:651 let minutes = UserDefaults.standard.object(forKey: "autoRestartIntervalMinutes") as? Int ?? 120`；`:654 if status == .ready \|\| status == .readyNoAgents,` | 吻合 / 成立：缺省值口径与「仅 ready 计时」准确；关闭时 guard 早退 | 亲核 |
| F-10：「缺席态 bridge 级 status 仍为 ready（management_api.go:462-466：status 只看 agents 配置），计时条件结构上满足」 | `go-bridge/management_api.go:462-466` | `:463 status := "ready"`；`:464-466 if len(s.cfg.Agents) == 0 { status = "ready_no_agents" }` | 吻合 / 成立：status 不含 backend 健康输入，缺席态仍 ready → 若开启则会计时 | 亲核 |
| F-10：「事故 pid 1917 于 10:26:25 启动、18:17 被 kill，7.7h 连续运行、期间无兜底重启」 | 事故档案 §2 时间线 | `10:26:25 bridge runtime 启动（pid 1917…）`…`18:17 止血：kill 1917 → launchd 回收全部僵尸`（全程平均 2142/466min ≈ 4.6/分钟，466min≈7.8h） | 吻合 / 成立：单 pid 连续运行跨 7.7h+，无中途重启记录 | 亲核 |
| F-10：「auto-restart 机制自 84a573ef/2026-06-17 即存在」 | `git log -S autoRestartIntervalMinutes` | `84a573ef 2026-06-17 feat(app): add MacBridge auto-restart safety net` | 吻合 / 成立：机制先于事故存在而未触发，与设置关闭互相印证 | 亲核 |
| F-10：「token exp（P1 观察量级 ~96h）」 | P1 方案 `docs/2026-10-12-codex-remote-probe-economy-p1-plan.md:252` | `exp 距今 **96h56m**…一次性取证、样本已销毁、不可复核…仅证明 exp 存在与量级，禁止当硬 TTL 或常量` | 吻合 / 成立：v4「~96h 量级、非常量」表述与 P1 证据纪律一致 | 亲核 |
| 实施授权：「事故档案状态行（@ 11ae0938）记录 owner 已预授权过审即实施」 | 事故档案 `:3-9` | `P2（缺席语义加固）方案 docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md 评审中，owner 已预授权过审即实施。` | 吻合 / 成立（r3 已核，本轮复读一致） | 亲核 |
| D1 承重：分类器四类文本 + 三消费方注释（`pairing_persist.go:242-251`、`:240-241`） | 同文件 `:238-251` | `:240-241 // Shared by reconnectFromStore, restorePersistedPairing and watchBinding so every restore loop surfaces the same category through the readiness seam.`；`:244 case "请打开并登录 ChatGPT Desktop", "请先安装并登录 ChatGPT Desktop", "ChatGPT 未登录", "读取 ChatGPT 登录态超时":` → `:245 return "请打开并登录 ChatGPT Desktop"`；`:246-247 case "no desktop environment": return "请打开 ChatGPT Desktop"` | 吻合 / 成立：D1 分类升级与「iOS 未安装/未登录共用 banner」依据不变 | 亲核（抽查） |
| D2 承重：watchBinding 循环（E-4/E-9 `:408`、`:418-421`；`:411` Reset） | 同文件 `:405-423` | `:408 err := a.pairing.restoreOnce(ctx)`；`:410-412 if err == nil { backoff.Reset(); continue }`；`:418-421 a.pairing.markOffline(offlineMessageForRestoreError(err)); retryIn := backoff.Next(); slog.Warn("codex-remote pairing reconnect waiting", …); a.sleepInterruptible(retryIn)` | 吻合 / 成立：双循环同规则扩展落点与「成功路径 Reset 不变」主张不变 | 亲核（抽查） |
| E-1/D1 落点：三态骨架与映射分支（`diagnostics.go:10-35`、`:19`、`:28-32`） | `agent/codex-remote/diagnostics.go:10-35` | `:11-13 ReadinessAvailable/PairingRequired/ServiceNotRunning`；`:19 func (a *Agent) StructuredInstanceReadiness() (string, string)`；`:28-32 if paired \|\| … { if snap.Message != "" { return ReadinessServiceNotRunning, snap.Message } … }` | 吻合 / 成立：非 available/pairing_required 分支即 class 映射落点 | 亲核（抽查） |
| E-2：枚举全集与直通（`agent_descriptor.go:20-34`、`:254`、`:260-269`） | 同文件 `:20-34`、`:253-270` | `:22 AgentStatusNotDetected AgentStatus = "not_detected"`、`:23 AgentStatusNotLoggedIn AgentStatus = "not_logged_in"`；`:254 return detectStructuredInstanceReadiness("codex-remote", agent)`；`:266-269 if strings.TrimSpace(status) == "" { return AgentStatusNotConfigured, … } return AgentStatus(status), detail` | 吻合 / 成立：零新增枚举、descriptor 层无需改动主张不变 | 亲核（抽查） |
| E-5：Stat 早退/spawn（`chatgpt_auth.go:55-57`、`:66`） | 同文件 `:53-57`、`:66` | `:55-57 if _, statErr := os.Stat(bin); statErr != nil { return "", "", fmt.Errorf("请先安装并登录 ChatGPT Desktop") }`；`:66 cmd := exec.CommandContext(ctx, bin, …)` | 吻合 / 成立：未安装不 spawn、未登录每轮 spawn 依据不变 | 亲核（抽查） |
| E-7：ticker 先例（`advertised_url_refresh.go:135` + `main.go:502`） | 两文件 | `:135 ticker := time.NewTicker(advertisedURLRefreshInterval)`；main.go `:502 go runAdvertisedLocalURLRefresher(ctx, server, mgmtSrv, *port)` | 吻合 / 成立：watchdog 接线层与先例同层 | 亲核（抽查） |
| backoff 1s→30s 镜像官方（D2 前提） | `agent/codex-remote/backoff.go:16-17` | `:16 reconnectBackoffBase = 1 * time.Second`；`:17 reconnectBackoffCap = 30 * time.Second`（头注释 :4 引 codex-rs `next_reconnect_delay`） | 吻合 / 成立 | 亲核（抽查） |
| D3：`go-bridge/zombie_watchdog.go` 为新文件 | `ls go-bridge/zombie_watchdog.go` | 不存在（No such file or directory） | — / 成立：确为新建，无既有实现被忽略 | 亲核 |

## 3. v4 差异面核验（本轮核心）

### 3.1 方法与限制

v3 字节快照不可得（方案未入 git、无备份）。差异核验采用三方对照：(1) v4 §0.3 自declared
修订范围；(2) r3 报告对 v3 受影响文本的逐处引文（F-10 位置原文）；(3) r2/r3 报告对 v2/v3
设计文本（D1/D2/D3/OD/E/S/R/验收）的详尽记录。对照结论只对「v4 相对 r3 记录的差异面」
主张有效性负责；该限制对本轮结论的影响：若 v3 存在 r2/r3 均未引述、且与 v4 现文不同的
段落，本轮无法感知——但该风险被 r2（全量锚点亲核）+ r3（v3 新增/改写文本全部锚点亲核）
的覆盖密度与 §0.3 声明的逐项落实压到可接受水平，且全局门控重走（§3.4）独立于差异面成立。

### 3.2 §0.3 声明范围逐项核验（全部落实）

| §0.3 声明的改动 | v4 实际文本 | 核验 |
| --- | --- | --- |
| §6 窗口上界改条件化表述 | §6（:330-338）：「实践上界条件化（r3 F-10）：默认配置（autoRestartEnabled 开启，缺省 true/120min…）下受 ~120min 兜底自动重启限制；auto-restart 被关闭时实践上界 = token exp（~96h 量级）——本机当前 autoRestartEnabled=0（owner 显式关闭；事故 pid 1917…7.7h 连续运行…84a573ef/2026-06-17 即存在，与此互相印证），即 S-4 部署目标机上该卸载掩蔽窗口实际可达 ~96h 量级；窗口内行为=现状…非回归；主场景…不受影响」 | **落实**：与 r3 F-10 修订方向逐字对应；条件化后的每条事实主张本轮亲核成立（§2.3 前六行） |
| §0.2 F-7 行「~120min 兜底」字样同步加限定 | §0.2 F-7 行（:43）：「兜底自动重启默认开启、间隔 120min——RuntimeManager.swift:649/:651，**默认值口径；本机实际 autoRestartEnabled=0，r3 F-10 补正**」；处置列「实践上界受 ~120min 自动重启限制（**默认配置口径；条件化表述见 0.3 F-10**）」 | **落实**（历史行保留 + 更正指针，符合 r3 修订方向「如保留字样同步加限定」） |
| D2 覆盖依据段「~120min 兜底」同步加限定 | D2（:246-247）：「缓存过期或 runtime 重启（兜底自动重启，**默认配置 ~120min；实际生效取决于 autoRestartEnabled，本机当前=0——r3 F-10**）后才 Stat 早退进入 notInstalled/120s，窗口上界见 §6 风险」 | **落实** |
| 头部状态行 | :3-13：v4 状态、r3 结论（APPROVED 阻塞 0/建议 1：F-10）、P0/P1 背景、实施授权（S-4 部署时机 owner 拍板） | **落实**（与事故档案状态行、r3 报告一致） |
| §2.1 未跟踪清单 | :152：「未跟踪：本方案 v4、r1/r2/r3 评审报告，均为 docs」 | **落实**（与本轮 `git status` 逐项一致） |
| §6 阻塞清单评审状态同步 r3 结论 | :343-348：「r3 对 v3 快照 APPROVED（阻塞 0 / 建议 1：F-10），v4 仅收口 F-10 表述精度，设计未变；r1 F-1..F-6、r2 F-7..F-9 经 r3 逐条核验闭合。下一阶段入口：实施…」 | **落实**（结论数字与 r3 报告一致；「逐条」措辞精度见 §3.5 记录，不构成意见） |
| 「不涉及其余设计」 | 见 §3.3 | **成立** |

### 3.3 设计未变验证（勘误不引入新设计）

- **D1**（classifyRestoreError/restoreErrClass 四类、lastRestoreClass 单一写入点、fail-closed
  映射、not_logged_in 首发与 Mac UI 可见面）：与 r2 F-8 校准后、r3 复核的文本一致，无变化。
- **D2**（双循环同规则、120s 固定不经 backoff、不推进轮次、absence 特征字段、E-9 覆盖
  依据）：除声明的一处限定外无变化；120s 语义各处（§1/R-2/D2/§2.3/OD-1/S-2/§6）口径一致。
- **D3**（watchdog 采样/判定/armed 行/阈值 64/5min/只观测不处置）：与 r1 F-1 闭合、r2/r3
  复核的文本一致，无变化。
- **OD-1**（decided，降频 120s 工程推荐）：无变化。
- **E-1..E-9 / S-1..S-5 / R-1..R-4 / 验收标准**：与 r2/r3 记录一致；E-9 的窗口上界指向
  §6（已条件化），E-8 仍为实施后完成门；S-5 仍含 CHANGELOG（r1 F-5）。
- **新增机制检查**：无新 E/S/R/OD、无新阈值/新文件/新协议面、无验收口径变化——§0.3
  「设计与验收口径不变」声明成立。

### 3.4 全局门控重走（复用豁免项，本轮重做）

- R→S→验收：R-1→S-1（五态单测含 fail-closed 负例/成功清除断言 + E-3 零 iOS 改动）、
  R-2→S-2（双循环节奏单测 + Warn 间隔对账）、R-3→S-3（判定纯函数边界 + 采样注入 +
  armed 行冒烟）、R-4→全部单测 + S-4 运行态；无悬空引用。
- E 门：Gate A（E-1..E-7、E-9）verified（r2 亲核 + 本轮源码身份复核复用 + 抽查）；
  E-8 正确登记为 S-4 完成门。Gate B：OD-1 decided（事故 §5.3-2「降为低频探测或停轮」
  许可范围内）。
- 非目标与简报逐项一致：零 iOS 改动（E-3 + r2 F-8 校准表述）、零新增 wire 枚举（E-2 +
  protocol pack 复用）、不动其他 backend 就绪面、不停轮（只降频）、不熔断、P1 门控/缓存
  语义不变。
- 无矛盾允许/禁止；回退边界（纯观测/分类/节奏层，revert 即回退）不变。
- 条件化表述未引入新矛盾：E-9/D2 指向 §6 的窗口上界引用闭合；「窗口内行为=现状非回归、
  主场景不受影响」与 E-9 时延限定链一致；~96h 标注为「量级」并保留「非常量」纪律。

### 3.5 过程记录（非意见）

§0.3/§6 称 r1 F-1..F-6「经 r3 逐条核验闭合」——r3 对 r2 三条（F-7..F-9）为逐条新锚点
亲核，对 r1 六条为源码身份复核后维持 r2 的逐条闭合判定（r3 §4.2 自述「身份复核后维持」）。
闭合状态结论本身准确（r2 逐条核验在先、源码身份未变），压缩措辞不影响任何门或 owner
决策，按「不凭措辞偏好制造意见」不立项。

## 4. 通过分支七条件机械校验（本轮逐条自检）

| # | 条件 | 本轮结果 |
| --- | --- | --- |
| 1 | verdict = APPROVED | 满足（本轮结论） |
| 2 | blockers = 0 | 满足（无阻塞） |
| 3 | round_complete = true | 满足（本轮按契约走完并给出 verdict） |
| 4 | scope 前缀 full（全案）/ limited（限定） | 满足：scope = full 前缀——简报【范围】覆盖方案全案（全部组件与零 iOS 主张），本轮执行为定向复核+复用+全局门控重走，APPROVED 覆盖全案无未审部分 |
| 5 | 评审报告存在于指定路径 | 满足：本报告写入 `docs/2026-10-13-codex-remote-absence-hardening-p2-plan-review-r4.md` |
| 6 | 方案哈希一致（评审员读取 = 派发；评审期间未变） | 满足：本轮读取与派发均为 `97a97624…ecf3`；方案本轮只读 |
| 7 | 简报直发（7 字段标记齐全） | 满足：任务简报含【需求】【非目标】【裁决】【授权】【来源】【输出】【范围】七字段（脚本侧校验，本轮核对无缺） |

## 5. 意见

本轮**无新意见**（阻塞 0 / 建议 0）。不编造意见凑数量。

### F-10 处置（r3 建议，本轮核验闭合）

- **闭合**。v4 §6 窗口上界条件化表述与 r3 F-10 修订方向逐字对应，满足其闭合标准「如 §6
  文本先行修订则与之一致」；§0.2 F-7 行与 D2 覆盖依据段同步加限定；条件化后全部事实
  主张本轮亲核成立（含易变的本机 defaults 重读）。设计、切片、验收均未受影响（与 r3
  判定一致：窗口内行为两种上界下均=现状非回归、主场景不受影响、E-9 时延限定结论被强化）。
- 残余义务移交 S-5：实施交付文档（完成报告/事故档案回填/CHANGELOG）中的窗口上界表述
  须保持条件化口径，不再无条件声称 ~120min 实践上界。

### 历轮意见状态

- r1 F-1..F-6、r2 F-7..F-9：r2/r3 逐条核验闭合（源码身份自 r2 未变、方案对应文本自 v3
  除声明勘误外未变——本轮对照确认），**维持闭合**，无重开迹象。
- F-1..F-10 全部闭合，无驳回项、无降级暗置项；v4 §0「全部采纳」与正文实际修订一致。

## 6. 剩余门（与阻塞分开列；通过不等于以下已满足）

1. **S-4 部署时机**：owner 拍板（方案自设的更严门，覆盖「改完即构建覆盖安装」惯例）。
2. **E-8 P1 口径复测**：S-4 完成门——部署后按 `build/p1-reconcile-75543.log` 口径复测
   探针/刷新节奏不劣化（含 P1 在线态观察收口）。
3. **watchdog armed 特征行运行态核验**：S-4 完成门（启动即出，pid/period/childrenThreshold
   字段；超标 WARN 以单测为准、健康部署不出现）。
4. **未安装态运行态对账 N/A 记录义务**：本机 ChatGPT.app 在装，该态以单测为准，运行态
   N/A 须如实记录（S-4 执行时落实）。
5. **实施 kickoff 来源清单再生成**：CLAUDE.md P0 来源门门 2（r2 F-9 闭合标准；本轮确认
   §2.1 在评审时点仍准确，但 kickoff 仍须按当时真实 HEAD 重新生成）。
6. **S-5 文档收口保持条件化口径**：窗口上界表述沿用 v4 条件化文本（F-10 残余义务）。

## 7. 结论复核

- 覆盖足够：v4 差异面逐项核验（§3.2/§3.3）+ F-10 事实链全亲核（含易变项重读）+ 元审核
  级锚点抽查 8 组全部吻合 + 全局门控重走无矛盾 + 七条件逐条自检满足。
- 未发现：勘误引入的新设计、伪造已验证事实、待捕项伪装成事实、必需要求被可选范围卡死、
  与 P0/P1 交付语义冲突、或修订引入的回归。
- 争议项：无。
- 限制披露：v3 字节快照不可得，差异面按 §3.1 三方对照法核验；延续模式未经独立第三方
  重审（差异为勘误级、覆盖密度高，见模式披露）。

**verdict: APPROVED（阻塞 0 / 建议 0）**。v4 为 r3 已审设计的勘误收口版，通过分支七条件
全部满足。按仓内预授权记录（本轮再核），本 APPROVED 即满足实施启动条件：S-1→S-2→S-3
开工，S-4 部署时机 owner 拍板；kickoff 按门 2 重新生成来源清单。
