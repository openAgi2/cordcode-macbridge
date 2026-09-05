# Claude Code backend 官方能力收敛升级 完成情况报告

- 日期：2026-09-04
- 计划：`docs/2026-09-04-claudecode-official-capability-upgrade-design.md`（v2.2）
- 队列状态：`.exec-plan/state/plan-62fb1a1b37fd.json`，31/31 todo done（proof-carrying）
- 结论：**Phase 0–4 全部实施完成；生产 runtime 已部署终版并通过运行态核验；
  行为层验收（真机矩阵）待 owner 执行**（见 §6）。

## 0. 来源清单（P0）

```text
Mac 仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claudecode-official
  分支=claudecode/official-capability
  提交=68f04affaa0f0fda0a3440f4d0771f849c10be60（终版；实施链 31a5afe→a863505→00d2e1e→09e1688→68f04af）
  未提交状态=干净（本报告与最终 state 入库于收口 commit）
  任务预期分支=handoffs/kickoff-20260904-claudecode-official-capability.md 指定的专用工作树 ✅
iOS 仓库路径=/Users/jacklee/Projects/cordcode-ios-claudecode-official
  分支=claudecode/official-capability-ios
  提交=74357a1d（mirror 同步；代码接线 8c707a1e）
  未提交状态=干净
CLI 版本锚（三段式）=PATH CLI 2.1.234（实测双锚：--version + system/init.claude_code_version）
  × Desktop 2.1.258（sessions/25813.json）× SDK 配对 2.1.260
预期产品特性=claude 目录真值化（三键）、会话内控制（set_model/set_permission_mode/interrupt 留进程）、
  hooks 事件层（自 spawn 会话）、文件面边界化（cordcode: 命名空间 + 形状锁）✅ 均已在产物中核验
生产部署=终版 Release（68f04affaa0f）已覆盖安装 /Applications 并重启；
  运行态核验：PID 88934 代际晚于构建、8777 新 PID、hooks 自检特征输出、健康端点 probeOk=true
```

## 1. 探针 dump 归档路径（Phase 0 硬门证据包）

`scripts/claudecode-phase0/`（commit 31a5afe 入库；对标 DSH_TURN_REPRO 供 CLI 升级复测）：

| 探针 | 归档 |
| --- | --- |
| 0.1 控制面六 subtype | `dumps/main.jsonl`（req_1–req_8 原文）+ `dumps/{bare-list,bypass}.jsonl` |
| 0.2 代际矩阵 | `README.md` 代际矩阵表（PATH CLI 2.1.234 × Desktop 2.1.258 分行） |
| 0.3 目录双 dump | main.jsonl req_1 vs req_2（程序化比对同构）+ bare-list 对照 |
| 0.4 cap 探测 | `dumps/turn.jsonl`（system/init capabilities 原文） |
| 0.5 hooks | `dumps/hooks-posts.jsonl`（POST 原文）+ `dumps/fixture-hook-silent-failure.jsonl`（S3） |
| 0.6 env 矩阵 | `dumps/env-matrix.json`（6 组合 × 真实 turn） |
| 0.7 网关复测 | `dumps/gateway-retest.json` + gateway-raw-*.json |

> **dump 覆写注记（audit P1-1）**：本表 hooks 行按 31a5afe 入库时（CLI 2.1.234，
> 4 事件 POST）描述；现行 dumps/ 已被 2026-09-05 的 2.1.261 复测覆盖写（9 行 POST：
> UserPromptSubmit / Stop / ConfigChange×2 / Pre×2 / Post×2 / SessionEnd）。
> 2.1.234 原始证据在 git 31a5afe。**「--settings 层 SessionStart 不触发」在
> 2.1.261 上复核仍成立**（HTTP POST 层 9 帧无 SessionStart）；复测流里的
> `SessionStart:startup` 出现于 stdout `system/hook_started|hook_response` 帧——
> 那是用户层 hook（死 cc-event-hook.sh 等）的执行帧，与 `--settings` HTTP hook
> 是两套机制（README「顺带取证」节，两代 CLI 同象），勿混淆。

结论要点：六 subtype 在 2.1.234 全 success；list_models 与 initialize.models 同构且无需先 init；
resolvedModel=canonical（cc-switch 别名不改目录）；capabilities 无 modelCatalog 但 list_models 可用；
`--settings` 层 SessionStart 不触发、ConfigChange 要求文件预存 cascade；模型选取优先级
`--model > 进程 env（仅 canonical id）> settings.model > settings 层 env`；网关改写
opus/sonnet→glm-5.3、haiku→glm-5.3-flash。全部回填设计文档 §3.4 与 CLAUDE.md。

## 2. 各 Phase 交付（证据分级：self-attested / re-verified）

| Phase | 交付 | 关键证据 | 分级 |
| --- | --- | --- | --- |
| 1 模型目录真值链 | initialize.models 主源 + list_models 刷新 + message.model 观测三键 + S7 网关分类 + 双头拉取；wire `resolved`/`observedModel`；iOS 三键解码+真值优先副标题 | 13 单测（fixture=真实 control_response）；iOS 3 单测；protocol pack 双仓同步 | tests re-verified（收口重跑全绿） |
| 2 会话内控制 | set_model 直达（fail visibly live_model_switch_failed）/set_permission_mode 受限四档（本地模拟仅缺位回退）/interrupt（S8 裁决 a：停回合留进程，interrupt_receipt_v1 能力门）；iOS BackendModelSetting conformance | 10 单测；system/status 反向同步；冲突表四行语义保持 | tests re-verified |
| 3 hooks 事件层 | Management `/internal/hooks/claude/{token}`（路径 token）；--settings 内联 5 事件（PermissionRequest/SessionStart 按证据缺席）；Stop→relay nudge 定向刷新；ConfigChange→目录刷新；心跳自检门控 + `GET /internal/hooks/claude/status` 如实上报 | 8 单测（真实 Stop POST fixture）；**生产日志特征输出 probe ok + 健康端点实测**（运行态级） | tests re-verified + 运行态自证 |
| 4 文件面边界 | 真实 transcript 形状锁 fixture（10 type）；custom-title 加 `cordcode:` 命名空间（读取双接受）；rename_session 对照结论（实测 success、有意不迁移）；不删文件面代码 | 3 单测；类型枚举锁 | tests re-verified |

S8 裁决（owner 2026-09-04，AskUserQuestion）：**方案 (a) interrupt=停回合、留进程**，已回填设计 §6 Phase 2.4。

## 3. 退出审计与修复

收口前内部审计（exec-plan start-exit rule）重跑测试组抓到 2 处回归，均已修复并全量重跑绿：

1. `TestRenameSession_*` 锁定旧 `custom-title` type → 更新为命名空间 type（行为本就要变）。
2. `/internal/status` 加 claudeHooks 破坏 R11 v0 observed 契约（5 string 字段 + golden fixture 逐字节）→ 健康状态挪独立 `GET /internal/hooks/claude/status`，v0 契约字节稳定。

## 4. 诚实清单：未实施项与原因

| 项 | 原因 |
| --- | --- |
| 真机行为层验收（模型列表三键、会话内切换即时生效、停止留进程、Stop 定向刷新感知） | 需 owner iPhone 操作（UI automation 需显式授权）；本任务完成代码 + 单测 + 生产部署运行态核验（进程代际/特征输出/健康端点） |
| iOS 选择器直接调用 switch_model（状态同步） | 设计 §7.3.2 分期："adapter 接线完成后再谈"；conformance 已就位，现有 send_message.model 路径已触发 Mac 侧会话内直达 |
| rename_session 活会话迁移 | 对照后有意不迁移：控制帧只能达存活会话，append 写入对任意历史会话可用（结论与迁移前置条件已文档化于 session_mutation.go） |
| Managed 层 hooks 注入 | M4 owner 裁决默认关；本机无落点（/Library/Application Support/ClaudeCode 不存在） |
| PostModelSwitch 接入 | CLI 2.1.234 文档级不存在（需 ≥2.1.251，设计 §3.2）；升级 CLI 后用入仓探针复测 |
| SessionStart 事件订阅 | Phase 0 实证 `--settings` 层不触发该事件（2.1.234 边界，非遗漏） |
| `get_context_usage` 升 A | 设计 Phase 4.3 标候选（非本期阻断） |

## 5. 提交清单

Mac（claudecode/official-capability）：`31a5afe` Phase 0 证据包 → `21a8b85` Phase 1 真值链 →
`432baf0` Phase 1 文档 → `a863505` Phase 2 控制 → `ee46068` Phase 2 文档 → `00d2e1e` Phase 3
hooks → `4b506ce`/`92f9ba1` 文档 → `09e1688` Phase 4 边界 → `68f04af` 审计修复。
iOS（claudecode/official-capability-ios）：`8c707a1e` Phase 2 接线 → `1380f052`/`74357a1d` mirror。

## 6. owner 真机验收矩阵（待执行）

| # | 前提 | 动作 | 应看到 |
| --- | --- | --- | --- |
| 1 | iPhone 连接 Mac，新建 Claude 会话 | 发一条消息，打开模型选择器 | 列表显示官方槽位（default/opus[1m]/sonnet/sonnet[1m]/haiku），副标题显示 canonical + 实际执行模型（如 `claude-sonnet-5 · 实际 glm-5.3`）；haiku 无思考档位 |
| 2 | 会话进行中 | 选择另一个模型后立即发消息 | 模型即时切换（无「下次会话才生效」），Mac 日志可见 set_model 往返 |
| 3 | 回合运行中 | 点「停止」 | 回合立即停止、会话不中断，紧接着再发消息直接继续（无需重启等待） |
| 4 | 会话收口一条消息后 | 观察 Mac go-bridge.log | `claude hook: Stop → targeted refresh` 日志行（事件驱动刷新） |
| 5 | 会话内改权限档（如 acceptEdits） | iPhone 设置权限模式 | 当前会话即时生效（appliesTo=current_session） |

> 逐行回报 ✅/❌ + 现象即可；#4 也可由 agent 代查日志。

## 7. owner 真机验收结果（2026-09-04 22:47，修复后部署 cf7ef6e）

| # | 结果 | 说明 |
| --- | --- | --- |
| 1 模型列表三键 | ✅（瑕疵豁免） | 官方槽位列表正确、观测名（glm-5.3）显示；resolved 前缀未显示——owner 裁决"不是核心问题，可以不修" |
| 2 会话内切模型即时生效 | ✅ + 已修复 | 即时生效确认；截图（glm-vision 代阅）证实"一堆提示"=0 条错误，实为 3 条 CLI 命令回显 XML 气泡（`<local-command-*>`），系 live 投影路径缺归一化（冷历史已有）——commit cf7ef6e 修复（导出 NormalizeClaudeUserText 并接入 live 投影两处）+2 回归测试+已部署。注意：已入库的旧 XML 行不回改，重测请再切一次模型看新行 |
| 3 停止=停回合留进程 | ✅ | 符合预期（S8 裁决 a 行为） |
| 4 Stop hook 定向刷新 | ✅（agent 代查） | 本代 runtime 日志 4 次 `claude hook: Stop → targeted refresh`（22:43–22:57，owner 测试会话 f4c4439b） |
| 5 权限档即时生效 | ✅ | 符合预期 |

验收矩阵 5/5 通过（#1 带 owner 豁免项；#2 修复后待 owner 复测一次）。

### 追记：第二轮复测发现深层回归（已修复 fdc27c5，2026-09-04 23:34 部署）

owner 复测确认命令回显气泡消失，但发现新症状：切模型后的下一回合整回合丢失
（问题 B 气泡消失、回复 B 接在回复 A 后）。生产日志 + kernel 复现测试双证根因：
上轮修复（cf7ef6e）让 caveat/<local-command-stdout> 行归一化后零事件，但它们仍按
内容行进 kernel 内容 transition——kernel 拒绝零事件 transition 且拒绝不推进
ledger cursor，后续所有行以 "Claude source batch gap" 全拒（23:17:15 生产日志
四连 rejected），投影停在 A 回合；iOS 只剩 stdout live 流，表现为 B 丢失错位。

修复（fdc27c5）：纯回显行（isClaudeEchoOnlyUserRow：全部 text 块归一化后为空且
无 tool_result）按非内容行路由（仅推进 cursor）。复现测试用真实 transcript 行序列
同形驱动，修复前三回合 gap 全拒、修复后 A/“/model haiku”/B 三回合结构完整。
runtime 重启后 kernel 内存态重建，受影响会话重开即自愈。

### 追记 2：第三轮复测「无流式/回复重复」——官方消费模型对齐（d5f5e30，2026-09-05 00:52 部署）

owner 复测确认归位修复生效；新反馈「发消息后空白几秒→一次性加载→回复B回复B→去重、无流式」。
取证（93cd4a10 transcript + 生产日志）三层根因：

1. 「等几秒」主因＝**bigmodel 429 速率限制重试 68 秒**（transcript 六连 rate_limit_error 行，
   15:50:37→15:51:11，上游限流，非 CordCode bug）；次因＝resume history drain 固定 10s 超时
   （既有机制）。
2. 「无流式」＝架构缺口：claude stdout 流式增量（CLI stream_event）无 uuid 身份（CLI 不回显
   user 帧），SSV2 reducer 跳过无身份 delta——iOS 只能等 file-relay 完成态整段（3s 粒度）。
3. 「回复重复」＝stdout 流与 file-relay 完成态行**双源向同一 item 各 append 一份**。

修复（对照 Agent SDK 官方消费模型：stream_event 打字机 + 完成帧差量收口）：
- relayEvents 给无身份流式 delta 补 kernel.ActiveTurnID（file-relay user 行建立的 turn 身份）
  → 流式增量经 IngestLive 进投影 patch（打字机）；完成帧 session 侧已有流式差量。
- 官方单源：agent relay（stdout）活跃时 file-relay 的 assistant 行退为 cursor-only（不双份）；
  user 行照常建 turn 身份；外部会话保持 file-relay 全量。
- 测试 5 例新增/更新（含真实 429 行集消费、双源不重复断言）；全量回归绿。

待 owner 复测：新建会话发消息（本地正常网关时）应看到打字机流式、无重复、无整段跳变。
已知边界：上游 429 限流期间的等待是官方行为（CLI 自身也在重试）。

### 追记 3：第四轮复测「无流式且完成态无内容」——假绿复盘与官方 client uuid 身份链（2026-09-05）

owner 复测 d5f5e30：全程无流式；完成态不加载正文，切走再切回才显示。调查（01:03 会话
93cd4a10 生产日志 + transcript + 源码 + CLI 真实探针）结论——**d5f5e30 部署的是一组
"测试绿、生产挂"的修复**：

1. **deltaBatcher 丢 turnId（假绿主犯）**：relayEvents 补的 turnId 经 33ms 攒批器时被
   emit() 重组丢弃（只保留 delta+itemId）→ 出批后又是无身份增量，reducer 照旧跳过。
   d5f5e30 单测直连 kernel.IngestLive 绕过攒批器 → 测试全绿，生产 stdout 流量 1335 帧
   全部无效。教训已入 think.md：凡有攒批/重组中间层的事件管线，集成测试必须从中间层
   入口驱动。
2. **双序号域幂等门（第二层，本轮新发现）**：Claude source batch 在 Kernel 锁内自取
   PerSessionSeq，live 事件走 publisher 独立计数器——两域打同一 reducer 幂等门
   （seq ≤ rev 即跳）。file-relay user 行 batch 先行推高 rev 时，后到的 stdout 流式
   delta 即使带身份也会被静默跳过（间歇性）。修复 = Kernel 每 session 原子发号器
   （IssueSessionSeq）作为唯一取号源，publisher 与 batch 都从它取号。
3. **drain 固定 10s（自加延迟）**：CLI 2.1.234 真实探针证明 `--resume` 不重放历史到
   stdout（重放帧是完整 assistant/user 帧，且实测为零）；handleSendMessage 里同步
   drainHistoryEvents 的 10s 等待纯属白等。移除 claudecode 的同步 drain，drain 窗口改
   事件驱动（首条 stream_event 即关，重放防御语义保留，12s watchdog 兜底仍在）。

**官方机制采纳（方案一，owner 2026-09-05 拍板）**：Claude Agent SDK 的 client uuid 契约
（`user_message_uuid`）——消费者提交 user 帧自带 uuid，CLI 写进 transcript user 行并在
result 帧回盖。真样本（2.1.234 探针，fixture 已入
`agent/claudecode/testdata/client-uuid/turn-stream.jsonl`）实证三点：输入帧 uuid 被
transcript 原样采纳；result 恒带 user_message_uuid；stream_event 序列即官方流式面。
t3code 对照（owner 问询）：其 Claude 集成不是 ACP，是官方 npm SDK 内嵌 Node + 发起方
自造 turnId + 自有 DB 做 SoT——身份模型与本修复一致。

实施（commit 链见 §5 追记）：Send 自造 v4 uuid 写入输入 user 帧（active+FIFO 队列记账，
writeJSON 失败回滚）；stdout 全事件面（流式/思考/工具/完成差量/收口）绑 client uuid 作
TurnID；result 按 user_message_uuid 对账消费队列（无 stamp 保守清空防串位）；
deltaBatcher 透传 turnId（不同 turn 不合并）；mapAgentEvent TextReplace/result 非 Done
分支透传 turnId。ActiveTurnID 反查降级为兜底（无 uuid 路径）。iOS 零改动（text_delta
的 turnId 是既有 wire 字段）。

测试：batcher 3 例（透传/不跨 turn 合并/同 turn 合并）；agent 6 例（帧盖章/流式身份/
官方 stamp 收口/FIFO 队列/无 stamp 清空/drain 事件驱动）+ 真样本 fixture；
TestBackfillClaudeStreamTurnID 改造为经 deltaBatcher→publisher→kernel 生产全链路（正是
它先抓到双序号域断点）；drain 旧语义测试按真样本证据更新。全量回归绿（go-bridge +
agent/claudecode + core）。

待 owner 复测：会话发消息应看到（a）发送后不再有固定 ~10s 空白；（b）逐字打字机流式；
（c）完成态正文即时可见、无需切回；（d）连续两条消息排队场景不串回合。

### 追记 5：三项候选收尾（2026-09-05，owner 指令当日完成）

§4 诚实清单中的三项「未实施」当日交付（CLI 升级评估同时执行）：

1. **CLI 升级 2.1.234 → 2.1.261**（Mac commit a200da6）：隔离安装 → 探针复测六项全绿
   （控制面六 subtype / get_context_usage 三模式 / Pre+PostModelSwitch 实证触发 /
   既有 hooks 照常 / changelog 无相关破坏 / transcript type 集与形状锁零新 type）→
   全局升级。证据：`scripts/claudecode-phase0/README.md` 2026-09-05 节。
2. **PostModelSwitch 观测接入**（a200da6）：hooks 订阅集 +PostModelSwitch（Pre 有阻塞
   语义不订）；`to_model`（网关改写观测名）经 `Agent.ObserveModelSwitch` 进目录
   observed 层；订阅集与分发各有单测。**§4 该行从「CLI 2.1.234 文档级不存在」变为
   已接入**。
3. **get_context_usage 升 A**（a200da6）：自 spawn 会话每 turn 收口后异步
   detail=summary 官方全量窗口占用（含 system prompt/tools/memory 分类 + 官方
   maxTokens），成功覆盖流帧 usage 近似值，fail closed；真样本 fixture 驱动 3 组
   单测。**§4 该行从「候选（非本期阻断）」变为已交付**。
4. **iOS 选择器直调 switch_model**（iOS commit 1ab76fc5）：门控 = 会话已建立 +
   backendKind==claudeCode + BackendModelSetting conformance；nil 选择 = default
   重置；失败不回滚（send_message.model 兜底）。3 用例模拟器全绿。**§4 该行从
   「设计 §7.3.2 分期」变为已接线**。

生产部署：Release（runtime a200da6，11:33:40Z 构建）已覆盖安装 /Applications，
运行态核验（PID 46939 代际晚于构建、8777 新 PID、内嵌 get_context_usage 特征
符号、hooks probe ok、无违规残留）。

**行为层验收（owner 2026-09-05 12:20 真机通过）**：① iPhone 选择器切换模型——
生产日志同刻出现 `PostModelSwitch sessionID=93cd4a10 requested=sonnet
from=glm-4.7 to=glm-5.3-flash[1M]`（observed 层实测记录网关真实改写，含 1M
变体）；② 上下文点击后出现系统/工具/记忆全量口径 ✅。三项候选收尾全部关账。

### 追记 4：第五轮复测「外部回合被压制」——单源门按回合发起方收窄（2026-09-05）

owner 复测 f4027ca：**iOS 发消息打字机流式 ✅（主链路修复确认）**。两个新现象：

1. **Mac Desktop 不实时显示 iOS 发的消息**（重启 Desktop 才见）：非 CordCode bug。
   Claude Desktop 不监听 transcript 的外部写入（官方无跨进程事件总线——正是我们
   产品需要 file polling 旁观 Desktop 的原因，方向反过来同理）。数据已持久写入
   会话文件；Desktop 侧重开会话即可见，无需重启整个 App。
2. **Mac Desktop 发消息（问题 2）→ iOS 收到问题、卡「执行中」永远无回复**：回归，
   根因是 d5f5e30 的 stdout 单源门 `agentRelayActive(sessionID)` 过粗——按**会话
   存活**而非**回合发起方**判定。本进程（iOS 侧 CLI 子进程）idle 存活期间，Desktop
   worker 写同一 transcript 的外部回合不经本 stdout，assistant 行却被「stdout 权威」
   压制为 cursor-only → iOS 无正文、无终态（heldTerminals 也不触发），永远执行中。

修复：门收窄为 `agentRelayActive && agentOwnsClaudeTurn(currentTurnID)`。新增
core.ClientTurnOwner 可选接口（agent/claudecode 实现自持 client uuid 集——
register 添加 / settle 后保留（晚到的同 turn 文件行仍属 stdout 权威）/ rollback
移除）；file-relay 用 file-order currentTurnID（user 行 uuid）查询归属，外部回合
照常全量供正文与终态。无会话对象/未实现接口判非自有（file-relay 供内容，安全方向）。

测试：新增 TestFileRelayProjectsExternalTurnWhileAgentRelayActive（owner 复测场景
回归锁：外部回合在 agent relay 活跃时正文 + turn_completed 均达 iOS）+
TestOwnsClientTurnSelfSet（自持集四态）；既有 cursor-only 测试改为声明自有回合。
全量回归绿。

### 追记 6：审计回执——P0 panic 修复 + P1/P2 文档关账（2026-09-05）

审计报告：`docs/2026-09-05-audit-2026-09-04-claudecode-official-capability-upgrade-design完成情况.md`
（8e8082e）。证据体系结论「成立」；以下为逐项处置：

**P0（阻断，已修复）**：追记 5（a200da6）引入的 `handleResult → go
emitProtocolContextUsage → sendControlRequest` 在半初始化 session（测试直接驱动
handleResult：无 stdin / 无 ctx）上 nil 解引用 panic——实测复现 3/6 FAIL，栈坐实
`writeJSONContext` 对 nil `cs.stdin`（session.go:1406）。修复 =
`emitProtocolContextUsage` 生命周期门（`!alive || stdin==nil || ctx==nil` 短路；
真实 spawn 会话恒过门，生产行为不变）。**补测按审计建议从 handleResult 入口驱动**
（中间层入口纪律第三次应验）：① bare session 不 panic（无门控时 goroutine 微秒级
击穿二进制）；② 全链路正向（handleResult → goroutine → 控制帧 → pipe 回真样本
fixture → `EventContextUsageUpdated` + lastUsage 被官方真值覆盖）。修复后 **10/10
全量绿**。附带发现：handleResult 自身对 nil `cs.ctx` 也有同步 panic 路径
（`select` 求值 `cs.ctx.Done()`），仅影响更裸的测试构造，门控同批覆盖。

**P1-1（已改）**：§1 表 hooks 行补 dump 覆写注记（见上）。

**P1-2（已复核，断言成立）**：见 §1 注记——HTTP POST 层 SessionStart 在 2.1.261
仍不触发；审计所指「SessionStart:startup 帧」为 stdout 用户层 hook 执行帧，两套
机制。

**P2（已关账）**：追记 3「待 owner 复测」（a）（b）（c）由追记 4 轮 owner 复测
确认（「iOS 发消息打字机流式 ✅ 主链路修复确认」——无固定空白/流式/完成态即时
一并覆盖）；（d）排队场景未显式复测，FIFO 队列语义由 `client_uuid_test` 单测锁定，
无回归报告。追记 4 两现象：现象 2（外部回合压制）owner 三场景复测 1-3 全过
（bc2d3d1 部署后）；现象 1（Desktop 不实时）确认为官方限制非缺陷（think.md 收口）。
