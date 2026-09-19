# 2026-09-19 bridge runtime「内存 2G+」诊断与修复（评审稿 v4，待复审）

> 状态：**修订 v4，待复审**。v1（`685646d`）经 r1 评审（`0469d9c`）不通过（5 阻断项）；
> v2（`bc328df`）经 r2 复审（`743bde8`）仍不通过（4 阻断项 + §4 404/410 追认 + §5 非阻断）；
> v3（`f1ed656`）经 r3 复审（`d4fa165911d5bd820977cdc18262c6df48a7d409`）仍不通过（4 个新
> 阻断项）。本版逐项处置，映射见 §9。**本轮业务代码有变化（`f45ac93`），已按 r3 准入
> 条件 5 重新 Release 构建、覆盖安装并验证新代际——部署证据在部署完成后写入本稿
> （R3-B1 处置），见 §5。**

## 1. 来源清单（P0，v4 补全）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
诊断时源码 HEAD=bd4a2ce（工作树干净；诊断对象为 87b32a1d 构建的生产二进制，见下）
修复 1–3 提交=f7cd91d（曾部署 /Applications，已被后续代际替换）
v1 评审稿提交=685646d；r1 评审报告提交=0469d9c
v2 修订提交=bc328dfd41099fb5369c4d905394a9d90f7896a6；r2 复审报告提交=743bde8e82439aa94e7175390756584fece1efbd
v3 代码修订提交=ca4e56d8a34b890990cad7c41e2051fe4260ec4e；v3 评审稿提交=f1ed656af43e9e52c2691aa9a882ae8e3f893a49
r3 复审报告提交=d4fa165911d5bd820977cdc18262c6df48a7d409
v4 代码修订提交=f45ac93f1f48e28e2b8639fa38d55e608b21264d（DeleteDevice/Unregister 回滚 + 顺序、enrollment identity 流式化、失败路径测试、think.md/CHANGELOG v4 状态）
未提交状态=本稿为 docs-only 提交；代码工作树在 f45ac93 后无其他未提交修改
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 6e6080eff677c9d8dda19ff1bc6f898b7e8880d7（r3 报告 §1 记录的当前状态；本任务无 iOS 代码声明或改动）
预期产品特性=512MiB runtime-managed 软限额；无订阅 watcher 门控；enrollment 不回放且保留进行中 turn（流式回溯，O(1) 条目内存）；404/410 事务性清理；设备撤销联动删除失败回滚 + 响应暴露；有界 continuity warm cache 与同指纹并发合并；一致内存遥测
```

诊断时运行中的生产二进制身份（`-version` 核对）：

```text
/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime
commit: 87b32a1d9556（2026-09-17T15:36:17Z 构建，go1.26.6）
```

`87b32a1d` 是本分支祖先；诊断时 `git diff 87b32a1d..bd4a2ce -- go-bridge core agent` 为空。
Go runtime 语义核对使用本机目标版本源码
`~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.6.darwin-arm64/src/runtime/`。

## 2. 问题现象

- owner 观察到 `cordcode-bridge-runtime` 活动监视器内存 **2G 多**。
- 实测（旧进程 PID 84472，2026-09-18 18:41 启动，运行 26h50m）：physical
  footprint **2.5G**（峰值 **3.8G**），累计 153 CPU 分钟（均值 ~9.4% 单核）。
- 系统级背景：整机 swap 已用 **14.7G/15G**。

## 3. 诊断证据链（v4 修正 §3.1 措辞）

### 3.1 原始观测（vmmap / 日志 / 采样，非源码推断）——等级：可靠

| 观测 | 数值 |
| --- | --- |
| Physical footprint | 2.5G（1 分钟内平稳 2.4–2.5G）；峰值 3.8G |
| RESIDENT / DIRTY | ~200M / ~59M |
| vmmap 报告的 SWAPPED/DIRTY 页 | 2.4G，其中 9 个 128MB Go 堆 arena 整块换出、0K 常驻（完全冰冷） |
| goroutine 数 | 264（正常） |
| 27h 内 hydrate 的 session | 仅 7 个（19 次 commit）；本进程只写了 2.4MB checkpoint |
| CPU 采样主帧 | `scanClaudeSessionMetadata → InspectTranscriptContinuity → json.Unmarshal`；次帧 `claudeWebPushWatcher.sweep/run` |
| 轮询量（27h） | claude 1615、codex-remote 1385 ok + 229 超时（p50 9.5s）、grokbuild 5283、opencode-web 1705、dsh-web 1611 |
| iOS 连接 | connectionGeneration=91（27h 内 91 次重连） |
| web-push | 3 个 Apple 端点持续 410 Gone（日志 84 次观测，跨 01:04→22:04） |
| 磁盘数据量 | `~/.claude/projects` 640MB / 619 个 jsonl；`~/.grok/sessions` 900MB |

> vmmap 证明的是 swapped/dirty **形状**（整块 arena 换出且冰冷），不证明「已归还页被
> compressor 扣住」的机制——后者只保留在 §3.4 候选解释中（R3-B4 处置）。

### 3.2 Go runtime 内部读取（LLDB）——v1 的 ring 单槽读法不成立，相关断言已撤回

**v1 错误（r1 评审 B2）**：把 `memstats.heapStats` 的单个 delta 槽当成一致快照，
得出「committed≈49.5MB / scavenger 累计归还 3.86GB」。Go 1.26.6
`runtime/mstats.go:667-745` 明确：槽类型是 `heapStatsDelta`，reader 必须旋转三代
并 merge；且 `HeapReleased`（mstats.go:168-172）是「已归还且尚未重新获取」的
**当前量**，不是累计释放量。**上述两个数值与「scavenger 已归还 3.86GB」的断言
全部撤回。**

仍然成立的是 **gcController 的普通原子字段**（非 ring，无聚合问题；校验位全过：
`gcPercent=100`、`memoryLimit=MaxInt64`、`heapMinimum=4MB`、`triggered=^uint64(0)`）：

| 字段 | 旧进程值 | 判读 |
| --- | --- | --- |
| `heapLive` | 20,774,576（~20MB） | 活堆 ~20MB——足以反驳「存在 2.5GB 可达 Go heap 对象」 |
| `heapMarked` | 18,160,712 | 上次 GC 存活 ~17MB |
| `gcPercentHeapGoal` | 36,825,354 | 下次 GC 目标 ~35MB |

**正确方法的一致读取（f7cd91d 部署代际进程 PID 91090，单次 attach 读全三代 + gen，
按聚合语义求和）**：

| 项 | 值 |
| --- | --- |
| gen / 槽 1、2 | 0 / 全零（无 reader 旋转过，槽 0 即聚合值） |
| committed（聚合） | 22,241,280（~21.2MB） |
| released（聚合，当前量语义） | 65,839,104（~62.8MB） |
| `gcController.memoryLimit` | **536,870,912** ——512MiB 默认已生效（部署验证） |
| `gcController.heapLive` | 11,401,152（~10.9MB） |
| 同时刻 vmmap footprint | 49.3M（峰值 75.2M）——与聚合值自洽 |

> 旧进程已随部署退出，无法用正确方法重取；其 ring 数值按 B2 要求撤回，不再
> 作为任何结论的依据。后续一致指标由 `/internal/diagnostics/runtime`
> `memory` 节（`runtime.ReadMemStats`，runtime 内完成旋转+聚合）持久提供，
> 并有 shape 契约测试（§4）。

### 3.3 应用层缓存逐一排除（源码审计）——等级：可靠（结论为「无 GB 级持有」）

投影 Kernel（27h 仅 2.4MB checkpoint）、LiveFrameBuffer（200 帧/1MB/60s 硬上限）、
claude catalog（元数据 + 指纹缓存）、web-push watcher accumulator（512B 封顶）、
grok 全局订阅器（offset + 小状态）、detail cache（21MB 磁盘）、web-push 账本
（123KB）——均无 GB 级持有。

### 3.4 根因结论——已证明项与候选解释分开

**已证明**：大 footprint 不是 2.5GB 可达 Go heap——活堆 ~20MB（gcController）与
footprint 2.5G 并存；2.4G 为 swapped 脏页且整块 arena 完全冰冷；应用层缓存无
GB 级持有；goroutine 正常。

**候选解释（未定论，r1 评审 Q5）**：「瞬态分配波触碰过的死页在整机内存压力下被
macOS 压缩器扣住而非丢弃」。v1 曾以（已撤回的）ring 读数支持「scavenger 已归还
后仍被扣住」；该支撑失效后，本解释只能列为候选。其他候选：未聚合的 runtime
retained memory（idle span 未释放）、非 Go 内存、mmap、kernel accounting。
swapped/retained 页的具体状态（已归还未重取 vs 仍被 runtime 持有）同样待一致
遥测定论。**裁决需要**：部署含 `memory` 遥测的构建后，在真实负载下记录
`Sys / HeapSys / HeapInuse / HeapIdle / HeapReleased / Sys-HeapReleased` 并与
同时刻 vmmap/footprint 对齐。

**分配波源头（源码归因，等级：可靠）**：

1. `resolveClaudeContinuationPaths`：每次 claude 历史加载重读项目目录全部 jsonl
   的 512KiB 头+尾窗口（619 文件 ≈ 数百 MB 读+解析）。
2. `claudeWebPushWatcher` 每 3s 无条件驱动 catalog refresh（refresh 在
   `SubscriptionCount()` 检查之前）；活跃 session 指纹每轮都变 → 每 3s 重解析。
   同时是 CPU 主源。
3. iOS 27h 重连 91 次，每次重连重拉全量投影快照。
4. codex-remote 轮询 p50 9.5s 持续超时重试（上游慢，已有退避）。

## 4. 修复方案（v4 状态）

### 修复 1：runtime 默认 GOMEMLIMIT 512MiB【r1 N2/Q1 裁决保留】

`applyDefaultMemoryLimit()`：`GOMEMLIMIT` env 未设置时
`debug.SetMemoryLimit(512<<20)`；env 显式设置则不干预。启动日志
`default memory limit applied limitBytes=536870912`。

**语义（按 Go 1.26.6 `runtime/debug/garbage.go:181-211`）**：这是 **Go runtime
管理内存的软限额**——runtime 提高 GC/归还力度以尝试维持 `MemStats.Sys -
HeapReleased` 不超过该值。进程总 footprint 仍可能超过它（OS 代持、C 内存、
mmap 不在管辖内）；过低时可能接近持续 GC；系统级 OOM 不因此被排除。512MiB
为 **provisional**：相对当前活堆（~20MB）有余量，待真实大历史 cold/warm 负载
数据（新遥测）复核。**不声称「钉在 512MiB」「用户看到低几百 MB」「不会 OOM」。**

### 修复 2：watcher 无订阅跳过 refresh + enrollment 重基线【r1 B3；r2-B3；r3-B3 流式化】

无订阅时完全跳过 catalog refresh；**禁用期间无条件清空 `startedAt` 与 states**。

enrollment 规则（由测试固定）：**订阅建立前已完成的 turn 不通知；订阅建立时
进行中、之后完成的 turn 会通知**。实现：`claudeFirstVisibleLiveCut` 之外，
enrollment sweep 经 `lastClaudeUserIdentityFromPath` **流式**回溯认领 baseline
前最后一条非 interrupt user 行的 turn 身份（R3-B3 处置）：

- **v3 缺陷（r3 评审 B3）**：旧实现复用 `scanClaudeRelayEntriesFromReader`，把
  completeCut 内全部 meaningful entry 连同 `json.RawMessage` 内容累积成 slice
  （本机最大 transcript ~64MB 会整段进堆），重新制造本次内存治理要消除的
  分配波。
- **v4 实现**：`lastClaudeUserIdentityFromReader` 专用流式扫描——过滤状态机与
  `scanClaudeRelayEntriesFromReader` 逐条对齐（compaction boundary / internal
  compact / task notification / resume meta + no-response 跳过；正向流式等价于
  旧实现的倒序查找：空 identity 的 user 行不覆盖已记录值），任意时刻只保留
  一个 identity 字符串，**O(1) 条目内存**。
- 测试：`TestLastClaudeUserIdentityFromReaderFiltersAndOrder`（全部过滤分支 +
  message.id 优先 + completeCut 截断）；
  `TestLastClaudeUserIdentityFromReaderBoundedMemoryOnLargeTranscript`（~12MB
  fixture + 周期性强制 GC 采样堆峰值，上界 = 基线 + 4MB；**判别力已实证**：旧
  slice 实现在同一采样形状下峰值超基线 **30MB**，流式实现 < 4MB，分离 7.5 倍）。
  另有 enrollment 真实时序测试
  `TestClaudeWebPushWatcherEnrollmentRetainsLiveTurnCompletingAfter`。
- 首轮 enabled sweep 尊重非零 `startedAt` 预设（生产构造形状测试）。

### 修复 3：WP-RESP-2 归档 + 样本捕获 + 404/410 清理启用【r2 §4 裁决已落地】

- 样本归档：数据目录 `web-push-samples/WP-RESP-2.jsonl`（3 端点 × 最早/最新，
  84 次 410 观测）。
- 404/410 分支 `webPushCaptureResponse` 样本捕获。
- `webPushExpirySemanticsProven = true`（授权链：owner 交评审裁决 → r2 报告 §4
  追认 → 失败路径修复完成后启用）。
- 失败路径：`MarkSubscriptionExpired` 持久化失败回滚内存；dispatcher 仅删除
  落盘成功记 `expired`，失败记 `expiry_cleanup_failed` 并保留订阅自愈重试。
- 测试：`TestWebPushStoreMarkExpiredRollsBackOnPersistFailure`、
  `TestDispatcher404CleanupFailureKeepsSubscriptionAndHonestLedger`。

### 修复 4：continuity 指纹缓存 v3【已提交 ca4e56d；r1 B4/N1 + r2-B2 处置】

有界 FIFO（4096）+ defensive copy + 读盘计数 + 同指纹并发 miss singleflight
（`claudeContinuityFlight` per-key in-flight，键 = path+size+mtime 完整指纹）。
效果边界：warm 收敛（同指纹并发恰好一次真实读）；冷启动首轮与指纹变化后首次
访问仍真实读盘；不声称「最大单波根治」。测试 6 条 + race ×3 全绿。

### 修复 5：设备撤销/注销联动的订阅删除事务性【R3-B2 处置，发布阻断】

- **v3 缺陷（r3 评审 B2）**：`DeleteDevice` 先删内存再持久化，失败不回滚——
  磁盘仍保留旧 subscription，当前进程暂时看不到，**runtime 重启后订阅复活**，
  已撤销、已断开 WebSocket 的设备继续收到含标题/预览的 Web Push（Apple
  endpoint 对活订阅可能 2xx，不能指望 404/410 兜底）。`Unregister` 同形（badge
  清理在持久化之前，失败时订阅回滚但 badge 已删）。
- **v4 实现**：`DeleteDevice`/`Unregister` 均改为 **subscription 删除先落盘、
  失败回滚内存**（与 `registerLocked`/`MarkSubscriptionExpired` 同一纪律）；
  badge 清理可独立失败、无投递权限影响，放在落盘之后。management 撤销路径在
  清理失败时于响应中如实返回 `pushCleanupError`（撤销本身不回滚——授权已收回、
  连接已断开），并删除「下次 404/410 也会被清除」的虚假注释保证。
- 测试：`TestWebPushStoreDeleteDeviceRollsBackOnPersistFailure`（store 级：
  失败报错 + 内存回滚 + 恢复权限后重载磁盘仍在）；
  `TestMgmtRevokeDeviceWebPushCleanupFailureExposed`（撤销 200 + revoked=true +
  响应含 pushCleanupError + 订阅保留无假成功）。

### 新增：`/internal/diagnostics/runtime` 一致内存遥测 + shape 契约【r1 Q6 优先级 1；r2 §5】

`memory` 节由 `runtime.ReadMemStats` 生成（runtime 内完成三代 ring 旋转+聚合）：
`sys / heapSys / heapInuse / heapIdle / heapReleased / sysMinusHeapReleased /
heapObjects / stackSys / numGC`。`TestMgmtRuntimeDiagnosticsMemoryShape` 断言
核心键存在且 `sysMinusHeapReleased == sys - heapReleased`。

## 5. 交付与部署证据（v4，部署完成后写入——R3-B1 处置）

### 5.1 构建来源门（实际执行构建的工作目录）

```text
构建工作目录=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
构建时提交=f45ac93f1f48e28e2b8639fa38d55e608b21264d
构建前未提交状态=干净（git status --short 无输出）
构建命令=GOSUMDB=sum.golang.org ./scripts/build-unsigned-release.sh（GOSUMDB 前缀规避本机 GOSUMDB=off + toolchain go1.26.6 的静默失败坑，见 think.md）
产物路径=build/unsigned-release/Build/Products/Release/CordCodeLink.app
runtime 版本元数据=cordcode-bridge-runtime 0.1.0 (commit: f45ac93f1f48, built: 2026-09-19T15:46:29Z)
产物符号核验=nm 确认 lastClaudeUserIdentityFromReader、(*WebPushStore).DeleteDevice、InspectTranscriptContinuity、applyDefaultMemoryLimit 均在
dist 产物=dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip（脚本生成）
```

### 5.2 安装与运行态（部署后实测）

```text
安装时间=2026-09-19T23:48:09+0800（killall + rm -rf /Applications/CordCodeLink.app + cp -R + open）
GUI 进程=PID 44119（/Applications/CordCodeLink.app/Contents/MacOS/CordCodeLink）
runtime 进程=PID 44215（/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，-port 8777 …）
8777 listener=lsof 确认由 PID 44215 LISTEN
特征日志=time=2026-09-19T23:48:15.733+08:00 level=INFO msg="go-bridge: default memory limit applied" limitBytes=536870912
启动 RSS=40880KB（仅记录，不作为内存效果验证——效果验证依赖新遥测的长期/负载数据）
```

### 5.3 部署历史（本任务全部代际）

| 代际 | runtime commit | 部署时间 | 状态 |
| --- | --- | --- | --- |
| 诊断对象 | 87b32a1d9556（2026-09-17 构建） | — | 已退出（诊断期运行 26h50m） |
| 修复 1–3 | f7cd91d | 2026-09-19 早些时候 | 已被替换（含 v1 410 翻转与 B3 enrollment 漏洞） |
| v3 代码 | f1ed656af43e（built 15:27:18Z） | 2026-09-19T23:29+0800（GUI 33250 / runtime 33343） | 已被替换（含 R3-B2/B3 缺陷） |
| **v4 代码（当前）** | **f45ac93f1f48（built 15:46:29Z）** | **2026-09-19T23:48:09+0800（GUI 44119 / runtime 44215）** | **运行中** |

### 5.4 验证状态

| 项 | 状态 |
| --- | --- |
| 定向测试 | go-bridge：relay 扫描族、watcher 全组、identity 流式（含堆上界）、dispatcher 404 正反 + 失败路径、store 回滚（MarkExpired + DeleteDevice）、revoke 暴露、memory limit、diagnostics shape——全绿；claudecode 全包全绿 |
| race | go-bridge（watcher/identity/dispatcher/store/revoke/diagnostics 组）+ claudecode（continuity ×3）通过；`go vet` 两包干净 |
| 部署 | 见 §5.2（部署后写入，非预先声明） |

## 6. 预期效果（措辞与证据等级对齐）

- GOMEMLIMIT：runtime 提高 GC/归还力度以尝试维持 `Sys - HeapReleased ≤ 512MiB`；
  **进程 footprint 仍可能超过该值**。数值 provisional，待负载数据复核。
- 无订阅期间零后台扫描；enrollment 不回放历史完成；enrollment 时进行中的
  turn 完成后通知（流式回溯，O(1) 条目内存，不再物化 transcript）。
- 修复 4：同一进程内未变化文件的 continuity 头尾扫描收敛为每指纹一次；
  冷启动首轮仍全量。
- 死订阅清理：404/410 即删（落盘成功）；持久化失败时保留订阅自愈重试，
  账本如实记录。
- 设备撤销/注销：subscription 删除落盘成功才从内存消失；失败回滚 + 响应暴露
  `pushCleanupError`，重启后无复活。

## 7. 遗留与开放问题（v4 更新）

1. ~~owner 追认 410 翻转~~——已由 r2 §4 追认并启用（失败路径已修）。
2. **根因定论**（§3.4）：部署新遥测后在真实负载下对齐 `Sys/HeapReleased` 与
   vmmap/footprint，再决定是否需要 XNU 级实验。**metrics-first**。
3. GOMEMLIMIT 数值复核：真实大历史 cold/warm 负载下 GC CPU、延迟、
   `Sys-HeapReleased` 数据。
4. iOS 27h 重连 91 次：另案按时间线查（r1 Q6：与内存修复解耦）。
5. pprof：r1 Q6 裁决「不应先于低暴露面的 runtime metrics」——metrics 已加，
   pprof 顺位其后，暂不做。
6. 有订阅时单个变化文件的 3s 有界扫描（512KiB 上限）：评审裁定暂不优先。
7. ~~`DeleteDevice` 同形缺口~~——R3-B2 已修（回滚 + 顺序 + 暴露 + 测试）。

## 8. 元复盘（v2 增补，v3/v4 补教训）

owner 元批评（「先用省事的临时办法糊弄」）与历轮评审教训一致。v3 新增两类：
**复用现成 helper 而不审其内存形状**（identity 回溯复用了返回全量 slice 的
scanner，把刚治理好的分配波从后门请回来——R3-B3）；**完成报告先于事实**
（部署尚未发生就把「部署完成」写进已提交文档——R3-B1）。v4 的处置原则追加：
**跨路径复用必须核对成本模型；部署证据只在部署后写入。**

## 9. 阻断项 → 处置映射（供复审）

### Round 1（报告 commit `0469d9c`）

| 评审项 | 处置 | 位置 |
| --- | --- | --- |
| B1 来源清单 | 补全：诊断时 HEAD / 各 commit / 部署产物 / iOS 仓完整身份 | §1 |
| B2 ring 误读 | 撤回 committed/released 断言；gcController 证据保留并标注等级；正确方法一致读取；ReadMemStats 遥测 | §3.2、§4 |
| B3 enrollment 漏洞 | 禁用分支无条件重置 startedAt/states；生产形状测试 + enrollment 回归测试 | 修复 2 |
| B4 cache 生命周期/冷波 | 容量 4096 + FIFO 逐出；结论改为「重复扫描收敛」；cold/warm 分开表述；新增同尺寸 mtime/删除重建/并发 miss/容量逐出测试 | 修复 4 |
| B5 owner 门 | v2 曾回退 false；r2 §4 追认后按裁决启用（失败路径先行） | 修复 3 |
| N1 alias 契约 | defensive copy；测试改读盘计数 | 修复 4 |
| N2 GOMEMLIMIT 措辞 | 按 garbage.go 语义重写代码注释与本稿；512MiB 标 provisional | 修复 1、§6 |

### Round 2（报告 commit `743bde8`）

| 评审项 | 处置 | 位置 |
| --- | --- | --- |
| R2-B1 `<本 commit>` 占位符 | v3 代码修订提交=ca4e56d…（真实哈希） | §1 |
| R2-B2 并发 miss 未合并 | `claudeContinuityFlight` per-key in-flight 合并；并发测试收紧为恰好 1 次真实读 | 修复 4 |
| R2-B3 enrollment 进行中 turn | 规则维持「进行中 turn 完成后通知」；回溯认领 + 真实时序测试（r3 又发现回溯 helper 的内存形状问题，见 R3-B3） | 修复 2 |
| R2-B4 长期文档定论残留 | think.md/CHANGELOG 重排为先证明项、页状态待定论 | think.md、CHANGELOG |
| r2 §4 404/410 两层裁决 | gate 置 true + 失败路径修复 + 2 条失败路径测试 | 修复 3 |
| r2 §5 非阻断 | memory shape 测试；计数注释；FIFO 首槽引用释放 | §4 |

### Round 3（报告 commit `d4fa165`）

| 评审项 | 处置 | 位置 |
| --- | --- | --- |
| R3-B1 部署前提交却宣称部署完成 | 本稿为**部署后** docs-only 修订：§5 写入真实构建来源门（工作目录/分支/提交/构建前干净/产物路径/版本元数据/符号核验）、安装时间、GUI/runtime PID、8777 listener、特征日志、启动 RSS、全部代际历史 | §5 |
| R3-B2 DeleteDevice 失败不回滚（发布阻断） | subscription 删除先落盘 + 失败回滚内存；badge 清理独立失败后置；Unregister 同形复核调整；撤销响应暴露 pushCleanupError；store + management 失败路径测试 | 修复 5 |
| R3-B3 identity 回溯物化整个 transcript | `lastClaudeUserIdentityFromReader` 流式 O(1) 条目内存，过滤状态机与旧实现逐条对齐；等价性测试 + 大 transcript 堆上界测试（周期 GC 采样；旧实现实测 +30MB / 新实现 <4MB，判别力实证） | 修复 2 |
| R3-B4 §3.1 压缩器事实化 | 表格行改为「vmmap 报告的 SWAPPED/DIRTY 页」+ 表下注记形状 vs 机制之分 | §3.1 |
