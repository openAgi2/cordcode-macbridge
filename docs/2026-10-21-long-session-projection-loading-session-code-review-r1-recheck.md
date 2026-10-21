# Claude 长 session 投影加载分支——评审 R1 修复轮复核（独立 · 只读）

> **复核结论：`PASS-WITH-FINDINGS`**。四项阻断/重大（BLOCKER-1/2、MAJOR-1/2）及 MINOR-6 已修复，并经独立复核确认关闭。但分支在合并前还必须处理一个 major：**RC-1**。
>
> - **已关闭**：
>   - BLOCKER-1：组合源严格语义。独立红绿复现成立；R1 的三个复现在 HEAD 上全部等于诚实重建；生产 runtime（`d88920e6`）自 12:10 起 8 次 Claude hydrate 全部为诚实重建，0 次折叠。
>   - BLOCKER-2：Claude descriptor 已撤回 `turn_detail_chunks_v1`，iOS 入口按 descriptor 门控隐藏。
>   - MAJOR-1：命令回显豁免，`/model` 场景结算 completed。
>   - MAJOR-2：写前重建目录，失败可观测。
>   - MINOR-6：协议镜像两仓字节一致。
> - **RC-1（major，修复轮未覆盖）**：打开**正在运行**的 Claude 会话时，冷开 hydrate 与 relay 注册存在竞态。仓库里既有的测试 `TestHandleGetSessionProjectionRunningSessionWithinBudget` 在本分支的测试族运行中**稳定失败**（d3b72f02 与 cedf0528 都是 3/3），main 上 3/3 通过。错误为 `claude relay coverage: no registered relay loop`。这就是 R1 MAJOR-1 中标为 PLAUSIBLE 的附带风险，本轮在测试层面证实。回执里「projection/hydrate 家族定向全绿」与此不符。
> - **RC-2（minor）**：BLOCKER-2 只修了客户端可见面。Mac 服务端仍接受 Claude 的 `session_turn_items`，与修订后的协议 pack（backend 门 = `{codex-remote}`，其余返回 `unsupported_capability`）不一致；任何发出该请求的客户端仍会把 failed 态写进共享投影。
> - 另有 7 个 nit（证据与文档层面），以及 4 条范围外观察（不计入结论）。
>
> **owner 下一步（仅建议）**：① 分支继续不合并、不 push；② 先修 RC-1（relay 的 coverage 注册改为同步完成后再进入 hydrate），并让该测试在其测试族中稳定通过；③ 顺手把 RC-2 的服务端 backend 门收回到 codex-remote。

---

## §0 来源清单（实测时点）

### 0.1 门点

| 门点 | 时点（CST） | 结果 |
| --- | --- | --- |
| ① 读源码前 | 2026-10-21 14:46:29 | 两仓 HEAD 与回执一致、工作区干净；任务分支各对应唯一工作树 |
| ② 写本文件前 | 2026-10-21 15:10:19 | 两仓 HEAD 未变、干净；目标文件不存在、不在 ignore 内 |
| ③ 构建/安装/部署 | — | **不适用**：只读复核，未构建、未安装、未重启任何进程 |

### 0.2 两仓来源（P0 七字段）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=cedf0528ae7fd4b16f746f1b0f2309e546e2295e
未提交状态=干净（写入本文件前）
任务预期分支=feat/claude-long-session-projection-loading（回执指定复核范围 2a625bff..cedf0528）
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / 64a386d68ae5e11f109b19bec85283bf2486b562
预期产品特性=Claude 冷开全量 + S-4 暖开；组合源 cut 严格（推进即诚实重建）；物化 schema 2；Claude 不声明 turn_detail_chunks_v1；命令回显 turn 不进 coverage

仓库路径=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=64a386d68ae5e11f109b19bec85283bf2486b562
未提交状态=干净
任务预期分支=feat/claude-long-session-projection-loading（回执指定复核范围 f65e7178..64a386d6）
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / cedf0528ae7fd4b16f746f1b0f2309e546e2295e
预期产品特性=claudePreviewRevision 2；清除缓存后 save 重建目录且失败可观测；协议镜像与 Mac 一致
```

### 0.3 运行态（只读观察）

- 8777 监听者为 `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，PID 82698，PPID 82668（`CordCodeLink`），`lstart Wed Oct 21 12:10:29 2026`。没有任何非 `/Applications` 实例。
- 二进制 mtime 为 10-21 11:36；`strings` 可见内嵌提交 `d88920e692b3`，以及新校验文案 `composite source cut mismatch at %d (stored=%d current=%d)`。
- **与回执差异**：回执写的是 PID 77773（11:36:39 起）。该代际已被 12:10:29 的重启替换（二进制相同）。当前 `go-bridge.log` 从 12:10:29 开始，77773 代际的日志已不在磁盘上（见 N7）。

### 0.4 证据类别与复现

- 【源码】行号均取自 0.2 的 HEAD。iOS 路径约定同 R1：`Models/`、`Services/` 等相对于 `OpenCodeiOS/OpenCodeiOS/`，`OpenCodeiOSTests/` 相对于 `OpenCodeiOS/`。
- 【scratch】在仓库外 `/tmp` 用 `git archive` 取出 `cedf0528`（head）、`d3b72f02`（pre，修复前）、`f868af18`（main）三棵树，以及 4 个各撤回一处修复的 red 变体（撤回方式见 §2）。只跑定向 `GOTOOLCHAIN=local /tmp/gobin/go test ./go-bridge|./agent/claudecode -count=1 -run '<主题>'`，没有 clean、没有全量包、没有改动 DerivedData。复核结束后已清理 scratch 目录；以下用例都可以按文中描述重建。
- 【生产日志】`~/Library/Application Support/CordCode Link/logs/go-bridge.log`（12:10:29 起），只读 grep。route id、凭据、完整设备 ID 一律不抄录。
- 【推断】单独标注。
- iOS 侧只做了静态阅读；回执中的真机结果标为 owner-attested，未独立复现。

---

## §1 复核范围

| 仓库 | 提交 | 类型 | 文件 |
| --- | --- | --- | --- |
| Mac | `c988f1b2` | 代码 | `projection_kernel.go`（注释）、`projection_materialization.go`（严格校验 + schema 2）、`projection_materialization_suffix_regression_test.go`（新） |
| Mac | `3cdf884f` | 代码 | `agent/claudecode/wire_descriptor.go`、`projection_window_v2_test.go` |
| Mac | `d88920e6` | 代码 | `claude_relay_coverage.go`、`claude_coverage_trailing_test.go`、`claude_coverage_command_echo_regression_test.go`（新） |
| Mac | `cedf0528` | 文档/状态 | 完成报告勘误 + §8、CHANGELOG、think.md、exec-plan、`unified-bridge-protocol.md` |
| iOS | `64a386d6` | 代码 + 镜像 | `ProjectionBaseCacheStore.swift`、`ProjectionBaseCacheTests.swift`、CHANGELOG、`unified-bridge-protocol.md`、read-file-v2 样本 |

`2a625bff..cedf0528` 区间是 **4** 个提交；回执写的「5 提交」把 R1 报告提交 2a625bff 也算了进去。iOS 是 1 个提交。修复轮没有改动任何范围外文件。

---

## §2 逐项复核

### BLOCKER-1 → **已关闭**（CONFIRMED）

- 【源码】`go-bridge/projection_materialization.go:341-346`：组合源任一段 `stored.Cursor != segment.Cursor` 即返回 `ErrProjectionCheckpointInvalid`。`:34`：`materializationSchemaVersion = 2`，`:305` 以 `!=` 比对，v1 head 全部失效。
- 【源码·可达性】严格门之后能到达 kernel 组合源分支的，只有 `stored_i == current_i` 的情况（`projection_kernel.go:1165-1177`），因此 `startOffset == endOffset`。`produceProjectionHydrateSource` 对组合源在 `handlers_projection.go:1276-1278` 直接返回，不读文件。后缀 API `GetRichSessionHistoryAtSegmentsFrom` 全仓唯一的调用点是 `handlers_projection.go:1315`，现在生产上不可达。
- 【scratch·独立红绿】red1（删除 `:341-346`）下，`TestCompositeSuffixAdvanceInvalidatesCheckpoint` 与 `TestClaudeRestartAfterGrowthMatchesFullRebuild` FAIL；恢复后 PASS。`TestMaterializationAcceptsSuffixAdvance`（单源）在各变体中都 PASS，单源语义没有变。
- 【scratch·R1 复现回归】在 HEAD 上重跑 R1 的复现，结果均与诚实重建逐 turn 一致：
  - 重启后增长（`TestReviewS2RestartSuffixFoldE2E`）；
  - 同 runtime 内增长后重拉（`TestReviewS2InRuntimeRepullFolds`）；
  - 损坏后再增长、再重启（`TestReviewS2CorruptionPersistsAcrossCatchUpAndRestart`，`repaired=true`、`matches honest=true`）。
- 【生产日志】12:10:29 以来共 8 次 `stage=hydrate_commit backendID=claude`，全部是 `checkpointHit=false startCursor=0`，0 次折叠。
  - `608e03af` 在 12:36:56 重建（cut 47,399,826）；14:36:14 文件长了 1,153 字节，再次走诚实重建。这是严格门在生产路径生效的直接证据。
  - `ce5ca3c8`（14:36:40）、`3b95f2e6`（14:41:29）同样是重建。
  - 这些与 owner「坏历史会话经双 bump 重建恢复」的验收一致。
- iOS：`Models/ProjectionBaseCacheStore.swift:41` 中 `currentClaudePreviewRevision = 2`，只作用于 Claude 家族；种子写入沿用该常量（`ProjectionRestoreThenIncrement.swift:665`）。
- 残留：读取越界缺陷本身还在，但已不可达（见 N1）。

### BLOCKER-2 → **客户端面已关闭；服务端门残留见 RC-2**

- 【源码】`agent/claudecode/wire_descriptor.go:33-39` 只剩撤回注释，能力已删除。iOS 的入口门 `Services/Bridge/CCCodeBridgeBackendClient.swift:252-253` 读取的是**backend descriptor** 能力表（与 todos、workspace_diff 等同一来源，`:219-253`），不是连接级 echo。因此 Claude 会话不再显示「加载详细过程」。
- 【scratch·红绿】red3（把能力加回 descriptor）下，`TestClaudeDescriptorDoesNotDeclareTurnDetailChunks` FAIL。
- 【源码】协议 pack 已在 `unified-bridge-protocol.md:270`、§11.7 开头、`:1007-1013` 三处加了撤回注。两仓现在字节一致（`f9f2a8b3…`）。

### MAJOR-1 → **已关闭**（CONFIRMED）

- 【源码】`go-bridge/claude_relay_coverage.go:290-292`：`Assistant == nil && claudeUserTextIsCommandEcho(User)` 的 turn 不进 coverage，由 commit 收尾的 `settleUnansweredCommandEchoTurns`（`projection_kernel.go:1428`）结算为 completed。悬挂 tool_use 仍在 coverage 内（`TestTrailingCoverageExemptsCommandEchoTurn` 含反断言）。
- 【scratch·红绿】red2（删除 `:290-292`）下，`TestTrailingCoverageExemptsCommandEchoTurn` 与 `TestTrailingCommandEchoSettlesCompletedAfterHydrate` FAIL。R1 的复现 `TestReviewTrailingCommandEchoUnderCoverage` 在 HEAD 上为 `user-line-4 status="completed"`。
- 对抗核对：该谓词（`projection_reducer.go:3237-3250`）与 settle 都是 main 上已有的。这次的豁免集严格窄于 main 在 d78100ee 之前的过滤（main 排除了全部 `Assistant == nil` 的 turn），所以相对 main 没有新增暴露面。
- 测试有效性见 N3。

### MAJOR-2 → **已关闭**（静态 CONFIRMED；真机结果 owner-attested）

- 【源码】`Models/ProjectionBaseCacheStore.swift:197`：每次写 temp 前先幂等执行 `createDirectory(withIntermediateDirectories:)`。`:206-211`：失败时打 `fgTrace("[BaseCache] save FAILED …")`。
- 【源码·测试】`OpenCodeiOSTests/ProjectionBaseCacheTests.swift:115` 覆盖「删目录 → 同一 actor 再 save → load 命中」；该类现有 12 个用例，与回执中的 12/12 一致。

### MINOR-6 → **已关闭**

- `unified-bridge-protocol.md`、`samples/read-file-v2/hello-ack-read-file-v2.json`、`bridge-v1.md` 两仓 SHA-256 相同。`diff -rq docs/protocol` 剩下的 3 处目录级差异（`message-web-fixtures`、`management-file-read`、`turn-presentation-v1`）在两仓 merge-base 时就已存在，不属于本分支。

### 登记另案的 R1 项

- MINOR-2（fence 泄漏）：R1 的复现 `TestReviewAnchorNewerInvalidItemsViewLeaksFence` 在 HEAD 上仍复现（`projection snapshot already pending`），与「另案」登记一致。
- 其余 MINOR-1/3/4/5/7/8、NIT-1…9 没有改动，本轮未重新验证。

---

## §3 新发现 / 残留

| ID | 级别 | 仓库 | 一句话 | 置信度 |
| --- | --- | --- | --- | --- |
| RC-1 | major | Mac | 运行中会话冷开与 relay coverage 注册竞态（d78100ee 放大，修复轮未覆盖）；既有测试在本分支稳定失败 | CONFIRMED（测试级）/ PLAUSIBLE（生产） |
| RC-2 | minor | Mac | BLOCKER-2 服务端 backend 门未收回，与修订后协议 pack 不一致 | CONFIRMED |
| N1–N7 | nit | 双仓 | 潜伏读取缺陷、schema bump 无测试钉住、测试断言面、文档与时间戳 | — |

### RC-1　运行中 Claude 会话冷开：coverage 与 relay 注册竞态（major）

- **机制【源码】**：
  - d78100ee 把 coverage 的需求集从「带 pending Ask 的 turn」扩到「全部尾部非终态 turn」（`claude_relay_coverage.go:277-293`），任何**正在运行**的普通 turn 都会请求 coverage。
  - `requestCoverage` 遇到尚未注册的 relay 时返回 `errClaudeCoverageNoRelay`（`:174-178`），hydrate 随之 `markHydrateFailed("projection.coverage_failed", retryable)`（`handlers_projection.go:1159-1164`）。
  - relay 的注册发生在 goroutine 内部：beforeHydrate 钩子（`handlers_projection.go:134-139`）只是启动 `go h.claudeSessionFileRelayLoop(...)`（`handlers_relay.go:140/146`）；loop 先做 `findClaudeSessionFile` 等准备工作，之后才在 `:857` 执行 `register`。没有任何一方等待注册完成。hydrate goroutine 的解析加 coverage 只要先于注册完成，冷开就会失败。
- **证据【scratch·测试】**：仓库既有测试 `go-bridge/handlers_projection_test.go:2124` `TestHandleGetSessionProjectionRunningSessionWithinBudget`，场景是 live 进程加一个在飞的普通 prompt。

  | 树 | `-run 'TestHandleGetSessionProjection'` ×3 | 单独运行 ×8 |
  | --- | --- | --- |
  | main `f868af18` | 3/3 PASS | 8/8 PASS |
  | pre `d3b72f02` | **3/3 FAIL** | 8/8 PASS |
  | HEAD `cedf0528` | **3/3 FAIL**（净树复跑 3/3 FAIL；实际工作树复跑 FAIL） | 两轮：**5/8**、8/8（随时序波动） |

  失败信息：`handlers_projection_test.go:2163: running-session cold open failed: claude relay coverage: no registered relay loop`。
- **生产【生产日志】**：保留日志（当前文件 + `.log.1`）中 `no registered relay loop` / `projection.coverage_failed` 出现 0 次。【推断】大 transcript 的解析耗时通常给 relay 留够了注册时间；小的、正在运行的会话（例如刚开始的会话）暴露最大。iOS 侧表现为一次可重试的失败加重试延迟。
- **CI【推断】**：`.github/workflows/ci.yml:40` 执行 `go test ./go-bridge/... -count=1`，本地测试族运行稳定失败，CI 很可能变红。未验证：本地工具链是 go1.26.6，CI 用 go.mod 的 1.25。
- **与 R1 的关系**：R1 MAJOR-1 写过「coverage 覆盖面变大后，更多 hydrate 会碰到 `errClaudeCoverageNoRelay`……未证实」，本轮在测试层面证实。修复轮的 `d88920e6` 只豁免命令回显，不覆盖普通在飞 turn。
- **建议（仅建议）**：在启动 loop goroutine 之前同步完成 coverage 注册（register 前移到 `startClaudeSessionFileRelayAt`），或者让 `requestCoverage` 在 relay 正在启动时有界等待注册。不要退回到「不 coverage 在飞 turn」，否则 d78100ee 要解决的悬挂卡死会回来。修完后该测试应在测试族运行和单独重复运行中都稳定通过。

### RC-2　Claude `session_turn_items` 服务端门未收回（minor）

- 【源码】`go-bridge/session_turn_items_handler.go:71-76` 仍然接受 `claude` / `claudecode`，二进制中可见对应文案 `session_turn_items supports codex-remote and claude`。而修订后的协议 `unified-bridge-protocol.md:1007-1013` 规定 backend 门回到 `{codex-remote}`，「其他 backend……调用 → `unsupported_capability`（不可重试），不触发任何上游拉取」。
- 【scratch】R1 的 `TestReviewClaudeTurnItemsOnChunksConnection` 在 HEAD 上仍然返回 ack `failed(upstream_error)`，内核 turn 被写成 `detailLoadState="failed"`。
- 触发：当前 iOS 按 descriptor 门控，不会发这个请求；只有旧客户端、畸形客户端或以后的回归会触发。因此休眠只靠客户端维持，服务端没有兜底，失败态仍会进共享投影与 checkpoint。
- 建议：服务端 backend 门回到 codex-remote（或按该 backend 的 descriptor 能力门控），Claude 返回 `unsupported_capability`；补一条 chunks 连接上 Claude `session_turn_items` 的 handler 级测试。

### Nits

- **N1 潜伏的读取越界缺陷**：`agent/claudecode/continuation.go:371-377` 依旧先 `LimitReader` 后 `Seek`，R1 的 API 级复现在 HEAD 上仍是 20 条（应为 10 条）。现在生产不可达，依赖两道门（`projection_materialization.go:341` 与 `handlers_projection.go:1276`）。`TestLoadClaudeContinuationHistoryAtSegmentsFromSuffixEquivalence` 对它仍然失明。建议顺手改成 `io.NewSectionReader(file, from, cursor-from)`，或删除 Claude 的 offset provider，免得日后重新放开推进时复发。
- **N2 schema bump 没有被测试钉住**：red4（把 `materializationSchemaVersion` 改回 1）下全部用例 PASS。原因是 `TestMaterializationV1HeadInvalidatedBySchemaBump` 用相对值 `materializationSchemaVersion - 1`（`projection_materialization_suffix_regression_test.go:119`），测试名声称的「bump 生效」不成立。建议写入字面量 1 的 head，或断言常量 ≥ 2。
- **N3 MAJOR-1 handler 测试的「不标失败」断言是空的**：`claude_coverage_command_echo_regression_test.go:85` 检查 `proj.Execution.Phase` 是否为 failed，但 reducer 的执行态字面量只有 `idle` / `running` / `requires_action` / `none`。真正的失败徽标在会话注册表（`handlers.go:3413` `markSettled`），没有被断言。status=completed 那条断言是有效的（red2 已证明）。
- **N4 完成报告身份与语义**：
  - L18-20 的来源/运行态仍是修复前的值（`66a6de16`、PID 40490、`5e4d0a97b9c2`）。
  - §8 没有记录修复轮的部署身份，而 exec-plan `review-r1-fixes` 写的是「运行态核验见 §8」。
  - S-2 行仍写「增量重启恢复」，但 Claude 现在只要增长就整份重建，只剩精确命中可以恢复，应写明这一语义。
- **N5 exec-plan 时间戳与措辞**：`review-r1-fixes.verification.checked_at` 与 `updated_at` 都是 `2026-10-21T06:30:00+08:00`，早于它所证明的修复提交（11:21）和部署（11:36）。回执说的「审计降级标注（unsupported-done-must-downgrade）」实际只是 notes 追加，相关 todo 的 status 与 attestation 都没变。
- **N6 bump 的理由不准确**：「anchor-newer 无法表达删除虚构 turn」不成立。Mac 侧 `projection_anchor_newer.go:66/84/255` 有 `RemovedTurnIDs`，iOS 侧 `ProjectionRestoreThenIncrement.swift:487-488` 会按 tombstone 处理。选择 bump 这个决定本身正确：自愈没有验证过，bump 更稳。建议把 CHANGELOG 和注释里的理由改准确。
- **N7 回执计数与身份**：提交数见 §1。运行代际见 §0.3：77773 代际的日志已不在磁盘，12:10 之前的验收证据无法从磁盘复核；12:10 之后的日志与验收结论一致。

---

## §4 回执声明核对

| # | 回执声明 | 复核 | 结论 |
| --- | --- | --- | --- |
| 1 | BLOCKER-1：恢复 main 严格语义；单源 digest 不变 | 源码、红绿、R1 复现回归、生产日志 | ✓ |
| 2 | schema 1→2，v1 存档失效 | 常量与 `!=` 校验成立；但测试钉不住 bump | ✓ / ⚠ N2 |
| 3 | iOS `claudePreviewRevision` 1→2；「anchor-newer 无法表达删除」 | 常量成立；理由不准确 | ✓ / ⚠ N6 |
| 4 | BLOCKER-2：撤回声明；协议三处撤回注；测试反转 | 成立；red3 能变红 | ✓（服务端门见 RC-2） |
| 5 | MAJOR-1：复用谓词豁免命令回显，悬挂 tool_use 保留 | 成立；red2 能变红 | ✓（N3） |
| 6 | MAJOR-2：写前建目录 + fgTrace + 单测 | 静态成立 | ✓ |
| 7 | 新增 6 个回归 | Mac 5（suffix_regression 3 + 纯函数 1 + handler 1）+ iOS 1 | ✓ |
| 8 | 红绿：摘除修复后 4 个可红测试全部 FAIL | 独立复现：red1 两个、red2 两个，另 red3 一个；schema bump 无可红测试 | ✓（N2） |
| 9 | 部署：runtime `d88920e6`，PID 77773，11:36:39 | 二进制 ✓；当前进程为 PID 82698、12:10:29 起 | ⚠ N7 |
| 10 | iPhone16 安装 `64a386d6`；真机 12/12 + 10/10 | 用例计数与源码一致；安装与运行未独立复现 | owner-attested |
| 11 | owner 真机验收；608e03af 经重建恢复 | 12:10 后的生产日志显示三个受影响会话均已诚实重建 | ✓（佐证） |
| 12 | 完成报告 6 处勘误 + §8 | 6 处勘误内容准确 | ✓ / ⚠ N4 |
| 13 | exec-plan 审计降级标注；queue 30 todos | 30 项均为 done（21 self-attested / 9 re-verified）；降级只体现在 notes；时间戳早于修复 | ⚠ N5 |
| 14 | MINOR-6 已修 | 字节一致 | ✓ |
| 15 | 「projection/hydrate/anchor/detail 家族定向全绿」 | `-run 'TestHandleGetSessionProjection'` 在 HEAD 上 3/3 FAIL（RC-1） | ✗ |

---

## §5 范围外观察（不计入结论）

- **O1**：`TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 在 main（`f868af18`）上用本机 go1.26.6 同样失败（`system-subtypes.jsonl scanner/manifest mismatch`），是既有问题，可能与工具链有关。**勘误（2026-10-21 后续排查）**：工具链的推测不成立。真正原因是 135ba20a 把 `claudeTranscriptRelayEntry.Error` 声明为字符串，而 `system/api_error` 行的 `error` 是对象，导致 live 扫描器把这类行判为 poison、实时更新冻结。详见 `docs/2026-10-21-claude-live-relay-zero-event-and-api-error-poison-fix-plan.md` 的 RC-B。
- **O2 Claude live source-batch 拒收循环（既有）**：
  - 当前日志中，本评审所在会话 `b66193d7` 正被 `dev_c5ad…` 以 full_stream 观察。自 14:46:29 起，`Claude projection transition has no projection subevents` 之后接连出现 `Claude source batch gap`，并伴随 `projection hydrate failed code=projection.source_batch_rejected retryable=true`。截至 14:56 统计，同类失败已累计 107 次。
  - `.log.1` 中有 47 次、`.log.3`（10-13/14，本分支之前）中有 47 次同类记录，说明这是既有问题，并非本分支或修复轮引入。
  - 【推断】外部 Claude 会话在 iOS 上旁观时，live 更新会持续中断。建议另案排查。
- **O3**：命令回显谓词会把以「/」开头的普通 prompt（例如绝对路径开头的问题）误判为本地命令，这是 main 上已有的行为。`d88920e6` 复用该谓词，相对 main 没有扩大暴露。
- **O4**：未核实 iOS 其他离线层（SessionSnapshotStore / MessageCacheManager）是否也保存了损坏期的内容。owner 已手动清缓存，本机不受影响。

---

## §6 总结论

**`PASS-WITH-FINDINGS`**：修复轮针对 R1 的四个阻断/重大问题及 MINOR-6 全部有效，均有独立红绿、复现回归和生产日志佐证。**合并前必须先修 RC-1**：这是本分支（d78100ee）相对 main 引入的竞态，导致既有测试在其测试族中稳定失败。RC-2 与 N1–N7 建议随下一轮一并处理。本复核没有修改任何业务代码，没有构建、安装、部署或 push；仓库内只新增本文件。
