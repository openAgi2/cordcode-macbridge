# 监工指令 2 号：补洞 C1 unsupported-generation diagnostics

> **Kind**：hole-fill  
> **对应审计**：`audit-001-gate-s-exit-and-c1.md`（verdict=partial）  
> **停止线**：只补 C1，不进入 C2。

## 1. 已锁死的问题

隔离 v2 serve 的最小复现得到：

```text
overall=passed
ocw_probe status=passed message=generation=v2 ...
```

`RunDiagnostics` 在 `probeInstance` 成功识别 v2 后仍把 generation 标成 passed，并继续 fetch catalog。它违反：

- convergence plan §6 C1：unsupported version 必须与 supported/unauthorized/unreachable 区分；
- S3 C1 `plannedAdd`：`unsupported generation status in diagnostics`；
- C1 quarantine：unverified generation 不得继续进入正常 adapter/catalog 路径。

## 2. 实施范围

1. 修正 `agent/opencode-web/diagnostics.go`：
   - `probe.err != nil` 继续保留现有失败分类；
   - `probe.gen != generation118` 必须输出稳定、可诊断的 `unsupported-generation (quarantined)`；
   - `ocw_probe` 不得标 `passed`；`OverallStatus` 必须是失败态；
   - 一旦识别 unsupported generation，必须停止，不得继续 `/provider` catalog/model 检查；
   - 不发送 POST、不订阅 SSE、不创建 session、不触碰 Kernel。
2. 尽量复用 `InstanceStatus/clientFor` 的 generation 判定语义，避免 diagnostics 与产品闸口再次漂移；不得引入 legacy fallback 或 recursive parser。
3. 修正 C1 测试/exec-plan 报告中的过度口径：
   - 可以证明的是 v2 没有新增或可用的 v2-specific capability，且 backend status/reason 不可用并被 quarantine；
   - 当前 descriptor capability 数组并非空，不得继续声称“capability 数组为零”；
   - 本指令不授权修改 WireDescriptor、capability advertisement 或协议。若认为必须改变现有 capability 产品语义，立即停止并把证据交 owner，不得自行改。

## 3. 必需测试

至少新增/补强以下 owning tests：

1. v2 diagnostics：
   - `OverallStatus == failed`；
   - `ocw_probe.status == failed`；
   - message 含 `unsupported-generation` 与 `quarantined`；
   - v2 被识别后 `/provider` 请求数为 0；
   - POST、`/global/event`、`/api/event` 请求数均为 0。
2. 1.18.18 diagnostics 仍为 passed。
3. unknown/unauthorized/server_unauthenticated 仍走 probe failure，不得误归类成 supported 或 v2 quarantine。
4. descriptor 口径回归：v2 backend status 必须非 available，reason 点名 quarantine；不要断言 capability 数组为空。

## 4. 回归与交付

运行并报告真实输出：

```bash
go test ./agent/opencode-web/ -count=1 -timeout 180s
go test ./go-bridge/ -run 'OpenCodeWeb|AgentDescriptor' -count=1 -timeout 180s
go test ./core/ -count=1 -timeout 180s
go vet ./agent/opencode-web/
go build ./...
```

要求：

- 单独 hole-fill commit；
- 最终工作树干净；
- 不修改 protocol、WireDescriptor、capability advertisement、iOS writer；
- 不改 C2–C7；
- 报告 commit、diff、测试输出、v2 diagnostics 实际结果和请求计数；
- 完成后停止，等待监工指令 2 号独立审计。

