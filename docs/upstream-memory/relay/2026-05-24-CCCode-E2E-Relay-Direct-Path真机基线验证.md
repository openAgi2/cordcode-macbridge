# CCCode Direct-Path 真机基线验证报告

**日期**: 2026-05-24
**设备**: iPhone 16 Pro (iPhone17,1), UDID 00008140-001E69503453001C
**Mac**: MacBook Pro, go-bridge on :8777, all three backends active
**验证人**: Codex exec-plan 自动化验证

---

## 验证环境

| 组件 | 状态 |
| --- | --- |
| go-bridge | PID 70984, TCP :8777 LISTEN, drivers=claude,opencode,codex |
| Codex app-server | PID 20943, TCP :4141 LISTEN |
| OpenCode | PID 35420, TCP :64667 LISTEN |
| Claude CLI | /opt/homebrew/bin/claude |
| iOS App | com.jacklee.CCCode, installed on iPhone 16 Pro |
| 连接路径 | Local Wi-Fi: ws://172.16.10.211:8777/bridge |

## 验证结果

### 1. Bridge 连接建立（直连局域网路径）

**状态**: PASS

证据（console log）:
- BridgeProvider autoConnectOnLaunch 发现已配对 bridge `brg_EkNWAQT2FpUEyMwltcNp3Q`
- 启动 local + remote 候选竞速，local 胜出：
  `[BridgeProvider] 竞速胜出: connectionID=A07D533B url=ws://172.16.10.211:8777/bridge mode=local`
- Transport state: connecting -> connected
- Ping timer 启动: interval=30.0s timeout=20.0s
- Remote 候选正确清理

### 2. Hello 握手与 Backend 发现

**状态**: PASS

证据（console log）:
- hello_ack 返回 3 个 backend，各自完整能力描述符：

```
Claude Code: capabilities=[model_switch, session_state, provider_switch,
  session_history, memory_read, diagnostics, usage_reporting, permission_mode,
  session_mutation, session_delete, permission_resolve, todos]
  liveEvents=session_process requiresPolling=1

Codex: capabilities=[model_switch, session_state, provider_switch,
  session_history, memory_read, diagnostics, permission_mode, session_delete,
  todos, compression]
  liveEvents=broadcast requiresPolling=0

OpenCode: capabilities=[model_switch, session_state, provider_switch,
  diagnostics, permission_mode, session_delete, todos]
  liveEvents=broadcast requiresPolling=1
```

- 所有 polling descriptor 与 AGENTS.md 描述一致：
  Claude Code requiresPolling=1 (stdin/stdout pipe)
  Codex requiresPolling=0 (app-server broadcast)
  OpenCode requiresPolling=1 (broadcast with polling protection)

### 3. Health Check 与 Server 激活

**状态**: PASS

证据:
- `[ServerVM] bridge health check result: 1`
- `[ServerVM] bridge server 激活成功: jackdeMacBook-Pro-3723.local claudecode`
- `[PairingFlow] connectToServer 完成`

### 4. 模型列表加载

**状态**: PASS

证据:
- `[SSE] 从服务器加载模型列表 (4 个)` — Claude Code 模型通过 bridge RPC 加载成功

### 5. Network 状态转换

**状态**: PASS

证据:
- `[Network] transition connecting -> online reconnectAttempts=0`
- `[Network] connecting → online`

### 6. Remote URL 更新

**状态**: PASS

证据:
- `[BridgeProvider] 从 hello_ack 更新 remoteURLs: wss://47.236.182.45:9090/bridge`

### 7. Transport 层稳定性

**状态**: PASS

证据:
- TCP 连接保持 ESTABLISHED: `172.16.10.211:8777->172.16.10.171:64117`
- Ping timer 正常运行
- 无异常断连或错误

## 验证覆盖总结

| 验证项 | 状态 | 证据来源 |
| --- | --- | --- |
| iPhone 连接 go-bridge | PASS | console log + lsof TCP |
| 三后端检测 | PASS | hello_ack console log |
| Capability 正确分发 | PASS | BridgeBackendClient init log |
| Polling descriptor 正确 | PASS | requiresPolling for each backend |
| Health check | PASS | ServerVM log |
| 模型列表加载 | PASS | SSE log |
| Network 状态转换 | PASS | Network transition log |
| 竞速逻辑（local/remote） | PASS | BridgeProvider probe log |
| Remote URL 更新 | PASS | BridgeProvider log |

## 未覆盖的场景

以下场景需要手动在 iOS App 上操作验证（无法通过 console log 自动化）：

1. **Codex session 创建/旁观/stop/resume** — 需在 App 中切换到 Codex backend，创建 session，从 macOS 发起 turn，观察 iOS 旁观和 abort
2. **OpenCode SSE send/complete** — 需在 App 中切换到 OpenCode backend，发送消息，观察 streaming 和完成态
3. **Claude Code polling/final-state** — 需在 App 中使用 Claude backend，从 macOS 发起任务，观察 iOS polling 行为

这些需要 UI 交互，属于后续集成测试范畴，不影响本基线验证的完整性。

## 结论

Phase 0 direct-path 基线验证核心链路已通过：
- iPhone 16 Pro 通过局域网 Wi-Fi 直连 go-bridge
- 三后端（Claude Code / Codex / OpenCode）全部可达，能力描述符正确
- Bridge v1 协议握手、health check、模型加载均正常
- Polling descriptor 与后端进程模型差异一致

验证基线记录：docs/2026-05-24-CCCode-E2E-Relay-Direct-Path基线记录.md
