# OpenCode Web Backend 设计稿评审报告

- 评审对象：`docs/2026-08-18-opencode-web-backend-design.md`（v2，2026-08-18）
- 评审日期：2026-08-19
- 评审方法：本机 4096 managed serve 只读活体探针（health/列表/详情/消息/status/provider/agent/project/todo/SSE 连接/占用回落/目录头；零写请求）+ 官方 checkout 源码（占用公式、权限 schema、v2 路由）+ 1.18 二进制 strings 取证 + go-bridge/iOS 全量接线扫描 + 旧包 `agent/opencode/` 行号核对
- 评审基准：dsh-web 四轮评审方法论（B1 载波病、M4 接线表病、R3 机制臆造病、「编译器强制」的边界）

## 1. 结论

**修改后通过**（3 项必改，均集中在接线表完整性——E 维度；无阻断）。

§3 前置核实的**全部关键断言经活体+源码双重证实**：载波确为 HTTP+SSE（`/global/event` 200 text/event-stream，帧 `data:{"payload":{...}}` 与 §3.6 逐字一致——dsh-web B1 病类不复发）；双代 API 表 1.18 列逐行与活体相符（本机 1.18.18，`/global/health` 401→200、`/session` 裸数组 100 项、`/session/status` 空对象、`/provider` dict）；占用公式与 `session-context-metrics.ts:28-61` 逐行一致且回落路径活体坐实（顶层 input=0 会话的 last assistant `info.tokens={total:18457,...}`——与设计引用同源同数）；权限 v2 `once|always|reject` schema 证实；十三坑的事实引用全部属实。

问题集中在：**iOS 接线表漏了一整类引用**（`if == .openCode` 比较型共 ≥12 处/3 文件——不走穷举检查、编译器不报错、且直接承载设计自己承诺的目录分组行为），以及 go-bridge 两处表外键控点（目录切换特判、relay 空闲超时名单）。这正是 dsh-web M4 病的变体，且比 dsh-web 更隐蔽（那边靠编译器兜底，这边不会）。

## 2. 问题清单

### 必改

**M1（iOS 接线表遗漏「if 比较型」引用 ≥12 处/3 文件，且承载目录分组核心行为）**

设计 §4.1.7 声称「穷举（编译器强制，漏一处编不过）」并列 12 文件。全仓扫描 `rg '\.openCode\b'`（非测试）实得 **18 文件**。表外除默认值类无害项（Server.swift/ServerConfig.swift/AddServerView.swift/BridgeOfflineSnapshotAdapter/BridgeDiscoveryService 的 `= .openCode` 默认与 decode 兜底）外，有**三类行为分支被遗漏**：

| 文件:行 | 语义 | 不加 `.openCodeWeb` 的后果 |
|---|---|---|
| `Views/Session/SessionsView.swift:916` | 缓存阶段 OpenCode bucket 种子 | 列表无分页桶行为 |
| `SessionsView.swift:2117/2130/2134` | `fetchProjects`+去重+`sortedOpenCodeProjects` 项目合并 | 项目列表走通用路径（混入 manual 目录、无 OpenCode 排序）——**直接击穿 §4.1.7 承诺的 `usesRootOnlySessionCatalog=false`「按目录对齐网页」** |
| `SessionsView.swift:2663/2685/2715` | bucket 懒加载与分页路径选择（:2715 `if kind == .openCode { return nil }`） | 目录分组深挖失效 |
| `Views/Components/SidebarView.swift:91/281/388` | 项目分组侧栏显示条件 + 按目录 group 懒加载 | **目录分组侧栏整体不显示**（:91 `== .openCode && projectRoots` 为 false） |
| `ChatViewModel+SessionManagement.swift:1134/1140` | agent 过滤（OpenCode 只显 primary agents） | agent 列表走 subagent 过滤路径，与旧 OpenCode 行为不一致 |

关键定性：这些全是 **`if ==`/`guard ==` 比较，不经过 Swift 穷举检查**——新增 `.openCodeWeb` **不会产生任何编译错误**，设计「编译器强制」的安全声明对它们不成立。修改建议：§4.1.7 表补上述 3 文件 ≥12 处（逐处归组决策，预期多数「与 `.openCode` 同组」），并把表头声明改为「switch 类编译器强制；**if 比较类编译器不报错，必须 rg 人工核对**」；默认值类 5 文件注记「无需改动」分类列出。

**M2（go-bridge 表外遗漏两处，含坑 5 复发路径）**

1. `dispatchRPC` `handlers.go:1236`：`if agent.Name() == "opencode" || shouldSwitchWorkDirForMethod(...)` 的 switchDir 特判——`shouldSwitchWorkDirForMethod` 对 `list_sessions/get_session/get_session_messages/get_session_projection` 返回 **false**（handlers.go:1226-1233），旧 opencode 靠 Name 特判让这四个读方法也切目录。opencode-web（Name=opencode-web）不匹配特判 → **列表/详情/历史/投影四个读方法永不切目录** → `x-opencode-directory` 恒为启动 workDir → 坑 5（目录头错）原样复发，§4.3.1「请求头=当前 workDir」的机制落空。修改建议：§4.1.5 表加这行，处置=特判扩为含 opencode-web（读方法也 switchDir，与旧 opencode 同构）。
2. `disablesRelayIdleTimeout` `handlers_relay.go:2421-2427`：`case "claude","claudecode","codex","opencode","dsh-web"` 禁用 relay 60s 空闲超时——注释明写这是 dsh-web 真机故障（审批等待期间无 text_delta，60s 超时把已 surface 的权限卡收口）。opencode-web 一期必接审批（permission.asked 等待期间同样无增量），**不加入必复发同型真机故障**。修改建议：表加这行，处置=加入 case。

**M3（`resubscribeObservationSessions` `handlers.go:385` 硬编码五 backend，设计的旁观卖点未覆盖此处）**

重连后 observation scope 的 re-attach 循环硬编码 `[]string{"codex","claudecode","opencode","grokbuild","claude"}`——**dsh-web 也不在**（dsh-web 未加也未见表内/报告记载，属既有疑似缺口）。opencode-web 的 §4.3.3「外部 turn 旁观」依赖 observation 机制，设计未提此点。修改建议：表加这行并显式决策（进列表=与 codex/claude 同待遇；若与 dsh-web 同样不进，需写明理由及 iOS 重连后外部会话恢复的实测验证项）。

### 建议

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| S1 | §3.6 顶层 `tokens` 样板失真：实测顶层 tokens **无 `total` 字段**（`{input,output,reasoning,cache:{read,write}}`，100 会话采样）；**message 级 `info.tokens` 有 `total`**（活体 18457 实例） | 活体采样 | 样板按实测修正，注明两级 tokens 形状差异 |
| S2 | `GET /project` 元素字段是 **`worktree`**（`{id,worktree,vcs,time,sandboxes}`），非 directory/path | 活体采样 | §4.3.7 list_projects 补字段映射（worktree→目录建议） |
| S3 | 本机 1.18.18 **双面共存**：`/api/health` 200（`{"healthy":true}`）、`/api/session` 返回 `{data:[...]}` 信封（且首项与无前缀面不同）——探针互斥性依赖「v2 无 `/global/health`」这一事实 | 活体 + checkout（v2 server 无 global/health 路由，health 走 effect HttpApi 组挂 /api） | §3.2 加一行注明双面共存事实与互斥依据；探针第三步（数组 vs `{data}`）仍是最终形状判据，维持 |
| S4 | 1.18 权限字面量增强取证：1.18 二进制 strings 中 `once/always/reject` 三字面量齐在 | `/opt/homebrew/lib/node_modules/opencode-ai/.../bin/opencode` strings | 写入 §3.4 提升置信；实施期仍按设计的「先探 once、4xx 再 allow」策略（二进制字面量不能证明唯一枚举） |
| S5 | `backend_capabilities.go:59-63` 的 permission_resolve 排除名单（`id != "opencode" && id != "codex"`）对新 id 自动放行 | 源码 | §4.1.5 注一句「无需改动，ToolAuthorizer 断言即广告」，防实施者误判要动它 |
| S6 | `handlers_session_pin.go:255` 旧 opencode 的 directory-scope pin 查找对新 backend 走 any-scope（default） | 源码 | 注记行为差异（可接受，如实） |
| S7 | `catalogCapabilityRequiredFor` 现列五 id、**dsh-web 不在**（false）且 dsh-web 的 list_models 工作正常 | handlers.go:1029-1035 | 设计称「不加则 list_models 失败被静默」——与 dsh-web 实际不符；实施前对照 dsh-web 行为确认危害描述，避免为不存在的静默路径做多余接线 |
| S8 | §4.3.4 switch_model 在 1.18「记 pending、下次 prompt 带新 model」——iOS 选择器即时反馈与服务端延迟生效的 UI 语义差 | 设计文本 | 注记：诊断/UI 需向用户呈现「下次发送生效」，避免真机验收争议 |
| S9 | 坑表引用 iOS `ChatViewModel.swift:327-329 漏 .deepSeekWeb` 是历史事实，**当前 main 已含 `.deepSeekWeb`**（dsh-web 实施时修复） | iOS main 分支实测 | 坑 9 引用补注「已修复，openCodeWeb 同样需加入」 |

## 3. 功能面覆盖对照表（A 维度）

基准：handlers.go dispatch 全集（53 RPC，dsh-web 评审已枚举）+ iOS 用户可见功能。「核实」列证据来源：活体（🧪）/源码（file:line）/官方 checkout（📦）。

| RPC/功能 | 设计处置 | 独立核实 | 缺口判定 |
|---|---|---|---|
| list_sessions | ✅ GET /session（裸数组） | 🧪 裸数组 100 项、title/directory/time.updated(ms) 全在 | 🟢（目录头机制缺接线=M2） |
| get_session | ✅ GET /session/:id | 🧪 200、字段同列表 | 🟢 |
| 置顶×2 | ♻️ bridge pin（resolvePinWire default） | handlers_session_pin.go:158-168 default=`resolveAgentListPin` ✓ 不加 case 即走 | 🟢（S6 scope 差异注记） |
| 下拉刷新/外部列表同步 | ✅ SSE session.created/deleted + CatalogRefreshSignaler + discovery 保底 | SSE 面活体 ✓（server.connected 帧实测）；既有机制 | 🟢 |
| get_session_messages | ✅ /session/:id/message → mapRichHistoryEntry 复制 | 🧪 裸数组、info/parts 形状与 §3.6 一致（活体 3 消息会话）；带 directory 头成功取数（坑 5 机制实证） | 🟢 |
| **占用圈 ⭕** | ✅ §3.3 公式 + tokens=0 回落 | 公式逐行=官方源码（session-context-metrics.ts:28-61）；🧪 回落实例：顶层 input=0、last assistant total=18457（与设计同源） | 🟢 核心卖点坐实 |
| get_session_projection | ✅+♻️ pathless | 接线表齐（见 §5） | 🟢（M2 switchDir 影响读路径） |
| 流式/旁观 | ✅ SSE 常驻 | 🧪 载波坐实 HTTP+SSE；帧 payload 包装与 §3.6 一致 | 🟢（M3 observation re-attach） |
| create_session | ✅ POST /session{directory,model} | 旧包 server_session.go:139-166 先例（只读核对）；目录头要求 ✓ | 🟢 |
| send_message | ✅ prompt_async **必带 model** | 坑 4 事实核实（:116-118 只有 parts）；§4.3.4 catalog 校验 | 🟢 |
| abort_generation | ✅ abort / interrupt（按代） | 双代表 v1.18 `abort`（旧包先例）、v2 interrupt（checkout） | 🟢 |
| 审批 resolve_permission | ✅ 一期必接（折叠表） | v2 `once|always|reject` schema 证实（permission.ts Reply Literals）；1.18 二进制 strings 三字面量齐在（S4）；折叠表与 core Behavior（interfaces.go:71-75）自洽 | 🟢（1.18 字面量按设计的先探策略挂账，可接受） |
| 问答 question×2 / resolve_user_input | ⛔ | 旧包 RespondQuestion not supported（:190-196）✓；1.18 问答面未活体打通 | 🟢 诚实（iOS 空态与旧 OpenCode 同态） |
| list_providers / list_models | ✅ /provider | 🧪 dict{all,default,connected}——§3.6「递归收集」策略兼容 | 🟢 |
| switch_model / set_provider | ✅ 会话级（1.18 pending 下次带） | v2 POST /model（checkout）；1.18 无独立面（设计如实） | 🟢（S8 语义注记） |
| list_agents | ✅ GET /agent | 🧪 裸数组 `{name,description,mode,native,permission}` | 🟢 |
| set_agent_preset / permission_modes×2 | ⛔ | 无同构面 | 🟢 诚实 |
| resume_session | ✅ StartSession+订阅 | — | 🟢 |
| rename_session | ✅ 条件（1.18 活体为准，无则 ⛔） | 只读无法钉死（写请求禁做） | 🟢 挂账处置可接受 |
| delete_session | ⛔（禁 CLI delete） | — | 🟢 诚实 |
| archive / compress | 2️⃣ | 🧪 `/compact` 子路径存在（GET 200） | 🟢 |
| share / diff 三件套 / git 六件套 / get_usage / fetch_todos | ⛔/2️⃣ | 无面；/todo 活体 200（二期有据） | 🟢 |
| list_projects | ✅ GET /project | 🧪 存在；字段是 worktree（S2） | 🟡 S2 |
| list_directory | ♻️ bridge FS | — | 🟢 |
| run_diagnostics | ✅（来源/代/health/catalog/loopback） | — | 🟢 |
| memory×2 / cancel_request_v1 / notifications / prekey×3 / 本地搜索 | ♻️/⛔ 自动 | 机制同 dsh-web 评审 | 🟢 |

**覆盖结论**：53 RPC + iOS 功能全部有明确处置，无「找不到处置」项；✅ 诚实性抽验全部通过（活体/源码双层）；分期构成可用产品（占用/发送/停止最小闭环 ✓、审批一期闭环 ✓、问答空态成立 ✓）。

## 4. 十三坑闭环判定表（C 维度）

| # | 坑 | 事实引用核实 | 消除机制落点 | 判定 |
|---|---|---|---|---|
| 1 | CLI+sqlite 列表 | ✅ opencode.go:538-540（listOpencodeSessions）、:707/717 sqlite3 | §4.3.1 只 GET /session + import_guard | 闭环 |
| 2 | CLI+磁盘模型缓存 | ✅ opencode.go:99-106 | §4.3.5 /provider + 禁符号清单 | 闭环 |
| 3 | 占用读错字段 | ✅ context_usage.go:86-107 顶层 tokens | §3.3 公式+回落（**活体坐实**） | 闭环 |
| 4 | 发送不带 model | ✅ server_session.go:116-118 | §4.3.4 catalog 校验+必带 model | 闭环 |
| 5 | 目录头错 | ✅ 活体实证（不带/带错头消息 0 条→带会话 directory 得 3 条） | §4.3.1 会话 directory——**但 dispatchRPC:1236 特判未入表（M2），机制不完整** | **部分**（M2 修复后闭环） |
| 6 | 问答未接 | ✅ server_session.go:190-196 | ⛔ 诚实空态 | 闭环 |
| 7 | 权限字面量 | ✅ v2 schema once/always/reject；旧包 :173-177 发 bridge 原值 | §3.4 折叠表+先探策略 | 闭环（S4 增强） |
| 8 | checkout 当唯一现网 | ✅ 本机 1.18.18 无前缀面 | §3.2 双代表+探针（S3 双面共存注记） | 闭环 |
| 9 | SSV2 漏 kind | ✅（历史事实；main 已含 deepSeekWeb——S9 补注） | §4.1.7/§8-5 同期 iOS 接线 | 闭环 |
| 10 | 抢端口 | ✅ | 客户端不 spawn + 沙盒 4296…4396 | 闭环 |
| 11 | iOS 自动批权限 | ✅ autoApproveBridgePermissionIfNeeded 只对 .openCode | §4.1.7 明令禁止进分支 | 闭环 |
| 12 | detectAgentStatus 默认 Available | ✅ agent_descriptor.go default=Available | §4.1.5 加 case（dsh-web 先例在） | 闭环 |
| 13 | shouldStartPassiveSubscription 默认 true | ✅ main.go:778-788 非 codex/opencode 一律 true | §4.1.5 空 URL 不启动 | 闭环 |

dsh-web 病类复发检查：载波断言（SSE 活体证实 ✓ 不复发）；机制臆造（抽查 §4.1.5 表的三条关键行为断言全部源码背书：`streamOpenCodeRichHistoryProjectionEvents` 确实硬编码 name="opencode"（handlers_projection.go:1231）、`streamBackendRichHistoryProjectionEvents` 按 getFirstAgentByName 取 agent（:1244）、`resolvePinWire` default 存在（handlers_session_pin.go:166）✓）；接线表病（M1/M2/M3=复发，且因 if 比较型无编译器兜底而更隐蔽）。§2.2 七条纪律（含新增 6/7）与设计自检一致。

## 5. 接线表核验结果（E 维度）

### 5.1 go-bridge 表内（§4.1.5）逐行

全部核验属实：main.go import 区/drivers:35/buildAgentOptions:801（opencode_url 塞所有 agent 的断言 ✓）/shouldStartPassiveSubscription:778/RegisterOpenCodeProxy:197（id=="opencode" 门控 ✓ 不注册即安全）、agent_descriptor.go:198 detectAgentStatus+default Available+:191 instanceStatusProber+dsh-web 先例、handlers.go catalogCapabilityRequiredFor:1029（🟡 S7 描述核对）/backendHasNoExternalEventSource:1041（仅 deepseek ✓ 不进）/backendKindForAgent:4287（default=Name() ✓）、handlers_projection 六点（:340 集合/:490 sourceChanged/:560 agentName switch/:653 pathless/:1027 负条件链/:1086 case 流）+ streamBackend 断言、projection_kernel.go:692、server.go:279（dsh-web 先例 `kind == "deepseek-web"` 确认——opencode-web kind=id 同名更简）、pin default ✓、handlers_opencode.go 不开门 ✓。

### 5.2 表外遗漏（rg '"opencode"' go-bridge 全量对照）

| 位置 | 定性 | 处置 |
|---|---|---|
| **handlers.go:1236 dispatchRPC switchDir 特判** | **必改 M2-1**：四读方法不切目录 | 特判扩含 opencode-web |
| **handlers_relay.go:2421 disablesRelayIdleTimeout** | **必改 M2-2**：审批等待 60s 收口（dsh-web 真机同型） | 加入 case |
| **handlers.go:385 resubscribeObservationSessions** | **必改 M3**：旁观 re-attach 名单（dsh-web 也不在） | 显式决策+入表 |
| agent_descriptor.go:46/:60（agentKind/DisplayName default） | default 回 id/Name()，新包实现 WireDescriptorProvider 即覆盖（§4.1.3 已列） | 注记无需改 |
| main.go:129 agentAliases | dsh-web 同样无条目（id=Name 时不需要） | 注记无需改 |
| backend_capabilities.go:59 | permission_resolve 排除名单对新 id 自动放行 | 注记（S5） |
| handlers.go:1022（ocProxy 全量拦截）、opencode-proxy.go:497、catalog_provider_opencode.go:97、handlers.go:4958（.config 过滤）、handlers_session_pin.go:255/284 | 均旧 backend 专属路径，新 id 不命中即安全 | 无需改（255 见 S6） |

### 5.3 iOS 表内（§4.1.7）与表外

表内 12 文件的行号/归组抽验合理（fromWireKind :24-31、sessionSyncV2ProjectionBackend :327 现况确认、autoApprove :1969、backendLiveAgentName :1961 等）。表外见 **M1**：SessionsView（7）/SidebarView（3）/SessionManagement（2）为行为 if 比较必补；Server/ServerConfig/AddServerView/BridgeOfflineSnapshotAdapter/BridgeDiscoveryService（默认值类 5 文件）注记无需改。`ChatViewModel+DirectoryPreferences.swift:107` 附近（dsh-web 评审的 deepSeek 通用路径点）设计表未列——归入 M1 一并 rg 核对。

## 6. 维度 D（架构与合规）摘要

隔离红线（不 import/不删/不写 db/不 bind/不 spawn）设计自洽且 import_guard_test 可实施（源文件字符串扫描 + `go list` import 图断言均可行）；生命周期（现网第二客户端/沙盒 4296…4396+独立数据目录/退役另任务）边界清晰；安全（loopback-only、凭据 0600+env 不进 argv）✓；SSV2 §4.5 真相清单成段完整；初衷四条 ✓；并存干扰（同一 serve 双客户端、审批同一把锁先答者得）如实；**头部已拍板 5 条全程未重开**。§8 八项拆分覆盖 §4.3 全部 ✅ 项（骨架/列表历史占用/SSE+活性/发送模型/投影/审批/iOS+协议/Release——缺 M1/M2 修复项需并入 5 与 7）；§6 测试计划覆盖接线表/占用回落/零输出/权限折叠/import 守卫/双代探针 ✓（补 M1 的 iOS 归组断言与 M2 的 relay 超时用例后完整）。

## 7. 修改后即可批准

M1/M2/M3 全部是设计文档层补表+补行（约 20 行文字），不动摇路线、架构与已拍板事项。修复后本设计达到 dsh-web v3.1 同等交付水位，可交 owner 终审。
