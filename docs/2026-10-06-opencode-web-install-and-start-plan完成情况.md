# 本轮任务完成情况：OpenCode Web 未安装可代装、未启动可点启动

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- Plan: `docs/2026-10-06-opencode-web-install-and-start-plan.md`（v2，r2 APPROVED；实施期登记 OD-1/OD-2=A 与 r2 建议 F-6/F-7 修订）
- Canonical State File: `.exec-plan/state/plan-50370998b41d.json`
- Legacy State File: none
- Completion Report Verdict: **proved-complete**
- Queue Summary: 23/23 todos done（20 个方案单元 todo + 3 个 owner 反馈触发的 review-fix todo），全部 verification present；tests 类证据在退出审计期重跑并升级 re-verified
- Related Commits: `0bacda228004`（功能实施）→ `6b928ca56e36`（live seam 修复）→ `e72568eb`（队列收口）→ `9e221e98f97c`（resolve 失败分支刷新修复）→ `b7b4a422`（review-fix 登记）→ `36302bf`（u8 前置解除）→ 本提交（u8 收口 + 报告）
- Generated At: 2026-10-06 20:58 (+08:00)

## 1. Overall Verdict (总体结论)

方案完整落地并通过端到端验收：OpenCode 行区分**未安装/未启动/就绪**三态，「安装」按钮走 npm 代装（`opencode-ai@1.18`，OD-2=A），「启动」按钮拉起 4096 段 managed server；go-bridge 描述符从布尔折叠切到结构化就绪（探针失败诚实报 `service_not_running`）；resolve 失败保留持久 endpoint。external_http/disabled 用户与 iOS 零改动（wire 枚举复用双仓既有值）。E-7 owner 验收通过（20:48 点「启动」→ PID 51404 服务起来 → 描述符 `available` → 行绿）。

证据分级：定向测试（go + Swift）在退出审计期全部重跑通过（re-verified）；两轮 Release 构建覆盖安装与运行态六项核验、E-5 真实形状复核、E-7 日志核对为 self-attested（命令输出已归档在队列 proof 的 artifacts 里）。实施期间发现并修复两个集成缺口（live 重读路径折叠、resolve 失败分支不刷新），均带回归测试。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| U1 go-bridge 结构化就绪 | proven-done | proven-done (re-verified) | n/a (justified) | done | readiness 全状态 7 用例 + 描述符切换 + 禁止回归；审计期重跑 ok |
| U2 resolve 持久 URL | proven-done | proven-done (re-verified) | n/a (justified) | done | 失败保留持久三元组/无状态文件留空/external_http 不覆盖；重跑 passed |
| U3 动作面 + 串行执行域 | proven-done | proven-done | n/a (justified) | done | 熔断重置/URL 变化 restart/未变不 restart/失败原文/冷启动互斥/.disabled 守卫/跨线程交错 7 用例 |
| U4 installer + record-first | proven-done | proven-done (re-verified) | n/a (justified) | done | argv 契约/prefix 回退/验证门/0600 记录/record-first/打码 8 用例 |
| U5 行 UI + L10n | proven-done | proven-done (re-verified) | proven-done | done | 8 行状态表逐行对拍（含 F-1 断言）+ 按钮矩阵 + 进行中态 + L10n 9 键；真实表面在 u7 构建上核验 |
| U6 CHANGELOG + 方案登记 | proven-done | proven-done (re-verified) | n/a (justified) | done | §8 收口测试组全绿 + 一次增量编译；OD 登记/F-6/F-7 修订 |
| U7 Release 交付 + 运行态核验 | — | — | proven-done | done | 两轮构建安装；六项核验（进程代际/8777/特征日志/描述符诚实/E-5 真实形状/无残留）；发现并修复 live seam 折叠 |
| U8 owner E-7 验收 | — | — | proven-done | done | owner 20:48 点「启动」；日志核对：PID 51404 spawn + 探针恢复 generation=1.18 + 描述符 available |
| review-fix（owner 反馈） | proven-done | proven-done | proven-done | done | resolve 失败分支刷新（9e221e9）+ 回归测试 + 重建部署核验 |

### 2.1 Upstream Anchors (上游锚点)

| Fix / todo | Upstream anchor (file:line) | First divergence vs upstream |
| --- | --- | --- |
| installer（U4） | `/Users/jacklee/Projects/opencode/README.md:53`（`npm i -g opencode-ai@latest` 官方入口 @ 2fa3363c92…） | CordCode 有意差异：钉 `opencode-ai@1.18` spec（OD-2=A owner 裁决——backend `clientFor` 只认 generation118 fail-closed，装 @latest 等于制造已知失败）；无 sudo/npx/sh -c、prefix 回退为 dsh 同构约束 |
| readiness seam（U1） | `agent/dsh-web/readiness.go:91`（in-repo 镜像 seam） | OpenCode 的判别序列按自身探针语义重写（URL 空/401/no-auth 200/v2 隔离/118），非 dsh 的 binary/port 判别——方案 §3/§6 论证的结构差异 |
| npm 发现 + nvm 扫描（U4） | `agent/dsh-web/binary.go:56-125`（findNpmBinary/latestNvmBinary） | Swift 侧改为搜索路径目录扫描（GUI 不继承 shell PATH，无 exec.LookPath 可用） |
| CLI record-first（U4） | `agent/dsh-web/binary.go:44-52`（findDSHBinary record-first 顺序） | 记录文件名/字段按 OpenCode 命名（opencode-install-record.json），顺序语义一致 |

## 3. Key File Changes (关键文件变更)

- `agent/opencode-web/readiness.go`（新）：StructuredInstanceReadiness seam——not_configured/service_not_running/available 三态，401 与 no-auth 200 专属文案，v2 隔离原文，失败探针每次重读重探
- `agent/opencode-web/probe.go`：probeResult 增 probeErrKind 分类（auth/unauthenticated/other），4 个返回点落位
- `agent/opencode-web/opencodeweb.go`：InstanceStatus 改为 seam 的布尔视图（单一判别序列防漂移）
- `go-bridge/agent_descriptor.go`：opencode-web case 切 detectStructuredInstanceReadiness
- `go-bridge/management_api.go`：liveAgentDescriptors 对 opencode-web 显式走结构化 seam（live 重读与 detectAgentStatus 路由一致；其他 backend 字节级不变）
- `MacBridge/.../RuntimeManager.swift`：resolve 失败保留持久 endpoint + 失败分支刷新行状态输入；OpenCodeSeatActionState @Published；startOpenCodeManagedServer（async 可等待、熔断重置、.disabled 守卫、URL 变化 restart/未变即绿）；installOpenCode（npm 代装后半段走显式启动）
- `MacBridge/.../OpenCodeManagedServer.swift`：串行执行域（专用 serial DispatchQueue，公开签名不变）；resetFailureLimit/currentState/persistedEndpoint；desktopConfigDir 测试注入
- `MacBridge/.../OpenCodeInstaller.swift`（新）：npm 发现（搜索路径+nvm）、install 流（global→prefix 回退→--version 验证门→0600 记录）、posix_spawn 进程组 runner（超时杀组）、打码
- `MacBridge/.../WorkspaceView.swift`：OpenCode 行三态文案/按钮矩阵/字幕/收口刷新（testAgent）
- `MacBridge/.../Localization.swift`：opencode_web_* 中英 9 键
- 测试：readiness_test.go、OpenCodeSeat{Resolve,Action,Row}Tests、OpenCodeInstallerTests、agent_descriptor_test.go 与 management_api_test.go 的切换/禁止回归断言
- `CHANGELOG.md`、方案文档（OD 登记 + F-6/F-7）、`.exec-plan/state/plan-50370998b41d.json`

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands: `GOSUMDB=sum.golang.org go test ./agent/opencode-web/... -count=1`；`go test ./go-bridge/ -run 'TestMgmtAgents|TestOpenCodeWeb|TestOpenCodeDescriptor|TestUnknownDescriptor|TestFullAgentCapabilities|TestBuildAgentDescriptor' -count=1`；`xcodebuild test-without-building -only-testing:<OpenCode 四套件 + 既有 OpenCodeManagedServer/EndpointResolver + DeepSeekSeatAction + WorkspaceView>`
- Result: 全绿（go：opencode-web 全包 ok 9.2s + 定向组 ok；Swift：8 套件全 passed）
- Attestation: **re-verified**（退出审计期 17:27-17:31 全部重跑）
- Main test files: `agent/opencode-web/readiness_test.go`、`MacBridge/MacBridgeTests/OpenCode{SeatResolve,SeatAction,SeatRow,Installer}Tests.swift`、`go-bridge/{agent_descriptor,management_api}_test.go`
- Artifact paths: 见队列 JSON 各 todo verification.artifacts

### 4.2 Regression evidence

- 两轮 Release 构建覆盖安装（`./scripts/build-unsigned-release.sh` → killall+pkill → cp -R /Applications → open）+ 部署后六项核验：进程代际（runtime 启动晚于构建）、8777 监听者为 /Applications 内嵌 runtime、新版本特征输出（探针重试行为 + runtimeCommit）、GET /internal/agents 描述符诚实、E-5 真实形状复核（临时实例：no-auth 401 / authed 200 / `/session` bare array = generation118）、无临时产物残留进程
- E-7 owner 验收（20:48）：PID 51404 `opencode serve --port 4096` spawn 成功、stderr 完整启动序列、状态文件新 pid、探针恢复 `generation=1.18`、描述符 `available`
- Attestation: self-attested（agent 执行并记录；owner 点按为人工动作）
- Artifact paths: 队列 JSON u7-integration-regression / u5-row-ui-regression / u8-owner-acceptance-e7 的 artifacts

### 4.3 Audit downgrade summary

- Downgraded todos: none（退出审计结构性检查 PASS，无降级）

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

1. **一次未解释的点击无动作**（18:2x 的那次点击）：无 spawn/testAgent/线程痕迹，静态分析未定位；后续点击（20:48）全链路正常。若复发，采集行按钮文案与字幕文字再查。
2. **codex-remote 的 live/静态描述符不一致**（既有，非本方案引入）：liveAgentDescriptors 对双 seam backend 走布尔折叠（codex-remote 的 pairing_required 等结构化状态只在 hello_ack 静态路径出现）。本方案为 opencode-web 显式路由，未动 codex-remote（dsh 方案期既定语义）。
3. **用户构建环境**：`go env GOSUMDB=off` 与 go.mod `toolchain go1.26.6` 冲突，裸 `go build` 与 Xcode prebuild 被阻断（基线即坏；本轮用命令级 `GOSUMDB=sum.golang.org` 覆盖，未改全局配置）。建议 `go env -w GOSUMDB=sum.golang.org`。
4. **既有测试失败**：`TestRelayEventsForwardsUserInputRequested`（relay 事件顺序断言）在评审基线 3fb49252 即失败，与本方案无关，未处理。
5. **owner 的 opencode 配置**：本轮经 owner 授权修复（limit.output=131072，models.dev 参考；备份 `opencode.json.bak-20261006`）。若 ctyun 网关实际上限更低，请求会报可见错误，届时调小。

## 6. Audit Focus (建议审核重点)

1. `liveAgentDescriptors` 的 opencode-web 显式路由（`go-bridge/management_api.go`）——确认其他 backend 的 live 行为字节级不变。
2. `startOpenCodeManagedServer` 的执行上下文（Task.detached + 串行域 + .disabled 守卫 + 冷启动互斥）——对照方案 §5 执行上下文专节逐项。
3. installer 的 argv 契约与记录文件权限（0600、无凭据）——`OpenCodeInstallerTests` 的断言即契约。
4. 行文本/按钮矩阵纯函数与方案 §3 八行状态表的逐行一致性（`OpenCodeSeatRowTests`）。

## 7. Constraints (关键约束)

- 不新增 wire 枚举/management 端点/协议字段（方案 §7）；iOS 零改动（E-6）。
- external_http/disabled 用户行为不变（无按钮、字幕透传）。
- 冷启动自动拉起保留（OD-1=A）；安装 spec 钉 `opencode-ai@1.18`（OD-2=A，owner 2026-10-06 确认）。
- 真实 npm 安装路径（u4）未在本轮验证——本机 opencode 已装（CLI found），「安装」按钮不出现；该路径的 argv/记录/回退契约由注入式测试锁定，真实安装留给未来未装机器的 owner 验收。
