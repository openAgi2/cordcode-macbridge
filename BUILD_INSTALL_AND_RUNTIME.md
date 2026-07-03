# MacBridge 构建、安装与运行态排查

> 本文从原一体仓库 `opencode-cc-connect/MacBridge编译安装指南.md` 与
> `本机服务常驻与端口约定.md` 迁移，并按拆分后的 `cordcode-macbridge` 当前实现校正。

## 组件与所有权

`CordCodeLink.app` 由两部分组成：

| 组件 | 位置 | 职责 |
| --- | --- | --- |
| SwiftUI macOS app | `MacBridge/` | 设置、配对、设备管理、runtime 生命周期 |
| Go runtime | `go-bridge/` | Bridge WebSocket、agent 适配、Management API、Relay |

Release 构建会把 Go runtime 编译为
`CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`。产品运行态中，
`CordCodeLink.app` 是 runtime 的唯一 supervisor；不要再为 `go-bridge` 安装独立
LaunchAgent，也不要让仓库里的开发二进制长期占用 `8777`。

## 端口与外部依赖

| 服务 | 默认端口 | 所有者 | iOS 是否直连 |
| --- | ---: | --- | --- |
| Bridge WebSocket | `8777` | `cordcode-bridge-runtime` | 是，局域网 `ws://` |
| Bridge TLS | `8778` | `cordcode-bridge-runtime` | 是，仅 Tailscale `wss://` |
| Management API | 随机 loopback 端口 | runtime，Mac app 调用 | 否 |
| OpenCode HTTP/SSE | `4096...4196`（managed）/ `64667`（legacy） | CordCode Link 默认托管 `opencode serve`；legacy/external 模式可由用户提供；端口选择在 Swift 端完成 | 否 |
| Codex app-server | `4141` | Codex app/CLI | 否 |
| Relay runtime | `8780` loopback | VPS 上的 `relay-server` | 否，外部由 nginx `8443` 终止 TLS |

当前正式 runtime 注册 `claude,opencode,codex`。Copilot 仍存在于部分通用类型与旧
UI 兼容代码中，但不属于当前 MacBridge runtime 的已迁移 backend。

## runtime 启动配置

产品态参数由 `RuntimeManager` 生成；通常应从 MacBridge UI 修改配置并让 supervisor
重启 runtime，不要手工修改 `/Applications` 内的命令行。

主要参数：

| Flag | 默认/产品值 | 作用 |
| --- | --- | --- |
| `-port` | `8777` | LAN Bridge WebSocket |
| `-drivers` | `claude,opencode,codex` | 注册的 backend |
| `-work-dir` | 当前目录 | agent 默认工作目录 |
| `-codex-backend` | CLI 默认 `exec`；Mac app 产品态 `app_server` | Codex 模式 |
| `-codex-app-server-url` | 空；显式配置时如 `ws://127.0.0.1:4141` | 可选共享 Codex service |
| `-opencode-url` | 空；Mac app 传入 resolved loopback URL | OpenCode HTTP/SSE。空 = endpoint not_configured，**不回落 64667**（详见 [docs/backends-and-config.md](docs/backends-and-config.md)） |
| `-management-host` | 产品态 `127.0.0.1` | Management API |
| `-management-port` | `0` | 随机 loopback 端口 |
| `-data-dir` | App Support 目录 | runtime/设备/TLS 状态 |
| `-log-dir` | data dir 下 `logs` | runtime 日志目录 |
| `-tls-port` | `8778` | Tailscale wss |
| `-remote-url` | 空 | 用户自定义 VPS/FRP/反向代理地址 |
| `-relay-enabled` | `true` | Relay 路径开关 |
| `-relay-service-addr` | 空 | 仅本地测试用的 in-process relay listener，必须是 loopback |
| `-pairing-include-tailscale` | 产品态设置值 | pairing QR 是否包含探测到的 Tailscale URL |
| `-pairing-include-remote` | 产品态设置值 | pairing QR 是否包含用户配置 remote URL |

`-tls-port` 当前由 go-bridge flag 默认值提供；产品态 `RuntimeManager` 不显式传该参数。

环境变量替代：

| 环境变量 | 对应配置 |
| --- | --- |
| `GO_BRIDGE_CODEX_BACKEND` | `-codex-backend` |
| `GO_BRIDGE_CODEX_APP_SERVER_URL` | `-codex-app-server-url` |
| `OPENCODE_BASE_URL` | `-opencode-url` |
| `OPENCODE_SERVER_USERNAME/PASSWORD` | OpenCode Basic Auth |
| `CCCODE_MANAGEMENT_TOKEN` | Management token |
| `CORDCODE_RELAY_ENDPOINT/ROUTE_ID/CREDENTIAL` | Relay 控制面配置 |
| `GO_BRIDGE_ADVERTISE_HOST` | 显式 LAN 广播 host |

`CCCODE_MANAGEMENT_TOKEN`、`CORDCODE_RELAY_*`、`OPENCODE_SERVER_*` 等控制面变量在解析后会从
runtime 环境清除，并由 `core.BuildAgentEnv` 阻止传入 agent/tool 子进程。Mac app 还会合并
常见 CLI 路径（如 `~/.bun/bin`、`~/.local/bin`、`/opt/homebrew/bin`、Codex.app 内嵌路径）
到 runtime PATH，避免 GUI 启动环境找不到已安装 backend CLI。

## 推荐构建与覆盖安装

本机开发按仓库约定使用 Release 脚本：

```bash
./scripts/build-unsigned-release.sh
```

脚本会：

1. 用 `MacBridge/CordCodeLink.xcodeproj` 构建 Release；
2. 由 Xcode pre-build step 编译并嵌入 Go runtime；
3. 校验 app/runtime 均可执行、架构一致、runtime 版本元数据正确；
4. 在 `dist/` 生成 zip 与 SHA-256。

开发机覆盖安装：

```bash
killall CordCodeLink 2>/dev/null || true
rm -rf /Applications/CordCodeLink.app
cp -R build/unsigned-release/Build/Products/Release/CordCodeLink.app /Applications/
open /Applications/CordCodeLink.app
```

不要把 Go binary 复制到 `Contents/MacOS/CordCodeLink`；那里必须是 Swift launcher。
修改 `go-bridge/` 或 `MacBridge/` 源码后，以上 Release 构建、覆盖安装和启动验证是交付
条件，不等待用户提醒。只运行 `go test` 或 Debug build 不代表本机已部署新版本。

## 运行态真值

数据目录：

```text
~/Library/Application Support/CordCode Link/
```

关键文件：

| 文件 | 含义 |
| --- | --- |
| `runtime.json` | runtime PID、Bridge 端口、Management URL、bridge epoch |
| `management-token` | Mac app 调 Management API 的本机 token，权限应为 `0600` |
| `credentials.json` | OpenCode 本地认证配置 |
| `opencode-managed-server.json` | CordCode 托管 OpenCode server 的 URL、端口、PID 与随机凭据，权限应为 `0600` |
| `devices.json` | 已授权设备 |
| `tls-cert.json` | Tailscale 自签名证书与 SPKI pin 的持久状态 |
| `logs/go-bridge.log` | runtime 主日志 |

`runtime.json` 和 `management-token` 写入失败会 fail-fast；当前实现不会用残缺文件制造
“已就绪”假象。

## 健康检查

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "CordCodeLink|cordcode-bridge-runtime|go-bridge"
cat "$HOME/Library/Application Support/CordCode Link/runtime.json"
tail -n 200 "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"
```

产品态监听进程应来自：

```text
/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime
```

验证 Management API：

```bash
DATA_DIR="$HOME/Library/Application Support/CordCode Link"
MGMT_URL="$(ruby -rjson -e 'print JSON.parse(File.read(ARGV[0]))["managementUrl"]' \
  "$DATA_DIR/runtime.json")"
TOKEN="$(cat "$DATA_DIR/management-token")"

curl -fsS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/status"
curl -fsS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/agents"
```

Bridge ready 与 backend available 是两个维度。Claude 未安装、OpenCode 未启动或 Codex
未登录时，应在 `/internal/agents` 暴露真实状态，不能把整个 runtime 判成崩溃。

## Codex app-server 的启动归属

产品态 Bridge 默认不连接共享 `4141`，而是在每个 Codex app-server session 中通过 stdio
启动 `codex app-server`。这避免依赖一个外部常驻 TCP listener。若用户显式配置
`-codex-app-server-url ws://127.0.0.1:4141`，MacBridge supervisor 仍只管理
`cordcode-bridge-runtime`，不负责创建或常驻这个外部服务；此时开发机需要由 Codex app/CLI
提供共享 service。手工验证可在独立终端运行：

```bash
codex app-server --listen ws://127.0.0.1:4141
```

然后用 `lsof -nP -iTCP:4141 -sTCP:LISTEN` 和 Bridge 日志确认 initialize/passive
subscription 成功。命令参数应以本机 `codex app-server --help` 为准。

若确实需要登录后常驻，可使用机器本地的 LaunchAgent，但 plist 不属于仓库配置：

- 通过 `command -v codex` 取得本机路径，不提交个人绝对路径；
- 不在 plist 或文档中提交 token、账号、HOME 路径；
- 只允许一个共享 `4141` listener，避免多个 app-server 竞争；
- MacBridge Restart 不会重启该服务，需分别检查其进程和日志。

## 日志与重启

```bash
tail -f "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log" \
  | tee /tmp/cordcode-macbridge.log
```

日志超过 8 MiB 会滚动，保留 3 代。默认启用 120 分钟周期性 runtime 重启，因此日志从
某个时间点重新开始不一定是崩溃；也可能是 `.starting` 卡住 60 秒后 supervisor 自愈重启。
自动重启有 `autoRestartEnabled` 开关，周期下限为 5 分钟且可运行时调整；排查长任务时可在
设置中临时关闭自动重启。

菜单中的 Restart 只重启内嵌 runtime：

- 正在运行的 Claude/Codex CLI session 会被 runtime shutdown 回收；
- 外部 OpenCode Desktop 与共享 Codex app-server 不会被重启；
- iOS transport 应断线并自动重连，随后用新的 `hello_ack` 重建 backend client。

sleep/wake 后 MacBridge 会等待约 2 秒再重启 runtime，并重置 crash counter。连续意外退出超过
`maxCrashRetries=3` 或 `.starting` 卡住连续 `maxStuckRestarts=5` 后，会停止自愈并把状态判为
`.crashed`，保留真实失败现场。

## 常见故障

### backend 显示 CLI not found / no agents available

先区分“runtime 没启动”和“单个 backend 不可用”：

```bash
command -v claude
command -v codex
command -v opencode
curl -fsS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/agents"
```

Mac app 启动的子进程 PATH 不一定和交互式 shell 完全相同。若 shell 能找到 CLI 而
`/internal/agents` 找不到，检查 `RuntimeManager` 构造的环境和完整 runtime 日志，不要新建
第二个 LaunchAgent 绕过 supervisor。

只有所有 driver 创建都失败时 runtime 才会报 `runtime_error.no_agents` 并退出；单个
backend 未安装或未登录应保持 Bridge ready，同时在 agent descriptor 中返回真实原因。

### OpenCode 401 / not_configured / server_unauthenticated

OpenCode 不再隐式硬编码 `64667`。新装默认 **Automatic / managed_local**：
CordCode Link 会启动一个只绑定 `127.0.0.1` 的 `opencode serve`，选择并持久化
`4096...4196` 范围内的端口，使用 Basic Auth，随后把 OpenCode Desktop 与 go-bridge 都指向
同一个 URL。先在 MacBridge 设置中确认 **Server Source**（Automatic / External HTTP /
Legacy 64667 / Disabled），再按症状排查：

```bash
# 确认 server 在监听（Automatic 下端口见 opencode-managed-server.json）
lsof -nP -iTCP:<port> -sTCP:LISTEN
cat "$HOME/Library/Application Support/CordCode Link/credentials.json"
cat "$HOME/Library/Application Support/CordCode Link/opencode-managed-server.json"
```

- descriptor `not_configured`：Automatic 下表示 managed server 未能解析/启动；External HTTP 下表示未配置 endpoint URL。External HTTP 下填一个
  loopback URL（`http://127.0.0.1:<port>`）并保存重启。
- `401`（auth_failed）：server 已响应但凭据不匹配。在设置中重新保存 username/password，
  并确保 `opencode serve` 用同一个 `OPENCODE_SERVER_PASSWORD` 启动，两端一致后重启。
- `server_unauthenticated`：no-auth `/global/health` 返回 `200`，说明 server 未启用
  Basic Auth（OpenCode 日志会出现 `OPENCODE_SERVER_PASSWORD is not set; server is unsecured`）。
  必须设置 `OPENCODE_SERVER_PASSWORD` 后重启 server；CordCode 拒绝无密码 endpoint
  （`legacy_64667` 例外，但会标 `legacy_insecure_unverified`）。
- `unreachable`：连接失败/超时。Automatic 下查 `logs/opencode-managed-server.err.log`；External HTTP 下确认 `opencode serve --hostname 127.0.0.1 --port <port>` 在运行。

Automatic 下 CordCode 会保活 managed child process；External HTTP 下 CordCode 只连接用户提供的 server，不启动也不保活它。不要把密码打印进 issue、日志或文档。

### Codex app-server 没有实时事件

```bash
lsof -nP -iTCP:4141 -sTCP:LISTEN
ps aux | grep '[c]odex app-server'
grep -E 'codex|passive subscription' \
  "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log" | tail -100
```

确认产品 runtime 使用 `app_server` 且 URL 为预期的 `4141`。端口存在但 initialize 失败时，
继续查 Codex 登录状态、版本和 JSON-RPC 握手；不要用 iOS 高频 polling 掩盖 passive
subscriber 缺失。

### iOS 无法添加或连接 Bridge

依次核对：

1. `8777` 是否由产品 runtime 监听；
2. Mac 与 iPhone 是否在同一可信局域网，或是否已有 Relay/安全自定义远程候选；
3. pairing 是否已在 Mac 端批准；
4. device token 是否被撤销；
5. iOS 与 Mac 两侧日志是否出现同一 pairing/session 标识；
6. `hello.protocol.version` 是否兼容。

普通 HTTP 请求 `/bridge` 不是完整连接测试；未带 token 返回认证错误只证明请求到达 auth
gate。真正可用性必须由 WebSocket upgrade、hello/hello_ack 和已认证 RPC 验证。

### iOS 能连，但 MacBridge 显示 runtime 连续退出

优先检查 `8777` 是否被旧开发进程占用：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "go-bridge|cordcode-bridge-runtime|CordCodeLink"
```

启动前 MacBridge 会自动禁用旧的 go-bridge LaunchAgent，并只会接管自家 runtime 占用的
`8777`（`cordcode-bridge-runtime` 或仓库开发态 `/go-bridge/go-bridge`）。若占用者不是自家
二进制，supervisor 会直接判定启动失败，不会误杀未知进程。

停止旧进程后删除残留 ready 文件并重启：

```bash
pkill -f "/opencode-cc-connect/go-bridge/" 2>/dev/null || true
pkill -f "/cordcode-macbridge/go-bridge/" 2>/dev/null || true
killall CordCodeLink 2>/dev/null || true
rm -f "$HOME/Library/Application Support/CordCode Link/runtime.json"
open /Applications/CordCodeLink.app
```

### runtime ready，但 Management API 不通

先核对 `runtime.json` 的 PID 是否仍存在、bridge epoch 是否来自本次启动，再看日志中的
`runtime.management_*` 或 `runtime.bootstrap_persist_failed`。不要手工伪造
`runtime.json` 或 token。

### 只调试 Go runtime

先关闭 MacBridge 释放端口，再前台运行：

```bash
killall CordCodeLink 2>/dev/null || true
go run ./go-bridge -port 8777 -drivers claude,opencode,codex
```

前台模式没有 Mac app supervisor 的自动重启、配置注入与 Management UI，只用于定向调试。

常用定向验证：

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1
go test ./go-bridge/... -run '<TestName>' -count=1
(cd relay-server && go test ./... -count=1)
```
