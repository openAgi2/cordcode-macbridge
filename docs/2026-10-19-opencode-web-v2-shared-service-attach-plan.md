# OpenCode v2 独立接入与共享服务设计方案

- 版本：v1.8（r6 通过后建议收编版）
- 日期：2026-10-19
- 状态：**r6 完整复审通过（APPROVED，0 阻塞/2 建议级）**。评审对象为 v1.7，SHA-256
  `37ddbaf09e1d4510fd9535232f845e844808f545168bfbd2bc2bf11ad0c7bd47`，报告
  `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r6.md`；通过
  覆盖 v1.4→v1.7 全部累积改动（§3.0/§3.11/E-11a/b/c、手动启动链、桶隔离），且
  「通过≠授权」。本版 v1.8 仅收编该两条建议级意见并回写评审状态，不改变任何设计
  命题、门控或验收标准；按 r6 闭合标准（与 F-4/F-15 同级处置）无需再轮评审。仍未
  授权实施。
- 版本史：v1.0 初版 → v1.1（r1/F-1~F-9，A.8）→ v1.2（r2-meta，A.9）→
  v1.3（r3/F-14，A.10）→ v1.4（r4/F-15，A.11；r5通过）→ v1.5（手动启动与目录
  隔离，A.12/A.13）→ v1.6（owner指定`agent/opencode-v2/`、ID=`opencode`，A.14/A.15）→
  v1.7（禁止新包及其公共依赖反向依赖旧包，A.16；r6完整复审通过）→
  v1.8（收编r6 F-16/F-17并回写评审状态，A.17）。
- 本轮改动范围：①头部/Gate C/阻塞清单回写 r6 完整复审通过结论；②F-17：§5 S-1
  依赖列「选择/身份按S-7」改为「选择/身份按E-11a（S-0冻结契约）」；③F-16 按本任务
  HEAD 亲核结果处置并在 §3.11 补精确子锚点（A.17）。R/OD/§3 设计命题、E/S 门控与
  验收矩阵零改动；除收编两条建议外不做其他设计变更，避免超出 r6 通过面。
- 文档文件名为既有交接入口而保留；标题与正文身份以本版为准。

---

## 1. 摘要与范围

### 背景（一句话）

OpenCode Desktop 2.0.32 已用官方「共享服务」模型（CLI/desktop/web 连同一个
`serve --service` 常驻进程，注册于 `~/.local/state/opencode/service.json`），而
MacBridge 的 opencode-web 仍指向自管的 v1 `opencode serve`（4096）。「一库两服」
已被 2026-10-18/19 运行取证证明造成三类实际断裂（附录 A）：流式不同步、历史
不可见、catalog 聚合回归。本方案让 MacBridge 以官方 attach 协议接入 v2 共享
服务。实现新建 `agent/opencode-v2/`，对外 backend ID=`opencode`，显示名为 OpenCode；
现有 `agent/opencode-web/`/ID=`opencode-web` 仅负责 v1。未运行时让用户在新的 OpenCode 行
手动启动，缺少可用 v2 时给出安装命令；不自动启动或回退到 4096。旧 v1 安装、配置和数据
保留，旧 driver 在新 v2 稳定验收后才按退役门下线。

### 需求（R）

| ID | 需求 / 验收结果 |
| --- | --- |
| R-1 | v2 共享服务在场时，MacBridge 优先 attach 它（不自建、不争抢）；desktop/CLI 在该服务上发起的 turn，iOS 实时流式可见 |
| R-2 | iOS 可列出并读取 v2 会话与消息历史（即 v2 投影 `session_message`，含 2026-10 以来 desktop 写入的内容） |
| R-3 | iOS 可在 v2 会话上发消息（文本/图片/文件附件）、中断 turn、应答 permission/question |
| R-4 | OpenCode（v2）行管理 v2 生命周期：未运行且已安装→“启动”；未安装→“未安装”＋准确安装命令/复制入口；点击后显示启动/接入过程与真实失败。打开 App、开启接入、重启 bridge、服务掉线均不自动拉起 v2 或回退 4096 |
| R-5 | catalog 按目录隔离失败：返回成功目录的完整分页结果并显示部分目录不可用的告警；单目录失败不阻断其余目录，全失败不得冒充空会话 |
| R-6 | 安全边界：service.json 密码不进 argv/日志；attach URL 强制 loopback；凭据随服务替换而刷新 |
| R-7 | v2 在独立 `agent/opencode-v2/` 实现，注册/广告/路由 backend ID=`opencode`；不依赖或复制最老 opencode/现有 opencode-web 的请求、事件、历史及状态机；两端显式识别新语义 |
| R-8 | v2 通过同版本样本、SSV2回归、双端实际运行与owner矩阵后才退役 `opencode-web`；不把旧 `opencode`/`opencode-web` 的投影、缓存、pins或端点无条件迁入新v2，不删除上游历史或卸载旧程序 |

### 非目标

- 不另造 timeline/传输协议；沿用既有 bridge-v1/SSV2 内容事务，但 **backend 身份含义改变必须同步 canonical pack、iOS模型/路由/缓存与remote-web消费**，不再声称wire或iOS零改动
- 不恢复最老 `agent/opencode` 的实现、旧 proxy语义或deprecated注册；重新使用 `opencode` 字符串只指本版独立v2，由owner本轮明确裁决
- 不做 v2 remote-access / pairing（`/api/pair`、`remote.route`）接入
- 不修复上游 v2 自身缺陷（迁移坏行、v1 插件断代）；只保证 MacBridge 链路对它们的容忍边界
- 不做 v2 自动冷启动/保活拉起、自动安装/升级，不因服务缺席启动 4096；**用户点击“启动”后的官方 v2 启动属于本期必交主路径**
- 不复制整个 Agent 拓扑改版或新增第二套 inventory/selection/supervisor；只扩展已有 OpenCode（v2）行与专用动作
- 不卸载、删除、自动停止现有 v1 或 v2 共享服务，不改上游配置/数据库；关闭接入和退出 CordCode 仅断开本客户端

### 验收标准（汇总）

1. desktop 2.0.32 开着共享服务时，CordCode Link 的 OpenCode（v2）行显示 attach 状态
   （来源=shared service，非 4096）；iPhone 端实时看到 desktop 发起的 turn 流式输出。
2. iPhone 端能打开 v2 会话并看到 2026-10-18/19 由 desktop 写入的消息（附录 A 场景复测）。
3. iPhone 端在 v2 会话发消息 → desktop 同步看到；中断/permission 应答可用。
4. 有可执行 v2、共享服务未运行时，OpenCode（v2）行显示“未启动”＋“启动”；用户点击后
   “启动中”→“正在接入”→“已接入 · 连接正常”。服务缺席期间和启动失败后均无自动拉起/4096
   回退。只有 v1 时显示“OpenCode v2 未安装”及 v2 安装命令，v1 保留；版本探针失败显示
   “检测失败”，不冒充未安装。复制命令不执行安装，安装完成重新检测后仍需显式启动。
5. 已接入服务真正停止后，行收敛为“未启动”或具体失败并重新提供适当动作；外部再次启动
   同一官方服务，或 id/pid/password 轮换后，可自动重附现有服务（只读发现，无 spawn）。
   普通 descriptor 失败不触发整 bridge 重启；只有经校验的新 endpoint 身份需应用时才走一次
   安全配置重启，忙碌时显示等待；防抖限速见 §3.2。退出 desktop 本身不被当成服务已停止。
6. 含 `~/Documents/todo-app-sisyphus` 等失败目录时，其余目录正常分页、列出与刷新；Mac 行显示
   “部分目录不可用”与失败数/原因；失败目录恢复后下一轮正常扫描恢复。全失败与空 registry
   分别显示加载失败和真实空列表，直接打开失败目录/会话时错误不被伪装为成功。
7. `ps`/日志中无 service.json 密码明文；attach URL 非 loopback 时拒绝。
8. 源码实现为独立`agent/opencode-v2/`，对外ID/kind=`opencode`、内部generation=
   `opencode-v2`。新driver无旧包import/复制状态机；新旧客户端、旧proxy、旧target/
   checkpoint/pin负例通过；未协商新语义的连接不能读写新backend。
9. 旧`opencode-web`在v2稳定证据和owner矩阵未完成前仍保留；退役只在§3.12门后执行，
   不卸载v1或停止现有4096，不自动迁入旧历史。

---

## 2. 来源与现状调查

### 2.1 参与仓库来源表（P0 来源清单）

| 仓库 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 | 任务预期来源 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（本工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 跟踪文件干净；7个未跟踪文档逐项见附录C；v1.6于11:20分析门/11:22首改门重新核实（附录D） | 本任务唯一写入工作树；业务源码锚归本提交；目标新增`agent/opencode-v2`/`opencode`官方v2与跨端身份隔离 |
| opencode（v2 上游，专用 checkout） | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61`（2026-10-16） | 干净（2026-10-19 纠错轮亲核：porcelain 空） | v2 官方实现唯一锚点 |
| opencode（旧 checkout，仅备注） | `/Users/jacklee/Projects/opencode` | `dev`（2026-10-19 复核：HEAD→dev，非 main） | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | 干净（2026-10-19 纠错轮亲核：porcelain 空） | **不作为本方案锚点**，避免来源混淆 |
| cordcode-ios（任务配套，实现期修改对象） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52` | 干净，11:20/11:22两门亲核 | 唯一同分支族且与已有任务来源一致，配套为本表Mac行；目标新opencode识别/Provider/target/cache/pin/SSV2与remote-web同步，本轮只读源码、只改Mac方案文档 |
| cordcode-macbridge（owner指定旧接入规则文档，仅只读参考） | `/Users/jacklee/Projects/cordcode-macbridge` | `main` | `ea0627e30f061c86e2a75711e70778884de7c48c` | 干净，11:20/11:22元数据复核 | 只读取`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`的源码优先/版本化规则；不作为本任务业务源码或构建来源，不沿用main的iOS配套 |
| cordcode-macbridge（owner 指定的只读状态机文档来源） | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | 既有 20 项文档修改/未跟踪，逐项见附录 C；被引用文档不在 dirty 集合 | **仅参考文档，不是本任务实现/配套来源**；读取 owner 指定 `docs/2026-10-15-agent-management-topology-and-state-machines.md` v3 的 §4.1/§5.2/§6.2/§7.3；不引用该工作树业务源码，也不沿用其 iOS 来源组合 |

运行身份（本机，2026-10-19 取证）：v2 共享服务 `http://127.0.0.1:49374`
（desktop 2.0.32 spawn，`service.json` 自报 version 2.0.32）；v1 managed 服务
`http://127.0.0.1:4096`（PID 28164，磁盘 CLI 1.19.3，健康自报 1.19.3）；
desktop app 2.0.32（Electron）。以上是前轮取证时间窗，不冒充本轮运行态。
旧上游checkout不作为v2语义依据；其历史身份保留在表内。v1.6的fresh来源门见附录D，
任务配对为表内M/I（Mac与iOS的claude-long-session-projection-loading工作树）；
上游、main旧规则文档与native-timeline状态机文档均不作为该产品配套。引用的状态机文档实际 SHA-256 为
`e49e2d5d00e6216bd70d0127c02949351be96474aef1c8b6cd0b14f91d74121a`，其 v3 方案
提供状态轴与交互参考，不把“待独立评审”文档当本任务已经实现的 API。

### 2.2 复用调查表（官方源码优先，避免自造轮子）

| 能力/需求 | 已有实现与锚点（opencode-v2@v2.0.32） | 选择 | 必要改动/无法复用的证据 |
| --- | --- | --- | --- |
| 服务发现/探活协议 | `packages/client/src/service-probe.ts:49-90`：读 `~/.local/state/opencode/service.json` → `GET /api/info`（Basic `opencode:<password>`）→ 校验响应 `{version,pid}` 与注册一致（pid `:78`、version `:79`）；404=协议不兼容。`fallback()` 同文件 `:39-41` 定位注册文件（`XDG_STATE_HOME` 可覆盖） | 复用协议，Go/Swift 各自实现薄客户端 | 官方是 TS 客户端内嵌函数，无可复用二进制；协议本身即复用对象 |
| 注册文件契约 | `packages/client/src/service.ts:46-57`（Info：id/version/url/pid/password）；写入者 `packages/cli/src/services/service-registration.ts:39`（0600、temp+rename 原子写） | 复用 | 无 |
| 单例仲裁/替换语义 | `service-registration.ts:40-67`：服务每 5s 回读注册文件（`Schedule.spaced("5 seconds")` 在 `:63`），被替换即自杀；`:68-71` 正常退出删文件 | 复用（只依赖，不重实现） | 无 |
| 固定端口 | `packages/cli/src/services/service-config.ts:56`：latest channel 默认 `0xC0DE`=49374 | 复用事实 | 无（端口确定性降低发现成本，但仍以注册文件为准） |
| 手动共享启动与 ensure 的副作用边界 | `packages/cli/src/commands/handlers/serve.ts:6` → `packages/cli/src/server-process.ts:64,159`；`packages/client/src/effect/service.ts:34` incumbent 探测；官方并发测试 `packages/cli/test/service.test.ts:381,637`。另有 `commands/handlers/service/start.ts:11` 调 `Service.ensure`，但 `packages/client/src/effect/service.ts:75-87,98-104` 会超时终止/版本替换现有 owner | **复用直接 `serve --service`，仅用户点击调用**；不重写官方单例，不调用 ensure/stop | 官方直接 serve 负责固定端口竞争/注册；Swift 只负责动作门、detached 生命周期和只读健康等待。已有活注册或配置冲突不另起，避免覆盖注册使旧 owner 自退；具体防护见 §3.2.3 |
| 启动进程生命周期 | `packages/client/src/service-contender.ts:19`：detached、stdin/stdout ignore、stderr pipe 尾部 8KiB、unref；`service-registration.ts:14,32`：注册 owner 自查 | 镜像官方进程/stdio 不变量，复用本仓进程执行 seam | Swift 的 Foundation Process 不能仅凭名字声称等价 detached/unref；S-1/E-9 必须证明退出客户端不杀共享 daemon、输出不阻塞、唯一 wait/reap，不新增外部 LaunchAgent |
| v2 安装包与命令 | `packages/cli/package.json:3` 的 `@opencode/cli`/`2.0.32`；`services/www/src/docs/content/index.mdx:14-18` 给 v2 npm/brew/install；`packages/cli/script/publish.ts:126`；`packages/cli/test/updater-install.test.ts:140,206` | 固定 v2 包并隔离 prefix，用户手动执行 | 旧 `opencode-ai`/`https://opencode.ai/install` 是 v1（`packages/web/src/content/docs/index.mdx:37`），不得拿它做 v2 安装提示；本轮只读 registry metadata 确认包身份（A.12），未下载/安装 |
| v2 bundled/staged 发现 | `packages/desktop/src/main/service/desktop-cli.ts:77,139`；`packages/desktop/src/main/lifecycle/configure.ts:33`；包版本旁置文件由 `packages/desktop/scripts/utils.ts:109` 写入；`packages/desktop/electron-builder.config.test.ts:73` | 复用官方路径与版本旁证，动作前只读 `--version` 有界核验 | 不运行 Desktop GUI；不把 App 版本或任意 PATH `opencode` 当 CLI 版本。安装身份来源与实际服务版本分别展示 |
| 身份/健康端点 | `packages/protocol/src/groups/server.ts:43-51`（`GET /api/info`）、`:5-19`（ServerInfo：version/pid/urls/paths/capabilities） | 复用 | 无；活体样本见 E-1 |
| 目录路由（两个独立机制，r1 更正） | **列表过滤**：`packages/protocol/src/groups/session.ts:168-174`的`SessionsQuery.directory`，`/api/session`用plain `directory` query。**scoped location**：`packages/server/src/location.ts:71-79`的`requestRef()`读取`location[directory]` query（`:74`）或`x-opencode-directory` header（`:75`）。活体矩阵（A.8）仅列表`?directory=`有效 | 新包逐项镜像官方两机制 | 旧`agent/opencode-web/client.go:97-124`仅作事故/接线记录，禁止复制实现或沿其注释反推v2；scoped写的最小上下文由E-3与官方consumer共同核定 |
| 事件流 | `packages/protocol/src/groups/event.ts:44-52`：`GET /api/event` SSE，官方描述"across all server locations"；volatile 契约（`:51`：慢消费者断流、断线丢事件） | 复用端点；映射自建 | 事件 schema 与 v1 完全不同（`packages/schema/src/event-manifest.ts:41-72`、`session-event.ts`），需要 v2→bridge 映射（附录 B 初表） |
| 会话 CRUD/读 | `packages/protocol/src/groups/session.ts:182,221,287,300,362`（list/create/get/remove/update）、`packages/protocol/src/groups/message.ts:68`（messages） | 复用 | envelope 按路由分型（§3.5，r1 更正：非统一 `{location,data}`）与 parts 带 id 的 schema 需映射层 |
| 发消息 | `session.ts:396-407`：`POST /api/session/:id/prompt`（可选caller `id`、PromptInput、metadata/delivery/resume） | 新包镜像官方请求 | 保留CordCode稳定correlation接口，不继承旧SendWithOptions/attachmentParts；v2 ID/parts/投递语义须E-3/E-4证明 |
| 中断 | `session.ts:746`：`POST /api/session/:id/interrupt` | 复用 | v1 abort 路径不同 |
| permission/question | `packages/protocol/src/groups/permission.ts:23-124`；全局form列表`form.ts:12`，session作用域list/reply/cancel在`session.ts:796-870`（reply`:842`） | 新包镜像官方生命周期/请求 | 旧preview分支不是2.0.32证据；E-5逐项绑定真实pending/reply/reload/resolution，不复制旧恢复逻辑 |
| project registry | `packages/protocol/src/groups/project.ts:6,17`（`/api/project`）；2026-10-19 活体：返回**裸数组**（27 项，无 envelope） | 复用 | membership 语义见 OD-4；裸数组形状进入 S-6 断言（F-5） |

### 2.3 MacBridge 现状锚点（本仓 `1fe9c65d`）

| 现状 | 锚点 | 与本方案的关系 |
| --- | --- | --- |
| 旧包有1.18/v2 generation与envelope探测残留 | `agent/opencode-web/client.go:24,63-70,97-124`；`probe.go:125-232` | 仅历史参考；本版新建v2 typed client，不复用该generation框架 |
| 旧probe曾把v2探活写成`/api/health`，实际2.0.32为`/api/info` | `agent/opencode-web/probe.go:174`；官方`packages/protocol/src/groups/server.ts:43`，A.8活体404/200 | 旧误判作为禁止从legacy推协议的反例；新包直接实现官方/info，不为接v2而改旧包探测 |
| 旧包v2 SSE已删除、v2被quarantine | `agent/opencode-web/events.go:142-145`；`opencodeweb.go:328-336` | 保持旧包拒绝v2；S-2b只放行新opencode产品，不解除此隔离 |
| Swift 已预留 `service_discovery_future` source 槽位 | `MacBridge/MacBridge/Services/OpenCodeEndpointResolver.swift:25-27`；`go-bridge/main.go:110`（flag 枚举已含该值） | OD-1 推荐激活该槽位为 v2 attach |
| managed v1 生命周期（spawn/adopt/stop）与 URL 解析所有权在 Swift | `RuntimeManager.swift:1157-1161`（resolve guard）、`:1264`（startOpenCodeManagedServer）；`OpenCodeManagedServer.swift:238-296`（ensureRunningOnQueue spawn）、`:385-388`（adopt） | attach 判定放 Swift（OD-1）；v2 分支隔离 v1 ensure/stop/sync，旧实现仅供显式旧来源使用 |
| Swift 会把 managed URL 同步进 desktop 配置 | `RuntimeManager.swift:1198`（syncDesktopConfig 调用）、`OpenCodeManagedServer.swift:318-344` | attach 模式必须跳过（2026-10-19 实证：该同步曾把 4096 写进 desktop、参与崩溃链，附录 A） |
| **Swift 侧监控/重启机制现状（r1 补充，F-2 依据）**：监控循环 3s 一轮（`RuntimeManager.swift:708-721`，pollManagementAPI + evaluateAutoRestart）；现有重启触发只有 crash 重试（`:687-690`）、120 分钟兜底（`:745-766`）、显式 `restart()`（`:458`）、`fileReadHealth.restartRecommended`（`:1015-1017`）；`resolveManagedOpenCodeIfNeeded()` 仅 start/apply 路径调用（`:536`/`:581`）；management `/internal/agents` 对 opencode-web 已做结构化就绪 live 重读（`management_api.go:692-733`，状态面 not_configured/service_not_running/available，`agent_descriptor.go:243-249`），Swift 每轮 status 后 detached 刷新 agents（`RuntimeManager.swift:1019` 一带） | 如左 | **backend 探活失败当前不触发任何重启路径**——attach 模式断裂后最长滞留 120 分钟；§3.2 的触发机制正是挂在这套既有循环上（不新增轮询器） |
| 旧凭据已用env设施（不常驻argv） | `go-bridge/main.go:104-106`的`OPENCODE_WEB_*` | 仅复用env清理/secret隔离设施；新v2新增独立`OPENCODE_V2_*`槽，不把旧opencode/opencode-web凭据当新服务身份 |
| OpenCode 行已具备专用动作，但目前是 v1 | `WorkspaceView.swift:1158-1198,1639-1685`；`RuntimeManager.swift:1209-1310` | 扩展为 v2 状态输入与显式启动；不能把旧 v1 `cliFound`/installer 当 v2 安装证据；原 URL-only 更新改为完整身份比较 |
| 已有 workspace 安装/指引/只读行观察 | `go-bridge/workspace_snapshot.go:15-45,93-125,212`；`toolinventory/catalog.go:16`、`recipe.go:93-108`；`BackendStatusViewModel.swift:182` | inventory/copy/行状态复用；当前 catalog `opencode-ai` 安装指引是 v1，shared v2 必须绑定 `@opencode/cli` 与准确版本/scope，补本机 source-aware recipe，不另建 inventory |
| catalog 聚合单目录失败即整轮失败 | `agent/opencode-web/sessions.go:137-143` | OD-5 裁决对象 |
| membership 的 desktop persist 读取（`.dat`）已被 v2 破坏 | `desktop_home_projects.go:24-33` | OD-4 一并处置（v2 下 `.dat` 不存在，2026-10-19 实证回退 registry，附录 A） |

已知复盘（think.md 相关条目，实施期必须遵守）：
- `think.md:2714-2716`：`docs/2026-08-18-opencode-web-backend-design.md` 已降为历史
  记录禁止据此施工；现行证据入口是 `docs/2026-08-20-opencode-web-source-parity-audit.md`
  与 `docs/2026-08-20-opencode-web-source-first-convergence-plan.md`。
- `think.md:334`（目录查询挂死：空 `.opencode` + 全局 plugin + 代理挂起 npm）、
  `think.md:1799`（新会话首回合完成态闪烁）、`think.md:2779`（opencode-web 排障教训）。
- `GO_BRIDGE_ARCHITECTURE.md` 的 Backend 语义锚点表 opencode-web 行「待补」（`think.md:852`）；
  本方案实施时应顺带补齐 v2 行锚点。

### 2.4 能力底表（v1 1.18 产品面 → v2.0.32 处置）

三线界定：声明（protocol 源码路由）＝官方 schema 声明；挂载（本机活体）＝2.0.32
服务实测；使用（go-bridge 调用）＝现 driver 调用点。状态取值：supported /
plan-supported（本方案纳入）/ deliberately unsupported / not applicable。

| 能力 | v1 现状 | v2.0.32 声明 | 本机活体 | 处置 |
| --- | --- | --- | --- | --- |
| 会话列表/详情/创建/改名/归档/删除 | 已用 | `session.ts:182-382` 声明 | 列表/详情已实测 200（附录 A） | plan-supported（S-4） |
| 消息历史读取 | 已用（v1 表） | `message.ts:68` | v2 读 200 实测；端点 **cursor 分页**（默认 50 条/页，2026-10-19 复测：首页 277,979 B，跨页全量 132 条——口径详见附录 A.2/A.8） | plan-supported（S-4） |
| 发消息（文本/图片/文件） | 已用 | `session.ts:396-407` prompt（payload 含可选 caller `id`） | 未采样 | plan-supported（S-5，E-3/E-4 门） |
| 中断 turn | 已用 | `session.ts:746` interrupt | 未采样 | plan-supported（S-5） |
| permission 请求/应答 | 已用 | `permission.ts:23-124` | 未采样 | plan-supported（S-5，E-5 门） |
| question（form） | 已用 | `form.ts:12`（`GET /api/form`） | 未采样 | plan-supported（S-5，E-5 门） |
| 实时事件流（外部 turn 广播） | 已用 `/global/event` | `event.ts:44`（`/api/event`，跨 location） | 流端点存在（desktop 日志在用）；payload 未采样 | plan-supported（S-3，E-2 门） |
| todos | 已用（v1 `/todo`） | **无 todo 路由组**（groups 目录无 todo） | N/A | **deliberately unsupported**（OD-6：能力下调，iOS 面隐藏） |
| 模型/供应商列表 | 已用 | `model.ts`/`provider.ts` 组 | `/api/model` 200 实测 | plan-supported（S-4） |
| workspace diff / 文件变更 | 已用（v1 事件） | `filesystem.changed`（`filesystem.ts:9`） | 未采样 | plan-supported（S-3 随事件映射；样本随 E-2 捕获） |
| session pin | bridge 侧 pinstore，与代际无关 | N/A | N/A | not applicable（保持） |
| 服务发现/身份 | 无（URL 由 Swift 注入） | `server.ts:43`（`/api/info`） | 401/200 实测（E-1） | plan-supported（S-1/S-2a） |

排除说明：以上枚举范围为 go-bridge opencode-web 现产品能力面（management API
capability 广告 + driver 调用点），不含 v2 全部路由组（voice/websearch/pty 等
与 CordCode 产品面无关，not applicable 不逐行列）。

---

## 3. 设计与决定

### 3.0 独立实现与身份边界（本版强制）

owner 2026-10-19 明确指定：**目录为 `agent/opencode-v2/`，对外 backend ID 为
`opencode`**；不是 `agent/opencode-web/v2/`，也不沿用 `opencode-web` 作为v2身份。

| 对象 | 实现/身份 | 本任务处置 |
| --- | --- | --- |
| 最老OpenCode接入 | 原 `agent/opencode/`；历史ID=`opencode` | 退役身份历史；不恢复旧实现/旧proxy；已归档源码也禁止生产import |
| 现有v1 Web接入 | `agent/opencode-web/`，ID=`opencode-web` | 保留稳定基线与显式旧服务接入，待§3.12稳定门后退役；不是v2代码母本 |
| 新v2接入 | **`agent/opencode-v2/`，Go包名拟为`opencodev2`；ID/kind=`opencode`** | 全新官方v2语义适配、独立fixture/测试/运行实例；显示为OpenCode（v2） |

**实现规则：**

1. 新driver只以 `/Users/jacklee/Projects/opencode-v2` 的目标版本官方UI调用链、服务端/
   schema、测试和真实样本定义请求、响应、事件、分页、排序、identity、重试/取消、状态机
   和完成条件。每项建立“官方UI＋server/schema＋真实样本＋bridge映射＋定向测试”证据链。
   旧adapter只能用于列历史事故与定位CordCode接线点，不能反向证明官方协议。
2. **禁止复制/直接import** `agent/opencode-web`、最老opencode归档实现及旧
   `OpenCodeProxy` 的请求/事件/history parser、arm/idle推断、permission/form恢复或
   代际fallback；禁止先复制整个目录再逐步替换URL。v1.5“在原文件增加v2分支、整套生命周期
   保留”的施工指令在本版作废。
3. **复用公共层，不复用旧driver包。** 新`agent/opencode-v2/`的生产代码、子包、测试
   及其全部传递依赖，均不得import、blank import、type alias、嵌入或经wrapper/factory/
   callback反向依赖`agent/opencode-web/`、其任何子包或最老opencode归档实现。不能把
   helper包装进名为“公共”的包，却让该包继续import旧driver。通用HTTP/Basic Auth、
   错误脱敏/日志、进程执行等如确需从旧包抽取，**先迁入不隶属任何backend的中立公共包**，
   补通用契约测试，再让v1与v2分别单向依赖它；不把旧包export API作为v2过渡方案。
   原本就在标准库/core/公共层的设施直接复用，只有一个调用者的薄helper可留新包私有，
   不为了“复用”制造共享层。事件顺序、去重identity、重连恢复和完成条件属于backend
   语义，不能按通用helper抽走，仍须逐项用v2官方源码与同版本样本证明。
4. 新包不含 `generation118` 分支或 v1 `/global/event`、`/session` 协议回退；只接受
   经来源/样本绑定的v2支持范围，未知版本/shape fail closed。旧包继续拒绝v2，不解除它
   的quarantine来服务新产品。条件放行针对**新driver**，见S-2a/S-2b。
5. 包隔离不是运行态双写许可：新旧driver不合并同一session；产品切换原子应用选择，
   不默认双挂、不双订阅同一官方服务、不把v2失败自动转交v1。旧历史/缓存不能借同名ID
   自动作为新v2真值，跨端身份门见§3.11。
6. 未来回归增加**依赖闭包**、注册与路由守卫：不仅扫新包直接import，还检查以新包/
   中立公共包为根的生产与测试依赖图，禁止出现旧包或其子包；过渡bridge装配层同时
   import两代driver不算两代彼此依赖，不能把整个runtime依赖图当新包闭包误判。
   公共helper不能传递依赖旧adapter。新`opencode`只
   注册一次，请求/SSE/history/live/reconnect均从新driver进入同一Kernel，不走旧proxy。
   旧真实样本若有调查价值只读引用证据；新v2 fixture独立归档，不以相对路径、go:embed、
   symlink或测试helper依赖旧目录维持v2测试。

**公共设施的装配方向（本版冻结）：**

```text
bridge装配层（go-bridge：持有Projection Kernel/EventPublisher/SSV2 fence）
    ├─ 经core接口装配 agent/opencode-v2
    └─ 经core接口装配 agent/opencode-web（仅过渡v1）
agent/opencode-v2  ──→ core / 标准库 / 中立公共包
agent/opencode-web ──→ core / 标准库 / 中立公共包
中立公共包        ──→ core / 标准库 / 通用库（不得反向依赖任一backend）
```

“复用Kernel/EventPublisher/SSV2”指继续使用**bridge层同一实例和事务链**，由新driver
通过core接口产出事实或调用明确注入的公共接口；不是让新包import `go-bridge`，也不是
经opencode-web转接到Kernel。Management、安全重启与Swift生命周期由现有owning层
使用公共服务；不把旧OpenCode专用manager当新v2的间接依赖。新旧driver彼此无依赖箭头。

**退役可分离标准：**在受控的构建/测试验证环境将旧opencode-web包及其测试资源排除，
只在bridge产品装配/catalog中移除旧driver入口，新v2及其公共依赖无需修改任何源码、
import、类型或调用即可构建并通过同一组定向测试。验证不在用户现用目录破坏性删除；
S-7先证明依赖闭包，S-9执行前再证明实际退役可分离性。失败时先在公共层完成解耦，
不能把一串“退役时再替换v2引用”留给后续。

本版是新v2接入的单一设计依据。旧
`docs/2026-08-20-opencode-web-source-first-convergence-plan.md` 仍约束保留中的v1
`opencode-web`；其§1/§3/§4的源码优先/独立版本样本纪律在本版继承，但其1.18.18
端点/字段、旧“任何桶失败整轮失败”不作为v2实现依据。该文档的历史流程名不在本任务
恢复；后续出现冲突先修订本版，不靠拼接几份旧方案决定新语义。

### 3.1 总体架构与数据流

```
OpenCode Desktop / CLI（官方，不动）
        └─ 官方共享服务（service.json 注册；实际地址从注册读取）

CordCode Link 的 OpenCode（v2）行
        ├─ 只读发现/安装检查 → 状态与按钮（§3.2）
        └─ 用户点击“启动” → 已验证的官方 v2 CLI 共享服务启动入口
           （仅此动作允许拉起；官方负责单例/注册，不创建专属 4096）

CordCode Link（Mac app，Swift）
        └─ endpoint 解析（改造点①）
             1) 同官方规则定位 <XDG_STATE_HOME 或 ~/.local/state>/opencode/service.json
                （0600，同用户；不把固定端口当发现协议）
             2) GET /api/info（Basic opencode:<password>，2s 超时）
                校验：loopback http、pid、响应 version 与注册一致
                支持的 generation 仍受 S-2a/S-2b 样本门约束
             3) 通过 → 新opencode的source=shared_service_v2：把url/username/password经
                独立OPENCODE_V2_BASE_URL/_SERVER_USERNAME/_SERVER_PASSWORD环境传给runtime，
                复用env设施但不占旧opencode-web凭据槽；跳过managed spawn/syncDesktopConfig
             4) 不通过 → 明确的未启动/检测失败/版本不支持等状态；不 fallback 4096
        （只读重发现及凭据轮换由 §3.2 的既有监控周期驱动；
         显式 external_http/legacy_64667 不参与自动来源选择）

cordcode-bridge-runtime（Go，改造点②）
        └─ 新 agent/opencode-v2/（backend ID=opencode）
             - probe：按官方 /api/info 验证身份与支持版本，不采用旧包shape arbiter定代际
             - SSE：官方 /api/event 生命周期＋新映射 → 既有bridge事实/单一Kernel
             - 读/写：官方 /api routes＋独立typed映射/真实fixture
             - 隔离：新driver未满足样本/跨端身份门不得广告可用，不切v1
        └─ 原 agent/opencode-web/（ID=opencode-web，仅v1）
             - 保留到稳定门；与新driver分别注册但产品选择互斥，不承载v2
```

不变量：
- **单一接入选择**：新的 `opencode` 只连接官方v2共享服务，旧 `opencode-web` 只连显式
  保留的v1来源；产品选择互斥且各自endpoint/凭据隔离。共享v2不可用就如实不可用，不跨
  到v1，也不把 `opencode-web` alias到 `opencode`。Swift唯一拥有endpoint选择。
- **显式启动**：安装、启用接入、桥接进程重启与后台探测均不能产生 v2 spawn；
  “重新检测/重附”只连已有进程。“启动”动作与接入开关分别管理。
- **共享进程所有权**：共享服务由官方实现管理；关接入、退出 CordCode 或 endpoint
  解析失败只清本客户端连接/凭据，不按端口/PID 停掉别的客户端共用的服务。
- **跨端身份同步**：对外ID/kind=`opencode`是新v2身份，canonical protocol pack、
  hello/descriptor/RPC分流、iOS/remote-web客户端及持久数据必须按§3.11成套适配；
  内容仍由既有Kernel/ProjectionStore唯一写入，不因新包增加第二timeline来源。
- **官方协议优先**：发现/探活/目录路由/事件端点/写路径全部按 2.0.32 源码契约，
  不自造等价物（§2.2 表）。

### 3.2 Swift 侧：共享发现、显式启动与行状态（S-1）

#### 3.2.1 来源与状态所有权

参考 owner 指定状态机文档的 §4.1 独立状态轴、§5.2 安全应用选择、§6.2 行展示顺序、
§7.3复制安装命令流程。复用已存在的SelectionStore、RuntimeManager、workspace snapshot/
通用inventory与行组件，不新造全局manager。新行的action状态独立命名为
`openCodeV2SeatAction`（拟议），旧`openCodeSeatAction`保持v1，启动/错误/版本事实不共槽。
本工作树`WorkspaceView.swift:1158-1198,1639-1685`与`RuntimeManager.swift:1209-1310`
只是旧专用动作的接线位置；它们的v1 managed启动/安装实现不能直接充当新v2行为。

- 新`opencode`只接受持久source=`shared_service_v2`，与旧`opencode-web`的managed/
  external/legacy设置分开保存；不把旧配置或enabled偏好静默改名迁入。正式选择/过渡的
  四拍见§3.11：用户显式选择新OpenCode后才持久化最新selection revision。旧
  `service_discovery_future`仅作为历史配置记录，不靠它触发默认冷启动或继承旧凭据。
  新v2启用但服务缺席时仍保持该选择，不回选旧driver。
- 用户显式 `external_http`/`legacy_64667` 保持原地址与行为，禁用 source/接入保持关闭；
  不被共享服务发现劫持（F-13）。本期共享模式不会自动回选 `managed_local`；已有 v1
  服务可以由用户继续独立使用。本方案不提供自动清理 4096 的动作。
- **用户意愿**只由既有 SelectionStore 管；安装/服务检查不写开关。**安装事实**复用
  inventory seam，但目标必须是可启动共享服务的 v2 CLI（§3.2.3），不能继续把 PATH 中
  任意 v1 `opencode` 记成“v2 已安装”。**服务发现**由 Swift resolver 管，**backend
  readiness**来自当前 runtime descriptor；**启动操作**由 RuntimeManager 的专用
  action/operation generation 管；**目录覆盖**由 driver 管（§3.7）。View 只映射这些观察。
- 发现函数为只读：按官方 `XDG_STATE_HOME`/home 规则找注册、验证 URL/版本/pid 及
  `/api/info`，单次网络预算 2s；不写/删注册、不 spawn、不调用 managed ensure。注册
  损坏、读取权限错误、身份不符、版本不支持独立报错；缺失/确认 stale 才是未运行。
  健康服务先于本机 launcher 判定：已发现可用服务时，即使没找到本机 CLI，仍可接入，
  不误报“未安装”。
- 新独立`resolveOpenCodeV2IfNeeded`（拟议）覆盖launch/apply/monitor的v2接线，
  不塞入旧managed方法伪装相同来源：只读发现；失败清除待传的旧v2凭据，不传占位URL；
  不调用`OpenCodeManagedServer.ensureRunning()`、`syncDesktopConfig()`或v1 installer。
  无endpoint时bridge仍可运行，OpenCode（v2）行如实未启动/失败。旧
  `resolveManagedOpenCodeIfNeeded()`继续只对显式旧opencode-web来源生效。

#### 3.2.2 行状态与按钮矩阵（权威）

状态组合沿用独立轴，不能压成一个“就绪”Bool；安装观察有时间戳、source 与 binary
fingerprint，过期/在飞观察不能继续给新启动动作放行。

| 前提／证据 | OpenCode（v2）行展示 | 可用动作／后续 |
| --- | --- | --- |
| 安装/注册观察在飞，无已证事实 | 检测中 | 不显示可执行启动；允许合并重查 |
| 接入关闭 | 未接入；安装事实独立展示 | 开关可开启；不开网络订阅、不拉起服务 |
| 共享服务不存在，已验证 v2 launcher | 已开启 · 未启动 | **“启动”**；不显示自动启动承诺 |
| 没有服务，只有 v1 或没有任何候选 | **OpenCode v2 未安装**；有 v1 时补“已检测到 v1，予以保留” | 具体 v2 安装命令＋复制＋官方说明；“重新检测”；无可用启动按钮 |
| 候选存在但版本探针 timeout/权限错误/输出无效 | 检测失败／版本无法确认及原因 | 重新检测；不伪装 missing，不发启动 |
| 注册损坏、身份不一致、认证失败、版本不支持或端口冲突 | 对应真实原因 | 重新检测/查看原因；不以“启动”强制替换已有进程 |
| 同一行启动操作在飞 | 启动中… | 按钮禁用、合并重复点击 |
| 服务 `/api/info` 已证 ready，endpoint 尚未应用或 backend 未 ready | 服务已启动 · 正在接入 | 展示应用过程；有活跃任务时“等待任务结束后接入” |
| endpoint 已应用、当前 descriptor available | 已接入 · 连接正常（共享服务 v2，实际版本） | 无“启动”；目录 partial 时叠加 §3.7 告警 |
| bridge 已停止/观察过期 | MacBridge 已停止／连接状态未知 | 提供原 bridge 启动/重新检测入口；不沿用旧绿色，不自动启动 bridge |
| 启动/应用失败 | 启动失败／接入失败＋真实原因 | 条件仍为“服务未运行＋已验证 launcher”时“重试启动”，否则重查/重试接入；不 spawn 4096 |

未安装时即使接入关闭也展示安装指引；复制命令只变成“已复制”，不进入 installing，不宣称
安装/登录/服务就绪。安装完成后重查只更新事实，不改开关、不自动启动（用户先前已开启的
意愿仍保持，不能因重查偷偷改成关闭）。外部服务模式不要求本机 CLI、不给本地 v2 启动
按钮；仍使用原配置/检测入口。

#### 3.2.3 手动安装与启动的交互四拍

1. **进入**：MacBridge 工作站的 OpenCode（v2）行。服务未运行且 v2 可启动时显示“启动”；
   未安装时行内显示 v2 安装命令、复制按钮与官方说明。保持该行现有布局，不扩成另一页。
2. **输入**：启动本机共享服务无需填端口/密码/工作目录，全部由官方实现产生并注册；
   复制安装命令后由用户在 Terminal 执行并完成官方要求的登录，再点“重新检测”。命令
   必须指向 v2、与 v1 安装 scope 分开，不用 `npm install -g opencode-ai` 的隐含 latest。
   本版给出的完整命令（仅展示/复制，本轮未执行；实现由 source-aware `CommandRecipe`
   生成，实际 `$HOME` prefix 按用户 home 解析并严格 shell quote）：

   ```bash
   npm install --global --prefix "$HOME/.local/share/opencode-v2/2.0.32" @opencode/cli@2.0.32
   ```

   安装验证用同一 prefix 的绝对路径 `bin/opencode --version`，预期 `2.0.32`；不改 PATH，
   不覆盖全局 v1；不改 XDG state/config/data，因此仍接入官方同一 shared service，而非创建
   另一套数据。若本机无可证明的 node/npm，行保留命令并显示“需先安装 Node.js”及官方链接，
   不自动装 Node。固定版本来自上游 tag/package 与官方 v2 安装文档；本轮只读 registry
   metadata 为200且 name/version吻合（A.12），仅证明发布元数据存在，未下载/安装。E-9
   仍要求未来真实安装后的binary/version确认，不能把本段当安装执行成功证据。
3. **发出**：“启动”在 off-main 单飞 operation 中重查安装和注册；若 Desktop/CLI 已抢先
   启动可用服务，直接复用，不再 spawn；若仍确认无活 owner，只运行已验证 v2 executable
   加 `argv=["serve","--service"]`。这是官方 Desktop/CLI ensure 的 server 子命令形状，
   server 自身的固定端口竞争/注册处理沿用官方 `server-process.ts`；不调用 v1
   `serve --port 4096`，不传 `--port 0`、`--stdio`，不执行 shell 拼接命令。
   v2 launcher 顺序为已验证的 OpenCode.app bundled CLI、同版本官方 staged CLI、显式
   v2 安装记录、PATH 中经版本/参数校验的 v2；v1 候选跳过但保留。版本未知就 fail closed。
4. **过程/结果**：动作在原行显示“启动中”与真实错误，服务注册＋`/api/info` 验证成功才
   “服务已启动”，再安全应用 endpoint，descriptor available 后才“连接正常”。重复点击
   合并；默认 30s 有界启动预算，超时保留错误并重新只读发现，不靠终止共享服务解决。
   启动调用成功不等于已接入；安装/启动/接入三个结果分别确认。

**启动前边界与刻意差异**：官方 `service start`（`commands/handlers/service/start.ts:11`）
虽然更短，但它调用的 `Service.ensure` 在同一注册连续 3 次超时后会 terminate，还可版本替换
（`packages/client/src/effect/service.ts:75-87,98-104`）。本期用户只授权手动启动，不隐含
共享服务恢复/替换权限，因此选直接 `serve --service`，不另写 ensure 的重试/锁/替换算法。
Swift 等候用同一个只读 discover；子进程退出 0 可能只是让位 incumbent，不能当 ready。

- 注册存在且 pid 活着但服务启动中/失败/超时/不支持时，**不把 discover 未返回 ready 当作
  owner 不存在**；保持该错误/启动状态，只读等待或让用户重查，不另起 contender。注册
  损坏/读失败同样阻断启动；仅不存在或结构可解且确认旧 pid 已死时才进入启动路径。
- 只读核对官方 service config 的 hostname/port/remote；保留默认 XDG/channel，不能为
  “保证可用”另开端口。已知非 loopback 配置或注册端点冲突时明确显示冲突并停止本次动作，
  不自动改配置、换地址或强启。remote 已开启时显示“启动将沿用 OpenCode 的远程访问设置”
  并在这次启动动作中确认；本期不新增/关闭 remote 功能。`disabled` 是普通客户端选择
  standalone 的开关，直接 `serve --service` 不以它为拒绝条件，不伪造上游拒绝语义。
- 官方是“配置目标端口”仲裁；不同端口但同注册文件的新进程仍可能使旧 owner 自退。
  所以要保留上述冲突门，不承诺任意外部配置竞态绝对无影响；本方不调用 terminate/stop，
  同配置的并发点击由官方端口仲裁，竞态由 E-9 官方测试/组件用例覆盖。
- spawn 镜像 `packages/client/src/service-contender.ts:19`：独立 detached 生命周期、
  stdin/stdout 忽略、stderr 持续排空只留 8KiB 脱敏尾部；不持有无人读取的 pipe。候选退出
  唯一 wait/reap，真正 winner 从注册和探活确认，不认自己 spawn 的 pid 必然获胜。
  Swift 平台 spawn seam 在 E-9 证明 detached 语义；Foundation Process 的存在不是该证据。

动作拥有唯一等待/取消属主。接入关闭、source 改变、App 退出或 operation generation 过期
时，取消本次观察与未提交配置，晚结果不得写回；若官方共享 daemon 已启动则只断开本方
控制任务，不终止 daemon。共享服务不是 CordCode-owned managed child，关开关/退出
不调用它的 stop/restart/replace。启动失败不清理注册、不覆盖密码、不杀占用端口的进程；
官方本路径没有 `.lock` 文件，不自建或删除一份锁。

#### 3.2.4 只读重发现、凭据轮换与应用防抖

- 复用 `RuntimeManager.startMonitoring()` 的现有周期（`RuntimeManager.swift:709-723`），
  不新增常驻 timer；只对已开启且来源为 shared v2 的行进行只读检查。注册变化、用户点击
  重查/启动后立即检查；未运行时自动重查最多每 60s 一次、单飞 2s 网络预算。相同 endpoint
  健康失效由现有 live descriptor 尽快反映；descriptor 失败只改变行状态，不重启 bridge。
- 比较身份使用注册 `(id,pid,url,version,password)` 的内存 fingerprint，secret 不落日志。
  已连接 endpoint 仍一致时复用原 probe/SSE 恢复；真正出现经验证的新身份才刷新 env 凭据。
  同 URL 密码/pid 变化也必须应用，不能沿用原动作 `RuntimeManager.swift:1293` 的仅 URL
  比较。恢复的是“连接已有服务”，不授予自动拉起/升级/替换服务的权限。
- 初次未运行、后从外部启动或凭据轮换需要新 config 时，沿用
  `applyConfigAndRestart`/`scheduleRestart`/Management V1 admission（同文件 `:462-523,773`
  起），忙碌时显示等待接入并只保留最新候选；不旁路 kill。bridge 已停止时仅保存待启动
  config，不自动拉起 bridge。旧 PID/bridgeEpoch、selection/source/action generation 的
  回调不得提交。
- 自动 endpoint 应用单飞、按身份去重，并有独立的**至少 60s 的 bridge 配置重启间隔**；同一候选应用失败后
  不每轮重发，等待身份/可应用活动状态变化或用户重试。显式点击可申请一次重试但仍单飞且
  走 admission。速率界单独测试：runtime ready 清 `stuckRestartCount`（`:1070-1072`）
  不会清该 60s 时间戳；不能把旧 maxStuckRestarts 当速率保证（吸收 F-10）。不动原其它
  runtime 重启原因的护栏。
- 关闭/退出只解绑本客户端：v2 无 `OpenCodeManagedServer.stop()` ownership；安装复制与
  启动动作不能触发 `syncDesktopConfig`。普通退出 Desktop 可能留 daemon 活着，状态只能由
  注册与官方探活证明，不能从 GUI 进程存在/退出推断。

### 3.3 新driver的身份探测与产品放行（S-2a / S-2b）

继承F-3“探测成功不等于映射可用”的门控要求，改变实现归属：两片都在独立
`agent/opencode-v2/`，不解除旧 `agent/opencode-web` 的v2隔离。

- **S-2a（新driver骨架/身份）**：独立注册新 `opencode`、输入独立endpoint配置、实现
  官方 `/api/info` typed probe与诊断；基线只接受目标 `2.0.32`（更宽范围须独立版本
  evidence pack），校验注册pid/version与实际服务一致、loopback/认证边界。`/api/session`
  的`data`键只是已知路由schema，不是猜测generation的仲裁器。不以旧包的1.18 fallback、
  `/api/health`或字段递归探测来识别v2。此片未满足数据映射/跨端门前保持unverified不可用。
- **S-2b（新产品条件放行）**：**E-2/E-3/E-4/E-5样本齐备＋S-3~S-6映射/读取就位＋
  E-11b身份分流与跨端组件门通过**后，才允许新 `opencode` 的available/能力广告与产品
  活体验证。样本缺失不靠注册成功或200响应放行；旧包仍只服务已验证v1。
- S-3~S-6的新代码/fixture回放可在受控组件环境开发与验证；正式iPhone/remote-web路径
  必须等S-2b。独立样本采集不等同于提前激活新backend，不能写owner生产服务试错。
- 全部新请求/流式/history必须能归属新包和v2服务；新旧同session ID也不混用。v1包仅因
  共享接线或OD-5目录隔离所需做有界改动，不往它里加入v2语义。

### 3.4 SSE：`/api/event` 订阅与事件映射（S-3）

- 新 `agent/opencode-v2/events.go` 实现官方 `/api/event` 客户端，事件decoder、订阅
  生命周期、重试/取消与permission/form恢复以目标v2官方client/UI调用链与E-2/E-5为准。
  不import或复制旧 `events.go`，不预置旧 `healArmedTurnsAfterDrop`/
  `recoverPendingAfterReconnect` 的arm/idle假设；旧watchdog/backoff仅可作为事故/测试
  场景参考，任何复用机制须有v2依据。
- **volatile契约恢复**：官方明示断线丢事件；按官方v2恢复链冷读session/message/
  execution及pending交互，用同一个Kernel事务/fence收敛，不用静默时间猜完成。
  CordCode目录发现可复用共享 `go-bridge/session_discovery.go:403-433` 的控制面指纹/
  失效通知，但目录指纹本身不证明timeline/turn已修复；E-2需覆盖断线中产生的增量、
  终态与重连重复事件。慢消费者/输出积压的处理逐项镜像官方边界并由新包定向测试证明。
- 事件映射（附录 B 初表，按 `event-manifest.ts:41-72` + `session-event.ts`
  源码声明起草，实施期以 E-2 活体样本逐条校正）：
  - `session.created/renamed/deleted/metadata.updated/model.selected` → 会话目录刷新
  - `session.message.content.updated` → 消息 parts 流式增量（对应 v1
    message.part.updated 的 bridge 事件）
  - `session.reasoning.*` → reasoning 流（对应 audit014 链路）
  - `session.execution.started/succeeded/failed/interrupted` → turn 生命周期
    （idle/armed 收敛，替代 v1 session.idle/error 的角色）
  - `session.inbox.*` → 首条消息可见性/投递（对齐 v1 首回合完成态闪烁复盘的收敛点）
  - permission/form 事件组 → question/permission 桥事件
  - `filesystem.changed` → workspace diff/live 文件变更（r1 对齐声明字面量）
- 去重：v2事件含`id`（`event.ts:9-13`）；去重的scope、重连窗口、处理次序和恢复边界
  由官方consumer与E-2样本确认，在新包实现并测试。不能只看字段同名便复制旧dedupe状态机。

### 3.5 读路径（S-4）

- envelope **按路由分型**（r1 更正，F-5：首版「`{location,data}` 统一解包…扩展到
  全部 v2 读路径」与活体不符；2026-10-19 活体实测三类形状）：
  - `/api/session` 列表 → `{data:[…], cursor:{previous?,next?}}`（无 location）；
  - `/api/model` → `{location:{directory}, data:[…]}`（157 项实测）；
  - `/api/project` → **裸数组**（27 项实测，无任何 envelope）。
  新包按各官方路由定义独立typed decoder，不复制旧包`unwrapDataEnvelope`的宽松
  descends-when-present行为；不猜统一envelope。`{location,data}`中的location是回显
  上下文，不能充当列表过滤证据。
- **分页（cursor）**：会话列表与消息列表都是 cursor 分页（`SessionsQuery.cursor`
  `session.ts:173`；消息端点活体实测默认 50 条/页，`cursor.next` 游走取全量，
  2026-10-19 复测 4 页/132 条）。读路径实现按本仓分页纪律（stable ID、不跳页）
  游走；「列表分页」断言进入 S-4/S-6 验证列。
- 会话列表/详情：v2 Session schema → `core.AgentSessionInfo`（字段映射表随
  E-2/E-4 样本落 testdata 后在实施中固化；标题/时间/模型/cost/tokens 已在
  活体样本可见，附录 A）。
- 消息历史：v2 parts（带 `id`）→ bridge 消息部件；role/type 映射
  （user/assistant/system×text/reasoning/tool…）。已知坏行（v1 投影里
  reasoning part 缺 `id`）在 v2 读路径实测 200（附录 A），无需特判，但映射层
  对未知 part 类型按既有 fail-closed 纪律丢弃并计数，不造 fallback。
- models/providers：`/api/model`（含 variants）→ 模型切换面；`/api/provider`
  → 供应商/凭据状态展示。

### 3.6 写路径（S-5）

- prompt：新包 `session.go` 的 `POST /api/session/:id/prompt` payload按官方
  `session.ts:396-407`构造：可选`id`承载一次生成的caller消息ID。stable correlation
  不变量沿用CordCode接口，ID格式、生成/提交时机依官方v2及E-3核验，**不复制旧
  SendWithOptions/attachmentParts实现**；attachments以v2官方part形状携带
  （形状以 E-3/E-4 样本为准）。**scoped 写的目录上下文**（`location[directory]`
  query / `x-opencode-directory` header / 不带，三态）由 E-3 矩阵实测确定最小
  必带形式后再固化——读路径已实测不带上下文可用（附录 A.8），写路径未采样，
  不预设（F-1）。
- interrupt：`POST /api/session/:id/interrupt`。
- permission 应答：`POST /api/session/:id/permission/:requestID/reply`
  （`permission.ts:117`），同样纳入 E-3 矩阵的目录上下文采样。
- question/form：全局列表 `GET /api/form`（`form.ts:12`）；**应答走 session 作用域
  form 组**（r1 更正，F-6）：`POST /api/session/:sessionID/form/:formID/reply`
  （`session.ts:842`）、取消 `DELETE …/form/:formID`（`:858`）、列表/详情
  `:796/:827`。E-5 捕获须含 `form.created → reply → form.replied` 完整流。
- `PromptOptions`/模型切换/agent 选择：v2 有对应端点（session.switchModel/
  switchAgent，`session.ts:330/346`），按现有 capability 面逐项接。

### 3.7 membership 与 catalog（S-6）

owner 2026-10-19 明确裁决“不要搞 fail-all”。本节替代旧 directive-003 中
“任何一个目录桶失败就让整轮全局会话列表失败”的条款；不保留预过滤后继续 fail-all 的分支。
这是 CordCode 私有的目录聚合/告警策略，上游 `/api/session?directory=` 的响应、cursor 与
错误按官方原样解析，不发明另一份上游历史源。

- v2 membership 以 `GET /api/project` 裸数组 registry 去重后的目录集合为准（OD-4）；
  不再读取 desktop 私有 `.dat`/`drafts.sqlite` 来决定 v2 可见目录。列表筛选仍用 plain
  `directory` query。不能仅靠 Mac App 的 `stat` 成功预断言 OpenCode 进程具有 TCC 权限。
- **每目录独立结算**：每个目录读取其所有 cursor 页；该目录全页成功才纳入本轮成功桶。
  目录内一页超时、TCC/权限错误、4xx/5xx、内容 schema 错误或分页不完整，该桶记录具体失败，
  其余桶继续。不得将桶的前半页冒充完整结果；单个桶失败也不得抛掉已成功桶。
- **有界请求**：同一轮目录 fan-out 并发最多 4；单桶 deadline 服从调用者剩余预算；一轮结束
  不在前台无上限重试错误目录。新聚合位于`agent/opencode-v2/sessions.go`，不复制原
  `agent/opencode-web/sessions.go:117-142`的无界goroutine/errs-first形态；不新建常驻轮询器。
- **返回规则**：至少一个桶成功（即使它真实返回 0 条）时，返回这些成功桶的结果，保持现行
  stable session ID 去重和确定排序；同时发布 `complete`/`partial` 的本机覆盖观察。
  registry 确认真为空时是 `complete` 空目录；非空 registry 且全部桶失败时返回真实的
  `catalog_unavailable` 错误并保留全部失败摘要，不能返回“成功的空列表”。registry 自身失败、
  服务认证/身份整体失效或父请求取消属于整次数据源/请求失败，不属于单桶 fail-all。
- **告警与所有权**：driver 拥有一份只读 `catalogObservation`，带 `endpointGeneration`、
  `sampledAt`、完整度、attempted/succeeded/failed 数和逐目录脱敏错误类别。Swift 从现有本机
  workspace snapshot 通道消费该可选观察（`go-bridge/workspace_snapshot.go:36-45` 扩展），
  OpenCode（v2）行在“已接入”下面显示“部分目录不可用（K/N）”及“重试”入口；展开仅显示失败
  目录/原因/上次检测时间。它不把 backend readiness 改为不可用，不新增 iOS wire capability。
  当前快照 codec 需配套定向测试，不能假称该字段已经存在。
- **恢复与失效**：重试入口合并为当前 endpoint 上的一次正常 catalog 刷新；服务故障、换代、
  接入关闭和旧结果晚到时遵守 generation 丢弃规则。失败目录仍留在 registry membership，
  下一轮既有扫描/SSE 触发刷新会再尝试；成功后清掉对应告警，不永久 blacklist，也不反复
  每目录单独刷屏。日志按本轮摘要及错误集合变化限频，不能把故障隐藏成无日志跳过。
- **列表与发现同源**：成功桶合并后的同一列表进入现有 discovery fingerprint 和 catalog
  cache fence（`go-bridge/session_discovery.go:391-440,535-542`）；覆盖变化导致可见集合变化时
  走同一失效/重拉通道。partial 只表示这次已确认的可用集合，不能把缺席的会话写成
  `session.deleted`，不能删除用户持久历史/Kernel，不能用旧桶内容冒充本轮读取成功。
  告警只在本机行呈现；本期 iOS 使用既有列表/错误与投影，不新增跨端目录告警 UI。
- **scoped 请求不被聚合策略篡改**：用户指定单目录列表、by-ID 打开会话或读取历史仍直接走
  对应官方请求，失败如实返回；已打开会话的投影不因 catalog partial 被清空。
- **适用范围**：新`opencode`全局聚合采用上述规则；过渡保留的`opencode-web`如沿用
  OD-5目录隔离，也只做单独有界聚合/告警修正，不移植v2 parser，不共享两代请求/缓存。
  本期按owner“不fail-all”裁决为旧v1补同类单桶失败回归，HTTP/schema/写路径保持v1契约。
  不推广到其它backend；稳定门前这些改动不构成提前退役旧包。

交付对账必须按同一 endpoint/时间窗记录 `N 个 registry 目录 = A 个成功 + K 个失败`；
成功目录官方分页消息/会话数与 bridge 输出数分别列出，按 stable ID 去重差异单列。失败目录
只能写“未读取”，不能计作 0；不再用“全部齐全”描述 partial 结果。验收负例至少包含
一桶 EPERM、一桶 schema/分页错误、全失败、全成功为空和恢复后告警消失。

### 3.8 新身份的能力广告与跨仓文档（S-7）

- 新`opencode`的能力按独立v2实现的core可选接口与E-2~E-5证据推导，§2.4是需求覆盖
  底表，不能整套复制旧`opencode-web`的WireDescriptor。todos处置遵OD-6，任何能力
  只有完整request/event/reload/error链闭合才广告；不能因同名ID继承最老opencode能力。
- 更新Mac canonical protocol pack，并同步配套iOS mirror/Swift/remote-web识别与选择，
  明确新的`id=opencode`/`kind=opencode`及§3.11语义代际/旧客户端隔离。沿用既有SSV2
  内容形状不等于零wire变更，禁止以旧枚举能decode为交付证据。
- 更新GO_BRIDGE_ARCHITECTURE.md：新增`agent/opencode-v2`/`opencode`行及来源/语义
  锚点，原`opencode-web`标v1过渡/退役条件。BUILD_INSTALL_AND_RUNTIME、CHANGELOG及
  两仓CLAUDE/AGENTS中产品身份/禁旧import说明随实施同步；不在本轮先改runbook。
- 独立v2包的设计权威是本方案；旧v1设计仍限旧driver，稳定门后统一文档退役标记。

### 3.9 安全

- service.json（0600，同用户）仅由Swift发现；新driver使用独立
  `OPENCODE_V2_BASE_URL`/`OPENCODE_V2_SERVER_USERNAME`/`OPENCODE_V2_SERVER_PASSWORD`
  环境通道（拟新增，对应新`opencode-v2-*`启动配置），旧`OPENCODE_WEB_*`仅旧driver。
  复用env解析/清理与BuildAgentEnv隔离设施，不复用旧凭据槽。密码不进argv/持久偏好/
  日志；注册身份fingerprint只在内存比较，不输出secret或Authorization头。
- attach URL 强制校验：`http` + loopback（127.0.0.1/[::1]），拒绝任何非
  loopback（官方 remote 功能开启时 url 仍为 loopback 代理，但本方案只信
  注册文件 url 字段并强制 loopback 校验，不解析 remote.route）。
- /api/info 的 pid 校验防止端口复用误连（对齐官方 `service-probe.ts:78-79`）。

### 3.10 OD 表

| ID | 问题 | 本版决定 | 状态/来源 | 影响切片 | 验收 |
| --- | --- | --- | --- | --- | --- |
| OD-1 | attach 判定放哪层 | Swift resolver 唯一拥有 endpoint/source，Go 只消费已解析环境和自己的 readiness | 工程决定延续；不是 owner 指定类名 | S-1 及全部 | 一个产品面、一个 endpoint，不因 Go 探测启动额外服务 |
| OD-2 | v2 服务未运行/未安装 | 未运行显示“启动”，用户点击才启动官方 v2；未安装显示“未安装”＋具体 v2 安装命令。没有自动 ensure-start，也没有缺席→v1 fallback | **decided，owner 2026-10-19 本轮原话：“不用吧，用户可以自己在macbridge手动启动v2…显示‘启动’按钮…没有v2 opencode…显示未安装和具体安装命令行”** | S-1（含状态/启动/安装指引，必交） | §3.2 状态矩阵；无服务时保持未启动，点击后才有一次官方启动动作，失败可见 |
| OD-3 | 4096去留 | 旧安装/数据/外部服务保留；新opencode只连v2，无自动spawn/fallback4096；用户显式选择v2时应用新selection，不静默迁旧source | OD-2及R-7独立身份的配套设计；**不构成停止/卸载v1的授权** | S-1/S-7 | v2失败保持失败；显式旧source不被劫持；旧配置/历史保留 |
| OD-4 | v2 membership | `/api/project` 官方 registry；不读 desktop 私有库 | 方案工程决定延续，沿官方公共 API | S-6 | registry 裸数组解析/去重；目录失败不改变 membership 真值 |
| OD-5 | 单目录失败 | 隔离该桶、返回其余成功桶并告警；错误目录下次正常重试，无 fail-all 备选。全失败与全局源失效如实报错，不冒充空列表 | **decided，owner 2026-10-19：“不要搞fail-all”**；本节显式更新旧 directive-003 聚合语义 | S-6 | 部分成功/全失败/空 registry/恢复矩阵，目录数与会话 ID 对账；scoped 错误不隐藏 |
| OD-6 | v2 todos | 不在官方契约外自建todo，按官方UI/实现/样本底表决定广告；如找到官方实现须补证据修订，不照搬旧包能力 | 方案范围决定，非owner逐字裁决；旧v1独立能力保持 | S-7 | 能力广告与独立实现/真实样本对齐 |
| OD-7 | 新v2包与backend ID | **`agent/opencode-v2/`，ID/kind=`opencode`**；不恢复最老实现，不作为旧包子目录或alias | **decided，owner本轮明确指定** | S-0/S-2a/S-7 | 注册唯一、独立import、hello/RPC/SSV2/客户端识别均指向v2；旧ID数据隔离 |
| OD-8 | 旧opencode-web退役 | 新v2稳定门通过且owner确认后退役旧包/挂载；旧程序/历史/外部服务不卸载不删除 | 延续本轮讨论目标；具体稳定/执行门见§3.12，不是当前删除授权 | S-8/S-9 | 稳定证据完整后才改产品lineup/import；先验收、后退役 |

---

### 3.11 `opencode` 重用身份的接线与数据兼容门（S-0/S-7）

backend ID按owner裁决保持`opencode`。字符串空闲不代表所有代码/磁盘数据已清除：
本任务配套来源M=`1fe9c65d…`/I=`89f0a369…`亲核仍有旧分流，以下必须作为一次coherent
跨仓变更完成。只改注册名、目录名或显示名均不算新身份接入。

| 边界／已有锚点（均为§2.1任务配套，不是main） | 本版必须实施的接线 |
| --- | --- |
| 原`agent/opencode/`不存在；`deprecated/opencode/opencode.go:23`仍有归档init注册，`core/registry.go:12`同名会覆盖 | 新`agent/opencode-v2`独占`opencode`注册；main显式import/alias到新包；deprecated import guard继续禁止旧包；对重复注册/错误import加定向守卫，不依赖init顺序 |
| `go-bridge/main.go:326-329`见`id=opencode`与旧URL会注册`OpenCodeProxy`；`handlers.go:1264`有旧dispatcher | 新v2不创建旧proxy，缺旧proxy仍走core/SSV2通用RPC；旧`-opencode-url`/credentials不得激活v1 proxy或给新driver注入凭据。明确拆除新ID命中的旧专用分流，禁止靠改显示名掩盖 |
| `handlers_opencode.go:422,437`旧catalog/overlay，`handlers.go:1272`的catalog feature门 | 新列表/分页/目录/改名等走新包实现与规范catalog seam；身份能力分流不误到旧HTTP，通用catalog缓存按新generation隔离；专项负例确认旧proxy0调用 |
| `agent_descriptor.go:116,216`旧opencode轮询/health（`detectAgentStatus`内`case "opencode":`@:216、health探活调用@:229，A.17亲核）；`handlers_projection.go:664,1386`、`handlers.go:2697`的hydrate/create白名单 | 新driver提供结构化readiness/WireDescriptor/官方live订阅与rich history/create；更新真实拥有该语义的分流，保证新ID冷/热只进入同一Kernel，禁止开旧轮询或旧history旁路 |
| Swift `ProductAgentCatalog.swift:17`/`AgentSelectionStore.swift:12,62`，`RuntimeManager.swift:1157,1778,1817` | 新产品ID、白名单、独立配置/env、安装recipe与workspace snapshot同时适配；新旧选择互斥；不把新ID传给旧双送URL/凭据逻辑 |
| I `BackendModels.swift:69-76`把opencode解为旧`.openCode`；`BridgeProvider.swift:985`按kind缓存；`ServerViewModel.swift:885`只比较bridge+kind | 新增明确的`.openCodeV2`内部身份（wire仍opencode），只在协商的新v2标记与descriptor一致时识别；旧`.openCode`仅历史Codable兼容/不可用，不原地复活为v2。Provider、target匹配、初连/重连/发送与remote-web都按新身份建立 |

#### 身份协商与旧客户端

- 使用现有`hello.capabilities[]`（`go-bridge/hello_handler.go:15`）增加可选识别能力
  **`opencode_v2_backend_v1`**（拟议名，本版冻结）：新客户端显式声明，ACK回显是否
  协商成功；新driver descriptor提供同名capability。wire `id/kind`仍均为`opencode`。
- 缺该声明的旧客户端不得看到可用的新`opencode` descriptor/运行session，也不得接收
  其内容推送、离线重放/mailbox或通过指定backendId绕过；逐连接握手、RPC、subscribe/
  observe/replay在direct和Relay共同门控。对未协商连接可回原status集合中的
  `version_unsupported` descriptor与“需要升级客户端”原因，不能把实际存在的新产品
  隐成空backends后假报noAgents。全局Management状态可显示新产品，不能用
  “客户端应该忽略未知capability”代替服务端硬门。
- 新客户端识别到`kind=opencode`但新标记缺失时，标为旧代不支持/需要升级，不把它当
  `.openCodeV2`；新旧双方不匹配不伪造noAgents成功。该可选协商是版本化识别扩展，旧
  `opencode`语义不在无协商连接上改变；canonical pack明确同名ID再利用与兼容矩阵。
- 新iOS/remote-web需先具备上述识别/拒绝能力，Mac再开放新driver。只有新标记+当前
  descriptor+per-backend SSV2能力成立才启用新timeline writer；沿用Kernel唯一writer、
  事务fence/revision/readiness和旧connection generation拒绝规则。

#### 持久数据与缓存

- 公开ID仍`opencode`；内部driver generation固定为`opencode-v2`（与CLI patch版本
  分开），作为存储namespace/缓存身份的一部分。不是第三个backend，也不进入server
  URL伪装身份。此标记贯穿session key、Projection source、catalog/cache、pin与客户端
  target/snapshot/model偏好；旧无标记记录一律归legacy，不自动作为v2数据读取。
- M `projection_kernel.go:188-200`的checkpoint文件名只含backend/session，新epoch
  本身不隔离磁盘；为新driver使用generation子namespace，并把source identity绑定
  generation与真实上游来源。复用LoadValidated及既有fence，绝不读取旧无标记checkpoint
  补新timeline；旧文件保留，首次新v2用官方API重新hydrate。
- M `pinstore/store.go:110`的backend/scope/session之外还存在GetAnyScope/List/RemoveAll
  跨scope操作；v2使用独立generation存储namespace，所有读写/overlay/list/清理一致，
  不能只加scope而仍扫旧ID。`handlers_session_pin.go:201`的新ID解析要走新core接口，
  不强制旧proxy。旧pins原样保留，只有核实v2真会话后用户主动重新pin。
- I `BackendModels.swift:309`现cacheScopeKey只有kind/endpoint/username，
  `CCCodeBridgeBackendClient.swift:155`sourceIdentity仅transport generation/ID；新
  `.openCodeV2`+driver generation纳入保存target、内存client、snapshot与ProjectionStore
  key，remote-web同样分代。历史`.openCode`/`.openCodeWeb`目标不按新广告自动匹配，
  source/generation切换丢弃旧内存投影/晚响应，不删除持久旧历史或复制正文当新真值。
- 所有新增namespace只隔离本地CordCode副本，不修改上游session ID/DB，也不凭重名
  合并两代session。旧ID与旧driver数据读取在E-11负例中必须为0。

#### 用户切换的完整交互

1. 进入Mac工作站；过渡期旧行标“OpenCode Web（v1）”，新行标“OpenCode（v2）”，
   安装与服务状态各自真实显示。新行默认未接入，不因旧opencode历史或已装v2自动开启。
2. 用户在新行开启接入；旧行若正在接入，显示“将停止CordCode对v1的接入并连接v2，
   现有OpenCode服务和历史会保留”；用户确认切换。无额外端口/密码输入，服务未起的
   “启动”仍是§3.2的另一个明确动作，不把切换接入当启动服务。
3. SelectionStore一次保存互斥集合与revision；RuntimeManager经admission安全应用，
   旧driver停止订阅/扫描/接入（本次切换不得停止旧外部服务，也不得把旧managed child
   当迁移清理目标），新driver只发现/连接官方v2。忙碌显示等待，不先假装切换完成。
4. 新PID/epoch、已应用revision、注册集合与descriptor确认后显示新行连接状态。iPhone/
   remote-web重连后显式选择“OpenCode（v2）”的独立target/列表；按官方v2冷读，不承接
   旧`.openCode`缓存。失败留在新选择/真实原因，用户可显式切回旧行；不自动fallback。

存量选择不静默改名：只有`opencode-web`的旧用户保持原选择直到明确切换；最老
`opencode`无generation记录不是已启用v2意愿，保留为legacy记录并提示选择新产品，不能
因白名单重新加入就自动激活。若配置同时含新旧ID，产品侧拒绝提交，runtime侧拒绝装配
该冲突组合并给诊断；其余backend选择不丢失。

### 3.12 稳定后退役 `opencode-web`（S-9，受门控）

本期是完整独立v2交付，不拆“只读一期/写二期”。稳定门是旧driver退役的先决条件，
不能为赶退役先删除仍被用户依赖的包；本轮文档/评审不授权执行删除或卸载。

**退役前全部满足：**

1. 新包的官方UI/server/source版本、同版本真实样本与E-1~E-11完成；E-2~E-5完整
   request/event/reload/error闭环，内容/目录对账数字交付；未知generation无静默fallback。
2. S-0~S-8定向单测、Mac Release与配套iOS增量构建/安装、受影响remote-web交付、
   运行代际/独有日志或descriptor特征均证明新代码在跑；组件/运行态/owner结论分别记录。
3. owner在一张direct/Relay矩阵确认：外部与iOS发消息、冷历史/分页、首回合与持续流式、
   reasoning/tool/附件、中断、permission/form、重连/凭据轮换、手动启动、目录partial。
   有未验证项按n/N列出，不能用测试绿或“跑了一会”替代稳定验收。
4. 过渡期没有新旧同服务双订阅、旧proxy请求、混合writer或旧缓存污染；真实失败有原因，
   新v2缺席不自动回旧服务。
5. 依赖闭包与退役可分离验证满足§3.0：排除旧包后新v2和中立公共包**零源码改动**即可
   构建/定向测试，v2测试不依赖旧fixture目录。若仍需批量替换v2引用，说明独立性未完成，
   不得进入退役。owner确认稳定并明确执行退役后才进入S-9。

S-9只退役CordCode旧适配入口：产品catalog/默认与可选挂载/blank import、旧专用动作和
仅旧包持有的代码，更新protocol/双端持久选择迁移与文档；旧selection迁为“旧接入已退役”
而不是全文件损坏或自动开启新v2。移除旧包前核对引用与冻结旧样本证据，新包必须能在不依赖
旧包时构建/运行。保留旧targets/pins/历史可追溯，不把它们合并进新backend。

退役**不包括**停止或卸载用户v1 CLI/4096、删除OpenCode配置/DB/会话、集成main或重写
Git历史。它们需要各自明确授权，不能由“opencode-web已稳定替代”推导。

## 4. 证据与实施前置门

证据类别标注：〔源码〕/〔活体〕/〔组件〕/〔拟议〕。状态：verified / pending。

| ID | 命题 | 类别/来源 | 样本与索引 | 断言与负例 | 获取方式 | 状态 | 未满足时阻塞 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| E-1 | `/api/info` 是 v2 身份/探活契约：no-auth 401、Basic 200、`{version,pid,urls,paths,capabilities}`，pid 与 service.json 一致 | 〔源码〕`server.ts:43-51,5-19`、`service-probe.ts:49-90`；〔活体〕本机 49374 | 活体响应已存本方案附录 A.3（脱敏：仅去 password，字段结构保留） | 断言如左；负例：pid 不符→视为不可用（官方语义） | 已完成（2026-10-19 curl；r1 评审轮+修订者各自独立复测，A.8.4） | **verified** | — |
| E-2 | `/api/event` SSE 帧形状与附录 B 映射表逐条吻合（turn 生命周期/流式/reasoning/permission/form/fs） | 〔源码〕`event.ts:44-52`、`event-manifest.ts:41-72`、`session-event.ts`；〔活体〕待捕 | 计划路径 `agent/opencode-v2/testdata/official-2.0.32/event-stream-*.sse`（尚不存在） | 每个映射类型≥1 正例；负例：未知 `rpc.*`/未知 type 不崩、计数丢弃 | 实施期在 49374 订阅并截取（desktop 发起真实 turn），脱敏后归档 | pending | S-3新映射与S-2b新产品放行 |
| E-3 | prompt 请求/响应形状（含 `id` caller-messageID、attachments parts、delivery）+ **scoped 写目录上下文矩阵**（`location[directory]` query / `x-opencode-directory` header / 不带，三态各一例，确定最小必带形式） | 〔源码〕`session.ts:396-407`；〔活体〕待捕 | 计划路径 `agent/opencode-v2/testdata/official-2.0.32/prompt-*.json` | 正例：文本+图片+文件各 1，含目录三态矩阵；负例：坏 sessionID→SessionNotFoundError 透传 | 实施期真实 POST（interrupt/permission reply 一并按目录三态采样） | pending | S-5 写路径 |
| E-4 | 消息历史 parts schema（id 必填、type 集合、role 映射）+ cursor 分页游走（`cursor.next` 至耗尽，条数与库表对账） | 〔源码〕`message.ts:68` 与 schema 组；〔活体〕部分已证（附录 A.2：v2 读 200，4 页/132 条复测） | 计划路径 `agent/opencode-v2/testdata/official-2.0.32/messages-*.json` | 正例含 reasoning/tool/text；负例：未知 part type fail-closed 计数 | 实施期真实 GET | pending | S-4 映射固化 |
| E-5 | 新包permission/form完整生命周期：官方list/get/reply/reload与resolution；**form应答走session作用域路由**（`session.ts:796-870`），不以旧preview分支为依据 | 〔源码〕`permission.ts:23-124`、`form.ts:12`（全局列表）、`session.ts:796-870`（session 作用域 form：list/reply `:842`/cancel `:858`）；〔活体〕待捕 | 计划路径 `agent/opencode-v2/testdata/official-2.0.32/permission-*.json`、`agent/opencode-v2/testdata/official-2.0.32/form-*.json` | 正例：permission 一次 ask 流 + form 完整流（`form.created → reply → form.replied`）；负例：过期 requestID/formID 应答→错误透传 | 实施期触发一次 ask 与一次 form | pending | S-5 permission/question |
| E-6 | 共享服务真正停止/外部重现/注册轮换；只读重附与安全应用，不自动 spawn/fallback | 〔源码〕`service-registration.ts:40-71`；本仓 `RuntimeManager.swift:462-523,709-723`；〔活体〕待确认 | 实施报告记录 endpoint/action/runtime generation 与脱敏身份 | 无服务保持“未启动”；相同身份掉线不重启 bridge；经校验的新 id/pid/password 才应用；自动应用至少隔 60s，ready 清 stuck 计数不破坏速率界 | 受控 fixture 先验；owner 已明确授权的实施验收窗再停/起真实共享服务，不能为采样擅自终止用户当前服务；退出 Desktop 不作为停服务证据 | pending | S-1 的验收标准 4/5，不阻塞文档，不构成当前服务操作授权 |
| E-7 | 目录隔离：TCC/单桶错误不阻断成功桶，全部失败与真实空分开；列表/指纹/告警同源 | 〔活体〕更正后的 `GET /api/session?directory=<TCC>` 200 n=1（A.8）只证明那次可达；〔拟议〕S-6 独立桶行为 | 实施报告对账 `N=A+K`、成功目录官方分页 ID 集合与 bridge 集合；失败目录不计作 0 | EPERM/超时/某页 schema 错误只丢该桶；成功空桶算成功；全失败报错；恢复自动清告警且 fingerprint/fence 更新，active 历史不被删除 | 单桶故障注入验证内部隔离；官方 v2 真实 GET 归档证明形状，不能用 fake server 反证外部契约；owner 授权的不同启动环境矩阵 | pending | S-6 完成门，OD-5 已决定，不再有分支选择门 |
| E-8 | 插件断代导致启动窗口 `/api/model` 503（awaitPlugins 5s 门）对 attach 探活/首读的影响边界 | 〔源码〕`location.ts:36-47`（5s 超时→ServiceUnavailableError）；〔活体〕已观测 4×503（附录 A.5） | 现象已录 | 边界：探活不得用 `/api/model`；首读 503 需限速重试而非判死 | 实施期重放 | pending | S-2a 的重试策略验收 |
| E-9 | 用户手动启动、v2 定位/安装指引、detached 生存期及冲突门 | 〔源码〕§2.2 启动/安装/发现四行；官方 `service.test.ts:381,637`、`service-lifecycle.test.ts:19,59`、`updater-install.test.ts:140,206`；〔磁盘〕bundle/staged 2.0.32 及 v1 1.19.3 的前轮静态旁证；〔活体〕手动动作未执行 | 实施期归档只读 version/help、固定包 metadata、argv/去敏 stderr/注册与 pid 时间线，不写密码；平台 detached seam 的测试归 owning Mac 类 | 无服务只在点击后 spawn；v1-only 给 v2 命令；注册不明/活 pid/非 loopback/remote 确认；并发让位、退出0未ready、超时不杀/不清注册、关接入后不回写、原 v1 不受改动 | 源码已核；固定包可用性先只读查询；动作/安装执行须未来实施授权与不影响现有服务的受控环境，不用当前用户真实服务试错 | pending | S-1 启动/安装交付门；不能以“实现时再定”省略主路径 |
| E-10 | 新opencode的本机catalogObservation/Swift告警codec | 〔源码〕既有workspace snapshot；〔拟议〕§3.7 | owning Go/Swift fixture/未来Management快照 | partial不改readiness为失败，旧结果拒绝；本机告警本身不增跨端timeline消息 | 内部契约单测＋owner矩阵 | pending | S-6完成门 |
| E-11a | 身份/路由/namespace契约冻结 | 〔源码〕§3.11双仓锚点；〔拟议〕marker与generation规则 | S-0的canonical+mirror字段/迁移/拒绝矩阵与完整来源 | 明确新包/唯一注册、opencode_v2_backend_v1协商、旧proxy零调用、旧target/checkpoint/pins不继承、选择互斥 | S-0冻结；不依赖S-7实现或S-8运行 | pending | S-7及各跨端实现的前置输入 |
| E-11b | 放行前的身份/跨端/依赖组件证明 | E-11a契约＋S-7实现 | direct/Relay握手与RPC/observe/replay负例、codec、新旧target/checkpoint/pins结果；新包/公共包生产与测试依赖闭包及fixture路径检查 | 唯一注册；新包/中立公共包对旧包/子包无直接或传递依赖；旧client无marker不接新内容；旧数据0误读/0误删，不双挂 | **S-7产出组件证据**；不要求S-8生产路径；bridge过渡装配双import不混入新包根闭包 | pending | **S-7组件完成与S-2b放行**；无环依赖 |
| E-11c | 放行后的真实身份/兼容验收 | E-11b＋S-2b已完成，进入S-8 | 新Mac/iOS/web真实部署身份、受控direct/Relay日志/进程与owner矩阵 | 新ID真实走v2、旧proxy无调用、旧客户端拒绝、旧数据不污染、退役旧选择不使全文件失效；组件与运行证据分开 | **S-8产出运行态证据**，当前未授权执行 | pending | **S-8交付完成与S-9稳定/退役门**，不作为S-2b或S-7前置 |

Gate 映射：
- Gate A（证据）：S-0先冻结独立身份/SSV2 writer与官方调用链；S-2a依赖E-1/E-8；
  S-3依赖E-2，S-4依赖E-4，S-5依赖E-3/E-5，S-6完成依赖E-7/E-10；S-1手动启动/
  安装/重附完成依赖E-6/E-9。**S-2b新产品放行前置=E-2/E-3/E-4/E-5齐备＋S-3~S-6
  映射/读取就位＋E-11b跨端组件身份/分流门通过**；旧包不解除v2隔离。产品活体验证在放行后，
  受控证据采集/组件测试不等同于开放产品。
- Gate B（产品决定）：OD-2/OD-5/OD-7已明确；无自动ensure/fail-all备选。OD-8退役
  必须等§3.12全部稳定证据与owner明确执行指令，S-9不得提前；不授权卸载/停4096。
- Gate C（阶段/授权）：v1.7 已由 r6 完整复审通过（APPROVED，0 阻塞/2 建议级，A.17）；
  v1.8 仅收编 F-16/F-17 并回写评审状态，设计命题零改动，按 r6 闭合标准无需再轮评审。
  当前仍只文档，“暂时不写代码”有效；E-2~E-11不授权当前POST/安装/服务操作；实施启动仍须 owner 另行授权。

---

## 5. 实施切片（单期交付，按序实施）

| 切片 | 需求/可见结果 | 组件与复用方式 | 依赖/前置 | 最小验证/完成证据 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-0 | 独立driver/身份与官方语义冻结 | 新包边界、§3.0公共设施依赖方向/必要helper归属清单、§3.11路由/数据清单；官方逐行为证据链；Kernel/fence唯一owning层 | 本版评审通过及实施授权；来源门fresh；OD-7 | 新包与公共helper依赖图、注册/分流/marker/namespace契约；所需helper先中立化，不建立过渡旧包API依赖 | 官方链/样本不明不编码，不靠旧包补 |
| S-1 | 新OpenCode行：只读发现/手动启动/准确命令/重附，无4096 fallback | 扩展现有Swift动作与通用inventory设施，独立v2配置/env；官方serve--service单例，旧v1设置保持 | S-0；OD-1/2/3；E-1/E-6/E-9；选择/身份按E-11a（S-0冻结契约） | v1-only/检测失败/XDG/完整身份/单飞/detached/超时不杀/60s应用速率界/新旧选择互斥 | 不自动spawn或切旧服务，真实失败 |
| S-2a | 新包注册与官方/info探测，产品未放行 | 新`agent/opencode-v2/{agent,client,probe}.go`（拟议）；`opencode`唯一注册、独立WireDescriptor/readiness | S-0/S-1；E-1/E-8；无旧import/proxy | pinned2.0.32支持/其它版本拒绝；缺endpoint/认证失败；旧包继续拒绝v2 | unverified/unsupported诊断，不fallback |
| S-2b | 新opencode产品条件放行 | 新driver与双端协商门；**不修改旧包v2quarantine来接v2** | **E-2/E-3/E-4/E-5齐备＋S-3~S-6映射/读取就位＋E-11b组件身份门通过** | 放行前后注册/descriptor与连接能力快照；S-7跨端组件完成后产品活体验证 | 未满足不得广告available或放行旧客户端 |
| S-3 | v2官方/event流与断线恢复，单Kernel写入 | 新`opencode-v2/events.go`，按官方consumer/样本实现；复用公共EventPublisher/Kernel，不复制旧heal/dedupe/watchdog状态机 | S-2a/E-2；产品验证在S-2b后 | typed事件/顺序/重复/断线中终态/permission恢复；writer/fence测试；真实对账 | 未知/未证事件诊断，不本地猜完成 |
| S-4 | v2 sessions/messages/models/providers读路径 | 新包typed decoder/官方分页/cold hydrate；新ID路由与cache namespace | **S-2a/E-4与E-11a冻结契约**；组件结果汇入E-11b；真实验证在S-2b后归E-11c | 真实三类响应/多页cursor、role/parts、冷历史计数与官方源对账、旧cache0读取 | 错误可见，未知shape不递归解析 |
| S-5 | v2 prompt/interrupt/permission/form/附件/稳定correlation | 新包session/permissions/forms，官方UI字段与调用顺序；core接口与Kernel复用 | S-2a/E-3/E-5；产品验证在S-2b后 | 实际请求/事件/reload/error链、首回合、双向发送与权限表单、中断/错误 | 不copy旧parser/send/recovery，不伪成功 |
| S-6 | 新包registry目录隔离、Mac告警/重试 | 新projects/sessions聚合；本机snapshot/Swiftcodec；共用catalog失效设施但新generation隔离；旧包仅有界OD-5修正 | E-7/E-10；组件S-2a，产品S-2b后 | `N=A+K`/成功ID对账；一桶错/全失败/空/恢复；同源指纹/fence与active历史不删 | partial明确告警，无fail-all |
| S-7 | 跨端opencode新身份/选择/存储隔离与protocol同步 | §3.11所列Mac dispatcher/proxy/catalog/descriptor/Kernel/pins、Swiftcatalog/selection/env、iOS `.openCodeV2`/Provider/cache、remote-web marker；canonical+mirror | **S-0与E-11a冻结契约**；与S-1~S-6并行，组件完成早于S-2b；不依赖S-8 | 新旧hello/RPC/observe/replay组件矩阵、旧ID数据0误读、raw writer0、选择互斥；**新包/公共包直接与传递依赖旧包为0（含测试/fixture）**，产出E-11b与两端定向build；生产核验留S-8/E-11c | 不识别就拒绝，不把旧opencode当v2 |
| S-8 | 新v2统一验证/交付与稳定验收 | Mac正式Release、iOS一次增量安装、受影响web交付、进程/日志、owner合并矩阵、活文档 | S-0~S-7组件完成、S-2b放行、E-11b；E-1~E-10按完成门 | 本片产出**E-11c真实身份/兼容证据**和§6真实场景/对账；组件/生产/owner分级 | 保留真实失败，旧包保留 |
| S-9 | 稳定后退役旧opencode-web | 移除旧产品装配/import/专用接入；旧selection保留可诊断，文档更新 | **§3.12稳定门全部满足＋owner明确执行退役** | 受控排除旧包/测试资源后，新v2及公共包零源码/import/调用修改即可构建/同组定向测试；保存target不损坏；旧程序/DB/历史未清理 | 仍需改v2引用则先解耦，不允许靠退役大改v2；未过门不删除 |

新旧接入按用户明确选择切换（§3.11），不静默迁移旧ID的配置/数据；不写上游DB、
不改Desktop状态、不卸载旧CLI。切片表是依赖图，S-2b在S-3~S-7组件完成后执行，
不是按表行号先放行再补代码。S-1状态/手动启动、S-6告警、S-7跨端身份都是同一期必交；
S-9只在稳定门后执行。本轮没有实施任何切片。

---

## 6. 验证、风险与交付

### R→S→验收映射

- R-1→S-1/S-2a/S-2b/S-3（desktop turn iPhone 流式）
- R-2→S-4（历史可见，附录 A 场景复测）
- R-3→S-5（发消息/中断/应答）
- R-4→S-1（安装指引/独立状态轴/显式启动/只读回附，含 §3.2 完整流程）
- R-5→S-6（TCC 聚合）
- R-6→S-1/S-2a/S-7（secret/身份边界）
- R-7→S-0/S-2a/S-7（独立包/注册/客户端与数据代际）
- R-8→S-7/S-8/S-9（无自动数据继承、稳定后退役）

### 测试计划（按风险定级 D3：状态/协议/跨面）

- 本轮 D0：只做文档空白/链接/引用与 R-OD-E-S 一致性静态检查，不运行产品测试、编译、
  安装或服务操作；本节以下均为未来实施验证。
- 组件级：resolver 的 XDG/合法/坏JSON/非loopback/活pid但未ready/死pid/版本不支持；
  inventory 的 v1-only/同名多CLI/超时与missing区分、source-aware固定版本prefix recipe；
  行状态纯函数、点击单飞、detached/唯一wait、过期generation、不自动启动/回退；
  endpoint同URL换password/pid、相同身份故障不重启、60s独立速率界、admission等待；
  probe/envelope/事件/parts/prompt/permission/form样本定向测试；S-6单桶隔离/全部失败/
  真实空/恢复/告警codec/同源指纹和cache fence。
- owning tests：新`agent/opencode-v2/*_test.go`与`testdata/official-2.0.32/`独立样本；
  Mac Workspace/RuntimeManager/EndpointResolver/Selection/recipe/snapshot相关定向类；
  Go注册/import/通用RPC/canonical catalog/SSV2/pin/checkpoint与hello协商守卫；iOS
  BackendKind/BridgeProvider/target/cache/ProjectionStore身份/codec定向类；remote-web
  backend选择/握手/重连/store命名空间定向测试。旧opencode-web测试仅验证过渡v1与
  有界OD-5修正，不能当新v2语义测试。真实wire fixture必须来自E-2~E-5官方样本。
- 真实路径：S-2b后验证共享服务下SSE/历史/写路径/断线冷对账与目录覆盖；手动停止/新启动/
  替换共享服务、真实POST/安装样本须未来明确授权且先证明不扰动用户现有环境。不能默认
  复用现在49374/4096“都有”的旧快照，也不通过反复关Desktop推断服务退出。
- 真机/产品验收：owner按下表一次性人工执行，agent完成Management API/进程/日志证据；
  无UI automation、无截屏/抢前台，agent-device永久停用。
- 成本纪律：本轮D0只静态检查；未来实施为D3跨仓身份/状态修改，复用稳定DerivedData，
  定向组后Mac Release与配套iOS各一次增量交付（真机可用集中安装一次），受影响web按
  其既有交付纪律处理。不因目录告警额外改timeline；不默认全量、clean或重复安装。

### Owner 人工验收矩阵（实施并交付后，一次性给出）

| # | 前提条件（网络、模式、session 状态） | 动作 | 应看到 |
| --- | --- | --- | --- |
| U1 | MacBridge已运行、OpenCode接入开启；无共享服务、有已验证v2 CLI | 进入OpenCode（v2）行，先等待一轮检测，再点“启动” | 点击前“未启动”＋“启动”，无自动服务；点击后启动中→正在接入→连接正常，无额外4096 |
| U2 | 没有v2服务；仅v1安装或完全未安装 | 查看并复制安装命令，用户执行后回到行“重新检测” | 显示“OpenCode v2未安装”与`@opencode/cli@2.0.32`独立prefix命令；复制不自动安装；v1保留；装后仍需明确“启动” |
| U3 | 一个v2候选存在但版本读取失败，或注册文件不明 | 重新检测 | 显示具体检测/权限/注册失败，不能显示“未安装”或强启已有进程 |
| U4 | Desktop/CLI已启动健康共享服务 | 开启OpenCode接入；随后关闭接入 | 开启只附着已有服务，关闭仅断开CordCode；Desktop原会话继续可用，无共享服务停止 |
| U5 | 已接入，owner允许受控停/起或官方升级；其它bridge任务可观察 | 停止真正的共享服务，再从官方工具启动；或官方升级轮换 | 服务消失后未启动/错误可见，无自动spawn/4096；外部恢复后只读回附；忙碌显示等待；无重启风暴 |
| U6 | 健康共享服务，至少1目录失败且至少1目录成功 | 刷新全局列表、展开Mac行告警；恢复目录后点“重试” | 成功目录可见且刷新正常；告警给失败数/原因；恢复后自动清告警；不删除既有会话历史 |
| U7 | 分别为全部目录失败、真实空registry两个场景 | 刷新列表 | 前者真实加载失败，后者空列表；不会把全失败显示成没有会话 |
| U8 | iPhone通过direct/Relay连同一v2，已有session A | Desktop发一条；iPhone打开同会话，再发送/中断/应答permission和form | 流式、冷历史和双向写路径按R-1~R-3正常；两种网络分别记录结果 |
| U9 | 旧opencode-web显式external/legacy来源，或新OpenCode接入关闭 | 进入工作站/重新检测/重启bridge | 不被shared发现劫持，不因安装自动启用/启动；旧来源保留 |
| U10 | 旧v1接入开启、新v2未开启；有活跃或空闲任务两态 | 在新OpenCode行开启并确认切换 | 一次selection原子互斥，忙碌等待；不关闭旧外部服务；新ID为opencode、显示v2，失败不自动切回 |
| U11 | 升级客户端留有最老opencode/旧opencode-web target、snapshot与pin | 连接新Mac并选择OpenCode（v2） | 新target/列表独立冷读；旧历史保留但不混入新timeline/pins；同session名不作为迁移证据 |
| U12 | 旧客户端未声明v2识别能力；direct/Relay各一次 | 连接开启新opencode的Mac | 明确需要升级/不可用，无新内容投递，无RPC误入v1；不报已连接无Agent假空态 |
| U13 | §3.12稳定证据齐全且owner已明确退役 | 执行退役后重启/重连 | 只下线旧CordCode适配入口；旧设置显示退役可诊断，新v2正常；v1程序/配置/历史未删 |

### 主要风险与应对

| 风险 | 触发信号 | 应对 |
| --- | --- | --- |
| 上游2.x wire漂移 | E-2~E-5样本与服务版本不符 | 版本/样本绑定，未知generation隔离并报版本不支持；不跨v1 fallback |
| Desktop升级轮换密码/pid | 旧凭据401或注册身份变更 | §3.2只读重发现，验证新身份后安全应用；同URL也更新；不terminate旧owner |
| 没有v2或服务未起 | inventory missing或确认无活owner | 准确安装命令或“启动”按钮；无隐式ensure/4096；错误不当missing |
| 官方ensure会停止不响应daemon | `Service.ensure`源码的3次timeout/replace分支 | 选择直接官方`serve --service`单例入口；遇不明活owner只报错，不重写恢复算法 |
| 已配置非loopback/remote/不同端口 | service config/注册与本地附着边界冲突 | 启动前呈现并按§3.2阻断/确认；不改上游配置或悄悄开启外部环境 |
| Detached/stdio不等价 | App退出时daemon死亡或pipe满 | E-9平台生命周期组件验证，持续排空8KiB尾部；未证明不能交付“共享常驻” |
| 插件启动窗口503 | 首读503 | 探活用/api/info；首读限速重试并保留原因（E-8） |
| 单目录失败造成部分列表 | catalogObservation partial | 成功桶继续，Mac行明确告警/重试；不删历史、不报全量；全失败报错 |
| 事件映射漏类型 | 未知type计数上涨 | fail-closed计数+采样日志；附录B随真实样本补 |

### 阻塞清单与下一阶段入口

- OD-2/OD-5 已决定；不再要求 owner 选择 ensure-start 或 fail-all。
- r6 已对 v1.7 完整复审通过（独立包/身份/跨端隔离、公共依赖边界与退役门在内，A.17）；
  v1.8 为建议收编版。E-2~E-11按Gate阻塞相应实施/完成。
  **S-2b仅在E-2/E-3/E-4/E-5齐备＋S-3~S-6映射/读取就位＋E-11b跨端组件门通过后执行**。
  S-9还需§3.12稳定验收与owner明确执行退役；不把先前的命名裁决当删除/卸载授权。
- 本轮交付仅修订文档与静态检查，业务代码、测试、构建/安装、OpenCode服务操作均不执行。
  独立评审通过后仍须 owner 另行授权实施。

---

## 附录 A：2026-10-18/19 运行取证索引（本机，脱敏）

A.1 版本与进程：desktop 2.0.32（Electron，`/Applications/OpenCode.app`）；v2 共享
服务 `serve --service` PID 多次更替、url 恒 `http://127.0.0.1:49374`
（service.json 注册）；v1 managed 4096 原进程 1.19.0（10-14 起），2026-10-19
02:19 经 owner 授权重启后为 1.19.3（PID 28164）。

A.2 「一库两服」断裂实证：desktop turn（02:23，ses_fe2dc9598ffe0b…）只写 v2 表
`session_message`（user/assistant/idle 三行）；同期 v1 `message`/`part` 新增
0 行；4096 对该会话最新一条停在 09-21 19:53 → 流式（进程内事件总线）与历史
（表分裂）双重不可见。v2 服务 `/api/session/<id>/message` 读同会话 200；端点为
cursor 分页（默认 50 条/页）。**口径（r1/F-7 统一，首版两处矛盾数字作废，见
A.8）**：2026-10-19 评审轮复测 = 首页响应体 277,979 B / 50 条，跨页全量
4 页 / 132 条（该时点）；首版所写「507 条/27MB 量级」与「1.97MB/507 条」为
未注明口径的旧抓取数字，今日已不可复现，不再引用。

A.3 `/api/info` 活体样本（2026-10-19，49374，去 password）：
`{"version":"2.0.32","pid":<活>,"urls":["http://127.0.0.1:49374"],"paths":{"tmp":"/private/var/folders/…/T/opencode"},"capabilities":{"persistentPty":true}}`；
no-auth → 401；带 Basic（`opencode:<service.json password>`）→ 200。

A.4 TCC 目录：v1 serve 对 `~/Documents/todo-app-sisyphus` `lstat` EPERM → 500
（go-bridge 每分钟聚合失败，01:27:12 起）；同一目录经 v2 服务（desktop 子进程）
可达的初证——**r1/F-1 更正**：首版本条写的探测形式
`/api/session?x-opencode-directory=…` → 200 是无效证据（该参数被服务端忽略，
响应与无参 baseline 字节级相同，见 A.8 矩阵）；正确形式
`GET /api/session?directory=<TCC>` → 200 且 n=1、全部会话 location 均为该目录
（2026-10-19 复测）。E-7 以更正后形式为准。

A.5 插件断代：v1 时代两个插件（本地 notify 插件、`opencode-openai-codex-auth@4.4.0`
peer `@opencode-ai/plugin ^1.0.150`）在 v2 加载失败；npm 重装期间 `/api/model`
4×503（span≈5000ms=awaitPlugins 5s 门）。

A.6 恢复链复盘：v2 desktop 恢复 tab 指向 4096 → v1 `/api` 读 v2 投影坏行
（reasoning part 缺 `id`，SchemaError `content[0].id`）500 → 渲染层致命错误页；
删除该 server 条目+停 4096 后 desktop 正常。Swift `syncDesktopConfig` 曾把
4096+密码写进 desktop 状态（S-1 跳过该同步的依据）。

A.7 membership 回归：v2 首启导入旧状态并删除 `opencode.global.dat` 等文件（日志
`imported:842` 是导入状态条目数，非 842 个文件；官方 `packages/desktop/src/main/storage/legacy.ts:65-88`
返回 `rows.length`，v1.5 更正原记录计数单位）→
go-bridge membership 由 desktop-persist(12 目录) 回退 registry(24 目录) →
引入 A.4 的 TCC 目录 → catalog 每分钟失败。

A.8 证据更正记录（r1，2026-10-19；本轮评审复核 + 修订者独立复测，均只读 GET，
凭据不落输出；复测环境 = v2 共享服务 127.0.0.1:49374，version 2.0.32，
service.json pid 29415 与监听进程一致）：

1. **A.4/E-7 探测形式更正（F-1）**：`/api/session` 目录参数矩阵（响应 md5 对账）
   ——`?location[directory]=<TCC>`、`?x-opencode-directory=<TCC>`（query）、
   header `x-opencode-directory: <TCC>`、无参四者字节级相同
   （md5 `db90e583f8b1cd1e744736c4ec843291`，n=50 全局列表首页），即**列表路由
   对这三种形式一律忽略**；唯一过滤形式 `?directory=<TCC>` → md5 不同、n=1、
   全部会话 location 均为该目录。结论：列表过滤 = `SessionsQuery.directory`
   （`session.ts:168-174`）；`location[directory]`/header 仅进入 scoped 路由的
   location 中间件（`location.ts:71-79`）。首版 A.4 的 200 属 no-op 探测错误归因。
   附带核实：scoped 读（`/api/session/<id>/message`）带与不带 header 响应相同
   （md5 一致），scoped 读不强制目录上下文；写路径未采样，进 E-3 矩阵。
2. **样本数字口径统一（F-7）**：A.2 首版「507 条/27MB」与 §2.4 首版「1.97MB/507
   条」矛盾且今日不可复现；以 2026-10-19 复测为准（首页 277,979 B/50 条、跨页
   4 页/132 条、`cursor.next` 游走），并确认该端点 cursor 分页（进 §3.5/S-4 断言）。
3. **envelope 分型复核（F-5）**：`/api/session` → `{data,cursor}`（无 location）；
   `/api/model` → `{location,data}`（157 项）；`/api/project` → 裸数组（27 项，
   键集 id/canonical/icon/sandboxes/time/vcs）。首版「统一 `{location,data}` 解包」
   表述作废。
4. **E-1 复测**：`/api/info` no-auth 401、Basic 200，body 与 A.3 逐字段一致
   （version 2.0.32/pid 29415/urls/paths.tmp/capabilities.persistentPty）；
   `/api/health` → 404（serve 服务端无此路由佐证）。
5. **上游锚点行号更正（F-4）**：全部按 r1 核验落回正文 §2.2/§2.3；其中
   `session.compacted` 实际声明于 `session-compaction-event.ts:7`、
   `filesystem.changed` 于 `filesystem.ts:9`（附录 B 已对齐）。

A.9 更正记录（r2-meta 纠错轮，2026-10-19）：r2 对 v1.1 给出 APPROVED 后，元审核
r2-meta 定向复核 confirm=false（4 项），按通过后纠错流程修订本方案。本轮纠错前
亲核来源状态（P0 门点复核，与 r2-meta §0 一致）：本仓工作树 `1fe9c65d…`（未跟踪
4 个 docs 文件）；opencode-v2 detached tag `v2.0.32` = `843a6c51…`，porcelain 空；
配套 iOS 工作树 `89f0a369…`，porcelain 空；旧 checkout `dev` = `22e771f5…`，
porcelain 空。逐项更正如下（格式：原错误 → 来源 → 旧/新身份 → 影响范围与处置）：

1. **[锚点-行号不符] r2 §2.3「ensure 形状 `service.ts:27-35`」摘录**（r2-meta
   问题 1）。原错误：r2 摘录把「Defaults to `opencode serve --service`」注释标为
   `:31`，且该行标注「本轮亲核/定位准确」。本轮亲核（同一 pinned 提交 `843a6c51…`、
   工作树干净，排除版本漂移）：该注释实际在 `packages/client/src/service.ts:29`，
   `:31` 是 env 变量注释；`:30 readonly command?` 与区间 `:27-35` 无误。旧/新身份：
   摘录子行号 `:31` → `:29`（+2 漂移）。影响范围：仅 r2 报告摘录列的引用精度；
   方案正文区间锚点与 OD-2 引用的 spawn 命令形状命题不受影响。处置：§2.2 ensure
   行补注 `:29`/`:30` 精确子行号；r2 报告为轮次证据不回改，以本记录为准。
2. **[锚点-行号不符] r2 §2.3「scoped 路由 `location.ts:71-79`」摘录**（r2-meta
   问题 2）。原错误：r2 摘录把 `query.get("location[directory]") ||` 标 `:76`、
   header 三元标 `:77`，且该行标注「本轮亲核（r1 亦核）/定位准确」。本轮亲核：两者
   实际在 `packages/server/src/location.ts:74/:75`，`:76-77` 是
   `return Location.Ref.make({`/`directory: AbsolutePath.make(…)` 构造两行；区间
   `:71-79` 与两机制拆分语义无误。旧/新身份：摘录子行号 `:76`/`:77` → `:74`/`:75`
   （+2 漂移）。影响范围与处置同第 1 条（§2.2 机制二行已补注 `:74`/`:75`）。
3. **[来源清单-配套工作树缺席] 方案 §2.1（及 r1/r2 来源表）未列配套 iOS 工作树**
   （r2-meta 问题 3；元审核注明不单独归责 r2）。原缺失：「iOS 仓无代码改动」这一
   跨仓主张（承载 R-1~R-3 与验收标准 1-3 的 iOS 面命题）无来源身份记录，也无显式
   配套判定。新身份：§2.1 新增末行——
   `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` @
   `feat/claude-long-session-projection-loading` @
   `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52`，未提交状态干净（本轮亲核）；配套
   判定=两侧 `git worktree list` 各仅存在一条本分支族工作树（本轮亲核），唯一配套
   对应成立，本方案对该仓零代码改动、仅作来源身份记录。影响范围：来源清单完整性
   （P0 模板「配套仓库路径/分支/提交」必填字段补齐）；设计内容零变化。
4. **[来源清单-引用组合未入表] r2 §2.1 缺旧 opencode checkout 行**（r2-meta
   问题 4）。原错误：该来源组合在 r1 来源表存在、且被 r2 §4 F-4 闭合叙述引用
   （「旧 checkout 分支名已更正为 dev」），但 r2 §2.1 只列 2 行——被引用来源组合
   未入表，相对 r1 覆盖回退。本轮亲核：旧 checkout 实际状态 `dev` @
   `22e771f5893e255ea78fc10557ed3fb98d3a1072`、porcelain 空，与方案 §2.1 既有行
   及 F-4 闭合事实一致（闭合事实本身无误）。处置：方案 §2.1 该行本就存在（方案侧
   无回退），其未提交状态由「未逐一核对」升级为「干净（纠错轮亲核）」；r2 报告侧
   缺口以本记录留痕（r2 报告为轮次证据不回改）。

本轮 4 项均为记录精度/来源清单完整性级更正：不改动任何 R/OD/S/E/Gate/验收标准；
r2 对 v1.1 的 APPROVED 所依据的全部设计命题在 v1.2 中逐字未动（变化仅 §1 头部、
§2.1 来源表、§2.2 两处行内子行号补注与本附录）。r2 建议 F-10~F-13 维持
「实施期吸收、无需再轮修订」的原判，由实施期（exec-plan 拆分/S-1 实施）承接。

A.10 更正记录（r3 复审采纳轮，2026-10-19）：r3 复审对 v1.2 给出 APPROVED（r2
通过结论恢复有效，blockers=0），唯一新建议 F-14（建议级）：附录 B 两处事件锚点
缺包路径。本轮亲核（pinned 上游 `843a6c51…`＝tag v2.0.32、工作树干净）核实
成立并采纳，逐项如下（格式：原错误 → 来源 → 旧/新身份 → 影响范围与处置）：

1. **[锚点-包路径歧义] 附录 B「permission.asked / permission.replied
   （`permission.ts:45-47`）」与「form.created / form.replied /
   form.cancelled（`form.ts:173-175`）」两行（r3-F-14）**。原问题：permission.ts
   与 form.ts 在上游两个包重名——`packages/protocol/src/groups/`（135 行 /
   26 行）与 `packages/schema/src/`（67 行 / 177 行）；方案 §2.2 同名引用的是
   groups 路由文件（`permission.ts:23-124`、`form.ts:12`），而附录 B 的
   `:45-47`/`:173-175` 仅在 schema 包命中——实施者按 §2.2 的文件路径找附录 B
   行号必然扑空。本轮亲核：schema/permission.ts `:45` asked、`:47` replied；
   schema/form.ts `:173-175` created/replied/cancelled 三行逐字命中；
   groups/form.ts 仅 26 行，`:173-175` 在该文件不可能存在。旧/新身份：
   `permission.ts:45-47` → `packages/schema/src/permission.ts:45-47`；
   `form.ts:173-175` → `packages/schema/src/form.ts:173-175`（行号与事件语义
   本身无误，仅补包路径）。影响范围：仅引用可核验性；不涉及任何设计命题、E-2
   gate 或映射语义。处置：附录 B 两行补注前缀；其余事件锚点文件
   （session-event/event-manifest/session-compaction-event）上游唯一无歧义，
   filesystem.ts 附录 B 引用（`:9`）r1/r3 已先后亲核且与 §2.2 无同名引用冲突，
   均不动。F-14 的 r3 闭合标准原可留待实施期 E-2 样本校正附录 B 时顺带补齐，
   本轮提前收编（修订量一句话级）；r3 报告为轮次证据不回改，以本记录为准。

A.11 更正记录（r4 复审采纳轮，2026-10-19）：r4 复审对 v1.3 延续 r3 的 APPROVED
（blockers=0），唯一新建议 F-15（建议级）：§2.1 本仓未跟踪文件计数较 v1.3 快照
滞后。本轮亲核（工作树 HEAD `1fe9c65d…`、分支 `feat/claude-long-session-
projection-loading`，与 §2.1 一致；追踪文件干净）核实成立并采纳，逐项如下
（格式：原错误 → 来源 → 旧/新身份 → 影响范围与处置）：

1. **[来源清单-计数滞后] §2.1 本仓行「未提交状态=干净；未跟踪 4 个 docs 文件
   （本方案 + r1/r2/r2-meta 报告；2026-10-19 纠错轮亲核）」（r4-F-15）**。原错误：
   计数停留在 v1.2 纠错轮时点——r3 报告在 v1.3 写作时点已落盘（第 5 个），v1.3
   修订未刷新该行；r4 报告落盘后成为第 6 个。本轮亲核：porcelain 列出未跟踪
   docs 文件 6 个（本方案 + r1/r2/r2-meta/r3/r4 报告），追踪文件干净。旧/新身份：
   「未跟踪 4 个 docs 文件（……纠错轮亲核）」→「未跟踪 6 个 docs 文件（本方案 +
   已落盘历轮评审报告 r1/r2/r2-meta/r3/r4；计数截至 2026-10-19 03:52）」——采用
   r4 建议的时点限定式表述，计数锚定快照时点，后续轮次报告落盘后本行不再逐轮
   回改，以来源门/porcelain 复核为准。影响范围：仅来源清单记录精度（P0 模板
   「未提交状态逐项列出」的时效性）；不涉及任何设计命题、门控或验收。处置：
   §2.1 本仓行改写如上；r4 报告为轮次证据不回改，以本记录为准。F-15 的 r4
   闭合标准（§2.1 该行计数与快照时点一致即可，无需单独再轮评审）在本轮修订后
   即告满足。

本轮 1 项为记录精度级更正：不改动任何 R/OD/S/E/Gate/验收标准；r2/r3/r4 通过
结论所依据的全部设计命题在 v1.4 中逐字未动（变化仅 §1 头部、§2.1 本仓行一处
单元格与本附录）。r2 建议 F-10~F-13 维持「实施期吸收、无需再轮修订」原判；
r4 报告 §6 所列实施期吸收项（F-10~F-15、附录 B 随 E-2 样本逐条校正）由实施期
（exec-plan 拆分 / S-1 实施）承接。

A.12 owner 裁决修订（v1.5，2026-10-19）：

1. OD-2 原“v2 缺席→自动 managed 4096；ensure-start 可选后续”作废。owner 要求在
   MacBridge OpenCode（v2）行手动启动 v2、未安装给具体命令，故手动启动/状态与安装指引
   纳入 S-1 必交，完整四拍见 §3.2；自动 fallback 与旧双向切换重启逻辑同时移除。
2. OD-5 原“单目录失败整轮失败/预过滤仍 fail-all”作废。owner 明确“不要搞fail-all”，
   §3.7 改独立桶结算＋返回成功结果＋告警/重试，S-6 同时覆盖本机 snapshot/Swift 告警。
   全失败/认证整体失效保留真实错误是数据真实性要求，不是恢复旧的单桶 fail-all。
3. 只读参考 owner 指定文档 v3 的状态轴/行决策/复制安装流程；不把它的历史来源或未实施
   API 当当前事实。专用上游核清 `Service.ensure` 超时 terminate 副作用后，选择官方
   直接 `serve --service` 的固定端口仲裁；本方不实现另一套服务单例/注册/替换。
4. F-10~F-13 具体吸收：独立60s endpoint应用速率界不依赖ready清零；S-6活体验证排序
   在S-2b之后；XDG路径按官方；显式external/legacy来源不被劫持。旧r1~r5报告保持
   历史记录，引用其中的旧fallback/OD pending时以本版正文为准。
5. 附录A.7修正“842文件”为842条状态项；其余前轮运行证据不当本轮活体重测。
   本轮未改业务代码/测试，未编译、安装、启停程序或执行真实API写操作。
6. 只读发布元数据核对：`GET https://registry.npmjs.org/@opencode%2fcli/2.0.32`
   → HTTP200，根 `name=@opencode/cli`、`version=2.0.32`，bin 为
   `opencode/opencode2 → bin/opencode.exe`、postinstall=`node ./postinstall.mjs`、有integrity。
   只消费该固定对象字段，无安装脚本执行、无下载二进制；不证明可执行或服务就绪。
7. 旧版v1.4 SHA-256=`65c30de93549a7e5ef72a8968e1161e96e3be08f6940c79e16aad6ef814db843`，
   对应 r5 的通过对象；本版含实质产品语义修改，独立复审前不得称已通过。

A.13 v1.5 定向复审记录（2026-10-19 10:51，独立 plan-reviewer）：

- 审查对象：v1.5，SHA-256
  `4609acbe05f25dfea754dc79b02caf50c78054422a2bd387f86e730884767c48`。
- verdict=`APPROVED`，blockers=0，advisories=0。
- scope=`limited`：本轮新增的 §1/R-4/R-5、§3.2、§3.7、OD-2/3/5、E-6/7/9/10、
  Gate、S-1/S-6、§6 矩阵；未对旧 SSE/历史/写路径进行整仓重审，不证明生产路径。
- 结论：手动启动/安装状态一致、单桶失败隔离与告警一致、本轮只文档边界一致，
  未发现导致实现走错或用户主路径不可达的实质矛盾；来源元数据与附录 C 相符。
- 复审后仅更新头部、Gate C、阻塞清单的评审状态与本记录，没有再修改设计命题。
  审查 hash 对应更新状态前版本，不在最终文件中自引用 hash；最终内容由交付工具另算。
- open gates：E-2~E-10 与未来实施授权保持，未因本次通过而执行代码/服务操作。

A.14 v1.6 owner独立包/身份裁决修订（2026-10-19）：

- owner明确更正为`agent/opencode-v2/`与backend ID=`opencode`，不是我此前提出的
  `agent/opencode-web/v2/`和继续用`opencode-web`。本版§3.0设为强制边界。
- 经任务配套源码亲核：原`agent/opencode/`已不存在、生产未导入归档；但旧proxy、
  ID专用handler、iOS `.openCode`与存储键仍有残留。owner“旧ID已不用”是产品注册层
  判断，不等于每个消费者/缓存都已无此字符串；§3.11逐点写入适配/隔离与E-11门。
- v1.5在原opencode-web里加v2分支、复用旧生命周期与iOS零改动的条款被本版替代。
  新包只以官方目标版本/真实样本建语义；v1旧包保留，稳定后按S-9退役，用户安装/服务/
  数据不在删除范围。v1.5手动启动、精确安装命令、目录隔离产品裁决不变。
- 旧v1接入规则文档SHA-256=
  `d7ce465fef9bc7a60bd9b8423ef1bea772f5b09db5b8025b20c2f93c5cfc047a`；引用来源完整
  身份见§2.1/附录D，仅规则文档参考，不从main分析业务源码。
- 修订前v1.5最终SHA-256=
  `61917230f0c7d28d756ef42ab2d4b6cdc8b2a829e67ed7084214153d995f00a5`。
  本次没有业务代码/协议实现、build/test/install、服务操作、提交或分支集成。

A.15 v1.6定向复核修订：独立评审指出E-11将S-7组件证据与S-8运行态混在同一放行前置，
会形成S-2b→S-8→E-11→S-2b闭环，意见采纳。E-11拆为a契约冻结（S-0输入）、b组件证明
（S-7产出，S-2b依赖）、c运行态证明（S-8产出，S-9依赖），各正文/切片/Gate同步更新。
没有改变owner裁决、协议内容或退役条件；没有提前实施。

最后一次闭合复核为APPROVED，0阻塞/0建议；仅确认该依赖循环修复，对象SHA-256=
`14c29dc24ca7052891b11ec9649d570aaf5e33662476905d72671767c5d7284f`。完整v1.6仍待
方案复审，不把定向收口或旧版通过当整版放行。复核后只更新头部状态与本记录。

A.16 v1.7公共依赖边界补强（2026-10-19）：owner强调允许复用公共设施不应造成新v2
依赖旧`agent/opencode-web/`，否则旧包退役会牵动大量v2引用。采纳为§3.0强制约束：
新包与公共helper的生产/测试直接及传递依赖均不能进入旧包；必要通用helper先归中立公共层，
两代分别依赖；Kernel/EventPublisher在bridge装配层使用同一实例，经core契约接入，
不经旧driver转接。E-11b/S-0/S-7增加闭包和fixture检查，§3.12/S-9增加受控排除旧包后
新v2/公共层零源码改动的构建/定向验证标准。只补依赖规则，未改产品语义或执行代码。

本轮来源门1与门2于2026-10-19 11:46分别重新执行，身份均为：M=
`/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` /
`feat/claude-long-session-projection-loading` / `1fe9c65d734949981d032de5f57e9ffb43f6bde9`；
I=`/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` / 同分支 /
`89f0a3693c7ba2ee45cd9191a08cbc5f765aee52`。M跟踪文件干净、附录C所列7个未跟踪文档
逐项不变；I干净，worktree fresh枚举唯一同族且与任务文档配对一致。预期来源即上述M/I，
预期特性不变（`agent/opencode-v2`/ID=`opencode`、共享v2、双端隔离），新增公共层对旧包
零依赖。上游`/Users/jacklee/Projects/opencode-v2` detached/tag`v2.0.32` /
`843a6c514e606b436a7fe713422ace0a93ad3d61`干净；本轮只读既有方案，不新增官方协议命题。
修订前v1.6 SHA-256=`dfb1ada2028ab0855729d700003e3b3504dc69c749b4a4cd4fccdfe8dcc4cf5e`。
本轮D0：文档静态检查；无业务代码、build/test/install/服务操作、commit或分支集成。

A.17 v1.8 r6 建议收编与评审状态回写（2026-10-19）：

- r6 完整复审（新评审员完整重读 v1.7 全文与 r1/r2/r2-meta/r3/r4/r5 历史报告，对
  v1.5~v1.7 新增锚点全量亲核）：verdict=APPROVED，0 阻塞/2 建议级（F-16/F-17），
  对象 v1.7 SHA-256=
  `37ddbaf09e1d4510fd9535232f845e844808f545168bfbd2bc2bf11ad0c7bd47`；报告
  `docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r6.md`（本会话
  只读，不改写历史报告）。通过覆盖 v1.4→v1.7 累积改动；「通过≠授权」，S-0~S-8 实施
  启动仍由 owner 决定，E-2~E-11 证据门不变。
- F-17（采纳）：§5 S-1 依赖列「选择/身份按S-7」→「选择/身份按E-11a（S-0冻结契约）」。
  依据 r6：S-1 选择/身份语义的真实前置输入是 E-11a 契约（S-0 产出，不依赖 S-7 实现）；
  原表述会把该部分绑到 S-7 实现进度、弱化切片并行度。全文仅此一处使用旧表述。
- F-16（部分不采纳，标明理由）：r6 称 `case "opencode":` 实际在 :218、:216 为
  `case "claude":`、函数自 :214 起。本会话在任务 HEAD
  `1fe9c65d734949981d032de5f57e9ffb43f6bde9`（该文件无未提交修改）以 Read+rg 双重
  亲核：`legacyRequiresPolling` 的 `id == "opencode"`@:116、`detectAgentStatus`@:212、
  `case "claude":`@:214、`case "opencode":`@:216、health 探活调用
  `detectOpenCodeService(...)`@:229——r6 所述三个行号整体 +2 偏移，与本工作树实际
  不符；:216 即 opencode case，:218 是其注释行。处置：不把 §3.11 引用改成 :218
  （照改会制造真实漂移），改为在该行补注精确子锚点（opencode case@:216、health
  调用@:229），实现 F-16 消除引用歧义的意图。r6 的语义命题（旧 health/轮询 fallback
  存在、新 v2 需结构化 readiness 替代）经亲核成立，:116 锚点亦精确；该差异不涉及
  任何门控（r6 亦定级为仅引用精度）。
- 本轮来源门：分析门 12:10、首改门 12:19（Asia/Shanghai）两次复核身份一致：M=
  `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` /
  `feat/claude-long-session-projection-loading` / `1fe9c65d734949981d032de5f57e9ffb43f6bde9`，
  tracked clean、未跟踪文档由 7 项增至 8 项（新增 r6 报告，逐项记录）；I=
  `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` /
  同分支 / `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52`，干净；上游
  `/Users/jacklee/Projects/opencode-v2` detached tag `v2.0.32` /
  `843a6c514e606b436a7fe713422ace0a93ad3d61`，干净。本轮 D0：仅编辑本方案与文档
  静态检查；无业务代码、build/test/install、服务操作、commit 或分支集成。

## 附录 B：v2→bridge 事件映射初表（源码声明版；实施期以 E-2 样本逐条校正）

| v2 事件（session-event.ts / event-manifest.ts 声明；r1 已对齐声明字面量） | bridge 侧对应（拟议） |
| --- | --- |
| session.created / session.deleted / session.renamed / session.metadata.updated | 会话目录刷新广播 |
| session.message.content.updated | 消息 parts 流式增量（对应 v1 message.part.updated 桥事件） |
| session.text.started/delta/ended、session.tool.input.started/delta/ended | 细粒度流式（声明存在；与上行如何分工由 E-2 样本仲裁后落映射，不预设） |
| session.reasoning.started/delta/ended | reasoning 流（audit014 链路） |
| session.execution.started/succeeded/failed/interrupted | turn 生命周期（armed→idle/error 收敛） |
| session.inbox.enqueued/delivered/cancelled/delivery.changed | 首条消息可见性/投递状态（对齐首回合闪烁复盘） |
| session.compacted（`session-compaction-event.ts:7`；r1 更正：非 `session.compaction.*`） | 上下文压缩状态镜像 |
| permission.asked / permission.replied（`packages/schema/src/permission.ts:45-47`） | question/permission 桥事件 |
| form.created / form.replied / form.cancelled（`packages/schema/src/form.ts:173-175`） | question 桥事件 |
| filesystem.changed（`filesystem.ts:9`；r1 更正：非 `FileSystem.Event.*` 字面量） | workspace diff / live 文件变更 |
| session.model.selected / session.agent.selected | 模型/agent 切换镜像 |
| rpc.* / 未知 type | 计数丢弃（fail-closed），日志采样 |

（r1 备注：声明全集还含 session.moved/forked/viewed/permissions/usage.*/step.*/
shell.*/skill.activated/synthetic 等——与 CordCode 产品面无关或待 E-2 样本定去留，
映射表按 E-2 捕获逐条增删，未列出不等于忽略。）

## 附录 C：v1.5 本轮来源门与既有未提交路径

时间：2026-10-19 10:30（分析前）/10:34（首次修订前），Asia/Shanghai。
路径、精确分支、完整提交见 §2.1；两次门点一致。任务预期写入工作树与配套为：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=1fe9c65d734949981d032de5f57e9ffb43f6bde9
未提交状态=跟踪文件干净；下列7项未跟踪
任务预期分支=本会话工作树feat/claude-long-session-projection-loading；只修订方案
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
配套分支=feat/claude-long-session-projection-loading
配套提交=89f0a3693c7ba2ee45cd9191a08cbc5f765aee52
配套未提交状态=干净
预期产品特性=opencode-web接官方v2共享服务、显式手动启动、准确安装指引、目录隔离；既有iOS wire
```

本任务工作树 7 项（修订前即存在；仅编辑最后一项）：

```text
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r1.md
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r2-meta.md
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r2.md
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r3.md
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r4.md
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r5.md
?? docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md
```

上游 `/Users/jacklee/Projects/opencode-v2` 为 detached tag `v2.0.32`、
`843a6c514e606b436a7fe713422ace0a93ad3d61`，干净；旧上游
`/Users/jacklee/Projects/opencode` 为 `dev`、
`22e771f5893e255ea78fc10557ed3fb98d3a1072`，干净，仅记录元数据，不作 v2 源码依据。
上游无产品配套仓要求；本任务只读，预期语义绑定官方 v2.0.32。

owner 指定参考文档仓 `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
为 `feat/ios-native-message-timeline`、`f868af18bbbbff6e7f47994c11fb76b49406b33e`。
它不是本任务配套仓；不读取其业务源码、不在该树编辑。下列 20 项既有 dirty 均保持：

```text
 M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r14.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r15.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r16.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r17.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r18.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r19.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r20.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r21.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r22.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23-meta.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24-meta.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r25.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

本轮无构建/安装/部署，第三门不适用；未来实施须重新生成，不能沿用本清单。

## 附录 D：v1.6 重新核验的来源门

2026-10-19 11:20分析前、11:22首次修订前（Asia/Shanghai）两次git元数据一致；
本轮在任务配套M/I上分析身份接线，不借用main或native-timeline工作树源码。

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=1fe9c65d734949981d032de5f57e9ffb43f6bde9
未提交状态=跟踪文件干净；附录C所列7项未跟踪文档逐项不变，方案为本轮唯一编辑目标
任务预期分支=本会话工作树feat/claude-long-session-projection-loading；只修订设计
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
配套分支=feat/claude-long-session-projection-loading
配套提交=89f0a3693c7ba2ee45cd9191a08cbc5f765aee52
配套未提交状态=干净
配对依据=本任务既有方案已记录该来源组合；fresh两仓worktree枚举各唯一同族，完整身份复核
预期产品特性=新agent/opencode-v2、backend ID/kind=opencode、官方v2共享服务、双端新代际识别；旧opencode-web稳定后退役
```

上游：`/Users/jacklee/Projects/opencode-v2`，detached/tag`v2.0.32`，
`843a6c514e606b436a7fe713422ace0a93ad3d61`，干净；任务预期官方v2语义证据源，无产品
配套关系，仅读、不构建。旧checkout不作本版v2语义依据。

规则文档参考：`/Users/jacklee/Projects/cordcode-macbridge`，分支`main`，
`ea0627e30f061c86e2a75711e70778884de7c48c`，干净；任务明确指定只读旧设计文档，
不是本任务实现或双仓产品配套，预期仅复用source-first与样本/隔离规则。

状态机参考：`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，
`feat/ios-native-message-timeline`，`f868af18bbbbff6e7f47994c11fb76b49406b33e`；
附录C所列20项dirty逐项复核保持；只读指定状态机文档，非实现/配套来源。

本轮无build/test/install/deploy/服务启停，不触发第三构建门。未来实施不得复用此清单
冒充fresh来源，尤其iOS已有并行工作树在前进，禁止跳到它或默认main借用产物。

（完）
