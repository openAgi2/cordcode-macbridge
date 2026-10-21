# S-3 v2 summary 首开 owner 验收（2026-10-20 通过）

配套：Mac d78100ee（卡死修复后 Release 18:45 部署）；iPhone 11 Debug
（ba56042f，v2 声明 + 判别解码 + F1-F3 全修）。

## owner 结论

「这次没出现长时间加载」——验收通过。

## 实测记录（fg-trace /tmp/v2-final.log + Mac log 对账）

| 会话 | 路径 | 耗时 | 载荷 | 消息 |
|---|---|---|---|---|
| a544f6e2 ×3 | 缓存+对账 | 0-3s | 增量 | 77 |
| eed413da ×2（原卡 5min） | 缓存+对账 | <1s | 增量 | 7 |
| 2fc42300 / 0806807a | 缓存 | 0-1s | 增量 | 13/16 |
| 3b95f2e6（新） | **v2 summary** | <1s | **4.7KB** | 14 |
| 608e03af（39 轮新） | **v2 summary** | 1s | **22KB，1 例外行** | 67 |

- v2 首开载荷 vs 全量：4.7KB/987KB ≈ 1/200；22KB ≈ 1/45。
- 零 TIMEOUT / 零 hydrate 阻塞；例外通道（always-visible）真机可用。
- 事故回归：尾部悬挂 turn 卡死（5 分钟）修复后 eed413da 秒开。

## 未尽事项（登记不阻塞）

- 展开明细（session_turn_items claude producer）本轮 owner 未操作——
  代码/测试已交付，交互验收留待日常使用覆盖。
- 缓存判定边界：他机（iPhone16）播种的旧缓存使本机首开走全量（eed413da
  首轮）——跨设备缓存形态优先级问题，低频，后续优化。
