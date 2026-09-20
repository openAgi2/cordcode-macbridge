# 2026-09-20 内存治理后续复审报告（Round 5）

复审对象：`docs/2026-09-20-memory-followups.md` v5（提交
`a50a5f9a766c9515614b523e6b88b8339ee79159`）及 think.md 同轮提交
`a48d013775bf056990733e11eef0d91b63231454`。

结论：**仍不通过，剩余 3 个方案阻断项。** Round 4 的三项技术裁决均已按所选方案
正确落地：epoch 转换与事务末 bootstrap 重读可防混代；趋势公式已统一为 4 样本、3 个
delta、≥24MiB/90min；confirmed response 的证据优先级与 12 行矩阵现在一致。剩余问题是
实施入口尚不闭合：监测脚本缺少 Management API 鉴权步骤，撤销 API 没有能承载四类
response 的 typed 契约，且 reload 已确认撤销的两行仍展示“撤销无法确认”的相反文案。
来源清单还使用了短哈希，不满足 P0 的完整提交要求。

本轮继续保持文档先行；不得据此报告直接开始业务代码或监测脚本实现。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=a50a5f9a766c9515614b523e6b88b8339ee79159
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套仓库分支=feat/ios-native-message-timeline
配套仓库提交=389a179a1b01ad2a858f24b5b11ea41058e02381
配套仓库未提交状态=干净
配套关系依据=两仓同名功能分支 + v5 §1.2 明示组合
预期产品特性=带鉴权且不混代的多代际内存监测；精确的 4 样本趋势触发器；能在 API 边界表达完整 response class 并与 reload 对账的撤销 reducer；文档通过后才编码
当前部署=/Applications/CordCodeLink.app 内嵌 runtime commit 124b73d7b0f2；本轮不以部署状态证明方案正确性
```

`a48d013` 只修改 `think.md`，`a50a5f9` 只修改 followups 文档；v5 的“本轮零代码
改动”声明成立。本轮未读取配套 iOS 业务源码。

## 2. Round 4 阻断项处置核验

| Round 4 项 | Round 5 结论 |
| --- | --- |
| R4-B1 epoch 两种表示 | **已处置。** UUID 字符串 → SHA-256 前 8 字节 big-endian uint64 → 0 改 1 与生产实现一致；活体 fixture、固定 fixture、零值桩和事务末 bootstrap 重读均已列入。 |
| R4-B2 趋势公式 | **已处置。** 4 个 t=0/30/60/90 样本形成 3 个相邻 delta，每步 ≥8MiB，总增量 ≥24MiB/90min；8MiB 已诚实降级为 provisional 启发式，边界 fixture 完整。 |
| R4-B3 证据优先级 | **核心裁决已处置。** confirmed response 与 absent 各自可确认撤销，confirmed × present 仅追加一致性告警，行 4/5/6 使用同一投递阻断证据；typed reload 也已回到 DeviceStore 层。API 返回契约与 presentation 仍有缺口，见 R5-B2。 |

## 3. 剩余阻断项

### R5-B1 — 监测脚本没有读取 Management token，生产端点会直接返回 401

§2.3.1 的 bootstrap 只读取 `runtime.json`，随后直接 GET `/internal/status` 和
`/internal/diagnostics/runtime`。生产 Management API 要求
`Authorization: Bearer <management-token>`；token 位于同一 data dir 的
`management-token` 文件，不在 `runtime.json` 中。独立实测不带 header 的 status 请求返回
401，故按 v5 规格实现的脚本无法产生任何有效样本。

下一版应把鉴权纳入原子采样契约，而不是留给实现者猜测：

1. bootstrap 同时读取 `runtime.json` 与 `management-token`；token 文件缺失、空值或不可读
   均写 rejected sample（独立原因码，如 `management_token_unavailable`）；
2. A、diagnostics、B 三个 HTTP 请求都使用 Bearer header；401/403 与网络/解析失败分开记录，
   便于区分身份换代和普通命令失败；
3. token 绝不写入 JSONL、日志、命令回显或 fixture；测试只用确定性假 token；归档文件权限
   不得放宽 token 文件权限；
4. 脚本测试至少覆盖正确 token、缺 token、空 token、401/403，以及事务中 token/runtime
   bootstrap 被替换。若选择不把 token 纳入末次重读，需说明依靠 A/B 与 HTTP 失败如何拒绝
   混代，不得静默拼样本。

仓库活文档 `BUILD_INSTALL_AND_RUNTIME.md` 已给出正确生产读取方式，可直接作为实现锚点。

### R5-B2 — 四类 response 尚无 API 边界类型，且 rows 7/10 的文案否定了 reload 对账结果

v5 的矩阵使用 `confirmedClean` / `confirmedCleanupFailure` / `protocolUnknown` /
`transportOrHTTPFailure`，但实施清单没有定义这些状态如何跨过
`DeviceAPIProviding.revokeDevice`。当前协议仍是：

```swift
func revokeDevice(_ deviceId: String) async throws -> DeviceRevocation
```

其中 `DeviceRevocation` 只能表达可选 `pushCleanupError`；`performRequest` 对非 2xx 只抛
`ManagementError.httpError(Int)`，现有 `try?` 解码又正是本方案要删除的状态丢失点。仅写
“解码失败 → protocolUnknown”不能告诉 DeviceStore 如何接收该状态、如何区分 404 特例，
也无法让 stub 对 12 行矩阵提供稳定输入。

下一版必须定义一个 typed revoke-attempt 契约（名称可变），至少明确：

- confirmed clean；
- confirmed cleanup failure(error)；
- protocol unknown（保留可诊断原因但不泄漏响应正文）；
- transport/HTTP failure（至少保留 HTTP status；若特例坚持写 `404 not_found`，还需决定是否
  解码并保留服务端 error code，而不是把所有 404 等同）；
- 无论上述哪一类，DeviceStore 都进入 typed reload；不能因 throw 提前跳过 reload。

该契约可以通过修改 `DeviceAPIProviding.revokeDevice` 返回类型，或定义协议可见的 typed
error/outcome 实现；但必须在文档中择一，wire fixtures 与 ViewModel stub 才有同一测试接口。

此外，矩阵行 7/10 已由 reload absent 确认“设备不再授权”，却共用
`devices_push_cleanup_unknown` 的首句 “The revocation outcome could not be confirmed”。这与
§4.3.1 的 OR 规则直接相反。至少拆成两类 presentation：

- rows 7/10：撤销/不再授权**已由列表确认**，仅 push cleanup 结果未知；
- rows 8/9：撤销结果与 cleanup 结果均未知（row 8 可注明设备仍在列表）。

测试应断言这两类文案不能互换，而不只断言 alert 被发布。

### R5-B3 — v5 来源清单仍使用配套仓短哈希

§1.2 写配套 iOS 提交为 `389a179a`。P0 来源门要求路径、分支、**完整提交哈希**和未提交
状态不可拆分；短哈希不满足该规则。实际完整值为：

```text
389a179a1b01ad2a858f24b5b11ea41058e02381
```

下一版应写入完整值，并把 v5 文档提交 `a50a5f9a766c9515614b523e6b88b8339ee79159`
作为历史来源事实记录。新版本自身不要求在内容中构造 Git 自引用哈希，但送审说明必须像本轮
一样给出最终提交。§5.3 重复的两行 “Swift 定向测试 | 21 条全绿”一并删除；这是机械文档
错误，不另立阻断项。

## 4. 独立核验

- `git show --stat` 证明 `a48d013` 仅修改 think.md、`a50a5f9` 仅修改 followups 文档。
- `main.go:managementBridgeEpoch` 与 v5 派生公式逐项一致；活体 UUID 的数值 fixture 与
  Round 4 实测一致。
- `BUILD_INSTALL_AND_RUNTIME.md` 和 `RuntimeManager` 均从 `management-token` 文件读取 bearer
  token；不带 header 的生产 status 请求返回 401。
- 当前 `DeviceAPIProviding.revokeDevice` 只能返回 `DeviceRevocation` 或 throw；没有
  `protocolUnknown` outcome。当前 `ManagementError.httpError` 只保存 HTTP status。
- v5 行 7/10 的生效判定均为“已撤销”，但绑定的 unknown 文案首句仍为“撤销结果无法确认”。
- `git diff --check` 通过。本轮为 D0 文档复审，没有运行 Go/Swift/UI/snapshot/device 测试，
  没有构建、安装或部署。

## 5. 下一轮准入条件

1. 将 `management-token` 的安全读取、Bearer header、鉴权失败原因码和对应 fixture 纳入六步
   事务（必要时调整为七步或将 token 纳入 bootstrap 子事务）。
2. 定义协议可见的 typed revoke-attempt outcome/error，明确四类 response 与 404 特例如何跨过
   API → DeviceStore 边界；任何结果都必须继续 reload。
3. 将 rows 7/10 的“撤销已确认、cleanup 未知”与 rows 8/9 的“撤销未知”拆成不同文案和测试。
4. 配套 iOS 来源改用完整提交哈希，记录 v5 的完整文档提交，并删除 §5.3 重复行。
5. 下一轮仍只修文档；文档通过后再一次实施监测脚本与 B4 代码、定向测试、Release 覆盖安装
   和新代际核验。
