# Session Loading 重构方案 — 第二轮评审

> 评审对象：[docs/2026-06-13-session-loading-systemic-redesign.md](2026-06-13-session-loading-systemic-redesign.md)（评审修订版）
> 评审依据：[第一轮评审](2026-06-13-session-loading-review.md) 的 17 项意见 + 处置表 §11
> 评审日期：2026-06-13
> 核对范围：文档每一项事实论断对照 `cccode-macbridge` 与 `opencodeIosNew` 两个仓库

---

## 0. 结论

**修订版质量显著提升，方向与第一轮建议一致。** 处置表 §11 逐条对应，且我核对了正文——不是空壳回应，是真实落地。建议**修正 2 处 Major 后即可进入 Phase 0**。本轮不再有 Blocker。

| 严重度 | 数量 |
| --- | --- |
| 🟠 Major（影响分页/索引正确性，Phase 0 前应冻结） | 2 |
| 🟡 Minor（精度/完整性/措辞） | 6 |
| ✅ 已验证为真的事实论断 | 12+ |

---

## 1. 第一轮意见处置核对（已逐条回到正文确认）

| 意见 | 处置表声明 | 正文是否真实落地 |
| --- | --- | --- |
| B1 Codex 索引已存在 | 采纳 | ✅ §2.1 列出缓存事实；§5.1 Codex 仅加固不重建；§5.6 加固项明确 |
| B2 根因未测量 | 采纳 | ✅ §4 Phase 0 强制门禁，分段指标齐全 |
| B3 resume 近空操作 | 采纳 | ✅ §2.1 确认；§7 降为可选，给出门槛（15%/150ms） |
| B4 Claude 接入点错误 | 采纳 | ✅ §5.4 明确接入 `handleListSessions`/`scanSessionsFromProjectDir` |
| M-1 反向读取破坏分组 | 采纳 | ✅ §6.2 明令禁止；§6.3 改为 `logicalMessageSpan` + tail replay |
| M-2 cursor 碰撞 | 采纳 | ⚠️ §6.6 加了复合键，但与 §5.3 排序键**不一致**（见新-N1） |
| M-3 写锁内 I/O | 采纳 | ✅ §5.3 锁外构建 + 锁内换指针 + singleflight |
| M-4 size 进指纹 | 采纳 | ✅ §5.2 / §6.3 / §5.6 三处都含 size |
| M-5 timer 触发 | 调整采纳 | ✅ §5.5 改请求时核对为主，理由充分，接受 |
| M-6 capability 机制 | 采纳 | ✅ §7.2 用 core 接口 + 每后端 capability，不动 RegisterAck |
| M-7 limit 已存在 | 采纳 | ✅ §2.1/§6.5/§6.6 反复声明，仅 cursor 字段为新增 |
| m-1..m-6 Minor | 全采纳 | ✅ 行号校正、验收分流、iOS 全路径、基线方法、删 128 行、删 LRU 均落地 |
| M1-M6 缺失项 | 全纳入 | ✅ Phase 0 / 跨项目 key / delete 失效 / 序列化体积 / OpenCode 边界 / Codex title |

**结论：17 项意见全部真实落地，处置表可信。** 这在迭代式设计文档里很少见，值得肯定。

---

## 2. 自我更正（第一轮我的一处不精确）

第一轮 M-6 我写「现有 caps 用 camelCase，文档的 `open_session` 是 snake_case，需统一」。复核 [agent_descriptor.go:88-133](../go-bridge/agent_descriptor.go#L88-L133) 后确认：**每后端能力（`deriveCapabilities` 产出）本来就是 snake_case**（`model_switch`、`session_history`、`session_mutation`、`content_chunking`…）。camelCase 只存在于 server 级 `hello_ack.capabilities`（[hello_handler.go:118-124](../go-bridge/hello_handler.go#L118-L124)）。

我把两层 capability 的命名惯例混淆了。修订版把新能力（`session_pagination`、`batch_open_session`）放在**每后端层并用 snake_case，是完全正确的**。此项撤回。

---

## 3. 新增问题（由修订版新设计引入）

### 🟠 新-N1：列表稳定排序键在 §5.3 与 §6.6 之间不一致，且 Codex 现有排序无 tie-breaker

两处对列表排序的 tie-breaker 顺序**互相矛盾**：

- §5.3 第 5 步：`updatedAt DESC, projectKey ASC, sessionID ASC`
- §6.6：`updatedAtMillis DESC, sessionID ASC, projectKey ASC`

cursor 分页要无重复无遗漏，**snapshot 的排序键与 cursor 编码的复合键必须逐字段同序**。当前两处 projectKey/sessionID 谁先不一致，实现者无论跟哪一处都会与另一处冲突。

更关键的是 **Codex 侧根本没有确定性排序**：现有 [list.go:134-136](../agent/codex/list.go#L134-L136) 只按 `ModifiedAt DESC` 排，且 entries 来自 map 迭代（[list.go:130-133](../agent/codex/list.go#L130-L133) `for _, entry := range c.files`），同 `ModifiedAt` 时顺序由 map 遍历决定——**非确定**。而 §6.6 的 `projectKey ASC` 是 Claude 维度，Codex session 没有 projectKey 概念（Codex 平铺在 `~/.codex/sessions/`，只有 sessionID + cwd 元数据）。

> 建议：
> 1. 统一 §5.3 与 §6.6 的 tie-breaker 顺序（任选其一，全文一致）。
> 2. 把"确定性排序键"定义为**按后端**：Claude = `(updatedAt, projectKey, sessionID)`，Codex = `(modifiedAt, sessionID)`。
> 3. §5.6 Codex 加固项补一条「为 `sessionListCache.sorted` 增加 sessionID tie-breaker」，否则 Codex 列表分页会漂。

### 🟠 新-N2：append 容忍的 cursor 语义未定义——活跃会话向上翻页可能每次都失效

§6.5 cursor 编码「transcript fingerprint generation」，fingerprint 不匹配返回 `cursor_stale`。但**活跃会话在流式输出时每个 append 都改 size/mtime → 改 fingerprint**。若向后翻页（`beforeCursor: oldestCursor` 加载更早消息）因此每次返回 `cursor_stale`，那么最需要该功能的场景——正在进行的对话里向上滚动——会彻底不可用。

§6.9 已把这点列为测试「transcript append 后旧 cursor 返回 `cursor_stale` **或**按定义继续有效」，但「或」等于没决定。

向后翻页引用的是 transcript **较旧前缀**里的位置，tail append 不应使其失效。正确语义应是：cursor 的 generation 代表**它所分页的前缀**，而非整个文件；append 只推高 `newestCursor`，不 invalidate 已有的 `oldestCursor`。只有前缀被改写（极少见，如 session 元数据回写）才 `cursor_stale`。

> 建议：在 §6.5 明确冻结为「**向后 cursor 对 tail append 容忍**；只有其引用前缀的 fingerprint 变化才 stale」。这是决定"向上滚动加载更早消息"能否在生产中使用的核心语义，必须在 Phase 2 实现前定死，不能留到测试时再选。

### 🟡 新-n1：`logicalMessageSpan` 缺少"未闭合 tool_use"标记，无法定位 tail-replay 起点

§6.3「活跃文件追加时，从最后一个**可能仍未闭合** entry 的 `ReplayStart` 重放」。但 span 结构只有 `Ordinal/StableID/ReplayStart/EndOffset`，没有字段表达"该 entry 是否还有未匹配 tool_result 的 tool_use"。实现者无从判断从哪个 entry 起算"未闭合"。

> 建议：span（或索引）增加 `OpenToolUses int`（或 bool），建索引时随 tool_use/tool_result 配对维护；tail-replay 起点取最后一个 `OpenToolUses>0` 的 entry 的 `ReplayStart`。

### 🟡 新-n2：span 字节区间会重叠，page 重建需"取并集再 replay"——未写明

因为 tool_result 会把前驱 assistant entry 的 `EndOffset` 外推到 result 位置（§6.3），该 entry 的字节区间会**越过其后继逻辑消息**。于是取 page `[i..i+N]` 不能逐 entry 切 `[ReplayStart_i..EndOffset_i]`（会漏掉外推的 result），而要取**全 page 的 `min(ReplayStart)..max(EndOffset)` 并集**再跑 grouping builder，再按 ordinal 抽取。文档靠"从 ReplayStart tail replay"隐含了这点，但没点破，容易让实现者写出漏绑 tool_result 的残缺消息。

> 建议：§6.3 补一句「取页 = 读该页所有 span 的字节并集，整体重跑 grouping，按 ordinal 截取」。另注明最坏情况（result 远离 use）下重建一页仍可能读较大区间——这是 Phase 0 要量化的成本之一。

### 🟡 新-n3：§12「message limit 参数 | types.go:92」指错位置

[types.go:92](../go-bridge/types.go#L92) 是 `ResumeSessionParams`，`Limit` 字段在 [types.go:100](../go-bridge/types.go#L100)（属于第 97 行的 `GetSessionMessagesParams`）。建议改为 `types.go:97` 或 `:100`。其余 §12 位置均已核对正确。

### 🟡 新-n4：§5.5「Bridge 自己创建/重命名/归档会话后显式失效」对 Claude 不完全成立

Claude session 文件由 **外部 `claude` CLI 异步写入**，不是 Bridge 创建；Bridge 只在 rename/archive 这类它发起的 RPC 时才"知道"。新建会话仍只能靠下次 list 的 fingerprint 核对发现。请求时核对确实足够保证一致性（这点 §5.5 本身成立），但措辞「Bridge 自己创建…」对 Claude 侧易误导。

> 建议：改为「Bridge 发起的 rename/archive/delete 成功后显式失效；外部进程（CLI）造成的新增/改动由请求时 fingerprint 核对捕获」。

### 🟡 新-n5：§6.3 持久化与 §6.4 预热的边界风险（建议显式 hedge）

- 持久化「可以」落盘（§6.3）：未提写原子性 / 多实例并发写。fingerprint 校验能保正确性，但建议加一句「单写者 + 原子替换，损坏即丢弃重建」。
- 预热最近 N 个 transcript（§6.4）：在 session 很大很多时，后台预热本身就是 Phase 0 要诊断的 I/O/内存峰值的潜在来源。§6.4.4 已用"N 与并发数由 Phase 0 决定"对冲，足够；仅建议把"预热不得触发内存峰值"也作为 Phase 0 观测项。

两者均非阻塞，但写明可减少实现期返工。

---

## 4. 事实核对结果（修订版的可验证论断）

| 论断 | 核对 | 结果 |
| --- | --- | --- |
| iOS 仓库 `/Users/jacklee/Projects/opencodeIosNew` 存在 | `test -d` | ✅ 存在 |
| §12 所列 8 个 iOS 文件全部存在 | 逐个 `test -f` | ✅ 全部存在 |
| §2.2「已在 iOS 工作区核对」 | 文件存在性 | ✅ 属实（非空壳声明） |
| §4.3「现有 `PerformanceTracer`」 | `grep` iOS 源码 | ✅ 存在（ChatViewModel+SessionManagement/MessageSync 引用，pbxproj 注册） |
| iOS `fetchSessions`/`fetchMessages` 现签名 | grep | ✅ 存在，且**当前无 cursor 参数** → cursor 确属新增工作，与 §6.5/§6.8 一致 |
| §13 `docs/protocol/schema/bridge-v1.types.ts` | `test -f` | ✅ 存在 |
| §12 Bridge 行号（handleListSessions 2185、scanSessions… 2323、scanClaude… 2359、getMsg 2434、delete 2630、resume 2649、dispatch 520、ocDispatch 592、codex cache 42/386、rich_history 18、claude ListSessions 436、loadClaudeRichHistory 781） | 逐行核对 | ✅ 全部正确（仅"message limit types.go:92"偏移，见新-n3） |
| `deriveCapabilities` 为 snake_case | grep | ✅ → 修订版 `session_pagination`/`batch_open_session` 命名正确（撤回我第一轮 M-6 的 casing 投诉） |

**事实层全部站得住，文档可信度高。**

---

## 5. 建议

1. **进入 Phase 0 前修正 2 处 Major**：
   - 新-N1：统一 §5.3/§6.6 排序键，并按后端定义 tie-breaker + 给 Codex 缓存补 sessionID tie-breaker。
   - 新-N2：在 §6.5 冻结「向后 cursor 对 tail append 容忍」语义。
2. **顺手修 4 处 Minor**（新-n1 未闭合标记、新-n2 span 并集重建、新-n3 行号、新-n4 措辞），均为低成本澄清，能避免实现期返工。
3. 修完后即可启动 Phase 0。Phase 0 的产出（`docs/2026-06-13-session-loading-baseline.md`）将自然验证/修正 §6.4 预热参数、§5.6 Codex 加固项清单、以及新-n2 的最坏情况重建成本。

---

*本轮评审无 Blocker。修订版对第一轮意见的处置质量很高，且事实论断经两仓库交叉核对全部成立。锁定上述 2 处 Major 语义后，方案可作为 Phase 0 的可靠前置输入。*
