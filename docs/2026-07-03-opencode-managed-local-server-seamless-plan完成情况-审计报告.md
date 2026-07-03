# OpenCode managed local server seamless plan 完成情况 — 审计报告

审计日期：2026-07-03
被审计文件：[docs/2026-07-03-2026-07-03-opencode-managed-local-server-seamless-plan完成情况.md](2026-07-03-2026-07-03-opencode-managed-local-server-seamless-plan完成情况.md)
对应 plan：[docs/2026-07-03-opencode-managed-local-server-seamless-plan.md](2026-07-03-opencode-managed-local-server-seamless-plan.md)
exec-plan 状态：`.exec-plan/state/plan-41ad0453cd44.json`
合并提交：`f2f2cf6`（Merge `eac1ef8` PR #2）

## 审计结论

**总体属实，可独立复现。** 完成情况描述的功能、构建、覆盖安装与本机运行态验收，
均在本次审计中被独立重现。代码层没有发现 fake / mock / fallback 假成功路径（符合 CLAUDE.md 硬约束），
password 仅进 env 不进 argv/logs，状态文件 0600，端口避让与 pid adoption 逻辑正确。

存在 **1 项中等准确性问题**（Go 全量测试"通过"被夸大，实际 FAIL）和若干轻微 / 披露项，不影响功能验收。
没有阻塞性问题。

## 审计方法

不依赖报告自述，逐项与 ground truth 核对：
- 读 exec-plan 状态文件，核对 39/39 todo 与 evidence。
- 实跑 Go / Swift 定向测试与全量套件。
- 在本机独立复现运行态：`lsof` / `pgrep` / `curl` / `runtime.json` / Desktop 配置 / go-bridge 日志。
- 实测 opencode CLI 事实（绕过 shell alias）。
- 委派独立 code reviewer 审查 `OpenCodeManagedServer.swift` 等 8 个源/测试文件（read-only）。

## 逐项核对

| # | 报告声明 | 核对方式 | 结论 |
|---|---|---|---|
| 1 | `OpenCodeManagedServer` supervisor（CLI 发现 / env-only password / 0600 / 日志脱敏滚动 / health-gated / pid adoption / 端口避让） | 代码审查 + 单测实跑 | ✅ 属实 |
| 2 | 端口选择最终补强：健康但 pid 非持久记录的端口不复用 | 单测 `testPortSelectionSkipsHealthyPortWithStalePersistedPID` 实跑通过 | ✅ 属实 |
| 3 | RuntimeManager 启动 bridge 前解析 managed endpoint，退出时停止 child | `ps`/`runtime.json`/日志实证 + 代码 | ✅ 属实 |
| 4 | 新增 `managed_local` source，新装默认 Automatic / managed local | resolver 单测 + 代码 | ✅ 属实 |
| 5 | Settings UI 增加 Automatic (Recommended) | 代码 + CHANGELOG（未做 UI 自动化，符合项目约束） | ✅ 属实 |
| 6 | go-bridge managed URL project scope 回归测试 | `handlers_test.go` + 实跑 | ✅ 属实 |
| 7 | 更新 4 份文档 | grep 实证 4 文件均命中 managed_local | ✅ 属实 |
| 8 | Swift 定向测试（ManagedServer / EndpointResolver / Behavior）通过 | 实跑 25 例（19 resolver + 6 managed server）0 失败 | ✅ 属实（MacBridgeBehaviorTests 部分 16 例未单独复跑，但编译通过且 Behavior 测试不在失败集） |
| 9 | Go `OpenCodeListProjects\|OpenCodeListSessions\|OpenCodeListDirectory` 通过 | 实跑 `ok 1.543s` | ✅ 属实 |
| 10 | shutdown 竞态 `TestServerCloseAllConnectionsClosesActiveWebSockets -count=50` 通过 | 实跑 `ok 0.454s` | ✅ 属实 |
| 11 | **`go test ./go-bridge/... -count=1` 通过** | **实跑 = FAIL（9 个 codex CLI 缺失失败）** | ⚠️ **夸大，见发现 F1** |
| 12 | Debug build 通过 | test host 构建成功（Swift 测试依赖） | ✅ 间接属实 |
| 13 | Release build + 产物 `dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip` | dist 产物存在（Jul 3 10:22）+ sha256 | ✅ 属实 |
| 14 | 覆盖安装 `/Applications/CordCodeLink.app` 并运行 | 进程来自 `/Applications/.../Resources/cordcode-bridge-runtime` | ✅ 属实 |
| 15 | 运行态：8777 / managed 4097 / 401 / 200 / proxy 注册 / SSE / relay / agents available | 全部独立复现 | ✅ 属实 |
| 16 | 本机原 4096 保留未接管 | `lsof :4096` 仍为独立 opencode serve | ✅ 属实 |
| 17 | Desktop `defaultServerUrl` 同步到 4097 | `opencode.settings` 实测 = `http://127.0.0.1:4097` + `.cccode-backup` 备份 | ✅ 属实 |
| 18 | owner 人工确认 iOS + UI 显示「自动托管（推荐）」 | 无法独立复现 | 🔶 仅声明（与运行态一致，需 owner 自证） |
| 19 | exec-plan 39/39 done，全部带 evidence | 读 state 文件逐条核对 | ✅ 属实 |
| 20 | 0600 state / password 不入 argv / 日志脱敏 / 无 fake 成功路径 | `stat` 600 + `ps` argv 无 password + 代码审查 + `testEvaluateReadyRequiresHealthNotJustStdoutHint` 通过 | ✅ 属实 |

## 独立复现的关键证据

```text
# CLI 事实（绕过 alias）
opencode --version            → 1.17.13
opencode serve --help         → 含 --print-logs / --port default 0 / --hostname default 127.0.0.1（无 --register / service，符合 stable 轨）

# 运行态（审计当下）
lsof :8777 -sTCP:LISTEN       → cordcode-bridge-runtime（来自 /Applications）
lsof :4097 -sTCP:LISTEN       → opencode serve --hostname 127.0.0.1 --port 4097 --print-logs（PID 958）
lsof :4096 -sTCP:LISTEN       → 独立 opencode serve（PID 1436，未被接管）✅
ps cordcode-bridge-runtime    → argv 含 -opencode-url http://127.0.0.1:4097，无 password ✅
curl 4097 /global/health      → 401（no-auth）✅
runtime.json                  → pid 30551, managementUrl http://127.0.0.1:57655

# 凭据与状态文件权限
opencode-managed-server.json  → -rw------- (600)，含 password（owner-only，合规）✅
credentials.json              → -rw------- (600) ✅

# Desktop 同步
ai.opencode.desktop/opencode.settings → "defaultServerUrl":"http://127.0.0.1:4097" ✅
存在 opencode.global.dat.cccode-backup-* 备份（备份/还原式写入）✅

# go-bridge 日志（当前）
opencode HTTP proxy registered url=http://127.0.0.1:4097 ✅
relay-bridge-client auto-reconnect → wss://relay.byteseek.uk:8443 ✅
agent registered: claude / opencode / codex ✅

# 测试（实跑）
Swift: OpenCodeEndpointResolverTests 19 + OpenCodeManagedServerTests 6 = 25 例, 0 失败, ** TEST SUCCEEDED **
  含 testEvaluateReadyRequiresHealthNotJustStdoutHint / testPersistenceFileUses0600Permissions
  含 testPortSelectionSkipsHealthyPortWithStalePersistedPID / testStartsOpencodeServeWithSecretOnlyInEnvironment
Go:   OpenCodeListProjects|... → ok 1.543s
Go:   TestServerCloseAllConnectionsClosesActiveWebSockets -count=50 → ok 0.454s
Go:   ./go-bridge/... -count=1 → FAIL（仅 9 个 TestPaginatedMessages_* / TestListSessionsPagination*，全部 codex CLI not found）
```

## 发现的问题

### F1 [中等·准确性] Go 全量测试"通过"声明不准

报告与 exec-plan（phase7-tests / phase9-tests）将 `go test ./go-bridge/... -count=1` 列为通过证据。
实测该命令为 **FAIL**，9 个失败全部是 codex CLI 缺失导致：

```
codex: "codex" CLI not found in PATH, install with: npm install -g @openai/codex
--- FAIL: TestPaginatedMessages_*  (8)
--- FAIL: TestListSessionsPagination* (2 类，共 9 例)
```

**定性**：这 9 个失败是**已知环境噪音、非本次回归**——上一轮 plan 的记忆
（[memory: opencode-shared-service-plan-blocker](../../.claude/projects/-Users-jacklee-Projects-cordcode-macbridge/memory/opencode-shared-service-plan-blocker.md)）
已记录"clean main 同样失败"，且失败集与 managed_local 改动无关（全部在 codex 分页测试）。
本次审计实测：所有 OpenCode 定向用例与 shutdown 竞态用例均通过，无回归。

**问题在于措辞**：报告把全量套件写成"通过"，容易被读成"全套件绿"。
建议改为「OpenCode 定向 + shutdown 竞态 + 全量非 codex 用例通过；9 个 codex CLI 依赖的分页测试因本机未装 codex 失败，
clean main 同样失败，非本次回归」。属措辞修正，不改变验收实质。

### F2 [轻微·仓库卫生] 未追踪的 OpenCode Desktop 逆向产物

仓库根目录有两个 untracked 文件，**报告与 exec-plan 均未提及**：

- `index.js`（3662 行）：`import electron, { app, ... } from "electron"`、`@lydell/node-pty-darwin-arm64`、`electron-store` 等。
- `sidecar.js`（106 行）：`Server.listen({ username: "opencode", password, cors: ["oc://renderer"] })`，`import("./chunks/node-DjeWLYWe.js")`。

二者是 OpenCode Desktop（2.0 / lildax 轨）打包产物的逆向/抽取片段，用于调研 Desktop sidecar 行为，
**不属于本次 managed_local 实现**（managed_local 走的是 stable `opencode serve`，不依赖这些代码），
也不应提交。建议删除或加入 `.gitignore`，避免误提交第三方代码 / 触发后续 secret-scan 噪音。

> git status 另有 `M CLAUDE.md`、`?? .exec-plan/state/plan-a4ebbe0ec47d.json`（上一轮 Phase A external_http 的状态文件）、
> 4 个 handoff 文件——均为正常过程产物，非本次问题。

### F3 [披露·不可独立复现] 人工 / owner 授权项

下列声明只能由 owner 自证，审计无法独立复现，但其结论与当前运行态一致（旁证支持）：

- Phase 0 的 DQ-0 / DQ-1 / DQ-2 Desktop 承载性 gate（热重载失败 → Cocoa terminate/reopen fallback；冷启动服从 managedURL）。
  其 `/tmp/cordcode-dq0b-*` / `/tmp/cordcode-dq2-*` 证据目录审计时已不存在。当前 Desktop 已指向 4097 且有备份，与 DQ-2 结论一致。
- "owner 已人工确认 iOS 端 OpenCode 模式使用正常"。当前 Mac 侧 runtime/SSE/relay/agents/Desktop 全部正常，
  但 iOS 端需 owner 在真机确认。

这不算缺陷——这类项本就标注为 manual / requires owner authorization，符合 exec-plan 的 `method: manual` 标记；
仅作为审计边界如实记录。

### F4 [轻微·代码] 端口选择 TOCTOU 窗口 / crash 监督为被动

独立 code review 指出两点（均非功能缺陷）：

1. `selectPort` 与后续 adopt/start 之间存在良性 TOCTOU 窗口（极小概率端口被占）。supervisor 对自有不健康 child 走 SIGTERM 清理是有意设计，不会误杀未知进程。
2. crash 监督为**被动**：managed server 崩溃后由下次 bridge 重启拉起，不是独立看门狗自动重启。因 bridge 自身有 120min 定时重启 + 失败率限制，无 spin-restart 风险。

建议（可选）：补一个失败率限制 / adopt-kill 路径 / Desktop sync restart 流程的单测（现有测试未覆盖这三条）。

### F5 [cosmetic] exec-plan evidence pid 时点漂移

exec-plan phase9-regression 记录 `pid 88122 / managementUrl 53195`；审计当下 runtime 为 `pid 30551 / managementUrl 57655`。
原因是 app 在证据落盘后又重启过（文件 mtime 10:21–10:22 vs 证据 UTC 02:17）。结构事实（8777 / 4097 / 4096 保留 / argv 注入）全部成立，仅快照时点不同，非问题。

## 建议（按优先级）

1. **修措辞（F1）**：把完成情况和 exec-plan evidence 里的"go test ./go-bridge/... -count=1 通过"改为明确区分 codex 环境失败，避免"全套件绿"误读。
2. **清仓库（F2）**：删除或 `.gitignore` 根目录 `index.js` / `sidecar.js`。
3. **（可选）让全量套件在无 codex 环境也能绿（F1 根因）**：为 9 个 codex 依赖的分页测试加 `//go:build codex` 之类 tag 或 `t.Skip`，使本机与 CI 在缺 codex 时不报红——这样后续审计/CI 不再需要"已知噪音"解释。
4. **（可选）补测（F4）**：为失败率限制、adopt-kill、Desktop sync restart 三条路径补单测。

## 一句话总结

实现与验收**真实可信、可独立复现**，无 fake/mock 路径、无 password 泄漏、无回归；唯一需修正的是
"Go 全量测试通过"这一句措辞（实为 9 个 codex 环境失败），以及清理两份未追踪的 Desktop 逆向文件。

---

## 第二轮审计（整改复核，2026-07-03）

针对第一轮 F1 / F2 的三步整改，**全部独立复核通过**。本轮未信任整改自述，全部对到 ground truth。

### 整改 1（措辞）— ✅ 通过

完成情况第 31–33 行已删除原先误导性的「`go test ./go-bridge/... -count=1` 通过」，
替换为「全量 … 的措辞说明（审计订正）」，明确写出：8 个 codex 分页测试 + 1 个回归测试在无 codex 时 FAIL、
源于 `codex.New` 在 `exec.LookPath` 失败时直接报错、非本次回归、现已加 `requireCodexCLI(t)` skip，
其余非 codex 用例实跑通过。措辞准确、不再产生"全套件绿"误读。

### 整改 2（治本 t.Skip）— ✅ 通过

- `requireCodexCLI` helper 定义于 [pagination_test.go:23](../go-bridge/pagination_test.go)，`t.Helper()` + `exec.LookPath("codex")` + `t.Skipf`，
  skip 消息与生产 `codex.New` 报错**逐字一致**，doc comment 解释清楚。
- **放置精确**：`requireCodexCLI(t)` 是下列 9 个原本 FAIL 测试的函数体第一行，逐一核对——
  `TestPaginatedMessages_FullBackwardTraversalNoDupesOrGaps`(回归文件:17)、`_FirstAndBackwardPage`(107)、
  `_CompactsDuplicateLargeMessageFields`(184)、`_CursorStaleAfterRewrite`(268)、`_FallbackWhenNotOptedIn`(300)、
  `TestListSessionsPagination`(345)、`_CursorSurvivesAppend`(404)、`TestListSessionsPagination_TieBreakByID`(458)、
  `_CursorVersionMismatch`(504)。
- **未误伤**：不需要 codex 的纯逻辑测试（`TestCompactDuplicate…*`、`TestPaginationSamplesMatchWireShape`、
  以及原本就有自己 env-gate skip 的 `TestPaginatedMessages_RealSessionFirstPageBounded`）均未加该 guard。
- **CI 覆盖未丢失**：`.github/workflows/ci.yml:34-35` 注释 + `npm install -g @openai/codex --force`，
  CI 有 codex → 这些测试在 CI **照跑**，仅在无 codex 的开发机 skip。整改自述「CI 上有 codex 的 slot 仍覆盖」属实。

### 整改 3（删除杂物）— ✅ 通过

`index.js` / `sidecar.js` 已物理删除（`ls` 返回 No such file；`git status` 与 `git ls-files --others`
均不再列出）。`git diff --name-only` 本轮只含：`CLAUDE.md`（第一轮前就已是 M，非本轮）、完成情况 doc、
两个 pagination `*_test.go`——**未触碰任何生产 runtime 文件**，故无需重建/覆盖安装。

### 独立复跑证据

```text
gofmt -l 两个 _test.go                         → 无输出（已格式化）
go vet ./go-bridge                             → vet_exit=0，无输出
go test ./go-bridge/... -count=1               → GO_EXIT=0, ok 14.212s   ← 全量真正全绿
go test -v（9 个 codex 测试，本机无 codex）     → 全部 --- SKIP, PASS, GO_EXIT=0
```

> 复跑特别规避了第一轮踩过的坑：go 命令不接 pipe，直接 `echo "GO_EXIT=$?"` 取 go 真实退出码，
> 不再被 `tail` 的 0 掩盖。

### 发现状态更新

| 编号 | 第一轮定级 | 第二轮结果 |
|---|---|---|
| F1 Go 全量"通过"夸大 | 中等 | ✅ 已关闭（措辞订正 + 治本 skip，全量实跑 GO_EXIT=0） |
| F2 未追踪 Desktop 逆向产物 | 轻微 | ✅ 已关闭（已删除） |
| F3 人工/owner 授权项不可复现 | 披露 | 不变（性质使然，运行态旁证一致） |
| F4 端口 TOCTOU / 被动 crash 监督 | 轻微 | 不变（非缺陷；可选补测仍未做） |
| F5 exec-plan evidence pid 漂移 | cosmetic | 不变 |

### 第二轮结论

三步整改**全部完成且正确**，无新增问题、无生产代码改动、无需重建。
本轮改动（完成情况 doc + 2 个 `*_test.go`）尚未 commit；通过复核，可按 owner 意图提交。
唯一可选的后续是 F4 的三条补充单测（失败率限制 / adopt-kill / Desktop sync restart），不影响本轮验收。
