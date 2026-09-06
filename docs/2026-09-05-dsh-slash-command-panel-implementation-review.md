# DSH「/」按钮命令面板实施方案 审计报告（audit-plan）

- 日期：2026-09-05
- 对象：[docs/2026-09-04-dsh-slash-command-panel-implementation.md](2026-09-04-dsh-slash-command-panel-implementation.md)
- 方法：audit-plan skill——方案中每条外部格式（DSH rc.2 wire / 会话日志事件）内容形状断言，
  本轮以「源码 @ tag `dsh-v0.1.1-rc.2`（b150a551b8）+ 本机活体只读探针」逐项验样；Mac/iOS 代码
  锚点在两仓当前 HEAD 复核。
- 活体座位：127.0.0.1:3080，pid 1055（与调研 v1.2 同一座位，未重启）。**本轮探针全部为
  只读 list 类**（`session.list` 取 id、`commands/list` 三种 payload 形状各一发），零 execute、
  零写、零消息——符合方案 §4.6 自设的护栏。

## 1. 核心结论

**方案的事实链全部验真，5 条推荐（D1–D5）全部成立、无一推翻；但存在 1 个 P0 级缺口：
方案只登记了 `command/run|done` 走已知丢弃，而面板 6 条命令中 4 条（plan/compact/goal/feedback）
执行后会向会话日志追加 dsh-web codec **不认识**的事件类型（`plan/mode`、`compaction/*`、
`goal/change`、`feedback/record`），codec default 分支是 reset（fail visibly）。**不做处理，
则本功能的核心动作（iPhone 点 `/plan`）会在事件到达瞬间重置 iPhone 侧解码器；有 open turn 时
（pendingIntent 在 in-turn 边界生效）就是 2026-08-16 真机矩阵记载过的「杀 mid-turn 码器」同类
故障。**该项必须进 Phase 1 工作项。

另一收获：方案 §4.6 留白的「list payload 包装（是否 args）」本轮已用活体探针**直接收口**
（答案：必须 `{args:{agentId}}`，见 §2），§9 风险表第 1 行可凭证据销项。

## 2. 逐内容类型验证表

| # | 内容类型 | 方案断言 | 验样结果 | 评级 |
| --- | --- | --- | --- | --- |
| 1 | `commands/list` 请求包装 | §4.6 留白：「是否 args 以活体为准，实施前复测」 | 活体探针（本座位）：`{args:{agentId}}` → ok，返回 6 条；`{agentId}` 直传 → `Remote payload must contain exactly one plain-object args field`；`{args:{sessionId}}` → `missing "agentId"; unexpected "sessionId"`。源码侧坐实：rc.2 gateway `invokeRpc` 对两段式 Remote 端点强制恰一个 `args` 字段（`packages/api/gateway/src/index.ts:198-209`） | 🟢 已收口（可写死进 §4/§5.3） |
| 2 | `commands/list` 响应形状 | §4.1：6 条、name 排序、`CommandDescriptor {name, description, input.hint}` | 活体：value=裸数组（无 commands 包装），恰好 `compact, export, feedback, goal, permission, plan`；descriptor 逐字段吻合（§5.2 示例的 `plan` description/hint 与活体逐字一致）。源码：`list(agent)` 带 `.sort(name)`（rc.2 `interaction/commands/src/index.ts`）；官方注册表 `KNOWN_SESSION_EVENT_TYPES` 无关此节 | 🟢 |
| 3 | `commands/execute` 生产形状 | §4.2：`{args:{agentId, line}}`，锚点 `permission_mode.go` | `agent/dsh-web/permission_mode.go:94-101`（`commandsExecuteRequest/Args`）+ `:111 applySessionPermission` + `:124` 调用，逐行吻合；失败判定（`kind==error`、双空=not matched）`:133-138` 即方案 §4.3 所指「permission 路径已如此」 | 🟢 |
| 4 | 未知命令语义 | §4.3：handler 返回 `undefined`、不写日志 | rc.2 `interaction/commands/src/index.ts` `execute`：`parseCommand` 未命中或名字未命中直接 `return undefined`，位于任何 lifecycle append 之前；docstring 原文 "Admission misses (syntax or unknown name) log nothing" | 🟢 |
| 5 | 解析正则 | §4.4：`/^\/([a-z][a-z0-9_-]*)(?=$|[\t\n\r ])/u` | rc.2 `parseCommand` 逐字符一致 | 🟢 |
| 6 | **命令执行的会话日志副产物事件** | 方案仅登记 `command/run|done`（§2 不做行、§9 行 4）为 known 丢弃 | `command/run|done` 确在 codec Class ② 清单（`agent/dsh-web/codec.go:225-227`）🟢；**但** `/plan`→`plan/mode`、`/compact`→`compaction/start|summary|end|prune`（rc.2 `compaction-basic/src/region.ts:189,215,447`、`compaction-tool-result-pruner`）、`/goal`→`goal/change`、`/feedback`→`feedback/record` 均为 durable 会话日志事件（官方 `KNOWN_SESSION_EVENT_TYPES` 全收录，rc.2 `core/session/src/known-event-types.ts`），mux `session/event` 是 raw passthrough → 必达 codec；而 dsh-web codec **对这四族零处理**（全 agent/ 目录 grep 仅退役 `.dsh` 的 known_types.go:36 有 plan/mode），default 分支 `resetf("unknown required event type")`（codec.go:233-236）。envelope `ignorable` 旁路（codec.go:161-167）只救「更新版本 harness 写的事件」，这四族是本版本注册表内事件、不带该标记 | 🔴 **P0：方案未覆盖** |
| 7 | `/export` 执行语义 | D2：失败展示官方错误；不做 iPhone ZIP | rc.2 `session-query/session-log-export/src/index.ts`：成功固定返回 `text:"Session log download requested."`，**真正下载由浏览器插件观察成功后触发**（`client/index.ts:38`）。即 iPhone 上成功=本机无产物、静默；若 Mac web 客户端同时在线，ZIP 会下载到**那边**。方案 §6.3/§8 矩阵未写成功路径的表现 | 🟡 需补一句成功语义 |
| 8 | Mac 机制锚点 | §5.1/§5.2：derive 类型断言追加 cap、bridge 协议同步点 | `go-bridge/backend_capabilities.go:5` derive 机制吻合（`ModeSwitcher`→`permission_mode` 即三件套先例 :43-45）；`not_supported`/`invalid_params` 错误码惯例存在（handlers.go:1206,1722 等）；协议 schema 真实路径为 **`docs/protocol/schema/bridge-v1.types.ts`**（方案 §5.2 写 `schema/bridge-v1.types.ts` 缺前缀）；`docs/protocol/samples/` canonical pack 惯例存在 | 🟢（1 处路径笔误 P2） |
| 9 | iOS 锚点（含漂移复核） | §6：`ChatInputAccessoryView` leftToolbar/compactRow、capability 点亮、permission 菜单 | iOS 仓已从钉位 `db7972c` 前进 8 提交至 `e6fb7ee`（全为 claudecode 配套），但 `ChatInputAccessoryView.swift`/`BackendModels.swift`/两个 Bridge client **零改动**——锚点在 HEAD 仍有效：`compactRow`:117、`leftToolbar`:150（attachButton :323）、capability 消费 `capabilities.contains("permission_mode")`（CCCodeBridgeBackendClient.swift:182）、`set_permission_mode` client（CCCodeBridgeClient.swift:844）、`BackendKind.deepSeekWeb`（BackendModels.swift:22）。iOS 协议 mirror 在仓根 `docs/protocol/bridge-v1.md` | 🟢（钉位需刷新，P1） |
| 10 | 锁定范围符合性 | 只做 DSH host 命令/输入框按钮/execute 不当聊天；技能、键入 /、另四家不做 | §2 不做表与 owner 锁定项一一对应，无越界；`send_message` 隔离在 §4.5/§5.3/§6.4 三处反复钉死并有测试项 | 🟢 |

## 3. 未验内容类型（无——原留白项本轮已补样）

方案原有的唯一 wire 留白（list payload 包装）已由本轮探针收口（§2 行 1），方案再无
无样本支撑的外部格式断言。bridge 两个新 RPC（`list_session_commands`/`execute_session_command`）
是 CordCode 自有设计而非外部格式，按设计评审（不适用样本规则）：形状、错误码、capability
门控、回滚路径均与既有惯例自洽。

## 4. 脚本/探针交叉验证记录

- 探针采用「正确形状 + 两个错误形状对照组」三发策略：A（`{args:{agentId}}`）成功、
  B（直传）与 C（`sessionId` 键）分别被网关以两条**不同**错误信息拒绝——两条错误信息互相
  印证了「包装必须 args、键名必须 agentId」，排除「A 只是碰巧被宽容解析」的解释。
- codec 缺口（§2 行 6）采用双源验证：CordCode 侧 grep（四族事件在 dsh-web 零处理）与
  DSH 侧源码（四族确为 durable log 事件、mux raw passthrough）交叉，非单边推断。
- 归因核验：`/export` 「iPhone 上没反应」的归因取自 rc.2 源码下载触发链（client 观察
  command 成功后 `controller.download`），非猜测。

## 5. 修订优先级

### P0（不修不批）

1. **Phase 1 增加 codec 工作项**：`agent/dsh-web/codec.go` Class ② 已知丢弃清单追加
   `plan/mode`、`compaction/start`、`compaction/prune`、`compaction/summary`、
   `compaction/end`、`goal/change`、`feedback/record`（先例：`approval/asked|decided`
   2026-08-16 同因入清单），配单测：fake 流喂这些类型 → 不 reset、不产 timeline 事件。
   同步 §2/§9 登记行——现文案「生命周期事件 codec 已当 known 丢弃，保持」**只对
   `command/run|done` 为真**，对上述四族为假。附带说明（产品可接受即可）：known-drop 意味着
   `/compact` 后 iPhone 旧时间线不重排（官方靠投影重渲染），第一期验收口径不受影响。
   （若想按 2026-08-13 dsh 设计意图对 compaction 做 surface replacement 属更大工程，
   不应塞进本项。）

### P1（批前改文档）

2. §4.6/§5.3/§9-行1：写入已验死的 list 形状——请求 `{args:{agentId:<sid>}}`、响应裸数组；
   §9 风险行 1（「list payload 包装与 execute 不一致」）凭探针销项。
3. §0 来源清单刷新漂移：iOS 配套 `db7972c → e6fb7ee`（8 提交，claudecode 配套，四个锚点
   文件零改动已复核）；DSH checkout `49a606bc → d347e70`（协议仍以 tag rc.2 为准，不受影响）。
4. D2 补 `/export` 成功语义一句：iPhone 成功=静默无产物（host 文案 "Session log download
   requested."），Mac web 客户端在线时 ZIP 下载到该客户端；§8 矩阵 #6 建议补 export 一行
   或在 #6 备注里点名。

### P2（顺手修）

5. §5.2 `schema/bridge-v1.types.ts` → `docs/protocol/schema/bridge-v1.types.ts`。
6. §5.1 `SessionCommand` 可加一句注释：官方 descriptor 另有 `input.images`（活体：goal/plan
   为 true），第一期不采集不下发，结构有意丢弃——防止二期读旧文档的人以为字段丢了是 bug。
7. §7 Phase 3 括号引「D3」实指 §8 真机矩阵（D3 是无参决策编号），建议改引 §8。

## 6. D1–D5 裁决复核（owner 授权可推翻，本轮均维持）

| # | 推荐 | 裁决 | 依据（本轮增量证据） |
| --- | --- | --- | --- |
| D1 | `/permission` → 现有权限菜单 | **维持** | 菜单/写路径全链在（iOS :182/:844 → Mac SetLiveMode → execute `/permission <preset>`）；官方裸 `/permission` 也是弹选择器，等价成立 |
| D2 | `/export` 留列表走 execute | **维持**（补成功语义，见 P1-4） | 失败可见成立；成功语义已查明（§2 行 7） |
| D3 | 第一期无参数 | **维持** | `/plan off`、`/plan <msg>` 属二期；无参行的 6 条命令全部可裸执行（/goal 裸=查看、/feedback 裸=错误可见，均不破坏） |
| D4 | capability 名 `session_commands` + 新接口 | **维持** | derive 机制先例在（:43-45）；与死接口 `CommandProvider`（core/interfaces.go:702）无碰撞；命名与既有 snake_case 一致 |
| D5 | 无会话隐藏按钮 | **维持** | 网关探针 C 证明 list 强依赖 `agentId`；无会话无合法 id，隐藏是唯一真话 |

## 7. 结论

方案**有条件通过**：事实基线（调研 v1.2）引用可靠、wire 断言全部验真、范围锁死无越界、
分期与回滚自洽。条件 = 落实 P0-1（codec 已知清单扩容）与 P1 文档修订。P0-1 不修，
owner 真机矩阵 #3（点 plan）与 #6（compact/goal）存在「RPC 绿灯、后台流 reset」的验收盲区。

## 8. 复评（v1.1，2026-09-05）——**通过（无条件），可开工**

对 v1.1 全文复核，逐项核对：

| 项 | v1.1 落点 | 核对 |
| --- | --- | --- |
| P0-1 codec 七事件 | §4.7（四族事件表 + 修法）、§5.3（Codec P0 + 单测「七 type 各喂一帧→不 reset、Events 空」）、§7 Phase 1（发布阻断 + 验收）、§8 矩阵 #3/#6「不 reset」+ 尾注、§9 风险行 + 回滚保留注、§10 例外声明 | ✅ 全部落位；事件清单与验样一致（plan/mode + compaction×4 + goal/change + feedback/record = 7）；`compaction/prune` 超集收录（rc.2 tool-result-pruner 可在 compaction 期触发）属保守正确 |
| P1-2 list 形状写死 | §4.1/§4.2（请求 JSON + 两条拒绝原文、裸数组、input.images 注）、§5.3（`commandsListRequest{Args:{AgentID}}`）、§9 行 1 划线销项 | ✅ 转录与探针原始输出一致 |
| P1-3 钉位刷新 | §0：Mac `6197da5`、iOS `e6fb7ee`（8 提交 + 锚点零改动复核注）、DSH checkout `d347e70` + tag 钉死、座位 pid 1055 | ✅ 与本轮实测一致 |
| P1-4 /export 成功语义 | D2 重写、§6.3、矩阵 #7 新行、§9 两行 | ✅ |
| P2-5/6/7 | §5.2 路径前缀、§5.1 input.images 注、§7 Phase 3 改引 §8 | ✅ |

v1.1 未引入新的无样本断言；新增文字均为本轮探针/源码结论的转录，无失真。
附带一个实施提醒（非阻断）：§5.1 `SessionCommand.Hint` 在顶层，官方在 `input.hint`，
且 `compact`/`export` 无 `input` 键——Go 实现需中间 wire 结构（`Input *struct{Hint}` 指针
或 omitempty）再映射，§5.3 的「裸数组映射 6 条」单测会兜住这条路径。
