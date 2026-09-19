# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v3，待复审）

> 状态：**待复审（Round 3）**。v2（`2fb00b8`）经 Round 2 评审（报告
> `docs/2026-09-20-memory-followups-review-report-r2.md`，commit
> `98af5625bfa6e6393500ebb1c754c1b1f1c060b2`）判定：Round 1 的 B1/B2 已完整处置，
> 剩余 3 个方案阻断项（R2-B1 监测无原子采样事务与完成条件 / R2-B2 整机 load 与
> 提案阈值不能充当负载证明与治理结论 / R2-B3 malformed 200 仍可能解码成清理成功、
> unknown 文案与人工验收路径未收口）。**v3 为 docs-only 修订：本轮零代码改动**
> （Round 2 评审已用 `git diff 8f77506..2fb00b8` 独立确认 v2 轮零业务代码变化；
> 本轮同样只改本文档与 think.md 指针）。Round 2 三项建议全部采纳（§6 无新增
> 不采纳项）；不采纳项仍为 Round 1 的 3 项，见 §6。
>
> 历史背景：r6 复审（报告 r6，commit `35a1b8062fee8853180eb67f5572637731003fde`）
> 通过主修复后记录三项非阻断后续；owner 于 2026-09-20 指示全部落地。业务代码提交
> `124b73d` 已完成 Release 构建、覆盖安装并验证新代际（§5，Round 1 评审已独立
> 复核部署身份一致；Round 2 复核当前部署仍为 `124b73d7b0f2`、PID 34681 监听 8777）。

## 1. 来源清单（P0）

### 1.1 v1 的错误与更正（Round 1 B1，Round 2 已确认处置）

v1 把配套 iOS 仓固定为旧评审身份 `8983700d...`（非本任务门点实测），且声称
`124b73d` 包含 think.md（实际 9 个文件不含；python 脚本锚点不匹配静默失败）。
两项已在 v2 更正，think.md 补记落盘为提交 `106bb95`。

### 1.2 本任务（v3 修订轮）三门点真实清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
门点1（读取源码/文档分析前）提交=98af562（Round 2 评审报告提交；工作树干净，git status --porcelain 无输出）
门点2（第一次修改文件前）提交=98af562（干净；本轮只改 docs/2026-09-20-memory-followups.md 与 think.md 指针，无业务代码修改）
门点3（构建/部署前）=不适用——本轮 docs-only（D0），无构建、无安装、无部署
本轮源码核读=go-bridge/management_api.go（runtimeIdentity/revoke 响应形状）、go-bridge/runtime_diagnostics.go（diagnostics 字段），只读取证用于 §2.3/§4.3 规格对齐，未修改
think.md 指针更新=随本提交（（v2）→（v3）及监测结论措辞同步）
本提交=本文档 v3（docs-only）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 80b4b04c（门点1 实测，与 Round 2 评审记录一致）
配套仓库未提交状态=OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift、OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift、OpenCodeiOS/OpenCodeiOSTests/NativeTimelineKeyboardContainerTests.swift 已修改（另一任务所有；本轮未读取其内容、未修改、未提交）
预期产品特性=本轮无产品代码变化；当前部署仍为 124b73d7b0f2（PID 34681 监听 8777，Round 2 评审实测）
```

### 1.3 `124b73d` 真实文件范围（9 个）

```text
CHANGELOG.md
MacBridge/MacBridge/Services/Localization.swift
MacBridge/MacBridge/Services/ManagementAPIClient.swift
MacBridge/MacBridge/ViewModels/DeviceStore.swift
MacBridge/MacBridge/Views/WorkspaceView.swift
MacBridge/MacBridgeTests/DeviceStoreTests.swift
MacBridge/MacBridgeTests/WorkspaceViewTests.swift
go-bridge/web_push_dispatcher.go
go-bridge/web_push_dispatcher_test.go
```

## 2. 内存对账首采（r6 后续 1；B2 已于 Round 2 确认处置）

### 2.1 采集现场与原始数据（完整 provenance）

```text
runtime 版本=ee43c8f 代际（r6 内存修复构建；当时部署，早于 124b73d 的 02:06+0800 安装）
runtime PID=12110，启动=2026-09-20T01:41:50+0800
采样时刻=2026-09-20T01:55+0800，运行时长=14m38s（878s）
bridge epoch=未记录（采样时未采集该字段——诚实标注，不回填）
系统负载=未记录（采样时未采集——诚实标注，不回填）
采集命令组=1) management API GET /internal/diagnostics/runtime 的 memory 节
  （经 lsof 探测 ephemeral 端口 + Bearer token，token 不落盘）
  2) vmmap --summary <pid>  3) ps rss
单位说明=management API memory 字段为字节（Go runtime.MemStats 语义）；
  vmmap 行为 vmmap 自打印的 M/K；ps RSS 为 KB
```

原始观测值（**全部为「观测」级——单时间点直接读数，无时间序列**）：

| 指标 | 原始值 | 证据级 |
| --- | --- | --- |
| `memory.sys` | 96,463,144 B（~92MB） | 观测 |
| `memory.heapInuse` | 8,830,976 B（~8.4MB） | 观测 |
| `memory.heapIdle` | 77,414,400 B（~73.8MB） | 观测 |
| `memory.heapReleased` | 71,630,848 B（~68.3MB） | 观测（当前量 gauge：已归还且尚未重新获取的页，非累计值） |
| `memory.sysMinusHeapReleased` | 24,832,296 B（~23.7MB） | 计算（= sys − heapReleased，字段定义） |
| `memory.numGC` | 4,877 | 观测（累计计数器） |
| vmmap Physical footprint | 47.5M（峰值 74.8M） | 观测 |
| vmmap TOTAL SWAPPED | 26.1M | 观测 |
| ps RSS | 36,144 KB | 观测 |
| 系统级 swap | 12.6G/13.3G used | 观测（整机，其他进程为主） |

可成立的计算（仅算术，不含行为断言）：

- 平均 GC 完成频率 = 4,877 次 ÷ 878s ≈ **5.6 次/s**（仅频率；单次代价未测）。
- `sysMinusHeapReleased` 23.7MB ≪ 512MiB：**本窗口内** Go runtime 管辖量远低于
  软限额（窗口限定）。

### 2.2 撤回清单（v1 越界断言，Round 2 已确认全部处置）

| # | v1 原断言 | 撤回理由 |
| --- | --- | --- |
| 1 | 「scavenger 活跃归还」 | `HeapReleased` 是当前 gauge，单点证明不了「活跃」 |
| 2 | 「分配率约 50–60MB/s」 | 无 `TotalAlloc` 计数器；数字无原始采样、无计算式 |
| 3 | 「单次代价小」 | `HeapInuse` 是状态 gauge，不是 GC pause/CPU cost |
| 4 | 「512MiB 无需下调」 | 14.6 分钟窗口裁决不了默认值 |

**无法补证说明**：PID 12110 已消亡，时间序列无法回溯采集；选择撤回，不以替代
数据冒充（Round 2 评审认可该边界）。

### 2.3 复采设计（R2-B1/R2-B2 重写：原子采样事务 + 完成条件 + 负载覆盖门 + provisional 告警线）

**前提**：`autoRestartEnabled` 默认 true、`autoRestartIntervalMinutes` 默认 120
——代际长度上限 ~2h 是预期行为；多代际监测不回答 27h 同进程累积问题（受控长
窗口不采纳为默认，§6.2）。

**端点形状（commit `98af562` 源码核读，与 Round 2 评审实测一致）**：

- `GET /internal/status` → `runtimeIdentity = {pid, bridgeEpoch}`（唯一 identity 源）。
- `GET /internal/diagnostics/runtime` → `startedAt`（RFC3339）、`processUserCPUSeconds`
  /`processSystemCPUSeconds`（累计）、`backgroundTasks`、`activeBackgroundScans`
  （瞬时 gauge）、`agentBackgroundScans:<backendID>`（含 `scans`/`successes`/
  `failures`/`turnItemRequests`/`scannedTurns` 累计计数）、`memory.*`——
  **不含 runtime identity**（identity 与 memory 分散在两个端点，正是混代风险的
  来源，R2-B1）。

#### 2.3.1 原子采样事务（防混代协议，每样本固定四步）

1. **identity A**：探测 management 端口（`lsof -nP -a -iTCP -sTCP:LISTEN` 定位
   runtime 进程的监听端口），`GET /internal/status` 取
   `A = (pid, bridgeEpoch)`。
2. **数据采集**（全部以 `A.pid` 为对象）：`GET /internal/diagnostics/runtime`
   （startedAt + CPU + 计数器 + memory）、`vmmap --summary A.pid`、
   `ps -o rss A.pid`、`sysctl vm.loadavg`（仅系统背景）。
3. **identity B**：再次 `GET /internal/status` 取 `B = (pid, bridgeEpoch)`。
4. **提交判定**：当且仅当 **A == B（pid 与 bridgeEpoch 均相等）** 且**所有命令
   成功** 且 **diagnostics `startedAt` 与该代已记录的 startedAt 一致**（同一
   进程同一启动）时，写一条**有效样本**（单条 JSONL，含全部字段+时间戳）；
   否则写一条 **rejected sample**（带原因码：`identity_changed` /
   `command_failed` / `started_at_mismatch`），**绝不把部分字段拼成有效样本**。

**样本时刻**：每代际内固定 `t=0/30/60/90`（分钟，对齐代际启动时刻）。t=0 =
检测到新代际（identity 或 startedAt 变化）后的第一个采样事务，立即执行。某时刻
事务 rejected 即该时刻掉样，**不补采、不重试凑数**（无法回溯采样）；下一计划
时刻继续。

#### 2.3.2 代际有效性与负载覆盖门（CordCode 自证负载，非整机 load）

- **有效代际** = 同一 (pid, bridgeEpoch, startedAt) 下 ≥3 个有效样本（即代际
  实测时长 ≥60 分钟）。
- **有载代际**（负载覆盖门，同代际首末有效样本之间满足任一）：
  1. 任一 `agentBackgroundScans:<backend>` 累计计数（`scans`/`turnItemRequests`/
     `scannedTurns`）delta > 0——发生过真实后台扫描/turn 拉取；或
  2. `processUserCPUSeconds + processSystemCPUSeconds` delta ≥ 60s——runtime
     自身 CPU 消耗 ≥1 分钟（纯 idle websocket 保活远低于此；任何 transcript
     扫描或 turn 中继都会超过）。
- 未达标代际 = **空闲/低负载代际**：只进「空闲观测」统计，不进「真实负载」
  结论；结论措辞必须区分两类覆盖度。
- `sysctl vm.loadavg` 仅作系统背景记录，**明确不作为 CordCode 负载证据**
  （R2-B2：整机 load 无法区分 CordCode 是否执行过扫描/poll/turn 活动）。
- 计数器瞬时/累计分类按 `98af562` 源码核读（`processCPUSeconds` 与
  `BackgroundTaskScanCounters` 为累计；`activeBackgroundScans`/`activeScans` 为
  瞬时）；脚本实施时复核，若与源码有出入以实施时源码为准并回填本文档。

#### 2.3.3 provisional 告警线（降级：只触发升级，不产生结论）

| 线 | 值 | 来源与理由 | 风险声明 |
| --- | --- | --- | --- |
| `sysMinusHeapReleased` 代际末值 | >256MiB | 512MiB 软限的 50%：提前半个软限触发调查，留取证余量 | 无生产分布数据支撑，纯早期预警 |
| vmmap footprint 代际末值 | >300MB | 健康快照 47.5M 的 ~6 倍、旧症状 2.5G 的 ~1/8（「数百 MB」带沿） | 同上 |

- 触发条件含趋势形态：≥3 个连续有效样本单调上升 **且** 代际末值越线。
- **语义（R2-B2 核心修正）**：触发 → 仅决定「停止默认监测路径、评估升级取证
  （§6.2 受控长窗口，需 owner 明确授权）」。**未触发不能推出任何结论**——
  「120 分钟代际内上界成立」「治理有效」「512MiB/GOMEMLIMIT 配置合理」均不得
  由「未越线」表述（否则阈值即结论，循环论证）。
- **明确能力边界**：本监测无 GC pause/alloc counter，**不能裁决 GOGC 或 512MiB
  最优值**，只能决定是否需要升级取证。

#### 2.3.4 完成条件（预固定；变更须修订本文档）

监测**完成**当且仅当以下三条同时满足：

1. 墙钟窗口 ≥ **7 个自然日**（覆盖工作日+周末使用模式）；
2. 有效代际（≥3 有效样本）≥ **20 个**；
3. 其中有载代际 ≥ **8 个**。

数值依据：7 天=使用模式覆盖；20 代际≈按典型每日开机时长折算至少数天连续
数据，防止单日偶然满足；8 有载代际=保证「真实负载」结论不建立在纯空闲观测上。

- **提前终止**：任一 provisional 告警线触发 → 停止默认路径并升级（§6.2）。
- **掉样处理**：rejected 样本只计入 rejected 统计（含原因码分布）；完成条件
  只数有效样本/代际。7 天后若有效代际或负载覆盖不足，**延长窗口直至满足并
  记录延长原因，不降低门槛**。
- **实施形态**：过审后实现为本地归档脚本（非产品代码、不进 app bundle、不改
  go-bridge），按 §2.3.1 事务与 §2.3.4 条件执行；本轮未实施。

### 2.4 最终报告形状（预留；监测完成后回填）

- 每指标 **observed max**（按代际、按全局）与分布（min/p50/p95）、有载/空闲
  代际覆盖度、rejected 样本数与原因分布、告警线触发记录。
- 结论措辞限定为描述性：「在 X 个有效代际（其中有载 Y 个）、Z 天窗口内，观测
  最大 sysMinusHeapReleased = …、footprint = …」；**不写**「治理有效」「512MiB
  合理」「GOGC 应调」。
- 附 §2.3.3 能力边界声明（不能裁决 GOGC/512MiB 最优值）。

## 3. API 硬化（r6 后续 2；已完成，Round 1/2 评审已独立复核通过）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive` 改为
`WebPushDeviceUnspecified`（拒绝但不清理）。dispatcher `default` 分支天然覆盖
零值。测试 `TestDispatcherAuthorizationMissingOrphanAndUnknown` 零值用例：0 次
HTTP 请求 + 订阅保留。本轮无变化。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3；B4 方案 v3）

### 4.1 已交付（`124b73d`）

`DeviceRevocation`/`revokeCleanupWarning`/`.alert`/双语 L10n/`DeviceStoreTests`
2 条新用例 + `WorkspaceViewTests` stub（Swift 定向 21 条全绿）。

### 4.2 评审发现的问题（Round 1 B4 + Round 2 R2-B3，全部成立）

1. 文案「可重试撤销」不可执行（Round 1，v2 已改）。
2. `try?` 解码使「解码失败」与「无清理错误」静默等价（Round 1，v2 已改）。
3. **R2-B3**：`RevokeDeviceResponse.revoked` 为 optional——合法 JSON `{}` 会成功
   解码并落入 `pushCleanupError=nil / unknown=false`，再次把未知当清理成功；
   `{"revoked":false}` 亦无明确语义。
4. **R2-B3**：unknown 文案的「runtime 自动重试」须为条件句（订阅仍在才重试；
   清理已成功但响应不可读时无待重试项）；撤销生效与否应以 `ListDevices`
   reload 为权威，不以 HTTP 2xx 单独认定。
5. **R2-B3**：人工验收路径不可达——生产响应不会自然产生 cleanup failure 或
   malformed 200；正常撤销本就不应弹警告。

### 4.3 过审后实施方案（本轮不动代码；过审后一次实施 + 定向测试 + 重建部署）

#### 4.3.1 响应结构不变量（与生产实现核读一致：`management_api.go` `handleRevokeDevice`
200 成功体 = `{"revoked": true, "deviceId": …}` + 可选 `pushCleanupError`）

**结构有效成功 ⇔ `revoked == true`（严格布尔真值）**。分类：

- `revoked==true` + 无 `pushCleanupError` → 清理成功，无警告；
- `revoked==true` + `pushCleanupError` → 清理失败警告（此态订阅确定仍在，
  「后续 fan-out 自动重试」无条件表述成立）；
- **以下全部进 protocol-unknown 路径**（`cleanupStatusUnknown = true`）：
  `{}`、缺 `revoked` 键、`revoked:false`、类型错误（如 `{"revoked":"yes"}`）、
  非 JSON 体。

#### 4.3.2 撤销生效判定（reload 权威，R2-B3）

撤销动作是否生效**不以 HTTP 2xx 单独认定**；以撤销后的 `ListDevices` 权威
reload 结果决定「已撤销」还是「状态未知」。完整决策表（每行一条 fixture/测试）：

| # | HTTP | 响应体 | reload 结果 | 呈现 |
| --- | --- | --- | --- | --- |
| 1 | 2xx | `revoked:true`，无 cleanupError | 设备消失 | 无警告 |
| 2 | 2xx | `revoked:true` + cleanupError | 设备消失 | cleanup-failure 警告（现有 key） |
| 3 | 2xx | `revoked:true` | 设备仍在 | **inconsistent 警告**（服务端确认撤销但列表未消失） |
| 4 | 2xx | `{}` / 缺 revoked / `revoked:false` / 类型错 / 非 JSON | 设备消失 | unknown 警告（响应无法确认；设备已从列表消失） |
| 5 | 2xx | 同上 | 设备仍在 | unknown 警告（响应无法确认且设备仍在列表，可能未生效） |
| 6 | 2xx | 同上 | reload 失败 | unknown 警告（响应与列表均无法确认） |
| 7 | 非 2xx / 网络错 | — | — | 撤销失败 `devicesError`（现有行为不变） |

#### 4.3.3 文案（条件性自愈；删除不可执行动作）

- `devices_push_cleanup_warning`（决策表 #2；此态订阅确定存在，重试表述准确）：
  - en: "The device was revoked, but cleaning up its push subscription failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
  - zh: "设备已撤销，但其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
- `devices_push_cleanup_unknown`（决策表 #4/#5/#6；**条件句**，以设备列表为权威）：
  - en: "The revocation response could not be confirmed, so the cleanup result of this device's push subscription is unknown. Use the device list as the source of truth: if the device has disappeared and its push subscription is still pending cleanup, the runtime retries it automatically on later notifications; if it still appears in the list, refresh and check the runtime log."
  - zh: "撤销响应无法确认，该设备推送订阅的清理结果未知。请以设备列表为准：若设备已从列表消失而订阅仍待清理，runtime 会在后续通知投递时自动重试；若设备仍显示在列表中，请刷新并查看 runtime 日志。"
- 新增 `devices_push_cleanup_inconsistent`（决策表 #3）：
  - en: "The runtime confirmed the revocation, but this device still appears in the authorized list. Refresh the list; if it persists, check the runtime log."
  - zh: "runtime 已确认撤销，但该设备仍出现在已授权列表中。请刷新列表；若持续显示，请查看 runtime 日志。"

#### 4.3.4 实施与测试清单

1. `RevokeDeviceResponse` 解码改为要求 `revoked == true`（缺键/false/类型错/非
   JSON → `cleanupStatusUnknown = true`，撤销成功语义不回滚但不冒充清理成功）。
2. `DeviceStore.revokeDevice` 按 §4.3.2 决策表分支：reload 结果 × 响应分类 →
   三种警告态（failure/unknown/inconsistent）或无警告。
3. **client 级 wire fixtures**（复用 `ManagementAPIClientTimeoutTests` 的
   StubHTTPServer 模式）：决策表 #1–#6 的响应体全覆盖（`revoked:true`±cleanupError、
   `{}`、`revoked:false`、`"revoked":"yes"`、非 JSON）。
4. **ViewModel 级测试**：reload 三分支（消失/仍在/失败）× 响应分类；presentation
   seam（`isRevokeCleanupWarningPresented`）在发布时 true、dismiss 后 false。
5. 定向测试 → Release 重建 + 覆盖安装 + 新代际核验（评审准入条件）。

## 5. 交付与部署证据（`124b73d` 历史记录；Round 1/2 已独立复核，本轮无新部署）

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

Round 2 评审实测：当前 runtime PID 34681（同版本 `124b73d7b0f2`）监听 8777；
该代际变化不用于证明 §2 首采数据。

### 5.3 验证状态

| 项 | 状态 |
| --- | --- |
| Go 定向测试 / race / vet | 全绿（Round 1 评审独立复跑；代码自 `124b73d` 未变化） |
| Swift 定向测试 | 21 条全绿（同上） |
| 部署 | §5.2；本轮（v3）零代码改动，无新构建/部署 |

## 6. 不采纳清单（评审建议未采纳项，逐项标明理由）

**Round 2 三项建议（原子采样事务、负载覆盖门与告警线降级、`revoked==true` 不变量
与验收分离）全部采纳，无新增不采纳项。** 以下为 Round 1 遗留的 3 项：

### 6.1 B2「补同一 PID 原始时间序列」——无法采纳，以撤回替代

PID 12110 已消亡，时间序列无法回溯采集（§2.2）。

### 6.2 B3 选项一「受控长窗口（关闭自动重启）」——不采纳为默认路径

理由：(1) 需要在 owner 日常使用的生产 Mac 上禁用 runtime 自动重启 27h+，风险与
信息价值不成比例；(2) 复采首要问题（真实负载下代际内观测上界）用多代际监测
即可回答。**升级条件**：§2.3.3 provisional 告警线触发时再评估，且必须 owner
明确授权。

### 6.3 B4「提供可达的『重试撤销』入口」——不采纳，采纳文案修正路径

撤销是一次性授权动作，撤销后设备从 `ListDevices` 消失；物理清理由服务端
deny-by-default + 后续 fan-out 自动重试自愈；用户侧重试入口不增加安全性，只增
UI 面积。

## 7. 遗留与下一步

1. **本文档过审后**：实施 §4.3（B4 代码修正：`revoked==true` 不变量 + 决策表
   分支 + 条件性文案 + wire/ViewModel fixtures + presentation seam）→ 定向测试
   → Release 重建 + 覆盖安装 + 新代际核验。
2. **多代际监测脚本**（§2.3）：过审后实施；按 §2.3.4 完成条件执行，最终报告
   回填 §2.4。
3. **owner 验收（R2-B3 修正：可达路径与自动化验收分离）**：
   - **owner 人工验收（唯一自然可达路径）**：在 Mac 端正常撤销一台真实设备 →
     应看到：设备从已授权列表消失、**无任何警告弹窗**（正常撤销不应弹警告）。
   - **failure / unknown / inconsistent 三态**：生产环境无安全自然触发方式
     （不做故障注入、不加 demo 路径），由 §4.3.4 的 deterministic client/
     ViewModel 测试验收，不经 owner 手工制造。
