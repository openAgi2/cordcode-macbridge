# Phase 3a（目录方向·iOS 面板入口）实现与测试证据

- iOS 工作树：`/Users/jacklee/Projects/cordcode-ios-plan-approval`（分支 plan/approval-layer-ios，起点 a579cccadfb4b875f96432d7db370b1ae82ba781，未提交仅用户未跟踪 message-web/public/ 未动）
- 方案：Mac 仓 `docs/2026-09-07-grok-build-slash-command-panel-implementation.md` v1.5 §6（面板入口/routing/认领与发送/异步隔离）
- 日期：2026-09-07
- 测试日志：`p3a-test-run.log`（首轮）、`p3a-test-run2.log`（隔离修补后终版，25/25）；真机安装：`p3a-device-install2.log`（构建+安装+启动 iPhone 16 Pro 00008140-001E69503453001C，org.openagi.cordcode）

## 交付物（iOS 文件）

| 文件 | 内容 |
|---|---|
| `Views/Chat/SlashCommandRouting.swift` | `SlashCommandPanelPolicy` per-backend 策略（dsh 白名单/内置回退/预取快照/List 失败 default sink 原规则全保留；Grok 无白名单/无回退/每次打开实拉/失败 fail-closed）；`SlashMenuPullResult`（commands/emptyCatalog/failed/superseded）+ `SlashMenuPulledPlanner` 纯行映射（空表 1 占位行；失败=说明行+重试行）；`SlashMenuStatus`；`AttachMenuDescriptor.Item.Kind` 扩 `.status`/`.retry`；`Section.attachments` 单一真值；`subtitleText`/`isPlaceholder` 扩展；判决器新增 `claimedToken` 重载 + `.claimedCommandUnavailable`（认领失败≠手写未知） |
| `App/ChatInputAccessoryView.swift` | `slashMenuPulledOnOpen` 模式：＋ 菜单命令节换 `UIDeferredMenuElement.uncached`（每次实际打开调宿主拉取，系统 loading；superseded 剩附件节）；compact/展开共用同一 UIMenu；`pullSlashMenuCommands`/`onSlashMenuRetry` 回调；`activeSlashClaimToken`/`currentDraftText` 只读口；拉取模式点选分流不吃内置回退；`menuAction(for:)` 两模式共用 |
| `App/ChatUIKitContainerView.swift` | 门控去 dsh 硬编码 → `supportsSessionCommands` capability + 有会话（Grok readiness 未开时 bridge 不广告，面板诚实缺席）；`SlashCommandScope (backend, sessionId)` + 单调 `slashCommandGeneration` 异步隔离（A→B→A 旧结果丢弃）；菜单拉取 `pullSlashCommandsForMenuOpen`（fenced，空表=emptyCatalog）；发送判决 policy 化（Grok List 失败保留输入+弹错绝不普通发送；dsh default sink 原规则）；认领失败 alert；执行中按 `(scope, name)` 键控（A 执行中不置灰 B 菜单）；完成回调代际 fence + 清理无条件落；`restoreSlashDraftIfUntouched`（用户已继续输入不覆盖） |
| `Resources/{zh-Hans,en}.lproj/Localizable.strings` | 4 键：menu.empty / menu.loadFailed / menu.retry / claimUnavailable |
| `OpenCodeiOSTests/SlashCommandGrokPanelTests.swift`（新，pbxproj 四段登记） | 13 项定向非 UI 单测 |
| `CordCode.xcodeproj/project.pbxproj` | 新测试文件登记 |

## 测试矩阵（25/25 绿 = 新 13 + dsh 原语义回归 12）

新测试：policy per-backend 四差异点；拉取态行映射（commands 执行中同名置灰异名不互斥/空表单占位行/失败两行/superseded 空）；附件节单一真值；认领判决五情形（已知 args-tolerant 执行/目录缺席=认领失败/token 被改回手写判决/无 input 命令认领防御执行/未认领手写未知普通发送）；deferred 菜单模式切换与 compact 共用；claim 生命周期与草稿只读口。
dsh 回归：SlashCommandPanelTests 12/12 原样通过（白名单三条、内置回退、官方 dispatch/matchEnter 镜像、claim 相位、wire 解码、sessionId 透传——原规则零变化）。

## 实现注记

- **Grok 每次打开**＝`UIDeferredMenuElement.uncached`：每次呈现重新调 provider，系统自带 loading；弹出菜单不支持原地刷新 → 重试行 tap 后收起，重开即新拉取（uncached 语义保证），重试动作本身触发宿主后台重拉、失败以 alert 反馈。
- **执行中隔离**：`slashCommandExecutingByScope` 按 scope 键控，显示集合=当前 scope 子集；执行入口先 `syncSlashCommandScope()` 再记账（防上次刷新后会话已切）；完成清理按捕获 scope 无条件落（不被代际 fence 跳过），空集合删键防泄漏。
- **发送路径 fence 语义**：await 后代际已变 → 静默丢弃（不恢复草稿——恢复会把旧会话内容写进新会话输入框）；dsh 同样受 fence（race 改进而非语义变化）。
- run.sh `first_real_device` 的 xctrace 探测对本机失准（iPhone 16 Pro 不匹配其 grep，与既有记忆一致：xctrace 标 Offline 但 devicectl 为准）→ 显式 `--device 00008140-001E69503453001C`。

## 未含 / 后续

- owner 真机手工验收（矩阵 ①②③ 的面板部分）不在本单元 done 范围：安装成功不代替产品行为证据。
- Grok 面板可见前提 = Mac 侧 `session_commands` capability 广告（1a readiness 门：1b+P6 通过前 grokCommandsReady=false → 面板诚实缺席）。本单元 iPhone 侧全部就位。
- Execute 异步终态展示细化 = p3b；模式方向（sessionMode/chip/权限 sessionId）= p3c。
