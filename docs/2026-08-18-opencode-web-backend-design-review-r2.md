# OpenCode Web Backend 设计稿 v3 第二轮评审报告

- 评审对象：`docs/2026-08-18-opencode-web-backend-design.md` v3（commit e4feb40，与一轮评审报告同提交）
- 评审日期：2026-08-19
- 评审方法：v2→v3 diff 逐项核验修订落实 + 对 v3 **新增/改写的断言**做独立源码复核（重点：作者亲核修正的 S7——落实修订时的机制描述是 R3 病类高发点）+ audit-plan 内容形状断言背书盘点

## 1. 结论

**APPROVE（通过，可交付 owner 终审）。** M1-M3 必改与 S1-S9 建议全部有效落实，无拒绝项；S7 的「亲核修正」经我独立源码复核**准确**（见 §2）；v3 未引入新的失实机制。两条实施期提示（行号基线漂移、M3 用例可测形态）不构成批准障碍。

## 2. 逐项核验

### M1（iOS if 比较型接线）——合格 ✅

§4.1.7 重构为两类安全性声明（switch=编译器强制 / if 比较型=静默漏、须 rg 人工核对）与一轮核验事实一致；SessionsView×7 / SidebarView×3 / SessionManagement×2 行为表的行号、语义、「不加入的后果」描述与一轮源码抽查逐点吻合；DirectoryPreferences 核对项与 5 个默认值文件注记齐备；§6 行为断言用例与 §8-7 并入到位。

### M2（go-bridge 两处表外键控）——合格 ✅

switchDir 特判（M2-1）与 disablesRelayIdleTimeout（M2-2）两行的处置与源码语义一致（`shouldSwitchWorkDirForMethod` 四读方法 false、dsh-web 在列的注释事实）；坑 5 行同步闭环更新；§6 请求头断言与 relay 超时用例补齐。

### M3（observation re-attach）——合格 ✅

决策进名单（与 codex/claude 同待遇）理由成立（外部 turn 旁观的 relay 重连恢复依赖）；**dsh-web 不在名单的既有缺口如实标注「另报 owner、不在本设计内修」——处置正确**（不越权代修他路线，交 owner 裁决）。

### S7（亲核修正）——独立复核**准确** ✅（本轮关键核验点）

作者把原行「否则 list_models 失败被静默」改写为「门控的是 list_sessions 对未协商 catalog_cursor_epoch_v2 的旧客户端（显式 wire 错误）」并改为**不加入**。我的独立复核：`catalogCapabilityRequiredFor` 唯一调用点 `handlers.go:1011`——`msg.Method == "list_sessions" && catalogCapabilityRequiredFor(agent.Name()) && !ConnCatalogCursorEpochV2(conn)` → 显式 `protocol.capability_required` wire 错误（retryable=false），注释明写「Phase 8B minimum-client retirement」——**与 list_models 无关、非静默**，作者修正属实；「dsh-web 不在列且工作正常→同判不加入」推论成立。这条修订本身就是「落实对照时亲核源码」的正面示范。

### S1-S6/S8/S9——逐条对照 diff 合格 ✅

S1 两级 tokens 形状（顶层无 total/message 级有）✓；S2 worktree 字段映射 ✓；S3 双面共存注记（互斥依据=v2 无 `/global/health`）✓；S4 二进制置信+先探维持 ✓；S5/S6 无需改动注记 ✓；S8 「下次发送生效」验收口径 ✓；S9 坑 9 补注 ✓。

## 3. audit-plan 背书盘点

v3 新增/改写的全部内容形状断言均有本会话证据背书，无「描述了但无样本」的内容类型：

| v3 断言 | 背书 |
|---|---|
| 两级 tokens 形状差异 | 活体 100 会话采样 + 18457 实例 |
| /project 字段=worktree | 活体采样 |
| 1.18.18 双面共存（/api/* 200） | 活体探针 + checkout v2 路由源码 |
| catalogCapabilityRequiredFor 真实语义 | 本轮源码复核（handlers.go:1011-1019） |
| if 比较型接线 3 文件行为语义 | 一轮源码抽查（SessionsView/SidebarView/SessionManagement 逐处） |
| switchDir 四读方法语义 / relay 超时注释 / observation 名单 | 一轮源码核验 |

## 4. 实施期提示（建议级，不阻塞批准）

1. **行号基线漂移**：v3 前一提交（361e5c2 Phase 5）已使 handlers.go 行号移动（如 `catalogCapabilityRequiredFor` 调用点现为 :1011）。设计已自声明「行号以 rg 复核为准」，实施时执行即可，无需改文档。
2. **M3 用例可测形态**：`resubscribeObservationSessions` 名单为函数内硬编码数组——§6 断言「名单含 opencode-web」需行为级测试（重连后 scope re-attach 观测）或将名单提为可测常量，实施时二选一。
3. **dsh-web 既有缺口**（resubscribe 名单缺 dsh-web）：属 dsh-web 路线问题，本设计已如实上报——建议 owner 裁决是否在 dsh-web 队列补一行（与本设计实施互不阻塞）。

## 5. 终态

- 一轮 3 必改 + 9 建议全部闭环；十三坑中坑 5 经 M2-1 补行后**完整闭环**（一轮判「部分」的唯一项消除）。
- v3 达到并超过 dsh-web v3.1 交付水位（接线表密度与两类安全性分类是其改进）。
- 待 owner 终审；批准后按 §8 八项拆分实施，实施期挂账项不变（1.18 权限字面量先探、rename/delete 活体、双代探针记录进诊断）。
