# 长 session 打开优化：非 Claude 适用面与 v2 越界评审

日期：2026-10-20。范围：指定双仓源码的静态分析、修复建议评审。未修改产品代码，未运行 build/test，未安装、部署、重启服务或操作会话。

## 1. Owner 决策单（只读这一节即可）

**结论：一些通用提速措施已经覆盖其他工具，但“Claude 已验收”不能代表其他工具也验收了；目前还把只为 Claude 做好的“先看摘要、点开全文”误用于其他工具，应修正。**

- **Claude Code**：这轮提速已由你验收，继续保留；本次另发现一处慢网络保护遗漏，工程侧补齐即可。
- **Codex Desktop**：原本就有“先看概要、按需取详细过程”的设计，不需要再套一层 Claude 摘要；当前误套后，某些旧会话可能无法打开被折叠的全文，应撤掉这层误套，保留通用传输和重开提速。
- **Grok Build**：传输和重开提速的代码已经接入，但尚无本轮实际提速数字；误套摘要后会提示展开却无法取回全文，还可能漏掉命令记录，应先修边界，再拿长会话测收益。
- **DeepSeek Harness**：传输和重开同样有提速机制，但不能承诺已像 Claude 一样快；误套摘要会让工具过程打不开、部分命令或子任务通知消失，应先撤掉误套，保留通用优化。
- **OpenCode Web**：传输和重开同样可能受益；不能因为官方支持分批读取，就认为手机这条链已经分批读取。当前误套摘要也无法展开全文，应先修边界，后续优先量一次超长会话的等待究竟花在哪里。

**我的建议：现在就修越界，保留其他工具的压缩传输和本地重开；将“先摘要后全文”限定给 Claude，同时处理此前误存的摘要，防止更新后旧问题继续出现。**工程上建议手机停止误用、Mac 拒绝越界请求，两边各守一层；这不需要你选择实现细节。

未来推广顺序建议（推断，尚无同条件速度对比）：先测 **OpenCode Web、Grok Build、DeepSeek Harness** 中哪个实际等待最久，再给最慢者推广；Codex Desktop 放后面，因为它已有类似设计。若现在必须排一条调查顺序：OpenCode → Grok → DeepSeek → Codex；这不是已测出的性能排名。

## 2. 来源身份、证据等级与评审结论

### 2.1 P0 双仓来源（本轮亲核）

| 项 | M：Mac 权威仓 | I：配套 iOS 仓 |
| --- | --- | --- |
| 绝对路径 | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` |
| 分支 | `feat/claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` |
| 完整提交 | `b7ca36214da2e777544932cd290ffc3001d01842` | `8770624fe62d8b052e570ae7f03366c2c7e59e76` |
| 分析开始及写报告前未提交状态 | 干净 | 干净 |
| 任务预期来源 | 用户指定的同路径、同分支、`b7ca3621` | 用户指定的同路径、同分支、`8770624f` |
| 配套来源 | 本表 I | 本表 M |
| 预期产品特性 | drivers：claude / codex-remote / grokbuild / dsh-web / opencode-web；v2 summary 交付范围仅 Claude | 统一 ProjectionStore、gzip、缓存、anchor；仅 Claude 获本轮 summary 产品验收 |

已枚举双仓 worktree；未使用默认 main 工作树的 CordCode 源码。iOS `git merge-base --is-ancestor f0d0f523 HEAD` 返回 0，证明该提交是祖先；这不代表 native timeline 的其他性能命题也在本轮重验。

报告代码锚点采用 `M:相对路径:行` / `I:相对路径:行`，全部归属本表冻结源码。报告落盘只新增本文件，不改被审源码。

文档内容身份（SHA-256）：

| 文件（M） | 版本 / SHA-256 |
| --- | --- |
| `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` | v30 / `7dbc754a82193f8feb6c22e4789218c6c44f90614746dc8b4fa7dbc22bf2c810` |
| `docs/2026-10-19-s3-v2-summary-freeze-spec.md` | v3 / `14a359713c3b2dac3ccd7f146023204a1cd20d47cd96c42b9c9adfed092f3a95` |
| `docs/protocol/bridge-v1.md` | `c1742c218b3bb11eb399a29828c269b7cdc5fffb0e25ce6a44c40a4e864a8480` |

### 2.2 证据边界

- **源码事实**：本轮亲核调用点、能力协商、handler、producer、缓存、decoder、展示和明细入口；可以证明路径可达与特定输入下的代码结果。
- **既有验收**：Claude 真机通过来自 owner 本次输入，及 M:`think.md:47` 起的复盘 / `docs/2026-10-20-s3-v2-owner-acceptance.md` 记录；本轮未重做真机验收。
- **推断/待实验**：非 Claude 的实际首开/暖开耗时、错误实际发生频率、真实会话大小排名。本轮没有采集这四种后端的同条件运行样本，不能宣称“全部端到端已验证”，也不能宣称真实数据已在源端丢失。
- 本轮不是外部协议形状全量 audit；官方 checkout 只核语义背景，不能代替安装版本 wire 样本。没有把 synthetic spec fixture 当成非 Claude 生产样本。

**评审结论：REVISION_REQUIRED。**越界事实成立；“只加一行首开判断”作为完整修复不充分。建议修复单元必须覆盖新首开、已有摘要缓存和服务端 summary 入口；v2 超时名单遗漏另列局部缺陷。非 Claude 暖开维持“机制可达但未完成本轮验收”，不建议本轮顺手关闭所有通用优化。

## 3. 越界是否成立：完整判定链

### 3.1 iOS 实际入口与触发条件

| 命题 / 用户原引用 | 本轮源码摘录与实际锚点 | 判定 |
| --- | --- | --- |
| A：`ChatViewModel.swift:422-429` 所有 backend 返回 true | I 同文件 `:424`：`.codex, .codexWeb, .codexRemote, .claudeCode, .openCode, .openCodeWeb, .grokBuild, .deepSeek, .deepSeekWeb` → true | 定位、语义成立；但它只是准入集合，不单独证明 UI 已接管 |
| UI 还需要什么 | I 同文件 `:431-440`：UITakeover enabled、非空 sid、集合准入、`supportsSessionSyncV2` | 必须同时满足，不能把“枚举 true”写成无条件接管 |
| 所有这些后端是否真的调用缓存入口 | I:`ViewModels/ChatViewModel+SessionManagement.swift:844-885`：`useProjectionOnlyOpen = sessionSyncV2Active`，取真实 wireBackendID，调用 `openWithBaseCache` | 成立，非 Claude 并无绕路 |
| B：`ProjectionRestoreThenIncrement.swift:40-55` 无 backend 门 | I 同文件 `:45-49`：load 后 nil/空 rows，`fetcher as? SessionProjectionWindowV2Fetching`，`supportsProjectionWindowV2` → `pullWindowV2Summary` | 定位、语义成立；这个分支还早于 anchor 能力门 |
| “supports”是否查了后端 | I:`Services/Bridge/CCCodeBridgeBackendClient.swift:1327-1328`：只查 `transport.supportsAcknowledgedCapability("projection_window_v2")` | 没查后端；生产客户端在 `:34` 对所有实例声明 v2 fetcher 协议 |
| 是否真正发 v2 summary 请求 | I:`Models/ProjectionRestoreThenIncrement.swift:755-764`：`.window0`、`itemsView:"summary"`；I:`Services/Bridge/CCCodeBridgeClient.swift:565`：发送 `get_session_projection_window_v2` | 成立 |

所以 **codex-remote / grokbuild / dsh-web / opencode-web 均实际可达**：正常挂载且可用、SSV2 接管成立、缓存 nil/空（首次、schema 不符、损坏、LRU 淘汰、换 bridge 身份等）、生产客户端遵循协议、当前连接 echo v2。它不要求 session 已很长，也不要求用户先打开 Claude，更不要求 anchor echo 为 true。

若已有非空合法 full 基底，则本次不走 v2 首开，而走 anchor 暖开；若已有 summary 基底，则走 summary anchor 对账，见 §5。网络/水合错误可能阻止最终成功，但不改变请求已经越界的事实。

### 3.2 Mac 没有拦住

- M:`go-bridge/server.go:53-56`：`projectionWindowV2ProductionEnabled = true`；`:534-536`：客户端声明 + 全局生产门 + ack.Ok 即设置连接标记。`producer=claude` 只是注释。该函数没有 selected backend 输入。
- M:`go-bridge/projection_window_v2_handler.go:34-67`：检查连接 capability、itemsView、direction、limit；没有后端白名单。
- 用户指出的 `:94-96` 的确只是给非 Claude 提前启动 live relay；`:103-107` 还明确列入 Grok、DSH、OpenCode 等冷水合路径；`:178` 切窗，`:215` 对所有后端调用同一个摘要器。
- M:`go-bridge/handlers_projection.go:477-487` 接受这些后端的投影水合；`:1426-1455` 分别进入对应 producer。不是“handler 通用但生产不可达”。

冻结口径：M:`docs/2026-10-19-s3-v2-summary-freeze-spec.md:76-87` 写单门协商及 Claude producer；`:269` 明确“非 Claude backend 的 v2 producer（只留协议推广空间）”为非目标。canonical `bridge-v1.md:3243-3247` 同样限定 producer=claude。因此这是**发布/适用范围的真实越界**，不是可用同形 Go struct 自动证明的推广成功。

### 3.3 echo 不是 per-backend：规范存在范围表达缺口

冻结文本主动删去了 descriptor 第二门（spec `:80-87`），不能声称“规范本来要求每后端 descriptor echo，只是实现忘了”。目前能力是共享连接上的一个布尔值；同一连接可服务多个 backend。它表示连接能说该 RPC，并不能表达哪些资源已经实现安全的 summary→detail 闭环。

应补清楚“连接协商 + 本轮 producer 适用集合”的关系；保持现有 echo 的连接语义。若未来要 descriptor 化或返回 per-backend 能力集合，那是另一次明确的协议冻结，不能悄悄改变同名布尔值含义。

## 4. 逐后端：会得到什么、会坏在哪里

### 4.1 共同摘要算法决定的事实

M:`go-bridge/projection_window_v2.go`：

- `:109-135`：任何 `status != completed` 的轮次完整内嵌 liveState；completed 轮次只扫描 User/Assistant 的待权限标记和 pending user_input。**不扫描 System**。
- `:173-205`：userBrief 只取第一个非空 text；agentBrief 取最后一个 text，agentBriefHead 取第一个 text。不是理解语义的自然语言总结。中间 text、附件、item identity、presentation、fileChanges、detailInline/manifest 等不进入普通 descriptor。
- `:97-104,188-193`：Assistant 中除少量排除类型外全算“工具过程”。reasoning/file/workflow/context_injection/context_compaction 不是自动获得各自正确摘要语义。
- `:207-219`：有工具过程或 brief 超 128 字即 folded。System-only completed turn 不计算 brief、不计工具过程、也不进入例外，产出只有身份/时间的 descriptor。
- I:`Models/SessionProjectionWindowV2.swift:174-233`：liveState 原样返回；否则只重建 user/assistant text 和折叠文案，folded 的 detailLoadState=notRequested。System-only descriptor 因此得到 user/assistant/system 均空的展示 turn。这是特定形态下确定的缺展示链，不是重新发生全部 JSON 解码成空 turn 的旧 bug。

### 4.2 四个产品后端的判定

| 后端 | 已有投影/历史事实 | 越界 summary 的确定后果与条件风险 |
| --- | --- | --- |
| **codex-remote** | M:`agent/codex-remote/history_paginated.go:856-940`：paginated 首开一页 summary，页上限 30（`:31`）；进行中的轮次仍补完整 items（`:830-853`）。legacy 则 full read，并标 DetailPreloaded。M:`handlers_projection.go:1690-1794` 映射 text/reasoning/tool/context_compaction/workflow/SystemNotes；`:1771-1772` 透传 preload | 普通 completed text 会再次截到 128 字；workflow 被归入工具过程；System notes/命令可能不显示。paginated session 有 detail producer，但需要验证“二次摘要”与原 summary/overlay 的语义。**legacy full** 的 detailInline 会在普通摘要化时丢失，原本本地可展开变成远程请求，而 handler `session_turn_items_handler.go:130-139` 拒绝 legacy：存在明确不可展开分支。不能写“Codex 有 detail，所以没风险” |
| **grokbuild** | M:`agent/grokbuild/session_catalog.go:676-693` 读取 updates 展示历史；M:`handlers_projection.go:1839` 调用 rich history limit=0。M:`projection_reducer.go:412-435` 共享命令卡 System turn 明确含 Grok；普通 reasoning/tool/text 通过 rich mapper 进入同一 Kernel | 文本被截、reasoning/tool 折叠；命令 System 行会在普通 completed 分支失去展示。没有 session_turn_items producer，无法取回折叠全文。每 turn 一个执行进程不是历史有界的证据 |
| **dsh-web** | M:`agent/dsh-web/history.go:24-29,60-135`：50-message 上游分页，多页汇集；limit=0 在此变成 **500 mapped-entry 预算**。M:`handlers_projection.go:1973-1977` 传用户附件，`:2054-2088` 传 workflow/忙时 context injection，`:2133-2254` 传命令/空闲 context injection | completed 普通行的附件不进入 descriptor；workflow/忙时注入被标作工具过程；System 命令和空闲注入可变成空展示 turn。没有 detail producer，无法恢复被折叠的过程。权限标记/待答问题和非 completed 轮次有完整例外保护，不能声称所有权限都会丢；但 command 的 turn.Status 本来就是 completed，即使 CommandKind=running，也不在这条保护内 |
| **opencode-web** | M:`agent/opencode-web/history.go:34-51`：请求 message endpoint **没有 limit/cursor query**，limit 仅在收到列表后裁；`:123-179` 映射 text/reasoning/tool/file；`:207-230` 提供 turn-level fileChanges。M:`handlers_projection.go:1839` 调用 limit=0 | completed 正文截短、reasoning/tool 折叠，普通 descriptor 不携带 fileChanges；无 detail producer，全文展开不闭合。官方支持分页不证明本 adapter 已消费分页，也不证明 Mac→iOS 投影有字节上界 |

这里的“会丢/无法展开”指以上代码形态下的显示与可访问性后果；没有断言这些 owner 的每一个真实 session 都同时包含所有形态，也没有断言源端文件丢失。

**明细缺口的双面证据**：M:`go-bridge/session_turn_items_handler.go:64-75` 只接受 codex-remote / claude / claudecode；后端 descriptor 中 Grok 无 static detail（`agent/grokbuild/wire_descriptor.go:20`），DSH 只有其他能力（`agent/dsh-web/wire_descriptor.go:33`），OpenCode 只有 streaming（`agent/opencode-web/wire_descriptor.go:31`）。I:`Services/Bridge/CCCodeBridgeBackendClient.swift:252-253` 据 descriptor 算支持；I:`ViewModels/ChatViewModel+TurnDetail.swift:103-115` 拦入口/加载。因此既可能是“文案说展开却无入口”，也可能在强行请求后收到 unsupported；不能以按钮没显示来证明内容没被折叠。

## 5. 最小修复评审：一行能止新首开，不能完整止血

### 5.1 两个候选的取舍

| 候选 | 能解决 | 不能解决 / 纪律判断 |
| --- | --- | --- |
| **iOS：v2 冷开只允许 Claude** | 让正常 App 的非 Claude 缓存 miss 回到既有 full 路径；保留其 full 基底暖开，最直接消除 owner 体验回归 | `openWithBaseCache` 参数只有 backendId，没有 backendKind；不能凭空在该函数写 `backendKind == .claudeCode`。可以从生产 client 读取明确类型，或在恰当层传入资格；wire identity 仍用真实 backendId，不用 kind 猜路由。一行条件不覆盖已落盘摘要，也不约束其他客户端 |
| **Mac：v2 handler 拒绝非 Claude** | 从 producer owner 层限制范围，覆盖其他客户端；应在订阅、live relay、水合、snapshot fence 前拒绝 | 单独部署会使现 iOS 冷开直接报错，`pullWindowV2Summary` 的错误分支（I `:818-828`）没有“unsupported 后改走 full”的正式接线。不能靠新增隐式 fallback 掩盖。也挡不住普通 anchor RPC 的 summary 模式 |

**推荐完整单元：iOS gate + 定向旧缓存失效 + Mac 服务端范围门，先明确协议勘误/收口文本，再实施。**不是让 owner 在两处正确性守卫之间二选一。

服务端错误码：未协商连接继续 `protocol.capability_required`；已经协商但 backend 不在交付集合，优先用 frozen 已有的 `invalid_params` 并给明确 backend message、retryable=false。现有 v2 错误集已有此码（canonical `bridge-v1.md:3310-3321`），不必发明新码。把“能力已 echo、但该 backend 不支持”也说成“缺 capability”容易再次误导诊断。若选择 `unsupported_capability`，须把它列入 v2 冻结错误集，不得仅代码先改。

### 5.2 必须处理已有 summary 缓存

事实链：

1. I:`Models/ProjectionRestoreThenIncrement.swift:49-52,776-803`：越界首开成功后照样记录 summaryRows、flush。
2. 同文件 `:620-651`：每行保存 `turn + summary + token`；I:`Models/ProjectionBaseCacheStore.swift:33,108-120`：schema=3，只校验 schema 和 backend/bridge/session 身份，**没有“这个 backend 可以保存 summary”校验**；`:8` 无 TTL。
3. 恢复时 I:`ProjectionRestoreThenIncrement.swift:125-130,166-175`：任一 row.summary 存在就判 summary seed，发送 itemsView=summary；没有再检查 v2/backend 资格。
4. M:`go-bridge/projection_anchor_newer_handler.go:150-158,204-225`：summary 对账继续套同一摘要器，没有非 Claude 门，也没有在此处要求 v2 echo。

所以只改首开一行，已有用户仍可能一直看到截短内容。**修复应在恢复/发布之前拒用非 Claude 的 summary 基底，并通过现有权威 full 路径重建；完整 full 基底继续可用。**不建议全局清所有 backend 的缓存，也不要重装 App 当迁移方案。离线时无法重建，应诚实显示需要联网重新下载，不能把被截过的摘要伪装为全文。

同时检查同会话 state 的 summaryRows 清理：本快照中赋值点集中在 restore/summary apply，full apply 在 I:`ProjectionStore.swift:1200-1221` 重置 window/淘汰账本并再次 flush，却未见清除 summaryRows。迁移必须同时清内存摘要元数据及与之绑定的展示/明细状态，防一次 full 重新写回 summary 标记。

Mac 也应在 anchor `itemsView=summary` 分支实施相同 backend 范围和 v2 协商检查，避免另一 RPC 绕过首开门；**普通 full 形态 anchor 不应因这次修复一并禁掉。**

### 5.3 冻结纪律应怎样落地

这是收紧到已声明的 Claude 交付范围；不需要恢复已经裁掉的 descriptor 第二门，也不应把全局 echo 改成仅当选中 Claude 才返回。

但 spec `:236-237` 仍写“客户端 gate=v2 echo 单维度”，与非目标组合后缺少资源范围规则。需明确记录本次勘误：连接布尔值保持；producer 范围为 claude/claudecode；普通 anchor 与 summary anchor 的适用面不同；不支持资源的 typed 错误明确。canonical + iOS mirror 同步，评审通过后再实现。

冻结清单逐项对账：canonical 正文、两端模型/消费者、mirror、schema/types、冻结正负样本、schemaRevision/测试。若仅增加资源范围判断而 wire 字段完全不变，schema/types 是否需字节变化和 revision 是否需 bump 应按仓内规则在变更说明中明确 N/A 或记录修订，不能为了凑清单造字段；新增码/改变 capability 含义则必须重新冻结，不能称“一行无协议影响”。

## 6. 适用面矩阵：不能把所有传输优化揉成一个“已支持”

四档：**已生效**＝本任务有既有产品验收；**机制生效但未验证**＝生产接线可达，本轮没有该后端端到端证据；**不适用**＝冻结范围/产品挂载不适用；**不受益**＝明确限定场景下同类痛点不存在。无数据时不强行归为“不受益”。

| 产品 backend | gzip | 正常响应单次 typed 解码 | 75s + 看门狗宽限 | 恢复失败退避 | S-4 缓存 + S-7 full anchor | S-3 v2 summary |
| --- | --- | --- | --- | --- | --- | --- |
| claude | 已生效 | 已生效 | 已生效于 full/anchor/v1；**v2 漏名单** | 已生效于事件恢复 | 已生效（既有 owner 验收） | 已生效（既有 owner 验收）；另有 §7 局部缺陷 |
| codex-remote | 机制生效但未验证 | 机制生效但未验证 | 同左；v2 越界路径不获这两项保护 | 机制生效但未验证 | 机制生效但未验证；部分 Kernel 覆盖风险见 §7 | 冻结不适用；**实际越界已启用**。paginated 场景“不再全量拉工具过程”的同类收益原本已有，二次摘要必要性低；legacy 不能套此结论 |
| grokbuild | 机制生效但未验证 | 机制生效但未验证 | 同上 | 机制生效但未验证 | 机制生效但未验证 | 冻结不适用；实际越界，有无法展开/命令缺展示风险 |
| dsh-web | 机制生效但未验证 | 机制生效但未验证 | 同上 | 机制生效但未验证 | 机制生效但未验证；部分历史边界需验证 | 冻结不适用；实际越界，有无法展开/附件与命令等缺展示风险 |
| opencode-web | 机制生效但未验证 | 机制生效但未验证 | 同上 | 机制生效但未验证 | 机制生效但未验证 | 冻结不适用；实际越界，有无法展开/文件摘要缺展示风险 |

矩阵说的是 **CordCode iOS 经 Bridge 的投影打开**，不代表优化了各工具自己的 Mac/Web UI，也不代表加快模型生成。

依据及限制：

- gzip：M:`go-bridge/server.go:156-185` 是连接通用的 `SendJSONReport`，无 backend 条件，≥32KiB 且压缩更小才用；I:`Services/Bridge/CCCodeBridgeTransport.swift:183-188,1638-1659` 声明/解压 direct gzip。Relay 使用独立 relay_gzip_v1（同文件 `:171`）；不能把 direct 的测量数字外推 Relay。小于阈值的 KB 摘要不压缩是预期，不等于未接线。
- 单次解码：I:`Services/Bridge/CCCodeBridgeTransport.swift:1788-1794` result/error 按路由头交 raw data；I:`Services/Bridge/ProjectionWireDecoder.swift:16-40,54-57` 正常信封 typed decode，backend 无关。错误/兼容形状有第二次 decode 尝试（`:23-27`），不能字面宣称每个异常帧也永远只 parse 一次。
- timeout/watchdog：I:`Services/Bridge/CCCodeBridgeTransport.swift:113-133` 按 RPC 方法而非 backend 选择；`:390-399` 以家族在途计数进入 90s 档。只列 full/anchor/window v1，未列 window v2。
- 退避：I:`Models/ProjectionStore.swift:177-195`：事件驱动 scheduleRecoveryPull 的 5/10/20/60s，**用户打开不受此门限制**。I:`ViewModels/ChatViewModel.swift:449` 起还有灾难恢复循环；不能写每个请求都有同一个退避。
- 暖开：§3 的统一入口 + I:`ProjectionRestoreThenIncrement.swift:60-115,134-218` 的协议/anchor echo/cache gate、恢复、对账、fallback/失败处理。缓存上限 4MiB/256 turns、总量128MiB（I:`ProjectionBaseCacheStore.swift:53-57`），没有 backend 排除。命中缓存不承诺完全离线完成权威对账，只是先恢复可读内容。
- 当前产品集合来自 M:`MacBridge/MacBridge/Services/RuntimeManager.swift:216`。iOS 中 codex/codexWeb/openCode/deepSeek 旧枚举仍 true，但对应 legacy 产品默认未挂载（M:`CLAUDE.md:595-614`）；对普通 owner 均记“不适用”，不得把枚举存在写成这些退役产品已经提速。显式重新挂载的 codex/codex-web 也需同样范围审计，不能自动推广。

## 7. 额外断点与验证优先级

### F-01（P1，确定）：非 Claude 的新开及缓存恢复均可进入未交付 summary

证据：§3–5。后果不仅是性能未测，而是 detail 闭环不存在、部分结构被消去。闭合标准：四个非 Claude 后端在 v2 echo=true 的正常冷开不得发 v2；预先存在的 schema3 summary seed 不被恢复/续写；full seed 仍走普通 anchor；Mac 两个 summary 入口均拒越界，Claude 不回归。

### F-02（P1，确定）：window v2 没有 75s 超时/大传输看门狗宽限

证据：I:`Services/Bridge/CCCodeBridgeTransport.swift:113-133` 无 `get_session_projection_window_v2`；实际发送见 I:`Services/Bridge/CCCodeBridgeClient.swift:565`。因此该方法走默认 30s（transport 同文件 `:97`）且自身不贡献 large-RPC 在途计数（依赖 `isProjectionFamilyMethod`）。同连接碰巧另有 full/anchor 在途可能临时提供宽限，但不能依赖它。

summary 常很小不等于风险不存在：M 摘要器 `:110-111,248-255` 把非 completed 轮次完整内嵌，running/error/aborted 大轮次都可能大；limit 也不是此例外的可靠网络耗时界。需把 v2 纳入同一方法分类并补方法名单单测；本轮只提出，不执行。owner 已有成功验收不反证这条慢网分支安全。

### F-03（推广门，源码可证风险条件，实际频率待测）：部分 Kernel 不能直接当全量存在性真值

M:`projection_anchor_newer.go:174-177,209-235`：anchor 不在当前 Kernel 就 stale；baseDigest turn 不在 Kernel 就返回 removed。Codex paginated 冷水合只有最新 30 turns（§4），DSH 又有500-entry截取；若之前手机缓存/Kernel 已含更早窗口，而 Mac 重启仅重建尾页，**未水合不等于源端删除**。

条件推导：缓存 tail 仍在新尾页、部分旧 digest 不在尾页 → removed；tail 也离开尾页 → typed stale/full。现代码不区分前者与源端真删除。这是不能把“通用 anchor handler”升级为“所有后端暖开已验收”的重要理由。需真实重启/翻旧页/新增长历史组合证据；未在本轮声称现场已经误删，影响是手机展示/缓存窗口，不是删上游原始数据。

### F-04（推广门）：还未证明“上游分页”消除了手机全量传输

- DSH `history.go:81-119` 多页汇集后统一映射，不是一页上游就一页手机；消息数边界不控制工具块/长文本字节数。
- OpenCode adapter 无分页 query，收到响应后才裁；安装的 API generation 是否默认分页、是否返回游标要单独抓样本。当前官方 checkout 已有新分页 envelope（见 §9），不能跨版本反推本机生产服务全量或完整性。
- Grok per-turn 是进程生命周期，history 仍 limit=0 重建；不构成“不受益”的依据。
- Codex paginated 首开已是有界摘要，但长文本、legacy、进行中轮次仍另算；不能以一个30-turn上限承诺所有响应都很小。

### F-05（P1，确定，范围协商的另一处偏差）：v2 echo 不继承 v1 声明及运行时 rollout 门

冻结文本要求 v1 声明 + SSV2 + 服务端 rollout（spec `:76-78`、canonical `:3243-3245`）。实际 M:`go-bridge/server.go:480-488` 只检查 v2 的 SSV2 前置；`:500-502` 在没有 v1 声明时仍调用 echo 函数；`:514-516` 的 `s.projectionWindowEnabled` 只门控 v1 echo；`:534` 的 v2 echo 用恒 true 的生产常量，没有检查 v1 声明或这个运行时开关。因此 `SSV2+v2、无v1` 或运行时 window 开关关闭仍可获得 v2。它不影响正常 iOS 总是同时声明 v1 的可达性结论，但意味着不能依赖“关闭 v1 rollout”来停 v2。补测应调用真实 negotiate 函数而不是只测 helloSupports 帮助函数（M:`projection_window_v2_test.go:203-215`）。完整修复应对齐已冻结条件；不能只改注释。

## 8. 推广成本与补测方案（本轮不执行）

推广属于 D3 状态/协议工作，不是把客户端 if 放开。暖开代码已通用，不需要重写一套；真正成本在证明来源覆盖、修正语义和跨 epoch 对账。

### 8.1 每个后端都需要的交付

1. **真实证据**：绑定实际安装版本/运行身份，采同会话 cold/重开/重连/运行中/权限或问题/错误/中止/长正文/工具/附件/系统行；记录上游→Kernel→wire→iOS 的 turn/item 数与字节数。未知形态只登记待验证，不用 fake 补事实。
2. **协议冻结**：说明该 backend 的 summary 保留/折叠/always-visible 集合；canonical、mirror、types/模型、冻结样本、schemaRevision 按实际 wire 变更成批审查。保持 kernel 唯一真值、同一 fence、turn generation 和按连接 overlay 身份。
3. **明细闭环**：完成或复用真实 producer：completed 资格、source cut、generation、页/byte/时间上限、失败 typed 状态、chunks 和控制队列、取消/重连；不能先折叠再说“明细以后做”。
4. **生产 decoder 契约**：冻结 JSON → iOS 生产 decoder → displayTurn/mapping → 折叠入口；同时测错误、full 判别形态与 liveState。不要只给 fake 直接构造 Swift 对象。
5. **暖开对账**：full/summary seed、live 修正、删除、跨 epoch、部分源覆盖、旧页/截断、缓存淘汰、离线/恢复网络；不要只测试“没新增轮次”的稳定 session。

### 8.2 差异工作与优先级

| 后端 | summary 推广新增工作 | 暖开重点 | 成本/收益判断（推断） |
| --- | --- | --- | --- |
| codex-remote | 已有 chunks producer，但必须分别保全 paginated/legacy：detailInline、不再重复截正文、System notes/goal、workflow、manifest；不能用统一128字方案直接覆盖两模式 | Mac重启只恢复尾页时的 digest 存在性；展开过明细的 token/generation | 明细设施新增较少，但语义集成非零；已有摘要方案，同类新增收益最低，先修越界再测 |
| grokbuild | 建 turn_detail producer（从官方展示 journal 真值归属），验证工具/goal/workflow/command/权限/问题、rewrite/compact 身份 | 全历史重建成本、外部 turn catch-up、命令边界与跨epoch | D3较大；无现成摘要分页，长工具会话潜在收益较高 |
| dsh-web | 建 detail producer（真实 session/page cut/turn 边界）、附件/command/context_injection/workflow 专门摘要语义，不把它们一律叫工具 | 500-entry边界、早期 turn 未读与真删除、pending queue/撤回/跨epoch | D3较大且内容种类多；已有上游分页但非字节限额，潜在收益仍高 |
| opencode-web | 先证明实际服务代际/分页调用；建 detail producer、tool/fileChanges/图片/权限/问题保留规则，按官方 message/item 身份拉完整内容 | 无query冷拉的代价、分页截断完整性、SSE与冷拉一致性 | D3较大；应先测上游拉取 vs 手机传输，v2只减少后一段，不能代替上游分页修正 |

**不能给真实速度或工期承诺**：无四后端同链路对比，也未给每个 producer 做实施设计。按静态架构先查 OpenCode/Grok，DSH紧随，Codex靠后；最终按“Mac水合/入队耗时 + 实际压缩字节数 + 手机收帧耗时”重排。小会话一律不承诺显著受益。

### 8.3 最小修复的必要回归（未来执行）

- I：扩展 `ProjectionWindowV2ConsumptionTests`，使用生产 backend 资格入口，参数化四个非 Claude + Claude：echo true/false、empty/full/summary cache。现文件 `:87-115` 的 open 测试只用 backendId=claude，漏掉范围负例。
- I：旧非 Claude summary seed、同进程已有 summaryRows 后 full 重建、离线拒用/联网恢复，证明不再写回 summary 标记；Claude schema3 暖开仍携带 summary。
- I：`ProjectionWindowV2WireContractTests` 继续用 frozen JSON 喂生产 decoder，不回退 fake-only；补 `isProjectionFamilyMethod` 对 window v2 的 timeout/watchdog 断言。
- M：v2 handler 四后端拒绝 + 无订阅/水合副作用；anchor itemsView=summary 同样拒绝；full anchor 仍可用；未echo与错误码分开；direct/relay 协商保持连接布尔语义。
- M：为每个准备推广的后端新增 system-only、attachments-only、多个正文片段、workflow、live permission/user_input、legacy inline 等 producer 真样本契约测试；Grok/DSH/OpenCode 在 producer 完成前不开放 summary。
- 真机验收另行在实施交付时完成；本轮没有运行测试，更没有把上述待测清单写成已通过。

## 9. 上游核验范围与可复验入口

上游只读 checkout 均干净；它们不是 CordCode 配套工作树，也不是已证明与本机运行版本一致的源。未据此宣称活体 wire 验证。

| 产品 / 路径 | 分支 / 完整提交 | 本轮定点核验 |
| --- | --- | --- |
| Codex `/Users/jacklee/Projects/codex` | main / `18795fc12571a3530d3e4ac18366e41cd2f57448` | `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:507-509`、`:1753-1755` 定义默认 summary；`app-server/src/request_processors/thread_processor.rs:3386-3396` 对应处理。符合“原有摘要先行”背景，不证明当前 Desktop 版本 |
| Grok `/Users/jacklee/Projects/grok-build` | main / `2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8` | `crates/codegen/xai-grok-shell/src/session/export.rs:3`、`session/helpers/replay.rs:4,77` 明确 updates 展示真值及重放；该文件 `:759` 起有临时目录重放测试，不运行 |
| DSH `/Users/jacklee/Projects/deepseek-harness` | master / `687f282e8625c6b8d11ff58211a90524be2e4317` | `packages/api/session-controller/src/history.ts:100-110,395-426` 消息计数分页并返回 hasMore；同包 `tests/history-records.client.spec.ts` 的记录投影测试已检索，未运行 |
| OpenCode `/Users/jacklee/Projects/opencode` | dev / `40cdf4fbdd9d60316f339113e7f9c9a121aece4a` | `packages/protocol/src/groups/message.ts:23-42,70-87` 定义 limit/order/cursor 和 data/cursor envelope，`packages/server/src/handlers/message.ts:33` 注册处理；这与旧 adapter 注释的1.18代际不能混为同一版本 |

以下只读命令可复核关键结论（不用 build，不执行会话动作）：

```bash
M=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
I=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
git -C "$M" branch --show-current
git -C "$M" rev-parse HEAD
git -C "$M" status --short
git -C "$I" branch --show-current
git -C "$I" rev-parse HEAD
git -C "$I" status --short
nl -ba "$I/OpenCodeiOS/OpenCodeiOS/Models/ProjectionRestoreThenIncrement.swift" | sed -n '31,90p;125,175p'
nl -ba "$M/go-bridge/projection_window_v2_handler.go" | sed -n '16,114p'
nl -ba "$M/go-bridge/projection_window_v2.go" | sed -n '97,225p'
nl -ba "$M/go-bridge/session_turn_items_handler.go" | sed -n '64,75p;120,139p'
nl -ba "$I/OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift" | sed -n '91,133p'
rg -n 'summaryRows' "$I/OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift" "$I/OpenCodeiOS/OpenCodeiOS/Models/ProjectionRestoreThenIncrement.swift"
rg -n 'GetRichSessionHistory\(ctx, sessionID, 0\)' "$M/go-bridge/handlers_projection.go"
nl -ba "$M/agent/opencode-web/history.go" | sed -n '34,51p'
nl -ba "$M/agent/dsh-web/history.go" | sed -n '60,119p'
```

为严格遵守“只分析、不构建”，未运行 Swift/Go 单测（两者都可能触发编译），未做新 wire 采集。报告验证仅 `git diff --check`、来源复核与静态锚点检查。下一轮实施需要的测试列在 §8，不能用本报告替代。
