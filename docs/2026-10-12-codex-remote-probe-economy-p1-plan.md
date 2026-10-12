# codex-remote 恢复循环探针经济性优化方案（P1）

> 状态：v3（r2 通过后纠错修订，2026-10-13）。r2 评审报告
> `docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r2.md` verdict=APPROVED，
> 但元审核 `docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r2-meta.md`
> confirm=false（锚点抽查 5/5 通过；来源清单缺配套 iOS 工作树身份）。本轮（v3）
> 改动范围仅事实修正与状态同步：§2.1 补录配套工作树完整身份（元审核 issues#1）、
> §2.3/§2.4/§7 修正 `ctrlRefreshExec` 声明锚点（r2 F-8 随轮闭合）、S-4 协调对象
> 点名、§6 下一阶段入口同步、新增 §8 更正记录；**设计语义零改动**，r2 已审设计
> 结论全部维持，更正明细（原错误/来源/旧新身份/影响范围）见 §8。
> v2 轮（按 r1 评审报告
> `docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r1.md` 修订）
> 改动范围：F-1 结构性修正（auth 获取与刷新门控解耦，D1/D2/S-2）、
> F-2 调用方回退契约（D3/S-3）、F-4 复用既有 authTokenFn seam（§2.4/S-1）、
> F-6 失败不缓存与日志计数口径（D2/D4/S-1）、F-5 撤销检测延迟登记为接受的
> 故意差异（§1/§3）、F-3 锚点收紧（§2.2/§2.3/§4）、F-7 去除对已销毁样本的
> 硬依赖（E-9/§1/§6）；处置明细见 §7。本文档是事故档案
> `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md` §5.2 P1 的实施方案。
> P0（三处收尸）已于 2026-10-12 实施闭环（commit 9365da6c/0c6016ad，对账 53 刷新/0 僵尸）；
> 本方案处理收尸之后的「纯浪费」：Desktop 缺席时恢复循环每轮全量 spawn auth 探针再丢弃。
> 本方案**只设计不实施**；评审通过 ≠ 实施授权（CLAUDE.md 方案评审循环）。

## 1. 摘要与范围

**现状问题**（P0 后仍存在）：`restoreOnce` 每轮无条件执行
`chatGPTAuth`（spawn 一个 ChatGPT 内嵌 codex `app-server --stdio` 探针）→
`refreshControlToken` → `lookupDesktopEnv` 失败 → backoff 1s→30s 重试。Desktop
已装未开时风暴节奏 ~4.6 轮/分钟 = ~4.6 个探针进程/分钟 + 每轮一次完整的
refresh start/finish HTTP 往返。收尸修复后不再泄漏进程，但探针 spawn 与握手
CPU 是纯浪费（P0 对账窗口实测：11.5 分钟 53 次刷新）。

**目标行为**：镜像官方 codex app-server-transport 的三个语义——(a) ctrl token
刷新按需求分级门控（剩余 >5min 直接跳过）；(b) account auth 结果缓存复用
（官方为登录生命周期绑定的 AuthManager 缓存）；(c) 刷新遭 401/403 先走认证
恢复（重取 auth + 重试一次）再判配对撤销。

**非目标**：

- 不改恢复循环 backoff（`backoff.go` 已镜像官方 `next_reconnect_delay`）。
- 不改 `watchBinding` 的 lead=60s / minRetry=30s（官方 Proactive 窗口 5min
  更宽，我们更保守，不是浪费；失败退避官方 24–36s 随机 vs 我们 30s 固定，
  在其区间内）。
- 不改探针机制本身（仍 spawn 探针进程读 Desktop 登录态；不直读 auth.json——
  是否可直读属于另一个事实缺口，若要换须另案取证）。
- P2（缺席状态机 / 重试预算 / zombie watchdog）不在本次范围。
- 不改 offline 消息映射与配对流程的用户可见语义。
- **接受（r1 F-5，故意差异）**：enrollment 撤销（用户在 chatgpt.com 侧撤销
  控制器）的检测延迟从「每轮 refresh（≤30s backoff）」变为「≤ 下一次
  Proactive/Required 刷新（~300s）+ backoff」——官方同款门控语义
  （NotNeeded 期间官方同样不刷新，`enroll.rs:246-263`）；不为此给 env/WS 401
  增加撤销映射（`ws.go:108-110` 现状保持）。

**需求编号**：

- **R-1** Desktop 缺席时恢复循环不再逐轮 spawn 探针：风暴态探针频率从
  ~4.6/分钟降至「auth 缓存窗口内的 0 + 缓存过期/失败驱动的少量」。
- **R-2** 在线态 ctrl token 主动刷新复用缓存 auth，不逐次 spawn 探针。
- **R-3** 刷新遭 401/403 不再立即撤销配对：先失效缓存→重取 auth→重试一次，
  仍拒绝才 `errPairingRevoked`（消除「缓存 auth 陈旧 → 假撤销 → 强制重新
  配对」这一由本方案引入的回归面，镜像官方 UnauthorizedRecovery）。
- **R-4** 门控/缓存/恢复路径有日志特征输出（对账与运行态验证锚点）。
- **R-5** 配对流程与诊断的显式探针行为不变（用户显式动作取新鲜 auth，不经
  缓存）。

**验收标准**（详见 §6）：定向测试绿；Release 覆盖安装后 Desktop 关闭运行
≥10 分钟：`auth_probe`（真实 spawn，见 D4 口径）≈ 1 次（冷启动）+ 0（auth
缓存 exp 窗口内；对账以该 ≥10 分钟窗口计，不依赖一次性取证样本）、
`token_refreshed` ≈ 每 5 分钟一次（刷新门控 Proactive 窗口）、runtime 名下
僵尸恒 0（P0 回归不破坏）；Desktop 打开后恢复循环在既有 backoff 节奏内成功
收口（无新增卡点；含冷启动时磁盘 ctrl 仍新鲜的场景——auth 每轮获取保证
`state.token` 非空，D1/F-1）。

## 2. 来源与现状调查

### 2.1 来源清单（方案阶段，P0 来源门）

```text
本仓仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
本仓分支=feat/ios-native-message-timeline
本仓提交=ff990f73be81a55f94e9f799c9b8a9fa7d0e1662
本仓未提交状态=产品代码干净（未跟踪新增 4 个 docs 文件：本方案 + r1/r2/r2-meta 评审报告）
上游仓库路径=/Users/jacklee/Projects/codex（官方 checkout，只读取证）
上游分支=main
上游提交=cc7ba33601286a751477832b01de7b6f8de0dd9c（2026-10-10，干净）
任务预期分支=纯 Mac 侧 runtime 行为优化（feat/ios-native-message-timeline worktree，无 iOS 代码改动）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline（同名功能分支族唯一对应；本方案未读 iOS 源码、不修改 iOS 仓——「未引用」不等于「不记录」）
配套分支=feat/ios-native-message-timeline
配套提交=7e32a62163ef66ab855eb71d571b38b1823574da
配套未提交状态=脏（status --porcelain 275 行：273 M + 2 ??；M 集中于 docs/（266）与 .exec-plan/（6）、OpenCodeiOS（1），?? 为 .exec-plan/native-timeline-status-index.json 与 .exec-plan/state/plan-4f2d48b2dd54.json——iOS timeline 任务在途工作，属该任务 agent/owner，本方案不读取、不修改）
预期产品特性=cordcode-bridge-runtime（drivers 不变）
```

（v3 纠错时点复核：本仓提交 ff990f73 与上游 cc7ba336 同 v1/v2 送审时一致，未漂移；
本仓未跟踪文件由 v2 时点的 2 个增至 4 个（新增 r2/r2-meta 评审报告，流程文档非
产品代码）。配套 iOS 工作树身份为本轮按 r2-meta 补录，枚举与核对过程、影响范围
见 §8.1。）

### 2.2 上游官方语义（锚点表，已亲核 @ cc7ba336）

| 语义 | 锚点 | 内容 |
| --- | --- | --- |
| 刷新需求分级 | `codex-rs/app-server-transport/src/transport/remote_control/enroll.rs:26`（`REMOTE_CONTROL_SERVER_TOKEN_REFRESH_SKEW_SECS = 5*60` 常量）、`:246-263`（`server_token_refresh_requirement_at`） | 无 token/已过期→`Required`；剩余 >5min 或失败延迟 `next_refresh_at` 未到→`NotNeeded`；最后 5min→`Proactive`。注：`next_refresh_at` 一侧（`:257-260`）是官方失败退避状态，本方案不镜像该状态（见 D1 有意差异说明） |
| 调用点门控 | `mod.rs:460`、`mod.rs:704` | `if enrollment.should_refresh_server_token()` 才调 refresh——刷新绝不无条件执行 |
| NotNeeded no-op | `server_api.rs:134-151` | `refresh_remote_control_server` 对 `NotNeeded` 直接 `Ok(())` 不发请求；`Required` 但 `next_refresh_at` 未到→`WouldBlock` |
| 失败退避 | `server_api.rs:27-28`（24s/36s 常量）、`:366-387`（`refresh_deferral` + 随机退避） | 瞬态失败→Retry-After 或 24–36s 随机延迟，期间保留有效 token 继续 |
| auth 缓存 | `auth.rs:1-2`（模块注释）、`:29-46`（`capture` 一次）、`:89-96`（`load_remote_control_auth`）、`:98-123`（`load_auth_manager`，至多一次 reload） | 认证凭据绑定一个 remote-control 登录生命周期，**不逐次重取**；重读由变更通知（`watch::Receiver<u64>`，`:59-61`）驱动 |
| 401 恢复 | `auth.rs:177-214`（`recover_remote_control_auth`）、`websocket.rs:1512-1520`（401/403→清 server token→刷新再重连；`:1518` 为错误文案行） | 认证类失败先走恢复步骤，不立即判死 |

### 2.3 本仓现状锚点（@ ff990f73）

| 现状 | 锚点 | 说明 |
| --- | --- | --- |
| 无条件探针+刷新 | `agent/codex-remote/pairing_persist.go:266-279`（restoreOnce：`chatGPTAuth` → `refreshControlToken`，非撤销 refresh 失败仅 Warn「trying saved token」继续）、`:79-93`（runCtrlTokenRefresh） | 每轮先 `chatGPTAuth`（spawn 探针）再 `refreshControlToken` |
| 恢复循环 | `pairing_persist.go:299-330` | store 存在即无限循环 restoreOnce，backoff 1s→30s（镜像 `next_reconnect_delay`，`websocket.rs:1366-1374`） |
| 401/403→撤销 | `agent/codex-remote/pairing.go:401-403`、`:426-428` | refresh start/finish 两个 401/403 位点直接 `errPairingRevoked` |
| env 查找依赖 auth token | `pairing.go:440-449` | `lookupDesktopEnv` 用 `p.state.token`；缺失时报 `pairing state incomplete`（:446-447）；`jsonRequest` 的 status 在 :449 被丢弃——**冷启动必须先取一次 auth**（D1 依据） |
| persist 不含 account token | `pairing_persist.go:95-106`（`persistedPairing` 字段）、`:249-260`（restore 只恢复 ctrl 侧，不写 `state.token`） | 字段仅 client/env/ctrl/key；进程重启后 `state.token` 必空（r1 F-1 / E-11） |
| ctrl token 600s 常量 | `pairing_persist.go:19-23` | E-9 复盘：5 样本恒 600s；lead=60s 已有 |
| 既有测试 seam | `pairing.go:73,133-138`（`authTokenFn` 拦截 `chatGPTAuth`；restore 测试 `pairing_test.go:278-280` 已用）、`pairing.go:83-84`（`ctrlRefreshExec` 声明，v3 按 r2 F-8 修正）、`pairing_persist.go:58-61`（调用点） | 两层 seam 已覆盖 auth 注入与整段刷新序列（r1 F-4） |
| JWT claims 解析 | `chatgpt_auth.go:125-150`（`accountIDFromJWT`）、`jwt.go:8-14`（`decodeJWTSegment`） | 已解析 claims；扩展顶层 `exp` 是小改 |
| Desktop 关闭时 refresh API 可达 | 事故档案 §3.2/§3.3 | `token_refreshed` 成功于 `no desktop environment` 之前——刷新不依赖 Desktop 在线 |

### 2.4 复用调查表

| 能力/需求 | 已有实现与锚点 | 选择 | 必要改动/无法复用的证据 |
| --- | --- | --- | --- |
| 刷新需求分级 | 上游 `enroll.rs:246-263`（Rust，进程内） | 镜像（新建 Go 小函数 `ctrlTokenRequirement`） | 语言/进程边界不能直接 import；镜像判定式与 5min SKEW 常量（`enroll.rs:26`） |
| auth 缓存 | 上游 `auth.rs` AuthManager（文件+watch 通知） | 镜像（PairingController 内存缓存） | 我们无 Desktop 侧变更通知通道；用 JWT `exp` 定界（比官方更保守的有界近似）+ 失败驱动失效 |
| 401 恢复 | 上游 `auth.rs:177-214` 恢复步骤 | 镜像（`refreshControlTokenWithRecovery` 包装） | 官方恢复为多步骤迭代器；我们单步（re-probe+重试一次）即覆盖「auth 陈旧」主因 |
| 探针执行与收尸 | 本仓 `chatgpt_auth.go:52-123`（P0 已修收尸） | 复用 | 无改动；仅调用频率变化 |
| backoff | `backoff.go` | 复用 | 无改动 |
| 测试注入 | `authTokenFn`（`pairing.go:73,133-138`；`pairing_test.go:278-280` restore 测试已用）+ `ctrlRefreshExec`（声明 `pairing.go:83-84`、调用点 `pairing_persist.go:58-61`，v3 按 r2 F-8 修正） | 复用（r1 F-4） | **不新增** `chatGPTAuthExec` 平行 seam：`chatGPTAuthCached` miss 路径调用既有 `chatGPTAuth`（→ `authTokenFn` → `loadChatGPTAuth`），hit/miss 用 `authTokenFn` 调用计数断言；`ctrlRefreshExec` 保持只包 auth→refresh→persist 整段序列 |

## 3. 设计与决定

区分：已确认事实（§2 锚点）、拟议设计（本节）、待验证假设（无阻塞项——E-9
为一次性取证，样本已销毁不可复核，见 §4 注；设计含无 `exp` 防御分支，不依赖
该样本数值）。

**D1 ctrl token 刷新需求分级（只门控刷新，不门控 auth 获取）**

新增 `ctrlTokenRequirement(ctrlExp string, now time.Time) requirement`，三级
镜像 `enroll.rs:246-263`：无/过期→`Required`；剩余 > `ctrlTokenSkewSecs`
（300，镜像 `REMOTE_CONTROL_SERVER_TOKEN_REFRESH_SKEW_SECS`，
`enroll.rs:26`）→`NotNeeded`；最后 300s→`Proactive`。有意差异：官方
`NotNeeded` 还含「失败延迟未到」一侧（`next_refresh_at`，`enroll.rs:257-260`），
本方案不引入该状态——恢复循环自身 backoff（1s→30s）与
`maybeRefreshCtrlToken` 的 minRetry=30s 已承担同职责（失败后限频重试），
门控只取 `exp` 一侧。

- `restoreOnce`（`pairing_persist.go:266-279` 改造，r1 F-1 结构）：**每轮**
  先 `chatGPTAuthCached(ctx)` 填 `state.token`（冷启动 cache miss = 1 次探针
  并入缓存；其后命中 0 spawn）——**auth 获取不受 ctrl 门控**；随后仅当
  `ctrlTokenRequirement != NotNeeded` 才执行 `refreshControlTokenWithRecovery`
  （D3），`NotNeeded` 直接进 `lookupDesktopEnv`。依据（E-7/E-11）：persist
  schema 不含 account JWT，冷启动 `state.token` 为空，`lookupDesktopEnv`
  报 `pairing state incomplete`——若 NotNeeded 连 auth 一起跳过，覆盖安装等
  进程重启后、磁盘 ctrl 仍新鲜的最多 ~300s 内（一个 ctrl 生命周期 600s 减
  SKEW 300s）即使 Desktop 打开也无法 restore。
- `maybeRefreshCtrlToken`（在线 2s tick）沿用 lead=60s 判定（不改），其执行体
  `runCtrlTokenRefresh` 改为 `chatGPTAuthCached` + `refreshControlTokenWithRecovery`。
- 显式流程不经缓存（R-5）：`StartRemoteControl`/诊断直连 `loadChatGPTAuth`
  （`diagnostics.go:55`）；配对 `Start` 收显式 token 参数（`pairing.go:228-233`），
  `SubmitCode` 沿用该显式 token（state 内传递，不经缓存获取），均不读不写
  authCache。

**D2 auth 结果缓存（PairingController 内存态，p.mu 保护）**

新增字段：`authCache{token, accountID, expAt time.Time, hasExp bool}`。取 auth
统一走 `chatGPTAuthCached(ctx)`；miss 时调用**既有** `chatGPTAuth`（r1 F-4，
经 `authTokenFn` seam 可注入）：

- 命中（`hasExp && now < expAt-60s`）→ 返回缓存并写回
  `state.token`/`state.accountID`（p.mu 下），**不 spawn**；记 Debug
  `auth_probe cache=hit`（不计数，避免风暴刷屏——每轮 restore 失败已有 Warn）。
- miss（冷启动/过期/被失效）→ 调 `chatGPTAuth`（→ `authTokenFn` →
  `loadChatGPTAuth`）：**成功**才解析 `exp`（`accountIDFromJWT` 同源 payload
  扩展，`chatgpt_auth.go:125-150`；`exp` 为 JWT 顶层 claim，不在
  `https://api.openai.com/auth` 命名空间下）并写缓存与 `state.token`；
  **失败不写缓存**（r1 F-6：`loadChatGPTAuth` 的失败路径——Stat 早退
  「请先安装并登录 ChatGPT Desktop」（:54-55）、「ChatGPT 未登录」（:112-113）、
  「读取 ChatGPT 登录态超时」（:122）——均无 token；把失败占坑当 hit 会让
  R-3 恢复跳过真正重探，也会让横幅失真）——失败照原样返回错误，下一轮再
  miss 再探（未安装/未登录场景与现状逐轮探测一致，横幅语义不变；目标场景
  「已装未开」探针成功、缓存生效）。
- 失效来源（三条）：(1) 显式 `invalidateAuthCache()`——D3 恢复路径与
  `invalidateRevokedPairing` 调用（撤销即清缓存：auth 生命周期绑定配对身份，
  镜像 `auth.rs:1-2`）；(2) `lookupDesktopEnv` 的 `jsonRequest` 返回 401/403
  （把 `pairing.go:449` 丢弃的 status 接住）——陈旧 auth 在 env 查找上的
  表现也必须能触发重取，否则缓存窗口内会盲目重试；(3) `exp` 自然到期
  （命中判定失败即 miss）。`exp` 缺失 → `hasExp=false`，仅失败驱动（镜像
  官方无 TTL 语义；防御分支，单测覆盖）。**运行时只信当场解析的 `exp`，
  禁止硬编码 TTL 常量**（r1 F-7；E-9 的 ~96h 是一次性观察量级，不是常量）。
- 恢复循环与 watchBinding 生命周期互斥（`watchBinding` 仅在 restore 成功后
  启动，`pairing_persist.go:329`），`reconnectFromStore` 与循环可并发但
  `restoreMu` 已串行化 restoreOnce；缓存读写均在 `p.mu` 下。最坏并发重复
  探针一次，P0 已保证收尸，可接受。

**D3 刷新认证拒绝先恢复后撤销（含 r1 F-2 调用方契约）**

`refreshControlToken` 的两个 401/403 位点（`pairing.go:401-403`、`:426-428`）
改返回可识别错误 `errCtrlAuthRejected`（携带位点）。新增
`refreshControlTokenWithRecovery(ctx)`：

1. 调 `refreshControlToken`；若非 `errCtrlAuthRejected` 原样返回。
2. `invalidateAuthCache()` → `chatGPTAuthCached`（必然 miss→探针一次）→
   用新 auth 重试 `refreshControlToken` 一次。
3. 仍 `errCtrlAuthRejected` → 返回 `errPairingRevoked`（语义与现状一致）。

调用方契约（r1 F-2：现状回退不得被「透传」删掉）：

- `restoreOnce`：`errPairingRevoked` → 返回（终止，触发撤销清理）；**其余
  刷新错误（5xx/网络等非认证错误）保持现状回退**——Warn
  「trying saved token」+ 用已保存 ctrl token 继续 `lookupDesktopEnv`
  （`pairing_persist.go:274-279` 现行为；磁盘 ctrl 未过期 + Desktop 在线 +
  refresh 端点瞬态故障时仍能绑定，断线韧性不回归）。auth 探针失败仍照原样
  返回（现行为：`pairing_persist.go:266-269`，offline 横幅分类依赖它）。
- `runCtrlTokenRefresh`（在线路径）：错误原样返回给 `maybeRefreshCtrlToken`，
  其既有处理（`errPairingRevoked`→立即失效；其余→Warn 保持连接）不变。

镜像 `auth.rs:177-214`（恢复优先）与 `websocket.rs:1512-1520`（401→清
token→刷新再重连）；现状「每轮新鲜 auth」对假撤销的防护由本条接管。

**D4 可观测性（R-4，运行态特征输出）**

- Info `auth_probe` 行在**真实 spawn 处**发出（`loadChatGPTAuth` 通过 Stat
  检查后；r1 F-6 口径）——它是精确的探针进程计数器，与调用方无关；Stat
  早退（未安装）与探针失败不升 Info，缓存命中 Debug `auth_probe cache=hit`
  不计数。
- `restoreOnce` 门控跳过刷新时，在既有 `restore waiting` Warn 上加字段
  `ctrlFresh=true`（不加新行，避免风暴刷屏）。
- 刷新执行时既有 `token_refreshed`/`ctrl_token_applied` 不变——对账三计数
  （auth_probe / token_refreshed / 僵尸）即可完整刻画行为。

**OD 表：无产品语义决定。** 全部为工程取舍且有上游锚点（SKEW=300s、exp
定界、恢复优先）；不制造 owner 裁决。r1 F-5 点名的用户可见差异已在 §1 非目标
显式登记为**接受的故意差异**（enrollment 撤销检测延迟 ≤ Proactive 窗口
~300s + backoff，官方同款门控语义）；除该条外无用户可见行为面变化。

## 4. 证据与实施前置门

| ID | 命题 | 证据类别/来源 | 断言与负例 | 状态 | 未满足时阻塞 |
| --- | --- | --- | --- | --- | --- |
| E-1 | 官方刷新分级：SKEW=5min、三级判定 | 源码 @ codex cc7ba336 `enroll.rs:26,246-263` | `expires_at > now+300s → NotNeeded`；负例：过期/无 token → Required | verified | D1 |
| E-2 | 官方 NotNeeded no-op、Required-延迟 WouldBlock | 同上 `server_api.rs:134-151` | NotNeeded→Ok(()) 不发请求 | verified | D1 |
| E-3 | 官方 auth 缓存生命周期绑定、非逐次 | 同上 `auth.rs:1-2,29-46,89-96,98-123` | capture 一次；load 读缓存 ≤1 reload（reload 循环在 `load_auth_manager`） | verified | D2 |
| E-4 | 官方 401 先恢复 | 同上 `auth.rs:177-214`、`websocket.rs:1512-1520` | 恢复步骤在判死之前 | verified | D3 |
| E-5 | 本仓 restoreOnce 无条件探针+刷新 | 源码 @ 本仓 ff990f73 `pairing_persist.go:266-279` | 每轮两步均执行 | verified | 全部切片（问题存在性） |
| E-6 | 本仓 401/403 直接撤销 | `pairing.go:401-403,426-428` | 两位点返回 errPairingRevoked | verified | D3 |
| E-7 | env 查找依赖 state.token，缺失报 incomplete | `pairing.go:440-449` | 冷启动必须先取 auth 一次 | verified | D1 冷启动设计 |
| E-8 | ctrl token 600s 常量、lead=60s 既有 | `pairing_persist.go:19-23`（E-9 复盘，5 样本） | 常量事实 | verified | D1 窗口推导 |
| E-9 | 真实 authToken JWT 带 `exp`，量级 ~96h | 原始运行证据：2026-10-12 20:5x 本机真实探针一次（Desktop 在线态，与生产同路径 `loadChatGPTAuth`）；claims 键含 `scp/sl/sub/exp/jti/aud/…/iat/nbf`，exp 距今 **96h56m**；token 值未落盘，临时取证文件已删除。**一次性取证、样本已销毁、不可复核（r1 F-7）：仅证明 `exp` 存在与量级，禁止当硬 TTL 或常量** | `exp` 存在且为未来时刻；负例：`exp` 缺失 → `hasExp=false` 失败驱动（防御分支，单测覆盖） | verified | D2 缓存定界（仅证 exp 存在） |
| E-10 | Desktop 关闭时 refresh API 可达 | 原始运行证据：事故档案 §3.2/§3.3（token_refreshed 成功于 no desktop environment 之前）+ P0 对账 53 次 | 刷新不依赖 Desktop 在线 | verified | D1 门控下的风暴行为推导 |
| E-11 | 持久化 pairing 不含 account JWT/accountID，冷启动 `state.token` 必空 | 源码 @ 本仓 ff990f73 `pairing_persist.go:95-106`（`persistedPairing` 字段集合）、`:249-260`（restore 只恢复 ctrl 侧） | 字段仅 client/env/ctrl/key；restoreOnce 不写 `state.token`（r1 F-1 亲核） | verified | D1 冷启动（auth 每轮获取） |

Gate A（证据）：E-1..E-11 全部 verified，无 pending（E-9 为一次性取证，见注）。
Gate B（产品决定）：无 OD（F-5 差异已登记为接受项）。
**两个门均满足，实施前置无阻塞**；实施完成门见 §5/§6。

## 5. 实施切片

| 切片 | 可见结果 | 组件与复用 | 依赖/前置 | 最小验证/完成证据 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-1 auth 缓存 | `chatGPTAuthCached` 命中不 spawn；miss 走既有 `chatGPTAuth` 探针，**仅成功**写缓存；`auth_probe` spawn 计数日志 | `chatgpt_auth.go`（复用 P0 修复版探针 + spawn 点 Info 行）、`chatgpt_auth.go:125-150`/`jwt.go` 扩展顶层 `exp`、`pairing_persist.go` 新增 `authCache` 字段（复用 `authTokenFn` seam，r1 F-4；不新增平行 seam） | E-3/E-9/E-11 | 单测：命中 0 探针（`authTokenFn` 不被调）/miss 1 探针（调用计数=1）/exp 过期重取/无 exp 失败驱动/**探针失败不写缓存（F-6）**/并发读写（-race） | 缓存实现缺陷 → 回退为直连探针（行为=现状，仅浪费回归） |
| S-2 restoreOnce 刷新门控（auth 每轮获取，仅刷新受门控） | Desktop 关闭风暴：探针 ≈0（缓存内）、刷新 ≈每 5min 一次；冷启动磁盘 ctrl 仍新鲜也能 restore | `ctrlTokenRequirement`（镜像 E-1/E-2）、`restoreOnce` 改造（r1 F-1 结构）、`lookupDesktopEnv` 接 401/403 失效缓存 | S-1；E-1/E-2/E-7/E-10/E-11 | 单测四态（r1 F-1）：冷启动+ctrl fresh → **1 探针（miss）** 0 刷新且 env 查找仍执行；缓存已填+ctrl fresh → **0 探针** 0 刷新且 env 查找仍执行；Proactive/Required → 刷新；env 401 → 缓存失效、下轮 miss 重探 | 门控判定错误 → 最坏回到每轮刷新（浪费回归，非正确性回归）；auth 每轮获取保证 env 查找时 `state.token` 恒非空（与现状一致，杜绝 F-1 空窗型 incomplete） |
| S-3 401 恢复优先 + 调用方回退契约 | auth 陈旧不再假撤销；真撤销仍撤销；非认证刷新失败不升级为整轮失败 | `refreshControlToken` 两位点改 `errCtrlAuthRejected`；`refreshControlTokenWithRecovery` 包装；`restoreOnce`/`runCtrlTokenRefresh` 按调用方契约接入（r1 F-2） | S-1；E-4/E-6 | 单测：401→失效缓存→重试成功（不撤销）；**401×2→errPairingRevoked**（既有 `TestRestoreRevokedInvalidatesEveryIdentityLayer` 语义保持：恢复重试后仍拒才撤销）；403 同；**refresh 5xx/网络错 → 不撤销、Warn 试用已保存 ctrl token、env 查找仍执行（F-2）**；auth 探针失败原样返回 | 恢复引入新错误路径 → 包装层仅作用于 `errCtrlAuthRejected`，其余交给调用方契约处置（不变式：非认证失败行为与现状完全一致） |
| S-4 部署与运行态对账 | 正式版含 P1；行为特征输出可对账 | Release 构建→覆盖安装→重启（**与 iOS 测试窗口协调，重启时机由 owner 拍板**——另一 agent 在配套工作树 `cordcode-ios-native-message-timeline`（身份见 §2.1/§8.1）做 iOS timeline 真机测试） | S-1/S-2/S-3 定向测试绿；`go test ./agent/codex-remote/... -count=1` 全绿 | 运行态五项核验（进程代际/8777/无临时产物/-version 提交身份/runtime.json）+ 对账（§6） | 构建或安装失败 → 保留失败现场，不宣称部署 |
| S-5 文档收口 | 事故档案 P1 状态回填、think.md、完成报告 | D0 级 | S-4 对账通过 | git diff --check；状态行与对账数字一致 | — |

注：CHANGELOG 是否记条目——owner 已于 2026-10-12 将 P0 同类条目从 CHANGELOG
移除（内部运维修复不进面向使用者的 CHANGELOG，见 commit 7d147cdc/ff990f73
往来）；P1 默认**不记**，owner 另有指示除外。

## 6. 验证、风险与交付

**R→S→验收映射**：

- R-1（风暴探针↓）→ S-1+S-2 → 对账：Desktop 关闭 ≥10 分钟，`auth_probe`
  （真实 spawn）≈1（冷启动）+0（缓存 exp 窗口内；对账数字以该 10 分钟窗口
  计，不依赖已销毁的一次性样本）；修复前同条件 ~46 次（4.6/min×10）。
- R-2（在线刷新复用缓存）→ S-1 → 单测 + 在线态观测：`token_refreshed`
  仍 ~每 9min（lead=60s 不变）而 `auth_probe` 不随刷新递增。在线
  （~9min=600s−lead60s）与缺席（Proactive ~5min）两条刷新节奏**分开对账**，
  勿混。
- R-3（恢复优先）→ S-3 → 单测四态（恢复成功/二次拒绝→撤销/非认证失败走
  saved-token 回退/探针失败原样返回）。
- R-4（可观测）→ D4 字段存在且对账可用（Info 仅计真实 spawn）。
- R-5（配对/诊断不变）→ 单测断言显式路径不经缓存：`diagnostics.go:55` 直连
  `loadChatGPTAuth`；`Start` 收显式 token 参数、`SubmitCode` 沿用之。

**对账门（产物门）**：Desktop 关闭 N≥10 分钟 → 三计数：`auth_probe`（真实
spawn）≈1、`token_refreshed` ≈ N/5（缺席 Proactive 节奏）、runtime 名下僵尸
恒 0（P0 回归）。修复前基线：`token_refreshed` ≈ 4.6N、每刷新一探针。Desktop
重开后恢复循环在既有 backoff 内成功（`phase=restored`）；**含冷启动磁盘 ctrl
仍新鲜的场景**（auth 每轮获取，`state.token` 非空，env 查找照跑）。

**方案阶段已验证**：上游语义锚点（E-1~E-4 亲核）、本仓现状（E-5~E-8/E-11）、
exp 真值（E-9 一次性真实探针取证，样本已销毁）。**需实施后验证**：全部单测、
运行态对账。

**风险**：

| 风险 | 触发信号 | 应对/回退 |
| --- | --- | --- |
| 缓存 auth 陈旧导致假撤销 | 刷新 401（Desktop 侧登出/换号） | D3 恢复路径接管（镜像官方）；单测锁死 |
| exp 缺失的 token 形态 | 探针返回无 exp JWT | `hasExp=false` 失败驱动（E-9 负例分支）；不阻塞 |
| 门控窗口内 Desktop 打开 | env 查找突然可命中 | 门控只跳过刷新，auth（缓存）每轮获取、env 查找每轮照跑 → 立即 restored，无新增卡点（r1 F-1 修正后成立） |
| enrollment 撤销检测延迟（r1 F-5） | 用户在 chatgpt.com 撤销控制器 | 接受为故意差异：≤ Proactive 窗口 ~300s + backoff（官方同款门控语义）；已登记 §1 非目标，不扩大 S-3 |
| 重启时机与 iOS 测试冲突 | 另一 agent 真机测试中 | S-4 前置：重启时机 owner 拍板 |
| 缓存并发竞态 | -race 报警 | 字段全在 p.mu 下；单测含 -race |

**回退边界**：三切片均为行为门控/包装层，不改持久化 schema、不改协议；
回退 = revert commit（无数据迁移、无不可逆动作）。

**阻塞清单**：无（方案级）。下一阶段入口：r2 评审已通过（APPROVED，唯一建议
F-8 已随 v3 闭合）+ 元审核缺项已随 v3 补录；owner 授权 → exec-plan 实施
（S-1→S-5）。

## 7. r1 评审处置表（v1 → v2）

| 意见 | 处置 | 核实证据（本轮亲核） | 改动位置 | 遗留门 |
| --- | --- | --- | --- | --- |
| F-1 [阻塞] NotNeeded 跳过 chatGPTAuth 掏空冷启动 env token | 采纳 | `persistedPairing`（pairing_persist.go:95-106）无 token/accountID；restoreOnce 只恢复 ctrl 侧（:249-260）；`lookupDesktopEnv`（pairing.go:446-447）token 空报 incomplete；ctrl 600s − SKEW 300s → 重启后最多 ~300s 卡 incomplete | §3 D1（auth 每轮获取、仅刷新受门控）、D2（写回 state.token）、§4 E-11、S-2 四态、§1 验收、§6 对账与风险表 | 无 |
| F-2 [阻塞] 非撤销 refresh 失败的 saved-token 回退未入控制流 | 采纳 | pairing_persist.go:274-279：非 `errPairingRevoked` 的 refresh 错误仅 Warn「trying saved token」后继续 env 查找；在线路径 `maybeRefreshCtrlToken` 另有 Warn-保持连接处理（:62-70） | §3 D3 调用方契约（restoreOnce 回退保留 / 在线路径不变）、S-3 增加 5xx 态 | 无 |
| F-3 [建议] 锚点行号不贴目标符号 | 采纳 | `enroll.rs:26`（常量；:22-28 含无关项）；`auth.rs:98-123`（reload 循环）；`websocket.rs:1512-1520`（分支；:1518 文案行）；`chatgpt_auth.go:125-150`（accountIDFromJWT）；`server_api.rs:27-28`（24/36s） | §2.2、§2.3、§4 E-1/E-3/E-4 行 | 无 |
| F-4 [建议] 不平行新增 chatGPTAuthExec，复用 authTokenFn | 采纳 | `authTokenFn`（pairing.go:73,133-138）已拦截 `chatGPTAuth`；restore 测试已用（pairing_test.go:278-280）；`ctrlRefreshExec`（声明 pairing.go:83-84、调用点 pairing_persist.go:58-61；v3 按 r2 F-8 修正声明锚点）包整段序列 | §2.3 seam 行、§2.4 测试注入行、§3 D2、S-1 | 无 |
| F-5 [建议] 撤销检测延迟未登记 | 采纳（登记为接受的故意差异，不加 401 映射） | `dialControllerWS`（ws.go:108-110）握手失败不解析 401；官方门控 NotNeeded 期间同样不刷新（enroll.rs:241-263）——延迟 ≤ Proactive 窗口是官方同款代价 | §1 非目标末条、§3 OD 段、§6 风险表 | 无 |
| F-6 [建议] 只在探针成功时写缓存 | 采纳 | `loadChatGPTAuth` 失败路径（chatgpt_auth.go:54-55,112-113,122）均无 token；Stat 早退发生在 spawn 之前 | §3 D2（失败不写缓存）、D4（Info 仅真实 spawn）、S-1/S-2 测试 | 无 |
| F-7 [建议] E-9 样本已删，96h 不得当硬 TTL | 采纳 | 方案自述 token 未落盘、临时文件已删，不可复核；设计已有 `hasExp=false` 防御分支，不依赖 96h | §4 E-9 注、§3 D2（禁止硬编码 TTL）、§1 验收、§6 R-1（对账以 10 分钟窗口计） | E-9 无残留样本（不阻塞；运行态只信当场 exp） |

不采纳项：无（七条意见全部核实成立）。

## 8. 更正记录（r2 通过后纠错，v3，2026-10-13）

r2 评审 verdict=APPROVED 后，元审核
`docs/2026-10-12-codex-remote-probe-economy-p1-plan-review-r2-meta.md`
（confirm=false；其定向复核范围＝锚点抽查＋来源清单完整性，锚点抽查 5/5 通过）
发现来源清单缺项。按通过后纠错流程修订本方案：v3 只做下表更正与状态行同步，
**设计语义（判定式、切片、测试与对账口径）零改动**，r2 已审设计结论全部维持。
版本链：v1 SHA-256
`c5690bf39cf6bb53673f7788c5b1f0e01d8f378ed7ca4fcc17641d4a13269048` → v2
`81b94d991141a47c464201e29868a3daa3a6f1334f85037c5fcd0a07830ccb23`（两者见
r2 报告记录）→ v3（本文档不自嵌自身哈希，以派发/评审记录为准）。

### 8.1 配套 iOS 工作树身份补录（元审核 issues#1，必须项）

| 项 | 内容 |
| --- | --- |
| 原错误 | §2.1 把配套仓写成「配套仓库路径/分支/提交=不适用（iOS 仓未读取源码）」——无路径、无分支、无提交、无未提交状态。违反项目 P0 来源门（Mac/iOS 双仓 CLAUDE.md：须先枚举工作树再选定同功能分支族，配套工作树无论是否实际引用都记录完整身份）。r2 评审报告「来源与覆盖」节同源同错（评审员清单沿用方案的「不适用」口径），由 r2-meta issues#1 点名 |
| 来源 | r2-meta 元审核报告 issues#1（confirm=false 的唯一缺项）；其依据为项目 P0 来源门与本次复核范围「含配套工作树，无论是否实际引用」。元审核另指出：本方案 S-4 把重启窗口绑在「另一 agent 的 iOS timeline 真机测试」上，配套身份并非与本任务无关；「未读 iOS 源码」应记「本轮未引用」，不能省略身份 |
| 旧身份 | `配套仓库路径/分支/提交=不适用（iOS 仓未读取源码）` |
| 新身份 | 配套仓库路径=`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`；配套分支=`feat/ios-native-message-timeline`；配套提交=`7e32a62163ef66ab855eb71d571b38b1823574da`；配套未提交状态=脏（porcelain 275 行：273 M——docs/ 266、.exec-plan/ 6、OpenCodeiOS 1；2 ?? 为 `.exec-plan/native-timeline-status-index.json` 与 `.exec-plan/state/plan-4f2d48b2dd54.json`；系 iOS timeline 任务在途工作，属该任务 agent/owner，本方案不读取、不修改）。已写入 §2.1，S-4 协调对象同步点名 |
| 影响范围 | 仅来源清单完整性/流程合规，不影响 r2 已审设计结论：本方案无 iOS 代码改动、未读 iOS 源码，§1–§7 全部锚点与证据（E-1..E-11）均不依赖该工作树内容。受影响的具体承诺：(a) S-4 重启窗口的协调对象自此有可指名身份；(b) 实施阶段若需读 iOS 源码或构建 iOS，必须先重新生成来源清单并对齐**当时**的配套工作树身份（若漂移以当时状态为准并记录，不得沿用本条） |

补录身份亲核过程（v3 修订时点独立重做，不转引 r2-meta 结论）：iOS 仓
`git worktree list` 枚举 7 项，`feat/ios-native-message-timeline` 分支唯一对应
`cordcode-ios-native-message-timeline`（其余为 main checkout、
`codex/chatgpt-codex-timeline-parity`、`codex/ios-widget-support` 与 3 个
detached——其中 2 个 detached 位于该树 `output/` 下的实现暂存树，不属功能
分支族）；`rev-parse` 确认分支与提交；`status --porcelain` 计 275 行。本仓与
上游复核未漂移（ff990f73 / cc7ba336，见 §2.1 注）。

### 8.2 `ctrlRefreshExec` 声明锚点修正（r2 F-8 建议随轮闭合）

| 项 | 内容 |
| --- | --- |
| 原错误 | §2.3 seam 行、§2.4 测试注入行、§7 F-4 处置行把 `ctrlRefreshExec` **声明**锚点写成 `pairing_persist.go:83-84`（该处实为 `p.mu.Lock()` / `p.state.token = token`）；正确声明在 `pairing.go:83-84`（`lastCtrlRefreshAttempt` / `ctrlRefreshExec` 字段）。系 v2 F-4 修订引入的误指（r2 定性：F-3 同类残余、新漏检而非新证据推翻） |
| 来源 | r2 评审报告 F-8（建议级，不阻断；闭合标准＝表内声明行与 `pairing.go` struct 字段一致）。v3 修订前本轮亲核 `pairing.go:80-84`（声明）、`pairing_persist.go:58-61`（调用点）、`pairing_persist.go:83-84`（锁行）三处确认 |
| 旧/新锚点 | 旧：声明=`pairing_persist.go:83-84`；新：声明=`pairing.go:83-84`，调用点=`pairing_persist.go:58-61`（原本已写对，保持不变）。已同步改 §2.3、§2.4、§7 F-4 三处 |
| 影响范围 | 仅锚点定位，不改「复用既有 seam、不平行新增 `chatGPTAuthExec`」的设计与测试方案；消除实施期按错误行号找声明落到无关锁行的隐患。r2 闭合标准已满足 |

### 8.3 状态行同步（非语义）

文档头状态（v2 送审 → v3 纠错修订）与 §6「下一阶段入口」（待 r2 评审 → r2 已
通过＋元审核缺项已补录，待 owner 授权）随本轮同步；不涉及任何设计内容变更。
