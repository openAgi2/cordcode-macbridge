# Claude 外部会话旁观时实时更新中断——修复方案 v1（送审稿）

> **状态**：方案阶段 · 送审稿 v1。按本仓「方案评审循环与阶段门」，评审「通过」前不修改业务代码。本文所有结论来自只读排查与仓库外 scratch 复现。
>
> **一句话**：在 iPhone 上旁观 Mac 端正在跑的 Claude 会话（Desktop / Terminal 发起的外部回合）时，实时视图会丢失工具调用、工具结果和中间说明，会话还会被反复标成失败，活动期间重开会话会失败；遇到一次 API 错误（如 429）后，实时更新会彻底冻结。两处根因都在 `main` 上就存在，与长 session 投影加载分支无关。

---

## 0. 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=c585c2f20c1f87f6787abc81cf4ef13f34c763e6（排查起点；本文写入前未变）
未提交状态=干净（写入本文件前）
任务预期分支=无指定（排查对象是既有缺陷）；生产 runtime 由本分支构建，所以在本工作树取证
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / 64a386d68ae5e11f109b19bec85283bf2486b562（只读：确认 iOS 对失败码的呈现，本方案不改 iOS）
预期产品特性=Claude 外部回合 file relay 旁观：工具调用、结果、中间说明、最终回复都实时出现，状态正确收口
```

- **与 main 的关系**：`main` = `ea0627e3`，等于 merge-base `f868af18` 加 1 个文档提交。`git diff f868af18..HEAD` 在本方案涉及的文件中，只有 `go-bridge/claude_relay_coverage.go` 有改动（d78100ee / d88920e6，与本问题无关）。`handlers_relay.go`、`projection_source_transaction.go`、`claude_source_state.go`、`claude_source_mapper.go` 与 main 一致，所以结论同时适用于 main 和当前运行的二进制。
- **运行态**：`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，内嵌 `d88920e692b3`，`go1.26.2` 构建，PID 82698（12:10:29 起）。
- **证据类别**：【生产日志】只读 grep；【真实样本】本机 `~/.claude/projects` transcript，只统计形态，不复制正文；【scratch】仓库外 `/tmp` 的 `git archive` 副本中跑定向测试；【源码】行号取自上面的提交；【推断】单独标注。

---

## 1. 现象与影响（产品语言）

| 场景 | 现在看到的 | 应该看到的 |
| --- | --- | --- |
| iPhone 打开 Mac 上正在执行的 Claude 会话（模型开着思考） | 用户提问出现；之后的工具调用卡片、工具结果、中间说明大量缺失；最终回复通常能出来 | 每一步按顺序实时出现，最后显示完成 |
| 同一场景下，回到列表再进入、App 切回前台、断线重连 | 活动期间重开常被拒（`projection.hydrate_failed`，可重试），iOS 进入重试 loading，退避上限内仍失败时提示「无法加载会话投影」 | 立即打开，内容完整 |
| Mac 端回合中出现一次 API 错误（429 / overloaded） | 该会话的实时更新**完全冻结**（之后什么都不来），只能等关闭重开 | 错误之后的内容照常实时出现 |

触发面：只影响 **file relay 旁观的外部回合**（Mac Desktop / Terminal 写 transcript）。iPhone 自己发起的回合，正文由 stdout 供给（`handlers_relay.go:1215-1222` 的 `stdout_owns_assistant_content` 分支），不受第一个根因影响。

---

## 2. 交互四拍（修复后应满足）

1. **打开**：iPhone 会话列表 → 点开一个 Mac 端正在执行的 Claude 会话。
2. **输入**：iPhone 不输入；Mac 端 Claude 继续执行，逐行写入 transcript（thinking、tool_use、tool_result、attachment、api-request……）。
3. **执行路径**：Mac file relay 逐行读取 → 每行走 Kernel 私有 source-batch 事务（`ApplyClaudeSourceRecordBatch`，唯一投影写者）→ `projection_patch` 推给 iPhone。路径本身不变，不新增第二条。
4. **过程展示**：iPhone 按顺序出现工具卡片（标题 + 输入）、工具结果、中间说明、最终回复，状态从执行中变为完成。没有可显示内容的行（空 thinking、空 text）只推进 Mac 私有账本，不在屏幕上产生任何东西。真正无法映射的行仍然失败关闭，但用户重开会话必须**立即**得到完整的冷重建，不能被退避窗口挡住。

---

## 3. 证据

### 3.1 生产日志【生产日志】

| 日志 | 会话 | 「no projection subevents」 | 「source batch gap」 | 时间窗 |
| --- | --- | --- | --- | --- |
| `go-bridge.log`（本分支 runtime） | `b66193d7`（本次排查所在会话，iPhone `full_stream` 旁观中） | 131 | 254 | 10-21 14:46:29 → 15:36:23，仍在持续 |
| `go-bridge.log.1` | `608e03af` | 16 | 31 | 10-20 11:56:42 → 12:51:12 |
| `go-bridge.log.3`（10-13/14，早于本分支） | `bbe396e6` | 14 | 33 | 10-13 20:51:37 → 21:31:55 |

每次拒收都伴随 `projection hydrate failed code=projection.source_batch_rejected retryable=true`。保留日志中 `quarantined invalid complete record` 出现 0 次，说明 RC-B 在生产上尚未被旁观会话撞到。

### 3.2 触发行形态（两种独立方法核对）【真实样本】

- **方法一（Python 直读 transcript）**：`b66193d7` 共有 519 条含 thinking 的 assistant 行，其中 **495 条是「只有 thinking 块、文字为空、仅带 signature」**，transcript 字段为 `thinkingDisplay:"updates"`，版本 2.1.299 / 2.1.304。拒收时刻前的行与日志一一对应：
  - `bbe396e6` 12:51:33Z 的 `thinking(text=0)` 行（2.1.295）→ 20:51:37 拒收；
  - `608e03af` 03:56:39Z 与 03:57:05Z 的 `thinking(text=1)`（单个空白字符，2.1.301）→ 11:56:42、11:57:09 拒收。
- **方法二（Go：生产扫描器 + 生产内容行判定 + 生产 mapper）**：对本机 14 天内的 516 个 transcript、共 144,390 条内容行运行 `scanCompleteClaudeRelayEntriesFromReader` 与 `claudeEntryToProjectionEvents`。**映射为 0 事件的内容行共 3,493 条，分布在 27 个文件**：

  | 行形态 | 行数 | 文件数 | 版本 |
  | --- | --- | --- | --- |
  | assistant `[thinking(empty)]` stop=`tool_use` | 3,222 | 14 | 2.1.219 – 2.1.304 |
  | assistant `[thinking(empty)]` stop=∅ | 236 | 4 | 2.1.219 / 2.1.295 / 2.1.299 |
  | assistant `[text(empty)]` stop=`tool_use` | 19 | 3 | 2.1.219 / 2.1.295 / 2.1.301 |
  | assistant `[text(empty)]` stop=∅ | 11 | 8 | 2.1.215 – 2.1.295 |
  | assistant `[tool_result]`（旧版本形态） | 5 | 4 | 2.1.181 – 2.1.258 |

  两种方法对主形态（空 thinking + 非终态 stop）的判断一致。没有发现 user 行进入 0 事件集合：echo-only 行已被 relay 特判过滤。
- **RC-B 形态**：14 天内 `type:"system", subtype:"api_error"` 且 `error` 为**对象**的行有 391 条；assistant 行的 `error` 是字符串，共 176 条。

### 3.3 上游锚点（Claude Code 闭源）

- Claude Code 没有开源实现可对照，按 CLAUDE.md 的「官方文档 + SDK 类型契约 + 真实样本」取证。
- SDK 类型契约：`/Users/jacklee/Projects/claude-agent-sdk-npm/package/sdk.d.ts`（0.3.260）`:2650-2657`，`setMaxThinkingTokens(…, thinkingDisplay?: 'summarized' | 'omitted' | null)`；0.3.301（`…/upstream-claude-agent-sdk-0.3.301/package/sdk.d.ts`）`:2958-2969` 增加了 `'highlights'`，`:9878-9882` 为 `ThinkingAdaptive.display?: 'summarized' | 'omitted'`。官方确有「不回传 thinking 正文」的显示模式，与样本中「thinking 为空、只有 signature」一致。
- transcript 上的 `thinkingDisplay:"updates"` 是 CLI 私有字段，SDK 中没有定义，**只按样本对待**。修复不依赖该字段的取值，只依赖「块内没有可显示文字」这一事实。

### 3.4 组件级复现【scratch，仓库外，生产代码路径】

- **RC-A**：真实 `claudecode.Agent` 加 `handleGetSessionProjection` 冷开，形成 Ready 投影和 file relay；随后以外部 CLI 的形态追加「问题B → 空 thinking → tool_use → tool_result → attachment → 空 thinking → 中间说明」。
  - live reducer 中该 turn 为 `status=running parts=[]`：工具卡、结果、中间说明全部缺失。
  - Kernel 进入 `phase=failed`，attempts=5，约 12s 后才允许重试。
  - 此时重拉返回 `projection.hydrate_failed`（`Claude source batch gap`）。
  - 对照组（thinking 有文字）：`parts=[reasoning, tool(toolu_1, completed, "ls"), text("中间说明")]`，Kernel 保持 Ready，重拉正常。
  - 空白 thinking（`" "`）、2.1.304 形态（tool_use 与 tool_result 之间夹着 last-prompt 等非内容行）同样触发。
- **RC-B**：同一冷开流程下追加「问题B → `system/api_error`（`error` 为对象）→ 回复B」，日志出现 `claudeSessionFileRelay quarantined invalid complete record` 与 `projection.source_poison_record`，「回复B」始终不出现，turn 一直处于 running。
- 仓库内既有测试 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest`（`claude_source_shapes_test.go:155`）在 main 与本分支上都失败，报错为 `system-subtypes.jsonl scanner/manifest mismatch … poison=&{ByteStart:1118 ByteEnd:1337}`，被毒化的正是 fixture 里那条 `api_error` 行。这条既有测试就是 RC-B 的现成红灯。

---

## 4. 根因（源码锚点）

### RC-A：零事件内容行被整行拒收，进而级联丢行

1. mapper 跳过空或空白 thinking（`handlers_relay.go:2390-2393`），只在终态 stop_reason 时才发 `turn_completed`（`:2475`）。因此「仅空 thinking + stop=`tool_use`/∅」的 assistant 行映射出 **0 个事件**。从产品角度看这是正确的：没有内容可显示。
2. 首次出现的内容记录会被 `ProposeClaudeSourceTransition` 判为 `ClaudeTransitionAcceptedProjection`（`claude_source_state.go:229-242`）。
3. `ApplyClaudeSourceRecordBatch` 在 `len(batch.Events) == 0` 时直接拒收，并且**不提交 transition**（`projection_source_transaction.go:133-135`），账本游标停在该行起点。对照之下，同函数中「有事件但 reducer 无变化」的情况会按 source-only 提交（`:174-181`），两者处理不对称。
4. relay 的文件读取位置已经前进（`handlers_relay.go:1159`），下一条内容行因此违反连续性栅栏，被拒为 `Claude source batch gap`（`projection_source_transaction.go:114-116`）：工具调用、中间说明就这样丢失。
5. 下一条非内容行（attachment、last-prompt、api-request……）执行 `AcknowledgeClaudeSourceRange` 时**不检查连续性**，直接把游标设到该行末尾（`projection_source_transaction.go:288-298`），等于悄悄跳过了被丢的行。直到下一条空 thinking 出现，同一循环再来一遍。
6. 每次拒收都会调用 `markHydrateFailed(…, retryable)`（`handlers_relay.go:1407`）→ `MarkFailed`，失败次数累加，`RetryAt` 按 1s→30s 退避（`projection_kernel.go:976`；`session_sync_v2_policy.go:11-13`）。在窗口内拉取会被拒（`projection_kernel.go:1077`），iOS 收到 `projection.hydrate_failed` 且 retryable（`handlers_projection.go:146-182`），进入 disaster pull loop，或显示「无法加载会话投影」（iOS `ChatViewModel.swift:740-787`）。外部会话活跃时，每个工具周期都会重新武装这个窗口。
7. **同类问题第二次出现**：2026-09-04 的 echo-only 行引发过同一级联（`handlers_relay.go:1199-1213` 的注释），当时是在 relay 循环里点状特判修复的。新的零事件形态（空 thinking）再次落入同一个缺口。

### RC-B：`system/api_error` 行毒化扫描器，实时更新冻结

- 135ba20a（10-16，「settle API errors」）给 `claudeTranscriptRelayEntry` 加了 `Error string \`json:"error"\``（`handlers_relay.go:1654`）。`system/api_error` 行的 `error` 是对象，`json.Unmarshal` 因类型不符失败。完整记录扫描器把它判为 poison（`:1946-1949`），隔离循环（`:1234-1258`）每次重扫都停在同一行，之后的行永远不会被消费。
- 其他解码点（`:1751`、`:1850`、`:2168`）遇到解码失败会 `continue` 跳过，不受影响；只有 live 完整记录扫描器会冻结。

---

## 5. 修复设计

### 5.1 RC-A-1（必须）：没有可投影内容的内容行，按 source-only 提交

- **单一判定**：把 `claudeTagSourceBlockOrdinals`（`claude_source_mapper.go:137-174`）里的块级 `meaningful` 规则提成一个函数，例如 `claudeSourceBlockProjects(entryType, block)`，再在其上实现行级函数 `claudeSourceRowHasProjectableContent(entry)`。行级规则与 mapper 完全同源：
  - assistant：任一块满足 text 非空白、thinking 非空白、`tool_use` / `server_tool_use`；或终态 stop_reason；或 `isApiErrorMessage`；
  - user：含 `tool_result`，或归一化文本非空；interrupt 与 echo-only 已在上游特判，保持不变。
- **记录携带判定结果**：`buildClaudeSourceRecordBatch` 计算该判定，结果写入 batch（例如 `Record.Projectable`）。
- **事务分流**：`ApplyClaudeSourceRecordBatch` 中 `len(events) == 0` 时：
  - `!Projectable`：`CommitClaudeSourceTransition(proposal)`，结果为 `AcceptedSourceOnly`。游标前进，logical record 与 graph node 照常记录，后续行的父链（H4）可以经由该 thinking 行解析到正确的 turn。reducer 不变，不投递任何东西。
  - `Projectable`（mapper 不一致）：**保持现状拒收**，失败关闭（护栏 #10）。
- **不改动的部分**：echo-only 特判（`handlers_relay.go:1214`）在本期不动，原因是改为事务内 source-only 会让 echo 行进入 GraphNodes、可能影响后续 turn 归属，需要单独评估（见 §5.4）。

### 5.2 RC-A-2（必须，最小止损）：live 失败不得封锁用户重开，并且要可诊断

- **立即可重试**：live-ingest 的三类失败（`projection.source_batch_rejected`、`projection.source_batch_build_failed`、`projection.source_state_missing`）改用「失败但立即可重试」的标记：`RetryAt = now`，`failureAttempts` 不参与冷源退避。理由是这时冷源（transcript）是健康的，退避只会挡住修复路径。下一次拉取直接冷重建，得到完整内容。冷源本身失败（解析/IO）的退避语义不变。
- **形态日志**：拒收的 warn 日志附上行形态（type、各块类型及是否为空、stop 类别、byteStart / byteEnd），**不记录正文**。今天这些信息只在 env 开关控制的 trace 里出现。
- **运行态特征**：每代 relay 第一次把「无可投影内容行」按 source-only 提交时，打一条 INFO（限流，每代一次），作为部署后的唯一特征输出。
- **本期不改 `AcknowledgeClaudeSourceRange` 的跳跃语义**。在缺少「服务端通知客户端重新对齐」通道的前提下，改成严格连续会让旁观视图在任何拒收后完全冻结，比现在更糟。这一隐患连同完整的诚实重同步设计放到 §5.4。

### 5.3 RC-B（必须）：`error` 字段按原始 JSON 解码

- 把 `claudeTranscriptRelayEntry.Error` 改为 `json.RawMessage`，新增 `claudeRelayErrorText(raw)`：字符串直接使用，对象取 `.message`（为空时取 `.type`），其他形态返回空。
- 唯一读取点 `handlers_relay.go:2365`（API 错误消息兜底）改用该函数，135ba20a 引入的行为保持不变。
- JSON 语法错误仍然 poison，失败关闭不放宽。

### 5.4 明确不在本期（单独方案）

- **诚实重同步**：拒收后暂停该代 relay 的 ingest → 立即冷重建 → 账本在新 cut 处重装（替换旧账本，而不是 `ensureClaudeSourceStateInstalled` 的 no-op，见 `handlers_projection.go:1002-1004`）→ relay 在新 cut 处重新绑定 → 客户端重新对齐；同时把 ack 改为严格连续。这一项涉及 relay 生命周期与 SSV2 对齐通道，需要独立设计和评审。
- **echo-only 特判并入事务级规则**：见 §5.1 的说明。
- **既有的 turn 身份二元性**：冷重建用 `user-line-N`，live 用行级 uuid。本问题不改变它。

---

## 6. SSV2 护栏核对

| 项 | 结论 |
| --- | --- |
| 真相 owner | Mac Projection Kernel（不变） |
| 唯一 writer | `ApplyClaudeSourceRecordBatch`（不变）；source-only 提交不写 reducer |
| 受影响事务域 | 仅 live（file relay）ingest；hydrate、窗口、明细不变 |
| 新增数据路径 | 无 |
| active 下全部写入口 | live batch（不变）、hydrate commit（不变）、ack（不变） |
| 失败呈现 | mapper 不一致仍拒收并标失败（带形态日志）；用户重开立即冷重建，不被退避窗口挡住 |
| 防双写 | relay 集成测试断言没有重复 part 和重复正文 |
| 第二裁判 / 自动 fallback | 无：判定只看块是否有可显示内容，不比较、不打分、不回退 legacy |

---

## 7. 测试计划（每条都要先红后绿）

| # | 层级 | 构造 | 断言 |
| --- | --- | --- | --- |
| T1 | Kernel 单元 | 首次出现的 assistant 记录，只含空 thinking，stop=`tool_use` | 结果为 `AcceptedSourceOnly`；游标推进到 ByteEnd；GraphNodes / LogicalRecords 已记录；reducer 无变化；紧随其后的 tool_use 连续并被接受 |
| T2 | Kernel 单元 | Projectable=true 但 0 事件（构造 mapper 不一致） | 仍拒收，游标不动（失败关闭不回退） |
| T3 | relay 集成（生产路径，仿 `TestClaudeFileRelayModelSwitchAppendKeepsProjecting`） | 真实形态周期：空 thinking、空白 thinking、stop=∅、`text(empty)`、2.1.301 / 2.1.304 两种行序 | 所有工具卡（含 title）、工具结果、中间说明、最终回复都出现；turn 为 completed；0 次拒收；Kernel 保持 Ready；没有重复 part |
| T4 | handler 端到端（scratch 正式化） | 冷开 → 外部增长 → 立即重拉 | live reducer 内容完整；重拉成功；内容与冷重建一致（turn ID 二元性按既有差异处理） |
| T5 | 扫描器 / relay | `system/api_error`（对象 error）后跟回复；另加一条 JSON 语法错误行 | 既有 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 转绿；回复实时出现、没有隔离；语法错误行仍 poison |
| T6 | Kernel / relay | 用 `claudeSourceBatchFaultHook` 强制一次拒收 | 状态为 failed 且 `RetryAt ≈ now`；随后拉取立即冷重建成功（不返回 `projection.hydrate_failed`） |
| T7 | realdata（tag，可选） | 生产扫描器 + mapper 普查本机 transcript | 「Projectable 但 0 事件」的行数为 0（防 mapper 规则漂移） |

回归测试族：`claude_file_relay*`、`claude_content_relay*`、`claude_source*`、`projection_source_transaction*`、`TestHandleGetSessionProjection*`。注意后者含评审复核 RC-1 的既有竞态失败，与本方案无关，需单列。验证等级为 D3，只动 Mac；iOS 无改动，不构建 iOS。

---

## 8. 部署与运行态验证

1. Release 构建 → 覆盖安装 `/Applications` → `killall CordCodeLink` 加 `pkill -f cordcode-bridge-runtime` → 确认 8777 由新 PID 监听，启动时间晚于构建，且没有非 `/Applications` 的实例。
2. **特征输出**：旁观一个开着思考的外部会话，日志中出现 §5.2 的 source-only INFO；连续 ≥3 个工具周期内 `no projection subevents` 与 `source batch gap` 均为 0。
3. RC-B 的生产验证依赖自然出现的 429，无法强制触发；以 T5 的组件级证据为准，并在报告中注明证据等级。

## 9. owner 验收矩阵

| # | 前提条件 | 动作 | 应看到 |
| --- | --- | --- | --- |
| 1 | Mac 上用 Claude Desktop（开着思考的模型）跑一个会调用多次工具的任务；iPhone 与 Mac 已连接（Relay 或局域网均可） | iPhone 打开这个正在执行的会话，旁观到任务结束 | 每次工具调用都有卡片和结果，中间说明文字出现，最后出现回复，状态变为完成 |
| 2 | 同上，任务进行中 | iPhone 退回会话列表，再立刻点进同一个会话 | 立即打开，内容完整，没有「无法加载会话投影」 |
| 3 | 同上，任务进行中 | iPhone 切到后台约 10 秒，再切回 | 内容继续实时增长，没有缺失 |
| 4 | Mac 空闲 | iPhone 在这个会话里发一条消息 | 与修复前一致：流式正常、完成正常（回归项） |

## 10. 回滚与风险

- **回滚**：`git revert` 本次提交即可。账本 schema、checkpoint、协议都没有变化，不需要数据迁移。
- **风险 1**：判定规则与 mapper 漂移，可能导致有内容的行被误判为 source-only。缓解：两者共用同一块级函数，并加 T7 普查守护。
- **风险 2**：「立即可重试」可能让某个未来未知形态引发频繁冷重建。缓解：冷重建只在客户端拉取时发生，iOS disaster loop 自带 1s→30s 退避；冷源失败的退避不受影响。
- **风险 3**：同一 uuid 以不同内容再次出现时，既有 prefix 规则会拒收（`claude_source_state.go:248-250`）。transcript 只追加，样本中未见这种情况；本期不改。

## 11. 需要评审 / owner 裁定

- **实施分支**：建议直接在 `feat/claude-long-session-projection-loading` 上实施，理由是生产安装的就是它，涉及文件与该分支的改动没有交集，以后合入 main 不会冲突。owner 若希望改为从 main 另开修复分支，需要明确指定，之后的集成也由 owner 指令驱动（P0）。
- **§5.2「立即可重试」是否纳入本期**：建议纳入，它是用户「重开失败」这一症状的直接修复。

## 附：评审复核报告勘误

`docs/2026-10-21-long-session-projection-loading-session-code-review-r1-recheck.md` §5 O1 曾推测 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 的失败「可能与工具链有关」。本次排查证实这一推测不成立：失败是 135ba20a 引入的 `Error string` 解码回归，即本文的 RC-B，main 上同样存在。
