# Claude 长 session 打开方案终审 R14

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，**v18**。
- 全文 SHA-256：`a2f1b507bba6ef6e5451b250cdb5634bd2388af96f2fde50be4e7b90e8304b52`。
- 正文 SHA-256（按方案交接块的切分规则）：`d7b021c6c6c054715e2d81f1e2cfd1d46d984ba923928c19d04329c2878d3f08`，与自报值一致。
- scope: **全文终审**，重点亲核首开/恢复/增量/fence/跨 epoch/物化 IO 的组合行为；先建立本轮风险清单，再读 R13 作回归核对。没有另派评审代理。
- blockers: **5**（F-41～F-45，均为 P1：会改变实现或验收结果）；advisories: **0**。
- implementation_readiness: **S-4/S-7 的恢复增量设计、S-2 的 IO 保证尚不能定稿**。S-0 可按既有授权继续只读取证。S-5/S-8 的方向和 opcode 定案本轮未发现新的阻塞，但不能据此把整份方案标为通过或将本次评审当作实施授权。

R13 的 F-39/F-40 已闭合；本轮阻塞不来自这两处修订。F-41 是 v14 方向变更后仍未清完的规范冲突；F-42/F-43/F-44 是 v10 恢复增量设计的既有漏检；F-45 是既有增量物化方案的性能论证缺口。R13「全案自洽、设计层面无未决项」不足以支持 v18 的最终通过，相关依据应暂停采用；不回写旧报告，也不推翻不受影响的源码核验。

## 来源身份与核验边界

### G1：源码分析前；G2：首次写报告前

配对依据为被评方案 §2.1 明确指定的两个 native-message-timeline 工作树，并已分别枚举 Git worktree。没有将默认 `main` 路径当作配套工作树。

| 别名 | 仓库绝对路径 | 精确分支 | 完整提交 | 任务预期来源 / 配套关系 / 预期特性 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | 方案 §2.1 指定；配套 I 的下述路径、分支及提交；审查 Claude SSV2、拟议 full+gzip 首开、anchor 增量与物化存储，不部署 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `e52daa275946ee0d41749aa7146ec8bbc8b05c29` | 方案 §2.1 指定；配套 M 的上述路径、分支及提交；审查同一产品特性以及唯一 replica/presentation writer |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897` | 方案 §2.1 指定的只读架构参考；关联 M/I 方案；不代表已安装 Codex 或 Claude 的运行版本，不构建、不部署 |

M 的 G1/G2 未提交状态一致，逐项如下（相对 M）：

```text
 M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

I 的 G1 未提交状态为：

```text
 M OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift
```

G2 发现同一工作树有并发开发，未提交状态变为：

```text
 M CHANGELOG.md
 M OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift
 M OpenCodeiOS/OpenCodeiOSTests/NativeTimelineSmoothHeightTests.swift
```

G2 的脏 controller SHA-256 为 `8d4c36aef76dffa14c3219704dcdddc0cef0a2ed62c5b1ae4ff3b562da51f4c9`。本报告不依赖这三个并发编辑文件作源码结论，也不触碰它们。I 的关键核验文件 `ProjectionReplica.swift`、`ProjectionStore.swift`、`SessionProjectionWindow.swift`、`BridgeWebSocketConnection.swift` 相对方案登记的 `dfb77ed93c610165730a88f8483c80e78941d321` 至本轮 HEAD 的路径限定 diff 为空，且不在脏清单内。U 在两道门均干净，三仓分支/提交均未改变。

方案 §2.1 是历史快照，不能替代上述本轮来源。正式 runtime 的 PID 8303 与安装包身份**未重新核验**，本轮不作运行态或端到端成功结论；也没有构建/安装/部署，因此不触发 G3。

### 本轮亲核锚点

以下为代表性高风险命题及实际源码摘录。源码位置均归属上表身份；方案无行号的拟议声明也保持原引用方式，不把实际行号冒充原引用。

| 方案命题 / 原引用 | 实际源码位置与最小摘录 | 定位 / 语义结果 |
| --- | --- | --- |
| §6 S-8：`BridgeWebSocketConnection.swift:326/:334`；另 `:368/:376` 属发送侧 | I `Services/Bridge/BridgeWebSocketConnection.swift:326`：`NSLog("[NetworkWebSocket] 收到完整消息 frameType=%d bytes=%d", opcode.rawValue, accumulated.count)`；`:334` 为分片完成日志 | F-39 更正命中；读取侧仍在 `:327/:335` 仅返回 `accumulated`，API 丢 opcode 的主张成立 |
| §3.6：client→Mac binary，`:304-306` / M `server.go:670` | I 同文件 `:305`：`try await sendFrame(opcode: .binary, payload: data)`；M `go-bridge/server.go:670`：`_, raw, err := ws.ReadMessage()` | 原定位与方向语义成立；不要求修改上行 |
| §3.6：direct 现状直写 JSON | M `go-bridge/server.go:115-124`：`func (c *Conn) SendJSONReport(v interface{}) error`，持 `c.mu` 后 `c.conn.WriteJSON(v)` | 定位/语义成立；新增 gzip 是未来设计，不是现状 |
| §3.6：relay gzip 纪律，`relay_conn.go:296/:304/:313` | M `go-bridge/relay_conn.go:297`：`outboundGzip && len(plaintext) >= relayGzipThreshold`；`:303-304`：`if len(compressed) < len(plaintext) { contentEncoding = "gzip" }`；`:313` 将 encoding 入队 | 语义成立；iOS 解压可复用 I `RelayBridgeFrameConnection.swift:821-873`，`:859-862` 已有解压上限错误 |
| §3.8 复用 `projection_window_handler.go:201-211` 注册 held turns | M 该文件 `:206-210`：收集 `response.Turns` 的 `TurnID` 后调用 `CompleteProjectionSnapshotWithHeldTurns(...)` | 定位/事务内注册语义成立；不证明缓存里但不在响应页里的旧 turns 已对账 |
| §3.4.2.5：更早 turn 的滞后突变由打开后 live 流自愈（引 §3.7 fence） | M `go-bridge/projection_snapshot_fence.go:120-124`：`cutRev: projection.SyncRev, expectedRev: projection.SyncRev`；`:269-275` 只发送 `fence.pending` | fence 事实成立，**不能支持离线期间旧修改会被重放**（F-42） |
| §3.8：复用 `growWindowRange(sizes, limit, boundary+1, false)`，原引 `projection_window.go:278-296` | M `go-bridge/projection_window.go:290` 确为该调用；`:195-214` 从 `boundaryIndex` 开始累积并返回；传 `boundary+1` 排除 anchor | 原定位成立，**语义只支持严格排他页，不能支持包含锚点**（F-43） |
| §3.4.3：`ProjectionReplica.swift:193-212/:208/:323` | I `Models/ProjectionReplica.swift:207-208`：`state.projection = projection; state.appliedRev = projection.syncRev`；`:323`：`guard page.syncRev >= state.appliedRev else { return .windowPageUnchainable }` | 原定位成立；只改 appliedRev 不会改 projection 的 revision/epoch（F-44） |
| §3.4.3：复用 applyWindowPage 自动校正 appliedRev | I `Models/SessionProjectionWindow.swift:123-126`：空页 `return self`；`:173`：`copy.syncRev = max(syncRev, page.syncRev)`；I `ProjectionReplica.swift:350-354`：投影采用 merged，appliedRev 采用 page 值 | appliedRev 校正成立，**不等于整体采用新 epoch 的一致状态**（F-44） |
| §3.4.3：`ProjectionStore.swift:1044-1056/:620-626`；`ChatViewModel.swift:1080` | I 前两处均在 replica `.applied` 后 `commitIfObserved(... lifecycle: .ready ...)`；I `ProjectionStore.swift:1197-1204` 保存投影/rev/lifecycle 并通知；`ChatViewModel.swift:1086-1089` guard 活跃 session/backend/ownership | 提交与通知的复用成立；`.ready` 并不是该 render guard 的直接判断条件，不能把“缺通知”和“该 guard 判断 lifecycle”混为一谈 |
| §3.2.3：M `projection_kernel.go:266-278/:389-415` 全 prefix 哈希 | M `go-bridge/projection_kernel.go:407`：`digestProjectionSourcePrefix(f, checkpoint.Cursor)`；`:411-412` 比较 prefix digest，不一致返回 invalid | 原定位/现状语义成立；拟议 stat 快速匹配能否处理 append 另见 F-45 |
| §2.3/§3.2.3：U suffix 参考 `thread_history_materialization.rs:42-45` | U `codex-rs/thread-store/src/local/thread_history_materialization.rs:42-45`：读取 projection state，`map_or(0, |state| state.next_byte_offset)`；`:64-70` 将 offset/ordinal 交给 `read_projection_steps` | 定位/参考语义成立；不能据此证明 Claude 的 prefix 完整性或 CordCode 的 IO 上界 |
| §2.3：U 行级物化 `0001_thread_history.sql:1` 与无云端 replay `cloud-client/README.md:18` | U SQL `:1` `CREATE TABLE thread_turns`，`:18` `CREATE TABLE thread_items`，`:34-37` 保存 offset/ordinal；README `:18`：`Attach has no history snapshot or replay guarantee` | 参考成立；没有把该参考当成 CordCode 运行证据 |

I 表中缩写文件路径分别以 `OpenCodeiOS/OpenCodeiOS/` 为根。

### 样本与专项

只读重读方案 §1.1 的原 checkpoint，未新建真实账号会话、未重新捕获 wire：

```text
文件：/Users/jacklee/Library/Application Support/CordCode Link/session-projection/checkpoints/683c24fad8a17ef8c833319a3e2e68af7064d81bf4d819c4b1c971e52e202c3f.json
原捕获来源：方案登记的 2026-10-17 事故 checkpoint；本轮复核日期：2026-10-18
bytes=34537988
sha256=9d55c187859c37cf9036d0c6f6c9b689998a6312351ac9b02c0bb1f5548a2c9b
projection keys=[bridgeEpoch, execution, sessionId, syncRev, turns, updatedAt]
turn records=119；独立 turnId 集合=119
Python ensure_ascii=False + 默认分隔符：projection=34427361 B
最大 turn=6347762 B，index=1
尾 17 turns 的数组=3801579 B
gzip level1=11142266 B；level6=9602913 B
```

尾部数组按两种方法复核：直接序列化数组，与 `2 + 各 turn 字节之和 + 2*(17-1)`，均为 3,801,579 B。仅 turn blob 的和为 3,801,545 B；34 B 差额来自数组括号/分隔符，不是记录差异。结果与方案量级一致，本轮不把 Python 口径冒充 Go `json.Marshal` 的生产计量。

按 audit-plan 接入约定区分：本轮检查的新 schema 是 **CordCode 自有拟议协议**，不是新 Claude JSONL 字段解析表；现有 decode/fence 事实以源码核验。S-7/S-8 尚无实现和真实新 wire，不能宣称外部格式专项或端到端已通过。它们应按冻结前置和实施完成门采证，不要求未实现路径现在先返回成功。F-42/F-43 是内部恢复协议的反例，与是否有未来 wire capture 无关。没有对 Claude 全部 transcript record 类型做专项枚举，也没有把本轮 checkpoint 重读写成新捕获。

## 阻塞意见

### F-41 [P1 / 阻塞] v14 定案与现行硬约束、回退及验收仍相互矛盾

**位置**：方案 `:256-257`（R-3/R-4）、`:363`（不变量 3）、`:689/:693`（anchor 恢复/eligibility）、`:738`（Gate B）、`:820/:847/:852/:870/:872`（验收）、`:899`（anchor 回退边界）；对照 `:254`、`:348`、`:549/:553`、`:748/:750` 和 §9 第一阶段。

**证据与后果**：

- R-1/数据流/第一阶段要求首开 full+gzip；但不变量 3 仍写「**Claude 首开不得返回整棵 projection**」，§7.3 又要求 `full projection open = 0 次（new peer / window available）`。正确执行 v14 首开也必然违反这些硬验收。
- 状态机写 typed stale → FallbackFull，S-7 行写未 echo → full；§3.8、§7.2.13、§7.4.12/14 和 anchor 回退边界却仍要求 typed stale、超两页或未 echo → **window_0（不回 full）**。这不是 §0 的历史记录，而是仍在使用的实施/验收条款。
- 同时 §3.7 明确 v1 响应 scalarState 再冻结仅为 contingency，默认交付只新增 anchor 响应。若实施者按上述回退条款走 window_0，就进入没有默认补齐 scalar 的路径，恢复出 idle 执行态或与双维 eligibility 互相堵死；若按 S-7 行回 full，则无法通过这些验收。
- Gate B 仍要求 S-3 冻结前先满足已被取代的 OD-2，而 S-3 切片行已去除此依赖。

**最小修订**：按已经定案的 v14 路线整理一张当前路径/触发条件/能力门/返回形状/失败行为表，统一活跃正文与验收。首开、typed stale、超限和未 echo 明确走何处；window_0 contingency 若保留，必须明确启用前置。移除已失效的硬约束与决定依赖；历史章节可以保留。

**闭合标准**：同一触发条件只有一个默认动作；full 首开不会被验收判失败；默认回退不依赖未交付的 v1 scalar 再冻结；OD-2 不再阻塞无关切片。F-32/F-37 的“全局引用已彻底收口”仅在这些条款修正后才能恢复。

### F-42 [P1 / 阻塞] 尾部 anchor 只能定位，不能证明恢复基底与权威快照一致

**位置**：§3.4.2 关键点 4/5（`:566-567`）、§3.8、OD-1/OD-3 的安全性论证、E-11，以及 §8 的“更早突变由 patch 自愈”。

**证据**：方案声称“更早 turn 的滞后突变由打开后的常规 live patch 流自愈”。但 M 的 window 路径调用 `BeginProjectionSnapshot`（`projection_window_handler.go:168`），不是选择旧 revision 区间的 `BeginProjectionSnapshotWithResume`；fence 从本次 `projection.SyncRev` 开始，只保证 **post-cut** pending patch 的顺序。I 的 `SessionProjectionWindow.swift:163-174` 只替换页内 turn 并保留其余基底。裸 anchor 请求没有缓存 revision/coverage 指纹/有效性 token，也没有其他方式要求返回离线修改。

**反例（内部协议推导，不冒充真实运行）**：缓存 `[A旧, B旧, C]` 后设备离线；Mac 将 A 改为 A新，C 的 ID 保留，之后没有任何 live 变化。重开只得到 `[C, D]` 及当前 scalar，然后把 appliedRev 推到 head。A新对应修改发生在 cut 之前，不会再次广播；稳态 sinceRev 已到 head，也取不到它。结果 `[A旧, B旧, C, D]` 却已清除 syncing，违反 R-7/E-11。epoch 不变同样会发生；跨 epoch 和保留 anchor 的重建也不能靠“anchor 不存在”识别。

**最小修订**：给缓存基底增加由 Mac 可验证的有效性/对账契约，或明确证明并约束所有保留前缀的不可变性。仍可保持 Mac 不存 per-device 进度；请求携带验证依据与 per-device 状态存储是两件事。验证失败使用已定案的诚实恢复路径，不能把“后续可能收到 patch”当完成证明。

**闭合标准**：E-11 增加“缓存范围内非 anchor turn 在离线期间改变、anchor 保留、重开后无后续事件”的反例，验证内容与权威重叠范围一致，或保持显式未对账并进入规定恢复；同 epoch/跨 epoch 均覆盖。E-11 不得只比较 turn ID 集合，须比较受影响内容/终态。不能在核心有效性尚无方案时以“冻结阶段再定”宣布恢复设计已完整通过。

### F-43 [P1 / 阻塞] anchor 页同时承诺刷新锚点与零新增空页，无法保证末轮终态及翻页前进

**位置**：§3.8 `:686-690`，§7.1.5、§7.2.13、§7.4.10。

**证据与后果**：

1. `:687` 要求包含 anchor，以刷新缓存保存时仍在 streaming 的末 turn；紧接 `:688` 要求 anchor==tail 时返回空 turns。最常见的“离开时尾 turn running，离线期间同一 turn 完成，没有新增 turn”恰好满足 anchor==tail：空页无法更新末 turn 的正文/终态，却会推进 rev、清除 syncing。
2. `:690` 指定复用 `growWindowRange(... boundary+1, false)`，其实际语义从 anchor 的下一个 turn 开始，严格排除 anchor，与包含语义相反。
3. 如果把 helper 改为从 anchor 开始但每页都重复 anchor，那么 `limit=1`，或 anchor 本身占满/超过 4MiB 时，页只能返回 anchor。hasNewer 仍为 true，下一次相同 anchor 请求仍返回它，无法前进。§7.1 已承认真实样本存在单 turn >4MiB，不能将这个边界排除为不可能。

**最小修订**：冻结一套可实现的“刷新 anchor + 严格后继分页”语义。可将刷新与续页规则分开，具体形式由作者选择；必须说明 anchor==tail 的非空修改如何返回、刷新计不计 limit/字节预算、第二页如何严格前进、超大 anchor 如何结束或进入明确恢复。单 turn 超限规则与“2 页≈8MiB”的计量也要一致。

**闭合标准**：协议/fixture/两端验收一致覆盖：无新增但末 turn 改变、无新增且完全没变、一条新增、limit=1、超大 anchor 后仍有新 turn、第二页前进。不能用“合法空页”测试掩盖需要刷新的末 turn；新路径实现前把互斥条款收为唯一契约。

### F-44 [P1 / 阻塞] 跨 epoch 恢复只重置 appliedRev，会留下投影、存储和 presentation 的混合 revision 身份

**位置**：§3.4.3 第 2 条（`:573`）及 §7.2.12。

**证据**：方案要求复用 `applyFull(forceReplace:)`，随后“显式压回 appliedRev 到 0”，并认为新页会自动校正。I 的 replica 确实将 appliedRev 改为 `page.syncRev`，但被复用的 `SessionProjection.applyingWindowPage` 对非空页取 `max(旧 projection.syncRev, page.syncRev)`（`:173`），空页直接 `return self`（`:123-126`），也没有换 `bridgeEpoch`。I 的 `SessionProjection.revisionKey`（`:696`）真实使用 `epoch|syncRev`，presentation 的 full apply 同样读取 `projection.syncRev`（`ProjectionPresentationEngine.swift:378/:390`）。这不是一个可以仅在私有计数器中修正的值。

**反例（按被指定复用代码推导）**：恢复 epoch A/rev 500；appliedRev 重置为 0；epoch B/rev 5 的页成功后，replica/store appliedRev 可为 5，但 projection 仍是 A/500（非空页 max，空页原样）。若没有下一条 patch，此不一致不会自行消失。后续正常 full 的 stale 比较又读旧 `projection.syncRev`（`ProjectionReplica.swift:199`），较小的权威投影可能被拒绝；缓存与 presentation 也会传播旧身份。

**最小修订**：将恢复定义成显式、原子的新 epoch 采用事务，区分“显示缓存的 provenance”与“已对账的当前 projection epoch/rev”。列清 replica、store、projection、coverage、presentation/cache metadata 的更新关系；对恢复中的 push/recovery 入口规定隔离时序，不能仅保证稳态 pull loop 不抢跑。

**闭合标准**：旧 epoch 大 rev → 新 epoch 小 rev，在非空页及零新增路径后，检查 projection、replica/store appliedRev、revisionKey、coverage、持久化 metadata 一致且可接下一条 patch/权威 full；加入恢复过程插入 patch 的定向时序反例，不能只断言 appliedRev 数值。

### F-45 [P1 / 阻塞] `(path,size,mtime)` 快速匹配不能支撑“正常 append 只读 suffix”的 IO 承诺

**位置**：§3.2.3 `:452-454`；R-5、§6 S-2、§7.1.2/§7.3 source advance。

**证据**：拟议策略写“按 `(path,size,mtime)` 快速匹配；匹配失败才退回全量 prefix digest”。正常 append 本来就会改变 size/mtime，尤其“持久化提交 → runtime 停止 → Claude 继续 append → runtime 重启”这一恢复场景；按该规则不命中便要读完旧 prefix。下两行又承诺 append catch-up 的 IO 只有 stat/尾部 4KiB + suffix、不整读约 97MiB，二者不能同时成立。M 的现有 digest 确实会全读 prefix；U 的 offset/ordinal 机制是不同数据源的不变量，不能替 Claude 证明 prefix 未改。

**最小修订**：把 unchanged、可信 append、疑似 rewrite/truncate/replace 的来源判定和 IO 成本分别定义清楚。若保留“只处理 suffix”硬目标，给出 append 在重启后也能通过的可证明验证机制及不变量；若决定此类恢复仍需整读 prefix，明确缩小性能承诺并重写验收/预算，不能继续报为零整读。

**闭合标准**：S-2 测试计划增加“保存 store → 停止 → 相同 source append → 重启恢复”，测实际读取字节区间，不仅测 reducer 处理事件数；与“prefix 被改但长度不退回”的反例区分。最终 IO 断言与所选验证机制一致。

## 跨轮闭合、剩余门与交付限制

| 项 | 本轮处置 |
| --- | --- |
| F-39 日志行号 | **闭合**；`:326/:334` 和发送侧澄清与源码一致 |
| F-40 计数关系 | **闭合**；v18 修订表、历史行及交接块均说明 14+3=17。本轮不重新证明原作者编辑动作确实有 17 次，只核验口径一致 |
| F-31/F-36/F-38 opcode/方向/API 改造 | 相关机制本轮亲核成立；不把尚未实施的新 wire 当真实证据 |
| F-29 两步提交 | replica→store→presentation/通知的复用成立；跨 epoch 整体身份另立 F-44，不能由旧核验推导闭合 |
| F-32/F-37 全局清扫结论 | **不维持“彻底收口”**；F-41 列出仍在使用的反例，属于对旧通过结论的纠错 |
| F-33/F-34 | 独立 anchor fixture 和事务内 held 注册方向保留；本轮亲核完成调用及其边界，没有把 held 注册当缓存前缀有效性证明 |
| 其它历轮意见 | 不做“全部重新验证”的笼统背书；本轮全文审查及高风险核验范围如上 |

未将正常实施后验收误列为本轮阻塞：E-4 新 wire、E-5 阶段指标、E-6 存储测试、E-7 writer seal、E-10 scalar、E-11 对账和 owner 真机矩阵仍应在对应切片完成。E-3 仍是 S-7 实施前证据门；S-7/S-8 冻结批次仍是前置；S-3 summary/detail 冻结仍需单独满足。此次新增意见要求先修正核心契约，不能以这些未来完成门替代设计答案。

本轮只新增此报告；不修改方案、不改业务代码、不提交既有脏文件、不运行 build/unit/UI/snapshot/模拟器自动化、不安装、不重启。完成静态源码核验、只读样本复算和文档 `git diff --check`；不提供运行成功或真实性层级之外的性能承诺。

交付前复核：方案全文哈希仍为上文 `a2f1b507…`，M/I 分支及完整提交未变；I 的未提交状态仍为 G2 三项，M 仅比 G2 新增本报告。报告的 `git diff --no-index --check /dev/null <report>` 通过。
