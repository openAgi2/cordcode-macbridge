# Claude 外部会话旁观时实时更新中断——修复方案 v2（送审稿）

> **状态**：方案阶段 · 送审稿 **v2**，按评审 R1 修订。评审「通过」前不修改业务代码。
>
> **一句话**：在 iPhone 上旁观 Mac 端正在执行的 Claude 会话（Desktop / Terminal 发起的外部回合）时，实时视图会丢失工具调用、工具结果和中间说明，会话会被反复标为失败、活动期间重开会被挡住；遇到一次 API 错误（如 429）后，实时更新会彻底冻结。两处根因在 `main` 上同样存在。**本期只消除这两类已证实的触发**；失败发生后如何恢复，另起一期设计（§5.4）。

## v2 修订说明（相对 v1：提交 `fd4777e6`，评审记录的 SHA-256 为 `c7866848…3e753`）

- **本轮改动范围**：只改本文档。没有修改任何业务代码、测试或协议，没有构建、安装或部署。
- **评审**：`/Users/jacklee/Projects/cordcode-ios-native-message-timeline/docs/2026-10-21-claude-live-relay-zero-event-and-api-error-poison-fix-plan-review-r1.md`（分支 `feat/ios-native-message-timeline`，提交 `1d76e38d53ab6b7c7369f453e6962a0eaf427d9e`），结论 REVISION_REQUIRED，2 项阻塞、2 项建议。逐项处理如下，**全部采纳，没有不采纳的项**。

| 评审项 | 处理 | 位置 |
| --- | --- | --- |
| F-01 / P1：把 mapper 未产出事件的行一律当 source-only，会把有内容或未知类型的行静默接受 | 采纳。改为**三分类**：可投影 / 已证明无展示 / 不支持。只有「已证明无展示」可以 source-only，它是有样本依据的白名单（空白 thinking、空白 text）。其余零事件行（含 assistant/tool_result、未知块、空 content、零事件 user 行）**照旧拒收**并附诊断。测试改为「真实 entry → 生产分类器 → mapper → Kernel」，并加入负例 | §5.1、§7 T1–T3、T7 |
| F-02 / P1：取消冷却只解除 admission 拦截，旧账本仍会让重开后的下一条实时内容再触发 gap | 采纳**选项二：收窄**。撤回 v1 §5.2 的「立即可重试」和「失败后重开即恢复」的承诺；本期**不改**失败与恢复语义，剩余限制在 §5.4 明示。冷却语义、账本重装、relay 重绑、并发 append 与旧代事件处置，连同评审给出的闭合测试，整体移入 Phase 2 单独方案。v1 的 T6 删除 | §1、§2、§5.2、§5.4、§7 |
| A-01 / P2：普查分母与样本索引要可复核 | 采纳。重跑普查并固定运行时刻、截止规则、两种分母与 poison 截断量；建立样本索引；两种方法对账 | §3.2 |
| A-02 / P2：「无冲突」表述过强；RC-1 应列为发布门 | 采纳。删除无冲突保证，改为按文件逐一核对；记录评审期间及本轮出现的并发修改；RC-1 列为该产品组合的发布门 | §0、§8、§11 |

另外两处小修：补齐 0.3.301 SDK 的绝对路径与哈希（§3.3）；样本描述修正为「thinking 文本为空或仅空白，signature 可能有也可能为空」。

---

## 0. 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=fd4777e6bd41d61106c127950d248e0be005816b（v2 全部行号锚点都绑定这个提交，可用 git show fd4777e6:<path> 复核）
未提交状态（2026-10-21 15:57:50）=3 个并发修改，不属于本方案，也不由本方案产生（开发者在修评审复核的 RC-1 / RC-2）：
  go-bridge/claude_relay_coverage.go    工作区 SHA-256 前缀 24d2060ded44043a（HEAD 8bd0e867dad02aa2）
  go-bridge/handlers_relay.go           工作区 a98680f54b702636（HEAD 7dc30a8f4b9fc82f）
  go-bridge/session_turn_items_handler.go 工作区 21f0c03fb428cdd1（HEAD 408fe17e617ae18e）
  （16:02:30 复查：又多出 agent/claudecode/continuation.go 已修改、go-bridge/session_turn_items_backend_gate_test.go 未跟踪，同属开发者的并发工作，本方案不涉及）
任务预期分支=方案指定在上列分支实施（§11），实施前重做来源门
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / 64a386d68ae5e11f109b19bec85283bf2486b562（只读：iOS 对失败码的呈现；本方案不改 iOS）
预期产品特性=Claude 外部回合 file relay 旁观：工具调用、结果、中间说明、最终回复都实时出现，状态正确收口
```

- **与 main 的交集**（按文件逐一核对，不作无冲突保证）：本期要改的文件是 `claude_source_mapper.go`、`projection_source_transaction.go`、`handlers_relay.go`，以及测试 `claude_source_shapes_test.go`、`claude_file_relay_test.go`、`projection_source_transaction_test.go`、`claude_source_mapper_test.go`、`testdata/claude-source-shapes/`。在 `f868af18..fd4777e6` 区间这些文件都没有改动，与 main 一致。v1 曾涉及 `projection_kernel.go`（冷却语义），本期已撤回、不再改动；分支对 `projection_kernel.go` 和 `handlers_projection.go` 本来就有改动，与本方案无交集。
- **并发修改的重叠**：开发者的未提交改动也在 `handlers_relay.go` 中，位置是 relay 启动与 coverage 注册（`startClaudeSessionFileRelayAt`、`claudeSessionFileRelayLoop` 开头），和本方案要改的符号（`claudeTranscriptRelayEntry.Error`、完整记录扫描器、`applyClaudeLiveSourceRecord` 的拒收诊断）不同，但属于同一个文件。**实施必须等这批 RC-1 / RC-2 改动提交之后再开始**，并重新取锚点（§5.5）。
- **运行态**：`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，内嵌 `d88920e692b3`，`go1.26.2`，PID 82698（12:10:29 起）。
- **证据类别**：【生产日志】只读 grep；【真实样本】本机 `~/.claude/projects`，只统计形态、计算哈希，不复制正文、signature、路径；【scratch】仓库外 `git archive fd4777e6` 副本中的定向测试，只作设计材料，不当作通过断言；【源码】绑定 `fd4777e6`；【推断】单独标注。

---

## 1. 现象与影响（产品语言）

| 场景 | 现在看到的 | 本期修复后应看到的 |
| --- | --- | --- |
| iPhone 打开 Mac 上正在执行的 Claude 会话（模型开着思考） | 用户提问出现，之后的工具卡片、工具结果、中间说明大量缺失 | 每一步按顺序实时出现，最后显示完成 |
| 同一场景下，活动期间退回列表再进入、App 切回前台、断线重连 | 常被拒（`projection.hydrate_failed`，可重试），iOS 进入重试 loading 或提示「无法加载会话投影」 | 正常打开。原因是已证实的触发（空白 thinking / text）不再造成拒收，冷却不会被武装 |
| Mac 端回合中出现一次 API 错误（429 / overloaded） | 该会话实时更新**完全冻结** | 错误之后的内容照常实时出现；错误行本身仍是诊断记录，不变成失败回合 |

触发面只涉及 **file relay 旁观的外部回合**。iPhone 自己发起的回合，正文由 stdout 供给（`handlers_relay.go:1215-1223`），不受 RC-A 影响。

**本期之后仍存在的限制（明示，见 §5.4）**：如果出现「不支持」类的零事件行（当前 CLI 版本中尚未观测到，§3.2），行为与现在相同：该行之后到下一条控制行之间的实时内容会丢，会话被标失败，最长 30 秒内重开会被挡住；冷却过后重开能看到完整的冷重建内容，但之后的实时内容仍可能再次 gap，直到下一条控制行。本期只为这种情况补上带行形态的诊断日志。

---

## 2. 交互四拍（修复后应满足）

1. **打开**：iPhone 会话列表 → 点开一个 Mac 端正在执行的 Claude 会话。
2. **输入**：iPhone 不输入；Mac 端 Claude 继续执行，逐行写入 transcript（thinking、tool_use、tool_result、attachment、api-request、偶尔的 system/api_error……）。
3. **执行路径**：Mac file relay 逐行读取 → 每行走 Kernel 私有 source-batch 事务（`ApplyClaudeSourceRecordBatch`，唯一投影写者）→ `projection_patch` 推给 iPhone。路径不变，不新增第二条。
4. **过程展示**：
   - iPhone 按顺序出现工具卡片（标题 + 输入）、工具结果、中间说明、最终回复，状态从执行中变为完成；
   - 「已证明无展示」的行（空白 thinking、空白 text）只推进 Mac 私有账本，屏幕上不产生任何东西；
   - system/api_error 行只作诊断记录，不会冻结后续内容，也不会把回合标为失败；
   - 「不支持」的零事件行照旧失败关闭，行为同现状并附诊断；失败后的恢复不在本期（§5.4）。

---

## 3. 证据

### 3.1 生产日志【生产日志】

只计 `msg="go-bridge: Claude source batch rejected"`，避免与 hydrate-failed 日志重复计数。

| 日志 | 会话 | no projection subevents | source batch gap | 时间窗 |
| --- | --- | --- | --- | --- |
| `go-bridge.log`（本分支 runtime） | `b66193d7`（iPhone `full_stream` 旁观中） | 146 | 281 | 10-21 14:46:29 → 15:59:29（统计于 15:59:37，仍在持续） |
| `go-bridge.log.1` | `608e03af` | 16 | 31 | 10-20 11:56:42 → 12:51:12 |
| `go-bridge.log.3`（10-13/14，早于本分支） | `bbe396e6` | 14 | 33 | 10-13 20:51:37 → 21:31:55 |

保留日志中 `quarantined invalid complete record` 出现 0 次：RC-B 在生产上尚未被旁观会话撞到。

### 3.2 普查（固定口径，两种方法）【真实样本】

- **运行时刻**：2026-10-21 15:58:31 +08:00（Go）与约 15:59（Python）。
- **截止规则**：文件 mtime ≥ 运行时刻减 14 天（2026-10-07 15:58:31 +08:00）。
- **两种分母**：
  - A = 这些文件里的全部记录，含旧版本历史；
  - B = 其中记录自身 `timestamp` ≥ 截止时刻的部分，即近 14 天新写的行。
- **Go 方法**（生产 `scanCompleteClaudeRelayEntriesFromReader`、生产内容行判定、生产 mapper，加上 scratch 中实现的拟议三分类器）：
  - 516 个文件，共 1,235,682,554 字节，实际扫描 979,037,578 字节；
  - **64 个文件在首个 poison 处截断**，未扫描 256,644,976 字节，64 个截断点**全部是 `system/api_error` 且 `error` 为对象**的行，即 RC-B；
  - 内容行 A=144,494 / B=70,319；零事件行 A=3,520 / B=3,289；
  - 三分类：可投影 140,974，已证明无展示 3,515，不支持 5；
  - **分类器与 mapper 的不变量「可投影 ⇔ 事件数 > 0」违例 0 条**。
- **Python 方法**（独立字段判定，整文件读取）：
  - 不截断时，已证明无展示 A=3,537 / B=3,291，不支持 A=8；
  - 模拟扫描器在 poison 处截断后，已证明无展示 A=3,517 / B=3,291，不支持 A=5；
  - 与 Go 相差 2 条，原因是两次运行相隔约 1 分钟、活跃 transcript 仍在增长，以及 Go 侧 admission 过滤（sidechain、compact 等）更严格。
  - 「不截断」比「截断」多出的 20 + 3 条行，是 RC-B 修复后扫描器才会看到的同类形态。

| 类 | 形态 | A | B | 版本 | 样本（文件 SHA-256 / 行 / 字节范围 / 记录 SHA-256） |
| --- | --- | --- | --- | --- | --- |
| 已证明无展示 | assistant `[thinking(blank)]` stop=`tool_use` | 3,249 | 3,032 | 2.1.219 – 2.1.304 | `495a03b5510a56ad3c0db16b2756cacd348d943b5ebddb5e9b8220c23eb9a6b9` / 29 / `[127094,143231)` / `a8555b241ce495f728a191b53b47c069635c058ab9a2b9d6324b70749331f899`（2.1.299，sig 存在，`thinkingDisplay:"updates"`） |
| 已证明无展示 | assistant `[thinking(blank)]` stop=null | 236 | 235 | 2.1.219 / 2.1.295 / 2.1.299 | `88a7884aeca14b78bef6df054ac9ff776bf3f62768e8cda9cc508c39d875cc84` / 10 / `[217070,220135)` / `3b25ecb46f1ac77fa09c75c11b6b6119d3ce24f974b94156c3f5aba3c97f3ad4`（2.1.299） |
| 已证明无展示 | assistant `[text(blank)]` stop=`tool_use` | 19 | 18 | 2.1.219 / 2.1.295 / 2.1.301 | `1d01e52290a4c787e13cae076b9b11b568b7450e521dc8e8284a21b88296a40e` / 8254 / `[27897650,27898877)` / `a67e7323a94a022ab101cbc1465031541772c89de1f404e81c48d7bde0701d89`（2.1.301，单个空白字符） |
| 已证明无展示 | assistant `[text(blank)]` stop=null | 11 | 4 | 2.1.215 – 2.1.295 | `ec073d6d4ad58d962f6d134bd70ad995de91ce09c18b0af071d42839e2697d25` / 373 / `[1692257,1693179)` / `41609fa86aeb46c1aba2d73956fa14726b5bc96316b39ec327d9159b81a157fb`（2.1.295） |
| 不支持 | assistant `[tool_result]` stop=`tool_use`（内容非空） | 4 | 0 | 2.1.181 – 2.1.209 | `e470595b29911a0663500f7e722ed34cec80760bf06cdf00b0afcb474c11f521` / 492 / `[1454628,1459037)` / `91d8066358c41fd50c51041334f11db2ff1ec49353cbab3d5577dda9d40bd95f`（2.1.197） |
| 不支持 | assistant `[tool_result]` stop=null（内容非空） | 1 | 0 | 2.1.258 | `648aeb500f8e41cca81bad6116e5d0ad9ee4404905fd28d70abf2775cc1bc226` / 39 / `[309268,425756)` / `7ed843795a0d52a36839842c1410ffaffea13a3e6af73a6a794504ddb1bf857a` |

- 评审 R1 §4 的样本 S1–S6 与上表互相独立、形态一致。其中 S2 是空白 thinking 且 `signature:""`，S3 是 `text:"\n"`，S4 是非空 assistant/tool_result，S5 是对象 error，S6 是字符串 error。
- 文件哈希取自运行时刻的原始字节；活跃文件之后还会增长，复核时以「字节范围 + 记录哈希」为准。
- 原始文件留在 owner 本机，路径与正文均不入库。

### 3.3 上游锚点（Claude Code 闭源）

- 没有开源实现可对照。transcript 形态**只按 §3.2 的真实样本认定**，不用 SDK 显示模式去反推 CLI 私有字段（例如 `thinkingDisplay:"updates"`）的语义。
- 背景：`/Users/jacklee/Projects/claude-agent-sdk-npm/package/sdk.d.ts`（0.3.260，SHA-256 `c705122752d364856eafd2c1ea9e1b12000f3dc4f69f19b7d45e84e6b89e79a7`）`:2650-2657`；`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/build/upstream-claude-agent-sdk-0.3.301/package/sdk.d.ts`（0.3.301，npm 包解压产物，位于被 ignore 的 build 目录，SHA-256 `26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773`）`:2958-2969`、`:9878-9882`。这两处只说明官方存在「省略 thinking 正文」一类的显示控制。

### 3.4 组件级复现【scratch，生产代码路径，未当作通过断言】

- **RC-A**：真实 `claudecode.Agent` 加 `handleGetSessionProjection` 冷开，然后按外部形态追加「问题B → 空 thinking → tool_use → tool_result → attachment → 空 thinking → 中间说明」。
  - live reducer 中该 turn 为 `running parts=[]`；
  - Kernel 状态 `failed`，attempts=5；
  - 冷却期内重拉返回 `projection.hydrate_failed`；
  - 对照组（thinking 有文字）完整且保持 Ready。
- **RC-B**：追加对象 error 的 `system/api_error` 行后，出现 `quarantined invalid complete record`，之后的回复不再出现。
- 仓库内既有测试 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 的失败断言在 `claude_source_shapes_test.go:155`（函数始于 `:100`），是 RC-B 的现成红灯。
- scratch 文件：`/tmp/cr-batch-repro-tests/`，含两个测试文件，哈希见评审 §1.3。

---

## 4. 根因（源码锚点，绑定 `fd4777e6`）

### RC-A：零事件内容行被整行拒收，进而级联丢行

1. mapper 跳过空白 thinking（`handlers_relay.go:2390-2393`）；空白 text 同样不发事件；只有终态 stop_reason 才发 `turn_completed`（`:2475-2479`）。因此，只含空白块且 stop 非终态的 assistant 行映射出 0 个事件。
2. 首次出现的内容记录被 `ProposeClaudeSourceTransition` 判为 `ClaudeTransitionAcceptedProjection`（`claude_source_state.go:229-242`）。
3. `ApplyClaudeSourceRecordBatch` 在 `len(batch.Events)==0` 时拒收且**不提交**（`projection_source_transaction.go:133-135`），账本游标停在该行起点。同一函数中「有事件但 reducer 无变化」的情况则按 source-only 提交（`:174-182`）。
4. relay 的读取位置已前进（`handlers_relay.go:1159`），下一条内容行因此违反连续性栅栏，被判 gap（`projection_source_transaction.go:114-116`）而丢弃。
5. 下一条控制行的 `AcknowledgeClaudeSourceRange` 不检查连续性，直接把游标设到行尾（`:288-298`），悄悄越过被丢的行。
6. 每次拒收都调用 `markHydrateFailed(…, retryable)`（`handlers_relay.go:1407`），失败计数累加，`RetryAt` 按 1s→30s 退避（`projection_kernel.go:976`）。冷却期内拉取不被准入（`:1077`），iOS 收到 `projection.hydrate_failed`（`handlers_projection.go:146-182`；iOS `ChatViewModel.swift:740-787`）。
7. 2026-09-04 的 echo-only 行曾引发同类级联，当时在 relay 循环里点状特判（`handlers_relay.go:1199-1214`）。

### RC-B：`system/api_error` 行毒化扫描器

- 135ba20a（10-16）加入 `Error string \`json:"error"\``（`handlers_relay.go:1654`）。`system/api_error` 行的 `error` 是对象，`json.Unmarshal` 失败，完整记录扫描器据此判 poison（`:1946-1949`）。隔离循环（`:1234-1258`）每次都停在同一行，之后的行永远不会被消费。
- 其他解码点（`:1751`、`:1850`、`:2168`）遇到失败直接 `continue`，不会冻结。

---

## 5. 修复设计

### 5.1 RC-A-1：三分类，source-only 只走白名单

**分类函数**放在 `claude_source_mapper.go` 中 mapper 旁边，记为 `classifyClaudeSourceRowContent(entry)`。按块分类，再汇总成行：

| 行类别 | 判定（与 mapper 发事件条件同源） | 事务处理 |
| --- | --- | --- |
| **可投影** | assistant：任一块是非空白 text、非空白 thinking、`tool_use` 或 `server_tool_use`；或终态 stop_reason；或 `isApiErrorMessage`。user：含 `tool_result`，或归一化文本非空 | 现有路径不变；若事件为 0，属于分类器与 mapper 不一致，**拒收**并附诊断 |
| **已证明无展示**（白名单） | 仅限 assistant：至少有一个块；**每个块**都是空白 thinking（signature 有无均可）或空白 text；stop 非终态（含 null 或空）；不是 API error 行。依据为 §3.2 的 K1/K2 四种形态，共 3,515 行，0 违例 | 提交 transition 为 `AcceptedSourceOnly`：游标前进，logical record 与 graph node 照常记录（后续行的父链可经它解析）；reducer 不变，不投递 |
| **不支持**（默认分支） | 除上述两类外的一切零事件行：assistant/tool_result（§3.2 的 U1/U2、评审 S4）、空 content 数组、未知块类型（如以后的新块、`redacted_thinking`、image 等，未观测）、零事件 user 行（未观测） | **照旧拒收**（失败关闭），游标不动，附形态诊断（§5.2） |

- **混合块**沿用既有策略：只要含可投影块，就按可投影处理，不支持的块照旧被 mapper 忽略。本期不改。
- **echo-only 与 interrupt 特判**（`handlers_relay.go:1178-1214`）不动。
- **同源约束**：mapper 中 text/thinking 的「是否空白」判断与分类器调用同一个块级函数。T7 普查持续守护不变量「可投影 ⇔ 事件数 > 0」。

### 5.2 RC-A-2：只增诊断，不改失败与恢复语义

- 拒收的 warn 日志附上行形态：type、各块类型及是否空白、stop 类别、行类别、byteStart/byteEnd。不记录正文、signature、工具输入。
- 每代 relay 第一次按「已证明无展示」提交 source-only 时，打一条限流 INFO（每代一次），作为部署后的运行态特征（§8）。
- **v1 的「立即可重试」已撤回**：`MarkFailed`、冷却、ack 跳跃语义本期全部不变。

### 5.3 RC-B：`error` 字段按原始 JSON 解码

- `claudeTranscriptRelayEntry.Error` 改为 `json.RawMessage`，新增 `claudeRelayErrorText(raw)`。唯一读取点 `handlers_relay.go:2362-2365`（assistant `isApiErrorMessage` 行的消息兜底）改用该函数。
- **已观测的外部形态**（§3.2 与评审 S5/S6）：
  - 字符串：assistant `isApiErrorMessage` 行，原样返回；
  - 对象：含 `message`（以及 `formatted`、`connection`、`isNetworkDown`、`rateLimits`、`noResponse` 等），返回 `message`。
- **未观测形态，属内部策略，不宣称外部契约**：对象缺少 `message`、数组、数值、null 时返回空串；该行**照常解码、不 poison**。
- `system/api_error` 行的语义不变：`Message == nil`，不被 admission 接纳，只推进账本游标（ack），**不**产生 `turn_error`，回合继续。
- JSON 语法错误仍然 poison，失败关闭不放宽。

### 5.4 Phase 2（单独方案，本期不做）与剩余限制

- **Phase 2 范围**：live-ingest 失败的冷却语义；重开时在 Kernel/fence 内一致地重建投影、**重装账本到新 cut**（替换旧账本，见 `handlers_projection.go:1002-1004` 的 no-op）、**relay 读位置重绑**（`handlers_relay.go:129-132` 中运行中 relay 直接返回）；并发 append 与旧代事件的处置；视需要把 ack 改为严格连续、把 echo-only 并入事务级规则。
- **Phase 2 闭合测试**（按评审 F-02 提供）：拒收一次 → 中间没有控制行跳跃 → 立即重开 → hydrate 完成 → 再追加两条连续内容行。核对内容、账本与读取 cut、无 gap、无重复、状态正确；同时保留真实冷源错误的退避测试。
- **剩余限制**：见 §1 末段。本期之后，在当前 CLI 版本中没有观测到会进入「不支持」的形态（U1/U2 只出现在 2.1.258 及更早版本，B=0）。

### 5.5 实施顺序与并发修改

1. 等开发者的 RC-1 / RC-2 改动（§0 列出的 3 个文件）提交后，再开始本期实施；
2. 重做双仓来源门，按届时的提交重新取行号；
3. 先补测试（§7）并确认在未修改代码时为红，再改代码；
4. 只提交本期列出的文件。

---

## 6. SSV2 护栏核对

| 项 | 结论 |
| --- | --- |
| 真相 owner | Mac Projection Kernel（不变） |
| 唯一 writer | `ApplyClaudeSourceRecordBatch`（不变）；「已证明无展示」只提交账本，不写 reducer |
| 受影响事务域 | 仅 live（file relay）ingest；hydrate、窗口、明细不变 |
| 新增数据路径 | 无 |
| active 下全部写入口 | live batch、hydrate commit、ack，三者都不变 |
| 失败呈现 | 「不支持」行与分类器/mapper 不一致仍然拒收并标失败，与现状一致，并附形态诊断；恢复语义不变（§5.4） |
| 防双写 | relay 集成测试断言没有重复 part、重复正文 |
| 第二裁判 / 自动 fallback | 无：白名单只看块是否属于已证明无展示的形态，不比较、不打分、不回退 legacy |

---

## 7. 测试计划（未改代码时先红，改后转绿；fixture 必须有出处）

- **fixture 出处**：在 `testdata/claude-source-shapes/` 新增脱敏行，由 §3.2 与评审 S1–S6 的真实记录派生：正文替换为占位，signature 是否存在保持原样，版本保留。manifest 记录来源的「记录 SHA-256 + 字节范围 + CLI 版本」。不用 scratch 合成串代替协议证据。

| # | 层级 | 构造（真实 entry → 生产分类器 → mapper → Kernel） | 断言 |
| --- | --- | --- | --- |
| T1 | 分类器 + Kernel | K1/K2 的四种形态（空 thinking、空白 thinking 且 sig 为空、空白 text；stop 分别为 `tool_use` 和 null） | 分类为已证明无展示；`buildClaudeSourceRecordBatch` → `ApplyClaudeSourceRecordBatch` 的结果为 `AcceptedSourceOnly`；游标推进到 ByteEnd；GraphNodes / LogicalRecords 已记录；reducer 无变化；紧随其后的 tool_use 连续并被接受 |
| T2 | 分类器 + Kernel（负例） | ① 非空 assistant/tool_result（U1 派生）；② 未知块类型；③ 空 content 数组 | 分类为不支持；拒收；**游标不动**；诊断日志带行形态；不允许因分类器返回 false 而推进账本 |
| T3 | 终态空行 | 空 thinking 或空白 text，stop=`end_turn` | 分类为可投影（终态）；产生 `turn_completed`；走投影路径，回合完成 |
| T4 | relay 集成（生产路径，仿 `TestClaudeFileRelayModelSwitchAppendKeepsProjecting`） | 真实行序：2.1.301 形态与 2.1.304 形态（tool_use 与 tool_result 之间夹控制行） | 所有工具卡（含 title）、结果、中间说明、最终回复都出现；回合完成；0 次拒收；Kernel 保持 Ready；没有重复 part |
| T5 | 扫描器 / relay（RC-B） | ① 对象 error 的 `system/api_error`（S5 派生）后跟回复；② assistant `isApiErrorMessage` 字符串 error（S6 派生）；③ 对象缺 message、数组、数值 error（内部策略）；④ JSON 语法错误行 | 既有 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 转绿；① 回复实时出现，没有隔离，api_error 行只 ack、不产生 `turn_error`；② `turn_error` 文本与改前一致；③ 正常解码、文本为空；④ 仍然 poison |
| T6 | （v1 的「立即重试」测试已删除，移入 Phase 2） | — | — |
| T7 | realdata（tag，按需在本机运行） | 生产扫描器 + 分类器 + mapper 普查本机 transcript | 「可投影 ⇔ 事件数 > 0」违例为 0；输出三类计数，供人工比对 §3.2 |

- **回归测试族**：`claude_file_relay*`、`claude_content_relay*`、`claude_source*`、`projection_source_transaction*`、`TestHandleGetSessionProjection*`。后者包含评审复核 RC-1 的既有竞态失败，单列报告，不作为本期的通过依据。
- **验证等级**：D3。只动 Mac，iOS 不改动也不构建。

## 8. 部署与运行态验证

1. Release 构建 → 覆盖安装 `/Applications` → `killall CordCodeLink` 加 `pkill -f cordcode-bridge-runtime` → 确认 8777 由新 PID 监听、启动时间晚于构建、没有非 `/Applications` 的实例。
2. **特征输出**：旁观一个开着思考的外部会话时出现 §5.2 的限流 INFO；连续 ≥3 个工具周期内 `no projection subevents` 与 `source batch gap` 均为 0。
3. RC-B 在生产上依赖自然出现的 429，以 T5 的组件级证据为准，并注明证据等级。
4. **发布门**：评审复核的 RC-1（运行中会话冷开与 coverage 注册竞态）是该产品组合的独立发布门。本方案不包含它的代码，但发布前须单列它的验证结果。

## 9. owner 验收矩阵

| # | 前提条件 | 动作 | 应看到 |
| --- | --- | --- | --- |
| 1 | Mac 上用 Claude Desktop（开着思考的模型）跑一个会调用多次工具的任务；iPhone 与 Mac 已连接（Relay 或局域网均可） | iPhone 打开这个正在执行的会话，旁观到结束 | 每次工具调用都有卡片和结果，中间说明出现，最后出现回复并显示完成 |
| 2 | 同上，任务进行中 | iPhone 退回会话列表，再立刻点进同一个会话 | 立即打开，内容完整，没有「无法加载会话投影」 |
| 3 | 同上，任务进行中 | iPhone 切到后台约 10 秒，再切回 | 内容继续实时增长，没有缺失 |
| 4 | Mac 空闲 | iPhone 在这个会话里发一条消息 | 与修复前一致：流式正常、完成正常（回归项） |

## 10. 回滚与风险

- **回滚**：`git revert`。账本 schema、checkpoint、协议都不变，不需要迁移。
- **风险 1：白名单过窄**，新的无展示形态会照旧拒收。缓解：形态诊断日志加上 T7 普查；只有拿到样本后才扩展白名单（失败关闭优先）。
- **风险 2：分类器与 mapper 漂移**。缓解：共用同一个块级函数，T7 守护不变量。
- **风险 3：同一 uuid 以不同内容再次出现**时，既有 prefix 规则会拒收（`claude_source_state.go:248-250`）。transcript 只追加，样本中未见；本期不改。

## 11. 需要评审 / owner 裁定

- **实施分支**：建议在 `feat/claude-long-session-projection-loading` 上实施，生产安装的就是它。本期文件与 main 的交集已在 §0 逐一核对；**不作「以后合入 main 无冲突」的保证**。owner 如需从 main 另开分支，需明确指定，之后的集成也由 owner 指令驱动（P0）。
- **Phase 2**（§5.4）何时立项，由 owner 排期。

## 附：评审复核报告勘误

`docs/2026-10-21-long-session-projection-loading-session-code-review-r1-recheck.md` §5 O1 曾推测既有失败测试「可能与工具链有关」，已在该报告中勘误：失败原因是 RC-B。
