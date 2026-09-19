# bridge runtime memory footprint 复审报告（Round 2）

复审对象：修订提交 `bc328dfd41099fb5369c4d905394a9d90f7896a6`、`docs/2026-09-19-bridge-runtime-memory-footprint-review.md` v2，以及工作树中未提交的 continuity cache v2。

结论：**仍不通过，需完成 4 个阻断项后复审。** Round 1 的 ring 误读、GOMEMLIMIT 过强措辞、首次无订阅时的历史回放漏洞和 owner gate 越权均已实质纠正；新增 `runtime.ReadMemStats` 遥测的方向正确。但 v2 来源仍有占位符、cache 对并发 miss 的承诺与实现相反、enrollment 测试没有覆盖文档宣告的“订阅时进行中 turn”，且长期文档仍保留已撤回的压缩器定论。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=bc328dfd41099fb5369c4d905394a9d90f7896a6
未提交状态=agent/claudecode/continuation.go（修改）；agent/claudecode/continuation_test.go（未跟踪）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 5ac43b897e2a9333bd191363ea83d68447d70550
配套仓库未提交状态=干净
预期产品特性=内存一致遥测；无订阅 watcher 门控；enrollment 不回放；有界 continuity warm cache；404/410 过期订阅清理待裁决
当前部署=/Applications/CordCodeLink.app，runtime commit f7cd91da3145，PID 91090；不含 bc328df 与未提交修复 4
```

## 2. Round 1 处置核验

| Round 1 项 | 复审结论 |
| --- | --- |
| B2 ring 单槽误读 | 已纠正。错误数值和累计语义已撤回；`runtime.ReadMemStats` 是正确持久化入口。旧进程根因仍保持候选而非定论。 |
| B3 启动时无订阅、稍后首订 | 历史完成回放漏洞已修：disabled sweep 无条件清空 state/startedAt。新增测试覆盖了该形状，但对“enrollment 时已进行中 turn”的宣告仍有新缺口，见 R2-B3。 |
| B4/N1 cache 生命周期与 alias | 4096 容量、FIFO 和 defensive copy 已实现；cold/warm 边界表述明显改善。但并发 miss 仍会重复扫描，见 R2-B2。 |
| B5 owner gate | 已正确回退为 false，注释不再伪称授权。404/410 技术语义裁决见 §4。 |
| N2 GOMEMLIMIT | 代码注释和 v2 主文已按 Go 1.26.6 软限额语义修正。 |

## 3. 阻断项

### R2-B1 — P0 来源清单仍含 `<本 commit>` 占位符

v2 §1 的 `v2 修订提交=<本 commit>` 不是精确提交身份，§9 却把 B1 标为已处置。当前被评审的代码来源是 `bc328dfd41099fb5369c4d905394a9d90f7896a6`，应在一个后续 docs-only 提交中把代码来源明确写为该哈希；文档自身的新提交哈希无需自引用。

### R2-B2 — cache 声称“每指纹一次”，实现却允许并发扫描风暴

`InspectTranscriptContinuity` 在 `get` miss 后立即解锁并读文件（`continuation.go:128-150`）。同一路径、同一指纹的并发请求都能穿过 miss，重复读取和 JSON 解析；`TestInspectTranscriptContinuityConcurrentMiss` 甚至明确接受 `reads() >= 1`，没有要求等于 1。

这与代码注释和 v2 文档的“cold miss，每指纹一次”相冲突。更重要的是，本修复的目标正是压低分配波；catalog refresh 与多个 history 请求并发时，stampede 会按并发数放大同一批 head/tail 解析。

需要按 `path + size + mtime` 合并 in-flight scan（singleflight 或等价的 per-key pending 机制），并把并发测试收紧为同一指纹只发生一次真实读。不能通过持有全局 mutex 完成磁盘扫描来修，因为那会把不同 transcript 的 cold seed 串行化。

### R2-B3 — enrollment 回归测试未覆盖它宣称的“订阅时进行中 turn”

新增测试先完成 enrollment sweep，随后一次性 append user + terminal assistant 两行。它证明的是“订阅后新开始并完成的 turn 会通知”，不是“订阅建立时已经进行中、订阅后才完成的 turn 会通知”。

当前实现把 re-enable 的 `startedAt` 设为首次 enabled sweep 的当前时刻；任何在该 sweep 前已经写入 transcript 的 user/start 行，其时间戳都早于 cut，会被 baseline 跳过。也就是说，文档固定的产品规则目前既未测试，按现有算法也不成立。

必须二选一并保持实现、测试、文档一致：

1. 若产品规则仍是“enrollment 时进行中的 turn 完成后通知”，测试必须在 enrollment sweep 前写入未完成 turn，sweep 后只追加 terminal 行；实现需要能保留这个未完成尾 turn，不能仅靠 enrollment 当前时间切割。
2. 若产品规则改成“首次 enabled sweep 后开始的 turn 才通知”，则明确接受 enrollment 窗口内进行中 turn 不通知，并删除 v2 中相反的承诺。

Round 1 问题是历史回放；不能用修复历史回放为由，顺手声明一个实现并不具备的 live-turn 保留语义。

### R2-B4 — 长期文档仍把已降级假设写成定论

`think.md` 本节标题仍是“不是泄漏，是 macOS 压缩器扣住的死页”，而正文已经正确降级为候选解释；同节末尾仍把 continuity cache 称为“最大单波”候选并继续列 pprof，和 v2 的 warm-only、metrics-first 裁决冲突。CHANGELOG 也应避免在同一句先把 swapped 页直接定性为瞬态波死页，再在括号里降级机制解释。

这些是长期维护真值，不是无害标题。应统一为：已证明“大 footprint 不是 2.5GB 可达 Go heap”；swapped/retained 页的具体状态及 compressor 机制仍待一致遥测与 vmmap 对齐。

## 4. 404/410 清理裁决

**语义裁决：追认。** 本轮 owner 明确把此项交由评审决定；三个 Apple subscription prefix 的长期真实 410、WP-RESP-2 归档和 404/410 状态机正反测试足以支持“404/410 表示该 subscription 不再可用，应删除”。本报告可作为该产品语义的授权记录。

但不应立刻只改 `false → true`。启用前还要修一个真实失败分支：

- `WebPushStore.MarkSubscriptionExpired` 先删除内存记录再持久化，persist 失败不恢复旧记录（`web_push_store.go:399-406`）；
- dispatcher 即使收到该错误，仍写 `expired` ledger（`web_push_dispatcher.go:335-339`）。

结果是磁盘失败时当前进程假装已清理，重启后订阅从旧文件复活，ledger 还声称 expired。这违反“真实路径失败必须暴露”的规则。启用 gate 前应让删除具备 rollback，并且只有持久化成功才记录 `expired`；失败保留 subscription，记录/暴露 storage failure，并增加定向测试。

因此裁决分为两层：**owner/产品语义已追认；production gate 在事务失败路径修好后置 true。**

## 5. 非阻断修正

- 新增 `memory` 响应字段尚无直接断言。应在 management API 定向测试中验证 `memory` 对象及核心键存在、`sysMinusHeapReleased == sys - heapReleased`；这也能防止未来 JSON shape 漂移。
- `claudeContinuityFileReads` 注释称其为“线上效果观测探针”，但当前既未导出也未接入 diagnostics；若只供测试，应删除该表述，避免把不可观察的计数写成生产能力。
- FIFO `order = order[1:]` 建议在逐出前清空首元素或改为 ring queue，及时释放 path 引用；当前容量仍有硬上限，因此不是阻断项。

## 6. 独立验证

- `GOSUMDB=sum.golang.org go test ./agent/claudecode -run '^TestInspectTranscriptContinuity' -count=1`：通过，`0.014s`。
- `GOSUMDB=sum.golang.org go test ./go-bridge -run '^(TestClaudeWebPushWatcher|TestDispatcher404|TestApplyDefaultMemoryLimit)' -count=1`：通过，`0.417s`。
- `GOSUMDB=sum.golang.org go test -race ./agent/claudecode -run '^TestInspectTranscriptContinuity' -count=1`：通过，测试执行 `1.021s`。race 通过只证明同步访问无 data race，不证明并发 miss 被合并。
- `git diff --check`：通过。
- 未运行 UI/snapshot test、模拟器/真机自动化、Release 构建、安装或重启；当前 `/Applications` 仍运行 `f7cd91d`，与 v2 文档一致。

## 7. 下一轮准入条件

1. 修复来源占位符和 `think.md`/CHANGELOG 的残留定论。
2. continuity cache 合并同指纹并发 miss，测试断言真实读次数严格为 1。
3. 对 enrollment 进行中 turn 做明确产品取舍，并让实现与真实时序测试一致。
4. 修复过期订阅删除的持久化 rollback/ledger 诚实性；随后按本轮追认把 gate 置 true。
5. 为 management `memory` shape 增加直接测试。
6. 上述定向测试通过后再提交修复 4、构建和部署；部署后按 v2 计划采集一致内存指标与 footprint。
