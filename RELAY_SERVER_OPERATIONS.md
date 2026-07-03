# Relay Server 部署与运维

> 本文按当前独立 Go module、HPKE 协议和仓库部署脚本校正。旧 FRP `:9090` 是并存的
> 历史服务，不属于 Relay 部署链；相邻旧一体仓库的安装文档只能作为迁移证据，不能替代本文。

## 架构

```text
iOS / MacBridge
    │ wss://relay.byteseek.uk:8443
    ▼
nginx :8443
    ▼
relay-server 127.0.0.1:8780
    ├─ route / device connection routing
    ├─ bounded mailbox
    └─ opaque HPKE envelopes
```

Relay 不持有会话明文。route credential、provisioning token、activation identity 和设备私钥
都不得写入本文档、commit、handoff 或日志。

## 官方 Relay、凭据与信任边界

产品内置的官方 Relay endpoint 是公开构建配置，不是秘密。MacBridge 首次启用 Relay 时，
由 `OfficialRelayProvisioner` 使用本机 activation identity 创建或恢复 route；普通用户
不需要领取部署级 provisioning token。route/device credential 均在运行期产生：

- Mac 侧 activation identity、route credential 和相关 secret 只保存在本机 app data dir，
  文件权限为 `0600`；
- iOS 设备只通过 Mac 批准后的 pairing 流程取得该设备专属的 route material；
- 不同设备不共享 credential；撤销设备后旧 credential 不得继续访问；
- Relay 可以看到路由所需 metadata 与密文大小/时序，但不能解密 Bridge RPC、事件或
  mailbox payload；
- Relay TLS 使用系统 CA 信任，业务 payload 另有 HPKE 端到端加密，不使用 Tailscale
  路径的 SPKI pin。

部署级 provisioning token 只用于运维验收接口，不属于 MacBridge 日常用户流程。不得把
它和普通 route credential、iOS device credential 或 pairing token 混为同一种密钥。

## 离线 mailbox

Relay 为暂时离线的已配对设备保存有界 HPKE 密文，而不是聊天明文：

1. 在线投递失败或目标离线时，符合 durable milestone 规则的 envelope 进入 per-device
   mailbox；
2. iOS 重连后按 cursor fetch；
3. iOS 在本地解密、校验并应用；
4. 成功 reconcile 后按 cursor ack；
5. TTL、单设备容量和 frame 大小限制负责淘汰；设备撤销会清理其未确认 mailbox。

服务端使用 SQLite WAL 持久化 route/device/mailbox。验证不能只看“fetch 返回 200”，还要
覆盖断线入库、服务重启后仍可 fetch、客户端 ack 后不重复、撤销后清空，以及慢设备队列
溢出不会阻塞同 route 的其他设备。

## 本机前置

凭据只存于本机 shell 环境：

```bash
export CORDCODE_RELAY_VPS_HOST='<host>'
export CORDCODE_RELAY_VPS_USER='<user>'
export CORDCODE_RELAY_VPS_PASS='<password>'
```

仓库的 `.zshrc` 示例值不是配置文件；不得提交真实密码。

## 构建

`relay-server/` 是独立 module（`module cordcode-relay`）：

```bash
(cd relay-server && go test ./... -count=1)
(cd relay-server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' \
  -o /tmp/cordcode-relay-server ./cmd/relay-server)
shasum -a 256 /tmp/cordcode-relay-server
```

不要在仓库根目录用 `go test ./...` 代替 relay module 测试。

## 首次 VPS 布局

推荐：

```text
/opt/cordcode-relay/
  bin/relay-server
  backups/
/var/lib/cordcode-relay/
  relay.db
/etc/cordcode-relay/relay.env
/etc/systemd/system/cordcode-relay.service
```

创建不可登录的系统用户并限制目录权限。systemd service 只监听
`127.0.0.1:8780`，公网 TLS 由 nginx `:8443` 提供。环境文件权限应为 `0600`。

nginx 必须：

- 代理 Relay WebSocket/HTTP 路径（如 `/v1/`、`/healthz`、`/readyz`）到 `127.0.0.1:8780`；
- 同时提供 Web QR Flow C 的静态客户端 `/web/`（通常 `alias /var/www/cordcode-web/;`），因为
  MacBridge 会把 `wss://relay...` 改写为 `https://relay.../web/?...` 作为 `webQrPayload`；
- 正确传递 `Upgrade`/`Connection`；
- 使用有效 CA 证书；
- 设置足够长的 WebSocket read timeout；
- 不记录 Authorization、route credential 或 envelope payload。

## 日常部署

构建 `/tmp/cordcode-relay-server` 后执行：

```bash
source ~/.zshrc
scripts/deploy-relay-vps.sh
```

脚本负责：

1. 只读检查远端状态；
2. 备份当前 binary；
3. 上传到临时路径；
4. 校验 SHA-256；
5. 保留 owner/group/mode 并原子替换；
6. 重启 systemd；
7. 执行健康检查；
8. 输出带时间戳备份路径的回滚命令。

提交代码不会自动更新 VPS；只有部署脚本成功完成后，运行中的 Relay 才使用新版本。

## 验证

VPS 上：

```bash
systemctl status cordcode-relay --no-pager
journalctl -u cordcode-relay -n 200 --no-pager
ss -lntp | grep 8780
nginx -t
```

外部：

```bash
curl -fsS https://relay.byteseek.uk:8443/healthz
```

还需确认：

- MacBridge 的 `RelayBridgeClient` 能自动重连；
- 已配对 iOS 设备能恢复 session；
- 撤销设备会即时下发 `device_revoked` 并断开 relay connection；
- mailbox/reconcile 不产生重复或倒序 frame；
- 慢 device 的有界发送队列只隔离自身，不阻塞同 route 其他设备。

## 更新失败与回滚

部署脚本失败时保留现场：

```bash
systemctl status cordcode-relay --no-pager
journalctl -u cordcode-relay --since "-10 min" --no-pager
sha256sum /opt/cordcode-relay/bin/relay-server
```

使用脚本打印的原子回滚命令恢复备份，然后再次执行健康检查。不要用临时旧协议兼容分支、
跳过 SHA 校验或把 Relay 改成明文转发来“先恢复服务”。

## 旧 VPS / FRP 自定义远程路径

原一体仓库的《OpenCode iOS 配置阿里云 VPS…》和 `go_bridge_使用指南.md` 后半部分记录了
FRP + nginx 的历史公网入口。它不是当前默认 Relay，也不提供 HPKE 端到端加密，但产品仍
保留“VPS / FRP / 反向代理”自定义地址能力，因此以下架构知识仍有效：

```text
iOS -- wss://custom-domain/... --> nginx TLS
    --> frps/VPS tunnel --> frpc/Mac --> cordcode-bridge-runtime:8777
```

现行规则：

- 默认远程方式是 Relay；仅在明确维护自定义基础设施时使用该路径；
- MacBridge 远程页填写最终外部 `wss://` 地址，`https://` 会规范化成 `wss://`；
- 公网 `ws://` 会被标记为不安全，iOS 拒绝连接；
- 不能使用自签名公网证书配合 iOS 信任绕过；公网 endpoint 应使用有效 CA 证书；
- Tailscale `wss://100.x:8778` 是另一条带 SPKI pin 的路径，不等同于 FRP；
- 自定义地址只是 pairing/hello 候选，Bridge device token/auth 仍然生效；
- 旧教程中的 OpenCode HTTP 直连、ATS 全局放开、`64667` 公网暴露和 Node Bridge `8766`
  均已废弃。

注意，“自定义远程地址”是把统一 Bridge WebSocket 暴露到公网；它不是
`relay-server` 的 route/device API，也没有官方 Relay 的 HPKE mailbox 语义。反过来，仅仅
部署一份兼容的 `relay-server` 也不会自动让当前产品改用它：还需要匹配的 endpoint 构建
配置、activation/provisioning 与 route 生命周期集成。两条路径不可只因 UI 中都出现
“远程/Relay”字样就互换配置。

### 自定义路径分层验证

```bash
# 1. Mac runtime
lsof -nP -iTCP:8777 -sTCP:LISTEN

# 2. Mac → tunnel client
# 使用实际 frpc/反向代理服务的状态命令和日志

# 3. VPS 内部 upstream
curl -i --max-time 3 http://127.0.0.1:<mapped-port>/bridge

# 4. 公网 TLS 与证书
curl -vk --max-time 5 https://<custom-host>/<bridge-path>
openssl s_client -connect <custom-host>:443 -servername <custom-host>

# 5. WebSocket/auth
# 普通 HTTP 的 400/401 只证明到达对应层；最终必须由已配对 iOS 完成
# WebSocket upgrade + hello_ack + authenticated RPC。
```

排障顺序必须保持 Mac runtime → tunnel client → VPS upstream → nginx/TLS → WebSocket/auth，
否则容易把 token、TLS、端口映射和 Bridge 协议问题混成一个“连接失败”。

| 现象 | 已证明 | 下一跳 |
| --- | --- | --- |
| Mac `8777` 未监听 | runtime 尚不可用 | 先修 MacBridge 安装/启动 |
| tunnel 日志连不上 Mac | 隧道本地端错误 | 核对目标必须是当前 runtime `8777` |
| VPS upstream timeout/refused | 公网入口尚未到 Bridge | 查 frps/frpc、映射端口与防火墙 |
| TLS 握手或证书失败 | nginx/证书层失败 | 查域名、SNI、证书链和有效期 |
| HTTP `400/401` | 请求已到 HTTP/auth gate | 改用真实 WebSocket + pairing token 验证 |
| WebSocket 成功但无 `hello_ack` | 协议握手未完成 | 对齐 iOS/Mac 日志中的 device、protocol 与 close reason |
