# 评审报告 R13：Claude 长 session 打开架构优化方案（v17 第十三轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R12 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v17**
方案 SHA-256（全文，开工实测）：`d66b971fbbf005526587aec84f118d3b78faa642a4fa636f19b8bc222b8d0ad0`——与派发记录一致
方案 SHA-256（收工复测）：`d66b971fbbf005526587aec84f118d3b78faa642a4fa636f19b8bc222b8d0ad0`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`c65d93338285134630d63718e89750e43cd6b4ce7b5b921dc76b3aa41d826333`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=96820）**精确命中**，记账自洽
范围：full（全量第十三轮复审）

## 1. 结论

- **verdict: APPROVED**
- blockers: **0**
- advisories: **2**（F-39、F-40，均为事实精度级，不影响可实施性）
- **R12 三条意见闭合判定：三条全部闭合**，且 F-37 的处置方式（grep 驱动全量清扫）是正确的方法论升级——同一问题第三次出现后改用机械验证而非人工逐处查找。
- **未阻塞的原因**：本轮两条新建议均为**锚点/计数的表述精度**问题（日志行号 `:324` 应为 `:326`；§0 表称「14 处」与交付说明称「17 处」不一致）。两者都不改变任何实施动作：前者是「日志行号引用偏差」，实施者按语义能找到正确行；后者是「修订表里的计数」，与设计无关。按判定口径（阻塞=方案层面不修订就不可实施/不可验收），两者均不构成阻塞。
- **设计层面无未决项**：架构、切片、门控、定案、回退面全部自洽；剩余门均为实施/验收阶段满足项。

**本轮自选新核验面（按指令要求声明，共三个）**：

1. **v17 是否真的只改描述而未动设计**（正面结论）——抽核六项设计不变量（W1 定案、F-26 协商复用、anchor 独立样本、held 注册、`applyFull(forceReplace:)`、不变量 6）全部保留，且 §0 声明「未改动 v16 的任何设计内容」与实测一致；
2. **清扫后的活引用收口是否彻底**（产出 F-40 的关联事实）——`grep "自动满足"` 在正文**零残留**（仅剩 §0 历史表与 §10 评审项的时代性记录）；`grep "S-1"` 的正文残留全部带「原 S-1 / v3 语境 / 本条已随 v14 失效」等标注或属评审项，**无未标注的活断言**；
3. **iOS opcode 改造面的精确性**（产出 F-39）——方案的日志行号引用 `:324/:334` 有一处偏差（`:324` 实为 `continue`，真正的日志行是 `:326`）。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 判定 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `f868af18bbbbff6e7f47994c11fb76b49406b33e`；`git status --porcelain` = M 方案 + ?? R10/R11/R12 报告 + ?? 调研报告 | ✓ 与 R12 一致（未推进） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `e52daa275946ee0d41749aa7146ec8bbc8b05c29`；`git status --porcelain` 为**空** | ✓ 与 R12 一致 |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，工作树干净 | ✓ 一致 |
| 正式 runtime | 未重测（本轮无运行态结论） | — 未核 |

**iOS 锚点洁净性**：工作树整体为空（`git status --porcelain` 无输出），故全部锚点文件内容 = 提交 `e52daa27` 内容。**核验对象身份成立。**

### 2.2 R12 三条意见闭合核验（本轮重点）

| 意见 | 实测 | 判定 |
| --- | --- | --- |
| **F-36** §3.6 方向更正 | §3.6 现文：「**定案：WebSocket opcode 区分（新增约束只作用于 Mac→client 方向，v17 更正 F-36）**——Mac→client：`text` = 原始 JSON（Mac 侧现状），`binary` = gzip 整帧（新增，仅发已协商连接，gorilla `WriteMessage(BinaryMessage, …)`）；iOS 按 opcode 分流」；并新增「**方向澄清与现状事实（v17 更正 F-36——v16 曾误写「client→Mac 恒为 text」）**」段：明确「client→Mac **现状就是 binary 帧**——iOS `send()` 即 `sendFrame(opcode: .binary, …)`（`BridgeWebSocketConnection.swift:304-306`），Mac 读循环丢弃 opcode（`server.go:670`）……**client→Mac 维持现状 binary，不在本方案变更边界内，不得顺手改成 text**；冻结断言「未协商连接零 binary 帧」**明确限定 Mac→client 方向**」 | ✓ **完整闭合**（两处锚点 `:304-306` / `:670` 实测精确） |
| **F-37** 清扫漏改 | **`grep "自动满足"` 全文：正文零残留**（剩余命中仅在 §0 历史表 `:30` 的 F-35 修订记录、`版本：` 行、`:714` E-10 行的「v14『自动满足』论断降级为『需护栏』」更正说明、`:719` Gate A 的「不再称『自动满足』」——**全部是更正性表述，无活断言**）；**E-3/E-4 已改指**：`| S-7 开工前必须闭合（v17 改指 F-37） |`、`| S-7 验收仍需真实 wire（v17 改指 F-37；口径 = anchor 增量页） |`；`grep "S-1"` 正文残留逐一检查：均为历史记录、评审项、或带「原 S-1 / v3 语境 / 本条已随 v14 失效」标注 | ✓ **完整闭合**（方法论升级正确） |
| **F-38** S-8 iOS transport 改造登记 | §6 S-8 行新增：「**iOS transport 改造（v17 补 F-38）**：`receiveData()` 签名（`BridgeWebSocketConnection.swift:6/:71/:308`）在 API 边界丢弃 opcode（仅 `:324/:334` 日志）——分流需改签名暴露 opcode；iOS 已有 opcode 枚举（`:134-141`：text=0x1/binary=0x2）且读循环已区分（`:311-330`），改造面小」；§7.2 新增第 17 条断言 | ✓ **闭合**（日志行号见 F-39） |

### 2.3 锚点核验（本轮亲核，含 R12 遗留）

| 方案引用 | 实测 | 判定 |
| --- | --- | --- |
| iOS `send()` = binary（F-36 核心） | `:304-306` `func send(_ data: Data) async throws { try await sendFrame(opcode: .binary, payload: data) }` | ✓ 精确 |
| Mac 读循环丢弃 opcode | `server.go:670` `_, raw, err := ws.ReadMessage()` | ✓ 精确 |
| `receiveData()` 签名 | `:6` `func receiveData() async throws -> Data`（协议）；`:71` 与 `:308` 两个实现同签名 | ✓ 精确 |
| iOS opcode 枚举 | `:134-141` `private enum WebSocketOpcode: UInt8 { continuation=0x0, text=0x1, binary=0x2, close=0x8, ping=0x9, pong=0xA }` | ✓ 精确 |
| iOS 读循环已区分 opcode | `:311-330`（`case .text, .binary:` 累加、`case .continuation:` 续传） | ✓ 精确 |
| **日志行号 `:324/:334`** | `:324` 实为 `continue`；日志行是 **`:326`**（完整消息）与 **`:334`**（分片完成） | 🟡 **一处偏差**（F-39） |
| `projection_window_handler.go:201-211`（held 注册） | 实测 `:201-211` 为 `turnIDs` 收集 + `CompleteProjectionSnapshotWithHeldTurns` 调用 | ✓ 精确（R11/R12 已核，本轮维持） |
| `projection_window_spec_test.go:33/:73-75`（fixture 固定文件） | `:33` `filepath.Join("..", "docs", "protocol", "samples", "projection-window-v1", "window-response-spec.json")`；`:73-75` 精确 key 断言 | ✓ 精确 |
| `applyFull` / `:208` / `:323` | `:193-212` 签名、`:208 state.appliedRev = projection.syncRev`、`:323 guard page.syncRev >= state.appliedRev` | ✓ 精确 |
| `ProjectionStore.swift:1044-1056` / `:620-626` | applyFull 后 commitIfObserved / applyWindowPage 后 commitIfObserved | ✓ 精确 |
| `ChatViewModel.swift:1080` / `:2419-2426` | `renderProjectionFromStore` / `isSessionExecuting` | ✓ 精确 |
| `projection_window.go:27-31`（OD-7 数值依据） | `maxWindowTurns=256` / `maxWindowEncodedBytes=4<<20` / `defaultWindowTurns=100` / `windowEncodingOverhead=4<<10` | ✓ 精确 |
| `bridge-v1.md:2266-2272` / `:2328-2332` | cursor 跨 epoch 禁令 / R10 freeze scope | ✓ 精确（历轮维持） |

### 2.4 数字复算（独立，非采信历轮）

按 §1.1 声明口径（UTF-8、`ensure_ascii=False` + 默认分隔符）：

| 项 | 本轮实测 |
| --- | --- |
| turns | **119** |
| 整棵 projection | **34,427,361 B** |
| 最大单 turn | **6,347,762 B** |
| 尾 17 turns | **3,801,579 B** |
| gzip level 1 | **11,142,266 B** |
| gzip level 6 | **9,602,913 B** |

与方案 §1.1 / R-2 的「约 34MB → gzip 后约 9.6–11MB」**精确吻合**。

### 2.5 设计不变量抽核（验证 v17 只改描述）

| 不变量 | 计数 | 判定 |
| --- | --- | --- |
| OD-6 = W1 定案 | 5 处 | ✓ 保留 |
| F-26 协商复用 `negotiateProjectionWindowV1` | 7 处（含「在 `negotiateProjectionWindowV1` 内扩展，direct `server.go:808` + relay `main.go:693` 双路径自动覆盖」） | ✓ 保留 |
| anchor 独立样本 `anchor-newer-response-spec.json` | 3 处 | ✓ 保留 |
| `CompleteProjectionSnapshotWithHeldTurns`（F-34） | 4 处 | ✓ 保留 |
| `applyFull(forceReplace: true)`（F-29） | 6 处 | ✓ 保留 |
| 不变量 6（Mac 无客户端状态） | 6 处 | ✓ 保留 |

**§0 声明「本章之外未改动 v16 的任何设计内容」与实测一致。**

### 2.6 覆盖声明

- **已实测核验**：三仓身份；方案全文哈希（开工 + 收工）；交接块正文哈希；R12 三条逐条（含 F-36 的方向限定、F-37 的 `grep` 双向验证、F-38 的 S-8 组件列与 §7.2.17）；12 处锚点抽核（含发现 F-39 的日志行号偏差）；数字独立复算；六项设计不变量；§10.21 复核点；`open_gates` 与决策表（`pending` 计数 = 0）；§0 结构 0.1–0.15 连续。
- **已独立推理（非命令实测，已标注）**：F-39 的「不影响实施」判断——由「实施者按语义（完整消息/分片完成）能定位到 `:326`/`:334`」推得；F-40 的「计数不一致不影响设计」——由「§0 表与交付说明的差异仅在修订记录层」推得。两者均**未运行任何 build/test**。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 运行态（本轮不涉及）；transcript 逐行 type 分布（对本案无影响）；**未 build / 未 test**（硬边界）。

## 3. 意见

### F-39 [建议] §6 S-8 行引用的日志行号 `:324/:334` 有一处偏差——`:324` 实为 `continue`，真正的完整消息日志在 `:326`

- **位置**：§6 S-8 切片行（「`receiveData()` 签名（`BridgeWebSocketConnection.swift:6/:71/:308`）在 API 边界丢弃 opcode（**仅 `:324/:334` 日志**）」）；§0 v17 修订表 F-38 行同。
- **实测证据**（`BridgeWebSocketConnection.swift:318-335`）：
  ```swift
  318  case .text, .binary:
  319      firstOpcode = frame.opcode
  320      try await validateAndAccumulateMessageSize(...)
  321      accumulated.append(frame.payload)
  322      // If FIN bit is not set, more fragments follow
  323      if !frame.fin {
  324          continue                      // ← :324 是 continue，不是日志
  325      }
  326      NSLog("[NetworkWebSocket] 收到完整消息 frameType=%d bytes=%d", opcode.rawValue, accumulated.count)   // ← 真正的日志
  327      return accumulated
  328  case .continuation:
  ...
  334      NSLog("[NetworkWebSocket] 收到分片消息完成 frameType=%d bytes=%d", ...)   // ← :334 正确
  ```
  即 `:334` 命中，`:324` 应为 `:326`。
- **影响**：**不改变任何实施动作**。方案该处的语义表述是准确的（「opcode 只用于日志」——实测 `:315` 的 `opcode` 变量确实只在 `:326`/`:334` 的 `NSLog` 中被使用，无其他消费点）。行号偏差只影响读者定位效率：按 `:324` 找会看到 `continue`，但按「日志」语义在附近两行内即可找到 `:326`。属事实精度问题。
- **修订方向**：把 `:324/:334` 改为 `:326/:334`；或在同处补一句「**读取路径**中 `opcode` 变量的消费点只有两处 `NSLog`（`:326`/`:334`），无其他用途（`:368`/`:376` 的 `opcode.rawValue` 属发送侧 `sendFrame` 参数，与本议题无关）」——后者更能表达方案想说的「接收侧 opcode 被丢弃」的实质。
- **闭合标准**：行号与实测一致，或改为不依赖精确行号的语义表述。

### F-40 [建议] v17 修订表称「本轮共修 14 处」，交付说明称「17 处」——文档内外的计数不一致

- **位置**：§0 v17 修订表 F-37 行（「本轮共修 **14 处**活引用」）。
- **实测证据**：方案 §0 表内写「本轮共修 **14 处**」（`grep -o "本轮共修 \*\*1[0-9] 处\*\*"` → 命中「14 处」）；而交付说明称「本轮共修 **17 处**（R12 点名的 E-3/E-4/Gate A/§9 之外，我的收口 grep 又抓出 5 处……以及最后复检再抓出 3 处）」。
- **影响**：**不影响设计、不影响实施、不影响验收**——它只是修订记录里的一个计数。但本方案历轮的可信度建立在「引用的数字均可亲核」之上（R6/R7/R9 三轮都以精确计数为证据，R9 的 F-28 正是因为「六条 vs 13 条」的计数偏差被记录）。同一份文档里出现「14 vs 17」的不一致，且**两者都是自己写的**，会让后续读者对同类计数失去校准。属建议级。
- **修订方向**：统一为实际数字（若交付说明的 17 处是准确的，§0 表应改 17；若 §0 表的 14 处是分类后的口径，应写明「14 处（另 3 处为复检补修，合计 17）」）。
- **闭合标准**：§0 表与交付说明的计数一致，或显式说明两个数字的关系。

## 4. 历轮意见闭合复核

### 4.1 R12 三条（本轮重点）

| 意见 | 级别 | 本轮判定 |
| --- | --- | --- |
| F-36 client→Mac 方向描述与代码相反 | 建议 | **闭合**（方向限定 + 现状事实 + 不得改 text 的约束 + 断言方向定界，四项齐备） |
| F-37 清扫漏 3 处 | 建议 | **闭合**（`grep` 双向验证：`自动满足` 正文零残留；`S-1` 正文残留全部带标注或属历史/评审项） |
| F-38 S-8 iOS transport 改造未登记 | 建议 | **闭合**（组件列 + §7.2.17 断言；日志行号偏差另立 F-39） |

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
| F-26 / F-27 | R8 | **闭合**（协商复用定案 7 处保留） |
| F-28 | R9 | **闭合** |
| F-29 / F-30 | R10 | **闭合** |
| F-31 | R11 | **闭合**（v16 定案 + v17 方向更正） |
| F-32 / F-33 / F-34 / F-35 | R11 | **全部闭合**（F-32 经 v17 grep 驱动清扫后彻底收口） |

**回归检查**：v17 改动集中于 §0/§3.6/§5/§6/§7.2/§8/§9/§10.21，本轮重读这些章节及全部交叉引用。**未发现**：架构方向矛盾、设计内容被误改、数字错误、锚点错位（除 F-39 的日志行号）。**§0 结构**：0.1–0.15 连续，无跳号无重复。

## 5. 已定案事项复核

owner 已定案的 OD-1/3/6/7/8 本轮抽核，理由事实基础**仍全部准确**，且 v17 未改动其任何内容：

- **OD-1 = B**（恢复基底 + 同步标记）✓
- **OD-3 = A**（无 TTL，identity/schema/容量/LRU 淘汰）✓
- **OD-6 = W1**（window 响应 additive 携带 scalar；v14 起交付载体收缩到 anchor 新类型）✓
- **OD-7 = A**（4MiB/256 turns、128MB、2 页、100 turns——引 `projection_window.go:27-31` 逐行精确）✓
- **OD-8 = (a)**（关闭 older 上滑，截断提示为唯一交互）✓

**决策表收口验证**：`sed -n '/^| ID | 问题/,/^$/p' | grep -c pending` = **0**，确无 pending 项。

## 6. 剩余门

设计层面**无未决项**。剩余门全部为实施/验收阶段满足项：

- **证据门**：**E-3**（window 通路未生效根因——**S-7 开工前必须闭合**）；**E-4**（anchor 页 ≤4MiB——S-7 验收）；E-5（iOS 阶段耗时——S-5）；E-6（materialization 正确性——S-2）；E-7（基底不污染权威 writer——S-4）；**E-10**（anchor 路径 scalar + **冷路径 execution 回归护栏**）；**E-11**（restore + 增量正确性——S-4 完成宣告）。
- **决定门**：**无 pending**。
- **冻结批次前置**：S-7 anchor-newer re-freeze 批次；S-8 `direct_gzip_v1` 冻结批次（机制已定案、iOS 改造已登记，可开工）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、R12 三条闭合、方向更正的代码一致性、清扫收口验证、iOS 改造面、设计不变量、数字复算），全部亲核完成后才读 R12 报告与历轮报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与 R12 的差异**：R12 判 REVISION_REQUIRED（0 阻塞 + 3 建议）；v17 修完后本轮判 **APPROVED**（0 阻塞 + 2 建议）。两条新建议（F-39 行号、F-40 计数）**均不改变任何实施动作**，与前几轮那些「不修就无法实施」的阻塞项有本质区别——这是判 APPROVED 的依据。
- **方法论评价**：F-37 是同一问题（悬空引用清扫）**第三次**出现（R11 的 F-32 → R12 的 F-37 → v17 的 grep 驱动全量清扫）。v17 改用机械验证（`grep "S-1"` + `grep "自动满足"` 全量过一遍）而非人工逐处查找，是正确的方法论升级。本轮独立复验确认该方法有效：`自动满足` 正文零残留、`S-1` 残留全部带标注。**这一处置方式值得作为后续同类修订的范式。**
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`python3` 交接块哈希切分 + checkpoint 复算（turns/整棵/最大 turn/尾 17/gzip 双级别）；`grep -n "自动满足"` 全文；`grep -n "S-1"` + 逐行分类（历史/评审项/带标注 vs 活断言）；`sed -n`/`awk` 逐段读 `BridgeWebSocketConnection.swift:304-306/311-330/318-340`、`server.go:670`、`projection_window_handler.go:201-211`、`projection_window_spec_test.go:33/73-75`、`ProjectionReplica.swift:193-212/208/323`、`ProjectionStore.swift:1044-1056/620-626`、`ChatViewModel.swift:1080/2419-2426`、`projection_window.go:27-31`；六项设计不变量 `grep -c`；决策表 `pending` 计数。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 12 份报告 + 调研报告、iOS 工作树一律只读未触碰；**未 build / 未 test / 未部署 / 未安装**。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、字节测量、gzip 双级别、日志计数、fixture key 断言、冻结字段集作用域、direct vs relay 传输路径、opcode 分流的 API 边界、held 注册链路、设计不变量抽核）。

---

评审员交接块：

```text
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: d66b971fbbf005526587aec84f118d3b78faa642a4fa636f19b8bc222b8d0ad0
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
scope: full
blockers: 0
advisories: 2
open_gates: E-3(S-7 开工前), E-4(anchor 口径), E-5, E-6, E-7, E-10(anchor 主对象+冷路径护栏), E-11, S-7/S-8 re-freeze 批次, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
