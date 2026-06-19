# MacBridge 构建、安装与运行态排查

> 本文从原一体仓库 `opencode-cc-connect/MacBridge编译安装指南.md` 与
> `本机服务常驻与端口约定.md` 迁移，并按拆分后的 `cccode-macbridge` 当前实现校正。

## 组件与所有权

`CCCodeBridge.app` 由两部分组成：

| 组件 | 位置 | 职责 |
| --- | --- | --- |
| SwiftUI macOS app | `MacBridge/` | 设置、配对、设备管理、runtime 生命周期 |
| Go runtime | `go-bridge/` | Bridge WebSocket、agent 适配、Management API、Relay |

Release 构建会把 Go runtime 编译为
`CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime`。产品运行态中，
`CCCodeBridge.app` 是 runtime 的唯一 supervisor；不要再为 `go-bridge` 安装独立
LaunchAgent，也不要让仓库里的开发二进制长期占用 `8777`。

## 端口与外部依赖

| 服务 | 默认端口 | 所有者 | iOS 是否直连 |
| --- | ---: | --- | --- |
| Bridge WebSocket | `8777` | `cccode-bridge-runtime` | 是，局域网 `ws://` |
| Bridge TLS | `8778` | `cccode-bridge-runtime` | 是，仅 Tailscale `wss://` |
| Management API | 随机 loopback 端口 | runtime，Mac app 调用 | 否 |
| OpenCode HTTP/SSE | `64667` | OpenCode Desktop | 否 |
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
| `-codex-app-server-url` | 产品态 `ws://127.0.0.1:4141` | 共享 Codex service |
| `-opencode-url` | `http://localhost:64667` | OpenCode HTTP/SSE |
| `-management-host` | 产品态 `127.0.0.1` | Management API |
| `-management-port` | `0` | 随机 loopback 端口 |
| `-data-dir` | App Support 目录 | runtime/设备/TLS 状态 |
| `-log-dir` | data dir 下 `logs` | runtime 日志目录 |
| `-tls-port` | `8778` | Tailscale wss |
| `-remote-url` | 空 | 用户自定义 VPS/FRP/反向代理地址 |
| `-relay-enabled` | `true` | Relay 路径开关 |

环境变量替代：

| 环境变量 | 对应配置 |
| --- | --- |
| `GO_BRIDGE_CODEX_BACKEND` | `-codex-backend` |
| `GO_BRIDGE_CODEX_APP_SERVER_URL` | `-codex-app-server-url` |
| `OPENCODE_BASE_URL` | `-opencode-url` |
| `OPENCODE_SERVER_USERNAME/PASSWORD` | OpenCode Basic Auth |
| `CCCODE_MANAGEMENT_TOKEN` | Management token |
| `CCCODE_RELAY_ENDPOINT/ROUTE_ID/CREDENTIAL` | Relay 控制面配置 |
| `GO_BRIDGE_ADVERTISE_HOST` | 显式 LAN 广播 host |

`CCCODE_*`、`OPENCODE_SERVER_*` 等控制面变量在解析后会从 runtime 环境清除，并由
`core.BuildAgentEnv` 阻止传入 agent/tool 子进程。

## 推荐构建与覆盖安装

本机开发按仓库约定使用 Release 脚本：

```bash
./scripts/build-unsigned-release.sh
```

脚本会：

1. 用 `MacBridge/CCCodeBridge.xcodeproj` 构建 Release；
2. 由 Xcode pre-build step 编译并嵌入 Go runtime；
3. 校验 app/runtime 均可执行、架构一致、runtime 版本元数据正确；
4. 在 `dist/` 生成 zip 与 SHA-256。

开发机覆盖安装：

```bash
killall CCCodeBridge 2>/dev/null || true
rm -rf /Applications/CCCodeBridge.app
cp -R build/unsigned-release/Build/Products/Release/CCCodeBridge.app /Applications/
open /Applications/CCCodeBridge.app
```

不要把 Go binary 复制到 `Contents/MacOS/CCCodeBridge`；那里必须是 Swift launcher。
修改 `go-bridge/` 或 `MacBridge/` 源码后，以上 Release 构建、覆盖安装和启动验证是交付
条件，不等待用户提醒。只运行 `go test` 或 Debug build 不代表本机已部署新版本。

## 运行态真值

数据目录：

```text
~/Library/Application Support/CCCode Bridge/
```

关键文件：

| 文件 | 含义 |
| --- | --- |
| `runtime.json` | runtime PID、Bridge 端口、Management URL、bridge epoch |
| `management-token` | Mac app 调 Management API 的本机 token，权限应为 `0600` |
| `credentials.json` | OpenCode 本地认证配置 |
| `devices.json` | 已授权设备 |
| `tls-cert.json` | Tailscale 自签名证书与 SPKI pin 的持久状态 |
| `logs/go-bridge.log` | runtime 主日志 |

`runtime.json` 和 `management-token` 写入失败会 fail-fast；当前实现不会用残缺文件制造
“已就绪”假象。

## 健康检查

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "CCCodeBridge|cccode-bridge-runtime|go-bridge"
cat "$HOME/Library/Application Support/CCCode Bridge/runtime.json"
tail -n 200 "$HOME/Library/Application Support/CCCode Bridge/logs/go-bridge.log"
```

产品态监听进程应来自：

```text
/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime
```

验证 Management API：

```bash
DATA_DIR="$HOME/Library/Application Support/CCCode Bridge"
MGMT_URL="$(ruby -rjson -e 'print JSON.parse(File.read(ARGV[0]))["managementUrl"]' \
  "$DATA_DIR/runtime.json")"
TOKEN="$(cat "$DATA_DIR/management-token")"

curl -fsS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/status"
curl -fsS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/agents"
```

Bridge ready 与 backend available 是两个维度。Claude 未安装、OpenCode 未启动或 Codex
未登录时，应在 `/internal/agents` 暴露真实状态，不能把整个 runtime 判成崩溃。

## Codex app-server 的启动归属

产品态 Bridge 默认连接共享的 `ws://127.0.0.1:4141`，但 MacBridge supervisor 只管理
`cccode-bridge-runtime`，不负责创建或常驻这个外部服务。开发机需要由 Codex app/CLI
提供它；手工验证可在独立终端运行：

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
tail -f "$HOME/Library/Application Support/CCCode Bridge/logs/go-bridge.log" \
  | tee /tmp/cccode-macbridge.log
```

日志超过 8 MiB 会滚动，保留 3 代。默认启用 120 分钟周期性 runtime 重启，因此日志从
某个时间点重新开始不一定是崩溃；排查长任务时可在设置中临时关闭自动重启。

菜单中的 Restart 只重启内嵌 runtime：

- 正在运行的 Claude/Codex CLI session 会被 runtime shutdown 回收；
- 外部 OpenCode Desktop 与共享 Codex app-server 不会被重启；
- iOS transport 应断线并自动重连，随后用新的 `hello_ack` 重建 backend client。

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

### OpenCode 返回 401

```bash
lsof -nP -iTCP:64667 -sTCP:LISTEN
cat "$HOME/Library/Application Support/CCCode Bridge/credentials.json"
```

`401` 说明 OpenCode server 已响应，但运行中的 server 凭据与 MacBridge 持久值不一致。
在 MacBridge 设置中重新保存凭据并重启 OpenCode server/MacBridge，使两端一致。不要把
密码打印进 issue、日志或文档。

### Codex app-server 没有实时事件

```bash
lsof -nP -iTCP:4141 -sTCP:LISTEN
ps aux | grep '[c]odex app-server'
grep -E 'codex|passive subscription' \
  "$HOME/Library/Application Support/CCCode Bridge/logs/go-bridge.log" | tail -100
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
pgrep -fl "go-bridge|cccode-bridge-runtime|CCCodeBridge"
```

停止旧进程后删除残留 ready 文件并重启：

```bash
pkill -f "/opencode-cc-connect/go-bridge/" 2>/dev/null || true
pkill -f "/cccode-macbridge/go-bridge/" 2>/dev/null || true
killall CCCodeBridge 2>/dev/null || true
rm -f "$HOME/Library/Application Support/CCCode Bridge/runtime.json"
open /Applications/CCCodeBridge.app
```

### runtime ready，但 Management API 不通

先核对 `runtime.json` 的 PID 是否仍存在、bridge epoch 是否来自本次启动，再看日志中的
`runtime.management_*` 或 `runtime.bootstrap_persist_failed`。不要手工伪造
`runtime.json` 或 token。

### 只调试 Go runtime

先关闭 MacBridge 释放端口，再前台运行：

```bash
killall CCCodeBridge 2>/dev/null || true
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
