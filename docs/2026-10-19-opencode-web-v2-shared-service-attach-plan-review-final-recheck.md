# 终审定向复核报告：OpenCode v2 独立接入与共享服务设计方案（v1.9）

- 评审轮次：**终审定向复核**（按终审报告 §9 的闭合路径进行，不是全量重审）
- 评审日期：2026-10-21（来源门 21:10，Asia/Shanghai）
- verdict：**REVISION_REQUIRED（不通过，需修订）**
- blockers：**1**（F-26，由修订引入，属 F-18 同一根因的残留）；advisories：**3**（F-27 ~ F-29）
- 评审对象：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md` v1.9，
  SHA-256 `4492c9ce70c09d235b6c47e1ebc3055f6803bdded80e64ebc9976d744269d471`，共 1359 行，未提交。
  修订前 v1.8 为 git blob `bc84a29e…`（SHA-256 `34bd16fe…0e45`）；v1.8→v1.9 改动为 +137/−47 行。
- 上一轮报告：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-final.md`
  （SHA-256 `07ede12d…9aad`）。
- 评审员模式：**同一评审员延续**（即终审评审员）。独立性限制：本轮复用了自己上一轮的核验结果，可能带着
  上一轮的盲区。缓解措施：本轮逐行读完全部 diff；对修订新增的指令文本重新做语义与源码核对；关键锚点在同一
  HEAD 上重新亲核。下面的 F-26、F-27 都是修订文本新引入的问题，不是旧问题重提。
- 边界：只写本报告。未改方案与历轮报告，未改业务代码，未 commit，未 build/test/install，
  未触碰 49374/4096，无 UI automation/截屏。**本结论不构成实施授权。**

## 0. 结论

v1.9 对终审 8 条意见的收编整体到位：
- F-19、F-20、F-21、F-23、F-25 已闭合；F-22 与 F-24 的正文已闭合（各有一处登记或传播上的残留，见 F-28/F-29）；
- F-18 的主体也已闭合：新产品路径不可达、S-1/U4/U10/U12 负例、§3.11 改为非穷尽基线并列入已知必处理项、
  S-0 机械化全量枚举（含方法、四分类处置与对账）、web push 纳入旧客户端硬门。

剩下的唯一阻塞是 §3.11 第 5 行对 Desktop 写入器给出的处置里有一个不安全选项：「改为只对显式旧
opencode-web 来源（且旧凭据来源）生效」。这个函数今天是不可达的死代码。按这个选项实现，就会对
opencode-web 的 external_http/legacy_64667 用户**新增**每次启动都把 v1 地址和密码写进 OpenCode Desktop
配置的行为——这与 F-18 同一类危险，只是换了一批用户，而且没有任何负例能抓到。这个选项源自本评审员
上一轮修订方向里的措辞「改为只对显式旧来源生效」，表述不严，本报告一并更正：**唯一安全的处置是删除**。

修改量只涉及几个表格单元，不需要再做全量或大范围复核。

---

## 1. 来源身份（本轮 fresh，21:10）

| 别名 | 绝对路径 | 分支/状态 | 完整提交 | 未提交状态 | 与上一轮相比 |
| --- | --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `bd240b80ec6eceb61a99f23120086c388ebeed2b` | ` M` 方案文档（v1.9）；`??` 终审报告 | HEAD 未变；业务源码与上一轮核验时完全相同 |
| I | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `04fc269bdb8a297b1578ac4b9f0d4b82a98c7997` | 空 | 未变；仍是 iOS 仓该分支族唯一工作树 |
| U | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 空 | 未变 |

三仓的源码身份与终审时相同，所以终审已经核验过的源码锚点可以**按命题复用**。v1.9 的 §2.1 已把 M/I
登记刷新为上表的提交，与实测一致。

## 2. 复核范围与方法

1. 逐行阅读 `git diff HEAD` 中 v1.8→v1.9 的全部改动块：头部、§2.1、§2.3、§3.2.3、§3.2.4、§3.4、§3.5、
   §3.11（导语、新增 4 行、协商节、切换第 3 步）、E-2~E-6、E-9、E-11b、Gate C、S-0/S-1/S-3/S-9、
   U4/U6/U10/U12、风险表新增 2 行、阻塞清单、附录 A.18。
2. 按终审报告给出的闭合标准逐条判定 F-18~F-25。
3. 对修订新增的**指令性文本**重新核对语义与源码，查它是否引入了新的风险；对新增锚点做抽核。
4. 回归检查：同一主题在 §3.11、E、S、U、Gate 之间是否一致；头部「改动范围」与实际 diff 是否相符。
5. 本轮在 M/I 上重新亲核的源码：
   - `RuntimeManager.swift:528/546/582/1134-1153` 及 `configureOpenCodeDesktopServerIfNeeded` 的全部调用者与测试引用；
   - 静态函数 `configureOpenCodeDesktopSettings` 的全部调用者；
   - `backend_capabilities.go:156-162`；
   - `shutdownForExit` 的 `:555-559`；
   - I 侧 `Models/Server.swift:100-114` 与 `Services/Storage/ServerStorage.swift:96-101`；
   - 生产 Go/Swift 中 `"opencode"` 字面量所在的目录分布，用来检验 S-0 搜索根是否覆盖完整。

## 3. 原意见闭合表

| 意见 | 终审闭合标准 | v1.9 落点 | 判定 |
| --- | --- | --- | --- |
| **F-18**（阻塞） | Desktop 写入器的处置与负例写入 S-1/E-11b/U；S-0 枚举有方法与对账；E-11b 含 web push | §3.11 导语改为非穷尽基线；新增第 5~8 行；§2.3 与 §3.2.4 交叉引用；S-0 枚举交付物（搜索根、模式、四分类、对账）；E-11b 加入 web push 与组件负例；S-1/U4/U10/U12 加入负例 | **主体闭合**。残留两处：(1) 第 5 行处置的「改为只对显式旧 opencode-web 来源生效」选项不安全 → 转 **F-26（阻塞）**；(2) 第 5 行写的是「S-1/E-11b 负例」，但 E-11b 的断言列没有 Desktop 负例（本轮 grep 命中 0），并入 F-26 一起闭合 |
| F-19 | S-0 产出漂移表，或 v1.9 刷新来源表 | §2.1 刷新为 `bd240b80`/`04fc269b`，并注明 iOS 分支历史被改写；S-0 加入「实施 HEAD 锚点重建 + 漂移表」；§3.11 导语说明旧行号登记在各自的来源提交上 | **闭合** |
| F-20 | E-2/E-4 断言列更新 | E-2：同窗同批的身份对照表、冻结 turn 身份约定、经 session.active 对账；E-4：`SessionMessagesQuery` 约束、方向语义、desc 游走；§3.4/§3.5 补锚点；S-3 验证列加入 session.active 对账 | **闭合** |
| F-21 | 相关 E 行的获取方式引用隔离配方 | S-0 加入隔离配方（临时 XDG_*、显式非默认端口、测试 seam、清理确认、只读与隔离的分类）；E-3/E-5/E-6/E-9 引用该配方；E-2 区分了只读 GET 与写类样本 | **闭合** |
| F-22 | 只留一处一致表述，U10 与之对应 | §3.11 切换第 3 步与 U10 定稿为「CordCode 自托管的 managed 子进程随旧席位取消而停止（既有行为）；外部/共享服务一律不动」，并标注 owner 确认 | **正文闭合**；owner 确认项没有登记进 OD 表或 Gate B → F-29（建议） |
| F-23 | §3.2.3 写明处理方式 | §3.2.3 加入只读镜像 `OPENCODE_PTY_HANDOFF`（缺失或过期就不注入）；E-9 加入对应用例 | **闭合** |
| F-24 | §6 或 §3.12 补上升版流程与 remote-web 前置澄清 | §6 风险表新增 2 行；§3.11 协商节改为「remote-web marker ≠ Mac 放行硬前置」 | **正文闭合**；但 S-7 行与 §5 表后文字没有同步 → F-28（建议） |
| F-25 | E-11b/S-9 的验证列含命令 | E-11b 固化 `go list -deps -test` 命令与旧包测试资源五文件清单；S-9 引用 | **闭合** |
| 终审 A5 补注（非编号） | U6 补一句 | U6 已补「iOS 列表可能变短且无目录提示」 | 已收编 |

其余检查：
- S-0 搜索根 `go-bridge/`+`core/`+`agent/`+`MacBridge/`、`OpenCodeiOS/`、`remote-web/src/` 覆盖完整。本轮按
  目录分布核对，生产代码里 `"opencode"` 字面量只出现在 go-bridge（16 个文件）和 MacBridge（11 个文件）；
  canonical protocol pack 的 kind 枚举由 S-7 负责更新，不需要进入枚举。
- 头部「本轮改动范围」①~⑧ 与实际 diff 逐项吻合。R、OD 既有裁决与 S-2b 放行前置都没有被改动。

## 4. 新增或改动锚点的核验

| v1.9 引用 | 实际（M/I/U 当前提交） | 判定 | 依据 |
| --- | --- | --- | --- |
| §3.11 第 5 行 `RuntimeManager.swift:1134-1153`；调用点 `:582`；`applyAgentSelection :528-546`；`:1158-1162` | `:528 func applyAgentSelection(…)`、`:546 }`；`:582 configureOpenCodeDesktopServerIfNeeded()`；`:1134 private func configureOpenCodeDesktopServerIfNeeded() {`；`:1158 guard config.drivers.contains("opencode-web"), …`、`:1159 openCodeManagedServer?.stop()` | 准确 / 成立 | 本轮亲核 |
| §3.11 第 6 行 `backend_capabilities.go:156-162` | `:156 if _, ok := agent.(core.SessionPermissionResponder); ok {`；`:158 } else if id != "opencode" && id != "codex" {` | 准确 / 成立 | 本轮亲核 |
| §3.11 第 6 行其余 Go 锚点（`main.go:1271-1279`、`handlers.go:1797-1804/588-593`、`server.go:650-671`、`handlers_session_pin.go:307-315`、pathless 名单、`handlers_projection.go:1424`、`handlers_opencode.go:32`、`web_push_dispatcher.go:236-253`） | 与终审 §4、F-18(b) 核验时为同一提交 `bd240b80`，引用文字一致 | 准确 / 成立 | 复用终审亲核（源码身份未变） |
| §3.11 第 7 行 iOS 锚点；第 8 行 remote-web 锚点 | 与终审核验时为同一提交 `04fc269b`，引用文字一致 | 准确 / 成立 | 复用终审亲核 |
| §3.4 `session.ts:275-285`、`:607`；§3.5/E-4 `message.ts:8-40`；§3.2.3 `effect/service.ts:63`、`pty-handoff.ts:68-78` | U 未变 | 准确 / 成立 | 复用终审亲核 |
| §3.11 第 7 行 `Models/Server.swift:113` 及其处置「默认解码改为显式 kind 匹配」 | `:113 backendKind = try container.decodeIfPresent(BackendKind.self, forKey: .backendKind) ?? .openCode`；`ServerStorage.swift:97-100` 对整表 `try? JSONDecoder().decode([Server].self, …)`，失败时 `return []` | 锚点准确；**处置文本有风险** → F-27 | 本轮亲核 |

**本评审员更正**：终审报告 A3 表与 F-18(b) 把 pathless 同族名单写成「7 处」，但列出的实际是 5 个文件里的
10 个位置，计数有误。v1.9 没有沿用这个数字，对方案没有影响，在此登记更正。

---

## 5. 意见

### F-26 [阻塞·修订引入] Desktop 写入器的处置含不安全选项：会把今天不可达的写入重新激活到 v1 用户身上

- **位置**：§3.11 第 5 行「本版必须实施的接线」列：「该写入器改为只对显式旧 opencode-web 来源（且旧凭据来源）
  生效或拆除，新产品被选中时不可达」。连带 E-11b 断言列与 U9。
- **证据**：
  - M `RuntimeManager.swift:1134-1138`：该写入器的 guard 是 `config.drivers.contains("opencode")`。今天
    `config.drivers = selection.enabledIDs`（`:531/:543`），而选择集合必须是 `ProductAgentCatalog.ids` 的子集，
    这个集合里没有 `opencode`（`ProductAgentCatalog.swift:12-18`；`AgentSelectionStore.swift:15`）。所以该写入器
    **今天不可达**。
  - 全仓只有 `:582` 一个调用点，没有任何测试引用它（本轮 rg）。
  - 被它复用的静态函数 `configureOpenCodeDesktopSettings`（`:1345`）另有调用者：v1 managed 路径的
    `OpenCodeManagedServer.syncDesktopConfig`（`OpenCodeManagedServer.swift:324`）以及
    `MacBridgeBehaviorTests.swift:573/617/674/714`。
  - 按方案的选项一实现（guard 改成 opencode-web 的 external_http/legacy_64667 来源），就会对这些 v1 用户
    **新增**每次启动的 Desktop 写入，写入 `opencode.global.dat` 的 server 列表（含 v1 URL 与密码）以及
    `opencode.settings`。其中 legacy_64667 正是有旧凭据的存量用户的默认迁移来源（`AppDependencies.swift:99-116`）。
  - Desktop v2 每次启动都会导入 `opencode.*.dat`，并把 `opencode.settings` 当作在用的 settings store
    （U `desktop/src/main/storage/index.ts:28-35`、`legacy.ts:18-24,65-93`、`keys.ts:1`；终审已核）。
- **影响**：
  - 选项一与方案的非目标「不改上游配置/数据库」、§5「不改 Desktop 状态」冲突，也与 v1 只做「有界改动」
    的原则冲突；它重现的正是附录 A.6 那一类危险，只是对象换成了 v1 用户。
  - 现有负例只覆盖新产品路径与切换路径（S-1/U4/U10），U9（v1 显式来源）不检查 Desktop 配置，所以这条回归
    可能被直接交付。
  - 同一行写着「S-1/E-11b 负例」，但 E-11b 断言列没有这条负例，S-2b 放行的证据包会漏掉它。
- **来源说明**：这个选项源自终审报告 F-18 修订方向的措辞「改为只对显式旧来源生效」。本评审员当时所说的
  「显式旧来源」是指已退役的旧 `opencode` driver 配置，而它已不可达，实际上等于「删除」。措辞不严导致歧义，
  在此更正。
- **修订方向**：
  1. 把第 5 行处置改为：**删除 `configureOpenCodeDesktopServerIfNeeded()` 及其 `:582` 调用**（自旧 `opencode`
     driver 退役后就不可达，删除不改变任何现有用户的行为）；保留静态函数 `configureOpenCodeDesktopSettings`，
     只供 v1 managed `syncDesktopConfig` 的既有路径使用；**不得**把它改成对 opencode-web 任何来源生效。
  2. 在 E-11b 断言列补上 Desktop 负例；或者把第 5 行的「S-1/E-11b 负例」改成「S-1 负例」，并保证 S-1 在
     S-2b 之前完成——二选一，保持一致即可。
  3. U9 的「应看到」补一句：opencode-web 的 external/legacy 来源启动时，不新增对 `ai.opencode.desktop/` 的写入，
     与现状一致。
- **闭合标准**：第 5 行只保留「删除」这一种处置；E-11b（或第 5 行措辞）与 S-1 一致；U9 补上那一句。复核者
  确认这三个单元格即可，不需要扩大复核范围。

### F-27 [建议·修订引入] `Models/Server.swift` 默认解码指令有丢失已保存服务器的风险

- **位置**：§3.11 第 7 行处置「`Models/Server.swift` 默认解码改为显式 kind 匹配」。
- **证据**：
  - I `Models/Server.swift:113` 现在对缺少 `backendKind` 的历史记录解为 `.openCode`；
  - `Services/Storage/ServerStorage.swift:96-101` 用 `try?` 解码整个 `[Server]` 数组，**任何一条**失败就返回 `[]`；
    下次 `saveServers` 会用变短后的列表覆盖存储。
  - 方案已规定新 v2 使用独立的 `.openCodeV2`，旧 `.openCode` 只做「历史 Codable 兼容、不可用」，因此保留现在的
    默认值本身就满足隔离要求。
- **影响**：如果把「显式 kind 匹配」实现为严格解码，缺少字段的旧记录会让用户**全部**已保存服务器消失。
  这个指令也不是隔离新身份所必需的。
- **修订方向**：改写为：缺少 kind 的历史记录继续解为旧 `.openCode`（历史兼容、不可用、可诊断），**不得**解为
  `.openCodeV2`，也**不得**改为严格解码；如确需迁移，按单条容错。
- **闭合标准**：第 7 行处置文字更新。

### F-28 [建议·F-24 残留] remote-web「非放行硬前置」没有传播到 S-7 与 §5

- **位置**：§5 S-7 行（组件包含「remote-web marker」）；§5 表后文字「S-2b在S-3~S-7组件完成后执行」（第 818 行）。
- **证据**：§3.11 协商节（第 687-689 行）与 §6 风险表（第 894 行）已写明 remote-web marker 不是 Mac 放行硬前置；
  但按 §5 的字面，S-7 包含 remote-web 部分，而 S-2b 要等 S-7 组件完成，这就让 remote-web 实际上又成了放行前置。
- **修订方向**：S-7 的组件描述把 remote-web 部分标为「不阻断 S-2b，随 remote-web 自身交付」；或把表后文字改为
  「S-2b 在 S-3~S-6 与 S-7 的 Mac/iOS 部分组件完成后执行」。
- **闭合标准**：§3.11、§6、§5 三处表述一致。

### F-29 [建议] 待 owner 确认的产品决定没有进入 OD 表与 Gate B

- **位置**：OD 表（OD-1~OD-8）、Gate B；§3.11 切换第 3 步与 U10；S-0 枚举的「需产品决定」分类。
- **证据**：
  - F-22 定稿为「managed 子进程随旧席位取消而停止，owner 实施启动时确认」，但只写在 §3.11、U10 和 A.18，
    OD 表没有相应条目，Gate B 也没有提到。
  - S-0 枚举会产出「需产品决定」的消费点（例如 iOS 的 project roots/agent picker 特性门是否对 `.openCodeV2` 开放），
    方案没有规定这些决定由谁、在何时作出，也没有说要在 S-7 开工前决定。
- **影响**：实施启动时容易漏掉确认；S-7 可能在决定悬而未决时开工。
- **修订方向**：
  - 新增 OD-9：managed 子进程停止语义，pending，推荐沿用既有行为，影响切片 S-1/S-7，验收对应 U10；并列入 Gate B。
  - 写明 S-0 枚举中的「需产品决定」项要登记为 OD，并在依赖它的 S-7 子项开工前决定。
- **闭合标准**：OD 表与 Gate B 更新，S-0 写明决定的流转规则。

---

## 6. 剩余门与实施就绪

- 证据门：E-1 verified（复用 2026-10-19 活体）；E-2~E-11 维持 pending，本轮不改变其状态。
- 产品决定：OD-2/5/7 decided；F-22 的停止语义待 owner 确认（建议按 F-29 登记为 OD-9）。
- 外部格式专项：v1.9 没有新增写成事实的外部形状。新增的 `session.active`、`SessionMessagesQuery`、
  PtyHandoff 都是源码锚点，并已写进对应 E 的断言与获取方式，作为待捕项处理，符合待捕项裁定。
  按 audit-plan 严格口径，专项仍是「本轮未复测活体」（指令禁止触碰 49374/4096）。
- implementation_readiness：
  - F-26 闭合后，v1.9（或 v1.10）即可作为实施冻结基线。
  - 冻结后各切片的门不变：S-0 先行（含全量枚举、锚点重建、隔离配方）；S-1、S-2a、S-7 的组件部分依赖 E-11a；
    S-3~S-6 分别受 E-2/E-4/E-3+E-5/E-7+E-10 阻塞；S-2b 的放行前置不变；S-8、S-9 不变。
  - 授权：**无**。实施、取证写操作、服务启停与安装都须 owner 另行授权。

## 7. 结论与闭合路径

**verdict: REVISION_REQUIRED**（blockers=1，advisories=3）。

v1.9 已经实质解决了终审最关心的风险：新产品路径不会再触发 Desktop 写入；身份复用的消费者有了机械化全量枚举
与必处理清单；旧客户端隔离补上了推送通道。剩下的阻塞 F-26 是同一根因上的一处措辞残留：处置里那个「改为对
v1 来源生效」的选项，会把今天的死代码变成对 v1 用户的 Desktop 写入，而且没有负例兜底。

闭合路径：
1. 作者改 §3.11 第 5 行、E-11b 断言列（或第 5 行措辞）与 U9，闭合 F-26；建议同批处理 F-27~F-29，它们也都只涉及
   表格单元或一两句话。
2. 复核者核对这几个单元格。如果 F-26 按闭合标准落实，且修订没有超出上述范围，就可以判定通过，作为实施冻结基线，
   不需要再做全量或大范围复核。
3. 实施启动仍由 owner 决定。
