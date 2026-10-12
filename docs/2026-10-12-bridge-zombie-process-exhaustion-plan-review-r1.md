# 2026-10-12 bridge 僵尸进程耗尽事故档案 — 方案评审 r1

## 结论

- verdict: **REVISION_REQUIRED**
- plan: `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md`
  - SHA-256: `d8a8f981323d2c6b44aa83571fd61c722a61da7818eed955a8ffde83c669f730`
  - 本轮：**全量**评审（事故归因 + P0/P1/P2 修复方案 + 缺席矩阵 + 交付门）
- blockers: **1**；advisories: **4**
- implementation_readiness:
  - **不可按 §5.4 现稿宣告 P0 完成**（把 P2 watchdog 写进 P0 发布验收）。
  - **P0-1 / P0-2 设计本身可实施**（收尸契约清楚，一处修复覆盖全部 `loadChatGPTAuth` 调用方）。用户本轮只授权评审，**未授权改产品代码**。
  - P0-3 可做，但须先改掉「每次 grace-respawn +1 僵尸」的叙述（见 F-2）。
  - P1 仍被上游 token 刷新语义证据门挡住；P2 不是本事故闭环所必需。

评审模式：首轮独立评审。本会话曾在 Chat 仓排过 ZCode `EAGAIN` 表象，**未撰写**本事故档案；以下锚点均为对本仓 `bc311b6c` 工作树的亲核，不复用原档案的「已核实」声明。

## 来源与覆盖

| 项 | 值 |
| --- | --- |
| 仓库 | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` |
| 分支 | `feat/ios-native-message-timeline` |
| HEAD | `bc311b6cb7591edb72bbb931acfc1f7e395714a4` |
| 工作树 | 干净提交 + 未跟踪本事故档案（与档案 §8 一致） |
| 运行态身份（档案自述，本轮未复采 ps） | 旧 runtime pid 1917；A/B 新 pid 81192；路径 `/Applications/CordCodeLink.app` |
| 专项审计 | N/A（无外部 wire/格式解析命题） |

独立风险清单（读档案前形成，再对照亲核）：

1. `Kill()` 不 `Wait()` 是否真在 `loadChatGPTAuth`。
2. 无限 restore 是否真以「已安装未打开」为触发，且每轮都 spawn。
3. Desktop 在线后是否仍周期性 spawn（临时规约是否说满）。
4. dsh-web / oauth 是否同类泄漏、频率叙述是否成立。
5. 是否还有漏掉的 `Start()` 无 Wait。
6. 交付清单是否把可选工作卡死必需范围。
7. 测试是否可在本仓 CI 上客观验收。

已核范围：`agent/codex-remote/{chatgpt_auth,pairing_persist,backoff,pairing,diagnostics,pairing_oauth}.go`；`agent/dsh-web/{resolver,installer,proc_unix}.go`；`agent/grokbuild/{catalog_session_list,session,grokbuild,session_catalog}.go`；`agent/claudecode/{session,claudecode}.go`；`agent/codex/session.go`；`agent/codex-web/transport.go`；`agent/opencode-web/readiness.go`；`go-bridge/agent_descriptor.go`；`MacBridge/.../RuntimeProcessController.swift`；全仓 `*.go` 的 `cmd.Start()`。未核：官方 `codex` checkout 的 token 刷新粒度（P1 已标明待查）；本轮未重新 `ps` 活体采样（运行态数字沿用档案，不作为通过依据）。

### 锚点核验表

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录 | 定位 / 语义 | 亲核 |
| --- | --- | --- | --- | --- |
| `chatgpt_auth.go:46-107` 只 Kill 不 Wait | `agent/codex-remote/chatgpt_auth.go:46-107` | `:69 Start`；`:72 defer func() { _ = cmd.Process.Kill() }()`；全函数无 `Wait()` | 定位准 / 语义成立 | 亲核 |
| `:47-50` 未安装 `os.Stat` 早退 | 同文件 `:47-50` | `if _, statErr := os.Stat(bin); statErr != nil { return ..., "请先安装并登录 ChatGPT Desktop" }` | 准 / 成立 | 亲核 |
| `:59` spawn `app-server --stdio` | 同文件 `:59` | `exec.CommandContext(ctx, bin, "-c", "model_provider=openai", "app-server", "--stdio")` | 准 / 成立 | 亲核 |
| `restorePersistedPairing` `:299-330` 有 store 则无限循环 | `pairing_persist.go:299-330` | `:304 Stat(storePath)` 成功后 `for { restoreOnce; backoff.Next(); sleep }` | 准 / 成立 | 亲核 |
| `restoreOnce` `:266` auth → `:274` refresh → `:280` lookupDesktopEnv | `pairing_persist.go:225-297` | 顺序与档案一致；`:208` `"no desktop environment"` → 「请打开 ChatGPT Desktop」 | 准 / 成立 | 亲核 |
| backoff 1s 封顶 30s 后重数 | `backoff.go:15-48` | `reconnectBackoffBase=1s`，`Cap=30s`，`d>=Cap` 时 `attempt=0` | 准 / 成立 | 亲核 |
| 调用方 `pairing.go:137` / `diagnostics.go:55` | `pairing.go:133-137`；`diagnostics.go:55` | `return loadChatGPTAuth(ctx)`；`loadChatGPTAuth(ctx)` | 准 / 成立（另有 `runCtrlTokenRefresh:80` 未在 §3.2 点名，见 F-3） | 亲核 |
| `pairing_oauth.go:129` open 无 Wait | `pairing_oauth.go:128-129` | `exec.Command("/usr/bin/open", rawURL).Start()` | 准 / 成立 | 亲核 |
| dsh-web `Start` `:195-242` 无 Wait | `resolver.go:191-242` | `:237 Start` 后 `:240 s.cmd=cmd; return pid`，无 Wait | 准 / **「每次 grace-respawn +1」不成立**，见 F-2 | 亲核 |
| `terminateProcessGroup` Stop 路径有 Wait | `proc_unix.go:34-51` | ESRCH / KILL 后 `cmd.Process.Wait()` | 准 / 成立 | 亲核 |
| `grace-expiry-respawn` `:816` | `resolver.go:814-818` + `:619` | 标签函数成立；`:619` spawn 前**未** `Stop()`；`Start` 在 cmd 仍在时直接报 already has a process | 定位准 / 语义「每轮留一具旧尸体」过强 | 亲核 |
| grok catalog Wait `:175-183` | `catalog_session_list.go:177-185` | `go func() { _ = cmd.Wait(); ... }()` 在 `:178-185` | 行号略偏 / 语义成立 | 亲核 |
| grok turn Wait `session.go:278` | `session.go:277-278` | `waitErr := cmd.Wait()` | 准 / 成立 | 亲核 |
| claude Wait `session.go:365` | `claudecode/session.go:364-365` | `waitErrCh <- cs.cmd.Wait()` | 准 / 成立 | 亲核 |
| claude LookPath `claudecode.go:217` | `:216-218` | `exec.LookPath(cliBin)` | 准 / 成立 | 亲核 |
| grok LookPath `grokbuild.go:171` | `:171-172` | `exec.LookPath(a.cliBin)` | 准 / 成立 | 亲核 |
| dsh 未安装 `:201-202` | `resolver.go:201-202` | `no dsh binary found ... install dsh first` | 准 / 成立 | 亲核 |
| opencode-web `readiness.go:12-14` / `:46` | `:12-14` 为注释；`:36-47` 为常量与 `baseURL=="" → not_configured` | 注释+实现都 fail-closed | 注释行当代码行 / 语义成立 | 亲核 |
| sqlite `session_catalog.go:348` | `grokbuild/session_catalog.go:348-350` | `:348 Command`；**`:350 Output()`** | 行号偏一行 / 语义成立（Output 内含 Wait） | 亲核 |
| `agent_descriptor.go:411` CombinedOutput | `go-bridge/agent_descriptor.go:411-412` 为 `ps`+`Output()`；CombinedOutput 在 `:435` | 见摘录 | **定位不覆盖 CombinedOutput** / 两边都是内含 Wait 的同步 API | 亲核 |
| codex Start/Wait `session.go:146/252` | `:146 Start`；`:252 Wait` | 配对成立 | 准 / 成立 | 亲核 |
| codex-web `:143/186` | `transport.go:143 Start`；`:186 go Wait` | 配对成立 | 准 / 成立 | 亲核 |
| Swift `RuntimeProcessController.swift:74` | `:74 terminationHandler` | `Process.terminationHandler` 常规管理 | 准 / 成立 | 亲核 |

全仓 `Start()` 抽样：生产路径漏收尸仅档案已列的 `chatgpt_auth` / `pairing_oauth` / `dsh-web execManagedStarter.Start`；`dsh-web/installer.go:263` 已有单一 Wait goroutine（`:267`），不应算泄漏。`deprecated/` 未挂载。

## 跨轮核验复用

无前轮评审报告。本轮全部亲核。

## 意见

### [F-1][阻塞] §5.4 把 P2 watchdog 写进 P0 发布验收，可选范围卡死必需闭环

- 位置：§5.1 P0 vs §5.3 P2-3 vs §5.4「运行态核验（… + watchdog 特征输出）」；对账数字「僵尸恒为 0」本身是 P0 产物门。
- 证据：P2-3 明确是「runtime 级 zombie watchdog」且标为缺席矩阵加固；§5.4 却要求 Release 安装后核验 watchdog 特征输出。CLAUDE.md「行为修复的产物门」要求的是对账数字（档案已有：N 次 `token_refreshed` vs 僵尸 0），不要求先做 watchdog。
- 影响：只做 P0-1 无法按现稿勾完成；要么把事故修复拖进 P2，要么在未做 watchdog 时假完成。
- 修订方向：§5.4 拆成「P0 完成门」与「P2 完成门」。P0 完成门 = 三处收尸 + 定向测试绿 + 覆盖安装后 **关闭 Desktop 跑 N 分钟、`token_refreshed`≥N 且 runtime 名下僵尸=0**。watchdog 特征输出只留在 P2。
- 闭合标准：§5.4 不再把 watchdog 列为 P0 发布核验项；P0 对账数字可独立勾选。

### [F-2][建议] dsh-web「grace 到期 respawn 每轮 +1 僵尸」与源码不符

- 位置：§3.5 / §5.1-3 / §6 矩阵 △ 行。
- 证据：`loseSeatLocked`（`:789`）不 `Stop`/`Wait`；grace 后 `:619 spawnOnSeat` 前也不 `Stop`。但 `execManagedStarter.Start` `:194-196` 在 `s.cmd != nil && Process != nil` 时直接返回 `already has a process`，不会再 spawn。死子进程在 Unix 上 `kill(pid,0)` 仍成功，因此更像「1 个僵尸卡住 + 后续 respawn 被挡」，不是「每次崩溃+respawn 再堆一具」。`spawnOnSeat` 失败/超时路径会 `Stop()` 从而 Wait；成功托管后崩溃走 `loseSeatLocked` 才是未收尸窗口。已有 `killProcessGroupNoWait`（`proc_unix.go:58-67`）专给「别人拥有唯一 Wait」用。
- 影响：P0-3 的 reaper 仍值得做，但按「每轮 +1」写测试会写不出复现；也可能误判优先级。
- 修订方向：改成「托管 seat 死后 `loseSeatLocked` 不 reap，cmd 仍占位，Start 被拒，留下 1 个僵尸直到某条 Stop」。P0-3 复用 `killProcessGroupNoWait` + 单一 reaper，不要再发明第三套信号函数。
- 闭合标准：叙述与 `Start` 拒绝条件一致；测试断言「子进程死后 Wait 被调用 / pid 不留 Z」，而不是「respawn N 次产生 N 僵尸」。

### [F-3][建议] §4.3 说 Desktop 在线后「不再周期性 spawn」过满

- 位置：§4.3；对照 `watchBinding` `:356-359` → `maybeRefreshCtrlToken` → `runCtrlTokenRefresh` `:80` → `loadChatGPTAuth`。
- 证据：成功 restore 后进入 `watchBinding`，每 2s tick 在 lead window（60s）内会再走一遍 auth 探针。token 600s + min retry 30s → 大约每数分钟 1 个僵尸，远低于 4.6/分钟，但不是「仅断线重连时 spawn 一次」。
- 影响：临时规约「保持 Desktop 运行」能避免 8 小时打满，不能把泄漏降到 0；P0-1 仍覆盖该路径，不必另开缺口。
- 修订方向：§4.3 改为「在线时恢复循环退出，泄漏降为 ctrl-token 刷新频率（约数分钟一次），P0 安装前仍可能缓慢堆积」。
- 闭合标准：临时规约不再声称零周期性 spawn。

### [F-4][建议] P0-1 测试注入点写「把 candidates() 做成包级 var」，但函数已存在；缺的是 bin 路径覆盖

- 位置：§5.1 回归测试段。
- 证据：`chatGPTCodexCandidates()` `:19-25`、`resolveChatGPTCodexPath(exists func)` `:27-37` 已可测路径解析。`loadChatGPTAuth` 仍经 `defaultChatGPTCodexPath()` 拿真实路径再 `exec`。现有 `capture_transport_error_test.go:101` 直接打真实 `loadChatGPTAuth`。
- 影响：按字面改 candidates 仍测不到 spawn/Wait；假二进制注入应覆盖 `defaultChatGPTCodexPath` / 包级 `authCodexBin`。
- 修订方向：写明可注入的是「实际 exec 的 bin」，假进程读 initialize/getAuthStatus 后挂住；断言 `Wait` 返回后 pid 不在 Z。尽量避免单测依赖 `ps`（GOOS/权限/并行易碎）；`Wait()` 返回即收尸完成。
- 闭合标准：测试不碰真实 ChatGPT.app，且在 `Wait` 契约上失败会红。

## 复审处置与剩余门

无前轮意见。

仍待证据/OD（**不是本轮阻塞**，但实施前要分清）：

- P1 auth 缓存窗口：须对照本机 `/Users/jacklee/Projects/codex` app-server-transport 刷新语义；未查证前不得定窗口、不得当 P0。
- P2 缺席状态机 / 重试预算 / watchdog：事故闭环不依赖它们。
- 运行态复采：本轮未重新 `ps`；P0 安装后的对账数字是**实施完成门**，不是方案通过门。

授权边界：评审通过不等于批准改代码、发版或再杀 runtime。
