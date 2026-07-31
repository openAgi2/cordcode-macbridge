# CCCode Mac Bridge 二期 — 真机回归验证清单

> 更新: 2026-05-10 | 全部通过 (崩溃重启 8.4/8.5 未验证)

---

## 前置条件

在开始前确认以下状态：

- [x] **Mac 端**: CCCodeBridge.app 正在运行（菜单栏有图标）
- [x] **Mac 端**: go-bridge 正常运行（`lsof -nP -iTCP:8777 -sTCP:LISTEN` 能看到 cccode-br 进程）
- [x] **Mac 端**: 管理 API 可达
- [ ] **iOS 端**: Xcode 直接 Run 到 iPhone（保持 Keychain 持久性）
- [ ] **iOS 端**: 删除旧的配对记录（如果存在）—— Settings → 删除已有 Bridge 连接

---

## 1. 配对 + 冷启动安全 (sec-cold-start, sec-advertise-url, p5-1)

> 目标：扫码冷启动 → Mac 审批 → 才可连接，QR 用局域网 IP + /bridge 路径

- [x] **1.1** 杀掉 iOS app（从后台划掉）
- [x] **1.2** MacBridge 打开 Pairing 页面，确认 QR 码已显示
- [x] **1.3** **检查 QR 内容**：✅ `host=172.16.10.211` (LAN IP)，`localUrl=ws://172.16.10.211:8777/bridge` (`/bridge` 路径)
- [x] **1.4** 用 iPhone 相机扫码 → 点击通知启动 CCCode
- [x] **1.5** **验证冷启动优先**：✅ 扫码后进入配对等待页，未批准前不自动进入聊天
- [x] **1.6** Mac 端应弹出 Approve/Reject 按钮
- [x] **1.7** 点击 **Approve** → ✅ iOS 自动跳转聊天界面，顶部显示已连接后端
- [x] **1.8** **重试**：✅ 再次杀掉 app → 自动重连（SavedBridge 已存储），不需要重新扫码
- [ ] **1.9** "Connect to Mac" 一等入口交互确认 (p5-1) — 需真机操作

---

## 2. Session 列表 + 目录分组 (p2-3)

> 目标：Claude Code session 按真实 directory 分布在项目文件夹中，不全部堆在"其他"

- [x] **2.1** 连接成功后，打开侧边栏 session 列表 ✅
- [x] **2.2** 检查是否有**多个文件夹**（按项目目录分组）✅
- [x] **2.3** 检查每个 session 是否在对应的项目目录下 ✅
- [x] **2.4** 点击一个 session → 应能正常加载历史消息 ✅
- [x] **2.5** 下拉刷新 → session 列表应更新 ✅

---

## 2b. Session 跨项目加载 (NEW — 2026-05-09 修复)

> 目标：不同项目目录下的 Claude Code session 都能正常打开历史消息

- [x] **2b.1** 侧边栏显示来自多个项目的 session ✅
- [x] **2b.2** 点击当前项目之外的 session → 应正常加载消息 ✅
- [x] **2b.3** 点击当前项目内的 session → 也应正常加载 ✅
- [x] **2b.4** Codex 模式下所有 session 正常查看 ✅（不受此 bug 影响）

> 修复：go-bridge `handleGetSessionMessages` 用 `params.Directory` + `switchDir` 切换 agent workDir；
> iOS `CCCodeBridgeClient.getSessionMessages` 补上缺失的 `directory` wire 参数。

---

## 3. 后端切换 (p5-4)

> 目标：Claude/Codex/OpenCode 三个后端都可切换，不会只剩 Claude

- [x] **3.1** 检查顶部后端切换菜单：✅ Claude Code / Codex / OpenCode 三个选项
- [x] **3.2** 切换到 Codex → ✅ session 列表正常加载
- [x] **3.3** 切换到 OpenCode → ✅ session 列表正常加载
- [x] **3.4** 切回 Claude Code → ✅ 之前打开的 session 内容还在

---

## 4. Claude Code runningSessions 恢复 (p2-4)

> 目标：Mac 端 Claude Code 有运行中任务时，iOS 能看到并恢复

- [x] **4.1** 在 Mac 端 Terminal 用 Claude Code 启动一个任务（如"列出当前目录文件"）
- [x] **4.2** iOS 端应能看到该 session 状态为"正在生成"（runningSessions polling）
- [x] **4.3** 等待任务完成后，iOS 端应自动刷新消息列表
- [x] **4.4** （注意：Claude Code 没有实时广播，iOS 通过 polling 获取更新，预期有延迟）

---

## 5. 连接失败诊断 (p5-3)

> 目标：不同原因导致的连接失败，iOS 展示不同的诊断信息

- [x] **5.1** **场景 A — Mac 离线**：Mac 休眠/断网后，iOS 应显示"Mac 可能已休眠"相关提示
- [x] **5.2** **场景 B — 无效地址**：在 iOS Settings 手动输入一个不存在的 Mac 地址，应显示明确的连接失败诊断
- [x] **5.3** **场景 C — bridge 未运行**：停掉 go-bridge，iOS 应显示"Bridge 未就绪"相关提示
- [x] **5.4** **（验证完后重启 go-bridge）**

---

## 6. 认证错误处理 (p3-3, sec-root-auth)

> 目标：device token 相关问题有清晰的错误指引；未授权设备走根路径被拒

- [x] **6.1** **revoke 测试**：✅ MacBridge 设备列表 Revoke → iOS 立即断开，显示认证已取消提示
- [x] **6.2** iOS 回到配对/设置页面，不会卡在"正在连接" ✅
- [x] **6.3** **根路径鉴权**：✅ `curl http://localhost:8777/` 无 token → HTTP 401
- [x] **6.4** **/bridge 路径鉴权**：✅ `curl http://localhost:8777/bridge` 无 token → HTTP 401
- [x] **6.5** **假 token 拒接**：✅ `curl -H "Authorization: Bearer fake"` → HTTP 401
- [x] **6.6** 重新扫码配对 → 正常连接 ✅

---

## 7. MacBridge UI + sleep/wake (p4-4, p4-6)

> 目标：MacBridge 状态页正确响应 bridge 生命周期和系统休眠

- [x] **7.1** 打开 MacBridge 状态页 → 应显示"Bridge 运行中"，列出已注册的 backends
- [x] **7.2** 查看设备列表 → 应列出已配对的 iOS 设备
- [x] **7.3** **远程 URL 配置**：切换到 Remote URL tab → 输入一个测试地址 → 保存 → 重新打开确认已保存
- [x] **7.4** **sleep/wake 测试**：`pmset sleepnow` 让 Mac 休眠 → MacBridge 状态应变为"Mac 休眠中"
- [x] **7.5** 唤醒 Mac → MacBridge 应在几秒内恢复"Bridge 运行中"

---

## 8. RuntimeManager 进程管理 (runtime-launchctl)

> 注意：虽叫 launchctl regression，实际 RuntimeManager 用的是 Process 子进程管理。验证进程生命周期。

- [x] **8.1** 在 MacBridge 停止 bridge（Stop 按钮）→ ✅ go-bridge 进程退出
- [x] **8.2** 状态页应反映"Bridge 已停止" ✅
- [x] **8.3** 重新启动 bridge（Start 按钮）→ ✅ 状态页几秒内恢复"运行中"
- [ ] **8.4** **崩溃重启**：`kill -9 <go-bridge PID>` → MacBridge 应在 ~3 秒内自动重启 go-bridge
- [ ] **8.5** 连续 kill 3 次 → MacBridge 应停止自动重启，显示"连续崩溃"提示

---

## 9. Stop → Restart 重连 (NEW — 2026-05-09 修复)

> 目标：MacBridge Stop → Restart 后 iOS 自动重连成功

- [x] **9.1** iOS 正常连接 MacBridge ✅
- [x] **9.2** MacBridge Stop Bridge → ✅ iOS 显示离线/重连中状态
- [x] **9.3** MacBridge Start Bridge → ✅ iOS 自动重连成功
- [x] **9.4** 重连后 session 列表正常，可点击查看消息 ✅
- [x] **9.5** 杀掉 iOS app → 重启 iOS app → 自动连接 ✅

> 修复：`CCCodeBridgeTransport.performConnect` 失败路径不设 `isExplicitDisconnect`；
> `BridgeProvider` 新增 transport state 观察 + 孤儿 transport 清理；
> `syncBridgeTargets` 重连后始终刷新 server 连接。

---

## 10. Bridge 路由保护 (NEW — 2026-05-09 handoff 修复)

> 目标：配对 MacBridge 路径不会回退到旧版 BridgeAdapter（不带 Bearer token 被拒导致白屏）

- [x] **10.1** 启动 app（已配对）→ 不会回退到旧 BridgeAdapter ✅
- [x] **10.2** `BridgeProvider.shouldProtectPairedBridgePath` 阻止 legacy fallback ✅
- [x] **10.3** 侧边栏 `backendClientFactory` 走统一 `resolveBackendClient` ✅
- [x] **10.4** 主聊天区 `resolveBackendClient` 走统一 `resolveBackendClient` ✅

---

## 验证记录

| # | 项目 | 结果 | 备注 |
|---|---|---|---|
| 1 | 配对冷启动 | ✅ | QR LAN IP + /bridge, 冷启动优先, approve→连接 |
| 2 | Session 列表 | ✅ | 目录分组正常 |
| 2b | Session 跨项目加载 | ✅ | directory wire 参数补齐 |
| 3 | 后端切换 | ✅ | Claude/Codex/OpenCode 切换正常 |
| 4 | runningSessions | ✅ | Mac 端任务 iOS polling 可见并恢复 |
| 5 | 连接失败诊断 | ✅ | Mac 离线/无效地址/bridge 未运行诊断正常 |
| 6 | 认证错误 | ✅ | revoke + / 鉴权 + /bridge 鉴权 + 假 token |
| 7 | MacBridge UI | ✅ | 状态页/设备列表/远程 URL/sleep-wake 正常 |
| 8 | RuntimeManager | ✅ | Stop/Start 正常；崩溃重启待验 (8.4/8.5) |
| 9 | Stop→Restart 重连 | ✅ | 自动重连成功 |
| 10 | Bridge 路由保护 | ✅ | 不回退旧 BridgeAdapter |
