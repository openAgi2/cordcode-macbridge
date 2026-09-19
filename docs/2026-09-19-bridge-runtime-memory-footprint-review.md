# 2026-09-19 bridge runtime「内存 2G+」诊断与修复（评审稿）

> 状态：**待评审**。修复 1–3 已提交（`f7cd91d`）并部署到 `/Applications` 运行中；
> 修复 4（continuity cache）已实现、测试绿，**按 owner 指示保持未提交**（工作树
> `agent/claudecode/continuation.go` 修改 + `continuation_test.go` 新增），等评审结论。
> 本文档同时记录 owner 的元批评（"先用省事的临时办法糊弄"）与响应——见 §8。

## 1. 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=f7cd91d（修复 1–3）；评审中的修复 4 为工作树未提交改动（bd4a2ce..工作树）
未提交状态=agent/claudecode/continuation.go（修改）+ agent/claudecode/continuation_test.go（新增）
任务预期分支=feat/ios-native-message-timeline（诊断对象为该分支族构建的生产二进制）
配套仓库=无 iOS 侧改动（无协议变更）
预期产品特性=启动日志 "default memory limit applied"；无订阅时 watcher 不再 3s 扫描；410 死订阅删除
```

诊断时运行中的生产二进制身份（已用 `-version` 核对）：

```text
/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime
commit: 87b32a1d9556（2026-09-17T15:36:17Z 构建，go1.26.6）
```

`87b32a1d` 是 `feat/ios-native-message-timeline` 的祖先；`git diff 87b32a1d..bd4a2ce --
go-bridge core agent` 为空（诊断时二进制与工作树源码零差异）。当前 `/Applications`
运行的是 `f7cd91d` 构建（PID 91090，2026-09-19 22:15:27 启动，已按部署后验证门核对）。

## 2. 问题现象

- owner 观察到 `cordcode-bridge-runtime` 活动监视器内存 **2G 多**。
- 实测：physical footprint **2.5G**（峰值 **3.8G**），进程自 2026-09-18 18:41 起运行
  26h50m，累计 153 CPU 分钟（均值 ~9.4% 单核）。
- 系统级背景：整机 swap 已用 **14.7G/15G**——内存严重超卖。

## 3. 诊断证据链

### 3.1 原始观测（vmmap / 日志 / 采样，非源码推断）

| 观测 | 数值 |
| --- | --- |
| Physical footprint | 2.5G（1 分钟内平稳 2.4–2.5G）；峰值 3.8G |
| RESIDENT / DIRTY | ~200M / ~59M |
| **SWAPPED（被 macOS 压缩器扣住的脏页）** | **2.4G**，其中 9 个 128MB Go 堆 arena **整块换出、0K 常驻**（完全冰冷） |
| Untagged（Go 堆）区域 | 5.2G 虚拟 / 543 region |
| goroutine 数 | 264（正常，无泄漏形态） |
| 27h 内 hydrate 的 session | 仅 7 个（19 次 commit）；本进程只写了 **2.4MB** checkpoint |
| CPU 采样主帧 | `scanClaudeSessionMetadata → InspectTranscriptContinuity → scanClaudeContinuityWindow → json.Unmarshal`（占采样大头）；次帧 `claudeWebPushWatcher.sweep/run` |
| 轮询量（27h） | claude 1615 次、codex-remote 1385 ok + 229 超时（**p50 9.5s**）、grokbuild 5283、opencode-web 1705、dsh-web 1611 |
| iOS 连接 | connectionGeneration=91（27h 内 91 次重连） |
| web-push | 3 个 Apple 端点持续 **410 Gone**（日志 84 次观测，跨 01:04→22:04） |
| 磁盘数据量 | `~/.claude/projects` 640MB / 619 个 jsonl；`~/.grok/sessions` 900MB |

### 3.2 Go runtime 内部读取（LLDB 只读直读，判定性证据）

二进制带符号表（`-trimpath` 不去符号）。`nm` 定位 `runtime.gcController`
（Go 1.26 布局：`gcPercent`@+0、`heapLive`@+104、`heapMarked`@+152），ASLR 基址取
vmmap Load Address，`lldb -p <pid> --batch -o "memory read -s 8 -f u -c 20 <addr>" -o detach`。

| 字段 | 值 | 判读 |
| --- | --- | --- |
| `gcPercent` | 100 | 校验位 ✓（默认 GOGC） |
| `memoryLimit` | MaxInt64 | 校验位 ✓（GOMEMLIMIT 未设） |
| `heapMinimum` | 4MB | 校验位 ✓ |
| **`heapLive`** | **20,774,576（~20MB）** | **活堆只有 20MB** |
| `heapMarked` | 18,160,712 | 上次 GC 存活 ~17MB |
| `gcPercentHeapGoal` | 36,825,354 | 下次 GC 目标 ~35MB |

`runtime.memstats.heapStats`（三代环形，delta 1168B）当前代：

| 字段 | 值 | 判读 |
| --- | --- | --- |
| `committed` | 51,904,512 | runtime 当前实际持有 **~49.5MB**（含 idle span） |
| **`released`（累计）** | **4,146,593,792** | scavenger **累计已归还 3.86GB** |
| `inHeap` / `inStacks` | 34MB / 2.2MB | 与 heapLive 相符 |

### 3.3 应用层缓存逐一排除（源码审计，均无 GB 级持有）

- 投影 Kernel：27h 仅 2.4MB checkpoint；`sessions map` 无逐出但本进程只 hydrate 过 7 个 session。
- LiveFrameBuffer：硬上限（200 帧 / 1MB / device-session / 60s）。
- claude catalog：快照只存元数据 + (mtime,size,sidecar) 指纹缓存，未变化文件不重读。
- web-push watcher：preview accumulator 512B 封顶；`projectionJSONLStartCut` 只读尾部。
- grok 全局订阅器：每文件只存 offset + 小 codec 状态。
- `grokUpdateState`、detail cache（21MB 磁盘）、web-push 账本（123KB）：均小。

### 3.4 根因结论

**不是活数据泄漏**：活堆 ~20MB、committed ~49.5MB、累计已归还 3.86GB。用户看到的
2.5G 是**瞬态分配波触碰过的死页**：GC 已回收、scavenger 已 madvise（Darwin 用
`MADV_FREE_REUSABLE`，`runtime/mem_darwin.go`），但整机内存压力下 macOS 压缩器把这些
页**压缩扣住而非丢弃**，physical footprint 长期挂在波峰值，直到进程退出。

> 诚实边界：「压缩器扣住已归还页」是对实测事实（活堆极小 + 归还量大 + swapped 大 +
> 1 分钟平稳）的最一致解释；XNU 内部对 MADV_FREE_REUSABLE 页的具体处置未从内核源码
> 逐行验证，评审可挑战此点（见 §7 Q5）。

**分配波源头**（按大小排）：

1. **`resolveClaudeContinuationPaths`（最大单波）**：每次 claude 历史加载都重读项目目录
   里**全部** jsonl 的 512KiB 头+尾窗口（619 个文件 ≈ 数百 MB 读+解析）。调用方：
   `loadClaudeContinuationHistory` / `richHistoryTranscriptSegments`（每次 rich history）。
2. **`claudeWebPushWatcher` 每 3s 无条件驱动 catalog refresh**：refresh 在
   `SubscriptionCount()` 检查**之前**执行；活跃 session 指纹每轮都变 → 每 3s 重解析
   （512KiB 元数据 + 1MiB 连续性窗口/变化文件）。这也是 CPU 主源。
3. iOS 27h 重连 91 次，每次重连重拉全量投影快照（大 session 单次序列化几十 MB）。
4. codex-remote 轮询 p50 9.5s 持续超时重试（上游慢，已有退避）。

## 4. 修复方案

### 修复 1：runtime 默认 GOMEMLIMIT 512MiB（`go-bridge/main.go`）【定位：安全网，非根治】

`applyDefaultMemoryLimit()`：`GOMEMLIMIT` env 未设置时 `debug.SetMemoryLimit(512<<20)`，
启动日志输出 `default memory limit applied limitBytes=536870912`（兼作部署后新版本
特征输出）。软上限不会 OOM——真需要更多时 GC 加频而非失败。

**定位说明（回应 owner 元批评）**：这是把用户可见内存从"数 G"钉到"低几百 MB"的
安全网；它不消除波本身。波本身的根治是修复 2/3/4。二者是互补关系，不是替代。

### 修复 2：watcher 无订阅时跳过 catalog refresh（`claude_web_push_watcher.go`）【根治：常驻波】

`sweep()` 把 `enabled := SubscriptionCount() > 0` 提到 `refresh(nil)` **之前**；无订阅
直接 return（不再每 3s 全量 stat + 重解析）。订阅消失时清空 states 并把 `startedAt`
置零；重新启用后的首轮 sweep 以当下重基线，不回放历史。

实现注意（评审点）：首轮 sweep 若 `startedAt` **非零必须尊重预设值**——
`TestClaudeWebPushWatcherFirstVisibleRetainsLiveCompletion` 曾因无条件覆盖而回归
（"启动前已在进行的 turn 完成后要通知"的语义依赖预设 startedAt）。

### 修复 3：WP-RESP-2 归档 + 翻转 `webPushExpirySemanticsProven`（`web_push_dispatcher.go`）【根治：死订阅】

- 日志中 3 个 Apple 端点（`wps_864da9c5` / `wps_8f2b69d2` / `wps_0f550fc9`）84 次
  410 观测已归档为数据目录 `web-push-samples/WP-RESP-2.jsonl`（每端点最早+最新各一条，
  共 6 条），与 `docs/2026-09-12-remote-web-push-badge-and-collapse-plan.md` §9 的预言
  一致。
- `webPushExpirySemanticsProven` 置 true：404/410 → `MarkSubscriptionExpired` 删除。
  死订阅不删则 `SubscriptionCount()` 恒 > 0，修复 2 的门控永不生效——两项是配套的。
- 404/410 分支补上 `webPushCaptureResponse`（此前该路径**不写样本文件**，证据门永远
  无法自满足，只能靠 runtime 日志手工归档）。
- 旧门控行为保留在 `TestDispatcher404PreSampleDoesNotDelete`（显式钉 `false`）防回退断裂。

**评审点（owner 门）**：原注释要求"由 owner 显式置 true"。本次由 agent 依据方案文档
§9 写明的翻转条件（"WP-RESP-2 真实样本归档后翻转该常量"）+ 样本已归档 + owner 在场
提出内存问题而执行，**未获得 owner 逐字确认**。评审请裁决：追认或回退。

### 修复 4：continuity 检查按指纹缓存（`agent/claudecode/continuation.go`）【根治：最大单波】**——未提交，待评审**

`InspectTranscriptContinuity` 增加 path 单键缓存，`(size, mtime)` 指纹做校验值：

- **必须 path 单键、指纹只做校验值**：活跃 session 每 3s 追加即换指纹，按指纹做键
  会随时间无界增长（每轮 sweep 一条）；path 单键则条目数 = 文件数（619 → ~200KB）。
- 指纹语义与 go-bridge catalog 的 `claudeSessionFingerprint`（mtime+size+sidecar）一致
  ——sidecar 不参与 continuity（boundary 只在 jsonl 正文里），故不含 sidecar。
- 命中返回缓存里的同一份 `BoundaryIDs` 切片（共享底层数组）。调用方均不修改：
  go-bridge catalog 建条目时 `append([]string(nil), ...)` 拷贝；
  `resolveClaudeContinuationPaths` 只读。**评审请复核这两个调用点**。
- TOCTOU 自愈：stat 与读窗口之间文件再变 → 缓存存旧指纹+新内容 → 下次指纹不匹配
  重读，不会持久错。
- 效果：`resolveClaudeContinuationPaths` 的全目录扫描从"每次历史加载读全部文件头尾"
  变为"只读指纹变化的文件"；catalog 对变化文件的 continuity 窗口读取同样受益。

测试（`continuation_test.go`，绿）：
- `TestInspectTranscriptContinuityCachesUnchangedFile`：二次调用返回**同一底层数组**
  （`&first.BoundaryIDs[0] == &second.BoundaryIDs[0]`；first 存活期间地址不可能被
  回收复用，地址不同 = 真的重读）。
- `TestInspectTranscriptContinuityInvalidatesOnFingerprintChange`：追加新 boundary
  （size 变化）+ 显式 Chtimes 后能读到新 boundary。

## 5. 交付与验证状态

| 项 | 状态 |
| --- | --- |
| 修复 1–3 | commit `f7cd91d`；定向测试绿（watcher 全组 / dispatcher 404 两例 / memory limit 两例 / 扩大 web-push+catalog 组） |
| 修复 4 | 工作树未提交；`go build` 过；`TestInspectTranscriptContinuity*` 绿 |
| 部署 | `f7cd91d` Release 构建已覆盖安装 `/Applications`（**不含修复 4**） |
| 部署后验证 | 新 PID 91090（22:15:27，晚于构建）✓；特征行 `default memory limit applied` ✓；8777 由新 runtime 监听 ✓；启动 RSS 39MB ✓；启动初期 codex-remote 配对报错为已知瞬态，数秒后恢复 |
| CHANGELOG / think.md | 已随 `f7cd91d` 提交 |

构建环境坑（已记入 think.md）：本机 `GOSUMDB=off` + go.mod `toolchain go1.26.6` →
仓内任何 go 命令静默失败（仅一行 toolchain 警告），且 `go build | head` 管道吃退出码
造成**假成功**；构建需 `GOSUMDB=sum.golang.org`（保 1.26.6）或 `GOTOOLCHAIN=local`。

## 6. 修复后用户可见效果（预期）

- 波峰被钉在 ~512MiB + runtime 开销：重度使用（大 transcript 历史 + 整机内存紧张 +
  长期不重启）下用户看到**低几百 MB**，不再随使用时长累积到 GB 级。
- 无订阅期间零后台扫描（CPU 从 ~9% 常驻降到接近空闲）；死订阅在下次投递尝试时删除。
- 修复 4 合入后，历史加载不再重读全目录（最大瞬态波消除，CPU 同步下降）。
- 诚实边界：内存严重超卖的 Mac 上死页残留形态仍可能出现，但被钉在数百 MB 量级
  （而非 3.8G 峰值）；这是 macOS 压缩器行为，进程内无法完全消除，只能不制造波。

## 7. 留给评审的问题清单

1. **GOMEMLIMIT 默认值**：512MiB 是否合适？（活堆 ~20MB，最大单波估 ~300-500MB；
   过低会让大历史加载期间 GC 加频变慢。）代码级默认 vs 仅 env 配置，哪个是产品正解？
2. **修复 4 的缓存设计**：path 单键 + (size,mtime) 校验是否与 catalog 指纹语义
   完全等价（sidecar 缺席是否安全）？共享 `BoundaryIDs` 切片的两个调用点是否
   确认不修改？缓存条目随文件数有界（~200KB/619 文件）是否可接受？
3. **修复 3 的 owner 门**：`webPushExpirySemanticsProven` 由 agent 依方案文档条件
   翻转，追认或回退？
4. **watcher 门控语义**：订阅出现时刻之前的进行中 turn，其完成不通知（重基线语义）；
   订阅出现前 turn 已 live、完成后通知（FirstVisible 语义保留）。产品上可接受吗？
5. **「压缩器扣住已归还页」的机制解释**：实测事实链是否足以支撑？是否有更简单的
   解释（如 scavenger 未跑完 / idle span 未释放）？可设计复核实验。
6. **遗留项的取舍**：iOS 27h 重连 91 次（每次重拉投影，SSV2 冷打开 by design，
   疑似手机前后台生命周期正常行为，未深查）；Management API 挂 pprof heap 端点；
   变化文件每 3s 的元数据重解析（512KiB 上限，有界）是否值得再优化。

## 8. 元复盘：owner 批评的回应

owner 指出：「发现问题总是用最省事、临时的办法糊弄过去，用户总不能隔几天清一次
缓存重启一次 App」。

对照本次行为的诚实记录：第一轮响应确实先给了"重启 CordCodeLink 立即见效"，把三个
最便宜的修复（GOMEMLIMIT / 门控 / 410 清理）做掉后，把**最大的单波**（continuity
全目录重读）列为"后续候选"推迟——正是被批评的模式。owner 推回后才实施修复 4。
本评审稿把修复 1 明确标注为"安全网而非根治"，并把修复 4 置于待评审状态，供评审
检验"哪些是根治、哪些仍是权宜"。

