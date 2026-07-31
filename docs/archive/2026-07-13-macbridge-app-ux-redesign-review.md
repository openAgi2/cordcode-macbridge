# CordCode Link macOS App UX 重设计方案 — 评审报告

日期：2026-07-13
评审对象：[docs/2026-07-13-macbridge-app-ux-redesign-report.md](../2026-07-13-macbridge-app-ux-redesign-report.md)
评审依据：本仓活文档（`BUILD_INSTALL_AND_RUNTIME.md`、`GO_BRIDGE_ARCHITECTURE.md`）、
`MacBridge/` SwiftUI 源码现状、`../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`、双仓 `think.md` 复盘。

## 评审结论（TL;DR）

**建议采纳，作为产品级重设计基线。** 方案的核心定调（把"runtime 控制台"
重塑为"安全工作站"）、信息架构收敛（主导航只保留"工作站 / 设备"、连接与
诊断改为一跳可达的按需界面）、渐进披露原则、分期实施策略，与当前架构、
协议和已有复盘结论**均不冲突**，且与代码现状已有相当程度的契合。

但有 **4 个必须在动工前澄清的事实问题**、**2 个需补强的工程边界**、
**1 处活文档需同步的滞后**。本评审不否定方案，而是给出实现期必须先对齐的
清单，避免重设计落地时把"现状本来已经对的地方"误判成"要新造的"。

| 维度 | 评价 |
| --- | --- |
| 产品定调与信息架构 | ✅ 正确，与活文档一致 |
| 与协议/配对/Relay 约束 | ✅ 无冲突 |
| 对"现状"描述的准确性 | ⚠️ 3 处偏负面/不准，需修正（见 §2） |
| 工程分期可行性 | ✅ P0 可独立交付，风险可控 |
| 数据归属与重构边界 | ⚠️ 漏列一处状态迁移（见 §3） |
| 伴生活文档同步 | ⚠️ backend 列表滞后（见 §4） |

## 1. 方案做对的地方（应予坚持）

1. **核心定调准确**。CordCode Link 是"让 iPhone/iPad 安全接入 Mac 上 AI 编程工具
   的工作站"，而非 runtime 控制台。这与 `AGENTS.md` / `GO_BRIDGE_ARCHITECTURE.md`
   的组件边界（SwiftUI app 是 supervisor + 用户界面，runtime 负责协议）一致。把
   "总览/设备/远程访问/设置/诊断"五个并列技术页降权为"工作站 / 设备"两个日常入口，
   方向正确。

2. **Relay 默认、LAN 自动、Tailscale/自定义归高级**的连接呈现策略，与活文档
   `BUILD_INSTALL_AND_RUNTIME.md` 与 `AGENTS.md` 的"三条远程连接路径与 TLS pin"
   表完全吻合（Relay=默认、LAN=自动加速、Tailscale=隐藏备选需 pin）。方案
   "不让用户在互斥模式间选择"准确反映了 LAN 与 Relay 本就不互斥的产品语义。

3. **不改变运行链路的承诺**（方案 L214："不能借机改变 Relay 默认、配对安全、
   backend 运行模型或诊断的真实语义"）与 `GO_BRIDGE_ARCHITECTURE.md` §"已知风险
   与不可破坏约束"一致——本次属 UI/UX 重构，不动 wire/pairing/capability。

4. **配对三段轨迹**"扫描二维码 → 在 Mac 上确认 → 开始使用"与
   `IOS_MAC_INTERACTION_FLOW.md`（L42-72）的 claim → approve/reject →
   pairing_complete 协议三段**一一对应**；方案明确"不能要求用户先理解 claimed"
   与协议把 claim 仅作为内部动词的做法一致。

5. **撤销走行尾菜单 + 确认框，并说明结果与恢复路径**（方案 L128/L56）与协议
   撤销语义一致：Mac 撤销后下发 `device_revoked` + 强制断连 + iOS 回配对入口
   （`IOS_MAC_INTERACTION_FLOW.md` L278-288）。可据此撰写确认文案。

6. **诊断摘要优先、原始日志降为按需**（方案 L159-163）与 `GO_BRIDGE_ARCHITECTURE.md`
   "Bridge ready 与 backend available 是两个维度"的事实吻合——摘要正是要呈现
   "runtime ready 但单个 backend 未就绪"这种分维状态。

## 2. 对"现状"描述需修正的 3 处（动工前澄清）

方案在"现状审视"中有 3 处描述把现状写得比实际更负面或更复杂，会误导
实现期的工作量判断。建议修正。

### 2.1 DiagnosticsView 当前并不存在（诊断是 ContentView 内联）

- **方案 L19/L161 假设**：存在独立 `DiagnosticsView` 文件 / 诊断页。
- **代码事实**：全仓**没有** `DiagnosticsView.swift`，也没有 `DiagnosticsView` 类型
  （grep 0 命中）。诊断是一级 Sidebar `NavigationTab.diagnostics`
  （`ContentView.swift:11`）选中后渲染的内联 `logsTab`
  （`ContentView.swift:285-347`），直接 `readTailLines(... maxLines: 200)`
  铺出原始日志行。
- **影响**：方案 L215"组件按产品区域拆分（…`DiagnosticsSheet`…）"成立，但
  P0/P2 实施时这不是"改造现有 DiagnosticsView"，而是**从 ContentView 内联代码
  中抽离**成独立组件。工作量与心智模型不同，需在 P2 任务里写清楚。

### 2.2 设备页 QR 配对区已是"按需触发"，并非常驻

- **方案 L36/L128 暗示**：配对 QR 区常驻、需要"穿过 QR 区才看到设备"。
- **代码事实**：`PairingView.swift:34-90` 按 `uiState` 分支渲染。`.idle` 态只显示
  一个 `pairNewDevice` 按钮（`.borderedProminent`），点击后才进入
  `.waitingForClaim` 显示 QR（`qrSection`，L107-151）。配对成功/过期/被拒切到
  各自结果视图。**QR 区不是常驻**。
- **影响**：方案"把配对改成任务式工作区 / Sheet"的方向正确，但现状距离目标
  比方案描述的更近——P1 主要是把"按钮触发 → 同页 QR → claim 审批"重构为
  "Sheet + 步骤轨迹"，而非"从常驻 QR 改成任务流"。这降低了 P1 风险。

### 2.3 设备撤销已走二次确认，红色按钮非"直触"

- **方案 L128/L37 暗示**：撤销按钮"主页面红色按钮泛滥"，需改为行尾菜单 + 确认。
- **代码事实**：设备列表"撤销授权"按钮（`ContentView.swift:271-274`）点击后
  触发 `confirmationDialog`（L217-230），其中才有 `role: .destructive`（红色）
  的撤销项 + 取消。**列表上的按钮是默认样式，红色撤销藏在确认弹窗里**。
- **影响**：方案目标（行尾 `…` 菜单 + 确认说明结果）合理，但"现状是红色按钮泛滥"
  不准确。实现期应表述为"把当前"列表按钮 + 弹窗"改为"行尾菜单 + 弹窗（含结果/
  恢复文案）"，是文案与按钮位置优化，不是安全行为的根本改动。

## 3. 数据归属与重构边界（方案漏列，需补强）

### 3.1 BridgeStatusViewModel 不持有设备列表与日志

- **方案 L194（P0 验收）说**："保持现有 `RuntimeManager`、Management API、
  `BridgeStatusViewModel` 与配对协议不变。"
- **代码事实**：`BridgeStatusViewModel`（`BridgeStatusViewModel.swift:7-14`）
  持有 `status / agents / relayConfigured / overviewDataError` 等。但
  **设备列表 `devices: [TrustedDevice]` 与日志 `logs: [String]` 是 `ContentView`
  的 `@State`**（`ContentView.swift:42-45`），不在该 VM。
- **影响**：P0/P2 把"工作站页（含设备概览行）"和"诊断摘要"拆成独立组件时，
  设备列表与日志的**状态归属必须一并迁移**（迁入各自 VM 或共享 store），否则
  会出现"ViewModel 不变、但组件拆不出来"的死结。这是方案唯一遗漏的工程边界，
  建议在 P0 任务清单显式增加："迁移 ContentView 的 `devices` / `logs` @State 到
  对应组件的 ViewModel，保持数据源（Management API）不变。"

### 3.2 PageContainer 的 820pt 硬约束波及远程访问页

- **方案 L39/L181 说**：现状内容固定在约 820pt 窄列，重设计应放宽到 880pt、
  宽屏才加次级栏。
- **代码事实**：`PageContainer.swift:25-32` 硬编码 `maxWidth: 820`；
  `ContentView.swift:61` 整窗 `minWidth: 820`。**关键**：`RemoteAccessView.swift:124`
  也走 `PageContainer(scrolls: false)`，即"左栏 + 右栏"二级导航实际被压在 820pt 内。
- **影响**：P1 把远程访问页改成"连接状态 Sheet"时，若要给高级展开区留出宽度，
  **不能只改 RemoteAccessView**，必须先解除 PageContainer 的 820 硬编码或让该 Sheet
  不走 PageContainer。建议 P0 就调整 PageContainer（改为可配置 maxWidth 或
  移除），否则 P1 会被这个隐式约束卡住。

## 4. 伴生活文档需同步（backend 列表滞后）

- **方案 L85/L94 列出 4 个工具**：Claude Code、Codex、Grok Build、OpenCode。
  这与代码一致——`RuntimeManager.swift:69` 默认
  `drivers: ["claude", "opencode", "codex", "grokbuild"]`，driver id 为
  `grokbuild`（`agent/grokbuild/grokbuild.go:45,97`），displayName "Grok Build"。
- **活文档滞后**：`BUILD_INSTALL_AND_RUNTIME.md:31` 写"当前正式 runtime 注册
  `claude,opencode,codex`"，L45 的 `-drivers` 默认值表也只列三者；`GO_BRIDGE_ARCHITECTURE.md`
  未提及 grokbuild agent。
- **影响**：这是文档与代码不一致，与 UX 方案无关。但方案首屏 mockup（L91-95）
  展示四行 AI 工具健康摘要，实现期 `hello_ack.backends[]` 会真实返回 4 个 backend，
  若活文档未同步，后人会怀疑方案 mockup 写错。建议**顺手在本次 UX 重设计交付时同步
  两份活文档的 backend 列表**（Grok Build 已通过 commit `e4f7efd` 引入，think.md
  L324-351 记录了其 5 个 bug 已修复，属已纳入产品态的 backend）。

## 5. 实施分期评估

| 阶段 | 评估 | 关键风险 / 补强 |
| --- | --- | --- |
| **P0 结构与首屏** | ✅ 可独立交付。重组 Sidebar + 新建 `WorkspaceView` + 迁入原生 Settings scene，均不触碰协议与 runtime。 | 必须同时：① 迁移 ContentView 的 `devices`/`logs` @State（§3.1）；② 调整 PageContainer 的 820 硬编码（§3.2）。 |
| **P1 设备与连接收口** | ✅ 风险低于方案描述。配对已是状态机（§2.2），P1 是"重构为 Sheet + 步骤轨迹 + 文案"，不是从零造任务流。 | 连接状态 Sheet 宽度受 PageContainer 约束（§3.2）；移除"自动/其他连接"左栏二级导航时注意 `selectedMethod` 状态机的归并。 |
| **P2 设置、诊断、抛光** | ✅ 方向正确。OpenCode source 默认 `.managedLocal`（`OpenCodeEndpointResolver.swift:10-29`、`SettingsViewModel.swift:22`）与方案"默认托管无需配置"一致；诊断摘要优先与 backend 分维状态事实一致。 | DiagnosticsView 是**从 ContentView 内联抽离**而非改造现有文件（§2.1）；legacy 64667 不作普通可见项已与代码默认值一致，按方案 L156 处理即可。 |

分期顺序合理：P0 先建立骨架且不改运行链路，P1/P2 在稳定骨架上收口。验收标准
（"10 秒 / 3 秒"判据）清晰可测。

## 6. 与已知复盘结论的交叉核对（无冲突确认）

| 复盘来源 | 是否构成约束 | 结论 |
| --- | --- | --- |
| macbridge `think.md` | 否 | 全文是 runtime/数据层 bug 复盘，无 IA / 导航 / 诊断页产品结论。 |
| ios `think.md` | 否 | 全是 iOS 侧渲染/状态机复盘，不约束 macOS app。 |
| `IOS_MAC_INTERACTION_FLOW.md` | 否（已对齐） | 配对三段、hello_ack 字段、撤销 `device_revoked` 均与方案一致（§1.4-1.5）。 |
| `GO_BRIDGE_ARCHITECTURE.md` 不可破坏约束 | 否（方案已承诺不改语义） | 方案明确不触碰 Relay 默认/配对安全/backend 模型/诊断真实语义。 |

唯一需在实现期另行核实的小事实：方案 L128 提到 "claimed" 状态，交互流程文档
仅把 claim 作为动词（approve/reject + pairing_complete 终态）。若实现要把
"等待确认"映射成具体 UI 状态名，以 `PairingView` 现有 `uiState`
（`.waitingForClaim` 等）为准，不要凭方案文案臆造协议字符串。

## 7. 给实现期的最小 checklist

动工前必须先做（不阻塞方案采纳，但阻塞 P0 编码）：

1. **同步活文档 backend 列表**：`BUILD_INSTALL_AND_RUNTIME.md:31,45` 与
   `GO_BRIDGE_ARCHITECTURE.md` 补入 `grokbuild`（§4）。
2. **确认 `PairingView.uiState` 枚举全集**：作为"任务式配对工作区"状态映射的
   真值来源，避免凭方案 mockup 臆造状态（§6 末）。
3. **列出 ContentView 待迁移的 @State**：`devices`、`logs`、以及配对/撤销相关
   `@State`，明确各自迁入哪个新组件的 VM（§3.1）。
4. **决定 PageContainer 处置**：放宽 maxWidth / 可配置 / 移除，并在 P0 完成
   （§3.2）。

实现期合规（沿用 AGENTS.md 既有约束，本评审不展开）：

- P0-P2 完成后做 macOS 定向 build 与 Swift 单元测试；UI 自动化 / snapshot /
  simulator automation / 真机 UI 验收须先取得 owner 授权。
- 不新增 mock/fallback 隐藏真实状态；Preview 用隔离 fixture，真实 UI 消费现有
  ViewModel 状态。
- 本轮是 UI 重构，**不改** `RuntimeManager`、Management API、配对协议、Relay、
  backend capability 推导。

## 8. 一句话总结

方案方向正确、与架构无冲突、分期可行；落地前修正 3 处现状描述、补 1 处状态迁移、
同步 1 处活文档滞后，即可作为下一轮实现的产品设计基线。
