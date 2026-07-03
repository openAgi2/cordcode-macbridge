# OpenCode managed local server seamless plan 完成情况

日期：2026-07-03

## 结论

已完成 plan `docs/2026-07-03-opencode-managed-local-server-seamless-plan.md` 的实现、定向测试、Release 构建、覆盖安装和本机运行态验收。

CordCode Link 现在默认使用 `managed_local` OpenCode source。Mac app 在启动 go-bridge 前托管本机 `opencode serve`，通过健康检查确认 Basic Auth 可用后，把真实 endpoint 注入 runtime，并同步 OpenCode Desktop 到同一 managed URL。

## 主要改动

- 新增 `OpenCodeManagedServer` Swift supervisor：CLI 发现、端口选择、env-only password 注入、0600 state、日志滚动/脱敏、health-gated readiness、持久 pid adoption、未知端口避让。
- 最终审阅补强端口选择边界：健康检查通过但 pid 不是持久记录的可收养 `opencode serve` 时，不复用该端口，避免在已占用端口上误启动。
- `RuntimeManager` 在启动 bridge 前解析 managed OpenCode endpoint，并在退出时停止自己托管的 child process。
- OpenCode source 新增 `managed_local`，fresh install 默认使用 Automatic / managed local；旧 external/legacy 配置继续保留迁移路径。
- Settings UI 增加 `Automatic (Recommended)`。
- go-bridge 增加 OpenCode managed URL project scope 相关回归测试。
- 更新 `BUILD_INSTALL_AND_RUNTIME.md`、`GO_BRIDGE_ARCHITECTURE.md`、`docs/backends-and-config.md` 和 `CHANGELOG.md`。

## 验证证据

- Swift 定向测试通过：
  - `OpenCodeManagedServerTests`
  - `OpenCodeEndpointResolverTests`
  - `MacBridgeBehaviorTests`
- Go 定向测试通过：
  - `go test ./go-bridge -run 'OpenCodeListProjects|OpenCodeListSessions|OpenCodeListDirectory' -count=1`
- CI 合并前修复并验证 Bridge shutdown 竞态：
  - `go test ./go-bridge -run TestServerCloseAllConnectionsClosesActiveWebSockets -count=50`
  - `go test ./go-bridge/... -count=1`
- Debug build 通过：
  - `xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' build`
- Release build 通过：
  - `./scripts/build-unsigned-release.sh`
  - 产物：`dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip`
- 已覆盖安装并启动：
  - `/Applications/CordCodeLink.app`
- 运行态验收通过：
  - `cordcode-bridge-runtime` 从 `/Applications/CordCodeLink.app/Contents/Resources/` 启动并监听 `:8777`
  - managed OpenCode child 监听 `127.0.0.1:4097`
  - no-auth `/global/health` 返回 `401`
  - Basic Auth `/global/health` 返回 `200 {"healthy":true,"version":"1.17.13"}`
  - go-bridge 日志显示 `opencode HTTP proxy registered url=http://127.0.0.1:4097`
  - go-bridge 日志显示 OpenCode SSE subscriber connected
  - go-bridge 日志显示 Relay reconnect connected
  - Management API `/internal/agents` 返回 OpenCode backend `available`
  - OpenCode Desktop settings `defaultServerUrl` 已同步到 `http://127.0.0.1:4097`
  - owner 已人工确认 iOS 端 OpenCode 模式使用正常，MacBridge 设置界面显示 `自动托管（推荐）`
  - 最终审阅后新增 stale persisted pid 端口避让 regression，Swift 定向测试 41 例通过

## 验收边界

本轮没有运行 UI tests、snapshot tests、simulator automation 或 iOS 真机自动化。原因是改动集中在 Mac app runtime/bootstrap/backend 管理链路，已通过代码阅读、定向单测、Release build、覆盖安装、本机真实运行态和 owner 的 iOS 人工验收完成验证；没有为了展示成功加入 fallback、mock 或假数据路径。

本机原有 `opencode serve` 监听 `4096` 被保留未接管；新 supervisor 按设计避让未知进程并启动自己的 `4097` child。

## Exec-plan 状态

状态文件：`.exec-plan/state/plan-41ad0453cd44.json`

队列结果：39 / 39 todos done，全部带 verification evidence。
