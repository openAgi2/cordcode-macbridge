# 2026-09-20 内存治理后续复审报告（Round 2）

复审对象：`docs/2026-09-20-memory-followups.md` v2（提交 `2fb00b8128fa268d79701e7ebca1d80df1776e45`）及 think.md 补记提交 `106bb95468e02c0b54b56b745846f4d16c494f3d`。

结论：**仍不通过，剩余 3 个方案阻断项。** Round 1 的 B1、B2 已完整处置：v2 的来源清单可复核，`124b73d` 文件范围已更正，首采四项越界断言均已撤回，单点数据与未知字段也已诚实分级。多代际路线和 B4 的总体方向可接受，但当前规格仍不足以产生可判定、不会混代的监测结论，且 malformed 200 响应仍可能被实现成“清理成功”。

本轮确认为 docs-only；不要求在本轮提前修改业务代码、构建或部署。

## 1. 本轮来源清单

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=2fb00b8128fa268d79701e7ebca1d80df1776e45
未提交状态=干净（写本报告前）
任务预期分支=feat/ios-native-message-timeline
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-native-message-timeline / feat/ios-native-message-timeline / 80b4b04c8dc002217b924c16bb9a63261de4a2a7
配套仓库未提交状态=已修改 OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift、OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift、OpenCodeiOS/OpenCodeiOSTests/NativeTimelineKeyboardContainerTests.swift（其他任务所有，本轮未读取其业务内容、未修改、未提交）
预期产品特性=首采只保留可证明的单点观测；可判定且不混代的 120 分钟代际监测；B4 过审后一次实施并验证
当前部署=/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime，version commit 124b73d7b0f2，GUI/runtime PID 30081/34681，8777 由该 runtime 监听
```

`git diff 8f77506..2fb00b8` 确认本轮只有 `think.md` 与 followups 文档变化，没有业务代码变化。v2 记录的 iOS `80b4b04c` 和 3 个 dirty paths 与复审门点一致。

## 2. Round 1 阻断项处置核验

| Round 1 项 | Round 2 结论 |
| --- | --- |
| B1 来源清单与文件范围 | **已处置。** 三门点、Mac/iOS 身份、iOS dirty paths 和 docs-only 边界已展开；`124b73d` 的 9 个文件与独立 think.md 提交已分开归属。 |
| B2 单点外推 | **已处置。** `HeapReleased` 只保留 gauge 语义；分配率、单次 GC 代价、scavenger active、512MiB 无需下调四项均明确撤回。无法回补已消亡 PID 的时间序列是合理边界。 |
| B3 忽略 120 分钟自动重启 | **方向已处置，规格未闭环。** 选择多代际而非关闭生产自动重启合理，也明确不回答 27h 同进程累积；但采样一致性、完成条件、负载覆盖与阈值语义仍缺，见 R2-B1/R2-B2。 |
| B4 文案/wire/presentation | **方向已处置，响应契约仍有洞。** 删除“重试撤销”、增加 unknown 状态和 client-level fixture 是正确方向；但 optional `revoked` 与人工验收形状尚未收口，见 R2-B3。 |

## 3. 剩余阻断项

### R2-B1 — 监测没有单样本事务边界，也没有完成条件

§2.3 定义了 `(PID, epoch)` 代际，却没有规定一次样本如何保证 memory、vmmap、ps 和身份来自同一代际。生产可用信息实际分散在两个端点：`GET /internal/status` 提供 `runtimeIdentity.pid/bridgeEpoch`，`GET /internal/diagnostics/runtime` 提供 `startedAt`、CPU/后台任务计数与 memory；采样过程中恰逢 120 分钟重启时，前半段可能来自旧 PID、后半段来自新 PID。仅在 JSONL 中写一个 PID/epoch 不能防止混样本。

脚本规格需要固定为一个可测试的采样事务：

1. 读取 status identity A，并取得与该代际对应的 management URL；
2. 拉 diagnostics，执行 `vmmap A.pid` 与 `ps A.pid`；
3. 再读 status identity B；
4. 只有 A == B、所有命令成功且 diagnostics `startedAt` 与已记录代际一致时才提交一整条 JSONL；否则写一条带原因的 rejected sample，绝不能把部分字段并成有效样本。

还缺监测任务的完成条件。文档没有规定至少监测多少墙钟时间、多少个有效代际、每代际必须是几个样本；因此“未触发 → 上界成立”可能在单个 60 分钟代际后就被宣告，也可能无限运行。尤其表中写“≥60 分钟（≥2 个样本）”，而 `连续 ≥3 样本` 的趋势条件至少需要 3 个有效点；是否在 t=0 采样没有明确写出。

下一版必须预先固定：t=0/30/60/90 的样本时刻、每代际最少 3 个成功样本、整项监测的最小有效代际数与墙钟窗口，以及掉样/重启后如何补足。没有这些条件，脚本即使实现也无法判断何时可以回填 §2.4。

### R2-B2 — load average 不能证明“真实负载覆盖”，提案阈值也不能冒充上界裁决

§2.3 把 `sysctl vm.loadavg` 当作负载可比性字段。它是整机 runnable/load 状态，无法区分 CordCode runtime 是否执行过 transcript 扫描、agent poll、WebPush watcher 或真实 session 活动；一台被其他进程压满而 CordCode 空闲的 Mac 也会显示高 load。当前 diagnostics 已提供 `startedAt`、process user/system CPU、`backgroundTasks`、`activeBackgroundScans`、`agentBackgroundScans:*` 等可归因计数，方案却没有归档它们。

同样，`256MiB` 与 `300MB` 目前没有来源或风险解释，并标为“可调”。它们可以作为 provisional **告警触发器**，但不能在未触发时推出“120 分钟代际内上界成立”；真正可成立的只有“在预先固定的观测窗口和已记录负载覆盖下，观测最大值为 X”。否则阈值本身就是结论，形成循环论证。

下一版应：

- 每个样本归档完整 diagnostics 中与负载相关的累计/当前计数和 process CPU，跨样本计算同代际 delta；load average 只作系统背景，不作 CordCode 负载替代；
- 定义至少一种有效负载覆盖门（例如有效代际内 background scan/session activity/CPU delta 达到明确条件），未覆盖则结论只能写“空闲/低负载观测”；
- 把 256MiB/300MB 明确降级为带理由的早期告警线，最终报告展示 observed max、趋势和覆盖度；不得用“未越告警线”表述为已经证明配置合理或治理根因成立；
- 明确这套不含 GC pause/alloc counter 的监测不能裁决 GOGC 或 512MiB 最优值，只能决定是否需要升级取证。

### R2-B3 — malformed 200 的结构有效性和人工验收仍未定义正确

§4.3 计划把 JSON decode 失败映射为 `cleanupStatusUnknown=true`，但当前 `RevokeDeviceResponse` 的 `revoked` 与 `pushCleanupError` 都是 optional。若实现只把 `JSONDecoder.decode` 成功当“结构有效”，合法 JSON `{}` 会成功解码并落入 `pushCleanupError=nil / unknown=false`，恰好再次把未知当成清理成功。`{"revoked":false}` 也必须有明确语义，不能显示“设备已撤销、投递已阻断”。

方案必须将成功 envelope 的结构不变量写死：至少要求 `revoked == true`；缺键、false、类型错误和非 JSON 都进入明确的 protocol/status-unknown 路径，并分别有 fixture。若产品仍以 HTTP 2xx 认定撤销动作已执行，文案必须说明“响应状态无法确认”而不是无条件声称设备已撤销；更稳妥的是结合随后 `ListDevices` 的权威 reload 结果决定“已撤销”还是“状态未知”。

unknown 文案中的“runtime retries ... automatically”也应改为条件句：若 subscription 仍在，后续 fan-out 才会重试；清理其实已成功但响应不可读时并不存在待重试项。

最后，§7.3 要求 owner “实际撤销一台设备观察警告弹窗（含 unknown 态）”，但正常生产响应不会制造 cleanup failure 或 malformed 200，ordinary revoke 反而不应弹警告。下一版需把验收分开：正常真实撤销只验收无警告与设备消失；failure/unknown 两态由 deterministic client/ViewModel tests 验收。除非另有安全且明确授权的故障注入路径，不得把不可自然到达的 unknown UI 状态交给 owner 人工制造。

## 4. 独立核验

- `106bb95` 只修改 `think.md`，首采记录已实际落盘；`2fb00b8` 只修改 followups 文档。
- `git diff --check`：通过。
- 当前 `/internal/status` 实测返回 `runtimeIdentity={pid:34681, bridgeEpoch:2011574066258607221}`；`/internal/diagnostics/runtime` 返回 `startedAt`、process CPU、background counters 与 memory，但不重复返回 runtime identity。该真实 shape 支持 R2-B1 的双端点采样事务要求。
- 当前部署 runtime 仍为 `124b73d7b0f2`，PID 34681 监听 8777；本轮没有新构建、安装或部署。
- 本轮为 D0 文档复审，没有重复运行 Go/Swift 测试，也没有运行 UI/snapshot/device automation；Round 1 已验证的代码未变化。

## 5. 下一轮准入条件

1. 把多代际脚本写成 identity A → diagnostics/vmmap/ps → identity B 的原子样本协议，混代或部分失败只记 rejected sample。
2. 固定 t=0/30/60/90、有效样本数、最小有效代际数、总墙钟窗口、掉样补足和最终停止条件。
3. 用 runtime 自身 diagnostics/CPU delta 定义负载覆盖；将 256MiB/300MB 降级为 provisional 告警线，最终只报告观测上界与覆盖度。
4. B4 要求 `revoked == true` 才是结构有效成功；补 `{}`、`revoked:false`、类型错误、非 JSON、带/不带 cleanup error 的 client fixtures，并定义 reload 后状态。
5. unknown 文案使用条件性自愈描述；正常 revoke 的 owner 验收与 failure/unknown 的自动化验收分离。
6. 下一轮仍先只修文档。文档通过后再按 §4.3 一次完成代码、定向测试、Release 覆盖安装和新代际核验。
