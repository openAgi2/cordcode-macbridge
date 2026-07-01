# Changelog

本文件记录 CordCode MacBridge 的对外可见变更，按 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 惯例组织，最新在前。

技术细节与文件级证据见同目录下每轮的 `docs/YYYY-MM-DD-<主题>完成情况.md` 及对应审计报告；本 CHANGELOG 面向使用者/维护者，记录「改了什么 / 有何提升」，不重复罗列实现细节。

版本号对齐 MacBridge Release 构建的 `MARKETING_VERSION`（见 `MacBridge/project.yml`）。日期为协调世界时（UTC）。

## [Unreleased]

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
