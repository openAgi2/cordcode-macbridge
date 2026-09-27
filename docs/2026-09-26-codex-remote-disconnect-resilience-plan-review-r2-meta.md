# codex-remote 断线韧性方案 r2 通过结论——元审核（定向复核）报告

- 日期：2026-09-26
- 审核员：元审核员-第2轮（按 `~/.zcode/agents/plan-reviewer.md` 硬边界工作；本轮唯一写入文件即本报告，其余一律只读）
- 复核对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r2.md`（r2 通过报告，verdict APPROVED）
- 方案：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`，本轮亲算 SHA-256 = `c80296e5260c1ea94ce6ba075301f0942c71e09dbce82d423ddc673cb6673950`，与派发记录及 r2 报告自报一致（评审对象身份确认）
- 复核范围（仅此两项，未复核其他内容）：
  1. 锚点抽查——从 r2 报告随机抽样带行号引用，逐行核对实际文件（定位 + 语义）；
  2. 来源清单完整性——r2 报告来源清单是否覆盖任务项目规则（AGENTS.md P0 来源门）要求的全部来源组合（含配套工作树，无论是否实际引用）。
- 复核结论：**confirm = false**——锚点抽查 7/7 全部通过、无问题；来源清单完整性发现 2 项问题（详见 §3）。锚点结论本身未受污染（缓解事实已亲核，见 §2.4），问题集中在来源清单对配套工作树的覆盖缺失。

---

## 1. 锚点抽查（复核范围一）

抽样方式：从 r2 报告 §3.1–§3.4 的带行号引用中随机抽 7 处（超出「至少 3 处」要求），覆盖 MacBridge 代码、官方 openai/codex、cordcode-ios、两仓 think.md 四类来源，含 E-12/S-3/S-7 承重锚点与常规锚点。每处均在本轮亲自读取实际文件核对。

| # | r2 报告引用 | 本轮核验（亲读文件/行） | 结果 |
| --- | --- | --- | --- |
| 1 | §3.1 `stream.go:390-407` ack 构造无 SegmentID；`:400-406` 摘录；`:397-399` armed 早退 | 亲读 `agent/codex-remote/stream.go`：`:390 func (s *Stream) ack(env Envelope)`；`:400-406 _ = s.conn.Write(Envelope{Type: typeAck, ClientID:…, EnvID:…, StreamID:…, SeqID: env.SeqID})` 确无 SegmentID 字段；`:397-399 if disabled { return }` | ✓✓ 定位与语义均吻合 |
| 2 | §3.1 `envelope.go:45 SegmentID *int`；`stream.go:339` readLoop 逐 chunk ack | 亲读 `agent/codex-remote/envelope.go:45` = `SegmentID *int`；`agent/codex-remote/stream.go:339` = `s.ack(env)`（chunk 分支内） | ✓✓ |
| 3 | §3.1 `main.go:1046-1066` 3s attach 循环、`:1056 ticker := time.NewTicker(3 * time.Second)` | 亲读 `go-bridge/main.go:1046-1066`：`:1046 func attachLiveCatalogPeriodically(...)`、`:1056 ticker := time.NewTicker(3 * time.Second)` | ✓✓ |
| 4 | §3.2 `websocket.rs:112-138` ack 游标：`:123`、`:129`、`:136` | 亲读官方 `codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:112-138`：`:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));`、`:129 let is_acked = envelope_cursor <= acked_cursor;`、`:136 self.buffer_by_stream.remove(&key);`（remove 仅在 ack 内） | ✓✓ |
| 5 | §3.2 `protocol.rs:114-119` 协议文档原文 | 亲读官方 `.../remote_control/protocol.rs`：`:118-119`「Chunk acknowledgements carry `segment_id` so the sender can retain only the still-unacked wire chunks on reconnect.」逐字吻合（Ack 文档注释起于 :116，引用区间覆盖正确） | ✓✓ |
| 6 | §3.3 `ProjectionStore.swift:164-181` 白名单不含 `projection.hydrate_failed` | 亲读 `cordcode-ios/OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:164-181`：`:164 private static func isRetryablePullError(code: String?) -> Bool`；`:166-176` 恰 11 项（projection.hydrating/disconnected/not_connected/relay.not_ready/relay.closed/request_timeout/send_timeout/ping_timeout/connect_timeout/hello_timeout/websocket.closed），确无 `projection.hydrate_failed` | ✓✓ |
| 7 | §3.4 macbridge `think.md:45` OD-2 裁决；iOS `think.md:477` | 亲读 macbridge `think.md:45` 含「**待立项（协议面）**：桥侧 `backend_status_changed` 推送」；iOS 仓 `think.md:477` 含「Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时、stream idle/closed、pairing 流反复重连），至 14:00 未恢复」 | ✓✓ |

**范围一结论：7/7 抽样锚点行号与语义全部吻合，无问题。**

## 2. 来源清单完整性（复核范围二）

### 2.1 任务项目规则要求

AGENTS.md（P0 来源门）要求：任何评审开始前必须为**每一个**涉及的仓库和工作树记录完整来源清单（仓库路径/分支/完整提交/未提交状态/任务预期分支/配套仓库路径/分支/提交/预期产品特性）；且「存在多个工作树时，必须先按下方 P0 来源门解析同一功能分支族，再读取源码或形成结论」。

### 2.2 本轮亲核的工作树实况（`git worktree list` 亲跑）

- cordcode-macbridge（3 个工作树）：`/Users/jacklee/Projects/cordcode-macbridge`（main @ 07721783）、`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`（**feat/ios-native-message-timeline** @ 07721783，r2 评审所在树）、`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（detached @ b2b25235）
- cordcode-ios（3 个工作树）：`/Users/jacklee/Projects/cordcode-ios`（main @ fe421cdc，r2 报告 iOS 锚点核验所用）、**`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`（feat/ios-native-message-timeline @ f4a81ee4，与评审所在 Mac 工作树同分支族的配套工作树）**、`/Users/jacklee/Projects/cordcode-ios-plan-approval`（detached @ a336b68b）

### 2.3 r2 报告来源清单覆盖情况

已覆盖（本轮对其声明逐项复核属实）：

- MacBridge 评审所在树：路径/分支/完整提交/未提交状态齐全（亲跑 `git branch --show-current`/`git rev-parse HEAD`/`git status --porcelain` 与报告一致）；
- MacBridge 代码锚点等价声明：`git diff 715104c6..HEAD --stat` 亲跑确认唯一改动文件即方案文档（+185 行），代码零改动，报告声明属实；
- cordcode-ios **main 工作树** @ fe421cdc：路径/分支/提交/干净状态记录齐全；`git diff bd46169..fe421cdc --stat -- <三锚点文件>` 亲跑为空，「锚点文件零改动、无行号漂移」声明属实；
- openai/codex @ e72da2b：`git rev-parse FETCH_HEAD HEAD` 亲跑两者相等、`git status --porcelain` 干净，与报告及方案 §2.1 记录一致。

**未覆盖（问题所在）**：

- r2 报告全文（grep `native-message-timeline|工作树|worktree` 亲跑核实）**未提及**与评审任务同分支族的配套 iOS 工作树 `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`（feat/ios-native-message-timeline @ f4a81ee4）——无工作树枚举记录、无同一功能分支族的解析过程、无「为何 iOS 来源用 main 工作树而非同族工作树」的说明或裁决记录。iOS 来源是静默选用 main 工作树（仅以方案 §2.1 记录的 bd46169 为锚，未处理同族候选的存在）。
- P0 强制来源清单模板的「任务预期分支」（iOS 侧：任务预期的工作树/分支未记录）与「预期产品特性」两字段未记录。

### 2.4 缓解事实（本轮亲核，不改变完整性判定）

- `git -C cordcode-ios diff fe421cdc..f4a81ee4 --stat -- '*ProjectionStore.swift' '*CCCodeBridgeModels.swift' '*ChatViewModel.swift' '*CCCodeBridgeTransport.swift'` 亲跑**为空**；且 main（fe421cdc）是 feat 分支祖先，feat 分支新增提交均为 docs(plan) 类提交——即 r2 报告核验所用的四个 iOS 锚点文件在「方案记录 bd46169 / 评审所用 main fe421cdc / 同族配套 f4a81ee4」三处**字节级一致**。r2 的 iOS 锚点结论（§1 抽查 6/7 号亦含 iOS 锚点）在任何候选来源下均成立，**APPROVED 结论的锚点依据未受污染**。
- 本问题属来源清单完整性/程序性缺陷，非锚点有效性缺陷。

## 3. 问题清单

1. **[来源清单完整性] 配套工作树未覆盖**：r2 报告来源清单未记录与评审任务同分支族的配套 iOS 工作树 `cordcode-ios-native-message-timeline`（feat/ios-native-message-timeline @ f4a81ee4），无工作树枚举、无分支族解析记录，iOS 来源静默使用 main 工作树。违反任务项目规则（AGENTS.md P0）「存在多个工作树时，必须先按 P0 来源门解析同一功能分支族，再读取源码或形成结论」及元复核要求的「含配套工作树，无论是否实际引用」。缓解：四个 iOS 锚点文件在全部三个候选 ref 下字节级零差异（本轮亲核），锚点结论未受污染。
2. **[来源清单完整性] P0 模板字段不全（低严重度）**：来源清单缺「任务预期分支」（iOS 侧未记录任务预期的工作树/分支）与「预期产品特性」两个字段的记录（后者在纯文档评审、无构建/安装场景下材料性低，但 P0 模板对「评审」同样强制要求记录，报告亦未标注 N/A 理由）。

## 4. 边界声明

- 本轮唯一写入文件为本报告；方案、r1/r2 报告、产品代码、两仓源码一律只读；未 commit、未部署、未构建。
- 复核仅覆盖任务指定的两项范围；r2 报告的评审逻辑、意见处置、门控一致性等不在本轮复核范围内，未作复核。
- 本轮未持有 r2 评审的任务简报原件；r2 报告仅在其 MacBridge 行声明「与简报声明一致」，iOS 侧未声明任何简报指定的配对——若简报曾显式指定 iOS 配对来源，应补记于报告来源清单方可满足 P0，当前报告内无此记录。
