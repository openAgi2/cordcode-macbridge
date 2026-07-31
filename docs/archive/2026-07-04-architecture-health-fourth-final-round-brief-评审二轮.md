# 架构健康第四轮（最终轮）开发简报 二轮评审报告

日期：2026-07-04
评审对象：[docs/2026-07-04-architecture-health-fourth-final-round-development-brief.md](../2026-07-04-architecture-health-fourth-final-round-development-brief.md)（v2，已按一轮评审 H1–H9 修订）
一轮评审：[docs/2026-07-04-architecture-health-fourth-final-round-brief-评审.md](2026-07-04-architecture-health-fourth-final-round-brief-评审.md)
评审重点：H1–H9 是否真正落入操作性章节（而非仅在 §10 处理记录挂牌）、软化后 H2 全文一致性、改动是否引入新矛盾。

---

## 0. 一句话结论

**通过，可作施工输入。** 9 条 H 项全部真正写进了操作性章节（Phase A/E、完成定义、验收标准、禁止形态、§3/§4 设计正文），不是只在 §10 挂牌——这是 v2 与" cosmetic 修订"的关键区别。一轮列的 4 个 P0 硬伤（H1/H2/H3/H4）全部消化，其中 H3/H4 的多段落贯穿质量高于预期。仅剩 3 处低危精修项，不阻断开工，建议在 Phase A 启动前顺手改掉，或在完成报告里显式交代。

---

## 1. H1–H9 落地核验

核验方法：对每条 H，除了看 §10 自述，回查正文相应章节是否真的改了。判定分三档：**贯穿**（多章节联动改写）、**单点**（至少一处正文改了）、**挂牌**（仅 §10 提及，正文未动）。

| ID | §10 自述 | 正文实证 | 落地质量 |
|---|---|---|---|
| **H1** 已修 bug 改结构性硬化 | "第 0 节改为已修问题的结构性硬化，完成定义改为 policy 接管后回归仍通过" | §0 第 32–34 行完整重写为"`e018cb5f` 已单点修复 + owner 真机复测 + 因此第四轮是结构性硬化"，并点明"Claude-only guard 重构泛化"目的 | **贯穿**，但完成定义处有残留瑕疵（见 §2.1） |
| **H2** closed 软化 | 部分采纳，owner 已要求当最后一轮 | §0 定位、§1 末、§2 硬约束、Phase D 完成条件、§7 验收、§8 不做、§9 模板共 7 处统一改为"本次专项收口 + 未来新系统性 gap 可另立专项"；§0 第 18 行写明"owner 已明确要求"——治理授权闭环 | **贯穿**，治理越权解除（owner 本轮显式拍板） |
| **H3** CHANGELOG 修订 | Phase D 增加修订既有条目 | Phase D 第 436–437 行（MacBridge + iOS 双仓 CHANGELOG）、完成定义 #9、Phase D 完成条件、§7 验收标准共 4 处联动 | **贯穿**，质量最高 |
| **H4** ownership 原子读写 | §4.2 新增硬约束 + P1 交错测试 | §4.2 第 236–244 行 5 条 MainActor 同步域规则 + "硬约束"声明 + 07-04 race 释义；P1 第 314 行交错窗口用例；Phase B 完成条件第 395 行；§7 验收第 474 行 | **贯穿**，质量高 |
| **H5** Claude-only guard 退场 | 生产路径由 policy 接管 | §3.2 第 159 行（迁移后禁用 + 例外需解释 + 后续删除条件）；Phase B 完成条件第 397 行；§7 验收第 473 行 | **贯穿** |
| **H6** Codex/OpenCode parity 取证 | Phase A 增加调研 | Phase A 第 372 行（产出写入完成报告）；P2 第 320/328 行（允许直通 + 禁止假造风险）；Phase A 完成条件第 381 行 | **贯穿** |
| **H7** MessageSync 行数 1480 | §1.1 改为 1480 | 第 58 行已改 | **单点**（足够） |
| **H8** 复用既有 initialization guard | §4.3 / Phase A 盘点 | §4.3 第 257 行（policy 不得与既有 guard 并行判决）；Phase A 第 370 行盘点清单 | **贯穿** |
| **H9** simulator unit test ≠ UI automation | P4 增加说明 | 第 357 行明确区分 | **单点**（足够） |

**结论：9 条全部"贯穿"或"单点足够"，零挂牌。** 这是 v2 能放行的根本原因——开发 agent 按正文施工时会在 Phase A/B/C/D 与验收标准里真正撞上这些约束，而不是只在一轮评审表里看到它们。

特别肯定两点：
- **H2 治理越权彻底解除**。一轮最重的 P0 是"自我授权永久关闭"。v2 第 18 行写入"owner 已明确要求'把第四轮当成最后一轮'"，且 owner 在本轮对话里显式确认"保留你已拍板的'第四轮当最后一轮'"——授权链完整，治理合规。
- **H3/H4 多段落贯穿**。这两条最容易做成"挂一句就交差"，v2 却把 H3 拆到 MacBridge/iOS 双仓 CHANGELOG + 完成定义 + 验收，把 H4 拆到 §4.2 五条规则 + P1 交错用例 + Phase B 完成条件 + §7 验收四点联动。这是高完成度的修订。

---

## 2. 残留问题（均低危，不阻断开工）

### 2.1 完成定义 #3 与 #7 重叠，#3 是"已满足"条款（H1 收尾不彻底）

第 79 行 #3"2026-07-04 Claude 冷启动重复从头输出问题有回归测试覆盖"——这条**今天已为真**（`e018cb5f` 已加 `testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream` 与 `testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream`）。作为第四轮完成定义，它会让完成报告把既有成果算进第四轮功劳，正是 H1 想纠正的口径。

v2 的实际处理是保留 #3、新增 #7（"policy 接管后仍通过 + 并发交错用例"）。净效果对，但 §10 自述写的是"完成定义改为 policy 接管后回归仍通过"——与实际"增列 #7 而非改写 #3"略有出入。

**建议**：把 #3 折进 #7，或改写 #3 为"07-04 回归测试存在，并在 policy 接管后仍通过"，与 §10 自述一致。

### 2.2 完成定义 #10 比 §7 验收标准少了一句"未来可另立专项"（H2 局部不一致）

第 86 行 #10："完成报告明确宣布本次架构健康专项 closed，**不再提出第五轮**。"
第 480 行 §7 验收："……不再派生下一轮；**未来新系统性 gap 可另立专项**。"

完成定义是开发 agent 写完成报告的对照表。#10 缺了那半句 hedge，agent 按 #10 写出的报告就会是"closed，无第五轮"——恰恰是 H2 想软化的越权口径。

**建议**：把 #10 对齐 §7 措辞，补"未来新系统性 gap 可另立专项"。

### 2.3 parity 调研若发现 Codex/OpenCode 真有等价风险，Phase B/C 范围如何扩未写明（H6 边界）

Phase A 第 372 行要求 parity 调研产出结论；P2 第 328 行允许"无等价风险时 Codex/OpenCode 直通"。但反方向没写：**若调研发现 Codex 真有等价覆盖风险，Phase B/C 是否必须扩展到该 backend？** 现状下 Phase B 标题是"接入 loadMessages 与 local send ownership"（偏 Claude local send 语义），Codex 的等价路径未必天然落在同一组调用点。

这是低概率场景（既有 `CodexSeamTests` 若存在等价 bug 通常已暴露），但写明一句能避免"调研发现了但 Phase B/C 没接住"的悬空。

**建议**：Phase A 完成条件加一句"若 parity 调研发现 Codex/OpenCode 等价风险，Phase B/C 调用点收敛必须显式覆盖该 backend，不得只修 Claude；若无风险，完成报告按 P2 第 328 行诚实说明"。

---

## 3. 几处可选增强（非缺陷，仅减少施工解释成本）

- **§4.2 ownership 调用点骨架**。五条 MainActor 规则用散文写了"读 ownership → policy 判决 → 记 token → fetch → apply 前复核"流程，但没给 6 行级伪代码骨架。这条是 v2 最重要的硬约束，给个 `read → decide → token → fetch → recheck ownership/session/initID → apply` 的最小骨架能显著降低开发 agent 的解释漂移。可选。
- **P0 #6 与 P3 的 session-switch 测试层次关系**。P0 #6（纯 policy 返回 `.rejectStaleSession`）与 P3（端到端 switchSession + 迟到 load）测同一场景的不同层，合理但建议各注一句"前者纯函数层、后者集成层"，避免开发 agent 误以为重复。

---

## 4. 放行建议

**通过，建议直接进入 Phase A。** 一轮的 4 个 P0 硬伤全部消化且治理授权闭环；9 条 H 项零挂牌；剩余 3 处低危精修（§2.1–2.3）可在 Phase A 启动前顺手改正，或在完成报告里显式交代（#3 折并、#10 对齐 §7、parity 反向扩展条款）。

简报已是合格的施工输入：根因扎实、状态模型设计正确、并发约束到位、scope 收口自觉、CHANGELOG 口径自洽、测试分层清晰。第四轮可以开工。
