# Claude 长 session 打开方案评审 R20（全量，fresh 模式）

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v24**
- 全文 SHA-256：`4262ffd502363e6f37325d7eb443c8c73fb989cd34910d4d4e9ce3b27a77bdc9`（与派发记录一致，评审对象身份确定）
- 正文 SHA-256：`681d3eb13529104d014c09db5a4a2a40f6564645c54414d26a403a77153be172`（按交接块切分规则 `raw[:idx].rstrip(\n)`、`idx = "\n---\n\n设计师交接块"` 本轮实测一致）
- scope: **full**（任务简报评审范围字段 = v24 全文 + §10.28 三条复核点 + R19 已闭合项回归 + 历轮 F-1~F-53 可追溯性）
- blockers: **1**（F-54，v24 修订引入的回归）；advisories: **2**（F-55/F-56）
- 评审员模式：fresh——先独立形成风险清单（死锁解除论证 / 自包含 cursor 单调覆盖 / F-53 两级 typed 链 / v24 修订全文同步性四条主线），再读 R19 报告核对闭合表述。R19 结论未作为本轮判断来源。
- implementation_readiness: R19 三条阻塞的**修订方向与核心文本**（§3.8 打包/续页规则、自包含 cursor、超限恢复语义、§3.4.2 状态机、OD-7、§3.9、E-11、held 注册）均已正确落地；但 §7.1.5 组合验收④ 与 §7.3 指标行两处活文本仍持 v23 口径（续页不含 turns / 同 cut），实现与测试无法同批判定（F-54）。修完 F-54 即达 APPROVED 条件（零其它阻塞）。S-7 仍受 E-3 证据门；OD-9 pending 不阻塞 S-2 默认档。

## 来源身份（G1：源码分析前重新生成）

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | `M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（= 待审 v24 本体）+ 未跟踪 r10–r19 评审报告与本可行性报告；任务简报指定来源 ✓ |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `da9fa03e1aa95fdc410d49d773bc741d04f7b8f6` | `M OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NarrativeRevealPacer.swift`、`M OpenCodeiOS/OpenCodeiOSTests/NativeTimelineSmoothHeightTests.swift`（2 项，均非方案锚点文件）；任务简报指定来源 ✓ |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897`（方案 §2.1 记录值；本轮未产生依赖 U 的新结论，未复核，如实登记） | 只读架构参考 |

与方案 §2.1 的差异登记：方案记 iOS 时点为 `dfb77ed9…` + 8 项未提交；R19 实测 `bb2f72a3…` + 4 项脏；本轮实测 `da9fa03e…` + 2 项脏。iOS 持续漂移属 native-timeline 开发线推进；方案 §2.1 已声明时点快照语义 + S-0 重生成义务，不构成方案缺陷。**本轮依赖的 iOS 锚点文件（BridgeWebSocketConnection / RelayBridgeFrameConnection / ProjectionStore / ProjectionReplica / SessionProjectionWindow / ChatViewModel）在 `bb2f72a3..da9fa03e` 的 path 限定 diff 为空（本轮实测），且均不在脏清单内**——R19 的 iOS 侧核验在本轮可按命题粒度复用。

本轮未构建、未安装、未部署、未运行测试、未修改方案与业务代码；唯一写入 = 本报告。

## 亲核锚点表

| 命题 / 方案原引用 | 实际来源及摘录（带行号） | 定位 / 语义 | 亲核或复用 |
| --- | --- | --- | --- |
| F-52：「`BeginProjectionSnapshot` 每次取当前 `kernel.Snapshot`（`projection_kernel.go:1887-1902`）」 | M `go-bridge/projection_kernel.go:1887` `func (k *ProjectionKernel) Snapshot(backendID, sessionID string)`，函数体经 `reducer.Snapshot(...)` 返回**当前**投影（约 :1887–1904）；调用点 `projection_window_handler.go:168` | 定位准确；语义支持——Snapshot 无 (cutRev, turnId) 历史版本，每次调用取当前行 | 本轮亲核（sed + grep 函数表） |
| F-52：「fence 完成即释放不跨请求持有（`projection_window_handler.go:168-210`）」 | M `projection_window_handler.go:168` `h.eventPublisher.BeginProjectionSnapshot(...)` 取 admission；成功路径 `:210` `CompleteProjectionSnapshotWithHeldTurns(conn, admission, ...)`、错误路径 `CompleteProjectionSnapshotError`（:192 附近）均释放 | 定位准确；语义支持——fence 生命周期 = 单请求内 | 本轮亲核 |
| held 注册（§3.8 / §7.1.5 F-34）：完成事务内注册、Completion failure 回滚 | 同上 `:201-211`：`turnIDs` 取自 `response.Turns`，经 `CompleteProjectionSnapshotWithHeldTurns` 注册，注释明写 "Completion failure rolls the set back"、"permanently strand the detail" | 定位准确；语义支持。**实施注**：现 `turnIDs` 只含 `response.Turns`，anchorRefresh（新字段）与 anchor turn 需按方案扩展注册——方案已登记（v16 F-34 + v23/v24 扩展），非缺陷 | 本轮亲核 |
| never-split 与上限常量（`projection_window.go:24-26` / `:27-31`） | M `projection_window.go:24-26` 注释 "…the bound limits accumulation and never splits a turn"；`:27-31` `maxWindowTurns=256` / `maxWindowEncodedBytes=4<<20` / `defaultWindowTurns=100` | 定位准确；语义支持（OD-7 数值类别对齐主张成立） | 本轮亲核 |
| newer 分支现状只能从 epoch 绑定 opaque cursor 进 + 排他切片（`projection_window.go:278-296`） | M `:277-294`：`case projectionWindowDirectionNewer:` → `decodeProjectionWindowCursor(params.Cursor)` + `validateProjectionWindowCursorScope`；`:290` `growWindowRange(sizes, limit, boundary+1, false)`，注释 "Strict turn-chain order (R7)" | 定位准确；语义支持——`boundary+1` = 严格后继（不含 anchor），方案 §3.8 `turns` 语义与此同源 | 本轮亲核 |
| locate 裸 turnId 锚定先例（`projection_window.go:297-301`） | M `:297-303`：`case projectionWindowDirectionLocate:` → `indexOfTurn(turns, params.AnchorTurnID)`，miss → `errProjectionWindowLocateOut` | 定位准确；语义支持——裸 turnId 锚定在冻结协议已有先例 | 本轮亲核 |
| F-53：iOS 逻辑消息 50MiB 硬顶（`BridgeWebSocketConnection.swift:12`） | I `BridgeWebSocketConnection.swift:12` `static let maximumMessageBytes = 50 * 1024 * 1024` | 定位准确；语义支持 | 本轮亲核（iOS @ da9fa03e） |
| F-53：relay 解压输出同受 50MiB（`RelayBridgeFrameConnection.swift:823/:859-862`） | I `RelayBridgeFrameConnection.swift:821-823` `RelayGzip.decompress(... maxOutputBytes: BridgeWebSocketContract.maximumMessageBytes)`；`:858-863` 超限抛 `relay.decompressed_too_large` | 定位准确；语义支持——gzip 不消解压上限 | 本轮亲核（同上） |
| §3.4.1：R1 冻结条款禁止跨 epoch 持久化 cursor（`bridge-v1.md:2266-2272`） | M `docs/protocol/bridge-v1.md:2266-2272`："…MUST NOT parse cursor contents, **persist cursors across bridge epochs**…" | 定位准确；语义支持——水位线 = 裸 tailTurnId 的依据 | 本轮亲核 |
| 方案正文 SHA-256（交接块声明 `681d3eb1…`） | 本轮 python 按切分规则实测 `681d3eb13529104d014c09db5a4a2a40f6564645c54414d26a403a77153be172` | 一致——版本级联与交接块同步成立 | 本轮亲测 |

## R19 意见闭合判定

**F-48（P1，死锁）——方向与核心文本闭合；v24 修订自身引入两处验收文本回归（本轮 F-54，见下）。**

- R19 闭合标准：增加 `46+1KiB / 46+9MiB / 42+6MiB` 验收；断言每页 ≤48MiB、链不跳、水位线最终前进、syncing 全到齐才清。→ v24 已交付：§3.8 打包 3（续页 = 内容页：turn phase 先于 correction phase、never-split 独占页、removedTurnIds/scalar/head/cursor 随页、turns 总页数计入超 2 页判定）+ §3.8 分页前进（事务内水位线 = 已交付链末端；46MiB+1KiB 由续页交付后前移）+ §7.1.5 后继 turn 交付验收①-③（三个形态与断言逐项对齐 R19 闭合标准）+ §3.4.2 状态机 / §3.9 / E-11 / OD-7 / held 注册同步。
- 独立推演死锁解除：46MiB anchor → 首包对账空间 = min(8, 48−2−46) = 0 → turns 进 correctionsPending → 续页 turn phase 交付 1KiB 后继（1+2=3 ≤48）→ 事务内水位线 = 该 turn → 下次请求 anchorRefresh 恢复正常体量。9MiB 后继走整 turn 独占页（9+2=11 ≤48）；42+6MiB 首包装 4MiB、6MiB 溢出续页独占。三形态均有 ≤48MiB 可实现页序列，链序由 `boundary+1` 排他语义 + R7 保证。**死锁判定成立、解除路径成立。**
- 但全文同步性 grep 发现两处 v23 口径活文本未随 v24 更新（详见 F-54）——按「实现与测试无法判定即阻塞」的历轮同型标准，F-48 的闭合在本轮被其修订自身的回归拖住，回归单列 F-54。

**F-52（P1，「同 cut」不可实现）——闭合。**

- R19 闭合标准（选自包含 cursor 方向时）：写清生成/持久化/寻址/容量/失效/typed stale 判定/崩溃恢复/测试；放宽为当前 head 必须重新证明无部分采用/覆盖。逐项对照 v24 §3.8「续页数据来源与绑定规则」：
  - 生成/寻址/容量/失效：复查集请求内嵌自包含，无持久化、无 TTL/去重问题（每次请求重建）——机制性免除 ✓；
  - 崩溃恢复：Mac 无 per-device 状态 + 判定输入全随请求携带 → 换连接/崩溃后重发同一 cursor 幂等 ✓；
  - typed stale 判定：anchor 离开保留区（+ epoch 变化——该分支的请求侧可执行性见 F-55 建议）✓；
  - 「放宽为当前 head 的重新证明」：单调覆盖一致性①-④给出（kernel 终态投影单调前进 + 已发项 token 复查 + 失效重发 upsert 覆盖 + 采用事务取最后一页 head + typed stale 整组放弃不发布部分状态）——本轮独立推演未找到反例：页间 live patch 由复查覆盖；事务中途 compaction 移除非锚点 turn 由复查集（removedTurnIds 语义，见 F-56 的显式化建议）；anchor 失效走 typed stale；head 身份由最后一页一次定;
  - 测试：§7.1.5 自包含 cursor 验收①-③（换连接重发幂等 / 两设备不串扰 / 跨 cut 复查 / anchor stale 中止）可构造 ✓；
  - 机制事实亲核：`kernel.Snapshot` 当前行无历史版本（:1887）、fence 完成即释放（`projection_window_handler.go:168-210`）、拟议 bbolt schema 无版本化 rows（§3.2.2，R19 已核、本轮未变）——「同 cut 不可实现」的事实基础成立，撤回是诚实处置。
  - 不变量 6 相容：全部判定输入随请求携带、Mac 零 per-device 状态 ✓；与现有每请求快照机制相容（复用而非新增）✓。

**F-53（P1，不可传输 full 冒充恢复成功）——闭合。**

- R19 闭合标准四项对照：① 显式行为与验收 → typed `full.oversized` 显式终态（§3.8 超限恢复语义 1）+ §7.1.5 full 超限验收（含「解压拒绝不得计入恢复成功」）✓；② 提高上限须同步 transport/memory 预算与测试 → `projection_max_bytes` hello 协商（direct 逻辑上限与 relay 解压上限同值提升）、内存峰值归 S-5、§8 风险表登记 ✓；③ `46MiB+1 turn + 其余内容 >50MiB` 反例 → E-11 full 超限反例 + 验收用例（构造 46MiB+1 turn 使 full >50MiB 默认档）✓；④ 禁止解压失败冒充回退成功 → 验收明文 ✓。
- 两级 typed 链（`reconciliation.oversized` → FallbackFull → `full.oversized`）在 §3.9 总表、R-2 双失败线、§8 两行（15s 时间线 / 字节线）三处口径一致（本轮交叉核对）✓。
- 「contingency `window_0` 不是恢复路径」论证成立：window 响应走同一 WebSocket、受同一逻辑上限（亲核 `maximumMessageBytes` 同时约束 direct 逻辑消息与 relay 解压输出），46MiB 级单 turn 对它同样不可传输——不引用是对的。

**R18 三处 + F-49/F-50/F-51 无回归**：47MiB 边界二义消除（§7.1.5 边界验收统一独占额度口径，与 §3.8/OD-7 一致）；anchorRefresh 仅首包（打包 2）维持；组合验收①-③/⑤ 与打包确定性维持（④的冲突子句见 F-54）；F-49 组合路径与 §7.1.2 限定（`:535`/`:916`）v22 口径在位；F-50 token 全 ops 终态（`:776`）+ §7.2.13 mixed-patch 测试在位；F-51 Mac restart 指标单行（`:985`）无重复。**未见回归。**

**历轮 F-1~F-53 可追溯性**：§0 修订历史 v1→v24 级联完整、各版全文 SHA-256 随行；抽查 v23 全文哈希 `ad1dc6a6…` 与 R19 报告记录一致 ✓。§0/§10 历史区的时代性「同 cut / 续页不含 turns」表述（`:4`/`:15`/`:26`/`:1101`）属既定保留纪律，不计为残留。

## 意见

### F-54 [阻塞]（v24 修订引入的回归）续页改内容页 + 撤回「同 cut」后，两处 v23 口径活文本未同步——验收断言与 §3.8 新规则直接冲突，实现与测试无法同批判定

- **位置 1**：§7.1.5 组合验收④（方案 `:936`）：「**修正页不重复 anchorRefresh 断言**：全部续页响应 **anchorRefresh/turns 字段为空或省略**，**head/cut 标识与首包一致（同 cut 续页）**；……构造续页 typed stale（**cut 失效**）→ FallbackFull」。
- **位置 2**：§7.3 指标表 anchor-newer 行（方案 `:987`）：「分层预算（**v23 F-48 终版**）……超限 correctionsPending 整 turn 粒度分页（**续页只含 changedTurns 剩余片**）」。
- **证据（与 v24 新规则的冲突）**：§3.8 `:793`「对账续页……**可携带 turns 剩余片**（turn phase……never-split 独占页）**与 changedTurns 剩余片**」；`:797`「**每续页独立取当前 committed head** 切片」；`:798`「采用事务……**采用最后一页的 head/scalar**——早页内容被晚页覆盖项以晚页为准」；OD-7 `:840` 已同步为「对账续页不携带 anchorRefresh、**可含 turns 剩余片（v24 F-48）**」。
- **影响**：实施者按 §3.8/OD-7 实现（续页含 turns、每页独立 head），测试按组合验收④断言「续页 turns 为空 + head/cut 与首包一致」必然失败；反向按断言实现则续页禁 turns——正是 R19 F-48 判定的死锁形态回归。§7.3 行「续页只含 changedTurns 剩余片」同型，且「v23 F-48 终版」标记已被 v24 修订取代（OD-7 行已改、此行未跟）。这与 R18 判 F-48 残留（「续页 anchorRefresh 携带规则未定义时合法组合不可实现」）属同一性质：文档内部二义使实现与测试无法判定。
- **修订方向（最小充分）**：① 组合验收④改写为「全部续页响应 anchorRefresh 字段为空或省略（turns 剩余片与 changedTurns 剩余片按 §3.8 turn phase → correction phase 出现，不属违例）；构造续页 typed stale 的语义对齐自包含 cursor 验收③（anchor 离开保留区 / epoch 变化），不再引用已撤回的 cut 概念」；② §7.3 行删「续页只含 changedTurns 剩余片」，续页形状对齐 v24（可含 turns 剩余片），版本标记改 v24 口径。§0 历史表与 §10.27 历轮评审项保留不改（时代性记录纪律）。
- **闭合标准**：下一轮全文活文本（§0/§10 历史区除外）grep 无「续页不含 turns / 只含 changedTurns / 同 cut 续页 / cut 失效」残留；§7.1.5 组合验收④与后继 turn 交付验收、自包含 cursor 验收②③ 在同一续页形状下可同批通过。

### F-55 [建议] 自包含 cursor 的 epoch 触发分支缺少请求侧可执行路径，宜在 re-freeze 冻结清单显式登记

- **位置**：§3.8 `:798` 单调覆盖④「anchor 离开保留区 / **epoch 变化** → typed stale」；对照自包含修正 cursor 的请求字段清单（`:796` 仅 `anchorTurnId` + 完整复查集）。
- **证据与影响**：Mac 每续页独立取当前 head，若续页请求不携带 epoch 身份，Mac 无从比对「epoch 变化」，该 typed stale 分支只能由 anchor 保留区判定或 iOS 侧感知（§3.4.2「epoch ≠ 当前：stale 标记升级，流程照常」）。正确性不受损——epoch 变化但 anchor 仍在保留区时，复查集 + 最后一页 head 收敛到当前 kernel 真值（本轮独立推演无反例）——但「epoch 变化 → typed stale」作为 Mac 侧分支的可执行性悬空，冻结时实施者需自行发明携带方式。
- **修订方向**：在 §3.8 自包含 cursor 段或 re-freeze 批次清单补一句：续页请求是否携带 bridgeEpoch（及 Mac 侧判定语义），或显式写明该分支由 iOS 侧判定 + 复查集兜底。
- **闭合标准**：冻结清单/正文显式覆盖，实施者无需自行发明。

### F-56 [建议] 复查集「已发项被移除」的处置未显式写出

- **位置**：§3.8 `:798` 单调覆盖②只写「已发项 token 失效（页间被 live patch 改动）→ 重发当前终态」；`removedTurnIds` 定义（`:783`）仅覆盖「baseDigests 中已不在 kernel 当前投影的 turnId」。
- **证据与影响**：事务中途 compaction 可移除非锚点 turn 而 anchor 仍在保留区（不触发 typed stale）——此时已发 turn 的正确处置是 removedTurnIds 而非重发。复查集语义上应同样适用 removedTurns 判定，但文本未连接两点，实施者可能只对未移除项做 token 比对。
- **修订方向**：在单调覆盖②补半句「已发项不在当前投影 → removedTurnIds（tombstone，iOS 从基底移除）」。
- **闭合标准**：正文显式覆盖该路径；可在自包含 cursor 验收②中附一个移除非锚点 turn 的构造。

## 复审处置与剩余门

| 原意见 | 本轮判定 |
| --- | --- |
| R19 F-48 | 方向与核心文本闭合（§3.8/§3.4.2/§3.9/OD-7/E-11/held/后继 turn 交付验收①-③ 全部落地）；v24 修订引入的验收文本回归单列为 F-54（阻塞） |
| R19 F-52 | 闭合（自包含 cursor 四要素 + 单调覆盖论证 + 验收可构造；机制事实亲核成立）；epoch 分支细化 → F-55 建议 |
| R19 F-53 | 闭合（两级 typed 链 + 协商 + 反例 + 验收；50MiB 双点亲核；§3.9/R-2/§8 三处口径一致） |
| R18 F-48 三处 / F-49 / F-50 / F-51 | 维持闭合，未见回归（组合验收④的冲突子句归 F-54） |

仍待证据/决定/授权门（与方案交接块一致，非本轮阻塞）：

- **证据门**：E-3（S-7 开工前）、E-4（anchor 页 wire 口径）、E-5、E-6、E-7、E-10、E-11（实施后定向测试 + 真机场景）；
- **决定门**：OD-9（growth-trust 档，pending owner 裁决；不阻塞 S-2 默认档 (a)）；
- **冻结门**：S-7 anchor-newer re-freeze 批次、S-8 `direct_gzip_v1` 冻结批次（`projection_max_bytes` 数值协商 wire 形态归此批次，见 F-55 同类）；
- **授权门**：S-6 owner 真机手动矩阵；UI automation / 真机操作未获授权（2026-10-10 agent-device 永久停用裁决）。

方案通过 ≠ 实施就绪：F-54 修复后方案层面可判 APPROVED，上述门仍按各自时点约束实施。

## 本轮动作边界声明

本轮仅写入本报告文件；未修改方案、业务代码、历轮报告；未运行 build/test；未构建、安装、部署或重启任何运行时；未触碰 iOS 工作树的未提交文件。方案正文与全文哈希在评审期间未变化（读取快照身份 = 派发身份）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 4262ffd502363e6f37325d7eb443c8c73fb989cd34910d4d4e9ce3b27a77bdc9
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r20.md
scope: full
blockers: 1
advisories: 2
open_gates: [E-3(S-7 开工前), E-4, E-5, E-6, E-7, E-10, E-11, OD-9(growth-trust 档，S-2 默认档不受阻塞), S-7/S-8 re-freeze 冻结批次, S-6 owner 真机矩阵授权]
round_complete: true
contract_version: plan-contract-v1.1
```
