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

**逐面对表结论**：一期 12 个已接面与官方用法一致（含 connected 过滤 prompt-model-selection.ts、占用公式 session-context-metrics.ts 逐字段、SSE 无寿命时限）。本轮对齐两处：占用改**严格五项和**（删自加的 total 回落）；发送补官方**默认模型回退链**——实际实现两级：pending 选择→会话采纳模型→首个 connected provider 的 default??首模型（回退候选全部过 catalog 门控，connected 空目录仍诚实报错零 POST）。**与官方五级链的已知缺口**（监工核验 2026-08-19 指出）：官方的「配置默认/最近使用」两级在 iOS 场景无对应物（iOS 自己的模型缓存等价于传入级）；「agent 指定」级有真实对应物但**输入链路不存在**——iOS `sendMessage(agent:)` 参数在 Bridge 适配层被丢弃、wire `send_message` 无 agent 字段、Mac 侧无 hook。补全该级 = 跨仓协议扩展（wire 加字段 + iOS 传值 + Mac 读 /agent 的 model），非 Mac 侧小改，待 owner 裁决；现状：iOS 选 agent 不选模型时发送用首个 connected 默认模型。官方有而一期未接（按设计 ⛔/2️⃣ 维持）：revert/unrevert、summarize(compact)、fork、children、diff、todo、init、command、shell——列为二期候选清单。tui/pty/lsp/mcp 等 Mac 客户端不适用面不接。

## 四·补二、provider 报错透传（owner 矩阵行 2 实测反馈 2026-08-19，commit `e404af7`）

**现象**：iOS 选了试用期已过的 GLM-5.2-Highspeed 发送，Mac 网页显示「当前订阅套餐暂未开放GLM-5.2-Highspeed权限」，iOS 无任何显示。

**活体取证**（errlab 沙盒：mock provider 返回智谱同款 403，抓完整 SSE 帧序列）：1.18 的失败链路为 `busy → session.status{type:"retry",attempt,message}×N（指数退避 3/8/16/34/60s…）→ session.error{error:{name:"APIError",data:{message,statusCode,isRetryable}}} → session.status idle + session.idle → assistant message.updated 带 info.error`。provider 报错文案**只**由 retry 帧、session.error 帧、assistant info.error 三处承载。

**根因**：订阅器事件表（抄自旧包）不认识 `retry` 状态、`session.error`/`session.idle` 事件类型、`info.error` 字段——三面全丢，idle 后只能发通用零输出文案。

**修复**：三面全部接住并记入 per-session 错误记录；零输出终端改用服务端原文（无记录才回落通用文案）；新 turn 起臂时清记录；冷历史的失败 assistant 消息把 info.error 文案显为 content（与网页一致）。测试：抓包帧逐字节回放（驱动式 + 真实 SSE 传输两套）断言终端携带「当前订阅套餐暂未开放…」原文；陈旧错误不污染下一轮健康 turn。

## 四·补三、列表目录限定 + retry 瞬态 + 终态文案 3× 去重（owner 真机反馈 2026-08-19 午后，commit `9315b62`）

**现象 A（列表）**：Mac 端 opencode desktop（已确认：`@opencode-ai/desktop` 为 **Electron** 应用，`electron-vite`/`electron-builder`——本质是 web 程序，owner 判断正确）连 4096，在 cordcode-ios 目录新建会话并发送；iOS opencode-web 列表**不可见该会话，重启也不可见**；切旧 opencode 模式立即可见。且 opencode-web 列表把所有历史目录都列出（含 Mac 已关闭/删除的），旧模式列表反而更像桌面端。

**活体根因（只读探针 owner serve 钉死）**：1.18 `GET /session` 按 `x-opencode-directory` 头**按目录返回**（带头 cordcode-ios → 14 条、红楼梦会话最新第一条）；**不带头的全局响应是陈旧百条切片**（最新条目停在 7 月 6 日，当天所有会话缺席）。旧实现走桥接 generic 路径：粘滞 workdir 头 + 全局列表 + 事后按目录过滤——其它目录的新会话**结构性不可见**；目录分组来自这份陈旧列表，幽灵目录全数漏出。旧 opencode 模式可用是因为它走 `ocProxy listSessions({Directory, Roots})`（CLI 限定目录 + roots 感知 + v2 游标）。

**修复（官方对齐：目录发现/分组以 serve 自己的工程注册表 `GET /project` 为准——与官方桌面端目录选择器同源；HTTP-only，不碰 CLI/storage）**：
- `ListSessions` 改为 /project 注册表逐目录**限定拉取**的合并视图（最新优先；ghost 与 `/` 剔除；15s TTL + SSE `session.created/deleted` 信号即时失效——桌面端新建会话即刻进指纹 → sessions_changed → iOS 刷新可见）；
- 新增 `core.DirectorySessionLister`：带 directory 的 list_sessions 走限定拉取；桥接叠加幽灵目录可见性过滤（与其它 catalog 同规则）；非该能力 backend（dsh-web/deepseek）契约不变；
- `ListProjectSuggestions` 去重 + 剔除 global/ghost worktree。

**现象 B（重试期无提示）**：重试退避（3/8/16/34/60s…）期间官方网页渲染 `session.status{type:"retry"}` 红行，iOS 全程无提示——owner 两次报障后本轮实现：新增 wire 事件 `session_retry_status{attempt,message,next}`（`core.EventRetryStatus`；非 durable milestone、不在 SSV2 raw deny-list、投影 kernel 忽略——control-plane 专用载体，旧客户端安全忽略）；iOS `mapBridgeEvent` → `.sessionRetryStatus` → runtime status 相位 `.retrying` → 状态栏「自动重试中（第N次）…」+ provider 原文（message-web 契约扩 `retrying` 态 + `retryAttempt` 字段，bundle 已重编）。

**现象 C（文案 3×）**：12.33.26 截图套餐报错显示 3 次。活体日志钉死：同一 SSE 失败帧被**双订阅线**（StartSession 专用订阅 + 全局 passive 订阅）各自映射一遍，且 `emitResultOnce` 消费 terminal 记录后，trailing `message.updated(info.error)` 又当「首次」重发——text_delta flush 3 次（rev 59/61/63）。修复：文案发射闸门提升到 **Agent 级 claim**（跨订阅线只发一次；新 turn 重臂）；测试双订阅线逐帧交错驱动钉死。

**同轮确认的既有事实链（不改，供 owner 裁决）**：失败 turn 的 raw `turn_error` 已在 deny-list 之外（SSV2 设备可收）且投影补丁含 turn settle + execution idle——服务端链路完整；12:49 复测 iOS 仍卡执行中，指向**设备上的 App 构建早于 493310f**（turn_error 解码修复需随新构建安装；「重启 App」不等于「重装新构建」）。

## 四·补四、网络面全量盘点（owner 质询「是否有假 HTTP / 真造轮子」2026-08-19）

`agent/opencode-web` 全部网络调用点（源码行号级）——**16 个面全部是官方端点，无一本地伪造替代**：

| 面 | 端点 | 源码 | 活体/测试背书 |
|---|---|---|---|
| 探针 | GET /global/health（+v2 /api/health 兜底） | probe.go:52-120 | 活体（双代互斥判据） |
| SSE | GET /global/event | events.go:123 | 活体（失败链路帧序钉死） |
| 会话状态 | GET /session/status | events.go:209, activity.go:35 | 活体（掉线治愈/探测） |
| 列表 | GET /session（按目录头） | sessions.go:151 | 活体（本轮钉死目录作用域语义） |
| 单会话 | GET /session/:id | sessions.go:208 | 活体 |
| 新建 | POST /session | session.go:218 | 活体（id/model 键形修正过） |
| 发送 | POST /session/:id/prompt_async（+同步 prompt 兜底） | session.go:185/168 | 活体（modelID 键形修正过） |
| v2 切模型 | POST /session/:id/model | session.go:239 | 沙盒 E2E |
| 取消 | POST /session/:id/abort\|interrupt | session.go:257 | 单测+owner 停止链路 |
| 历史/占用 | GET /session/:id/message | history.go:30/253 | 活体（占用公式逐字段对齐） |
| 模型目录 | GET /provider | models.go:96 | 活体（三段信封/connected 钉死） |
| agents | GET /agent | agents.go:20 | 单测 |
| 工程 | GET /project | projects.go:43 | 活体（本轮成为列表骨架） |
| 权限 | POST /session/:id/permission/:rid/reply | permissions.go:105-120 | 单测（owner 矩阵行 6 待验） |

**本地实现的部分都是官方 TS 客户端同样在本地做的**（占用五项和公式、connected 过滤、错误文案以正文承载、目录头传递），以及桥接自身机制（指纹发现、目录缓存、双订阅线映射）——不替代任何 serve 端点。

**官方四流程对照（owner 点名，源码级）**：
- 模型列表：官方 `legacy.provider.list()`（bootstrap.ts:233）↔ 本实现 GET /provider + connected 过滤（models.go:96，prompt-model-selection 同构）；
- session 列表：官方 v1 SDK `session.list({directory,…,order:"desc"})` = **GET /session?directory= 查询参数**（sdk/js gen SessionListData.query.directory）↔ 本实现对齐（commit 872e688；此前用等效的 x-opencode-directory 头，活体两种形状 14 条最新优先互证）；
- 创建 session：官方 POST /session（SDK SessionCreateData）↔ session.go:218 同端点同键形（id/model，活体修正过）；
- 同步新 session：官方 SSE `event.listen` → event-reducer `session.created` 插入 store（server-sync.tsx:531 / event-reducer.ts:131）↔ 本实现同一 SSE 流 session.created → 目录信号 → 指纹重扫 → sessions_changed 推 iOS（桥接层等价翻译，iOS 为客户端）。

**owner 质疑成立的点与根因**：`GET /project` 最初就接了（首版 projects.go），但只接到 list_projects 建议 RPC；**会话列表骨架**按设计稿 §4 做成「无头 GET /session 全量 + 指纹」——设计稿本身把无头响应当全量真值（错误假设，活体证明它是陈旧百条切片）。补做的官方源码审计穷举了 SDK API 面并对表 models/占用/错误三面，但**没有审「官方 web 如何拉会话列表」这条路径**——审计记录把 /session list 标为「已接且一致」，核对的是端点存在性而非语义。该错误假设单点炸出整串症状（新会话不可见→指纹不变→无刷新→目录分组污染），与 owner「测试出一堆问题也不奇怪」的推断一致。教训入档：**对表必须到语义级（官方客户端如何调用+响应形状活体验证），端点存在性不构成一致性证明**。

## 四·补五、官方调用形状契约（owner 质询「如何证明其他地方没犯相同错误」，commit `8fe67b2`）

**机制化证明**：`official_shapes_test.go` 把官方 v1 SDK（sdk/js gen types.gen.ts）的逐端点请求形状——路径、查询参数、body 键集——钉进测试常跑；任何面今后形状漂移（重蹈 /session 列表覆辙）测试直接红。本轮审计结论与修正：

- **directory 全面对齐官方查询参数**：SDK 全部 12 个 Data 类型均为 `query{directory?}`；此前只列表带查询参数、其余面只发请求头（等效但非官方形状）。doRequest 现统一追加 `?directory=`（头保留作冗余）。
- **create 对齐 SessionCreateData**：官方 body 只可选 {parentID,title}——directory 走查询参数、**model 不属于 create**（首条 prompt_async 才绑定会话模型）。旧实现把 directory+model 塞 body（serve 容忍多余键，非官方形状）。**裸沙盒（真 1.18.18、隔离 XDG、4296）行为验证**：`POST /session?directory=…` body `{}` → 200 落对目录、`GET /session?directory=` 列表可见、DELETE 清理。
- **权限枚举 SDK 证明**：`once|always|reject`（PostSessionIdPermissionsPermissionIdData）——旧实现的 allow/deny 回退字面量是永不接受的死信，已删；路径 `/session/{id}/permissions/{rid}` 与 body `{"response":…}` 本就与官方一致（当初探测式实现歪打正着，现由 SDK 证明钉死）。
- **v1/v2 同步 prompt 路径复核**：v1=`/session/{id}/message`、v2=`/api/session/{id}/prompt`（client gen 证实）——本实现 v2 分支路径正确，v1 只用 prompt_async（官方路径），无偏差。
- **待核面（如实）**：v2 分支整体（v2 权限模型为 request/saved 注册表制、v2 prompt body `{"prompt":…}` 形状）未对 v2 SDK 逐一复核——owner 现网是 1.18，v2 留待接入现网时补核；/global/health 探针为桥接自创（官方客户端无此用法，用于双代探测，非对官方面的翻译，无对齐义务）。

**证明结构三层**：①形状层——official_shapes_test 契约（SDK 类型 → 断言）；②行为层——裸沙盒真二进制验证（create/list/delete）+ 既有 sandbox E2E（探测/目录/发送/失败收口）；③现网层——owner 矩阵行 1–6 真机验收（占用/审批/旁观等）。三层各管一段，缺层即回到「端点存在性≠语义一致性」的老坑。

## 四·补六、权限框对齐官方（owner 对比反馈 2026-08-19 18:15，Mac 官方框信息量更高 + 「始终允许」按钮缺失）

**owner 现场对比**：同一 session 同一请求，Mac 官方桌面权限框＝标题「需要权限」+ 类别「访问项目目录之外的文件」+ 路径 `/Users/jacklee/Projects/Chat/*` + 三按钮「拒绝 / 始终允许 / 允许一次」；iOS opencode-web 权限卡信息量低且只有两按钮。

**根因（又一起「端点存在性≠载荷形状」）**：`handlePermissionAsked` 读 `tool`（字符串）、`title`、`description`——**真实帧里全都不存在**。这是与 §补五 /session 列表同型的错误：路径/事件名对了，载荷形状是臆造的。

**官方形状钉死（双背书）**：
- **源码层**：main 仓库 `PermissionV1.Request`＝`{id, sessionID, permission, patterns[], metadata{}, always[], tool{messageID,callID}}`（tool 是对象）；桌面渲染 `session-permission-dock.tsx`＝i18n `settings.permissions.tool.{kind}.description` 类别行 + patterns 逐行等宽 + 无条件三按钮（once/always/reject → 允许一次/始终允许/拒绝）；server 端 always 回复把 `always[]` patterns 存为会话期 allow 规则并自动放行同会话覆盖的 pending。
- **活体层（permlab，真 1.18.18 二进制 + mock 模型）**：mock 流式返回 read 目录外文件的 tool call → serve 真实 ask：`/global/event` 帧与 `GET /permission` 均为上述形状（kind=`external_directory`、metadata 带 filepath/parentDir）；`POST /session/{id}/permissions/{rid}` body `{"response":"always"}` → 200 + `permission.replied{reply:"always"}` + pending 清空。**v1 SDK gen types 里的 `{type,pattern,title}` 形状是旧版文档，1.18.18 实际不讲它**——以活体为准。

**实施（三层贯穿）**：
- core.Event 加 `PermissionKind/PermissionPatterns`；`handlePermissionAsked` 按活体帧重写；`replyLiteral` 加 `always→"always"`（v2 分支同映射）；claudecode/grokbuild/dsh-web 把 `Behavior=="always"` 容错为一次性 allow（防御纵深——iOS 只对官方载荷发 always）。
- go-bridge `permission_request` 载荷 additively 加 `permissionKind`/`patterns`（薄载荷向后兼容，其他 backend 原样）；协议文档 + 双仓 schema 同步（顺带补齐 Mac 侧 schema 缺的 session_retry_status 联合项，两仓恢复逐字节一致）。
- iOS：ToolStep 加官方字段（Codable 兼容旧缓存）；TaskDock 官方布局（「需要权限」+ 类别行 + patterns 逐行 + 拒绝[plain]/始终允许[bordered]/允许一次[prominent]，官方顺序）；消息流卡标题「需要权限」+ 副标题类别行；`OfficialPermissionCatalog` 逐键镜像官方 zh.ts 14 键（未知键不渲染类别行——官方 `value === key` 语义）；`approveAlways` 对官方请求回 wire `"always"`（非官方降级 `"allow"`，与 Claude 等兼容）。**非官方载荷的权限卡两按钮布局原样保留。**

**测试**：agent events_test 夹具换成活体帧（旧夹具即虚构形状的实物证据，已销）；go-bridge 官方/薄载荷双向断言；iOS wire 行为（always/降级）、官方字段解码、dock 模型透传、catalog 全键、ToolStep 旧 JSON 兼容共 10 个新用例。先存失败不变（dsh-web 后台任务详情 ×1、go-bridge 投影时序 ×1，均 stash 证实在 HEAD 复现，与本轮无关）。

## 四·补七、权限卡复测收口：投影 SoT 缺官方字段 + 双 backend 同 serve 互扰 + 老 opencode 移除（owner 复测 2026-08-19 19:23，commit `6a50a75` / iOS `f334e95`）

**owner 复测**：文案已显示（如「是否编写xx文件」）但「始终允许」按钮仍缺；截图卡仍是旧布局（「权限请求」+「正在读取 /path」+ `unknown` 杂字 + 两按钮）。

**根因（日志实锤，两叠加）**：
1. **投影 SoT 缺字段**：SSV2 权限卡以投影 part 为 SoT（raw permission_request 只是兜底）。内核 permission_request reducer 造的 ProjectionPart 从未携带 permissionKind/patterns——第一轮只修了 raw 事件链，投影链没修。`unknown` 杂字来源定位：iOS `mapToolStep` 的 `part.toolName ?? "unknown"`（薄 part 无 toolName）。
2. **双 backend 同 serve 互扰**：runtime 同时启动老 `opencode`（-opencode-url）与 `opencode-web`（-opencode-web-url），**两者同指 4096**。同一 permission.asked 帧被两条订阅各自翻译：两条 permission_request（同 per_id、薄/厚）+ 两条投影流（各自独立 syncRev）灌进 iOS 同一 replica（`applyPatch` 只按 sessionId 分键、不分 backend）——后到者胜，rev 交错还触发 base mismatch → recovery pull，卡片呈现非确定。

**三处收口**：
- **投影 SoT（根修）**：内核 reducer 落 `permissionKind`/`permissionPatterns` 进 ProjectionPart（merge 非空不回退；clone 深拷贝新 slice）；schema/协议文档 additive 同步；iOS SessionProjectionPart + mapToolStep 透传。
- **iOS 竞态防御**：`mergedToolStepOnUpsert`——同 id 权限步骤官方字段只进不出、已解析卡不被迟到 pending 重开；`recordPendingPermissionOfficial` 只升不降。
- **移除干扰源（owner 指令）**：老 opencode backend 从 Swift 默认驱动列表与 go flag 默认移除（`-drivers claude,codex,grokbuild,dsh-web,opencode-web`；代码保留，回滚=加回列表并翻转驱动测试断言）。新 runtime 注册表已验证无 backend=opencode，4096 单订阅。

**遗留说明**：iOS 端老 opencode 模式入口未动（Mac 侧 backend 缺席后 descriptor 不再下发，iOS 不会展示可用 backend）；如需彻底清理 iOS 入口另立任务。

## 四·补八、读问薄卡复测：官方三按钮改为无条件渲染（owner 复测 2026-08-19 19:42，iOS `c1762c6`）

**owner 复测**（老 backend 移除后）：iOS 发起「写贾宝玉外貌描写到红楼梦故事.txt」——读取问（第 1 问）仍两按钮无「始终允许」，编辑问（第 2 问）三按钮正常；**Mac 端两问均三按钮**。

**取证（全栈沙盒复现）**：搭独立复现栈（permlab mock serve + 沙盒 bridge 实例 8799 + 配对 WS 客户端模拟 iOS wire 行为，含 set_observation_scope full_stream）。结论：**Mac 侧双链两问均带官方字段**——wire permission_request（每问双订阅线×双投递路径共 4 份）全部 kind=external_directory + patterns；内核 reducer/PartOp 序列化结构直通 ProjectionPart。薄卡来自 iOS 侧某条未定位的步骤落地路径（真机 syslog 因 Wi-Fi 连接无法取证，模拟器 UI 复现因无 idb 后端未完成）。

**修正（官方本义）**：与其继续追未定位路径，改对齐官方行为本身——官方桌面 `session-permission-dock.tsx` 的三按钮**无条件渲染**，不看载荷 richness（这正是 Mac 端两问都有「始终允许」的原因）。iOS 据此：
- `PermissionDockModel.offersOfficialActions`：opencode-web 会话恒 true；`permissionKind` 非空时任何 backend 也启用。
- TaskDock 官方卡双条件（kind 或 offersOfficialActions）；薄步骤回退显示 title 行。其他 backend 两按钮布局不变。
- `approveAlways` 对 opencode-web 会话直发 wire `"always"`（不再依赖 official 标记；serve 枚举已活体验证）；其他 backend 降级 `"allow"` 行为不变。

**测试**：`testOpenCodeWebSessionOffersOfficialActionsEvenWithoutKind`（薄步骤 + opencode-web 会话 → 官方按钮）；TaskDock/WireBehavior 套件绿。真机 c1762c6 已装。

## 四·补九、验收行 2 收口：坏模型回合 iOS 空等 = relay prekey 耗尽（owner 验收 2026-08-19 20:2x，iOS `b2d934c`）

**owner 验收**（5/6 过：占用圈/历史一致/实时旁观/审批收口 ✅）：行 2——坏模型（权限不足/额度没了）Mac 端已报错，iOS 空等。

**取证（双路）**：
- 现网日志：两次 iOS 发送（20:21:56 好模型流式正常、20:22:45）均完整跑完（turn_completed 20:23:09），serve 侧消息记录完好；但自 **20:22:26 起 `relay-router: prekey exhausted`（两台设备）**持续告警，且整个 runtime 生命周期 iPhone **零次** `get_delivery_prekey_status`。
- 全栈沙盒复现（mock provider 第 2 次调用回 403 权限不足）：**Mac 翻译链对该失败模式完全正确**——`session_retry_status(attempt=5) → turn_error(真实文案) → session_state_changed idle → text_delta(错误文本)`，终态收口 + 文案均 YES。排除翻译层。

**真因**：`turn_error/turn_completed` 是 durable 里程碑，设备瞬断（锁屏/后台/重连窗口）时走 relay 加密信箱投递；**直连 transport 恒 `relayCredentials:nil`** → `runRelayPostConnectMaintenance` 直接跳过 → LAN 直连期间 prekey 池只耗不补（目标水位 32，全天测试耗尽）→ 信箱投递丢失 → iPhone 永远等不到终态 → 空等（Mac 官方端直连 serve 正常报错）。

**修复（iOS b2d934c）**：`loadRelayCredentialsForMaintenance`——直连时凭据从 SavedBridgeStore 装载（与 connector 同源语义，无 relay 配置静默跳过）；`replenishDeliveryPrekeysIfNeeded` 改走装载器；维护任务改 **5 分钟周期循环**（长连接期间也补水）。MailboxReplay/RelayFrame/Phase2 套件绿。

**验收行 5 说明**：旧 OpenCode 入口按 owner 指令已从 Mac 驱动列表移除（§补七），iOS 旧入口不再可用属预期；恢复 = 驱动列表加回（测试断言已翻转，回滚路径在案）。

## 四·补十、backlog 四项清偿（owner 指令 2026-08-19 深夜，Mac `9796432` / iOS `a049625`）

**1. 先存测试失败修复（两仓）**：
- **Mac go-bridge 投影派生帧**（先存确定失败，非 flaky）：`question_asked/question_resolved` 是 canonical `user_input_*` 的单向派生 legacy 呈现帧，给 SSV2 连接发 raw = 复活已废弃的双发布路径——`shouldDeliverRawEventLocked` 拦截（legacy 连接照发）。
- **Mac dsh-web 后台任务详情**：断言停在 Phase 4 只读语义，实现已是 Phase 5（running 可 `session.cancel`）——更新断言 + 补终态任务不可取消。
- **iOS ColdCache 挂起**（全量套件此前永远跑不完）：根因 `testInitialize_codexRootCatalogDeduplicates…` 漏 preset，无预设 scope 的 `loadSessionListSnapshot` continuation 永挂——补 cache-miss 预设；`ControllableSnapshotStore` 加 30s 挂起兜底自动放行（防未来遗漏释放；正常竞态用例毫秒级释放不受影响）。
- **iOS pinch-zoom**：iOS 27 runtime 下 `scrollView.pinchGestureRecognizer` 为 nil，`nil == false` 恒假——`isViewportZoomDisabled` 对 nil 手势按 scale 锁死判定。
- **成果**：全量 CCCodeTests 首次完整跑完——**2059 过 / 7 败**；7 个均为基线（stash）证实的先存失败（重试/合并计数类产品语义漂移，此前被挂起掩蔽，新暴露），已立 backlog 待专项，非本批引入。

**2. iOS 旧 OpenCode 入口清理**（决策：入口移除、兼容层保留）：`serverCreationCases` 去 `.openCode` + `isDeprecated` 标记（thinBridge/deepSeek 同列）；枚举与 Codable 解码保留（已存 openCode 服务器仍可显示/编辑，Mac 侧无 backend 即不可用）；AddServerView 新建默认 `.claudeCode`；切换菜单按 deprecated 过滤（夹具随之更新）。

**3. 重试瞬态行补偿**（owner 验收遗留小缺口，双侧）：
- Mac（9796432）：Agent 记录最近重试快照（2 分钟新鲜窗），`StartSession` 重附（重开会话/relay 重连）时重放一次 `session_retry_status`；回合收口（idle）清除防复活。测试 `TestRetrySnapshotReplayOnStartSessionAndClearOnIdle`。
- iOS（a049625）：ChatViewModel 记录最近 `session_retry_status` 快照，`didBecomeActive`（解锁/回前台）时当前会话仍在执行且快照新鲜 → 重放 `.retrying` 相位（运行状态栏「自动重试中（第 N 次）」补显）。

**4. v2 形状复核**（对 v2 SDK `v2/gen/types.gen.ts` 逐一审计，修复三处漂移 + 五个契约测试 `TestOfficialShape_V2_*`）：
- postModel 扁平 body → 嵌套 `{"model":{id,providerID}}`（V2SessionSwitchModelData/ModelRef）；
- abort/interrupt 去 JSON body（两代 SDK 均 `body?: never`）；
- directory 收敛：v2 仅 `GET /api/session`（V2SessionListData 唯一声明 directory query），其余路由不带；`x-opencode-directory` 头为 1.18 惯例，v2 wire 不带；
- v2 响应信封兼容：create/fetchSessionInfo 解 `{"data":…}` 包裹；
- **如实声明**：v2 无现网验证（owner serve 1.18）；location 绑定（LocationRef/workspace 流）未实现——v2 create 在 serve 默认 location 落点，接入 v2 现网前需补核。

**新暴露 backlog（先存、7 项，基线证实）**：SessionsViewModelServerSwitchTests ×4（initialize 重试未发生）、testSessionRefreshNotification_isCoalescedForCodex（1≠2）、testCodexSessionResumeWaits…（1≠0）、testAuthoritativeEmptyTodoFetchClearsStaleCacheAndDock（权威空拉取未清陈旧 todo 缓存）——均为产品语义漂移，需逐个判读意图后修，另立任务。

## 四·补十一、新会话首回合残留双症状修复（owner 复测 2026-08-20 上午，Mac `58a1261`）

**症状**（iOS 真机，opencode-web chat 目录新建会话发第 1 条消息）：输入框立即 executing→**立即 completed**→数秒后回复**整段一次性**到达（非流式）；第 2 条消息起完全正常；dsh-web（同为 web API+协议转换）首回合正常。§补九/补十的双端修复（`6fcad07`/`a0b623d`/iOS `79c1a12`）已让内容可渲染，本轮清的是残留时序病。

**根因**（三处同根于首回合 pending→real 懒建会话窗口，双端勘察收敛）：
1. **裸 idle 假终态**（agent `events.go emitResultOnce`）：`POST /session` 的创建广播（`session.updated/status idle`）在 user echo 之前穿过 SSE filter，`turnID==""` 落兜底分支发出健康 `EventResult`——假终态让 relay 退出（opencode-web 不跨回合存活），**首回合 live feed 断供**（第二回合 send 重启 relay 故正常）。
2. **seal 竞态**（go-bridge `streamBackendRichHistoryProjectionEvents`）：iOS 首拉落在 prompt_async 已入队、serve busy-map 未登记的窗口，1.18 缺 key 判**确定性 idle**→刚发出的 user turn 被 seal 成 `turn_error{rich_history_unanswered}`→提前 commit 终态基线→iOS 输入框翻完成。
3. **sourceIsLive 缺失**（go-bridge `ensureProjectionHydrated`）：opencode-web 不在 §3.1 live 信号采样名单（只有 claude/claudecode/codex），首拉必然 mid-turn 的提交闸门只能等 cold-armed 在飞 turn 的终态——期间所有 live 帧攒在 `pendingLive`，回合结束才一张 patch 放出整段回复。dsh-web 首回合正常 = real id 同步返回（首拉在发送前、空会话瞬时 commit），无此窗口。

**修复**（Mac `58a1261`，三处）：① `emitResultOnce` 裸 idle（无 armed turn 且无 terminal error）不 emit、不置 `completed`；② seal 决策加桥 registry-live 覆盖（桥持有 live 的会话不得 seal；死会话冷开 seal 语义保留，2026-08-14 空 turn 修复不回归）；③ opencode-web 补 `sourceIsLive` 采样（registry liveness）→ 首拉 mid-turn 按 §3.1 提交诚实 running partial，流式照常。注：dsh-web 的 live-only admission 分支**不适用**于 opencode-web——`pathlessRichHistoryBackend` 含 opencode-web，live-only source 会走 empty-rebuild 分支清空基线，故选 sourceIsLive 路线。

**回归**：`TestFreshSessionIdleBeforeUserEchoDoesNotEmitResult`（agent，含真回合收口恰好一次）；`TestOpenCodeWebLiveSessionColdPullSkipsSealAndCommitsRunningPartial`（busy-map 竞态+seal 覆盖）/ `…WithBusyProbeCommitsRunningPartial`（sourceIsLive 单独钉死）/ `…DeadSessionColdPullStillSealsUnansweredTurn`（死会话 seal 保留对照）。全仓 go test 绿（2069 项全过，含先存）。

**装机**：Mac Release 已覆盖 `/Applications`（runtime `58a1261244d0`，11:04 构建，已重启在线）；iOS 侧本轮无改动（`79c1a12` 已在真机）。待 owner 复测同场景：预期输入框持续 executing + 流式回复 + 正常收口。

## 四·补十二、首回合 ~1s completed 闪烁收口（owner 复测 2026-08-20 11:1x，Mac `4e42185`）

**症状**：补十一后复测——攒帧已消（能流式输出、收口正常），残留：发送后输入框 executing→completed 持续约 1s→executing 流式。owner 问询「为啥中间有大概 1 秒变成完成态」。

**根因**（两个残余竞态，均取证时已识别为次级路径）：
1. **registry rebind 滞后**：real id 在 `Send` 内同步解析（懒建会话），但 registry 的 pending→real rebind 原要等 relay 消费首个 real-id SSE 事件。窗口内首拉虽能解析 real id，`getSession(real)` 却落空——补十一的 seal 覆盖与 sourceIsLive 采样双双失效。
2. **首 prompt 持久化竞态**：首拉可在 user 消息 durable 前命中冷源 `GET /session/{id}/message` 返回 0 条 → 按「真空会话」commit 空 idle 基线 → iOS 渲染 idle 翻完成；echo/流式帧到达（~1s）→ running 投影 → 翻回 executing。这正是 owner 观察到的 1s 窗口。

**修复**（Mac `4e42185`，两处）：① `handleSendMessage` 在 Send 成功后立即 `rebindSessionIDIfResolved`（幂等；relay 逐事件 rebind 保留为兜底）——seal 覆盖/sourceIsLive 自 Send 返回即刻生效；② opencode-web 冷源对「registry-live 且 0 条消息」短暂重拉（200ms×6，上限 1.2s）再放行空基线——首 prompt 持久化窗口内不再 commit 空 idle；死会话与其他 backend 保持单次拉取（web 空会话冷开不受影响，对照测试钉死）。

**回归**：`TestOpenCodeWebSendRebindsPendingToRealBeforeFirstEvent`（行为级，sendHook 模拟 Send 内解析 real id；断言 real 键即时可用 + pending 别名同对象）；`TestOpenCodeWebLiveEmptyColdSourceRepollsForFirstPromptPersist` / `TestOpenCodeWebDeadEmptyColdSourceSingleFetch`。全仓 go test 绿。

**装机**：Mac Release 覆盖 `/Applications`（runtime `4e42185`，11:2x，已重启在线）；iOS 侧仍无改动。待 owner 三测同场景：预期全程 executing 无闪烁 + 流式 + 收口。






## 五、验收矩阵（owner 执行——§6 现网行 1–6）

前提：Mac 运行新 Release（已装 `/Applications`，runtime commit `78b72f1`）；iPhone 安装本分支 Debug（已装）；Mac 网页打开 `http://127.0.0.1:4096`。

| # | 前提条件 | 动作 | 应看到 |
|---|---|---|---|
| 1 | 网页里找一个占用约十几万 tokens 的会话 | iPhone 切 **OpenCode Web** 打开该会话，看 ⭕ 占用圈 | 有已用/窗口数值（非「暂无」），与网页百分比一致 |
| 2 | 同一会话 | iPhone 选目录内模型后发一条短消息 | 有流式回复；若故意选目录外坏模型 → 明确失败提示（非空等 81ms）。附加观察点（监工建议）：**只选 agent 不选模型**发送 → 现状会用首个 connected provider 的默认模型（官方 web 此场景用该 agent 的 model 字段，见 §四·补 缺口注记） |
| 3 | 网页 `/server/<项目>/session/…` 任一历史会话 | iPhone OpenCode Web 按同目录打开 | 历史消息与网页一致（含工具/思考块） |
| 4 | Mac 网页某会话打字发 turn | iPhone 已打开同一会话 | 实时旁观（外部 turn 流式到 iPhone） |
| 5 | 同一 Mac | 切回旧 **OpenCode** 入口 | 旧入口仍可用；占用圈空等脏行为**保持原样** |
| 6 | 让会话触发一次工具审批 | iPhone 上 Allow / Deny 各试一次；另试一次网页先答 | turn 继续/停止；网页先答则 iOS 收口；**绝不自动 allow** |

owner 回报格式：行号 + ✅/❌ + 现象即可。行 1–6 全 ✅ 后，退役流程（§4.2 另立任务）才可启动。

## 六、未完成 / 边界如实声明

- ~~**§8-8 回归（现网矩阵行 1–6）未执行**~~ → **已闭环（owner 真机验收 2026-08-19，三轮）**：行 1/3/4/6 ✅；行 2 初判 ❌ → 取证定真因 relay prekey 耗尽（§补九，非翻译层）→ 修复后复测通过；行 5 旧入口不可用 = 按 owner 指令移除（§补七），预期行为。**本报告整体完成。**
- 两条先存测试失败（见「验证记录」）非本次引入，未修复（不在本任务范围，避免越权改动）。
- M3 提示 3（dsh-web 不在 re-attach 名单的既有缺口）：设计已记录为 owner 决策项，本次未代为修复（`observationResubscribeBackends` 注释保留标记）。
- 真机 UI 仅安装启动（`scripts/run.sh device`），未做任何 UI automation/视觉验收（需 owner 明确授权）。
- 已知小缺口（owner 记过、暂不动）：重试瞬态行（session_retry_status）为瞬态设计，熄屏/后台窗口缺席，终态不受影响；如需收紧再立任务（可拉取状态或回前台补偿）。
