# 2026-09-19 bridge runtime「内存 2G+」诊断与修复（评审稿 v3，待复审）

> 状态：**修订 v3，待复审**。v1（commit `685646d`）经评审（报告
> `docs/2026-09-19-bridge-runtime-memory-footprint-review-report.md`，commit `0469d9c`）
> 判定不通过（5 阻断项）；v2（commit `bc328df`）经复审（报告
> `docs/2026-09-19-bridge-runtime-memory-footprint-review-report-r2.md`，commit `743bde8`）
> 仍不通过（4 阻断项 + §4 404/410 语义追认 + §5 非阻断修正）。本版逐项处置，
> 映射见 §9。全部代码与测试已提交（`ca4e56d`，见 §1）；部署按 r2 准入条件 6
> 在定向测试通过后进行（见 §5）。

## 1. 来源清单（P0，v3 补全真实哈希）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
诊断时源码 HEAD=bd4a2ce（工作树干净；诊断对象为 87b32a1d 构建的生产二进制，见下）
修复 1–3 提交=f7cd91d（已部署 /Applications，见 §5）
v1 评审稿提交=685646d；r1 评审报告提交=0469d9c
v2 修订提交=bc328dfd41099fb5369c4d905394a9d90f7896a6；r2 复审报告提交=743bde8e82439aa94e7175390756584fece1efbd
v3 代码修订提交=ca4e56d8a34b890990cad7c41e2051fe4260ec4e（singleflight 缓存、410 失败路径 + gate 翻转、enrollment live-turn 测试、memory shape 测试、think.md/CHANGELOG 措辞）
未提交状态=本稿为 docs-only 提交；代码工作树在 ca4e56d 后无其他未提交修改
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 5ac43b897e2a9333bd191363ea83d68447d70550
配套仓库未提交状态=OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift 修改（非本任务产物——本任务无 iOS 侧改动，未读取该文件内容做任何结论）
预期产品特性=runtime 默认内存软限额（Go runtime-managed）；无订阅时 watcher 不扫描；enrollment 重基线不回放且进行中 turn 完成后通知；404/410 清理（r2 §4 追认，失败路径已修）；continuity 重复扫描收敛（同指纹并发 miss 合并为一次）；/internal/diagnostics/runtime 一致内存快照 + shape 契约测试
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

## 3. 诊断证据链（v2 修正证据等级，v3 无变化）

### 3.1 原始观测（vmmap / 日志 / 采样，非源码推断）——等级：可靠

| 观测 | 数值 |
| --- | --- |
| Physical footprint | 2.5G（1 分钟内平稳 2.4–2.5G）；峰值 3.8G |
| RESIDENT / DIRTY | ~200M / ~59M |
| SWAPPED（被 macOS 压缩器扣住的脏页） | 2.4G，其中 9 个 128MB Go 堆 arena 整块换出、0K 常驻（完全冰冷） |
| goroutine 数 | 264（正常） |
| 27h 内 hydrate 的 session | 仅 7 个（19 次 commit）；本进程只写了 2.4MB checkpoint |
| CPU 采样主帧 | `scanClaudeSessionMetadata → InspectTranscriptContinuity → json.Unmarshal`；次帧 `claudeWebPushWatcher.sweep/run` |
| 轮询量（27h） | claude 1615、codex-remote 1385 ok + 229 超时（p50 9.5s）、grokbuild 5283、opencode-web 1705、dsh-web 1611 |
| iOS 连接 | connectionGeneration=91（27h 内 91 次重连） |
| web-push | 3 个 Apple 端点持续 410 Gone（日志 84 次观测，跨 01:04→22:04） |
| 磁盘数据量 | `~/.claude/projects` 640MB / 619 个 jsonl；`~/.grok/sessions` 900MB |

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

**正确方法的一致读取（f7cd91d 部署后新进程 PID 91090，单次 attach 读全三代 + gen，
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
> 并有 shape 契约测试（§4 新增节）。

### 3.3 应用层缓存逐一排除（源码审计）——等级：可靠（结论为「无 GB 级持有」）

投影 Kernel（27h 仅 2.4MB checkpoint）、LiveFrameBuffer（200 帧/1MB/60s 硬上限）、
claude catalog（元数据 + 指纹缓存）、web-push watcher accumulator（512B 封顶）、
grok 全局订阅器（offset + 小状态）、detail cache（21MB 磁盘）、web-push 账本
（123KB）——均无 GB 级持有。

### 3.4 根因结论——已证明项与候选解释分开（r1 B2 / r2 B4 统一口径）

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

## 4. 修复方案（v3 状态）

### 修复 1：runtime 默认 GOMEMLIMIT 512MiB【r1 N2/Q1 裁决保留；措辞已按软限额语义】

`applyDefaultMemoryLimit()`：`GOMEMLIMIT` env 未设置时
`debug.SetMemoryLimit(512<<20)`；env 显式设置则不干预。启动日志
`default memory limit applied limitBytes=536870912`。

**语义（按 Go 1.26.6 `runtime/debug/garbage.go:181-211`）**：这是 **Go runtime
管理内存的软限额**——runtime 提高 GC/归还力度以尝试维持 `MemStats.Sys -
HeapReleased` 不超过该值。进程总 footprint 仍可能超过它（OS 代持、C 内存、
mmap 不在管辖内）；过低时可能接近持续 GC；系统级 OOM 不因此被排除。512MiB
为 **provisional**：相对当前活堆（~20MB）有余量，待真实大历史 cold/warm 负载
数据（新遥测）复核。**不声称「钉在 512MiB」「用户看到低几百 MB」「不会 OOM」。**

### 修复 2：watcher 无订阅跳过 refresh + enrollment 重基线【r1 B3 修复；r2-B3 补齐 live-at-enrollment】

无订阅时完全跳过 catalog refresh；**禁用期间无条件清空 `startedAt` 与 states**
（v1 只在「曾见过订阅」时清空，导致「进程启动时无订阅、数小时后首次订阅」沿用
进程启动时刻为首见 cut，回放订阅前的历史完成——r1 评审 B3）。

enrollment 规则（由测试固定）：**订阅建立前已完成的 turn 不通知；订阅建立时
进行中、之后完成的 turn 会通知**。r2-B3 指出 v2 只测了「订阅后新开始的 turn」；
v3 补齐实现支撑与真实时序测试：

- 实现：`claudeFirstVisibleLiveCut` 之外，enrollment sweep 经
  `lastClaudeUserIdentityFromPath`（`handlers_relay.go`）**回溯认领** baseline 前
  最后一条非 interrupt user 行的 turn 身份——订阅时已进行中、未完成的尾 turn
  因此不被 baseline 切掉，terminal 行到达后照常通知。
- 测试：`TestClaudeWebPushWatcherEnrollmentRetainsLiveTurnCompletingAfter`——
  enrollment 前写入「旧完成 turn + 未完成 turn（仅 user 行）」，disabled sweep，
  注册订阅，enrollment sweep（0 回放），**只追加 terminal 行**，sweep 后恰好 1 条
  候选且预览来自该 terminal 行。与
  `TestClaudeWebPushWatcherFirstVisibleRetainsLiveCompletion`（进程启动即有订阅的
  FirstVisible live-turn 语义）共同覆盖两条路径。
- 首轮 enabled sweep 尊重非零 `startedAt` 预设（生产构造形状测试
  `TestClaudeWebPushWatcherSkipsCatalogRefreshWithoutSubscriptions` 已改造）。

### 修复 3：WP-RESP-2 归档 + 样本捕获 + 404/410 清理启用【r2 §4 两层裁决均已落地】

- 样本归档保留：数据目录 `web-push-samples/WP-RESP-2.jsonl`（3 端点 × 最早/最新，
  84 次 410 观测），与 `docs/2026-09-12-remote-web-push-badge-and-collapse-plan.md`
  §9 预言一致。
- 404/410 分支补上 `webPushCaptureResponse` 样本捕获——保留。
- **语义追认（r2 §4）**：owner 已把 404/410 清理语义交由评审裁决，r2 复审报告
  §4 追认「404/410 = subscription 不再可用，应删除」，该报告即授权记录。
- **失败路径（r2 §4 启用前置）已修**：
  - `WebPushStore.MarkSubscriptionExpired`：持久化失败时**回滚内存删除**并返回
    错误（与 `registerLocked`/`Unregister` 同一纪律）——磁盘失败不再出现
    「内存已删、重启后订阅从旧文件复活」的假清理。
  - dispatcher：仅删除**落盘成功**才记 `expired`；失败保留 subscription、记
    `expiry_cleanup_failed`（真实状态，后续投递自愈重试清理），并 WARN 暴露。
  - 测试：`TestWebPushStoreMarkExpiredRollsBackOnPersistFailure`（store 级：
    chmod 破坏持久化 → 删除报错 + 内存回滚 + 恢复权限后重载磁盘仍在）；
    `TestDispatcher404CleanupFailureKeepsSubscriptionAndHonestLedger`
    （dispatcher 级：404 + 持久化失败 → 账本 `expiry_cleanup_failed`、
    subscription 保留）。
- **`webPushExpirySemanticsProven = true`**：注释如实记录授权链（owner 交评审
  裁决 → r2 报告 §4 追认 → 失败路径修复完成后启用）。

### 修复 4：continuity 指纹缓存 v3【已提交 ca4e56d；r1 B4/N1 + r2-B2 处置】

`InspectTranscriptContinuity` 的进程内缓存：

- **有界生命周期**：严格容量（默认 4096）+ FIFO 逐出（逐出时清空 order 首槽
  引用，r2 §5）；删除/重命名/迁移后的残留路径不再永久滞留。
- **defensive copy**：命中与冷读返回值均深拷贝 `BoundaryIDs`，解除 exported API
  与缓存内部的 alias（r1 N1）；测试用**读盘计数**（`claudeContinuityFileReads`，
  注释明确仅测试用途）证明 warm 命中。
- **同指纹并发 miss 合并（r2-B2）**：`claudeContinuityFlight` per-key in-flight
  机制，键为 `path+size+mtime` 完整指纹——第一个到达者成为 leader 真实读盘，
  并发等待者等 `done` 后复用同一结果；**不持全局 mutex 扫盘**（不同 transcript
  的 cold seed 不互相串行化），扫描期间文件被追加时新指纹立即开自己的 flight。
  缓存命中检查与 flight 查找在同一临界区，put 与 flight 删除之间不存在
  「两者都 miss」的窗口，等待者不会重复扫盘。读盘失败不写缓存（flight 照常
  完成，等待者拿到与自行失败一致的结果；下一个调用者自愈重读）。
- **效果边界（诚实表述）**：消除的是**同一进程内未变化文件的重复头尾扫描**
  （warm 收敛，同指纹并发 miss 恰好一次真实读）；冷启动首轮与指纹变化后的
  首次访问仍真实读盘。**不声称「最大单波根治」**——冷启动第一次
  catalog/rich-history 扫描仍遍历全部 JSONL。
- 指纹 `(size, mtime)` 与 catalog 的 `claudeSessionFingerprint` 方向一致但
  **不声称等价**（catalog 另含 sidecar 与 Desktop state，服务不同缓存内容；
  sidecar 不参与 continuity 是安全的——boundary 只在 JSONL 正文）。
- 测试（6 条，全绿，race 3 轮通过）：warm 零读盘 + 污染隔离、cold 计数、
  size 变化失效、**同尺寸 mtime 变化失效**、**删除后重建/path 复用**、
  **并发 miss（断言恰好 1 次真实读）**、**容量逐出**。

### 新增：`/internal/diagnostics/runtime` 一致内存遥测 + shape 契约【r1 Q6 优先级 1；r2 §5 补测试】

`memory` 节由 `runtime.ReadMemStats` 生成（runtime 内完成三代 ring 旋转+聚合，
即评审要求的「一致快照」）：`sys / heapSys / heapInuse / heapIdle / heapReleased /
sysMinusHeapReleased / heapObjects / stackSys / numGC`。这是 B2 复核条件 2 的
持久化形式，也是 GOMEMLIMIT 数值复核的数据源。

`TestMgmtRuntimeDiagnosticsMemoryShape`（r2 §5）：断言 `memory` 对象核心键存在
且 `sysMinusHeapReleased == sys - heapReleased`，防止未来 JSON shape 漂移破坏
对账口径。

## 5. 交付与验证状态（v3）

| 项 | 状态 |
| --- | --- |
| 修复 1–3、遥测、修复 4（含 singleflight）、全部测试 | 已提交 `ca4e56d`（本稿 docs-only 提交引用该哈希） |
| 定向测试 | claudecode 全包 + continuity 6 条（race ×3）；go-bridge：watcher 全组（含 enrollment live-turn）、dispatcher 404 正反 + 失败路径、store 回滚、memory limit、diagnostics shape、WebPush/Dispatcher/Watcher 全族——全绿；`go vet` 两包干净 |
| 部署 | 按 r2 准入条件 6（定向测试通过后构建和部署）执行；部署身份与验证见下 |
| 部署后验证 | 进程代际、`-version` 特征行、8777 listener、启动日志 `default memory limit applied`；**启动 RSS 不作为内存效果验证**——效果验证依赖新遥测的长期/负载数据（§7.2） |

## 6. 预期效果（措辞与证据等级对齐）

- GOMEMLIMIT：runtime 提高 GC/归还力度以尝试维持 `Sys - HeapReleased ≤ 512MiB`；
  **进程 footprint 仍可能超过该值**。数值 provisional，待负载数据复核。
- 无订阅期间零后台扫描；enrollment 不回放历史完成；enrollment 时进行中的
  turn 完成后通知。
- 修复 4：**同一进程内**未变化文件的 continuity 头尾扫描收敛为每指纹一次
  （并发 miss 合并）；冷启动首轮仍全量（与 catalog seed 同批摊销，
  rich-history 先行时单独支付）。
- 死订阅清理：404/410 即删（落盘成功）；持久化失败时保留订阅自愈重试，
  账本如实记录。

## 7. 遗留与开放问题（v3 更新）

1. ~~owner 追认 410 翻转~~——已由 r2 §4 追认并启用（失败路径已修）。
2. **根因定论**（§3.4）：部署新遥测后在真实负载下对齐 `Sys/HeapReleased` 与
   vmmap/footprint，再决定是否需要 XNU 级实验。**metrics-first**。
3. GOMEMLIMIT 数值复核：真实大历史 cold/warm 负载下 GC CPU、延迟、
   `Sys-HeapReleased` 数据。
4. iOS 27h 重连 91 次：另案按时间线查（r1 Q6：与内存修复解耦）。
5. pprof：r1 Q6 裁决「不应先于低暴露面的 runtime metrics」——metrics 已加，
   pprof 顺位其后，暂不做。
6. 有订阅时单个变化文件的 3s 有界扫描（512KiB 上限）：评审裁定暂不优先。
7. `WebPushStore.DeleteDevice`（设备撤销联动删除）持久化失败时不回滚内存——
   与 r2 §4 同形的已知缺口，本轮未改（不在 r2 阻断项内），留待下轮评审裁决。

## 8. 元复盘（v2 增补，v3 无变化）

owner 元批评（「先用省事的临时办法糊弄」）与 v1 评审的教训一致：第一轮把最大
单波（continuity 全目录重读）推迟为「后续候选」；v1 评审稿又出现三处同类问题——
把单槽 ring 读数当判定性证据（B2）、把「曾见过订阅」当成唯一需要重置基线的
场景（B3）、把缓存说成「最大单波根治/条目随当前文件数有界」（B4）。v2/v3 的
处置原则：**每个断言标注证据等级；每个修复写明效果边界；owner 门不越权**。

## 9. 阻断项 → 处置映射（供复审）

### Round 1（报告 commit `0469d9c`）

| 评审项 | 处置 | 位置 |
| --- | --- | --- |
| B1 来源清单 | 补全：诊断时 HEAD / 各 commit / dirty overlay / 部署产物 / iOS 仓完整身份 | §1 |
| B2 ring 误读 | 撤回 committed/released 断言；gcController 证据保留并标注等级；正确方法一致读取；ReadMemStats 遥测 | §3.2、§4 |
| B3 enrollment 漏洞 | 禁用分支无条件重置 startedAt/states；生产形状测试 + enrollment 回归测试 | 修复 2 |
| B4 cache 生命周期/冷波 | 容量 4096 + FIFO 逐出；结论改为「重复扫描收敛」；cold/warm 分开表述；新增同尺寸 mtime/删除重建/并发 miss/容量逐出测试 | 修复 4 |
| B5 owner 门 | v2 曾回退 false；r2 §4 追认后按裁决启用（失败路径先行） | 修复 3 |
| N1 alias 契约 | defensive copy；测试改读盘计数 | 修复 4 |
| N2 GOMEMLIMIT 措辞 | 按 garbage.go 语义重写代码注释与本稿；512MiB 标 provisional | 修复 1、§6 |

### Round 2（报告 commit `743bde8`）

| 评审项 | 处置 | 位置 |
| --- | --- | --- |
| R2-B1 `<本 commit>` 占位符 | v3 代码修订提交=ca4e56d8a34b890990cad7c41e2051fe4260ec4e（真实哈希）；本稿为 docs-only 提交 | §1 |
| R2-B2 并发 miss 未合并 | `claudeContinuityFlight` per-key（path+size+mtime）in-flight 合并，leader 唯一真实读盘；不持全局 mutex 扫盘；并发测试收紧为 `reads() == 1` | 修复 4 |
| R2-B3 enrollment 进行中 turn | 产品规则维持「进行中 turn 完成后通知」；实现经 `lastClaudeUserIdentityFromPath` 回溯认领；新增真实时序测试（enrollment 前未完成 turn、之后只追加 terminal 行） | 修复 2 |
| R2-B4 长期文档定论残留 | think.md 标题改为「已证明不是 2.5GB 可达 Go heap；swapped/retained 页状态与压缩器机制待一致遥测」；尾段候选改为 metrics-first 顺序（去掉「最大单波」与 pprof 优先）；CHANGELOG 句式重排（先证明项，页状态为待定论） | think.md、CHANGELOG.md（ca4e56d） |
| r2 §4 404/410 两层裁决 | 语义追认落地：gate 置 true（注释记录授权链）；失败路径修复（MarkSubscriptionExpired 回滚 + 账本仅落盘成功记 expired，失败记 expiry_cleanup_failed）+ 2 条失败路径测试 | 修复 3 |
| r2 §5 非阻断 | `TestMgmtRuntimeDiagnosticsMemoryShape`（键 + sysMinusHeapReleased 恒等式）；`claudeContinuityFileReads` 注释去「线上观测探针」言；FIFO 逐出清空首槽引用 | §4 新增节、修复 4 |
