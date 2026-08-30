# turn_detail_lazy_v1 封闭取证报告（closed evidence, 2026-08-30）

诊断模式运行时（Mac 2412a8a + `TURN_ITEMS_DIAGNOSTIC=1`），owner 在真实会话
`01a04be1-444f-7e11-b339-6ca323141083` 依次点击思考回合，共触发 8 次诊断 walk、
覆盖 7 个独立回合（01a04c13 被点击两次，两次 walk 字节完全一致，分页确定性良好）。
诊断 walk 只取证不提交投影，诊断上限 128 页 / 16MB raw / 90s。

## 证据总表

| 回合 (turnId 前缀) | 页数 | EOF | raw 累计 | item 解码字节 | envelope | item 数 | 最大单 item | 结局 |
|---|---|---|---|---|---|---|---|---|
| 01a04de8 | 9 | ✅ | 571,777 B | 566,072 B | 5,705 B (1.0%) | 44 | 133,613 B cmd | eof |
| 01a04c77 | 46 | ✅ | 967,176 B | 937,241 B | 29,935 B (3.1%) | 227 | 155,827 B cmd | eof |
| 01a04c13 (×2) | 128 (cap) | ❌ | 5,731,672 B | 5,647,451 B | 84,221 B (1.5%) | 640 | **1,057,417 B cmd (p98)** | diag_max_pages |
| 01a04dd5 | 1 | ❌ | 21,075 B | 20,417 B | — | 5 | 18,873 B cmd | rpc_error @61s |
| 01a04cd5 | 19 | ❌ | 425,408 B | 412,906 B | — | 95 | 155,827 B cmd | rpc_error @70s |
| 01a04ded | 18 | ❌ | 425,285 B | 413,441 B | — | 90 | 38,597 B cmd | rpc_error @70s |
| 01a04e48 | 128 (cap) | ❌ | 4,583,252 B | 4,499,028 B | 84,224 B (1.8%) | 640 | **542,320 B cmd (p43)** | diag_max_pages |

item 构成（01a04c13）：reasoning 286 / commandExecution 230 / fileChange 110 /
agentMessage 8 / contextCompaction 5 / userMessage 1。

## owner 四问的答案

### 1. 1.2MB 级页面是单 item、多 item 还是 JSON 膨胀？
**单个 commandExecution item，实锤。**
01a04c13 p98：整页 raw 1,058,503 B，其中单个 commandExecution 1,057,417 B
（99.90%），同页其余 4 个 item 合计仅 428 B，JSON envelope 仅 658 B。
01a04e48 p43 同构：整页 543,661 B，单 item 542,320 B（99.75%）。
第一轮生产计量的 1.23MB 页面由此同机制解释（该轮最大 item 未测量；本轮直接观测到 1.06MB 单 item）。

### 2. 四个回合真正的 EOF 页数与总大小
- 两个回合到达 EOF：9 页 / 572KB、46 页 / 967KB。
- 两个回合**走满 128 页诊断上限仍未到 EOF**：raw 分别 5.73MB 与 4.58MB、
  各 640 items——真实回合规模 ≥128 页 / ≥5.7MB，此前"30-60 页 / 1-2.5MB"是低估。
- 三个回合被上游停摆中断（见下）。7 回合合计 1,741 items 中只有 2 个 >512KB。

### 3. JSON envelope / 转义开销
1.0%–3.1%，可忽略。字节几乎全是 item 正文（commandExecution 的 aggregatedOutput）。

### 4. upstream raw → CordCode projection 的放大/缩小
**无放大，轻微缩小**（仅 EOF 回合可测，失败 walk 无 HistoryTurn）：
- 01a04de8：raw 571,777 → 投影 parts JSON 554,983 = **97.1%**
- 01a04c77：raw 967,176 → 投影 parts JSON 918,762 = **95.0%**

即：整回合原子 patch 若存在，字节数≈raw。5.7MB 回合的原子 patch 将是冻结门限
512KB 的 11 倍、被否决的 2MB patch 上限的 2.8 倍——整回合原子路径对大回合不可行，
增量 per-page patch 是唯一出路（与终案架构一致）。

## 新发现：上游 ~60s RPC 停摆（3/7 walk 中断）

01a04dd5（第 2 页起）、01a04cd5（第 20 页起）、01a04ded（第 19 页起）的
thread/items/list 调用发出后上游静默 ~60 秒无响应字节，由我方传输层空闲保护
`ws.go:318 streamIdleLimit = 60s` 杀流报 rpc_error。特征：
- 停摆点与深度无关（01a04dd5 在第 2 页就停摆；而两个 128 页 walk 全程无停摆）；
- 间歇性、不可预测（43% 的 walk 命中）。

**结论：一次 walk 拉到 EOF 在真实网络/上游条件下不可靠——增量提交 +
已确认 cursor 断点续传不是优化项，是必需项。** 同时支持保留 90s 单次尝试上限
（60s 停摆 + 续传重试正好落在批次边界内）。

## 数据 → 终案参数映射（供裁决，不代裁决）

| 终案参数 | 观测依据 |
|---|---|
| patch 目标 128–256KB / 硬顶 512KB | 页粒度 patch 天然符合：除两个巨型页外，全部页 raw 在 2KB–165KB（p95≈160KB）。巨型页 99.9% 由单 item 构成，经 blob/chunk 摘除后 <10KB。**落地 oversize chunking 后无需任何额外截断即可满足目标带。** |
| blob/chunk 二级懒加载阈值 256KB | 1,741 items 中仅 2 个超 256KB（1.06MB、542KB）；其余最大 155,827B。阈值取 256KB 只摘除真正的巨型 item。 |
| 废止 512KB / 24 页永久门 | 实测真实回合 ≥128 页 / ≥5.7MB 且 EOF 未知；门限把合法大回合永久锁死。 |
| 批次预算 + 暂停/续传 | 128 页 walk 需 65–70s（页均 ~500ms，巨型页 0.8–1.2s），>150 页回合单次 90s 内走不完；叠加 43% 停摆率，续传必需。 |
| 单页 raw 门限（修正后数据再定） | 最大观测页 1.06MB，99.9% 为单 item → 该门限在 blob/chunk 落地后实际上由 chunk 阈值接管，单独的页字节门只需兜底防 envelope 异常。 |

## 附注

- 失败 walk 的 `replayReason` 复播为 max_pages（复播函数只建模页/字节/时间三门，
  不建模 RPC 错误）；生产冻结门下这三个回合实际会以上游错误/超时失败，不影响上表证据。
- 「点击无反应」为诊断模式设计行为：不提交投影 → UI 保持原失败态，证据在日志。
- 取证完成、参数裁决后：`launchctl unsetenv TURN_ITEMS_DIAGNOSTIC` 并重启 runtime
  恢复生产模式。
