# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v4，待复审）

> 状态：**待复审（Round 4）**。v3（`c976ef4`）经 Round 3 评审（报告
> `docs/2026-09-20-memory-followups-review-report-r3.md`，commit
> `fd5d29b6f885c6ee2b2d4bf725816e82e1141df2`）判定：完成条件（7 天/20 有效代际/
> 8 有载代际）、runtime 自证负载门、provisional 告警语义、`revoked == true`
> 不变量、自然路径与自动化验收分离均已认可；剩余 3 个方案阻断项（R3-B1 提交
> 归属误写 / R3-B2 告警漏单波峰形状 + management URL 发现方式 / R3-B3 决策表
> 非完整状态空间 + `loadDevices()` Void 不足以支撑 reducer）。**v4 为 docs-only
> 修订：本轮零代码改动**；Round 3 三项建议全部采纳（§6 无新增不采纳项）。
>
> 历史背景：r6 复审通过主修复后记录三项非阻断后续；owner 于 2026-09-20 指示全部
> 落地。业务代码提交 `124b73d` 已完成 Release 构建、覆盖安装并验证新代际（§5）。
> 评审循环纪律（owner 2026-09-20 定案，commit `f79c6d4`/`c4f8e8b`）：方案阶段
> 文档循环直至评审「通过」是进入开发阶段的唯一入口——本文档即处于该循环中。

## 1. 来源清单（P0）

### 1.1 v1 的错误与更正（Round 1 B1，已处置）

v1 把配套 iOS 仓固定为旧评审身份 `8983700d...`（非本任务门点实测），且声称
`124b73d` 包含 think.md（实际 9 个文件不含）。已在 v2 更正，think.md 首采补记
落盘为提交 `106bb95`。

### 1.2 v3 归属更正（R3-B1）与本任务（v4 轮）三门点清单

**v3 归属更正**：think.md 与 followups 文档是**两个独立提交**，v3 §1.2 的
「think.md 指针更新=随本提交」表述有误，更正为：

```text
think.md 同轮提交（v3 轮）=d3c7404...（只改 think.md）
本文档（v3）提交=c976ef4...（只改 docs/2026-09-20-memory-followups.md）
```

（来源事实修正，不重写历史；Round 3 评审 §4 已独立核验两提交各自单文件。）

**v4 轮三门点**：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
门点1（读取源码/文档分析前）提交=c4f8e8b（Round 3 报告 fd5d29b 之后 owner 的评审纪律两提交 f79c6d4/c4f8e8b；工作树干净，git status --porcelain 无输出）
门点2（第一次修改文件前）提交=c4f8e8b（干净；本轮只改 think.md 与本文档，无业务代码修改）
门点3（构建/部署前）=不适用——本轮 docs-only（D0），无构建、无安装、无部署
本轮源码核读=go-bridge/runtime_startup.go（runtime.json 字段：managementUrl/pid/bridgeEpoch）、go-bridge/management_api.go 与 runtime_diagnostics.go（v3 已核读部分复核），只读取证用于 §2.3/§4.3 规格对齐，未修改
think.md 同轮提交（v4 轮）=a1818b200f041fc03fa17d53de751cb8fdb3f14e（只改 think.md）
本文档（v4）提交=本提交（只改 docs/2026-09-20-memory-followups.md）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 6b6cd95a（门点1 实测，干净——Round 3 评审期间其他任务已提交其 3 个 iOS 文件，本轮工作树无未提交修改）
预期产品特性=本轮无产品代码变化；当前部署仍为 124b73d7b0f2（PID 34681 监听 8777，Round 3 评审实测）
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
  （当时经 lsof 探测端口；v4 起正式脚本改用 runtime.json bootstrap，见 §2.3.1）
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
数据冒充。

### 2.3 复采设计（R2-B1/B2 + R3-B2 修订：runtime.json bootstrap + 原子采样事务 + 完成条件 + 负载覆盖门 + 三类独立 OR 告警）

**前提**：`autoRestartEnabled` 默认 true、`autoRestartIntervalMinutes` 默认 120
——代际长度上限 ~2h 是预期行为；多代际监测不回答 27h 同进程累积问题（受控长
窗口不采纳为默认，§6.2）。

**端点与 bootstrap 形状（commit `c4f8e8b` 源码核读，与 Round 2/3 评审实测一致）**：

- `runtime.json`（go-bridge `runtime_startup.go` `WriteReadyFrame` 原子写；Mac
  App 同源发现）：`managementUrl`（如 `http://127.0.0.1:61945`）、`pid`、
  `bridgeEpoch`（UUID 字符串）——**management URL 的权威来源**。
- `GET /internal/status` → `runtimeIdentity = {pid, bridgeEpoch}`（唯一 identity 源）。
- `GET /internal/diagnostics/runtime` → `startedAt`（RFC3339）、
  `processUserCPUSeconds`/`processSystemCPUSeconds`（累计）、`backgroundTasks`、
  `activeBackgroundScans`（瞬时 gauge）、`agentBackgroundScans:<backendID>`（含
  `scans`/`successes`/`failures`/`turnItemRequests`/`scannedTurns` 累计计数）、
  `memory.*`——**不含 runtime identity**。
- 实测同一 runtime 进程同时监听 management（`127.0.0.1:61945`）与 bridge
  （`*:8777`）端口，未来可能还有 8778——**lsof「监听端口」无法唯一选择
  management 端口**（R3-B2）。

#### 2.3.1 原子采样事务（防混代协议，每样本固定五步）

1. **bootstrap**：读 `runtime.json` 取 `managementUrl` 与期望 `(pid, bridgeEpoch)`
   （**不以 lsof 猜端口**；lsof 仅作诊断交叉检查——PID/listener 一致性）。
2. **identity A**：`GET /internal/status`（用 bootstrap URL）取
   `A = (pid, bridgeEpoch)`，与 runtime.json 的 pid/epoch 交叉核对；不匹配（残留
   旧帧/新 runtime 接管）→ 重读 runtime.json 一次，仍不匹配 → rejected sample
   （原因码 `bootstrap_mismatch`）。
3. **数据采集**（全部以 `A.pid` 为对象）：`GET /internal/diagnostics/runtime`
   （startedAt + CPU + 计数器 + memory）、`vmmap --summary A.pid`、
   `ps -o rss A.pid`、`sysctl vm.loadavg`（仅系统背景）。
4. **identity B**：再次 `GET /internal/status` 取 `B = (pid, bridgeEpoch)`。
5. **提交判定**：当且仅当 **A == B（pid 与 bridgeEpoch 均相等）** 且**所有命令
   成功** 且 **diagnostics `startedAt` 与该代已记录的 startedAt 一致**（同一
   进程同一启动）时，写一条**有效样本**（单条 JSONL，含全部字段+时间戳）；
   否则写一条 **rejected sample**（原因码：`bootstrap_mismatch` /
   `identity_changed` / `command_failed` / `started_at_mismatch`），**绝不把
   部分字段拼成有效样本**。

**样本时刻**：每代际内固定 `t=0/30/60/90`（分钟，对齐代际启动时刻）。t=0 =
检测到新代际（identity 或 startedAt 变化）后的第一个采样事务，立即执行。某时刻
事务 rejected 即该时刻掉样，**不补采、不重试凑数**；下一计划时刻继续。

**脚本测试矩阵（过审后随脚本实施，至少覆盖）**：正常样本 / A≠B 改代 /
startedAt 变化 / 任一命令失败 / bootstrap mismatch / 单点绝对越线 / footprint
peak 越线 / 趋势触发（R3-B2）。

#### 2.3.2 代际有效性与负载覆盖门（CordCode 自证负载，非整机 load）

- **有效代际** = 同一 (pid, bridgeEpoch, startedAt) 下 ≥3 个有效样本（即代际
  实测时长 ≥60 分钟）。
- **有载代际**（负载覆盖门，同代际首末有效样本之间满足任一）：
  1. 任一 `agentBackgroundScans:<backend>` 累计计数（`scans`/`turnItemRequests`/
     `scannedTurns`）delta > 0——发生过真实后台扫描/turn 拉取；或
  2. `processUserCPUSeconds + processSystemCPUSeconds` delta ≥ 60s——runtime
     自身 CPU 消耗 ≥1 分钟（纯 idle websocket 保活远低于此）。
- 未达标代际 = **空闲/低负载代际**：只进「空闲观测」统计，不进「真实负载」
  结论；结论措辞必须区分两类覆盖度。
- `sysctl vm.loadavg` 仅作系统背景记录，**明确不作为 CordCode 负载证据**。
- 计数器瞬时/累计分类按源码核读；脚本实施时复核，出入以实施时源码为准并回填。

#### 2.3.3 provisional 告警线（R3-B2 重写：三类**独立 OR** 触发，只升级不定论）

| 触发器 | 条件 | 覆盖形状 |
| --- | --- | --- |
| ① 绝对越线 | 任一有效样本 `sysMinusHeapReleased` **current** > 256MiB | 持续高位 |
| ② footprint 波峰 | 任一有效样本 vmmap footprint **current 或进程 lifetime peak**（`Physical footprint (peak)`）> 300MB | **两采样点之间单次大分配波后回落/高位持平——原事故形状（continuity cold scan 单波），①和③都捕获不了** |
| ③ 趋势异常 | 连续 ≥3 有效样本 `sysMinusHeapReleased` 严格上升且**每步增量 ≥8MiB** | 稳定爬升的早期形态 |

- 三者**互不替代、独立 OR**：③不要求与①同时成立（v3 的「单调上升且末值越线」
  联合条件已作废——它会漏掉②形状）。
- ③增量门依据：正常 GC 归还/重取造成的 gauge 波动为个位数 MiB；≥8MiB/步 ×
  ≥3 步（≥24MiB/60min）的稳定爬升远低于 256MiB 绝对线但值得早期关注。预固定
  值，变更须修订本文档。
- 256MiB 来源 = 512MiB 软限的 50%（提前半个软限触发调查）；300MB 来源 = 健康
  快照 47.5M 的 ~6 倍、旧症状 2.5G 的 ~1/8。**均无生产分布数据支撑，纯早期
  预警**。
- **语义**：触发 → 仅决定「停止默认监测路径、评估升级取证（§6.2，需 owner
  明确授权）」。**未触发不能推出任何结论**——「上界成立」「治理有效」
  「512MiB/GOMEMLIMIT 配置合理」均不得由「未越线」表述。
- **能力边界**：本监测无 GC pause/alloc counter，**不能裁决 GOGC 或 512MiB
  最优值**，只能决定是否需要升级取证。

#### 2.3.4 完成条件（预固定；变更须修订本文档；Round 3 已认可）

监测**完成**当且仅当：①墙钟 ≥ **7 个自然日**（覆盖工作日+周末使用模式）；
②有效代际 ≥ **20 个**；③其中有载代际 ≥ **8 个**。

- **提前终止**：任一 provisional 告警触发 → 停止默认路径并升级（§6.2）。
- **掉样处理**：rejected 只计入统计；完成条件只数有效样本/代际；7 天后不足则
  **延长窗口直至满足并记录原因，不降低门槛**。
- **实施形态**：过审后实现为本地归档脚本（非产品代码、不进 app bundle、不改
  go-bridge），按 §2.3.1 事务与 §2.3.4 条件执行，测试覆盖 §2.3.1 矩阵。

### 2.4 最终报告形状（预留；监测完成后回填）

- 每指标 **observed max**（按代际、按全局）与分布（min/p50/p95）、有载/空闲
  代际覆盖度、rejected 样本数与原因分布、告警触发记录（含②的 lifetime peak）。
- 结论措辞限定为描述性：「在 X 个有效代际（其中有载 Y 个）、Z 天窗口内，观测
  最大 sysMinusHeapReleased = …、footprint（含 lifetime peak）= …」；**不写**
  「治理有效」「512MiB 合理」「GOGC 应调」。
- 附 §2.3.3 能力边界声明。

## 3. API 硬化（r6 后续 2；已完成，Round 1/2/3 评审已独立复核通过）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive` 改为
`WebPushDeviceUnspecified`（拒绝但不清理）。dispatcher `default` 分支天然覆盖
零值。测试零值用例：0 次 HTTP 请求 + 订阅保留。本轮无变化。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3；B4 方案 v4）

### 4.1 已交付（`124b73d`）

`DeviceRevocation`/`revokeCleanupWarning`/`.alert`/双语 L10n/`DeviceStoreTests`
2 条新用例 + `WorkspaceViewTests` stub（Swift 定向 21 条全绿）。

### 4.2 评审发现的问题（Round 1 B4 + R2-B3 + R3-B3，全部成立）

1. 文案「可重试撤销」不可执行（Round 1，v2 已改）。
2. `try?` 解码使「解码失败」与「无清理错误」静默等价（Round 1，v2 已改）。
3. optional `revoked` 使 `{}` 解码成清理成功（R2-B3，v3 已改 `revoked == true`）。
4. unknown 文案须条件句、撤销生效以 reload 为权威（R2-B3，v3 已改）。
5. **R3-B3**：v3 的 7 行决策表不是 response × reload 完整状态空间——遗漏
   `confirmedClean × reload 失败`、`cleanupError × 设备仍在`、`cleanupError ×
   reload 失败`、以及「服务端已执行但响应丢失 × reload 后设备消失」（lost
   response，标准副作用-后连接丢失形状，仓内 pairing client 已有同类 reconcile
   测试）；v3 第 7 行「非 2xx/网络错 → devicesError 且不 reload」与「reload
   权威」自相矛盾。
6. **R3-B3**：现有 `loadDevices()` 返回 `Void`、失败时保留旧 `devices`——
   不足以支撑可靠 reducer（`await loadDevices()` 后读到的可能是陈旧数组）。
7. **R3-B3**：「unknown 仍保持撤销成功语义」预设错误——只有 `revoked:true`
   或 reload absent 才能确认撤销生效。

### 4.3 过审后实施方案（本轮不动代码；过审后一次实施 + 定向测试 + 重建部署）

#### 4.3.1 正交状态定义（先定义状态，再列完整矩阵）

**response class**（HTTP + 响应体，与生产实现核读一致：200 成功体恒含
`revoked:true` + 可选 `pushCleanupError`）：

| class | 判定 |
| --- | --- |
| `confirmedClean` | 2xx 且 `revoked == true` 且无 `pushCleanupError` |
| `confirmedCleanupFailure(error)` | 2xx 且 `revoked == true` 且有 `pushCleanupError` |
| `protocolUnknown` | 2xx 但 `{}` / 缺 `revoked` / `revoked:false` / 类型错误 / 非 JSON |
| `transportOrHTTPFailure` | 网络错误或非 2xx（**含响应丢失——服务端可能已执行**） |

**reload class**（来自新增 typed reload seam，§4.3.2）：

| class | 判定 |
| --- | --- |
| `absent` | reload 成功且设备不在列表 |
| `present` | reload 成功且设备仍在列表 |
| `failed` | reload 失败（**不得读旧 `devices` 数组推断**） |

**生效判定规则（R3-B3 准入 5：删除 unknown=撤销成功预设）**：只有
`revoked == true`（confirmed response）**或** reload `absent` 才确认「已撤销」；
其余为 `未生效`（请求失败且权威列表仍在）或 `不一致`（响应与列表矛盾）或
`未知`。

#### 4.3.2 typed reload seam（R3-B3 准入 4 的实现前提）

新增 `DeviceAPIProviding` 侧 typed reload（具体命名实施时定，语义如下）：

```swift
func reloadDevicesForResult() async -> Result<[TrustedDevice], Error>  // 或等价 typed outcome
```

- `DeviceStore.revokeDevice` 的决策**只消费该 seam 的返回值**；`await` 后**不读**
  可能陈旧的 `devices` 数组（现 `loadDevices()` 失败时保留旧数组，正是不可靠
  来源）。
- ViewModel 状态（列表、错误、警告）由 (response class, reload outcome) **原子
  更新**。
- 现有 `loadDevices()`（Void）保留为普通列表刷新入口，内部复用 typed seam；
  其既有失败语义（`devicesError` + 保留旧列表）不因此改变。

#### 4.3.3 完整决策矩阵（4 × 3 = 12 行，测试逐行覆盖）

| # | response class | reload class | 生效判定 | cleanup 信息 | 呈现 |
| --- | --- | --- | --- | --- | --- |
| 1 | confirmedClean | absent | 已撤销 | clean | 无警告 |
| 2 | confirmedClean | present | 不一致 | clean | inconsistent 警告 |
| 3 | confirmedClean | failed | 已撤销（响应已确认） | clean | 无撤销警告；列表刷新失败由 reload 路径自身呈现（`devicesError`），不从旧数组推断 |
| 4 | confirmedCleanupFailure | absent | 已撤销 | failed(err) | cleanup-failure 警告（可声称投递已阻断：撤销已确认 + 订阅确定未清） |
| 5 | confirmedCleanupFailure | present | 不一致 | failed(err) | inconsistent 警告 + cleanup 错误附注（**不得声称投递已阻断**） |
| 6 | confirmedCleanupFailure | failed | 已撤销（响应已确认） | failed(err) | cleanup-failure 警告；列表刷新失败另行呈现 |
| 7 | protocolUnknown | absent | 已撤销（reload 对账） | unknown | unknown 警告（条件性自愈文案） |
| 8 | protocolUnknown | present | 未知（倾向未生效） | unknown | unknown 警告（设备仍在列表，可能未生效） |
| 9 | protocolUnknown | failed | 未知 | unknown | unknown 警告（响应与列表均无法确认） |
| 10 | transportOrHTTPFailure | absent | **已撤销（lost response 对账）** | unknown | unknown 警告（请求结果未知；设备已从列表消失） |
| 11 | transportOrHTTPFailure | present | 未生效 | unknown | `devicesError`（现有失败语义；权威列表显示仍在） |
| 12 | transportOrHTTPFailure | failed | 未知 | unknown | `devicesError`（请求失败）+ 撤销状态未知；下次成功刷新自然对账 |

**特例**：`404 not_found × absent` → 服务端无此设备（含此前已撤销），无 cleanup
对象 → 无警告，按「设备已不在授权列表」呈现。

**三条最低规则（R3-B3）**：

1. reload `absent` 可以 reconcile lost response（行 7/10）——撤销尝试后**总是**
   执行 typed reload（v3 第 7 行「不 reload」作废）；
2. reload `present` 不能声称投递已阻断（行 2/5/8 呈现均不含该表述）；
3. reload `failed` 不能从旧 `devices` 数组推断结果（行 3/6/9/12 只依赖 typed
   seam 返回值）。

#### 4.3.4 文案（条件性自愈；删除不可执行动作与无效预设）

- `devices_push_cleanup_warning`（行 4/6；此态订阅确定存在，重试表述准确）：
  - en: "The device was revoked, but cleaning up its push subscription failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
  - zh: "设备已撤销，但其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
- `devices_push_cleanup_unknown`（行 7/8/9/10；**条件句**，以设备列表为权威）：
  - en: "The revocation outcome could not be confirmed, so the cleanup result of this device's push subscription is unknown. Use the device list as the source of truth: if the device has disappeared and its push subscription is still pending cleanup, the runtime retries it automatically on later notifications; if it still appears in the list, refresh and check the runtime log."
  - zh: "撤销结果无法确认，该设备推送订阅的清理结果未知。请以设备列表为准：若设备已从列表消失而订阅仍待清理，runtime 会在后续通知投递时自动重试；若设备仍显示在列表中，请刷新并查看 runtime 日志。"
- `devices_push_cleanup_inconsistent`（行 2/5；行 5 追加 cleanup 错误附注）：
  - en: "The runtime confirmed the revocation, but this device still appears in the authorized list. Refresh the list; if it persists, check the runtime log."
  - zh: "runtime 已确认撤销，但该设备仍出现在已授权列表中。请刷新列表；若持续显示，请查看 runtime 日志。"

#### 4.3.5 实施与测试清单

1. `RevokeDeviceResponse` 解码要求 `revoked == true`（缺键/false/类型错/非 JSON
   → `protocolUnknown`）。
2. 新增 typed reload seam（§4.3.2）；`revokeDevice` 决策只消费其返回值。
3. Reducer 按 §4.3.3 十二行矩阵 + 404 特例实现；撤销尝试后总是 reload。
4. **wire fixtures**（client 级，StubHTTPServer 模式）：response class 全覆盖
   ——`revoked:true`（±cleanupError）、`{}`、`revoked:false`、`"revoked":"yes"`、
   非 JSON、非 2xx、连接丢失（lost response）。
5. **ViewModel 测试**：12 行矩阵逐行（response class × reload class 组合）+
   404 特例 + presentation seam（`isRevokeCleanupWarningPresented` 发布 true /
   dismiss false，覆盖三种警告态）。
6. 定向测试 → Release 重建 + 覆盖安装 + 新代际核验。

## 5. 交付与部署证据（`124b73d` 历史记录；Round 1/2/3 已独立复核，本轮无新部署）

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

Round 2/3 评审实测：当前 runtime PID 34681（同版本 `124b73d7b0f2`）监听 8777；
该代际变化不用于证明 §2 首采数据。

### 5.3 验证状态

| 项 | 状态 |
| --- | --- |
| Go 定向测试 / race / vet | 全绿（Round 1 评审独立复跑；代码自 `124b73d` 未变化） |
| Swift 定向测试 | 21 条全绿（同上） |
| 部署 | §5.2；本轮（v4）零代码改动，无新构建/部署 |

## 6. 不采纳清单（评审建议未采纳项，逐项标明理由）

**Round 3 三项建议（提交归属更正、告警三类 OR 触发与 runtime.json bootstrap、
完整状态空间 reducer 与 typed reload seam）全部采纳，无新增不采纳项。** 以下为
Round 1 遗留的 3 项：

### 6.1 B2「补同一 PID 原始时间序列」——无法采纳，以撤回替代

PID 12110 已消亡，时间序列无法回溯采集（§2.2）。

### 6.2 B3 选项一「受控长窗口（关闭自动重启）」——不采纳为默认路径

理由：(1) 需要在 owner 日常使用的生产 Mac 上禁用 runtime 自动重启 27h+，风险与
信息价值不成比例；(2) 复采首要问题（真实负载下代际内观测上界）用多代际监测
即可回答。**升级条件**：§2.3.3 任一 provisional 告警触发时再评估，且必须 owner
明确授权。

### 6.3 B4「提供可达的『重试撤销』入口」——不采纳，采纳文案修正路径

撤销是一次性授权动作，撤销后设备从 `ListDevices` 消失；物理清理由服务端
deny-by-default + 后续 fan-out 自动重试自愈；用户侧重试入口不增加安全性，只增
UI 面积。

## 7. 遗留与下一步

1. **本文档过审后（开发阶段，一次完成）**：实施 §4.3（typed reload seam +
   十二行 reducer + 条件性文案 + wire/ViewModel fixtures + presentation seam）
   与 §2.3 监测脚本（含 §2.3.1 测试矩阵）→ 定向测试 → Release 重建 + 覆盖
   安装 + 新代际核验。
2. **监测执行**：按 §2.3.4 完成条件运行，最终报告回填 §2.4。
3. **owner 验收（自然路径与自动化验收分离，Round 2/3 已认可）**：
   - **owner 人工验收（唯一自然可达路径）**：Mac 端正常撤销一台真实设备 →
     设备从已授权列表消失、**无任何警告弹窗**。
   - **failure / unknown / inconsistent 三态及 lost-response 对账**：生产环境
     无安全自然触发方式（不注入故障、不加 demo 路径），由 §4.3.5 的
     deterministic client/ViewModel 测试验收。
