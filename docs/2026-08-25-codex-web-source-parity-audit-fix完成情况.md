# codex-web 源码对齐审计修复完成情况

- 日期：2026-08-25 起
- 驱动：owner 监工指令（codex-web 源码对齐审计修复）
- 审计文档（豁免卡登记簿 + 事实基准）：[2026-08-25-codex-web-source-parity-audit.md](2026-08-25-codex-web-source-parity-audit.md)
- 纪律：每个修复先在本文档写明「官方实现位置 + 我方实现的第一处分歧」再动代码（设计 §3.4）；代码注释带上游锚点或豁免卡编号；每批定向测试 + go test ./... + go vet 通过后进下一批。

## 批次 1：收口对称性与小修

### 1-首笔 A1/B1：InteractionRegistry 移植声明 + history 有界化 + 不变量测试

- **官方实现位置**：`tui/src/app/app_server_requests.rs:74-360`（PendingAppServerRequests：分类型 HashMap :74-80、会话重置 clear() :89、note_server_request :97、take_resolution :201、resolve_notification :305）+ 收口唯一路径 `ServerRequestResolved`（`app_server_events.rs:118-142`）。
- **我方实现的第一处分歧**：官方是**单连接单视角**——resolve_notification 一次 dismiss、会话重置 clear() 整体清空；本仓是**长驻 bridge + 双泵（主/观察连接）两视角**——每泵须各发一次收口事件（kernel 幂等）、`resolvedByRequest` 为晚到第二泵保留归属、`DropEpoch` 承担官方 clear() 的断线等价物。次级分歧：官方 TUI 进程生命周期短且 clear() 兜底，本仓进程长驻而 `history` map 只增不减（进程生命周期无界）。
- **处置**：
  1. interactions.go 文件头补移植母本声明 + 上述差异清单；
  2. `history` 有界化（与 `resolvedByRequest` 同策略：1024 全清）；
  3. clear() 语义对照审查落注释（DropEpoch = 断线版 clear；官方重发是重 surface 的唯一真相，重连后 Register 刷新 epoch）；
  4. 不变量测试补齐（B1 卡待补项）：permission kind 每 epoch 恰一次、第二泵经 resolvedByRequest 归属发自己那份、DropEpoch 后死 epoch 无 pending 可归属不产出；
  5. resolvedEvents/registry 注释补 B1 豁免卡引用。

### 1a A2：permission 乐观收口清算

- **官方实现位置**：TUI 收口唯一路径 = `ServerRequestResolved` 通知（`app_server_events.rs:118-142`）；发送应答后不本地关闭。
- **我方实现的第一处分歧**：`go-bridge/handlers.go` `handleResolvePermission`（:4083-4096）在 RespondPermission 成功后**立即乐观 publish `permission_resolved`**。git 取证（`git log -L`）：该块由 **630fb8d（fix(dsh-web): 审批/问答同步到 iPhone）** 引入——它是 **dsh-web 的产品缺口修复**（dsh 宿主无官方 resolved 广播到达 SSV2 投影），不是 codex-web 发明；但 handler 为多 backend 共享，codex-web 会连带吃到本地乐观收口，违反「官方 resolved 是唯一收口真相」。
- **复现与修复设计**：codex-web 的官方路径与 user_input 完全同构（202b41c 后 `interactions.go resolvedEvents` 对 permission kind 双泵产出 `EventPermissionResolved` → `go-bridge/events.go:197` 映射为同名 `permission_resolved` logical event → reducer 收卡，与乐观 publish 产物同构）。复现测试将证明：跳过乐观 publish 后，仅靠官方 per-pump 事件投影卡即收口。
- **处置**：引入 `core.OfficialResolutionSource` 标记接口（codex-web Agent/agentSession 实现）——handler 对标记 backend 不做本地乐观收口；dsh-web/opencode-web 等无官方广播的 backend 保留原行为并补声明注释。若真机复测发现官方路径覆盖不了某场景，按 B 类登记豁免卡（不允许无声明保留）。
- **验收**：允许/拒绝后卡片立即收口（owner 真机）；测试断言收口事件源 = serverRequest/resolved 驱动（非 handler 本地 publish）。

### 1c C2：plan 合成终态（待实施时填写分歧分析）

### 1d B5：turn/completed 缺 turn.id 归属（待实施时填写分歧分析）

## 批次 2：A3 官方算法回迁（待批次 1 完成后填写）

## 批次 3：C1 冷用量加固（待填写）

## 批次 4：B3/B4（待填写）

## 批次 5：流程项（待填写）

## 批次 6：iOS 抽查（待填写）
