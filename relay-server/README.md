# CCCode Relay Server

`relay-server` 是部署在 VPS 的独立公网密文路由服务。它不启动 agent、不访问 Mac backend，也不解密 Bridge payload。

## 能力

- `POST /v1/activations/routes`：MacBridge 首次启动以本机 Keychain 中生成的 Ed25519 激活身份签名创建或恢复 route，不要求用户配置服务地址或密钥。
- `POST /v1/routes/register`：保留给运维验收的部署级 provisioning token route 创建入口，不属于普通用户流程。
- `POST /v1/routes/{route}/devices/register`：Mac 使用 `bridgeAuth` 登记已批准设备。
- `GET /v1/routes/{route}/bridge` 与 `GET /v1/routes/{route}/devices/{device}`：在线 WebSocket 密文转发。
- `GET .../mailbox` 与 `POST .../mailbox/ack`：设备离线密文 fetch/ack。
- `POST .../revoke`：撤销设备并清理未确认密文。
- SQLite WAL 保存 route、device 与 mailbox；支持 TTL、容量淘汰、健康检查、按敏感操作/IP 限速和无 payload 安全日志。

## 本地构建与测试

```bash
go test ./... -count=1 -race
go build ./cmd/relay-server
```

公网部署完成后，使用仅在执行进程中提供的 provisioning token 运行真实 API/WebSocket 验收：

```bash
RELAY_LIVE_BASE_URL=https://relay.example.com:8443 \
RELAY_LIVE_PROVISION_TOKEN='<keychain 中的 token>' \
go test ./internal/relay -run TestLiveRelayDeployment -count=1
```

该测试会在生产数据库创建一个验收 route，随后验证在线透传、离线 fetch/ack 与设备撤销；不会将 credential 输出到日志。

需要证明 VPS 重启恢复时，额外指定 `RELAY_LIVE_RESTART_GATE=/tmp/relay-restart.ready`；测试在密文入库后创建 gate 文件并等待运维重启 relay 服务、删除 gate 文件，随后再 fetch/ack 同一密文。

## 生产配置

服务端不保存 provisioning token 明文。该 token 仅用于运维验收入口；普通 MacBridge 使用签名激活接口，不分发该 token。先在安全终端生成随机 token，只把摘要写入 VPS 环境文件：

```bash
TOKEN="$(openssl rand -base64 32)"
printf '%s' "$TOKEN" | shasum -a 256
```

`/etc/cccode-relay/relay.env` 示例，`RELAY_PROVISION_TOKEN_SHA256` 替换为上一步摘要：

```bash
RELAY_LISTEN_ADDR=127.0.0.1:8780
RELAY_DB_PATH=/var/lib/cccode-relay/relay.db
RELAY_PUBLIC_ENDPOINT=wss://relay.example.com:8443
RELAY_PROVISION_TOKEN_SHA256=<sha256-hex>
RELAY_ACTIVATION_RATE_LIMIT_PER_MINUTE=6
RELAY_MAILBOX_TTL=24h
RELAY_MAX_MAILBOX_BYTES=52428800
RELAY_MAX_FRAME_BYTES=2097152
RELAY_RATE_LIMIT_PER_MINUTE=30
```

部署时应创建专用的非登录用户 `cccode-relay`，数据库目录权限限于该用户，环境文件权限设为 `0600`。生产 systemd 与 Nginx 配置应由部署环境单独维护，不随公开候选源码迁入。

## TLS 与 Cloudflare

公网端点由部署方通过 `RELAY_PUBLIC_ENDPOINT` 指定。若使用 Cloudflare 代理 WebSocket/TLS，生产配置要求：

1. Cloudflare SSL/TLS 设置为 `Full (strict)`。
2. VPS Nginx 安装对应域名的 Cloudflare Origin CA 证书或公开受信证书。
3. Cloudflare 保持 WebSockets 可用；可进一步启用 Authenticated Origin Pulls。
4. Nginx 维护 Cloudflare 官方 IP 段的 `real_ip` 白名单后才把客户端 IP 传给 relay，避免源站直连伪造限速键。
5. relay 进程仅监听 `127.0.0.1:8780`，TLS 由 Nginx 终止；禁止用自签名绕过证书验证。

## 尚需线上执行

仓库内实现与本地测试不能证明公网服务已运行。正式完成还要求在目标服务器部署二进制、安装证书、启用 systemd/Nginx、设置生产 token，并以真实 Mac/iPhone 验证在线、离线、ack、撤销与服务重启恢复。
