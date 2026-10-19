# 元审核报告 r2-meta：对第 2 轮评审通过结论的定向复核

- 元审核日期：2026-10-19
- 被复核对象：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-r2.md`（verdict APPROVED）
- 方案：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，SHA-256 复核
  `ceb463b079e08e57efc442c25b02c47338351e39380c8d1e8c5cd4826d0c7ecb`（本元审核重算一致，与派发哈希相同）
- 复核范围：仅两项——(1) 锚点抽查（随机抽带行号引用逐行核对）；(2) 来源清单完整性
  （是否覆盖任务项目规则要求的全部来源组合，含配套工作树，无论是否实际引用）。
  两项之外的内容不复核。
- **结论：confirm = false**，问题 4 条（§3）；其中 2 条为锚点行号与实际文件不符，
  2 条为来源清单完整性缺口。

## 0. 元审核来源清单（P0，本轮亲核）

| 仓库/工作树 | 绝对路径 | 分支/状态 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| cordcode-macbridge（评审目标工作树） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `1fe9c65d734949981d032de5f57e9ffb43f6bde9` | 干净；未跟踪 3 个 docs 文件（r1/r2 报告+方案；r2 写作时点为 r1+方案 2 个，其表述与当时状态一致） |
| opencode（v2 上游） | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32`（grafted HEAD） | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 干净（porcelain 空） |
| opencode（旧 checkout，仅备注） | `/Users/jacklee/Projects/opencode` | `dev` | `22e771f5893e255ea78fc10557ed3fb98d3a1072` | porcelain 空 |
| cordcode-ios（配套工作树，唯一分支族对应） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52` | 干净（porcelain 空） |

任务预期分支：本任务家族 `feat/claude-long-session-projection-loading`；MacBridge 与 iOS
两侧各存在且仅存在一个该分支族工作树（两侧 `git worktree list` 亲核）。

## 1. 锚点抽查

### 1.1 抽样方法

以 `$RANDOM`（0–32767）对 28 个候选引用单元（r2 §2.3 表 25 行 + F-10/F-12/F-13 三条
意见的证据锚点）取模去重，抽出 9 个单元：§2.3 第 3、4、5、9、12、20、21、23 行 +
F-12 证据。因在第 5 行发现行号偏差，为定性偏差是否孤立，追加核对 3 个相邻单元
（§2.3 第 1、2、11 行 + F-10 证据，其中第 11 行与 F-10 同批核对）——合计 12 个引用单元，
覆盖 opencode-v2 上游 7 处与本仓 Swift/Go 5 处。

### 1.2 逐项核对结果（上游 commit 843a6c51 / 本仓 1fe9c65d，均为本轮实际文件）

| # | r2 引用（报告位置） | r2 摘录声称 | 实际核对 | 判定 |
| --- | --- | --- | --- | --- |
| 1 | §2.3 第 3 行 form 组（F-6 修订落点） | list `:796`、detail `:827`、reply `:842`、cancel `:858` | `grep -n` 四路由端点行精确命中 796/827/842/858 | 通过 |
| 2 | §2.3 第 4 行 service-probe `:78-79`；404→compatible:false `:65-73` | pid/version 不符返回 `{service: undefined…}` | `:78`、`:79` 逐字命中；`:65-73` 恰为 404 分支（`compatible: false`） | 通过 |
| 3 | §2.3 第 5 行 `service.ts:27-35`（EnsureOptions） | `:30 readonly command?: …`；`:31 /** … Defaults to \`opencode serve --service\`. */` | `:30` 命中；**注释实际在 `:29`**（`:31` 是 env 注释），偏 +2 | **行号不符** |
| 4 | §2.3 第 9 行 schema 事件字面量 | `session-compaction-event.ts:7`、`filesystem.ts:9`、`form.ts:173-175` | 三文件 grep 精确命中 7/9/173-175（created/replied/cancelled） | 通过 |
| 5 | §2.3 第 12 行 监控循环（F-2 新锚点） | 实际 `:709-722`；`:718 pollManagementAPI()`、`:719 evaluateAutoRestart()`、`:722 Task.sleep(3_000_000_000)` | 三行逐字精确命中；`startMonitoring` 函数体 709-725 | 通过 |
| 6 | §2.3 第 20 行 probe.go `:174` / shape arbiter `:196-216` | `:174 probeHealth(ctx, c, "/api/health")`；`:197-201 shapePath="/session"…generationV2→"/api/session"+shapeDataEnvelope` | 逐行精确命中 | 通过 |
| 7 | §2.3 第 21 行 sessions.go `:260-262` / `:137-143` | unwrapDataEnvelope 注释 `:260-261`；directive-003 注释 `:139-140` | 逐字精确命中 | 通过 |
| 8 | §2.3 第 23 行 client.go `:97-124`（`:111-117`） | `if c.gen == generationV2 { … ONLY V2SessionListData declares query.directory … appendQuery=GET且HasSuffix("/api/session") }` | `:111-117` 逐字命中 | 通过 |
| 9 | F-12 证据 `service-probe.ts:40` | `join(process.env["XDG_STATE_HOME"] ?? join(homedir(), ".local", "state"), "opencode", "service.json")` | `:40` 逐字命中（fallback() 39-41） | 通过 |
| 10 | 补查 §2.3 第 1 行 SessionsQuery（F-1 阻塞闭合锚） | `:168 export const SessionsQuery`；`:170 directory:`；`:173 cursor:` | 三行逐字精确命中 | 通过 |
| 11 | 补查 §2.3 第 2 行 `location.ts:71-79` | `:76 query.get("location[directory]") \|\|`；`:77 (request.headers["x-opencode-directory"] ? decode(…) : process.cwd())` | **实际在 `:74` 与 `:75`**（`:76-77` 是 `return Location.Ref.make({`/`directory: AbsolutePath.make(...)`），偏 +2；区间 :71-79 正确、语义成立 | **行号不符** |
| 12 | 补查 §2.3 第 13 行 triggerAutoRestart + F-10 证据 | 函数头 `:757`、`:759`/`:765`/`:768`、`:417 maxStuckRestarts = 5`；F-10 `:1070-1072` stuck 清零 | 全部逐行精确命中 | 通过 |

### 1.3 抽查结论

- 12 个引用单元中 10 个行号+语义全部精确吻合，含全部阻塞闭合承重锚（F-1 SessionsQuery、
  F-2 触发链/监控循环、F-6 form 四路由）与 F-10/F-12 意见证据。
- 2 个单元存在行号偏差（见 §3 问题 1、2），均为 **+2 漂移**、均在 opencode-v2 上游
  TS 文件、均被 r2 标注「本轮亲核 / 定位准确」。上游同一 pinned 提交
  `843a6c51…`、工作树干净（porcelain 空、无 stash），可排除版本漂移或未提交改动解释，
  属通过报告自身的引用行号错误。
- 两处偏差的摘录内容与语义本身成立（EnsureOptions 默认命令、requestRef 双机制），
  不推翻 APPROVED 的实质判断；但按元审核契约「行号与语义都必须吻合」，属必须上报问题。

## 2. 来源清单完整性

### 2.1 r2 实际覆盖

r2 §2.1 来源身份表含 2 个组合，均具备路径+分支+完整提交+未提交状态，且与本元审核
亲核的实际状态一致（MacBridge 工作树 1fe9c65d ✓；opencode-v2 843a6c51 tag v2.0.32 ✓）。
运行身份（49374/4096、service.json）本轮未复测，非来源清单完整性问题。

### 2.2 缺口

按 CLAUDE.md P0 来源清单模板（`配套仓库路径/分支/提交` 为清单必填字段）、「任何跨仓
分析、设计、评审……都必须写入上述完整来源清单」及本元审核口径（「含配套工作树，
无论是否实际引用」）：

1. **配套 iOS 工作树缺席**。r2 来源表不含配套工作树身份，也无「经核配套工作树为 X、
   本方案 iOS 侧零引用」的显式判定记录。实际唯一配套工作树存在且干净：
   `cordcode-ios-claude-long-session-projection-loading` @
   `feat/claude-long-session-projection-loading` @ `89f0a369…`。方案虽在非目标中声明
   「iOS 仓无代码改动」，但 R-1/R-2/R-3 与验收标准 1–3 全部是 iOS 面命题，「iOS wire
   零变更」本身是跨仓主张——来源清单应承载该组合（或显式记录排除依据）。
   注：方案 §2.1 自身同样未列该组合，r1 亦未列；本条按元审核口径同时成立，不单独归责 r2。
2. **旧 opencode checkout 行缺失**。方案 §2.1 P0 来源表第三行
   （`/Users/jacklee/Projects/opencode`，dev @ `22e771f5…`，「不作为锚点仅备注」）与
   r1 §2.1 均含该行；r2 §2.1 只保留 2 行，但 r2 §4 F-4 闭合叙述仍引用旧 checkout
   （「旧 checkout 分支名已更正为 dev」）。被引用的来源组合未入 r2 来源身份表，
   相对 r1 为覆盖回退（该行实际状态本元审核已亲核：dev @ 22e771f5，与方案更正后一致，
   闭合事实本身无误）。

## 3. 问题清单（confirm=false 的依据）

1. **[锚点-行号不符]** r2 §2.3「§2.2 ensure 形状 `service.ts:27-35`」行（标注「本轮亲核/
   定位准确」）：摘录将 `Defaults to \`opencode serve --service\`` 注释标为 `:31`，实际在
   `packages/client/src/service.ts:29`（+2 漂移；`:31` 实为 env 注释）。`:30 readonly
   command?`、区间 :27-35 与语义正确。
2. **[锚点-行号不符]** r2 §2.3「§2.2 scoped 路由 `location.ts:71-79`」行（标注「本轮亲核
   （r1 亦核）/定位准确」）：摘录 `:76 query.get("location[directory]") ||` 与
   `:77 (request.headers["x-opencode-directory"]…` 实际在
   `packages/server/src/location.ts:74/:75`（+2 漂移）。区间 :71-79 与语义正确。
3. **[来源清单-配套工作树缺席]** r2 来源身份表未覆盖配套 iOS 工作树
   （`/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` @
   `feat/claude-long-session-projection-loading` @ `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52`，
   干净），亦无显式的配套工作树判定/排除记录；方案 R-1~R-3 与验收标准 1–3 为 iOS 面
   命题，「iOS 仓无代码改动」属需来源承载的跨仓主张。
4. **[来源清单-引用组合未入表]** 方案 §2.1 第三行（旧 opencode checkout，dev @
   `22e771f5…`）在 r1 来源表中存在、在 r2 §4 F-4 闭合叙述中被引用，但 r2 §2.1 来源
   身份表无该行。

## 4. 定性说明（不改变 confirm=false）

- 问题 1、2 是报告引用精度错误，不是结论性错误：对应命题语义成立，且全部阻塞闭合
  承重锚（SessionsQuery、form 四路由、触发链、监控循环、F-10/F-12 证据）抽查全部
  精确命中。若仅存在 §3 问题 1、2，属「修订摘录行号」级整改；叠加 §3 问题 3、4 的
  来源清单缺口后，按元审核口径整体 confirm=false。
- 修复建议（供下轮）：更正 r2 §2.3 两处摘录行号（或由修订流程重发）；来源身份表补
  配套 iOS 工作树行（或显式排除记录）与旧 checkout 行。
