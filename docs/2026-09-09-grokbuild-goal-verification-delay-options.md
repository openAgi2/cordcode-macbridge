# Grok Build goal 完成态延迟：纯官方 runtime 下的方案清单（未实施）

> 2026-09-09 记录。owner 报障：grok build 模式 goal 任务**可见回复结束后，仍需几十秒
> 才变为完成态、goal bar 才消失**。本文回答「纯桥接官方 Grok Build（1.0.24）前提下还有
> 哪些修复路径」，供后续 session 直接取用。**本方案未实施**，仅为待提升项备档。
>
> owner 裁决背景（2026-09-09 收尾）：曾有一轮用「patch 官方 grok 源码 + 整机替换
> runtime」修复此问题，被 owner 否决并已回退——CordCode 恢复为只桥接官方 Grok Build，
> **不再通过 CordCode 私有 runtime 修补**（违反「cordcode 不拥有 agent 进程、官方 CLI
> 是真相源」的产品初衷，见 iOS 仓
> `docs/[综合版对比]2026-08-04-t3code-vs-cordcode.md` §0/§2/§6）。后续 session
> 不得重走 fork-runtime 路线。

## TL;DR

官方 1.0.24 的 goal 两阶段验证中，第一阶段（隐藏 round evaluator）对所有可观测信道
**结构性静默**，桥接侧拿不到任何提前信号。因此只有两条真路：

- **A. 上游 issue/PR**（根治，推荐立即做）：补丁已在本地分支备好，且与官方 latch
  设计同构，上游亲和度高；
- **B. MacBridge 投影层提前进入已有「验证中」态**（短期可选，唯一不依赖上游、又不
  伪造完成态的桥接侧方案）。

其余：C 等官方新版本、D 接受现状（可辩护默认）、E 定时器伪造完成（**禁止**）。

## 官方 1.0.24 的结构性静默（源码证据）

grok goal 回合在官方源码中的时间线（锚点见文末来源清单）：

1. 可见 worker 回复结束 → `run_goal_round_end()`（goal.rs:1330）；
2. **第一阶段** `evaluate_goal_round()`（goal.rs:73-143）：完整隐藏模型推理（最多
   重试两次），**不发射任何事件**——不写 ACP 流、不写 updates.jsonl，连
   `WorkerCompleted` 历史条目也要等它返回后才追加（goal.rs:1365）。这就是 owner
   看到的几十秒；
3. **第二阶段** `verify_goal_candidate()`（goal.rs:1374→164）才第一次调用
   `emit_goal_verifying`（goal.rs:188→360-375，置位 `verifying_in_flight`）——这是
   MacBridge `7ee42df` / iOS `88f89fcf` 已消费的 `verifying_completion` 信号，在官方
   runtime 下**必然迟到一整个第一阶段**；
4. goal 通知同一时刻双写：ACP 扩展通知 `x.ai/session_notification`（实时）+
   updates.jsonl 持久化（goal_orchestrator.rs:79-103）。换信道（轮询/tailer）拿不到
   更早的点。

已排除的歪路：

- `goal_classifier_enabled` 是 kill-switch：关掉它 goal 会直接 **pause**（"Goal
  verification is unavailable"，goal.rs:167-173），不是「快完成」开关；
- 第一阶段 evaluator 耗时是上游推理延迟，桥接侧无法缩短。

## 方案

### A. 上游 issue/PR（根治，推荐）

本地分支 `codex/upstream-proposal-do-not-deploy-early-goal-verifying`
（grok-build checkout）保留着完整补丁：在可见回复结束、第一阶段开始前调用官方已有的
`emit_goal_verifying`，并用官方已有的 `TrackerDropGuard` 在结束/取消时清 latch。分支
描述已标注 `DO NOT DEPLOY — retained only as an upstream issue/PR proposal`。

上游亲和度论据（写 PR 描述可直接用）：补丁零新机制——官方对 planning 阶段已有对称的
`emit_goal_planning`（goal.rs:377-392），第二阶段已有 `emit_goal_verifying` +
`verifying_in_flight` latch；补丁只是把同一 latch 复用到更早的边界，17 行 + 协议回归
测试（`goal_planner_e2e_tests.rs`）。收益表述：慢 evaluator 会让所有客户端在整个第一
验证阶段把 goal 显示为「执行中」。

动作：向 xai-org/grok-build 提 issue（含本仓报障 repro）+ PR（该分支 diff）。合入后
所有客户端受益、CordCode 零维护。不确定性只在 xai 响应周期。

### B. MacBridge 投影层提前进入「验证中」态（短期可选，未实施）

关键前提已成立：iOS 已把 `verifyingCompletion` 当作「用户可见完成边界」（
`ChatViewModel.swift:1000` 注释原话），banner/composer 收口与**回退语义都已实现**
（"evaluator 要求继续时后续 false 会重开"，`ChatViewModel.swift:1000-1009`）。因此
B 只需在 go-bridge 的 grok goal 状态机加触发器：

- **触发**：goal 激活 + 该轮可见 assistant 消息已结束 + 静默 N 秒（远小于 evaluator
  耗时，例如 3-8s 起调）且无 chunk/tool 活动 → 本地投影 `verifyingCompletion=true`；
- **回退**：再出现 chunk/tool/user 消息或官方快照 → 立即回执行态（官方 false 的
  重开路径已存在）；
- **完成仍只认官方** `GoalUpdated` completed，不因启发式提前显示完成。

交互走查（方案 B 的过程展示拍；打开/输入/发送三拍沿用现有 goal 流程不变）：goal turn
执行中 bar 显示执行态 → 可见回复结束 + 静默窗到 → bar 切「验证中」、composer 解锁
（不隐藏 bar、不显示完成）→ evaluator 裁决 Continue 则 bar 回执行态继续下一轮；
completed 则正常完成收口；evaluator 失败 pause 则走已有 blocked 收口
（MacBridge `3bf81ea` 已分类 `goal_evaluation_failed`）。

代价与风险：

- 轮中长思考 / 工具间隙可能短暂误标「验证中」再弹回（标签闪动，不是错误完成）；
  需要调好去抖窗口，且只以「assistant 消息结束」为触发点，不以 tool 间隙触发；
- 属启发式：代码须明示「派生态」（建议 wire 上可区分 derived vs official，或至少
  注释+文档声明），不得伪装成官方信号；
- 落点只在 MacBridge（`agent/grokbuild/goal_workflow.go` + go-bridge projection），
  不动协议文档语义、不动 iOS。

### C. 等官方 1.0.25+ 自行修复

A 的被动版。每次升级 grok 时留意 release note / 上游 issue 进展即可。

### D. 接受现状（可辩护默认）

第一阶段期间 bar 显示「执行中」是官方语义的如实转译，零风险零代码。当前收尾后的
状态即此。

### E. 禁止：定时器判完成 / 直接隐藏 bar

evaluator 可能裁决 Continue（官方补丁注释自认这一点），提前显示完成 = 伪造终态，
踩「不得制造假成功」红线。永久排除。

## 当前状态与遗留物（2026-09-09 收尾后）

- grok-build checkout：`main` 已恢复 `origin/main@75810042`，工作树干净；补丁只在
  分支 `codex/upstream-proposal-do-not-deploy-early-goal-verifying`（未推送、未建
  issue/PR）；
- 本机 runtime：官方 `grok 1.0.24 (68e414c661e3)`，`~/.grok/bin/grok` →
  `downloads/grok-1.0.24-macos-aarch64`；
- 曾部署的 patched 产物已移入废纸篓：
  `~/.Trash/xai-grok-pager-patched-b9f236e3-20260909`（勿再部署）；
- MacBridge `7ee42df` / iOS `88f89fcf` 的 `verifyingCompletion` 桥接与 UI 收口
  **保留**：解析的是官方已有字段（官方 `goal_orchestrator.rs` 在第二阶段就会发），
  在官方 runtime 下有效，只是触发晚。

## 来源清单

- grok-build checkout `/Users/jacklee/Projects/grok-build` @ `75810042`（origin/main，
  工作树干净）。注意：安装版 1.0.24 的发布提交 `68e414c661e3` 在镜像仓库不可解析，
  行号锚点以镜像 main 为准，与发布版可能有小幅漂移；
- MacBridge 工作树 `cordcode-macbridge-plan-approval`，分支 `plan/approval-layer` @
  `7ee42df`（另有 3 个未跟踪 handoff/docs md，与本文无关）；
- iOS 工作树 `cordcode-ios-plan-approval` @ `88f89fcf`（另有未跟踪
  `message-web/public/`）；
- 初衷对照文档：iOS 仓 `docs/[综合版对比]2026-08-04-t3code-vs-cordcode.md`。
