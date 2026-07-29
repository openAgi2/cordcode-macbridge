# B2 config package removal 删除前审计点

日期：2026-07-03  
关联计划：`docs/2026-07-03-architecture-health-execution-plan.md`  
关联 exec-plan 状态：`.exec-plan/state/plan-dadda4ec2d90.json`

## 结论

本轮只完成 B2 删除前的小批次：解除 agent provider 测试对 legacy `config` 包的 test-only 依赖，并补充删除前证据。

`config/` 包仍未删除，`batch-b2-config-package-removal-impl` 仍应保持 `blocked`，直到删除前审计确认不会破坏迁移或调试流程，并由 owner 确认不再维护 `.cc-connect/config.toml` 的旧业务写入能力。

## 已完成

- 新增 `agent/providerseedtest/provider_seed.go`：测试专用 provider seed loader。
- 新增 `agent/providerseedtest/provider_seed_test.go`：覆盖 provider refs、agent type 过滤、agent-specific endpoint/model、Codex headers 与 `${ENV}` 展开。
- `agent/claudecode/provider_integration_test.go` 不再 import `github.com/openAgi2/cordcode-macbridge/config`。
- `agent/codex/provider_switch_test.go` 不再 import `github.com/openAgi2/cordcode-macbridge/config`。
- `go-bridge/provider_switch_test.go` 的静态防回归清单扩展到两个 agent provider 测试文件和 test helper。
- 未搬迁 Weixin/Feishu 或旧业务写入逻辑；test helper 只读取 provider seed。

## 复核命令与结果

已执行并通过：

```bash
go test ./agent/providerseedtest -count=1
go test ./go-bridge -run 'TestProviderSeedDoesNotImportLegacyConfigPackage|Test.*Provider|TestProviderSeed' -count=1
go test ./agent/claudecode ./agent/codex -run 'TestIntegration_ProviderSwitch_EnvVars|TestIntegration_AgentTypeChange_FiltersProviders|TestIntegration_Codex_ProviderSwitch_EnvVars|TestIntegration_Codex_ProviderConfig_WrittenCorrectly' -count=1
! rg -n 'github.com/openAgi2/cordcode-macbridge/config' agent/claudecode agent/codex agent/providerseedtest --glob '*.go'
go test ./go-bridge/... -count=1
CC_SKIP_INTEGRATION=1 go test ./... -count=1
```

全仓 Go import 扫描当前仍会命中 `go-bridge/provider_switch_test.go` 中的静态断言字符串，这是预期命中，不是 import：

```text
go-bridge/provider_switch_test.go: strings.Contains(..., "github.com/openAgi2/cordcode-macbridge/config")
```

## 删除前仍需审计

B2 删除本体开始前，后续 agent 应至少复核：

1. `rg -n 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' .`
   - 预期：除静态断言字符串外，不应有 import。
2. `rg -n 'Weixin|Feishu|EnsureProjectWithFeishu|EnsureProjectWithWeixin' --glob '*.go' .`
   - 预期：删除 `config/` 后生产 Go 不应残留旧业务写入符号。
3. `go test ./... -count=1`
   - 删除后必须跑全仓 Go 测试。
4. 确认不再需要维护 `.cc-connect/config.toml` 的旧业务写入能力。
   - 当前保留目标只是读取 provider seed。

## 当前 blocked 原因

`batch-b2-config-package-removal-impl` 仍 blocked，剩余 blocker 从“test-only 依赖未处理”缩小为：

- 删除 `config/` 本体前的独立审计尚未完成。
- owner 对旧业务写入能力不再维护的最终确认尚需在删除前明确记录。
- 删除后还必须补完整 `go test ./... -count=1` 与 Weixin/Feishu 残留扫描证据。
