# 2026-09-20 内存治理后续终审报告（Round 9）

终审对象：`docs/2026-09-20-memory-followups.md` v9（提交
`5f0683dc16376244c40bbc4b850951f7cd6a64ce`）及同轮 `think.md` 提交
`f845a3a8a77c1f8a4cae999be38f3d36817b243b`。

结论：**通过。方案评审结束，可以进入开发阶段。** Round 8 的 4 个阻断项与 2 个同期修正均
已形成可实现、可测试的闭环：JSONL journal 有唯一 durable truth 与 crash recovery；跨实例
crash 计数不再要求死亡进程写状态；oversize 完整进入第 13/14/15 行；并发 revoke 在 store
层 fail-fast busy，第一笔 outcome/reload/warning 不丢；缺键默认值和不可信数据根也有诚实的
处理边界。

本轮按 Round 8 报告的五项 checklist 逐项核验，并分别推演 append/state 四个崩溃点、SIGKILL
重启、oversize × 三类 reload、refresh/revoke/revoke 交错及启动前权限失败。没有新的发布阻断。
下文两条实现注记只是消除局部措辞歧义，不要求 v10，不改变通过结论。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=5f0683dc16376244c40bbc4b850951f7cd6a64ce
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套仓库分支=feat/ios-native-message-timeline
配套仓库提交=389a179a1b01ad2a858f24b5b11ea41058e02381
配套仓库未提交状态=干净
配套关系依据=两仓同名功能分支 + v9 §1.2 明示组合
预期产品特性=安全、可恢复、可审计的多代际内存监测；有界 HTTP wire 读取；保持 reload 对账、文案证据和并发一致性的设备撤销 reducer；方案通过后进入实现
当前部署=/Applications/CordCodeLink.app 内嵌 runtime commit 124b73d7b0f2；历史部署不用于证明待实现方案已生效
```

`f845a3a8...` 只修改 `think.md`，`5f0683dc...` 只修改 followups 文档；v9 的零代码
改动声明成立。配套 iOS 工作树未被读取或修改。`git diff --check
b20b6511...5f0683dc` 通过。

## 2. Round 9 checklist 核验

| # | 准入条件 | 终审核验 |
| --- | --- | --- |
| 1 | JSONL/state durable truth 与 crash recovery | **通过。** JSONL 先 append+flush+fsync，state 后写且仅为可丢弃快照；启动截断 partial tail、按稳定键重放与去重；四个 crash point 均有明确预期。不存在“state 已提交而 journal 未持久化”的合法顺序。 |
| 2 | dirty-run 跨实例 crash 计数 | **通过。** run marker 在主循环前写入；只有 ≥60s 完整 discovery cycle 且完成成功采样或完整 idle 检查才算 healthy；未清 marker 由下一实例记 transient；fatal/clean 先写结果、清 dirty 后 exit 0。SIGKILL、五次升级、重置及正常退出均列入测试。 |
| 3 | oversize 走第 13/14/15 行 | **通过。** `responseTooLarge` 无产品特例；absent 进入第 13 行 lost-response 对账，present/failed 分别进入 14/15；主动 cancel 不得把 reason 覆盖为 `.cancelled`，三类 reload 均有测试。 |
| 4 | revoke/revoke、warning 与 forced reload | **通过。** 第二笔重叠 revoke 在 store 层返回 typed busy，不发请求、不递增 token；第一笔保留强制 reload 和 warning。forced reload 使用私有 bypass；warning 清理、dismiss generation 和 stale busy 清理均已写死并进入交错断言。 |
| 5 | UserDefaults 缺键与不可信数据根 | **通过。** policy 记录 raw/effective/source；缺键映射代码默认值而非错误。不可信根目录不再虚假承诺能写 fatal marker，使用 exit 0 + 最小诊断，并要求 `O_NOFOLLOW` + `fstat`。 |

## 3. 失败路径推演结论

### 3.1 journal 与停止状态

- crash 在 append 前：没有 committed journal record，恢复后 slot 仍可按调度规则处理；不会出现
  state 超前。
- crash 在 JSONL fsync 后、state 前或 state rename 前：恢复重放 journal，补回 state；稳定键
  阻止重复有效事实。
- crash 在 state rename 后：journal 已先持久化，二者一致；state 损坏或丢失仍可重建。
- 告警 sample 本身进入 durable journal，恢复时可重建告警/stop 状态；stop marker 不是唯一证据。

因此 v9 已消除 Round 8 指出的跨文件提交窗口。实现时所有影响可恢复 state 的记录，包括检测到
前一 dirty run 的 transient 事件与 healthy reset，都应遵循同一 journal-first 纪律；这是
“JSONL 为唯一 durable truth”的直接推论，不另开设计项。

### 3.2 launchd 与 crash loop

dirty marker 由活着的新实例解释，覆盖 SIGKILL/解释器崩溃/掉电后重启；healthy milestone
避免“启动一瞬间即清零”，31 秒循环会累计到 fatal。fatal/clean stop 以 exit 0 配合
`KeepAlive={SuccessfulExit:false}`，不会形成预期内停止后的重启风暴。

### 3.3 revoke 状态机

oversize 没有 confirmed response，仍必须执行一次 reload；reload absent 可独立确认撤销，因此
第 13 行成立。并发第二笔 revoke 不改变 token，不能使第一笔结果 stale；普通 refresh 被合并，
但内部 forced reload 不经过公开 `loadDevices()` 门控，因此“一次且仅一次 reload”可实现。

## 4. 非阻断实现注记（不要求 v10）

1. v9 §4.3.6 第 623 行仍写“若 **refresh/revoke** 已在飞行中而新 revoke 开始”，其中
   `revoke` 是残留措辞。该节前一条、决策表和重叠测试已明确规定：**在飞行中的 revoke 遇到
   第二笔 revoke 必须 busy 拒绝，不能递增 token；只有在飞行中的 refresh 会被新 revoke
   递增 token 后判 stale。** 实现和测试以这条主规则为准。
2. restart policy 的 `wrong-type` 虽可标 unavailable，报告最好同时保留运行时代码实际采用的
   fallback effective value（例如 `source=invalid_type_fallback`）；domain 整体不存在等价于两个
   key 均 absent，应使用 `code_default`。这只提高诊断完整度，不改变监测门或安全结论。

## 5. 开发阶段准入与验收边界

文档已通过，可按 v9 §7 一次性实施，不再进行方案版本循环。实施交付必须同时满足：

1. 监测脚本、plist/install lifecycle、journal/dirty-run、parser/security 和全部 crash/双进程/
   fake-clock fixtures 落地；不得用 state snapshot 取代 JSONL truth。
2. raw revoke loader、64KiB 流式上限、五类 outcome、15 行 reducer、七键文案和 operation
   coordinator 落地；oversize、取消、404、重叠 revoke 和所有交错测试通过。
3. 按风险运行定向测试；业务代码变化后执行 Release 构建、覆盖安装和新 runtime 代际核验。
4. launchd 监测安装后单独验证 plist 身份、锁、进程、数据权限和首个有效/idle cycle；不把脚本
   单测冒充生产运行态。
5. owner 人工验收仅保留正常撤销自然路径；异常态由 deterministic tests 验收。长期内存结论仍
   必须等待 v9 §2.3.6 的真实监测完成，不能在部署时提前宣布治理有效或 512MiB 最优。

本轮为 D0 文档终审，未运行 build、unit/UI test、安装或部署。
