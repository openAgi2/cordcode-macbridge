# codex-remote 缺席语义加固方案（P2）

> 状态：v4（r3 修订版：收口 r3 建议 F-10——「~120min 实践上界」按本机
> autoRestartEnabled=0 条件化，设计与验收口径不变）。r3 评审对 v3 快照结论
> **APPROVED**（阻塞 0 / 建议 1：F-10）。本文档是事故档案
> `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md` §5.3 P2 的实施方案。
> 前序：P0（三处收尸）已闭环（9365da6c/0c6016ad，对账 53 刷新/0 僵尸）；P1（探针
> 经济性）已实施部署（95cbb3e0，对账 10.5 分钟 1 探针/2 刷新/0 僵尸，在线观察
> 在途）。本方案处理 §6 缺席矩阵暴露的系统性缺口：缺席状态显式化、重试预算、
> runtime 级 zombie watchdog。实施授权：事故档案状态行（@ 11ae0938）记录
> **owner 已预授权过审即实施**，owner 工作流入口原始请求（「实施这个文档里的
> 所有任务」）重申同一授权——过审即具备实施授权；S-4 部署时机仍按 §5 owner
> 拍板。

## 0. 修订记录

### 0.1 r1 → v2（六条意见全部采纳，无驳回项）

评审报告：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan-review-r1.md`
（REVISION_REQUIRED，阻塞 2 / 建议 4）。本轮对六条意见逐条先行源码核实再处置，
**全部采纳，无驳回项**：

| 意见 | 核实结论（亲核） | 处置 |
| --- | --- | --- |
| F-1 阻塞：D3「正常态不打日志」与「首个周期特征日志行」验收矛盾 | 成立（方案内部矛盾：健康部署下无任何特征行可核验） | D3 增 goroutine 启动一次性 armed INFO 特征行；§1/R-4/S-4/§6 四处口径统一为「armed 行核验；超标 WARN 以单测为准」 |
| F-2 阻塞：分级降频未覆盖 watchBinding 第二恢复循环 | 成立（pairing_persist.go:372-423 亲核：`:408` 同一 restoreOnce、`:418-421` 同一 backoff/offline 分类；`:240-241` 注释明示三消费方共享；restoreOnce 消费方共 `:254/:353/:408` 三处） | 采修订方向 (a)：D2/S-2 扩展为两循环同规则；D1 钉死 lastRestoreClass 单一写入点（restoreOnce 出口：失败写/成功清）；新增 E-9 |
| F-3 建议：锚点精度四处 | 逐项成立（`:412` 为 `Output()` 非 CombinedOutput；offlineMessageForRestoreError 实际 242-251、restorePersistedPairing 实际 338-370；事故 §6 矩阵 dsh-web 收尸列事故时标 △（P0-3 已修）；`BackendModels.swift:486-487` 注释属 BackendCodexGoalRecord.status，非 descriptor 通路） | E-6 方法名修正；E-4/§1/§2.2 行号统一；非目标矩阵措辞按实际标记改写；E-3 证据描述按实际机制重写 |
| F-4 建议：lastRestoreClass 成功后不清除 | 成立（成功后断连首窗口按陈旧类别映射；现状同窗口显示陈旧 message「已连接到 ChatGPT Desktop」，非回归） | 并入 D1 单一写入点语义（成功路径清除）；S-1 单测增断言 |
| F-5 建议：S-5 缺 CHANGELOG | 成立（CLAUDE.md 惯例「[Unreleased] 追加」+ 事故 §5.4 P0 完成门先例） | S-5 增补 CHANGELOG [Unreleased] 一项 |
| F-6 建议：notLoggedIn 探针节奏未在 §6 风险明示 | 成立（chatgpt_auth.go:66 Stat 通过即 spawn；pairing.go:108-131 失败不进缓存 → 1-30s 节奏下每轮短命探针） | §6 风险节补记；E-5 断言范围校准（「无进程收益损失」仅 notInstalled） |

本轮修订仅覆盖上述六条意见及其必要一致性维护（行号统一、门引用同步），不涉及
其余设计。

### 0.2 r2 → v3（三条建议全部采纳，无驳回项）

评审报告：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan-review-r2.md`
（APPROVED，阻塞 0 / 建议 3：F-7、F-8、F-9）。本轮对三条建议逐条先行源码核实
再处置，**全部采纳**：

| 意见 | 核实结论（亲核） | 处置 |
| --- | --- | --- |
| F-7 建议：E-9 可达链省略 auth 缓存时延限定 | 成立（pairing.go:114 缓存命中即返回、不查 Stat；restoreOnce pairing_persist.go:308 先 chatGPTAuthCached 后 :319 lookupDesktopEnv；"no desktop environment" 生成点 pairing.go:565-568；缓存仅存成功、有效期仅由 token exp 决定 pairing.go:103-106/:124-130；兜底自动重启默认开启、间隔 120min——RuntimeManager.swift:649/:651，默认值口径；本机实际 autoRestartEnabled=0，r3 F-10 补正） | E-9 补时延限定；D2 覆盖依据段同步；§6 风险补窗口上界一行（理论上界 = token exp、实践上界受 ~120min 自动重启限制（默认配置口径；条件化表述见 0.3 F-10）、窗口内=现状非回归、主场景冷启动不受影响） |
| F-8 建议：「iOS 已能显示的原始字符串」措辞强于实际机制 | 成立（iOS resolve 仅特判 available/pairing_required，其余通用分支显示 descriptor.reason（E-3）；offlineMessageForRestoreError pairing_persist.go:244-245 把未安装/未登录/超时映射为**同一条** banner「请打开并登录 ChatGPT Desktop」；Mac 侧 BridgeStatusView.swift:339-340 对 not_detected/not_logged_in 有独立文案，ProductCopyTests.swift:60-64/:88 与 MacBridgeBehaviorTests.swift:164 有测试覆盖） | §1 非目标、D1、S-1 可见结果三处按评审指定位置校准，另将 OD 表注、§6 R→S 行两处同族表述一并对齐：枚举级区分的可见面 = wire（status 值）+ Mac UI；iOS 显示 reason 文案（未安装/未登录共用 banner） |
| F-9 建议：§2.1 来源清单滞后一个 docs-only 提交 | 成立（HEAD=11ae0938，`git diff 95cbb3e0 HEAD -- '*.go' '*.swift'` 为空——两提交间变更仅 .exec-plan state/事故档案/think.md/P1 完成报告；.exec-plan state 已随 11ae0938 入库、不再未跟踪；当前未跟踪 = 本方案 + r1/r2 评审报告） | §2.1 更新至 11ae0938 并注零代码差异与当前未跟踪状态；§2.2 标题同步；保留实施 kickoff 按 P0 门 2 重新生成清单义务 |

另有一致性校正（超出三条建议文本、属必要维护）：r2 报告 §6 门 1 判「授权未
发出」与仓内记录不一致——事故档案状态行（@ 11ae0938，已入库）明文记录
「owner 已预授权过审即实施」，owner 工作流入口原始请求（「实施这个文档里的
所有任务」）重申同一授权。v2 头部「评审通过 ≠ 实施授权」据此校正为「过审即
具备实施授权（S-4 部署时机仍 owner 拍板）」。

本轮修订仅覆盖上述三条建议及其必要一致性维护（头部状态行、§6 下一阶段入口
同步 r2 结论与预授权记录），不涉及其余设计。

### 0.3 r3 → v4（一条建议采纳，无驳回项）

评审报告：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan-review-r3.md`
（APPROVED，阻塞 0 / 建议 1：F-10；r3 同时逐条核验 r1 F-1..F-6、r2 F-7..F-9
均闭合）。本轮对一条建议先行源码/运行态核实再处置，**采纳**：

| 意见 | 核实结论（亲核） | 处置 |
| --- | --- | --- |
| F-10 建议：「实践上界受 ~120min 兜底自动重启限制」在本部署目标机上不成立（本机 autoRestartEnabled=0，事故 7.7h 单 pid 运行与之一致） | 成立（本机 `defaults read org.openagi.cordcode.link` → `autoRestartEnabled = 0`、`autoRestartIntervalMinutes` 未设——owner 显式关闭兜底重启（CLAUDE.md 亦记「临时关 autoRestartEnabled」为排障手段）；RuntimeManager.swift:649/:651 的 true/120min 仅为缺省值口径；缺席态 bridge 级 status 仍为 ready（management_api.go:462-466：status 只看 agents 配置），计时条件结构上满足，但事故档案 §2：pid 1917 于 10:26:25 启动、18:17 被 kill，7.7h 连续运行、期间无兜底重启，而 auto-restart 机制自 84a573ef（2026-06-17）即存在——与设置关闭互相印证：本机实践上界不是 ~120min，而是 token exp（P1 观察量级 ~96h）） | §6 窗口上界改条件化表述（默认配置上界 ~120min 需 autoRestartEnabled 开启；关闭时实践上界 = token exp——本机当前 autoRestartEnabled=0，即 S-4 部署目标机实际口径）；§0.2 F-7 行与 D2 覆盖依据段的「~120min 兜底」字样同步加限定 |

F-10 属 r2 F-7 建议措辞自身携带的精度缺口（v3 采纳时未核本机实际设置；r3
判定不触及设计与验收——窗口内行为两种上界下都=现状非回归、主场景不受影响、
E-9 时延限定结论反而被强化）。r3 原判 F-10 可留待实施文档收口、不要求再送
评审；本修订按其闭合标准「如 §6 文本先行修订则与之一致」在方案文本先行
落实。本轮修订仅覆盖该条意见及其必要一致性维护（头部状态行、§2.1 未跟踪
清单、§6 阻塞清单评审状态同步 r3 结论），不涉及其余设计。

## 1. 摘要与范围

**现状问题**（事故档案 §5.3 + 本轮差距分析）：

1. **缺席状态折叠**：codex-remote 的 `StructuredInstanceReadiness`
   （diagnostics.go:19-35）已有三态（available / pairing_required /
   service_not_running），但「ChatGPT.app 未安装」「已登录但 Desktop 未开」
   「已安装未登录」三种缺席原因全部折叠进 `service_not_running` 的 detail
   文本（offlineMessageForRestoreError，pairing_persist.go:242-251）——
   状态机语义缺位正是事故文档的原始表述；本轮核实：**骨架已在、分类未接**。
2. **重试无预算**：两个恢复循环——`restorePersistedPairing`
   （pairing_persist.go:338-370）与 `watchBinding`（:372-423，r1 F-2/E-9）——
   对所有失败一视同仁（backoff 1s→30s 永续）。P1 后未安装态虽不再泄进程
   （Stat 早退不 spawn），但恢复循环仍以 1~30s 节奏永续空转、日志常刷
   （事故 §5.3-2 点名的浪费形态）。
3. **无 zombie watchdog**：runtime 无自身子进程健康自检；本次事故若有
   watchdog，2142 个僵尸在首个采样点就会告警，而不是 7.7 小时后整机 EAGAIN
   才被发现（事故 §5.3-3）。

**目标行为**：

- (a) codex-remote 就绪五态：`available` / `pairing_required` /
  `not_detected`（ChatGPT.app 未安装，对齐 claude/grok 的 CLI 缺席语义）/
  `not_logged_in`（探针明确非 chatgpt 登录态） / `service_not_running`
  （已装已登录但 Desktop 离线）——**全部复用既有 wire 枚举**
  （agent_descriptor.go:20-34），零新增枚举。
- (b) 恢复循环错误分级重试（**两个恢复循环同规则**，r1 F-2）：永久类
  （未安装）降频至 120s 固定间隔；暂时类维持既有 1s→30s backoff（已镜像
  官方 `next_reconnect_delay`）。
- (c) runtime zombie watchdog：周期自检自身名下子进程/僵尸数，超阈值 WARN
  （报警 + 长期回归证据）；启动时一次性 armed 特征 INFO 行作为部署后运行态
  核验锚点（对齐 CLAUDE.md 部署后运行态验证第 2 条，r1 F-1）。

**非目标**：

- 不改 iOS 仓任何代码：iOS 对未知 status 走通用不可用分支安全显示 reason
  文案（证据 §4 E-3；未安装/未登录共用同一 banner，pairing_persist.go:244-245
  ——iPhone 用户可见文案不区分该两类，枚举级区分的可见面是 wire（status 值）
  与 Mac UI（BackendStatusText 独立文案，BridgeStatusView.swift:339-340），
  r2 F-8 校准）；不新增 wire 枚举；不改 protocol pack schema。
- 不动 dsh-web / opencode-web / grok / claude 的就绪面（已达标：dsh-web
  四态 readiness.go、opencode-web 三态 readiness.go、CLI 型 LookPath
  干净报 not_detected——事故 §6 矩阵未安装/未打开列所记行为亲核成立；
  dsh-web 收尸列事故时标 △，已由 P0-3（0c6016ad reaper）修复，非本方案
  范围）。
- 不引入「停轮」（永久类只降频不停：用户可能中途安装 ChatGPT.app，
  停轮需要外部事件唤醒，降频是无需新机制的安全近似；见 OD-1）。
- 不做 backend 级进程数配额/熔断（watchdog 只观测不处置——处置属新语义，
  越出事故档案 P2 范围）。
- P1 已交付的刷新门控/auth 缓存语义不变。

**需求编号**：

- **R-1** codex-remote 就绪输出区分五种缺席/可用状态（枚举级，非文本级），
  且不折叠探针失败为 not_configured（对齐 opencode-web fail-closed 先例）。
- **R-2** 未安装态恢复循环降频（120s），暂时类维持 1s→30s；两类节奏都有
  日志特征可对账。
- **R-3** runtime 周期自检僵尸/子进程数，超阈值 WARN（特征输出），不处置。
- **R-4** 上述全部有单测；watchdog 特征输出（启动 armed 行）在部署后
  运行态核验（事故 §5.4 P2 完成门）。

**验收标准**（详见 §6）：定向测试绿；Release 覆盖安装后：watchdog armed
一次性特征 INFO 行（pid/period/childrenThreshold 字段）出现在启动日志——
超标 WARN 属异常态路径，健康部署不出现，判定以单测为准并如实记录；未安装态
（若可构造）恢复循环节奏 ≥120s；既有 backoff/门控/缓存行为零回归（P1 对账
口径复测不劣化）。

## 2. 来源与现状调查

### 2.1 来源清单（方案阶段，P0 来源门）

```text
本仓仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
本仓分支=feat/ios-native-message-timeline
本仓提交=11ae09386c5dfc926127629ed3014563d1fdf116（docs-only 提交：事故档案
  状态行/§5.6 回填、think.md、P1 完成报告、.exec-plan state 入库；相对锚点
  基线 95cbb3e0bb4716df88c7482d57ca1f94d2470380 零代码差异——
  git diff 95cbb3e0 HEAD -- '*.go' '*.swift' 为空，§2.2/§4 全部代码锚点在
  HEAD 逐字节有效，r2 F-9）
本仓未提交状态=干净（未跟踪：本方案 v4、r1/r2/r3 评审报告，均为 docs）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline（只读证据：status 解码兜底）
配套分支=feat/ios-native-message-timeline
配套提交=507a1e23b8c38847f1433921dbe65557965a52e8
配套未提交状态=脏（porcelain 277 行：iOS timeline 任务在途工作，属该任务
  agent/owner；本方案只读其 3 个 swift 文件（BackendModels.swift 等），不修改）
上游仓库=不适用（P2 三项均为 CordCode 私有 bridge 运维语义：官方 codex
  transport 无「宿主未安装自身」概念——它就是被安装的那个；缺席分类与
  watchdog 无官方对应物可镜像。既有镜像义务的 backoff/刷新语义 P1 已对齐
  且本方案不改）
任务预期分支=纯 Mac 侧；无 iOS 代码改动
预期产品特性=cordcode-bridge-runtime（drivers 不变；新增 watchdog 观测）
```

实施 kickoff 时按 CLAUDE.md P0 来源门（第一次修改文件前）用当时真实 HEAD 与
未跟踪状态重新生成本清单（r2 F-9 闭合标准；本快照清单仅证明方案阶段来源）。

### 2.2 本仓现状锚点（@ 95cbb3e0；HEAD 11ae0938 零代码差异，r2 F-9）

| 现状 | 锚点 | 说明 |
| --- | --- | --- |
| codex-remote 就绪骨架 | `agent/codex-remote/diagnostics.go:10-35` | 三态已有（available/pairing_required/service_not_running）；错误原因只在 detail 文本 |
| 错误分类已有雏形 | `pairing_persist.go:242-251`（offlineMessageForRestoreError，r1 F-3 行号校正） | 文本映射四类缺席原因（未安装/未登录/超时/Desktop 离线）——分类逻辑存在，只输出文本 |
| 恢复循环 ×2 | `pairing_persist.go:338-370`（restorePersistedPairing）、`:372-423`（watchBinding，r1 F-2/E-9） | 两循环均消费 restoreOnce 错误（消费方共三处：`:254` reconnectFromStore 单次调用、`:353`/`:408` 两循环）、全错误同 backoff 1~30s 永续；P1 已接缓存+门控；`:240-241` 注释明示三消费方共享 offline 分类 |
| wire 枚举全集 | `go-bridge/agent_descriptor.go:20-34` | not_logged_in / not_detected 均已定义；not_logged_in 当前无发射方 |
| descriptor 消费 | `agent_descriptor.go:254`（codex-remote → detectStructuredInstanceReadiness）、`:260-269` | 任意非空 status 字符串直通 wire——**枚举级扩展无需改 descriptor 层** |
| 就绪先例 | `agent/opencode-web/readiness.go:36-73`（fail-closed 判别序列）、`agent/dsh-web/readiness.go:37-91`（四态） | 状态语义、fail-closed、描述符只读 seam 的既定形态 |
| 周期任务先例 | `go-bridge/advertised_url_refresh.go:135`（ticker goroutine） | watchdog 的落点模式 |
| 事故证据 | `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md` §3/§5.3/§6 | 2142 僵尸 7.7h 无告警；缺席矩阵 |

### 2.3 复用调查表

| 能力/需求 | 已有实现与锚点 | 选择 | 理由 |
| --- | --- | --- | --- |
| 就绪状态机 | opencode-web/dsh-web readiness.go 先例 + codex-remote 三态骨架 | 扩展 | 只需把 offlineMessageForRestoreError 的分类升级为枚举并接入 StructuredInstanceReadiness |
| wire 枚举 | AgentStatus 全集（agent_descriptor.go:20-34） | 复用 | not_detected/not_logged_in 已定义未用；零新增枚举 = 零 iOS 改动 |
| 错误分类 | offlineMessageForRestoreError | 扩展 | 返回 (class, message) 而非仅 message；单一分类源 |
| 重试节奏 | backoff.go（已镜像官方） | 扩展 | 两个恢复循环内按 class 分流：permanent → 120s 固定；transient → 既有 backoff.Next() |
| 周期自检 | advertised_url_refresh.go ticker 模式 | 镜像形态新建 | zombie 统计是 go-bridge 私有；注入 seam 保证可测 |
| 进程表读取 | `ps -axo ppid=,stat=`（P0/P1 对账已用）；Go 侧 `syscall` 无直接子进程枚举 | 复用 | watchdog 采样经 exec ps（同步 `Output()` 收尸合规，事故 §3.5 表）；阈值判定注入可测 |

## 3. 设计与决定

**D1 codex-remote 就绪五态（枚举级分类接入）**

- `offlineMessageForRestoreError` 升级为
  `classifyRestoreError(err) (restoreErrClass, string)`，class ∈
  {errClassNotInstalled, errClassNotLoggedIn, errClassDesktopOffline,
  errClassTransient}（未安装 = os.Stat 失败路径错误文本「请先安装并登录
  ChatGPT Desktop」；未登录 = 「ChatGPT 未登录」；Desktop 离线 =
  "no desktop environment"；其余 = transient）。文本映射保持现状
  （offlineMessageForRestoreError 继续存在作 view 层）。
- `PairingController` 增 `lastRestoreClass`（p.mu 下）。**写入点单一且明确
  （r1 F-2/F-4）**：在 `restoreOnce` 出口统一维护——失败返回路径写入
  `classifyRestoreError` 的 class，成功返回路径（含 client 已连接早退）清除；
  三个消费方（reconnectFromStore / restorePersistedPairing / watchBinding，
  `pairing_persist.go:240-241` 注释明示的共享不变量）自动共享，无需各自维护。
  成功清除消除「成功后断连首窗口按陈旧类别显示」——如冷启动缺席期写入的
  notInstalled 在 app 已装后仍误标 not_detected（最长 ~45s，即下一次
  restoreOnce 失败覆盖前）；现状同窗口本就显示陈旧 message（「已连接到
  ChatGPT Desktop」），非回归，清除后该窗口 fail-closed。
  `Agent.StructuredInstanceReadiness`（diagnostics.go:19）在非
  available/pairing_required 分支按 class 映射：
  not_installed→`not_detected`、not_logged_in→`not_logged_in`、
  desktop_offline/transient→`service_not_running`（保持现值）。fail-closed：
  class 未知或已清除时维持 service_not_running（不折叠为
  not_configured——opencode-web 先例红线）。
- `not_logged_in` 是既有枚举的首次发射：iOS 端走通用不可用分支显示 reason
  文案（E-3 证据，无崩溃/丢弃路径），无需 iOS 改动；Mac 端
  `BackendStatusText.display` 对 not_detected/not_logged_in 已有独立文案与
  测试（BridgeStatusView.swift:339-340、ProductCopyTests.swift:60-64、
  MacBridgeBehaviorTests.swift:164）——枚举区分的可见面在 wire 与 Mac UI
  （r2 F-8 校准）；protocol pack 不改（schema 无新值）。

**D2 恢复循环重试预算（分级降频，两个恢复循环同规则——r1 F-2，采评审修订
方向 (a)）**

`restorePersistedPairing`（pairing_persist.go:338-370）与 `watchBinding`
（:372-423）的 restore 失败分支同规则：`classifyRestoreError` 为 notInstalled
→ `a.sleepInterruptible(120s)`（固定，不经 backoff——未安装是环境事实，不是
抖动，指数退避无意义；分片睡眠保证 Stop 后 ≤2s 返回）；其余 → 既有
`backoff.Next()`（watchBinding 成功路径既有 `backoff.Reset()` 不变；
notInstalled 轮不推进 backoff 轮次，类别翻转回暂时类后按既有语义继续）。两处
等待 Warn 均补 class 特征字段 `absence`（notInstalled 轮 `absence=
not_installed`）——节奏可对账（两条 Warn 间隔 ≈120s vs 暂时类 ≤30s）。安装后
首个 120s 周期内自动恢复探测（OD-1 的降频决定对两循环同样成立——停轮风险面
论证与循环无关）。

覆盖 watchBinding 的依据（E-9）：它与 restorePersistedPairing 共用同一
restoreOnce 错误全集（含 Stat 早退的 notInstalled），可达链 = 配对成功运行中
→ 卸载 ChatGPT.app → 流断 → watchBinding 重试（时延限定，r2 F-7：卸载落在
auth 缓存有效期内时，重试先经 desktopOffline 表现——chatGPTAuthCached 缓存
命中跳过 Stat（pairing.go:114），restoreOnce 直接走到 lookupDesktopEnv 失败
"no desktop environment"（pairing_persist.go:319、pairing.go:565-568）；缓存
过期或 runtime 重启（兜底自动重启，默认配置 ~120min；实际生效取决于
autoRestartEnabled，本机当前=0——r3 F-10）后才 Stat 早退进入 notInstalled/120s，
窗口上界见 §6 风险）。若只降频前者，该路径仍 1~30s
永续空转刷日志（§1-2 要消除的形态），且与 D1 共享类别后出现「显示
not_detected 却按暂时类节奏刷」的自相矛盾。

**D3 runtime zombie watchdog（只观测不处置）**

新文件 `go-bridge/zombie_watchdog.go`：

- 采样：`ps -axo ppid=,stat=` 过滤本 pid 的直接子进程；统计
  {children, zombies}（exec 同步 `Output()`，事故 §3.5 收尸合规表）。
- 判定：zombies > 0（任一即报——僵尸本不该存在，P0 后恒 0，出现即异常）或
  children > 64（远超正常驱动拓扑：grok catalog 1 + 每 session CLI 若干；
  阈值宽松防误报）→ `slog.Warn("go-bridge zombie watchdog", "children", n,
  "zombies", z, "pid", os.Getpid())`——报警与长期回归证据（超标态路径；
  部署核验锚点是下条 armed 行，非此 WARN）。
- 周期 5min ticker，随 runtime 启动（与 advertised_url_refresh 同层）。
  **启动特征行（r1 F-1）**：goroutine 启动时打一条一次性 INFO——
  `slog.Info("go-bridge zombie watchdog armed", "pid", os.Getpid(),
  "period", "5m", "childrenThreshold", 64)`——每 runtime 生命周期仅一条、
  零常驻噪音，字段文案为本提交独有，作为部署后运行态核验的特征输出
  （CLAUDE.md 部署后运行态验证第 2 条「提交独有字段/行为」；不依赖首周期
  tick，启动即出）。采样期正常态仍**不打日志**（避免常驻噪音），仅超标
  告警；连续超标每周期一条（告警不节流，回归证据密度优先）。超标 WARN 属
  异常态路径，健康部署不出现——其判定以单测为准（S-3），部署核验以 armed
  行为准（S-4）。
- 可测性：采样函数与判定函数分离，判定纯函数单测；采样注入 fake。

**OD 表**：

| ID | 问题 | 选项 | 推荐 | 状态 |
| --- | --- | --- | --- | --- |
| OD-1 | 未安装态停轮还是降频？ | 停轮（需外部唤醒机制，安装事件无 hook）/ 降频 120s | 降频：无需新机制，安装后 ≤120s 自愈；停轮引入「永不恢复」风险面 | decided（工程推荐，非产品取舍；如 owner 有停轮偏好可改） |

无产品语义决定（五态均为既有枚举的枚举级细化，iOS 显示 reason 文案无需改动；
watchdog 只加日志不加行为）。

## 4. 证据与实施前置门

| ID | 命题 | 证据类别/来源 | 断言与负例 | 状态 | 未满足时阻塞 |
| --- | --- | --- | --- | --- | --- |
| E-1 | codex-remote 三态就绪骨架已存在 | 源码 @ 95cbb3e0 diagnostics.go:10-35 | available/pairing_required/service_not_running | verified | D1 |
| E-2 | not_detected/not_logged_in 为既有 wire 枚举、descriptor 层任意非空 status 直通 | agent_descriptor.go:20-34, 260-269 | 无新枚举即可五态 | verified | D1 |
| E-3 | iOS 对未知 status 走通用不可用分支安全显示（raw String 解码，无 enum 强转） | 源码 @ iOS 507a1e23 BackendModels.swift:25-38（:25 guard status=="available"；:26 codexRemote+pairing_required 特判；:31-37 其余走通用不可用分支，显示 descriptor.reason）、CCCodeBridgeModels.swift:188-193（:192 descriptor status 为 raw String 解码） | not_logged_in/not_detected 在 iOS 走通用不可用分支显示 reason 文案，无崩溃/丢弃路径；负例：iOS 全仓无 descriptor not_logged_in/not_detected 特判（不依赖）。注（r1 F-3 校准）：BackendModels.swift:486-487 的 raw value 注释属 BackendCodexGoalRecord.status（goal 记录字段），非 descriptor 通路，不作为本证据 | verified | D1（零 iOS 改动主张） |
| E-4 | 两个恢复循环全错误同 backoff | pairing_persist.go:338-370（restorePersistedPairing）、:372-423（watchBinding :418-421 同一 backoff.Next()） | 任一循环未安装也 1~30s 永续（watchBinding 经 :408 同一 restoreOnce 错误全集可达 notInstalled） | verified | D2 |
| E-5 | 未安装态不 spawn（Stat 早退）；未登录态仍每轮 spawn | chatgpt_auth.go:55-57（Stat 早退）、:66（Stat 通过后 exec 探针）、pairing.go:108-131（失败不进缓存） | notInstalled 降频纯为省日志/空转（无进程收益损失）；notLoggedIn 探针每轮真实 spawn、节奏维持 1-30s（接受行为，见 §6 风险，r1 F-6） | verified | D2 |
| E-6 | ps 采样收尸合规（同步 Output） | 事故 §3.5 表；agent_descriptor.go:412 `Output()`（detectCodexAppServerProcess 的 ps 采样；`CombinedOutput()` 同类先例 :435/:299/:322，r1 F-3 方法名校正） | watchdog 采样不引入新僵尸 | verified | D3 |
| E-7 | 周期任务先例形态 | advertised_url_refresh.go:135 | ticker goroutine 随 runtime 起 | verified | D3 |
| E-8 | P1 行为不因 P2 改动回归 | P1 对账口径（build/p1-reconcile-75543.log） | 部署后复测探针/刷新节奏不劣化 | 需实施后验证 | S-4 完成门 |
| E-9 | watchBinding 与 restorePersistedPairing 共用 restoreOnce 错误全集与 offline 分类（r1 F-2） | pairing_persist.go:372-423（:408 restoreOnce、:418-421 同一 backoff/offlineMessageForRestoreError）、:240-241（注释明示 reconnectFromStore/restorePersistedPairing/watchBinding 三消费方共享） | notInstalled 经 watchBinding 可达（配对成功→卸载 ChatGPT.app→流断→重试→Stat 早退）；时延限定（r2 F-7）：卸载落在 auth 缓存有效期内时先经 desktopOffline 表现（缓存命中跳过 Stat，pairing.go:114），缓存过期或 runtime 重启后才进入 notInstalled——窗口上界与主场景豁免见 §6 风险；restoreOnce 消费方共三处（:254/:353/:408），reconnectFromStore 为单次调用无循环 | verified | D2 |

Gate A：E-1..E-7、E-9 verified。Gate B：OD-1 已决定（工程推荐）。**实施前置无阻塞**。

## 5. 实施切片

| 切片 | 可见结果 | 组件与复用 | 依赖/前置 | 最小验证/完成证据 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-1 就绪五态 | 枚举级缺席区分在 wire 与 Mac UI 行可见（Mac 独立文案，BridgeStatusView.swift:339-340）；iOS 行显示 reason 文案（未安装/未登录共用 banner，E-3）——零 iOS 改动（r2 F-8 校准） | classifyRestoreError（扩展 offlineMessageForRestoreError）、lastRestoreClass（restoreOnce 出口单一写入点：失败写/成功清，r1 F-2/F-4）、StructuredInstanceReadiness 映射 | E-1/E-2/E-3 | 单测：五态映射（含 class 未知/已清除 fail-closed→service_not_running、探针超时→transient、成功后类别清除——断连首窗口不再按陈旧类别显示）；readiness 快照在 restore 失败后即时反映 | 映射缺陷 → 回退三态（仅文本差异回归） |
| S-2 重试预算 | 未安装态 Warn 间隔 ≈120s（absence 特征字段）；暂时类节奏不变；两个恢复循环同规则 | restorePersistedPairing + watchBinding 失败分支分流 + sleepInterruptible(120s) | E-4/E-5/E-9；S-1（共用 classifier） | 单测：notInstalled→120s 不走 backoff；transient→backoff.Next()；watchBinding 失败分支同规则（notInstalled→120s / transient→backoff.Next()，r1 F-2）；安装恢复（错误类型翻转后下轮走 backoff） | 分流错误 → 退回统一 backoff（现状） |
| S-3 zombie watchdog | 启动 armed 特征 INFO 行（一次性）；超阈值 WARN；采样期正常静默 | go-bridge/zombie_watchdog.go（ticker + 采样/判定分离 + 启动 armed 行，r1 F-1） | E-6/E-7 | 单测：判定纯函数（0 僵尸/1 僵尸/children 阈值边界）；采样注入 fake；启动接线冒烟（含 armed 特征行输出） | watchdog 自身故障只 Warn 自身失败，不影响 runtime（只观测） |
| S-4 部署验证 | 正式版含 P2；watchdog armed 特征行可核验 | Release→覆盖安装→重启（时机 owner 拍板） | S-1..S-3 测试绿；`go test ./agent/codex-remote/... ./go-bridge/... -count=1` | 五项运行态核验 + watchdog armed 特征行核验（启动即出，不依赖首周期 tick）+ P1 口径复测（探针/刷新节奏不劣化）+ 未安装态节奏对账（如可构造） | 部署失败保留现场 |
| S-5 文档收口 | 事故档案 P2 状态回填、think.md、完成报告、CHANGELOG [Unreleased]（r1 F-5：五态就绪/重试节奏/watchdog 均为用户与运维可见变化） | D0 | S-4 | git diff --check；状态与对账一致 | — |

## 6. 验证、风险与交付

**R→S→验收**：R-1→S-1（五态单测 + 零 iOS 改动证据 E-3：iOS 通用分支安全
显示 reason 文案）；R-2→S-2（节奏单测+运行态 Warn 间隔对账）；R-3→S-3（判定
单测+部署后 armed 特征行）；R-4→全部单测 + S-4 运行态。

**方案阶段已验证**：E-1..E-7、E-9（本仓 + iOS 只读源码亲核）。**需实施后
验证**：E-8（P1 口径复测）、watchdog armed 特征行（运行态；超标 WARN 判定以
单测为准——健康部署不出现）、五态真实分布（未安装态本机难构造——
ChatGPT.app 在装；该态以单测为准，运行态验证 N/A 并如实记录）。

**风险**：watchdog 采样本身 exec ps 每周期一次（5min 一次，成本可忽略；
采样失败只 Warn 不 panic）；not_logged_in 首次发射若 iOS 存在未知值崩溃路径
（E-3 证据已排除：raw String 解码，无 enum 强转）；未登录态（notLoggedIn）
维持 1-30s 探针节奏——Stat 通过、每轮真实 spawn 一个短命探针（auth 缓存只存
成功、失败不进缓存，pairing.go:108-131），P0 后每轮均收尸、无累积，属任务
简报钉死的接受行为，E-5 的「无进程收益损失」仅对 notInstalled 类成立
（r1 F-6）；120s 窗口内安装后恢复延迟（接受：≤120s，远好于现状的永不降频
噪音）；auth 缓存有效期内的卸载窗口（r2 F-7）：notInstalled 检测与 120s 降频
延迟到缓存失效——缓存有效期仅由当场解析的 token exp 决定（pairing.go:103-106，
无硬编码 TTL；P1 一次观察量级 ~96h，非常量），实践上界条件化（r3 F-10）：
默认配置（autoRestartEnabled 开启，缺省 true/120min，RuntimeManager.swift:649/:651；
重启清空内存缓存）下受 ~120min 兜底自动重启限制；auto-restart 被关闭时实践
上界 = token exp（~96h 量级）——本机当前 autoRestartEnabled=0（owner 显式关闭；
事故 pid 1917 于 10:26:25 启动、18:17 被 kill，7.7h 连续运行、期间无兜底重启，
auto-restart 机制自 84a573ef/2026-06-17 即存在，与此互相印证），即 S-4 部署
目标机上该卸载掩蔽窗口实际可达 ~96h 量级；窗口内
行为=现状（desktopOffline 暂时类 1-30s + service_not_running），非回归；主
场景（冷启动/无缓存未安装）不受影响——缓存为空，首轮即 Stat 早退。

**回退边界**：全部为观测/分类/节奏层，不改持久化 schema、不改协议、不改
进程生命周期；回退 = revert。

**阻塞清单**：无。评审状态：r3 对 v3 快照 APPROVED（阻塞 0 / 建议 1：F-10），
v4 仅收口 F-10 表述精度，设计未变；r1 F-1..F-6、r2 F-7..F-9 经 r3 逐条核验
闭合。下一阶段入口：实施——事故档案状态行（@ 11ae0938）记录 owner 已预授权
「过审即实施」，owner 工作流入口原始请求重申实施事故文档全部任务（r3 独立
核验一致：该 APPROVED 即满足实施启动条件）；S-4 部署时机仍 owner 拍板；kickoff
按 P0 来源门（门 2）重新生成来源清单（r2 F-9）。
