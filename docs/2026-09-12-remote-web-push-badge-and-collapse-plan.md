# Remote Web 推送：通知折叠治理 + 应用图标角标 实施计划

日期：2026-09-12  
状态：**实施计划（本轮不写代码）**  
适用面：Remote Web PWA（`/web/`，iOS「添加到主屏幕」的 web app），协议面 `web_push_v1`  
读者：接手的实现 agent。先读 §1 现场与 §2 现状核查，再按 §6 的批次落地。

相关文档：`docs/protocol/bridge-v1.md`「Web Push (`web_push_v1`)」章节（canonical）；iOS 仓镜像 `cordcode-ios-plan-approval/docs/protocol/`。

---

## 1. 触发与现场

Owner 2026-09-11 报「web App 收不到通知」，排查后闭环：

- 推送链路**是通的**：当天 78 次投递尝试，与 Codex Desktop 回合完成**逐条 1:1 对齐（差 1–3 秒）**。
- 真正症状是**通知被 iOS 折叠成一整组摘要**：几十条堆在一起，新通知不再以横幅浮现，把折叠的几十条划掉后，再发新消息就能正常看到通知。
- 于是产生两个诉求：① 治折叠（通知量/聚合）；② 给 web app 图标加**角标**，让「有多少件事在等你」由一个数字表达，而不是靠堆横幅。

---

## 2. 现状核查（代码锚点，均已核）

| 面 | 现状 | 锚点 |
| --- | --- | --- |
| 通知文案与聚合键 | `tag = "cc_" + keyHash[:16]`、`Topic = "cc_" + keyHash[:16]`——**按通知唯一**，所以每条通知各自成条，离线积压也不会被合并 | `go-bridge/web_push_dispatcher.go` `buildPayload` / `keyHashTag` |
| 通知键 | `<backend>\|<sessionId>\|<turnId>\|completed`（permission 为 `…\|<requestId>\|permission`）⇒ **每个回合一条通知**，跨回合不会合并 | `go-bridge/web_push_producer.go:160,189` |
| 类别门 | 只有 `completion` 开启；`permission` / `input` / `error` 三类 `Passed=false`（等真实样本 EVT-PERM-1 / EVT-INPUT-1 / EVT-ERROR-1） | `web_push_producer.go` `webPushKindGates` |
| 载荷 | `schemaVersion:1 { notification{title,body,tag}, target{bridgeId,backendId,sessionId,eventId,anchor} }`——**没有角标字段** | `go-bridge/web_push_protocol.go:64–84` |
| 载荷校验 | SW 只认 `schemaVersion === 1` + `notification{title,body,tag}` + target 形状；**任何 push 都必弹**（非法载荷弹「CordCode 推送数据错误」）——无静默分支 | `remote-web/public/sw.js` `parsePayload` / `push` 监听 |
| 客户端订阅 | 只比对 VAPID 公钥：一致就 `reused`（重注册同一 endpoint），**不校验订阅是否仍有效** | `remote-web/src/core/push/push-manager.ts` `reconcile` |
| 投递侧 | fan-out 到全部订阅；404/410 只记 `expiry_unverified`、**永不清理** | `web_push_dispatcher.go`；`webPushExpirySemanticsProven = false` |
| 原生 App 角标 | 只请求了 `.badge` 权限，**从未设置**应用图标角标 | iOS 仓 `Services/NotificationService.swift:48` |

平台事实（外部，供设计定界）：

- 苹果官方口径：web push = Push + Notifications + **Badging** + Service Worker；角标用 `navigator.setAppBadge` 设、`navigator.clearAppBadge` 清；`setAppBadge(0)` 与 `clearAppBadge()` 等效（WebKit 官方博客 2023-04-25「Badging for Home Screen Web Apps」）。
- iOS **16.4+** 且**必须已「添加到主屏幕」**；API 只在主屏幕 web app 暴露（Safari 标签页与 WKWebView 内都没有），用 `'setAppBadge' in navigator` 特性检测。
- **角标权限的真实模型（2026-09-12 评审按 WebKit 官方原文校正）**：`setAppBadge` 随时可调；角标是否**显示**只取决于通知权限是否授予。iOS 另有独立的「徽标」用户开关（设置 → 通知 → 该 web app），但 **WebKit 明确「we never expose this user preference to the web app」**——该开关对网页完全不可见、调用也不会因此 reject，徽标关闭时 `setAppBadge` 照常 resolve、只是图标不出数字。`NotAllowedError` 只在 `Notification.permission !== "granted"` 时抛出。⇒ 任何「检测徽标开关状态」的设计都不可实现，只能检测「API 存在 + 通知权限」。
- **Safari 不允许静默推送**：每次 push 必须立刻呈现通知，否则**吊销该站点推送权限** ⇒ 不存在「只更新角标、不弹通知」的后台路径。
- Android Chrome 不暴露该 API；Firefox 全平台不支持 ⇒ 必须特性检测 + 静默降级。
- 角标是**提示值**：大数会被截断（常见 99 / 999+），`0` 等同清除。

---

## 3. 目标与非目标

**目标**

1. 同一条会话的连续完成不再堆成几十条：iOS 上表现为「每个会话最多一条 + 一个数字」。
2. web app 图标显示角标，含义 = **本设备自上次成功前台确认以来，有更新待查看的会话数**。同一会话连续完成只计 1，角标与 A2 的「每会话一条」口径一致。
3. 角标按认证设备独立记账并持久化；只以真实 completion 事件和设备确认推进状态，不从通知条数、局部投影或客户端自增猜测。
4. 全程 fail-closed：取不到该设备的持久化状态就**不下发角标字段，也不清除现有角标**；只有服务端确认计数为 0 或设备成功确认已查看后才清除。

**非目标（本计划不做）**

- 不改通知类别门（`permission` / `input` / `error` 仍按样本门管控）。
- 本轮角标不表达「待审批 / 待回答」数量。只有对应类别的真实样本门开放、且动作产生时能够合法发送可见通知后，才能另案把它们纳入后台角标。
- 不做静默推送（平台禁止）。
- 不动 VAPID、订阅存储与 404/410 清理策略（见 §9）。
- 不做原生 iOS App 的应用图标角标（另案，见 §9）。

---

## 4. 方案 A：通知折叠治理

折叠的根因是**条数**：每个回合一条、`tag`/`Topic` 都按通知唯一，几十条自然堆成一叠。本版明确同时实施 A1+A2；A3 移出本轮。

### A0. 投递覆盖不依赖客户端打开 session（前置不变量）

- 有效 Web Push enrollment 面向该设备可见的 backend completion，不得以 Web App 当前 backend、当前 session、`set_observation_scope`、session subscriber 或 reducer 是否已 hydrate 作为候选资格。客户端从未打开过的 session 也必须通知。
- 仍保持单一 timeline ingest owner：不得为了发送通知给未打开 session 构造隐藏 projection。服务级 backend 使用明确的 replay-free live event seam；若上游事件本身要求 per-thread attach（Codex app-server），replay-free observer 必须通过 `thread/loaded/list` / recency head 执行 `thread/resume(excludeTurns:true)` 建立订阅，并周期性捕捉后来驻留的 thread。DSH/OpenCode Web 全局流没有 cursor：必须先过滤 lifecycle，只放行本订阅内观察到的 turn，并在同一 turn completion 后拒绝重连重放。Grok Build inline/leader 模式都以 per-session `updates.jsonl` 为 durable live journal：全局 watcher 对启动前已完成历史建立 baseline，仅保留未完成 turn 的尾部，新建 journal 从 0 观察，truncate/rewrite 重新 baseline。Claude Code 没有全局事件 API，使用按 watcher 启动时间建立 byte cut 的 transcript 增量观察。首次可见不等于新建：跳过启动前 timestamp 的完整记录，只消费启动后新增/完成的记录，防止 Mac 端打开旧 session 时回放历史。
- 两条通知-free 路径都只接受真实 live completion，并继续使用每回合唯一 notification key 做持久化幂等；hydrate、history、resume replay、启动时已有 transcript、开启通知之前积累的历史记录均不得补发。
- replay-free source 没有权威 Kernel `bridgeEpoch:seq` 时，必须派生确定性的域分隔 `target.eventId`（同一 turn 重试稳定，不同 turn 不同），不得发送空 `eventId`；`web_push_v1` 的 target 四个身份字段全部必填。
- replay-free completion 的正文预览只能来自同一 live source 在 terminal 前实际观察到的 assistant text delta，按 `(backendId,sessionId,turnId)` 有界累积并在 completion 时消费；不得从历史回读，也不得因终态帧无正文而编造内容。
- session 已被交互 relay/observation 接管时，通知-only observer 只推进自己的 source cut，不与现有 ingest owner 竞争，也不重复产生候选。
- Claude 同一 logical turn 可能写出 textless `end_turn` 后再写带正文 `end_turn`；通知-only observer 必须按 turn 挂起/去重并优先等待真实预览。两个物理终态行不得产生两条通知，队列内重复也不能依赖 delivery ledger 事后撤销。

### A1. 稳定的 `Topic`（低风险，建议直接做）

- `Topic` 是 RFC 8030 的**离线合并**机制：同一 Topic 的**待发**通知只保留最新一条（已投递的不受影响）。
- 现值按通知唯一 ⇒ 完全不合并；改为**按会话稳定**后，Push Service 尚未交付的积压会收敛成一条。锁屏、休眠不等于消息仍滞留在 Push Service；已经交给系统通知中心的消息不受 Topic 影响。
- 会话聚合身份必须是 `(backendId, sessionId)`，不能只用 `sessionId`。固定算法：`sessionAggregationKey = base64url(sha256("web-push-session-v1\0" + backendId + "\0" + sessionId))[:22]`，`Topic = "ccs_" + sessionAggregationKey`。结果仅含 URL/filename-safe 字符且固定 26 字符。
- 约束：Topic 最长 32 字符且限 URL/filename-safe 字符集；超限/非法会被端点拒（`BadWebPushTopic`）。

### A2. 稳定的 `tag`（接受替换取舍）

- `tag` 决定 iOS 上同 tag 通知**互相替换**。`tag = "ccs_" + sessionAggregationKey`，与 Topic 共用同一个 `(backendId, sessionId)` 聚合身份；不同 backend 的同名 session 不得互相覆盖。
- 取舍：**丢历史**（同会话早先的完成通知被替换）。本版明确接受，以最新一条通知承载深链，以角标表达有更新待查看的会话总数。

### A3. 发送侧合并 / 节流（本轮不做）

- **禁止**通过从 notification key 删除 `turnId` 实现合并。现有 `LedgerShouldSend` 对同一 key 一旦 `accepted` 就永久不再发送；改成 session-only key 会退化成「每个会话一生只通知一次」，不是可替换通知。
- 若 A1+A2 真机验证后仍需节流，另案设计「事件幂等键」与「展示聚合键」分离的持久化调度器：事件键继续按 turn 唯一；聚合键按 `(backendId, sessionId)`；窗口内只发送最新 candidate，窗口结束后必须重新可发送。不得用内存 timer 制造崩溃丢通知窗口。
- 本计划以 A1+A2 为完整的折叠治理范围，不把尚未定义持久化、崩溃恢复和深链覆盖规则的 A3 混入实施批次。

### A4. 明确不做

- 不靠「减少类别」来降量（类别门是样本门，不是体验旋钮）。
- 不在 SW 里丢弃通知（Safari 会吊销权限）。
- 不复用 notification key 作为 Topic/tag 聚合键：前者负责事件幂等，后者负责展示替换。

---

## 5. 方案 B：应用图标角标

### B1. 唯一口径：本设备待查看会话数

本轮选择与当前可发送事件严格对齐的口径：

> `badge = authenticated device 自上次成功确认已查看以来，MacBridge 在该设备保持有效 enrollment 期间观察到 completion 的不同 (backendId, sessionId) 数量`

- 单位是**会话数**，不是回合数。同一会话连续完成十次仍计 1；这与 A2「通知中心每会话最多一条」一致。
- 状态按认证 `deviceId` 独立持久化；设备 A 打开 web app 不得清除设备 B 的角标。
- 首次启用、重新绑定到新 endpoint、或显式关闭后重新启用，从空集合开始，不补算功能启用前的历史完成。
- 不读取 reducer 全局投影，不声称“待审批/待回答”，因而不存在部分 hydrate、旧 checkpoint 或会话枚举不完整导致的假 0/假正数。
- `permission` / `input` 将来要进入角标，前提是对应事件样本门已经开放，并在动作**产生时**发送可见通知；不能只等 completion 时机会性附带。

明确拒绝客户端自增：Topic 可能在 Push Service 合并多条消息，SW 实际收到的 push 次数不是业务未读数；清缓存和多设备也会使本地自增漂移。

### B2. 服务端设备状态与并发

新增与 Web Push store 同安全等级的设备状态，例如：

```text
deviceId -> {
  bindingId: opaque random id,
  revision: uint64,
  unreadSessions: map<sessionAggregationKey, lastCompletionRevision>,
  updatedAtMillis
}
```

- 使用独立于 subscription 主文件的 badge-state 文件，置于同一 MacBridge dataDir，0600、原子写；badge 文件损坏只关闭角标表达，不能把健康的推送订阅判成 misconfigured。文件中只保存不可逆的 `sessionAggregationKey`，不保存标题、正文、sessionId、endpoint 或密钥。
- 新版客户端每次创建新的 browser subscription 时生成 `bindingId = "wpb_" + base64url(16 random bytes)`，作为 register params 的 additive 字段并保存到现有本地 binding 记录；同一 subscription 的 hello reconcile 复用它。服务端只接受该固定格式并且日志不得记录原值。仅凭 endpoint 相等不能判断是否同一启用周期，因为 disable 后浏览器可能再次给出相同 endpoint。
- 升级时若浏览器已有 subscription、但旧本地 binding 记录没有 bindingId，客户端生成一次 bindingId 并用现有 subscription 注册，从空 badge 状态开始；不能补算升级前历史。服务端发现同一 bindingId 对应的 subscription identity 改变时拒绝注册，客户端必须走现有完整 rebind 并生成新 bindingId。
- register 同一设备、同一 `bindingId` 是幂等重注册，不清空集合；不同 `bindingId` 开启新状态并清空旧集合；unregister / trusted-device revoke 删除对应状态。旧客户端没有 `bindingId` 时保持既有订阅能力，但服务端不为它启用 badge 状态，避免伪造 binding 生命周期。
- completion 对某订阅发送前，在该设备状态锁内先原子持久化：递增 revision，并把当前 session key 更新为新 revision；payload 的 badge 是持久化后 map 中不同 session key 的数量，同时携带 bindingId/revision。状态写失败则本次 payload 缺省全部 badge 字段，但通知仍发送。状态一旦持久化，即使本次网络投递失败也保留：该 completion 是设备 enrollment 期间真实发生的更新，后续成功 push 会携带收敛后的总数。
- 当前 dispatcher 有多个 worker，同一设备的状态分配必须串行，保证 revision 单调；状态持久化与 payload 快照完成后可以释放锁并继续网络投递。Push Service 不保证不同 Topic 的设备到达顺序，不能靠 HTTP 请求顺序维持角标单调性；乱序由 SW 的 revision 水位解决。
- 每设备最多保存 4096 个 unread session key。第 4097 个不同 key 到来时进入 `saturated` 状态并继续推进 revision，但停止下发 badge 数字；不得无界增长，也不得拿 999 冒充未知基数。页面仍可用当前 revision 执行 acknowledge：只有确认水位之后没有新 revision 时才能把 saturated 状态原子重置为空；若期间有新 completion，则继续 saturated、页面不动现有角标并再走下一轮 get→ack。
- payload 因 badge 按设备不同，不能再在 fan-out 循环外一次性构造完整 payload。标题、正文、target 可先构造共享模板，badge 元组必须从每个设备在状态锁内取得的持久化快照注入。
- 本轮不借机修复 §9 的共享 delivery ledger；badge 状态直接由真实 completion candidate + 当前有效设备 enrollment 推进，不读取会被其他订阅覆盖的 ledger 记录，也不把 Push Service 的 2xx 误称为设备已展示。

### B3. 载荷（增量字段，向后兼容）

- 在 `notification` 下新增一组全有或全无的可选字段：`badge`（整数，统一范围 **0..999**，发送端以 `min(999, 实际集合大小)` 封顶）、`badgeBindingId`（非空 opaque string）、`badgeRevision`（十进制字符串）。语义：**该 binding 在本次 completion 纳入持久化状态后的待查看会话数及其单调版本**；`badge=0` 表示清除。revision 使用字符串，避免 JavaScript 对大整数失真。
- 正常 completion 投递的持久化集合至少包含当前会话，因此 badge 通常为 1..999；0 主要由页面确认 RPC 返回并用于页面清除。协议仍允许 0，避免发送端与页面类型出现两套定义。
- `badgeRevision` 必须匹配 `^(0|[1-9][0-9]{0,19})$` 且能解析为 uint64；bindingId 使用 B2 的固定格式。服务端状态 saturated 时三个字段全部缺省。
- SW 只认 `schemaVersion === 1`；`parsePayload` 是**白名单返回**，旧 SW 会丢掉新增字段但通知照常弹出 ⇒ 旧客户端零影响，无需升 `schemaVersion`。新 SW 只有在三个字段同时存在且都合法时才返回 badge 元组；任一缺省/非法只剔除整组 badge 字段，不能把整条通知判为非法，也不能影响 `showNotification`。
- 需同步：`docs/protocol/bridge-v1.md` 的 Web Push 章节（canonical）、iOS 仓镜像、`docs/protocol/schema/` 与 `docs/protocol/samples/web-push/` 夹具；`web_push_protocol_contract_test.go` 的字段级断言一并更新。

### B4. SW 与页面调用点

1. **SW 的 `push` 处理器**（应用未打开也能更新）：先立即调用 `showNotification`，同时异步处理 badge 元组。SW 在现有 IndexedDB settings store 保存 `{ activeBindingId, lastAppliedRevision }`；仅当 payload binding 与 active binding 相同、且 revision 严格大于水位时，才在一个 IndexedDB readwrite transaction 中原子认领该 revision，随后调用 `self.navigator.setAppBadge(n)`。旧 binding 或旧/重复 revision 只跳过 badge，通知仍照常展示。角标 API 失败不回滚水位，避免更旧 payload 回退数字；下次页面前台查询会用服务端绝对值校正。处理合法的新 badge revision 后，SW 向已控制窗口发送 `CORDCODE_PUSH_BADGE_DIRTY_V1`；可见页面据此启动前台确认。
   - 必须特性检测（`'setAppBadge' in navigator`）并吞掉 rejection（`Notification.permission !== "granted"` 时会 reject `NotAllowedError`）；rejection 绝不能让 `waitUntil` 失败——通知必须照常弹出（userVisibleOnly 契约）。iOS 徽标独立开关**不会**产生 rejection（平台不可见，见 §2），无需也不能特判。
   - 始终传**数字**（不传参数的「圆点」在 iOS Safari 上不可靠）。
2. **页面与 SW 共享 binding**：成功 register/reconcile 后，页面把当前 `bindingId` 写入 IndexedDB，并保留既有 revision 水位；创建新 binding 时原子替换 activeBindingId 并把水位归零。disable 时删除 active binding/watermark。SW 不从 push payload 自行切换 binding，防止迟到的旧绑定消息复活角标。
3. **页面查询**：新增 additive RPC `get_push_badge_state`，`web_push.manage` scope，参数 `{ schemaVersion: 1, bindingId }`，结果 `{ schemaVersion: 1, bindingId, revision: "<decimal>", status: "available"|"saturated", unreadSessionCount?: 0..999 }`；count 只在 available 时存在。它只读取 authenticated device 当前 binding 自己的 badge 状态，不枚举会话、不返回 session identity；请求 binding 与服务端当前 binding 不符时返回稳定错误 `web_push.binding_mismatch`（`retryable:false`，客户端需先 reconcile，而不是原请求盲重试），不回退到设备旧状态。
4. **页面确认**：新增 `acknowledge_push_badge`，同 scope、参数 `{ schemaVersion: 1, bindingId, throughRevision: "<get 返回的 revision>" }`。available 时服务端只删除 `lastCompletionRevision <= throughRevision` 的条目；saturated 时只有当前 revision 未超过 throughRevision 才能整体清空，否则保持 saturated。RPC 返回 ack 完成后的当前绝对快照，形状同 get，可能包含查询之后到达的新 revision。available 时页面把共享水位推进到**返回的 revision**，再按返回 count set/clear；saturated 时不设置或清除角标。RPC 失败时不推进水位、不伪造已同步状态。
5. **触发时机**：首次 authenticated hello 完成且 enrollment 为 enabled、页面从 hidden 进入 visible、通知点击进入，以及可见页面收到 `CORDCODE_PUSH_BADGE_DIRTY_V1` 时，执行一次“get → acknowledge throughRevision”。不要直接挂在页面的 completion 终态上：candidate 入队和 badge 持久化是异步的，过早 get 会漏掉刚完成的 revision。并发触发必须 single-flight，结束后若期间又触发过则再跑一轮。saturated 且 ack 期间 revision 继续前进时最多立即重试 3 轮，仍不稳定就保持现有角标并等下一次前台触发，不能无限循环。
6. **关闭通知**：用户 disable 时先用已记录 subscriptionId best-effort unregister，随后浏览器 unsubscribe、清本地 binding/水位，并无条件调用本地 `clearAppBadge()`。服务端可达时 unregister 删除该设备 badge 状态；不可达时重新启用生成的新 bindingId 也不得继承旧集合。
7. **门控**：页面只在 enrollment 为 enabled 时查询/确认/设置角标。系统通知权限仍是 granted 但应用内已关闭通知时，设置页不得显示「角标已启用」，前台逻辑不得重新设置角标。

### B5. 失败与降级

- API 不存在 / 平台不支持 / `Notification.permission !== "granted"`（`NotAllowedError`）⇒ 静默跳过，界面可用性不受影响。可检测的只有「API 存在性 + 通知权限」两项；iOS 徽标独立开关**不可检测**（WebKit 隐私设计），诊断面/设置页**不得声称**能识别它——最多显示「角标 API 可用 + 通知权限已授予」并附静态提示（若图标不出数字，去 iOS 设置 → 通知 → 该 web app 检查「徽标」）。
- 不因为角标失败而重试推送、不补发。
- badge store 不可读/不可写：通知仍按原路径发送，但 payload 缺省 badge；不得用 0、内存集合或 reducer 快照代替持久化状态。状态写失败必须保留诊断，不能只改内存制造不可恢复的数字。
- `acknowledge_push_badge` 不可达：不改服务端集合、不清本地角标；下次 authenticated foreground 再尝试。该重试是用户前台确认重试，不是后台静默推送。

### B6. UI 提示

- 设置页的「通知」开关旁补一行角标状态，文案同时考虑 enrollment，而不是只看系统权限：① API 不存在 → 「当前环境不支持图标角标」；② enrollment disabled → 「开启通知后可显示角标」；③ enrollment enabled 但通知权限不为 granted → 「通知权限未授予，无法显示角标」；④ enrollment enabled + API 存在 + 权限 granted → 「角标可用」+ 静态提示「若图标不显示数字，请在 iOS 设置 → 通知 → 该 web app 中打开『徽标』」。
- **不得**写成「检测到徽标开关已关闭」或无条件写「角标已启用」：平台不暴露独立徽标开关，应用也必须尊重自己的 enrollment 状态。

---

## 6. 实现批次（建议顺序）

| 批次 | 内容 | 依赖 | 可独立验收 |
| --- | --- | --- | --- |
| P1 | 落实 A0 未打开 session 的投递覆盖；提取唯一 `sessionAggregationKey` helper；A1 Topic 改为按 `(backendId, sessionId)` 稳定 | 无 | 从未打开的 Claude/Codex session completion 均通知且不创建隐藏 projection；两个 backend 的同名 session 不碰撞；Topic ≤32 且字符合法；真实离线期间同会话只交付最新待发消息 |
| P2 | A2 tag 与 Topic 共用同一聚合键 | P1 | 真机：通知中心同会话只留最新一条，不同会话各留一条 |
| P3 | 按设备持久化 badge state、bindingId、revision 分配；payload 加 badge 元组；协议、schema、夹具同步 | P1 | 多设备独立；同会话多回合只计 1；MacBridge 重启后状态保留；badge 文件损坏不阻断通知 |
| P4 | `get_push_badge_state` / `acknowledge_push_badge` RPC；SW revision 水位；页面 get→ack single-flight；disable/重绑定生命周期 | P3 | 乱序 push 不回退数字；打开或点通知后只确认查询 revision 之前的更新；期间新 completion 不被误清；关闭通知后不再恢复角标 |
| P5 | 设置页状态与静态提示 | P4 | 四种 enrollment/API/permission 文案与真实可检测状态一致 |

每批次独立提交，**先写失败测试再改**（本仓惯例）。

---

## 7. 测试与验收

**自动化**

- producer/source 单测：replay-free Codex live completion 在零 observation、零 reducer state 时仍产生恰好一个候选；Codex per-thread attach 必须 excludeTurns、幂等并让 loaded thread completion 到达 observer，同一 turn 的 live text delta 必须形成 completion 预览；DSH/OpenCode replay-free lifecycle filter 必须放行一次 observed turn 并拒绝 reconnect replay；Grok updates journal watcher 必须跳过已完成历史、观察新建 journal、并保留启动时未完成 turn 的尾部；Claude watcher 首次启动及 enrollment 前历史只建立 cut、不补发，首次可见的启动前历史不得回放而启动后 live completion 保留，textless/text 双终态只产生一个真实预览候选，交互 relay 接管时不重复；各路径都不得创建隐藏 projection。
- SW 单测：badge 三字段全有才解析，部分缺省/格式错误/数字越界时整组剔除且通知照弹；`setAppBadge` 不存在或 reject 时通知仍弹；同 binding 的 rev2 先于 rev1 到达时最终保持 rev2；旧 binding payload 不设角标。
- Mac store 单测：设备 A/B 隔离；同会话重复只计 1；不同 backend 同 sessionId 计 2；4096 边界与 saturated 恢复；持久化重启；损坏/写失败时字段缺省；同 bindingId register 幂等、新 bindingId 清空旧状态、同 binding 换 subscription 拒绝、unregister/revoke 清理。
- dispatcher 单测：两个 worker 同设备并发取得不同且单调的 revision；不同设备独立；失败投递保留已观察更新；后续成功 payload 收敛；badge store 失败不阻断通知；Topic/tag 共用聚合键且 ≤32 字符。
- RPC 单测：只能读写 authenticated device 自身状态；binding mismatch 稳定报错；`get` 返回 available/saturated + revision；available ack 不删除 throughRevision 之后的更新；saturated 只在水位稳定时清空；重复 ack 幂等；`web_push.manage` scope；未认证拒绝。
- 契约测试：`web_push_protocol_contract_test.go` 对 `badge`、两个 RPC、唯一 0..999 边界做字段级断言；canonical Markdown、schema、fixture 与 iOS 镜像逐字段一致。现有仅做字符串包含的测试不足以证明镜像一致，本批次需补强。
- remote-web 单测：同 binding 的 rev2 先于 rev1 到达时最终保持 rev2；旧 binding payload 不设角标；SW 新 revision 向窗口发 dirty、旧 revision 不发；页面 completion 事件本身不抢跑 ack；ack 返回 revision 可压住在途旧 push；get→ack single-flight；ack 期间新触发会补跑；RPC 失败不清；返回非零时 set、0 时 clear；disable 无条件本地 clear 且不再前台校正；设置页四种文案状态。

**真机矩阵（owner）**

0. Web App 保持默认 backend 且不打开任何 session：分别在 Mac 端完成一个从未由 Web App 打开过的 Claude session 和 Codex session，两者都必须收到通知；随后打开其中一个 session 不得导致历史 completion 补发。
1. 已安装 web app、通知与徽标权限均开：两个不同会话完成后角标为 2；其中任一会话再次完成仍为 2。
2. 关掉 iOS 的「徽标」开关：通知照常弹、图标不出数字、web 端无任何报错（该开关对 web 不可见，属平台预期）。
3. 同一会话连续完成多个回合：通知中心只留最新一条，角标只计 1；不同会话各保留一条。
4. 手机真正断网 10 分钟后恢复：每个会话只收到 Push Service 尚未交付消息中的最新一条；测试记录不得把普通锁屏等同断网。
5. 点通知深链进入：落到正确会话；get→ack 成功后清除已观察 revision；确认期间另一会话完成时，新更新仍保留。
6. 两台设备同时订阅：打开设备 A 只清 A；设备 B 数字不变。
7. 应用内关闭通知后返回前台：图标角标保持清除，设置页不声称角标已启用；重新启用新 endpoint 不继承旧数字。
8. MacBridge 重启：未确认集合恢复；badge store 人为损坏时通知仍弹但不携带 badge，并暴露真实诊断。

---

## 8. 风险与硬约束

- **不能静默推送**（Safari 明文要求，否则吊销权限）⇒ 角标只能「随通知附带 + 前台校正」，不要设计成「后台准确实时」。
- `tag` 替换会丢同会话历史；本方案明确接受该取舍，以最新通知 + 每会话未读数字表达状态。
- 角标是提示值（可能被截断）⇒ 文档与 UI 文案不要承诺精确数字。
- 多设备：角标状态由 MacBridge 按认证 device 独立持久化，任何 get/ack/unregister 都只能作用于本设备。
- 取不到设备状态必须**既不设置，也不清除**；不得用客户端自增、局部 reducer 或 0 冒充真值。
- Push Service 2xx 只表示 accepted，不表示设备已经展示；角标集合表达 enrollment 期间观察到的 completion，不把传输结果包装成阅读回执。
- 同一设备的 revision 分配必须串行，但网络投递不承担顺序保证；SW 必须依靠 `(bindingId, revision)` 水位拒绝乱序旧值。

---

## 9. 相关但不在本计划内（建议另开）

1. **幽灵订阅不清理**：5 条订阅里 4 条所属设备记录已被删除，其中 3 条 Apple 端点自 09-03 / 09-08 / 09-09 起持续 404/410；`webPushExpirySemanticsProven = false` 使它们永不删除、每次通知白发一遍。→ 需要 WP-RESP-2 真实样本归档后翻转该常量（会顺带把投递账本洗一遍）。
2. **账本不可归因**：`LedgerRecord` 按 `notificationKeyHash` 记账，同一条通知 fan-out 到多订阅的结果被**最后一个覆盖** ⇒ 无法判断「哪个端点还活着」，本次排查中正是它掩盖了「手机收不到」。→ 建议改为按「通知 × 订阅」记账。
3. **原生 iOS App 的应用图标角标**：`NotificationService.swift:48` 已请求 `.badge` 但从未设置；它是本地通知路径，可用 `setBadgeCount` 在运行时设置，后台更新需另设计（与 PWA 两条路，不要混）。

---

## 10. 本版设计裁决

1. 本轮角标固定为「本设备待查看会话数」，不实现无法与 completion 触发时机对齐的「待审批 / 待回答数」。
2. 接受 A2「同会话只留最新一条」，数字按不同会话计数；这是解决通知中心折叠堆积的核心产品取舍。
3. A3 不纳入本轮；尤其禁止删除 notification key 中的 turnId。只有 A1+A2 真机证据仍不足时再另案。
4. 角标是通知 enrollment 的组成部分，不是独立功能：disable 必须清除，disabled 状态不得前台重设。

---

## 11. 锚点速查

| 面 | 文件 |
| --- | --- |
| 载荷 / 类别 / 错误码 | `go-bridge/web_push_protocol.go` |
| 投递状态机 / tag / Topic / fan-out | `go-bridge/web_push_dispatcher.go` |
| 通知键 / 类别门 / producer 位点 | `go-bridge/web_push_producer.go` |
| 订阅与账本存储 | `go-bridge/web_push_store.go` |
| 订阅注册 RPC 与校验 | `go-bridge/web_push_subscription.go` |
| Web Push RPC 路由 / scope | `go-bridge/handlers.go` `handleWebPushRPC`、`go-bridge/rpc_scopes.go` |
| SW（弹通知 / 深链 / 待接入角标） | `remote-web/public/sw.js` |
| 客户端订阅生命周期（reconcile/enable/disable） | `remote-web/src/core/push/push-manager.ts` |
| 客户端 binding / SW 共享状态 | `remote-web/src/core/push/push-binding-store.ts`、SW IndexedDB settings store |
| 设置页通知开关 | `remote-web/src/conversation/PushNotificationSettings.tsx` |
| 协议 canonical | `docs/protocol/bridge-v1.md`「Web Push (`web_push_v1`)」 |
| 协议夹具 | `docs/protocol/samples/web-push/`、`docs/protocol/schema/` |
