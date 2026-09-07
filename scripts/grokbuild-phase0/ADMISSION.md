# D1 准入终表 + 模式可行/阻断结论（p0b-regression 交付）

> 2026-09-07。二进制 `grok 1.0.13 (5e9a58528b76)`（sha256 见 SOURCES.md）；源码对照 grok-build 1.0.16 @ `72a6125`。
> 实现代码：`agent/grokbuild/acu_state.go`（`grokAdmittedCommands` / `grokExcludedCommands`，守卫测试 `TestGrokAdmittedCommandsTerminalTable`）。
> 证据样本：`samples/`（下表逐行标注）；形状断言：`validate_samples.py`（P6 段全绿）。

## 一、反馈类型准入终表（§7）

**准入类型判据（正文组，P6 已验证）**：host-turn 本地 built-in 命令——
① 零模型（`totalTokens=0`）；② 反馈正文（成功与失败文案同轨）经 `session/update` agent_message_chunk 且 `_meta.hostTurn=true`；③ `end_turn` settle（`turn_completed{stop_reason:"end_turn"}` + prompt 响应 `stopReason:"end_turn"`），失败文案不是 RPC reject。
[样本] A/B；[源码] slash_exec.rs builtin 表本地执行、`ok_end_turn(0, None)`。

| 命令 | 官方目录 | 准入 | 依据 | 样本路径 |
| --- | --- | --- | --- | --- |
| `hooks-list` | ✔（27 条 ACU） | **准入** | 正文组判据，成功样例直接验证 | samples/p6-turns.json turn A |
| `hooks-add` | ✔ | **准入** | 正文组判据，失败文案样例直接验证（成功路径同轨，未单独取样） | samples/p6-turns.json turn B |
| `hooks-remove` | ✔ | **准入（按类型判据）** | 同 slash_exec.rs 本地臂、同反馈形状；未逐一取样 | 类型判据 + [源码] |
| `hooks-trust` | ✔ | **准入（按类型判据）** | 同上 | 同上 |
| `hooks-untrust` | ✔ | **准入（按类型判据）** | 同上 | 同上 |
| `context` | ✔ | **排除** | pager-local surface（ShowContextInfo），第一波未接入 | 方案 §1 D1 |
| `feedback` | ✔ | **排除** | 会外发消息；未经 owner 明确授权不执行、不取样 | 方案 §6/§7 |
| `dream` / `flush` | ✔（目录/队列观测） | **排除** | 可能修改跨会话记忆；隔离性未证明 | 方案 §7 行 2 |
| `always-approve` | ✔ | **排除** | 不可逆权限变更；状态组通道未接通 | 方案 §7 行 3 |
| `compact` | ✔ | **排除（未取证）** | 压缩不可逆；状态组未取证 | 方案 §7 行 3 |
| `session-info` / `plugins` / `reload-plugins` / `goal` / `loop` / `workflow` / skills 类 / 其余 bundled | ✔ | **排除（未取证）** | 各属无输出组/状态组/skills·workflows·goal 组，反馈形态未按矩阵取证 | 方案 §7 行 4 |

**扩表规则**：新命令进入本表必须先有对应组的样本取证 + 类型判据复核（§7「新命令不得只因出现在目录就自动获准」）；`TestGrokAdmittedCommandsTerminalTable` 把默认表钉死为上表 5 条，扩表须同步改守卫测试与本表。

## 二、turn 生命周期结论（服务 1b Execute）

1. 正常终态集合 = `end_turn`（C 样本）；取消 = `cancelled` + `cancellationCategory=MidTurnAbort`（D 样本，response `_meta` / ext TurnCompleted `_meta` / prompt_complete 三处一致）；[源码] 全集 {end_turn, cancelled, max_tokens, refusal}，硬错误 = RPC reject（无 stopReason、无 turn_completed）——**dispatcher 两路都要能终结**。
2. `session/cancel` 是 notification（无响应帧）；取消后原 `session/prompt` 响应自行以 `cancelled` 终结——terminal future 不得只等 end_turn。
3. usage 三处、无独立 usage 通知：prompt 响应 `result._meta`（含 `usage{modelCalls,costUsdTicks,modelUsage}`）；ext TurnCompleted `usage.totals`（camelCase）；`response_completed` update（anthropic snake_case）。取消 turn 自身无 usage 报告（D）。
4. hostTurn 反馈正文经标准 `session/update` 流出（与模型正文同轨、`_meta.hostTurn=true` 区分）——共用 Events 通道即可承接，无需独立通道。

## 三、模式可行/阻断结论（P4/P7/P8 汇总）

- **P7（决定性）**：短命 mode-only 路径（idle set plan → 落盘 → 关闭 → 新 actor load）恢复后有效模式**不是 Plan**（无 CMU、`plan_mode.json` 保持 Pending、system prompt 无 plan 注入、`prompt_context.prompt_mode` 恒 extend）。与 1.0.16 源码 `from_snapshot`（Pending→Inactive）一致。**Phase 2 短命方案停止**；未出现经证明的官方 actor 生命周期方案前，只交付明确禁用与诊断（p2/p3c 已按此口径交付：`grokModeSwitchBlockedReason` + 三态 chip 只读）。
- **P4(a)** 按 P7 判定**免执行**（再证一次已知结论只浪费费用）；P4(b)（外部 actor 归属/写序）属 Phase 2 改道后的取证，在官方生命周期方案获认可前不做。
- **P8 权威读契约**：唯一持久化源 = session 目录 `plan_mode.json`；恢复映射 Active→plan、Pending/ExitPending/Inactive→default、未知/缺失→unknown。已实现于 `agent/grokbuild/session_mode.go`（GetSessionMode）并贯通 hydrate 投影（p2/p3c）。
- **P6 模式侧**：真实 turn 中未观察到任何模式相关通知或状态迁移（C/D 全程无 CMU）——与会话内 set_mode 才产生 CMU 的源码行为一致；模式读写不依赖 turn 通道。

## 四、费用与复位（收口）

- 模型调用：C 1 次（modelCalls=1，totalTokens=14691，costUsdTicks=35159400）+ D 已开始生成（部分流，上游未报告计费）；A/B 0 次。在 owner 授权 ≤4 次内。
- 复位：`/tmp/grokbuild-probe-home-p6`、`/tmp/grokbuild-p6-out` 已删除（2026-09-07）；无外发消息；未触碰 `~/.grok` 真实会话与生产进程。
