# 第二轮评审：OpenCode managed local server 无缝接入方案（修订版）

> 评审对象：[docs/2026-07-03-opencode-managed-local-server-seamless-plan.md](../2026-07-03-opencode-managed-local-server-seamless-plan.md)（修订版，685 行）
> 评审日期：2026-07-03
> 前序：[首轮评审](2026-07-03-opencode-managed-local-server-seamless-plan-review.md)
> 评审方法：核对"评审处理记录"逐条落地情况；并对修订**新引入**的承载性假设做与 B2 同等强度的反查。

## 0. 总体结论

**修订质量高，首轮 8 项全部得到实质性回应**——尤其是 B2，作者用本机 `opencode serve --help` + 真实 health 实测取代了纸面假设，并把"`--port` 默认 0、不是 4096 搜索"如实写回文档，这是这个仓库方案里少见的严谨。B3 选择"维持无缝目标 + 接受 Desktop graceful restart 代价"也是自洽的决策，不再有首轮的目标/范围矛盾。

**但修订本身引入了两个新的、对外部 App 行为的承载性假设，没有得到 B2 同等的实测验证，且都直接威胁 §14 的"无需手动"判据：**

| # | 问题 | 严重度 |
| --- | --- | --- |
| **R2-1** | graceful quit Desktop 用 AppleScript/NSWorkspace，但 MacBridge 无 entitlements、ad-hoc 签名、且当前零 Apple Event 用法 → 首次触发大概率弹 macOS Automation 授权框，是一个"手动步骤" | **阻断 §14** |
| **R2-3** | "Desktop 重启后会连到写入的 managedURL"是未证前提；若 Desktop 每次启动都自起 vlocal，整套方案在 Desktop 侧失效，且无 fallback | **阻断 §14** |

其余为 should-fix / minor，不阻断进入开发，但建议在动工前澄清。

> 重要原则：作者对 `opencode serve`（外部二进制）做到了"实测为真"。**对 OpenCode Desktop（外部 Electron App）的两个关键行为，需要补上同样的实测**，否则 B3 的修订只是把"未证假设"从 opencode serve 换成了 Desktop。

---

## 1. 首轮意见落地核对（全部确认）

| 首轮项 | 修订落地 | 评价 |
| --- | --- | --- |
| B1 文档可跟踪性 | 部分采纳：未改 `.gitignore`，但 CHANGELOG 已不再引用具体 gitignored 文件名（CHANGELOG:13 改为"产出本地 managed local server 开发规格"） | ✅ 合理。CHANGELOG↔未跟踪文件的引用断链已消除。剩余的 specs 归属留作 owner 决策，标注清晰。 |
| B2 CLI 假设未证 | 采纳 + 实测：新增"CLI 实测取证"节，固化 `--print-logs` 存在、`--port` 默认 `0`、Basic Auth 401/200 + `healthy/version` body、stderr `server listening on ...` | ✅ **金标**。这正是首轮期望的反查强度。 |
| B3 目标 vs 范围 | 采纳，维持无缝目标：§7.466-479 把 graceful quit + reopen 纳入开发范围，并显式接受"可能中断正在进行的 turn"的代价 | ✅ 自洽。但代价评估不完整，见 R2-1。 |
| S1 canonical 契约 | §3.180-194 新增 canonical 形式（`http://127.0.0.1:<port>`、无 slash、IPv4），列出 7 个必须一致的 surface，并写明 Go 侧 `TrimRight` 与 Swift 写入不一致会静默 fallback | ✅ 到位。§9/§10 补了 trailing-slash 测试。 |
| S2 可测 seam | §6.330-339 列出 `CLIResolver/PortProber/HealthProbe/ProcessFactory/DesktopProcessController/evaluateReady` | ✅ 到位。`DesktopProcessController` 是修订新加的，正好支撑 R2-1 的实测。 |
| S3 OpenCode 失败不拖累 Claude/Codex | §3.153 + §7.429-433 启动不变量 | ✅ 到位。 |
| S4 orphan pid 策略 | §6.385-395 五步处置（pid+命令行+health+auth 校验，收养/清理/换端口） | ✅ 到位，权衡清晰。 |
| S5 持久化文件归属 | §6.308-312 固定独立 `opencode-managed-server.json`，原子写、0600，并给出与 `credentials.json` 的语义区分理由 | ✅ 到位。 |

**这一节无需再争论。** 下面只谈修订引入的新问题与少数残留缺口。

---

## 2. R2-1（阻断 §14）：graceful quit 的 macOS 权限代价未被评估

### 问题

§7.472：

> graceful quit 优先用 AppleScript / NSWorkspace 请求正常退出，等待退出确认后再 `open /Applications/OpenCode.app`...

§7.478 只把代价定义为"可能中断用户正在进行的 OpenCode Desktop turn"。但实际还有一层代价没评估——**这个 quit 动作本身可能触发 macOS 的自动化授权（Automation TCC），那是一个手动步骤**。

证据：

1. **MacBridge 无任何 entitlements**：仓库内不存在 `*.entitlements` 文件，`project.yml` 无 `sign/entitlement/sandbox/hardened` 配置；分发走 `scripts/build-unsigned-release.sh`（ad-hoc 签名）。首轮已引用 CHANGELOG 2026-06-19 记录的"ad-hoc 签名导致钥匙串授权反复弹窗"问题。
2. **MacBridge 当前零 Apple Event 用法**：全仓 grep 仅 `NSWorkspace.shared.notificationCenter`（sleep/wake 通知，不走 Apple Events），无 `osascript`、无 `NSAppleScript`、无 `NSRunningApplication.requestTermination`、无 `quit app`。即 graceful quit 是**全新机制，没有任何现成的授权基建**。
3. macOS 对跨 App 发 Apple Event 要求发送方拥有 Automation 权限。对 **ad-hoc 签名 / 身份不稳定**的 App，首次发送会弹 `"CordCode Link" 想要控制 "OpenCode.app"`，且因为签名身份不稳定，**重建后可能再弹一次**——这与 2026-06-19 钥匙串问题是同一类病根。

### 为什么这是阻断

§14.678 写：

> 无需手动输入端口/命令/凭据、**无需手动重启 OpenCode Desktop**

而 §1.122 也把"手动重启 Desktop"列为用户不该做的事。但若实现走 `osascript -e 'quit app "OpenCode"'`，干净新用户**首次**触发自动切换时，macOS 会要求其手动到"系统设置 → 隐私与安全 → 自动化"授权——这本身就是 §14 禁止的"手动步骤"，且比"手动重启 Desktop"更隐蔽、更难排查。

### 建议（与 B2 同形：先实测，再选实现）

在 §7 增加一节"Desktop quit 机制实测取证"，**开发前**用本机实测确认以下三种路径哪一种不触发 TCC 弹窗：

```bash
# 路径 A：osascript Apple Event（大概率触发 Automation 授权）
osascript -e 'tell application "OpenCode" to quit'

# 路径 B：NSRunningApplication.requestTermination(_:)（Cocoa 路径，授权特征不同）

# 路径 C：直接 NSWorkspace 共享打开配置 + 依赖 Desktop 自身重启判定
```

把实测结果写进文档，并据此三选一：

- **若 A/B 都触发 TCC**：则 §1/§14 必须**显式增加**"首次需授予一次自动化权限"作为已知手动步骤，并诚实下调"无缝"措辞；或改用不触发 TCC 的方案（如只写配置 + 提示，回到首轮的收紧目标选项，但那样 B3 决策要重做）。
- **若 B（NSRunningApplication）不触发**：则 §7.472 应**指定只用 NSRunningApplication.requestTermination，禁用 osascript**，并写明理由。当前"AppleScript / NSWorkspace"并列为实现者埋了选错路径的坑。
- 无论选哪条，加一条 §13 reviewer 检查："Desktop 自动 quit 路径不得引入 macOS TCC 授权框"。

---

## 3. R2-3（阻断 §14）：Desktop 重启后是否真的连 managedURL，是未证前提

### 问题

整套 B3 修复依赖一个**没有实测、也没有 Phase A 证据**的前提：OpenCode Desktop 在（重新）启动时会读取 CordCode 写入的 `opencode.global.dat`（`currentSidecarUrl` / `defaultServerUrl` / `projects[managedURL]`）并连过去，**而不是每次启动都自起一个新的 `vlocal` sidecar**。

§0.78 的 Phase A 结论只证明了"三者连同一个 server 时能对齐"——但 Phase A 是**用户手动起 opencode serve + Desktop 配置已就位**的场景，并没有证明"Desktop 冷启动时会服从写入的 currentSidecarUrl 而非自起 vlocal"。OpenCode Desktop 自带 `vlocal` sidecar（§0.94），它的启动逻辑完全可能是"每次启动都生成新的 vlocal"，把 `currentSidecarUrl` 当历史记录而非启动指令。**如果是后者，CordCode 写配置 + 重启 Desktop 之后，Desktop 仍然连到一个新的随机端口 vlocal，iOS 连的是 managedURL，scope 再次分裂——而且这次 CordCode 还主动重启了用户的 Desktop，代价已经付了，目标却没达成。**

### 为什么这是阻断

这是 §10.8 / §14 第二条验收路径（"Desktop 已运行"分支）能否通过的决定性变量，但方案没有给它单独的实测节，也没有失败 fallback（§11 失败表里"Desktop 正在运行但未重读配置"直接假设 graceful restart 能解决，等于把未证前提当成了已证结论）。

### 建议

把 R2-3 当作 B2 的姊妹项处理：

1. **开发前实测**（owner 本机，5 分钟）：手动编辑 `opencode.global.dat` 把 `currentSidecarUrl` / `defaultServerUrl` 改成一个外部 `opencode serve` URL（即 Phase A 的 endpoint），**完全冷启动** OpenCode Desktop，然后看 Desktop 实际连接的是写入的 URL 还是新 vlocal（可用 `lsof -nP -iTCP -sTCP:LISTEN | grep opencode` + Desktop 自身日志/网络面板确认）。
2. 把结论写进文档新增的"Desktop 启动行为实测"节。
3. **若 Desktop 不服从写入的 URL**：B3 的"自动 quit + reopen"无效，需要回到首轮的"收紧目标"或寻找别的让 Desktop 切换的机制（如 Desktop 命令行参数、环境变量）。这是 B3 决策能否成立的前置，**应排在 §12 step 0 的实测里，而不是留到 §10.8 验收时才暴露**——否则可能开发完成后才发现核心路径走不通。
4. §13 增加 reviewer 检查："Desktop 冷启动连接目标已被实测确认服从 currentSidecarUrl"。

---

## 4. Should-fix（建议动工前澄清）

### R2-2 Desktop "当前连接" 检测是启发式，未写明

§7.470-471 用"已运行且已连 managedURL / 已运行但连 vlocal"做分支，但**没说怎么知道 Desktop 当前连的是哪个**。读 `opencode.global.dat` 的 `currentSidecarUrl` 是"持久化意图"，不是"运行态事实"——而且 CordCode 在 quit 前往往已经写过配置，此时文件已被改成 managedURL，不能反映 Desktop 进程的实际连接。

实际可行的只是启发式："Desktop 进程在跑 +（写配置**之前**读到的）`previousURL`（`RuntimeManager.swift:752` 已有）≠ managedURL → 需要重启"。方案应：

- 明确"检测发生在写配置**之前**，复用现有 `previousURL`"。
- 承认这是启发式：可能误判（Desktop 实际已在 managedURL 但 `previousURL` 旧）→ 多余重启一次；也可能漏判。两种误判都应在 §11 失败表里有一行。

### R2-4 managed server restart ↔ bridge restart 互触发循环未禁止

§3.154 / §7.433 说"managed server restart 与 bridge restart 必须接入同一套 generation 收敛"。方向对，但 `OpenCodeManagedServer` 是独立 Swift 服务，共享 `RuntimeManager.launchGeneration` 的具体方式没写。风险是**互触发**：managed server 重启 → 触发 bridge restart → bridge restart 又调 managed server ensureReady → 再次 restart。应加一条不变量：

> managed server 重启不得直接触发 bridge restart；bridge restart 只在 config 变更时发生，config 变更同时调度"managed server ensure + bridge restart"为同一 generation 的两个子步骤，顺序固定，不互相反向调用。

### R2-6 managed server 日志没有滚动策略（确认与 bridge log 是两套）

§3.144 / §6.357-360 要求 `logs/opencode-managed-server.log` 落盘前脱敏。但实测 `RuntimeManager.swift:532-541` 的 `rotateLogFileIfNeeded`（8 MiB × 3 代）只作用于 bridge 自己经 `logFileHandle` 写的 `go-bridge.log`，**不会覆盖** managed server 那个由 `Process.standardError` 重定向产生的独立文件。`opencode serve --print-logs` 长跑会无限增长。

建议：§6 显式要求 `OpenCodeManagedServer` 实现自己的 size-cap 滚动（复用同样的 `maxLogBytes` 阈值即可），或显式声明"managed server 日志按 N MiB 滚动、保留 M 代"，并在 §10 补一条权限/滚动测试。否则只是又一个会撑爆磁盘的子进程日志。

---

## 5. Minor（实现阶段顺手处理）

- **R2-5 canonical 单一来源**：§3.193 列了 7 个 surface 必须一致，但 `configureOpenCodeDesktopSettings(serverURL:)` 当前**内部不做归一化**（`RuntimeManager.swift:730-735` 直接用入参），靠每个调用方各自传规范形式。建议方案显式写"managedURL 在 `OpenCodeManagedServer` 内 `normalizeLoopbackURL` 一次产出一个 `let`，所有 7 个 surface 复用同一个 `String` 值，禁止各自重新拼接"——比"必须一致"的口号更可执行。
- **R2-7 service_discovery_future 迁移措辞**：§5.251 给了"提示改选**或**下次保存时迁到 managed_local"两个选项，应二选一。对一个曾显式选未来 daemon 路线的高级用户，"下次保存时静默迁走"是意外行为；建议只保留"提示改选"，不替用户改写显式选择。
- **R2-8 改默认值必须同步改一条既有测试**：§5.249 把新装默认从 `disabled` 改成 `managed_local`，会与现有 `OpenCodeEndpointResolverTests.swift:162 testMigrationFreshInstallDefaultsDisabled`（当前断言 `disabled`）冲突。§10 应显式列出"**更新**该测试为 `managed_local`"，而不是只说"新增 managed_local 测试"——否则实现者可能同时留下两条相互矛盾的断言。
- **R2-9 ready 判定的"未立即退出"阈值**：§6.379 "Process 已启动且未立即退出" 缺具体阈值。`opencode serve` bind 端口需百毫秒级，建议写明"survive ≥ 1s 且 health 通过才 ready；1s 内退出记为 crashed 计入连续失败计数"，避免实现者把阈值设得太短导致冷启动误判崩溃。
- **R2-10 `--print-logs` stderr 双重定向**：§6.357-359 列了 `.log` 和 `.err.log` 两个文件，但 `--print-logs` 只输出到 stderr（实测节 §CLI.73 确认）。stdout 几乎为空，`.log` 会是空文件。建议合并为一个 `opencode-managed-server.err.log`，或在文档里说明 stdout 文件预期为空、仅保留以备将来 opencode 改变行为。

---

## 6. 对 §13 Reviewer 检查点的补充建议

修订版从 8 条扩到 13 条，新增的 5 条（canonical 一致、seam 单测、Claude/Codex 不被拖累、Desktop 已运行分支验收、orphan 处置）都准确。基于本轮再补 3 条：

14. **Desktop 自动 quit 路径是否触发 macOS Automation 授权框**（对应 R2-1）。
15. **Desktop 冷启动连接目标是否经实测确认服从 `currentSidecarUrl`**（对应 R2-3）。
16. **managed server restart 与 bridge restart 是否存在互触发循环**（对应 R2-4）。

---

## 7. 修订后的 §12 实现顺序建议

把"两个实测"插到现有 step 0 旁边，作为进入编码的硬前置：

```text
0. CLI 实测取证（已有）+ Desktop 两项实测（新增）：
   0a. opencode --version 变化 → 重跑 serve --help / health（已有）。
   0b. Desktop quit 机制是否触发 TCC（R2-1）。
   0c. Desktop 冷启动是否服从 currentSidecarUrl（R2-3）。
   0b/0c 任一不通过 → 回到 §1/§14 措辞修订或方案重设计，再进入 step 1。
```

这与首轮 D2 的精神一致：**外部二进制 / 外部 App 的承载性行为，先证后用**。

---

## 8. 二轮评审结论

**首轮问题已实质性清零；修订质量在这个仓库的方案族里属于上乘。**

但 B3 的修订引入了两条新的承载性假设（Desktop quit 的 TCC 代价、Desktop 重启后服从 managedURL），它们的"未证"程度与首轮 B2（`--print-logs`、4096）相当，而 B2 已经被实测清除——**这两条也应得到同等强度的实测后再进入开发**。否则风险是把首轮"opencode serve 的纸面假设"换成"OpenCode Desktop 的纸面假设"，问题平移而不是消除。

**建议状态：**

- 关掉 R2-1、R2-3（各做一次 5 分钟级本机实测，写入文档）后，**可以进入开发**。
- R2-2 / R2-4 / R2-6 建议在写对应代码前补进方案。
- R2-5 / R2-7 / R2-8 / R2-9 / R2-10 可在实现阶段顺手处理。

如果 0b/0c 的实测结果不理想（TCC 必弹 / Desktop 不服从配置），则需要回到 §1 与 §14 重新协调"无缝"的边界——这属于产品决策，应当由 owner 拍板，而不是由实现者写到一半才发现。
