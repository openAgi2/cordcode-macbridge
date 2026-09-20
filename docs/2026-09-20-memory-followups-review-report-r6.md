# 2026-09-20 内存治理后续复审报告（Round 6）

复审对象：`docs/2026-09-20-memory-followups.md` v6（提交
`6987fce160c5ec4473bdde725b85665725d73ae9`）及 think.md 同轮提交
`caa340f53d457539452b2f6074692ca2b208b1df`。

结论：**仍不通过，但问题已一次性收敛为 3 个完整阻断簇。** Round 5 的三项要求均已
实质处置：Management token 已进入采样事务，四类 revoke outcome 与两类 unknown 文案已
写入，来源哈希和重复验证行也已修正。本轮按来源、采集安全、外部输出解析、调度恢复、
覆盖度、API wire、reducer/presentation、并发状态和发布准入九个维度重新通读，不再只审上一轮
差异。剩余问题不是产品裁决分歧，而是方案还缺少几个直接决定“能否安全实现、能否连续运行
7 天、测试能否穷尽”的契约。

本报告把每个阻断簇的子项和验收条件全部列全。下一版应一次性逐项关闭；除非修订引入新的
产品语义，本清单关闭后即可进入终审，不应再按单点往返。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=6987fce160c5ec4473bdde725b85665725d73ae9
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套仓库分支=feat/ios-native-message-timeline
配套仓库提交=389a179a1b01ad2a858f24b5b11ea41058e02381
配套仓库未提交状态=干净
配套关系依据=两仓同名功能分支 + v6 §1.2 明示组合
预期产品特性=安全、可恢复、可审计的多代际内存监测；穷尽 wire 输入并保持 reload 对账的设备撤销 reducer；文档通过后才编码
当前部署=/Applications/CordCodeLink.app 内嵌 runtime commit 124b73d7b0f2；本轮不以部署状态证明方案正确性
```

`caa340f5...` 只修改 `think.md`，`6987fce...` 只修改 followups 文档；v6 的零代码
改动声明成立。配套 iOS 工作树未被读取或修改。

## 2. Round 5 阻断项处置核验

| Round 5 项 | Round 6 结论 |
| --- | --- |
| R5-B1 Management 鉴权 | **主体已处置。** token 缺/空/不可读、Bearer、401/403、事务末重读、0600 和不归档 token 均已明确。采集器仍缺 URL/redirect/argv 安全及命令解析边界，统一列入 R6-B1。 |
| R5-B2 typed outcome + 文案 | **主体已处置。** `RevokeAttemptOutcome`、HTTP status/error code、任何 outcome 后 reload、pending/unknown 文案分离均已给出。现有网络层会丢弃错误 body，payload 分类也未穷尽，见 R6-B3。 |
| R5-B3 来源与重复行 | **已处置。** Mac/iOS/历史来源均使用 40 位哈希；v5 历史提交已记录；§5.3 重复行已删除。 |

## 3. 阻断簇一：采集事务缺少安全的 endpoint、超时和外部输出解析契约

### R6-B1.1 — 读取磁盘 URL 后携带 bearer，必须先验证 loopback 并禁止 redirect

v6 从 `runtime.json.managementUrl` 取任意字符串后附带 bearer token 发请求。如果文件残留、
损坏或被同用户进程改写为外部 URL，通用 HTTP 客户端还可能跟随 redirect，token 就会离开
本机。生产 runtime 当前确实生成 `http://127.0.0.1:<ephemeral-port>`，但脚本不能把这一事实
留作隐含假设。

实施契约必须固定：

- scheme 只能是 `http`；host 只能是数值 loopback（生产值 `127.0.0.1`；若要支持 `::1`，须
  显式列出）；禁止 userinfo、path/query/fragment，port 必须是 1...65535；
- HTTP 客户端禁止自动 redirect；任何 3xx 均 rejected，绝不把 Authorization 转发到 Location；
- URL 缺失、JSON 无效、字段类型错误、文件缺失/不可读使用独立
  `bootstrap_unavailable`/`bootstrap_invalid`，不能让脚本崩溃或混入 `command_failed`；
- 测试覆盖外部 host、非 http、userinfo、redirect、非法/缺失 port 和残缺 runtime.json。

### R6-B1.2 — “token 不回显”不足以防止 token 进入进程参数

若实现用 `curl -H "Authorization: Bearer $TOKEN"`，token 虽未写 JSONL，仍会短暂出现在 argv/
进程观察面。方案应要求使用进程内 HTTP 库，或另一种不会把 token 放入 argv、环境、临时文件
和错误文本的方式。错误归档只保留 status/reason code，不保留完整 request/header。

归档目录本身应 0700、文件 0600；启动时验证权限，不能只在创建时设置后假定不变。测试需
扫描成功/失败输出，证明假 token 不出现在 stdout、stderr、JSONL 和异常文本中。

### R6-B1.3 — 每个外部操作和整个事务都必须有硬超时

当前六步协议没有 HTTP、`vmmap`、`ps`、`sysctl` 或整笔事务的 deadline。任一命令挂住会
跳过后续计划槽、永远到不了 identity B，也无法形成 rejected sample。应预固定：

- 单 HTTP/命令超时；
- 整笔采样最大墙钟；
- 超时后终止本事务创建的子进程并记录 `command_timeout`；
- B 与 bootstrap 重读必须在总 deadline 内，不能用无界等待换取“原子性”。

fake command/HTTP 测试至少覆盖每一类超时、子进程退出回收和下一计划槽仍可执行。

### R6-B1.4 — vmmap/ps 的 wire parser 与 300MB 单位尚未定义

告警 ② 依赖外部命令文本，却没有真实输出 fixture、精确单位或缺字段策略。`300MB` 也没有说明
是 300,000,000 B 还是 300MiB；`vmmap` 的 `M/K/G` 和 `ps rss` 的 KB 不能靠实现者猜。

方案应钉死：

- 300MB 的精确 byte 常量和比较符号；建议与其他门统一写成明确 byte 值；
- 从真实 `vmmap --summary` 归档的脱敏 fixture 解析 current footprint、
  `Physical footprint (peak)` 和需要报告的 swapped 字段；K/M/G、小数、空格变化均有测试；
- peak 缺失、重复行、未知单位、非数字不得按 0；应 rejected 或把该指标标为 unavailable，且
  告警覆盖度随之下降；二者择一写死；
- `ps -o rss=` 的无表头形状、KB→byte 换算，以及命令 PID 不存在时的失败路径；
- HTTP JSON 的必需字段/类型校验：memory、startedAt、CPU available/counters 缺失时不得以
  零值制造有效样本。

这批 parser fixture 必须和纯算法 fixture 分开，防止测试只证明自造字符串能解析。

## 4. 阻断簇二：7 天监测缺少可恢复调度、实际配置门和覆盖度口径

### R6-B2.1 — t=0 同时被定义为“启动对齐”和“首次检测”，两者并不相同

§2.3.1 写 t=0/30/60/90“对齐代际启动时刻”，又把 t=0 定义为检测到新代际后的首次事务。
若脚本在进程启动 12 分钟后才发现它，后续到底采启动后 30/60/90，还是检测后 30/60/90，
会产生不同的时长和趋势序列。

下一版应只保留一种：推荐以 diagnostics `startedAt` 为代际原点，将样本分配到明确 slot，
错过的历史 slot 只记 `missed`、不补采；或者明确全程以 detection time 为观测原点，并删除
“对齐启动时刻”。同时写明代际发现轮询频率、slot 容差和系统时钟跳变策略。趋势 fixture 要
覆盖晚发现和 slot 边界。

### R6-B2.2 — 默认 120 分钟不是运行时事实，必须核对实际设置并报告可评估覆盖度

`autoRestartEnabled` 和 `autoRestartIntervalMinutes` 是可变 UserDefaults，代码每 3 秒重读；
“默认 true/120”不能证明监测期间实际值。若 interval <90、自动重启关闭、手动重启频繁，
4 样本趋势触发器可能一次也没有可评估机会。

开始与每次配置变化时应记录实际 restart policy（不得修改它），最终报告至少增加：

- 有效代际数（≥3 样本）；
- **趋势可评估代际数**（完整 4 slot）；
- current/peak 指标可评估样本数及 parser unavailable 数；
- 各代际真实 observed duration，而不是笼统称“120 分钟上界”。

“未触发趋势”只有在同时报告可评估分母时才有意义。

### R6-B2.3 — 7 天进程生命周期、睡眠和恢复没有定义

本地脚本要跨重启、Mac sleep、脚本崩溃和终端关闭运行，但文档未说明谁拉起、如何防双实例、
如何恢复状态、怎样处理 sleep 中错过的 slot。“掉样不补采”只覆盖事务 rejected，不覆盖根本
没有运行的计划槽。

方案需明确最小可靠运行模型：

- 前台常驻或 launchd/user agent 二选一；若用 launchd，配置、日志和卸载方式写清；
- 单实例锁；状态/JSONL 的原子持久化与重启恢复；重复 slot 幂等；partial line 检测；
- sleep/wake、脚本停机、runtime 不存在分别记录 missed/rejected 原因；醒来不补造旧样本；
- “≥7 个自然日”精确定义为本地日历覆盖还是 elapsed ≥168h；系统时钟回拨不应缩短窗口；
- provisional 告警触发后的停止标志持久化，重启后不能继续悄悄采集。

用 fake clock/state store 覆盖重启恢复、双实例、sleep 跳槽、重复唤醒、部分写入和告警后恢复。

### R6-B2.4 — CPU 60 秒只能证明 runtime-active，不能单独证明“真实负载”

后台 scan counter delta 能证明发生扫描/turn 拉取；CPU delta ≥60s 只能证明进程消耗 CPU，
也可能来自忙循环、维护任务或缺陷。v6 把二者 OR 后统一称“有载/真实负载”，并断言 idle
保活远低于 60s，但没有分布证据。

应把代际至少分成：`scan-evidenced`、`cpu-active-only`、`idle/low-activity`；60s 与其他阈值
一样标为 provisional，不把 CPU-only 直接写成用户真实负载。完成条件“≥8 个有载代际”需明确
是否只数 scan-evidenced，或允许 CPU-active-only 但最终报告分别列分母；不能合并后宣称真实
工作负载覆盖。

## 5. 阻断簇三：revoke outcome 在现有网络层不可实现，wire 状态仍未穷尽

### R6-B3.1 — `performRequest` 抛错前丢弃 body，无法“尽力解码 error code”

当前 `performRequest` 获得 `(data, response)` 后，对非 2xx 只抛
`ManagementError.httpError(Int)`；`data` 已丢失。v6 却要求 catch 该错误后再解码响应体
`{"error":"not_found"}`，调用层已经拿不到 body，因此 404 特例按现方案不可实现。

下一版必须择一：

- 为 revoke 使用返回 `(status, data)` 的专用 raw request；或
- 修改底层 HTTP error，使其携带 status 和受控 data/errorCode（同时审计其他调用方）。

推荐专用 raw request，减少改变所有 Management API 的影响面。无论选择哪种，都要限制响应体
大小、不把原 body 写日志，只把经过类型校验的 `error` code 放入 outcome。测试必须真正经过
HTTP client 层，不能在 DeviceStore stub 里直接伪造 code 来绕过丢 body 的现状。

### R6-B3.2 — protocolUnknown 五类并未覆盖全部 JSON shape

当前枚举把 `{}` 注释成 `emptyBody`，同时又有 `missingRevokedKey`，边界重叠；还缺：

- 真正的零字节/纯空白 body；
- 合法 JSON 但顶层为 `null`、array、string/number/bool；
- `revoked:null`；
- `revoked:true` 但 `pushCleanupError` 类型错误；
- `pushCleanupError` 为空串/空白串；
- 非 2xx body 的 `error` 存在但类型不是 string。

不一定要为每个形状增加 enum case，但必须给出**无重叠、全覆盖、固定优先级**的分类表。例如
零字节→emptyBody、有效 object 缺键/`null`→missing-or-null、顶层/字段类型错→typeMismatch、
语法错→malformed。还要明确：`revoked:true` 与坏 cleanup 字段是保留“撤销已确认 + cleanup
unknown”，还是整包降级 protocolUnknown；当前四类 response 没有前一种状态，必须书面选择并
让矩阵匹配，不能依赖 JSONDecoder 偶然的失败顺序。

### R6-B3.3 — network outcome 丢失可呈现诊断，且方法是否 `throws` 未写死

`.networkError` 无 associated code/message，但矩阵行 11/12 要设置 `devicesError` 并声称保留
现有失败语义。应至少携带稳定、可本地化、非敏感的错误类别（如 `URLError.Code`/cancelled/
other），不得把任意服务器正文或本地路径直接进 UI。

协议签名也应明确写成 `async -> RevokeAttemptOutcome`（non-throwing），或列出唯一仍可 throw
的不可映射错误及其“仍然 reload”路径；“任何 outcome 都 reload”不能遗漏 throw/cancellation。
client fixture 需要验证网络错误、cancelled、HTTP failure、protocol failure 都进入一次且仅一次
reload。

### R6-B3.4 — `@MainActor` 不等于 async 事务原子，需防旧 reload 覆盖新状态

`DeviceStore` 在 `await revokeDevice` 和 `await listDevices` 处可重入；页面 on-appear 刷新、错误区
Retry 或另一操作可与撤销 reload 交错。文档虽写“原子更新”，没有 generation/operation token，
旧请求可能在新请求后返回并覆盖 devices/error/warning。

实施应为 revoke flow 建立 operation generation（或等价 stale-result guard），最终 reducer 只在
generation 仍当前时一次提交 `devices`、`hasLoadedDevices`、`devicesError`、warning；开始新操作
时清理/保留旧警告的规则也写死。测试用 controllable continuations 覆盖：旧 reload 后返回、
普通 refresh 与 revoke 交错、连续两次操作、dismiss 与晚到结果。无需扩大到 UI automation。

## 6. 已通过项与非阻断整理

- P0 来源清单、提交归属和 docs-only 声明通过。
- epoch 派生、bootstrap/token 末次重读、A/B/startedAt 防混代逻辑通过；只需按 R6-B1 补安全
  与 failure shape，不再重开算法选择。
- 绝对/current、lifetime peak、4 样本趋势三类 OR 及 provisional 语义通过；只需精确 300MB
  单位和覆盖度。
- 7 天/20 有效代际/8 活跃代际可以保留；“活跃”分层按 R6-B2.4 改名与报告，不要求重新讨论
  受控 27h 窗口。
- confirmed-first、reload absent 对账、12 行矩阵和 404-not_found × absent 特例通过；只需让
  client 真正产生这些 outcome，并补齐 payload 分类。
- pending/unknown/inconsistent/cleanup-failure 四类文案的产品方向通过；文案无需再改写，除非
  R6-B3.2 选择增加“confirmed revoke + cleanup unknown”状态。
- owner 仅验收正常撤销自然路径、异常态由 deterministic tests 验收的分工通过。
- §5.3 当前写法通过；本轮无需构建、测试、安装或部署。

## 7. 终审准入清单（下一版一次性逐项勾销）

1. Management URL 严格 loopback 校验、禁 redirect、token 不进 argv/env/temp/output，bootstrap
   无效分类和权限检查写入规格与 fixture。
2. HTTP/命令/整事务硬超时、子进程回收、真实 vmmap/ps fixture、300MB 精确 byte 常量和所有
   unavailable/failure 规则写入规格。
3. 统一 t=0 原点；定义 discovery cadence/slot tolerance/missed slot；核对实际 restart policy；
   增加 trend/peak 可评估分母。
4. 写明 7 天监测的拉起方式、单实例、持久状态、sleep/crash/restart 恢复、幂等与告警后停止；
   “自然日”给出精确定义。
5. scan-evidenced、CPU-active-only、idle 分层；60s 降级 provisional；修正“真实负载”措辞及
   ≥8 活跃代际的计数口径。
6. 解决非 2xx body 丢失：专用 raw revoke request 或携 body 的 typed HTTP error 二选一；真实
   client fixture 证明 404 error code 可达。
7. 给 2xx/非 2xx JSON shape 做无重叠全覆盖分类；明确坏 cleanup 字段是否保留 revoked:true
   证据；补全部 wire fixtures。
8. 写死 non-throwing/throwing 签名、保留可本地化网络诊断，并保证所有 failure/cancel 路径一次
   reload。
9. DeviceStore 加 operation generation/stale guard，定义一次性状态提交与旧 warning 清理规则，
   补 async 交错测试。
10. 下一轮仍只改文档。以上 9 项全部在正文和测试清单中可定位后，可进入 Round 7 终审；终审
    不再重新讨论已通过的产品裁决，只检查本清单闭合和修订是否引入新矛盾。
