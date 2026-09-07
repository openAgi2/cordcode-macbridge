# Phase 3c（模式方向·iOS）禁用+诊断消费证据

- iOS 工作树：plan/approval-layer-ios @ 27e8b879（p3a 后 fd1e3cbb 之上；用户未跟踪 message-web/public/ 未动）
- 方案 §6 状态投影/chip 行；口径与 p2 一致：**只读诊断交付，模式切换未交付（P7 阻断）**
- 测试日志：`p3c-test-run.log`（SessionModeProjection 5 + GrokPanel 13 + Panel 12 = 30/30，TEST SUCCEEDED）；真机安装：`p3c-device-install.log`（iPhone 16 Pro 构建成功→安装→启动）

## 交付物（iOS 文件）

| 文件 | 内容 |
|---|---|
| `Models/SessionProjection.swift` | `SessionModeView{status,mode?,canSet,reason?}`（形状镜像 Mac go-bridge）；`SessionModeChipStyle`（planActive/planInactive/pending/unknown）+ `chipStyle` 纯映射（default 也可见；不借 dsh pending 反转）；投影/patch additive `sessionMode` 字段 + `withSessionMode` |
| `Models/ProjectionStore.swift` | `applyingPatch`：present replaces / absent keeps |
| `App/ChatInputAccessoryView.swift` | `updateSessionModeChip(_:)` 三态呈现（planActive 橙色无 × 角标=只读；其余灰阶+后缀 …/?）；与 dsh planMode chip 共用物理 chip、可见性取并集；Grok 交付期 canSet=false → 恒 disabled（alpha 0.6）；dsh 可点公式零变化 |
| `App/ChatUIKitContainerView.swift` | `refreshSessionModeChip()`（grok 投影驱动；切会话/无投影→chip 缺席；按 sessionId 键控天然隔离）挂到三处刷新触发（$messages + sessionProjectionDidChange + 面板刷新）；`exitPlanModeFromChip` grok 早退（不可点绝不借 host 命令通道伪造切换） |
| 测试 | `OpenCodeiOSTests/SessionModeProjectionTests.swift`（5 项，pbxproj 登记） |

## 权限 sessionId 全链——如实暂缓

方案 §6 权限读取/模式写响应两行以「后端实现 per-session 权限面」为前提。p2 交付中 Grok 已明确无 ModeSwitcher（两 RPC 对 grok not_supported），无后端可消费 sessionId 参数与 appliesTo: current_session——iOS 侧先行铺设属于为不存在的后端造线。暂缓并在此记录；后端实现时按方案 §5.1 补全链（协议、conformer、解码、调用者）。

## 测试矩阵（5 项）

snapshot/patch sessionMode wire 解码（含 unknown 无 mode、canSet=false 透传）；patch 合并 present-replaces/absent-keeps + syncRev 推进；chip 三态映射四情形；chip 呈现（无投影隐藏/default 与 plan 与 pending 与 unknown 全可见/恒 disabled/nil 回缺席——dsh 公式做不到的 default 可见被锁定）；dsh planMode 路径回归（可点性/清除不变）。

注：可见性断言走 compact chip（布局无关）；展开态 chip 需 expanded 输入态，非 UI 单测环境恒隐藏是既有布局语义。
