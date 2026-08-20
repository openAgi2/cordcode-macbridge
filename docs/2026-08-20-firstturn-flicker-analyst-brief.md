# 独立分析任务：opencode-web 新会话首回合「~1 秒完成态闪烁」根因

## 0. 你的角色与方法纪律（先读）

你是独立根因分析师。前任修复者已连续两轮未命中本症状——**不要沿用其结论链，从证据重建时间线**。方法纪律：

1. **先取证，后归因，最后才谈修复。** 每一个归因断言必须挂至少一条实测证据（日志行 / wire 帧 / 时间戳）。仅凭读码推出的结论标记为「假设」，不是「发现」。
2. **本案的核心证据缺口**：把 iOS 输入框从「执行中」翻成「完成态」的那个信号，**至今从未被实际观测过**——前两轮全部是读码推断。你的第一任务是抓到它（它是谁、几点到、走哪条路）。
3. 禁止在定位根因之前修改任何产品代码。允许为取证加临时日志/调试构建（不进正式安装通道）。
4. 前两轮修复已装机且单测全绿但症状不变——这个事实本身是输入，见 §4。

## 1. 环境与版本

- Mac 桥仓：`/Users/jacklee/Projects/cordcode-macbridge-opencode-web`（分支 `opencode/web`）
- iOS 仓：`/Users/jacklee/Projects/cordcode-ios`（分支 `opencode/web`，真机 Debug `79c1a12`）
- 运行时：`/Applications/CordCodeLink.app` → runtime 进程（端口 8777，`-drivers claude,codex,grokbuild,dsh-web,opencode-web`，`-opencode-web-url http://127.0.0.1:4096`）。应为 commit `4e421859bac1`。
  **第 0 步先验证**：iPhone 实际连的就是这个实例与版本（桥启动日志有版本行；`pgrep -fl cordcode-bridge-runtime` 看进程启动时间），排除「修了但跑的是旧实例」。
- opencode serve：owner 真实实例 `127.0.0.1:4096` —— **只读探针**（GET + Basic Auth，凭据 `ps eww <serve-pid>` 取），**严禁写请求**。
- 真机：iPhone 16 Pro。仅授权探测/构建/安装/启动（`scripts/run.sh device`），**无 UI automation 授权**；UDID/Team ID 不得写入文档日志。
- iOS 测试只跑 `-only-testing:CCCodeTests`；改 Mac 侧必须 `./scripts/build-unsigned-release.sh` 产 Release 覆盖装 `/Applications`（严禁临时构建产物装机）。

## 2. 精确症状（owner 三轮复测完全一致）

opencode-web 模式，chat 目录新建 session，发送第一条消息（例：「讲个猴哥语录100字左右」）：

| 时刻 | 输入框状态 | 备注 |
|---|---|---|
| 点击发送 | 执行中（乐观态） | 正常 |
| 立即（<0.5s） | **完成态** | 异常，闪烁开始 |
| ~1s 后 | 执行中 | 翻回，此后**流式输出正常、收口正常** |

对照面（关键）：同会话**第二条消息起完全正常**；**dsh-web**（DeepSeek harness，同为 web API+协议转换 + SSV2 投影）**首回合完全正常**。症状 100% 稳定复现，非概率性。

## 3. 架构速写（导航用，非结论）

- **懒建会话**：`create_session` 对 opencode-web 快速返回 `pending-<id>`（`handlers.go` ~:2066）；真实 `ses_xxx` 在首条 `Send` 内 `POST /session` 同步创建（`agent/opencode-web/session.go` `ensureServerSession`）；relay 在首个 real-id 事件上做 pending→real rebind（现 `handleSendMessage` Send 成功后也立即 rebind，见 §4-R2）。iOS 从首个带 real id 的投影帧/RPC 学到 real id 并 `rebindPendingSessionIdIfNeeded` + `setObservedSessionIDs([real])`（`ChatViewModel+CodexStreaming.swift` ~:1512）。
- **iOS SSV2**：投影为唯一真相。桥侧 v2 连接 raw 事件 deny-list（`projection_delivery.go` `isSessionSyncV2RawTimelineEvent` ~:58-77）拒绝 `turn_started/turn_completed/user_message/text_delta/reasoning_delta/session_state_changed/error` 等；iOS 只吃 `projection_patch` 推送 + `get_session_projection` RPC。
- **输入框状态**（iOS）：`isSessionExecuting`（`ChatViewModel.swift` ~:2159）= 投影 execution.phase ∈ {running, requires_action} ‖ `isGenerating`（乐观）‖ requiresAction badge。发送时 `setGenerationState(.waitingForAssistant, "sendMessage.placeholder-created")` 置位；投影 idle 渲染（`renderProjectionFromStore` ~:1044 / `applyPresentationChangeSet` ~:1124）或 finalize 链（`scheduleCodexTurnFinishDebounce` ~500ms → `handleCodexTurnFinished` → `completeGenerationCycle`）清位。
- **桥投影管线**：opencode-web 在 `forceColdInspection` 家族，首拉（sinceRev=0）开冷检事务；事务期 live 帧入 `pendingLive` 不下发；提交闸门 `WaitHydrateCommitReady`（`projection_kernel.go` ~:967）；`sourceIsLive` 允许在飞 turn 按 running partial 提交。

## 4. 已做的修复（均已装机、单测全绿）——以及它们对症状的实际影响

- **R1 = `58a1261`**：① agent `emitResultOnce` 抑制「无 armed turn 的裸 idle」假终态；② 冷源 seal 决策加桥 registry-live 覆盖；③ opencode-web 补 `sourceIsLive` 采样。
  → **效果：整段一次性到达（攒帧）症状消失**，owner 确认能流式输出。
- **R2 = `4e42185`**：① `handleSendMessage` Send 成功后立即 pending→real early rebind（幂等）；② opencode-web 冷源对「registry-live 且 0 条消息」200ms×6 重拉（防首 prompt 未持久化时 commit 空 idle 基线）。
  → **效果：~1s 完成态闪烁完全不变**（时长、概率、表现三轮零变化）。
- **关键推论（供你校准，不是结论）**：若闪烁由「桥下发 idle 基线/终态投影」驱动，R2 至少应改变其时长或概率；零变化提示 (a) 翻转信号不走投影路径，或 (b) 走投影但来自 iOS 本地状态/本地占位渲染，或 (c) 走一条两轮都没碰到的信号路径。

## 5. 已排除项（有代码级证据，可复核但不要重走）

1. v2 deny-list 拒绝 `session_state_changed`/`turn_completed`/`text_delta` 等 raw 帧到 iOS（`projection_delivery.go:58-77`，2026-08-20 核对）。
2. sendMessage HTTP 直返触发 finalize：桥 backend 的 sendMessage 恒返 nil（`CCCodeBridgeBackendClient.swift:338-364`）。
3. relay 60s idle 超时提前收口：opencode-web `disablesRelayIdleTimeout=true`。
4. 假终态 EventResult 致 relay 退出断流（R1 已修，且流式恢复证实）。
5. 冷检闸门阻塞至回合终态的攒帧（R1③ 已修，流式恢复证实）。

## 6. 未排除候选（按前任怀疑度排序——请用证据裁决，不要被排序带偏）

1. **iOS raw 过滤窗穿透 → 本地 finalize 链**：SSV2 下 `current==pending`、事件带 real id 时，raw 事件穿透过滤窗（`ChatViewModel+CodexStreaming.swift` ~:145-190：非当前 session 不早退；~:236 先 rebind，~:241 guard 因刚 rebind 通过），随后走老状态机：`.sessionStatusChanged(isIdle)` / `.sessionStateChanged(.idle)` / `.turnCompleted` 任一 → `scheduleCodexTurnFinishDebounce`（~:276/:310/:448）→ 500ms → finalize → 完成态。**待裁决问题：首秒内到底有没有、是哪个 raw 帧带 real id 到达 iOS？deny-list 之外还有哪些事件名会发到 v2 连接？（对照法：枚举桥全部 `publishEvent`/`deltaBatcher.Send` 的事件名 × deny-list 清单）**
2. **iOS 本地占位/渲染回退**：`sendMessage.placeholder-created` 建的本地态在无投影可渲染窗口内被 idle 对齐（`isReadyForUITakeover` guard 失效路径 / paint fence 清理）。
3. **桥侧残余 idle 下发**：pre-send 对 pending id 的 404 失败拉取以错误帧形式触达 iOS 渲染层；或 early rebind 后 `get_session_projection(pending)` 解析到 real 的时序里仍有没盖到的分支（用 §7-1 的 RPC trace 日志直接裁决）。
4. **dsh-web 对照的机理差异没找全**：dsh-web real id 在 create 时同步返回（首拉在发送前、空会话瞬时 commit，无 pending 窗口）——顺着这个差异枚举 iOS 在「无 pending 窗口」时不会执行的代码路径。
5. **运行实例错位**（低概率，§1 第 0 步排除）。

## 7. 取证手段（按此顺序，证据密度从高到低）

1. **桥日志**：`~/Library/Application Support/CordCode Link/logs/`。关键行：`relayEvents forwarding`（每回合前 3 事件 + turn_completed/error）、`session id rebind complete`、`projection_rpc stage=mac_receive/response_enqueue`（含 sinceRev/headRev/outcome）、`projection_shadow stage=hydrate_commit / live_only_admission_commit`、`[K4Patch]`。让 owner 复现一次，导出发送时刻 ±5s 全量行，对齐闪烁时刻。
2. **iOS 侧日志**：真机连接下 Console.app 过滤进程 CordCode，重点 `[TB-VM] scheduleCodexTurnFinishDebounce`（**其 `source=` 参数直接指认触发源**）、`handleCodexTurnFinished`、`[Apply]`、`[ProjPull]`。这是裁决候选 1 vs 2/3 的最短路径。
3. **wire 帧级抓取**（日志不足以裁决时）：桥调试构建给该 conn 的全部下发帧加一条「帧名+时间戳」日志，或本地起代理——直接回答「首秒到底发了什么」。这是本案最直接的裁决证据，优先级高于继续读码。
4. **沙盒复现栈**（可完全自建，不碰 owner serve）：mock provider + 真 opencode serve + 沙盒桥；复建命令在 `docs/2026-08-18-opencode-web-backend-design完成情况.md` §四·补九/补十。注意：mock 必须显式选 mock 模型且恒 200（漏选会命中 serve 内置未鉴权模型的 stream error，造成假失败）。
5. **iOS E2E**：`OpenCodeWebNewSessionE2EDiagnosticTests`（真 transport 直连沙盒桥跑完整 iOS 管线；沙盒缺位自动 XCTSkip）。

## 8. 交付物

1. 实测时间线：帧名/日志行/时刻 → 输入框状态翻转点（谁在几点把 iOS 翻成完成态）。
2. 唯一根因 + 证据链（每步挂证据）。
3. 最小修复建议 + **为什么前两轮没打中**（R1/R2 各自修的是什么、为什么与本症状正交或未触达）。
4. 可证伪预测：修复后哪个帧/日志行应消失或时移。

## 9. 红线约束（不可违反）

- ① `agent/opencode/`（老 opencode）代码保留不删、驱动列表已移除；② 4096 是 owner 真实 serve，只读探针；③ Mac 改动必须 Release 覆盖安装 `/Applications`（`./scripts/build-unsigned-release.sh`）；④ iOS 真机仅授权探测/构建/安装/启动，无 UI automation；UDID/Team ID 不写入文档；⑤ iOS 测试只跑 `-only-testing:CCCodeTests`；⑥ 官方对齐标准 = 调用形状级对照 `~/Projects/opencode` 官方源码；⑦ 测试证据要源码/活体背书。
