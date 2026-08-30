# G0 懒加载会话历史 — 证据报告与裁决清单（2026-08-30）

## 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-codex-remote
分支=codex/codex-remote-backend
提交=见下方提交链（本报告与 fixture 同批提交）
未提交状态=CLAUDE.md（owner 手改，保留不动）；.exec-plan/state/plan-6e34cf39c628.json（exec-plan 约定不入库）
任务预期分支=codex/codex-remote-backend（kickoff 指令指定）
上游源码=/Users/jacklee/Projects/codex @ tag rust-v0.150.0-alpha.12.2（git show 只读；本地 checkout 不在 tag 上，全部锚点经 tag 读取）
实测目标=ChatGPT Desktop 26.825.41651（bundle 7345）/ 内嵌 codex-cli 0.151.0-alpha.7.1 / controller 协议 v3
```

## 采集概要（4 次 live 运行，全部 owner 在场授权）

| 运行 | 结果 | 贡献 |
| --- | --- | --- |
| 1 | 探针挂死（已终止） | 暴露空闲看门狗 × 慢 RPC × close 竞态三连缺陷（修复已提交） |
| 2 | 完成 → attempt-009 | 全电池：inventory、链路、items 16 页、illegal、resume；冷态控制读 240s 超时（第 1 次） |
| 3 | 发现阶段中止（部分保全） | 暴露老线程冷加载翻页超时 + 阶段数据丢失两个缺陷（修复已提交） |
| 4 | 完成 → attempt-010 | legacy 次级电池、暖态控制读（240s 超时，第 3 次证据）、跨步发现 24 线程 |

fixtures：`agent/codex-remote/testdata/phase0/live/attempt-009-history-lazy-g0.json`、
`attempt-010-history-lazy-g0.json`（均 gitleaks PASS、validator `--history-only` PASS、
`history-fixture-assert.mjs` 零负结果）。事件日志保留于提交历史与本报告附注。

## §3.0.5 九项清单（attempt-010）

| # | 项 | 状态 | 证据 |
| --- | --- | --- | --- |
| 1 | historyMode 清单 | present | 150 线程 = 22 paginated + 128 legacy + 0 未知 |
| 2 | Summary 0/1/2 分布 + cursor 往返 | present | 0:0 / 1:5 / 2:20；backwards 往返 pass |
| 3 | NotLoaded 空 items 同一性 | present | notLoaded 首页 6093B/456ms（summary 同窗 74753B/654ms，12× 缩减） |
| 4 | items/list 形状/翻页/空页/非法 turnId | partial | 4 组多页样本（最深 16 页）；无空页观测（EOF=nextCursor=nil，空页非协议必需）；非法 turnId 空页成功（官方语义） |
| 5 | Reasoning 四态 + 非空 content | present-shapes-but-no-nonempty-content | 55 样本：双空 32 / 仅 summary 23 / 仅 content 0 / 双非空 0 → **G0.5 裁决** |
| 6 | CommandExecution output | present | null 2 / 非空 42，最大 57040B |
| 7 | Summary↔items 官方 id 一致性 | present | first-user / final-agent 分别比对 pass（neg-1） |
| 8 | 分 historyMode 字节/耗时 | present | paginated 链路 + legacy 全读（见 T0.2 表） |
| 9 | >30 回合翻页全链 | 修订后达成（账号覆盖限制） | 24/150 采样最深 25 回合；cursor 机制由 items 16 页链 + 官方锚点覆盖（计划已修订） |

## §3.0.7 负结果：零触发

- neg-1/2/3/4/5 全部 pass（neg-5 按 2026-08-30 修订后的官方过滤语义断言）。
- neg-6 control 对照**不可获得**（修订记录，见下）。

**计划修订（三条，全部带官方源码或三重实测证据）**：

1. **§3.0.7-5**：tag `rust-v0.150.0-alpha.12.2` `thread_processor.rs:3365-3425`——`turn_id`
   直接下推 store 过滤，错误映射只覆盖 thread 级；未知合法 turnId → 200 空页。线上实测一致
   （attempt-009/010 均 52B 空页）。断言改为：空页成功为正；返回错误或非空 items 为负。
2. **§3.0.7-6**：paginated 线程 `includeTurns=true` 经 WSS 冷/暖态均 240s 无响应（×3）；
   同传输 legacy 768ms 成功（148591B/13 turns）→ 模式特有行为。无缺口判定退位链内证据
   （无重复 + cursor 链完整 + EOF + backwards 往返 + notLoaded 同一性 + §1.5 官方不变量）。
3. **§3.0.5-9**：账号无 >30 回合线程（24/150 采样，最深 25）；cursor 机制由 items 多页链覆盖。

## T0.2 体积/耗时基线（按 historyMode）

| 路径 | paginated（id-1/id-12） | legacy（id-143） |
| --- | --- | --- |
| thread/read 元数据 | 2893B / 682ms（7361B / 415ms） | 2975B / 418ms |
| Summary 首页（limit 30） | 74753B / 654ms（96107B / 816ms） | 38509B / 668ms |
| notLoaded 首页 | 6093B / 456ms | 未测（legacy 无该视图语义） |
| includeTurns 全读 | **不可获得**（240s×3 超时） | 148591B / 768ms / 13 turns |
| items/list | 按 turn 分页（下） | -32601 method-not-found |

**单回合资源画像**（items/list，limit 5/页）：

- 中位 turn：2–5 页、10–25 items、~85KB；
- 最重 turn（id-341）：16 页、77 items、376KB 总量，最大单页 108KB；
- 最大单 item：commandExecution output 57040B。

**资源门建议值**（供 T2.x 实现与 owner 裁决）：turns 页 limit 30（75–96KB/页）；items 页
limit 5；单 turn items 上限 24 页 / 512KB（超限 → `failed` + 原因码，不截断不 placeholder）；
单 RPC 超时 30s（链路页 90s 冷启动窗口）；控制面 includeTurns **不得**出现在产品路径
（paginated 不可用，legacy 走旧路径）。

## T0.3 Summary 断言

0/1/2 分布 0:0、1:5、2:20（各槽可缺席实证）；仅 first-user/final-agent 类型（neg-2 pass）；
`itemsView/status/time` 字段齐备；cursor 正反往返 pass。

## T0.4 十类 item 覆盖

- live 实测 5/10：userMessage(64)、agentMessage(71)、reasoning(55)、commandExecution(44)、
  fileChange(10)。
- 未观测 5 类：mcpToolCall、dynamicToolCall、plan、webSearch、contextCompaction →
  按计划保持 fail-closed / SkippedTypes 可观测；解码面由 schema replay 十类覆盖
  （proven 主张仅限 live 出现类型，计划 §3.1 原文规则）。

## T0.5 legacy 裁决（pending owner）

实测（id-143，128 个 legacy 线程在库）：turns/list **可用**（38509B 首页）；items/list
**-32601**（与官方 `"thread/items/list is not supported yet"` 一致）；includeTurns 全读
**可用且快**（768ms/148KB）。

**建议**：legacy 保留旧全读路径（includeTurns），不提供 summary/items 懒加载能力；
§2.5 能力矩阵 legacy 格 = full-read only。不接受"仅传输收益"折中（items/list 缺失使
明细懒加载在 legacy 上不可实现）。

## T0.6 resume 候选路径裁决（pending owner）

- 关键断言 `thread.turns == []` **两次运行均成立**；initialPage 25 turns + 双 backwardsCursor；
- 往返：candidate 1 RPC 921ms/78611B vs baseline 2 RPC 1336ms/77646B；
- 字节确定性：两次运行 78611B 完全一致；
- live subscription：单次 attach，resume 后 live 方法仅 `thread/goal/cleared`，无重复流迹象。

三条件齐备 → **建议启用**：resume(excludeTurns+initialTurnsPage) 作为冷打开首页原语
（§2.4 producer 补水合取页沿用 ReadThreadSummary 的官方不变量）。

## G0.5 reasoning content 裁决（pending owner）

55 样本 content 全空（双空 32 / 仅 summary 23）→ 该账号历史中 reasoning content 整体不可用。
**建议**：按计划 §3.0.5-5 原文——从本版验收与实现主张中**删除"完整思考"**，UI 仅映射
summary，不保留 pending 形状声明。

## 版本重锚（已按 owner 开工指令 + runbook 建议接受）

证据锚定 installed 26.825.41651 / 0.151.0-alpha.7.1（探针运行时记录）；
源码锚点保持冻结 tag `rust-v0.150.0-alpha.12.2` + additive-only diff 记录
（usageMetadata / functionCallOutput / misalignment 错误细节，anchored 文件核心函数未变）。

## G0 判定

**G0-PASS-CONDITIONAL**：九项达成（含三条证据化修订）、零负结果触发、T0.2/T0.3/T0.4
基线齐备、版本重锚已接受。**待 owner 裁决四项后放行 Phase 1**：
T0.5 legacy 策略、T0.6 候选启用、G0.5 删除完整思考主张、资源门建议值。
