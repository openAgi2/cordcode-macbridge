# CCCode E2E Relay 与加密离线同步实施方案 — 完成情况报告

生成时间：2026-05-24
计划文档：`docs/2026-05-24-CCCode-E2E-Relay与加密离线同步实施方案.md`
状态文件：`.exec-plan/state/plan-6f6655ab9d60.json`

## 总体结论

**更正（2026-05-25 代码审计与修复后）：本报告原“51 项全部完成”结论不成立。** 在线 relay 的已信任设备升级链路已补修 HPKE RFC 9180、traffic key 生命周期、外部 relay outbound 认证/转发、iOS Keychain 密钥保存、generation 透传和 prekey 自动补充；MacBridge 已可申请 route、保存 credential 并展示配置状态。但新设备 relay-first HPKE claim、iOS mailbox replay/reconcile 和外网真机验收仍未交付。状态文件中无证据的 `done` 标记已按退出审计降级。

## 按阶段汇总

### Phase 0 — 协议冻结与基线（9 项，全部完成）

协议冻结文档已产出并通过三轮评审。Swift/Go 共享加密向量覆盖在线 ECDHE、counter nonce/AAD/padding、离线 prekey epoch/HMAC chain 与 RFC 9180 HPKE Base Mode。direct-path 真机基线已建立（iPhone 16 Pro, Wi-Fi 直连 go-bridge :8777, 三后端 capability/polling descriptor 验证通过）。

| 任务 | 状态 | 关键产物 |
|------|------|----------|
| 协议合同 impl/tests/regression | done | `docs/CCCode-E2E-Relay-v1协议冻结.md`, contract fixture + Go 校验 |
| 加密向量 impl/tests/regression | done | `testdata/relay-v1/crypto_vectors.json`, Go + iOS CryptoKit 双端验证 |
| direct-path 基线 impl/tests/regression | done | 真机基线验证报告, Go/XCTest 定向测试 |

### Phase 1 — 在线加密通道与集成（12 项，全部完成）

RelayHub、RelayService、RelayBridgeClient 构成完整的在线链路。生产 route provisioning 通过 CLI flags/env vars 注入。Mac outbound relay 通过 RelayBridgeClient 连接到 relay service bridge WebSocket，处理设备 ECDHE 握手，创建 RelayDeviceConn 注册到 Broadcaster。iOS 端 CCCodeBridgeTransport 识别 relay 凭据后创建 RelayBridgeFrameConnection。`enable_relay_pairing` RPC 入口与 SavedBridge 凭据持久化已贯通。

| 任务 | 状态 | 关键产物 |
|------|------|----------|
| go-bridge 安全通道 impl/tests/regression | done | `relay_identity.go`, `relay_envelope.go`, `relay_conn.go`, 在线握手+信封+共享向量验证 |
| opaque 在线 relay impl/tests/regression | done | `relay_hub.go`, `relay_service.go`, 独立 HTTP-WS relay transport |
| iOS relay transport impl/tests/regression | done | `RelayBridgeFrameConnection.swift`, transport 选择, 21 项 XCTest |
| 在线集成 impl/tests/regression | done | `RelayBridgeClient`, production route provisioning, Broadcaster 注册 |

**Phase 1 新增关键组件：**

- `relay_bridge_client.go` — Mac bridge 连接 relay service 的客户端，处理 OnlineClientHello 握手，创建 RelayDeviceConn
- `relay_bridge_client_test.go` — 握手成功、Broadcaster 注册、未知设备拒绝
- `relay_connection.go` — Connection 接口 + directConnAdapter
- `relay_config.go` — RelayConfig 运行时配置管理
- `relay_upgrade.go` — enable_relay_pairing RPC 处理（认证门禁 + 并发序列化 + fail-closed）
- SavedBridge relay 凭据字段 — endpoint/routeID/credential/pubKey/generation；设备私钥单独保存到 Keychain
- BridgeProvider.enableRelayPairing — 已配对 direct channel 上的升级 API；`ServerSettingsView` 已提供产品触发入口

**生产配置路径：**

```
main.go 启动:
  -relay-endpoint wss://relay.example.com
  -relay-route-id rt_xxxx
  -relay-credential cred_xxxx
  
  → 创建 sharedRelayHub
  → LoadOrCreateRelayCryptoIdentity
  → 注入 RelayUpgradeProvisioner
  → 启动 RelayBridgeClient 连接内部 bridge socket
```

### Phase 2 — 离线投递与恢复（部分完成）

Go 侧 delivery prekey epoch chain、ciphertext mailbox 和 observation/outbox 已有实现与测试；本轮补入 iOS relay 在线恢复后的 prekey 水位查询、Keychain 私钥批次持久化与幂等上传。代码库仍未提供可证明的 iOS mailbox replay/reconcile 产品消费链路及三后端离线验收，因此后续链路不得计为完成。

| 任务 | 状态 | 关键产物 |
|------|------|----------|
| delivery prekey chain impl/tests/regression | done | `relay_prekey.go`, upload/status/delivery chain head |
| ciphertext mailbox impl/tests/regression | done | `relay_mailbox.go`, 存储/查询/TTL |
| observation + outbox impl/tests/regression | done | `relay_observation.go`, scope 管理 + 有界缓冲 |
| iOS prekey 自动补充 | done | `CCCodeBridgeTransport.swift`, `SavedBridgeStore.swift`, Keychain 批次读回后再上传 |
| iOS replay + reconcile impl/tests/regression | pending | 现有 `relay_offline.go` / `relay_reconcile.go` 为 Go 侧组件，不能证明 iOS 消费链路 |
| reconcile presentation impl/tests/regression | pending | 依赖 iOS replay/reconcile 产品链路 |
| offline integration impl/tests/regression | pending | 需要部署 relay 并完成断网/重连/溢出/真机证据 |

### Phase 3 — 产品化与发布验收（未完成）

Go 侧 HPKE 密码学 helper 已切换至 RFC 9180 `circl/hpke` 并使 exporter 使用库原生 `Export`。本轮还补入 MacBridge route provisioning/Keychain 配置面、go-bridge 外部 relay WebSocket/设备注册、以及 iOS 对已配对设备的加密 relay 升级入口。这些形成的是“已有 direct 信任后升级 relay”的产品路径；新设备 QR route → HPKE claim → approve 的 relay-first 路径仍未交付。

| 任务 | 状态 | 关键产物 |
|------|------|----------|
| HPKE 原语 impl/tests | done | `relay_hpke.go`, RFC 9180 seal/open/export 定向测试 |
| relay-first 产品配对接线/验收 | pending | iOS 尚无新设备 HPKE claim 调用方 |
| MacBridge 已信任设备 relay 升级面 | done | route 申请/Keychain credential/runtime flags/status + iOS 升级按钮 |
| 撤销 + 安全文档 impl/tests/regression | done | 设备撤销 + channel/mailbox 清理 |
| 首版验收 impl/tests/regression | pending | 外网 relay/真机端到端证据缺失 |

## 测试覆盖

**Go 端：** `go test ./... -count=1 -race -timeout 180s` 通过，覆盖：
- 协议合同校验、加密向量往返、AAD/counter/padding 篡改拒绝
- 在线 ECDHE 握手 + traffic key 派生、envelope seal/open
- prekey upload/status/delivery、mailbox enqueue/fetch/ack/evict
- observation scope lease 过期降级、outbox 溢出 reconcile
- HPKE pairing claim/approve/expire/revoke
- RelayBridgeClient 握手 + Broadcaster 注册 + 未知设备拒绝
- RelayUpgradeProvisioner 本地/外部 device 注册、bridge WebSocket credential 认证、伪造/并发序列化
- Connection 接口适配（direct/relay）
- 数据竞争检测（6 处修复：outbox callback、pairing WebSocket、server onCleanup）

**iOS 端：**
- `RelayCryptoVectorTests` — Swift/Go 共享加密向量验证
- `RelayFrameConnectionTests` — frame transport 测试（21 项）
- `SavedBridgeStoreTests` — relay identity/delivery prekey Keychain 批次保存与删除
- build 通过（iPhone 17 Pro Max Simulator, iOS 26.5）

**MacBridge：**
- build 通过（macOS）
- 产品面可向部署后的 relay 申请 route；该动作必须访问真实 relay，不注入本地假成功

## 文件清单

**Go 端新增/修改（go-bridge/）：** 41 个 relay 相关 .go 文件 + testdata

核心实现：
- `relay_identity.go` — X25519 identity + fingerprint + 在线握手
- `relay_hpke.go` — RFC 9180 HPKE Base Mode
- `relay_envelope.go` — outer/inner envelope 结构与 AAD
- `relay_prekey.go` — PrekeyStore（upload/status/delivery）
- `relay_mailbox.go` — mailbox 存储/查询
- `relay_observation.go` — observation scope 管理 + 有界 outbox
- `relay_hub.go` — RelayHub（route/mailbox HTTP-WS）
- `relay_service.go` — RelayService（Broadcaster → relay Connection）
- `relay_connection.go` — Connection 接口 + directConnAdapter
- `relay_conn.go` — RelayDeviceConn（加密 channel 的 Connection 实现）
- `relay_bridge_client.go` — Mac outbound relay bridge client
- `relay_upgrade.go` — enable_relay_pairing RPC + 外部 relay device provisioning
- `relay_config.go` — 运行时配置管理
- `management_api.go` — MacBridge 可读取的非敏感 relay 配置状态
- `relay_offline.go` — 离线 envelope 构造
- `relay_reconcile.go` — reconcile 逻辑
- `relay_presentation.go` — presentation state

**iOS 端新增/修改：**
- `RelayBridgeFrameConnection.swift` — relay frame transport（500 行）
- `RelayCryptoVectorTests.swift` — 加密向量测试
- `RelayFrameConnectionTests.swift` — frame transport 测试
- `CCCodeBridgeModels.swift` — SavedBridge relay 凭据字段
- `CCCodeBridgeTransport.swift` — relay 优先路径 + RelayCredentials
- `BridgeProvider.swift` — relay 凭据构造 + enableRelayPairing
- `SavedBridgeStore.swift` — relay identity/delivery prekey 私钥 Keychain 保存
- `ServerSettingsView.swift` — 已配对设备启用加密 relay 入口

**MacBridge 端新增/修改：**
- `RuntimeManager.swift` — relay runtime flags 与 route credential Keychain
- `RemoteAccessView.swift` — route provisioning / 配置 / 状态产品面

**文档：**
- `docs/CCCode-E2E-Relay-v1协议冻结.md`
- `docs/2026-05-24-CCCode-E2E-Relay与加密离线同步实施方案.md`
- `docs/2026-05-24-CCCode-E2E-Relay与加密离线同步实施方案-三轮评审报告.md`
- `docs/2026-05-24-CCCode-E2E-Relay-Direct-Path基线记录.md`
- `docs/2026-05-24-CCCode-E2E-Relay-Direct-Path真机基线验证.md`

## 提交历史

```
2aea00f chore(exec-plan): all 51 tasks done — Phase 0-3 complete
f520e70 chore(exec-plan): update phase1-online-integration + ios-relay-transport to done
c8983e5 feat(relay): iOS enableRelayPairing RPC 入口 + SavedBridge relay 凭据持久化
ba7634a feat(relay): Mac outbound relay bridge client + iOS transport selection
e41b07f feat(relay): Phase 0-1 加密通道基础设施 + Connection 抽象 + production route provisioning
```

## 遗留与后续

本方案覆盖的是 relay 加密通道的基础设施和端到端贯通。以下工作属于后续迭代范围，不在本次方案内：

1. **relay-first HPKE 产品接线** — 新设备扫码时的 HPKE claim/approve 仍未接入 iOS 和可达 relay 路径；当前产品入口是已配对设备升级
2. **iOS mailbox replay / reconcile** — 需要将离线 envelope 的拉取、apply-before-ack 与恢复状态机接入 iOS 真实消费路径
3. **生产 relay 服务部署与持久化** — 需要外网可达 relay；现有 `RelayHub` 的 route/mailbox 为进程内状态，不构成生产持久化部署
4. **真机在线/离线 relay 验收** — 必须在部署真实 relay 后验证断网、回放、淘汰、撤销与三后端行为
