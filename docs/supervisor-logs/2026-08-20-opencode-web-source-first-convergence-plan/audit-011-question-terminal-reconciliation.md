# 监工审计报告 11 号（对应监工指令 11 号）

> **审计时间**：2026-08-21T03:39:35Z  
> **审计严格度**：严格（源码追踪、A7 raw 复核、独立并发/全仓测试、runtime 只读核验）  
> **Verdict**：**partial**

## 0. 独立复验

- `go test ./go-bridge -run TestAudit011 -count=20 -timeout 5m`：PASS，103.7s。
- `go test -race ./agent/opencode-web -run 'TestAudit011|TestQuestion|TestAudit010' -count=20 -timeout 5m`：PASS，12.6s。
- `go test ./... -count=1 -timeout 10m`、`go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- `git diff --check`：PASS；Mac/iOS 仓审计开始时 clean。
- A7 raw 独立核对：pending GET/event 带 `que_…`、`messageID`、`callID`；resolved history tool part保留 `messageID`、`callID`、terminal state，但没有原 `que_…` interaction ID。

## 1. Overall Verdict

指令 11 的主体修复成立：四个 barrier 测试是真正受控交错；lost-terminal gap 能由 A7 terminal history 原位收口；旧 snapshot 不会清新 ask；resolve RPC owning test 确实穿过具体 adapter、server broadcast、Kernel 和权威 headRev。单 Kernel ingest owner没有回退。

仍有一个可实际触发的 lifecycle 漏口：`gateAdmitRequested` 在判断 lifecycle 已 terminal **之前**先把请求写回 `pendingQuestions`。迟到 `question.asked` 或陈旧 recovery row 虽不会重新投影 pending Dock，却会重新开放 reply mapping；后续 `ResolveUserInput` 会命中该 stale request 并再次 POST 官方 reply/reject 路由。这违反“resolution once”和指令 11 的 terminal precedence。现有测试只检查 Dock 状态，没有检查 terminal 后二次 resolve 的零 POST。

另外，开发 agent 发现 A7 history 无法恢复原 interaction ID 后没有按指令停止，而是自行选择 fresh-process resolved history 降为 tool activity。证据本身正确，但执行程序越过了明确停止线。设计 owner 本次依据 raw 证据正式收紧 canonical §6.8：pending fresh reload恢复 Dock；同进程 missed terminal 原位收口；fresh process resolved history只 hydrate tool activity并保证零 phantom Dock。若未来要求跨进程恢复原 structured resolved Dock，必须先批准持久 identity/protocol 设计，不能发明 ID。

因此本轮为 **partial**，不是 rejected：主状态机与证据路线正确，剩余产品缺口有界且无需新外部事实；按指令 12 号做一次 terminal reply-mapping 封口即可。

## 2. 已验证通过

| 项目 | 结论 | 证据 |
|---|---|---|
| lost terminal reconciliation | ✅ | answered/rejected gap 子测 20 轮通过；history exact shape fail-closed |
| source fence | ✅ | recovery/live terminal 与旧空 snapshot/new asked 均为 barrier 交错 |
| actual resolve RPC full path | ✅ | handler → concrete adapter → POST → broadcast → Kernel → headRev/status |
| terminal projection precedence | ✅ | terminal 不被迟到 requested 覆盖；一个 part 原位 settle |
| single ingest owner | ✅ | 未新增 reducer/writer；事件仍只经 session route |
| A7 fresh-process boundary | ✅ 设计收口 | raw history无 interaction ID；canonical 已明确禁止伪造 structured Dock |
| terminal reply mapping | ❌ | terminal 后 late asked/stale recovery 会重写 `pendingQuestions` |

## 3. 可证明的 reply-mapping 回流

`agent/opencode-web/questions.go` 的 `gateAdmitRequested` 当前顺序是：

1. 初始化并写入 `pendingQuestions[req.ID] = req`；
2. 再读取 `(sessionID, interactionID)` lifecycle；
3. 若 status 已非空（包括 answered/rejected），返回 false、跳过投影。

因此 `TestAudit011_LateAskedAfterResolvedDoesNotRearm` 只能证明 Projection 仍 answered，不能证明交互已不可再次提交。`lookupQuestion` 首先读取 `pendingQuestions`，会把这条迟到 ask 当成可回复请求；陈旧 recovery 走同一个 admission 函数，也存在同样问题。

修复必须让 lifecycle admission 与 reply mapping 使用同一个 terminal 判定：terminal/duplicate requested 应在写 mapping 前返回；resolved admission应持续清除 mapping。不得用 HTTP 404/409 作为正常二次提交兜底，更不得靠 server 去重冒充本地 lifecycle 正确。

## 4. 路线图与报告诚实性

- 报告正确更正了指令 10 的实现 hash 为 `4a2afb6`。
- 报告诚实披露 fresh-process identity 缺失，但违反了指令“无法表达即停”的程序门禁；本审计已由 design owner 用 A7 raw 将该事实写回 canonical，不追求猜测式 structured state。
- exec-plan 的 barrier/tests 可晋升为 re-verified；regression 因 reply mapping 未闭合降级并创建 directive-012 review-fix triplet。
- owner UI 矩阵仍未执行，不得标产品 done。

## 5. Partial 处理

- 监工指令 12 号只修 terminal reply-mapping 回流并补 owning negative tests。
- 不重开 history mapping、strict tail、variant UI、iOS、协议或能力；不再次修改 canonical。
- 修复后重跑 audit011 barrier/full-path、agent race、全仓 Go 与真实 question answer/reject sandbox，重装 Mac Release，然后停等 audit-012。
