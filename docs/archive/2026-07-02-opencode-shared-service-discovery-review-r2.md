# 第二轮评审：OpenCode 共享本地服务接入方案（修订版）

> 评审对象：[docs/2026-07-02-opencode-shared-service-discovery-plan.md](../2026-07-02-opencode-shared-service-discovery-plan.md)（已按第一轮评审修订为 stable-compatible 分阶段方案）
> 评审日期：2026-07-02
> 评审人：Claude（Opus 4.8）
> 评审基线：源码核对 + 本机运行态实测（含本次新增的 `opencode serve` 认证实测，见 §5）
> 第一轮评审：[docs/2026-07-02-opencode-shared-service-discovery-review.md](2026-07-02-opencode-shared-service-discovery-review.md)

## 0. 总体结论

**修订大幅到位，Phase A 在 stable 1.17.13 上经实测确认可行；可进入实现。** 第一轮的阻断级
B1 与主要 M1–M4 全部闭环。第二轮新发现 2 个主要问题（共享 server 持久化/归属、存量用户升级
默认行为）和若干次要项——都是"落地质量"层面，不影响 Phase A 的可行性，但建议进入实现前补齐，
否则会变成"能用但脆弱 + 升级静默断 OpenCode"。

| 维度 | 第一轮 | 第二轮 |
| --- | --- | --- |
| 默认路径在 stable opencode 可行性 | ❌ 阻断 | ✅ **Phase A `external_http` 实测可行**（见 §1） |
| B1：`service`/`--register` 不在 stable | 阻断 | ✅ 已降级为 `service_discovery_future`，门控条件清晰 |
| M1：RuntimeManager 不传 `-opencode-url` | 主要 | ✅ §5.3/T03 显式新增 argv，理由正确 |
| M2：supervisor 接管外部 daemon | 主要 | ✅ Phase A 不启动/不常驻外部进程，与活文档约定一致；daemon 生命周期推迟到 Phase B |
| M3：凭据来源与迁移 | 主要 | ✅ 新增 §5.5，三类来源与迁移规则覆盖 |
| M4：64667 实为旧 unified bridge | 主要 | ✅ §2/§4/T07 记录并加前置清理；loopback-only 写入 §8 |

---

## 1. Phase A 凭据模型经实测成立（第一轮 m1/m2 一并闭环）

第一轮 m1 要求"对 stable `opencode serve` 做端点 + 认证实测"。**本次评审实测通过**（绕过 alias，
对 `/opt/homebrew/bin/opencode` 1.17.13，loopback，5s 后关闭并清理）：

```
$ OPENCODE_SERVER_PASSWORD='testpass-round2' opencode serve --hostname 127.0.0.1 --port 41523 --print-logs
opencode server listening on http://127.0.0.1:41523

GET /global/health  (no auth)        → HTTP 401
GET /global/health  (opencode:testpass-round2) → HTTP 200 {"healthy":true,"version":"1.17.13"}
GET /global/health  (opencode:wrongpass)        → HTTP 401
```

结论：

- stable `opencode serve` **确实接受 `OPENCODE_SERVER_PASSWORD` 环境变量**作为 Basic Auth 密码；
- username **确为 `opencode`**（闭环第一轮 m2，方案 §3/§5.4 硬编码 `opencode` 正确）；
- `/global/health` 行为与方案 §5.2 校验要求（401→`auth_failed`、200+`healthy/version`→通过）
  **完全一致**；
- `--hostname 127.0.0.1 --port <p>` 正常绑定 loopback。

**这把 Phase A 从"理论上可行"升级为"实测可行"，是本次评审最大的去风险点。** 方案的 T02
health/auth 校验、§5.3 argv、§5.4 Desktop 同步、§5.5 凭据来源，都建立在这个契约上，现在它被
经验确认。

> 唯一未实测的相邻点：**未设 `OPENCODE_SERVER_PASSWORD` 时 `opencode serve` 的行为**（随机密码？
> 无认证？拒绝启动？）。方案所有示例都带该 env，等于隐式契约"server 必须带密码启动"。建议在
> T06 文档里写明这条契约，并提示"不设密码的具体后果"（见 R2-6）。

---

## 2. 第一轮阻断/主要问题的闭环确认

- **B1 ✅**：出路 1（去 `service` 依赖）已采纳。`external_http` 为 Phase A 默认；
  `service_discovery_future` 为 disabled/future，§3 Phase B 给出 4 条激活前置条件（命令面、
  `--register`、文件路径权限、端点兼容），均要求"本机实测"。门控严谨。
- **M1 ✅**：§5.3/T03 明确 RuntimeManager argv 新增 `-opencode-url <url>`，user/pass 走 env；
  理由"URL 非 secret 可进 argv、password 是 secret 不进 argv"正确，且与既有
  `clearControlPlaneEnv()`/agent env deny-list 衔接。
- **M2 ✅**：§5.2 明确 Phase A"不启动或常驻 OpenCode 外部进程，只连接已存在 server，与 supervisor
  只管 cccode-bridge-runtime 的活文档约定一致"。daemon 生命周期推迟到 §10.1 future 项。──这是
  正确的取舍：先把数据面接通，daemon 归属留待 Phase B 再定。
- **M3 ✅**：§5.5 列出三类凭据来源与 external_http 规则、`credentials.json` 向后兼容、
  legacy→external 迁移、future service_discovery 独立凭据。覆盖到位。
- **M4 ✅**：§2 实测记录 64667 占用者、§4 非目标、T07 前置清理命令、§8 loopback-only。

---

## 3. 第二轮新发现的主要问题

### R2-1 — 共享 server 的持久化与归属未定义（Phase A 最大落地缺口）

Phase A 的核心契约是"CordCode/iOS/Desktop 都连同一个用户/运维启动的 `opencode serve`"。
§5.2 正确地不让 MacBridge supervisor 启动它（M2 的解法），但这意味着**没人负责让这个共享
server 活着**：

- T06/§5.7 给的启动命令 `opencode serve ...` 是**前台命令**——关终端/重启就死；
- server 一死，CordCode 和 Desktop（默认 server 已被改写到同一 URL）**同时**失去 OpenCode backend；
- BUILD_INSTALL_AND_RUNTIME 对 Codex 同类问题给了 LaunchAgent 指南，本方案没有对应物。

这不是正确性或安全 bug，但是个**可操作性大坑**：用户按方案配好一切，重启后 OpenCode 整体消失，
且 iOS/Desktop 两端同时报错，归因困难。

**要求（进入实现前补一条即可）：**

1. 在 T06 增加持久化指引：要么给出 LaunchAgent plist 模板（参照 BUILD_INSTALL_AND_RUNTIME 的
   Codex 模式：`command -v opencode` 取路径、不提交个人路径/token、只允许一个 listener），
   让共享 server 开机自启；
2. 要么在方案里**显式声明**"Phase A = bring-your-own-server，持久化由用户负责；managed daemon
   属于 Phase B"，并在 Settings 文案与验收标准里写明这一限制，避免给人"装好就稳定共享"的错觉。

### R2-2 — 升级时存量用户的默认行为不明确（迁移完整性 / 防静默回归）

当前产品态：RuntimeManager 不传 `-opencode-url`，go-bridge 默认 `64667`，Desktop 配置写 64667。
即**存量用户的 OpenCode 默认是"能用"的**（不管 64667 上实际是什么）。

修订后 §3 step 6 + §5.5："若未配置 endpoint，OpenCode backend 为 `not_configured`，**或者**
用户显式选择 `legacy_64667`。"——这个"**或者**"是歧义的：**升级后存量用户默认落在哪边？**

- 若默认 `not_configured`：升级 CordCode 后，存量用户的 OpenCode backend 静默变 unavailable，
  iOS 端 OpenCode 直接消失。**回归。**
- 若默认 `legacy_64667`：行为连续，但要写明是一次性迁移，并提示用户迁到 `external_http`。

方案没有定义这条升级默认路径。这是比 R2-1 更隐蔽、更可能咬人的迁移隐患。

**要求：** §5.5 增加一段"升级默认"：首次启动新版本时，若检测到既有 `credentials.json` 但用户
未显式选 source，**自动落到 `legacy_64667`**（保持行为连续），并在 Settings/日志出一次性提示，
引导迁移到 `external_http`；全新安装默认 `not_configured`。T01/T03 测试要覆盖这条迁移分支。

---

## 4. 第二轮次要问题

| ID | 位置 | 问题 | 建议 |
| --- | --- | --- | --- |
| R2-3 | §7 失败模式表 | 缺"Desktop 已在运行"分支：改写 `opencode.global.dat`+`defaultServerUrl` 不会切换 Desktop **当前活动** session，要等 Desktop 重启或用户手动切。用户可能误以为已共享。 | 补一行：Desktop 已运行时，配置下次生效或需手动重启/切换；验收标准 #10 已隐含，但失败模式表要说清。 |
| R2-4 | T07 验证命令 | `-only-testing:CordCodeLinkTests/OpenCodeEndpointResolverTests` 只跑一个测试类；但 T01–T04 还引入 health/auth 测试与 `MacBridgeBehaviorTests` 用例。 | T07 改为跑全部新增/相关 OpenCode 测试类，或显式列出 `-only-testing:` 多个 target。 |
| R2-5 | §5.4 / §1 | 同时把 `opencode serve` 与 `opencode web` 列为"共享 server 启动方式"。`web` 会开浏览器，比 `serve` 重，且 §4 非目标已说不在默认路径启动 `web`。 | 统一：canonical 启动只用 `opencode serve`；`web` 仅作为 §5.7 手动 attach 验证的可选项，不在 §1 列为 server 来源。 |
| R2-6 | §5.7 / T06 | 未设 `OPENCODE_SERVER_PASSWORD` 时 `opencode serve` 行为未实测、未文档化。 | T06 写明契约："server 必须以 `OPENCODE_SERVER_PASSWORD` 启动"；补一句不设密码的后果（实测后填）。 |
| R2-7 | §5.5 | "首次启动仍可复用旧 `com.opencode.server` LaunchAgent 凭据"——该 LaunchAgent 在当前部署里基本不出现（本机就没有）。 | 标注该路径为 legacy/少见，避免实现时过度投入；或明确其检测优先级。 |
| R2-8 | §3 step 2 | 允许 `http://localhost:<port>`。`localhost` 在某些机器解析到 `::1`（IPv6），而 `opencode serve --hostname 127.0.0.1` 只听 IPv4 loopback。 | 校验/连接统一用 `127.0.0.1`，或在 normalize 时把 `localhost`→`127.0.0.1`，避免 v4/v6 不匹配导致 health 不可达。 |

---

## 5. 仍建议在实现中补的实测（非阻断）

1. **无密码启动行为**（R2-6）：跑一次不带 `OPENCODE_SERVER_PASSWORD` 的 `opencode serve`，记录
   health 是 200（无认证，安全隐患）还是随机密码（CordCode 拿不到，等同 vlocal 问题）还是拒绝启动。
2. **Desktop 改写生效时机**（R2-3）：实跑一遍——Desktop 运行中改 `opencode.global.dat`+`defaultServerUrl`，
   观察 active session 是否切换、何时切换。
3. **`/global/event` SSE 与 `/session/*`** 在 stable serve 上的实际行为，确认与 §5.6 hybrid 矩阵
   假设一致（第一轮 A7 只在 1.x 源码层核实过）。
4. **`agent_descriptor.go`** 空 URL 分支（T05）确保真的返回 `not_configured` 而非残留 dial。

---

## 6. 进入实现前的最小修订清单（按优先级）

1. **【R2-2，必做】** §5.5 定义升级默认：存量用户→`legacy_64667`（连续）+ 一次性迁移提示；
   新装→`not_configured`。T01/T03 加迁移分支测试。
2. **【R2-1，必做】** T06 增加共享 server 持久化方案（LaunchAgent 模板 或 显式 bring-your-own-server
   声明 + Settings 文案）。
3. **【R2-3】** §7 补"Desktop 已运行"分支说明。
4. **【R2-4】** T07 验证命令覆盖全部相关测试类。
5. **【R2-5/R2-6/R2-7/R2-8】** 次要，择机处理。
6. **【§5 实测】** 实现过程中补做无密码启动、Desktop 改写时机、SSE/端点 三项实测。

完成 R2-1 与 R2-2 后，Phase A 即可进入 T01 实现。

---

## 7. 一句话总结

**修订把第一轮的阻断与主要问题全部闭环，Phase A `external_http` 经实测在 stable 1.17.13 上
凭据/认证/health 契约成立，可以动手；只剩"共享 server 谁让它活着"（R2-1）和"升级时存量用户
默认落哪边"（R2-2）两个落地问题，补上即可避免"能用但脆弱 + 静默回归"。**
