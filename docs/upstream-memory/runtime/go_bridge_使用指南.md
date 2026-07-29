# go-bridge 使用指南

iOS app 通过 **go-bridge** 统一连接 Mac 上的 Claude Code、OpenCode、Codex 后端。go-bridge 是一个 Go WebSocket 服务，默认监听 `8777`，只做 wire protocol 适配；真实 runtime 逻辑在 `/Users/jacklee/Projects/cc-connect` 的 agent/core 层实现。

本文档目标：以后 agent 需要编译、启动、重启、排查 go-bridge 时，不需要再去代码里捞启动逻辑。

## 架构

```
iOS App  ←── WebSocket ──→  go-bridge (Go, port 8777)  ←──→  cc-connect agents
           ws://Mac-IP:8777                              Claude / OpenCode / Codex
```

- iOS 不直连 Claude、OpenCode、Codex，统一连 `ws://<Mac-IP>:8777`
- go-bridge 运行在 Mac 上，推荐用 `launchctl submit` 常驻
- go-bridge 通过 `go-bridge/go.mod` 的 `replace` 引用本地 `/Users/jacklee/Projects/cc-connect`
- 修改 cc-connect 后，必须重新编译 `go-bridge/go-bridge` 并重启 go-bridge 进程

## 快速开始

### 1. 编译

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
go build -o go-bridge .
```

编译产物：

```bash
/Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge
```

确认二进制更新时间：

```bash
stat -f '%Sm %N' -t '%Y-%m-%d %H:%M:%S %z' \
  /Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge
```

### 2. 前台调试启动

前台调试适合临时验证，退出终端后服务停止。

**前提：启动 Codex standalone app-server**（enable streaming delta）：

```bash
/opt/homebrew/bin/codex app-server --listen ws://127.0.0.1:4141 > /tmp/codex-app-server.log 2>&1 &
# 确认监听
lsof -nP -iTCP:4141 -sTCP:LISTEN
```

启动 go-bridge：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
./go-bridge -port 8777 -drivers claude,opencode,codex \
  -work-dir /Users/jacklee/Projects/opencode-cc-connect \
  -codex-backend app_server \
  -codex-app-server-url ws://localhost:4141
```

旧模式（无 Codex streaming，不推荐）：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
./go-bridge -port 8777 -drivers claude,opencode,codex \
  -work-dir /Users/jacklee/Projects/opencode-cc-connect
```

启动成功应看到类似日志：

```text
time=... level=INFO msg="go-bridge: agent registered" backendId=claude agent=claudecode
time=... level=INFO msg="go-bridge: agent registered" backendId=opencode agent=opencode
time=... level=INFO msg="go-bridge: agent registered" backendId=codex agent=codex
time=... level=INFO msg="go-bridge: listening" addr=:8777 drivers=claude,opencode,codex
```

### 3. CCCodeBridge App 托管常驻与自动配对（推荐）

在生产或日常真机联调中，**不推荐**手动配置 LaunchAgent plist 或使用 `launchctl submit` 来运行 `go-bridge`。

现在，项目由菜单栏应用 **`CCCodeBridge.app`** 统一托管。
- **生命周期管理**：`CCCodeBridge.app` 启动时，其 `RuntimeManager` 服务会自动探测并终止任何占用 `8777` 端口的冲突进程，并强行卸载/注销旧的 `com.opencode.gobridge` 临时/常驻服务。
- **启动参数与 PATH 注入**：`CCCodeBridge` 会通过 Swift 的 `Process` API 运行内置的 Go Runtime（产品包里名为 `cccode-bridge-runtime`），并自动合并当前系统的 `PATH` 环境变量，传入正确的 `-work-dir` 和 `-drivers` 启动参数。
- **自动配对免配机制 (OpenCode Credentials Sync)**：
  - 在首次启动或发现凭据为空时，`CCCodeBridge` 会自动在本地生成一组随机 Basic Auth 凭据（用户名 `opencode`，密码为随机 UUID），写入其专有的 `credentials.json`（位于 `~/Library/Application Support/CCCode Bridge/` 目录，权限为 0600）。
  - 同时，App 会将这组生成的凭据**自动同步写入** OpenCode Desktop 的配置文件目录：`~/Library/Application Support/ai.opencode.desktop/opencode.settings` 以及 `opencode.global.dat`。
  - 这样，当 OpenCode Desktop 启动它的 HTTP 服务时，会直接强制要求这组凭据进行 Basic Auth。而 CCCodeBridge 在启动内置 go-bridge 进程时，会自动读取 `credentials.json` 并通过环境变量/命令行参数注入给 go-bridge。
  - 用户和开发者**无需手动复制和配置任何 OpenCode 密码**。

#### 如何安装与运行产品态 Bridge

1. 按照 `MacBridge编译安装指南.md` 编译并安装 `CCCodeBridge.app` 到 `/Applications/CCCodeBridge.app`。
2. 打开 App，它将自动在 macOS 菜单栏常驻。
3. 点击菜单栏图标，即可在 UI 面板上查看 runtime 状态，或者直接点击 **Restart** 触发 Go 运行时重启。
4. 运行日志将输出在默认路径 `/tmp/go-bridge.log` 中。

## 管理命令

| 操作 | 命令 / 方式 |
|------|------|
| 重启 Bridge (产品态) | 点击菜单栏 CCCode Bridge 图标 → 选择 **Restart** |
| 启动 Host App (产品态) | `open /Applications/CCCodeBridge.app` |
| 关闭 Host App (产品态) | `killall CCCodeBridge 2>/dev/null \|\| true` |
| 编译并覆盖内置 Go 运行时 | `cd go-bridge && go build -o /Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime .` |
| 强制释放 8777 端口 | `pkill -f cccode-bridge-runtime 2>/dev/null \|\| true; lsof -tiTCP:8777 -sTCP:LISTEN \| xargs kill 2>/dev/null \|\| true` |
| 查看 8777 端口占用 | `lsof -nP -iTCP:8777 -sTCP:LISTEN` |
| 查看运行时进程命令 | `ps -o pid,ppid,lstart,command -p $(lsof -tiTCP:8777 -sTCP:LISTEN)` |
| 查看日志 | `tail -f /tmp/go-bridge.log` |
| HTTP 探活 | `curl -sS --max-time 2 http://127.0.0.1:8777/` （预期返回 `Bad Request` 说明服务在运行） |
```

## iOS 端连接

在 iOS app Settings 中选择 **Add Bridge Manually**。

| 字段 | 值 |
|------|----|
| Host | Mac 局域网 IP，可用 `ipconfig getifaddr en0` 查看 |
| Port | `8777` |
| Username / Password | go-bridge 默认不需要 |

成功后 iOS 应发现多个 backend target：

```text
MacBook Bridge                    192.168.1.100:8777 / jack / 3 backend target(s)
  ├── Claude Code
  ├── OpenCode
  └── Codex
```

go-bridge 重启后，如果 iOS 端 backend target 没刷新，删除旧 Bridge 后重新 Add Bridge。

## 启动参数

| Flag | 默认值 | 说明 |
|------|--------|------|
| `-port` | `8777` | WebSocket 监听端口 |
| `-drivers` | `claude,opencode,codex` | 启用的 backend，逗号分隔 |
| `-work-dir` | 当前目录 | agent 工作目录 |
| `-codex-backend` | `exec` | Codex 模式：`exec` 或 `app_server` |
| `-codex-app-server-url` | 空 | Codex app-server URL，例如 `ws://localhost:4141` |
| `-opencode-url` | `http://localhost:64667` | OpenCode HTTP API 地址 |
| `-opencode-user` | 空 | OpenCode Basic auth 用户名 |
| `-opencode-pass` | 空 | OpenCode Basic auth 密码 |

对应环境变量：

| 环境变量 | 对应 Flag |
|----------|-----------|
| `GO_BRIDGE_CODEX_BACKEND` | `-codex-backend` |
| `GO_BRIDGE_CODEX_APP_SERVER_URL` | `-codex-app-server-url` |
| `OPENCODE_BASE_URL` | `-opencode-url` |
| `OPENCODE_SERVER_USERNAME` | `-opencode-user` |
| `OPENCODE_SERVER_PASSWORD` | `-opencode-pass` |

## 后端依赖

| Backend | 依赖 | 说明 |
|---------|------|------|
| Claude Code | `claude` CLI 在 PATH | 独立 CLI 进程模型，无服务端广播 |
| OpenCode | `opencode` CLI 在 PATH；HTTP proxy 默认连 `http://localhost:64667` | go-bridge 注册 agent，同时注册 OpenCode HTTP proxy |
| Codex (推荐) | `codex` CLI 在 PATH；standalone app-server `ws://localhost:4141` | app-server 模式：实时 streaming delta + 被动订阅广播 |
| Codex exec (旧) | `codex` CLI 在 PATH | 无 streaming，不推荐 |

确认 CLI 是否在当前 shell PATH：

```bash
command -v claude
command -v opencode
command -v codex
```

如果当前 shell 能找到，但 launchctl 启动找不到，按本文的 `/bin/zsh -lc 'export PATH=...; exec ...'` 方式启动。

### Codex app-server LaunchAgent 常驻

Codex streaming delta 需要一个 standalone app-server 监听 `ws://127.0.0.1:4141`。可用 LaunchAgent 常驻：

创建 `~/Library/LaunchAgents/com.codex.app-server.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.codex.app-server</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/bin/codex</string>
        <string>app-server</string>
        <string>--listen</string>
        <string>ws://127.0.0.1:4141</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/codex-app-server.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/codex-app-server.log</string>
    <key>WorkingDirectory</key>
    <string>/Users/jacklee/Projects/opencode-cc-connect</string>
</dict>
</plist>
```

安装并启动：

```bash
launchctl load ~/Library/LaunchAgents/com.codex.app-server.plist
lsof -nP -iTCP:4141 -sTCP:LISTEN
```

管理命令：

| 操作 | 命令 |
|------|------|
| 查看状态 | `launchctl list | grep codex.app-server` |
| 停止 | `launchctl unload ~/Library/LaunchAgents/com.codex.app-server.plist` |
| 重启 | `launchctl unload ~/Library/LaunchAgents/com.codex.app-server.plist && launchctl load ~/Library/LaunchAgents/com.codex.app-server.plist` |
| 查看日志 | `tail -f /tmp/codex-app-server.log` |

## 常见故障

### 端口 8777 没有监听

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
launchctl list | rg 'com\.opencode\.gobridge|PID|Status'
tail -80 /tmp/go-bridge.log
```

如果 `launchctl list` 中 `com.opencode.gobridge` 的 PID 是 `-`，说明任务已退出。继续看 `/tmp/go-bridge.log`。

### 日志显示 CLI not found

原因：launchctl 默认 PATH 找不到 `claude`、`opencode`、`codex`。

修复：确认 `~/Library/LaunchAgents/com.opencode.gobridge.plist` 中 PATH 包含所需目录，然后 `launchctl unload && launchctl load` 重启。

### 日志显示 no agents available, exiting

原因：所有 driver 都创建失败。最常见还是 PATH 问题。

排查：

```bash
tail -80 /tmp/go-bridge.log
command -v claude opencode codex
```

修复：确认 CLI 存在后，用 plist 方式重启。

### 编译后 iOS 行为没变

原因：新二进制已编译，但旧进程还在监听 `8777`。

修复：必须重启进程并确认 PID 启动时间晚于二进制更新时间。

```bash
stat -f '%Sm %N' -t '%Y-%m-%d %H:%M:%S %z' \
  /Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge
lsof -nP -iTCP:8777 -sTCP:LISTEN
ps -o pid,ppid,lstart,command -p $(lsof -tiTCP:8777 -sTCP:LISTEN)
```

### iOS Add Bridge 失败

1. 确认 Mac 和 iPhone 在同一局域网
2. 确认 iOS 填的是 Mac 局域网 IP，不是 `localhost`
3. 确认端口是 `8777`
4. 确认 Mac 上 `lsof -nP -iTCP:8777 -sTCP:LISTEN` 有 go-bridge
5. 查看 `/tmp/go-bridge.log`

### OpenCode 相关读路径异常 / 401

go-bridge 的 OpenCode HTTP proxy 默认连接：

```text
http://localhost:64667
```

OpenCode HTTP API 需要 Basic auth 认证。如果 iOS 端连接 OpenCode 报 401，说明 go-bridge 与 OpenCode Desktop 之间的认证凭据（username/password）未同步成功。

排查与恢复：

1. **确认 OpenCode Server 在运行**：
   ```bash
   lsof -nP -iTCP:64667 -sTCP:LISTEN
   ```
2. **确认 go-bridge 进程已注入认证参数**：
   ```bash
   ps -o command -p $(lsof -tiTCP:8777 -sTCP:LISTEN)
   ```
   检查进程的启动参数中是否包含 `-opencode-user` 与 `-opencode-pass`。
3. **核对 CCCodeBridge 持久化凭据**：
   查看 CCCodeBridge 自动生成的密码：
   ```bash
   cat "$HOME/Library/Application Support/CCCode Bridge/credentials.json"
   ```
4. **核对 OpenCode Desktop 配置中写入的凭据**：
   ```bash
   # 查看 OpenCode 记录的当前服务连接信息
   cat "$HOME/Library/Application Support/ai.opencode.desktop/opencode.settings"
   ```
   如果两边记录的密码不一致，在 CCCodeBridge UI 的 **Settings** (设置) 面板中，检查配置凭据；或者直接重启 `CCCodeBridge` 让其自动再次写入同步。

### Codex app-server 模式没有实时事件

确认 go-bridge 是用 app-server 参数启动的：

```bash
ps -o command -p $(lsof -tiTCP:8777 -sTCP:LISTEN)
```

命令里应该包含：

```text
-codex-backend app_server -codex-app-server-url ws://localhost:4141
```

同时确认 Codex app-server 在 `4141`：

```bash
lsof -nP -iTCP:4141 -sTCP:LISTEN
```

## 测试与验证

go-bridge 自身测试：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
go test ./... -count=1
```

cc-connect Codex agent 定向测试示例：

```bash
cd /Users/jacklee/Projects/cc-connect
go test ./agent/codex -count=1
```

如果 `cc-connect/agent/codex` 包测试因既有接口断言阻塞，应记录具体编译错误，不要把失败伪装成通过。

启动后最小验证：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
tail -20 /tmp/go-bridge.log
curl -sS --max-time 2 http://127.0.0.1:8777/ 2>&1
```

`curl` 返回 `Bad Request` 是正常的：go-bridge 根路径期望 WebSocket upgrade，不是普通 HTTP API。

## 相关文件

| 文件 | 作用 |
|------|------|
| `go-bridge/main.go` | 启动入口、flag 解析、agent 注册 |
| `go-bridge/server.go` | WebSocket server |
| `go-bridge/handlers.go` | RPC 路由、session 管理、能力暴露 |
| `go-bridge/events.go` | `core.Event` 到 iOS wire event 的映射 |
| `go-bridge/types.go` | wire protocol 类型 |
| `go-bridge/provider_switch.go` | Codex provider 配置加载/切换 |
| `go-bridge/opencode-proxy.go` | OpenCode HTTP proxy |
| `go-bridge/go.mod` | Go module，含 `/Users/jacklee/Projects/cc-connect` replace |
| `/Users/jacklee/Projects/cc-connect/agent/*` | 各 backend runtime 实现 |
| `/Users/jacklee/Projects/cc-connect/core/*` | core interfaces、engine、message/event 类型 |
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/UnifiedBridgeAdapter.swift` | iOS bridge adapter |
| `OpenCodeiOS/OpenCodeiOS/Services/Backend/UnifiedBridgeTransport.swift` | iOS WebSocket transport |


## 外网访问（阿里云 VPS + frp 穿透）

iPhone 在外网时，通过阿里云 VPS 反向代理连接 Mac 上的 go-bridge。

### 架构

```
iPhone (外网)
  ↓ WSS (WebSocket Secure)
阿里云 VPS (47.236.182.45:9090)
  ↓ Nginx SSL 反向代理 → 127.0.0.1:8777
  ↓ frp TCP 映射 (VPS 8777 → Mac 8777)
MacBook (家里局域网)
  ↓
go-bridge (127.0.0.1:8777)
```

### 端口规划

| 位置 | 端口 | 用途 | 协议 |
|------|------|------|------|
| VPS | 7000 | frps 服务端口 | TCP |
| VPS | 9090 | Nginx WSS 入口（对外） | HTTPS/WSS |
| VPS | 8777 | frp 映射端口（内部） | TCP |
| Mac | 8777 | go-bridge WebSocket | WS |

### Mac 端 — frpc 配置

文件：`~/Projects/LaunchAgentTask/frpc/frpc.ini`

```ini
[common]
server_addr = 47.236.182.45
server_port = 7000
token = opencode-secret-2026

[opencode]
type = tcp
local_ip = 127.0.0.1
local_port = 8777
remote_port = 8777

[smb]
type = tcp
local_ip = 172.16.10.211
local_port = 445
remote_port = 445
```

frpc 通过 LaunchAgent 常驻：

```bash
# 查看 frpc 状态
ps aux | grep frpc | grep -v grep
cat ~/Projects/LaunchAgentTask/frpc/frpc.log | tail -10

# 重启 frpc
launchctl unload ~/Library/LaunchAgents/frpc.plist
launchctl load ~/Library/LaunchAgents/frpc.plist
```

### VPS 端 — Nginx + frps

**Nginx 配置**：`/etc/nginx/sites-available/opencode`

```nginx
server {
    listen 9090 ssl;
    server_name _;

    ssl_certificate /etc/ssl/certs/opencode-selfsigned.crt;
    ssl_certificate_key /etc/ssl/private/opencode-selfsigned.key;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location / {
        proxy_pass http://127.0.0.1:8777;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400;
        proxy_send_timeout 86400;
        chunked_transfer_encoding on;
    }
}
```

修改后 reload：

```bash
ssh root@47.236.182.45  # 密码见实践文档
nginx -t && systemctl reload nginx
```

**frps**：`/opt/frp/frps.ini`，bind_port 7000，无需特殊配置。

### iOS 端连接

iOS app Settings → Add Bridge Manually：

| 场景 | Host | Port | HTTPS |
|------|------|------|-------|
| 外网 | `47.236.182.45` | `9090` | **必须开启** |
| 局域网 | Mac 局域网 IP（如 `172.16.10.211`） | `8777` | 关闭 |

> **外网必须开启 HTTPS。** VPS Nginx 9090 只监听 SSL，plain HTTP 会被直接拒绝。如果 iOS 端 HTTPS 没开，连接会超时无响应。go-bridge 重启后如果 backend target 列表没刷新，删掉旧 Bridge 条目重新 Add Bridge 即可。

外网模式使用自签名 SSL 证书，iOS 端通过 `InsecureURLSessionDelegate` 信任。`Info.plist` 已配置 `NSAllowsArbitraryLoads: true` 和 `47.236.182.45` 域名例外。

### 验证链路

```bash
# 1. Mac 本地 — go-bridge 在监听
lsof -nP -iTCP:8777 -sTCP:LISTEN

# 2. Mac 本地 — HTTP 探活（返回 Bad Request 是正常的）
curl -sS --max-time 2 http://127.0.0.1:8777/

# 3. VPS 内部 — frp 隧道通
ssh root@47.236.182.45 "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8777/"
# 预期: 400

# 4. 外网 — Nginx WSS 入口
curl -k -s -o /dev/null -w '%{http_code}' https://47.236.182.45:9090/
# 预期: 400

# 5. 外网 — WebSocket 升级
curl -k -v --max-time 5 \
  -H "Upgrade: websocket" \
  -H "Connection: Upgrade" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" \
  https://47.236.182.45:9090/
# 预期: 101 Switching Protocols

# 6. Mac — frpc 日志确认 proxy 注册
tail -10 ~/Projects/LaunchAgentTask/frpc/frpc.log
# 预期: [opencode] start proxy success
```

### 常见故障

**外网连接 400/502**

1. 确认 go-bridge 在 Mac 上运行：`lsof -nP -iTCP:8777 -sTCP:LISTEN`
2. 确认 frpc 在 Mac 上运行：`ps aux | grep frpc | grep -v grep`
3. 确认 frps 在 VPS 上运行：`ssh root@47.236.182.45 "systemctl status frps"`

**外网连接 SSL 错误**

自签名证书过期，需要重新生成：

```bash
ssh root@47.236.182.45
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/ssl/private/opencode-selfsigned.key \
  -out /etc/ssl/certs/opencode-selfsigned.crt \
  -subj "/C=CN/ST=Beijing/L=Beijing/O=OpenCode/CN=47.236.182.45"
systemctl reload nginx
```

**frp token 不匹配**

确认 `~/Projects/LaunchAgentTask/frpc/frpc.ini` 的 token 与 VPS `/opt/frp/frps.ini` 一致。

**iOS 外网连不上但 Mac curl 正常**

检查 `Info.plist` 中 `NSAppTransportSecurity` 是否包含 `NSAllowsArbitraryLoads: true` 和 `47.236.182.45` 域名例外。同时确认 `InsecureURLSessionDelegate.swift` 存在且所有 `URLSession` 创建处都传入了该 delegate。
