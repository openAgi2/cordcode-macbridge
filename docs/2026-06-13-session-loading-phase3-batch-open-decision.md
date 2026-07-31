# Phase 3 batched open_session 决策:NO-GO

> 依据:`docs/2026-06-13-session-loading-systemic-redesign.md` 第 7.1 节门槛、Phase 0 基线、Phase 2 分页完成后实测。

## 决策

**NO-GO**:不实施 batched `open_session`。记录证据,不新增 `BatchSessionOpener` 接口,不声明 `batch_open_session` capability。

## 门槛(第 7.1 节)

实施条件(满足任一即 GO):
```
非历史解析/传输的串行前置耗时 > 打开总耗时的 15%
或 > 150ms
```

## 证据

### 1. Phase 0 基线(NO-GO)

`docs/2026-06-13-session-loading-baseline.md` 第 97 行明确判定:
> C: batched `open_session` | NO-GO | Measured dominant costs are history parsing/materialization/transport, not an extra serial RPC above the 150ms threshold. Re-evaluate after pagination.

主导耗时是历史解析/物化/传输,不是超过 150ms 的额外串行 RPC。

### 2. review 文档(batch-open 收益被高估)

`docs/2026-06-13-session-loading-review.md` 第 67-77 行:
- `resume_session` 当前已是近乎空操作(订阅连接 + 解析 directory,约 1 个 LAN RTT)
- 支柱 C 真正能省的:把 2 次**廉价**往返(resolve + resume)合并成 1 次 RPC,**LAN 上约省 5-20ms**
- 用户感知的"长时间转圈后超时"是单次 RPC 太慢(历史解析),不是往返次数多——合并 RPC 不减少那次历史解析的工作量

5-20ms 远低于 150ms 阈值。

### 3. Phase 2 分页完成后(post-pagination 复评)

分页已完成(phase2-bridge-pagination-regression done)。post-pagination Relay 历史请求典型耗时约 28-57ms(exec-plan `phase3-batch-open-decision-impl` notes)。

门槛的两个条件:
- **>150ms**:串行前置耗时(resolve + resume)约 5-20ms,远低于 150ms。**不满足。**
- **>打开总耗时 15%**:打开总耗时含历史解析(数百 ms~秒级),5-20ms 占比 < 5%。**不满足。**

## 结论

两个门槛均不满足。batched `open_session` 不实施。

Phase 2 分页(已实施)是打开超时的真正解药——它限定了首屏 payload,把大会话的 12.45MB 历史响应压到 ~960KiB 页。batch-open 只能再省 5-20ms 往返,收益可忽略,不值得新增接口和 capability 的复杂度。

## 后续触发条件

若以下任一变化发生,重新评估:
- 测得 resolve/resume 串行前置 > 150ms(例如 Relay 路由恶化、directory 解析变慢)
- 打开总耗时显著下降(历史解析被其它机制优化),使 5-20ms 占比升至 > 15%
