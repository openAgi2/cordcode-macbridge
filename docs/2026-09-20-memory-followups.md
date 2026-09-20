# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v7，待终审）

> 状态：**待终审（Round 7）**。v6（`6987fce160c5ec4473bdde725b85665725d73ae9`）
> 经 Round 6 评审（报告 `docs/2026-09-20-memory-followups-review-report-r6.md`，
> commit `b4db658718dddfe710495977bc316c19f14fd45d`）判定：Round 5 三项已实质处置，
> 剩余 3 个完整阻断簇（采集安全与解析契约 / 7 天调度与覆盖度 / revoke wire 可实
> 现性）共 9 项终审准入条件。**v7 为 docs-only 修订：本轮零代码改动**，按
> §8 勾销表逐项关闭 9 条准入条件；Round 6 各选择点全部采纳或择一落地（决策表
> 见 §6 头部），无新增不采纳项。
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

### 1.2 本任务（v7 轮）三门点清单（全部完整哈希）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
门点1（读取源码/文档分析前）提交=b4db658718dddfe710495977bc316c19f14fd45d（Round 6 评审报告提交；工作树干净，git status --porcelain 无输出）
门点2（第一次修改文件前）提交=b4db658718dddfe710495977bc316c19f14fd45d（干净；本轮只改 think.md 与本文档，无业务代码修改）
门点3（构建/部署前）=不适用——本轮 docs-only（D0），无构建、无安装、无部署
本轮源码核读=MacBridge/MacBridge/Services/RuntimeManager.swift:622-624（autoRestartEnabled/autoRestartIntervalMinutes UserDefaults 键，只读探测源）、MacBridge/MacBridge/Views/SettingsView.swift:13-14（@AppStorage 默认值）、go-bridge/main.go:766-775/815-822、runtime_startup.go（前轮已核读部分复核），只读取证用于规格对齐，未修改
v6 历史来源事实=本文档 v6 提交 6987fce160c5ec4473bdde725b85665725d73ae9；同轮 think.md 提交 caa340f53d457539452b2f6074692ca2b208b1df
think.md 同轮提交（v7 轮）=8938a018a74ccda3f478401ffd2b82789dfec1c2（只改 think.md）
本文档（v7）提交=本提交（只改 docs/2026-09-20-memory-followups.md；最终哈希在送审说明中给出，不在正文构造自引用）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 389a179a1b01ad2a858f24b5b11ea41058e02381（门点1 实测，干净；与 Round 4–6 评审门点一致）
预期产品特性=本轮无产品代码变化；当前部署仍为 124b73d7b0f2（Round 6 评审实测）
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

### 2.3 复采设计（R2–R6 修订总集）

**前提**：`autoRestartEnabled` / `autoRestartIntervalMinutes` 是可变 UserDefaults
（`RuntimeManager.swift:622-624` 周期重读；默认 true/120 只是缺省值）——监测
期间**实际值**必须记录（§2.3.3），不得以默认值冒充运行时事实。多代际监测不
回答 27h 同进程累积问题（受控长窗口不采纳为默认，§6.2）。

**端点与 bootstrap 形状（源码核读，与 Round 2–6 评审实测一致）**：

- `runtime.json`（`runtime_startup.go` `WriteReadyFrame` 原子写）：
  `managementUrl`、`pid`、`bridgeEpoch`（**UUID 字符串**）。
- `management-token`（同一 data dir，0600）：Management API 鉴权 token；product
  模式下每次 launch 在 ready frame 前原子重写（`main.go:766-775`）——跨代际
  可能轮换。API 要求 `Authorization: Bearer <token>`，无 header 实测 401。生产
  读取锚点：`BUILD_INSTALL_AND_RUNTIME.md`。
- `GET /internal/status` → `runtimeIdentity = {pid, bridgeEpoch(uint64)}`——
  数值 epoch 由 `main.go:815-822 managementBridgeEpoch` 派生（SHA-256 前 8 字节
  big-endian uint64，0→1），与 UUID 字符串**不能直接相等比较**。
- `GET /internal/diagnostics/runtime` → `startedAt`、`processUserCPUSeconds`/
  `processSystemCPUSeconds`（累计）、`processCPUAvailable`、`backgroundTasks`、
  `activeBackgroundScans`（瞬时）、`agentBackgroundScans:<backendID>`（累计：
  `scans`/`successes`/`failures`/`turnItemRequests`/`scannedTurns`）、`memory.*`
  ——不含 runtime identity。
- 实测同进程监听 management（`127.0.0.1:61945`）与 bridge（`*:8777`）——lsof
  无法唯一选择 management 端口，仅作诊断交叉检查。

#### 2.3.1 采集事务与安全契约（六步 + R6-B1.1/2/3）

**URL 严格校验（发任何请求之前；R6-B1.1）**：解析 `managementUrl`，要求全部
满足，否则 rejected（`bootstrap_invalid`，不崩溃）：

- scheme 只能是 `http`；
- host 只能是数值 loopback 白名单 **{`127.0.0.1`, `::1`}**（显式列出；生产值
  `127.0.0.1`）；
- 禁止 userinfo；path 为空或 `/`；无 query、无 fragment；port ∈ 1...65535。

`runtime.json` 缺失/不可读 → `bootstrap_unavailable`；JSON 无效/字段类型错/URL
缺失 → `bootstrap_invalid`——均与 `command_failed` 分开，脚本不得崩溃。

**HTTP 客户端（R6-B1.1/2）**：

- **进程内 HTTP 库**（禁止 `curl`/`wget` 等子进程 HTTP——token 会进 argv）；
  token 只存在于进程内存变量，不进 argv、环境变量、临时文件、错误文本。
- **禁止自动 redirect**：任何 3xx → rejected（`redirect_rejected`），绝不把
  Authorization 转发到 Location。
- 错误归档只保留 status/reason code，不保留完整 request/headers。

**归档权限（R6-B1.2）**：归档目录 0700、文件 0600；**启动时验证权限**（不只
创建时设置），不匹配则修复或拒绝启动并报告。

**硬超时（R6-B1.3，预固定；变更须修订本文档）**：单 HTTP 请求 **10s**；单本地
命令（vmmap/ps/sysctl）**15s**；**整笔事务 90s**（含 identity B 与 bootstrap
重读——不得用无界等待换原子性）。超时 → 终止**本事务创建的**子进程并记录
`command_timeout`；下一计划槽不受影响。

**六步事务**（每样本）：

1. **bootstrap（含鉴权）**：读 `runtime.json`（URL 校验如上）+ `management-token`
   （缺失/空/不可读 → `management_token_unavailable`）。
2. **identity A（Bearer）**：`GET /internal/status` 取 `A=(pid, uint64Epoch)`；
   核对 `A.pid == bootstrap.pid` 且 `A.epoch == deriveEpoch(bootstrap.UUID)`
   （`deriveEpoch` 与 `managementBridgeEpoch` 逐字节同算法）。不匹配 → 重读
   runtime.json 一次，仍不匹配 → `bootstrap_mismatch`。401/403 →
   `auth_rejected`（与网络/解析失败分开）。
3. **数据采集**：`GET /internal/diagnostics/runtime`（Bearer；字段校验见
   §2.3.2）、`vmmap --summary A.pid`、`ps -o rss= A.pid`、`sysctl vm.loadavg`
   （仅系统背景）。
4. **identity B（Bearer）**：再次 `GET /internal/status` 取 `B`。
5. **bootstrap 重读（含 token）**：再读 `runtime.json` 与 `management-token`，
   URL/pid/原始 UUID/token 内容均未变，否则 `bootstrap_rewritten`。
6. **提交判定**：A == B 且所有命令成功且 `startedAt` 与该代已记录值一致且
   bootstrap 重读未变 → **有效样本**（单条 JSONL）；否则 rejected（原因码：
   `bootstrap_unavailable` / `bootstrap_invalid` / `management_token_unavailable`
   / `bootstrap_mismatch` / `redirect_rejected` / `auth_rejected` /
   `command_failed` / `command_timeout` / `payload_invalid` / `identity_changed`
   / `started_at_mismatch` / `bootstrap_rewritten`），**绝不拼部分字段**。

**epoch 转换契约测试**：活体 fixture（UUID `68fef32a-ef11-4c08-9392-bc1b7e32712e`
→ `2011574066258607221`）；固定 fixture ≥1；零值保护分支（hash 桩全零前 8 字节
→ 1）。

**安全/超时 fixtures**：外部 host、非 http、userinfo、redirect（3xx）、非法/
缺失 port、残缺 runtime.json、每类超时、子进程退出回收、下一计划槽仍可执行、
成功/失败输出扫描假 token 不出现在 stdout/stderr/JSONL/异常文本。

#### 2.3.2 外部输出解析契约（R6-B1.4）

**精确 byte 常量（与其他门统一为二进制）**：告警②阈值 = **300MiB =
314,572,800 B，严格大于（>）**（同族：256MiB = 268,435,456 B；8MiB =
8,388,608 B）。

**vmmap parser**：从**真实归档的 `vmmap --summary` 输出（脱敏 fixture）**解析
current `Physical footprint:`、`Physical footprint (peak)`、`TOTAL SWAPPED`
（报告用）。单位表：K=1024、M=1,048,576、G=1,073,741,824（vmmap 二进制惯例；
以真实 fixture 钉死——若实际输出与该表不符，以 fixture 为准并回填本文档）；
小数与空格变化均有测试。

**解析失败策略（择一写死）**：**指标级 unavailable**——peak 缺失、重复行、
未知单位、非数字 → 该指标标 `unavailable`（**不按 0**），样本其余指标仍有效，
告警②覆盖度下降并在最终报告计入 unavailable 数。**命令执行失败**（非零退出/
超时/PID 不存在）→ 整笔 rejected（`command_failed`）。

**`ps -o rss=`**：无表头形状；KB→B ×1024；PID 不存在 → 命令失败 → 整笔
`command_failed`。

**HTTP JSON 必需字段/类型校验**：`startedAt`（string RFC3339）、`memory.{sys,
heapSys,heapInuse,heapIdle,heapReleased,sysMinusHeapReleased,heapObjects,stackSys,
numGC}`（数值）、`processUserCPUSeconds`/`processSystemCPUSeconds`（数值）、
`processCPUAvailable`（bool）——缺失或类型错 → **不得以零值制造有效样本** →
rejected（`payload_invalid`）。`processCPUAvailable == false` → CPU 指标标
unavailable（负载门回落到 scan 计数器）；`agentBackgroundScans:*` 可选（按
backend 存在）。

**fixture 分层（R6-B1.4）**：**parser fixture 与纯算法 fixture 分开**——vmmap/ps
用真实归档输出脱敏 fixture（防自造字符串自证）；epoch 转换/阈值边界用合成
fixture。

#### 2.3.3 调度与恢复模型（R6-B2.1/2/3）

**代际原点（R6-B2.1 择一：diagnostics `startedAt`）**：slot k = startedAt +
30k 分钟（k=0..3）；**slot 窗口 = [t_k, t_k+15min)**（含左不含右），样本时间戳
落入即归属该 slot；窗口外记 `out_of_slot`（不用于趋势）；每 slot 至多一个有效
样本（后到重复记 `duplicate`，保留首个）。**v6 的「对齐代际启动时刻」与
「检测后 t=0」双重定义作废**——检测晚于 slot 的历史 slot 记 `missed`，不补采。

**代际发现节奏**：每 60s 检查 bootstrap（runtime.json 内容/stat）与 runtime
存活性；变化 → 新代际发现流程（立即执行首个可用 slot 事务）。

**时钟跳变**：样本时间戳 < 上一样本 → 该对不参与趋势，记 `clock_anomaly`；
监测窗口不因回拨缩短（见下）。

**拉起方式（R6-B2.3 择一：launchd user agent）**：

- plist（Label、ProgramArguments、`KeepAlive={SuccessfulExit:false}`——崩溃
  自动拉起；**告警停止后干净退出不复活**）、日志路径、卸载方式（`launchctl
  bootout gui/$(id -u) …`）在实施时随脚本交付并写入本文档回填。
- **单实例锁**：状态文件 flock + PID。
- **状态/JSONL 原子持久化**（tmp+rename）；启动时读状态文件恢复当前代际/slot
  进度；**partial line 检测**（JSONL 末行不完整 → 丢弃记 `corrupt_line`）。
- sleep/wake、脚本停机、runtime 不存在：对应计划槽分别记 missed（原因
  best-effort：`sleep` / `script_down` / `runtime_absent`）；**醒来不补造旧
  样本**。
- **「≥7 个自然日」精确定义**：前向推进墙钟 ≥168h（检测到时钟回拨按回拨量
  顺延，不缩短）**且**覆盖 ≥7 个本地日历日。
- **告警停止标志持久化**：provisional 告警触发 → 写停止标志 + 干净退出；
  重启后读到标志立即退出，不悄悄继续采集。
- **实际 restart policy 记录（R6-B2.2）**：监测开始与每次检测到变化时，只读
  探测 Mac App UserDefaults（键 `autoRestartEnabled` / `autoRestartIntervalMinutes`，
  `RuntimeManager.swift:622-624` 同源；app domain 实施时核对并回填本文档），
  **不得修改**；最终报告含各时点实际值。

**调度/恢复测试（fake clock/state store）**：重启恢复、双实例、sleep 跳槽、
重复唤醒、部分写入、告警后恢复、晚发现、slot 边界（R6-B2.1 fixture）。

#### 2.3.4 代际分层与负载覆盖门（R6-B2.4）

代际分**三层**（同代际首末有效样本之间）：

| 层 | 判定 | 语义 |
| --- | --- | --- |
| `scan-evidenced` | 任一 `agentBackgroundScans:<backend>` 累计计数（`scans`/`turnItemRequests`/`scannedTurns`）delta > 0 | **唯一算「真实工作负载证据」的层** |
| `cpu-active-only` | 仅 `processUserCPUSeconds + processSystemCPUSeconds` delta ≥ 60s，无扫描证据 | 只证明 runtime 活跃（可能来自忙循环/维护任务/缺陷），**不得称为真实负载** |
| `idle/low-activity` | 两者皆无 | 空闲观测 |

- 60s 与 256MiB/300MiB/8MiB 同级 **provisional**（无分布数据支撑）。
- 完成条件「≥8 活跃代际」口径修正：**≥8 个 `scan-evidenced`**；
  `cpu-active-only` 单列分母，不并入、不合并宣称真实负载覆盖。
- `sysctl vm.loadavg` 仅系统背景，不作 CordCode 负载证据。

#### 2.3.5 provisional 告警线（三类**独立 OR** 触发，只升级不定论）

| 触发器 | 条件（精确公式） | 覆盖形状 |
| --- | --- | --- |
| ① 绝对越线 | 任一有效样本 `sysMinusHeapReleased` **current** > 268,435,456 B（256MiB） | 持续高位 |
| ② footprint 波峰 | 任一有效样本 vmmap footprint **current 或进程 lifetime peak**（`Physical footprint (peak)`）> **314,572,800 B（300MiB，R6-B1.4 精确化）** | 两采样点之间单次大分配波后回落/高位持平——原事故形状 |
| ③ 趋势异常 | **4 个连续有效样本（完整 slot 序列）构成 3 个相邻 delta，每个 delta ≥ 8,388,608 B（8MiB）**；总增量 ≥ 24MiB/90min 由每步下限蕴含 | 稳定爬升的早期形态 |

- 三者互不替代、独立 OR；③不要求与①同时成立。
- 8MiB/256MiB/300MiB 均为 **provisional 运维启发式**（无生产分布支撑，只影响
  是否升级取证）；预固定，变更须修订本文档。
- **语义**：触发 → 仅「停止默认监测、评估升级取证（§6.2，需 owner 授权）」；
  **未触发不能推出任何结论**。「未触发趋势」**必须同时报告可评估分母**（§2.4）。
- **能力边界**：无 GC pause/alloc counter，不能裁决 GOGC 或 512MiB 最优值。
- **③边界 fixtures**：delta 恰等于 8,388,608 B（触发）/ 差 1 byte（不触发）/
  非严格上升（不触发）/ 步数不足（不触发）。

#### 2.3.6 完成条件（预固定；变更须修订本文档）

监测**完成**当且仅当：①窗口满足 §2.3.3 精确定义（≥168h 前向墙钟 + ≥7 本地
日历日）；②有效代际（≥3 有效样本）≥ **20 个**；③**scan-evidenced 代际 ≥
8 个**（R6-B2.4 口径）。

- **提前终止**：任一 provisional 告警触发 → 写停止标志并升级（§6.2）。
- **掉样处理**：rejected/missed 只计入统计；完成条件只数有效样本/代际；窗口
  不足则延长并记录原因，不降门槛。

### 2.4 最终报告形状（预留；监测完成后回填）

- 每指标 **observed max**（按代际、按全局）与分布（min/p50/p95）、**各代际
  真实 observed duration**（不笼统称「120 分钟上界」——R6-B2.2）。
- **覆盖度分母（R6-B2.2）**：有效代际数（≥3 样本）；**趋势可评估代际数**
  （完整 4 slot）；current/peak 指标可评估样本数及 parser `unavailable` 数；
  三层负载分母（scan-evidenced / cpu-active-only / idle）。
- **restart policy 记录**：各时点实际 `autoRestartEnabled` /
  `autoRestartIntervalMinutes`（只读探测值）。
- rejected/missed 样本数与原因分布、告警触发记录（含② lifetime peak）。
- 结论措辞限定为描述性；**不写**「治理有效」「512MiB 合理」「GOGC 应调」；
  附 §2.3.5 能力边界声明。

## 3. API 硬化（r6 后续 2；已完成，Round 1–6 评审已独立复核通过）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive` 改为
`WebPushDeviceUnspecified`（拒绝但不清理）。dispatcher `default` 分支天然覆盖
零值。测试零值用例：0 次 HTTP 请求 + 订阅保留。本轮无变化。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3；B4 方案 v7）

### 4.1 已交付（`124b73d`）

`DeviceRevocation`/`revokeCleanupWarning`/`.alert`/双语 L10n/`DeviceStoreTests`
2 条新用例 + `WorkspaceViewTests` stub（Swift 定向 21 条全绿）。

### 4.2 评审发现的问题（Round 1–6 累计，全部成立；1–6 已于 v2–v6 处置）

7. **R6-B3.1**：现网络层 `performRequest` 非 2xx 抛 `ManagementError.httpError(Int)`
   前丢弃 body——「尽力解码 error code」不可实现。
8. **R6-B3.2**：protocolUnknown 五类未覆盖全部 JSON shape（零字节/纯空白、顶层
   null/array/标量、`revoked:null`、`revoked:true` + 坏 `pushCleanupError`、空串/
   空白串、非 2xx `error` 非 string）；`{}` 与 missingRevokedKey 边界重叠。
9. **R6-B3.3**：`.networkError` 无可呈现诊断；方法 `throws` 与否未写死；
   cancellation 路径未定义。
10. **R6-B3.4**：`@MainActor` ≠ async 事务原子——`await` 可重入，旧 reload 可
    覆盖新状态；无 operation generation/stale guard。

### 4.3 过审后实施方案（本轮不动代码；过审后一次实施 + 定向测试 + 重建部署）

#### 4.3.1 唯一证据优先级（R4-B3 定案；Round 4–6 已核验）

**服务端事实**：`handleRevokeDevice` 只有在 `DeviceStore.RevokeDevice` **成功
持久化**后才返回 `revoked:true`（失败 404，体含 `{"error":"not_found",...}`）；
`pushCleanupError` 是成功后的附加字段；`ListDevices` 从同一 store 排除 revoked
记录。

**规则（唯一，机械适用全部 15 行）**：

1. **撤销生效判定**：confirmed response（`revoked:true`）与 reload `absent`
   各自单独确认「已撤销」（OR）。
2. **confirmed × present**：不降级撤销结论，列表只触发一致性告警。
3. **投递阻断声明的证据 = confirmed 撤销 + confirmed cleanup failure**，与
   reload 结果无关。
4. **`revoked:true` + 坏 cleanup 字段**（R6-B3.2 择一）：**保留撤销证据，新增
   `confirmedCleanupUnknown`**——服务端只有成功持久化才返回 true，cleanup 字段
   不可信只影响 cleanup 信息，不降级整包（符合 confirmed-first）。
5. **无 confirmed response** 时 reload 是唯一当前证据：`present` → 未生效/倾向
   未生效；`absent` → 已撤销（对账）；`failed` → 未知。
6. **reload `failed` 永远不从旧 `devices` 数组推断任何结论**。

#### 4.3.2 API 边界契约（R6-B3.1/2/3）

**非 2xx body 获取（R6-B3.1 择一：专用 raw request）**：`ManagementAPIClient`
为 revoke 增加**私有 raw 请求路径**，返回 `(status: Int, data: Data)`——**不
修改** `performRequest` 与其他调用方（减少影响面）。响应体大小上限 **64KiB**；
原 body 不写日志；只有类型校验通过的 `error` code 字符串进入 outcome。测试
必须经真实 HTTP client 层（StubHTTPServer），**不得在 DeviceStore stub 伪造
code 绕过丢 body 现状**。

**五类 response + 完整分类表（R6-B3.2：无重叠、全覆盖、固定优先级，按序判定
先命中先归属）**：

2xx body：

| 序 | 形状 | 归属 |
| --- | --- | --- |
| 1 | 体零字节或纯空白 | `protocolUnknown(.emptyBody)` |
| 2 | JSON 语法非法 | `protocolUnknown(.malformedJSON)` |
| 3 | 合法 JSON 但顶层非 object（null/array/string/number/bool） | `protocolUnknown(.revokedTypeMismatch)` |
| 4 | 顶层 object 无 `revoked` 键 | `protocolUnknown(.missingRevokedKey)` |
| 5 | `revoked` 值非布尔（含 null/其他类型） | `protocolUnknown(.revokedTypeMismatch)` |
| 6 | `revoked == false` | `protocolUnknown(.revokedFalse)` |
| 7 | `revoked == true` 且无 `pushCleanupError` 键 | `confirmedClean` |
| 8 | `revoked == true` 且 `pushCleanupError` 为非空非空白 string | `confirmedCleanupFailure(err)` |
| 9 | `revoked == true` 且 `pushCleanupError` 类型错误（非 string）**或空串/纯空白** | `confirmedCleanupUnknown`（§4.3.1 规则 4） |

（v6 的 `{}`→emptyBody 与 missingRevokedKey 重叠由序 1/4 的固定顺序消除。）

非 2xx：`transportOrHTTPFailure(.httpStatus(status, serverErrorCode))`——
serverErrorCode = 尽力解码体 `{"error": ...}`（**非 string 或缺失 → nil**，不改
变分类）；404 特例 = `status==404 && code=="not_found"`，不把所有 404 等同。

网络/取消：`transportOrHTTPFailure(.networkError(category))`——category 为
**稳定、可本地化、非敏感**的类别（`offline` / `timedOut` /
`cannotConnectToHost` / `cancelled` / `other`，映射自 `URLError.Code` 等；**不把
服务器正文或本地路径进 UI**——R6-B3.3）。

**签名写死（R6-B3.3）**：

```swift
func revokeDevice(_ deviceId: String) async -> RevokeAttemptOutcome  // non-throwing
```

所有失败（**含 cancellation**）映射进 outcome，无不可映射错误、无仍可 throw 的
撤销路径错误；**任何 outcome（含 cancelled）都执行恰好一次 typed reload**——
reload 自身被取消 → reload class = `failed`，走对应行。client fixture 验证
network error / cancelled / HTTP failure / protocol failure 各进入**一次且仅一次**
reload。

**typed reload outcome（归属状态管理层）**：`DeviceStore` 内部包装现有
`listDevices() async throws -> [TrustedDevice]` 为 typed outcome；不给 API 协议
新增重复 RPC；reducer 只消费返回值，不读陈旧 `devices` 数组。

#### 4.3.3 完整决策矩阵（5 × 3 = 15 行，由 §4.3.1 机械生成；测试逐行覆盖）

| # | response class | reload | 生效判定 | cleanup | 呈现 |
| --- | --- | --- | --- | --- | --- |
| 1 | confirmedClean | absent | 已撤销（双确认） | clean | 无警告 |
| 2 | confirmedClean | present | 已撤销 + 列表不一致 | clean | inconsistent 警告 |
| 3 | confirmedClean | failed | 已撤销（response 确认） | clean | 无撤销警告；列表刷新失败由 reload 路径呈现 |
| 4 | confirmedCleanupFailure | absent | 已撤销（双确认） | failed(err) | cleanup-failure 警告（含投递阻断声明） |
| 5 | confirmedCleanupFailure | present | 已撤销 + 列表不一致 | failed(err) | inconsistent 警告 + 清理失败附句（含投递阻断声明） |
| 6 | confirmedCleanupFailure | failed | 已撤销（response 确认） | failed(err) | cleanup-failure 警告；列表刷新失败另行呈现 |
| 7 | confirmedCleanupUnknown | absent | 已撤销（双确认） | unknown | **pending 警告**（撤销已确认，cleanup 未知） |
| 8 | confirmedCleanupUnknown | present | 已撤销 + 列表不一致 | unknown | inconsistent 警告 + **cleanup-unknown 附句** |
| 9 | confirmedCleanupUnknown | failed | 已撤销（response 确认） | unknown | **pending 警告**；列表刷新失败另行呈现 |
| 10 | protocolUnknown | absent | 已撤销（reload 对账） | unknown | **pending 警告** |
| 11 | protocolUnknown | present | 倾向未生效 | unknown | **unknown 警告**（撤销结果未知；设备仍在列表） |
| 12 | protocolUnknown | failed | 未知 | unknown | **unknown 警告**（撤销与 cleanup 均未知） |
| 13 | transportOrHTTPFailure | absent | **已撤销（lost response 对账）** | unknown | **pending 警告** |
| 14 | transportOrHTTPFailure | present | 未生效 | unknown | `devicesError`（现有失败语义；网络诊断用 §4.3.2 category） |
| 15 | transportOrHTTPFailure | failed | 未知 | unknown | `devicesError` + 撤销状态未知；下次成功刷新自然对账 |

**特例**：`404 not_found`（`.httpStatus(404, "not_found")`）× absent → 服务端
无此设备（含此前已撤销），无 cleanup 对象 → 无警告，按「设备已不在授权列表」
呈现；其他 404 → 行 14/15 语义。

**最低规则（由 §4.3.1 机械导出）**：

1. 撤销尝试后**总是恰好一次** typed reload（含 cancelled 路径）；reload
   `absent` 可单独确认撤销并对账 lost response（行 10/13）。
2. confirmed response 可单独确认撤销（行 2/3/5/6/7/8/9）；与 reload 冲突时
   不降级撤销结论，只追加一致性告警；投递阻断声明只依赖 confirmed 证据（行
   4/5/6 同结论）。
3. 无 confirmed response 时 reload 是唯一当前证据：`present` → 未生效/倾向未
   生效（行 11/14），不声称投递阻断。
4. reload `failed` 不从旧 `devices` 数组推断任何结论（行 3/6/9/12/15）。

#### 4.3.4 文案（六键；L10n 组合而非嵌套格式化）

- `devices_push_cleanup_warning`（行 4/6 主文案）：
  - en: "The device was revoked, but cleaning up its push subscription failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
  - zh: "设备已撤销，但其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
- `devices_push_cleanup_pending`（行 7/9/10/13：撤销已确认、仅 cleanup 未知）：
  - en: "The device no longer appears in the authorized list, so the revocation is confirmed. The cleanup result of its push subscription is unknown; if it is still pending, the runtime retries it automatically on later notifications. Check the runtime log if needed."
  - zh: "该设备已从已授权列表消失，撤销已确认。其推送订阅的清理结果未知；若仍待清理，runtime 会在后续通知投递时自动重试；必要时可查看 runtime 日志。"
- `devices_push_cleanup_unknown`（行 11/12：撤销与 cleanup 均未知）：
  - en: "The revocation outcome could not be confirmed, and the cleanup result of this device's push subscription is also unknown. Refresh the device list: if the device still appears, the revoke may not have taken effect — check the runtime log; if it has disappeared, the runtime retries any pending cleanup automatically on later notifications."
  - zh: "撤销结果无法确认，该设备推送订阅的清理结果也未知。请刷新设备列表：若设备仍显示在列表中，撤销可能未生效，请查看 runtime 日志；若已消失，仍待清理的订阅由 runtime 在后续通知投递时自动重试。"
- `devices_push_cleanup_inconsistent`（行 2/5/8 基础文案）：
  - en: "The runtime confirmed the revocation, but this device still appears in the authorized list. Refresh the list; if it persists, check the runtime log."
  - zh: "runtime 已确认撤销，但该设备仍出现在已授权列表中。请刷新列表；若持续显示，请查看 runtime 日志。"
- `devices_push_cleanup_inconsistent_detail`（行 5 追加句）：
  - en: " Its push subscription cleanup also failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications."
  - zh: "其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理。"
- `devices_push_cleanup_unknown_detail`（行 8 追加句）：
  - en: " The cleanup result of its push subscription is unknown; if it is still pending, the runtime retries it automatically on later notifications."
  - zh: "其推送订阅的清理结果未知；若仍待清理，runtime 会在后续通知投递时自动重试。"

#### 4.3.5 实施与测试清单

1. `RevokeAttemptOutcome` 契约落地（§4.3.2）：raw request + 五类 response +
   分类表 + 网络 category；`revokeDevice` 改 non-throwing。
2. `DeviceStore` typed reload outcome 包装 + §4.3.6 generation guard；任何
   outcome 后恰好一次 reload。
3. Reducer 按 §4.3.1 规则机械实现（15 行矩阵 + 404 特例）。
4. **wire fixtures（client 级，经真实 HTTP 层）**：分类表 9 行 2xx 形状逐行
   （零字节/纯空白/语法错/顶层 null/array/标量/缺键/`revoked:null`/
   `revoked:"yes"`/`revoked:false`/`revoked:true`±cleanupError/
   cleanupError 类型错/空串/空白串）+ 非 2xx（404 带/不带 `not_found`、500）+
   连接丢失（lost response）+ 网络错误/cancelled（各恰好一次 reload）。
5. **ViewModel 测试**：15 行矩阵逐行 + 404 特例 + 行 5/8 组合文案断言 +
   pending/unknown 两类文案**不可互换**断言（行 7/9/10/13 必含「撤销已确认」
   且不含「无法确认撤销」；行 11/12 相反）+ presentation seam（四种警告态
   发布 true / dismiss false）+ typed outcome 包装测试。
6. **async 交错测试（controllable continuations，§4.3.6）**：旧 reload 后
   返回、普通 refresh 与 revoke 交错、连续两次操作、dismiss 与晚到结果。
7. 定向测试 → Release 重建 + 覆盖安装 + 新代际核验。

#### 4.3.6 operation generation / stale-result guard（R6-B3.4）

- `DeviceStore` 建立单调递增 **operation generation**：每次撤销操作取当前
  gen；所有 `await` 结果（outcome、reload）**仅在 gen 仍当前时应用**，否则
  丢弃（`@MainActor` ≠ async 事务原子）。
- 最终 reducer 在 **@MainActor 一次同步块**提交 `devices` /
  `hasLoadedDevices` / `devicesError` / warning。
- **所有写 `devices` 的异步路径共享同一 generation guard**（页面 on-appear
  刷新、错误区 Retry 与撤销 reload 交错防护）。
- **旧 warning 清理规则**：开始新撤销操作时清除上一轮 warning（不跨操作
  保留）；dismiss 只清 warning、不影响 gen；晚到的旧 gen 结果被丢弃，**不会
  复活已 dismiss 的 warning**。

## 5. 交付与部署证据（`124b73d` 历史记录；Round 1–6 已独立复核，本轮无新部署）

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

Round 2–6 评审实测：当前 runtime PID 34681（同版本 `124b73d7b0f2`）监听 8777；
该代际变化不用于证明 §2 首采数据。

### 5.3 验证状态

代码自 `124b73d7b0f2155ed19207c3ec6fe00cdf5fa888` 未变化；Go/Swift 定向测试、
race、vet 的独立复跑记录见 Round 1–6 评审报告。本轮（v7）零代码改动，无新
构建/部署。

## 6. 不采纳清单与择一决策表

**Round 6 择一决策表（各选择点均采纳评审给出的选项之一，无否决）**：

| 选择点 | 选项 | v7 落地 |
| --- | --- | --- |
| R6-B1.4 解析失败 | 整笔 rejected vs 指标级 unavailable | **指标级 unavailable**（命令执行失败仍整笔 rejected） |
| R6-B2.1 t=0 原点 | startedAt vs detection time | **startedAt**（评审推荐） |
| R6-B2.3 拉起方式 | 前台常驻 vs launchd | **launchd user agent**（7 天无人值守必需） |
| R6-B3.1 非 2xx body | 专用 raw request vs 改底层 typed error | **专用 raw request**（评审推荐，影响面小） |
| R6-B3.2 revoked:true + 坏 cleanup | 新状态 vs 整包降级 | **新状态 `confirmedCleanupUnknown`**（符合 confirmed-first） |
| 300MB 单位 | 10^6 vs 2^20 | **300MiB = 314,572,800 B**（与其他门统一二进制） |

**现存不采纳项（Round 1 的 3 项 + Round 4 的 1 项）**：

### 6.1 B2「补同一 PID 原始时间序列」——无法采纳，以撤回替代

PID 12110 已消亡，时间序列无法回溯采集（§2.2）。

### 6.2 B3 选项一「受控长窗口（关闭自动重启）」——不采纳为默认路径

理由：(1) 需要在 owner 日常使用的生产 Mac 上禁用 runtime 自动重启 27h+，风险与
信息价值不成比例；(2) 复采首要问题用多代际监测即可回答。**升级条件**：§2.3.5
任一 provisional 告警触发时再评估，且必须 owner 明确授权。

### 6.3 B4「提供可达的『重试撤销』入口」——不采纳，采纳文案修正路径

撤销是一次性授权动作，撤销后设备从 `ListDevices` 消失；物理清理由服务端
deny-by-default + 后续 fan-out 自动重试自愈；用户侧重试入口不增加安全性，只增
UI 面积。

### 6.4 R4-B1 备选「PID-only bootstrap（不比较 epoch）」——不采纳

理由：降低 epoch 防混代强度。采纳主方案：与 `managementBridgeEpoch` 逐字节
相同的转换（§2.3.1 第 2 步 + 转换契约测试）。

## 7. 遗留与下一步

1. **本文档过审后（开发阶段，一次完成）**：实施 §4.3（raw request + 五类
   response + 15 行 reducer + 六键文案 + generation guard + 全部 fixtures）与
   §2.3 监测脚本（安全契约 + 解析契约 + launchd 调度 + 三层负载 + 测试矩阵）→
   定向测试 → Release 重建 + 覆盖安装 + 新代际核验。
2. **监测执行**：按 §2.3.6 完成条件运行，最终报告回填 §2.4。
3. **owner 验收**：正常撤销自然路径（设备消失、无警告）；异常态由
   deterministic 测试验收。

## 8. Round 6 终审准入清单勾销表

| # | 准入条件（评审报告 §7） | v7 落地位置 | 测试位置 |
| --- | --- | --- | --- |
| 1 | loopback 校验、禁 redirect、token 不进 argv/env/temp/output、bootstrap 无效分类、权限检查 | §2.3.1 URL 严格校验 + HTTP 客户端 + 归档权限 | §2.3.1 安全/超时 fixtures |
| 2 | HTTP/命令/整事务硬超时、子进程回收、真实 vmmap/ps fixture、300MB 精确常量、unavailable/failure 规则 | §2.3.1 硬超时 + §2.3.2 解析契约（314,572,800 B） | §2.3.1 超时 fixtures + §2.3.2 真实 fixture 分层 |
| 3 | 统一 t=0 原点、discovery cadence/slot tolerance/missed slot、实际 restart policy、趋势/peak 可评估分母 | §2.3.3（startedAt 原点、60s 发现、slot 窗口、restart policy 只读探测）+ §2.4 分母 | §2.3.3 调度/恢复测试（晚发现、slot 边界） |
| 4 | 拉起方式、单实例、持久状态、sleep/crash/restart 恢复、幂等、告警后停止、「自然日」精确定义 | §2.3.3 launchd 模型 + 168h/7 日历日定义 + 停止标志 | §2.3.3 fake clock/state store 测试 |
| 5 | scan-evidenced / cpu-active-only / idle 分层、60s provisional、「真实负载」措辞、≥8 计数口径 | §2.3.4 三层 + §2.3.6 完成条件（≥8 scan-evidenced） | §2.3.4（随脚本实施） |
| 6 | 非 2xx body 丢失解决（raw request 或 typed error）、真实 client fixture 证明 404 error code 可达 | §4.3.2 专用 raw request + 64KiB 上限 | §4.3.5 wire fixtures（经真实 HTTP 层） |
| 7 | 2xx/非 2xx JSON shape 无重叠全覆盖分类、坏 cleanup 字段裁决、全部 wire fixtures | §4.3.2 分类表（9 行 2xx + 非 2xx + 网络）+ §4.3.1 规则 4 | §4.3.5 fixtures 4 |
| 8 | non-throwing 签名写死、可本地化网络诊断、所有 failure/cancel 一次 reload | §4.3.2 签名 + networkError(category) + 恰好一次 reload | §4.3.5 fixtures 4（cancelled/网络错误） |
| 9 | operation generation/stale guard、一次性状态提交、旧 warning 清理规则、async 交错测试 | §4.3.6 | §4.3.5 测试 6（controllable continuations） |
