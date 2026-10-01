# 2026-10-01 dsh-web：探活请求失败误收口修复

## 0. 交付范围与来源门

修复提交 **8e5205bd05e3698708169d72f4232d4cd9db6999**，已构建 Release 并安装/重启 MacBridge。19 个定向测试（含 10 个请求失败子场景）在 race detector 下通过。组件级错误分类和真实测试 listener 死亡已验证；生产态仅验证新进程、新策略启用、原 dsh 实例接回与产品后端齐全。owner 长回复视觉效果尚待回归，不能把组件测试称为真机复现已消除。

任务预期双仓均为 `feat/ios-native-message-timeline`，本轮只改 dsh-web driver 私有生命周期判定，不改 wire schema、protocol pack、iOS 终态映射或滚动算法。产品启用后端为 `claude,codex-remote,grokbuild,dsh-web,opencode-web`；保留现有 native timeline iOS 包。配对依据为本任务 native timeline 工作树及已有交付文档，重新枚举并核对，不以相邻默认 main 代替。

| 门点 | iOS | MacBridge |
| --- | --- | --- |
| 源码分析/首次测试文件编辑 | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` · `feat/ios-native-message-timeline` · `60ce57fa88ed021027bc476b2e42f08c54ff11be`，以下 12 项未跟踪 | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` · `feat/ios-native-message-timeline` · `1b02106869a2d7d7a84d6577ec527696f1d86825`，干净 |
| 生产代码首次编辑 | 同上，12 项未跟踪 | 同路径/分支/1b021068全哈希，仅新增未跟踪 `agent/dsh-web/resolver_probe_failure_test.go` |
| 构建前/安装前/部署复核/本报告写入前 | 同路径/分支/60ce57fa全哈希，12 项未跟踪 | 同路径/分支 · `8e5205bd05e3698708169d72f4232d4cd9db6999`，干净 |

iOS 原有未提交路径逐项保留：

```text
?? .exec-plan/state/plan-80132345a4bf.json
?? .exec-plan/state/plan-9c3627eb04ec.json
?? OpenCodeiOS/Packages/MarkdownView/.build/
?? OpenCodeiOS/Packages/MarkdownView/Package.resolved
?? docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r2-meta.md
?? docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r2.md
?? docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r3-meta.md
?? docs/2026-09-30-native-timeline-fence-tick-cost-plan-review-r3.md
?? docs/2026-10-01-opencode-web-v2-event-stream-migration-plan-review-r2.md
?? docs/2026-10-01-opencode-web-v2-event-stream-migration-plan-review.md
?? scripts/__pycache__/
?? scripts/e3/__pycache__/
```

Mac 本轮拥有的改动：`agent/dsh-web/resolver.go`、`agent/dsh-web/dshweb.go`、`agent/dsh-web/turn_terminal_test.go`（仅注释）、`agent/dsh-web/resolver_probe_failure_test.go`、`CHANGELOG.md`。后续本报告与 `think.md` 为文档提交，不改变已部署代码身份。未改其他代理文件、集成 main、清理缓存或重启 dsh。

官方只读仓 `/Users/jacklee/Projects/deepseek-harness`，分支 `master`，HEAD `639ed015397290b3745d163aafe02ffee4aa3f84`，干净；实际对照已安装 npm `@deepseek-ai/dsh` **0.1.7-rc.2** 的 tag `dsh-v0.1.7-rc.2`，完整 commit **477b4f420553e8a52c2fbccc464d7561b239c443**，不把较新 HEAD 当目标版本。

## 1. 为什么修在 Mac，而非 iOS

原件调查见配套 iOS 的 `docs/2026-10-01-native-timeline-measure-reuse-physical-and-dsh-terminal-followup.md`（调查提交 `78a711ddc9d82af540a7351593fede64d53b50e4`）。15:45:06 某次 held `session/list` probe 失败，被一律转换成 seat loss 和 running-session terminal；真实正文仍继续约 16 秒。iOS 正确消费了这个错误终态，停止 pacer，随后 114 次整表派生/提交全部超过 33.34ms，出现坐标回退。历史日志没有保存原始 probe 错误，不能认定当时究竟是取消、超时、业务、认证还是其他错误。本修复切断已复现的误判路径，不补造历史错误类型，不宣称所有卡帧都由此造成。

目标 tag 的 `packages/api/session-controller/src/list.ts:125` 读取可见会话，取消/存储错误不拥有 turn 终止语义；`packages/api/gateway/src/index.ts:345` 的 `invoke`/`invokePrepared` 保留业务错误和取消，`packages/api/gateway/tests/gateway.host.spec.ts:1033` 起覆盖 `ok:false` 与取消分类。仅读取这些官方源码和测试，未执行上游测试套件。CordCode 的座位/宽限期为私有生命周期规则，保留原有真实失联终态、宽限期不跨端口收养、不启动，以及到期重启契约。

## 2. 行为与边界

- held `session/list` 业务 RPC 错误、HTTP 401/403/503、坏响应、caller cancel/deadline、探活超时、读 EOF/reset：返回原请求错误；不清 held identity、不增加 lossSeq、不进入 grace、不发 turn terminal。失败没有被重试、吞掉或显示为成功；保持已持有身份不是证明该次 RPC 成功。
- 只有 caller 未取消、`carrierError.Status == 0`、底层 `net.OpError.Op == "dial"` 且 `ECONNREFUSED`，才具备进入原有 seat-loss 的证据。超时/半断链不据此宣布实例死亡；本轮没有新增黑洞网络/进程代际检测。真实 listener 关闭后的连接拒绝仍只向 known-running session 发一次终态。
- 网络操作在锁外，失联前锁内复核 held 指针。同一 held 实例并发拒绝只形成一次 loss edge；迟到的失败不清除后来 rebound 的身份。移除 held 分支将 negative-cache 时间本身当失联证据的逻辑。
- 启动日志 `held probe loss policy policy=dial-refused-only requestFailuresTerminateTurn=false` 证明新策略启用。失败记录 operation/category/HTTP status/RPC code/context/transport op/errno；不写响应正文、RPC message/details 或 cookie。stream 1006 的既有重连路径保持，不能仅据单次流断开宣告 turn 结束。

未在正式路径引入 mock、fallback、假 running、延时收口或更长重试窗口；HTTP/transport 测试夹具仅位于 Go test 文件。

## 3. 测试证据与成本

先对未改生产源码跑新断言：10 个请求失败场景全部复现错误终态转换，并发探测得到 2 个 loss edges。首次超时夹具在 handler/cleanup 等待挂住，45s watchdog 终止；修正为原子开关和显式清理通道后，正确基线 wall **1.47s**、body **0.131s**，11 项全部按预期红。挂起不是生产修复失败，保留两份日志。

最终一次 `go test -race ./agent/dsh-web -run <19项精确选择器> -count=1 -v -timeout=70s`：**19 个顶层测试通过**，其中请求失败表 10 个子场景；body **6.575s**，wall **17.02s**。覆盖原错误保留/无假终态、真实 listener 死亡/一次回调/一次 terminal、宽限期恢复/到期启动、stream 断开重连、并发拒绝、迟到失败、negative-cache 不制造失联、cold resolve、启动 single-flight、readiness/typed error 与 caller deadline。完整选择器见私有 `final-targeted-race.log` 的测试名；没有全套/UI/snapshot 测试。

使用已安装 `/Users/jacklee/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.6.darwin-arm64/bin/go`，`GOTOOLCHAIN=local`；不下载或更改全局 toolchain 配置。Release 使用与 `scripts/build-unsigned-release.sh` 相同构建/签名参数，但直接执行 xcodebuild，以避免脚本无条件删除 DerivedData。固定复用 `build/unsigned-release`，增量编译 **12.78s**；未运行 clean/npm/iOS build。

## 4. 安装和生产运行身份

- 新产物 `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/build/unsigned-release/Build/Products/Release/CordCodeLink.app`；bundle ID `org.openagi.cordcode.link`、arm64；codesign deep/strict 通过，无 get-task-allow；Swift/runtime 架构一致。
- runtime `-version`：`cordcode-bridge-runtime 0.1.0 (commit: 8e5205bd05e3, built: 2026-10-01T08:28:04Z)`。
- runtime SHA-256：`865ca0b01827827277ee16ccd4484a07c939fa8d4b126144a3a0bbb77ba8372c`；Swift executable SHA-256：`66042d36e842dcb0d1fe244cdabe7acbf1b40d4562407516448a36f4a7b2353e`。安装后二次 SHA/版本一致。
- 安装前 Management API：0 个 bridge-owned active turns、0 pending interactions、healthy。正常退出 App 后确认旧 runtime PID **5507** 退出、8777 释放；旧包移入私有目录备份，再 ditto 新包并打开 App。没有全局 pkill/killall 或清空应用数据。
- 新 runtime PID **49686**，启动 **2026-10-01 16:30:07 +0800**，实际路径 `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`；8777 监听归属该 PID。生产日志 16:30:07.766 出现本次独有 policy 行，16:30:08.135 在原 3080 seat adopt 成功。原 dsh PID **47836** 保持，无 dsh 重启。五个启用后端均 present，最终只读描述符全部 available。
- 首轮安装后断言误把已于 2026-09-04 退役的 codex-web 要求为启用后端，导致核验脚本中止。没有错工作树、缺产品代码或再次安装；对照 `RuntimeManager.swift:200–206`、实际进程 `-drivers`、runtime.json 与描述符后修正为现有五后端。初始错误断言与修正证据均保留。不能恢复退役后端来满足错误清单。
- 安装/启动独立 stopwatch 未保存，不能给出可靠单独 wall 值；安装前状态记录16:30:02，新进程16:30:07启动，后续约两分钟包含核验清单修正，不包装成编译或安装耗时。运行态复核完成16:32:34。

本轮没有 iOS 生产源码修改，iPhone 保持 **1ceb3269cdfea5dd8eaf3df00001b295c665eb9b** 的已有 native timeline 包/采样设置，不做无关重装。Mac runtime 重启会令 iOS transport 重连；需要 owner 的真实长回复验收，不运行 UI automation。

## 5. Owner 回归与后续

| # | 前提条件（网络、模式、session 状态） | 动作 | 应看到 |
| --- | --- | --- | --- |
| 1 | 原有网络，DeepSeek Harness，MacBridge 已更新 | iPhone 打开 App，等待重连，发送一条长回复 | 正文持续完整，跟随正常，不再出现请求失败后提前收口引起的上下跳动 |
| 2 | 同模式，第二条长回复正在流式输出 | 回到会话列表再进入同一会话 | 正文继续，状态与实际执行一致；列表请求失败时不把仍输出的 turn 宣告实例死亡 |
| 3 | 回复自然结束 | 观察末尾，再切后台一次 | 内容/高度/行号正确，无大片空白，收尾稳定；后台导出供 agent 复算 |

只回报步骤号及 ✅/❌＋现象即可，不需要手工日志。不要人为关闭真实 dsh 来补已有 listener-death 单测。真实错误终态之外的普通收尾 285ms 未覆盖样本继续单列；新一轮日志才能评估剩余卡帧与本次收益。

证据目录 `/tmp/dsh-seat-terminal-fix/`：analysis/edit/production-edit/build/install/verified-deployment/documentation 来源门、两轮基线、最终 race 日志、Release 命令/日志/身份、生产策略行、安装前后 API/PID/listener 与核验清单修正。原包备份和 token-bearing 原件仅私有保留，不提交 token、真实 session 文本或路由身份。
