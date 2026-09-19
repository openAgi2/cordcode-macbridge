# 2026-09-19 bridge runtime「内存 2G+」诊断与修复（评审稿 v2，待复审）

> 状态：**修订 v2，待复审**。v1（commit `685646d`）经评审（报告
> `docs/2026-09-19-bridge-runtime-memory-footprint-review-report.md`，commit `0469d9c`）
> 判定不通过，5 个阻断项 + 2 个非阻断修正项。本版逐项处置，映射见 §9。
> 修复 1–3 修订版与新增遥测已提交（见 §1 提交清单）；修复 4（continuity cache v2）
> 按复审准入条件保持**未提交**。

## 1. 来源清单（P0，v2 补全）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
诊断时源码 HEAD=bd4a2ce（工作树干净；诊断对象为 87b32a1d 构建的生产二进制，见下）
修复 1–3 提交=f7cd91d（已部署 /Applications，见 §5）
v1 评审稿提交=685646d；评审报告提交=0469d9c
v2 修订提交=<本 commit>（watcher enrollment 修复、410 回退、GOMEMLIMIT 措辞、内存遥测）
未提交状态=agent/claudecode/continuation.go（修改）+ agent/claudecode/continuation_test.go（新增）——修复 4 v2，待复审
任务预期分支=feat/ios-native-message-timeline
配套仓库=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 5ac43b897e2a9333bd191363ea83d68447d70550（未提交状态=干净，本次复核重新生成；无 iOS 侧改动）
预期产品特性=runtime 默认内存软限额（Go runtime-managed）；无订阅时 watcher 不扫描；enrollment 重基线不回放；404/410 样本捕获（清理待 owner 追认）；continuity 重复扫描收敛（修复 4，未提交）；/internal/diagnostics/runtime 增加一致内存快照
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

## 3. 诊断证据链（v2 修正证据等级）

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

### 3.2 Go runtime 内部读取（LLDB）——v2 修正：v1 的 ring 单槽读法不成立，相关断言撤回

**v1 错误（评审 B2）**：把 `memstats.heapStats` 的单个 delta 槽当成一致快照，
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

**v2 补充：正确方法的一致读取（新进程 PID 91090，单次 attach 读全三代 + gen，
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
> 作为任何结论的依据。后续一致指标由新增的 `/internal/diagnostics/runtime`
> `memory` 节（`runtime.ReadMemStats`，runtime 内完成旋转+聚合）持久提供。

### 3.3 应用层缓存逐一排除（源码审计）——等级：可靠（结论为「无 GB 级持有」）

投影 Kernel（27h 仅 2.4MB checkpoint）、LiveFrameBuffer（200 帧/1MB/60s 硬上限）、
claude catalog（元数据 + 指纹缓存）、web-push watcher accumulator（512B 封顶）、
grok 全局订阅器（offset + 小状态）、detail cache（21MB 磁盘）、web-push 账本
（123KB）——均无 GB 级持有。

### 3.4 根因结论——v2 降级为假设 + 候选解释

**已证实**：footprint 2.5G 与活堆 ~20MB 并存；2.4G 为 swapped 脏页且整块 arena
完全冰冷；应用层缓存无 GB 级持有；goroutine 正常。

**候选解释（未定论，评审 Q5）**：「瞬态分配波触碰过的死页在整机内存压力下被
macOS 压缩器扣住而非丢弃」。v1 曾以（已撤回的）ring 读数支持「scavenger 已归还
后仍被扣住」；该支撑失效后，本解释只能列为候选。其他候选：未聚合的 runtime
retained memory（idle span 未释放）、非 Go 内存、mmap、kernel accounting。
**裁决需要**：部署含 `memory` 遥测的构建后，在真实负载下记录
`Sys / HeapSys / HeapInuse / HeapIdle / HeapReleased / Sys-HeapReleased` 并与
同时刻 vmmap/footprint 对齐（复审准入条件 2 的持久化形式）。

**分配波源头（源码归因，等级：可靠）**：

1. `resolveClaudeContinuationPaths`：每次 claude 历史加载重读项目目录全部 jsonl
   的 512KiB 头+尾窗口（619 文件 ≈ 数百 MB 读+解析）。
2. `claudeWebPushWatcher` 每 3s 无条件驱动 catalog refresh（refresh 在
   `SubscriptionCount()` 检查之前）；活跃 session 指纹每轮都变 → 每 3s 重解析。
   同时是 CPU 主源。
3. iOS 27h 重连 91 次，每次重连重拉全量投影快照。
4. codex-remote 轮询 p50 9.5s 持续超时重试（上游慢，已有退避）。

## 4. 修复方案（v2 状态）

### 修复 1：runtime 默认 GOMEMLIMIT 512MiB【v2 措辞修正，评审 N2/Q1 裁决保留】

`applyDefaultMemoryLimit()`：`GOMEMLIMIT` env 未设置时
`debug.SetMemoryLimit(512<<20)`；env 显式设置则不干预。启动日志
`default memory limit applied limitBytes=536870912`。

**语义（按 Go 1.26.6 `runtime/debug/garbage.go:181-211`）**：这是 **Go runtime
管理内存的软限额**——runtime 提高 GC/归还力度以尝试维持 `MemStats.Sys -
HeapReleased` 不超过该值。进程总 footprint 仍可能超过它（OS 代持、C 内存、
mmap 不在管辖内）；过低时可能接近持续 GC；系统级 OOM 不因此被排除。512MiB
为 **provisional**：相对当前活堆（~20MB）有余量，待真实大历史 cold/warm 负载
数据（新遥测）复核。**不再声称「钉在 512MiB」「用户看到低几百 MB」「不会 OOM」。**

### 修复 2：watcher 无订阅跳过 refresh + enrollment 重基线【v2 修复 B3 漏洞】

无订阅时完全跳过 catalog refresh；**禁用期间无条件清空 `startedAt` 与 states**
（v1 只在「曾见过订阅」时清空，导致「进程启动时无订阅、数小时后首次订阅」沿用
进程启动时刻为首见 cut，回放订阅前的历史完成——评审 B3）。

enrollment 规则（由测试固定）：**订阅建立前已完成的 turn 不通知；订阅建立时
进行中、之后完成的 turn 会通知**（`claudeFirstVisibleLiveCut` 以 enrollment 时刻
为 cut）。首轮 enabled sweep 尊重非零 `startedAt` 预设（进程启动即有订阅的常见
路径 + FirstVisible live-turn 语义）。

测试：`TestClaudeWebPushWatcherSkipsCatalogRefreshWithoutSubscriptions`（v2 改为
生产构造形状：构造时设置 startedAt）+ 新增
`TestClaudeWebPushWatcherEnrollmentAfterDisabledPeriodDoesNotReplayHistory`
（生产形状 + 启动后订阅前历史完成 + enrollment 不回放 + 进行中 turn 完成后通知）。

### 修复 3：WP-RESP-2 归档 + 样本捕获；**清理翻转回退，待 owner 追认**【v2 处置 B5】

- 样本归档保留：数据目录 `web-push-samples/WP-RESP-2.jsonl`（3 端点 × 最早/最新，
  84 次 410 观测），与 `docs/2026-09-12-remote-web-push-badge-and-collapse-plan.md`
  §9 预言一致。
- 404/410 分支补上 `webPushCaptureResponse` 样本捕获（v1 前该路径不写样本文件，
  证据门无法自满足）——保留。
- **`webPushExpirySemanticsProven` 回退为 `false`**（v1 曾置 true）：原门要求
  「归档后由 owner 显式置 true」，v1 的翻转未获该授权，注释还错误写成已授权
  （评审 B5）。现注释如实记录：样本已归档、翻转待 owner 显式追认；追认前
  404/410 不删 subscription、记 expiry_unverified。
- **owner 决定点**：追认即一行改动（`false`→`true`）+ 注释更新为真实授权记录。
  技术证据充分偏向翻转（评审 B5 原文：三个 prefix 的大量真实 410、状态机正反
  测试齐备）。

### 修复 4：continuity 指纹缓存 v2【未提交，待复审；v2 处置 B4+N1】

`InspectTranscriptContinuity` 的进程内缓存，v2 重写：

- **有界生命周期**：严格容量（默认 4096）+ FIFO 逐出；删除/重命名/迁移后的
  残留路径不再永久滞留（v1 无任何清理，评审 B4）。
- **defensive copy**：命中与冷读返回值均深拷贝 `BoundaryIDs`，解除 exported API
  与缓存内部的 alias（评审 N1）；v1 测试用指针相等证明命中，反而把危险 alias
  固化为契约——已改为**读盘计数**（`claudeContinuityFileReads`）证明。
- **效果边界（诚实表述）**：消除的是**同一进程内未变化文件的重复头尾扫描**
  （warm 收敛）；冷启动首轮与指纹变化后的首次访问仍真实读盘（cold miss，
  每指纹一次）。**不声称「最大单波根治」**——冷启动第一次 catalog/rich-history
  扫描仍遍历全部 JSONL。
- 指纹 `(size, mtime)` 与 catalog 的 `claudeSessionFingerprint` 方向一致但
  **不声称等价**（catalog 另含 sidecar 与 Desktop state，服务不同缓存内容；
  sidecar 不参与 continuity 是安全的——boundary 只在 JSONL 正文）。
- 测试（6 条，全绿）：warm 零读盘 + 污染隔离、cold 计数、size 变化失效、
  **同尺寸 mtime 变化失效**、**删除后重建/path 复用**、**并发 miss**、
  **容量逐出**。

### 新增：`/internal/diagnostics/runtime` 一致内存遥测【评审 Q6 优先级 1】

`memory` 节由 `runtime.ReadMemStats` 生成（runtime 内完成三代 ring 旋转+聚合，
即评审要求的「一致快照」）：`sys / heapSys / heapInuse / heapIdle / heapReleased /
sysMinusHeapReleased / heapObjects / stackSys / numGC`。这是 B2 复核条件 2 的
持久化形式，也是 GOMEMLIMIT 数值复核的数据源。

## 5. 交付与验证状态（v2）

| 项 | 状态 |
| --- | --- |
| 修复 1（措辞修正）、修复 2（含 B3 修复）、修复 3（回退）、内存遥测 | 本 v2 commit 提交 |
| 修复 4（continuity cache v2） | **未提交**（复审准入条件 6：定向测试通过后再决定提交） |
| 定向测试 | claudecode（6 条 continuity）+ go-bridge（watcher 全组含 2 条新测试、dispatcher 404 正反、memory limit、diagnostics、web-push/catalog 扩大组）全绿 |
| 部署 | `/Applications` 仍为 `f7cd91d` 构建（PID 91090）——**含 v1 的 410 翻转与 B3 enrollment 漏洞**；重新部署待复审通过后与修复 4 一并进行 |
| 部署后验证（f7cd91d 当时） | 进程代际 ✓、特征行 ✓、8777 ✓；**启动 RSS 39MB 不作为内存效果验证**（评审条件 6）——效果验证依赖新遥测的长期/负载数据 |
| 当前进程一致读数 | footprint 49.3M（峰值 75.2M）、heapLive ~10.9MB、memoryLimit=512MiB 生效（§3.2 v2 表） |

## 6. 预期效果（v2 措辞修正）

- GOMEMLIMIT：runtime 提高 GC/归还力度以尝试维持 `Sys - HeapReleased ≤ 512MiB`；
  **进程 footprint 仍可能超过该值**。数值 provisional，待负载数据复核。
- 无订阅期间零后台扫描；enrollment 不回放历史完成。
- 修复 4 合入后：**同一进程内**未变化文件的 continuity 头尾扫描收敛为每指纹
  一次；冷启动首轮仍全量（与 catalog seed 同批摊销，rich-history 先行时单独支付）。
- 死订阅清理：待 owner 追认后生效（追认前每次通知仍白发一遍，量级：3 个端点）。

## 7. 遗留与开放问题（v2 更新）

1. **owner 追认 410 翻转**（§4 修复 3）——一行改动，等明确授权。
2. **根因定论**（§3.4）：部署新遥测后在真实负载下对齐 `Sys/HeapReleased` 与
   vmmap/footprint，再决定是否需要 XNU 级实验。
3. GOMEMLIMIT 数值复核：真实大历史 cold/warm 负载下 GC CPU、延迟、
   `Sys-HeapReleased` 数据。
4. iOS 27h 重连 91 次：另案按时间线查（评审 Q6：与内存修复解耦）。
5. pprof：评审 Q6 裁决「不应先于低暴露面的 runtime metrics」——metrics 已加，
   pprof 顺位其后，暂不做。
6. 有订阅时单个变化文件的 3s 有界扫描（512KiB 上限）：评审裁定暂不优先。

## 8. 元复盘（v2 增补）

owner 元批评（「先用省事的临时办法糊弄」）与 v1 评审的教训一致：第一轮把最大
单波（continuity 全目录重读）推迟为「后续候选」；v1 评审稿又出现三处同类问题——
把单槽 ring 读数当判定性证据（B2）、把「曾见过订阅」当成唯一需要重置基线的
场景（B3）、把缓存说成「最大单波根治/条目随当前文件数有界」（B4）。v2 的处置
原则：**每个断言标注证据等级；每个修复写明效果边界；owner 门不越权**。

## 9. 阻断项 → 处置映射（供复审）

| 评审项 | 处置 | 位置 |
| --- | --- | --- |
| B1 来源清单 | 补全：诊断时 HEAD / 各 commit / 修复 4 dirty overlay / 部署产物 / iOS 仓完整身份 | §1 |
| B2 ring 误读 | 撤回 committed/released 断言；gcController 证据保留并标注等级；正确方法一致读取（新进程）；新增 ReadMemStats 遥测 | §3.2、§4 新增节 |
| B3 enrollment 漏洞 | 禁用分支无条件重置 startedAt/states；生产形状测试改造 + 新增 enrollment 回归测试（含进行中-turn 规则固定） | 修复 2 |
| B4 cache 生命周期/冷波 | 容量 4096 + FIFO 逐出；结论改为「重复扫描收敛」；cold/warm 分开表述；新增同尺寸 mtime/删除重建/并发 miss/容量逐出测试 | 修复 4（未提交） |
| B5 owner 门 | `webPushExpirySemanticsProven` 回退 false；注释改为真实授权状态；owner 决定点显式列出 | 修复 3 |
| N1 alias 契约 | defensive copy；测试改读盘计数 | 修复 4（未提交） |
| N2 GOMEMLIMIT 措辞 | 按 garbage.go 语义重写代码注释与本稿；512MiB 标 provisional | 修复 1、§6 |
