# dsh-web Backend 设计稿 v2 第二轮评审报告

- 评审对象：`docs/2026-08-16-dsh-web-backend-design.md` v2（commit a979d43）
- 评审日期：2026-08-16
- 评审方法：逐项对照第一轮问题清单（`…-review.md`）核验修订落实 + 对 v2 **新增断言**重新做源码/活体核验（不采信采纳记录自述）
- 第一轮基线：dsh pin 47f9438 源码 + 本机 3080 活体 + bridge/iOS 源码（证据索引见第一轮报告 §5）

## 1. 结论

**APPROVE（通过）。** 第一轮 1 阻断 + 4 必改 + 14 建议全部得到有效处置；唯一部分不采纳项（M3 的 merge 前置）理由成立且不引入技术缺口。二轮复核未发现新的阻断或必改问题；新发现 6 条**建议级**尾项（实施细节与文字残留，可随实施处理或 v2.1 微调，不阻塞 owner 终审批准）。

## 2. 第一轮条款逐项复核

### 阻断 B1（载波）——修复完整 ✅

- §3.2 重写内容与第一轮证据链**逐点一致**：WebSocket（`websocket-downlink.ts` ws WebSocketServer）、GET 恒 426 / upgrade 得 101（活体）、README「SSE 帧」象限措辞溯源、三层信封（ClientRequest/ServerResponse/ServerRequest + payload 槽）。
- 全文 SSE 残留扫描：仅存在于更正说明/纪律/采纳记录语境（第 43/51/53/59/230 行），**无功能性 SSE 描述残留**；§4.3.1「第二条 WebSocket 流」、§4.3.3「两条常驻 WebSocket 流」、§6「含 WebSocket 端点」、§8.3「WS mux/host 双流管线」同步更正到位。
- §2.3 第 5 条纪律（物理载波断言须读实现+活体）把教训固化，超出第一轮要求的修复范围，认可。

### 必改 M1（SessionActivityProbing）——落地 ✅

§4.3.2（v2:133）设计与 `core/interfaces.go:185-193` 契约一致：running 徽标数据源、**错误/未知 ⇒ 保守 active**（「Implementations must be conservative: unknown/error ⇒ active」原文对应）、并正确援引 `handlers_projection.go:1232-1238` commit gate 语义。§6 有三态专项测试。坑 4 后半闭环成立。

### 必改 M2（审批一期必接）——落地 ✅，附二轮源码加证

- 升格理由保留（policy=ask 活体证据）；映射面（帧→既有 permission/question 事件、应答 `/api/respond`、`resolve_user_input` 不用于一期）明确。
- **二轮新源码加证**（v2 断言「回显帧信封 rpcId」与「先答者得」）：
  - `approvals.ts:1-21`：approval requested 帧=server-request（稳定 rpcId），应答=client-response **回显该 rpcId**，payload=`{sessionId, approvalId, outcome}`，**outcome 仅 `allowed-once`|`rejected`**（cancelled/unavailable 是 host 侧），最终结果走 resolved 帧——v2 §3.2 断言准确；
  - resolved 帧经 `broadcast()` 推送全部 mux 消费者（api-proxy.ts broadcast 函数，第一轮已核）——「web 先答 → bridge 收 resolved 帧关闭请求」的先答者得机制成立。

### 必改 M3（复用 vs 不共享 / 分支依赖）——采纳部分有效，不采纳部分理由成立 ✅

- 矛盾修复：「复用=复制映射表（连同 wire fixture 单测）进 dshweb、不 import」+ 实施基线写明两仓 `dsh/driver` 分支（§4.1，v2:93）。当前分支即 dsh/driver，`agent/dsh/codec.go` 在树内可达，复制来源满足。
- 「合 main 前置」不采纳：理由=owner 已裁决 merge 时机另行决定，同分支两包并存即满足复制来源。**技术核验成立**（dshweb 与 dsh 同树无 import 依赖，合 main 时序不影响实现正确性）；merge 时机属 owner 已裁决事项，按评审纪律不重开。接受。

### 必改 M4（接线点清单）——落地且完整 ✅

§4.3.2 表（v2:123-131）覆盖第一轮全部 ≥8 处：投影 7 处（hydrate 集合:337、pathless:468/:629、guard+case:1003/:1059、forceCold×2、**不进**项 :432/:548、**不进**剪枝 handlers.go:1039）+ descriptor/main/server 3 处。pathless 家族归属与「不进」判定与第一轮源码核对一致。仅一处行号归属小瑕疵（见 R2-6）。

### 建议 S1-S14——全部落地 ✅（两处轻微残留，见 R2-4/R2-5）

逐条对照：S1 active:true 过滤+全量进诊断（§4.3.5）✓；S2 §5 差异清单 ✓（但 §6 矩阵行未显式落，R2-5）；S3 双实例策略+不确定性如实+sandbox 实验+二期重探（§4.2/§6）✓；S4 外部会话路由+先答者得+question 选型（§4.3.4）✓（措辞歧义见 R2-3）；S5 三行补齐（§4.3.8）✓；S6 版本占位符语义（§4.3.8）✓；S7 RpcError 透传断言+WS 重连专项（§6）✓；S8 §8 重排 8 项、create/rename/directory/projects/双流/审批/接线点全补 ✓；S9 search 改 2️⃣ ✓；S10 iOS 11 文件清单+两处显式决策点（§4.1）✓；S11 无凭据面+暴露面如实（§4.2/§4.4）✓；S12 SSV2 七项清单成段（§4.5，内容合理：真相 owner=dsh web、零直接写、失败呈现含「审批无超时裁判」符合护栏 4/7）✓；S13 理由修正（§4.3.5）✓；S14 前缀更正（§4.3.5）✓（§4.3.8 残留一处，R2-4）。

## 3. 二轮新发现（全部建议级，无阻断/必改）

| # | 发现 | 证据 | 建议 |
|---|---|---|---|
| R2-1 | **question 整批作答语义未展开**：dsh 一次 ask 多题**整批一次作答**（`questions.ts:11-14`「one ask, many questions, one answer — never split per question」；应答 payload=`{sessionId, answer}` 无资源 id，rpcId 即批 id）；bridge `question_reply` 是单题模型（questionId+optionIds）。映射需在 bridge 侧聚合整批，iOS 逐题作答的聚合点与部分作答中间态未设计 | questions.ts:1-20 + handlers.go:3636-3660 | §4.3.4 补一句：question/requested 整批展开为逐题事件、应答在 bridge 聚合为单次 respond；未答齐时等待（不代答） |
| R2-2 | **approval outcome 二值集与 iOS 权限 UI 选项的映射**：dsh client 仅 `allowed-once`\|`rejected`（approvals.ts:17-21），无「始终允许」持久化面；bridge `PermissionResult.Behavior` 是自由 string。iOS 权限 UI 若有 always 类选项，对 dsh-web 应隐藏或如实降级为「允许一次」 | approvals.ts + core/interfaces.go:56-58 | §4.3.4 补一句 outcome 集如实映射，always 类选项不出现 |
| R2-3 | 外部会话审批 surface 条件措辞歧义：「iOS 已订阅/打开的会话」与括号内技术判据「bridge registry 有会话对象（`h.getSession()` 命中）」是两个集合——observation 订阅的外部会话无 registry 对象，按技术判据不 surface（唯一可实现，因 `resolve_permission` 依赖 `h.getSession`），但「已订阅」字面会误导 | v2:152 + handlers.go:3625-3633 | 措辞统一为「registry 判据」：仅 iOS 创建/打开（registry 有对象）的会话 surface；observation 订阅会话的 approval 帧只随事件流可见 |
| R2-4 | §4.3.8（v2:180）残留 `agentPresets` 复数前缀一处（S14 只改了 §4.3.5；rpc-map.ts:54-59 实为 `agentPreset.*`） | rpc-map.ts | 文字更正 |
| R2-5 | S2 采纳记录称「真机矩阵加一行」，§5（v2:200）也写「真机矩阵加一行覆盖」，但 §6 真机验收（v2:208）未显式列出该行（旧件早期 `default/deepseek-chat` 会话续聊→可见错误→switch_model 修复） | v2 §5 vs §6 | §6 矩阵补该行 |
| R2-6 | 接线点表行号归属小错位：`:468` 属 `ensureProjectionHydrated`（func 起 418 行）内的 sourceChanged 判定，非表中所写「prepareProjectionHydrateSource pathless 分支」（该分支是 `:629`，func 起 534 行）；两处均需加入，处置不受影响 | handlers_projection.go:418/534/468/629 | 表格行归位（468 并入 forceCold/sourceChanged 行） |

## 4. 三项遗留评审项终态（对照第一轮判定）

| 遗留项 | 一轮判定 | 二轮判定 |
|---|---|---|
| ① 投影尾封口（坑 4 后半） | 未闭环 | **闭环**——§4.3.2 SessionActivityProbing 设计与 core 契约逐点一致（保守方向、数据源、不实现的后果援引准确），§6 三态测试补齐 |
| ② RpcError 透传（坑 7） | 文本闭环、测试面缺 | **闭环**——§6 专项（构造 bad-request/业务错误帧断言原始文本到达 iOS 侧） |
| ③ 失败必现终态（坑 8） | 文本闭环、分期矛盾 | **闭环**——审批升格一期必接消除挂起路径；§4.5「审批超时→无超时裁判（iOS 不裁判）」符合 SSV2 护栏 |

## 5. 结论与后续

设计稿 v2 达到可批准状态。R2-1/R2-2 建议在实施前顺手补进设计（两句话的成本，避免实施时在 question 聚合与权限选项映射上重新调查）；R2-3~R2-6 为文字修订。均不构成批准障碍。

owner 终审批准后，按 §8 八项拆分走 exec-plan；实施期须完成的待证项（设计已如实挂账）：双实例受控实验（sandbox DSH_HOME）、`session/projection` 帧与 get_usage 二期核对。
