# dsh-web 单实例 3080 收敛方案 v3 第三轮评审报告（收口）

- 评审对象：`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md` v3（主仓，未 commit）
- 对照：`…-review.md`（一轮）、`…-review-r2.md`（二轮）
- 评审日期：2026-08-19
- 评审方法：不采信 §12 自述；对 R2-1 与七条建议的落点回源码。核验树 = `opencode/web` `9796432` + iOS 仓既有 Swift 锚点。
- 本轮目的：判定能否交给开发者开工，不再开方向评审。

## 1. 结论

**APPROVE。可以按 §12 五件切口交给开发者，不必先动 iOS，也不必再改设计方向。**

二轮唯一必改 R2-1 已按建议收敛成单一默认路径；七条建议都有正文落点。本轮抽查的新锚点全部属实，没有新的阻断或必改。剩下的是实施时的工程选择（类型化错误、终态只推一次），文档已经把「必须看到什么」写死了。

## 2. 二轮条款复核

| 项 | 落点 | 本轮 |
|---|---|---|
| R2-1 单一默认路径 | §3.2：宽限 `available=true` + `instance reconnecting (grace until <t>)`；`available=false` 封死 | ✅ `detectInstanceStatusProber` 确在 `agent_descriptor.go:247-255`（文称 `:246-258`，含注释，映射事实一致）：`available=false` 一律 `not_configured` |
| R2-1 撤回「iOS 藏入口」 | §3.2 iOS 实测段 | ✅ `BridgeProvider.swift:887` / `:1131` 只按 kind；`BackendModels.swift:34` 认 `deepseek-web`；`CCCodeBridgeModels.swift:182-183` 有 status/reason、无按 status 过滤 |
| R2-1 禁 not_configured 收窄为 RPC 语义 | §3.2 RPC 段 | ✅ 不再用藏入口当理由 |
| R2-S1 send 新接线 | §3.2 / §8.5 / §12 第 2 条 | ✅ `session.go:132-144` 走 `s.client.Call`；`handlers.go:2284/2306/2319` 三处 `send_failed`；go-bridge 零处 `backend_unavailable` |
| R2-S2 turn 终态生产者 | §3.2 / §12 第 3 条 | ✅ 写明 `runStreamLoop` 只重连；要求对 registry running 会话推终态 |
| R2-S3 两树 diff | 文首 | ✅ 补了 `main.go`、`RuntimeManager.swift`，删掉「完全一致」 |
| R2-S4 跳过探测 | §9 | ✅ 现行=换列表仍探、miss 仍 3096；本案=配置端口即位子 |
| R2-S5 行号 | §0.2 | ✅ 函数头 `:796`，`return true` `:810` |
| R2-S6 冷启动并发 RPC | §3.2 / §8.3 / §8.6 | ✅ 立即 `backend_unavailable`，不阻塞 30s |
| R2-S7 冷启动行端口 | §8.3 | ✅ 「权威端口（默认 3080）」 |

补充注记（reconnecting 文案 iOS 无消费点、可见性在 Mac）与默认路径一致，不是翻案。

§11 一轮表里 M3 行仍残留「实施 gate」字样，已被同节「已被二轮 R2-1 取代」划掉，**不构成实施歧义**（以 §3.2 / §12 为准）。

## 3. 交给开发者时的实施注记（非必改）

这些不挡开工，写进 r3 以免开发者踩坑；不必回写设计也能做对。

1. **类型化宽限错误**。`list_sessions` 今天失败码是 `list_failed`（`handlers.go:2939`），send 是 `send_failed`。要让「含已打开会话」的 RPC 都变成 `backend_unavailable`，resolver 应返回可 `errors.As` 的错误（例如 `ErrInstanceReconnecting`），handler 各入口识别后改码。不要只改 `handleSendMessage` 三处。
2. **已打开 session 如何感知宽限**。`dshSession.Send` 不经 `Resolve`。实现上让 `Send` 先看 resolver 宽限态并返回上述类型错误，或 handler 在 dsh-web send 前查询宽限态。两种都行，文档不锁死。
3. **turn 终态只推一次**。进入宽限与流 1006 可能先后发生。对同一 session 的终态要幂等，避免双发 `turn_error`。
4. **`InstanceStatus` 必须特判宽限**。现行 `Current()==nil` → `available=false`。宽限时要在 `lostAt` 置位期间仍报 `available=true`，否则又会经 detector 落成 `not_configured`。

## 4. 判定

| 问题 | 答案 |
|---|---|
| 还要改设计吗？ | 不要。方向、wire、锁、验收、切口已齐。 |
| 还要动 iOS 吗？ | 不要。通用错误气泡即可。 |
| 交给谁？ | 开发者按 §12 五件做；单测至少覆盖 §8.1（流重连 17s 不收养）和 §8.5（send 含已打开会话 → `backend_unavailable`，非 `send_failed`）。 |

本轮无新必改，不再开 v4。
