# 2026-10-12 codex-remote token 刷新循环僵尸进程泄漏，致整机 fork 配额耗尽（事故档案）

> 状态：**已修复（P0 于 2026-10-12 实施完成并通过对账，见 §5.5 与 §8 实施段）**。
> 修复范围 = §5.1 P0 三处收尸（commit `9365da6c` + `0c6016ad`，Release 0.1.0 内嵌
> runtime commit `0c6016ad13a6` 已覆盖安装）；P1（探针经济性）仍被上游刷新语义证据门
> 挡住未实施，P2（缺席语义加固）可选未实施，两者待 owner 另行授权。
> 本文记录问题描述、根因、已执行的止血、修复方案（P0/P1/P2）、P0 实施对账，并回答
> 「backend 未安装 / 未打开时是否会出现同类问题」的系统性问题（§6 缺席矩阵）。
>
> **修订记录 r1（2026-10-12）**：按
> `docs/2026-10-12-bridge-zombie-process-exhaustion-plan-review-r1.md`
> （REVISION_REQUIRED：阻塞 F-1，建议 F-2~F-4）修订。本轮改动范围 = 仅本文档，
> 未改产品代码。四条意见**全部采纳**（含亲核源码复证），无不采纳项：
> F-1 §5.4 拆分为 P0/P2 完成门，watchdog 不再卡 P0；F-2 dsh-web 失效模式勘正为
> 「1 僵尸滞留 + respawn 被 Start 守卫拒绝」（原文「每次 respawn +1」与
> resolver.go:194-196 不符）；F-3 §4.3 勘正「Desktop 在线 ≠ 零 spawn」（在线态
> ctrl-token 刷新仍走 auth 探针）；F-4 回归测试注入点改为实际 exec 的 bin 路径、
> 断言改 Wait 契约（不再依赖 ps 系统态）。另按评审锚点表勘误行号：grok catalog
> Wait goroutine 178-185、sqlite `Output()` :350、agent_descriptor `ps+Output()`
> :412 / `CombinedOutput()` :435。
>
> **评审记录 r2（2026-10-12）**：
> `docs/2026-10-12-bridge-zombie-process-exhaustion-plan-review-r2.md` 判定
> **APPROVED**（阻塞 0 / 建议 0），F-1~F-4 全部闭合，**P0 设计通过**。r2 所审
> 版本 SHA-256 =
> `7e7a505a1fe537c9882c3f10dc9fc3b4f8cca2c372efab3f8c3f32473f25f2af`；
> 过审后本文只做两处**非设计性补记**（本段 + §5.1-1 调用方括号补
> `runCtrlTokenRefresh`），设计内容与过审版一致。落实 r2 的剩余门（与 §5.4
> 一致）：P0 实施完成门 = 定向测试绿 + 覆盖安装后关 Desktop 跑 N 分钟对账，
> 未过不得宣称已修复/发版；P1 仍被上游刷新语义证据门挡住（未查证官方
> checkout 前不得定缓存窗口）；P2 可选、不阻塞 P0；评审通过不增加写代码/
> 发版/杀 runtime 的授权。§5.1-1 的括号补记对应 r2 修订回归的说明——该清单
> 不全但修复点在 `loadChatGPTAuth` 一处、路径仍被覆盖，评审员已注明**不重开
> 意见**，此处仅为清单完整性。

## TL;DR

- **不是线程泄漏，是进程表配额被僵尸占满**。macOS 按 UID 限制进程表条目
  （`kern.maxprocperuid=2666`，僵尸仍占条目）。
- 泄漏源：`agent/codex-remote/chatgpt_auth.go` 的 `loadChatGPTAuth` 每次 spawn 一个
  ChatGPT 内嵌 codex `app-server --stdio` 探针，读完登录态后只 `Kill()` **从不
  `Wait()`**，每个探针留下一个僵尸。
- 触发条件：**曾配对成功（持久化 pairing 存在）+ ChatGPT Desktop 已安装但未运行**。
  此时 `restorePersistedPairing` 无限重试，每轮都先刷一次 auth（+1 僵尸）再失败于
  「no desktop environment」，退避 1s→30s→归零重数，平均 ~4.6 个僵尸/分钟。
- 7.7 小时攒下 2142 个僵尸，用户进程 2601/2666，整机无法 fork：Claude 崩溃循环、
  xcodebuild、ZCode、Ghostty 的 EAGAIN 全是**受害者**，不是根因。
- 「根本没安装 ChatGPT App」**不会**复现本事故（`os.Stat` 早退，不 spawn、无僵尸）；
  危险的恰恰是「已安装但不可用（未打开 / 未登录）」这个中间态（§6）。

## 1. 事故现象

2026-10-12 下午起，整机间歇性无法创建进程，多个应用同时报错：

- Terminal / Ghostty：`forkpty: Resource temporarily unavailable`（未能创建新的进程和
  打开伪终端）；
- ZCode：`spawn git EAGAIN`、`spawn ZCode Helper EAGAIN` → `SQLite startup failed:
  transport_closed`；
- Claude Desktop 17:43–17:44 连崩 3 次（启动 0.75 秒即崩，崩溃拉起循环）；
- xcodebuild 17:37 崩溃（崩在 `NSTask launch`，responsible process 是 claude）。

用户当天上午重启过一次机器，恢复数小时后复发——「重启只是把泄漏清零」。

早期（错误）假设曾把责任归给「Claude 在跑 Xcode-beta iOS 构建拉起上百个编译进程」；
后续进程普查发现 Claude 只是受害者，真正持有 2000+ 僵尸的是
`cordcode-bridge-runtime`。本档案基于对 runtime 名下僵尸的精确归因完成。

## 2. 时间线与量化证据

全部来自本机实测（采样工具 `ps`，时间 2026-10-12 下午）：

| 时间 | 事实 |
| --- | --- |
| 10:26:25 | bridge runtime 启动（pid 1917，`/Applications/CordCodeLink.app` 内嵌，drivers=claude,codex-remote,grokbuild,dsh-web,opencode-web）。**10:26:25 即产生第一个僵尸**（恢复循环开机即跑） |
| 17:37–17:44 | xcodebuild / Claude 崩溃（进程配额接近耗尽的外在表现） |
| ~17:52 | 另一 agent 普查：用户进程 2532/2666，其中 2089 个 `<defunct>`，2108 个僵尸父进程为 1917 |
| 18:05:50 | 本调查首次采样：僵尸 2111（1917 名下 2108），用户进程 2561/2666 |
| 18:15:50 | 僵尸 2142，用户进程 2601/2666（**余量 65，约 12 分钟后打满**）；10 分钟增量 31 → ~3.1/分钟；全程平均 2142/466min ≈ **4.6/分钟** |
| 18:17 | 止血：`kill 1917` → launchd 回收全部僵尸；Mac app 自动拉起新 runtime（pid 81192，路径仍为 `/Applications` 正式版） |
| 18:16:58 | 新 runtime 启动后 **2 秒**出现第一个僵尸；95 秒内累积 11 个（A/B 复证，§3.4） |

唯一存活的子进程：grok catalog 单例（pid 1922，存活 7h44m）——grok 链路正常。

## 3. 根因

### 3.1 泄漏点（源码锚点）

`agent/codex-remote/chatgpt_auth.go:46-107` `loadChatGPTAuth`：

```go
// :47-50 未安装 ChatGPT.app 时 os.Stat 失败，这里就返回了（不 spawn、无僵尸）
bin := defaultChatGPTCodexPath()
if _, statErr := os.Stat(bin); statErr != nil {
    return "", "", fmt.Errorf("请先安装并登录 ChatGPT Desktop")
}
...
cmd := exec.CommandContext(ctx, bin, "-c", "model_provider=openai", "app-server", "--stdio")  // :59
...
if err := cmd.Start(); err != nil { ... }        // :69
defer func() { _ = cmd.Process.Kill() }()        // :72  ← 只 Kill，全函数无 cmd.Wait()
```

探针读完成功（或失败/超时）返回时，`Kill()` 发信号但**没人收尸**——子进程退出后
成为挂在 runtime 名下的僵尸。`StdoutPipe()` 的 os/exec 契约也要求调用方 `Wait()`
收尾，当前实现同时违反两条。

### 3.2 触发链（为什么它会无限跑）

`agent/codex-remote/pairing_persist.go:299-330` `restorePersistedPairing`：只要持久化
pairing 文件存在（`:304 os.Stat(p.storePath)`），就**无限循环**调用 `restoreOnce`
（`:225-297`），每轮：

1. `p.chatGPTAuth(ctx)`（`:266`）→ `loadChatGPTAuth` → **spawn 探针 + Kill 不
   Wait → +1 僵尸**；
2. `refreshControlToken(ctx)`（`:274`）→ 成功（日志 `ctrl_token_applied` /
   `token_refreshed`，token 名义有效期 600s）；
3. `lookupDesktopEnv(ctx, rec.EnvID)`（`:280`）→ ChatGPT Desktop 未运行 →
   `no desktop environment` → 本轮失败；
4. `offlineMessageForRestoreError`（`:204-213`）把消息映射为「请打开 ChatGPT
   Desktop」，退避后重试。

退避策略 `agent/codex-remote/backoff.go`：基准 1s、封顶 30s、±10% 抖动、**封顶当次
返回后从基准重数**（注释标明镜像官方 codex-rs `app-server-transport websocket.rs
next_reconnect_delay`）。因此永久性故障下重试永不停止，节奏在 1s~30s 间循环，
平均 ~4.6 次/分钟——每个周期泄漏一个探针。

**触发前提**：曾配对成功（磁盘上有 pairing store）+ ChatGPT Desktop 已安装（内嵌
codex 二进制存在）但 app 未运行。开机即触发（两个实例的首个僵尸都在启动后 2 秒内
出现），完全解释「上午重启修好、几小时后又挂」。

`loadChatGPTAuth` 的全部调用方都各泄一个/次：恢复循环（§3.2）、配对流程
（`pairing.go:137`）、诊断（`diagnostics.go:55`），以及 **Desktop 在线时的
ctrl-token 主动刷新**——`watchBinding` 的 2s tick 在 token 临近过期时（lead=60s /
minRetry=30s，pairing_persist.go:23/28）经 `maybeRefreshCtrlToken`
（pairing_persist.go:37-71）→ `runCtrlTokenRefresh`（:79-93，`:80` 先
`chatGPTAuth` 再刷 token）同样 spawn 探针（r1 F-3 补录：在线态并非零 spawn，
见 §4.3）。

### 3.3 证据对账（僵尸 lstart ↔ 日志 token_refreshed 一对一）

僵尸进程的 `lstart` 即探针 spawn 时刻；日志 `token_refreshed` 在其后 ~1.5–2s
（auth 握手耗时）。旧实例（1917）：

| 僵尸 lstart | 日志 token_refreshed |
| --- | --- |
| 18:07:10 | 18:07:11.504 |
| 18:07:27 | 18:07:28.489 |
| 18:07:58 | 18:07:59.978 |
| 18:08:01 | 18:08:03.437 |
| 18:08:06 | 18:08:07.870 |
| 18:09:11 | 18:09:13.265 |
| 18:09:14 | 18:09:16.385 |

15 秒节奏的 `opencode-web: endpoint probe ok`（纯 HTTP，不 spawn）与 60 秒节奏的
claude fork 检测（读 JSONL 文件）时间网格均**不**与僵尸对齐——排除。

### 3.4 A/B 复证（干净实例确定性重现）

kill 1917 后（僵尸 2142→5，用户进程 465/2666），ChatGPT Desktop 仍未运行。新
runtime 81192 于 18:16:56 启动：18:16:58 首个僵尸（pid 81193），95 秒内 11 个，
且与新一轮 `token_refreshed`（18:18:13/16/20/26/36）继续一一对齐。根因在干净
实例上确定性重现，非旧实例的偶发状态残留。

### 3.5 排除项（其余周期循环的收尸现状）

| 循环 / 子进程 | 频率 | 收尸 |
| --- | --- | --- |
| opencode-web endpoint probe | 15s | 纯 HTTP，无子进程 |
| claude session fork 检测 | ~60s | 读 JSONL 文件，无子进程 |
| grok catalog 单例 | 常驻 | 后台 `cmd.Wait()` goroutine（catalog_session_list.go:178-185）；实测存活 7h44m |
| grok 每 turn 子进程 / claude 每 session CLI | 按需 | `session.go:278` / `session.go:365` 有 Wait goroutine |
| sqlite3 / ps / lsof / --version 探针 | 按需 | 同步 API 内部 Wait：`Output()`（session_catalog.go:350、agent_descriptor.go:412）、`CombinedOutput()`（agent_descriptor.go:435） |
| 退役 backend（codex / codex-web） | 未挂载 | Start/Wait 均配对（codex/session.go:146/252、codex-web/transport.go:143/186） |
| Swift 端 managed opencode server（4096） | 常驻 | `terminationHandler` 常规管理（RuntimeProcessController.swift:74）；实测 Mac app 名下 0 僵尸 |

残余同类问题（低频，非本次事故源，随 P0 一并修）：

- `agent/codex-remote/pairing_oauth.go:129`：`exec.Command("/usr/bin/open", rawURL).Start()`
  fire-and-forget，无 Wait。每次 OAuth 配对点击泄一个。
- `agent/dsh-web/resolver.go:191-242` `execManagedStarter.Start`：seat 进程
  （`dsh web`）spawn 后无任何 Wait。`Stop()` 路径有收尸（proc_unix.go:34-51
  terminateProcessGroup → `Process.Wait()`），`spawnOnSeat` 的失败/超时/子进程
  早亡路径也都会 `Stop()`。**未收尸窗口 = 托管 seat 自发死亡**：`loseSeatLocked`
  （resolver.go:789）只进 grace 窗口、不 Stop/Wait，死进程占着 `s.cmd` 位。
  r1 F-2 勘正失效模式：**不是「每次 respawn 再堆一具」，而是 1 个僵尸滞留 +
  grace 到期的 respawn 被 `Start` 的 already-has-a-process 守卫
  （resolver.go:194-196）拒绝——seat 自动恢复同时被卡**，直到某条路径调用
  `Stop()` 才收尸清位。泄漏不随 respawn 次数累积（每个 starter 生命周期至多
  1 个），但附带的功能损害（自动恢复失效）比原叙述更重。P0-3 修复目标。

### 3.6 为什么全家一起挂

macOS 按 UID 计进程表条目（`sysctl kern.maxprocperuid` = 2666），僵尸不释放条目。
2142 僵尸 + ~460 活进程 ≈ 2601/2666 时，任何 `fork()`/`posix_spawn` 返回 EAGAIN：
Claude（NSTask 启动即崩）、xcodebuild、ZCode Helper（SQLite 打不开）、Ghostty
（forkpty）同时报错——「Claude 跑构建打满进程」是果不是因。

## 4. 止血（已执行 + 复发手册 + 临时规约）

> 修复包已于 2026-10-12 覆盖安装（§5.5），本节 §4.2/§4.3 仅在回滚到修复前构建时适用。

### 4.1 已执行（2026-10-12 18:17）

1. 固定证据后 `kill 1917`（只杀 runtime；`killall CordCodeLink` 匹配不到内嵌
   runtime，见 incidents/2026-08-25-runtime-restart.md）；
2. launchd 立即回收 2142 个僵尸（僵尸 2142→5，用户进程 2601→465/2666）；
3. Mac app 自动拉起新 runtime（supervisor 自愈），核验：新 pid 81192、
   `/Applications/CordCodeLink.app` 路径、8777 监听者身份正确。

### 4.2 复发处置手册（修复包安装前再次变慢/报 EAGAIN 时）

```bash
# 1) 确认还是同一个泄漏（僵尸父进程应为当前 runtime pid）
ps -axo ppid=,stat= | awk '$2 ~ /^Z/ {print $1}' | sort | uniq -c | sort -rn | head -3
sysctl kern.maxprocperuid; ps -u $(id -un) | wc -l

# 2) 只重启 runtime（Mac app 会自动拉起，iOS 客户端自动重连）
kill "$(pgrep -f 'cordcode-bridge-runtime -port 8777')"

# 3) 核验：僵尸回落、8777 由新 pid 监听
ps -axo stat= | grep -c '^Z'; lsof -nP -iTCP:8777 -sTCP:LISTEN
```

### 4.3 临时规约（修复包安装前；r1 F-3 勘正）

**保持 ChatGPT Desktop 运行——大幅降频，但不是零。** Desktop 在线时
`restoreOnce` 成功收口，1~30s 粒度的无限重试退出；但 `watchBinding` 的 2s tick
会在 ctrl token 临近过期时（lead 60s、min retry 30s）经
`maybeRefreshCtrlToken` → `runCtrlTokenRefresh` → `chatGPTAuth` 再 spawn 一次
auth 探针（pairing_persist.go:37-71、79-93）。按 token 有效期 600s 估算约每 10
分钟量级一次（刷新失败时以 ≥30s 节奏重试），修复前在线态每小时仍会缓慢堆积
个位数僵尸——数天内打不满配额，但非零；长期不装修复包可定期按 §4.2 重启
runtime 清僵尸。若坚持不开 Desktop，泄漏以 ~4.5/分钟继续，约 8 小时后再次打满
（余量 ≈ 2666−465 ≈ 2180）。

## 5. 修复方案（待实施）

### 5.1 P0：补收尸（本次事故直接根因，三处）

1. **`agent/codex-remote/chatgpt_auth.go`**：`loadChatGPTAuth` 的收尾改为
   关 stdin → `Kill()` → `cmd.Wait()`（os/exec 对 `StdoutPipe` 的契约要求；
   Wait 返回 `signal: killed` 属预期，忽略即可）。一处修复覆盖全部调用方
   （restoreOnce / pairing / diagnostics / 在线 ctrl-token 刷新
   `runCtrlTokenRefresh`，完整清单见 §3.1）。
2. **`agent/codex-remote/pairing_oauth.go:129`**：`open` 改为 `Run()`（毫秒级
   返回，内部 Wait），或 `Start()` + 后台 goroutine `Wait()`。
3. **`agent/dsh-web/resolver.go`**：针对 §3.5 勘正后的失效模式（托管 seat 自发
   死亡 → 1 僵尸滞留 + respawn 被 Start 守卫拒绝），`execManagedStarter.Start`
   成功后挂一个**单一**后台 reaper goroutine `cmd.Wait()`——子进程退出即收尸
   并清 `s.cmd`，Start 守卫恢复放行（seat 自动回归）。停止路径复用现有
   `killProcessGroupNoWait`（proc_unix.go:58-67，专为「唯一 Wait 属于别的
   goroutine」的调用方准备），不新造第三套信号函数；`terminateProcessGroup` /
   `Stop()` 改为只发信号不 Wait，避免与 reaper 双 Wait 竞态
   （`waitid: no child processes`）。本项修的是「1 僵尸 + 恢复被卡」，不是
   「respawn N 次堆 N 僵尸」（后者与 Start 守卫语义不符）。

回归测试（r1 F-4 修订）：注入点是**实际 exec 的 bin 路径**——把
`loadChatGPTAuth` 里 `defaultChatGPTCodexPath()` 的产物参数化或提为包级可覆盖
var 即可；`chatGPTCodexCandidates()` / `resolveChatGPTCodexPath(exists)` 本身
已可测，不必再包一层。测试注入「假 codex 二进制」（shell 脚本：读
initialize/getAuthStatus 后输出合法 JSON 再挂住），断言以 os/exec 的 **Wait
契约**为准——`Wait()` 返回即收尸完成：观测 Kill→Wait 顺序与 Wait 返回值
（`signal: killed` 属预期）；**不把 `ps` 系统态轮询作为 CI 硬断言**
（GOOS/权限/并行易碎），系统级佐证仅作本地诊断。dsh-web 与 oauth 两处同法：
断言「子进程死后 Wait 被调用 / pid 不留 Z 态」，而非「respawn N 次产生 N
僵尸」（后者与 Start 守卫语义不符，写不出复现，见 §3.5 勘正）。

### 5.2 P1：restore 循环的探针经济性（行为变化，随修复评审）

token 名义有效期 600s，恢复循环却在 1~30s 粒度上每轮全量 spawn 一个
`app-server --stdio` 探针再丢弃——修好收尸后这只是浪费（每轮一个短命进程 + 握手
CPU）。可选优化：auth 结果缓存（如 4~5 分钟内复用）、或仅在 ctrl token 临近过期
时才刷 auth。**属行为语义变化**，实施前须按上游源码优先门对照本机
`/Users/jacklee/Projects/codex` checkout 中 app-server-transport 的刷新语义
（backoff 已对齐 `next_reconnect_delay`，token 刷新粒度是否也有官方对应物需查证），
不得凭感觉定缓存窗口。

### 5.3 P2：缺席语义加固（§6 矩阵暴露的系统性缺口）

1. **缺席状态显式化**：每个 backend 的 readiness 区分
   `not_installed / not_configured / not_running / not_logged_in / available`。
   opencode-web 已有先例（readiness.go:12-14 注释与 :36-47 实现，fail-closed，
   probe 失败不得折叠为 not_configured）；dsh-web 有 `ErrSeatNotRunning`。codex-remote
   目前只有 offline 消息映射（pairing_persist.go:204-213），无状态机语义。
2. **重试预算**：永久性错误（未安装）与暂时性错误（未打开）不同对待——未安装
   时降为低频探测或停轮（当前未安装虽不泄进程，但恢复循环永不停止、日志常刷）。
3. **runtime 级 zombie watchdog**：runtime 周期自检自身名下子进程数与僵尸数，
   超阈值打 WARN（特征输出），既当报警也当长期回归证据（对齐 CLAUDE.md 部署后
   运行态验证第 2 条「新版本特征输出」）。

### 5.4 完成门与验收对账（r1 F-1 拆分：P0 与 P2 分开勾选）

**P0 完成门（本事故闭环的必需范围，不含 watchdog）：**

- 三处收尸修复 + 回归测试 + `go test ./agent/codex-remote/... ./agent/dsh-web/...`
  定向绿；
- Release 构建覆盖安装 `/Applications`，killall + open 重启，运行态核验（进程
  代际 + 8777 监听者）；
- **对账数字（产物门）**：关闭 ChatGPT Desktop 运行 N 分钟 → 日志
  `token_refreshed` ≥ N 次而 `ps` 下 runtime 名下僵尸数**恒为 0**；修复前同条件
  为 N 次刷新 ≈ N 个僵尸（§3.3/§3.4 已留存修复前基线）；
- think.md 补复盘条目；CHANGELOG `[Unreleased]` 记录；本文状态行改为已修复并回填
  对账数字。

**P2 完成门（缺席加固，独立验收，不阻塞 P0 闭环）：**

- watchdog 上线后的特征输出核验（**不列入 P0 安装核验项**）；
- 缺席状态机 / 重试预算按 §5.3 各自语义单独验收。

### 5.5 P0 实施完成记录（2026-10-12）

P0 完成门逐项核验（对照 §5.4）：

- **三处收尸修复**：commit `9365da6c`（codex-remote：`loadChatGPTAuth` defer
  stdin.Close→Kill→`cmd.Wait()`，`chatGPTCodexBin`/`openBrowserBin` 测试注入 seam；
  `openBrowser` 改 `Run()`）+ `0c6016ad`（dsh-web：单一 reaper goroutine 独占
  `cmd.Wait()`、退出即清 `s.cmd` 恢复 Start 守卫放行、Stop 信号化复用
  `killProcessGroupNoWait`、删除 `terminateProcessGroup`）。
- **回归测试**：`chatgpt_auth_reap_test.go`（success/not-logged-in/timeout 三路径，
  断言 Wait 契约 = 返回后 pid ESRCH；stub 经 `chatGPTCodexBin` 注入，无 ps 硬断言）、
  `pairing_oauth_reap_test.go`、`starter_reap_test.go`（自发死亡→收尸+守卫恢复、
  Stop→reaper 收尸、守卫仍拒双 spawn）。`go test ./agent/codex-remote/... ./agent/dsh-web/...
  -count=1` 全绿（6.7s / 24.4s）。
- **Release 覆盖安装**：`build-unsigned-release.sh` → 内嵌 runtime
  `-version` = `0.1.0 (commit: 0c6016ad13a6, built: 2026-10-12T11:13:49Z)` →
  killall + pkill runtime → cp 到 `/Applications` → 重启。运行态核验五项全过：新
  runtime pid 95438（lstart 19:15:30 晚于构建）、8777 LISTEN=95438、无
  `/Applications` 之外实例、`runtime.json` pid/bridgeEpoch 更新、旧 runtime 81192
  名下 ~50 僵尸随重启回收（全系统 56→3，余 3 属无关父进程 899/1540）。
- **对账数字（产物门）**：owner 配合退出 ChatGPT Desktop，离线态窗口
  19:42:29–19:53:59（11.5 分钟）：日志 `token_refreshed` 7→60（**53 次刷新**，
  ≈4.6/min，与修复前风暴节奏一致）+ `no desktop environment`×57（restore 失败
  循环全速运行，事故条件完整复现）；runtime 95438 名下僵尸 **7 次采样恒 0**。
  修复前同条件为 53 刷新 ≈ 53 僵尸（§3.3/§3.4 的 1:1 基线）。佐证在线态
  （19:16–19:42 观测）：Desktop 在线时每 ~9.5 分钟一次主动刷新 spawn 探针，僵尸
  恒 0。采样档案：`build/zombie-reconcile-95438-{offline,online}.log`。
- **文档**：think.md 复盘条目、CHANGELOG `[Unreleased]`、本节回填。

## 6. Backend 缺席矩阵：未安装 / 未打开是否同类风险

关键区分：**`exec` 找不到二进制 → `Start()` 直接失败 → 根本没有子进程 → 无僵尸；
只有「spawn 成功后退出且无人 Wait」才产生僵尸**。所以「未安装」大多安全，危险的
是「已安装但不可用（未打开 / 未登录 / 崩溃）」的中间态。

| Backend | 未安装 | 已安装、未打开/未登录 | 子进程收尸现状 |
| --- | --- | --- | --- |
| Claude Code（CLI 型，bridge 拥有生命周期，无「需提前打开的 app」） | `StartSession` supervisor `LookPath` 干净报错（claudecode.go:217），无子进程 | spawn 后快速退出，`session.go:365` Wait goroutine 收尸；usage/诊断探针走 `Output()` | ✔（7.7h 无 claude 僵尸实测） |
| Grok Build（CLI 型，同上） | `grokbuild.go:171 LookPath` 报错，无子进程 | catalog 单例 + 每 turn 子进程均有后台 Wait（catalog_session_list.go:178-185、session.go:278） | ✔（单例存活全程实测） |
| DeepSeek Harness（`dsh-web`，seat 常驻进程由 bridge 冷启动） | `resolver.go:201-202` 干净报错，不 spawn；冷启动只探测不拉起（explicit StartSeat 才 spawn） | 托管 seat 自发死亡 → **1 僵尸滞留 + grace 到期 respawn 被 Start 守卫拒绝（自动恢复被卡）**，直到某条 `Stop()` 收尸清位；非累积型（r1 F-2 勘正，P0-3） | △ Stop / spawn 失败路径有 Wait；自发退出路径缺失 |
| OpenCode Web（纯 HTTP 客户端，不 spawn 本地进程） | URL 未配置 → `not_configured`（readiness.go:46） | 探针失败重试，无进程开销；4096 的 serve 由 Swift 端 OpenCodeManagedServer 管理（terminationHandler，实测 0 僵尸） | ✔ 无本地子进程 |
| Codex Desktop（`codex-remote`，依赖 ChatGPT Desktop app 在线） | **曾配对过**：`os.Stat` 早退（chatgpt_auth.go:47-50），不 spawn、无僵尸，但恢复循环仍无限重试刷日志；**从未配对**：无 store 文件，循环根本不启动（pairing_persist.go:301-304） | **本次事故**：探针 spawn 成功 → Kill 不 Wait → 每轮重试 +1 僵尸；未登录路径（探针能起、auth 非 chatgpt）同样泄 | ✘ P0-1 修复目标 |

矩阵外备注：退役 backend（codex / codex-web）Start/Wait 配对完整；`deprecated/`
目录生产代码禁 import（CI guard），无泄漏路径。

## 7. 对「逻辑建立在已安装且已打开的前提上」的评估

用户判断**部分成立，需修正一处前提**：

1. **成立的部分**：恢复/重试循环把「暂时不可用」（Desktop 没开）当无限重试对象，
   没有缺席状态机、没有重试预算、没有全局收尸兜底与监控——任何「已安装但不可用」
   的 backend 都可能进入这类长循环，循环里再叠加资源泄漏就是本次事故。
2. **需修正的部分**：「未安装」情形大多有干净报错（LookPath/os.Stat 早退，不产生
   子进程、不泄僵尸）——MacBridge 并非完全没处理缺席；真正没处理好的是
   **「已安装但不可用」的中间态**（codex-remote 未打开/未登录、dsh seat 崩溃），
   以及所有 spawn 路径的收尾契约（§5.1 三处）。
3. 用户的因果链基本正确，两处措辞精确化：打满的是**进程表配额**（僵尸占条目）而非
   线程；被泄的是重试链路里 spawn 的**探针子进程**，而非配对逻辑本身的线程。

## 8. 来源清单（P0 来源门）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=bc311b6cb7591edb72bbb931acfc1f7e395714a4
未提交状态=干净（本文档为调查后新增，未提交）
任务预期分支=本仓事故调查，无跨仓 iOS 配对需求
配套仓库路径/分支/提交=不适用（纯 Mac 侧 runtime 行为；iOS 仓未读取源码）
预期产品特性=cordcode-bridge-runtime（drivers=claude,codex-remote,grokbuild,dsh-web,opencode-web）

运行态证据：
- 旧 runtime pid 1917（10:26:25 启动，18:17 kill），/Applications/CordCodeLink.app 内嵌
- 新 runtime pid 81192（18:16:56 启动，A/B 复证用）
- 日志 ~/Library/Application Support/CordCode Link/logs/go-bridge.log（18:0x 窗口）
- 崩溃报告 ~/Library/Logs/DiagnosticReports/（Claude×3、xcodebuild×1，受害者证据）
- ps 采样：18:05:50 / 18:15:50 / 18:18:51 三轮，数字见 §2
```

### 8.1 实施段来源清单（2026-10-12 P0 落地，exec-plan 队列归档 .exec-plan/state/plan-938d6b9865a5.json）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=0c6016ad13a67d0aead5c34b55ff6b02376d2f37（含修复 commit 9365da6c；基线 bc311b6c）
未提交状态=构建时仅未跟踪文档（事故/评审文档、exec-plan 状态）；代码修复已先行提交
构建产物=dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip（runtime commit 0c6016ad13a6）
安装=/Applications/CordCodeLink.app（Release 覆盖安装，2026-10-12 19:15）
对账 runtime=pid 95438（19:15:30 启动，/Applications 正式版内嵌）
对账窗口=离线态 19:42:29–19:53:59（Desktop 退出）+ 在线态 19:16–19:42；采样档案
  build/zombie-reconcile-95438-{offline,online}.log
预期产品特性=已核对：三处收尸修复在产物内（-version 提交身份 + 运行态 53 刷新 0 僵尸）
```
