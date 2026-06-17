# 本轮任务完成情况：Bridge "卡连接中" 根治实施规格

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cccode-macbridge`
- Plan: `docs/2026-06-17-bridge-hang-implementation-spec.md`
- Canonical State File: `/Users/jacklee/Projects/cccode-macbridge/.exec-plan/state/plan-1d959e07b060.json`
- Legacy State File: none（本 queue 为新建，未从 `.claude/exec-plans/` 迁移）
- Completion Report Verdict: `proved-complete`
- Queue Summary: 15/15 todos done。14 项 required 全 proven，其中 **8 项 re-verified**（tests/regression 重跑）、**5 项 impl self-attested**、**1 项 regression self-attested/owner-accepted**；1 项 regression `n/a (justified)`（required:false）。
- Related Commits: `4c2d3d0`（本地 main，未 push）
- Generated At: 2026-06-17T17:05:00Z

## 1. Overall Verdict (总体结论)

代码全部落地、部署并验证。go-bridge（P0-A 写 deadline + 死锁安全关闭、P0-B relay ping/pong 保活）与 relay-server（PR-2 读写 deadline + setReadKeepalive）均已 commit、随 MacBridge Release 编译安装到 `/Applications`、relay-server 二进制部署到生产 VPS。

- **自动化证据强**：5 个 `-tests` 与 3 个 `-regression` 共 8 项在 exit audit 中**重跑通过**（`re-verified`），覆盖写 deadline 触发、死锁安全关闭、relay ping/读 deadline、keepalive 重置 deadline 等核心机制。
- **唯一非独立验证项**：`pr1-p0b-regression`（真机 iOS 后台→自动重连）为 `self-attested / owner-accepted`——真机测试期间 MacBridge 120min 定时兜底重启恰好触发并 truncate 了 `/tmp/go-bridge.log`，干净的 iOS 后台日志未获取；owner 决定接受替代硬证据（VPS nft 模拟：Mac 自动重连 102s 恢复、无需手动重启）与功能确认（切回前台加载正常），不再重测，等实际使用反馈。
- 本报告由执行工作的同一 agent 撰写，所有 `self-attested` 证据已显式标注，不冒充独立验证。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| pr1 / P0-A 写 deadline + 死锁安全关闭 | proven-done | proven-done | proven-done | done | impl self-attested；tests + regression re-verified |
| pr1 / P0-B relay ping/pong 保活 | proven-done | proven-done | done (self-attested) | done | impl self-attested；tests re-verified；regression **owner-accepted**（真机日志缺失） |
| pr2 / 写 deadline | proven-done | proven-done | proven-done | done | impl self-attested；tests + regression re-verified |
| pr2 / 读 deadline | proven-done | proven-done | proven-done | done | impl self-attested；tests + regression re-verified |
| pr2 / ping（可选） | proven-done | proven-done | n/a (justified) | done | pong 响应由 setReadKeepalive 实现+tested；relay 主动 ping 冗余未实施 |

## 3. Key File Changes (关键文件变更)

**go-bridge（PR-1，随 app 分发）**
- `go-bridge/server.go`：`SendJSON` 加 10s 写 deadline（`bridgeWriteTimeout` var）；连续 5 次写错误后调底层 `c.conn.Close()` 让读循环退出（注释写明死锁陷阱：不可用 `CloseWithControl`/`c.Close()`，会重入 `c.mu`）。
- `go-bridge/relay_bridge_client.go`：`SendEnvelope`/server hello/`sendServerHelloError` 三处加 10s 写 deadline；`Connect` 设 90s 读 deadline + pong handler + 起 `pingLoop`；新增 `pingLoop(done, period)`（30s ping，`WriteControl` 不套 writeMu，随 `c.done` 退出）。保活参数在调用方 goroutine 捕获传入子 goroutine（race-clean）。
- `go-bridge/pairing_handler.go`：`NotifyComplete`/`sendPairingResult` 两处加写 deadline。

**relay-server（PR-2，独立 VPS 部署）**
- `relay-server/internal/relay/server.go`：`socketPeer.write` 加 10s 写 deadline；`readBridgeFrames`/`readDeviceFrames` 加 90s 读 deadline + 新增 `socketPeer.setReadKeepalive()`（`PingHandler` 收 ping 时重置读 deadline 并回 pong）。

**测试**
- `go-bridge/write_deadline_test.go`、`go-bridge/relay_bridge_client_test.go`（新增 ping/读 deadline 测试）、`relay-server/internal/relay/deadline_test.go`。

**部署 / 文档**
- `scripts/deploy-relay-vps.sh`（无密钥、读环境变量、自动重试 banner 超时）、`CLAUDE.md`「Deploying relay-server to the VPS」章节。
- `docs/2026-06-17-bridge-connecting-hang-root-cause[-review].md`、`docs/2026-06-17-bridge-hang-implementation-spec[-review].md`（根因 + spec + 三轮评审）。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- **命令**（exit audit 重跑，`re-verified`）：
  - `cd go-bridge && go test ./... -count=1` → ok（8.6s）
  - `cd go-bridge && go test . -run 'TestSendJSONWriteDeadlineFiresOnBlockedWrite|TestSendJSONClosesConnAfterRepeatedWriteErrors|TestRelayBridgeClientPingAndReadDeadlineDetectsHalfOpen' -race -count=1` → ok
  - `cd relay-server && go test ./... -count=1` → ok
  - `cd relay-server && go test ./internal/relay/ -run 'TestSocketPeerWriteDeadline|TestSocketPeerReadDeadline|TestSocketPeerReadKeepalive' -race -count=1` → ok
- **Attestation**: `re-verified`（上述命令在 exit audit 中实际重跑，结果记录于 artifact）
- **Main test files**: 见上“测试”段
- **Artifact paths**:
  - `.exec-plan/artifacts/bridge-hang/pr1-p0a-tests.log`
  - `.exec-plan/artifacts/bridge-hang/pr1-p0a-regression.log`
  - `.exec-plan/artifacts/bridge-hang/pr1-p0b-tests.log`
  - `.exec-plan/artifacts/bridge-hang/pr2-write-deadline-tests.log`
  - `.exec-plan/artifacts/bridge-hang/pr2-read-deadline-tests.log`
  - `.exec-plan/artifacts/bridge-hang/exit-audit.log`

### 4.2 Regression evidence

- **pr1-p0a / pr2-write / pr2-read regression**：`re-verified`（全套 + race 重跑通过，现有 `TestPingTickerClosesOnTimeout` 不回归）。
- **pr1-p0b regression（真机 iOS 后台）**：`self-attested / owner-accepted`。
  - 替代硬证据：VPS `nft drop :8443` 模拟 relay 半开 → Mac 在 T0+139s 报 `close 1006 EOF` → backoff 重试 → **102s 内自动重连成功，无需手动重启**（对照原症状 5 分 18 秒 + 手动重启）。
  - 真机 iOS 后台实测：切回前台后 session list / session 加载正常；但干净的 iOS 后台期间 Mac 侧日志被 MacBridge 120min 定时兜底重启覆盖，未获取。
  - **owner 决定**：接受现有证据，不再重测，等实际使用反馈。
- **pr2-ping regression**: `n/a (justified)` — relay 主动 ping 冗余（Mac P0-B 已驱动双向保活）；pong 响应 + deadline reset 已由 `setReadKeepalive` 实现并经 `TestSocketPeerReadKeepaliveExtendsDeadlineOnPing` 覆盖。

### 4.3 Audit downgrade summary

- 无。exit audit 未 downgrade 任何 todo；所有 `done` 状态在重跑后成立。
- 唯一非 re-verified 的 required 项是 `pr1-p0b-regression`（self-attested/owner-accepted），已在 §1、§4.2 如实标注，未伪装为独立验证。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

1. **pr1-p0b-regression 真机干净日志缺失**：因定时兜底重启覆盖日志，未拿到“iOS 后台→Mac 90s/CF 139s 自动重连”的端到端真机日志。替代证据（VPS 模拟 + 功能确认）已由 owner 接受；若后续复现“卡连接中”，优先重测（关定时重启或 `tee` 镜像日志）。
2. **Cloudflare 代理掩盖 Mac 90s 读 deadline**：实测发现部署为 `Mac↔Cloudflare↔relay`，CF 在 Mac↔CF 段做 WebSocket pong 代理，使 Mac 的 P0-B 90s 读 deadline 在此部署下不触发；半开检测实际由 CF ~139s 超时主导。代码在非 CF 直连场景仍有效；写 deadline（P0-A）与自动重连不受影响。若要 Mac 自身 90s 生效：relay 直连 VPS:8443 不经 CF，或 CF 关 WebSocket 保活（目前没必要）。
3. **Pre-existing data races（非本计划引入）**：`go-bridge/TestServerCloseAllConnectionsClosesActiveWebSockets`、`agent/codex/TestPassiveSubscribe_ReconnectAfterServerClose` 在隔离 `-race` 下偶发失败（clean baseline 已复现）。全套 timing 下通过，与本修复无关。
4. **relay-server 部署链独立**：VPS 二进制需单独部署（已部署 PID 367103 / sha `104fe8c7`）；后续 `relay-server/` 代码改动不会随 app 更新生效，须重跑 `scripts/deploy-relay-vps.sh`。

## 6. Audit Focus (建议审核重点)

1. **死锁陷阱**（`server.go` SendJSON 失败分支）：确认用的是 `c.conn.Close()`（gorilla 方法），而非 `CloseWithControl`/`c.Close()`（会重入 `c.mu`）。代码注释已写明。
2. **pingLoop 不套 writeMu**：`relay_bridge_client.go` pingLoop 的 `WriteControl` 是否在 `writeMu` 之外（gorilla 允许并发；套锁会让 ping 被卡住的数据写阻塞，违背保活初衷）。
3. **relay setReadKeepalive**：`relay-server` 的 `PingHandler` 是否既重置读 deadline、又回 pong（缺任一会致保活失效或连接误判死）。
4. **参数可注入性**：`bridgeWriteTimeout`/`relayPingPeriod`/`relayReadTimeout`/`relayWriteDeadline`/`relayReadDeadline` 是否均为 `var`（非 const），测试能否覆盖短值。

## 7. Constraints (关键约束)

- relay-server 是独立 Go module（`cccode-relay`），独立 VPS 部署链；代码改动须 `scripts/deploy-relay-vps.sh` 才生效。
- 凭据（VPS 密码）只在 `~/.zshrc` 的 `CCCODE_RELAY_VPS_*` 环境变量，不入仓库；CLAUDE.md 仅记变量名。
- UI 自动化与真机验证需 owner 显式授权（本次 pr1-p0b-regression 已授权并实测）。
- 不向生产 runtime 添加 fallback/mock 路径掩盖真实失败（遵循 CONTRIBUTING/SECURITY）。
