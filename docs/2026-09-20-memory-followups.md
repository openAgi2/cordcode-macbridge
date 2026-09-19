# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v1，待复审）

> 状态：**待复审**。r6 复审（报告 `docs/2026-09-20-bridge-runtime-memory-footprint-review-report-r6.md`，
> commit `35a1b8062fee8853180eb67f5572637731003fde`）通过主修复后记录了三项非阻断后续；
> owner 于 2026-09-20 指示全部落地。本文记录三项的处置、内存对账首采数据与部署证据。
> 业务代码有变化（`124b73d`），已重新 Release 构建、覆盖安装并验证新代际。

## 1. 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
r6 通过时代码=ee43c8f783710f826817c7a60109691e24689df7（评审稿 v6 终态=264e5c6，标记提交=e34019a）
本后续代码提交=124b73d7b0f2155ed19207c3ec6fe00cdf5fa888（API 零值硬化、Mac UI pushCleanupError、测试、think.md/CHANGELOG）
未提交状态=本稿为 docs-only 提交；代码工作树在 124b73d 后无其他未提交修改
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 8983700d9e820dfdb0f504226bd23bf05a935112（r5/r6 报告记录的状态；本任务无 iOS 代码声明或改动）
预期产品特性=授权枚举零值拒绝态；Mac 端撤销清理失败弹窗提示；内存遥测对账首采
```

## 2. 内存对账首采（r6 后续 1）

### 2.1 采集现场（ee43c8f 代际，PID 12110，2026-09-20 01:41:50 启动）

**采集时间=2026-09-20T01:55+0800，运行时长 14m38s。** 窗口很短（owner 指示
立即执行），数据证明遥测管线与当前健康状态，**不能定论**长期行为与压缩器假设
（旧进程 2.5G footprint 用了 26h50m 才累积出来）。

| 指标 | 值 | 判读 |
| --- | --- | --- |
| Management API `memory.sys` | 96,463,144（~92MB） | runtime 从 OS 获取的总量 |
| `memory.heapInuse` | 8,830,976（~8.4MB） | 活跃堆极小 |
| `memory.heapIdle` | 77,414,400（~73.8MB） | 空闲 span（含已归还） |
| `memory.heapReleased` | 71,630,848（~68.3MB） | **scavenger 活跃归还**（当前量语义） |
| `memory.sysMinusHeapReleased` | 24,832,296（~23.7MB） | **GOMEMLIMIT 管辖量，远低于 512MiB** ✓ |
| `memory.numGC` | 4,877（14.6 分钟） | GC 频繁（~5.6 次/s）但单次代价小（heapInuse 8.4MB）；分配率约 50–60MB/s，与 3s watcher sweep + 轮询一致，值得在长窗口复采时观察 |
| vmmap Physical footprint | 47.5M（峰值 74.8M） | 正常 |
| vmmap TOTAL SWAPPED | 26.1M | **对比旧进程 2.4G** |
| ps RSS | 36,144KB | — |
| 系统级 | swap 12.6G/13.3G used | 整机内存压力仍在（其他进程），runtime 自身健康 |

### 2.2 初步结论与待办

- **已证明（本窗口）**：遥测管线端到端可用；GOMEMLIMIT 管辖量 23.7MB 远低于
  512MiB（数值无需下调）；scavenger 在活跃归还（heapReleased 68.3MB）；
  footprint 47.5M / swapped 26.1M 与旧进程 2.5G / 2.4G 形成鲜明对比。
- **未定论（窗口不足）**：压缩器假设（swapped/retained 页的具体状态）与
  长期负载下的 footprint 走势——旧进程的 2.4G swapped 需要 ~27h 累积。
- **复采要求**：真实负载运行 1–2 天后，用同一命令组（management API
  `/internal/diagnostics/runtime` 的 `memory` 节 + `vmmap --summary <pid>` +
  `ps rss`）再采一次；若 footprint 回到数百 MB 且 `sysMinusHeapReleased`
  仍 ≤512MiB，则「死页滞留由 GOMEMLIMIT + 波源治理压住」成立；若
  footprint 高而 `sysMinusHeapReleased` 低，则压缩器候选解释获得支持。
  numGC 频率（当前 ~5.6/s）在长窗口中若持续，可评估 GOGC 调参降低 GC churn。

## 3. API 硬化（r6 后续 2）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive`（允许！）改为
新增的 `WebPushDeviceUnspecified`（**拒绝但不清理**——「不知道」只 deny 不删
订阅）。dispatcher 的 `default` 分支天然覆盖零值（deny by default），无需改
调用点。测试：`TestDispatcherAuthorizationMissingOrphanAndUnknown` 增加零值
用例——回调返回零值 → 0 次 HTTP 请求 + 订阅保留（不触发清理）。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3）

- **API 层**：`DeviceAPIProviding.revokeDevice` 返回 `DeviceRevocation`
  （`pushCleanupError: String?`）；`ManagementAPIClient` 解码 revoke 响应体
  （解码失败不回滚撤销，只丢失提示）。
- **ViewModel**：`DeviceStore.revokeCleanupWarning`（@Published）在撤销成功但
  清理失败时发布（**不视为撤销失败**，`devicesError` 保持 nil）；
  `dismissRevokeCleanupWarning()` 供确认后清除。
- **View**：`WorkspaceView` 在撤销确认框之后挂 `.alert`（与既有
  codexRestart 弹窗同一绑定模式），双语 L10n（en/zh 均新增
  `devices_push_cleanup_warning_title` / `devices_push_cleanup_warning`），
  文案明确「设备已撤销、推送投递已被阻断、可重试撤销或查看 runtime 日志」。
- **测试**：`DeviceStoreTests` 新增 2 条（清理失败→发布警告且不算错误+可
  dismiss；清理成功→警告为 nil）；`WorkspaceViewTests` stub 更新。Swift 定向
  21 条全绿。

## 5. 交付与部署证据（部署完成后写入）

### 5.1 构建来源门

```text
构建工作目录=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
构建时提交=124b73d7b0f2155ed19207c3ec6fe00cdf5fa888
构建前未提交状态=干净（git status --short 无输出）
构建命令=GOSUMDB=sum.golang.org ./scripts/build-unsigned-release.sh
产物路径=build/unsigned-release/Build/Products/Release/CordCodeLink.app
runtime 版本元数据=cordcode-bridge-runtime 0.1.0 (commit: 124b73d7b0f2, built: 2026-09-19T18:06:36Z)
dist 产物=dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip
```

### 5.2 安装与运行态（部署后实测）

```text
安装时间=2026-09-20T02:08:21+0800（killall + rm -rf /Applications/CordCodeLink.app + cp -R + open）
GUI 进程=PID 30081（/Applications/CordCodeLink.app/Contents/MacOS/CordCodeLink）
runtime 进程=PID 30304（/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，-port 8777 …）
8777 listener=lsof 确认由 PID 30304 LISTEN
特征日志=time=2026-09-20T02:08:32.439+08:00 level=INFO msg="go-bridge: default memory limit applied" limitBytes=536870912
启动 RSS=78112KB（仅记录，不作为内存效果验证）
```

### 5.3 验证状态

| 项 | 状态 |
| --- | --- |
| Go 定向测试 | dispatcher 全组（含零值用例）、store 全组、授权判定、revoke、memory limit、diagnostics shape——全绿 |
| Go race + vet | 通过；干净 |
| Swift 定向测试 | DeviceStoreTests（8 条，含 2 条新警告路径）+ WorkspaceViewTests（13 条）——21 条全绿 |
| 部署 | 见 §5.2（部署后写入，非预先声明） |

## 6. 遗留

1. **内存对账复采**（§2.2）：真实负载 1–2 天后同命令组复采；这是压缩器假设
   与 GOMEMLIMIT 数值复核的最终定论步骤。
2. Mac UI 的 pushCleanupError 弹窗为可观测性补充；真机/桌面端到端验收
   （实际撤销一台设备观察弹窗）由 owner 验收，未在本轮自动化范围内。
