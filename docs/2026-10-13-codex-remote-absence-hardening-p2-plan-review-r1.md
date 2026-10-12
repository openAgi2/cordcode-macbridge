# codex-remote 缺席语义加固方案（P2）评审报告 r1

- 评审轮次：第 1 轮（首轮，无历轮报告；风险清单独立建立）
- 评审模式披露：本轮为本方案首次评审，评审员未持有本方案历轮结论，独立建立检查范围后核对。事故档案 r1/r2 修订记录与 P1 评审结论作为背景材料读入，未以其替代亲核。
- 评审技能/契约：plan-review（契约 plan-contract-v1.1）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **2**（F-1、F-2）
- advisories: **4**（F-3、F-4、F-5、F-6）
- 方案：`docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md`（v1 送审，未跟踪新增文件）
- 方案 SHA-256：`da025781a096093eb4edbd3d795e1e08c280450a9e5a0b6a615f392f9959023c`（与派发 SHA 一致，本轮评审对象即该快照）
- 本轮范围：**full 全量**（R/OD/E/S 映射、全部现状锚点、门控一致性、验证与验收矩阵、Mac 仓源码 + iOS 只读证据；上游不适用性论证复核）

**总体判断**：方案与事故档案 §5.3 P2 三项任务对齐良好，复用调查扎实（E-1..E-7 全部亲核成立），来源清单与实际工作树状态逐项吻合，"零 iOS 改动"主张的证据链（wire 枚举已存在 + descriptor 直通 + iOS raw String 解码 + 通用不可用分支）核实成立。两处阻塞均为方案内部一致性问题，修订成本低：F-1 是 D3 设计与验收标准的直接矛盾；F-2 是 R-2 的类别→节奏不变量在共用 `restoreOnce` 的第二个循环上未闭合。修订后可过。

**implementation_readiness**：

- 无已授权可开工切片——按方案评审循环纪律，实施入口是"方案文档过审 + owner 授权"，两者目前均未满足。
- 设计层面：S-1（就绪五态）主体不受两条阻塞影响；S-3（watchdog）受 F-1 影响需先定特征输出形态；S-2（重试预算）受 F-2 影响需先定 watchBinding 处置；S-4 验收口径依赖 F-1 的解决。
- 背景记录：用户在本次工作流入口的原始请求为「实施这个文档里的所有任务」（指事故档案）。该表述显示 owner 有实施意向，但按方案自身门（§6「通过后 owner 授权 → 实施」）与任务简报（评审通过 ≠ 实施授权），实施授权仍待 owner 在过审版本上明确确认范围与时机。

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 用途 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `95cbb3e0bb4716df88c7482d57ca1f94d2470380` | 干净；未跟踪：`.exec-plan/state/plan-92731ca149b2.json`、方案文档本身 | 方案 §2.1 清单与实际一致（方案自述"本方案为未跟踪新增"） |
| iOS（只读） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `507a1e23b8c38847f1433921dbe65557965a52e8` | 脏 277 行（iOS timeline 在途任务） | E-3 证据；方案 §2.1 所记 277 行与实际 `git status --porcelain \| wc -l` 一致 |
| 上游 | 不适用 | — | — | — | 方案 §2.1 论证 P2 三项为 bridge 私有运维语义，无官方对应物；本轮复核认可（官方 codex transport 无「宿主未安装自身」概念；backoff/刷新镜像义务属 P1 且本方案不改） |

运行证据档案（本轮只读核对）：`build/p1-reconcile-75543.log`、`build/p1-reconcile-75543-offline-extended.log`、`build/zombie-reconcile-95438-{offline,online}.log`（存在性 + 计数核对）。

### 2.2 亲核范围与 N/A

- 已亲核：方案 §2.2 全部现状锚点、E-1..E-7 全部命题、D1/D2/D3 涉及的全部源码符号（含方案未提及但受设计影响的 `watchBinding`、`chatGPTAuthCached`、`sleepInterruptible`、`backoff.go`）、opencode-web/dsh-web readiness 先例、iOS descriptor 解码与消费路径、事故档案 §3/§5.3/§5.4/§6、P0/P1 提交历史、CHANGELOG 现状。
- 未核/不适用：未运行任何测试或构建（方案阶段，评审只读）；未核 iOS `output/` 下两个 baseline worktree 副本（非 E-3 证据来源，主工作树已覆盖）；未核 `session_catalog.go:350`（方案未引用该行，E-6 引用的 `agent_descriptor.go:412` 已核）。

### 2.3 锚点核验表

原引用保持原样；"定位/语义"分列判断。摘录为能检验命题的最小原文。

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 亲核 |
| --- | --- | --- | --- | --- |
| E-1 三态骨架 `diagnostics.go:10-35` | `agent/codex-remote/diagnostics.go:10-35` | `:11-13 ReadinessAvailable/PairingRequired/ServiceNotRunning`；`:19 func (a *Agent) StructuredInstanceReadiness() (string, string)` | 吻合 / 成立 | 亲核 |
| E-2 枚举全集 `agent_descriptor.go:20-34`；"not_logged_in 当前无发射方" | `go-bridge/agent_descriptor.go:20-34` | `:22 AgentStatusNotDetected = "not_detected"`、`:23 AgentStatusNotLoggedIn = "not_logged_in"`、`:24 ServiceNotRunning`、`:30 PairingRequired` | 吻合 / 成立；全仓 grep `not_logged_in\|AgentStatusNotLoggedIn` 仅命中 :23 声明，无发射方、无测试引用——"首次发射"主张成立 | 亲核 |
| E-2 直通 `agent_descriptor.go:254`、`:260-269` | 同文件 `:253-254`、`:260-270` | `:254 return detectStructuredInstanceReadiness("codex-remote", agent)`；`:266-269 if strings.TrimSpace(status) == "" { return NotConfigured... } return AgentStatus(status), detail` | 吻合 / 成立：任意非空 status 直通 wire，枚举级扩展无需改 descriptor 层 | 亲核 |
| E-3 iOS 特判 `BackendModels.swift:25-26` | iOS `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift:25-38` | `:25 guard descriptor.status == "available" else {`；`:26 if kind == .codexRemote, descriptor.status == "pairing_required" {`；`:31-37` 其余走通用不可用分支，显示 `descriptor.reason` | 吻合 / 成立：not_logged_in/not_detected 走通用分支，无崩溃路径 | 亲核 |
| E-3 raw 解码、负例"无 not_logged_in 特判" | iOS `Services/Bridge/CCCodeBridgeModels.swift:188-193` | `:192 let status: String`（CCCodeAgentDescriptor.Codable） | 吻合 / 成立：raw String 解码无 enum 强转；iOS 全仓 grep 无 descriptor `not_logged_in/not_detected` 特判；`BridgeProvider.swift:958-975/1218-1240` acceptedBackends 按 kind/id 过滤、不按 status 丢弃——未知 status 的 descriptor 仍到达 UI | 亲核 |
| E-3 `:486-487` 注释 | 同文件 `:486-487` | `:486 /// Kept as the official raw value so a future status remains displayable.`、`:487 let status: String` | 定位吻合，但该字段属 `BackendCodexGoalRecord`（goal 记录 status），非 descriptor status 通路——证据描述需校准，见 F-3 | 亲核 |
| E-4 恢复循环 `pairing_persist.go:334-363`（§1）/`338-367`（§2.2） | `agent/codex-remote/pairing_persist.go:338-370` | `:344 backoff := newReconnectBackoff()`；`:362-366 p.markOffline(offlineMessageForRestoreError(err)); retryIn := backoff.Next(); slog.Warn("codex-remote pairing restore waiting", ...); a.sleepInterruptible(retryIn)` | §2.2 行号准确、§1/E-4 范围偏移（见 F-3）/ 语义成立：非撤销错误一律同 backoff，未安装也 1~30s 永续 | 亲核 |
| E-5 Stat 早退 `chatgpt_auth.go:55-57` | `agent/codex-remote/chatgpt_auth.go:53-57` | `:54 bin := chatGPTCodexBin()`；`:55-57 if _, statErr := os.Stat(bin); statErr != nil { return "", "", fmt.Errorf("请先安装并登录 ChatGPT Desktop") }` | 吻合 / 成立：未安装不 spawn | 亲核 |
| E-6 ps 先例"agent_descriptor.go:412 CombinedOutput 同类" | `go-bridge/agent_descriptor.go:410-412` | `:411 cmd := exec.CommandContext(ctx, "ps", "-ax", "-o", "command=")`；`:412 output, err := cmd.Output()` | 定位吻合但方法名不符：:412 是 `Output()`；`CombinedOutput()` 同类在 :435（detectCodexCLI）与 :299/:322。事故档案 r1 勘误自己也记 ":412 Output() / :435 CombinedOutput()"（见 F-3）/ 语义成立：同步收尸合规的 ps 采样先例存在 | 亲核 |
| E-7 ticker 先例 `advertised_url_refresh.go:135` | `go-bridge/advertised_url_refresh.go:130-151` + `go-bridge/main.go:502` | `:135 ticker := time.NewTicker(advertisedURLRefreshInterval)`；main.go `:502 go runAdvertisedLocalURLRefresher(ctx, server, mgmtSrv, *port)` | 吻合 / 成立：ticker goroutine 随 runtime 起，watchdog 接线层存在 | 亲核 |
| 错误分类雏形 `pairing_persist.go:242-251`（§1）/`230-243`（§2.2） | `agent/codex-remote/pairing_persist.go:242-251` | `:244 case "请打开并登录 ChatGPT Desktop", "请先安装并登录 ChatGPT Desktop", "ChatGPT 未登录", "读取 ChatGPT 登录态超时":`；`:246 case "no desktop environment":` | §1 行号准确、§2.2 起点早 12 行（见 F-3）/ 语义成立：四类缺席原因文本可区分 | 亲核 |
| backoff 1s→30s 镜像官方 | `agent/codex-remote/backoff.go` | `reconnectBackoffBase = 1s / Cap = 30s`；头注释"对齐官方 remote-control 策略 (codex-rs app-server-transport websocket.rs: next_reconnect_delay)"；`sleepInterruptible` 同文件 `:59`（分片睡眠，Stop 后 ≤2s 返回） | 吻合 / 成立：D2 的 `sleepInterruptible(120s)` 与 Stop 响应性兼容 | 亲核 |
| 就绪先例 opencode-web `readiness.go:36-73` / dsh-web `readiness.go:37-91` | `agent/opencode-web/readiness.go:33-73` / `agent/dsh-web/readiness.go:37-42,64+` | opencode-web 三态常量 `:36-40`（available/not_configured/service_not_running，头注释 fail-closed "never collapsing a probe failure into not_configured"）；dsh-web 四态 `:38-41`（+port_conflict） | 吻合 / 成立 | 亲核 |
| P1 对账口径 `build/p1-reconcile-75543.log`（"10.5 分钟 1 探针/2 刷新/0 僵尸"） | `build/p1-reconcile-75543.log` + `-offline-extended.log` | 短档 `auth_probe=1 token_refreshed=2 ... zombies=0`；扩展档 `runtime_pid=75543 lstart=01:26:55`，sample5 `01:37:23`（≈10.5 分钟窗口）；`zombies_under_runtime=0` | 吻合 / 成立：数字与窗口均对得上（窗口取 runtime 启动→末次采样） | 亲核 |
| 事故档案 §5.3 P2 三项 / §5.4 P2 完成门 / §6 矩阵 | `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md` | §5.3-1 缺席状态显式化、-2 重试预算（"未安装时降为低频探测或停轮"）、-3 zombie watchdog；§5.4 P2 门 = "watchdog 上线后的特征输出核验 + 缺席状态机/重试预算按 §5.3 各自语义单独验收"；§6 矩阵 | 吻合 / 成立：R-1..R-4 与三项任务一一对应；OD-1 降频选项在事故许可范围内 | 亲核 |
| **方案未提及：`watchBinding` 第二恢复循环** | `pairing_persist.go:372-423` | `:408 err := a.pairing.restoreOnce(ctx)`；`:418-421 a.pairing.markOffline(offlineMessageForRestoreError(err)); retryIn := backoff.Next(); slog.Warn("codex-remote pairing reconnect waiting", ...); a.sleepInterruptible(retryIn)` | 方案 D2/S-2 未覆盖此循环 / 语义见 F-2 | 亲核 |
| **方案未提及：auth 缓存只存成功** | `agent/codex-remote/pairing.go:108-131` | `:110-111 注释 "caches ONLY successes — a failed probe must not occupy the cache"`；按 token exp 失效，`invalidateAuthCache` 于 401/403/撤销 | — / 支撑 F-2 可达性分析 | 亲核 |

### 2.4 范围解读记录（非意见）

事故档案 §5.3-1 原文写「**每个 backend** 的 readiness 区分 …」，本方案收窄为仅 codex-remote。本轮核对认为收窄有依据且与任务简报一致：§6 矩阵中 claude/grok（LookPath→not_detected，agent_descriptor.go:295/:318 亲核）、dsh-web（四态 readiness.go 亲核）、opencode-web（三态 readiness.go 亲核）就绪面均已显式化；事故档案同段自己点明缺口主体是 codex-remote（"目前只有 offline 消息映射…无状态机语义"）。此为范围解释的显式记录，不构成意见；owner 如按字面读「每个 backend」另有期待，属其裁决空间。

## 3. 意见

### F-1［阻塞］D3「正常态不打日志」与验收标准「watchdog 首个周期出现特征日志行」直接矛盾

- **位置**：§1 验收标准（"Release 覆盖安装后：watchdog 首个周期出现特征日志行"）、R-4（"watchdog 特征输出在部署后运行态核验（事故 §5.4 P2 完成门）"）、S-4（"watchdog 首周期日志核验"）、§6（"需实施后验证：…watchdog 特征输出"）vs D3（"正常态**不打日志**（避免常驻噪音），仅超标告警"）。
- **证据**：D3 明确设计为健康态零输出；而 P0 已修复 + 部署机 ChatGPT.app 在装（方案 §6 自述"未安装态本机难构造"），S-4 部署后runtime 的预期状态正是健康态（0 僵尸、children ≪ 64）→ 按设计**没有任何特征日志行可核验**。四处验收文本与设计互相矛盾。且 D3 自称"对齐 CLAUDE.md 部署后运行态验证第 2 条"——该条要求"日志必须出现本次提交**独有**的字段/行为；'日志仍在正常滚动'不算证据"：静默 watchdog + 本机不可构造的 notInstalled Warn（D2 的 `absence` 字段同样在本机健康态下不出现）意味着 S-4 在预期部署态下没有任一提交独有输出可证明新代码在跑。
- **影响**：S-4/R-4/事故 §5.4 P2 完成门按现文不可满足；实施者要么违背 D3 加日志、要么造一个超标现场（方案未规定且属 hack），验收口径悬空。影响切片 S-3、S-4。
- **修订方向**（最小充分）：watchdog 启动时打**一条**一次性 INFO（如 `slog.Info("go-bridge zombie watchdog armed", "pid", os.Getpid(), "period", "5m", "childrenThreshold", 64)`）——每 runtime 生命周期仅一条，零常驻噪音，同时满足「首周期特征日志行」与 CLAUDE.md 运行态验证第 2 条；或显式改写验收为「健康态 N/A、仅超标可核验」，但需另行指定一条提交独有的部署验证标记（不推荐，弱于前者）。
- **闭合标准**：修订版 D3 与 §1/R-4/S-4/§6 四处口径一致——要么设计含一次性特征行且验收保持，要么验收显式改口径并给出替代的提交独有验证手段；下一轮按修订后的唯一口径核对。

### F-2［阻塞］R-2 的「永久类降频」只落在 restorePersistedPairing，未覆盖同样消费 restoreOnce 错误的 watchBinding 循环

- **位置**：R-2（"未安装态恢复循环降频（120s）"）、§1 目标 (b)（"恢复循环错误分级重试：永久类（未安装）降频至 120s 固定间隔"——均为循环无关的类别→节奏不变量）、D2/S-2（仅 `restorePersistedPairing`）。
- **证据**：`pairing_persist.go:372-423 watchBinding` 同样以 `restoreOnce` 失败驱动重试（`:408`），失败后 `backoff.Next()` 1~30s 永续（`:419-421`），与 restorePersistedPairing 共用同一错误全集（含 `loadChatGPTAuth` 的 Stat 早退错误「请先安装并登录 ChatGPT Desktop」）。可达链：配对成功运行中 → 用户卸载 ChatGPT.app → 连接断 → watchBinding 重试；auth 缓存只存成功、按 token exp 失效（`pairing.go:108-131` 亲核）或经 401/撤销失效后，下一轮探针 Stat 失败 → notInstalled 类。此时按 D1（`lastRestoreClass` 在 `restoreOnce` 失败时写入，两循环共享）readiness 显示 `not_detected`，而重试节奏仍是 1-30s——恰是 §1-2 自述要消除的「永续空转、日志常刷」形态（该路径无 spawn，纯日志/空转浪费）。另：D1 未钉死 `lastRestoreClass` 的写入点——若实现为仅 restorePersistedPairing 的错误分支写入，则 watchBinding 失败后类别陈旧/缺失，readiness 在该路径回退 service_not_running，D1 的"分类接入"同样不完整。两种实现读法各留一个缺口。
- **影响**：R-2/目标 (b) 的不变量（永久类 120s）在方案的分类器自己可达并标注的第二个循环上不成立；S-2 测试清单也只覆盖单循环。类别→状态（D1）与类别→节奏（D2）在 watchBinding 上互相矛盾（显示 not_detected 却按暂时类节奏刷）。
- **修订方向**（最小充分，二选一并写明）：(a) 把同一 class 分流接入 watchBinding 的 restore 重试路径（notInstalled → 120s，其余 backoff；S-2 增一条对应单测）；或 (b) 显式记录排除 watchBinding 的理由与边界（何态可达、为何接受 1-30s），同时钉死 `lastRestoreClass` 写入点语义（建议在 restoreOnce 失败出口统一写入，两循环共享），使 R-2/(b) 的表述与 D2/S-2 范围一致。
- **闭合标准**：修订版对 watchBinding 有明确处置（覆盖或带理由的显式排除），`lastRestoreClass` 写入点无歧义，R-2/D2/S-2 三处口径互洽；若选 (a)，S-2 测试清单含 watchBinding 分支。

### F-3［建议］锚点精度：行号内部不一致、E-6 方法名误标、§6 矩阵"均标 ✔"与原文不符、E-3 证据描述过度

- **位置/证据**（原引用保持原样并排实际）：
  1. E-6 引「agent_descriptor.go:412 CombinedOutput 同类」——实际 `:412` 为 `cmd.Output()`（detectCodexAppServerProcess 的 ps 采样）；`CombinedOutput()` 在 `:435`（detectCodexCLI）与 `:299/:322`。事故档案 r1 勘误行自己即写「agent_descriptor `ps+Output()` :412 / `CombinedOutput()` :435」。语义（同步收尸合规先例存在）成立。
  2. 同一函数两组行号：`offlineMessageForRestoreError` §1 记 242-251（准确）、§2.2 记 230-243（起点早 12 行）；`restorePersistedPairing` §1/E-4 记 334-363、§2.2 记 338-367（后者准确）。语义均成立，行号应统一为准确组。
  3. 非目标「事故 §6 矩阵均标 ✔」——矩阵中 dsh-web 行收尸列原文为「△ Stop / spawn 失败路径有 Wait；自发退出路径缺失」（P0-3 已修，见 §5.1/§5.5）；readiness 主张（dsh 四态/opencode-web 三态/CLI LookPath）本身亲核成立。措辞宜改为引用实际标记或"P0 后状态"。
  4. E-3 表述「iOS 按原始字符串显示 status」并引 `:486-487` 注释——该注释属 `BackendCodexGoalRecord.status`（goal 记录字段），非 descriptor status 通路；descriptor 的实际机制是"仅特判 available/pairing_required（:25-26），其余走通用不可用分支显示 `descriptor.reason`（:31-37）+ raw String 解码（CCCodeBridgeModels.swift:192）"。结论（零 iOS 改动、未知值安全显示）成立，但证据描述应按实际机制校准，避免下轮误引。
- **影响**：不改变任何设计结论；但按 CLAUDE.md 评审来源门「行号或符号实际来自另一处」的口径累积会削弱报告可信度，且 §1/§2.2 行号不一致会给实施者对号入座制造歧义。
- **修订方向**：统一行号、修正 E-6 方法名、改矩阵措辞、按实际机制重写 E-3 描述。
- **闭合标准**：修订版上述四处与源码逐字对得上。

### F-4［建议］`lastRestoreClass` 成功后不清除：连接断开后的首窗口可能按陈旧类别显示

- **位置**：D1（class 仅"restoreOnce 失败时写入"，无成功清除语义）。
- **证据**：成功 restore 后 class 保留最后一次失败类别；之后连接断开、下一次 restoreOnce 失败（45s 超时内）覆盖类别之前，`StructuredInstanceReadiness` 的非 available 分支会按陈旧 class 映射（如冷启动缺席期写入的 notInstalled 在 app 已装后仍显示 not_detected，最长 ~45s）。现状同窗口显示陈旧 message（"已连接到 ChatGPT Desktop"），非回归，但一行"成功时重置 class"可消除。
- **影响**：短暂的错误缺席标签；影响 S-1 验收的稳定性。
- **修订方向**：D1 增一句——restore/bind 成功路径清除或重置 `lastRestoreClass`（未知值 → fail-closed service_not_running 已覆盖）。
- **闭合标准**：修订版 D1 含成功路径的类别处置；S-1 单测可含"成功后断连首窗口"断言（可选）。

### F-5［建议］S-5 文档收口缺 CHANGELOG `[Unreleased]` 条目

- **位置**：§5 S-5（仅列"事故档案 P2 状态回填、think.md、完成报告"）。
- **证据**：CLAUDE.md 惯例「每轮对外可见的改动完成后，在 [Unreleased] 下追加一节」；P0 完成门（事故 §5.4）也含 CHANGELOG；五态就绪与重试节奏是用户/运维可见变化（iOS 行为文案、日志节奏）。GO_BRIDGE_ARCHITECTURE.md 经查未文档化 codex-remote readiness 三态（grep 无 StructuredInstanceReadiness/就绪相关行），无需回填该文档。
- **影响**：交付惯例缺口；不影响设计。
- **修订方向**：S-5 增补"CHANGELOG [Unreleased] 记录"一项。
- **闭合标准**：修订版 S-5 含 CHANGELOG。

### F-6［建议］notLoggedIn 态的探针节奏（1-30s 每轮一次 spawn）未在 §6 风险节明示

- **位置**：§6 风险（只列了 watchdog 采样成本、not_logged_in 首发崩溃风险、120s 恢复延迟）；E-5「降频纯为省日志/空转，无进程收益损失」仅对 notInstalled 成立。
- **证据**：已装未登录时每轮 restore 的 auth 探针仍真实 spawn（Stat 通过 → spawn → `ChatGPT 未登录`），失败不进缓存（`pairing.go:108-131` 只存成功）→ 1-30s 节奏下持续 ~2-4.6 次/分钟短命探针。P0 后无僵尸、属探针经济性残余；与任务简报钉死的"暂时类维持 1-30s"一致，非方案缺陷，但宜如实记录，避免 E-5 被误读为覆盖 notLoggedIn。
- **影响**：透明度；不影响验收。
- **修订方向**：§6 风险节补一句"未登录态维持 1-30s 探针节奏（每次一轮短命探针，P0 后已收尸），属接受的行为"。
- **闭合标准**：修订版 §6 含该说明。

## 4. 剩余门（与阻塞分开列）

以下为方案自身登记的、**不因评审结论变化**的实施前/实施后门：

1. **E-8（P1 口径复测）**：实施后验证项，S-4 完成门——部署后按 `build/p1-reconcile-75543.log` 口径复测探针/刷新节奏不劣化。状态：pending（正确登记为完成门，非前置门）。
2. **实施授权**：owner 未授权实施（评审通过 ≠ 授权）。用户原始请求「实施这个文档里的所有任务」显示意向，待 owner 在过审版本上确认范围与时机。
3. **S-4 部署时机**：方案已注明"时机 owner 拍板"。
4. **未安装态运行态对账**：本机 ChatGPT.app 在装，运行态 N/A，以单测为准并如实记录（方案 §6 已诚实登记；S-4 执行时须落实该记录义务）。
5. **P1 在线观察在途**（背景，非本方案门）：方案头部的在线态观察仍在途，S-4 复测时应一并覆盖。

## 5. 覆盖与结论复核

- 需求映射：事故 §5.3 P2 三项 → R-1..R-4 → S-1..S-4，无悬空引用；OD-1 已决定且登记为工程决定（降频 120s，与事故"降为低频探测或停轮"的许可范围一致）；非目标与任务简报逐项一致。
- E 门完整性：E-1..E-7 本轮全部亲核成立（含两处引用瑕疵，见 F-3）；E-8 正确登记为实施后完成门；Gate A/B 的"实施前置无阻塞"判断在证据层面成立。
- 验证层级相称性：单测覆盖五态映射（含 fail-closed 负例）、节奏分流、判定纯函数边界；运行态验证对齐 CLAUDE.md 部署门——唯 watchdog 特征输出验收与设计矛盾（F-1）。
- 未发现：伪造已验证事实、把待捕项写成事实、必需要求被可选范围卡死、或与 P0/P1 已交付语义的冲突（P1 门控/缓存语义在 D1/D2 下不变——分类器只读错误、分流只改 sleep 来源，`chatGPTAuthCached`/ctrl 刷新门控路径未被触碰，亲核确认）。

**verdict: REVISION_REQUIRED（阻塞 2 / 建议 4）**。两条阻塞均为低成本文字级修订（F-1 定一次性特征行或改验收口径；F-2 定 watchBinding 处置或显式排除），不涉及架构重做；闭合后建议直接进入下一轮复核（定向复核 F-1/F-2 闭合 + 全局引用一致性即可，其余亲核结论按命题/锚点粒度复用）。
