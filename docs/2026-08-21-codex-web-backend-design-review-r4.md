# codex-web Backend 设计四轮评审报告（r4，确认轮）

- 日期：2026-08-21
- 评审对象：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md) **v1.4**（含 §18/§19/§20 三轮采纳记录）
- 前序评审：[r1](2026-08-21-codex-web-backend-design-review.md) · [r2](2026-08-21-codex-web-backend-design-review-r2.md) · [r3](2026-08-21-codex-web-backend-design-review-r3.md)
- 评审方式：全文重读 v1.4 新增内容（§2.5、§7.3、Phase 5、§20）；核对前置分析文档的降级标注；对 §7.3 shape 索引逐行复核前序轮次已核实的源码事实；检查 v1.4 有无新引入矛盾。

---

## 1. 最终结论

**APPROVE**

三轮评审 r3 §6 提出的全部事项（1 应修 + 3 增强）均已正确落实，其中 §6.3 选择了正确的执行时点（Phase 5 当前 HEAD 审计而非现在冻结静态清单），处置合理。v1.4 新增内容全部与前序轮次核实的源码事实一致，未发现新引入的矛盾、错误断言或分级不一致。

**设计文档集收口完成，评审周期结束。** 下一步为执行性工作（Phase 0），不再需要设计评审轮次；若 Phase 0 样本与本文断言冲突，按 §3.0 证据优先级以样本为准并回写设计与 §7.3。

## 2. r3 事项落实核验

| r3 事项 | v1.4 落点 | 核验结果 |
|---|---|---|
| §6.1 前置分析降级纠错（应修） | 前置分析头部新增 `[!WARNING]` 块：明确"历史输入，不是实施权威"，并**逐项点名禁止恢复的旧结论**（重构旧 `agent/codex`、统一 wire identity、猜测固定端口、`codex exec` 默认基线、"外部 turn 已可实时订阅"）；状态行改"不授权实施"；设计头部链接同步改为"仅作问题来源，已降级，不授权实施" | ✅ 双侧落实；点名式禁止清单优于笼统降级，精确对应 r3 §6.1 表列出的五类矛盾 |
| §6.2 历史故障 → 消除机制映射表 | 新增 §2.5 四行表（identity 漂移 / EOF 假完成 / delta-completed 双发 / provider 单帧误诊），每行含根因、结构性消除机制、验证落点；收尾规则"测试失败必须修结构分歧，不得用旧 file relay fallback 遮蔽"；§13.1 新增"历史故障回归"测试项（与"旧 backend 回归"分列） | ✅ 四项故障与 think.md 复盘（2026-07-22 rollout identity/EOF/双发、2026-07-06 provider 非流式）一一对应；消除机制与 §7.1 红线、§9.2 封口规则、§13.2 帧级指标互洽 |
| §6.3 iOS 文件级影响审计 | Phase 5 重写为"先审计、后改码"硬门槛：扫描面 12 类（BackendKind/wire kind/discovery/switch/server creation/display/capability gate/model-permission-agent mapping/cache scope/stream-recovery 特例/protocol mirror/tests）；逐文件四类标记（must change / verified generic / intentionally codex-only / N/A）；两个强制裁决（禁机械 `\|\| .codexWeb`、identity/cache 独立）；"审计缺失时 Phase 5 不得开工" | ✅ 扫描面覆盖并超出 dsh-web S10 的 11 文件清单类别；**执行时点选择正确**——Phase 0–4 期间 iOS HEAD 会漂移，现在冻结文件名清单必然陈旧，绑定 Phase 5 入场时点 + 硬门槛是更强而非更弱的约束 |
| §6.4 协议 shape 沉淀 | 新增 §7.3 集中索引 11 个 surface 的源码推导 shape，序言明确"不是 wire fixture，须 Phase 0 样本 + 生成 schema 同时吻合才能升级为 contract 输入"；收尾禁止据此生成递归猜测器 | ✅ 逐行复核：`Model` 无 provider、`thread/start` model/modelProvider stable、`turn/start` 无 modelProvider、steer `expectedTurnId` 必填、`Config` snake_case + flatten additional + ExperimentalApi、requestUserInput 批结构按题 id 应答、command approval experimental 剥除、permission approval `GrantedPermissionProfile+scope`、elicitation 三 variant、plan delta experimental、`turn/completed` itemsView 三档——全部与前序轮次源码核验一致 |
| §20 三轮采纳记录 | 新增 §20，含 §6.3 执行时点说明与两处部分采纳的存续声明 | ✅ 描述准确 |

## 3. 新引入问题

无阻断、无必改、无建议。v1.4 的所有新增断言均在 r1–r3 已核实的证据范围内；§2.5/§7.3/Phase 5 与既有章节（§7.1、§9.1/9.2、§13.1、§15）交叉引用一致。

## 4. 评审周期总结

| 轮次 | 结论 | 阻断 | 必改 | 建议 | 备注 |
|---|---|---|---|---|---|
| r1 | APPROVE WITH CHANGES | 1（B1 共享运行时事实） | 3（M1 分级 / M2 provider-memory / M3 接线清单） | 5 | 全部源码逐项核查 24 项 |
| r2 | APPROVE WITH CHANGES（收口级） | 0 | 1（R2-M1 config/read 事实） | 2 | M2 不采纳结论经复核成立，论证一处错误 |
| r3 | APPROVE | 0 | 0（设计本体）；文档集 1 应修 + 3 增强 | — | 更正 r2 报告自身的组成判断；dsh-web 对比分析 |
| r4（本轮） | APPROVE | 0 | 0 | 0 | 确认性收口 |

- 累计处置：1 阻断 + 4 必改 + 7 建议 + 1 文档集应修 + 3 增强，全部落实或经独立复核维持部分采纳（一轮 M2、二轮 R2-M1 两处，理由均以官方源码为据且被后序轮次复核确认）。
- 相比 dsh-web 的四轮评审（1 阻断 + 6 必改 + 23 建议），codex-web 用更少的评审轮次达到了更完整的收口，且把 dsh-web 靠评审逼出的三个构件（接线清单、iOS 审计、坑表）直接建进了设计。
- 设计 v1.4 与其文档集（前置分析已降级、三轮评审报告归档）现在构成自洽的证据链：pin 源码 → 评审核验 → 受控 Gate → Phase 0 待证清单。

## 5. 移交

Phase 0 开始执行。出口以设计 §8.2/§12/Phase 0 为准；执行中发现与本文断言冲突时，按 §3.0 证据优先级处理并回写 §7.3 与相关章节。
