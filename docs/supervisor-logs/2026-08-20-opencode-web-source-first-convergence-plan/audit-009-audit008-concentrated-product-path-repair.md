# 监工审计报告 9 号（对应监工指令 9 号）

> **审计对象**：开发 agent 对监工指令 9 号的集中完成报告  
> **审计时间**：2026-08-21T01:35:48Z  
> **审计严格度**：严格（独立复跑 + 源码追踪 + 真实隔离 serve + runtime/device 核验）  
> **Verdict**：**partial**

## 0. 独立核实

- `go test ./go-bridge ./agent/opencode-web -run TestAudit008 -count=1 -timeout 5m`：PASS。
- `go test -race ./agent/opencode-web -count=1 -timeout 5m`：PASS，4.776s。
- `go test ./... -count=1 -timeout 10m`：全 PASS。
- `go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- iOS `ModelVariantSelectionTests + OpencodeWebVariantAgentWireTests`：11/11 PASS，0.073s；只执行 `CCCodeTests` selected tests，无 UI test execution。
- 独立启动隔离 OpenCode 1.18.18 serve（4398/4399），复跑 C4 A3/A4/E3 与 C6/C7 question/todo/permission/mutation：全部 PASS；4096 PID 23874 前后不变；4398/4399 已回收。
- 当前 8777 runtime 来自 `/Applications/CordCodeLink.app`；4096 仍由同一安装 app 管理。
- 双仓工作树在审计开始和复跑后均 clean。

## 1. Overall Verdict

Audit-008 的主缺口大部分已真实修复：active/passive 双 ingest reproducer 已到生产 Kernel 路径且独立复跑通过；unopened session 不再产生隐藏 projection；Todo 主要 alias/default/partial 行为已删除；iOS 两个配置入口确实消费同一 variant builder。Mac、iOS 定向测试和真实 sandbox 也均可复验。

但指令 9 号的 Question source-identity/reload 退出判据尚未完成，strict decoder/mutation 仍有少量明确宽松分支。开发报告把这些表述为已全部封死，属于覆盖面 overclaim；范围有界，可走同路线的 hole-fill，不需要重写 canonical，也不构成重新驳回整个批次的硬偏离。

## 2. 核实矩阵

| 检查项 | 独立结论 | 证据 |
|---|---|---|
| C4 active+passive single ingest | ✅ | `audit008_fullpath_test.go` 走 real adapter/Handlers/EventPublisher/Kernel；一次 source fact 投影 `ONCE` |
| unopened catalog-only | ✅ | 无 route、无 subscriber 时 reducer 无 snapshot；passive 只做 registry bookkeeping |
| Todo alias/default/partial | ✅ 主缺口已修 | `decodeTodoRows` 整体失败且不污染 lastTodos |
| Provider map-key fallback/config null | ✅ 主缺口已修 | connected model 必须自带 ID；config absent 与 null/wrong type 分开 |
| Mutation response/convergence | ⚠️ partial | boolean/list/by-ID 已有；仍未限制 exact HTTP 200，archive 未比较 echoed timestamp |
| Question live pending 到 Projection | ✅ | A7 asked full path 出现一个 pending part |
| Question source message correlation | ❌ | `events.go:745-756` 只检查 messageID 非空，TurnID 直接取 session `activeTurn`；不验证 tool.messageID 属于该 session/turn，callID 也未要求非空 |
| Question reply/reject/reload/reconnect full path | ❌ | 新 full-path test 只覆盖 asked→pending；reply/reject 仍停在 adapter/generic reducer，GET `/question` 只在 ResolveUserInput lookup 时使用，不能在重启/漏帧后重新呈现 dock |
| `/agent` strict required fields | ❌ | decoder 只要求 name；`descriptor()` 仍把缺失 mode 默认成 primary，未要求 evidence-proven description/mode/native |
| Provider exact top-level | ⚠️ partial | `all` 必须非空，但缺失/null `default` 或 `connected` 仍与合法 empty 混同 |
| Todo exact row | ⚠️ partial | required 三字段已严格，但未拒绝额外/alias key 与 required fields 同时存在 |
| iOS variant consumer | ✅ | UIKit/SwiftUI 都调用 `ModelVariantSelection.options`，状态/wire 定向测试 11/11 |
| capability final set | ⚠️ final set 正确，lag test 较弱 | test 使用 production derivation；bare-agent negative 只证明接口缺席，未覆盖 concrete opencode-web readiness，但不是本轮阻塞核心 |
| iOS physical install | ❌ 尚未完成 | 开发时点 unavailable；审计时 `devicectl` 已显示 physical iPhone `available (paired)`，supervisor 按权限不代装 |

## 3. 剩余缺口的源码证据

### 3.1 Question 仍不是 messageID-proven correlation

`handleQuestionAsked` 读取 `req.Tool.MessageID`，但逻辑只有：

1. `turnID := s.activeTurn(sessionID)`；
2. 判断 `turnID != "" && messageID != ""`；
3. 直接发出 `TurnID=activeTurn`、`ItemID=callID`。

已有 `messageIDs[messageID] → sessionID` 事实未参与校验，也没有 assistant-message→owning-turn 的稳定关联。一个 stale/mismatched non-empty messageID 会被挂到当前 active turn；空 callID 也会进入事件。这不满足指令 9 的“source-proven identity、无法 attribution 则 fail closed”。

### 3.2 GET `/question` 不是 Dock cold recovery

`fetchPendingQuestions/lookupQuestion` 只在 iOS 已经持有 interactionID 并调用 `ResolveUserInput` 时运行。进程重启或 asked SSE 漏帧后，iOS 根本没有 interactionID，也没有任何路径把 GET 返回的 pending row重新送入同一个 Kernel route。因此报告中的“GET /question 冷恢复保持同规则”和退出判据中的 reload/reconnect 尚无产品路径或 full-path test。

### 3.3 strict response 仍有可复现空洞

- Rename/archive/delete 仍使用 `code >= 400`，会接受 201/202 等 evidence 未证明的成功码；delete 报告声称覆盖 other-2xx，但 destructive test 只改变 body，没有设置非 200 status。
- Archive 只检查 `time.archived > 0`，没有断言与请求/服务器确认值一致；测试只有 missing archived，没有 wrong timestamp。
- `/agent` 的 `mode` 缺失仍默认 `primary`；description/native 的 presence/type 未验证。
- `/provider` 无法区分缺失/null `default`、`connected` 与合法空对象/数组。

## 4. 路线图偏离检查

| # | 判据 | 命中 | 说明 |
|---|---|---|---|
| 1 | 删除性测试 | 否 | 双 ingest referee 已通过 owner 归一化移除 |
| 2 | 三环归属 | 否 | 改动均属于 canonical C4/C6/C5/C7/iOS consumer |
| 3 | 待删特征 | 是（软） | activeTurn/default-primary/宽 2xx 仍是宽松归属 |
| 4 | 半接管 | 否 | active/passive timeline 双 owner 已修复 |
| 5 | 覆盖面诚实 | 是（软） | 报告称 reload、other-2xx、strict agent 全覆盖，源码/测试未达到 |
| 6 | 叙事真实性 | 否 | 明确区分 self-attested、supervisor、owner UI |
| 7 | 控制变量污染 | 否 | commits 范围与指令一致，双仓 clean |
| 8 | 根因门 | 否 | 修复基于 Audit-008 reproducer 和同版本样本 |

## 5. Partial 处理

- 采用路径 A：新开监工指令 10 号 `hole-fill`。
- 只补 Question source correlation/cold recovery、剩余 strict destructive cases 和当前已恢复可用的 iPhone 安装。
- C4、Todo 主修、variant UI 与 canonical 不重开设计；只跑必要回归防止破坏。

## 6. Three-Track done

- exec-plan proven：开发 agent 自述 yes，但本审计将相关 regression 降级并新增 review-fix triplet。
- 监工 verified：**partial**。
- owner 真机矩阵：pending；当前仍不下发 UI 矩阵。
- 三轨未全绿，产品尚未 done。
