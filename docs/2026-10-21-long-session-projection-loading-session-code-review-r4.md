# Claude 长 session 投影加载分支——复核 R4（R3 工作单回执：重建身份 + v2.1 实施，独立 · 只读）

> **结论：代码层 `PASS`；测试与证据层有待修订 → 总评 `PASS-WITH-FINDINGS`。**
>
> - **部署身份已证明，R3-1 关闭**：用与产物相同的工具链（go1.26.2）从已提交的 `c308ad25` 重新编译 runtime，结果与 `/Applications` 中的文件逐字节一致（SHA-256 `007c58e2…bc77`）。已安装的 runtime 就是已提交代码，没有混入未提交改动。RC-1 发布门的部署层随之关闭。
> - **v2.1 生产代码与方案一致，未发现缺陷**：三分类、事务内 source-only、同源空白判断、RawMessage 解码都按方案实现。真实数据普查（517 个文件）中 poison 为 0；生产内容入口上仍会被拒收的只剩 8 行，全是 CLI 2.1.258 及更早的旧形态，近 14 天新写入的为 0。
> - **R4-1（重要）**：开发者的验收窗口里，RC-A 的触发输入根本没有出现。验收会话用的是 glm-5.3，它的 thinking 有正文，没有空白行，所以「验收窗 0 拒收」区分不了修好和没修好。本复核用另一个会话（本复核会话，Opus）补上了 Mac 侧生产证据：白名单 INFO 已出现，56 个空白 thinking 行，0 拒收。但 owner 在 iPhone 上的验收还没有覆盖 RC-A。
> - **R4-2（重要）、R4-3（次要）**：两处测试守护失效。T2 的第 ②③ 个负例被 gap 掩盖；T4 的 2.1.304 那一段没有被断言。
> - **其余**：完成报告和 CHANGELOG 有几处误读日志（R4-4），T7 普查的口径不完整（R4-5），另有若干细节（R4-6～R4-9）。
>
> **owner 下一步**：
> 1. 用会产生空白思考的模型（例如本机 Claude Desktop 的 Opus 会话），补做方案 §9 的第 1、2 项验收；
> 2. 让开发者按 §6 修订测试、fixture 和文档。这些修订不改生产代码，**不需要重新构建或安装**。

---

## §0 来源清单（实测时点）

| 门点 | 时点（CST） | 结果 |
| --- | --- | --- |
| ① 读源码前 | 2026-10-21 18:50:18 | Mac HEAD `6ca44e73`、iOS HEAD `6c56144e`，两仓干净 |
| ② 写本文件前 | 2026-10-21 19:09:10 | 两仓 HEAD 未变、干净；目标文件不存在 |
| ③ 构建/安装 | — | 不适用。仓库外做了一次可复现编译，只用来比对身份：产物写在 `/tmp`，不安装，不触碰 DerivedData |

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=6ca44e73d9bb2d4c02be87ad0757a263e0566d44
未提交状态=干净
任务预期分支=回执指定的复核范围 97958344..6ca44e73
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / 6c56144e86ea1f54f1c426353062a87e37b6060e（干净，范围内无新提交）
预期产品特性=RC-A 空白行白名单走 source-only；RC-B 的 error 字段按 RawMessage 解码；运行态特征=白名单限流 INFO
```

**运行态（只读）**

- runtime 进程：PID 35789，PPID 35760，`lstart 18:29:23`，监听 8777。
- 没有非 `/Applications` 的实例：`pgrep` 只匹配到本次查询自身的 shell。
- 二进制：`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`
  - SHA-256 `007c58e2c78731a1892f5cbe31e399112ac0839e77f7b75301d5a8cb7ed2bc77`，mtime 18:29:20；
  - 内嵌提交 `c308ad25017b`；
  - build info：go1.26.2、`-trimpath=true`、无 VCS 戳。

**证据类别**

- 【源码】绑定 `6ca44e73`（与 `c308ad25` 相比只差文档）。
- 【scratch】仓库外的 `git archive` 副本，包括可复现编译、变异实验和普查测试（`/tmp/cr-r4/`，用后删除）。
- 【生产日志】只读 grep。
- 【真实样本】本机 `~/.claude/projects`，只统计形态，不输出正文、signature 和路径。
- 【推断】单独标注。

---

## §1 复核范围

- **Mac** `97958344..6ca44e73` 共 3 个提交：
  - `d1275da9`：文档；
  - `c308ad25`：实施，13 个文件，+773/−15；
  - `6ca44e73`：文档。
  - 回执写的「4 提交……及你的 97958344 本身」已自行说明了计法，不计为问题。
- `c308ad25` 改动的生产文件只有方案允许的三个：`claude_source_mapper.go`、`projection_source_transaction.go`、`handlers_relay.go`。另有两个新测试文件和 testdata。
- `projection_kernel.go`、`handlers_projection.go`、`MacBridge/` 在区间内零改动。`c308ad25..6ca44e73` 只改了文档。
- **iOS**：`64a386d6..6c56144e` 已在 R3 复核（只改注释），本轮没有新提交。

---

## §2 逐项复核

### 2.1 工作单五步

| 步 | 回执声明 | 独立核验 | 结论 |
| --- | --- | --- | --- |
| 1 干净重建 | `97958344` 重建，PID 27003 | 已被第 4 步的部署取代，运行态无法再核；重启前留档的日志 `/tmp/cordcode-r3-logs/` 存在，权限 0600 | 记录在案 |
| 2 文档更正 | `d1275da9` | think.md 的 O2 行、§8 部署勘误、fixture 失败的定性都已更正 | 关闭（R3-4 的措辞见 R4-9） |
| 3 实施 v2.1 | `c308ad25` | 见 2.2、2.3 | 代码通过；两处测试守护失效 |
| 4 部署 v2.1 | `c308ad25017b`，SHA `007c58e2…`，PID 35789 | 见下方说明 | **R3-1 关闭** |
| 5 取证与对账 | `6ca44e73` | 见 R4-1、R4-4、R4-5 | 证据需更正 |

第 4 步的独立核验【scratch】：

- 工具链：`/opt/homebrew/bin/go` 是 go1.26.2，与产物的 build info 一致。
- 源码：`git archive c308ad25`。
- 参数：与 `project.yml` 相同，即 `-trimpath`，ldflags 为 version `0.1.0`、commit `c308ad25017b`、date `2026-10-21T10:28:16Z`。
- 结果：重编产物的 SHA-256 是 `007c58e2c78731a1…d2bc77`，与 `/Applications` 中的文件**逐字节一致**。
- 时间线：进程 lstart 18:29:23，晚于构建（18:28:16），构建又晚于提交（18:28:10）。

### 2.2 实现与方案逐条对照【源码】

| 方案要求 | 实现 | 核验 |
| --- | --- | --- |
| §5.1 三分类与白名单 | `classifyClaudeSourceRowContent`（`claude_source_mapper.go:47`） | 只限 assistant；至少一个块；每块都是空白 thinking 或空白 text；stop 非终态，复用 `isFinalClaudeStopReason`；不是 `isApiErrorMessage`；未知块判「不支持」。与方案一致 |
| source-only 的决定在事务内 | `ApplyClaudeSourceRecordBatch` 的零事件分支（`projection_source_transaction.go:141`） | 判定顺序：区间 → gap → proposal → 图节点类 source-only → 零事件分支。白名单行走 `CommitClaudeSourceTransition` 并标 `AcceptedSourceOnly`，与既有 `applied==0` 分支写法相同。冷却和 ack 都没动 |
| 同源空白判断 | `claudeBlockIsBlankDisplay`（`:33`） | mapper、tagger、分类器三处共用 |
| §5.2 诊断与限流 INFO | 拒收错误带 rowClass、块形态和 stop；每代 relay 打一次 INFO（`handlers_relay.go:944`） | 不含正文、signature、工具输入；生产上已出现（见 R4-1） |
| §5.3 RawMessage | `Error json.RawMessage`（`:1679`）加 `claudeRelayErrorText`（`:1711`），只有一个读取点 | 其余三个解码点（`:1798`、`:1897`、`:2215`）解码后都先执行 `Message == nil → continue`，system/api_error 行的行为不变；JSON 语法错误仍然 poison |

### 2.3 独立红绿与变异【scratch】

| 实验 | 结果 |
| --- | --- |
| HEAD 不变异 | 回归族跑两轮，全绿（7.0s、5.1s），覆盖 `TestClaudeFileRelay`、`TestClaudeContentRelay`、`TestClaudeSource`、`TestProjectionSourceTransaction`、`TestHandleGetSessionProjection` 和新用例；`./agent/claudecode` 全绿；`go vet` 通过 |
| E4：撤回白名单分支 | T1 单跑 FAIL（`no projection subevents: rowClass=proven_no_display …`）；T4 FAIL |
| E5：模拟 RC-B 回退（对象 error 判为 poison） | T4 FAIL；`TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` FAIL |
| M-T2：只把 `redacted_thinking` 和空 content 改判为白名单 | 相关主题测试（`Claude\|Blank\|Unsupported\|SourceShape\|Relay\|Mapper\|ProjectionSource\|Classif`，21s）**全部 PASS** → R4-2 |
| M-T4：把 turn C 的 tool_use 行换成不支持形态 | 原 T4 **PASS**；按 turn 分设标志的 scratch 测试 FAIL → R4-3 |
| 加强版 T4，不变异 | turn C 的 Bash 卡片、tool-c1 结果、「回复C占位」、回合完成全部出现，PASS。生产行为是对的 |
| corpus oracle H4 | 新 fixture 中可解析的 assistant 行为 5+1+3+0=9，12+9=21，与脚本计算一致；`streaming==indexed` 交叉校验通过 |

---

## §3 发现

### R4-1（重要，证据）：开发者的验收窗口撑不起 RC-A 的「生产路径」结论；本复核已用另一会话补上 Mac 侧证据

**证据**

- 【真实样本】验收会话 ce5ca3c8 在 18:29–18:38 的 33 个 assistant 行，模型全是 `glm-5.3`。thinking 都有正文，空白 thinking 或空白 text 行为 0。
- RC-A 的触发条件就是这类空白行。修复前，这样的输入同样不会被拒收，所以「验收窗 0 拒收」区分不了修好和没修好。
- 完成报告一边写「本窗口 40 新行 0 空白形态」，一边把「RC-A 拒收消除」标成生产路径，两处自相矛盾。
- CHANGELOG 和完成报告用 b66193d7 的 146/281 作「修复前同强度对照」。但 b66193d7 是 Opus 会话，有空白 thinking，两边的模型和输入都不同，不是同强度对照。
- 方案 §9 第 1 项的前提是「开着思考的模型」，本意是会写出空白 thinking 行的形态。这次验收用的模型不产生这种形态，所以 owner 的 UI 验收实际上没有覆盖 RC-A。

**本复核补充的生产证据**【生产日志 + 真实样本】

- 环境：同一 runtime 代际（PID 35789，`c308ad25`），会话 b66193d7（即本复核会话，模型 `claude-opus-5-5`）。
- 18:50:19 出现白名单限流 INFO：`rowClass=proven_no_display type=assistant blocks=[thinking:blank] stop=tool_use`。
- 18:48–19:09，该会话新写入 56 个空白 thinking 行、63 个 tool_use 行、62 个 tool_result 行。
- 同期整份日志中，`Claude source batch rejected`、`source batch gap`、`no projection subevents`、`projection hydrate failed`、`quarantined invalid complete record` 都是 0。relay 自 18:32:01 启动后一直在运行，没有退出记录。
- 同一会话修复前的数字：14:46–15:59 共 146 次 `no projection subevents`、281 次 gap（方案 §3.1）。这才是同会话、同模型的对照。
- 证据等级：**生产路径（Mac 侧）**。iPhone 端的显示没有观测到——这段时间没有客户端在看这个会话的实时推送。

**处理建议**

1. 更正完成报告 §1、§5 和 CHANGELOG 中的对照与证据等级；
2. owner 用会产生空白 thinking 的模型补做方案 §9 第 1、2 项。

### R4-2（重要，测试）：T2 的第 ②③ 个负例被 gap 掩盖，F-01 的闭合测试只有 1/3 起作用

- **机制**：
  - `unsupported-zero-event.jsonl` 中第一个负例（assistant 行里的 tool_result）被拒收后，游标不动；
  - 第 2、3 行的起点因此不连续，在 gap 检查处就被拒收了，根本走不到分类器分支；
  - 测试的断言只要求「拒收数 = 3」，以及「至少一条错误含 rowClass」。
- **变异**：只把 `redacted_thinking` 和空 content 改判为白名单，相关主题测试全部通过。仓库里也没有直接断言 `classifyClaudeSourceRowContent` 的单测。
- **影响**：生产代码现在是对的。但 F-01（未知形态必须失败关闭）是方案评审的 P1 阻塞项，它的回归守护目前只覆盖了 1/3。
- **建议**：
  - 每个负例单独构造：用新的 kernel，或让它紧跟在已接受的行之后；逐条断言错误中的 `rowClass=unsupported` 和对应的块形态；
  - 补一组分类器的表驱动单测，覆盖四种白名单形态、未知块、空 content、混合块、终态空行、API error 行和 user 行。

### R4-3（次要，测试）：T4 的 2.1.304 那一段断言空转

- **机制**：turn B 或 turn C 完成都会把 `sawCompletedC` 置真。循环在 turn B 的部件齐全后就退出了，turn C 的 Bash 卡片、tool-c1 结果和「回复C占位」都没有被检查。
- **变异**：把 turn C 改坏后，原 T4 仍然 PASS。
- **生产行为是对的**：按 turn 分设标志的 scratch 测试在 HEAD 上通过。
- **建议**：
  - turn B、turn C 各设一个完成标志，并补上 turn C 的部件断言；
  - 按方案 §7 补上 title、Kernel Ready、无重复 part 的断言。

### R4-4（次要，证据表述）：完成报告 §5 与 CHANGELOG 有四处误读日志

1. **「7 次 hydrate 全 settled=true」**：7 条 `checkpoint_stage` 实际全是 `settled=false`。`settled` 的定义是 `Execution.Phase != "running"`（`handlers_projection.go:1242`），运行中的会话本来就是 false，这不是故障，但回执把值写反了。
2. **「账本 sourceCursor 连续推进至 17,259,893」**：日志里的 `sourceCursor` 是每次冷 hydrate 的起始 cut（`source.Cursor = admission.StartCut`，`:1225`），不是 live 账本的游标，证明不了 live ingest 是连续的。「余下 23.9KB 在飞」的解释也不成立。
3. **「投影侧 41/41 可投影块全部进事务（n/N）」**：这不是投影侧的计数，而是从 0 拒收推出来的。况且这个窗口没有触发输入（R4-1），修复前同样会是 0 拒收。如实的写法是：transcript 侧有 41 个可投影块；投影侧没有取得逐 part 计数，生产侧只能看到 0 拒收。
4. **RC-1 的「7 次 hydrate」**：
   - 这个会话的 relay 只在 18:31:48.522 启动过一次；
   - 只有第一次打开处在竞态窗口内：18:31:48.521 收到请求 → relay 启动 → 18:31:49.141 coverage liveBound → 18:31:49.146 hydrate 提交，执行态为 running；
   - 其余 6 次打开时 relay 已经在运行；
   - 所以 RC-1 的生产证据是 **n=1**：结论成立，但样本很小。

CHANGELOG 里「41/41 可投影块全部进入投影」和「修复前同强度对照」两句是面向用户的，需要同步更正。

### R4-5（次要，普查）：T7 的口径和不变量检查都不完整，「705 与方案量级一致」不成立

**T7 本身的问题**

- realdata 测试对全部 admitted 行做分类；
- 只检查了「可投影 ∧ 0 事件」和「白名单 ∧ 事件 > 0」，漏了「不支持 ∧ 事件 > 0」这个方向；
- 注释说会统计 poison 文件，实际没有输出。

**本复核重做的普查**【scratch + 真实样本】：按 relay 循环真实的内容入口统计，即 admitted、非 interrupt、有 UUID 和 Message、user 或 assistant、非 echo-only。

- **输入**：截止 2026-10-07T19:00+08:00，517 个文件，1,241,450,911 字节全部扫完。**poison 记录为 0**；修复前有 64 个文件在 api_error 行处被截断。
- **全 admitted 口径**：可投影 196,861 / 白名单 3,639 / 不支持 705，与开发者的量级吻合。
- **705 行拆开看**：
  - 401 行是 compact_boundary 系统行（没有 message），不走事务；
  - 296 行是 echo-only user 行，也不走事务；
  - 真正会进事务的零事件内容行只有 8 行。
- **生产内容入口**：
  - 共 200,405 行；
  - 零事件行 3,647 行，其中 3,639 行现在走白名单 source-only；
  - **8 行仍会被拒收**，全是 `assistant [tool_result]`（U1/U2 形态），CLI 版本 2.1.181–2.1.258，B 分母为 0。
- **不变量**：两个方向的违例都是 0。

这与方案 §3.2/§5.4 的判断一致：残余拒收只出现在旧版本 CLI。

**建议**：T7 改为按内容入口统计、检查两个方向，并输出 poison 计数；完成报告改用上面的口径。

### R4-6（细节，fixture 出处）：manifest 中有出处标注与源记录不符

- **signature 标注不符**：
  - `blank-display-blocks` 中「blank thinking stop=null ← `88a7884a…` 行 10（CLI 2.1.299，signature absent）」；
  - 本复核按字节范围和记录哈希定位到这条源记录，它的 signature 长度为 1784，不是空的，与复审 R2 的亲核一致；
  - fixture 中对应的那一行（stop=null）没有 signature 字段，所以「signature presence per source preserved」不成立；
  - 评审 R1 的 S2 才是 `signature:""` 的样本。应改为引用 S2，或者给这一行补上 signature 占位。
- **未观测形态被标为「真实形态派生」**：
  - `api-error-shapes` 整个文件标为 `observed-derived-real-shape`，但其中的数组、数值、null、缺 message 四种都是方案明确写为「未观测、属内部策略」的形态；
  - 对象和字符串两类也没有逐条给出记录哈希、字节范围和版本；
  - `unsupported-zero-event` 中的空 content 行同样是未观测形态。
- **影响**：只涉及出处标注。分类器不看 signature，行为覆盖不受影响。

### R4-7（细节，测试卫生）：T4 失败时会 panic，拖垮整个测试包

- **问题**：读循环在 `ReadJSON` 出错后 `continue`（`claude_file_relay_test.go:926`）。gorilla/websocket 在读超时后连接就失效了，反复读取会 panic：`repeated read on failed websocket connection`。整个测试二进制随之退出，同一次运行中排在它后面的测试都不会执行，也不会报告。
- **实测**：E4 变异的组合运行中，T1–T3 就是因此没有执行。
- **建议**：这个写法沿用自同文件的 `:675`；同一文件的 `:773` 用的是 `break`，照改即可。

### R4-8（细节，格式）：`c308ad25` 引入 gofmt 漂移

- `gofmt -l` 列出两个文件：
  - `claude_source_mapper.go`：`RowClass:` 没对齐；
  - `handlers_relay.go`：`claudeTranscriptRelayEntry` 的字段对齐被插入的注释打断。
- 父提交 `d1275da9` 中这两个文件都是干净的。CI 不检查 gofmt。
- gofmt 只调整格式，不改变编译产物。

### R4-9（细节）

- **死代码**：T1 的循环从 1 开始，里面的 `if i == 0` 永远不成立。
- **潜在 nil 解引用**：`claudeSourceRowShapeSummary`（`:101`）在块循环里直接解引用 `entry.Message`，nil 检查只出现在后面取 stop 的地方。目前唯一的调用方已保证 Message 非空，属潜在隐患。
- **R3-4 措辞说轻了**：`d1275da9` 把 R3-4 写成「无错误行为」。实际上新旧 loop 的生命周期重叠时，会回到修复前的 fail-closed，也就是冷开失败。不是假成功，但也不是「无错误」。
- **待澄清**：回执写「iPhone16 安装 `6c56144e`」。如果是本轮重新安装，就是工作单之外的动作（该提交只改注释，无害）。请写明是「核验已装版本」还是「重新安装」。

---

## §4 范围外观察（不计入结论）

- **O-A**（每次启动都清空 `go-bridge.log`）：未改动，与工作单一致。开发者已按工作单在每次重启前留档。
- **K4Patch 的两次 drop**（ce5ca3c8，18:32:10 和 18:35:11）：都发生在 hydrate 提交的同一毫秒，且 `syncRev = expectedRev = cutRev`，被丢的补丁已包含在快照中。这属于分支既有的 fence 逻辑，不在 v2.1 范围。
- **anchor-newer stale slice**：18:35:11 出现一次，客户端随即改走 snapshot（req_16 的 `outcome=snapshot`），这是分支设计中的降级路径。
- **`projection live session prepare failed … belongs to another backend`**：本代际出现 4 次；历史日志 `.log.1`、`.log.2`、`.log.3` 中分别有 37、47、26 次，属既有现象，与本轮无关。

---

## §5 总结论

- **代码层 `PASS`**：
  - v2.1 的生产代码与方案一致，未发现缺陷；
  - 部署身份已用可复现构建证明，R3-1 关闭；
  - RC-1 发布门：代码层（R3）和部署层（R4）都已关闭，生产行为证据为 n=1。
- **RC-A**：Mac 侧生产路径已证明（本复核，b66193d7）；owner 的 UI 验收需要补做（R4-1）。
- **RC-B**：真实数据普查中 poison 为 0（realdata 级证据）；生产上自然出现的 429 样本仍在等待。
- **测试与文档**：
  - R4-2、R4-3 两处守护失效，需要补测试；
  - R4-4、R4-5 的证据表述需要更正；
  - R4-6～R4-9 是细节。
- **修订范围**：以上修订只涉及测试、fixture、格式和文档，不改生产代码，**不需要重新构建或安装**，已部署的身份保持为 `c308ad25017b`。
- **本复核的边界**：没有修改业务代码，没有构建安装或部署，没有 push。仓库内只新增了本文件；仓库外的 scratch 目录（`/tmp/cr-r4/`）用后删除。

---

## §6 给开发者的修订单（可直接转发）

1. **测试**（只改测试和 fixture，不改生产代码）：
   - **T2**：
     - 每个负例单独构造：用新的 kernel，或让它紧跟在已接受的行之后；
     - 逐条断言错误中的 `rowClass=unsupported` 和对应的块形态；
     - 补一组 `classifyClaudeSourceRowContent` 的表驱动单测，覆盖四种白名单形态、未知块、空 content、混合块、终态空行、API error 行和 user 行；
     - 验收标准：把 `redacted_thinking` 或空 content 改判为白名单时，必须有测试变红。
   - **T4**：
     - turn B、turn C 各设一个完成标志，补齐 turn C 的 Bash 卡片、tool-c1 结果和「回复C占位」断言；
     - 按方案 §7 补上 title、Kernel Ready、无重复 part 的断言；
     - `ReadJSON` 出错时改为 `break`；
     - 验收标准：把 turn C 的 tool_use 改成不支持形态时，T4 必须变红，而且不会 panic。
   - **T7**：改为按 relay 的内容入口统计（排除 interrupt、echo-only 和没有 message 的行），检查不变量的两个方向，并输出 poison 计数。
   - **manifest**：
     - 改正 `88a7884a` 行 10 的 signature 描述：改为引用 R1 的 S2，或者给 fixture 补上 signature 占位；
     - `api-error-shapes` 中未观测的形态单独标注；对象和字符串两类补上记录哈希、字节范围和版本。
   - **格式与死代码**：对 `claude_source_mapper.go` 和 `handlers_relay.go` 跑 gofmt（只改格式，编译产物不变）；删掉 T1 里的死代码。
2. **文档**：
   - **完成报告 §1、§5 与 CHANGELOG**：
     - 删掉「验收窗 RC-A 拒收消除 = 生产路径」「41/41 全部进入投影」「修复前同强度对照」三种说法；
     - RC-A 的生产证据改为引用本报告 R4-1 中 b66193d7 的数字；
     - `settled` 和 `sourceCursor` 按 R4-4 更正；
     - RC-1 写明证据为 n=1。
   - **T7 结果**：按 R4-5 的口径重写——残余拒收 8 行，全是 CLI 2.1.258 及更早的旧形态，近 14 天为 0。
   - **R3-4 的措辞**：改为「新旧 loop 罕见重叠时会回到修复前的 fail-closed（冷开失败），不会产生假成功」。
   - **iPhone16**：写明是「核验已装版本」还是「本轮重新安装」。
3. **整理给 owner 的验收说明**：用会产生空白思考的模型（例如本机 Claude Desktop 的 Opus 会话）补做方案 §9 的第 1、2 项。
4. **本轮不做**：不改生产代码；不重新构建、不重新安装；不 push、不合并；Phase 2、R3-4、O-A 仍不在本轮范围。

**回执要求**：每项给出变异前后的红绿结果和所用命令；文档更正列出改动的位置。
