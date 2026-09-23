# dsh-web 收敛方案 r8 评审定向复核（codex 审核两处纠正）

- 日期：2026-09-23
- 复核对象：
  1. 方案文档 `docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v8 的 A4a
     锚点纠正与 §9.8 勘误节；
  2. r8 报告 `docs/2026-09-23-dsh-web-source-first-convergence-plan-review-r8.md`
     的 P0 来源清单 iOS 行补记。
- 复核范围：**仅** codex 审核结论指出的两处纠正及其声明的改动范围，不重做全量评审。
- **结论：通过。** 两处纠正均与源码/事实逐行吻合，改动范围未超出 §9.8 勘误声明。
  r8 的「通过」结论按既定规则正式认定。

## 逐项复核记录

### 1. A4a 锚点复核 — 吻合

**1a. 方案文档 A4a 行（§3 Gate A 表，第 147 行）**

Read 工具读方案文档 147 行，A4a 行负例单元格现为：

> 模型不支持图片 → `session/attachment-invalid`（MODEL_DOES_NOT_SUPPORT_IMAGES，
> `commands.ts:350-355`，reason 字段在 :354）

锚点已从 v8 原文的 `:346-353` 改为 `:350-355` 并标注 reason 字段在 :354。
`grep -n "346-353\|350-355"` 全文检查：`346-353` 仅剩一处出现在 §9.8 勘误节
（第 621 行，作为误写历史记录保留），无其他残留旧锚点。

**1b. 上游源码逐行亲核**

Read 工具读 `/Users/jacklee/Projects/deepseek-harness/packages/api/session-controller/src/commands.ts`
第 340–369 行，逐行确认（该文件在本复核时点无未提交修改，
`git status --porcelain -- <file>` 输出为空）：

| 行号 | 源码内容 | 核对结果 |
| --- | --- | --- |
| 344 | `const hasImage = request.content.some(part => part.type === 'image')` | hasImage 起点，§5.4 引用区间起点吻合 |
| 346 | `try {` | §9.8 勘误所述「346 是 `try {`」吻合 |
| 350 | `if (model.inputModalities !== undefined && !model.inputModalities.includes('image')) {` | **350 = inputModalities 校验条件**，吻合 |
| 351 | `throw new RemoteError(` | throw 块起点 |
| 352 | `'session/attachment-invalid',` | 错误码吻合 A4a 行 |
| 353 | `` `Model "${current.model}" does not support image input.` `` | §9.8 所述「353 是错误消息字符串」吻合 |
| 354 | `{ reason: 'MODEL_DOES_NOT_SUPPORT_IMAGES' },` | **354 = reason 字段**，吻合 |
| 355 | `)` | throw 块终点；**351–355 = throw 块**，吻合 |

即新锚点 `commands.ts:350-355`（reason 字段 :354）与源码逐行吻合，codex
审核指出的错位（旧锚点 346-353 未覆盖 :354 的 reason 字段）确已修正。

**1c. §5.4 引用块仍准确**

Read 方案文档 §5.4（第 485–499 行）：第 490–492 行引用
`session-controller/src/commands.ts:344-363`，描述为「模型 image 支持校验
（不支持则 `session/attachment-invalid`，MODEL_DOES_NOT_SUPPORT_IMAGES）+
`admitPromptContent` 持久化」。上游亲核：344 = hasImage 起；模型校验与 throw
（350–355）在区间内；`admitPromptContent` 调用在 362 行（区间内，363 为
`createUserMessage`）。引用区间与描述仍准确，未被本次纠正波及。

### 2. r8 报告来源补齐复核 — 吻合

**2a. 报告内补记内容**

Read r8 报告 P0 来源清单（第 7–16 行）：

- 第 14 行已新增 iOS 行：`iOS（配套）`｜路径
  `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`｜分支
  `feat/ios-native-message-timeline`｜提交
  `dd3911785868930dfbf45e681e0837e88333148f`｜未提交状态「干净」，并带
  【补记：本行按 codex 审核结论（2026-09-23）补齐……提交身份由 codex 审核亲核】
  标注。
- 第 16 行将原省略判断（「本轮未使用 iOS 仓源码……故不列 iOS 来源行」）以删除线
  保留，并附【更正（codex 审核结论 2026-09-23）……原省略判断有误】更正注记。

**2b. iOS 工作树 git 亲核**

```
git -C /Users/jacklee/Projects/cordcode-ios-native-message-timeline rev-parse HEAD
  → dd3911785868930dfbf45e681e0837e88333148f
git -C /Users/jacklee/Projects/cordcode-ios-native-message-timeline branch --show-current
  → feat/ios-native-message-timeline
git -C /Users/jacklee/Projects/cordcode-ios-native-message-timeline status --porcelain
  → （空输出，工作树干净）
```

提交、分支、「干净」状态三项均与补记行逐字一致。

### 3. 改动范围抽查 — 未超出勘误声明

- **Date 行未升版**：Read 方案文档第 3 行，仍为
  `Date: 2026-09-23（v8，同日第七轮修订）`，未升为 v9，勘误以 §9.8 形式记录
  而非新版本轮次。
- **§9.8 勘误节存在且内容相符**：文档共 632 行，§9.8（第 614–632 行）为末节，
  标题「勘误（v8 过审后定向纠正，依 codex 审核结论 2026-09-23）」，逐条记录
  A4a 锚点修正（含旧锚点错位细节）与 r8 来源清单补齐两项纠正，并声明本复核
  通过后 r8 结论正式认定——与 codex 审核结论的两处问题一一对应。
- **A4a 行除锚点外内容未被改动**：与 r8 报告中对 A4a 行的三处描述对照——
  「A4a 对 OD-3 任一选项必需」（r8 §2 交叉对账）对应现「图片附件（OD-3 任一
  选项均必需）」；「A3a/A4a/A5/A6/A7 待捕」（r8 §4）对应现状态列「待捕」；
  r8 §1 锚点核验记录所述原文引用区间 346-353 即被修正的旧锚点。另与 r7 报告
  意见 3 对 v7 A4 行的描述（「A4 只规定 image part、`session/attachment` 和
  图片拒绝」）对照，A4a 行的样本列（types.ts:89-96 image part + session/attachment
  读取）、断言列（attachmentId ↔ journal 引用映射）、缺项即停列（S4 图片范围
  阻塞）均一致，仅负例列锚点由 `:346-353` 换为 `:350-355`（及随锚点补充的
  「reason 字段在 :354」标注）。

## 来源清单（本复核亲核）

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 说明 |
| --- | --- | --- | --- | --- | --- |
| MacBridge（本仓） | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 与 r8 报告第 18–21 行记录一致（dsh-web 实现文件已修改 + 方案/评审文档未跟踪），HEAD 未变 | 复核对象两份文档均为未跟踪文件，按工作树现状读取 |
| 官方 dsh（上游） | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | `commands.ts` 无未提交修改（`git status --porcelain -- <file>` 为空） | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`，与 r8 报告声明的上游版本一致；锚点亲核即在此身份上执行 |
| iOS（配套） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净（`git status --porcelain` 为空） | 与 r8 报告补记行逐字一致；本复核未引用 iOS 源码作任何结论 |

## 本轮执行说明

- 亲核命令：`git rev-parse HEAD` / `git branch --show-current` /
  `git status --porcelain`（三仓）、`git describe --tags --exact-match HEAD`
  （上游）、`grep -n`（锚点残留与评审报告对照）；Read 工具读方案文档
  1–40 / 120–179 / 483–517 / 560–632 行、r8 报告全文、上游 commands.ts
  340–369 行。
- 未执行：任何文件修改（本报告除外）、任何 git 提交、任何对运行座位/实例的
  请求；未重做 r8 全量评审（30+ 锚点抽查、Gate 交叉对账等不在本次范围）。
