# bridge runtime memory footprint 评审报告

评审对象：`docs/2026-09-19-bridge-runtime-memory-footprint-review.md`（commit `685646d`）及其引用的已提交修复 `f7cd91d`、未提交 continuity cache。

结论：**不通过，需修订后复审。** 当前有 5 个阻断项：来源清单不满足 P0、Go runtime 统计解释错误、watcher 首次订阅基线实现与文档不符、continuity cache 不能支撑“最大单波根治/条目有界”的结论、410 翻转越过了明确 owner gate。修复 4 不应按现状提交；已部署的修复 1–3 中，watcher 与 owner gate 也需要处置。

## 1. 本次评审来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=685646d317205a86b865af07010e2cf0eb78446b
未提交状态=agent/claudecode/continuation.go（修改）；agent/claudecode/continuation_test.go（未跟踪）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 5ac43b897e2a9333bd191363ea83d68447d70550
配套仓库未提交状态=干净
预期产品特性=runtime 默认内存软限额；无订阅时 watcher 不扫描；404/410 清理死订阅；continuity 重复扫描收敛
```

iOS 无协议或源码改动，本次只核对配套工作树身份并读取其指令；所有代码结论均归属于上面的 Mac 工作树。Go runtime 语义核对使用本机目标版本源码：
`~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.6.darwin-arm64/src/runtime/`。

## 2. 阻断项

### B1 — 文档自己的来源清单不满足 P0

原文 §1 写的是 `提交=f7cd91d`，但评审稿实际位于 `685646d317205a86b865af07010e2cf0eb78446b`；未提交修复 4 也不是文中写的笼统 `bd4a2ce..工作树`，而是叠加在 `685646d` 上的两条明确路径。原文又以“配套仓库=无 iOS 侧改动”替代了配套 iOS 的绝对路径、分支、完整提交和状态。

这不是排版问题。现行 P0 明确规定评审文档缺任一仓库的路径 + 分支 + 完整提交时不得判定通过。应把上节来源清单原样级别的信息补回原文，并区分：诊断时旧二进制来源、修复 1–3 的提交、评审时源码 HEAD、修复 4 的 dirty overlay、当前部署产物身份。

### B2 — `heapStats` 单槽被误读，根因的“判定性证据”不成立

原文 §3.2 把 `runtime.memstats.heapStats` 的“当前代”单槽解释为：

- `committed=51,904,512` ⇒ runtime 当前实际持有约 49.5MB；
- `released=4,146,593,792` ⇒ scavenger 累计归还 3.86GB。

Go 1.26.6 源码与此相反：

- `runtime/mstats.go:667-676` 明确把槽类型命名为 `heapStatsDelta`，这些字段都是 delta；
- `runtime/mstats.go:716-745` 明确要求 reader 旋转三代并 merge，单独读取任一 ring slot 不是一致的全局快照；
- `runtime/mstats.go:450-476` 只有 **after aggregation** 的 `released/committed` 才能与全局计数对应；
- `runtime/mstats.go:168-172` 的 `HeapReleased` 是“已归还且尚未重新获取”的**当前量**，不是累计释放量。

因此，`committed≈49.5MB` 和“累计已归还 3.86GB”都不能从现有 LLDB 读法推出。`gcController.heapLive≈20MB` 仍是有价值的证据，足以反驳“存在 2.5GB 可达 Go heap 对象”，但不能排除未聚合的 runtime retained memory、非 Go 内存、mmap、kernel accounting，也不能证明 2.4GB swapped 页已经全部 `MADV_FREE_REUSABLE`。

原文 §3.4 的结论必须降级为假设。复核应在目标进程内通过 `runtime.ReadMemStats` 或 `runtime/metrics` 取得一致快照，至少记录 `Sys`、`HeapSys`、`HeapInuse`、`HeapIdle`、`HeapReleased` 及 `Sys-HeapReleased`，再与同一时刻的 `vmmap`/footprint 对齐。没有这一步，不能把“macOS 压缩器扣住已归还页”写成已证实根因。

### B3 — watcher 首次订阅基线存在实现漏洞

文档声称“无订阅直接 return；重新启用后的首轮以当下重基线，不回放历史”。已提交实现只在 `subscriptionsObserved == true` 时清空 `startedAt`（`go-bridge/claude_web_push_watcher.go:74-79`），但生产构造器一开始就把 `startedAt` 设为进程启动时刻（`:47`）。

于是这一序列不会按文档工作：

1. runtime 启动时没有订阅；
2. 多轮 disabled sweep 因 `subscriptionsObserved == false` 保留旧 `startedAt`；
3. 数小时后首次注册订阅；
4. 首次可见 cut 仍以 runtime 启动时刻为界，而不是 enrollment 时刻。

后续 transcript 再增长时，订阅前但进程启动后的记录可能被重新消费，形成历史通知。现有 `TestClaudeWebPushWatcherSkipsCatalogRefreshWithoutSubscriptions` 在 `:322` 手工构造了 `startedAt` 为零的 watcher，正好绕开生产构造形状，未覆盖该缺陷。

需要先修实现并增加“非零 startedAt + 启动时零订阅 + 后注册订阅”的回归测试，再讨论 §7 Q4 的产品语义。期望语义本身合理：订阅建立前已完成的 turn 不通知；订阅建立前已开始、建立后才完成的 turn 是否通知，应由 enrollment cut 的明确规则和测试固定，不能由陈旧的进程启动时间偶然决定。

### B4 — continuity cache 只消除重复热扫描，不是“最大单波根治”，且生命周期无界

`InspectTranscriptContinuity` 的 cache miss 仍会读取同样的 512KiB head/tail。进程冷启动后的第一次 catalog/rich-history 扫描仍要遍历项目目录全部 JSONL，因此原文 §6 的“最大瞬态波消除”不成立；当前实现消除的是**同一进程内未变化文件的重复扫描**。

同时，`sync.Map` 只 `Store`、从不 `Delete`，也没有容量或目录代际上限。path 单键只避免“同一路径每个指纹一条”，不能推出“条目数 = 当前文件数”。删除、重命名、项目迁移或曾访问过的其他 `CLAUDE_CONFIG_DIR` 路径都会永久留在进程内。对一个以长期内存稳定为目标的修复，这个生命周期缺口本身是阻断项。

修复 4 至少应满足：

1. 明确定义 bounded eviction/pruning（例如按目录扫描代际清理消失路径，或有严格容量的 LRU）；
2. 把结论改为“重复扫描收敛”，除非另有 cold-path 设计真正消除首轮全目录读取；
3. 用分配/读取计数或 benchmark 证明 warm hit 的收益，并单独报告 cold miss；
4. 增加同尺寸但 mtime 变化、删除后重建/path reuse、并发 miss 的覆盖。

sidecar 不进入 continuity 指纹是安全的：compact boundary、custom title 和 created timestamp 都来自 JSONL 正文。但这不等于与 catalog 的完整 fingerprint “完全等价”；catalog 还包含 sidecar 与 Desktop state，它们服务的是不同缓存内容。

### B5 — 410 默认翻转越过明确 owner gate，代码注释还写成了相反事实

`f7cd91d^:go-bridge/web_push_dispatcher.go:63-65` 明确要求 WP-RESP-2 归档后“由 owner 显式置 true”。本次没有获得这句授权；当前实现注释却写成“owner 于 2026-09-19 …置 true”（当前文件 `:63-68`），与原文 §4 自己承认的事实冲突。

技术证据是充分偏向翻转的：数据目录中有 6 条 WP-RESP-2 归档，旋转日志也能独立找到三个 subscription prefix 的大量真实 410；404/410 清理状态机已有正反测试。**技术建议是 owner 追认翻转**，但评审 agent 不能代替 owner 完成显式授权。程序状态应二选一：

- owner 在当前任务明确追认，则保留 `true` 并把代码注释改成真实的授权记录；
- 未追认前回退为 `false`，不能用“owner 在场提出了内存问题”替代授权。

## 3. 非阻断但必须修正

### N1 — 不要把共享 `BoundaryIDs` 底层数组做成缓存契约

当前两个调用点确实都不修改：`resolveClaudeContinuationPaths` 只读，catalog 在持久化到 snapshot 前复制。但 `InspectTranscriptContinuity` 是跨包 exported API，直接返回 cache 内的可变 slice；未来任何调用者原地排序/修改都能污染全局 cache 或制造 data race。

现有测试用指针相等证明命中，反而把危险的 alias 固化成契约。命中应返回 defensive copy；“未读盘”应通过可注入 reader/scan hook 或包内计数验证。Boundary ID 数量很小，这点 slice copy 不会重新制造 transcript 解析波。

### N2 — GOMEMLIMIT 512MiB 可作为代码默认，但文档不能把它写成 footprint 上限

只依赖环境变量不能保护普通 GUI 用户，因此“代码默认 + 环境覆盖”比“仅 env 配置”正确。512MiB 相对当前 `heapLive≈20MB` 有足够余量，可暂时保留，但在真实大历史 cold/warm 路径上没有 GC CPU、延迟和 `Sys-HeapReleased` 数据前，只能标为 provisional。

Go 1.26.6 `runtime/debug/garbage.go:181-211` 明确说明这是 runtime-managed memory 的软限额，排除 OS 代持、C 内存和 `syscall.Mmap`。所以“波峰钉在 512MiB”“用户看到低几百 MB”“软上限不会 OOM”都过强。准确表述应是：runtime 会提高 GC/归还力度以尝试维持 `MemStats.Sys - HeapReleased`，进程 footprint 仍可能超过该值，过低时可能接近持续 GC，系统级 OOM 也并未被排除。

## 4. 对 §7 六个问题的裁决

| 问题 | 裁决 |
| --- | --- |
| Q1 GOMEMLIMIT | 保留代码级默认、env 可覆盖；512MiB 暂定可接受，但必须补真实 cold/warm 负载数据，并修正文档中的“硬钉住/不会 OOM”措辞。 |
| Q2 cache 设计 | path 单键 + `(size,mtime)` 校验方向正确，sidecar 缺席安全；当前实现因无 eviction、冷波仍在、共享 slice 和测试缺口而不通过。 |
| Q3 owner 门 | 技术上建议追认；程序上在 owner 明确追认前应回退。评审不能代替 owner 授权。 |
| Q4 watcher 语义 | “enrollment 前完成不回放”合理；当前实现没有实现该语义，先修 B3 再验。 |
| Q5 压缩器解释 | 证据不足；`heapStats` 误读后只能列为候选解释，不能定论。先取一致 runtime 指标，再决定是否需要 XNU 级实验。 |
| Q6 遗留项 | 优先级：一致内存遥测 > B3/B4 修复 > 真实负载复测。91 次重连另案按时间线查；pprof 不应先于低暴露面的 runtime metrics；有订阅时单个变化文件的 3s 有界扫描暂不优先。 |

## 5. 独立核验记录

- `GOSUMDB=sum.golang.org go test ./agent/claudecode -run '^TestInspectTranscriptContinuity' -count=1`：通过，`0.010s`。这只证明现有两条测试绿，不覆盖上述生命周期与 cold-path 问题。
- 当前 `/Applications` runtime：PID `91090`，启动时间 `2026-09-19 22:15:27`，8777 listener 来自 `/Applications/CordCodeLink.app/.../cordcode-bridge-runtime`，`-version` 为 `f7cd91da3145`；确实不含未提交修复 4。
- WP-RESP-2：数据目录文件 6 行；`go-bridge.log.1/.2` 可独立找到三个 subscription prefix 的大量 `status=410` 记录。
- 未运行 UI test、snapshot test、模拟器/真机自动化、Release 构建或覆盖安装；本次产物仅为 D0 评审报告。

## 6. 复审准入条件

1. 修正原文来源清单和内存证据等级；删除或重写所有依赖错误 `heapStats` 解释的断言。
2. 用一致 runtime 指标重新验证 retained/released/footprint 关系。
3. 修复 watcher 初次 enrollment 基线并补生产构造形状测试。
4. 将 continuity cache 做成有界生命周期、defensive-copy API，并诚实区分 cold 与 warm 效果。
5. owner 对 410 翻转明确追认，或在追认前回退。
6. 定向测试通过后，再决定是否提交修复 4 与重新部署；不得用当前启动 RSS 代替长期/重负载验证。
