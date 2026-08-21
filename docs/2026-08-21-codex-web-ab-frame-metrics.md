# codex-web vs 旧 codex 帧级 A/B 对照（§13.2）

采集时间：2026-08-22 04:00:01 CST

条件：同机；上游 mock Responses provider（排除 provider 单帧差异）；模型 mock-model；每侧 5 轮同型 prompt（MOCK:STREAM）；A=官方 daemon 路径，B=旧 backend app_server 模式（独立官方 ws app-server 实例，§10.1 并存规则）。

| 指标 | codex-web 均值 | 旧 codex 均值 |
|---|---:|---:|
| send → turn/started (ms) | 1 | 6 |
| send → 首 delta (ms) | 17 | 23 |
| 每轮 delta 数 | 10 | 10 |
| delta 平均字符 | 29 | 32 |
| delta 最大字符 | 29 | 32 |
| 相邻 delta 最大间隔 (ms) | 0 | 0 |
| 完成延迟 (ms) | 18 | 24 |

逐轮明细（A=codex-web / B=旧 codex）：

| # | A started | A first | A deltas | A gap | A done | B started | B first | B deltas | B gap | B done |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 2 | 18 | 10 | 0 | 19 | 21 | 38 | 10 | 0 | 39 |
| 2 | 4 | 22 | 10 | 0 | 23 | 3 | 18 | 10 | 0 | 20 |
| 3 | 1 | 15 | 10 | 0 | 16 | 3 | 19 | 10 | 0 | 21 |
| 4 | 1 | 17 | 10 | 0 | 18 | 3 | 22 | 10 | 0 | 23 |
| 5 | 1 | 16 | 10 | 0 | 17 | 2 | 19 | 10 | 0 | 20 |

结论边界：上游恒定（mock 单响应多 delta），两侧差异反映 transport/adapter 路径；Bridge 33ms batching 与 iOS render cadence 不在本测量内（§8.1 分层）。
