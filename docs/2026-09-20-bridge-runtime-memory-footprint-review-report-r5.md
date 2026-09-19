# bridge runtime memory footprint 复审报告（Round 5）

复审对象：代码提交 `3218cb51e8ec7d5f7910437cf6057ed73e9fa7cd`、评审稿 v5 提交 `12e83c366772d50253cc278792d463b9e6bef099`，以及已安装到 `/Applications/CordCodeLink.app` 的 `3218cb51e8ec` runtime。

结论：**仍不通过，剩余 1 个发布阻断项。** v5 已正确修复“明确查到 revoked 记录”时的同进程过滤和删除重试，`registerLocked` replacement rollback 也已完成；但生产过滤器把 DeviceStore 未初始化、查询错误和记录缺失全部解释成“允许投递”。生产启动顺序和设备替换路径证明这些并非理论状态，因此“立即且跨重启 fail closed”尚未成立。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=12e83c366772d50253cc278792d463b9e6bef099
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 8983700d9e820dfdb0f504226bd23bf05a935112
配套仓库未提交状态=干净
预期产品特性=512MiB runtime-managed 软限额；无订阅 watcher 门控；enrollment 不回放且以 O(1) 条目内存保留进行中 turn；404/410 事务性清理；只有当前有效 trusted device 才能进入 Web Push fan-out；有界 continuity warm cache 与同指纹并发合并；一致内存遥测
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit 3218cb51e8ec，built 2026-09-19T16:01:30Z；GUI PID 53136，runtime PID 53227；8777 由该 runtime 监听
```

本轮开始时配套 iOS HEAD 为 v5 记录的 `5ea9799...`；评审期间该工作树由其他任务追加两个 docs-only 提交并前进到 `8983700...`，仍为同名功能分支且干净。本任务没有读取 iOS 业务源码形成结论，也没有 iOS 修改；报告按写入前门点记录最新身份。

## 2. Round 4 处置核验

| Round 4 项 | Round 5 结论 |
| --- | --- |
| R4-B1 revoked fan-out | **部分处置。** 注入的 callback 对明确 revoked 记录会在 HTTP 前跳过，并重试物理清理；三阶段 WebPushStore 测试通过。但生产初始化、missing/error 语义和真实 FileDeviceStore 重载没有被覆盖，见 R5-B1。 |
| r4 §4 register replacement rollback | 已处置。替换落盘失败会恢复旧记录，内存与磁盘保持一致，失败路径测试通过。 |
| Mac UI `pushCleanupError` | 继续列为可观测性补充，非阻断；它不能代替投递授权过滤。 |

## 3. 发布阻断项

### R5-B1 — production filter 在真实的 nil/error/missing 状态下 fail open

`webPushDeviceRevokedFilter` 目前只有“record 存在且 `RevokedAt != nil`”返回 true；以下情况全部返回 false、允许投递：

1. `globalDeviceStore == nil`；
2. `LookupByDeviceID` 返回错误；
3. 找不到 device record。

这三个形状在生产中均可到达：

- **启动窗口**：`main.go` 在约 315–326 行构造并启动 dispatcher/watcher，直到约 387 行构造 ManagementConfig 时才调用 `newTrustedDeviceStore` 并写入 `globalDeviceStore`。候选队列和被动订阅在此前已经接线；窗口内 filter 明确 fail open。若 candidate 与赋值并发，还存在对全局 interface 的未同步读写风险，现有 race 测试没有执行该生产启动序列。
- **DeviceStore 加载失败**：`newTrustedDeviceStore` 在 `devices.json` 加载失败时返回一个空 `MemoryDeviceStore`。此时 direct auth 会让既有设备全部失效，但磁盘 WebPush subscriptions 仍能加载；filter 把所有 device 都判为 missing 并继续发送，恰好违背 fail closed。
- **设备替换产生 orphan**：`ReplaceDevice` 会删除 legacy ID 或同安装身份的旧 deviceID，并返回 `replacedDeviceIDs`；management 当前只记录日志，没有联动删除对应 WebPush subscription。旧 subscription 随后成为 missing，而 v5 明确让 missing fail open，因此旧 endpoint 继续收到推送。

现有 `TestDispatcherRevokedDeviceFailsClosedAndRetriesCleanup` 没有覆盖上述生产形状。所谓“重启阶段”只重新 `LoadWebPushStore`，revoke callback 仍闭包引用同一个 `MemoryDeviceStore`；它没有把 revoked record 写入 `FileDeviceStore`、重新从 `devices.json` 加载，也没有调用 production `webPushDeviceRevokedFilter`。所以它证明了 callback 逻辑，不证明 v5 声称的持久 DeviceStore 跨重启接线。

安全不变量应改成：**只有明确查到且未 revoked 的 trusted device 才允许投递。** 对 product-injected filter 而言，未初始化、查询错误、missing 都应停止本次投递；是否重试物理删除可再区分“明确 revoked/missing”和“暂时查询失败”，但不能先发出去。测试默认不注入 filter 仍可服务纯 dispatcher 单测，不应据此降低产品路径授权语义。

下一轮至少需要：

1. 在启动 dispatcher 前创建并冻结实际 DeviceStore 引用，直接注入该引用的授权判断，避免晚初始化全局变量及其数据竞争窗口；
2. 产品路径只允许显式 active record，nil/error/missing 均 fail closed；
3. 对 `ReplaceDevice` 返回的旧 ID 联动清理 subscription，或让 missing/orphan 路径安全跳过并自愈清理；
4. 使用真实临时 `devices.json` + `web-push-subscriptions.json` 的 production-shape 测试：持久 revoke 后同时重载 FileDeviceStore/WebPushStore，确认 0 HTTP 请求；另测 nil、lookup error、missing、replacement orphan 均为 0 请求，active record 正常发送；
5. DeviceStore 加载失败时不得让旧 subscription 获得比 direct auth 更宽的授权。

## 4. 非阻断修正

- v5 部署历史中 v4 时间写成 `2026-09-19T23:48:0900+0800`，多了 `00`；应改为 `2026-09-19T23:48:09+0800`。
- `DeviceRevoked func(deviceID string) bool` 只能表达二态，容易再次把“未知”折叠成 active。建议改成正向 `DeviceAuthorized`，或返回明确 decision/error，使调用点默认 deny；这是实现建议，不限定具体 API。

## 5. 独立验证

- `GOSUMDB=sum.golang.org go test ./go-bridge -run '^(TestDispatcherRevokedDeviceFailsClosedAndRetriesCleanup|TestWebPushDeviceRevokedFilter|TestWebPushStoreRegisterReplacementRollsBackOnPersistFailure|TestMgmtRevokeDeviceWebPushCleanupFailureExposed|TestWebPushStoreDeleteDeviceRollsBackOnPersistFailure|TestDispatcher404|TestLastClaudeUserIdentity|TestClaudeWebPushWatcher|TestApplyDefaultMemoryLimit|TestMgmtRuntimeDiagnosticsMemoryShape)' -count=1`：通过，`0.795s`。
- `GOSUMDB=sum.golang.org go test -race ./go-bridge -run '^(TestDispatcherRevokedDeviceFailsClosedAndRetriesCleanup|TestWebPushDeviceRevokedFilter|TestWebPushStoreRegisterReplacementRollsBackOnPersistFailure|TestMgmtRevokeDeviceWebPushCleanupFailureExposed|TestLastClaudeUserIdentity|TestClaudeWebPushWatcherEnrollmentRetainsLiveTurnCompletingAfter)$' -count=1`：通过，`1.203s`。该 race 集合不执行 `Main` 的 dispatcher-before-global-store 初始化序列。
- `GOSUMDB=sum.golang.org go test ./agent/claudecode -count=1`：通过，`2.469s`。
- `GOSUMDB=sum.golang.org go test -race ./agent/claudecode -run '^TestInspectTranscriptContinuity' -count=3`：通过，`1.028s`。
- `GOSUMDB=sum.golang.org go vet ./go-bridge ./agent/claudecode`：通过。
- 运行中 Management API `memory` 实测：`sys=96327960`、`heapReleased=71630848`、`sysMinusHeapReleased=24697112`，恒等式成立。该瞬时值只证明 endpoint/shape 正常，不作为真实负载效果定论。
- 运行态独立核验：`3218cb51e8ec`，GUI/runtime PID 53136/53227，8777 listener 与 512MiB 特征日志均和 v5 一致。
- `git diff --check`：通过。
- 未运行 UI/snapshot test、模拟器/真机自动化；未重新构建、安装或重启。

## 6. 下一轮准入条件

1. 将 product fan-out 改成显式 active 才允许；消除 DeviceStore 晚初始化窗口和全局未同步读写。
2. 覆盖真实 FileDeviceStore 跨重启、加载失败、missing/orphan、lookup error 与 active 正常投递测试。
3. 处理 `ReplaceDevice` 返回旧 ID 后的 subscription 生命周期，不能让 orphan endpoint 因 missing fail open。
4. 修正 v5 文档中 missing/nil fail open 与“跨重启 fail closed”的错误结论及时间戳笔误。
5. 保持当前已通过的流式 identity、continuity、watcher、410 cleanup、store rollback、memory shape 测试与 race 全绿。
6. 业务代码变化后按来源门重新 Release 构建、覆盖安装并验证新代际；部署证据继续只在部署完成后写入。
