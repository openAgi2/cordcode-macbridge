# CordCode Bridge v1 Schema 对照表

> Schema revision：2026-08-02
> Protocol：`cordcode-bridge` version `1`
> Canonical source：`cordcode-macbridge/docs/protocol/bridge-v1-schema.md`

## 命名决策

Bridge v1 的协议名冻结为 `cordcode-bridge`。旧 `unified-bridge` 只作为历史迁移诊断语境出现，不作为新协议名。

`backendId` 在 v1 envelope 中暂时保留，用来兼容当前 go-bridge 和 iOS 现有连接层；产品语义上它表示 agent/provider target，不表示多个 backend 与 go-bridge 并列。新业务方法优先使用 `agent.list`，当前 `backend.list` 可作为迁移别名。

## 最小 Envelope

| JSON 字段 | Go 类型 | Swift 类型 | 说明 |
|---|---|---|---|
| `type` | `string` | `String` | `hello` / `hello_ack` / `request` / `result` / `event` / `pairing_result` |
| `requestId` | `string` | `String?` | request/result correlation |
| `method` | `string` | `String?` | `session.list` 等方法名 |
| `params` | `json.RawMessage` | method-specific `Codable` | 请求参数 |
| `ok` | `bool` | `Bool?` | result 或 ack 成功标记 |
| `data` | `any` | method-specific `Codable` | 成功响应数据 |
| `error` | `WireError` | `BridgeErrorEnvelope?` | 点分命名错误码 |

## Go Struct 草案

Go 协议类型落在 `go-bridge/bridge_v1_schema.go`，包含：

- `BridgeV1Hello`
- `BridgeV1HelloAck`
- `BridgeV1PairingClaimParams`
- `BridgeV1PairingResult`
- `BridgeV1EventEnvelope`
- `BridgeV1Protocol`

## Swift Codable 草案

```swift
struct BridgeV1Protocol: Codable, Equatable {
    let name: String
    let version: Int
    let schemaRevision: String?
    let supportedSchemaRevisions: [String]?
}

struct BridgeV1Client: Codable, Equatable {
    let app: String
    let version: String
    let deviceId: String
}

struct BridgeV1Hello: Codable, Equatable {
    let type: String
    let client: BridgeV1Client
    let `protocol`: BridgeV1Protocol
}

struct BridgeV1CurrentURLs: Codable, Equatable {
    let local: String
    let remote: String?
}

struct BridgeV1BridgeProfile: Codable, Equatable {
    let bridgeId: String
    let displayName: String
    let runtimeVersion: String
    let currentURLs: BridgeV1CurrentURLs
    let `protocol`: BridgeV1Protocol
}

struct BridgeV1Capabilities: Codable, Equatable {
    let remoteAccessConfig: Bool
    let trustedDevices: Bool
    let offlineSnapshots: Bool
    let workspaceList: Bool
    let sessionMutation: Bool
}

struct BridgeV1RunningSession: Codable, Equatable {
    let backendId: String
    let workspaceId: String?
    let sessionId: String
    let status: String
}

struct BridgeV1HelloAck: Codable, Equatable {
    let type: String
    let ok: Bool
    let bridge: BridgeV1BridgeProfile?
    let capabilities: BridgeV1Capabilities?
    let backends: [BridgeV1BackendInfo]?
    let bridgeStatus: String?
    let runningSessions: [BridgeV1RunningSession]?
    let error: BridgeV1Error?
}

struct BridgeV1Error: Codable, Equatable {
    let code: String
    let message: String
}
```

## 方法名冻结

| 方法 | 认证 | 说明 |
|---|---|---|
| `pairing.claim` | 否 | iOS 认领二维码或手动码 |
| `pairing.poll` | 否，需 pairing secret | 等待 Mac 端 approve/reject |
| `hello` | 是 | 首包认证、版本握手、能力获取 |
| `bridge.status` | 是 | runtime 状态摘要 |
| `agent.list` | 是 | agent/provider target 列表；`backend.list` 为迁移别名 |
| `workspace.list` | 是 | workspace 列表 |
| `session.list` | 是 | session 列表 |
| `session.messages` | 是 | session 消息历史 |
| `session.send` | 是 | 发送消息 |
| `resolve_user_input` | 是 | 回答 MacBridge 持有 responder 的结构化问题 |

## 结构化用户输入

Backend 只有在 adapter、真实 responder 与 Projection Kernel 都 ready 时才在
`hello_ack.backends[].capabilities` 中广告 `structured_user_input_v1`。iOS 还必须同时处于
`session_sync_v2` 才能发送 answer action。

### Projection part

```ts
interface BridgeUserInputOption {
  id: string;
  label: string;
  description?: string;
}

interface BridgeUserInputQuestion {
  id: string;
  header?: string;
  prompt: string;
  answerMode: "single" | "multiple" | "text";
  options: BridgeUserInputOption[];
  allowsCustomAnswer: boolean;
  isSecret: boolean;
  required: boolean;
}

interface BridgeUserInputPart {
  type: "user_input";
  interactionId: string;
  status: "pending" | "answered" | "rejected" | "auto_resolved" | "unavailable" | "failed";
  questions: BridgeUserInputQuestion[];
  canRespond: boolean;
  canReject: boolean;
  expiresAt?: number;
  resolvedAt?: number;
  resolutionSource?: "ios" | "mac" | "other_client" | "backend";
  diagnosticCode?: string;
}

interface BridgeUpsertUserInputOp {
  turnId: string;
  messageId: string;
  op: "upsert_user_input";
  part: BridgeUserInputPart;
}
```

Swift `SessionProjectionPart.CodingKeys` 必须把 `userInput*` stored properties 映射到 wire flat
keys，例如 `userInputInteractionId = "interactionId"`、`userInputQuestions = "questions"`。

约束：

- `status == pending && canRespond == true` 才渲染可交互表单。
- `diagnosticCode == observe_only` 是外部 session 的只读 transcript 镜像，不得发 action。
- Claude AskUserQuestion 根据真实 custom-result 证据使用 `allowsCustomAnswer=true`；
  外部 session 只读展示 Other，不伪造输入权。
- pending part 使 `execution.phase=requires_action`；RPC ack 不允许 iOS 本地删卡。
- projection 不存储 secret/custom 答案正文。

### Answer RPC

```ts
interface ResolveUserInputParams {
  sessionId: string;
  interactionId: string;
  clientActionId: string;
  action: "answer" | "reject";
  answers?: Array<{
    questionId: string;
    values: Array<
      | { kind: "option"; optionId: string }
      | { kind: "text"; text: string }
    >;
  }>;
}

interface ResolveUserInputResult {
  interactionId: string;
  outcome: "accepted" | "already_resolved" | "in_progress";
  currentStatus: BridgeUserInputPart["status"];
  headRev: number;
}
```

`outcome/currentStatus` 是 action acknowledgement：`in_progress/pending` 表示另一 writer 已取得
claim，不能据此把卡片写成终态。客户端仅以 `headRev` 对应的 Session Projection
`user_input` part 判定 answered/rejected 等终态；本 RPC 不形成第二个 projection writer。

稳定错误码：`interaction_not_found`、`interaction_already_resolved`、
`invalid_backend_request`、`invalid_answer_shape`、`response_not_supported`、
`backend_response_failed`、`session_not_active`。

## 错误码命名空间

| 前缀 | 示例 |
|---|---|
| `auth.*` | `auth.missing_token`, `auth.invalid_token`, `auth.revoked` |
| `protocol.*` | `protocol.unsupported_version`, `protocol.unknown_method` |
| `pairing.*` | `pairing.expired`, `pairing.rejected`, `pairing.already_claimed` |
| `bridge.*` | `bridge.not_ready`, `bridge.shutting_down`, `bridge.needs_update` |
| `agent.*` | `agent.not_detected`, `agent.unavailable`, `agent.version_unsupported` |
| `workspace.*` | `workspace.not_found`, `workspace.access_denied` |
| `session.*` | `session.not_found`, `session.not_running`, `session.conflict`, `session.held_by_external_worker`, `session.owner_check_failed` |
| `permission.*` | `permission.not_found`, `permission.already_resolved` |
| `network.*` | `network.timeout`, `network.connection_refused` |

Claude `send_message` 在续接一个尚未由当前 MacBridge registry 持有的 session 前执行 best-effort
进程预检。检测到同 session 的记录进程仍存活时返回
`session.held_by_external_worker`；无法完成归属检查（能力缺失、检查错误或超时）时 fail-closed
返回 `session.owner_check_failed`。两者 wire error 均携带 `retryable: true`，表示外部状态变化后可由
用户重新发送；它不代表客户端必须自动重试。

## 事件类型冻结

`session.started`, `session.completed`, `session.failed`, `session.aborted`, `message.started`, `message.delta`, `message.completed`, `tool.started`, `tool.delta`, `tool.completed`, `permission.requested`, `permission.resolved`, `todos.updated`, `model.changed`, `agent.status_changed`, `bridge.status_changed`。

## Fixture

JSON fixture 位于 `go-bridge/testdata/bridge-v1`。当前覆盖 `hello` 与 `hello_ack` 的 Go
decode/encode round trip。Swift 与 TypeScript 必须复用同一 wire 字段命名。
