# dsh-web source-first convergence 专项方案（canonical）

- Date: 2026-09-23（v8，同日第七轮修订）
- Canonical status: **本文档是 dsh-web 对当前 dsh 官方源码做整体升级适配的唯
  一实施方案权威。** 按方案评审循环执行：本文档送审 → 评审 → 不通过则修订为
  下一版 → 通过后才进入开发阶段。S1 除外——owner 已于 2026-09-23 上午单独
  批准先行实施并部署（见 §5.1 完成记录）；S2～S5 在本文档复审通过前不得实
  施，复审通过后仍受各自 Gate A 退出条件约束（§3）。
- 评审输入：
  - 第一轮：[2026-09-23-dsh-web-source-first-convergence-plan-review.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review.md)（不通过，
    7 条：6 阻塞 + 1 建议；v2 已处置，见 §9.1）。
  - 第二轮：[2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md)（不通过，
    4 阻塞 + 1 建议；v3 处置，见 §9.2）。
  - 第三轮：[2026-09-23-dsh-web-source-first-convergence-plan-review-r3.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review-r3.md)（不通过，
    1 阻塞 + 2 建议；v4 处置，见 §9.3）。
  - 第四轮：[2026-09-23-dsh-web-source-first-convergence-plan-review-r4.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review-r4.md)（不通过，
    3 阻塞；v5 处置，见 §9.4）。
  - 第五轮：[2026-09-23-dsh-web-source-first-convergence-plan-review-r5.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review-r5.md)（不通过，
    4 阻塞 + 2 建议；v6 处置，见 §9.5）。
  - 第六轮：[2026-09-23-dsh-web-source-first-convergence-plan-review-r6.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review-r6.md)（不通过，
    5 阻塞；v7 处置，见 §9.6）。
  - 第七轮：[2026-09-23-dsh-web-source-first-convergence-plan-review-r7.md]
    (2026-09-23-dsh-web-source-first-convergence-plan-review-r7.md)（不通过，
    4 阻塞 + 1 建议；v8 处置，见 §9.7）。

## 本轮（v8）改动范围声明

1. **OD-5 阻塞范围收窄**（r7 意见 1 阻塞）：OD-5 影响范围精确到「S2 跨版
   本防御部分（A2 门控）与 rc 兼容矩阵」，并显式声明 alpha.1 已证的
   `commands/execute` 错参修复**独立于 OD-5**（其证据来自运行座位活体样
   本，不涉及版本矩阵选择）——消除「同一修复既被允许又被禁止」的冲突，
   实施者无需自行选择较宽一条。
2. **A3 拆为 A3a/A3b**（r7 意见 2 阻塞）：A3a=可见性证据（OD-2b 任一选项
   均必需：插入/消耗/id 连续/重连）；A3b=管理操作证据（仅 OD-2b=B 启用：
   updateQueue 编辑/撤回/steer + 拒绝样本），各自文件/断言/负例/缺项即停；
   S3 的阻塞规则按范围拆分（可见性与管理互不牵连）。
3. **A4 拆为 A4a/A4b**（r7 意见 3 阻塞）：A4b 为文件 receipts 的条件门
   （仅 OD-3=B 启用），覆盖上传→receipt→prompt 准入（`commands.ts:358-371`）
   →读取/重开链的独立证据要求；样本缺 → 仅文件范围阻塞，不得靠 image 样
   本放行。
4. **OD-1 B 选项补全范围与验收**（r7 意见 4 阻塞）：B = S5 整体不实施
   （置顶维持桥本地、归档/恢复维持现状、行 22–25 降级 future），配独立验
   收标准；§5.5 的实施与门按 A/B 分别写清。
5. **建议采纳**：OD-4 验收补可测阈值建议值（records ≤ 500、JSON ≤ 2 MiB、
   解码 p95 ≤ 500ms，owner 裁决时可调）。

**不采纳项：无。** r7 四条阻塞与一条建议全部采纳。

- 上游锚点（本方案写作时点，v8 复核）：
  - 本机官方源码 checkout：`/Users/jacklee/Projects/deepseek-harness`，
    tag `dsh-v0.1.7-alpha.2`，commit `00102833dfaee1da9f48a3a8eae9d34005a75218`。
  - 运行中座位实例：`127.0.0.1:3080`，进程 `node /opt/homebrew/bin/dsh
    --profile web --host 127.0.0.1 --port 3080 --no-open`（PID 38778，
    2026-09-23 00:34:37 启动），CLI 版本 0.1.7-alpha.1（`dsh --version` 实
    测）。证据：`scripts/dshweb-phase0/alpha1-commands-goals-wire.json`。
  - 可安装通道：latest 0.1.5-rc.2 / next 0.1.5-rc.3 / alpha 0.1.7-alpha.x。

## 0. 执行边界

本文档是实施契约。每个 supported 表面有一份执行档案（§5），包含官方源码锚
点、同版本真实样本、wire 形状、bridge 映射、失败行为、测试与排除项。伴生证
据文件（`scripts/dshweb-phase0/` 样本包）只做证据账本，不能授权实现。

证据优先级（继承 opencode-web 收敛方案 §1，全部适用）：

1. 官方源码（目标版本 checkout，带 commit）；
2. 目标版本活体的真实脱敏样本（请求/响应/帧）；
3. 官方测试/fixture。

禁止作为证据：本仓 legacy `agent/dsh`（SDK stdio 路线）的任何行为、从字段名
反推协议、凭 chunk 类型猜测、为「可能的旧版/新版」加递归解析或静默
fallback。dsh 自身源码一行不改（owner 2026-09-22 红线，继续有效）。

## 1. 对齐定义（四层，与 opencode-web 方案 §2 同构）

「跟随官方 Web」= 四层一致：

1. **调用**：CordCode 发送与官方 Web UI 相同的有意义字段与 content parts
   （含 `assistantStream: true` 这类开关——S1 教训：少一个开关整条通道静默
   消失）。
2. **观察**：CordCode 消费与官方客户端相同的权威响应/帧流，含错误与收口
   路径。
3. **状态**：创建、列表、重开、改名、归档、置顶、外部回合、重连收敛到同一
   服务端真值。
4. **呈现**：bridge-v1 只广告已证明的能力；正文/思考、权限/问答、运行/排队
   等区分保持。

不要求像素级对齐；要求服务端语义对齐。

## 2. 支持的运行版本策略

dsh alpha 通道处于高频重构期，**参数与端点会跨 alpha 版本改名**。策略：

1. 每个表面绑定版本范围 + 源码 commit + 样本包 + 契约测试（继承
   opencode-web 方案 §3）。
2. 网关的 descriptor 校验错误（`gateway/arguments-invalid`：
   `args fields do not match the descriptor: missing "X"; unexpected "Y"`）
   是可诊断的版本漂移信号：桥收到即如实上报（字幕/诊断），**禁止**为「可能
   的新版」加猜测兼容或双发试探。
3. 未知版本 fail-closed：报「不支持的 dsh 版本」并给出可诊断状态，不递归
   搜 JSON。
4. 版本探测纳入 readiness：`hello_ack.backends[].statusMessage` 只携带版
   本号与来源标识（如 CLI 路径），**不含凭据、cookie 或其他非版本信息**。

### 2.3 已实证的漂移案例（两端证据对等）

| 表面 | alpha.1 运行版（活体样本已归档） | alpha.2 源码（checkout 核对） | 影响 |
| --- | --- | --- | --- |
| commands/list | 参数 `agentId`，200 ok | `list(agent: Agent)`（`interaction/commands/src/index.ts:314-315`） | 下一次 dsh 升级即断 |
| commands/execute | **`{agentId, line, submittedAttachments}`**（正向形状 200 ok；`images` 被拒 verbatim 归档） | `execute(agent, line, submittedAttachments, signal)`（同文件 :360-366） | **现行 bug：桥传 `images` 在当前座位被拒，斜杠命令执行今天就坏**（list 可用）；S2 第一修复项 |
| goals/get 等 | `goals/get` 用 `agentId` 200 ok；`goal/get` 404 | `@Remote('get'…)`，`get(agent: Agent)`（`goal/goal/src/index.ts:276-277,390-391,646-648`） | 下一次 dsh 升级即断；alpha.2 多出 `complete`/`create` |
| goals/pause、resume、clear、edit | 四方法 `{agentId, ref, request?}` 请求**形状**全部被接受（错形 `agent` 被 descriptor 拒 verbatim 归档；正形在无 goal 一次性会话上得业务错误 "no current goal"——方法已执行，形状成立）。**形状被接受 ≠ 按钮端到端可用**：有 goal 时的实际 mutation 成功路径（状态改变、响应解码、投影收敛）未由本证据验证，列入 S2 活体复测（对真实 goal 各做一次真实动作） | `pause/resume/clear(agent: Agent, ref: GoalRef)`、`edit(agent: Agent, ref: GoalRef, request: EditGoalRequest)`（`goal/goal/src/index.ts:328-329,351-352,363-364,432-433`） | 下一次 dsh 升级即断；A2 逐项取证 |
| fileReferences/list | 参数 `agentId`（`agent` 被拒 verbatim 归档） | `list(agent: Agent, query)`（`session-controller/src/file-references.ts:30-38`） | 同类漂移；该表面桥未消费（future），登记备查 |

- alpha.1 侧证据（全部 verbatim 归档）：
  `scripts/dshweb-phase0/alpha1-commands-goals-wire.json`（17 项探针：含
  commands/execute 四种形状、goals/get 与四个 goal 变更方法的错形/正形、
  skills/list、fileReferences/list、座位进程身份、CLI 版本、捕获时间；
  `live_bug_finding` 登记现行 bug；`goal_mutations_status` 登记 goal 四方法
  **形状被接受、端到端成功未证**）。
- **S2 实施门**：alpha.2 的 `Agent` 跨 Remote wire 形状样本（A2）未取得前，
  S2 的「跨版本防御」部分不得实施；**但 execute 现行 bug 修复（alpha.1 形
  状 `submittedAttachments`）只依赖已归档的 alpha.1 活体证据，过审后即可实
  施**。不从 alpha.1 形状外推 alpha.2 的 `Agent` wire。alpha.2 样本取得路
  径：owner 升级正式座位，或经 owner 同意另起测试座位。

## 3. Gate A — 证据包（实施前置，逐切片退出条件）

通用五要素：**同版本真实样本 + 脱敏文件索引（`scripts/dshweb-phase0/`）+
字段级断言 + 负例（或不适用的范围理由）+ 缺项即停 disposition**。「缺项即
停」= 任一要素缺失时，对应 supported 行降级为阻塞/future，不得进入实施；
不允许「实现期再确认」。每个编号可独立判定通过/阻塞。

| 编号 | 表面 | 样本（同版本捕获） | 文件索引 | 字段级断言 | 负例 | 缺项即停 | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A1 | assistant-stream | 两轮真实 turn 逐帧记录（每帧 revision/index/attemptId/chunkType/字符数） | `s1-live-turn-frames.json`（rounds[0/1]，154/135 帧） | revision 自 baseline+1 逐帧 +1；chunk index 每 attempt 自 0 连续；end.outcomeSeq == assistant/message journal seq——三条已程序化核验通过（文件内 `verification` 字段） | revision 断档、index 断档、无 start 的 chunk（单测 `TestAssistantStreamContinuityDrops` 已钉） | 已满足 | **完成** |
| A1b | S1 生产运行态 | 四轮探针 turn 的 runtime 特征日志 | `s1-runtime-log-excerpt.json`（180 行逐行脱敏 + runtime.json epoch + 二进制内嵌 commit b4e42d5c5a9a = 仓库 HEAD + ps 代际） | 新代际晚于构建；text_delta passive 事件为 S1 构建独有；零复位 | 无独立负例——不适用理由：该编号是运行态身份与特征输出核验，负例由 A1 的单测负例承担 | 已满足 | **完成** |
| A2 | commands/goals alpha.2 wire（七面） | alpha.2 座位上逐项请求/响应：`commands/list`、`commands/execute`（第三参形状）、`goals/get`、`goals/pause`、`goals/resume`、`goals/clear`、`goals/edit`（`ref`+`request` 形状）——**桥消费的每一面逐一取证**，不因 get 已证而外推 mutation | 预定 `alpha2-commands-goals-wire.json`（未创建前本行为阻塞） | 每面参数名与 alpha.2 descriptor 一致；edit 的 ref/request 字段形状；响应类型（GoalView/GoalRef/void） | 每面 `agentId` 在 alpha.2 被拒的 verbatim 错误；edit 的负例**拆分**——①缺 `request` 参数＝网关 descriptor 负例（alpha.2 样本确认）；②`request` 对象存在但 `objective`/`maxGoalRounds` 均缺失且存在 goal＝业务负例，**待捕**（需有 goal 的会话；源码预期 `GOAL_INVALID_EDIT`，`goal/goal/src/index.ts:331-334`（throw 在 :334），以样本确认为准，不预定错误码） | 文件未创建 → S2 跨版本防御部分阻塞（execute 现行 bug 修复不受此门约束，见 §2.3）；任一面样本缺失则该面阻塞 | **阻塞**（等 alpha.2 座位） |
| A3a | inbox **可见性**（OD-2b 任一选项均必需） | ①运行中 prompt → splice 插入（UserMessage.id）+ inbox 投影基线；②消耗 → splice 移除 + user/message 同 id 落定；③重连 → 投影重建 | 预定 `alpha1-inbox-wire.json` | UserMessage.id 在 inbox↔user/message 间连续（去重键成立） | 无独立活体负例——不适用理由：本编号只读观察（follow/projections/page），写入仅隔离会话的正常 prompt（§3.1）；「非法 splice 投影错误」为持久化折叠内部不变量（`inbox.ts:32-55`），正常 Remote 写入无法提交，由桥内 reducer 定向单测按源码语义覆盖（r6 意见 3 更正保留） | id 连续性未证实 → S3 **可见性**阻塞 | 待捕 |
| A3b | inbox **管理操作**（仅 OD-2b=B 时启用；A 选项下本行不适用、不阻塞） | `session/updateQueue` 编辑/撤回/steer 变更的请求/响应 + 对应 splice（removedCount/outcome）帧 | 预定 `alpha1-updatequeue-wire.json` | 管理操作后 inbox 投影与 Mac web 行为一致 | **活体负例（可安全触发的 Remote 拒绝）**：updateQueue steer 变更拒绝（`session/steer-unavailable`，`commands.ts:477-479`——属本编号，不属 A7）的请求/响应样本 | OD-2b=B 且样本缺 → S3 **管理范围**阻塞（可见性不受此门影响）；OD-2b=A 时本编号关闭 | 条件待捕 |
| A4a | 图片附件（OD-3 任一选项均必需） | ①发送：prompt image part `{type:'image',mediaType,data,name?}` 完整请求/响应（`types.ts:89-96`）+ 准入持久化后的 journal 引用；②读取：session/attachment 请求/响应与 attachmentId 引用映射 | 预定 `alpha1-attachment-wire.json` | attachmentId ↔ journal 引用映射成立 | 模型不支持图片 → `session/attachment-invalid`（MODEL_DOES_NOT_SUPPORT_IMAGES，`commands.ts:350-355`，reason 字段在 :354） | 引用映射未证实 → S4 **图片范围**阻塞（映射在样本前保持待定，不预设） | 待捕 |
| A4b | 文件 receipts（**条件门：仅 OD-3=B 时启用**；A 选项下本行不适用、不阻塞） | 独立于图片的上传→receipt→准入→读取/重开链：`fileUploads.upload(sessionId,{data,name?},signal)`（官方 Web 调用 `file-upload/client/runtime.ts:213-220`；Remote 声明 `file-upload/src/index.ts:105-106`）→ prompt 的 `{type:'file',receiptId}` part → 准入解析与绑定（`commands.ts:358-371` resolvePromptFileReceipts/bindPrompt）→ 读取/重开路径 | 预定 `alpha1-file-receipts-wire.json` | receiptId ↔ prompt part ↔ journal 引用关系成立；重开后文件可读 | 上传超限/无效 receipt 的拒绝路径（以样本确认，不预定错误码） | OD-3=B 且样本缺 → S4 **文件范围**阻塞（图片范围不受此门影响，不得靠 image 样本放行）；OD-3=A 时本编号关闭 | 条件待捕 |
| A5 | workspace 管理 | pin/unpin/archive/unarchive 请求/响应 + 基线 pinned/archived 字段 + 排序帧；**幂等成功样本**：unarchive 不存在/未归档 id = 成功 no-op（官方语义，`workspace-controller/src/commands.ts:183-194` "An id that is not archived is not an error: the call is idempotent"、`workspace/src/index.ts:385-405` 无存在性检查、官方测试 `workspace.spec.ts:1086,1101` 对 never-archived 与已消失会话均验证成功） | 预定 `alpha1-workspace-mgmt-wire.json` | 归档行从分组表面消失与 Mac web 一致；pinned 字段往返一致；unarchive 幂等成功（响应为完整 archive set，无错误） | **可达负例**：活动会话归档不带 `stopActivity` → `workspace/session-active`（`workspace-controller/src/commands.ts:174`）。~~unarchive 不存在会话的错误~~——与官方源码相反，已删（r6 意见 2）；不在正式座位上追逐源码否定的错误 | 样本缺 → S5 阻塞（不得凭源码形状直接实施） | 待捕 |
| A6 | session/control 流 | baseline + projection 帧序列 | 预定 `alpha1-control-stream.json` | 与逐会话 projections 轮询信息等价（同会话同时刻对比） | 负例不适用——理由：本编号只做等价性评估以决定是否切换通道，无新映射行为；若评估失败维持现状（轮询），无产品风险 | 等价性未证实 → 维持现状不切换（该行保持 future） | 待捕 |
| A7 | prompt mode（steer，**direct 路径**） | 回合运行中官方 Web 的 `session/prompt` `mode:'steer'` 请求形状 + 行为（对比空闲 `queue`）——注意这是 direct 路径：`agent.steer(message)`（`commands.ts:372`），与 `session/updateQueue` 的 steer 变更（A3/OD-2b）是**两个不同取证对象** | 预定 `alpha1-steer-wire.json` | steer 消息进入 next-step 的 splice 形状 | direct 路径的实际拒绝样本（源码映射 `session/agent-busy` "prompt rejected"，`commands.ts:380`——以样本确认，不预定）；`session/steer-unavailable` 属 updateQueue（`commands.ts:477-479`），**不在本编号**，归 A3 | 样本缺 → OD-2a 悬置（维持现状 queue），不阻塞其他切片；本编号当前只做形状镜像评估不实施切换 | 待捕 |

### 3.1 写操作取证隔离规则（A2～A5 适用，r5 意见 4）

A2（goal 变更成功路径与业务负例）、A3（prompt/updateQueue 写入）、A4（图
片 prompt）、A5（pin/archive）都会改变真实服务端状态。逐项规定：

- **通用红线**：一切写入只允许发生在**新建的一次性隔离会话**上
  （`session/create`，探针目录 `/tmp/dshweb-phase0-probe`）或一次性隔离工
  作区；**禁止对 owner 既有会话/工作区做任何写入**（含 goal、prompt、队
  列、图片、pin、archive）。只读取证不受此限。
- **结束收尾**：每次取证会话结束后必须
  `workspace/archiveSession {sessionId, stopActivity: true}` 归档，并把归
  认确认与（脱敏截断的）会话 id 记入对应证据文件。
- **A2**：goal 成功路径样本在隔离会话上经官方 goal 创建命令建立真实 goal
  后执行 pause/resume/clear/edit 并核对状态收敛；`GOAL_INVALID_EDIT` 业务
  负例同在隔离 goal 会话上取样；取样后归档。
- **A3**：队列写入链（运行中 prompt → 队列项 → updateQueue 编辑/撤回/steer
  变更 → 消耗）全部在隔离会话上完成；结束后归档。
- **A4**：图片 prompt 在隔离会话上发送；`session/attachment` 为只读取证，
  对既有会话亦允许（只读）。
- **A5**：pin/unpin/archive/unarchive 只对隔离会话或一次性隔离工作区执
  行；排序（insertBefore/insertSessionBefore）只在隔离工作区上执行。
- **无法满足时**：改用另座测试实例（经 owner 同意另起）并保持该取证门阻
  塞；不得以「样本待捕」为由对 owner 既有会话写入。

## 4. Gate B — 能力地图（116 行底表 + 7 面装配排除 = 全仓 123 面对账）

底表来源（v5 扩界）：**全仓** `extends TypertRemoteService` 穷举 **加两条
核实线**——①官方 Web 客户端调用点核实（`packages/client/` 各 ui-* 包的
`remote.*` 注入与调用）；②目标座位挂载探针（错形参数在网关 descriptor 层
被拒＝已挂载，404＝未挂载；证据
`scripts/dshweb-phase0/alpha1-web-assembly-mounts.json`）。入表范围：
`packages/api/` 11 服务 67 面 + 桥消费 namespace 14 面（行 68–81）+ 官方
客户端实际调用的其余 namespace 22 面（行 82–103）+ `pluginRegistryProbe`
1 面（行 104）+ `dynamicCordisRunner` 12 面（行 105–116）。观察清单仅余
agentTeams（1 面）与 speech（6 面），以**正确方法**探得 404（装配排
除）。全仓对账：28 个真实服务类、123 个 `@Remote` = 底表 116 + 装配排除
7；本表不宣称无条件覆盖全部官方表面，未入表面以「无客户端调用点或未挂
载」的证据为界。状态：**已支持**
（已实现并有测试）/ **切片**（本专项切片承接，过审 + Gate A 后实施）/
**future**（证据齐后立档）/ **deliberately unsupported**（登记且
`hello_ack` 不广告）。

### session-controller（namespace `session`，18 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 1 | session/list | 已支持 | 会话目录 |
| 2 | session/search | future | iOS 无搜索入口；A3/A5 邻接样本齐后立档 |
| 3 | session/create | 已支持 | 绑定创建 |
| 4 | session/selectModel | 已支持 | 模型选择 |
| 5 | session/modelCatalog | 已支持 | 目录 |
| 6 | session/canOpenWorkspacePath | deliberately unsupported | Mac 桌面集成，iOS 无此能力 |
| 7 | session/openWorkspacePath | deliberately unsupported | 同上 |
| 8 | session/workspacePathApplications | deliberately unsupported | 同上 |
| 9 | session/rename | 已支持 | 改名 |
| 10 | session/fork | future | iOS 无 fork 入口 |
| 11 | session/prompt | 已支持 | 发送；image part 由 S4 扩展 |
| 12 | session/attachment | 切片（S4 读取侧） | 读取已持久化图片 |
| 13 | session/updateQueue | future | S3 先做可见；管理待 OD-2b |
| 14 | session/cancel | 已支持 | 取消回合 |
| 15 | session/page | 已支持 | 历史分页 |
| 16 | session/follow（stream） | 已支持 | S1 直播 |
| 17 | session/projections | 已支持 | 投影读取 |
| 18 | session/control（stream） | future | A6 等价性评估后决定 |

### skills / fileReferences（session-controller 子 namespace，2 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 19 | skills/list | future | 用户可见技能目录（`skill-catalog.ts:19-40`，alpha.1 活体可用、结构已探针）；iOS 无技能面板入口，与斜杠命令面板邻接，A2 后评估 |
| 20 | fileReferences/list | future | @ 引用路径候选（`file-references.ts:17-38`；alpha.1 用 `agentId`，漂移行见 §2.3）；iOS 输入辅助无此面 |

### workspace-controller（namespace `workspace`，11 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 21 | workspace/follow（stream） | 已支持 | 归组基线 |
| 22 | workspace/pinSession | 切片（S5） | 语义按 OD-1 |
| 23 | workspace/unpinSession | 切片（S5） | 同上 |
| 24 | workspace/archiveSession | 切片（S5） | 归档/恢复按 OD-1 |
| 25 | workspace/unarchiveSession | 切片（S5） | 同上 |
| 26 | workspace/create | future | iOS 无工作区管理入口 |
| 27 | workspace/rename | future | 同上 |
| 28 | workspace/delete | future | 同上（破坏性操作另需 owner 授权） |
| 29 | workspace/insertBefore | future | 排序，随工作区管理 |
| 30 | workspace/insertSessionBefore | future | 同上 |
| 31 | workspace/initializeDefault | deliberately unsupported | host 引导期内部操作 |

### directoryPicker（workspace-controller 子 namespace，3 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 32 | directoryPicker/pick | deliberately unsupported | 原生 Mac 目录选择对话框（`directory-picker.ts:54`），iOS 无法承载 |
| 33 | directoryPicker/list | future | 文件系统浏览，随工作区管理（future 池） |
| 34 | directoryPicker/createDirectory | future | 同上 |

### settings-controller（namespace `settings`，5 面）与 credentials（子 namespace，3 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 35 | settings/describe | 已支持 | 权限模式等 |
| 36 | settings/update | 已支持 | 同上 |
| 37 | settings/replace | future | 整档替换，iOS 无设置编辑面 |
| 38 | settings/mutate | future | 细粒度变更，同上 |
| 39 | settings/openSettingsDocument | deliberately unsupported | 桌面 UI 打开动作 |
| 40 | credentials/describe | deliberately unsupported | 凭据管理红线：认证只读 `~/.dsh/.credentials.yaml` 的 `client-connection/browser-session` 一条，其余凭据不读取不缓存不打印（`credentials.ts:67-117` 仅为源码登记，不作运行态断言） |
| 41 | credentials/set | deliberately unsupported | 同上（写路径更不触碰） |
| 42 | credentials/unset | deliberately unsupported | 同上 |

### workspace-files（namespace `workspaceFiles`，5 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 43 | workspaceFiles/read | future | 官方 Web 文件浏览器；桥的 `get_workspace_diff` 走桥本地 git 路径（`go-bridge/checkpoint.go`），不消费此 API |
| 44 | workspaceFiles/readBytes | future | 同上 |
| 45 | workspaceFiles/stat | future | 同上 |
| 46 | workspaceFiles/list | future | 同上 |
| 47 | workspaceFiles/changes（stream） | future | 同上 |

### job-controller（namespace `job`，3 面）

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 48 | job/list（stream） | future | 官方后台任务面板；桥的 background_tasks 现由 session/list subagent 行承接（`background_tasks.go`），等价性待评估 |
| 49 | job/follow（stream） | future | 同上 |
| 50 | job/kill | future | 同上 |

### terminal-controller（namespace `terminal`，10 面）

终端面板不在 iOS 产品面——10 面逐一登记，全部 deliberately unsupported：

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 51 | terminal/environment | deliberately unsupported | 终端面板不在 iOS 产品面 |
| 52 | terminal/shells | deliberately unsupported | 同上 |
| 53 | terminal/list | deliberately unsupported | 同上 |
| 54 | terminal/create | deliberately unsupported | 同上 |
| 55 | terminal/retain（stream） | deliberately unsupported | 同上 |
| 56 | terminal/follow（stream） | deliberately unsupported | 同上 |
| 57 | terminal/write | deliberately unsupported | 同上 |
| 58 | terminal/resize | deliberately unsupported | 同上 |
| 59 | terminal/rename | deliberately unsupported | 同上 |
| 60 | terminal/close | deliberately unsupported | 同上 |

### account-controller（namespace `account`，7 面）

账号/凭据管理红线；认证只读 browser-session 一条，不进日志/诊断/hello_ack
——7 面逐一登记，全部 deliberately unsupported：

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 61 | account/getState | deliberately unsupported | 账号/凭据管理红线 |
| 62 | account/getProfile | deliberately unsupported | 同上 |
| 63 | account/getBalance | deliberately unsupported | 同上 |
| 64 | account/startSignIn | deliberately unsupported | 同上 |
| 65 | account/cancelSignIn | deliberately unsupported | 同上 |
| 66 | account/signOut | deliberately unsupported | 同上 |
| 67 | account/watch（stream） | deliberately unsupported | 同上 |

### 桥消费的 `packages/api` 外 namespace（14 面，行 68–81）

全仓穷举发现这些服务类在 `packages/api` 之外且被本桥消费（live 功能已证
明挂载）；v3 底表漏列，v4 补齐：

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 68 | commands/list | 已支持（S2 跨版本防御） | 斜杠命令目录；`interaction/commands/src/index.ts:314`；alpha.1 用 `agentId`（已归档） |
| 69 | commands/execute | **现行受损（S2 第一修复项）** | 运行座位拒绝桥发送的 `images`（§2.3 正反证据）；修复并验证前不以本行证明能力可用；`interaction/commands/src/index.ts:360` |
| 70 | goals/get | 已支持（S2/A2） | `goal/goal/src/index.ts:276` |
| 71 | goals/edit | 已支持（**形状已证；端到端待 S2 活体复测**，S2/A2） | 带 `ref`+`request`；`index.ts:328`；alpha.1 形状已探针证实（§2.3 收窄声明） |
| 72 | goals/pause | 已支持（**形状已证；端到端待 S2 活体复测**，S2/A2） | `index.ts:351`；alpha.1 形状已探针证实（§2.3 收窄声明） |
| 73 | goals/resume | 已支持（**形状已证；端到端待 S2 活体复测**，S2/A2） | `index.ts:363`；alpha.1 形状已探针证实（§2.3 收窄声明） |
| 74 | goals/clear | 已支持（**形状已证；端到端待 S2 活体复测**，S2/A2） | `index.ts:432`；alpha.1 形状已探针证实（§2.3 收窄声明） |
| 75 | goals/complete | future | 桥未消费；alpha.2 新增，A2 后评估 |
| 76 | goals/create | future | 桥未消费；alpha.2 新增，A2 后评估 |
| 77 | agentPresets/list | 已支持 | 预设目录；`preset/agent-preset-registry/src/index.ts:174` |
| 78 | agentPresets/select | 已支持 | 预设切换；同文件 :304 |
| 79 | llm/listConfigurableProviders | 已支持 | 模型目录组成；`llm/llm/src/index.ts:544` |
| 80 | llm/listProviders | future | 桥未消费（host 侧 provider 清单） |
| 81 | llm/discoverModels | future | 桥未消费（host 侧模型发现） |

### 官方 Web 客户端实际调用的其余 namespace（22 面，行 82–103）

调用点核实（`packages/client/`）+ 目标座位挂载探针（全部已挂载，证据见
`alpha1-web-assembly-mounts.json`）后入表；iOS 产品面现状决定 disposition：

| # | Remote | 状态 | 说明（官方调用点 → 用户可见面） |
| --- | --- | --- | --- |
| 82 | pluginManager/listPlugins | future | 插件管理 UI（`ui-plugin-manager/manager-store.ts:526+`）；iOS 无插件面 |
| 83 | pluginManager/listBundles | future | 同上 |
| 84 | pluginManager/registries | future | 同上 |
| 85 | pluginManager/inspect | future | 同上 |
| 86 | pluginManager/setPluginEnabled | future | 同上（管理操作留 Mac web） |
| 87 | pluginManager/setBundleEnabled | future | 同上 |
| 88 | pluginManager/installBundle | future | 同上 |
| 89 | pluginManager/waitForInstall | future | 同上 |
| 90 | pluginManager/cancelInstall | future | 同上 |
| 91 | pluginManager/removeBundle | future | 同上 |
| 92 | pluginInventory/list | future | 设置页插件清单 tab（`ui-settings-plugin-inventory/index.ts:30-38`） |
| 93 | permissionPresets/catalog | future | 权限预设目录（`ui-permission-presets/catalog.ts:134`）；桥权限模式现走 settings/describe+update |
| 94 | messageFeedback/list | future | 消息反馈（`ui-message-feedback/index.ts:60`） |
| 95 | messageFeedback/put | future | 同上 |
| 96 | messageFeedback/delete | future | 同上 |
| 97 | sessionFeedback/record | future | /feedback 命令后端（`ui-message-feedback` 注入 `remote.sessionFeedback`） |
| 98 | subagents/prompt | future | Web 子代理面板交互（`session-controller/client/sessions/session.ts:291`）；桥的 workflow 卡为 journal 投影只读 |
| 99 | subagents/interruptByParent | future | 同上（:350） |
| 100 | fileUploads/upload | future（绑 S4 OD-3） | 文件上传 receipts（`file-upload/client/runtime.ts:213-220`）；OD-3 若含文件 receipts 则进 S4 证据范围 |
| 101 | officeToPdf/render | future | 文档预览侧栏（`ui-sidebar-documentpreview/office/index.ts:70-96`） |
| 102 | officeToPdf/generation | future | 同上 |
| 103 | sessionReferenceResolver/candidates | future | @ 引用会话候选（`ui-reference/index.ts:43`） |

### pluginRegistryProbe 与 dynamicCordisRunner（13 面，行 104–116）

r5 纠正后入表（正确方法探针 + `packages/extensions/` 客户端调用点核实）：

| # | Remote | 状态 | 说明 |
| --- | --- | --- | --- |
| 104 | pluginRegistryProbe/fastest | future | 插件安装 UI 的 registry 可达性探针（npm/npmmirror 竞速；官方 Web `manager-store.ts:586` 调用；正确方法探针证实挂载）；iOS 无插件面 |
| 105 | dynamicCordisRunner/undefineFromPanel | future | cordis preset 面板交互（`ui-cordis/client/index.ts:38-58`）；iOS 无 cordis 面 |
| 106 | dynamicCordisRunner/runHostHalf | future | cordis 宿主半边运行（`cordis-client-runner/client/index.ts:182-273` 一带）；同上 |
| 107 | dynamicCordisRunner/getClientCode | future | 同上 |
| 108 | dynamicCordisRunner/resolveRequestRun | future | 同上 |
| 109 | dynamicCordisRunner/settleUserRun | future | 同上 |
| 110 | dynamicCordisRunner/stopFromPanel | future | 同上（ui-cordis 调用点） |
| 111 | dynamicCordisRunner/syncInspectManifest | future | 同上（cordis-client-runner 调用点） |
| 112 | dynamicCordisRunner/resolveInspectQuery | future | 同上 |
| 113 | dynamicCordisRunner/inventory | future | 同上（ui-cordis 调用点） |
| 114 | dynamicCordisRunner/reportRenderFailure | future | 同上（cordis-client-runner 调用点） |
| 115 | dynamicCordisRunner/reportClientGuardFailure | future | 同上 |
| 116 | dynamicCordisRunner/invoke | future | 同上 |

### 装配排除的 namespace（7 面，正确方法 404 证据）

- **`agentTeams`（TeamService，experimental/agent-team，1 面 `view`）**：
  目标座位正确方法探针 404——未挂载，装配排除。
- **`speech`（SpeechController，experimental/api-speech-to-text，6 面）**：
  目标座位正确方法探针 404——未挂载，装配排除（实验性语音转写）。

证据：`scripts/dshweb-phase0/alpha1-web-assembly-mounts.json`（含
pluginRegistryProbe 错误方法的更正注记）。

### owner 裁决表（OD-1～OD-5，canonical 恢复版）

> r6 意见 1：v5 编辑时本表被误删，现恢复为 canonical 内容并补决定状态与验
> 收标准。**规则：未裁决的 OD 所影响切片保持阻塞——方案评审通过不等于
> OD 裁决；每项需 owner 明确决定后，其影响切片才可进入实施。**

| # | 问题 | 互斥选项 | 决定状态 | 影响切片 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| OD-1 | 置顶与归档/恢复语义：切官方 `workspace/pinSession`/`archiveSession` 还是保持桥本地 pinstore？归档/恢复是否纳入 iOS 会话列表？ | **A 官方语义**：置顶切 `workspace/pinSession`、归档/恢复切 `archiveSession`/`unarchiveSession` 并纳入 iOS 列表（S5 实施）；**B 维持现状**：置顶保持桥本地 pinstore（行为不变），归档/恢复不新增 iOS 入口、会话列表行为维持现状——**S5 整体不实施**（底表行 22–25 的「切片」状态降级为 future） | **待裁决**（方案倾向 A） | S5（行 22–25） | **A 选项验收**：iPhone 置顶在 Mac web 可见且反向一致；归档行双端隐藏、恢复双端可见；与 Mac web 操作互不覆盖。**B 选项验收**：置顶行为与现状一致（桥本地）；无新增归档/恢复面；不宣称双端一致；S5 不实施即满足 |
| OD-2a | 回合运行中 iOS 发消息用官方 `steer`（转向当前回合）还是 `queue`（排队下一回合，现状硬编码）？ | A 镜像官方（运行中 steer、空闲 queue，依赖 A7 样本）；B 维持 queue | **待裁决**（方案倾向 A） | 发送路径（S2 后续；A7） | 裁决模式下运行中消息到达官方 Web 同款位置（next-step splice / next-turn 队列），拒绝路径按 A7 实测样本收口 |
| OD-2b | 队列管理范围：S3 只做排队可见，还是纳入 `session/updateQueue` 编辑/撤回？ | A 只可见；B 可见+管理（编辑/撤回，依赖 A3） | **待裁决** | S3（行 13） | 裁决范围内 iOS 队列操作与 Mac web 行为一致；范围外操作不广告 |
| OD-3 | 附件范围：S4 只做图片（prompt image part + attachment 读取）还是含文件 receipts（`fileUploads/upload`）？ | A 只图片；B 图片+文件 receipts（行 100 随之进 S4 证据范围） | **待裁决** | S4（行 12、100） | 裁决范围内 iOS 发附件在 Mac web 可见且反向可读；范围外不广告 |
| OD-4 | follow 快照窗口：对齐官方 `maxMessages:500 + turnWindow` 还是维持 `maxMessages:6`？ | A 对齐官方；B 维持 6 | **待裁决** | S1 后续调优（不阻塞已交付的 S1） | 重连后 gap-seed 覆盖最近消息无重复/无缺口；**可测阈值（建议值，owner 裁决时可调）**：快照 records ≤ 500 条、快照 JSON ≤ 2 MiB、Go 侧解码 p95 ≤ 500ms（探针可测，A/B 任一选项均按此验收；B 另要求现状基准不回退） |
| OD-5 | 版本目标：专项收敛钉 alpha 通道（owner 实际在用）还是同时保 rc 通道兼容矩阵？ | A 只钉 alpha；B alpha+rc 矩阵 | **待裁决** | **仅** S2 的跨版本防御部分（A2 门控的 alpha.2 形状重写）与 rc 兼容矩阵；**不含** alpha.1 已证的 `commands/execute` 错参修复（该修复的证据来自运行座位 alpha.1 活体样本，§2.3/§5.2 的「过审后即可实施」对其单独成立，独立于本裁决） | 按裁决建立版本范围+样本包+契约测试矩阵；范围外版本 fail-closed 可诊断 |

## 5. 切片顺序与执行档案

### 5.1 S1 — assistant-stream 直播（✅ 已完成并部署，owner 单独批准先行）

- 官方锚点（源码行为层，`dsh-v0.1.7-alpha.2` 核对）：
  `packages/api/session-controller/src/history.ts:165-175`（opt-in 订阅）、
  `packages/api/session-controller/src/types.ts:485-541`（请求字段与帧
  union）、`packages/api/session-controller/src/client/transport.ts:179-215`
  （硬编码 true；快照必带基线；revision 逐帧 +1）、
  `packages/api/session-controller/src/client/sessions/assistant-stream.ts:120-180`
  （折叠：start 注册、无 start 的 chunk 忽略、index 连续、end 释放
  settlement）、`packages/core/agent/src/runtime-types.ts:355-363`（chunk 瞬
  态、journal 完成时落 `assistant/message`）。
- 实现：follow 请求带 `assistantStream:true`；帧折叠进 codec（text-delta→
  EventText、reasoning-delta→EventThinking、usage→用量事件；revision/index
  断档告警并暂停瞬态发射、不污染 journal 路径；快照基线 activeAttempt 前缀
  补发）；settlement 走 journal 只簿记不双发。
- 验证（三层分列，证据索引）：
  - **组件级**：单测 ×4 + 全包回归绿；活体逐帧证据两轮（154/135 帧）——
    `scripts/dshweb-phase0/s1-live-turn-frames.json`：revision 逐帧 +1、
    chunk index 连续、end.outcomeSeq 与 assistant/message journal seq 对齐
    三条断言程序化核验通过（文件内 `verification` 字段可独立复核）；计数：
    轮一 42 text-delta（82 字符）/86 reasoning-delta，轮二 44/65。
    早前 45/94、41/80 两轮为**摘要级历史记录**（无逐帧字段，已被上述记录
    取代，不再作为核验依据）。
  - **生产运行态**：Release 覆盖安装后 runtime 新代际（PID 89580，
    2026-09-23 10:34:41 启动，晚于 10:33:44 构建；runtime.json epoch
    ce1754ab；运行二进制内嵌 commit b4e42d5c5a9a = 仓库 HEAD）；特征输出 =
    四轮探针 turn 的 passive 通道逐条 `text_delta` 事件（S1 构建独有行
    为）、零复位——`scripts/dshweb-phase0/s1-runtime-log-excerpt.json`
    （180 行逐行脱敏日志）。
  - **owner 真机**：流式复测（已请 owner 验收）。
- 已知边界（随专项收敛）：abandoned（失败/重试）尝试的已发增量无法撤回
  （bridge-v1 无撤回语义，冷拉收敛）；revision 断档官方重开 follow，我们
  告警暂停（冷拉兜底对账）。

### 5.2 S2 — commands/goals：现行 bug 修复 + 版本漂移防御

- **第一项（现行 bug，过审后即可实施）**：活体证据（§2.3）证明运行座位
  alpha.1 的 `commands/execute` 要求 `{agentId, line, submittedAttachments}`，
  桥当前传 `images` 被拒——斜杠命令执行路径今天就是坏的（list 可用、execute
  报 descriptor 错误）。修复：payload 第三参改为 `submittedAttachments: []`
  （官方无附件调用语义），契约测试钉 alpha.1 形状（用已归档 verbatim 样本）。
- **第二项（跨版本防御，A2 门控）**：alpha.2 的 `agent` 参数 wire 形状样本
  取得后，按目标版本重写 **七面** 形状——commands/list、commands/execute、
  goals/get、goals/pause、goals/resume、goals/clear、goals/edit（edit 含
  `ref`+`request`）；descriptor 校验错误如实上报；两代并存按实测版本选择
  （只携带版本信息），不做静默双发，不从 alpha.1 外推、不因 get 已证外推
  mutation。
- 测试：契约测试逐面钉两代形状（alpha.1 用已归档样本——含四 mutation 的
  错形/正形记录；alpha.2 用 A2 七面样本）；升级座位后活体复测面板与全部
  goal 操作——对**有 goal 的会话**各做一次真实 pause/resume/clear/edit 动作
  并核对状态收敛（§2.3 收窄后，这是 goal 按钮端到端可用的唯一证据路径）。
- 实施时同步纠正：`agent/dsh-web/commands.go:47-56` 的旧注释与既有测试
  仍称 `images:[]` 被座位接受（2026-09-05 rc.2 时点结论，已被 alpha.1 活
  体证据推翻）——修 payload 时一并改注释与测试，不留矛盾断言。

### 5.3 S3 — 排队消息可见（inbox）

- 官方锚点：`packages/core/agent/src/types.ts:38-55`（InboxState：
  next-turn/next-step 两条有序表；UserMessage.id 跨表唯一）、
  `packages/core/agent-loop/src/inbox.ts:20-62`（inbox 投影只折叠
  `agent/inbox/spliced`；splice = {target, start, removedCount?, inserted,
  outcome?}）、`packages/core/session/src/types.ts:309`（`'user/message':
  UserMessage`——journal 落定事件与 inbox 成员同类型、同 id 空间）、
  `packages/core/agent/src/consumed-work.ts:83`（claim/cancel 折叠）。
- **写入所有权设计**（A3 活体确认 id 连续性后生效）：时间线对排队消息的唯
  一写入者是投影 reducer，按 **UserMessage.id** upsert——inbox splice 插入
  → 排队占位行；`user/message` 落定（同 id）→ 占位行原位替换为正式消息行；
  splice removedCount 移除 → 撤行。去重键 = id（占位与落定不双行）。
  `agent/inbox/spliced` journal 事件从 codec 已知控制面跳过表移入本切片映
  射。
- 范围与门：可见性（占位/落定/撤行）依赖 **A3a**（id 连续性未证实 → 可见
  性阻塞）；管理操作（编辑/撤回）仅在 **OD-2b=B** 时纳入，依赖 **A3b**
  （样本缺 → 仅管理范围阻塞，不影响可见性）。

### 5.4 S4 — 图片附件

- 官方锚点（调用顺序按源码）：
  1. **发送**：prompt content parts 内联 image part
     `{type:'image', mediaType, data, name?}`（`session-controller/src/types.ts:89-96`）；
  2. **准入持久化**：`session-controller/src/commands.ts:344-363`——模型
     image 支持校验（不支持则 `session/attachment-invalid`，
     MODEL_DOES_NOT_SUPPORT_IMAGES）+ `admitPromptContent` 持久化；
  3. **读取**：`session/attachment`（`index.ts:413-420`）读取**已被会话日志
     引用**的持久化图片——它不是上传端点。
- 实施与门：图片范围（OD-3 任一选项）依赖 **A4a**；文件 receipts 仅在
  **OD-3=B** 时纳入，依赖 **A4b**（样本缺 → 仅文件范围阻塞，不得靠
  image 样本放行）；iOS 发图 → prompt image part，接收侧经
  `session/attachment` 读取渲染；attachmentId/receiptId ↔ journal 引用映
  射在样本确认前保持待定。

### 5.5 S5 — 置顶/归档官方语义

- 官方锚点：`workspace-controller` pin/unpin/archive/unarchive
  （`WorkspaceArchiveSessionRequest{sessionId, stopActivity?}` 已核，
  `types.ts:122-132`）；workspace/follow 基线 pinned/archived 字段。
- 实施与门：**OD-1=A** 时实施官方语义切换（依赖 A5 样本，缺则阻塞，不
  凭源码形状直接实施；验收 = 归档行隐藏/恢复与 Mac web 一致）；**OD-1=B**
  时 S5 不实施（置顶维持桥本地、归档/恢复维持现状，验收见 OD-1 B 选项），
  A5 样本不采集、不阻塞其他切片。

### 5.6 S6 —（future 池）session/control、search/fork、workspace 管理、
workspace-files、job-controller、skills、fileReferences、cordis 互动面
（dynamicCordisRunner 12 面 + pluginManager/pluginInventory 插件面）、
follow 窗口对齐

证据齐后逐项立档再实施；不与本轮捆绑。

## 6. 测试策略

- 每切片：官方形状契约测试（fixture 来自 Gate A 真实样本脱敏）+ 既有全包回
  归；fake server 只验证内部行为，不反向证明协议形状。
- 活体验证分层如实标注：源码行为（锚点核对）/ 组件级（探针）/ 生产
  runtime 级（日志特征输出）/ owner 真机验收。对账数字交付（逐帧记录 +
  程序化核验结果，不用摘要反证细节断言）。

## 7. 发布门

1. 全部切片定向测试绿 + 一次增量 Release 构建覆盖安装；
2. 运行态验证：进程代际 + 构建身份（二进制内嵌 commit）+ 本次提交独有特征
   输出；
3. owner 真机矩阵：iPhone 流式（S1 已待验）、斜杠命令执行（S2 第一项修
   复后）、goal、排队可见、附件、置顶/归档双端一致、dsh 重启行保持绿；
4. secret scan（cookie/凭据不进日志/诊断/hello_ack 的红线复扫；版本状态
   只携带版本信息）。

## 8. 完成定义

§4 底表 116 行中所有「已支持/切片」行四层对齐且有契约测试钉住；deliberately
unsupported 行在能力地图登记且 `hello_ack` 不广告；future 行有明确再入口
（证据编号）。版本策略（§2）落地：descriptor 漂移 fail-closed 可诊断。

## 9. 评审意见处置表

### 9.1 第一轮（v2 处置）

| # | 意见 | 处置 |
| --- | --- | --- |
| 1 | S4 附件方向冲突 | 采纳：§5.4 重写、A4 拆组 |
| 2 | Gate B 底表不完整 | 采纳：v2 扩为 59 面（v3 进一步补齐为 67 面单行，见 9.2 #1） |
| 3 | 漂移证据不对等 | 采纳：§2.3 归档样本 + S2 实施门 |
| 4 | Gate A 缺退出条件 | 采纳：§3 五要素 |
| 5 | S3 写入所有权 | 采纳：§5.3 + OD-2 拆分 |
| 6 | S1 证据索引 | 采纳：§5.1 三层分列 + phase0 索引 |
| 7 | OD 边界/编号 | 采纳：OD-1/2a/2b/4、引用修正 |

### 9.2 第二轮（v3 处置）

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| 1 | 漏 8 个 Remote 表面 | 采纳：穷举 `extends TypertRemoteService`（11 服务），§4 补为 67 面单行底表 | skills/fileReferences/credentials/directoryPicker 逐项 disposition；credentials 因红线不做运行态断言，仅源码登记 |
| 2 | S1 证据不可复核 | 采纳（选强闭合）：新捕两轮逐帧记录 + 程序化核验；旧 45/94、41/80 如实降级为摘要级历史；runtime 证据补 180 行逐行日志 + epoch + 二进制 commit 身份 | 未采用「降级为摘要」备选：完成记录是后续实施的事实基础，降级留空洞 |
| 3 | execute 无样本 | 采纳（选强闭合）：补四种形状活体探针（含正向 submittedAttachments）；**并修正 v2 断言**——alpha.1 execute 已是 `{agentId,line,submittedAttachments}`，发现现行 bug（桥传 images 被拒）登记为 S2 第一修复项 | 未采用「移出已实证表」备选：证据已取得且暴露了需要过审后修复的生产缺陷，移出会掩盖它 |
| 4 | Gate A 五要素未落实 | 采纳：§3 每行补文件索引/字段级断言/负例（或不适用范围理由）/缺项即停 | A1b 负例不适用已给范围理由；A6/A7 同 |
| 5 | 建议：逐 Remote 单行 + 区分状态 | 采纳：§4 每面一行，状态列区分已支持/切片/future/deliberately unsupported | terminal/account 10+7 面按同处置分组编号逐一登记 |

### 9.3 第三轮（v4 处置）

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| 1（阻塞） | A2 只证 get，未覆盖桥消费的 pause/resume/clear/edit | 采纳（选补齐取证）：A2 扩为七面逐项样本/负例/契约测试；goals/complete、create 标 future；另补 alpha.1 四方法安全探针（错形 descriptor 拒绝 + 正形业务错误），证明四方法请求形状在 alpha.1 被接受（成功路径未验证，v5 按 r4 #3 收窄，见 §9.4） | 未采用「从支持范围移除」备选：goal 管理是已交付 live 能力，移除即产品回退 |
| 2（建议） | terminal/account 仍为合并行 | 采纳：10+7 面逐项展开为单行 | — |
| 3（建议） | A7 负例不适用理由过宽 | 采纳：理由收窄为「本轮只做形状镜像」；启用 steer 前必须补拒绝路径样本（`session/steer-unavailable` `types.ts:222`、队列 steer 拒绝 `commands.ts:477-479`，锚点已核） | — |
| （主动） | v3 底表未含桥消费的 packages/api 外 namespace | v4 补齐：全仓穷举 + 14 行（68–81）+ 13 类观察清单 | 随意见 1 一并闭合，防下轮同类发现 |

### 9.4 第四轮（v5 处置）

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| 1（阻塞） | 观察清单含官方 Web 实际调用的 Remote（pluginManager/pluginInventory/permissionPresets/messageFeedback/subagents + fileUploads） | 采纳：调用点逐一核实（另自查补 officeToPdf、sessionReferenceResolver 两处），22 面入底表（行 82–103）逐面 disposition；挂载证据用错形探针取得（9 namespace 已挂载、3 类 404 未挂载）归档 `alpha1-web-assembly-mounts.json`；底表声明改为「穷举 + 调用点核实 + 挂载探针」，不再宣称无条件全覆盖 | 未采用任何排除备选：所有被点名的调用点均以入表处置 |
| 2（阻塞） | A2 edit 负例把缺 request 等同 GOAL_INVALID_EDIT | 采纳：负例拆为「缺 request＝网关 descriptor 负例（alpha.2 样本）」与「空 request 且有 goal＝业务负例**待捕**（源码预期 GOAL_INVALID_EDIT，以样本为准，不预定）」 | — |
| 3（阻塞） | goal「今天可用、无新 bug」超出探针证明范围 | 采纳：§2.3 与证据文件 `goal_mutations_status` 收窄为「形状被接受；有 goal 时的成功路径未验证，列入 S2 活体复测（对真实 goal 各做一次真实动作并核对状态收敛）」；§5.2 测试计划同步 | 未采用「补成功路径证据」备选：需在有 goal 的会话上做真实 mutation，属 S2 活体复测的既定内容，方案阶段不预跑 |

### 9.5 第五轮（v6 处置）

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| 1（阻塞） | pluginRegistryProbe 探错方法（list 不存在，唯一方法是 fastest） | 采纳：错误探针从证据文件移除并留更正注记；正确方法重探——**已挂载**；入底表行 104（future） | — |
| 2（阻塞） | dynamicCordisRunner 实为 12 面、有扩展客户端调用点、交叉引用不实 | 采纳：12 面入底表（行 105–116）逐面 future；调用点核实（ui-cordis + cordis-client-runner）；§5.6 补 cordis 项；全仓对账 28 服务 123 面 = 底表 116 + 装配排除 7 | v5 只搜 packages/client/ 漏了 packages/extensions/ 客户端，已改正 |
| 3（阻塞） | A7 引用了 updateQueue 的错误码当 direct steer 负例 | 采纳：A7 改为 direct 路径（agent.steer，commands.ts:372；拒绝映射 session/agent-busy :380，以样本确认）；steer-unavailable 归 A3/OD-2b | — |
| 4（阻塞） | Gate A 写操作缺隔离与收尾规则 | 采纳：新增 §3.1 逐项隔离规则（隔离会话/工作区、允许动作、结束归档、证据记录；无法满足→另座测试实例并保持阻塞） | — |
| 建议 a | commands/execute 标现行受损 | 采纳：行 69 状态改「现行受损（S2 第一修复项）」；§5.2 补 commands.go 旧注释/测试同步纠正项 | — |
| 建议 b | A2 edit 锚点补到抛错行 | 采纳：`:331-333` → `:331-334` | — |

### 9.6 第六轮（v7 处置）

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| 1（阻塞） | OD-1～OD-5 裁决表从 canonical 消失 | 采纳：恢复为 §4 末尾 canonical 裁决表（问题/互斥选项/决定状态/影响切片/验收标准），并新增「未裁决的切片保持阻塞」规则 | v5 编辑误删，根因是替换区间覆盖裁决块——本轮起对 §4 的结构性编辑先做边界检查 |
| 2（阻塞） | A5 负例与上游相反（unarchive 是幂等成功） | 采纳：负例改可达（session-active，commands.ts:174 亲核）；unarchive 幂等成功入样本（commands.ts:183-194、workspace/src/index.ts:385-405、官方测试 :1086,1101 亲核） | — |
| 3（阻塞） | A3 要求活体制造不可提交的内部不变量错误 | 采纳：非法 splice 降为定向测试负例（inbox.ts:32-55 亲核）；活体负例改为 updateQueue steer 拒绝的请求/响应样本 | — |
| 4（阻塞） | goal 两处仍称按钮可用 | 采纳：§2.3 证据行与证据文件 purpose 统一为「形状被接受、端到端成功未证」；§4 行 71–74 加限定 | — |
| 5（阻塞） | steer 锚点错行（:374 实为 binding.commit） | 采纳：三处改 `commands.ts:372`（源码亲核）；v6 新增锚点全部复核 | — |

### 9.7 第七轮（v8 处置）

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| 1（阻塞） | execute 修复与 OD-5 待裁决冲突 | 采纳：OD-5 影响范围收窄到跨版本防御/rc 矩阵；execute 修复显式排除在 OD-5 外 | — |
| 2（阻塞） | OD-2b=A 仍被管理样本门卡死 | 采纳：A3 拆 A3a（可见性，必需）/A3b（管理，仅 B 选项启用）；S3 阻塞规则按范围拆分 | — |
| 3（阻塞） | OD-3=B 缺文件 receipts 证据门 | 采纳：新增 A4b 条件门（上传→receipt→准入→读取链独立取证）；不得靠 image 样本放行 | — |
| 4（阻塞） | OD-1 B 选项无范围却共用 A 验收 | 采纳：B 选项补全（S5 不实施、行 22–25 降级 future）+ 独立验收；§5.5 按选项分写 | — |
| 建议 | OD-4 缺可测阈值 | 采纳：补建议值（records ≤ 500、JSON ≤ 2 MiB、解码 p95 ≤ 500ms），owner 可调 | — |

### 9.8 勘误（v8 过审后定向纠正，依 codex 审核结论 2026-09-23）

codex 审核认可 r8 对第七轮四条阻塞的闭合判断，但按最初约定的来源门指出
两处需纠正后才能正式认定通过；本节为定向纠正记录（非新一轮评审）：

1. **A4a 锚点修正**：MODEL_DOES_NOT_SUPPORT_IMAGES 的校验与抛错实际在
   `commands.ts:350-355`（reason 字段在 :354），v8 原文误写
   `:346-353`（346 是 `try {`，353 是错误消息字符串）——已修正。r8 报
   告自身写明代码在 :351-355 却仍判锚点全部吻合，属评审员核对疏漏，
   由 codex 审核纠正。
2. **r8 报告 P0 来源清单补齐**：跨仓评审来源门要求记录配套 iOS 工作树，
   r8 报告原清单省略——已在报告中补记
   `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`、分支
   `feat/ios-native-message-timeline`、提交
   `dd3911785868930dfbf45e681e0837e88333148f`（codex 审核亲核）。

两处纠正经定向复核（见
`2026-09-23-dsh-web-source-first-convergence-plan-review-r8-recheck.md`）
后，r8 的通过结论按既定规则正式认定。
