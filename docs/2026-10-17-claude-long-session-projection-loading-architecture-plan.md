# Claude 长 session 打开架构优化方案

日期：2026-10-17
版本：v9（按评审报告 R9 修订；v8 全文 SHA-256 `a985e330153d8b180ee4d192d86613c3dff9593a6e501021ca9e466d98d21af3`，v7 全文 `4c598fb00112908c5269c4197c8ea0fe2a312cd30c24f83d350a8da6ef8ef3f4`，v6 全文 `a4d360225a0c5747298728c297ad7a1108eae77357f8b08023c3522d623262ee`，v5 全文 `4840baac4a6cbc921d4d7fd8440911b586b58628102b15fc7484d74490f5cd63`，v4 全文 `ef386fdff0d154b6df170dc99177a26bf23f871a3254679ea1a94b6caed5e6fc`，v3 全文 `0dda4476301d2d0e4e9bfc9ac0304a0b2c4def4ba129f0bec8f3c631a018e24a`，v2 全文 `9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18`，v1 全文 `35fb3d333e220deb6f4fc8e0fc53ad7927c72557879dc97b0214c28cbf04fbfb`）
状态：**已通过方案评审（R9 verdict: APPROVED，0 阻塞 + 1 建议 F-28 已随本版落地）**；本版不构成实施授权，实施按 §5 Gate A 前置与 repo 2026-09-20 评审循环纪律执行
范围：Claude / Claude Code 的 session_sync_v2 投影打开链路；后续再推广到其它 backend

## 0. v9 修订范围

按评审报告 R9（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r9.md`）修订。R9 判定 **APPROVED（0 阻塞 + 1 建议 F-28）**——F-26/F-27 经 R9 核验全部闭合，OD-6 定案 W1 与 §3.2.2 存储选型维持（连续第五轮）。本轮**仅落地唯一建议 F-28**，不改动任何设计内容：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-28 §3.7 re-freeze 批次第⑥项称 `bridge_v1_schema.go` 注释块「既有六条注释惯例」，实测为 **13 条日期条目 / 8 个不同日期**（同日多次返工按 `（同日二次返工）` 标注，故条目数 ≠ 日期数） | 建议 | §3.7 批次⑥ 更正为「13 条日期条目 / 8 个不同日期」并附实测口径（`grep -c "^	// 20[0-9][0-9]-" go-bridge/bridge_v1_schema.go` = 13）与「追加动作与既有条数无关」的说明；其余内容不变 |

本章之外**未改动任何设计内容**：W1 定案、fence scalar 分支、re-freeze 批次六项结构、peer 组合行为表、存储选型、架构方向、切片划分、门结构、§7 验收矩阵全部保持 v8 原样。§10 新增第 14 项记录本轮复核点。

R9 结论摘要（供后续实施/验收参照）：**方案层面已通过**（R9 §1「判 APPROVED 的依据」）；F-28 属「计数措辞偏差，不改变动作、不影响验收」。R8 的两条（F-26 协商机制、F-27 schemaRevision 纪律）经 R9 逐项亲核闭合。R9 另记录一处正面核验结论（非缺陷）：既有 `projection_window_spec_test.go:325-340` 已有 direct+relay 双路径镜像护栏，但为**字符串计数型**且全文 `grep -c scalar` = 0——若按 v8 定案复用 `negotiateProjectionWindowV1`，该护栏已保证两路径覆盖（v8 §7.1.4 新增断言属行为级加固）；若改为独立函数，既有护栏完全不覆盖，§7.1.4 断言是唯一防线。

### 0.1 v8 修订范围（历史，按 R8）

按评审报告 R8（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r8.md`）逐条修订。R8 判定 REVISION_REQUIRED（**0 阻塞** + 2 建议 F-26/F-27，并明示修完两条即达 APPROVED 条件）；R7 两条意见（F-24/F-25）经 R8 核验全部闭合，OD-6 定案 W1 与存储选型维持（连续第四轮）。两条意见经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-26 `projection_window_scalar_v1` 的协商机制未定义：direct（`server.go:808`）与 relay（`main.go:693`）**各自独立**调用 `negotiateProjectionWindowV1`，capability 字符串只在 `server.go` 出现（3 处）、relay 靠函数复用覆盖——W1 选「复用」还是「独立函数」决定 relay 是否自动覆盖；漏 relay 会使 iOS 在 Relay（生产路径）收不到 scalar echo → 双维度 gate 永不满足 → **静默永久回 full**，只在 §7.4 场景 5 暴露 | 建议 | §3.7 W1 机制行定案为**在 `negotiateProjectionWindowV1` 内扩展**（relay 自动覆盖、零额外接线），并写明「若实施时改为独立函数，必须 direct+relay 两处接线并附双路径 echo 断言」的约束；§7.1.4 补 direct+relay 双路径 scalar echo 断言；§6 S-1 组件列同步 |
| F-27 re-freeze 批次未覆盖 `schemaRevision` 纪律，且存在既有 doc/code 分歧（代码常量 `BridgeProtocolSchemaRevision = "2026-09-05"`（`bridge_v1_schema.go:77`），README 记 `2026-10-06-r1`（`:15`）；字段纯信息性，`server.go:716`/`hello_handler.go:188` 只填充不比较） | 建议 | §3.7 re-freeze 批次增补第⑥项：按惯例 bump `schemaRevision` 并在 `bridge_v1_schema.go` 累积变更注释；README `2026-10-06-r1` 分歧显式登记为**既有已知分歧、不得在 S-1 变更边界内顺带修正**（独立处置，防无关分歧卷入本批次） |

本章之外仅做必要一致性维护（§10 评审重点更新），未改动 W1 定案、re-freeze 批次既有五项、peer 组合表、存储选型、架构方向、切片划分与其余门结构。

### 0.2 v7 修订范围（历史，按 R7）

按评审报告 R7（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r7.md`）逐条修订。R7 判定 REVISION_REQUIRED（**0 阻塞** + 2 建议 F-24/F-25，并明示修完两条即达 APPROVED 条件）；R6 三条意见（F-21/F-22/F-23）经 R7 核验全部闭合，OD-6 定案 W1 与存储选型维持。两条意见经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-24 re-freeze 批次第⑤项 iOS 断言行号错位（v6 四处误引 `:51`——该行断言 window **描述符** key，`scalarState` 落在 Result 层不触碰它；真正会失败的是 response 级断言 `:48`，v6 全文未引）；批次③漏冻结样本**双副本**同步义务 | 建议 | §3.7 断言清单第 3 条、批次③⑤、§7.2.1、§10.11 四处行号更正（`:51` → `:48`，并补注 `:51` 与 Mac `:77-80` 为描述符级、不受影响）；批次③补「两仓独立副本（实测同哈希 `6853e4ac…`，iOS 测试经 `fixtureRoot` 读本仓副本）必须同步更新并保持字节一致」 |
| F-25 重试次数指标在 window 路径下不可观测（window handler 无 `projection_rpc` trace，grep = 0；fence 日志无 requestId 且 full/window 共用）；双维度 gate 的合法回退若不区分口径会被误计为重试 | 建议 | §7.3 重试指标行补**可观测前提**（S-1 交付 window 路径与 full 同构的 `projection_rpc` trace，full 侧锚点 `handlers_projection.go:89/:175`）与**计数口径**（合法回退 ≤1 次不计重试；hydrating/网络失败的重复发起计入）；§6 S-1 组件列补该 trace 交付项 |

本章之外仅做必要一致性维护（§10 评审重点更新），未改动 W1 定案、re-freeze 批次结构、peer 组合表、存储选型、架构方向、切片划分与其余门结构。

### 0.3 v6 修订范围（历史，按 R6）

按评审报告 R6（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r6.md`）逐条修订。R6 判定 REVISION_REQUIRED（1 阻塞 F-21 + 2 建议 F-22/F-23），意见经本轮独立源码亲核后**全部采纳**（无驳回项）；R6 同时复核 F-20 已闭合、OD-6 定案 W1 维持、§3.2.2 存储选型维持，相关状态一并记录：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-21 W1 的 `scalarState` wire 增量未登记协议冻结义务，且会打破 Mac/iOS 两侧三处冻结字段硬断言 | 阻塞 | §3.7 W1「代价」行改为 **R10 re-freeze 前置登记**（更正「标准 additive」的不完整定性）：列出三处冻结断言锚点与冻结样本，处置 =「更新冻结物而非删除断言」；re-freeze 五项批次内容写入 §3.7，并纳入 §5 Gate A 与 §6 S-1 前置（与 OD-6 定案同级）；§7.1.4 / §7.2.1 补冻结断言更新项；§8 风险表补「re-freeze 批次被绕过」行 |
| F-22 「未收到 scalar echo 即不走 window」需 iOS eligibility 双维度，「新 Mac + 旧 iOS」组合行为未评估 | 建议 | §3.7 W1 补 **peer 组合行为表**（四格逐格写明是否走 window、scalar 来源、执行态正确性）；登记 iOS 实现点（`isProjectionWindowEligible` 扩双维度）；「新 Mac + 旧 iOS」定性为既有缺口形态（由 iOS 版本升级解决，不在 S-1 范围）；服务端 echo 绑定作为已考虑并否决的替代（理由记录）；§7.2.1 补断言；§8 风险行更新 |
| F-23 §2.1 iOS 行提交哈希与未提交枚举未回填实测值 | 建议 | §2.1 iOS 行回填 R6 实测（`dfb77ed9…`，纯 docs 单文件 +251 行漂移，8 项未提交逐项列出），保留时点快照语义与 S-0 重生成义务 |

本章之外仅做必要一致性维护（§10 评审重点更新），未改动 W1 定案、fence scalar 分支设计、存储选型、架构方向、切片划分与其余门结构。R4/R5 意见（F-17/F-18/F-19/F-20）经 R6 核验全部闭合；R6 确认 F-21 属方案在 W1 wire 增量上从未登记的既有协议纪律义务（历轮第三次「方案没写的约束」盲区），非 v5 修订回归。

### 0.4 v5 修订范围（历史，按 R5）

按评审报告 R5（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r5.md`）逐条修订。R5 判定 REVISION_REQUIRED（1 阻塞 F-20 + 0 建议），意见经本轮独立源码复核后**采纳**（无驳回项）；R5 同时完成 OD-6 定案复核（W1 维持）与 §3.2.2 存储选型定案复核（维持），相关状态一并更新：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-20 §3.7 W1 fence 语义（v4 按 F-17 重写后）遗漏 `decide` 路由维度：清空仅作用于 decide=true pending frame；「Execution-only 边界」在当前代码不可达（真实存在的 Execution-only patch 走 decide=false 常规链，不被清空）；§7.1.4 断言缺构造前提 | 阻塞 | §3.7 fence 语义段补 decide 路由维度（第四个事实维度）并修正三个 bullets 的归因；「真实边界」改写为「防御未来新增 detail commit 构造点产生 Execution-only commit」（现可达性为零）；「execution 变化通常伴随 turn ops」句改写为「安全性来自 decide 路由」；边界处置（fence scalar 分支）重定位为防御性分支，OD-6 定案条件 2 设计与理由不变；§7.1.4 第三条改写为双链断言、第四/五条补构造前提与真实样本 |
| OD-6 定案复核（R5 §3：W1 维持；定案条件 1 在 v4 未完全达成） | — | §4 OD-6 行补 v5 记录（条件 1 随 F-20 修订达成；条件 2 设计不变、定位为防御性）；§5 Gate A 补「S-1 开工前置包含 F-20 修订」 |
| §3.2.2 存储选型定案复核（R5 §4：维持） | — | §3.2.2 定案标注补 R5 复核维持记录 |

本章之外仅做必要一致性维护（§10.8 复核请求更新为 decide 维度、§10.10 措辞精确化），未改动 W1 定案、fence scalar 分支设计、架构方向、切片划分与其余门结构。R4 三条意见（F-17/F-18/F-19）经 R5 核验全部闭合；R5 §6 确认 F-20 属 v4 新改内容的机制性偏差（R4 的 F-17 证据亦未覆盖 decide 维度），非历轮意见回归。

### 0.5 v4 修订范围（历史，按 R4）

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-17 §3.7 W1 fence「已知限制」归因与代码实际行为不符；真实边界（Execution-only patch 被 fence 清空）未覆盖 | 阻塞 | §3.7 W1「fence 语义」段按代码真实行为重写（held 集合连接级累积、`patchTurnIDs` 交集判定、清空仅发生在 fence pending 路径）；「已知限制」替换为真实边界 = 纯 Execution 变化（无任何 turn op）的 patch 在 fence 期间对 window 连接被清空 `Execution`；按定案条件 2 显式处置（fence scalar 分支，主路线）；§7.1.4 第三条与 §7.4 场景 8 断言同步改写 |
| F-18 F-13 登记的 capability 双门偏离无收口路径 | 建议 | §2.2 已知偏离行补收口指针；§6 登记收口实施项（归属 S-3 协议冻结，方向二选一） |
| F-19 §1.1 两处数字口径标注残留 | 建议 | 「约 97MB」标注 101,520,809 B（96.8 MiB）；6,347,762 B 补注 python 口径 + Go 验收口径数字；全文「97MB」统一为「约 97MiB」 |
| OD-6 定案回填（R4 §3，owner 委托评审轮定案） | — | **定案 W1（window 响应 additive 携带 scalar state）**，定案条件 1（= F-17 修订）与条件 2（Execution-only 边界处置）随本轮回填；§3.7 措辞从「推荐/待定案」改为定案；§4 决策表回填并冻结；§5 Gate A / §6 S-1 / §10.8 同步 |
| §3.2.2 存储选型定案复核（R4 §4 维持） | — | §3.2.2 定案标注更新为「R4 复核维持」；选型内容与格式设计不变 |

本章之外仅做必要一致性维护（「定案路线」→「W1 定案路线」等交叉引用、口径统一），未改动架构方向、切片划分与其余门结构。R3 的 6 条意见（F-11~F-16）+ R2 遗留 F-10 经 R4 独立核验全部闭合，本轮不再改动其修订内容。

### 0.6 v3 修订范围（历史，按 R3）

按评审报告 R3 逐条修订：R3 全部 6 条意见（含 2 阻塞）经 v3 轮独立源码/日志/样本复核后全部采纳，无驳回项；F-16 的重试事实按该轮复测修正为 8 次投递（R3 记 4 次为少算）；R2 遗留 F-10 一并处置。R4 已对下表全部闭合项独立核验通过：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-11 `window_0` 冷开不携带会话级标量状态 | 阻塞 | §1.2 R-6 改为主动设计目标；§2.2 补现状锚点；新增 §3.7 三条修复路线完整分析（登记 OD-6，owner 已于 2026-10-17 委托评审轮定案）；S-1 范围扩展（§3.3/§6）；新增 E-10 与 §7.1/§7.2/§7.4 scalar state 定向验收；§3.4.1 preview 同存 scalar |
| F-12 SQLite 选型与仓内构建/依赖约束冲突 | 阻塞 | §3.2.2 重写为「存储选型决策」：候选对比（含 go.mod/CGO/Xcode 构建/CI/iOS 影响）、与既有纪律（`session_catalog.go` / `import_guard_test.go`）的关系、定案 Mac=bbolt + iOS=文件型 store；Gate A 与 S-2 前置同步（§5/§6）；§8 风险与回退更新 |
| F-13 window capability 单门实现与协议文档双门规范偏离 | 建议 | §2.2 登记为已知偏离（S-1 不改）；E-3 取证方向补 descriptor 维度（§5） |
| F-14 window 路径 `forceColdInspection` 无 `sinceRev` 门 | 建议 | §3.2.3 补组合路径与 IO 上界；§7.3 补指标行；S-2 登记「Ready 且 source 未变则跳过冷检」等价门实施项（同时闭合 F-3 残余） |
| F-15 全部字节指标未声明 JSON 编码口径 | 建议 | §1.1 新增口径声明（UTF-8 字节；验收以 Go `json.Marshal` 输出为准）并记录三种口径实测差异；§7.3 表头口径说明 |
| F-16 事故重试事实弱化、验收缺重试次数指标 | 建议 | §1.1 修正为实测 8 次全量投递（用户可见重开至少 4 次）；§7.3 新增重试次数指标行；§7.4 每场景记录重试计数；§9 第一阶段补重试次数 0 |
| F-10（R2 遗留）iOS 未提交文件枚举漂移 | 建议 | §2.1 iOS 行标注「时点快照」语义；S-0 开工时按 P0 来源门重生成来源清单 |

本章之外仅做必要一致性维护（术语、交叉引用），未改动架构方向、切片划分与门结构。

### 0.7 v2 修订范围（历史，按 R1）

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-1 preview cache 身份 key 与 epoch 行为未定义 | 阻塞 | §3.4.1 定义 bridge ID 术语与 key 构成；§3.4.2 状态机补 epoch 变化分支与安全性依据；§6 S-4、§7.2、§7.4 场景 4 写明预期 |
| F-2 R-1 与 frozen v1 never-split-a-turn 偏差 | 阻塞 | §1.1 补实测超大 turn 事实；§1.2 R-1 改为受上限**规则**约束；§7.3 window encoded bytes 行同步口径 |
| F-3 重启场景 prefix 校验 IO 成本未定义 | 建议 | §3.2.3 补 prefix 校验策略与重启预算；§7.3 补指标行（v3：F-14 组合路径补全残余） |
| F-4 首屏目标未区分 Mac 冷/热 | 建议 | §1.2 R-2、§7.3 注明 warm 前提，冷场景单列预算；§7.4 补状态记录要求 |
| F-5 OD-2 与 S-1 门控关系歧义 | 建议 | §3.3 Phase A、§4 OD-2 影响切片、§5 Gate A 三处对齐 |
| F-6 未引用 think.md 复盘 | 建议 | 新增 §2.5 检索记录 |
| F-7 Codex 锚点路径不完整 | 建议 | §2.3 改为 `codex-rs/cloud-client/README.md:18` |
| F-8 测试矩阵缺两项 | 建议 | §7.1 补多设备并发；§7.2 补 hydrating retryable 重试节奏；§7.3 冷场景计入 |
| F-9 v2 与 turn_detail 家族关系缺倾向 | 建议 | §3.3 冻结清单第 6 项写明倾向 |

## 1. 摘要与范围

### 1.1 问题

超长 Claude session 在 iOS 打开时会出现：

1. 首开十几秒无内容；
2. iOS 15 秒 hard cap 触发「无法加载会话投影（超时）」；
3. 反复重试后才成功。实测（v3 修正，F-16）：事故窗口 17:30:56–17:33:52 内，同一 session 共发生 **8 次** `sinceRev=0 outcome=snapshot` 全量投递（req_18 / req_33 / req_54 / req_58 / req_67 / req_77 / req_82 / req_91，connectionGeneration 1→5），用户可见的「重新打开」至少 4 次；
4. 成功路径仍反复传输和处理约 34MB 的完整 projection。

**字节计量口径声明（v3 补，F-15）**：本文全部字节数字 = **UTF-8 编码字节长度**、JSON 序列化使用 `ensure_ascii=False` + 默认分隔符。该口径未声明时会系统性漂移——同一样本三种口径实测：整棵 projection 在 `ensure_ascii=True`（CJK 转义为 `\uXXXX`）下为 40,663,541 B（虚高 18%），`ensure_ascii=False` + 紧凑分隔符为 34,336,858 B；最大单 turn 在 `ensure_ascii=True` 下 7,940,153 B（虚高 25%）；以**字符数**而非字节数计量还会再低约 17%（本样本最大 turn 字符数 5,284,620 vs 字节 6,347,762，差值即约 53 万个 CJK 字符的 2 字节溢价）。**验收与指标一律以 Go `json.Marshal` 输出字节为准**（真实 UTF-8 + 紧凑分隔符 + `<`/`>`/`&` HTML 转义，即生产 `turnEncodedSizes` 的口径，`go-bridge/projection_window.go:146-155`）；§7.3 表头重申。

2026-10-17 样本 `bbe396e6-11ea-4754-943e-df5d2effcd14` 的关键事实：

- Claude transcript：101,520,809 B = 96.8 MiB（下文沿用「约 97MiB」表述；v4 补口径，F-19——原「约 97MB」为 MiB 口径误标，十进制 MB 口径应为约 101.5MB）；
- Mac 冷 hydrate：约 1.794s，不是主要瓶颈；
- Mac 返回 full projection：约 34MB；
- iOS hard cap：15s；
- 当时连接为 Direct LAN，`relay_gzip_v1` 不适用；
- 本地估算 gzip：level 1 约 11.2MB，level 6 约 9.6MB，仍不能消除 iOS decode / replica / presentation 成本；
- 按现有 4MiB window 规则估算，尾部 17 个 turns 约 3.79MB；Relay gzip level 6 后约 1.08MB。现有 window 机制本可把本次 payload 从 34MB 降到 4MiB 内。
- 最大单个 turn 序列化后 6,347,762 B（十进制约 6.35MB；v4 补口径，F-19——此为本节声明口径 python `ensure_ascii=False` 的数字，Go `json.Marshal` 验收口径实测 6,336,926 B，两口径均 > 4MiB，临界判定不受影响）> 4MiB，且不在尾部 17 turns 内（index 1/119）。按 frozen v1 的 never-split-a-turn 语义（`go-bridge/projection_window.go:24-26`），若该 turn 落入窗口，payload 可合法超过 4MiB——验收口径见 R-1 与 §7.1.1。
- **现状 `window_0` 响应不携带会话级标量状态**（v3 补，F-11）：`ProjectionWindowResponse` 只有 `Window/Turns/SyncRev/Resume`（`go-bridge/projection_window.go:77-82`），iOS 侧 `emptySession` 将 execution 硬编码 `.idle`（`SessionProjectionWindow.swift:96-99`）。这意味着「让首开真正走 window」本身会引入执行态/权限锁丢失，修复路线见 §3.7（OD-6，v4 已定案 W1）。

原始证据：

- 截图：`/Users/jacklee/Downloads/截屏 2026-10-17 17.31.18.heic`
- Mac log：`~/Library/Application Support/CordCode Link/logs/go-bridge.log`
- Transcript：`~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/bbe396e6-11ea-4754-943e-df5d2effcd14.jsonl`
- Checkpoint：`~/Library/Application Support/CordCode Link/session-projection/checkpoints/683c24fad8a17ef8c833319a3e2e68af7064d81bf4d819c4b1c971e52e202c3f.json`（34,537,988 B，mtime 2026-10-17 17:30:57，评审期未变化）

### 1.2 目标结果

| ID | 结果 |
| --- | --- |
| R-1 | 超长 Claude session 首开不再发送完整 projection；`window_0` payload 受既有上限**规则**约束——多 turn 累积 ≤4MiB，单个超过累积上限的 turn 仍整 turn 单独成页、payload 可超 4MiB（frozen v1 never-split-a-turn，`go-bridge/projection_window.go:24-26`）；超大 turn 场景由 Phase B summary page（≤1MiB 目标）收口。 |
| R-2 | iPhone 16 Pro Direct LAN 上，对本次约 97MiB/119 turns 样本（字节口径见 §1.1），权威 window 首屏目标 ≤3s；Relay 正常网络目标 ≤5s。前提：Mac 投影状态 warm（checkpoint/materialization 命中，无需全量 hydrate 重建）；Mac 冷全量 hydrate 场景单列预算（§7.3）。目标是实施验收，不是当前已验证结果。 |
| R-3 | iOS 15s hard cap 保留为最后失败线；正常 bounded path 不应触达。超时后重试也只重试有界 page，不得回到 full projection。**单次打开的重试次数是独立验收指标（§7.3），不因「最终成功」而豁免（v3 补，F-16）。** |
| R-4 | 打开、切换、重连不再因为内存 projection 被清空而丢失所有可用状态；iOS 可显示明确标记的 last-good preview，同时继续权威同步。 |
| R-5 | Mac projection 持久化从单个大 JSON checkpoint 演进为可增量追加、可分页查询的物化索引；普通 transcript append 不触发全量重建。 |
| R-6 | Claude 运行中 session 仍保持现有 live-partial / terminal 收敛语义；窗口化不得丢失 active turn、permission card、执行状态和 patch 顺序。**（v3 补，F-11）这在本方案中是主动设计目标而非「保持现状」：现状 window 响应不携带 execution 等标量状态（§1.1/§2.2），S-1 生效即违反本条，必须随 §3.7 W1 定案路线一并交付，验收见 E-10 与 §7.1/§7.2。** |
| R-7 | 所有失败显式可见；不得用旧缓存冒充权威成功、不得回到 legacy history、不得造第二 timeline writer。 |
| R-8 | 其它 backend 的行为默认不变。协议和存储设计留出推广空间，但实施切片以 Claude 为先。 |

### 1.3 非目标

1. 不把 iOS→Mac 上行、Direct WebSocket、agent 本地 stdio 全部改成 gzip；这不是本次瓶颈。
2. 不用提高 15s timeout 作为主修复。
3. 不恢复 legacy `get_session_messages` 或 message snapshot 作为 SSV2 真相。
4. 不做 UI automation / snapshot 默认验证；真机验收由 owner 按明确矩阵执行。
5. 不把 OpenAI Codex 源码直接移植进 CordCode；只复用其架构不变量。

## 2. 来源与现状调查

### 2.1 来源身份

| 来源 | 身份 |
| --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，`feat/ios-native-message-timeline@11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；写入时工作树干净，当前变更为本文档与历轮评审报告（docs/ 未跟踪文件） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，`feat/ios-native-message-timeline@dfb77ed93c610165730a88f8483c80e78941d321`（**v6 回填 R6 实测，F-23**；方案写入时点为 `1e895ea9ef42bc83c287c3acd66c3d7616340b4a`，其间漂移经 R6 实测为**纯 docs 单文件新增**（+251 行评审文档），全部方案锚点文件内容零变化）；未提交 **8 项**（R2/R3/R6 三次实测一致）：M `CordCode.xcodeproj/project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、未跟踪 `StreamingHeightPresentation.swift`、`NativeTimelineSmoothHeightTests.swift`、`StreamingHeightPresentationTests.swift`——**本行为时点快照，S-0 开工时必须按 P0 来源门重新生成** |
| Codex 上游 | `/Users/jacklee/Projects/codex`，`main@ce0323f2f2d3b25e82806cd555237da3db4e1897`，干净 |
| 正式 Mac runtime | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，PID 8303，监听 8777；运行时构建身份与源码 HEAD 不相同，日志结论需区分 |

iOS 未提交修改属于当前用户或其他代理，本方案不依赖其内容，也不修改它们；历轮评审已确认全部锚点文件不在脏清单内。

### 2.2 现有 CordCode 能力

| 能力 | 锚点 | 结论 |
| --- | --- | --- |
| Mac window 生产开关 | `go-bridge/main.go:560`，`SetProjectionWindowEnabled(true)` | 生产源码已启用；正式 runtime 也含该能力符号 |
| window 上限 | `go-bridge/projection_window.go:24`，256 turns / 4MiB / 默认 100 turns | 复用；本次样本会被 4MiB 截到约 17 turns |
| window capability gate | `go-bridge/projection_window_handler.go:32` | 复用；未协商连接 fail-closed。**已知偏离（v3 登记，F-13，S-1 不改）**：实现为 hello echo 单门（`go-bridge/server.go:373-395` 只查客户端声明 + 服务端开关），而 `docs/protocol/bridge-v1.md:2173-2175` 要求「clients MUST gate window RPCs on the descriptor list, not the global echo」的双门中，descriptor 侧从未广告——全仓非测试代码无一处把 `projection_window_v1` 写入 backend descriptor，iOS `CCCodeBridgeBackendClient.swift:1308-1310` 同样只查 echo。E-3 取证已补 descriptor 维度（§5）。**收口实施项（v4 补，F-18）：已登记为 §6 S-3 的协议冻结项，方向二选一（descriptor 写入并按文档条件 echo，或修订协议文档为单门语义）** |
| **window 响应字段完备性（v3 补，F-11）** | `go-bridge/projection_window.go:77-82`（`ProjectionWindowResponse` 只有 `Window/Turns/SyncRev/Resume`）；对照 `go-bridge/projection_types.go:341-359`（`SessionProjection` 含 `Execution/PlanMode/SessionMode/CollaborationMode/CodexGoal/Goal`）；`projection_window.go:226-241`、`projection_window_handler.go`、`projection_window_older_hydrate.go` 三个文件零处引用 `Execution` | **现状 window 路径不投递 scalar state**；iOS `emptySession` 硬编码 `.idle`（`SessionProjectionWindow.swift:96-99`）、`applyingWindowPage` 只合并 turns、消费链 `isSessionExecuting`/输入框锁/`composerAwaitingUserAction` 读 `projection.execution.phase`（`ChatViewModel.swift:2419-2426` 等）。修复路线见 §3.7 |
| **window 冷源检查条件（v3 补，F-14）** | `go-bridge/projection_window_handler.go:102-106`（`forceColdInspection := params.SessionID != "" && (backend ∈ …)`，无 `sinceRev` 门）对照 full 路径 `go-bridge/handlers_projection.go:122`（`params.SinceRev == 0 && …`） | 每次 `window_0` 都强制冷源检查；成本上界见 §3.2.3（轻量，不是整读） |
| iOS window replica | `OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:590` | 已有 apply / pull / coverage ledger / cursor recovery |
| iOS 首开入口 | `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift:851`，调用 `pullOpenWindowed` | 源码已按 capability 走窗口 |
| iOS hard cap | `OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:145` | 保留，但 bounded path 不应触达 |
| Mac checkpoint | `go-bridge/projection_kernel.go:280`，`go-bridge/projection_kernel.go:1123` | 现状是整棵 JSON projection；composite source cut 必须精确匹配，append 后易整体重建 |
| Relay gzip | `docs/protocol/relay-frame-compression.md:3`，`go-bridge/relay_conn.go:296` | 只覆盖 Relay Mac→client；本次 Direct 无效 |
| iOS legacy message cache | `OpenCodeiOS/OpenCodeiOS/Services/MessageCacheManager.swift:6`，`OpenCodeiOS/OpenCodeiOS/Services/Storage/SessionSnapshotStore.swift:101` | 内存 LRU / 单文件 JSON，非 SSV2 projection cache；10MB 上限不适合长 session |
| SSV2 禁止 legacy load | `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift:98` | 保留；不能借 message cache 恢复旧双 writer |
| iOS projection decode | `OpenCodeiOS/OpenCodeiOS/Services/Bridge/ProjectionWireDecoder.swift:9` | 现状多次全量 JSON parse / re-encode，需要重构 |

### 2.3 Codex 可复用架构

| Codex 机制 | 锚点 | CordCode 采用方式 |
| --- | --- | --- |
| SQLite `thread_turns` / `thread_items` / projection state | `codex-rs/state/thread_history_migrations/0001_thread_history.sql:1` | 行级物化模型的**架构参考**（turn/item 行、projection state 行）；物理存储引擎不照搬——CordCode 侧选型见 §3.2.2（v3：受仓内约束，选 bbolt 而非 SQLite） |
| 只从 `next_rollup_byte_offset` / ordinal 处理 suffix | `codex-rs/thread-store/src/local/thread_history_materialization.rs:42` | Claude transcript 正常 append 只投影 suffix |
| metadata-only resume + `initialTurnsPage` | `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:422` | 对应 `window_0`，后续升级 summary page |
| `thread/turns/list` / `thread/items/list` 分页 | `codex-rs/app-server/src/request_processors/thread_processor.rs:5888` | 后续 `projection_window_v2` 拆 turn 骨架与 item detail |
| 官方 TUI 初始 5 turns / 100 items | `codex-rs/tui/src/app_server_session/history.rs:28` | 首屏 page 优先小窗口，滚动再加载 older |
| loaded thread 延迟卸载 | `codex-rs/core/src/config/mod.rs:4050`，`app-server/src/request_processors/thread_lifecycle.rs:18` | iOS preview cache 与 Mac kernel resident state 分层借鉴，不照搬 TTL |
| paginated model context 反向扫描 compaction | `codex-rs/thread-store/src/local/model_context.rs:30` | 以后模型上下文与 UI 投影进一步分离；本方案先解决 UI 打开 |

Codex `cloud-client` 本身没有历史缓存：`codex-rs/cloud-client/README.md:18` 明确 `Attach` 无 history snapshot/replay。它的快速体验来自 app-server / thread-store 的分页与物化索引，而不是 remote gzip。

### 2.4 关键矛盾

当前源码两端都实现了 `projection_window_v1`，但事故窗口内：

- Mac 只出现 `get_session_projection sinceRev=0`；
- 没有任何 `get_session_projection_window` RPC；
- 因此实际 iOS 包或运行时协商结果没有走到 window。

原因尚未证明。不能把它写成“功能缺失”，因为源码和正式 runtime 均有实现。S-0 必须先固定安装 / 协商证据，再允许后续切片宣称修复。

### 2.5 think.md 复盘检索（v2 补，repo 活文档纪律）

按 repo 规则（排查 session/history/加载异常必须先检索两仓 `think.md`）完成检索，v2 补记入方案。结论：以下条目均为**同症状不同根因**的历史复盘，无与本案冲突或可直接复用的结论。

- Mac `think.md:450`：iOS 27h 重连 91 次、每次重拉全量投影快照——「全量重拉」问题的先例，但发生在重连链路，本案是首开；
- Mac `think.md:834`：审批卡 15s 超时——审批控制面部件，与投影打开无关；
- Mac `think.md:1205`：冷源活跃信号 gate 永不 ready → iOS 15s 超时重试死循环——同症状（15s 超时 + 多次重试）在本产品有先例，可作 §1.1 问题背景佐证；根因是 gate 判定而非 payload 大小；
- iOS `think.md:3343` / `:3485` / `:3491`：legacy `get_session_messages` 路径的 15s 超时与软重连副作用——legacy 路径，本案是 SSV2 `get_session_projection`，不同链路。

## 3. 架构设计

### 3.1 总体数据流

目标打开链路：

```text
iOS open
  ├─ 读取本地 last-good projection preview（明确 stale/syncing）
  ├─ 发送 get_session_projection_window(window_0, bounded)
  └─ 收到 page（含 §3.7 W1 定案路线交付的 scalar state）后经唯一 ProjectionReplica writer apply

Mac
  ├─ Claude source inspector
  ├─ ProjectionKernel hydrate / live patch（仍是唯一真相 writer）
  ├─ ProjectionMaterializationStore 增量物化 turn/item 行
  └─ window handler 从 committed head / materialized page 切 bounded result
```

不变量：

1. **单一真相 writer**：Mac `ProjectionKernel` 继续拥有 projection truth；materialization store 是派生索引，不直接读 agent、不生成第二份真相。
2. **单一 iOS timeline writer**：权威数据（turns **与 scalar state**，v3 补）仍只能经 `ProjectionStore -> ProjectionReplica -> PresentationEngine` 进入 timeline；preview cache 只做显示层预览，不进入 replica。
3. **有界首开**：Claude 首开不得返回整棵 projection；旧 peer 可继续 full path，新 peer 一旦 window capability 生效不得静默回落。
4. **失败显式**：缓存必须带 freshness 状态；网络失败不能把 preview 标成 authoritative ready。
5. **身份先于数据**：所有持久化 page / row 先校验 backend、bridge 身份（持久 `bridgeId`；`bridgeEpoch` 仅作 staleness 标记与 provenance，见 §3.4.1）、session、source identity、schema，再参与显示或查询。

### 3.2 Mac Projection Materialization Store

#### 3.2.1 为什么替换大 JSON checkpoint

现有 checkpoint 保存整棵 `SessionProjection`。本次样本 checkpoint 约 34.5MB。它有三个结构性问题：

1. 每次保存 / 读取 / clone 都是整棵 projection；
2. composite source 的 checkpoint cut 需要精确匹配，transcript append 后容易变成 full rebuild；
3. 无法按 turn/item 分页查询，window 仍需要内存中完整 projection。

这不是把 JSON 压缩就能解决的问题；需要把派生状态拆成可增量更新的行。**（v3 补，F-12）本节论证的是「不该用单个大 JSON」，不构成「必须用 SQLite」——存储引擎选型见 §3.2.2 的候选对比与定案。**

#### 3.2.2 存储选型与物理格式（v3 重写，F-12）

**选型约束（全部亲核）**：

- 根模块 `go.mod` / `go.sum` 无任何 SQLite 驱动（无 `modernc.org/sqlite`、无 `mattn/go-sqlite3`）；现有依赖仅 toml/semver/webpush/circl/pty/websocket/compress/crypto；
- runtime 构建脚本（`MacBridge/project.yml:53`）为 `GOOS=darwin GOARCH=… go build`，**未设 `CGO_ENABLED`**——CGO 驱动会改变交叉编译前提与 Xcode 构建链；
- 仓内有明确的相反先例：`agent/grokbuild/session_catalog.go:14-16` 注释写明「SQLite is queried via the optional `sqlite3` CLI … to avoid adding a CGO/pure-Go sqlite dependency to the main MacBridge module」；`agent/opencode-web/import_guard_test.go:23-24` 把 `sqlite3` 列为该包 forbidden surface（语义是禁止 adapter 直接触碰 OpenCode 自身存储）；
- iOS 侧零 SQLite 栈：`OpenCodeiOS/project.yml` 仅 2 个 vendored package（MarkdownView / BorderBeamKit），全源码无 `import SQLite` / `GRDB`。

**候选对比与定案**：

| 候选 | 新依赖 | CGO | 构建链影响 | 与既有纪律关系 | 判定 |
| --- | --- | --- | --- | --- | --- |
| `modernc.org/sqlite`（纯 Go SQLite） | 大（传递依赖翻译版 libc，二进制 +10MB 级） | 否 | 无 | **正面推翻** `session_catalog.go` 记录在案的「避免纯 Go sqlite 依赖」立场，需显式理由 | 不选：为行级物化引入整套 SQL 栈性价比低 |
| `mattn/go-sqlite3` | 中 | **是** | `project.yml` 构建脚本需设 `CGO_ENABLED=1` 并匹配 C 工具链，破坏当前纯 Go 交叉编译 | 同上 | 不选：构建链代价最大 |
| `sqlite3` CLI 子进程 | 无 | 否 | 无 | 先例本身（仅限可选、只读发现路径）；核心常驻存储逐 patch shell-out 不可接受 | 不选：核心路径不能靠子进程 |
| **`go.etcd.io/bbolt`（纯 Go B+tree KV）** | **小（单模块，MIT，stdlib 依赖）** | 否 | **无**（纯 Go，现有 `go build` 直接可用） | **不触碰**：非 sqlite——`import_guard_test.go` 的 forbidden fragment 是 `sqlite3`（且仅 opencode-web 包内）；`session_catalog.go` 的立场针对 sqlite 依赖 | **定案（Mac 侧）** |
| 自研 append-only log + 内存索引 | 无 | 否 | 无 | —— | 不选：patch 流是 upsert/mutation 型而非纯追加，自研需重造 WAL/崩溃一致性/compaction，E-6 测试负担与出错面最大 |

**定案：Mac materialization store 用 bbolt；iOS preview cache（§3.4.1）用每 session 单文件 + 原子写，不新增任何 iOS 依赖。**该定案为工程决策（评审/owner 可推翻；推翻时须同步替换本节格式设计与 Gate A 前置）。**R4 复核维持（v4 记录）：五项选型约束全部亲核成立，四个替代候选无翻案依据；R5 复核再次维持（v5 记录，R5 报告 §4：五项约束独立亲核，无翻案依据）。**CI 已跑 `govulncheck`（根模块），新依赖纳入其覆盖。

**逻辑 schema（行模型保持 v2 设计不变；物理映射到 bbolt bucket/key，实施前在测试中冻结格式）**：

```text
materialization store 文件：
~/Library/Application Support/CordCode Link/session-projection/materialization.bbolt

bucket 布局（单文件、嵌套 bucket）：
<backend_id>/<session_id>/
  head          -> 行：schema_version, bridge_epoch(provenance), sync_rev,
                    source_generation, updated_at, scalar_state_json
  segments/<ordinal>  -> 行：identity, committed_cursor, observed_size, prefix_sha256
  turns/<8B 大端 ordinal + turn_id> -> 行：root_turn_id, status, started_at,
                    completed_at, duration_ms, first_user_item_id,
                    final_agent_item_id, turn_blob_json
  items/<turn_id + 8B ordinal + item_id> -> 行：updated_ordinal, item_type, item_json
```

说明：

- Phase A 先落 `turns/*` 的 `turn_blob_json`，保证行为闭环并降低迁移风险；
- Phase B 把 `turn_blob_json` 拆到 `items/*`，支持 summary page / item lazy detail（大 value 拆行同时缓解 bbolt 大 value 写放大，见 §8）；
- `scalar_state_json` 保存 execution、plan/session/collaboration mode、goal 等非 turn 状态；
- `head.bridge_epoch` 只记录最后一次 commit head 的 epoch，作 provenance / 排查字段，**不作为行有效性判据**——重启后行有效性按 schema version、backend/session、segment membership 与 cursor/prefix 判定（§3.2.3）。若把 per-start epoch 当有效性判据，每次重启都会整体误判失效，破坏 S-2 的重启增量目标；
- 所有写入在一个 bbolt `Update` 事务内完成（单写者模型，与 kernel 单 writer 对齐），head 与 rows 同 commit；
- 尾部分页查询 = `turns` bucket 游标 `Seek` + `Prev`（bbolt 原生支持反向游标），天然满足 window/older page 形态。

#### 3.2.3 增量折叠规则

正常打开：

1. 读取 `head` 与 `segments`；
2. 校验 schema、backend/session、source membership 和 prefix；
3. 若当前 source cursor ≥ committed cursor，只折叠 `[committed_cursor, current_cursor)`；
4. 将 suffix 产生的 changed turns / items upsert；
5. 同一事务推进 head、segment cursor、sync_rev。

失效与全量重建：

- schema version 不兼容；
- backend / session / segment membership 变化；
- 文件 truncation 或 cursor 回退；
- prefix digest 不匹配；
- source generation 显式变化；
- materialization 内部一致性校验失败。

失效是诚实重建，不是 fallback。重建完成后原子发布新 head。

prefix 校验成本与重启场景（v2 补 F-3；v3 补 F-14 组合路径）：

- 已确认现状：`digestProjectionSourcePrefix`（`go-bridge/projection_kernel.go:266-278`）用 `io.CopyN` 顺序读整个 prefix 再哈希——约 97MiB transcript 重启后若走该路径需整读；
- Codex 参照不重读 prefix：`thread_history_materialization.rs:42-45` 直接信任持久化的 `next_byte_offset`，只处理 suffix；
- 拟议策略（S-2 格式冻结时定案）：重启后首开先按 `(path, size, mtime)` 快速匹配 segment（零整读）；快速匹配命中即接受 committed cursor、只折叠 suffix；匹配失败或 head/segment 不一致时才退回全量 prefix digest 重算（`prefix_sha256` 仍持久化，用于 mtime 不可靠场景的最终判定；重算不匹配则按上方失效规则诚实重建）；
- **window 路径强制冷检（v3 补，F-14）**：window handler 的 `forceColdInspection` 无 `sinceRev` 门（`projection_window_handler.go:102-106`），**每次 `window_0` 都触发冷源检查**——与 full 路径（`handlers_projection.go:122`，仅 `SinceRev == 0` 时）条件不同。冷检成本上界（亲核）：`richHistoryTranscriptSegments` 只对每个 segment `os.Stat`（`agent/claudecode/continuation.go:379-392`），`projectionJSONLStartCut` 只做每 segment 尾部 4KiB 反向扫描定位换行（`handlers_projection.go:915-954`）——**都不是整读 transcript**。稳态（kernel Ready 且 source 未变）下 `BeginHydrateTransaction` 返回 `AlreadyReady` 不重建（`projection_kernel.go:1030-1045`）；
- **组合路径**（长 session 最常见场景）：transcript append 后首次 `window_0`（`source.Cursor > committedSourceCursor`）会走 catch-up——S-2 落地后该路径 IO 上界 = 冷检（stat + 尾部 4KiB 扫描）+ suffix 折叠，仍不整读约 97MiB transcript；但 checkpoint/store 未替换前的旧 `LoadValidated` 路径内含整读 prefix 的 `validateProjectionSourceCheckpoint`（`projection_kernel.go:389-415`），S-2 完成前该成本仍在。S-2 登记实施项：给 window handler 增加「kernel 已 Ready 且 source 未变则跳过冷检」的等价门（对齐 full 路径的 `sinceRev` 语义）；
- 重启后首开与上述组合路径的 Mac 侧时间预算均纳入 §7.3 指标表。

#### 3.2.4 与 live patch 的关系

`ProjectionKernel` 仍是 reducer / patch / fence 的唯一所有者：

- cold hydrate commit 后，将 changed turns 写入 materialization；
- live `projection_patch` flush 后，同一 materialization writer 按 patch 的 `upsertTurns`、`partOps`、`turnStateOps` 更新 rows；
- window handler 读取与 kernel head 一致的 store revision，并在 snapshot fence 下返回 page；
- store 落后时先推进到 requested head，不得返回旧 page 冒充当前 head。

Phase A 可先让 window 继续从 kernel in-memory snapshot 切片，materialization 只解决重启和增量重建；Phase B 再把 page 查询下推到 materialization store。

### 3.3 首屏 page 策略

#### Phase A：恢复并强制使用既有 `projection_window_v1`

不改 frozen v1 语义：

- iOS `pullOpenWindowed` 继续调用 `window_0`；
- Mac 4MiB / 256 turns 上限不变；
- 修复协商 / 安装 / 观测，使当前两端实际走 v1；
- iOS 增加请求 `limit` 的默认值（拟议 25；该数值属 OD-2 裁决范围，见 §4 与 §5 Gate A——OD-2 未裁决前 S-1 按现有默认 limit 100 turns 开工，4MiB 仍会进一步截断）。本次样本按字节预计返回约 17 turns / 3.79MB；
- **（v3 补，F-11）S-1 范围扩展**：恢复 window 实际路径必须**同时**交付 §3.7 W1 定案路线的 scalar state，否则 S-1 生效即违反 R-6——「让首开走 window」不是纯协商修复，同时是一次首开内容语义变更（从「full projection 含全部 scalar state」变为「window 页只有 turns」）。S-1 完成宣告需 E-10。

v1 回退规则保持：旧 Mac 或旧 iOS 可 full projection。对声明了能力的新 peer，不允许因非 typed 错误静默 full fallback。

#### Phase B：新增 `projection_window_v2` summary page

v1 的 `TurnProjection` 仍可能携带完整 parts。为避免单个超大 turn 主导首屏，新增 additive capability：

```text
projection_window_v2
```

请求：

```ts
{
  sessionId: string,
  backendId: string,
  direction: "window_0" | "older" | "newer" | "latest" | "locate",
  cursor?: string,
  limit?: number,
  itemsView: "summary" | "full",
  anchorTurnId?: string
}
```

响应：

- turn descriptor：ID、状态、时间、duration、first user summary、final agent summary；
- coverage cursor；
- syncRev / resume；
- `itemsView:"summary"` 时不携带完整工具输出 / diff / 长过程明细；
- 明细通过 `session_turn_items`（turn_detail_lazy/chunks 家族）或 v2 item page 按需加载。

v2 冻结前必须定义：

1. summary card 的最小可渲染字段；
2. active turn / permission / question 的例外；
3. item ID 与 turn generation 的绑定；
4. live patch 对 summary 与 detail 的更新语义；
5. cursor stale / scope mismatch / limit 错误；
6. 与 `turn_detail_lazy_v1`、`turn_detail_chunks_v1` 的关系。**倾向（冻结评审定案，v2 补）**：v2 item page 演进 / 复用 turn_detail 家族已有的 item 绑定与 chunk 语义，不平行新建第二套明细协议；若冻结评审推翻该倾向，必须显式论证两套通道并存的理由与迁移路径，否则不得另立。

Claude 先实现，其它 backend 只保留协议推广空间。

### 3.4 iOS last-good preview cache

#### 3.4.1 存储模型

新增 `ProjectionPreviewCacheStore`，不复用 `MessageCacheManager`：

- **存储形态（v3 改，F-12）：每 session 单文件 + 元数据行 + 原子写（temp + rename，对齐仓内 `pinstore` / sidecar 纪律），不引入 SQLite/GRDB**——单 session 的 last-good page 本就有界（≤4MiB 规则 + scalar state），文件形态足够；容量控制按目录级 LRU（按 savedAt 淘汰最旧 session 文件）+ 总字节数上限，拟议 64MB；
- key（文件名/目录名）：backend identity + bridge ID + session ID；
- metadata：schema、bridge ID、bridge epoch（仅供 staleness 标记，不参与 key）、syncRev、head/tail turn ID、source generation、savedAt；
- 内容：最近 authoritative window page 的 turn rows **+ scalar state（v3 补，F-11 关联）**——preview 与权威 page 同源，scalar 缺口与 F-11 是同一问题的两个表现，修复 F-11 时一并覆盖 preview 的 scalar 字段；
- 无 TTL。淘汰只因容量 / schema / identity invalidation，不是因为时间过期。

术语与 key 语义（v2 补，F-1）：

- **bridge ID** = Mac `identity.json` 中跨重启持久的 `bridgeId`（`go-bridge/data_dir.go:15-23`；经 hello 的 bridge profile 下发，`go-bridge/hello_handler.go:108-111`）。它**不是** `bridgeEpoch`——后者是每次 runtime 启动重新生成的进程唯一 UUID（`go-bridge/runtime_startup.go:137-140`、`go-bridge/main.go:158`）。
- key **不含 epoch**：Mac runtime 重启（epoch 变化）不使 preview 全部 miss；换 Mac / 重置 bridge 身份（bridgeId 变化）才整体 miss。
- epoch 存入 metadata，仅用于打开时的 staleness 判定与显示分级（见 §3.4.2）。

Phase A 可存 bounded page 的 turn rows + scalar state；Phase B 拆 summary / detail rows。

#### 3.4.2 打开状态机

```text
CacheMiss
  └─ show loading, pull window_0

CacheHit
  ├─ off-main decode / map preview（含 scalar state 的显示映射）
  ├─ UI shows last-good + “正在同步” watermark
  └─ concurrently pull authoritative window_0

CacheHit（metadata.epoch ≠ 当前 epoch：Mac runtime 已重启，v2 补）
  ├─ 仍显示 last-good，stale 标记升级（如「上次同步于 Mac 重启前」）
  ├─ 照常 pull authoritative window_0（key 不变，不 miss、不清 cache）
  └─ authoritative 到达并 apply 后按 AuthoritativeApplied 处理

AuthoritativeApplied
  ├─ replace preview through ProjectionReplica/PresentationEngine
  ├─ save new last-good page（写入当前 epoch 进 metadata）
  └─ clear syncing watermark

AuthoritativeFailed
  ├─ keep preview visible
  ├─ show explicit stale / failed state
  └─ retry bounded page only
```

关键点：

1. preview 不写入 `ProjectionReplica`，避免污染 revision / cursor / patch chain；
2. cached page 不改变 `sessionSyncV2Active`；
3. cache 命中不能清除真实错误；
4. stale 时输入框 / 执行动作按现有 projection 权威状态处理，不能把 preview 当 ready；
5. epoch 不匹配只升级 stale 显示、不丢弃 preview，安全性依据（v2 补）：preview 是纯显示层，不进入 replica；session 真相由 Mac 侧 projection 唯一拥有，runtime 重启不改变 transcript source 身份本身。若新 epoch 下 session 被 compact / 重写，权威 page 到达后经 replica 整体替换 preview；等待期间输入框 / 执行动作按第 4 点的权威状态处理，不会把重启前内容当 ready 提交。

#### 3.4.3 切换 session 不再清掉持久能力

现有 `switchSession` 调用 `projectionStore.clear(sessionId:)` 是为了防止跨 session 泄漏，合理保留。但持久 preview cache 不随之删除；重新进入同一 session 时可先显示 last-good，再等待权威 page。

### 3.5 iOS 单次解析管线

现状：

1. transport 先把完整 inbound frame decode 成 `CCCodeBridgeInboundEnvelope`，其中 `data` 是动态 `BridgeAnyCodable`；
2. projection trace 又 re-encode / parse envelope；
3. `ProjectionWireDecoder` 再次 `JSONSerialization` 全量 parse；
4. 取出 `data` 后 re-encode；
5. `JSONDecoder` 再 decode。

这会把 34MB 放大成多次对象图构建和内存峰值。

目标：

1. 新增轻量路由头扫描，只读 `type/requestId/error`，不构建 `data` 对象；
2. result route 直接用 typed `ResultEnvelope<Payload>` 一次 decode；
3. projection trace 只记录顶层字段 / 已有 request context，不 re-encode payload；
4. v2 page / cache row 分别 decode，可在 actor 上分批执行；
5. 所有 decode 计时：wire bytes、decode、replica apply、presentation、MainActor commit。

Phase A 至少移除 `ProjectionWireDecoder` 中 `data` re-encode 与 trace re-encode；Phase B 完成单次 typed decode。

### 3.6 传输压缩定位

保留现有：

- Relay Mac→client：`relay_gzip_v1`，≥32KiB 且压缩更小才启用；
- Relay chunks：gzip 后分片与公平调度。

本次不新增 Direct gzip。理由：

1. 事故为 Direct，但根因是 34MB full projection；
2. v1 window 已把样本降至约 3.79MB；
3. v2 summary page 目标 ≤1MiB；
4. Direct gzip 不能消除 iOS JSON 对象图和 presentation 成本。

若后续仍有弱网 Direct 需求，另案冻结 `direct_gzip_v1` 或标准 `permessage-deflate`，不得改变 `relay_gzip_v1` 语义。

### 3.7 window 路径的会话级标量状态交付（v3 新增，F-11；v4 定案 = W1，OD-6 已回填冻结）

**现状缺口（全部亲核）**：`window_0` 是 iOS 冷开唯一入口（`ProjectionStore.swift:763-777`），但

1. Mac `ProjectionWindowResponse`（`projection_window.go:77-82`）只有 `Window/Turns/SyncRev/Resume`；`sliceProjectionWindowWithUpstream`（`:226-287`）与 `projection_window_handler.go`、`projection_window_older_hydrate.go` 零处引用 `Execution`；
2. iOS `applyingWindowPage`（`SessionProjectionWindow.swift:116-176`）只合并 turns；`emptySession`（`:96-98`）把 execution 硬编码 `.idle`；`ProjectionReplica.applyWindowPage` 构造的 change set 全部 `execution: nil`；
3. fence 完成事务对不持有该 patch 任何 turn 的 window 连接，排队（fence pending）的 **decide=true** frame 被显式清空 `Execution`（`projection_snapshot_fence.go:224-235`；decide 路由维度见 W1 fence 语义，v5 补 F-20）；
4. 消费链全部读 `projection.execution.phase`：`isSessionExecuting`（`ChatViewModel.swift:2419-2426`）、输入框锁、`composerAwaitingUserAction`（`ChatViewModel+TaskDock.swift:33`）、session badge（`ChatViewModel.swift:1227-1233`）。

后果：S-1 修好协商后，运行中的 Claude session 首开将 turn 内容正确、但 execution 恒为 `.idle`——不显示执行中、输入框不禁用、`requires_action`（权限卡/提问）不锁发送。iOS 有独立于 projection 的 `runtimeStateStore` badge 路径（`ChatViewModel+TaskDock.swift:36-39`）与 `isGenerating` 本地状态，不是完全无显示，但与 projection 分支形成**双源不一致**。

**三条修复路线（OD-6 已定案 W1，v4 回填）**：owner 于 2026-10-17 裁决「三条路线都写成待评审员确定，由评审员分析」；R4 评审报告 §3 完成定案——**W1（window 响应 additive 携带 scalar state）**，附两个定案条件（条件 1 = fence 语义按代码真实行为重写、条件 2 = Execution-only 边界显式处置），均已随 v4 回填本节与 §7.1/§7.4。W2/W3 的淘汰理由经 R4 亲核维持（W2 冷开二次请求竞态面更大且多一次 RTT 挤压 R-2；W3 的 `runtimeStateStore` 是 live 事件消费者，冷开运行中 session 无历史事件可学，结构性不覆盖主场景）。W1/W2/W3 的机制分析保留如下，作为定案依据与后续实施参照：

**W1：window 响应 additive 携带 scalar state（OD-6 定案路线，v4 冻结；v3 曾列为推荐）**

- 机制：新增 additive capability `projection_window_scalar_v1`（hello 声明 + Mac echo，echo 条件 = 现有 `projection_window_v1` 条件 + 本实现落地）；**协商实现定案（v8 补，F-26）：在既有 `negotiateProjectionWindowV1` 函数内扩展 scalar echo 分支**——direct（`go-bridge/server.go:808`）与 relay（`go-bridge/main.go:693`，注释明写 "relay path mirrors the direct hello negotiation exactly"）**各自独立调用该函数**，capability 字符串只在 `server.go` 出现（:255/:366/:392）、relay 恰因函数复用而覆盖——扩展该函数使 relay 路径自动获得 scalar echo、零额外接线；**约束**：若实施时改为独立 `negotiateProjectionWindowScalarV1` 函数，必须同时在 direct 与 relay 两个调用点接线并附双路径 echo 断言（§7.1.4），否则 iOS 在 Relay（iPhone 生产路径）永远收不到 scalar echo → 双维度 gate 永不满足 → **静默永久回 full**，只能在 §7.4 场景 5「Relay 弱网重开」暴露；Mac `ProjectionWindowResponse` 增加可选 `scalarState` 字段：`execution`（必选，覆盖 running / requires_action / activeTurnId）+ `planMode/sessionMode/collaborationMode/goal`（可选，语义镜像 `SessionProjection` 的 additive scalar 字段：nil = 该 backend 无此投影）。字节量级为数百 B，不影响 4MiB 预算；
- iOS：`applyingWindowPage` 合并 `scalarState`，经 `ProjectionReplica` 与 full 路径同构地写 execution；`emptySession` 的 `.idle` 仅作无数据兜底；
- 旧 peer 兼容与 peer 组合行为表（v6 补，F-22——不再以「旧客户端忽略该字段」代替组合行为结论）：
  - **iOS 双维度 gate 实现点**：现 `isProjectionWindowEligible()`（`ProjectionStore.swift:595-599`）只查 window echo 单维度（`supportsProjectionWindow` = `transport.supportsAcknowledgedCapability("projection_window_v1")`，`CCCodeBridgeBackendClient.swift:1308-1310`）。W1 落地时扩为**双维度**：window echo **AND** scalar echo（经同一 `supportsAcknowledgedCapability` 通用机制加 `projection_window_scalar_v1` key 即可，`CCCodeBridgeTransport.swift:723-725`）——任一未 echo 即 `pullOpenWindowed` fail-closed 回 full 路径；
  - **四组合行为表**（「新/旧」= 该端是否含 W1 实现）：

  | 组合 | window echo | scalar echo | iOS 行为 | 执行态正确性 |
  | --- | --- | --- | --- | --- |
  | 新 Mac + 新 iOS | ✓ | ✓ | 走 window，`scalarState` 随 page 交付 | 正确（E-10 验收对象） |
  | 旧 Mac + 新 iOS | ✓（旧 Mac 不认识 scalar，不回显） | ✗ | 双维度 gate 不满足 → fail-closed 回 full path | 正确（慢，但语义无损 = S-1 前现状） |
  | 新 Mac + 旧 iOS | ✓ | ✗（旧 iOS 未声明 scalar） | 旧 iOS 只查 window echo → 走 window、收不到 `scalarState` | **既有缺口形态**（= S-1 修复前若协商生效的形态；E-3 取证将确认当前安装组合实际从未走到 window）。定性：既有问题，由 iOS 版本升级解决，不在 S-1 范围 |
  | 旧 Mac + 旧 iOS | ✗（现状未生效） | ✗ | full path | 现状 |

  - **已考虑并否决的替代**：Mac 端把 window echo 绑定到「客户端声明 scalar capability」（未声明者不 echo、直接回 full）可消除「新 Mac + 旧 iOS」格的缺口，但需在 re-freeze 中额外修改 frozen v1 的 hello echo 条件、扩大再冻结范围；鉴于本产品 Mac+iOS 同仓同节奏发布、该组合仅存在于升级窗口且行为不劣于现状，**不采用**（若 E-3 取证发现存量旧 iOS 已在实际走 window，重新评估并回到 re-freeze 评审）；
- fence 语义（v4 按 R4 F-17 重写、**v5 按 R5 F-20 补 `decide` 路由维度**——v4 重写仍遗漏该维度，三处归因与代码不符，已修正；锚点：`go-bridge/projection_snapshot_fence.go:210-236`、`go-bridge/event_publisher.go:764-817/:852-945/:1674-1749/:1769-1818`）：
  - **held 集合是连接级累积的**：`recordConnWindowTurnsLocked`（`event_publisher.go:1674-1687`）向 `projectionHeldTurns[conn][key][id]` 逐个添加、不替换；每次 window page 完成投递时在 fence 完成事务内注册该 page 的 turns（`go-bridge/projection_snapshot_fence.go:214-216`）。清除仅发生在投递失败/超时（`ClearConnWindowTurns`，`projection_snapshot_fence.go:288/297`，另 :247-249 queue 溢出路径同属投递失败类）与 sync_invalidate 路径；**older page 请求不清除既有 held 集合**。该累积语义使 held 判定（decide=true 链，见下）在 older page 期间仍能命中 active turn；
  - **`decide` 路由维度（v5 补，F-20）：fence.pending 区分 decide=true 与 decide=false，held 判定与清空只作用于前者**。fence 完成事务先过 decide 门（`projection_snapshot_fence.go:224` `if !frame.decide { continue }`）：decide=false 的 frame 在完成事务的投递循环中原样投递、不判 held、零清空（`:269-276`）；decide=true 的 frame 才进入 held 交集判定（:227），不命中的 window 连接被清空 patch 内容（`resolved.UpsertTurns/PartOps/TurnStateOps/Execution = nil`，:230-235）。两条入队路径：**常规 live patch**（`PublishProjectionPatch` → `deliverProjectionPatchLocked` 广播链 `event_publisher.go:901` / `deliverSingleConnPatchLocked` 单连接链 :826）排进 fence.pending 时 decide=false；**rule-4 detail commit**（`deferProjectionPatchForFenceLocked`，`event_publisher.go:771-776` → `appendFencePatchLocked(..., true)`，唯一调用链 = `PublishProjectionDetail` rule-4 分支 :1802-1811，即 turn_detail_lazy/chunks/session_turn_items 家族）decide=true；
  - **常规 live patch 的 fence 期间投递与 held 无关（v5 修正 v4 归因）**：v4 原文「针对 active turn 的 live patch 仍满足 held 交集判定 → 不清 `Execution` → 照常投递」归因错位——常规链 frame decide=false，fence 完成后**原样投递**，其「照常投递」来自 decide 路由而非 held 交集；fence 之外的常规广播（`deliverProjectionPatchLocked`，`event_publisher.go:852-945`）同样对所有 syncV2 连接完整投递、零清空。因此 older page 期间任何常规 live patch（含 execution 更新）对 window 连接照常到达，无需 UI 层 badge 补丁；
  - **Execution-only patch 的现可达性（v5 改写，F-20；v4 所述「真实边界」在当前代码不可达）**：会被 held 判定清空的只有 decide=true 链，而该链的现有构造点全部经 `CommitTurnStateOps/V2`（含 TurnID 的 TurnStateOp：`turn_detail_kernel_commit.go:52-62`、`turn_detail_batch_engine.go:239-252` 等；`PublishProjectionDetail` 全部 9 个调用点同族），`patchTurnIDs`（`event_publisher.go:1726-1749`）恒非空——**现有代码不存在 Execution-only 的 decide=true patch**。Claude 链路真实存在的 Execution-only patch 是外部执行态信号（`claude_stub_status.go:145-157` `syncProjectionExecution` → `ApplyExternalExecutionState`（`projection_reducer.go:539-565` 只改 Execution、SyncRev++、零 turn op）→ `PublishProjectionPatch`），**走 decide=false 常规链，不会被 fence 清空**。即 v4 冻结表述「纯 Execution 变化的 patch 在 fence pending 中对任何 window 连接都被清空」描述的事件在当前代码中不可达；该 patch 的安全性来自**发布链（decide 路由）**而非「execution 变化通常伴随 turn ops」的伴随性（v4 该句已删——伴随性不是安全判据）；
  - **边界处置（OD-6 定案条件 2；v5 重定位为防御性分支，设计不变）**：fence 完成事务增加 scalar 分支——协商了 `projection_window_scalar_v1` 的连接跳过 `resolved.Execution = nil`（`UpsertTurns/PartOps/TurnStateOps` 的 per-turn 清空规则保持不变）。理由：会话级标量不属于 per-turn 数据，per-turn 清除规则不应作用其上（语义正确性）；该分支防御的是**未来新增 detail commit 构造点产生 Execution-only commit**（decide=true 链的新可达形态）——现有构造点虽全部含 TurnID，但该性质无机制约束，guard 范围写宽无害且成本为一个按连接 capability 的条件判断。备选路线（以定向测试证明链路不存在 Execution-only patch 并登记为已知行为）**不采用**：无法防御未来新增构造点；scalar 分支落地时仍附带定向测试（§7.1.4）作为回归护栏；
- 代价与**协议再冻结（re-freeze）前置（v6 补，F-21——更正 v3–v5「标准 additive」的不完整定性）**：`scalarState` 不改变既有字段的语义，但它改变 `BridgeGetSessionProjectionWindowResult` 的**字段集合**，而该集合被以下三处硬断言与协议纪律锁定：
  1. Mac `go-bridge/projection_window_spec_test.go:46-57`（`frozenProjectionWindowFields` 冻结字段集常量）+ `:198-211`（`TestProjectionWindowSchemaTypesMatchFrozenFields`：读 `docs/protocol/schema/bridge-v1.types.ts` 后 `reflect.DeepEqual` 精确比对——该测试的存在目的即「catches accidental edits … without a re-freeze」）；
  2. 同文件 `:73-75`（对冻结样本 `docs/protocol/samples/projection-window-v1/window-response-spec.json` 的 response keys 做**精确 key 集合**断言）；
  3. iOS `OpenCodeiOS/OpenCodeiOSTests/ProjectionWindowSpecTests.swift:48`（fixture **response keys** 集合相等断言——v7 更正：v6 误引 `:51`，该行断言的是 window 描述符 key，`scalarState` 落在 Result 层不触碰它）+ `:129-130`（`frozenInterfaceFields` 同一冻结集）。

  且 `docs/protocol/bridge-v1.md:2328-2332`（R10 — Freeze scope）明写：**「Open product semantics discovered later are BLOCKERS to be re-frozen here first; implementing agents MUST NOT guess them in code」**——scalar state 交付正是 later-discovered product semantics，属 re-freeze 范畴而非普通 additive 字段。**处置：re-freeze 与 W1 同批交付（Gate A / S-1 前置，与 OD-6 定案同级）**，批次内容六项（v8 由五项扩为六项，F-27）：① `docs/protocol/bridge-v1.md` §Projection Window 新增 scalar 子章节，冻结 `scalarState` 形状（字段、必选/可选、与 `SessionProjection` scalar 字段的镜像关系、nil 语义、`projection_window_scalar_v1` 协商规则）；② `docs/protocol/schema/bridge-v1.types.ts` 更新；③ 冻结样本 `window-response-spec.json` 新增含 `scalarState` 的样本条目（沿既有 `synthetic-spec-fixture` provenance 纪律，首个真实 wire capture 后按 README 纪律替换，衔接 E-4）；**该样本是两仓独立副本（v7 补，F-24）**——Mac `docs/protocol/samples/projection-window-v1/window-response-spec.json` 与 iOS 仓同路径副本（实测同哈希 `6853e4ac…`，iOS 测试经 `fixtureRoot` 读本仓副本，`ProjectionWindowSpecTests.swift:34/:43/:85`）——两份必须同步更新并保持字节一致；④ Mac spec 测试冻结常量/断言更新为含 `scalarState` 的新冻结集（`:46-57`、`:73-75`、`:198-211`）；⑤ iOS spec 测试同步：response 级 key 集合断言 `ProjectionWindowSpecTests.swift:48` 与 schema 冻结集 `:129-130`；`:51`（window 描述符 key 断言）与 Mac `:77-80`（描述符级 DeepEqual）**不受 `scalarState` 影响、无需改动**（v7 更正 v6 误引）；⑥ **`schemaRevision` 纪律（v8 补，F-27）**：按协议 pack 惯例 bump `BridgeProtocolSchemaRevision`（现停在 `2026-09-05`，`go-bridge/bridge_v1_schema.go:77`）并在该文件注释块累积本次 additive 变更记录（v9 更正计数，F-28：既有注释块为 **13 条日期条目 / 8 个不同日期**（`2026-07-24` / `07-29` / `08-01` / `08-02` / `08-22` / `08-23` / `09-05` / `09-06`），同日多次变更按 `（同日二次返工）` / `（同日三次返工）` 标注，故**条目数 ≠ 日期数**；实测口径 `grep -c "^	// 20[0-9][0-9]-" go-bridge/bridge_v1_schema.go` = 13。v8 曾记「既有六条」为计数偏差。追加动作与既有条数无关：按 `<日期>: <变更摘要>` 追加一条即可）；**边界声明**：README `docs/protocol/README.md:15` 记 `2026-10-06-r1` 与代码常量的不一致是**既有已知分歧**（`git log -S` 证明代码从未取过该值；本方案之前已存在；字段纯信息性，`server.go:716`/`hello_handler.go:188` 只填充不比较、不参与门控）——**不得在 S-1 变更边界内顺带「修正」README**，登记后独立处置，防止无关分歧卷入本批次变更。**更新冻结物、不删除断言**——schema 漂移防护对新冻结形状继续生效；
- 对 R-2 影响：零额外往返。

**W2：window 页到达后由 iOS 追加一次轻量 scalar 拉取**

- 机制：不动 window 响应；新增轻量 RPC（如 `get_session_scalar_state`），iOS 在 `window_0` 到达后追加调用；
- 代价：**多一次往返**，直接挤压 R-2 的 Direct ≤3s 预算（尤其叠加 `projection.hydrating` 重试的冷 Mac 场景）；仍需新增 RPC 与协议冻结（protocol pack 增量与 W1 相当）；需定义 page 与 scalar 两次响应之间 patch 到达的排序/syncRev 对齐规则，竞态面更大；
- 优点：window 响应形状不变。

**W3：scalar state 交由 iOS `runtimeStateStore` 单源承担**

- 机制：不动 wire；执行态/权限锁显示改由 runtimeStateStore（live 事件源）承担，R-6 相应改写；
- 结构性缺陷：runtimeStateStore 是 live 事件消费者，**冷开一个「已在运行」的 session 时没有历史事件可学**——恰好覆盖不了本案主场景（打开运行中的超长 session）；且固化执行态双源（badge vs projection.execution），与 SSV2「projection 是真相」的语义冲突，R3 已亲核现状双源不一致的实例；
- 除非评审给出本方案未见的依据，列为不推荐（v4 状态：R4 定案已淘汰，维持本判断）。

**对比与定案（v4 更新：R4 已定案 W1；表中判定经 R4 亲核维持，无夸大）**：

| 维度 | W1 additive 携带 | W2 追加拉取 | W3 runtimeStateStore |
| --- | --- | --- | --- |
| 额外往返 | 0 | 1 次/冷开 | 0（但冷开拿不到值） |
| 协议增量 | 可选字段 + additive capability | 新 RPC | 无（语义改写） |
| R-2 首屏影响 | 无 | 有（叠加冷 Mac 重试更差） | 违反 R-6 主场景 |
| 单一真相链路 | 保持（scalar 随权威 page 经 replica） | 保持（两次权威响应） | 破坏（执行态脱离 projection） |
| 冷开 running session 正确性 | 正确 | 正确 | **不正确** |

**验收要求（OD-6 已定案 W1，以下为硬性验收，v4 措辞更新）**：running session 经 window 冷开后 `isSessionExecuting == true`；`requires_action` 时输入框禁用、权限卡正常锁发送；scalar 交付不得依赖 full projection 兜底；preview cache 同存 scalar（§3.4.1）。W1 定案路线的实施细节（additive capability + `scalarState` 字段 + fence scalar 分支）见上文与 §7.1.4/§7.2.10-11。

## 4. 决策表

| ID | 问题 | 选项与推荐 | 状态 | 影响切片 |
| --- | --- | --- | --- | --- |
| OD-1 | cache 命中后网络未完成时是否显示旧内容 | A. 只显示 loading；B. 显示 last-good + syncing watermark（推荐）；C. 显示旧内容且无提示 | pending，需 owner 裁决 | S-4 |
| OD-2 | 首屏窗口大小 | A. v1 默认 100 turns / 4MiB；B. iOS 请求 25 turns / 4MiB（推荐 Phase A）；C. v2 summary ≤1MiB（推荐 Phase B） | pending，需 owner 裁决 | S-3；S-1 中 iOS 请求 `limit` 数值部分（S-1 功能恢复本体不依赖，见 Gate A） |
| OD-3 | preview cache 是否设 TTL | A. 无 TTL，按 identity/schema/容量淘汰（推荐）；B. 时间 TTL | pending，需 owner 裁决 | S-4 |
| OD-4 | 15s hard cap | A. 保留（推荐）；B. 提高；C. 降低 | engineering decision：保留，等 bounded path 达标后再评估 | S-1、S-6 |
| OD-5 | 旧大 JSON checkpoint 是否迁移 | A. 不迁移，首次诚实重建（推荐）；B. exact-match 迁移 | engineering decision：不迁移，derived data 可重建 | S-2 |
| **OD-6**（v3 新增，v4 回填定案，v5 记录条件达成，v6 补 re-freeze 交付条件） | **window 路径 scalar state 交付路线（F-11）** | W1 window 响应 additive 携带；W2 window 后追加 scalar 拉取；W3 runtimeStateStore 单源 | **已定案 W1（2026-10-17，R4 评审轮按 owner 委托定案；R5/R6 复核维持）**。附两个定案条件：① §3.7 W1 fence 语义按代码真实行为重写——v4 按 F-17 达成部分，v5 按 F-20 补 decide 路由维度后完整达成（R5 判定 v4 遗漏该维度；R6 核验闭合）；② Execution-only patch 边界显式处置 = fence scalar 分支（协商 `projection_window_scalar_v1` 的连接不清空 `resolved.Execution`，per-turn 清空规则不变；v5 重定位为**防御未来新增 detail commit 构造点**的防御性分支——现有代码该形态不可达，设计不变），备选「测试排除 + 登记已知行为」路线不采用（理由 §3.7）。**v6 补（F-21，R6 附带要求）：W1 交付批次必须含 R10 re-freeze（协议章节/schema/冻结样本/两侧 spec 冻结断言更新，见 §3.7 代价行），否则过不了既有测试门**。实施细节随本行冻结 | S-1（范围扩展）、S-4、S-6 |

OD-1 / OD-2 / OD-3 影响用户可见语义，实施前需 owner 决策；OD-6 已按 owner 委托由 R4 评审轮定案（W1，附条件已回填冻结）；其余为工程决策，评审通过后按方案执行。

## 5. 证据与前置门

| ID | 命题 | 证据 / 状态 | 未满足时阻塞 |
| --- | --- | --- | --- |
| E-1 | 事故是 iOS timeout 而非 Mac hydrate failed | verified：Mac 17:30:56 hydrate/enqueue，截图 17:31:18 timeout；log 无 `projection.hydrate_failed` | 已满足 |
| E-2 | 事故实际未走 window | verified：事故窗口只有 `get_session_projection`，无 window RPC | 阻塞 S-1 完成宣告 |
| E-3 | window 未走的具体原因 | pending：需同 builds 的 hello capabilities、iOS `supportsProjectionWindow`、安装包身份、**backend descriptor capabilities 是否含 `projection_window_v1`（v3 补，F-13——已知实现为 hello echo 单门，取证须覆盖 descriptor 维度以完整排除）** | S-1 修复前必须闭合 |
| E-4 | 现有 v1 window 对样本可降到 4MiB 内 | component-level verified：本地 checkpoint 尾部 17 turns 约 3.79MB（口径见 §1.1）；非 wire capture | S-1 验收仍需真实 wire |
| E-5 | iOS 阶段耗时分布 | pending：需 receive / gunzip / decode / apply / presentation / UI commit signposts | S-5 完成宣告 |
| E-6 | 新 materialization store 正确性 | pending：实施后以合成 100MB 级 fixture + 本地 realdata tag 测试 | S-2 |
| E-7 | preview cache 不污染权威 writer | pending：实施后定向测试 | S-4 |
| E-8 | Relay gzip 边界 | verified：协议与源码；本次 Direct 不适用 | 不阻塞 S-1/S-2 |
| E-9 | Codex 参考机制 | verified：Codex source commit 已记录；只作为架构参考，不是 CordCode 运行证据 | 不阻塞 |
| **E-10**（v3 新增） | window 冷开 scalar state 正确性（F-11） | pending：按 W1 定案路线（含 fence scalar 分支，§3.7）实施后，定向测试（§7.1.4/§7.2）+ 真机场景 2（§7.4）验证 running session 冷开不显示空闲、`requires_action` 锁输入框、不依赖 full 兜底 | **S-1 完成宣告**（与 E-3/E-4 并列） |

Gate A：

- S-1 开始实施只需 E-1/E-2 与现有源码锚点 + OD-6 定案（已定案 W1，R4 定案、R5/R6 复核维持，v4 回填、v5 补 F-20 后冻结内容与代码行为一致）+ F-20 修订完成（v5 已完成，R5 要求作为开工前置）+ **F-21 re-freeze 完成（v6 登记，R6 要求与 W1 同批交付：协议章节 + schema + 冻结样本 + Mac/iOS 两侧 spec 冻结断言更新，更新冻结物而非删除断言，见 §3.7 代价行）**；修复完成宣告还需 E-3/E-4/**E-10** 真实运行证据。
- OD-2 不阻塞 S-1 开工（v2 补，F-5）：S-1 按现有默认 limit（v1 default 100 turns / 4MiB）实施并验收；OD-2 裁决改变的只是 iOS 请求 `limit` 数值（25），属 S-1 的参数化后续与 S-3 的前置。
- S-2 开始前需**存储选型定案 + 存储格式（bucket/key 布局）冻结与不变量测试清单**；选型已由 §3.2.2 定案（bbolt + iOS 文件型，评审可推翻）；完成需 E-6。
- S-4 开始前需 OD-1/OD-3 决策；完成需 E-7。

Gate B：

- S-3 的 v2 协议冻结前需 OD-2 和 summary UI 语义确认。
- 所有真实设备 UI 验收需 owner 执行；agent 不自动运行 UI automation。

## 6. 实施切片

| 切片 | 可见结果 | 组件与复用 | 前置条件 | 最小验证 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-0：运行身份与阶段指标 | 一次打开能对齐 iOS build、Mac runtime commit、hello echo、window/full 选择和各阶段耗时；开工前按 P0 来源门重生成来源清单（v3 补，F-10） | Mac hello/log；iOS transport/projection trace；management API | 无 | 定向日志测试；对当前样本采集一次 | 只保留诊断，不改变行为 |
| S-1：恢复 v1 window 实际路径（含 scalar state，v3 扩） | 长 Claude 首开发出 `get_session_projection_window`，payload 受 R-1 规则约束，不再 full snapshot；**运行中 session 冷开执行态/权限锁不丢（§3.7 W1 定案路线，含 fence scalar 分支）** | 复用 `projection_window_v1` / iOS `pullOpenWindowed` + §3.7 W1 定案路线（含 **R10 re-freeze 批次**（协议章节/schema/冻结样本两仓副本/两侧 spec 冻结断言/`schemaRevision` bump，§3.7 代价行——F-21/F-24/F-27）、iOS mirror、eligibility 双维度 gate（§3.7——F-22）、**scalar 协商复用 `negotiateProjectionWindowV1` 扩展（direct+relay 双路径自动覆盖，§3.7——F-26）**、fence scalar 分支，**window 路径 `projection_rpc` trace（v7 补，F-25——与 full 同构：requestId/backend/sessionID/outcome；full 侧锚点 `handlers_projection.go:89/:175`，window handler 现无该 trace，缺它则 §7.3 重试指标不可观测）**） | E-1/E-2 + OD-6 定案（已定案 W1，见 Gate A）+ **F-21 re-freeze 批次**；完成需 E-3/E-4/**E-10** | Mac handler 测试；iOS negotiation 测试；**scalar state 定向测试（§7.1.4/§7.2.10-11）**；真实 Direct/Relay 样本 | 若协商仍失败，停止后续性能结论，先修安装 / 能力身份 |
| S-2：Mac materialization store | runtime 重启或 transcript append 后只处理 suffix；不再读写 34MB JSON checkpoint | **bbolt store（§3.2.2 选型定案）**；ProjectionKernel 单 writer | **存储格式冻结 + 测试计划**（选型已定案） | append / restart / compact lineage / truncation / live patch race 定向测试；**window 强制冷检组合路径（§3.2.3）IO 上界测试** | store invalid 时诚实 full rebuild；旧 checkpoint 保留不删除 |
| S-3：`projection_window_v2` summary page | 首屏 turn summary ≤1MiB，过程明细按需加载 | v1 producer / turn_detail_lazy 语义借鉴 Codex items list | OD-2；协议冻结 | schema mirror、cursor、active turn、item detail、typed error 测试 | v2 未协商时走 v1，不造第二 writer |
| S-4：iOS preview cache | 打开先显示 last-good + syncing；权威 page 到达后替换 | **文件型 preview store（每 session 单文件 + 原子写，含 scalar state，§3.4.1）**；显示层独立于 replica | OD-1/OD-3 | cache hit/miss、identity mismatch、网络失败、epoch change（预期：重启后 epoch 不匹配 → key 不变不 miss，显示 last-good + 升级 stale 标记，权威 page 到达后替换并写入新 epoch；bridgeId 变化 → 整体 miss）、切换回 same session 测试 | cache invalid 只回到 loading，不显示假成功 |
| S-5：单次解析与分段 apply | 移除多轮全量 JSON parse/re-encode，长 page CPU / 内存峰值下降 | transport route scanner + typed result decoder | S-1 | decoder benchmark、actor cancellation、trace correctness 测试 | decode 失败显式 transport/projection error |
| S-6：真实样本验收 | 约 97MiB Claude 样本 Direct/Relay 达标，无 15s timeout，**单次打开重试次数 = 0（§7.3，v3 补）** | owner 真机手动矩阵 | S-1 最少；完整目标需 S-2/S-4/S-5 | 记录 Mac/iOS 阶段指标、**每次打开的重试计数**与截图 / 日志 | 未达标保留失败现场，禁止提高 timeout 收口 |

S-1 是架构功能恢复，不是补丁：`projection_window_v1` 本来就是 S4B/S4C/S4D 的正式设计，当前事故证明它没有在实际运行路径生效。**（v3 补，F-11）同时 S-1 是一次首开内容语义变更：必须携带 §3.7 W1 定案路线的 scalar state 交付，否则只是把 34MB 问题换成状态丢失问题。**

**（v4 补，F-18）capability 双门偏离收口实施项（归属 S-3，协议冻结时执行）**：F-13 登记的实现与协议文档偏离（hello echo 单门实现 vs `docs/protocol/bridge-v1.md:2173-2175` 双门规范，见 §2.2）不再无限期悬置，在 S-3 v2 协议冻结时二选一收口，由协议冻结评审定案方向：

- (a) Mac 侧把 `projection_window_v1`（及 W1 的 `projection_window_scalar_v1`）写入 backend descriptor 的 `capabilities` 列表，并按文档条件（descriptor 支持 + 全局开关均满足才 echo）对齐实现；iOS 消费端同步按 descriptor 门控；或
- (b) 修订 `docs/protocol/bridge-v1.md` 该条款为单门语义（实现即规范，删去 descriptor 门控要求）。

在收口前，S-1 / W1 的 scalar capability 沿用现状单门模式（与 v1 一致）；当前唯一已知消费端为本仓 iOS 客户端（只查 echo），单门模式不产生已知消费端的静默失效面，但任何新客户端接入前必须先完成本收口。

## 7. 验证计划

### 7.1 Mac 定向测试

1. `projection_window_v1`：
   - capability echo 后 `window_0` 必达；
   - 4MiB / 256 turns 上限；
   - 单个超大 turn 单独成页（payload 可超 4MiB，与 R-1 口径一致）；
   - cursor stale / scope mismatch；
   - active turn 尾锚；
   - 多设备并发（v2 补，F-8）：iPhone + iPad 同时打开同一长 session，各自 window 请求返回一致的 committed head page、互不干扰（每 conn 独立 subscribe + snapshot fence，`go-bridge/projection_window_handler.go:93-99`）。
2. materialization：
   - append suffix only；
   - runtime restart；
   - composite Claude continuation membership；
   - prefix digest mismatch；
   - truncation / cursor 回退；
   - live patch 与 window fence 并发；
   - store schema version；
   - **window 强制冷检组合路径（v3 补，F-14）**：append 后首次 `window_0` 的 IO 上界 = 冷检（stat + 尾部 4KiB 扫描）+ suffix 折叠，断言不整读 transcript。
3. metrics：
   - source cursor delta；
   - materialized rows / bytes；
   - page encoded bytes；
   - kernel head syncRev；
   - response queue time。
4. **window scalar state（v3 补，F-11；v4 按 W1 定案路线改写，F-17；v5 补断言构造前提与双链断言，F-20）**：
   - scalar capability echo 后 `window_0` 响应含 `scalarState`，running session 的 execution 与 kernel head 一致；
   - **冻结断言更新（v6 补，F-21）**：re-freeze 批次内把 `projection_window_spec_test.go` 的 `frozenProjectionWindowFields`（`:46-57`）、fixture key 断言（`:73-75`）与 schema types 断言（`:198-211`）更新为含 `scalarState` 的新冻结集——**更新冻结物而非删除断言**，更新后断言对未再冻结的后续漂移继续失败（防护不减）；`window-response-spec.json` 新增含 `scalarState` 的样本条目；
   - 未协商 scalar capability 的连接收不到该字段（additive 不影响旧 peer）；
   - **双链投递断言（v5 改写，F-20——两条链分别断言，防止走错链的「假通过」）**：① 常规 live patch（`PublishProjectionPatch` 链，**含 Execution-only 形态**——可用 Claude 外部执行态信号路径构造：`claude_stub_status.go:145-157` → `PublishProjectionPatch`）在快照 fence 期间对 window 连接以 decide=false 排队，fence 完成后**原样投递**（不判 held、零清空），older page 期间同样到达（对应 §7.4 场景 8）；② detail commit（`PublishProjectionDetail` rule-4 链，decide=true）在 held 交集命中时（连接持有该 patch 的 turn）不清空、原样投递——held 累积语义使 `window_0` 投递过 active turn 后 older page 期间仍能命中；
   - **fence scalar 分支断言（v4 新增，F-17；v5 补构造前提，F-20）**：Execution-only patch 须以 **decide=true** 进入 fence.pending 才会触及清空分支——测试须经 `PublishProjectionDetail` 触发（或直接构造 decide=true pending frame 进入 fence 完成事务），并构造该 commit 为纯 Execution 变化（无 TurnStateOps，未来新增构造点的形态）。断言：协商 `projection_window_scalar_v1` 的连接该 Execution 不被清空；负对照「未协商该 capability 的连接保持清空」**只在 decide=true 路径可观测**——若用常规链（decide=false）构造 Execution-only patch，该 patch 原样投递、清空现状观察不到，测试会「通过但未测到 scalar 分支」（无效）；
   - **回归护栏（v5 更新样本，F-20）**：① 以 `claude_stub_status.go` 路径为真实样本断言常规链 Execution-only patch 的 fence 期间投递（证明「发布链决定安全性」）；② grep 型护栏：记录 `PublishProjectionDetail` 调用点族（现 9 处）是否新增 Execution-only 构造形态——新增即证明 scalar 分支从防御性转为必要，触发 §8 风险表复核。
   - **双路径 echo 断言（v8 补，F-26）**：`negotiateProjectionWindowV1` 扩展后，direct（`server.go:808`）与 relay（`main.go:693`）两条 hello 链都必须断言 `projection_window_scalar_v1` echo 生效——该函数被两条路径各自独立调用、relay 覆盖完全依赖函数复用；若实施改为独立协商函数，断言必须覆盖两处调用点，防止 relay 遗漏导致 Relay 路径静默永久回 full（§7.4 场景 5 兜底暴露）；

### 7.2 iOS 定向测试

1. hello capability echo 与 `supportsProjectionWindow`；**（v6 补，F-22/F-21；v7 更正行号，F-24）eligibility 双维度：scalar echo 缺失时 `pullOpenWindowed` 回 full 的定向断言（`isProjectionWindowEligible` 扩展后，锚点 `ProjectionStore.swift:595-599`）；`ProjectionWindowSpecTests.swift` 冻结断言随 re-freeze 批次更新为含 `scalarState`（response 级 `:48` + schema 冻结集 `:129-130`；描述符级 `:51` 不受影响）（更新冻结物而非删除断言）**；
2. `pullOpenWindowed` 不因非 typed error 静默 full；
3. preview cache schema / identity / 容量；
4. stale preview 状态只能被权威结果清除；
5. replica 唯一 writer seal；
6. decoder 输出与旧 decoder 深度相等；
7. trace 不再构造完整 payload 对象；
8. 冷 Mac 首个 `window_0` 收到 `projection.hydrating`（retryable=true，`go-bridge/projection_window_handler.go:124-126`）时的重试/退避行为：有界重试 bounded page，不回落 full、不无限重试（v2 补，F-8）；
9. epoch 变化（Mac runtime 重启）不淘汰 preview key、只升级 stale 标记；bridgeId 变化才整体 miss（v2 补，F-1）；
10. **scalar state（v3 补，F-11，路线无关）**：running session 经 window 冷开后 `isSessionExecuting == true`（消费链 `ChatViewModel.swift:2419-2426`）；`requires_action` 时输入框禁用、权限卡锁发送；scalar 交付不依赖 full projection 兜底；
11. **preview scalar（v3 补）**：cache 命中渲染 preview 时执行态标记与 metadata 一致；权威 page 到达后 scalar 随 page 一同替换。

### 7.3 性能基线

固定样本：

- 合成 fixture：≥100MB JSONL、≥120 turns、含超大 tool output / diff / reasoning；
- 本地 realdata 测试：owner 事故 transcript，测试代码不提交内容；
- 设备：iPhone 16 Pro；
- 路径：Direct LAN 与 Relay。

**字节类指标一律按「UTF-8 编码字节、Go `json.Marshal` 输出（紧凑分隔符 + HTML 转义）」计量（v3 补，F-15；口径声明与漂移幅度见 §1.1）；重试类指标按单次打开计数（v3 补，F-16）。**

记录：

| 指标 | 目标 |
| --- | --- |
| Mac source advance | append 只处理 suffix，不重读约 97MiB transcript |
| Mac restart 首开（segment 快速匹配命中，§3.2.3） | 只读 head/segments 行，不整读约 97MiB prefix；全量 prefix digest 重算仅发生在快速匹配失败时（v2 补，F-3） |
| **window_0 冷源检查 IO（v3 补，F-14）** | 每次请求触发（无 `sinceRev` 门）：每 segment `os.Stat` + 尾部 4KiB 反扫；断言不整读 transcript；append 后 catch-up = 冷检 + suffix 折叠 |
| window encoded bytes | v1 多 turn 累积 ≤4MiB；单个超限 turn 整 turn 单独成页、可超 4MiB（与 R-1 口径一致）；v2 summary ≤1MiB |
| iOS wire receive → decode | 可量化，无多次 full object graph |
| cache paint | ≤500ms（cache hit，目标，非当前承诺） |
| authoritative first page（Mac warm：checkpoint / materialization 命中） | Direct ≤3s；Relay ≤5s |
| authoritative first page（Mac 冷全量 hydrate，checkpoint miss） | 第一阶段（S-0/S-1/S-5）不并入 ≤3s/≤5s 达标判定：预算 = Mac 冷 hydrate（本次实测约 1.794s）+ bounded page 传输 / decode / apply + 可能一次 `projection.hydrating` 重试（F-8），验收记录实测值；第二阶段（S-2 后重启不再全量重建）冷场景并入达标判定（v2 补，F-4） |
| 15s timeout | 0 次 |
| full projection open | 0 次（new peer / window available） |
| **单次打开 full-snapshot / window_0 重试次数（v3 补，F-16；v7 补可观测前提与计数口径，F-25）** | **0 次**（事故实测 8 次全量投递 / 用户可见重开至少 4 次；「最终成功但有 2–3 次重试」不判通过）。**可观测前提（v7）**：window handler 现无 `projection_rpc` trace（`grep logProjectionRPCTrace projection_window_handler.go` = 0），window 成功仅经 fence 日志（`projection_snapshot_fence.go:127/:239`，字段为 sessionPrefix/cutRev/generation 与 sessionPrefix/pending/invalidated/expectedRev——均无 requestId 且 full/window 共用）——**S-1 交付必须包含 window 路径与 full 同构的 `projection_rpc` trace**（requestId/backend/sessionID/outcome；full 侧锚点 `handlers_projection.go:89/:175`），否则本指标在 window 生效后不可计数。**计数口径（v7）**：按「同一 session 单次打开内的 RPC 发起序列」计数（Mac trace 与 iOS transport trace 对账）；capability 不满足产生的**合法回退不计入重试**（双维度 gate 判定后直接 full，或 window 收到 typed `protocol.capability_required` 后回 full——各 ≤1 次，单独归类）；`projection.hydrating` / 网络失败后的重复发起计入重试 |

### 7.4 真机 owner 矩阵

每条场景执行前用 S-0 指标记录 Mac warm / cold 状态（checkpoint / materialization 是否命中），结果按 §7.3 分行口径对照（v2 补，F-4）；**每次打开记录重试计数（v3 补，F-16）**。

1. 冷 App + idle 长 Claude session，Direct；
2. 冷 App + running Claude session，Direct。**应看到（v3 补，F-11）：执行中状态与权限卡语义不丢——不显示「空闲」，`requires_action` 时输入框禁用（按 §3.7 W1 定案路线交付）**；
3. 切走再切回同一 session；
4. Mac runtime 重启后重开。应看到（v2 补，F-1）：若此前有该 session 的 preview，先显示 last-good + 升级的 stale / 正在同步标记（不退回纯 loading、不清 cache）；权威 page 到达后替换并清除标记。无 preview 时正常 loading；
5. Relay 弱网重开；
6. 断网时有 cache：显示 stale，不假成功；
7. 恢复网络后 authoritative page 替换 preview；
8. 滚动到顶触发 older page，视口不跳底。**（v4 改写，F-17）older page 期间针对 active turn 的 execution 更新应照常投递（held 集合连接级累积、older page 不清除，§3.7 W1 fence 语义）——核对 UI 上执行态 / 权限卡在浏览 older page 期间保持正确，不出现「翻页后执行态丢失/显示空闲」**。

## 8. 风险与回退

| 风险 | 触发信号 | 处理 |
| --- | --- | --- |
| 实际安装包仍未协商 window | S-0 无 echo / 无 window RPC | 停止性能优化，先修 build / capability identity |
| materialization 与 kernel head 不一致 | page revision / turn set mismatch | fail closed：invalidate store 并 full rebuild |
| preview 被误当权威 | UI 清错误或允许发送但 replica 未 ready | 保留独立 lifecycle；测试禁止 preview 写 replica |
| summary page 丢关键 UI 状态 | permission/question/active turn 显示异常 | v2 冻结这些字段为 always-visible，不合格不上 |
| **scalar 路线（W1 已定案）的 wire 兼容**（v3 补，F-11；v6 更新，F-22） | 任一 peer 组合出现「window 生效但 execution 恒 idle」（新 Mac + 旧 iOS 为已知既有缺口形态，见 §3.7 组合表，不在 S-1 范围） | iOS 双维度 gate fail-closed：未收到 scalar echo 即不走 window（回 full 路径）；§7.2.1 断言覆盖各组合 |
| **re-freeze 批次被绕过**（v6 补，F-21） | 实施者为过测试删改/删除冻结断言，或跳过协议文档/冻结样本更新直接改 schema | 禁止删除断言；re-freeze 五项内容（协议章节/schema/冻结样本/Mac spec/iOS spec）同一批次交付（§3.7 代价行）；S-1 交付评审检查该批次完整性 |
| **materialization 写入成为 live bottleneck（bbolt 大 value 写放大）**（v3 改，F-12） | patch flush 延迟上升 | 异步批量提交；超大 turn（如 6.35MB blob）的写放大由 Phase B item 拆行缓解；必要时按容量阈值后台重写紧凑化；page 读取必须等不低于 requested revision |
| **新依赖供应链**（v3 补，F-12） | bbolt 漏洞告警 | CI `govulncheck` 已覆盖根模块；升级走依赖评审 |
| Relay 分片 / gzip 引入重组问题 | chunk timeout / auth failure | 不改变 v1；新增传输测试后才能动 |

回退边界：

- `projection_window_v1` 回退 = 撤掉 enable / capability echo，返回 full path；数据不受影响。scalar additive capability（W1）回退 = 不 echo 该 capability，iOS 自动回 full 路径；不产生数据迁移。
- materialization 回退 = 停用 materialization store，继续旧 checkpoint；旧 checkpoint 不删除（新 store 文件独立，可直接弃用）。
- preview cache 回退 = 不读 cache，仅显示 loading；权威链路不变。
- v2 未冻结前不实施；v2 可整体不协商。

## 9. 完成定义

### 第一阶段（解决事故）

- S-0、S-1（含 §3.7 W1 定案路线的 scalar state 交付）、S-5 的必要部分完成；
- 事故样本实际走 `window_0`；
- 不再出现 15s timeout / 多次重试——**单次打开重试次数 = 0（§7.3，v3 补）**；
- **运行中 session 冷开不丢执行态 / 权限锁（E-10，v3 补）**；
- Mac 与 iOS 阶段耗时可对账。

### 第二阶段（架构收口）

- S-2 / S-3 / S-4 完成；
- Mac 不再以单个大 JSON checkpoint 作为长 session 派生状态；
- iOS 有明确 stale-while-revalidate 的持久 preview；
- summary / item detail 分离；
- 其它 backend 推广有独立方案和测试矩阵。

## 10. 评审重点

请评审优先检查：

1. S-1 是否应作为 P0 修复，而不是被视为补丁；
2. materialization store 放在 Mac 而不是 iOS 是否正确；
3. preview cache 不进入 `ProjectionReplica` 的边界是否足够强；
4. Phase A 保留 `turn_blob_json`、Phase B 拆 item rows 的迁移策略是否可接受；
5. v2 summary page 与既有 `turn_detail_lazy_v1` / chunks 的关系是否应重用或另立 RPC；
6. OD-1 / OD-2 / OD-3 的推荐是否满足产品语义；
7. 目标耗时和测试矩阵是否需要调整；
8. **（v3 提出，v4 状态更新，v5 复核请求更新，F-11 / OD-6 / F-20）§3.7 scalar state 交付路线已定案 = W1（R4 定案、R5 复核维持）**。请复核 v5 按 F-20 的修订质量：fence 语义的 decide 路由维度描述是否与 `projection_snapshot_fence.go:223-236` + `event_publisher.go:764-817/:852-945/:1769-1818` 实际行为逐点一致；「Execution-only 边界现不可达、scalar 分支定位为防御未来新增 detail commit 构造点」的表述是否准确；§7.1.4 双链断言与构造前提是否可写出确定性失败判据；
9. **（v3 提出，v4/v5 状态更新，F-12）§3.2.2 存储选型定案（Mac=bbolt、iOS=文件型、不引入 SQLite）已经 R4、R5 两轮复核维持**。如仍有异议可推翻，但需给出对构建链/依赖纪律更优的替代与理由；
10. **（v4 新增，F-18；v5 措辞精确化）capability 双门偏离的收口实施项（§6 表下独立段落登记，归属 S-3）是否方向正确、归属切片是否合理**；
11. **（v6 新增，F-21/F-22/F-23；v7 更正锚点）**：① §3.7 代价行的 R10 re-freeze 前置登记与五项批次内容是否完整、三处冻结断言锚点是否准确（`projection_window_spec_test.go:46-57/:73-75/:198-211`、`ProjectionWindowSpecTests.swift:48/:129-130`（v7 更正：response 级为 `:48`，描述符级 `:51` 不受影响）、`bridge-v1.md:2328-2332`）、冻结样本双副本同步义务是否完整；② §3.7 peer 组合行为表四格结论是否成立，「新 Mac + 旧 iOS = 既有缺口、由 iOS 升级解决、不在 S-1 范围」的定性与服务端 echo 绑定的否决理由是否可接受；③ §2.1 iOS 行回填是否与实测一致。（R6 已核验 F-20 闭合；R7 已核验 R6 三条闭合且判 0 阻塞——本项本轮抽核即可。）；
12. **（v7 新增，F-24/F-25）**：① 断言行号更正是否已全数落地（`§3.7 断言清单第 3 条 / 批次⑤ / §7.2.1 / 本项` 四处一致指向 `:48`，`:51` 与 Mac `:77-80` 标注为描述符级不受影响）、批次③双副本（同哈希 `6853e4ac…`）同步义务是否可执行；② §7.3 重试指标的可观测前提（window `projection_rpc` trace 交付项）与计数口径（合法回退 ≤1 次不计重试、hydrating/网络失败计入）是否成立、§6 S-1 组件列与 §7.3 是否一致。
13. **（v8 新增，F-26/F-27）**：① §3.7 W1 协商定案「在 `negotiateProjectionWindowV1` 内扩展（direct `server.go:808` + relay `main.go:693` 双路径自动覆盖）」是否正确、独立函数替代的约束（两处接线 + 双路径断言）是否完整、§7.1.4 双路径 echo 断言是否可构造；② re-freeze 批次第⑥项（`schemaRevision` bump + `bridge_v1_schema.go` 注释累积 + README `2026-10-06-r1` 既有分歧的边界声明）是否符合协议 pack 惯例、变更边界是否清晰。（R7 两条经 R8 核验闭合、R6 三条经 R7 核验闭合且 R8 复认——历项抽核即可。）
14. **（v9 新增，F-28）**：① §3.7 批次⑥ 的注释条目计数是否已与实测一致（`grep -c "^	// 20[0-9][0-9]-" go-bridge/bridge_v1_schema.go` = 13 条 / 8 个不同日期）、「条目数 ≠ 日期数」的说明是否准确、是否明确「追加动作与既有条数无关」；② 本轮是否确未改动任何设计内容（W1 定案、fence scalar 分支、re-freeze 六项结构、peer 组合表、存储选型、切片与门结构均应与 v8 一致）。（R9 verdict = APPROVED；本版仅落地唯一建议 F-28，历项抽核即可。）

---

设计师交接块：

```text
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_version: v9（2026-10-17，按评审报告 R9 修订；R9 verdict = APPROVED（0 阻塞 + 1 建议），本版仅落地唯一建议 F-28（批次⑥ 注释条目计数更正为 13 条 / 8 个日期），未改动任何设计内容）
plan_sha256: b2382ec6f830675f88c1f2f3199aa9124f1e22ca4e436e55e6ec7f14226af4fe  # 正文（不含本交接块，切分 = raw[:idx].rstrip(\n)，idx = "\n---\n\n设计师交接块"）
scope: full
review_status: APPROVED（R9）
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, F-21 re-freeze 批次(S-1 前置), S-6 owner 真机手动矩阵授权
brief_attached: true
```
