# 评审报告：`docs/2026-09-22-dsh-web-install-and-start-plan.md`（round 1）

- 日期：2026-09-22
- 结论：**不通过（程序门阻断；设计本体无阻断缺陷）**。修订 v2 后重审。
- 评审性质：只读评审，未修改任何业务代码；本报告是本轮唯一交付物。

## 0. 评审来源清单

```text
Mac 仓=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=8091d7b05e5fe335648b4b1d61a4ff7fadd64db8
未提交状态=10 M + 3 ??：
  M  GO_BRIDGE_ARCHITECTURE.md
  M  agent/opencode-web/events.go
  M  agent/opencode-web/history.go
  M  core/message.go
  M  docs/2026-08-19-dsh-web-canonical-3080-instance-design.md
  M  docs/protocol/schema/bridge-v1.types.ts
  M  go-bridge/events.go
  M  go-bridge/handlers_projection.go
  M  go-bridge/projection_reducer.go
  M  go-bridge/projection_types.go
  ?? agent/opencode-web/parity_turn_summary_test.go
  ?? docs/2026-09-22-dsh-web-install-and-start-plan.md
  ?? go-bridge/projection_reducer_turn_filechanges_test.go
任务预期分支=feat/ios-native-message-timeline（一致）
上游=/Users/jacklee/Projects/deepseek-harness
上游提交=0d1f50007f9bca3f52b06e1c3074fa14d5fb0720（dsh-v0.1.6-alpha.1-5-g0d1f50007f，工作树干净）
iOS 配套=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
iOS 提交=279c9018ec333a3337966ba70f85c6a79ede6f13 [feat/ios-native-message-timeline]
iOS 未提交=timeline 相关文件（AssistantTimelineRenderSpec / ChatMessageGroupModels /
  ChatTimelineAdapterUIKit / NativeTimelineRow / Message / SessionProjection /
  SessionProjectionMapping 等），与 dsh-web status 无关；本评审对 iOS 只读
```

## 1. 阻断发现（v2 必须处理）

### R1-1 来源清单不完整，且与评审范围重叠的未提交修改未纳入来源

方案 §0 的「未提交状态」只列了 6 个路径（agent/opencode-web/events.go、
agent/opencode-web/history.go、core/message.go、go-bridge/events.go、
go-bridge/projection_reducer.go、go-bridge/projection_types.go），实际工作树为
10 M + 3 ??。漏列：`GO_BRIDGE_ARCHITECTURE.md`、
`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、
`docs/protocol/schema/bridge-v1.types.ts`、`go-bridge/handlers_projection.go`，以及两个
未跟踪测试文件。

其中前两个的未提交修改正是本方案自己的「取代说明」（见 R1-2），与评审范围直接重叠。
按本仓 AGENTS.md 评审门——「工作树存在与评审范围重叠的未提交修改，但评审没有把它
纳入来源」→ 评审不得判定通过——本轮不能通过。

**修订要求**：v2 的 §0 逐项列全 10 M + 3 ??，并把两处取代说明显式声明为本变更集的
doc-only 交付物（或按 R1-2 回退）。

### R1-2 取代说明在评审通过前已写入活文档，且被表述为「owner 裁决」

工作树中已存在的两处未提交修改：

- `docs/2026-08-19-dsh-web-canonical-3080-instance-design.md` 头部注记：「本文
  『不代装』和『冷启动缺位则补拉』已被 `docs/2026-09-22-…-plan.md` 取代」。
- `GO_BRIDGE_ARCHITECTURE.md` 座位模型节注记：「2026-09-22 **owner 裁决**尚未落地，
  以 `docs/2026-09-22-…-plan.md` 为准……下文第 3 点在该方案实施前仍是运行中的代码
  行为」。

方案自身 Status 仍是「方案，未实施。评审通过前不改业务代码」。业务代码确实未动
（工作树无任何 dsh-web 相关源码修改，已核对），但两处活文档注记以既成事实的口吻
（「已被取代」「owner 裁决」）在评审前落地；评审无法从仓内证据核实 owner 是否确已
在 2026-09-22 裁决该方向。被取代的是 owner 产品指令（`agent/dsh/discovery.go:3-8`
「MacBridge NEVER installs…」，2026-08-15 指令 v2）与 2026-08-19 设计裁决——按
2026-08-24 事故纪律，改变 owner 裁决必须有可归属的授权记录。

**修订要求**（二选一，按事实落笔）：

1. owner 确已裁决 → v2 在 §0 写明裁决出处（时间、载体、内容），并声明两处注记属于
   本变更集的 doc-only 交付；
2. owner 未裁决 → 回退两处注记，措辞改为「拟由……取代，待评审」，v2 通过后再落地。

## 2. 非阻断发现（v2 建议处理）

### R2-1 `port_conflict` 的只读判定机制未写明（P2）

§3 状态表承诺「座位被非 dsh 占用 → `port_conflict`」，但现有占用原文来自 spawn 错误
（`agent/dsh-web/diagnostics.go:76` 查 `LastSpawnErr` 含 "non-dsh"）；冷启动不再补拉后，
未点「启动」时没有 spawn 错误可查。只读判定需要新机制：TCP 可连但 `host.describe`
不应答，或 lsof（`diagnostics.go:60-62` 注释已有「read-only discrimination is lsof +
the state file + InstanceStatus」的概念）。v2 补一句判定机制与数据来源，避免实施时
各自发挥；不补则该行只能降级为 `service_not_running` + 点启动后浮出真实错误。

### R2-2 §7 缺「诊断只读」回归断言（P2）

§5/§8 把 `RunDiagnostics` 从「Resolve 可能补拉」（`diagnostics.go:60-72`，注释明说
MUTATING）改为只读，但 §7 测试清单没有对应条目。补一条：诊断路径不调用 starter
（连同「重新检查」/冷启动不补拉一起锁死）。

### R2-3 §8 交付清单未列 CHANGELOG（P3）

仓库惯例：对外可见改动完成后在 `CHANGELOG.md` `[Unreleased]` 追加条目。建议列入
§8 第 6 步。

### R2-4 锚点小误（P3）

- 「2026-08-13 起的探测链永不安装」：正式锚点是 2026-08-13 设计
  （`docs/2026-08-13-dsh-driver-design.md`）+ 2026-08-15 owner 产品决策附记 v2
  （`agent/dsh/discovery.go:3` 引的是 2026-08-15）。建议两个日期都写。
- `README.zh.md:26/:29` 行号 ±2（内容核实属实）。

## 3. 已核实的声明（本轮全部通过）

| 方案声明 | 证据 |
| --- | --- |
| 上游提交/描述 | `0d1f50007f…`、`dsh-v0.1.6-alpha.1-5-g0d1f50007f` ✓ |
| README 安装入口是 `npx @deepseek-ai/dsh web`、默认 3080、`--no-open` | README.zh.md「通过 npm 运行」节 ✓ |
| 包名 `@deepseek-ai/dsh`、bin `dsh`、`apps/cli/package.json` 无 engines | apps/cli/package.json ✓ |
| 根 package.json `^22.19.0 \|\| >=24.0.0` 是 monorepo 约束 | package.json ✓ |
| 发布包无 engines（`npm view` 为空）→ 不得用 monorepo 版本拒绝用户 Node | npm view ✓ |
| npm latest=0.1.5-rc.2；本机 `/opt/homebrew/bin/dsh`=0.1.7-alpha.1 | npm view / 实测 ✓（dist-tags：latest 0.1.5-rc.2、alpha 0.1.7-alpha.1、next 0.1.5-rc.3） |
| `dshweb.go:228` InstanceStatus 只回布尔 | `agent/dsh-web/dshweb.go:228-244` ✓ |
| 宽限内禁止 `available=false` | `dshweb.go:224-231` ✓；宽限 120s（`resolver.go:62`）✓ |
| `agent_descriptor.go:271-280` 折叠成 `not_configured` | `go-bridge/agent_descriptor.go:271-281` ✓ |
| 枚举已有 `not_detected`/`service_not_running`（含 `port_conflict`） | `agent_descriptor.go:22-34` ✓ |
| `detectStructuredInstanceReadiness`（`agent_descriptor.go:249`） | `agent_descriptor.go:249-259` ✓；codex-remote 样板 `agent/codex-remote/diagnostics.go:16-35` ✓ |
| `resolver.go:131` 30 秒 boot 超时；`:156-157` argv | `resolver.go:131`、`resolver.go:156-158` ✓ |
| 诊断今天会 Resolve 并可能补拉 | `agent/dsh-web/diagnostics.go:60-84`（60-62 注释明说 MUTATING）✓ |
| `not_configured` → 「未配置」 | `BridgeStatusView.swift:338` ✓ |
| 不可用行只有「重新检查」 | `WorkspaceView.swift:609-617` ✓；按钮样式样板 `:576-602` ✓ |
| 全局文案「未找到」「未运行」 | `Services/Localization.swift:1166-1168` ✓ |
| CLI 搜索路径无 nvm | `RuntimeManager.swift:252-275`（bun/homebrew//usr/local/pnpm/volta/~/.npm-global）✓ |
| `POST /internal/agents/{id}/test` 只重建描述符 | `management_api.go:653-669` ✓（dsh-web 的 InstanceStatus 只读） |
| legacy 永不安装锁 | `agent/dsh/discovery.go:3-8`、`discovery_test.go:290` `TestProbeChainNeverInstalls`（含 grep-lock）✓ |
| 现有冷启动自动补拉 | `resolver.go:10-11`（§3.1 cold start spawns on seat）、`:484/:543` ✓ |

## 4. 方案开放条件的核清（iOS，本评审已完成）

方案 §3 要求「实施前在配套工作树上确认」iOS 不按 status 藏入口。本评审已在配套
工作树（279c9018）只读核清：

- `OpenCodeiOS/Services/Backend/BackendModels.swift:15-40`
  `BackendSwitchAvailability.resolve`：仅当 `status != "available"` 时禁用切换并透传
  `reason`（codexRemote/pairing_required 特判），**不按 status 移除入口**；
- `deepSeekWeb` 是常驻 `BackendKind` 枚举值（`BackendModels.swift:81`），按 kind 匹配，
  与 status 无关；
- `not_detected`/`service_not_running`/`port_conflict` 均为既有 wire 枚举，iOS 保留
  原始 status 值可显示（`BackendModels.swift:486-487`）；
- iOS 既有文案已含「Install dsh (npm i -g @deepseek-ai/dsh)」（`BackendModels.swift:210`），
  与本方案安装路径一致。

结论：状态从 `not_configured` 变为 `not_detected`/`service_not_running` 不会藏 iOS 入口，
方案 §3 的开放条件成立。实施时可直接引用本节；按方案要求在实施门再复核一次亦可。

## 5. 设计质量评价

- **四拍完整**：§2.1/§2.2 各自走完打开→输入→发出去→过程，§2.3/§2.4 覆盖已就绪与
  失败停留；没有「先接通 API、按钮下期再做」的腰斩。
- **无 wire 协议变更**：四个状态全部复用既有枚举；新增两条 management API 是本机
  token 内部接口，不触 bridge-v1；iOS 零改动（且开放条件已核清）。
- **改动面收敛**：冷启动 vs 宽限到期的分流映射 resolver 现有 `everResolved` 状态
  （`resolver.go:481`），不需要新状态机。
- **安全红线保留**：loopback、永不 `--trusted-host`、无 sudo、安装记录 0600 无凭据、
  npm 输出 token 打码、不捆绑 Node。
- **npx 不当「已安装」的判断正确**：npx 缓存不是 `LookPath("dsh")` 稳定可得的二进制。
- **验证分级合规**：§7 按 D2/D3 定向测试 + 一次增量编译，真机安装留给 owner 点一次，
  符合授权边界与构建成本纪律。

## 6. 结论与下一步

不通过。开发者修订 v2：

1. **必须**：处理 R1-1（补全 §0 来源清单）、R1-2（取代说明的归属或回退）；
2. **建议**：R2-1（port_conflict 只读判定机制）、R2-2（诊断只读回归断言）；
3. **可选**：R2-3（CHANGELOG）、R2-4（锚点日期）。

v2 送审时只交付修订后的方案文档；两处活文档注记在评审通过前保持与 R1-2 的处理
一致。设计本体（状态模型、安装/启动流、测试计划）本轮未发现阻断缺陷，v2 无需
重构方向。
