# codex-remote 懒加载历史计划开工指令

将以下内容作为实施 agent 的完整开工指令执行。

---

你现在开始执行：

`docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`

这是一个 drive-to-completion 的 exec-plan 任务。除真实外部阻塞外，不要在阶段之间停下来询问是否继续；
每个 gate 通过后继续推进下一阶段，直到全部 required todo 有 proof，或按 exec-plan 规则记录明确 blocker。

## 1. 固定工作目录与分支

继续使用现有两个 worktree，**不要新建 worktree、不要新建或切换分支、不要合并 main、不要 push**：

```text
Mac / Bridge:
  root   = /Users/jacklee/Projects/cordcode-macbridge-codex-remote
  branch = codex/codex-remote-backend

iOS:
  root   = /Users/jacklee/Projects/cordcode-ios-codex-remote
  branch = codex/codex-remote-backend-ios
```

开工第一步在两个实际目录分别执行只读 P0 preflight，确认 repo root、branch、HEAD、worktree status。
任何分支/目录不符都先停止实施并报告，不能在错误 worktree 上继续。

Mac worktree 当前预期存在以下文档改动，它们是本计划的正式开工基线，不得丢弃或覆盖：

```text
M  docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md
?? docs/2026-08-30-codex-remote-lazy-history-plan-final-audit-r6.md
?? docs/2026-08-30-codex-remote-lazy-history-kickoff-directive.md
```

先复核这些 diff 与计划终版一致；若一致，将三份文档作为一个 docs-only baseline commit 提交，再开始
代码实施，使后续 proof 和变更边界可追踪。不得清理任何其他用户改动；若实际 status 多出无关修改，
保留并绕开。

## 2. 与主计划的关系

母计划是：

`docs/2026-08-26-codex-remote-backend-implementation-plan.md`

母计划已经交付 codex-remote 的基础设施：Remote controller/WSS、identity、catalog/history 基线、SSV2、
live 双向同步、session mutation、目录/标题、重连与 CPU 修复。其 exec-plan 状态当前为：

```text
state  = .exec-plan/state/plan-bb4683ae3ec1.json
queue  = done 111 / proven 111 / required 108 / re-verified 31
report = docs/2026-08-28-2026-08-26-codex-remote-backend-implementation-plan完成情况.md
status = current
```

本懒加载计划是母计划交付后的**后续性能/体验增强子计划**：

- 复用母计划已经 proven 的 transport、live、projection、window、mutation 基础；
- 不重新实现或重新证明整个 codex-remote backend；
- 但本计划新增的 Summary 首屏、items 按需拉取、upstream→window 接线、`turnStateOps`、多客户端
  delivery、真机性能回归，必须在本计划自己的任务中独立取证；
- 母计划是已完成依赖和证据来源，不是本次需要重新打开的执行队列；
- 若实施发现母计划已有行为发生真实回归，在本计划队列中创建明确的 review-fix triplet，并在 proof
  中引用母计划 todo/完成报告；不要静默改写母计划历史状态。

## 3. Exec-plan state：必须独立，不与母计划共用

**禁止复用或修改**母计划 JSON：

`.exec-plan/state/plan-bb4683ae3ec1.json`

原因：exec-plan state 按 plan 相对路径的 SHA-1 前 12 位唯一绑定；母计划队列已经 111/111 proven 且完成
报告 current。把新任务塞回该 JSON 会使已封账队列与完成报告失真，也违反“一份 plan 路径对应一份
canonical state”的规则。

本计划应创建并只写下面的独立 canonical state：

```text
Plan relative path:
  docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md

Path hash:
  6e34cf39c628

Canonical state:
  .exec-plan/state/plan-6e34cf39c628.json

Legacy-compatible path:
  .claude/exec-plans/plan-6e34cf39c628.json
  （当前不存在；只作为兼容读取位置，不双写）
```

虽然计划跨 Mac 与 iOS 两个 worktree，**只创建这一份 state**，位置固定在 Mac 项目根目录。该 state
同时承载 Mac、go-bridge、协议文档和 iOS todo；iOS 的文件、测试日志和 commit hash作为相应 todo 的
proof artifacts。不要为 iOS 再建立第二份 exec-plan JSON，否则跨仓 gate 会分裂、无法形成一个完成判定。

每个新 todo 的 `source` 或 `notes` 写入关系标记：

```text
parent-plan:docs/2026-08-26-codex-remote-backend-implementation-plan.md
parent-state:.exec-plan/state/plan-bb4683ae3ec1.json
parent-status:proved-complete
```

涉及官方 Codex 复用/对齐的 impl/tests/regression todo，`verification.upstream_anchor` 必须填写计划 §1.5
给出的 `/Users/jacklee/Projects/codex` tag 源码 `file:line`；缺少 upstream anchor 不得标 done。

## 4. 启动命令与队列纪律

在 Mac 项目根目录执行：

```text
/exec-plan docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md
/exec-plan docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md start
```

第一条只做 sync：创建独立 state、解析 Phase 0–4 和 G0–G4、生成 proof-carrying triplets；检查生成的
canonical path 必须是 `.exec-plan/state/plan-6e34cf39c628.json`。第二条开始 drive ready queue。

队列拆分必须遵守：

- 每个具体交付物拆成 `impl / tests / regression` triplet；
- `tests → impl`，`regression → tests`；下一阶段 impl 默认依赖上一阶段 regression；
- G0 live fixture/性能基线/owner 裁决是 Phase 1 的硬前置；G0 未通过不得写生产 parser/mapper；
- `turnStateOps` canonical protocol 先于 handler；协议未冻结不得各端自行猜 shape；
- UI/性能/真机工作必须保留 required regression todo，不能标 N/A；
- legacy 只有在 target inventory 证明不存在时，相关格才允许
  `N/A(no target legacy thread)`，并附 inventory artifact；
- 每次状态变化立即持久化 JSON，proof 写入后才能标 done；
- tests 尽量在 audit/exit audit 重新执行并标 `re-verified`；手工真机证据诚实标 `self-attested`；
- 不在本计划未全部 required todo proven 前生成正向完成报告。

## 5. 实施原则与 gate

从 Phase 0 / G0 开始，严格按计划顺序执行。最重要的约束：

1. **官方源码优先**：目标行为基线是 `/Users/jacklee/Projects/codex` tag
   `rust-v0.150.0-alpha.12.2`，不是移动的 main。`ReadTurnItems` 逐项镜像
   `paginated_turn_full_items` 的六项不变量，不重新发明分页结束条件。
2. **真实 fixture 先行**：target `thread/turns/list`、`thread/items/list`、`thread/read` 的 shape、identity、
   cursor、Reasoning/CommandExecution 内容必须由 G0 脱敏 dump 支撑；源码证明能力，不替代目标 live
   fixture。
3. **`initialTurnsPage` 正确请求**：候选必须是
   `thread/resume(excludeTurns=true, initialTurnsPage={limit:30, sortDirection:desc, itemsView:summary})`，
   并断言 `thread.turns == []`；非空立即判候选失败，不能把 full history 字节排除在性能统计外。
4. **禁止假成功**：不加自动 full fallback、mock、placeholder、缓存快照或静默 unknown-item 丢弃。
5. **live 零回归**：不改共享 `FlushPatch`/`ps.tools` 和 codec/ws/pairing/backoff 语义；每阶段回归保留
   Mac↔iOS 双向发送、接力与重连验证。
6. **单一投影真相源**：`session_turn_items` result 只做 ack；canonical items 只经 projection
   snapshot/patch 下发。
7. **资源和并发 fail-closed**：maxPages/maxBytes/timeout 由 G0 数据裁决；unknown item 整回合原子失败；
   generation fence、多连接 revision、orphan loading、state-only change-set 全部按计划测试。

## 6. 测试、安装与成本边界

- 默认使用代码阅读、静态检查、定向 Go/Swift 单测、定向 build；未经 owner 明确允许，不主动运行 UI
  tests、snapshot tests、simulator automation 或其他高消耗视觉流程。
- 真机验收按计划 G3/G4 执行。若修改 iOS 代码且 iPhone 已连接 Mac，每批 iOS 修改验证完成后自动安装
  到连接的 iPhone；安装失败必须记录真实 blocker，不能用模拟器结果冒充真机。
- 修改 `agent/` / `go-bridge/` 后按仓库交付门执行定向测试、unsigned Release 构建、覆盖安装与启动验证。
- 配对码只进入 localhost 表单；live fixture 必须脱敏并通过 secret scan，不在聊天或提交中泄露凭据。

## 7. 完成与回写关系

本子计划完成时：

- 只由 `.exec-plan/state/plan-6e34cf39c628.json` 判定完成；
- 生成本计划自己的 completion report，不覆盖母计划完成报告；
- completion report 中列出母计划为 proved-complete dependency，并分别列出 Mac/iOS commit 与证据；
- 不把新 todo 追写进母计划 JSON，不把母计划完成报告改成“再次完成”；
- 若 living docs 需要反映懒加载最终行为，使用本计划的 doc-sync todo 更新，并在母计划文档中只添加
  一条指向本子计划及其完成报告的后续增强链接，不改写母计划已完成队列历史。

现在先完成 P0 preflight 与 docs-only baseline commit，然后执行两条 `/exec-plan` 命令并从 G0 开始。

---

