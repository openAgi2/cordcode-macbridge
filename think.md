# 后续计划索引（待办另案总账）

> 防丢：所有「已裁决另案 / 挂起待做」的计划在此各占一行。新裁决一项后续案时登记一行；
> 每轮收口/交接时核对「状态」列并更新。做完了就把整行删掉。

| 后续案 | 是什么 / 入口 | 前置依赖 | 状态（更新于） |
| --- | --- | --- | --- || iOS 发送 Grok 模型应用条目 id | iOS send_message 的 model 回发 transcript 底层 id（glm-5.3），目录外触发 unknown model id；Mac 端 a0b0f11 已软化兜底（消息可发出），根治需 iOS 改发 list_models 条目 id | 无（可与其它 iOS 任务合并） | Mac 兜底已部署（2026-09-02）；iOS 侧未开工 |
| Grok follower 交互升级 | iOS 无缝接力 Mac 端 grok 任务的根治路径（iOS 作为 leader 客户端，消息进同一 full-capability agent，per-client 能力路由 + mid-turn interjection）。入口 `docs/2026-08-28-grokbuild-leader-mode-design.md` §9/§15 D-3 | ~~source-first 冻结 follower 上行可用性~~ **前置调查已完成（2026-09-03，上游 72a61251）**：① 权限应答官方支持——`session/request_permission` 与 ask_user_question 同属共享交互集（server.rs is_interaction_request，广播全订阅者 + 缓存重放 + first-answer-wins），应答为标准 ACP response，上行无方法门、response 不被 id 改写触碰；② 插话官方支持——上行客户端消息全部无条件转发 agent（server.rs Acp 分发），mid-turn 语义 = server-authoritative 共享队列 + `x.ai/queue/interject`（expected_version 乐观锁，owner 仅归属非权限门），`x.ai/queue/changed` 全客户端广播；③ 无 writer 仲裁阻塞——协议只有 session_driver（管下行 driver-only reverse request 的路由），driver 断线自动转移，交互类刻意排除在 driver-only 外。共享集还含 exit_plan_mode / mcp/elicit 两类免费顺带。实现期仍需真实 wire fixture 锚定（checkout 72a61251 ≠ 安装版 5e9a5852，question 轨道已在安装版活体验证过路由） | **question 已交付**（2026-09-03 owner 矩阵四行全过：iPhone 可答/静默收口/断线恢复/关开关回退；四轮修复 1fc6f1f/fdb7d97/ceffc9c/8c1ac9b，复盘见下方 2026-09-03 条目）；**permission 已交付**（2026-09-03 commit 1661e91，§24 计划→exec-plan 三元组，Mac 单仓 iOS 零改动：leader 门加宽+registry kind/byWire+AnswerPermission+SessionPermissionResponder 路由；单测 5 例+包全量绿+Release 四门过；owner 真机矩阵四步全过——TUI 发起权限 turn→iPhone 卡出现、iPhone 允许→任务继续+Mac 弹窗自动收口、iPhone 拒绝路径、Mac 先答→iPhone 卡自动消失；grok TUI 5 档 vs iOS 允许/拒绝的选项差异为跨后端通用卡设计现状，范围扩展另案；另：grok 权限全放行排障教训——Ctrl+O 是 yolo 全放行开关、`[ui] permission_mode` 持久化，测试前两者都要处于 ask 态）；**exit_plan_mode + 选项透传已交付（2026-09-03 §25，owner 真机验收：程度可接受）**：plan 审批复用 §24 permission 管道（iPhone 允许→approved/拒绝→cancelled，标题取计划首个标题行，MCP 表单仍 observe-only）；权限卡选项透传（实收 options kind 动态映射 permissionActions：allow_always→「总是允许」三键卡、reject_always 有意不透传、execute→bash 等类别行；permissionOutcome kind 精确化 always→allow_always）；两轨（leader+OFF）同步；leader_plan_test 5 例 + leader_permission_options_test 6 例 + §24 兼容全绿；owner 后续裁决：iOS 当前两键计划卡（标题行+允许/拒绝）程度可接受；完整体验（计划文档全文展示+Mac 端全按钮集）属专门计划方案、须跨 backend 通用，本轮不编码，调研文档先行（指令已细化交写文档 agent）；interjection 后置 Phase B（iOS UI 产品决策，§24.6） |
| remote-web 集中测试轮 | 12 门浏览器端验收矩阵 + 4 web-push 取证门（owner 2026-09-02 裁决：先 iOS 任务 → 整体迁移 remote-web → 集中测试）。入口 iOS 仓 `.exec-plan/state/plan-4fe9645c3a36.json` 注记 | iOS App 端任务完成 + remote-web 整体迁移完成 | pending 非阻断；功能路径已真机验证过，16 门属迁移后回归确认（2026-09-02） |
| iOS 进入 Codex / DSH / Grok 计划模式 | 三条都是「Mac 进入计划 → iPhone 能批；iPhone 自己切不进」。Codex 入口文档 `docs/2026-09-04-codex-ios-plan-mode-entry.md`（owner 2026-09-04：先不做 iOS 开启 Codex Plan，后续再调研）。DSH = Mac 标准套餐 + `/plan`（`commands/execute`）；Grok iOS Plan 只写 agent 内存。禁止合成一个全 backend Plan 按钮 | Codex 批准路径已交付（Mac App Plan → iPhone 卡 → 批准实施，owner 真机 2026-09-04） | **挂起**（2026-09-04）；Codex 批准面已绿，入口未开工 |

## 2026-09-05 Claude Code 流式「假绿」复盘：deltaBatcher 丢 turnId + client uuid 官方解法

owner 三轮复测（无流式 → 重复 → 无流式且完成态无内容），第三轮修复 d5f5e30 部署后
仍无流式且切走再切回才见正文。生产日志（01:03 会话 93cd4a10「讲个法国笑话」）+
transcript + 源码三层根因：

1. **deltaBatcher 丢 turnId（假绿主犯）**：relayEvents 给 stdout 流式增量补的
   turnId（backfillClaudeStreamTurnID）经 33ms 攒批器时被丢弃——`delta_batcher.go`
   emit() 重组 data 只保留 `delta`+`itemId`，claude 增量恰好无 itemId → 出批后
   又是无身份增量，reducer 照旧跳过。stdout 全程在流（seq=1→1335）但投影正文为 0。
   **d5f5e30 的单测直接调 kernel.IngestLive，绕过了 batcher → 测试绿、生产挂。**
   教训：凡走攒批/重组中间层的事件管线，集成测试必须从 batcher 入口驱动，不许
   直连 kernel。
2. **双源同时哑火**：d5f5e30 把 file-relay assistant 行在 agent-relay 活跃期改
   cursor-only（stdout 权威），但 stdout 内容因 #1 全被跳过 → 期间无任何源写正文
   → 完成态广播空回合；切回触发全量 rehydrate 才恢复。「关掉兜底前必须证明主源
   真的通」，方向对但顺序错。
3. **resume drain 固定 10s**：长会话 resume 历史重放超 10s 窗口，每次发消息先烧
   10s 空白（user 行 01:03:30 才落盘，首 token 01:03:33.8）。

**官方机制实测（2.1.234 真样本，/tmp 探针 + frames.json）**：输入 user 帧带自造
`uuid` → **transcript user 行 uuid 就是它**（file-relay 建的 turn 身份 = 发送方
uuid，双平面身份统一）；**result 帧回盖 `user_message_uuid`**（2.1.234 盖收口帧；
SDK 0.3.260 契约新版盖首条回复帧）。这就是官方对「CLI 不回显 user 帧、stdout 无
身份」的解法——发起方自持身份，不再反查 ActiveTurnID 猜归属。t3code 对照：其
Claude 集成不是 ACP（ACP 只用于 Cursor/Grok），是官方 npm SDK 内嵌 Node + 发起方
自造 turnId + 自有 DB 做 SoT、transcript 不当事件源。

修复方案（owner 2026-09-05 拍板）：① batcher 透传 turnId；② Send 自造 client
uuid 写输入帧，stdout 事件（含完成差量）以此作 turnId，result 的
user_message_uuid 校验收口，ActiveTurnID 反查降级兜底；③ drain 事件驱动
（前提：resume 重放期不含 stream_event，需探针取证）。

## 追记：双序号域 + 单源门范围错误（第四/五轮复测）

**双序号域幂等门**（全链路测试抓到的第二层）：Claude source batch 在 Kernel 锁内
自取 PerSessionSeq、live 事件走 publisher 独立计数器——两域打同一 reducer 幂等门
（seq ≤ lastAppliedRev 即跳）。file-relay user 行 batch 先行推高 rev 时，后到的
stdout 流式 delta 即使带身份也被静默跳过（间歇性）。修复 = Kernel 每 session
原子发号器（IssueSessionSeq）唯一取号源，publisher 与 batch 都从它取号。

**drain 真相**：2.1.234 真样本证明 `--resume` 根本不重放历史到 stdout——
handleSendMessage 的同步 10s drainHistoryEvents 是纯自加延迟，已移除；drain 窗口
改首条 stream_event 事件驱动关闭（重放防御语义保留，12s watchdog 兜底）。

**stdout 单源门范围错误**（owner 第五轮复测：Mac Desktop 发消息 iOS 永远卡执行中）：
`agentRelayActive(sessionID)` 过粗——本进程 idle 存活期间，外部进程（Desktop/
Terminal worker）写同一 transcript 的回合不经本 stdout，却被「stdout 权威」压制
assistant 行 → iOS 收到问题卡执行中、无正文无终态。修复 = 门收窄为
`agentRelayActive && agentOwnsClaudeTurn(currentTurnID)`：core.ClientTurnOwner
（claude 自持 client uuid 集，settle 后保留）判定回合归属；外部回合照常走
file-relay 全量。**教训：单源模型的「源」必须按回合发起方判定，不能按会话存活
判定。**

**Mac Desktop 不实时显示 iOS 消息**（owner 第五轮问询 + 2026-09-05 追查）：Claude Desktop 不监听
transcript 的外部写入（无跨进程事件总线——正是我们自己产品需要 file polling 旁观
Desktop 的原因）。数据已持久写入会话文件，Desktop 侧重开会话即可见；不是
CordCode 可修的 bug。2026-09-05 取证补全：Desktop Code tab 自己也是「每会话一个私有
CLI 子进程」模型（活体：`Claude-3p/claude-code/2.1.260/claude --resume=<id>
--input/output-format stream-json`，版本比 CLAUDE.md 锚的 2.1.258 新）；无本地监听端口/
IPC、深链只有导航级路由（`claude://code/continue|new|needs-input`、`claude://resume`）、
`claude attach` 只服务 `--bg` 后台会话体系。GitHub 已知问题类别（#53717/#48955）。
Codex/OpenCode/DSH 能实时同步是因为有共享 server/bus，Claude 没有——此为架构差异。

**2026-09-05 重大发现：官方 Remote Control 已是多端同步会话架构**（
<https://code.claude.com/docs/en/remote-control>，本机 bundled CLI 2.1.260 已支持
`--remote-control`；owner settings 的 `remoteControlAtStartup` 未开启）：本地 CLI 出站
HTTPS 注册 Anthropic API，claude.ai/code 与官方手机 App 作为远程 surface 流式收发——
「终端/浏览器/手机可互换发消息、subagent/workflow 进度全端同步」，transcript 存
Anthropic 服务器做同步锚点；Desktop 是 RC surface 之一（resume 带 RC 的会话会 reattach
到同一 claude.ai 会话；Trusted Devices 列 Desktop 为可 view/steer 端）。对 CordCode 的
潜在路线 A（codex-remote 同构）：Desktop 托管会话 + 开 RC，bridge 作为 RC 远程客户端接入
→ iOS 回合在 Desktop 托管进程执行 → Desktop 原生直播（用户愿望直接满足），iPhone 侧
事件全来自官方流。障碍：RC「远程客户端 ↔ Anthropic 服务器」wire 协议无公开文档（官方
客户端仅 claude.ai/code 与官方 App），需真实 fixture probe（类比 codex-remote 当初）；
认证走 claude.ai 账号；transcript 上 Anthropic 云（产品语义变化，须 owner 裁决）。路线 B：
维持现状等官方开放。**未裁决，仅登记。**

**2026-09-05 probe 收口 + 裁决：no-go（无订阅）**（报告
`docs/2026-09-05-claudecode-rc-client-probe.md`，证据包
`scripts/claudecode-rc-probe/`）：owner 确认**无 Pro/Max/Team 订阅**，而
RC 硬要求订阅（API key 不支持）→ 路线 A（Desktop host + bridge 远程客
户端）与 F1（SDK bridge worker host）**全部搁置**，唯一恢复条件 = 未来
订阅。本机另有两条独立阻塞已实锤：user settings env 块的网关 base URL +
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`（shell env / project settings
覆盖均无效，doctor 实验取证）；Desktop Code tab 会话走 cc-switch 本地代理
（`127.0.0.1:15721/claude-desktop`，host 注入优先于 user settings）而非
官方直连。**正面遗产（未来订阅后直接复用，勿重新调研）**：Agent SDK
0.3.260 的 `/bridge`（`createCodeSession`→cse_* 会话、`POST
/v1/code/sessions/{id}/bridge` mint worker JWT=注册+bump epoch、
`attachBridgeSession` 全套控制回调/20s 心跳/4090 接管关闭码/outboundOnly
镜像模式）与 `/browser`（SSE 读+POST 写 `query()`）是官方 @alpha 接入面；
CLI 2.1.234/2.1.260 二进制内嵌 `/v1/code/sessions` 客户端实现与 API 表。
无订阅期间「Desktop 实时显示 iOS 消息」仍无官方路径（上方「无跨进程总
线」结论不变）。

## 2026-09-05 官方能力三项收尾：CLI 升 2.1.261 + get_context_usage 升 A + iOS 选择器直调

owner 指令三项（选择器直调 / CLI 升级评估 / get_context_usage 升 A）当日完成：

1. **CLI 升级评估→执行**：隔离安装 2.1.261 → 探针复测六项全绿（控制面六 subtype
   全 success、get_context_usage 三模式 success、Pre/PostModelSwitch 实证触发、
   既有 hooks 照常、changelog 234→261 无控制面/stream-json 输出变更、transcript
   type 集与形状锁 fixture 零新 type）→ 全局升级。**探针 env 坑**：mirror 刷新误抓
   Desktop 会话 env（无 ANTHROPIC 键）+ `CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p`
   下 CLI 走 Desktop 认证通道 → "Not logged in"、Stop 不触发（196ms synthetic 假
   turn）。重建 mirror = settings.json env 块（cc-switch 当前解析态）。**成功体嵌套**：
   initialize/list_models/get_context_usage 载荷在 `response.response` 双层，interrupt/
   set_model 裸键在第一层。
2. **PostModelSwitch 接入**（2.1.261 dump）：`to_model` 是网关改写后观测名（glm-5.3 →
   glm-5.3[1M]）、`requested_model` 是槽位名（default 重置时 null）、`source:"sdk"`。
   hooks 订阅集 +PostModelSwitch（Pre 有阻塞语义不订）；HandleClaudeHook → Agent
   .ObserveModelSwitch → catalog.observe（补 assistant 帧观测的盲区：/model 类会话内
   切换不经 assistant 帧时也保住 observed 映射）。
3. **get_context_usage 升 A**：自 spawn 会话每 turn 收口后异步取 detail=summary（不打
   per-category API），全量窗口占用（含 system prompt/tools/memory 分类）+ 官方
   maxTokens，成功覆盖流帧 usage 近似值；fail closed。fixture=
   `agent/claudecode/testdata/context-usage/get_context_usage-summary-2.1.261.json`
   （2.1.261 字段超出 SDK 0.3.260 类型声明——autoCompactThreshold/messageBreakdown/
   skills/slashCommands/apiUsage，dump-first 纪律的直接受益）。
4. **iOS 选择器直调 switch_model**（设计 §7.3.2 收尾）：ChatViewModel.selectModel →
   pushModelSelectionToBackend（门控：会话已建立 + backendKind==claudeCode +
   BackendModelSetting conformance；nil 选择 = default 重置；失败不回滚——Mac fail
   visibly + send_message.model 仍是权威重试路径）。**门控必须含 backendKind**：
   CCCodeBridgeBackendClient 是全部 bridge backend 共用类，仅 conformance 判定会把
   其他四个 backend 也带进直调（设计非目标 §9.3）。
5. **打开即拉活（owner 问询「为何须发消息才见详细上下文」→ 当日两轮修复）**：根因
   = 详细上下文唯一来源是运行中进程的 get_context_usage，进程此前延迟到 send_message
   才 spawn（旧理由「--resume 重放撑爆 events channel」已被探针推翻）。第一轮挂
   handleResumeSession 未生效——**owner 复测 + 日志证实 iOS 打开 claude 会话的真实
   链路是 get_session→fetch_todos→set_observation_scope，不经 resume_session**（教训：
   挂钩前先看真实调用链，入口假设要日志验证）。第二轮挂 set_observation_scope
   （full_stream 才拉活，milestones_only 旁路不拉；租约续期靠 registry 预检去重），
   spawn 后 1.5s 首取官方全量口径；emitProtocolContextUsage 移除 drain 门（实时快照
   非重放伪影，resume idle 会话的 drain 窗口内也如实可用）。owner 2026-09-05 13:26
   真机 ✅，生产日志两例 open-spawn ready（79f35e86 / 3451bf01）。


# 2026-09-04 Codex 计划：iPhone 能批、不能开

owner 真机：Mac Codex App 计划模式发任务 → iPhone 同步计划正文并点批准执行，通过。
iPhone 不能自己切入 Codex Plan（与 DSH 相同：入口在 Mac）。owner 裁决入口先不做。
官方 Plan 是 `collaborationMode`（`turn/start` / `thread/settings/update`），不是
Claude 的 `permission-mode`，也不是 dsh `/plan`。后续调研入口
`docs/2026-09-04-codex-ios-plan-mode-entry.md`；禁止把 Claude Plan 按钮复用到 Codex。

# Claude Code 冷启动既有 session 首轮流式从头重播：跨仓排查结论

## 2026-09-04 codex-web 已退役：三个防误判点

Owner 2026-09-04 裁决：codex-web 从产品 lineup 退役（Mac/iOS 不再出现、runtime
不挂载），`agent/codex-web` 源码保留，Codex 面由 codex-remote 承接。完整决策/
改动/回滚见 `docs/2026-09-04-codex-web-backend-retirement-完成情况.md`。排障时
别把以下预期行为当 bug 查：

1. **启动 WARN「codex-web agent not present; topology bridge dimension stays
   unresolved」**：topology monitor（go-bridge/main.go）的既有优雅降级，每次
   启动一条，正常。
2. **Mac/iOS 源码里大量 codex-web 引用仍在**：WorkspaceView 行内按钮/横幅、
   ManagementV1Codec activity 计数、daemon 重启逻辑、iOS codexWeb 枚举与
   MessageWeb 分支——都以 `agent.kind == "codex-web"` / `drivers.contains` 为键，
   不挂载即不渲染，是退役模式「代码保留」的一部分，不是漏删；iOS codexWeb 与
   codexRemote 共享流式机制，深删会伤 Codex Desktop。
3. **日志 catalog 行偶现 "codex-web" 字样**：是旧工作树目录名（如
   `cordcode-ios-codex-web-backend`）出现在 workspace filter dropped_basenames，
   与 backend 无关。

回滚 = RuntimeManager drivers 加回 `"codex-web"`（daemon seat 自动恢复）+ iOS
serverCreationCases/isDeprecated 还原 + 双侧测试断言翻转。

## 2026-09-03 Grok follower 问题断线恢复四层剥洋葱：同症状两层根因 + 揭盖式连环雷

owner 矩阵行 3「iPhone 杀 App 重开 → 挂起的问题卡应恢复且可答」连续三轮失败，
每轮修复都让下一层露出来。完整链：

1. **D-G3（leader 死亡场景）**：leader 进程死 → 订阅 ECONNREFUSED → 永久回退
   tailer。修复 = 订阅统一循环 + 10s dial 探测 reclaim（1fc6f1f）。
2. **D-G4（订阅活着场景）**：owner 场景 leader 活着、订阅未断，D-G3 不触发。
   真缺口 = iPhone 重开时全冷 hydrate（`checkpointHit=false`）从 chat_history
   重建——而 chat_history 转换器根本不产 question 帧。修复 =
   `GetRichSessionHistory` 以 `leaderSocketDialable` 为 `questionsLive` 门，
   未答 `ask_user_question` tool_call 产出 pending user_input part（fdb7d97）。
3. **gate 卡死**：D-G4 部署后「一直正在展开会话」。pending user_input turn
   **故意**无终态（阻塞边界，转换器不 seal turn_completed），而
   `WaitHydrateCommitReady` 要求非终态 turn 数为 0 且 grokbuild 无 §3.1
   冷源活跃信号 → gate 永不 ready → iOS 15s 超时重试死循环（ceffc9c）。
4. **panic 重启循环**：gate 放行后仍「一直正在展开会话」。hydrate 首次走到
   commit → `Restore` 安装基线——Restore 构造 projectionSession 漏建
   `userInputs` map（其余 4 个 map 都建了），leader subscriber 重发挂起 ask 的
   live 事件写 nil map → `panic: assignment to entry in nil map` → runtime
   崩溃 → supervisor 重启 → iOS 重连重拉 → 再 panic。生产日志铁证：
   `hydrate_commit headRev=254` snapshot 已发出，10ms 后 panic 栈（8c1ac9b）。

**教训：**

- **「修复生效后立刻出现新故障」往往是揭盖，不是回归。** gate 放行让 hydrate
  commit 这条从未走过的路径首次被执行，Restore 的 latent nil map 就在路径上。
  排障时先问「这次修复让哪条新路径首次可达」，再评估风险面。
- **同症状不同层**：两轮「正在展开会话」分别是 gate 死循环（行为层）和 panic
  重启循环（运行层）。SSV2 护栏第 9 条再次应验：先证运行层（panic 栈、进程
  代际、runtime_ready 次数）再谈行为层。第二轮 10 分钟定位全靠先 grep panic。
- **拿 HasSessionSubscriber 当 live 信号前先查 RPC 自身的订阅副作用**：
  `handleGetSessionProjection` 拉取即订阅（WP5 patch push），「无订阅 hydrate」
  在生产不可达——测试构造该场景会失败且失败方向误导。grokbuild 的窄门在
  agent 层（`questionsLive` 才产 pending part），宽门（拉取即订阅）+ 窄门组合
  才是正确语义。
- **hydrate 基线安装（Restore）必须重建派生索引**：快照装进 reducer 后，后续
  live 事件的跨源归属（interactionId → owning turn）依赖 `userInputs` 索引；
  只装快照不建索引，轻则 live 重发落错 turn，重则 nil map 崩进程。

## 2026-09-03 Grok rename 后消息页标题不更新：同一磁盘字段的两条解析链分裂

owner 验收 rename 功能：列表标题已改、消息页 header 停留旧名。排查教训：

- **纸面链路全对 ≠ 没问题，先找「同一数据的第二条消费链」**。iOS 静态链
  （rename 响应 → selectedSession → header）逐环无条件正确，问题在 Mac 侧
  get_session 回退链返回旧标题把新值覆盖回去。rename 后 50ms 的 get_session
  （日志 req_19→req_20）就是覆盖者。
- **官方 rename 只写 `generated_title` + `title_is_manual`，不回写
  `session_summary`**（上游 persistence.rs `display_title()` 的字段分工）。
  CordCode 列表链走 catalog `session/list`（官方解析，新名）、详情链
  （`Agent.ListSessions` → `parseSummaryFile`）直读磁盘 `session_summary`
  （旧 auto-title）——两条链对同一 summary.json 给出不同标题。修复 =
  `parseSummaryFile` 镜像 `display_title()` 优先级（非空 `generated_title`
  优先，**不看 `title_is_manual`**：自动生成标题同字段，官方测试明确锁定）。
- **复现路径与用户路径不同时，复现产物只可用于取证不可用于结论**。env-var
  automation open-session 复现出的「新会话」占位是 iOS 冷启动竞态
  （get_session 在连接就绪前发出且 `try?` 吞掉、无重试），与 owner 症状无关；
  真正的锚点是生产日志里 owner 时间线的 RPC 序列。
- **生产路径验证可用临时配对 wire 探针**：pairing create（Management API）→
  `/pairing` claim → approve → `pairing_complete` 拿 device token → 带查询参数
  `?token=&deviceId=` 连主 WS → hello → get_session，最后 revoke 探针设备。
  免 UI、免真机，读生产 runtime 同一 handler（脚本
  /tmp/cccode-rename-investigate/probe_getsession.py，shape 见
  pairing_handler.go / auth_gate.go）。

## 2026-09-03 同名 iOS 设备配对互踢：UIDevice.name 泛称 × 名字清理启发式无格式校验（已修复）

2026-09-02 13:35 事故（iPhone 11 批准后 iPhone 16 被踢回扫码页）的根因与修复教训：

- **iOS 16 起 `UIDevice.current.name` == `model`，永远返回泛称 "iPhone"**，与设置里
  的本名无关。所有现代 iOS 原生客户端上报的 (platform, displayName) 必然相同
  （`("ios","iPhone")`），撞名是系统性必然，改名规避不了。
- **deviceID 格式即身份代际**：iOS 稳定 ID = `"dev_" + UUID hex`（下划线，Keychain
  持久）；`pairing_handler` 兜底 = `dev-%x`（短横线，每配对随机）；web/旧记录 = 裸
  32-hex。`ReplaceDevice` 的名字清理启发式本意是清升级前随机 ID 残留，却没校验旧
  记录是否真是 legacy 格式 → 稳定 ID 记录按名字互相顶掉，同 bridge 原生 iOS 客户端
  最多共存一台。修复 = 名字清理只对无 `dev_` 前缀记录生效 + identityPublicKey 等价
  替换加固（双方非空才比较，空 key 不算同身份）。
- **写探针先核对 ID 格式语义**：首轮 wire 探针用了 `dev-probe-xxx`（短横线），按
  修复语义恰属 legacy、被名字清理是正确行为——探针失败反而实证了 legacy 分支仍
  生效。生产探针设备 ID 必须用 `dev_` 下划线格式才模拟现代 iOS 客户端。
- 方案与实施记录：`docs/2026-09-02-same-name-ios-device-eviction-fix-design.md`。

## 2026-09-03 iOS automation open-session 冷启动竞态：桩 client 本地抛错 = 无 wire RPC（已修复）

§23.8 排查中顺带暴露、随后修复的 E2E 自动化路径坑（iOS 主树工作区，随 iOS 侧提交）：

- **`BridgeUnavailableBackendClient` 桩是「本地抛错」而非「慢」**。冷启动 hello_ack
  未到达时 `resolveBackendClient` 返回桩，桩的全部方法（含 getSession）立即 throw
  unavailableError()——不产生 wire RPC、被 `try?` 静默吞掉、无重试。症状：占位
  Session（「新会话」）永久停留，Mac 日志**完全没有** get_session（不是失败，是
  没发出）。排障时「Mac 侧无日志」先查 iOS 是否拿的是桩。
- **修复 = 有界等待真实 client**（ContentView.swift `openSession` Task 内 10s/200ms
  轮询 `client is BridgeUnavailableBackendClient`）+ 等待窗口内用户切走
  （`selectedSession?.id != sessionID`）则不回写。同函数还承接通知点开
  （notification-pending/live）路径，一并受益；用户手动打开本就不受影响。
- **devicectl env 注入要点**：host 侧 `DEVICECTL_CHILD_<KEY>=value` 经
  `devicectl device process launch --terminate-existing` 转发给 app（app 读
  `OPENCODE_OPEN_SESSION_ID` 等，AppLaunchAutomation.swift）；不加
  `--terminate-existing` 只前台化已运行进程，env 不被消费。验证配方：注入重启 →
  grep go-bridge.log 出现新 get_session → 截图 header 显示真实标题。

## 2026-09-02 Grok model/effort 实测取证：checkout ≠ 目标二进制，snake/camel 已分叉

grok 上游 source-first 核验时最关键的发现：**本机 checkout
（`/Users/jacklee/Projects/grok-build` @ bc7f02ed）不是目标二进制的版本**。本机安装
grok 1.0.13（自报 commit 5e9a58528b76，`git log` 证实不在 checkout 历史中），两者
wire 已分叉：

- `session/set_model`（snake_case）在 1.0.13 上是唯一可用方法名；camelCase
  `session/setModel` 返回 -32601 Method not found（checkout 的测试 fixture
  `test_leader_stdio_integration.rs:1292` 恰是 camelCase——新版行为，照抄会直接发错）。
- extension meta 在 1.0.13 走 **`_meta`**（下划线前缀）；checkout 源码是无前缀
  `meta`。
- `session/set_model` 的 `modelId` **服务端必填**：effort-only 切换缺省会报
  "missing field `modelId`"——必须从 session/new|load 响应顶层 `models` 真值取
  当前模型重发。
- `session/new` params `_meta.{modelId,reasoningEffort}` 均被消费；`session/load`
  不接受模型参数，显式选择须 load 后经 set_model 补投。
- 持久化铁证：`~/.grok/sessions/**/summary.json` 的 `current_model_id` /
  `reasoning_effort` 直接验证切换落盘。

取证方法（零 API 消耗）：`grok agent stdio` 起 fake 客户端只发
initialize/authenticate/session-new/set_model/close，不 prompt。比读源码更强，
符合「目标二进制与 checkout 不同版本时以目标版本真实样本为准」纪律。Cargo
registry 里没有 agent-client-protocol crate 源码，这条路反而逼出了更直接的实证。

教训：上游 checkout 只能当**语义参考**（ModelState 结构、meta 键常量、effort
门控语义与 1.0.13 完全一致）；wire 细节（方法名大小写、meta 前缀）必须用目标
版本探针实证。若当时照 checkout 实现 camelCase，测试全绿但真机发一个
-32601，又要多一轮 owner 复测。

实现锚点（commit c1dfa81）：`agent/grokbuild/acp_types.go`（wire 类型+注释存证）、
`grokbuild.go`（adoptModelCatalog / EffortsForModel / AvailableModels 目录优先）、
`session.go`（newSession `_meta` / load 后软失败 set_model / Send 前漂移硬失败）、
`session_model_switch_test.go`（11 测试，fixture=真实样本形状）。

### 二轮真机再翻车：官方双模型 id 形态 × iOS 回显值当选择发回（a0b0f11 修复）

effort 门修完后 owner 主动选 glm+高仍 -32602。探针排除法收网：官方 set_model
**只校验 modelId/sessionId**（effort 传 bogus/xhigh 都成功），-32602 必是
modelId 无效。真相：**官方目录/set_model 请求用条目 id（"grok-4.5"），持久化
与应答用底层 id（"glm-5.3"）**——owner config `[model."grok-4.5"] model="glm-5.3"`
的两种身份。summary.json 落 `current_model_id=glm-5.3`，iOS transcript 真值读出
显示「glm 5.3」，选择后把这个**显示值当 model id 回发** → 目录外 → unknown
model id。旁证闭环：无 effort 修剪 log（iOS 这次没发 effort 字段）；第一次不选
模型发送成功（不发 model 就无漂移）。

修复分两层：Mac 端 `unknown model id` 软化（WARN + 保持会话当前模型继续
turn——此时会话本就在用户所选模型上，杀 turn 只会阻断发消息；其余错误硬失败）
+ callRPC 错误带官方 data + set_model 参数日志；**iOS 侧根治另案**（发送
list_models 条目 id 而非 transcript 显示值）。

教训：**官方「同一实体多种 id 形态」是 wire 契约的一部分**（请求 id ≠ 持久化
id ≠ 显示 id），翻译时三种形态都要取证——只抓请求面样本会漏掉回显面。另：
探针「错误指纹矩阵」（故意发坏参数看 data）比成功路径探针信息量更大，这次
直接把「-32602 必是 modelId 无效」钉死。

### 首轮真机即翻车：iOS 状态残留 × 缺客户端侧 effort 门（da17648 修复）


owner 矩阵行 3 复现：iOS 先选 grok-4.6+high（行 2 测试），切 GLM 后 effort 残留
high，MacBridge 把 `set_model{glm, effort:high}` 原样上抛 → 官方 -32602 → Send
硬失败、turn 被杀。第一轮实现只做了「发送选择」，没做「验证选择」。

上游 `model_state.rs` 的 `resolve_effort_for_model` 本来就是官方客户端的本地门：
目录外模型或无 supports 标志 → Unsupported；token 不在该模型菜单 → UnknownToken；
注释明言 "so the TUI fails instead of sending a blocked effort to the API"——官方
TUI 从不把无效组合发给 API。教训：**翻译官方 wire 契约时，客户端侧的校验门也是
契约的一部分**；只镜像了 server 接受面、没镜像 client 拒绝面，等于把官方客户端
不会产生的请求发给了 server。修复 `effectiveEffortForModel`（目录证明无效 →
丢弃 effort + log；model 无效仍照发让官方诚实报错；无目录真值透传）。

iOS 侧的根因（切模型不清 effort 状态）不修也能工作——Mac 端修剪后 GLM turn 正常
发出；将来 iOS 主动清状态属于 UX 打磨另案。

## 2026-09-02 Grok Leader 验收期两复盘：订阅键三分语义 + user echo 身份补齐覆盖缺口

### D-G2「无人观看 60s 自动下线」两轮修复：订阅键语义混用

owner 真机验收行 12 发现 D-G2 永不触发。两轮根因（`98ae793` + `05152aa`）都出在
订阅键语义，不是计时器：

1. `handleSetObservationScope` 只往订阅集合加键、从不退订——iPhone 切走会话后旧观察
   键残留，App 连接期间 `HasSessionSubscriber` 恒真，守望永不超时。
2. 修完仍不触发：iOS `get_session` 携带 `currentSessionDirectory`，读路径订阅记的是
   **带目录键**；而守望判据查的是**空目录观察键**。带目录键不等于观察键，幸存了第一轮
   的 reconcile。

由此固化订阅键三分语义（后续改 `handlers_relay.go` 订阅逻辑必须遵守）：

- **空目录键 = 观察键**：纯旁观，随 scope 切换退订，是 D-G2 守望的判据对象；
- **带目录键 = 自有会话键**：send_message / resume 产生，切走会话后**继续**收流，
  不参与守望取消；
- `Targets` 的 noDir 匹配保证空目录键是带目录事件的合法投递目标（官方 leader 按
  session+cwd 广播，订阅键去目录后能命中）。

教训：**「订阅键」不是单一概念**。设计守望/退订/取消类逻辑前，先枚举该键的全部生产
者与消费者，确认它们指的是同一语义；否则单测各自都绿、真机永不触发（两轮修复期间
13 个 D-G2 单测全程绿，靠的就是它们只测空目录键路径）。

### row 6「iPhone 自己发的消息消失」：身份重建只覆盖了一条消费路径

owner 验收行 6 发现：iPhone 自有 turn 流式正常但 prompt 不显示。根因链（预存缺口，
2026-08-05 codec 重写时引入，非本分支）：

- 上游 grok-build `meta.rs user_message_chunk_meta` 按设计只 stamp
  `promptIndex`/`hideFromScrollback`，**无 promptId**（source-first @ bc7f02ed 已核）；
- `convertSessionUpdate` 产出无 TurnID 的 `EventUserMessage`（正确——身份由消费方补）；
- 但补齐逻辑（98f0e57）只加在观察路径 `grokLeaderSessionRelayLoop` 的
  pendingUserText，自有 turn 的 `relayEvents` 路径从未有过；
- wire `user_message` 无 turnId/itemId → SSV2 reducer（projection_reducer.go:668）
  跳过 → iOS localSend 乐观占位在权威投影推进后被替换 → 发送的消息消失。

修复（`39e29a8`）：身份补齐下沉到 `grokSession.emitTurnScoped`（session 层，
handleNotification 统一入口），缓冲 echo、同 turn 首个带身份事件（含 turn_completed
终态的 prompt_id）到达时以其身份补发；`Send` 开新 turn 时清残留。

教训：**同一 codec 输出有多条消费路径时，任何「补身份/补状态」横切逻辑必须在汇聚层
做，或在每条路径分别做**。98f0e57 在 loop 层做补齐只覆盖了它看到的那条路径——写这类
补齐时先列全 codec 事件的全部消费者（grep 产出的 Event 类型流向），再选层；否则验收
期必然再撞一次。测试也要按消费路径分（观察路径 pendingUserText / 自有路径
emitTurnScoped 各有钉子）。

### 附带实测事实（后续排障直接引用）

- TUI 存活时 `kill -9` leader → **~12 秒内 TUI 自动重生 leader**（connect_or_spawn，
  socket 重绑）；要做稳定 stale socket 实验，必须先完全退出 TUI（leader 带
  `--no-exit-on-disconnect` 存活）再杀。
- D-G1 回退三要素（订阅结束 + 未转发事件 + ctx 未取消）实测成立；已转发 ≥1 事件后
  断开不回退（F-7 收口语义）。
- D-G2 取消延迟实测精确 60s（elapsed=1m0s），无虚假中断。

## 2026-08-28 Codex Remote Phase 0 冻结（2026-09-02 已解除）

2026-08-28 曾按原 Gate P0 停工：官方不把 reconnect cursor 交给 controller，Desktop live
未证明 `turn/started`/delta/`turn/completed`。当日 Owner 改写 Gate（接受无 cursor 的首连
live 流，进入 Phase 1），产品路径随后全线落地：根方案 111/111、懒加载 60/60、iOS 33/33
proved-complete，owner 真机矩阵 A+B 2026-09-02 全过，经 owner 授权合入 `main`。冻结期
取证细节见 `docs/2026-08-28-codex-remote-phase0-fail-blocked.md`（历史记录）。

否则不得注册 backend、不得动 `agent/codex-web`/`agent/codex`、不得进 iOS Phase 3。

## 2026-08-28 Claude 后台任务完成通知泄漏为 iOS 用户气泡

现象：Mac 端 Claude Code 在执行后台 Bash task 时，iOS 中途打开同一 session 后出现完整
`<task-notification>` XML，包含 task id、tool-use id 和 `/private/tmp/.../tasks/*.output`。
它不是 CordCode Web Push 正文，也不是 reasoning：截图中 XML 是 synthetic user bubble，下面的
“思考中”才是独立 runtime status。

真实 transcript 形状是两行控制输入：先有无 uuid/message 的顶层 `queue-operation enqueue`，随后
Claude Code 写入 `type=user`、`origin.kind=task-notification` 的消息并用它触发后续 assistant。
旧实现只把顶层 queue row 判为 inert；第二行满足普通 user/message 结构，冷 hydrate、live file
relay 和 rich history 都会把 XML 投影成 `user_message`。

修复纪律：

1. 只认结构化 `origin.kind=task-notification`，禁止用 `<task-notification>` 字符串匹配；用户真的输入
   同名 XML 时仍必须原样显示。
2. live scanner 仍消费该物理行并推进 source byte cursor，但 `Admitted=false`，不进入 projection
   transaction；后续 assistant 沿 file-order fallback 继续归属上一条可见用户 turn。
3. 冷 hydrate、mapper 防御门、rich history 与 session metadata 使用同一 origin 语义；不能只在 iOS
   renderer 隐藏，否则 legacy/history/其他客户端继续泄漏，且 cursor/turn ownership 无法统一。
4. CordCode Web Push 的 `PushIntent`/ledger 是独立终态通知管线，不会写 Claude transcript；两者仅名称
   都含 notification，排障时必须先查 transcript 的 `origin`，不要把通知 UI 与 agent 控制输入混为一谈。

## 2026-08-27 web push 完成通知正文预览：四个叠加缺陷与多生产者抢键教训

owner 要求完成通知对齐 Antigravity（标题=会话名、正文=真实回复预览）。通知能到但正文
恒为固定文案，连续四轮生产取证（evidence Chrome 真实 turn + `getNotifications()` +
WP-RESP 账本时间戳）才挖全根因。**单轮日志完全正常、tag/锚点每轮都正确**——这类
「通知到了但内容不对」的 bug 必须用 ledger 的 `firstAttemptMillis` 对时序，不能只看
dispatch 是否发生。

四层叠加（commit 6c9d053 → 26ec9c4 → 6603936 → b7d300e，逐层修完才通）：

1. **flush 晚绑定误判**：`cachedPID==0` 在 watcher 先于 `claude -p` 启动的窗口恒真
   （web 发送流必然如此），thinking 行终态被提前放行、预览恒空。进程死亡要用显式
   `boundProcessDied` 标志，不能用「PID 为空」当死亡信号。
2. **多生产者抢 notificationKey**：claude turn 完成有两个事实生产者——agent relay
   （relayEvents，位点 1）和 file relay watcher（位点 3）。位点 1 在流式中段就声明空
   预览 intent，ledger 按键去重把数秒后带预览的 flush 丢掉。**谁负责挂起等预览，谁独占
   终态 intent**：位点 1 对 claude completion 在 `relayKindIs(claude_file)` 时让位。
3. **非 hydrate 窗口整体丢通知**：batch 路径（`applyClaudeLiveSourceRecord` 非 hydrating
   分支）终态从不挂起、无人声明 intent；且 batch 事务结算后 `ActiveTurnID` 返回空
   （`Execution.ActiveTurnID` 清空 + 无 running turn），flush 时 fail-closed。intent 的
   turnId 必须**优先取终态事件自身**（mapper turnId），kernel active turn 只做回退。
4. **标题缓存键漂移**：catalog 写入用 agent 名 `claudecode`（`agentBackendID`），通知
   读取用事件流 `claude`——同一后端两个名字必须归一（`webPushTitleCanonicalBackend`）。

结构性事实（后续排障直接引用，勿重新调查）：

- **kernel committed reducer 不物化 claude 流式正文**：`textAppends` 以 append_text
  PartOps 发完即清，`turn.Assistant` 到下次 hydrate/checkpoint restore 才重建。所以完成
  通知的正文预览只能来自**同源 mapper 事件流的累积器**（`claudeTurnTextAccumulator`，
  512B cap、user_message 重置），不能读 kernel。batch 路径在隔离测试里能物化正文，
  生产混合路由下到不了——别再沿这个方向挖。
- 同一 claude turn 的 transcript 里 thinking 行与 text 行都带 `stop_reason=end_turn`，
  mapper 各发一次终态；flush 必须按 turn 去重，不能依赖「首个发布结算后第二个
  ActiveTurnID 为空」的偶然去重。
- claude web turn 的完成事件**两条 relay 都会看到**（stdout agent relay + transcript
  file relay 同时活跃，`relayKindClaudeFile` 提升后 agent relay 继续跑）——「某路径不经
  relayEvents」的旧结论按当时代码成立，现在两者并存。

验证方法：evidence Chrome（`/tmp/cccode-evidence-chrome`，CDP 9233）发真实 turn，读
`navigator.serviceWorker.getRegistration().getNotifications()`；tag = `cc_` +
sha256(`backend|session|turn|completed`)[:16] 可对 transcript turn uuid 精确核对锚点；
`web-push-samples/WP-RESP.jsonl` + `web-push-delivery-ledger.json` 的
`firstAttemptMillis` 判定哪个 intent 赢了抢键。终态验收：title=`CordCode · Greeting`、
body=真实回复、tag 精确匹配（owner 真机 ✅）。

## 2026-08-24 codex-web 共享 daemon 拓扑：共享 server 与私有 stdio 之别

### 正确的拓扑（2026-08-24 之前文档写错过）

Codex Desktop 和 codex-web adapter 是**同一个官方 app-server daemon 的两个客户端**，不是
两个 runtime。daemon 由 `codex app-server daemon` 持有一个 control socket（UDS），Desktop
和 adapter 各自 dial 它；codex-web **不**另起 stdio 独占 runtime。旧分析文档把 codex-web
写成 `work_dir=~/` 的 stdio 子进程是错误拓扑——那正是「另起一个 adapter-owned runtime」
的老路，本轮明确放弃。

这条拓扑决定了两件事：codex-web 是观察/被动型（thread/observe 订阅，不持有写会话），
iOS 侧的 turn 由 Mac 发起点（Desktop）或 iOS 发起（adapter 的观察连接）都落在同一
daemon 的同一 thread 上。也决定了下面所有「断在分派」而不是「断在投递」的 bug 形态。

### 让 codex App 「看见」共享 daemon 上的会话：目录必须同源

第一轮真机：Mac 新建 session，iOS 列表不刷新（目录指纹恒 438，而 thread/list 已经是
429/430——新 session 进了旧数据源）。根因不是投递，是**分派**：`discoveryFingerprint` /
`handleListSessions` / 3s hint 三处都用 `agent.Name() == "codex"` 字符串分派，codex-web
的 `Name() == "codex-web"` 不满足 `codexThreadLister`，只能走旧 `agent.ListSessions()`
（ListAllThreads）——与 thread/list 富管线不同源，指纹永远不含新 session。

教训：**能力用接口断言，不要用 `Name() ==` 字符串**。修复为 capability 断言
（`codexThreadLister` / `codexThreadHeadLister`），codex-web 实现
`FetchThreadList`/`FetchThreadListHead` 进入同一 catalog seam，三处分派同源。

### 同源之后立刻踩到风暴：语义指纹在流式 turn 中每 3s 触发全量刷新

3s hint 探测用与权威全量相同的语义指纹（含 `updatedAtMillis`）：流式 turn 每个 delta 都
改写 updatedAt → 每次探测「head changed」→ 全量刷新 → 指纹又变 → `sessions_changed`
风暴（generation 每 3s +1，当天到 108）。`codexDiscoveryHintFingerprint` 改成
`listOrderFingerprint`（顺序+id）：新增/删除/recency 变化仍触发，updatedAt churn 不再。
日志同时打 raw/filter 两个计数，438 vs 429 这类差异才能审计。

### 观察/被动会话的停止：注册表里没有它

codex-web 会话不进 go-bridge 会话 registry（被动泵只建 `session == nil` 的 stub）。
iOS 点「停止」时旧路径 `handleAbortGeneration` 只区分「未命中/空」——裸 stub 被当命中，
删掉 stub 后 no-op，daemon 上的 Mac turn 继续跑（真机两次 abort 后 text_delta 接着流式）。
修复：`abortObservedThread` 按 threadID 直达官方 `turn/interrupt`（`ThreadTurnCanceler`，
turnID 取观测 `liveCodec.ActiveTurn`，miss 时 thread/read 冷基线兜底，fail closed）。
**被观测的 turn 的停止，身份来源只能是观测流**——本地 turn/start 回执不存在的场景下，
「订阅前已运行」的 turn 靠冷基线找 inProgress。

## 2026-08-24 iOS 首次发送后 3–5 秒无反应：先 paint，再做控制面 RPC

### 现象为什么只像“首条慢”

真机同时出现两种同源现象：打开既有 session 后发第一条消息，以及新建 session 后发第一条
消息，点击发送后界面会像没响应一样停 3–5 秒；同一 session 的第二条通常立即显示。原因不是
模型首 token 慢，也不是 WebSocket 没发出去，而是第二条命中了 session directory / projection
缓存，第一条仍在等待 `createSession`、`getSession`、目录解析或第一张 projection echo。

旧实现把这些**控制面前置条件**误当成了**展示用户刚刚提交内容的前置条件**。SSV2 路径还以
“ProjectionStore 是唯一 writer”为由完全不写本地 user row / assistant placeholder，于是发送
动作要等 Mac 权威投影绕一圈回来后才有任何可见反馈。

### 正确分层：乐观展示不是伪造权威状态

发送入口应同步完成 presentation paint，然后才异步等待真实 RPC：

1. 立即把 user message 和空的 streaming assistant placeholder 写入 `messages[]`，清输入框、
   滚到底部，并建立 `.localSend` generation ownership。
2. 新 session 随后才执行真实 `createSession`；既有冷 session 随后才执行真实 `getSession` /
   directory resolution；最后执行真实 send。不得用假 session、假回执或 fallback 冒充成功。
3. `createSession` 回来后把本地 generation 绑定到真实 session id。若回执没带 directory，保留
   本次请求时捕获的真实 directory，避免为了同一事实再阻塞一次 catalog/getSession 往返。
4. RPC 真失败时，把既有 placeholder 转成真实错误并走 generation finalize；不能删除乐观行制造
   “从未发送”，也不能把请求已发等同于服务端已完成。

这里的本地两行只是短生命周期的 presentation overlay；turn 的完成、失败、abort 仍只认 Mac
Projection Kernel。SSV2 的“单一权威 writer”约束禁止客户端编造 projection 终态，不禁止客户端
即时呈现自己刚输入的文本。

### 必须有 revision fence，否则乐观行会被旧 projection 擦掉

只在 `sendMessage` 前面 append 两行仍不够。发送后马上可能触发 loading、旧 Ready snapshot 或
changeset render；它们的 `syncRev` 尚未越过发送前 head，会把 `messages[]` 重画为空/旧历史，形成
“出现一下 → 消失 → 几秒后整段弹出”。

修复采用 local-send paint fence：append 完成后记录发送前 `appliedRev` 和乐观消息快照。只要当前
generation 仍是同 session 的未完成 `.localSend`，且 projection 尚无 ready head 或
`projection.syncRev <= baselineRev`，render 就恢复/保留该快照并维持 generating；当权威 head
首次满足 `syncRev > baselineRev` 时释放 fence，之后完全由 projection 渲染。切换 session、结束
generation、失效或错误路径必须清 fence，避免 overlay 泄漏到别的会话。

不要用时间窗、首 token、消息数量或文本相等猜 fence 何时释放；唯一稳定边界是 projection revision
越过发送前基线。

### 测试要卡住真实慢点，而不是只测最终结果

最有辨识力的两个 seam 测试分别阻塞 `createSession` 和 `getSession`，在解除阻塞**之前**断言：

- `messages` 已有 user + streaming assistant placeholder；
- user 内容就是刚提交的 draft；
- 既有 session 已进入 generating；
- 后端 send 尚未被允许完成。

这样才能证明 paint-before-RPC，而不是仅证明 RPC 很快时最终能显示。SSV2 还需覆盖 fence 矩阵：
loading 保留、相同/落后 rev 保留、head 越界释放、changeset 路径同样受 fence、session switch 清理。

另一个测试坑：`sendMessage` 为了先 paint，会把 wire submission 放入 `streamTask`。断言 UI paint
可以在 `sendMessage` 返回后立即做；断言 `sentContents` 必须再 `await streamTask?.value`，否则测到的
只是 unstructured task 调度竞态。旧测试名/断言若仍坚持“SSV2 不得 optimistic timeline writer”，
应更新为“允许 presentation overlay，但权威 settlement 仍来自 projection”。

### 验收与落地

iOS commit `15bb645` 在 Codex 分支落地；SessionSyncV2Tests 59/59、Codex 首发 seam 5/5，signed
真机包安装后 owner 验证：打开既有 session 的首条、新建 session 的首条都可立即显示。这个问题的
验收点是“点击发送后的同一帧级可见反馈”，不能以最终消息成功到达代替。

## 2026-08-23 ccswitch 变更 provider：运行中 daemon 不重读 config.toml

ccswitch 改完 `config.toml` 后运行中的共享 daemon 不重读配置（进程内副本）。正确杠杆是
官方 `codex app-server daemon restart`（同一 daemon 读新配置），不是杀掉等 Desktop 掉回
stdio（见上方 2026-08-22 条目——Desktop 探测失败就锁死私有 stdio，且 restart 必须快于
其 2500ms/1s 探测窗口）。

本轮补上人机杠杆：Codex Web 行「重启共享 Codex 服务」按钮（执行 daemon restart，控制
socket 恢复后提示；有任务执行时禁用；Desktop 未自动恢复则提示完整退出重开）；检测到
`config.toml` 变更时该行提示「重启后生效」，避免用户以为已生效。

iOS 侧的连带坑：daemon restart 换控制 socket → bridge transport 退避重连 → iOS 预建
bridge client 的激活健康检查一次性采样（旧实现 2 次 × 500ms）落在重连窗口内 → server
不激活 + 错误横幅固定显示直到 App 重启。修复：`waitForBridgeClientHealth` 按
pollInterval 轮询最多 maxAttempts 次，窗口覆盖一次完整退避（1/2/4/8s…），耗尽仍不健康
才报错。教训：**任何「重启全局服务」的改动都必须按完整退避窗口规划客户端激活态竞态**，
多试一次不够。

## 2026-08-23 codex-web todo dock：官方没有持久化 todo 查询接口

### 事实与数据源归属

官方 `turn/plan/updated` 是 todo **唯一**结构化真相（`{step,status}`）；`thread/read` 的
plan item 只有 text（无结构）。没有「fetch todos」官方 API——所以**不能靠 JSONL/history
反推**，也不能把 raw `todos_updated` 当稳定真值（当天 6 组事件 ×2 帧重复，全天
`[TODO] sse-case` 0 次——raw 路由未定点，不归因不修）。8s 轮询本来就是兜底设计。

### 修了什么

- Mac：codex-web 实现 `core.TodoProvider`——`planCache` 镜像官方 EventPlan（中央泵与
  订阅解码两条路径都写），`FetchTodos` 返回副本，无缓存返回空而非 `not_supported`；
  会话删除清缓存；1024 条上限超限重置（plan 是易失状态，宁重拉不泄漏）。
- iOS（详见 iOS 仓 think.md）：轮询门控从「缓存含 active 项」改为「后端支持 todos 且
  会话打开」——冷打开时 plan 可能晚于首次 fetch，空列表 **不是** 全完成；停止条件 =
  非生成态 + todos 全完成；`discardStaleCompletedTodoPlanForNewGeneration` 清空旧完成
  计划后立即重启 8s 轮询（原实现停表，新任务全程无数据源）。

### 教训

「用户看到 dock 不动」的三层取证顺序：Mac 有没有 EventPlan → bridge 投递有没有 →
iOS 轮询/分支是否活着。**不要把架设在脆弱 raw 帧上的修复当验收**——raw `todos_updated`
当天 0 次进 VM，dock 能修好靠的是轮询兜底 + 事件缓存。

## 2026-08-24 MacBridge「启动失败」：runtime.json 删除 + alreadyRunning 楔子

截图「启动失败 / Bridge runtime 已在运行 (PID 95026)」，点重启也失败。

根因链条：`RuntimeManager` 每次 launch **前删除 runtime.json** → launch 抛
`alreadyRunning`（controller 持有健康进程——这**不是**启动失败）→ 被删除的 runtime.json
无人补写 → `pollManagementAPI` 每轮 early-return → UI 永久卡死。两端修复：Go 侧 15s
周期原子重写（`RewriteRuntimeJSON`，删除窗口自愈）；App 侧采纳已运行 PID 转「等待管理
接口就绪」而不是展示失败。

教训：**`alreadyRunning` ≠ 失败**；「启动前清理」类状态文件操作必须先想清楚「谁是写回
方」——本场景有两个进程生命周期（controller 持有 vs launch 发起），第二个 launch 的
清理动作会摧毁第一个进程的写回契约。

## 2026-08-24 SSV2 护栏违规收口：谁写 messages[] 是一半，谁写 Kernel 是全部

发本轮审计（12 条护栏 + 专项声明）确认的违规清单并全部收口：

- raw permission/question 落回 timeline（曾「投影已吃 permission_*，raw 作兜底」——）
  兜底=双写）；Mac delivery seal + iOS 只留通知/控制动作。
- 乐观发送（`localSendProjectionBaselineRev`/`localSendOptimisticPaint` hold/restore）
  用 revision 猜「何时保留本地消息」= consumer referee，删除。
- abort 先 `settleLocalAbortState`（RPC 成功后本地置终态）= 冒充权威完成态，改为只发请求
  等投影。
- 发送 RPC 直返的 assistant Message 写 timeline——SSV2 下硬拒绝。

**P2 已收口（2026-08-24，选择 (b) 删除合成，9cf9287）**：`abortObservedThread` 此前在
官方 `turn/interrupt` ACK 成功后合成发布 `turn_completed{reason:aborted}` 与
`session_state_changed: idle`，经 Kernel `IngestLive` 提前写终态——control-plane 请求
不能产生 timeline 终态，否则仍是第二 writer（护栏 8 的例外只适用于非 timeline 控制面，
护栏 7 的完成态只认权威投影）。收口后：ACK 只证明取消请求被接受，不再合成任何终态；
Projection Kernel 保持原 rev、active turn、running phase，直到共享 daemon 的官方
`turn/completed` 经观察流到达。新测试 `TestAbortObservedThreadDirectCancel` 在
turn_started→running 预置后断言 ACK 前后 `SyncRev/Phase/ActiveTurnID` 逐一不变。
注意边界：registry 路径（bridge 拥有生命周期的进程型 agent session，CancelTurn+Close
后官方事件流随进程终止）仍保留合成收口——与本例「共享 daemon 上官方帧仍会来」的观察
路径是两种 context，勿混用。

**P2 边界更正（2026-08-24 真机，e87be32）**：registry 路径不是天然「进程型」——共享
daemon 的 codex-web 会话在 registry 里照样有 AgentSession（iOS 发起的 turn），但 Close
并不会终止 daemon 上的 turn。12:02:38 真机：注册表路径 CancelTurn 失败（错误仅 DEBUG）
后仍合成 `turn_completed{aborted}`+`idle`（flush syncRev=816），官方 turn 继续 → 官方
工具帧把投影挂回 running → iOS 屏幕上「停止 1 秒后恢复执行中」（第二次停止走观察直达
才真停）。因此判据是后端是否共享 daemon（`sharedDaemonCodexBackend`：codex-web /
app_server 模式 codex），不是 registry 有无；合成终态只留给真正 Close 即终止的私有
进程后端。CancelTurn 失败同时升级为 Info 可见。

审计教训：**verdict 不能只数 iOS 侧 `messages[]` 写点**；Mac 侧合成事件进 Kernel 才是
同一违规的高级形态（护栏 7 的「完成态只认权威投影」）。

**P2 第三次边界（2026-08-24 真机 12:55-12:58，本窗口）**：「不合成」够不够？不够——
合成只是违规的一种形态。12:52-12:58 真机序列：iOS 发起 turn A（12:52:48 send_message，
注册表路径 AgentSession + agent relay）→ turn A 自然完成（12:53:51，relay 转发
seq=601）→ Mac Desktop 在同一 thread 发起 turn B（12:55:31.743）→ iOS 点停止：
第一次（12:55:49 req_11）用过期 turnID（agentSession.activeTurnID 停在 turn A——
`observeEvent` 无调用者，只有本端 TurnStart 返回值被记录）→ 官方 `-32600 expected
active turn id A but found B`；**该请求仍执行 deleteSession+sess.Close（removeListener）**
→ relay 从此读不到任何官方帧，但事件通道永不关闭，relay goroutine 成僵尸；
agentRelayRunning 残留 true → 被动泵「单一摄入所有者」门永久挡掉官方帧 → 第二次
abort（12:55:55 req_13，走观察直达）成功、官方 turn/completed（interrupted）只有
被动泵收到并被门丢弃 → Kernel 停在 syncRev=443（tool_started）、Execution 永久
running → iOS「执行中」；被动泵 markIdle（门内之前无条件执行）给已删除会话建 idle
stub → Mac「待执行」。**修法三件套**：①agentSession 事件改为监听→转发链，转发前
observeEvent 维护 activeTurnID（外部 turn 覆盖、完成清空），Close 关闭对外 events
通道——relayEvents `!ok` 据此退出并清 agentRelayRunning，官方后续帧由被动泵接管
（僵尸不再挡门）；currentTurnForControl 改为中央泵观测优先、本端返回值兜底。②共享
daemon 注册表 abort 不删会话不 Close（relay 保留，官方收口经它进 Kernel）。③relayEvents
`!ok` 对共享 daemon 不合成 events_channel_closed（同一「不得抢先写终态」规则；私有后端
保留）。注册表路径 12:55:49 的「失败仍删除」已被②消灭：第一次停止即用观测 turnID
成功，无需第二次点击。

## 2026-08-22 Codex Desktop：daemon 探测失败会锁死私有 stdio

官方 Desktop（当前 ChatGPT `app.asar`）每次 `transport.connect()`——包括断线重连——都会再跑 `codex app-server daemon version`，spawn timeout 2500ms。control socket 不在时该命令通常立刻失败，不会等满 2.5s。失败就把 `kind` 写成 `stdio`，`supportsReconnect()` 变 false，这个 Desktop 进程再也回不去 websocket。首次重连大约在 websocket 断开后 1s。

这不是 MacBridge 能翻的开关，也不能靠退出 Link 修好。已经锁死的 Desktop 只能完整退出一次。登录座位必须在那 1s 窗口内把官方 daemon 补回；60s 周期 `daemon start` 盖不住。MacBridge 退出本来就不会 `daemon stop`。

ccswitch 改完 `config.toml` 之后，正确杠杆是官方 `codex app-server daemon restart`（让同一 daemon 读新配置），不是把 daemon 杀掉等 Desktop 自己掉 stdio。restart 期间如果 Desktop 先探测失败，仍会锁死，所以 restart 必须快于上述窗口。

本机 2026-08-22 活体：Desktop 内嵌 CLI `codex-cli 0.149.0-alpha.4.1`，managed standalone `0.149.0-alpha.4`。exact `--version` 字符串门把登录座位和 attach env 整段 fail-closed 掉了，但 Desktop 自己的 `fg(appServerVersion)` 只要求 ≥ `0.141.0`，这个 daemon 其实过得去。座位补位不得再被这条更严的门挡住。

## 2026-08-21 OpenCode Web：无权限模型重试时 iPhone 只显示执行中（owner 关闭）

### 现象
选没有套餐权限的模型发消息。Mac Desktop 立刻显示「当前订阅套餐暂未开放… / 重试中 N 秒后 - 第 M 次尝试」。iPhone 全程「执行中」，等重试全部失败才出报错。

### 官方 Desktop
`packages/session-ui/src/components/session-retry.tsx`：`session.status.type==="retry"` 时画重试行，正文是 `status.message`，副行是 retrying + inSeconds(`next`) + attempt。SSE 形状 `{type:"retry", attempt, message, next}`。1.18.18 serve 经 `SessionStatus.set({type:"retry",…})` 发 `session.status`（`packages/opencode/src/session/status.ts` + processor retry policy）。

### 关闭结论（2026-08-21，owner）
两轮真机仍看不到执行中的重试行。owner：属于锦上添花，**不再修**。idle 后的终态报错已经有（`session.error` → text + `turn_error`），产品可接受。树里留下的 gap 绕过 / `runtimeStatusRevision` **不得当已修好广告**，CHANGELOG 不记「修复」。

### 两轮弯路（下次若重开，先证明 wire）
1. **只绕 envelope gap**：以为和 todo deck 同一扇门。`session_retry_status` 不进投影 deny-list、raw 是设计上的唯一载体；syncV2 跳号会让 gap 门整帧丢掉。绕过后真机仍失败。
2. **再补时间线重画**：runtimeStatus 字典不是 `@Published`，重试期间没有 messages/投影补丁，`scheduleRender` 不跑。加了 `runtimeStatusRevision` 后真机仍失败。

活体日志（ses_fef7，约 19:38 send → 19:39:58 终态）：中间约 70s **没有任何** INFO 级 `session_retry_status`（当时它只打 Debug）。终态是 `passive event text_delta` + `K4Patch turn_error`——这就是 iPhone「等重试结束才出报错」的载体。**没有在重试窗口里证明 Mac 把 retry 帧发出去了。** 第二轮才把该事件升到 INFO，但 owner 已关题，未再取证。

### 若重开：最短证据顺序
1. 再发一条无权限模型，立刻 grep go-bridge.log 的 `session_retry_status`（现已 INFO）。没有这条，先修 Mac SSE 解析/发射，不要再动 iOS 渲染。
2. 有这条，再看 iOS 是否收到、`runtimeStatusRevision` 是否 +1、RunningStatusBar 是否带 subtitle。用户看的也可能是输入框「执行中」（`isGenerating`），不是状态条。
3. 官方对照：Desktop 行来自 `session.status` 的 `message` + `next` + `attempt`，不是终态 `session.error`。

不要再猜「再加一层 iOS 门」而不看 70s 窗口的 wire。

## 2026-08-21 OpenCode Web：重启 App 后历史没有思考过程和工具调用

## 2026-08-21 OpenCode Web：重启 App 后历史没有思考过程和工具调用

### 现象
iPhone OpenCode Web 打开正在写文件的 session：直播能看到思考过程 +「已读取文件 / 已编辑文件」。杀 App 再进同一位置：只剩一段拼在一起的正文，「过程」里没有思考、没有工具卡。Claude / Codex / 旧 OpenCode 的历史工具调用都正常。

### 教训（又一次：先看官方 Desktop/web）
不要猜「是不是 iOS 渲染」或沿用本仓 legacy `mapRichHistoryEntry` 的嵌套 `tool` 对象。官方 web 冷开历史跟直播走同一份 `session.messages` 的 `{info, parts}`，时间线用 `renderable(part)` + `groupParts`：`type=reasoning` 有 text 就显示，`type=tool` 且 `tool` 不是 `todowrite` 就显示工具卡。

本轮如果先读：
- `packages/schema/src/v1/session.ts` `ToolPart`：`tool: Schema.String`，`state` 是兄弟字段
- `packages/app/src/context/server-session.ts` `client.session.messages`
- `packages/session-ui/src/components/message-part.tsx` `renderable`

活体 `GET /session/ses_fe7908102ffeGCC5opsX3UbGt2/message`（红楼梦，directory=cordcode-ios）立刻对上：25 条 tool 全是 `"tool":"read"|"edit"`，38 条 reasoning 带 `text`。本仓测试夹具却写成了 `tool: {id, toolName, state}`——那是从旧 `agent/opencode/providers.go` 抄来的，从来不是 1.18.18 HTTP 形状。

### 根因
`mapRichHistoryEntry`：

```
tool, _ := part["tool"].(map[string]any)
if tool == nil { continue }
```

官方 `tool` 是字符串，断言失败，**每一条工具 part 被丢弃**。冷投影因此只有 text（+ reasoning parts），iPhone 重启后看不到工具卡；过程区只剩拼起来的中文正文。直播不走这条 mapper（SSE `handleToolPart` 已经 `firstString(part, "tool")`），所以当场有工具卡。

### 修复
按官方 ToolPart 映射：`tool` 字符串 + 兄弟 `state.{status,input,output,title,metadata,time}` → 投影 step（id=part.id，title、toolInput、duration、edit 的 `metadata.filediff` → fileChanges）。`todowrite` 跟官方 `HIDDEN_TOOLS` 一样不进时间线（todo dock 另走）。reasoning 继续进 Parts，并写入 `Thinking` 给 overlay。单测用官方字符串形状，不再把嵌套对象当 1.18.18 真值。

### 不要再做的
- 不要用 legacy adapter / 手写 fake server 的 tool 形状证明外部协议。
- 不要在 iOS 上为「冷开没有过程」加启发式。Mac 把 part 译对，投影 hydrate 已经会发 `reasoning_delta` / `tool_started`。

### 真机验收（2026-08-21）
owner：测试结果基本符合预期。

## 2026-08-20：opencode-web 新会话首回合完成态闪烁

### 现象
iPhone 新开 OpenCode Web 会话发第一条，输入框立刻（&lt;0.5s）变完成态，等到第一条 `text_delta` 才回到执行中。第二条和 dsh-web 首条不闪。

### 根因
冷 `get_session_projection sinceRev=0` 提交 `execution.phase=idle`（`executionBytes=16`）。活会话未收口 user turn + 空 assistant 壳被当成完整快照 `turn_completed`；pathless hydrate 从空 reducer 起，`CommitHydrateTransaction` Restore 冷 idle 盖掉已经 live 的 running（`pendingLive=0`）。R2 的「registry-live 且历史 0 条就 200ms×6 再拉」打不中这条（真机是 1 条用户消息），且违反 SSV2 第 4/6 条。

### 修复（只动 Mac Kernel）
拆掉 0 条重拉。live 空 assistant 不再 `turn_completed`。`CommitHydrateTransaction` 禁止 running/requires_action → idle。pending→real 早 rebind 保留。不要在 iOS 用 localSend 否决投影 idle。

### 真机验收（2026-08-20）
owner：新建 session，输入发送后输入框一直执行中，可正常流式，输出完变为完成态。符合预期。

## 2026-08-17：Claude / Grok / OpenCode 点 ⭕ 没数据

### 现象
iPhone 上 DeepSeek Harness / Codex 的上下文圈有数，Claude Code / Grok Build / OpenCode 打开是「暂无上下文用量数据」。

### 根因
iOS 圈只认 `get_session.contextUsage` 且要求 `contextWindow > 0`。全仓原先只有 Codex 和 dsh-web 实现 `GetSessionContextUsage`。

- **Grok**：88 份 `updates.jsonl` 里 **0 条** `usage_update`。用量在 `turn_completed.usage`（回合计费累计，不能当占用）和 `signals.json` 的 `contextTokensUsed` / `contextWindowTokens`（本机 25/25 有数，窗口 500K）。`auto_compact_started` 也带同一对字段。
- **OpenCode**：`GET /session/:id` 有 `tokens{input,output,reasoning,cache}`；模型窗口在 `/provider` 的 `limit.context`（如 mimo-v2.5-free = 200K）。driver 以前不读。
- **Claude**：JSONL assistant `usage` 有 input/cache/output，**没有**官方窗口。`/usage` 是订阅额度。启发式占用 = 最后一条 assistant 的 `input+cache_read+cache_creation`，窗口 200K / 1M。

### 修复
三家补 `GetSessionContextUsage` + 必要的 live `context_usage_updated`。不要把 Grok `turn_completed.usage.inputTokens` 当占用。

## 2026-08-17：DeepSeek Harness 长会话 iPhone「无法加载会话投影」

### 现象
「Exec plan 交接审计与门禁核实」在官方 Mac web 能打开，iPhone 报「无法加载会话投影。重新打开会话可重试。」（无「超时」二字）。同 backend 的短会话正常。

### 根因
`get_session_projection` → `projection.hydrate_failed`（约 0.67s，不是 15s 超时）。官方 `session.history` 按 **user/assistant 消息数** 分页，一条消息含成千上万 `assistant/chunk`。dsh-web 以前 `maxMessages=2000` 一页拉完：该会话 258 条消息 / 277k 事件 / **55MiB**，超过 unary 32MiB `LimitReader`，JSON 被截断，Unmarshal 失败。对照：「西游记」短会话 3MiB，能打开。

### 修复
每页改官方默认 50 条消息；读满 32MiB+1 视为超大页并减半重试。不要再把 `maxMessages` 当成事件条数。


## 2026-08-04 追加复盘：Grok 外部任务 iOS 输入框卡"完成态"

### 现象
Mac 端发起的 Grok Build 任务正在执行时,iOS 端同步该 session 的过程中输入框一直停在"完成态",没有进入"执行中"。Claude/Codex/OpenCode 无此问题。

### 根因(两层,均在 MacBridge)
1. **codec 丢弃上游 durable `turn_completed`**:`convertSessionUpdate`(`agent/grokbuild/acp_codec.go`)的 default 分支把 `turn_completed` sessionUpdate 当未知类型丢弃。真实 `~/.grok/sessions/*/updates.jsonl` 证实上游在终态发 durable `turn_completed`(440 次,带 `prompt_id`+`stop_reason`,method `_x.ai/session/update`,无 isReplay → 进 leader live rail),但 codec 不映射它。
2. **relay loop 不合成 turn-start 信号**:`grokLeaderSessionRelayLoop`(`go-bridge/handlers_relay.go`)只转发内容事件,不合成 `turn_started`/`session_state_changed(running)`。上游 grok-build 不发任何 turn-start sessionUpdate(真实数据 `response_started`=0)。

iOS 进入"执行中"(`isGenerating`)唯一可靠触发是收到 `turn_started` 或 `session_state_changed(running)`。两个都收不到 → 输入框停完成态。

### 诊断方法(autonomous,不依赖 owner)
- 用本机 `~/.grok/sessions/*/updates.jsonl` 作为真实协议样本(上游持久化的通知 = live rail 同源),grep `sessionUpdate` tag 分布。替代了 audit 报告要求的"真实 leader wire 捕获"。
- 对照 codex(`handlers_relay.go:347-355`)和 claude(`:586-597`)的 file relay:它们都会在检测到活跃 turn 时合成 `turn_started`+`session_state_changed(running)` —— grok 的 leader relay 缺这步。
- audit-plan 评审(`docs/2026-08-04-grok-external-turn-completing-state-fix-audit.md`)纠正了 v1 方案的事实错误("upstream 永不发 turn_completed"是错的)。

### 修复(三层,MacBridge 单侧)
- **改动 A(codec,主收口)**:`convertSessionUpdate` 加 `case "turn_completed"`,映射成 `EventResult{Done:true, TurnID:prompt_id}`;`error` stop_reason 转 `EventError`。`sessionUpdatePayload` 加 `PromptID`/`StopReason` 字段(兼容 `prompt_id`/`promptId` 两种 key)。`mapAgentEvent` 已把它转成 wire `turn_completed`,relay loop markIdle 自然生效。
- **改动 B(relay,开始信号)**:`grokLeaderSessionRelayLoop` 在首个内容事件(`text_delta`/`reasoning_delta`/`tool_started`/`tool_finished`)前合成 `turn_started`(turnId 空)+ `session_state_changed(running)`,置 `turnArmed`。turnId 解耦(开始信号用空 ID,结束信号用 prompt_id),避免 ID 跳变。
- **改动 C(relay,兜底)**:`defer` 里若 `turnArmed` 仍为 true(leader 异常断开,未收 turn_completed),补发 `session_state_changed(idle)` + markIdle。

### 关键决策记录
- **不动 `handlers.go:1824-1839`(Bug 4 补丁)**:它针对 iOS 本地发起路径(那条路径 `session.go:380` 自己 emit turn_started),与 leader relay loop 是独立路径。动它会重新引入 2026-07-12"卡执行中"回归。
- **不动 iOS** `sessionSyncV2ProjectionBackend`(grok 被排除):grok 尚未迁移到 projection,是已知架构边界。iOS 对 wire 事件是 backend-agnostic 的,Mac 发对事件就能进入执行中。
- **不动 capability**:保持 `requiresPollingForExternalTurns=true` 的 probe 并行兜底。

### 验证
- 定向测试:`go test ./agent/grokbuild/... -count=1`(15 通过,含 5 个新 turn_completed 变体)+ `go test ./go-bridge/... -count=1`(含 6 个新 grok leader relay 测试:合成/幂等/error/plan 不触发/defer idle/subscribe error)。
- Release 构建 + 覆盖安装 `/Applications`(runtime commit `4218327f883a`,built `2026-08-04T13:43:50Z`),8777 监听者核对为正式版。
- **待 owner 真机验收**:Mac 发起 Grok 任务 → iOS 输入框变"执行中";任务完成 → 恢复"完成态"(不卡执行中)。这是诚实边界——单测和部署不等于端到端成功。

### 后续原则
- **真实数据样本胜过静态推测**:audit 用上游源码静态分析发现"upstream 发 turn_completed",但本机 `updates.jsonl` 直接证实了 440 次 + 字段形状,是最短路径。排查协议类 bug 先 grep 本机持久化样本。
- **turn 生命周期合成属 relay 层,不属 codec**:codec 是无状态 ACP→core.Event 映射;turn 开始/结束的 wire 语义合成发生在 relay loop(和 codex/claude 一致)。但 durable 终态信号的**映射**(把上游 turn_completed 转成 core.Event)属 codec——因为它是协议变体到事件的 1:1 转换。
- **leader channel close ≠ turn 结束**:close 只表示 leader 断开。turn 结束的权威信号是上游 `turn_completed`。收口逻辑必须区分两者。



### 最终状态（后续 agent 先看此表）

| 方案 | remote-web | iOS | MacBridge / Relay | 结论 |
|---|---|---|---|---|
| A. history ETag / 条件请求 | 已接入 | 早已接入 | MacBridge 早已有 `ifNoneMatchRevision` | 解决重复读取，不解决首次冷加载 |
| B. gzip 后再加密 | 已接入 | 已接入 | MacBridge 按客户端能力对 Relay 下行压缩 | 解决首次大 history 的传输体积 |
| D. 大响应分片 + 可抢占优先级队列 | **未实施** | **未实施** | 单 `readLoop` + `writeMu` 架构仍在 | 目前只有客户端编排缓解，不能写成 D 已完成 |

### A：ETag 只能复用“与 revision 配对的真实 history”

MacBridge 的 `get_session_messages` 会在完整响应中返回 `revision`；客户端下次发送
`ifNoneMatchRevision`，命中后服务端只返回紧凑的 `unchanged: true`。iOS 原本已使用该契约，
remote-web 此前漏接，导致每次切回看过的 session 仍重复传输数 MB history。

remote-web 的最终实现同时保留 per-session 内存消息 bucket 和对应 revision，并以
`sessionId + directory` 隔离 revision。收到 `unchanged: true` 时只允许恢复与其配对的真实内存
bucket；若本地 bucket 不存在则直接报错，不能用空数组、旧快照或 fallback 冒充成功。backend
切换会清空消息与 revision，完整响应没有 revision 时也会删除旧 revision。

这项优化只覆盖第二次及后续读取。第一次打开 session 没有 revision，仍必须获得完整 history；
因此不能用 A 的单测命中或切回秒开，声称首次 Relay 冷加载已优化。

### B：压缩必须发生在 padding / AEAD 之前，并由客户端显式协商

正式能力名是 `relay_gzip_v1`，只影响 MacBridge → Relay client 的在线帧：

```text
Bridge JSON -> gzip -> padding -> ChaCha20-Poly1305
ChaCha20-Poly1305 -> remove padding -> gzip decode -> Bridge JSON
```

MacBridge 只在连接是 Relay、认证后的 Bridge hello 声明能力且 hello 被接受时启用；当前 sender
只考虑至少 32 KiB 的 payload，并且只有 gzip 后确实更小时才发送 `contentEncoding: "gzip"`。
该字段属于 envelope AAD，攻击者或中间层增删、替换字段都会使认证失败。Web 只有在浏览器提供
`DecompressionStream` 时才声明能力；iOS 只在 Relay transport 声明，并在解密后使用有上限的流式
gzip decoder。旧 Web/iOS、不支持能力的客户端、Direct WebSocket、iOS → Mac 上行和 mailbox
继续使用原格式。

WebSocket `permessage-deflate` 作用在已经加密的高熵 envelope 上，几乎不能压缩，不能替代 B。
解压失败、未知编码、超出解压上限都必须暴露真实 transport 错误，禁止把压缩帧当普通 JSON
重试或加入静默 fallback。

### D：当前只有“优先发送小 RPC”的缓解，核心架构没有改

MacBridge Relay 路径仍是单 readLoop 同步处理 RPC，所有出站写共享 `writeMu`。一个大 history
响应写 socket 时，后到的模型、权限等小 RPC 仍可能在接收缓冲区等待。remote-web 当前在切换
session 前预取模型和权限，并用 promise dedup 让 Composer 复用结果；soft reconnect 期间还会等待
新 transport ready 后才允许 session history 请求。这能避免常见的“小 RPC 排在巨帧后面超时”，
但它不是分片，也不是可抢占队列。iOS 本轮接入 A/B，没有新增 D 协议或队列。

未来真正实施 D 时，行为拥有者主要在 MacBridge / Relay transport，而不是在 Web 或 iOS UI 层。
至少要同时定义：分片 envelope 与 AAD、每片和整消息大小上限、counter/顺序与重组、取消和超时、
有界 backpressure、控制帧优先级、公平性，以及旧客户端协商。Relay 服务器现有 per-device queue
也不等于“大 RPC 已分片且可被小 RPC 抢占”。在这些完成并有跨端测试前，任何 agent 都不得把
prefetch、gzip 或现有 queue 记为“D 已完成”。

### 诊断与验收经验

- 先看 MacBridge `session loading metrics`：`request_total_ms` 接近 `socket_send_ms` 表示瓶颈在
  Relay 写入；history parse/encode 很短时不要先改 handler。
- A 的验收要分别测首次打开与切回：首次仍传全量，切回应命中 `unchanged` 且保留真实消息。
- B 的验收要确认 hello/ack 能力、Mac 日志里的压缩前后字节数，以及 envelope 在解密后才能解压。
- Web owner 实测 B 后长 session 明显加快；iOS 已完成定向单测、真机构建安装，但 Relay 长 session
  的最终加载速度仍需真实 Relay 路径人工验收，不能用 LAN 或单元测试替代。
- 即使 A/B 已显著缩短时间，单 readLoop + `writeMu` 的 head-of-line blocking 仍是已知架构债；
  若以后仍出现巨帧阻塞小 RPC，再评估 D，不要回退整页 history 或降低正常大帧写超时。

---

日期：2026-07-04
结论：本次 owner 真机复现的主因不在 MacBridge 重复生成，也不在 Claude CLI stdout 中断，而在
iOS 本地 Claude live stream 期间仍执行普通历史同步并覆盖 timeline。MacBridge 日志用于排除
重复执行，并暴露 iOS 高频 `get_session_messages` 是关键证据。

## 现象

iOS App 冷启动后，Claude Code 模式打开一个已存在 session，发送“讲个狐狸笑话”。发送后出现
runtime status strip“正在思考中”，开始流式输出。回复较长时，输出一段后页面闪一下，status strip
重新出现，回答不是从上次半截继续，而是从头重新流式输出。重复 3 到 4 次后才完整收口。随后
输入框还会短暂再次进入执行中状态，几十秒后恢复。留在同一个 session 再发第二问时正常。

## MacBridge 侧排查结论

日志窗口内没有看到重复 `send_message`，也没有 Claude CLI 断连重启导致同一 prompt 被重新执行。
相反，MacBridge 持续收到 iOS 发来的 `get_session_messages`，并返回同一 session 的 persisted
history；同时夹杂 `fetch_todos`。这说明服务端只是按请求返回 transcript 历史，视觉上的“从头重播”
来自客户端把历史片段重新应用到当前 live stream。

排查期间曾修过一个真实但非主因的 MacBridge 风险：Claude 既有 session 的 transcript file relay
和真实 AgentSession stdout relay 共用 `relayRunning` 布尔位。若 file relay 抢先占位，
`send_message` 可能无法启动真实 stdout relay。修复后改为记录 relay kind（agent / claude_file），
并允许真实 agent relay 接管 file relay；该修复有回归测试保护，但 owner 复测确认问题依旧，
因此它不是这次“从头重播”的主因。

## 最终根因

iOS 本地发送 Claude turn 时，本地 user/assistant 使用本地 UUID；MacBridge 返回的 Claude
transcript 历史使用服务端 id。生成中如果普通 `loadMessages` 把服务端历史套回 UI，就会把当前
live stream 中的 assistant 替换/合并成服务端较旧或不同 id 的片段。长回复期间这些历史同步多次发生，
所以用户看到 status strip 闪烁、回答半截消失并从头输出。

最初只挡住 iOS `startRunningSessionPolling` 中的一处 `loadMessages`，但冷启动既有 session 时仍有
resident probe、后台刷新、session 切换后续刷新等路径会直接调用 `loadMessages`。因此正确边界不是
“某个轮询入口跳过历史”，而是 iOS 历史同步入口本身必须识别 Claude 本地 live turn ownership。

## 最终修复方案（iOS 仓）

1. `ChatViewModel+MessageSync.loadMessages` 增加入口级保护：Claude Code 本地 turn 进行中时，
   普通历史同步直接返回，不 fetch、不 apply、不写 cache。

2. `recoverAfterSendCompletion` 显式传 `allowDuringClaudeLocalSend: true`，允许 turn 完成后做一次
   权威历史同步和快照写入。也就是生成中禁止历史覆盖，完成后仍以服务端历史对账。

3. `startRunningSessionPolling` 在 Claude 本地 turn 看到远端 idle 时进入
   `recoverAfterSendCompletion`，而不是直接清理执行态。

4. iOS 增加回归：
   `RemoteRunningSessionTests.testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream`
   和 `testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream`。

## 验证

- iOS 定向测试 3 条通过：
  `testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream`、
  `testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream`、
  `testClaudeCodeTurnCompletion_transitionsToIdle`。
- iOS Debug build 已安装到连接的 iPhone 16 Pro。
- owner 真机复测确认：同一路径冷启动既有 Claude session 后，首轮长回复不再半截闪烁和从头重播。

## 后续原则

- MacBridge 日志出现高频 `get_session_messages` 但没有重复 `send_message` 时，优先怀疑 iOS
  timeline 同步覆盖，而不是 Claude CLI 重跑。
- Claude 本地 live turn 期间，普通历史同步不能作为生成中刷新源，只能在完成后做权威对账。
- MacBridge 的 file relay / agent relay 状态拆分保留为正确的风险修复，但不要把它当作本次
  现象的根因。

---

# OpenCode session 列表加载方案（实际实现）

本文记录 CordCode iOS OpenCode 模式 session 列表加载的真实修复路径。
设计文档 `docs/2026-07-02-opencode-project-first-session-list-plan.md` 的部分判断
（array-only/no-cursor、保守 limit=5）在真机验证后被推翻；本文以最终真机验证通过
的实现为准。

## 问题

iOS OpenCode 模式 session 列表存在三个叠加缺陷：

1. 每个项目只显示 1~3 条 session，远少于 OpenCode Desktop 的真实数量。
2. 没有「加载更多」入口，无法翻页。
3. 冷启动只加载字母序前 3 个项目，其余项目标题出现但 session 为空。

## 根因（三个独立缺陷叠加）

### 缺陷 1：MacBridge hasMore 逻辑对小项目是错的

OpenCode 路径原来没有全量列表，靠「返回数 >= limit」猜 hasMore。小项目返回 2~3 条
（< limit 5），hasMore=false，iOS 据此判定「已到末页」，「加载更多」入口永不出现。

对比 Codex/Claude 路径用 `paginateSessionList`：内存里有全量列表，用
`len(sessions) > limit` 算 hasMore 并返回可翻页的 nextCursor。

### 缺陷 2：rootsOnly 客户端丢弃把子 session 全砍了

MacBridge 原来在 Go 侧 `continue` 掉 parent_id 非空的 session。OpenCode 重度项目的
子 session（subagent、fork、compaction）比例高，砍完只剩 1~3 条 root。

### 缺陷 3：冷启动 .prefix(3) 只给前 3 个项目发请求

`loadSessions` 中 `.prefix(3)` 硬编码只加载前 3 个项目 bucket，其余靠 LazyVStack
视口懒加载，截图时多数项目没滚到。

## 最终修复方案

核心思路：让 OpenCode 路径走和 Codex/Claude 完全相同的 `paginateSessionList` 分页。

### MacBridge 侧（go-bridge）

`ocHandleListSessions`（handlers.go）重写：

- 对每个项目一次性从 OpenCode server 拉取 100 条 root session（常量
  `openCodeSessionFetchLimit = 100`），而不是之前的 limit=5。100 匹配 server 默认上限。
- `rootsOnly` 不再在 Go 侧客户端 `continue` 丢弃，而是作为 `roots=true` 查询参数
  发给 server，由 server 做 SQL `isNull(parent_id)` 过滤（和 OpenCode 源码一致）。
- 拉回后在内存按 `updatedAtMillis DESC` 排序，然后调用 `paginateSessionList(mapped,
  cursor, limit)`——与 Codex/Claude 走同一个函数。
- `hasMore` 和 `nextCursor` 由真实剩余数据量计算，不再瞎猜。
- `rootsOnly + cursor` 不再被拒绝。

`OpenCodeProxy.listSessions`（opencode-proxy.go）增加 `Roots bool` 字段，发 `roots=true`。

### iOS 侧（cordcode-ios）

`loadMoreOpenCodeSessions`（SessionsView.swift）改为 cursor 追加分页：

- 不再用「limit 加 5 重取」的旧方式。
- 用 bucket 已存的 `nextCursor` 发下一页请求，`append: true` 追加到已有 session 列表。
- 守卫条件改为 `bucket.hasMore && !bucket.isLoading && bucket.nextCursor 非空`。

侧栏（SidebarView.swift）上一轮已补的改动保持：项目区块进入视口自动触发
`loadOpenCodeBucketIfNeeded`；未加载项目显示「加载中」，加载完为空显示「暂无会话」。

### 为什么不直接用 OpenCode server 的 cursor

OpenCode server 的 `/api/session` 有 cursor（`packages/protocol/src/groups/session.ts`），
但 stable 1.17.13 的 `/session`（instance httpapi）是 array-only。MacBridge 连的是
instance httpapi。所以一次性拉 100 条再在内存分页是当前最正确的做法；未来上游
instance httpapi 支持 cursor 后可零改动切换到 server-side cursor。

## 验证

- `go test ./go-bridge/... -count=1` 全通过。
- `TestOpenCodeListSessionsFetchesLargePageAndPaginatesInMemory`：验证上游拉 100、
  roots=true、limit=2 切片返回 2 条且 hasMore=true，第二页 cursor 翻页返回剩余。
- iOS `SessionLoadOwnershipTests` 通过。
- Mac Release build + /Applications 覆盖安装 + runtime 8777 确认。
- iOS Debug build 安装到 iPhone。
- owner 真机验收通过（2026-07-03）：项目标题 basename、Chat 项目首页 5 条可翻页、
  小项目显示真实数量、go-bridge.log 无 ERROR。

## 设计文档中过时的部分

设计文档判断 OpenCode 为 array-only/no-cursor，因此采用保守 limit=5 + 客户端 rootsOnly
丢弃。实际读源码后发现 server 默认 limit 50/100 且支持 roots SQL 过滤，改为一口气拉 100
再内存分页更正确。完成情况文档 §4 的 Known Limits 中「无服务端加载更多是正确行为」
已被本方案推翻。

## 2026-07-04 追加复盘：冷启动既有 Claude session 的 spurious session_state_changed(idle)

iOS 侧「首轮流式从头重播」再次复现后，跨仓联调定位到一条 Mac 侧的已知 artifact。

### 现象（iOS 侧表现，根因在 Mac）

iPhone 冷启动既有 Claude Code session 并发送消息后，回复输出一段后闪一下、从头重播，重复 3~4 次。
Mac 日志：单个 turn 内 `get_session_messages` 被调 336 次，但 `send_message` 仅 2 次、`text_delta` 正常生成 ——
说明 Mac 没有重复执行 prompt，问题在 iOS 反复拉历史覆盖 live timeline（iOS 侧诊断与修复见 ../cordcode-ios/think.md 同节）。

### 真正根因（Mac 侧）

既有 Claude session 的 transcript file relay 与真实 AgentSession stdout relay 共用 `relayRunning` 状态位。
冷启动既有 session 时，**file relay 抢先基于上一轮已完成的 transcript** 广播 `session_state_changed(idle)`，
几乎与 iOS 的 `send_message` 同时到达（实测 T+0ms）；真实 agent stdout relay 要等 CLI 首个 stdout 才报
`session_state_changed(running)`（实测 T+10s）。对 Claude Code 的长 thinking 阶段（首 token 30s+）来说，
这个 spurious idle 是**假的** —— CLI 正在跑、只是还没出 token。

`7c1d97d "Harden Claude cold-start relay handling"` 的 relay-kind 拆分（agent / claude_file）曾试图修这个窗口，
让真实 agent relay 能接管 file relay。但实测 Mac 仍会在冷启动时发 spurious idle —— relay-kind 拆分修的是
「file relay 占位导致 send_message 起不来真实 stdout relay」，没修「file relay 仍会广播基于旧 transcript 的 idle」。

### 本次处理

iOS 侧兜底（已实现）：Claude local turn 首 text_delta 前收到的 `session_state_changed(idle)` 一律忽略，
ownership 稳住 `.localSend` 直到真实 `turnCompleted`。详见 ../cordcode-ios/think.md「首 token 前 spurious idle 收口」节。

Mac 侧**未**在本轮改：spurious idle 仍会发出，但 iOS 不再据此收口。Mac 侧的正确修法（后续独立清债）应是：
file relay 不得在「真实 agent relay 未确认 idle」前单方面广播 idle；或 file-relay 的初始状态读取不得用上一轮已完成
transcript 的终态当作当前 turn 的初态。

### 关键诊断信号（Mac 日志）

- 正常：`send_message` → `relayEvents forwarding event=text_delta` → `turn_completed`（一条 turn 内 send=1, turn_completed=1）。
- 异常（本次 bug 间接证据）：`get_session_messages` 在单个 turn 内被调数百次（iOS 反复拉历史）。
- Mac 是否发了 spurious idle：搜 `session_state_changed` / relay-kind 日志，看 send 后是否有先 idle 后 running 的翻转。

### 后续原则

- relay 状态位（file vs agent）的拆分要彻底：file relay 不得在 agent relay 未确认前广播 session 状态翻转。
- iOS 侧对 Claude local turn 首 token 前的 idle 不信任是必要防御；Mac 侧的根因修复不能让 iOS 撤掉这层兜底
  （冷启动 / 重连等场景仍可能再次出现 spurious 状态）。
- 跨仓「流式异常」排查：先看 Mac `relayEvents forwarding` 是否正常生成 text_delta（排除 Mac/CLI 重跑），
  再看 `get_session_messages` 频率（iOS 是否在 turn 内反复拉历史），最后用 `devicectl --console` 抓 iOS 端 NSLog
  定位 ownership 翻转时机。

# Claude 斜杠命令 / skill 文档泄漏：Mac 已干净、iOS 仍脏 = iOS 本地缓存陈旧

2026-07-04。Mac 源头过滤已上线，用户仍反馈 iOS 消息页显示 skill 全文；最终根因是 iOS
本地缓存未自愈，冷启动后恢复。记此条防止后续在 Mac 侧空转。

## 现象

iOS Claude Code 模式下，含 `/handoff-doc`、`/takeover` 等 skill 调用的 session 在消息页
显示 skill 文档全文（`Base directory for this skill: ... # Mission ...`）和 CLI 内部协议标签
（`<command-name>` / `<command-message>` / `<command-args>` / `<local-command-stdout>` /
`<local-command-caveat>`）。Mac 侧 `agent/claudecode` 已实现源头过滤并对全机 141 个真实
transcript 回归 0 泄漏，但 iPhone 仍显示旧内容 —— 表面矛盾。

## 根因（纯 iOS 侧，Mac 已干净）

Mac 源头过滤（`agent/claudecode/claudecode.go` 的 `normalizeClaudeUserText` +
内容驱动 `isClaudeSkillInstructionText`，`extractTextContent` 同步清洗预览/标题）**已生效**：
对 iPhone 此刻正在轮询的 `d8bff4fb-8275-4659-b6fe-082559c63d92`（最初泄漏的 9 个之一）
把真实 transcript 喂给线上同一 parser，输出全部泄漏 marker = 0；`/Applications` 内嵌
runtime（22:59 构建、pid 38490、8777 监听者）含修复符号；wire 映射 `richHistoryEntryToWire`
是干净数据的纯变换，不会回污。

iOS 显示的是**修复前持久化的本地缓存**，两层：

- 内存缓存（`MessageCacheManager.getCachedMessages`）：命中时 `ChatViewModel+MessageSync.swift`
  的 `loadMessages` 用缓存 `replaceMessagesFromServer` 后通常仍会继续 fetch（Phase 4 后
  `usesBackendLiveEventStream` 全部为 true、Claude 走分页，line 169-173 的早返回不触发），
  但缓存首屏先显示。
- 磁盘快照 `~/Library/Application Support/SessionSnapshots/snapshot-<scopeHash>-<sessionHash>.json`
  （`SessionSnapshotStore.swift`）：**键只含 `(backend identity, sessionId)`，无内容版本号**；
  `currentSnapshotSchemaVersion=3` 只做「比 app 新才删」的前向校验，**不剔旧**。修复前写入的
  脏快照在普通重开/冷启动后仍会被 `loadSnapshot` 读出并先渲染。

冷启动清掉内存缓存后，磁盘快照路径 fetch 到 Mac 干净数据、`reconcileServerMessagesAgainstDisplayedSnapshot`
按内容 diff 自愈，并 `persistSnapshot` 写回干净快照。用户冷启动后所有 skill session 恢复正常。

## 验证

- 字节级取证（临时测试，已删）：对真实 `d8bff4fb….jsonl` 跑 `LoadClaudeRichHistoryFromReader`，
  118 条 / 1.28MB 输出中 `Base directory for this skill` ×0、五类命令标签 ×0、`## Mission` ×0，
  仅保留 2 个合法 `Launching skill` tool_result。
- 线上二进制 = 修复版：`strings` 命中 `isClaudeSkillInstructionText` / `normalizeClaudeUserText`；
  pid 38490 即 `/Applications/CordCodeLink.app` 内嵌 runtime。
- 设备验证（owner）：冷启动 iOS App 后打开原 session，skill 全文消失；其他 skill 命令 session
  亦符合预期。

## 后续原则

- **「Mac 干净但 iOS 脏」排查优先级**：先在 Mac 侧对 iPhone 实际请求的那个 sessionID 做字节级
  取证（runtime 日志里 `get_session_messages` 的 sessionID + response_bytes），再动 iOS。
  Mac 干净则问题在 iOS 缓存或传输，不要回头改 Mac。
- **Mac 内容侧修复对 iOS 不是即时生效**：iOS 有内存缓存 + 磁盘快照两层，磁盘快照键无内容版本。
  一次冷启动可触发 reconcile 自愈；若需强制清旧脏快照，`SessionSnapshotStore` 没有 min-schema
  删除逻辑，得 `clearAllData` / 删 SessionSnapshots 目录 / 重装 App。
- iOS 源头治理（可选清债，本轮未做）：给 `SessionSnapshotStore` 加 min-schema（低于即删）或内容
  hash 版本键，让 Mac 侧的内容级修复能确定性失效旧快照，而不依赖冷启动 + reconcile 时序。
- 排查工具：`tail -f ~/Library/Application\ Support/CordCode\ Link/logs/go-bridge.log` 看
  `get_session_messages` 的 sessionID/response_bytes/result_count；对目标 session 可写临时 Go 测试
  调 `LoadClaudeRichHistoryFromReader` 对真实 JSONL 取证。

## 2026-07-05 Claude session PID 复用 latent bug（已修）

- **现象假设**：某 Claude session 的 stub（`~/.claude/sessions/<pid>.json`，含 `sessionId/pid/cwd`）
  因 claude 异常退出未清理而残留；OS 把该 PID 复用给无关进程。`GetRunningSessionIDs` / `LiveSessionProcess`
  原本只用 `kill(pid, 0)` 判活 → stale session 被误判 running → `enrichSessionStateWithAgent` 在
  `resume_session`/`list_sessions` 响应里报 `runtimeState=running` → iOS 进入 phantom executing
  （输入框锁"执行中"、status strip 不消失）。
- **07-05 复现未触发**：那次是 external turn（用户在 Mac Claude 窗口打字），且目标 session `16c63341`
  的 stub 当时正确缺失（`~/.claude/sessions/` 只有其它 PID），所以 `GetRunningSessionIDs` 正确报 idle。
  但代码审查发现 `agent/claudecode/proc_unix.go:49 isProcessRunning` 是纯 `kill(pid,0)`，不校验进程身份，
  是真实 latent bug。
- **修复**：`agent/claudecode/proc_seam.go` 新增可注入 seam `procIdentityAlive(pid, expectCwd)`，在
  `procAlive`（liveness）之上叠加 `verifyClaudeProcessIdentity`：`ps -p <pid> -o comm=` 校验可执行名含
  `claude`，`/proc/<pid>/cwd` readlink（Linux）/ `lsof -a -p <pid> -d cwd -Fn`（macOS）校验 cwd 与 stub 一致；
  任一强不匹配 → 非 live；平台探测失败 fail-open。`LiveSessionProcess` 和 `GetRunningSessionIDs` 改用该 seam。
  `IsProcessAlive`（公共契约，`go-bridge/handlers_relay.go:263` file-relay 每 tick 复查 cached PID 用）保留
  纯活性不动——那时身份已在 relay 启动时确认过一次，复用 PID 顶多多 silent watch 一个 live-idle TTL（90s），
  不发伪事件。回归测试 `TestGetRunningSessionIDs_PIDReuseNotRunning`。
- **排查要点**：报告"iOS phantom executing"时，先查 `~/.claude/sessions/*.json` 里目标 sessionId 的 stub
  是否残留 + 该 PID 现在是否仍是 claude（`ps -p <pid> -o comm,cwd`）。Mac 日志看 `enrichSessionStateWithAgent`
  返回的 runtimeState 需对照 transcript 是否真在跑（`isSessionExecutingCached`）。


# OpenCode active turn 流式：批处理 CLI → managed server + SSE（2026-07-06）

## 现象
iOS OpenCode 模式发消息无流式（等整段答完才出现）；Claude Code 模式正常。owner 测试矩阵：OpenCode（mimo V2.5 free）Mac 流式 / iOS 非流式；Codex 经官方/cliproxyapi 两端都流式；Codex 经 cligate 两端都非流式。

## 根因（OpenCode）
`agent/opencode/opencode.go` `StartSession` → `newOpencodeSession` 永远 spawn `opencode run --format json`（批处理，一轮 turn 只发 1 帧 `text_delta`），**完全绕过** Swift 已托管的 `opencode serve`（`-opencode-url` 传入的 `httpBaseURL`/`httpAuthHeader` 在 active turn 路径是死字段，只被被动 SSE subscriber + 诊断用）。managed server 本身流式正常（mimo 实测一轮 turn 49~80 帧 `message.part.delta` 分布在数秒内）。整个 opencode agent 改造前没有任何 POST 发消息代码（`providers.go` 只有 GET `fetchJSON`）。

## 修复（commit `330de91`）
- 新增 `opencodeServerSession`（`agent/opencode/server_session.go`，实现 `core.AgentSession`）：`Send` 时 `POST /session/:id/prompt_async`（204 非阻塞），消费一条 dedicated、按 sessionID client-side 过滤的 `/global/event` SSE。
- **复用** `sseSubscriber` 全套解析 + dedup + 生命周期翻译（`message.part.delta`→`EventText`、`session.status idle`→`EventResult`、`message.updated`→snapshot diff），只新增 `sessionFilter`（atomic；pending 态 chatID 未定时全丢，避免把别的 session 事件串到当前 iOS turn）。
- `StartSession` 按 `httpBaseURL` 分流：server 在 → `newOpencodeServerSession`；否则回退 `newOpencodeSession`（批处理 CLI 兜底，不中途切换）。
- 模型经 `resolveOpencodeModelLocked`（active provider 的 Name/Model）解析，建 session 时用 `{model:{id,providerID}}` 绑定。
- `providers.go` 加 POST-capable `doRequest`，`fetchJSON` 复用。
- live 集成测试（`server_session_live_test.go`，env-gated `OPENCODE_LIVE=1`）：80 帧流式 vs 批处理 1 帧。owner iOS mimo 真机验收逐字流式。

## 关键实证（防下次重新摸黑）
- **prompt body 的 `providerID/modelID` 不生效**（实测 Quotio body 被忽略，仍用 session 默认）。模型必须 **session 级**设定：`POST /session {model:{id,providerID}}`（字段是 `id` 不是 `modelID`）。
- managed server 上 **xirang 报 ProviderModelNotFoundError、zhipuai-coding-plan retry 5 次失败**（疑似 auth 绑 Mac opencode App 而非 managed server），只有 **opencode/mimo-v2.5-free** 实测跑通。owner 决定只管 mimo，zhipu/xirang 不查。
- `message.part.delta` = 流式 token 事件（sst/opencode#33397）；`/global/event` server 端**不支持** sessionID 过滤（sst/opencode#9650），**必须 client-side 过滤**（`sse_subscriber.go` 的 `extractSSESessionID` 已这么做）。
- `opencode serve` 的 SSE 有已知可靠性 bug（静默丢 #28729、不转发 #26866）—— server_session 不能假设 SSE 永远可靠，依赖 `deltaForPartSnapshot` 兜底。
- `x-opencode-directory` header 在 CJK workDir 有 bug（#13167/#13256），本仓库中文 owner 需留意。

## Codex 流式 —— 甄别 cligate 供应商，不要错查到 appserver_session.go
codex app-server（`agent/codex/appserver_session.go`）本身发 `item/agentMessage/delta` 完全正常（实测 31~38 帧/turn，通知名经二进制 strings 验证正确，handler/optOut/transport 全对，stdio 和 ws 两种传输都验证过）。**经官方/cliproxyapi 供应商 iOS 本就流式**。**经 cligate 供应商两端都不流式**（Mac codex + cligate 也一样），根因是 cligate 上游：`src/routes/responses-route.js:972` 和 `:1192`（`_responsesToChatBody` / `sendViaNativeResponsesProvider`）把 Responses→Chat Completions 时**硬编码 `stream:false`**，攒满整段再 `sendResponsesSSE()` 假装流式；同文件 `:1423-1441` 的 ChatGPT 账号池路径已是真流式 pipe，可参考修。**排查 codex 流式先确认供应商**，别一头扎进 appserver_session.go（那里没 bug）。

## 诊断 trick
- 数一轮 turn 的 `text_delta` 帧（go-bridge.log `relayEvents forwarding`）：1=批处理，多=流式。Claude ~1495 帧/turn 是参照。
- codex app-server 通知名可用 `strings /Applications/Codex.app/Contents/Resources/codex | grep -i agentMessage` 核实（不需 runtime 抓包）。
- opencode managed server 状态：`cat "$HOME/Library/Application Support/CordCode Link/opencode-managed-server.json"`（url/user/password），curl `http://127.0.0.1:<port>/global/config` 看 providers。
- codex app-server WS daemon：`codex app-server --listen ws://127.0.0.1:<port>`（CLI 原生支持，加 `--listen` flag）。

## Grok Build 真机疑难排查（2026-07-12，5 个 bug 全部修复）

### Bug 1: StartSession RWMutex read→write 升级死锁 → 30s RPC 超时
- **现象**：Grok session 发消息后 30s 超时。MacBridge 日志能看到 `initialize_done` 但没有 `session_loaded`。
- **根因**：`Agent.StartSession` 持 `a.mu.RLock()`，内部 `newGrokSession → loadSession → SetWorkDir` 请求 `a.mu.Lock()`。Go `sync.RWMutex` **不支持 read→write 升级**——永久死锁。
- **教训**：Go RWMutex 的 RLock→Lock 是永久死锁（不 panic）。StartSession 这类入口方法不要持锁——子方法自己加锁。
- **修法**：移除 `grokbuild.go` StartSession 的 RLock。

### Bug 2: convertSessionUpdate 未知类型 → EventError → iOS "unknown error"
- **现象**：turn 完成后弹 "unknown error"。
- **根因**：`acp_codec.go` `convertSessionUpdate` default 分支把未知 sessionUpdate type 转成 `EventError{Done:true}`。iOS 映射为 `("error", {message:"unknown error"}, done=true)`。
- **修法**：default → `return nil`（静默跳过未知类型）。

### Bug 3: legacy HistoryProvider 无 ID → iOS probe 误激活 generation
- **现象**：打开 Grok 历史 session 输入框卡"执行中"。
- **根因**：Grok 只实现 `core.HistoryProvider`，`core.HistoryEntry` 没有 `ID` 字段。iOS 为缺失 ID 生成随机 UUID → external-turn probe 误判新 turn。
- **教训**：历史消息 ID 必须稳定。修复路径是 `RichHistoryProvider`（`core.RichHistoryEntry` 有 ID）。ID 从 JSONL **物理行号**（不是过滤后数组索引）+ 原始行 hash 派生。
- **修法**：`session_catalog.go` 新增 `GetRichSessionHistory` + `deriveStableMessageID`。`grokbuild.go` 加 `var _ core.RichHistoryProvider` 断言。
- **关键约束**：`legacyHistoryEntryToWire` 是包级函数，没有 sessionID/行号参数——**不能在 legacy 路径补建 ID**。必须走 rich 路径。handler 已优先走 rich（`handlers.go:2442`）。

### Bug 4: handlers.go grokbuild 跳过 session_state_changed(running)
- **现象/根因**：grokbuild 的 `turn_started` 事件已通过 `syncRuntimeStateStore` 激活 iOS 执行态。额外发 `session_state_changed(running)` 会让 `isGenerating` 过早激活；如果 turn_completed 的 debounce 在 session 切换时被取消，isGenerating 永久残留。
- **修法**：`handlers.go:1481` `if agent.Name() != "grokbuild"` 跳过 running 广播。

### Bug 5: relayEvents idle timeout 后事件投递正常（非 MacBridge 问题）
- **排查结论**：5G relay 冷启动发消息卡住的问题，MacBridge 侧完全正常（日志确认 `send_message` → `turn_started` → `turn_completed` 全链路转发，`RelayDeviceConn.SendJSON` 无 dropped）。根因在 iOS 侧的 stale background generation marker（详见 iOS 仓 `think.md`）。
- **教训**：排查 relay 卡住时，先确认 MacBridge 日志有完整的 `relayEvents forwarding` 链——如果有，问题在 iOS 侧，不要改 MacBridge。

---

## 2026-07-13 Codex 既有 session 的思考过程历史重放

### 现象

Codex Desktop 的旧 session 在 iOS 首次重放时会缺少工具步骤，或只显示「已执行 N 个工具」。即使 iOS
清除了本地缓存，展示仍不变。

### 根因

Codex transcript 的 `custom_tool_call` 常以 `name=exec` 记录，真实 `tools.exec_command` / `tools.apply_patch`
嵌在 JavaScript 输入；`custom_tool_call_output` 则可能是结构化 content array。旧 parser 只把
`apply_patch` 视为有效 custom call，并把 output 当 JSON string 解码，导致数组解码失败后 tool completion
被丢弃。另一个常见误判是只编译 Debug Mac app：iPhone 的 bridge 实际运行 `/Applications/CordCodeLink.app`
内嵌 runtime，Debug 产物不会替换它。

### 修复与原则

1. `Output` 使用 `json.RawMessage`，兼容 JSON string 和带 `text` 字段的数组，保留真实 tool output。
2. 对 `exec` 包装只在其中包含**单一且可解析**的真实操作时还原：`exec_command` 提取 `cmd`，
   `apply_patch` 显示 patch 目标。多操作/混合包装必须保留 generic，不能杜撰为某一个操作。
3. parser 改动需要 `go test ./agent/codex -run TestGetRichSessionHistory -count=1`；交付到 iPhone 前还必须
   Release 构建、覆盖安装 `/Applications/CordCodeLink.app`、重启 app，并比对内嵌 runtime。只重装 iOS App
   无法部署 Go parser 改动。
4. 当「清 iOS 缓存后仍旧」时，先核对当前运行 PID 是否来自 `/Applications/CordCodeLink.app`，再检查
   installed runtime 是否与新 Release 一致；确认这一点前不要把问题归因为 iOS cache 或 renderer。

## 2026-07-14 Codex idle 历史 session 误触发执行态并导致 iOS 滚底

### 现象与证据

iOS 打开已经完成的长 Codex history 时，会周期性拉回底部。MacBridge 日志中该 session 没有 active runtime，
请求也始终是 `get_session_messages paginate=false limit=0`，所以这不是分页；但 idle 状态反复传输完整 history，单次约 5 MB。

### 根因与修复

Codex transcript file relay 已经是外部 turn 的权威来源，会发送真实的 `turn_started` / `turn_completed`。但
`agentDescriptor.RequiresPollingForExternalTurns` 仍把 Codex 标为需要 polling，促使 iOS 对已打开 session 做 full-history resident probe。transcript 补全或重写随即被客户端投影为新 turn，进而触发 follow-output 滚动。

因此 Codex descriptor 现在明确返回 `false`，由 transcript relay 负责真实 turn 生命周期；iOS 同时删除自己的 Codex 无条件 fallback。该标记是跨端状态机契约，不能把它当作可随意保留的兼容开关。

### 可复用教训

1. 已支持可靠 `turn_started` / `turn_completed` 的 driver 必须关闭 history polling；两条路径并存会让历史重写伪装成 live turn。
2. 排查“iOS 自动滚底”先查 `get_session_messages` 参数、active runtime 与 relay event 链路。滚动层通常只是正确响应了错误的 generating 状态。
3. 对长 transcript，full-history polling 既放大流量也放大误判窗口；完成态 session 不能以 history diff 作为活跃任务证据。

### 验证

`go test ./go-bridge -run TestAgentDescriptorCodex -count=1` 通过，Release runtime 已替换并配合 iOS 重装。owner 于 2026-07-14 手动打开并上滑已完成 Codex history，确认不再跳到底部；未运行 UI 自动化。

---

## 2026-07-14 Grok rich history 过程相位不能被 accumulator 重排

### 现象

Grok 已完成 history 已经能提供结构化 `parts[]`，但 iOS 展示仍不像真实执行过程：一轮中多个 reasoning
被集中在最前，工具调用集中在其后。前端即使按 parts 顺序渲染，也只能得到错误的相位排布。

### 根因

`turnAccumulator` 为了形成一条 assistant turn，将每一段 reasoning 汇总到独立字段，并在 build 时无条件
prepend 为第一个 `reasoning` part；工具和正文则另存 parts。于是原始
`reasoning → tool → reasoning → tool → text` 被不可逆地改写为
`reasoning(合并) → tool → tool → text`。

### 修复与约束

1. reasoning 只允许与**紧邻**的 reasoning 合并；遇到 tool 或 assistant text 必须 flush 到 `parts[]`。
2. assistant text 也必须作为真实 text part 写入，既是正文又是下一段 reasoning 的相位边界；不能仅留在
顶层 `Content`。
3. `build()` 只能 flush pending reasoning 后直接使用累积 parts，禁止再 prepend 一个汇总 thinking part。
4. 以合成 fixture 断言完整序列 `reasoning, tool, reasoning, tool, text`；只断言“含有所有 part”不足以防回归。

### 可复用教训

- `parts[]` 的顺序是 presentation contract，不是可自由规范化的缓存。任何跨类型重排都会让消费端失去
  恢复真实时间线的能力。
- 当视觉问题呈现为“所有思考在前、所有工具在后”时，应先检查 producer 是否丢失相位边界，而不是先改 iOS
  group/CSS。
- Go driver 变更只有 Release 构建、替换 `/Applications/CordCodeLink.app` 并重启 runtime 后才会作用于真机；
  iOS 重装本身不会更新 MacBridge parser。

## 2026-07-15 MacBridge 国际化（L10n）首选语言脏缓存与超时误判为配对失效

### 现象

1.  **系统语言检测被脏缓存覆盖**：Mac 系统的真实语言是中文，但安装最新版 MacBridge 启动后却依然默认显示为英文。
2.  **配对通道误报错失效**：用户打开「配对新设备」弹窗仅几秒钟，就突然弹出报错「配对通道已失效，配对二维码在 5 分钟内未被扫描会自动过期。请重新生成。」

### 根因

1.  **首选语言脏缓存机制**：
    *   SwiftUI 的 `@AppStorage("appLanguage")` 被用来持久化语言。如果之前在测试或历史版本中，由于默认策略或手动点击，导致本地 `UserDefaults` 中已经存入了 `"en"` 的键值，之后的启动将永远读取这个 `"en"`，这就产生了“脏缓存”的现象。
    *   在 macOS 沙盒下，如果没有在 Xcode 工程中显式完成中文 `.lproj` 资源束绑定，macOS 会对 `Locale.preferredLanguages` 进行自动截断和过滤（由于 App 本身在系统看来不支持中文，所以强制过滤为 App 所支持的 `en`），这就导致 `Locale.preferredLanguages.first` 永远返回 `en`，默认初始化的 fallback 完全失效。
2.  **网络超时被误判为 5 分钟到期**：
    *   在 `PairingView.swift` 的 `errorStateView` 判定逻辑中，错误地将包含 `"request timed out"`（即网络请求超时）的所有网络底层错误（例如因为向公网 Relay 轮询 claim 发生超时或连接被取消等），全部误判归类为了 `isTimeout`，进而渲染成了「配对通道已失效，二维码在 5 分钟内未扫描会自动过期」。
    *   事实上，配对通道真正的“5 分钟倒计时到期”有专门的 `.expired` 状态机做权威处理，完全不需要在普通的异常信息中通过包含 `"timed out"` 这种字符串来进行模糊映射。这直接导致网络波动时的临时超时报错被严重扭曲为了“二维码过期”。

### 修复方案

1.  **引入 `didUserSetLanguage` 用户主动切换标志**：
    *   在 `UserDefaults` 引入 `didUserSetLanguage` 偏好门控标志。如果用户从未在右上角菜单中手动设置过语言（标志为 `false`），则强制忽略本地任何 `appLanguage` 缓存，直接以系统当前的真实偏好为准。
    *   **获取真实系统语言**：改用 `UserDefaults.standard.stringArray(forKey: "AppleLanguages")` 这条系统最原始的全局首选语言链，这能 100% 绕过 App 自身本地化包带来的截断过滤，真实拿取 Mac 当前的语言环境（中文系统必定为 `"zh-Hans"` 或 `"zh-Hans-CN"`）。
2.  **剔除误导性的超时映射**：
    *   移除 `errorStateView` 里面对 `"request timed out"` 的模糊匹配判定。让所有的网络层普通超时报错正确回归到 `exclamationmark.triangle`（配对发生错误）类型，并附带明显的「重试」按钮，使用户知晓这是网络异常而非二维码过期。

### 验证

*   `xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' test` 全套单元测试通过，尤其是更新了 `LayoutConstants.connectionSheetHeight` 调整至 `740` 防止英文滚动条的 IA 契约尺寸测试。
*   Release 构建覆盖安装后，删除 App 缓存重新打开，中文系统成功首次默认加载中文界面。

### 后续原则

*   在 macOS 中进行设备检测和语言检测时，优先使用 `UserDefaults.standard.stringArray(forKey: "AppleLanguages")` 来获取真实的系统环境，防止因 App 本地化支持范围不一致而被系统沙盒强行过滤截断。
*   异常渲染不得将一般的网络状态或动作超时（Network / Request Timeout）和产品业务规则上的时间到期（Business Session Expiration）混为一谈。

## 2026-07-19 Claude 外部 turn file-relay：turn_completed 后不能 return，进程 live 时继续 watch

### 现象（owner 真机复现，iOS+Web 同时）

Mac Desktop/CLI Claude 多轮外部 turn（Mac 端发起、客户端旁观）出现：

- Web 完全收不到回复，必须靠「下一问」才把「上一答」同步出来
- iOS 历史同步也偶发掉 turn
- file-relay 日志里频繁出现 `claudeSessionFileRelay turn completed, exiting`，然后该 session 进入
  「无 relay」状态，直到下一次 `get_session_messages` 才重启

### 根因（go-bridge 侧）

`claudeSessionFileRelayLoop`（`handlers_relay.go`）在检测到 `finalAssistant` 时：

```go
if entry.finalAssistant {
    h.sendSessionEvent(sessionID, backendID, "turn_completed", ...)
    h.broadcastIdleState(sessionID, backendID)
    slog.Info("go-bridge: claudeSessionFileRelay turn completed, exiting", ...)
    return  // ← BUG
}
```

`return` 让 goroutine 退出，`relayRunning[sessionID]` 标记被清掉。Claude Desktop 在**同一 PID**
上连续多轮 turn 是常态——下一轮 user 写入 JSONL 时，**没有任何 goroutine 在 watch**，
`turn_started` 永远不会发出，直到客户端发起下一次 `get_session_messages` 触发重启 relay。
窗口期内客户端既收不到 `turn_started`，也看不到正在生成的 assistant body（Claude Desktop
只在 end_turn 才 flush JSONL），表现为「必须等下一问才能看到上一答」。

另一个相关 bug：live-idle TTL 退出条件原本是「90s 无文件增长就退出」，**不管 Claude 进程
是否仍存活**。Claude 长 thinking 阶段 transcript 静默 90s+ 是正常的，却被误判为「session 已死」。

### 修复（go-bridge）

1. **`finalAssistant` 不再 `return`**：改为 `runningObserved = false; continue`，在 Claude 进程
   仍 live 时继续 watch。下一轮 user 写入 JSONL 立刻广播 `turn_started`。
2. **live-idle TTL 仅在进程已不存活时才退出**：进程仍 live 但 transcript 静默时保持 watching，
   防止长 thinking 被误杀。原退出条件保留作为进程死亡后的清理路径。

### 测试更新

`claude_file_relay_test.go`：

- `TestClaudeFileRelayTickUsesCachedPID`：live 时保持 running，进程死后才停（断言
  `handlers.relayKindIs(sessionID, relayKindClaudeFile) == true`）。
- 其它已有用例（WarmStartUser / LiveIdleSnapshot / Interrupt 等）不依赖「turn_completed 后
  退出」，只读 2 条事件即通过，无需修改。

`go test ./go-bridge/ -run ClaudeFileRelay` 全部通过。

### 配套 Web 侧修复（详见 ../cordcode-ios/think.md 复盘 VII-IX）

Web 端也有一个叠加 bug：`applyExternalTurnHistory` 无脑把 trailing assistant 标成
`isStreaming:true`，导致安全网 `externalTurnLooksComplete` 永远 false，即使服务端已 flush 出
完整 body 也永不自动 settle。Mac 修了 file-relay 不退出 + Web 修了不强制 streaming，
两层叠加的「下一问才同步上一答」才彻底消除。

### 后续原则

- **长生命周期 watcher goroutine 不要在常规完成信号上 `return`**：完成 ≠ session 关闭。
  只要有可能产生下一轮事件（同 PID、同 socket、同 session），就应该继续 watch，把退出条件
  严格限制在「真正不可恢复」（进程死亡、socket 关闭、超长 idle + 进程不存活）。
- **跨客户端 turn 同步需要双端配合**：Mac 端负责广播边界事件，客户端负责在缺事件时也能
  从权威历史推导 settle；任何一端假设「对方一定会发事件」都会在边界条件下丢 turn。
- **file-relay 的退出语义**：「finalAssistant 写入」只是 transcript 状态变化，**不是 watcher
  生命周期事件**；这两个概念必须分开。

---

# 2026-07-21 跨仓指针：iOS 输入框执行中 / 外部 turn 收口（本仓无代码变更）

> **完整复盘在 iOS 仓** `../cordcode-ios/think.md`「2026-07-21 复盘 XI」。  
> 设计/实现/审计：`../cordcode-ios/docs/2026-07-21-ios-generation-single-authority-*.md`。

## 结论（Mac 侧只需知道）

- **根因在 iOS generation 多权威收口**（expected stale 裸 return、多 poll force-complete、load 内自 complete、Idle 下 delta activate 等），**不是**本轮 go-bridge EMIT 缺失。
- owner 真机卡住瞬间 `go-bridge.log` 常有 `codexSessionFileRelay EMIT turn_started/turn_completed` + history 增长 → 投递侧可用；排障仍用 EMIT 日志 + LAN/relay 对照。
- **本仓 2026-07-21 无业务代码 commit**；file-relay「turn_completed 后继续 watch」原则仍见上文 2026-07-19 节。
- iOS 已收敛：输入框 `isGenerating||requiresAction`、HEAL、`externalTurnLooksComplete`、load post-apply settle、Idle 不 activate；owner 三连 ✅。剩余 G1 poll 函数合一 / G6 recover 结构在 iOS 后续 PR。
# 2026-07-22 Codex rollout identity / completion boundary

Codex file-relay 与 rich history 曾把同一 rollout turn 投影成不同身份：scanner 已读取 `task_started.turn_id`，但 lifecycle payload 的 `turnId` 为空，history entry 又使用独立派生 ID。更严重的是 rich-history reader 在当前文件 EOF 就写 `TurnCompletedAt` 并把最后文本标成 final；活跃 rollout 每次增长都会被客户端观察成一次伪完成。

现在以 rollout 原生信号为唯一真值：`task_started.turn_id` 贯穿 lifecycle、delta `itemId` 与 history entry ID；EOF 保持 progress 且无完成时间，只有 `task_complete` 关闭 turn。transcript index span 同时覆盖 start/complete 记录，分页 replay 不丢这两项证据。消费端因此可以按 exact ID reducer 合并，不需要正文相似度启发式。

第五轮真机回归进一步证明，仅修 assistant 身份仍不够：Codex rollout 会在 `task_started` 后写入 `response_item(role=user)`，旧 file-relay 忽略该记录，导致 Mac 端问题只能随 history 回源迟到。现在 scanner 将它映射为 `user_message`，复用 response-item `id`，并绑定当前 source turn ID；`event_msg.user_message` 不重复解析，避免 rollout 双写造成重复。iOS 的 foreground/history reconcile 同时被约束为活跃 push turn 的 merge-only 校准者，不能凭部分 history 提前结束 turn。

---

---

# 2026-07-27 K5.2 Claude projection SoT（uuid + keep-watch + catch-up）

完整产品复盘在 iOS 仓 `../cordcode-ios/think.md`「复盘 XVIII」。本仓只记 go-bridge 契约。

## 修了什么

1. **`b787975`** — Claude transcript identity  
   - 结构加顶层 `uuid`  
   - turn/item：`message.id` 优先，否则 `uuid`  
   - live file-relay growth 复用 `claudeEntryToProjectionEvents`（带 identity 的 user_message / text_delta / turn_completed）  
   - 禁止空 `turnId` 的 turn_started / 无 itemId 的 text_delta（reducer 会 skip）

2. **`a39133e`** — multi-session live + reopen  
   - process 未 live：**继续 watch** transcript，不立即 exit；PID 晚到可 late-bind  
   - 未 live 时不根据历史 tail 武装 running  
   - `ProjectionKernel.committedSourceCursor`：Ready 后若 `source.Cursor` 更大，强制 catch-up hydrate，而不是 `AlreadyReady`

## 为何需要

Owner K5.2：A 同步正常，B 打开后 Mac 发消息 3 无 live，切回仍无。日志：B relay `process not live ... exiting`；reopen `headRev` 钉死旧 rev。根因在本仓 SoT，不在 iOS UI。

## 测试

`go test ./go-bridge -run 'TestClaudeFileRelay|TestClaudeEntryToProjectionEvents|TestProjectionCatchUpWhenSourceAdvancesPastReady'`

## 原则

- Claude user 身份以真实 JSONL 为准（常无 message.id）。  
- file-relay 生命周期 ≠ process 此刻可发现。  
- Ready projection 必须能对 source 增长 catch-up。  
- 不把这类洞交给 iOS referee。



## OpenCode K5.3 (2026-07-28)

OpenCode session_sync_v2 via rich-history hydrate + live TurnID/ItemID.

Follow-on SoT fixes same day (owner matrix green):
1. `handleOpenCodeRPC` allowlist `get_session_projection` (cold open).
2. `DeltaBatcher` preserve `itemId` on text/reasoning flush (live content).
3. SSE `noteUserPrompt` for bare user + part.delta (user bubbles).
4. Multi-step: do **not** emit EventResult on intermediate assistant `time.completed` / `step_finish`; only `session.status`/`session.updated` idle closes the turn (composer no longer flips idle on tools).
5. iOS side (sibling repo): v2 allows todos control-plane; discards stale completed plans on new generation.

Tests: `go test ./agent/opencode -run 'TestSSESubscriber_MultiStep|CompletionIsIdempotent|ToolTodoAndIdle'`.


# 2026-07-29 remote-web 首开 Claude projection 报 project dir not found

## 现象与证据

web 第一次打开 Claude session，`get_session_projection` 立即
`projection.hydrate_failed: claudecode: project dir not found`；再打开任意 session 后恢复。
同一时刻 `claudeSessionFileRelay` 能找到正确 JSONL，说明 session 与 transcript 都存在，失败只在
cold hydrate source inspection。

## 根因

Claude `prepareProjectionHydrateSource` 通过 agent `TranscriptPath` 查文件；该实现从共享
`agent.workDir` 推导 `~/.claude/projects/<key>`。首开前 workDir 还是 runtime 启动目录，尚未被其他
带 directory 的 RPC 更新。后续“自愈”只是别的请求偶然改了共享状态。

直接在 read-only projection handler 上 `SetWorkDir` 也不安全：多设备可能同时冷开不同项目，
共享 agent workDir 会产生跨 session 竞态。

## 修复

- `get_session_projection` 将 request directory 传入 hydrate source resolver；
- Claude 用已有 `findClaudeSessionFile(sessionID, directory)` 解析真实 transcript；
- 不修改 agent workDir；Codex/OpenCode source 路径不变；
- 测试以 stale agent workDir + 正确 session directory 冷拉，证明投影有内容且 workDir 未变化，
  `-count=10` 稳定通过。

## 验证边界

- 新增定向测试及相关 projection tests 通过；
- Release build 通过；
- 仓库全量 Go 仍有两个独立既有失败：
  `TestScanCodexTranscriptRelayEventsToolsAndTokens`（Codex itemId）、
  `TestRegressionR1_LeaseAutoDowngrade`（lease expiry）；单独重跑仍失败，本轮不顺手改。

## 2026-08-07：Codex archived session hydrate 失败

`findSessionFile` 只扫 `~/.codex/sessions/`，Desktop archive 物理移动到
`archived_sessions/` 后 cold hydrate 报 session file not found。
修复：active 优先，archived fallback（`7baafd8`）。

## 2026-08-12：Claude Desktop archive/delete 不消失（iOS 列表残留）

### 现象
owner 真机矩阵：Claude Code 模式下 Mac 端改名/新建/发消息都能同步，但 Mac 端
Claude Desktop archive/delete 后，iOS 列表仍显示这些 session，重启 iOS App 也一样。

### 根因（MacBridge 单侧，compatibility catalog 看不到 Desktop 私有状态）
`claude_session_catalog.go` 只扫 `~/.claude/projects/**/*.jsonl`。Claude Desktop 3P
（`claude-desktop-3p`）archive/delete **不改 JSONL 文件**：
- archive：只把 `~/Library/Application Support/Claude-3p/claude-code-sessions/<acct>/<org>/local_*.json`
  里的 `isArchived` 置 true；transcript 文件原样保留。
- delete：只删自己的 `local_*.json` 并在同目录写 `deleted_<uuid>` tombstone；
  transcript 文件也原样保留。

日志证据（`~/Library/Logs/Claude-3p/main.log`，2026-08-12 00:12–00:17）：
`LocalSessions.delete/archive` 与实际 `local_*.json` + `deleted_*` 一一对应；
对应的 Chat JSONL 仍存在。

### 修复
新增 `claude_desktop_state.go`：读 Desktop 自己的纯数据文件（不碰 app.asar /
私有 Electron API），把
- `local_*.json isArchived=true` → 给对应 CLI transcript 打 `ArchivedAt`（文件 mtime），
  catalog 照旧输出 `archivedAtMillis`，iOS/remote-web 既有过滤逻辑隐藏；
- `deleted_*` tombstone → 直接从 catalog 排除对应 JSONL；
- Desktop 文件 mtime 并入 `claudeSessionFingerprint`，archive/unarchive/delete 都能
  让缓存失效并触发 `sessions_changed`。

兼容两个 App Support 根：`Claude-3p/` 与 `Claude/`，并同时扫
`claude-code-sessions/` 与 `local-agent-mode-sessions/`。解析失败时 fail-safe：
不伪造结果，维持旧 JSONL 列表行为。

### 验证
- 定向单测：`TestClaudeSessionCatalogHonorsDesktopArchiveAndDelete`（archive 标记、
  unarchive 失效、tombstone 排除与恢复）。
- 本机真实状态校验（临时测试，未提交）：`91330136-…`（已 delete tombstone）不再列出；
  `9e5bc559-…`（已 archive）带 `archivedAtMillis`。
- 全量 `go test ./go-bridge/...` PASS；定向 race PASS。
- Release 重建 + 覆盖安装 `/Applications/CordCodeLink.app`。

### 诚实边界
Desktop 存储格式是私有且可能随版本变化；这是兼容 catalog 的 best-effort，不是
Claude 原生 catalog 同源。等 Claude 上游暴露稳定 catalog 接口后再迁移，不要把它
当成 exact parity。

### owner 复测（2026-08-12）
真机复测反馈「基本符合预期」：Desktop archive/delete 后 iOS 列表能及时消失，
unarchive 也能恢复；本轮修复闭环。

### owner 复测（2026-08-12）：SSV2 运行中 session 冷开超时修复
真机矩阵 1-5 全部「基本符合预期 ✅」：运行中 Claude 长任务冷开 15s 内出内容
（running partial）、turn 完成后自动补全为 completed、短任务/idle session 冷开
正常、crashed 进程冷开收口为 aborted/诚实失败。三阶段修复闭环：§3.1
`sourceIsLive` 提交门槛（admission 采样）+ §3.2 Claude relay 提前到 hydrate 前 +
§3.3 进程死亡合成 `turn_aborted`，配套 iOS §3.4 late-retryable 清错并重启
pull loop。详见 `docs/2026-08-12-ssv2-running-session-projection-timeout-fix-plan.md`。

### 2026-08-12 追加：Grok 运行中 session 冷开重复投递未完成 turn 的 prompt（已修 ✅）

现象：grok build 正在执行时 iOS 冷开，显示「问题A，回复A(前半截)，问题A，回复A(从前半截
处开始)」。真实 session `019ff1b3-…` + go-bridge.log 证实：冷 hydrate 基线 revs 1–311 已含
chat_history 的 user 行(问题A) + assistant 行(前半截回复)；tailer attach 补扫
`latestPendingUserMessage` 又把 updates.jsonl 同一条 `user_message_chunk` 投递 → relay 以
新 turnId=3a5c0825… 合成 turn_started 并补发 user_message(rev 312) → iOS 看到问题/回复两遍。

根因：grok 在 turn 执行中就把 user 行追加进 chat_history.jsonl，而 attach 补扫逻辑只按
「最后一个 turn_completed 之后」判断，未检查 chat_history 是否已含该 prompt。

修复：`historyContainsUserPrompt`（归一化 = unwrap `<user_query>` + trim + 跳过
synthetic/bootstrap，与 readRichSessionHistory 用户行一致；真实样本逐字节一致 343 字符），
chat_history 已含该 prompt 时抑制 attach 补扫。竞态窗口（prompt 已发、chat_history 未落盘）
仍补扫，不破坏原设计。iOS 零改动。测试：新增 2 个（history 已含 → 不投递；history 未含 →
仍投递），原有 catch-up 测试保持通过；go test ./agent/grokbuild/ + ./go-bridge/... 全绿。
Release 构建 68e01ae 已部署 /Applications。owner 真机复测（2026-08-12）：「测试结果基本上
没问题 ✅」。

## 2026-08-15：grokbuild 握手窗口事件丢弃语义（记录在案，非缺陷）

2026-08-14 修复 session/load replay 死锁（commit 862e4f8）后明确的既有语义：握手窗口
（进程启动 → session/new|load 完成 + drain 结束）内到达的**一切** ACP 通知都按 replayed
历史状态处理，可能被丢弃——包括 `session/request_permission`。这与修复前 post-handshake
drain 的行为一致（drain 注释明确丢弃 replayed state "including any prior error"），无回归。
真实 live 权限请求属于已开始的 turn，在 StartSession 返回之后到达，不在此窗口内。
落点：`agent/grokbuild/session.go` emit() 握手丢弃分支的 scope note。

同日另一记录：`turn_error` wire producer 激活（opencode 空转 turn + idle 验证的冷 hydrate
seal，commits e00b389/8eabd6e），canonical `docs/protocol/bridge-v1.md` 事件清单补入
`turn_error`/`turn_aborted` 及说明，iOS mirror 已同步。

## 2026-08-16 臆想教训：dsh web「未分组」结论先于取证

- 事件：真机复测中 Mac web 显示手机会话于「未分组」，agent 未读 web 源码即断言「Chat 未注册 workspace，用户加上即可」——事实相反（Chat 早已注册；分组机制是 workspace.json 显式 sessionIds 名单而非路径匹配），被 owner 以双端截图纠正。
- 教训：对**另一个系统的行为**下结论前必须读它的源码/数据；「听起来合理的机制解释」≠ 证据。跨系统（iOS/dsh web/harness）行为差异排查同样适用本仓排障纪律：先取证再定性，不确定就说不确定。
- 顺带固化的事实：dsh web workspace 分组=显式名单（entity.ts attachSession 仅 web 自身 create/fork 调用，校验头行 cwd=workspace 路径）；workspace.json 受 dsh-storage-domain 两写恢复协议管理，外部进程不可盲写。
- 2026-08-17 复发：dsh-web `ListSessions` 一期把 `cwd` 直接写成 `directory`，iOS 按路径归组 → 同一 Chat cwd 但不在 Chat `sessionIds` 里的会话（太阳能/月球）进了 iOS Chat，官方在「未分组」；`archivedSessionIds`（讲个光头笑话等）官方隐藏、iOS 仍显示。修复=ListSessions 读 `workspace.list` 名单写 directory / ArchivedAt，不再用 cwd 冒充分组。
- 2026-08-17 同日：owner 在 iOS Chat 目录新建「加州笑话」仍进未分组。官方 `session.create` 源码（apiproxy create）**只有 payload 带 workspaceId 才 attachSession**；只传 cwd 只写会话头目录、不进名单。设计 §3.5「cwd 命中已注册 workspace 自动归组」是错的。修复=create 前 workspace.list 按路径反查 id，命中则只传 workspaceId。

## 2026-08-16 路线教训：接入外部工具先枚举全部暴露面，「SDK 无 X」≠「无法 X」

- 事件：dsh 接入第一版把「SDK 协议面没有 list/resume」铁板钉钉为「无法读取 Mac 端会话」，为此建造 live-only 投影 admission、iOS 隐藏列表等整套补偿架构。事实：会话数据一直在 `~/.dsh/sessions`（claudecode 文件读取先例就在本仓）；dsh web 的 api-proxy HTTP 契约（session.list/history/prompt 含 queue|steer/续聊/mux+host WS 事件流/workspace/approvals，schema 化）才是最全面、且官方前端日常在用的面。owner 两轮点破（store 可读 → web API 全量）。
- 教训：接入用户自有工具时，先枚举它**暴露的全部面**（协议 / 文件 / 常驻服务+官方前端所用的 API），锚定用户实际使用的那条最全面通道，而不是我们恰好要对接的那条最窄通道；否定性断言必须标注「在哪几个层面找过」。三轮真机故障（存储编码、模型白名单、归组臆想）与路线错误同源=MacBridge 重新推导官方已保证的事实。
- 处置：SDK stdio 路线暂停、成果保留（收口全文见 `docs/2026-08-16-dsh-session-store-bridge-design完成情况.md` 收口节）；后续 dsh-web backend=官方 API 转发器+bridge-v1 翻译器，与现有 deepSeek backend 并行，先完成四项核实（prompt 续聊语义/mux 帧契约/托管启动形态/API 版本承诺）再出设计。

## 2026-08-17 官方 StatsLine 不在 iOS 时间线重算

- 官方输入框底下那一行来自整本日志投影：`sessionStats`（轮/步/llmMs/toolMs/ttft/decode）+ `tokenUsage`（uncached/cacheRead/cacheWrite/output）。缓存命中 = cacheRead / (uncached+cacheRead+cacheWrite)。窗口内节点 fold 只是没投影时的 fallback，分页/压缩会改窗口，不能当账单。
- iOS 放在已有 ⭕ 表的「本会话」，不另做 composer footer。数字只转发官方投影，不从手机消息列表加总。

## 2026-08-20 OpenCode Web：HTTP 纯度不等于官方 Web 语义一致

- owner 在实施/真机测试中发现，`opencode-web` 虽然已经只走官方 HTTP/SSE，创建 session 后首条
  消息、模型/agent 参数、事件终态等仍反复出错。复审确认根因在原设计的方法：允许从 legacy
  `agent/opencode` 复制 prompt/SSE/history 语义，再以 endpoint 可达和同源 fake-server 测试证明
  “对齐官方 Web”；这是一条循环证据链。
- 2026-08-20 对安装版 1.18.18、同版本官方源码和隔离 serve 三方核验后，已确认的系统缺口包括：
  官方 prompt 携带 messageID/agent/model/variant/多类 parts，现实现只发 text+model；官方模型选择
  有五级链，现实现只覆盖部分；官方 session list 使用 roots/limit，现实现按 `/project` 聚合且不带
  limit；真实 SSE 同时出现 direct payload 与嵌套 `sync`，现解析器忽略后者；question/todo 未接；
  v2 没有当前活体样本。todo 活体项还与生成 SDK 的 `id` 声明发生漂移。
- 原 `docs/2026-08-18-opencode-web-backend-design.md` 及完成情况已降为历史记录，禁止继续据此施工。
  当前证据和后续阶段门分别见 `docs/2026-08-20-opencode-web-source-parity-audit.md`、
  `docs/2026-08-20-opencode-web-source-first-convergence-plan.md`。owner 当前暂停产品代码实施。
- 长期纪律已写入 `CLAUDE.md`：官方 UI/source → 目标版本真实样本 → bridge 映射 → 实现/测试；
  legacy 只能当反例和接线索引，endpoint 2xx、SDK 类型或从设计手写的 fixture 都不能单独证明 parity。

## 2026-08-21 OpenCode Web：todo dock / 任务同步修了三层才绿（owner 真机）

owner 现象叠了三轮才完整：iPhone 发消息后卡「正在生成」或正文只到一半；多步任务 Mac Desktop
正常跑完 todo，iPhone 全程没有任务卡。数据源**从来不是文件反推**——opencode-web 的 todo 只有
官方 `GET /session/{id}/todo` 和 SSE `todo.updated`（形状 A8：`{content,status,priority}`
全量替换，无 item id）。每修一层，下一层才露出。不要先去扫 JSONL / transcript，也不要先当
server SSE bug（#28729）或传输 keepalive。

### 层 1 — live reasoning 被当成终态错误，拆掉 relayEvents

- 推理模型每轮都有 populated reasoning。live 载体按 E2「不翻译」走 `emit(EventError)`。
- `go-bridge/handlers_relay.go` 对非 claude 的任意 `EventError` 合成 wire `turn_error` 并
  `return`，relayEvents 当场拆除。正文半截、会话卡执行中；Mac Desktop 不受影响。
- 修：`events.go` 改为 `skipLiveReasoning`（Debug 跳过，不发 EventError、不上 Thinking）。
  E2 语义仍是「live 不翻译」；历史 hydrate（directive-014）继续显示思考块。
- 测试：`TestReasoningSkippedUntranslatedAndNonFatal`、`TestReasoningModelTurnStreamsTextAndCompletes`。
- **不要放松**「非 claude EventError = 终态」本身——那是进程崩溃收口。上游不得再把可折叠的
  非致命问题丢进这条路径。

### 层 2 — 空闲后再发：全局 SSE 被二次 Close 拆掉（整条链路静默）

- 日志指纹：`send_message` → `session not found` → `SSE subscriber connected` →
  `replacing stale agent relay after session respawn` → `relayEvents started` →
  **零 forwarding / 零 passive**。4096 日志证明 turn 在 server 侧跑完。当天 13:39 那条
  首个 SSE 上是流式正常的。
- `serverSession.Close()` 无 closeOnce、且不关 `events` 通道。idle cleanup 第一次 Close
  已把 global SSE refs 减到 0；`relayEvents` 因通道永不关闭而僵尸驻留，`agentRelaySess`
  仍指向旧 session。下次 send 的 `startRelayIfNotRunning` 对**同一旧对象再 Close 一次**——
  此时 `globalSub` 已经是新流，第二次 `releaseGlobalSubscriber` 把 refs 1→0，刚 dial 的
  `/global/event` 当场被拆掉。14:58 重连后立刻出现的 2 条 desktop `passive event` 正好卡在
  connect 与 stale.Close 之间（~112ms），之后全沉默，是同一机制。
- 旁证：`relayEvents exited` 在 idle 之后缺失。
- 修：`Close` 幂等（closeOnce）+ unregister 之后 `close(s.events)` 让僵尸 relay 退出；
  最后 holder 释放打 `opencode-web SSE: last holder released, tearing stream`。
- 测试：`TestStaleSessionDoubleCloseDoesNotTearRespawnedSSE`（先红后绿）。

### 层 3 — iOS syncV2 envelope gap 把门把已到达的 todos_updated 整轮丢掉

Mac 侧投递没问题之后，iPhone 仍可能没有 dock。syncV2 下正文/工具走 projection_patch（另一套
syncRev），raw 子集 `perSessionSeq` 必然跳号；`handleRoutedLiveEvent` 入口的
`timelineStore.acceptance` 把 `todos_updated` 判 gap 整轮丢弃。todo deck 是控制面全量替换
快照，不是投影真相源，不该过这扇门。修在 iOS：
`ChatViewModel+CodexStreaming.swift` 入口前置拦截 `.todosUpdated` 直接 `handleTodoUpdate`。
测试 `testRoutedTodosUpdatedBypassesEnvelopeGapGate`。细节见相邻 iOS 仓 `think.md`
「2026-08-21 OpenCode Web：todos_updated 被 envelope gap 门整轮丢掉」。
这和 2026-07 的 `handleCodexLiveEvent` v2 早退是**另一扇门**，不要合并成同一个 bug。

### 排查顺序（再出现「有任务无 dock / 正在生成」）

1. go-bridge：有没有 `relayEvents forwarding` / `passive event` / `todos_updated`。
   零 forwarding + `replacing stale agent relay` → 层 2。有 forwarding 但 iPhone 无 dock → 层 3。
   有 text 半截 + `turn_error` 且错误像 reasoning unsupported → 层 1。
2. 4096 server 日志（只读）确认 turn 是否在跑。Mac Desktop 正常 ≠ bridge 收到了 SSE。
3. iOS「正在生成」是本地乐观态，不能当事件到达证据。

## 2026-08-21 OpenCode Web：iPhone 目录数对不上 Desktop（17 vs 6）——先看官方源码

owner 验收（2026-08-21 晚）：修完后 iOS OpenCode Web 会话列表与 Mac Desktop **基本上一样**。

### 教训（下次 opencode-web 排障第一条）

**先打开 `../opencode/packages/app` 里 Desktop/web 首页的真实调用链，再写假设。**
不要从本仓旧 adapter、GET 探活、iOS prefix、缓存冲刷往下猜。本轮走了弯路：先当
iOS 只预拉 6 个工程、再当 GET /project 就是首页。owner 当场指出「昨天刚修过同类问题，
是看官方 web 才找到正确调用」——对。昨天是 `session.list({directory, roots:true, limit})`；
今天是「对哪些 directory 去调」。同一纪律。

### 走弯路（不要再走）

1. 把 `GET /project`（服务端注册表，活体 31 行、deleted-still-registered）当成 Desktop
   首页侧栏。关掉 tab 不会从注册表消失，所以 iPhone 出现 17 个还在磁盘上的目录。
2. 在 iOS 上修 `prefix(6)` / 添加目录冲刷。那是次要：即便每个注册表目录都打对了
   `session.list`，目录集合仍然是错的。
3. 活体乱探 `GET /project/current`（返回 `worktree="/"`）当「当前 6 个」。那是当前
   instance 的 current project，不是打开集合。

### 官方真相（1.18.18 `packages/app`）

- **首页目录集合**：`home-controller.ts` `project.list` =
  `createServerProjects`（`server.tsx`）。数据在 Desktop persist
  `~/Library/Application Support/ai.opencode.desktop/opencode.global.dat`
  的 `server.projects["http://127.0.0.1:4096"]`。`closeHomeProject` 只改这份列表 +
  recentlyClosed，**没有**对应的 HTTP DELETE。活体该 key 7 行（含今天的
  `cordcode-macbridge`）；owner 目视约 6 个，同一集合。
- **每个打开目录的会话**：`global-sync/session-load.ts`
  `loadRootSessionsV1` → `client.session.list({ directory, roots: true, limit })`。
  不带 directory 的 `GET /session` 是陈旧全局切片（2026-08-19 已钉：当天新会话缺席）。

### 最终修

Mac `desktop_home_projects.go`：list_projects / 聚合 catalog 只读 Desktop 该 serve URL
的打开集合（只读 persist，不写）。没有 persist 才回落 `GET /project`。运行日志：
`home project list source=desktop-persist:... count=7 url=http://127.0.0.1:4096`。
每个打开目录仍走官方 `session.list({directory, roots:true, limit})`。
测试：`TestParseDesktopOpenedWorktreesMatchesServerURL`、
`TestProjectWorktreeDirsPrefersDesktopOpenSetOverRegistry`。
iOS：打开集合里每个工程都拉 bucket（不再名字序 prefix(6)）；添加目录后立刻拉该目录。

没有「当前打开集合」HTTP。对齐 Desktop 首页就要读官方 UI 同一份 persist，不能发明
GET /project 过滤规则。
