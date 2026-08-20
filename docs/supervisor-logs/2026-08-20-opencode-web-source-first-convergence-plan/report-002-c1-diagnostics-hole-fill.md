# 开发 Agent 报告 2 号：C1 diagnostics 补洞

> **报告来源**：owner 于 2026-08-20 在对话中粘贴的开发 agent 完成报告。

## 提交

- `5b8451c`：修复 unsupported-generation diagnostics，工作树干净。

## 实现主张

- `RunDiagnostics` 在 `probe.gen != generation118` 时将 `ocw_probe` 标为 failed，`OverallStatus=failed` 并立即停止。
- unsupported-generation 文案由 `unsupportedGenerationDetail` 在 `InstanceStatus`、`clientFor`、diagnostics 三处共享。
- v2 判定后不进入 `/provider`、不 POST、不订阅 SSE、不创建 session、不触碰 Kernel。
- exec-plan 口径改为“backend 不可用 + quarantine reason + 无新增 v2 capability”；不再声称 descriptor capability 数组为空。
- 未修改 WireDescriptor、capability advertisement、协议或 iOS。

## 测试主张

- `TestV2DiagnosticsQuarantinedStopsAtProbe`：diagnostics failed，消息含 unsupported-generation/quarantined，catalog/POST/SSE 请求均为零。
- `TestDiagnostics118StillPassed`：1.18.18 仍通过。
- `TestDiagnosticsUnknownAndUnauthenticatedStayProbeFailures`：unknown 与 server_unauthenticated 不误归入 v2 quarantine。
- `TestOpenCodeWebV2QuarantineDescriptorNotAvailable`：descriptor 非 available，reason 点名 quarantine，不断言 capability 数组为空。
- `go test ./agent/opencode-web/ -count=1 -timeout 180s`：PASS。
- `go test ./go-bridge/ -run 'OpenCodeWeb|AgentDescriptor' -count=1 -timeout 180s`：PASS。
- `go test ./core/ -count=1 -timeout 180s`：PASS。
- `go vet ./agent/opencode-web/`、`go build ./...`：PASS。

## 停止线声明

- diff 仅 diagnostics、共享措辞、定向测试、go-bridge descriptor test 和 exec-plan state。
- 未进入 C2；未修改 protocol、WireDescriptor、capability advertisement 或 iOS writer。
- 等待指令 2 号独立审计。

## Recheck 补洞报告（2026-08-20）

- commit：`257f0df`，6 文件，`+15/-15`，工作树干净。
- `clientFor` 已改用 `unsupportedGenerationDetail(res.gen, res.detail)`；删除独立硬编码与旧 `no capability` 表述。
- 14 处测试断言统一升级为 `unsupported-generation (quarantined)`，保留 v2 fail-closed、零 prompt/SSE/Kernel、descriptor unavailable 和 unknown/no-auth 非 quarantine 的边界。
- 开发侧复跑：agent package、focused C1 tests、go-bridge descriptor、core、vet、build 全部通过。
- 未修改 protocol、WireDescriptor、capability advertisement 或 iOS；未进入 C2；等待 `audit-002-recheck`。
