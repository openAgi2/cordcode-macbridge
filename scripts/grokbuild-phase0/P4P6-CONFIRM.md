# P4/P6 真实 turn 取证 — 费用、副作用与复位方案（报 owner 确认）

> 2026-09-07。Phase 0a 已完成（见 EVIDENCE.md）。**P7 判定已否定短命 mode-only 路径**（1.0.13 Pending 恢复后非 Plan），因此 P4(a)「idle set plan → 关闭 → 新 actor load → 真实 prompt 证明按 Plan 工作」**不再需要执行**——那只会再证一次已知结论，浪费费用。P4(b)（外部 actor 归属/写序）属于 Phase 2 改道后的取证，在提出并获得认可的官方生命周期方案之前不做。

## 本次请求确认的取证范围（P6 最小集，服务 Execute/1b 与 D1 准入）

全部在**隔离 GROK_HOME**（/tmp 临时目录）+ 本机 auth.json 只读复制的探针会话中进行；不触碰 `~/.grok` 真实会话、不动 leader（生产进程）。

| # | 取证项 | 预计调用量/费用 | 状态副作用 | 隔离与复位 |
| --- | --- | --- | --- | --- |
| 1 | 一条正常 prompt 的完整 turn 样本：session/prompt 请求/response（含 `result.stopReason`）、AgentMessageChunk 流、user echo、turn_completed、usage_update | **1 次模型调用**（grok-4.6，SuperGrok 订阅额度内；内容为"回复 ok"级别的最小指令） | 隔离 home 下新建 1 个可弃测试会话 | 删 /tmp home 目录即复位 |
| 2 | 取消路径：发起 prompt 后立即 `session/cancel`，记录 cancel 通知与终态形状 | ≤1 次模型调用（大概率立即取消，可能产生极小计费） | 同上，同一测试会话 | 同上 |
| 3 | 错误终态（可选）：给一条必然失败的最小 prompt（如空附件/非法参数）观察 error 终态 | ≤1 次模型调用 | 同上 | 同上 |
| 4 | hooks 命令样例（§7 正文组）：`/hooks-add` 非法路径失败样例 +（可选）一条无外发成功命令 | **0-1 次模型调用**（hooks-add 非法路径是本地失败，可能不消耗模型） | 在隔离 home 写 hook 文件失败（无文件落地） | 同上 |
| 5 | 权限请求样例（可选）：让 prompt 触发一次工具权限请求，记录 request/选项形状后拒绝 | 1 次模型调用（拒绝即终止） | 无文件修改（拒绝后中断） | 同上 |

**明确不请求**（未经单独授权一律不做）：
- `feedback` 命令（外部发送，绝不自动执行）；
- `dream`/`flush`（可能修改跨会话记忆；隔离 home 下记忆隔离未证明前不取样）；
- `always-approve` 状态组、compact 压缩（不可逆；后续需要时单独报明）；
- 真机 UI 操作、simulator、snapshot 测试。

## 总费用口径

- 模型调用合计 **2-4 次**最小 prompt（grok-4.6 订阅额度内；如取消/错误即时收口则更少）。
- 不确定性：取消路径若模型已开始生成，可能仍计入一次调用的 token；错误终态样例可能不触发模型。**上限按 4 次调用预估**。
- 无任何外发消息（feedback 不做）；无 ~/.grok 真实会话写入；不动生产 CordCodeLink/leader 进程。

## 复位方式

全部取证在 `/tmp/grokbuild-probe-home-*` 完成，取证后整目录删除即完全复位；auth.json 为只读复制，凭证不进任何证据文件（归档样本统一脱敏 email/team_id/agentId 等）。

---

**请 owner 确认**：是否按上表 1-5（或裁剪其中可选项 3/5）执行 P6 最小集取证？确认后我将立即执行并把样本归档进 `scripts/grokbuild-phase0/samples/`（p0b-exec）。在等待确认期间，我继续推进不依赖它的 1a（目录方向）与 Phase 2 阻断下的禁用/诊断交付。
