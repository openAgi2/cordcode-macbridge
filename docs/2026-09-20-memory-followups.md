# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v6，待复审）

> 状态：**待复审（Round 6）**。v5（`a50a5f9a766c9515614b523e6b88b8339ee79159`）
> 经 Round 5 评审（报告 `docs/2026-09-20-memory-followups-review-report-r5.md`，
> commit `f2c8a159db0342bc22f4bc55753030f2d2d477b0`）判定：Round 4 三项技术裁决
> （epoch 转换、4 样本趋势公式、confirmed-first 证据优先级）均已通过核验；剩余
> 3 个阻断项（R5-B1 监测脚本缺 management-token 鉴权 / R5-B2 四类 response 无
> API 边界类型且 rows 7/10 文案否定 reload 对账结果 / R5-B3 来源清单短哈希 +
> §5.3 重复行）。**v6 为 docs-only 修订：本轮零代码改动**。Round 5 三项建议全部
> 采纳（§6 无新增不采纳项）。
>
> 历史背景：r6 复审（报告 commit `35a1b8062fee8853180eb67f5572637731003fde`）
> 通过主修复（代码 `ee43c8f783710f826817c7a60109691e24689df7`）后记录三项非阻断
> 后续；owner 于 2026-09-20 指示全部落地。业务代码提交
> `124b73d7b0f2155ed19207c3ec6fe00cdf5fa888` 已完成 Release 构建、覆盖安装并验证
> 新代际（§5）。评审循环纪律（owner 2026-09-20 定案）：方案阶段文档循环直至
> 评审「通过」是进入开发阶段的唯一入口。

## 1. 来源清单（P0）

### 1.1 历史更正（Round 1 B1 / Round 3 R3-B1，均已处置）

v1 的 iOS 旧身份复用与 `124b73d` 误含 think.md 已于 v2 更正（think.md 首采补记
= `106bb95468e02c0b54b56b745846f4d16c494f3d`）；v3 的 think.md/followups 提交
归属误写已于 v4 更正（`d3c7404d72dedae981b71e6482218a366818c849` 只改 think.md、
`c976ef41ff9c0e88aaaf42e15f3fa02271812c41` 只改 followups 文档）。

### 1.2 本任务（v6 轮）三门点清单（R5-B3：全部使用完整哈希）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
门点1（读取源码/文档分析前）提交=f2c8a159db0342bc22f4bc55753030f2d2d477b0（Round 5 评审报告提交；工作树干净，git status --porcelain 无输出）
门点2（第一次修改文件前）提交=f2c8a159db0342bc22f4bc55753030f2d2d477b0（干净；本轮只改 think.md 与本文档，无业务代码修改）
门点3（构建/部署前）=不适用——本轮 docs-only（D0），无构建、无安装、无部署
本轮源码核读=go-bridge/main.go:766-775（management-token 每次 launch 在 ready frame 前原子重写，0600）、main.go:815-822（managementBridgeEpoch）、go-bridge/runtime_startup.go（runtime.json 字段），只读取证用于 §2.3 规格对齐，未修改
v5 历史来源事实（R5-B3 记录）=本文档 v5 提交 a50a5f9a766c9515614b523e6b88b8339ee79159；同轮 think.md 提交 a48d013775bf056990733e11eef0d91b63231454
think.md 同轮提交（v6 轮）=caa340f53d457539452b2f6074692ca2b208b1df（只改 think.md）
本文档（v6）提交=本提交（只改 docs/2026-09-20-memory-followups.md；最终哈希在送审说明中给出，不在正文构造自引用）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 389a179a1b01ad2a858f24b5b11ea41058e02381（门点1 实测，干净；与 Round 4/5 评审门点一致）
预期产品特性=本轮无产品代码变化；当前部署仍为 124b73d7b0f2（Round 5 评审实测）
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
  （当时经 lsof 探测端口；正式脚本改用 runtime.json + management-token bootstrap，见 §2.3.1）
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

### 2.3 复采设计（R2–R5 修订：runtime.json + management-token bootstrap + epoch 转换契约 + 原子采样事务 + 完成条件 + 负载覆盖门 + 三类独立 OR 告警）

**前提**：`autoRestartEnabled` 默认 true、`autoRestartIntervalMinutes` 默认 120
——代际长度上限 ~2h 是预期行为；多代际监测不回答 27h 同进程累积问题（受控长
窗口不采纳为默认，§6.2）。

**端点与 bootstrap 形状（源码核读，与 Round 2–5 评审实测一致）**：

- `runtime.json`（go-bridge `runtime_startup.go` `WriteReadyFrame` 原子写）：
  `managementUrl`（如 `http://127.0.0.1:61945`）、`pid`、`bridgeEpoch`——
  **`bridgeEpoch` 是 UUID 字符串**（如 `68fef32a-ef11-4c08-9392-bc1b7e32712e`）。
- `management-token`（同一 data dir，0600）：**Management API 鉴权 token**。
  `main.go:766-775`：product 模式下 runtime 在发布 ready frame **之前**原子重写
  该文件（写失败 fail-fast 退出）——即 token 文件**每次 launch 重写、跨代际可能
  轮换**。生产 Management API 要求 `Authorization: Bearer <token>`；不带 header
  实测返回 401（Round 5 评审核验）。正确生产读取方式见仓内活文档
  `BUILD_INSTALL_AND_RUNTIME.md`（实现锚点）。
- `GET /internal/status` → `runtimeIdentity = {pid, bridgeEpoch}`——**此处
  `bridgeEpoch` 是 uint64 数值**，由 `main.go:815-822 managementBridgeEpoch` 从
  UUID 字符串派生：`SHA-256(UUID 字符串字节)` 取**前 8 字节 big-endian uint64**，
  结果为 0 时改为 1。**两种 wire 表示不能直接相等比较**。
- `GET /internal/diagnostics/runtime` → `startedAt`（RFC3339）、
  `processUserCPUSeconds`/`processSystemCPUSeconds`（累计）、`backgroundTasks`、
  `activeBackgroundScans`（瞬时 gauge）、`agentBackgroundScans:<backendID>`（含
  `scans`/`successes`/`failures`/`turnItemRequests`/`scannedTurns` 累计计数）、
  `memory.*`——**不含 runtime identity**。
- 实测同一 runtime 进程同时监听 management（`127.0.0.1:61945`）与 bridge
  （`*:8777`）端口——**lsof「监听端口」无法唯一选择 management 端口**；lsof 仅
  作诊断交叉检查。

#### 2.3.1 原子采样事务（防混代协议，每样本固定六步；R5-B1 鉴权纳入契约）

1. **bootstrap（含鉴权）**：读 `runtime.json` 取 `managementUrl`、期望 `pid`、
   原始 UUID epoch；**同时读同目录 `management-token`**（生产读取方式以
   `BUILD_INSTALL_AND_RUNTIME.md` 为锚点）。token 文件缺失、空值或不可读 →
   rejected sample（独立原因码 `management_token_unavailable`）。
2. **identity A（Bearer）**：`GET /internal/status`（用 bootstrap URL，带
   `Authorization: Bearer <token>`）取 `A = (pid, uint64Epoch)`。核对：
   `A.pid == bootstrap.pid` **且** `A.epoch == deriveEpoch(bootstrap.UUID)`——
   `deriveEpoch` 与 `managementBridgeEpoch` **逐字节同算法**（SHA-256 over UUID
   字符串字节 → 前 8 字节 big-endian uint64 → 零值改 1）。不匹配（残留旧帧/新
   runtime 接管）→ 重读 runtime.json 一次，仍不匹配 → rejected sample
   （`bootstrap_mismatch`）。**HTTP 401/403 → rejected sample（`auth_rejected`，
   与网络/解析失败分开记录——区分 token/身份换代与普通命令失败）**。
3. **数据采集**：`GET /internal/diagnostics/runtime`（**同样带 Bearer header**；
   startedAt + CPU + 计数器 + memory）、`vmmap --summary A.pid`、
   `ps -o rss A.pid`、`sysctl vm.loadavg`（仅系统背景）。
4. **identity B（Bearer）**：再次 `GET /internal/status` 取 `B`。
5. **bootstrap 重读（含 token）**：再读一次 `runtime.json` **与
   `management-token`**，核对 URL / pid / 原始 UUID / **token 内容**均未变——
   token 每次 launch 被 runtime 原子重写（§2.3 端点形状），跨代际可能轮换；
   A/B 恰好命中同一 endpoint 但 bootstrap 文件已跨过换代窗口的混代由此拒绝。
6. **提交判定**：当且仅当 **A == B** 且**所有命令成功** 且 **diagnostics
   `startedAt` 与该代已记录的 startedAt 一致** 且 **bootstrap 重读（runtime.json
   + token）未变**时，写一条**有效样本**（单条 JSONL，含全部字段+时间戳）；
   否则写一条 **rejected sample**（原因码：`management_token_unavailable` /
   `bootstrap_mismatch` / `auth_rejected` / `identity_changed` /
   `command_failed` / `started_at_mismatch` / `bootstrap_rewritten`），
   **绝不把部分字段拼成有效样本**。

**token 安全边界（R5-B1）**：token 绝不写入 JSONL、日志、命令回显或 fixture；
脚本测试只用确定性假 token；归档文件权限不得宽于 token 文件（如 JSONL 0600），
不得因归档放宽 token 文件权限。

**epoch 转换契约测试（脚本实施时）**：

- 活体 fixture：UUID `68fef32a-ef11-4c08-9392-bc1b7e32712e` → epoch
  `2011574066258607221`（Round 4 评审实测）；
- 固定 fixture ≥1 个（实施时计算并钉死期望值）；
- 零值保护分支（hash 桩注入全零前 8 字节 → 返回 1）。

**样本时刻**：每代际内固定 `t=0/30/60/90`（分钟，对齐代际启动时刻）。t=0 =
检测到新代际（identity 或 startedAt 变化）后的第一个采样事务，立即执行。某时刻
事务 rejected 即该时刻掉样，**不补采、不重试凑数**；下一计划时刻继续。

**脚本测试矩阵（至少覆盖）**：正常样本（正确 token）/ A≠B 改代 / startedAt
变化 / 任一命令失败 / bootstrap mismatch / **缺 token / 空 token / 401 / 403** /
**事务中 token 或 runtime bootstrap 被替换（第 5 步检出）** / 单点绝对越线 /
footprint peak 越线 / 趋势触发 / **趋势阈值边界**（§2.3.3）。

#### 2.3.2 代际有效性与负载覆盖门（CordCode 自证负载，非整机 load）

- **有效代际** = 同一 (pid, uint64Epoch, startedAt) 下 ≥3 个有效样本（即代际
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

#### 2.3.3 provisional 告警线（三类**独立 OR** 触发，只升级不定论）

| 触发器 | 条件（精确公式） | 覆盖形状 |
| --- | --- | --- |
| ① 绝对越线 | 任一有效样本 `sysMinusHeapReleased` **current** > 256MiB（> 268,435,456 B） | 持续高位 |
| ② footprint 波峰 | 任一有效样本 vmmap footprint **current 或进程 lifetime peak**（`Physical footprint (peak)`）> 300MB | 两采样点之间单次大分配波后回落/高位持平——原事故形状（continuity cold scan 单波），①③都捕获不了 |
| ③ 趋势异常 | **4 个连续有效样本（t=0/30/60/90）构成 3 个相邻 delta，每个 delta ≥ 8,388,608 B（8MiB）**；总增量 ≥ 24MiB（25,165,824 B）/90min 由每步下限蕴含 | 稳定爬升的早期形态 |

- 三者**互不替代、独立 OR**：③不要求与①同时成立。
- **③的 8MiB 增量门**：与 256MiB/300MB 同级的 **provisional 运维启发式**——无
  生产分布数据支撑，不描述为「已测得的正常波动边界」；只影响是否升级取证。
  预固定值，变更须修订本文档。
- 256MiB 来源 = 512MiB 软限的 50%；300MB 来源 = 健康快照 47.5M 的 ~6 倍、旧症状
  2.5G 的 ~1/8。**均无生产分布数据支撑，纯早期预警**。
- **语义**：触发 → 仅决定「停止默认监测路径、评估升级取证（§6.2，需 owner
  明确授权）」。**未触发不能推出任何结论**。
- **能力边界**：本监测无 GC pause/alloc counter，**不能裁决 GOGC 或 512MiB
  最优值**，只能决定是否需要升级取证。
- **③边界 fixtures（脚本测试）**：delta 恰等于 8,388,608 B（触发——`≥` 含等）/
  差 1 byte（8,388,607 B，不触发）/ 非严格上升序列（含持平或下降步，不触发）/
  步数不足（仅 2 个 delta，不触发）。

#### 2.3.4 完成条件（预固定；变更须修订本文档；Round 3 已认可）

监测**完成**当且仅当：①墙钟 ≥ **7 个自然日**（覆盖工作日+周末使用模式）；
②有效代际 ≥ **20 个**；③其中有载代际 ≥ **8 个**。

- **提前终止**：任一 provisional 告警触发 → 停止默认路径并升级（§6.2）。
- **掉样处理**：rejected 只计入统计；完成条件只数有效样本/代际；7 天后不足则
  **延长窗口直至满足并记录原因，不降低门槛**。
- **实施形态**：过审后实现为本地归档脚本（非产品代码、不进 app bundle、不改
  go-bridge），按 §2.3.1 事务与 §2.3.4 条件执行，测试覆盖 §2.3.1 矩阵与
  §2.3.3 边界。

### 2.4 最终报告形状（预留；监测完成后回填）

- 每指标 **observed max**（按代际、按全局）与分布（min/p50/p95）、有载/空闲
  代际覆盖度、rejected 样本数与原因分布、告警触发记录（含②的 lifetime peak）。
- 结论措辞限定为描述性：「在 X 个有效代际（其中有载 Y 个）、Z 天窗口内，观测
  最大 sysMinusHeapReleased = …、footprint（含 lifetime peak）= …」；**不写**
  「治理有效」「512MiB 合理」「GOGC 应调」。
- 附 §2.3.3 能力边界声明。

## 3. API 硬化（r6 后续 2；已完成，Round 1–5 评审已独立复核通过）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive` 改为
`WebPushDeviceUnspecified`（拒绝但不清理）。dispatcher `default` 分支天然覆盖
零值。测试零值用例：0 次 HTTP 请求 + 订阅保留。本轮无变化。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3；B4 方案 v6）

### 4.1 已交付（`124b73d`）

`DeviceRevocation`/`revokeCleanupWarning`/`.alert`/双语 L10n/`DeviceStoreTests`
2 条新用例 + `WorkspaceViewTests` stub（Swift 定向 21 条全绿）。

### 4.2 评审发现的问题（Round 1 B4 + R2-B3 + R3-B3 + R4-B3 + R5-B2，全部成立）

1. 文案「可重试撤销」不可执行（Round 1，v2 已改）。
2. `try?` 解码使「解码失败」与「无清理错误」静默等价（Round 1，v2 已改）。
3. optional `revoked` 使 `{}` 解码成清理成功（R2-B3，v3 已改）。
4. unknown 文案须条件句、撤销生效以 reload 为权威（R2-B3，v3 已改）。
5. 7 行决策表非完整状态空间、`loadDevices()` Void 不足以支撑 reducer（R3-B3，
   v4 已改 12 行 + typed outcome）。
6. confirmed × present 冲突无统一证据优先级（R4-B3，v5 已改 confirmed-first 规则）。
7. **R5-B2**：四类 response class 没有 API 边界类型承载——现协议
   `revokeDevice(...) throws -> DeviceRevocation` 只能表达可选 `pushCleanupError`，
   `performRequest` 非 2xx 只抛 `ManagementError.httpError(Int)`；「解码失败 →
   protocolUnknown」无法告诉 DeviceStore 如何接收状态、如何区分 404 特例，stub
   也无法为 12 行矩阵提供稳定输入。
8. **R5-B2**：rows 7/10 已由 reload absent 确认撤销，却共用「撤销结果无法确认」
   文案——与 §4.3.1 的 OR 规则相反。

### 4.3 过审后实施方案（本轮不动代码；过审后一次实施 + 定向测试 + 重建部署）

#### 4.3.1 唯一证据优先级（R4-B3，Round 4/5 已核验；先定规则再生成矩阵）

**服务端事实（Round 4 评审核验）**：`handleRevokeDevice` 只有在
`DeviceStore.RevokeDevice` **成功持久化**后才返回 `revoked:true`（失败返回
404，体含 `{"error":"not_found",...}`）；`pushCleanupError` 是成功后的附加字段；
`ListDevices` 从**同一 store** 排除 revoked 记录。因此 confirmed × present 是
协议/存储一致性异常，**不是**撤销未生效的证据。

**优先级规则（唯一，机械适用全部 12 行）**：

1. **撤销生效判定**：confirmed response（`revoked:true`）与 reload `absent`
   **各自单独**确认「已撤销」（OR）。
2. **confirmed × present**：不降级撤销结论（安全语义来自 confirmed response），
   列表只触发**一致性告警**。
3. **投递阻断声明的证据 = confirmed 撤销 + confirmed cleanup failure**，与 reload
   结果无关——行 4/5/6 同一份证据 → **同一结论**。
4. **无 confirmed response**（protocolUnknown / transportOrHTTPFailure）时，
   reload 是唯一当前证据：`present` → 未生效/倾向未生效，不声称阻断；`absent`
   → 已撤销（对账）；`failed` → 未知。
5. **reload `failed` 永远不从旧 `devices` 数组推断任何结论**。

#### 4.3.2 API 边界契约：typed revoke-attempt outcome（R5-B2 择一：修改
`DeviceAPIProviding.revokeDevice` 返回类型）

**选择**：修改 `DeviceAPIProviding.revokeDevice` 的返回类型为协议可见的 typed
outcome（不采用 typed error 方案）；**任何结果都必须继续 typed reload，不能因
throw 提前跳过**。

```swift
/// 撤销尝试的 API 边界契约：四类 response class + 可诊断原因（不泄漏响应正文）。
/// 取代 124b73d 交付的 DeviceRevocation 在 API 边界的位置（§4.1 保留其历史名）。
enum RevokeAttemptOutcome: Sendable, Equatable {
    case confirmedClean
    case confirmedCleanupFailure(pushCleanupError: String)
    case protocolUnknown(RevokeProtocolIssue)
    case transportOrHTTPFailure(RevokeTransportIssue)
}

enum RevokeProtocolIssue: Sendable, Equatable {
    case emptyBody            // 200 但体为空对象 {}
    case missingRevokedKey    // 缺 revoked 键
    case revokedFalse         // revoked == false
    case revokedTypeMismatch  // revoked 类型错误（如 "yes"）
    case malformedJSON        // 非 JSON 体
}

enum RevokeTransportIssue: Sendable, Equatable {
    case networkError
    case httpStatus(status: Int, serverErrorCode: String?)
    // serverErrorCode：非 2xx 时尽力解码响应体 {"error": ...} 字段（如 404
    // not_found）；解码失败为 nil。404 特例 = status==404 且 code=="not_found"，
    // 不把所有 404 等同（R5-B2）。
}
```

**`ManagementAPIClient.revokeDevice` 映射规则**：

- `performRequest` 抛 `ManagementError.httpError(status)` → 尽力解码响应体
  `error` 字段 → `.transportOrHTTPFailure(.httpStatus(status, code))`；其他异常
  （网络等）→ `.transportOrHTTPFailure(.networkError)`。**不再向上抛撤销路径
  错误**（全部映射进 outcome）。
- 2xx 体解码：`revoked == true` → `confirmedClean` /
  `confirmedCleanupFailure(pushCleanupError)`；否则按具体失败形状映射
  `.protocolUnknown(...)` 五个 case。
- **`DeviceStore.revokeDevice` 收到任何 outcome 都执行 typed reload**，再按
  §4.3.3 矩阵呈现。

**typed reload outcome（归属状态管理层，Round 4 已定）**：保持在 `DeviceStore`
内部——包装现有 `DeviceAPIProviding.listDevices() async throws -> [TrustedDevice]`
为 typed outcome（如 `Result<[TrustedDevice], Error>`）；**不给 API 协议新增重复
RPC**。reducer 只消费该返回值，不读陈旧 `devices` 数组；现有 `loadDevices()`
（Void）保留为列表刷新入口，内部复用同一包装。

**stub 接口**：`DeviceAPIStub.revokeDevice` 返回 `RevokeAttemptOutcome` 枚举值
——12 行矩阵的稳定测试输入；`listDevices` stub 控制 reload 三分支。

#### 4.3.3 完整决策矩阵（4 × 3 = 12 行，由 §4.3.1 规则机械生成；测试逐行覆盖）

| # | response class | reload class | 生效判定 | cleanup 信息 | 呈现 |
| --- | --- | --- | --- | --- | --- |
| 1 | confirmedClean | absent | 已撤销（双确认） | clean | 无警告 |
| 2 | confirmedClean | present | 已撤销 + 列表不一致 | clean | inconsistent 警告 |
| 3 | confirmedClean | failed | 已撤销（response 确认） | clean | 无撤销警告；列表刷新失败由 reload 路径自身呈现（`devicesError`），不从旧数组推断 |
| 4 | confirmedCleanupFailure | absent | 已撤销（双确认） | failed(err) | cleanup-failure 警告（含投递阻断声明） |
| 5 | confirmedCleanupFailure | present | 已撤销 + 列表不一致 | failed(err) | inconsistent 警告 + 清理失败附句（**含投递阻断声明——与行 4/6 同一证据同一结论**，另提示列表异常） |
| 6 | confirmedCleanupFailure | failed | 已撤销（response 确认） | failed(err) | cleanup-failure 警告（含投递阻断声明）；列表刷新失败另行呈现 |
| 7 | protocolUnknown | absent | 已撤销（reload 对账） | unknown | **cleanup-pending 警告**（撤销已由列表确认，仅 cleanup 未知——§4.3.4 拆分） |
| 8 | protocolUnknown | present | 倾向未生效（无 confirmed response，列表为当前证据） | unknown | **unknown 警告**（撤销结果未知；设备仍在列表，可能未生效） |
| 9 | protocolUnknown | failed | 未知 | unknown | **unknown 警告**（撤销结果与 cleanup 结果均未知） |
| 10 | transportOrHTTPFailure | absent | **已撤销（lost response 对账）** | unknown | **cleanup-pending 警告**（撤销已由列表确认，仅 cleanup 未知） |
| 11 | transportOrHTTPFailure | present | 未生效 | unknown | `devicesError`（现有失败语义） |
| 12 | transportOrHTTPFailure | failed | 未知 | unknown | `devicesError`（请求失败）+ 撤销状态未知；下次成功刷新自然对账 |

**特例**：`404 not_found`（`RevokeTransportIssue.httpStatus(404, "not_found")`）
× absent → 服务端无此设备（含此前已撤销），无 cleanup 对象 → 无警告，按「设备
已不在授权列表」呈现。其他 404（code 为 nil 或非 not_found）→ 行 11/12 语义。

**最低规则（由 §4.3.1 机械导出）**：

1. 撤销尝试后**总是**执行 typed reload；reload `absent` 可单独确认撤销并对账
   lost response（行 7/10）。
2. confirmed response 可单独确认撤销（行 2/3/5/6）；与 reload 冲突时**不降级撤销
   结论**，只追加一致性告警（行 2/5）；投递阻断声明只依赖 confirmed 证据，与
   reload 结果无关（行 4/5/6 同结论）。
3. 无 confirmed response 时 reload 是唯一当前证据：`present` → 未生效/倾向未
   生效（行 8/11），不声称投递阻断。
4. reload `failed` 不从旧 `devices` 数组推断任何结论（行 3/6/9/12）。

#### 4.3.4 文案（R5-B2 拆分两类 unknown；L10n 组合而非嵌套格式化）

- `devices_push_cleanup_warning`（行 4/6 主文案；此态订阅确定存在，重试表述
  准确）：
  - en: "The device was revoked, but cleaning up its push subscription failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
  - zh: "设备已撤销，但其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
- `devices_push_cleanup_pending`（**行 7/10 专用**：撤销已由列表确认，仅 cleanup
  结果未知——首句不得出现「无法确认撤销」）：
  - en: "The device no longer appears in the authorized list, so the revocation is confirmed. The cleanup result of its push subscription is unknown; if it is still pending, the runtime retries it automatically on later notifications. Check the runtime log if needed."
  - zh: "该设备已从已授权列表消失，撤销已确认。其推送订阅的清理结果未知；若仍待清理，runtime 会在后续通知投递时自动重试；必要时可查看 runtime 日志。"
- `devices_push_cleanup_unknown`（**行 8/9 专用**：撤销结果与 cleanup 结果均
  未知；行 8 另由列表状态体现「可能未生效」）：
  - en: "The revocation outcome could not be confirmed, and the cleanup result of this device's push subscription is also unknown. Refresh the device list: if the device still appears, the revoke may not have taken effect — check the runtime log; if it has disappeared, the runtime retries any pending cleanup automatically on later notifications."
  - zh: "撤销结果无法确认，该设备推送订阅的清理结果也未知。请刷新设备列表：若设备仍显示在列表中，撤销可能未生效，请查看 runtime 日志；若已消失，仍待清理的订阅由 runtime 在后续通知投递时自动重试。"
- `devices_push_cleanup_inconsistent`（行 2/5 基础文案）：
  - en: "The runtime confirmed the revocation, but this device still appears in the authorized list. Refresh the list; if it persists, check the runtime log."
  - zh: "runtime 已确认撤销，但该设备仍出现在已授权列表中。请刷新列表；若持续显示，请查看 runtime 日志。"
- `devices_push_cleanup_inconsistent_detail`（行 5 追加句，与基础文案**拼接**为
  同一 alert 的消息体；行 2 不追加）：
  - en: " Its push subscription cleanup also failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications."
  - zh: "其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理。"

#### 4.3.5 实施与测试清单

1. `RevokeAttemptOutcome` 契约落地（§4.3.2）：`DeviceAPIProviding.revokeDevice`
   返回类型修改 + `ManagementAPIClient` 映射（非 2xx 解码 `error` code；2xx 按
   `revoked == true` / 五种 protocolUnknown 形状）。
2. `DeviceStore` 内 typed reload outcome 包装（复用 `listDevices()`）；reducer
   只消费 (outcome, reloadOutcome)，任何 outcome 后总是 reload。
3. Reducer 按 §4.3.1 规则机械实现（十二行矩阵 + 404 特例）。
4. **wire fixtures**（client 级，StubHTTPServer 模式）：response class 全覆盖
   ——`revoked:true`（±cleanupError）、`{}`、`revoked:false`、`"revoked":"yes"`、
   非 JSON、非 2xx（404 带/不带 `not_found` code、500）、连接丢失（lost
   response）。
5. **ViewModel 测试**：12 行矩阵逐行 + 404 特例 + 行 5 组合文案断言（同时含
   投递阻断句与列表异常句）+ **两类 unknown 文案不可互换断言**（行 7/10 消息
   必含「撤销已确认」且不含「无法确认撤销」；行 8/9 相反——R5-B2）+
   presentation seam（`isRevokeCleanupWarningPresented` 发布 true / dismiss
   false，覆盖四种警告态）+ typed outcome 包装测试（成功/失败两分支，失败不读
   旧数组）。
6. 定向测试 → Release 重建 + 覆盖安装 + 新代际核验。

## 5. 交付与部署证据（`124b73d` 历史记录；Round 1–5 已独立复核，本轮无新部署）

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

Round 2–5 评审实测：当前 runtime PID 34681（同版本 `124b73d7b0f2`）监听 8777；
该代际变化不用于证明 §2 首采数据。

### 5.3 验证状态（R5-B3 清理：测试结果不再重复维护，以评审报告独立复跑记录为准）

代码自 `124b73d7b0f2155ed19207c3ec6fe00cdf5fa888` 未变化；Go/Swift 定向测试、
race、vet 的独立复跑记录见 Round 1–5 评审报告 §2/§4。本轮（v6）零代码改动，
无新构建/部署。

## 6. 不采纳清单（评审建议未采纳项，逐项标明理由）

**Round 5 三项建议（token 鉴权纳入采样契约、typed revoke-attempt 契约、两类
unknown 文案拆分 + 完整哈希 + §5.3 清理）全部采纳，无新增不采纳项。** 现存
不采纳项为 Round 1 的 3 项与 Round 4 的 1 项：

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

### 6.4 R4-B1 备选「PID-only bootstrap（不比较 epoch）」——不采纳

理由：降低 epoch 防混代强度——PID 复用窗口内无法区分代际。采纳主方案：脚本
实现与 `managementBridgeEpoch` 逐字节相同的转换（§2.3.1 第 2 步 + 转换契约
测试）。

## 7. 遗留与下一步

1. **本文档过审后（开发阶段，一次完成）**：实施 §4.3（`RevokeAttemptOutcome`
   契约 + typed reload outcome + 规则化 reducer + 拆分文案 + wire/ViewModel
   fixtures + presentation seam）与 §2.3 监测脚本（含 §2.3.1 鉴权与测试矩阵、
   §2.3.3 边界 fixtures）→ 定向测试 → Release 重建 + 覆盖安装 + 新代际核验。
2. **监测执行**：按 §2.3.4 完成条件运行，最终报告回填 §2.4。
3. **owner 验收（自然路径与自动化验收分离，Round 2–5 已认可）**：
   - **owner 人工验收（唯一自然可达路径）**：Mac 端正常撤销一台真实设备 →
     设备从已授权列表消失、**无任何警告弹窗**。
   - **failure / unknown / pending / inconsistent 四态及 lost-response 对账**：
     生产环境无安全自然触发方式（不注入故障、不加 demo 路径），由 §4.3.5 的
     deterministic client/ViewModel 测试验收。
