# Gate Desktop Attach：当前 Codex Desktop 连接官方 local daemon

## 当前构建复核（2026-08-24）

- Desktop：ChatGPT `26.818.41509`（bundle `6962`）
- Desktop 内嵌 CLI：`codex-cli 0.149.0-alpha.4.1`
- standalone CLI：`codex-cli 0.149.0-alpha.4`
- `app.asar` SHA-256：`8eb91bd9efbf9a4dd04b9b0afdbfcb4e0bab5da18c1919ad74ca327c00c7e791`
- 内嵌 `codex` SHA-256：`09db9560f6f9dec139d3324254fb3c8fdbad5ecce1d8c794113dc15294f6aefd`

对当前 `app.asar` 做本地只读解包后，`.vite/build/src-CLzQUgbV.js` 中的本地 transport 逻辑再次确认：

1. local、非 Windows host；
2. `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`；
3. `CODEX_APP_SERVER_FORCE_CLI!=1`；
4. 没有 `CODEX_CLI_PATH`、host `codex_cli_command` 或 bundle 强制 CLI 覆盖；
5. 使用当前选择的 CLI 在 2500ms 内执行 `app-server daemon version`，返回的 `appServerVersion` 通过 Desktop 自己的兼容检查；

以上条件同时满足时，transport 将 `kind` 从 `stdio` 改为 `websocket`，连接
`$CODEX_HOME/app-server-control/app-server-control.sock`；任一条件不满足或 probe 抛错时，代码明确落入
`stdio` 的 `QB(...)` 分支。该 transport 的 `supportsReconnect()` 仅在 `kind=websocket` 时返回 true。
`CODEX_APP_SERVER_FORCE_CLI=1` 是当前构建仍存在的、仅供隔离取证使用的强制 stdio 入口；不得进入产品 fallback。

同日只读现场复核（PID 仅代表本次采样）：

- Desktop 主进程有 `1` 个 Unix FD peer 命中 daemon control-socket object；
- CordCode runtime 有 `2` 个 Unix FD peer 命中同一 daemon；该数量与主 connection + observer 的预期形态一致，但单靠 FD 数量不能独立证明两个逻辑 client 的身份；
- `launchctl getenv CODEX_APP_SERVER_USE_LOCAL_DAEMON` 为 `1`；
- daemon `version` 返回 `status=running`；
- 未为本次复核启动隔离 Desktop、未终止 owner 进程。

因此当前构建的**静态 Attach Gate 与当前 shared 活体状态通过**。强制 stdio/private 与 mixed 聚合仍须按分析文档的隔离实验协议补采；在补采前不得把旧构建的 private/mixed 活体样本冒充当前构建证据。

以下 2026-08-22 内容保留为历史基线，不覆盖本节当前构建结论。

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
  `$CODEX_HOME/app-server-control/app-server-control.sock`；
- 因而 `launchctl setenv` 可以作为 CordCode Link 配置“下一次正常启动 Desktop 使用共享 daemon”的产品入口。

PID/FD 仅是本次活体采样值，不是产品常量。

## Gate 裁决

路线 A **PASS**：当前 Desktop 有官方、免注入的 local-daemon attach 入口。后续产品实现必须：

1. 先确保官方 managed standalone 与 daemon 可用；
2. 在 Desktop 启动前为用户 launchd domain 设置上述环境变量；
3. CordCode 只连接同一 control socket；
4. 禁止 `managed-loopback-ws` 作为 Desktop 产品 fallback；
5. 首次启用或登录环境重建后，已经运行的 Desktop 需要重启一次以继承环境；CordCode 不自动终止它。

## Gate 2 产品接线复验

Release `74fc3866d18c` 覆盖安装后：

- `/Applications/CordCodeLink.app` 内嵌 runtime 版本为
  `cordcode-bridge-runtime 0.1.0 (commit: 74fc3866d18c, ...)`；
- 旧 `managed-loopback-ws` 进程与 record 已清理，进程树不再出现
  `codex app-server --listen ws://127.0.0.1:<managed-port>`；
- CordCode runtime PID `98206` 的 UDS FD `4`/`24` 分别指向官方 daemon PID `85510`
  接受的 control-socket FD `19`/`20`（主 connection + observer connection）；
- 由已安装 CordCode Link 设置 launchd 环境后，经 LaunchServices 正常启动的隔离 Desktop
  PID `98498` 没有私有 app-server 子进程，其 FD `140` 指向同一 daemon control-socket
  listener；
- `codex-web` passive subscription 与 catalog seed 均在该 daemon 上成功。

PID/FD 仍只代表本次采样。owner 当前正在使用的主 Desktop 是设置环境前启动的进程；为了不终止
官方客户端及本 Codex 会话，本轮没有自动重启它。owner 真机矩阵前必须手动重启 Desktop 一次。

## v2.0 可复跑拓扑检查

只读检查脚本：

```text
scripts/codex-web-phase0/verify_shared_daemon_topology.sh
```

默认验证 active `CODEX_HOME` standalone 与 Desktop 内嵌 CLI exact version、launchd attach 环境、
daemon listener 和零 managed-loopback。提供隔离 Desktop/CordCode Release PID 后还会用 Unix FD peer
证明它们连接同一 daemon：

```text
CODEX_DESKTOP_PID=<isolated-desktop-pid> \
CORDCODE_RUNTIME_PID=<installed-runtime-pid> \
scripts/codex-web-phase0/verify_shared_daemon_topology.sh
```

v2.0 hardening 同时从正式 Go lifecycle 删除了 managed process 的构造、持久化与 Close 回收面；只保留
旧 record 的严格 PID/argv/start-time/port 校验清理。daemon start binary 固定解析 active
`CODEX_HOME/packages/standalone/current/codex`，不再从 PATH 或 Desktop 内嵌 CLI 降级选择。

## v2.0 Release 拓扑复验（2026-08-22）

由当前源码构建并覆盖安装的 unsigned Release：

- runtime：`cordcode-bridge-runtime 0.1.0 (commit: 364dec7ce099, built: 2026-08-22T09:19:11Z)`；
- CordCode Link PID `16518`，其 Release runtime PID `16645`；
- 使用独立 `--user-data-dir`、经 LaunchServices 启动的隔离 Desktop PID `16837`；
- 官方 daemon PID `85510`；standalone 与 Desktop CLI 均为 `codex-cli 0.149.0-alpha.4`；
- `verify_shared_daemon_topology.sh` 实测 `desktop_shared_peers=1`、
  `cordcode_shared_peers=2`、`managed_loopback_count=0`、`topology_gate=PASS`；
- 隔离 Desktop 进程树没有私有 `codex app-server` 子进程；验证后只终止了隔离 Desktop 及其
  crashpad helper，owner 正在使用的主 Desktop 未被修改。

这证明已安装 Release 和继承 launchd 环境的新 Desktop 可以同时附着同一个官方 daemon。owner 主
Desktop PID `68040` 启动早于环境设置，仍保留其旧私有 stdio app-server；必须由 owner 手动退出并重开
一次，才能进入 v2.0 T1 双端最小闭环验收。PID 仅为本次证据，不是产品常量。
