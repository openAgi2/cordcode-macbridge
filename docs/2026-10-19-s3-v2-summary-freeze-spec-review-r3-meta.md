# S-3 `projection_window_v2` 冻结规范 R3 通过结论定向复核（meta）

日期：2026-10-19（Asia/Taipei）。复核对象：
`docs/2026-10-19-s3-v2-summary-freeze-spec-review-r3.md`（R3 通过报告）及其评审对象
`docs/2026-10-19-s3-v2-summary-freeze-spec.md`。

## 结论

- verdict: **复核通过（confirm=true）**
- 锚点抽查：**11 处核对，11 处吻合，0 处偏差**（随机抽取 3 处 + 补充加验 8 处）。
- 来源清单完整性：**覆盖全部来源组合**（M、I 配套工作树、母方案、上游处置），实测 git
  身份与报告记录逐一相符，无缺项。

## 复核范围与方法

按复核任务仅两项：锚点抽查、来源清单完整性。不重评方案内容本身。

随机抽取方法：R3 报告「锚点核验表」列亲核锚点 10 组，以 `$RANDOM` 从 1-10 无偏抽取，
命中 #1、#5、#7（满足「至少 3 处」）；另补充加验 #2/#4/#8（与已读文件同范围零成本）、
#6/#9（I 仓跨仓覆盖）、#10（方案文档 grep）、以及两条新建议 F-R3-1/F-R3-2 的证据
锚点。实际共核对 11 处。

## 一、锚点抽查结果（11/11 吻合）

| # | 报告引用（r3.md 行） | 抽取方式 | 实际核对结果 |
| --- | --- | --- | --- |
| 1 | `projection_types.go:57-75` permission 族（:57/:61/:67/:75 摘录，报告 :60） | **随机** | :57 注释行、:61 `RequiresPermissionConfirmation bool` json tag、:67 `PermissionKind`、:75 `PermissionPlan` 逐字吻合；:57-75 恰为 permission 字段块。语义（camelCase wire 名 / pending tool flag）成立 |
| 5 | `session_turn_items_handler.go:136-141` chunks echo 选 §11.8 引擎（:138/:139，报告 :64） | **随机** | :138 `if h.eventPublisher.ConnTurnDetailChunksV1(conn) {`、:139 `handleSessionTurnItemsV2(...)` 逐字吻合；:136-141 恰为 v2 dispatch 块 |
| 7 | `session_turn_items_handler.go:68/:80/:95` retryable 逐项 false/false/true（报告 :66） | **随机** | :68 capability_required 尾参 `false`、:80 invalid_params `false`、:95 `code, retryable = "projection.hydrating", true` 逐字吻合，与 §6 逐项标注一致 |
| 2 | `projection_types.go:98-115` user_input 族（:98/:108/:115，报告 :61） | 补充 | 三行逐字吻合；Go 属性 `UserInputStatus` 的 json tag 确为 `"status,omitempty"`（:108），支撑 F-R3-1「wire 名是 `status`」 |
| 4 | `session_turn_items_handler.go:64-70` 查 `ConnTurnDetailV1`（:66/:67-68，报告 :63） | 补充 | :66/:67-68 逐字吻合，错误文案确含 `turn_detail_lazy_v1` |
| 8 | 同文件 `:71-75`/`:115-125`/`:127-131` 三重门（报告 :67） | 补充 | :71 `BackendID != "codex-remote"`、:115-118 turn_not_found、:120-123 not completed→invalid_params、:130 `HistoryMode != "paginated"` 逐项吻合 |
| 3 | `projection_reducer.go:924-933` 谓词 + `:2117` 清 flag（报告 :62） | 补充 | :925 `Type == "user_input" && UserInputStatus == "pending"`、:928-929 tool + RequiresPermissionConfirmation + ToolStatus 空或 pending、:2117 决议时 `RequiresPermissionConfirmation = false`（上下文 :2115-2119 确为 permission 决议路径）逐字吻合——「§3 识别条件与 kernel 谓词逐字段同构」成立 |
| 6 | I `CCCodeBridgeTransport.swift:209/:219` 双 append（报告 :65） | 补充（跨仓） | :209 `capabilities.append("turn_detail_lazy_v1")`、:219 `capabilities.append("turn_detail_chunks_v1")` 逐字吻合 |
| 9 | I `SessionProjection.swift:355` + `SessionProjectionMapping.swift:392`（报告 :68） | 补充（跨仓） | :355 `case userInputStatus = "status"`（CodingKey）、:392 `let status = (part.userInputStatus.flatMap { UserInputStatus(rawValue: $0) }) ?? .unavailable` 逐字吻合；F-R3-1「`userInputStatus` 是内部属性名、wire 名 `status`」成立 |
| 10 | 方案文档 `interactive` 残留 grep 仅 :18/:149（报告 :69） | 补充 | `grep -n interactive` 实测恰 2 处：:18 位于修订记录块（标题 :8，v3 条目 :10-19，属对原意见的引述）、:149 为 §3「无 `interactive` 标记」否定句，与报告判定一致 |
| 11 | F-R3-1 证据 `bridge-v1.types.ts:884-886`（报告 :101-104） | 补充（建议级证据） | `docs/protocol/schema/bridge-v1.types.ts` :884 `type: "user_input";`、:886 `status: "pending" \| …` 逐字吻合；另核 F-R3-2 引文（方案 :250-254 交付物 3 全句）与报告引用一致，「单类即达标」歧义读法在原文中确实存在 |

另核 F-R3-2 所引方案原文（:250-254）与报告引文逐字一致。

## 二、来源清单完整性核对

项目规则（CLAUDE.md P0「评审与文档来源」）：被引用仓库须有路径 + 分支 + 完整提交；
引用行号不得来自另一工作树；功能分支文档不得用 `main` 核验；评审范围内未提交修改必须
纳入来源；来源清单须覆盖每个涉及的仓库与工作树（含配套工作树，无论是否实际引用）。

实测（复核时点 2026-10-19 晚）：

| 项 | R3 报告记录 | 实测 | 判定 |
| --- | --- | --- | --- |
| M 路径/分支/提交 | `…macbridge-claude-long-session-projection-loading` / `feat/claude-long-session-projection-loading` / `d5255ed126b27de0c6decf173c1eee4a44386dd2` | 三者逐一相符（HEAD=d5255ed1「S-3 v2 summary 冻结规范送审稿 v1」） | ✓ |
| M 未提交状态 | 方案文档 v3（SHA `14a35971…`）+ R1/R2 报告 untracked；其余干净 | porcelain 恰为：spec.md modified + r1/r2/r3 untracked（r3=报告自身，报告已声明「唯一写入=本报告」）；spec 实测 SHA-256=`14a359713c3b2dac3ccd7f146023204a1cd20d47cd96c42b9c9adfed092f3a95` 与记录一致 | ✓（评审对象本身即未提交 v3，已按规则纳入来源并记 SHA） |
| I（配套工作树，实际有引用）路径/分支/提交/状态 | `…ios-claude-long-session-projection-loading` / 同名分支 / `0fbac7f57a08543def643141147ff70d9ef0c320` / 干净 | 四者逐一相符（porcelain 空） | ✓ |
| 母方案 | M `docs/2026-10-17-…-architecture-plan.md`，同 M 提交，v30/R25 APPROVED | 文件存在于 M 工作树 | ✓ |
| 上游 codex 参照 | 显式处置：v3 未新增上游语义引用，无需新取证，R1/R2 已核锚点按「提交未变 + 引用文本未变」复用 | v3 新增/变更文本所引锚点（本轮 11 处核对对象）确全部为 CordCode 双仓自有文件；M/I 提交实测与 R1/R2 记录一致（M、I 提交在三轮间未变） | ✓ |
| 引用越界检查 | — | 逐条清点 R3 全部引用文件：M 侧（go-bridge 4 个 .go、docs/protocol 3 件、agent wire_descriptor 2 件、方案/母方案文档）与 I 侧（Transport/SessionProjection/Mapping/BackendClient/ProjectionStore/CacheStore）无一落在清单外工作树 | ✓ |
| 用 `main` 核验 | 报告核验均在功能分支工作树 | 实测两工作树均在 `feat/claude-long-session-projection-loading`，非 `main` | ✓ |

结论：R3 来源清单覆盖任务涉及的全部来源组合——M、I（配套工作树，且被实际引用）、
母方案（M 内同提交）、上游（显式免取证处置且理由经核成立）；四条「评审不得判定通过」
的失败条件均未触发。来源清单无缺项。

## 三、总体判定

- 随机抽查的 3 处锚点（#1/#5/#7）行号与语义全部吻合；加验 8 处亦全部吻合，无一例
  「行号来自另一工作树」或「摘录与原文不符」。
- 来源清单完整且与实测 git 身份一致。
- 未发现任何需列报的问题：**confirm=true，issues=0**。

本报告为定向复核（meta），不改变 R3 的 APPROVED 结论，也不构成对方案内容的重评。

## 本报告来源清单

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `d5255ed126b27de0c6decf173c1eee4a44386dd2` | spec.md modified（v3，SHA `14a35971…`）+ r1/r2/r3/r3-meta 报告 untracked；其余干净 |
| I | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `0fbac7f57a08543def643141147ff70d9ef0c320` | 干净（git status 空） |

任务预期分支 = 两仓同名 `feat/claude-long-session-projection-loading`（M/I 即本任务
配套组合）；M/I 提交与 R1/R2/R3 记录一致。本轮只读源码与文档，唯一写入 = 本报告；
无构建/安装，预期产品特性项不适用（纯文档评审）。
