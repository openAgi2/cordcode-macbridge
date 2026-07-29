# CCCode iOS / MacBridge 拆仓与 cc-connect 合并方案

> **命名说明（2026-06-24）：** 本文档写于 repo rename 之前。文中 cccode-macbridge/cccode-ios 指 GitHub 旧仓库名(现为 cordcode-*);Go module path 已从 github.com/openAgi2/cccode-macbridge 重命名为 …/cordcode-macbridge。本文为历史记录。


生成时间：2026-06-01  
状态：方案讨论稿，暂未执行  
目标读者：后续接手迁移的 agent / reviewer / maintainer

## 1. 背景与目标

当前 `opencode-cc-connect` 同时承载：

- iOS App：`OpenCodeiOS/`
- 消息渲染前端：`message-web/`
- MacBridge App：`MacBridge/`
- MacBridge runtime adapter：`go-bridge/`
- Relay 服务：`relay-server/`
- 内部执行文档、handoff、exec-plan 状态

同时，`go-bridge` 依赖本地私有项目 `/Users/jacklee/Projects/cc-connect`，后者提供 Agent runtime/core 能力。两个项目目前都是私有 GitHub 仓库。

如果后续希望公开项目，当前结构存在三个主要问题：

1. iOS 客户端、Mac 桌面服务、Go runtime、Relay 服务混在一个仓库，公开后的产品边界不清晰。
2. `go-bridge` 通过本地 `replace` 依赖 `cc-connect`，外部用户无法独立构建。
3. 仓库内包含大量内部文档、真实部署痕迹、个人路径和验收记录，不适合直接切 public。

本方案建议将项目拆成两个公开候选仓库：

- `cccode-ios`：只承载 iPhone/iPad App 与消息渲染前端。
- `cccode-macbridge`：承载 MacBridge App、Go runtime adapter、Relay 服务，并尽量合并现有 `cc-connect` 的 `core/agent` 代码。

旧仓库 `opencode-cc-connect` 保留为私有历史仓库或迁移期 monorepo，不建议直接公开。

## 2. 推荐结论

推荐路线：

1. 新建私有 `cccode-macbridge` 仓库。
2. 干净迁移 `MacBridge/`、`go-bridge/`、`relay-server/`。
3. 从现有 `cc-connect` 迁入 MacBridge 需要的 `core/` 与 `agent/` 代码。
4. 让 `cccode-macbridge` 成为自给自足的 Mac 端项目，不再依赖本地 `/Users/.../cc-connect`。
5. 新建私有 `cccode-ios` 仓库。
6. 干净迁移 `OpenCodeiOS/` 与 `message-web/`。
7. 两个仓库通过稳定 wire protocol / relay protocol 协作，不共享运行时代码。
8. 两边完成 secret 清理、构建门禁和公开版文档后，再决定是否切 public。

不建议现在把所有东西合成一个更大的 monorepo。对公开项目而言，iOS App 和 MacBridge runtime 是两个不同产品面，拆开后维护、发布、签名、CI 和权限边界都更清楚。

## 3. 目标仓库边界

### 3.1 `cccode-ios`

职责：

- iPhone / iPad App。
- Pairing UI。
- Direct WebSocket / Relay transport 客户端。
- Mailbox replay 客户端。
- `BackendClient` 抽象与 iOS ViewModel。
- `message-web/` React/Vite 源码，以及由 iOS build phase 生成并嵌入 Resources 的构建产物。
- iOS 真机回归文档。
- iOS-only 的 AI agent 指引文件，例如 `CLAUDE.md` 或 `AGENTS.md`，只描述 iOS 架构和外部 MacBridge 依赖。

建议结构：

```text
cccode-ios/
  OpenCodeiOS/
    CCCode.xcodeproj 或 project.yml
    OpenCodeiOS/
    OpenCodeiOSTests/
  message-web/
  docs/
    protocol/
    setup-ios.md
    pairing.md
    real-device-regression.md
  README.md
  LICENSE
```

不应包含：

- `MacBridge/`
- `go-bridge/`
- `relay-server/`
- Claude/OpenCode/Codex/Copilot agent runtime 实现
- `cc-connect` 的 `core/agent`
- 私有 handoff、exec-plan 状态、真实 VPS 部署记录

### 3.2 `cccode-macbridge`

职责：

- MacBridge App。
- 内嵌或伴随运行的 `cccode-bridge-runtime`。
- Agent runtime/core。
- Claude Code / OpenCode / Codex 后端适配。
- Direct WebSocket server。
- Relay pairing、device trust、mailbox delivery、outbox。
- 可选的 `relay-server` 开发和部署实现。
- Mac 端安装、签名、notarization、release 文档。

建议结构：

```text
cccode-macbridge/
  MacBridge/
    CCCodeBridge.xcodeproj 或 project.yml
    MacBridge/
  go.mod
  go.sum
  cmd/
    cccode-bridge-runtime/
    relay-server/
  bridge/
    handlers.go
    server.go
    relay_*.go
    provider_*.go
  core/
    atomicwrite.go
    interfaces.go
    message.go
    provider.go
    providerproxy.go
    redact.go
    registry.go
    runas*.go
    session.go
  config/
    config.go
  agent/
    claudecode/
    opencode/
    codex/
  relay/
    internal/
  docs/
    protocol/
    setup-macbridge.md
    relay-deploy.md
    signing-notarization.md
  README.md
  LICENSE
```

说明：

- 以上结构是目标形态，不要求第一步就完成目录重命名。
- 初期可以保留 `go-bridge/`、`relay-server/` 原目录，先完成独立构建；后续再做包名和目录整理。
- `bridge/`、`cmd/`、`relay/` 的重排属于第二阶段清理，不应和首次迁移混在一个不可 review 的大改里。
- `relay-server/` 当前是独立 Go module，且不依赖 `cc-connect`。Phase 1 建议保持它的独立 module 边界，避免把 SQLite 等 relay-server 专属依赖拉入 MacBridge runtime module；是否并入主 module 应作为后续单独决策。

## 4. cc-connect 合并策略

### 4.1 为什么合进 MacBridge

如果公开目标是 CCCode 这个产品，而不是单独出售/维护一个通用 Agent SDK，那么将 `cc-connect` 的核心能力合进 `cccode-macbridge` 更合适：

- 外部用户 clone 一个 MacBridge 仓库即可构建 Mac runtime。
- 不再需要本地 `replace` 或额外私有仓库。
- MacBridge release 与 runtime/core 版本天然一致。
- go-bridge 与 agent core 的接口变更可以同仓 review。
- 后续签名、notarization、自动更新打包更直接。

代价：

- `cc-connect` 作为独立 SDK 的边界会弱化。
- 如果 Telegram/Slack 或其他消费者仍依赖 `cc-connect`，需要额外迁移计划。
- 初次迁移会涉及大量 Go import path 修改。

本方案建议产品优先：先合并 MacBridge 真实使用的 core/agent 子集；独立 SDK 需求以后再拆包。

cc-connect 上游同步策略需要在 Phase 0 明确：如果现有 `cc-connect` 仍有活跃上游或其他产品线消费者，`cccode-macbridge` 应采用 copy-and-diverge 的产品内 fork 策略，并保留旧私有仓库作为迁移来源；如果仍要持续吸收上游变更，则必须指定 backport owner 和同步节奏。没有这个承诺时，不应把合仓解释为通用 SDK 的长期维护方案。

### 4.2 迁入范围

优先迁入：

```text
cc-connect/core/atomicwrite.go
cc-connect/core/interfaces.go
cc-connect/core/message.go
cc-connect/core/provider.go
cc-connect/core/providerproxy.go
cc-connect/core/redact.go
cc-connect/core/registry.go
cc-connect/core/runas.go
cc-connect/core/runas_audit.go
cc-connect/core/runas_check.go
cc-connect/core/runas_windows.go
cc-connect/core/session.go
cc-connect/config/
cc-connect/agent/claudecode/
cc-connect/agent/opencode/
cc-connect/agent/codex/
```

按需验证后再迁入：

```text
cc-connect/core/streaming.go
```

R2 评审实测 `go-bridge` 和 `agent/claudecode`、`agent/opencode`、`agent/codex` 都不直接引用 `streaming.go` 的导出符号，而且该文件内部存在 `Platform` 字段耦合。因此它不再作为优先迁入项；只有在 Phase 1 `go build` 证明真实路径需要时才迁入，并且应优先裁剪到导出 DTO/函数所需的最小集合。

需要按实际 import 补齐的支撑代码：

- core 内被上述文件直接引用的类型、错误、工具函数。
- agent 内各 backend 的 session、event parser、model/provider 支持。
- `agent/claudecode/` 与 `agent/codex/` 显式依赖的 `config/` 包。
- 当前 `go-bridge` 测试依赖的 fake/helper 类型，如果属于生产路径依赖则迁入，否则保留测试内。
- 如果 `go build` 发现额外 core 依赖，必须记录文件名、引用符号和是否引入 Platform/Engine 耦合，再决定是否迁入。

暂缓迁入或删除：

- `cc-connect/core/engine.go`：不迁入。评审确认 `go-bridge` 不创建也不使用 `core.Engine`，已有自己的 session 管理逻辑；迁入该文件会把聊天平台适配、cron、TTS、i18n、heartbeat、management 等大量无关传递依赖带入 MacBridge。
- `cc-connect/core/streaming.go`：默认不迁入，除非 `go build` 证明实际需要。原因：R2 评审未发现 go-bridge 或三类 agent 引用其导出符号，且文件内部存在 Platform 耦合。
- Telegram / Slack / bot 消费端逻辑。
- 与 CCCode MacBridge 无关的部署脚本。
- 旧 demo、fixture、一次性实验代码。
- 私有平台配置。
- 没有当前调用方的 adapter。
- `agent/acp/`：暂缓迁入。cc-connect 中 Copilot/ACP 实际路径是 `agent/acp/`，不是 `agent/copilot/`；当前 `go-bridge` 未 import 该 backend。只有当 MacBridge 产品明确恢复 Copilot/ACP 后端时，才作为独立任务迁入。

迁入原则：

- MacBridge 用到什么，迁什么。
- 不能为了“保留完整历史”把无关私有逻辑带进公开候选仓库。
- runtime 逻辑继续放在 `core/agent`；wire protocol 适配继续放在 bridge/go-bridge 层。
- 以 `go-bridge` 的实际导出类型使用为准，而不是以 `cc-connect/core` 的包级完整性为准。评审数据表明 `go-bridge` 只使用 core 中约 58 个导出类型，迁入比例约为 10-15%。

### 4.3 core 依赖分析与精简策略

本方案采用路线 A：接口层复制 + 轻量运行时集成。

具体策略：

- 只迁入 `go-bridge` 与三个实际 agent 后端需要的 core 类型、接口、DTO 和标准库工具函数，起点为 R2 依赖扫描得到的文件清单。
- 不迁入 `core.Engine`。MacBridge runtime 继续使用 `go-bridge/handlers.go` 现有 session registry、event relay、provider switch 和 relay delivery 管理逻辑。
- 对 `message.go` / `session.go` / agent 目录的传递依赖做最小化裁剪；如果某个类型只服务 Telegram/Slack/Discord/Feishu/DingTalk/Line/WeChat/WeCom/WeiBo/QQ/Kimi/Gemini 等聊天平台，不进入 `cccode-macbridge`。
- 如果 agent 目录内部引用 core 的无关平台类型，优先在迁移时把 agent 依赖收窄到 `core.Agent`、`core.AgentSession`、`core.Event`、`core.ModelOption`、todo/memory/context/diagnostic 等 MacBridge 真实需要的接口。

R2 依赖扫描结论：

| 文件 | 迁入状态 | 迁入原因 |
| --- | --- | --- |
| `core/interfaces.go` | 优先迁入 | `core.Agent`、`core.AgentSession`、模型/todo/memory/context/diagnostic 等接口和 DTO |
| `core/message.go` | 优先迁入 | `core.Event`、事件类型和消息 DTO |
| `core/runas.go` | 优先迁入 | `BuildSpawnCommand`、`ExecSudoRunner`、`SpawnOptions`、`FilterEnvForSpawn` 等 agent 执行路径 |
| `core/runas_audit.go` | 优先迁入 | `runas.go` 同包审计依赖 |
| `core/runas_check.go` | 优先迁入 | `runas.go` 同包环境检查依赖 |
| `core/runas_windows.go` | 优先迁入 | `runas.go` 条件编译补充 |
| `core/provider.go` | 优先迁入 | `GetProviderModel`、`GetProviderModels` |
| `core/providerproxy.go` | 优先迁入 | `NewProviderProxy` |
| `core/redact.go` | 优先迁入 | `RedactArgs`、`RedactEnv` |
| `core/registry.go` | 优先迁入 | `RegisterAgent` |
| `core/atomicwrite.go` | 优先迁入 | `AtomicWriteFile` |
| `core/session.go` | 优先迁入 | `ContinueSession` 等 agent session helper |
| `core/streaming.go` | 按需验证 | 当前未发现 go-bridge/agent 直接引用导出符号，且内部有 Platform 耦合 |

上述新增的 10 个 core 文件约 1,942 行，R2 评审确认只依赖 Go 标准库，不引入 Platform / Engine / Bridge 耦合。执行 agent 仍必须用 `go build` 校验该清单，而不是把它视为不可变真理。

不采用的路线：

- 不采用“整体迁入 `core/` 再慢慢删”的路线。原因：评审确认 `core/` 约 53 个生产文件、55,782 行，至少 60% 与 CCCode 无关，整体迁入会显著增加公开项目认知负担。
- 不采用“迁入 `engine.go` 后用空实现/构建标签绕过”的路线。原因：这会制造表面可构建但边界不清的状态，并且容易把平台调度、TTS、cron、i18n 等无关失败隐藏到后续运行期。
- 暂不采用“先在 cc-connect 内做大规模 package 拆分”的路线。原因：它可能是长期最干净的 SDK 化方案，但会把拆仓公开化任务扩大成 cc-connect 架构重构；当前目标是产品仓库可构建、可验证、可公开。

## 5. 协议边界

拆仓后，iOS 与 MacBridge 之间唯一稳定边界应是协议，而不是共享源码。

必须冻结并文档化：

- Direct WebSocket wire protocol。
- Relay pairing protocol。
- Relay encrypted channel envelope。
- Mailbox delivery envelope。
- Device trust / revoke 行为。
- Backend capability schema。
- Event names 与 payload schema。优先提供 JSON Schema 或等效的 TypeScript type，不只保留自然语言说明。
- Version negotiation / minimum compatible version。

协议文档应指定 canonical source，避免两个仓库各自维护副本后漂移。推荐顺序：

1. 建立轻量 `cccode-protocol` 仓库，两个仓库通过 URL 或 submodule 引用。
2. 如果暂不建第三仓库，则以 `cccode-macbridge/docs/protocol/` 为 canonical，`cccode-ios` 只保留同步副本和来源链接。

协议文档目录建议：

```text
docs/protocol/
  unified-bridge-protocol.md
  relay-v1.md
  mailbox-envelope.md
  capability-matrix.md
```

协议变更规则：

- 新字段默认可选。
- 删除字段必须先经过一个兼容版本。
- iOS 端不应依赖 MacBridge 内部 Go 类型名。
- MacBridge 不应依赖 iOS ViewModel / Swift model 的内部字段名。
- 每次协议变更必须同时更新 iOS compatibility notes 与 MacBridge release notes。
- `protocolVersion` 建议使用整数 major version 表示破坏性协议代际，例如 `1`、`2`；非破坏性扩展用 `runtimeVersion`/release version 描述。iOS 连接时检查 minimum compatible protocol major。

## 6. 迁移阶段

### Phase 0：公开化预审，不改代码

目标：

- 明确新仓库边界。
- 列出 secrets / 私有信息风险。
- 确认 `cc-connect` 是否有其他仍需保留的消费者。

检查项：

- `go-bridge/go.mod` 的本地 `replace`。
- 文档中的真实 VPS IP、域名、route id、credential、token。
- Xcode signing team、bundle id、profile 名称。
- hardcoded `/Users/jacklee/...` 路径。
- `handoffs/`、`.exec-plan/`、完成情况文档是否应迁入。
- `docs/` 下大量内部评审、完成情况、验收记录逐个分类：公开 setup/protocol 文档可迁，内部过程文档默认不迁。
- Git 历史是否包含敏感信息。
- `cc-connect` 是否仍有 Telegram/Slack/其他 bot 消费者，是否需要继续维护旧私有仓库。

验收：

- 形成迁移清单。
- 决定新仓库名称、license、默认 visibility。
- 决定是否干净迁移，或通过 `git filter-repo` 保留部分历史。

建议：公开候选仓库使用干净迁移，旧私有仓库保留完整历史。

### Phase 1：创建 `cccode-macbridge` 私有仓库

迁移内容：

```text
MacBridge/
go-bridge/
relay-server/
```

从 `cc-connect` 迁入：

```text
core/atomicwrite.go
core/interfaces.go
core/message.go
core/provider.go
core/providerproxy.go
core/redact.go
core/registry.go
core/runas.go
core/runas_audit.go
core/runas_check.go
core/runas_windows.go
core/session.go
config/
agent/claudecode/
agent/opencode/
agent/codex/
```

`core/streaming.go` 仅在 `go build` 证明需要时迁入；默认不作为 Phase 1 初始迁入文件。

第一步可以保留原始目录名，减少变量。

需要修改：

- 移除 `go-bridge/go.mod` 对 `/Users/jacklee/Projects/cc-connect` 的本地 replace。
- 统一 Go module path。
- 修正 import path。
- 确认 MacBridge 打包 runtime 时引用新仓库内的 runtime 产物。
- 确认 MacBridge Xcode Build Phase 中类似 `cd "${SRCROOT}/../go-bridge"` 的路径在新仓库仍有效。
- 确认 `CCCodeBridge.app` 内实际嵌入可执行的 `cccode-bridge-runtime`。
- 清理默认配置中的真实 relay endpoint / route / credential。

建议 Go module 形态：

```text
cccode-macbridge/
  go.mod
  go-bridge/
  relay-server/
  core/
  config/
  agent/
```

初期建议 `go-bridge`/core/agent/config 使用 MacBridge runtime 主 module；`relay-server` 保持现有独立 module。这样既能让 MacBridge runtime 摆脱本地 `replace`，又不会把 relay-server 的 SQLite 和部署依赖强行引入 runtime module。

建议 module path：

- MacBridge runtime 主 module：`github.com/openAgi2/cccode-macbridge`
- relay-server module：保持当前 `cccode-relay`，除非后续要单独发布 Go library。

如果新仓库暂时不公开，也可以短期使用本地 module name `cccode-macbridge`；但只要计划公开或被外部 clone 构建，推荐直接使用最终 GitHub module path，避免二次 import path 迁移。

Phase 1 验收命令：

```bash
cd cccode-macbridge
go build ./go-bridge
go test ./go-bridge/... -count=1
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' build
BUILT_PRODUCTS_DIR=$(xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' -showBuildSettings | awk -F'= ' '/ BUILT_PRODUCTS_DIR = / {print $2; exit}')
test -x "$BUILT_PRODUCTS_DIR/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime"
```

说明：Phase 1 的第一验收目标是独立 build 和 runtime 嵌入成功；`go test ./...` 应作为最终门禁，但如果迁移早期因测试 import path 批量失效，应单独记录并修复，不得用跳过测试制造完成假象。

时间盒：如果 Phase 1 启动后 3 个工作日内 `go test ./go-bridge/...` 仍未通过，应拆出独立测试修复任务并记录失败原因；Phase 2 的 iOS 拆仓可以并行推进，但 MacBridge repo 不得标记完成。

不得在 Phase 1 做的事：

- 不重写协议。
- 不重构 iOS。
- 不引入 fallback/mock 替代真实 runtime。
- 不把 Telegram/Slack 等无关消费者一起迁入。

### Phase 2：创建 `cccode-ios` 私有仓库

迁移内容：

```text
OpenCodeiOS/
message-web/
docs/protocol/
```

需要修改：

- 清理任何指向旧 monorepo 内 MacBridge/go-bridge 的路径假设。
- 确认 `message-web` 构建产物仍能嵌入 iOS Resources。
- README 改成 iOS-only setup。
- 真机测试文档改成依赖外部已安装的 MacBridge。

Phase 2 验收命令：

```bash
cd cccode-ios/message-web
npm run build:ios

cd cccode-ios
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' build
```

真机安装可以作为人工验收，不作为默认自动化门禁。

不得在 Phase 2 做的事：

- 不把 Go runtime 搬进 iOS 仓库。
- 不把 MacBridge app 放进 iOS 仓库。
- 不在 iOS 正式逻辑中添加 demo/mock/fallback 连接路径。

### Phase 3：协议与跨仓兼容门禁

目标：

- 确保两个仓库独立后仍能配对、连接、收发、replay。
- 建立协议兼容检查方式。

建议补充：

- 先从当前真实实现提取协议基准：iOS 端 `OpenCodeiOS/OpenCodeiOS/Services/Backend/BridgeModels.swift`、`BackendModels.swift`、`BridgeMessageMapping.swift` 的 Codable/映射类型，以及 MacBridge/go-bridge 端 `types.go` 的 JSON tag。
- MacBridge 输出 `protocolVersion`、`runtimeVersion`、`capabilities`。
- iOS 在连接时记录并展示不兼容错误。
- 文档化 minimum compatible MacBridge version。
- 将协议样例 JSON 放进两个仓库的测试资源。
- 在 Phase 3 完成前进入协议冻结窗口：iOS 和 MacBridge 不得单方面修改 wire protocol；确需修改时必须同时更新 canonical 协议文档和另一端兼容处理。

验收：

- Direct LAN pairing + WebSocket 连接通过。
- Relay pairing + encrypted channel 连接通过。
- Offline mailbox replay 至少完成一次真实设备闭环。
- Revoke 后旧设备无法继续连接。

注意：根据项目约束，UI tests、snapshot tests、simulator automation、真机视觉自动化不应由 agent 擅自运行；需要用户明确许可后再执行。

### Phase 4：公开化清理

两个新仓库都需要：

- `README.md`
- `LICENSE`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.gitignore`
- secret scan 记录
- CI workflow
- release checklist

MacBridge 公开文档应包含：

- 本地安装。
- 后端依赖安装。
- Claude Code / OpenCode / Codex / Copilot 配置方式。
- Relay endpoint 配置方式。
- 自建 relay-server 部署方式。
- `relay-server` 默认 Linux/VPS DB 路径说明，避免 Mac 本地开发者误以为 MacBridge 会写 `/var/lib/cccode-relay/relay.db`。
- 签名和 notarization 说明。

iOS 公开文档应包含：

- Xcode 构建。
- Bundle id / signing 替换说明。
- Pairing 流程。
- Relay/offline mailbox 行为说明。
- 真机回归 checklist。

必须清理或替换：

- 真实 relay endpoint。
- 真实 VPS IP。
- route id。
- pairing token。
- management token。
- OpenCode password。
- Apple Developer Team / profile 个人信息。
- `/Users/jacklee/...` 绝对路径。
- handoff 和 exec-plan 内部状态。

### Phase 5：旧仓库退役

`opencode-cc-connect` 建议保持私有，作为历史和迁移来源。

退役步骤：

1. README 顶部写清新仓库位置。
2. 冻结 feature 开发。
3. 只接受迁移相关 bugfix。
4. 两个新仓库稳定后 archive 或保留私有只读。

## 7. Git 历史策略

### 7.1 干净迁移

做法：

- 新建空 repo。
- 从当前工作树复制公开候选源码。
- 第一条 commit 写作 `Initial source import`。
- 旧 repo 保留完整历史。

优点：

- 公开风险最低。
- 不需要清洗历史 secrets。
- 初始公开内容可控。

缺点：

- 新 repo 看不到旧开发历史。
- blame 信息丢失。

建议：对准备公开的仓库采用干净迁移。

### 7.2 过滤历史迁移

做法：

```bash
git filter-repo --path MacBridge --path go-bridge --path relay-server
git filter-repo --path OpenCodeiOS --path message-web
```

优点：

- 保留部分 blame。
- 审计演进更方便。

缺点：

- 仍需彻底 secret scan。
- 历史文档、路径、配置容易残留。
- 对公开发布风险更高。

建议：只有在确认历史不含敏感信息时使用。

## 8. CI / 验收门禁

### 8.1 `cccode-macbridge`

最低门禁：

```bash
go test ./go-bridge/... -count=1
(cd relay-server && go test ./... -count=1)
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' build
```

建议增强：

```bash
go vet ./go-bridge/...
go test ./go-bridge/... -race -count=1
(cd relay-server && go test ./... -race -count=1)
codesign --verify --deep --strict <built CCCodeBridge.app>
```

如果迁移 `.golangci.yml` 或引入 `staticcheck`/`golangci-lint`，应作为 PR CI 门禁之一。两个新仓库都应有 PR CI，而不是只在 merge 后验证。

需要人工验收：

- MacBridge 启动 runtime。
- port 8777 direct WebSocket 可连接。
- relay bridge 可连接。
- Claude/OpenCode/Codex backend 至少各完成一次 basic send。
- revoke 后设备无法继续访问。

### 8.2 `cccode-ios`

最低门禁：

```bash
cd message-web && npm run build:ios
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' build
```

建议 targeted unit test：

```bash
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test -only-testing:CCCodeTests
```

`message-web` 应增加 `npm run test` 作为 PR CI 门禁，避免只验证可构建、不验证渲染逻辑。

需要人工或明确授权后执行的真机验收：

- Pairing。
- Direct connection。
- Relay connection。
- Offline mailbox replay。
- Long-history session loading。
- Backend switching。

## 9. 风险与处理

### 9.1 Go import path 大面积修改

风险：迁入 `cc-connect` 后会出现大量 import path 报错。

处理：

- 先保留目录结构，尽量只改 module path。
- 使用 `go build` 和 targeted `go test` 驱动修复。
- 不在同一阶段做 package 重命名和架构重构。

### 9.2 core 迁入膨胀与认知负担

风险：`cc-connect/core/` 约 53 个生产文件、55,782 行，其中大量代码服务聊天平台、TTS、cron、i18n、Markdown 渲染、management、heartbeat。整体迁入会让 `cccode-macbridge` 公开仓库背上与产品无关的维护成本。

处理：

- 执行 §4.3 的精简迁入路线，以 R2 文件清单为初始迁入集合。
- `engine.go`、`bridge.go`、`management.go`、`cron.go`、`heartbeat.go`、`speech.go`、`tts.go`、`i18n.go`、`markdown_*.go` 默认不迁。
- `streaming.go` 默认不迁；只有真实 build 需要时才做最小化迁入。
- 若某个 agent 编译确实需要其中少量 DTO，先抽出 DTO，不迁入完整平台实现。

### 9.3 core.Engine 双向依赖

风险：`engine.go` 内部引用 Platform、Bridge、Management、Cron、Hooks 等 cc-connect 平台抽象；CCCode 的 go-bridge 并不使用 `core.Engine`，迁入后反而需要补齐大量无关实现。

处理：

- `engine.go` 明确列入“不迁入”。
- MacBridge runtime 继续使用 go-bridge 自己的 session registry 和 handler path。
- 如果未来需要更统一的 engine，应单独设计 CCCode-specific engine，而不是搬入 cc-connect engine。

### 9.4 core 与 bridge 边界变差

风险：合仓后 `bridge` 可能直接侵入 agent runtime 内部状态。

处理：

- 保持原则：runtime logic 在 `core/agent`，wire protocol 在 `bridge/go-bridge`。
- bridge 只依赖 `core.Agent`、`core.AgentSession`、`core.Event` 等接口。
- 禁止 View/UI/transport 代码直接依赖 agent 私有实现。

### 9.5 iOS / MacBridge 协议版本漂移

风险：分仓后两边发布节奏不同，一边升级导致另一边不可用。

处理：

- 建立 `protocolVersion`。
- iOS 连接时检测 minimum compatible runtime。
- 协议文档和 JSON 样例作为测试资源。
- release notes 必须声明兼容范围。

### 9.6 协议冻结窗口缺失

风险：Phase 1 和 Phase 2 完成后，两个仓库可能在 Phase 3 / Task E 前并行开发。如果这段窗口内两边各自修改 wire protocol，会在集成验证时累积不兼容。

处理：

- Phase 3 完成前约定协议冻结。
- 确需改协议时，同步更新 canonical 协议文档、JSON Schema/TypeScript type、iOS Codable/映射、MacBridge JSON tag。
- 每周做一次轻量协议兼容检查，至少比对 iOS wire model 与 MacBridge `types.go` 的字段名、可选性和事件 payload。

### 9.7 secrets 泄露

风险：公开仓库带出真实 endpoint、credential、路径、签名信息。

处理：

- 新仓库采用干净迁移。
- 不迁入 `handoffs/`、`.exec-plan/`、内部完成情况文档。
- 使用 secret scan。
- 将真实配置改为用户本地 config/env/CLI flag。

### 9.8 其他 cc-connect 消费端断裂

风险：`cc-connect` 若还有 Telegram/Slack 等使用者，合并后会影响它们。

处理：

- Phase 0 先审计 `cc-connect` 消费者。
- 若仍需维护，可将旧 `cc-connect` 私有保留一段时间。
- MacBridge 合并只代表 CCCode 产品线，不必立即删除旧项目。

### 9.9 MacBridge 构建脚本路径耦合

风险：MacBridge Xcode Build Phase 当前依赖相对路径定位 Go runtime。拆仓后如果目录重命名，Xcode build 可能成功到一半才在脚本阶段失败，或者嵌入旧 runtime。

处理：

- Phase 1 保留 `go-bridge/` 原目录，先减少路径变量。
- 验收时检查 app bundle 内 `Contents/Resources/cccode-bridge-runtime` 存在且可执行。
- 后续若重命名 `go-bridge/`，必须同 commit 更新 Build Phase 和文档。

### 9.10 go-bridge 测试迁移工作量

风险：go-bridge 测试文件较多，迁移 core/agent/config 后 import path 和 fake 类型可能批量失效。

处理：

- Task A 先做实际传递依赖分析，再迁入最小集合。
- 保留并修复现有测试，不用删除或跳过来换取表面成功。
- `go test ./go-bridge/... -count=1` 是 Phase 1 完成门禁；若短期未过，任务不得标记完成。

## 10. 建议给执行 agent 的任务拆分

### Task A：MacBridge repo bootstrap

输入：

- 当前仓库：`opencode-cc-connect`
- 当前本地 `cc-connect`

输出：

- 新私有 repo `cccode-macbridge`
- 能独立通过 `go test ./go-bridge/...` 与 `(cd relay-server && go test ./...)`
- 能独立 build `CCCodeBridge.app`

范围：

- 复制 `MacBridge/`、`go-bridge/`、`relay-server/`
- 分析 go-bridge 对 cc-connect/core 的实际传递依赖，输出完整文件清单，包含文件名、引用符号、是否引入 Platform/Engine 耦合。
- 以 R2 清单为初始集合，复制：
  - `core/atomicwrite.go`
  - `core/interfaces.go`
  - `core/message.go`
  - `core/provider.go`
  - `core/providerproxy.go`
  - `core/redact.go`
  - `core/registry.go`
  - `core/runas.go`
  - `core/runas_audit.go`
  - `core/runas_check.go`
  - `core/runas_windows.go`
  - `core/session.go`
  - `config/`
- `core/streaming.go` 仅在 `go build` 证明需要时迁入。
- 复制 `agent/claudecode/`、`agent/opencode/`、`agent/codex/`
- 修 import 和 go module
- 移除本地 replace
- 修复 go-bridge 现有测试 import path，保持测试门禁

不做：

- iOS 迁移
- UI 改版
- 协议重写
- 公开发布
- 迁入 `core/engine.go`
- 默认迁入 `core/streaming.go`
- 迁入 `agent/acp/`，除非另有独立产品需求

### Task B：iOS repo bootstrap

输出：

- 新私有 repo `cccode-ios`
- `message-web` 构建通过
- iOS build 通过

范围：

- 复制 `OpenCodeiOS/`
- 复制 `message-web/`
- 复制必要协议文档
- 清掉 MacBridge/go-bridge 源码依赖

不做：

- Go runtime 迁移
- MacBridge 构建
- 真机 UI 自动化

### Task C：protocol compatibility pack

输出：

- canonical 协议文档、JSON Schema 或等效 TypeScript type、JSON 样例。
- 连接兼容策略。

范围：

- unified bridge protocol
- relay v1
- mailbox envelope
- capability matrix
- version negotiation
- 确定 `protocolVersion` 在 `hello` / `register` 或等效握手消息中的字段名、校验逻辑和错误返回。

不做：

- 在两个仓库手工维护两份互相独立的协议副本

### Task D：public readiness pass

输出：

- 公开前清理报告。
- README/LICENSE/SECURITY/CONTRIBUTING。
- secret scan 结果。
- CI 配置。

范围：

- 两个新仓库分别执行。
- 不直接公开，直到 owner 确认。

### Task E：integration verification

输出：

- iOS ↔ MacBridge 端到端验证记录。
- direct path、relay path、offline mailbox replay 的结果。
- revoke / reconnect / backend switching 的人工验收记录。

范围：

- 使用已经拆出的两个私有仓库产物。
- 真实 MacBridge App + 真实 iOS App。
- 不用 mock/fallback 替代失败路径。

注意：

- UI tests、snapshot tests、simulator automation、真机视觉自动化需要 owner 明确授权后再执行。
- 默认可做代码阅读、静态检查、targeted build、targeted unit test。

## 11. 暂不建议做的事

- 不建议直接把当前 `opencode-cc-connect` 切 public。
- 不建议把 iOS、MacBridge、cc-connect 全部塞进一个更大的公开 monorepo。
- 不建议迁移时顺手重写协议或重构 UI。
- 不建议把历史 handoff / exec-plan / 完成情况文档公开。
- 不建议用 mock/fallback 让迁移后的 build “看起来能跑”；真实路径失败应直接暴露并修复。

## 12. 推荐执行顺序摘要

```text
1. 新建私有 cccode-macbridge
2. 迁入 MacBridge/go-bridge/relay-server
3. 迁入 cc-connect core/config/agent 最小必要集合，不迁入 engine.go
4. 用 R2 core 清单启动迁移，`streaming.go` 按需验证
5. 修 go module/import，跑通 go test 与 MacBridge build
6. 新建私有 cccode-ios
7. 迁入 OpenCodeiOS/message-web
8. 跑通 message-web build 与 iOS build
9. 建立协议文档与兼容门禁
10. 做公开化清理和 secret scan
11. 做一次真实 iOS ↔ MacBridge 集成验证
12. owner 确认后再切 public
```

本方案的核心取舍是：公开项目优先保证边界清楚、构建可复现、真实路径可验证；历史完整性和一次性大重构都放在次要位置。

## 13. 评审建议采纳情况

评审文档：`docs/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案-评审报告.md`

已采纳：

- 移除 `core/engine.go` 迁入建议。原因：`go-bridge` 不使用 `core.Engine`，迁入会带来聊天平台、TTS、cron、i18n、heartbeat 等无关传递依赖。
- 补充 `config/` 包到迁入范围。原因：`agent/claudecode/` 与 `agent/codex/` 显式依赖该包。
- 修正 `agent/copilot/` 为 `agent/acp/` 并标注暂缓。原因：cc-connect 实际路径是 `agent/acp/`，且当前 `go-bridge` 未使用 ACP/Copilot。
- 增加 §4.3 core 精简迁入策略。原因：评审数据表明 `core/` 大部分与 CCCode 无关，必须以实际依赖为准。
- 明确 `relay-server` 保持独立 module。原因：它不依赖 cc-connect，且 SQLite/部署依赖不应污染 MacBridge runtime module。
- 明确协议 canonical source、protocol major version、JSON Schema/TypeScript type 诉求。原因：分仓后协议漂移是主要风险。
- 增加 Phase 1 对 MacBridge Build Phase 路径和 runtime 嵌入的验收。原因：拆仓后路径耦合容易导致 app bundle 嵌入旧 runtime 或缺 runtime。
- 增加 CI 建议：PR CI、`go vet`/lint、`message-web npm run test`。原因：公开仓库需要可重复的 review 门禁。
- 增加遗漏风险：core 膨胀、Engine 双向依赖、Build Phase 路径、go-bridge 测试迁移、relay-server 部署路径。
- 增加 Task E 集成验证。原因：两个仓库都能 build 不等于 iOS ↔ MacBridge 产品路径可用。

部分采纳 / 暂不采纳：

- 未采纳“在迁入时标注 `// cccode: unused`”。原因：本修订版选择最小迁入，不整体带入 unused 代码，因此不需要用注释标记大量无关文件。
- 暂不采纳“建立独立 `cccode-protocol` 仓库”为立即执行项。原因：这是推荐的最佳形态，但会增加第三个仓库和发布流程；本方案先要求指定 canonical source，允许后续再抽独立 protocol repo。
- 暂不采纳“在 cc-connect 侧先做 internal package 拆分”。原因：这会把任务扩大成 cc-connect 架构重构；当前公开化目标更需要可控的产品仓库迁移。
- 暂不采纳“整体迁入 core 后用构建标签裁剪”。原因：容易制造边界不清和假成功，且违背公开仓库最小可维护面的目标。

## 14. 第二轮评审建议采纳情况

评审文档：`docs/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案-评审报告-R2.md`

已采纳：

- 补充 R2 发现的 10 个 core 文件到优先迁入清单：`atomicwrite.go`、`provider.go`、`providerproxy.go`、`redact.go`、`registry.go`、`runas.go`、`runas_audit.go`、`runas_check.go`、`runas_windows.go`、`session.go`。原因：R2 扫描确认三个实际 agent 直接引用这些符号，且这些文件只依赖标准库，不引入 Platform/Engine 耦合。
- 将 `streaming.go` 从优先迁入降级为按需验证。原因：R2 未发现 go-bridge 或三个实际 agent 引用其导出符号，且该文件内部存在 Platform 耦合。
- 在 §4.3 增加具体依赖分析表。原因：执行 agent 需要可审计的初始文件清单，而不是只看到方法论。
- 明确建议 Go module path：MacBridge runtime 使用 `github.com/openAgi2/cccode-macbridge`，relay-server 保持 `cccode-relay`。原因：import path 决策会影响 Phase 1 全部迁移。
- 改进 Phase 1 runtime 嵌入验证，使用 `xcodebuild -showBuildSettings` 提取 `BUILT_PRODUCTS_DIR`。原因：DerivedData 路径在本地和 CI 中不稳定。
- 补充 Phase 1 `go test` 时间盒。原因：测试修复不应无限阻塞 iOS 拆仓并行推进，但 MacBridge repo 也不能在测试未过时标记完成。
- 更正 Phase 3 协议基准文件名：`BridgeModels.swift`、`BackendModels.swift`、`BridgeMessageMapping.swift`、`types.go`。原因：这些才是当前真实 wire protocol 实现入口。
- 增加协议冻结窗口风险和处理。原因：拆仓后 Phase 3 / Task E 前的并行开发容易积累 wire protocol 不兼容。
- 统一 §9 风险编号为平级编号。原因：避免后续追加风险时编号混乱。

部分采纳 / 暂不采纳：

- 未将 `streaming.go` 直接移到“不需要迁入”。原因：R2 的扫描结论足以把它降级，但最终仍应以 Phase 1 `go build` 和 agent 编译结果为准；因此保留“按需验证”状态。
- 未把 Phase 2 启动完全绑定到 Phase 1 `go test` 通过。原因：iOS 拆仓与 go-bridge 测试修复可以并行；但文档已明确 MacBridge repo 不得在测试未通过时标记完成。

## 15. 第三轮评审建议采纳情况

评审文档：`docs/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案-评审报告-R3.md`

已采纳：

- 同步 Phase 1 “从 `cc-connect` 迁入”代码块，使其与 §4.2 / §4.3 / Task A 一致：12 个优先 core 文件 + `config/` + 三个实际 agent 目录。
- 将 Phase 1 中的 `core/streaming.go` 改为单独说明：仅当 `go build` 证明需要时迁入，默认不作为初始迁入文件。
- 在 Task C 中补充 `protocolVersion` 协商时机决策。原因：R3 认为该项不阻塞主方案，但应在协议兼容包任务中明确落点。

部分采纳 / 暂不采纳：

- 暂不在主方案中直接指定 `protocolVersion` 必须放入 `hello` 还是 `register`。原因：当前真实 wire protocol 需要在 Task C 中从 `BridgeModels.swift` / `BackendModels.swift` / `BridgeMessageMapping.swift` / `types.go` 一并提取后再定，过早写死字段位置会增加返工风险。
