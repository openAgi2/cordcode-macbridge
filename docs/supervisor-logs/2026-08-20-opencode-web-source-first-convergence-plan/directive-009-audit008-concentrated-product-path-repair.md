# 监工指令 9 号：Audit-008 集中产品路径补洞

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`  
> **依据**：`audit-008-concentrated-c2-c7-implementation.md` + owner 明确选择方案 A  
> **范围**：MacBridge + iOS 双仓  
> **下发时间**：2026-08-20T18:48:54Z  
> **kind**：hole-fill

## 任务

保持 canonical mapping 和产品范围不变，用一个集中补洞批次修复 Audit-008 的产品路径缺口。允许内部按下述 Wave 使用 reviewable commits 连续推进；除非触发停止线，不在每个功能后等待监工。全部实现、full-path owning tests、安装和自审完成后只提交一次报告，等待一次独立 re-audit。

本指令不要求重新调研 OpenCode，也不授权改写 canonical。A1–A10、E1b/E4b/E5b 与 pinned 1.18.18 source 仍是唯一外部 shape 事实源。

## Wave 0 — 先把失败变成可复验门禁

在修改行为前增加能够在当前错误实现上失败的 owning tests：

1. **C4 full-path double-ingest reproducer**：用真实 `Handlers + deltaBatcher/EventPublisher + ProjectionKernel` 同时建立 opencode-web active session route 与常驻 passive subscription，注入一条 normalized direct SSE fact，证明当前会发生两次 ingest/revision/part mutation。不得只测 subscriber channel 数、网络 dial 数或 adapter event 数。
2. **C6 Question full-path reproducer**：回放 A7 的真实 `tool.messageID/callID`，覆盖 `handleRawEvent → mapAgentEvent → EventPublisher/Kernel → Projection`，在修复前证明 `user_input_requested` 被 identityless drop；测试终点必须是 projection 中出现且只出现一个 pending user-input part，而不是 adapter `core.Event`。
3. **Strict-shape destructive set**：为 Todo endpoint/event、provider/config、rename/archive/delete 建立当前宽松实现必失败的负向用例，逐项覆盖 Wave 2 的 exact rejection 条件。
4. **iOS consumer test**：在不跑 UI automation 的前提下，为现有 UIKit menu 与 SwiftUI model configuration 建立可测试的 variant menu/action consumer；当前没有 selector 时测试先失败。

测试可以先以明确 expected-failure/红灯 commit 落盘，但不得把红灯状态标 proven；随后连续进入修复。

## Wave 1 — C4/C6 truth-owner 修复

### 1.1 C4 唯一 timeline owner

- 一个 backend instance 仍只允许一条 `/global/event` 网络订阅。
- 对任一 `(directory, sessionID, source fact)` 只能有一个 production timeline delivery owner 和一次 `EventPublisher/Kernel` ingest。
- registered/open session 走其唯一 active Kernel route；passive path 不得把同一 content/status/tool/question/todo event再次送进 `deltaBatcher`。
- unopened external session 只允许刷新 catalog/control metadata；不得建立隐藏 timeline、raw broadcast 或第二 Projection。之后打开时由 hydrate 收敛。
- 多连接观察同一个 session 时共享同一 Kernel/route，不得因 route channel 数量重复 ingest。
- reconnect 继续使用同一 subscriber、同一 route、同一 Kernel epoch；A3/A4/A5 终态语义不变。
- 删除或封死造成双 owner 的 active/passive referee 路径；禁止用 event hash、时间窗、revision 比较或下游 dedupe 掩盖双写。

在 C4 full-path test 变绿前，`external_turn_streaming` 必须视为 unavailable；最终只能在 subscriber-count + active/passive single-ingest + unopened catalog-only + reconnect-same-Kernel 四类测试同时通过后恢复广告。

### 1.2 Question 的 source-proven identity

- 扩展 A7 decoder 保存真实 `tool.messageID` 与 `tool.callID`；不得从当前 active turn、数组位置、时间或随机值猜 identity。
- 使用现有 subscriber correlation 将 source `messageID` 归属到真实 owning turn；`callID` 作为 interaction/tool item correlation。任何无法完成 source-proven attribution 的 asked frame fail closed 并可诊断，不能投影 phantom turn。
- `question.asked` 只生成一次 canonical `user_input_requested`；`question.replied/rejected` 只原位 resolve 同一 interaction；禁止 legacy question frame 回灌、raw second path 或 `messages[]` writer。
- GET `/question` 冷恢复必须保留同一 identity 规则；如果同版本 pending response 缺必要 tool identity，与 A7 证据冲突则触发停止线，不得 fallback 到 ActiveTurnID。
- owning test 必须覆盖 asked、reply、reject、external resolution、reload/reconnect，并断言 projection revision/part 各只更新一次。

在 full-path Projection test 变绿前，`structured_user_input_v1` 必须视为 unavailable；不能以 adapter interface 存在作为广告依据。

## Wave 2 — 所有 strict decoder/mutation fail closed

### 2.1 Todo

- endpoint 与 `todo.updated` 只接受 A8 已证明的 ordered bare list；每行必须同时存在且类型正确、值非空：`content`、`status`、`priority`。
- 删除 `text` alias、`pending/normal` 默认值、坏行 `continue`、partial replacement 和旧缓存复活。
- 任一 malformed row 使整次 replacement 失败；不得修改 lastTodos，不得发部分 `EventPlan`，并输出可诊断错误。Todo 仍是 control plane，不进入 SessionProjection timeline、不造 ID。

### 2.2 Provider/config/agent selection

- `/provider` 顶层及 connected provider/model required rows 严格按 E4b；删除 provider/model silent skip 和 model map-key ID fallback。未连接 provider 可被过滤，但它的物理 row 不能用猜测补齐。
- `/config` 必须区分 evidence-proven `model` key absent 与 `model:null`/非 string/空 string；只有 key absent 表示没有 configured model，其余未证明 shape fail closed。
- 保持 §6.6 已钉死的 provider-default-over-config 选择顺序、connected validation、zero POST 和 variant live-key gate；不得恢复递归解析或 server-side implicit selection。
- `/agent` 同样保持 strict required-field 语义；新增负向测试不能只覆盖 happy bare array。

### 2.3 Rename/archive/delete

- Rename/archive 只接受有效 Session.Info：response ID 必须存在并与请求 session 一致；rename title、archive `time.archived` 必须与服务器返回事实一致。删除“缺 ID 时回填请求 ID”。
- Delete 只接受 evidence-proven HTTP 200 bare boolean `true`；`false`、null、object、empty、其他 2xx 或 malformed body 都失败。
- Caller delete 的完成条件是成功 response + authoritative list absence + by-ID 404；不得只 signal catalog 后立即声称成功，不得等待或制造 `session.deleted`。
- mutation 任一步失败都保留原 metadata/handle，并返回真实错误；HTTP/catalog 路径不得写 timeline。

在对应 strict/full-path tests 变绿前，`todos`、`session_mutation`、`session_delete` 均不得向 iOS 宣称 available。禁止为 opencode-web 加永久 backend-id 例外来伪造 readiness；若现有 interface-derived capability 机制无法诚实表达 lag gate，暂停并报告所需的最小通用 readiness 机制，不能自行扩展协议。

## Wave 3 — iOS model variant 真实 UI consumer

- 在现有模型配置 UI 中接入 variant selector：UIKit `makeModelConfigurationMenu` 路径和 SwiftUI `ChatGPTModelConfigurationPopover` 路径必须行为一致，尽量复用同一个纯数据/action builder，避免两套语义漂移。
- 仅当当前 selected model 的 live `variants` 非空时显示；选项包含“自动/未设置”与 catalog keys，选中状态与 `selectedModelVariant` 一致。
- 切换 model 后清理不属于新 model 的 variant；catalog refresh 删除当前 key 时归 nil；发送时只有合法非空选择进入 `send_message.params.model.variant`。
- variant 不得与 reasoning effort 合并或互相覆盖；不新增 backend 白名单、placeholder variant 或 guessed label。
- 定向测试至少覆盖：无 variants 隐藏、non-empty 显示 exact keys、选择/清除、换模型归零、catalog key 消失归零、UIKit/SwiftUI consumer 使用同一 selection contract、最终 wire 包含/省略正确。
- 不运行 XCUITest、snapshot、simulator automation；允许普通 `CCCodeTests` 定向 unit test。

## Wave 4 — capability 恢复、集中回归和安装

1. capability 必须以完整 consumer path 为 gate，而不是“实现了 Go interface”：
   - `external_turn_streaming`：C4 one-ingest/full-path 绿；
   - `structured_user_input_v1`：A7→Kernel→Projection→iOS consumer 绿；
   - `todos`：strict endpoint/event + control consumer 绿；
   - `session_mutation`/`session_delete`：strict response + authoritative convergence 绿。
2. capability negative-before-positive test 必须证明未满足 owning gate 时 absent；最终 real agent 的 advertised set 才恢复。permission、attachments 等本次未驳回能力不得被误伤；E2 reasoning、legacy `question_reply`、OD-3 继续 absent。
3. 复跑：
   - Mac relevant targeted tests、`go test -race ./agent/opencode-web/`、`go test ./...`、`go vet`、`go build ./...`；
   - iOS 定向 build + 新增相关 `CCCodeTests/<Class>`，不得全量 2072、不得 UI tests；
   - 所有命令按双仓 CLAUDE/AGENTS watchdog、pipefail、终态和 owned-process 回收规则执行。
4. 全绿后重建并覆盖安装 Mac Release，确认 8777 runtime 来自 `/Applications/CordCodeLink.app`；检测到可用 iPhone 时按 iOS 仓规则 build/install/launch，不自动点击或视觉验收。
5. 不操作生产 Relay/VPS，不使用真实 provider 账号，不碰 owner-managed 4096；若 Mac App 安装规则必然重启其自管 serve，报告前后 PID/parent 和原因。

## 必须新增的防复发测试

- `active route + passive subscriber + two clients` 下，一条 A1/E3 source fact 只产生一次 Kernel revision/part mutation。
- unopened E3 session 零 timeline ingest，打开 hydrate 后与 server head 收敛。
- A7 raw frame 到 Projection 的 pending/answered/rejected/reload 全路径，空/错 tool identity fail closed。
- Todo 的 alias/default/partial-row 全部 rejection，失败不污染 last-known replacement。
- Provider/config 的 missing/null/wrong type/malformed connected rows destructive matrix。
- Rename/archive `{}`、错 ID、错 title/time；delete `false/null/{}/204`；成功后 list+by-ID 双确认。
- UIKit/SwiftUI variant consumer 与 wire round-trip。
- capability lag/restore exact-set test；不得在测试 helper 中手写一个与 production derivation 不同的“广告算法”。

## 停止线

仅在以下情况暂停争议 slice；无共享边界的其他 slice可继续：

1. A7 GET `/question` 或 live raw 证明缺少完成 source-proven turn identity 所需字段；
2. 修复唯一 timeline owner 必须新增第二 reducer/writer、raw timeline route 或协议字段；
3. truthful capability lag 需要新的 wire field/产品语义，而不是仓内通用 readiness gate；
4. 第一轮修复未改变 full-path reproducer 的 revision/Projection 结果；此时必须抓第一 divergence，禁止增加 dedupe/referee；
5. build/test hang 或诊断收集超出仓库上限且 owned-process 收尾后仍无最小结果。

停止时保留失败现场、真实日志和样本，标 blocked；不得 fallback、fake success 或自行改 canonical。

## 集中完成报告要求

- commits 按 red-test / product / tests / activation / closeout 分类及 diff scope；
- Audit-008 每条缺口的 before→root cause→after 表；
- C4 production owner/route 图或简表，证明 active/passive/multi-client/unopened 四种情况只有一个 ingest owner；
- Question source identity 到 Kernel/Projection 的逐字段映射；
- strict destructive matrix 与 mutation convergence 真实输出；
- capability before-withdrawn/final-restored 表及 production derivation 依据；
- iOS 两个 UI consumer 的定向测试、build、真机安装结果；
- Mac full/race/build/vet、Release 安装、8777/4096/4398/4399 收尾；
- 双仓 `git status --short`；
- 明确 exec-plan proven ≠ supervisor verified ≠ owner UI done。

## 退出判据

- Audit-008 四类缺口及 strict 子项全部由 full-path owning tests 封住；
- C4 无双 owner/双 ingest，Question 真正进入一次 Kernel/Projection；
- 所有 verified shape fail closed，无 alias/default/silent skip/任意 2xx 成功；
- variant 在两个现有 iOS 配置入口可用且 wire 正确；
- capability 与最终完整路径一致，E2/OD-3 不变；
- 双仓 bounded regression、安装与进程收尾完成且 clean；
- 提交一次报告后停止，等待 audit-009；不得自行进入 owner UI 矩阵。

## 不授权

- 不授权改 canonical、增加未定义 protocol/capability、实现 reasoning/OD-3、恢复 legacy fallback。
- 不授权 owner 真机 UI 操作、XCUITest/snapshot/simulator automation、真实账号或生产部署。
