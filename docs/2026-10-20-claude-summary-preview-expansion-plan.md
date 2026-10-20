# Claude 首屏正文预览扩容补充方案 v1

日期：2026-10-20。状态：待独立评审；通过前不改业务代码。
本轮范围：已交付 S-3 v2 的正文预览调参 + 直接相关的缓存形态保真与一次升级；不新增 RPC/数据通道。

## 1. 需求与完整交互

owner 原话：「Claude code 模式下，刚打开十几秒都是那种简略的会话……过一段时间会加载完整的各种工具调用，思考过程，能不能再开大点」。

- R-1：点开 Claude 会话，无额外输入，仍发既有 v2 summary 请求；首屏提问/回答给到 1024 Unicode 码点（原128），回答开头给到256（原64）；短于上限的正文完整可读，超长保持真实截断标记。
- R-2：同一正文头部是 brief 的前缀时只呈现一次；不同首段/末段仍用省略号区分。工具/思考保留原按需展开入口：点击 → session_turn_items/chunks → 原有明细呈现；不承诺自动全量补齐或改变明细下载耗时。
- R-3：冷开、状态 patch、缓存 flush、重开 anchor 对账都保留 summary 形态；升级后不让旧128字种子长期命中。Claude 一次重建；其他四 backend 缓存继续命中。
- R-4：继续≤1MiB摘要软预算、现有窗口turn上限、always-visible原样、SSV2唯一writer、scope门；暂不push。

这是既有行为的局部优化，owner已提出执行需求；本方案独立评审只切分设计/开发阶段，不另造owner审批。

## 2. 来源清单与事实

本次亲核：2026-10-20 21:39，首次修改前复核21:51（UTC+8）。

|别名|绝对工作树路径|精确分支|完整提交|未提交状态|任务预期/配套|
|---|---|---|---|---|---|
|M|/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading|feat/claude-long-session-projection-loading|7eba7e33d7a3852fff86bd4fd44b90a53cb3824e|干净（写本方案前）|本任务Mac；配I|
|I|/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading|feat/claude-long-session-projection-loading|8a813c47b4303916eef83dd2f855d5c8963ca397|干净|本任务iOS；配M|

预期产品特性：claude/claudecode 独享 projection_window_v2 summary；codex-remote、grokbuild、dsh-web、opencode-web沿既有完整缓存；不恢复退役backend。

官方来源：Claude闭源，无可登记的官方CLI源码commit。2026-10-20本机PATH CLI实测2.1.300；官方文档 https://code.claude.com/docs/en/agent-sdk/typescript 的 SDKAssistantMessage.message: BetaMessage、getSessionMessages(): SessionMessage[] 是消息/历史参考契约；文档为本轮在线读取的滚动版本，不冒充2.1.300发布快照。M/GO_BRIDGE_ARCHITECTURE.md:204-233、M/agent/claudecode/transcript_shapes_test.go:10-18、:91-116及脱敏fixture界定JSONL无官方schema承诺。现有Phase0证据包为2.1.261，不冒充本机现版。本次不改CLI调用或外部解析；128/64来自CordCode私有策略（M/go-bridge/projection_window_v2.go:16-23、:173-204），无需另造外部协议。官方没有提供本次要复用的CordCode投影/缓存实现；检索范围仅上述SDK文档与已有证据包。

原始证据与解释分开：

- owner提供的既有HEIC截图（21:35:53）显示句中截断、重复开头及「本条内容较长 · 2段工具过程 · 17KB · 展开查看」。只分析owner图片，无截图/录屏/设备操作。
- 本轮只读拉取的iPhone16 `Documents/fg-trace.log` 存于本机临时证据 `/tmp/s3v2-owner-2135-fg-trace.log`。21:35:22，7afc3bd7 summary100行、payloadBytes34512、folded72、mapped121；21:36:04恢复路径发get_session_projection(sinceRev13488)，21:36:15同rev完整36,371,146B到达，FULL144轮。Mac原始日志：21:36:04.642入队。约10–11秒是这次大帧在途，不是编码/写盘。
- 源码解释：I/ChatViewModel.swift:1809、:1845、:1915（路径为OpenCodeiOS/OpenCodeiOS/ViewModels/）与日志对应foreground recovery；现有日志不能唯一识别哪个恢复信号。没有summary后固定43秒自动补full的策略。恢复全量路径本轮不改，不把预览扩容宣传为消除大帧传输。
- 只读归档JSON checkpoint估算（不是当前运行投影，也不是新协议样本）：7f389d08@2026-10-19T03:18:02+08，47轮：完整正文44/69→64/69，估算行JSON21488→40955B；7afc3bd7@2026-10-18T12:33:31+08，尾100/121轮：69/96→86/96，37459→75967B。估算仅用于选择调参；实施后用生产summarize函数重新测量。JSON字段/metadata/例外未完整纳入估算，不当作wire实测。

|现成能力|源码锚点（均相对所列别名）|复用/扩展|
|---|---|---|
|summary生产/anchor翻译|M/go-bridge/projection_window_v2.go:summarizeTurn/summarizeWindowTurns；projection_anchor_newer_handler.go:211-234|只扩字数，维持字段、预算降级与token含义|
|summary窗口选择|M/go-bridge/projection_window_v2_handler.go:192-204|更新1536B旧等价尺寸为10KiB常量（256轮亦<4MiB），不让full字节预算裁摘要窗口|
|摘要展示|I/OpenCodeiOS/OpenCodeiOS/Models/SessionProjectionWindowV2.swift:174-210|复用并去除已包含的head前缀重复；不隐藏不同内容|
|本地基底存储|I/.../Models/ProjectionBaseCacheStore.swift:28-51、:108-120|可选Claude局部revision，schema3与其他backend保持|
|形态保真|I/.../Models/ProjectionStore.swift:1362-1372；ProjectionRestoreThenIncrement.swift:146-166、:648-672|commit保summaryRows；明确full替换仍主动清除|
|对账token|M/go-bridge/projection_anchor_newer.go:134-147、:232-270|继续full内容token，不能用它检测brief策略升级|

本次是内部策略调整，不新增外部内容类型、字段抽取、RPC、capability或backend，免外部格式专项；既有判别联合、真实wire消费测试仍回归。所有受影响面=Claude summary window + summary anchor + iOS展示/本地缓存；其余backend明确不进入summary（保留已有scope测试）。

## 3. 拟议设计与边界

S-1（R-1/R-4）：summaryBriefMaxChars=1024，summaryBriefHeadChars=256；仍取用户首text及助手最后text part的前N码点。用集中常量10KiB给summary窗口做等价尺寸，100/256轮均不被4MiB full预算提前裁切；实际摘要预算仍由summarizeWindowTurns执行，always-visible例外不计入且原样。不变更冻结样本字段集、major/schemaRevision（wire字段与含义不变，扩大的是策略阈值）；canonical bridge-v1.md/schema注释和I mirror同批更新，冻结v3原文保留并追加本补充方案的替代说明，不无声覆写历史。

S-2（R-2/R-3）：iOS displayTurn若head==brief或brief.hasPrefix(head)则只显示brief；非前缀的首末段保持head+省略号+brief。commitIfObserved把summaryRows与window/baseCache一起保留；已明确处理full的路径仍清summaryRows，不保留到full形态。

S-3（R-3）：ProjectionBaseCacheSnapshot末尾加可选本地字段claudePreviewRevision:Int?（默认nil，Codable对旧文件兼容）。当前值=1。flush仅在identity.backend==claude/claudecode写1，其他写nil；load身份/schema校验后仅对Claude要求1，旧文件视miss但不预删除（成功后原子覆写）。范围包含旧Claude full种子，因为历史summary可能已误标full，禁止按内容长度猜形态。其他四backend不加revision门、不清目录、不全局bump schema3。升级后的首开一次summary重建，后续保形态暖开；连接旧Mac时沿现有无v2路径，仍正确但无法凭新iOS使旧Mac生产1024字。本地revision不是服务端策略协商，不冒充它。

真相owner/唯一writer：Mac committed Kernel → 既有window/anchor → iOS replica/ProjectionStore。新增的只有本地缓存兼容字段，不是timeline事实。受影响事务域=已存在的summary pull、patch提交、restore/anchor、flush。失败沿既有typed错误与离线快照显示；不新增全量fallback、定时器、重试或第二writer。4MiB/256轮、全局128MiB本地预算保持。

OD：无待owner裁决；1024/256是根据样本选择的本轮工程默认，工具/思考按需展开沿owner已批准A+③。

## 4. 门、验证和完成证据

|E|状态/来源|实施后断言与负例|缺项停止点|
|---|---|---|---|
|E-1|verified：上述双仓身份/官方SDK边界/原始日志|修改与build前再核验来源；不把CLI2.1.261证据说成2.1.300|来源变动即停编辑/安装|
|E-2|pending，内部producer定向测试|600字不截、1025字截到1024；Unicode码点边界；head256；工具/running/权限例外；超预算老轮降级；48/100/256轮不被full预算裁摘要|S-1完成门|
|E-3|pending，I组件测试|前缀不重复、独立首末段保留；cold summary→state-only patch→flush/load→clear→warm anchor→flush/load保持summary；显式full后清summary；full请求计数为0|S-2完成门|
|E-4|pending，本地缓存测试|Claude旧revision缺失（summary及误标full）miss；revision1命中；非Claude原schema3无revision仍命中；不删除旧文件/其他key；其他后端scope负例|S-3完成门|
|E-5|pending，生产summarize对归档真实投影的组件测量|报告输入/输出turn计数、完整正文数、摘要实际JSON与gzip字节；区别归档与21:35生产实例；不泄露正文|调参完成门|
|E-6|pending，D3定向验证+部署|定向Go组；iOS Consumption/BaseCache+wire contract相关用例（repo fixture仅模拟器必要时）；稳定DerivedData/定向only-testing/5分钟watchdog；无UI自动化|安装前必须通过|

Gate A=E-1满足；E-2…E-6是实现后才能完成的收口门，失败修同一主路径。Gate B=本方案独立APPROVED+owner既有授权，两者齐备后才开发。本轮未改任何产品代码或运行build/test。

实施顺序：S-1→S-2/S-3 coherent edit→统一定向验证→双仓各自commit+CHANGELOG（暂不push）→Mac一次Release覆盖安装、后台重启与新PID/8777/版本身份核验→I一次增量真机构建安装iPhone16。复用已有/tmp/s3v2-mac-dd、/tmp/s3v2-dd；不clean、不创建新DerivedData。

Mac启动不得用open/activate抢屏：使用已装/Applications包后台启动方式；如无法无侵入启动，降低到产物安装验证并诚实列运行态未证，不为验证违反全局硬约束。禁止运行临时App、agent-device、截图/录屏、UI test。

owner人工一次确认：Claude点开较长会话→首屏正文更完整→点展开能看工具/思考→回列表重开仍即时且不退成旧128字；其他backend此前已通过，本轮仅组件范围门保证，不要求owner重测全矩阵。

已知旁支不并入本轮：foreground recovery使用full大帧；连接detail overlay尚未全路径持久化。它们有独立拥有层，预览扩容不能声称已解决其时延/离线问题。
