# 评审报告 R12：Claude 长 session 打开架构优化方案（v16 第十二轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R11 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v16**
方案 SHA-256（全文，开工实测）：`171fe27df8bf17f327f00844b4c67a3a12ada700db4167831aabb15439c51c43`——与派发记录一致
方案 SHA-256（收工复测）：`171fe27df8bf17f327f00844b4c67a3a12ada700db4167831aabb15439c51c43`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`6ae8fba8b8d970f34c183b5ab256b3b39cdbb75762d905094d1cce9348bc211a`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=93326）**精确命中**，记账自洽
范围：full（全量第十二轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **0**
- advisories: **3**（F-36、F-37、F-38）
- **R11 五条意见闭合判定：F-33/F-34/F-35 三条完整闭合；F-31 机制定案正确但遗漏一项现状前提；F-32 清扫未彻底（3 处漏改）。**
- **未判 APPROVED 的原因**：本轮自选新核验面发现 **F-36（阻塞级风险，但定级为建议）**——v16 定案的 opcode 自描述机制建立在一个**与现状相反的前提**上：「client→Mac 恒为 text」，而实测 iOS 直连**当前就是用 binary 帧发送**（`BridgeWebSocketConnection.swift:304-306`）。这不会让 S-8 失效（Mac 读循环不区分 opcode），但会让「未协商连接零 binary 帧」这条冻结断言**在现状下就不成立**——实施者若照字面写断言会立刻失败。
- **R11 的 F-31 定案方向正确**：WebSocket opcode 是协议原生语义、非启发式、旧客户端零影响，三条替代的否决理由（信封字段鸡生蛋、magic 嗅探违反「不猜」、permessage-deflate 已否决）**全部成立且论证扎实**。这是本方案历轮中处理得最好的一次阻塞项修订。

**本轮自选新核验面（按指令要求声明，共三个）**：

1. **opcode 定案与 iOS 现状的相容性**（产出 F-36）——实测 iOS 直连走**自研 WebSocket 实现** `BridgeWebSocketConnection`（不是 URLSessionWebSocketTask），其 `send()` 固定用 `.binary`（`:304-306`），而 `receiveData()` 虽然解析了 opcode（`:311-330`）但**在 API 边界丢弃**（返回 `Data`，协议签名 `func receiveData() async throws -> Data`）。这意味着：① 「client→Mac 恒为 text」与现状不符；② iOS 要实现「按 opcode 分流」必须**改 API 签名**（返回 `(opcode, Data)` 或新增方法）；
2. **Go 侧读循环的 opcode 容忍度**（产出 F-36 的缓解事实）——`server.go:670` 是 `_, raw, err := ws.ReadMessage()`，**opcode 被丢弃**，所以 Mac 当前对 text/binary **一视同仁地 JSON 解析**。这解释了为什么现状能工作，也意味着 S-8 只需在**发送侧**新增 opcode 区分，接收侧无需改动；
3. **F-32 清扫的完整性抽检**（产出 F-37）——`grep` 全文后发现 E-3/E-4 的阻塞对象、Gate A 的「自动满足」、§9 完成定义三处**仍写 S-1 口径**。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 判定 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `f868af18bbbbff6e7f47994c11fb76b49406b33e`；`git status --porcelain` = M 方案 + ?? R10 报告 + ?? R11 报告 + ?? 调研报告 | ✓ 与 R11 一致（未推进） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `e52daa275946ee0d41749aa7146ec8bbc8b05c29`；`git status --porcelain` 为**空** | ✓ 与 R11 一致 |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，工作树干净 | ✓ 一致 |
| 正式 runtime | 未重测（本轮无运行态结论） | — 未核 |

**iOS 锚点洁净性（逐文件实测）**：`ProjectionStore.swift`、`ProjectionReplica.swift`、`SessionProjectionWindow.swift`、`CCCodeBridgeBackendClient.swift`、`CCCodeBridgeTransport.swift`、`ChatViewModel.swift`、`ProjectionWindowSpecTests.swift` 逐个 `git status --porcelain --` 实测**全部 clean**；工作树整体为空。**核验对象 = 提交内容，身份成立。**

### 2.2 R11 五条意见闭合核验（本轮重点）

| 意见 | 实测 | 判定 |
| --- | --- | --- |
| **F-31** §3.6 是否定案自描述机制 | §3.6 新增「压缩帧自描述机制（v16 定案，F-31）」完整段落：text = 原始 JSON / binary = gzip(JSON envelope) 整帧；Mac 仅对已协商连接发 binary（gorilla `WriteMessage(BinaryMessage, …)`）；iOS 按 opcode 分流；阈值在序列化后字节上（≥32KiB 且更小）；**三条替代的否决理由随行记录**（① `WireMessage` 加字段=鸡生蛋 + base64 放大 33%；② gzip magic 嗅探=违反「不猜」纪律；③ permessage-deflate=v14 已否决） | ✓ **机制定案正确、论证扎实**（但见 F-36 的现状前提问题） |
| F-31 冻结批次是否同步更新 | 「独立批次（协议文档新章节**含 binary 帧语义与 capability 协商** + **binary fixture 样本**（gzip 字节 + 其解压后 JSON，README 记哈希）+ 断言（Mac：阈值/更小才压/**未协商连接零 binary 帧**；iOS：binary → gunzip → 与 text 路径输出深度相等）+ `schemaRevision`）」 | ✓ 落地（断言 ① 见 F-36） |
| **F-33** 独立样本文件 | §3.8「**冻结样本用独立文件**：`docs/protocol/samples/projection-window-v1/anchor-newer-response-spec.json`（两仓同步副本、字节一致）——Mac/iOS 既有 fixture 断言固定读 `window-response-spec.json` 并做精确 key 比对，追加新条目进同一文件必然失败；spec 测试为 anchor 样本新增对应读取点，不动既有文件与其断言」；§7.1.5「既有 `window-response-spec.json` 及其精确 key 断言零改动」 | ✓ **完整闭合** |
| **F-34** held 注册 | §3.8 新增「held turns 注册（v16 补，F-34）」bullet：`CompleteProjectionSnapshotWithHeldTurns(conn, admission, requestId, response, turnIDs)`（`projection_window_handler.go:201-211`），引用 P0-2 注释原文「**permanently strand the detail**」，增量页全部 turns（含锚点）注册；§7.1.5 补「并发 detail commit 负对照」断言 | ✓ **完整闭合**（锚点 `:201-211` 实测精确） |
| **F-35** E-10 双口径 | §5 E-10 行（`:702`）重写为「scalar state 正确性：**anchor 增量路径**（主对象）+ **冷路径 execution 回归护栏**」，冷路径护栏写明「running session 经全量 + gzip 首开后 `isSessionExecuting == true`」「v14『自动满足』论断降级为『需护栏』（防 S-5 解码重构丢失 execution 字段，消费链 `ChatViewModel.swift:2419-2426`）」；阻塞对象改为「**S-7 完成宣告**（anchor 部分）；冷路径护栏随第一阶段交付」 | ✓ **完整闭合** |
| **F-32** 悬空引用清扫 | **部分闭合**：trace 交付 → S-7 ✓；S-5 前置 → 无 ✓；S-3 前置 → v2 协议冻结 ✓；older gate 交付 → S-4 ✓（`§4 OD-8 行 :485` 与 `§7.2.15` 均改指）；§8 基底缓存回退 → 全量冷路径 ✓；§2.2 的「S-1 不改」→ 归 S-3 收口 ✓；§3.7 的「S-1 修好协商后」→ 补 v14 语境注 ✓。**但仍有 3 处漏改**（见 F-37）：E-3 阻塞对象、E-4 阻塞对象、Gate A 的「自动满足」+ §9 完成定义 | 🟡 **部分闭合**（F-37） |

### 2.3 F-31 定案的技术前提核验（本轮重点 + 新核验面）

**方案定案**：「`text` 帧 = 原始 JSON（现状不变），`binary` 帧 = gzip(JSON envelope) 整帧压缩；Mac 仅对**已协商 `direct_gzip_v1` 的连接**发送 binary 帧（gorilla `WriteMessage(BinaryMessage, …)`），iOS 按 opcode 分流（text → 现行解析；binary → gunzip → 同一解析路径）；**client→Mac 恒为 text**（镜像 relay 只压 Mac→client 的纪律）」

**实测结果**：

| 前提 | 实测 | 判定 |
| --- | --- | --- |
| gorilla 支持 `WriteMessage(BinaryMessage, …)` | `auth_gate.go:56`、`pairing_handler.go:90` 均用 `conn.WriteMessage(websocket.TextMessage, data)` → **gorilla API 支持，只是现有调用点用 TextMessage** | ✓ 可行 |
| Mac 读循环是否需要改 | `server.go:670` `_, raw, err := ws.ReadMessage()` —— **opcode 被丢弃**，对 text/binary 一视同仁做 `json.Unmarshal` | ✓ **接收侧无需改动**（这是 S-8 的一个有利事实） |
| iOS 直连用什么 WebSocket | `BridgeWebSocketConnection`（**自研实现**，基于 `NWConnection` + 手写帧解析），不是 `URLSessionWebSocketTask` | — 事实 |
| iOS 是否已有 opcode 枚举 | `BridgeWebSocketConnection.swift:134-141` `private enum WebSocketOpcode: UInt8 { continuation=0x0, text=0x1, binary=0x2, close=0x8, ping=0x9, pong=0xA }` | ✓ 已有 |
| iOS 读循环是否区分 opcode | `:311-330` **区分了**（`case .text, .binary:` 合并累加、`case .continuation:` 续传），但 `:315 let opcode = firstOpcode ?? frame.opcode` 只用于日志（`:324 NSLog(... frameType=%d ...)`） | ✓ 解析层已区分 |
| **iOS API 边界是否暴露 opcode** | **否**——协议 `:6 func receiveData() async throws -> Data`、`:71` 与 `:308` 两个实现均**只返回 `Data`**，opcode 在返回时丢弃 | 🟡 **缺口（F-36 ①）** |
| **client→Mac 当前发什么帧** | **binary**——`:304-306`：`func send(_ data: Data) async throws { try await sendFrame(opcode: .binary, payload: data) }` | 🟡 **与方案「恒为 text」相反（F-36 ②）** |

**判断**：opcode 机制**技术上完全可行**（iOS 已有枚举与解析、Mac 接收侧无需改、gorilla 支持），方案的机制选择正确。但方案对**现状的两处描述与实际不符**，且遗漏了 iOS 侧一处必要的 API 改造。详见 F-36。

### 2.4 数字与锚点抽核

§1.1 数字独立复算：checkpoint 34,537,988 B；turns 119；整棵 34,427,361 B；最大单 turn 6,347,762 B（index 1，不在尾 17）；尾 17 = 3,801,579 B；transcript 101,520,809 B = 96.8178 MiB；gzip1 11,142,266 / gzip6 9,602,913 B；8 次全量投递。**全部维持命中。**

锚点抽核 12 处：`projection_window.go:24-32/52-60/77-82/252-257/278-296/297-301`、`projection_revision_journal.go:8-12/127`、`handlers_projection.go:89/122/175/255`、`projection_snapshot_fence.go:214-216/224/227/230-235/269-276`、`event_publisher.go:1674-1687/1726-1749`、`projection_window_handler.go:201-211`、`server.go:670`、`types.go:13-33`、`relay_conn.go:296/304/313`、`projection_window_spec_test.go:33/46-57/73-75/198-211`、iOS `ProjectionStore.swift:1044-1056/620-626`、`ProjectionReplica.swift:193-212/208/323/465`、`BridgeWebSocketConnection.swift:6/71/134-141/304-306/308-330`、`ChatViewModel.swift:1080/2419-2426`、`ProjectionWindowSpecTests.swift:48/129-130`——**全部维持命中**。

### 2.5 覆盖声明

- **已实测核验**：三仓身份 + iOS 锚点洁净性逐文件；方案全文哈希（开工 + 收工）；交接块正文哈希；R11 五条逐条（含 F-31 的三条否决理由、F-33 的独立文件、F-34 的 held 锚点、F-35 的 E-10 行、F-32 的逐处改指）；**opcode 机制的 6 项技术前提**（gorilla API / Mac 读循环 / iOS WebSocket 实现 / opcode 枚举 / 读循环分流 / API 边界 / 发送帧类型）；F-32 清扫完整性（全文 grep）；数字与锚点抽核；§0 历史表 0.1–0.14 连续。
- **已独立推理（非命令实测，已标注）**：F-36 的「未协商连接零 binary 帧断言在现状下不成立」——由「iOS `send()` 固定 `.binary`」+「Mac 读循环不区分 opcode」两项实测推得，**未运行任何测试**；F-38 的「iOS 需改 API 签名」——由 `receiveData() -> Data` 的协议签名推得。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 运行态（本轮不涉及）；transcript 逐行 type 分布（对本案无影响）；**未 build / 未 test**（硬边界）。

## 3. 意见

### F-36 [建议] opcode 定案与 iOS 现状有两处不符：`client→Mac 恒为 text` 与实测相反（现状发 binary），且 iOS 的 `receiveData()` 丢弃 opcode，须改 API 才能分流

- **位置**：§3.6「压缩帧自描述机制（v16 定案，F-31）」段（「`text` 帧 = 原始 JSON（现状不变）……**client→Mac 恒为 text**（镜像 relay 只压 Mac→client 的纪律）」）；§3.6 冻结批次断言（「Mac：阈值/更小才压/**未协商连接零 binary 帧**」）。
- **实测证据**：
  1. **iOS 直连当前用 binary 帧发送**——`BridgeWebSocketConnection.swift:304-306`：
     ```swift
     func send(_ data: Data) async throws {
         try await sendFrame(opcode: .binary, payload: data)
     }
     ```
     即 **client→Mac 的现状是 binary，不是 text**。方案说「text = 原始 JSON（现状不变）」与「client→Mac 恒为 text」两句都与实测相反。
  2. **Mac 读循环不区分 opcode，所以现状能工作**——`server.go:670` `_, raw, err := ws.ReadMessage()`，随后直接 `json.Unmarshal(raw, &msg)`。这正是为什么 client 发 binary 也能被正确解析。
  3. **iOS 的 `receiveData()` 在 API 边界丢弃 opcode**——协议 `:6` `func receiveData() async throws -> Data`；实现 `:308-330` 内虽 `case .text, .binary:` 区分并累加，但 `:315 let opcode = firstOpcode ?? frame.opcode` 仅用于 `:324` 的日志输出，**返回值只有 `Data`**。要实现「按 opcode 分流」，必须改协议签名（如返回 `(opcode, Data)` 或新增 `receiveFrame()`）。
  4. **iOS 已有 opcode 枚举**——`:134-141` 完整定义 `text=0x1 / binary=0x2`，改造面小于从零实现。
- **影响**：
  - **对 S-8 功能本身无阻塞**：Mac 接收侧不区分 opcode，所以「client 继续发 binary」不影响任何现有行为；S-8 只需在**Mac 发送侧**新增 binary，iOS 接收侧改 API 分流。机制可行。
  - **但会误导实施者**：① 若照「client→Mac 恒为 text」去改 iOS 的 `send()`（把 `.binary` 改成 `.text`），是**无谓的行为变更**，且可能与 relay 路径的既有假设冲突；② 更要紧的是冻结断言「**未协商连接零 binary 帧**」——**在现状下 Mac 收到的 binary 帧来自 client 方向**（client 发的是 binary）。若该断言按「Mac 出站方向」理解则成立（未协商连接 Mac 不发 binary），但方案未限定方向，字面读会与现状冲突。这是「断言写宽了会误判」的典型。
- **修订方向**：① §3.6 把「text 帧 = 原始 JSON（现状不变）」与「client→Mac 恒为 text」改写为准确表述：**Mac→client 方向现状是 text；client→Mac 方向现状是 binary（`BridgeWebSocketConnection.send` 固定 `.binary`），S-8 不改该方向**；② 冻结断言明确**方向限定**：「**Mac→client 出站**方向，未协商 `direct_gzip_v1` 的连接零 binary 帧；已协商但单帧 <32KiB 或压缩不变小时同样发 text」；③ 补充 iOS 侧改造项：「`receiveData() -> Data` 需扩展为可返回 opcode（改协议签名或新增方法），`CCCodeBridgeTransport` 的 frame 接收路径据此分流」——并说明该改造对 relay 路径（`RelayBridgeFrameConnection`）的复用边界（relay 走 `URLSessionWebSocketTask`/`NWConnection` 的既有封装，是否需要同步扩展）。
- **闭合标准**：§3.6 对两个方向的现状描述与实测一致；冻结断言含方向限定；iOS 侧 API 改造项已登记。

### F-37 [建议] F-32 清扫未彻底：E-3/E-4 的阻塞对象、Gate A 的「自动满足」、§9 完成定义三处仍写 S-1 口径

- **位置**：§5 E-3 行（`"S-1 修复前必须闭合"`）；§5 E-4 行（`"S-1 验收仍需真实 wire"`）；§5 Gate A S-7 段（`"E-10（anchor 路径 scalar；冷路径全量原生含 execution，自动满足）"`）；§9 第一阶段（`"运行中 session 首开执行态 / 权限锁不丢（全量原生含 scalar，E-10 冷路径自动满足）"`）。
- **实测证据**（`grep -n` 全文，排除 §0 历史表）：
  - `:695` `| E-3 | … | S-1 修复前必须闭合 |`
  - `:696` `| E-4 | … | S-1 验收仍需真实 wire |`
  - `:707` Gate A S-7 段：`…E-10（anchor 路径 scalar；冷路径全量原生含 execution，**自动满足**）`
  - `:888` §9：`运行中 session 首开执行态 / 权限锁不丢（全量原生含 scalar，E-10 冷路径**自动满足**）`
- **影响**：三处均与 v14 取消 S-1 后的现实不符：E-3/E-4 的阻塞对象应是 **S-7**（R11 已改 E-2/E-10，但漏了 E-3/E-4）；而「自动满足」正是 F-35 明确降级为「需护栏」的表述，v16 在 §5 E-10 行已改，但 Gate A 与 §9 **仍保留旧措辞**——形成**同一份文档内 E-10 有两种口径**（`:702` 说「需护栏」、`:707`/`:888` 说「自动满足」）。这会让实施者不知道冷路径到底要不要做护栏断言（F-35 刚补的那条）。
- **修订方向**：① `:695` `S-1 修复前必须闭合` → `S-7 开工前必须闭合`；② `:696` `S-1 验收仍需真实 wire` → `S-7 验收仍需真实 wire（anchor 页 ≤4MiB 口径）`；③ `:707` 与 `:888` 的「自动满足」→「需冷路径 execution 回归护栏（§5 E-10）」。④ 建议做一次全文 `grep -n "S-1"` 与 `grep -n "自动满足"` 的收口检查，把「活断言」与「历史记录」分开（§0 历史表保留）。
- **闭合标准**：全文无指向已取消切片 S-1 的活断言；E-10 口径在全文档统一（无「自动满足」残留）。

### F-38 [建议] S-8 未登记 iOS 侧 transport 层的改动项，冻结批次的「iOS：binary → gunzip → 与 text 路径深度相等」断言缺改造前提

- **位置**：§3.6 S-8 段（「iOS 按 opcode 分流（text → 现行解析；binary → gunzip → 同一解析路径）；iOS 解压复用 Relay 路径既有 gunzip 代码」）；§3.6 冻结批次（「断言（… iOS：binary → gunzip → 与 text 路径输出深度相等）」）；§6 S-8 切片行（组件列未列 iOS transport 改造）。
- **实测证据**：
  - iOS 接收路径的 API 边界只返回 `Data`（`BridgeWebSocketConnection.swift:6/71/308`），无 opcode。
  - relay 路径的 gunzip 复用点需确认：`RelayBridgeFrameConnection` 走的是 `URLSessionWebSocketTask`（`:107` 注释）或 `NWConnection`，与直连的 `BridgeWebSocketConnection` **不是同一条连接实现**——「iOS 解压复用 Relay 路径既有 gunzip 代码」在**解压算法**层面成立，但在**帧接收与分流**层面需要新增。
  - §6 S-8 行的组件列只写「应用层逐帧，镜像 `relay_gzip_v1` 纪律」，未列 iOS transport 改造。
- **影响**：S-8 的 Mac 侧（gorilla `WriteMessage(BinaryMessage)`）与 iOS 侧（改 API + 分流 + gunzip）工作量不对称，但方案的组件列只描述了前者。冻结批次的「与 text 路径深度相等」断言**前提是 iOS 能拿到 opcode**——该前提未登记，断言无法构造。
- **修订方向**：① §3.6 补「iOS 侧改动项」bullet：`receiveData()` 签名扩展 / `CCCodeBridgeTransport` 的 frame 分发 / gunzip 接入点（复用 `relay` 的 gunzip 实现但需独立于 relay 的连接封装）；② §6 S-8 组件列补 iOS transport 改造；③ 冻结批次断言补「前提：iOS 已能按 opcode 分流」。
- **闭合标准**：S-8 的 iOS 侧改动项在 §3.6 与 §6 均有登记，冻结断言的前提明确。

## 4. 历轮意见闭合复核

### 4.1 R11 五条（本轮重点）

| 意见 | 级别 | 本轮判定 |
| --- | --- | --- |
| F-31 direct 帧自描述机制 | 阻塞 | **机制定案正确、论证扎实**；但两处现状描述与实测不符 + iOS API 改造未登记 → 降级为 F-36/F-38 建议 |
| F-32 悬空引用清扫 | 建议 | **部分闭合**（3 处漏改 → F-37） |
| F-33 独立样本文件 | 建议 | **完整闭合** |
| F-34 held 注册 | 建议 | **完整闭合** |
| F-35 E-10 双口径 | 建议 | **完整闭合**（§5 E-10 行；但 Gate A/§9 未同步 → 并入 F-37） |

### 4.2 历项抽核

| 意见 | 出处 | 判定 |
| --- | --- | --- |
| F-1 ~ F-9 | R1 | **全部闭合**（抽核维持） |
| F-10 | R2 | **闭合** |
| F-11 / F-12 | R3 | **闭合** |
| F-13 ~ F-16 | R3 | **闭合** |
| F-17 / F-18 / F-19 | R4 | **闭合** |
| F-20 | R5 | **闭合**（decide 路由五条断言维持） |
| F-21 / F-22 / F-23 | R6 | **闭合** |
| F-24 / F-25 | R7 | **闭合** |
| F-26 / F-27 | R8 | **闭合** |
| F-28 | R9 | **闭合** |
| F-29 / F-30 | R10 | **闭合**（R11 确认） |

**回归检查**：v16 改动集中于 §0/§3.6/§3.7/§3.8/§5/§6/§7.1.5/§7.2.10/§7.3/§9/§10.20，本轮重读这些章节及全部交叉引用。**发现的问题**：F-36（opcode 现状前提 + iOS API）、F-37（F-32 漏改 3 处）、F-38（S-8 iOS 侧改动未登记）。**未发现**：架构方向矛盾、数字错误、锚点错位（全部锚点逐字命中）。

## 5. 已定案事项复核

owner 已定案的 OD-1/3/6/7/8 本轮抽核，理由事实基础**仍全部准确**：

- **OD-1 = B**、**OD-3 = A**、**OD-6 = W1**、**OD-7 = A**（引 `projection_window.go:27-31` 逐行精确）、**OD-8 = (a)**（六处一致）——**维持**。
- **本轮唯一新增提示**：OD-8 的 older gate 交付对象已在 v16 改指 S-4（R11 的 F-32 项），理由「windowed 会话随 restore+increment 进入默认路径」成立——**这是本轮一处质量较高的主动修正**（R11 只指出「应改指」，v16 给出了为什么是 S-4 而非 S-7 的论证）。

## 6. 剩余门

- **证据门**：**E-3**（window 通路未生效根因——**S-7 开工前置**，见 F-37）；**E-4**（anchor 页 ≤4MiB——S-7 完成宣告，见 F-37）；E-5（iOS 阶段耗时——S-5）；E-6（materialization 正确性——S-2）；E-7（基底不污染权威 writer——S-4）；**E-10**（anchor 路径 scalar + **冷路径 execution 回归护栏**）；**E-11**（restore + 增量正确性——S-4 完成宣告）。
- **决定门**：**无 pending**（OD-1/3/6/7/8 全部定案；OD-2 被 v14 取代）。
- **冻结批次前置**：S-7 anchor-newer re-freeze 批次；**S-8 `direct_gzip_v1` 冻结批次**（机制已定案，但需先落实 F-36/F-38 的两处描述与改造项登记）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、R11 五条闭合、opcode 机制的六项技术前提、F-32 清扫完整性、S-8 iOS 侧改动、E-10 口径统一），全部亲核完成后才读 R11 报告与历轮报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与 R11 的差异**：R11 判 REVISION_REQUIRED（1 阻塞 + 4 建议）；v16 修完后本轮判 REVISION_REQUIRED（0 阻塞 + 3 建议）——**阻塞已解除**，三条新建议均为「描述精度 + 登记完整性」层面，不改变架构与机制。
- **本轮新核验面的价值**：F-36 是本轮最重要的发现。它揭示了一个**方案描述与代码现状相反**的情况（「client→Mac 恒为 text」vs 实测 `send()` 固定 `.binary`），而这类错误之所以危险，是因为**它不会让功能失效**（Mac 不区分 opcode），只会让冻结断言写错方向、让实施者做无谓改动。这与前几轮「方案没写的约束」不同，属于「方案写错的现状」——是评审需要主动比对代码而非仅核对方案内部一致性的又一例证。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；逐锚点文件 `git status --porcelain --`；`python3` 交接块哈希切分；`sed -n`/`awk` 逐段读 `BridgeWebSocketConnection.swift:6/71/134-141/296-330`、`server.go:665-690`、`auth_gate.go:56`、`pairing_handler.go:90`、`RelayBridgeFrameConnection.swift:107/120/160`、`projection_window_handler.go:201-211`；`grep -n "S-1"` / `grep -n "自动满足"` 全文收口检查；`grep -c "contentEncoding"` 复验。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 11 份报告 + 调研报告、iOS 工作树一律只读未触碰；**未 build / 未 test / 未部署 / 未安装**。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、字节测量、日志计数、fixture key 断言实测、冻结字段集作用域、**direct vs relay 传输路径对比**、**opcode 分流的 API 边界**、held 注册链路）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 171fe27df8bf17f327f00844b4c67a3a12ada700db4167831aabb15439c51c43
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
scope: full
blockers: 0
advisories: 3
open_gates: E-3, E-4(anchor 口径), E-5, E-6, E-7, E-10(anchor 口径 + 冷路径护栏), E-11, S-7 anchor-newer re-freeze 批次, S-8 direct_gzip_v1 冻结批次, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
