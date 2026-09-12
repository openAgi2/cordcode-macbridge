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
| 「待处理」真值 | 投影里已有：`RequiresPermissionConfirmation`（待批准）、`UserInputStatus`（pending…）+ `UserInputCanRespond`（待回答） | `go-bridge/projection_types.go:44,91,93` |
| 命名先例 | 原生 App 已有 `SessionRuntimeBadgeState.requiresAction`（会话列表/任务坞在用） | iOS 仓 `Services/SessionRuntimeStateStore.swift`、`Views/Session/SessionsView.swift` |
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
2. web app 图标显示角标，含义 = **当前有多少件事在等你**（默认取「待处理」口径，见 §4.3）。
3. 全程 fail-closed：取不到真值就**不下发角标字段**（本次不表达，沿用现状值）；页面侧取得真值后校正，真值为 0 才清除。任何路径都不伪造数字。

**非目标（本计划不做）**

- 不改通知类别门（`permission` / `input` / `error` 仍按样本门管控）。
- 不做静默推送（平台禁止）。
- 不动 VAPID、订阅存储与 404/410 清理策略（见 §9）。
- 不做原生 iOS App 的应用图标角标（另案，见 §9）。

---

## 4. 方案 A：通知折叠治理

折叠的根因是**条数**：每个回合一条、`tag`/`Topic` 都按通知唯一，几十条自然堆成一叠。三个抓手，建议**同时上 A1+A2**，A3 视 owner 取舍。

### A1. 稳定的 `Topic`（低风险，建议直接做）

- `Topic` 是 RFC 8030 的**离线合并**机制：同一 Topic 的**待发**通知只保留最新一条（已投递的不受影响）。
- 现值按通知唯一 ⇒ 完全不合并；改为**按会话稳定**（例如 `cc_s_<sessionId 摘要>`）后，手机离线/休眠期间的积压会收敛成一条。
- 约束：Topic 最长 32 字符且限 URL/filename-safe 字符集；超限/非法会被端点拒（`BadWebPushTopic`）。

### A2. 稳定的 `tag`（有取舍，需 owner 裁决）

- `tag` 决定 iOS 上同 tag 通知**互相替换**。改为按会话稳定后，同一会话只会看到最新一条，堆叠立刻消失。
- 取舍：**丢历史**（同会话早先的完成通知被替换）。若采用，建议与角标一起上——「条数」的信息交给角标承担。
- 若要保留历史，则只做 A1，并把 A3 作为主要抓手。

### A3. 发送侧合并 / 节流（可选，最治本）

- 形态：把通知键从「每回合一条」改为「**每会话一条（可被替换）**」——键里去掉 turnId，或在生产侧加一个「同会话 N 秒内不重复通知」的窗口。
- 影响面：`LedgerShouldSend` 的去重语义会从「每回合一次」变成「每会话一次」；深链目标（`target.eventId`/`anchor`）会指向最新回合。
- 建议：如果 A1+A2 落地后折叠已消失，A3 可延后；反之再上。

### A4. 明确不做

- 不靠「减少类别」来降量（类别门是样本门，不是体验旋钮）。
- 不在 SW 里丢弃通知（Safari 会吊销权限）。

---

## 5. 方案 B：应用图标角标

### B1. 数据来源（关键设计点）

三个候选，建议**分两阶段**：

| 方案 | 口径 | 是否需要读状态 | 评价 |
| --- | --- | --- | --- |
| **B1-a 待处理数**（阶段 1，推荐） | 待批准 + 待回答的会话数（投影真值已有） | **不需要新增持久化** | 天然有生命周期：批准/回答后自动归零；与原生 App 的 `requiresAction` 同义，语义一致。下发时需**实时读 kernel 投影**（新增 reducer 计数方法，见 B1 注） |
| B1-b 未读完成数 | 自上次打开以来完成的回合数 | 需要（按设备记 lastOpen，或客户端本地记） | 更贴直觉，但要防漂移（多设备、清缓存、重装） |
| B1-c 客户端自增 | SW 每收一条 push 就 +1，页面打开清零 | 不需要 | 零 Mac 改动，但数的是「通知条数」而非「未读事项」，遇合并即偏；不建议作为唯一口径 |

**注意一个硬约束**：后台改角标只能搭在「本来就要弹的那条通知」上（不能静默推送）。所以 B1-a 的角标**随 `completion` 通知一起下发**（在下发时计算当时的待处理数），而不是靠 `permission`/`input` 通知——后两类的通知门还没开。这也正好让「等你的审批/问题」在通知量不变的前提下被看见。

**B1 注（2026-09-12 评审补充，计数口径与真值边界）**：

- 计数规则（唯一口径，Mac kernel 为唯一 owner）：遍历 reducer 全部会话投影，满足任一即计 1——① 任一 part `RequiresPermissionConfirmation == true`（reducer 只在 pending 时保持 true）；② 任一 `user_input` part `status == "pending" && canRespond == true`。单位是**会话数**。
- 真值边界（诚实声明）：计数只覆盖 **kernel 已建立投影的会话**。冷重启后尚未 hydrate 的会话其待处理项不可见 ⇒ 计数可能**偏少**（绝不偏多、不伪造）；会话打开/被动泵 hydrate 后自然收敛。文档与 UI 文案不得承诺「全量精确」。
- reducer 目前只有按会话 `Snapshot`，无枚举 ⇒ 需新增一个锁内只读计数方法（不克隆投影，逐会话算布尔后汇总），dispatcher 经注入 reader 调用（沿用 `SetPreviewReader` 的注入先例）。

### B2. 载荷（增量字段，向后兼容）

- 在 `notification` 下新增可选 `badge`（整数，0..999，producer 端封顶）。语义：**下发时的待处理会话数（含 0；0 = 清除角标）**；字段缺省 = 本次不表达角标（真值不可得），**不是**清零。
- SW 只认 `schemaVersion === 1`；`parsePayload` 是**白名单返回**（显式挑字段重组对象），旧 SW 会丢掉 `badge` 但通知照常弹出 ⇒ **旧客户端零影响**，无需升 `schemaVersion`；新 SW 必须显式解析并校验 `badge`（可选、整数、0..9999 外的值视为非法字段剔除但不影响通知）。
- 需同步：`docs/protocol/bridge-v1.md` 的 Web Push 章节（canonical）、iOS 仓镜像、`docs/protocol/schema/` 与 `docs/protocol/samples/web-push/` 夹具；`web_push_protocol_contract_test.go` 的字段级断言一并更新。

### B3. 调用点

1. **SW 的 `push` 处理器**（应用未打开也能更新）：`badge` 字段存在 → `self.navigator.setAppBadge(n)`（`n === 0` 等效清除，与 WebKit 语义一致）；字段缺省 → **不动角标**（本次不表达）。
   - 必须特性检测（`'setAppBadge' in navigator`）并吞掉 rejection（`Notification.permission !== "granted"` 时会 reject `NotAllowedError`）；rejection 绝不能让 `waitUntil` 失败——通知必须照常弹出（userVisibleOnly 契约）。iOS 徽标独立开关**不会**产生 rejection（平台不可见，见 §2），无需也不能特判。
   - 始终传**数字**（不传参数的「圆点」在 iOS Safari 上不可靠）。
2. **页面**（应用打开/前台化/本人批准或回答后校正）：页面**不在本地重算**——remote-web 的会话列表行不携带待处理标记、页面只持有活跃会话投影，全局口径在客户端**没有数据源**（2026-09-12 评审发现的实现缺口）。改为：新增 additive RPC `get_push_badge_count`（`web_push.manage` scope，结果 `{ schemaVersion: 1, pendingActionCount: <int> }`，与下发侧共用同一 kernel 计数方法），页面调用后 `setAppBadge(n)`；`n === 0` 时 `clearAppBadge()`；RPC 失败/不可用 = 真值不可得 → 不动角标。禁止客户端自增或从通知条数推算。
3. **清除规则**：点击通知深链进来**不要盲目清零**——页面经 RPC 取真值再设；只有真值为 0 才清。

### B4. 失败与降级

- API 不存在 / 平台不支持 / `Notification.permission !== "granted"`（`NotAllowedError`）⇒ 静默跳过，界面可用性不受影响。可检测的只有「API 存在性 + 通知权限」两项；iOS 徽标独立开关**不可检测**（WebKit 隐私设计），诊断面/设置页**不得声称**能识别它——最多显示「角标 API 可用 + 通知权限已授予」并附静态提示（若图标不出数字，去 iOS 设置 → 通知 → 该 web app 检查「徽标」）。
- 不因为角标失败而重试推送、不补发。

### B5. UI 提示

- 设置页的「通知」开关旁补一行角标状态，文案严格限定在可检测事实内：① API 不存在/平台不支持 → 「当前环境不支持图标角标」；② 通知权限未授予 → 「开启通知后可显示角标」；③ 两者皆备 → 「角标已启用」+ 一行静态提示「若图标不显示数字，请在 iOS 设置 → 通知 → 该 web app 中打开『徽标』」。**不得**写成「检测到徽标开关已关闭」（平台不可检测，见 §2/B4）。

---

## 6. 实现批次（建议顺序）

| 批次 | 内容 | 依赖 | 可独立验收 |
| --- | --- | --- | --- |
| P1 | A1 稳定 `Topic`（按会话） | 无 | 真机：离线一段时间后只收到最新一条 |
| P2 | B1-a + B2 + B3 + B4：reducer 计数方法、载荷加 `badge`、`get_push_badge_count` RPC、SW 设角标、页面经 RPC 校正、协议与夹具同步 | P1 无强依赖，可并行 | 真机：角标随通知出现；打开 app 后按真值收敛/清零 |
| P3 | A2 稳定 `tag`（**需 owner 裁决**） | P1 | 真机：同会话只留一条 |
| P4 | A3 发送侧合并/节流（视 P1–P3 效果决定） | P1/P3 | 真机：连续多回合不再刷屏 |
| P5 | B5 设置页角标状态与提示 | P2 | 目视 |

每批次独立提交，**先写失败测试再改**（本仓惯例）。

---

## 7. 测试与验收

**自动化**

- SW 单测：`badge` 存在/缺省/0/非整数或越界（剔除字段、通知照弹）；`setAppBadge` 不存在时静默跳过；`setAppBadge` reject 时**通知仍要弹**（`waitUntil` 不失败）。
- Mac 单测：计数方法（pending 权限 / pending 可回答 user_input / 已解决归零 / 跨会话计数 / reducer 未接线 = 真值不可得）；`badge` 只在真值可得时下发（含真值 0）；真值不可得时字段缺省（不写 0、不猜）；`get_push_badge_count` RPC 形状与 scope；`Topic` 按会话稳定、同会话多次完成同 Topic、≤32 字符、字符集合法；`tag` 语义变更的回归（去重/替换，P3 时）。
- 契约测试：`web_push_protocol_contract_test.go` 字段级断言 + canonical Markdown + iOS 镜像一致性（本仓既有机制）。
- remote-web 单测：页面校正在 RPC 成功时 set/clear、RPC 失败时不动角标；设置页三种文案状态（不支持 / 未授权 / 已启用+静态提示）。

**真机矩阵（owner）**

1. 已安装 web app、通知与徽标权限均开：收到完成通知时角标出现，数字与「待处理数」一致。
2. 关掉 iOS 的「徽标」开关：通知照常弹、图标不出数字、web 端无任何报错（该开关对 web 不可见，属平台预期）。
3. 连续完成多个回合：只看到每会话一条（A2 生效时），角标反映累计待处理数。
4. 手机离线 10 分钟后恢复：只收到最新一条（A1 生效时）。
5. 点通知深链进入：落到正确会话；角标按真值收敛，未读清零逻辑不误清。

---

## 8. 风险与硬约束

- **不能静默推送**（Safari 明文要求，否则吊销权限）⇒ 角标只能「随通知附带 + 前台校正」，不要设计成「后台准确实时」。
- `tag` 替换会丢历史（A2）⇒ 需 owner 明确取舍。
- 角标是提示值（可能被截断）⇒ 文档与 UI 文案不要承诺精确数字。
- 多设备：角标是**每设备**的，口径必须能在设备本地算出来（B1-a 满足；B1-b 需要读状态）。
- 取不到真值必须「不设/清除」，**不得用本地自增冒充真值**。

---

## 9. 相关但不在本计划内（建议另开）

1. **幽灵订阅不清理**：5 条订阅里 4 条所属设备记录已被删除，其中 3 条 Apple 端点自 09-03 / 09-08 / 09-09 起持续 404/410；`webPushExpirySemanticsProven = false` 使它们永不删除、每次通知白发一遍。→ 需要 WP-RESP-2 真实样本归档后翻转该常量（会顺带把投递账本洗一遍）。
2. **账本不可归因**：`LedgerRecord` 按 `notificationKeyHash` 记账，同一条通知 fan-out 到多订阅的结果被**最后一个覆盖** ⇒ 无法判断「哪个端点还活着」，本次排查中正是它掩盖了「手机收不到」。→ 建议改为按「通知 × 订阅」记账。
3. **原生 iOS App 的应用图标角标**：`NotificationService.swift:48` 已请求 `.badge` 但从未设置；它是本地通知路径，可用 `setBadgeCount` 在运行时设置，后台更新需另设计（与 PWA 两条路，不要混）。

---

## 10. 待 owner 裁决

1. 角标口径：B1-a「待处理数」（推荐，阶段 1）是否够用，是否要 B1-b「未读完成数」（需读状态）。
2. 是否接受 A2「同会话只留一条」（丢历史）以换取彻底不折叠。
3. 是否现在就把 A3（发送侧每会话合并）纳入范围。

---

## 11. 锚点速查

| 面 | 文件 |
| --- | --- |
| 载荷 / 类别 / 错误码 | `go-bridge/web_push_protocol.go` |
| 投递状态机 / tag / Topic / fan-out | `go-bridge/web_push_dispatcher.go` |
| 通知键 / 类别门 / producer 位点 | `go-bridge/web_push_producer.go` |
| 订阅与账本存储 | `go-bridge/web_push_store.go` |
| 订阅注册 RPC 与校验 | `go-bridge/web_push_subscription.go` |
| SW（弹通知 / 深链 / 待接入角标） | `remote-web/public/sw.js` |
| 客户端订阅生命周期（reconcile/enable/disable） | `remote-web/src/core/push/push-manager.ts` |
| 设置页通知开关 | `remote-web/src/conversation/PushNotificationSettings.tsx` |
| 「待处理」真值 | `go-bridge/projection_types.go:44,91,93` |
| 命名先例（原生 requiresAction） | iOS 仓 `Services/SessionRuntimeStateStore.swift` |
| 协议 canonical | `docs/protocol/bridge-v1.md`「Web Push (`web_push_v1`)」 |
| 协议夹具 | `docs/protocol/samples/web-push/`、`docs/protocol/schema/` |
