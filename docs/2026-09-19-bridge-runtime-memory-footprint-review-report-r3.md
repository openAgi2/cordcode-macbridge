# bridge runtime memory footprint 复审报告（Round 3）

复审对象：代码提交 `ca4e56d8a34b890990cad7c41e2051fe4260ec4e`、评审稿 v3 提交 `f1ed656af43e9e52c2691aa9a882ae8e3f893a49`，以及已安装到 `/Applications/CordCodeLink.app` 的同提交 runtime。

结论：**仍不通过。Round 2 的四个阻断项、404/410 两层裁决和三个非阻断修正确已落地，但本轮发现 4 个阻断项。** 其中一个是发布证据未写回文档，一个是 v3 仍残留已降级的压缩器定论；另外两个是代码路径问题：设备撤销清理失败会使订阅在重启后复活，以及 enrollment 的 live-turn 回溯会物化整个 transcript，重新制造本次内存治理要消除的大分配波。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=f1ed656af43e9e52c2691aa9a882ae8e3f893a49
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 6e6080eff677c9d8dda19ff1bc6f898b7e8880d7
配套仓库未提交状态=干净
预期产品特性=512MiB runtime-managed 软限额；无订阅 watcher 门控；enrollment 不回放且保留进行中 turn；404/410 事务性清理；有界 continuity warm cache 与同指纹并发合并；一致内存遥测
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit f1ed656af43e，built 2026-09-19T15:27:18Z；GUI PID 33250，runtime PID 33343；8777 由该 runtime 监听
```

v3 成稿时记录的配套 iOS 快照是 `5ac43b897e2a9333bd191363ea83d68447d70550` 且存在一处非本任务 dirty 文件；该仓在 v3 成稿后于 23:27:36 前进到当前 `6e6080e...` 并变为干净。两者按时间区分，不把当前状态倒写成 v3 成稿时状态；本任务没有 iOS 代码声明或改动。

## 2. Round 2 处置核验

| Round 2 项 | Round 3 结论 |
| --- | --- |
| R2-B1 来源占位符 | 代码来源哈希已替换为真实 `ca4e56d...`；但部署发生在 v3 文档提交之后，最终部署来源门和运行态证据未写回，见 R3-B1。 |
| R2-B2 并发 miss | 已实现 `path + size + mtime` per-key flight；cache hit 与 flight 判定同锁，磁盘扫描不持全局锁，并发测试收紧为一次真实读，定向 race 通过。 |
| R2-B3 enrollment 进行中 turn | 真实时序测试正确覆盖“enrollment 前只有 user，之后只追加 terminal”；通知语义成立。但所复用的回溯 helper 有本任务相关的分配问题，见 R3-B3。 |
| R2-B4 长期文档定论 | `think.md` 与 CHANGELOG 已正确降级；v3 评审稿自己的原始观测表仍保留相反措辞，见 R3-B4。 |
| r2 §4 404/410 | `MarkSubscriptionExpired` 持久化失败会恢复内存记录；dispatcher 仅成功落盘后记 `expired`，失败记 `expiry_cleanup_failed`；gate 的授权链准确。 |
| r2 §5 非阻断 | management memory shape 测试、测试计数器注释和 FIFO 首槽清理均已完成。 |

## 3. 阻断项

### R3-B1 — v3 在部署前提交，却把部署写成已完成，且没有最终部署来源清单

v3 文档提交时间为 23:25:00；安装 runtime 的构建时间为 23:27:18，GUI/runtime 分别在 23:29:02/23:29:08 启动。也就是说，提交的 v3 不可能包含本次部署后现场。§5 却写“部署执行；部署身份与验证见下”，实际下文没有给出构建目录来源门、完整部署提交、PID、启动时间、listener 或特征日志。

本轮独立核验确认部署本身是真的：`-version` 为 `f1ed656af43e`，PID 33343 来自 `/Applications` 并监听 8777，日志出现 `default memory limit applied limitBytes=536870912`。问题是完成报告没有承载这些证据，违反 P0 对构建/安装/部署来源的持久记录要求。

需要在部署后 docs-only 修订中写入：构建工作目录、分支、完整提交、构建前 dirty 状态、产物路径/版本、安装时间、进程代际、8777 listener、独有特征日志；不要继续用“见下”指向不存在的证据。

### R3-B2 — `DeleteDevice` 失败不回滚，会让已撤销设备继续收到通知

`WebPushStore.DeleteDevice` 在 `web_push_store.go:326-338` 先删除 `byDeviceID` 和 badge state，再持久化 subscription；持久化失败时直接返回，没有恢复任何内存状态。磁盘仍保留旧 subscription，当前进程暂时看不到它，但重启后会重新加载。management 撤销路径已经先成功撤销 trusted device，再把这个清理错误降为 WARN（`management_api.go:813-823`）。

这不是普通残留：Apple endpoint 仍可能返回 2xx，因此“下次 404/410 会清除”的代码注释没有保证。结果是已撤销、已断开 WebSocket 的设备仍可能在 runtime 重启后继续收到包含标题/预览的 Web Push，违反撤销生命周期和隐私边界。

v3 §7 已把它列为待裁决项。本轮裁决：**发布阻断。** 应至少保存旧 subscription 与 badge state，subscription 持久化失败时恢复一致状态；更稳妥的顺序是先让 subscription 删除落盘，再做可独立失败、无投递权限影响的 badge 清理。增加 store 失败测试和 management revoke 路径测试，证明失败不会产生“内存已删、磁盘仍在”的假成功。顺带复核 `Unregister` 的同形 badge 顺序，但不要以扩大重构代替修复本阻断路径。

### R3-B3 — enrollment 回溯把整个 transcript 物化，重新引入大分配波

`claudeWebPushWatcher.consumeGrowth` 在首次 post-enrollment growth 时调用 `lastClaudeUserIdentityFromPath(path, offset)`。该 helper 用 `scanClaudeRelayEntriesFromReader(io.LimitReader(...))` 从文件头扫描到 baseline，并把全部 meaningful entry（连同 `json.RawMessage` 内容）累积到 slice，最后才倒序找最后一条 user。

这不是有界尾读。本机当前最大 Claude transcript 约 64MB，另有多个 37MB/14MB 文件；在大 transcript 的 live turn 于 enrollment 后完成时，这条新路径会为“只找最后一个 user identity”物化整段历史。它直接违背本次内存修复的目标，并可能抵消 continuity warm cache 避免的分配波。

应改为不累积历史记录的专用扫描：流式维护最后一个合格 user identity，保持 O(1) 条目内存；若采用反向尾扫，必须证明超长单 turn、interrupt/resume meta 与 compaction 边界下仍能找到正确 identity。新增大 transcript 测试或 allocation 上界断言，防止再次复用返回全量 slice 的 helper。

### R3-B4 — v3 原始观测表仍把压缩器候选写成事实

v3 §3.1 的表格仍写 `SWAPPED（被 macOS 压缩器扣住的脏页）`，而 §3.4、think.md 和 CHANGELOG 已正确说明“swapped/retained 页状态与压缩器机制待一致遥测定论”。`vmmap` 证明的是 swapped/dirty 形状，不证明“已归还页被 compressor 扣住”的机制。

应把表格改为中性原始观测，例如 `vmmap 报告的 SWAPPED/DIRTY 页`；压缩器只保留在候选解释中。否则同一份 v3 内部仍同时存在事实口径和候选口径。

## 4. 独立验证

- `GOSUMDB=sum.golang.org go test ./agent/claudecode -count=1`：通过，`2.445s`。
- `GOSUMDB=sum.golang.org go test ./go-bridge -run '^(TestClaudeWebPushWatcher|TestDispatcher404|TestWebPushStoreMarkExpired|TestApplyDefaultMemoryLimit|TestMgmtRuntimeDiagnosticsMemoryShape)' -count=1`：通过，`0.619s`。
- `GOSUMDB=sum.golang.org go test -race ./agent/claudecode -run '^TestInspectTranscriptContinuity' -count=3`：通过，测试执行 `1.051s`。
- `GOSUMDB=sum.golang.org go test -race ./go-bridge -run '^(TestClaudeWebPushWatcherEnrollmentRetainsLiveTurnCompletingAfter|TestDispatcher404CleanupFailureKeepsSubscriptionAndHonestLedger|TestWebPushStoreMarkExpiredRollsBackOnPersistFailure|TestMgmtRuntimeDiagnosticsMemoryShape)$' -count=1`：通过，测试执行 `1.246s`。
- 运行中 Management API `memory` 实测：`sys=100522264`、`heapReleased=77905920`、`sysMinusHeapReleased=22616344`，恒等式成立。该瞬时值只证明 endpoint/shape 正常，不作为真实负载效果定论。
- `git diff --check`：通过。
- 未运行 UI/snapshot test、模拟器/真机自动化；未重新构建、安装或重启。

## 5. 下一轮准入条件

1. 修复 `DeleteDevice` 持久化失败的一致性与撤销后通知风险，并增加失败路径测试。
2. 把 enrollment identity 回溯改为有界内存实现，增加大 transcript/分配边界回归证据。
3. 在部署后更新评审稿：补全真实部署来源与运行态证据，并清除 §3.1 的压缩器事实化措辞。
4. 保持当前已通过的 continuity、watcher、410 cleanup、memory shape 定向测试和 race 继续全绿。
5. 上述通过后无需再次部署本轮未变的旧二进制；若业务代码发生变化，则按来源门重新 Release 构建、覆盖安装并验证新代际。
