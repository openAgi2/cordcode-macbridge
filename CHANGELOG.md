# Changelog

本文件记录 CCCode MacBridge 的对外可见变更，按 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 惯例组织，最新在前。

技术细节与文件级证据见同目录下每轮的 `docs/YYYY-MM-DD-<主题>完成情况.md` 及对应审计报告；本 CHANGELOG 面向使用者/维护者，记录「改了什么 / 有何提升」，不重复罗列实现细节。

版本号对齐 MacBridge Release 构建的 `MARKETING_VERSION`（见 `MacBridge/project.yml`）。日期为协调世界时（UTC）。

## [Unreleased]

### 2026-06-19 — Relay 凭据迁出钥匙串，消除重装后授权弹窗

**问题**：每次重装 MacBridge 后打开 App，macOS 弹出「CCCode Bridge 想要使用你储存在钥匙串的机密信息」并要求输入登录密码。根因是 App 走 ad-hoc 签名，钥匙串按代码签名 / Team ID 授权访问，重装后 Team ID 变化即判定为陌生应用、触发授权弹窗。这对「下载即用」的普通用户是不可接受的体验。

**改动**：Relay 的三份密钥（route credential、activation install id、activation 签名私钥）从钥匙串迁出，改用文件存储，与 OpenCode `credentials.json` 同目录（`~/Library/Application Support/CCCode Bridge/relay-secrets/`）、同样 `0600` 权限。

- **提升**：重装 / 升级后不再弹钥匙串授权窗口；无需开发者证书或稳定 Team 签名。文件存储的 0600 保护对「丢了可重新 provisioning」的 relay route credential 安全性足够。
- **一次性迁移**：存量用户首次启动新版时，若文件不存在且旧版钥匙串条目还在，自动读取旧值、写入文件、删除钥匙串条目——凭证无缝继承，不会因迁移丢值而触发重新 provisioning（后者曾导致 iOS 配对的端到端凭证与 Mac 端不一致、显示离线）。迁移为尽力而为：钥匙串读取失败（含用户拒绝授权）时不阻塞，直接新生成凭证。
- **全新安装无影响**：没有旧钥匙串条目的用户从头走文件存储，行为与旧版等价。

### 2026-06-19 — 深度运行期 Code Review 修复（commit `a85adf1f613e`）

本轮按 `docs/2026-06-19-deep-runtime-implementation-plan.md` 完成 11 项运行期修复（T01–T11），经独立审计（`docs/2026-06-19-implementation-完成情况-审计报告.md`）逐行反查源码 + 独立复跑全部测试通过。覆盖安全、进程治理、Relay 背压、跨进程契约、稳定性五类。

#### 安全
- **控制面凭据不再泄漏进 agent 子进程（T01）**：agent（Claude/Codex/OpenCode）及其工具子进程的环境从「全量继承 `os.Environ()`」改为 deny-list（`CCCODE_*`/`OPENCODE_SERVER_*`/`CLAUDECODE`）+ 运行时 allowlist 双保险。stderr 在进入日志/错误帧前统一脱敏。
  - **提升**：远程设备无法再通过 agent 工具（如 shell）读取到 go-bridge 的管理 token、relay 凭据，消除了从 data-plane 向 loopback 控制面横向移动的风险。
- **配对限流 bucket 无界增长治理（T08）**：新增惰性 TTL 清理 + 全局容量上限（4096），超限对新 key fail-closed。
  - **提升**：任意 pairingId/IP 无法制造无界内存增长。

#### 稳定性 / 进程治理
- **runtime shutdown 不再泄漏 agent 子进程（T02）**：新增幂等、deadline 约束的 `Handlers.Shutdown`，并发关闭所有活跃 session；main 的关停顺序修正为 HTTP Server → handlers.Shutdown → CloseAllConnections → relay/tls/mgmt。
  - **提升**：SIGTERM / 睡眠唤醒 / 长跑后不再残留 agent 子进程。
- **连接 Close 超时不再触发 events channel panic（T03）**：四条订阅路径（opencode/codex/sse/appserver）对齐范本——超时分支绝不直接 `close` channel，改由延迟 goroutine 等 producer 退出后再关。
  - **提升**：消除「连接 Close 超时后 producer 仍发事件 → closed-channel send panic 崩溃」。
- **Claude 改为进程组回收（T02）**：新增 `Setpgid` + 进程组 kill，对齐 codex。
  - **提升**：Claude 的 sudo/shell/插件子进程不再在 shutdown 后残留。
- **修复 Codex 重连测试自身数据竞争（T11）**：`closeCount` 改 `atomic.Int32`。
  - **提升**：`-race -count=20` 稳定通过，修掉实跑复现的 DATA RACE。

#### Relay 背压（独立 module，已部署生产 `wss://relay.byteseek.uk:8443`）
- **per-device 有界发送队列，消除跨 device 队头阻塞（T04）**：每个 device 一个有界队列（256 帧 / 8 MiB）+ 专用 writer goroutine；bridge 的投递从同步 write 改为非阻塞 enqueue，队列满则断开慢 device 并把当前帧落入 mailbox（不丢）。
  - **提升**：原先一个卡住的 device（满 TCP 窗口）会阻塞**同 route 所有 device** 的投递；现在慢 device 只断开自己，正常 device 照常收帧。

#### 跨进程契约（Swift）
- **连续 restart 收敛为单次启动（T05）**：`launchGeneration` + 可取消 `restartTask` + `applyConfigAndRestart`；100 ms 内连续多次 restart（配置变更 + Relay provisioning 回调）收敛为单次进程启动。
  - **提升**：不再端口反复接管 / ready frame 抖动。
- **runtime.json / management-token 写失败 fail-fast（T06）**：`WriteReadyFrame` 返回 error，写失败时发布 `bootstrap_persist_failed` + exit，绝不发布 ready。
  - **提升**：磁盘满 / 权限错误时不再进入「网络已开放但 UI 永远未就绪」的假死态（原先每 60 s 重启）。
- **management API 客户端短超时 + 轮询解耦（T07）**：专用 ephemeral `URLSession`（request 2 s / resource 5 s）替代 `URLSession.shared`；status 决定存活状态、agents 刷新改为独立低优先级任务。
  - **提升**：management 半开（accept 连接不响应）时 supervisor ≤ 5 s 进入恢复，而非卡死数十秒。

#### 架构债务（非用户可见，为后续铺路）
- **Handler 生命周期组件可整体关闭（T09）**：`ObservationManager` 的 lease loop 从构造函数移到显式 `Start(ctx)`；`StartCleanupLoop` 改可停 ticker。测试不再泄漏 goroutine。
- **god-object 最小治理（T10）**：新增实例级 `ConfigRepository`，旧包级全局标注 `Deprecated`。为后续拆分 `handlers.go` / `config.go` 铺路，本轮不拆大文件。

#### 前后对比

| 维度 | 之前 | 之后 |
| --- | --- | --- |
| 安全边界 | agent 可继承控制面密钥 | deny-list + allowlist 双保险，stderr 脱敏 |
| 进程生命周期 | shutdown 泄漏子进程、events panic | 统一 shutdown + 进程组回收 + 安全 close |
| Relay 可用性 | 单慢 device 拖垮整条 route | per-device 隔离背压 |
| Swift 状态机 | 双 restart、假就绪、mgmt 卡死 | generation 收敛 + fail-fast + 短超时 |
| 可测试性 | 全局状态、race、goroutine 泄漏 | 实例注入、atomic、显式 Start |

#### 验证
- `go build` / `go vet ./...` + relay-server build/test/race + Swift `xcodebuild build` 全绿
- 11 项定向测试全通过，无新增回归（pre-existing 失败：未装 codex CLI、`AvailableModels` 时序 flaky，已 baseline 确认与本轮无关）
- 完成：`docs/2026-06-19-deep-runtime-implementation-plan完成情况.md`；审计：`docs/2026-06-19-implementation-完成情况-审计报告.md`

---

> **维护说明**：后续每轮工作请在最上方（`[Unreleased]` 下）按相同结构追加一节，标题为「日期 — 主题（commit）」。发布正式版时把 `[Unreleased]` 改为对应版本号与日期，再新开一个 `[Unreleased]`。
