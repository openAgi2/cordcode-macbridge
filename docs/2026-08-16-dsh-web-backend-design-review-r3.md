# dsh-web Backend 设计稿 v3 第三轮评审报告

- 评审对象：`docs/2026-08-16-dsh-web-backend-design.md` v3（commit 6b922ef）
- 评审日期：2026-08-16
- 评审方法：对照 v2→v3 diff 逐条核验 R2-1…R2-6 落实情况；对 v3 新增的**行为设计断言**（R2-1 批问答、R2-2 选项声明）重新做 bridge/iOS/dsh 三侧源码核验——纯文字修订不再重验源码，行为设计必须重验
- 前两轮报告：`…-review.md`（一轮）、`…-review-r2.md`（二轮）

## 1. 结论

**修改后通过。** R2-3/R2-4/R2-5/R2-6 四条纯修订落地合格；但 R2-1 与 R2-2 在 v3 从「建议」升级为行为设计时**各自引入了一处与源码不符的机制**（一处选错标识字段导致覆盖性缺陷、一处描述了不存在的声明机制），均需段落级修正后才能交付实施。两条都局限在 §4.3.4 审批/问答段落，路线与其余设计不受影响。

值得记录的元教训：二轮这两条建议原文（「映射需聚合」「应隐藏或如实降级」）本身有据，v3 落实时补的**机制细节**（共享 rpcId、按事件声明渲染）未经源码核验——正是 §2.3 纪律 1/5 要防的错误类型在「修订动作」里的复发。建议 v3.1 修正后，实施前对 §4.3.4 全段做一次源码对照复核。

## 2. 逐项核验

### R2-3（surface 判据统一）——✅ 合格

措辞改为「registry 命中（`h.getSession()` 有会话对象——即 iOS 打开过该会话；observation 订阅而无 registry 对象不构成判据，两者是不同集合）」——准确消除二轮指出的歧义。

### R2-4（agentPreset 前缀）——✅ 合格

§4.3.8 已更正 `agentPreset.*`。

### R2-5（真机矩阵行落 §6）——✅ 合格

§6 补入 S2 行（旧件早期会话续聊→官方模型错误可见透传→switch_model 修复后可续），且额外补了审批链路与列表自动同步两行验收——超出要求，内容与设计一致。

### R2-6（接线点表 :468 归属）——✅ 合格

表格修正为「`prepareProjectionHydrateSource` pathless 分支（:534 起，条件在 :629）」+「`ensureProjectionHydrated` ~:418 起 sourceChanged 判定」——与 handlers_projection.go 函数边界（418/534 行 func 起）一致。

### R2-1（问答整批作答）——❌ 必改 R3-1：标识选错，N-1 题会被 iOS 覆盖

**v3 设计**：「一批 N 题 → N 个 question 事件**共享同一帧 rpcId**（作 questionId），dshweb 持批状态」。

**源码事实（三侧）**：

1. **iOS 存储是按 questionId 替换式 upsert**：`question_asked` → `upsertCodexToolStepInMessage(itemId: questionId, …)`（`ChatViewModel+CodexStreaming.swift:768`），该函数「同 id 替换、异 id 追加」（`:1826-1832`）。N 个共享 rpcId 的 question 事件在 iOS **只剩最后一题**，其余 N-1 题不可见、不可作答 → 整批永远收不齐 → **挂起**（违反 fail-visibly，且是 v3 自己引入）。
2. **dsh 每题自带唯一 id**：`AskUserQuestionItem.id`（`events.schema.ts:21`）；应答形状 `AskUserQuestionAnswer = {answers: [{id, selected, custom?}]}` **按题目 id 键控**（`user-questions/src/types.ts:53-66`）；host 侧 `respond` 用 `matchesQuestions(payload, pending)` 校验答案与题目集合匹配（`api-proxy.ts` respond 分支）。
3. **bridge wire 的 questionId 字段**承载 `ev.QuestionID`（`events.go:199-206`），语义即「题目标识」。

**修正方向**：questionId = dsh 题目 id（每题唯一，iOS N 题并列可答）；**帧 rpcId 由 dshweb 内部持有作批 key**（不进 wire）；iOS 逐题 `question_reply(questionId=题 id)` 到达后映射回批内该题，收齐整批一次 respond（answers 按题 id 组装，与 dsh 应答形状天然对齐）。

**附带两处需一并写明**：

- **R3-1b（中间态描述与 iOS 模型不符）**：v3「单题已答但批未齐=该题显示已提交」——iOS 的提交是事件驱动收口：`replyQuestion` RPC 成功后**不**本地置 completed（`ChatUIKitContainerView.swift:3443-3455` 仅打诊断标签），step 状态由 `question_resolved` 事件推进（`ChatViewModel+CodexStreaming.swift:779-793`）。要出现「已提交」显示，dshweb 需在单题应答受理后**立即发该题的 question_resolved**（bridge 侧即时反馈），批齐 respond 后 host 广播的 question/resolved 帧作权威收口（iOS upsert 幂等，已 completed 不变）；若不发逐题 resolved，实际表现是「该题保持 pending」（也可接受，但与 v3 文字不符）。二选一如实写明。
- **R3-1c（cancel 的 wire 形状）**：「任一题 reject → 整批 cancel（outcome cancelled）」的 dsh 侧表达是 **respond 的 error 分支**（`result.ok:false` + `error.code:'cancelled'` → host `ASK_CANCELLED`，`api-proxy.ts` respond 分支），**不是 value 载荷**（与 approval 的 outcome 走 value 不对称）。补一句防实施踩坑。

### R2-2（approval 二值 outcome）——❌ 必改 R3-2：「按事件声明渲染」机制不存在

**v3 设计**：「dsh-web 的 permission 事件只声明『本次允许/拒绝』两选项，iOS 权限 UI 的『始终允许』类变体对该 backend 隐藏（**按事件声明渲染**，不虚构能力）」。

**源码事实**：

1. **wire 无声明字段**：`permission_request` 事件载荷仅 `{requestId, toolName, toolInput, toolInputRaw}`（`go-bridge/events.go:156-161`）——不存在选项集声明。
2. **iOS 选项硬编码**：`permission_request` 处理固定构造四项 Approve / Always Approve / Reject / Always Reject（`CCCodeBridgeBackendClient.swift:1390-1395`），不读任何声明字段。
3. **关键缓解事实（v3 未发现）**：iOS 的 always 变体在 wire 层**本就折叠为二值**——`approveAlways` 发送 `behavior:"allow"`、`rejectAlways` 发送 `"deny"`（`CCCodeBridgeBackendClient.swift:787-802`；`:779-782` wire 契约注释：backend 只把 `behavior=="allow"` 当允许）。因此 dshweb 映射 `allow→allowed-once`、`deny→rejected` 即可**正确工作，无语义反转风险**；残余问题仅是「Always Approve」标签对 dsh-web 的语义弱化（always 无持久化面，下次仍会问）。

**修正方向**：删除「按事件声明渲染」（臆造机制）；改写为：「iOS 权限选项在 wire 层折叠为 allow/deny 二值（源码实证），dshweb 映射 allow→allowed-once / deny→rejected 即正确；『始终允许』的持久化语义对 dsh-web 不生效（无官方面）——标签语义弱化如实接受；隐藏 always 变体为 iOS 侧可选优化（按 backend 归组的新增穷举点），一期不做」。若 owner 要求一期就隐藏，需把该点补进 §4.1 的 iOS 归组清单（当前 11 文件清单不含 `CCCodeBridgeBackendClient.swift:1390` 这个行为点）。

## 3. 遗留评审项终态复核

三项遗留项（坑 4 后半 / 坑 7 / 坑 8）在 v2 已闭环，v3 的审批/问答段落修订不触及闭环机制（SessionActivityProbing、RpcError 透传断言、审批必接均在）——终态判定不变。但注意：**R3-1 若不修正，将以新形态复活坑 8**（批问答覆盖缺陷→iOS 挂起）——这是「闭环判定针对机制、新修订引入新路径」的实例，再次说明行为设计段落必须源码对照。

## 4. 结论与后续

- v3.1 修正 R3-1（含 1b/1c）与 R3-2 两段（合计约十行文字），即可维持两轮 APPROVE 结论交付 owner 终审。
- 修正时建议顺带执行 §4.3.4 全段源码对照复核（本段三轮内两处失实的集中地）。
- R3-2 修正后，「iOS 归组清单是否增加权限选项点」取决于 owner 是否要求一期隐藏 always 变体——默认不要求（wire 折叠已保证正确性）。
