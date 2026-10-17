# 评审报告 R7：Claude 长 session 打开架构优化方案（v6 第七轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R6 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v6**
方案 SHA-256（全文，开工实测）：`a4d360225a0c5747298728c297ad7a1108eae77357f8b08023c3522d623262ee`——与派发记录一致
方案 SHA-256（收工复测）：`a4d360225a0c5747298728c297ad7a1108eae77357f8b08023c3522d623262ee`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`6ba548a6baca53bb9104115d2d294ca5856a2c3469400895229fc70c5f904ded`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=54514）**精确命中**，记账自洽
范围：full（全量第七轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **0**
- advisories: **2**（F-24、F-25）
- **F-21 / F-22 / F-23 闭合判定：三条全部闭合**（F-21 有一处锚点精度瑕疵，降级为 F-24 建议；F-22 与 F-23 完整闭合，见 §3）。
- **OD-6 = W1 复核：维持**。**§3.2.2 存储选型（Mac=bbolt / iOS=文件型）复核：维持**。两者本轮均独立重判，无推翻依据（§5）。
- **未判 APPROVED 的原因**：本轮发现两处建议级缺陷（F-24 锚点精度、F-25 重试/协议耦合的可观测性缺口）。按契约「阻塞=方案层面不修订就不可实施/不可验收的缺陷」，这两项**不构成实施阻塞**——S-1 按 v6 文本仍可开工并可通过既有测试门。但 F-24 涉及 re-freeze 批次的**完整性**（v6 自述「五项批次」中第⑤项列错了行号、漏列了真正会失败的那条断言），若不修，实施者按文面执行会在 iOS 侧遇到一个**未被登记的失败测试**，正好重演 F-21 想防的形态。因此本轮不判 APPROVED。

**本轮自选新核验面（按指令要求声明）**：除复核 F-21/F-22/F-23 与全量锚点外，本轮新增两个此前七轮均未覆盖的核验面：

1. **re-freeze 批次「五项」的逐项可执行性**（产出 F-24，建议）——逐项核对 v6 列出的锚点是否真的指向会被 `scalarState` 打破的那条断言；发现 iOS 侧引用了错误的行号，且 Mac/iOS 两侧冻结样本是**两份独立副本**（非共享文件），v6 未登记双副本同步义务；
2. **协议再冻结与既有「冻结即不可改」测试纪律的相互作用**（产出 F-25，建议）——re-freeze 更新冻结物之后，既有测试的防护语义是否真的「不减」；以及 window 重试指标与协议冻结之间的观测缺口。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 | 判定 |
| --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；`git status --porcelain` 仅 8 个未跟踪 docs 文件（方案 + R1/R2/R2-meta/R3/R4/R5/R6） | 同 | ✓ 一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = **`dfb77ed93c610165730a88f8483c80e78941d321`**；未提交 **8 项**（M `project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift`） | v6 已回填 `dfb77ed9…` + 8 项逐项枚举 | ✓ **完全一致**（F-23 闭合） |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | 同 | ✓ 一致 |
| 正式 runtime | PID 8303，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime` 监听 `*:8777`；构建身份 ≠ 源码 HEAD | 同 | ✓ 一致（不作产物身份推断） |

**锚点文件洁净性复核**（决定核验对象身份）：`ProjectionStore.swift`、`SessionProjectionWindow.swift`、`ProjectionWindowSpecTests.swift` 逐个 `git status --porcelain -- <file>` 实测**全部 clean**，即本轮全部 iOS 锚点核验对象 = 提交 `dfb77ed9` 内容，未被 8 项未提交修改污染。iOS 工作树配对：`git worktree list` 中该分支唯一工作树。全部未提交文件**只读未触碰**。

### 2.2 数字与日志复测（audit-plan 专项）

沿用方案 §1.1 声明的口径（UTF-8 字节、`ensure_ascii=False` + 默认分隔符）独立重算，与 v5 轮结论一致（本轮抽核，未重复全量三口径测量）：

| 项目 | 实测 | 判定 |
| --- | --- | --- |
| checkpoint | 34,537,988 B | ✓ |
| turns | 119 | ✓ |
| 整棵 projection | 34,427,361 B | ✓ |
| 最大单 turn | 6,347,762 B（index 1，不在尾 17） | ✓ |
| 尾 17 turns | 3,801,579 B；gzip6 = 1,084,899 B | ✓ |
| transcript | 101,520,809 B = 96.8178 MiB | ✓ |
| 8 次全量投递 | req_18/33/54/58/67/77/82/91，gen 1→5，17:30:56.864–17:33:52.331 | ✓ |
| E-2 window RPC = 0 / E-1 hydrate_failed = 0 | `grep -c` = 0 / 0 | ✓ |

### 2.3 F-21 闭合核验（本轮重点）

v6 §3.7「代价」行已重写为 R10 re-freeze 前置登记。逐项核对其引用锚点是否**准确指向会被 `scalarState` 打破的断言**：

| v6 引用 | 实测原文 | 会被 `scalarState` 打破？ | 判定 |
| --- | --- | --- | --- |
| Mac `projection_window_spec_test.go:46-57`（`frozenProjectionWindowFields`） | `:46 var frozenProjectionWindowFields = map[string][]string{` … `:54-56 "BridgeGetSessionProjectionWindowResult": {"window", "turns", "syncRev", "resume"}` | **是** | ✓ 准确 |
| Mac `:73-75`（fixture response keys） | `:73 if got, want := sortedRawKeys(response), []string{"resume", "syncRev", "turns", "window"}; !reflect.DeepEqual(got, want) {` | **是**（response 级断言） | ✓ 准确 |
| Mac `:198-211`（schema types DeepEqual） | `:196-197` 注释原文「catches accidental edits to bridge-v1.types.ts **without a re-freeze**」；`:204-210` `for typeName, wantFields := range frozenProjectionWindowFields { ... if !reflect.DeepEqual(fields, want) {` | **是** | ✓ 准确（v6 引用的注释文字逐字命中） |
| iOS `ProjectionWindowSpecTests.swift:129-130`（`frozenInterfaceFields`） | `:127 let frozenInterfaceFields: [String: Set<String>] = [` … `:130 "BridgeGetSessionProjectionWindowResult": ["window", "turns", "syncRev", "resume"],` | **是** | ✓ 准确 |
| iOS `:51`（v6 称「fixture response keys 集合相等断言」） | `:47 let response = try XCTUnwrap(fixture["response"] as? [String: Any])` / `:48 XCTAssertEqual(Set(response.keys), ["window", "turns", "syncRev", "resume"])` / `:50 let window = ...` / `:51 XCTAssertEqual(Set(window.keys), windowFields)` | **否** — `:51` 断言的是 **`window` 描述符**的 key（`windowFields` = `windowId/generation/coverage/headTurnId/tailTurnId/hasOlder/hasNewer/nextOlderCursor/nextNewerCursor`，`:16-19`），`scalarState` 落在 `BridgeGetSessionProjectionWindowResult` 层，不进 `window` 描述符 | 🟡 **行号错位**（见 F-24） |
| `bridge-v1.md:2328-2332`（R10） | `:2328 **R10 — Freeze scope.**` … `:2331-2332 "Open product semantics discovered later are BLOCKERS to be re-frozen here first; implementing agents MUST NOT guess them in code."` | — | ✓ 逐字精确 |

**结论**：v6 对 F-21 的**实质处置完全正确**——把「标准 additive」更正为 re-freeze 前置、五项批次内容、Gate A/S-1 前置、§8 风险行、OD-6 附带要求、「更新冻结物而非删除断言」原则，全部到位且经核验成立。**唯一瑕疵**是批次第⑤项把 iOS 侧的 response-level 断言错引为 `:51`（真正的是 `:48`）。这是**锚点精度问题，不是设计问题**，故降级为 F-24。

### 2.4 F-22 闭合核验

| v6 声明 | 实测 | 判定 |
| --- | --- | --- |
| `isProjectionWindowEligible()`（`ProjectionStore.swift:595-599`）只查 window echo 单维度 | `:596 func isProjectionWindowEligible() async -> Bool {` / `:597 guard let fetcher = fetcher as? any SessionProjectionWindowFetching else { return false }` / `:598 return await fetcher.supportsProjectionWindow` / `:599 }` | ✓ 精确 |
| 扩双维度经同一 `supportsAcknowledgedCapability` 机制加 key 即可（`CCCodeBridgeTransport.swift:723-725`） | `:723 func supportsAcknowledgedCapability(_ capability: String) -> Bool {` / `:724 isRegistered && acknowledgedCapabilities[capability] == true` / `:725 }` | ✓ 精确，机制通用，方案判断成立 |
| 四组合行为表 | 四格逐格给出 window echo / scalar echo / iOS 行为 / 执行态正确性，语义自洽；「旧 Mac + 新 iOS → 双维度不满足 → fail-closed 回 full」与 `pullOpenWindowed` 的 `guard ... else { return await pullFull(...) }`（`:764-766`）一致 | ✓ 成立 |
| 「新 Mac + 旧 iOS = 既有缺口、由 iOS 升级解决、不在 S-1 范围」 | 旧 iOS 只查 window echo → 走 window、收不到 `scalarState` → 执行态恒 idle。该形态**确实是 S-1 修复前的现状**（现状根本没走 window，故不是新引入的劣化） | ✓ 定性成立 |
| 服务端 echo 绑定的否决理由 | 「需额外修改 frozen v1 hello echo 条件、扩大再冻结范围；Mac+iOS 同仓同节奏发布、组合仅存在于升级窗口、行为不劣于现状」 | ✓ 理由成立且已记录为「已考虑并否决」 |
| §8 风险行更新 | `:694` 明确「新 Mac + 旧 iOS 为已知既有缺口形态，见 §3.7 组合表，不在 S-1 范围」 | ✓ 一致 |

**F-22 闭合。** 这是本轮质量最高的一处修订：不仅补了表，还把否决的替代方案与重新评估触发条件（「若 E-3 取证发现存量旧 iOS 已在实际走 window，重新评估」）一并写明。

### 2.5 F-23 闭合核验

v6 §2.1 iOS 行实测与 R6 报告完全一致（HEAD `dfb77ed9…`、8 项逐项枚举、漂移性质「纯 docs 单文件 +251 行」、三次实测一致注记、时点快照 + S-0 重生成义务）。**F-23 闭合。**

### 2.6 锚点抽核（历轮既有锚点维持）

按 R6 已全量亲核的锚点抽核复核（本轮抽 12 处高价值项）：`projection_window.go:77-82`（`ProjectionWindowResponse` 四字段）、`projection_window.go:24-26`（never-split）、`projection_window_handler.go:32/102-106/124-126`、`handlers_projection.go:122`、`projection_kernel.go:266-278/280/1123`、`main.go:560`、`server.go:373-395`、`projection_snapshot_fence.go:224/227/230-235/269-276`、`event_publisher.go:771-776/1726-1749/1802-1811`、`claude_stub_status.go:145-157`、iOS `ProjectionStore.swift:145/152/763-777`、`SessionProjectionWindow.swift:96-98/116-176`、Codex `codex-rs/cloud-client/README.md:18`——**全部维持命中**。F-20 五条断言按 v6 §10.11 的说明抽核（decide 门、两条入链、held 累积），维持 R6 的闭合结论。

### 2.7 覆盖声明

- **已实测核验**：三仓 + runtime 来源身份；方案全文哈希（开工 + 收工）；交接块正文哈希（idx=54514 精确命中）；§1.1 数字抽核；E-1/E-2 日志计数；F-21 六处锚点逐条比对（含发现 iOS `:51` 错位）；F-22 六项声明逐条比对；F-23 逐项比对；re-freeze 批次五项逐项可执行性；Mac/iOS 冻结样本**双副本**哈希比对（`6853e4ac…` 两侧相同，证实为独立副本而非共享文件）；「标准 additive」残留检查（仅 2 处以「更正对象」身份出现，无活断言）；§10.11 新增复核请求。
- **已独立推理（非命令实测，已标注）**：F-25 的「重试指标与协议冻结的观测缺口」——基于 §7.3 指标表与 §7.4 矩阵文本 + 既有日志字段（`projection_rpc stage=response_enqueue`）推得，未运行任何测试或采集新日志。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 精确构建 commit（未探 runtime 内部，S-0 职责）；transcript 逐行 type 分布（对本案结论无影响）；iOS 8 个未提交文件内容（按指令只读未触碰，均非锚点文件）；**未 build / 未 test**（硬边界），故 F-24 的「该测试会失败」是由「冻结字段集常量 + `DeepEqual` 断言 + 样本精确 key 断言」三条静态证据推得的必然性，非实跑结果。

## 3. 意见

### F-24 [建议] re-freeze 批次第⑤项把 iOS 侧 response-level 冻结断言错引为 `:51`，真正会被 `scalarState` 打破的 `:48` 全文未提；且两侧冻结样本是独立双副本，v6 未登记同步义务

- **位置**：§3.7「代价」行第 3 条（`ProjectionWindowSpecTests.swift:129-130` + `:51`（fixture response keys 集合相等断言））；批次内容第⑤项（「iOS spec 测试同步（`:51`、`:129-130`）」）；§7.2.1（「`ProjectionWindowSpecTests.swift` 冻结字段集（`:51`、`:129-130`）」）；§10.11①（「`ProjectionWindowSpecTests.swift:51/:129-130`」）——**四处一致地引用 `:51`**。
- **实测证据**（`OpenCodeiOS/OpenCodeiOSTests/ProjectionWindowSpecTests.swift`）：
  ```swift
  16  private let windowFields: Set<String> = [
  17      "windowId", "generation", "coverage", "headTurnId", "tailTurnId",
  18      "hasOlder", "hasNewer", "nextOlderCursor", "nextNewerCursor",
  19  ]
  ...
  47  let response = try XCTUnwrap(fixture["response"] as? [String: Any])
  48  XCTAssertEqual(Set(response.keys), ["window", "turns", "syncRev", "resume"])   // ← response 级，会被打破
  49
  50  let window = try XCTUnwrap(response["window"] as? [String: Any])
  51  XCTAssertEqual(Set(window.keys), windowFields)                                  // ← window 描述符级，不会被打破
  ```
  `scalarState` 落在 `BridgeGetSessionProjectionWindowResult` 层，**不进 `window` 描述符**，故 `:51` 在 re-freeze 后**不需要修改**，而 `:48` **必须修改**。对照 Mac 侧：v6 正确引用了 `:73-75`（response 级），未引 `:77-80`（window 描述符级）——**Mac 侧引用正确、iOS 侧错位，两侧不对称**。
- **另一项实测发现**：`docs/protocol/samples/projection-window-v1/window-response-spec.json` 在**两个仓库各有一份独立副本**：
  ```bash
  shasum -a 256 <MacBridge>/docs/protocol/samples/.../window-response-spec.json
  # 6853e4ace9152a8c0e1bb91cd4a96fe366ba42fa674dea96da777afe73731194
  shasum -a 256 <iOS>/docs/protocol/samples/.../window-response-spec.json
  # 6853e4ace9152a8c0e1bb91cd4a96fe366ba42fa674dea96da777afe73731194   ← 相同，但为两个文件
  ```
  iOS 测试经 `fixtureRoot`（`:34`）读**自己仓**的副本；Mac 测试经 `filepath.Join("..", "docs", ...)`（`:33`）读 MacBridge 仓副本。因此 re-freeze 批次第③项（「冻结样本新增含 `scalarState` 的样本条目」）**必须同时对两份文件执行**，否则 iOS 测试读旧副本仍会失败（`:48` 断言 response keys 恰为 `["window","turns","syncRev","resume"]`）。v6 的五项批次把③写成单数「冻结样本」，未区分双副本。
- **影响**：v6 的**设计处置正确**（re-freeze 前置、五项批次、Gate A 前置、不删断言），故不构成实施阻塞。但批次清单本身有两处不精确：① 第⑤项列了不需要改的 `:51`、漏了必须改的 `:48`；② 第③项未登记双副本同步。实施者若严格按文面执行，改完 `:51` 后 `:48` 仍会失败，正好重演 F-21 想防的「按方案做完仍撞上未登记失败测试」形态——只是这次范围小得多（一条断言、一份样本副本），实施者会自行发现并修好，故为建议级。
- **修订方向**：① 把四处 `:51` 更正为 `:48`（或写作 `:48`（response keys）+ `:129-130`（frozenInterfaceFields），并注明 `:51` 是 window 描述符断言、不受影响）；② 批次第③项改为「Mac 与 iOS **两份** `window-response-spec.json` 副本同步新增条目（实测当前两侧哈希相同 `6853e4ac…`，须保持同步）」；③ §7.1.4 的冻结断言更新项同步补 `:48`。
- **闭合标准**：re-freeze 批次清单的锚点与「必须修改的断言」一一对应，无多余、无遗漏；冻结样本的双副本同步义务显式登记。

### F-25 [建议] §7.3「单次打开重试次数 = 0」指标在 window 路径下的观测口径未定义，且与 re-freeze 引入的新协商维度存在耦合风险

- **位置**：§7.3 指标表「单次打开 full-snapshot / window_0 重试次数」行（目标 0 次）；§7.4 矩阵「每次打开记录重试计数」；§5 E-10；§7.2.8（`projection.hydrating` 重试节奏）。
- **实测证据**：
  - 事故期 8 次投递的日志可识别信号是 `projection_rpc stage=response_enqueue ... outcome=snapshot`（`go-bridge/handlers_projection.go:255` 附近 `logProjectionRPCTrace("response_enqueue", ..., "snapshot", ...)`）；**window 路径没有对应的 outcome 日志**——`projection_window_handler.go` 全文无 `logProjectionRPCTrace` 调用（`grep -n "logProjectionRPCTrace" go-bridge/projection_window_handler.go` 无输出），window 成功路径只经 `CompleteProjectionSnapshotWithHeldTurns` 完成，落日志的是 `[K4Patch] fence_begin/fence_complete`（`projection_snapshot_fence.go:239`）与 `[K4Patch] fence_begin`（`:120`），**不含 requestId、不含 window_0 方向**。
  - `fence_begin` 日志字段为 `sessionPrefix/cutRev/generation`（`projection_snapshot_fence.go:127-130`），`fence_complete` 为 `sessionPrefix/pending/invalidated/expectedRev`（`:239-243`）——两者都不带 `requestId`，因此**无法把一次 iOS 打开关联到其 window_0 请求**。
- **影响**：§7.3 要求「单次打开重试次数 = 0」并按 §7.4 逐场景记录计数，但 window 路径生效后（正是 S-1 的目标状态），日志里**没有可直接计数的 window_0 请求标识**。验收员只能靠 `fence_begin` 的条数间接推断，而 fence 同时被 full path 与 window path 共用（`handleGetSessionProjection` 也走 `BeginProjectionSnapshotWithResume`），存在**混淆风险**：一次 full 回退 + 一次 window 成功，与两次 window 重试，在 `fence_begin` 计数上可能不可区分。这与 §7.3 把重试次数定为硬指标（「最终成功但有 2–3 次重试不判通过」）的严格度不匹配。
  - **耦合风险**：F-22 要求 iOS 扩双维度 gate 后，「旧 Mac + 新 iOS」组合会 fail-closed 回 full path。该路径下每次打开会产生 **1 次 window 尝试（`.unsupported`）+ 1 次 full**，若验收口径不区分「typed fail-closed 回退」与「重试」，可能被误计为重试。方案未定义该区分。
- **修订方向**：① §7.3 该行补充观测口径：明确 window_0 请求的可计数信号（建议在 `projection_window_handler.go` 增加与 full path 对称的 `logProjectionRPCTrace("window_receive"/"window_enqueue", ...)`，带 `requestId`/`direction`），或改用 S-0 的 iOS 侧 trace 作为权威计数源；② 明确「fail-closed 回退 full」**不计入重试次数**（它是组合行为表定义的正常路径，非重试），并在 §7.4 矩阵注明；③ 若采①的日志增量，需说明其属 S-0 诊断范围、不改变行为（与 S-0「只保留诊断，不改变行为」一致）。
- **闭合标准**：§7.3 的重试指标有明确的、可区分 window/full 且可关联到单次打开的观测口径；fail-closed 回退与重试的计数边界明确。

## 4. 历轮意见闭合复核（本轮独立重判）

| 意见 | 级别 | 出处 | 本轮判定 |
| --- | --- | --- | --- |
| F-1 ~ F-9 | 2 阻塞 + 7 建议 | R1 | **全部闭合**（v2 起，本轮抽核维持） |
| F-10 iOS 枚举漂移 | 建议 | R2 | **闭合**（v6 已回填实测值，见 F-23 处置） |
| F-11 window 冷开缺 scalar | 阻塞 | R3 | **闭合**（设计层面；实施前置由 F-21 承接，v6 已登记） |
| F-12 SQLite 选型 | 阻塞 | R3 | **闭合**（bbolt/文件型，五项约束本轮抽核维持） |
| F-13 ~ F-16 | 4 建议 | R3 | **全部闭合** |
| F-17 | 阻塞 | R4 | **闭合** |
| F-18 / F-19 | 2 建议 | R4 | **闭合** |
| F-20 decide 路由维度 | 阻塞 | R5 | **闭合**（R6 已全量亲核五条断言；本轮按 §10.11 抽核维持） |
| **F-21 re-freeze 前置** | 阻塞 | R6 | **闭合**（实质处置全部成立；锚点精度瑕疵另立 F-24） |
| **F-22 peer 组合行为** | 建议 | R6 | **闭合**（四格表 + 双维度 gate + 否决理由 + 风险行，全部核验成立） |
| **F-23 iOS 来源回填** | 建议 | R6 | **闭合**（与实测逐项一致） |

**回归检查**：v6 改动集中于 §0/§2.1/§3.7/§4 OD-6/§5 Gate A/§6 S-1/§7.1.4/§7.2.1/§8/§10.11，本轮重读这些章节及全部交叉引用（§3.7 代价行 ↔ 批次五项 ↔ Gate A ↔ S-1 ↔ §7.1.4 ↔ §7.2.1 ↔ §8 风险行 ↔ OD-6 行 ↔ §10.11），**未发现修订引入的新矛盾或悬空引用**；架构方向、切片划分、存储选型、W1 定案、fence scalar 分支设计均未改动（与 §0 声明一致）。「标准 additive」旧定性仅在 §0.1 表与 §3.7 更正说明中**以「更正对象」身份**出现（实测 2 处），无残留活断言，与 v6 自述一致。

## 5. 已定案事项复核（本轮独立重判）

### 5.1 OD-6 = W1（维持）

独立重做三路线分析，无推翻依据：

- **W1**：零额外往返；scalar 随权威 page 经 replica 同构写入；冷开 running session 正确。v6 已把 re-freeze 成本显式登记（F-21），该成本属「实施前置」而非「路线劣势」。
- **W2**：多一次 RTT 直接挤压 R-2 的 ≤3s 预算（尤其叠加 `projection.hydrating` 重试的冷 Mac 场景），且需定义两次响应间的 patch 排序/syncRev 对齐，竞态面更大。维持淘汰。
- **W3**：`runtimeStateStore` 是 live 事件消费者，冷开「已在运行」的 session 无历史 live 事件可学——结构性不覆盖主场景；且固化执行态双源。维持淘汰。

**结论：W1 维持。** v6 新增的 OD-6 附带要求（「W1 交付批次必须含 R10 re-freeze，否则过不了既有测试门」）经核验**成立且必要**。

### 5.2 §3.2.2 存储选型（Mac=bbolt / iOS=文件型，维持）

五项约束本轮抽核（R4/R5/R6 已连续三轮亲核）：`go.mod`/`go.sum` 无 SQLite 驱动、`project.yml:53` 未设 `CGO_ENABLED`、`session_catalog.go:14-16` 既有立场、`import_guard_test.go:23-24` forbidden surface、iOS 零 SQLite 栈。**全部维持成立**。bbolt 仍是唯一同时满足「零 CGO + 小依赖 + 非 sqlite」的候选。**维持，无翻案依据。**

## 6. 剩余门

- **证据门**：E-3（window 未生效根因——S-1 修复前必须闭合；含 descriptor 维度）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时——S-5 完成宣告）；E-6（materialization 正确性——S-2 完成）；E-7（preview 不污染权威 writer——S-4 完成）；**E-10**（window 冷开 scalar 正确性——S-1 完成宣告）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。
- **S-1 开工前置（v6 已登记）**：E-1/E-2 + OD-6 定案 + F-20 修订（已完成）+ **F-21 re-freeze 批次**（含 F-24 的两处精度修正后更完整）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、数字抽核、F-21/F-22/F-23 逐条、re-freeze 五项可执行性、双副本核验、残留定性检查），全部亲核完成后才读 R4/R5/R6 报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与前轮差异**：R6 判 REVISION_REQUIRED（F-21 阻塞）；本轮确认 F-21 实质闭合，且 F-22/F-23 亦闭合。**本轮 0 阻塞、2 建议**，未判 APPROVED 的唯一原因是 F-24 涉及 re-freeze 批次的完整性——它是 F-21 想防的同一形态在更小尺度上的残留（方案自述的批次清单有一处指向错误断言、漏一处必须改的断言、漏双副本同步），修完即达 APPROVED 条件。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；逐锚点文件 `git status --porcelain -- <file>`；`python3` checkpoint 复算；`grep -c` 日志计数；`shasum -a 256` 双仓冻结样本比对；`sed -n`/`awk` 逐段读 `projection_window_spec_test.go`、`ProjectionWindowSpecTests.swift`、`projection_snapshot_fence.go`、`projection_window_handler.go`；`grep -n` 残留定性检查与 `logProjectionRPCTrace` 调用点检查。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 6 份报告、iOS 8 个未提交文件一律只读未触碰；未 build / 未 test / 未部署 / 未安装。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、字节测量、日志计数、fixture key 断言实测、双副本哈希比对）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: a4d360225a0c5747298728c297ad7a1108eae77357f8b08023c3522d623262ee
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r7.md
scope: full
blockers: 0
advisories: 2
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, F-21 re-freeze 批次(S-1 前置), S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
