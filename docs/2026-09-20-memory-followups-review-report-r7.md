# 2026-09-20 内存治理后续终审报告（Round 7）

终审对象：`docs/2026-09-20-memory-followups.md` v7（提交
`908444f7a83c1f10787ea1afe90578c1cbe768cd`）及同轮 `think.md` 提交
`8938a018a74ccda3f478401ffd2b82789dfec1c2`。

结论：**暂不通过。Round 6 的 9 项准入要求均有对应处置，但 v7 在把方案具体化时引入了
5 个新的实现级矛盾。** 其中两个会破坏 7 天监测的单实例/可恢复性，三个会让撤销流程失去
响应体上限、展示未经证实的事实，或在并发刷新时丢失撤销结果。它们不是继续重开已裁决的
产品方向，而是对 v7 新增机制做端到端推演后发现的闭合缺口。

本轮已按来源、采集器进程模型、文件持久化、调度恢复、指标解析、HTTP wire、15 行 reducer、
文案与异步交错完整复核。下文同时列出阻断项、非阻断项及一份最终验收清单；下一版不需要再
扩写背景或重新解释 Round 1–6，只需逐项修正这些可定位问题。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=908444f7a83c1f10787ea1afe90578c1cbe768cd
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套仓库分支=feat/ios-native-message-timeline
配套仓库提交=389a179a1b01ad2a858f24b5b11ea41058e02381
配套仓库未提交状态=干净
配套关系依据=两仓同名功能分支 + v7 §1.2 明示组合
预期产品特性=安全、可恢复、可审计的多代际内存监测；穷尽 wire 输入并保持 reload 对账与并发一致性的设备撤销 reducer；文档通过后才编码
当前部署=/Applications/CordCodeLink.app 内嵌 runtime commit 124b73d7b0f2；本轮不以部署状态证明方案正确性
```

`8938a018...` 只修改 `think.md`，`908444f7...` 只修改 followups 文档；v7 的零代码
改动声明成立。配套 iOS 工作树未被读取或修改。

## 2. Round 6 九项准入条件核验

| Round 6 项 | Round 7 核验 |
| --- | --- |
| URL/redirect/token/权限 | 主体已处置；URL 白名单、禁 redirect、进程内 HTTP 和权限目标均已写明。启动失败与 launchd 重启语义仍未闭合，见 R7-B2。 |
| 三级超时与外部 parser | 已处置；HTTP/命令/事务 deadline、真实 fixture、二进制阈值和 unavailable 规则已明确。 |
| startedAt slot/restart policy/覆盖度 | 主体已处置；slot、分母和只读探测均已写明。实际 app domain 仍留待实施时决定，见 R7-B2。 |
| 7 天拉起、锁、持久恢复 | **未真正闭合。** `flock` 的对象同时被 atomic rename 替换，不能保证单实例，见 R7-B1。 |
| 三层负载口径 | 已处置；只有 scan-evidenced 计入完成门。可选 counter 的 wire schema 仍需补一个小契约，见 N1。 |
| 非 2xx body | 主体已处置；专用 raw request 可拿到 status/data。但 64KiB 上限尚无可执行的读取策略和 overflow outcome，见 R7-B3。 |
| 2xx/非 2xx 全分类 | 已处置；9 行 2xx 表与 `confirmedCleanupUnknown` 闭合。需给 oversize 增加分类。 |
| non-throwing/network/cancel/reload | 已处置；签名、网络类别和每次一次 reload 已写死。 |
| generation/stale guard | **未闭合。** 普通 refresh 与 revoke 谁取得 generation 优先权未定义，`isRevoking` 也不在原子提交内，见 R7-B5。 |

## 3. 阻断项

### R7-B1 — 不能在会被 `rename` 替换的状态文件 inode 上做单实例锁

v7 §2.3.3 同时规定：

- “状态文件 flock + PID”（第 244 行）；
- 状态文件用 `tmp + rename` 原子持久化（第 245 行）。

`flock` 锁住的是已打开的 inode。进程 A 锁住旧状态文件后，用临时文件 rename 覆盖路径；
进程 B 再打开该路径时得到的是新 inode，可以成功取得另一把锁。此时两个采集器都认为自己是
单实例，并发写状态/JSONL，正好破坏 v7 要防的双实例和重复 slot。

修订必须改为**独立、稳定、生命周期内绝不 rename/unlink 的 lock 文件**，例如
`monitor.lock`：启动时 `open` 后持有 flock 到进程退出；状态快照仍可单独 tmp+rename。PID 只能
作为诊断字段，不能代替内核锁。测试需真实 fork/双进程覆盖：持锁进程反复替换 state 文件时，
第二进程始终无法进入；持锁进程异常退出后第二进程才能取得锁。仅用 fake state store 的单进程
测试不能证明这个性质。

同时应把 JSONL 的写法择一写死：若每次整文件 tmp+rename，则不存在“末行 partial append”的
正常恢复模型；若 append，则需在同一稳定锁下 append、检测/截断 partial line，并把 state 的
atomic snapshot 与 JSONL append 分开描述。当前把两种模型叠在一句中，实施者无法机械落地。

### R7-B2 — launchd 运行单元仍不是可安装、可恢复的确定规格

v7 第 241–257 行把 `Label`、`ProgramArguments`、日志路径、卸载方式和 app domain 都留到
“实施时回填”，但同一节又依赖这些值证明 7 天无人值守和实际 restart policy。这里缺的不是
文档装饰，而是进程能否启动以及读到哪个 app container 的关键输入：

1. “脚本”使用何种仓内运行时/可执行物未选定。launchd 环境不能依赖交互 shell 的 PATH、
   pyenv/Homebrew 或 Codex 会话；`ProgramArguments` 必须是绝对、稳定、部署后仍存在的路径。
2. plist 安装路径、Label、working directory、stdout/stderr、状态/归档/lock 的绝对根目录未
   固定，无法判断权限检查、单实例和卸载是否作用于同一份数据。
3. app domain 已可由当前工程确定为 `org.openagi.cordcode.link`
   （`MacBridge/CordCodeLink.xcodeproj/project.pbxproj:524`），不应继续留作实施时猜测。
4. `KeepAlive={SuccessfulExit:false}` 会重启非零退出进程。永久配置/权限错误若按“拒绝启动”
   非零退出，会进入无限 crash loop；文档没有区分 transient crash、永久 fatal stop 和告警后的
   clean stop，也没有规定 fatal marker/退出码/节流策略。

下一版应选择一个仓库拥有、可随任务交付的具体实现载体，给出完整 plist 模板和绝对路径契约；
固定 app domain；定义安装、bootstrap/bootout、升级和卸载；并写死退出语义：哪些错误由进程
修复，哪些写持久 fatal 状态后以成功退出防止 launchd 重启，哪些才允许非零退出重试。对应测试
至少覆盖最小 launchd 配置生成、缺依赖/坏权限不形成重启风暴、告警 stop 与 fatal stop 的恢复
方式。无需实际等待 7 天，也无需 UI automation。

### R7-B3 — “64KiB 响应体上限”目前只是事后断言，不是读取上限

v7 §4.3.2 规定 raw revoke request 返回 `(status, data)` 且响应体上限 64KiB，但未规定如何
在网络读取阶段强制上限，也未给超过上限的 outcome。若直接使用现有风格的
`URLSession.data(for:)`，系统会先把完整 body 收进 `Data`，调用方随后检查 `data.count` 已经
失去限制内存占用的意义；恶意/损坏的本地响应仍可分配任意大 body。

方案必须二选一写死：使用 delegate/bytes 流式累计并在第 65,537 byte 立即 cancel，或使用等价
的有界 loader；`Content-Length > 65536` 可提前拒绝，但不能单独依赖 header。还需定义
`responseTooLarge` 归入哪个稳定 outcome（建议 protocol/HTTP failure 的专用 reason，不把截断
body继续送 JSON parser），并增加三条真实 HTTP fixture：恰好 65,536 B、65,537 B、无
Content-Length 的 chunked/流式超限。测试还应证明超限后连接任务被取消，原 body 不进入日志。

### R7-B4 — pending 文案把 response 确认误写成“列表已消失”

15 行矩阵的第 9 行是 `confirmedCleanupUnknown × reload failed`：撤销只由
`revoked:true` response 确认，列表刷新失败，**没有任何证据证明设备已从当前授权列表消失**。
但 §4.3.4 把第 7/9/10/13 行都映射到同一个 `devices_push_cleanup_pending`，首句固定为
“该设备已从已授权列表消失”。因此第 9 行会向用户展示一个未观测到的事实。

需按证据拆分文案，不能只改测试关键词：

- response-confirmed（至少第 9 行，亦可覆盖第 7 行）应写“runtime 已确认撤销”；
- reload-absent（第 10/13 行，及第 7 行的双确认）才可写“设备已从列表消失”；
- cleanup unknown 的后半句可以共享。

ViewModel 测试除“已确认/无法确认”外，还必须断言 reload failed 的文案不含“已从列表消失”，
reload absent 的文案才可包含该事实。矩阵本身无需重裁决。

### R7-B5 — 单一 generation 没有定义操作优先级，且漏掉 `isRevoking`

v7 §4.3.6 要求“所有写 devices 的异步路径共享同一 generation”，但只说每次撤销取得 gen，
没有说明普通 on-appear/Retry refresh 是否递增 gen，以及它与进行中的 revoke 谁应胜出：

- refresh 若递增 gen，会使已经在服务端生效的 revoke flow 变 stale；随后 response/reload/warning
  被丢弃，cleanup failure 的可观测性也随之丢失；
- refresh 若不递增 gen，旧 refresh 又可能在 revoke reload 后覆盖新列表；
- 两者若各自独立 generation，则仍需定义最终写入的偏序，不能只说“共享 guard”。

此外，最终同步提交清单只有 `devices/hasLoadedDevices/devicesError/warning`，没有
`isRevoking`。当前生产实现使用 `defer { isRevoking = false }`
（`MacBridge/MacBridge/ViewModels/DeviceStore.swift:56-57`）；连续操作或 stale 旧操作完成时，
旧 defer 可把新操作仍在进行中的状态提前清掉，重新开放按钮。

下一版应选定一个明确模型，例如：所有 list/revoke 操作统一递增 token，revoke 期间普通 refresh
被串行化/合并到 revoke 的强制 reload，或用带优先级的 operation coordinator；无论选择哪种，
必须保证服务端撤销已发生后其 reducer/warning 不会被无关 refresh 静默取消。`isRevoking`、错误、
warning 和列表状态都要受同一 current-operation 检查；旧操作结束不得清除新操作的 busy 状态。
交错测试应逐项断言**最终列表、warning、devicesError、hasLoadedDevices、isRevoking 以及 reload
次数**，而不只是“旧结果未覆盖 devices”。

## 4. 非阻断但应随本次一次性收口

### N1 — `agentBackgroundScans:*` 是完成门证据，仍需最小 wire schema

§2.3.2 只说该对象“可选”，§2.3.4 却用其 delta 决定是否计入 8 个 scan-evidenced 代际。应固定
对象必须为 map，`scans/turnItemRequests/scannedTurns` 为非负整数；缺对象表示该 backend 无此
证据，字段缺失/类型错/计数回退应标该 backend counter unavailable，而不是按 0、负 delta 或
整笔样本失败。代际内至少任一同名 counter 首末可比较且 delta > 0 才算 scan-evidenced。增加
缺字段、类型错、计数回退和多 backend fixture，防完成门被 malformed optional 数据误触发。

### N2 — 168h 应防系统时钟向前跳导致提前完成

v7 已处理回拨，却未处理向前大跳。应把完成门的 elapsed 定义为可持久化的累计前向小步时长，
或给单次 forward jump 设置 anomaly 上限并暂停完成判定；否则手工改时钟/NTP 异常可瞬间满足
168h。最终报告保留 wall-clock 起止和 anomaly，7 个本地日历日仍作为独立门。该项不改变
采样 slot 的既有裁决。

### N3 — 权限不匹配的“修复或拒绝”需从二选一变成确定规则

§2.3.1 第 155–156 行的“修复或拒绝启动”会让实现和测试有两种合法答案。建议写死：owner
仍是当前 uid 且仅 mode 过宽时 chmod 收紧；owner/类型/symlink/不可修复异常时写 fatal reason
并按 R7-B2 的永久停止语义退出。临时文件从创建瞬间就用 0600，不能先按默认 umask 创建再
事后 chmod。

## 5. 已通过且不应再重开

- P0 来源清单、40 位哈希、docs-only 提交归属与工作树状态通过。
- URL loopback 白名单、禁 redirect、Bearer/token 生命周期、A/B/bootstrap 三重防混代和 epoch
  派生算法通过。
- HTTP/命令/事务三级 deadline、子进程只回收本事务子进程、真实 vmmap/ps fixture、MiB 常量、
  指标级 unavailable 与 payload-invalid 规则通过。
- startedAt slot 原点、15 分钟窗口、missed 不补采、三类 OR 告警、provisional 语义和报告分母
  通过。
- scan-evidenced / cpu-active-only / idle 的产品口径，以及完成门只计 scan-evidenced 通过；只补
  N1 的 wire 细节。
- 2xx 的 9 行分类、`confirmedCleanupUnknown`、non-throwing outcome、稳定网络类别、取消后一次
  reload 和 15 行产品矩阵通过；只修 B3/B4/B5，不重写 confirmed-first 裁决。
- owner 只人工验收正常撤销路径，异常态由 deterministic unit/client tests 验收的分工通过。
- 本轮是 D0 文档评审，无需 build、unit test、UI test、安装或部署。

## 6. Round 8 最终准入清单

1. 锁改为独立稳定 lock inode；state snapshot 与 JSONL append/replace 模型分开；真实双进程测试
   证明 state rename 期间仍只有一个实例。
2. 固定采集器实现载体、绝对安装/数据/log/plist 路径、Label、ProgramArguments、app domain
   `org.openagi.cordcode.link`、安装/卸载命令和永久 fatal/transient crash/clean stop 退出语义。
3. 64KiB 改为网络读取阶段硬上限；定义 oversize outcome；补 65536/65537/chunked 三个真实
   HTTP 边界测试。
4. 拆分 response-confirmed 与 reload-absent 的 pending 文案；第 9 行不得声称列表已消失。
5. 定义 refresh/revoke 的 generation 优先级或串行协调；把 `isRevoking` 纳入 stale guard 与原子
   状态断言；交错测试检查全部可见状态和 reload 次数。
6. 同轮补 N1 counter schema、N2 forward clock anomaly、N3 确定的权限处置规则。
7. 下一轮仍只改文档，不写产品代码。以上均可在正文与测试清单逐项定位后，Round 8 只做
   checklist verification；若没有新增自相矛盾，即可通过文档评审并进入实施，不再重开 Round
   1–6 已通过的裁决。
