# 评审报告 R8：Claude 长 session 打开架构优化方案（v7 第八轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R7 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v7**
方案 SHA-256（全文，开工实测）：`4c598fb00112908c5269c4197c8ea0fe2a312cd30c24f83d350a8da6ef8ef3f4`——与派发记录一致
方案 SHA-256（收工复测）：`4c598fb00112908c5269c4197c8ea0fe2a312cd30c24f83d350a8da6ef8ef3f4`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`8943d56900efff28150f5fc206995eec1bba0a4f94004f87a3a57ecd0e6e0452`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=57403）**精确命中**，记账自洽
范围：full（全量第八轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **0**
- advisories: **2**（F-26、F-27）
- **F-24 / F-25 闭合判定：两条全部闭合**（F-24 四处行号更正逐处亲核命中、双副本义务可执行；F-25 可观测前提 + 计数口径 + §6 同步登记，三者一致，见 §3）。
- **OD-6 = W1 复核：维持。§3.2.2 存储选型复核：维持。**（§5）
- **未判 APPROVED 的原因**：本轮自选新核验面发现两处建议级缺口（F-26 `projection_window_scalar_v1` 的协商实现机制未定义、与 direct/relay 双 hello 路径的镜像义务未登记；F-27 re-freeze 批次未覆盖 `schemaRevision` 纪律与既有 doc/code 分歧）。两者均**不阻塞 S-1 开工**——实施者会在编码时自然发现并处理，故为建议级。但 F-26 属「方案没写的既有链路约束」盲区**第四次**出现（前三次：fence 语义 → SQLite 选型 → 协议冻结），且这次落在 W1 的核心机制上（新 capability 怎么被协商出来），不修则 S-1 的「Mac echo 后 iOS 才走 window」这条主线缺少可执行定义。

**本轮自选新核验面（按指令要求声明）**：除复核 F-24/F-25 与全量锚点外，本轮新增两个此前八轮均未覆盖的核验面：

1. **新 capability 的协商实现路径与双 hello 路径镜像义务**（产出 F-26，建议）——`projection_window_scalar_v1` 是 W1 的开关，但方案只写「hello 声明 + Mac echo」，未定义 echo 由哪个函数实现、是否需要 direct/relay 两条路径分别登记；
2. **re-freeze 批次与 `schemaRevision` 纪律的关系**（产出 F-27，建议）——协议 pack 有 schemaRevision 递增惯例，且实测存在既有 doc/code 分歧（README `2026-10-06-r1` vs 代码 `2026-09-05`），re-freeze 五项批次未涉及。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 | 判定 |
| --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；`git status --porcelain` 仅 9 个未跟踪 docs 文件（方案 + R1/R2/R2-meta/R3/R4/R5/R6/R7） | 同 | ✓ 一致（与交付说明「零代码改动」相符） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `dfb77ed93c610165730a88f8483c80e78941d321`；未提交 **8 项**（M `project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift`） | v6 已回填 `dfb77ed9…` + 8 项枚举 | ✓ 一致 |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | 同 | ✓ 一致 |
| 正式 runtime | PID 8303，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime` 监听 `*:8777`；构建身份 ≠ 源码 HEAD | 同 | ✓ 一致（不作产物身份推断） |

**锚点文件洁净性**：`ProjectionWindowSpecTests.swift`（本轮 F-24 核心锚点）逐文件 `git status --porcelain --` 实测 **clean**，核验对象 = 提交 `dfb77ed9` 内容。全部未提交文件**只读未触碰**。

### 2.2 F-24 闭合核验（四处行号 + 双副本）

| v7 声明 | 实测 | 判定 |
| --- | --- | --- |
| `§3.7` 断言清单第 3 条改为 `:48` | `:519` 现为「iOS `ProjectionWindowSpecTests.swift:48`（fixture **response keys** 集合相等断言——v7 更正：v6 误引 `:51`，该行断言的是 window 描述符 key，`scalarState` 落在 Result 层不触碰它）」 | ✓ 落地 |
| 批次⑤ 改为 `:48` + `:129-130`，并标注 `:51` 与 Mac `:77-80` 不受影响 | `:521` 批次⑤：「response 级 key 集合断言 `ProjectionWindowSpecTests.swift:48` 与 schema 冻结集 `:129-130`；`:51`（window 描述符 key 断言）与 Mac `:77-80`（描述符级 DeepEqual）**不受 `scalarState` 影响、无需改动**」 | ✓ 落地 |
| §7.2.1 改为 `:48` | `:645`：「冻结断言随 re-freeze 批次更新为含 `scalarState`（response 级 `:48` + schema 冻结集 `:129-130`；描述符级 `:51` 不受影响）」 | ✓ 落地 |
| §10.11 改为 `:48` | `:750`：「`ProjectionWindowSpecTests.swift:48/:129-130`（v7 更正：response 级为 `:48`，描述符级 `:51` 不受影响）」 | ✓ 落地 |

**锚点内容复核**（独立读取，非采信方案文字）：
```swift
47  let response = try XCTUnwrap(fixture["response"] as? [String: Any])
48  XCTAssertEqual(Set(response.keys), ["window", "turns", "syncRev", "resume"])   // ← 会被 scalarState 打破
50  let window = try XCTUnwrap(response["window"] as? [String: Any])
51  XCTAssertEqual(Set(window.keys), windowFields)                                  // ← 描述符级，不受影响
```
Mac 对照 `go-bridge/projection_window_spec_test.go:77-80` 为 `window` 描述符 DeepEqual，同样不受影响。**v7 的四处置换与「描述符级不受影响」的定性均准确。**

**双副本义务**（`§3.7` 批次③）：v7 补「该样本是两仓独立副本（v7 补，F-24）——Mac `docs/protocol/samples/projection-window-v1/window-response-spec.json` 与 iOS 仓同路径副本（实测同哈希 `6853e4ac…`，iOS 测试经 `fixtureRoot` 读本仓副本，`ProjectionWindowSpecTests.swift:34/:43/:85`）——两份必须同步更新并保持字节一致」。本轮实测三处 `fixtureRoot` 读取点（`:34` 定义、`:43` 与 `:85` 两次 `contentsOf`）全部命中，双副本哈希一致。**可执行，闭合。**

### 2.3 F-25 闭合核验（可观测前提 + 计数口径 + §6 同步）

| v7 声明 | 实测 | 判定 |
| --- | --- | --- |
| window handler 无 `projection_rpc` trace（grep = 0） | `grep -c logProjectionRPCTrace go-bridge/projection_window_handler.go` = **0**；该文件仅两处 `slog.Warn`（`:195` fence 释放失败、`:211` enqueue 失败），**成功路径零日志** | ✓ 精确 |
| fence 日志字段无 requestId | `projection_snapshot_fence.go:127-130`（`sessionPrefix/cutRev/generation`）、`:239-243`（`sessionPrefix/pending/invalidated/expectedRev`）——均无 requestId | ✓ 精确 |
| full 侧锚点 `handlers_projection.go:89/:175` | `:89 logProjectionRPCTrace("mac_receive", ...)`；`:175 logProjectionRPCTrace("response_enqueue", ..., code, nil)` | ✓ 逐字符精确 |
| §7.3 补可观测前提 | `:682` 已写入「S-1 交付必须包含 window 路径与 full 同构的 `projection_rpc` trace（requestId/backend/sessionID/outcome；full 侧锚点 `handlers_projection.go:89/:175`），否则本指标在 window 生效后不可计数」 | ✓ 落地 |
| §7.3 补计数口径 | `:682` 已写入「按「同一 session 单次打开内的 RPC 发起序列」计数（Mac trace 与 iOS transport trace 对账）；capability 不满足产生的**合法回退不计入重试**（双维度 gate 判定后直接 full，或 window 收到 typed `protocol.capability_required` 后回 full——各 ≤1 次，单独归类）；`projection.hydrating` / 网络失败后的重复发起计入重试」 | ✓ 落地，口径可判定 |
| §6 S-1 组件列同步登记 | `:593` 已写入「**window 路径 `projection_rpc` trace（v7 补，F-25——与 full 同构：requestId/backend/sessionID/outcome；full 侧锚点 `handlers_projection.go:89/:175`，window handler 现无该 trace，缺它则 §7.3 重试指标不可观测）**」 | ✓ 落地 |
| §10.12 复核请求 | `:751` 存在，含两项复核点 | ✓ 落地 |

**F-25 闭合。** 这处修订的可取之处是把「指标不可观测」升级成了**交付项**（而非仅记录风险），并给出了可判定的计数边界（合法回退 vs 重试）。

### 2.4 数字与日志抽核

沿用 §1.1 声明口径独立抽核（本轮抽核，未重复全量三口径）：checkpoint 34,537,988 B；turns 119；整棵 34,427,361 B；最大单 turn 6,347,762 B（index 1，不在尾 17）；尾 17 = 3,801,579 B / gzip6 1,084,899 B；transcript 101,520,809 B = 96.8178 MiB；8 次全量投递（req_18/33/54/58/67/77/82/91，gen 1→5）；E-2 window RPC = 0；E-1 hydrate_failed = 0。**全部维持命中。**

### 2.5 锚点抽核（历轮既有锚点维持）

抽核 12 处高价值项：`projection_window.go:77-82/24-26`、`projection_window_handler.go:32/102-106/124-126`、`handlers_projection.go:122`、`projection_kernel.go:266-278/280/1123`、`main.go:560`、`server.go:373-395`、`projection_snapshot_fence.go:224/227/230-235/269-276`、`event_publisher.go:771-776/1726-1749/1802-1811`、`claude_stub_status.go:145-157`、iOS `ProjectionStore.swift:145/152/763-777`、`SessionProjectionWindow.swift:96-98/116-176`、Codex `codex-rs/cloud-client/README.md:18`——**全部维持命中**。

### 2.6 覆盖声明

- **已实测核验**：三仓 + runtime 来源身份；方案全文哈希（开工 + 收工）；交接块正文哈希（idx=57403 精确命中）；F-24 四处行号逐处比对 + 描述符级断言内容独立复核 + 双副本哈希与三处 `fixtureRoot` 读取点；F-25 六项声明逐项比对（含 window handler `grep -c` = 0、fence 字段实测、full 侧 `:89/:175` 逐字符）；§7.3/§6/§10.12 一致性；数字抽核；既有锚点抽核。
- **已独立推理（非命令实测，已标注）**：F-26 的「新 capability 协商路径需显式定义」——基于 direct（`server.go:808`）与 relay（`main.go:693`）两条 hello 路径各自独立调用 negotiate 函数的实测结构推得；F-27 的「re-freeze 未覆盖 schemaRevision」——基于协议 pack 的 revision 惯例与实测 doc/code 分歧推得。两者均未运行任何构建或测试。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 精确构建 commit（S-0 职责）；transcript 逐行 type 分布（对本案无影响）；iOS 8 个未提交文件内容（按指令只读未触碰）；**未 build / 未 test**（硬边界）。

## 3. 意见

### F-26 [建议] `projection_window_scalar_v1` 的协商实现机制未定义，且与 direct/relay 双 hello 路径的镜像义务未登记

- **位置**：§3.7 W1 机制段（`ProjectionWindowResponse` 前的「新增 additive capability `projection_window_scalar_v1`（hello 声明 + Mac echo，echo 条件 = 现有 `projection_window_v1` 条件 + 本实现落地）」）；§3.7 iOS 双维度 gate 段；§5 Gate A；§6 S-1 组件列。
- **实测证据**：
  1. **两条 hello 路径各自独立协商**——direct：`go-bridge/server.go:808` `if !s.negotiateProjectionWindowV1(ack, &hello, connection) {`（同段 `:814` lazy、`:820` chunks）；relay：`go-bridge/main.go:693` `if !server.negotiateProjectionWindowV1(ack, &hello, conn) {`（同段 `:700` lazy、`:707` chunks）。**每条 capability 都必须在两处分别登记**，`main.go:704-706` 的注释明写「relay is the iPhone production path — the mirror is mandatory, not optional」。
  2. **能力字符串只存在于 server.go**——`grep -c '"projection_window_v1"'`：`server.go`=3、`main.go`=0。即 relay 路径之所以覆盖，是因为它**调用了同一个 `negotiateProjectionWindowV1`**，而非自己写字符串。
  3. 因此新 capability 有两种实现形态，后果不同：
     - **(a) 复用 `negotiateProjectionWindowV1`**（在该函数内一并 echo scalar）→ relay 自动覆盖，**零额外工作**；
     - **(b) 新增独立 `negotiateProjectionWindowScalarV1`** → **必须**在 `server.go` 与 `main.go` 各加一次调用，漏掉 relay 会导致「Relay 路径下 iOS 收不到 scalar echo → 双维度 gate 不满足 → 永久回 full path」，即 R-2 的 Relay ≤5s 目标无法达成。
  4. 方案现文只写「hello 声明 + Mac echo，echo 条件 = …」，**未指明 (a) 还是 (b)**，也未提及 relay 镜像。
- **影响**：W1 的开关就是这条 capability。若实施者选 (b) 且只在 `server.go` 加（照抄 lazy/chunks 的 server.go 部分而漏掉 main.go），Direct 正常、**Relay 静默退化**——而 §7.4 真机矩阵场景 5「Relay 弱网重开」才会暴露，此时已到验收阶段。方案对此没有任何提示或测试项（§7.1.1 只测「capability echo 后 window_0 必达」，未区分 direct/relay；§7.2.1 只测 iOS 侧 `supportsProjectionWindow`）。
- **修订方向**：① §3.7 W1 机制段明确协商实现形态（推荐 (a)：在 `negotiateProjectionWindowV1` 内一并处理 scalar，理由 = relay 自动覆盖、与 window 强耦合天然同门）；若选 (b)，则显式登记「必须在 `server.go:808` 与 `main.go:693` 两处各加调用」并给出锚点；② §7.1.1 或 §7.2.1 补一条**双路径断言**：Direct 与 Relay 连接各自 scalar echo 生效；③ §8 风险表可加一行「Relay 路径 scalar echo 遗漏 → Relay 永久回 full」。
- **闭合标准**：方案指明 scalar capability 的协商实现形态；若为独立函数则明确登记双路径调用义务；测试矩阵含 direct/relay 双路径 echo 断言。

### F-27 [建议] re-freeze 五项批次未覆盖 `schemaRevision` 纪律；实测存在既有 doc/code 分歧（README `2026-10-06-r1` vs 代码 `2026-09-05`）

- **位置**：§3.7 代价行 re-freeze 批次五项（① 协议章节 ② schema types ③ 冻结样本双副本 ④ Mac spec ⑤ iOS spec）；§6 S-1 前置。
- **实测证据**：
  1. **协议 pack 有 revision 递增惯例**——`docs/protocol/README.md:12-16` 的 Current Versions 表列 `Schema revision`；`go-bridge/bridge_v1_schema.go:6-15` 注释明写「非破坏性可选新增，不 bump major version，**只 bump schemaRevision**」，且该常量随每次 additive 变更在注释中累积记录（`:12` 2026-07-24、`:55-57` planMode、`:59-63` session_goal、`:65-69` context_injection、`:71-76` workflow）。
  2. **但代码常量停在 `2026-09-05`**——`bridge_v1_schema.go:77 BridgeProtocolSchemaRevision = "2026-09-05"`；iOS `CCCodeBridgeModels.swift:9 static let schemaRevision = "2026-09-05"`（两侧一致）。
  3. **而 README 记 `2026-10-06-r1`**——`docs/protocol/README.md:15`。`git log -S 'BridgeProtocolSchemaRevision = "2026-10-06-r1"'` 无命中，即**代码从未取过该值**；该分歧由 `a50946c4`（session_media_dimensions，2026-10-06）引入，属**既有分歧、非本方案造成**。
  4. `schemaRevision` 是**纯信息字段、不参与门控**——`bridge_v1_schema.go:8-9` 明写「hello 只在 Protocol.Version 上 gating（`hello_handler.go:97`），schemaRevision 纯信息字段，旧客户端不受影响」；实测 `SchemaRevision` 在 `hello_handler.go:188`（ack 填充）与 `server.go:716`（relay 帧）仅作填充，无任何比较分支。
  5. **方案全文零处提及 schemaRevision**（`grep -c schemaRevision` = 0）。
- **影响**：不阻塞实施（`schemaRevision` 不门控，W1 功能不依赖它）。但 re-freeze 批次的语义是「按 R10 重新冻结协议」，而协议 pack 的既有惯例是每次 additive 变更同步 bump `schemaRevision` 并累积注释——批次五项**没有这一项**。后果有两个：① 实施者按批次做完 re-freeze，`schemaRevision` 仍停在 `2026-09-05`，与协议 pack 惯例不一致（下一个接手者无法从 revision 判断 `scalarState` 属于哪次变更）；② 既有 doc/code 分歧（README `2026-10-06-r1` vs 代码 `2026-09-05`）会与本次新增混在一起，若实施者顺手「修正」README，会把一个与本案无关的既有分歧卷入本批次，污染 S-1 的变更边界。
- **修订方向**：① re-freeze 批次补第⑥项：bump `BridgeProtocolSchemaRevision`（Mac `bridge_v1_schema.go:77`）与 iOS `CCCodeBridgeProtocol.schemaRevision`（`CCCodeBridgeModels.swift:9`）为同一新值，并在常量注释中按既有格式追加本次变更条目；② 显式声明**既有 doc/code 分歧不在本批次范围**（README `2026-10-06-r1` 与代码 `2026-09-05` 的差异另行处置），避免实施者把无关修正混入；③ 或明确写「本次 re-freeze 不 bump schemaRevision，理由：…」，使该决定显式而非遗漏。
- **闭合标准**：re-freeze 批次对 `schemaRevision` 有明确处置（bump 或显式不 bump + 理由），且既有 doc/code 分歧被显式排除在本批次之外。

## 4. 历轮意见闭合复核（本轮独立重判）

| 意见 | 级别 | 出处 | 本轮判定 |
| --- | --- | --- | --- |
| F-1 ~ F-9 | 2 阻塞 + 7 建议 | R1 | **全部闭合**（抽核维持） |
| F-10 iOS 枚举漂移 | 建议 | R2 | **闭合** |
| F-11 window 冷开缺 scalar | 阻塞 | R3 | **闭合**（设计层面；实施前置由 F-21 承接） |
| F-12 SQLite 选型 | 阻塞 | R3 | **闭合** |
| F-13 ~ F-16 | 4 建议 | R3 | **全部闭合** |
| F-17 | 阻塞 | R4 | **闭合** |
| F-18 / F-19 | 2 建议 | R4 | **闭合** |
| F-20 decide 路由维度 | 阻塞 | R5 | **闭合**（R6 全量亲核，R7/R8 抽核维持） |
| F-21 re-freeze 前置 | 阻塞 | R6 | **闭合**（R7 核验，本轮抽核维持） |
| F-22 peer 组合行为 | 建议 | R6 | **闭合** |
| F-23 iOS 来源回填 | 建议 | R6 | **闭合** |
| **F-24 行号错位 + 双副本** | 建议 | R7 | **闭合**（四处置换逐处命中；描述符级定性准确；双副本义务含哈希与三处读取锚点，可执行） |
| **F-25 重试指标不可观测** | 建议 | R7 | **闭合**（可观测前提升级为 S-1 交付项；计数口径可判定；§6/§7.3/§10.12 三处一致） |

**回归检查**：v7 改动集中于 §0/§3.7/§6 S-1/§7.2.1/§7.3/§10.11-12，本轮重读这些章节及全部交叉引用（§3.7 批次 ↔ Gate A ↔ S-1 ↔ §7.1.4 ↔ §7.2.1 ↔ §7.3 ↔ §10.12），**未发现修订引入的新矛盾或悬空引用**；W1 定案、re-freeze 批次结构、peer 组合表、存储选型、架构方向、切片划分均未改动（与 §0 声明一致）。「标准 additive」旧定性经 `grep` 复查仍仅以「更正对象」身份出现，无残留活断言。

## 5. 已定案事项复核（本轮独立重判）

### 5.1 OD-6 = W1（维持）

- **W1**：零额外往返；scalar 随权威 page 经 replica 同构写入；冷开 running session 正确。v6/v7 已把 re-freeze 与 trace 交付显式登记为实施前置，成本透明。
- **W2**：多一次 RTT 挤压 R-2 的 ≤3s/≤5s 预算；需定义两次响应间 patch 排序，竞态面更大。维持淘汰。
- **W3**：`runtimeStateStore` 是 live 事件消费者，冷开「已在运行」session 无历史事件可学，结构性不覆盖主场景；且固化执行态双源。维持淘汰。

**结论：W1 维持。** 本轮新增的 F-26 属 W1 的**实施机制细节**（协商形态），不构成对 W1 路线本身的挑战。

### 5.2 §3.2.2 存储选型（Mac=bbolt / iOS=文件型，维持）

五项约束本轮抽核（连续第四轮）：`go.mod`/`go.sum` 无 SQLite 驱动、`project.yml:53` 未设 `CGO_ENABLED`、`session_catalog.go:14-16` 既有立场、`import_guard_test.go:23-24` forbidden surface、iOS 零 SQLite 栈。**全部维持成立**，无翻案依据。

## 6. 剩余门

- **证据门**：E-3（window 未生效根因——S-1 修复前必须闭合；含 descriptor 维度）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时——S-5）；E-6（materialization 正确性——S-2）；E-7（preview 不污染权威 writer——S-4）；**E-10**（window 冷开 scalar 正确性——S-1 完成宣告）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4，OD-2 阻塞 S-3 协议冻结）。
- **S-1 开工前置（已登记）**：E-1/E-2 + OD-6 定案 + F-20 修订（已完成）+ F-21 re-freeze 批次（含 v7 的双副本义务）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、F-24/F-25 逐条、新 capability 协商路径、schemaRevision 纪律、数字与锚点抽核），全部亲核完成后才读 R4–R7 报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与前轮差异**：R7 判 REVISION_REQUIRED（0 阻塞 + 2 建议）；本轮确认 F-24/F-25 全部闭合，仍判 REVISION_REQUIRED（0 阻塞 + 2 新建议）。分歧根因仍是「方案没写的既有链路约束」——本次落在 W1 的协商机制（F-26）与协议 revision 惯例（F-27）上。**两条均为建议级**：S-1 按 v7 文本可开工，实施者会在编码/验收阶段自然发现，但 F-26 若在 Relay 侧遗漏会直到真机矩阵场景 5 才暴露。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`python3` 交接块哈希切分；`grep -c logProjectionRPCTrace go-bridge/projection_window_handler.go`；`grep -c '"<capability>"' server.go main.go` 双路径镜像比对；`sed -n`/`awk` 逐段读 `projection_window_spec_test.go`、`ProjectionWindowSpecTests.swift`、`projection_snapshot_fence.go`、`bridge_v1_schema.go`、`hello_handler.go`、`server.go`、`main.go`、`docs/protocol/README.md`；`git log -S` 追溯 schemaRevision 分歧来源；`grep -c schemaRevision <plan>`。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 7 份报告、iOS 8 个未提交文件一律只读未触碰；未 build / 未 test / 未部署 / 未安装。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、字节测量、日志计数、fixture key 断言实测、双副本哈希比对、双 hello 路径镜像比对）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 4c598fb00112908c5269c4197c8ea0fe2a312cd30c24f83d350a8da6ef8ef3f4
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r8.md
scope: full
blockers: 0
advisories: 2
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, F-21 re-freeze 批次(S-1 前置), S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
