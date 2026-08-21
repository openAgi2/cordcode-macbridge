# 监工指令 13 号：Question lookup terminal gate 最终封口

> **路线图**：canonical §6.8、§8 场景 6  
> **依据**：audit-012 partial  
> **范围**：MacBridge opencode-web + owning test；iOS不变  
> **kind**：hole-fill

## 目标

保留 12 号已验证的内存 mapping封口，只消除 `lookupQuestion` 在 lifecycle拒绝 row后仍无条件返回该 row的旁路。完成后集中报告并停止等待 audit-013。

## 1. 红灯

产品代码修改前新增并在旧实现确认红：

1. pending已投影并被 server terminal解决；mapping已清除；当前 `GET /question` **仍返回同一 stale row**。
2. 穿过真实 `resolve_user_input` handler → concrete opencode-web responder尝试 answer/reject。
3. 期望：权威不可提交错误（`interaction_not_found` 或明确 `already_resolved`）、官方 reply/reject零 POST、Projection保持同一 terminal part。

至少 answered+answer 与 rejected+reject各一子用例。测试期间不得把 `/question` 改空，不得让 server POST返回404/409掩盖 adapter旁路。

## 2. 实现

- lifecycle gate必须把“是否允许这个 GET row成为 reply shape”的结果返回给 `lookupQuestion`；被 terminal拒绝的 row不能写 mapping，也不能作为函数成功值返回。
- 若 GET包含目标 ID但 lifecycle已 terminal，应继续扫描并最终返回权威不可提交错误；不得 POST。
- pending cold restart仍必须允许尚无 lifecycle的真实 pending row成为 reply shape；不得破坏 directive-010 pending reload。
- 保持同一 `questionMu` admission次序；不得新增第三表/referee、sleep、fallback、协议字段、Kernel writer。

## 3. 回归与停止

- `go test ./go-bridge -run 'TestAudit01[123]' -count=20 -timeout 6m`。
- `go test -race ./agent/opencode-web -run 'TestAudit01[0123]|TestQuestion' -count=20 -timeout 5m`。
- `go test ./... -count=1 -timeout 10m`、vet、build。
- 真实隔离 1.18.18重跑 question、pending reload、answer/reject reopen；回收4398/4399、保持4096 owner-managed。
- Mac产品代码变化后Release重装并确认8777来自 `/Applications`；iOS不变。
- 报告必须引用含方括号正则的命令，保证默认 zsh可直接复现。提交报告后停止等待 audit-013，不进入 owner UI矩阵。
