# Changelog

本文件记录 CordCode MacBridge 的对外可见变更，按 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 惯例组织，最新在前。

技术细节与文件级证据见同目录下每轮的 `docs/YYYY-MM-DD-<主题>完成情况.md` 及对应审计报告；本 CHANGELOG 面向使用者/维护者，记录「改了什么 / 有何提升」，不重复罗列实现细节。

版本号对齐 MacBridge Release 构建的 `MARKETING_VERSION`（见 `MacBridge/project.yml`）。日期为协调世界时（UTC）。

## [Unreleased]

### 2026-07-03 — 修复 OpenCode 连续 turn 流式收口抖动

- **OpenCode 连续问答的完成事件按 turn 复位**：OpenCode SSE 订阅在同一 session 进入新 user/running 状态时清除上一轮 completion 去重，避免第二轮开始后 completion 被 session 级状态吞掉。
- **避免历史轮询后伪造完成造成状态条闪烁**：OpenCode 事件 relay 不再使用空闲超时自动补 `turn_completed`，减少生成结束后的 runtime status strip 重复亮灭和布局抖动。

### 2026-07-03 — OpenCode Automatic managed local server 实现

- **OpenCode 新装默认改为 Automatic（managed_local）**：CordCode Link 会自动启动并管理一个只绑定 `127.0.0.1` 的 `opencode serve`，选择并持久化 `4096...4196` 范围内的端口和随机 Basic Auth 凭据；iOS 仍只连接 MacBridge，不直连 OpenCode。
- **Desktop 与 iOS 自动对齐同一 OpenCode scope**：MacBridge 写入 OpenCode Desktop 默认 server、`currentSidecarUrl` 与 `projects[managedURL]`，优先合并 Desktop `local` 项目集合；本机实测确认 Desktop 运行中不热重载配置，因此实现保留 Cocoa graceful quit + reopen fallback，且冷启动会服从写入的 managedURL。
- **失败保持真实可见**：managed server 启动失败不会阻塞 Bridge，Claude/Codex 继续可用；OpenCode 则保持未配置/不可用诊断。`opencode-managed-server.err.log` 独立脱敏滚动，password 不进入 argv。
- **验证**：新增 `OpenCodeManagedServerTests`，更新 OpenCode source 迁移与 Go managedURL scope 回归测试；Swift OpenCode 定向测试与 Go OpenCode list_projects/list_sessions 定向测试通过。

### 2026-07-03 — OpenCode 无缝接入 managed local server 方案

- 产出本地 managed local server 开发规格，把 OpenCode 最终目标从手动 `external_http` 配置细化为 CordCode Link 自动托管本机 OpenCode shared server、自动同步 Desktop 默认 server 与项目 scope、iOS 扫码后直接看到 Mac 端 OpenCode Desktop 项目/session 的实现路径。

### 2026-07-02 — 修复 OpenCode Desktop 切到 external_http 后项目列表为空

- **修复重启 OpenCode Desktop 后项目/session 看起来消失**：CordCode 同步 Desktop 默认 server 到 `external_http` endpoint 时，现在会优先把 Desktop `local` scope 下的完整项目集合迁移到新 endpoint key，并用旧 active server / legacy `64667` 只补充缺项，避免 Desktop 重启后进入一个没有项目历史的新 server scope。
- **保留并合并已有 external_http 项目状态**：如果目标 endpoint 已经有项目列表，会按 worktree 去重后合并 `local`/旧 server 的缺项；`lastProject` 已存在时不覆盖，避免用户手动整理过的 Desktop 状态被回滚。

### 2026-07-02 — OpenCode 项目列表跟随 Desktop 打开的 workspace

- **修复 iOS OpenCode 模式显示大量已关闭项目目录**：OpenCode `/project` 是历史 catalog,会包含 Desktop 里已经手动关闭的项目；Desktop 侧栏真正打开的项目保存在本机 `opencode.global.dat` 的 `server.projects[scope]` 数组中。MacBridge `list_projects` 现在按 Desktop 源码语义读取该数组,只向 iOS 返回仍在数组里的 opened projects 并保留 Desktop 顺序；`expanded=false` 仅代表 Desktop 侧栏折叠状态,不再被误判为关闭。读不到 Desktop 状态时才保留原 `/project` catalog 作为诊断事实。
- **项目名跟随 Desktop 元数据**：返回项目时优先使用 `/project` metadata 里的 `name`,没有元数据时退回目录 basename,对齐 Desktop 的 `displayName(project) = project.name || basename(worktree)`。
- **OpenCode session 列表对齐 Desktop 加载方式**：目录级 session list 允许 `rootsOnly + limit`,MacBridge 转发为 `x-opencode-directory + ?limit=N`,并按 Desktop 的保守策略在 array response `len == limit` 时标记“可能还有更多”；仍拒绝 `rootsOnly + cursor`,因为 Desktop 当前并不使用 session cursor 做 sidebar 加载。

### 2026-07-02 — OpenCode list_sessions 提高 limit 上限以支持完整项目拉取

- **OpenCode `list_sessions` 的 limit 上限从 50 提高到 1000**：OpenCode server 是 array-only(无 cursor/无 total),一次性返回 `min(limit, total)`,唯一能控制取回量的就是单次 `limit`。原 50 上限会让真实项目(观测到 459 条)被截断且无法翻页。提高到 1000 后客户端可一次拉满整个项目;对小项目无副作用(服务端只返回实际总数)。

### 2026-07-02 — OpenCode project-first session 列表分页协议

- **OpenCode `list_sessions` 改为目录级 page**：go-bridge 的 OpenCode proxy path 现在接收 `directory + limit + cursor`，向 upstream 发送 `x-opencode-directory` 和非空 query 参数，并继续返回既有 `{ sessions, nextCursor, hasMore }` envelope；array-only OpenCode 1.17.13 轨道不伪造 `hasMore`。
- **避免 global dump 误当全项目 catalog**：bare `/session` 仍只代表 `global`，项目桶必须走目录 scoped 请求；当前实现已按 Desktop 方式允许 `rootsOnly + limit`,但仍拒绝 `rootsOnly + cursor`,避免 cursor page 后再 client-side 过滤造成漏页。
- **诊断更清楚**：OpenCode list 日志新增 directory、limit、cursor-present、result-count、next-cursor-present、duration，便于从 `go-bridge.log` 验证冷启动请求预算。
- **协议文档同步**：`list_sessions` 分页与 `get_session_messages` 的 `session_pagination` capability 明确拆开；列表 cursor 是 backend/project/directory scoped opaque 值，客户端不得解析或跨 scope 复用。

### 2026-07-02 — OpenCode 共享本地服务接入（Phase A 显式 external_http）实现

- **移除对固定 `127.0.0.1:64667` 的隐式默认依赖**：OpenCode backend 改为显式 **Server Source** 模型（External HTTP / Legacy 64667 / Service discovery (future) / Disabled）。`go-bridge` 的 `-opencode-url` 默认改为空，`agent/opencode` 不再 fallback `http://localhost:64667`；endpoint 未解析时 descriptor 报 `not_configured`，绝不 dial `64667`。
- **External HTTP：bring-your-own-server**：CordCode 连接用户/运维启动的 stable `opencode serve`（loopback + Basic Auth），不启动也不保活它。Swift 端 `OpenCodeEndpointResolver` 规范化 URL（`localhost`→`127.0.0.1`，拒绝非 loopback/https），`OpenCodeHealthValidator` 先以 no-auth `/global/health` 证明 server 要求认证（必须 401）再做 authed 校验；无密码 server（no-auth 200）默认拒绝为 `server_unauthenticated`。
- **RuntimeManager 显式传 URL、凭据走 env**：argv 增加 `-opencode-url <url>`（URL 非 secret），password 仍经 `OPENCODE_SERVER_PASSWORD` 环境变量传递，不进 argv / 日志；argv/env 构造提取为可测试 static。Desktop 默认 server 配置同步到 resolved endpoint URL，不再固定写 `64667`，且去重保留用户其它 server。
- **升级连续性与新装默认**：存量 `credentials.json` + 无显式 source 自动一次性迁移到 `legacy_64667` 并提示改配 external_http；全新安装默认 Disabled。`legacy_64667` 是唯一允许带 `legacy_insecure_unverified` 警告继续运行的兼容例外。
- **iOS/Desktop 共享同一 OpenCode server**：消除过去 Desktop `vlocal` 与 iOS 固定 `64667` 分裂成两个项目/session scope 的问题（不修改 OpenCode 源码；不抓取 Desktop sidecar 密码）。
- **验证**：新增 `OpenCodeEndpointResolverTests`（18 例，URL 规范化/解析/迁移）、`OpenCodeHealthValidatorTests`（10 例，no-auth/authed 区分、schema、timeout、legacy 例外）、`MacBridgeBehaviorTests`（argv/env/Desktop 配置 6 例）、Go `TestDetectAgentStatus_OpenCode*` + `TestShouldStartPassiveSubscription`。Debug 构建 + 全部定向单测通过；既有 9 个 codex pagination 测试因本机未装 `codex` CLI 的环境原因失败（已确认在 clean main 同样失败，非本次回归）。

### 2026-07-02 — OpenCode 共享本地服务接入方案文档

- 新增 `docs/2026-07-02-opencode-shared-service-discovery-plan.md`，明确“不修改 OpenCode 源码”前提下不能直接发现 Desktop `vlocal` sidecar 密码；当前 stable 可开发路线改为显式 `external_http` 共享 server，并保留未来 `opencode service` discovery 的能力门控入口。
- 方案给出开发任务、失败模式、安全约束和验证清单，目标是移除 CordCode 对固定 `64667` 的默认依赖，避免 OpenCode Desktop 与 iOS 项目/session 分裂；经评审后修订为 stable-compatible 分阶段方案：当前可开发的是显式 `external_http` 共享 server，`opencode service` discovery 因 stable 1.17.13 未暴露 `service`/`--register` 被降级为 future-gated。第二轮补充 Phase A 认证实测、bring-your-own-server 持久化边界、存量用户升级默认迁移到 `legacy_64667` 的连续性规则；第三轮补强 passwordless guard，要求 no-auth `/global/health` 返回 200 的 OpenCode server 默认拒绝，并把 `legacy_64667` 明确为带警告的兼容例外。
- 修复 OpenCode backend 状态检测仍探测旧 `/health` 的问题；现在 descriptor 与方案一致使用 `/global/health` 并校验 `healthy/version` body，避免 shared server 已可用但 iOS/MacBridge 仍显示 OpenCode 未启动。
- 修复 OpenCode 模式项目与目录选择回归：`list_projects` 兼容 OpenCode 1.17.13 的 `worktree` 字段并映射为 iOS 需要的 `directory`；`list_directory` 在 OpenCode RPC 分支中重新走通用目录浏览 handler，恢复 iOS 手动添加项目目录能力。

### 2026-07-01 — Codex 外部 session 结束后 iOS 执行态快速收口

- **修复 Mac 端 Codex 任务完成后 iOS 输入框十几秒才恢复**：当 iOS 旁观 Mac 端 Codex session 时，go-bridge 现在会监视 Codex JSONL transcript 中真实的 `task_started` / `task_complete` 事件；`task_complete` 到达后立即广播 `turn_completed` + `session_state_changed: idle`，让 iOS 走 500ms 终态 debounce，而不是等待 history probe 的多轮 unchanged 兜底。Codex transcript relay 与标准 `AgentSession` relay 使用独立 lifecycle key，可覆盖 registry 里已有 session 但标准事件流收不到外部最终事件的情况。
- **保持长工具执行不闪断**：该 relay 只使用 Codex transcript 的真实任务生命周期事件，不把工具静默或历史无变化当成结束；长时间 `sleep` / verify / build 期间仍保持 running。
- **验证**：新增 go-bridge 单测覆盖 Codex transcript task state 解析。

### 2026-07-01 — Claude Code 模式支持流式打字机输出

- **修复 Claude Code 后端回复整段出现**：MacBridge 启动 Claude Code CLI 时启用 `--include-partial-messages`，并消费 `stream_event/content_block_delta`，将 token 级文本转成统一 `text_delta` 事件下发给 iOS。
- **避免 checkpoint 重复与多段文本丢失**：Claude CLI 会在 token delta 后再发完整 assistant checkpoint；driver 现在按 content block index 去重，正常路径不重复，尾部差量会补齐，异常非前缀 checkpoint 通过 `message_updated` 发送完整文本真值。
- **工具调用与历史重放边界保持真实**：`input_json_delta` 不作为文本重复下发，工具仍由最终 `tool_use` block 产出；`--resume` 历史重放期继续抑制 live 事件，避免把历史内容当作实时流灌给 iOS。
- **验证**：新增真实形状 JSONL fixture 与 claudecode driver 单测，覆盖首个 checkpoint 不清 partial、多 text block、尾差量、非前缀 reconcile、message id 切换和 historyDraining。Sonnet/GLM-5-Turbo 路径已用真实 Claude CLI 证明 `--include-partial-messages` 会产出 `stream_event`；Opus/GLM-5.2 路径仍返回本地 gateway `529 overloaded`，作为 provider route 问题单独处理。

### 2026-06-30 — iOS 新建的 Claude Code session 出现在 Mac IDE/桌面会话列表（修复 entrypoint 过滤）

- **现象**：iOS Claude Code 模式**新建** session 发消息，iOS 正常收到回复且消息持续可见；但 Mac 端 VSCode Claude Code 扩展（及 Claude 桌面 App）的会话列表里**找不到这条 session**，重启 Mac App 仍找不到。
- **根因（已用磁盘 + claude 二进制证据坐实，非数据丢失）**：claude 给 stream-json 方式 spawn 出来的 session 打标 `entrypoint=sdk-cli`（这是 MacBridge 不设 `CLAUDE_CODE_ENTRYPOINT` 时的默认）。Anthropic 的 IDE/桌面会话列表**按 entrypoint 过滤，只显示各自创建的**：VSCode/JetBrains 扩展 = `claude-desktop-3p`，桌面 App = `claude-desktop`。于是 `sdk-cli`（MacBridge 创建）的 session 即便 JSONL 完整落盘在 `~/.claude/projects/<cwd-hash>/<uuid>.jsonl`、内容正确、可被 `claude --resume <id>` 打开，也被这些列表排除——重启无效，因为是 entrypoint 过滤而非缓存。取证：owner 的 Chat 项目下 30 条 session 干净二分为 `claude-desktop-3p`（24，VSCode 可见）+ `sdk-cli`（6，MacBridge 创建不可见），其余字段完全一致；claude 二进制含 `CLAUDE_CODE_ENTRYPOINT` 环境变量与各 tag 值。
- **修复**：[`runtimeEnvLocked`](agent/claudecode/claudecode.go) 给 claude spawn env 注入 `CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p`（用 `core.MergeEnv` 覆盖+去重，确保始终生效）。MacBridge 本身就是第三方 host（"3p"），打这个标签语义正确，且与 IDE 扩展自创 session 同标签 → 出现在 **VSCode 扩展**的会话列表。实测设该 env var 后，新 session transcript 的 `entrypoint` 字段确为 `claude-desktop-3p`。
- **范围与边界**：仅影响**新建** session；已存在的 6 条 `sdk-cli` 旧 session 仍不在列表（未原地改写 transcript）。本修复只决定 transcript 的展示标签，不影响 iOS 端可见性、消息收发或持久化。
- **已知不可修 surface**：独立 **Claude 桌面 App**（`/Applications/Claude.app`）即便 session 同标签也不显示 iOS/MacBridge 创建的 session。取证：桌面 App 自带独立 Claude Code runtime（`~/Library/Application Support/Claude-3p/claude-code/2.1.187/`），其可见 session 与 iOS session 的 `entrypoint` 都是 `claude-desktop-3p`（仅 `version` 2.1.187 vs 2.1.185 之差），说明桌面 App 不按 tag/字段过滤，而是用自有 Electron 会话索引（IndexedDB）只收录经它自己创建的 session，不扫 `~/.claude/projects/`。这是桌面 App 的产品设计，非 MacBridge 可修；iOS 创建的 session 仍可经 **iOS / VSCode 扩展 / 终端 `claude --resume <id>`** 访问。
- **验证**：定向 Go 单测 `TestRuntimeEnvTagsClaudeEntrypointForIDEVisibility` / `TestRuntimeEnvClaudeEntrypointOverridesAndDedupes` 守护；Release 重建 + 覆盖 `/Applications` 安装 + 重启完成，新 runtime 已起（`cordcode-bridge-runtime` 二进制含该 env var）。**端到端：owner 已验收 iOS 新建 Claude session 出现在 VSCode 扩展列表；Claude 桌面 App 不显示（见上，已知不可修）。**

### 2026-06-30 — Claude Code 模型列表以 `~/.claude/settings.json` 为权威源（修复网关场景下选 GLM 仍 529）

- **现象**：iOS Claude Code 模式选 GLM-4.7 发消息，claude 回报 `Repeated 529 Overloaded … inference gateway (127.0.0.1:15721)`，即使 GLM 无速率限制。
- **根因**：`AvailableModels`（[`agent/claudecode/claudecode.go`](agent/claudecode/claudecode.go)）从 provider/网关 `/v1/models`/硬编码取模型，不读 owner 的 `~/.claude/settings.json`。该文件的 `env` 块是一张别名表：`ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL`（真实 id，如 `claude-haiku-4-5`）+ `*_MODEL_NAME`（显示名，如 `glm-4.7`）。网关收到 `claude-haiku-4-5` 才路由到 GLM-4.7，"glm-4.7" 只是显示名。旧逻辑下 iOS 拿到的模型 `providerID="default"`，被 iOS 的 `providerID=="claude"` 过滤丢弃 → 不带 model 发送 → claude 无 `--model` → 网关默认路由 → 529。
- **修复**：
  1. 新增 [`agent/claudecode/settings_models.go`](agent/claudecode/settings_models.go)：读 `~/.claude/settings.json`（`CLAUDE_CONFIG_DIR` 优先），把三对别名映射成 `ModelOption{Name=claude 别名(haiku/sonnet/opus), Desc=*_MODEL_NAME}`；mtime 懒重载（不引入 fsnotify、不启后台 goroutine）；缺失/无映射返回 nil 走 fallback。`AvailableModels` 优先用它。只读 `*_MODEL`/`*_MODEL_NAME`，不把 `ANTHROPIC_API_KEY`/`AUTH_TOKEN` 等密钥反序列化进程序变量。
  2. [`modelProviderForAgent`](go-bridge/handlers.go) 对 `claudecode` 显式返回 `providerID="claude"`（语义正确，且让 iOS 的过滤通过）。
- **映射契约**：`Name`（送 `claude --model`）= 别名，claude 按 settings.json 解析成真实 id 送网关——与 owner 顶层 `"model":"opus"` 同机制。iOS 显示 `*_MODEL_NAME`（glm-4.7）、发送别名（haiku）。iOS 侧无需改动。
- **验证**：定向单测 `TestSettingsModels_*`（5 项）通过；go-bridge model/provider 测试无回归；Release 重建 + 重启完成，新 runtime `runtime_ready`。端到端（iOS 选 GLM-4.7 不再 529、收到回复）由 owner 真机验收。详见 `../cordcode-ios/docs/2026-06-30-claudecode-models-from-settings-json.md`。

### 2026-06-30 — 修复 iOS 向已存在 Claude Code 会话发消息时 go-bridge 崩溃（消息被吞）

- **现象**：iOS 打开一个 Mac 端已存在的 Claude Code session 并发送消息，iOS 收不到回复、Mac 端刷新也看不到这条消息（重启后 iOS 也丢失）。
- **根因（已用 `go-bridge.log` 坐实）**：iOS 打开会话时，`claudeSessionFileRelay`/session-state 事件会先对该 sessionID 调 `markRunning`，在 `sessionRegistry` 留下 `session==nil` 的占位 `trackedSession`（用于状态追踪）。`getSession` 对它返回 `(nil, true)`；`handleSendMessage`（[handlers.go:1817](go-bridge/handlers.go)）只判 `ok` 就调用 `sess.Send`（[:1887](go-bridge/handlers.go)），对 nil 接口派发 → **panic**，HTTP 连接被 net/http 回收，`send_message` RPC 永不返回结果，消息也没送达 agent。
- **修复**：`handleSendMessage` 的首次 session 查找改为 `if !ok || sess == nil`（与同函数二次检查 `existingOk && existing != nil` 一致），把 nil 占位当"未持有真实会话"，回落到 `StartSession(ctx, realID)` 即 `claude --resume` 正确续接。`getSession`/`markRunning` 的状态追踪契约不变（`ok` 仍表示 trackedSession 存在），避免影响 `resume_session` 的 runtimeState 等既有行为。
- **验证**：定向 Go 单测 `TestClaudeSendMessageWithNilStubDoesNotPanic` 复现并守护；go-bridge 全量测试通过（除 4 个因本机未装 `codex` CLI 的环境性失败）。Release 构建 + 覆盖 `/Applications` 安装 + 重启 MacBridge 完成，新 runtime 已 `runtime_ready`。真机端到端（发消息收到回复、Mac 能看到）由 owner 验收。

### 2026-06-30 — Codex 历史 session 下发用户图片附件

- **修复 Codex 历史消息丢失 `input_image`**：Codex JSONL 的 rich history 解析现在会把用户消息里的 `input_image.image_url` 转成协议 `files/parts`，iOS 打开历史 session 时可拿到 Mac 端 Codex 已显示的真实图片。
- **相同 prompt 文本的多图消息保持区分**：图片 file id 由图片 URL 稳定派生；多条同文案用户消息各自携带不同图片时，下发结果不会把后续图片压成前一张。

### 2026-06-30 — send_message 转发图片/文件附件到 agent（跨仓联动）

- **修复 iOS 发来的图片/文件附件被 go-bridge 丢弃**：`SendMessageParams` 原无 attachments 字段，`handleSendMessage`（主路径）与 `ocHandleSendMessage`（opencode 路径）都硬编码 `sess.Send(content, nil, nil)`，导致 agent 永远收不到图。现新增 `AttachmentInput` + `splitAttachments`（`go-bridge/attachments.go`）：按 unified-bridge-protocol 的 `AttachmentInput{kind,mime,filename,base64}` 解码 base64，按 kind/mime 拆成 `core.ImageAttachment`/`core.FileAttachment` 传给 `sess.Send`。三个 driver（claudecode/codex/opencode）都已消费图片+文件，故对所有 backend 生效；非法/空 base64 附件被丢弃不伪造。
- 与 iOS 侧联动：iOS 端 bridge client 现按协议上送 attachments（见 cordcode-ios CHANGELOG）。协议 spec（`docs/protocol/unified-bridge-protocol.md`）早已定义 attachments，本轮补齐传输层实现。

### 2026-06-29 — Codex prompt 模板发送不再依赖共享 4141

- 修复 iOS 在 Codex 模式点击「继续任务 / 总结当前状态 / 只跑相关测试 / 解释失败原因」后报 `codex app-server ws dial ws://127.0.0.1:4141 ... connection refused` 的问题：MacBridge 产品默认不再强制注入共享 Codex app-server URL，未显式配置 URL 时改走 go-bridge 已有的 stdio app-server session 路径。
- 当未配置共享 app-server URL 时，Codex descriptor 会标记为需要历史轮询，避免继续假定存在进程级 passive websocket 事件流；显式配置共享 URL 的高级用法仍保留原 websocket/broadcast 行为。

### 2026-06-28 — Claude Code effort 真值源 + iOS 覆盖持久化

- **修正 Claude Code session effort 同步此前实际不生效**：上一轮虽已把「当前 Claude runtime effort」回填进历史 session，但 MacBridge 的 Claude runtime effort 此前没有任何来源（macOS App 不配置、iOS 仅在发消息时回传），恒为空，导致 iOS 仍显示「自动」。现已改为启动时从 `~/.claude/settings.json` 的 `effortLevel`（Mac 端 Claude Code 的真实全局 effort 偏好；回退到同文件 env 的 `CLAUDE_CODE_EFFORT_LEVEL`）读取并注入 Claude runtime，因此打开任意 Claude Code session 都能显示与 Mac 端一致的智能等级（如 `Extra High`）。
- **iOS 显式改动的 effort 现在跨重启持久化**：当 iOS 发消息时显式改变 effort 且与当前值不同，MacBridge 会把该选择原子写入数据目录的 `claude-effort.json`，重启后优先于 `settings.json` 生效；未显式改动时仍以 `settings.json` 为准。
- 背景澄清：Claude Code 的 transcript 不记录 per-session effort（已抽样确认），故「某历史 session 当时的 effort」不可恢复；MacBridge 能忠实反映的是「Mac 端 Claude Code 当前的全局 effort」及其在 iOS 上的最近一次选择。

### 2026-06-28 — Claude Code session 同步模型与智能等级

- Claude Code 历史 session 列表和单 session 查询现在会把 MacBridge 当前 Claude runtime 的 reasoning effort 补入缺少 effort 元数据的旧 transcript，iOS 打开这些 session 时可显示与 Mac 端一致的模型和智能等级（如 `glm-5.2` + `Ultra`）。
- Claude Code effort 枚举补齐 `low`、`medium`、`high`、`xhigh`、`max`、`ultra`，并兼容旧的 `ultracode` 输入为 `ultra`。

### 2026-06-22 — 修复外部进程会话状态同步缺陷 (运行态同步)

- **修复被动订阅事件流下的会话运行状态更新缺失**：在 `go-bridge` 的 `startPassiveSubscription` 事件监听中，当监听到外部 Agent 独立进程产生的 `turn_started`、`turn_completed`、`session_state_changed`、`session_status_changed` 等代表运行态变化的事件时，实时将状态同步更新到 `h.sessions` 缓存中，并增强 `sessionRegistry` 中的状态标记，允许外部独立进程在缓存尚未注册时自动补齐临时状态。由此解决 iOS 客户端连接并切换到新开运行中会话时，由于 go-bridge 返回的 runtimeState 为空而导致 iOS 侧输入框保持简易模式且思考不展开的问题。

### 2026-06-19 — 恢复拆仓时遗失的维护文档

- 从原一体仓库迁回 MacBridge 构建安装、runtime/端口诊断、go-bridge backend 进程模型和 Relay 部署资料，并作为仓库根目录活文档维护。
- 按当前内嵌 runtime、持久日志、Management API、HPKE Relay、TLS pin 与独立 Go module 状态重写，删除旧 Node Bridge、外部 cc-connect replace、Copilot sidecar 和 FRP 默认路径等过时说明。
- 将共享 `CLAUDE.md` 中的 VPS 主机与用户示例改为占位符，避免项目指南携带机器专属部署值。
- 补齐新 session 冷启动规则、Release 覆盖安装条件，以及 Claude 独立进程、Codex 共享 app-server `4141`、OpenCode HTTP/SSE `64667` 的运行与排障模型；修正失效的 Relay 首装链接和已迁出钥匙串的 identity 描述。
- 对原一体仓库五份关键根文档做逐节覆盖审计，补回 runtime flags/env、完整故障树、事件管线、registry/rebind、离线通知、OpenCode hybrid 路由矩阵、架构约束与旧 VPS/FRP 自定义路径的现行边界。
- 在 `CLAUDE.md` 建立维护入口，并链接 iOS 侧端到端交互文档，减少后续排障只看单仓库导致的误判。

### 2026-06-19 — 修复撤销授权对 relay 连接不生效（安全）

**问题**：在管理 UI 撤销某台 iPhone 的设备授权后，若该 iPhone 走 relay 加密通道连接（默认推荐的远程路径），撤销不会即时生效——iOS 仍能继续访问 Bridge、拉取会话内容，只有杀 App 重启后才进入扫码页。

**根因**：`DeviceConnRegistry`（撤销时负责下发 `device_revoked` 事件并断开连接的注册表）此前只在 direct 直连路径（`server.go`）注册连接，relay 路径的 `RelayDeviceConn` 从未注册进去。撤销授权调用 `DisconnectDevice(deviceID)` 时在注册表里找不到 relay 连接，既不发事件也不断开。

**改动**：
- `DeviceConnRegistry` 的连接存储从 `[]*Conn` 改为 `[]Connection`（接口），让 direct 与 relay 两种连接类型都能注册。
- relay 连接在认证成功后注册到注册表；在 stale 清理、心跳半开清理（`pruneDeadDevice`）、`closeConn`、`Close` 四处连接移除点同步注销，避免撤销时对已关闭连接发事件。
- `DisconnectDevice` 在下发 `device_revoked` 事件后补 `conn.Close()`，确保即使客户端未及时处理事件，连接也被强制断开（此前 direct 路径仅发事件不 Close）。

**提升**：撤销授权对 direct 与 relay 两条路径行为一致、即时生效。新增 `device_conn_registry_test.go` 覆盖接口化存储、多连接撤销、注销隔离（相关测试 3/3 通过）。

### 2026-06-19 — Relay 凭据迁出钥匙串，消除重装后授权弹窗

**问题**：每次重装 MacBridge 后打开 App，macOS 弹出「CCCode Bridge 想要使用你储存在钥匙串的机密信息」并要求输入登录密码。根因是 App 走 ad-hoc 签名，钥匙串按代码签名 / Team ID 授权访问，重装后 Team ID 变化即判定为陌生应用、触发授权弹窗。这对「下载即用」的普通用户是不可接受的体验。

**改动**：Relay 的三份密钥（route credential、activation install id、activation 签名私钥）从钥匙串迁出，改用文件存储，与 OpenCode `credentials.json` 同目录（`~/Library/Application Support/CCCode Bridge/relay-secrets/`）、同样 `0600` 权限。

- **提升**：重装 / 升级后不再弹钥匙串授权窗口；无需开发者证书或稳定 Team 签名。文件存储的 0600 保护对「丢了可重新 provisioning」的 relay route credential 安全性足够。
- **一次性迁移**：存量用户首次启动新版时，若文件不存在且旧版钥匙串条目还在，自动读取旧值、写入文件、删除钥匙串条目——凭证无缝继承，不会因迁移丢值而触发重新 provisioning（后者曾导致 iOS 配对的端到端凭证与 Mac 端不一致、显示离线）。迁移为尽力而为：钥匙串读取失败（含用户拒绝授权）时不阻塞，直接新生成凭证。
- **全新安装无影响**：没有旧钥匙串条目的用户从头走文件存储，行为与旧版等价。

### 2026-06-19 — 深度运行期 Code Review 修复（commit `a85adf1f613e`）

本轮按 `docs/2026-06-19-deep-runtime-implementation-plan.md` 完成 11 项运行期修复（T01–T11），经独立审计（`docs/2026-06-19-implementation-完成情况-审计报告.md`）逐行反查源码 + 独立复跑全部测试通过。覆盖安全、进程治理、Relay 背压、跨进程契约、稳定性五类。

#### 安全
- **控制面凭据不再泄漏进 agent 子进程（T01）**：agent（Claude/Codex/OpenCode）及其工具子进程的环境从「全量继承 `os.Environ()`」改为 deny-list（`CCCODE_*`/`OPENCODE_SERVER_*`/`CLAUDECODE`）+ 运行时 allowlist 双保险。stderr 在进入日志/错误帧前统一脱敏。
  - **提升**：远程设备无法再通过 agent 工具（如 shell）读取到 go-bridge 的管理 token、relay 凭据，消除了从 data-plane 向 loopback 控制面横向移动的风险。
- **配对限流 bucket 无界增长治理（T08）**：新增惰性 TTL 清理 + 全局容量上限（4096），超限对新 key fail-closed。
  - **提升**：任意 pairingId/IP 无法制造无界内存增长。

#### 稳定性 / 进程治理
- **runtime shutdown 不再泄漏 agent 子进程（T02）**：新增幂等、deadline 约束的 `Handlers.Shutdown`，并发关闭所有活跃 session；main 的关停顺序修正为 HTTP Server → handlers.Shutdown → CloseAllConnections → relay/tls/mgmt。
  - **提升**：SIGTERM / 睡眠唤醒 / 长跑后不再残留 agent 子进程。
- **连接 Close 超时不再触发 events channel panic（T03）**：四条订阅路径（opencode/codex/sse/appserver）对齐范本——超时分支绝不直接 `close` channel，改由延迟 goroutine 等 producer 退出后再关。
  - **提升**：消除「连接 Close 超时后 producer 仍发事件 → closed-channel send panic 崩溃」。
- **Claude 改为进程组回收（T02）**：新增 `Setpgid` + 进程组 kill，对齐 codex。
  - **提升**：Claude 的 sudo/shell/插件子进程不再在 shutdown 后残留。
- **修复 Codex 重连测试自身数据竞争（T11）**：`closeCount` 改 `atomic.Int32`。
  - **提升**：`-race -count=20` 稳定通过，修掉实跑复现的 DATA RACE。

#### Relay 背压（独立 module，已部署生产 `wss://relay.byteseek.uk:8443`）
- **per-device 有界发送队列，消除跨 device 队头阻塞（T04）**：每个 device 一个有界队列（256 帧 / 8 MiB）+ 专用 writer goroutine；bridge 的投递从同步 write 改为非阻塞 enqueue，队列满则断开慢 device 并把当前帧落入 mailbox（不丢）。
  - **提升**：原先一个卡住的 device（满 TCP 窗口）会阻塞**同 route 所有 device** 的投递；现在慢 device 只断开自己，正常 device 照常收帧。

#### 跨进程契约（Swift）
- **连续 restart 收敛为单次启动（T05）**：`launchGeneration` + 可取消 `restartTask` + `applyConfigAndRestart`；100 ms 内连续多次 restart（配置变更 + Relay provisioning 回调）收敛为单次进程启动。
  - **提升**：不再端口反复接管 / ready frame 抖动。
- **runtime.json / management-token 写失败 fail-fast（T06）**：`WriteReadyFrame` 返回 error，写失败时发布 `bootstrap_persist_failed` + exit，绝不发布 ready。
  - **提升**：磁盘满 / 权限错误时不再进入「网络已开放但 UI 永远未就绪」的假死态（原先每 60 s 重启）。
- **management API 客户端短超时 + 轮询解耦（T07）**：专用 ephemeral `URLSession`（request 2 s / resource 5 s）替代 `URLSession.shared`；status 决定存活状态、agents 刷新改为独立低优先级任务。
  - **提升**：management 半开（accept 连接不响应）时 supervisor ≤ 5 s 进入恢复，而非卡死数十秒。

#### 架构债务（非用户可见，为后续铺路）
- **Handler 生命周期组件可整体关闭（T09）**：`ObservationManager` 的 lease loop 从构造函数移到显式 `Start(ctx)`；`StartCleanupLoop` 改可停 ticker。测试不再泄漏 goroutine。
- **god-object 最小治理（T10）**：新增实例级 `ConfigRepository`，旧包级全局标注 `Deprecated`。为后续拆分 `handlers.go` / `config.go` 铺路，本轮不拆大文件。

#### 前后对比

| 维度 | 之前 | 之后 |
| --- | --- | --- |
| 安全边界 | agent 可继承控制面密钥 | deny-list + allowlist 双保险，stderr 脱敏 |
| 进程生命周期 | shutdown 泄漏子进程、events panic | 统一 shutdown + 进程组回收 + 安全 close |
| Relay 可用性 | 单慢 device 拖垮整条 route | per-device 隔离背压 |
| Swift 状态机 | 双 restart、假就绪、mgmt 卡死 | generation 收敛 + fail-fast + 短超时 |
| 可测试性 | 全局状态、race、goroutine 泄漏 | 实例注入、atomic、显式 Start |

#### 验证
- `go build` / `go vet ./...` + relay-server build/test/race + Swift `xcodebuild build` 全绿
- 11 项定向测试全通过，无新增回归（pre-existing 失败：未装 codex CLI、`AvailableModels` 时序 flaky，已 baseline 确认与本轮无关）
- 完成：`docs/2026-06-19-deep-runtime-implementation-plan完成情况.md`；审计：`docs/2026-06-19-implementation-完成情况-审计报告.md`

---

> **维护说明**：后续每轮工作请在最上方（`[Unreleased]` 下）按相同结构追加一节，标题为「日期 — 主题（commit）」。发布正式版时把 `[Unreleased]` 改为对应版本号与日期，再新开一个 `[Unreleased]`。
