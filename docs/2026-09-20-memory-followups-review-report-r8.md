# 2026-09-20 内存治理后续终审报告（Round 8）

终审对象：`docs/2026-09-20-memory-followups.md` v8（提交
`b1c5ff70269ac3408fb2ad942df075c63659ffe6`）及同轮 `think.md` 提交
`5626248941ce88fa3ea85720a7fca96fa21b4671`。

结论：**仍暂不通过，但 Round 7 的 5 个阻断项和 3 个非阻断项均已正面处置；本轮只剩
4 个由 v8 具体机制推演出来的闭合缺口。** 其中：双文件持久化缺少崩溃一致性真相，launchd
无法按当前文字可靠计算“连续 crash”，oversize 与 15 行矩阵之间存在明确漏分支，operation
coordinator 仍未定义并发二次 revoke。另有两个小的可执行性修正应同期完成。

这次没有重新讨论 Round 1–7 已通过的产品裁决。以下每项都给出确定修法和测试边界；下一版只
需做这些局部修正，不再扩写背景、阈值、文案体系或矩阵主体。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=b1c5ff70269ac3408fb2ad942df075c63659ffe6
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套仓库分支=feat/ios-native-message-timeline
配套仓库提交=389a179a1b01ad2a858f24b5b11ea41058e02381
配套仓库未提交状态=干净
配套关系依据=两仓同名功能分支 + v8 §1.2 明示组合
预期产品特性=安全、可恢复、可审计的多代际内存监测；有界 HTTP wire 读取；保持 reload 对账、文案证据和并发一致性的设备撤销 reducer；文档通过后才编码
当前部署=/Applications/CordCodeLink.app 内嵌 runtime commit 124b73d7b0f2；本轮不以部署状态证明方案正确性
```

`56262489...` 只修改 `think.md`，`b1c5ff70...` 只修改 followups 文档；v8 的零代码
改动声明成立。配套 iOS 工作树未被读取或修改。`git diff --check
2ecad95f...b1c5ff70` 通过。

## 2. Round 8 七项准入核验

| Round 8 项 | 核验结论 |
| --- | --- |
| 稳定 lock inode + 双持久化模型 | lock inode 问题已修复，真实双进程测试也已列出；但 state 与 JSONL 之间仍没有崩溃事务协议，见 R8-B1。 |
| launchd 完整规格 | 载体、路径、Label、app domain、安装/卸载和三类退出已明确；“连续 transient”无法从异常终止中可靠得知，见 R8-B2。 |
| 64KiB 读取硬上限 | 流式上限、取消与三条边界 fixture 已写明；但 oversize 明确漏掉 reload-absent，见 R8-B3。 |
| pending 文案拆分 | **通过。** 第 9 行只陈述 response-confirmed，不再声称列表消失；测试断言匹配证据来源。 |
| refresh/revoke coordinator | refresh 与 revoke 的优先级、`isRevoking` 原子提交均已写明；并发 revoke/revoke 仍无规则，见 R8-B4。 |
| N1/N2/N3 | counter schema、前跳 anomaly、权限确定规则均已处置；UserDefaults 缺键与 fatal marker 不可写场景需补，见 N1/N2。 |
| docs-only | **通过。** 两个提交均为单文件文档提交，工作树干净。 |

## 3. 阻断项

### R8-B1 — `samples.jsonl` 与 `state.json` 仍不是一个可恢复事务

v8 §2.3.3 正确地把 stable lock、atomic state snapshot 和 append-only JSONL 分开了，但每个
有效样本同时影响两个文件：JSONL 增加记录，state 更新 slot/代际/完成计数。单实例锁只能防并发，
不能让两个文件原子提交。当前没有规定写入顺序、fsync 边界或启动时的权威重放规则：

- 先 append JSONL，随后在 state rename 前崩溃：重启后 state 认为 slot 未提交，可能重复采样；
- 先写 state，随后在 JSONL append 前崩溃：state 认为 slot 已完成，但报告缺失原始样本；
- append 已进用户态缓冲但未落盘，state 已替换：仍是后一种形状；
- partial-tail 截断只解决半行，解决不了“完整 JSONL 行存在但 state 落后”或反向不一致。

修订应明确一个唯一真相。推荐让 JSONL 成为 write-ahead/source-of-truth：每条记录含稳定唯一键
`(pid, epoch, slot, recordKind)`；在持锁状态下 append 完整行、flush+fsync 后再写 state snapshot；
启动时先截断 partial tail，再重放 JSONL，以唯一键重建/校正 state，并将重复完整行确定性去重。
state 只作为可丢弃加速快照，绝不能覆盖日志中更晚的已提交事实。若选择其他 journal 协议，也
必须给出等价的不丢不重证明。

测试必须注入至少四个 crash point：append 前、完整 append 后/state 前、state temp 写后/rename
前、state rename 后；每个点重启恢复后断言同一 slot 恰好一个有效事实、完成计数一致、告警 stop
不丢失。真实双进程锁测试保留，但它不能替代这组 crash-recovery 测试。

### R8-B2 — 进程自身无法凭空知道上一个实例是“连续第几次 crash”

v8 第 310–322 行要求脚本把连续 transient 计数保存在 state，达到 5 次后写 fatal marker。
如果顶层 Python 异常被捕获，当前实例可以在非零退出前记一次；但真正的异常终止（SIGKILL、
解释器崩溃、机器掉电）没有机会更新 state。launchd 会看到退出状态并重启，下一实例却拿不到
“前一实例为何退出”的可靠输入。于是最需要防的 crash loop 可能永远保持计数 0。

需把机制改为可恢复的 dirty-run protocol，而不是要求死亡进程写遗言：每次启动在进入主循环前
原子写 `run_in_progress`/run generation；只有达到预定义的 healthy milestone 后才清除或重置
连续失败计数；重启发现未清 dirty marker 时，将前一代计为一次 transient。还必须定义
healthy milestone（例如成功存活并完成一次 discovery cycle，不能只“启动成功一瞬间”就清零）
和时间窗口，避免每 31 秒崩一次却永不累计。fatal/clean stop 必须先写各自 marker、清 dirty
marker，再 exit 0，不能被下一次启动误算为 crash。

测试除可捕获异常外，应真实覆盖 `kill -9` 后重启累计、连续五次升级 fatal、达到 healthy
milestone 后重置、fatal/clean exit 不计入 transient。若不想实现 crash-loop 计数，应删除“5 次
自动 fatal”的承诺并依赖 launchd throttle；不能保留一个在 SIGKILL 路径不可实现的安全断言。

### R8-B3 — `responseTooLarge` 被归入 transport 家族，却显式跳过了第 13 行

v8 已规定 oversize 属于 `transportOrHTTPFailure`，因此 reload 结果仍应完整落入矩阵：

- absent → 第 13 行，lost-response 对账确认撤销；
- present → 第 14 行；
- failed → 第 15 行。

但 §4.3.2 第 449–451 行写“按行 14/15”，§4.3.3 第 518–520 行再次写“其他 404 与
oversize → 行 14/15”，两处都排除了 absent。真实形状完全可达：服务端已经完成撤销并开始返回
异常大 body，client 在第 65,537 byte 取消读取，随后 reload 得到 absent。若按文字实现，会把
已由 reload 对账确认的撤销错误地呈现成未生效/未知。

修订只需统一为：`responseTooLarge` 无特例，机械走第 13/14/15 行；404-not_found × absent
仍保留现有无警告特例。wire/ViewModel 测试增加 oversize × absent/present/failed 三行，尤其断言
absent 使用 reload-absent pending 文案。流式 loader 还需保证“因主动超限 cancel 产生的
URLError.cancelled”不会覆盖已经锁定的 `responseTooLarge` reason。

### R8-B4 — coordinator 只定义了 refresh/revoke，没有定义 revoke/revoke

§4.3.6 规定所有 list/revoke 共用 token，并定义了 revoke 期间普通 refresh 合并，但没有规定
第二个 revoke 在第一个 revoke 尚未完成时如何处理。UI 当前通常用 `isRevoking` 禁用按钮，不能
代替 store 层不变量：方法仍可由测试、未来入口或重复 Task 调用。若第二个 revoke 直接递增 token，
第一个服务端撤销可能已生效，但其 cleanup failure、mandatory reload 和 warning 会因 stale 被
丢弃；若两个都继续，最终列表可能对，第一台设备的清理告警却永久丢失。

必须择一写死并测试：

- **串行队列**：每个 revoke 保留自己的 outcome + 强制 reload + warning，按序提交；或
- **store 层拒绝/合并并发 revoke**：`isRevoking` 时第二次调用不发网络请求，并返回/记录明确
  busy 结果；不能静默覆盖 token。

同时补回 v7 已有而 v8 重写时消失的 warning 生命周期规则：新 revoke 开始是否清除上一条
warning、dismiss 只清当前 warning 还是带 warning-generation、旧结果能否复活已 dismiss 状态。
mandatory reload 必须走内部强制路径，不能调用会因 `isRevoking == true` 而被当作“普通 refresh”
合并掉的公开 `loadDevices()`。交错测试应增加两次**重叠** revoke（不是仅顺序调用），并继续断言
列表、warning、error、busy、网络 revoke 次数和 reload 次数。

## 4. 非阻断但应同期修正

### N1 — `defaults read` 的“缺键”是正常有效配置，不应成为采集错误

源码对缺键使用有效默认值：`autoRestartEnabled ?? true`、
`autoRestartIntervalMinutes ?? 120`。本机只读取证也证明当前
`autoRestartIntervalMinutes` 缺键时，`defaults read ... autoRestartIntervalMinutes` 返回错误，
而不是 `120`。因此 §2.3.3 第 324–326 行不能把两个命令成功当作唯一有效形状。

规格应记录 `{rawPresence, effectiveValue, source}`：键存在则严格解析类型和值；键缺失则使用与
源码一致的 true/120 并标 `source=code_default`；domain/命令失败或类型错误才标 unavailable，且
不得修改偏好。增加 present、missing 和 wrong-type fixture。

### N2 — 不可信数据根可能无法安全写入 `fatal.json`

权限规则规定数据根/文件 owner、类型或 symlink 错时写 `fatal.json` 后 exit 0。但若数据根本身
owner 错、不可写或是 symlink，把 marker 写回同一根目录要么失败，要么违反“发现 symlink 后不
跟随”的规则；launchd 的 StandardOut/StandardErrorPath 也可能在脚本校验前已打开该路径。

实施规格应允许此类启动前安全失败以 exit 0 + launchd 可见的最小诊断结束，而不虚假承诺 marker
一定写成；或者为 fatal marker/log 选择由安装器预创建并验证、与被检查数据根分离的可信目录。
打开 lock/state/temp/log 时使用 no-follow + open 后 fstat 校验，避免仅 lstat→open 的竞态。

## 5. 已通过且不再重开

- Round 7 的稳定 lock inode、state/JSONL 模型分离、真实双进程锁测试方向通过；只补 R8-B1 的
  跨文件 crash consistency。
- Python 标准库载体、绝对 ProgramArguments、plist Label、app domain、安装/升级/bootout、
  KeepAlive/ThrottleInterval 和 fatal/clean exit 方向通过；只补 R8-B2/N2。
- URL/token/redirect/权限目标、六步防混代、三级 deadline、外部 parser、counter schema、前后
  时钟 anomaly、三层负载和完成门通过。
- 64KiB 读取阶段硬上限、Content-Length 非唯一依据、三条边界 fixture 和
  `responseTooLarge` typed reason 通过；只修 R8-B3 的矩阵覆盖。
- 七键文案和第 9 行 response-confirmed 文案通过。
- refresh/revoke 的统一 token、refresh 合并、旧 refresh stale、`isRevoking` 纳入原子提交方向
  通过；只补 R8-B4 的 revoke/revoke 与 warning 生命周期。
- 15 行证据优先级、404 特例、任何 outcome 后一次 reload、owner/测试验收分工均不重开。
- 本轮 D0 文档评审无需 build、unit/UI test、安装或部署。

## 6. Round 9 最终 checklist

1. 为 JSONL + state 定义单一 durable truth、写入/fsync/重放/去重顺序；补四个 crash point 的
   restart-recovery 测试。
2. 用 dirty-run/healthy-milestone 协议实现跨实例连续 crash 计数，或删除不可兑现的“五次 fatal”
   承诺；覆盖 SIGKILL、重置及 clean/fatal exit。
3. `responseTooLarge` 统一走第 13/14/15 行；补 oversize × 三类 reload，并保持主动超限 reason
   不被 cancelled 覆盖。
4. 明确并发 revoke/revoke 是串行还是 store 层拒绝；恢复 warning/dismiss generation 规则；强制
   reload 使用不会被 refresh 合并的内部路径；补重叠 revoke 测试。
5. 同期补缺键 UserDefaults 的 effective-default 规则，以及数据根不可信时 fatal 诊断的安全路径。
6. 下一轮仍只改文档。以上五项在正文和测试清单可定位后，只核 checklist；没有新矛盾即可明确
   通过方案评审并进入开发阶段，不再重开既有产品裁决。
