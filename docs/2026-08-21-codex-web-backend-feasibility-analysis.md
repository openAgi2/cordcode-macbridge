# Codex Web API Backend 接入可行性与架构分析报告

> [!WARNING]
> **历史输入，不是实施权威。** 本文形成于 source-first 设计收口前，保留了已经被后续源码核验否定或
> 降级的基线叙事、事件名、端口示例和路线选项。实施 `codex-web` 时必须以
> [codex-web Backend 设计](2026-08-21-codex-web-backend-design.md)、其 Phase 0 真实样本和 pinned
> Codex 官方源码为准；不得从本文恢复“重构旧 `agent/codex`”、统一 wire identity、猜测固定端口、
> `codex exec` 默认基线或“外部 turn 已可实时订阅”等结论。

- **日期**：2026-08-21
- **状态**：历史可行性调研输入；已由 source-first 设计校正，不授权实施
- **参考物**：
  - [2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md)
  - [2026-08-20-opencode-web-source-first-convergence-plan.md](2026-08-20-opencode-web-source-first-convergence-plan.md)
  - Codex 源码库：`/Users/jacklee/Projects/codex`

---

## 1. 核心结论（Executive Summary）

**完全可行，且架构契合度极高。**

Codex 源码中原生包含一套成熟完备、生产级的官方服务化后端——**`codex app-server`**（它是 OpenAI 官方 VS Code 扩展和桌面端背后的核心驱动）。效仿 DeepSeek Harness (`dsh-web`) 和 OpenCode (`opencode-web`) 的 Web API 接入模式，不仅在技术上完全可行，而且能够从根本上解决当前 `codex exec` 模式频繁起停子进程、文件系统读写竞争以及高 CPU 开销的问题，完美对齐 CordCode 的 **Session Sync v2 (SSV2)** 架构护栏。

---

## 2. Codex 源码深度剖析 (`/Users/jacklee/Projects/codex`)

Codex 源码基于 Rust 实现，在 `codex-rs/` 下提供了一整套成熟的服务端抽象：

### 2.1 传输层与 Web 服务 (`codex-rs/app-server-transport`)
- **Web 框架**：使用 `axum`（Rust 异步 Web 框架）作为底层服务器。
- **监听模式**：
  - WebSocket 监听：`codex app-server --listen ws://IP:PORT`（如 `ws://127.0.0.1:4141`）。
  - Unix Domain Socket：`--listen unix://` 或 `--listen unix://PATH`。
  - Stdio 模式：`--stdio`（JSON-RPC 2.0 over stdin/stdout）。
- **HTTP 探活与健康检查**：
  - `GET /readyz`：当监听器准备好接收连接时响应 `200 OK`。
  - `GET /healthz`：健康检查接口，无 `Origin` 头时响应 `200 OK`。
- **安全与信任模型（Trust Fence）**：
  - 内置 `reject_requests_with_origin_header` 中间件，拒绝任何携带 `Origin` 头的外部跨域浏览器请求（响应 `403 Forbidden`）。
  - Loopback（`127.0.0.1`）默认放行非浏览器客户端；非 loopback 监听器强制要求配置 Token 认证。与 DSH Web 的信任栅栏设计同构。

### 2.2 协议与领域模型 (`codex-rs/app-server-protocol`)
Codex app-server 提供了完备的 JSON-RPC 2.0 领域模型（v1/v2 协议）：
1. **Thread（会话层）**：
   - `thread/start`：创建新会话（支持 cwd、model、permissions、sandbox 策略）。
   - `thread/resume`：重开/挂载已有会话。
   - `thread/list`：查询会话列表，原生支持 cursor 分页、cwd 过滤、status 状态。
   - `thread/read`：读取会话详情与 turn 历史。
   - `thread/archive` / `thread/unarchive` / `thread/delete`：会话归档与删除。
   - `thread/name/set`：重命名会话。
2. **Turn（轮次层）**：
   - `turn/start`：发送用户输入开启新轮次。
   - `turn/steer`：插话转向（向正在运行中的轮次注入新指令）。
   - `turn/interrupt`：中断/中止当前轮次生成。
   - `turn/started` / `turn/completed`：轮次生命周期通知与 token 结算。
3. **Item（流式与工件层）**：
   - `item/started` / `item/completed`：各项输出（命令、文件修改、推理、消息）开始与完成。
   - `item/agentMessage/delta`：文本消息流式输出。
   - `item/reasoning/delta`（或 `item/reasoning`）：思考过程流式输出。
4. **审批与交互（Approval / Question）**：
   - `item/commandExecution/requestApproval`：命令行与高危操作审批。
   - `tool/requestUserInput`：向用户提出结构化问答（1~3 题）。
5. **模型与配置**：
   - `model/list`：模型目录查询与 reasoning effort 阶梯枚举。
   - `config/read` / `config/value/write` / `config/batchWrite`：配置查询与热更新。
   - `permissionProfile/list`：权限预设列表。

### 2.3 守护进程与持久管理 (`codex-rs/app-server-daemon`)
Codex 提供了 `codex app-server daemon` 子命令，支持 PID 文件管理、状态互斥锁以及平滑启停，适合与 MacBridge 的进程管理器对接。

---

## 3. 三大后端横向对比矩阵

| 架构维度 | DeepSeek Harness (`dsh-web`) | OpenCode (`opencode-web`) | Codex 现状 (`agent/codex`) | Codex Web API 目标形态 (`codex-web`) |
|---|---|---|---|---|
| **官方服务形态** | `dsh --profile web` | `opencode serve` | `codex exec --json` (CLI 子进程) | `codex app-server --listen ws://...` |
| **通信传输载波** | HTTP POST + 2× WebSocket (`mux`, `host`) | REST HTTP + SSE (`/global/event`) | 每次 turn 启动 stdio 子进程 | 单例长连接 **WebSocket (JSON-RPC 2.0)** |
| **探活机制** | `POST /api/host.describe` | `GET /global/health` | 无（直接执行命令行） | `GET /healthz` 或 `GET /readyz` |
| **生命周期管理** | 探测复用 3080 → Managed spawn → 写状态 JSON | 探测复用 4096 → Managed spawn → 写状态 JSON | 无 Managed，每次调用启动独立子进程 | 探测现有端口 → Managed spawn → 记 `codex-web-managed-server.json` |
| **会话列表获取** | `session.list` (官方 API 供给) | `GET /session?roots=true` (官方 HTTP) | **直接遍历磁盘** `~/.codex/sessions/*.jsonl` | `thread/list` (官方 JSON-RPC，带 cwd/cursor/status) |
| **历史加载 (Hydrate)** | `session.history` → SSV2 Pathless | `GET /session/:id/message` → SSV2 Pathless | **直接读取/解析** 磁盘 `.jsonl` 文件 | `thread/read` / `thread/turns/list` → SSV2 Pathless |
| **发送与插话** | `session.create` / `prompt` (queue/steer) | `POST /session` / `prompt_async` | 启动 `codex exec` / `resume` 子进程 | `thread/start` / `resume` + `turn/start` / `steer` |
| **流式事件处理** | WS `session/event` 帧 → `core.Event` | SSE `event.part.*` → `core.Event` | 捕获 CLI stdout JSONL 文本行 | WS `item/*`, `turn/*` 通知 → `core.Event` |
| **中断 (Abort)** | `session.cancel` | `POST /session/:id/abort` | `SIGKILL` 强杀子进程及进程组 | `turn/interrupt` (JSON-RPC) |
| **审批与提问** | `approval/requested` → `/api/respond` | `permission_request` / `user_input` | 阻塞子进程 stdin/stdout | `commandExecution/requestApproval` / `requestUserInput` |
| **模型/配置管理** | `llm.models`, `llm.providers` | `/provider`, `/config` | 读取 `config.toml` 或传 CLI 参数 | `model/list`, `config/read`, `config/value/write` |
| **SSV2 护栏约束** | 真相归 DSH Web 服务，MacBridge 零写磁盘 | 真相归 OpenCode 服务，MacBridge 零写磁盘 | **违反单写者**：MacBridge 与 CLI 共读写磁盘 | **完美符合**：Truth Owner 归 `app-server`，MacBridge 零写磁盘 |

---

## 4. 效仿 DSH/OpenCode 的具体落地架构设计

### 4.1 架构拓扑

```text
┌─────────────────────────────────────────────────────────────┐
│                       CordCode iOS Client                   │
└──────────────────────────────┬──────────────────────────────┘
                               │ bridge-v1 protocol (8777 / relay)
┌──────────────────────────────▼──────────────────────────────┐
│                    MacBridge (Go Backend)                   │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │                   agent/codex-web                     │  │
│  │  - Lifecycle: Probe 127.0.0.1 / Managed Spawn app-srv │  │
│  │  - Transport: WebSocket Client (JSON-RPC 2.0)         │  │
│  │  - Health Probe: HTTP GET /healthz                    │  │
│  │  - SSV2 Pipeline: Pathless Hydrate + Live Ingest      │  │
│  └───────────────────────────┬───────────────────────────┘  │
└──────────────────────────────┼──────────────────────────────┘
                               │ JSON-RPC 2.0 over WebSocket (ws://127.0.0.1:<port>)
┌──────────────────────────────▼──────────────────────────────┐
│                  Official Codex app-server                  │
│                                                             │
│  - Axum Web Server (GET /healthz, WebSocket upgrade)        │
│  - State Owner: ~/.codex/sessions, SQLite DB, config.toml   │
│  - Engine: Thread/Turn/Item lifecycle, tool execution       │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 模块落地要点

#### 1. 服务生命周期（探测复用 + Managed 托管）
1. **探测阶段**：向默认/预配端口（如 `ws://127.0.0.1:3845` 或 `4141`）发起 HTTP `GET /healthz` 探活请求：
   - 响应 `200 OK` → 复用用户已启动的实例。
2. **Managed 模式**：未探测到时，由 MacBridge 自动启动托管进程：
   ```bash
   codex app-server --listen ws://127.0.0.1:<3850..3950>
   ```
   并将端口、实例类型记录于 `~/Library/Application Support/CordCode Link/codex-web-managed-server.json`（权限 `0600`）。
3. **安全栅栏**：监听绑定严格锁定在 `127.0.0.1`，仅供本机 Loopback 访问。

#### 2. 会话管理与 SSV2 对齐
- **列表（`list_sessions`）**：调用 `thread/list{cwd: "..."}`。
  - 直接获得 `threadId`、`preview/title`、`createdAt`、`updatedAt`、`status` 等元数据。
  - 彻底废除旧模式下遍历扫描 `~/.codex/sessions/**/*.jsonl` 的高负载 IO 逻辑。
- **历史加载（`get_session_projection` / Hydrate）**：
  - 调用 `thread/read{threadId, includeTurns: true}` 或 `thread/turns/list`。
  - 将官方结构映射为 Rich History，经私有事务喂入 `ProjectionKernel`。
  - 归入 **Pathless 家族**（与 `opencode-web`、`dsh-web` 保持一致），MacBridge 不直接读写磁盘文件。

#### 3. 消息发送与流式响应
- **新建与续聊**：
  - 新建会话：`thread/start{cwd, model, permissions}` → 获得 `threadId` → `turn/start{threadId, input}`。
  - 续聊会话：直接调用 `turn/start{threadId, input}`（官方原生 Resume，无需判断进程是否存在）。
  - 插话：调用 `turn/steer{threadId, input}`。
- **流式事件转换**：
  - 监听 WebSocket 下行通知：
    - `turn/started` → 标记活跃态，推导 turn 状态。
    - `item/agentMessage/delta` → 映射为 `core.EventTextDelta`。
    - `item/reasoning/delta` → 映射为思考增量。
    - `item/started` / `item/completed` → 映射为工具调用卡片与执行结果。
    - `turn/completed` → 终态封口，结算 Usage。
- **中断生成**：
  - 调用 `turn/interrupt{threadId, turnId}`，平滑终止。

#### 4. 审批与交互（Permissions & User Input）
- 接收 `item/commandExecution/requestApproval` → 映射为 MacBridge 的 `permission_request` 事件（iOS 端展示权限审批）。
- 接收 `tool/requestUserInput` → 映射为 `user_input_requested` 事件（iOS 端展示问答卡片）。
- 用户应答后，通过 WebSocket 回发对应的 JSON-RPC 结果，解除挂起。

---

## 5. 迁移收益分析

1. **消除子进程频繁起停与孤儿进程**：
   - 现有的 `codex exec` 在用户每发一条消息时都需要重新拉起一个重量级的 Node/Rust CLI 进程。
   - Web API 模式采用常驻服务与长连接，降低交互延迟；生成中断由 RPC 精确控制，避免了通过 `SIGKILL` 杀进程组时漏掉子孙进程的风险。
2. **彻底落实 SSV2 单一真实源（Single Source of Truth）**：
   - 消除 MacBridge 对 `~/.codex/sessions` 文件的直接读写，避免与 CLI 产生文件锁冲突、缓存不一致等问题。
   - 会话状态的权威归 `codex app-server` 独占，MacBridge 仅做纯粹的内存投影。
3. **支持外部会话感知与多端接力**：
   - 用户在本地 VS Code 或终端操作产生的 Codex 会话，MacBridge 通过 `thread/list` 和事件订阅可实时感知并在 iPhone 上同步呈现。

---

## 6. 关键差异点与设计注意事项

1. **通信载波选型**：
   - OpenCode Web: REST HTTP + SSE。
   - DSH Web: HTTP POST + 2× WebSocket (`events.mux`, `events.host`)。
   - **Codex app-server**：**全双工 WebSocket (JSON-RPC 2.0)**。所有的 Unary 请求和双向事件流在同一个 WebSocket 上交互，使用 `id` 进行关联。
2. **探活与健康检查**：
   - Codex app-server 的 WebSocket 监听器在 HTTP 握手前可直接通过 `GET /healthz` 和 `GET /readyz` 探活，探活逻辑极其轻量。
3. **平滑演进与兼容性**：
   - 建议在 MacBridge 内部以 `agent/codex-web`（或重构 `agent/codex`）形式推进，对外保持 wire kind 统一，确保 iOS 侧零破坏升级。
