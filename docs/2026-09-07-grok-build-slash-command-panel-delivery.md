# Grok Build 命令面板与计划模式——交付说明（p4）

> 2026-09-07。方案：`docs/2026-09-07-grok-build-slash-command-panel-implementation.md`（v1.5，评审基线 plan/approval-layer @ 6b6994a）。
> 双仓：Mac `cordcode-macbridge-plan-approval`（本仓，plan/approval-layer）；iOS `cordcode-ios-plan-approval`（plan/approval-layer-ios，评审基线 iOS main @ c3b1d5b0 只读未合并）。
> 目标二进制：`~/.grok/bin/grok` = `grok 1.0.13 (5e9a58528b76)`，sha256 见 `scripts/grokbuild-phase0/SOURCES.md`。源码对照 grok-build 1.0.16 @ `72a6125`（owner 指令：实施前先读 `/Users/jacklee/Projects/grok-build` 源码找现成实现）。

## 一、交付总览（按方向）

| 方向 | 交付物 | 提交 | 状态 |
|---|---|---|---|
| **目录（List）** | 专用 child 真实拉取会话 ACU + ACU side-state（(sessionID,cwd) 键控、list 优先、失败禁用）+ D1 准入交集 | Mac 94bf46d（1a）+ 48895c5（P6 后准入 5 条 hooks-*） | ✅ proven |
| **执行（Execute）** | 共用 turn dispatcher（镜像 grok-build TurnReportSlot/FinalizationGate/cancel 结算）+ 活 actor 注册表 + ExecuteSessionCommand 真实现 + stopReason/_meta 终态保留 | Mac 4565a62（1b）、ddb76db（3b cancelled→aborted 投影） | ✅ proven |
| **模式（Mode）** | P8 权威读（plan_mode.json+官方恢复映射）+ typed `sessionMode` 投影视图 + iOS 三态只读 chip；**切换 P7 阻断未交付**（按方案 §8 行 2 失败分支：明确禁用+诊断） | Mac 627d610（p2）、iOS 27e8b879（p3c） | ⚠️ 只读诊断交付 |
| **iOS 面板** | capability 门控扩 Grok + 每次打开实拉（UIDeferredMenuElement.uncached）+ 认领失败分离 + scope/代际异步隔离 | iOS fd1e3cbb（p3a）；p3b 零改动验证 | ✅ proven |
| **Phase 0 取证** | P1/P2/P3/P5/P7/P8 零模型 + P6 真实 turn（owner 确认 ≤4 次调用，实际 C 1 次 + D 部分流） | Mac e734b51 + 48895c5 | ✅ 归档 |
| **协议/文档** | bridge-v1.md canonical+镜像（session_commands 扩 grokbuild、turn 终态增量字段、`sessionMode` 视图）+ 双仓 CHANGELOG + 本交付说明 | 本提交（p4） | ✅ |

## 二、Phase 0 取证与费用（收口）

- 样本全部归档 `scripts/grokbuild-phase0/samples/`（17 份 + p6-turns.json），脱敏完备（313 UUID→REDACT_UUID；email/teamId/agentId/hostname/token 零出现；cwd 路径保留为既有口径）。
- **P7 决定性判定**：短命 mode-only 路径（idle set plan→落盘→关闭→新 actor load）恢复后有效模式**不是 Plan**（无 CMU、`plan_mode.json` 保持 Pending、system prompt 无 plan 注入、`prompt_context.prompt_mode` 恒 extend）——Phase 2 按方案阻断，未自造兼容层。
- **P6 真实 turn**（owner 2026-09-07 确认，隔离 home）：A/B hostTurn 反馈零模型实证（`totalTokens=0`）；C 完整模型 turn（end_turn，modelCalls=1，14691 tokens，costUsdTicks 35159400≈$0.0035）；D 取消（cancelled + MidTurnAbort 三处一致；cancel 为 notification，prompt future 自行终结）。实际模型调用 1-2 次 ≤ 授权 4 次。`/tmp/grokbuild-probe-home-p6` 已删除复位；无外发消息（feedback 未执行）。
- **准入终表**：`scripts/grokbuild-phase0/ADMISSION.md`（正文组 5 条 hooks-*：2 直接取样 + 3 类型判据；扩表须样本+守卫测试同改）。

## 三、执行语义要点（与 dsh 的差异）

1. Grok 命令是 **prompt 语义 host 动作**：经会话自己的活 actor 共用 turn dispatcher（绝不 `send_message` 降级、绝不另起 child）。无活 actor（冷会话）→ 明确失败「先在会话里发条消息」。
2. 反馈可见面：官方正文经 `agent_message_chunk`（update 级 `_meta.hostTurn=true`，1.0.13 实证两层 meta）→ 时间线 **assistant 消息**（成功/失败文案同轨、无业务错误窗）；无 dsh 式命令卡/计划芯片。
3. 终态：`end_turn`→success+resultText 原文；`cancelled`→RPC 失败（带类别）；硬错误（RPC reject/EOF）→`execute_failed`；**EOF 不成功**（actor 死亡结算为错误，绝不静默成功）；operation lease 单在飞（busy 快速失败，不镜像官方 prompt_queue——取舍记录 `scripts/grokbuild-phase1b/TESTS.md`）。

## 四、测试证据汇总

- Mac 定向+回归全绿：1b 15 测试（dispatcher 单元/注册表 CAS/e2e 终态与取消/EOF/lease）+ go-bridge 终态 payload 与 cancelled→aborted 测试 + 三包回归（grokbuild 29.9s / go-bridge 74.8s / core 0.5s）；运行记录 `scripts/grokbuild-phase1b/TESTS.md`。
- iOS：SlashCommandGrokPanelTests 13/13、SessionProjectionModelsTests+SessionModeProjectionTests 15/15、定向 build（iPhone 17 Pro Max 模拟器）SUCCEEDED。
- 样本形状断言：`validate_samples.py` 全绿（含 P6 44 项，log `samples/validate-run-p6-2026-09-07.txt`）。
- 协议一致性（p4-tests）：bridge-v1.md canonical ≡ iOS 镜像（byte-identical）；文档键（sessionMode 四键 / stopReason/cancellationCategory / cancelled→aborted）与 go-bridge wire 代码逐一核对一致。

## 五、安装与 owner 验收

- iOS 基线：fd1e3cbb（3a）+ 27e8b879（3c）——p3b 零 iOS 改动，无新二进制，不重装；3b 行为（cancelled→已中止展示）由 Mac 侧投影驱动，重连后生效。
- **owner 真机操作矩阵（§9 ①-⑥）待 owner 实际执行逐项打勾**——安装成功不代替产品行为验收：
  - ①两种输入布局每次打开目录三态（实拉/空/失败可重试）；
  - ②hooks-list 点选执行正文原样显示、hooks-add 非法路径失败文案原样显示（本批新增可验项，替代原 compact 项——compact 未准入）；
  - ③未知手写 slash 与认领失败分离（保留输入、明确提示、绝不普通发送）；
  - ④模式门未过 → **Plan pending→确认→重启→真实计划 prompt→审批回 default 全链不做**（P7 阻断；只有只读 chip 三态可看）；
  - ⑤A/B 会话切换和权限菜单不串；
  - ⑥Mac 外部切换仅在已证明范围观察，未知归属不可写（模式 canSet=false 恒成立）。

## 六、未完成 / 阻断逐项（诚实清单）

| 项 | 状态 | 依据 |
|---|---|---|
| 模式切换（iPhone 切 Plan / ExitPending 审批） | **阻断，未交付** | P7 决定性失败；须先有经证明的官方 actor 生命周期方案（P4(b) 外部 actor/常驻路径）才可开通 |
| P4(a) 恢复后按 Plan 工作证明 | 免执行 | P7 已否定短命路径，再证一次已知结论只浪费费用 |
| 其余命令组准入（compact/always-approve/goal/skills/workflows/session-info…） | 未取证未准入 | §7 矩阵：无输出组/状态组/skills 组需各自样本取证；扩表规则见 ADMISSION.md |
| `feedback`/`dream`/`flush` | 明确排除 | 外发/跨会话记忆未隔离；未经单独授权不执行 |
| iOS 仓 `docs/protocol/unified-bridge-protocol.md` 镜像落后 canonical（codex-remote §11.7/11.8 等既有漂移） | 未同步（非本任务范围） | 属并行 codex-remote 工作的同步债；本任务只同步 bridge-v1.md（canonical 权威） |
| owner 真机矩阵 | 待 owner | 上表 §五 |

## 七、回滚

- 目录+执行：`grokCommandsReady` 关闭（单 atomic）即 capability 下线，iOS `/` 按钮消失；不做旧缓存成功路径。
- 模式：`sessionMode.canSet=false` 恒成立（无入口可回滚）；诊断 chip 可随投影字段缺席自然隐藏。
- 协议全部 additive（sessionMode/stopReason/cancellationCategory 零值省键），旧客户端忽略；绝不恢复六键 legacy 空转、不退回 dsh `/plan` 内置菜单。
