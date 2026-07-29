# 架构健康度治理执行计划

- **日期**：2026-07-03
- **来源评估**：[2026-07-03-architecture-health-assessment.md](2026-07-03-architecture-health-assessment.md)
- **性质**：给后续开发 agent 直接实施和给评审 agent 复核的施工计划。不是新的架构评估。
- **范围**：`cordcode-macbridge` 为主，`cordcode-ios` 仅在涉及 protocol/capability 客户端兼容或 web shared package 时进入。
- **执行原则**：先做低风险、高收益、可验证的小步治理；不要一上来拆 iOS 巨型 UI 或做跨仓大重构。
- **计划修订**：2026-07-03 施工前评审后补齐验收 filter、现有测试不回归、capability 顺序、TOML options 解码等价性与 agent type 过滤测试要求。评审建议全部采纳，未采纳项见第 10 节。

---

## 0. 施工前置约束

开发 agent 开始前必须先读：

1. 本计划。
2. [2026-07-03-architecture-health-assessment.md](2026-07-03-architecture-health-assessment.md)。
3. [CLAUDE.md](../CLAUDE.md) 中 Build & test、Backend runtime model、Protocol versioning、CHANGELOG 规则。
4. 如果改 iOS：`../cordcode-ios/CLAUDE.md` 与 `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`。

硬约束：

- 不运行 UI tests、snapshot tests、simulator automation、真机安装，除非项目方明确授权。
- 不引入生产 fallback、mock、placeholder 或“假成功”路径来掩盖真实失败。
- 不改变 wire protocol 字面量：`cordcode-bridge`、`cccode-relay`、HKDF info 字符串均为冻结契约。
- 不重新启用 `session_pagination` capability。当前关闭原因是 UI backward paging 振荡 + relay 帧上限，不是缺稳定游标。
- 每个任务必须做到：代码改动、定向测试、必要文档/CHANGELOG、风险说明。

---

## 1. 批次规划

| 批次 | 目标 | 是否建议立即实施 | 风险 |
|---|---|---:|---|
| A | capability 单源化，修 `question_reply` hello_ack 漏宣告 | 是 | 低 |
| B | `config/config.go` 死重瘦身，先切断生产依赖，再删除无关业务包 | 是，但分两步 | 中 |
| C | web renderer 共享包化 | 暂缓到 A/B 之后 | 中 |
| D | iOS/Mac god-object 拆分 | 暂缓，先补 characterization tests | 高 |
| E | 工程宪法与 CI 卡口 | A/B 后补 | 中 |

第一轮建议只做 **A + B1**。B2 是否删除整个 `config/` 包，必须由 B1 的测试结果和评审确认后再做。

---

## 2. 批次 A：Capability 单源化

### A1. 问题

当前同一份 backend capability 逻辑写了两遍：

- `go-bridge/handlers.go`：`Handlers.BackendList()`
- `go-bridge/agent_descriptor.go`：`deriveCapabilities()`

两者已经漂移：

- `BackendList()` 在 Codex `app_server` 模式追加 `compression` 和 `question_reply`。
- `deriveCapabilities()` 只追加 `compression`，导致 `hello_ack.backends[]` 漏 `question_reply`。

### A2. 目标行为

同一个 backend 在两条来源上的 capability 必须一致：

- Management API / `BackendList()`
- `hello_ack.backends[]` / `BuildAgentDescriptor()`

Codex `app_server` 模式必须同时宣告：

- `compression`
- `question_reply`

Codex 非 `app_server` 模式不得宣告这两个 app-server 专属能力。

`opencode` 与 `codex` 仍不得宣告 `permission_resolve`，除非后续真实实现变更。

`session_pagination` 仍不得宣告。

### A3. 推荐改法

新增一个小文件：

```text
go-bridge/backend_capabilities.go
```

放一个单一来源函数，名字建议：

```go
func deriveBackendCapabilities(id string, agent core.Agent, codexBackendMode string) []string
```

要求：

- 把当前 `BackendList()` 和 `deriveCapabilities()` 的共同逻辑搬进去。
- 保持现有 `deriveCapabilities()` capability 追加顺序，避免顺序敏感测试或客户端调试输出出现无意义漂移；`question_reply` 与 `compression` 同处 Codex `app_server` 分支，顺序为 `compression` 后接 `question_reply`。
- 保留 `session_pagination` 的关闭注释，或把注释从 `agent_descriptor.go` 移到新函数旁。
- `BackendList()` 只调用该函数，不再手写 capability。
- `BuildAgentDescriptor()` 只调用该函数，不再保留第二套 `deriveCapabilities()`。
- 可保留 `deriveCapabilities` wrapper 一轮兼容测试，但不要形成第三套逻辑。

### A4. 必须新增/调整的测试

MacBridge 仓库内新增或修改 Go 测试：

1. `go-bridge/agent_descriptor_test.go`
   - 必须新增至少一个以 `TestBuildAgentDescriptor` 开头的测试，确保 A5 / 第 7 节 `-run` filter 不会空跑。
   - 新增：Codex `app_server` descriptor 包含 `compression` 与 `question_reply`。
   - 新增：Codex 非 `app_server` descriptor 不包含 `compression` / `question_reply`。
   - 新增：descriptor 不包含 `session_pagination`。

2. `go-bridge/handlers_test.go` 或新建 `go-bridge/backend_capabilities_test.go`
   - 新增：同一个 fake agent 经 `BackendList()` 与 `BuildAgentDescriptor()` 生成的 capabilities 集合相同。
   - 新增：OpenCode/Codex 仍不宣告 `permission_resolve`。

现有 capability 相关测试必须继续全绿，包括但不限于：

- `TestBackendList*`
- `TestFullAgentCapabilities`
- `TestCodexAppServerCompressionCapability`
- `TestCodexExecNoCompressionCapability`
- `TestRegisterAck*`

测试不要依赖 slice 顺序，除非实现明确排序；建议转 set 比较。

### A5. 验收命令

```bash
go test ./go-bridge -run 'Test.*Capabilit|TestBackendList|TestBuildAgentDescriptor' -count=1
go test ./go-bridge/... -count=1
```

如改动触及 protocol 文档或 iOS mirror，再补：

```bash
cd ../cordcode-ios
xcodebuild -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode -destination 'generic/platform=iOS' build
```

本任务预计不需要 iOS 改动，也不需要 UI automation。

### A6. 完成证据

完成报告必须列出：

- 单一 capability 函数位置。
- `question_reply` 同时出现在 `BackendList()` 和 `BuildAgentDescriptor()` 产物中的测试名。
- 至少一个 `TestBuildAgentDescriptor*` 测试名，证明 A5 filter 已兑现。
- `session_pagination` 仍关闭的测试或代码证据。
- 现有 capability 相关测试未回归的证据。
- `go test ./go-bridge/... -count=1` 通过结果。

---

## 3. 批次 B：`config/config.go` 死重瘦身

### B1. 背景

`config/config.go` 3238 行，含大量与当前 MacBridge 产品无关的 Weixin/Feishu/Webhook/Cron/TTS/Hook/Speech/Display 等历史业务结构。

生产代码当前只有一个文件 import：

```text
go-bridge/provider_switch.go
```

实际依赖能力：

- 加载 `~/.cc-connect/config.toml` 或 `CC_CONNECT_CONFIG`
- 读取 projects
- 按 agent type 与 work_dir / base_dir 选 project
- 读取 project agent providers
- 读取 active provider
- 映射到 `core.ProviderConfig`

### B2. 分步策略

不要一刀删整个 `config/` 包作为第一步。推荐两步：

1. **B1：切断生产依赖**
   - 在 `go-bridge/` 内实现最小 TOML 读取结构。
   - `go-bridge/provider_switch.go` 不再 import `github.com/openAgi2/cordcode-macbridge/config`。
   - 保留 `config/` 包和测试不动，降低第一步风险。

2. **B2：删除或归档 config 包**
   - 只有 B1 通过评审后再做。
   - 删除 `config/` 包及其仅服务旧业务的测试，或迁到 `docs/upstream-memory/`/历史归档。
   - 同步检查 `go test ./...` 是否还包含无用旧测试。

### B3. B1 推荐改法

新增文件：

```text
go-bridge/provider_seed_config.go
```

定义最小内部结构，示意：

```go
type providerSeedConfig struct {
    Projects []providerSeedProject `toml:"projects"`
}

type providerSeedProject struct {
    Name    string            `toml:"name"`
    Mode    string            `toml:"mode"`
    BaseDir string            `toml:"base_dir"`
    Agent   providerSeedAgent `toml:"agent"`
}

type providerSeedAgent struct {
    Type      string               `toml:"type"`
    Options   map[string]any       `toml:"options"`
    Providers []providerSeedProvider `toml:"providers"`
}
```

Provider 结构必须覆盖当前映射字段：

- `name`
- `api_key`
- `base_url`
- `model`
- `thinking`
- `env`
- `models[].model`
- `models[].alias`
- `codex.wire_api`
- `codex.http_headers`

解析库优先使用根 module 已有依赖 `github.com/BurntSushi/toml`，不要引入新 TOML parser。

保留这些函数的外部行为：

- `loadProviderSeedForAgent(agentType, workDir string)`
- `ccConnectConfigPath()`
- `findProviderProject(...)`
- `projectMatchesWorkDir(...)`
- `providerConfigsToCore(...)`

可以把参数类型从 `ccconfig.*` 改成内部类型，但函数语义不变。

### B4. B1 必须测试

修改或新增 `go-bridge/provider_switch_test.go`：

1. `CC_CONNECT_CONFIG` 指向不存在文件时返回空 provider 且不报错。
2. 显式 `CC_CONNECT_CONFIG` 优先于默认路径。
3. exact `work_dir` 匹配优先。
4. `multi-workspace` + `base_dir` prefix 匹配保留。
5. active provider 从 `agent.options.provider` 读取。
6. `agent.type` 不匹配的 project 不被选中。
7. `agent.options.provider` 与 `agent.options.work_dir` 的 TOML 解码等价性保留：
   - 使用 `map[string]any` 解码后，string 类型的 `provider` / `work_dir` 仍能被 `optionString` 正确提取。
   - 同一个 fixture 中可包含 number/bool 等混合 options，证明非 string 值不会误转成 active provider 或 work_dir。
8. provider 映射保留：
   - model list
   - env
   - codex wire_api
   - codex http_headers

新增静态防回归测试：

```go
func TestProviderSeedDoesNotImportLegacyConfigPackage(t *testing.T)
```

该测试读取 `go-bridge/provider_switch.go` / `go-bridge/provider_seed_config.go`，断言不包含：

```text
github.com/openAgi2/cordcode-macbridge/config
```

### B5. B1 验收命令

```bash
go test ./go-bridge -run 'Test.*Provider|TestProviderSeed' -count=1
go test ./go-bridge/... -count=1
```

如 B1 不删除 `config/` 包，不要求 `go test ./config` 变化。

B1 完成时，现有 provider seed 相关测试必须继续全绿，包括但不限于：

- `TestLoadProviderSeedForAgent_*`
- `TestApplyProviderSeed_*`
- `go-bridge/provider_switch_test.go` 中既有 provider 解析与映射测试

### B6. B2 删除 config 包的进入条件

只有同时满足下面条件，才能进入 B2：

- B1 已合入或至少通过评审。
- `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' --glob '!*_test.go' .` 返回空。
- 项目方确认不再维护 `.cc-connect/config.toml` 的旧业务写入能力，只保留读取 provider seed。
- 评审 agent 同意删除 `config/` 包不会破坏迁移或调试流程。

B2 删除时必须处理：

- `config/config.go`
- `config/*_test.go`
- `agent/claudecode/provider_integration_test.go`
- `agent/codex/provider_switch_test.go` 中 test-only 对旧 config 包的依赖

B2 验收：

```bash
rg 'Weixin|Feishu|EnsureProjectWithFeishu|EnsureProjectWithWeixin' --glob '*.go' .
rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' .
go test ./... -count=1
```

允许历史 docs 命中 Weixin/Feishu，不允许生产 Go 代码命中。

### B7. 完成证据

B1 完成报告必须列出：

- 新最小 TOML parser 文件。
- 生产 import 已切断的 `rg` 结果。
- provider seed 行为测试名。
- options 解码等价性测试名。
- agent type 不匹配不被选中的测试名。
- 现有 provider seed 相关测试未回归的证据。
- `go test ./go-bridge/... -count=1` 通过结果。

B2 完成报告必须额外列出：

- 删除/归档的文件清单。
- 剩余 Weixin/Feishu 命中范围。
- `go test ./...` 结果。

---

## 4. 批次 C：web renderer 共享包化

### C1. 当前证据

`../cordcode-ios` 下：

- `message-web/src/components/blocks/ToolBlock.tsx` 与 `remote-web/src/renderer/components/blocks/ToolBlock.tsx` 字节全等。
- `DiffViewer.tsx` 字节全等。
- `ReasoningBlock.tsx` 仅差 2 行。
- `ProcessGroup.tsx`、`NarrativeBlock.tsx` 高度重复。
- React 版本不同：`message-web` 18.3.1，`remote-web` 19.2.7。

### C2. 不建议立即做的原因

这是中等风险跨前端工程重构，容易牵动构建、打包、资源嵌入和 UI 行为。必须在 A/B 后单独执行。

### C3. 施工边界

推荐新增共享包目录：

```text
../cordcode-ios/shared-message-renderer/
```

第一步只迁移纯展示、无平台依赖组件：

- `ToolBlock`
- `DiffViewer`
- 共享 types 中稳定的 block/item 类型

暂不迁移：

- 含 Electron/remote 专属逻辑的入口
- iOS WebKit bridge
- automation fallback
- git directive UI，除非两边产品行为已确认一致

### C4. 验收

不得使用截图/UI automation 作为默认验收。先用：

```bash
cd ../cordcode-ios/message-web && npm test -- --runInBand
cd ../cordcode-ios/message-web && npm run build
cd ../cordcode-ios/remote-web && npm test -- --runInBand
cd ../cordcode-ios/remote-web && npm run build
```

实际命令以两个 package.json 中现有 scripts 为准；若 scripts 不存在，执行 agent 必须先记录阻塞，不得造假通过。

---

## 5. 批次 D：God-object 拆分

### D1. 范围

目标对象：

- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift`
- `go-bridge/handlers.go`

### D2. 进入条件

不得作为第一轮治理开工。进入前必须先补 characterization tests：

- 对被拆方法的输入/输出、事件、状态变更建立测试。
- 拆分 PR 每次只移动一个职责块。
- 不同时改业务行为和物理拆文件。

### D3. 推荐拆分顺序

1. `go-bridge/handlers.go`
   - 先按 RPC domain 拆文件，保持同一 package。
   - 不改 method 名，不改 wire shape。

2. `BridgeProvider.swift`
   - 先抽纯策略：connection strategy、candidate URL 构造、trace。
   - 再抽 recovery ownership。
   - 最后处理 ForTesting 注入，改为协议/工厂注入。

3. `ChatViewModel` 巨型方法
   - 先抽纯 reducer/mapper。
   - 保留外部 public API。

4. `ChatUIKitContainerView.swift`
   - 最后拆，因为 UI blast radius 最大。

### D4. 禁止事项

- 不用“顺手重写 UI”伪装重构。
- 不改变 session/turn 状态语义。
- 不删除架构护栏测试。
- 不运行 UI automation，除非明确授权。

---

## 6. 批次 E：工程宪法与 CI 卡口

### E1. 目标

把 AI 多模型轮换最容易造成漂移的点变成仓库规则：

- 日志体系：Swift 统一使用一个 wrapper 或明确 `NSLog`/`os.Logger` 使用边界。
- 本地化：禁止新增硬编码用户可见中文 UI 字符串。
- 测试注入：生产类内 `ForTesting` 只能在明确标注区域出现，新增需说明无法通过协议注入替代。
- 长文件/长方法：先告警，不一开始硬 fail。
- protocol 改动：必须同步 Mac protocol pack、iOS mirror、定向测试、living docs。

### E2. 推荐落地

第一步只加文档和非阻塞检查：

```text
docs/engineering-constitution.md
scripts/check-architecture-hygiene.sh
```

CI 初期设为 warning 或独立手动 job。等现有债务清掉后再改为 required。

### E3. 验收

```bash
scripts/check-architecture-hygiene.sh
```

脚本应输出：

- 当前存量计数。
- 新增违规检查方式。
- 哪些规则只是 warning。

---

## 7. 推荐第一轮执行清单

第一轮只做：

1. **A：Capability 单源化**
2. **B1：切断 `go-bridge` 对 legacy config 包的生产依赖**

不要做：

- B2 删除整个 `config/` 包
- C web shared package
- D god-object 拆分
- E CI hard gate

第一轮完成标准：

- `question_reply` 在 Management API 和 `hello_ack.backends[]` capability 一致。
- `session_pagination` 仍关闭。
- `go-bridge/provider_switch.go` 不再 import `config` 包。
- provider seed 行为保持。
- 现有 `TestBackendList*` / descriptor capability / provider seed 相关测试全部不回归。
- 定向 Go 测试通过。
- `CHANGELOG.md` `[Unreleased]` 追加一节，说明架构治理改了什么、降低了什么风险。

推荐第一轮最终命令：

```bash
go test ./go-bridge -run 'Test.*Capabilit|TestBackendList|TestBuildAgentDescriptor|Test.*Provider|TestProviderSeed' -count=1
go test ./go-bridge/... -count=1
```

如果仅改 Go bridge 且不改 `MacBridge/` Swift 或 runtime build scripts，第一轮不要求自动覆盖安装 `/Applications/CordCodeLink.app`。如果执行 agent 实际改了 `go-bridge/` 生产源码并准备交付可运行 App，则按 [CLAUDE.md](../CLAUDE.md) 完成 Release 构建与覆盖安装；失败必须保留真实错误，不得声称部署成功。

---

## 8. 给评审 agent 的核验清单

评审时优先查这些：

1. 是否真的只有一个 capability 推导来源。
2. `question_reply` 是否同时出现在 `BackendList()` 和 `BuildAgentDescriptor()` 的 Codex app_server 产物里。
3. `session_pagination` 是否被误启用。
4. `go-bridge/provider_switch.go` 是否还 import legacy `config` 包。
5. provider seed TOML 是否覆盖 model/env/codex 子字段。
6. provider seed TOML 是否保留 options string 解码等价性。
7. agent type 不匹配的 project 是否不会被选中。
8. 现有 capability/provider 相关测试是否全部不回归。
9. 是否引入生产 fallback/mock/placeholder。
10. 是否改了 wire protocol 字面量。
11. 是否误跑或要求 UI automation。
12. CHANGELOG 是否记录对外可见的架构治理改动。

---

## 9. 退出条件

本计划不是完成所有架构治理才算结束。第一轮可在 A + B1 完成后交付评审。

若执行过程中发现以下情况，停止扩大范围并回报：

- provider seed 真实 TOML 形状超出 B1 最小结构。
- capability 单源化牵出 iOS 客户端解析差异。
- 删除 config 包会影响仍在使用的迁移/调试工具。
- 任何测试需要 UI automation 或真机才能证明。

---

## 10. 施工前评审建议采纳状态

来源：[2026-07-03-architecture-health-execution-plan-review.md](archive/2026-07-03-architecture-health-execution-plan-review.md)。

### 已采纳

1. **P1-1 验收 filter 空跑风险**：A4 已要求新增至少一个 `TestBuildAgentDescriptor*` 测试，A6 已要求完成报告列出该测试名。
2. **P1-2 现有测试不回归**：A4/A6、B4/B7、第 7 节已把现有 capability/provider 相关测试不回归列为强制验收，并明确 `go test ./go-bridge/... -count=1` 为完成证据。
3. **P2-1 capability 顺序**：A3 已要求单源函数保持现有 `deriveCapabilities()` 追加顺序，并把 `question_reply` 放在 `compression` 之后。
4. **P2-2 TOML options 解码等价性**：B4 已新增 `agent.options.provider` / `agent.options.work_dir` 的 `map[string]any` 解码等价性测试要求。
5. **P2-3 agent type 过滤**：B4 已新增 `agent.type` 不匹配 project 不被选中的测试要求。

### 未采纳

无。本轮评审建议全部采纳；未发现需要拒绝或延后处理的建议。
