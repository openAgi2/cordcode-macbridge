# 监工指令 10 号：Question recovery、strict tail 与 iOS 安装收口

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`  
> **依据**：audit-009 partial，采用 Path A hole-fill  
> **范围**：MacBridge targeted repair + iOS install（iOS 产品代码只在测试暴露真实缺陷时允许修）  
> **下发时间**：2026-08-21T01:35:48Z  
> **kind**：hole-fill

## 任务边界

保留指令 9 已验证的 C4 one-ingest、Todo 主修和 variant UI，不重新设计、不大范围重构。一次性完成以下三组尾项，提交一次报告后停止等待 audit-010。

## 1. Question source correlation 与 cold recovery

### Live asked

- `tool.messageID` 与 `tool.callID` 都必须非空；任一缺失整帧 fail closed，不进入 pending registry、不发 canonical event。
- 不得把“messageID 非空”当作 correlation。必须证明 `tool.messageID` 属于 asked 的 session，并通过 subscriber 的 message/turn facts映射到 owning turn；stale、other-session、unknown assistant message 全部 fail closed。
- 禁止直接用 `activeTurn(sessionID)` 作为无条件归属；它只能作为已由 tool.messageID 关联验证后的 turn fact，不能成为 fallback。
- 添加 destructive tests：missing callID、unknown messageID、other-session messageID、stale previous-turn messageID、正确 same-turn messageID。

### Pending/reload/reconnect

- 在 session route 建立/恢复时，用官方 GET `/question` 拉取 pending rows，并只处理目标 session。
- pending row 的 `tool.messageID/callID` 必须按同一 source-proven 规则映射；必要的 owning-turn 事实应来自同一次权威 hydrate/history transaction或已经验证的 live correlation。不得 ActiveTurnID fallback、不得新建 phantom turn、不得 raw second path。
- 将恢复出的 pending question 通过现有唯一 Kernel route投影一次；与同时到达的 live `question.asked` 必须按 interactionID 收敛为同一 part/revision 语义，不双 ingest。
- full-path owning tests必须覆盖：live asked、reply、reject、external resolution、断线重连、进程/adapter cold reload、GET+live race；每个 interaction 最多一个 pending part，resolved 原位更新，空/错 identity 零 projection。
- 如果 A7 history/GET 事实不足以完成 source-proven owning-turn 映射，触发停止线并带 raw/source 证据报告，不得猜。

## 2. Strict destructive tail

- Rename/archive/delete 只接受 evidence-proven HTTP 200；201/202/204/206/其他 2xx 全部失败。
- Archive response 的 `id` 与 requested session 一致，`time.archived` 必须与本次请求确认值一致；missing/zero/different timestamp 均失败。
- Delete 继续要求 bare `true` + by-ID 404 + scoped-list absence；非 200 即使 body=true 也失败，且不 signal catalog。
- `/agent` 每个 verified row 必须显式携带且类型正确：`name`、`description`、`mode`、`native`；删除缺 mode→primary 默认。可选字段只按同版本证据处理。
- `/provider` 顶层必须显式存在且类型正确：`all` array、`default` object、`connected` array；区分 missing/null 与合法 empty。connected provider/model required fields维持 fail closed，不恢复 map-key/recursive fallback。
- Todo row 若要求“exact `{content,status,priority}`”，则额外 alias/unknown key 也必须按 canonical 处理；至少新增 `{content,...,text:...}` destructive case，不能只拒绝缺 content 时的 text alias。
- 所有失败断言 prior state/catalog signal/timeline 均不被污染。

## 3. iOS 定向回归与真机安装

- 复跑 `ModelVariantSelectionTests` 与 `OpencodeWebVariantAgentWireTests`，不得全量 CCCodeTests，不得 UI tests/automation。
- 审计时 physical iPhone 已重新显示 `available (paired)`；按 iOS 仓规则由开发 agent 执行 `scripts/run.sh device --device <现场探测值>` 完成 Debug build/install/launch。不得把设备标识写入报告或文档。
- 安装仅验证产物和启动，不自动点击；owner UI 矩阵仍等待 audit-010。

## 4. 回归与报告

- Mac：新增 targeted tests、`go test -race ./agent/opencode-web`、`go test ./...`、vet/build；真实隔离 1.18.18 至少复跑 question/reload/mutation 场景。
- 保持 4096 owner-managed；回收 4398/4399；Mac 产品代码有变化则按规则重新 Release build/install并确认 8777 来源。
- 报告必须逐条列出 audit-009 剩余缺口、真实测试名/输出、HTTP status destructive matrix、Question GET/live race 的 Kernel revision/part 结果、iOS 安装结果和双仓 clean。
- exec-plan proven ≠ supervisor verified ≠ owner UI done；提交报告后停止。

## 停止线

1. A7 GET/history 缺少 source-proven cold owning-turn correlation；
2. 需要新增协议字段、第二 writer/reducer 或 ActiveTurn fallback；
3. first fix 不改变 cold reload/full-path result；
4. test/build hang 超出 watchdog 且无法收尾。

不得改 canonical、不得实现 E2/OD-3、不得恢复 fallback、不得进入 owner UI 矩阵。
