# 监工审计报告 8 号（对应监工指令 8 号）

> **审计对象**：开发 agent 对监工指令 8 号的集中完成报告  
> **审计时间**：2026-08-20T18:41:29Z  
> **审计严格度**：严格（独立复跑 + 源码追踪 + git/runtime 核验）  
> **Verdict**：**rejected**

## 0. Audit Context

- 监工指令：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/directive-008-concentrated-c2-c7-implementation.md`
- 开发报告：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/report-008-concentrated-c2-c7-implementation.md`
- 完整报告：`docs/2026-08-20-opencode-web-source-first-convergence-plan完成情况.md`
- 设计文档 hash：`3676df7772c783bed63b65aa3e656c81bdb69fb2`，`design_doc_changed=false`

独立复跑：

- `go test ./... -count=1 -timeout 10m`：PASS；`go-bridge` 47.174s。
- `go test -race ./agent/opencode-web/ -count=1 -timeout 5m`：PASS，4.786s。
- protocol/agent/variant 三个定向 Go 测试：PASS。
- `go vet ./agent/opencode-web/ ./go-bridge/ ./core/`：PASS。
- `go build ./...`：PASS。
- iOS `OpencodeWebVariantAgentWireTests`：4/4 PASS，0.014s，`** TEST SUCCEEDED **`。
- 双仓 `git status --short`：审计开始时均 clean。
- runtime：8777 PID 9309 来自安装目录；4096 PID 9258 的 PPID 为 CordCodeLink app。

## 1. Overall Verdict

构建、单测、race、镜像和安装主张大体真实，但本批不能通过监工验收。审计在产品路径发现两个会直接破坏 canonical 行为的结构性缺陷：活跃 session 的同一 SSE 事件会经 active route 与 passive tap 两次送进 `deltaBatcher`；structured question 虽在 adapter 层生成事件，却因缺少 Kernel 所需的 turn identity 被 reducer 丢弃。前者违反 §6.5 的唯一 EventPublisher/Kernel ingest，属于半接管硬否决；后者意味着已广告的 Question Dock 实际不可达。

此外，Todo/C7 mutation 的 decoder 仍含 canonical 明令禁止的 silent/default fallback，iOS model variant 只接通状态和 wire，没有接入现有模型配置 UI。故“57/57 proven”和 capability activation 不能升级为 supervisor verified，也不能下发 owner 真机矩阵。

## 2. 逐项核实矩阵

| 检查项 | 开发自述 | 监工核实 | 结论 |
|---|---|---|---|
| commits / worktree | 双仓已提交且 clean | `git log`、`git show --stat`、`git status` | ✅ |
| Mac 全仓测试/build/vet | 全绿 | 独立复跑全部通过 | ✅ |
| iOS 定向 wire 测试 | 4/4 | 独立复跑 4/4，未执行 UI test | ✅ |
| 单一 global SSE connection | 一个 backend-instance 一条连接 | C4 测试与源码证明单一 network dial | ✅ |
| 单一 Kernel ingest | 同一事件只 ingest 一次 | `emit` 同时复制到 route 和 passive；`relayEvents` 与 `startPassiveSubscription` 均调用 `deltaBatcher.Send` | ❌ |
| Question Dock 完整路径 | asked→canonical→Kernel→iOS | adapter event 的 `TurnID/ItemID` 为空；reducer 对非 dsh-web 的 identityless request 直接 return | ❌ |
| Todo strict shape | exact content/status/priority，malformed fail | endpoint 接受 `text` alias并默认 status/priority；event 跳过坏行并默认字段 | ❌ |
| C7 strict/convergence | malformed error，delete 以 bool+list/by-ID 收敛 | rename/archive 接受 `{}` 并回填请求 ID；delete 接受任意 2xx 且不解码 `true`、不确认 absence | ❌ |
| C5 strict provider/config | strict decoder | provider 坏行静默 `continue`、缺 model id 回退 map key；`model:null` 与 absent 混同 | ❌ |
| iOS variant UI availability | capability/UI 已激活 | `selectModelVariant` 仅在 ViewModel/测试中出现；现有两个模型配置入口均未调用 | ❌ |
| runtime install | 8777 来自正式 app | `lsof`/`ps` 独立核实 | ✅ |

## 3. 关键驳回证据

### 3.1 C4：一个网络订阅者不等于一个 Kernel ingest

- `agent/opencode-web/events.go:1252-1278` 把同一 normalized event 同时投递到 session route 和每个 passive tap。
- `go-bridge/handlers_relay.go:2585-2593` 的 active relay 对 route event 调用 `deltaBatcher.Send`。
- `go-bridge/main.go:777-784` 的 passive subscription 对同一 event 再次调用 `deltaBatcher.Send`。
- 现有测试只断言两边都能观察到 event 和只有一条 `/global/event`，没有把两条消费路径同时接进 EventPublisher/Kernel 断言 revision/part 只提交一次。

这违反 canonical §6.5：“路由进 existing registered/subscribed session 的 one EventPublisher/Kernel；未打开 session 只刷新 catalog”。当前实现正处于 active/passive 双 owner 的半接管状态，`external_turn_streaming` 不得保持广告。

### 3.2 C6 Question：adapter 测试绿，但 Kernel 会丢事件

- A7 raw 样本真实带 `tool.messageID` 与 `tool.callID`。
- `agent/opencode-web/events.go:725-743` 的 `question.asked` decoder 未保存 `tool`，发出的 `core.Event` 没有 `TurnID/ItemID`。
- `go-bridge/events.go:245-263` 将空 ID 原样映射成空 `turnId/itemId`。
- `go-bridge/projection_reducer.go:1072-1101` 明确要求 `turnId`；只有 dsh-web 可以 fallback，opencode-web 空 ID 直接 return。

因此 `structured_user_input_v1` 虽被 capability 测试点亮，真实 SSV2 投影不会出现 Question Dock。现有 `TestQuestionAskedMapsToCanonicalUserInput` 只停在 adapter `core.Event`，未覆盖 Kernel/Projection。

### 3.3 Strict-shape 与 mutation overclaim

- canonical §6.9 要求 Todo item 精确 `{content,status,priority}`，malformed update 整体失败；当前 endpoint 接受 `text`、默认 `pending/normal`，live event 还会静默跳过坏行。
- canonical §6.10 要求 rename/archive 消费有效 Session.Info，delete 必须解码 boolean `true` 并确认 list absence/by-ID 404；当前实现以任意 2xx 为成功，并对缺 ID 的 mutation response 回填请求 ID。
- canonical §6.6 要求 strict provider/config decode；当前 provider row/model row 仍有 skip/map-key fallback，`/config model:null` 未与 evidence-proven absent key 区分。

### 3.4 iOS variant 不是 UI 激活

`b3919de` 只修改 models、bridge、ViewModel 和测试。全仓调用搜索显示 `selectModelVariant`、`availableModelVariants`、`selectedModelVariantTitle` 未被任何 View/UIKit 配置菜单消费；`ChatUIKitContainerView.makeModelConfigurationMenu` 与 `ChatGPTModelConfigurationPopover` 仍只有模型/effort。canonical §6.6/§6.11.1 要求现有模型配置 UI 在 non-empty variants 时提供 selector，因此不能声称 UI availability 已完成。

## 4. 路线图偏离检查

| # | 判据 | 命中 | 证据 |
|---|---|---|---|
| 1 | 删除性测试 | 否 | 暂无新增裁判代码待删结论 |
| 2 | 三环归属 | 否 | 文件范围属于 canonical C2–C7 |
| 3 | 待删清单特征 | 是（软） | strict decoder 中存在默认值、alias、silent skip |
| 4 | 半接管最差状态 | **是（硬否决）** | active relay + passive relay 同时拥有同一 timeline event 的 ingest |
| 5 | 覆盖面诚实 | 是（软） | adapter/unit 通过被叙述为 Kernel/UI 完整路径 |
| 6 | 叙事真实性 | 否 | 报告明确区分 self-attested/verified/owner done |
| 7 | 控制变量污染 | 否 | 集中批次由指令明确授权，工作树 clean |
| 8 | 根因锁死门 | 否 | 功能实现有 pinned source/sample 基线 |

判据 4 命中，按 supervise 铁律 7 驳回实施，不能让开发 agent自行继续改路线或进入 owner UI 验收。

## 5. 给 owner 的裁决选项

### 选项 A（推荐）：保持 canonical，不改产品语义，发一个集中修复指令

范围固定为：统一 C4 timeline owner；用 A7 `tool.messageID/callID` 完成 question→Kernel identity；收紧 Todo/provider/config/mutation decoder；补 iOS variant selector；先撤回未完整能力，修复和 owning full-path tests 通过后再恢复。影响是增加一轮集中开发与一次集中审计，但不需要重新调研或改设计路线。

### 选项 B：缩减首发范围

立即撤回 `external_turn_streaming`、`structured_user_input_v1`、todos、session mutation/variant UI 的广告，只保留已完整验证的基础 list/send/stream/model wire。后续逐项恢复。影响是更快得到可发布基础版，但功能范围显著缩小。

监工推荐 A，因为缺口均已有同版本样本和 canonical 决策，不需要重新写设计文档；只是实现/测试没有覆盖到最终 consumer。

## 6. Three-Track done

- exec-plan proven：**已被本次审计推翻，需 invalidated**。
- 监工 verified：**rejected**。
- owner 真机矩阵：pending，当前不应开始。
- 结论：本批尚未 done；构建与安装成功不能替代 single-ingest、Kernel consumer 和 UI availability。
