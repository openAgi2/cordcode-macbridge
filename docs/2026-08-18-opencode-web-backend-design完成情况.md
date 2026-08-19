# OpenCode Web Backend 完成情况（opencode-web）

- 日期：2026-08-19
- 对应设计：[2026-08-18-opencode-web-backend-design.md](2026-08-18-opencode-web-backend-design.md)（v3.1，两轮评审 APPROVE）
- 状态：**§8 八项中 1–7 项全数完成并落证；§8-8 Release 已覆盖安装，现网矩阵行 1–6 待 owner 执行**（本文「验收矩阵」节）。沙盒绿不替代现网验收（设计 §6 红线）。
- 施工队列：`.exec-plan/state/plan-7b61c5c09d57.json`（24 todo：21 done / 1 done(release-impl) / 1 done(release-tests) / 1 pending(owner 矩阵)）。提交区间 `b91af75..78b72f1`（MacBridge `opencode/web`）+ iOS `opencode/web` `6cd4594`/`6624f36`。

## 一、交付摘要

新 backend `opencode-web`（包 `agent/opencode-web`，驱动 id / wire kind 均 `opencode-web`，显示名 OpenCode Web）= 官方 `opencode serve` 的**纯 HTTP/SSE 客户端 + bridge-v1 翻译器**。列表、历史、占用、发送、模型、活跃态、审批全部来自官方 HTTP；MacBridge 不推导存储事实。旧 `opencode`（hybrid）一行未改、入口并存（descriptor 同屏 available，脏行为原样保留供对照）。

| §8 项 | 内容 | commit | 证据 |
|---|---|---|---|
| 1 骨架 | 注册/Client/双代探针/WireDescriptor/InstanceStatus + go-bridge 骨架接线 + import 守卫 | `b91af75` | 15 用例（探针选代/401/无认证拒绝/守卫三件） |
| 2 列表/历史/占用 | §3.3 官方占用公式（消息级、无窗口→nil）+ 双代列表 + mapRichHistoryEntry + /provider 递归目录 + /project worktree + /agent | `0880188` | 11 用例（含 tokens=0 回落、不谎报 200k、目录头断言） |
| 3 SSE+活跃 | 事件表全量翻译、占用重算走消息级、零输出 turn_error、catalog 刷新信号、/session/status 三态 | `a6a3e06` | 13 用例（多步不提前完成红线、live SSE 端到端） |
| 4 发送+模型 | catalog 门控发送（零 POST）、附件响亮拒绝、按代停止、Close 只拆 SSE、权限折叠 impl | `b19c1b1` | 10 用例（catalog 拦截零 POST、4xx 原文透传） |
| 5 投影接线 | §4.1.5 全表：M2-1 switchDir/M2-2 relay 超时/M3 re-attach 名单（提为可测常量）/pathless hydrate/SSV2 广告；S7 两不进清单维持 | `439c8f6` | TestOpenCodeWebProjectionWiring + M2-1 行为级 + 冷拉重建 Ready |
| 6 审批 | ToolAuthorizer 点亮 permission_resolve；诊断接入双代探针+折叠状态 | `0cb0ec4` | 6 用例（once→allow 回退/reject→deny/v2 once|reject/ToolAuthorizer） |
| 7 iOS+协议 | BackendKind.openCodeWeb 穷举全量 + **if 比较型 13 处**（SessionsView 7/SidebarView 3/SessionManagement 2/输入条目录 1）+ SSV2 投影族 + Mac drivers/flag/env + 协议 canonical/mirror | iOS `6cd4594`/`6624f36`，Mac `c304756`/`de71b94` | BridgeModelsTests 50 全过、SSV2 55 全过、MacBridgeBehaviorTests 20 全过、真机安装启动成功 |
| 8 Release | Release 覆盖安装 `/Applications`（最终 runtime commit `78b72f1`），现网 descriptor + 只读探针 + 沙盒端到端 | `78b72f1` + state commits | 见下「验证记录」 |

## 二、验证记录（自证部分）

**现网（owner 4096 真实 serve，全部只读，零写请求）**：
- Release app descriptor（Management API 只读）：`opencode-web` **available**，reason `generation=1.18 url=http://127.0.0.1:4096`；capabilities 含 `permission_resolve`/`external_turn_streaming`/`session_history`/`model_switch`，**不含** todos/question_reply（符合 §4.1.4）。
- 旧 `opencode` descriptor 并存原样（poll=True 等脏行为保持）。
- curl 只读：authed health 200；GET /session 200（100 会话）；SSE 连接 200，3 秒窗口收到 `server.connected` 帧（payload 信封形状与解析器一致）。

**沙盒端到端（`opencode serve --port 4296`，独立 XDG 数据目录 /tmp/ocw-sandbox，Basic Auth；已停）**：
`TestSandboxEndToEnd`（环境门控，CI 自动跳过）在真实 1.18.18 二进制上全链路 PASS：探针判代 1.18 → 列表（隔离库）→ 目录 6572 模型 → create + catalog 门控发送 204 → SSE 用户气泡 → **真实 provider-auth 缺失 turn 以零输出 turn_error 收口**（§3.5 81ms 空转场景在真 serve 上被正确暴露，文案含 may be unavailable）→ abort 路由 2xx → 诊断全绿。

**回归**：`go test ./agent/opencode-web/...`（52+ 用例）全过；`./go-bridge/...` 失败集与施工前基线**零增量**——仅两条**先存失败**（`TestCanonicalUserInputIsProjectionOnlyAndLegacyIsOneWayDerived` 顺序依赖 flaky，单跑/全量在干净树与工作树均随机翻转，多轮 stash 对照取证；`agent/dsh-web TestGetBackgroundTaskDetailFoundAndMissing` 两树同挂），均与本次改动无因果，如实上报不掩盖。`git diff --stat agent/opencode` 全程为空。

## 三、实施期挂账处置（设计 v3.1 文末清单）

| 挂账 | 处置 |
|---|---|
| 1.18 权限字面量先探 | 已按「先探」实施（once→4xx→allow；reject→4xx→deny），接受结果运行时写入诊断 `foldDiagnostics`。只读取证：本机 `/opt/homebrew/bin/opencode`（1.18.18）strings 含 once×2/reject×2/always×3（S4 复证），上下文不排他——活体钉死依赖 owner 现网审批流（矩阵行 6） |
| rename/delete 1.18 HTTP 活体钉死 | 未钉死前维持 ⛔：`SessionDeleter`/`SessionRenamer` 均未实现，RPC 走 not_supported（沙盒中未发现对应路由文档，不做臆造尝试写路由） |
| 双代探针结果进诊断 | 已落：`run_diagnostics` 的 `ocw_probe` 行携带 `generation=… url=…` |
| M3 名单可测形态 | 取「可测常量」形态：`observationResubscribeBackends` 包级变量 + 成员断言（设计二选一） |

## 四、设计执行中的活体修正（均有真 serve 证据）

1. **prompt_async 模型键为 `modelID`**（设计 §3.6 样板写 `id`）：沙盒活体 400 `Missing key at ["model"]["modelID"]` 钉死；**create 仍收 `id`**（两写路由键形不同）。已修 `session.go` 并把单测断言改为 modelID（commit `78b72f1`）。读取面（GET /session 等）确认为 `id`，不受影响。
2. **工具事件 TurnID 归因**：旧 sse_subscriber 复制件中 tool 事件不带 TurnID，健康多步工具 turn 会被误判零输出。新包修复（`handleToolPart` 传 messageID 归因），有注释与单测背书。
3. **tool 名/state 嵌套**：SSE 工具 part 的 `toolName`/`state` 可嵌套在 `tool` 对象内，与历史映射（活体形状）对齐后补 fallback。
4. **【owner 报障 2026-08-19 修复·二】SSE 流被 30s 客户端超时杀流 → 卡「执行中」**（commit `3f91726`）：owner 复测 turn1 正常、turn2 流到一半停滞且永远执行中（Mac 网页正常完成）。根因：共享 `http.Client{Timeout:30s}` 被用于 SSE GET——Go 的 Client.Timeout 覆盖响应体读取全程，超 30 秒的 turn 被客户端中途杀流；订阅器静默退出无重连，终端 idle 事件随连接丢失（旧包用无超时 DefaultClient 故无此病）。修复：SSE 专用无超时客户端；断线自动重连（1–15s 退避）；重连前按 /session/status 治愈掉线期已 idle 的 armed turn（v2 保守不治愈）；占用重算移出读循环。行为级测试 TestSSEStreamReconnectsAndHealsAfterDrop 复现中途断流并断言治愈+重连。
5. **【owner 报障 2026-08-19 修复·一】模型目录须按 `connected` 过滤 + 5MB 单飞缓存**（commit `b8030e3`）：owner 发现 iOS 模型列表混入数百个从未配置的 provider（zeldoc/siliconflow 等）且全模式卡顿。活体取证：1.18 `GET /provider` 是三段信封 `{all:[192 provider × 全量 models.dev = 6637 模型 ≈ 5MB], default, connected:[7 个已配置 provider = 65 模型]}`——官方网页选择框只渲染 `connected`。旧实现递归收集 `all` 全量且每次 list_models/发送门控/占用窗口都重拉 5MB。修复：typed 信封解析 + 只收 `connected`（空 connected=空目录，诚实）+ 60s 单飞缓存全消费方共享。沙盒复跑目录 6572→9；现网只读数学核对 iOS 将显示 65 个（7 provider）；Release 已重装（b8030e3），**owner iPhone 复测待回报**。

## 四·补、官方源码审计与对齐（2026-08-19，owner 指令「参照官方 web」）

owner 指出理想路径是 dsh-web 式「读官方 web 源码→穷举 API→包装」。补做该审计（checkout /Users/jacklee/Projects/opencode @dev，v2 线；1.18 分歧以活体为准）：

**官方 v1 SDK 面（packages/sdk/js gen，1.18 谱系）**：session{list,create,status,delete,get,update,children,todo,init,fork,abort,share,unshare,diff,summarize,messages,prompt(POST /session/:id/message),message/:mid,prompt_async,command,shell,revert,unrevert}、provider{list,auth,oauth}、Global.event=get.sse("/global/event")、project{list,current}、agent、path/config/vcs/file/pty/mcp/lsp/formatter/tui。

**逐面对表结论**：一期 12 个已接面与官方用法一致（含 connected 过滤 prompt-model-selection.ts、占用公式 session-context-metrics.ts 逐字段、SSE 无寿命时限）。本轮对齐两处：占用改**严格五项和**（删自加的 total 回落）；发送补官方**默认模型回退链**（pending→会话采纳→首个 connected provider 的 default??首模型；回退候选全部过 catalog 门控，connected 空目录仍诚实报错零 POST）。官方有而一期未接（按设计 ⛔/2️⃣ 维持）：revert/unrevert、summarize(compact)、fork、children、diff、todo、init、command、shell——列为二期候选清单。tui/pty/lsp/mcp 等 Mac 客户端不适用面不接。

## 五、验收矩阵（owner 执行——§6 现网行 1–6）

前提：Mac 运行新 Release（已装 `/Applications`，runtime commit `78b72f1`）；iPhone 安装本分支 Debug（已装）；Mac 网页打开 `http://127.0.0.1:4096`。

| # | 前提条件 | 动作 | 应看到 |
|---|---|---|---|
| 1 | 网页里找一个占用约十几万 tokens 的会话 | iPhone 切 **OpenCode Web** 打开该会话，看 ⭕ 占用圈 | 有已用/窗口数值（非「暂无」），与网页百分比一致 |
| 2 | 同一会话 | iPhone 选目录内模型后发一条短消息 | 有流式回复；若故意选目录外坏模型 → 明确失败提示（非空等 81ms） |
| 3 | 网页 `/server/<项目>/session/…` 任一历史会话 | iPhone OpenCode Web 按同目录打开 | 历史消息与网页一致（含工具/思考块） |
| 4 | Mac 网页某会话打字发 turn | iPhone 已打开同一会话 | 实时旁观（外部 turn 流式到 iPhone） |
| 5 | 同一 Mac | 切回旧 **OpenCode** 入口 | 旧入口仍可用；占用圈空等脏行为**保持原样** |
| 6 | 让会话触发一次工具审批 | iPhone 上 Allow / Deny 各试一次；另试一次网页先答 | turn 继续/停止；网页先答则 iOS 收口；**绝不自动 allow** |

owner 回报格式：行号 + ✅/❌ + 现象即可。行 1–6 全 ✅ 后，退役流程（§4.2 另立任务）才可启动。

## 六、未完成 / 边界如实声明

- **§8-8 回归（现网矩阵行 1–6）未执行**——必须 owner 真机操作，不得以沙盒绿或单测冒充（本报告因此不声明整体完成）。
- 两条先存测试失败（见「验证记录」）非本次引入，未修复（不在本任务范围，避免越权改动）。
- M3 提示 3（dsh-web 不在 re-attach 名单的既有缺口）：设计已记录为 owner 决策项，本次未代为修复（`observationResubscribeBackends` 注释保留标记）。
- 真机 UI 仅安装启动（`scripts/run.sh device`），未做任何 UI automation/视觉验收（需 owner 明确授权）。
