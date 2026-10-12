# 元审核报告 r2：codex-remote 恢复循环探针经济性优化（P1）

- confirm: false
- 对象：`docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r2.md`（verdict APPROVED）
- 方案：`docs/2026-10-12-codex-remote-probe-economy-p1-plan.md`
- 方案 SHA-256：`81b94d991141a47c464201e29868a3daa3a6f1334f85037c5fcd0a07830ccb23`（29962 bytes，与 r2 / 派发记录一致）
- 范围：仅 (1) 锚点抽查；(2) 来源清单完整性。未重做全量评审。

## 本轮来源门（读取源码前）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=ff990f73be81a55f94e9f799c9b8a9fa7d0e1662
未提交状态=未跟踪 docs/2026-10-12-codex-remote-probe-economy-p1-plan.md、docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r1.md、docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r2.md；产品代码干净
任务预期分支=feat/ios-native-message-timeline worktree（纯 Mac runtime P1 方案）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套分支=feat/ios-native-message-timeline
配套提交=7e32a62163ef66ab855eb71d571b38b1823574da
配套未提交状态=脏（status --porcelain 275 行：273 M + 2 ??，含 .exec-plan/state 与 docs/ 大量修改）；本轮未读 iOS 源码
预期产品特性=cordcode-bridge-runtime（drivers 不变）

上游仓库路径=/Users/jacklee/Projects/codex
上游分支=main
上游提交=cc7ba33601286a751477832b01de7b6f8de0dd9c
上游未提交状态=干净
```

Mac 工作树枚举（`git worktree list`）：`cordcode-macbridge` main、`cordcode-macbridge-codex-timeline-parity`、本树、`cordcode-macbridge-plan-approval`（detached）、`cordcode-macbridge-widget-support`。iOS 同名功能分支族唯一对应为 `cordcode-ios-native-message-timeline`。

## (1) 锚点抽查

抽样方法：从 r2「锚点核验表」及正文带行号引用去重后，以
`sha256(plan_sha256 || "-r2-meta")` 前 8 字节为种子，`random.sample` 抽 5 处（要求 ≥3）。
逐行对照本仓 `ff990f73` / 上游 `cc7ba336` 实际文件。

| # | r2 原引用 | 实际位置 | 原文要点 | 行号 | 语义 |
| --- | --- | --- | --- | --- | --- |
| 1 | `auth.rs:1-2` 生命周期绑定注释 | 上游 `codex-rs/app-server-transport/src/transport/remote_control/auth.rs:1-2` | `//! Credentials and recovery bound to one remote-control login lifetime.` | 吻合 | 吻合 |
| 2 | `enroll.rs:246-263` `server_token_refresh_requirement_at` 三级判定 | 同文件 `:246-264`（函数体 `:250-263`） | 无 token/`expires_at`→Required；`expires_at <= now`→Required；`expires_at > now+SKEW \|\| next_refresh_at > now`→NotNeeded；否则 Proactive | 吻合 | 吻合 |
| 3 | `server_api.rs:134-151` NotNeeded no-op；Required 且 `next_refresh_at > now`→WouldBlock | 同文件 `:134-151`；`WouldBlock` 由 `:40-42` `retry_deferred` 的 `ErrorKind::WouldBlock` 给出 | `NotNeeded`→`Ok(())`；Required + `next_refresh_at > now`→`retry_deferred` | 吻合 | 吻合（WouldBlock 是 error kind，非该段标识符） |
| 4 | `pairing_persist.go:95-106` persist 无 account JWT | 本仓 `agent/codex-remote/pairing_persist.go:95-106` `persistedPairing` | 字段仅 client/env/ctrl/key 等，无 `token`/`accountID` | 吻合 | 吻合 |
| 5 | `ws.go:108-110` 握手失败不映射撤销 | 本仓 `agent/codex-remote/ws.go:108-110` | `handshake failed` 包装返回，不解析 401→`errPairingRevoked` | 吻合 | 吻合 |

另核（非抽样、不扩大范围）：r2 F-8 所主张的正确声明点 `pairing.go:83-84`（`ctrlRefreshExec`）与 `pairing_persist.go:83-84`（`p.mu.Lock()` / `p.state.token = token`）均与源码一致——这是方案误指、评审纠正，不计入本项问题。

**锚点抽查：通过。** 5/5 行号与语义吻合。

## (2) 来源清单完整性

r2「来源与覆盖」已给出本仓路径 + 分支 + 完整提交 + 未提交（当时为方案 + r1；r2 自身当时尚未落盘，合理），以及上游路径/分支/提交/干净状态；与本轮复核一致，未漂移。

缺项：配套 iOS 工作树被写成

```text
配套仓库路径/分支/提交=不适用（方案声明无 iOS 源码改动；本轮未读 iOS 源码）
```

项目 P0 来源门（本仓与 iOS 仓 `CLAUDE.md`）要求清单含「配套仓库路径/分支/提交」，且须先枚举工作树再选定同功能分支族；元审核范围写明「含配套工作树，无论是否实际引用」。磁盘上存在唯一同名配套：

```text
/Users/jacklee/Projects/cordcode-ios-native-message-timeline
feat/ios-native-message-timeline
7e32a62163ef66ab855eb71d571b38b1823574da
（脏，未读源码）
```

方案 S-4 还把重启窗口绑在「另一 agent 的 iOS timeline 真机测试」上，配套身份并非与本任务无关。将配套标「不适用」而未记录路径/分支/完整提交，清单不完整。未读 iOS 源码应记「本轮未引用」，不能省略身份。

本仓、上游两项本身完整，不另开条。

## issues

1. r2 来源清单将配套 iOS 工作树标为「不适用」，未记录唯一对应组合 `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` / `feat/ios-native-message-timeline` / `7e32a62163ef66ab855eb71d571b38b1823574da`（及未提交状态）。项目 P0 与本次复核范围要求配套工作树无论是否实际引用都必须写入完整身份。

## 结论

锚点抽查 5/5 通过。来源清单缺配套 iOS 工作树身份，confirm=false。本报告不评价 r2 的设计通过结论，也不构成实施授权。
