# Changelog

本文件记录 CordCode MacBridge 的对外可见变更，按 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 惯例组织，最新在前。

技术细节与文件级证据见同目录下每轮的 `docs/YYYY-MM-DD-<主题>完成情况.md` 及对应审计报告；本 CHANGELOG 面向使用者/维护者，记录「改了什么 / 有何提升」，不重复罗列实现细节。

版本号对齐 MacBridge Release 构建的 `MARKETING_VERSION`（见 `MacBridge/project.yml`）。日期为协调世界时（UTC）。

## [Unreleased]

### 2026-07-12 — 兼容 ChatGPT App 内嵌的 Codex runtime

- **改了什么**：CordCode Link 启动 go-bridge 时补入新版 ChatGPT App 的 Codex CLI 目录（`/Applications/ChatGPT.app/Contents/Resources`）。OpenAI 将独立 Codex App 合并为 ChatGPT App 后，该目录中的可执行文件仍名为 `codex`，但旧的 `/Applications/Codex.app/...` 路径已不存在。
- **有何提升**：使用新版 ChatGPT App 的用户不再被误判为“未安装 Codex”；Bridge 可正常以 app-server stdio 模式启动 Codex、iOS 可恢复加载 Codex session。原独立 Codex App 路径继续保留，兼容未升级用户。

### 2026-07-12 — 修复新版 Codex 会话打开时断连

- **改了什么**：Codex 新版 transcript 可能先写入 `patch_apply_end`、后写入对应的工具调用；rich-history 解析器此前假定已有可挂载的 tool step，错误索引空 steps 并触发 Go panic。现在只忽略无法关联的孤立 patch 完成事件，继续返回该会话的真实消息历史。
- **有何提升**：iOS 打开新版 Codex 创建且包含文件修改的 session 时，不再因 Bridge 连接被 panic 中断而无限“加载中”；其余消息、已关联 patch 与旧版会话解析保持不变。

### 2026-07-12 — 打开会话时不再显示冗余完成通知

- **改了什么**：iOS 打开某个会话时会撤销该会话已排队或已送达的「任务已完成」本地通知；完成回调也会在当前正在显示该会话时跳过投递。
- **有何提升**：用户已在消息页阅读该会话时，不会再被同一会话的完成横幅遮挡；切到其他会话或离开 App 后的后台完成通知仍会保留。

### 2026-07-06 — 修复 iOS OpenCode 模式无流式输出（active turn 改走 managed server + SSE）

- **改了什么**：OpenCode 模式下 iOS 发消息时，go-bridge 的 active turn 从批处理 `opencode run --format json`（一轮 turn 只在结束时发 1 帧 `text_delta`，iOS 表现为「等整段答完才一次性出现」）改为走 Swift 已托管的 `opencode serve` HTTP API + `/global/event` SSE。新建 `opencodeServerSession`（实现 `core.AgentSession`）：`Send` 时 `POST /session/:id/prompt_async`（204 非阻塞），消费一条 dedicated、按 sessionID client-side 过滤的 SSE，`message.part.delta` → 增量 `EventText`、`session.status idle` → `EventResult`。复用 `sseSubscriber` 全套事件解析 + dedup + 生命周期翻译，只新增 `sessionFilter`（atomic；pending 态全丢，避免 chatID 未定前把别的 session 事件串到 iOS）。`StartSession` 按 `httpBaseURL` 分流（server 在 → 流式；否则回退原 CLI 批处理，保留兜底）。模型经 `resolveOpencodeModelLocked` 解析，建 session 时用 `{model:{id,providerID}}` 绑定（prompt body 的 `providerID/modelID` 实测不生效，模型必须 session 级设定）。`providers.go` 加 POST-capable `doRequest`，`fetchJSON` 复用。绝不 kill `opencode serve`（全局共享，归 Swift `OpenCodeManagedServer.swift` 管）。
- **有何提升**：iOS OpenCode 模式（owner 真机实测 opencode/mimo-v2.5-free）发「讲一个1000字的故事」，消息页从「等整段答完才一次性出现」变为**逐字流式增长**，与 Mac opencode App 一致。live 集成测试（`server_session_live_test.go`，env-gated）对着托管 server 实测一轮 turn **80 帧 EventText**（对比批处理 CLI 的 1 帧）+ EventResult 正常收口。Claude/Codex 路径不动。注：Codex 模式经 cligate 供应商的非流式是 **cligate 上游问题**（`responses-route.js` 硬编码 `stream:false`，攒满整段再假装流式），不在本次范围；codex 经流式供应商（官方/cliproxyapi）iOS 本就流式正常。
- 改动限于 MacBridge `agent/opencode`：新增 `server_session.go` + `server_session_test.go` + `server_session_live_test.go`，改 `opencode.go`（`StartSession` 分流 + `resolveOpencodeModelLocked`）、`providers.go`（`doRequest`）、`sse_subscriber.go`（`sessionFilter`）、`session.go`（`stageImages` 抽成自由函数 `stageOpencodeImages` 供 CLI 和 server session 共用）、`session_test.go`。不动 wire protocol（对 iOS 仍是 `text_delta`，只是帧数从 1 变多）、不动 iOS、不动 relay。

### 2026-07-05 — 修复 Claude session PID 复用导致 stale session 误判 running

- **改了什么**：`agent/claudecode` 判定 Claude session 是否 running 的 PID 活性检查从纯 `kill(pid, 0)` 升级为 PID 身份校验。新增可注入 seam `procIdentityAlive(pid, expectCwd)`：在 PID 存活之上，再用 `ps` 校验可执行名包含 `claude`、用 `/proc/<pid>/cwd`（Linux）或 `lsof`（macOS）校验 cwd 与 stub 记录一致；任一强不匹配（PID 被复用为非 claude 进程，或 cwd 不同）即判非 live。`LiveSessionProcess` 和 `GetRunningSessionIDs` 改用该 seam，`IsProcessAlive`（file-relay 每 tick 的 cached PID 复查）保留纯活性语义不动。身份校验对平台探测失败 fail-open（不因 ps/lsof 暂时不可用而误判 idle），PID-reuse 防线只依赖强不匹配分支。Windows 仅为编辑器/CI 可移植性构建，fail-open 占位。
- **有何提升**：某个 Claude session 的 stub 残留（claude 异常退出未清理）且该 PID 被 OS 复用给无关进程时，不再被误判为 running——避免 iOS 因此进入 phantom executing（输入框锁"执行中"、status strip 不消失）。本次 07-05 external-turn 复现因 stub 正确缺失未触发，但这是真实 latent bug。新增 `TestGetRunningSessionIDs_PIDReuseNotRunning` 回归（active transcript + 复用 PID → 非 running；身份恢复后 → running 作对照）。
- 改动限于 MacBridge：`agent/claudecode/proc_seam.go`（新 seam）、`agent/claudecode/proc_unix.go` + `proc_windows.go`（身份校验）、`agent/claudecode/claudecode.go`（两处调用点改用 seam）、`core/interfaces.go`（`LiveSessionLister` 文档说明 `Live` 现为身份校验）。不动 wire protocol、iOS、relay，不改 `IsProcessAlive` 公共契约。

### 2026-07-05 — 修复 Claude 外部 turn 在 iOS 上滞后一轮显示

- **改了什么**：Claude Code 会话在 Mac 端外部进程（Claude App / Terminal）里继续对话时，MacBridge 的 transcript file relay 不再因为初始 snapshot 看起来 idle 就立即退出。现在 relay 会用 live-only PID 活性判断区分「已完成死进程」和「仍活着但 transcript 暂未增长」：死进程仍立即广播 idle 并退出；活进程进入轮询，看到新 user 行会发 `turn_started`，看到 final assistant 会发 `turn_completed` + idle 并退出。初始扫描改为 reader-based classifier，能在 relay 重启后识别已经写入的 user 行并补发 `turn_started`，避免 live-idle TTL 空窗吞掉 per-turn anchor；中断标记会完成当前 turn 但继续 watch；meta-only 增长不会重复发事件；进程死亡会有界收口。
- **有何提升**：iOS 旁观 Mac 端发起的 Claude 外部 turn 时，不再只到下一轮才显示上一轮回复的执行锚点；已完成或崩溃残留的 transcript 也不会被误报为 running。同步修复了 2026-07-05 CPU 修复中暴露的生产注册问题：`runningMap` cache 现在能在 `-drivers claude`（agent 注册名为 `"claude"`、`agent.Name()=="claudecode"`）下正确调用 `GetRunningSessionIDs`，不再只在测试里的 `"claudecode"` 注册名下生效。
- 改动范围限于 MacBridge Go runtime：新增 `core.LiveSessionLister` / `LiveSessionProcess`，`agent/claudecode` 复用 Claude session stub 扫描并暴露 live-only PID 检查；`go-bridge` file relay 增加 live gate、cached PID tick recheck、two-tier idle lifecycle 和定向回归测试。不改 wire protocol、不新增 `hello_ack` capability、不伪造 file-relay `text_delta`；外部 turn 内容仍由 iOS 历史同步渲染。

### 2026-07-05 — 修复 Claude list_sessions 高 CPU：list 路径不再 per-row 解析 transcript

- **改了什么**：iOS 反复刷新 Claude 会话列表时，`cordcode-bridge-runtime` 会因 `handleListSessions → enrichSessionStateWithAgent → GetRunningSessionIDs` 对每个列出的 session 重复解析 transcript（事件中 `wire_mapping_ms` 一度达 9.5–11.8s、144 session × ~116MB）而逼近单核 100%。现在 list 路径改为 list-safe 批量 enrichment：`getRunningMap(ctx, agent)` 每请求一次性算出 running 集，`enrichSessionStatesForList` 只用 registry + running map 给行打 `runtimeState`，**不再对任何行打开/解析 transcript、不再 `markIdle`、不再写 `/tmp/bridge-sessions.json`**。`GetRunningSessionIDs` 的结果用 2s TTL 缓存（burst 只算一次），MacBridge 拥有的 turn 状态迁移立即失效缓存；外部启动的 Claude turn 在 ≤1 个 TTL 窗口后探测到（仍由 live-PID 有界的 `GetRunningSessionIDs` 负责，未引入后台扫描器）；`isSessionExecuting` 结果按 `sessionID+path+size+mtime`（size 与 mtime 同时比较）缓存，把冷缓存代价收敛到「变化的 live transcript 数」。3 个 list 调用点（Claude / 非 Claude / OpenCode）全部迁移到批量 API；`reasoningEffort` 注入保留；single-session detail 路径（`get_session`/`get_session_messages`）仍保留更深的 transcript 检视。
- **有何提升**：list_sessions 在 catalog + running-map 缓存命中时不再花数秒在 `wire_mapping_ms`（144 session 的 fixture 实测 cold≈56ms / cache-hit≈42ms / per-row transcript 打开数=0），runtime 不再因 iOS 刷新而逼近单核；completed session 不再因 list 路径的 stale-running 校验被误判；外部 Claude turn 仍能在 TTL 内被发现。新增定向测试覆盖零 per-row transcript 打开、running map 每请求一次、TTL/失效、外部 turn（可注入 PID 活性 seam）、transcript 缓存指纹与 large-K 冷缓存护栏。
- 改动限于 MacBridge（`go-bridge/handlers.go`、`go-bridge/handlers_opencode.go`、`go-bridge/handlers_relay.go`、`go-bridge/types.go`、新增 `go-bridge/running_map_cache.go` + `go-bridge/transcript_probe.go`、`agent/claudecode/claudecode.go`、新增 `agent/claudecode/transcript_exec_cache.go` + `agent/claudecode/proc_seam.go`），不动 wire protocol、iOS 或 relay。

### 2026-07-04 — 清洗 Claude Code 斜杠命令在 iOS 消息页的协议标签泄漏

- **改了什么**：Claude Code 模式下，用户在 Mac 端执行 `/handoff-doc`、`/takeover`、`/model`、`/compact` 等斜杠命令时，Claude CLI 注入的内部协议标签（`<command-message>`、`<command-name>`、`<command-args>`、`<local-command-stdout>`、`<local-command-caveat>`）和 skill 文档全文（`Base directory for this skill: ... # Mission Takeover ...`）原本会原样作为用户消息出现在 iOS 消息页。现在 `agent/claudecode` 的 rich history 与会话列表预览统一清洗：斜杠命令收敛为简洁的 `/cmd args摘要`（args 按 rune 计数截断到 120，不切断多字节字符），`<local-command-stdout|stderr|caveat>` 等纯协议回显整条过滤，skill 文档注入按内容特征可靠过滤。
- **有何提升**：iOS 消息页和会话列表不再显示 CLI 内部 XML 标签噪声和 skill 全文，斜杠命令以可读的命令名形式呈现；普通文本（含合法的 `<`/`>` 字符）不受影响。已对本机全部 141 个真实 Claude transcript 回归验证 0 泄漏。
- 关键修正：skill 文档注入的过滤从"launch 状态机驱动"改为"内容特征驱动"。真实 transcript 中 skill 文档注入（`isMeta=true`、以 `Base directory for this skill:` 开头）不总是紧跟在 `Launching skill` tool_result 后面，原状态机会漏掉这种独立出现的注入；现按内容直接判定。
- 改动范围限于 MacBridge 源头（`agent/claudecode/claudecode.go` 的 user 文本分支与 `extractTextContent`），不动 iOS、wire protocol 或 shared-message-renderer；新增定向测试覆盖五类标签清洗、多字节截断与 skill 文档独立注入场景。

### 2026-07-04 — 记录 Claude 冷启动 spurious idle 跨仓结论

- 跨仓联调定位：冷启动既有 Claude session 时，transcript file relay 抢先基于上一轮已完成 transcript 广播 `session_state_changed(idle)`（早于真实 agent stdout relay 报 `running`），是 iOS 侧「首轮流式从头重播」的上游诱因。本轮 Mac 代码未改（relay-kind 拆分 `7c1d97d` 已修 file relay 占位问题但未覆盖 spurious idle 广播），iOS 侧已兜底（忽略 Claude local turn 首 token 前的 idle）。Mac 侧 file-relay/agent-relay 状态收敛为后续独立清债。详见 `think.md` 同节。

### 2026-07-04 — 强化 agent 自主诊断规则

- `CLAUDE.md` 新增“Autonomous diagnosis and evidence collection”规则，明确 bug 排查时 agent 必须先自行读取源码、日志、进程、端口、配置、Management API 和定向测试证据；不得默认让 owner 手动跑命令、复制日志或替 agent 选择实现路径。
- 明确连接真机时的边界：只读设备探测与日志采集应由 agent 自行完成；点击、输入、滑动、截图、视觉验收和 UI automation 仍需 owner 当前任务明确授权。
- 把 `think.md` 和相邻 iOS 仓 `think.md` 提升为 session/history/live-stream/执行态等问题的排障入口，要求 agent 先复用既有复盘结论，避免重复调查。

### 2026-07-04 — 修复 Claude Code 冷启动既有 session 首轮流式重复

> **根因口径修订（2026-07-04 架构健康第四轮）**：本条目原描述把 Mac 侧
> `relayRunningKind` 拆分当作冷启动重复从头输出的主因。经日志与 `think.md`
> 复核，Mac runtime 没有重复 `send_message`；**症状主因在 iOS 侧**——iOS 在本地
> live stream 中途执行普通 `loadMessages` / running polling / todo refresh，把权威
> 历史覆盖到了本地正在流式增长的 timeline（已由 iOS `e018cb5f Fix Claude local
> stream history overwrite` 单点修复）。Mac 侧 `relayRunningKind` 拆分属于 **latent
> bug / 独立 hardening**（transcript file relay 与真实 CLI stdout relay 不应混用为
> 同一布尔占位），保留为独立加固，不再标记为症状主因。第四轮把 iOS 侧的结构性硬化
> 完成为 backend-agnostic turn sync policy（见下条）。

- （Mac 侧 latent bug / 独立 hardening）冷启动打开既有 Claude Code session 后，首个本地提问可能被 transcript file relay 抢占真实 CLI stdout relay；现在 `send_message` 会让真实 AgentSession relay 接管。这不是冷启动重复从头输出的主因（主因在 iOS，见上），但两类 relay 混用是 latent bug，独立修复。
- 新增 go-bridge 回归测试，覆盖“已有 Claude file relay 标记 + 立刻本地发送”的接管路径，防止后续重构再次把两类 relay 混用成同一个布尔占位。

### 2026-07-04 — 架构健康第四轮（最终轮）：Chat turn sync state-model hardening

第四轮（本次专项收口轮）把 iOS `ChatViewModel` 的 local send / live event / history sync / running-session polling / session switch 互斥与优先级规则，从散落在多个 extension 的 Claude-only ad-hoc 条件（`isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend`）重构为 backend-agnostic 的显式 policy/coordinator，并用定向测试 + strict net-growth gate 防回涨。

- **新增 policy 类型（iOS 仓 `../cordcode-ios`）**：`ChatTurnSyncPolicy`（纯函数 enum：`Ownership` `.none`/`.localSend`/`.remoteLive`/`.reconciling`、`LoadTrigger` 8 case、`LoadDecision` 5 case）+ `ChatTurnSyncState`（`@MainActor` state holder：`decideLoad`/`beginLoadIfAllowed`/`canApply`/`finishLoad`，MainActor 原子读写 + apply 前复核）。policy 不访问 `ChatViewModel`/全局状态/网络。
- **接入生产调用点（iOS 仓）**：`loadMessages` 入口统一经 `turnSyncState.decideLoad` → `beginLoadIfAllowed`（`.defer*`/`.reject*` 在网络请求前短路）→ fetch → `canApply`（apply 前复核 ownership/session/initializationID/token）→ `finishLoad`；`sendMessage`/`beginGenerationTurn` 设置 ownership，`completeGenerationCycle` 转入 `.reconciling`，final reconcile apply 后清回 `.none`；`switchSession` 清理旧 session ownership。
- **Backend-aware 差异**：Claude Code 在 `.localSend` 时 defer 普通 load（CLI 无跨 session live bus）；Codex/OpenCode 走 merge-only（app-server/SSE live 权威、merge 幂等）。这是能力判断，不是「Claude 就跳过」粗规则。
- **定向测试（iOS 仓）**：`ChatTurnSyncPolicyTests` 25 条纯函数单测；`RemoteRunningSessionTests` 新增 `testClaudeCodeInterleave_inFlightHistoryLoadDoesNotOverwriteLivePartialAfterLocalSend`（证明 apply 前复核真实存在）+ `testClaudeCodeSecondTurn_finalReconcileClearsOwnershipForNextTurn`（证明 ownership 清回 .none 不阻塞下一轮）；既有 Claude/Codex/OpenCode/session-switch 相关测试全绿。
- **strict net-growth gate（本仓）**：`scripts/hygiene-baseline.json` 新增 `chatviewmodel_generation`（2336/56）+ `chatviewmodel_messagesync`（1577/46）两条 baseline；`check-architecture-hygiene.sh` 泛化为遍历所有 baseline 条目；`CORDCODE_HYGIENE_STRICT=1` 下净增即 fail。第三轮 BridgeProvider gate 保留。
- **文档同步**：`../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md` 新增「Turn ownership / history sync gate / final reconcile」小节；本仓 `GO_BRIDGE_ARCHITECTURE.md` 新增「iOS live event vs history polling 消费边界」小节；本条目修订既有 07-04 Claude 冷启动条目根因口径。
- **专项收口声明**：本次架构健康专项到第四轮结束（closed）。剩余大文件（`ChatUIKitContainerView`、`claudecode.go`、`appserver_session.go`、`handlers.go`、`BridgeProvider` 下一子域）作为普通维护债进入日常 backlog，不派生「第五轮架构健康」。未来若出现新系统性 gap，需另立专项。完整完成报告见 `docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md`。

### 2026-07-04 — 架构健康第三轮：BridgeProvider transport creation 子域提取（BridgeTransportConnector）

第三轮按 brief 执行 P0 → P3，目标是让 iOS god-object `BridgeProvider.swift` 实际变薄：把 transport creation 子域（构造 / direct+relay attempt / 多候选 direct race / 未采纳 transport 清理）从 `BridgeProvider` 拆到独立 `BridgeTransportConnector.swift`，不改 protocol、pairing、Relay crypto、路径选择语义或 recovery ownership。12 个 exec-plan 任务全部 proven done。

- **P0 测试保护（iOS 仓 `../cordcode-ios`）**：在未拆代码上确认 `BridgeLANFirstFallbackTests` / `BridgePathSwitchTests` / `GodObjectCharacterizationTests` 全绿（46 用例）；补 brief T1 指明的最小缺口——direct + relay 双失败时暴露 relay 链末端真实错误（`relay.connect_failed`）并记录 `relay-fallback-after-direct-fail` trace，不构造假成功。锁定提取前基线 lines=1967 / funcs=88 / forTesting=36。
- **P1 提取 BridgeTransportConnector（iOS 仓）**：新增独立 `BridgeTransportConnector.swift`（`@MainActor final class`），迁出 `connectTransport` / `relayCredentials(for:)` / `runDirectSingle` / `runRelay` / `runDirectPhase` / `attemptDirectPhase` / `attemptRelay` / `runDirectRace` 与 `RaceTransportCollector` / `RaceResult` / `RaceCompletion` 及三组测试 factory 注入。`BridgeProvider.connectBridge` 保留策略选择、generation/recovery 协调、adopt；通过注入式 `configure(generationGuard:probeRoundNotifier:taskCountLogger:)` 把 connection coordinator 上下文单向交给 connector。设计约束严格落地：connector 不写 `activeBridge` / `cachedClients` / `connectionStatus` / `activeConnectionKind`，不持 `RecoveryCoordinator`，不持 UI 状态，仅持 `SavedBridgeStore` 作 relayStore；`runDirectRace` 提取边界止于 `applyHelloAckLocalURLRefresh` 之前（后者保留在 BridgeProvider）。`BridgeProvider` 仅新增 1 个窄 forward `transportConnectorForTesting()`，未超过 brief 的 ≤2 上限。
- **P1 测试（iOS 仓）**：现有黑盒测试 factory 注入调用点改写到 `provider.transportConnectorForTesting().setXxxFactoryForTesting(...)`，断言与对外行为不变；新增 `BridgeTransportConnectorTests.swift` 6 条 connector 级定向测试，覆盖 `connectTransport` 非 CCCodeBridgeError 失败清理、`runDirectRace` 全候选失败聚合错误与 cleanup、relay factory 抛真实错误传播、direct factory 注入返回真实结果、generation superseded 拒绝 attempt。iOS 仓定向 52 用例全绿。
- **P2 baseline 下调 + strict gate（本仓）**：`scripts/hygiene-baseline.json` 下调为 lines=1629 / funcs=71 / forTesting=27（均低于 brief 目标 ≤1700 / ≤78 / ≤30）；`CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh` 通过（STRICT passed — no BridgeProvider net growth）。
- **P3 build / 真机安装启动（iOS 仓）**：定向 `xcodebuild test`（52 用例）+ Debug 构建在已连接物理设备 iPhone 16 Pro（UDID BFC431AC…）安装并启动成功（`Launched application with org.openagi.cordcode`）；未执行 UI automation / snapshot / 自动点击。

诚实口径：iOS 代码改动与定向测试发生在 `../cordcode-ios` 仓，baseline 下调与 strict gate 发生在 MacBridge 仓；两仓提交边界清晰（iOS 一条提交 + MacBridge 文档/gate baseline 一条提交）。connector 级测试覆盖了 T2/T3 的不变量（清理 + 不写 active state）；真实 socket 握手 / 真机肉眼连接路径核对仍按 brief 第 6 节归到 owner 人工验收清单。完整完成报告见 `docs/2026-07-04-architecture-health-third-round-development-brief完成情况.md`。

### 2026-07-04 — 架构健康第三轮开发 brief

- 新增第三轮开发 brief，明确第三轮主轴为 iOS `BridgeProvider` 的 `transport creation` 子域 extract-and-test，而不是继续扩大范围或直接拆 ChatViewModel。
- 规定先补 direct/relay attempt、未采纳 transport 清理、adoption 边界三类不变量测试，再提取 `BridgeTransportConnector.swift`；不改 protocol、pairing、Relay crypto、路径选择语义或 recovery ownership。
- 明确完成标准：`BridgeProvider.swift` 指标必须下降、MacBridge `hygiene-baseline.json` 必须下调并通过 strict gate，iOS 代码改动后按真机连接状态执行构建/安装/启动。
- 按独立评审修订 brief：修正不存在的 `attemptRelayConnection` 符号，补入 `runDirectRace` / `RaceTransportCollector` / `RaceResult` / `RaceCompletion` 等 transport-creation 层真实切片，定调本轮采用独立 `BridgeTransportConnector` 类型并纳入 direct race，量化目标为 `BridgeProvider.swift` lines ≤1700 / funcs ≤78 / ForTesting ≤30。
- 按第二轮评审补齐发车前澄清：P1 允许并预期改写现有测试的 factory 注入调用点到 connector 测试入口；`runDirectRace` 边界止于 `applyHelloAckLocalURLRefresh` 前；若 race 迁出阻塞，则 lines 目标挂起并暂停升级 owner，不接受降级提交。

### 2026-07-04 — 架构健康第二轮：web 共享包收口 5/5 + BridgeProvider 净增长 gate + handlers.go 物理分发

第二轮按 brief 推荐顺序 P0 → P2 → P1 执行，目标是止住恶化、降低第三轮拆分摩擦，不动 iOS god-object 本体。16 个 exec-plan 任务全部 proven done。

- **P0 web shared renderer 收口 5/5（代码在相邻 iOS 仓 `../cordcode-ios`）**：把剩余 3 个重复组件迁入 `shared-message-renderer`，共享包 exports 覆盖 DiffViewer/ToolBlock/ReasoningBlock/ProcessGroup/NarrativeBlock。
  - `ReasoningBlock`：2 行文案差异（中/英）通过 `host.labels` 注入；迁移后两 app 剩余 diff 实测仅 labels 值。
  - `ProcessGroup`：43 行真实差异（摘要文案 + 分类粒度 + 复数语义）通过 `summarizers` 注入保留，共享包首次引入 `components/turns/`。
  - `NarrativeBlock`：68 行差异（message-web 独有的 git directive summary）通过 `transformContent` 注入；共享包新增 react-markdown peer `>=9 <11` / remark-gfm peer `>=4 <5`，**9.x 与 10.x 跨大版本兼容经三包 typecheck/build 实测确认**。
  - 共享包新增 12 条定向 vitest（labels 注入 / summarizers / transformContent / DOM 契约）；三包 typecheck + build 全绿。message-web 视觉回归 owner 真机目测通过（2026-07-04，iPhone 16 Pro）；remote-web 靠对称薄 wrapper + typecheck/build。
- **P2 BridgeProvider 净增长 strict gate（本仓）**：新增 `scripts/hygiene-baseline.json`（冻结基线 lines=1967/funcs=88/forTesting=36）；`check-architecture-hygiene.sh` 增加 `CORDCODE_HYGIENE_STRICT=1` 分支——任一指标净增即 exit 1、允许减少、iOS 仓缺失时 graceful skip（不破坏 CI）；CI macbridge job 接入 best-effort 跨仓 checkout `openAgi2/cordcode-ios` + strict hygiene step。既有 5 个 inventory 段仍 warning-only，未被提升为 fail。
- **P1 handlers.go 物理分发（本仓）**：4559 行 `handlers.go` 拆出 `handlers_opencode.go`（488 行，OpenCode proxy 簇）+ `handlers_relay.go`（829 行，relay 簇含 brief 指定的 4 个 transcript 探测 helper 整组搬迁），`handlers.go` 降至 3269 行（-1290，-28%）。纯物理 move，不改函数体 / RPC 行为 / session registry / agent driver / protocol 字面契约；`go build` + 定向过滤 + 全量 `go test ./go-bridge/...` 全绿。

诚实口径：P0 三组件迁移代码与验证发生在 iOS 仓（typecheck/build/vitest），P2/P1 发生在 MacBridge 仓；react-markdown 跨版本兼容经三包实测而非穷尽 runtime 验证；`cordcode-ios` 已确认公开（无 auth 可读），CI strict gate 在每个 PR/push 实际执法。完整完成报告见 `docs/2026-07-04-architecture-health-second-round-development-brief完成情况.md`。

- **独立完成审计**：新增 `docs/2026-07-04-architecture-health-second-round-completion-audit.md`，复跑 exec-plan 结构核查、iOS 三包 typecheck/build/vitest、P2 strict gate（含模拟增长 exit 1）和 P1 Go build/tests。审计结论为通过，仅指出完成报告中 `required:true ×16` 应理解为 `verification.required=true ×16` 的低优先级口径修正。

### 2026-07-04 — 架构健康第二轮开发交接文档

- 新增第二轮开发 brief，基于第一轮 gap analysis 和讨论结论，把下一轮范围收敛为 web shared renderer 剩余组件迁移、`handlers.go` 物理分发、BridgeProvider 净增长 gate 试点。
- 明确第二轮不做 iOS god-object 大手术，第三轮再按子域 extract-and-test 启动本体拆分，避免“测试保护还不够”变成永久延期。
- 按独立评审修订 brief：移除不可复现的“漂移扩大”论据，补齐 `ProcessGroup` 路径、OpenCode handler 拆分、hygiene strict gate/CI 接入边界，并记录评审意见全部采纳。
- 按 r2 复核继续修正 P2 论据：移除不可复现的 `BridgeProvider` “78→88 func 增长”说法，改为静态 god-object、历史下沉点、无净增长门禁的可复现依据。
- 按 r3 清理复核微调 P1 拆分说明：列出 relay 簇内 4 个不带 relay 之名的 transcript 探测 helper（`detectClaudeTranscriptState` / `detectCodexTranscriptTaskState` / `scanCodexTranscriptTaskEvents` / `codexEventPayloadType`），要求整组搬迁以防反向依赖。r3 同时确认 brief 全部量化论据可在当前 `main` 复现，评审循环结束。

### 2026-07-04 — 架构健康第一轮整体完成（28/28 proven done）

本轮在 B2 删除 legacy config 包后收口，28 个 exec-plan 任务全部 done 且 proven。各批次交付：

- **A 能力单源化**：`backend_capabilities.go` 成为 BackendList 与 agent descriptor 的唯一能力推导源；Codex app_server 一致宣告 compression + question_reply；session_pagination 保持关闭。
- **B1 provider seed 解耦**：`provider_seed_config.go` 最小 TOML reader，`provider_switch.go` 切断对 legacy config 的生产依赖。
- **B2-predelete**：新增 `agent/providerseedtest/` 测试专用 loader，把 Claude/Codex 的 provider 测试从 legacy config 迁走。
- **B2（主项）**：删除 `config/` 包（4 文件，含 Weixin/Feishu/Web 后台等旧业务结构），中性化 `claudecode_test.go` 残留 Feishu fixture。
- **C web renderer 共享包**：iOS 仓 `shared-message-renderer/`，迁移 DiffViewer + ToolBlock，用 `host.post` 触发 openDetail/permissionAction/questionAction。
- **D god-object characterization**：iOS 仓加 `GodObjectCharacterizationTests.swift`（连接策略 + 生成周期边界），不拆 god object、只锁现状行为。
- **E 工程宪法**：`engineering-constitution.md` + `check-architecture-hygiene.sh`，warning-only 存量报告，不阻断 CI。

验证强度：

- **B2 验收**：Weixin/Feishu/业务符号扫描 0 命中、生产 config-import 0 命中、`go build`/`go vet`/全量 `go test ./...`（runtime 等价 PATH）全绿、relay-server 独立 module 绿、活文档无残留引用。
- **追加验证**（删除后更强确认）：Mac Release 重建+装机成功（commit `ea20d1ab4e0b`，启动 0 ERROR/WARN，8777 是 `/Applications` 内嵌 runtime）；iOS 从 `codex/web-renderer-shared-c1` 分支装到 iPhone 16 Pro；owner 真机功能冒烟通过。
- **诚实口径**：impl 类标 self-attested，命令类标 re-verified，功能性 UI 标 owner-verified（冒烟级，非穷尽）；Batch D 的 xcodebuild test 未本轮重跑，保留前序 re-verified。完整完成报告见 `docs/2026-07-03-architecture-health-execution-plan完成情况.md`。

### 2026-07-03 — 删除 legacy config 包（架构健康第一轮收口）

- **删除 `config/` 死重包**：移除约 6418 行的 legacy `config` 包（`config.go` / `config_test.go` / `config_repository.go` / `config_repository_test.go`）。删除前已确认它是孤儿包 — 生产代码、测试、cmd 入口均 0 处 import，`config.X()` 调用方为 0；仓内唯一引用是 `go-bridge/provider_switch_test.go` 的静态反回归守卫字符串（非真实 import）。
- **不再携带与 CordCode Link 无关的历史业务写入能力**：随包移除 Weixin/Feishu 平台凭据写入、Web 管理后台、Cron/Webhook/TTS/Hook/Speech 等旧一体仓（cc-connect）时代的结构，缩小运行路径维护面。owner 已确认这些能力不再维护。
- **删除后验收全绿**：`go build ./...` / `go vet ./...` / 根 module `go test ./... -count=1`（runtime 等价 PATH）全通过；Weixin/Feishu/业务符号与生产 config-import 扫描零命中；`go.mod` 的 `BurntSushi/toml` 保留（B1 的 provider seed reader 仍在用）。架构健康执行计划第一轮 28/28 todo 全部 proven done，正向完成报告见 `docs/2026-07-03-architecture-health-execution-plan完成情况.md`。

### 2026-07-03 — B2 删除前解除 provider 测试对 legacy config 的依赖

- **provider 集成测试不再 import legacy config 包**：Claude Code / Codex 的 provider 相关测试改为通过轻量 provider seed test helper 读取 `.cc-connect/config.toml`，保留真实配置存在才运行、缺失则 skip 的行为。
- **删除前证据更清晰**：新增 provider seed test helper 覆盖 provider refs、agent type 过滤、agent-specific endpoint/model、Codex headers 与 `${ENV}` 展开；静态防回归测试扩展到 agent provider 测试文件，避免删除 `config/` 前又引入 test-only 依赖。
- **仍未删除 `config/` 包**：B2 删除本体继续等待删除前审计和 owner 对旧业务写入能力不再维护的确认。

### 2026-07-03 — 架构健康治理第一轮：能力单源化与 provider seed 瘦身

- **后端 capability 宣告改为单一来源**：Management API 与 `hello_ack.backends[]` 现在共用同一套能力推导，Codex `app_server` 模式会一致宣告 `compression` 与 `question_reply`，避免客户端从不同入口看到不一致能力。
- **保持风险能力关闭**：`session_pagination` 仍不宣告；OpenCode/Codex 仍不宣告未实现的 `permission_resolve`，避免 UI 误启用不可用路径。
- **切断 go-bridge 对 legacy config 包的生产依赖**：provider seed 读取改为 go-bridge 内部最小 TOML 结构，保留 `.cc-connect/config.toml` 的 provider refs、work_dir/base_dir 匹配、active provider、models/env/Codex headers 映射，降低旧 Weixin/Feishu 等历史业务结构对运行路径的维护压力。
- **新增 warning-only 工程卫生检查**：新增工程宪法与 `scripts/check-architecture-hygiene.sh`，把日志、本地化、`ForTesting`、长文件和 protocol 同步规则变成可见的存量报告；当前只提示不阻塞 CI，避免在债务清零前制造硬失败。
- **补齐 web renderer 共享包施工设计**：新增 batch C 设计实施文档，限定第一轮只迁移 `DiffViewer` / `ToolBlock` 与稳定类型，要求用 host adapter 隔离 iOS WebKit 与 remote-web 宿主差异。

### 2026-07-03 — 活文档对齐当前 CordCode Link 架构

- **修正文档中的旧品牌与命令**：根活文档、安装说明、release checklist 和 README 统一使用 `CordCodeLink.app`、`cordcode-bridge-runtime`、`cordcode-relay` module 与当前 `/opt/cordcode-relay/bin/relay-server` 部署路径，避免照抄旧命令找不到 runtime 或 Relay 备份。
- **补齐当前运行态说明**：OpenCode 默认 `managed_local`、Codex transcript relay、Claude streaming partial、`transcriptindex` 分页索引、runtime 自愈规则、`hello_ack.currentURLs.locals`、Web QR `/web/` 静态部署和 capability 来源差异已写回活文档。
- **提升维护可恢复性**：CI、runtime.json `bridgeEpoch`、Relay nginx 要求和 OpenCode 排障路径更新为当前实现，后续 agent 不需要从过程文档反推最新架构。

### 2026-07-03 — 修复 OpenCode 连续 turn 流式收口抖动

- **OpenCode 连续问答的完成事件按 turn 复位**：OpenCode SSE 订阅在同一 session 进入新 user/running 状态时清除上一轮 completion 去重，避免第二轮开始后 completion 被 session 级状态吞掉。
- **避免历史轮询后伪造完成造成状态条闪烁**：OpenCode 事件 relay 不再使用空闲超时自动补 `turn_completed`，减少生成结束后的 runtime status strip 重复亮灭和布局抖动。

### 2026-07-03 — OpenCode Automatic managed local server 实现

- **OpenCode 新装默认改为 Automatic（managed_local）**：CordCode Link 会自动启动并管理一个只绑定 `127.0.0.1` 的 `opencode serve`，选择并持久化 `4096...4196` 范围内的端口和随机 Basic Auth 凭据；iOS 仍只连接 MacBridge，不直连 OpenCode。
- **Desktop 与 iOS 自动对齐同一 OpenCode scope**：MacBridge 写入 OpenCode Desktop 默认 server、`currentSidecarUrl` 与 `projects[managedURL]`，优先合并 Desktop `local` 项目集合；本机实测确认 Desktop 运行中不热重载配置，因此实现保留 Cocoa graceful quit + reopen fallback，且冷启动会服从写入的 managedURL。
- **失败保持真实可见**：managed server 启动失败不会阻塞 Bridge，Claude/Codex 继续可用；OpenCode 则保持未配置/不可用诊断。`opencode-managed-server.err.log` 独立脱敏滚动，password 不进入 argv。
- **验证**：新增 `OpenCodeManagedServerTests`，更新 OpenCode source 迁移与 Go managedURL scope 回归测试；Swift OpenCode 定向测试与 Go OpenCode list_projects/list_sessions 定向测试通过。

### 2026-07-03 — OpenCode 无缝接入 managed local server 方案

- 产出本地 managed local server 开发规格，把 OpenCode 最终目标从手动 `external_http` 配置细化为 CordCode Link 自动托管本机 OpenCode shared server、自动同步 Desktop 默认 server 与项目 scope、iOS 扫码后直接看到 Mac 端 OpenCode Desktop 项目/session 的实现路径。

### 2026-07-02 — 修复 OpenCode Desktop 切到 external_http 后项目列表为空

- **修复重启 OpenCode Desktop 后项目/session 看起来消失**：CordCode 同步 Desktop 默认 server 到 `external_http` endpoint 时，现在会优先把 Desktop `local` scope 下的完整项目集合迁移到新 endpoint key，并用旧 active server / legacy `64667` 只补充缺项，避免 Desktop 重启后进入一个没有项目历史的新 server scope。
- **保留并合并已有 external_http 项目状态**：如果目标 endpoint 已经有项目列表，会按 worktree 去重后合并 `local`/旧 server 的缺项；`lastProject` 已存在时不覆盖，避免用户手动整理过的 Desktop 状态被回滚。

### 2026-07-02 — OpenCode 项目列表跟随 Desktop 打开的 workspace

- **修复 iOS OpenCode 模式显示大量已关闭项目目录**：OpenCode `/project` 是历史 catalog,会包含 Desktop 里已经手动关闭的项目；Desktop 侧栏真正打开的项目保存在本机 `opencode.global.dat` 的 `server.projects[scope]` 数组中。MacBridge `list_projects` 现在按 Desktop 源码语义读取该数组,只向 iOS 返回仍在数组里的 opened projects 并保留 Desktop 顺序；`expanded=false` 仅代表 Desktop 侧栏折叠状态,不再被误判为关闭。读不到 Desktop 状态时才保留原 `/project` catalog 作为诊断事实。
- **项目名跟随 Desktop 元数据**：返回项目时优先使用 `/project` metadata 里的 `name`,没有元数据时退回目录 basename,对齐 Desktop 的 `displayName(project) = project.name || basename(worktree)`。
- **OpenCode session 列表对齐 Desktop 加载方式**：目录级 session list 允许 `rootsOnly + limit`,MacBridge 转发为 `x-opencode-directory + ?limit=N`,并按 Desktop 的保守策略在 array response `len == limit` 时标记“可能还有更多”；仍拒绝 `rootsOnly + cursor`,因为 Desktop 当前并不使用 session cursor 做 sidebar 加载。

### 2026-07-02 — OpenCode list_sessions 提高 limit 上限以支持完整项目拉取

- **OpenCode `list_sessions` 的 limit 上限从 50 提高到 1000**：OpenCode server 是 array-only(无 cursor/无 total),一次性返回 `min(limit, total)`,唯一能控制取回量的就是单次 `limit`。原 50 上限会让真实项目(观测到 459 条)被截断且无法翻页。提高到 1000 后客户端可一次拉满整个项目;对小项目无副作用(服务端只返回实际总数)。

### 2026-07-02 — OpenCode project-first session 列表分页协议

- **OpenCode `list_sessions` 改为目录级 page**：go-bridge 的 OpenCode proxy path 现在接收 `directory + limit + cursor`，向 upstream 发送 `x-opencode-directory` 和非空 query 参数，并继续返回既有 `{ sessions, nextCursor, hasMore }` envelope；array-only OpenCode 1.17.13 轨道不伪造 `hasMore`。
- **避免 global dump 误当全项目 catalog**：bare `/session` 仍只代表 `global`，项目桶必须走目录 scoped 请求；当前实现已按 Desktop 方式允许 `rootsOnly + limit`,但仍拒绝 `rootsOnly + cursor`,避免 cursor page 后再 client-side 过滤造成漏页。
- **诊断更清楚**：OpenCode list 日志新增 directory、limit、cursor-present、result-count、next-cursor-present、duration，便于从 `go-bridge.log` 验证冷启动请求预算。
- **协议文档同步**：`list_sessions` 分页与 `get_session_messages` 的 `session_pagination` capability 明确拆开；列表 cursor 是 backend/project/directory scoped opaque 值，客户端不得解析或跨 scope 复用。

### 2026-07-02 — OpenCode 共享本地服务接入（Phase A 显式 external_http）实现

- **移除对固定 `127.0.0.1:64667` 的隐式默认依赖**：OpenCode backend 改为显式 **Server Source** 模型（External HTTP / Legacy 64667 / Service discovery (future) / Disabled）。`go-bridge` 的 `-opencode-url` 默认改为空，`agent/opencode` 不再 fallback `http://localhost:64667`；endpoint 未解析时 descriptor 报 `not_configured`，绝不 dial `64667`。
- **External HTTP：bring-your-own-server**：CordCode 连接用户/运维启动的 stable `opencode serve`（loopback + Basic Auth），不启动也不保活它。Swift 端 `OpenCodeEndpointResolver` 规范化 URL（`localhost`→`127.0.0.1`，拒绝非 loopback/https），`OpenCodeHealthValidator` 先以 no-auth `/global/health` 证明 server 要求认证（必须 401）再做 authed 校验；无密码 server（no-auth 200）默认拒绝为 `server_unauthenticated`。
- **RuntimeManager 显式传 URL、凭据走 env**：argv 增加 `-opencode-url <url>`（URL 非 secret），password 仍经 `OPENCODE_SERVER_PASSWORD` 环境变量传递，不进 argv / 日志；argv/env 构造提取为可测试 static。Desktop 默认 server 配置同步到 resolved endpoint URL，不再固定写 `64667`，且去重保留用户其它 server。
- **升级连续性与新装默认**：存量 `credentials.json` + 无显式 source 自动一次性迁移到 `legacy_64667` 并提示改配 external_http；全新安装默认 Disabled。`legacy_64667` 是唯一允许带 `legacy_insecure_unverified` 警告继续运行的兼容例外。
- **iOS/Desktop 共享同一 OpenCode server**：消除过去 Desktop `vlocal` 与 iOS 固定 `64667` 分裂成两个项目/session scope 的问题（不修改 OpenCode 源码；不抓取 Desktop sidecar 密码）。
- **验证**：新增 `OpenCodeEndpointResolverTests`（18 例，URL 规范化/解析/迁移）、`OpenCodeHealthValidatorTests`（10 例，no-auth/authed 区分、schema、timeout、legacy 例外）、`MacBridgeBehaviorTests`（argv/env/Desktop 配置 6 例）、Go `TestDetectAgentStatus_OpenCode*` + `TestShouldStartPassiveSubscription`。Debug 构建 + 全部定向单测通过；既有 9 个 codex pagination 测试因本机未装 `codex` CLI 的环境原因失败（已确认在 clean main 同样失败，非本次回归）。

### 2026-07-02 — OpenCode 共享本地服务接入方案文档

- 新增 `docs/2026-07-02-opencode-shared-service-discovery-plan.md`，明确“不修改 OpenCode 源码”前提下不能直接发现 Desktop `vlocal` sidecar 密码；当前 stable 可开发路线改为显式 `external_http` 共享 server，并保留未来 `opencode service` discovery 的能力门控入口。
- 方案给出开发任务、失败模式、安全约束和验证清单，目标是移除 CordCode 对固定 `64667` 的默认依赖，避免 OpenCode Desktop 与 iOS 项目/session 分裂；经评审后修订为 stable-compatible 分阶段方案：当前可开发的是显式 `external_http` 共享 server，`opencode service` discovery 因 stable 1.17.13 未暴露 `service`/`--register` 被降级为 future-gated。第二轮补充 Phase A 认证实测、bring-your-own-server 持久化边界、存量用户升级默认迁移到 `legacy_64667` 的连续性规则；第三轮补强 passwordless guard，要求 no-auth `/global/health` 返回 200 的 OpenCode server 默认拒绝，并把 `legacy_64667` 明确为带警告的兼容例外。
- 修复 OpenCode backend 状态检测仍探测旧 `/health` 的问题；现在 descriptor 与方案一致使用 `/global/health` 并校验 `healthy/version` body，避免 shared server 已可用但 iOS/MacBridge 仍显示 OpenCode 未启动。
- 修复 OpenCode 模式项目与目录选择回归：`list_projects` 兼容 OpenCode 1.17.13 的 `worktree` 字段并映射为 iOS 需要的 `directory`；`list_directory` 在 OpenCode RPC 分支中重新走通用目录浏览 handler，恢复 iOS 手动添加项目目录能力。

### 2026-07-01 — Codex 外部 session 结束后 iOS 执行态快速收口

- **修复 Mac 端 Codex 任务完成后 iOS 输入框十几秒才恢复**：当 iOS 旁观 Mac 端 Codex session 时，go-bridge 现在会监视 Codex JSONL transcript 中真实的 `task_started` / `task_complete` 事件；`task_complete` 到达后立即广播 `turn_completed` + `session_state_changed: idle`，让 iOS 走 500ms 终态 debounce，而不是等待 history probe 的多轮 unchanged 兜底。Codex transcript relay 与标准 `AgentSession` relay 使用独立 lifecycle key，可覆盖 registry 里已有 session 但标准事件流收不到外部最终事件的情况。
- **保持长工具执行不闪断**：该 relay 只使用 Codex transcript 的真实任务生命周期事件，不把工具静默或历史无变化当成结束；长时间 `sleep` / verify / build 期间仍保持 running。
- **验证**：新增 go-bridge 单测覆盖 Codex transcript task state 解析。

### 2026-07-01 — Claude Code 模式支持流式打字机输出

- **修复 Claude Code 后端回复整段出现**：MacBridge 启动 Claude Code CLI 时启用 `--include-partial-messages`，并消费 `stream_event/content_block_delta`，将 token 级文本转成统一 `text_delta` 事件下发给 iOS。
- **避免 checkpoint 重复与多段文本丢失**：Claude CLI 会在 token delta 后再发完整 assistant checkpoint；driver 现在按 content block index 去重，正常路径不重复，尾部差量会补齐，异常非前缀 checkpoint 通过 `message_updated` 发送完整文本真值。
- **工具调用与历史重放边界保持真实**：`input_json_delta` 不作为文本重复下发，工具仍由最终 `tool_use` block 产出；`--resume` 历史重放期继续抑制 live 事件，避免把历史内容当作实时流灌给 iOS。
- **验证**：新增真实形状 JSONL fixture 与 claudecode driver 单测，覆盖首个 checkpoint 不清 partial、多 text block、尾差量、非前缀 reconcile、message id 切换和 historyDraining。Sonnet/GLM-5-Turbo 路径已用真实 Claude CLI 证明 `--include-partial-messages` 会产出 `stream_event`；Opus/GLM-5.2 路径仍返回本地 gateway `529 overloaded`，作为 provider route 问题单独处理。

### 2026-06-30 — iOS 新建的 Claude Code session 出现在 Mac IDE/桌面会话列表（修复 entrypoint 过滤）

- **现象**：iOS Claude Code 模式**新建** session 发消息，iOS 正常收到回复且消息持续可见；但 Mac 端 VSCode Claude Code 扩展（及 Claude 桌面 App）的会话列表里**找不到这条 session**，重启 Mac App 仍找不到。
- **根因（已用磁盘 + claude 二进制证据坐实，非数据丢失）**：claude 给 stream-json 方式 spawn 出来的 session 打标 `entrypoint=sdk-cli`（这是 MacBridge 不设 `CLAUDE_CODE_ENTRYPOINT` 时的默认）。Anthropic 的 IDE/桌面会话列表**按 entrypoint 过滤，只显示各自创建的**：VSCode/JetBrains 扩展 = `claude-desktop-3p`，桌面 App = `claude-desktop`。于是 `sdk-cli`（MacBridge 创建）的 session 即便 JSONL 完整落盘在 `~/.claude/projects/<cwd-hash>/<uuid>.jsonl`、内容正确、可被 `claude --resume <id>` 打开，也被这些列表排除——重启无效，因为是 entrypoint 过滤而非缓存。取证：owner 的 Chat 项目下 30 条 session 干净二分为 `claude-desktop-3p`（24，VSCode 可见）+ `sdk-cli`（6，MacBridge 创建不可见），其余字段完全一致；claude 二进制含 `CLAUDE_CODE_ENTRYPOINT` 环境变量与各 tag 值。
- **修复**：[`runtimeEnvLocked`](agent/claudecode/claudecode.go) 给 claude spawn env 注入 `CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p`（用 `core.MergeEnv` 覆盖+去重，确保始终生效）。MacBridge 本身就是第三方 host（"3p"），打这个标签语义正确，且与 IDE 扩展自创 session 同标签 → 出现在 **VSCode 扩展**的会话列表。实测设该 env var 后，新 session transcript 的 `entrypoint` 字段确为 `claude-desktop-3p`。
- **范围与边界**：仅影响**新建** session；已存在的 6 条 `sdk-cli` 旧 session 仍不在列表（未原地改写 transcript）。本修复只决定 transcript 的展示标签，不影响 iOS 端可见性、消息收发或持久化。
- **已知不可修 surface**：独立 **Claude 桌面 App**（`/Applications/Claude.app`）即便 session 同标签也不显示 iOS/MacBridge 创建的 session。取证：桌面 App 自带独立 Claude Code runtime（`~/Library/Application Support/Claude-3p/claude-code/2.1.187/`），其可见 session 与 iOS session 的 `entrypoint` 都是 `claude-desktop-3p`（仅 `version` 2.1.187 vs 2.1.185 之差），说明桌面 App 不按 tag/字段过滤，而是用自有 Electron 会话索引（IndexedDB）只收录经它自己创建的 session，不扫 `~/.claude/projects/`。这是桌面 App 的产品设计，非 MacBridge 可修；iOS 创建的 session 仍可经 **iOS / VSCode 扩展 / 终端 `claude --resume <id>`** 访问。
- **验证**：定向 Go 单测 `TestRuntimeEnvTagsClaudeEntrypointForIDEVisibility` / `TestRuntimeEnvClaudeEntrypointOverridesAndDedupes` 守护；Release 重建 + 覆盖 `/Applications` 安装 + 重启完成，新 runtime 已起（`cordcode-bridge-runtime` 二进制含该 env var）。**端到端：owner 已验收 iOS 新建 Claude session 出现在 VSCode 扩展列表；Claude 桌面 App 不显示（见上，已知不可修）。**

### 2026-06-30 — Claude Code 模型列表以 `~/.claude/settings.json` 为权威源（修复网关场景下选 GLM 仍 529）

- **现象**：iOS Claude Code 模式选 GLM-4.7 发消息，claude 回报 `Repeated 529 Overloaded … inference gateway (127.0.0.1:15721)`，即使 GLM 无速率限制。
- **根因**：`AvailableModels`（[`agent/claudecode/claudecode.go`](agent/claudecode/claudecode.go)）从 provider/网关 `/v1/models`/硬编码取模型，不读 owner 的 `~/.claude/settings.json`。该文件的 `env` 块是一张别名表：`ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL`（真实 id，如 `claude-haiku-4-5`）+ `*_MODEL_NAME`（显示名，如 `glm-4.7`）。网关收到 `claude-haiku-4-5` 才路由到 GLM-4.7，"glm-4.7" 只是显示名。旧逻辑下 iOS 拿到的模型 `providerID="default"`，被 iOS 的 `providerID=="claude"` 过滤丢弃 → 不带 model 发送 → claude 无 `--model` → 网关默认路由 → 529。
- **修复**：
  1. 新增 [`agent/claudecode/settings_models.go`](agent/claudecode/settings_models.go)：读 `~/.claude/settings.json`（`CLAUDE_CONFIG_DIR` 优先），把三对别名映射成 `ModelOption{Name=claude 别名(haiku/sonnet/opus), Desc=*_MODEL_NAME}`；mtime 懒重载（不引入 fsnotify、不启后台 goroutine）；缺失/无映射返回 nil 走 fallback。`AvailableModels` 优先用它。只读 `*_MODEL`/`*_MODEL_NAME`，不把 `ANTHROPIC_API_KEY`/`AUTH_TOKEN` 等密钥反序列化进程序变量。
  2. [`modelProviderForAgent`](go-bridge/handlers.go) 对 `claudecode` 显式返回 `providerID="claude"`（语义正确，且让 iOS 的过滤通过）。
- **映射契约**：`Name`（送 `claude --model`）= 别名，claude 按 settings.json 解析成真实 id 送网关——与 owner 顶层 `"model":"opus"` 同机制。iOS 显示 `*_MODEL_NAME`（glm-4.7）、发送别名（haiku）。iOS 侧无需改动。
- **验证**：定向单测 `TestSettingsModels_*`（5 项）通过；go-bridge model/provider 测试无回归；Release 重建 + 重启完成，新 runtime `runtime_ready`。端到端（iOS 选 GLM-4.7 不再 529、收到回复）由 owner 真机验收。详见 `../cordcode-ios/docs/2026-06-30-claudecode-models-from-settings-json.md`。

### 2026-06-30 — 修复 iOS 向已存在 Claude Code 会话发消息时 go-bridge 崩溃（消息被吞）

- **现象**：iOS 打开一个 Mac 端已存在的 Claude Code session 并发送消息，iOS 收不到回复、Mac 端刷新也看不到这条消息（重启后 iOS 也丢失）。
- **根因（已用 `go-bridge.log` 坐实）**：iOS 打开会话时，`claudeSessionFileRelay`/session-state 事件会先对该 sessionID 调 `markRunning`，在 `sessionRegistry` 留下 `session==nil` 的占位 `trackedSession`（用于状态追踪）。`getSession` 对它返回 `(nil, true)`；`handleSendMessage`（[handlers.go:1817](go-bridge/handlers.go)）只判 `ok` 就调用 `sess.Send`（[:1887](go-bridge/handlers.go)），对 nil 接口派发 → **panic**，HTTP 连接被 net/http 回收，`send_message` RPC 永不返回结果，消息也没送达 agent。
- **修复**：`handleSendMessage` 的首次 session 查找改为 `if !ok || sess == nil`（与同函数二次检查 `existingOk && existing != nil` 一致），把 nil 占位当"未持有真实会话"，回落到 `StartSession(ctx, realID)` 即 `claude --resume` 正确续接。`getSession`/`markRunning` 的状态追踪契约不变（`ok` 仍表示 trackedSession 存在），避免影响 `resume_session` 的 runtimeState 等既有行为。
- **验证**：定向 Go 单测 `TestClaudeSendMessageWithNilStubDoesNotPanic` 复现并守护；go-bridge 全量测试通过（除 4 个因本机未装 `codex` CLI 的环境性失败）。Release 构建 + 覆盖 `/Applications` 安装 + 重启 MacBridge 完成，新 runtime 已 `runtime_ready`。真机端到端（发消息收到回复、Mac 能看到）由 owner 验收。

### 2026-06-30 — Codex 历史 session 下发用户图片附件

- **修复 Codex 历史消息丢失 `input_image`**：Codex JSONL 的 rich history 解析现在会把用户消息里的 `input_image.image_url` 转成协议 `files/parts`，iOS 打开历史 session 时可拿到 Mac 端 Codex 已显示的真实图片。
- **相同 prompt 文本的多图消息保持区分**：图片 file id 由图片 URL 稳定派生；多条同文案用户消息各自携带不同图片时，下发结果不会把后续图片压成前一张。

### 2026-06-30 — send_message 转发图片/文件附件到 agent（跨仓联动）

- **修复 iOS 发来的图片/文件附件被 go-bridge 丢弃**：`SendMessageParams` 原无 attachments 字段，`handleSendMessage`（主路径）与 `ocHandleSendMessage`（opencode 路径）都硬编码 `sess.Send(content, nil, nil)`，导致 agent 永远收不到图。现新增 `AttachmentInput` + `splitAttachments`（`go-bridge/attachments.go`）：按 unified-bridge-protocol 的 `AttachmentInput{kind,mime,filename,base64}` 解码 base64，按 kind/mime 拆成 `core.ImageAttachment`/`core.FileAttachment` 传给 `sess.Send`。三个 driver（claudecode/codex/opencode）都已消费图片+文件，故对所有 backend 生效；非法/空 base64 附件被丢弃不伪造。
- 与 iOS 侧联动：iOS 端 bridge client 现按协议上送 attachments（见 cordcode-ios CHANGELOG）。协议 spec（`docs/protocol/unified-bridge-protocol.md`）早已定义 attachments，本轮补齐传输层实现。

### 2026-06-29 — Codex prompt 模板发送不再依赖共享 4141

- 修复 iOS 在 Codex 模式点击「继续任务 / 总结当前状态 / 只跑相关测试 / 解释失败原因」后报 `codex app-server ws dial ws://127.0.0.1:4141 ... connection refused` 的问题：MacBridge 产品默认不再强制注入共享 Codex app-server URL，未显式配置 URL 时改走 go-bridge 已有的 stdio app-server session 路径。
- 当未配置共享 app-server URL 时，Codex descriptor 会标记为需要历史轮询，避免继续假定存在进程级 passive websocket 事件流；显式配置共享 URL 的高级用法仍保留原 websocket/broadcast 行为。

### 2026-06-28 — Claude Code effort 真值源 + iOS 覆盖持久化

- **修正 Claude Code session effort 同步此前实际不生效**：上一轮虽已把「当前 Claude runtime effort」回填进历史 session，但 MacBridge 的 Claude runtime effort 此前没有任何来源（macOS App 不配置、iOS 仅在发消息时回传），恒为空，导致 iOS 仍显示「自动」。现已改为启动时从 `~/.claude/settings.json` 的 `effortLevel`（Mac 端 Claude Code 的真实全局 effort 偏好；回退到同文件 env 的 `CLAUDE_CODE_EFFORT_LEVEL`）读取并注入 Claude runtime，因此打开任意 Claude Code session 都能显示与 Mac 端一致的智能等级（如 `Extra High`）。
- **iOS 显式改动的 effort 现在跨重启持久化**：当 iOS 发消息时显式改变 effort 且与当前值不同，MacBridge 会把该选择原子写入数据目录的 `claude-effort.json`，重启后优先于 `settings.json` 生效；未显式改动时仍以 `settings.json` 为准。
- 背景澄清：Claude Code 的 transcript 不记录 per-session effort（已抽样确认），故「某历史 session 当时的 effort」不可恢复；MacBridge 能忠实反映的是「Mac 端 Claude Code 当前的全局 effort」及其在 iOS 上的最近一次选择。

### 2026-06-28 — Claude Code session 同步模型与智能等级

- Claude Code 历史 session 列表和单 session 查询现在会把 MacBridge 当前 Claude runtime 的 reasoning effort 补入缺少 effort 元数据的旧 transcript，iOS 打开这些 session 时可显示与 Mac 端一致的模型和智能等级（如 `glm-5.2` + `Ultra`）。
- Claude Code effort 枚举补齐 `low`、`medium`、`high`、`xhigh`、`max`、`ultra`，并兼容旧的 `ultracode` 输入为 `ultra`。

### 2026-06-22 — 修复外部进程会话状态同步缺陷 (运行态同步)

- **修复被动订阅事件流下的会话运行状态更新缺失**：在 `go-bridge` 的 `startPassiveSubscription` 事件监听中，当监听到外部 Agent 独立进程产生的 `turn_started`、`turn_completed`、`session_state_changed`、`session_status_changed` 等代表运行态变化的事件时，实时将状态同步更新到 `h.sessions` 缓存中，并增强 `sessionRegistry` 中的状态标记，允许外部独立进程在缓存尚未注册时自动补齐临时状态。由此解决 iOS 客户端连接并切换到新开运行中会话时，由于 go-bridge 返回的 runtimeState 为空而导致 iOS 侧输入框保持简易模式且思考不展开的问题。

### 2026-06-19 — 恢复拆仓时遗失的维护文档

- 从原一体仓库迁回 MacBridge 构建安装、runtime/端口诊断、go-bridge backend 进程模型和 Relay 部署资料，并作为仓库根目录活文档维护。
- 按当前内嵌 runtime、持久日志、Management API、HPKE Relay、TLS pin 与独立 Go module 状态重写，删除旧 Node Bridge、外部 cc-connect replace、Copilot sidecar 和 FRP 默认路径等过时说明。
- 将共享 `CLAUDE.md` 中的 VPS 主机与用户示例改为占位符，避免项目指南携带机器专属部署值。
- 补齐新 session 冷启动规则、Release 覆盖安装条件，以及 Claude 独立进程、Codex 共享 app-server `4141`、OpenCode HTTP/SSE `64667` 的运行与排障模型；修正失效的 Relay 首装链接和已迁出钥匙串的 identity 描述。
- 对原一体仓库五份关键根文档做逐节覆盖审计，补回 runtime flags/env、完整故障树、事件管线、registry/rebind、离线通知、OpenCode hybrid 路由矩阵、架构约束与旧 VPS/FRP 自定义路径的现行边界。
- 在 `CLAUDE.md` 建立维护入口，并链接 iOS 侧端到端交互文档，减少后续排障只看单仓库导致的误判。

### 2026-06-19 — 修复撤销授权对 relay 连接不生效（安全）

**问题**：在管理 UI 撤销某台 iPhone 的设备授权后，若该 iPhone 走 relay 加密通道连接（默认推荐的远程路径），撤销不会即时生效——iOS 仍能继续访问 Bridge、拉取会话内容，只有杀 App 重启后才进入扫码页。

**根因**：`DeviceConnRegistry`（撤销时负责下发 `device_revoked` 事件并断开连接的注册表）此前只在 direct 直连路径（`server.go`）注册连接，relay 路径的 `RelayDeviceConn` 从未注册进去。撤销授权调用 `DisconnectDevice(deviceID)` 时在注册表里找不到 relay 连接，既不发事件也不断开。

**改动**：
- `DeviceConnRegistry` 的连接存储从 `[]*Conn` 改为 `[]Connection`（接口），让 direct 与 relay 两种连接类型都能注册。
- relay 连接在认证成功后注册到注册表；在 stale 清理、心跳半开清理（`pruneDeadDevice`）、`closeConn`、`Close` 四处连接移除点同步注销，避免撤销时对已关闭连接发事件。
- `DisconnectDevice` 在下发 `device_revoked` 事件后补 `conn.Close()`，确保即使客户端未及时处理事件，连接也被强制断开（此前 direct 路径仅发事件不 Close）。

**提升**：撤销授权对 direct 与 relay 两条路径行为一致、即时生效。新增 `device_conn_registry_test.go` 覆盖接口化存储、多连接撤销、注销隔离（相关测试 3/3 通过）。

### 2026-06-19 — Relay 凭据迁出钥匙串，消除重装后授权弹窗

**问题**：每次重装 MacBridge 后打开 App，macOS 弹出「CCCode Bridge 想要使用你储存在钥匙串的机密信息」并要求输入登录密码。根因是 App 走 ad-hoc 签名，钥匙串按代码签名 / Team ID 授权访问，重装后 Team ID 变化即判定为陌生应用、触发授权弹窗。这对「下载即用」的普通用户是不可接受的体验。

**改动**：Relay 的三份密钥（route credential、activation install id、activation 签名私钥）从钥匙串迁出，改用文件存储，与 OpenCode `credentials.json` 同目录（`~/Library/Application Support/CCCode Bridge/relay-secrets/`）、同样 `0600` 权限。

- **提升**：重装 / 升级后不再弹钥匙串授权窗口；无需开发者证书或稳定 Team 签名。文件存储的 0600 保护对「丢了可重新 provisioning」的 relay route credential 安全性足够。
- **一次性迁移**：存量用户首次启动新版时，若文件不存在且旧版钥匙串条目还在，自动读取旧值、写入文件、删除钥匙串条目——凭证无缝继承，不会因迁移丢值而触发重新 provisioning（后者曾导致 iOS 配对的端到端凭证与 Mac 端不一致、显示离线）。迁移为尽力而为：钥匙串读取失败（含用户拒绝授权）时不阻塞，直接新生成凭证。
- **全新安装无影响**：没有旧钥匙串条目的用户从头走文件存储，行为与旧版等价。

### 2026-06-19 — 深度运行期 Code Review 修复（commit `a85adf1f613e`）

本轮按 `docs/2026-06-19-deep-runtime-implementation-plan.md` 完成 11 项运行期修复（T01–T11），经独立审计（`docs/2026-06-19-implementation-完成情况-审计报告.md`）逐行反查源码 + 独立复跑全部测试通过。覆盖安全、进程治理、Relay 背压、跨进程契约、稳定性五类。

#### 安全
- **控制面凭据不再泄漏进 agent 子进程（T01）**：agent（Claude/Codex/OpenCode）及其工具子进程的环境从「全量继承 `os.Environ()`」改为 deny-list（`CCCODE_*`/`OPENCODE_SERVER_*`/`CLAUDECODE`）+ 运行时 allowlist 双保险。stderr 在进入日志/错误帧前统一脱敏。
  - **提升**：远程设备无法再通过 agent 工具（如 shell）读取到 go-bridge 的管理 token、relay 凭据，消除了从 data-plane 向 loopback 控制面横向移动的风险。
- **配对限流 bucket 无界增长治理（T08）**：新增惰性 TTL 清理 + 全局容量上限（4096），超限对新 key fail-closed。
  - **提升**：任意 pairingId/IP 无法制造无界内存增长。

#### 稳定性 / 进程治理
- **runtime shutdown 不再泄漏 agent 子进程（T02）**：新增幂等、deadline 约束的 `Handlers.Shutdown`，并发关闭所有活跃 session；main 的关停顺序修正为 HTTP Server → handlers.Shutdown → CloseAllConnections → relay/tls/mgmt。
  - **提升**：SIGTERM / 睡眠唤醒 / 长跑后不再残留 agent 子进程。
- **连接 Close 超时不再触发 events channel panic（T03）**：四条订阅路径（opencode/codex/sse/appserver）对齐范本——超时分支绝不直接 `close` channel，改由延迟 goroutine 等 producer 退出后再关。
  - **提升**：消除「连接 Close 超时后 producer 仍发事件 → closed-channel send panic 崩溃」。
- **Claude 改为进程组回收（T02）**：新增 `Setpgid` + 进程组 kill，对齐 codex。
  - **提升**：Claude 的 sudo/shell/插件子进程不再在 shutdown 后残留。
- **修复 Codex 重连测试自身数据竞争（T11）**：`closeCount` 改 `atomic.Int32`。
  - **提升**：`-race -count=20` 稳定通过，修掉实跑复现的 DATA RACE。

#### Relay 背压（独立 module，已部署生产 `wss://relay.byteseek.uk:8443`）
- **per-device 有界发送队列，消除跨 device 队头阻塞（T04）**：每个 device 一个有界队列（256 帧 / 8 MiB）+ 专用 writer goroutine；bridge 的投递从同步 write 改为非阻塞 enqueue，队列满则断开慢 device 并把当前帧落入 mailbox（不丢）。
  - **提升**：原先一个卡住的 device（满 TCP 窗口）会阻塞**同 route 所有 device** 的投递；现在慢 device 只断开自己，正常 device 照常收帧。

#### 跨进程契约（Swift）
- **连续 restart 收敛为单次启动（T05）**：`launchGeneration` + 可取消 `restartTask` + `applyConfigAndRestart`；100 ms 内连续多次 restart（配置变更 + Relay provisioning 回调）收敛为单次进程启动。
  - **提升**：不再端口反复接管 / ready frame 抖动。
- **runtime.json / management-token 写失败 fail-fast（T06）**：`WriteReadyFrame` 返回 error，写失败时发布 `bootstrap_persist_failed` + exit，绝不发布 ready。
  - **提升**：磁盘满 / 权限错误时不再进入「网络已开放但 UI 永远未就绪」的假死态（原先每 60 s 重启）。
- **management API 客户端短超时 + 轮询解耦（T07）**：专用 ephemeral `URLSession`（request 2 s / resource 5 s）替代 `URLSession.shared`；status 决定存活状态、agents 刷新改为独立低优先级任务。
  - **提升**：management 半开（accept 连接不响应）时 supervisor ≤ 5 s 进入恢复，而非卡死数十秒。

#### 架构债务（非用户可见，为后续铺路）
- **Handler 生命周期组件可整体关闭（T09）**：`ObservationManager` 的 lease loop 从构造函数移到显式 `Start(ctx)`；`StartCleanupLoop` 改可停 ticker。测试不再泄漏 goroutine。
- **god-object 最小治理（T10）**：新增实例级 `ConfigRepository`，旧包级全局标注 `Deprecated`。为后续拆分 `handlers.go` / `config.go` 铺路，本轮不拆大文件。

#### 前后对比

| 维度 | 之前 | 之后 |
| --- | --- | --- |
| 安全边界 | agent 可继承控制面密钥 | deny-list + allowlist 双保险，stderr 脱敏 |
| 进程生命周期 | shutdown 泄漏子进程、events panic | 统一 shutdown + 进程组回收 + 安全 close |
| Relay 可用性 | 单慢 device 拖垮整条 route | per-device 隔离背压 |
| Swift 状态机 | 双 restart、假就绪、mgmt 卡死 | generation 收敛 + fail-fast + 短超时 |
| 可测试性 | 全局状态、race、goroutine 泄漏 | 实例注入、atomic、显式 Start |

#### 验证
- `go build` / `go vet ./...` + relay-server build/test/race + Swift `xcodebuild build` 全绿
- 11 项定向测试全通过，无新增回归（pre-existing 失败：未装 codex CLI、`AvailableModels` 时序 flaky，已 baseline 确认与本轮无关）
- 完成：`docs/2026-06-19-deep-runtime-implementation-plan完成情况.md`；审计：`docs/2026-06-19-implementation-完成情况-审计报告.md`

---

> **维护说明**：后续每轮工作请在最上方（`[Unreleased]` 下）按相同结构追加一节，标题为「日期 — 主题（commit）」。发布正式版时把 `[Unreleased]` 改为对应版本号与日期，再新开一个 `[Unreleased]`。
