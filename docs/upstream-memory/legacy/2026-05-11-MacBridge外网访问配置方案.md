# MacBridge 配置阿里云 VPS 实现外网访问 Mac — 实践方案

> 日期: 2026-05-11 | 适配 MacBridge 原生 Mac App 架构
> 基于: `opencode-ios配置阿里云vps实现外网访问Macbook-opencode实践.md` v2.0

---

## 一、需求背景

### 1.1 场景

- Mac 在家里运行 MacBridge App（内含 go-bridge），监听 `ws://局域网IP:8777`
- iPhone 在同一 WiFi 下通过 `ws://IP:8777` 直连正常
- 外出时 iPhone 需要通过公网连接 Mac

### 1.2 解决方案

复用现有阿里云 VPS（47.236.182.45）的 frp + Nginx 隧道，暴露 go-bridge WebSocket 端口：

```
iPhone (外网)
  ↓ WSS (WebSocket Secure)
阿里云 VPS (47.236.182.45:9090)
  ↓ Nginx SSL 终结 + frp 隧道
MacBook (家里)
  ↓
MacBridge App → go-bridge (端口 8777)
  ├── Claude Code (stdin/stdout)
  ├── OpenCode (HTTP :64667)
  ├── Codex (ws://localhost:4141)
  └── Copilot (ws://localhost:8875)
```

### 1.3 与旧方案的区别

| 旧方案 (v2.0) | 新方案 (v3.0) |
|---|---|
| go-bridge 通过 launchctl 命令行启动 | MacBridge App 管理 go-bridge 子进程 |
| iOS 手动填 IP+端口配对 | iOS 扫码配对（BridgePairingView） |
| 无远程 URL 概念 | MacBridge 设置页 Remote URL 字段 |
| InsecureURLSessionDelegate 处理自签名 | 同样需要（WSS 自签名证书不变） |

---

## 二、架构设计

### 2.1 端口规划

| 位置 | 端口 | 用途 | 协议 |
|------|------|------|------|
| VPS | 7000 | frps 服务端口 | TCP |
| VPS | 9090 | Nginx WSS 入口（外部唯一暴露端口） | HTTPS/WSS |
| VPS | 8777 | frp 映射端口（内部，仅 Nginx 访问） | TCP |
| Mac | 8777 | go-bridge WebSocket | WS |

### 2.2 数据流

```
iPhone App → wss://47.236.182.45:9090/bridge
  → VPS Nginx (SSL 终结, 9090)
    → VPS 127.0.0.1:8777 (frp 映射)
      → frp 隧道 → Mac 127.0.0.1:8777
        → go-bridge WebSocket handler
          → hello 握手 → register → 正常通信
```

---

## 三、配置步骤

### 3.1 VPS 端配置

> 如已按旧方案配置过，跳过 3.1.1 和 3.1.2，直接确认配置是否匹配 3.1.3。

#### 3.1.1 安装 frps

```bash
ssh root@47.236.182.45

cat > /opt/frp/frps.ini << 'EOF'
[common]
bind_port = 7000
token = opencode-secret-2026
dashboard_port = 7500
dashboard_user = admin
dashboard_pwd = opencode123
EOF

# 如尚未创建 systemd 服务：
cat > /etc/systemd/system/frps.service << 'EOF'
[Unit]
Description=frp server
After=network.target

[Service]
Type=simple
ExecStart=/opt/frp/frps -c /opt/frp/frps.ini
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable frps
systemctl restart frps
systemctl status frps
```

#### 3.1.2 生成自签名 SSL 证书

```bash
mkdir -p /etc/ssl/private

# 检查证书是否过期：
openssl x509 -enddate -noout -in /etc/ssl/certs/opencode-selfsigned.crt

# 如需重新生成（有效期 365 天）：
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/ssl/private/opencode-selfsigned.key \
  -out /etc/ssl/certs/opencode-selfsigned.crt \
  -subj "/C=CN/ST=Beijing/L=Beijing/O=CCCode/CN=47.236.182.45"

chmod 600 /etc/ssl/private/opencode-selfsigned.key
```

#### 3.1.3 配置 Nginx

```bash
cat > /etc/nginx/sites-available/opencode << 'EOF'
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

        # WebSocket 长连接
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400;
        proxy_send_timeout 86400;
        chunked_transfer_encoding on;
    }
}
EOF

ln -sf /etc/nginx/sites-available/opencode /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

### 3.2 MacBook 端配置

#### 3.2.1 确认 frpc 正在运行

```bash
# 检查 frpc 进程
ps aux | grep frpc

# 查看日志
tail -20 /tmp/frpc.log
```

frpc 配置文件 `~/Projects/LaunchAgentTask/frpc/frpc.ini` 应为：

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
```

如果 frpc 没运行或配置不对：

```bash
# 启动/重启 frpc
launchctl unload ~/Library/LaunchAgents/frpc.plist 2>/dev/null
launchctl load ~/Library/LaunchAgents/frpc.plist

# 验证
tail -5 /tmp/frpc.log
# 应看到：[opencode] start proxy success
```

#### 3.2.2 确认 MacBridge + go-bridge 正在运行

```bash
# 确认 go-bridge 监听
lsof -nP -iTCP:8777 -sTCP:LISTEN

# 确认 MacBridge App 运行
ps aux | grep CCCodeBridge
```

如果 go-bridge 没运行：打开 `/Applications/CCCodeBridge.app`，点 Start。

#### 3.2.3 在 MacBridge 中设置 Remote URL

1. 打开 MacBridge App
2. 进入 **Status** 标签页
3. 在 **Remote URL** 输入框填入：`wss://47.236.182.45:9090`
4. 点 **Save & Restart**

这一步会让 go-bridge 在 hello_ack 中告诉 iOS 这个远程地址，iOS 端会自动保存到 SavedBridge 的 remoteURL 字段。

### 3.3 iOS 端配置

#### 3.3.1 局域网配对（首次）

1. iPhone 和 Mac 在同一 WiFi
2. MacBridge 菜单栏 → **Pair New Bridge**
3. iPhone App → **Settings → Pair New Bridge** → 扫描 Mac 显示的 QR 码
4. 配对成功后 iPhone 自动连接

#### 3.3.2 外网使用

配对成功后，iOS 的 SavedBridge 已包含 remoteURL（`wss://47.236.182.45:9090`）。

断开 WiFi / 离开家里网络后，iOS 会自动尝试用 remoteURL 连接。

也可以手动切换：
1. **Settings → 已配对的 Mac** → 选择要连接的 Mac
2. BridgeProvider 会优先尝试 localURL，失败后自动 fallback 到 remoteURL

#### 3.3.3 SSL 证书信任（iOS 端代码支持）

MacBridge 的 iOS Transport 已内置 `InsecureURLSessionDelegate`，允许自签名证书的 WSS 连接。Info.plist 已配置 `NSAllowsArbitraryLoads`。

如需确认：

```bash
grep -A5 NSAppTransportSecurity OpenCodeiOS/OpenCodeiOS/Resources/Info.plist
```

---

## 四、验证测试

### 4.1 分步验证

```bash
# 1. Mac 本地：go-bridge 是否监听
lsof -nP -iTCP:8777 -sTCP:LISTEN
# 预期：go-bridge 进程

# 2. Mac 本地：WebSocket 是否响应
curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8777/
# 预期：426（要求升级 WebSocket）

# 3. VPS 内部：frp 隧道是否通
ssh root@47.236.182.45 "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8777/"
# 预期：426

# 4. VPS 外部：Nginx WSS 是否通
curl -k -s -o /dev/null -w '%{http_code}' https://47.236.182.45:9090/
# 预期：426

# 5. 外网 WebSocket 升级测试
curl -k -v --max-time 10 \
  -H "Upgrade: websocket" \
  -H "Connection: Upgrade" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" \
  https://47.236.182.45:9090/
# 预期：101 Switching Protocols
```

### 4.2 iOS 端验证

1. iPhone 关闭 WiFi（或切到蜂窝网络）
2. 打开 CCCode App
3. 如果已配对过 Mac 并配置了 Remote URL → 自动尝试远程连接
4. 连接成功后应看到 session 列表正常加载
5. 选择一个 session 发消息，验证正常工作

### 4.3 安全验证

在 iOS Settings 中手动添加一个 `ws://` 公网地址（如 `ws://47.236.182.45:9090`），应该被拒绝：
- `validateRemoteURLSecurity` 只允许 `ws://` 用于 localhost 和私有网络
- 公网地址必须使用 `wss://`

---

## 五、故障排查

### 5.1 iOS 连不上远程

```bash
# 1. 检查 go-bridge 是否运行
lsof -nP -iTCP:8777 -sTCP:LISTEN

# 2. 检查 frpc 隧道
tail -20 /tmp/frpc.log
# 看 "start proxy success" 或错误信息

# 3. 检查 VPS 端
ssh root@47.236.182.45 "systemctl status frps && systemctl status nginx"

# 4. 检查 SSL 证书是否过期
openssl s_client -connect 47.236.182.45:9090 </dev/null 2>&1 | grep notAfter

# 5. 检查 MacBridge Remote URL 是否正确
# MacBridge App → Status → Remote URL 应为 wss://47.236.182.45:9090
```

### 5.2 连接成功但功能异常

```bash
# 检查 go-bridge 日志
cat /tmp/go-bridge.log | tail -50

# 检查各后端是否正常
# MacBridge App → AI Tools 标签页，查看各后端状态
```

### 5.3 频繁断连

```
可能原因：
- Nginx proxy_read_timeout 过短（已设 86400s，通常不会）
- frp 隧道不稳定（检查 VPS 网络状态）
- go-bridge ping 心跳超时（iOS 端 10s ping + 服务端 30s ping/90s timeout）
```

---

## 六、安全建议

### 6.1 当前方案（开发/个人使用）

- 自签名证书 + `NSAllowsArbitraryLoads` → 仅限开发阶段
- frp token 作为唯一认证层
- iOS 端通过 deviceToken + deviceId 双重认证（WebSocket 握手后 register）

### 6.2 生产环境改进

1. **正规 SSL 证书**：购买域名（如 `bridge.cccode.app`）+ Let's Encrypt 免费证书
2. **移除 InsecureURLSessionDelegate**：使用正规证书后删除
3. **移除 NSAllowsArbitraryLoads**：恢复 ATS 默认安全策略
4. **frp 加密**：frps/frpc 之间启用 TLS
5. **VPS 防火墙**：只暴露 9090（WSS 入口），关闭 7000/8777 外网访问

---

## 七、方案 B：Tailscale 直连（零配置隧道）

> 适用场景：不想维护 VPS / Nginx / frp，只需设备间互通。免费版支持 3 台设备。

### 7.1 方案对比

| | 方案 A：frp + VPS | 方案 B：Tailscale |
|---|---|---|
| 需要公网服务器 | ✅ 阿里云 VPS | ❌ 不需要 |
| 配置复杂度 | 高（frps + frpc + Nginx + SSL） | 低（装 app，登录） |
| 自签名证书 | 需要 + iOS 代码绕过 | **不需要**（Tailscale 内置加密） |
| iOS ATS 问题 | 需要 NSAllowsArbitraryLoads | 无（ws:// 走 Tailscale 内网 IP） |
| 端口暴露 | VPS 9090 对公网开放 | 不开放任何端口 |
| 依赖方 | 阿里云 VPS 可用性 | Tailscale 协调服务器（免费稳定） |
| 延迟 | VPS 地域影响（国内 VPS 延迟低） | WireGuard 直连（设备间最短路径） |
| 成本 | VPS 月费 | 免费（≤3 台设备） |

**结论：个人使用优先选 Tailscale，有 VPS 或需要固定公网入口用方案 A。**

### 7.2 架构

```
iPhone (任意网络)          MacBook (任意网络)
  ↓                           ↓
Tailscale 虚拟网络 (100.x.x.x)
  ↓                           ↓
┌──────────┐    ws://100.x.x.x:8777    ┌──────────────────┐
│ iPhone   │◄──────────────────────────►│ MacBridge App     │
│ CCCode   │   Tailscale 隧道加密       │ → go-bridge :8777 │
└──────────┘                           └──────────────────┘
```

关键点：
- 两台设备各有一个 `100.x.x.x` 的 Tailscale IP，属于同一虚拟局域网
- iPhone 用 `ws://100.x.x.x:8777` 连接，**等价于局域网直连**
- 不需要 SSL、不需要 Nginx、不需要证书绕过——Tailscale 隧道层已加密
- iOS 端 `validateRemoteURLSecurity` 会识别 `100.x.x.x` 为非私有 IP，需走 `ws://` 校验逻辑放行

### 7.3 安装配置

#### 7.3.1 Mac 端

1. 下载安装 [Tailscale macOS App](https://tailscale.com/download/mac)
2. 登录 Tailscale 账号（Google/Microsoft/Apple 均可）
3. 点击菜单栏 Tailscale 图标 → 确认已连接
4. 查看本机 Tailscale IP：

```bash
# 方法 1：命令行
tailscale ip -4
# 输出如：100.101.102.103

# 方法 2：菜单栏
# Tailscale 图标 → 点击头像旁显示的 IP
```

#### 7.3.2 iPhone 端

1. App Store 搜索 **Tailscale**，安装
2. 登录**同一个** Tailscale 账号
3. 打开 Tailscale VPN 开关，确认已连接
4. Settings → 查看本机 Tailscale IP

#### 7.3.3 MacBridge 配置

1. 打开 MacBridge App
2. **Status** → **Remote URL** 填入：`ws://100.x.x.x:8777`（Mac 的 Tailscale IP）
3. 点 **Save & Restart**

> 注意这里用 `ws://` 而非 `wss://`。Tailscale 隧道已加密，不需要额外 TLS 层。

#### 7.3.4 iOS 配对

与方案 A 相同：同一 Tailscale 网络下扫码配对即可。

配对后 SavedBridge 的 remoteURL 自动保存为 `ws://100.x.x.x:8777`。
离开 Mac 本地 WiFi 后 iOS 自动 fallback 到这个远程地址。

### 7.4 验证

```bash
# 1. Mac 端确认 Tailscale IP
tailscale ip -4
# 如：100.101.102.103

# 2. Mac 端确认 go-bridge 监听
lsof -nP -iTCP:8777 -sTCP:LISTEN

# 3. iPhone 端通过蜂窝网络测试
# 关闭 WiFi → 打开 CCCode App → 自动连 remoteURL → 查看连接状态

# 4. 从另一台设备 ping 通（可选）
ping 100.101.102.103
```

### 7.5 故障排查

**连不上 Tailscale IP**

```bash
# Mac 端
tailscale status    # 确认在线
tailscale ping 100.x.x.x  # ping iPhone 的 Tailscale IP

# iPhone 端
# Tailscale App → 确认 VPN 开关打开
# 如果显示 "Relay" 而非 "Direct"：两台设备无法直连，走中继（仍可用，延迟稍高）
```

**iOS 拒绝 ws:// Tailscale IP**

`validateRemoteURLSecurity` 检查 `100.x.x.x` 发现不是 RFC 1918 私有地址，可能拒绝。
需要在 iOS 端放行 Tailscale 网段（100.64.0.0/10，CGNAT 地址空间）：

```swift
// 在 validateRemoteURLSecurity 中，已有 localhost/loopback/RFC1918 检查后加：
if host.hasPrefix("100.") {
    let parts = host.split(separator: ".")
    if parts.count >= 2, let first = Int(parts[0]), first == 100,
       let second = Int(parts[1]), (64...127).contains(second) { return }
}
```

> 如果你的 Tailscale IP 以 `100.` 开头但连接被拒绝，需要加这段。
> 实际上当前代码中只要设备间配对成功、remoteURL 通过 hello_ack 传递，
> iOS 会直接使用保存的 URL 而不走额外校验。这个检查只在首次手动输入时生效。

**性能不如局域网**

Tailscale 默认走 WireGuard 隧道，直连时延迟 ≈ 局域网；中继时增加 20-100ms。
可以通过 `tailscale status` 查看 iPhone 是 "Direct" 还是 "Relay" 连接。
如果长期 Relay，检查两端 NAT 类型（对称 NAT 无法打洞，需 DERP 中继）。

### 7.6 与方案 A 共存

两套方案可以同时使用：

- MacBridge Remote URL 填 `wss://47.236.182.45:9090`（方案 A）
- SavedBridge 的 remoteURL 保存的是 VPS 地址
- 如需切换到 Tailscale：MacBridge 改 Remote URL → `ws://100.x.x.x:8777` → Save & Restart
- iOS 端重新扫码配对或手动编辑 SavedBridge 的 remoteURL

---

## 八、相关文件（两方案共用）

```
VPS（仅方案 A）：
  /opt/frp/frps.ini
  /etc/systemd/system/frps.service
  /etc/nginx/sites-available/opencode
  /etc/ssl/certs/opencode-selfsigned.crt
  /etc/ssl/private/opencode-selfsigned.key

Mac:
  ~/Projects/LaunchAgentTask/frpc/frpc.ini       # 方案 A：frpc 隧道配置
  ~/Library/LaunchAgents/frpc.plist               # 方案 A：frpc 自启动
  /Applications/CCCodeBridge.app                  # MacBridge App
  /Applications/Tailscale.app                     # 方案 B：Tailscale
  /tmp/frpc.log
  /tmp/go-bridge.log

iOS:
  CCCodeBridgeTransport.swift (WebSocket + ping heartbeat)
  BridgeProvider.swift (remoteURL fallback + validateRemoteURLSecurity)
  InsecureURLSessionDelegate.swift (自签名证书信任，仅方案 A 需要)
  Info.plist (ATS 配置，仅方案 A 需要)
