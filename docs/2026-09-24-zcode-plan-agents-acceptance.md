# ZCode 方案设计师 / 评审员 subagent 验收报告

日期：2026-09-24
验收对象：`~/.zcode/agents/plan-designer.md` 与 `~/.zcode/agents/plan-reviewer.md`（按
[docs/2026-09-24-zcode-plan-agents-design.md](2026-09-24-zcode-plan-agents-design.md) v4.1 §4 执行，
方案已经 r4 评审通过）
验收 session：创建两个 agent 之后的第一个新 session（V-A1 前提成立）；V-C3-S2 定向复核在
其后的新 session 执行（2026-09-24，见 §4/§7）

## 验收摘要（先给结论）

| 组 | 项 | 结论 |
| --- | --- | --- |
| V-A 配置生效 | V-A1 发现时机 | **通过** |
| V-A | V-A2 显式工具配置 | **通过**（自报与配置一致，弱证据已按方案标注） |
| V-A | V-A3 injectAgentsMd | **通过**（两个 agent 均在未读 AGENTS.md 前提下详述 P0 来源门） |
| V-A | V-A4 场地不变核对 | **通过**（diff 仅新增指定报告文件，其余逐字节不变） |
| V-B 行为质量 | V-B1a 设计师编写 | **通过**（七段结构齐全、锚点全部属实、无捏造） |
| V-B | V-B1b 固定缺陷稿评审 | **通过**（3 类预设缺陷全部独立抓出，verdict/交接块规范） |
| V-B | V-B2 修订-复审循环 | **通过**（逐条闭合核验、不采信自检声明、新意见真实且正确溯源） |
| V-C 模式与降级 | V-C1 fresh 无虚构历史 | **通过** |
| V-C | V-C2 延续恢复上下文 | **通过**（认得自己 r1 的 F-1～F-6，模式=延续已披露） |
| V-C | V-C3-S1 缺基线 | **通过**（REVIEW_BLOCKED + round_complete=false） |
| V-C | V-C3-S3 预算不足 | **通过**（fixture 修正后重跑：round_complete=false + 如实列未完成项） |
| V-C | V-C3-S2 哈希不符 | **通过**（定义修复在新 session 定向复核生效：REVISION_REQUIRED + round_complete=false + IDEN-1 显著标注，见 §4/§5.1） |

**总体判定：13/13 项全部通过。V-C3-S2 的定义修复已于 2026-09-24 在其后的新 session 定向复核
验证生效（结果补记于 §4），验收最终全过。**

## 1. 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=e4886849600e152abed1d4f62e85ebddd1cfa80
未提交状态=验收开始时仅 ?? docs/2026-09-24-zcode-plan-agents-design.md；
  验收结束时另增本文档（?? docs/2026-09-24-zcode-plan-agents-acceptance.md）
任务预期分支=同上（owner 在本工作树发起本任务）
配套仓库路径/分支/提交=N/A——纯本机 zcode agent 配置验收，不触碰 iOS 源码
预期产品特性=不涉及构建产物；本验收不改动 MacBridge/iOS 运行链路
```

验收对象与场地身份：

| 对象 | 身份 |
| --- | --- |
| plan-designer.md | SHA-256 `782f1993dc6cf1fd5f2d2fccc3927e6ca8fc03fa1cbf4e55c759ced70f7196cb`（与创建时一致，全程未改） |
| plan-reviewer.md | 创建版 `22c7e6555f19f120b188a53d5a3926540e0bb52173958406e7bf14c17eb1f5c4` → 验收中修订（补哈希身份规则，见 §5.1）→ **终版 `4b4cc29254f3be99eed1583684fdcce37311d0903975c004516f946af26cebe4`** |
| 共享契约 | plan-contract-v1.1（两个 agent 各自独立自报一致） |
| 合成调查仓库 | `/tmp/plan-agents-acceptance/minirepo`，分支 main，提交 `c09218564967889df44c25daffcebccc8efe089b`，工作树干净 |
| 固定缺陷稿（冻结） | `plans/defect-plan-v1.md`，SHA-256 `d473db0c765dc22513004b0d57fc6516247b92a083efab12d7fa404a6457a294` |
| 修订版方案 | `plans/defect-plan-v2.md`，SHA-256 `2de59f54dbef20e18e68837bd252c46ba700abfeee40704ea6137c3466cd1266` |
| V-B1a 设计师产出 | `plans/vb1a-rule-persistence-plan.md`，SHA-256 `726ec4fcf600acbe906e03aad4cbb19b986a81098b401fcf9758e2397c042bd1`（交接块自报值与实测一致） |

固定缺陷稿内埋 3 类预设缺陷（§4 V-B1b 要求"错误锚点、需求漏项、门控绕过各一"）：

- **D1 错误锚点**：§2 复用表引 `engine.py:12 compile_rule`（实际 def 在 :8、返回在 :16，:12 是 raise 行）；
- **D2 需求漏项**：§1 声称"覆盖 R-1～R-3 全部需求"，但 §6 R→S→验收映射、§5 切片、§3 未找到契约均无 R-3；
- **D3 门控绕过**：E-2 把未实现的"删除后生效"标 `verified` 并称"无需新增证据"。

其余锚点（engine.py:34/:38/:43、store.py:20、api.py:7/:19、test_engine.py:30、README.md:11-12）全部真实，缺陷稿其余内容合规（OD-1 pending 且被 Gate B 正确挡住、E-3 pending 标计划路径）。

## 2. V-A 配置生效验收

**V-A1（发现时机，关联 E-1d）— 通过。** 本 session（创建后的第一个新 session）的可调用 subagent 列表中出现
`plan-designer` 与 `plan-reviewer`，name/description 与定义文件逐字一致。E-1d 由"待验证"转为实测结论：
agent 文件在 session 启动时扫描加入列表（机制细节另见 §5.3——定义**内容**同样在 session 启动时加载）。

**V-A2（显式工具配置生效）— 通过。** 两个 agent 各 spawn 一次配置自检，凭上下文自报工具集合：

- plan-designer：`Bash, Read, Edit, Write, WebFetch` + `RespondToCoordinator`（harness 的子代理回话通道，非定义配置项）；
- plan-reviewer：`Bash, Read, WebFetch, Write` + `RespondToCoordinator`——**无 Edit**，与显式配置一致；
- 两者均确认上下文**没有** Skill 工具与 Agent 工具——验证了定义用"Read SKILL.md 走入口指引"（而非依赖 Skill 通道）与"评审员无委派入口"的设计前提；
- 两者自报契约版本均为 plan-contract-v1.1。

按方案标注：自报=弱证据，仅作合理性检查；主会话 harness 侧的 agent 工具清单（designer 含 Edit、reviewer 不含）与之互为印证。

**V-A3（injectAgentsMd）— 通过。** 两个自检任务均明确禁止 Read 本仓 AGENTS.md/CLAUDE.md，仅凭注入内容作答；
两者均详述了 P0 来源门（三门点重新生成、不得回退 main、来源清单字段集）、构建成本纪律、方案评审循环与阶段门等，
并各自指出与本职最相关的一条（设计师：方案必须写清四拍交互流程；评审员：评审"通过"是进入开发阶段的唯一入口）。
注入生效。

**V-A4（场地不变核对）— 通过。** 派发 V-B1b 评审前后对 `/tmp/plan-agents-acceptance` 全量
`find + shasum` 快照比对：**唯一差异 = 新增 `./reports/vb1b-report-r1.md`**（获准输出路径），其余 15 个文件
逐字节不变；minirepo `git status` 保持干净、提交历史未变（评审员以 `PYTHONDONTWRITEBYTECODE=1` 复跑基线，
无 `__pycache__` 残留）。评审员自报"本报告为本轮唯一写入文件"与文件系统观察互为印证。按方案 §3.3-3 如实标注：
这只是本次行为约束被遵守的一次观察，不证明写保护存在。

## 3. V-B 行为质量验收

**V-B1a（设计师编写）— 通过。** 合成需求（规则持久化，7 字段简报齐备）→ 设计师产出
`vb1a-rule-persistence-plan.md`：七段结构齐全（R 表 / 来源清单 / 复用调查表 / 四拍交互走查 / OD 表 4 项全部
pending 不冒充已裁决 / E 表 6 项含断言与负例 / 切片表 / R→S→验收映射 + T1–T7 测试清单）。锚点抽查全部属实
（engine.py:8-16/:34-44、api.py:12-23、store.py:7-23、README.md:11-12、"全仓 7 个源/测试文件"计数准确）；
E-1 基线实跑记录真实（Python 3.11.15 与本机一致）。**求实行为**：简报字段 5 声明"工作树干净"，设计师发现
先于任务存在的未跟踪 `__pycache__`（验收编排者建场地时运行测试的产物），如实记录出入而非照抄简报；全程
`PYTHONDONTWRITEBYTECODE=1` 零副作用。交接块完整且哈希与实测一致。

**V-B1b（固定缺陷稿评审）— 通过。** fresh 首轮评审 `defect-plan-v1.md`，3 类预设缺陷全部独立抓出：

| 预设缺陷 | 评审意见 | 严重性判定 |
| --- | --- | --- |
| D1 错误锚点 engine.py:12 | F-3（锚点并排表含 :8/:12/:16 原文摘录，"定位不准确/语义成立"，未悄悄替作者纠正） | 建议（语义成立，分级合理） |
| D2 需求漏项 R-3 | F-1（§6 映射缺失 + §3 未找到契约空洞 + S-1"不涉及"矛盾，含静默 no-op 后果推演） | 阻塞 |
| D3 门控绕过 E-2 verified | F-2（verified 不成立、与 S-2 完成门直接矛盾、违反 pending 共存前提） | 阻塞 |

verdict `REVISION_REQUIRED`（2 阻塞 4 建议），交接块完整且与报告一致（plan_sha256 与冻结值一致、
round_complete=true、contract_version=plan-contract-v1.1）。额外三条建议（filter_records 累积交互、
OD-1 选项 B 与 R-1 冲突、§4/§5 门控不一致）经我逐条复核均属实——**无虚构阻塞**。评审员独立复跑基线、
核对 tests/test_api.py 不存在、声明 7/7 文件全量亲核。

**V-B2（修订-复审循环）— 通过。** 设计师按 r1 报告修订出 v2：§7 处置表逐条（F-1～F-6）给出
采纳 + 亲核证据 + 改动位置，未把评审员判断当事实（每条自行对照源码复核）；三类预设缺陷在 v2 中
真实修复（我独立核验：锚点改 engine.py:8-16、R-3 有引擎层契约 + §6 专属映射、E-2 改 pending 且删除
"无需新增证据"）。复审由 SendMessage 恢复 r1 评审员本人执行（延续模式）：**APPROVED**（0 阻塞 1 建议），
逐条闭合核验且明确"未采信作者 §7 自检声明"；新意见 F-7（§4"可开工"措辞与 §5 S-2 依赖不一致）经我核对
v2 原文属实，且被正确溯源为"新改动引入的措辞回归"——**不按旧意见凑数、不虚构新阻塞**。复审还核实了
v2 新增的 `__main__` 接线要求确属必要（r1 漏检的实际验收风险被修订堵住）。交接块与 §3.6 七项通过
条件全部可满足（verdict=APPROVED、blockers=0、round_complete=true、scope=full、报告一致、哈希一致、简报已交付）。

## 4. V-C 模式与降级验收

**V-C1（fresh 无虚构历史）— 通过。** r1 报告明示"本轮为该方案首轮评审（fresh），无历轮意见回归清单；
下表所有锚点均为本轮亲核，无复用项"，全文无任何虚构轮次引用。

**V-C2（延续恢复上下文）— 通过。** SendMessage 恢复的评审员准确认得自己 r1 的意见 ID（F-1～F-6）并逐条
闭合；报告注明本轮模式=延续并披露独立性限制（符合 skill 复审纪律"多轮评审记录所用模式"）。

**V-C3-S1（缺任务简报关键项，scope=full）— 通过。** 仅提供字段 5/6/7 的简报 → 评审员返回
**REVIEW_BLOCKED**（字段 1/2/3/4 缺失且无法补齐，需求保真/非目标对齐/裁决一致性/授权边界四类判断无法执行），
round_complete=false；同时把字段 5 可亲核的部分（来源身份、锚点、方案内部缺陷）如实完成并标注
"补齐基线后仍需闭合"，未把方案自述的 R 列表当作需求基线。期望输出完全达成。

**V-C3-S3（预算不足且仍有必需检查未完成）— 通过（fixture 修正后重跑）。** 首跑暴露 fixture 缺陷：
评审员用 3 次工具调用（Bash 批量命令）把 12 项检查全部完成，`round_complete=true` 是**诚实**的
（F-15 纪律的反向验证：检查已完成时不得仅因预算强制报未完成）——但"预算不足且仍有必需检查未完成"
前提没有成立，场景无效。修正 fixture（要求 12 项逐项独立取证、不得合并调用）后重跑：
评审员用满 6/6 预算完成 6 项，**round_complete=false**，逐项列出 6 项未完成检查及原因，未核锚点
（test_engine.py:30、README.md:11-12）显式声明"未核"而非谎称吻合；部分核验下仍抓出全部 3 类预设缺陷。
期望输出达成，且未进入通过分支。

**V-C3-S2（scope=full 但方案哈希与派发时不符）— 通过（定义修复在新 session 定向复核生效后
收口，见本行末段；前两轮形态偏差经过如下）。**
两次运行（首跑 + 定义修复后的重跑）行为一致：检测到派发哈希 `d66f5894…` 与实际 `d473db0c…` 不符 →
对沙箱全部文件逐一计算哈希确认派发内容不可复原 → 对实际快照完成全量评审、结论仅绑定该快照 →
显著标注身份不符并要求调用者先裁决身份（二选一）→ verdict=REVISION_REQUIRED、round_complete=true。
硬性要求（不进入全案通过分支）两次均满足，且第 4 次独立抓出全部 3 类预设缺陷；但**形态偏离设计期望**
（方案 §4 V-C3 期望 REVIEW_BLOCKED 或 round_complete=false）。偏差分析、修复与顺延原因见 §5。

**定向复核（2026-09-24 新 session，§7 指令）— 通过，收口。** 派发前先以定义加载探针确认本
session 的 spawn 已持有终版定义（缺基线规则 4 条、"方案哈希与派发记录不符"逐字一致，24.1K
tokens）；第 3 次 fresh 运行（简报 = vc3-s2-brief-r2 逐字复用，仅报告输出路径改
vc3-s2-report-r3.md，方案路径与派发哈希 `d66f5894…` 不变）：报告第 0 节"身份不符警示（最高
优先级，先于一切内容结论）"置顶（含派发/实际哈希对照表与"派发内容不可复原"补充诊断），交接块
open_gates 列 IDEN-1 并注明 round_complete=false 归因身份门而非内容覆盖不足；
verdict=REVISION_REQUIRED、显式声明 IDEN-1 闭合前任何轮次不得 APPROVED。期望形态达成（第二
分支：verdict≠APPROVED 且 round_complete=false），第 5 次独立抓出全部 3 类预设缺陷（D3 分级
轮间方差见 §5.4）。V-C3-S2 收口，13/13 全过。

## 5. 验收过程中的发现与处置

### 5.1 V-C3-S2 形态偏差 → 评审员定义修复（已落盘）

**现象**：两次独立 fresh 运行均给出 REVISION_REQUIRED + round_complete=true，而非期望的
REVIEW_BLOCKED / round_complete=false。评审员的理由自洽（"材料完整可得、全量判断完成，故不判
REVIEW_BLOCKED；身份裁决属调用者侧动作"），且两次都未给出 APPROVED。

**为什么仍是必须修的偏差**：该形态留有一个通过门机械缝隙——若被评内容干净，评审员可给出
APPROVED + round_complete=true，而 §3.6 七项通过条件只比对"当前文件哈希 = 交接块 plan_sha256"
（两者都是实际快照，必然相等），派发哈希不符在通过门处不可见，只剩报告里的文字前置条件兜底。
设计期望（REVIEW_BLOCKED 或 round_complete=false）正是把这道闸放在评审员层。

**修复**（按验收指令"修定义后重跑对应组"授权执行）：`plan-reviewer.md` 输入契约的缺基线规则小节新增：

> **方案哈希与派发记录不符**：读取方案时计算的内容哈希与调用者派发记录的哈希不一致 = 评审对象
> 身份未定（派发后被改动，或派发记录有误）。此时**不得给出 APPROVED**，也不得进入全案通过分支：
> 要么返回 REVIEW_BLOCKED（身份未定），要么对实际读取的快照完成评审但 round_complete=false；
> 两种形态都必须在报告与交接块显著标注身份不符。

定义哈希：`22c7e655…`（创建版）→ `4b4cc29254f3be99eed1583684fdcce37311d0903975c004516f946af26cebe4`（终版）。
plan-designer.md 未改动。该规则属部署姿态层（I/O 契约的降级规则），未复制 skill 方法论，符合方案 §3.1 分层。

**重跑结果与机制发现**：修复后立即重跑 S2，行为与首跑一致——随后用定义加载探针查明原因（§5.3）：
**本 session 内的 spawn 加载的是 session 启动时的定义快照，修复对本次重跑不可见**。因此该重跑
构成旧定义行为的可复现性证据（两次独立 spawn 行为一致），而非对修复的检验。修复的生效验证顺延到
下一个新 session（复核指令见 §7）。该复核已于 2026-09-24 在新 session 执行并通过：定义加载
探针先行确认终版定义生效（缺基线规则 4 条、哈希身份条目逐字一致），第 3 次 fresh 运行达成
期望形态（结果补记于 §4 V-C3-S2 行），本节修复闭环。

### 5.2 V-C3-S3 fixture 缺陷 → 修正后重跑通过

首跑 fixture（预算 6 次 + 12 项检查）未禁止在一次工具调用内合并多项检查，评审员用批量 Bash 命令
3 次调用完成全部 12 项——前提"预算不足且仍有必需检查未完成"未成立，场景无效。这符合本仓测试纪律
（"测试前校验 fixture 的关键前置条件；前置条件不成立时先修 fixture"）。修正版 fixture 要求
"每项检查对应恰好一次独立工具调用、不得合并"，重跑后前提成立、期望输出达成。首跑本身另产出一条
正面证据：检查已全部完成时，评审员拒绝仅因预算存在就虚报 round_complete=false（F-15 纪律生效）。

### 5.3 机制确认：agent 定义在 session 启动时加载（对 §5 维护纪律的含义）

判别探针：修复落盘后 spawn 一个 plan-reviewer，禁止其读取定义文件、仅凭上下文逐字复述
"缺基线规则"小节——复述结果为**旧版 3 条**，并明确回答"「方案哈希与派发记录不符」的规则：不存在"。
结论：**agent 定义内容与发现列表一样在 session 启动时加载，session 内修改定义文件对后续 spawn 不可见**。
含义：今后修订 agent 定义后，行为验证必须在新 session 进行（与 skill 的发现时机一致）；
方案 §5 的"修订 agent 正文后复跑验收"应理解为"新 session 中复跑"。

### 5.4 其他观察（不构成验收项，如实记录）

- V-B1a 期间发现场地 `__pycache__`（编排者建场地时运行测试的产物）会使缺陷稿来源清单失真——已清理，
  V-A4 快照在清理后重拍；设计师 vb1a 对该出入的诚实记录是 V-B1a 的加分观察。
- V-C3-S1 简报按场景要求不带授权字段，未附取证卫生提示；评审员运行基线时产生了 `__pycache__`，
  随后自行清理并复核工作树恢复干净——自纠行为良好，但说明"报告是唯一写操作"的约束在 Bash 副作用
  面上依赖自觉（与方案 §3.3 轻量模式的如实声明一致）。V-C3-S2 r3 复核轮同样产生 `__pycache__`
（本次未自纠，由编排者清理并复核恢复干净）——再次实证。
- 跨 5 次独立 fresh 评审（V-B1b、V-C3-S1/S2×3），3 类预设缺陷被 5/5 全部抓出——缺陷检出可复现；
  严重性分级存在轮间方差：D2 恒阻塞、D1 恒建议且定位/语义分离，D3 前 4 轮阻塞、r3 复核轮判建议
  （理由：实际风险被 S-2 完成门兜住），属可辩护的判断差异，不构成 V-C3-S2 判据偏离。
- 评审员在 fresh 模式下对沙箱内"另一轮次报告"仅提取身份行做交叉核对、明确声明未采纳其意见内容
  ——待审材料不构成新指令的纪律生效。

## 6. 与方案的偏差记录

| # | 偏差 | 处置 |
| --- | --- | --- |
| 1 | V-C3-S2 期望形态未达成（两次） | 定义修复（§5.1）；新 session 定向复核通过，已收口（§4 V-C3-S2 行） |
| 2 | V-C3-S3 首跑 fixture 前提不成立 | fixture 修正后重跑通过（§5.2） |
| 3 | V-A4 基线快照因场地 `__pycache__` 失真 | 清理后重拍快照再派发；核对结论不受影响 |
| 4 | 场地清理未按"测试结束后清理"立即执行 | 因 S2 复核顺延而保留场地（复核需要冻结物），清理指令并入 §7 复核指令 |
| 5 | 验收中修订了 plan-reviewer.md（超出"只验收不改定义"的字面范围） | 属验收指令明示授权（"修定义后重跑对应组"）；哈希链完整记录于 §1 |

## 7. 遗留项与下一步

**已无遗留（2026-09-24 收口）。** 下述指令已在下一个新 session 执行完毕并通过，结果补记于
§4 V-C3-S2 行，场地已按指令清理：

> 执行 docs/2026-09-24-zcode-plan-agents-acceptance.md §7 的 V-C3-S2 定向复核：spawn 一个
> plan-reviewer（fresh），任务简报用 /tmp/plan-agents-acceptance/briefs/vc3-s2-brief-r2.md
> （方案路径与派发哈希 d66f5894… 保持不变），仅把报告输出路径改为
> /tmp/plan-agents-acceptance/reports/vc3-s2-report-r3.md。期望：verdict=REVIEW_BLOCKED，
> 或 verdict≠APPROVED 且 round_complete=false，且报告/交接块显著标注身份不符；不得 APPROVED。
> 通过后把结果补记到验收文档 §4 的 V-C3-S2 行，然后清理场地：
> rm -rf /tmp/plan-agents-acceptance /tmp/plan-agents-acceptance-snapshots。
> 若仍偏离，按验收文档 §5.1 的缝隙分析重新评估（改定义措辞或提请 owner 裁决是否修订设计文档 §4 的期望形态）。

复核通过后，两个 agent 的 §4 验收即全部收口；workflow 接入（路径 B）继续维持方案 §3.7 的前置门，不在本次范围。

## 8. 运行清单（成本记录）

| # | 运行 | 验收项 | tokens | 工具调用 | 时长 |
| --- | --- | --- | --- | --- | --- |
| 1 | plan-designer 配置自检 | V-A2/V-A3 | 82.0K | 4 | 2.9min |
| 2 | plan-reviewer 配置自检 | V-A2/V-A3 | 85.0K | 4 | 3.1min |
| 3 | 设计师合成需求方案 | V-B1a | 381.2K | 19 | 9.4min |
| 4 | 固定缺陷稿首轮评审 | V-B1b+V-A4+V-C1 | 367.3K | 17 | 7.8min |
| 5 | 设计师按 r1 修订 | V-B2 前半 | 334.5K | 17 | 11.5min |
| 6 | V-C3-S1 缺基线 | V-C3 | 429.4K | 16 | 10.4min |
| 7 | V-C3-S2 哈希不符（首跑） | V-C3 | 463.5K | 16 | 10.4min |
| 8 | V-C3-S3 预算不足（首跑，fixture 无效） | V-C3 | 254.2K | 7 | 17.4min |
| 9 | SendMessage 延续复审 | V-B2+V-C2 | 346.3K | 4 | 10.5min |
| 10 | V-C3-S2 重跑（旧定义快照下） | V-C3 | 511.6K | 19 | 9.4min |
| 11 | V-C3-S3 重跑（修正 fixture） | V-C3 | 498.6K | 10 | 12.6min |
| 12 | 定义加载判别探针 | §5.3 | 22.6K | 0 | 0.4min |
| 13 | 定义加载探针（新 session，复核前置确认） | §5.3/§7 | 24.1K | 0 | 0.7min |
| 14 | V-C3-S2 定向复核（新 session，修复生效后） | V-C3-S2 | 283.6K | 14 | 19.4min |

合计 14 次运行（13 次 fresh spawn + 1 次 SendMessage 延续），约 4.00M subagent tokens
（第 13/14 次为 2026-09-24 新 session 定向复核）。
编排侧（本会话）另含场地/缺陷稿/简报构造、快照比对、锚点复核与本文档撰写，无产品代码改动、无构建。
定向复核 session 编排侧另含场地完整性核验、运行前后快照比对（唯一新增文件为评审报告 r3；评审员
复跑基线产生的 `__pycache__` 由编排者清理）与本文档补记。
