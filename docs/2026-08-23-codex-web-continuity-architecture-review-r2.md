# Codex Web 连续性架构评审 r2：修订版合同核验

- 日期：2026-08-23
- 状态：**第二轮评审报告。只评不改。权威合同已于同日按 §6 动作表修订，采纳与否见合同 §11；本文保持核验原文，不回写为第二合同。**
- 评审对象：[2026-08-22-codex-web-continuity-architecture.md](2026-08-22-codex-web-continuity-architecture.md) 2026-08-23 修订版（commit `cc61709`），含 §10 评审处置
- 前置评审：[2026-08-23-codex-web-continuity-architecture-review.md](2026-08-23-codex-web-continuity-architecture-review.md)（一轮，双仓源码核验 + CPU 附录 A）
- 核验基线：MacBridge 本仓 commit `2cdf322` 工作树 + iOS 镜像仓 `../cordcode-ios-codex-web-backend/`；代码行号以当时工作树为准
- 置信约定：沿用一轮——「代码直证」为源码可复核事实；「归因候选」为待 §7 矩阵坐实的机制

> [!IMPORTANT]
> 总评：修订忠实度高，R1–R9 全部按 §10 处置落实，「不采纳」七项裁决均有依据且与一轮评审原文自洽；
> iOS 镜像已同步（仅 4 处预期内本地化差异，并声明「本仓不另维护评审原文」），一轮评审原文未被改写
> （仅 `2cdf322` 加了指向修订版的说明）。合同整体已可开工，但本轮发现一个会让 L2「可验证声明」
> 修成假验证的实现级缺口（§2 F1），建议开工前在 §6.2 补一句。

## 0. 结论一览

| 评审维度 | 结论 |
|---|---|
| §10 处置表 vs 一轮 R1–R9 | ✅ 全部落实，R5 部分采纳的裁决优于一轮字面建议（删机制、留现象） |
| 「不采纳」七项 | ✅ 全部认可，理由与一轮置信约定一致 |
| iOS 镜像同步 | ✅ 4 处预期本地化差异，内容同步 |
| 一轮评审原文完整性 | ✅ 未被改写（`2cdf322` 仅加指向说明） |
| 新引入文本的代码事实 | ✅ 抽核无误（丢失点、CPU 数字、结构事实） |
| §6.2.1「等到 scope RPC 成功」的可验证性 | ❌ 按字面实现是假验证（§2 F1，代码直证） |
| §6.3.1 选项 b「权威 pull」前提 | ⚠️ 未写明数据源前提，可能假绿（§3 F2） |

## 1. 落实核对（逐条通过）

- **R1**：§4.4 主断点换为 durable/live 路由不对称；256 满丢移入 §3「当时容易误判」列、全文降为写路径隐患；「归因候选（机制代码直证）」标注贯穿 §3/§4.4/§10。
- **R2**：§4.4 收齐四个丢失点（offlineQueue nil 跳过、K4Patch 溢出不重发、sink 溢出断连、iOS `handler=nil` 与 2048 入站缓冲清空），并声明「不要求一次 PR 全修，但定向测试不得假装没看见」。
- **R3/R4**：§6.3 拆「旁观（主）/写路径（辅）」且声明辅测试通过 ≠ 旁观 L3 完成；§7 #2 补「逐字/逐段流式」断言，#7 新增，判层正确（#3/#4/#7 → L3）。
- **R5（部分采纳）**：删「没有重连声明链」机制表述（§4.3 断裂点 4 改写为四条 iOS 缺口 + 新进程竞态归因候选），保留 §3 现象行「切 session 才好」并标注为用户绕过。此裁决优于一轮字面建议：删掉现象会让后续 agent 以为真机没发生过。
- **R6/R7/R8/R9**：§6.2 改为可验证声明 + 循环解析当前 client；三条结构事实进 §4.3 且明确「不是立刻要建的第二套观察」；`set_observation_scope` 标注单一充分入口 / 已建待验并警告不得读成从零建造；stall watchdog 与 milestone「假在听」均已写入。
- **CPU**：§6.4 收采样结论、独立施工、座位不调间隔（§4.1 加实测注记），与一轮附录 A 一致。
- 新增 §5.9/§5.10 禁令、§8 取证顺序（先看 `routeRelayOfflineStampedEvent` 再考虑扩 256）均为正确的施工面收口。
- 「不采纳」七项全部认可，两条关键裁决尤其正确：durable 不对称保持归因候选身份；「L2 完成 = 给 codex-web 加 relay watcher」拒绝（会变成第二观察源，违反单一充分入口）。

## 2. F1（最重要，阻碍 §6.2.1 按字面落地）：「scope RPC 成功」≠「attach 生效」，现状 RPC 永远返回 Ok

代码直证：

1. `handleSetObservationScope` 对 `AttachLiveThread` 是 `go` **异步发射**（`go-bridge/handlers.go:1142`），随后无条件 `conn.SendResult(..., &ResultResponse{Ok: true}, nil)`（`:1164`）——RPC 响应不携带 attach/subscribe 结果。
2. `observeThread` 的三种失败——transport 错误、`-32600` ownership 冲突（"thread held by another app-server"）、官方 rpcErr——**全部只 `slog.Warn` 后返回**（`agent/codex-web/events.go:551-559`），不回传给 RPC。
3. 更隐蔽的：`obsClient == nil` 时 `AttachLiveThread` **静默 no-op**（`events.go:533-534`）——新 go-bridge 进程上观察连接若仍在 backoff 重连，attach 根本没发生，RPC 依旧 Ok。

其中 ownership 冲突分支恰是合同关心的场景（Desktop 离开共享 daemon 时）：scope RPC 绿、live 流永远不来、iOS 以为自己在旁观。因此 §6.2.1「恢复事务必须等到 scope RPC 成功」按字面实现是一个**永远绿色的假验证**，直接违反 C6「已生效」。

建议：§6.2.1 补一句——「RPC 成功的定义必须包含逐 session 的 attach/subscribe 结果回传；现状 `Ok:true` 不区分 attach 成败（`handlers.go:1142` 异步发射、`events.go:553-559` 吞错、`obsClient` 未就绪时静默跳过）」，并把「把结果带回 RPC 响应」列为 L2 施工的一部分。

## 3. F2（§6.3.1 选项 b 前提未写明）：权威 pull 的数据源必须是官方源

代码直证：零目标窗口的事件在 **Kernel ingest 之前**就被订阅者门丢掉——`go-bridge/main.go:796` 的 `HasSessionSubscriber` 门位于 DeltaBatcher（`:797`）与 `kernel.IngestLive`（`event_publisher.go:829`）之前，所以断连窗口内连 go-bridge 内存 Kernel 里也没有 completed。

因此「重连后权威 pull 收口相位」（§6.3.1 选项 b）只有当 pull 的最终数据源是官方 `thread/read`（冷校准源；`agent/codex-web/events.go:515-516` 注释表明该源存在且用于观察连接自身缺口）时才成立；若 pull 只读 go-bridge 内存投影，则是假绿。建议 §6.3.1 给选项 b 加前提：「pull 必须能从官方源重建终态，实施前先核实 projection pull 的数据源链」。

## 4. F3（注脚级精度）：dispatchEvent 的边界情形

§4.4「dispatchEvent 不在旁观链上」对纯旁观成立；但若 bridge 在同一 thread 持有写 session（iPhone 早前发过消息），同一外部回合也会经写连接进 dispatchEvent，两路并行、投影层去重。建议加一句注脚，避免取证时在日志看到 dispatchEvent 就误判「修错了面」。

## 5. F4（#7 细化）：两个子变体走不同路径

#7 的「锁屏/退后台再回前台」若连接未实际断开，事件走 per-conn 缓冲投递，可能在 durable 不对称未修的情况下也通过；**真零目标路由（重启 Link、连接实际断开）才是 durable 不对称的强形式**。建议把「重启 Link」子变体标为 #7 必测，锁屏变体作为 iOS 缓冲路径的补充覆盖。

## 6. 结论与建议动作

修订版合格：层描述、禁令、验收矩阵与两仓代码现实一致；处置表没有把候选写成已证实，也没有删掉 owner 现象。按合同「先改本文再改代码」的纪律处置本轮发现：

| # | 动作 | 归层 | 时机 |
|---|---|---|---|
| F1 | §6.2.1 补「RPC 成功必须含 attach 结果回传」+ 对应施工项 | L2 | **开工前必须** |
| F2 | §6.3.1 选项 b 加数据源前提，实施前核实 pull 链路 | L3 | 选型前 |
| F3 | §4.4 加 dispatchEvent 边界注脚 | L3 注脚 | 随下次改文 |
| F4 | §7 #7 标注「重启 Link」为必测强变体 | 验收 | 随下次改文 |
