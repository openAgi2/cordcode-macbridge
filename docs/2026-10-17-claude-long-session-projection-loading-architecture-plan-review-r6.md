# 评审报告 R6：Claude 长 session 打开架构优化方案（v5 第六轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R5 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v5**
方案 SHA-256（全文，开工实测）：`4840baac4a6cbc921d4d7fd8440911b586b58628102b15fc7484d74490f5cd63`——与派发记录一致
方案 SHA-256（收工复测）：`4840baac4a6cbc921d4d7fd8440911b586b58628102b15fc7484d74490f5cd63`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`f36328d54d216c385a81fd88c2b69240e60469c72c7e9fab3ef55f004e580b56`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`）实测命中，记账自洽
范围：full（全量第六轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **1**（F-21）
- advisories: **2**（F-22、F-23）
- **F-20 闭合判定：闭合。** v5 对 §3.7 fence 语义的 `decide` 路由维度修订与 §7.1.4 断言改写，经本轮逐点亲核与代码实际行为**完全一致**（五条断言全部命中，见 §2.3）。这是本方案历轮中质量最高的一次机制性修订：不仅改正了归因，还把「安全性来自发布链而非伴随性」这一因果讲清楚了。
- **OD-6 定案（W1）复核：维持。** 本轮独立分析 W1/W2/W3 三条路线，无推翻依据（§5）。
- **§3.2.2 存储选型（Mac=bbolt / iOS=文件型）复核：维持。** 五项约束本轮独立亲核全部成立（§5）。
- **与 R5 的关系**：R5 的唯一阻塞 F-20 已闭合；R5 的 OD-6/存储选型复核结论本轮独立复现成立。本轮阻塞 F-21 是**新核验面发现的既有约束缺失**——不是 F-20 修订引入的回归，而是方案在 W1 wire 增量上从未登记的协议冻结义务（与 R2→R3、R4→R5 同型的「方案没写的约束」盲区，本轮第三次出现于不同章节）。

**本轮自选新核验面（按指令要求声明）**：除复核 F-20 与全量锚点外，本轮新增三个此前六轮均未覆盖的核验面，并各自产出结论：

1. **W1 wire 增量的协议冻结义务**（F-21，阻塞）——新增 `scalarState` 字段会直接撞上仓内**已存在的硬性冻结断言**；
2. **W1 capability 的 fail-closed 声明在 iOS 现有分支结构下的可落地性**（F-22，建议）——方案声明「未收到 scalar echo 即不走 window」，但现有 `pullOpenWindowed` 的分支语义使该声明需要额外区分才能成立；
3. **§7.1.4 断言的测试基础设施可构造性**（F-23 的正面结论，无缺陷）——既有 `projection_snapshot_fence_test.go` 已具备 decide=true 构造能力，断言可写出确定性失败判据，此点确认成立。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 | 判定 |
| --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，`git rev-parse HEAD` = `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；`git status --porcelain` 仅 7 个未跟踪 docs 文件（方案 + R1/R2/R2-meta/R3/R4/R5） | `11caa133…` @ 同分支，docs 未跟踪 | ✓ 一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = **`dfb77ed93c610165730a88f8483c80e78941d321`**（方案记 `1e895ea9…`）；未提交 **8 路径**：M `CordCode.xcodeproj/project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift` | `1e895ea9…`，4 项时点快照（v3 已标注会漂移） | 🟡 **提交已漂移**，方案未回填（见 F-23） |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | 同 | ✓ 一致 |
| 正式 runtime | PID 8303，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime` 监听 `*:8777`（`lsof -nP -iTCP:8777 -sTCP:LISTEN`）；`strings` 含 `get_session_projection_window` ×2、`projection_window_scalar_v1` ×**0** | PID 8303，监听 8777，构建身份 ≠ 源码 HEAD | ✓ 一致（`projection_window_scalar_v1` 为 0 属预期：W1 尚未实施，不作产物身份推断） |

**iOS 漂移的性质判定（实测，非推断）**：

```bash
git diff --stat 1e895ea9ef42bc83c287c3acd66c3d7616340b4a..dfb77ed93c610165730a88f8483c80e78941d321
#  1 file changed, 251 insertions(+)
#  ...0-17-plan-review-loop-workflow-dsh-port-spec.md | 251 +++++
git diff --name-only 1e895ea9…..dfb77ed9… | grep -E "ProjectionStore|ProjectionReplica|SessionProjectionWindow|SessionProjection\.swift|CCCodeBridgeTransport|CCCodeBridgeBackendClient|ChatViewModel"
#  （无输出）
```

即：`1e895ea9 → dfb77ed9` 是**纯 docs 单文件新增**，本方案全部 iOS 锚点文件内容零变化。**锚点文件均不在 8 项未提交清单内**（逐文件 `git status --porcelain -- <file>` 实测全部 clean）。故本轮全部 iOS 锚点核验对象 = 提交内容，身份成立，不污染任何结论。

iOS 工作树配对：`git worktree list` 显示该分支仅 `cordcode-ios-native-message-timeline` 一个工作树（另有 4 个其他分支/游离工作树，与本案无关），配对唯一无歧义。全部未提交文件本轮**只读未触碰**。

### 2.2 事故样本与数字复算（audit-plan 专项）

按方案 §1.1 声明的口径（**UTF-8 字节、`ensure_ascii=False` + 默认分隔符**）独立重算，不使用历轮数字：

| 方案声明 | 本轮实测（命令输出） | 判定 |
| --- | --- | --- |
| checkpoint 34,537,988 B，mtime 2026-10-17 17:30:57 | `os.path.getsize` = **34,537,988** | ✓ 精确 |
| 119 turns | `len(proj["turns"])` = **119** | ✓ 精确 |
| 整棵 projection 约 34MB | **34,427,361 B**（默认分隔符）/ 34,336,858 B（紧凑） | ✓ 精确 |
| 最大单 turn 6,347,762 B，index 1/119，不在尾 17 内 | **6,347,762 B**（index **1**）；`idx >= len-17` → **False** | ✓ 逐字节精确 |
| 最大 turn Go 口径 6,336,926 B | 紧凑分隔符实测 **6,336,926 B** | ✓ 精确 |
| 最大 turn 字符数 5,284,620 | `len(json.dumps(turn))` = **5,284,620** | ✓ 精确 |
| 尾 17 turns 约 3.79MB | **3,801,579 B** | ✓ 精确 |
| 尾 17 turns gzip6 约 1.08MB | **1,084,899 B** | ✓ 精确 |
| gzip level 1 约 11.2MB / level 6 约 9.6MB | **11,142,266 B** / **9,602,913 B** | ✓ 精确 |
| transcript 101,520,809 B = 96.8 MiB | `ls -l` = **101,520,809**；`/1024/1024` = **96.8178** | ✓ 精确（「约 97MiB」表述正确，v4 的 F-19 口径修正有效） |
| 事故 8 次 `sinceRev=0 outcome=snapshot`，gen 1→5 | 全日志 grep 实测 **8 条**：req_18(gen1) / req_33(gen2) / req_54(gen3) / req_58(gen3) / req_67(gen4) / req_77(gen5) / req_82(gen5) / req_91(gen5) | ✓ **精确**（R3 记 4 次确为少算，v3 的 F-16 修正正确） |
| 时间窗 17:30:56–17:33:52 | 实测首 17:30:56.864、末 **17:33:52.331** | ✓ 精确到毫秒 |
| E-2：无 window RPC | `grep -c get_session_projection_window` = **0** | ✓ verified 成立 |
| E-1：无 hydrate_failed | `grep -c hydrate_failed` = **0** | ✓ verified 成立 |

**口径敏感性交叉验证**（确认 §1.1 声明本身正确）：同一 checkpoint 在 `ensure_ascii=True` 下整棵为 40,663,541 B（较声明口径虚高 18.1%）、最大 turn 7,940,153 B（虚高 25.1%）——与方案 §1.1 记录的数字完全一致，说明该节的口径声明是**实测得出而非估算**。

### 2.3 F-20 五条断言逐点核验（本轮重点一）

按指令给出的五个核验点逐条实测，**全部命中**：

| # | 指令给出的断言 | 本轮实测（文件:行号 + 原文） | 判定 |
| --- | --- | --- | --- |
| 1 | decide 门在 `projection_snapshot_fence.go:224`；清空（:230-235 四字段）与 held 判定（:227）只作用于 decide=true；decide=false 在完成事务投递循环（约 :269-276）原样投递、不判 held、零清空 | `:223 for i, frame := range fence.pending {` / `:224 if !frame.decide {` / `:225 continue` / `:227 if p.connHoldsAnyTurnLocked(... patchTurnIDs(frame.patch)) {` / `:230 resolved := frame.patch` / `:231-234 UpsertTurns/PartOps/TurnStateOps/Execution = nil` / `:235 fence.pending[i].patch = resolved`；投递循环 `:269 for _, frame := range fence.pending {` → `:271-275` 直接 `projectionPatchEvent(... frame.patch)`，**循环体内无 decide、无 held、无清空** | ✓ **完全一致** |
| 2 | 两条入队路径：常规 live patch 排 decide=false；decide=true 唯一来路 = `deferProjectionPatchForFenceLocked`（event_publisher.go:771-776）→ `PublishProjectionDetail` rule-4 分支（约 :1802-1811） | `grep -n "appendFencePatchLocked("` 全仓**仅 3 个调用点**：`:775`（`..., true`，在 `deferProjectionPatchForFenceLocked` 内）、`:826`（`..., false`，单连接链）、`:901`（`..., false`，广播链）。`deferProjectionPatchForFenceLocked` 全仓唯一调用点 = `:1810`，位于 `PublishProjectionDetail` 的 rule-4 分支（`:1802 if fence := p.projectionFences[key]; fence != nil && !fence.invalidated {` → `:1810 p.deferProjectionPatchForFenceLocked(...)`） | ✓ **完全一致，且是穷尽性证据**（3 个调用点全枚举） |
| 3 | held 累积：`recordConnWindowTurnsLocked`（event_publisher.go:1674-1687）连接级累积、older page 不清除；清除仅投递失败/超时/sync_invalidate | `:1674 func (p *EventPublisher) recordConnWindowTurnsLocked(...)` → `:1682-1686 for _, id := range turnIDs { ... p.projectionHeldTurns[conn][key][id] = struct{}{} }`（**逐个 set 插入，无 reset**）。清除点全枚举：`projection_snapshot_fence.go:249`（queue 溢出）、`:288`（resultDone 错误）、`:297`（写超时）、`:307-309`（`enqueueProjectionInvalidateLocked` 内 `delete(per, projectionDeliveryKey(...))` = sync_invalidate 路径）。**older 分支内无任何清除** | ✓ **完全一致** |
| 4 | Execution-only patch 现可达性：detail commit 构造点全含 TurnID → `patchTurnIDs` 恒非空 → decide=true 链无 Execution-only；真实 Execution-only 走 `claude_stub_status.go` → `ApplyExternalExecutionState`（零 turn op）→ `PublishProjectionPatch`（decide=false），不被清空 | `patchTurnIDs`（`:1726-1749`）只从 `TurnStateOps.TurnID` / `PartOps.TurnID` / `UpsertTurns.TurnID` 收集；`ApplyExternalExecutionState`（`projection_reducer.go:539-565`）只做 `SyncRev++`、`Execution = next`、`ps.execution = &next`，**零 turn op**；`flushLocked`（`:2799-2802`）在 `ps.execution != nil` 时置 `patch.Execution`，不含任何 turn 字段 → 该 patch 的 `patchTurnIDs` 为空 → 若它走 decide=true 会被清空，但它实际走 `PublishProjectionPatch` → `deliverProjectionPatchLocked`/`deliverSingleConnPatchLocked` → **decide=false**，故不被清空 | ✓ **完全一致** |
| 4b | `claude_stub_status` 是否为真实（非测试）路径 | `main.go:314-316`：`if isClaudeBackendID(id) { startClaudeStubStatusPoller(handlers, id) }`——claude backend 注册时**无条件启动**，是生产路径 | ✓ 成立（该样本可作回归护栏） |
| 5 | `PublishProjectionDetail` 现 9 处调用点 | 非测试调用点实测 **9 处**：`session_turn_items_handler.go:224/265/286/334`（4）+ `turn_detail_batch_engine.go:251/414/450/599/761`（5） | ✓ 精确 |

**F-20 结论**：v5 §3.7 的四段 fence 语义（held 累积 / decide 路由 / 常规链与 held 无关 / Execution-only 现不可达）与 §7.1.4 的双链断言，与代码实际行为**逐点一致**，v4 的「安全性来自 execution 变化通常伴随 turn ops」错误归因已被正确删除并替换为「安全性来自发布链（decide 路由）」。R5 要求的「三处归因修正」全部达成。

### 2.4 §7.1.4 断言可构造性（本轮重点一的第 5 点，正面结论）

- **双链断言可构造**：既有 `projection_snapshot_fence_test.go` 已具备完整构造能力——`:360-365` 用 `SetConnProjectionDeliveryMode(..., ProjectionDeliveryWindow)` 建立 window 连接、`:366-367 beginFence` 建立双 fence、`:379 publisher.PublishProjectionDetail(...)` 构造 decide=true 入队、`:385/:388 CompleteProjectionSnapshotWithHeldTurns` 分别以 `[]string{"T1"}`（命中）与 `[]string{"T2"}`（未命中）完成。**这正是 §7.1.4 断言 ② 所需的 held 命中/未命中对照，基础设施现成**。
- **负对照可观测性**：v5 明确指出「未协商 capability 的连接保持清空」只在 decide=true 路径可观测，且警告用常规链构造会「通过但未测到 scalar 分支」——该警告经本轮核实**正确且必要**（常规链 decide=false，`:224 continue` 使清空分支根本不可达）。这是一处高质量的测试设计判断。
- **`claude_stub_status` 样本可用性**：见 §2.3 第 4b 行，生产可达，可作回归护栏样本。
- **grep 型护栏可执行**：`PublishProjectionDetail` 调用点族可机械枚举（本轮即用 `grep -rn "PublishProjectionDetail(" go-bridge/*.go` 得出 9 处），护栏成立。

### 2.5 锚点核验（本轮亲核）

**F-20/v5 新增锚点（全部命中）：**

| 方案引用 | 实测 | 判定 |
| --- | --- | --- |
| `projection_snapshot_fence.go:210-236` | `:210-213` P0-2 注释；`:214-216` 注册；`:217-222` invalidated 清空 pending；`:223-236` decide 门 + held + 清空 | ✓ 精确 |
| `projection_snapshot_fence.go:214-216` | `if len(heldTurnIDs) > 0 && !fence.invalidated { p.recordConnWindowTurnsLocked(...) }` | ✓ 精确 |
| `projection_snapshot_fence.go:224` / `:227` / `:230-235` | 见 §2.3 第 1 行 | ✓ 逐字符精确 |
| `projection_snapshot_fence.go:247-249` / `:288` / `:297` | queue 溢出 `clearConnWindowTurnsLocked`、resultDone 错误 `ClearConnWindowTurns`、写超时 `ClearConnWindowTurns` | ✓ 精确 |
| `projection_snapshot_fence.go:269-276` | 投递循环，无 decide/held/清空 | ✓ 精确 |
| `event_publisher.go:764-817` | `:764-770` rule-4 deferral 注释；`:771-776` `deferProjectionPatchForFenceLocked`；`:778-817` `appendFencePatchLocked`（含 decide 参数文档 `:781-782`、`decide bool` `:786`、`:814` 入队） | ✓ 精确 |
| `event_publisher.go:1674-1687` | `recordConnWindowTurnsLocked` 逐个插入 | ✓ 精确 |
| `event_publisher.go:1726-1749` | `patchTurnIDs` | ✓ 精确 |
| `event_publisher.go:1769-1818` | `PublishProjectionDetail`（`:1769` 函数签名起）→ rule-4 `:1802-1811` | ✓ 精确 |
| `event_publisher.go:852-945` | `deliverProjectionPatchLocked` 广播链（`:852` 起） | ✓ 精确 |
| `event_publisher.go:901` | 广播链内 `appendFencePatchLocked(..., false)` | ✓ 精确 |
| `event_publisher.go:826` | 单连接链内 `appendFencePatchLocked(..., false)` | ✓ 精确 |
| `claude_stub_status.go:145-157` | `:146 func (p *claudeStubStatusPoller) syncProjectionExecution` → `:150 ApplyExternalExecutionState` → `:153 FlushProjectionPatch` → `:157 PublishProjectionPatch` | ✓ 精确 |
| `projection_reducer.go:539-565` | `ApplyExternalExecutionState`（`:539` 起，`:561-564` SyncRev++/Execution 赋值） | ✓ 精确 |
| `turn_detail_kernel_commit.go:52-62` | `:52 patches := make(...)`；`:59-63 ProjectionPatch{BaseRev, SyncRev, TurnStateOps: ops}`（**含 TurnID**） | ✓ 精确 |
| `turn_detail_batch_engine.go:239-252` | `:239-247 CommitTurnStateOpsV2([]TurnStateOp{{TurnID: turnID, ...}})`；`:251 PublishProjectionDetail` | ✓ 精确 |

**v3/v4 既有锚点（抽核维持）：** `projection_window.go:77-82`（`ProjectionWindowResponse` 四字段）、`projection_window.go:226-241`、`projection_window_handler.go:32/93-99/102-106/124-126`、`handlers_projection.go:122/915-954`、`projection_kernel.go:266-278/280/389-415/1030-1045/1123`、`main.go:560`、`server.go:373-395`、`backend_capabilities.go`（无 `projection_window_v1`）、`data_dir.go:15-23`、`hello_handler.go:108-111`、`runtime_startup.go:137-140`、`main.go:158`、`session_catalog.go:14-16`、`import_guard_test.go:23-24`、`MacBridge/project.yml:53`、iOS `ProjectionStore.swift:145/152/590/596-599/763-777`、`SessionProjectionWindow.swift:96-98/116-176`、`ChatViewModel.swift:1227-1233/2419-2426`、`ChatViewModel+TaskDock.swift:33/36-39`、`CCCodeBridgeBackendClient.swift:1308-1310`、`CCBridgeTransport.swift:723-725`、Codex `codex-rs/cloud-client/README.md:18` 等——**全部维持命中**。

### 2.6 覆盖声明

- **已实测核验**：三仓 + runtime 来源身份（P0 门）；方案全文哈希（开工 + 收工）；交接块正文哈希；§1.1 全部 12 项数字（独立复算，含三种编码口径交叉验证）；E-1/E-2 日志证据；F-20 五条断言（含穷尽枚举 `appendFencePatchLocked` 3 个调用点、`ClearConnWindowTurns` 全部清除点、`PublishProjectionDetail` 9 个调用点）；§7.1.4 可构造性（既有测试基础设施 + 生产可达性）；v5 新增全部锚点 + v3/v4 锚点抽核；§0 修订表逐条对应正文（F-20 行、OD-6 行、§10.8/§10.10 更新均实测命中）。
- **已独立推理（非命令实测，已标注）**：F-21 的「冻结断言会被新字段打破」——由「冻结字段集常量 + `reflect.DeepEqual` + fixture 精确 key 断言」三条实测证据推得必然失败，但**未实际运行测试**（受硬边界约束：不 build、不 test）；F-22 的分支语义分析——基于 `pullOpenWindowed`/`pullWindow` 源码逐行阅读推得，未运行 iOS 测试。
- **未核验及原因**：截图 heic（无视觉工具授权，且日志时间线已独立闭环）；生产 runtime 的精确构建 commit（未探 runtime 内部——方案已声明「构建身份 ≠ 源码 HEAD、日志结论需分层」，S-0 职责）；transcript JSONL 逐行 type 分布（以 checkpoint turn 级测量 + 文件字节数交叉印证，对本案结论无影响）；iOS 8 个未提交文件内容（按指令只读未触碰，且均非锚点文件）。

## 3. 意见

### F-21 [阻塞] W1 的 `scalarState` wire 增量未登记协议冻结义务，且会直接打破 Mac/iOS 两侧已存在的冻结字段硬断言

- **位置**：§3.7 W1 机制段（`ProjectionWindowResponse` 增加可选 `scalarState` 字段）；§3.7 「代价」行（「wire 变更需同步 protocol pack（Mac 权威 + iOS mirror）与定向测试；属标准跨仓协议增量（additive，不改 frozen v1 既有字段语义）」）；§6 S-1 组件列（「含 protocol pack additive 更新」）。
- **实测证据**（4 项，全部命令可复现）：
  1. **Mac 侧冻结字段集常量**——`go-bridge/projection_window_spec_test.go:46-57`：
     ```go
     var frozenProjectionWindowFields = map[string][]string{
         ...
         "BridgeGetSessionProjectionWindowResult": {
             "window", "turns", "syncRev", "resume",
         },
     }
     ```
     断言机制 `:198-211`：`extractInterfaceFields(...)` 后 `if !reflect.DeepEqual(fields, want) { t.Fatalf(...) }`。**新增 `scalarState` 到 `bridge-v1.types.ts` 会使该测试必然失败**；若不改 schema 只改 Go struct，则 `:205-209` 的解析仍通过但类型与 wire 脱钩（方案 §6 明写要「protocol pack additive 更新」，故必改 schema）。
  2. **Mac 侧 fixture 精确 key 断言**——`:73-75`：
     ```go
     if got, want := sortedRawKeys(response), []string{"resume", "syncRev", "turns", "window"}; !reflect.DeepEqual(got, want) {
     ```
     冻结样本 `docs/protocol/samples/projection-window-v1/window-response-spec.json` 实测 `response` keys = `['resume','syncRev','turns','window']`，`provenance = "synthetic-spec-fixture"`。一旦样本或响应加入 `scalarState`，此断言必然失败。
  3. **iOS 侧同类硬断言**——`OpenCodeiOS/OpenCodeiOSTests/ProjectionWindowSpecTests.swift:129-130`：
     ```swift
     "BridgeGetSessionProjectionWindowResult": ["window", "turns", "syncRev", "resume"],
     ```
     且 `:51 XCTAssertEqual(Set(window.keys), windowFields)` 对 window 描述符做**集合相等**断言（非子集）。
  4. **协议文档自身的 re-freeze 义务**——`docs/protocol/bridge-v1.md:2328-2332`（R10 — Freeze scope）：
     > 「Open product semantics discovered later are **BLOCKERS to be re-frozen here first**; implementing agents MUST NOT guess them in code.」
     W1 的 scalar state 交付正是「later discovered product semantics」，按 R10 必须先 re-freeze 协议章节，而不是按普通 additive 字段处理。
- **影响**：方案把 W1 定性为「标准跨仓协议增量（additive，不改 frozen v1 既有字段语义）」，这个定性**在事实层面不完整**：`scalarState` 确实不改变既有字段的**语义**，但它改变了 `BridgeGetSessionProjectionWindowResult` 的**字段集合**，而该字段集合被 Mac 测试、Mac 冻结样本、iOS mirror 测试三处**硬编码锁定**。按方案当前描述直接实施，S-1 一开工就会遇到三个必然失败的既有测试，而方案既没有登记「需同步更新这三处冻结断言」，也没有登记 R10 要求的 re-freeze 前置。实施者面对失败测试会面临两种都是错的走法：删改冻结断言（失去 schema 漂移防护）或放弃 `scalarState`（S-1 违反 R-6）。这正是 R2→R3、R4→R5 同型的盲区——方案只描述了自己要改什么，没核对「要改的东西被什么既有约束锁定」。
- **修订方向**：① §3.7 的「代价」行从「标准 additive」改为明确登记 **re-freeze 前置**：按 R10 先在 `docs/protocol/bridge-v1.md` §Projection Window 内新增 scalar 子章节并冻结 `scalarState` 形状（字段、必选/可选、与 `SessionProjection` 的镜像关系、nil 语义），再改 `bridge-v1.types.ts`；② 显式列出三处冻结断言（`projection_window_spec_test.go:46-57` 常量、同文件 `:73-75` fixture key、iOS `ProjectionWindowSpecTests.swift:129-130`+`:51`）并说明「随 re-freeze 一并更新为含 `scalarState` 的新冻结集」，而不是留待实施期临时处理；③ 冻结样本 `window-response-spec.json` 需新增 scalar 形态的样本条目（现只有一份 `provenance=synthetic` 样本，R10 纪律要求「MUST NOT guess them in code」，故样本必须先行）；④ 把 ①② 纳入 Gate A 或 S-1 前置条件，与 OD-6 定案同级。
- **闭合标准**：§3.7/§6 明确登记 re-freeze 前置与三处冻结断言的更新义务，且 §5 Gate A 或 S-1 前置条件体现该义务；不再使用「标准 additive」这一掩盖字段集合变更的定性。

### F-22 [建议] 「未收到 scalar echo 即不走 window」的 fail-closed 声明，在现有 iOS 分支结构下需要额外区分才能成立

- **位置**：§3.7 W1 旧 peer 兼容 bullet（「未收到 scalar echo 的新 iOS **不走 window**（fail-closed 回 full 路径，即 S-1 之前的现状）」）；§8 风险表 scalar 行（「fail-closed：未收到 scalar echo 即不走 window」）；§7.2.1（「hello capability echo 与 `supportsProjectionWindow`」）。
- **实测证据**：
  - `ProjectionStore.swift:763-777` `pullOpenWindowed`：`guard await isProjectionWindowEligible() else { return await pullFull(...) }`，随后 `pullWindow` 的 `.unsupported, .skipped` **也** `return await pullFull(...)`。
  - `:596-599 isProjectionWindowEligible()` 只查 `fetcher.supportsProjectionWindow`（= `transport.supportsAcknowledgedCapability("projection_window_v1")`，`CCCodeBridgeBackendClient.swift:1308-1310`）。
  - `pullWindow` 的错误映射 `:739-740`：`case "protocol.capability_required": return .unsupported`。
  - `CCCodeBridgeTransport.swift:723-725`：`supportsAcknowledgedCapability` 只对 `acknowledgedCapabilities[capability] == true` 返回 true；未知/未 echo 的 capability 返回 false。
- **影响**：方案要求「未收到 scalar echo 的新 iOS 不走 window」。当前 `isProjectionWindowEligible` 只有一个维度（window echo）。要实现该声明，必须新增「scalar echo」维度并让它也参与 gate。这本身可行（`supportsAcknowledgedCapability` 是通用的，加一个 key 即可），但方案没有写清以下**三个会产生不同行为**的组合，实施者需自行猜测：
  - **新 Mac + 新 iOS**：window echo=true、scalar echo=true → 走 window（正常）；
  - **旧 Mac + 新 iOS**：window echo=true、scalar echo=**false**（旧 Mac 不认识该 capability，不回显）→ 按声明应「不走 window」回 full。这条与 §2.4「旧 Mac 可继续 full path」一致，**行为正确但需显式实现**；
  - **新 Mac + 旧 iOS**：旧 iOS 不声明 scalar capability，新 Mac 不回显 scalar（按方案 echo 条件「客户端声明 + 服务端开关」）→ 旧 iOS 只查 window echo=true → 走 window 且**收不到 scalarState** → 旧 iOS 出现「window 生效但无 scalar」，即 §8 风险表要防的组合。方案说「不声明 scalar capability 的 iOS 收不到该字段（JSON additive，旧客户端忽略）」——但「旧客户端忽略该字段」与「旧客户端在无 scalar 的情况下仍走 window 且执行态恒 idle」是两个不同后果，方案把前者写成了兼容性结论，未评估后者。
- **修订方向**：① 在 §3.7 补一张三组合行为表（新/新、旧/新、新/旧），逐格写明「是否走 window」「scalar 来源」「执行态正确性」；② 明确旧 iOS（不声明 scalar）在新 Mac 下的预期：因旧 iOS 是本仓可控且已发布版本，需说明「旧 iOS 是否本来就是 §3.7 描述的缺口状态（即 S-1 前的现状）」——若是，则应显式声明「该组合的缺口是既有问题、由 iOS 版本升级解决，不在 S-1 范围」；③ §7.2.1 的测试项补「scalar echo 缺失时 `pullOpenWindowed` 回 full」的定向断言。
- **闭合标准**：三组合行为在方案中逐格明确，且 §7.2 有对应断言；不再用「旧客户端忽略该字段」代替「旧客户端在缺 scalar 时是否走 window」的结论。

### F-23 [建议] §2.1 iOS 行的提交哈希与未提交枚举仍未回填实测值（漂移持续，本轮为第 3 次记录）

- **位置**：§2.1 iOS 行（记 `1e895ea9…` + 4 项未提交）。
- **实测**：HEAD = `dfb77ed9…`（漂移），未提交 8 项（见 §2.1 表）。本轮已实测确认漂移为**纯 docs 单文件新增、全部锚点文件内容零变化**，因此**不影响任何已核命题**，故仍为建议级。
- **影响**：方案已按 R3 的 F-10 加了「时点快照 + S-0 重新生成」的标注，这是正确的处置方式，故不构成阻塞。但方案正文记录的哈希与实测差距已达 1 个提交，若后续轮次继续沿用同一段文字，会累积成「来源清单与实测不符」的记录负担。此外方案 §2.1 的未提交枚举（4 项）与实测（8 项）差异已在 R2/R3 两次报告指出，v3 只加了标注未更新枚举。
- **修订方向**：把 §2.1 iOS 行改为「实测于 2026-10-17（R6）：`dfb77ed9…`，未提交 8 项（逐项列出），其中与本方案相关的锚点文件均 clean」；或保持标注但把枚举更新为当前实测值并注明「R2/R3/R6 三次实测一致为 8 项」。
- **闭合标准**：§2.1 的提交与未提交枚举至少有一次与最新实测一致，或明确声明「本行永久保持写入时点快照，实测以 S-0 为准」并接受该行不再更新。

## 4. 历轮意见闭合复核（本轮独立重判）

| 意见 | 级别 | 出处 | 本轮判定 |
| --- | --- | --- | --- |
| F-1 preview cache key 与 epoch | 阻塞 | R1 | **闭合**（v2 起）。bridge ID/epoch 术语与状态机经本轮锚点抽核维持。 |
| F-2 R-1 与 never-split 偏差 | 阻塞 | R1 | **闭合**（v2 起，v4 补口径）。R-1 措辞与 `projection_window.go:24-26` 一致；6,347,762 B 本轮逐字节复现。 |
| F-3 重启 prefix 校验 IO | 建议 | R1 | **闭合**（v3 补 F-14 组合路径后）。§3.2.3 已覆盖「window 强制冷检 + append 后 catch-up」组合与 IO 上界。 |
| F-4 首屏目标冷/热 | 建议 | R1 | **闭合**。 |
| F-5 OD-2 与 S-1 门控 | 建议 | R1 | **闭合**。 |
| F-6 think.md 检索 | 建议 | R1 | **闭合**（§2.5 六条行号本轮抽核命中）。 |
| F-7 Codex 路径 | 建议 | R1 | **闭合**。 |
| F-8 测试矩阵两项 | 建议 | R1 | **闭合**。 |
| F-9 v2 与 turn_detail 关系 | 建议 | R1 | **闭合**。 |
| F-10 iOS 未提交枚举漂移 | 建议 | R2 | **未闭合但已降级处置**——v3 加了时点快照标注（可接受），本轮并入 F-23 继续记录。 |
| F-11 window 冷开缺 scalar | 阻塞 | R3 | **闭合**（v3 §3.7 + v4 定案 W1 + v5 F-20 修订）。设计层面闭合；**但 F-21 指出其实施前置（协议冻结）未登记**——这是 F-11 的**残余实施风险**，非 F-11 设计缺陷。 |
| F-12 SQLite 选型冲突 | 阻塞 | R3 | **闭合**（v3 §3.2.2 重写为 bbolt/文件型，五项约束本轮独立亲核成立）。 |
| F-13 capability 单门偏离 | 建议 | R3 | **闭合**（v3 登记 + v4 补收口实施项，§6 表下独立段落与 §10.10 均实测存在）。 |
| F-14 window 强制冷检 | 建议 | R3 | **闭合**（§3.2.3 + §7.3 指标行 + S-2 实施项）。 |
| F-15 字节口径 | 建议 | R3 | **闭合**（§1.1 口径声明 + 三种口径实测差异；本轮交叉验证其数字准确）。 |
| F-16 重试次数 | 建议 | R3 | **闭合**（v3 修正为 8 次，本轮实测 8 次精确；§7.3 指标行 + §9 完成定义）。 |
| F-17 fence「已知限制」归因错 | 阻塞 | R4 | **闭合**（v4 重写 + v5 补 decide 维度）。 |
| F-18 双门偏离无收口 | 建议 | R4 | **闭合**。 |
| F-19 数字口径标注残留 | 建议 | R4 | **闭合**（§1.1「约 97MiB」、6,347,762 B 双口径标注本轮实测准确）。 |
| F-20 decide 路由维度遗漏 | 阻塞 | R5 | **闭合**。五条断言全部亲核命中（§2.3），§7.1.4 断言可构造（§2.4）。 |

**回归检查**：v5 改动集中于 §0/§3.7/§4/§5/§6/§7.1/§10，本轮重读这些章节及全部交叉引用（§3.7 ↔ OD-6 ↔ Gate A ↔ S-1 ↔ §7.1.4 ↔ §7.4.8 ↔ §10.8），未发现修订引入的新矛盾或悬空引用；架构方向、切片划分、门结构未动（与 §0 声明一致）。

## 5. 已定案事项复核（可挑战项，本轮独立重判）

### 5.1 OD-6 = W1（维持）

本轮独立重做三路线分析，未找到推翻依据：

- **W1（additive 携带）**：零额外往返，scalar 随权威 page 经 replica 同构写入，冷开 running session 正确。**唯一新发现的实施成本是 F-21 的 re-freeze 与三处冻结断言更新**——但这属「已定案方案的实施前置」，不改变 W1 相对 W2/W3 的优劣排序。
- **W2（追加拉取）**：多一次往返直接挤压 R-2 的 ≤3s 预算（尤其叠加 `projection.hydrating` 重试的冷 Mac 场景），且需定义两次响应间的 patch 排序/syncRev 对齐，竞态面更大。维持淘汰。
- **W3（runtimeStateStore 单源）**：`runtimeStateStore` 是 live 事件消费者（`ChatViewModel.swift:1227-1233` 的 badge 写入来自 projection；`ChatViewModel+TaskDock.swift:36-39` 的 badge 读取来自 store），冷开一个「已在运行」的 session 时无历史 live 事件可学——结构性不覆盖本案主场景。维持淘汰。

**结论：W1 维持。** 附带要求：F-21 的 re-freeze 与冻结断言更新必须与 W1 同批交付，否则 W1 无法通过既有测试门。

### 5.2 §3.2.2 存储选型（Mac=bbolt / iOS=文件型，维持）

五项约束本轮独立亲核：

1. `go.mod`/`go.sum` 无 SQLite 驱动——**实测成立**（`grep "modernc.org/sqlite\|mattn/go-sqlite3" go.sum` 无输出；`go.mod` require 仅 toml/semver/webpush/circl/pty/websocket/compress/crypto）；
2. `MacBridge/project.yml:53` 未设 `CGO_ENABLED`——**实测成立**（该行为 `GOOS=darwin GOARCH="$GO_ARCH" go build -trimpath -ldflags ...`）；
3. `agent/grokbuild/session_catalog.go:14-16` 明确「避免加 CGO/纯 Go sqlite 依赖」——**实测成立**（原文逐字命中）；
4. `agent/opencode-web/import_guard_test.go:23-24` 把 `"sqlite"+"3"` 列为 forbidden surface——**实测成立**（原文命中；且该 guard 仅作用于 opencode-web 包，方案的「不触碰」判断准确）；
5. iOS 零 SQLite 栈——**实测成立**（`OpenCodeiOS/project.yml` 仅声明 MarkdownView/BorderBeamKit 两个 package；`grep -r "import SQLite\|GRDB"` 无输出）。

**bbolt 的判定（`go.etcd.io/bbolt`，纯 Go、MIT、无 CGO）在五项约束下是唯一同时满足「零 CGO + 小依赖 + 非 sqlite」的候选**，方案对 `modernc.org/sqlite`（正面推翻既有立场）、`mattn/go-sqlite3`（破坏交叉编译）、`sqlite3` CLI（核心路径不可 shell-out）、自研 append-only（patch 是 upsert 型，需重造 WAL）的淘汰理由本轮均核实成立。**维持。**

## 6. 剩余门

实施前仍未满足的门（pending 门不计入方案阻塞数）：

- **证据门**：E-3（window 未生效根因——**S-1 修复前必须闭合**；含 descriptor 维度）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时——S-5 完成宣告）；E-6（materialization 正确性——S-2 完成）；E-7（preview 不污染权威 writer——S-4 完成）；**E-10**（window 冷开 scalar 正确性——S-1 完成宣告）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4，OD-2 阻塞 S-3 协议冻结）。
- **本轮新增前置**：**F-21 的 re-freeze 与三处冻结断言更新义务**须在 S-1 开工前登记（与 OD-6 定案同级），否则 S-1 无法通过既有测试门。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、12 项数字、E-1/E-2、F-20 五点、§7.1.4 可构造性、三个自选新核验面），全部亲核完成后才读 R4/R5 报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与 R5 的差异**：R5 判 REVISION_REQUIRED（F-20 阻塞）；本轮确认 F-20 已闭合，但因**自选新核验面**发现 F-21（协议冻结义务缺失）而维持 REVISION_REQUIRED。分歧根因与前几轮同型——不是 R5 核错了事实，而是 R5 的核验面覆盖了 §3.7 的 fence 机制、未覆盖「该机制要落地的 wire 变更被什么既有测试/协议纪律锁定」。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`git worktree list`；`git diff --stat/--name-only 1e895ea9..dfb77ed9`；逐锚点文件 `git status --porcelain -- <file>`；`python3` checkpoint 复算（三种编码口径）；`grep -c` 日志计数与 `sed` 提取 8 次投递；`grep -n "appendFencePatchLocked("` / `ClearConnWindowTurns` / `PublishProjectionDetail(` 全枚举；`sed -n` 逐段读 fence / event_publisher / claude_stub_status / projection_reducer / 三个测试文件；`strings | grep -c` runtime capability 符号。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 5 份报告、iOS 8 个未提交文件一律只读未触碰；未 build / 未 test / 未部署 / 未安装。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项实际运行：checkpoint dump、三种口径字节测量、gzip 双级别、日志计数、fixture key 断言实测）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 4840baac4a6cbc921d4d7fd8440911b586b58628102b15fc7484d74490f5cd63
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r6.md
scope: full
blockers: 1
advisories: 2
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, F-21 re-freeze 前置, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
