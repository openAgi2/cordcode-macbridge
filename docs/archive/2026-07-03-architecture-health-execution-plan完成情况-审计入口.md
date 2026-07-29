# Architecture Health Execution Plan 完成情况与审计入口

日期：2026-07-03  
计划文档：`docs/2026-07-03-architecture-health-execution-plan.md`  
执行状态源：`.exec-plan/state/plan-dadda4ec2d90.json`  
Mac 分支：`codex/architecture-health-a-b1`  
iOS 分支：`codex/web-renderer-shared-c1`

## 结论

截至本报告写入时，exec-plan 队列里除 `batch-b2-config-package-removal-impl` 外，其余任务均已完成并记录了证据。

`batch-b2-config-package-removal-impl` 保持 `blocked` 是刻意状态：旧 `config/` 包删除必须先有“实际无人用”的证据，不能靠实现 agent 拍板删除。因此本报告不是“全量完成报告”，而是给后续 agent 做结果审计的状态报告。

## 当前队列状态

| 状态 | 数量 | 说明 |
| --- | ---: | --- |
| Done | 24 | 已完成，均有 verification 记录 |
| Blocked | 1 | 仅 `batch-b2-config-package-removal-impl` |
| Pending / In progress | 0 | 无 |

唯一 blocked：

| Todo | 原因 | 后续进入条件 |
| --- | --- | --- |
| `batch-b2-config-package-removal-impl` | 删除旧 `config/` 包需要 B1 结果审计和 owner 确认；当前要求保持 blocked | 先做出“旧 `config/` 包实际无人用”的证据，再决定是否删除 |

## 已完成范围

### Batch A：capability 单源化

完成内容：

- 新增 `go-bridge/backend_capabilities.go`，让 BackendList 和 BuildAgentDescriptor 共用同一能力推导源。
- 保持 capability 顺序、Codex app_server 的 `compression` / `question_reply`、`permission_resolve` 和 `session_pagination` 语义。

证据：

- `go-bridge/backend_capabilities.go`
- `go-bridge/handlers.go`
- `go-bridge/agent_descriptor.go`
- `go test ./go-bridge -run 'Test.*Capabilit|TestBackendList|TestBuildAgentDescriptor' -count=1`
- `go test ./go-bridge/... -count=1`

### Batch B1：provider seed config 解耦

完成内容：

- 新增 `go-bridge/provider_seed_config.go`，用最小 TOML seed model 取代运行路径对旧 `config` 包的依赖。
- `provider_switch.go` 改为调用 `loadProviderSeedConfig`。
- 生产 Go 文件静态检查确认不再 import `github.com/openAgi2/cordcode-macbridge/config`。

证据：

- `go-bridge/provider_seed_config.go`
- `go-bridge/provider_switch.go`
- `go-bridge/provider_switch_test.go`
- `go test ./go-bridge -run 'Test.*Provider|TestProviderSeed' -count=1`
- `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' --glob '!*_test.go' .`
- `./scripts/build-unsigned-release.sh`
- Release app 覆盖安装到 `/Applications/CordCodeLink.app` 并验证 `8777` listener。

### Batch C：iOS message-web / remote-web 共享 renderer 包化

完成内容：

- 新增 `../cordcode-ios/shared-message-renderer/`。
- C1：建立 `tsconfig paths` + Vite `resolve.alias` 源码级共享，不使用 `file:`、`npm link`、根 workspace、publish 或拷贝到 `node_modules`。
- C2：迁移 `DiffViewer` 到共享包，两端保留 thin host adapter。
- C3：迁移 `ToolBlock` 到共享包，统一通过 `host.post(...)` 发出 `openDetail`、`permissionAction`、`questionAction`。
- `NarrativeBlock` git directive UI 和 `ReasoningBlock` labels 未迁移，符合本轮计划边界。

关键文件：

- `../cordcode-ios/shared-message-renderer/src/components/blocks/DiffViewer.tsx`
- `../cordcode-ios/shared-message-renderer/src/components/blocks/ToolBlock.tsx`
- `../cordcode-ios/shared-message-renderer/src/host.ts`
- `../cordcode-ios/message-web/src/components/blocks/DiffViewer.tsx`
- `../cordcode-ios/message-web/src/components/blocks/ToolBlock.tsx`
- `../cordcode-ios/remote-web/src/renderer/components/blocks/DiffViewer.tsx`
- `../cordcode-ios/remote-web/src/renderer/components/blocks/ToolBlock.tsx`

证据：

- `(cd ../cordcode-ios/shared-message-renderer && npm run typecheck && npm run test)`
- `(cd ../cordcode-ios/message-web && npm run test:vitest -- DiffViewer)`
- `(cd ../cordcode-ios/message-web && npm run test:vitest -- ToolBlock)`
- `(cd ../cordcode-ios/remote-web && npm run test:vitest -- DiffViewer)`
- `(cd ../cordcode-ios/remote-web && npm run test:vitest -- ToolBlock)`
- `(cd ../cordcode-ios/message-web && npm run test:vitest -- src/App.test.tsx -t 'permission|tool blocks|DiffViewer')`
- `(cd ../cordcode-ios/message-web && npm run typecheck && npm run build)`
- `(cd ../cordcode-ios/remote-web && npm run typecheck && npm run build)`
- `(cd ../cordcode-ios/message-web && npm run build:ios)`
- `(cd ../cordcode-ios && xcodebuild -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' build)`
- `(cd ../cordcode-ios && scripts/run.sh device --device BFC431AC-C205-56B2-BB4D-9EC0C57A0C05)`
- `! rg -n -g '!**/node_modules/**' 'postToNative|bridge/native|renderer/bridge|relay/|OpenCodeI?OS' shared-message-renderer/src`

### Batch D：god-object characterization tests

完成内容：

- 只增加 characterization tests，不拆分 god object。
- 覆盖 `BridgeProvider.selectConnectionStrategy(...)` 的策略矩阵。
- 覆盖 `ChatViewModel` generation cycle 的 settled boundary。

证据：

- `../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/GodObjectCharacterizationTests.swift`
- `../cordcode-ios/OpenCodeiOS/CordCode.xcodeproj/project.pbxproj`
- `(cd ../cordcode-ios && xcodebuild -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test -only-testing:CCCodeTests/GodObjectCharacterizationTests)`
- 结果：Executed 2 tests, 0 failures。

未做事项：

- 没有拆分 `BridgeProvider` 或 `ChatViewModel`。
- 没有运行 UI tests、snapshot tests 或 simulator UI automation。

### Batch E：工程宪法与非阻塞 hygiene

完成内容：

- 新增 `docs/engineering-constitution.md`。
- 新增 warning-only `scripts/check-architecture-hygiene.sh`。
- 脚本只做 inventory / warning，不改变运行路径，不接入 required CI gate。

证据：

- `bash -n scripts/check-architecture-hygiene.sh`
- `scripts/check-architecture-hygiene.sh`
- `git diff --check`

## 审计建议

后续 agent 可以按以下顺序审计：

1. 读取 `.exec-plan/state/plan-dadda4ec2d90.json`，确认 blocked 只剩 `batch-b2-config-package-removal-impl`。
2. 对 Mac repo 复跑：
   - `go test ./go-bridge/... -count=1`
   - `./scripts/build-unsigned-release.sh`
   - `git diff --check`
3. 对 iOS repo 复跑：
   - `(cd ../cordcode-ios/shared-message-renderer && npm run typecheck && npm run test)`
   - `(cd ../cordcode-ios/message-web && npm run typecheck && npm run build && npm run build:ios)`
   - `(cd ../cordcode-ios/remote-web && npm run typecheck && npm run build)`
   - `(cd ../cordcode-ios && xcodebuild -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' build)`
   - `(cd ../cordcode-ios && xcodebuild -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test -only-testing:CCCodeTests/GodObjectCharacterizationTests)`
   - `git -C ../cordcode-ios diff --check`
4. 如需审 B2，不应直接删除旧 `config/`。先做静态和构建证据，证明旧包没有生产、测试、脚本或文档约束仍依赖它，再决定是否进入删除。

## 已知注意点

- `remote-web` build 中 Vite 对 keystore dynamic/static import 的 warning 仍存在；本轮未改该路径。
- iOS 真机安装使用显式 device id：`BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`。
- 本报告由同一执行 agent 生成；`exec-plan` 中 `re-verified` 表示本轮实际复跑过命令，`self-attested` 表示实现/结构性证据由执行 agent 记录，仍应由审计 agent 独立复核。
