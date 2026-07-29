# 本轮任务完成情况：CCCode iOS / MacBridge 拆仓与 cc-connect 合并方案

> **命名说明（2026-06-24）：** 本文档写于 repo rename 之前。文中 cccode-macbridge/cccode-ios 指 GitHub 旧仓库名(现为 cordcode-*);Go module path 已从 github.com/openAgi2/cccode-macbridge 重命名为 …/cordcode-macbridge。本文为历史记录。


## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/opencode-cc-connect`
- Plan: `docs/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案.md`
- Canonical State File: `/Users/jacklee/Projects/opencode-cc-connect/.exec-plan/state/plan-c4b930b9aa36.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: `15/15 todos done, 15/15 proven`
- Related Commits: MacBridge `81d0a36`, `3c9fa43`, `b14fcbe`, `6e4ede5`, `4ff89f8`; iOS `6d2a92a`, `2e1231e`, `d1102e8`
- Generated At: `2026-06-11T12:05:16Z`

## 1. Overall Verdict (总体结论)
方案已达到 proved-complete。Task A-D 的拆仓、兼容文档、构建测试和公开前硬化均有持久化证据；Task E 已在真实 MacBridge、真实 iPhone、生产 Relay 和真实 Claude Code 后端上完成验证。

最终缺口 Offline mailbox replay 已闭环：iPhone 离线时生产 Relay 从 0 增至 1 条加密帧，iPhone 通过 5G 恢复后将其消费并 ACK，Relay 回到 0 条待处理帧。未使用 mock、假数据、fallback 或手工注入 mailbox 数据。

## 2. Phase Completion Matrix (阶段完成矩阵)
| Phase | Impl | Tests | Regression | Verdict | Evidence Summary |
| --- | --- | --- | --- | --- | --- |
| Task A MacBridge repo bootstrap | proven-done | proven-done | proven-done | complete | Go build/test、relay-server test、MacBridge build、嵌入 runtime 验证通过 |
| Task B iOS repo bootstrap | proven-done | proven-done | proven-done | complete | message-web build/test、iOS build、仓库边界验证通过 |
| Task C protocol compatibility pack | proven-done | proven-done | proven-done | complete | 双仓协议包、schema、samples 和 crypto vectors 已同步并验证 |
| Task D public readiness pass | proven-done | proven-done | proven-done | complete | README/LICENSE/SECURITY/CI/readiness/release checklist 与 secret scans 完成 |
| Task E integration verification | proven-done | proven-done | proven-done | complete | Direct、Relay 5G、真实后端、offline mailbox、revoke、reconnect、backend switching 均有证据 |

## 3. Key File Changes (关键文件变更)
- `/Users/jacklee/Projects/cccode-macbridge`: 独立 MacBridge/go-bridge/relay-server 仓库及最小 cc-connect runtime 边界。
- `/Users/jacklee/Projects/cccode-ios`: 独立 iOS/message-web 仓库及同步协议包。
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge`: Bridge wire 适配、Relay mailbox 路由、delivery prekey 与诊断能力。
- `/Users/jacklee/Projects/cccode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge`: iOS Bridge/Relay 连接、加密 mailbox replay、cursor/ACK/reconcile。
- `/Users/jacklee/Projects/cccode-macbridge/docs/public-readiness.md`: MacBridge 公开前边界与剩余发布事项。
- `/Users/jacklee/Projects/cccode-ios/docs/public-readiness.md`: iOS 公开前边界与剩余发布事项。
- `/Users/jacklee/Projects/opencode-cc-connect/docs/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案-TaskE集成验证记录.md`: Task E 真机与 Relay 验证证据。
- `/Users/jacklee/Projects/opencode-cc-connect/.exec-plan/state/plan-c4b930b9aa36.json`: 15 项 proof-carrying queue 状态。

## 4. Verification Evidence (验证证据)
### 4.1 Automated tests
- Commands:
  - MacBridge: `go build ./go-bridge`, `go test ./go-bridge/... -count=1`
  - Relay: `go test ./... -count=1`
  - MacBridge app: targeted macOS `xcodebuild`
  - message-web: `npm ci`, `npm run build:ios`, `npm run test`
  - iOS: targeted `xcodebuild` build and Relay crypto/network state unit tests
  - Public hardening: Gitleaks git/dir scans in both split repositories
- Result: Task A-D required build, test, schema, scan, and boundary gates passed.
- Main test files: `go-bridge/...`, `relay-server/...`, `OpenCodeiOSTests/RelayCryptoVectorTests.swift`, `OpenCodeiOSTests/MailboxReplayTests.swift`, message-web Vitest suite.
- Artifact paths:
  - `/Users/jacklee/Projects/cccode-macbridge/docs/public-readiness.md`
  - `/Users/jacklee/Projects/cccode-ios/docs/public-readiness.md`

### 4.2 Regression evidence
- Device / replay / benchmark / manual validation:
  - Real iPhone 16 Pro, iOS 27.0, connected to owner-hosted Relay over cellular data.
  - Online Relay QR pairing and Claude Code send/receive passed.
  - Revoke-deny, reconnect-after-network-interruption, and backend switching passed.
  - 2026-06-11 owner completed a unified real-device regression for the latest iOS changes: each backend remembers its last selected model; cold launch reconnects in the background without blocking the UI; the session list no longer flashes a load-failure state during bridge cold start; and the connection-status dot shows the expected state color.
  - Direct authenticated probe completed a real Claude Code turn.
  - Delivery prekey status reported 31 available keys before offline replay.
  - Relay mailbox baseline: `pendingFrameCount=0`, `pendingMailboxBytes=0`.
  - With CCCode offline, real Claude session `18ddb3cc-7554-4070-9935-1c61d0e40a7f` completed and Relay changed to one pending encrypted frame, 904 bytes.
  - After CCCode launched over 5G/LTE, Relay returned to zero pending frames and bytes, proving fetch and ACK.
- Artifact paths:
  - `/Users/jacklee/Projects/opencode-cc-connect/docs/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案-TaskE集成验证记录.md`
  - `/Users/jacklee/Projects/opencode-cc-connect/.exec-plan/state/plan-c4b930b9aa36.json`

### 4.3 Audit downgrade summary
- Downgraded todos: none.
- Why they were downgraded: not applicable. All 15 todos are done with present verification summaries and artifacts.

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)
- 两个拆分仓库已创建为 GitHub 私有仓库并推送 `main`：
  - `https://github.com/openAgi2/cccode-ios`
  - `https://github.com/openAgi2/cccode-macbridge`
- 两个远端 `main` 均已通过 fresh-clone Gitleaks Git 历史和工作树扫描。
- iOS `message-web` 已升级 Vite/Vitest 并通过测试、构建和真机构建安装；`npm audit --audit-level=moderate` 当前为 0 vulnerabilities。
- 正式签名、notarization、provisioning 和 release channel 由 owner 明确延期，不阻塞当前私有仓库发布。
- 生产 Relay endpoint 的正式配置注入方式仍待决定；真实 endpoint、route credential 和 device credential 继续禁止提交到源码。
- 本次真机使用 iOS 27 beta，系统构建比 Xcode 27 beta 所带 device support 高一个修订，导致 CoreDevice 启动和截图工具偶发超时；产品 Relay 路径本身已通过。

## 6. Audit Focus (建议审核重点)
1. 核对两个拆分仓库的源码边界与 AGPL/public-readiness 文档是否符合公开计划。
2. 核对 MacBridge mailbox marker、delivery prekey 和 Relay store-only 语义与 iOS decrypt/reconcile/ACK 顺序。
3. 核对 Task E 记录中的 Relay `0 -> 1 -> 0` 证据是否足以支持 offline mailbox replay 完成结论。
4. 对外发布前重新确认签名配置、发布渠道和生产 endpoint 注入边界。

## 7. Constraints (关键约束)
- 未向公开候选源码提交 owner-hosted Relay endpoint、route credential 或设备 credential。
- 未使用 mock、fallback、假数据、placeholder 或缓存快照替代真实后端与真实 Relay。
- 真机验证使用安装的 iOS app、安装的 MacBridge app、生产 Relay 和真实 Claude Code turn。
- `todo.md` 仅为信息参考；完成结论以 canonical exec-plan state 和 proof artifacts 为准。
