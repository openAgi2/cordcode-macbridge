# Session Loading 重构方案 — 终审（第三轮）

> 评审对象：[docs/2026-06-13-session-loading-systemic-redesign.md](2026-06-13-session-loading-systemic-redesign.md)（第二轮评审修订版）
> 评审依据：[第一轮](2026-06-13-session-loading-review.md)、[第二轮](2026-06-13-session-loading-review-r2.md)
> 评审日期：2026-06-13

---

## 0. 结论

**通过，可进入 Phase 0。本轮无 Blocker、无 Major。**

第二轮的 2 项 Major（排序键冲突 / append 致 cursor 失效）与 5 项 Minor 均已真实落地，且新增的 prefix-generation 谱系设计经推演**正确且自洽**。剩余仅为实现期的低优先级澄清项，不阻塞 Phase 0 启动。

三轮评审下来，文档已从「方向对但与代码现状脱节」演进为「事实准确、设计可实施、风险有对策」。建议以此版本作为 Phase 0 的正式前置输入。

---

## 1. 第二轮意见处置核对（已逐条回到正文确认）

| 第二轮意见 | 落地情况 |
| --- | --- |
| 🟠 新-N1 排序键冲突 + Codex 无 tie-breaker | ✅ §5.3 与 §6.6 的 Claude 键现已逐字段一致（`updatedAt DESC, projectKey ASC, sessionID ASC`）；§6.6 明确 Codex 用 `(modifiedAt DESC, sessionID ASC)`；§5.6 把 Codex tie-breaker 加固项写明，并准确诊断现有 [list.go:131-136](../agent/codex/list.go#L131-L136) 的 map 遍历漂移 |
| 🟠 新-N2 append 致 backward cursor 失效 | ✅ §6.5.1 新增「Cursor append 语义」，冻结 prefix-generation 谱系：纯 tail append 继承旧 generation，backward cursor 继续有效；前缀改写/截短/替换才 `cursor_stale` |
| 🟡 新-n1 span 缺未闭合标记 | ✅ `logicalMessageSpan` 增加 `OpenToolUses`，定义增减规则与 tail-replay 起点（含"无未闭合时从末 entry 重放以允许尾部合并"） |
| 🟡 新-n2 span 重叠回放未定义 | ✅ §6.3 明确"取 `min(ReplayStart)..max(EndOffset)` 并集整体 replay，再按 ordinal 抽取；不得逐 span 独立解析"，并加最坏情况度量 `page_replay_bytes` |
| 🟡 新-n3 types.go 行号 | ✅ §12 已改为 `types.go:97-101`，核对正确 |
| 🟡 新-n4 Claude 创建措辞 | ✅ §5.5 区分「Bridge 发起的 rename/archive/delete」与「外部 CLI 写入由下次 fingerprint 核对捕获」 |
| 🟡 新-n5 持久化与预热边界 | ✅ §6.3 加单写者+原子替换+损坏重建；§6.4 加进程级内存与在途字节预算，并把预热纳入 Phase 0 观测 |

处置表 §11.5 与正文一致，甚至记录了我第二轮的自我更正（capability casing）。处置质量在三轮里持续可靠。

---

## 2. 新设计的正确性评估

本轮最关键的新增是 **prefix-generation 谱系 + continuity anchor**（§6.5.1、§6.3）。我对其做了对抗性推演：

**判定：设计正确且自洽。** 关键在于 append 判定受 **fingerprint 闸门**（size+mtime）前置约束，使几个看似可能的漏洞实际不成立：

- **同尺寸原地改写**（如 [list.go:380-407 patchSessionSource](../agent/codex/list.go#L380-L407) 把 `source:exec`→`cli`、`codex_exec`→`codex_cli_rs`，后者变长 2 字节）：size 变 + 字节整体位移 → continuity anchor 失败 + fingerprint 不匹配 → 走重建路径、新 generation、旧 cursor `cursor_stale`。**正确**（这是真实会发生的场景，方案处理对了）。
- **同尺寸同 mtime 的中间字节改写**：append 路径要求 size 增长，未增长即不进 append 路径 → fingerprint（mtime 变）触发全量重建 → 新谱系。**无漏洞**。
- **inode/替换**：新文件 inode 变 → 非祖先 → `cursor_stale`。**正确**。

`OpenToolUses` + 并集 replay 的组合保证：未闭合 tool_use 跨 append 后能从正确 `ReplayStart` 重建；重叠 span 不丢 tool_result。逻辑闭合。

§6.9 测试清单覆盖了 append 容忍、截短 stale、anchor 失败不误判、span 重叠、未闭合跨 append 等所有关键边界——是这份方案最扎实的部分。

---

## 3. 实现期低优先级澄清（非阻塞，建议在 Phase 1/2 实现时落实）

以下均不影响 Phase 0 启动，也非正确性缺陷，仅在实现时定细则可减少返工：

1. **continuity anchor 应为校验和而非字节逐位比较。** §6.5.1「验证旧 EOF 前固定窗口的 continuity anchor」未指明形式。建议明确为该窗口的强校验和（如 xxHash/BLAKE3 截断），使碰撞概率可忽略；并定义窗口大小为可配置常量。

2. **generation 谱系跨 Bridge 重启的持久化行为需写明。** §6.3 的 `transcriptPageIndex` 只含单个 `PrefixGeneration string`，未表示祖先链。若仅持久化当前 generation，重启后旧 backward cursor 因无法证明祖先关系而 `cursor_stale`——这是**安全降级**（不会拼错历史），但与「活跃会话持续输出不失效」的承诺在重启边界上有出入。建议一句说明：祖先链是否持久化；若不持久化，明确"重启后 backward cursor 返回 stale 属预期"。

3. **transcript index 的刷新并发模型未声明。** §5.3 的 singleflight 是给列表索引的；Phase 2 的 transcript index 同样应**按 session singleflight**，否则两个并发 `get_session_messages` 会重复建索引。建议补一句。

4. **`OpenToolUses` 须对所有"产生结果"的行类型递减。** [rich_history.go](../agent/codex/rich_history.go) 中 Codex 有三种结果行：`function_call_output`(164)、`custom_tool_call_output`(170)、`event_msg/patch_apply_end`(71-72)。若实现只对第一种递减，OpenToolUses 会卡在 >0，导致永远从过早起点的 replay（正确但浪费）。建议在 §6.3 注明"按各 backend 的全部结果行类型维护"。§6.9 已覆盖 patch result 测试，意识到位，仅需落到字段定义。

5. **generation 粒度与谱系深度。** 若每个检测到增长的刷新都建新 generation，长活跃会话谱系会很深，祖先判定 O(深度)。建议明确 generation = 每次索引刷新（非每行 append），并对谱系做有界扁平化（如周期性把祖先折叠进根）。低优先级。

---

## 4. 事实核对

- §12 `types.go:97-101`：核对正确（`GetSessionMessagesParams` + `Limit` 字段）。✅
- §5.6 对 Codex 现状「map 遍历 + 仅 ModifiedAt 排序」的诊断：与 [list.go:131-136](../agent/codex/list.go#L131-L136) 完全一致。✅
- 其余 Bridge/iOS 位置在第二轮已全部核对，本轮未变。✅
- 新增设计（prefix generation、OpenToolUses、并集 replay）为设计性内容，不涉及新的代码事实声明，无需额外核对。

**文档事实层继续全部成立。**

---

## 5. 最终判断

| 维度 | 评价 |
| --- | --- |
| 问题诊断 | ✅ 强制 Phase 0 先测量，不再假设瓶颈 |
| 代码现状准确度 | ✅ 三轮核对全部成立，行号已校正 |
| 设计正确性 | ✅ 索引、分页、cursor 语义、并发均自洽；对抗推演未发现漏洞 |
| 向后兼容 | ✅ 可选字段 + 新方法 + 每后端 capability，不改 protocol major |
| 风险与边界 | ✅ 范围边界清晰，含 OpenCode/MacBridge 不覆盖声明 |
| 可实施性 | ✅ Phase 门禁明确，交付物与测试清单具体 |

**放行 Phase 0。** 第 3 节的 5 项澄清建议在 Phase 1/2 实现时顺手补入，不必现在返工文档。Phase 0 产出的 `docs/2026-06-13-session-loading-baseline.md` 将自然验证/定参：预热 N 与并发、Codex 加固清单、§6.3 的 `page_replay_bytes` 最坏情况、以及 generation 粒度选择。

三轮评审结束。该方案已具备作为实施蓝本的成熟度。

---

*终审通过。感谢迭代过程中对每一条意见的认真处置——这是设计文档协作的一个高质量范例。*
