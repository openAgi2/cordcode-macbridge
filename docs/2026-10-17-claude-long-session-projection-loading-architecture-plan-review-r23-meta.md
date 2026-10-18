# R23 通过结论定向复核（元审核）

日期：2026-10-18（Asia/Taipei）
复核对象：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23.md`（verdict: APPROVED）
复核范围（仅此两项，按派发指令）：(1) 锚点抽查——随机抽带行号引用逐行核对；(2) 来源清单完整性。
复核模式：只读（除本文件外未写入任何文件；未运行 build/test；未修改方案、报告、代码）。

## 结论

- **confirm = false**（发现 1 项问题）
- 问题性质：通过报告来源清单中 Codex 上游（U）的完整提交哈希为无效值（对象库中不存在的 commit），且该行「= 方案 §2.1 记录值，本轮实测一致」的一致性声明与报告自身转写的字符串不符。
- 影响面评估：U 为只读架构参考，报告自述「本轮未产生依赖 U 的新结论」，且本轮锚点抽查未涉及 U 依赖的结论——**该缺陷不动摇 R23 的任何技术判定（锚点抽查全部通过）**，但按 P0 来源门「路径 + 分支 + 完整提交是不可拆分的来源身份」与派发指令「发现任何问题即 confirm=false」处置。

## 一、锚点抽查（范围 1）

从通过报告随机抽取 11 处带行号引用（≥3 处要求），逐行核对实际文件，行号与语义**全部吻合**：

| # | 报告引用 | 实际核对结果 | 判定 |
| --- | --- | --- | --- |
| 1 | M `go-bridge/server.go:780` = `ack.BridgeEpoch = s.bridgeEpoch` | Read server.go:780 逐字一致；:781 注释证实 direct 路径（报告称「iOS 侧 epoch 判定在 direct/relay 两路径均可执行」语义成立） | ✓ |
| 2 | M `go-bridge/main.go:640` = `ack.BridgeEpoch = bridgeEpoch` | Read main.go:640 逐字一致；:641 注释证实 relay 路径 | ✓ |
| 3 | M `go-bridge/projection_window.go:24-31`（never-split 注释 24-26；`maxWindowTurns=256`/`maxWindowEncodedBytes=4<<20`/`defaultWindowTurns=100` 28-31） | Read projection_window.go:20-33：注释与三个常量值、行号全部一致 | ✓ |
| 4 | I `BridgeWebSocketConnection.swift:12` = `static let maximumMessageBytes = 50 * 1024 * 1024` | Read `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeWebSocketConnection.swift:12` 逐字一致（工作树 @7527e1ea，见来源核验） | ✓ |
| 5 | 方案 `:816` changedTurns 定义含「baseDigests 中与当前内容 digest 不一致或 token 为 null（失效，必须回传）……覆盖离线期间对缓存范围内旧 turn 的修改」 | sed -n 816p 逐字吻合——F-60 建议的前提（「baseDigests 中」限定词仍在）成立 | ✓ |
| 6 | 方案 `:830` 自包含修正 cursor：「完整复查集 = 原始 baseDigests 全部项 ∪ 已发项的 (turnId → 客户端已收 token)（含首包 anchorRefresh 的 turn 与已交付 turns）——复查集不随页收缩」 | sed -n 830p 逐字吻合（F-59 闭合判定引用属实） | ✓ |
| 7 | 方案 `:832` 单调覆盖②「Mac 对复查集内全部项比对当前 digest……无论该项是否已在早页确认→本页重发当前终态」及④ epoch 判定主体（内嵌 `server.go:780`/`main.go:640` 锚点） | sed -n 832p 吻合；②的 v27 扩展措辞与④的 anchor=Mac / epoch=iOS 划分均在 | ✓ |
| 8 | 方案 `:972` 验收①「携带完整复查集（原始 baseDigests ∪ 已发项 token……）」+ 验收②含 v27 补 F-59 构造「早页复查命中（确认无修改）的基底项在续页请求前被 live patch 修改→续页重发新终态」 | sed -n 972p 逐字吻合（两构造均在） | ✓ |
| 9 | 方案 `:1155` 交接块 open_gates 含「S-7/S-8 re-freeze 批次(含 F-59 全集复查集请求字段形状冻结)」 | sed -n 1155p 吻合 | ✓ |
| 10 | 方案 `:817` removedTurnIds「复查集中任何项被移除同经本字段返回……（无论已发/未发/早页确认）的正确处置是 tombstone 而非重发」 | sed -n 817p 吻合（R20 F-56 维持闭合的引用属实） | ✓ |
| 11 | 方案 `:1063-1064` §8 两行（触顶 15s 时间线 / `projection_max_bytes` 字节线双失败线） | sed -n '1063,1064p' 吻合（F-53 维持判定的引用属实） | ✓ |

**锚点抽查结论：11/11 通过，未发现行号或语义偏差。**

## 二、来源清单完整性（范围 2）

### 2.1 报告来源表逐项核验（本轮实测命令与结果）

| 报告记录 | 本轮实测 | 判定 |
| --- | --- | --- |
| M = `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，`feat/ios-native-message-timeline`，`f868af18bbbbff6e7f47994c11fb76b49406b33e`，脏清单 = 方案 M + 未跟踪 r10–r22 报告与 feasibility 报告，go-bridge/MacBridge 源码不在脏清单内 | `git rev-parse HEAD` = `f868af18bbbbff6e7f47994c11fb76b49406b33e`；`branch --show-current` = `feat/ios-native-message-timeline`；`status --porcelain` 与报告一致（M 方案 + 未跟踪 r10–r23 与 feasibility 报告；r23 为报告自身，评审时点尚未存在，不构成偏差；源码目录零修改属实） | ✓ |
| 「已枚举全部 6 个 Mac 工作树，本任务唯一对应 native-message-timeline 工作树」 | `git worktree list` 输出恰 6 项，`feat/ios-native-message-timeline` 仅 native-message-timeline 一处 | ✓ |
| I = `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，`feat/ios-native-message-timeline`，`7527e1ead184fa88c58b05ef86839b7ba6489009`，工作树干净 | `git rev-parse HEAD` = `7527e1ead184fa88c58b05ef86839b7ba6489009`；branch 一致；`status --porcelain` 为空（干净）；`git worktree list`（iOS 仓，7 工作树）中 `feat/ios-native-message-timeline` 唯一对应该路径——**配套工作树已覆盖且身份正确** | ✓ |
| U = `/Users/jacklee/Projects/codex`，`main`，`ce0323f2f2d3b25e82806cd55e237da3db4e1897`（= 方案 §2.1 记录值，本轮实测一致） | `git rev-parse main` = **`ce0323f2f2d3b25e82806cd555237da3db4e1897`**；报告哈希 `…cd55e237…` 经 `git cat-file -t` 实测**在对象库中不存在**（could not get object info）；方案 §2.1（:387）记录的是实际值 `…cd555237…` | **✗（见问题 1）** |
| 方案哈希 = `54e82035…f891e`（派发记录一致） | `shasum -a 256 <plan>` = `54e8203544385c50845a203a677386f7825b3b2c4a512f3f6ac4a1f1992f891e`，与派发哈希一致 | ✓ |

### 2.2 覆盖完整性判定

任务/方案涉及的仓库与工作树组合共三组：Mac 功能工作树（M）、配套 iOS 工作树（I）、Codex 上游（U）。报告 G1 表三组**全部在列**（含配套工作树，即使本轮结论未实际依赖 I/U 新结论也已登记）。方案 §2.1 第四行「正式 Mac runtime」（`/Applications` 运行时 PID/端口）不是仓库/工作树，且 R23 未引用任何运行时日志或构建产物证据，P0 来源门按「每一个涉及的仓库和工作树」衡量不要求评审报告登记该行——不构成缺失。

**覆盖完整性通过；唯一缺陷是 U 行的完整提交值为无效哈希（正确性缺陷，见下）。**

## 三、发现的问题

### 问题 1（来源清单正确性）：U 行完整提交哈希为不存在于对象库的无效值，且「实测一致」声明与自身转写不符

- **证据链**（全部本轮实测）：
  1. 报告 r23 第 23 行（来源表 U 行）记录提交为 `ce0323f2f2d3b25e82806cd55e237da3db4e1897`，并注「（= 方案 §2.1 记录值，本轮实测一致）」。
  2. `git -C /Users/jacklee/Projects/codex rev-parse main` 实际输出 `ce0323f2f2d3b25e82806cd555237da3db4e1897`（与报告字符串在第 26 个字符处差异：实际为 `5`，报告误写为 `e`）。
  3. `git -C /Users/jacklee/Projects/codex cat-file -t ce0323f2f2d3b25e82806cd55e237da3db4e1897` → `fatal: could not get object info`（该哈希不是仓库中的任何对象）；对照实际哈希 `cat-file -t` → `commit`。
  4. 方案 §2.1（`:387`）记录「Codex 上游 | `/Users/jacklee/Projects/codex`，`main@ce0323f2f2d3b25e82806cd555237da3db4e1897`，干净」——方案记录值 = 实际值 ≠ 报告转写值，故报告的「= 方案 §2.1 记录值」声明按其自身字符串为假（一位转写错误）。
- **规则依据**：P0 来源门——「路径 + 分支 + 完整提交 + 未提交状态是不可拆分的来源身份」「任一被引用仓库缺少路径 + 分支 + 完整提交，评审不得判定通过」。记录一个对象库中不存在的完整提交哈希，使该来源身份不可复现。
- **影响限定**：U 为只读架构参考、报告自述「本轮未产生依赖 U 的新结论」，且本复核 11 处锚点抽查全部通过——R23 的技术判定不受此缺陷影响；缺陷限于来源身份记录本身。按派发指令「发现任何问题即 confirm=false」处置为不确认，修复仅需将 U 行哈希更正为 `ce0323f2f2d3b25e82806cd555237da3db4e1897`（或删去「实测一致」的错误声明并重新实测登记）。

## 四、本轮动作边界

除本复核报告外未写入任何文件；全部命令为只读（`shasum` / `git rev-parse` / `git status` / `git worktree list` / `git cat-file -t` / `grep -n` / `sed -n` / Read / find）；未运行 build/test；未触碰方案、历轮报告、业务代码与运行时。

复核员交接块：

```text
confirm: false
issues: 1（U 行完整提交哈希无效 + 一致性声明失实；覆盖完整性本身通过，锚点抽查 11/11 通过）
scope: 定向复核（锚点抽查 + 来源清单完整性）
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23-meta.md
```
