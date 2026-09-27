# codex-remote 断线韧性方案 r3 通过结论——元审核（定向复核）报告

- 日期：2026-09-26
- 审核员：元审核员-第 3 轮（按 `~/.zcode/agents/plan-reviewer.md` 硬边界工作；本轮唯一写入文件即本报告，其余一律只读）
- 复核对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r3.md`（r3 通过报告，verdict APPROVED）
- 方案：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`，本轮亲算 SHA-256 = `811ee4526abc6db0f9af44eeda96b19132fe419559c71dd24cf0463903d23208`，与派发记录及 r3 报告自报一致（评审对象身份确认）；正文哈希 `head -n 265 | shasum -a 256` = `5ab5f9208d362788d6685d4e6196475b0ce954ffb5fc348935e3119e8377cd1b` 亦亲算吻合
- 复核范围（仅此两项，未复核其他内容）：
  1. 锚点抽查——从 r3 报告随机抽样带行号引用，逐行核对实际文件（定位 + 语义）；
  2. 来源清单完整性——r3 报告来源清单是否覆盖任务项目规则（AGENTS.md P0 来源门）要求的全部来源组合（含配套工作树，无论是否实际引用）。
- 复核结论：**confirm = true**——锚点抽查 9/9 全部通过；来源清单覆盖全部来源组合（含配套工作树，逐项亲证）；无问题。一项零后果的形式性注记见 §2.4，不构成问题（理由在该节展开）。

---

## 1. 锚点抽查（复核范围一）

抽样方式：从 r3 报告 §3.2/§3.3/§3.4 的带行号引用中随机抽 9 处（超出「至少 3 处」要求），覆盖 MacBridge 代码、官方 openai/codex、cordcode-ios、macbridge think.md 四类来源，含 E-12/S-3/S-7/S-1a/E-3 承重锚点、OD-2/OD-3 裁决锚点与常规锚点。每处均在本轮亲自读取实际文件核对；MacBridge 侧在评审所在树读取（`git status --porcelain` 亲跑确认源码文件零改动，仅方案文档与 docs 有未提交项，工作树读取即 HEAD@07721783 内容），iOS 侧在 main 工作树读取（HEAD 亲核为 fe421cdc、干净），官方侧在 `/Users/jacklee/Projects/codex` 读取（HEAD==FETCH_HEAD==e72da2b 亲核、干净）。

| # | r3 报告引用 | 本轮核验（亲读文件/行） | 结果 |
| --- | --- | --- | --- |
| 1 | §3.2 `stream.go:390-407` ack 构造无 SegmentID（E-12 承重）；`:400-406` 摘录；`:397-399` disabled 早退 | 亲读 `agent/codex-remote/stream.go`：`:390 func (s *Stream) ack(env Envelope)`；`:400-406 _ = s.conn.Write(Envelope{Type: typeAck, ClientID:…, EnvID:…, StreamID:…, SeqID: env.SeqID})` 确无 SegmentID 字段；`:397-399 if disabled { return }` | ✓✓ 定位与语义均吻合 |
| 2 | §3.2 `envelope.go:45 SegmentID *int`；`stream.go:339` readLoop 逐 chunk ack；`:359-373`/`:362-367` 熔断 | 亲读 `agent/codex-remote/envelope.go:45` = `SegmentID *int`；`stream.go:339` = `s.ack(env)`（chunk/deliver 分支内）；`:362-373 observeTransportErrorSentinel` 内 `:363 bytes.Contains(payload, transportErrorSentinel)` → `:366 s.acksDisabled = true` | ✓✓ |
| 3 | §3.2 `backoff.go:15-56` 退避形状：`:16` base 1s、`:17` cap 30s、`:42-46` 封顶归零、`:47` jitter | 亲读 `agent/codex-remote/backoff.go`：`:16 reconnectBackoffBase = 1 * time.Second`；`:17 reconnectBackoffCap = 30 * time.Second`；`:42-46 if d >= cap { …attempt = 0 }`（封顶后归零重数）；`:47 jitter := 0.9 + 0.2*b.rng.Float64()` | ✓✓ |
| 4 | §3.2 `handlers_projection.go:603-605` source_inspection_failed 一律 retryable=true（S-1a 承重） | 亲读 `go-bridge/handlers_projection.go:603-605`：`h.markHydrateFailed(backendID, sessionID, "projection.source_inspection_failed", err.Error(), true,)`——末参 true 即 retryable | ✓✓ |
| 5 | §3.3 `websocket.rs:112-138` ack 游标（S-3/S-7/E-12 承重）：`:123`/`:129`/`:136` | 亲读官方 `codex-rs/app-server-transport/src/transport/remote_control/websocket.rs`：`:112 fn ack(...)`；`:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));`；`:129 let is_acked = envelope_cursor <= acked_cursor;`；`:136 self.buffer_by_stream.remove(&key);`（remove 仅在 ack 内、buffer 空时） | ✓✓ |
| 6 | §3.3 `protocol.rs:114-119` Ack 协议文档（E-12 承重，逐字） | 亲读官方 `.../remote_control/protocol.rs`：引文「Chunk acknowledgements carry `segment_id` so the sender can retain only the still-unacked wire chunks on reconnect.」逐字位于 `:118-119`；Ack 文档注释起于 `:116`，引用区间 `:114-119` 覆盖正确 | ✓✓ |
| 7 | §3.3 `enroll.rs:26` 5 分钟提前量（OD-3 量级先例） | 亲读官方 `.../remote_control/enroll.rs:26` = `const REMOTE_CONTROL_SERVER_TOKEN_REFRESH_SKEW_SECS: i64 = 5 * 60;` | ✓✓ |
| 8 | §3.4 `ProjectionStore.swift:164-181` 白名单不含 `projection.hydrate_failed`（E-3） | 亲读 `cordcode-ios/OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:164-181`：`:164 private static func isRetryablePullError(code: String?) -> Bool`；`:166-176` 恰 11 项（projection.hydrating/disconnected/not_connected/relay.not_ready/relay.closed/request_timeout/send_timeout/ping_timeout/connect_timeout/hello_timeout/websocket.closed），确无 `projection.hydrate_failed` | ✓✓ |
| 9 | §3.4 macbridge `think.md:45` OD-2 裁决 | 亲读 macbridge `think.md:45`：含「**待立项（协议面）**：桥侧 `backend_status_changed` 推送」 | ✓✓ |

**范围一结论：9/9 抽样锚点行号与语义全部吻合，无问题。**

## 2. 来源清单完整性（复核范围二）

### 2.1 任务项目规则要求

AGENTS.md（P0 来源门）要求：任何评审开始前必须为**每一个**涉及的仓库和工作树记录完整来源清单（仓库路径/分支/完整提交/未提交状态/任务预期分支/配套仓库路径/分支/提交/预期产品特性）；「存在多个工作树时，必须先按下方 P0 来源门解析同一功能分支族，再读取源码或形成结论」；且评审通过判定受四条否决条件约束（被引用仓库缺路径+分支+完整提交／行号符号实来自另一工作树／文档针对功能分支但核验用了 main／与评审范围重叠的未提交修改未纳入来源）。

### 2.2 本轮亲核的工作树实况

- `git worktree list`（macbridge 评审所在树亲跑）：3 树——`/Users/jacklee/Projects/cordcode-macbridge`（main @ 07721783）、`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`（feat/ios-native-message-timeline @ 0772178358fb582474eb5aada5f50b7d8608053e，评审所在树）、`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（detached @ b2b25235）
- `git worktree list`（cordcode-ios 亲跑）：3 树——`/Users/jacklee/Projects/cordcode-ios`（main @ fe421cdc136930e3729b6b3d5c4479d5ecb90df4，`git status --porcelain` 亲跑为空＝干净）、`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`（feat/ios-native-message-timeline @ 8eb262da2f3d8523f8270a9e5d2e4a9b0e5db7a5，干净，**同族配套树**）、`/Users/jacklee/Projects/cordcode-ios-plan-approval`（detached @ a336b68b）
- `git merge-base --is-ancestor f4a81ee4 8eb262da` 亲跑 exit 0（f4a81ee4 确为配套树祖先，与 r3 报告「移动目标」记录一致）
- `git rev-parse HEAD FETCH_HEAD`（codex checkout 亲跑）：两者相等 = e72da2b53805894878023d01949a25a082e0a5cb，`git status --porcelain` 干净，分支 main
- 评审所在树 `git status --porcelain` 亲跑：1 M（方案文档）+ 6 ??（2 个 zcode docs + r1/r2/r2-meta/r3 四份评审报告）。r3 报告记录「5 个未跟踪 docs」在其实评时点准确（r3 报告自身是评审期间新产生的第 6 个），与报告记录无矛盾。

### 2.3 r3 报告来源清单覆盖情况（逐项亲核属实）

r3 报告 §2.1「来源身份表」覆盖**全部**来源组合：

| 要求的组合 | r3 报告记录 | 本轮亲核 |
| --- | --- | --- |
| MacBridge 评审所在树 | 路径/分支/完整提交/未提交状态（方案未提交修订 + 未跟踪 docs 逐项）齐全 | ✓ 与实况一致 |
| MacBridge main 树、第三树（detached） | 枚举记录（§2.1 枚举项行） | ✓ `git worktree list` 逐树吻合 |
| cordcode-ios main 树（简报 ③ 显式指定的锚点来源） | 路径/分支/完整提交/干净状态齐全 | ✓ |
| **cordcode-ios 同族配套树**（无论是否实际引用） | 独立一行：完整哈希 8eb262da…、干净、f4a81ee4 祖先关系亲核 | ✓ 本轮重跑 merge-base 证实 |
| cordcode-ios 第三树（detached） | 枚举记录 | ✓ |
| openai/codex 官方 checkout（上游 source-first 门） | 完整哈希、HEAD==FETCH_HEAD、干净、main | ✓ 本轮重跑 rev-parse 证实 |
| 分支族解析 + 任务预期来源 | §2.1 表行标签（「简报 ③ 显式指定」「方案与评审所在树」）+ §2.1 注 ② + §4.1-②（owner 裁决：main 撰写、同步至当前分支、评审在当前树） | ✓ 记录在案 |

r2-meta 两项问题的修复状态（本轮亲核）：

- **问题 1（配套工作树未覆盖）——已在 r3 报告自身来源清单修复**：配套树独立成行、完整哈希、干净状态、祖先关系亲核、「移动目标」如实标注；且四 iOS 锚点文件跨候选 ref 零差异的承重声明本轮重跑证实——`git diff --stat fe421cdc <bd46169|f4a81ee4|8eb262da> -- <ProjectionStore.swift/CCCodeBridgeModels.swift/CCCodeBridgeTransport.swift/ChatViewModel.swift>` 三组亲跑**均空**。iOS 锚点结论与工作树选择无关。
- **问题 2（P0 模板字段不全）——实质已修复**：「任务预期分支」已记录于 r3 报告自身（表行标签 + §2.1 注 ② + §4.1-②）；「预期产品特性」修复落在方案 §2.1 七列表（本轮亲读 plan:55-61 证实每行均记 N/A + 理由「纯设计、无构建场景」，plan:259 勘误-2 留有更正记录），r3 报告 §4.1-⑤ 对该表的存在与 N/A 理由作了显式核验与认可。

P0 四条评审否决条件逐条核对：① 全部实际引用来源均有路径+分支+完整提交（评审树/ios main/codex/配套树四者全哈希亲核在案）→ 不触发；② 抽查 9 锚点全部来自报告声明的工作树（含跨 ref 零差异保障）→ 不触发；③ 方案针对功能分支，MacBridge 核验在 feat 评审树进行；iOS 用 main 树系简报 ③ 显式指定且四锚点文件全候选 ref 字节级一致 → 不触发；④ 评审树与评审范围重叠的未提交状态（方案文档 + 未跟踪 docs）已逐项纳入来源 → 不触发。

**范围二结论：来源清单覆盖任务规则要求的全部来源组合（含配套工作树），完整性成立。**

### 2.4 形式性注记（不构成问题，如实记录）

r3 报告**自身**的 §2.1 来源表为六列（仓库/工作树路径/分支/提交/未提交状态/核验方式），未像方案 §2.1 那样把「预期产品特性」作为一列复述（该字段对评审报告本身定义上即 N/A——评审无产物/无构建场景）。本轮判定其不构成问题，理由：① 复核范围二的问句是来源清单是否**覆盖全部来源组合**——覆盖完整（§2.3 逐项亲证）；② 该字段的 N/A + 理由已存在于 r3 报告文本内（§4.1-⑤「N/A 理由（纯设计、无构建场景）成立」），且其实质修复已落在存续到实施期的 durable 产物（方案 §2.1 七列表，本轮亲读证实）——与 r2 报告当时「全文无任何 N/A 标注」的情形实质不同；③ P0 规则为评审通过判定义的四条否决条件均不涉及该字段，且全部不触发；④ 该缺口对来源身份、锚点有效性、任何结论均零后果。若 owner 按 r2-meta 问题 2 的字面要求（报告自身清单亦须记该字段或 N/A）从严掌握，可将本注记视为后续报告格式的改进项，但不影响 r3 通过结论的有效性。

## 3. 复核结论

- **confirm = true**：r3 通过结论有效。锚点抽查 9/9 吻合（含 E-12/S-3/S-7/S-1a/E-3/OD-2/OD-3 承重锚点）；来源清单覆盖全部来源组合（含配套工作树，完整哈希 + 祖先关系 + 跨 ref 零差异均本轮重跑证实）；r2-meta 两项问题在报告侧/方案侧的修复均经本轮亲核属实。
- 无问题清单（issues 为空）；§2.4 注记为零后果形式性观察，不列入问题。

## 4. 边界声明

- 本轮唯一写入文件为本报告；方案、r3 报告、历轮报告、两仓源码、官方 checkout 一律只读；未 commit、未部署、未构建、未采集任何运行证据。
- 复核仅覆盖任务指定的两项范围；r3 报告的评审逻辑、勘误 hunk 归属分析、门控一致性、R2-A1~A4 处置、audit-plan 接入等不在本轮复核范围内，未作复核。
- 本轮未持有 r3 评审的任务简报原件；r3 报告对简报内容的转述（如「简报 ③ 显式指定 iOS 锚点来源 = main @ fe421cdc」）本轮以方案 §2.1/§8 同源记录与工作树实况交叉印证，未见矛盾。
