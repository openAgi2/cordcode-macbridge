# 监工指令 12 号：Question terminal reply-mapping 最终封口

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md` §6.8、§8 场景 6  
> **依据**：audit-011 partial  
> **范围**：MacBridge opencode-web + owning tests；iOS 不变  
> **kind**：hole-fill

## 目标

保留 11 号已经独立验证的 barrier/source-fence/history reconciliation/resolve full-path。只封住 terminal 后迟到 asked 或陈旧 recovery 重新写入 reply mapping 的漏洞；完成后一次集中报告并停止等待 audit-012。

## 1. 红灯先行

在修改产品代码前扩展 owning tests，至少证明当前实现以下两条为红：

1. `question.replied/rejected` 已令 part terminal 后，重复/迟到同一 `question.asked`；再次走真实 `resolve_user_input` handler时必须返回 interaction-not-found/already-resolved 的权威不可提交结果，且官方 reply/reject **零新增 POST**，Projection仍是同一 terminal part。
2. recovery GET 取得 stale pending row、live terminal先落、释放 barrier后；同样再次 resolve必须零新增 POST。不得只检查 Dock status。

answer/reject 两种 terminal 至少各覆盖一次；测试穿过具体 opencode-web responder。禁止用顺序单元函数调用冒充第 2 条 barrier。

## 2. 实现边界

- `gateAdmitRequested` 必须先在同一 lifecycle gate内判定 terminal/duplicate，再决定是否写 `pendingQuestions`；terminal 或 stale requested不得恢复 reply mapping。
- `gateAdmitResolved` 继续清除 reply mapping；server terminal仍是唯一 Projection terminal owner。
- `lookupQuestion` 不得绕过 lifecycle terminal状态；如果保留两张表，必须证明两者在同一锁/同一 admission次序下一致。优先消除双真相，不增加第三个 referee、sleep、fallback或 server-404 正常化。
- 不改 A7 mapping、canonical、protocol、WireDescriptor、capability、Kernel/reducer或 iOS；不进入 owner UI 矩阵。

## 3. 回归

- `go test ./go-bridge -run TestAudit01[12] -count=20 -timeout 5m`。
- `go test -race ./agent/opencode-web -run 'TestAudit01[012]|TestQuestion' -count=20 -timeout 5m`。
- `go test ./... -count=1 -timeout 10m`、`go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`。
- 真实隔离 1.18.18 重跑 question、pending reload、answer/reject reopen；4398/4399 回收，4096 owner-managed。
- Mac 产品代码变化后 Release重建/安装并只读确认 8777 来自 `/Applications`；iOS不变，不重复测试/安装。

## 4. 报告与停止

报告逐项列出：两个红灯 before/after、terminal 后二次 resolve结果与 POST计数、20轮/race/full/sandbox输出、runtime/端口/clean状态。不得把 exec-plan proven写成 supervisor verified。提交报告后停止等待 audit-012。
