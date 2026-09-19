# Codex Remote 三项命令面板方案样本审计

> 2026-09-09；审计对象：[实现方案 v1.2](../2026-09-07-codex-remote-slash-command-panel-implementation.md)。来源组合：Mac `c38b16e9b0d46adf2b1447c111d984749f78e390`、iOS `6a419322f165a99a0e722a93b5de0ded03831f47`、上游目标 tag `rust-v0.153.4`（`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`）。

## 1. 核心结论

架构判断成立：Compact、Plan、Goal 应分别走官方 typed API，并复用现有 attach、projection、菜单和 goal UI，不能发送 slash 字符串或 `command/exec`。但样本门尚不成立：现存真实 Remote fixture 只提供旧代际冷历史中的一个 `contextCompaction` item，以及 `thread/goal/cleared` 方法出现记录；没有当前目标 Remote 代际的三项请求/响应/完整通知/冷读闭环。故判定为 **有条件可执行**：可直接交给下一 agent 从 P0 开始，不可跳到功能实现或能力广告。

## 2. 按内容类型核验

| 内容类型/文档主张 | 本轮实际 dump | 评级 |
| --- | --- | --- |
| Compact request / 空 ACK | 目标 tag 源码定义 `{threadId}` → `{}`，但没有当前 Remote request/response dump | 🔴 无活体样本 |
| Compact live started/completed/turn 终态 | 目标 tag 测试展示同一 item id 的 started/completed，但这是 mock-server 源码测试；当前 Remote 无事件 dump | 🔴 无活体样本 |
| Compact 冷历史 `contextCompaction` | 旧 Remote fixture `attempt-009` 实际命中 1 条：`{"turnId":"id-358","item":{"type":"contextCompaction","id":"id-650"}}`；来源为 Desktop `26.825.41651` / CLI `0.151.0-alpha.7.1`，不是当前 0.153.4 目标 | 🟡 历史形状已证，目标代际待证 |
| `collaborationMode/list` | 目标 tag 源码响应为 `data[]`，元素含 `name/mode/model/reasoning_effort`；README 明确 built-in preset 不返回 developer instructions。当前 Remote 无 dump | 🔴 无活体样本 |
| Plan/Default `thread/settings/update` | 目标 tag 源码为 partial update、空 ACK；无当前 Remote request/response dump | 🔴 无活体样本 |
| `thread/settings/updated` 与冷启动当前 settings | 源码通知为 `{threadId, threadSettings}`；方案尚未证明 resume/read/update response 中哪一个能提供可靠冷基线 | 🔴 无活体样本/冷源未决 |
| Goal get/set/clear response | 目标 tag 源码确认 get nullable goal、set 完整 goal、clear `cleared`；当前 Remote 无 request/response dump | 🔴 无活体样本 |
| `thread/goal/updated` | 目标 tag 源码含 `{threadId, turnId?, goal}`；当前 Remote 无完整 notification dump | 🔴 无活体样本 |
| `thread/goal/cleared` | 旧 Remote fixture 只记录方法名出现，未保留 params；本地 0.149 mock JSONL 有 `{threadId}`，不能代替 Remote 目标样本 | 🟡 方法存在已证，字段路径未证 |
| 三项错误、乱序、并发与断线恢复 | 当前 Remote 没有 invalid/parent-owned/active-turn/ACK 丢失/Desktop 同写样本 | 🔴 无活体样本 |

## 3. 未核验内容类型

P0 必须补：C0 当前 controller/binary 代际；C1 attach/resume 及当前 settings 冷源；C2 compact request、空 ACK、started/completed、turn/error、冷拉；C3 list、Plan/Default update、settings updated、断线读回；C4 goal 空/非空 get、set/updated、clear/cleared、完整字段；C5 每项至少一条真实失败或未知结果，以及 Desktop 同时写后的权威收敛。每组保留 request、response、notification、冷读四类脱敏证据和顺序，不得用 standalone/mock/schema fixture 顶替。

## 4. 脚本交叉核验

- 对全部 `testdata/phase0/live/*.json`，策略 A（`jq .. | objects | select(.item.type == "contextCompaction")`）与策略 B（Node 递归遍历对象）均只找到 `attempt-009` 的 1 条，turn/item id 完全一致。
- 对旧 Remote fixture 中 `thread/goal/cleared` 字符串，jq 字符串递归与 Node 路径递归一致：attempt-008 为 1 次、attempt-009 为 1 次、attempt-010 为 2 个记录位置；这些是方法目录/观察摘要，不是 4 次完整 notification payload，不能据此推断 params。
- 对本地 `scripts/codex-web-phase0/dumps/reconnect/raw.jsonl`，jq 与 Node 都得到 `{method:"thread/goal/cleared", params:{threadId:...}}`；其 provenance 是 standalone 0.149 mock app-server，因此仅作为 decoder fixture，不计入 C4。

两种策略没有计数分歧。关键归因复核结果是：Compact 的唯一命中是 **一个冷历史 item**，Goal 的四个命中是 **字符串出现位置**，不能把单位误写成 live 操作次数或完整消息条数。

## 5. 修订优先级

- **P0**：采集 C0–C4 当前目标 Remote 样本；在此之前三项 capability readiness 保持关闭。
- **P0**：Plan payload 使用当前 thread model、preset mask，并显式发送 `settings.developer_instructions: null`；list 中缺该字段不表示 update 应省略。
- **P1**：确定 settings 的官方冷基线来源；若无法证明，Plan capability 不广告。
- **P1**：按同一 reducer 汇入 Goal response/notification 和 Compact live/cold item，保留 thread/turn/item/epoch 身份与乱序语义。
- **P2**：样本与定向单测通过后再做 iOS 菜单/表单；不运行未获授权的 UI、snapshot 或 simulator automation。
