# 审计报告：2026-09-04 claudecode 官方能力升级 完成情况

- 审计对象：`docs/2026-09-04-claudecode-official-capability-upgrade-design完成情况.md`
- 方法：audit-plan skill（内容形状断言逐条对真实 dump 验证 + proof-carrying 断言抽查）
- 审计日：2026-09-05
- 来源清单（P0）：

```text
Mac 仓库=/Users/jacklee/Projects/cordcode-macbridge-claudecode-official
  分支=claudecode/official-capability  提交=faee0d128a28c4fe4acdd772ab8e0b93e14b6d74  未提交=干净
iOS 仓库=/Users/jacklee/Projects/cordcode-ios-claudecode-official
  分支=claudecode/official-capability-ios  未提交=干净（提交核验见下）
对照 dump=scripts/claudecode-phase0/（现行=2.1.261 复测覆写版 + git 31a5afe 原始版）
运行环境=PATH CLI 2.1.261（升级后）；Desktop 内嵌 2.1.260
```

## 1. 核心结论

**文档的内容形状断言几乎全部经得起真实 dump 复验（含双 CLI 代际双策略交叉），
proof-carrying 的存在性断言 100% 兑现；但抓到一个 P0：`agent/claudecode`
测试包在当前 HEAD 存在间歇性 panic（本审计 7 次全量运行 2 次 FAIL，引入
点 a200da6 的 `emitProtocolContextUsage` goroutine），使文档多处「全量回归
绿」断言在当前时点不可靠。**另有两处文档时效性问题（§1 归档表未同步
复测覆写、SessionStart 断言的 CLI 代际边界）与一处复测账本缺口。

文档的诚实质量总体是高的：§4 诚实清单的三项「未实施」被追记 5 真实
关账（源码 + 测试 + 生产日志三证齐全），假绿复盘（追记 3）自我揭发的
纪律也符合本仓 fail-closed 文化。

## 2. 逐内容类型验证表

| # | 文档断言 | 验证方法 | 结果 | 评级 |
| --- | --- | --- | --- | --- |
| 1 | 引用证据存在：17 个 Mac 提交、4 个 iOS 提交、两个工作树分支、§1 全部 dump/fixture 路径 | `git rev-parse` 逐个 + `ls` | 21/21 提交存在且主题吻合；dump/fixture 全在位 | 🟢 |
| 2 | 控制面六 subtype 全 success（§1） | 解析 main.jsonl：out 行 control_request ↔ in 行 control_response 按 request_id 配对（策略 A）+ success 计数（策略 B）；**双代验证**：现行（2.1.261 复测版）与 `git show 31a5afe` 原始版 | 两代均 8/8 success、请求响应零失配；六 subtype 家族与文档一致 | 🟢 |
| 3 | list_models 与 initialize.models 同构、无需先 init（§1） | README 结论 + main.jsonl req_1/req_2 载荷 | dump 在位，README 载明；未逐字段 diff（见 §3） | 🟢（抽查级） |
| 4 | hooks「4 事件 POST 原文」（§1） | 解析 hooks-posts.jsonl 的嵌套 body（str→JSON） | **31a5afe 原始版恰 4 事件**（UserPromptSubmit/Stop/ConfigChange×2/SessionEnd）；现行文件为 6 事件 9 行（复测加 Pre/PostModelSwitch 各 2） | 🟢（对原始版；见 P1-1） |
| 5 | env 矩阵「6 组合 × 真实 turn」+ 优先级结论（§1） | env-matrix.json 主表 6 组合逐行比对 | A/D/E/F→glm-5.3、B/C→glm-5.3-flash；E（settings 层 env）不生效、F（空串）=unset 两个细断言均实 | 🟢 |
| 6 | 网关改写 opus/sonnet→glm-5.3、haiku→glm-5.3-flash（§1） | env-matrix assistant.model 观测 + gateway-retest.json（cc-switch 15721 Bearer 200 / x-api-key 401、bigmodel 直连对照） | 双源吻合；gateway-retest 同时印证 cc-switch 认证语义 | 🟢 |
| 7 | transcript 形状锁 fixture 10 type（§2 Phase 4） | 数 fixture.jsonl type 全集 vs transcript_shapes_test.go 锁定枚举 | 10 type 一一对应（assistant/atis-latch/attachment/custom-title/last-prompt/mode/queue-operation/relocated/system/user） | 🟢 |
| 8 | client-uuid fixture 三点（追记 3） | turn-stream.jsonl：26 行=system 1+stream_event 22+assistant 2+result 1 | result 的 `user_message_uuid` = 探针构造的 `deadbeef-*` 值（发送值被采纳回盖的直接证明）；22 帧含 6 种打字机子类型（message_start→content_block_delta→…→message_stop） | 🟢 |
| 9 | get_context_usage 富载荷（追记 5） | context-usage fixture | maxTokens=200000、categories/autoCompactThreshold/messageBreakdown/skills 等 12+ 键，与 README「字段超出 SDK 0.3.260 类型声明」一致 | 🟢 |
| 10 | 追记点名测试存在 | grep 四个测试名 | TestRenameSession_* / TestOwnsClientTurnSelfSet / TestBackfillClaudeStreamTurnID / TestFileRelayProjectsExternalTurnWhileAgentRelayActive 全在 | 🟢 |
| 11 | 「全量回归绿」（追记 3/4/5） | `go test ./agent/claudecode/ ./core/ -count=1` × 7 次 | core 7/7 绿；**claudecode 5/7 绿、2/7 panic FAIL**（见 §4 P0） | 🔴 |
| 12 | 生产部署运行态（追记 5：PID 46939、11:33:40Z 构建） | lsof 8777 + ps 代际 | PID 46939（lstart 11:35:11）晚于构建；监听者=/Applications 内嵌 runtime；无违规残留；a200da6 ⊇ bc2d3d1（追记 4 修复已随部署） | 🟢 |
| 13 | §4 三项关账（PostModelSwitch / get_context_usage / iOS 直调） | 源码 grep + iOS 仓 | hooks_sink.go:130 订阅集含 PostModelSwitch（无 Pre ✅）；control_ops.go GetContextUsageLive；model_catalog.go ObserveModelSwitch；iOS 1ab76fc5 + ClaudeModelSelectorDirectSwitchTests.swift | 🟢 |

## 3. 未验证内容类型（无样本/未核验，如实列出）

- **list_models ↔ initialize.models 的逐字段同构 diff**：本审计只确认两代
  dump 在位与 README 结论，未做字段级 diff（文档断言源自 Phase 0 程序化
  比对；如需复核用 main.jsonl req_1/req_2 可直接重放）。
- **生产 PostModelSwitch 日志行**（追记 5 行为层验收引的
  `PostModelSwitch sessionID=93cd4a10 ...`）：本审计未从生产 go-bridge.log
  重取该行（验收引文 2026-09-05 12:20，文档自证 + 源码订阅集在位，接受
  为文档级，未独立复核）。
- **iOS 三键解码 3 单测 / conformance 3 用例的运行**：只核验文件与提交
  存在，未在 iOS 仓执行构建/测试（超出本审计时间盒）。

## 4. 脚本交叉验证注（含本审计自身的三次踩坑）

本审计验证断言 2 时，脚本字段路径三次猜错，全部由 dump 纠正——恰好反向
印证了 dump 记录的形状与 README「成功体嵌套提醒」节的自述：

1. label 行是探针 meta（`waiting_for: req_N`），非请求本体 → 请求/响应在
   `line` 字段（str，需二次 parse）；
2. control_response 的 request_id/success **不在顶层**，在
   `response.request_id` + `response.subtype=="success"`（双层嵌套）；
3. hooks body 是 JSON 字符串且事件名字段为 `hook_event_name`（非
   `hook_event`）。

修正后策略 A（配对）与策略 B（计数）在两代 dump 上结论一致。

## 5. 修订优先级

### P0（测试真实性，建议尽快处理）

**`agent/claudecode` 间歇 panic**：`go test ./agent/claudecode/ -count=1`
7 次运行 2 次 panic（其余 5 次全绿，时序敏感）。panic 链：
`handleResult`（session.go:1001）起 goroutine → `emitProtocolContextUsage`
（control_ops.go:220）→ `GetContextUsageLive` → `sendControlRequest`
（control_ops.go:201 nil deref）。引入提交：**a200da6**（追记 5 的
get_context_usage 升 A）。触发面：未走完整初始化的 session 对象在该
异步路径上解引用 nil。影响：①文档追记 3/4/5 的「全量回归绿」断言当前
不可靠（flaky）；②该路径与生产 handleResult 共用，建议加生命周期 nil
门或把发射推迟到 session 完整初始化后，并补一条**从 handleResult 入口
驱动**的测试（追记 3 的教训原话：凡有中间层的事件管线，集成测试必须
从中间层入口驱动——本例又应验一次）。

### P1（文档时效性，改文档即可）

1. **§1 归档表未同步复测覆写**：现行 `main.jsonl`/`hooks-posts.jsonl`
   已是 2026-09-05 的 2.1.261 复测覆写版（hooks 实为 6 事件 9 行），
   §1 仍按 31a5afe 原始版描述（「4 事件」）。README 复测节已注明
   「dumps 为 2.1.261 覆写，2.1.234 原始证据在 git 31a5afe」，完成情况
   文档应加同款一句，避免读者按 §1 数现行文件对不上。
2. **SessionStart 断言的代际边界**：README「--settings 层不触发」是
   2.1.234 实测；2.1.261 复测 dump 的 in 首帧出现 `SessionStart:startup`
   hook_started 帧。当前订阅集（hooks_sink.go:130）不含 SessionStart，
   行为无直接影响，但「不触发」结论已跨代际，建议在 README 升级节补
   一行 2.1.261 的复核结果（触发 or 不触发），订阅集设计曾依赖该结论。

### P2（账本完整性）

1. 追记 4 末尾「待 owner 复测」的外部回合场景（Desktop 发消息 → iOS 收
   正文+终态）无后续回报记录；追记 3 末尾 (a)–(d) 四项复测也未逐项关账
   （其中打字机流式已被追记 4 的复测确认 ✅）。建议补一行最终状态。
2. 「六 subtype」口径注记：实为 6 种 subtype × 8 个请求（set_model /
   set_permission_mode 各含 reset 对照轮）——文档表述无错，防止后续
   读者按 6 数请求对不上 dump。

## 6. 审计结论

完成情况文档的**证据体系成立**：存在性 100%、抽样的内容形状断言在两
代 CLI dump 上全部复验通过、诚实清单与关账记录相互印证、生产运行态
代际吻合。**放行条件 = 处理 §5 P0**（agent/claudecode flaky panic 修复
或至少在文档中撤回「全量回归绿」的当前有效性声明）；P1/P2 为文档修订，
不阻断。

## 7. 处置复核与闭环（2026-09-05，处置 f75a261）

审计方独立复核（不采信回执文字）：**三项全部通过，审计闭环放行。**

| 项 | 复核方法 | 结果 |
| --- | --- | --- |
| P0 修复 | 读 f75a261 diff（三重门 `!alive \|\| stdin==nil \|\| ctx==nil` 与回执一致；新测试从 handleResult 入口驱动且注释记录了修复前 7 跑 2 挂基线）；`go test ./agent/claudecode/ -count=1` **6/6 独立运行全绿**（修复前实测 7 跑 2 挂）；`go test ./go-bridge/... -count=1` 全量 ok | ✅ |
| P1-1/P1-2 | 追记 6 diff：§1 hooks 行补 dump 覆写注记；SessionStart 注记给出两层机制区分 | ✅（见下注） |
| P2 | 追记 6 关账记录（(a)(b)(c) 追记 4 轮确认、(d) 单测锁定如实标注无真机复测） | ✅ |
| 部署 | 只读核验：8777 = PID 10006（lstart 12:44:33，晚于 f75a261 12:43:42）、`/Applications` 身份、无违规残留、日志 12:44:33.583 `mgmt: claude hooks endpoint probe ok` | ✅ |

注（P1-2 精确化，审计方接受）：处置方复核指出审计所见 `SessionStart:startup`
帧是 **stdout 的 `system/hook_started` 用户层 hook 执行帧**（owner settings
的死 cc-event-hook 脚本），与 `--settings` 内联 HTTP hook 的 POST 层是两套
机制；HTTP POST 层 9 帧无 SessionStart（与本审计对 hooks-posts.jsonl 的观察
自洽）。「--settings 层 SessionStart 不触发」在 2.1.261 复核仍成立。该两层
区分已写入注记，防止后续再误读——审计原表述「需按新版复核」由此收口为
「断言成立，注记精确化」。
