# CCCode Mac Bridge Phase 0 开发决策摘要

> 日期：2026-05-07
> 适用范围：Phase 0 工程执行、issue/PR 描述、开发群同步

## 可复制摘要

Phase 0 起，CCCode 产品化路线只把 `go-bridge` 视为唯一 backend/runtime 基础。早期 Node.js UnifiedBridge 服务已经废弃，不再进入产品化实现、测试或发布路径。

iOS 代码中的 `UnifiedBridgeTransport`、`UnifiedBridgeAdapter`、`UnifiedBridgeModels` 等 `UnifiedBridge*` 命名是历史命名债，不表示当前 go-bridge 连接功能废弃。P0/P1/P2 的处理原则是保留现有可用连接能力，在 Bridge v1 schema、认证和模型迁移稳定后逐步重命名和迁移，不直接删除当前可用功能。

`Claude Code`、`OpenCode`、`Codex`、`Copilot` 是 go-bridge 管理的 agent/provider target，不是和 go-bridge 并列的 backend。后续任务统一使用 `go-bridge runtime`、`agent/provider`、`Bridge v1` 术语，避免继续把历史 `UnifiedBridge` 协议名当成当前产品 backend。

用户安装产品态 `CCCode Bridge.app` 后，不需要单独下载、编译、配置 `cc-connect`、go-bridge CLI、launchctl 服务或其他 Bridge 依赖。`cc-connect` 是 runtime 内部 Go module 依赖，Phase 0 优先通过 tagged module 收敛，不复制为长期 fork。

## 术语约束

| 推荐术语 | 含义 | 避免写法 |
|---|---|---|
| go-bridge runtime | Mac 端唯一 backend/runtime 基础 | UnifiedBridge backend |
| Bridge v1 / cccode-bridge | 新产品化协议 | unified-bridge 新协议 |
| agent/provider target | Claude/OpenCode/Codex/Copilot 等运行目标 | 多 backend 并列 |
| UnifiedBridge* 命名债 | iOS 现有可用 go-bridge 连接层的历史命名 | 废弃功能、可直接删除 |

## Phase 0 执行约束

- 不提前执行 Phase 1/2/3/4。
- 不删除 iOS 当前可用的 `UnifiedBridge*` 功能。
- 默认只运行静态检查、Go build、Go unit test。
- 不主动运行 UI tests、snapshot tests、simulator automation 或高成本视觉验证。
- 开发阶段不使用 fallback、假数据、mock 数据或 placeholder 掩盖真实路径失败；fixture 只能用于协议测试并与正式逻辑隔离。
