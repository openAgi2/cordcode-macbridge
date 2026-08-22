# Gate Desktop Attach：当前 Codex Desktop 连接官方 local daemon

- 采集日期：2026-08-22
- Desktop：ChatGPT `26.818.31338`（bundle `6892`）
- Desktop 内嵌 CLI：`codex-cli 0.149.0-alpha.4`
- standalone CLI：`codex-cli 0.149.0-alpha.4`
- `app.asar` SHA-256：`7db5508d4acd2c324cc572cd6f8d6d07900d185831bd6d54005a573e7186de54`
- 内嵌 `codex` SHA-256：`10afbeddd6f951635d8fcfbb337034d37934bb3495c16d053b3560d75747619b`

## 宿主入口

对当前安装包的 `app.asar` 做本地只读解包后，Desktop 的 app-server 连接逻辑存在一条第一方 local-daemon 分支。该分支要求：

1. 本地 host、非 Windows；
2. `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`；
3. 未设置 `CODEX_APP_SERVER_FORCE_CLI=1`；
4. 未用 `CODEX_CLI_PATH` 或 host `codex_cli_command` 覆盖 CLI；
5. 当前 bundle 不命中其强制 CLI 条件；
6. `codex app-server daemon version` 成功且版本兼容。

满足条件后，Desktop 不再 spawn 私有 stdio `codex app-server`，而是用 WebSocket transport 连接：

```text
~/.codex/app-server-control/app-server-control.sock
```

这是 Desktop 当前安装包的宿主实现证据；开源 Rust TUI 的 `AppServerTarget::LocalDaemon` 只作为机制交叉参考，不替代本证据。

## 活体验证 A：直接环境变量启动隔离 Desktop

前置：用官方 standalone `codex app-server daemon start` 启动 daemon。隔离 Desktop 使用独立 `--user-data-dir`，不退出或修改 owner 正在使用的主 Desktop。

Desktop 日志的关键状态为：

```text
[AppServerConnection] Transport start success ... transport=websocket
[AppServerConnection] initialize_handshake_result ... outcome=success transportKind=websocket
[AppServerConnection] ... currentState=connected ... transport=websocket
```

进程与 FD 证据：

- 隔离 Desktop 下没有新的 `codex ... app-server` 子进程；
- Desktop 的 Unix FD peer 与 daemon 监听 `app-server-control.sock` 的 socket object 相同；
- daemon `version` 返回 `status=running`，managed/app-server/CLI 版本均为 `0.149.0-alpha.4`。

## 活体验证 B：LaunchServices 继承用户 launchd 环境

执行：

```text
launchctl setenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 1
open -na /Applications/ChatGPT.app --args --user-data-dir=<isolated-dir> --no-first-run
```

结果：

- 隔离 Desktop PID `87111` 没有私有 app-server 子进程；
- Desktop FD `144` 的 peer 为 daemon 监听 socket object；
- daemon PID `85510` 的 FD `10` 监听
  `/Users/jacklee/.codex/app-server-control/app-server-control.sock`；
- 因而 `launchctl setenv` 可以作为 CordCode Link 配置“下一次正常启动 Desktop 使用共享 daemon”的产品入口。

PID/FD 仅是本次活体采样值，不是产品常量。

## Gate 裁决

路线 A **PASS**：当前 Desktop 有官方、免注入的 local-daemon attach 入口。后续产品实现必须：

1. 先确保官方 managed standalone 与 daemon 可用；
2. 在 Desktop 启动前为用户 launchd domain 设置上述环境变量；
3. CordCode 只连接同一 control socket；
4. 禁止 `managed-loopback-ws` 作为 Desktop 产品 fallback；
5. 首次启用或登录环境重建后，已经运行的 Desktop 需要重启一次以继承环境；CordCode 不自动终止它。

