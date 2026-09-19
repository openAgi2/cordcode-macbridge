# 2026-09-20 内存治理后续评审报告（Round 1）

评审对象：`docs/2026-09-20-memory-followups.md`（提交 `7b8d6a2182efde45d9c128f0f0b84a5663583a5e`）及其引用的业务代码提交 `124b73d7b0f2155ed19207c3ec6fe00cdf5fa888`。

结论：**不通过，共 4 个阻断项。** 授权枚举零值硬化本身正确，Go/Swift 定向测试和当前部署身份也能独立复现；阻断集中在来源清单、内存首采的证据外推、不可执行的 1–2 天复采方案，以及 Mac UI 警告的真实恢复路径与 wire seam 测试。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=7b8d6a2182efde45d9c128f0f0b84a5663583a5e
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / aabbe6c0005c640bf3b2686059fdbf6c8c6a0eab
配套仓库未提交状态=已修改 OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift、OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift、OpenCodeiOS/OpenCodeiOSTests/NativeTimelineKeyboardContainerTests.swift（其他任务所有，本轮未读取其业务内容、未修改、未提交）
预期产品特性=WebPush 授权枚举零值拒绝；Mac 撤销设备时可见 pushCleanupError；有来源、代际和时间序列约束的内存对账
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit 124b73d7b0f2，built 2026-09-19T18:06:36Z
```

文档引用的 iOS 仓仅用于配套来源身份；本轮代码形状结论全部来自上述 Mac 工作树。评审开始时部署 PID 为 GUI 30081/runtime 30304；定向 Swift 测试结束后 supervisor 于 `02:14:15` 启动同一已安装版本的新 runtime PID 34681，当前仍由 `/Applications` 路径监听 8777，未发现临时 app/runtime 残留。此代际变化不用于证明原文的首采数据。

## 2. 已验证通过的部分

- `WebPushDeviceUnspecified` 已成为 `WebPushAuthorizationDecision` 零值；dispatcher 只有精确 `Active` 继续发送，零值进入默认拒绝分支且不删除订阅。零值回归测试验证 0 HTTP 请求、subscription 保留。
- Go 授权、双文件重载、撤销失败、自愈、revoke API 和 memory shape 定向测试通过；race 通过；`go vet ./go-bridge/...` 通过。
- `DeviceStoreTests` 8 条与 `WorkspaceViewTests` 13 条独立复跑通过，共 21 条。首次未覆盖 `GOSUMDB=off` 环境导致 build phase 失败；按仓内已知约束加 `GOSUMDB=sum.golang.org` 后通过。
- `/Applications` 产物版本、安装时间、启动特征日志和 8777 listener 与 §5 的 `124b73d` 部署身份一致。评审期间 runtime 重启后版本仍为 `124b73d7b0f2`。

## 3. 阻断项

### B1 — 来源清单明确复用了旧报告的 iOS 身份，违反本任务独立来源门

§1 把配套 iOS 固定为 `8983700d...`，并注明它是“r5/r6 报告记录的状态”。这不是本任务开始时重新生成的来源清单。时间线也直接否定该值：iOS 工作树在 `01:53–02:01` 已连续前进到 `aabbe6c0...`，而本后续业务提交发生在 `02:06:30`、文档提交发生在 `02:09:29`。因此 `8983700d...` 在本任务代码和文档提交时都不是配套工作树 HEAD。

同一清单还声称 `124b73d` 包含 `think.md`，但 `git show --name-only 124b73d` 的 9 个文件中没有 `think.md`，仓库内也找不到本次 14.6 分钟首采数字的 think.md 记录。

下一版必须写入本任务三个门点各自真实的 Mac/iOS 路径、分支、完整 HEAD 和 dirty paths，不能引用旧评审身份替代；同时把 `124b73d` 的文件范围改成真实内容，或另行提交并准确引用确实承载首采记录的文档提交。

### B2 — 单点快照被外推成动态行为、分配率和 512MiB 数值裁决

§2.1/§2.2 有四处证据等级越界：

1. `HeapReleased=68.3MB` 是“当前已归还且尚未重新获取”的 gauge，只证明采样瞬间存在 released pages，不能单凭一个点证明“scavenger 活跃归还”。要证明“活跃”至少需要同一进程多个时间点的变化，或对应 runtime trace/metrics。
2. `NumGC=4877` 与 uptime 可以推出平均 GC 完成频率约 5.6/s，但不能推出“单次代价小”。`HeapInuse` 是当前堆状态，不是 GC pause 或 CPU cost；需要 `PauseTotalNs`/pause distribution、`GCCPUFraction` 或等价 `runtime/metrics` 时间序列。
3. 当前 management memory shape 没有 `TotalAlloc` 或 alloc bytes counter。仅凭 `NumGC`、`HeapInuse` 和一个快照无法推出“分配率约 50–60MB/s”，更不能把它归因于 3s watcher sweep 与轮询。该数字在仓库中也没有原始采样或计算式。
4. 文档一面承认窗口不足、最终需长窗口复核 GOMEMLIMIT，另一面又写“512MiB 数值无需下调”。14.6 分钟且 `Sys-HeapReleased` 仅 23.7MB，只能支持“本窗口没有触及软限额”，不能裁决默认值是否合适。

下一版应撤回上述动态/因果/配置结论，或补同一 PID 的原始时间序列、明确计算式和所需指标。首采原始输出应归档或至少记录完整命令、时间、PID、bridge epoch、runtime commit、uptime、负载说明及单位；派生结论逐项标注“观测”“计算”“推断”。

### B3 — “1–2 天后复采”在默认 120 分钟自动重启下无法验证 27 小时累积

§2.2/§6 要求真实负载运行 1–2 天后再执行一次命令组，却没有代际连续性门。仓库活文档和生产实现都表明 `autoRestartEnabled` 默认 true、`autoRestartIntervalMinutes` 默认 120；因此 1–2 天后的 PID 通常最多运行约两小时。旧异常用了约 26h50m 才累积出来，用一个新代际快照与旧 27 小时代际对比，不能裁决长期增长、压缩器候选或 512MiB 默认值。

复采方案必须二选一并写完整：

- 受控长窗口：明确关闭周期重启的开始/恢复时刻与风险边界，持续记录同一 PID + bridge epoch；任何代际变化立即判该窗口失效并重新计时；或
- 多代际监测：以固定间隔自动归档每个代际的 uptime、PID/epoch、memory、vmmap、RSS 与负载计数，结论限定为“120 分钟代际内上界”，不得拿它回答 27 小时同进程累积问题。

此外，验收不能只写“footprint 回到数百 MB”。必须预先定义采样周期、最低同代际时长、可比负载指标、趋势/上界阈值和重启处理规则。`Sys-HeapReleased <= 512MiB` 只说明 Go runtime 管辖量未超过软限额附近，不自动证明“死页滞留由 GOMEMLIMIT + 波源治理压住”。

### B4 — UI 给出不存在的恢复动作，实际 wire decode seam 没有测试

§4 和双语文案都说用户“可重试撤销”。但 `ListDevices` 只返回未撤销设备；撤销成功后 `DeviceStore` 立即 reload，该设备从 Workspace 列表消失，用户没有再次点击“撤销”的入口。服务端 fan-out 会对 denied subscription 自动重试物理清理，但文案没有说明这个真实行为，反而给出不可执行动作。

同时，新增的两条 `DeviceStoreTests` 直接让 stub 返回已经解码好的 `DeviceRevocation`；它们没有覆盖唯一新增 wire seam——`ManagementAPIClient` 是否能从实际 200 JSON 响应解出 `pushCleanupError`。`WorkspaceViewTests` 只更新 protocol stub，也没有验证 alert 的绑定或文案。当前实现还用 `try?` 静默吞掉响应解码失败，使“撤销成功但提示丢失”与“确实没有 cleanup error”不可区分。

下一版至少需要：

1. 把文案改成真实可执行的恢复说明，例如明确投递已阻断、runtime 会在后续 fan-out 自动重试清理，并提供确实存在的日志入口；若产品真要“重试撤销”，先提供可达入口再写文案。
2. 增加 `ManagementAPIClient` 级 HTTP fixture 测试，覆盖带/不带 `pushCleanupError` 的 200 响应；并明确 malformed success body 的产品语义——可以保持 revoke 成功，但不能与“无清理错误”静默等价。
3. 给 UI 状态至少增加可测试的 presentation mapping/binding 断言，防止 ViewModel 有值但 alert 没有可达呈现。无需运行 UI test 或 snapshot test。

## 4. 独立验证记录

- `go test ./go-bridge -run '^(TestDispatcherAuthorizationMissingOrphanAndUnknown|TestWebPushDeviceAuthorizationDecisions|TestDispatcherDeviceAuthorizationFileStoreReload|TestDispatcherRevokedDeviceFailsClosedAndRetriesCleanup|TestMgmtRevokeDeviceWebPushCleanupFailureExposed|TestApplyDefaultMemoryLimit.*|TestMgmtRuntimeDiagnosticsMemoryShape)$' -count=1`：通过，`0.343s`。
- 同组授权/重载/revoke 核心路径 `-race`：通过，`1.356s`。
- `go vet ./go-bridge/...`：通过。
- `GOSUMDB=sum.golang.org xcodebuild ... test -only-testing:CordCodeLinkTests/DeviceStoreTests -only-testing:CordCodeLinkTests/WorkspaceViewTests`：21 条通过，测试本体 `0.111s`，命令约 `11.3s`。
- 当前生产 runtime 新代际 PID 34681：版本 `124b73d7b0f2`，8777 listener 正常，未发现非 `/Applications` runtime。独立瞬时 memory 为 `sys=96131352`、`heapReleased=73408512`、`sysMinusHeapReleased=22722840`；vmmap physical footprint `23.5M`、peak `70.8M`、TOTAL swapped `2432K`。这是评审时另一代际的约 1 分钟快照，只用于确认采集管线，不补强原文的长期结论。
- 未运行 UI/snapshot test、模拟器或真机自动化；未重新构建 Release、覆盖安装或部署。

## 5. 下一轮准入条件

1. 修正本任务真实来源清单及 `124b73d` 文件范围。
2. 撤回或用时间序列补证 scavenger active、50–60MB/s、单次 GC 代价小和 512MiB 无需下调四项断言。
3. 把默认 120 分钟自动重启纳入复采设计，增加 PID/epoch continuity、负载、采样周期、阈值和窗口失效规则。
4. 修正不可执行的“重试撤销”文案，补 actual ManagementAPIClient 200-response decode 测试及可测试的 UI presentation seam。
5. 保持零值授权、deny-by-default、存储回滚、真实双文件重载和部署身份验证全绿；业务代码变化后重新完成 Release 覆盖安装与运行态代际核验。
