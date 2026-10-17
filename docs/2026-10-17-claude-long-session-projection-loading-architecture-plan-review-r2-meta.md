# 元复核报告：R2 评审通过结论的定向复核

日期：2026-10-17
复核员：元审核员-第2轮（plan-reviewer 契约，定向复核模式）
对象：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r2.md`（verdict: APPROVED）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`
方案 SHA-256（本轮实测，与派发记录及 R2 报告头一致）：`9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18`

复核范围（仅此两项，按任务指定）：(1) 锚点抽查——从通过报告中随机抽 ≥3 处带行号引用逐行核对；(2) 来源清单完整性——通过报告的来源清单是否覆盖任务项目规则要求的全部来源组合。**结论：confirm=true，未发现问题。**

## 1. 锚点抽查

抽样方法：以 R2 报告 §2.3「本轮亲核」锚点表（约 25 处带行号引用）为总体，python3 `random.Random('2026-10-17-meta-r2')` 洗牌后取前 6（满足"≥3 且随机"；种子固定可复现）。逐行核对结果全部命中：

| # | R2 报告引用 | 实际文件内容（本轮 Read 亲核） | 行号 | 语义 |
| --- | --- | --- | --- | --- |
| 1 | `go-bridge/projection_kernel.go:266`（`digestProjectionSourcePrefix` 签名） | :266 `func digestProjectionSourcePrefix(r io.Reader, cursor int64) (string, error) {`，函数体恰止于 :278 | ✓ | ✓ |
| 2 | 同上 `:272`（顺序读全部 prefix 再哈希） | :272 `n, err := io.CopyN(h, r, cursor)`（:270 `sha256.New()`，:277 `hex.EncodeToString`） | ✓ | ✓ |
| 3 | `go-bridge/projection_kernel.go:280` / `:1123` | :280 `func (s *ProjectionCheckpointStore) LoadValidated(`；:1123 `checkpointHit := false` | ✓ | ✓ |
| 4 | `go-bridge/projection_window_handler.go:124-126`（hydrating retryable） | :124 `case errors.Is(err, errProjectionHydrating):` / :125 `code = "projection.hydrating"` / :126 `retryable = true`，逐字符命中 | ✓ | ✓ |
| 5 | iOS `ProjectionStore.swift:145`（hard cap 文档首行）/ `:152` | :145 `/// Client-side open hard cap for `pull` (default 15s, matching the committed SSV2 contract).`；:152 `var pullHardCapNanoseconds: UInt64 = 15_000_000_000` | ✓ | ✓ |
| 6 | iOS `think.md:3343` / `:3491` | :3343 `2. 历史页 RPC timeout **15s**（健康路径 <1s；15s 足够慢网，又能 fail-closed）。`；:3491 `loadOlder → fetchMessagesPage → rpc("get_session_messages", 15s)`（:3489 节标题「## 触发链」） | ✓ | ✓ |

锚点文件来源身份（保证核验对象 = 报告核验对象）：

- Mac 侧 `projection_kernel.go` / `projection_window_handler.go`：MacBridge 工作树未提交项仅 3 个 docs 文件（`git status --porcelain` 实测），go-bridge 文件均为提交 `11caa133` 内容。
- iOS 侧 `ProjectionStore.swift` / `think.md`：iOS 工作树脏清单（实测 8 路径，见 §2）不含这两个文件，均为提交 `1e895ea9` 内容——与 R2 §2.3「复用 R1」的锚点身份论证一致。

抽查 6/6 命中，行号与语义均吻合，无一错位。

## 2. 来源清单完整性

任务项目规则（repo CLAUDE.md P0 来源门 + 评审与文档来源节）要求的来源组合，对照 R2 报告 §2.1 覆盖情况：

| 规则要求的来源组合 | R2 覆盖 | 本轮独立复核 |
| --- | --- | --- |
| MacBridge 工作树（路径+分支+完整提交+未提交状态） | §2.1 行 1：`11caa1332cf4…` @ `feat/ios-native-message-timeline` + 未跟踪枚举；绝对路径在方案 §2.1（`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`），报告以「方案 §2.1 声明」列显式对照复核 | ✓ 实测一致（`git rev-parse HEAD` = `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`） |
| 配套 iOS 工作树（含未提交逐项） | §2.1 行 2：`1e895ea9…` @ `feat/ios-native-message-timeline` + 8 路径枚举（F-10 漂移意见） | ✓ 实测一致；`git worktree list` 证实 iOS 仓仅 `cordcode-ios-native-message-timeline` 一个工作树在该分支，配对唯一无歧义；8 脏路径逐项吻合（M pbxproj / M LiveTailFollowController / M NativeTimelineViewController / M TimelineStreamingMarkdownCell / M LiveTailFollowControllerTests / ?? StreamingHeightPresentation / ?? NativeTimelineSmoothHeightTests / ?? StreamingHeightPresentationTests），且全部锚点文件不在脏清单 |
| Codex 上游 checkout | §2.1 行 3：`ce0323f2…` @ `main`，干净 | ✓ 实测一致（`/Users/jacklee/Projects/codex`，`git status --porcelain` 为空） |
| 正式 Mac runtime 二进制 | §2.1 行 4：符号计数（`grep -c get_session_projection_window` = 2）+ PID 8303 监听 8777 + 「runtime 与源码 HEAD 不同、需分层下结论」告诫 | 覆盖项齐备（本轮未重跑 strings/lsof，非本次复核范围的否决点；报告已自带分层告诫，方案 E-3/S-0 承接运行级取证） |
| Claude Code 上游 | 按本仓上游源码优先门，Claude Code 无本机官方源码 checkout（官方文档 + SDK 类型契约 + cli 定点取证），不构成需列入清单的工作树来源组合 | 不适用 |

结论：R2 报告（连同其显式对照的方案 §2.1 完整清单，含绝对路径）覆盖了任务规则要求的全部来源组合，含配套 iOS 工作树；未提交状态逐项列出，且报告以 F-10 主动记录了快照漂移并要求 S-0 按 P0 门再生成。无缺失组合。

## 3. 复核过程记录

- 抽样与核验命令：`shasum -a 256 <方案>`（命中）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`git worktree list`（MacBridge 仓与 iOS 仓各一次）；锚点逐行 Read（本报告 §1 表即实际读到的行内容）。
- 本轮唯一写入文件：本报告。方案、R1/R2 报告、三仓源码、iOS 8 个未提交文件一律只读未触碰。
- 未复核（超出本次两项范围，按要求不做）：R2 意见闭合表逐项重判、样本字节复测、日志行核验、R1 复用锚点的重读。

## 4. 结论

- 锚点抽查：6/6 命中（行号 + 语义双吻合）。
- 来源清单完整性：全部必需来源组合覆盖，配套工作树解析唯一。
- **confirm=true，issues 无。R2 的 APPROVED 结论在本次定向复核范围内未被推翻。**
