# bridge runtime memory footprint 复审报告（Round 4）

复审对象：代码提交 `f45ac93f1f48e28e2b8639fa38d55e608b21264d`、评审稿 v4 提交 `28e45884a342a11b9bbc602474f82343e10b12ef`，以及已安装到 `/Applications/CordCodeLink.app` 的 `f45ac93f1f48` runtime。

结论：**仍不通过，剩余 1 个发布阻断项。** R3-B1、R3-B3、R3-B4 已完整处置；R3-B2 修好了 subscription 内存/磁盘不一致，也如实返回了 cleanup error，但没有完成“撤销后停止投递”的安全语义。清理落盘失败时 subscription 被恢复到 dispatcher 的可见集合，dispatcher 不核验 trusted-device revoke 状态，Mac 客户端又丢弃响应体，因此已撤销设备会在当前进程和重启后继续收到 Web Push。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=28e45884a342a11b9bbc602474f82343e10b12ef
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 5ea9799d3701a4ba4d0ac9cfe19b16153c2f4254
配套仓库未提交状态=干净
预期产品特性=512MiB runtime-managed 软限额；无订阅 watcher 门控；enrollment 不回放且以 O(1) 条目内存保留进行中 turn；404/410 事务性清理；设备撤销后不再投递；有界 continuity warm cache 与同指纹并发合并；一致内存遥测
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit f45ac93f1f48，built 2026-09-19T15:46:29Z；GUI PID 44119，runtime PID 44215；8777 由该 runtime 监听
```

本轮开始时配套 iOS HEAD 为 v4 记录的 `6e6080e...`；评审期间该工作树由其他任务追加两个 docs-only 提交并前进到 `5ea9799...`，仍为同名功能分支且干净。本任务未读取 iOS 业务源码形成结论，也没有 iOS 修改；报告按写入前门点记录最新身份，不把后来的提交倒写成 v4 成稿时状态。

## 2. Round 3 处置核验

| Round 3 项 | Round 4 结论 |
| --- | --- |
| R3-B1 部署证据 | 已处置。v4 确在部署后提交，§5 包含实际构建目录、完整代码提交、构建前状态、产物版本、安装时间、PID、listener 与特征日志；本轮独立核验一致。 |
| R3-B2 DeleteDevice | **部分处置。** 删除落盘失败会恢复内存，badge 清理已后置，response 会带 `pushCleanupError`；但撤销后的投递授权仍未切断，见 R4-B1。 |
| R3-B3 全量物化 | 已处置。专用 scanner 只保存最后一个 identity，过滤状态机与旧 scanner 对齐；大 transcript 堆上界测试、定向 race 均通过。 |
| R3-B4 压缩器事实化 | 已处置。§3.1 改为 vmmap 可证明的 swapped/dirty 形状，并明确机制仅是候选。 |

## 3. 发布阻断项

### R4-B1 — cleanup error 被暴露了，但 revoked device 仍在投递集合中

当前失败链路如下：

1. `handleRevokeDevice` 先将 trusted device 持久化为 revoked 并断开连接；
2. `DeleteDevice` 删除 subscription 后持久化失败，于是把记录恢复进 `byDeviceID`；
3. management response 仍是 HTTP 200，只附加 `pushCleanupError`；
4. Mac 客户端 `ManagementAPIClient.revokeDevice` 丢弃整个成功响应体，`DeviceStore` 随后刷新列表；revoked device 已被 `ListDevices` 隐藏，用户既看不到 warning，也没有可用的重试入口；
5. `WebPushDispatcher.deliverCandidate` 直接遍历 `store.Subscriptions()`，不查询 `DeviceStore` 或 revoked tombstone。因此恢复的 subscription 会立即继续投递；runtime 重启后磁盘旧记录再次加载，行为不变。

所以 v4 §6 的“失败回滚 + 响应暴露，重启后无复活”不成立。严格说它不是“复活”——失败后 subscription 从未退出可投递集合。当前测试也把 `SubscriptionCount()==1` 当作成功条件，却没有断言 revoked device 不会收到下一条 candidate。

此项仍是发布阻断，因为推送携带标题和内容预览，授权撤销必须优先于 store 一致性。实现可自行选择，但必须形成持久、可验证的 fail-closed 语义，例如：

- dispatcher 在 fan-out 前以持久 trusted-device revoke 状态过滤；或
- 维护跨重启可恢复的 revoked/suppressed tombstone，并在 storage 恢复后重试物理删除。

仅把 HTTP 200 response 解码并展示 warning 不够，它不能阻止未授权投递；仅保留内存删除也不够，重启会从旧 subscription 文件恢复。验收测试必须覆盖：cleanup 持久化失败后，同进程下一条 candidate 不向 revoked device 发请求；重载/重启形状下仍不发；恢复存储后可完成清理。Mac UI 是否额外展示 warning 可作为可观测性补充。

## 4. 非阻断修正

- v4 多处称 DeleteDevice/Unregister 与 `registerLocked` 是“同一纪律”，但 `registerLocked` 在替换既有 device subscription 后持久化失败会直接 `delete(s.byDeviceID, deviceID)`，不会恢复旧记录；磁盘仍是旧记录，当前进程却为空。建议保存 `existing, hadExisting` 并在失败时恢复，增加 replacement failure 测试。它不改变本轮 revoke 授权裁决，故列为非阻断，但当前文档的“同一纪律”并不准确。
- 大 transcript 内存测试证明的是当前 12MB/小行 fixture 在周期 GC 下不超过基线 +4MB；scanner 允许单行最高 16MB，因此不要把 `+4MB` 写成所有合法输入的全局字节上界。v4 当前使用“O(1) 条目内存”并将 +4MB 限定为测试，表述可以接受。

## 5. 独立验证

- `GOSUMDB=sum.golang.org go test ./go-bridge -run '^(TestLastClaudeUserIdentity|TestClaudeWebPushWatcher|TestWebPushStoreDeleteDevice|TestWebPushStoreMarkExpired|TestMgmtRevokeDeviceWebPushCleanupFailureExposed|TestDispatcher404|TestApplyDefaultMemoryLimit|TestMgmtRuntimeDiagnosticsMemoryShape)' -count=1`：通过，`1.266s`。
- `GOSUMDB=sum.golang.org go test -race ./go-bridge -run '^(TestLastClaudeUserIdentity|TestClaudeWebPushWatcherEnrollmentRetainsLiveTurnCompletingAfter|TestWebPushStoreDeleteDeviceRollsBackOnPersistFailure|TestMgmtRevokeDeviceWebPushCleanupFailureExposed)$' -count=1`：通过，`1.329s`。
- `GOSUMDB=sum.golang.org go test ./agent/claudecode -count=1`：通过，`2.652s`。
- `GOSUMDB=sum.golang.org go test -race ./agent/claudecode -run '^TestInspectTranscriptContinuity' -count=3`：通过，`1.029s`。
- `GOSUMDB=sum.golang.org go vet ./go-bridge ./agent/claudecode`：通过。
- 运行中 Management API `memory` 实测：`sys=108648728`、`heapReleased=87506944`、`sysMinusHeapReleased=21141784`，恒等式成立。该瞬时值只证明 endpoint/shape 正常，不作为真实负载效果定论。
- 运行态独立核验：`f45ac93f1f48`，GUI/runtime PID 44119/44215，8777 listener 与 512MiB 特征日志均和 v4 一致。
- `git diff --check`：通过。
- 未运行 UI/snapshot test、模拟器/真机自动化；未重新构建、安装或重启。

## 6. 下一轮准入条件

1. cleanup 持久化失败时，revoked device 必须立即且跨重启从 Web Push fan-out 中 fail closed；增加同进程、重载和恢复清理测试。
2. 修正文档中“失败回滚后重启无复活”的错误结论，明确 subscription 存储一致性与授权投递过滤是两个不同不变量。
3. 建议同时修复 `registerLocked` replacement failure 的旧记录恢复和对应措辞。
4. 保持当前已通过的流式 identity、continuity、watcher、410 cleanup、memory shape 测试与 race 全绿。
5. 业务代码变化后按来源门重新 Release 构建、覆盖安装并验证新代际；部署证据继续只在部署完成后写入。
