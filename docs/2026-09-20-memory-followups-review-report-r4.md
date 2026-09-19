# 2026-09-20 内存治理后续复审报告（Round 4）

复审对象：`docs/2026-09-20-memory-followups.md` v4（提交
`3fed7d7c5f9423834af5b0ce90201364bd8f75ac`）及 think.md 同轮提交
`a1818b2`。

结论：**仍不通过，剩余 3 个方案阻断项。** v4 已解决 Round 3 的提交归属、
management URL 发现方式、单波峰告警入口和 response × reload 状态空间缺口；12 个
组合及 lost-response 对账已经列全。但当前监测规格会把每一个真实 runtime 判成
`bootstrap_mismatch`，趋势阈值的样本数/步数互相矛盾，撤销 reducer 对 confirmed response
与 reload 冲突时也没有一致的权威顺序，因此还不能直接进入实现。

本轮继续保持文档先行；不得据此报告直接开始业务代码或监测脚本实现。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=3fed7d7c5f9423834af5b0ce90201364bd8f75ac
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套仓库分支=feat/ios-native-message-timeline
配套仓库提交=389a179a1b01ad2a858f24b5b11ea41058e02381
配套仓库未提交状态=干净
文档 v4 记录的方案核读时 iOS 来源=6b6cd95a43c85873da8e20e94690b2eaaef2d8b4，干净
配套关系依据=两仓同名功能分支 + v4 §1.1 明示组合
预期产品特性=不混代且可执行的多代际内存监测；可捕获绝对高位、进程 lifetime peak 与趋势异常；完整且确定的撤销结果 reducer；文档通过后才编码
当前部署=/Applications/CordCodeLink.app 内嵌 runtime commit 124b73d7b0f2；本轮不以部署状态证明方案正确性
```

`a1818b2` 只修改 `think.md`，`3fed7d7` 只修改 followups 文档；v4 的“本轮零代码
改动”声明成立。当前 iOS HEAD 晚于 v4 记录的 `6b6cd95a`，但本轮未用新增 iOS 内容形成
结论。

## 2. Round 3 阻断项处置核验

| Round 3 项 | Round 4 结论 |
| --- | --- |
| R3-B1 提交归属误写 | **已处置。** `d3c7404` 与 `c976ef4`、`a1818b2` 与 `3fed7d7` 均按真实的独立提交归属记录。 |
| R3-B2 单波峰与 URL 发现 | **部分处置。** `runtime.json.managementUrl` 已成为 bootstrap，绝对值/current-or-peak/趋势也已拆成独立 OR；但 epoch 表示不一致会使 bootstrap 校验恒失败，趋势步数也仍不确定，见 R4-B1/B2。 |
| R3-B3 完整 reducer | **部分处置。** 4 × 3 矩阵、lost-response reload、typed outcome 和“不读陈旧数组”已补齐；但冲突行的权威规则自相矛盾，见 R4-B3。 |

## 3. 剩余阻断项

### R4-B1 — `runtime.json` UUID epoch 与 status 数值 epoch 被当成同一值直接比较

§2.3 正确记录了 `runtime.json.bridgeEpoch` 是 UUID 字符串，却把它与
`/internal/status.runtimeIdentity.bridgeEpoch` 直接作为同一个 `(pid, bridgeEpoch)` 交叉
核对。生产实现不是同一种 wire representation：

- `runtime_startup.go` 把原始 UUID 字符串写进 `runtime.json`；
- `main.go:managementBridgeEpoch` 对该字符串做 SHA-256，取前 8 字节 big-endian
  `uint64`（结果为 0 时改为 1）；
- `/internal/status` 发布的是这个 `uint64`。

当前真实 `runtime.json` 的 UUID 为 `68fef32a-ef11-4c08-9392-bc1b7e32712e`，按生产算法
得到 status epoch `2011574066258607221`。两者直接比较永不相等，所以 v4 五步事务会把
所有样本拒绝为 `bootstrap_mismatch`。

下一版必须写死一种可测试的转换/核对契约。推荐：脚本实现与
`managementBridgeEpoch` 完全相同的转换，将 runtime.json UUID 规范化成 `uint64` 后再与 A
比较；测试至少覆盖普通 UUID、产生非零值的固定 fixture、以及算法的零值保护。另一种可行
方案是 bootstrap 只核对 PID，并明确 UUID 不参与跨端点相等判断，但这会降低 epoch 防混代
强度，不应在文档中无声采用。事务末还应重读一次 `runtime.json`，核对 URL/PID/原始 UUID
未变，否则 A/B 虽恰好命中同一 endpoint，仍可能跨过 bootstrap 文件换代窗口。

### R4-B2 — 趋势触发器的“样本数、步数、时间跨度”不一致

§2.3.3 表格写“连续 ≥3 个有效样本、每步增量 ≥8MiB”，按 t=0/30/60 只有 **2 个增量**，
即最低 `16MiB/60min`；紧随其后的依据却写“≥3 步（≥24MiB/60min）”。若要求 3 个增量，
必须是 4 个样本，且按既定采样间隔跨度为 90 分钟。脚本和测试无法同时实现这两种定义。

下一版应择一并固定精确公式，例如：

- 3 个样本、2 个相邻 delta 均 ≥8MiB、总增量 ≥16MiB/60min；或
- 4 个样本、3 个相邻 delta 均 ≥8MiB、总增量 ≥24MiB/90min。

同时，“正常 GC gauge 波动为个位数 MiB”目前没有本文档承认缺失的生产分布数据支撑，不能
作为 8MiB 的事实依据。可把 8MiB 明确降级为与 256MiB/300MB 同等级的 provisional 运维
启发式，并说明只影响是否升级取证，不把它描述成已测得的正常波动边界。脚本测试要增加
阈值等于/差 1 byte、非严格上升、步数不足三组边界 fixture。

### R4-B3 — reducer 同时宣称 confirmed response 可确认撤销，又让 `present` 否定投递阻断

§4.3.1 的总规则是：`revoked:true` **或** reload absent 即确认已撤销。按该规则，行 2/5
即使 reload present，也至少有服务端 confirmed response。可 §4.3.3 又把两行判为“不一致”，
并写死“reload present 不能声称投递已阻断”；行 5 尤其同时掌握
`revoked:true + pushCleanupError`，与行 4/6 使用的是同一份投递阻断证据，却得出相反文案。
这不是呈现差异，而是同一证据在 reducer 中有两套优先级。

下一版必须先固定冲突时的权威规则，再从规则机械生成矩阵。可接受的保守规则是：

1. confirmed response 与 reload absent 都能单独确认撤销；
2. confirmed response × present 标记“已确认撤销，但列表不一致”，安全语义仍来自
   confirmed response，列表只触发一致性告警；或
3. 若产品决定任何 present 都压过 response，则必须修改 §4.3.1 的 OR 规则，明确冲突时降级为
   unknown，且行 3/6 的 reload failed 也需解释为何仍信 response。

当前服务端 `handleRevokeDevice` 只有在 `DeviceStore.RevokeDevice` 成功后才返回
`revoked:true`，随后才可能附加 `pushCleanupError`；`ListDevices` 又从同一 store 排除 revoked
记录。因此 confirmed × present 是协议/存储不一致告警，不应靠一条笼统的“present 永远不能
声称阻断”掩盖证据优先级问题。

typed reload seam 也应归属 `DeviceStore`（内部调用现有 `DeviceAPIProviding.listDevices()`）
或直接使用后者的 typed throws 返回，而不是给 API 协议再增加一个语义重复的
`reloadDevicesForResult()` RPC。此项可随 R4-B3 一并澄清，不单独阻断。

## 4. 独立核验

- `git show --stat` 证明 `a1818b2`/`3fed7d7` 以及历史 `d3c7404`/`c976ef4` 的提交归属均与
  v4 所写一致。
- `runtime_startup.go` 的 `RuntimeReadyFrame.BridgeEpoch` 为 string；
  `main.go:managementBridgeEpoch` 明确执行 SHA-256 → big-endian uint64；两端 wire 值不能直接
  相等比较。
- `handleRevokeDevice` 先持久 revoke，失败则返回 404；只有成功后才返回
  `revoked:true`，push cleanup 失败仅作为附加字段。`handleListDevices` 使用同一
  `DeviceStore.ListDevices()`。
- 当前 `DeviceAPIProviding` 已有 `listDevices() async throws -> [TrustedDevice]`；typed reload
  outcome 可以在 `DeviceStore` 层包装，不需要制造第二个网络方法。
- `git diff --check` 通过。本轮为 D0 文档复审，没有运行 Go/Swift/UI/snapshot/device 测试，
  没有构建、安装或部署。

## 5. 下一轮准入条件

1. 明确 runtime.json UUID epoch → status uint64 epoch 的转换算法及测试，或书面选择较弱的
   PID-only bootstrap；事务末重新核对 bootstrap 文件未换代。
2. 统一趋势触发器的样本数、delta 数和时间跨度；把 8MiB 降级为无生产分布支撑的
   provisional 启发式并补边界测试。
3. 给 confirmed response × reload present/failed 固定唯一证据优先级，再据此修正 12 行矩阵、
   三条最低规则和三类文案。
4. typed reload outcome 保持在状态管理层，复用现有 `listDevices()`；不得给同一 GET 语义制造
   两个 API 方法，除非文档给出不同网络契约。
5. 下一轮仍只修文档；文档通过后再一次实施监测脚本与 B4 代码、定向测试、Release 覆盖安装
   和新代际核验。
