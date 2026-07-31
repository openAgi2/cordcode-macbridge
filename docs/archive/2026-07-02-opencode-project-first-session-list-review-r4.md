# 第四轮评审：OpenCode Project-First Session List Redesign

Date: 2026-07-02

评审对象：[docs/2026-07-02-opencode-project-first-session-list-plan.md](../2026-07-02-opencode-project-first-session-list-plan.md)（已按第三轮 [review-r3](2026-07-02-opencode-project-first-session-list-review-r3.md) 打磨）

方法：逐条核实本轮 6 处改动是否落地，并做最终一致性扫读。

---

## 0. 总体结论

**6 处改动全部准确落地，方案内部一致、无新矛盾，已具备进入 exec-plan 的条件。** 本轮只剩**一个澄清**（不是错误）：决策门产出的是"验证/预期范围"，不是"两条代码分支"。把它说清，可避免实施者真的去写 `if cursorVerified {...} else {...}` 的编译期分支——那正是你一直想避免的返工。

---

## 1. 本轮 6 处改动落地核实

| 改动 | 位置 | 核实 |
| --- | --- | --- |
| 可观测性修正：请求计数用现有 `go-bridge: RPC request` 日志即可，不卡首发 | §10.9 L481-482、§11.4 L498、§12 L519 | ✅ 三处一致，措辞准确（区分了"计数已可证"与"富诊断仍缺"） |
| 富诊断日志保留为增强项（directory/project、limit、cursor-present、result_count、duration） | §10.9 L482、§11.4 L498 | ✅ 明确"parallel with the proxy work"，非硬门禁 |
| `ocHandleListSessions` 必须新提取 limit/cursor 并透传 | §5.2 L237 | ✅ 写到位，且点明了 `extractPositiveInt(msg,"limit")` / `extractStringParam(msg,"cursor")` 两个函数名，实施者不会猜 |
| §9.1 httptest 断言上游请求捕获到 limit/cursor | §9.1 L418 | ✅ 可测（httptest.Server 捕获 `r.URL.RawQuery`） |
| §9.3 手动验收按 full / array-only 分支 | §9.3 L442、L446、L447 | ✅ step 4/8 标"Full cursor-capable only"，新增 step 9 array-only 专属 |
| §11.3 改清：两分支都支持 limit+信封，仅 cursor 透传取决于决策门 | §11.3 L497 | ✅ 措辞清楚 |

无遗漏、无过度修订。

---

## 2. 一个澄清：决策门是"验证范围门"，不是"代码分支门"

这是本轮唯一值得记录的点，源自对 §5.2 + §11.2 + §11.3 的交叉读。

**事实**：bridge 侧其实是**单一运行时自适应实现**，两条"分支"在代码上完全相同：

- **双形状解码**（§5.2 L256-272）：array 与 `{data,cursor}` 在运行时按返回形状识别，与 curl 预检结果无关。
- **limit 提取 + 信封**：两分支完全一致。
- **cursor 透传**：handler 提取 cursor（无则空串）并透传；`ocProxy` 在 cursor 为空时**省略** `cursor=` 查询参数（见下文实现提示），无副作用。
- **`hasMore`/`nextCursor`**：由上游是否真返回 cursor 决定，运行时得出。

因此 §11.1/§11.2 的"决策门 → full / first-page-only 两条路径"**不是两条代码路径**，它实际只决定：

1. 开发者在 4096 上**验证哪些手动步骤**（§9.3 已按此分支）；
2. protocol 文档与验收条款**承诺什么**（§12 已按此分支）。

而 iOS 的"加载更多"按钮可见性本就是 **`hasMore` 驱动**（§6.3 L342）：在 cursor-capable server 上 `hasMore` 可为真→按钮显示；在 array-only server 上 `hasMore` 恒假→按钮自动不显示。**无需把 curl 预检结果编译期传入 iOS**。

**为什么值得说清（防返工）**：

- 防止实施者在 bridge 或 iOS 写 `if cursorVerified { fullBranch() } else { firstPageBranch() }`——这会引入两套代码路径，等用户 server 升级支持 cursor 后还得回来改。运行时自适应（双解码 + `hasMore` 驱动）能让"server 升级→加载更多自动可用"零代码改动。
- 顺带简化 §6.3 L349 与 §9.3 step 9 / §12 的 array-only 条款：它们不是"iOS 主动隐藏按钮"，而是"`hasMore` 自然为假所以按钮不出现"——是运行时结果，不是产品开关。

**实现提示（顺手）**：§5.2 L252 `GET /session?limit=<limit>&cursor=<cursor>` 建议补一句——cursor 为空时 `ocProxy` **省略** `cursor=` 参数（不要发空串），避免个别 server 对空 cursor 报错。

**建议的最小改写**（不强制）：在 §11.2 后加一句——

> Note: both paths share one runtime-adaptive bridge implementation (dual response decode in §5.2; cursor omitted when empty; `hasMore` derived from the upstream cursor). The decision gate determines validation scope and protocol/acceptance wording, not separate code branches. iOS load-more visibility is `hasMore`-driven, so no compile-time track flag is plumbed to the client.

---

## 3. 绿灯

三轮发现的问题已全部闭环：

- 防返工边界（§5.0.1）经代码核实，CLI fallback 论断证据充分。
- cursor 决策门前置，排序/方向验证覆盖两条分支。
- 可观测性解耦，首发不依赖新富诊断日志。
- handler limit/cursor 提取与 httptest 断言到位。
- §9.3/§12 按分支组织，自洽。

唯一遗留是 §2 的措辞澄清（可选）。做完即可进 exec-plan，第一步仍是 §11.1 的 curl 决策门。

**累计四轮的实施红线（不变，供 exec-plan 引用）**：

1. OpenCode 留 proxy 路径，不并入 generic（§5.0.1）。
2. 复用信封**形状**，不复用 generic handler / `paginateSessionList` / CLI fallback。
3. bridge 单一运行时自适应实现（双解码 + 空 cursor 省参 + `hasMore` 驱动），决策门只决定验证范围，不写编译期分支。
4. `cursor` 对 iOS 恒为 opaque、memory-only、不跨 backend/项目/进程复用。
5. `hasMore` 仅由上游真实 next cursor 决定，不做"恰好等于 limit"推断。
6. 真实失败要暴露，不加 mock/fallback（[CLAUDE.md](../../CLAUDE.md)）。
