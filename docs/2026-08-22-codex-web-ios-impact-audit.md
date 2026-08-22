# Codex Web Phase 5 iOS 文件级影响审计

- 审计日期：2026-08-22
- iOS 仓库：`../cordcode-ios`
- 审计基线：`main@2cdb490f17ce98b36a03c6f3cf59c86e3257feda`
- 基线状态：工作区 clean；本地 `main` 相对 `origin/main` ahead 27
- MacBridge 实现基线：`codex/codex-web-backend@e8f8aab`
- 结论：**PASS，可以进入 Phase 5 产品接线；必须按下述裁决实施，不能把 `.codexWeb` 机械并入所有 `.codex` 分支。**

## 审计边界与不变量

- timeline 真相 owner：MacBridge Projection Kernel。
- iOS active timeline 唯一 writer：`ProjectionStore`；`codex-web` 只有 descriptor 声明
  `session_sync_v2` 且产品模式为 `.active` 时才能接管。
- 受影响事务域：backend discovery/selection、bridge RPC routing、control-plane model/approval、
  projection ownership、local cache namespace；不新增第二条消息数据路径。
- 失败呈现：未知 wire kind 继续被忽略；已保存但当前不可用的 backend 继续返回
  `BridgeUnavailableBackendClient`，不得回退到旧 `codex`。
- 防双写：`codex-web` 进入 SSV2 migrated set；旧 Codex rollout poll/history 特例不得继承。

分类词汇：`must change` = Phase 5 必改；`verified generic` = 当前实现按 backend ID/capability
泛化，新增枚举后无需改；`intentionally codex-only` = 有证据要求只保留旧 `codex`；`N/A` =
该扫描面没有 repo-native代码改动。

## 两项强制裁决

### A. 禁止机械 `|| .codexWeb`

`codex-web` 与旧 `codex` 只共享已由协议事实证明的产品语义：Bridge 通用 client、rich history、
实时 core events、structured user input、model catalog/per-model effort、SSV2 projection。以下旧
Codex 行为不得继承：

1. rollout 文件/history probe 与 transient `session file not found` 特判；`codex-web` descriptor
   明确 `RequiresExternalTurnPolling=false`，外部 turn 由 daemon broadcast + Kernel projection 提供。
2. `providerId ∈ {codex,openai}` 硬过滤；`codex-web` model ID 来自官方当前 provider 目录，provider
   可为第三方，目录成员校验才是权威。
3. 新 session 默认 `.xhigh`；`codex-web` 服从 model/list 的 per-model default effort。
4. 旧 Codex `default/auto-review/full-access/custom` permission-mode catalog；`codex-web` 当前只广告
   approval responder，不广告 `permission_mode`，所以不能展示或发送旧模式。

### B. backend/cache/session identity 必须独立

- Swift identity：`BackendKind.codexWeb.rawValue == "codexWeb"`。
- wire identity：hello descriptor `id/kind == "codex-web"`，RPC `backendId == "codex-web"`。
- fallback normalization：空 descriptor ID 只能补成 `codex-web`，禁止补成 `codex`。
- cache/session scope：现有 `BackendServerIdentity.cacheScopeKey = backendKind|endpoint|username`
  可保持；加入独立 enum 后自然隔离。必须加测试证明相同 endpoint/username/sessionId 下
  `.codex` 与 `.codexWeb` 的 identity、message cache、snapshot、draft/model scope 不相等。
- 不因两者读取同一官方 store/thread 而共享任何 iOS cache key；跨 backend 同 thread ID 仍是两份
  产品观察空间。

## 12 类扫描结论

### 1. BackendKind

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift` | must change | 新增 `.codexWeb`；补全所有穷举属性，但逐属性按本审计裁决分组。不得标 deprecated。 |
| `OpenCodeiOS/OpenCodeiOS/Models/Server.swift` | verified generic | Codable 持久化直接使用独立 enum raw value。 |
| `OpenCodeiOS/OpenCodeiOS/Models/ServerConfig.swift` | verified generic | `backendKind` 已进入 `backendIdentity`，无需别名。 |

### 2. wire kind

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift` | must change | `fromWireKind("codex-web") -> .codexWeb`。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift` | must change | `CCCodeBridgeBackendID.normalized("", .codexWeb) == "codex-web"`；不能复用 `codex`。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeModels.swift` | verified generic | descriptor `kind/id` 保持 String，未知字段宽松解码。 |

### 3. backend discovery / switch

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/BridgeDiscoveryService.swift` | must change | kind 与 id fallback 都增加 `codex-web`，映射到 `.codexWeb`。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift` | verified generic | client 字典按 `kind.rawValue`，RPC 仍使用 descriptor `backend.id`；backend switch 不需要专用分支。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ServerViewModel.swift` | must change | 补全 timeout 文案穷举；现有 bridge group/switch 逻辑按 enum 泛化。 |
| `OpenCodeiOS/OpenCodeiOS/App/ContentView.swift`、`DrawerContainerView.swift`、`DrawerContainerViewController.swift` | verified generic | 切换使用 Server/BridgeGroup identity，无 Codex 分支。 |

### 4. server creation

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift` | must change | `.codexWeb` 加入 `serverCreationCases`，与旧 `.codex` 同时可选。 |
| `OpenCodeiOS/OpenCodeiOS/Views/Settings/AddServerView.swift` | verified generic | picker 只消费 `serverCreationCases`。 |
| `OpenCodeiOS/OpenCodeiOS/Models/Server.swift` | verified generic | Server 转 ServerConfig 保留独立 kind。 |

### 5. display / icon

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift` | must change | 显示名 `Codex Web`；一期复用现有 `codex_logo` 资产；model/empty/history 文案走通用组。 |
| `OpenCodeiOS/OpenCodeiOS/Models/SessionLifecycleDiagnosticPhase.swift` | must change | 增加 daemon app-server/official thread/SSV2 描述，不能复制 rollout 描述。 |
| `OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift` | must change | agent/title/timeline fallback 显示 `Codex Web`；agent picker 隐藏，目录 chrome 隐藏。 |
| `OpenCodeiOS/OpenCodeiOS/Views/Settings/ServerSettingsView.swift` | must change | reasoning 展示偏好增加独立 codex-web key；不借用旧 Codex 的 AppStorage 身份。 |
| `OpenCodeiOS/OpenCodeiOS/Views/Components/SidebarView.swift`、`FloatingChatNavigationHeader.swift` | verified generic | 使用 `backendKind.displayName/iconName`。 |

### 6. capability gate

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift` | must change | `.codexWeb` 加入 `sessionSyncV2ProjectionBackend`；实际 takeover 仍同时要求 backend-scoped `session_sync_v2`。 |
| `OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift` | must change | projection failed 的 header retry 改由 `sessionSyncV2Active`/projection 状态门控，不再写死 `.codex`。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift` | verified generic | permissions/models/structured input/session sync 均来自 descriptor capability 或协议接口。 |
| `OpenCodeiOS/OpenCodeiOS/Views/Components/SidebarView.swift` | verified generic | mutation/pin 等菜单按 capability，不按 backend 名称。 |

### 7. model mapping

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/ModelManagementService.swift` | must change | catalog 不做 Copilot 过滤；cache key 保留独立 backend scope。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift` | must change | model 走目录成员校验且允许任意官方 effective provider；reasoning effort 从 model 条目透传。旧 `.codex` provider allowlist 保留 codex-only。 |
| `OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift` | must change | 模型显示要求非空 provider；不继承旧 Codex permission-mode catalog。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift` | intentionally codex-only | `.codex` 默认 `.xhigh` 不扩展；codex-web 使用官方 per-model default。 |

### 8. permission mapping

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Views/Chat/SelectionSheets.swift` | must change | 补 `.codexWeb` 无 capability 时的诚实文案；有 `supportsPermissionActions` 时继续走通用审批文案。 |
| `OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift` | intentionally codex-only | 旧 Codex permission-mode 顺序、默认 catalog、文案/icon 特判不扩展到 codex-web。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift` | verified generic | approve/reject 与 structured user input 已按 capability/interaction ID 泛化。 |

### 9. agent mapping

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift` | must change | codex-web 无 `agent_selection`，隐藏固定 agent 菜单，fallback 名为 `Codex Web`。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift` | must change | 通用 core-event reducer 的 fallback agent name 增加 `codex-web`；session-state 外部激活按相同 event 语义接入。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+AgentRuntimeStatus.swift` | must change | codex-web reasoning/tool/permission/question 事件使用 `.codex` 展示 source（这是 UI event vocabulary，不是 backend/cache identity）。 |
| `OpenCodeiOS/OpenCodeiOS/App/MessageWeb/MessageWebModels.swift` | verified generic | `.codex` 是 runtime-status 展示 source vocabulary，不是 `BackendKind` 或 cache identity；无需新增 source 枚举。 |

### 10. session / message cache scope

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift` | verified generic + test required | `BackendServerIdentity.cacheScopeKey` 已含 enum raw value。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Storage/MessageCacheManager.swift`、`SessionSnapshotStore.swift`、`SessionDraftStore.swift` | verified generic + test required | 均消费 `BackendServerIdentity`; 不改 production key 结构，只加跨 backend 隔离测试。 |
| `OpenCodeiOS/OpenCodeiOS/Services/ModelManagementService.swift` | verified generic + test required | model/session selection cache 同样由 backend identity 分区。 |

### 11. stream / recovery special case

| 文件 | 分类 | 裁决 |
|---|---|---|
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift` | intentionally codex-only | `startCodexExternalTurnProbePolling`、rollout history completion、Codex probe restart 不扩展；codex-web descriptor 明确无需 polling。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift` | intentionally codex-only | active-push Codex history merge、context usage history refresh、transient rollout file miss 不扩展；codex-web active timeline 只由 projection writer。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift` | intentionally codex-only | `.codex` resident history watchdog 不扩展；codex-web 依赖 daemon observation/SSV2 reconcile。 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatTurnSyncPolicy.swift`、`ChatTurnSyncState.swift` | verified generic | ownership key 已包含 backend scope/session；无 backend 枚举分支。 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`、`ForegroundRecoveryCoordinator.swift` | verified generic | recovery/cuts 按 wire backend ID 分区，`codex-web` 自然独立。 |

### 12. protocol mirror / tests

| 文件 | 分类 | 裁决 |
|---|---|---|
| Mac canonical `docs/protocol/bridge-v1.md`、`docs/protocol/unified-bridge-protocol.md` | must change | 新增 `id/kind=codex-web`、能力与独立 identity；先于 iOS mirror 提交。 |
| iOS mirror `docs/protocol/bridge-v1.md`、`docs/protocol/unified-bridge-protocol.md` | must change | 与 canonical 字节级同步；冻结枚举加入 `codex-web`。 |
| `OpenCodeiOS/OpenCodeiOSTests/BridgeTransportTests.swift` | must change | wire/discovery/server creation/display/cache identity。 |
| `OpenCodeiOS/OpenCodeiOSTests/CCCodeBridgePhase2Tests.swift` | must change | backend ID normalization 与 descriptor routing。 |
| `OpenCodeiOS/OpenCodeiOSTests/AutomationLaunchRequestTests.swift` | must change | hello discovery 后双 Codex backend 同时出现、切换不串 target。 |
| `OpenCodeiOS/OpenCodeiOSTests/ChatViewModelSessionSyncV2Tests.swift` | must change | capability absent 不接管、present 时 projection-only writer。 |
| `OpenCodeiOS/OpenCodeiOSTests/ModelReasoningEffortWireMappingTests.swift` | must change | 第三方 provider-qualified model 与 per-model effort。 |
| `OpenCodeiOS/OpenCodeiOSTests/StructuredUserInputIOSMappingTests.swift`、`BridgePermissionWireBehaviorTests.swift` | verified generic + targeted regression | 复用现有 wire mapper，新增 codex-web backend ID 用例即可。 |
| `OpenCodeiOS/OpenCodeiOSTests/AgentRuntimeStatusTests.swift` | must change | codex-web source/reasoning/tool/interaction 状态。 |
| UI/snapshot tests | N/A | 审计与产品接线默认不运行；真机 UI 验收按 §13.3 由 owner 执行。 |

## 实施清单（由本审计派生）

1. Mac canonical protocol 先提交；iOS mirror 随后同步。
2. iOS 按 `must change` 清单一次性补 enum、wire/discovery、display、SSV2、model/interaction 映射。
3. 加独立 identity/cache 测试，明确断言 `.codex != .codexWeb`、`codex != codex-web`。
4. 静态核对所有旧 `.codex` production 引用：只能是本表标出的 `must change` 或
   `intentionally codex-only`，不得出现未裁决分支。
5. 只跑定向 build 与 `CCCodeTests/<class>`；不运行 UI/snapshot/simulator automation。
6. 修改 iOS App 后按仓库规则探测真机并自动 build/install/launch，但不操作设备 UI。

## 可复跑证据

```bash
python3 scripts/codex-web-phase5/verify_ios_impact_audit.py
```

该脚本从固定 iOS commit 读取源码，不依赖之后工作区的实现状态；它验证 12 类齐全、所有
production `.codex` 分支文件均已在本审计逐文件裁决、关键 must-change/test/protocol 文件完整，
并验证基线当时尚无 `.codexWeb`（证明这是实施前审计，而不是事后补文档）。
