# R24 通过结论定向复核（元审核）

日期：2026-10-18（Asia/Taipei）
复核对象：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24.md`（verdict: APPROVED）
复核范围（仅此两项，按派发指令）：(1) 锚点抽查——随机抽带行号引用逐行核对（行号与语义都必须吻合）；(2) 来源清单完整性（含配套工作树，无论是否实际引用）。
复核模式：只读（除本文件外未写入任何文件；未运行 build/test；未修改方案、报告、业务代码）。

## 结论

- **confirm = false**（发现 1 项问题，含两处子行号偏差）
- 问题性质：通过报告「亲核锚点表」F-52 行（r24.md:32）中两处子行号引用与实际文件偏 1 行——注释块引用 `199-204`（实际 200-205）与 `205: turnIDs := make(...)`（实际 206）。该行其余两个锚点（168、210）精确吻合，且全部语义声明在修正行号后均成立。
- 影响面评估：受影响行是 F-52 机制事实锚点，其语义结论（fence 单请求生命周期、held 只注册 `response.Turns`）经本轮在正确行号亲测全部成立——**该缺陷不动摇 R24 的技术判定**，但按派发指令「行号与语义都必须吻合」「发现任何问题即 confirm=false」处置。
- 方案身份：`shasum -a 256` 实测 = `dfafe1c6ceeb96ea86e284aeebe8c8bb712d47bb6b619c6e611ced5892ee8bbd`，与派发哈希及 r24 报告声明一致（评审对象身份确定，本轮复核同一快照）。

## 一、锚点抽查（范围 1）

从通过报告随机抽取 11 组带行号引用（≥3 处要求），跨 M 仓 4 文件、iOS 仓 3 文件、Codex 上游 1 文件、方案文档 10+ 行、r23 报告 1 行与 r23-meta 交叉引用，逐行核对：

| # | 报告引用（r24.md 行号） | 实际核对结果（本轮命令） | 判定 |
| --- | --- | --- | --- |
| 1 | r24:33 `server.go:780` = `ack.BridgeEpoch = s.bridgeEpoch`；`main.go:640` = `ack.BridgeEpoch = bridgeEpoch` | `awk 'NR==780'`/`awk 'NR==640'` 逐字一致 | ✓ |
| 2 | r24:34 I `BridgeWebSocketConnection.swift:12` = `static let maximumMessageBytes = 50 * 1024 * 1024` | awk :12 逐字一致（工作树正式路径 `OpenCodeiOS/OpenCodeiOS/Services/Bridge/`） | ✓ |
| 3 | r24:34 I `RelayBridgeFrameConnection.swift:823` = `maxOutputBytes: Int = BridgeWebSocketContract.maximumMessageBytes`；`:859-862` guard + `"relay.decompressed_too_large"` | awk :821-824/:857-863：823 与 859-862 逐字一致 | ✓ |
| 4 | r24:35 I `SessionProjection.swift:696` = `var revisionKey: String { "\(bridgeEpoch ?? "")|\(syncRev)" }` | awk :696 逐字一致 | ✓ |
| 5 | r24:38 M `projection_window.go:24-26` never-split 注释；`:28-30` 三常量 256 / 4<<20 / 100 | awk :20-35：注释 24-26、常量 28-30 全部一致 | ✓ |
| 6 | r24:38 M `projection_window.go:77-82` `ProjectionWindowResponse` 字段；`:278-301` newer/locate 分支 | awk+grep：字段 Window/Turns/SyncRev/Resume 在 78-81（struct 声明行 77，引用区间 78-82 覆盖全部被引字段）；`decodeProjectionWindowCursor`@279、`growWindowRange(sizes, limit, boundary+1, false)`@290、`"Strict turn-chain order (R7)"`@291、`indexOfTurn`@286/:296、`errProjectionWindowLocateOut`@300 均在引用区间内 | ✓ |
| 7 | r24:32 M `projection_window_handler.go:168` = `proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(`；`:210` = `CompleteProjectionSnapshotWithHeldTurns(...)` | `grep -n`：168、210 逐字一致 | ✓ |
| 8 | r24:32 M 同行：「`199-204` 注释 "…permanently strand the detail…Completion failure rolls the set back."」 | `grep -n`：注释块实际 **200-205**；被引句尾「Completion failure rolls the set back.」在 **205**（引用区间外），199 为空行 | **✗（问题 1-a）** |
| 9 | r24:32 M 同行：「`205: turnIDs := make(...)`」 | `grep -n 'turnIDs := make'` → **206**（205 为注释行） | **✗（问题 1-b）** |
| 10 | r24:40 U `thread_history_materialization.rs:42-45` = `let start_offset = projection_state.as_ref().map_or(0, \|state\| state.next_byte_offset);` | find+awk :40-48：语句在 43-45（引用区间 42-45 覆盖），引文逐字一致 | ✓ |
| 11 | r24:30/:56/:57 勘误三事实（r23.md:23 记录 `…cd55e237…` + 「本轮实测一致」；实际 main = `…cd555237…`；报告值 cat-file 不存在） | awk r23.md:23 逐字确认记录值与声明；`git -C /Users/jacklee/Projects/codex rev-parse main` = `ce0323f2f2d3b25e82806cd555237da3db4e1897`；`cat-file -t ce0323f2f2d3b25e82806cd55e237da3db4e1897` → `fatal: could not get object info`（exit 128）——三项全部复现 | ✓ |
| 12 | r24:49/:50/:85 方案 `:826`（anchorRefresh 仅对账首包携带）/`:827`（changedTurns 复查集口径 + v28 措辞同步注记）/`:828`（removedTurnIds 复查集中任何项）/`:841`（续页请求携带 anchorTurnId + 完整复查集）/`:843`（单调覆盖② 复查集内全部项）/`:838`（打包 3 不携带 anchorRefresh） | awk 逐行 + `grep -o` 短语级核对：全部在位 | ✓ |
| 13 | r24:30/:57 方案 `:398`（§2.1 Codex 行记录实际值 + 更正注记）；`:980-:984`（预算边界/组合验收④ anchorRefresh 收窄/:982 不回填切页即止/:983 自包含 cursor 验收/:984 full 超限验收） | awk+grep -o：:398 记录 `main@…cd555237…`（= 实际值）；:981 含「④ …全部续页响应 anchorRefresh 字段为空」；:982 含「不回填、切页即止」；:983/:984 主题与内容一致 | ✓ |
| 14 | r24:58 「r23-meta 锚点抽查 11/11 通过」 | Read r23-meta.md §一：「11/11 通过，未发现行号或语义偏差」——引用属实 | ✓ |

**锚点抽查结论：14 组中 13 组吻合；1 组（r24:32 F-52 行）内两处子行号偏 1 行（见问题 1）。**

## 二、来源清单完整性（范围 2）

### 2.1 报告来源表逐项核验（本轮实测命令与结果）

| 报告记录（r24.md:20-22） | 本轮实测 | 判定 |
| --- | --- | --- |
| M = `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，`feat/ios-native-message-timeline`，`f868af18bbbbff6e7f47994c11fb76b49406b33e`，脏清单 = 方案 M + 未跟踪评审报告与 feasibility 报告，源码不在脏清单，任务简报指定 f868af18 或更新 | `git branch --show-current`/`git rev-parse HEAD`/`git status --porcelain` 全部一致（脏清单仅 docs/，go-bridge/MacBridge 零修改）；「6 个 Mac 工作树、本分支唯一对应本工作树」经 `git worktree list` 复现（恰 6 项，`feat/ios-native-message-timeline` 唯一） | ✓ |
| I（配套工作树）= `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，`feat/ios-native-message-timeline`，`7527e1ead184fa88c58b05ef86839b7ba6489009`，干净 | branch/commit 逐字一致；`git status --porcelain` 为空（干净）；`git worktree list`（iOS 仓 7 工作树）中该分支唯一对应此路径——**配套工作树已覆盖且身份正确** | ✓ |
| U = `/Users/jacklee/Projects/codex`，`main`，`ce0323f2f2d3b25e82806cd555237da3db4e1897`（本轮实测；R23 误写值已由 v28 勘误） | `git rev-parse main` = `ce0323f2f2d3b25e82806cd555237da3db4e1897` 逐字一致；R23 误写值 `…cd55e237…` `cat-file -t` → fatal（对象库不存在）——**U 行哈希为有效实际值**（r23-meta 问题 1 已在 v28 勘误 + 本报告登记中正确修复） | ✓ |
| 方案全文哈希 = `dfafe1c6…`（= 派发记录） | `shasum -a 256` 一致 | ✓ |

### 2.2 覆盖完整性判定

任务/方案涉及的仓库与工作树组合共三组：Mac 功能工作树（M）、配套 iOS 工作树（I）、Codex 上游（U）。r24 G1 表三组**全部在列**（配套工作树 I 实际被引用且在列；U 经 v28 勘误后身份有效）。`任务预期分支` 在 M 行记录（「任务简报指定来源（f868af18 或更新）✓」）。`预期产品特性` 字段不适用：本轮为纯方案评审（未构建、未安装、未部署，r24 自述与锚点表均未引用任何构建产物或运行时证据），与 r23-meta §2.2 对评审轮的同一裁量一致（`/Applications` 运行时不是仓库/工作树，评审未引用其证据即不要求登记）。

**来源清单覆盖完整性：通过（无缺失组合、无无效身份）。**

## 三、发现的问题

### 问题 1（锚点行号精确性）：r24.md:32（亲核锚点表 F-52 行）两处子行号与实际文件偏 1 行

- **证据链**（全部本轮实测，文件为提交内源码、工作树源码零修改，评审时点与现在内容一致）：
  1. r24.md:32 引用「`199-204` 注释 "…permanently strand the detail…Completion failure rolls the set back."」——`grep -n` 实测：该注释块为 **200-205**（`// turn_detail_lazy_v1 rule 3/4…`@200 … `// behind the stale response. Completion failure rolls the set back.`@205）；被引句尾在 205（引用区间之外），199 为空行。
  2. r24.md:32 引用「`205: turnIDs := make(...)`」——`grep -n 'turnIDs := make'` 实测 = **206**；205 是注释行。
  3. 同行其余引用精确：`168: BeginProjectionSnapshot(` ✓、`210: CompleteProjectionSnapshotWithHeldTurns(...)` ✓（grep -n 双确认）；「held 注册（`:201-211`）」区间覆盖实际代码块（206-210）。
- **规则依据**：派发指令——锚点抽查要求「行号与语义都必须吻合」；发现任何问题即 confirm=false。
- **影响限定**：该行全部语义声明（fence 生命周期 = 单请求内、held 现状只注册 `response.Turns`、注释内容原文存在）在修正行号后均成立，本轮已在正确行号亲测复核；受影响的是引用精确性而非技术结论。修复 = 将 r24 该行两处子行号更正为 200-205 与 206（或加勘误注记）。

## 四、本轮动作边界

除本复核报告外未写入任何文件；全部命令为只读（`shasum` / `git rev-parse` / `git branch --show-current` / `git status --porcelain` / `git worktree list`（双仓）/ `git cat-file -t` / `grep -n` / `awk` / `sed -n` / `find` / Read）；未运行 build/test；未触碰方案、历轮报告、业务代码与运行时。

复核员交接块：

```text
confirm: false
issues: 1（r24.md:32 F-52 锚点行两处子行号偏 1 行：注释块 199-204→实际 200-205、turnIDs := make 205→实际 206；同行 168/210 精确，语义在修正行号后全部成立；来源清单覆盖完整性通过）
scope: 定向复核（锚点抽查 + 来源清单完整性）
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24-meta.md
```
