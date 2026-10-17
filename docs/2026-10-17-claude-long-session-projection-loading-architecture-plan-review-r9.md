# 评审报告 R9：Claude 长 session 打开架构优化方案（v8 第九轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R8 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v8**
方案 SHA-256（全文，开工实测）：`a985e330153d8b180ee4d192d86613c3dff9593a6e501021ca9e466d98d21af3`——与派发记录一致
方案 SHA-256（收工复测）：`a985e330153d8b180ee4d192d86613c3dff9593a6e501021ca9e466d98d21af3`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`ed8e0287620b655431946fe6db5ca29eaf56c48b60e4cbb1d53390b29af35777`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=60475）**精确命中**，记账自洽
范围：full（全量第九轮复审）

## 1. 结论

- **verdict: APPROVED**
- blockers: **0**
- advisories: **1**（F-28，事实性表述偏差，不影响可实施性）
- **F-26 / F-27 闭合判定：两条全部闭合**（F-26 协商定案 + 双路径断言 + §6 同步，三处一致；F-27 批次第⑥项 + 边界声明，锚点准确，见 §3）。
- **OD-6 = W1 复核：维持（连续第五轮）。§3.2.2 存储选型复核：维持（连续第五轮）。**
- **判 APPROVED 的依据**：本轮零阻塞。历轮累计 28 条意见中，27 条已闭合且经本轮抽核维持；唯一新发现 F-28 是**注释条数的计数偏差**（v8 称「既有六条注释惯例」，实测 13 条日期注释 / 8 个不同日期），它不改变「按惯例累积注释」这一指令的正确性——实施者按日期格式追加即可，无论既有是 6 条还是 13 条，动作完全相同。故为建议级，不阻塞 APPROVED。
- **本轮检查范围完整声明**：见 §2.6。round_complete=true。

**本轮自选新核验面（按指令要求声明）**：除复核 F-26/F-27 与全量锚点外，本轮新增两个此前九轮均未覆盖的核验面：

1. **既有测试护栏对 scalar capability 的覆盖度**（产出 §3.2 的正面结论 + F-28 关联）——实测发现 `projection_window_spec_test.go:325-340` **已存在** direct+relay 双路径镜像护栏，但它是**字符串计数型**（`strings.Count(serverRaw, "negotiateProjectionWindowV1") >= 2` + `strings.Contains(mainRaw, ...)`），且全文 `grep -c scalar` = **0**。这决定了 v8 新增 §7.1.4 断言的**实际必要性分层**（见 §3.2）。
2. **`negotiateProjectionWindowV1` 的可测试性与既有覆盖**（产出 §3.2）——该函数在 `projection_window_producer_test.go:299-328` 已有 4 处直接调用测试，`ack`/`hello`/`conn` 三元组可构造，故 v8 要求的双路径 echo 断言**可写出确定性失败判据**。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 | 判定 |
| --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；`git status --porcelain` 仅 10 个未跟踪 docs 文件（方案 + R1/R2/R2-meta/R3/R4/R5/R6/R7/R8） | 同 | ✓ 一致（零代码改动） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `dfb77ed93c610165730a88f8483c80e78941d321`；未提交 **8 项**（M `project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift`） | `dfb77ed9…` + 8 项枚举 | ✓ 一致 |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | 同 | ✓ 一致 |
| 正式 runtime | PID 8303，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime` 监听 `*:8777`；构建身份 ≠ 源码 HEAD | 同 | ✓ 一致（不作产物身份推断） |

**锚点文件洁净性**：`ProjectionWindowSpecTests.swift`、`ProjectionStore.swift`、`SessionProjectionWindow.swift` 逐个 `git status --porcelain --` 实测 **clean**，核验对象 = 提交 `dfb77ed9` 内容。全部未提交文件**只读未触碰**。

### 2.2 F-26 闭合核验

| v8 声明 | 实测 | 判定 |
| --- | --- | --- |
| direct 与 relay **各自独立**调用 `negotiateProjectionWindowV1` | `server.go:808 if !s.negotiateProjectionWindowV1(ack, &hello, connection) {`；`main.go:693 if !server.negotiateProjectionWindowV1(ack, &hello, conn) {` | ✓ 精确 |
| capability 字符串只在 `server.go` 出现（`:255/:366/:392`），relay 靠函数复用覆盖 | `grep -c '"projection_window_v1"'`：server.go=**3**、main.go=**0**；三处分别为 `:255` 注释、`:366` 检查、`:392` echo（逐行读取确认） | ✓ 逐处精确 |
| `main.go:693` 注释原文「relay path mirrors the direct hello negotiation exactly」 | `main.go:692` 原文 `// projection_window_v1 relay path mirrors the direct hello negotiation exactly.` | ✓ 逐字命中 |
| §3.7 定案为「在 `negotiateProjectionWindowV1` 内扩展」 | `:507` 已写入定案 + 「零额外接线」+ 独立函数替代的硬约束 + 「静默永久回 full / 只在 §7.4 场景 5 暴露」的后果 | ✓ 落地 |
| §7.1.4 双路径 echo 断言 | `:653` 新增条目，含 direct/relay 两个锚点、函数复用依赖说明、独立函数分支的断言覆盖要求、兜底暴露点 | ✓ 落地 |
| §6 S-1 组件列同步 | `:604` 已含「scalar 协商复用 `negotiateProjectionWindowV1` 扩展（direct+relay 双路径自动覆盖，§3.7——F-26）」 | ✓ 落地 |
| §10.13 复核请求 | `:764` 存在，含两项 | ✓ 落地 |

**F-26 闭合。** 该修订的正确性在于把「机制选择」而非「机制描述」定死：选复用 → relay 自动覆盖、零接线；并给替代路线留了硬约束（两处接线 + 双路径断言）与失败后果（Relay 静默永久回 full，仅在真机场景 5 暴露）。这是一处能防住真实回归的修订。

### 2.3 F-27 闭合核验

| v8 声明 | 实测 | 判定 |
| --- | --- | --- |
| 代码常量停在 `2026-09-05`（`bridge_v1_schema.go:77`） | `:77 BridgeProtocolSchemaRevision = "2026-09-05"` | ✓ 精确 |
| README 记 `2026-10-06-r1`（`:15`） | `docs/protocol/README.md:15 \| Direct bridge \| \`cordcode-bridge\` \| 1 \| \`2026-10-06-r1\` \|` | ✓ 精确 |
| `git log -S` 证明代码从未取过该值 | `git log -S 'BridgeProtocolSchemaRevision = "2026-10-06-r1"'` → 无命中 | ✓ 成立 |
| 字段纯信息性，`server.go:716`/`hello_handler.go:188` 只填充不比较 | `server.go:716` relay 帧填充；`hello_handler.go:188` ack 填充；全仓无 `SchemaRevision ==` / `!=` 比较分支 | ✓ 成立 |
| 批次第⑥项（bump + 注释累积 + 边界声明） | `:532` 已写入⑥，含「按协议 pack 惯例 bump」「在该文件注释块累积本次 additive 变更记录」「既有六条注释惯例」；边界声明含「不得在 S-1 变更边界内顺带『修正』README」「登记后独立处置」 | ✓ 落地（措辞见 F-28） |
| §6 S-1 组件列同步 | `:604` 已含「`schemaRevision` bump」于 re-freeze 批次括注内 | ✓ 落地 |

**F-27 闭合。** 边界声明（「不得顺带修正 README」）是这处修订的关键价值：它把一个与本案无关的既有分歧挡在 S-1 变更边界之外，避免变更集被污染。

### 2.4 数字与日志抽核

沿用 §1.1 声明口径独立抽核（本轮抽核）：checkpoint 34,537,988 B；turns 119；整棵 34,427,361 B；最大单 turn 6,347,762 B（index 1，不在尾 17）；尾 17 = 3,801,579 B / gzip6 1,084,899 B；transcript 101,520,809 B = 96.8178 MiB；8 次全量投递（req_18/33/54/58/67/77/82/91，gen 1→5）；E-2 window RPC = 0；E-1 hydrate_failed = 0。**全部维持命中。**

### 2.5 锚点抽核（历轮既有锚点维持）

抽核 12 处高价值项：`projection_window.go:77-82/24-26`、`projection_window_handler.go:32/102-106/124-126`、`handlers_projection.go:122`、`projection_kernel.go:266-278/280/1123`、`main.go:560`、`server.go:373-395`、`projection_snapshot_fence.go:224/227/230-235/269-276`、`event_publisher.go:771-776/1726-1749/1802-1811`、`claude_stub_status.go:145-157`、iOS `ProjectionStore.swift:145/152/763-777`、`SessionProjectionWindow.swift:96-98/116-176`、`ProjectionWindowSpecTests.swift:48/51/129-130`、Codex `codex-rs/cloud-client/README.md:18`——**全部维持命中**。

### 2.6 覆盖声明

- **已实测核验**：三仓 + runtime 来源身份；方案全文哈希（开工 + 收工）；交接块正文哈希（idx=60475 精确命中）；F-26 六项声明逐项比对（含 `grep -c` 双路径计数、三处字符串逐行读取、`main.go:692` 注释逐字）；F-27 五项声明逐项比对（含 `git log -S` 追溯、比较分支全仓搜索）；§7.1.4/§6/§10.13 一致性；**既有测试护栏覆盖度**（`projection_window_spec_test.go:325-340` 逐行读取、`grep -c scalar` = 0）；**`negotiateProjectionWindowV1` 可测试性**（`projection_window_producer_test.go:299-328` 四处直接调用）；数字抽核；既有锚点抽核。
- **已独立推理（非命令实测，已标注）**：F-28 的「注释条数偏差不改变动作」——由「实测 13 条日期注释 / 8 个不同日期」与「v8 称六条」的对比，结合「按日期格式追加」这一动作与既有条数无关推得；§3.2 的必要性分层——由既有护栏的字符串计数实现方式推得。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 精确构建 commit（S-0 职责）；transcript 逐行 type 分布（对本案无影响）；iOS 8 个未提交文件内容（按指令只读未触碰）；**未 build / 未 test**（硬边界）。

## 3. 意见

### F-28 [建议] v8 称 `bridge_v1_schema.go` 注释块「既有六条注释惯例」，实测为 13 条日期注释（8 个不同日期）

- **位置**：§3.7 re-freeze 批次第⑥项（「在该文件注释块累积本次 additive 变更记录（既有六条注释惯例）」）。
- **实测证据**：
  ```bash
  grep -c "^	// 20[0-9][0-9]-" go-bridge/bridge_v1_schema.go          # → 13
  grep -o "^	// 20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]" ... | sort -u | wc -l  # → 8
  ```
  13 条分别位于 `:12`（2026-07-24）、`:17`（2026-07-29）、`:21`（2026-08-01）、`:27`（2026-08-02）、`:35`（2026-08-22）、`:40`（2026-08-23）、`:44`（2026-09-05）、`:50`（2026-09-05 二次）、`:59`（2026-09-05 三次）、`:65`（2026-09-06）、`:71`（2026-09-06 二次）等；其中「同日多次返工」按 `（同日二次返工）` / `（同日三次返工）` 标注，故**条目数（13）≠ 日期数（8）**。
- **影响**：**不改变任何动作**。第⑥项的指令是「按惯例累积变更记录」，实施者按 `<日期>: <变更摘要>` 格式追加一条即可——既有是 6 条还是 13 条，追加动作完全相同，且不存在「需要凑够六条」之类的隐含约束。因此该偏差**不影响可实施性、不影响验收**，仅为事实性表述精度问题。之所以记录，是因为本方案历轮的可信度建立在「引用的数字与锚点均可亲核」之上（R6/R7/R8 三轮均以精确数字为证据），若留下一处易被复核者发现的计数偏差，会削弱后续轮次对同类数字的信任。
- **修订方向**：把「（既有六条注释惯例）」改为「（既有 13 条日期条目、8 个不同日期，同日多次变更按 `（同日二次返工）` 标注）」，或简化为「（既有注释块已累积 13 条日期条目）」。
- **闭合标准**：该处计数与实测一致，或改为不含具体数字的表述（如「沿用既有注释块格式」）。

### 附：§3.2 正面核验结论（非缺陷，供后续实施参考）

本轮自选核验面发现一处**既有护栏与 v8 新增断言的关系**，值得记录（不构成意见）：

- **既有护栏已存在**：`go-bridge/projection_window_spec_test.go:325-340` 已有 direct+relay 双路径镜像检查，注释原文「Direct + relay hello paths must both route through negotiateProjectionWindowV1 so a declared-but-not-echoed connection can never call the RPC」。
- **但它是字符串计数型**：`strings.Count(serverRaw, "negotiateProjectionWindowV1") >= 2`（server.go）+ `strings.Contains(mainRaw, "negotiateProjectionWindowV1")`（main.go）。全文 `grep -c scalar` = **0**——即该护栏**只保证 window capability 的协商被两路径覆盖，不保证 scalar capability 被覆盖**。
- **因此 v8 §7.1.4 新增的双路径 echo 断言是必要增量，且必要性分层清晰**：
  - 若按 v8 定案**复用** `negotiateProjectionWindowV1` → scalar echo 随该函数自动获得，既有字符串护栏已能保证两路径覆盖，新增断言属**行为级加固**（从「函数被调用」升级为「echo 实际生效」）；
  - 若改为**独立函数** → 既有护栏**完全不覆盖**（它只匹配 `negotiateProjectionWindowV1` 这个字符串），v8 §7.1.4 的断言是**唯一防线**。这正好印证 v8 把「独立函数」列为需硬约束的替代路线是对的。
- **可构造性确认**：`negotiateProjectionWindowV1` 在 `projection_window_producer_test.go:299-328` 已有 4 处直接调用（构造 `ack` / `hello` / `conn` 三元组，断言返回值与 `ack.Ok`），故 §7.1.4 的「双路径 echo 生效」断言可写出确定性失败判据（对同一组 hello 输入，分别在 direct/relay 路径下断言 `ack.Capabilities["projection_window_scalar_v1"] == true`）。

## 4. 历轮意见闭合复核（本轮独立重判）

| 意见 | 级别 | 出处 | 本轮判定 |
| --- | --- | --- | --- |
| F-1 ~ F-9 | 2 阻塞 + 7 建议 | R1 | **全部闭合**（抽核维持） |
| F-10 iOS 枚举漂移 | 建议 | R2 | **闭合** |
| F-11 window 冷开缺 scalar | 阻塞 | R3 | **闭合** |
| F-12 SQLite 选型 | 阻塞 | R3 | **闭合** |
| F-13 ~ F-16 | 4 建议 | R3 | **全部闭合** |
| F-17 | 阻塞 | R4 | **闭合** |
| F-18 / F-19 | 2 建议 | R4 | **闭合** |
| F-20 decide 路由维度 | 阻塞 | R5 | **闭合** |
| F-21 re-freeze 前置 | 阻塞 | R6 | **闭合** |
| F-22 peer 组合行为 | 建议 | R6 | **闭合** |
| F-23 iOS 来源回填 | 建议 | R6 | **闭合** |
| F-24 行号错位 + 双副本 | 建议 | R7 | **闭合** |
| F-25 重试指标不可观测 | 建议 | R7 | **闭合** |
| **F-26 协商机制未定义** | 建议 | R8 | **闭合**（定案复用 + 替代硬约束 + 双路径断言 + §6 同步，三处一致） |
| **F-27 schemaRevision 纪律** | 建议 | R8 | **闭合**（批次⑥ + 边界声明，锚点全部准确；计数措辞另立 F-28） |

**回归检查**：v8 改动集中于 §0/§3.7/§6 S-1/§7.1.4/§10.13，本轮重读这些章节及全部交叉引用（§3.7 批次六项 ↔ Gate A ↔ S-1 ↔ §7.1.4 ↔ §10.13 ↔ 交接块），**未发现修订引入的新矛盾或悬空引用**；W1 定案、re-freeze 批次既有五项、peer 组合表、存储选型、架构方向、切片划分均未改动（与 §0 声明一致）。§0 编号连续（v8→0.1 v7→…→0.6 v2）实测确认。

## 5. 已定案事项复核（本轮独立重判）

### 5.1 OD-6 = W1（维持，连续第五轮）

- **W1**：零额外往返；scalar 随权威 page 经 replica 同构写入；冷开 running session 正确。v6–v8 已把 re-freeze（含 schemaRevision）、trace 交付、协商机制全部显式登记为实施前置，成本完全透明。
- **W2**：多一次 RTT 挤压 R-2 预算；需定义两次响应间 patch 排序，竞态面更大。维持淘汰。
- **W3**：`runtimeStateStore` 是 live 事件消费者，冷开「已在运行」session 无历史事件可学，结构性不覆盖主场景。维持淘汰。

**结论：W1 维持，无翻案依据。**

### 5.2 §3.2.2 存储选型（Mac=bbolt / iOS=文件型，维持，连续第五轮）

五项约束本轮抽核：`go.mod`/`go.sum` 无 SQLite 驱动、`project.yml:53` 未设 `CGO_ENABLED`、`session_catalog.go:14-16` 既有立场、`import_guard_test.go:23-24` forbidden surface、iOS 零 SQLite 栈。**全部维持成立，无翻案依据。**

## 6. 剩余门

- **证据门**：E-3（window 未生效根因——S-1 修复前必须闭合；含 descriptor 维度）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时——S-5）；E-6（materialization 正确性——S-2）；E-7（preview 不污染权威 writer——S-4）；**E-10**（window 冷开 scalar 正确性——S-1 完成宣告）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。
- **S-1 开工前置（已登记）**：E-1/E-2 + OD-6 定案（W1，已完成）+ F-20 修订（已完成）+ **F-21 re-freeze 批次（六项，含 v8 的 schemaRevision bump 与双副本义务）**。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

**说明**：以上均为方案已登记、由实施/验收阶段满足的门，不构成方案层面的阻塞。方案本身已达可实施状态。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、F-26/F-27 逐条、既有测试护栏覆盖度、negotiate 函数可测试性、数字与锚点抽核），全部亲核完成后才读 R4–R8 报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与前轮差异**：R8 判 REVISION_REQUIRED（0 阻塞 + 2 建议）；本轮确认 F-26/F-27 全部闭合，**判 APPROVED**（0 阻塞 + 1 建议）。F-28 是计数措辞偏差，不改变动作、不影响验收，故不构成 APPROVED 的障碍——这与 R8 未判 APPROVED 的情形有本质区别：R8 的两条（尤其 F-26）若不修会导致 Relay 静默退化且只在真机验收才暴露，属「方案不修订则不可验收」；F-28 修不修，实施者做的事完全一样。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`python3` 交接块哈希切分；`grep -c '"<capability>"' server.go main.go`；`sed -n` 逐行读 `server.go:255/366/392/808`、`main.go:692-693`、`bridge_v1_schema.go:77`、`docs/protocol/README.md:15`；`grep -c "^	// 20[0-9][0-9]-"` 与 `sort -u` 计日期；`git log -S` 追溯 schemaRevision；`grep -n negotiateProjectionWindowV1 go-bridge/*_test.go`；`grep -c scalar go-bridge/projection_window_spec_test.go`；逐锚点文件 `git status --porcelain --`。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 8 份报告、iOS 8 个未提交文件一律只读未触碰；未 build / 未 test / 未部署 / 未安装。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、字节测量、日志计数、fixture key 断言实测、双副本哈希、双 hello 路径镜像比对、既有测试护栏覆盖度分析）。

---

评审员交接块：

```text
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: a985e330153d8b180ee4d192d86613c3dff9593a6e501021ca9e466d98d21af3
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r9.md
scope: full
blockers: 0
advisories: 1
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, F-21 re-freeze 批次(S-1 前置), S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
