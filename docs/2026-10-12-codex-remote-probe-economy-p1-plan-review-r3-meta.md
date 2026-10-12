# 元审核报告 r3：codex-remote 恢复循环探针经济性优化（P1）

- confirm: true
- 对象：`docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r3.md`（verdict APPROVED）
- 方案：`docs/2026-10-12-codex-remote-probe-economy-p1-plan.md`
- 方案 SHA-256：`28bd60b1904f753c896d9b9ac77b79a4aac46cce90bfa6465d8cf56e85a9c880`（与 r3 / 派发记录一致；本轮 `shasum -a 256` 复算相同）
- 范围：仅 (1) 锚点抽查；(2) 来源清单完整性。未重做全量评审。

## 本轮来源门（读取源码前）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=ff990f73be81a55f94e9f799c9b8a9fa7d0e1662
未提交状态=产品代码干净；未跟踪 5 个 docs 文件：本方案 + r1/r2/r2-meta/r3（本轮报告写入前计数；写入后将再增本文件）
任务预期分支=feat/ios-native-message-timeline worktree（纯 Mac runtime P1 方案；复核 r3 通过结论）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套分支=feat/ios-native-message-timeline
配套提交=3f179dfbfba06b530efcf03e3930edca6c5c921c
配套未提交状态=脏（status --porcelain 精确 274 行：272 M + 2 ??；M：docs/ 266、.exec-plan/ 6；??：.exec-plan/native-timeline-status-index.json、.exec-plan/state/plan-4f2d48b2dd54.json）。本轮未读 iOS 源码。
预期产品特性=cordcode-bridge-runtime（drivers 不变）

上游仓库路径=/Users/jacklee/Projects/codex
上游分支=main
上游提交=cc7ba33601286a751477832b01de7b6f8de0dd9c
上游未提交状态=干净
```

Mac 工作树枚举（`git worktree list`）：`cordcode-macbridge` main、`cordcode-macbridge-codex-timeline-parity`、本树、`cordcode-macbridge-plan-approval`（detached）、`cordcode-macbridge-widget-support`。iOS 同名功能分支族唯一对应为 `cordcode-ios-native-message-timeline`（其余为 main checkout、`codex/chatgpt-codex-timeline-parity`、`codex/ios-widget-support`，以及该树 `output/` 下 2 个 detached 实现暂存树 + `cordcode-ios-plan-approval` detached）。

相对 r3 快照的漂移（不计入 r3 缺项）：r3 文件 mtime `2026-10-13 00:23:36`；配套 iOS 在 `00:23:49` 从 r3 所记 `7e32a62163ef66ab855eb71d571b38b1823574da` 前进 1 提交到本轮 HEAD `3f179dfb`（`docs(ios): record word-fade decoration ink diagnosis`，含原 OpenCodeiOS 脏文件入提交）。porcelain 由 r3 的 275（273 M + 2 ??，含 OpenCodeiOS 1）变为本轮 274（272 M + 2 ??）。Mac 提交 `ff990f73` 与上游 `cc7ba336` 未漂移。

## (1) 锚点抽查

抽样方法：从 r3「锚点核验表」去重后的带行号引用，以
`sha256(plan_sha256 || "-r3-meta")` 前 16 hex 为种子（`9d0e3c13c25bbcdd`），`random.sample` 抽 5 处（要求 ≥3）。
逐行对照本仓 `ff990f73` / 上游 `cc7ba336` 实际文件。

| # | r3 原引用 | 实际位置 | 原文要点 | 行号 | 语义 |
| --- | --- | --- | --- | --- | --- |
| 1 | `pairing_persist.go:19-23` lead=60s / 600s 注释；调度 `:47-52` | 本仓 `agent/codex-remote/pairing_persist.go:19-23`；调度 `:47-52` | `:23` `ctrlTokenRefreshLead = 60 * time.Second`；注释写 E-9 五样本 600s；`:47-52` 按 lead 与 `ctrlTokenRefreshMinRetry` 早退 | 吻合 | 吻合 |
| 2 | `pairing.go:83-84` `ctrlRefreshExec` 声明 | 本仓 `agent/codex-remote/pairing.go:83-84` | `:83` `lastCtrlRefreshAttempt time.Time`；`:84` `ctrlRefreshExec func(context.Context) error` | 吻合 | 吻合（v3/F-8 声明点） |
| 3 | restore 测试 `pairing_test.go:278-280` | 本仓 `agent/codex-remote/pairing_test.go:278-280` | `authTokenFn` 注入返回 `"account-jwt", "acct", nil` | 吻合 | 吻合 |
| 4 | D4 Info 落点 `chatgpt_auth.go:52-56` Stat 早退 | 本仓 `agent/codex-remote/chatgpt_auth.go:52-56` | `loadChatGPTAuth` 入口；`:54-55` `os.Stat(bin)` 失败则 `"请先安装并登录 ChatGPT Desktop"`，发生在 spawn 前 | 吻合 | 吻合 |
| 5 | `chatgpt_auth.go:65` `exec.CommandContext` 为真实 spawn | 同文件 `:65` | `cmd := exec.CommandContext(ctx, bin, "-c", "model_provider=openai", "app-server", "--stdio")` | 吻合 | 吻合 |

另核（非抽样、不扩大范围）：r3 对 F-8 对照点 `pairing_persist.go:83-84` 为 `p.mu.Lock()` / `p.state.token = token`（声明在 `pairing.go`、调用点 `pairing_persist.go:58-61`）；以及表内高风险上游 `enroll.rs:26` SKEW、`:246-263` 三级判定（函数体 `:250-263`，闭括号 `:264`）、`mod.rs:460,704`、`server_api.rs:134-151` / `:27-28,366-386`（r3 已注明 `:387` 为空行）、`auth.rs:1-2,29-46,89-96,98-123,177-214`、`websocket.rs:1512-1520` 与 `:1366-1374`。行号与语义均吻合。

**锚点抽查：通过。** 5/5 行号与语义吻合。

## (2) 来源清单完整性

r3「来源与覆盖」已给出：

- 本仓路径 + 分支 + 完整提交 + 未提交（写入前 4 个流程 docs；产品代码干净）
- 配套 iOS 路径 + 分支 + 完整提交 + 未提交状态（porcelain 精确 275 = 273 M + 2 ??，并点名 `??` 两路径与 M 分类；声明本轮未读 iOS 源码）
- 上游 Codex 路径 + 分支 + 完整提交 + 干净
- 任务预期分支与预期产品特性
- Mac / iOS 工作树枚举，同名功能分支族唯一对应

对照项目 P0（本仓与 iOS 仓 `CLAUDE.md` 强制来源清单）及本次复核范围「含配套工作树，无论是否实际引用」：r2-meta issues#1（配套标「不适用」）在 r3 已闭合。被引用仓库（本仓、上游）均含路径 + 分支 + 完整提交；配套虽未引用源码，身份已展开。本轮现场身份与 r3 快照的差异发生在 r3 落盘之后（见上），不构成 r3 清单缺项。

## issues

无。

## 结论

锚点抽查 5/5 通过。来源清单覆盖本仓、配套 iOS 工作树与上游 Codex 三组身份。confirm=true。本报告不评价 r3 的设计通过结论，也不构成实施授权。
