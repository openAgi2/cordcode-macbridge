# Codex Desktop：iOS 进入计划模式（另案，未开工）

- 日期：2026-09-04
- 状态：**owner 裁决挂起**——先不做 iOS 端开启 Codex 计划模式；只记录缺口与调研入口
- 已交付面：Mac Codex App 计划模式产出计划 → iPhone 同步正文 + 计划审批卡 → iPhone 点批准开始实施

## 0. 来源清单（记录时）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=1d607601540be755658b7444144af0c2f797e136
未提交状态=codex-remote plan_review 实现与测试（本另案不改代码）
任务预期分支=plan/approval-layer
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-plan-approval
配套分支=plan/approval-layer-ios
配套提交=7468a54d9b7444278fe015f28ceeb3521f988959
上游 Codex=/Users/jacklee/Projects/codex
  分支=main  提交=50fffd5ed367aa99491d9ec58575626fce4e9dd4  干净
预期产品特性=iOS 旁观并批准 Mac 已进入的 Codex Plan；iOS 自己切 Plan 不在本轮
```

## 1. Owner 真机结论（2026-09-04）

| 路径 | 结果 |
| --- | --- |
| Mac Codex App 打开计划模式，发任务 | 弹出计划正文 +「实施此计划？」 |
| iPhone 同步该计划 | 计划审批卡出现，正文可见 |
| iPhone 点批准 | 计划开始执行 |
| iPhone 自己打开 Codex 计划模式 | **做不到**（与 DeepSeek Harness 相同产品形状） |

Owner 裁决：iOS 开启计划模式**先不做**，补文档，后续再调研如何做。

## 2. 当前产品形状（有意保持）

进入计划模式的客户端是 **Mac 上的 Codex App（ChatGPT Desktop）**，不是 iPhone：

1. 用户在 Mac Codex 切到 Plan 协作模式并发送任务；
2. 官方产出 `ThreadItem::Plan`；Desktop TUI 弹出「Implement this plan?」；
3. CordCode 在 `turn/completed` 合成 `permissionKind=plan_review` 卡到 iPhone；
4. iPhone 批准 = 代发官方原文 `"Implement the plan."` + Default `collaborationMode`。

iPhone 输入条没有 Codex Plan / Default 协作模式开关。这与 DeepSeek Harness 一致：
Mac 切「标准」后用 `/plan`（`commands/execute`）进入；iPhone 把 `/plan` 当聊天发出
不会进入计划模式。Grok 的 iOS「Plan」目前也只是 agent 内存，不会启动 grok plan。

## 3. 为什么不是「把 Claude 的 Plan 按钮接到 Codex」

官方 Plan 不是 Claude 那种 session 启动 `--permission-mode plan`，也不是 dsh 的 slash
`/plan`。它是 **collaboration mode**（TUI 本地编排 + app-server 实验字段）：

| 官方锚点（Codex `50fffd5`） | 含义 |
| --- | --- |
| `codex-rs/protocol/src/config_types.rs` `ModeKind` serde `plan` / `default` | 协作模式枚举 |
| `turn/start.collaborationMode`（app-server-protocol v2 `turn.rs`） | 本回合带上 Plan 或 Default |
| `thread/settings/update.collaborationMode` | 改线程当前协作模式 |
| `collaborationMode/list` | 官方预设列表（Plan 预设选 medium effort；内置 developer instructions 用 `settings.developer_instructions: null`） |
| `tui/src/chatwidget/plan_implementation.rs` | 「Implement this plan?」是 TUI 弹窗，不是 wire 审批 |

CordCode `codex-remote` 的普通发送（`session.go` `SendWithOptions`）只传 `threadId` /
`input` / 可选 `model`/`effort`，**不带** `collaborationMode`。批准路径才显式带
Default 模式（`plan_review.go`）。因此 iPhone 在未进入 Plan 的线程上发消息，会按该线程
当前官方模式走——通常是 Default，不会产出 Plan item。

iOS 现有「计划模式」入口（`PlanModeEntryAction`）是为 Claude `permission-mode=plan`
设计的（只能随会话启动/resume 生效）。Codex Remote **不广告** `permission_mode`，
iPhone 不会把 Claude 那套 Plan 按钮画到 Codex Desktop 上。不要把 Claude 入口复用成
Codex 入口。

## 4. 后续调研要回答的问题（未核实，禁止据此编码）

1. **最小官方动作**：iPhone 发下一条消息时在 `turn/start` 带
   `collaborationMode: {mode:"plan", settings:{model, developer_instructions:null}}`
   是否就等于「进入计划模式」？是否还要先 `thread/settings/update`？
2. **UI 面**：Plan / Default 是协作模式选择器，不是权限模式菜单。放在输入条哪一格、
   如何与已有 Claude Plan / dsh 预设区分。
3. **目录**：是否必须先 `collaborationMode/list`，还是硬编码 `plan`/`default` + 当前模型。
4. **双客户端**：iPhone 切 Plan 时 Mac Desktop 的模式指示会不会漂；Desktop 是否另有
   权威模式、会不会把 iPhone 的 collaborationMode 盖掉。
5. **与已交付批准路径的关系**：iPhone 已能在 Mac 进入 Plan 之后批准实施。入口层只解决
   「谁发起计划」，不要改批准代发 `"Implement the plan."` 的语义。
6. **对照**：DeepSeek iOS `/plan` 入口、Grok iOS Plan 不生效——三条应分开设计，不要合成
   一个「全 backend Plan 按钮」。

调研时必须再读目标版本 Codex 源码（本机 `/Users/jacklee/Projects/codex`），不能沿用
本文撰写日的 commit 印象。未取得 `turn/start.collaborationMode` 的真实样本前 fail closed。
