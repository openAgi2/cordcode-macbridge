# CCCode MacBridge 架构与代码审查报告（v4）

审查日期：2026-06-18

审查范围：`MacBridge/`、`go-bridge/`、`core/`、`config/`、`agent/`、`transcriptindex/`、`relay-server/`、构建与 CI 配置

审查维度：架构、系统健壮性、稳定性、安全性、可维护性、测试与交付

修订依据：

- `docs/code-review-2026-06-18-评审意见.md`
- `docs/code-review-2026-06-18-评审意见-r2.md`

## 1. 结论摘要

项目的总体架构方向是成立的：macOS UI、Go bridge runtime、agent adapter、公共 relay 被划分为相对清晰的边界；直连与 Relay 共用业务 RPC 分发；Relay 使用端到端加密并将服务端限制为不透明信封路由；设备凭据只持久化哈希；关键网络连接已具备读写 deadline、心跳、重连和主动吊销能力。现有 Go 测试量和协议回归测试也明显高于同规模项目的平均水平。

但当前版本仍不建议按“已完成安全加固的公开远程控制产品”标准发布。核心原因不是密码算法，而是权限边界和资源边界存在缺口：

1. `read_file` RPC 可绕过 agent 权限体系读取 Mac 用户权限下的任意小于 2 MiB 的文件。
2. 直连 WebSocket 未设置读帧上限，认证设备可通过超大 JSON 帧造成进程内存压力或 OOM。
3. Relay 限流无条件信任客户端提供的 `CF-Connecting-IP`，且限流桶永不回收，可被组合绕过限流并制造内存增长。
4. 受信设备存储先修改内存、后非原子覆盖磁盘；写盘失败或进程崩溃会产生“当前进程已生效、重启后回滚”或文件损坏。
5. Tailscale TLS 失败会自动降级为明文 `ws://`，与项目“不用 fallback 掩盖真实失败”的约束冲突，也会暴露 bearer token 与业务内容。
6. 直连配对的 6 位手动码没有尝试次数或来源速率限制，未配对客户端可以并发枚举并抢先 claim 当前配对会话。

风险统计：

| 等级 | 数量 | 含义 |
| --- | ---: | --- |
| P0 | 1 | 可直接突破预期权限边界，应立即修复 |
| P1 | 7 | 可导致远程拒绝服务、凭据/内容暴露或关键状态不一致 |
| P2 | 9 | 中期会放大故障、维护和供应链风险 |

## 2. 威胁模型与信任边界

本报告按以下攻击者画像定级，避免把公网、已配对设备和本地攻击混为一谈：

| 攻击者 | 已有能力 | 主要入口 |
| --- | --- | --- |
| A. 公网随机攻击者 | 不知道 route/device/token，不持有配对 QR | Relay HTTP/WebSocket、公开配对 claim 端点 |
| B. 同 LAN/Tailscale 的未配对客户端 | 可访问 Mac bridge 端口，但没有 device token | `/pairing`、WebSocket 握手、HTTP header 阶段 |
| C. 已配对但不再可信的设备 | 持有有效 device token；典型场景是 iPhone 被盗、转卖、备份泄漏或设备被恶意软件控制 | 完整 Bridge RPC，包括 Relay 解密后的 RPC |
| D. Relay route/bridge credential 泄漏者 | 可冒充该 route 的 bridge，但不能自动解开已有 E2EE 内容 | Relay bridge socket、设备注册、mailbox 写入 |
| E. Mac 本地低权限进程或同机用户 | 可尝试访问日志、端口和用户可读文件，但不假定已获得当前用户完整权限 | loopback management API、日志文件、Application Support |

核心信任边界：

- device token 只证明“曾被配对”，不应等价于 Mac 登录用户的全部文件权限。
- management token 是本机管理员能力；一旦泄漏，可调用 `/internal/*` 管理面。
- Relay 是不可信传输层，机密性依赖 HPKE/ECDHE 信封；route credential 仍具有路由和设备注册管理权。
- 配对 QR/手动码属于短期 bearer capability；看到或猜中它的人可以参与 claim，最终仍依赖 Mac 用户审批。

定级约定：

- P0/P1 以攻击者 B、C 或公网 A 可达到的影响为主。
- 仅在攻击者 E 已具备本地执行能力后才成立的纵深防御问题，通常定为 P2。
- 需要 route credential、QR 泄漏或其他秘密先失陷的问题，会显式写明利用前提。

攻击者 C 与 D 虽然都持有有效凭据，但获取成本不同：C 通常来自手机物理失陷、备份泄漏或设备恶意软件，发生门槛中等；D 需要 Mac/Keychain、激活流程或部署配置额外失陷，门槛更高。因此同等影响下，C 类问题的发布定级权重高于 D 类问题。

## 3. 架构评估

### 3.1 做得较好的部分

- 组件边界基本合理：SwiftUI 负责产品生命周期和设置，Go runtime 负责协议与 agent，`relay-server` 保持独立模块和独立部署链。
- agent 能力采用 opt-in interface，而非不断膨胀单一接口，适合后端能力不对称的现实情况。
- Relay 数据面与 Bridge RPC handler 分离，公共 Relay 不接触明文业务数据。
- Relay 凭据在数据库中存储摘要，比较使用 `hmac.Equal`；长期 Relay credential 在 macOS Keychain 中保存。
- 设备吊销会主动断开当前连接，降低“仅数据库标记但旧连接继续工作”的窗口。
- gorilla/websocket 的并发写约束被显式封装，关键路径设置 write deadline，并有针对死锁、半开连接和大帧的回归测试。
- SQLite 使用 WAL、foreign key、单连接与事务维护 mailbox cursor 和容量，离线邮箱的一致性设计较完整。
- CI 已覆盖 secret scan、两个 Go module、macOS 构建和 unsigned release 产物。

### 3.2 主要架构债务

- `go-bridge/handlers.go` 超过 3,200 行，同时承担路由、会话生命周期、文件读取、历史加载、provider、诊断和 relay delivery 等职责。
- `config/config.go` 超过 3,200 行，配置模型、迁移、持久化和业务操作高度集中。
- `RuntimeManager.swift` 超过 1,100 行，进程生命周期、端口接管、日志、OpenCode 配置、Relay provisioning、Keychain 和休眠恢复耦合在一个 `@MainActor` 类型中。
- 多个包级全局变量（如 device store、pairing store、连接 registry）形成隐式依赖，限制多实例测试，并增加初始化顺序和并发推理成本。
- “已认证设备拥有什么权限”没有形成可执行的 capability policy。当前认证后基本获得完整 bridge RPC 面，敏感 RPC 只能依赖各 handler 自己正确约束。

建议的目标分层：

```text
Transport/Auth
    -> Capability Policy
        -> RPC Application Services
            -> Agent Adapters / Filesystem Services / Relay Services
                -> Durable Stores
```

认证只回答“是谁”，policy 层必须继续回答“该设备是否可执行这个方法、可访问哪个 workspace、是否需要本机确认”。

## 4. 详细问题

## P0

### P0-1 `read_file` 可绕过 agent 权限读取任意本地文件

证据：

- `go-bridge/handlers.go:3138-3178`
- `read_file` 被直连和 Relay RPC 正常暴露：`go-bridge/handlers.go:579-580`、`go-bridge/handlers.go:627-628`

代码注释声称“拒绝敏感路径”，实际仅执行 `filepath.Clean`，没有：

- workspace 根目录约束；
- 允许目录或已签发文件引用约束；
- symlink 解析后的边界校验；
- 敏感目录拒绝；
- 本机交互确认；
- 细粒度 capability 校验。

任何持有 device token 的客户端都可以直接请求例如 `~/.ssh/*`、`~/.aws/*`、项目 `.env`、agent auth 文件等，只要目标是普通文件且不超过 2 MiB。该路径还绕过 Codex/Claude 自身的工具审批与 sandbox 机制。

攻击链放大：

```text
失陷的已配对设备
  -> read_file("~/Library/Application Support/CCCode Bridge/management-token")
  -> 获得本机 management bearer token
  -> 调用 127.0.0.1:<random>/internal/*
  -> 创建/审批配对、吊销设备、读取管理状态和 Relay 诊断
```

management API 只监听 loopback，远程设备通常不能直接访问该地址；但 token 泄漏仍突破了“仅 Mac App 持有本机管理能力”的设计边界，并可与本地代理、浏览器或后续本机落点组合利用。相同规则还必须保护 `relay_identity.key`、`devices.json`、agent auth 文件与云凭据。

整改：

1. 长期方案：`read_file` 只接受服务端生成的 opaque file reference，不再接受任意路径。
2. 短期止损：允许根仅为当前已授权 workspace 与 bridge attachment 目录。
3. 对允许根和目标执行 `Abs` + `EvalSymlinks`，再用 `filepath.Rel` 校验目标没有越界。
4. 在短期兼容阶段显式拒绝 `management-token`、`relay_identity.key`、`devices.json`、`~/.ssh`、`~/.aws`、`~/.config/gcloud`、agent auth 文件及 `.env`；黑名单不能替代根目录白名单。
5. 将文件读取权限纳入设备 capability；读取 workspace 外文件必须通过 Mac 本机确认。
6. 增加针对绝对路径、`../`、symlink、home secret、management-token 和 stat/read 竞态的安全回归测试。
7. workspace 锚点：`handleReadFile(conn, msg)` 当前签名不含 workspace，授权根需在 handler 内反查——按 `msg.BackendID` 取 agent（`getAgent`，`handlers.go:281`），优先用 `extractDir(msg)`（`handlers.go:376`）或 `h.sessions.directoryForSession(msg.SessionID)`（`types.go:302`）得到 session 绑定的工作目录，再按 `WorkDirSwitcher.GetWorkDir()`（`core/interfaces.go:367`）兜底；拿不到任何授权根时直接返回 `file.outside_authorized_root`，不进入后续 stat/read。

## P1

### P1-1 直连 WebSocket 没有读帧上限，可被大帧耗尽内存

证据：

- `go-bridge/server.go` 在升级连接后未调用 `SetReadLimit`。
- 主 bridge server：`go-bridge/main.go:289-294`
- Relay server 已正确设置 32 MiB 上限：`relay-server/internal/relay/server.go:469`、`:490`

`ReadJSON` 会在解析前接收完整消息。认证设备、泄漏 token 的攻击者，或开发模式下的任意网络客户端可以发送极大文本帧，造成内存突增、GC 抖动甚至 OOM。Relay 侧有限制并不能保护 LAN/Tailscale 直连入口。

整改：

- 在升级后、首次读取前设置 `SetReadLimit(1 << 20)`；当前客户端入站 RPC 不需要 32 MiB。
- 将握手/RPC 请求上限与大响应上限分离；客户端请求通常不需要 32 MiB。
- 对 content、prekey batch、数组长度等再做结构化上限。
- 增加超限 close code 和内存边界测试。

### P1-2 Relay 限流可被伪造来源头绕过，并造成限流桶无界增长

证据：

- `relay-server/internal/relay/server.go:696-697`
- `relay-server/internal/relay/server.go:757-765`
- `relay-server/internal/relay/limiter.go`

`clientIP` 无条件信任 `CF-Connecting-IP`。任何直达 nginx/relay 的客户端都可以自行设置该 header，从而每次请求使用不同“IP”绕过 rate limit。与此同时 `RateLimiter.buckets` 没有 TTL 清理或容量上限，攻击者可持续制造唯一 key，导致常驻内存增长。

应拆成两个独立整改工单：

1. 受信代理边界错误导致的限流绕过。
2. bucket 无清理、无容量上限导致的内存放大。

整改：

- 只在 `RemoteAddr` 属于明确配置的可信代理网段时读取代理头。
- 当前部署若由 nginx 终止 TLS，应由 nginx 覆盖并传递单一受信 header，应用侧验证代理来源。
- 给 bucket 增加定期清理、最大条目数和观测指标。
- 激活、配对提交等高价值端点增加 route/install/claim 维度限流，而不仅是 IP。

### P1-3 设备存储不是事务性持久化，故障后认证状态可回滚或损坏

证据：

- `go-bridge/trusted_device_store.go:174-224`
- `go-bridge/trusted_device_store.go:258-266`

`AddDevice`、`ReplaceDevice`、`EnableRelay`、`RevokeDevice` 都先修改内存，再调用 `save()`；`save()` 使用 `os.WriteFile` 直接覆盖正式文件。

失败模式：

- 写盘失败：当前进程内已接受/吊销，重启后恢复旧状态。
- 覆盖过程中崩溃或磁盘满：`devices.json` 可能被截断，下一次启动直接失败。
- `ReplaceDevice` 保存失败时，内存中的旧 token 已被删除，新 token 却未持久化，当前进程和磁盘状态分叉。

整改：

- 在副本上完成变更，原子写盘成功后再替换内存快照。
- 变更全程持有 store 写锁：深拷贝当前 `MemoryDeviceStore`，只修改副本，将副本序列化并原子持久化；写盘失败直接丢弃副本，成功后一次性替换 `FileDeviceStore.mem` 指针。不得先修改正式内存对象，也不得在写盘期间释放锁，否则会重新引入 lost update/TOCTOU。
- 深拷贝陷阱：`MemoryDeviceStore` 字段私有（`byID map[string]TrustedDeviceRecord`、`byToken map[string]string`，`trusted_device_store.go:67-68`），当前没有 `Clone()`，需新增。`TrustedDeviceRecord.RevokedAt` 是 `*time.Time` 指针（`trusted_device_store.go:25`），直接值拷贝会让新旧 store 共享同一指针，后续对原 store 的 `RevokeDevice` 写入会污染副本；深拷贝时必须对该字段单独复制（复制非 nil 时指向的 time 值，再取地址）。
- `core.AtomicWriteFile` 可复用其临时文件+fsync+rename 思路，但它当前不执行目录 fsync（`core/atomicwrite.go` 无 `dir.Sync()`），需在其后补 `dir.Sync()` 再清理临时目录，否则崩溃后目录项不一定落盘。
- 使用同目录临时文件、`fsync`、权限设置、rename，并在需要时同步目录。
- 为磁盘满、权限错误、进程中断和损坏恢复增加测试。
- 同样审视 `identity.json`、`config.json`、`runtime.json` 和 display-name 文件。

### P1-4 TLS 失败后自动降级为明文 WebSocket

证据：

- `go-bridge/main.go:167-179`
- `CLAUDE.md:160` 明确禁止在生产运行路径用 fallback 掩盖真实失败。

Tailscale 自签名证书生成失败或 `tls-port=0` 时，代码将远程候选切换为 `ws://`。这会使 bearer token、RPC、源代码片段和 agent 输出在链路上明文传输，同时把真实安全故障表现为“仍可用”。

整改：

- 产品模式下证书生成失败应禁用该远程候选并暴露明确错误，不得降级。
- 仅显式开发参数可启用明文远程访问，并在 UI 中持续标红。
- 配对 payload 不应自动发布不安全 URL。

### P1-5 Info 日志记录用户消息内容，缺少内容级 redaction

证据：

- `go-bridge/handlers.go:1582` 在 Info 级别记录最多 120 字符 `contentPreview`。

用户提示中经常包含源码、token、错误日志、内部 URL。无论日志最终写入哪里，Info 级日志都不应记录用户消息正文；该内容还可能进入崩溃包、远程诊断、用户主动分享的日志或备份。

整改：

- 默认禁止记录用户内容，仅记录长度、request ID、backend 和耗时。
- 将结构化错误与隐私字段分类，统一 redaction。
- 添加测试，扫描默认日志输出，断言 prompt、device token、management token、relay credential 和 OpenCode credential 不出现（与 §8.1 凭据责任矩阵一致）。

### P1-6 管理面启动失败没有使 product runtime 启动失败

证据：

- `go-bridge/main.go:228-234`
- 随后仍写 `runtime_ready`：`go-bridge/main.go:390-406`

管理 API 监听失败只记录日志，runtime 仍继续启动并发布 ready frame。失败分支不会给 `managementURL` 赋值，因此 `runtime.json.managementUrl` 为空；Mac App 在 `pollManagementAPI` 中因 guard 失败持续返回，表现为静默停留在 starting，之后才可能由卡住重启逻辑处理。这不是“读到错误 URL”，而是启动契约允许空管理地址。

整改：

- product mode 下 management API 是必需依赖；启动失败应写结构化 `runtime_error` 并退出。
- Mac 侧把“子进程仍运行但 ready frame 的 `managementUrl` 为空”判为明确致命启动错误，展示稳定错误码，不能只等待 60 秒重启。
- Relay identity 初始化失败也应根据配置意图决定 fail closed，而不是静默关闭 Relay。
- ready frame 只应在所有声明为必需的监听面和持久化文件成功后写出。

### P1-7 直连配对的 6 位手动码无尝试限流，可被枚举并抢先 claim

证据：

- `go-bridge/pairing_session.go:302-311` 生成 6 位纯数字手动码，熵约 20 bit。
- `go-bridge/pairing_handler.go:171-177` 允许只提交 `manualCode` 查找会话。
- `/pairing` 没有 IP/连接/会话级速率限制，也没有总尝试次数。
- `PairingSession.Claim` 采用 first-writer-wins；先猜中的客户端会把状态从 `created` 改为 `claimed`。

攻击者 B 在配对窗口内可以并发枚举手动码。成功后未必能绕过 Mac 用户最终审批，但可以：

- 抢先占用真实配对会话，阻止合法设备 claim；
- 在 UI 中注入伪造设备名，诱导用户误批；
- 通过大量连接消耗 goroutine 和 WebSocket 资源。

QR 中的 `pairingID` 为 16 位 base36 随机串，强度明显更高；问题集中在“手动码单独作为 lookup secret”且无在线猜测保护。

整改：

- 最小止损：取消 `manualCode` 单独查找路径，claim 必须同时提交高熵 `pairingId` 和 `manualCode`；手动码退化为二次确认因子，不能再作为 20 bit lookup secret。
- 对 `/pairing` 增加来源 IP、pairing session 和全局并发三层限流。
- 单个 session 连续失败达到阈值后失效并要求 Mac 重新生成。
- 手动码校验应同时绑定 bridge/pairing ID；若产品允许，提升码长或改为带校验的更高熵短码。
- Mac 审批 UI 必须明确展示设备 ID、平台和 claim 来源，不只展示可伪造名称。
- WebSocket 在 claim 前设置读帧上限、read deadline 和最大消息次数。

## P2

### P2-1 主 HTTP server 和管理 server 缺少握手阶段超时

证据：

- `go-bridge/main.go:293`
- `go-bridge/management_api.go:122`
- 本地 relay test server：`go-bridge/main.go:322`

连接升级后的 WebSocket 有 read deadline，但 HTTP header 读取阶段没有 `ReadHeaderTimeout` 和 `MaxHeaderBytes`。主端口监听所有网卡，存在低成本 slowloris 风险。

建议至少设置 `ReadHeaderTimeout`、`IdleTimeout`、`MaxHeaderBytes`；不要对 WebSocket 数据面设置会误伤长连接的普通 `WriteTimeout`。

### P2-2 管理 token 生成与持久化错误被忽略

证据：

- `RuntimeManager.swift:600-610`
- `RuntimeManager.swift:790-793` 的 `generateToken` 忽略 `SecRandomCopyBytes` 返回值。

随机源失败会返回全零或部分零 token；写文件/改权限失败仍继续启动，最终表现为 Mac App 无法管理子进程。安全材料生成和 0600 持久化都应是 fail-fast 操作。

### P2-3 `SaveFilesToDisk` 是潜在任意路径写原语

证据：

- `core/message.go:84-105`

`FileAttachment.FileName` 未做 basename 化和边界校验，`filepath.Join(attachDir, fname)` 可被 `../` 或绝对路径逃逸。当前 `go-bridge` 的 `send_message` wire model 尚未接入文件数组，因此未按当前远程可利用 P0 处理；但该函数已被多个 agent session 复用，一旦附件协议接入会立即成为任意文件覆盖漏洞。

整改应在共享函数内完成，而不是要求每个调用方预清洗。

### P2-4 Relay 激活 nonce 没有服务端去重

证据：

- `relay-server/internal/relay/server.go:154-203`
- 数据库 schema 未存储 activation nonce。

请求校验 timestamp 和签名，但没有实现 nonce 的一次性语义，因此同一合法激活请求在五分钟窗口内可重放。单独重放通常只是把 credential 设置为合法请求原本携带的值；需要攻击者已观察到该请求或同时掌握该 credential，才会上升为实质凭据回滚风险。保留 P2，主要原因是协议语义不完整和纵深防御不足，而非当前存在低门槛接管。

### P2-5 文件持久化权限和原子性策略不统一

`core.AtomicWriteFile` 已存在，但关键安全状态仍大量直接 `os.WriteFile`。此外 `identity.json`、`config.json`、display name 为 0644，是否包含未来敏感字段没有 schema 级保证。

建议建立统一 `securestore`：

- 原子写；
- 文件/目录权限；
- owner 校验；
- schema version；
- 损坏备份和恢复；
- 明确哪些字段允许明文。

### P2-6 CI 供应链不可完全复现

证据：

- `.github/workflows/ci.yml`

问题：

- `go install github.com/zricethezav/gitleaks/v8@latest` 每次解析不同版本。
- GitHub Actions 使用浮动 major tag，而非 commit SHA。
- 没有 dependency vulnerability scan 或 SBOM。

建议固定 gitleaks 版本、按 SHA 固定第三方 Action、增加 `govulncheck` 和 release SBOM。安全工具自身不应使用 `latest`。

### P2-7 大型文件与全局状态增加回归概率

热点：

- `config/config.go`：约 3,230 行
- `go-bridge/handlers.go`：约 3,211 行
- `agent/codex/appserver_session.go`：约 1,788 行
- `RuntimeManager.swift`：约 1,112 行

建议按行为边界拆分，不做纯粹“为了行数”的重构：

- handlers：session、filesystem、history、provider、diagnostics、delivery；
- RuntimeManager：process supervisor、bootstrap store、port ownership、logging、relay provisioning；
- config：schema、load/migrate、project mutation、provider mutation。

每次拆分必须保持协议测试与行为测试不变。

该项不阻塞当前安全发布整改，应列入发布后的 vNext 架构周期，避免与 P0/P1 修复混在同一批大规模重构。

### P2-8 日志路径、权限与滚动治理不足，且运维文档固化了 `/tmp` 行为

证据：

- `RuntimeManager.swift:50` 默认 `/tmp/go-bridge.log`。
- `RuntimeManager.swift:752-774` 使用固定路径创建/打开日志，没有显式 0600、symlink 防护或滚动策略。
- `CLAUDE.md:165` 将 `/tmp/go-bridge.log` 被 120 分钟重启截断作为正式排障约定。

该项从原 P1-5 拆出并降为 P2。降级依据是远程攻击者不能仅凭固定路径直接读取本机日志，利用通常需要攻击者 E 的本地落点。这里不依赖“macOS TCC 必然保护任意 `/private/tmp` 文件”作为安全保证：固定路径、权限未显式设置、symlink 风险和无容量治理本身仍需修复。

整改：

- 日志迁入 Application Support/Logs，目录 0700、文件 0600。
- 使用拒绝 symlink 的安全打开方式，并实现大小/代数滚动。
- 修改 `CLAUDE.md:165` 的排障说明，删除“定时重启等价于日志轮转”的隐含假设。
- 内容 redaction 仍按 P1-5 作为发布阻断项处理。

### P2-9 Relay 首次配对 claim 采用 first-writer-wins，QR 泄漏时可造成配对 DoS

该项与 P1-7 同属 claim 抢占，但利用前提不同：P1-7 的攻击者 B 不持有任何配对 secret，可直接在线枚举低熵手动码；本项要求完整 QR capability 先泄漏，门槛更高，因此定为 P2。

证据：

- `relay-server/internal/relay/store.go:418-432` 以 `(route_id, claim_id)` 为主键插入 pending claim。
- `relay-server/internal/relay/server.go:357-381` 提交 claim 不要求 bridge/device auth，而依赖 QR 中的 route、claim 和 capability。
- 重复 claim 插入返回错误，现有记录保持不变。

这是 bearer QR 设计的自然结果：获得完整 QR capability 的人本来就被允许提交 claim；因此不把它定为“无凭据公网接管”。但 QR 被截图、录屏、肩窥或错误分享后，攻击者可先提交自己的 HPKE claim，占用该 claim ID，导致合法手机无法提交。Mac 仍需审批，机密性未直接破坏，主要影响是可用性和社会工程风险。

整改：

- UI 明确提示 QR 等价于短期配对凭据，避免截图分享。
- 对同 claim 的冲突返回稳定 `relay.pairing_claim_conflict`，并在 Mac UI 显示“claim 冲突，重新生成二维码”。
- 可考虑在 Relay 端保存 claim capability 绑定的提交摘要，允许相同请求幂等重试，同时拒绝不同请求替换。

## 5. Relay 公网攻击面专项复核

### 5.1 Prekey 批量

评审意见提出“prekey 批量端点是否可撑爆 Relay 表”。复核后结论是：**不采纳“Relay 数据库 prekey 表可被公网撑爆”这一具体漏洞表述**。

原因：

- `relay-server` 没有 prekey 上传端点和 prekey 数据表。
- prekey 是 E2EE 解密后的 inner RPC，由 `go-bridge/handlers.go:484-506` 处理。
- 请求必须来自已经认证并建立安全 channel 的设备。
- `go-bridge/relay_prekey.go:27-29` 和 `:174-182` 对单设备可用 prekey 设置 64 个硬上限。

仍有一项中低风险观察：`processedBatches` 保存历史 batch ID 且不清理。恶意已认证设备需要等待 prekey 被消费、持续上传新 batch 才能长期放大该 map，门槛和增长速度有限。建议将 processed batch 幂等记录改为有界 LRU/TTL，但不单列发布阻断漏洞。

### 5.2 Mailbox 灌写与驱逐

评审意见提出“攻击者冒充受害 device 灌垃圾并驱逐真实消息”。复核后结论是：**不采纳“普通公网客户端或已认证 device 可直接向受害 mailbox 写垃圾”的漏洞表述**。

原因：

- mailbox append 只发生在 bridge socket 的读循环。
- Relay 校验 envelope 的 `routeId`、`senderId == "bridge"`、目标 device 存活状态。
- device socket 只允许 `senderId == deviceID` 且 `destinationId == "bridge"`，不能写 device mailbox。

成立的剩余风险：

- 攻击者 D（route/bridge credential 已泄漏）可冒充 bridge 向该 route 的设备写入 opaque frame，触发容量驱逐。
- buggy bridge 也可能自我驱逐真实消息。
- 当前策略是“旧帧静默淘汰 + warning 日志”，设备侧没有可靠获知丢帧区间。

因此建议把 mailbox eviction 作为可靠性和 credential-compromise containment 工单：增加 eviction counter、每 device 指标、chain gap 通知及可选的“容量满则拒绝新帧”策略评估。

### 5.3 Route activation/claim 竞争

- activation 由 `install_id` 主键、signing public key 唯一约束和事务保护；同一 install 的不同 key 会失败，未发现可替换既有 identity 的竞态。
- 并发首次 activation 可能有一个请求因唯一约束冲突失败，属于可重试的可用性行为；当前错误统一映射为 `relay.activation_conflict`，可观测性尚可。
- pairing claim 的 first-writer-wins 风险已单列为 P2-9。

## 6. 配对流程专项复核

### 6.1 已有安全保障

- pairing session ID 使用 16 位 base36 CSPRNG 字符串，远强于 6 位手动码。
- device token 使用 32 字节 `crypto/rand`，持久化仅保存 SHA-256 hash。
- session 状态迁移有互斥锁，重复 claim 返回 `pairing.already_claimed`。
- Mac 管理面 approve/reject 需要 management bearer token。
- Relay-first claim 使用 HPKE，claim 内外 device ID/public key 一致性会被校验。
- 直接配对在注册 pending connection 后才对 Mac 暴露 claimed 状态，已处理 approval push 的 TOCTOU。

### 6.2 已确认问题

- 6 位手动码可单独查找且没有限流：P1-7。
- `/pairing` claim 前没有读帧上限和 read deadline，可被慢连接和大消息消耗资源；并入 P1-7 整改。
- 配对状态只在内存中保存；runtime 重启会使进行中的配对失效。该行为本身是 fail closed，不是安全漏洞，但 UI 应明确提示重新生成配对码。
- Relay claim first-writer-wins：P2-9。

### 6.3 需进一步产品确认

- Mac 审批 UI 当前展示哪些不可伪造属性，需要结合独立 iOS 仓库和真实交互确认；本仓库只能确认服务端保存了 device ID、display name 和 platform。
- 如果手动码设计目标是在公网 Relay 使用，应提高熵并采用更严格限流；如果仅限同 LAN，仍不能省略在线猜测保护。

## 7. 稳定性与健壮性评估

### 已有保障

- Bridge 和 Relay 均有连接读写 deadline、ping/pong 与连接替换逻辑。
- RuntimeManager 能区分旧 PID 回调、休眠、用户停止和 crash retry。
- Relay mailbox 的 cursor 分配、容量淘汰、ack 和 revoke 清理位于事务中。
- session registry、连接 registry、broadcast 和关键 crypto state 有锁保护。
- 大量 regression test 覆盖历史上出现过的连接、分页、relay 和协议问题。

### 仍需补齐

- 入口资源治理：请求帧大小、字符串长度、数组数量、并发 session 数、每设备速率。
- 持久化一致性：所有“认证/身份/配置”状态必须原子提交。
- 启动契约：ready 必须代表所有必需依赖就绪。
- 可观测性：应有稳定错误码、重启原因计数、连接数、帧大小、限流命中、mailbox eviction、持久化失败指标。
- 日志治理：隐私分类、滚动、权限、结构化 request correlation。
- 故障注入：磁盘满、权限丢失、SQLite busy、代理断连、超大帧、半开连接、时钟偏移。

## 8. 安全模型建议

建议明确三层信任：

1. **未配对客户端**：只能提交有界配对 claim，严格限流。
2. **已配对普通设备**：可管理自己的会话，但只能访问显式授权 workspace。
3. **本机管理员能力**：设备管理、workspace 外文件读取、credential/config 修改、危险 provider 操作。

device token 不应天然等价于 Mac 登录用户全部文件权限。建议为设备记录增加 capability/version，并把授权判断集中在 RPC 分发前，而不是散落在 handler 中。

推荐默认策略：

- 只允许 `wss://` 或 E2EE Relay。
- workspace 采用 Mac 本机显式授权列表。
- 文件读取只使用 opaque reference。
- 新设备默认最小权限。
- 敏感能力升级需要 Mac 本机确认并可撤销。
- token 轮换、设备吊销和 relay generation 变化均主动断开旧连接。

### 8.1 凭据轮换与吊销责任

| 凭据 | 当前生命周期 | 用户责任 | 应由系统自动化承担 |
| --- | --- | --- | --- |
| Device token | 配对时生成；重新配对会通过 `ReplaceDevice` 替换；可在 Mac 管理面吊销 | 手机丢失、转卖或怀疑泄漏时立即吊销设备 | 替换/吊销后主动断开旧直连和 Relay channel；提供 last-used/last-remote 异常提示；不做无依据的周期轮换 |
| Management token | Mac App 首次启动生成并复用文件中的 token | 用户不应复制或分享；怀疑泄漏时需触发重置 | 修复后应提供原子“重置并重启 runtime”操作；旧 token 立即失效；禁止被 Bridge 文件 RPC 读取 |
| Relay route credential | Mac provisioning 生成并保存在 Keychain；签名 activation 可更新同 route credential | 一般不要求用户手工管理；怀疑 Mac/Keychain 泄漏时执行“重置 Relay 凭据” | 提供显式轮换动作：先生成新 credential，经签名 activation 提交并持久化成功，再重启 bridge；不得后台定时轮换导致设备无故离线 |
| Relay device credential | 首次 Relay 配对/升级时服务端生成；重新注册同 device 会替换 | 通过 Mac 吊销对应设备 | 重新注册时旧 credential 立即失效并断开旧 socket；吊销需清空 mailbox |
| OpenCode credential | 首次启动自动生成，用户可在设置中修改并重启 | 用户决定何时修改；若外部 OpenCode 服务泄漏则手工轮换 | 保存必须原子化；更新后重启使用新值；不在日志或诊断中回显 |
| Pairing code/capability | 5 分钟短期 bearer capability | 不截图、不分享；出现冲突时重新生成 | 过期自动失效；失败次数/速率限制；新建配对时可使旧 session 失效 |

责任原则：

- 自动化负责“安全状态一致地生效”：生成、原子持久化、旧凭据失效、连接断开和错误呈现。
- 用户负责“基于现实事件做撤销决定”：设备丢失、账号转移、怀疑泄漏。
- 周期轮换不是默认目标。缺少双凭据过渡和恢复协议时，盲目自动轮换会制造可用性事故；应先提供显式、可回滚、可观测的轮换操作。

## 9. 测试与交付评价

本次实际执行：

```text
go build ./go-bridge                                  PASS
go test ./... -count=1                               PASS
go test ./go-bridge/... -count=1                     PASS
go vet ./...                                          PASS
(cd relay-server && go test ./... -count=1)          PASS
(cd relay-server && go vet ./...)                    PASS
xcodebuild ... -configuration Debug ... build        PASS
```

按项目约束，本次没有运行 UI tests、snapshot tests、Simulator automation 或真机流程。

测试缺口优先级：

1. `read_file` workspace/symlink/secret 边界。
2. 直连 WebSocket 超大帧与连接级内存上限。
3. `FileDeviceStore` 磁盘失败和 crash consistency。
4. Relay 可信代理头、bucket 回收、伪造 IP。
5. TLS 生成失败必须 fail closed。
6. runtime ready 契约与 management bind failure。
7. 日志不得包含消息内容或凭据。
8. 手动配对码猜测、claim 并发与限流。

### 9.1 最小验证用例草稿

以下是整改验收用例，不代表本次已实现。

草稿中的 `callReadFile`、`dialAuthenticatedBridge`、`newStoreWithFailingAtomicWriter` 和 `claimManualCode` 是整改 PR 需要一并补齐的测试 helper。优先复用现有 fixture：`trusted_device_store_test.go` 提供 `newTestStore()` / `makeTestRecord(deviceID)`（设备 ID 用 `dev1`/`dev2`，草稿据此对齐，不用 `dev_1`）；`pairing_handler_test.go` 用 `httptest.NewServer(http.HandlerFunc(handlePairingWebSocket))` 起 `/pairing`，可复用作 WS 拨号入口；`server_auth_test.go` 提供 `httptest.NewRecorder` 级的 auth middleware 断言。尚无现成的“带设备认证的 bridge WS 拨号 helper”，`dialAuthenticatedBridge` 需新建：用 `httptest.NewServer` 起主 server，构造 `TrustedDeviceRecord` 并把其明文 token 作为 `Authorization: Bearer` 头拨号。

#### P0-1：拒绝 workspace 外路径和 symlink 逃逸

```go
func TestReadFileRejectsOutsideWorkspaceAndSymlinkEscape(t *testing.T) {
    workspace := t.TempDir()
    secretDir := t.TempDir()
    secret := filepath.Join(secretDir, "management-token")
    require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o600))
    require.NoError(t, os.Symlink(secret, filepath.Join(workspace, "link")))

    for _, path := range []string{secret, filepath.Join(workspace, "link")} {
        result := callReadFile(t, workspace, path)
        require.Equal(t, "file.outside_authorized_root", result.Error.Code)
        require.NotContains(t, result.JSON, "secret")
    }
}
```

验收点：绝对路径、`../` 和 symlink 三种方式都不能越过授权根；management token 内容不能进入响应或日志。

#### P1-1：直连 WebSocket 超限帧被关闭

```go
func TestBridgeRejectsInboundFrameOverOneMiB(t *testing.T) {
    ws := dialAuthenticatedBridge(t)
    oversized := bytes.Repeat([]byte("x"), (1<<20)+1)
    require.NoError(t, ws.WriteMessage(websocket.TextMessage, oversized))

    _, _, err := ws.ReadMessage()
    require.Error(t, err)
    require.True(t, websocket.IsCloseError(err, websocket.CloseMessageTooBig))
}
```

验收点：服务端不分配与攻击帧成比例的长期内存，连接以稳定 close code 结束，其他连接继续可用。

#### P1-3：设备存储写盘失败不改变内存快照

```go
func TestFileDeviceStoreSaveFailureDoesNotCommitMemory(t *testing.T) {
    store := newStoreWithFailingAtomicWriter(t)
    before, _ := store.ListDevices()

    err := store.RevokeDevice("dev1")
    require.Error(t, err)

    after, _ := store.ListDevices()
    require.Equal(t, before, after)
}
```

验收点：写盘失败时内存和磁盘都保持旧状态；成功写入时文件始终是完整 JSON，重开后状态一致。

#### P1-7：手动配对码猜测被限流

```go
func TestPairingManualCodeAttemptsAreBounded(t *testing.T) {
    for i := 0; i < maxPairingAttempts; i++ {
        require.Equal(t, "pairing.invalid_code", claimManualCode(t, "000000").Code)
    }
    require.Equal(t, "pairing.rate_limited", claimManualCode(t, "000001").Code)
}
```

验收点：限制同时作用于来源、session 和全局并发；达到阈值后不会泄漏“码存在/不存在”的差异。

## 10. 最小错误码契约

错误码应在 transport、management API 和 Relay 间保持语义稳定；人类可读 message 可以本地化，客户端逻辑只能依赖 code。

| 错误码 | HTTP/WS 语义 | 使用场景 |
| --- | --- | --- |
| `file.outside_authorized_root` | RPC error | 路径不在授权 workspace/attachment 根 |
| `file.symlink_escape` | RPC error | symlink 解析后越界 |
| `file.sensitive_path_denied` | RPC error | 短期兼容阶段命中明确敏感路径 |
| `request.frame_too_large` | WS close 1009 | 入站帧超过 1 MiB |
| `auth.device_revoked` | HTTP 401 / WS policy close | 已吊销设备继续访问 |
| `pairing.invalid_code` | 配对结果失败 | 错误或过期手动码，不区分存在性 |
| `pairing.rate_limited` | HTTP 429 / 配对结果失败 | 配对尝试超过来源/session 限额 |
| `pairing.claim_conflict` | HTTP 409 | session 已被另一 claim 占用 |
| `runtime.management_bind_failed` | runtime error frame | product management API 无法监听 |
| `runtime.management_url_missing` | Mac 启动错误 | ready frame 缺少必需 management URL |
| `relay.proxy_header_untrusted` | 审计事件 | 非受信代理发送来源 IP header |
| `relay.pairing_claim_conflict` | HTTP 409 | 相同 route/claim ID 的不同提交冲突 |
| `relay.mailbox_capacity_exceeded` *(planned)* | HTTP/WS error | 未来选择拒绝新帧而非驱逐时使用；当前尚未实现 |
| `relay.prekey_exhausted` | RPC/event | 无可用 delivery prekey |
| `store.commit_failed` | HTTP 500 / runtime error | 安全状态无法原子持久化 |

## 11. 建议整改顺序

### 第一批：发布阻断项

1. 收紧或暂时移除任意路径 `read_file`。
2. 给直连入口增加读帧和结构化请求上限。
3. 修复 Relay 可信代理与限流桶回收。
4. 将设备状态改为原子、事务性持久化。
5. 删除产品模式下的 `ws://` 自动降级。
6. 停止记录消息预览并实现内容级 redaction。
7. management 启动失败双端 fail-fast：Go runtime 拒绝 ready，Mac 端拒绝空 `managementUrl`。
8. 给手动配对码增加限流、失败次数和 claim 前连接资源上限。

### 第二批：稳定性闭环

1. product mode management/identity 初始化 fail-fast。
2. 为 HTTP 握手设置超时和 header 上限。
3. 统一 secure atomic store。
4. 补充故障注入和资源边界测试。
5. 增加运行指标与错误码。
6. 迁移日志目录、设置权限与滚动，并同步修改 `CLAUDE.md:165`。

### 第三批：架构演进

1. 引入集中式 capability policy。
2. 按行为拆分大型 handler 和 RuntimeManager。
3. 消除包级全局 store/registry，改为显式依赖注入。
4. 固定 CI 工具和 Action 版本，增加漏洞扫描与 SBOM。

P2-7 的大文件拆分不阻塞当前发布，不应进入 P0/P1 修复 PR。

## 12. 发布验收 Checklist

以下发布门禁必须全部勾选。若某项因产品决策不适用，必须记录批准人、理由和替代控制，不能直接跳过。例外记录在 checklist 同目录的 `release-checklist-exceptions.md`，单行格式：`门禁项 | 批准人 | 理由 | 替代控制 | 日期`，PR 描述附该文件链接以便审计。

### 12.1 P0/P1 安全门禁

- [ ] **P0-1 文件边界**：`read_file` 只能读取授权 workspace/attachment 根；绝对路径、`../`、symlink 和敏感文件均被拒绝。
- [ ] **P0-1 回归测试**：`TestReadFileRejectsOutsideWorkspaceAndSymlinkEscape` 或等价测试通过，且日志中不出现 secret 内容。
- [ ] **P1-1 帧上限**：直连 Bridge 与 `/pairing` 在首次读取前设置明确 `SetReadLimit`；普通 RPC 入站上限为 1 MiB。
- [ ] **P1-1 回归测试**：`TestBridgeRejectsInboundFrameOverOneMiB` 或等价测试通过，超限连接关闭且其他连接仍健康。
- [ ] **P1-2 可信代理**：只有配置的 nginx/loopback 代理来源可以影响客户端 IP；伪造 `CF-Connecting-IP` 不能绕过限流。
- [ ] **P1-2 bucket 治理**：限流 bucket 有 TTL 清理和容量上限，并有高基数压力测试。
- [ ] **P1-3 持久化提交顺序**：设备状态按“锁内复制 → 修改副本 → 原子写盘 → swap 内存指针”提交。
- [ ] **P1-3 故障测试**：`TestFileDeviceStoreSaveFailureDoesNotCommitMemory`、损坏文件重开和成功提交重开测试通过。
- [ ] **P1-4 fail closed**：产品模式不存在 TLS 失败后自动发布 `ws://` 的路径；失败在 Mac UI 显式呈现。
- [ ] **P1-5 日志脱敏**：默认日志不包含 prompt、device token、management token、Relay/OpenCode credential。
- [ ] **P1-6 启动契约**：management bind 失败时 Go runtime 不发送 ready；Mac 端拒绝空 `managementUrl`。
- [ ] **P1-7 最小止损**：`manualCode` 不能单独查找 session，claim 必须同时提交 `pairingId`。
- [ ] **P1-7 完整防护**：来源/session/全局限流、失败失效、claim 前 read deadline/帧限制均已实现。
- [ ] **P1-7 回归测试**：`TestPairingManualCodeAttemptsAreBounded`、并发 claim 和存在性 oracle 测试通过。

### 12.2 运行与协议门禁

- [ ] §10 中本次实现涉及的错误码已集中定义，客户端不依赖本地化 message 分支。
- [ ] Device/management/Relay credential 的轮换或吊销会使旧凭据失效，并主动断开旧连接。
- [ ] management token 重置和 Relay route credential 轮换具有显式用户操作、失败提示与原子持久化。
- [ ] Relay mailbox eviction、rate-limit 命中和 store commit failure 有可观测计数或稳定日志事件。
- [ ] `CLAUDE.md:165` 已同步更新，不再把定时重启截断 `/tmp` 日志当作日志治理方案。
- [ ] 协议行为发生变化时，`docs/protocol/` 和 iOS 兼容说明已同步更新。

### 12.3 构建与测试门禁

- [ ] `go build ./go-bridge`
- [ ] `go test ./... -count=1`
- [ ] `go vet ./...`
- [ ] `(cd relay-server && go test ./... -count=1)`
- [ ] `(cd relay-server && go vet ./...)`
- [ ] `xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' build`
- [ ] P0/P1 新增定向回归测试全部通过，并在 CI 中持续运行。
- [ ] 未经用户明确批准，不以 UI test、snapshot test、Simulator automation 或真机流程替代上述代码级门禁。

## 13. 评审意见采纳矩阵

| 评审意见 | 处理 | 说明 |
| --- | --- | --- |
| 补充 management-token 攻击链 | 采纳 | 已加入 P0-1，并扩展到 relay identity、device store 和 agent auth 文件 |
| P1-1 入站上限给出 1 MiB | 采纳 | 已写入整改和测试草稿 |
| P1-2 拆成代理头绕过与 bucket 增长两个工单 | 采纳 | 保持同一风险编号，但要求独立追踪 |
| P1-3 复用 `core.AtomicWriteFile` | 部分采纳 | 可复用其思路和代码；现有原语仍缺目录 fsync/提交前内存快照，不能只机械替换 |
| P1-4 引用项目禁止 fallback 约束 | 采纳 | 已引用 `CLAUDE.md:160` |
| P1-5 日志路径降 P2、内容 redaction 保持 P1 | 采纳 | 已拆为 P1-5 与 P2-8 |
| “macOS `/tmp` 受 TCC，因此固定路径风险弱” | 不作为结论依据 | 不采纳这一绝对化前提；不同进程、用户、sandbox 和文件权限组合复杂。降级依据改为“需要本地落点，不是远程直接入口”，同时保留权限、symlink 和滚动问题 |
| P1-6 应描述为空 management URL，并双端修复 | 采纳 | 已修正文义并增加 `runtime.management_url_missing` |
| P2-4 nonce 风险措辞收窄 | 采纳 | 明确需要观察合法请求或 credential 泄漏才有实质危害 |
| 安全性评级 C 改 D | 采纳 | 任意文件读取可进一步触及管理 token，发布态应评 D |
| 增加威胁模型 | 采纳 | 已增加攻击者 A-E 和定级约定 |
| 增加 Relay prekey 专项 | 采纳审查范围，不采纳原漏洞假设 | Relay 无 prekey 表/端点，Go bridge 单设备上限 64；记录 `processedBatches` 有界化建议 |
| 增加 mailbox 垃圾驱逐分析 | 采纳审查范围，部分不采纳 | 普通 device 不能写 mailbox；只有认证 bridge/泄漏 route credential 可写。作为可靠性与凭据失陷后的 containment 问题处理 |
| 增加 route/claim 竞争分析 | 采纳 | activation identity 替换未发现漏洞；Relay pairing claim first-writer-wins 单列 P2-9 |
| 增加配对流程审查 | 采纳 | 新增专项，并确认手动码无在线猜测保护为 P1-7 |
| 附 PoC/测试草稿 | 采纳 | 提供 P0-1、P1-1、P1-3、P1-7 最小用例 |
| 给出错误码契约 | 采纳 | 新增最小错误码表 |
| 修改 `/tmp/go-bridge.log` 文档耦合 | 采纳 | P2-8 和第二批整改明确要求同步修改 `CLAUDE.md:165` |
| P2-7 不阻塞发布 | 采纳 | 已明确放入后续架构周期 |

### 13.1 第二轮评审意见

| R2 意见 | 处理 | 说明 |
| --- | --- | --- |
| P1-3 补 memory-before-disk 实现约束 | 采纳 | 已明确锁内深拷贝、写副本、成功后 swap 指针的提交顺序 |
| P2-9 与 P1-7 交叉说明定级差异 | 采纳 | 已说明无 secret 在线枚举与 QR capability 泄漏的门槛差异 |
| 评级表增加整改后预期 | 采纳 | 最终评级增加目标列 |
| PoC helper 落地说明 | 采纳 | 已要求复用现有测试 fixture 并在整改 PR 中补 helper |
| `relay.mailbox_capacity_exceeded` 标为未来策略 | 采纳 | 已增加 `planned` 标记 |
| 增加可勾选发布验收 checklist | 采纳 | 新增 §12，绑定 PoC、错误码、整改和构建门禁 |
| P1-7 取消 manualCode 单独查找作为最小止损 | 采纳 | 已列为 P1-7 第一条整改和发布门禁 |
| 明确凭据轮换责任边界 | 采纳 | 新增 §8.1，区分用户事件判断与系统一致性责任 |
| 威胁模型补充 C/D 获取成本差异 | 采纳 | 已增加横截面说明 |

### 13.2 第三轮评审意见

| R3 意见 | 处理 | 说明 |
| --- | --- | --- |
| P0-1 补 workspace 锚点 | 采纳 | 已在 P0-1 整改第 7 条给出 `getAgent`/`extractDir`/`directoryForSession`/`GetWorkDir()` 反查路径与拒绝条件 |
| P1-3 补 `RevokedAt` 指针深拷贝陷阱与 `Clone()` 缺失 | 采纳 | 已在 P1-3 整改显式提示 `*time.Time` 指针需单独复制，并说明需新增 `Clone()` |
| §9.1 设备 ID `dev_1` 与现有 fixture `dev1` 不一致 | 采纳 | 草稿已改为 `dev1`，并补充三个 fixture 各自提供的 helper 说明 |
| PoC helper 各提供什么 | 采纳 | §9.1 已写明 `newTestStore()`/`makeTestRecord()`/`httptest` 起服务方式及 `dialAuthenticatedBridge` 新建方式 |
| P1-5 脱敏清单补 OpenCode credential | 采纳 | P1-5 整改与测试断言已纳入 OpenCode credential |
| §12 例外记录位置 | 采纳 | §12 已指定 `release-checklist-exceptions.md` 单行格式与审计要求 |
| 维持 P1-3“部分采纳 core.AtomicWriteFile” | 维持 | 复用其思路，但显式要求补 `dir.Sync()`，保持原评级判断不变 |

## 14. 最终评级

| 维度 | 当前评级 | 发布门禁及优先整改完成后预期 | 说明 |
| --- | --- | --- | --- |
| 架构方向 | B+ | B+ | 边界清晰，Relay/E2EE 与 agent abstraction 设计较好 |
| 健壮性 | B | B+ | 完成入口限制、限流和启动契约后可提升 |
| 稳定性 | B- | B+ | 原子持久化和故障回归测试闭环后可提升 |
| 安全性 | D | B- | 必须关闭任意文件读取、明文降级、配对枚举和内容日志 |
| 可维护性 | C+ | B- | 第一批不会消除大文件债务，但错误码、policy 和测试契约可降低变更风险 |
| 交付工程 | B | B+ | 发布 checklist、固定依赖和持续安全回归测试落地后可提升 |

综合结论：项目已具备可靠原型和受控内测基础，但当前安全评级为 D，不应作为已完成安全加固的公网产品发布。完成 §12 发布门禁及优先整改后，安全性可重新评估至 B-，综合工程状态可评估至 B 左右。
