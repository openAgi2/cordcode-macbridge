# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v2，待复审）

> 状态：**待复审（Round 2）**。v1（`7b8d6a2`）经 Round 1 评审（报告
> `docs/2026-09-20-memory-followups-review-report.md`，commit
> `cc8eacf532f4fd80985699eb32e07e4e086f5a3f`）判定 4 个阻断项（B1 来源清单 /
> B2 单点外推 / B3 复采方案无视 120 分钟自动重启 / B4 不可执行文案与缺失 wire
> 测试）。**v2 为 docs-only 修订：本轮零代码改动**，B4 的代码修正全部改写为
> 「本文档过审后实施」的方案（§4.3）；工作树在修订前后保持干净（除本提交与
> think.md 补记提交外无其他改动）。不采纳的评审建议集中在 §6，逐项标明理由。
>
> 历史背景：r6 复审（报告 `docs/2026-09-20-bridge-runtime-memory-footprint-review-report-r6.md`，
> commit `35a1b8062fee8853180eb67f5572637731003fde`）通过主修复后记录三项非阻断
> 后续；owner 于 2026-09-20 指示全部落地。业务代码提交 `124b73d` 已完成 Release
> 构建、覆盖安装并验证新代际（§5，Round 1 评审已独立复核部署身份一致）。

## 1. 来源清单（P0，B1 修正）

### 1.1 v1 的错误与更正

v1 §1 把配套 iOS 仓固定为 `8983700d9e820dfdb0f504226bd23bf05a935112` 并注明
「r5/r6 报告记录的状态」——这是复用旧评审身份，不是本任务门点重新生成的清单，
违反独立来源门；且该值在本任务业务提交（`124b73d`，2026-09-20T02:06:30+0800）
与 v1 文档提交（`7b8d6a2`，02:09:29+0800）时都不是 iOS 工作树 HEAD（当时已到
`aabbe6c0...`）。v1 还声称 `124b73d` 包含 think.md——实际该提交共 9 个文件，
**不含 think.md**（v1 写作时用 python 脚本改 think.md，锚点不匹配导致静默失败，
脚本却无条件打印成功；首采记录当时未落盘）。两项均按评审 B1 更正。

### 1.2 本任务（v2 修订轮）三门点真实清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
门点1（读取源码/文档分析前）提交=8f77506（工作树干净）
门点2（第一次修改文件前）提交=8f77506（干净；本轮只改 docs/ 与 think.md，无业务代码修改）
门点3（构建/部署前）=不适用——本轮 docs-only（D0），无构建、无安装、无部署
think.md 补记提交=106bb95（首采记录落盘，grep 验证命中；承载 §2 引用的 think.md 记录）
本提交=本文档 v2（提交哈希见 git log；docs-only）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 80b4b04c（门点1 实测）
配套仓库未提交状态=OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift、OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift、OpenCodeiOS/OpenCodeiOSTests/NativeTimelineKeyboardContainerTests.swift 已修改（另一任务所有；本轮未读取其内容、未修改、未提交）
预期产品特性=本轮无产品代码变化；当前部署仍为 124b73d7b0f2（评审期间 supervisor 启动的新 runtime 代际 PID 34681 亦为该版本，见评审报告 §1/§4）
```

### 1.3 `124b73d` 真实文件范围（9 个，更正 v1 的「含 think.md」误记）

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

think.md 的首采记录本轮已另行落盘为提交 `106bb95`（见 §1.2），不再误归于
`124b73d`。

## 2. 内存对账首采（r6 后续 1；B2 重写：证据分级 + 撤回越界断言）

### 2.1 采集现场与原始数据（完整 provenance）

```text
runtime 版本=ee43c8f 代际（r6 内存修复构建；当时部署，早于 124b73d 的 02:06+0800 安装）
runtime PID=12110，启动=2026-09-20T01:41:50+0800
采样时刻=2026-09-20T01:55+0800，运行时长=14m38s（878s）
bridge epoch=未记录（采样时未采集该字段——诚实标注，不回填）
系统负载=未记录（采样时未采集 load average——诚实标注，不回填）
采集命令组=
  1) management API GET /internal/diagnostics/runtime 的 memory 节
     （经 lsof 探测 ephemeral 端口 + Bearer token，token 不落盘）
  2) vmmap --summary <pid>
  3) ps rss
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

- 平均 GC 完成频率 = 4,877 次 ÷ 878s ≈ **5.6 次/s**（仅频率；单次代价未测，
  见 §2.2 撤回 3）。
- `sysMinusHeapReleased` 23.7MB ≪ 512MiB（536,870,912 B）：**本窗口内** Go
  runtime 管辖量远低于软限额（窗口限定，见 §2.2 撤回 4）。

### 2.2 撤回清单（v1 越界断言，按评审 B2 逐项撤回）

| # | v1 原断言 | 撤回理由 | 重新表述（证据级：推断，弱） |
| --- | --- | --- | --- |
| 1 | 「scavenger 活跃归还（heapReleased 68.3MB）」 | `HeapReleased` 是当前 gauge，单点只证明采样瞬间存在已归还页，证明不了「活跃」（需同 PID 多时间点变化或 runtime trace） | 采样瞬间存在 68.3MB 已归还未重取的页 |
| 2 | 「分配率约 50–60MB/s，与 3s watcher sweep + 轮询一致」 | 当前 memory shape 无 `TotalAlloc`/alloc bytes 计数器；该数字无原始采样、无计算式，也不能归因到具体波源 | 分配率未测（无计数器）；如需测量须先扩展遥测 |
| 3 | 「单次代价小（heapInuse 8.4MB）」 | `HeapInuse` 是当前堆状态 gauge，不是 GC pause/CPU cost；需 `PauseTotalNs`/pause 分布、`GCCPUFraction` 或时间序列 | 单次 GC 代价未测 |
| 4 | 「512MiB 数值无需下调」 | 14.6 分钟窗口 + 管辖量 23.7MB 只能支持「本窗口未接近软限额」，裁决不了默认值是否合适（旧异常 ~26h50m 才累积） | 本窗口未触及软限额；数值复核依赖 §2.3 复采 |

**无法补证说明（不采纳「补同一 PID 时间序列」路径）**：PID 12110 已随后续
部署/重启消亡，时间序列无法回溯采集。评审给出的「撤回或补证」二选一中，本稿
选择**撤回**，不以任何替代数据冒充原进程时间序列。

### 2.3 复采设计（B3 重写：多代际监测，纳入 120 分钟自动重启）

**前提**：`autoRestartEnabled` 默认 true、`autoRestartIntervalMinutes` 默认 120
（活文档与生产实现一致）——任何「1–2 天后单次复采」采到的都是至多 ~2h 的新
代际，不能回答旧进程 27h 同进程累积问题。v1 的「1–2 天后复采一次」方案作废。
本稿采用评审二选一中的**多代际监测**；受控长窗口不采纳为默认（理由见 §6.2）。

**代际定义与失效规则**：

- 代际身份 = (runtime PID, bridge epoch)，均取自 management API。任一变化即
  新代际；比较对跨越代际边界即失效，须重新起算。
- 默认 120 分钟自动重启使代际长度上限 ~2h——这是**预期行为**，不是采样失败；
  手动重启同样只记为代际边界。

**采样设计**：

| 参数 | 值 | 说明 |
| --- | --- | --- |
| 采样周期 | 每代际内每 30 分钟一次（对齐代际启动时刻） | 代际通常 ~120min → 每代际 ~4 个样本 |
| 每样本字段 | 时间戳、PID、epoch、uptime、`memory.*` 全字段（sys/heapInuse/heapIdle/heapReleased/sysMinusHeapReleased/numGC）、vmmap footprint/peak/swapped、ps RSS、load average（`sysctl vm.loadavg`） | v1 缺 epoch 与负载，本轮补入字段定义 |
| 最低有效代际时长 | ≥60 分钟（≥2 个样本） | 短于 60min 的代际照常记录，但不进入上界结论 |
| 负载可比性 | load average 逐样本记录 | 跨代际比较仅定性；**主结论限定为代际内上界** |
| 上界判定（提案阈值，可调） | 任一 ≥60min 代际内：(a) `sysMinusHeapReleased` 连续 ≥3 样本单调上升且代际末值 >256MiB，或 (b) vmmap footprint 代际末值 >300MB → 标记异常 | 未触发 → 「120 分钟代际内上界成立」 |
| 异常处置 | 触发阈值 → 停止默认路径，评估 §6.2 受控长窗口（需 owner 明确授权） | 不自行关自动重启 |

**结论边界（诚实声明）**：多代际监测只能回答「真实负载下 120 分钟代际内的
上界」；**不能**回答「27h 同进程累积」。后者只有受控长窗口能回答，而它不是
默认路径。`sysMinusHeapReleased ≤ 512MiB` 只说明 Go runtime 管辖量未超软限额
附近，不自动证明「死页滞留由 GOMEMLIMIT + 波源治理压住」。

**实施形态**：过审后实现为本地归档脚本（非产品代码、不进 app bundle、不改
go-bridge），按上表字段以固定间隔写 JSONL；本轮未实施。numGC 若在长监测中持续
~5.6/s，再评估 GOGC 调参（依赖时间序列，本轮无立场）。

## 3. API 硬化（r6 后续 2；已完成，Round 1 评审已独立复核通过）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive`（允许！）改为新增的
`WebPushDeviceUnspecified`（**拒绝但不清理**——「不知道」只 deny 不删订阅）。
dispatcher 的 `default` 分支天然覆盖零值（deny by default），无需改调用点。测试：
`TestDispatcherAuthorizationMissingOrphanAndUnknown` 增加零值用例——回调返回零值
→ 0 次 HTTP 请求 + 订阅保留（不触发清理）。Go 定向测试、race、vet 与部署身份
验证均由 Round 1 评审独立复跑通过（报告 §2/§4），本轮无变化。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3；B4 重写：已交付部分 + 过审后实施方案）

### 4.1 已交付（`124b73d`，Round 1 评审已验证的部分）

- `DeviceAPIProviding.revokeDevice` 返回 `DeviceRevocation`（`pushCleanupError`）；
  `DeviceStore.revokeCleanupWarning`（@Published）撤销成功但清理失败时发布（不
  视为撤销失败）；`WorkspaceView` 挂 `.alert`；双语 L10n；`DeviceStoreTests` 2 条
  新用例 + `WorkspaceViewTests` stub 更新（Swift 定向 21 条全绿）。

### 4.2 评审发现的问题（Round 1 B4，全部成立）

1. 文案「可重试撤销」**不可执行**：`ListDevices` 只返回未撤销设备，撤销成功后
   设备从列表消失，用户没有再次点击「撤销」的入口；文案反而没有说明真实恢复
   行为（服务端 fan-out 对 denied subscription 自动重试物理清理）。
2. `ManagementAPIClient.revokeDevice` 用 `try?` 解码：响应体解码失败与「确实
   没有 cleanup error」静默等价。
3. 缺 `ManagementAPIClient` 级 200 响应 decode 测试（现有测试都注入已解码好的
   `DeviceRevocation`，未覆盖唯一 wire seam）。
4. 缺可测试的 presentation mapping/binding 断言（ViewModel 有值 ≠ alert 可达）。

### 4.3 过审后实施方案（本轮不动代码；文档过审后一次实施 + 定向测试 + 重建部署）

1. **文案修正**（删除不可执行的「可重试撤销」，改为真实行为；行为依据 =
   go-bridge `deliverCandidate` 的 Denied 分支每次后续 fan-out 重试
   `DeleteDevice`，r4/r5 轮已验证，评审报告 §B4 亦确认）：
   - `devices_push_cleanup_warning`：
     - en: "The device was revoked, but cleaning up its push subscription failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
     - zh: "设备已撤销，但其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
   - 新增 `devices_push_cleanup_unknown`（解码失败态，见下条）：
     - en: "The device was revoked, but the cleanup result of its push subscription could not be read from the runtime response. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
     - zh: "设备已撤销，但无法从 runtime 响应读取其推送订阅的清理结果。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
2. **`cleanupStatusUnknown` 语义**：`DeviceRevocation` 增加 `cleanupStatusUnknown`
   字段；`ManagementAPIClient.revokeDevice` 改 guard-let 解码，失败时返回
   `DeviceRevocation(pushCleanupError: nil, cleanupStatusUnknown: true)`（撤销已
   成功不回滚，但**不得**与「无清理错误」静默等价）；`DeviceStore` 对 unknown
   态发布 §4.3.1 的 unknown 文案。
3. **wire 测试**（`ManagementAPIClient` 级 HTTP fixture，复用
   `ManagementAPIClientTimeoutTests` 的 StubHTTPServer 模式）：200 带
   `pushCleanupError` → 解出；200 不带 → nil 且 unknown=false；200 畸形体 →
   unknown=true 且不抛错（撤销成功语义保持）。
4. **presentation seam**：`DeviceStore` 增加
   `isRevokeCleanupWarningPresented`（由 `revokeCleanupWarning != nil` 派生）；
   `WorkspaceView` alert 绑定改用该 seam；`DeviceStoreTests` 断言 seam 在发布时
   true、dismiss 后 false，并补 unknown 态用例。

## 5. 交付与部署证据（`124b73d` 历史记录；Round 1 评审已独立复核一致，本轮无新部署）

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

Round 1 评审期间 supervisor 启动的新 runtime 代际（PID 34681）版本仍为
`124b73d7b0f2`，8777 listener 正常（评审报告 §1/§4）；该代际变化不用于证明
§2 首采数据。

### 5.3 验证状态

| 项 | 状态 |
| --- | --- |
| Go 定向测试 | dispatcher 全组（含零值用例）、store 全组、授权判定、revoke、memory limit、diagnostics shape——全绿（Round 1 评审独立复跑） |
| Go race + vet | 通过；干净（Round 1 评审独立复跑） |
| Swift 定向测试 | DeviceStoreTests（8 条）+ WorkspaceViewTests（13 条）——21 条全绿（Round 1 评审独立复跑） |
| 部署 | §5.2；本轮（v2）零代码改动，无新构建/部署 |

## 6. 不采纳清单（评审建议未采纳项，逐项标明理由）

### 6.1 B2「补同一 PID 原始时间序列」——无法采纳，以撤回替代

PID 12110 已消亡，时间序列无法回溯采集（§2.2）。评审给出的「撤回或补证」
二选一中选择撤回；不以新代际数据冒充原进程时间序列。

### 6.2 B3 选项一「受控长窗口（关闭自动重启）」——不采纳为默认路径

理由：(1) 需要在 owner 日常使用的生产 Mac 上禁用 runtime 自动重启 27h+，而自动
重启本身是连接稳定性与状态治理的一部分——本轮内存修复针对的正是长代际累积，
为取证主动制造超长代际，风险与信息价值不成比例；(2) 复采的首要问题（真实负载
下代际内是否守住上界）用多代际监测即可回答。**升级条件**：多代际监测触发
§2.3 异常阈值时再评估，且必须 owner 明确授权后才可执行。

### 6.3 B4「提供可达的『重试撤销』入口」——不采纳，采纳文案修正路径

理由：撤销是一次性授权动作，撤销成功后设备从 `ListDevices` 消失，天然没有
「再撤销一次」的产品语义；物理清理由服务端 deny-by-default + 后续 fan-out 自动
重试自愈（r4/r5 轮验证），用户侧重试入口不增加安全性，只增加 UI 面积。采纳
评审给的第一条路径：文案改为真实可执行的恢复说明（§4.3.1）。

## 7. 遗留与下一步

1. **本文档过审后**：实施 §4.3（B4 代码修正：文案 + `cleanupStatusUnknown` +
   wire 测试 + presentation seam）→ 定向测试 → Release 重建 + 覆盖安装 +
   新代际核验（评审准入条件 5）。
2. **多代际监测脚本**（§2.3）：过审后实施；监测结论回填本文档 §2.4（预留）。
3. **owner 验收**：Mac 端实际撤销一台设备观察警告弹窗（含 unknown 态文案），
   由 owner 真机验收，不在自动化范围内。
