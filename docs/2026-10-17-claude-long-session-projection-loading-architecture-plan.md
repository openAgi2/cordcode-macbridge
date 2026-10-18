# Claude 长 session 打开架构优化方案

日期：2026-10-17
版本：v30（R25 APPROVED 后落地唯一建议 F-62，零设计变更：§3.8 首包打包句「三者合计」改为分层计量表述（turns 与 changedTurns 使用本页剩余对账空间；scalar/metadata 按已定义预算计入；anchorRefresh 不计入对账预算、另按独占额度与总硬顶计量）——消除与「anchorRefresh 不计入」定义的字面冲突；额度/顺序/续页行为/验收不变；R25 闭合标准明示可随冻结誊写同步、无需再启动评审循环；前版 v29（R24 APPROVED 后纠错修订，零设计变更：落地 R24 唯一建议 F-61——§3.8 自包含 cursor 段补续页身份显式标记（回传上一响应修正 cursor；无标记 = 首包携带 anchorRefresh / 有标记 = 续页不携带），re-freeze 冻结清单随全集复查集请求字段形状一并冻结；登记元审核更正记录——R24 报告锚点表 F-52 行两处子行号偏 1（注释块 199-204 应为 200-205、turnIDs := make 205 应为 206；同行 168/210 精确），方案侧引用（:168 与 :201-211 区间）经本轮亲核无误、无需修正，更正记录与影响限定见 §0 v29；交接块代码围栏闭合补齐（R24 附带观察）；历轮闭合项维持）；前版 v28（R23 APPROVED 后纠错修订，零设计变更：落地 F-60——§3.8 `changedTurns` 定义句来源限定词「baseDigests 中」同步为「复查集中」（v19 时代限定随 v27 全集复查集口径对齐，re-freeze 誊写随同一口径）；登记元审核更正记录——R23 报告来源表 U 行 Codex 提交哈希一位转写错误（`…cd55e237…` 应为 `…cd555237…`，对象库不存在的无效值）+ 失实「实测一致」声明，方案 §2.1 记录值经亲测 = 实际 main 无误，更正记录与影响限定见 §0 v28；历轮闭合项维持）；前版 v27（按 R22 评审修订：F-59——复查集定义统一为全集口径：原始 baseDigests 全部项 ∪ 已发项 token、每续页携带全量不收缩（原 §3.8「剩余未对账项」收缩限定与 §7.1.5 验收①全集口径定义了两个不同集合——按 §3.8 实现则验收①断言失败、按验收①实现则违反收缩限定；且收缩读法有真实漂移窗口：早页确认项页间被 live patch 修改时新终态只能靠恢复门 patch 交付、rev ≤ 采纳 head 走版本校验丢弃分支即丢内容 = R-7 假成功）；单调覆盖②复查对象从「已发项」扩为「复查集内全部项」、removedTurnIds 定义同步、验收②补「早页确认项页间被修改 → 复查捕获重发」构造；R21 已闭合项（F-57/F-58）维持、R20/R19 已闭合项经 R22 复核无回归；前版 v26（按 R21 评审修订：F-57——§7.3「重开增量路径」行的 v10 时代「anchor typed stale → window_0 回退」活文本更正为 FallbackFull（F-41 五处清扫漏掉的第六处，重试计数口径不变）；F-58——§3.8 确定性打包顺序补相位边界定案（turn 装不下即切页、本页剩余空间不回填、changedTurns 不得越过未交付 turns 提前装页）+ 后继 turn 交付验收③括注对齐（唯一合法页序列）；F-55 落地一致性——epoch hello_ack 锚点补 relay 路径（main.go:640，R21 亲核）；R20 已闭合项（F-54/F-55/F-56）维持、R19/R18 已闭合项经 R21 复核无回归；前版 v25（按 R20 评审修订：F-54——§7.1.5 组合验收④与 §7.3 指标行两处 v23 口径活文本同步到 v24 续页内容页规则（续页可含 turns 剩余片、每续页独立取 head、采用最后一页 head；typed stale 构造不再引用已撤回的 cut 概念）；F-55——对账事务 epoch 中止分支判定主体定案 iOS 侧（hello_ack epoch 比对 + 复查集兜底；续页请求不携带 epoch、Mac 侧不比对），re-freeze 冻结清单登记；F-56——复查集已发项被移除的处置显式化（经 removedTurnIds tombstone 返回、不重发）+ 自包含 cursor 验收②补构造；R19 已闭合项（F-48/F-52/F-53 方向与核心文本）维持；前版 v24（按 R19 评审修订：F-48 残留闭合——对账续页改内容页（不携带 anchorRefresh、可含 turns 剩余片 + changedTurns，turn phase 先于 correction phase、never-split 独占页、事务内 turns 计入超 2 页判定），对账事务内水位线 = 已交付 turn 链末端（解除 46MiB anchor + 后继 turn 死锁）；F-52 新增闭合——「同 kernel cut」宣称撤回，改为自包含 cursor（完整复查集随请求携带、每续页独立取当前 head、已发 token 失效重发覆盖、采用最后一页 head）+ 单调覆盖一致性论证（无历史版本存储 / 无跨请求 fence / 不变量 6 相容）；F-53 新增闭合——typed `full.oversized` 显式终态失败 + `projection_max_bytes` hello 能力化协商（默认 50MiB、提升档冻结定，contingency window 不是恢复路径）；R18 已闭合项维持；前版 v23（按 R18 评审修订：F-48 三处残留闭合——边界统一到独占额度口径（46MiB anchor 恰满合法 / 46MiB+1 超限，47/48 归超限判定，消除 §7.1.5「47MiB 总硬顶内合法」与 ≤46MiB 规则的判定二义）；anchorRefresh 携带规则定案为仅首包（修正续页不重复）+ 确定性打包顺序（mandatory 2MiB 预留 → anchorRefresh → turns → changedTurns 按 turnId 升序；首包对账空间 = min(8MiB, 48−2−anchorRefresh)）+ 续页同 kernel cut 绑定与对账完成事务一次性提交 + held 注册扩展到 changedTurns；组合验收 46+9/20+46/46+46 与打包确定性登记 §7.1.5；F-49/F-50/F-51 已闭合；前版 v22（按 R17 评审修订：F-48 预算边界补全（anchorRefresh ≤46MiB 为 envelope 留空间；changedTurn never-split 修正额度 + typed oversized；边界验收 8/20/47/48/48+1 登记）；F-49 组合路径与 §7.1.2 残留清理（「不整读」限定 window handler 冷检，重启全链默认整读）；F-51 Mac restart 指标行去重；F-50 已闭合；前版 v21（按 R16 评审修订：F-48 预算分层修正（响应总硬顶 48MiB，对账预算 8MiB 不含 anchorRefresh；OD-7 行登记新数值）；F-49 §3.2.3 尾部 IO 残留清理（(ii) 默认含 prefix 校验整读）；F-50 token 绑定 patch 全 ops 应用后的最终 turn；前版 v20（按 R15 复审修订：F-42 token 生命周期绑定不变量；F-44 对账完成事务显式采用 head + per-session 恢复门；F-45 撤回「两层兜底」——growth-trust 降级 OD-9 可选档；F-46 对账响应总预算 + changedTurns 分页；F-47 source-ledger 生产链纳入 S-2；前版 v19（按 R14 终审修订五阻塞：F-41 路径/回退/验收统一 + §3.9 路径总表；F-42 baseDigests 全基底对账契约（离线修改经 changedTurns 修正，live patch 只覆盖订阅后事件）；F-43 anchorRefresh 独立字段（刷锚与空页/预算解耦、超大锚不阻塞前进）；F-44 跨 epoch 原子采用事务（身份改写而非仅重置 appliedRev）；F-45 四态来源分类 + growth-trust（重启后 append 仍 suffix-only，等长改写登记为已知限制）；v29 全文 SHA-256 `0f975321372234bb1aa2fbc6cff2f50c9a73f872946a047a8bbf9e627f332494`，v28 全文 SHA-256 `dfafe1c6ceeb96ea86e284aeebe8c8bb712d47bb6b619c6e611ced5892ee8bbd`，v27 全文 SHA-256 `54e8203544385c50845a203a677386f7825b3b2c4a512f3f6ac4a1f1992f891e`，v26 全文 SHA-256 `f9063e95203558e93532ecb77fe71584c6e31d61952822b48e7a31eec06c5036`，v25 全文 SHA-256 `a3cdf1d2048f8ac74e62d76edeed62c9d0269275569609e05d17623aa62a11bc`，v24 全文 SHA-256 `4262ffd502363e6f37325d7eb443c8c73fb989cd34910d4d4e9ce3b27a77bdc9`，v23 全文 SHA-256 `ad1dc6a670d66b2fddd18dda6a17665cc5d3e6b7386d25e511c6e1b7af9a6769`，v22 全文 SHA-256 `6a85455fc0ed0bd9ce21fa4509f2526b08f01f7cc15c9acdb7b4b2629880d4cd`，v21 全文 SHA-256 `ef1f60cf5f0313c5017aff346a27211d4e9b5bde731a9e4c8d636f6c11a6a539`，v20 全文 SHA-256 `ec8904b3f5d2e2dc96dc3b37f06ee525b375fda075c3cc67efce4f9060fce35b`，v19 全文 SHA-256 `87efb0fc42a203ed61747fe194d4ec2cc17e9a466be5a891de55aa2ab596f013`，v18 全文 SHA-256 `a2f1b507bba6ef6e5451b250cdb5634bd2388af96f2fde50be4e7b90e8304b52`，v17 全文 `d66b971fbbf005526587aec84f118d3b78faa642a4fa636f19b8bc222b8d0ad0`，v16 全文 `171fe27df8bf17f327f00844b4c67a3a12ada700db4167831aabb15439c51c43`，v15 全文 `689c531dce365ce3d43b148aa21aab0f62fed43410fa515a5800c70ace539993`，v14 全文 `0209c6d8b3ece40bd80d0c84914c41a8d2d82ab381e111ee1ff15fcab2493da4`，v13 全文 `01633fc9b98cd95a9aed0abf3af640ad91a327d2dadba5ff6d922834a81ecdc2`，v12 全文 `732b47537c4abd591704301845a4c9233661d1aad86fb62fd549951d23122261`，v11 全文 `c1b2162556b72ef0742b5fbd586dbc51eb6c07eb980614b3d4d891f5831ce34e`，v10 全文 `e90f17a50e5c13103b8f4287c60ae01d779477a158de69ccd5e658477e251ddc`，v9 全文 `5189f17ccb2b017cee0a8b77c70da4655ee5cd5b78e63fc3ec0aa74a6649255b`，v8 全文 `a985e330153d8b180ee4d192d86613c3dff9593a6e501021ca9e466d98d21af3`，v7 全文 `4c598fb00112908c5269c4197c8ea0fe2a312cd30c24f83d350a8da6ef8ef3f4`，v6 全文 `a4d360225a0c5747298728c297ad7a1108eae77357f8b08023c3522d623262ee`，v5 全文 `4840baac4a6cbc921d4d7fd8440911b586b58628102b15fc7484d74490f5cd63`，v4 全文 `ef386fdff0d154b6df170dc99177a26bf23f871a3254679ea1a94b6caed5e6fc`，v3 全文 `0dda4476301d2d0e4e9bfc9ac0304a0b2c4def4ba129f0bec8f3c631a018e24a`，v2 全文 `9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18`，v1 全文 `35fb3d333e220deb6f4fc8e0fc53ad7927c72557879dc97b0214c28cbf04fbfb`）
状态：**已通过（R25 判 APPROVED：零阻塞；两条 META-FAIL 经 R25 复现定性为报告记录缺陷，方案侧无阻塞设计缺陷。本版为通过后落地建议 F-62（零设计变更措辞对齐）。方案阶段闭合，实施受 §5/交接块所列证据/决定/冻结/授权门约束**；本版不构成实施授权
范围：Claude / Claude Code 的 session_sync_v2 投影打开链路；后续再推广到其它 backend

## 0. v30 修订范围（R25 APPROVED 后落地 F-62：首包预算句计量措辞对齐，零设计变更）

R25（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r25.md`）判 **APPROVED（0 阻塞 + 1 建议 F-62）**，直接批准 v29 快照本身（非以「通过后低风险修改」代替评审）：逐行重核锚点表、重演 F-42/F-44/F-48/F-52/F-53/F-59/F-61 组合行为、两次 META-FAIL 均复现并定性为**报告记录缺陷**（R23 的 U 提交转写错误、R24 的子行号偏移——受影响技术依据已用有效来源重建），方案侧无阻塞设计缺陷。本版沿用 v18/v28/v29 先例做**通过后落地建议**，零设计变更：

| 事项 | 级别 | 修订处 |
| --- | --- | --- |
| F-62 [建议]（R25）：§3.8 打包算法第 2 步「turns → changedTurns；**三者合计**受本页对账空间约束」——「三者」字面把 anchorRefresh 又计了一次对账空间，与同节「anchorRefresh 不计入」的明确定义、§7.1.5 边界验收（46MiB anchor 合法、首包对账空间 0）及 OD-7/§7.3 分层计量冲突；R25 判定为措辞建议（不重开 F-48、不阻塞通过），闭合标准 = 该句不再把 anchorRefresh 计入已明确排除它的对账空间，**可随冻结誊写同步、无需再启动方案评审循环** | 建议 | §3.8 打包第 2 步改写为分层计量表述：**turns 与 changedTurns 使用本页剩余对账空间 = min(8MiB, 48MiB − 2MiB − anchorRefresh 序列化大小)；scalar/metadata 按已定义预算计入，anchorRefresh 不计入对账预算——另按独占额度与总硬顶计量**；额度、顺序、续页行为与验收全部不变（修订处随行注明 v30/F-62 与原措辞差异） |

本章之外仅做必要一致性维护（版本行/状态行/交接块）。

### 0.1 v29 修订范围（历史，R24 APPROVED 后纠错：落地 F-61 + 登记元审核更正记录 + 补齐交接块围栏）

R24（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24.md`）判 **APPROVED（0 阻塞 + 1 建议 F-61）**——v28 纠错修订自身（F-60 落地质量 + 元审核更正记录事实基础）独立复核通过、历轮闭合项无回归；元审核（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24-meta.md`，**confirm = false**）发现 R24 报告锚点表一处行号精确性缺陷（不影响 R24 技术判定，见下表影响限定）。本版为**通过后纠错修订**（沿用 v18/v28 先例）：落地唯一建议 + 登记更正记录 + 顺手补齐 R24 附带观察的排版事项，**零设计变更**（全部切片、门、不变量、定案与 v28 一致）：

| 事项 | 级别 | 修订处 |
| --- | --- | --- |
| F-61 [建议]（R24）：对账续页请求的「续页标记」未显式列入请求字段清单——§3.8 自包含 cursor 的请求侧仅 `anchorTurnId` + 复查集，续页请求与全新对账首包在 Mac 侧不可区分，「续页不携带 anchorRefresh」缺 Mac 侧判定输入；行为已被规范句 + 组合验收④双锁定（不构成实现/测试判定二义），空白仅在 wire 形状层（与 F-55 同型） | 建议 | §3.8 自包含修正 cursor 段补**续页身份显式标记**：续页请求回传上一响应的修正 cursor（v20 F-46 原案的请求侧重申），Mac 以该标记区分首包与续页（**无标记 = 全新对账事务首包，携带 anchorRefresh；有标记 = 续页，不携带 anchorRefresh**）；re-freeze 冻结清单登记「续页身份标记字段随全集复查集请求字段形状一并冻结」——冻结评审可直接写出首包/续页两种请求形状 |
| **元审核更正记录（r24-meta 问题 1；本轮亲测复核确认）**：R24 报告亲核锚点表 F-52 行（r24.md:32）两处子行号偏 1。**原错误**：报告引用「`199-204` 注释 …permanently strand the detail…Completion failure rolls the set back.」与「`205: turnIDs := make(...)`」。**来源**：元审核报告 r24-meta §三问题 1 + 本轮独立亲测（`sed -n 196,212p` + `grep -n`）。**旧/新身份**：注释块旧（报告）199-204 → 新（实际）**200-205**（`// turn_detail_lazy_v1 rule 3/4…` @200；被引句尾「Completion failure rolls the set back.」在 **205**（旧引用区间之外），199 为空行）；`turnIDs := make` 旧（报告）205 → 新（实际）**206**（205 为注释行）；同行其余锚点 `168: BeginProjectionSnapshot(` 与 `210: CompleteProjectionSnapshotWithHeldTurns(...)` 精确吻合。**影响范围**：该行全部语义声明（fence 生命周期 = 单请求内、held 现状只注册 `response.Turns`、注释原文存在）在修正行号后均成立；**方案侧锚点经本轮亲核全部无误、无需修正**——方案引用为区间 `projection_window_handler.go:201-211`（覆盖注释尾 201-205（含被引句 @204-205）+ turnIDs 构造 206-209 + 完成调用 210-211）与 `:168`，历轮方案文本从未逐行引用 199-204/205；**R24 技术判定（APPROVED）不受动摇**，缺陷限于报告引用精确性 | 更正记录 | 更正记录入册于本表；R24 报告本体按时代性记录纪律不回写，错误以本更正记录为准据；后续引用 `projection_window_handler.go` 该代码块行号以实际为准（注释 200-205 / turnIDs 构造 206-209 / 完成事务 210-211；方案区间引用 `:201-211` 维持有效） |
| R24 附带观察（不计入意见数）：方案以交接块 `open_gates` 行结尾，代码围栏未闭合、无尾随换行——纯排版事项 | 排版 | 交接块代码围栏闭合 + 尾随换行补齐（不影响正文哈希切分规则） |

本章之外仅做必要一致性维护（版本行/状态行/§10.33/交接块）。

### 0.2 v28 修订范围（历史，R23 APPROVED 后纠错：落地 F-60 + 登记元审核更正记录）

R23（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23.md`）判 **APPROVED（0 阻塞 + 1 建议 F-60）**——F-59 闭合成立、历轮闭合项无回归；元审核（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23-meta.md`，**confirm = false**）发现 R23 报告来源清单一处来源身份记录缺陷（不影响 R23 技术判定，见下表影响限定）。本版为**通过后纠错修订**（沿用 v18 于 R13 APPROVED 后的先例）：落地唯一建议 + 登记更正记录，**零设计变更**（全部切片、门、不变量、定案与 v27 一致）：

| 事项 | 级别 | 修订处 |
| --- | --- | --- |
| F-60 [建议]（R23）：`changedTurns` 定义句来源限定词「baseDigests 中」为 v19 时代限定（当时复查集 = baseDigests 本身、无二义），未随 v27 全集复查集口径同步——与单调覆盖②「Mac 对复查集内全部项比对」字面张力，冻结誊写 changedTurns 字段语义时需读者自行调和；定级建议的依据（R23）：全部相关验收断言的是行为（重发终态）而非字段来源，字段句无法被单独实施为规范（被机制句 + 验收②双重锁定） | 建议 | §3.8 `changedTurns` 定义句限定词改「**复查集中**」（v28 措辞同步注记随行，载体范围随之明确 = 复查集全部项的当前终态 upsert，含已发项与早页确认项的页间修改）；re-freeze 冻结的 changedTurns 字段语义随同一口径誊写（冻结门登记） |
| **元审核更正记录（r23-meta 问题 1；本轮亲测复核确认，见下「旧/新身份」）**：R23 报告来源表 U 行（Codex 上游）记录完整提交为**无效哈希**并附失实一致性声明。**原错误**：报告 U 行记录 `ce0323f2f2d3b25e82806cd55e237da3db4e1897` + 注「（= 方案 §2.1 记录值，本轮实测一致）」。**来源**：元审核报告 r23-meta §三问题 1（实测证据链）+ 本轮独立复核。**旧/新身份**：旧（报告转写，无效）`…82806cd55e237…` → 新（实际 main，本轮亲测 `git -C /Users/jacklee/Projects/codex rev-parse main` = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git cat-file -t` → `commit`；报告值 `cat-file -t` → `fatal: could not get object info`，第 26 位 `5` 被误写为 `e`）。方案 §2.1（:387）记录值 = 实际值——**方案侧来源身份无误、无需修正**，错误仅在 R23 报告转写，其「实测一致」声明按其自身字符串为假（违反 P0 来源门「路径+分支+完整提交为不可拆分来源身份」）。**影响范围**：U 为只读架构参考、R23 自述「本轮未产生依赖 U 的新结论」、元审核锚点抽查 11/11 通过——**R23 技术判定（APPROVED）不受动摇**；缺陷限于报告来源身份记录本身 | 更正记录 | 更正记录入册于本表；§2.1 Codex 上游行加更正注记（本行 = 唯一权威 U 来源身份：`main@ce0323f2f2d3b25e82806cd555237da3db4e1897`；后续引用 U 来源身份一律以 §2.1 为准，不得转引 R23 报告 U 行）；R23 报告本体属历轮评审记录、不由本方案修订（时代性记录纪律），错误以本更正记录为准据 |

本章之外仅做必要一致性维护（版本行/状态行/§10.32/交接块）。

### 0.3 v27 修订范围（历史，按 R22 评审）

按 R22 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r22.md`）修订。R22 判定 REVISION_REQUIRED：R21 两条（F-57/F-58）**全部判闭合**；R20 三条（F-54/F-55/F-56）与 §10.28 三点**维持闭合、无回归**；v26 新增 relay epoch 锚点亲核属实。新增 **F-59（阻塞——v24 自包含 cursor 文本的旧漏检，R20/R21 未检出，非 v26 回归）**。经本轮亲核（两处文本 grep 实证 + 漂移窗口机理独立推演）后**采纳修订方向①（全集口径，评审推荐）**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-59 [阻塞]（v24 文本旧漏检）：复查集定义两处不一致——§3.8 规范文本「完整复查集 = 原始 baseDigests **剩余未对账项** ∪ 已发项 token」（收缩口径）vs §7.1.5 验收①「完整复查集（**原始 baseDigests ∪ 已发项 token**）」（全集口径）——两个不同集合：页 1 复查命中的项按收缩读法移出后续请求，按 §3.8 实现则验收①断言失败、按验收①实现则收缩限定被违反。且收缩读法存在真实漂移窗口：早页确认项页间被 live patch 修改时，既不在复查集（不会重发）、也不是本事务已发项（单调覆盖②不适用）、其新终态不在任何对账页内——唯一交付载体是恢复门排队的 patch，而该 patch rev ≤ 采用事务采纳的最后一页 head（修改发生在末页快照之前），走 §3.4.3「或按 rev 版本校验丢弃重排」合法分支即丢内容、projection 声称已达 head、syncing 已清 = R-7 假成功 | P1 | 采纳全集口径（严格更安全、成本仅每页多比对 ≤256 个 token，且与验收①现有文本一致）：① §3.8 自包含修正 cursor 定义改「原始 baseDigests **全部项** ∪ 已发项 token——复查集**不随页收缩**，每续页携带全量」（F-59 漂移窗口机理随更正注记入册：全集复查下早页确认项页间被修改由当页复查捕获重发、末页快照后到达的 patch rev > head 按版本校验正常应用，对恢复门两个实现分支均稳健）；② 单调覆盖②复查对象从「已发项」扩为「复查集内**全部项**」（token 失效即重发，无论是否早页确认）；③ removedTurnIds 定义同步（「复查集中**任何项**被移除同经本字段返回」）；④ §7.1.5 验收①不变（已是全集口径），验收②补「早页复查命中（确认无修改）的基底项在续页请求前被 live patch 修改 → 续页复查集仍含该项 → token 失效 → 重发新终态」构造；§0.2 v24 历史表的收缩口径表述按时代性记录纪律保留不改 |

本章之外仅做必要一致性维护。

### 0.4 v26 修订范围（历史，按 R21 评审）

按 R21 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r21.md`）修订。R21 判定 REVISION_REQUIRED：R20 三条（F-54/F-55/F-56）**全部判闭合**；§10.28 三点（死锁解除 / 自包含 cursor 单调覆盖 / 两级 typed 链）独立复演成立；R19/R18 已闭合项无回归。新增 **F-57（阻塞，v10 时代活文本漏检——非 v25 回归）**与 **F-58（建议）**。另按 R21 亲核锚点表补一处 F-55 落地一致性锚点。经本轮亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-57 [阻塞]（v10 时代活文本，v19 F-41 五处清扫漏掉的第六处）：§7.3「重开增量路径」行仍写「anchor typed stale → `window_0` 回退属合法回退不计重试」——与 F-41 统一后的 FallbackFull 规则五处（§3.8 typed stale / §3.9 总表 / §7.2.14 / §7.4 场景 14 / §8 回退边界）及 §7.3 表内自身两行（full projection open / 重试次数）正交冲突：按该行写测试会断言 typed stale 触发 `window_0` 请求（回归 v14 前形态，且 window_0 无默认 scalar 补齐），按其余五处实现则断言必失败——实现与测试无法同批判定 | P1 | §7.3「重开增量路径」行更正为「anchor typed stale → **FallbackFull（全量冷路径，§3.9）** 回退属合法回退不计重试」（v26 更正注记随行；重试计数口径不变——合法回退不计重试的语义保留，仅回退目标对齐 §3.9）；§0/§10 历史区不动 |
| F-58 [建议]：§7.1.5 后继 turn 交付验收③括注「首包 4MiB 空间按序装更小的对账内容」不可由 §3.8 确定性打包算法（「按序填充、装不下即切页」+「同页内 turns 先于 changedTurns 填充」）推出——自然读法 = turn phase 有未决项时切页、首包剩余空间不回填、changedTurns 不得越过未交付 turns 提前装页；一般场景（42MiB anchor + 6MiB 后继 + changedTurns 并存）下「回填」与「不回填」两种读法产出两个不同页序列，与「同一输入只允许一个合法页序列」的确定性宣称相悖，打包确定性断言⑤无法裁决 | 建议 | 采用评审修订方向②：§3.8 确定性打包顺序**显式定案相位边界**——turn phase 内某 turn 装不下当前页剩余对账空间时本页 turn phase 即终止（该 turn 溢出至后续页），**当前页剩余空间不回填**：correction phase（changedTurns）不得越过未交付的 turns 提前装页（即使存在能装入的更小项）；turns 全部交付完毕后 changedTurns 才按序进入（可与末个 turns 同页）；§7.1.5 验收③括注对齐「不回填、切页即止」（v26 更正注记随行） |
| （R21 亲核锚点表补充，随 F-55 落地一致性采纳）方案 §3.8 单调覆盖④的 epoch hello_ack 锚点只引 direct `server.go:780`，R21 实测 relay 路径同样携带（`main.go:640` `ack.BridgeEpoch = bridgeEpoch`，本轮亲核确认）——iOS 生产 Relay 路径的判定可执行性宜一并入册 | 一致性 | §3.8 单调覆盖④ epoch 锚点补 relay 路径（`main.go:640`） |

本章之外仅做必要一致性维护。

### 0.5 v25 修订范围（历史，按 R20 评审）

按 R20 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r20.md`）修订。R20 判定 REVISION_REQUIRED：R19 三条阻塞（F-48 残留 / F-52 / F-53）的**修订方向与核心文本均判闭合**，但 v24 修订自身引入一处回归 **F-54（阻塞）**；另两条建议 F-55/F-56。R18 三处 + F-49/F-50/F-51 经 R20 复核无回归。三条经本轮亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-54 [阻塞]（v24 修订引入的回归）：续页改内容页 + 撤回「同 cut」后，两处 v23 口径活文本未同步——§7.1.5 组合验收④仍断言「全部续页响应 anchorRefresh/**turns** 字段为空或省略、head/cut 标识与首包一致（**同 cut 续页**）」并以「**cut 失效**」构造 typed stale；§7.3 指标行仍写「（**续页只含 changedTurns 剩余片**）」且版本标记停在「v23 F-48 终版」——与 §3.8 打包 3（续页可含 turns 剩余片）/ 每续页独立取当前 head / 采用最后一页 head / OD-7 行直接冲突，实现与测试无法同批判定（按 §3.8 实现则断言必失败；按断言实现则 R19 F-48 判定的死锁形态回归） | P1 | ① 组合验收④改写（v25）：断言收窄到 **anchorRefresh**（turns 剩余片与 changedTurns 剩余片按 turn phase → correction phase 出现不属违例）；head 口径改「每续页独立取当次 committed head、采用事务以最后一页 head 为准」；typed stale 构造对齐自包含 cursor 验收③（anchor 离开保留区），不再引用已撤回的 cut 概念；② §7.3 行删「续页只含 changedTurns 剩余片」、续页形状对齐 v24（可含 turns 剩余片）、版本标记改「v24 F-48 终版」、补后继 turn 交付用例引用；§0 历史表与 §10.27 历轮评审项按时代性记录纪律保留不改 |
| F-55 [建议]：自包含 cursor 的「epoch 变化 → typed stale」分支缺请求侧可执行路径——续页请求字段仅 `anchorTurnId` + 完整复查集（无 epoch 身份），Mac 无从比对 epoch，该分支的 Mac 侧可执行性悬空，冻结时实施者需自行发明携带方式 | 建议 | §3.8 单调覆盖④显式定案**判定主体**：anchor 保留区 = Mac 侧 typed stale（判定输入随请求携带）；**epoch 中止 = iOS 侧判定**（bridgeEpoch 是 runtime 启动身份、epoch 变化必伴随连接断开——iOS 重连后经 hello_ack 取得当前 epoch，与对账事务开始时记录值不一致 → 整组放弃 FallbackFull）；**Mac 侧不比对 epoch、续页请求不携带 epoch 身份**；iOS 判定遗漏时由复查集幂等 + 最后一页 head 收敛兜底（正确性不受损）；并与 §3.4.2 打开时点「epoch ≠ 当前：流程照常」分支作时点/后果区分；re-freeze 批次清单显式登记该定案（冻结时写明语义，实施者无需自行发明） |
| F-56 [建议]：复查集「已发项被移除」的处置未显式写出——单调覆盖②只写 token 失效重发；`removedTurnIds` 定义只覆盖 baseDigests 项；事务中途 compaction 移除非锚点 turn 而 anchor 仍在保留区（不触发 typed stale）时，已发 turn 的正确处置是 removedTurnIds 而非重发，文本未连接两点 | 建议 | 单调覆盖②补「**已发项不在 kernel 当前投影 → 经 `removedTurnIds` 返回 tombstone（iOS 从基底移除），不重发**」；`removedTurnIds` 定义补「复查集中已发项被移除同经本字段返回」；自包含 cursor 验收②补「事务中途 compaction 移除非锚点 turn（anchor 仍在保留区）」构造 |

本章之外仅做必要一致性维护。

### 0.6 v24 修订范围（历史，按 R19 评审）

按 R19 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r19.md`）修订。R19 判定 REVISION_REQUIRED：R18 的 47MiB 二义 / 续页重复 anchor / 简单组合已闭合，F-49/F-50/F-51 维持闭合；**F-48 仍未闭合**（turns 无交付路径），**新增 F-52/F-53**。三条经本轮亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-48 残留：46MiB anchor 使首包对账空间为 0，而对账续页又明确不携带 turns——anchor 后还有任何新 turn 时 turns 首包放不下、续页不能送、水位线无法前进（下一请求仍以同一 46MiB anchor 开始 = 死锁；后继 turn 交付是水位线前进的正常主路径，非仅 changedTurns 边界） | P1 | 对账续页改写为**内容页**：不携带 anchorRefresh（anchor 开销只付一次），**可携带 turns 剩余片**（turn phase：严格后继、R7 链序、never-split 独占页）**与 changedTurns 剩余片**（correction phase，turnId 升序；同页 turns 先填）；**对账事务内水位线 = 已交付 turn 链末端**（§3.8 分页前进改写——46MiB anchor + 1KiB 后继由续页交付后水位线前移，死锁解除）；事务内 turns 页数计入「超 2 页退全量」；§7.1.5 后继 turn 交付验收（46+1KiB / 46+9MiB / 42+6MiB）；状态机/held 注册/§3.9 同步 |
| F-52 新增：「续页与首包同 kernel snapshot cut」不可实现——现有 `BeginProjectionSnapshot` 每次取当前 `kernel.Snapshot`（`projection_kernel.go:1887-1902`）、fence 完成即释放不跨请求持有（`projection_window_handler.go:168-210`）、bbolt `turns/items` 均为当前行无 (cutRev, turnId) 历史版本（§3.2.2）；共享 cut artifact / 版本化存储未设计 | P1 | **「同 cut」宣称撤回**（共享 artifact / 版本化 rows 经比较否决：生成/TTL/去重/崩溃恢复均为新增重机制），改为**自包含 cursor + 单调覆盖 + 已发 token 复查**：续页请求携带完整复查集（原始 baseDigests 剩余 ∪ 已发项 token，含 anchorRefresh turn 与已交付 turns）；每续页独立取当前 committed head（复用现有每请求快照机制——不跨请求 fence、不需要历史版本存储、Mac 无 per-device 状态与不变量 6 相容）；已发项 token 失效（页间 live patch）→ 重发当前终态 upsert 覆盖（幂等）；采用事务以最后一页 head/scalar 为准——单调覆盖一致性论证（kernel 终态投影单调 + 复查 + 覆盖）替代同 cut；§7.1.5 自包含 cursor 验收（重放幂等 / 两设备不串扰 / 跨 cut 复查 / anchor stale 中止） |
| F-53 新增：>46MiB 项回退 FallbackFull，但 full projection 可能超 iOS 50MiB 逻辑消息/解压硬顶（gzip 只压网络不消解压上限）——不可传输的 full 被写成恢复成功路径 | P1 | §3.8 新增**超限恢复语义**两级定案：① **typed `full.oversized` 显式终态失败**（full 序列化 > `projection_max_bytes` → iOS 显式失败态，不假成功不部分渲染；两级 typed 链：reconciliation.oversized → FallbackFull → full.oversized）；② **能力化上限协商 `projection_max_bytes`**（hello，direct/relay 同值提升；默认 50MiB 维持、提升档 S-7/S-8 冻结定，数值归 OD-7；内存峰值由 S-5 承接；contingency `window_0` 同受上限、不是恢复路径）；§8 风险表新增行、R-2 双失败线改写、E-11 full 超限反例、§7.1.5 full 超限验收（解压拒绝不得计入恢复成功指标） |

本章之外仅做必要一致性维护。

### 0.7 v23 修订范围（历史，按 R18 评审）

按 R18 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r18.md`）修订。R18 判定 REVISION_REQUIRED：**F-49/F-50/F-51 闭合**；F-48 三处残留（47MiB 边界冲突 / 修正续页 anchorRefresh 携带规则未定义 / 缺组合验收与打包顺序）。经本轮亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-48 残留：§7.1.5「47MiB anchor（总硬顶内合法）」与 §3.8/OD-7「anchorRefresh ≤46MiB 超即 oversized」直接冲突（47 同时满足两条规则，判定二义）；修正续页是否重复携带 anchorRefresh 未定义——46MiB anchor + 9MiB changedTurn、20MiB anchor + 46MiB changedTurn 等合法组合每页超 48MiB 总硬顶、不可实现；缺大 anchor + 大 changedTurn 组合验收与确定性打包顺序 | P1 | ① 边界统一到独占额度口径：**46MiB anchor 恰满合法**（46 + mandatory 2MiB = 48 恰满总硬顶，首包对账空间 0 → 全部 correctionsPending）、**46MiB+1 超限**（47/48 同判定 → typed oversized → FallbackFull）；② §3.8 增**确定性打包算法**（mandatory 2MiB 预留 → 首包 anchorRefresh（唯一一次）→ turns → changedTurns 按 turnId 升序；首包对账空间 = min(8MiB, 48−2−anchorRefresh)；修正续页**不携带 anchorRefresh/turns**、普通片 8MiB、单个 >8MiB changedTurn 独占页 ≤46MiB）+ **续页绑定规则**（同 kernel cut、对账完成事务全部到齐一次性提交、续页 typed stale → FallbackFull 无部分状态泄漏）；held 注册扩展到 changedTurns；③ §7.1.5 组合验收 46+9 / 20+46 / 46+46 + 修正页不重复 anchor 断言 + 打包确定性断言；OD-7/§7.3/§3.4.2 状态机同步 |

本章之外仅做必要一致性维护。

### 0.8 v22 修订范围（历史，按 R17 评审）

按 R17 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r17.md`）修订。R17 判定 REVISION_REQUIRED：**F-50 闭合**；F-48/F-49 残留；F-51 建议。三条经本轮亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-48 残留：anchorRefresh ≤48MiB 与全字段合计 ≤48MiB 未给 envelope/scalar/metadata 留空间；单个 changedTurn 超 8MiB（对账预算）但 < 48MiB 时无法分页且未定义 typed oversized/FallbackFull；R16 要求的 8/20/47/48/48+1 边界验收未登记 | P1 | §3.8 预算再修正：**anchorRefresh ≤46MiB**（为 envelope/scalar 留 ≥2MiB）；**changedTurn 同样 never-split**——单个 changedTurn 可独占修正页（单 turn ≤46MiB，超出 → typed oversized → FallbackFull），分页按整 turn 粒度不截断；**边界验收 8/20/47/48/48+1 MiB 用例登记 §7.1.5**；OD-7 行同步 |
| F-49 残留：§3.2.3 组合路径仍写「S-2 后 append 后 window_0 不整读 97MiB」（与默认 source-ledger prefix 整读冲突）；§7.1.2「断言不整读 transcript」未限定范围 | P1 | 组合路径段改写：S-2 后该路径 IO = **重启默认含 prefix 校验整读（source-ledger）**+ 冷检 + suffix 重投影（OD-9 启用后才为 stat+4KiB+suffix）；§7.1.2「不整读」限定为 **window handler 冷检路径**（stat + 4KiB 反扫不整读），完整生产链（含 source ledger `Observe`）按 §3.2.3 (ii) 如实计 |
| F-51 §7.3 Mac restart 首开指标出现两行（v20 改的一行 + 残留旧行），易只改一处漂移 | 建议 | 去重合并为单行（v20 口径） |

本章之外仅做必要一致性维护。

### 0.9 v21 修订范围（历史，按 R16 评审）

按 R16 评审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r16.md`）修订。R16 判定 REVISION_REQUIRED：**F-42/F-44 闭合；F-45 以 OD-9 pending + 安全默认档闭合**；F-46/F-47 方向正确但分别被 F-48/F-49 残留阻塞；另增建议 F-50。三条经本轮亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-48 对账响应预算自相矛盾：正文要求四字段合计 ≤8MiB 却允许 anchorRefresh 单 turn 48MiB——20MiB anchor 同时「合法」（≤48）和「越界」（>8），实现与测试无法判定；OD-7 行未登记 8MiB/48MiB | P1 | §3.8 预算改**分层结构**：响应总硬顶 **48MiB**（全字段合计，< 50MiB transport 解压上限留余量）；其内两层——anchorRefresh（never-split 特例，独占额度，超 48MiB → typed oversized → FallbackFull）+ **对账预算（turns + changedTurns + scalar）≤8MiB**（anchorRefresh **不计入** 8MiB）；§7.3 行同步；**OD-7 决策行补登 8MiB/48MiB**（数值变更，恢复 pending 微调口径） |
| F-49 §3.2.3 结尾残留 v19 的「(i)/(ii) = stat + 尾部 4KiB + suffix」零整读结论，与同节 (ii) 默认 source-ledger prefix digest 整读矛盾——S-2 可能按残留行跳过校验，或把生产必需的 97MiB 读量判失败 | P1 | §3.2.3 尾部 IO 上界行改写：(i) 常驻 = suffix-only；(ii) 重启默认 = **prefix 校验整读（≈ transcript 大小）+ suffix 重投影**（OD-9 启用后 = stat + 4KiB + suffix）；(iii) 全等 = stat；(iv) = digest 裁决 + 重建 |
| F-50 同一 patch 内 upsertTurns + turnStateOps + partOps 可能产生「最终内容 + 中间版本 token」 | 建议 | §3.8 token 规则第 2 条精确化：**token 只能绑定该 patch 全部 ops 应用后的最终 turn 状态**（upsertTurns 提供基底 → stateOps/partOps 就地修改 → 以最终结果计算 token 或置 null）；§7.2.13 补 mixed-patch 测试 |

本章之外仅做必要一致性维护。

### 0.10 v20 修订范围（历史，按 R15 复审）

按 R15 复审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r15.md`）修订。R15 判定 REVISION_REQUIRED：**F-41/F-43 闭合**；F-42/F-44/F-45 未完全闭合；新增 F-46/F-47（均为 P1）。核心纠偏：**v19 的「baseDigests 与 growth-trust 互相兜底」不成立——两者共享同一 Kernel，Kernel 恢复错误时两层皆错**（反例：transcript prefix 被等长改写 + 合法 suffix append，membership 不变、growth-trust 恢复 A旧、iOS 回传 A旧 digest、Mac 从 A旧算 digest → 匹配 → changedTurns 空 → 假成功）。五条经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-42 digest 未定义与缓存内容的全生命周期绑定（patch 的 partOps/turnStateOps 局部修改后 token 语义缺失；退后台直接保存活跃 replica；种子/full/patch/对账/迁移各来源未覆盖） | P1 | §3.8 定义 **token 生命周期绑定不变量**：(内容, token) 必须来自同一权威交付；patch 局部修改后无新 token → **该行 token 置失效（null）**，对账请求 null → Mac 必须回传当前终态+新 token（re-freeze 可选优化：patch 附带受影响 turn 新 token）；写盘保存 (内容, token, tokenValid)；E-11 增两组反例 |
| F-44 零新增页仍留 rev=0（"空页保持 0 直到下一条 patch"与新页携带 head 矛盾，混合身份仍在）；@MainActor 只串行同步片段，`await replica/presentationEngine`（`ProjectionStore.swift:612-619/:1180/:1197-1203`）期间 patch 可重入 | P1 | §3.4.3 采用事务补**第二阶段**：无论 turns 空否，对账完成事务显式把 projection epoch/head/scalar/修正集一次性发布（恢复专用 apply 语义，不复用空页 `return self` 分支）；**per-session 恢复事务门**（跨 await 显式持有）：恢复期间 patch/recovery 入口排队或版本校验丢弃；§7.2.12 增 await 边界插 patch 反例 + 零新增进 head 断言 |
| F-45 growth-trust 与 baseDigests 共享同一 Kernel，不互相兜底；`§7.1.2 "prefix 改写且长度变化→(iv)"` 与增长进 (ii) 冲突；接受静默旧数据 = R-7 产品取舍，不能由工程修订默认授权 | P1 | **撤回「互相兜底」结论**（§3.2.3/§8）；growth-trust 降级为 **OD-9（pending，owner 裁决 R-7 例外取舍）**可选档；默认重启路径保留 source-ledger prefix digest 校验（整读成本如实登记，见 F-47）；分类修正：等长+mtime 变 → 可疑走校验；增长 → 默认校验（OD-9 启用后方可跳过） |
| F-46 changedTurns 无总预算：缓存旧 turn 现在膨胀（64×1MiB ≈ 64MiB）可突破有界重开与 **50MiB transport 解压上限**（`BridgeWebSocketConnection.swift:12`、`RelayBridgeFrameConnection.swift:823/:859-862`——gzip 不取消解压上限） | P1 | §3.8 定义**对账响应总预算**（拟议 8MiB，OD-7 一并裁决）：turns+anchorRefresh+changedTurns+scalar 合计；超预算 → changedTurns 分页（`correctionsPending` + cursor，anchor 不变续拉）；**部分修正未完成不得清 syncing/判已对账**；单 turn 超 48MiB（<50MiB 留余量）→ typed `reconciliation.oversized` → FallbackFull；去重（同 turnId 多处 → 合并）；§7.3 指标改总载荷口径；E-11 增膨胀反例 |
| F-47 source generation 生产链全读 prefix：`claudeSessionFileRelay` 启动即 `Observe`（`handlers_relay.go:882-884`），`digestClaudeSourcePrefix:122` = `io.CopyN` 全 prefix——**不受 trace 开关保护**；S-2 只测 bbolt loader 会漏掉它 | P1 | **S-2 范围扩展**：source ledger/correlation 的生成、持久化、恢复与 live relay 启动纳入交付与测试（真实 owning 调用链全文件读量：store 恢复→source ledger→relay 启动，trace 开/关均可解释）；§3.2.3 IO 上界如实改写；**撤回「完整路径 suffix-only 已证明」表述**；R-5 精确化：「append 不触发全量**重投影**」≠「零 prefix 校验读」 |

本章之外仅做必要一致性维护。

### 0.11 v19 修订范围（历史，按 R14 终审）

按 R14 终审报告（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r14.md`）修订。R14 判定 **REVISION_REQUIRED（5 阻塞 F-41~F-45，均为 P1）**，推翻 R13 的 APPROVED——阻塞不来自 F-39/F-40 修订，而是 v14 转向后的规范残留（F-41）与 v10 恢复增量设计的既有漏检（F-42/43/44）及物化 IO 论证缺口（F-45）。五条经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-41 v14 定案与现行硬约束/回退/验收矛盾：不变量 3 仍写「首开不得返回整棵 projection」、§7.3 仍要求 full open = 0、四处回退条款仍写 window_0（不回 full）、Gate B 仍挂已取代的 OD-2——正确执行 v14 也必然违反这些条款 | P1 | 不变量 3 改「有界重开」；R-3 重开/冷路径分口径；§7.3 full open 行重定义；§3.8/§7.2.13/§7.4.12/§7.4.14/§8 五处回退统一 FallbackFull；Gate B 去 OD-2；**新增 §3.9 打开/回退路径总表**（触发/动作/能力门/返回形状/失败行为唯一化） |
| F-42 尾部 anchor 只能定位不能证明基底有效：window 路径走 `BeginProjectionSnapshot`（非 WithResume，`projection_window_handler.go:168`），fence 只保证 post-cut patch 顺序——**离线期间**对旧 turn 的修改不会被重放，「由 live patch 自愈」不成立；反例：缓存 [A旧,B旧,C]，离线 A→A新 且无后续事件，重开得 [A旧,B旧,C,D] 且 syncing 已清 | P1 | §3.4.2 关键点 5 重写（承认 live patch 只覆盖订阅后事件）；**§3.8 新增 baseDigests 全基底对账契约**：请求回传缓存全部 turns 的 Mac 生成 digest，响应新增 `changedTurns`（digest 不符的当前 upsert，覆盖离线修改）与 `removedTurnIds`（tombstone）；syncing 清除以对账完成为前提；E-11 增离线修改反例（比较内容终态非仅 turn ID 集合） |
| F-43 anchor 页同时承诺「含锚点刷新」与「零新增空页」矛盾；`growWindowRange(boundary+1)` 实为严格排他与「包含」相反；超大锚点（样本实存 >4MiB 单 turn）使分页不前进 | P1 | **§3.8 重构为 anchorRefresh 独立字段**：`turns` = 严格后继（helper 语义不变）；`anchorRefresh` = 锚点当前终态、**不计页 limit/字节预算**、never-split 单列计量；anchor==tail 时 turns 空但 anchorRefresh 仍携带终态（矛盾解除）；分页前进锚 = 页尾 turn（超大锚只膨胀 anchorRefresh 不阻塞）；OD-7 计量注明不含 anchorRefresh |
| F-44 跨 epoch 只重置 appliedRev 留下混合身份：`applyingWindowPage` 非空页取 `max(旧 syncRev, page.syncRev)`（`:173`）、空页 `return self`（`:123-126`）——projection 仍是旧 epoch/大 rev；`revisionKey = epoch\|syncRev`（`:696`）与 presentation（`:378/:390`）传播旧身份；后续 full 的 stale 比较会拒绝新 epoch 小 rev 权威投影 | P1 | **§3.4.3 第 2 条重写为「原子新 epoch 采用事务」**：以改写身份（bridgeEpoch=当前、syncRev=0）的副本执行 applyFull，appliedRev=0、coverage 清零同事务完成；缓存文件保留原 provenance 仅作显示分级；恢复期间 patch 隔离时序入册；§7.2.12 增 revisionKey/coverage/presentation 一致性与恢复中插 patch 反例 |
| F-45 (path,size,mtime) 快速匹配不能支撑「append 只读 suffix」：正常 append 改变 size/mtime → 快速匹配必失配 → 全 prefix digest 整读（`projection_kernel.go:407`），与「不整读约 97MiB」承诺冲突；恢复场景「保存→停止→append→重启」恰好触发 | P1 | **§3.2.3 改四态来源分类**：(i) kernel 常驻 = 零验证 suffix-only；(ii) **重启且 size 增长 = growth-trust**（信任 committed cursor 直折 suffix，不重读 prefix——Codex `next_byte_offset` 同型不变量；原地等长/增长型改写不检测，登记为已知限制，由段 membership/generation 变化检测 + iOS baseDigests 对账兜底）；(iii) size/mtime 全等 = 快速匹配零读；(iv) size 回退/membership/generation 变化 = 诚实失效重建；prefix_sha256 降级为争议裁决；§7.1.2 增「保存→停止→append→重启」实测读取字节区间测试；§7.3 source advance 行更新 |

本章之外仅做必要一致性维护。R13 的「方案阶段闭合」结论随本版撤销，重新进入评审。

### 0.12 v18 修订范围（历史，R13 APPROVED 后落地两条建议）

按评审报告 R13（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md`）修订。R13 判定 **APPROVED（0 阻塞 + 2 建议 F-39/F-40，均不改变实施动作）**；R12 三条（F-36/F-37/F-38）经 R13 核验全部闭合，六项设计不变量抽核保留，数字独立复算全部吻合，决策表 pending = 0。**按 repo 2026-09-20 评审循环纪律，方案阶段闭合，APPROVED 为开发阶段唯一入口。**本轮仅落地两条建议（沿用 v9 于 R9 后的先例），零设计变更：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-39 日志行号 `:324` 应为 `:326`（实测 `:324` 是 `continue`，`:326` 才是「收到完整消息」NSLog；`:334` 正确；`opcode.rawValue` 共 4 处，`:368/:376` 属发送侧 `sendFrame` 参数非读取路径消费点——语义本就准确，仅行号偏差） | 建议 | §6 S-8 组件列更正为「读取路径仅 `:326/:334` 日志」并附发送侧两处澄清 |
| F-40 §0 表称「共修 14 处」、交付说明称「17 处」——两数都是 v17 自己写的，文档内外不一致（本方案历轮可信度建立在「数字均可亲核」上，R9 F-28 同型） | 建议 | 统一为 **17 处 = 初批 14 + 收口复检补修 3**（R-6 行、§3.7「Gate A / S-1 前置」、§8 re-freeze 行），§0 v17 历史行加补注、版本行与交接块同步 |

本章之外**未改动任何设计内容**（全部切片、门、不变量、定案与 v17 一致）。

### 0.13 v17 修订范围（历史，按 R12）

按评审报告 R12（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md`）修订。R12 判定 REVISION_REQUIRED（**0 阻塞** + 3 建议 F-36~F-38，明示修完即达 APPROVED）；R11 的 F-31 定案与 F-33/F-34/F-35 闭合均经 R12 核验通过。三条意见经本轮独立源码亲核后**全部采纳**（无驳回项；F-36 承认 v16 把设计意图误写为现状描述）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-36 §3.6「client→Mac 恒为 text」与代码相反——iOS `send()` 现状即 `sendFrame(opcode: .binary, …)`（`BridgeWebSocketConnection.swift:304-306`）；Mac 读循环丢弃 opcode（`server.go:670` `_, raw, err := ws.ReadMessage()`），对 text/binary 一视同仁——错写不会导致功能失效（测不出来），但使冻结断言「未协商连接零 binary 帧」方向不明，且可能诱导实施者把 iOS `send()` 改成 text（无谓行为变更） | 建议 | §3.6 更正为：opcode 约束**只作用于 Mac→client 方向**（压缩只发生在该方向）；client→Mac **维持现状 binary、不在变更边界内、不得顺手改 text**；现状事实（含两组锚点）随行登记；冻结断言明确限定 Mac→client 方向 |
| F-37 F-32 清扫漏 3 处（E-3「S-1 修复前」、E-4「S-1 验收」、Gate A/§9 的「自动满足」与 E-10 行双口径并存）——同一问题第二次漏改 | 建议 | **grep 驱动彻底清扫**（`grep "S-1"` + `grep "自动满足"` 全量过一遍）：本轮共修 **14 处**活引用——E-3/E-4/E-8 阻塞对象→S-7/S-2、OD-4/OD-6 影响切片改指、OD-8 措辞去 S-1 语境、Gate A OD-2 条标记 v14 后失效、双门段/§8/§3.7 组合表/§2.5 边界声明去 S-1 语境、**3 处「自动满足」统一为「需护栏」口径**（§3.3/Gate A/§9）（**另有收口复检补修 3 处——R-6 行、§3.7「Gate A / S-1 前置」、§8 re-freeze 行——合计 17 处；v18 补注 F-40 统一计数**）；§0 历史表与 §10 历轮评审项的时代性记录保留（既定纪律） |
| F-38 S-8 未登记 iOS transport 改造——`receiveData()` 签名（`BridgeWebSocketConnection.swift:6/:71/:308`）在 API 边界丢弃 opcode（仅 `:324/:334` 日志），「binary→gunzip→与 text 深度相等」断言的前提（iOS 拿得到 opcode）未登记；好消息：iOS 已有 opcode 枚举（`:134-141`）且读循环已区分（`:311-330`） | 建议 | §6 S-8 组件列登记 iOS transport 改造（签名暴露 opcode，改造面小——枚举与读循环现成）；§7.2 新增第 17 条断言（分流 + 「client→Mac 维持 binary」负对照） |

本章之外未改动 v16 的任何设计内容。

### 0.14 v16 修订范围（历史，按 R11）

按评审报告 R11（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md`）修订。R11 判定 REVISION_REQUIRED（1 阻塞 F-31 + 4 建议 F-32~F-35）；R10 两条（F-29/F-30）经 R11 核验全部闭合；v14 方向、v12/v13 older 处置、v15 三项定案的事实基础均经 R11 复核成立（含首开 ≤8s 的独立预算分解：S-5 + S-8 同时交付时有余量）。五条意见经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-31 `direct_gzip_v1` 未定义压缩帧自描述机制（relay 的 gzip 标记住在 envelope `contentEncoding`（`relay_conn.go:296/304/313`，进 AAD），direct 的 `WireMessage`（`types.go:13-33`）无该字段、`Conn.SendJSON` 直接 WriteJSON——iOS 收到帧无法区分 gzip/原始 JSON；S-8 冻结批次全部依赖此机制，实施者只能自行发明（违反 R10「MUST NOT guess」） | 阻塞 | §3.6 定案 **WebSocket opcode 自描述**：text 帧 = 原始 JSON（现状），binary 帧 = gzip(JSON envelope) 整帧；Mac 仅对已协商 `direct_gzip_v1` 的连接发 binary；client→Mac 恒为 text。三种替代（信封字段 / gzip magic 嗅探 / permessage-deflate）的否决理由随行记录。S-8 冻结批次内容同步更新（协议文档 binary 帧语义 + binary fixture 样本 + opcode 分流断言） |
| F-32 v14 取消 S-1 后的悬空引用群（§7.3 trace 交付仍写「S-1 交付」、S-5 前置写 `S-1`、S-3 前置写已被取代的 `OD-2`、§4 OD-8 行与 §7.2.15 的「§6 S-1 / S-1 交付」、E-2/E-10 行的「S-1 完成宣告」、§8 基底缓存回退仍写 `window_0`） | 建议 | 逐一改指：trace 交付 → S-7；S-5 前置 → 无（与 S-8 同批建议）；S-3 前置 → v2 协议冻结（OD-2 已取代说明）；older gate 交付 → **S-4**（windowed 会话随 restore+increment 进入默认路径）；E-2/E-10 阻塞对象 → S-7；§8 回退 → 全量冷路径。**§0 历史修订表中时代准确的 S-1 引用保留不改**（那是各版本当时的真实记录） |
| F-33 anchor-newer 样本必须独立文件（Mac/iOS fixture 断言固定读 `window-response-spec.json` 并做精确 key 比对（`projection_window_spec_test.go:33/:73-75`），追加新条目必然失败） | 建议 | §3.8 与 §6 S-7 定案：anchor-newer 样本用**独立文件**（`anchor-newer-response-spec.json`，两仓同步副本、字节一致），spec 测试新增对应读取点，不动既有文件及其断言 |
| F-34 §3.8 未提 held turns 注册（现有 window 页完成路径用 `CompleteProjectionSnapshotWithHeldTurns`（`projection_window_handler.go:201-211`），P0-2 审计注释警告漏注册会「permanently strand the detail」） | 建议 | §3.8 新增 held 注册 bullet：anchor 增量页（含锚点 turn）复用同一完成事务注册 held 集合，使后续 detail commit 不被清空；§7.1.5 补对应断言 |
| F-35 E-10 收缩到 anchor 后冷路径 execution 失去任何门；§5 E-10 行（`:686`）仍是旧口径「window 冷开 / S-1 完成宣告」，与正文矛盾 | 建议 | §5 E-10 行重写（anchor 主对象 + **冷路径 execution 回归护栏**——running session 全量首开后 `isSessionExecuting == true`，防 S-5 解码重构丢字段；v14「自动满足」降级为「需护栏」）；§7.2.10 改双口径（冷路径全量 + anchor 增量） |

本章之外未改动 v15 的任何设计内容（三项 OD 定案、v14 方向、增量架构全部维持）。

### 0.15 v15 修订范围（历史，OD-1/OD-3/OD-7 定案回填，理由随行）

owner 于 2026-10-17 裁决「按推荐回填」三项 pending 决策（理由按 owner 要求**随行写入 §4 决策表**，不只在修订表里留摘要）。**决策表就此收口：无 pending 项。**

| 变更 | 修订处 |
| --- | --- |
| **OD-1 定案 B**：恢复基底 + 「正在同步」标记（增量未返回期间） | §4 OD-1 行（附三条理由：A 与 owner 重开 UX 要求冲突 / C 丢失失败显式边界 / B 安全性已论证）；§3.4.2 状态机标注 |
| **OD-3 定案 A**：无 TTL，仅 identity/schema/容量/LRU 淘汰 | §4 OD-3 行（附四条理由：时间非正确性判据 / TTL 在最需要缓存的场景最伤 / 占用与消失各有更准机制 / 仓内先例）；§3.4.1；§7.2.16 负对照 |
| **OD-7 定案 A 并收敛具体数值**：单 session 4MiB/256 turns；总量 128MB + LRU（缓存目录排除 iCloud 备份）；增量超限 2 页退全量冷路径；anchor 页 limit 默认 100 turns/4MiB | §4 OD-7 行（附四组数值理由）；§3.4.1（含 v14 一致性修正：淘汰回退路径改为全量冷路径）；§3.8；§7.3；§6 S-4；§7.2.16 配套断言 |

本章之外未改动 v14 的任何设计内容。

### 0.16 v14 修订范围（历史，owner 方向变更 #2）

owner 于 2026-10-17 裁决：**去掉 S-1 的「首开走 `window_0`」——首开 = 全量拉取 + gzip 压缩减少网络体积**（首开 UX = 全屏加载图标，一次性成本）；**重开主路径（restore + anchor-newer 增量）不变**。本版回填该方向并登记其结构性后果：

| 变更 | 修订处 |
| --- | --- |
| 首开载荷从 `window_0`（≤4MiB 有界）改为**全量 projection + gzip**：R-1 改写（有界性从首开撤到重开增量）；R-2 首开目标改全量口径；`window_0` **降级为 contingency**（协议与两端实现保留——超长 session 首开触顶 15s 时的降级开关；启用时需补 scalarState 冻结，见 §3.7 范围注） | §1.2 R-1/R-2；§3.3；§3.4.2（回退路径 FallbackFull）；§8 |
| **新增 S-8：`direct_gzip_v1` 传输压缩**（今天只有 Relay 有 gzip，事故恰为 Direct）——镜像 `relay_gzip_v1` 纪律：应用层逐帧、≥32KiB 且压缩更小才启用、仅 Mac→client 大响应；iOS 解压复用 relay 路径既有 gunzip | §3.6（原「不新增 Direct gzip」立场由本裁决推翻）；§6 新切片 S-8；§7 |
| **scalarState 冻结范围缩小（真简化）**：`scalarState` 只出现在**新的 anchor-newer 响应类型**上，**不再触碰 frozen v1 `BridgeGetSessionProjectionWindowResult` 字段集**——F-21 批次从「改冻结断言」缩小为「新增类型 + 新样本 + schemaRevision」；全量首开路径原生含 execution（`SessionProjection` 自带），E-10 范围相应收缩到 anchor 路径 | §3.7 范围注；§5 E-10 |
| **E-3（window 通路未生效根因）仍必须修**：anchor-newer 是 window 家族 RPC，跑在同一协商/handler 管道上——S-1 的管道工程不消失，并入 S-7；「首开 window_0」相关验收（E-4 口径、OD-2）改由 anchor 页承接 | §5；§6；§4 OD-2 处置 |
| **风险登记（本方向的接受项）**：首开耗时随会话体积线性增长（gzip 只压网络、不消 iOS 解码——S-5 单次解析因此更关键）；超长 session 首开仍可能触顶 15s 显式失败（缓解 = contingency 开关）；UX 不一致：首开可见全部历史、重开恢复的是有界尾部 + 截断提示 | §7.3；§8；§9 |

本章之外未改动 v13 的其它设计（增量架构、S-2/S-4/S-5、OD-6/OD-8 定案、不变量全部维持）。

### 0.17 v13 修订范围（历史，OD-8 定案回填）

owner 于 2026-10-17 裁决 **OD-8 = (a) 关闭**（采纳推荐：第一版不碰 prepend，窗口起点顶死 + 截断提示——事故修复与增量收益不变，渲染风险不担）。本版回填定案并落地配套交付与验收：

| 变更 | 修订处 |
| --- | --- |
| OD-8 定案回填：S-1 期间 older 上滑**不触发网络加载**，截断提示为唯一交互；older page 登记为独立后续专项（不在本方案切片内），启用前提 = `think.md:2699-2724` 复盘三条复活条件逐条满足并引用复盘、不得仅因首开慢复活 | §4 OD-8 行定案；§2.5；§3.3；§7.4 场景 8 改为 (a) 唯一验收形态 |
| **older gate 交付项**：若 SSV2 windowed 会话的滚动触发链已接线（`ChatViewModel+MessageSync.swift:981` SSV2 load-older 路径、`ChatUIKitContainerView.swift:2060` older-page prepend 处理），S-1 交付必须将其 gate 关闭；验收断言 = 滚动到顶零 `direction=older` 网络请求（Mac trace） | §6 S-1 最小验证；§7.2 新增第 15 条 |

本章之外未改动 v12 的任何设计内容。

### 0.18 v12 修订范围（历史，owner 质询触发）

owner 质询「S-1 的 window 是否本质上就是被放弃的分页（think.md 有大量 prepend 跳动教训）」；经本轮亲核 + DeepSeek 外部分析合并修订，三件事（全部采纳，另记录 DeepSeek 一处范畴错误，见下）：

| 变更 | 修订处 |
| --- | --- |
| ① 补引 iOS `think.md:2699-2724`（2026-07-12 分页复盘）并**重分类为 older 方向同根因**（§2.5 原总定性「均为同症状不同根因」不再成立）；附 **append/prepend 边界声明**：S-1 首开（空屏首批内容，无插入动作）与 v10 重开增量（尾部 append，不改变已渲染内容位置与高度）**不受该复盘约束**；SSV2 window 的 older 路径在数据层向已加载内容之前插入未知 turn（`SessionProjectionWindow.swift` insertion cursor，注释明写 "an older page's unknown prefix lands ahead of it"）——**与该复盘同坑**，处置归 OD-8 | §2.5 |
| ② **截断提示升格为正式要求**：v10/v11 仅两处顺带提及（§3.4.1 淘汰侧、§7.4 场景 12 回退形态），S-1 首开即截断的主场景无要求定义——新增 §3.3 Phase A 条目（文案「更早的 N 轮未加载」、判定 = 权威 `hasOlder` × 本地 coverage ledger、出现/消失时机），学 Claude App | §3.3；§7.4 场景 8 |
| ③ **older 上滑加载开/关登记 OD-8**（pending，推荐 (a) 关闭——对齐复盘「宁可慢，不要跳」的产品决定；older page 作为独立后续切片，启用前提 = 复盘三条复活条件逐条满足）；§7.4 场景 8 改为按 OD-8 双形态验收 | §4；§7.4 |

**对 DeepSeek 分析的纠正（记录在案）**：其「我错在哪」节把 v10 的 newer 方向增量一并归入被放弃对象——**范畴错误**：2026-07-12 复盘废弃的是 **older 侧 prepend**（往已渲染内容头部插入）；v10 增量是 **newer 侧尾部 append**，渲染风险面相反，不受该复盘约束（DeepSeek 自己的最终建议也未动 newer 增量，实质结论一致）。另：其称截断提示「完全没有」为夸大——实为两处顺带提及、缺正式要求定义（本版 ② 补齐）。`supportsSessionPagination = false` 硬编码（`CCCodeBridgeBackendClient.swift:226`，本轮复核实存）与复盘三条复活条件（锚点+精确视口偏移 / 异步高度二次测量不漂移 / 三 backend 真机连续翻页）经亲核确认。

### 0.19 v11 修订范围（历史，按 R10）

按评审报告 R10（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md`）修订。R10 判定 REVISION_REQUIRED（**0 阻塞** + 2 建议 F-29/F-30，并确认架构方向成立、§3.4.3 证据链闭合、修完两条即达 APPROVED 条件）。两条意见经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-29 restore 漏了 store 层提交步骤：`applyFull` 是 replica 层方法（change-set 携带 lifecycle 但不更新 store `State.lifecycle`、不通知 UI）；store 层既有调用点纪律 = applyFull / applyWindowPage 后紧跟 `commitIfObserved(lifecycle: .ready, changeSet:)`（`ProjectionStore.swift:1044-1056` / `:620-626`）。缺此步则 `State.lifecycle` 停留 `.disabled` → `renderProjectionFromStore` 静默丢弃渲染——restore 成功但屏幕空白 | 建议 | §3.4.2 restore 行改**两步**；§3.4.3 第 1 条展开为两步注入（含锚点与空白屏机理）；§7.2.12 补 lifecycle=.ready 与渲染断言 + 负对照 |
| F-30 `MemoryResident` 是死路径：`projectionStore.clear` 全仓唯一调用点 = `switchSession`（`SessionManagement.swift:232`，注释明写防 `isActive`/`sessionSyncV2Active` 跨切泄漏）且切走时无条件执行——「切走再切回」必走 BaseCacheHit；内存态只存在于「从未切走」时，而彼时不存在打开动作 | 建议 | §3.4.2 删除 MemoryResident 分支（打开状态机入口收敛为 BaseCacheHit / BaseCacheMiss）+ 新增关键点 6（**不得为打开路径改动 `switchSession` 的 clear 语义**，属需单独论证的设计变更）；§3.4.1 内存层 bullet 补注「重开必经磁盘恢复（有界，计入 §7.3 ≤1s 目标）」 |

**评审子证据的两处偏差记录（不影响结论，本轮实测）**：① R10 称 replica 层 `grep -c lifecycle` = 0，实测 `ProjectionReplica.swift` 有 8 处 lifecycle 字样（change-set 携带字段，apply outcome 硬编码 `.ready`，:25/:43/:163/:275/:344/:371/:435/:465）——核心主张「replica 不更新 store `State.lifecycle`、不通知 UI」仍成立；② R10 引 `renderProjectionFromStore` 未标文件，实际位于 `ChatViewModel.swift:1080`（guard 含 `sessionSyncV2Active`，:1087-1090）。本章之外未改动 v10 的任何设计内容（restore-then-increment 架构、§3.8 协议切片、不变量 6、S-7/OD-7/E-11 全部维持）。

### 0.20 v10 修订范围（历史，owner 方向变更）

**本版不是按评审意见的修订**：owner 于 2026-10-17 提出新方向（**iOS 增量从 Mac 拉取最新消息再 append 到 timeline**），经调研报告（`docs/2026-10-17-incremental-append-feasibility-report.md`，v3）与 owner 三问讨论定案（首开路径 / 双层缓存与保留策略 / Mac 无客户端状态）形成本修订。R9 的 APPROVED 是 agent 评审结论；owner 方向变更后方案按 repo 评审循环纪律重新进入评审（下一轮待审）。变更总表：

| 变更 | 性质 | 修订处 |
| --- | --- | --- |
| **增量追加成为重开主机制（restore-then-increment）**：磁盘基底缓存经唯一 writer 恢复为 timeline 基底 + Mac 端 turnId 锚定增量页 append；`window_0` 退为首开/冷启动/回退路径 | owner 方向变更 | §1.2 R-4/R-2；§3.1 数据流与不变量 2/6；**§3.4 整节重写**（v2–v9 的 last-good preview cache 设计被取代）；新增 §3.8 anchor-newer 协议切片与 §6 S-7 切片；§4 OD-7；§5 E-11 与 Gate A；§6 S-4 重设计；§7.1.5/§7.2.12-14/§7.3 新指标行/§7.4 场景 9-15；§8 风险与回退；§9 |
| **架构不变量入册（讨论定案六条）**：Mac 对客户端进度完全无状态（禁止 per-device 记忆）、Mac 存真相而非客户端缓存、水位线 = 裸 `tailTurnId`（R1 禁止持久化 wire cursor）、双层缓存形态、有界滑动 + LRU、首开与回退统一 `window_0` | 讨论定案 | §3.1（新增不变量 6 并强化不变量 2）；§3.4 |
| **实施细节入册**：restore 注入入口 = `applyFull(forceReplace: true)`（`ProjectionReplica.swift:193-212`）；`appliedRev` 重置必须发生在 applyFull 之后、增量请求之前（`:208` 赋值 / `:323` guard 证据；漏重置会 `.windowPageUnchainable` 或稳态退 34MB full snapshot） | 调研 + 外部评估提出，经亲核采纳 | §3.4.3 |
| OD-7（缓存预算与回退阈值）登记 pending | 产品裁决项 | §4；不阻塞评审（沿用 OD-1/2/3 pending 先例） |

**不变的部分**：S-1（window 链路恢复 + W1 scalar 交付）定位与全部内容不变，且是本方向的前置依赖；S-2（Mac 物化）/S-3（v2 summary）/S-5（解码管线）定位不变；OD-6 = W1 定案、fence scalar 分支、re-freeze 批次机制（§3.8 复用之）、存储选型（Mac=bbolt、iOS 文件型）全部维持。**整体回退面**：停用 S-4 重设计与 S-7 即恢复 v9 行为（display-only preview + window_0 权威重拉），见 §8。

### 0.21 v9 修订范围（历史，按 R9）

按评审报告 R9（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r9.md`）修订。R9 判定 **APPROVED（0 阻塞 + 1 建议 F-28）**——F-26/F-27 经 R9 核验全部闭合，OD-6 定案 W1 与 §3.2.2 存储选型维持（连续第五轮）。本轮**仅落地唯一建议 F-28**，不改动任何设计内容：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-28 §3.7 re-freeze 批次第⑥项称 `bridge_v1_schema.go` 注释块「既有六条注释惯例」，实测为 **13 条日期条目 / 8 个不同日期**（同日多次返工按 `（同日二次返工）` 标注，故条目数 ≠ 日期数） | 建议 | §3.7 批次⑥ 更正为「13 条日期条目 / 8 个不同日期」并附实测口径（`grep -c "^	// 20[0-9][0-9]-" go-bridge/bridge_v1_schema.go` = 13）与「追加动作与既有条数无关」的说明；其余内容不变 |

本章之外**未改动任何设计内容**：W1 定案、fence scalar 分支、re-freeze 批次六项结构、peer 组合行为表、存储选型、架构方向、切片划分、门结构、§7 验收矩阵全部保持 v8 原样。§10 新增第 14 项记录本轮复核点。

R9 结论摘要（供后续实施/验收参照）：**方案层面已通过**（R9 §1「判 APPROVED 的依据」）；F-28 属「计数措辞偏差，不改变动作、不影响验收」。R8 的两条（F-26 协商机制、F-27 schemaRevision 纪律）经 R9 逐项亲核闭合。R9 另记录一处正面核验结论（非缺陷）：既有 `projection_window_spec_test.go:325-340` 已有 direct+relay 双路径镜像护栏，但为**字符串计数型**且全文 `grep -c scalar` = 0——若按 v8 定案复用 `negotiateProjectionWindowV1`，该护栏已保证两路径覆盖（v8 §7.1.4 新增断言属行为级加固）；若改为独立函数，既有护栏完全不覆盖，§7.1.4 断言是唯一防线。

### 0.22 v8 修订范围（历史，按 R8）

按评审报告 R8（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r8.md`）逐条修订。R8 判定 REVISION_REQUIRED（**0 阻塞** + 2 建议 F-26/F-27，并明示修完两条即达 APPROVED 条件）；R7 两条意见（F-24/F-25）经 R8 核验全部闭合，OD-6 定案 W1 与存储选型维持（连续第四轮）。两条意见经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-26 `projection_window_scalar_v1` 的协商机制未定义：direct（`server.go:808`）与 relay（`main.go:693`）**各自独立**调用 `negotiateProjectionWindowV1`，capability 字符串只在 `server.go` 出现（3 处）、relay 靠函数复用覆盖——W1 选「复用」还是「独立函数」决定 relay 是否自动覆盖；漏 relay 会使 iOS 在 Relay（生产路径）收不到 scalar echo → 双维度 gate 永不满足 → **静默永久回 full**，只在 §7.4 场景 5 暴露 | 建议 | §3.7 W1 机制行定案为**在 `negotiateProjectionWindowV1` 内扩展**（relay 自动覆盖、零额外接线），并写明「若实施时改为独立函数，必须 direct+relay 两处接线并附双路径 echo 断言」的约束；§7.1.4 补 direct+relay 双路径 scalar echo 断言；§6 S-1 组件列同步 |
| F-27 re-freeze 批次未覆盖 `schemaRevision` 纪律，且存在既有 doc/code 分歧（代码常量 `BridgeProtocolSchemaRevision = "2026-09-05"`（`bridge_v1_schema.go:77`），README 记 `2026-10-06-r1`（`:15`）；字段纯信息性，`server.go:716`/`hello_handler.go:188` 只填充不比较） | 建议 | §3.7 re-freeze 批次增补第⑥项：按惯例 bump `schemaRevision` 并在 `bridge_v1_schema.go` 累积变更注释；README `2026-10-06-r1` 分歧显式登记为**既有已知分歧、不得在 S-1 变更边界内顺带修正**（独立处置，防无关分歧卷入本批次） |

本章之外仅做必要一致性维护（§10 评审重点更新），未改动 W1 定案、re-freeze 批次既有五项、peer 组合表、存储选型、架构方向、切片划分与其余门结构。

### 0.23 v7 修订范围（历史，按 R7）

按评审报告 R7（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r7.md`）逐条修订。R7 判定 REVISION_REQUIRED（**0 阻塞** + 2 建议 F-24/F-25，并明示修完两条即达 APPROVED 条件）；R6 三条意见（F-21/F-22/F-23）经 R7 核验全部闭合，OD-6 定案 W1 与存储选型维持。两条意见经本轮独立源码亲核后**全部采纳**（无驳回项）：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-24 re-freeze 批次第⑤项 iOS 断言行号错位（v6 四处误引 `:51`——该行断言 window **描述符** key，`scalarState` 落在 Result 层不触碰它；真正会失败的是 response 级断言 `:48`，v6 全文未引）；批次③漏冻结样本**双副本**同步义务 | 建议 | §3.7 断言清单第 3 条、批次③⑤、§7.2.1、§10.11 四处行号更正（`:51` → `:48`，并补注 `:51` 与 Mac `:77-80` 为描述符级、不受影响）；批次③补「两仓独立副本（实测同哈希 `6853e4ac…`，iOS 测试经 `fixtureRoot` 读本仓副本）必须同步更新并保持字节一致」 |
| F-25 重试次数指标在 window 路径下不可观测（window handler 无 `projection_rpc` trace，grep = 0；fence 日志无 requestId 且 full/window 共用）；双维度 gate 的合法回退若不区分口径会被误计为重试 | 建议 | §7.3 重试指标行补**可观测前提**（S-1 交付 window 路径与 full 同构的 `projection_rpc` trace，full 侧锚点 `handlers_projection.go:89/:175`）与**计数口径**（合法回退 ≤1 次不计重试；hydrating/网络失败的重复发起计入）；§6 S-1 组件列补该 trace 交付项 |

本章之外仅做必要一致性维护（§10 评审重点更新），未改动 W1 定案、re-freeze 批次结构、peer 组合表、存储选型、架构方向、切片划分与其余门结构。

### 0.24 v6 修订范围（历史，按 R6）

按评审报告 R6（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r6.md`）逐条修订。R6 判定 REVISION_REQUIRED（1 阻塞 F-21 + 2 建议 F-22/F-23），意见经本轮独立源码亲核后**全部采纳**（无驳回项）；R6 同时复核 F-20 已闭合、OD-6 定案 W1 维持、§3.2.2 存储选型维持，相关状态一并记录：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-21 W1 的 `scalarState` wire 增量未登记协议冻结义务，且会打破 Mac/iOS 两侧三处冻结字段硬断言 | 阻塞 | §3.7 W1「代价」行改为 **R10 re-freeze 前置登记**（更正「标准 additive」的不完整定性）：列出三处冻结断言锚点与冻结样本，处置 =「更新冻结物而非删除断言」；re-freeze 五项批次内容写入 §3.7，并纳入 §5 Gate A 与 §6 S-1 前置（与 OD-6 定案同级）；§7.1.4 / §7.2.1 补冻结断言更新项；§8 风险表补「re-freeze 批次被绕过」行 |
| F-22 「未收到 scalar echo 即不走 window」需 iOS eligibility 双维度，「新 Mac + 旧 iOS」组合行为未评估 | 建议 | §3.7 W1 补 **peer 组合行为表**（四格逐格写明是否走 window、scalar 来源、执行态正确性）；登记 iOS 实现点（`isProjectionWindowEligible` 扩双维度）；「新 Mac + 旧 iOS」定性为既有缺口形态（由 iOS 版本升级解决，不在 S-1 范围）；服务端 echo 绑定作为已考虑并否决的替代（理由记录）；§7.2.1 补断言；§8 风险行更新 |
| F-23 §2.1 iOS 行提交哈希与未提交枚举未回填实测值 | 建议 | §2.1 iOS 行回填 R6 实测（`dfb77ed9…`，纯 docs 单文件 +251 行漂移，8 项未提交逐项列出），保留时点快照语义与 S-0 重生成义务 |

本章之外仅做必要一致性维护（§10 评审重点更新），未改动 W1 定案、fence scalar 分支设计、存储选型、架构方向、切片划分与其余门结构。R4/R5 意见（F-17/F-18/F-19/F-20）经 R6 核验全部闭合；R6 确认 F-21 属方案在 W1 wire 增量上从未登记的既有协议纪律义务（历轮第三次「方案没写的约束」盲区），非 v5 修订回归。

### 0.25 v5 修订范围（历史，按 R5）

按评审报告 R5（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r5.md`）逐条修订。R5 判定 REVISION_REQUIRED（1 阻塞 F-20 + 0 建议），意见经本轮独立源码复核后**采纳**（无驳回项）；R5 同时完成 OD-6 定案复核（W1 维持）与 §3.2.2 存储选型定案复核（维持），相关状态一并更新：

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-20 §3.7 W1 fence 语义（v4 按 F-17 重写后）遗漏 `decide` 路由维度：清空仅作用于 decide=true pending frame；「Execution-only 边界」在当前代码不可达（真实存在的 Execution-only patch 走 decide=false 常规链，不被清空）；§7.1.4 断言缺构造前提 | 阻塞 | §3.7 fence 语义段补 decide 路由维度（第四个事实维度）并修正三个 bullets 的归因；「真实边界」改写为「防御未来新增 detail commit 构造点产生 Execution-only commit」（现可达性为零）；「execution 变化通常伴随 turn ops」句改写为「安全性来自 decide 路由」；边界处置（fence scalar 分支）重定位为防御性分支，OD-6 定案条件 2 设计与理由不变；§7.1.4 第三条改写为双链断言、第四/五条补构造前提与真实样本 |
| OD-6 定案复核（R5 §3：W1 维持；定案条件 1 在 v4 未完全达成） | — | §4 OD-6 行补 v5 记录（条件 1 随 F-20 修订达成；条件 2 设计不变、定位为防御性）；§5 Gate A 补「S-1 开工前置包含 F-20 修订」 |
| §3.2.2 存储选型定案复核（R5 §4：维持） | — | §3.2.2 定案标注补 R5 复核维持记录 |

本章之外仅做必要一致性维护（§10.8 复核请求更新为 decide 维度、§10.10 措辞精确化），未改动 W1 定案、fence scalar 分支设计、架构方向、切片划分与其余门结构。R4 三条意见（F-17/F-18/F-19）经 R5 核验全部闭合；R5 §6 确认 F-20 属 v4 新改内容的机制性偏差（R4 的 F-17 证据亦未覆盖 decide 维度），非历轮意见回归。

### 0.26 v4 修订范围（历史，按 R4）

| 意见 | 级别 | 修订处 |
| --- | --- | --- |
| F-17 §3.7 W1 fence「已知限制」归因与代码实际行为不符；真实边界（Execution-only patch 被 fence 清空）未覆盖 | 阻塞 | §3.7 W1「fence 语义」段按代码真实行为重写（held 集合连接级累积、`patchTurnIDs` 交集判定、清空仅发生在 fence pending 路径）；「已知限制」替换为真实边界 = 纯 Execution 变化（无任何 turn op）的 patch 在 fence 期间对 window 连接被清空 `Execution`；按定案条件 2 显式处置（fence scalar 分支，主路线）；§7.1.4 第三条与 §7.4 场景 8 断言同步改写 |
| F-18 F-13 登记的 capability 双门偏离无收口路径 | 建议 | §2.2 已知偏离行补收口指针；§6 登记收口实施项（归属 S-3 协议冻结，方向二选一） |
| F-19 §1.1 两处数字口径标注残留 | 建议 | 「约 97MB」标注 101,520,809 B（96.8 MiB）；6,347,762 B 补注 python 口径 + Go 验收口径数字；全文「97MB」统一为「约 97MiB」 |
| OD-6 定案回填（R4 §3，owner 委托评审轮定案） | — | **定案 W1（window 响应 additive 携带 scalar state）**，定案条件 1（= F-17 修订）与条件 2（Execution-only 边界处置）随本轮回填；§3.7 措辞从「推荐/待定案」改为定案；§4 决策表回填并冻结；§5 Gate A / §6 S-1 / §10.8 同步 |
| §3.2.2 存储选型定案复核（R4 §4 维持） | — | §3.2.2 定案标注更新为「R4 复核维持」；选型内容与格式设计不变 |

本章之外仅做必要一致性维护（「定案路线」→「W1 定案路线」等交叉引用、口径统一），未改动架构方向、切片划分与其余门结构。R3 的 6 条意见（F-11~F-16）+ R2 遗留 F-10 经 R4 独立核验全部闭合，本轮不再改动其修订内容。

### 0.27 v3 修订范围（历史，按 R3）

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

### 0.28 v2 修订范围（历史，按 R1）

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
| R-1 | **（v14 改写，owner 方向变更 #2）首开 = 全量拉取 + gzip 压缩（`direct_gzip_v1`，§3.6/S-8）——有界性不再适用于首开，首开耗时随会话体积线性增长**；`window_0` 降级为超长 session 首开触顶 15s 时的 contingency 开关（协议与两端实现保留，payload 规则不变——多 turn 累积 ≤4MiB、单 turn 超限整 turn 单独成页，frozen v1 never-split-a-turn，`go-bridge/projection_window.go:24-26`）；**重开路径有界**：anchor-newer 增量页受 limit/4MiB 约束（§3.8）。 |
| R-2 | **（v14 改写）首开（冷，全量 + gzip + S-5 单次解析）：对本次约 97MiB/119 turns 样本（projection 约 34MB，gzip 后约 9.6–11MB，字节口径见 §1.1），目标 Direct ≤8s / Relay ≤10s**（全屏加载图标，一次性成本；目标是实施验收，不是当前已验证结果）；**重开（有效水位线）目标 ≤1s（v10）**：本地恢复 + KB 级 anchor-newer 增量页。15s hard cap 保留为失败线；**超长 session 双失败线登记于 §8（v24 补 F-53：15s 触顶时间线 = 接受项 + contingency 缓解；full 序列化超 `projection_max_bytes` = typed `full.oversized` 显式终态失败）**。 |
| R-3 | iOS 15s hard cap 保留为最后失败线；正常 bounded path 不应触达。**（v19 分口径 F-41）重开路径**超时后只重试有界对账页（不回 full）；**冷路径**（首开 / FallbackFull 回退）超时后重试 full 是设计行为，不判失败——两口径分开计数。**单次打开的重试次数是独立验收指标（§7.3），不因「最终成功」而豁免（v3 补，F-16）。** |
| R-4 | 打开、切换、重连不再因为内存 projection 被清空而丢失所有可用状态。**（v10 改写，owner 方向变更；v19 更正 F-41）重开路径主机制 = restore-then-increment（§3.4）：磁盘基底缓存经唯一 writer 恢复为 timeline 基底，对账增量页（含 changedTurns 修正）append 补齐至权威头部；恢复期间以明确 syncing/stale 标记区分「已恢复未对账」状态。首开与回退走全量冷路径（full + gzip，§3.9 总表）。** |
| R-5 | Mac projection 持久化从单个大 JSON checkpoint 演进为可增量追加、可分页查询的物化索引；普通 transcript append 不触发全量重建。 |
| R-6 | Claude 运行中 session 仍保持现有 live-partial / terminal 收敛语义；窗口化不得丢失 active turn、permission card、执行状态和 patch 顺序。**（v3 补，F-11；v17 去语境 F-37）这在本方案中是主动设计目标而非「保持现状」：现状 window 响应不携带 execution 等标量状态（§1.1/§2.2），window 路径若不随 §3.7 W1 定案路线交付 scalar 即违反本条（v3 语境为「S-1 生效即违反」），验收见 E-10 与 §7.1/§7.2。** |
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
| Codex 上游 | `/Users/jacklee/Projects/codex`，`main@ce0323f2f2d3b25e82806cd555237da3db4e1897`，干净（本行记录值经实测 = 实际 main；**R23 评审报告来源表曾将其一位误写为 `ce0323f2f2d3b25e82806cd55e237da3db4e1897`（对象库不存在的无效哈希）并附失实的「实测一致」声明——元审核 `review-r23-meta.md` 更正、v28 亲测复核确认，更正记录见 §0 v28；后续引用 U 来源身份以本行为准，不得转引该报告 U 行**） |
| 正式 Mac runtime | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，PID 8303，监听 8777；运行时构建身份与源码 HEAD 不相同，日志结论需区分 |

iOS 未提交修改属于当前用户或其他代理，本方案不依赖其内容，也不修改它们；历轮评审已确认全部锚点文件不在脏清单内。

### 2.2 现有 CordCode 能力

| 能力 | 锚点 | 结论 |
| --- | --- | --- |
| Mac window 生产开关 | `go-bridge/main.go:560`，`SetProjectionWindowEnabled(true)` | 生产源码已启用；正式 runtime 也含该能力符号 |
| window 上限 | `go-bridge/projection_window.go:24`，256 turns / 4MiB / 默认 100 turns | 复用；本次样本会被 4MiB 截到约 17 turns |
| window capability gate | `go-bridge/projection_window_handler.go:32` | 复用；未协商连接 fail-closed。**已知偏离（v3 登记，F-13——登记时为「S-1 不改」；v14 后默认路径不涉 window，偏离收口仍归 S-3 冻结时二选一）**：实现为 hello echo 单门（`go-bridge/server.go:373-395` 只查客户端声明 + 服务端开关），而 `docs/protocol/bridge-v1.md:2173-2175` 要求「clients MUST gate window RPCs on the descriptor list, not the global echo」的双门中，descriptor 侧从未广告——全仓非测试代码无一处把 `projection_window_v1` 写入 backend descriptor，iOS `CCCodeBridgeBackendClient.swift:1308-1310` 同样只查 echo。E-3 取证已补 descriptor 维度（§5）。**收口实施项（v4 补，F-18）：已登记为 §6 S-3 的协议冻结项，方向二选一（descriptor 写入并按文档条件 echo，或修订协议文档为单门语义）** |
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

按 repo 规则（排查 session/history/加载异常必须先检索两仓 `think.md`）完成检索，v2 补记入方案。结论：**前四条为同症状不同根因**（无与本案冲突或可直接复用的结论）；**第五条（2026-07-12 分页复盘，v12 补引）与本案 older 方向同根因**，直接约束 OD-8 的处置。

- Mac `think.md:450`：iOS 27h 重连 91 次、每次重拉全量投影快照——「全量重拉」问题的先例，但发生在重连链路，本案是首开；
- Mac `think.md:834`：审批卡 15s 超时——审批控制面部件，与投影打开无关；
- Mac `think.md:1205`：冷源活跃信号 gate 永不 ready → iOS 15s 超时重试死循环——同症状（15s 超时 + 多次重试）在本产品有先例，可作 §1.1 问题背景佐证；根因是 gate 判定而非 payload 大小；
- iOS `think.md:3343` / `:3485` / `:3491`：legacy `get_session_messages` 路径的 15s 超时与软重连副作用——legacy 路径，本案是 SSV2 `get_session_projection`，不同链路。
- **iOS `think.md:2699-2724`（2026-07-12 分页复盘，v12 补引——同根因，非同症状）**：旧页 prepend（往已渲染 timeline 头部插入）改变 item 集合与高度，MessageWeb/Virtuoso 的分组、异步内容高度与重渲染时序不能可靠恢复可视位置 → 上滑跳底/漂移；产品决定 `supportsSessionPagination` 强制 `false`（`CCCodeBridgeBackendClient.swift:226` 硬编码，本轮复核实存），并封存三条复活条件（稳定 message ID + 精确相对视口偏移恢复锚点；Markdown/图片/工具输出展开与异步高度二次测量后锚点不漂移；三 backend 真机连续翻页不跳底/不跳位）。**append/prepend 边界声明**：该复盘约束的是 **older 方向（头部插入）**——SSV2 window 的 older 路径在数据层同样向已加载内容之前插入未知 turn（`SessionProjectionWindow.swift` insertion cursor，注释明写 "an older page's unknown prefix lands ahead of it"，`updated.insert(turn, at: cursor + 1)`），与之同坑；**OD-8 已定案 (a) 关闭（v13 回填，2026-10-17 owner 裁决）：方案实施期 older 上滑不触发网络加载**；**首开**（v14 起 = 全量拉取；登记时语境为 S-1 window 首开——空屏首批内容，同样无插入动作）与 **v10 重开增量**（newer 侧尾部 append，不改变已渲染内容的位置与高度）**不受该复盘约束**。

## 3. 架构设计

### 3.1 总体数据流

目标打开链路：

```text
iOS open（v10：restore-then-increment 为主，window_0 为首开/冷启动/回退）
  ├─ 内存态在（App 未杀且未被 clear）→ 直接进入稳态 patch 流
  ├─ 磁盘基底缓存命中（身份 + schema 校验）→ 经唯一 ProjectionStore/Replica writer 恢复为基底
  │    → anchor-newer(anchorTurnId=tailTurnId, 含锚点)（§3.8，含 scalar state）append 补齐
  │    → 更新水位线与缓存 → 滑动淘汰 → 写盘
  └─ 首开 / 身份不匹配 / 水位线 typed 失效 / 增量超限 → get_session_projection 全量（+ direct_gzip_v1，§3.6/S-8；v14：window_0 降级 contingency）
       → 经唯一 writer 整体重建 → 缓存取有界尾部滑动窗 + 写水位线（首开产物 = 增量链种子，按设备各一份）

Mac
  ├─ Claude source inspector
  ├─ ProjectionKernel hydrate / live patch（仍是唯一真相 writer）
  ├─ ProjectionMaterializationStore 增量物化 turn/item 行
  └─ window handler 从 committed head / materialized page 切 bounded result
     （anchor-newer 亦经此 handler：对 kernel turn 列表现场定位切片，全程不持客户端进度状态——不变量 6）
```

不变量：

1. **单一真相 writer**：Mac `ProjectionKernel` 继续拥有 projection truth；materialization store 是派生索引，不直接读 agent、不生成第二份真相。
2. **单一 iOS timeline writer（v10 改写）**：权威数据（turns **与 scalar state**）仍只能经 `ProjectionStore -> ProjectionReplica -> PresentationEngine` 进入 timeline。基底缓存（§3.4）**必须经同一 writer 注入**——restore 走 `applyFull(forceReplace: true)`、增量走 `applyWindowPage`——缓存不得绕过 replica 直接驱动 PresentationEngine；注入后的权威对账点仍是 Mac 增量/权威页，缓存不是第二真相。
3. **有界重开**（v19 改 F-41——原「有界首开：Claude 首开不得返回整棵 projection」为 v14 前口径，与首开 = 全量 + gzip 的定案矛盾）：**重开路径的增量对账页受 limit/4MiB 约束（§3.8）；首开/回退 = 全量 + gzip 是 v14 设计行为**，其边界由 15s hard cap 与 contingency 机制承担（§3.9 路径总表为唯一权威——同一触发条件只有一个默认动作）。
4. **失败显式**：缓存必须带 freshness 状态；网络失败不能把恢复基底标成 authoritative ready（增量页是对账点）。
5. **身份先于数据**：所有持久化 page / row 先校验 backend、bridge 身份（持久 `bridgeId`；`bridgeEpoch` 仅作 staleness 标记与 provenance，见 §3.4.1）、session、source identity、schema，再参与显示或查询。
6. **Mac 对客户端进度完全无状态（v10 新增，owner 定案）**：Mac 不存 per-device offset / 已读位置 / 客户端记忆；anchor-newer 请求自带 `anchorTurnId`，Mac 对 kernel turn 列表现场定位切片（与 `locate` 同构；类比 HTTP Range/ETag、IMAP UID——服务端无状态，客户端持游标）。多设备各水位线各请求，天然不串扰；任何一端重启都不需要另一端配合恢复。**两端各自对自己的持久化状态做增量恢复**（Mac：S-2 checkpoint/materialization；iOS：§3.4 基底缓存），互不存储对方的状态。**禁止**将来以任何理由往 Mac 加 per-device 状态（会引入多设备同步、重启恢复、断连重连三重复杂度）。

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
- **来源判定与 IO 分类（v19 重写 F-45——原「(path,size,mtime) 快速匹配，失败才全量 digest」不能支撑「append 只读 suffix」：正常 append 改变 size/mtime → 必失配 → 全 prefix 整读（`projection_kernel.go:407`），恰好击中「保存 → 停止 → append → 重启」这一最常见恢复场景）**，四态分类：
  - **(i) kernel 常驻**：零验证、suffix-only（现状语义）；
  - **(ii) 重启（v20 重写 F-45/F-47——v19 的 growth-trust 被否决：与 baseDigests 共享同一 Kernel，Kernel 恢复错误时两层皆错（反例：prefix 被等长改写 + 合法 suffix append → membership 不变 → 恢复 A旧 → iOS 回传 A旧 digest → Mac 从 A旧算 → 匹配 → changedTurns 空 → 假成功）；且 source generation 的生产计算本身全读 prefix（见下）**：**默认 = source-ledger prefix digest 校验（整读 prefix）+ suffix 折叠**——IO 如实登记为「prefix 校验整读 ≈ transcript 大小（97MiB 顺序读约 0.3–1s）」；**R-5 精确化：S-2 的承诺是「append 不触发全量重投影（重投影仅 suffix）」，不是「零 prefix 校验读」**；
  - **source ledger 生产链（v20 登记 F-47——此前 S-2 只测 bbolt loader 会漏掉它）**：`claudeSessionFileRelay` 启动即安装 source ledger 并调用 `claudeSourceCorrelation.Observe`（`handlers_relay.go:877/:882-884`，注释明写「Needed for every live source batch… the Kernel cursor/gap/generation fence keys on SegmentStableKey/SegmentGeneration」）；`Observe` 对 cursor 之前全部内容哈希（`claude_source_trace.go:99` 调 `digestClaudeSourcePrefix:122` = `io.CopyN`，`:75` 对 previous cursor 也重读）——**不受 `GO_BRIDGE_CLAUDE_SOURCE_TRACE` 日志开关保护，是生产必需**；S-2 测试必须覆盖「store 恢复 → source ledger → relay 启动」全链读量（trace 开/关均可解释）；
  - **growth-trust = OD-9 可选档（v20 降级 F-45——接受「prefix 原地等长/增长型改写不检测」是 R-7 的产品例外取舍，须 owner 显式裁决，不得由工程修订默认授权）**：仅在 OD-9 启用后，(ii) 可跳过 prefix 校验直折 suffix；启用时 iOS baseDigests 对账**仍只保证「消费者与 Kernel 一致」，不证明「Kernel 与 source 一致」**——该残余风险随 OD-9 一并接受；
  - **(iii) 重启且 (path, size, mtime) 全等**：快速匹配命中（零整读），接受 committed cursor、只折叠 suffix（覆盖「停止后无 append」场景）；
  - **(iv) size 回退 / 路径或 membership 变化 / source generation 显式变化 / (ii) 解析失败**：诚实失效重建；`prefix_sha256` 持久化**降级为争议裁决工具**（mtime 不可靠、疑似 rewrite 的最终判定；重算不匹配 → 重建），不再是常规路径；
  - 各态 IO 上界（v21 修正 F-49——原「(i)/(ii) = stat + 4KiB + suffix」为 v19 growth-trust 残留，与 (ii) 默认整读矛盾）：**(i) 常驻 = suffix-only（零校验）**；**(ii) 重启默认 = prefix 校验整读（≈ transcript 大小，source-ledger 生产链必需）+ suffix 重投影**（OD-9 启用后方可跳过校验 = stat + 4KiB + suffix）；**(iii) 全等 = stat（快速匹配零读）**；**(iv) = prefix digest 裁决 + 重建**；
- **window 路径强制冷检（v3 补，F-14）**：window handler 的 `forceColdInspection` 无 `sinceRev` 门（`projection_window_handler.go:102-106`），**每次 `window_0` 都触发冷源检查**——与 full 路径（`handlers_projection.go:122`，仅 `SinceRev == 0` 时）条件不同。冷检成本上界（亲核）：`richHistoryTranscriptSegments` 只对每个 segment `os.Stat`（`agent/claudecode/continuation.go:379-392`），`projectionJSONLStartCut` 只做每 segment 尾部 4KiB 反向扫描定位换行（`handlers_projection.go:915-954`）——**都不是整读 transcript**。稳态（kernel Ready 且 source 未变）下 `BeginHydrateTransaction` 返回 `AlreadyReady` 不重建（`projection_kernel.go:1030-1045`）；
- **组合路径（v22 改 F-49——原「S-2 后仍不整读 97MiB」与 (ii) 默认 source-ledger prefix 整读冲突）**：transcript append 后首次 `window_0`（`source.Cursor > committedSourceCursor`）走 catch-up——S-2 落地后该路径 IO = **(ii) 重启默认：source-ledger prefix 校验整读（≈ transcript 大小）+ 冷检（stat + 尾部 4KiB 扫描）+ suffix 重投影**（OD-9 启用后方可跳过 prefix 校验）；重投影仅 suffix（R-5 精确口径）。S-2 登记实施项：给 window handler 增加「kernel 已 Ready 且 source 未变则跳过冷检」的等价门（对齐 full 路径的 `sinceRev` 语义）；
- 重启后首开与上述组合路径的 Mac 侧时间预算均纳入 §7.3 指标表。

#### 3.2.4 与 live patch 的关系

`ProjectionKernel` 仍是 reducer / patch / fence 的唯一所有者：

- cold hydrate commit 后，将 changed turns 写入 materialization；
- live `projection_patch` flush 后，同一 materialization writer 按 patch 的 `upsertTurns`、`partOps`、`turnStateOps` 更新 rows；
- window handler 读取与 kernel head 一致的 store revision，并在 snapshot fence 下返回 page；
- store 落后时先推进到 requested head，不得返回旧 page 冒充当前 head。

Phase A 可先让 window 继续从 kernel in-memory snapshot 切片，materialization 只解决重启和增量重建；Phase B 再把 page 查询下推到 materialization store。

### 3.3 首屏 page 策略

#### Phase A（v14 改写，owner 方向变更 #2）：首开全量 + gzip；window 通路修复服务于增量

- **首开（冷路径）= 现行 full pull（`get_session_projection`，原生含 execution 等 scalar state）+ S-5 单次解析 + S-8 `direct_gzip_v1` 传输压缩**——iOS 首开路径无需改走 window；UX = 全屏加载图标（一次性成本，目标见 §7.3）；
- **`window_0` 从首开主路径降级为 contingency**：协议与两端实现保留；仅当超长 session 首开触顶 15s（§8 风险）时作为降级开关启用（启用时需补 §3.7 的 v1 响应 scalarState 冻结，见范围注）；
- **window 通路修复（原 S-1 管道工程，v14 起并入 S-7）仍然必做**：E-3（window/capability 协商未生效根因）取证与修复——anchor-newer 是 window 家族 RPC，跑在同一协商/handler 管道上；含 window `projection_rpc` trace（F-25）与双路径 echo 纪律（F-26）；
- **scalar state（§3.7 W1 定案）范围收缩（v14）**：全量首开原生含 execution（`SessionProjection` 自带；E-10 冷路径**需护栏**——F-35 口径，v17 对齐）；`scalarState` 字段只出现在**新的 anchor-newer 响应类型**上（§3.8）——不触碰 frozen v1 字段集；
- **截断提示（v12 补，v14 范围调整）**：首开全量可见、无需提示；**重开恢复基底后**（缓存为有界滑动尾部）顶部显示截断告知（如「更早的 N 轮未加载」），判定依据 = 权威 `hasOlder` × 本地 coverage ledger（不猜）；增量 append 与滑动淘汰后的刷新时机 = 权威 page（含增量页）apply 之后；**OD-8 已定案 (a)：不提供上滑加载，截断提示为唯一交互**（v13 回填）。**UX 不一致登记（owner 接受）**：首开可见全部历史、重开为截断视图；
- v1 回退规则保持：旧 Mac 或旧 iOS 可 full projection。对声明了能力的新 peer，不允许因非 typed 错误静默 full fallback。

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

### 3.4 iOS 基底缓存与 restore-then-increment 打开路径（v10 整节重写，owner 方向变更；v2–v9 的「last-good preview cache」display-only 设计被本版取代）

#### 3.4.1 存储模型（双层：磁盘保底 + 内存加速）

新增 `ProjectionBaseCacheStore`（v10 由 v9 `ProjectionPreviewCacheStore` 重设计），不复用 `MessageCacheManager`：

- **磁盘层（保底，owner 定案：卸载重装才会真正失去）**：每 session 单文件 + 原子写（temp + rename，对齐仓内 `pinstore` / sidecar 纪律），内容 = 已加载 turns（有界范围）+ scalar state（对齐 §3.7 W1 交付）+ 水位线 metadata；存放于 **Application Support**（对齐仓内惯例：`SessionSnapshotStore.swift:316-318`、`BackgroundGenerationMarkerStore.swift:22`、`SessionReadStateStore.swift:35` 均用 `.applicationSupportDirectory`）——iOS 系统在存储压力下清 Caches 而不清 Application Support；不引入 SQLite/GRDB；
- **内存层（加速）**：活跃 session 的 replica 本身即最新态（live patch 持续 apply）；`switchSession` 的 `clear` 只清内存态、不删磁盘缓存；**写盘时机** = 权威 page / 增量页 apply 后、App 退后台时；磁盘落后内存安全——从旧水位线拉增量只是多拉几轮，turnId upsert 幂等。**（v11 补，F-30）`clear` 在切走时无条件执行（全仓唯一调用点 `SessionManagement.swift:232`），故重开必经磁盘恢复 + 反序列化——这是重开路径的常规成本（有界，已计入 §7.3 ≤1s 目标）；内存层的价值在活跃期 patch 流与前台恢复，不在重开路径**；
- key（文件名/目录名）：backend identity + bridge ID + session ID（不变量 5）；
- metadata：schema、bridge ID、bridge epoch（仅供 staleness 标记，不参与 key）、syncRev、head/tail turn ID（**tailTurnId = 水位线**）、savedAt；
- **水位线 = 裸 `tailTurnId`（owner 定案）**：跨重启、跨 epoch 的数据身份。**不得持久化 wire cursor**（R1 冻结条款 `bridge-v1.md:2266-2272` 明文禁止跨 epoch 持久化 cursor）、不得用数字序号（compaction/重排后错位）；请求时由 turnId 现场构造锚定；
- **有界滑动 + LRU（对「永不清除」的工程化修正；OD-7 已定案 v15，数值与理由见 §4）**：单 session 滑动上限 **4MiB/256 turns**（超限从**头部**淘汰最旧 turns，`hasOlder` 截断提示覆盖淘汰侧）；跨 session 总量预算 **128MB** + LRU 淘汰最久未打开的 session；淘汰代价 = 该 session 下次打开退回**全量冷路径（full + gzip，v14 口径）**，无正确性损失；**无 TTL（OD-3 已定案 v15：时间不是正确性判据，理由见 §4）**；**缓存目录标记排除 iCloud 备份**（可重导出的派生数据）；
- Phase 划分沿用：Phase A 存 v1 page 的 turn rows + scalar state；Phase B 拆 summary / detail rows。

#### 3.4.2 打开状态机（restore-then-increment）

```text
BaseCacheHit（磁盘命中：身份 + schema 校验通过——重开的唯一常规入口。
  v11 补 F-30：「切走再切回」必经此路径——projectionStore.clear 在切走时无条件执行
  （全仓唯一调用点 SessionManagement.swift:232）；内存态不参与打开状态机）
  ├─ restore 两步（v11 补 F-29）：
  │    ① replica 层 applyFull(forceReplace: true) 注入基底
  │    ② store 层 commitIfObserved(lifecycle: .ready, changeSet:)——缺此步则
  │       State.lifecycle 停留 .disabled，renderProjectionFromStore
  │       （ChatViewModel.swift:1080，guard 含 sessionSyncV2Active）静默丢弃渲染 = 空白屏
  ├─ 显式重置 appliedRev（§3.4.3 第 2 条）→ 显示基底 + 「正在同步」标记（**OD-1 已定案 B，v15 回填**）
  ├─ anchor-newer(anchorTurnId=tailTurnId, 含锚点)（§3.8）
  │    ├─ 成功（含空页 = 零新增，anchorRefresh 仍刷新末轮终态）→ apply turns/anchorRefresh/changedTurns/removedTurnIds
  │    │      → correctionsPending（v23 F-48；v24 改：对账续页不重复 anchorRefresh、可交付 turns 剩余片（水位线 = 已交付链末端）、自包含 cursor 复查，§3.8 打包/续页规则）→ 续页全部到齐
  │    │      → 对账完成（无未应用的修正）→ 更新水位线 → 滑动淘汰 → 写盘 → 清 syncing 标记（v19 F-42：对账完成才清）
  │    ├─ hasNewer 且 ≤2 页 → 串行续拉 append（超限 → ↓FallbackFull）
  │    └─ typed stale（anchor 离开 Mac 保留区）→ ↓FallbackFull
  └─ epoch ≠ 当前：不 miss、不清缓存，stale 标记升级（「上次同步于 Mac 重启前」），流程照常

BaseCacheMiss（无缓存 / schema 失效 / bridgeId 变）
  └─ 全量冷路径（full + direct gzip，v14；与首开同路径）：replica 整体重建
     → 缓存取有界尾部滑动窗 + 写水位线（首开产物 = 增量链种子，按设备各一份）

IncrementFailed（增量网络失败）
  ├─ 基底保持显示 + 显式 stale / failed 状态
  └─ 仅重试有界增量页（不回 full、不清缓存）
```

关键点（v9 五点的 v10 修订版）：

1. 缓存只能经 `applyFull(forceReplace: true)` / `applyWindowPage` 注入 replica（不变量 2），不得绕过 writer 直接驱动显示；
2. cached 内容不改变 `sessionSyncV2Active`；cache 命中不能清除真实错误；
3. syncing/stale 期间输入框 / 执行动作按现有 projection 权威语义处理——真相在 Mac，增量页是权威对账点，不会把未对账内容当 ready 提交（与 v9 安全性论证同构，语义从 preview 升级为基底）；
4. epoch 不匹配只升级 stale 显示、不 miss 不清缓存（安全性依据同 v9：runtime 重启不改变 transcript source 身份；若新 epoch 下 session 被 compact / 重写，anchor typed stale 或权威页经 replica 整体替换兜底）；
5. **基底与 Mac 真值漂移的对账路径（v19 重写 F-42——原「更早 turn 的滞后突变由打开后的常规 live patch 流自愈」不成立：window 路径走 `BeginProjectionSnapshot`（非 WithResume，`projection_window_handler.go:168`），fence 只保证 post-cut patch 顺序，**离线期间**对旧 turn 的修改不会被重放、稳态 sinceRev 已到 head 也取不到）**：漂移修正完全依赖 §3.8 的 **baseDigests 全基底对账契约**——缓存保存时存储 Mac 生成的 per-turn digest，重开请求回传全部缓存的 digest，Mac 比对后经 `changedTurns`（离线修改的当前 upsert）/`removedTurnIds`（tombstone）返回修正；compaction → anchor typed stale → FallbackFull 整体替换。**syncing 标记的清除以对账完成为前提**：增量页（含空 turns + anchorRefresh）到达且 changedTurns/removedTurnIds 应用完毕后才清除——不得因「后续可能收到 patch」提前清除（R-7）。
6. **打开路径与 `switchSession` 的边界（v11 补，F-30）**：**不得为打开路径改动 `switchSession` 的 clear 语义**——clear 防的是 `isActive`/`sessionSyncV2Active` 跨切泄漏（调用点注释明写，store 为进程级 per-session 表），改动属需单独论证的设计变更（会重新引入泄漏面）；重开必经磁盘恢复是常规成本（见 §3.4.1 内存层注），不是待消除的缺陷。

#### 3.4.3 restore 实施细节与切换 session

1. **注入入口 = 两步（v10 登记 replica 层、v11 补 store 层，F-29）**：① replica 层 `ProjectionReplica.applyFull(_:sessionId:forceReplace:)`（`ProjectionReplica.swift:193-212`）——`forceReplace: true` 恰好绕开 `projection.syncRev < current.syncRev` 的 stale 判断；② **store 层 `commitIfObserved(sessionId:projection:appliedRev:lifecycle: .ready, changeSet:)`**——对齐仓内既有调用点纪律（applyFull 后 `ProjectionStore.swift:1044-1056`、applyWindowPage 后 `:620-626` 均紧跟此步）。只做①不做②的后果：replica 的 change-set 虽携带 lifecycle（apply outcome 硬编码 `.ready`），但 store 的 `State.lifecycle` 不更新、UI 不被通知——`renderProjectionFromStore`（`ChatViewModel.swift:1080`，guard 含 `sessionSyncV2Active`）静默丢弃渲染，**restore 成功但屏幕空白**；
2. **跨 epoch 恢复 = 原子新 epoch 采用事务（v19 重写 F-44——原「显式重置 appliedRev」不够：`applyingWindowPage` 非空页取 `max(旧 syncRev, page.syncRev)`（`SessionProjectionWindow.swift:173`）、空页 `return self`（`:123-126`）——只重置 appliedRev 会留下 projection 携带旧 epoch/大 rev 的混合身份；`revisionKey = epoch|syncRev`（`SessionProjection.swift:696`）与 presentation（`ProjectionPresentationEngine.swift:378/:390`）会把旧身份传播给 diff 与缓存，后续 full 的 stale 比较读旧 `projection.syncRev`（`ProjectionReplica.swift:199`）会拒绝新 epoch 小 rev 的权威投影）**：
   - **采用事务内容（同一 MainActor/store 串行域内一次完成，不可被 patch 中途插入）**：① 以**身份改写副本**执行 `applyFull(forceReplace: true)`——副本内容（turns/scalar）不变，但 `bridgeEpoch = 当前 epoch`（hello_ack 携带，`server.go:780`）、`syncRev = 0`；② `appliedRev = 0`；③ coverage ledger 清零；④ 缓存**文件**保留原 provenance（epoch/syncRev 仅作显示 stale 分级）——「显示缓存的 provenance」与「已采用的当前 projection 身份」由此分离；
   - **后续链路（v20 改 F-44——原「空页保持 0 直到下一条 patch」被 R15 否决：零新增对账响应携带当前 head（如 rev 5），replica 把 appliedRev 设 5、projection 若走空页 `return self` 保持 0 → 混合身份仍在）**：**对账完成事务（第二阶段）无论 turns 是否为空，都显式把 projection 的 `bridgeEpoch`/`syncRev` 设为响应值、应用 scalar/修正集，一次性发布一致状态**——恢复路径使用专用 apply 语义（合并 turns/changedTurns 后**无条件设置 head**，不复用 `applyingWindowPage` 的空页 `return self` 与非空 `max` 分支）；此后 `revisionKey`、presentation、后续 full 的 stale 比较全部在新 epoch 数轴上；稳态 `pullDeltaOrFull` 的 sinceRev = 响应 head（否则退 **34MB full snapshot**——增量架构自埋的事故回退路径）；
   - **per-session 恢复事务门（v20 新增，F-44——@MainActor 只串行同步片段，复用链含 `await replica.applyWindowPage`（`ProjectionStore.swift:612-619`）与 `await presentationEngine.apply`（`:1180/:1197-1203`），等待期间同 MainActor 的 patch/recovery 方法可重入）**：恢复开始 → 首个对账响应发布完成期间，store 持有 per-session 恢复门（串行队列或版本号校验）：期间到达的 patch/recovery push **排队**待门释放后按序 apply，或按 rev 版本校验丢弃重排；早到的 push 不得按恢复用 rev=0 校验（否则现有 `applyFrame` 的 base mismatch 会安排恢复 pull，形成恢复风暴）；
3. **切换 session**：现有 `switchSession` 调用 `projectionStore.clear(sessionId:)` 清离开的 session（防跨 session 泄漏，保留）；基底缓存不随之删除——切回同一 session 时走 BaseCacheHit 路径（v9 §3.4.3 语义保留，路径升级为 restore + increment）。

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

### 3.6 传输压缩定位（v14 反转：Direct gzip 入scope）

保留现有：

- Relay Mac→client：`relay_gzip_v1`，≥32KiB 且压缩更小才启用；
- Relay chunks：gzip 后分片与公平调度。

**新增 S-8：`direct_gzip_v1`（v14，owner 方向变更 #2——首开回到全量后，Direct 传输体积必须压）**：

- 形态 = **应用层逐帧压缩，镜像 `relay_gzip_v1` 纪律**：≥32KiB 且压缩更小才启用、仅 Mac→client 大响应、经 additive capability 协商（hello echo）；iOS 解压复用 Relay 路径既有 gunzip 代码；
- 已否决的替代：标准 `permessage-deflate`（WebSocket 扩展）——压缩双向全帧、上下文内存与阈值为传输层全局行为，可控性与可测性低于应用层逐帧（沿用仓内 relay 的既有模式更一致）；
- v3–v13 的「不新增 Direct gzip」立场由本裁决推翻（其理由 2「v1 window 已把样本降至 3.79MB」不再成立——首开已不走 window）；理由 4 仍然成立并因此更关键：**gzip 只压网络，不消 iOS JSON 对象图与 presentation 成本——S-5 单次解析是首开达标的另一半，两者缺一不可**；
- **压缩帧自描述机制（v16 定案，F-31——R11 阻塞项）**：relay 的 gzip 标记住在 relay envelope 的 `contentEncoding` 字段（`relay_conn.go:296/:304/:313`，且进 AAD）；direct 的 `WireMessage`（`go-bridge/types.go:13-33`）无该字段、`Conn.SendJSON` 直写 JSON text 帧——压缩后的帧不再是可读 JSON，字段方案存在自指矛盾。**定案：WebSocket opcode 区分（新增约束只作用于 Mac→client 方向，v17 更正 F-36）**——Mac→client：`text` 帧 = 原始 JSON（Mac 侧现状），`binary` 帧 = gzip(JSON envelope) 整帧压缩（新增，仅发送给**已协商 `direct_gzip_v1` 的连接**，gorilla `WriteMessage(BinaryMessage, …)`）；iOS 按 opcode 分流（text → 现行解析；binary → gunzip → 同一解析路径）；阈值判定在序列化后字节上（≥32KiB 且压缩更小）。**方向澄清与现状事实（v17 更正 F-36——v16 曾误写「client→Mac 恒为 text」）**：压缩只发生在 Mac→client（镜像 relay 纪律）；**client→Mac 现状就是 binary 帧**——iOS `send()` 即 `sendFrame(opcode: .binary, …)`（`BridgeWebSocketConnection.swift:304-306`），Mac 读循环丢弃 opcode（`server.go:670` `_, raw, err := ws.ReadMessage()`）对 text/binary 一视同仁按 JSON 解析，因此该方向错写不会导致功能失效（也正因如此测不出来）；**client→Mac 维持现状 binary，不在本方案变更边界内，不得顺手改成 text（无理由的行为变更）**；冻结断言「未协商连接零 binary 帧」**明确限定 Mac→client 方向**（client→Mac 的 binary 帧是既有现状，不属断言范围）。**否决的替代（理由随行）**：① `WireMessage` 加 `contentEncoding` 字段——字段在 JSON 内部，读取前必须先解压/先按 JSON 解析（鸡生蛋）；改用外层信封 + base64 载荷会放大约 33% 体积并双重编码；② gzip magic number 嗅探（`0x1f 0x8b`）——启发式嗅探违反「不猜」纪律（R10），未来非 gzip 二进制帧会误判；③ `permessage-deflate`——v14 已否决（双向全帧、上下文内存、传输层全局行为，可控性/可测性差）。opcode 是 WebSocket 协议原生语义（非启发式），旧客户端零影响（未协商连接永远收不到 binary 帧）；
- 冻结：独立批次（协议文档新章节**含 binary 帧语义与 capability 协商** + **binary fixture 样本**（gzip 字节 + 其解压后 JSON，README 记哈希）+ 断言（Mac：阈值/更小才压/未协商连接零 binary 帧；iOS：binary → gunzip → 与 text 路径输出深度相等）+ `schemaRevision`），不得改变 `relay_gzip_v1` 语义。

### 3.7 window 路径的会话级标量状态交付（v3 新增，F-11；v4 定案 = W1，OD-6 已回填冻结）

**现状缺口（全部亲核）**：`window_0` 是 iOS 冷开唯一入口（`ProjectionStore.swift:763-777`），但

1. Mac `ProjectionWindowResponse`（`projection_window.go:77-82`）只有 `Window/Turns/SyncRev/Resume`；`sliceProjectionWindowWithUpstream`（`:226-287`）与 `projection_window_handler.go`、`projection_window_older_hydrate.go` 零处引用 `Execution`；
2. iOS `applyingWindowPage`（`SessionProjectionWindow.swift:116-176`）只合并 turns；`emptySession`（`:96-98`）把 execution 硬编码 `.idle`；`ProjectionReplica.applyWindowPage` 构造的 change set 全部 `execution: nil`；
3. fence 完成事务对不持有该 patch 任何 turn 的 window 连接，排队（fence pending）的 **decide=true** frame 被显式清空 `Execution`（`projection_snapshot_fence.go:224-235`；decide 路由维度见 W1 fence 语义，v5 补 F-20）；
4. 消费链全部读 `projection.execution.phase`：`isSessionExecuting`（`ChatViewModel.swift:2419-2426`）、输入框锁、`composerAwaitingUserAction`（`ChatViewModel+TaskDock.swift:33`）、session badge（`ChatViewModel.swift:1227-1233`）。

后果：window 协商修好后（v3 语境为「S-1 修好协商后」；**v14 起：冷路径全量原生含 execution，该缺口只由 anchor 增量页承载，见 §3.8 与 §3.7 v14 范围注**），运行中的 Claude session 若经 window 路径首开将 turn 内容正确、但 execution 恒为 `.idle`——不显示执行中、输入框不禁用、`requires_action`（权限卡/提问）不锁发送。iOS 有独立于 projection 的 `runtimeStateStore` badge 路径（`ChatViewModel+TaskDock.swift:36-39`）与 `isGenerating` 本地状态，不是完全无显示，但与 projection 分支形成**双源不一致**。

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
  | 新 Mac + 旧 iOS | ✓ | ✗（旧 iOS 未声明 scalar） | 旧 iOS 只查 window echo → 走 window、收不到 `scalarState` | **既有缺口形态**（= S-1 修复前若协商生效的形态；E-3 取证将确认当前安装组合实际从未走到 window）。定性：既有问题，由 iOS 版本升级解决，不在默认交付范围（v17 改 F-37） |
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

  且 `docs/protocol/bridge-v1.md:2328-2332`（R10 — Freeze scope）明写：**「Open product semantics discovered later are BLOCKERS to be re-frozen here first; implementing agents MUST NOT guess them in code」**——scalar state 交付正是 later-discovered product semantics，属 re-freeze 范畴而非普通 additive 字段。**处置：re-freeze 与 W1 同批交付（登记时语境为「Gate A / S-1 前置」；v14 后随批次降级 contingency，见下方范围注——v17 去语境 F-37）**，批次内容六项（v8 由五项扩为六项，F-27）：① `docs/protocol/bridge-v1.md` §Projection Window 新增 scalar 子章节，冻结 `scalarState` 形状（字段、必选/可选、与 `SessionProjection` scalar 字段的镜像关系、nil 语义、`projection_window_scalar_v1` 协商规则）；② `docs/protocol/schema/bridge-v1.types.ts` 更新；③ 冻结样本 `window-response-spec.json` 新增含 `scalarState` 的样本条目（沿既有 `synthetic-spec-fixture` provenance 纪律，首个真实 wire capture 后按 README 纪律替换，衔接 E-4）；**该样本是两仓独立副本（v7 补，F-24）**——Mac `docs/protocol/samples/projection-window-v1/window-response-spec.json` 与 iOS 仓同路径副本（实测同哈希 `6853e4ac…`，iOS 测试经 `fixtureRoot` 读本仓副本，`ProjectionWindowSpecTests.swift:34/:43/:85`）——两份必须同步更新并保持字节一致；④ Mac spec 测试冻结常量/断言更新为含 `scalarState` 的新冻结集（`:46-57`、`:73-75`、`:198-211`）；⑤ iOS spec 测试同步：response 级 key 集合断言 `ProjectionWindowSpecTests.swift:48` 与 schema 冻结集 `:129-130`；`:51`（window 描述符 key 断言）与 Mac `:77-80`（描述符级 DeepEqual）**不受 `scalarState` 影响、无需改动**（v7 更正 v6 误引）；⑥ **`schemaRevision` 纪律（v8 补，F-27）**：按协议 pack 惯例 bump `BridgeProtocolSchemaRevision`（现停在 `2026-09-05`，`go-bridge/bridge_v1_schema.go:77`）并在该文件注释块累积本次 additive 变更记录（v9 更正计数，F-28：既有注释块为 **13 条日期条目 / 8 个不同日期**（`2026-07-24` / `07-29` / `08-01` / `08-02` / `08-22` / `08-23` / `09-05` / `09-06`），同日多次变更按 `（同日二次返工）` / `（同日三次返工）` 标注，故**条目数 ≠ 日期数**；实测口径 `grep -c "^	// 20[0-9][0-9]-" go-bridge/bridge_v1_schema.go` = 13。v8 曾记「既有六条」为计数偏差。追加动作与既有条数无关：按 `<日期>: <变更摘要>` 追加一条即可）；**边界声明**：README `docs/protocol/README.md:15` 记 `2026-10-06-r1` 与代码常量的不一致是**既有已知分歧**（`git log -S` 证明代码从未取过该值；本方案之前已存在；字段纯信息性，`server.go:716`/`hello_handler.go:188` 只填充不比较、不参与门控）——**不得在 S-1 变更边界内顺带「修正」README**，登记后独立处置，防止无关分歧卷入本批次变更。**更新冻结物、不删除断言**——schema 漂移防护对新冻结形状继续生效；**范围注（v14，owner 方向变更 #2）**：`window_0` 不再是首开主路径（contingency 除外）——**本节 re-freeze 批次（①–⑥）降级为 contingency 触发项**（`window_0` 被启用时执行）；`scalarState` 的主交付载体改为 §3.8 anchor-newer 的**新响应类型**（新增 capability/类型/样本，不触碰 frozen v1 `BridgeGetSessionProjectionWindowResult` 字段集——三处冻结断言无需更新）；冷路径（全量首开）原生含 execution。fence scalar 分支与全部 fence 语义不受影响（对 anchor 页同样适用）；
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

### 3.8 window 家族的 anchor-newer 增量协议（v10 新增，owner 方向变更；重开主路径的服务端半边）

**定位**：独立 additive capability `projection_window_anchor_newer_v1`，**不等 S-3 v2 / OD-2**（正交：v2 summary 收口超大 turn 首屏体积，anchor-newer 解决重开增量）；复用 §3.7 已定案的 R10 re-freeze 批次机制、协商纪律（F-26：在 `negotiateProjectionWindowV1` 内扩展，direct `server.go:808` + relay `main.go:693` 双路径自动覆盖；若独立函数必须两处接线 + 双路径断言）与 trace 纪律（F-25：走 window `projection_rpc` trace，纳入 §7.3 重试口径）。

**协议形状**（细节由冻结评审定案）：

- 请求：`direction: "newer"` + `anchorTurnId`（裸 turnId，**无 cursor**）+ `limit`（**默认 100 turns / 4MiB——OD-7 已定案 v15，对齐协议默认，冻结时按实测微调**）+ **`baseDigests`（v19 新增，F-42 全基底对账契约）**：`{turnId → digest}` 映射，覆盖缓存基底的**全部** turns（受缓存上限约束 ≤256 项）；digest 为 **Mac 生成的不透明 token**（如 sha256(json.Marshal(turn)) 截断），随每个曾下发的 turn/page 附带——iOS 缓存原样存储并在下次请求回传，**避免跨语言 canonicalization**；Mac 保持无 per-device 状态（验证数据随请求携带，与不变量 6 相容）；
- **响应（对账增量页，v19 重构 F-42/F-43）**：
- **token 生命周期绑定不变量（v20 新增，F-42 闭合——R15 反例：缓存保存 V2 内容却回传 V1 token，比较「Mac 与 V1 一致」推不出「缓存 V2 与 Mac 一致」）**：**每一缓存行的 (内容, token) 必须来自同一权威交付**，逐来源覆盖：
  1. **full 种子 / anchor-newer 对账响应**：turn 内容与 token 同帧到达（Mac 下发的每 turn 附带其 token），iOS 原样配对存储；
  2. **live patch → token 失效规则（v21 精确化 F-50——同一 patch 内 `upsertTurns + turnStateOps + partOps` 可能产生「最终内容 + 中间版本 token」）**：**token 只能绑定该 patch 全部 ops 应用后的最终 turn 状态**（upsertTurns 提供基底 → stateOps/partOps 就地修改 → 以最终结果为准）——若 patch 为受影响 turn 附带新 token，该 token 必须对应全 ops 后的终态；**未附带新 token 的局部修改（partOps/turnStateOps/detail commit）→ 该行 token 立即置失效（存 `null`）**——对账请求中 `digest: null` 的语义 = 「无有效摘要，请返回该 turn 当前终态」，Mac 必须将其纳入响应并附新 token。**绝不允许「新内容配旧 token」或「终态配中间态 token」**；
  3. **可选优化（re-freeze 冻结时定）**：patch 的 partOps/turnStateOps 附带受影响 turn 的新 token（省一次回传）——绑定不变量不依赖此优化；
  4. **写盘规则**：退后台保存活跃 replica 时**逐行**保存 `(内容, token, tokenValid)`；schema 迁移或缺失 token 的行一律按 tokenValid=false 处理；
  5. **Mac 侧匹配规则**：请求 token 非 null 且与当前摘要相等 → 无修改；非 null 且不等 → changedTurns；**null → 必须回传当前终态**（不得因 token 匹配跳过修正后清 syncing）；
  - `turns`：**严格位于 anchor 之后**的 turns——复用 `growWindowRange(sizes, limit, boundary+1, false)` 现成语义（v19 更正 F-43：v10/v18 的「页包含 anchor turn」表述与该 helper 的排他语义及空页条款矛盾）；
  - `anchorRefresh`：**anchor turn 的当前完整终态（独立字段，v19 新增 F-43）——不计入页 limit/字节预算**；anchor 在保留区即返回（**仅对账首包携带，修正续页不重复——v23 定案 F-48**，见下方打包形状）；遵循 never-split（超大 anchor 可超 4MiB，计量单列、不计入「2 页 ≈2×4MiB」）；**anchor == tail 且零新增时 `turns` 为空但 `anchorRefresh` 仍携带终态**——解决「空页 vs 刷新末轮」矛盾（缓存保存时仍在流式中的末 turn 离线完成、无新增 turn 的最常见场景由此覆盖）；
  - `changedTurns`（v19 新增 F-42；v20 补预算与分页 F-46；v28 措辞同步 F-60——原来源限定词「baseDigests 中」为 v19 时代口径（当时复查集 = baseDigests 本身、无二义），v27 全集复查集口径下与单调覆盖②「Mac 对复查集内全部项比对」存在字面张力；同步后本字段句与机制句指向同一集合，载体范围随之明确 = 复查集全部项的当前终态 upsert（含已发项与早页确认项的页间修改，R23 F-60 证据））：**复查集中**与当前内容 digest 不一致或 **token 为 null（失效，必须回传）** 的 turns 的**当前完整 upsert**——覆盖**离线期间**对缓存范围内旧 turn 的修改（live patch 只覆盖订阅后事件，见 §3.4.2 关键点 5 修正）；**受对账响应总预算约束并支持分页**（见下方总预算条目）；
  - `removedTurnIds`（v19 新增）：baseDigests 中已不在 kernel 当前投影的 turnId（compaction/截断的 tombstone，iOS 从基底移除）；**复查集中任何项被移除同经本字段返回（v25 补 F-56、v27 随全集口径扩 F-59——对账事务中途 compaction 移除非锚点 turn 而 anchor 仍在保留区时，被移除项（无论已发/未发/早页确认）的正确处置是 tombstone 而非重发，见单调覆盖②）**；
  - **对账响应预算与打包形状（v20 立 F-46；v21 分层 F-48；v23 增确定性打包算法与修正续页形状——R18：续页是否重复 anchorRefresh 未定义时，46MiB anchor + 9MiB changedTurn 等合法字段组合每页超 48MiB 总硬顶、不可实现）**：
    - **响应总硬顶 = 48MiB**（全字段序列化合计，< 50MiB transport 解压上限留 header 余量；`BridgeWebSocketConnection.swift:12`、`RelayBridgeFrameConnection.swift:823/:859-862`——gzip 不取消解压上限）；
    - **mandatory 预留 = 2MiB**（v23 显式化——envelope + scalar/metadata + removedTurnIds + 分页元数据；任何页（含 never-split 独占页）都先扣除，保证 mandatory 字段永远可发）；
    - **anchorRefresh 独占额度**（never-split 特例）：**≤46MiB（v22 收紧 F-48——原 48MiB 与总硬顶相同，未给 envelope/scalar/metadata 留空间）**；超 46MiB → typed `reconciliation.oversized` → FallbackFull；
    - **changedTurn 同样 never-split（v22 补 F-48）**：修正分页按**整 turn 粒度**（不截断单 turn）；单个 changedTurn 可独占一页修正额度（单 turn ≤46MiB，超出 → typed oversized → FallbackFull）；
    - **对账预算（turns + changedTurns + scalar/metadata）≤ 8MiB / 每页**——anchorRefresh 不计入（v21 改）；
    - **确定性打包顺序（v23 新增 F-48——同一输入只允许一个合法页序列；v26 补相位边界定案 F-58——R21：原文本未显式定义「装不下的 turn 之后、本页剩余空间如何处置」，§7.1.5 验收③括注曾暗示跨相位回填，与唯一合法页序列宣称相悖——一般场景（大 anchor + 装不下的后继 turn + changedTurns 并存）下两种读法产出两个不同页序列）**，按序填充、装不下即切页——**相位边界（F-58 定案）：turn phase 内某 turn 装不下当前页剩余对账空间时，本页 turn phase 即终止（该 turn 溢出至后续页，never-split 独占或续页普通片），当前页剩余空间不回填：correction phase（changedTurns）不得越过未交付的 turns 提前装页——即使存在能装入剩余空间的更小项；turns 全部交付完毕后 changedTurns 才按序进入（可与末个 turns 同页，同首包 anchorRefresh → turns → changedTurns 的页内顺序）**：
      1. 扣除 mandatory 预留 2MiB；
      2. **首包**：`anchorRefresh`（**唯一一次**，见下条）→ `turns`（严格后继，受 limit/4MiB）→ `changedTurns`（按 turnId 升序）；**turns 与 changedTurns 使用本页剩余对账空间 = min(8MiB, 48MiB − 2MiB − anchorRefresh 序列化大小)；scalar/metadata 按已定义预算计入，anchorRefresh 不计入对账预算——另按独占额度与总硬顶计量（v30 措辞对齐 F-62——原「三者合计」字面把 anchorRefresh 重复计了一次，与上条「anchorRefresh 不计入」定义冲突；额度、顺序、续页行为与验收均不变）**——大 anchor 挤压首包时对账内容可少于 8MiB 甚至为 0，剩余全部 `correctionsPending`；
      3. **对账续页（correctionsPending 翻页；v24 改写 F-48——R19：原「不携带 turns」使 46MiB anchor + 任意后继 turn 无交付路径——首包空间 0、续页无 turns、水位线无法前进，下一请求仍以同一 46MiB anchor 开始 = 死锁；后继 turn 交付是水位线前进的正常主路径，非仅 changedTurns 边界）**：**不携带 anchorRefresh**（anchor 开销只付一次），**可携带 turns 剩余片**（turn phase：严格后继、R7 链序、never-split——单个后继 turn 超 8MiB 走整 turn 独占页，frozen v1「单 turn 超限整 turn 单独成页」语义同源）**与 changedTurns 剩余片**（correction phase：按 turnId 升序）；同页内 turns 先于 changedTurns 填充（与首包顺序一致）；removedTurnIds/scalar/head/修正 cursor 随页携带；**对账事务内 turns 总页数计入「超 2 页退全量」判定**（OD-7 口径——首包即能判定时首包 typed FallbackFull，事务中途 live 新增致超限时中止事务 → FallbackFull）；
      4. 全字段序列化 ≤48MiB 硬顶终检（普通内容超 → 切页；never-split 字段超独占额度 → typed oversized）；
    - **续页数据来源与绑定规则（v24 重写 F-52——R19：原「同 kernel snapshot cut」不可实现：现有 `BeginProjectionSnapshot` 每次取当前 `kernel.Snapshot`（`projection_kernel.go:1887-1902`）、fence 完成即释放不跨请求持有（`projection_window_handler.go:168-210`）、拟议 bbolt `turns/items` 均为当前行、无 (cutRev, turnId) 历史版本（§3.2.2）——「同 cut」宣称撤回，共享 cut artifact / 版本化存储路线经比较后否决：生成/TTL/去重/崩溃恢复均为新增重机制，且与不变量 6 的张力需要额外论证）**，改为**自包含 cursor + 单调覆盖 + 已发 token 复查**：
      - **自包含修正 cursor（v27 更正 F-59——原「原始 baseDigests 剩余未对账项 ∪ …」的收缩限定与 §7.1.5 验收①的全集口径定义了两个不同集合：早页复查命中（确认无修改）的项按收缩读法移出后续请求后，页间被 live patch 修改时既不在复查集（不会重发）、也不是本事务已发项（单调覆盖②不适用）、其新终态不在任何对账页内——唯一交付载体只剩恢复门排队的 patch，而该 patch 的 rev ≤ 采用事务采纳的最后一页 head（修改发生在末页快照之前），走 §3.4.3「或按 rev 版本校验丢弃重排」的合法丢弃分支即丢内容、projection 声称已达 head、syncing 已清 = R-7 假成功；全集口径以每页多比对 ≤256 个 token 的成本关闭该窗口——早页确认项页间被修改同样由当页复查捕获重发，且末页快照之后到达的 patch rev > head、按版本校验正常应用，对恢复门两个实现分支均稳健）**：续页请求携带 `anchorTurnId` + **完整复查集 = 原始 baseDigests 全部项 ∪ 已发项的 `(turnId → 客户端已收 token)`（含首包 anchorRefresh 的 turn 与已交付 turns）——复查集不随页收缩，每续页携带全量**；**续页身份显式标记（v29 补 F-61——原请求字段清单仅 `anchorTurnId` + 复查集，续页请求与全新对账首包在 Mac 侧不可区分，「续页不携带 anchorRefresh」（anchorRefresh 条目与打包 3）缺 Mac 侧判定输入；行为已被规范句 + 组合验收④双锁定，空白仅在 wire 形状层——与 F-55 同型）**：续页请求显式携带续页身份 = **回传上一响应的修正 cursor**（v20 F-46 原案「correctionsPending + cursor，anchor 不变续拉」的请求侧重申；v23 起响应侧「修正 cursor 随页携带」已定）；Mac 以该标记区分首包与续页——**无标记 = 全新对账事务首包（携带 anchorRefresh），有标记 = 续页（不携带 anchorRefresh）**；Mac **无 per-device 状态**（不变量 6 相容——全部判定输入随请求携带，每次请求独立可重放，换连接/崩溃后重发同一 cursor 结果幂等）；
      - **每续页独立取当前 committed head 切片**（现有每请求 `BeginProjectionSnapshot` 机制原样复用——不需要跨请求 fence、不需要历史版本存储、不需要共享 cut artifact）；
      - **单调覆盖一致性**（替代「同 cut」的正确性论证）：① kernel 是 turn 终态投影、committed head 单调前进——晚页 cut 的 turn 内容 ≥ 早页（相等或更新）；② Mac 对复查集内**全部项**比对当前 digest（v27 扩 F-59——原「已发项」限定与全集复查集不匹配：早页复查命中（确认无修改）的项页间被 live patch 修改时同样必须由当页复查捕获，否则其新终态无对账页交付载体，见自包含修正 cursor 的 F-59 注记）：token 失效（页间被 live patch 改动——无论该项是否已在早页确认）→ 本页重发当前终态，upsert 覆盖早页内容（幂等）；**复查项不在 kernel 当前投影（v25 补 F-56、v27 随全集口径扩为全部复查项——事务中途 compaction 可移除非锚点 turn 而 anchor 仍在保留区、不触发 typed stale）→ 经 `removedTurnIds` 返回 tombstone（iOS 从基底移除），不重发**；未发项继续按序发送；③ iOS **对账完成事务（§3.4.3）在全部续页到齐后一次性提交，采用最后一页的 head/scalar**——早页内容被晚页覆盖项以晚页为准，未被覆盖项 = 已通过复查的终态；④ **typed stale 与 epoch 中止的判定主体（v25 定案 F-55——原「anchor 离开保留区 / epoch 变化 → typed stale」未区分谁判定，epoch 分支在 Mac 侧不可执行：续页请求不携带 epoch 身份，Mac 无从比对）**：**anchor 离开保留区 → Mac 侧 typed stale**（复用 `cursor_stale` 族——判定输入 = `anchorTurnId`，随请求携带、请求侧可执行）→ 整组放弃、FallbackFull，**不发布部分采用状态**；**epoch 变化 → iOS 侧判定中止**（bridgeEpoch 是 runtime 启动身份，epoch 变化必伴随连接断开——iOS 重连后经 hello_ack 取得当前 epoch（direct `server.go:780`；relay 路径同携带，`main.go:640`——v26 补 R21 亲核锚点，iOS 生产 Relay 路径同样可执行），与对账事务开始时记录的 epoch 不一致 → iOS 主动整组放弃、FallbackFull，同样不发布部分采用状态；**Mac 侧不比对 epoch、续页请求不携带 epoch 身份**——该分支的保守性来自跨 epoch 事务的交付链复杂性，非正确性必需：若 iOS 判定遗漏（epoch 已变而续页照发），复查集自包含幂等 + 采用事务取最后一页 head 仍收敛到当前 kernel 真值，不产生部分采用错误状态）；与 §3.4.2 打开时点的「epoch ≠ 当前：stale 标记升级、流程照常」分支的区分：后者判定的是**缓存 provenance 陈旧**（restore 前、数据本身可能仍正确——runtime 重启不改变 transcript source 身份），前者判定的是**对账事务交付链跨 epoch**（首包 head 已过时）——两个时点、两种后果，实施时不得混用；
      - held 注册覆盖全部续页交付的 turns 与 changedTurns（v23 扩展维持）；
    - **超限恢复语义（v24 新增 F-53——R19：>46MiB 项回退 FallbackFull，但 full projection 本身可能超 iOS 50MiB 逻辑消息/解压硬顶（`BridgeWebSocketConnection.swift:12` / `RelayBridgeFrameConnection.swift:823/:859-862`——gzip 只压网络、不消解压上限），不可传输的 full 不得写成恢复成功路径）**，两级定案：
      1. **typed `full.oversized` 显式终态失败**：Mac 端 full 序列化 > 当前协商传输逻辑上限（`projection_max_bytes`）→ 返回 typed `full.oversized`，iOS 显式失败态（「会话超出可传输上限」——R-7 失败显式可见：不假成功、不部分渲染、不静默回 legacy）；触发链完整形态：`reconciliation.oversized` → FallbackFull → full 也超限 → `full.oversized` 显式失败（两级 typed，不冒充成功）；
      2. **能力化上限协商 `projection_max_bytes`**：hello 协商（两端均 CordCode 实现；direct 逻辑消息上限与 relay 解压上限同值提升）；**默认维持 50MiB（现状），提升档（如 256MiB）在 S-7/S-8 冻结时定**（数值归 OD-7 冻结微调口径）；内存代价（解压 + 解析峰值随上限走）由 S-5 单次解析承接、§8 风险表登记；**contingency `window_0` 不是恢复路径**（window 响应走同一 WebSocket、受同一上限——46MiB 级单 turn 对它同样不可传输，不引用）；
    - 对账未完成（correctionsPending 未拉完）不得清 syncing / 判「已对账」（v20）；
    - 同 turnId 出现于 anchorRefresh/turns/changedTurns 多处 → 合并为一条；R15 膨胀反例（64×1MiB ≈ 64MiB changedTurns）由对账预算 + 分页承接；
    - 数值（48MiB / 46MiB / 2MiB / 8MiB）登记 OD-7（v21 补登、v23 补打包形状，恢复数值微调口径）；
  - `scalarState`：同前（execution 等）；
  - R7 严格链序不跳过未加载 turn；older 侧空页先例（`pageEmptyNewerAnchor`）语义保留；
- **分页前进（F-43 闭合；v24 补 F-48——R19：对账事务内水位线 = 已交付链末端，解除大 anchor 死锁）**：下一请求的 anchor = 本页（`anchorRefresh` ∪ `turns`）的最后一个 turn——`turns` 严格后继保证只要存在新 turn 页必然前进；**对账事务内（correctionsPending 未完结）水位线 = 已交付 turn 链的末端**——首包只交付 anchorRefresh 时 = anchor 自身；对账续页交付 turns 后 = 其最后一个 turn（46MiB anchor + 1KiB 后继场景由续页交付该 turn 后水位线前移至正常大小 turn，后续请求 anchorRefresh 恢复正常，死锁解除）；**超大 anchor 只膨胀 anchorRefresh、不阻塞 turns 交付**（limit=1 / anchor 占满预算均不会停滞）；水位线更新同样取页尾；
- **typed stale**：anchor 不在 kernel 保留区（R6 retention miss）→ 复用 `cursor_stale` 错误族（语义 = 水位线失效，客户端 fail-closed 回 **FallbackFull**，v19 更正 F-41，不假成功）；
- **服务端实现**：`newer` 分支入口增加「`anchorTurnId` 直接锚定」形态（现只能从 epoch 绑定的 opaque cursor 进，`projection_window.go:278-296`）——切片逻辑复用 `growWindowRange(sizes, limit, boundary+1, false)`（现成）；**`locate(anchorTurnId)`（`:297-301`，anchor 失效 → `locate_out_of_window`）已证明裸 turnId 锚定在冻结协议中存在先例**——anchor-newer 不是新发明，是把既有锚定能力扩展到 newer 方向；
- **held turns 注册（v16 补，F-34）**：anchor 增量页的完成路径必须复用 window 页的同一完成事务语义——`CompleteProjectionSnapshotWithHeldTurns(conn, admission, requestId, response, turnIDs)`（`projection_window_handler.go:201-211`；P0-2 审计注释明写：held 集合必须在 fence release 内注册，漏注册会使并发 detail commit 被路由成 no-op revision patch、**"permanently strand the detail"**）——增量页的全部 turns（含锚点 turn 自身）注册进该连接的 held 集合，使后续针对这些 turn 的 detail commit（decide=true）不被 fence 清空；**对账续页交付的 turns 剩余片与 changedTurns 同样注册（v23 补、v24 扩 F-48——续页经同一完成事务投递，漏注册同样 strand detail）**；§7.1.5 补对应断言；
- **re-freeze 批次**：与 §3.7 六项同构（协议章节新增 anchor-newer 小节 / schema 新类型 / 冻结样本 / Mac spec 断言 / iOS spec 断言 / `schemaRevision` bump + README 既有分歧边界声明）——**更新冻结物、不删除断言**；**冻结清单显式定案（v25 补 F-55）：对账事务的 epoch 中止分支由 iOS 侧判定（hello_ack epoch 比对，§3.8 单调覆盖④），续页请求不携带 epoch 身份、Mac 侧不做 epoch 比对——协议文档冻结时写明该语义，实施者无需自行发明 epoch 携带方式**；**续页身份标记字段随全集复查集请求字段形状一并冻结（v29 补 F-61——冻结评审须能直接写出首包/续页两种请求形状，实施者无需自行发明「续页如何不被当作首包」的判定方式）**；本切片批次为**主交付**（W1 的 v1-响应批次已随 v14 降级 contingency，不存在合并对象——v16 更正 v10 的「可与 W1 合并」表述）。**冻结样本用独立文件（v16 补，F-33）**：`docs/protocol/samples/projection-window-v1/anchor-newer-response-spec.json`（两仓同步副本、字节一致）——Mac/iOS 既有 fixture 断言固定读 `window-response-spec.json` 并做**精确 key 比对**（`projection_window_spec_test.go:33/:73-75`、`ProjectionWindowSpecTests.swift` fixtureRoot 读取点），追加新条目进同一文件必然失败；spec 测试为 anchor 样本**新增对应读取点**，不动既有文件与其断言；
- **iOS 消费**：`pullOpenWindowed` 前置基底缓存路径（§3.4.2）；eligibility 在 W1 双维度之上增第三维度（anchor-newer echo）——**未 echo 时回退全量冷路径 FallbackFull（v19 更正 F-41——原「回退 window_0 不回 full」与 v14 定案矛盾，且 window_0 无默认 scalar 补齐）**；水位线即 §3.4.1 的 `tailTurnId`。

**与 S-3 v2 的关系**：两者共用 anchor 语义（v2 请求草案已含 `anchorTurnId`），冻结评审时可统一 anchor 参数定义，但 capability 分开协商、互不阻塞。

### 3.9 打开/回退路径总表（v19 新增，F-41——同一触发条件只有一个默认动作，本表为唯一权威）

| 触发条件 | 默认动作 | 能力门 | 返回形状 | 失败行为 |
| --- | --- | --- | --- | --- |
| 首开（本设备无缓存 / 身份或 schema 失效 / bridgeId 变） | **全量 full + direct gzip**（`get_session_projection`，现行 RPC） | 无新门（`direct_gzip_v1` echo 则压缩，否则明文——两者均为合法响应） | `SessionProjection`（原生含 execution 等 scalar） | 显式失败；网络/hydrating 重试计入指标；**超长 session 触顶 15s → contingency `window_0`**（启用前置 = §3.7 v1 响应 scalarState 冻结批次 + §8 风险登记） |
| 重开·水位线有效 | **anchor-newer 对账增量**（含 baseDigests 全基底对账 + anchorRefresh；**对账续页可含 turns 剩余片与 changedTurns（v24 F-48/F-52——自包含 cursor，不携带 anchorRefresh）**） | anchor echo（eligibility 第三维度） | `turns`（严格后继）+ `anchorRefresh`（仅首包）+ `changedTurns`/`removedTurnIds`（对账修正）+ `scalarState` | typed stale（anchor 离开保留区）→ **FallbackFull**；full 也超 `projection_max_bytes` → typed `full.oversized` 显式失败（v24 F-53）；网络/hydrating 重试有界增量页 |
| 重开·增量 >2 页（≈2×4MiB，不含 anchorRefresh） | **FallbackFull** | — | 同首开 | 同首开 |
| 重开·anchor capability 未 echo | **FallbackFull**（不回 window_0） | — | 同首开 | 同首开 |
| 稳态（打开后） | live patch 流 + `pullDeltaOrFull` | — | patch / delta | 现有语义 |
| contingency `window_0`（仅超长触顶时启用） | window_0 尾窗 | 启用前置：v1 scalarState 冻结批次（§3.7，contingency 触发项） | window 页 + scalarState（补冻结后） | cursor stale → 重发 window_0；**无 scalar 时执行态显示为既有缺口（§3.7 组合表），启用前置即为此而设** |

**一致性规则（F-41 闭合）**：正文任何章节与本表冲突时以本表为准；「有界」约束适用于重开增量页与 contingency 尾窗，不适用于首开/FallbackFull（其边界 = 15s cap + gzip + S-5）；旧 peer 组合行为仍见 §3.7 组合表（W1 语境）。

## 4. 决策表

| ID | 问题 | 选项与推荐 | 状态 | 影响切片 |
| --- | --- | --- | --- | --- |
| OD-1 | cache 命中后网络未完成时是否显示旧内容 | A. 只显示 loading；B. 显示 last-good + syncing watermark（推荐）；C. 显示旧内容且无提示 | **已定案 B（2026-10-17 owner 裁决「按推荐」，v15 回填）**。理由：① **A 与 owner 明确的重开 UX 要求直接冲突**——重开不应出现全屏加载图标、直接跳到最底部等增量 append；A 等于恢复基底却不给看，放弃增量方向的主要收益；② **C 丢失失败显式边界**——无标记则无法区分「最新」与「同步失败停在旧内容」，违反 R-7 / 不变量 4；③ **B 的安全性已经论证**：基底经身份 + schema 校验才恢复，正确性最终裁判是增量页（typed stale 兜底），同步期间输入/发送按权威语义处理，不把未对账内容当 ready 提交 | S-4 |
| OD-2 | 首屏窗口大小 | A. v1 默认 100 turns / 4MiB；B. iOS 请求 25 turns / 4MiB；C. v2 summary ≤1MiB | **被 v14 方向取代**（首开不再走 window）：limit 语义保留给 anchor-newer 增量页大小，数值并入 OD-7 一并裁决；v2 summary（S-3）不受影响 | S-3（v2）；S-7（anchor 页 limit，经 OD-7） |
| OD-3 | preview cache 是否设 TTL | A. 无 TTL，按 identity/schema/容量淘汰（推荐）；B. 时间 TTL | **已定案 A（2026-10-17 owner 裁决「按推荐」，v15 回填）**。理由：① **时间不是正确性判据**——缓存对错取决于 anchor typed stale 与身份/schema 校验，与保存时长无关（两周前的缓存 anchor 仍在保留区即完全正确；五分钟前的缓存也可能因 compaction 失效）；TTL 会扔掉仍正确的缓存（白白退回冷路径），却不能让已失效的缓存更安全（安全性靠 typed stale + fail-closed）；② **TTL 在最需要缓存的场景最伤**——久未打开的会话重开、弱网/离线时恰是期望「直接看到上次尾部」的场景，TTL 到期强制退回首开体验；③ **TTL 想解决的两个真问题各有更准机制**：磁盘占用由 LRU 容量淘汰（按使用频率，比年龄精准）、会话在 Mac 侧消失由 typed 失效 → 全量冷路径错误处理；④ **仓内先例**（`SessionReadStateStore` 等持久 store）均为身份/schema 驱动、无时间 TTL | S-4 |
| OD-4 | 15s hard cap | A. 保留（推荐）；B. 提高；C. 降低 | engineering decision：保留，等 bounded path 达标后再评估 | S-4/S-7（增量）、S-5/S-8（首开冷路径）、S-6（v17 改指 F-37） |
| OD-5 | 旧大 JSON checkpoint 是否迁移 | A. 不迁移，首次诚实重建（推荐）；B. exact-match 迁移 | engineering decision：不迁移，derived data 可重建 | S-2 |
| OD-6（v3 新增，v4 回填定案，v5 记录条件达成，v6 补 re-freeze 交付条件） | **window 路径 scalar state 交付路线（F-11）** | W1 window 响应 additive 携带；W2 window 后追加 scalar 拉取；W3 runtimeStateStore 单源 | **已定案 W1（2026-10-17，R4 评审轮按 owner 委托定案；R5/R6 复核维持）**。附两个定案条件：① §3.7 W1 fence 语义按代码真实行为重写——v4 按 F-17 达成部分，v5 按 F-20 补 decide 路由维度后完整达成（R5 判定 v4 遗漏该维度；R6 核验闭合）；② Execution-only patch 边界显式处置 = fence scalar 分支（协商 `projection_window_scalar_v1` 的连接不清空 `resolved.Execution`，per-turn 清空规则不变；v5 重定位为**防御未来新增 detail commit 构造点**的防御性分支——现有代码该形态不可达，设计不变），备选「测试排除 + 登记已知行为」路线不采用（理由 §3.7）。**v6 补（F-21，R6 附带要求）：W1 交付批次必须含 R10 re-freeze（协议章节/schema/冻结样本/两侧 spec 冻结断言更新，见 §3.7 代价行），否则过不了既有测试门**。实施细节随本行冻结 | S-7（anchor 响应交付，v17 改指 F-37）、S-4、S-6 |
| **OD-7**（v10 新增，v15 定案） | **基底缓存预算与 anchor 页数值** | A. 具体数值组合（见状态列）；B. 其它数值 | **已定案 A（2026-10-17 owner 裁决「按推荐」，v15 回填；v21 补登 F-46/F-48 新数值——恢复数值微调口径）：单 session 滑动上限 4MiB/256 turns；跨 session 总量 128MB + LRU；增量超限 2 页（turns 页数计）退全量冷路径；anchor-newer 页 limit 默认 100 turns / 4MiB；对账预算（turns+changedTurns+scalar）≤8MiB；响应总硬顶 48MiB；anchorRefresh / 单个 changedTurn never-split 独占额度 ≤46MiB（超限 typed oversized → FallbackFull，为 envelope 留 ≥2MiB——v22 收紧）；**对账续页不携带 anchorRefresh、可含 turns 剩余片（v24 F-48）、普通片 8MiB/页、首包对账空间被大 anchor 挤压时可少于 8MiB（v23 打包形状，R18 F-48）；full 传输上限 `projection_max_bytes` 默认 50MiB 维持、提升档 S-7/S-8 冻结时定（v24 F-53）****。理由：① **4MiB/256 turns 对齐 frozen v1 window 家族既有上限类别**（`projection_window.go:27-31` 的 `maxWindowTurns=256` / `maxWindowEncodedBytes=4<<20`），不发明新数值类别，且 4MiB 磁盘恢复 + 解码稳定落在重开 ≤1s 预算内——上限再大（如 16MiB）恢复耗时吃掉增量的速度优势；② **总量取拟议区间上沿 128MB**——iPhone 存储占比可忽略，预算越大 LRU 命中率越高、冷路径回退越少；配套：缓存目录标记排除 iCloud 备份（可重导出的派生数据不值得进备份）；③ **超限阈值 2 页**——新增超过两页意味着用户离开很久、上下文本已断裂，逐页 append 多轮往返且有布局跳动风险，v14 冷路径（全量 + gzip）一次给全量真相 + 原生 scalar；2 页以内（日常重开常态 0~几轮）纯 append；④ **anchor 页 limit 100 turns 对齐协议默认值**，增量场景通常远小于此 | S-4、S-6 |
| **OD-8**（v12 新增，owner 质询触发：window 的 older 路径与 2026-07-12 分页复盘同坑，§2.5） | **方案实施期 older 上滑加载开还是关**（登记时称「S-1 期间」，v17 去语境 F-37） | (a) **关闭（推荐）**：上滑不触发网络加载，截断提示（§3.3）为唯一交互——对齐复盘「宁可慢，不要跳」的产品决定；older page 登记为独立后续切片，启用前提 = 复盘三条复活条件逐条满足并引用该复盘；(b) 保留：older page 随 window 路径交付，但三条复活条件（锚点+精确视口偏移恢复、异步高度二次测量后不漂移、三 backend 真机连续翻页不跳底/不跳位）必须作为其验收门 | **已定案 (a) 关闭（2026-10-17 owner 裁决「按推荐 (a)」，v13 回填）**：方案实施期内上滑不触发网络加载、截断提示为唯一交互；older page 为**独立后续专项**（不在本方案切片内），启用前提 = 复盘三条复活条件逐条满足并引用复盘、不得仅因首开慢复活（复盘原文）；配套 older gate 交付项见 §6 S-4 / §7.2.15（v16 改指 F-32——windowed 会话随 restore+increment 进入默认路径，gate 归 S-4 交付） | S-4、S-7、S-6 |
| **OD-9**（v20 新增，F-45 降级登记） | **growth-trust 档：重启后是否跳过 source prefix 校验** | (a) 不启用（默认推荐）：重启后 source-ledger prefix digest 校验整读（IO ≈ transcript 大小，与现网生产链一致），S-2 承诺 = 重投影 suffix-only；(b) 启用：跳过 prefix 校验直折 suffix（重启更快），**接受「prefix 原地等长/增长型改写不检测、Kernel 可能恢复旧数据且 iOS 对账无法发现」为 R-7 例外取舍** | pending，需 owner 裁决（R-7 产品例外取舍，不得由工程修订默认授权；不阻塞评审——S-2 按默认档 (a) 实施） | S-2 |

OD-1 / OD-3 / OD-7 已定案（v15 回填，理由随行）；OD-6（W1）与 OD-8（older 关闭）已定案；OD-2 被 v14 取代（limit 归 OD-7）；**OD-9（growth-trust 档）pending——owner 裁决 R-7 例外取舍，S-2 按默认档 (a) 实施不受阻塞（v20 新增）**；其余为工程决策。

## 5. 证据与前置门

| ID | 命题 | 证据 / 状态 | 未满足时阻塞 |
| --- | --- | --- | --- |
| E-1 | 事故是 iOS timeout 而非 Mac hydrate failed | verified：Mac 17:30:56 hydrate/enqueue，截图 17:31:18 timeout；log 无 `projection.hydrate_failed` | 已满足 |
| E-2 | 事故实际未走 window | verified：事故窗口只有 `get_session_projection`，无 window RPC | 阻塞 S-7 完成宣告（v16 改指 F-32） |
| E-3 | window 未走的具体原因 | pending：需同 builds 的 hello capabilities、iOS `supportsProjectionWindow`、安装包身份、**backend descriptor capabilities 是否含 `projection_window_v1`（v3 补，F-13——已知实现为 hello echo 单门，取证须覆盖 descriptor 维度以完整排除）** | S-7 开工前必须闭合（v17 改指 F-37） |
| E-4 | 现有 v1 window 对样本可降到 4MiB 内 | component-level verified：本地 checkpoint 尾部 17 turns 约 3.79MB（口径见 §1.1）；非 wire capture | S-7 验收仍需真实 wire（v17 改指 F-37；口径 = anchor 增量页） |
| E-5 | iOS 阶段耗时分布 | pending：需 receive / gunzip / decode / apply / presentation / UI commit signposts | S-5 完成宣告 |
| E-6 | 新 materialization store 正确性 | pending：实施后以合成 100MB 级 fixture + 本地 realdata tag 测试 | S-2 |
| E-7 | preview cache 不污染权威 writer（**v10 注**：语义升级为「基底缓存不污染权威 writer」——restore 必须经唯一 writer 注入，§3.1 不变量 2 / §3.4.3） | pending：实施后定向测试 | S-4 |
| E-8 | Relay gzip 边界 | verified：协议与源码；本次 Direct 不适用 | 不阻塞 S-2/S-7（v17 改指 F-37） |
| E-9 | Codex 参考机制 | verified：Codex source commit 已记录；只作为架构参考，不是 CordCode 运行证据 | 不阻塞 |
| **E-10**（v3 新增；v14 收缩口径、v16 修正行文并补冷路径护栏，F-35） | scalar state 正确性：**anchor 增量路径**（主对象）+ **冷路径 execution 回归护栏** | pending：anchor 路径按 §3.7/§3.8 实施后定向测试（§7.1.4/§7.2.10）+ 真机场景 2/11；**冷路径护栏（随 S-5/S-8 交付）**：running session 经全量 + gzip 首开后 `isSessionExecuting == true`——v14「自动满足」论断降级为「需护栏」（防 S-5 解码重构丢失 execution 字段，消费链 `ChatViewModel.swift:2419-2426`） | **S-7 完成宣告**（anchor 部分）；冷路径护栏随第一阶段交付 |
| **E-11**（v10 新增；v19 补离线修改反例与身份一致性，F-42/F-44） | restore + 增量打开路径正确性（含**全基底对账**与**跨 epoch 身份一致**） | pending：S-4/S-7 实施后定向测试（§7.2.12-14）+ 真机（§7.4 场景 9-15）。**对账验收（F-42 升级：比较内容终态，非仅 turn ID 集合）**：同一 session「restore + anchor-newer（含 baseDigests）」最终基底与「全新全量打开」在重叠范围**内容一致**；**离线修改反例**：缓存 [A旧,B旧,C]，离线期间 Mac 将 A 改为 A新、anchor（C）保留、无后续事件——重开后对账必须经 changedTurns 返回 A新，syncing 只在对账完成后清除（同 epoch/跨 epoch 均覆盖）；compaction 反例（removedTurnIds + anchor typed stale）；**token 生命周期反例（F-42 v20）**：(a) 对账页后收到 partOps→写盘→重开（token 已失效 → 对账请求 null → Mac 必须回传当前终态）；(b) 缓存内容/token 不匹配且 Mac 恢复到旧版本（token 匹配但内容是新的——失效规则保证该行 token 为 null，强制回传修正）；**changedTurns 膨胀反例（F-46 v20）**：多条旧 turn 离线膨胀、零新增、总修正超预算/transport 上限 → correctionsPending 分页续拉，部分修正未完成不清 syncing；**对账续页跨 cut 复查反例（v24 F-52）**：首页已发 turn 在续页请求前被 live patch 修改 → 复查 token 失效 → 续页重发当前终态（upsert 覆盖），采用事务以最后一页 head 为准；**大 anchor 后继交付反例（v24 F-48）**：46MiB anchor + 1KiB 后继 turn → 对账续页 turn phase 交付、水位线前移至该 turn；**full 超限反例（v24 F-53）**：`reconciliation.oversized` → FallbackFull 后 full > `projection_max_bytes` → typed `full.oversized` 显式失败（不冒充恢复成功）；**跨 epoch 身份一致性（F-44）**：采用事务后 projection/replica/store 的 `revisionKey = epoch\|syncRev`、coverage、presentation、持久化 metadata 一致且可接下一条 patch/权威 full（非空页与零新增空页路径均验）；水位线 typed 失效回退、滑动淘汰、双设备各自水位线互不串扰 | **S-4 完成宣告**（与 S-7 联合） |

Gate A：

- **（v14 改写）原 S-1 切片取消**：首开走全量 + gzip（S-5 + S-8），window 管道工程并入 S-7——**S-7 开始实施需 E-1/E-2 与源码锚点 + E-3（window 通路未生效根因——anchor-newer 依赖同一协商/handler 管道，仍是开工前置）+ OD-6 定案（W1，交付范围按 §3.7 v14 范围注收缩到 anchor 响应）+ anchor-newer re-freeze 批次（§3.8）**；S-7 完成宣告需 E-3/**E-4（anchor 页 ≤4MiB 口径）**/**E-10（anchor 路径 scalar；冷路径护栏口径见 E-10 行——v17 对齐 F-35/F-37，不再称「自动满足」）**。
- OD-2 不阻塞 S-1 开工（v2 补，F-5）——**本条已随 v14 失效**（S-1 取消、OD-2 被取代、limit 归 OD-7），保留为历史注记；现行门控见上条 S-7。
- S-2 开始前需**存储选型定案 + 存储格式（bucket/key 布局）冻结与不变量测试清单**；选型已由 §3.2.2 定案（bbolt + iOS 文件型，评审可推翻）；完成需 E-6。
- S-4（**v10 重设计**为基底缓存与 restore+increment 打开路径）开始前需 **S-7 落地**（OD-1/OD-3/OD-7 已全部定案，v15 回填）；完成需 E-7/**E-11**。
- S-7（v10 新增）开始前需 anchor-newer re-freeze 批次（§3.8；可与 W1 批次合并交付）；完成需 Mac/iOS spec 断言更新与 §7.1.5 / §7.2.13 定向测试。

Gate B：

- S-3 的 v2 协议冻结前需 summary UI 语义确认（**v19 去 OD-2 F-41**——OD-2 已被 v14 取代、limit 归 OD-7，不再阻塞任何切片）。
- 所有真实设备 UI 验收需 owner 执行；agent 不自动运行 UI automation。

## 6. 实施切片

| 切片 | 可见结果 | 组件与复用 | 前置条件 | 最小验证 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-0：运行身份与阶段指标 | 一次打开能对齐 iOS build、Mac runtime commit、hello echo、window/full 选择和各阶段耗时；开工前按 P0 来源门重生成来源清单（v3 补，F-10） | Mac hello/log；iOS transport/projection trace；management API | 无 | 定向日志测试；对当前样本采集一次 | 只保留诊断，不改变行为 |
| S-2：Mac materialization store | runtime 重启或 transcript append 后只处理 suffix；不再读写 34MB JSON checkpoint | **bbolt store（§3.2.2 选型定案）**；ProjectionKernel 单 writer | **存储格式冻结 + 测试计划**（选型已定案） | append / restart / compact lineage / truncation / live patch race 定向测试；**window 强制冷检组合路径（§3.2.3）IO 上界测试** | store invalid 时诚实 full rebuild；旧 checkpoint 保留不删除 |
| S-3：`projection_window_v2` summary page | 首屏 turn summary ≤1MiB，过程明细按需加载 | v1 producer / turn_detail_lazy 语义借鉴 Codex items list | v2 协议冻结评审（summary UI 语义确认；OD-2 已被 v14 取代、limit 语义归 OD-7——v16 改 F-32） | schema mirror、cursor、active turn、item detail、typed error 测试 | v2 未协商时走 v1，不造第二 writer |
| **S-7：anchor-newer 协议切片（v10 新增；v14 吸收原 S-1 管道工程）** | Mac 支持 turnId 锚定的 newer 增量页（含锚点、空页、typed stale、`scalarState`）；direct+relay 双路径协商 | `projection_window_anchor_newer_v1` additive capability + re-freeze 批次（§3.8——新类型/**独立样本文件**（v16 补 F-33），**不触碰 frozen v1 字段集**；W1 的 v1-响应批次已降级 contingency）；协商复用 `negotiateProjectionWindowV1` 扩展（F-26 纪律）；window `projection_rpc` trace（F-25 纪律）；**held 注册复用 `CompleteProjectionSnapshotWithHeldTurns`（§3.8，v16 补 F-34）** | re-freeze 批次 + **E-3 取证修复（window 通路未生效根因）** | Mac handler 测试（§7.1.5：锚点命中/失效/含锚点/空页/上限/双路径 echo/scalar/**held 注册与并发 detail 负对照**/**独立样本文件断言**）；spec 断言（新增类型与读取点） | 未 echo 时 iOS 回全量冷路径（full + gzip，不假成功） |
| **S-8：`direct_gzip_v1` 传输压缩（v14 新增；v16 补自描述机制 F-31）** | Direct Mac→client 大响应（首开全量、未来大页）gzip 传输：样本 34MB → 约 9.6–11MB（§1.1 实测口径） | 应用层逐帧，**镜像 `relay_gzip_v1` 纪律**：≥32KiB 且压缩更小才启用、仅 Mac→client、additive capability 协商；**自描述 = WebSocket opcode（§3.6 v16 定案、v17 方向更正 F-36：仅 Mac→client 方向新增 binary 压缩帧；三种替代否决理由随行）**；iOS 解压复用 relay 路径既有 gunzip；**iOS transport 改造（v17 补 F-38；v18 更正行号 F-39）**：`receiveData()` 签名（`BridgeWebSocketConnection.swift:6/:71/:308`）在 API 边界丢弃 opcode（读取路径仅 `:326/:334` 日志——`:324` 是 `continue`；`opcode.rawValue` 另有 `:368/:376` 两处属发送侧 `sendFrame` 参数，非读取路径消费点）——分流需改签名暴露 opcode；iOS 已有 opcode 枚举（`:134-141`：text=0x1/binary=0x2）且读循环已区分（`:311-330`），改造面小；独立冻结批次 | 冻结批次（协议文档 binary 帧语义 / binary fixture 样本 / opcode 分流断言 / `schemaRevision`） | 阈值边界（小帧不压、压缩变大不启用）、未协商连接零 binary 帧、iOS binary→gunzip 与 text 路径深度相等、Direct/Relay 双路径回归、压缩耗时入预算 | 关闭 capability 即回不压缩传输（= 现状行为） |
| **S-4：iOS 基底缓存与 restore+increment 打开路径（v10 重设计）** | 重开同一 session：本地恢复基底 + 增量 append 补齐（KB 级），不重拉尾窗；首开/回退走**全量冷路径（full + gzip，v14）** | **双层缓存 store（§3.4.1：磁盘 Application Support 单文件 + 原子写 + 滑动淘汰/LRU；内存 replica 加速）**；restore 经 `applyFull(forceReplace: true)` + appliedRev 重置（§3.4.3）；显示经唯一 writer | OD-1/OD-3/OD-7（均已定案 v15）+ **S-7** | cache hit/miss、身份/schema 失效、epoch change（不 miss 只升级 stale）、**restore+增量对账（E-11：与全新全量打开重叠范围一致）**、增量 typed stale 回退、滑动淘汰、网络失败、切回 same session 测试 | cache invalid 只回到全量冷路径/loading，不显示假成功 |
| S-5：单次解析与分段 apply | 移除多轮全量 JSON parse/re-encode，长 page CPU / 内存峰值下降 | transport route scanner + typed result decoder | 无（v16 改 F-32；**与 S-8 同为第一阶段承重项，建议同批交付**——gzip 只压网络，S-5 才解 decode） | decoder benchmark、actor cancellation、trace correctness 测试；**冷路径 execution 护栏（F-35：running session 全量首开后 `isSessionExecuting == true`，防解码重构丢字段）** | decode 失败显式 transport/projection error |
| S-6：真实样本验收 | 约 97MiB Claude 样本 Direct/Relay 达标，无 15s timeout，**单次打开重试次数 = 0（§7.3，v3 补）** | owner 真机手动矩阵 | **S-5/S-8（首开达标，v14）** 最少；完整目标需 S-2/S-4/S-5/S-7 | 记录 Mac/iOS 阶段指标、**每次打开的重试计数**与截图 / 日志 | 未达标保留失败现场，禁止提高 timeout 收口 |

**（v14 改写）原 S-1 切片取消**：首开走全量 + gzip（S-5 单次解析 + S-8 `direct_gzip_v1`）；window 通路修复、scalar（anchor 响应范围）、trace 与双路径 echo 纪律全部并入 S-7（增量的管道工程）；`window_0` 机制（协议 + 两端实现）保留为超长 session 首开触顶的 contingency 开关，不在默认路径。

**（v4 补，F-18）capability 双门偏离收口实施项（归属 S-3，协议冻结时执行）**：F-13 登记的实现与协议文档偏离（hello echo 单门实现 vs `docs/protocol/bridge-v1.md:2173-2175` 双门规范，见 §2.2）不再无限期悬置，在 S-3 v2 协议冻结时二选一收口，由协议冻结评审定案方向：

- (a) Mac 侧把 `projection_window_v1`（及 W1 的 `projection_window_scalar_v1`）写入 backend descriptor 的 `capabilities` 列表，并按文档条件（descriptor 支持 + 全局开关均满足才 echo）对齐实现；iOS 消费端同步按 descriptor 门控；或
- (b) 修订 `docs/protocol/bridge-v1.md` 该条款为单门语义（实现即规范，删去 descriptor 门控要求）。

在收口前，window 家族 capability（v14 后主用 = anchor-newer；原「S-1 / W1」表述为登记时语境，v17 改 F-37）沿用现状单门模式（与 v1 一致）；当前唯一已知消费端为本仓 iOS 客户端（只查 echo），单门模式不产生已知消费端的静默失效面，但任何新客户端接入前必须先完成本收口。

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
   - **window 强制冷检组合路径（v3 补 F-14；v22 限定范围 F-49）**：append 后首次 `window_0` 的 **window handler 冷检路径** IO 上界 = stat + 尾部 4KiB 反扫（**「不整读」仅指此冷检路径**——完整生产链含 source ledger `Observe` 的 prefix 校验整读，按 §3.2.3 (ii) 如实计，不得以此行的「不整读」豁免）+ suffix 重投影。
   - **重启恢复全链读量（v20 改 F-45/F-47）**：「保存 store → 停止 runtime → 相同 source append → 重启 → relay 启动」实测**真实 owning 调用链的全部文件读取**（bbolt loader + source ledger `Observe` + relay 启动，trace 开/关均可解释）——断言重投影仅 suffix、IO 读量与 §3.2.3 (ii) 声明一致（默认含 prefix 校验整读）；「prefix 被原地改写（等长）」反例：digest 校验捕获（Mac 侧 source-ledger，非 iOS 对账——后者与 Kernel 共享错误源）；growth-trust 档（OD-9）未启用时不得出现「跳过 prefix 校验」的读量。
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
5. **anchor-newer 增量（v10 新增，S-7）**：
   - `anchorTurnId` 锚定的 newer 页：锚点命中（`turns` 严格后继 + `anchorRefresh` 独立字段，v19 F-43）/ 锚点离开保留区（typed `cursor_stale` 族 → FallbackFull）/ anchor == tail（合法空 `turns` + `anchorRefresh` 终态）；
   - **对账断言（v19 新增，F-42）**：`baseDigests` 命中/不命中 → `changedTurns`/`removedTurnIds` 正确返回（离线修改反例：A旧→A新 经 changedTurns 修正；removedTurnIds 为 compaction tombstone）；每 turn 的 digest 由 Mac 生成并随页下发（iOS 不自行计算）；
   - **预算边界验收（v22 新增 F-48；v23 修正 R18——原「47MiB anchor（总硬顶内合法）」与 ≤46MiB 独占额度规则直接冲突：47 同时满足「>46 → oversized」与「≤48」，实现与测试无法判定）**：边界用例统一到独占额度口径——8MiB（对账预算临界：恰满/超 1B → correctionsPending）；20MiB anchor（独占额度内合法，不触对账预算）；**46MiB anchor（恰满独占额度：46 + mandatory 2MiB = 48 恰满总硬顶，首包对账空间 = min(8, 48−2−46) = 0 → turns/changedTurns 全部 correctionsPending，合法）**；**46MiB+1 anchor（47/48MiB 同此判定：超独占额度 → typed oversized → FallbackFull）**；单个 changedTurn 9MiB（超对账预算 → 独占修正页）；46MiB changedTurn（恰满独占修正页：46 + mandatory = 48）；46MiB+1 changedTurn（超独占额度 → oversized）；
   - **组合验收（v23 新增，F-48——R18：续页 anchorRefresh 携带规则未定义时合法组合不可实现）**：① 46MiB anchor + 9MiB changedTurn → 首包仅 anchorRefresh + mandatory（correctionsPending），9MiB 单 turn 独占修正页（9+2=11 ≤48），每页全字段序列化 ≤48MiB；② 20MiB anchor + 46MiB changedTurn（单 turn）→ 首包 = 20 + 普通片 min(8, 48−2−20)=8MiB + mandatory，46MiB 单 turn 独占修正页（46+2=48 恰满）；③ 46MiB anchor + 46MiB changedTurn（单 turn）→ 首包 anchor-only + mandatory，续页独占修正页 46+2=48 恰满；④ **修正页不重复 anchorRefresh 断言（v25 改写 F-54——原「续页 turns 为空或省略 + head/cut 标识与首包一致（同 cut 续页）+ 构造 cut 失效」为 v23 口径活文本，与 v24 续页内容页规则直接冲突）**：全部续页响应 **anchorRefresh** 字段为空或省略（**turns 剩余片与 changedTurns 剩余片按 §3.8 打包 3 的 turn phase → correction phase 顺序出现，不属违例**——v24 F-48 续页为内容页）；每续页 head 取当次请求的 committed head（**无需与首包一致**——采用事务以最后一页 head 为准，§3.8 单调覆盖③）；iOS 采用事务在全部续页到齐后一次性提交——构造续页 typed stale 的语义对齐自包含 cursor 验收③（**anchor 离开保留区**；epoch 中止分支的判定主体见 §3.8 单调覆盖④，不再引用已撤回的 cut 概念）→ FallbackFull、无部分采用状态泄漏；⑤ **打包确定性断言**：同一输入（同 anchor/同 changedTurns 集）两次生成的页序列字节一致（顺序：mandatory 预留 → anchorRefresh 仅首包 → turns → changedTurns 按 turnId 升序）；
   - **后继 turn 交付验收（v24 新增，F-48——R19：46MiB anchor + 后继 turn 是水位线前进的正常主路径，非仅 changedTurns 边界）**：① 46MiB anchor + 1KiB successor → 首包 anchor-only（correctionsPending），对账续页 turn phase 交付 1KiB turn、水位线前移至该 turn，下一请求 anchorRefresh 恢复正常大小；② 46MiB anchor + 9MiB successor → 续页整 turn 独占页（9+2=11 ≤48，never-split）；③ 42MiB anchor + 6MiB successor → 首包对账空间 4MiB < 6MiB，该 turn 溢出至续页交付（**首包剩余 4MiB 空间不回填、切页即止——changedTurns 不得越过未交付 turns 提前装页，相位边界见 §3.8 确定性打包顺序；v26 更正 F-58——原「按序装更小的对账内容」暗示跨相位回填，与「装不下即切页」的唯一页序列读法冲突**）；断言：每页全字段 ≤48MiB、turn chain 不跳过（R7）、水位线最终前进、syncing 只在 turns 与 corrections 全部到齐后清除；
   - **自包含 cursor 验收（v24 新增，F-52）**：① 续页请求携带完整复查集（原始 baseDigests ∪ 已发项 token，含 anchorRefresh turn 与已交付 turns）——**Mac 无状态断言**：换连接重发同一 cursor 结果幂等（upsert 覆盖）、两设备交错请求互不串扰；② **跨 cut 复查**：构造「首页已发 turn 在续页请求前被 live patch 修改」→ 复查 token 失效 → 续页重发当前终态，采用事务以最后一页 head 为准，最终基底与最后一页 cut 的一致状态相等；**另构造「事务中途 compaction 移除非锚点 turn（anchor 仍在保留区）」（v25 补 F-56）→ 复查发现该复查项不在当前投影 → 经 removedTurnIds 返回 tombstone、iOS 从基底移除（不重发该 turn 内容）**；**再构造「早页复查命中（确认无修改）的基底项在续页请求前被 live patch 修改」（v27 补 F-59）→ 续页复查集仍含该项（全集不收缩）→ token 失效 → 续页重发新终态——该窗口内恢复门 patch 若 rev ≤ 采纳 head 会走版本校验丢弃分支丢内容，全集复查是对账页内的唯一可靠交付载体**；③ anchor 离开保留区（事务中途 compaction）→ typed stale → FallbackFull、无部分采用状态泄漏；
   - **full 超限验收（v24 新增，F-53）**：构造 46MiB+1 turn + 其余内容使 full 序列化 > 50MiB（默认协商档）→ `reconciliation.oversized` → FallbackFull → **`full.oversized` 显式失败**（iOS 错误态可见、不部分渲染、不把 transport 解压拒绝冒充对账回退成功）；协商提升档后同一 session full 成功；解压拒绝不得计入「恢复成功」指标；
   - **分页前进断言（v19 新增，F-43）**：超大 anchor（>4MiB，样本实存）不阻塞 `turns` 页前进（anchorRefresh 不占页预算）；limit=1 时翻页仍前进；anchor==tail 无新增时页不循环；
   - limit / 4MiB 上限与 R7 严格链序（不跳过未加载 turn）；
   - 未协商该 capability 的连接收到 typed `protocol.capability_required`（fail-closed，不返回错误窗口）；
   - direct + relay 双路径 echo（同 F-26 纪律）；
   - anchor-newer 请求走 window `projection_rpc` trace（同 F-25 纪律），可按 requestId 计数（§7.3）；
   - **held 注册断言（v16 补，F-34）**：增量页全部 turns（含锚点）在完成事务内注册进该连接 held 集合（`CompleteProjectionSnapshotWithHeldTurns`，`projection_window_handler.go:201-211`）——并发 detail commit 负对照：增量页投递后，针对其 turn 的 decide=true commit 不被清空（漏注册 = "permanently strand the detail"，P0-2）；注册失败随完成失败回滚；
   - **独立样本文件断言（v16 补，F-33）**：anchor 样本经新增读取点读 `anchor-newer-response-spec.json`；既有 `window-response-spec.json` 及其精确 key 断言零改动。

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
10. **scalar state（v3 补 F-11；v16 双口径改，F-35）**：running session 经**冷路径（全量 + gzip 首开）与 anchor 增量重开**后均 `isSessionExecuting == true`（消费链 `ChatViewModel.swift:2419-2426`）；`requires_action` 时输入框禁用、权限卡锁发送；anchor 路径的 scalar 交付不依赖 full 兜底（typed stale 回退冷路径后全量原生含 scalar）；
11. **preview scalar（v3 补）**：cache 命中渲染 preview 时执行态标记与 metadata 一致；权威 page 到达后 scalar 随 page 一同替换。
12. **restore 路径（v10 新增，E-11；v11 补 F-29/F-30；v19 补 F-44 身份断言）**：**采用事务**（§3.4.3 第 2 条）：身份改写副本（bridgeEpoch=当前、syncRev=0）+ `applyFull(forceReplace: true)`（`ProjectionReplica.swift:193-212`）+ appliedRev=0 + coverage 清零，同事务完成；**（F-44）身份一致性断言**：事务后及首条对账页（非空与零新增空页两路）apply 后，projection/replica/store 的 `revisionKey = epoch|syncRev`、coverage、presentation change-set、持久化 metadata 一致且可接下一条 patch 与新 epoch 小 rev 的权威 full（负对照：只重置 appliedRev 不改写身份 → `max(旧,新)` 混合身份、full stale 误拒——按被指定复用代码推导的反例）；**（F-44）恢复中插 patch 的时序反例**：事务执行期间到达的 patch 被串行排队、不产生部分采用状态；**（F-44 v20）在每个 await 边界（`replica.applyWindowPage` / `presentationEngine.apply` / `commitIfObserved`）插入 patch**：不产生恢复风暴（base mismatch 误触发 recovery pull）或覆盖新状态；**零新增页进 head 断言（F-44 v20）**：零新增且此后无任何 patch 时，对账完成事务后 projection/replica/store 即为响应 head（非等待下一条 patch）；稳态 `pullDeltaOrFull` sinceRev 正确（不退 full snapshot）；**（F-29）两步注入断言**：restore 后 `State.lifecycle == .ready` 且 `renderProjectionFromStore`（`ChatViewModel.swift:1080`）实际渲染；负对照 = 只做 replica 层 applyFull 不做 store 层 commit → 渲染为空；**（F-30）`switchSession` 的 clear 语义未被打开路径改动**（回归护栏：切走再切回必走 BaseCacheHit，且无 `isActive`/`sessionSyncV2Active` 跨切泄漏）；
13. **mixed-patch token 测试（v21 新增，F-50）**：同一 patch 内 `upsertTurns + turnStateOps + partOps` 作用于同一 turn——若附 token 则必须等于全 ops 应用后终态的摘要（中间版本 token = 失败）；未附 token → 该行 token 为 null，下次对账强制回传。
14. **anchor-newer 增量消费（v10 新增，S-7/E-11；v19 更正 F-41/F-42/F-43）**：水位线有效时打开走 anchor-newer（含 baseDigests 对账）；**typed stale / 超 2 页 / anchor 未 echo 一律回退全量冷路径 FallbackFull**（原「回退 window_0」为 v14 前残留）；零新增 = 空 turns + anchorRefresh（末 turn 终态仍刷新）；changedTurns/removedTurnIds 应用完毕且对账通过才清 syncing；多页串行 append ≤2 页；eligibility 第三维度（anchor echo 缺失回全量冷路径）；
15. **对账验收（v10 新增，E-11）**：同一 session「restore + anchor-newer」最终 turn 集与「全新 `window_0` 打开」重叠范围一致；双设备各自水位线互不串扰（Mac 无客户端状态，不变量 6）；滑动淘汰后 `hasOlder` 提示覆盖淘汰侧。
16. **older 上滑关闭（v13 新增，OD-8(a) 定案配套）**：滚动到顶**不发起** `get_session_projection_window(direction=older)` 网络请求——若 SSV2 windowed 会话的滚动触发链已接线（`ChatViewModel+MessageSync.swift:981` SSV2 load-older 路径、`ChatUIKitContainerView.swift:2060` older-page prepend 处理），**S-4 交付将其 gate 关闭（v16 改指 F-32：windowed 会话随 restore+increment 进入默认路径）**；截断提示为唯一交互（§3.3）；断言 = Mac trace 零 `direction=older` 请求。
17. **缓存淘汰与 TTL 负对照（v15 新增，OD-3/OD-7 定案配套）**：**时间本身不触发任何淘汰**（负对照——只有 identity/schema/容量/LRU 是淘汰原因，任意设定保存时长后缓存仍可用）；单 session 超 4MiB/256 turns 从头部淘汰 + `hasOlder` 覆盖淘汰侧；总量超 128MB 触发 LRU 淘汰最久未打开的 session，被淘汰 session 下次打开走全量冷路径且无正确性损失；缓存目录被标记排除 iCloud 备份；增量超 2 页（≈2×4MiB）退全量冷路径（OD-7 阈值）。
18. **direct gzip opcode 分流（v17 新增，F-36/F-38 配套）**：`receiveData()` 签名改造后 iOS 按 opcode 分流——text → 现行解析；binary → gunzip → 同一解析路径，输出与 text 路径深度相等；**负对照：client→Mac 维持现状 binary 发送不变（不得改成 text，§3.6 方向澄清）**；「未协商连接零 binary 帧」断言**限定 Mac→client 方向**（client→Mac binary 为既有现状）。

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
| Mac source advance（v20 改 F-45/F-47） | **重投影 suffix-only**（append 只重投影新增部分，不重投影 97MiB）——这是 R-5 的精确化承诺；**IO：重启后 prefix 校验整读是生产默认**（source ledger 必需）；「零 prefix 校验读」仅 OD-9 启用后成立 |
| Mac restart 首开（v20 改 F-45/F-47） | **(ii) 重启默认 = source-ledger prefix digest 校验（整读 ≈ transcript 大小，97MiB 约 0.3–1s——生产链既有成本，`handlers_relay.go:882-884`）+ suffix 重投影**；growth-trust 跳过档 = OD-9 pending（owner 裁决 R-7 例外）；(iii) 全等 = 快速匹配零读；(iv) 争议 = prefix_sha256 裁决 + 重建 |
| **window_0 冷源检查 IO（v3 补，F-14）** | 每次请求触发（无 `sinceRev` 门）：每 segment `os.Stat` + 尾部 4KiB 反扫；断言不整读 transcript；append 后 catch-up = 冷检 + suffix 折叠 |
| anchor-newer 增量页 bytes（v14 改口径；v19 计量修正 F-43） | **分层预算（v24 F-48 终版；v25 对齐 F-54——原「续页只含 changedTurns 剩余片」与「v23 F-48 终版」标记为 v23 口径残留）：响应总硬顶 48MiB（全字段含 envelope）< 50MiB transport 解压上限；mandatory 预留 2MiB；anchorRefresh（**仅首包携带，修正续页不重复**）与单个 changedTurn 均 never-split 独占额度 ≤46MiB（超 → typed oversized → FallbackFull）；对账预算（turns+changedTurns+scalar）≤8MiB/页，首包对账空间被大 anchor 挤压时可少于 8MiB；超限 correctionsPending 整 turn 粒度分页（**续页可含 turns 剩余片与 changedTurns 剩余片——turn phase 先于 correction phase，§3.8 打包 3**）；边界验收 8/20/46/46+1 + 组合与后继 turn 交付用例（§7.1.5）**；turns 默认 100 turns/4MiB（OD-7 v15+v21）；「超 2 页」按 turns 页数计；v2 summary ≤1MiB（S-3 不变） |
| iOS wire receive → decode | 可量化，无多次 full object graph |
| cache paint | ≤500ms（cache hit，目标，非当前承诺；v10 注：restore 路径的基底显示目标，含 `applyFull` 注入与 presentation） |
| **重开增量路径（v10 新增，restore-then-increment）** | 有效水位线的重开：本地恢复 + anchor-newer 增量，**权威完成 ≤1s（目标）**（增量页 KB 级；预算 = 磁盘恢复 + 单次有界请求往返）；增量重试口径：网络/`projection.hydrating` 失败后的重复发起计入 §7.3 重试行；anchor typed stale → **FallbackFull（全量冷路径，§3.9）** 回退属合法回退不计重试（v26 更正 F-57——原「`window_0` 回退」为 v10 时代口径、v19 F-41 五处清扫漏掉的第六处活文本，与 §3.9 唯一权威回退规则及 §3.8/§7.2.14/§7.4 场景 14/§8 五处 FallbackFull 条款冲突；重试计数口径本身不变） |
| authoritative first page（首开 = 全量 + gzip + S-5 单次解析，Mac warm，v14） | Direct ≤8s；Relay ≤10s（样本 34MB → gzip 约 9.6–11MB） |
| authoritative first page（首开，Mac 冷全量 hydrate，checkpoint miss） | 不并入上排达标判定：预算 = Mac 冷 hydrate（本次实测约 1.794s）+ gzip 传输 / 单次 decode / apply + 可能一次 `projection.hydrating` 重试（F-8），验收记录实测值；第二阶段（S-2 后重启不再全量重建）冷场景并入达标判定（v2 补 F-4，v14 改口径） |
| **direct gzip 压缩比与耗时（v14 新增，S-8）** | 样本 level 6 ≈ 9.6MB（约 28% 原始体积）；Mac 压缩 + iOS gunzip 耗时计入首开预算；阈值边界（小帧不压、压缩变大不启用）有断言 |
| 15s timeout | 0 次 |
| full projection open（v19 重定义 F-41） | **首开 = full 是 v14 设计行为，不判失败**（原「0 次（new peer / window available）」为 v14 前口径残留）；**重开路径的 full** 仅由 FallbackFull 触发（typed stale / 增量超 2 页 / anchor 未 echo），各属合法回退不计重试、按 §3.9 总表验收 |
| **单次打开 full-snapshot / window_0 重试次数（v3 补，F-16；v7 补可观测前提与计数口径，F-25）** | **0 次**（事故实测 8 次全量投递 / 用户可见重开至少 4 次；「最终成功但有 2–3 次重试」不判通过）。**可观测前提（v7）**：window handler 现无 `projection_rpc` trace（`grep logProjectionRPCTrace projection_window_handler.go` = 0），window 成功仅经 fence 日志（`projection_snapshot_fence.go:127/:239`，字段为 sessionPrefix/cutRev/generation 与 sessionPrefix/pending/invalidated/expectedRev——均无 requestId 且 full/window 共用）——**S-7 交付必须包含 window 路径与 full 同构的 `projection_rpc` trace**（v16 改指 F-32；requestId/backend/sessionID/outcome；full 侧锚点 `handlers_projection.go:89/:175`），否则本指标在 window 生效后不可计数。**计数口径（v7）**：按「同一 session 单次打开内的 RPC 发起序列」计数（Mac trace 与 iOS transport trace 对账）；capability 不满足产生的**合法回退不计入重试**（双维度 gate 判定后直接 full，或 window 收到 typed `protocol.capability_required` 后回 full——各 ≤1 次，单独归类）；`projection.hydrating` / 网络失败后的重复发起计入重试 |

### 7.4 真机 owner 矩阵

每条场景执行前用 S-0 指标记录 Mac warm / cold 状态（checkpoint / materialization 是否命中），结果按 §7.3 分行口径对照（v2 补，F-4）；**每次打开记录重试计数（v3 补，F-16）**。

1. 冷 App + idle 长 Claude session，Direct。**（v14）首开 = 全量 + gzip：全屏加载图标，样本目标 ≤8s、一次成功、零重试；历史完整可见（无需截断提示）**；
2. 冷 App + running Claude session，Direct。**应看到（v3 补 F-11，v14 路径改）**：执行中状态与权限卡语义不丢——不显示「空闲」，`requires_action` 时输入框禁用（**全量首开原生含 execution**，不再依赖 window scalar 交付）；
3. 切走再切回同一 session；
4. Mac runtime 重启后重开。应看到（v2 补，F-1）：若此前有该 session 的 preview，先显示 last-good + 升级的 stale / 正在同步标记（不退回纯 loading、不清 cache）；权威 page 到达后替换并清除标记。无 preview 时正常 loading；
5. Relay 弱网重开；
6. 断网时有 cache：显示 stale，不假成功；
7. 恢复网络后 authoritative page 替换 preview；
8. **滚动到顶（OD-8 已定案 (a)，v13 回填）**：上滑**不触发网络加载**——顶部显示截断提示「更早的 N 轮未加载」（§3.3；判定 = `hasOlder` × coverage ledger），核对提示出现/消失时机正确（首开即截断、增量 append 后仍正确、滑动淘汰后覆盖淘汰侧）；Mac trace 无 `direction=older` 请求（§7.2.15）。**（older page 为独立后续专项：启用前提 = `think.md:2699-2724` 复盘三条复活条件逐条满足；启用后本场景追加「older page 期间 execution 更新照常投递」的核验——held 集合连接级累积、older page 不清除，§3.7 W1 fence 语义）**；
9. **（v10 新增 E-11；v14 改口径）首开种子写入**：首次打开（本设备无缓存）走**全量 + gzip**；随后重开走增量——验证首开产物（有界尾部滑动窗 + 水位线）成为增量链种子；
10. **（v10）重开零新增**：增量空页静默返回，水位线更新，UI 无感；
11. **（v10）重开少量新增（1–5 轮）**：append 到尾部，视口不跳，执行态 / 权限卡语义正确（scalar 随增量页到达）；
12. **（v10）重开大量新增（>2 页阈值）**：**退全量冷路径 FallbackFull（v19 更正 F-41——原「退 window_0」为 v14 前残留）**整体重建，「更早的消息未显示」提示正确；
13. **（v10）Mac 重启后重开**：水位线仍有效（turnId 身份，不变量 6），增量照常完成；stale 标记升级后随增量到达清除；
14. **（v10）水位线失效回退**：anchor 离开 Mac 保留区 → typed stale → **全量冷路径 FallbackFull（v19 更正 F-41）**，不假成功、不清整个缓存身份；
15. **（v10）双设备**：iPhone 与 iPad 各自打开同一 session，各自水位线各自增量，互不影响（Mac 无客户端状态）。

## 8. 风险与回退

| 风险 | 触发信号 | 处理 |
| --- | --- | --- |
| 实际安装包仍未协商 window | S-0 无 echo / 无 window RPC | 停止性能优化，先修 build / capability identity |
| materialization 与 kernel head 不一致 | page revision / turn set mismatch | fail closed：invalidate store 并 full rebuild |
| **基底缓存被误当权威**（v3 补 F-11；v10 改写） | 增量未完成/失败时 UI 清错误或允许发送但未对账 | syncing/stale 显式标记；增量页是权威对账点；E-7/E-11 测试禁止缓存绕过 writer |
| **恢复基底与 Mac 真值漂移**（v10 新增；v19 重写 F-42） | **离线期间**旧 turn 被修改（live patch 不会重放——`BeginProjectionSnapshot` 只保证 post-cut 顺序）/ compaction | 显示滞后且 syncing 已清（假成功） | **baseDigests 全基底对账**：重开请求回传全部缓存 digest，Mac 经 `changedTurns`/`removedTurnIds` 返回修正；syncing 以对账完成为清除前提；compaction → anchor typed stale → FallbackFull；订阅后的突变仍由常规 patch 流覆盖（decide=false 完整投递，§3.7 fence 语义） |
| **跨 epoch appliedRev 误判**（v10 新增） | restore 后未重置即链增量页 | `.windowPageUnchainable` / 稳态退 34MB full snapshot | §3.4.3 第 2 条时序：applyFull 后显式重置、增量先于稳态 loop（§7.2.12 测试锁定） |
| **缓存无界增长**（v10 新增） | 多次打开持续 append | 磁盘/内存膨胀 | 滑动上限 + 总量 LRU（OD-7）；淘汰代价 = 下次打开走全量冷路径 FallbackFull（v19 更正 F-41） |
| **Mac 侧被加 per-device 状态**（v10 新增） | 实现走样 | 多设备串扰、重启恢复复杂化 | §3.1 不变量 6 入册，评审把关 |
| **超长 session 首开触顶 15s**（v14 接受项；v24 补 F-53：触顶与超限是两条失败线——前者 15s 时间线、后者 `projection_max_bytes` 字节线） | projection 远大于样本（如 ≥3×） | 首开显式失败 + 重试（有界性已从首开撤除） | contingency：启用 `window_0` 降级开关（协议与两端实现保留；启用时补 §3.7 v1 响应 scalarState 冻结）；S-5 单次解析先行交付；§1.3 非目标 2（不提高 15s timeout 作为主修复）维持 |
| **full 序列化超传输逻辑上限**（v24 新增，F-53） | `reconciliation.oversized` → FallbackFull 后 full > `projection_max_bytes`（46MiB 级单 turn 常伴随巨型 projection） | **typed `full.oversized` 显式终态失败**（R-7：失败显式可见——iOS 明确错误态、不部分渲染、不假成功） | 能力化协商 `projection_max_bytes`（hello，direct/relay 同步；默认 50MiB 维持、提升档 S-7/S-8 冻结定，数值归 OD-7）；内存峰值随上限走由 S-5 单次解析承接；contingency `window_0` 同受上限、不是恢复路径（§3.8 超限恢复语义） |
| **首开全量可见 vs 重开截断的 UX 不一致**（v14 接受项） | 首开后 App 重启 / 重开 | 用户感知「历史变少了」 | 截断提示明确告知（§3.3）；owner 接受并登记（§0 v14 表） |
| **direct gzip 双向误用 / 阈值失控**（v14 新增，S-8） | 压缩小帧（延迟上升）或压缩后更大仍发送 | 性能回退 | 镜像 `relay_gzip_v1` 纪律：≥32KiB 且更小才启用；仅 Mac→client；阈值边界测试（§7.3） |
| summary page 丢关键 UI 状态 | permission/question/active turn 显示异常 | v2 冻结这些字段为 always-visible，不合格不上 |
| **scalar 路线（W1 已定案）的 wire 兼容**（v3 补，F-11；v6 更新，F-22） | 任一 peer 组合出现「window 生效但 execution 恒 idle」（新 Mac + 旧 iOS 为已知既有缺口形态，见 §3.7 组合表，不在默认交付范围——原「S-1 范围」语境，v17 改 F-37） | iOS 双维度 gate fail-closed：未收到 scalar echo 即不走 window（回 full 路径）；§7.2.1 断言覆盖各组合 |
| **re-freeze 批次被绕过**（v6 补，F-21） | 实施者为过测试删改/删除冻结断言，或跳过协议文档/冻结样本更新直接改 schema | 禁止删除断言；re-freeze 五项内容（协议章节/schema/冻结样本/Mac spec/iOS spec）同一批次交付（§3.7 代价行）；**S-7 交付评审检查该批次完整性（v17 改指 F-37；现行主批次 = anchor-newer，W1 v1-响应批次为 contingency）** |
| **materialization 写入成为 live bottleneck（bbolt 大 value 写放大）**（v3 改，F-12） | patch flush 延迟上升 | 异步批量提交；超大 turn（如 6.35MB blob）的写放大由 Phase B item 拆行缓解；必要时按容量阈值后台重写紧凑化；page 读取必须等不低于 requested revision |
| **新依赖供应链**（v3 补，F-12） | bbolt 漏洞告警 | CI `govulncheck` 已覆盖根模块；升级走依赖评审 |
| Relay 分片 / gzip 引入重组问题 | chunk timeout / auth failure | 不改变 v1；新增传输测试后才能动 |

回退边界：

- `projection_window_v1` 回退 = 撤掉 enable / capability echo，返回 full path；数据不受影响。scalar additive capability（W1）回退 = 不 echo 该 capability，iOS 自动回 full 路径；不产生数据迁移。
- **anchor-newer capability 回退（v10 登记；v19 更正 F-41）= Mac 不 echo → iOS 回退全量冷路径 FallbackFull（不回 window_0——其无默认 scalar 补齐且与 v14 定案矛盾）；无数据迁移。**
- materialization 回退 = 停用 materialization store，继续旧 checkpoint；旧 checkpoint 不删除（新 store 文件独立，可直接弃用）。
- **基底缓存回退（v10 登记；v16 改指 F-32）= 不读缓存，重开退回全量冷路径（full + gzip，v14 口径；v13 及以前为 `window_0` 尾窗）；权威链路不变。**
- **v10 方向整体回退 = 停用 S-4 重设计与 S-7，恢复 v9 的 display-only preview + `window_0` 权威重拉设计（v9 经 R9 APPROVED，回退面完整保留）。**
- **v14 方向整体回退 = 恢复 v13 的 S-1 设计（`window_0` 首开）**：机制与两端实现均保留，回退 = iOS 打开路径改回 `pullOpenWindowed` + 补 §3.7 v1 响应 scalarState 冻结批次。
- **`direct_gzip_v1`（S-8）回退 = 不 echo 该 capability，回不压缩传输（= 现状行为）；无数据迁移。**
- v2 未冻结前不实施；v2 可整体不协商。

## 9. 完成定义

### 第一阶段（解决事故，v14 改写：首开 = 全量 + gzip）

- S-0、**S-5（单次解析）**、**S-8（`direct_gzip_v1`）** 完成——首开（全量 + gzip）达标：样本 Direct ≤8s / Relay ≤10s、一次成功、**单次打开重试次数 = 0（§7.3）**；
- **运行中 session 首开执行态 / 权限锁不丢（全量原生含 scalar，E-10 冷路径护栏断言随第一阶段交付——v17 对齐 F-35/F-37）**；
- Mac 与 iOS 阶段耗时可对账。

### 第二阶段（架构收口）

- S-2 / S-3 / **S-4（v10 重设计）/ S-7（v10 新增）** 完成；
- Mac 不再以单个大 JSON checkpoint 作为长 session 派生状态；
- iOS 有明确 stale-while-revalidate 的持久基底缓存，**重开主路径 = restore-then-increment（v10）：恢复基底 + KB 级增量 append（E-11 对账通过）**；
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
15. **（v10 新增，owner 方向变更——非评审意见修订）**：① §3.4 基底缓存重设计（双层形态 / 裸 `tailTurnId` 水位线 / 滑动淘汰 + LRU / restore 状态机）与 §3.8 anchor-newer 协议切片是否完整自洽；§1.2 R-4、§3.1 数据流与不变量 2/6 的改写是否准确；② §3.4.3 两处实施细节（`applyFull(forceReplace:)` `ProjectionReplica.swift:193-212`、appliedRev 重置时序与 `:208`/`:323` 证据链）是否正确、34MB 回退路径论证是否成立；③ S-7 与 W1 的 re-freeze 批次合并交付是否合理；④ E-11 对账验收与 §7.4 场景 9-15 是否可执行；⑤ OD-7 pending 处理（不阻塞评审）是否符合 OD-1/2/3 先例；⑥ 整体回退面（停用 S-4/S-7 即回 v9 行为）是否完整。**本版为 owner 驱动的方向变更，按 repo 评审循环纪律重新进入评审。**（R10 已确认架构方向成立、§3.4.3 证据链闭合——本轮抽核即可。）
16. **（v11 新增，F-29/F-30）**：① §3.4.2/§3.4.3 的两步注入（`applyFull` + `commitIfObserved(lifecycle: .ready)`，锚点 `ProjectionStore.swift:1044-1056`/`:620-626`、`ChatViewModel.swift:1080`）与空白屏机理是否准确、§7.2.12 负对照断言是否可构造；② MemoryResident 删除后打开状态机入口（BaseCacheHit/BaseCacheMiss）是否与 `clear` 唯一调用点（`SessionManagement.swift:232`）的实际可达路径一致、「不得改 switchSession clear」约束（§3.4.2 关键点 6）是否明确。（R10 判 0 阻塞且明示修完两条即达 APPROVED；历项抽核即可。）
17. **（v12 新增，older 分页与 2026-07-12 复盘的关系，owner 质询触发；v13 状态更新：OD-8 已定案）**：① §2.5 补引的分页复盘（**同根因重分类**：前四条同症状不同根因、第五条同根因）与 append/prepend 边界声明（S-1 首开与 v10 newer 增量不受该复盘约束；SSV2 older 路径数据层头部插入与之同坑，`SessionProjectionWindow.swift` insertion cursor）是否准确；② 截断提示要求（§3.3 / §7.4 场景 8）是否完整可验收（文案、判定依据、出现/消失时机）；③ **OD-8 定案 (a)（v13 回填，owner 2026-10-17 裁决）是否六处一致**（§2.5/§3.3/§4/§6 S-1/§7.2.15/§7.4 场景 8）、older gate 交付项（若触发链已接线则 gate 关闭）与「零 `direction=older` 请求」断言是否可执行、older page 独立后续专项的启用前提（复盘三条复活条件）是否明确。
18. **（v14 新增，owner 方向变更 #2——去掉 S-1 首开窗口，首开全量 + Direct gzip）**：① R-1/R-2 改写（有界性从首开撤到重开增量；首开目标 ≤8s/≤10s）与 `window_0` contingency 定位（协议与实现保留、启用时补 scalarState 冻结）是否自洽；② S-7 吸收原 S-1 管道工程（E-3 仍是前置）与 scalarState 范围收缩（只上 anchor 新响应类型、不触碰 frozen v1 字段集——F-21 批次降级 contingency）是否成立；③ S-8 `direct_gzip_v1`（镜像 `relay_gzip_v1` 纪律、permessage-deflate 否决理由）与「gzip 只压网络、S-5 才解 decode」的依赖关系是否成立；④ 接受项登记（超长 session 首开触顶风险 + UX 不一致）与回退面（恢复 v13 S-1 设计）是否完整；⑤ OD-2 处置（被取代、limit 归 OD-7）是否恰当。
19. **（v15 新增，OD-1/OD-3/OD-7 定案回填，理由随行）**：① 三行定案理由是否准确（OD-1：A 与 owner 重开 UX 冲突、C 丢失败显式、B 安全性论证；OD-3：「时间非正确性判由」+ LRU/typed-stale 分工 + 仓内先例；OD-7：四组数值理由与 frozen v1 上限类别 `projection_window.go:27-31` 对齐、128MB 上沿 + iCloud 备份排除、2 页阈值与 v14 冷路径关系、100 turns 协议默认）；② 定案在 §3.4.1/§3.4.2/§3.8/§7.3/§6 S-4/§7.2.16 的一致性（含 §3.4.1 淘汰回退路径已改全量冷路径的 v14 一致性修正）；③ 决策表是否确已无 pending 项。
20. **（v16 新增，F-31~F-35）**：① §3.6 opcode 自描述机制（text/binary 分流、三种替代的否决理由、协商 gating、阈值判定位置）与 S-8 冻结批次（binary 帧语义 / binary fixture / opcode 分流断言）是否自洽、可实现、无遗漏（如 iOS WebSocket 栈对 binary 帧回调的支持核对）；② F-32 清扫是否干净——正文活引用无残留 S-1（`grep "S-1"` 仅剩 §0 历史表的时代性记录），改指对象（trace→S-7、older gate→S-4、E-2/E-10→S-7、S-5 前置→无、S-3 前置→v2 冻结、§8 回退→全量冷路径）是否恰当；③ anchor 独立样本文件与新增 spec 读取点（F-33）、held 注册断言与并发 detail 负对照（F-34）的可构造性；④ E-10 双口径（anchor 主对象 + 冷路径护栏随 S-5/S-8 交付）与 §7.2.10 一致性。（R11 明示「F-31 修完即可解除阻塞」；本版定案 F-31 并全量落地四条建议。）（R12 已核验 F-31 定案质量、F-33/34/35 闭合——本项抽核即可。）
21. **（v17 新增，F-36~F-38）**：① §3.6 方向更正是否与代码一致（client→Mac 现状 binary：`BridgeWebSocketConnection.swift:304-306`；Mac 丢弃 opcode：`server.go:670`）、「零 binary 帧」断言是否已明确限定 Mac→client、§7.2.17 负对照（不得改 client→Mac 的 binary 发送）是否可构造；② F-37 清扫后正文活引用收口（`grep -n "S-1"` 与 `grep -n "自动满足"`——剩余命中应仅在 §0 历史表、§10 历轮评审项、以及带「登记时语境/原 S-1」标注的行内）；③ S-8 的 iOS transport 改造登记（`receiveData()` 签名暴露 opcode，枚举与读循环现成）与改造面评估是否准确。（R12 判 0 阻塞且明示修完即达 APPROVED；本版全量落地三条。）（R13 已核验三条全部闭合——本项归档。）
22. **（v18 记录，评审循环闭合；v19 随 R14 撤销「闭合」结论）**：R13 verdict = APPROVED（0 阻塞 + 2 建议）；v18 落地 F-39/F-40 零设计变更。**R14 终审判 REVISION_REQUIRED（5 阻塞 F-41~F-45），本项的「方案阶段闭合」结论撤销**——F-39/F-40 的闭合维持。
23. **（v19 新增，F-41~F-45）**：① §3.9 路径总表是否消除全部触发/动作歧义（不变量 3、R-3/R-4、§7.3 full open 行、五处回退条款、Gate B OD-2——同表一致）；② baseDigests 对账契约（Mac 生成 digest / iOS 回传 / changedTurns/removedTurnIds / syncing 以对账完成为清除前提）能否闭合 F-42 反例（离线 A旧→A新；比较内容终态非仅 turn ID 集合；同/跨 epoch）且与不变量 6（Mac 无 per-device 状态）相容；③ anchorRefresh 独立字段（不计页预算、never-split、空 turns+anchorRefresh、超大锚不阻塞前进、超限阈值改按 turns 页数）是否消除 F-43 三处矛盾；④ §3.4.3 采用事务（身份改写副本 + appliedRev=0 + coverage 清零，同事务不可插入）与 §7.2.12 身份一致性断言（revisionKey/presentation/full stale 非空与空页两路）是否闭合 F-44；⑤ §3.2.3 四态分类（growth-trust + 已知限制 + baseDigests 兜底）与 §7.1.2/§7.3 的 IO 断言是否自洽且可实测（读取字节区间）。

24. **（v20 新增，F-42~F-47）**：① token 生命周期绑定不变量（五条规则：同源交付/patch 失效/可选优化/写盘/Mac 匹配）能否闭合 R15 的 V2 内容+V1 token 反例，E-11 两组新反例是否可构造；② 对账完成事务第二阶段（零新增也显式进 head）+ per-session 恢复门（跨 await 排队/版本校验）是否消除混合身份与恢复风暴，§7.2.12 的 await 边界反例是否可执行；③ 「两层共享同一 Kernel」的承认与 OD-9 登记（growth-trust 降级、R-7 例外须 owner 裁决）是否诚实且门控正确；④ 对账响应总预算（8MiB/48MiB/分页/去重/部分修正不清 syncing）与 50MiB transport 上限的关系是否闭合 F-46 膨胀反例；⑤ S-2 范围扩展（source ledger 生产链全链测试）与 R-5 精确化（重投影 suffix-only ≠ 零校验读）是否自洽。

25. **（v21 新增，F-48~F-50）**：① 分层预算（总硬顶 48MiB / anchorRefresh 独占 / 对账预算 8MiB 不含 anchorRefresh / typed oversized）是否消除「20MiB anchor 同时合法与越界」的判定矛盾，OD-7 行是否已登记新数值；② §3.2.3 尾部 IO 上界与 (ii) 默认整读是否一致（无 v19 残留）；③ token 绑定「全 ops 后终态」规则与 mixed-patch 测试（§7.2.13）是否闭合 F-50。

26. **（v22 新增，F-48 残留/F-49 残留/F-51）**：① 预算边界（anchorRefresh ≤46MiB 为 envelope 留 ≥2MiB；changedTurn never-split 独占额度 + 整 turn 粒度分页 + typed oversized；总硬顶 48MiB）是否消除全部边界歧义，§7.1.5 的 8/20/47/48/48+1 用例是否可构造；② §3.2.3 组合路径与 §7.1.2 的「不整读」范围限定（仅 window handler 冷检）是否与 (ii) 默认整读一致、无残留；③ §7.3 Mac restart 指标是否已去重为单行。

27. **（v23 新增，F-48 残留闭合——R18）**：① §7.1.5 边界用例（46MiB anchor 恰满合法 / 46MiB+1 超限，47/48 归 46+1 判定）与 §3.8 独占额度规则、OD-7 行是否完全一致、无二义残留；② 确定性打包算法（mandatory 2MiB 预留 → anchorRefresh 仅首包 → turns → changedTurns 按 turnId 升序；首包对账空间 = min(8MiB, 48−2−anchorRefresh)）与修正续页形状（不携带 anchorRefresh/turns）是否使 46+9 / 20+46 / 46+46 三个组合均有全字段 ≤48MiB 的可实现页序列（§7.1.5 组合验收①-③）；③ 续页绑定规则（同 kernel cut、对账完成事务全部到齐一次性提交、续页 typed stale → FallbackFull 无部分状态泄漏）与 held 注册扩展（changedTurns）是否与 §3.4.3 对账完成事务、token 生命周期不变量第 1 条一致。

28. **（v24 新增，F-48 残留 / F-52 / F-53——R19）**：① 对账续页携带 turns 剩余片（turn phase 先于 correction phase、never-split、计入「超 2 页退全量」）与「对账事务内水位线 = 已交付链末端」是否解除 46MiB anchor + 后继 turn 死锁（§7.1.5 后继 turn 交付验收①-③：1KiB / 9MiB / 42+6MiB 三形态，页 ≤48MiB、链不跳、水位线最终前进、syncing 全到齐才清）；② 自包含 cursor（完整复查集随请求携带、每续页独立取当前 head、已发 token 失效重发覆盖、采用最后一页 head）是否在撤回「同 cut」宣称后仍成立单调覆盖一致性论证，且与不变量 6（Mac 无 per-device 状态）、现有 fence/快照机制（不跨请求、无历史版本）相容——换连接重发幂等与两设备不串扰断言是否可构造；③ typed `full.oversized` 显式终态 + `projection_max_bytes` hello 协商（默认 50MiB、提升档冻结定、contingency window 不是恢复路径）是否消除「不可传输 full 冒充恢复成功」，两级 typed 链（reconciliation.oversized → FallbackFull → full.oversized）验收与 §8 风险表 / R-2 双失败线是否一致。

29. **（v25 新增，F-54 / F-55 / F-56——R20）**：① 全文活文本（§0/§10 历史区除外）是否已无「续页不含 turns / 只含 changedTurns / 同 cut 续页 / cut 失效」残留，§7.1.5 组合验收④与后继 turn 交付验收、自包含 cursor 验收②③ 是否在同一续页形状（续页 = 内容页、每续页独立 head、采用最后一页 head）下可同批通过；② epoch 中止分支定案 iOS 侧判定（hello_ack epoch 比对、Mac 侧不比对、续页请求不携带 epoch、复查集兜底）是否与 §3.4.2 打开状态机「epoch ≠ 当前：流程照常」分支自洽——打开时点（缓存 provenance 陈旧）与对账事务中途（交付链跨 epoch）两场景的区分是否清楚、兜底论证是否成立，re-freeze 冻结清单登记是否足以让实施者无需自行发明；③ 复查集已发项被移除 → `removedTurnIds`（tombstone、不重发）是否与 removedTurnIds 既有定义、token 生命周期不变量一致，自包含 cursor 验收②的移除构造是否可执行。（R21 已判三条全部闭合——本项归档抽核。）

30. **（v26 新增，F-57 / F-58——R21）**：① 全文活文本（§0/§10 历史区与「原『…』为 v14 前/v10 时代残留」更正注记除外）grep `window_0 回退|回退 \`window_0\`|退 window_0` 是否零命中，§7.3 三行（重开增量路径 / full projection open / 重试次数）是否在同一回退目标（FallbackFull）下互洽；② §3.8 相位边界定案（turn 装不下即切页、本页剩余空间不回填、changedTurns 不得越过未交付 turns 提前装页、turns 交付完毕后 changedTurns 可与末个 turns 同页）与 §7.1.5 验收③、「42MiB anchor + 6MiB successor + changedTurns」组合的唯一合法页序列是否可写出、与打包确定性断言⑤是否一致。（R22 已判两条全部闭合——本项归档抽核。）

31. **（v27 新增，F-59——R22）**：① §3.8 自包含修正 cursor 与 §7.1.5 验收①的复查集是否为同一集合的唯一定义（原始 baseDigests 全部项 ∪ 已发项 token、每续页携带全量不收缩——冻结评审可据此直接写出请求字段形状，无需实施者自行选择）；② 单调覆盖②「复查集内全部项」与 removedTurnIds 定义（任何项被移除 → tombstone）、token 生命周期不变量是否一致；③「早页复查命中的确认项在续页请求前被 live patch 修改」窗口的交付路径是否唯一明确（全集复查重发是对账页内唯一可靠载体；恢复门「版本校验丢弃」分支是否不再承担该窗口的内容保证），验收②新增构造是否可执行；re-freeze 冻结批次是否把全集复查集的请求字段形状一并冻结（R22 冻结门登记项）。（R23 已判 F-59 闭合（漂移窗口关闭机理独立推演成立）——本项归档抽核。）

32. **（v28 记录，R23 APPROVED 后纠错——非再评审请求）**：① F-60 落地后 §3.8 内 `changedTurns` 的来源范围（「复查集中」）与单调覆盖②的复查对象在字面上是否指向同一集合、无需读者自行调和（re-freeze 誊写随同一口径）；② 元审核更正记录（§0 v28）的旧/新身份、影响范围与「后续引用 U 来源身份以 §2.1 为准」的处置是否完整可追溯——实施/后续评审引用 Codex 上游提交时是否按 §2.1 的 `main@ce0323f2f2d3b25e82806cd555237da3db4e1897` 执行、不再转引 R23 报告 U 行的无效哈希。（R24 已判 F-60 闭合、更正记录事实基础独立亲测成立——本项归档抽核。）

33. **（v29 记录，R24 APPROVED 后纠错——非再评审请求）**：① F-61 落地后冻结评审能否据 §3.8 直接写出首包/续页两种请求形状（无标记 = 首包携带 anchorRefresh / 回传修正 cursor = 续页不携带），实施者无需自行发明「续页如何不被当作首包」的判定方式；② 元审核更正记录（§0 v29）的旧/新行号（注释块 200-205 / turnIDs 构造 206 / 完成事务 210-211）与「方案区间引用 `:201-211` 维持有效」的处置是否完整——后续实施/评审引用该代码块时是否按实际行号执行、不转引 R24 报告锚点表的偏移子行号。

---

设计师交接块：

```text
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_version: v30（2026-10-18，R25 APPROVED 后落地唯一建议 F-62，零设计变更：§3.8 首包打包句「三者合计」→ 分层计量表述（turns/changedTurns 用本页剩余对账空间，anchorRefresh 不计入对账预算、另按独占额度与总硬顶计量）；额度/顺序/续页行为/验收不变；R25 闭合标准明示无需再启动评审循环；历轮闭合项维持）
plan_sha256: 88dede7eeae620260550bf567c0a364493d936cff9064b579f41cbc455a4a388  # 正文（不含本交接块，切分 = raw[:idx].rstrip(\n)，idx = "\n---\n\n设计师交接块"）
scope: full
review_status: R25 判 APPROVED（0 阻塞 + 1 建议 F-62，本版落地；两条 META-FAIL 经 R25 复现定性为报告记录缺陷）；方案阶段闭合，实施受证据/决定/冻结/授权门约束
open_gates: E-3(S-7 开工前), E-4, E-5, E-6, E-7, E-10, E-11, OD-9(growth-trust 档，S-2 按默认档不受阻塞), S-7/S-8 re-freeze 批次(含 F-55 epoch 语义、F-59 全集复查集请求字段形状、F-61 续页身份标记字段、F-60 changedTurns 口径誊写、projection_max_bytes wire 形态), S-6 owner 真机手动矩阵授权
```
