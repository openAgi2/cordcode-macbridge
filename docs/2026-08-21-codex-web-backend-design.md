# codex-web Backend v2.0（Desktop 同 daemon 拓扑 + 官方 app-server 翻译）

- 日期：2026-08-21
- 状态：**v2.0 topology-first 施工合同；取代 v1.5–v1.7 中“Terminal Gate 可单独放行产品实施”的冲突规则。Desktop 与 CordCode 连接同一官方 daemon 是产品代码开工和继续扩面的共同硬门；任何 PARTIAL、独立 runtime 或 managed-loopback 结果均不得放行。**
- 执行进度（2026-08-25 晚）：**全部完成（111/111 done 均 proven）。** Phase 0–5 与 owner 真机矩阵（模型目录 parity + 交互回归，双拓扑）于 2026-08-25 验收，缺陷修复链 Mac 0f524d7..202b41c / iOS aeb13d5。Phase 6：owner 明示放弃观察窗直接退役——Mac drivers 移除 `codex`（980d358）、iOS `BackendKind.codex` isDeprecated + 退出 serverCreationCases（b700932），代码保留；部署验证 = runtime drivers 无 codex + 真机安装；**owner 退役后真机复测 PASS（codex 消失、codex-web 正常）。** 完成报告见 [2026-08-21-codex-web-backend-design完成情况.md](2026-08-21-codex-web-backend-design完成情况.md)；持久化真相见 `.exec-plan/state/plan-c48486da6336.json`。
- 参考方案：[2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md)
- 一轮评审：[2026-08-21-codex-web-backend-design-review.md](2026-08-21-codex-web-backend-design-review.md)
- 二轮评审：[2026-08-21-codex-web-backend-design-review-r2.md](2026-08-21-codex-web-backend-design-review-r2.md)
- 三轮评审：[2026-08-21-codex-web-backend-design-review-r3.md](2026-08-21-codex-web-backend-design-review-r3.md)
- 四轮确认评审：[2026-08-21-codex-web-backend-design-review-r4.md](2026-08-21-codex-web-backend-design-review-r4.md)
- 历史前置分析（仅作问题来源，已降级，不授权实施）：[2026-08-21-codex-web-backend-feasibility-analysis.md](2026-08-21-codex-web-backend-feasibility-analysis.md)
- **连续性合同（v2.0 拓扑之后、禁止按现象单修）**：[2026-08-22-codex-web-continuity-architecture.md](2026-08-22-codex-web-continuity-architecture.md)
- **拓扑可观测性后续计划（已完成，不替代 Phase 6 owner 门）**：[2026-08-24-codex-web-topology-sync-monitor-implementation-plan完成情况.md](2026-08-24-codex-web-topology-sync-monitor-implementation-plan完成情况.md)
- Codex 源码 pin：`536f86e5cc9ec1ff38457d099bf320b9d08eeeba`
- 本机官方二进制：`codex-cli 0.148.0-alpha.21`（设计时）；**Phase 0 实施时实测 `0.149.0-alpha.4`**（ChatGPT.app 内嵌，版本漂移已按 §3.2 记录，schema 与 pin 逐项吻合）
- 不变约束：CordCode 初衷 + SSV2 十二条护栏 + source-first/真实样本纪律

> [!IMPORTANT]
> 本文中的“官方 API”特指 **OpenAI Codex 官方二进制提供的 `codex app-server` JSON-RPC API**，
> 不是 OpenAI Platform Responses API，也不是 MacBridge 自建的 Codex 兼容服务。
> MacBridge 只负责调用官方 API，并把官方 Thread / Turn / Item / ServerRequest 语义翻译成
> CordCode `bridge-v1`；它不拥有模型推理、不重建 Codex runtime，也不成为 session 真相源。

> [!WARNING]
> **v2.0 是唯一施工解释。** v1.5 的四轮 APPROVE、旧 Phase 0 Terminal PASS、历史完成报告和
> exec-plan `done` 状态都不能覆盖本版拓扑硬门。若任务队列、旧评审或 companion 文档与本文冲突，
> 立即停止产品代码，先修正文档和队列；禁止选择更容易标记完成的解释继续施工。

## 0. 结论与路线裁决

`codex-web` 不是“另一个能调用 Codex API 的 backend”，而是 Mac Codex Desktop 官方工作流在 iOS
上的第二个 connection。产品成立的必要拓扑只有一种：

```text
Codex Desktop ───── WebSocket over UDS ────┐
                                            ├─ 同一个官方 codex app-server daemon
CordCode codex-web ─ WebSocket over UDS ───┘
```

共享 `~/.codex`、能互相 `thread/list/read`、使用同版本官方二进制，均不能替代“同一 daemon”的证明。
只共享磁盘而各自持有 app-server，会产生 active writer 冲突、收不到对方 live 事件，也不满足 CordCode
初衷。

### 0.1 不可协商的宿主拓扑不变量

以下条件缺一即停止产品施工：

1. Desktop 不持有私有 Embedded/stdio app-server；CordCode 不持有第二个 managed-loopback app-server；
2. Desktop 与 CordCode 的实际 FD/peer 指向同一官方 daemon control socket；
3. 两端用同一官方 thread identity 双向创建和续聊，不迁移、不复制、不抢 writer；
4. Desktop 发起的 turn 能通过同一 daemon 让 CordCode 实时进入 active、接收 item/delta 与唯一终态；
5. iOS 发起的 turn 能被仍然打开的 Desktop 实时看到并继续；测试不得要求先退出另一客户端；
6. daemon、Desktop 内嵌 CLI 与 standalone 版本不兼容时 fail closed，不另起服务伪造可用。

`managed-loopback-ws`、每 session stdio app-server、共享 store + 独立 writer 都是本产品路线的
**架构失败态**，不是可接受的兼容模式。它们只可用于历史清理、隔离协议实验或旧 backend 回归，
不得出现在 `codex-web` 正式启动选择中。

### 0.2 成功先例的正确迁移方式

从 dsh-web/opencode-web 迁移的第一原则不是 adapter 文件布局，而是：

> 先让官方 GUI 连接将被 CordCode 复用的那个官方服务实例，再把 CordCode 作为第二客户端接入。

模块分层、事件泵、session binding、SSV2 和 capability 组织只能在该宿主拓扑已经证明后借鉴。
不能先完成一个自有官方 runtime 的完整 adapter，再把 Desktop 接线留到产品末期。

### 0.3 代码隔离仍然成立

新增实现与现有 `codex` backend 保持代码来源独立，达到本文退役门槛后再下线旧入口。

这里的“独立”首先是代码来源上的独立：

> **`agent/codex-web/` 必须从一个空目录开始。禁止复制、改名、裁剪或包装
> `agent/codex/` 后把它称为新 backend。**

如果需要借鉴 CordCode backend 的工程骨架，只借鉴 `agent/dsh-web/` 的模块分层、官方服务生命周期、
事件泵、session binding、SSV2 pathless 接线和 capability 组织方式；Codex 的请求、事件、状态机、
审批、提问、重连与错误语义全部从 Codex 官方源码的客户端实现重新建立。

`codex-web` 的定义是：

> **Codex 官方长驻 app-server 的 API 客户端 + bridge-v1 协议翻译器。**
> session 列表、历史、创建、续聊、流式事件、审批、提问、模型和用量均优先以官方 API 为唯一数据面；
> MacBridge 不直接解析或写入 `~/.codex/sessions/**/*.jsonl` 来补造这些事实。

本路线不是把现有 `agent/codex` 改名，也不是简单把 stdio 换成 WebSocket。它必须先证明 §0.1 的
宿主拓扑，再证明同一 daemon 可以承担 catalog、history、turn、interaction 和 live stream。
顺序不可倒置：iOS 自己发消息成功、adapter 单测通过或 TUI 共用 daemon，均不能单独授权后续产品面。

#### 0.3.1 记录在案的豁免：rollout 尾部冷用量

codex-web 的冷用量（已加载 thread 的当前 context 占用）读取，在官方无对应 RPC 的前提下，允许唯一一条受控文件路径：仅打开官方 `thread/read` 返回的 `Thread.path`，tail 读取 8MB 内最新的 `event_msg/token_count` 记录。该路径登记为记录在案的豁免，约束：

1. 契约 fixture 冻结形状，不吻合即弃用并打诊断，不静默回退；
2. CLI 版本门控（已验证版本族外不走文件路径）；
3. descriptor 与日志显式标注 `usage-source: rollout-tail-experimental`；
4. 该路径只读、不做 session 发现或第二目录；
5. 官方提供冷用量 RPC 后立即退役本路径。

除本豁免外，§0.3「官方 API 唯一数据面」红线对其余全部用量事实仍然生效。

> 落款：owner 批准 2026-08-26；源码对齐审计 §3.3-C1（[2026-08-25-codex-web-source-parity-audit.md](2026-08-25-codex-web-source-parity-audit.md)）；监工指令 2 号。

## 1. CordCode 初衷如何约束本设计

CordCode 不是新的 Codex harness。它是用户 Mac 上官方 Codex 工作流的 iOS 延伸：

- **零迁移**：继续使用用户已有的 Codex 工作区、账号、配置、session 和官方/第三方 provider；
- **官方真相**：Thread、Turn、Item、模型与权限事实来自 Codex 官方 runtime；
- **双向接力**：iOS 创建的 session 能被 Mac 官方客户端继续，Mac 创建的 session 也能在 iOS 继续；
- **不锁定**：停用 CordCode 后，用户仍可在官方 Codex 中使用同一批 session；
- **不自托管**：MacBridge 可以探测、启动和连接官方 app-server，但不实现自己的 agent runtime、
  session store 或模型代理；
- **第三方 provider 不降级**：官方 app-server 从用户 Codex 配置解析出的 provider/model 必须原样继承，
  不能用 MacBridge 手写白名单替代；
- **失败可见**：官方 API 缺能力、版本漂移、写入所有权冲突或外部事件不可见时直接报错，
  不用假数据、磁盘猜测或静默 fallback 制造“已经同步”的表象。

因此本文明确禁止：

- 自建 Codex event store 并把它升级为真相源；
- MacBridge 直接写 Codex rollout、SQLite 或 `config.toml`；
- 为了补齐 API 盲区，私自生成 session/title/model/status；
- 把文件轮询包装成“官方 API 实时流”；
- 让 iOS 直接连接 app-server 端口，绕过 Bridge 配对、能力协商与 Relay 加密链路。

## 2. 当前基线：为什么必须新建，而不是宣称现有 Codex 没有 API

### 2.1 现有 `codex` 已经使用 app-server

当前 CordCode 产品默认是 `app_server` 模式，不是每轮重新运行 `codex exec`：

- 默认每个活跃 session 通过 stdio 启动自己的 `codex app-server`；
- 显式配置 `-codex-app-server-url` 时可连接共享 WebSocket service；
- `thread/start` / `thread/resume`、`turn/start` / `turn/interrupt`、文本/思考 delta、审批和提问
  已经在 `agent/codex` 中实现；
- catalog 已有长驻 client 调用官方 `thread/list`；
- 冷 rich history 与外部 turn 生命周期仍有 rollout JSONL/file relay 路径。

所以新路线的价值不能写成“终于从 `codex exec` 升级到官方 API”。正确差异是：

| 维度 | 旧 `codex` | 新 `codex-web` |
|---|---|---|
| 产品身份 | 稳定 Codex backend | 独立实验 backend |
| 活跃 session | 默认每 session 一个 stdio app-server | 一个官方长驻共享 app-server/daemon |
| catalog | 长驻 app-server `thread/list` | 同一共享服务 `thread/list` |
| 冷历史 | rollout rich-history 仍参与 | 官方 `thread/read` 为一期基线 |
| 外部 turn | transcript file relay 是关键来源 | 必须先证明共享服务的官方 live event；不准伪装 |
| session 文件 | 仍有只读解析路径 | 主路径零直接读写 |
| 迁移方式 | 保留不动 | 独立入口并行 A/B，过门槛后再退旧入口 |

### 2.2 空目录原则：不是复制旧 `agent/codex` 再升级

`codex-web` 在实验期需要独立回答“共享官方服务是否更好”。若直接重构旧 backend：

- 无法区分改善来自共享服务、history API，还是旧逻辑改动；
- 写入所有权和 cache 串扰会污染比较；
- app-server experimental API 漂移时没有稳定回退入口；
- 真机发现退化后无法立即切回旧行为。

因此旧 `agent/codex` 在实验阶段保持行为冻结，而 `agent/codex-web/` 的第一份提交必须表现为
**从零新增文件**，不是对旧目录的批量复制/rename。以下行为全部禁止：

- `cp -R agent/codex agent/codex-web` 后逐步修改；
- import `agent/codex`，或用 type alias/wrapper 复用旧 `Agent`、session、catalog、history、
  passive subscriber、file relay、codec 与 cache；
- 把旧文件复制进新目录，仅通过改 package/name 形成“新实现”；
- 抽取一个实质上仍由旧 backend 状态机控制的共享基类，再让 `codex-web` 套壳；
- 用旧 rollout/file-relay fixture 反向定义官方 app-server 的 API 形状。

允许复用的只有两类：

1. `core`、bridge-v1、SSV2、通用目录/git 等本来就属于跨 backend 的公共接口；
2. 在 `codex-web` 已依据官方源码和同版本真实样本独立实现并稳定后，另案评审是否抽取真正无状态、
   无 backend ownership 的公共工具。不得为了省第一版工作量，预先从旧 backend 抽代码作为起点。

### 2.3 可以抄什么：dsh-web 抄架构，Codex 官方源码抄语义

`codex-web` 有两个明确且互不替代的参考源：

| 参考源 | 可以借鉴 | 不能借鉴/推断 |
|---|---|---|
| `agent/dsh-web/` | package 分层、Probe → official managed service、agent-level event pump、thin session binding、interaction registry、diagnostics、pathless hydrate、descriptor/capability 组织 | DSH 的 RPC method、payload、批问答、重连、先答者语义和错误码 |
| Codex 官方源码 | app-server client 并发模型、initialize、typed request、ordered event consumption、server request resolution、thread/turn/item reducer、session lifecycle、重连与错误处理 | CordCode bridge-v1、SSV2 或 iOS 产品语义 |

建议的新目录文件组织可以参考 dsh-web 的职责边界，例如：

```text
agent/codex-web/
  codexweb.go          # Agent 注册与依赖组装
  lifecycle.go         # 官方 daemon/shared service 探测与归属
  transport.go         # WS/UDS/proxy transport
  rpc.go               # JSON-RPC correlation + server request response
  sessions.go          # thread/list/read 与 catalog
  history.go           # official Thread/Turn/Item → rich history
  session.go           # thin thread binding + turn actions
  events.go            # agent-level ordered event pump
  codec.go             # official app-server event → core.Event
  interactions.go      # approvals/questions/elicitation registry
  models.go            # model/profile/config read mapping
  diagnostics.go
  wire_descriptor.go
  testdata/official-<version>/
```

这只是职责骨架，文件内容不能从 `agent/codex` 搬运。Codex 语义实现优先对照：

- `codex-rs/app-server-client/src/lib.rs`：官方 client facade、request/notification/server request、
  ordered event queue、错误分层与 bounded shutdown；
- `codex-rs/app-server-client/src/remote.rs`：远端 transport、连接和请求处理；
- `codex-rs/tui/src/app/session_lifecycle.rs`：官方 session start/resume/close 行为；
- `codex-rs/tui/src/app/app_server_events.rs`、`thread_events.rs`、`thread_routing.rs`：官方 UI
  如何分发、缓存和路由 app-server 事件；
- `codex-rs/app-server/tests/suite/v2/`：请求顺序、通知顺序、审批、提问、断线和 ownership 的
  官方可执行契约。

所谓“直接抄官方源码”是指：在许可证允许范围内，按上述官方调用链逐段移植其**已验证算法和状态机**
到 Go，并在代码注释中记录上游文件/commit；不是只看 README 后自行设计一个看似等价的客户端，
更不是把 Rust 文本机械翻译后跳过真实样本和 bridge 语义评审。

### 2.4 OpenCode Web 绕路的教训必须在本项目封死

`opencode-web` 初期以旧 `agent/opencode` 为核心升级，结果把 CLI backend 的历史假设、状态机、fallback
和官方 Web API 混在一起，产生不兼容与多 writer 打架，最后不得不通过
[2026-08-20-opencode-web-source-first-convergence-plan.md](2026-08-20-opencode-web-source-first-convergence-plan.md)
重新收敛为 source-first adapter。

`codex-web` 不重复这条路径：

```text
禁止：legacy codex backend → 改 transport/补 API → 不断兼容 → 最后重写

要求：空 codex-web 目录
        → dsh-web backend 架构骨架
        → Codex 官方客户端调用链 + server/protocol 源码
        → 同版本真实样本
        → bridge-v1 显式翻译
```

旧 `agent/codex` 只允许作为：

- CordCode 需要接入哪些 `core` interface、handler、wire descriptor 和 iOS capability 的索引；
- 已发生过的 rollout identity、EOF 假完成、delta/completed 重复、provider 非流式等故障清单；
- A/B 对照与回滚对象。

它不能证明 Codex 官方 API 的 request shape、event ordering、session ownership 或错误语义。

### 2.5 历史故障必须被新路线结构性消除

旧 backend 仅提供故障证据，不提供新实现。下表把已发生问题转成 `codex-web` 的结构约束与明确测试，
避免“旧代码未复制，但旧故障在新 adapter 中被重新发明”。

| 历史故障 | 根因 | `codex-web` 的结构性消除机制 | 验证落点 |
|---|---|---|---|
| rollout/thread identity 漂移，历史与 live 对不上 | 以文件路径、扫描结果或本地别名代替官方 identity | 全链只用 backendId + threadId + turnId + itemId + connection epoch；冷基线和 live reducer 都来自官方 API | contract fixture；§9 SSV2 删除 checkpoint 重建；identity merge 定向测试 |
| EOF/暂时无增量被误判为 turn 完成 | 文件 relay/读取边界被当作 lifecycle 真相 | 仅官方 `turn.status`、`turn/completed` 与 terminal item 可封口；active/unknown/NotLoaded 不本地猜完成 | §9.2 active/idle/failed/interrupted/NotLoaded fixture；terminal 唯一终态测试 |
| delta 与 completed snapshot 重复正文或重复工具结果 | 两条输入路径分别 append，再用正文相似度补救 | `(threadId, turnId, itemId)` reducer 合并 delta/snapshot；禁止正文启发式去重 | codec started/delta/completed replay；同 item exact-identity 去重测试 |
| 第三方 provider 单帧输出被误诊为 Bridge 非流式 | 未分层测量 upstream、app-server、Bridge batching 与 iOS cadence | 不制造 token；记录 app-server 实收 delta 和 Bridge/iOS 帧级指标，能力结论区分 provider 与 adapter | §8.1 分层诊断；§13.2 A/B 帧级指标与 custom-provider 样本 |

上述四行均为新 backend 的回归门槛。测试失败时必须修复对应结构分歧，不能启用旧 rollout/file relay
作为 fallback 来“消除”现象。

## 3. Source-first 证据基线与未决事实

### 3.0 证据与实现优先级

每一个 `codex-web` 功能必须按以下顺序建立，低优先级证据不得覆盖高优先级事实：

1. **Codex 官方客户端真实调用点**：先确认官方 TUI/Desktop/extension 如何调用 app-server；
2. **Codex 官方 server/protocol/reducer**：确认 method、wire type、事件顺序、ownership 和错误；
3. **同版本真实脱敏样本**：确认当前二进制的物理载荷与源码一致；
4. **bridge-v1 显式映射**：决定 CordCode 如何保留官方语义；
5. **dsh-web 架构骨架**：决定文件边界、生命周期与 SSV2 接线方式；
6. **旧 codex backend**：最后只核对 CordCode 接线点和历史回归，不参与定义上游行为。

完整证明元组为：

```text
官方客户端 call site
  + 官方 server/protocol/reducer
  + 同版本真实样本
  + 明确 bridge-v1/SSV2 映射
  + 定向 replay/integration test
```

缺任何一项都只能标记为 `unverified`，不能通过复制旧 backend 或增加 fallback 继续编码。

### 3.1 已由官方源码确认

当前 pin 的 `codex-rs/app-server/README.md` 与协议源码确认：

- transport 支持 `stdio://`、`unix://`、`unix://PATH`、`ws://IP:PORT`；
- WebSocket listener 提供 `/healthz` 与 `/readyz`，loopback 可无远程 auth，非 loopback 强制认证；
- app-server v2 提供 `thread/*`、`turn/*`、`item/*`、`model/list`、`config/read`、
  `permissionProfile/list` 等 JSON-RPC surface；
- turn 的流式生命周期为 `turn/started` → `item/started` → item-specific delta →
  `item/completed` → `turn/completed`；
- 审批、提问、MCP elicitation 等是 server-initiated JSON-RPC request，客户端必须回 response；
- `thread/read` 可只读获取已存储 thread，`includeTurns` 可包含历史且不 resume；
- `thread/turns/list` 是 experimental，不能作为一期不可替代的唯一历史路径；
- 同一个 paginated thread 同时只能由一个 app-server process 持有写权限；冲突时
  `thread/resume` / `thread/archive` / `thread/delete` 可返回 `-32600`，只读请求仍可用；
- `thread/unsubscribe` 只取消当前连接订阅，最后一个订阅离开后 thread 仍可能保持加载约 30 分钟，
  不能把 unsubscribe 当成即时释放写所有权。

### 3.2 仓库内已有真实样本

现有 `agent/codex/testdata/` 已保存并用于测试的脱敏样本包括：

- `thread_list_sanitized.json`：真实 `codex-cli 0.147.0-alpha.6.5` 的 `thread/list` 请求/响应；
- `turn_started.json` / `turn_completed.json`；
- `item_agentMessage_delta.json`、reasoning delta；
- command、file change、MCP、dynamic tool、context compaction、plan；
- `structured_user_input/` 下的 ServerRequest / response 契约。

这些样本证明现有 codec 的来源，不等于自动冻结 `0.148.0-alpha.21`。Phase 0 必须针对实施时实际
安装版本重新脱敏采集；若新样本与旧 fixture 不同，以新版本官方响应为准，并记录版本漂移。

### 3.3 目前不能当作既成事实的关键点

以下内容在 Phase 0 实验前只能写成假设，不能广告为 capability：

1. 目标二进制中，普通终端 `codex` TUI 在受控 runtime 选择下，能否向第二个 connection 提供
   完整且有序的同 thread item delta；
2. Codex Desktop、VS Code extension 与 CLI 是否使用同一个可被第三方 client 订阅的 daemon；
3. 一个连接是否会收到由另一个连接创建、但自己未 start/resume 的 thread 的完整 item delta；
4. 断线重连后官方服务会重放哪些事件，哪些必须由 `thread/read` 重建；
5. experimental `thread/turns/list` 在目标版本上的分页与兼容范围。

官方源码已经证明共享机制存在，但覆盖面有明确边界：

1. **自动启动**：只有 `interactive.agents_overview && remote.is_none()`（`codex agents`）会在 CLI
   入口自动启动 daemon（`codex-rs/cli/src/main.rs:2588-2602`）；
2. **普通默认 TUI 会复用运行中的 daemon**：无 `-c` 覆盖、非 strict、无不可重放启动覆盖时，
   `can_reuse_implicit_local_daemon()` 返回 true；TUI 探测默认 Unix control socket，存活则选择
   `AppServerTarget::LocalDaemon`，否则选择 Embedded（`tui/src/lib.rs:437-459,850-920`；
   `tui/src/startup_orchestration.rs:138-190`）；
3. **覆盖启动保持隔离**：带 `-c`、strict config、workload identity 或不可重放覆盖时，即使 daemon
   已运行也不会隐式复用，TUI 进入 Embedded runtime；
4. **同 daemon 多连接订阅是官方模型**：`try_ensure_connection_subscribed` /
   `try_add_connection_to_thread` 支持多个 connection 订阅同一 thread
   （`app-server/src/thread_state.rs:533-581`）。

因此核心 Gate 不再问“机制是否存在”，而是验证目标二进制和 Desktop 宿主的实际接线、事件完整性与
多连接行为。Terminal 的 LocalDaemon 路径只证明机制和 transport；只有 Desktop 与 CordCode 同 daemon
且 T1 双向竖切通过，才能得到产品 PASS。Desktop 进入 Embedded 是产品阻断，不得用 Terminal PASS 抵消。

### 3.4 Bug 修复纪律：先找官方实现的第一处分歧

实施后遇到 Codex API、流式、history、approval、question、重连或 ownership bug 时，固定按以下顺序：

1. 在相同版本官方客户端复现或找到对应场景；
2. 阅读官方客户端 call site，确认它实际发送什么、订阅什么、何时更新状态；
3. 阅读官方 server/protocol/reducer 与相关 v2 test，确认服务端承诺；
4. 捕获官方客户端与 app-server 的真实时间线；
5. 捕获 codex-web → bridge-v1 → SSV2 的同场景时间线；
6. 找到两条时间线的**第一处分歧**，只修这一处；
7. 将官方样本和该 bug 的回归测试一起归档。

以下做法禁止：

- 先在旧 `agent/codex` 搜一个相似 handler，复制后不断打补丁；
- 因为字段名相似就增加递归 JSON 搜索、多个 generation fallback 或正文启发式合并；
- 官方源码已有 request queue、event queue、server request registry 或 reconnect 算法时重新发明一套；
- 一个修复没有改变 owner 现象后，在同一假设上继续叠第二个、第三个修复。

一次定向修复未改变现象，就必须停止产品代码修改，保留失败证据并回到官方调用链重新定位。
修复报告必须写明：官方实现位置、真实样本、第一处分歧、移植点和回归测试。只写“测试通过”不构成
官方行为等价证明。

## 4. 目标架构

```text
CordCode iOS / iPadOS
        │
        │ bridge-v1 over LAN 8777 / HPKE Relay
        ▼
CordCode Link / go-bridge
        │
        ├── backend identity: codex-web
        │   ├── official service lifecycle probe
        │   ├── JSON-RPC request/response correlator
        │   ├── Thread/Turn/Item → core.Event translator
        │   ├── ServerRequest → permission/question translator
        │   ├── SSV2 pathless hydrate + live ingest
        │   └── diagnostics / version-contract gate
        │
        ▼
Official Codex app-server daemon / shared service ◄──── Codex Desktop
        │                                               （第二 connection，禁止 Embedded）
        ├── user Codex auth and config
        ├── official/custom provider and model resolution
        ├── official thread/turn/item lifecycle
        └── official persistence under CODEX_HOME
```

MacBridge 不把官方 JSON-RPC 原样暴露给 iOS。翻译边界固定在 MacBridge：

- 上游变化只影响 `agent/codex-web` 的协议适配与 fixture；
- iOS 继续消费稳定的 bridge-v1 session、message、tool、permission、question、usage 语义；
- Relay 只传 CordCode 加密帧，不知道 app-server 地址、认证或 Codex 内容结构。

## 5. 模块与身份

### 5.1 独立身份

- Go 目录：`agent/codex-web/`
- Go package：`codexweb`
- agent 注册名：`codex-web`
- wire kind：`codex-web`
- iOS：`BackendKind.codexWeb`
- 产品显示名：`Codex Web`（实验期；退役旧入口时再决定是否恢复显示为 `Codex`）
- legacy cleanup state：`codex-web-managed-server.json` 只识别并安全清理旧 owned process；正式路径不得创建

同一个官方 `thread.id` 在 `codex` 与 `codex-web` 中会作为两个 backend scope 的 session 出现。
SSV2 key 必须包含 backend id，禁止只按 thread id 合并。实验期 UI 出现两个模式下的同名 session
属于预期隔离，不允许用跨 backend cache 去重。

### 5.2 内部组件

建议拆分为以下职责，避免一个文件同时持有 transport、RPC、codec 和 SSV2 状态：

| 组件 | 职责 |
|---|---|
| lifecycle | exact-version gate、探测/启动官方 daemon、配置 Desktop attach、记录归属、fail-closed 诊断 |
| transport | WebSocket、Unix socket 或官方 proxy 的字节传输与重连 |
| rpc client | initialize、request id、pending response、server request response、通知分发 |
| catalog | `thread/list`、archive list、分页、官方排序 |
| history | `thread/read(includeTurns)` → rich history → pathless hydrate |
| session | start/resume、turn/start/steer/interrupt、unsubscribe |
| codec | Thread/Turn/Item/错误 → `core.Event`，无业务状态推测 |
| interaction | approval、requestUserInput、MCP elicitation 的 request-id 生命周期 |
| models | `model/list`、permission profile、effective config 的只读映射 |
| diagnostics | CLI/app-server version、transport 来源、稳定/实验能力、失败原文 |

该表描述的是从空目录新建的职责，不是把旧 `agent/codex` 的同名文件复制过来。评审时不能以
“文件名和旧模块一样”作为复用理由；每个文件都必须能追溯到 dsh-web 架构职责或 Codex 官方调用点。

## 6. 官方服务生命周期与 transport

### 6.1 选择顺序

Desktop 产品路径只允许一个运行时真相源：

1. **官方 daemon 是登录级座位，不是 MacBridge 子进程**：用户 LaunchAgent 以 KeepAlive
   循环执行官方 `daemon start`（已运行则 `alreadyRunning`，不换 PID），补位间隔必须
   短于 Desktop 断线后的首次重连（约 1s）。MacBridge 启动只探测/补位；停止或退出
   **不得** `daemon stop`、不得 bootout 该 daemon、不得杀掉 Desktop。
2. **官方 daemon 复用**：探测 Codex 官方 control socket/`daemon version`，已运行则复用；
3. **官方 daemon managed start**：座位尚未起来时调用 `codex app-server daemon start`，
   再通过 control socket 连接；
4. **Desktop attach**：登录域设置 `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`。Desktop **启动时**
   探测成功则连同一 control socket；已附着后，MacBridge 退出不应要求再重启 Desktop。
5. **失败可见**：standalone 缺失、daemon 启动失败或环境配置失败时，
   `codex-web` 标为 `not_configured`/`incompatible`，不得另起 app-server 假装可用。
   Desktop 是否附着由它自己的 `daemon version` 兼容检查决定（当前宿主是
   app-server ≥ 0.141.0），不是 `codex --version` 字符串全等。CLI patch 级
   偏差只记日志，不得因此跳过 daemon start / 登录座位。

这不是“优先级列表”，而是串行前置条件。官方 Desktop 在每次 `transport.connect()`
（含 reconnect）都会再跑 `codex app-server daemon version`（spawn timeout 2500ms）。
control socket 不在时该命令通常立刻失败，不会等满 2.5s。失败就把 `kind` 写成
`stdio`，`supportsReconnect()` 变为 false，该 Desktop 进程没有远程翻回 API。
这只允许作为异常恢复（Desktop 已经掉到私有 stdio），**不是** MacBridge 正常退出流程。
不得 kill/steal Desktop，也不得趁其仍在 Embedded runtime 时启动第二服务继续工作。
登录座位必须在 Desktop 首次重连探测前把官方 daemon 补回来；60s 周期补位盖不住这个窗口。

`-codex-web-app-server-url` 只保留给隔离 contract/e2e 与明确的非 Desktop 实验；Desktop 产品模式
不能使用它，因为当前 Desktop 已证明的入口是 local daemon Unix socket，而不是任意 loopback URL。
此前的 `managed-loopback-ws` 只保留为历史失败证据与旧 owned-process 清理输入，不再是产品 fallback。

连接 daemon control socket 有两种实现路径（Phase 0 实测修正，见 §22-2）：

- Go 直连 Unix domain socket，**讲 WebSocket over UDS**（control socket 由官方 `accept_async` 做
  WS 升级；裸 newline JSON 连接会被立即关闭；每个 JSON-RPC 消息一个 WS text 帧）；或
- 启动轻量 `codex app-server proxy --sock <path>`——它是**纯字节中继**，客户端仍需自行讲 WS。

proxy 只是 transport adapter，不拥有 agent session；与旧 backend “每 session 启一个 app-server”不同。
Phase 0 已完成断线、server request 与多连接取样（dumps/ownership、reconnect、gate-terminal）。

### 6.2 探活不等于能力就绪

WebSocket `/healthz` 或 daemon socket 可连接，只能证明进程活着。backend 可用必须依次满足：

1. transport 建立；
2. `initialize` 成功并记录 server/version/capability；
3. 发送 `initialized`；
4. `thread/list` 最小请求成功；
5. `model/list` 成功；
6. stable API 字段与冻结 contract 一致。

任一步失败：`codex-web` 标为 `not_configured` 或 `incompatible`，保留官方 JSON-RPC error 与版本信息；
不得退回 JSONL parser 假装 backend 可用。

### 6.3 进程归属

- 发现用户/官方客户端已启动的 daemon：MacBridge 只复用，绝不 stop/restart；
- 由 CordCode 首次调用官方 `daemon start`：记录 `startedByCordCode=true`，但默认退出 CordCode Link 时
  仍不停止共享 daemon，避免中断官方客户端；
- **用户可见副作用**：CordCode 启动 daemon 后，后续使用默认启动配置的普通 Terminal `codex`
  会自动 attach 该 daemon；这会把原本 Embedded 的运行位置静默改为共享 runtime。诊断必须区分
  `external-daemon-reused` 与 `cordcode-started-daemon`，后者还应显示是否观察到用户 TUI connection/thread，
  供 ownership 冲突排查；不得把两种来源都简化成 `connected`；
- 旧版本遗留的 owned `managed-loopback-ws` 仅可按 PID/argv/start-time/listen-port 四重校验后回收；
  不得恢复、复用或新建为 Desktop 产品服务；
- managed record 只存 transport 来源、端点、PID/启动时间/版本等生命周期事实，不存 token、session 内容；
- 不修改用户 Codex 自动更新、账号、provider 或全局配置。

## 7. 官方 API → bridge-v1 功能映射

符号：✅ 一期基线；🧪 有官方 experimental 面，取样与版本门控后才启用；⚠️ 官方字段存在但
CordCode 产品能力仍有证据/目录缺口，不得广告；♻️ Bridge 通用能力；⛔ 不支持/不广告。

| CordCode 能力 | 官方 app-server 面 | 一期 | 说明 |
|---|---|---:|---|
| list_sessions | `thread/list` | ✅ | 保留官方排序、cursor、archive/source 语义；请求字段以目标版本样本为准 |
| get_session | `thread/read` | ✅ | 只读，不 resume |
| rich history hydrate | `thread/read{includeTurns:true}` | ✅ | 一期稳定基线，映射为 SSV2 pathless history |
| history pagination | `thread/turns/list` | 🧪 | experimental；不能成为一期唯一历史入口 |
| create_session | `thread/start` | ✅ | cwd/model/provider/permission 只传官方支持字段 |
| resume_session | `thread/resume` | ✅ | 会取得写入 ownership；必须执行冲突保护 |
| send_message | `turn/start` | ✅ | 输入 parts 保留 text/image/localImage 等官方类型支持边界 |
| steer | `turn/steer` | ✅ | 必填 `expectedTurnId`；bridge 必须跟踪当前 active turnId；仅 active regular turn，review/compact 拒绝时原错透传 |
| stop | `turn/interrupt` | ✅ | 使用真实 threadId + turnId，不杀 daemon |
| live status | `thread/status/changed`、`turn/started/completed` | ✅ | running/idle/failed 不从文本增量推测 |
| assistant stream | `item/agentMessage/delta` | ✅ | 同 itemId 按顺序拼接，bridge 可维持既有 33ms 批处理 |
| reasoning | summary/text delta + completed item | ✅ | 分清 summary 与 raw reasoning，不重复 |
| command/tool | item lifecycle + output delta | ✅ | started/delta/completed 使用同一 itemId |
| file changes | `fileChange` item lifecycle | ✅ | iOS 仅展示轻量摘要，遵守“不做消息内 inline diff”决策 |
| MCP/dynamic tool | item + server request | ✅ | 仅对真实取样过的 variant 广告 |
| plan/todos | plan item/notification | ✅/🧪 | plan item started/completed 生命周期 ✅；`item/plan/delta` 流式为 experimental 🧪，取样和版本门控后才消费 |
| context usage | `thread/tokenUsage/updated` | ✅ | 使用官方窗口与累计量，不本地估算 |
| command approval | `item/commandExecution/requestApproval` | ✅/🧪 | 基础 request/response ✅；`availableDecisions`/`additionalPermissions` 为 experimental 字段（stable schema 剥除）。**Phase 0 实测修正（§22-1）**：`additionalPermissions` 未开 `experimentalApi` 时被 server 出站剥除，但 `availableDecisions` 在未声明 experimentalApi 的连接上仍**物理到达**（accept / acceptWithExecpolicyAmendment / cancel）；一期 UI 仍按可能缺失兼容，不依赖其稳定性；合法 decision 为 accept/cancel/结构化变体，cancel 后 turn 终态 interrupted |
| file approval | `item/fileChange/requestApproval` | ✅ | 不把“发送 response 成功”提前显示为修改成功 |
| permission request | `item/permissions/requestApproval` | ✅ | 呈现官方 `RequestPermissionProfile`，响应 `GrantedPermissionProfile + scope`；该载荷没有 `availableDecisions` |
| structured questions | `item/tool/requestUserInput` | 🧪 | README/类型均标 experimental；stdio 与 daemon WS 均已取得真实样本。**Phase 4 实测补充（§22-7）**：官方 `request_user_input` tool 要求每题 2–3 个非空 options，并自动加入自由文本 Other；合法三题在隔离 daemon WS 下完成 request → answers map → resolved → turn completed。仍须版本门控 |
| MCP elicitation | `mcpServer/elicitation/request` | 🧪 | Form / `openai/form` / Url 三类；`openai/form` 还需 initialize capability `mcpServerOpenaiFormElicitation` |
| list_models | `model/list` | ✅ | 当前 configured provider 的模型目录；顺序、hidden、reasoning efforts 全由官方目录提供，不冒充 provider 目录 |
| effective provider | `config/read` → `config.model_provider` | 🧪 只读 | v2 `Config` 是 snake_case 特例；读取 typed 当前有效 provider，保证继承用户第三方配置。`ConfigReadResponse`/嵌套 `Config` 带 `ExperimentalApi` 标记，字段形状按 Phase 0 样本冻结；不写 `config.toml` |
| new-thread provider | `thread/start{model,modelProvider}` | ⚠️ | 字段稳定，但只能创建时选择；provider id 必须来自已验证官方事实，不能手写/猜测；Phase 4 样本通过后决定是否广告 |
| switch_model | `turn/start{model}` | ✅ | 模型覆盖对本 turn 及后续 turn 生效；不改变 provider |
| list/switch provider | 无独立 provider-list / running-thread provider-switch RPC | ⛔ 一期 | `model/list` 的 typed `Model` 不含 provider；`config/read` 的 typed 字段只有当前 `model_provider`，但 flatten `additional` 可能携带未建模的 `model_providers` 表，精确存在性/组成/形状以 Phase 0 样本为准，一期禁止从该兜底字段递归提取 provider 目录；官方唯一 provider 级 RPC `modelProvider/capabilities/read` 也只读当前 configured provider。不得复制旧 backend 的 `config.toml` 写路径；官方新增稳定目录/切换 API 前不实现 iOS provider switch |
| permission profiles | `permissionProfile/list` | ✅ | beta 能力必须版本门控；不手写 profile |
| effective config | `config/read` | 🧪 只读 | response/nested config 带 `ExperimentalApi` 标记，仅用于取样冻结后的诊断/effective facts；一期不通过 API 改全局 config |
| rename | `thread/name/set` | ✅ | 以通知或重新读取确认，不做本地乐观真相 |
| archive/unarchive | `thread/archive` / `thread/unarchive` | ✅ | ownership 冲突原错可见 |
| delete | `thread/delete` | ✅ | 破坏性动作仍由现有 iOS 确认流程保护 |
| fork | `thread/fork` | 🧪 | 取得父子 thread 与 history 样本后接入 |
| unsubscribe | `thread/unsubscribe` | ✅ | 只代表取消订阅，不承诺立即卸载或释放 ownership |
| list_directory/projects | Bridge 本地目录服务 | ♻️ | 不属于 session 真相，可复用通用只读能力 |
| git/PR 操作 | Bridge 通用 git 能力 | ♻️ | 不伪装成 app-server API；由 descriptor 单独声明 |
| memory files | app-server 无 memory 文件 API | ⛔ 一期 | deliberately unsupported：AGENTS.md 是官方输入文件而非 session 真相；二期如接入只能走受限只读 AGENTS.md 路径并另案评审 |

### 7.1 事件映射红线

- `turn/started` 是唯一 turn 开始真相，不能因为首个 delta 到达而补造开始；
- `turn/completed.status`、`error` 和 terminal item 是完成/失败真相，不能把当前读取到 EOF 当完成；
- `turn/completed` 携带 `turn.status/error` 与 `turn.items`；`itemsView` 可能是 Full/Summary/NotLoaded。
  完整工件仍以持续消费 `item/*` 为准，不能假设 completed 自带全量 item；
- 每个 item 以 `(threadId, turnId, itemId)` 作为 reducer 身份，禁止正文相似度去重；
- delta 与 completed snapshot 必须避免双发；codec 从官方 reducer/样本独立实现，旧 codec 只作输出回归对照；
- 未识别通知记录 method/version，不能导致连接崩溃；若它影响已广告 capability，则该 capability
  fail closed，而不是递归猜字段；
- **通知分级（Phase 0 实测，§22-4）**：`thread/started`、`thread/status/changed` 等全局通知在订阅前
  也会到达所有连接；`turn/started`、`item/*`、`turn/completed` 只发已订阅连接且**不重放**。
  mid-turn attach 收到后续 delta 与唯一终态，但 `turn/started` 不补发——turn 开始事实由
  `thread/status/changed(active)` + `item/started` 推导，完成仍只认 `turn/completed`；
- JSON-RPC error 的 code/message/data 原样进入诊断，面向 iOS 的错误可以本地化，但不能丢原文。

### 7.2 审批与提问

server-initiated request 必须有独立 registry：

- key 至少包含 connection epoch + request id + threadId + turnId；
- iOS 只为已在 bridge registry 打开的 session surface 交互，避免抢答用户正在 Mac 端处理的请求；
- response 回原 JSON-RPC request id；
- `serverRequest/resolved` 或相关 item completed 才是 UI 收口信号；
- 断线时清理旧 epoch pending，不向新连接重放旧 response；
- 多题 `requestUserInput` 必须用真实样本确认官方批结构，再映射到 iOS 单题提交模型；
- **transport 证据不可类推**：stdio 与 daemon WS 分别保留真实样本；模型 tool 输入若违反官方
  “每题非空 options”约束会收到 function-call error，不能误判成 server-request 路由失败；
- Mac 与 iOS 同时可答时必须实测“先答者得”行为，不能沿用 DSH 的结论。

### 7.3 源码推导的 wire shape 索引（待 Phase 0 样本确认）

本表集中记录当前源码 pin `536f86e5cc9ec1ff38457d099bf320b9d08eeeba` 已核实的 typed shape，供
Phase 0 设计采样与 adapter skeleton；它**不是 wire fixture**。每一行只有在目标二进制真实脱敏样本
与生成 schema 同时吻合后，才能从“源码推导”升级为 contract test 输入；不吻合时以样本为准并回写本表。

| Surface | 当前 pin 的源码推导 | Phase 0 必须冻结 |
|---|---|---|
| `model/list` | response `data: Vec<Model>` + cursor；typed `Model` 不含 provider id | model 全字段、hidden/effort、分页及 custom-provider 目录行为 |
| `thread/start` | stable `model`、`modelProvider` 可选；provider 仅创建时输入 | 字段剥除/默认值、custom provider id 来源、thread 回显的 effective model/provider |
| `turn/start` | 可选 `model`，对本 turn 及后续 turn 生效；无 `modelProvider` | model override 的生效范围、失败原文及 completed 回显 |
| `turn/steer` | `expectedTurnId` 必填；仅 active regular turn | regular/review/compact 三类结果与 stale turnId error |
| `config/read` | response `config` 内部是 snake_case；typed `model_provider`；flatten `additional`；response/config 带 `ExperimentalApi` | **Phase 0 已裁决（§22-3）**：0.149.0-alpha.4 实测 `additional = {}`，不含 `model_providers`；样本 dumps/models-config |
| `item/tool/requestUserInput` | experimental；批 questions，答案按 question id 返回；官方 tool 输入每题必须 2–3 个 options，server params `isOther=true` 允许自由文本 Other；stdio 单题与 daemon-WS 三题已冻结（§22-7） | blocking(Plan mode) 与失败/取消的完整物理载荷仍待补样 |
| command approval | 基础 request/response 稳定；两字段 experimental（schema 剥除）；**Phase 0：wire 上 availableDecisions 不剥除、additionalPermissions 剥除**；decision 枚举 accept/cancel/结构化，cancel→interrupted | Phase 0 已采（dumps/interaction）；resolved→completed 顺序已入样本 |
| permission approval | `RequestPermissionProfile` → `GrantedPermissionProfile + scope`；没有 command 的 `availableDecisions` | allow/deny/scope 与失败/断线行为 |
| MCP elicitation | Form / `openai/form` / Url；`openai/form` 依赖 initialize capability | 三 variant request/response、capability absent 与取消/失败行为 |
| plan | item started/completed 生命周期稳定；`item/plan/delta` experimental | 无 delta 与有 delta 两组 reducer 时间线 |
| `turn/completed` | 携带 status/error/items；`itemsView` 可为 Full/Summary/NotLoaded | 三种 itemsView 与 live `item/*` 合并后不重不漏 |

禁止从表格字段名生成递归 JSON 猜测器。未取样 variant 必须保持 capability 关闭，而不是靠 optional map
或 unknown-field 搜索在生产中试探。

## 8. 流式输出与“外部 Codex turn”核心 Gate

### 8.1 iOS 自己发起的 turn

`codex-web` 通过同一长连接收到 `item/agentMessage/delta`，理论上可以减少每-session app-server
生命周期与连接切换成本。但旧 `codex` 已经能收到官方 delta；因此这条场景的预期是“更稳定/更低开销”，
不是未经测量就承诺“从分块变逐字”。

如果一轮只收到一个大 delta，应先区分：

- provider/upstream 本身是否真流式；
- app-server 是否收到多个 upstream delta；
- codex-web 是否按 itemId 正常转发；
- Bridge 33ms batching 与 iOS 约 66ms render cadence 是否只是正常平滑层。

已知第三方 provider 可以在上游硬编码非流式；换 backend 不会把单帧上游凭空变成 token stream。

### 8.2 Mac 官方客户端发起的外部 turn

这是决定 `codex-web` 是否允许存在产品实现的硬门槛，不只是退役旧 backend 的比较项。Phase 0
必须逐宿主验证，并控制 daemon 状态与客户端启动配置：

| 宿主/场景 | 前提条件 | 必须观察到的官方证据 | 判定 |
|---|---|---|---|
| Terminal 普通 `codex` TUI | **daemon 已运行；默认启动配置；无 `-c`/strict/non-replayable override** | TUI 选择 LocalDaemon；codex-web 收到同一 thread 的 `turn/started`、多个 delta、`turn/completed` | transport/daemon 佐证；不能代替 Desktop Gate |
| Terminal 普通 `codex` TUI | daemon 未运行；先启动 TUI | TUI 选择 Embedded；之后启动 daemon/codex-web 不应伪称收到该 live turn；list/read 能力另测 | 已知隔离边界，不计整体 FAIL |
| Terminal 普通 `codex` TUI | daemon 已运行；带 `-c`、strict 或不可重放覆盖 | TUI 选择 Embedded，codex-web 不应串入该 live stream | 已知隔离边界，不计整体 FAIL |
| Codex Desktop / ChatGPT Codex | daemon 已运行；`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`；CLI/daemon 版本兼容；Desktop 重新启动并继承环境 | Desktop 无私有 Embedded app-server 子进程；FD 指向同一 control socket；codex-web 收到同一 thread 全生命周期 | **产品拓扑 Gate；必须 PASS** |
| VS Code Codex extension | 记录其实际 app-server endpoint/进程归属 | 同上；若连接独立 app-server，记录隔离事实 | Phase 0 实测，不从 TUI 推断 |
| codex-web/iOS 自己发起 | daemon 运行，记录 Mac 客户端启动模式 | Mac 官方客户端能看到并续聊同一官方 thread | 双向串行接力 Gate |

判定规则（取代 v1.x）：

- **PASS**：Desktop 与 CordCode 的进程/FD/socket 证据证明同一 daemon；Desktop→CordCode live turn、
  CordCode→Desktop 同 thread 续聊、双方同时打开无 writer 冲突三项均通过；Terminal 结果仅作旁证；
- **EVIDENCE-ONLY**：TUI 共用 daemon，或 Desktop/CordCode 只能共享 list/read/store。只允许继续源码、
  宿主、进程和最小探针调查；不得创建/扩展 catalog、history、turn、interaction、model 或 iOS 产品功能；
- **FAIL/BLOCKED**：Desktop 仍持有独立 runtime、任一端必须退出另一端才能写、出现 active writer
  冲突、无法观察外部 live turn，或只能靠 managed-loopback 工作。停止产品实施并重新打开设计。

不存在能放行后续 Phase 的 `PARTIAL`。共享 store 的串行可见性不是双向接力，也不能被登记为产品 PASS。

### 8.3 重连

一期不假设 app-server 提供任意事件 replay cursor。断线恢复固定为：

1. 新 connection epoch 完成 initialize；
2. 对用户当前打开的 thread 做只读 `thread/read(includeTurns:true)` 冷校准；
3. 只有确需继续写入时才 `thread/resume`，避免无意义抢 ownership；
4. SSV2 以官方 item/turn identity merge，不能把历史 snapshot 当新 live turn；
5. 恢复后的 pending approval/question 只有在官方重新发送 server request 时才 surface；
6. 无法证明已经完成的交互保持未知/待校准，不本地合成成功。

## 9. SSV2 真相清单

- **真相 owner**：Codex 官方 app-server 及其官方持久化层；
- **唯一 writer**：所有 thread/turn/config 写入只经官方 JSON-RPC；MacBridge 零直接写 rollout/SQLite/config；
- **MacBridge 角色**：短期 RPC correlation、live reducer、pathless projection cache；均可丢弃重建；
- **冷基线**：`thread/read(includeTurns:true)`；`thread/turns/list` 仅在 experimental gate 后用于分页优化；
- **live 基线**：同一 app-server connection 的 Thread/Turn/Item/ServerRequest；
- **active 写入口**：thread start/resume/name/archive/unarchive/delete/fork，turn start/steer/interrupt，
  以及 server-request response；全部经官方 API；
- **禁止旁路**：`codex-web` package 不调用 rollout parser、file relay、session path scanner；
- **失败呈现**：API/contract/ownership/provider/stream 错误均可见，不用文件结果伪造 RPC 成功；
- **事务 identity**：backendId + threadId + turnId + itemId + connection epoch；
- **重建原则**：checkpoint 可删除；删除后只靠官方 API 能恢复到同一可见历史。

### 9.1 SSV2 / projection kernel 接线清单

`codex-web` 进入 SSV2 pathless rich-history 家族时，必须逐项完成下表。任一处漏接都可能表现为
“backend 能列 session，但冷 hydrate、尾部封口或 source 切换静默失效”；因此该表同时是 Phase 2
code review 与定向测试清单，而不是实现提示。

| 接线点 | 当前 Go 入口/符号 | `codex-web` 要求 |
|---|---|---|
| backend 支持判定 | `handlers_projection.go` / `backendSupportsProjectionHydrate` | 明确加入 `codex-web`，只在官方 rich history contract 可用时返回 true |
| request 强制冷校准 | `handlers_projection.go` 的 request-level `forceCold` 集合 | archive/source/transport 等会令 projection 基线失效的变化必须覆盖 `codex-web` |
| source 变化强制冷校准 | `handlers_projection.go` 的 `sourceChanged` → `forceCold` 集合 | daemon endpoint、thread source 或 backend identity 改变后不得沿用旧 checkpoint |
| hydrate source 准备 | `prepareProjectionHydrateSource` 的 backend name switch 与 pathless 条件 | 返回无本地路径的官方 thread source；不得生成、查找或借用 rollout 路径 |
| live/kernel 与 cold 分流 | `handlers_projection.go` 的 backend-specific live/pathless 分支 | live reducer 与 `thread/read(includeTurns:true)` 冷基线使用同一官方 identity 合并 |
| hydrate range 生产 | `produceProjectionHydrateRange` 的 allow-list 与 rich-history dispatch | 接到 `streamBackendRichHistoryProjectionEvents` 等价路径，不能落入文件范围读取 |
| pathless kernel 家族 | `projection_kernel.go` / `pathlessRichHistoryBackend` | 将 `codex-web` 声明为 pathless；checkpoint 不得携带 session file path |
| descriptor/capability | `agent_descriptor.go` | 暴露真实 instance status、history/live/interaction 能力；Gate 未过的能力 fail closed |
| driver 启动与配置 | `main.go` 的 blank import、default drivers、flags/config/agent creation | 独立注册 `codex-web`，不覆盖或别名到 `codex` |
| server identity/lifecycle | `server.go` 的 backend registration、identity family 与 lifecycle sets | wire/backend/cache identity 独立；并存期不得共享旧 `codex` 的运行态 |
| provenance 禁区 | 旧 `codex` store/file/transcript 路径 | 不进入旧 store、rollout、file-relay、session scanner 或 `transcriptindex` 分支 |

### 9.2 尾部封口与 activity 真相

- hydrate 是否可以把尾部 turn 封为 terminal，以官方 `thread.status` 与最后一个 `turn.status` 为权威；
- active、unknown 或 `itemsView=NotLoaded` 且无法证明 terminal 的尾部，不得根据“当前没有新 delta”本地封口；
- 只有官方状态表明 completed/failed/interrupted，且相关 item identity 已完成冷/live merge，才提交 terminal projection；
- Phase 0 fixture 必须覆盖 active thread、idle thread、失败/中断 turn 与 NotLoaded 视图，并据此判断
  adapter 是否还需实现 `SessionActivityProbing` 等价能力。若官方 status 已完整覆盖，就不额外造一套
  本地 activity detector；若样本证明缺口，则以独立 capability 补齐，不能用时间阈值猜测完成。

## 10. 与旧 `codex` 并存及写入所有权

### 10.1 并存规则

- 旧 `codex` 源码、注册和产品入口在实验期保留；
- `codex-web` 使用独立 backend/wire/iOS identity；
- 两者可同时 list/read 同一批官方 session；
- 不允许同时 resume/write 同一个 thread；
- A/B 流式比较默认使用两条内容相同但 id 不同的测试 session；
- 若必须测试同一 thread 接力，先结束旧 backend 持有的 app-server session，再由新 backend resume；
- 新 backend 的 shared daemon 可能在 unsubscribe 后继续持有 thread，切回旧 backend 前必须观察
  `thread/closed`/notLoaded 或由明确的进程退出释放，不能凭 UI 已离开判断。

### 10.2 冲突处理

`-32600` ownership error 必须翻译为明确的“该会话正由另一个 Codex app-server 持有”，并携带：

- thread id（日志可脱敏）；
- 当前 backend/transport 来源；
- 请求方法；
- 官方 error message；
- 建议用户回到当前持有者完成/退出，而不是 MacBridge 自动 kill 其他官方客户端。

禁止自动终止未知 Codex/app-server 进程来抢 session。

## 11. 安全、认证与版本边界

### 11.1 本地安全

- managed WebSocket 只绑定 `127.0.0.1`，禁止 `0.0.0.0`；
- 优先 Unix socket/官方 daemon，socket 权限与路径由官方实现管理；
- 非 loopback app-server 不属于一期；不得为了远程方便关闭认证；
- iOS 永远只连接 CordCode Bridge，LAN 配对与 Relay HPKE 边界不变；
- 诊断与 managed state 不记录账号 token、provider key、prompt 或完整 session 内容；
- 不把 app-server control socket 暴露进 Relay。

### 11.2 API 稳定性

`codex app-server` 命令与部分 API 在当前二进制中明确标记 experimental；开启
`capabilities.experimentalApi` 的方法/字段无向后兼容保证。因此：

- 一期主链只依赖目标版本已生成 schema、已抓真实样本并已通过 contract test 的 surface；
- stable 与 experimental fixture 分开；
- 初始化记录 CLI version、app-server version、capabilities；
- 未知版本先跑只读 probe，不直接广告全部 capability；
- experimental 能力逐项开关，不能因为打开总 capability 就默认全部支持；
- `config/read` 的请求参数本身没有 experimental 字段，但 `ConfigReadResponse` 与嵌套 `Config` 类型带
  `ExperimentalApi` 标记；因此读取 `config.model_provider`、`config.additional` 等响应内容仍按 experimental
  contract 处理，必须以目标版本真实样本冻结，不能由请求可发送推导响应字段稳定；
- Codex 升级导致 contract test 失败时，`codex-web` fail closed，旧 `codex` 仍可回退；
- 公开 OpenAI Docs 当前不足以代替 app-server wire contract，实施以目标二进制生成 schema、
  官方源码和真实脱敏捕获三者交叉核对。

## 12. Phase 0A 真实样本包（能力编码前硬门槛；不替代 Phase 0 宿主拓扑门）

必须在隔离 `CODEX_HOME` 与脱敏 workspace 中采集，不读取或提交用户真实 prompt/token/path：

| 样本组 | 至少包含 |
|---|---|
| initialize | request/response、capabilities、initialized、版本 |
| lifecycle | daemon absent/running/start/version、WS health/ready、proxy/UDS 断开 |
| catalog | loaded/notLoaded/active/archived、custom provider、cursor 下一页 |
| history | user/agent/reasoning/command/file/MCP/dynamic tool/compaction/plan/error |
| turn | start、多个 text delta、reasoning delta、tool output delta、completed/failed/interrupted |
| interaction | command/file/permission approval、1题/多题 requestUserInput、resolved/cleanup |
| models/config | official model、自定义 provider model、reasoning effort、hidden/不可用项；采集 `config/read`，确认 typed `config.model_provider`，以及 flatten `config.additional` 是否包含 `model_providers`、实际组成与 wire 形状（敏感值脱敏）。该样本只验证事实，不授权一期递归提取 provider 目录 |
| ownership | 两 app-server 同 resume、只读仍成功、archive/delete 冲突 |
| reconnect | live 中断线、重连 read、pending server request 的真实行为 |
| external host | Terminal 按“daemon 已运行/未运行 × 默认启动/带覆盖启动”受控采样，记录 TUI 选择 LocalDaemon 或 Embedded 及 codex-web 实收事件；Desktop、VS Code 分别记录实际 endpoint、transport、进程归属与同 thread event stream，不从 Terminal 结果类推 |

每份 fixture 元数据必须记录：Codex CLI version、源码 commit（若可对应）、transport、实验 capability、
采集命令、脱敏说明和预期映射。根据本设计手写 fake server 只能测 CordCode 内部逻辑，不能作为外部
协议形状证据。

## 13. 测试与验收

### 13.0 风险优先验收顺序

验收不再等“完整 adapter 实施完成后一次性覆盖”。固定顺序为：

1. **T0 宿主拓扑门（产品代码前）**：Desktop attach、同 daemon FD/socket、无私有/managed 第二 runtime；
2. **T1 最小双向竖切门（只完成最小 text turn 接线后）**：Desktop 创建→iOS 发现并实时旁观、iOS
   创建→Desktop 发现并续聊、双方同时打开同一 thread 无 writer 冲突；
3. **T2 能力扩面门**：T1 owner 真机 PASS 后，才允许 catalog/history 完整面、interaction、models、
   mutations、SSV2 边界逐项实施；
4. **T3 完整回归与退役门**：全矩阵、LAN/Relay、重连、旧 backend 回滚。

T0/T1 任一失败立即冻结后续能力。adapter 内部测试只能证明翻译器内部一致性，不能替代 T0/T1。

### 13.1 自动化测试

- contract：真实 fixture 逐字段冻结 request/response/notification/server request；
- provenance：检查 `agent/codex-web` 不 import `agent/codex`、`transcriptindex` 或任何旧
  rollout/file-relay/session scanner 包，无旧 session/history/cache type alias 或 wrapper；首版代码审查
  必须确认不是目录 copy/rename；
- lifecycle：external daemon、CordCode-started daemon、Desktop attach、双失败、旧版本 incompatible、
  managed WS 产品路径零启动；遗留 managed record 只测严格身份校验后的安全清理；
- topology：Desktop 无私有子进程、Desktop/CordCode FD 指向同一 control socket、产品进程树无
  `app-server --listen`；
- transport：request id correlation、并发响应、unknown notification、断线 epoch、server request response；
- catalog/history：cursor、archive、官方排序、pathless hydrate、长历史有界加载；
- codec：每个 Item variant 的 started/delta/completed，exact identity 去重；
- terminal：completed/failed/interrupted/error 均产生唯一终态；
- interaction：审批/提问/resolved、断线清理、重复提交、Mac 先答；
- ownership：隔离 contract 证明第二独立 writer 会收到 `-32600`；产品拓扑回归必须证明同 daemon
  多 connection 不走该失败路径，且不得 kill/steal；
- provider：custom provider/model 不被过滤或替换；
- SSV2：删除本地 projection 后，只靠官方 API 可完整重建；
- 历史故障回归：§2.5 四项分别有定向测试，证明新路径用官方 identity/status/reducer/帧级证据消除，
  不以旧 file relay fallback 遮蔽；
- 旧 backend 回归：旧 `agent/codex` 行为与测试不变；
- iOS：新 BackendKind、capability、cache scope、切换/列表/消息 reducer 定向单测；
- protocol：Mac canonical pack 与 iOS mirror 同步。

### 13.2 性能与流式指标

同一网络、provider、模型、prompt 长度下，分别记录旧 `codex` 与 `codex-web`：

- send → `turn/started`；
- send → 首个 `agentMessage/delta`；
- 一轮 delta 数、平均/最大 delta 字符数；
- 最大相邻 delta 间隔；
- turn 完成延迟；
- app-server/proxy 进程数与空闲资源；
- 重连后可见历史一致性。

“看起来更丝滑”可作为体验结论，但退役决策必须同时有帧级数据，避免把 provider 单帧、Bridge batching
或 iOS render cadence 混为 backend 差异。

### 13.3 owner 真机验收矩阵

按 §13.0 分批执行。第 6–8 行属于 T1，必须在 Phase 1 最小竖切后先执行并 PASS；其余行不能
把第 6–8 行后置或抵消：

| # | 前提条件（网络、模式、session 状态） | 动作 | 应看到 |
|---:|---|---|---|
| 1 | LAN；Codex Web；新 session | iPhone 发长回答请求 | 首 token 后连续流式，完成态唯一收口 |
| 2 | Relay；Codex Web；新 session | 同上 | 内容与 LAN 一致，无整段延迟或重复 |
| 3 | LAN；daemon 已运行；Terminal Codex 默认配置；active session | Mac 发长回答，iPhone 打开同 session | TUI 使用 LocalDaemon；iPhone 实时进入执行中并连续显示 delta |
| 4 | LAN；daemon 未运行；先启动 Terminal Codex 默认配置 | Mac 发长回答，再打开 Codex Web | TUI 使用 Embedded；iPhone 不伪造 live stream，只按已验证的 list/read 边界显示 |
| 5 | LAN；daemon 已运行；Terminal Codex 带 `-c`/strict/non-replayable 覆盖 | Mac 发长回答，iPhone 打开同 session | TUI 使用 Embedded；iPhone 不串入该隔离 turn |
| 6 | LAN；Desktop 与 CordCode 已证明同 daemon；双方保持打开 | Desktop 发消息 | iPhone 立即进入执行态，连续显示同 turn delta 与唯一终态；不得切 session/刷新才出现 |
| 7 | 双方保持打开；Codex Web 创建并完成 | 在 Desktop 原地发现并续聊 | 原 session、原 cwd、原 effective provider/model 可继续；不得重启/复制/迁移 |
| 8 | 双方保持打开；Desktop 创建并完成 | 在 iPhone 发现并续聊 | 原 session 可继续且 Desktop 同时打开不报 active writer；不得退出任一端 |
| 9 | custom provider 已由 Codex 配置为 effective provider | 新建与续聊各一次 | 继承同一 effective provider，模型目录不被手写替换；iOS 不显示未实现的 provider 切换 |
| 10 | turn 请求 command/file approval | iPhone 允许/拒绝 | 官方 turn 继续/拒绝，状态由 resolved/completed 收口 |
| 11 | requestUserInput 多题 | iPhone 完整作答 | 一次官方 response，turn 继续，卡片不丢题/不提前完成 |
| 12 | Codex Web 正在写 thread | 尝试旧 Codex 打开同 thread | 明确 ownership 冲突，不崩溃、不抢进程 |
| 13 | live turn 中断网后恢复 | 恢复连接 | 历史校准不重复，active/terminal 状态与官方一致 |
| 14 | 切回旧 Codex | 使用独立测试 session 发消息 | 旧入口仍正常，证明回滚通道有效 |

## 14. 实施拆分

### Phase 0：Desktop 共享宿主拓扑 Gate（任何产品代码前）

0. 先检查 Desktop 宿主，而不只检查 `codex-rs` server：读取当前安装包宿主实现、启动环境、内嵌 CLI、
   官方 standalone/daemon 版本与 feature flags；默认现场出现私有 stdio 子进程不是调查终点；
1. 启动版本完全匹配的官方 daemon，并配置 Desktop 官方 attach 入口；
2. 正常重启隔离 Desktop，采集进程树、FD、control socket peer，证明没有私有 Embedded app-server；
3. CordCode 最小探针作为第二 connection 接入同一 daemon，采集 initialize、全局通知、thread subscription；
4. 在两 connection 同时存在时验证同 thread read/resume/turn、live event 与 writer ownership；
5. Terminal/VS Code 只作为逐宿主覆盖证据，不能替代 Desktop 产品 Gate；
6. 输出 PASS/EVIDENCE-ONLY/FAIL。只有 §8.2 的 PASS 可解冻 Phase 0A；其余结果不得新建产品实现目录、
   接 iOS 产品入口或启动 managed-loopback。

### Phase 0A：协议与真实样本冻结

1. 生成目标二进制 stable/experimental JSON schema；
2. 建立官方客户端 call-site / server / protocol / test 的逐能力索引；
3. 完成 §12 真实脱敏样本包；
4. 验证 writer ownership、unsubscribe/close、server request 和重连行为；
5. 每项能力必须具备“官方宿主/客户端 call site + server/schema source + 同版本真实 daemon 样本 +
   bridge 映射 + 定向测试”；缺一保持未证明。

### Phase 1：独立 package、官方生命周期与最小双向竖切

1. 从空目录新建 `agent/codex-web`；不得复制/import/包装 `agent/codex`，旧目录零行为改动；
2. 按 dsh-web 的职责分层搭骨架，但不复制任何 DSH 协议语义；
3. 产品 lifecycle 只实现 official daemon reuse/start + Desktop attach + fail closed；**不得实现 compatible
   managed WS 产品 fallback**；
4. 按官方 client queue 实现 initialize、RPC correlation、ordered events、epoch、shutdown；
5. 只接通完成 T1 所需的最小 list/read/create/resume/text turn/live delta 与 iOS backend 竖切；
6. 立即由 owner 执行 §13.3 第 6–8 行及“双方同时打开无 writer 冲突”。未 PASS 时冻结 Phase 2–6，
   不得以继续完善 adapter 为由绕过。

### Phase 2：catalog + history + SSV2（依赖 T1 owner PASS）

1. `thread/list` catalog；
2. `thread/read(includeTurns)` rich history；
3. 按 §9.1 逐项完成 pathless hydrate/kernel/descriptor/registration/forceCold 接线；
4. 按 §9.2 用官方 thread/turn status 完成死会话尾部封口，验证是否需要 activity probe 等价物；
5. archive、rename/delete；
6. 禁止旧 store、rollout、file relay、parser、`transcriptindex` import 的结构测试。

### Phase 3：turn + live stream 完整面（依赖 T1 owner PASS）

1. start/resume/turn start/steer/interrupt；
2. Thread/Turn/Item 全映射；
3. error/usage/status/reconnect；
4. 帧级性能对照。

### Phase 4：交互与模型（依赖 T1 owner PASS）

1. command/file/permission approvals；
2. requestUserInput，必要时 MCP elicitation 单独 gate；
3. model/list、custom provider 继承、permission profiles；
4. interaction/provider fixtures 与定向测试。

### Phase 5：双仓完整产品接线

1. **先提交 iOS 文件级影响审计，再改产品代码**：从当前 iOS HEAD 重新扫描 `BackendKind`、wire kind、
   backend discovery/switch、server creation、display/icon、capability gate、model/permission/agent mapping、
   session/message cache scope、stream/recovery special case、protocol mirror 与相关 tests；审计逐文件标记
   `must change / verified generic / intentionally codex-only / N/A`，不得只凭编译器报错补穷举 switch；
2. 审计必须单独裁决两点：`codex-web` 是否在每一处继承旧 `codex` 产品行为必须由 capability/协议事实决定，
   禁止机械增加 `|| .codexWeb`；backend/cache/session identity 必须独立，禁止为“共享历史”复用旧 cache key；
3. Mac wire descriptor/canonical protocol pack；
4. iOS mirror、`BackendKind.codexWeb`、经审计确认的穷举 switch 与 cache scope；
5. Mac 默认 drivers 增列但不替换 `codex`；
6. Phase 1 已只完成最小 iOS 竖切；本阶段扩展完整 capability/UI，并按影响审计选择定向 build/test、
   Release 安装与 §13.3 剩余真机矩阵。审计缺失或 T1 未 PASS 时 Phase 5 不得开工。

### Phase 6：并行观察与退役裁决

1. 两 backend 使用独立 session 做 A/B；
2. 汇总功能、流式、provider、ownership、重连与资源结果；
3. 全部退役门槛通过后，先移除旧产品入口；
4. 旧源码保留一个发布观察窗口，确认无回滚后再另案删除。

## 15. 旧 `codex` backend 退役门槛

以下条件缺一不可：

1. iOS 发起 turn 的首帧、连续性、终态不低于旧 backend；
2. **Mac Desktop 与 CordCode 的 T0/T1 均为 PASS**；PARTIAL/EVIDENCE-ONLY 不允许退役，也不允许继续扩面；
3. session list、完整历史、archive/rename/delete、长 session 分页无能力倒退；
4. iOS ↔ Mac 官方客户端双向接力通过，cwd/provider/model 不变；
5. custom provider 真实验收通过；
6. approvals/questions/interrupt/error/usage 全链路通过；
7. ownership 冲突可诊断，无自动抢占或误杀；
8. daemon 重启、Bridge 重启、LAN/Relay 重连可恢复；
9. 目标 Codex 版本的 stable/experimental contract 有明确兼容策略；
10. 旧 backend 回滚通道已验证。
11. provider/model 能力对照完成：effective provider 可读、当前 provider 的模型目录与 turn model switch
    通过真实 custom-provider 样本；若仍无 provider 目录/切换 API，必须由 owner 明确接受该能力差距，
    或等待官方 API 补齐，不能把 `model/list` 冒充 provider 列表；
12. memory 能力对照完成：一期 deliberately unsupported 的 AGENTS.md/memory 差距已由 owner 明确接受，
    或在另案评审后通过受限只读官方输入路径补齐；不得以恢复旧文件扫描写路径满足此门槛。

退役指的是先从产品入口移除，不等于立即删除源码、测试和历史迁移工具。旧 session 不做迁移：
`codex-web` 必须直接通过官方 API 打开原 thread；做不到就不满足 CordCode 初衷。

## 16. 明确非目标

- 不实现 OpenAI Responses API 代理；
- 不复制、改名、包装或 import 旧 `agent/codex` 来生成 `agent/codex-web`；
- 不让 MacBridge 保存或转发用户 OpenAI/provider API key；
- 不自建 Codex-compatible server；
- 不修改 Codex 官方源码或 vendor 私有 fork；
- 不为未知版本写递归字段猜测/fallback parser；
- 不用 JSONL file relay 作为 `codex-web` live 主路径；
- 不承诺所有第三方 provider 都能产生 token 级流；
- 不在一期接入全部 experimental surface；
- 不因新增 backend 改变 CordCode LAN/Relay/HPKE 安全边界；
- 不在实验期删除旧 `codex`。

## 17. 实施期权威证据入口

- Codex app-server 协议说明：`/Users/jacklee/Projects/codex/codex-rs/app-server/README.md`
- Codex v2 wire types：`/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/v2/`
- Codex 官方 client facade：`/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/lib.rs`
- Codex 官方 remote client：`/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/remote.rs`
- Codex WebSocket transport/auth/health：`/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/websocket.rs`
- app-server daemon：`/Users/jacklee/Projects/codex/codex-rs/app-server-daemon/src/`
- 普通 TUI/daemon 启动路径：`/Users/jacklee/Projects/codex/codex-rs/cli/src/main.rs`
- TUI daemon 探测与 runtime 选择：`/Users/jacklee/Projects/codex/codex-rs/tui/src/lib.rs`
- TUI 启动编排：`/Users/jacklee/Projects/codex/codex-rs/tui/src/startup_orchestration.rs`
- 官方 TUI session/event 路径：`/Users/jacklee/Projects/codex/codex-rs/tui/src/app/`
- 官方 app-server v2 tests：`/Users/jacklee/Projects/codex/codex-rs/app-server/tests/suite/v2/`
- CordCode 架构骨架参考：`agent/dsh-web/`
- CordCode 旧 Codex（仅接线索引/坑清单/A-B 对照）：`agent/codex/`
- 旧 backend 真实脱敏 fixtures（仅历史证据，实施版本须重抓）：`agent/codex/testdata/`
- OpenCode Web 反面教训：[2026-08-20-opencode-web-source-first-convergence-plan.md](2026-08-20-opencode-web-source-first-convergence-plan.md)
- CordCode backend 运行模型：`GO_BRIDGE_ARCHITECTURE.md`
- 流式/rollout/identity 复盘：`think.md`
- CordCode 初衷：`../cordcode-ios/docs/[综合版对比]2026-08-04-t3code-vs-cordcode.md`

本文的实施原则可以压缩为一句话：

> **从空目录建立 codex-web：dsh-web 只供架构骨架，Codex 官方源码与真实样本决定行为；
> 官方 app-server 拥有 Codex 事实，MacBridge 只做 API 调用和协议翻译。先证明共享运行时带来
> 真正的双向实时接力，再谈下线旧 backend。**

## 18. 一轮评审采纳记录

对应评审文档：
[2026-08-21-codex-web-backend-design-review.md](2026-08-21-codex-web-backend-design-review.md)。

| 评审项 | 处理 | 设计修订 / 未采纳理由 |
|---|---|---|
| B1 共享运行时事实基础 | 全部采纳 | 重写 §3.3，明确默认配置 TUI 会复用已运行 daemon，覆盖启动强制 Embedded；同步收窄 §0 目标，并把 daemon 状态 × 启动配置加入 §8.2、§12、§13.3、Phase 0 |
| M1a structured questions 分级 | 全部采纳 | 改为 🧪，保留“默认启用但仍是 experimental”的双重事实，要求真实样本与版本门控 |
| M1b approval 字段归属 | 全部采纳 | 将 `availableDecisions`/`additionalPermissions` 归回 command approval experimental 面；permission request 改为 `RequestPermissionProfile`，不再声称有该字段 |
| M1c plan 分级 | 全部采纳 | item 生命周期保持 ✅，`item/plan/delta` 单列 🧪 |
| M2 provider/model/memory | **部分采纳** | 采纳能力审计、effective provider、new-thread provider、turn model、memory deliberate-unsupported 与退役对照；**未采纳**“把 `list_providers`/provider switch 直接映射到 `thread/start{model,modelProvider}` + `turn/start{model}`”这一能力结论。官方 `protocol/v2/model.rs` 的 typed `Model` 不含 provider，`protocol/v2/turn.rs` 的 `turn/start` 只允许 model，唯一 provider 级 RPC `modelProvider/capabilities/read` 也只读当前 configured provider；协议没有 provider-list 或运行中 thread 切换 provider RPC。`config/read` 的 typed `Config` 只有当前 `model_provider`，但 server 将 effective `ConfigToml` 经 JSON 反序列化为 v2 `Config`，flatten `additional` 可能承接未建模的 `model_providers`；这不是稳定 provider 目录契约，精确内容由 Phase 0 样本冻结，一期禁止递归提取。照一轮建议实现会虚报能力；一期明确 ⛔，且禁止回退写 `config.toml`。`protocol/v2/thread.rs` 虽允许新建 thread 传 `modelProvider`，也只能在 provider id 有官方事实来源并通过 Phase 4 样本后广告 |
| M3 SSV2/kernel 接线与尾封口 | 全部采纳 | 新增 §9.1 十一处接线清单与 §9.2 官方 status 封口规则，并写入 Phase 2 |
| S1 steer 前置 | 全部采纳 | 补 `expectedTurnId` 必填及 active turnId 跟踪 |
| S2 MCP elicitation variants | 全部采纳 | 改为 Form / `openai/form` / Url，并补 initialize capability |
| S3 权威源码入口/turn 语义 | 全部采纳 | 修正 `protocol/v2/` 目录，新增 transport 与 TUI 编排源码入口，校正 `itemsView` 语义 |
| S4 provenance | 全部采纳 | 禁止清单加入 `transcriptindex` 与任何 rollout/file-relay/session scanner 包 |
| S5 daemon 用户可见副作用 | 全部采纳 | §6.3 明确 CordCode 启动 daemon 会改变后续默认 TUI runtime，并要求诊断区分 daemon 来源与 TUI attach |

评审给出的 15 条可执行修订中，除第 8 条关于 provider 能力的映射结论按上述官方协议证据部分采纳外，
其余均已落实。文档仍保持“Phase 0 Gate 先于产品实现”的边界；完成设计修订不等于共享运行时、
Desktop/VS Code 覆盖面或第三方 provider 行为已经实测通过。

## 19. 二轮评审采纳记录

对应评审文档：
[2026-08-21-codex-web-backend-design-review-r2.md](2026-08-21-codex-web-backend-design-review-r2.md)。

| 评审项 | 处理 | 设计修订 / 未采纳理由 |
|---|---|---|
| R2-M1 `config/read` 事实修正 | **部分采纳** | 采纳核心纠正：typed 面只有 `model_provider`，flatten `additional` 可能承接 `model_providers`；已同步修订 §7、§12 与 §18，一期 ⛔ 和禁止递归提取不变。**不采纳**把该兜底内容直接写成“必然是完整内置+自定义 provider 目录”的既成事实：v2 typed schema 不承诺该键及其稳定性，当前 `ConfigToml.model_providers` 注释指向用户自定义项，而 runtime `Config` 才明确称 combined map；server 转换后的精确组成必须由目标二进制 Phase 0 样本确认 |
| R2-S1 字段名与 experimental 门控 | 全部采纳 | §7 改为 `config.model_provider`，effective provider/config 改为 🧪 只读；§11.2 明确 request 可发送不等于 response 字段稳定，按真实样本冻结 |
| R2-S2 provider capabilities 正面证据 | 全部采纳 | §7 ⛔ 行补 `modelProvider/capabilities/read`，明确它只面向当前 configured provider，不能误作 provider 列表 API |

二轮评审确认的一轮修订、SSV2 接线清单及共享运行时 Gate 均原样保留。完成本轮收口后，设计满足进入
Phase 0 的文档前置条件；“满足文档前置”不代表 `config.additional` 形状、共享 event stream 或官方宿主
覆盖面已经通过真实样本验证。

## 20. 三轮评审采纳记录

对应评审文档：
[2026-08-21-codex-web-backend-design-review-r3.md](2026-08-21-codex-web-backend-design-review-r3.md)。

| 评审项 | 处理 | 设计修订 / 未采纳理由 |
|---|---|---|
| r3 设计本体结论 | 保留 | r3 对 v1.3 给出 APPROVE，未提出设计事实修正；§3.3、§7、§8.2、§9、§11.2、§12 及前两轮部分采纳裁决均保持不变 |
| §6.1 前置分析降级纠错 | 全部采纳 | 前置分析头部新增历史输入警告与禁止恢复的旧结论；设计头部将链接角色改为“仅作问题来源，不授权实施”，避免实施者把旧基线/事件名/端口/路线选项当现行设计 |
| §6.2 历史故障映射 | 全部采纳 | 新增 §2.5“故障 → 根因 → 结构性机制 → 验证落点”四行表，并在 §13.1 增加逐项历史故障回归门槛 |
| §6.3 iOS 改动量审计 | 全部采纳（Phase 5 入场执行） | Phase 5 改为“先审计、后改码”，规定扫描面、逐文件分类及两个行为裁决。没有在 v1.4 冻结当前文件名清单，因为实施前源码仍会变化，静态清单会陈旧；这不是拒绝审计，而是把文件级清单绑定到 Phase 5 当前 HEAD，审计缺失即不得开工 |
| §6.4 协议 shape 沉淀 | 全部采纳 | 新增 §7.3 集中索引已核实 shape，并明确它只是 pinned-source 推导，必须经 Phase 0 schema + 真实样本升级后才能成为 contract fixture，禁止据此生成递归猜测器 |

三轮评审没有新增需要拒绝的路线建议。仍然有效的两处部分采纳只有 §18 的一轮 M2 与 §19 的二轮
R2-M1，其不采纳理由均保留；r3 已独立复核这两项理由成立。以下“v1.4 不改变
PASS/PARTIAL/FAIL 口径”只描述当时决策，已被 v2.0 §8.2 的 PASS/EVIDENCE-ONLY/FAIL 取代。

## 21. 四轮确认评审与设计冻结记录

> **历史记录，不再授权施工。** v1.5 的冻结结论未发现“产品目标要求 Desktop、PASS 规则却只强制
> Terminal”的矛盾。2026-08-22 owner 真机反证和 Desktop attach 源码/活体证据已满足重新开设计条件，
> 因而由 v2.0 取代。以下内容只用于追溯当时决策。

对应确认报告：
[2026-08-21-codex-web-backend-design-review-r4.md](2026-08-21-codex-web-backend-design-review-r4.md)。

| 评审项 | 处理 | 冻结结论 |
|---|---|---|
| r3 四项文档集增强 | 确认通过 | 前置分析降级、§2.5 故障闭环、Phase 5 iOS 当前 HEAD 审计、§7.3 shape 索引全部保持原文 |
| 新引入问题 | 无 | r4 未发现阻断、必改、建议、事实错误或分级不一致，不新增技术修订 |
| 一轮 M2 / 二轮 R2-M1 | 维持部分采纳 | 两处不采纳理由均已被后序评审按官方源码独立复核确认；不得在 Phase 0 前恢复 provider-list 虚报或把 flatten `additional` 当稳定目录 |
| 评审周期 | 结束 | v1.5 为评审冻结基线；下一步是 §14 Phase 0，不再创建常规设计评审轮次 |

### 21.1 唯一执行入口

执行 agent 必须从 v2.0 §14 Phase 0 开始，先完成 Desktop 共享宿主拓扑 Gate，再进入 Phase 0A
协议样本。旧 Terminal-only PASS、旧 exec-plan 状态和本节历史措辞均不能越过该依赖。

### 21.2 允许重新打开设计的条件

仅以下证据可解除冻结：

1. 目标二进制真实样本与 §7.3 或其他明确断言冲突；
2. 生成 schema 与 pinned source typed shape 不一致；
3. Terminal/Desktop/VS Code 的受控实验推翻 §8.2 前提或覆盖面；
4. 官方 Codex 版本升级改变 stable/experimental method、ownership、daemon 或 event ordering；
5. 实施按 §3.4 找到官方调用链与设计映射的第一处可复现分歧。

纯措辞偏好、重复总结、没有新证据的“再评一轮”不解除冻结。出现有效反证时，不允许静默补 fallback：
必须保留样本、标注受影响 capability，按 §3.0 回写设计并重新裁决相关 Phase 0 Gate。

## 22. Phase 0 采纳与纠偏记录（2026-08-22）

对应裁决文档：[2026-08-21-codex-web-phase0-gate-verdict.md](2026-08-21-codex-web-phase0-gate-verdict.md)。
证据：`scripts/codex-web-phase0/`（schemas 692 文件、§12 九组样本、TUI 三场景、宿主实测；四套
validate 共 136 断言全 PASS，可复跑）。历史裁决曾把“Terminal daemon+默认 TUI 路径成立”登记为
共享运行时 Gate PASS，同时只记录 Desktop 独立 stdio runtime。**v2.0 判定该 PASS 口径无效**：
Terminal 只能证明 daemon transport，不能放行 Desktop 产品路线。§22-8/9 的 Desktop attach 与产品接线
证据才满足新的 T0；T1 owner 双向真机结果仍必须独立记账。

| # | 回写项 | 证据落点 |
|---|---|---|
| 1 | command approval `availableDecisions` 未声明 experimentalApi 也物理到达（`additionalPermissions` 被剥除）；decision accept/cancel/结构化，cancel→interrupted | dumps/interaction + validate_schemas s13 |
| 2 | control socket = WebSocket over UDS（JSON-RPC 每 WS text 帧一消息）；`app-server proxy` 为纯字节中继 | unix_socket.rs `accept_async` + gate-terminal 实测 |
| 3 | `config/read` 的 flatten `additional` 实测为空，不含 `model_providers` | dumps/models-config |
| 4 | 通知分级与不重放边界（全局 vs 订阅者级） | gate-terminal README 关键发现 2 |
| 5 | **Phase 2 实测补充**：从未有过 turn 的 thread 不出现在 `thread/list`（rollout 未物化，list 为 scan-and-repair + state DB 视图；`thread/start` 后立即 `turn/start` 即可见，turn 是否完成无关）。catalog 不得为此伪造条目或改用 loaded 集合冒充列表 | p2-catalog e2e（agent/codex-web/sessions_e2e_test.go 空条目断言）|
| 6 | **Phase 2 实测补充**：`thread/list` 默认 created_at（秒粒度）cursor 翻页会跳过与 cursor 同秒创建的兄弟条目（官方 `should_skip` 只认严格 `ts < cursor.ts`，tie-breaker id 在遍历层不生效）；dumps/catalog 的 `cursor_page2_count:0`（同秒 4 条）即此行为。CodCode 聚合 catalog 用服务端默认页大小（单页覆盖），不依赖小页深翻页补全 | rollout/src/list.rs `should_skip` + p2-catalog e2e limit=1 边界断言 |
| 7 | **Phase 4 daemon-WS 实测补充**：首次 ASK3 失败根因不是 transport，而是旧 mock fixture 第二题缺 options；官方 `normalize_request_user_input_tool_args` 明确拒绝任一空 options，并把错误作为 function_call_output 回给模型。修正为每题 2–3 options 后，同版本隔离 daemon WS 当前连接真实收到三题 `item/tool/requestUserInput`，adapter 写 option/text(Other)/option answers map，随后收到 `serverRequest/resolved` 且 turn completed | [requestUserInput daemon-WS Gate](2026-08-22-codex-web-userinput-daemon-gate.md)；`codex-rs/core/src/tools/handlers/request_user_input_spec.rs`；`TestE2EInteractionUserInput` |
| 8 | **Desktop attach Gate 补充**：当前 ChatGPT `26.818.31338` 宿主在 `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`、无强制 CLI 覆盖且 daemon 版本兼容时，以 WebSocket-over-UDS 连接 `~/.codex/app-server-control/app-server-control.sock`；隔离 Desktop 无私有 app-server 子进程，FD peer 与 daemon 监听 socket 相同。由此撤销“Desktop 只能独立 stdio”的旧覆盖面结论，并禁止产品回落 `managed-loopback-ws` | [Desktop attach Gate](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md)；当前 Desktop `app.asar` 本地只读检查；隔离进程树/FD/initialize 日志 |
| 9 | **Gate 2 产品接线**：MacBridge 先做 Desktop/standalone exact-version gate、启动官方 daemon、再向用户 launchd domain 写 attach 开关；codex-web 产品空 home 按官方规则解析为 `CODEX_HOME`/`~/.codex`；daemon 失败直接 `shared-daemon-required`，managed-loopback 只留旧 record 安全清理。Release 中 CordCode 两 connection 与隔离 Desktop 的 FD 均落在同一 daemon | commit `74fc3866d18c`；`RuntimeManager.configureCodexDesktopSharedRuntime`；`ResolveCodexHome`；[Desktop attach Gate](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md) |

§22-7 补齐 daemon 主路径 structured-user-input 真实证据；§22-8 由 owner 真机反证触发重新开门；
§22-9 将 Desktop 产品拓扑修正并部署为官方单 daemon 多 connection。自动拓扑 Gate 不替代 owner 真机矩阵。
实施注意的新事实清单（daemon 前置、同 daemon 多连接 resume 无冲突、`excludeTurns` 需
experimentalApi、pending server request 不重放、TUI PTY 驱动细节等）见裁决文档 §4。

## 23. v2.0 施工防偏航清单

新 agent 接手时必须先回答并附证据；任一项回答为“否/未知”时，只能调查，不能扩展产品功能：

| 问题 | 合格证据 | 不合格替代品 |
|---|---|---|
| Desktop 是否连接官方共享 daemon？ | Desktop 进程树无私有 app-server；FD/peer 指向 control socket | 共享 `CODEX_HOME`、能 `thread/list` |
| CordCode 是否连接同一个实例？ | daemon 端接受两 connection，socket inode/peer 可关联 | CLI 版本相同、URL 看起来相似 |
| Mac turn 是否实时到 iOS？ | 同 turn 的 active→item/delta→terminal 时间线 | 切 session 后重拉历史可见 |
| iOS turn 是否实时到 Desktop？ | Desktop 保持打开时看到同 thread/turn 并可继续 | 退出 Desktop 后 resume 成功 |
| 是否只有一个 writer/runtime？ | 同时打开无 `already has an active writer` | 捕获错误并给出友好文案 |
| 是否 fail closed？ | attach/版本/daemon 失败时 codex-web 不可用且保留原错 | 启动 managed-loopback、stdio 或 store fallback |

强制停线信号：

1. owner 报告“修复后现象不变”；
2. 需要退出另一个官方客户端才能继续同一 session；
3. Desktop 出现私有 app-server，或 CordCode 出现 `app-server --listen`；
4. 只能在刷新、切 session、重启后看到对方消息；
5. 自动测试只证明 adapter request/response，没有宿主进程/FD/socket 或双向时间线；
6. exec-plan 准备把下游能力标 done，但 T0/T1 仍 missing、PARTIAL 或仅 self-attested。

停线后固定动作：保留失败现场 → 记录原假设与反证 → 从 Desktop 宿主、官方 client、daemon server
三层寻找第一处分歧 → 用一个最小实验证伪新假设 → 回写本文与任务依赖 → 才能恢复施工。禁止连续堆叠
adapter 补丁，也禁止用更多局部 PASS 稀释宿主拓扑失败。
