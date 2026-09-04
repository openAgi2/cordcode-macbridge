# Claude Code backend 官方能力收敛升级设计 第三轮评审报告

- 评审对象：`docs/2026-09-04-claudecode-official-capability-upgrade-design.md` **v2.1**（2026-09-04）
- 对照基线：`docs/2026-09-04-claudecode-official-capability-upgrade-design-review-r2.md`（v2，通过，七条建议）
- 评审日期：2026-09-04
- 评审方法：逐项对照 R2-S1..S7，**不采信 §11.6 自述**；对刷新的 sqlite 计数做本机只读复测。未跑 Phase 0 探针。
- 评审边界：纯文档评审，未改设计稿、未改代码。

---

## 0. 本次评审来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=1d607601540be755658b7444144af0c2f797e136
未提交状态=
  已修改 docs/2026-09-04-claudecode-official-capability-upgrade-design.md（v2.1 相对已入库 v2 / 0514e97）
  未跟踪 docs/2026-09-04-claudecode-official-capability-upgrade-design-review-r2.md
  未跟踪本 r3 文件
  （v2.1 §0.1 写成「未跟踪本 v2.1 修订」不准确，见 R3-S1）
任务预期分支=plan/approval-layer 续作
配套 iOS 实施=/Users/jacklee/Projects/cordcode-ios-plan-approval  plan/approval-layer-ios  dbf0c048359ef3fec5106ed31102847c4f311eb3
iOS main（仅选择器/abort 现状引用）=/Users/jacklee/Projects/cordcode-ios  main  61f67bf63a8a5f6e10e9e9c62fbd9fe36f2236cd
配套 Mac main=/Users/jacklee/Projects/cordcode-macbridge  main  a2200cf4771b7ded4a09577bdcf9599d145d93c1
上游 SDK=0.3.260（claudeCodeVersion=2.1.260）
本机 CLI=PATH 2.1.234
```

相对 r2：Mac HEAD `390ed6e` → `1d60760`（`0514e97` 入库 v2+r1 评审；`1d60760` 为无关 exec-plan 状态）。发送侧控制=零的源码结论未因这两次 docs 提交改变。

---

## 1. 结论

**通过（APPROVE）。设计层可以停，下一步是 Phase 0 证据包。**

r2 七条建议全部在正文落地，不是只写在 §11.6。无新阻断、无新必改。剩余是来源清单措辞和一处过时产品例子，不挡探针。

---

## 2. R2-S1..S7 逐项复核

| 项 | 复核 | 落点证据 |
|---|---|---|
| **R2-S1 标量优先级** | ✅ | §2.2：Managed > `--settings` > **local > project > user**，与官方 settings 文档高→低顺序一致。功能性 `user > project > local` 已清零 |
| **R2-S2 resolved 独立字段** | ✅ | Phase 1.1 / 1.5 / §7.3.1：wire optional `resolved` 承载 `resolvedModel`；槽位沿用 `id`；明确不复用 `Alias`（方向相反 + `modelItemsForWire` 不下发）。「三列」功能性用法改为「三键」（槽位 / resolved / 观测改写名）。§11.3 S5 标题仍写「三列」属 r1 历史行，可留 |
| **R2-S3 冲突表** | ✅ | Phase 2.3 四行真表：SetLiveMode / D5 纯 allow / 本地 auto-answer / sessionActive，规则列写死。§2.4 / §5 / 风险 7 的引用现在有实体 |
| **R2-S4 haiku 多映射** | ✅ 计数与本轮复测同形 | 见下节。删除了单映射 66 |
| **R2-S5 rename_session** | ✅ | Phase 0.1 发送链已含 `rename_session`；Phase 4.2 改为「已列入 Phase 0.1」，不再互指 |
| **R2-S6 小残留** | ✅ | initialize 行号 §2.1 / §10 统一 `:3989`；§11.1 B1 落点不再指向风险 10；Phase 0.1 要求 `bypassPermissions` 单独记录 success/拒绝 |
| **R2-S7 两套 hook** | ✅ | Phase 0.3 区分 `initialize.hooks`（SDK callback / `hook_callback`）与 `--settings` HTTP hook；`hooks_applied` 只管前者；省略对照项标非硬门 |

### R2-S4 sqlite 复测（本轮只读）

| 文档 v2.1 | 本轮实测 | 判定 |
|---|---|---|
| haiku identity 538 | 538 | 🟢 |
| haiku→glm-4.7 325 | 325 | 🟢 |
| haiku→glm-5.3-flash 85 | 85 | 🟢 |
| haiku-4-5-20251001→glm-5.3-flash 152 | 152 | 🟢（r2 时 146，v2.1 刷新正确） |
| haiku→mimo-v2.5 50 | 50 | 🟢 |
| haiku→glm-5-turbo 6 | 6 | 🟢 |
| sonnet-5→glm-5.3 7603 | **7631** | 🟡 文档已声明随时间漂移；本轮又 +28 |
| fable-5→glm-5.3 677 | **689** | 同上 |
| opus-5→glm-5.3 584 / opus-4-8→glm-5.2 6943 / identity sonnet 3069 / identity opus-4-8 2002 | 同数 | 🟢 |

多映射事实成立。不要为追齐瞬时计数再改文档。

---

## 3. 三轮新发现（全部建议级，不挡 Phase 0）

| # | 问题 | 建议 |
|---|---|---|
| **R3-S1** | §0.1「未提交状态=未跟踪二轮评审文件与本 v2.1 修订」不准确：设计文件是 **已跟踪修改**（相对 `0514e97` 入库的 v2）；未跟踪的是 r2 评审文件 | 实施前再生来源清单时改成「已修改设计稿 + 未跟踪 r2/r3 评审」 |
| **R3-S2** | §2.1 仍写 `resolvedModel`「正是 iOS『haiku → glm-5.3』行渲染需要的字段」。按 v2.1 三键：`resolved`=官方 canonical（如 `claude-haiku-4-5`），`glm-5.3` 是观测改写名，且 haiku 族主改写也不是 glm-5.3 | 例子改成「`sonnet` → resolved `claude-sonnet-5`，观测可能是 `glm-5.3`」 |
| **R3-S3** | 观测改写名「另键」未给出 wire 字段名（`resolved` 已命名） | Phase 1 实施时定一个键名（如 `observedModel`）并进 protocol pack；探针前不强制 |
| **R3-S4** | `rename_session` 已进 Phase 0.1，§7.1 红线清单仍未列入其成功体 | 补进「未 dump 不得当已核实」列表，与其它 control_response 同等 |

§2.4 仍写「树内 @ 390ed6e」：对「发送侧=零」仍真（其后两次提交都是 docs），实施前按 HEAD 重核即可。

---

## 4. 进入 Phase 0

设计评审到此结束。下一步按 v2.1 §6：spawn **逐字** `baseClaudeInnerArgs`，stdin 保持打开，代际矩阵分行出结论。

配对保持：Mac `plan/approval-layer` × iOS `plan/approval-layer-ios`。配对未再核前不要改 `agent/claudecode` 或 iOS。

S8 的 interrupt 留进程 vs Close，仍是 **编码前** 必须写死的产品/工程裁决，不是本轮文档缺口。
