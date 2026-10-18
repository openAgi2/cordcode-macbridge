# S-0 运行身份与阶段指标取证记录（Claude 长 session 投影打开优化 · 方案 v30）

日期：2026-10-18
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` v30（R25 APPROVED）
exec-plan：`.exec-plan/state/plan-b70583acde4f.json` → `s0-runtime-evidence`

## 0. P0 来源清单（门点 1：读取源码分析前生成）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=6a76d92e06b0191be9aa0363118235dc7b607694
未提交状态=干净
任务预期分支=feat/claude-long-session-projection-loading（本 worktree）
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading feat/claude-long-session-projection-loading b77faf3c579b571a9df86f6e3cbae3eacaa8a21e
预期产品特性=Claude 长 session 投影打开链路优化（S-0~S-8）

仓库路径=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=b77faf3c579b571a9df86f6e3cbae3eacaa8a21e（= feat/ios-native-message-timeline 已提交 HEAD；主 checkout main 同点）
未提交状态=干净
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading feat/claude-long-session-projection-loading 6a76d92e06b0191be9aa0363118235dc7b607694
```

方案文档正文 SHA-256 机械校验：`88dede7eeae620260550bf567c0a364493d936cff9064b579f41cbc455a4a388` ✅（切分规则 = `raw[:idx].rstrip(\n)`，idx 为 `\n---\n\n设计师交接块`）。

## 1. 正式 runtime 构建身份（vs 源码 HEAD）

| 项 | 值 | 取证方式 |
| --- | --- | --- |
| 安装路径 | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime` | `pgrep`/`lsof` |
| 内嵌 runtimeCommit | **`adfef51c13ff`**（= `adfef51c13ff49963267943e756d91c60f5af413`） | binary strings 孤立 12-hex |
| 内嵌 runtimeDate | `2026-10-17T06:48:53Z`（= 本地 14:48:53 +0800） | binary strings |
| adfef51c 提交 | `feat(workspace): overlay topology and put service controls on the title row`，2026-10-17 09:25:45 +0800，feat/ios-native-message-timeline 分支 | `git log` |
| 与源码 HEAD 差异 | HEAD `6a76d92e` = adfef51c + `11caa133`（fix workspace）+ `f868af18`（docs v9）+ `6a76d92e`（docs v30）——**后三者为 bugfix+纯文档**，runtime 相关 Go 源码与安装版一致（11caa133 是 Swift workspace 改动） | `git log adfef51c..6a76d92e` |
| ebab32f9 flag 翻转 | adfef51c **含** 2026-08-26 `SetProjectionWindowEnabled(true)` 翻转（HEAD 祖先链验证 ✓）；全部 6 个 Mac worktree 均含 | `git merge-base --is-ancestor` |
| 进程 | PID **8303**（PPID 8264 = CordCodeLink GUI），启动 Sat Oct 17 14:49:35 2026，运行 ~29h43m；**事故窗口（17:30-17:33）由同一进程服务** | `ps -o pid,ppid,lstart` |
| 8777 监听者 | `cordcode-bridge-runtime` PID 8303（/Applications 内嵌）✓；无非 /Applications 违规残留 | `lsof -nP -iTCP:8777 -sTCP:LISTEN` |
| Management API | `status=ready`，bridgeEpoch=347236777953071546，registeredDrivers=claude,codex-remote,dsh-web,grokbuild,opencode-web，version=0.1.0 | `/internal/status`（token 认证） |
| binary capability 符号 | `projection_window_v1`（3 处）、`session_sync_v2`（4 处）、`relay_gzip_v1`（1 处）、`turn_detail_lazy_v1`（3 处）均存在 | `strings` 子串匹配 |

结论：正式 runtime 含 window 实现且 echo 开启；**运行时身份 ≠ 源码 HEAD**（差异为 1 个 Swift fix + 2 个 docs commit，Go 源码一致），日志结论与源码分析可互相印证。

## 2. E-3 取证：window 通路未生效根因（S-7 开工门）

### 2.1 观察事实（全部日志代）

- 4 代日志（`go-bridge.log`+`.1/.2/.3`，跨度 **2026-10-04 16:16 → 2026-10-18 20:37**）中 `get_session_projection_window` RPC 计数 = **0**。
- 事故窗口（2026-10-17 17:30:56–17:33:52）：8× `get_session_projection sinceRev=0 outcome=snapshot`（req_18/33/54/58/67/77/82/91），connectionGeneration 1→5，与方案 §1.1 一致。
- 事故连接为 **LAN direct**（`client connected remote=192.168.1.2:62219` + direct `hello_ack sent`）；当前日志 29 个 hello_ack 全部 direct 路径、全部 ok=true。
- hello_ack 日志无 capability 字段（server.go:841 只记 ok/device/catalog_cursor_epoch_v2）；`SetConnProjectionWindowV1`（projection_delivery.go:231）无日志——Mac 日志无法判别「客户端声明与否」。

### 2.2 Mac 侧源码链（HEAD 6a76d92e，与安装版 Go 源码一致）

- `negotiateProjectionWindowV1`（server.go:370-395）：客户端 hello 声明 `projection_window_v1` + `session_sync_v2` 前置满足 且 `s.projectionWindowEnabled` 且 ack.Ok → **echo** `capabilities["projection_window_v1"]=true` 并注册连接（`SetConnProjectionWindowV1`）。
- 声明 window 而缺 ssv2 → hello 失败（protocol.invalid_capabilities，retryable=false）——**日志中无任何 ok=false hello_ack**，排除该形态。
- `main.go:560` `server.SetProjectionWindowEnabled(true)`（PERF-S4D 发布门，2026-08-26 ebab32f9）；`server.go:250` 的 `projectionWindowProductionEnabled=false` 常量是翻转前的死代码（非测试代码零引用）。
- hello_ack 的 `Capabilities map[string]bool` 在 `HandleHello` 构造时初始化（hello_handler.go:212），negotiate 注入后 Marshal 保真（自定义 MarshalJSON 透传）。

### 2.3 iOS 侧源码链（配对 worktree b77faf3c = main 同点）

- hello 声明：`CCCodeBridgeTransport.helloCapabilities(isRelay:advertisesRecovery:)`（:148-200）——`sessionSyncV2Enabled`（静态，`sessionSyncV2Mode` 默认 `.active`，**全仓无运行时改写点**）为 true 时追加 `session_sync_v2` + **`projection_window_v1`**（:168）+ turn_detail 两项。**单一构造器**（唯一调用点 :531 构造 hello :519），无第二 hello 路径。
- echo 消费：`supportsAcknowledgedCapability`（:723-725）= `isRegistered && acknowledgedCapabilities[capability] == true`；map 由 hello_ack 填充（:599 `ack.capabilities ?? [:]`），断连/重连清空。
- 打开路径：`ChatViewModel+SessionManagement.swift:851` `pullOpenWindowed`（sessionSyncV2Active = mode 允许 × backend-kind 允许表（claudeCode ✓）× `supportsSessionSyncV2`——该值来自**描述符合并数组**而非连接 echo map，与 window eligibility 读 echo map **不同源**，事故中 SSV2 激活正常即证明描述符链健康）。
- `pullOpenWindowed`（ProjectionStore.swift:763-777）：`guard await isProjectionWindowEligible()`（= fetcher.supportsProjectionWindow = echo map）失败 → **`pullFull`（get_session_projection sinceRev=0）**——与事故日志形状**精确一致**。
- `pullWindow`（:655+）window0 方向的 RPC 前退出路径仅 3 个：fetcher 类型不符 / echo 不满足 / in-flight pendingPull——任一满足都不会产生 window RPC。

### 2.4 descriptor 维度（F-13 偏离复核）

`/internal/agents` 实测 claude descriptor capabilities（29 项）**不含 `projection_window_v1`**（亦不含连接级 `session_sync_v2`）——协议文档 bridge-v1.md:2173-2175 的双门规范中 descriptor 侧从未广告，实现为 hello echo 单门。iOS 消费端只查 echo（CCCodeBridgeBackendClient.swift:1308-1310），**descriptor 缺失不是 iOS 走 full 的阻断点**；该偏离收口仍按方案归 S-3 协议冻结时二选一。

### 2.5 根因判定

**与全部观察一致的唯一分支：事故时（以及至今）iPhone 上安装的 iOS app 构建不含 2026-08-26 PERF-S4C（2a7c351f）的 `projection_window_v1` hello 声明（或更旧、无 windowed 打开路径）**。推理链：

1. 若安装 app 为当前家族构建（≥2a7c351f）：hello 必声明 window+ssv2（静态默认、无改写点、单构造器）→ Mac（echo 已开，2.2）必回显 → `pullOpenWindowed` 必发 window RPC → 日志应有 `get_session_projection_window`。**实际 4 代日志为 0**——矛盾。
2. 反之，安装 app 早于 2a7c351f：hello 不声明（或打开路径无 pullOpenWindowed）→ Mac 不 echo → eligibility=false → pullFull = 事故精确形状。**一致**。

注意：当前 iOS 主 checkout/配对 worktree（b77faf3c，含声明）与手机安装版是两回事；本机 DerivedData 有 10-17/10-18 的构建产物（可能为模拟器构建），无法据此推断手机安装身份。

### 2.6 残留待证项（不阻塞 S-5/S-8；S-7 开工前补强）

- **安装包身份实测**：USB 未连接（`pymobiledevice3 usbmux list` = 空），无法只读抓取 syslog（hello 声明）或 apps 列表（安装版本）。两个消解路径：
  1. owner 方便时将 iPhone USB 连接 Mac 一次（agent 只读取证：syslog hello 行 / apps list），或
  2. 随 S-4/S-6 交付安装新 iOS 包后，安装身份自然成为已知（新包必声明，预期 window/anchor RPC 出现即闭环验证）。
- Mac 侧与双端源码侧证据已闭合，根因判定不依赖该残留项的答案（两个候选答案都不改变 S-7 的实施内容：anchor-newer 走同一协商/handler 管道，源码层已证明健康）。

## 3. 事故样本现状（2026-10-18 20:3x 实测）

| 项 | 值 |
| --- | --- |
| transcript | `~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/bbe396e6-11ea-4754-943e-df5d2effcd14.jsonl`，**101,522,713 B**（方案快照 101,520,809 B +1,904 B，mtime 10-18 09:47——session 仍在活跃使用） |
| checkpoint | `…/session-projection/checkpoints/683c24fa….json`，34,537,988 B，mtime **10-17 17:30（事故时刻后未再更新）** |
| 日志保留 | 4 代覆盖 2026-10-04 → 2026-10-18；事故窗口完整在当前代（行 7670-8456 区间） |

## 4. 对切片的影响结论

- **S-5/S-8（第一阶段承重）**：不受 E-3 残留影响，即可开工。
- **S-7**：E-3 根因已判定（Mac 管道健康；根因在 iOS 安装身份）。按方案 Gate A，S-7 开工前建议完成 §2.6 残留项之一（优先路径 1：owner USB 连接一次供只读取证；或在第一阶段交付时顺带安装新 iOS 包验证 hello 声明→echo→window RPC 链路）。
- **S-6/验收**：安装新包后 `hello` 声明 → echo → anchor-newer RPC 首次出现，即为 E-3 闭环的运行态证据。
- E-1（iOS timeout 非 Mac hydrate failed）：方案已 verified（Mac 17:30:56 hydrate/enqueue 在先），本轮事故窗口日志复核一致。
- E-2（事故未走 window）：4 代日志零 window RPC，再确认。

## 5. 本记录的证据等级声明

- 运行身份/端口/进程/Management API/日志计数/文件 stat：**生产运行态实测**（本机 /Applications 正式版）。
- 双端源码链：**源码级亲核**（Mac HEAD 6a76d92e = 安装版 Go 源码；iOS 配对 worktree b77faf3c；行号见各节）。
- 根因判定：基于上述两级的**推理收口**（2.5 推理链），安装包身份一项未经设备实测（§2.6）。
