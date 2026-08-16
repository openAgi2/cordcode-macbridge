# dsh-web Backend 设计稿 v3.1 第四轮评审报告（收口轮）

- 评审对象：`docs/2026-08-16-dsh-web-backend-design.md` v3.1（commit 7b05c45）
- 评审日期：2026-08-16
- 评审方法：对照 v3→v3.1 diff 核验两项必改的修正质量；对修正中**新引入的行为断言**（中间态保持 pending、host 批 resolved 收口、批状态生存期）补做 dsh 源码核验
- 历史报告：`…-review.md`（r1）/ `…-review-r2.md` / `…-review-r3.md`

## 1. 结论

**APPROVE（通过，可交付 owner 终审）。** R3-1/R3-2 两项必改按源码事实正确重写，无新引入的失实机制；§2.3 纪律 1 的复发封堵与 §4.3.4 自查声明经复核可信。本轮补验中确认的三条收尾建议（S-1 批 resolved 展开机制一句话、S-2 断线边界如实标注、S-3 重复提交幂等）均为建议级，其中 S-1 建议批准前顺手补入（一句话成本），不构成批准障碍。

## 2. 必改项修正核验

### R3-1（问答整批）——修正合格 ✅

逐点对照源码：questionId=每题自带 id（events.schema.ts:21 ✓）、iOS 替换式 upsert 的引用（ChatViewModel+CodexStreaming.swift:768/1826-1832 ✓）、帧 rpcId 降为内部批 key 不上 wire ✓、respond 按题 id 组装（user-questions/types.ts:53-66 ✓）、不合成逐题 resolved（虚假乐观论证成立——他题 reject 整批取消时先前「已提交」确为谎言）✓、reject 走 error 分支不对称（respond 实现 ✓）。v3 的覆盖性缺陷（坑 8 新路径）消除。

### R3-2（审批选项）——修正合格 ✅

删除臆造机制 ✓；wire 无声明字段（events.go:156-161 ✓）、iOS 硬编码四项（CCCodeBridgeBackendClient.swift:1390-1395 ✓）、always 折叠二值（:787-802 ✓）、标签弱化如实接受 ✓、隐藏变体列可选优化并提示 §4.1 清单补充 ✓。与三轮核验事实逐点一致，无保留。

### §2.3 纪律 1 修订 / §4.3.4 自查声明——核验可信 ✅

纪律条款准确点名复发模式与纠正来源。自查所列其余断言的三轮证据逐条在案：create/prompt/resume（r1 ensureSession）、cancel（r1 schema）、approval outcome 集（r2 approvals.ts:17-21）、rpcId 回显（r2 approvals.ts:1-21）、resolved 帧全员广播（r1/r2 broadcast）、registry 判据（r1 handlers.go:3625）。

## 3. 本轮补验与收尾建议（均建议级）

### S-1 批 resolved 帧的「统一映射」写明展开机制

v3.1 写「各题保持 pending 直至 host 批 `question/resolved` 帧到达后统一映射」。补验的 wire 事实：host 帧载荷仅 `{sessionId, questionRpcId, outcome}`（events.schema.ts:52），**无逐题内容**；iOS 的 `question_resolved` 事件形状为 `{questionId, result}`（events.go:208-212）。故「统一映射」=dshweb 从批状态查 rpcId→题 id 集合，展开为 N 个 `question_resolved`（result 置 outcome 或留空）。建议把这一句写进设计，防实施者预期帧内有逐题数据。

### S-2 断线边界的收口路径如实标注

补验：mux 重开**重放 still-pending** 的 question/approval 帧（同 rpcId，api-proxy.ts:689「initial push and mux-open replay share it」/:3445 pendingQuestions/pendingApprovals 重放）——bridge 断线重连后 dshweb 可重建批状态、iOS 重收 N 题（upsert 同 id 幂等），恢复路径存在。但断线窗口内**已被 web 端应答**的批不重放（replay 仅 still-pending）——iOS 该批 pending step 在 live 视图内不收口，自愈靠会话重开冷加载（ask 的工具调用结果入 durable 日志，history 重建终态）。此边界与既有 backend 的 transient question 同类、非永久挂起，建议一句话如实标注（含「官方无批状态查询面，live 视图内自愈二期评估」）。

### S-3 重复提交幂等

批 pending 期间 iOS 可对同一题重复提交（UI 不禁用）——dshweb 批状态按题 id 覆盖即可，一句话写明。

## 4. 终态判定

- 三项遗留评审项：**闭环维持**（坑 4 后半/M1、坑 7/透传断言、坑 8/审批必接+R3-1 修正；S-2 断线边界属既有系统同类，冷开自愈，不构成新击穿）。
- 四轮累计：1 阻断（B1）+ 6 必改（M1-M4、R3-1/R3-2）+ 23 建议（S1-S14、R2 系列、S-1~S-3）全部处置；owner 已裁决事项（SDK 路线、merge 时机）未重开。
- v3.1 交付状态：**待 owner 终审**。S-1 建议随终审顺手补入；S-2/S-3 可在实施期（§8 拆分 4 审批问答链路）落实。实施期挂账项不变：双实例 sandbox 实验、get_usage 二期投影核对。
