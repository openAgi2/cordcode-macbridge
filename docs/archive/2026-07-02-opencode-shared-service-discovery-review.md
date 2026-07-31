# 评审报告：OpenCode 共享本地服务接入方案

> 评审对象：[docs/2026-07-02-opencode-shared-service-discovery-plan.md](../2026-07-02-opencode-shared-service-discovery-plan.md)
> 评审日期：2026-07-02
> 评审人：Claude（Opus 4.8）
> 评审基线：源码核对 + 本机运行态实测（见 §9 证据附录）
> 评审立场：方向认可，但当前规格不可直接实现——核心机制不在 stable opencode 发行版中。

## 0. 总体结论

| 维度 | 结论 |
| --- | --- |
| 产品方向（一个 server，多 client） | ✅ 合理，与 OpenCode 官方心智模型一致 |
| **默认路径在当前 stable opencode 上的可行性** | ❌ 不可行——`opencode service` / `serve --register` 不在 stable CLI（含 1.17.13）中（见 B1） |
| 对 CordCode 现状源码的引用 | ✅ 准确 |
| 对 OpenCode 源码的引用 | ⚠️ 引用的 `daemon.ts` 在源码/tag 中存在，但**未编译进 stable 二进制的命令面** |
| 与 CordCode 架构活文档的契合度 | ⚠️ 大方向一致，但破坏"supervisor 只管 cccode-bridge-runtime"约定（见 M2） |
| 安全约束（§8） | ✅ loopback-only、不进 argv、fail-closed |
| 任务分解（T01–T06） | ✅ 结构清晰；但 T01 建立在 stable CLI 不存在的命令上 |

**建议：方案不能按当前规格进入实现。** 必须先在 B1 的三条出路里选一条并改写方案，再处理 M1–M4。

---

## 1. 阻断级问题（Blocker）

### B1 — `opencode service` / `serve --register` 不在 stable 发行版 CLI 中

**严重度：阻断（方案核心机制落空）**

方案 §3、§5.2、T01 的核心动作：

```
MacBridge 调用官方 opencode service start，获取共享 server URL
MacBridge 从官方 state 目录读取 password（0600）
```

该机制的源码依据是 `/Users/jacklee/Projects/opencode/packages/cli/src/services/daemon.ts`
（核对属实：`service start` → spawn `serve --register` → 写 `server.json`/`password`）。
**但本机实测，stable CLI 并不暴露它。**

实测（绕过 shell alias，对升级后的真实二进制）：

```
$ readlink /opt/homebrew/bin/opencode
../lib/node_modules/opencode-ai/bin/opencode.exe        ← npm opencode-ai@latest

$ /opt/homebrew/bin/opencode --version
1.17.13

$ /opt/homebrew/bin/opencode service --help
（回退顶层 help —— service 非已知命令）

$ /opt/homebrew/bin/opencode serve --help | grep -iE 'register|mdns|port|hostname'
--port / --hostname / --mdns / --mdns-domain            ← 无 --register
```

顶层命令清单（与旧 1.4.3 完全一致，**无 `service`**）：`completion / acp / mcp / [project] /
attach / run / debug / providers / agent / upgrade / uninstall / serve / web / models / stats /
export / import / github / pr / session / plugin / db`。`serve` 选项无 `--register`。

**源码 vs 发行物的不一致（根因）：**

```
$ git -C /Users/jacklee/Projects/opencode cat-file -e v1.17.13:packages/cli/src/services/daemon.ts && echo IN_TAG
IN_TAG                                                   ← 源码文件在 v1.17.13 tag 内
# 但由该 tag 构建的 opencode-ai@1.17.13 二进制并不暴露 service 命令
```

`daemon.ts` 作为源码在仓库 dev 分支与 `v1.17.13` tag 中都存在，但**没有被注册进 stable
`opencode` CLI 的命令面**。

**那 `service` 在哪？** 只在另一条 **2.0 preview 轨道**上：

```
$ @opencode-ai/cli@1.17.13（bin 名 lildax，self-description "OpenCode 2.0 preview"）
SUBCOMMANDS:
  api / debug / migrate / service / serve
  service    Manage the background server       ← 这里才有
  serve      Start the v2 API server            ← 注意是 "v2 API server"
```

`lildax` 才有 `service`，且它的 `serve` 明确是 **"v2 API server"**——与方案假设的 1.x 端点
（`/global/health`、`/global/event`、`GET/POST /session`、`/session/:id/message`、
`/session/:id/prompt_async`、`/session/:id/abort`）是否一致**未经核实**，极可能不同。

> 教训：方案所有"OpenCode 官方能力"均来自源码阅读，未做 stable 二进制级实测。"源码里有
> `daemon.ts`" 不等于 "stable `opencode` 暴露 `service`"。后续凡引用 OpenCode 能力，必须以
> 本机 stable 二进制 `<cmd> --help` 实测为准。

**三条出路（方案作者须先选一条，任一条都要求改写方案）：**

1. **去依赖重写（建议优先评估）**：只用 stable 实际提供的 `opencode serve --hostname 127.0.0.1
   --port <p>`。但它没有 `--register`、不写 `server.json`/`password`——方案想逃离的"凭据从哪来"
   问题不会被官方 discovery 解决，仍要回到现状模型（MacBridge 自管 Basic Auth / 复用 Desktop
   LaunchAgent）。这削弱了方案核心价值，需重新论证"为什么还要做"。
2. **切 2.0-preview `lildax` 轨**：改用 `@opencode-ai/cli`。但 (a) 安装物名是 `lildax` 不是
   `opencode`，`locateCLI()` 与所有用户文案要改；(b) `serve` 是 "v2 API server"，方案 §2/§5.6
   假设的 `/global/*`、`/session/*` 端点必须对 `lildax` 实测重核，不能沿用 1.x 源码结论；
   (c) 把不稳定 preview 轨当 MacBridge 默认依赖，风险高。
3. **等 `service` 进 stable**：方案挂起，等 `opencode-ai` 的 `service`/`--register` 进入
   `latest` 再实现。

**要求：** 在方案顶部写明所选出路与前置条件；否则 T01 无意义。在 §7 失败模式表补一行
"`opencode` 无 service 能力 → OpenCode backend unavailable，reason 含所需最低版本或替代轨道"。

---

## 2. 主要问题（Major）

### M1 — "go-bridge 继续使用现有 `-opencode-url`" 与现状不符，T02 隐藏新行为

方案 §5.5 称 "go-bridge 继续使用现有 `-opencode-url`、`-opencode-user`、`-opencode-pass`，
不需要先改数据面。" 暗示 discovery 只是把值填进既有入口。

现状核对：

- [RuntimeManager.swift](../../MacBridge/MacBridge/Services/RuntimeManager.swift) 构造 go-bridge
  argv（约 276–298 行）**并不传 `-opencode-url`**；URL 完全依赖 [go-bridge/main.go](../../go-bridge/main.go)
  的 flag 默认 `http://localhost:64667`（`envOr("OPENCODE_BASE_URL", "http://localhost:64667")`，
  约 35 行）。
- 用户名/密码走**环境变量** `OPENCODE_SERVER_USERNAME/PASSWORD`（约 301–313 行），不走 argv。

**结论：** T02 实际是**新增**把 `-opencode-url` 写进 argv（首次让 RuntimeManager 显式传 URL），
不是"继续使用"。应：

1. T02 显式说明 discovery 成功后 RuntimeManager argv 新增 `-opencode-url`；
2. 澄清 user/pass 仍走 env（保持 §8），并说明与既有 `credentials.json` 的关系（见 M3）；
3. 说明 [agent/opencode/opencode.go](../../agent/opencode/opencode.go) 的 fallback
   `http://localhost:64667`（约 74–80 行）在 T04 删除后，argv 不传 URL 时必须报错而非回退。

### M2 — supervisor 接管"启动外部 daemon"，破坏既有所有权约定，daemon 生命周期未定义

[BUILD_INSTALL_AND_RUNTIME.md](../../BUILD_INSTALL_AND_RUNTIME.md) 对 Codex 明确约定：
"MacBridge supervisor 仍只管理 `cccode-bridge-runtime`，不负责创建或常驻这个外部服务"。
方案 §5.2 选择在 Swift supervisor 层调用 `opencode service start`，让 Mac app 成为外部
opencode daemon 的启动者，与该约定冲突。

`opencode service` daemon 是长驻进程，方案未定义：

1. **跨 MacBridge 重启**：daemon 已在跑、pid 有效时，复用还是 restart？`service start` 是否幂等？
2. **跨 MacBridge 退出/卸载**：daemon 继续跑（孤儿）是否可接受？谁停？
3. **daemon 不健康**：§5.2 只说 `validate()`，未说 validate 失败时是 `service restart` 还是直接 unavailable。
4. **并发冲突**：已有 `opencode serve` 或 Desktop sidecar 在跑时行为未定义。

**要求：** §5.2 增加"daemon 生命周期与所有权"小节；在两份活文档标注 OpenCode 为 supervisor
所有权约定的**有意例外**。（若按 B1 出路 1 重写、不再用 `service start`，则本条大幅简化。）

### M3 — 与既有 OpenCode 凭据来源（LaunchAgent 复用 / credentials.json）未协调

[GO_BRIDGE_ARCHITECTURE.md](../../GO_BRIDGE_ARCHITECTURE.md) 与 [BUILD_INSTALL_AND_RUNTIME.md](../../BUILD_INSTALL_AND_RUNTIME.md)
描述当前凭据模型："优先复用现有 `com.opencode.server` LaunchAgent 凭据，否则生成随机凭据写入
`credentials.json` 并同步 OpenCode Desktop 配置。" `credentials.json` 在 app data dir 实测存在（0600）。

方案 §5 引入 `service_discovery` 后，凭据来源变成 daemon 的 `password` 文件，全程未提：

1. 既有 LaunchAgent 复用路径在 `service_discovery` 下被取代、保留还是优先？
2. 存量用户的 `credentials.json` 如何迁移/失效？（[AppDependencies.swift](../../MacBridge/MacBridge/App/AppDependencies.swift)
   `readCredential/writeCredentials` 约 239–263 行是否仍参与）
3. 模式切换时凭据来源如何切换。

**要求：** 新增"凭据来源与迁移"小节，理清 `password` 文件 / `credentials.json` / LaunchAgent
三者优先级与互斥。（若按 B1 出路 1 重写，凭据模型基本延续现状，本条反而是最现实落点，需重点写清。）

### M4 — "64667 = OpenCode Desktop" 的所有权假设与本机实况不符，影响验收

[BUILD_INSTALL_AND_RUNTIME.md](../../BUILD_INSTALL_AND_RUNTIME.md) 端口表把 64667 标为
"所有者：OpenCode Desktop"。方案 §1/§2 也以"Desktop 的 vlocal sidecar"为论证基础。本机实况：

```
$ lsof -nP -iTCP -sTCP:LISTEN | grep opencode
opencode  1589 ... TCP *:64667 (LISTEN)          ← opencode serve，由旧 unified bridge 启动
$ ps -p 1589 -o command=
/opt/homebrew/bin/opencode serve --hostname 0.0.0.0 --port 64667 --print-logs
$ ps -p 1575 -o command=    # 父进程
/opt/homebrew/bin/node /Users/jacklee/Projects/opencodeIosNew/bridge/src/start-unified.mjs
```

即本机 64667 的真正占用者是**旧一体仓库 `opencodeIosNew` 的 unified bridge 启动的
`opencode serve`**，而非 OpenCode Desktop（Desktop 1.17.13 的 sidecar 在 `127.0.0.1:53603`）。

**影响：**

- 验收标准 #3 "不再出现 CordCode 自动注入的 64667" 会与这个**外部遗留进程**混淆；
- `--hostname 0.0.0.0` 把 OpenCode 暴露到所有网卡，与方案 §8 "只允许 loopback" 冲突，切换前应清掉；
- BUILD_INSTALL_AND_RUNTIME.md 端口表 64667 所有权描述需订正。

**要求：** T06 前增加环境前置清理；订正端口表。

---

## 3. 次要与准确性问题（Minor）

| ID | 位置 | 问题 | 建议 |
| --- | --- | --- | --- |
| m1 | §2 "OpenCode 官方能力" | `/global/*`、`/session/*` 端点在 1.x 源码里属实，但 stable `opencode serve`（无 `--register`）是否就是同一套、是否需 Basic Auth，方案未在本机 serve 上实测。 | 选定 B1 出路后，对实际要用的 server 入口做一次端点 + 认证实测。 |
| m2 | §5.4 / §3 | "用户名固定为 opencode"——`ServerReadyData.username` 允许 `null`，`daemon.ts` 只确认了 password 生成，username 字面值未核对。 | 不硬编码；从实际 server 响应读取 username。 |
| m3 | §5.4 | 把 service password 明文写入 `opencode.global.dat`。 | 注明该文件期望权限，确认 Desktop 不回写到日志/崩溃报告。 |
| m4 | §2 / §5.7 | stable `opencode serve` 已有 `--mdns`（默认 hostname 0.0.0.0）。方案未提该机制就否定"自管端口"。 | 在"不采纳"补一句：mDNS 绑 0.0.0.0，与 §8 loopback-only 冲突，故不取。 |
| m5 | §2 / §9 | "v1.17.13 per-server state/session tabs 隔离"——源码只找到 per-server `projects`/`lastProject` store，session 级 tab 隔离未精确坐实。 | 降级为"per-server 项目/会话作用域"。 |
| m6 | §5.3 | "macOS 默认 `~/.local/state/opencode`"——实测属实（目录存在并被 CLI 使用）。 | 保留；可加"以本机实测为准"。 |
| m7 | 整体 | 方案分析源码 HEAD（dev 分支，`describe github-v1.2.25-987-g...`），但 stable 发行物并非 HEAD 的完整命令面。 | 凡引用"官方能力"，区分"源码里有"与"stable CLI 暴露"，以二进制实测为准——B1 的根因教训。 |

---

## 4. 与架构活文档及仓库约束的契合度

| 约束来源 | 契合度 | 说明 |
| --- | --- | --- |
| [GO_BRIDGE_ARCHITECTURE.md](../../GO_BRIDGE_ARCHITECTURE.md) hybrid 路由矩阵 | ✅ | §5.6 不改 wire protocol、不动 proxy/SSE 分工。 |
| 同上"控制面 secret 不进 agent 子进程" | ✅ | §8 与现状（`clearControlPlaneEnv` 清除 `OPENCODE_SERVER_*`）一致。 |
| 同上"OpenCode 凭据由 MacBridge 管理" | ⚠️ | 见 M3。 |
| [BUILD_INSTALL_AND_RUNTIME.md](../../BUILD_INSTALL_AND_RUNTIME.md) "supervisor 只管 cccode-bridge-runtime" | ❌ | 见 M2。 |
| 同上端口表 64667=OpenCode Desktop | ❌ | 见 M4。 |
| CONTRIBUTING "不加 mock/fallback" | ✅ | §1/§4/§9 反复强调真实失败。 |
| CLAUDE.md "改 agent driver/runtime 须先读活文档" | ✅ | 引用了两份活文档概念。 |
| CLAUDE.md "UI 自动化/真机需 owner 授权" | ✅ | §4/T06 不跑 UI automation。 |
| CLAUDE.md "Go/Swift 改完须 Release 构建覆盖安装" | ✅ | T06 含完整覆盖安装与核对。 |

---

## 5. 完整性缺口（方案未覆盖）

1. **stable CLI 能力未实测**：见 B1。方案的"官方能力"全来自源码阅读，没有一条经 stable 二进制实测。
2. **daemon 生命周期**：见 M2。
3. **凭据迁移**：见 M3。
4. **并发与冲突**：已有 `opencode serve` / Desktop sidecar / 旧 unified bridge 并存时（本机正是）`service start` 行为未定义。
5. **回滚/逃生口**：discovery 上线后若回归如何禁用？`custom_http` 存在但无用户侧开关（如 `OPENCODE_FORCE_CUSTOM_URL`）。
6. **Desktop 未安装**：§7 有分支，但未说配置同步函数在 `~/Library/Application Support/ai.opencode.desktop/` 不存在时是否安全跳过。
7. **国际化**：T05 改 Localization.swift，未列具体 key 与中英文文案。

---

## 6. 优点（值得保留）

1. **资料采纳规则（§0）清醒**：以源码为第一真相、列出"不成立的外部结论"。
2. **非目标（§4）到位**：不抓 IPC/内存/日志密码、不并发写同一 state、不引入 mock/fallback、不跑 UI automation。
3. **安全约束（§8）方向正确**：loopback-only、fail-closed、secret 不进 argv/日志。
4. **保留 hybrid 路由与 wire 协议不变（§5.6）**：改动收敛在 discovery + 配置层，风险面可控。
5. **对 Desktop sidecar 的源码引用准确**：§2 对 `vlocal`（随机端口 + `randomUUID()` + IPC-only + `type:"sidecar",variant:"base"`）的描述核对全部属实——"为何不能直接复用 Desktop sidecar"论证扎实。
6. **失败模式表（§7）**：覆盖主要分支（需补 B1 对应行）。

---

## 7. 进入实现前的最小修订清单（按优先级）

1. **【B1，必做】** 在三条出路中选一条并写进方案顶部：(1) 去 `service` 依赖重写 discovery；
   (2) 切 `lildax`/2.0-preview 并重核 v2 API；(3) 等 `service` 进 stable。
2. **【B1 补】** 凡方案引用的"opencode 能力"，改用本机 stable 二进制 `--help` 实测结果为准，不再只引源码。
3. **【M1】** T02 显式说明 RuntimeManager argv 新增 `-opencode-url`；user/pass 仍走 env。
4. **【M2】** §5.2 增"daemon 生命周期与所有权"（若走出路 1 则简化）；活文档标注 OpenCode 为 supervisor 约定的有意例外。
5. **【M3】** 新增"凭据来源与迁移"小节，理清 `password` 文件 / `credentials.json` / LaunchAgent 优先级与互斥。
6. **【M4】** T06 前清理旧 unified bridge 进程、释放 64667；订正 BUILD_INSTALL_AND_RUNTIME 端口表。
7. **【m1–m7】** 次要项择机处理；m1/m2 在实际选定的 server 入口上补抓证。

---

## 8. 一句话总结

**方向对、Desktop 源码引用扎实；但方案的核心 discovery 机制（`opencode service start` +
读 password 文件）只存在于 dev 源码与 2.0-preview（`lildax`）轨道，不在任何 stable
`opencode`（含 1.17.13）里。升级无法解决——必须先在"去依赖重写 / 切 2.0-preview /
等 stable" 三条出路里选一条并改写方案，再谈实现。**

---

## 9. 证据附录

### A1. 升级后的 stable opencode（1.17.13）命令面

```
$ readlink /opt/homebrew/bin/opencode
../lib/node_modules/opencode-ai/bin/opencode.exe          ← npm opencode-ai@latest

$ /opt/homebrew/bin/opencode --version
1.17.13

$ /opt/homebrew/bin/opencode service --help
（回退顶层 help —— service 非已知命令）

$ /opt/homebrew/bin/opencode serve --help | grep -iE 'register|mdns|port|hostname'
--port / --hostname / --mdns / --mdns-domain              ← 无 --register
```

顶层命令（与旧 1.4.3 一致，无 `service`）：completion / acp / mcp / [project] / attach /
run / debug / providers / agent / upgrade / uninstall / serve / web / models / stats /
export / import / github / pr / session / plugin / db。

### A2. 两条 npm 轨道（同名版本 1.17.13，不同 CLI）

| npm 包 | bin 名 | self-description | 有 `service`? | `serve` 性质 |
| --- | --- | --- | --- | --- |
| `opencode-ai@latest`（=本机升级目标） | `opencode` | 1.x TUI/CLI（yargs） | ❌ | `starts a headless opencode server`（无 `--register`） |
| `@opencode-ai/cli@latest` | `lildax` | **"OpenCode 2.0 preview"**（clap/cobra 风格） | ✅ | **"Start the v2 API server"**（端点待重核） |

两包同版本号 1.17.13、同仓库 `github.com/anomalyco/opencode`、同 maintainer（thdxr /
adamelmore，SST/opencode 团队，GitHub Actions OIDC 12h 前发布）。`lildax` 是 2.0-preview
代号；`service`/`--register` 机制只在这条轨道暴露。

### A3. 源码 `daemon.ts` 存在但未进 stable 命令面

```
$ git -C /Users/jacklee/Projects/opencode branch --show-current
dev
$ git -C /Users/jacklee/Projects/opencode describe --tags
github-v1.2.25-987-ga39db9c6f
$ git -C /Users/jacklee/Projects/opencode cat-file -e v1.17.13:packages/cli/src/services/daemon.ts && echo IN_TAG
IN_TAG                                                    ← 源码文件在 v1.17.13 tag 内
# 但 opencode-ai@1.17.13 二进制不暴露 service —— 见 A1
```

### A4. state 目录实测

```
$ ls "$HOME/.local/state/opencode"
locks/  model.json  prompt-history.jsonl
# 当前无 server.json/password（stable CLI 不支持 service/serve --register，不会产生）。
# 结论：方案 §5.3 的 ~/.local/state/opencode 路径假设成立，但 password 文件不会被 stable CLI 产生。
```

### A5. 64667 实际占用者（非 Desktop）

```
$ lsof -nP -iTCP -sTCP:LISTEN | grep opencode
opencode 1589 ... TCP *:64667 (LISTEN)              ← opencode serve，旧 unified bridge 启动
OpenCode 13672 ... TCP 127.0.0.1:53603 (LISTEN)     ← Desktop 1.17.13 sidecar（随机端口）
$ ps -p 1589 -o command=
/opt/homebrew/bin/opencode serve --hostname 0.0.0.0 --port 64667 --print-logs
# 父进程 1575 = node /Users/jacklee/Projects/opencodeIosNew/bridge/src/start-unified.mjs
```

### A6. CordCode 现状引用核对（均属实）

| 方案引用 | 结果 | 证据 |
| --- | --- | --- |
| go-bridge `-opencode-url` 默认 64667 | ✅ | [main.go](../../go-bridge/main.go):35 |
| agent/opencode fallback 64667 | ✅ | [opencode.go](../../agent/opencode/opencode.go):74–80 |
| RuntimeManager 固定写 64667 到 Desktop 配置 | ✅ | [RuntimeManager.swift](../../MacBridge/MacBridge/Services/RuntimeManager.swift):727–744 |
| RuntimeManager **不**显式传 `-opencode-url`（与 §5.5 表述相悖，见 M1） | ✅ | argv 276–298 行无该 flag；user/pass 走 env 301–313 行 |
| `agent_descriptor.go` 空 URL 时 dial 64667 | ✅ | [agent_descriptor.go](../../go-bridge/agent_descriptor.go):206,251–284 |
| 控制面 env 清除 / 不进 agent 子进程 | ✅ | [main.go](../../go-bridge/main.go):70,723–747；[core/message.go](../../core/message.go):42–52,134–141 |
| `credentials.json` 存在（0600） | ✅ | [AppDependencies.swift](../../MacBridge/MacBridge/App/AppDependencies.swift):239–263 |
| T02 接入点 `AppDependencies.swift` | ✅ 合理 | 同文件 76–98 行组装 RuntimeConfig |
| T05 待改 `docs/backends-and-config.md`、`Localization.swift` | ✅ 均存在 | — |

### A7. OpenCode 源码引用核对（1.x 源码层，均属实）

| 方案引用 | 结果 | 证据（`/Users/jacklee/Projects/opencode`） |
| --- | --- | --- |
| `daemon.ts` 含 `server.json`/`password`/`serve --register`/0600 | ✅（源码层） | `packages/cli/src/services/daemon.ts`:40-41,48-53,122 |
| Desktop 端口=OPENCODE_PORT 或随机 loopback，密码=`randomUUID()` | ✅ | `packages/desktop/src/main/index.ts`:304-329 |
| renderer 注册 `type:"sidecar",variant:"base"` | ✅ | `packages/desktop/src/renderer/index.tsx`:386-395 |
| Desktop 总是启动 sidecar | ✅ | `main/index.ts`:304-369 无条件 |
| HTTP 端点 health/event/session/... | ✅（1.x 源码层） | `packages/opencode/src/server/routes/instance/httpapi/groups/{global,session}.ts` |
| `opencode.global.dat` schema + `defaultServerUrl` | ✅ | `packages/app/src/context/server.tsx`；`packages/app/src/entry.tsx`:14 |

> A7 的"属实"指 **1.x 源码层**。`daemon.ts` 与 `/global/*` 端点是否在 stable `opencode` 二进制中可用，见 A1/A2——结论是不能（B1）。
