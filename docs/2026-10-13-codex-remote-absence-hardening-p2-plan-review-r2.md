# codex-remote 缺席语义加固方案（P2）评审报告 r2

- 评审轮次：第 2 轮（复审；r1 结论 REVISION_REQUIRED 阻塞 2 / 建议 4）
- 评审模式披露：延续模式（r1 报告全文读入作回归清单）。本轮未把 r1 的"亲核/成立"
  直接当通过依据：全部承重锚点（E-1..E-7、E-9 涉及的每一处源码行）本轮逐一重新
  亲核，另完成 r1 未覆盖的四项新检查（Mac Swift 状态消费、protocol pack 状态枚举、
  ps 采样格式、分类器三类错误文本的生成点溯源）。r1 报告仅用于意见闭合对照。
- 评审技能/契约：plan-review（契约 plan-contract-v1.1）

## 1. 结论

- **verdict: APPROVED**
- blockers: **0**
- advisories: **3**（F-7、F-8、F-9，均为透明度/精度级，不影响设计与验收可行性）
- 方案：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md`（v2，未跟踪新增文件）
- 方案 SHA-256：`da7dc44cc319481f7711fc9903a1291f1090860434bd43d7081d7cdbe67da551`
  （与派发 SHA 一致，本轮评审对象即该快照）
- 本轮范围：简报定向范围（agent/codex-remote 错误分类器/就绪五态/循环节奏 +
  go-bridge zombie_watchdog/启动接线 + 定向单测 + Release 部署验证含 E-8 复测 +
  零 iOS 改动主张的 iOS 只读证据）。实际执行为全量锚点亲核（见 §2.2）。

**总体判断**：r1 六条意见（F-1..F-6）全部闭合且无驳回项——两条阻塞（F-1 特征行
矛盾、F-2 watchBinding 覆盖）的修订均落到正确的源码事实上，四处口径（§1/R-4/
D3/S-3/S-4/§6）现已互洽；E-9 新证据的每处锚点（:372-423、:408、:418-421、
:240-241、:254/:353/:408）本轮逐行核verified。修订未引入回归。本轮独立新查的
四项（Mac Swift 对 not_logged_in 的既有映射、protocol pack 无封闭状态枚举、
`ps -axo ppid=,stat=` 可用性、三类缺席错误文本的生成点）全部通过，其中 Mac 侧
`BackendStatusText.display` 已把 `not_logged_in` 映射为独立文案并有测试覆盖，
"Mac 行可见枚举级缺席区分"主张成立。三条新建议均为措辞/登记精度问题，不阻塞。

**implementation_readiness**：

- 设计通过；**无已授权可开工切片**——实施入口仍是「方案过审 + owner 授权」，
  授权未发出（评审通过 ≠ 实施授权）。用户在工作流入口的原始请求「实施这个文档里
  的所有任务」显示实施意向，待 owner 在本过审版本上确认范围与时机。
- 授权后可开工顺序：S-1 → S-2（依赖 S-1 分类器）→ S-3（独立）→ S-4（测试绿 +
  owner 拍板部署时机）→ S-5。E-8 为 S-4 完成门（实施后验证，非前置）。
- 实施 kickoff 时按 CLAUDE.md P0 来源门重新生成 §2.1 清单（见 F-9：本快照清单
  已滞后一个 docs-only 提交）。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 备注 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | HEAD=`11ae09386c5dfc926127629ed3014563d1fdf116`；方案锚点基线=`95cbb3e0bb4716df88c7482d57ca1f94d2470380` | 干净；未跟踪：方案 v2、r1 评审报告（本轮新增 r2 报告） | `git diff 95cbb3e0 HEAD -- '*.go' '*.swift'` 为空——两提交间零代码改动，方案全部代码锚点在 HEAD 逐字节有效。差异见 F-9 |
| iOS（只读） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `507a1e23b8c38847f1433921dbe65557965a52e8` | 脏（porcelain 277 行，本轮复核 `wc -l`=277，与方案 §2.1 一致；iOS timeline 在途任务） | E-3 证据来源；只读 3 个 swift 文件 |
| 上游 | 不适用 | — | — | — | 方案 §2.1 论证 P2 三项为 bridge 私有运维语义；r1 已复核，本轮无新证据推翻（官方 codex transport 无「宿主未安装自身」概念；D2 的 backoff 镜像义务属 P1 已交付且本方案不改） |

运行证据档案（存在性复核）：`build/p1-reconcile-75543.log`、
`build/p1-reconcile-75543-offline-extended.log` 均在（E-8 口径来源有效）。

### 2.2 亲核范围与 N/A

- **本轮亲核**（全部为重新核对，非复用 r1）：
  - `agent/codex-remote/`：pairing_persist.go（offlineMessageForRestoreError
    :242-251、三消费方注释 :240-241、restoreOnce :263-336 全部返回路径、
    restorePersistedPairing :338-370、watchBinding :372-423、reconnectFromStore
    :253-261）、diagnostics.go:10-43、chatgpt_auth.go:40-129（Stat 早退/探针
    spawn/未登录/超时四路径）、pairing.go:100-139（auth 缓存只存成功）+
    :531-570（lookupDesktopEnv 与 "no desktop environment" 生成点）+ :641、
    backoff.go 全文（1s/30s、Next/Reset、sleepInterruptible 分片）。
  - `go-bridge/`：agent_descriptor.go:20-34（枚举全集）、:253-254（codex-remote
    接线）、:260-270（直通）、:293-322（claude/grok LookPath→not_detected、
    CombinedOutput :299/:322）、:408-422（ps+`Output()` :412）、:424-435
    （CombinedOutput :435）、advertised_url_refresh.go:128-151（:135 ticker）、
    main.go:502（goroutine 接线）。
  - `agent/opencode-web/readiness.go:33-50`（三态常量 + fail-closed 注释）、
    `agent/dsh-web/readiness.go:35-45`（四态常量）。
  - iOS（只读）：BackendModels.swift:15-40（resolve 分支）、:483-492（goal
    record 注释归属）、CCCodeBridgeModels.swift:188-201（raw String 解码）；
    iOS 全仓 grep `not_logged_in|notDetected`（负例，见锚点表）。
  - Mac Swift（本轮新增，r1 未查）：BridgeStatusView.swift:320-348
    （nextStepGuidance + BackendStatusText.display 全表）、
    ProductCopyTests.swift:63-100、MacBridgeBehaviorTests.swift:164。
  - 事故档案 §5.3/§5.4/§5.6/§6（P2 三项、完成门、P1 对账、缺席矩阵含 dsh-web
    △ 行 :374）、:50/:82（2142 僵尸/7.7h）。
  - 环境事实：`ps -axo ppid=,stat=` 在本机输出正常（D3 采样格式可行）；
    `go-bridge/zombie_watchdog.go` 尚不存在（确为新文件）；`docs/protocol/`
    无 `backends[].status` 封闭枚举（bridge-v1-schema.md:227 的 `agent.*` 是
    错误码命名空间，非 descriptor 状态）。
- **未核/不适用**：未运行测试或构建（方案阶段只读）；未核 iOS `output/` 下
  baseline 副本（非 E-3 来源）；未核 `session_catalog.go:350`（E-6 未引用）。

### 2.3 锚点核验表（本轮亲核；原引用保持原样）

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 本轮依据 |
| --- | --- | --- | --- | --- |
| E-1 三态骨架 `diagnostics.go:10-35` | `agent/codex-remote/diagnostics.go:10-35` | `:11-13 ReadinessAvailable="available" / ReadinessPairingRequired="pairing_required" / ReadinessServiceNotRunning="service_not_running"`；`:19 func (a *Agent) StructuredInstanceReadiness() (string, string)`；`:28-32 if paired \|\| (a.pairing != nil && a.pairing.hasPersistedIdentity()) { if snap.Message != "" { return ReadinessServiceNotRunning, snap.Message } ... }` | 吻合 / 成立：非 available/pairing_required 分支即 :28-32，D1 的 class 映射落点存在 | 亲核 |
| E-2 枚举全集 `agent_descriptor.go:20-34`；"not_logged_in 当前无发射方" | `go-bridge/agent_descriptor.go:20-34` | `:22 AgentStatusNotDetected AgentStatus = "not_detected"`、`:23 AgentStatusNotLoggedIn AgentStatus = "not_logged_in"`、`:24 ServiceNotRunning`、`:30 PairingRequired`；全仓 grep `not_logged_in\|NotLoggedIn`（排除 relay-server/deprecated）仅命中 :23 声明 + 测试函数名 `chatgpt_auth_reap_test.go:120`（非发射方） | 吻合 / 成立 | 亲核 |
| E-2 直通 `:254`、`:260-269` | 同文件 `:253-254`、`:260-270` | `:254 return detectStructuredInstanceReadiness("codex-remote", agent)`；`:265-269 status, detail := prober.StructuredInstanceReadiness(); if strings.TrimSpace(status) == "" { return AgentStatusNotConfigured, ... } return AgentStatus(status), detail` | 吻合 / 成立：任意非空 status 直通 wire，枚举级扩展无需改 descriptor 层 | 亲核 |
| E-3 iOS 特判与通用分支 `BackendModels.swift:25-38` | iOS `Services/Backend/BackendModels.swift:15-40` | `:25 guard descriptor.status == "available" else {`；`:26 if kind == .codexRemote, descriptor.status == "pairing_required" {`；`:32-36 let trimmedReason = descriptor.reason?...; return BackendSwitchAvailability(isEnabled: false, reason: trimmedReason.flatMap { $0.isEmpty ? nil : $0 } ?? "后端暂不可用")` | 吻合 / 成立：not_logged_in/not_detected 走通用分支显示 reason，无崩溃路径 | 亲核 |
| E-3 raw 解码 + 负例 | iOS `CCCodeBridgeModels.swift:188-201`；iOS 全仓 grep | `:192 let status: String`（CCCodeAgentDescriptor.Codable 字段）；全仓 `not_logged_in|notLoggedIn|not_detected|notDetected` 命中仅为 TaskDock 测试状态域（TaskDockState/TestOutputParser 等）与 goal record，无 descriptor 状态特判 | 吻合 / 成立：raw String 解码、未知值安全 | 亲核 |
| E-3 注（r1 F-3 校准）"`:486-487` 属 goal record 非 descriptor" | iOS `BackendModels.swift:483-492` | `:483 struct BackendCodexGoalRecord: Equatable, Sendable {`…`:486 /// Kept as the official raw value so a future status remains displayable.`、`:487 let status: String` | 吻合 / 校准正确：该注释属 goal 记录字段 | 亲核 |
| E-4 两循环同 backoff `pairing_persist.go:338-370`、`:372-423` | 同文件 | `:353 err := p.restoreOnce(ctx)`；`:362-366 p.markOffline(offlineMessageForRestoreError(err)); retryIn := backoff.Next(); slog.Warn("codex-remote pairing restore waiting", ...); a.sleepInterruptible(retryIn)`；`:408 err := a.pairing.restoreOnce(ctx)`；`:418-421 a.pairing.markOffline(offlineMessageForRestoreError(err)); retryIn := backoff.Next(); slog.Warn("codex-remote pairing reconnect waiting", ...); a.sleepInterruptible(retryIn)`；`:411 backoff.Reset()`（成功路径，D2「不变」主张） | 吻合 / 成立：两循环同规则现状一致，D2 的双循环扩展落点准确 | 亲核 |
| E-9 三消费方 `:240-241` 注释；消费方 `:254/:353/:408` | 同文件 `:235-241`、`:253-261`、`:353`、`:408` | `:240-241 // Shared by reconnectFromStore, restorePersistedPairing and watchBinding so every restore loop surfaces the same category through the readiness seam.`；reconnectFromStore `:253-261` 单次调用 restoreOnce 无循环 | 吻合 / 成立：D1「restoreOnce 出口单一写入点、三消费方自动共享」有源码依据；reconnectFromStore 确为单次调用 | 亲核 |
| D1「成功返回路径（含 client 已连接早退）清除」（r1 F-4） | `restoreOnce :263-336` | `:274-280 if cl != nil && !cl.IsClosed() { p.mu.Lock(); p.state.phase = PairPhaseReady; p.state.message = "已连接到 ChatGPT Desktop"; p.mu.Unlock(); return nil }`；成功尾部 `:326-335`（PairPhaseReady + return nil） | 吻合 / 成立：早退成功路径真实存在，清除语义可实现且不遗漏 | 亲核 |
| E-5 Stat 早退 `chatgpt_auth.go:55-57`、spawn `:66` | 同文件 `:53-57`、`:66`、`:114`、`:129` | `:54 bin := chatGPTCodexBin()`；`:55-57 if _, statErr := os.Stat(bin); statErr != nil { return "", "", fmt.Errorf("请先安装并登录 ChatGPT Desktop") }`；`:66 cmd := exec.CommandContext(ctx, bin, "-c", "model_provider=openai", "app-server", "--stdio")`；`:114 return "", "", fmt.Errorf("ChatGPT 未登录")`；`:129 return "", "", fmt.Errorf("读取 ChatGPT 登录态超时")` | 吻合 / 成立：未安装不 spawn、未登录每轮 spawn、超时路径均在（三类分类文本生成点溯源完成） | 亲核 |
| E-5/F-6 auth 缓存只存成功 `pairing.go:108-131` | 同文件 `:108-135` | `:110-111 // ... and caches ONLY successes — a failed probe must not occupy the cache`；`:114 if c := p.auth; c != nil && (!c.hasExp \|\| time.Now().Before(c.expAt.Add(-authCacheExpiryMargin)))` 命中即返回 | 吻合 / 成立：notLoggedIn 每轮真实探针、无进程收益损失仅 notInstalled 成立 | 亲核 |
| 分类器 desktopOffline 文本 `"no desktop environment"` | `pairing.go:531-570`、`:641` | `:565-568 env := pickDesktopEnv(list.Items); if env == nil { return nil, fmt.Errorf("no desktop environment") }`（lookupDesktopEnv，restoreOnce :319 调用链内） | 吻合 / 成立：Desktop 离线在 restore 链产生该文本，D1 分类与 E-9 可达链（缓存过期后 Stat 早退）均成立；时延限定见 F-7 | 亲核 |
| offlineMessageForRestoreError `:242-251`（文本映射四类） | 同文件 `:242-251` | `:244 case "请打开并登录 ChatGPT Desktop", "请先安装并登录 ChatGPT Desktop", "ChatGPT 未登录", "读取 ChatGPT 登录态超时":` → `:245 return "请打开并登录 ChatGPT Desktop"`；`:246-247 case "no desktop environment": return "请打开 ChatGPT Desktop"` | 吻合 / 成立：未安装/未登录/超时共用一条 banner、Desktop 离线另一条——iOS 文案区分度证据（F-8 依据） | 亲核 |
| E-6 ps 收尸先例 `agent_descriptor.go:412 Output()`、CombinedOutput `:435/:299/:322` | 同文件 `:410-412`、`:435`、`:299`、`:322`；事故档案 :176 | `:411 cmd := exec.CommandContext(ctx, "ps", "-ax", "-o", "command=")`；`:412 output, err := cmd.Output()`；事故 §3.5 表「同步 API 内部 Wait：Output()（session_catalog.go:350、agent_descriptor.go:412）、CombinedOutput()（agent_descriptor.go:435）」 | 吻合 / 成立（r1 F-3 方法名校正后的 v2 引用与源码逐字一致） | 亲核 |
| E-7 ticker 先例 `advertised_url_refresh.go:135` | 同文件 `:130-151` + `main.go:502` | `:135 ticker := time.NewTicker(advertisedURLRefreshInterval)`；main.go `:502 go runAdvertisedLocalURLRefresher(ctx, server, mgmtSrv, *port)` | 吻合 / 成立：watchdog 接线层与先例同层 | 亲核 |
| backoff 1s→30s + sleepInterruptible 分片 | `agent/codex-remote/backoff.go` | `:16-17 reconnectBackoffBase = 1 * time.Second / reconnectBackoffCap = 30 * time.Second`；`:31-49 Next()（推进 attempt，封顶当次返回后 attempt 归零）`；`:58-77 // sleepInterruptible 分片睡眠，Agent.Stop 后最多 2s 内返回` | 吻合 / 成立：D2「120s 固定、notInstalled 轮不推进 backoff、Stop 后 ≤2s」与既有语义兼容 | 亲核 |
| 非目标：CLI 型 LookPath 干净报 not_detected | `agent_descriptor.go:293-295`、`:316-318` | `:293-295 path, err := exec.LookPath("claude"); if err != nil { return AgentStatusNotDetected, "claude CLI not found in PATH" }`；`:316-318`（grok 同型） | 吻合 / 成立 | 亲核 |
| 非目标：dsh-web 四态 / opencode-web 三态先例 | `agent/dsh-web/readiness.go:37-42`、`agent/opencode-web/readiness.go:34-40` | dsh `:38-41 ReadinessAvailable/NotDetected/ServiceNotRunning/PortConflict`；opencode-web `:36-40` 三态 + 头注释 fail-closed | 吻合 / 成立 | 亲核 |
| 事故 §6 dsh-web 收尸列 △（r1 F-3 措辞校正） | 事故档案 `:374` | `△ Stop / spawn 失败路径有 Wait；自发退出路径缺失`（P0-3 已修，§5.5） | 吻合 / v2 措辞与原文一致 | 亲核 |
| 事故 §5.3 P2 三项 / §5.4 P2 完成门 | 事故档案 `:284-293`、`:309-312` | §5.3-1 缺席状态显式化、-2 重试预算（「降为低频探测或停轮」）、-3 watchdog；§5.4 P2 门 = watchdog 特征输出核验 + 状态机/重试预算单独验收 | 吻合 / 成立：R-1..R-4 与三项一一对应；OD-1 降频在事故许可范围内 | 亲核 |
| **本轮新增**：Mac Swift 对 not_logged_in/not_detected 的既有消费 | `MacBridge/MacBridge/Views/BridgeStatusView.swift:334-348` + 测试 | `:339 case "not_detected": return L10n.statusNotFound`；`:340 case "not_logged_in": return L10n.statusLoginRequired`；`:345 default: return status`（未知值原样透传）；ProductCopyTests `:63-64`（"Login Required"）、`:84-88`（未知值 + 全状态无禁用词）、MacBridgeBehaviorTests `:164` | — / 成立：五态枚举区分在 Mac UI 已有独立文案与测试，「Mac 行可见」主张成立；`not_logged_in` 首次发射对 Mac 侧零风险 | 亲核（r1 未查） |
| **本轮新增**：protocol pack 无 backends[].status 封闭枚举 | `docs/protocol/bridge-v1-schema.md:227`、`bridge-v1.md`（grep） | `:227 \| \`agent.*\` \| \`agent.not_detected\`, \`agent.unavailable\`, \`agent.version_unsupported\` \|`（错误码命名空间表，非 descriptor 状态）；bridge-v1.md 中 status 匹配均为 permission answer（:1931）与 seat 可用性（:2851-2857） | — / 成立：「protocol pack 不改（schema 无新值）」主张成立，状态字段为开放字符串 | 亲核（r1 未查） |

### 2.4 范围解读记录（非意见，承 r1 §2.4 复核）

事故 §5.3-1 原文「**每个 backend** 的 readiness 区分…」，方案收窄为仅 codex-remote。
本轮复核维持 r1 判断：§6 矩阵中 claude/grok（LookPath→not_detected，:295/:318 本轮
亲核）、dsh-web（四态）、opencode-web（三态）就绪面均已显式化，缺口主体是
codex-remote；与任务简报范围一致。owner 如按字面另有期待，属其裁决空间。

## 3. 意见（本轮新发现，均为建议）

### F-7［建议］E-9 可达链省略 auth 缓存时延限定：卸载发生在缓存有效期内时，notInstalled 检测/降频延迟到缓存失效

- **位置**：E-9（"notInstalled 经 watchBinding 可达（配对成功→卸载 ChatGPT.app→
  流断→重试→Stat 早退）"）、D2 覆盖理由段、§6 风险节（未记该窗口）。
- **证据**：`pairing.go:114` 缓存命中即返回（不查 Stat）；缓存仅存成功、按 token
  exp 失效（`:110-111` 注释；P1 证据 token exp ≈96h）。卸载 ChatGPT.app 且配对
  存续时：watchBinding→restoreOnce→chatGPTAuthCached **缓存命中跳过 Stat**→
  lookupDesktopEnv（pairing.go:565-568）→"no desktop environment"→分类
  desktopOffline（暂时类），readiness 显示 service_not_running、节奏维持 1-30s。
  直到缓存过期/失效（401）或 runtime 重启（默认 ~120min 兜底重启清内存缓存）后，
  探针才 Stat 早退 → notInstalled → not_detected + 120s。r1 F-2 的分析原文含此
  限定（"auth 缓存…按 token exp 失效…后，下一轮探针 Stat 失败"），v2 的 E-9
  压缩链把它丢了。
- **影响**：窗口内五态显示与降频不达成分歧窗口的上界被高估为即时；属透明度问题，
  非回归（窗口内行为=现状）。对主场景（冷启动/无缓存时未安装——事故与简报针对的
  形态）无影响：缓存为空，首轮即 Stat 早退。S-2 单测注入构造 notInstalled 错误，
  不受影响。
- **修订方向**：E-9 可达链补一句限定（"auth 缓存有效期内先经 desktopOffline
  表现，缓存过期/失效或 runtime 重启后进入 notInstalled"），§6 风险节可注一行
  窗口上界（token exp，实践受 ~120min 自动重启限制）。
- **闭合标准**：下一版（或实施提交说明）文本含该时延限定；不要求改设计。

### F-8［建议］「iOS 已能显示的原始字符串 / iOS 端按原始字符串显示」措辞强于实际机制：iOS 显示的是 reason 文案，未安装/未登录共用同一 banner

- **位置**：§1 非目标（"五态全部是 iOS 已能显示的原始字符串（证据 §4 E-3）"）、
  D1（"iOS 端按原始字符串显示（E-3 证据）"）、S-1 可见结果（"iOS/Mac 行可见
  枚举级缺席区分"）。
- **证据**：iOS descriptor.status 的唯一消费是 `BackendModels.swift:25-26`
  （available/pairing_required 特判），其余走通用分支显示 `descriptor.reason`
  （`:32-36`）；而 `offlineMessageForRestoreError`（pairing_persist.go:244-245）
  把未安装/未登录/超时三类错误映射为**同一条** banner「请打开并登录 ChatGPT
  Desktop」，仅 Desktop 离线（:246-247）是另一条。因此 iPhone 上用户可见文案
  无法区分未安装与未登录；枚举级区分实际落在 wire 层（status 值）与 **Mac UI**
  （BridgeStatusView.swift:339-340 独立文案 + 测试）。E-3 本身的 v2 描述（走
  通用不可用分支显示 reason 文案）是准确的，问题只在上述三处压缩表述。
- **影响**：owner 若按字面在 iPhone 上验收 S-1「五态可见」，会在未安装/未登录
  之间看不到文案差异，产生验收歧义。不影响设计正确性：R-1 为枚举级（任务简报
  钉死），零 iOS 改动与无崩溃主张均成立（E-3）。
- **修订方向**：三处各补半句校准，如「iOS 经通用不可用分支显示 reason 文案
  （未安装/未登录共用 banner），枚举区分在 wire 与 Mac UI（BackendStatusText
  独立文案）可见」。
- **闭合标准**：下一版不再声称 iOS「显示原始状态字符串」本身；S-1 可见结果
  明示 Mac 侧为枚举可见面。

### F-9［建议］§2.1 来源清单滞后一个 docs-only 提交；「仅 .exec-plan 状态文件未跟踪」已过期

- **位置**：§2.1（"本仓提交=95cbb3e0bb47…（P1 已提交；本方案为未跟踪新增）"；
  "本仓未提交状态=干净（仅 .exec-plan 状态文件未跟踪）"）。
- **证据**：评审时 HEAD=`11ae0938`（2026-10-13 02:39），其内容为
  incident 头/§5.6、think.md、P1 完成报告、`.exec-plan/state/plan-92731ca149b2.json`
  ——该 state 文件**已随此提交入库**（不再未跟踪）；`git diff 95cbb3e0 HEAD --
  '*.go' '*.swift'` 为空。当前未跟踪项实为：方案 v2、r1 评审报告。
- **影响**：全部代码锚点因零代码差异仍然逐字节有效（本轮按 HEAD 亲核），不否定
  任何结论；但按 CLAUDE.md P0 来源门，实施 kickoff 直接照抄该清单会把来源身份
  记错一档。
- **修订方向**：实施 kickoff 时按门 2 重新生成清单（本来就必做）；或在下一版
  §2.1 更新为 11ae0938 并注一行「相对 95cbb3e0 仅 docs/.exec-plan 变更，代码
  零差异」。
- **闭合标准**：实施前的来源清单反映 kickoff 时真实 HEAD 与未跟踪状态。

## 4. r1 意见闭合处置

| r1 意见 | 本轮核验 | 处置 |
| --- | --- | --- |
| F-1［阻塞］D3「正常态不打日志」vs 验收「首周期特征行」矛盾 | D3 增 goroutine 启动一次性 armed INFO（`"go-bridge zombie watchdog armed", pid/period/childrenThreshold`，启动即出不依赖首 tick）；§1 验收标准、R-4、S-3（含冒烟断言 armed 行）、S-4（armed 特征行核验）、§6 五处口径统一为「armed 行核验；超标 WARN 以单测为准、健康部署不出现」；armed 行文案+字段为本提交独有，满足 CLAUDE.md 运行态验证第 2 条 | **闭合** |
| F-2［阻塞］分级降频未覆盖 watchBinding 第二恢复循环 | 采修订方向 (a)：D2 双循环同规则（notInstalled→sleepInterruptible(120s) 固定不经 backoff、不推进轮次；其余→既有 backoff.Next()；watchBinding 成功路径 backoff.Reset() 不变——:411 亲核）；D1 钉死 lastRestoreClass 单一写入点（restoreOnce 出口：失败写/成功清含 client 已连接早退 :274-280 亲核）；E-9 新增且锚点（:372-423/:408/:418-421/:240-241/:254/:353/:408）本轮全部亲核成立；S-2 测试清单含 watchBinding 分支 | **闭合**（E-9 链的缓存时延限定另记 F-7 建议） |
| F-3［建议］锚点精度四处 | (1) E-6 方法名已改 `Output()`，CombinedOutput 同类 :435/:299/:322 亲核一致；(2) 行号统一：§1/§2.2/E-4 均为 offlineMessageForRestoreError :242-251、restorePersistedPairing :338-370、watchBinding :372-423，与源码逐字对上；(3) 非目标矩阵措辞改为「dsh-web 收尸列事故时标 △（P0-3 已修）」，与事故 :374 原文一致；(4) E-3 描述按实际机制重写（通用分支+reason 显示），:486-487 注释归属标注正确 | **闭合** |
| F-4［建议］lastRestoreClass 成功后不清除 | D1 增成功路径清除（含早退）；S-1 单测增「成功后类别清除——断连首窗口不再按陈旧类别显示」断言 | **闭合** |
| F-5［建议］S-5 缺 CHANGELOG | S-5 增补 CHANGELOG [Unreleased] 一项（五态就绪/重试节奏/watchdog 均为用户与运维可见变化） | **闭合** |
| F-6［建议］notLoggedIn 探针节奏未明示 | §6 风险节补记（Stat 通过每轮真实 spawn、缓存只存成功 pairing.go:108-131、P0 后均收尸、属简报钉死的接受行为）；E-5「无进程收益损失」限定为仅 notInstalled 类 | **闭合** |

r1 建议无被降级或暗置不改项；v2 §0「全部采纳」与正文实际修订一致。

## 5. 门控一致性复核（本轮重查）

- **R→S→验收**：R-1→S-1（五态单测含 fail-closed 负例+快照即时性）、R-2→S-2
  （双循环节奏单测+运行态 Warn 间隔对账）、R-3→S-3（判定纯函数边界+采样注入
  fake+启动接线冒烟）、R-4→全部单测+S-4 运行态；无悬空引用。
- **E 门**：Gate A（E-1..E-7、E-9）本轮全部亲核 verified；E-8 正确登记为
  S-4 完成门（实施后）。Gate B：OD-1 decided（降频 120s，工程决定，owner 可
  改停轮偏好），在事故 §5.3-2「降为低频探测或停轮」许可范围内。
- **非目标与简报逐项一致**：零 iOS 改动（E-3）、零新增 wire 枚举（E-2 + 本轮
  protocol pack 复核）、不动其他 backend 就绪面（§6 矩阵复核）、不停轮、不熔断、
  P1 门控/缓存语义不变（D1/D2 只读错误分类与改 sleep 来源，chatGPTAuthCached/
  ctrl 门控路径未触碰——:304-318 亲核）。
- **无矛盾允许/禁止**：watchdog「只观测不处置」与 S-3 失败处理（自身故障只 Warn）
  一致；「采样期正常静默」与「armed 一次性行」不冲突；「超标 WARN 不节流」仅限
  异常态路径。
- **回退边界**：纯观测/分类/节奏层，revert 即回退；与 P0/P1 已交付语义无冲突。

## 6. 剩余门（与阻塞分开列；通过不等于以下已满足）

1. **实施授权**（owner）：评审通过 ≠ 实施授权。用户原始请求「实施这个文档里的
   所有任务」显示意向；待 owner 在本过审版本（SHA da7dc44c…）上确认范围与时机。
2. **S-4 部署时机**：方案已注明「时机 owner 拍板」。
3. **E-8 P1 口径复测**：S-4 完成门——部署后按 `build/p1-reconcile-75543.log`
   口径复测探针/刷新节奏不劣化（含 P1 在线观察收口）。
4. **watchdog armed 特征行运行态核验**：S-4 完成门（启动即出，字段
   pid/period/childrenThreshold）。
5. **未安装态运行态对账 N/A 记录义务**：本机 ChatGPT.app 在装，该态以单测为准，
   运行态 N/A 须如实记录（S-4 执行时落实）。
6. **实施 kickoff 来源清单再生成**（CLAUDE.md 门 2；见 F-9）。

## 7. 结论复核

- 覆盖足够：r1 全部承重锚点本轮重核 + 四项新检查（Mac Swift 消费、protocol
  pack、ps 格式、错误文本溯源）通过；三条新建议均为登记精度，无设计或验收缺陷。
- 未发现：伪造已验证事实、待捕项伪装成事实、必需要求被可选范围卡死、与 P0/P1
  交付语义冲突、或修订引入的回归。
- 争议项：无。

**verdict: APPROVED（阻塞 0 / 建议 3：F-7、F-8、F-9）**。建议不构成实施前置；
owner 如采纳可在实施提交中顺手收口（F-7/F-8 各一两句文字，F-9 为 kickoff 例行）。
下一步：owner 决定是否授权实施。
