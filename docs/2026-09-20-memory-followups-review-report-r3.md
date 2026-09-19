# 2026-09-20 内存治理后续复审报告（Round 3）

复审对象：`docs/2026-09-20-memory-followups.md` v3（提交 `c976ef41ff9c0e88aaaf42e15f3fa02271812c41`）及 think.md 同轮提交 `d3c7404`。

结论：**仍不通过，剩余 3 个方案阻断项。** v3 已实质解决 Round 2 对完成条件、负载覆盖、告警语义降级、`revoked == true` 和人工验收分离的要求；7 天 + 20 个有效代际 + 8 个有载代际可以作为这次描述性监测的停止条件。但来源提交归属仍有一处事实错误，告警条件会漏掉原事故的单波峰形状，B4 决策表也尚未覆盖 response/reload 的完整状态空间。

本轮继续保持文档先行；不得据此报告直接开始业务代码实现。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=c976ef41ff9c0e88aaaf42e15f3fa02271812c41
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
复审开始时配套仓库=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 80b4b04c8dc002217b924c16bb9a63261de4a2a7
复审开始时配套仓库未提交状态=ChatUIKitContainerView.swift、NativeTimelineViewController.swift、NativeTimelineKeyboardContainerTests.swift 已修改（其他任务所有；本轮未读取、未修改）
写报告前配套仓库=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 2884f3604bb8cf6e8e69ca02edfce977fc6d09a9，干净
配套仓变化说明=其他任务于本轮评审期间提交上述 3 个 iOS 文件；本评审没有使用其内容形成结论
预期产品特性=不混代、可停止、能捕获单波峰的 120 分钟代际监测；完整 response × reload 撤销结果模型；文档通过后才编码
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit 124b73d7b0f2，GUI/runtime PID 30081/34681，8777 listener 正常
```

`git diff 98af562..c976ef4` 仅包含 followups 文档和 think.md；零业务代码改动声明成立。

## 2. Round 2 阻断项处置核验

| Round 2 项 | Round 3 结论 |
| --- | --- |
| R2-B1 原子采样与完成条件 | **大部分处置。** A→数据→B、rejected sample、t=0/30/60/90、掉样不补、7 天/20 有效代际/8 有载代际均已明确。management URL bootstrap 仍有歧义，且告警捕获形状有缺口，见 R3-B2。 |
| R2-B2 负载与阈值语义 | **已处置。** runtime 自身累计计数/CPU delta 取代 load average；空闲和有载分层；阈值降级为 provisional 告警，最终报告只写 observed max/分布/覆盖度，不裁决 GOGC 或 512MiB 最优值。 |
| R2-B3 response 结构与验收 | **部分处置。** `revoked == true`、缺键/false/type/non-JSON、条件性文案和自然路径/自动化验收分离均正确；但决策表不是完整乘积，见 R3-B3。 |

## 3. 剩余阻断项

### R3-B1 — v3 来源清单把独立 think.md 提交误写为“随本提交”

§1.2 写 `think.md 指针更新=随本提交`，但实际历史为两个独立提交：`d3c7404` 只改 think.md，随后 `c976ef4` 只改 followups 文档。用户交付说明已经正确列出两个提交，正文来源清单反而把它们合并归属。

下一版应明确：

```text
think.md 同轮提交=d3c7404...（只改 think.md）
本文档提交=c976ef4...（只改 docs/2026-09-20-memory-followups.md）
```

这是来源事实修正，不要求重写两次提交或改历史。

### R3-B2 — 告警条件漏掉单次分配波；management URL 发现方式不够确定

§2.3.3 对两条告警都要求“≥3 个连续样本单调上升且代际末值越线”。这会漏掉本次治理最关心的形状：一次大分配波在两个采样点之间发生，memory/footprint 突然跃升后高位持平，或者当前量随后下降但 `vmmap Physical footprint (peak)` 已超过阈值。该形状既不满足连续三点上升，也未必在代际末仍越线，却正是 continuity cold scan 等最大单波曾造成的问题。

告警应拆成互不替代的 OR 条件：

1. **绝对越线**：任一有效样本的 `sysMinusHeapReleased > 256MiB`；
2. **footprint 波峰**：任一有效样本的 current footprint **或进程 lifetime peak** `>300MB`；
3. **趋势异常**：连续 ≥3 有效样本上升作为额外触发器，并给出独立的最小增量/斜率门，不能要求它与绝对越线同时成立。

这些仍只是 provisional 升级触发器，不改变 §2.4 的描述性结论边界。

另外，§2.3.1 用 `lsof` 从 runtime 的监听端口中“定位 management 端口”。真实进程同时监听 `127.0.0.1:61945` 和 `*:8777`；未来还可能有 8778，靠“监听端口”描述无法唯一选择。仓库已有权威 bootstrap：`runtime.json` 明确给出 `managementUrl`、PID 和 UUID bridge epoch。脚本应先读 `runtime.json` 获取 URL，再以 `/internal/status` 的 runtimeIdentity 作为采样代际身份，并在事务末重新读取/核对；不得把 lsof 端口猜测写成正式算法。`lsof` 可保留为诊断或 PID/listener 交叉检查。

脚本测试至少应覆盖：正常样本、A/B 改代、startedAt 改变、任一命令失败、单点绝对越线、peak 越线和趋势触发。

### R3-B3 — “7 行完整决策表”遗漏多个生产组合，并放弃了 lost-response 对账

§4.3.2 的表并不是 response × reload 的完整状态空间，至少遗漏：

- `revoked:true`、无 cleanupError × reload 失败；
- `revoked:true + cleanupError` × 设备仍在；
- `revoked:true + cleanupError` × reload 失败；
- 非 2xx/网络错误，但服务端已在响应丢失前完成 revoke × reload 后设备消失。

最后一种不是理论形状：HTTP 请求有副作用后连接丢失是标准 lost-response 情况，仓内 pairing client 已有同类 reconcile 测试。当前表第 7 行直接走 `devicesError` 且不 reload，与本节“ListDevices reload 为权威”的裁决互相矛盾。

下一版应先定义正交状态，而不是继续手列不完整组合：

- response class：`confirmedClean` / `confirmedCleanupFailure(error)` / `protocolUnknown` / `transportOrHTTPFailure`；
- reload class：`absent` / `present` / `failed`；
- presentation result：撤销已确认、未生效、状态未知，以及独立的 cleanup known-failed/unknown 信息。

然后列出完整矩阵或明确的优先级 reducer，并让测试逐组合覆盖。至少遵守：reload absent 可以 reconcile lost response；reload present 不能声称投递已阻断；reload failed 时不能从旧 `devices` 数组推断结果。

现有 `loadDevices()` 只返回 `Void`，失败时保留旧 `devices` 并设置错误。实施方案必须明确新增一个返回 `Result<[TrustedDevice], Error>`（或等价 typed outcome）的 reload seam，再原子更新 ViewModel；不能在 `await loadDevices()` 后读取可能陈旧的数组来执行决策表。

§4.3.4 中“unknown 仍保持撤销成功语义”的表述也应删除：`revoked:false`、invalid body 或 transport loss 在 reload 前都只能是状态未知。只有 `revoked:true` 或 reload absent 才能确认撤销生效。

## 4. 独立核验

- `d3c7404` 只修改 think.md；`c976ef4` 只修改 followups 文档；`git diff --check` 通过。
- 当前 runtime 的 `lsof` 同时显示 `127.0.0.1:61945` management listener 与 `*:8777` bridge listener；`runtime.json` 明确给出 `managementUrl=http://127.0.0.1:61945`、PID 34681 与 UUID epoch，证明正式脚本无需猜端口。
- `handleRevokeDevice` 的 200 body 确实恒含 `revoked:true`，可选 `pushCleanupError`；v3 采用 `revoked == true` 作为 confirmed response 有源码依据。
- 当前 `DeviceStore.loadDevices()` 返回 Void，reload 失败只设置 `hasLoadedDevices=false/devicesError`，不会清空旧数组；该形状不能直接支撑 v3 决策表。
- 本轮为 D0 文档复审，没有运行 Go/Swift/UI/snapshot/device 测试，没有构建或部署；已部署业务代码未变化。

## 5. 下一轮准入条件

1. 更正 `d3c7404` 与 `c976ef4` 的独立提交归属。
2. 告警改为绝对 current、lifetime peak、趋势三类 OR 触发；趋势线给出独立增量/斜率语义。
3. management URL 以 `runtime.json` bootstrap，status identity 做事务前后校验；lsof 仅作诊断交叉检查。
4. B4 改用 response class × typed reload outcome 的完整 reducer，覆盖 lost response；不得从 reload 失败后的旧数组推断撤销状态。
5. 删除 unknown=撤销成功的预设；只有 confirmed response 或 reload absent 才确认生效。
6. 下一轮仍只修文档；文档通过后再一次实施脚本与 B4 代码、定向测试、Release 覆盖安装和新代际核验。
