# 监工审计报告 10 号（对应监工指令 10 号）

> **审计时间**：2026-08-21T02:42:00Z  
> **审计严格度**：严格（源码追踪、独立测试、真实隔离 OpenCode 1.18.18、runtime/device 只读核验）  
> **Verdict**：**partial**

## 0. 独立复验

- `go test ./agent/opencode-web ./go-bridge -run TestAudit010 -count=1 -timeout 5m`：PASS。
- `go test -race ./agent/opencode-web -count=1 -timeout 5m`：PASS，5.332s。
- `go test ./... -count=1 -timeout 10m`：第二次全 PASS；第一次仅 `agent/opencode/TestAvailableModels_BackgroundRefreshUpdatesDiskCache` 在 `t.TempDir` 清理时碰到后台写入，随后该用例 `-count=20` 与全仓复跑均 PASS，作为既有 cleanup flaky 记录，不归因于本批 opencode-web。
- `go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- iOS 定向 `ModelVariantSelectionTests` 7/7 + `OpencodeWebVariantAgentWireTests` 4/4：PASS，未执行 UI tests；有 300s 进程组 watchdog。
- 独立隔离 1.18.18 serve：`TestSandboxC6C7/(question|question-reload|mutations)` 全 PASS；4096 PID 前后不变；4398/4399 已回收。
- `/Applications/CordCodeLink.app` 内 runtime 正在监听 8777；iPhone 当前可连接，`org.openagi.cordcode` 可由 `devicectl device info apps` 查到已安装。
- 双仓在审计前后 clean。

## 1. Overall Verdict

指令 10 的大部分尾项成立：messageID/callID live attribution、pending cold reload、exact-200 mutation、archive timestamp、provider/todo strictness、Mac/iOS 安装与 `/agent.description` 证据门控均通过独立核验。`description` 偏差应接受：同版本官方 schema 明确 optional，真实 serve 的 hidden internal agents 确实省略该字段，强制所有行存在会破坏真实路径。

但 Question recovery 仍是“add-only pending restore”，没有闭合“resolution 落在 SSE 断线/GET 并发窗口”的状态机。报告称 GET/live race、reply/reject/external/reload 已 full-path 完成，实际测试与实现没有覆盖 authoritative resolution 与 recovery 的并发顺序；这会留下永久 pending Dock，直接违反 canonical §6.8 和 §8 场景 6 的 first-server-resolution-wins/converge 判据。范围有界，按 partial + hole-fill 处理。

## 2. 已验证通过

| 项目 | 结论 | 证据 |
|---|---|---|
| live source identity | ✅ | `provenQuestionTurn` 要求 messageID/callID、same-session assistant、parentID 与 armed turn 一致；destructive tests PASS |
| pending cold reload | ✅ | fresh adapter 在真实 1.18.18 serve 通过 GET `/question` + history parentID 恢复同一 pending interaction |
| strict mutation tail | ✅ | rename/archive/delete exact 200；archive echoed timestamp 等值；真实 mutation 回归 PASS |
| provider/todo strictness | ✅ | provider 三个顶层键 presence/type；Todo exact 3-key row；destructive tests PASS |
| agent description exception | ✅ 接受 | official `agent.ts` schema 为 optional；真实 hidden compaction/summary/title 缺 description；non-hidden 仍 fail closed |
| iOS regression/install | ✅ 技术轨 | 11/11 定向测试；设备上可查到 CordCode 已安装；未做 owner UI 验收 |

## 3. 未闭环的 Question resolution/recovery

### 3.1 Recovery 只增加 pending，不对账已解决项

`questions.go:124-169` 对 GET `/question` 的每个 pending row执行 note/claim/requested emit，但没有处理“本地已投影 pending、服务器列表已无此 ID”的集合差。A7 同版本样本已经证明：reply/reject 后 GET `/question` 变成空数组；reload history 同时保留 question tool 的 terminal state（answer 为 completed + metadata.answers，reject 为 error）。

因此当 `question.replied/rejected` 在 SSE gap 中丢失时：

1. Kernel 中已有 pending part；
2. 重连 GET 返回空；
3. 当前 recovery 什么也不发；
4. pending Dock 永久残留。

这不是可由 `claimQuestionProjection` 解决的重复问题，而是缺少 terminal reconciliation。

### 3.2 claim 与 resolution 存在可证明的顺序反转

`noteQuestionAsked`、`claimQuestionProjection`、`sub.emit(requested)` 分属三个步骤；`questionResolved` 可以在 claim 之后、requested emit 之前清除 claim 并发出 resolved。Reducer 明确丢弃“没有 requested part 的 resolved”。随后迟到的 requested 再创建 pending part，最终状态错误。

同样，若 GET snapshot 取到空列表后，新 live asked 才到达，任何简单“absence means resolved”的修补又会误清新的 pending。收口必须有真实 source cut/version fence 或进入同一串行归约顺序，不能再叠加布尔 claim/时间窗口。

### 3.3 所谓 race/full-path 测试覆盖面过报

- `agent/opencode-web/audit010_question_test.go:269-305` 明确先等同步 recovery 返回，再调用 live handler。
- `go-bridge/audit010_fullpath_test.go:223-242` 同样在 StartSession recovery 完成后才 push live asked。
- full-path reply/reject 测试只 push server broadcast；实际 iOS `resolve_user_input` → real opencode-web responder → official POST → server event → Kernel/headRev 的一条链未在同一个 owning test 中出现。
- 没有“pending 已投影 → SSE drop → server resolves during gap → reconnect GET empty/history terminal → same part resolved”的测试。

所以测试名中的 race/full-path 不能支撑报告的覆盖范围。

## 4. 报告与状态准确性

- 完成报告写实现 commit `1e9a714`，该对象不存在；实际实现 commit 是 `4a2afb6`。这是报告事实错误，需在下一份报告修正。
- exec-plan 三元组为 self-attested done；本审计将其 regression 降级并新建 review-fix triplet。
- owner UI 矩阵仍未执行，不能标产品 done。

## 5. 路线图偏离检查

| # | 判据 | 命中 | 说明 |
|---|---|---|---|
| 1 删除性测试 | 否 | 没有恢复已删除 referee |
| 2 三环归属 | 否 | 修复属于 C6 observation/recovery → Kernel |
| 3 待删特征 | 是（软） | bool claim 是局部裁判，不能表达 pending/resolved/source order |
| 4 半接管 | 否 | 单 Kernel writer 保持 |
| 5 覆盖面诚实 | 是（软） | 顺序测试被表述为 race，broadcast 被表述为完整 reply/reject 链 |
| 6 叙事真实性 | 否 | 三轨声明仍明确 |
| 7 控制变量污染 | 否 | 双仓 clean，范围集中 |
| 8 根因门 | 否 | 缺口可由现有 A7 样本与源码确定 |

## 6. Partial 处理

- 路径 A：监工指令 11 号 narrow hole-fill。
- 只收口 Question resolution/recovery lifecycle、真正并发的 source fence、actual resolve RPC full path 和报告 hash；不重开 strict tail、variant UI、iOS 安装。
- exec-plan proven：partial；监工 verified：partial；owner 真机矩阵：pending。
