# 2026-10-14 Claude 后台任务执行态错位修复完成报告

## 结论

Claude session `bbe396e6-11ea-4754-943e-df5d2effcd14` 的根因在 MacBridge，不在 iOS：
Claude 官方 per-process session registry 已明确写入 `status="busy"`，但旧实现读取该文件时只取
`pid/sessionId/cwd`，执行态仍从主流 transcript 推断。后台任务只推进 sidechain 时，主流最后可见
assistant turn 可以长期停在已结束状态，于是同一 session 被误判为 live-but-idle；iOS 继续打开/恢复该
session 后又生成第二个 Claude 进程，官方 registry 中同一 session 出现 busy/idle 两个 stub。旧 poller
逐文件发布状态，导致每秒交替 running/idle；5 分钟后 idle TTL 又把观察槽收掉，最终表现为时间线完成态、
列表不显示执行中、后台任务图标仍显示 1 个任务的三处状态不一致。

修复后 MacBridge 以官方 `busy/idle` 为执行态权威：busy 保持 session 执行中，阻止 live-but-idle resume；
同一 session 多个 stub 聚合且 busy 优先；已打开 projection 的 execution phase 同步官方状态；观察槽在
外部 busy 期间每秒 touch，不再被 idle TTL 误收口。iOS 代码零改动、零构建、零安装。

## 来源身份

本节是修复与部署前复核的来源清单。MacBridge 修改为本轮修复产生；iOS 未提交项为既有
docs/exec-plan 状态，本轮没有修改任何 iOS 文件。

- MacBridge：
  - 仓库路径=`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
  - 分支=`feat/ios-native-message-timeline`
  - 提交=`dc37508d2c9f4eaefdfbedbeb6b45f50125dfc03`
  - 未提交状态（本报告写入前）：
    - ` M CHANGELOG.md`
    - ` M agent/claudecode/claudecode.go`
    - ` M agent/claudecode/claudecode_test.go`
    - ` M go-bridge/claude_stub_status.go`
    - ` M go-bridge/projection_kernel.go`
    - ` M go-bridge/projection_reducer.go`
    - ` M go-bridge/session_state_pipeline_test.go`
    - `?? go-bridge/claude_stub_projection_test.go`
  - 任务预期分支=`feat/ios-native-message-timeline`
  - 配套仓库路径/分支/提交=`/Users/jacklee/Projects/cordcode-ios-native-message-timeline` / `feat/ios-native-message-timeline` / `8f8d64c49e8f8c90cc2b2fc7b7d044036ba72da4`
  - 预期产品特性=产品 lineup `claude,codex-remote,grokbuild,dsh-web,opencode-web`，Claude 外部 worker 的官方 `status=busy` 被列表、projection 与 resume 前置检查共同消费。

- iOS：
  - 仓库路径=`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`
  - 分支=`feat/ios-native-message-timeline`
  - 提交=`8f8d64c49e8f8c90cc2b2fc7b7d044036ba72da4`
  - 未提交状态=既有 274 项 `.exec-plan/` 与 `docs/` 状态（完整清单见本报告附录）；`OpenCodeiOS/` 与 `message-web/` 脏文件数=0。
  - 任务预期=本轮只修 MacBridge；禁止修改 iOS 生产源码。
  - 配套 MacBridge 来源=上方 `dc37508d2c9f4eaefdfbedbeb6b45f50125dfc03` 工作树。

## 官方样本与日志证据

- 本机 `claude --version`：`2.1.296 (Claude Code)`。
- 目标外部 worker stub：`~/.claude/sessions/9247.json`，关键字段为
  `pid=9247`、`sessionId=bbe396e6-11ea-4754-943e-df5d2effcd14`、`cwd=/Users/jacklee/Projects/cordcode-ios-native-message-timeline`、
  `version=2.1.295`、`status=busy`、`statusUpdatedAt=1791914764495`（2026-10-13 18:06:04 UTC）。进程
  `/Users/jacklee/Library/Application Support/Claude/claude-code/2.1.295/.../claude` 活着并带
  `--resume=bbe396e6-11ea-4754-943e-df5d2effcd14`。
- 目标 sidechain：`~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/bbe396e6-11ea-4754-943e-df5d2effcd14/subagents/agent-a7aa00e521f705c52.jsonl`
  持续出现 `isSidechain=true` 的 assistant/tool_result/thinking 事件；主流 transcript 的主 turn 已完成。
  这证明“主流 transcript 已 settle”不能覆盖官方 registry 的 busy。
- 旧 runtime 日志（`~/Library/Application Support/CordCode Link/logs/go-bridge.log.1`）：
  - 01:13:17 `preflightClaudeResume live-but-idle external process, allowing resume`，随后打开同 session。
  - 01:13:24 起每秒两条 `session_state_changed` control-plane delivery，对应两个 stub 的 running/idle 交替。
  - 01:18:54 `cleaning idle session ... idle=5m36.77293175s`；02:09:54 同目标再次 `idle=5m48.156914s`。
  - 02:04:06 旧外部 PID 68383 的 transcript watcher 退出，但官方 registry 中后续仍有目标 session 的 busy worker。

## 实现

- `agent/claudecode/claudecode.go`
  - `claudeSessionStub` 解析官方 `status` 字段。
  - `claudeStubExecuting`：`busy => executing=true`；`idle => executing=false`；只有旧版 registry 无
    `status` 时才保留主流 transcript 推断兼容路径。
- `go-bridge/claude_stub_status.go`
  - 同一 session 多个 per-process stub 先聚合再发布，busy 优先，避免目录顺序造成每秒翻转。
  - 首轮完整登记初值，不制造启动风暴。
  - running 时 touch session registry，防止已打开观察槽在外部长任务期间触发 5 分钟 idle cleanup。
  - 幂等同步已 Ready projection 的 execution phase。
- `go-bridge/projection_kernel.go` / `go-bridge/projection_reducer.go`
  - 新增 backend-owned external execution state 通道。
  - 只作用于已 Ready 且已有 reducer state 的 session，不创建假 turn、不在 hydrate 事务中覆盖 cold baseline。
  - external idle 只清除无 `ActiveTurnID` 的外部 running，不收口真实 source-proven active turn。
- 无协议 wire shape 变更，因此不需要 iOS protocol mirror 或 Swift 模型改动。

## 测试与构建

- 定向测试：
  - `go test ./go-bridge ./agent/claudecode -run 'TestClaudeStubStatusPoller|TestExternalExecutionState|TestSessionRegistryStatus|TestGetRunningSessionIDs|TestLiveSessionProcess' -count=1`
- 全包：
  - `go test ./agent/claudecode -count=1`：PASS，2.422s。
  - `go test ./go-bridge -count=1`：PASS，172.681s。
- 静态检查：`go vet ./agent/claudecode ./go-bridge`：PASS。
- Release 构建：`./scripts/build-unsigned-release.sh`：PASS，约 43s。
  - Runtime：`cordcode-bridge-runtime 0.1.0 (commit: dc37508d2c9f, built: 2026-10-13T18:00:03Z)`。
  - App：`build/unsigned-release/Build/Products/Release/CordCodeLink.app`。
  - Zip：`dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip`。
  - SHA-256：`c5530c887d83f14889b038aa59f4192c690495c6d70a93b74ea28963c7733f04`。
- 安全与格式：`git diff --check` PASS；`gitleaks git --redact --config .gitleaks.toml .`
  扫描 1439 commits、约 708.12 MB，无泄漏；`gitleaks dir --redact --config .gitleaks.toml .`
  扫描约 189.40 MB，无泄漏。
- iOS：未运行 iOS build/test/install，原因是没有 iOS 源码变化；这不是 MacBridge 部署缺口的替代证据。

## 部署与运行态核验

- 部署前 `/internal/status`：`bridgeOwnedActiveTurns=0`、`pendingInteractions=0`、admission accepting。
- 第一次优雅退出未生效的原因已确认：当时只有孤立的 runtime PID 5318 监听 8777，`CordCodeLink` GUI
  主进程不在进程表，`killall CordCodeLink`/AppleScript 都没有可作用对象。未覆盖安装，保留现场后本轮
  复核安全状态。
- 第二次部署按精确 PID 5318 只终止 `/Applications/CordCodeLink.app/.../cordcode-bridge-runtime`，
  未触碰任何外部 Claude 进程；旧 App bundle 移至 `/tmp/CordCodeLink.pre-dc37508.app` 作回滚备份。
- 新产物通过 `ditto` 覆盖到 `/Applications/CordCodeLink.app` 并重新打开：
  - App PID 10992；runtime PID 11056，PPID 10992，启动时间 2026-10-14 02:15:21。
  - runtime epoch=`b4d0f5cb-3b85-49e1-ab09-9a3eb4a058fd`。
  - 8777 由新 runtime 监听；进程路径仅来自 `/Applications/CordCodeLink.app`。
  - 安装包内 runtime 与已测 Release 产物 byte-for-byte 相同，且 `-version` 报 `dc37508d2c9f`。
  - `/internal/agents`：`claude,codex-remote,dsh-web,grokbuild,opencode-web` 全部 available。
  - 新日志无 panic；仅有启动瞬间 codex-remote pairing 未就绪 ERROR，同一秒后的 pairing 流程恢复为 available（既有启动竞态，不是本修复引入）。
  - 新 runtime 连续运行超过 15 分钟，目标官方 stub 仍为 `busy` 且活进程存在；新日志没有再出现目标
    session 的 `cleaning idle session`。本轮窗口内 iPhone 未重新打开该 session，因此未伪造 UI 验收。

## 未完成 / 边界

- iPhone 三处 UI（时间线、列表徽标、后台任务图标）需要 owner 在真实设备上重新进入该 session 或刷新
  列表后人工确认；本轮没有授权或执行 iOS UI automation。
- Claude 2.1.295/2.1.296 的 `status` 语义来自本机官方 registry 真实样本；未知 status 仍不映射，不做
  fallback 假状态。
- 旧 App 回滚备份保留在 `/tmp/CordCodeLink.pre-dc37508.app`，不作为运行进程；系统重启或明确回滚后再处理。
- 用户未要求 commit，本轮不提交。

## 附录：iOS 当前未提交状态清单

本清单只证明来源身份与“无 iOS 生产源码改动”；这些是既有工作树状态，本轮未修改。

```text
 M .exec-plan/state/plan-1c94c47a8dda.json
 M .exec-plan/state/plan-26468876b0fe.json
 M .exec-plan/state/plan-661a69a5f71e.json
 M .exec-plan/state/plan-666f59cfe5dd.json
 M .exec-plan/state/plan-8690b2249f7b.json
 M .exec-plan/state/plan-b7d3e325de87.json
 M docs/2026-09-12-ios-message-timeline-native-migration-plan.md
 M "docs/2026-09-12-ios-message-timeline-native-migration-plan\345\256\214\346\210\220\346\203\205\345\206\265.md"
 M docs/2026-09-13-ios-message-timeline-native-migration-kickoff.md
 M docs/2026-09-13-ios-message-timeline-native-migration-plan-review-round2.md
 M docs/2026-09-13-ios-message-timeline-native-migration-plan-review-round3.md
 M docs/2026-09-13-ios-message-timeline-native-migration-plan-review-round4.md
 M docs/2026-09-13-ios-message-timeline-native-migration-plan-review-round5.md
 M docs/2026-09-13-ios-message-timeline-native-migration-plan-review-round6.md
 M docs/2026-09-13-native-message-timeline-pr0-source-audit.md
 M docs/2026-09-13-native-timeline-pr0-nonui-evidence.md
 M docs/2026-09-14-native-timeline-internal-n-beta-evidence.md
 M docs/2026-09-14-native-timeline-internal-n1-observation-window.md
 M docs/2026-09-14-native-timeline-pr0-row6-device-report.md
 M docs/2026-09-14-native-timeline-pr2-layout-microdesign.md
 M docs/2026-09-14-native-timeline-pr3-highlighter-matrix-decision.md
 M docs/2026-09-14-native-timeline-pr3-streaming-poc-results.md
 M "docs/2026-09-16-native-timeline-bottom-gap\345\256\214\346\210\220\346\203\205\345\206\265.md"
 M docs/2026-09-16-native-timeline-process-group-parity-analysis.md
 M docs/2026-09-16-native-timeline-process-group-parity-kickoff.md
 M docs/2026-09-16-native-timeline-process-group-parity-review-round2.md
 M docs/2026-09-16-native-timeline-process-group-parity-review-round3.md
 M docs/2026-09-16-native-timeline-process-group-parity-review-round4.md
 M docs/2026-09-16-native-timeline-process-group-parity-review.md
 M docs/2026-09-17-native-timeline-process-group-parity-completion-report.md
 M docs/2026-09-18-native-timeline-live-viewport-height-fix-report.md
 M docs/2026-09-20-native-timeline-livetail-follow-status-report.md
 M docs/2026-09-20-native-timeline-streaming-smoothness-optimization-plan.md
 M "docs/2026-09-20-native-timeline-streaming-smoothness-optimization-plan\345\256\214\346\210\220\346\203\205\345\206\265.md"
 M docs/2026-09-21-native-process-group-chatgpt-parity-plan-review-v1.md
 M docs/2026-09-21-native-process-group-chatgpt-parity-plan-review-v2-directive.md
 M docs/2026-09-21-native-process-group-chatgpt-parity-plan-review-v2.md
 M docs/2026-09-21-native-process-group-chatgpt-parity-plan-review-v3.md
 M docs/2026-09-21-native-process-group-chatgpt-parity-plan.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v10.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v11.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v12.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v13.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v4.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v5.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v6.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v7.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v8.md
 M docs/2026-09-22-native-process-group-chatgpt-parity-plan-review-v9.md
 M docs/2026-09-22-native-process-group-parity-dev-directive.md
 M "docs/2026-09-22-native-process-group-parity-dev-directive\345\256\214\346\210\220\346\203\205\345\206\265.md"
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-S0-baseline.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r1.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r10.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r11.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r12.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r13.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r14.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r2-meta.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r2.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r3.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r4.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r5.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r6.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r7.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r8.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan-review-r9.md
 M docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan.md
 M "docs/2026-09-25-native-timeline-streaming-pacing-anchor-plan\345\256\214\346\210\220\346\203\205\345\206\265.md"
 M docs/2026-09-26-native-timeline-capture-repair.md
 M docs/2026-09-26-native-timeline-finalize-hydration-plan-review-r1.md
 M docs/2026-09-26-native-timeline-finalize-hydration-plan-review-r2.md
 M docs/2026-09-26-native-timeline-finalize-hydration-plan-review-r3.md
 M docs/2026-09-26-native-timeline-finalize-hydration-plan-review-r4.md
 M docs/2026-09-26-native-timeline-finalize-hydration-plan.md
 M docs/2026-09-26-native-timeline-long-repro-diagnosis.md
 M docs/2026-09-26-native-timeline-steady-release-plan-review-r1.md
 M docs/2026-09-26-native-timeline-steady-release-plan-review-r2.md
 M docs/2026-09-26-native-timeline-steady-release-plan.md
 M docs/2026-09-27-native-timeline-single-cell-streaming-markdown-plan-review-r1.md
 M docs/2026-09-27-native-timeline-single-cell-streaming-markdown-plan-review-r2.md
 M docs/2026-09-27-native-timeline-single-cell-streaming-markdown-plan-review-r3.md
 M docs/2026-09-27-native-timeline-single-cell-streaming-markdown-plan.md
 M docs/2026-09-27-native-timeline-streaming-markdown-plan-review-r1.md
 M docs/2026-09-27-native-timeline-streaming-markdown-plan-review-r2.md
 M docs/2026-09-27-native-timeline-streaming-markdown-plan-review-r3.md
 M docs/2026-09-27-native-timeline-streaming-markdown-plan.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r2.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r3.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r4.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r5.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r6.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r7-meta.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r7.md
 M docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan.md
 M docs/2026-09-28-cordcode-chatgpt-codex-timeline-parity-plan-review-r1.md
 M docs/2026-09-28-cordcode-chatgpt-codex-timeline-parity-plan-review-r2-meta.md
 M docs/2026-09-28-cordcode-chatgpt-codex-timeline-parity-plan-review-r2.md
 M docs/2026-09-28-cordcode-chatgpt-codex-timeline-parity-plan-review-r3.md
 M docs/2026-09-28-cordcode-chatgpt-codex-timeline-parity-plan.md
 M docs/2026-09-28-native-timeline-phase2-s0-e3-investigation.md
 M docs/2026-09-28-native-timeline-phase2-status-analysis.md
 M docs/2026-09-28-native-timeline-streaming-frame-motion-next-step-plan.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan-review-r1.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan-review-r2.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan-review-r3.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan-review-r4.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan-review-r5-meta.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan-review-r5.md
 M docs/2026-09-29-native-timeline-chunked-follow-plan.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r1.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r2.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r3.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r4.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r5.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r6.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r7-meta.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan-review-r7.md
 M docs/2026-09-29-native-timeline-streaming-height-stability-plan.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r1.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r2.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r3.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r4.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r5.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r6.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r7.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r8.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r9-meta.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan-review-r9.md
 M docs/2026-09-30-native-timeline-blank-band-and-finalize-jolt-plan.md
 M docs/2026-09-30-native-timeline-e1e3-interim-findings.md
 M docs/2026-09-30-native-timeline-ep3-failure-analysis.md
 M docs/2026-09-30-native-timeline-ep3-takeover-fix.md
 M docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r1.md
 M docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r2-meta.md
 M docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r2.md
 M docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r3-meta.md
 M docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r3.md
 M docs/2026-09-30-native-timeline-fence-tick-cost-plan.md
 M docs/2026-09-30-native-timeline-history-layout-cost-investigation.md
 M docs/2026-09-30-native-timeline-history-metric-key-reuse-fix.md
 M docs/2026-09-30-native-timeline-physical-upscroll-investigation.md
 M docs/2026-09-30-native-timeline-residual-frame-cost-fix.md
 M docs/2026-09-30-native-timeline-residual-frame-cost-investigation.md
 M docs/2026-09-30-native-timeline-residual-motion-forensics-report.md
 M docs/2026-09-30-native-timeline-streaming-commit-scan-reuse.md
 M docs/2026-10-01-native-timeline-canonical-measurement-reuse-and-recycle-fix.md
 M docs/2026-10-01-native-timeline-finalize-continuity-fix.md
 M docs/2026-10-01-native-timeline-finalize-continuity-physical-followup.md
 M docs/2026-10-01-native-timeline-finalize-derivation-physical-followup.md
 M docs/2026-10-01-native-timeline-finalize-full-path-observation.md
 M docs/2026-10-01-native-timeline-finalize-linear-scan-and-preview-flush.md
 M docs/2026-10-01-native-timeline-finalize-row-derivation-reuse.md
 M docs/2026-10-01-native-timeline-highlight-quadratic-selection-fix.md
 M docs/2026-10-01-native-timeline-identical-highlight-fix.md
 M docs/2026-10-01-native-timeline-kind-dispatch-optimization.md
 M docs/2026-10-01-native-timeline-kind-dispatch-physical-followup.md
 M docs/2026-10-01-native-timeline-linear-scan-physical-followup.md
 M docs/2026-10-01-native-timeline-math-floor-physical-followup.md
 M docs/2026-10-01-native-timeline-math-follow-jitter-investigation.md
 M docs/2026-10-01-native-timeline-math-height-floor-geometry-fix.md
 M docs/2026-10-01-native-timeline-measure-reuse-physical-and-dsh-terminal-followup.md
 M docs/2026-10-01-native-timeline-new-turn-height-floor-fix.md
 M docs/2026-10-01-native-timeline-post-key-reuse-independent-recheck.md
 M docs/2026-10-01-native-timeline-post-key-reuse-physical-followup.md
 M docs/2026-10-01-native-timeline-post-probe-fix-finalize-followup.md
 M docs/2026-10-01-native-timeline-post-settled-layout-followup.md
 M docs/2026-10-01-native-timeline-rendered-measure-physical-followup.md
 M docs/2026-10-01-native-timeline-rendered-slot-measurement-reuse.md
 M docs/2026-10-01-native-timeline-settled-layout-metrics-fix.md
 M "docs/2026-10-02-2026-09-28-cordcode-chatgpt-codex-timeline-parity-plan\345\256\214\346\210\220\346\203\205\345\206\265.md"
 M docs/2026-10-02-native-timeline-content-diff-reuse-optimization.md
 M docs/2026-10-02-native-timeline-content-diff-reuse-physical-followup.md
 M docs/2026-10-02-native-timeline-exact-measure-without-presentation-layout.md
 M docs/2026-10-02-native-timeline-finalize-alignment-physical-followup.md
 M docs/2026-10-02-native-timeline-finalize-source-and-inset-optimization.md
 M docs/2026-10-02-native-timeline-finalized-item-derivation-optimization.md
 M docs/2026-10-02-native-timeline-finalized-item-physical-followup.md
 M docs/2026-10-02-native-timeline-history-block-derivation-optimization.md
 M docs/2026-10-02-native-timeline-history-block-physical-followup.md
 M docs/2026-10-02-native-timeline-presentation-construction-optimization.md
 M docs/2026-10-02-native-timeline-presentation-reuse-physical-followup.md
 M docs/2026-10-02-native-timeline-row-index-batch-preparation-optimization.md
 M docs/2026-10-02-native-timeline-row-index-physical-followup.md
 M docs/2026-10-02-native-timeline-scratch-measure-physical-followup.md
 M docs/2026-10-03-native-timeline-batch-topology-optimization.md
 M docs/2026-10-03-native-timeline-batch-topology-physical-followup.md
 M docs/2026-10-03-native-timeline-consumed-height-delivery-optimization.md
 M docs/2026-10-03-native-timeline-consumed-height-delivery-physical-followup.md
 M docs/2026-10-03-native-timeline-exact-height-reuse-physical-followup.md
 M docs/2026-10-03-native-timeline-height-ledger-takeover-audit.md
 M docs/2026-10-03-native-timeline-indexed-exact-height-reuse-optimization.md
 M docs/2026-10-03-native-timeline-short-history-markdown-physical-followup.md
 M docs/2026-10-03-native-timeline-stable-height-fast-return-optimization.md
 M docs/2026-10-03-native-timeline-stable-height-fast-return-physical-followup.md
 M docs/2026-10-03-native-timeline-table-text-install-optimization.md
 M docs/2026-10-03-native-timeline-table-text-install-physical-followup.md
 M docs/2026-10-04-native-timeline-ordinary-batch-height-verification-analysis.md
 M docs/2026-10-04-native-timeline-remaining-frame-cost-analysis-response.md
 M docs/2026-10-04-native-timeline-remaining-frame-cost-independent-analysis.md
 M docs/2026-10-05-native-timeline-streaming-fluency-status-and-next-directions.md
 M docs/2026-10-05-native-timeline-streaming-fluency-status-review-r2.md
 M docs/2026-10-05-native-timeline-streaming-fluency-status-review-r3.md
 M docs/2026-10-05-native-timeline-streaming-fluency-status-review-r4.md
 M docs/2026-10-05-native-timeline-streaming-fluency-status-review.md
 M docs/2026-10-07-cordcode-ios-timeline-review.md
 M docs/2026-10-07-native-timeline-streaming-cadence-design-review.md
 M docs/2026-10-07-native-timeline-streaming-cadence-design.md
 M docs/2026-10-07-native-timeline-streaming-cadence-history-replay-repair.md
 M docs/2026-10-07-native-timeline-streaming-cadence-image-height-optimization.md
 M docs/2026-10-07-native-timeline-streaming-cadence-replay-entry-repair.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s0-1-analysis.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s0-2-and-capacity-fix.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s0-s1-evidence.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-1-analysis.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-2-and-probe-repair.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-a-analysis.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-evidence.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-h-analysis.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-hr2-failure.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-i-analysis.md
 M docs/2026-10-07-native-timeline-streaming-cadence-s2-r-analysis.md
 M docs/2026-10-08-cordcode-device-storage-recovery.md
 M docs/2026-10-08-cordcode-ios-timeline-review-v2.md
 M docs/2026-10-08-native-timeline-b1-trace-save-failure.md
 M docs/2026-10-08-native-timeline-evidence-decision.md
 M docs/2026-10-08-native-timeline-fp8-cold-replay.md
 M docs/2026-10-08-native-timeline-long-image-owner-log.md
 M docs/2026-10-08-native-timeline-paired-presentation-gate.md
 M docs/2026-10-08-native-timeline-paired-warm-replay.md
 M docs/2026-10-08-native-timeline-s2-append-chain-cost.md
 M docs/2026-10-08-native-timeline-s3-ab-per-tick-candidate.md
 M docs/2026-10-08-native-timeline-s3-pacer-frame-rate-fix.md
 M docs/2026-10-08-native-timeline-same-height-commit.md
 M docs/2026-10-08-native-timeline-streaming-cadence-e05-d1-baseline.md
 M docs/2026-10-08-native-timeline-streaming-cadence-e05-first-pair.md
 M docs/2026-10-08-native-timeline-streaming-cadence-isolated-replay-bulk-validation.md
 M docs/2026-10-08-native-timeline-streaming-cadence-s2-b-scheduler-repair.md
 M docs/2026-10-08-native-timeline-streaming-cadence-s2-c-acceptance.md
 M docs/2026-10-08-native-timeline-takeover-evidence-audit.md
 M docs/2026-10-08-native-timeline-unified-evidence-preparation.md
 M docs/2026-10-09-cordcode-ios-a1-amendment-review-r1.md
 M docs/2026-10-09-cordcode-ios-batch-context-review-r1.md
 M docs/2026-10-09-cordcode-ios-c2a-review.md
 M docs/2026-10-09-cordcode-ios-c2b-review-r1.md
 M docs/2026-10-09-cordcode-ios-incremental-layout-review-v2.md
 M docs/2026-10-09-cordcode-ios-incremental-layout-review-v3.md
 M docs/2026-10-09-cordcode-ios-incremental-layout-review-v4.md
 M docs/2026-10-09-cordcode-ios-incremental-layout-review.md
 M docs/2026-10-09-cordcode-ios-measure-no-highlight-change-note.md
 M docs/2026-10-09-cordcode-ios-progressive-measurement-review-r1.md
 M docs/2026-10-09-native-timeline-a1-component-validation.md
 M docs/2026-10-09-native-timeline-a1-cost-lifecycle-amendment.md
 M docs/2026-10-09-native-timeline-a1-first-attempt.md
 M docs/2026-10-09-native-timeline-batch-context-validation.md
 M docs/2026-10-09-native-timeline-c01-validation.md
 M docs/2026-10-09-native-timeline-c2-exact-width-reuse-design.md
 M docs/2026-10-09-native-timeline-c2a-validation.md
 M docs/2026-10-09-native-timeline-c2b-exact-measurement-design.md
 M docs/2026-10-09-native-timeline-c2b-prototype-validation.md
 M docs/2026-10-09-native-timeline-direct-exact-size-change-note.md
 M docs/2026-10-09-native-timeline-incremental-layout-design.md
 M docs/2026-10-09-native-timeline-markdown-batch-context-design.md
 M docs/2026-10-09-native-timeline-measure-without-highlight-change-note.md
 M docs/2026-10-09-native-timeline-progressive-exact-measurement-design.md
 M docs/2026-10-09-native-timeline-progressive0a-validation.md
 M docs/2026-10-10-cordcode-ios-table-incremental-review.md
 M docs/2026-10-10-native-timeline-phase-close-review.md
 M docs/2026-10-10-native-timeline-release-hitch-baseline.md
 M docs/2026-10-10-native-timeline-scroll-lease-fix.md
 M docs/2026-10-10-native-timeline-table-incremental-change-note.md
 M docs/native-timeline-streaming-optimization-history.md
?? .exec-plan/native-timeline-status-index.json
?? .exec-plan/state/plan-4f2d48b2dd54.json
```
