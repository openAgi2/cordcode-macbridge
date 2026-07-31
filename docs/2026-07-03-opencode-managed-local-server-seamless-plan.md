# OpenCode managed local server 无缝接入方案

> 日期：2026-07-03  
> 状态：已完成 / 正式关闭（2026-07-03）。39/39 todos proven done；真实 iOS 端到端流量证据 + owner 人工确认 iOS OpenCode 模式正常。详见同名完成情况报告「正式关闭」小节。  
> 目标读者：接手开发的 agent / reviewer  
> 目标：干净新用户环境中，用户打开 CordCode Link 后无需手动输入端口、命令或凭据；iOS 扫码审批后能看到 Mac 端 OpenCode Desktop 的项目和 session。

## 前置阅读

接手开发前先读下面五份文档，避免重复实现已经完成的 project-first 基础能力，或沿用已经被现场证据推翻的旧判断：

- [docs/2026-07-02-opencode-project-first-session-list-plan完成情况.md](2026-07-02-opencode-project-first-session-list-plan完成情况.md)：记录 project-first session list 已完成的实现、测试和验收状态。本文方案默认复用这条已落地链路，不重新设计 `list_projects` / `list_sessions` 的基础算法。
- [think.md](../think.md)：记录后续现场认知更新，尤其是 OpenCode Desktop scope、`local` 项目集合、array-only 判断被推翻等调查结论。实现 managed local server 时应以这里的更新结论和源码为准。
- [docs/2026-07-03-opencode-managed-local-server-seamless-plan-review.md](archive/2026-07-03-opencode-managed-local-server-seamless-plan-review.md)：首轮代码走读级评审。本文已吸收其中的 B2/B3/S1-S5/D4-D8；B1 的归属决策见下方“评审处理记录”。
- [docs/2026-07-03-opencode-managed-local-server-seamless-plan-review-r2.md](archive/2026-07-03-opencode-managed-local-server-seamless-plan-review-r2.md)：第二轮评审。本文已吸收其中 R2-2/R2-4/R2-6/R2-5/R2-7/R2-8/R2-9/R2-10；R2-1/R2-3 被提升为进入开发前的 owner 授权实测 gate。
- [docs/2026-07-03-opencode-managed-local-server-seamless-plan-review-r3.md](archive/2026-07-03-opencode-managed-local-server-seamless-plan-review-r3.md)：第三轮 sign-off 倾向评审。本文已吸收 DQ-0、`previousURL` 契约、bootstrap 超时、干净账户 TCC 复验、DQ-1 harness 路径收敛等收口建议。

## 评审处理记录

首轮评审提出的意见按“代码和本机实测为真相源”处理：

| 项 | 处理 | 原因 |
| --- | --- | --- |
| B1 文档可跟踪性 | 部分采纳 | 当前 `.gitignore` 明确把 `/docs/*` 作为 local-only，用户本轮也明确要求落到 `docs/`。本文不擅自改 `.gitignore` 或迁移到 `specs/`；但已同步 `CHANGELOG.md`，不再引用具体 gitignored 文件名，避免跟踪文件指向 clone 后不存在的路径。后续若 owner 决定 specs 进入 PR，应单独修改 `.gitignore` 或新建被跟踪目录。 |
| B2 CLI 假设未证 | 采纳并实测修正 | 本机 `opencode 1.17.13` 证明 `--print-logs` 存在，`--port` 默认是 `0`，不是“从 4096 起搜索”。本文已把 4096-4196 改为 CordCode 产品选择。 |
| B3 无缝目标 vs Desktop 已运行 | 采纳，选择维持原无缝目标 | 本任务目标是最终产品体验，不收紧为“提示用户手动重启”。本文把首次切换时的 OpenCode Desktop 自动 graceful quit + reopen 纳入开发范围，并明确风险、限制和验收分支。 |
| S1 scope key 契约 | 采纳 | 新增 managedURL canonical 契约与 Go/Swift 测试要求。 |
| S2 可测 seam | 采纳 | 新增 `CLIResolver` / `PortProber` / `HealthProbe` / `ProcessFactory` / `evaluateReady` 要求。 |
| S3 OpenCode 失败不拖累 Claude/Codex | 采纳 | 新增启动不变量。 |
| S4 orphan pid 策略 | 采纳 | 新增收养/清理策略。 |
| S5 持久化文件归属 | 采纳 | 固定为独立 `opencode-managed-server.json`，不复用用户配置语义的 `credentials.json`。 |

第二轮评审处理：

| 项 | 处理 | 原因 |
| --- | --- | --- |
| R2-1 Desktop quit 的 TCC 代价 | 采纳为开发前硬 gate，本文未擅自执行 | quit/reopen OpenCode Desktop 会打断用户当前 Desktop 状态；且 Terminal 里跑 `osascript` 只能证明 Terminal 的 TCC 行为，不能等价证明 CordCode Link 这个 ad-hoc app 的 TCC 行为。必须由 owner 授权后，用 CordCode Link 同签名上下文实测；唯一推荐路径是临时开发分支中的 `DesktopProcessController` harness。 |
| R2-3 Desktop 冷启动是否服从 managedURL | 采纳为开发前硬 gate，本文未擅自执行 | 该实测需要改写 OpenCode Desktop 配置并冷启动 Desktop，会影响用户现场。本文把它前置为 §12 step 0b/0c，任何不通过都必须回到 §1/§14 重新定目标。 |
| R2-2 Desktop 当前连接检测 | 采纳 | 明确只能使用写配置前的 `previousURL` 作为启发式，不把改写后的 `currentSidecarUrl` 当运行态事实。 |
| R2-4 restart 互触发循环 | 采纳 | 新增不变量：managed server restart 与 bridge restart 不能互相反向调用。 |
| R2-6 managed server 日志滚动 | 采纳 | 新增独立 `.err.log` 滚动要求，不依赖 `go-bridge.log` 的现有滚动。 |
| R2-5 canonical 单一来源 | 采纳 | 要求 `OpenCodeManagedServer` 规范化一次，后续 surface 复用同一个 `let managedURL`。 |
| R2-7 `service_discovery_future` 迁移二选一 | 采纳 | 只提示用户改选，不静默迁移显式选择。 |
| R2-8 默认值测试冲突 | 采纳 | 测试计划明确更新既有 `testMigrationFreshInstallDefaultsDisabled`。 |
| R2-9 ready 阈值 | 采纳 | `survive >= 1s + health pass` 才 ready。 |
| R2-10 stdout / stderr 文件 | 采纳 | 只保留 `opencode-managed-server.err.log`，stdout 不单独落空文件。 |

第三轮评审处理：

| 项 | 处理 | 原因 |
| --- | --- | --- |
| DQ-0 Desktop 热重载配置 | 采纳 | 新增为 DQ-1/DQ-2 之前的首个 owner 授权实测。若 Desktop 运行中 watch 配置并自动切到 managedURL，可省掉 quit/reopen、TCC 和中断 turn 的主要风险。 |
| `previousURL` 暴露契约 | 采纳 | 源码中 `previousURL` 当前只是 `configureOpenCodeDesktopSettings` 内部 local，函数返回 `Void`；本文改为要求返回 sync result，或由 coordinator 调用前显式读取。 |
| bootstrap 超时 | 采纳 | 明确等待 managed server ready 的上限为 5 秒，超时放行 bridge，OpenCode 后台继续重试。 |
| 干净账户 TCC 复验 | 采纳 | DQ-1 在开发机通过不等于终端用户机器通过；真机/干净账户验收必须覆盖首次自动切换无 TCC 弹窗。 |
| DQ-1 helper 路径 | 采纳 | 临时开发分支 harness 是唯一推荐路径；最小 helper 仅作不得已备选，避免 bundle id / signing identity 不等价。 |

## CLI 实测取证

本节来自 2026-07-03 本机实测，开发前若升级了 `opencode` 版本，需要重新跑同类命令并更新结论。

```bash
$ command -v opencode
/opt/homebrew/bin/opencode

$ opencode --version
1.17.13

$ opencode serve --help
opencode serve

starts a headless opencode server

Options:
  -h, --help         show help
  -v, --version      show version number
      --print-logs   print logs to stderr
      --log-level    log level
      --pure         run without external plugins
      --port         port to listen on [default: 0]
      --hostname     hostname to listen on [default: "127.0.0.1"]
      --mdns         enable mDNS service discovery (defaults hostname to 0.0.0.0)
      --mdns-domain  custom domain name for mDNS
      --cors         additional domains to allow for CORS
```

短暂启动验证：

```bash
OPENCODE_SERVER_USERNAME=opencode \
OPENCODE_SERVER_PASSWORD=<redacted> \
opencode serve --hostname 127.0.0.1 --port 4197 --print-logs
```

实测结果：

- no-auth `GET /global/health` 返回 `401`。
- Basic Auth `opencode:<password>` 的 `GET /global/health` 返回 `200`。
- authed body 为 `{"healthy":true,"version":"1.17.13"}`。
- stderr 会输出 `opencode server listening on http://127.0.0.1:4197`。
- `--port` 默认值是 `0`。因此本文后续的 `4096...4196` 是 CordCode 的可预测端口选择，不是 OpenCode CLI 自身默认行为。

## Desktop 承载性实测 gate

本节是进入开发前的硬 gate。当前文档没有执行这些实测，因为它们会改写 OpenCode Desktop 配置，部分路径还会 quit/reopen OpenCode Desktop，可能中断用户当前工作。接手开发 agent 必须在 owner 明确授权后先做完，并把结果追加到本文。

执行顺序固定为 DQ-0 → DQ-1 → DQ-2。DQ-0 成立时，优先采用热重载路径，DQ-1 的 graceful quit/reopen 只作为热重载失败时的 fallback gate。

### Gate DQ-0：Desktop 运行中是否热重载配置

目标：验证 OpenCode Desktop 运行时是否 watch `opencode.global.dat` / `opencode.settings`，并在 CordCode 写入 managedURL 后自动切换到该 server。如果热重载成立，最复杂的 graceful quit + reopen、TCC、打断 turn 风险都可以降为 fallback。

实测步骤：

1. 备份：
   - `~/Library/Application Support/ai.opencode.desktop/opencode.global.dat`
   - `~/Library/Application Support/ai.opencode.desktop/opencode.settings`
2. 启动一个带 Basic Auth 的外部 OpenCode server，例如：

   ```bash
   OPENCODE_SERVER_USERNAME=opencode \
   OPENCODE_SERVER_PASSWORD=<redacted> \
   opencode serve --hostname 127.0.0.1 --port 4198 --print-logs
   ```

3. 保持 OpenCode Desktop 正在运行，且当前连接仍为 `vlocal` / 非 `http://127.0.0.1:4198`。
4. 不 quit Desktop，直接把 Desktop 配置写成 `http://127.0.0.1:4198`：
   - `server.currentSidecarUrl`
   - `server.list[]` 对应 HTTP entry
   - `server.projects["http://127.0.0.1:4198"]`
   - `opencode.settings.defaultServerUrl`
5. 观察 5 到 30 秒，用运行态证据确认 Desktop 是否自动切换：
   - `lsof -nP -iTCP -sTCP:ESTABLISHED | grep -E 'OpenCode|4198'`
   - OpenCode Desktop 自身日志或网络面板
   - `opencode.global.dat` 重读后的 `currentSidecarUrl`

判定：

- 若 Desktop 自动切到 `http://127.0.0.1:4198`，本方案优先实现“只写配置 + 等待热重载确认”。graceful quit/reopen 降级为热重载超时或失败时的 fallback，DQ-1 只需验证 fallback 路径。
- 若 Desktop 在 30 秒内不切换，维持现有 graceful quit + reopen 方案，继续 DQ-1/DQ-2。

### Gate DQ-1：Desktop quit 机制是否触发 TCC

目标：找出 CordCode Link 可用、且不会让干净新用户额外处理 macOS Automation 授权弹窗的 Desktop graceful quit 路径。

必须比较：

1. Apple Event / AppleScript 路径，例如 `osascript -e 'tell application "OpenCode" to quit'`。
2. Cocoa 路径：`NSRunningApplication.requestTermination(_:)`。

实测要求：

- 不能只从 Terminal 跑 `osascript` 后下结论。TCC 的发送方是发起进程，Terminal 的授权结果不能代表 CordCode Link。
- 必须用 CordCode Link 同签名上下文验证。唯一推荐路径是在临时开发分支中加入最小 `DesktopProcessController` harness，由实际 CordCode Link app 进程触发 quit。最小 helper 仅在无法改临时分支时作为备选；使用 helper 时必须说明 bundle id / signing mode / cdhash 与 CordCode Link 的等价性，否则证据无效。
- 记录是否出现 `"CordCode Link" 想要控制 "OpenCode.app"` 或等价 Automation 弹窗。
- 记录重建 / 重新安装后是否再次弹窗。ad-hoc 签名身份不稳定时，首次成功不代表发布体验稳定；开发机上已有历史授权也不能代表终端用户干净机器。

判定：

- 若 `NSRunningApplication.requestTermination(_:)` 不触发 TCC 且能正常退出 OpenCode Desktop，则本方案指定只用该路径，禁止使用 `osascript` / `NSAppleScript` / Apple Events。
- 若所有正常 quit 路径都触发 TCC，则 §1/§14 的“无需手动”目标不成立。必须由 owner 决定：接受首次授权步骤、收紧目标为“提示用户重启 Desktop”、或寻找 OpenCode Desktop 官方切换机制。

### Gate DQ-2：Desktop 冷启动是否服从 managedURL

目标：证明 CordCode 写入 `opencode.global.dat` / `opencode.settings` 后，OpenCode Desktop 冷启动会连接 managedURL，而不是每次重新创建随机 `vlocal`。

实测步骤：

1. 备份：
   - `~/Library/Application Support/ai.opencode.desktop/opencode.global.dat`
   - `~/Library/Application Support/ai.opencode.desktop/opencode.settings`
2. 启动一个带 Basic Auth 的外部 OpenCode server，例如：

   ```bash
   OPENCODE_SERVER_USERNAME=opencode \
   OPENCODE_SERVER_PASSWORD=<redacted> \
   opencode serve --hostname 127.0.0.1 --port 4198 --print-logs
   ```

3. 在 OpenCode Desktop 完全退出状态下，把 Desktop 配置写成 `http://127.0.0.1:4198`：
   - `server.currentSidecarUrl`
   - `server.list[]` 对应 HTTP entry
   - `server.projects["http://127.0.0.1:4198"]`
   - `opencode.settings.defaultServerUrl`
4. 冷启动 OpenCode Desktop。
5. 用运行态证据确认 Desktop 实际连接目标：
   - `lsof -nP -iTCP -sTCP:ESTABLISHED | grep -E 'OpenCode|4198'`
   - OpenCode Desktop 自身日志或网络面板
   - `opencode.global.dat` 重读后的 `currentSidecarUrl`

判定：

- 若 Desktop 实际连接 `http://127.0.0.1:4198`，本文的 graceful restart 方案成立，可进入开发。
- 若 Desktop 冷启动仍自起 `vlocal`，则本方案不能满足 §14。必须暂停实现，回到 §1/§14 重新定义“无缝”边界或寻找 Desktop 支持的 server 切换机制。

## 0. 背景与结论

当前 `external_http` Phase A 已证明一件事：只要 OpenCode Desktop、MacBridge runtime、iOS 连接的是同一个 OpenCode HTTP server，现有 project-first session 列表就能和 Desktop 对齐。

但 Phase A 仍要求用户手动启动：

```bash
OPENCODE_SERVER_PASSWORD='<password>' opencode serve --hostname 127.0.0.1 --port <port>
```

这不是最终产品体验。最终目标应与 Claude Code / Codex 一致：新用户安装后扫码即可使用。

当前 stable `opencode 1.17.13` 的事实：

- 没有可用的 `opencode service` 命令。
- `opencode serve --help` 没有 `--register`。
- `opencode serve --help` 有 `--print-logs`。
- `opencode serve --help` 显示 `--port` 默认值为 `0`，不会证明它默认从 `4096` 起找端口。
- OpenCode Desktop 自带 `vlocal` sidecar 使用随机 loopback 端口 + 随机密码，密码只在 Electron 内部 IPC 流转，CordCode 外部进程不能可靠发现。
- stable `opencode serve` 支持 loopback server + Basic Auth。

因此本方案选择：

**CordCode Link 自己启动并管理一个认证的本机 OpenCode shared server，然后自动把 OpenCode Desktop 默认 server 和 MacBridge runtime 都指向它。**

这不是实验路线，而是本轮要实现的最终目标路径。

## 1. 目标用户路径

干净新用户环境：

1. 用户已安装 OpenCode CLI / OpenCode Desktop。
2. 用户安装并打开 CordCode Link。
3. CordCode Link 检测到 `opencode` CLI。
4. CordCode Link 自动启动一个 loopback-only、Basic Auth 的 OpenCode managed server。
5. CordCode Link 自动把 OpenCode Desktop 的默认 server 配置到该 managed server。
6. CordCode Link 自动把 Desktop `local` scope 的项目集合合并到 managed server scope。
7. 用户在 iOS CordCode 扫码并审批。
8. iOS OpenCode 模式显示 Mac 端 OpenCode Desktop 的项目和 session，项目/session 列表与 Desktop 保持一致。

用户不需要：

- 输入端口。
- 复制命令。
- 手动设置 `OPENCODE_SERVER_PASSWORD`。
- 手动编辑 OpenCode Desktop 配置文件。
- 手动重启 OpenCode Desktop 来完成首次切换。
- 理解 `local` / `4096` / `64667` / `server scope`。

## 2. 非目标

- 不修改 OpenCode / OpenCode Desktop 源码。
- 不抓取 Electron IPC、DevTools、内存或日志中的 Desktop `vlocal` 密码。
- 不连接无密码 OpenCode server 作为默认路径。
- 不把 `64667` 或 `4096` 作为不可变产品端口；端口应可探测和持久化。
- 不默认绑定 `0.0.0.0`、LAN IP 或 mDNS。
- 不用 mock/fallback/placeholder 伪造 OpenCode 可用。
- 不让 iOS 直连 OpenCode server；iOS 仍只连 MacBridge。
- 不在本轮改 project-first session 列表算法；现有 `list_projects` / `list_sessions` 路径应复用。

## 3. 关键约束

### 安全

- managed server 必须绑定 `127.0.0.1`。
- password 必须随机生成并持久化为 `0600`。
- password 不得进入 argv、日志、Desktop `opencode.settings` 明文之外的非必要位置。  
  注：OpenCode Desktop 自身 server list 需要保存 HTTP server password，这是 Desktop 的现有数据模型；CordCode 写入时不得额外打印。
- `--print-logs` 捕获的是 OpenCode 自身 stderr，CordCode 不能绝对保证上游永不打印敏感字段。写入 `logs/opencode-managed-server.err.log` 前必须走现有脱敏策略或等价过滤，至少覆盖 password 明文、Basic Auth header、`OPENCODE_SERVER_PASSWORD`。
- `opencode serve` 的 no-auth `/global/health` 必须返回 `401`，authed `/global/health` 必须返回 `200` 且 body 符合 `{"healthy":true,"version":"..."}`。

### 运行态

- CordCode Link 是 `cccode-bridge-runtime` 的 supervisor，也应成为 managed OpenCode server 的 supervisor，至少在 Phase B 初版中如此。
- managed OpenCode server 崩溃后应重启；连续失败应暴露错误状态，不用旧 server 伪装成功。
- CordCode Link 退出时可以停止 child-process 版 managed server；若后续升级 LaunchAgent，再改为常驻。
- 不得影响用户手动选择 `external_http` / `legacy_64667` / `disabled` 的能力。
- managed server 失败不能阻塞 `cccode-bridge-runtime` 启动。OpenCode 可以 degraded / unavailable，但 Claude 与 Codex 必须照常可用。
- managed server restart 与 bridge restart 必须接入同一套 `launchGeneration` / config generation 收敛，不得各自独立触发互相覆盖的 restart。
- managed server restart 不得直接触发 bridge restart；bridge restart 也不得反向调用 managed server restart。配置变更时只调度一次 generation，顺序固定为 `ensure managed server` → `derive RuntimeConfig` → `start/restart bridge`。

### Desktop scope

OpenCode Desktop 可见项目集合在：

```text
~/Library/Application Support/ai.opencode.desktop/opencode.global.dat
  root["server"] JSON string
    server.projects["local"]              # Desktop 原始本地项目集合
    server.projects["<managed-url>"]      # shared server scope
    server.currentSidecarUrl
    server.list[]
    server.lastProject
```

新 endpoint 写入时必须：

1. 优先读取 `projects["local"]`。
2. 合并到 `projects[managedURL]`。
3. 若 `projects[managedURL]` 已存在，按 `worktree` 去重保留已有项并追加缺项。
4. 用旧 active URL / `legacy_64667` 仅补充 `local` 没有的项目。
5. `lastProject[managedURL]` 已存在则保留；不存在时优先用 `lastProject["local"]`。

这条约束来自 2026-07-03 的真实回归：只迁移 `64667` 会把 10 个 Desktop 项目缩成 1 个 `Chat`。

### managedURL canonical 契约

所有组件必须使用同一个 managedURL 字节形式：

```text
http://127.0.0.1:<port>
```

要求：

- 无 trailing slash。
- host 固定为 IPv4 `127.0.0.1`，不要写 `localhost` 或 `[::1]`。
- 必须显式带端口。
- `OpenCodeEndpointResolver.normalizeLoopbackURL` 输出、`OpenCodeManagedServer` 持久化 `url`、Desktop `currentSidecarUrl`、Desktop `opencode.settings.defaultServerUrl`、`projects[managedURL]` key、RuntimeConfig `opencodeURL`、Go `NewOpenCodeProxy(baseURL)` 输入必须一致。
- Go 侧当前会 `strings.TrimRight(baseURL, "/")` 后按 key 查 `server.projects`；如果 Swift 写入了带 slash 的 key，Go 会静默读不到该 scope 并 fallback `local`。因此测试必须覆盖 trailing slash 不一致时的行为，避免 session scope 分裂被项目 fallback 掩盖。
- canonical URL 必须在 `OpenCodeManagedServer` 内通过 `OpenCodeEndpointResolver.normalizeLoopbackURL` 产出一次，保存为单一 `let managedURL`，后续 7 个 surface 复用该 `String`。禁止各调用点重新拼接 URL。

## 4. 现有实现可复用部分

### Swift / MacBridge

- `OpenCodeEndpointResolver`
  - 已有 `external_http` / `legacy_64667` / `service_discovery_future` / `disabled`。
  - 已有 loopback URL 规范化和 password required 规则。
- `OpenCodeHealthValidator`
  - 已有 no-auth 401 + authed 200 校验。
- `RuntimeManager.configureOpenCodeDesktopSettings`
  - 已能写 `opencode.global.dat` 和 `opencode.settings`。
  - 已修复为合并 `local` 项目集合到 endpoint scope。
- `RuntimeManager`
  - 已能把 `-opencode-url <url>` 传给 runtime。
  - password 已走 `OPENCODE_SERVER_PASSWORD` env，不进 argv。
- `SettingsViewModel`
  - 已能保存 credentials/source/url。

### Go runtime

- `agent/opencode.New`
  - 已取消隐式 fallback `64667`。
  - URL 为空时 degraded / not_configured。
- `go-bridge/opencode-proxy.go`
  - 已支持 OpenCode HTTP API、Basic Auth、projects/sessions/messages/todos/models。
- `go-bridge/handlers.go`
  - `list_projects` 已按 Desktop visible project dirs 过滤。
  - `list_sessions` 已走 project-first cursor 分页方案。

### iOS

无需为本方案新增 iOS 协议。只要 MacBridge OpenCode backend available 且 projects/sessions 返回正确，iOS 现有 OpenCode project-first UI 应可直接工作。

## 5. 新增 source：`managed_local`

在 `OpenCodeServerSource` 增加：

```swift
case managedLocal = "managed_local"
```

语义：

- CordCode Link 自动启动/管理 OpenCode shared server。
- 新装默认 source 应为 `managed_local`。
- 已有用户显式配置的 `external_http` / `legacy_64667` / `disabled` 必须保留，不强行迁移。
- `service_discovery_future` 保留为未来官方 daemon 路线，但不是当前默认；它从未可用，不应阻止新装进入 `managed_local`。

迁移规则：

| 场景 | source |
| --- | --- |
| credentials.json 已有显式 `opencode_source` | 尊重现值 |
| 全新安装且无显式 source | `managed_local` |
| 旧版本已有 credentials 但无 source | 保守迁移到 `legacy_64667` 或改为一次性提示迁移；不要静默破坏老用户 |
| 显式 source 为 `service_discovery_future` | 不视为可用 source；保留用户显式选择，但在 UI 提示当前 stable 不可用，并建议改选 `managed_local` |

建议本轮对旧用户保持现有升级连续性：无 source 但已有 credentials 仍迁 `legacy_64667`。新装默认才是 `managed_local`。后续可做显式迁移按钮。

## 6. 新组件：OpenCodeManagedServer

新增 Swift 服务，建议文件：

```text
MacBridge/MacBridge/Services/OpenCodeManagedServer.swift
```

职责：

1. 查找 `opencode` CLI。
2. 维护 managed server 状态。
3. 选择/持久化端口。
4. 生成/持久化 username/password。
5. 启动 `opencode serve`。
6. 解析 stdout 或健康检查得到 URL。
7. 失败时提供明确诊断。
8. stop/restart。

### 状态模型

建议：

```swift
enum OpenCodeManagedServerState: Equatable {
    case disabled
    case starting
    case running(url: String, pid: Int)
    case unavailable(reason: String)
    case crashed(reason: String)
}
```

持久化文件建议：

```text
~/Library/Application Support/CordCode Link/opencode-managed-server.json
```

字段：

```json
{
  "version": 1,
  "url": "http://127.0.0.1:4096",
  "port": 4096,
  "username": "opencode",
  "password": "<random>",
  "pid": 12345,
  "updated_at": "..."
}
```

权限：

- `opencode-managed-server.json`: `0600`
- 不复用 `credentials.json` 保存 managed runtime 状态。`credentials.json` 表示用户可编辑的 OpenCode endpoint 配置；`opencode-managed-server.json` 表示 CordCode 托管进程的 url/port/pid/password，语义不同。
- 写入必须原子化：写临时文件、落盘、再 rename；单测覆盖 `0600` 权限、损坏 JSON、旧版本字段迁移。

### CLI 查找

优先级：

1. 用户显式配置的 CLI path（如果未来设置里提供）。
2. `ProcessInfo.processInfo.environment["PATH"]`。
3. 常见路径：
   - `/opt/homebrew/bin/opencode`
   - `/usr/local/bin/opencode`
   - `/usr/bin/opencode`（通常不存在，但可探测）
4. 找不到则 OpenCode backend 显示 unavailable：`opencode CLI not found`。

macOS GUI app 的 PATH 经常不包含 Homebrew 路径，所以 `/opt/homebrew/bin` / `/usr/local/bin` 的显式探测不是冗余逻辑，不能在“清理代码”时删掉。

不要静默安装 OpenCode。

### 可测 seam

`OpenCodeManagedServer` 不应在单元测试里依赖真实 `opencode` 二进制或真实端口。实现时必须把下面 seam 注入：

- `CLIResolver`: 返回 `opencode` 路径或 nil。
- `PortProber`: 判断端口占用、选择端口、模拟端口耗尽。
- `HealthProbe`: 封装 no-auth / authed `/global/health`，复用 `OpenCodeHealthValidator` 的 fetch 注入风格。
- `ProcessFactory`: 构造/启动进程，测试中可记录 argv/env 并模拟 pid/退出。
- `DesktopProcessController`: 查询/quit/open OpenCode Desktop，测试中模拟“未运行 / 已运行且指向 vlocal / quit 失败”。
- `evaluateReady(processAlive, noAuthStatus, authedStatus, stdoutHint) -> State`: 纯函数表驱动测试，stdout hint 只能加速，不能单独判 ready。

### 启动命令

使用 `Process`：

```text
OPENCODE_SERVER_USERNAME=opencode
OPENCODE_SERVER_PASSWORD=<password>
opencode serve --hostname 127.0.0.1 --port <port> --print-logs
```

注意：

- password 通过 env 传，不进 argv。
- `--hostname 127.0.0.1` 必须固定。
- 不使用 `--mdns`。
- `--port <port>` 必须显式传入。虽然 OpenCode 默认 `--port 0` 可以由系统分配端口，但 CordCode 需要稳定 URL 写入 Desktop scope，所以不能依赖随机端口。
- `--print-logs` 实测输出到 stderr；只落盘到 CordCode log dir 下的 `logs/opencode-managed-server.err.log`。stdout 预期为空，本轮不单独创建空的 stdout log 文件。
- 日志落盘前必须脱敏 password / Basic Auth / `OPENCODE_SERVER_PASSWORD`。
- `opencode-managed-server.err.log` 是独立文件，不受 RuntimeManager 当前 `go-bridge.log` 滚动逻辑保护。`OpenCodeManagedServer` 必须实现自己的 size-cap 滚动，建议复用 8 MiB、保留 3 代的策略。

### 端口策略

推荐：

1. 若持久化 port 存在，先尝试该 port。
2. 否则从 `4096` 开始探测到 `4196`。这是 CordCode 产品策略，不是 OpenCode CLI 默认行为。
3. 端口被占用时：
   - 如果占用者是一个健康、认证匹配的 managed OpenCode server，可复用。
   - 否则选择下一个端口。
4. 选定后持久化 URL。

不要固定要求 `4096` 必须可用。

### ready 判定

ready 必须满足：

1. Process 已启动并存活至少 1 秒；1 秒内退出计为 `crashed`，纳入连续失败计数。
2. `/global/health` no-auth 返回 `401`。
3. `/global/health` with Basic Auth 返回 `200` + OpenCode health body。

stdout 中 `server listening on ...` 可用于加速发现 URL，但不能单独作为 ready。

### 启动时的旧进程 / orphan 处置

MacBridge 崩溃或被 kill 时，child-process 版 `opencode serve` 可能成为 orphan。启动 managed server 时按以下策略处理：

1. 若 `opencode-managed-server.json` 存在，且 `pid` 仍存活，且该 pid 的命令行能确认是 `opencode serve --hostname 127.0.0.1 --port <persisted-port>`，先执行健康检查。
2. 健康检查通过且 Basic Auth 匹配，则收养该进程：更新状态为 `running(url, pid)`，不再启动第二个进程。
3. 若 pid 存活但命令行不匹配，不杀，视为无关进程，选择下一个端口。
4. 若 pid 命令行匹配但健康检查失败，先 graceful terminate；超时后 kill；随后重新启动自己的 managed server。
5. 若持久化文件丢失，但端口被占用，不得凭端口号误杀。只能在健康+认证匹配时复用；认证无法匹配则选择下一个端口，并把占用诊断写入日志。

这条策略避免两种风险：误杀同 PID 复用后的无关进程，以及无限累积旧 `opencode serve` orphan。

### crash / restart

建议最小策略：

- 进程退出后，若 source 仍是 `managed_local`，自动重启。
- 连续失败计数，例如 5 次 / 60 秒，进入 `unavailable`，不无限刷屏。
- 失败原因进入 UI / status / logs。
- runtime 不应连接旧 URL 假装 available。

## 7. AppDependencies / RuntimeManager 集成

### 启动顺序

当前 `AppDependencies.init()` 构造 `RuntimeConfig` 后启动 `runtimeManager.start()`。

managed_local 需要变成：

1. 读取 credentials/source。
2. 若 source == `managed_local`：
   - 启动/复用 managed OpenCode server。
   - 得到 resolved endpoint URL + username/password。
   - 写 Desktop server config。
   - 若 Desktop 正在运行且未使用 managedURL，执行受控重启流程。
   - 再构造/更新 `RuntimeConfig.opencodeURL`。
3. 启动 `cccode-bridge-runtime`。

这可能要求把 OpenCode endpoint resolution 从纯同步 init 拆成 async bootstrap。可选实现：

- 先构造 RuntimeManager，但延迟 `startBridge()`，在 `Task` 中 await managed server ready 后启动。
- 或 RuntimeManager 支持启动后更新 OpenCode config 并 restart。  
  推荐第一种，避免首次启动先报 not_configured 再重启。

启动不变量：

- managed server ready 不是整个 bridge 的硬前置。等待 managed server bootstrap 的上限为 5 秒；超时后 `cccode-bridge-runtime` 仍必须启动，OpenCode descriptor 报 `not_configured` / `not_detected` / `service_not_running` 的真实原因，Claude/Codex 不受影响。OpenCodeManagedServer 在后台继续异步重试，最终把状态推进到 `running` 或 `unavailable`。
- bootstrap UI 需要能表达 `preparing OpenCode`、`OpenCode unavailable`、`Desktop sync pending`，不能无限 spinner。
- managed server start/stop/restart 与 bridge restart 必须共享 RuntimeManager 的 generation 收敛模型，避免 config 变更时一个 restart 覆盖另一个 restart。
- managed server restart 与 bridge restart 不能互相反向调用。配置变更时由上层 coordinator 创建一个 generation，并按固定顺序执行：`ensure managed server` → `write/sync Desktop config` → `derive RuntimeConfig` → `start/restart bridge`。运行中 managed server crash/restart 只更新 OpenCode 状态，不直接 restart bridge；只有 resolved endpoint 字节值变化时才由同一 generation 触发 bridge restart。

### RuntimeManager 责任边界

`RuntimeManager` 继续管理 `cccode-bridge-runtime`。

managed OpenCode server 可由独立 `OpenCodeManagedServer` 管理，但 RuntimeManager 需要：

- 在 config 变更时触发 managed server start/stop。
- 在 app shutdown 时 stop child-process 版 managed server。
- 在 restart runtime 前尽力确保 managed server ready；若失败，仍启动 runtime，但 OpenCode URL 为空或标记不可用，不影响其他 backend。

### Desktop 配置写入

复用并扩展：

```swift
RuntimeManager.configureOpenCodeDesktopSettings(
    desktopDir: ...,
    serverURL: managedURL,
    username: "opencode",
    password: password
)
```

必须保证：

- `server.list` 首位是 managed HTTP entry。
- 同 URL 旧 entry 去重。
- `currentSidecarUrl = managedURL`。
- `opencode.settings.defaultServerUrl = managedURL`。
- `projects[managedURL]` 合并 Desktop `local` 完整项目集合。

Desktop 运行时是否热重载配置由 Gate DQ-0 判定。为满足“扫码后无缝看到 Desktop 项目和 session”的最终目标，本轮范围包含 Desktop 自动切换：

- 写配置后记录状态：`desktop_config_synced`.
- 如果 OpenCode Desktop 未运行：只写配置，不主动打开 Desktop。
- Desktop 是否“已经连接 managedURL”只能用启发式判断：在写配置之前读取现有 `previousURL`，若 `previousURL == managedURL` 则认为无需重启；若 `previousURL != managedURL` 且 Desktop 进程正在运行，则先等待 DQ-0 热重载确认，失败后才执行 graceful quit + reopen fallback。
- `previousURL` 必须由实现显式暴露，不能依赖 `RuntimeManager.configureOpenCodeDesktopSettings` 当前函数体里的 local 变量。实现二选一：
  - 改签名，让 `configureOpenCodeDesktopSettings(...)` 返回 `OpenCodeDesktopSyncResult(previousSidecarURL: String?, didSidecarChange: Bool, didProjectsMerge: Bool)`。
  - 或由 bootstrap/coordinator 在调用 `configureOpenCodeDesktopSettings(...)` 前独立读取一次 `opencode.global.dat` 的 `server.currentSidecarUrl`，并把该值传入 Desktop 切换决策。
- 不得在写完配置后再读取 `currentSidecarUrl` 来判断运行态连接；那只是持久化意图，不是当前进程的真实连接。
- graceful quit 路径必须由 Gate DQ-1 实测决定。若 `NSRunningApplication.requestTermination(_:)` 不触发 TCC，则只用该路径；除非 Gate DQ-1 证明安全，否则禁止使用 `osascript` / `NSAppleScript` / Apple Events。
- graceful quit 后等待退出确认，再 `open /Applications/OpenCode.app` 或原 bundle path。
- 不直接 `kill -9`。若 Desktop 在超时内拒绝退出，保留失败现场，Settings 显示 `Desktop restart required` 诊断；这属于自动切换失败，不得伪装验收通过。
- UI 仍提供“Resync / Restart OpenCode Desktop”按钮，用于失败后的显式重试。

代价与边界：

- 自动重启 Desktop 可能中断用户正在进行的 OpenCode Desktop turn。为了满足本任务的“无手动步骤”目标，本方案接受这个产品代价，但实现必须在日志和 UI 中明确记录发生过 Desktop graceful restart。
- 该操作是本机 app 操作，不是 UI test；实现测试应通过 `DesktopProcessController` seam 模拟，不跑 UI automation。

## 8. Settings UI 调整

设置页应从“手动 external_http 为主”改为“自动托管为默认”。

建议 UI 文案：

- Server Source:
  - Automatic (Recommended): CordCode starts a local OpenCode server and connects Desktop + iOS to it.
  - External HTTP: connect to an existing OpenCode server.
  - Legacy 64667.
  - Disabled.

对于 `managed_local`：

- 显示 server 状态：Starting / Running / CLI not found / Auth failed / Crashed。
- 显示 URL（非 secret），例如 `http://127.0.0.1:4096`。
- 不显示 password，保留“Regenerate server password”按钮。
- 提供“Restart managed server”按钮。
- 提供“Resync OpenCode Desktop”按钮。

手动认证区仍可保留，但不要让新用户以为必须复制命令。

## 9. go-bridge 侧改动

Go runtime 大部分无需改。

必须确认：

1. `-opencode-url` 非空时：
   - OpenCode proxy 注册。
   - SSE subscriber 启动。
   - descriptor status available。
2. `list_projects` 对 managedURL scope 生效。
3. `list_sessions` 继续按 project-first cursor 分页。

建议补测试：

- `openCodeDesktopVisibleProjects` 当 `projects[managedURL]` 有 10 项时优先返回 managedURL。
- `projects[managedURL]` 缺失但 `local` 有项目时仍 fallback local（当前已有 fallback 逻辑，需守护）。
- managedURL canonical 测试：fixture 同时包含 `projects["http://127.0.0.1:4096"]` 与 `projects["http://127.0.0.1:4096/"]` 时，`baseURL="http://127.0.0.1:4096"` 必须命中无 slash key；当只有 slash key 时应按当前契约 fallback `local`，并通过测试暴露该不一致风险。

## 10. 测试计划

### Swift 单元测试

新增/扩展：

1. `OpenCodeManagedServerTests`
   - 找不到 CLI → unavailable。
   - 端口被占用 → 选择下一个端口。
   - stdout 不是唯一 ready 条件；health 不通过则 failed。
   - password 不出现在 argv。
   - env 包含 `OPENCODE_SERVER_PASSWORD`。
   - stop/restart 状态迁移。
   - `opencode-managed-server.json` 写入权限为 `0600`，损坏 JSON 不导致假成功。
   - `opencode-managed-server.err.log` 达到 size cap 后滚动，保留 3 代，不无限增长。
   - pid 存活且命令行匹配时可收养 orphan；pid 不匹配时不得误杀。
   - Desktop 已运行且指向 vlocal 时，通过 `DesktopProcessController` 触发 graceful quit + reopen。
   - Desktop quit 超时后进入诊断状态，不伪装同步完成。
   - DQ-0 热重载路径：写配置后等待 Desktop 连接 managedURL；成功时不调用 quit/reopen。
   - 5 秒 bootstrap 超时后 bridge 可启动，OpenCode 后台继续重试。
   - 所有测试通过 `CLIResolver` / `PortProber` / `HealthProbe` / `ProcessFactory` seam 完成，不依赖真实 `opencode`。

2. `OpenCodeEndpointResolverTests`
   - `managed_local` source resolve 行为：由 managed server 提供 URL，不要求用户输入 URL。
   - external_http 规则不变。
   - 更新既有 `testMigrationFreshInstallDefaultsDisabled`：新装默认应从 `disabled` 改为 `managed_local`。不要同时留下新旧两条互相矛盾的断言。
   - `service_discovery_future` 显式选择不被静默改写；UI/diagnostic 提示用户改选 `managed_local`。

3. `MacBridgeBehaviorTests`
   - 新装默认 source 为 `managed_local`。
   - managed_local ready 后 RuntimeConfig 包含 resolved URL。
   - Desktop config 写入 managedURL。
   - `projects[managedURL]` 合并 local 10 项，不被 legacy 1 项覆盖。
   - managedURL 在 RuntimeConfig、Desktop `currentSidecarUrl`、`defaultServerUrl`、`projects` key 中字节一致。
   - managedURL 由单一 `let managedURL` 传入所有 surface，不在 Desktop sync / RuntimeConfig / persisted state 各自重新拼接。
   - Desktop sync 暴露 `previousSidecarURL` / `didSidecarChange`，或 coordinator 在写配置前读取 previousURL；重启决策点不能依赖 Void 函数内部 local。
   - password 不进入 runtime argv。
   - managed server unavailable 时 bridge 仍启动，Claude/Codex 不受影响，OpenCode descriptor 暴露真实错误。
   - managed server crash/restart 不直接触发 bridge restart；只有 resolved endpoint 字节值变化时，同一 generation 才安排 bridge restart。

### Go 单元测试

1. `go-bridge` OpenCode list_projects visible project tests。
2. `go-bridge` managedURL canonical / trailing slash scope tests。
3. `go-bridge` OpenCode list_sessions existing pagination tests 不应回退。

### 定向 build/test

按仓库约束：

```bash
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink \
  -configuration Debug -destination 'platform=macOS' test \
  -only-testing:CordCodeLinkTests/OpenCodeManagedServerTests \
  -only-testing:CordCodeLinkTests/OpenCodeEndpointResolverTests \
  -only-testing:CordCodeLinkTests/MacBridgeBehaviorTests

go test ./go-bridge -run 'OpenCodeListSessions|OpenCodeListProjects|OpenCodeListDirectory' -count=1
```

修改 `MacBridge/` 或 `go-bridge/` 后必须：

```bash
./scripts/build-unsigned-release.sh
killall CordCodeLink 2>/dev/null || true
rm -rf /Applications/CordCodeLink.app
cp -R build/unsigned-release/Build/Products/Release/CordCodeLink.app /Applications/
open /Applications/CordCodeLink.app
```

安装后核对：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "CordCodeLink|cccode-bridge-runtime|opencode serve"
cat "$HOME/Library/Application Support/CordCode Link/runtime.json"
tail -n 100 "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"
```

### 手动/真机验收

UI automation / 真机操作仍需 owner 明确授权。

开发前人工 gate 必须先完成：

1. Gate DQ-0：OpenCode Desktop 运行中是否热重载写入的 managedURL。若成立，优先走热重载路径，DQ-1 仅验证 fallback。
2. Gate DQ-1：Desktop graceful quit 路径不会触发 macOS Automation/TCC 手动授权，或 owner 明确接受该手动授权并同步收紧 §14。
3. Gate DQ-2：OpenCode Desktop 冷启动服从写入的 managedURL，或 owner 明确接受 Desktop 不能自动切换并同步收紧 §14。

最终验收必须覆盖：

1. 干净数据目录或模拟干净新用户：
   - 无 `credentials.json`。
   - 无手动 `opencode_url`。
   - OpenCode Desktop 有 local projects。
2. 打开 CordCode Link。
3. managed server 自动启动，`opencode serve` 绑定 `127.0.0.1:<port>`。
4. `/internal/agents` 中 OpenCode `available`。
5. OpenCode Desktop 使用 managedURL。
6. `opencode.global.dat`:
   - `currentSidecarUrl == managedURL`
   - `projects[managedURL]` 包含 Desktop local 项目集合
7. iOS 扫码审批后：
   - OpenCode 项目列表显示 Desktop 项目。
   - 每个项目 session 数量与 Desktop 一致。
   - `Chat` 等大项目可分页加载。
   - go-bridge log 无 OpenCode ERROR/WARN。
8. Desktop 已运行分支：
   - 预先打开 OpenCode Desktop，使其连接 `vlocal`。
   - 打开 CordCode Link 后，CordCode 优先通过热重载切到 managedURL；若 DQ-0 不成立，则自动 graceful quit + reopen Desktop。
   - 切换后的 Desktop `currentSidecarUrl` / 实际连接目标为 managedURL。
   - iOS 与 Desktop 在同一项目/session scope 下对齐。
9. 干净账户首次自动切换：
   - 在未给 CordCode Link 授予 Automation 权限的干净 macOS 用户账户或等价环境中验证。
   - 首次触发 Desktop 自动切换不得出现 macOS Automation/TCC 授权弹窗；若出现，§14 的“无需手动”判定不成立。

## 11. 失败模式与预期表现

| 失败 | 表现 |
| --- | --- |
| 未安装 `opencode` CLI | OpenCode backend unavailable，Settings 显示安装提示 |
| 端口范围被占满 | managed server unavailable，显示端口占用诊断 |
| `opencode serve` 启动后无 auth | 视为严重错误，停止并显示 server_unauthenticated |
| Desktop 配置写入失败 | OpenCode backend 可用但 Desktop sync warning；iOS 可能仍能用 server catalog，但不承诺 Desktop 对齐 |
| Desktop 正在运行但未热重载配置 | 先等待热重载确认；30 秒未切换则走 graceful quit + reopen fallback；fallback 失败则 Settings 显示 `Desktop restart required`，验收不通过 |
| `previousURL` 启发式误判 Desktop 不在 managedURL | 可能多余 graceful restart 一次；日志记录 previousURL、managedURL、Desktop pid |
| `previousURL` 启发式漏判 Desktop 实际不在 managedURL | iOS/Desktop scope 仍可能分裂；Settings 显示 `Desktop sync suspect`，要求用户触发 Resync，验收不通过 |
| Desktop quit 路径触发 macOS TCC 授权 | §14 “无需手动”不成立；暂停实现，由 owner 选择接受首次授权、改为提示重启，或寻找非 TCC 路径 |
| Desktop 冷启动不服从 managedURL | graceful restart 路线无效；暂停实现，回到 §1/§14 重新定目标 |
| Desktop 正在进行 turn | 首次自动切换可能中断该 turn；日志/UI 记录 Desktop graceful restart |
| managed server crash | 自动重启；连续失败后 unavailable |
| managed server log 持续增长 | `opencode-managed-server.err.log` 自带 8 MiB × 3 代滚动；滚动失败进入 warning，不阻塞 bridge |
| MacBridge 崩溃留下 orphan `opencode serve` | 下次启动按 pid/命令行/health/auth 收养或清理，不凭端口号误杀 |
| 用户切到 external_http | 停止 managed child process，改用用户 endpoint |
| 用户切 disabled | 停止 managed child process，runtime 重启后 OpenCode not_configured |

## 12. 实现顺序

0. 开发前实测 gate：
   - 0a. 若 `opencode --version` 不是本文实测的 `1.17.13`，先重跑 CLI 实测取证并更新本文事实表。
   - 0b. owner 授权后执行 Gate DQ-0：Desktop 运行中是否热重载 managedURL。若通过，优先实现热重载路径，graceful quit/reopen 降级为 fallback。
   - 0c. owner 授权后执行 Gate DQ-1：Desktop quit 机制是否触发 TCC。若不通过，暂停实现，回到 §1/§14 修订目标或换机制。
   - 0d. owner 授权后执行 Gate DQ-2：Desktop 冷启动是否服从 managedURL。若不通过，暂停实现，回到 §1/§14 修订目标或换机制。
1. 增加 `managed_local` source 和迁移规则；更新 fresh install 默认值测试。
2. 实现 `OpenCodeManagedServer` 的持久化、CLI 查找、端口选择、启动、health、stop；同时实现所有可测 seam。
3. 实现 managed server 独立日志脱敏与 8 MiB × 3 代滚动。
4. 实现 orphan pid 收养/清理策略。
5. 调整 AppDependencies bootstrap：managed server 失败不阻塞 bridge，成功后向 RuntimeConfig 注入 canonical managedURL。
6. 复用现有 `migrateOpenCodeDesktopProjects`（已 `local`-first），补 managedURL scope、canonical URL、Desktop graceful restart 回归测试。
7. 调整 Settings UI，把 Automatic 设为推荐默认。
8. 补 Swift 单元测试。
9. 补 Go visible project / canonical URL tests。
10. 定向 build/test。
11. Release 构建 + 覆盖安装。
12. owner 授权后做真机端到端验收。
13. 更新 living docs：
    - `BUILD_INSTALL_AND_RUNTIME.md`
    - `GO_BRIDGE_ARCHITECTURE.md`
    - `docs/backends-and-config.md`（如存在/相关）
    - `CHANGELOG.md`

## 13. Reviewer 重点检查

Reviewer 应重点反查：

1. 是否仍有任何隐式 fallback 到 `64667`。
2. password 是否进入 argv/log。
3. managed server 是否只绑定 `127.0.0.1`。
4. Desktop 项目迁移是否优先 `local` 完整集合，而不是旧 `64667`。
5. 新装默认是否真的为 `managed_local`。
6. 失败时是否暴露真实错误，而不是回退到 mock/legacy。
7. UI 是否仍暗示用户必须手动复制 `opencode serve` 命令。
8. managedURL 是否在 Swift/Go/Desktop/持久化文件中保持 `http://127.0.0.1:<port>` 无 slash 契约。
9. `OpenCodeManagedServer` 单测是否通过 seam 完成，而不是依赖真实二进制或真实端口。
10. managed server 启动失败时 Claude/Codex 是否仍可用。
11. Desktop 已运行且指向 vlocal 的分支是否被验收覆盖。
12. orphan `opencode serve` 是否会被收养/清理，且不会误杀无关进程。
13. Desktop 自动 quit 路径是否经 CordCode Link 同签名上下文实测不触发 macOS Automation/TCC 授权框。
14. Desktop 冷启动连接目标是否经实测确认服从 `currentSidecarUrl` / `defaultServerUrl` 的 managedURL。
15. Desktop 运行中是否热重载配置已先于 quit/reopen 实测；若热重载成立，代码是否优先走无 quit 路径。
16. `previousURL` 是否通过返回值或前置读取暴露给决策点，而不是依赖 `configureOpenCodeDesktopSettings` 内部 local。
17. managed server restart 与 bridge restart 是否可能互相触发循环。
18. managed server 日志是否独立滚动，不依赖 `go-bridge.log` 的 rotate。
19. Release 安装后 `/Applications/CordCodeLink.app` 内嵌 runtime 是否为最新构建。

## 14. 验收判定

本任务完成的唯一标准：

**在干净新用户环境中，打开 CordCode Link 后无需手动输入端口/命令/凭据、无需手动重启 OpenCode Desktop，iOS 扫码审批后能看到 Mac 端 OpenCode Desktop 的项目和 session。**

如果仍需要用户手动启动 `opencode serve`、复制 password、输入 URL、编辑 Desktop 配置文件、手动重启 OpenCode Desktop，则本任务未完成。

验收必须同时覆盖两条路径：

- OpenCode Desktop 未运行：CordCode 写入配置并启动 managed server，iOS 可见 Desktop local 项目/session。
- OpenCode Desktop 已运行且指向 vlocal：CordCode 优先通过热重载切换 Desktop；若 DQ-0 不成立，则自动 graceful quit + reopen Desktop。切换后 Desktop 与 iOS 同指向 managedURL scope。

若 Gate DQ-0 成立，本文 graceful quit/reopen 相关范围应降为 fallback。若 Gate DQ-1 或 Gate DQ-2 在开发前实测不通过，本节必须先重写。不得在已知需要 TCC 手动授权或 Desktop 不服从 managedURL 的情况下继续声称“无需手动”。
