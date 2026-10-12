# 本轮任务完成情况：2026-10-12 codex-remote 僵尸进程泄漏事故 P0 修复

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- Plan: `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md`（§5.1 P0，r2 评审 APPROVED，owner 2026-10-12 授权实施）
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/.exec-plan/state/plan-938d6b9865a5.json`
- Legacy State File: none（新队列，无 legacy 迁移）
- Completion Report Verdict: `proved-complete`
- Queue Summary: 16/16 todos done，16/16 proven；其中 3 个 `tests` 类为 `re-verified`（出口审计重跑），其余 `self-attested`
- Related Commits: `9365da6c`（codex-remote 收尸修复+测试）、`0c6016ad`（dsh-web 收尸修复+测试）、`1eb234ee`（事故档案+评审文档+think.md+CHANGELOG）；Release 产物内嵌 runtime commit `0c6016ad13a6`
- Generated At: 2026-10-12T12:12:00+08:00（queue hash `05ea56a4d6b8`）

## 1. Overall Verdict (总体结论)

事故文档 §5.1 P0（三处子进程收尸修复）已实施、测试、部署并对账收口，全部必需 todo 均带证明完成。核心证据为产物门对账：ChatGPT Desktop 退出 11.5 分钟内 53 次 `token_refreshed`（≈4.6/min，与修复前风暴节奏一致，`no desktop environment`×57 证明 restore 失败循环全速运行）而 runtime pid 95438 名下僵尸 7 次采样恒 0；修复前同条件为 1:1 泄漏（§3.3/§3.4 基线）。自动化测试（两包定向 + 新增 7 个收尸用例）在出口审计中重跑通过（`re-verified`）。P1（探针经济性）与 P2（缺席语义加固）按方案不在本轮范围（前者被上游刷新语义证据门挡住，后者可选项），见第 5 节。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| p0-fix auth 探针 | proven-done | proven-done (re-verified) | n/a (justified: 统一运行态对账覆盖) | complete | 代码 `git:9365da6c`；测试重跑 ok（re-verified）；运行态 53 刷新/0 僵尸（self-attested） |
| p0-fix oauth open | proven-done | proven-done (re-verified) | n/a (justified: 需真实 OAuth 交互，UI automation 永久停用) | complete | 代码 `git:9365da6c`；stub 断言重跑 ok（re-verified） |
| p0-fix dsh seat | proven-done | proven-done (re-verified) | n/a (justified: 自杀 stub 直接覆盖死亡链) | complete | 代码 `git:0c6016ad`；3 用例重跑 ok（re-verified） |
| p0-verify 部署 | proven-done | n/a (justified: 部署无自动化面) | proven-done | complete | 运行态核验五项（self-attested，命令输出留档于状态文件 artifacts） |
| p0-verify 对账 | — | — | proven-done | complete | 产物门对账数字（self-attested；采样档案 `build/zombie-reconcile-95438-*.log`） |
| p0-docs | proven-done | n/a (justified: D0) | n/a (justified: 纯文档) | complete | `git:1eb234ee`，git diff --check 通过（self-attested） |

## 3. Key File Changes (关键文件变更)

- `agent/codex-remote/chatgpt_auth.go`：`loadChatGPTAuth` defer 收尾改为 stdin.Close→Process.Kill→`cmd.Wait()`（满足 `StdoutPipe` 契约，`signal: killed` 属预期）；新增包级 var `chatGPTCodexBin` 作测试注入 seam。
- `agent/codex-remote/pairing_oauth.go`：`openBrowser` 由 `Start()` 改 `Run()`；新增包级 var `openBrowserBin`。
- `agent/dsh-web/resolver.go`：`execManagedStarter.Start` 挂单一 reaper goroutine 独占 `cmd.Wait()`（退出即收尸并清 `s.cmd`，Start 守卫恢复放行）；`Stop()` 信号-only（复用 `killProcessGroupNoWait`）。
- `agent/dsh-web/proc_unix.go` / `proc_windows.go`：删除 `terminateProcessGroup`（unix TERM→KILL 轮询版与 windows 版）；windows `killProcessGroupNoWait` 由 no-op 改为杀直接子进程不 Wait。
- 新增测试：`agent/codex-remote/chatgpt_auth_reap_test.go`（3 用例）、`pairing_oauth_reap_test.go`、`agent/dsh-web/starter_reap_test.go`（3 用例）。
- 文档：`think.md` 复盘条目、`CHANGELOG.md` [Unreleased]、事故档案状态/§5.5/§8.1/§4 回填、r1/r2 评审文档入库。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands: `go test ./agent/codex-remote/... ./agent/dsh-web/... -count=1`（工具链 `go1.26.6` 缓存路径直调，绕过 GOSUMDB=off 校验问题）
- Result: `ok agent/codex-remote 7.171s`、`ok agent/dsh-web 24.797s`（出口审计重跑；首轮 6.693s/24.381s）
- Attestation: `re-verified`（三个 tests todo 均为审计期重跑结果）
- Main test files: `chatgpt_auth_reap_test.go`、`pairing_oauth_reap_test.go`、`starter_reap_test.go`
- Artifact paths: 状态文件各 tests todo artifacts 字段（命令摘要）

### 4.2 Regression evidence

- 运行态（生产路径，非组件级）：Release 覆盖安装 `/Applications`（内嵌 `-version` = `0.1.0 (commit: 0c6016ad13a6, built: 2026-10-12T11:13:49Z)`），runtime pid 95438；离线态窗口 19:42:29–19:53:59 `token_refreshed` 7→60、`no desktop environment`×57、僵尸 7 采样恒 0；延展至 19:58:21 累计 81 次刷新（T0 后 74 探针）仍 0 僵尸；在线态 19:16–19:42 每 ~9.5min 刷新一次、僵尸恒 0；旧 runtime 81192 名下 ~50 僵尸随重启回收（全系统 56→3，余 3 属无关父进程）。
- Attestation: `self-attested`（owner 配合退出 Desktop 的实机观测，不可在审计中重放；采样档案见下）
- Artifact paths: `build/zombie-reconcile-95438-offline.log`、`build/zombie-reconcile-95438-online.log`
- n/a 项：三个修复单元的独立 regression todo 均 `required:false`，na_reason 记录于状态文件（由统一运行态对账 / stub 单测覆盖）。

### 4.3 Audit downgrade summary

- Downgraded todos: 无。
- 执行期内一次**状态文件索引错位**（reconcile 证明误写入 doc-sync-impl 下标）已按 todo id 全量修正并复核（16/16 done、必需证明齐全后重算 queue hash），未影响任何代码/测试/部署事实；修正后的状态以 `05ea56a4d6b8` 为准。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

- **P1 未实施（被证据门挡住）**：restore 循环在 Desktop 关闭时仍以 1~30s 粒度每轮全量 spawn 探针（修复后无泄漏但浪费进程+握手 CPU；19:42–19:58 观测 ≈4.6 次/分钟持续）。实施前必须先查证 `/Users/jacklee/Projects/codex` checkout 中 app-server-transport 的刷新语义，不得凭感觉定缓存窗口。
- **P2 未实施（可选）**：缺席状态机（not_installed/not_running/not_logged_in 区分）、永久性错误的重试预算、runtime 级 zombie watchdog 均未做；Desktop 关闭时日志仍常刷。
- **windows 交叉编译为 HEAD 既有破损**（`migrate.go`/`resolver.go` 的 `syscall.Kill`），非本轮引入亦未修复；本产品仅 darwin 运行。
- **Stop() 失去 TERM 宽限**：dsh-web 托管 seat 的失败路径现为立即 KILL（方案 §5.1-3 的既定取舍，调用方均为无响应/已死子进程路径）。
- 工具链环境：`GOSUMDB=off` + go.mod `toolchain go1.26.6` 使裸 `go build` 报校验错；本轮以缓存工具链 PATH（`~/.local/go-toolchains`）绕过。若复现，同法处理，勿改仓库 go.mod。

## 6. Audit Focus (建议审核重点)

1. `loadChatGPTAuth` 的 defer 收尾是否覆盖全部 Start 之后的返回路径（成功/未登录/超时/扫描 EOF），及 `cmd.Wait()` 与 scanner 的使用顺序是否满足 os/exec 对 `StdoutPipe` 的契约。
2. dsh-web reaper 的并发面：reaper 清 `s.cmd` 的 `s.cmd == cmd` 判断、Stop 信号化后与 reaper 的时序、Start 守卫在 reaper 尚未运行的调度窗口内拒绝一次 respawn 的影响（下一轮重试应成功）。
3. 对账数字的可复算性：`go-bridge.log` 中 `token_refreshed`/`no desktop environment` 计数与采样档案时间戳（19:42:29 基线 7 → 19:53:59 计 60）。

## 7. Constraints (关键约束)

- 测试验证路径遵守「Release 覆盖安装 `/Applications` → killall + pkill runtime → open」正式路径，无临时构建产物混入（`pgrep` 核验无 `/Applications` 之外实例）。
- Desktop 退出/重开由 owner 手动执行（owner 明确选择配合对账）；agent 未操控用户 GUI 应用。
- P1/P2 未获实施授权，本轮未触碰；评审通过 ≠ 实施授权的边界在事故档案头部保持记录。
