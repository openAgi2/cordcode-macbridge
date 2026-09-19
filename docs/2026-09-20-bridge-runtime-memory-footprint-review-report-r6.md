# bridge runtime memory footprint 复审报告（Round 6）

复审对象：代码提交 `ee43c8f783710f826817c7a60109691e24689df7`、评审稿 v6 提交 `264e5c638c58d6580acb78318b74f6c1f12f3b6d`，以及已安装到 `/Applications/CordCodeLink.app` 的 `ee43c8f78371` runtime。

结论：**通过，无发布阻断项。** R5-B1 要求的显式 active 才允许投递、DeviceStore 启动顺序、degraded/error/missing 语义、replacement orphan 生命周期和双文件真实重载测试均已落实。先前已通过的 continuity cache、流式 enrollment identity、404/410 事务性清理、内存软限额与一致遥测未发生回归。真实负载内存对账和 Mac UI 展示 `pushCleanupError` 继续作为非阻断后续事项。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=264e5c638c58d6580acb78318b74f6c1f12f3b6d
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 8983700d9e820dfdb0f504226bd23bf05a935112
配套仓库未提交状态=干净
预期产品特性=512MiB runtime-managed 软限额；无订阅 watcher 门控；enrollment 不回放且以 O(1) 条目内存保留进行中 turn；404/410 事务性清理；只有显式 active trusted device 才能进入 Web Push fan-out；有界 continuity warm cache 与同指纹并发合并；一致内存遥测
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit ee43c8f78371，built 2026-09-19T17:39:57Z；GUI PID 12024，runtime PID 12110；8777 由该 runtime 监听
```

本轮没有读取 iOS 业务源码形成代码形状结论，也没有 iOS 修改。配套 iOS 身份仅用于满足跨仓来源门并确认任务分支族一致。

## 2. Round 5 阻断项处置核验

| Round 5 要求 | Round 6 核验结论 |
| --- | --- |
| dispatcher 前创建并冻结 DeviceStore 引用 | **已处置。** `Main` 在构造 dispatcher 前调用 `newTrustedDeviceStoreChecked`，授权闭包直接捕获该 store；ManagementConfig 复用同一实例，不再在 fan-out 中读取晚初始化全局。 |
| 只有显式 active 才允许；nil/error/missing 均不得放行 | **已处置。** production callback 仅在找到未撤销记录时返回 `WebPushDeviceActive`。lookup error 与 degraded store 的 missing 返回 `Unknown`；健康 store 的 missing 和 revoked 返回 `Denied`。dispatcher 只有 `Active` 分支继续发送，`Denied` 与默认分支都在 HTTP 前退出。 |
| replacement orphan 生命周期 | **已处置。** pairing approve 对 `ReplaceDevice` 返回的旧 ID 主动调用 `DeleteDevice`；若清理失败，健康 store 下的 missing 仍被授权层拒绝并在下一次 fan-out 自愈。 |
| 真实 FileDeviceStore 跨重启及 active 对照 | **已处置。** `TestDispatcherDeviceAuthorizationFileStoreReload` 同时从真实 `devices.json` 与 `web-push-subscriptions.json` 重载，验证 revoked 每阶段均不投递、active 每阶段恰好投递一次、持久化恢复后只剩 active subscription。 |
| devices.json 加载失败不得比 direct auth 更宽 | **已处置。** constructor 返回 degraded 标志；空 fallback store 的 missing 映射为 `Unknown`，拒绝投递但保留 subscription，待文件修复并重启后恢复。 |
| v5 文档错误结论及时间戳 | **已处置。** v6 改为 deny-by-default 三态语义，并将 v4 安装时间修正为 `2026-09-19T23:48:09+0800`。 |

代码审查同时确认：`Denied` 清理失败不会转而投递；`Unknown` 不会误删可能仍有效的订阅；switch 的 default 会拒绝 `Unknown` 及未来新增的非 Active 枚举值。存储一致性与投递授权已按 r4/r5 裁决分层。

## 3. 独立验证

- 定向 go-bridge 测试通过：授权五分支、orphan/Unknown dispatcher 行为、真实双文件重载、revoked 三阶段重试、register replacement rollback、revoke cleanup 错误暴露、DeleteDevice rollback、404、watcher/enrollment、identity、memory limit 与 diagnostics shape；耗时 `0.870s`。
- 对授权、重载、撤销、rollback 与 management 失败路径运行 `-race`：通过，耗时 `1.285s`。
- `go test ./agent/claudecode -count=1`：通过，耗时 `2.387s`。
- continuity/identity 相关 race 测试连续 3 轮：通过，耗时 `1.117s`。
- `go vet ./go-bridge/... ./agent/claudecode/...`：通过。
- 运行中 Management API `memory` 实测：`sys=96201000`、`heapReleased=69459968`、`sysMinusHeapReleased=26741032`，恒等式 `sysMinusHeapReleased == sys - heapReleased` 成立。该瞬时值仅证明生产 endpoint 与 shape 正常，不作为真实负载内存效果定论。
- 未运行 UI/snapshot test、模拟器或真机自动化；本轮只评审，未重新构建、安装或重启。

## 4. 部署运行态核验

- `/Applications/CordCodeLink.app` 与内嵌 runtime mtime 均为 `2026-09-20T01:41:50+0800`。
- GUI PID 12024 启动于 `01:41:50`；runtime PID 12110 启动于 `01:41:57`，PPID 12024；启动时间晚于构建时间。
- PID 12110 监听 TCP 8777，命令路径来自 `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`。
- runtime `-version` 为 `ee43c8f78371`，built `2026-09-19T17:39:57Z`。
- 启动日志首行包含本代际预期特征：`default memory limit applied limitBytes=536870912`。
- 未发现来自 `/tmp`、DerivedData、`build/` 或源码目录的 CordCodeLink/runtime 进程。

因此 v6 文档所述构建、安装和当前运行代际能够独立复现；本结论属于生产运行态身份验证，不把产物符号检查或单测冒充部署证据。

## 5. 非阻断建议与后续边界

1. `WebPushDeviceActive` 当前是枚举零值。现有 production callback 每条路径都显式返回，dispatcher 也只接受该精确值，因此不构成当前漏洞；但从 API 防误用角度，建议未来把零值保留给 `Unknown`/`Denied`，让未初始化 decision 在类型层面也天然拒绝。该项不要求本轮继续改代码或重新部署。
2. 按 v6 §7，在真实 transcript 负载下同时采集 `Sys`、`HeapReleased`、`Sys-HeapReleased` 与系统 footprint，才能裁决 swapped/retained 页机制并复核 512MiB provisional 默认值。启动 RSS 或单次空闲快照不能完成该结论。
3. Mac UI 展示 `pushCleanupError` 仍是有价值的可观测性补充，但授权层已经阻断撤销设备投递，不影响本轮发布通过。

## 6. 最终裁决

Round 6 **通过**。R5-B1 已完成代码、失败语义、持久重载测试和生产部署四层闭环；无需 Round 7 修订即可结束本次代码发布审查。后续 metrics-first 内存采样应作为效果验证任务单独执行，不应回写成尚未取得的根因定论。
