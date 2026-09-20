# 2026-09-20 内存治理 r6 通过后的三项后续（评审稿 v9，待 Round 9 终审）

> 状态：**待 Round 9 终审（checklist verification）**。v8（`b1c5ff70269ac3408fb2ad942df075c63659ffe6`）
> 经 Round 8 终审（报告 `docs/2026-09-20-memory-followups-review-report-r8.md`，
> commit `b20b651121a57bd756a7e23b6fe2593a418fff85`）判定：Round 7 的 5 阻断 +
> 3 非阻断均已正面处置；剩 4 个 v8 机制推演出的闭合缺口（R8-B1 双文件崩溃
> 一致性 / R8-B2 异常终止无法自记 crash 次数 / R8-B3 oversize 漏第 13 行 /
> R8-B4 并发 revoke 未定义）+ 2 个同期修正（N1 缺键默认值 / N2 不可信数据根）。
> **v9 为 docs-only 修订：本轮零代码改动**，按 §8 的 Round 9 五项 checklist
> 逐项关闭；Round 1–7 已通过裁决冻结不重开。
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

### 1.2 本任务（v9 轮）三门点清单（全部完整哈希）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
门点1（读取源码/文档分析前）提交=b20b651121a57bd756a7e23b6fe2593a418fff85（Round 8 终审报告提交；工作树干净，git status --porcelain 无输出）
门点2（第一次修改文件前）提交=b20b651121a57bd756a7e23b6fe2593a418fff85（干净；本轮只改 think.md 与本文档，无业务代码修改）
门点3（构建/部署前）=不适用——本轮 docs-only（D0），无构建、无安装、无部署
本轮只读核验=defaults read org.openagi.cordcode.link autoRestartIntervalMinutes → "Could not find key"（缺键形状本机复现，与 Round 8 评审 N1 一致）；前轮源码核读部分未变化
v8 历史来源事实=本文档 v8 提交 b1c5ff70269ac3408fb2ad942df075c63659ffe6；同轮 think.md 提交 5626248941ce88fa3ea85720a7fca96fa21b4671
think.md 同轮提交（v9 轮）=f845a3a8a77c1f8a4cae999be38f3d36817b243b（只改 think.md）
本文档（v9）提交=本提交（只改 docs/2026-09-20-memory-followups.md；最终哈希在送审说明中给出，不在正文构造自引用）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 389a179a1b01ad2a858f24b5b11ea41058e02381（门点1 实测，干净；与 Round 4–8 评审门点一致）
预期产品特性=本轮无产品代码变化；当前部署仍为 124b73d7b0f2（Round 8 评审实测）
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

### 2.3 复采设计（R2–R8 修订总集）

**前提**：`autoRestartEnabled` / `autoRestartIntervalMinutes` 是可变 UserDefaults
（app domain `org.openagi.cordcode.link`，`project.pbxproj:524`；`RuntimeManager.swift:622-624`
周期重读；**缺键时源码默认 true/120**——见 §2.3.3 N1 规则）。多代际监测不回答
27h 同进程累积问题（受控长窗口不采纳为默认，§6.2）。

**端点与 bootstrap 形状（源码核读，与 Round 2–8 评审实测一致）**：

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

#### 2.3.1 采集事务与安全契约（六步 + R6-B1 + R7-N3 + R8-N2）

**URL 严格校验（发任何请求之前）**：解析 `managementUrl`，要求全部满足，否则
rejected（`bootstrap_invalid`，不崩溃）：

- scheme 只能是 `http`；
- host 只能是数值 loopback 白名单 **{`127.0.0.1`, `::1`}**（显式列出；生产值
  `127.0.0.1`）；
- 禁止 userinfo；path 为空或 `/`；无 query、无 fragment；port ∈ 1...65535。

`runtime.json` 缺失/不可读 → `bootstrap_unavailable`；JSON 无效/字段类型错/URL
缺失 → `bootstrap_invalid`——均与 `command_failed` 分开，脚本不得崩溃。

**HTTP 客户端**：

- **进程内 HTTP 库**（禁止 `curl`/`wget` 等子进程 HTTP——token 会进 argv）；
  token 只存在于进程内存变量，不进 argv、环境变量、临时文件、错误文本。
- **禁止自动 redirect**：任何 3xx → rejected（`redirect_rejected`），绝不把
  Authorization 转发到 Location。
- 错误归档只保留 status/reason code，不保留完整 request/headers。

**归档权限与文件打开规则（R7-N3 + R8-N2）**：归档目录 0700、文件 0600；**启动
时验证权限**。处置规则写死：

- owner 仍是当前 uid 且**仅 mode 过宽** → `chmod` 收紧后继续；
- **open lock/state/temp/log 一律 `O_NOFOLLOW` + open 后 `fstat` 复核**（防
  lstat→open 的 TOCTOU 竞态）；
- owner 错 / 类型错（非普通文件）/ symlink / 数据根不可写 / 其他不可修复异常
  → **启动前安全失败**：**不向该数据根写任何文件（包括 fatal.json——不虚假
  承诺 marker 可写）**，以 **exit 0 + stderr 最小诊断**结束（launchd 的
  StandardErrorPath 在脚本校验前已打开；若该路径本身不可用，诊断丢失是可接受
  的诚实边界，报告为「启动前安全失败，无持久诊断」）；
- 临时文件**从创建瞬间即 0600**（以 0600 mode 创建，不得先按默认 umask 创建
  再事后 chmod）。

**硬超时（预固定；变更须修订本文档）**：单 HTTP 请求 **10s**；单本地命令
（vmmap/ps/sysctl）**15s**；**整笔事务 90s**（含 identity B 与 bootstrap 重读
——不得用无界等待换原子性）。超时 → 终止**本事务创建的**子进程并记录
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
   bootstrap 重读未变 → **有效样本**（按 §2.3.3 journal 协议提交）；否则
   rejected（原因码：`bootstrap_unavailable` / `bootstrap_invalid` /
   `management_token_unavailable` / `bootstrap_mismatch` / `redirect_rejected`
   / `auth_rejected` / `command_failed` / `command_timeout` / `payload_invalid`
   / `identity_changed` / `started_at_mismatch` / `bootstrap_rewritten`），
   **绝不拼部分字段**。

**epoch 转换契约测试**：活体 fixture（UUID `68fef32a-ef11-4c08-9392-bc1b7e32712e`
→ `2011574066258607221`）；固定 fixture ≥1；零值保护分支（hash 桩全零前 8 字节
→ 1）。

**安全/超时 fixtures**：外部 host、非 http、userinfo、redirect（3xx）、非法/
缺失 port、残缺 runtime.json、每类超时、子进程退出回收、下一计划槽仍可执行、
成功/失败输出扫描假 token 不出现在 stdout/stderr/JSONL/异常文本。

#### 2.3.2 外部输出解析契约（R6-B1.4 + R7-N1 counter schema）

**精确 byte 常量（与其他门统一为二进制）**：告警②阈值 = **300MiB =
314,572,800 B，严格大于（>）**（同族：256MiB = 268,435,456 B；8MiB =
8,388,608 B）。

**vmmap parser**：从**真实归档的 `vmmap --summary` 输出（脱敏 fixture）**解析
current `Physical footprint:`、`Physical footprint (peak)`、`TOTAL SWAPPED`
（报告用）。单位表：K=1024、M=1,048,576、G=1,073,741,824（vmmap 二进制惯例；
以真实 fixture 钉死——若实际输出与该表不符，以 fixture 为准并回填本文档）；
小数与空格变化均有测试。

**解析失败策略**：**指标级 unavailable**——peak 缺失、重复行、未知单位、非数字
→ 该指标标 `unavailable`（**不按 0**），样本其余指标仍有效，告警②覆盖度下降并
在最终报告计入 unavailable 数。**命令执行失败**（非零退出/超时/PID 不存在）→
整笔 rejected（`command_failed`）。

**`ps -o rss=`**：无表头形状；KB→B ×1024；PID 不存在 → 命令失败 → 整笔
`command_failed`。

**HTTP JSON 必需字段/类型校验**：`startedAt`（string RFC3339）、`memory.{sys,
heapSys,heapInuse,heapIdle,heapReleased,sysMinusHeapReleased,heapObjects,stackSys,
numGC}`（数值）、`processUserCPUSeconds`/`processSystemCPUSeconds`（数值）、
`processCPUAvailable`（bool）——缺失或类型错 → **不得以零值制造有效样本** →
rejected（`payload_invalid`）。`processCPUAvailable == false` → CPU 指标标
unavailable（负载门回落到 scan 计数器）。

**`agentBackgroundScans:<backendID>` wire schema（R7-N1）**：

- 对象必须为 map；`scans` / `turnItemRequests` / `scannedTurns` 为**非负整数**；
- **缺整个对象** = 该 backend 无此证据（不是错误，不标 unavailable）；
- 字段缺失/类型错/**计数回退**（末值 < 首值）→ 该 backend 的 counter 标
  `unavailable`——不按 0、不产生负 delta、**不导致整笔样本失败**；
- 代际 `scan-evidenced` 判定：至少一个 backend 的**同名 counter** 首末可比较
  且 delta > 0（全部 unavailable 的 backend 不贡献证据）；
- fixtures：缺字段、类型错、计数回退、多 backend 混合。

**fixture 分层**：**parser fixture 与纯算法 fixture 分开**——vmmap/ps 用真实
归档输出脱敏 fixture；epoch 转换/阈值边界/counter schema 用合成 fixture。

#### 2.3.3 调度与恢复模型（R6-B2 + R7-B1/B2 + R8-B1/B2 + N1/N2）

**代际原点**：slot k = startedAt + 30k 分钟（k=0..3）；**slot 窗口 =
[t_k, t_k+15min)**（含左不含右），样本时间戳落入即归属该 slot；窗口外记
`out_of_slot`（不用于趋势）；每 slot 至多一个有效样本（后到重复记
`duplicate`，保留首个）。检测晚于 slot 的历史 slot 记 `missed`，不补采。

**代际发现节奏**：每 60s 检查 bootstrap（runtime.json 内容/stat）与 runtime
存活性；变化 → 新代际发现流程（立即执行首个可用 slot 事务）。

**时钟跳变**：样本时间戳 < 上一样本 → 该对不参与趋势，记 `clock_anomaly`。
完成门的 elapsed 定义：维护持久化 `last_observed_wallclock`，每次观测差 d：
`d < 0`（回拨）→ 顺延窗口不计负；`0 ≤ d ≤ 24h` → 计入 elapsed；`d > 24h` →
记 `clock_anomaly_forward`，该段不计入 elapsed。最终报告保留 wall-clock 起止
与全部 anomaly 记录；7 个本地日历日仍为独立门。

**持久化 journal 协议（R8-B1 重写：JSONL 为唯一 durable truth）**：

- **单一真相**：`samples.jsonl` 是 write-ahead / source-of-truth；每条记录含
  **稳定唯一键 `(pid, epoch, slot, recordKind)`**（recordKind ∈ {sample,
  rejected, missed, duplicate, …}）。
- **写入顺序（持锁下）**：append 完整 JSONL 行 → **flush + fsync** → 再写
  `state.json` 快照（tmp+rename）。state 只是**可丢弃的加速快照**，绝不能覆盖
  日志中更晚的已提交事实。
- **启动恢复**：先截断 partial tail（记 `corrupt_line`）→ **重放 JSONL** →
  以唯一键重建/校正 state（slot 进度、代际、完成计数、告警停止标志）→ 重复
  完整行**确定性去重**（保留首条、计数 `duplicate`）。
- **不丢不重证明边界**：先 append 后 state 崩溃 → 重放恢复该 slot（不重复
  采样）；先 state 后 append 崩溃 → 不可能（顺序固定 append 在前）；append
  未落盘而 state 已替换 → fsync 在 state 前完成，排除该形状。
- **crash-recovery 测试（注入 ≥4 个 crash point）**：①append 前；②完整
  append 后 / state 前；③state temp 写后 / rename 前；④state rename 后——
  每点重启恢复后断言**同一 slot 恰好一个有效事实、完成计数一致、告警 stop 不
  丢失**。真实双进程锁测试保留，但不替代这组测试。

**单实例与锁**：独立稳定 `monitor.lock`（启动 open 后 flock 到进程退出，生命
周期内绝不 rename/unlink；PID 只是锁文件内诊断字段）。**真实双进程测试**：
持锁进程反复 rename 替换 state 文件时第二进程始终无法取得锁；kill -9 后才能
取得。

**launchd 运行单元（R7-B2 + R8-B2）**：

- **实现载体**：仓内单文件 Python 3 脚本（`scripts/memory-monitor/monitor.py`），
  只用系统标准库，由绝对路径 **`/usr/bin/python3`** 执行。
- **绝对路径契约**：

```text
脚本（仓内）=<repo>/scripts/memory-monitor/monitor.py
脚本（安装副本）=$HOME/Library/Application Support/CordCode Link/memory-monitor/monitor.py
数据根目录=$HOME/Library/Application Support/CordCode Link/memory-monitor/
  monitor.lock（稳定锁 inode，绝不 rename/unlink）
  samples.jsonl（append-only durable truth，唯一键 (pid, epoch, slot, recordKind)）
  state.json（tmp+rename 加速快照，可由 JSONL 重放重建）
  run_in_progress（dirty-run marker，见下）
  fatal.json（永久 fatal marker，含 reason；仅数据根可信时可写，见 §2.3.1 N2）
  stop.json（告警停止标志）
plist=$HOME/Library/LaunchAgents/org.openagi.cordcode.link.memory-monitor.plist
Label=org.openagi.cordcode.link.memory-monitor
ProgramArguments=[/usr/bin/python3, <安装副本绝对路径>]
WorkingDirectory=<数据根目录>
StandardOutPath/StandardErrorPath=<数据根>/monitor.log
KeepAlive={SuccessfulExit:false}（非零退出才重启）
ThrottleInterval=30（重启节流）
app domain（restart policy 只读探测）=org.openagi.cordcode.link（project.pbxproj:524 实证）
```

- **安装/卸载**：安装 = 复制脚本 + 生成 plist + `launchctl bootstrap gui/$(id -u)
  <plist>`；卸载/停止 = `launchctl bootout gui/$(id -u) <plist>` + 删除数据根
  （owner 决定）；升级 = 替换安装副本后 `launchctl kickstart -k gui/$(id -u)/<Label>`。
- **退出语义（三类）+ 跨实例 crash 计数（R8-B2 dirty-run 协议）**：
  1. **transient crash**（非零退出，含 SIGKILL/解释器崩溃/掉电——死亡进程无法
     写遗言）：launchd 按 KeepAlive 重启（ThrottleInterval=30 节流）。连续
     transient 计数由 **dirty-run 协议**实现：每次启动**进入主循环前**原子写
     `run_in_progress`（run generation）；只有达到 **healthy milestone**——
     **存活一个完整 discovery cycle（≥60s）且完成至少一次成功采样事务或一次
     完整 idle 检查**（不是「启动成功一瞬间」）——才清除 marker 并重置连续
     失败计数。重启发现**未清除的 dirty marker** → 前一 run 计一次 transient；
     连续 ≥5 次 → 升级 fatal（写 fatal.json + 清 dirty marker + exit 0）。
     每 31s 崩一次的循环永远达不到 milestone → 计数持续累计 → 5 次后 fatal。
  2. **fatal stop**（永久配置/权限/依赖错误）：**先写 `fatal.json`（含
     reason）→ 清 dirty marker → exit 0**（launchd 不重启，也不被下次启动误算
     为 crash）；恢复 = owner 修复后删除 marker 并 `launchctl kickstart`。数据
     根不可信时按 §2.3.1 N2 启动前安全失败（不承诺 marker 写成）。
  3. **clean stop**（provisional 告警触发）：**先写 `stop.json` → 清 dirty
     marker → exit 0**；重启后读到标志立即退出，不悄悄继续采集；恢复 = owner
     评估升级后清除标志重启。
- **测试**：最小 plist 生成校验；缺依赖/坏权限 → 启动前安全失败（exit 0 +
  stderr 最小诊断，不形成重启风暴）；**kill -9 后重启累计 transient、连续五次
  升级 fatal、达到 healthy milestone 后重置、fatal/clean exit 不计入 transient**；
  crash point ×4 的 restart-recovery（见上）；告警 stop 与 fatal stop 的恢复
  方式。

**实际 restart policy 记录（R8-N1：缺键=有效配置）**：监测开始与每次检测到
变化时只读探测，记录 **`{rawPresence, effectiveValue, source}`**：

- 键存在 → 严格解析类型与值（`source=user_set`）；
- **键缺失 → 使用与源码一致的默认值**（`autoRestartEnabled=true` /
  `autoRestartIntervalMinutes=120`，`RuntimeManager.swift:622-624` 同源）并标
  `source=code_default`——**缺键不是采集错误**（本机实测 `defaults read` 对缺键
  返回错误而非默认值，脚本不得把「命令成功」当唯一有效形状）；
- domain 不存在 → 等价于两个键均缺 → `code_default`；命令失败/类型错误 → 该时点
  标 `unavailable`，**同时记录运行时代码实际采用的 fallback effective value
  （`source=invalid_type_fallback`，Round 9 终审注记 2）**（**不得修改偏好**）；
- fixtures：present / missing / wrong-type 三形状。

**调度/恢复测试（fake clock/state store）**：重启恢复、sleep 跳槽、重复唤醒、
部分写入（corrupt_line 截断）、告警后恢复、晚发现、slot 边界。

#### 2.3.4 代际分层与负载覆盖门

代际分**三层**（同代际首末有效样本之间）：

| 层 | 判定 | 语义 |
| --- | --- | --- |
| `scan-evidenced` | 任一 backend 同名 counter（§2.3.2 N1 schema）首末可比较且 delta > 0 | **唯一算「真实工作负载证据」的层** |
| `cpu-active-only` | 仅 `processUserCPUSeconds + processSystemCPUSeconds` delta ≥ 60s，无扫描证据 | 只证明 runtime 活跃，**不得称为真实负载** |
| `idle/low-activity` | 两者皆无 | 空闲观测 |

- 60s 与 256MiB/300MiB/8MiB 同级 **provisional**（无分布数据支撑）。
- 完成条件「≥8 活跃代际」口径：**≥8 个 `scan-evidenced`**；
  `cpu-active-only` 单列分母，不并入。
- `sysctl vm.loadavg` 仅系统背景，不作 CordCode 负载证据。

#### 2.3.5 provisional 告警线（三类**独立 OR** 触发，只升级不定论）

| 触发器 | 条件（精确公式） | 覆盖形状 |
| --- | --- | --- |
| ① 绝对越线 | 任一有效样本 `sysMinusHeapReleased` **current** > 268,435,456 B（256MiB） | 持续高位 |
| ② footprint 波峰 | 任一有效样本 vmmap footprint **current 或进程 lifetime peak**（`Physical footprint (peak)`）> 314,572,800 B（300MiB） | 两采样点之间单次大分配波后回落/高位持平——原事故形状 |
| ③ 趋势异常 | **4 个连续有效样本（完整 slot 序列）构成 3 个相邻 delta，每个 delta ≥ 8,388,608 B（8MiB）**；总增量 ≥ 24MiB/90min 由每步下限蕴含 | 稳定爬升的早期形态 |

- 三者互不替代、独立 OR；③不要求与①同时成立。
- 8MiB/256MiB/300MiB 均为 **provisional 运维启发式**；预固定，变更须修订本文档。
- **语义**：触发 → 仅「写 stop.json + 清 dirty marker + clean stop + 评估升级
  取证（§6.2，需 owner 授权）」；**未触发不能推出任何结论**。「未触发趋势」
  必须同时报告可评估分母（§2.4）。
- **能力边界**：无 GC pause/alloc counter，不能裁决 GOGC 或 512MiB 最优值。
- **③边界 fixtures**：delta 恰等于 8,388,608 B（触发）/ 差 1 byte（不触发）/
  非严格上升（不触发）/ 步数不足（不触发）。

#### 2.3.6 完成条件（预固定；变更须修订本文档）

监测**完成**当且仅当：①elapsed 满足 §2.3.3 定义（按 ≤24h 观测段累计的
前向墙钟 ≥168h，回拨顺延、前跳 >24h 不计入 + ≥7 个本地日历日）；②有效代际
（≥3 有效样本）≥ **20 个**；③**scan-evidenced 代际 ≥ 8 个**。

- **提前终止**：任一 provisional 告警触发 → 写停止标志并升级（§6.2）。
- **掉样处理**：rejected/missed 只计入统计；完成条件只数有效样本/代际；窗口
  不足则延长并记录原因，不降门槛。

### 2.4 最终报告形状（预留；监测完成后回填）

- 每指标 **observed max**（按代际、按全局）与分布（min/p50/p95）、**各代际
  真实 observed duration**。
- **覆盖度分母**：有效代际数（≥3 样本）；**趋势可评估代际数**（完整 4 slot）；
  current/peak 指标可评估样本数及 parser `unavailable` 数；三层负载分母
  （scan-evidenced / cpu-active-only / idle）。
- **restart policy 记录**：各时点 `{rawPresence, effectiveValue, source}`（含
  `code_default` 与 `unavailable` 形状）。
- rejected/missed 样本数与原因分布、告警触发记录（含② lifetime peak）、
  **wall-clock 起止与 clock anomaly 记录**（含前跳）。
- 结论措辞限定为描述性；**不写**「治理有效」「512MiB 合理」「GOGC 应调」；
  附 §2.3.5 能力边界声明。

## 3. API 硬化（r6 后续 2；已完成，Round 1–8 评审已独立复核通过）

`WebPushAuthorizationDecision` 零值从 `WebPushDeviceActive` 改为
`WebPushDeviceUnspecified`（拒绝但不清理）。dispatcher `default` 分支天然覆盖
零值。测试零值用例：0 次 HTTP 请求 + 订阅保留。本轮无变化。

## 4. Mac UI 展示 pushCleanupError（r6 后续 3；B4 方案 v9）

### 4.1 已交付（`124b73d`）

`DeviceRevocation`/`revokeCleanupWarning`/`.alert`/双语 L10n/`DeviceStoreTests`
2 条新用例 + `WorkspaceViewTests` stub（Swift 定向 21 条全绿）。

### 4.2 评审发现的问题（Round 1–8 累计，全部成立；1–15 已于 v2–v8 处置）

16. **R8-B1**：samples.jsonl 与 state.json 双文件缺崩溃一致性协议——写入顺序、
    fsync 边界、启动重放规则未定义。
17. **R8-B2**：异常终止（SIGKILL/掉电）的进程无法自行记录「连续第几次
    crash」——五次升级 fatal 机制不可兑现。
18. **R8-B3**：`responseTooLarge` 属 transport 家族却显式只走第 14/15 行，漏掉
    reload absent 的第 13 行（lost-response 对账）。
19. **R8-B4**：coordinator 只定义 refresh/revoke，未定义两个重叠 revoke——
    第二笔递增 token 会使第一笔 cleanup warning 丢失；v8 重写时丢失了 v7 的
    warning 生命周期规则。

### 4.3 过审后实施方案（本轮不动代码；过审后一次实施 + 定向测试 + 重建部署）

#### 4.3.1 唯一证据优先级（R4-B3 定案；Round 4–8 已核验）

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
4. **`revoked:true` + 坏 cleanup 字段**：保留撤销证据，`confirmedCleanupUnknown`。
5. **无 confirmed response** 时 reload 是唯一当前证据：`present` → 未生效/倾向
   未生效；`absent` → 已撤销（对账）；`failed` → 未知。
6. **reload `failed` 永远不从旧 `devices` 数组推断任何结论**——也不得在呈现
   中声称只有 reload 才能证明的事实（第 9 行不得声称「已从列表消失」）。

#### 4.3.2 API 边界契约（R6-B3 + R7-B3 + R8-B3）

**非 2xx body 获取**：`ManagementAPIClient` 为 revoke 增加私有 raw 请求路径，
返回 `(status: Int, data: Data)`——不修改 `performRequest` 与其他调用方。原
body 不写日志；只有类型校验通过的 `error` code 字符串进入 outcome。测试必须
经真实 HTTP client 层（StubHTTPServer），不得在 DeviceStore stub 伪造 code。

**64KiB 读取阶段硬上限（R7-B3）**：

- **流式累计**：用 delegate/bytes 流式读取并累计；**累计到第 65,537 byte 立即
  cancel 任务**（或等价有界 loader）——不是 `URLSession.data(for:)` 收完再
  检查的事后断言。
- `Content-Length > 65536` 可提前拒绝，但**不得单独依赖 header**（无
  Content-Length 的 chunked/流式响应必须被流式上限截获）。
- **oversize outcome**：`RevokeTransportIssue.responseTooLarge`——归
  transport/HTTP failure 家族，**不把截断 body 送 JSON parser**。
- **主动超限 cancel 的 reason 保持（R8-B3）**：因主动超限而 cancel 产生的
  `URLError.cancelled` **不得覆盖已锁定的 `responseTooLarge`**——实现须在
  发起主动 cancel 前先记录 oversize 判定，cancel 回调按该判定归类。
- **三条真实 HTTP 边界 fixture**：恰好 65,536 B（接受）/ 65,537 B（oversize）/
  无 Content-Length 的 chunked 流式超限（oversize）。测试证明超限后连接任务被
  **取消**、原 body 不进日志。

**五类 response + 完整分类表（无重叠、全覆盖、固定优先级，按序判定先命中先
归属）**：

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
| 9 | `revoked == true` 且 `pushCleanupError` 类型错误（非 string）或空串/纯空白 | `confirmedCleanupUnknown` |

非 2xx：`transportOrHTTPFailure(.httpStatus(status, serverErrorCode))`——
serverErrorCode = 尽力解码体 `{"error": ...}`（非 string 或缺失 → nil）；404
特例 = `status==404 && code=="not_found"`。**体超限（任意状态码）→
`transportOrHTTPFailure(.responseTooLarge)`**。

网络/取消：`transportOrHTTPFailure(.networkError(category))`——category 为
稳定、可本地化、非敏感类别（`offline` / `timedOut` / `cannotConnectToHost` /
`cancelled` / `other`，映射自 `URLError.Code` 等；不把服务器正文或本地路径进
UI）。

**签名写死**：

```swift
func revokeDevice(_ deviceId: String) async -> RevokeAttemptOutcome  // non-throwing
```

所有失败（含 cancellation）映射进 outcome；**任何 outcome（含 cancelled 与
oversize）都执行恰好一次 typed reload**——reload 自身被取消 → reload class =
`failed`。client fixture 验证 network error / cancelled / HTTP failure /
protocol failure / oversize 各进入**一次且仅一次** reload。

**typed reload outcome（归属状态管理层）**：`DeviceStore` 内部包装现有
`listDevices() async throws -> [TrustedDevice]` 为 typed outcome；不给 API 协议
新增重复 RPC；reducer 只消费返回值，不读陈旧 `devices` 数组。

#### 4.3.3 完整决策矩阵（5 × 3 = 15 行；测试逐行覆盖）

| # | response class | reload | 生效判定 | cleanup | 呈现 |
| --- | --- | --- | --- | --- | --- |
| 1 | confirmedClean | absent | 已撤销（双确认） | clean | 无警告 |
| 2 | confirmedClean | present | 已撤销 + 列表不一致 | clean | inconsistent 警告 |
| 3 | confirmedClean | failed | 已撤销（response 确认） | clean | 无撤销警告；列表刷新失败由 reload 路径呈现 |
| 4 | confirmedCleanupFailure | absent | 已撤销（双确认） | failed(err) | cleanup-failure 警告（含投递阻断声明） |
| 5 | confirmedCleanupFailure | present | 已撤销 + 列表不一致 | failed(err) | inconsistent 警告 + 清理失败附句（含投递阻断声明） |
| 6 | confirmedCleanupFailure | failed | 已撤销（response 确认） | failed(err) | cleanup-failure 警告；列表刷新失败另行呈现 |
| 7 | confirmedCleanupUnknown | absent | 已撤销（双确认） | unknown | **pending（reload-absent）** |
| 8 | confirmedCleanupUnknown | present | 已撤销 + 列表不一致 | unknown | inconsistent 警告 + cleanup-unknown 附句 |
| 9 | confirmedCleanupUnknown | failed | 已撤销（response 确认） | unknown | **pending（response-confirmed）——不得声称「已从列表消失」** |
| 10 | protocolUnknown | absent | 已撤销（reload 对账） | unknown | **pending（reload-absent）** |
| 11 | protocolUnknown | present | 倾向未生效 | unknown | **unknown 警告**（撤销结果未知；设备仍在列表） |
| 12 | protocolUnknown | failed | 未知 | unknown | **unknown 警告**（撤销与 cleanup 均未知） |
| 13 | transportOrHTTPFailure | absent | **已撤销（lost response 对账）** | unknown | **pending（reload-absent）** |
| 14 | transportOrHTTPFailure | present | 未生效 | unknown | `devicesError`（网络诊断用 category） |
| 15 | transportOrHTTPFailure | failed | 未知 | unknown | `devicesError` + 撤销状态未知；下次成功刷新自然对账 |

**特例（R8-B3 修正：`responseTooLarge` 无特例）**：`responseTooLarge` 与其他
transportOrHTTPFailure 一样**机械走第 13/14/15 行**（absent → 第 13 行
lost-response 对账，呈现 reload-absent pending 文案；present → 14；failed →
15）——服务端已完成撤销并返回异常大 body、client 超限取消后 reload 得 absent
是可达形状，不得错误呈现为未生效/未知。唯一保留的特例：`404 not_found`
（`.httpStatus(404, "not_found")`）× absent → 服务端无此设备（含此前已撤销），
无 cleanup 对象 → 无警告，按「设备已不在授权列表」呈现；其他 404 → 行 14/15。

**最低规则（由 §4.3.1 机械导出）**：

1. 撤销尝试后**总是恰好一次** typed reload（含 cancelled/oversize 路径）；
   reload `absent` 可单独确认撤销并对账 lost response（行 10/13）。
2. confirmed response 可单独确认撤销（行 2/3/5/6/7/8/9）；与 reload 冲突时不
   降级撤销结论，只追加一致性告警；投递阻断声明只依赖 confirmed 证据（行
   4/5/6 同结论）。
3. 无 confirmed response 时 reload 是唯一当前证据：`present` → 未生效/倾向未
   生效（行 11/14），不声称投递阻断。
4. reload `failed` 不从旧 `devices` 数组推断任何结论（行 3/6/9/12/15），**也
   不呈现只有 reload 才能证明的事实**（行 9 文案不含「已从列表消失」）。

#### 4.3.4 文案（七键；按证据来源拆分 pending）

- `devices_push_cleanup_warning`（行 4/6 主文案）：
  - en: "The device was revoked, but cleaning up its push subscription failed: %@. Push delivery to this device stays blocked; the runtime retries the cleanup automatically on later notifications. Check the runtime log if it keeps failing."
  - zh: "设备已撤销，但其推送订阅清理失败：%@。对该设备的推送投递已被阻断；runtime 会在后续通知投递时自动重试清理；若持续失败，可查看 runtime 日志。"
- `devices_push_cleanup_pending`（**行 7/10/13：reload-absent 确认**——列表证据
  存在，可陈述「已从列表消失」）：
  - en: "The device no longer appears in the authorized list, so the revocation is confirmed. The cleanup result of its push subscription is unknown; if it is still pending, the runtime retries it automatically on later notifications. Check the runtime log if needed."
  - zh: "该设备已从已授权列表消失，撤销已确认。其推送订阅的清理结果未知；若仍待清理，runtime 会在后续通知投递时自动重试；必要时可查看 runtime 日志。"
- `devices_push_cleanup_pending_response`（**行 9：仅 response 确认，reload
  failed**——不得声称列表已消失）：
  - en: "The runtime confirmed the revocation. The cleanup result of this device's push subscription is unknown; if it is still pending, the runtime retries it automatically on later notifications. The device list could not be refreshed; refresh it later to verify. Check the runtime log if needed."
  - zh: "runtime 已确认撤销该设备。其推送订阅的清理结果未知；若仍待清理，runtime 会在后续通知投递时自动重试。设备列表刷新失败，可稍后刷新确认；必要时可查看 runtime 日志。"
- `devices_push_cleanup_unknown`（行 11/12）：
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

1. `RevokeAttemptOutcome` 契约落地（§4.3.2）：raw request + 流式 64KiB 上限 +
   五类 response + 分类表 + 网络 category + `responseTooLarge`（主动超限
   cancel 不覆盖 reason）；`revokeDevice` 改 non-throwing。
2. `DeviceStore` typed reload outcome 包装 + §4.3.6 operation coordinator；
   任何 outcome 后恰好一次 reload。
3. Reducer 按 §4.3.1 规则机械实现（15 行矩阵 + 404 特例）。
4. **wire fixtures（client 级，经真实 HTTP 层）**：分类表 9 行 2xx 形状逐行 +
   非 2xx（404 带/不带 `not_found`、500）+ 连接丢失 + 网络错误/cancelled +
   **oversize 三边界（65,536 B / 65,537 B / chunked 无 Content-Length 超限）**
   ——各恰好一次 reload；超限后任务取消、原 body 不进日志；**主动超限 cancel
   归类为 `responseTooLarge` 而非 `networkError(.cancelled)`**。
5. **ViewModel 测试**：15 行矩阵逐行 + 404 特例 + **oversize × absent/present/
   failed 三行（absent 用 reload-absent pending 文案——R8-B3）** + 行 5/8 组合
   文案断言 + pending 两类文案不可互换断言（行 7/10/13 含「已从列表消失」；
   行 9 不含且含「runtime 已确认撤销」；行 11/12 含「无法确认」）+
   presentation seam + typed outcome 包装测试。
6. **async 交错测试（controllable continuations，§4.3.6）**：旧 reload 后
   返回、普通 refresh 与 revoke 交错、**两次重叠 revoke（R8-B4：第二次 busy、
   不发网络请求、revoke 网络次数=1、第一笔 warning 保留）**、连续两次操作、
   dismiss 与晚到结果——逐项断言最终列表、warning、devicesError、
   hasLoadedDevices、isRevoking、revoke 网络次数与 reload 次数。
7. 定向测试 → Release 重建 + 覆盖安装 + 新代际核验。

#### 4.3.6 operation coordinator（R7-B5 + R8-B4 重写）

**模型**：**统一 operation token + refresh 合并 + 并发 revoke store 层拒绝**：

- 所有 list/revoke 操作统一递增**同一个 operation generation**（token）。
- **revoke 进行中（`isRevoking == true`）时**：
  - 普通 refresh（on-appear/Retry）**不发起独立网络请求**：登记
    pending-refresh 标记，合并进 revoke 的强制 reload；
  - **第二个 revoke 调用被 store 层拒绝（R8-B4 择一）**：不发网络请求、
    **不递增 token**，返回明确的 busy 结果（`RevokeFlowResult.busy` 或等价
    typed 返回；测试可断言，不静默覆盖）——第一个 revoke 的 outcome、强制
    reload 与 cleanup warning 完整保留提交，不被 stale 丢弃。
- 若 refresh 已在飞行中而新 revoke 开始：新 revoke 递增 gen → 旧 refresh 结果变
  stale 被丢弃。**（Round 9 终审注记 1 勘误：只有飞行中的 refresh 可被判 stale；
  飞行中的 revoke 遇到第二笔 revoke 一律按上条 busy 拒绝，不递增 token。）**
- **强制 reload 走内部 forced 路径**（`DeviceStore` 私有
  `performForcedReload()`，即 typed outcome 包装）——**不调用公开
  `loadDevices()`**（后者在 `isRevoking == true` 时会被当作普通 refresh 合并掉，
  导致 revoke 自身的 reload 丢失）。
- **warning 生命周期规则（R8-B4 恢复并扩展 v7 规则）**：
  - 新 revoke **真正发起网络请求时**清除上一条 warning（旧警告不跨操作保留）；
    busy 拒绝的调用不清除任何 warning；
  - **dismiss 带 warning-generation**：dismiss 记录被清除的 warning 所属 gen；
    晚到的旧 gen 结果被丢弃，**不会复活已 dismiss 的 warning**；
  - `isRevoking` 只由 gen 仍当前的结束路径清除——**旧操作结束不得清除新操作
    的 busy 状态**。
- 最终同步提交清单（@MainActor 一次同步块）：`devices` / `hasLoadedDevices` /
  `devicesError` / warning / **`isRevoking`**。
- 交错测试断言见 §4.3.5 测试 6。

## 5. 交付与部署证据（`124b73d` 历史记录；Round 1–8 已独立复核，本轮无新部署）

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

Round 2–8 评审实测：当前 runtime PID 34681（同版本 `124b73d7b0f2`）监听 8777；
该代际变化不用于证明 §2 首采数据。

### 5.3 验证状态

代码自 `124b73d7b0f2155ed19207c3ec6fe00cdf5fa888` 未变化；Go/Swift 定向测试、
race、vet 的独立复跑记录见 Round 1–8 评审报告。本轮（v9）零代码改动，无新
构建/部署。

## 6. 不采纳清单与择一决策表

**Round 8 择一决策表（各选择点均采纳评审给出的选项之一，无否决）**：

| 选择点 | 选项 | v9 落地 |
| --- | --- | --- |
| R8-B1 journal 协议 | JSONL write-ahead truth vs 其他 journal | **JSONL 为唯一 durable truth**（唯一键 + append/fsync 后写 state + 启动重放去重；state 为可丢弃快照） |
| R8-B2 crash 计数 | dirty-run 协议 vs 删除五次 fatal 承诺 | **dirty-run 协议**（run_in_progress marker + healthy milestone + 未清计 transient；fatal/clean 先写 marker 清 dirty 再 exit 0） |
| R8-B4 并发 revoke | 串行队列 vs store 层拒绝 | **store 层拒绝**（isRevoking 时第二次调用 busy 返回、不发网络请求不递增 token） |
| R8-N2 不可信数据根 | 启动前安全失败 vs 安装器预建可信目录 | **启动前安全失败**（exit 0 + stderr 最小诊断；不承诺 fatal.json 可写；O_NOFOLLOW + fstat） |

**历史择一决策（Round 6/7，保留）**：解析失败=指标级 unavailable；t=0 原点=
startedAt；拉起=launchd；非 2xx body=专用 raw request；revoked:true+坏 cleanup=
`confirmedCleanupUnknown`；300MB=300MiB；JSONL=append-only；载体=/usr/bin/python3；
64KiB=流式硬上限；oversize=transport 家族；pending 拆分=仅第 9 行 response-
confirmed；协调=统一 token+合并；前跳上限=24h。

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

1. **本文档过审后（开发阶段，一次完成）**：实施 §4.3（raw request + 流式上限
   + 五类 response + 15 行 reducer + 七键文案 + operation coordinator + 全部
   fixtures）与 §2.3 监测脚本（python3 载体 + launchd 规格 + journal 协议 +
   dirty-run + 安全/解析契约 + 三层负载 + 测试矩阵）→ 定向测试 → Release 重建
   + 覆盖安装 + 新代际核验。
2. **监测执行**：安装 launchd 单元，按 §2.3.6 完成条件运行，最终报告回填 §2.4。
3. **owner 验收**：正常撤销自然路径（设备消失、无警告）；异常态由
   deterministic 测试验收。

## 8. Round 9 终审 checklist 勾销表

| # | 准入条件（Round 8 报告 §6） | v9 落地位置 | 测试位置 |
| --- | --- | --- | --- |
| 1 | JSONL+state 单一 durable truth、写入/fsync/重放/去重顺序；四个 crash point 的 restart-recovery 测试 | §2.3.3 持久化 journal 协议（唯一键、append+fsync→state、启动重放重建、确定性去重、不丢不重证明边界） | §2.3.3 crash-recovery 测试（①append 前/②append 后 state 前/③temp 后 rename 前/④rename 后） |
| 2 | dirty-run/healthy-milestone 跨实例 crash 计数（或删除承诺）；覆盖 SIGKILL、重置、clean/fatal exit | §2.3.3 launchd 退出语义（run_in_progress marker、healthy milestone=存活一个 discovery cycle+一次成功采样/idle 检查、未清计 transient、≥5 升级 fatal、fatal/clean 先写 marker 清 dirty 再 exit 0） | §2.3.3 launchd 测试（kill -9 累计/五次升级/milestone 重置/fatal-clean 不计入） |
| 3 | `responseTooLarge` 统一走 13/14/15 行；oversize × 三类 reload；主动超限 reason 不被 cancelled 覆盖 | §4.3.2（主动超限 cancel 不覆盖 reason）+ §4.3.3 特例（oversize 无特例，机械走 13/14/15；仅 404-not_found × absent 保留无警告特例） | §4.3.5 fixtures 4（三边界+任务取消+归类）+ 测试 5（oversize × absent/present/failed，absent 用 reload-absent pending） |
| 4 | 并发 revoke/revoke 串行或拒绝；warning/dismiss generation 规则；强制 reload 内部路径；重叠 revoke 测试 | §4.3.6（store 层拒绝 busy、不递增 token；warning 生命周期三规则含 warning-generation；performForcedReload 内部路径不被 refresh 合并） | §4.3.5 测试 6（重叠 revoke：busy/网络次数=1/第一笔 warning 保留 + 全状态与 reload 次数断言） |
| 5 | 缺键 UserDefaults effective-default 规则；数据根不可信时 fatal 诊断安全路径 | §2.3.3 实际 restart policy 记录（{rawPresence, effectiveValue, source}，缺键=code_default；present/missing/wrong-type fixture）+ §2.3.1 N2（启动前安全失败 exit 0+stderr，不承诺 fatal.json；O_NOFOLLOW+fstat） | §2.3.3 N1 三形状 fixture + §2.3.1 权限规则测试 |
