# codex-web backend 退役完成情况（2026-09-04）

## 决策记录

Owner 2026-09-04 指令：将 codex web backend 从 iOS 端和 MacBridge 端移除——
不再出现、不再以 runtime 挂载运行；`agent/codex-web` 本身不删，代码保留。

这是继 `opencode`（2026-08-19）、`codex`（2026-08-25）之后的第三次 lineup 退役，
模式完全一致：**源码保留、产品面移除、回滚 = 把 id 加回 drivers 列表**。
Codex 产品面此后由 `codex-remote`（Codex Desktop / Remote Control）独立承接。

## 来源清单（P0 门）

编辑与验证全程使用以下来源组合：

```text
MacBridge 仓库路径=/Users/jacklee/Projects/cordcode-macbridge
MacBridge 分支=main  提交=bbbdbb78df3bb8240a2279e92c2e17a7ff7ab161
MacBridge 未提交状态=本轮退役修改（见下表）+ 未跟踪 docs/2026-09-03-plan-mode-cross-backend-survey.md（用户在写，未触碰）
iOS 仓库路径=/Users/jacklee/Projects/cordcode-ios
iOS 分支=main  提交=aeba9111164a021f2df38cf3f831335a57baa1d1
iOS 未提交状态=本轮退役修改（见下表）
预期产品特性=runtime agents 恰为 claude/codex-remote/grokbuild/dsh-web/opencode-web 五个，无 codex-web
```

两仓各只有一个工作树（`git worktree list` 核对），任务未指定功能分支，main 配对唯一。

## 改动清单

| 仓库 | 文件 | 改动 |
| --- | --- | --- |
| MacBridge | `MacBridge/MacBridge/Services/RuntimeManager.swift` | 默认 drivers 移除 `codex-web`；注释记录裁决、daemon seat 联动与回滚方式 |
| MacBridge | `MacBridge/MacBridgeTests/MacBridgeBehaviorTests.swift` | drivers 断言翻转为 `XCTAssertFalse(contains("codex-web"))`；显式传 `drivers: ["codex-web"]` 的 daemon seat 单测保留（测的是保留代码） |
| MacBridge | `CLAUDE.md` | What this repo is lineup、Backend runtime model 表（Codex Web 行移除）、legacy 清单加 codex-web 条目、legacy codex 节注记 |
| MacBridge | `GO_BRIDGE_ARCHITECTURE.md` | 双真值段（2026-09-04 校正）、Codex 节定位注记、Codex Web 节标题改「产品 lineup 已退役 2026-09-04」+ 裁决注记、SSV2 外部 turn 家族列表 |
| MacBridge | `BUILD_INSTALL_AND_RUNTIME.md` | 默认注册段、`-drivers` 表产品值 |
| MacBridge | `CHANGELOG.md` | `[Unreleased]` 追加移除条目 |
| iOS | `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift` | `serverCreationCases` 移除 `.codexWeb`；`isDeprecated` 加入 `.codexWeb`（枚举 case 与 wire 映射保留） |
| iOS | `OpenCodeiOS/OpenCodeiOSTests/BridgeTransportTests.swift`（类名 `BridgeModelsTests`） | serverCreationCases / isDeprecated 断言翻转 |
| iOS | `CLAUDE.md` | backend 演进补注（2026-08-31）加退役更新 |

## 设计要点

1. **对齐退役先例**：与 `codex`（commit 0a85ad5）同款最小改动面——产品门只有
   RuntimeManager drivers 列表一处；go-bridge `defaultDrivers`（standalone 调试用）
   按先例不动（`codex` 退役时也未动它）。
2. **共享 daemon seat 自动失效**：`configureCodexDesktopSharedRuntime`
   （RuntimeManager.swift）以 `drivers.contains("codex-web")` 为门，移除后每次
   launch 自动 `.skipped`——不再代启动官方 daemon、不再写 launchd
   `CODEX_APP_SERVER_USE_LOCAL_DAEMON`。该 seat 本是让 codex-web 旁观 Desktop
   turn 用的，无 codex-web 即无消费者。
3. **codex-remote 不受影响的证据**：`agent/codex-remote/codexremote.go` 文件头
   明确声明 "does not import ... the shared-daemon Codex Web backend"；Remote
   Control 链路独立于共享 daemon。运行态复核：退役后 `/internal/agents` 中
   codex-remote `available`（controller protocol 3 environment stream bound）。
4. **iOS 保留枚举与解码路径**：codexWeb 与 codexRemote 深度共享
   ChatViewModel+CodexStreaming / MessageWeb 机制，深删会伤 Codex Desktop；且
   枚举保留保证已保存的 Codex Web 服务器 Codable 兼容（显示、标记不可用），
   与 `codex` 退役同款处理。runtime 不再下发该 backend，iOS 侧行为分支自然 inert。
5. **UI 残留代码保留**：WorkspaceView 的 codex-web 行内按钮/横幅、
   ManagementV1Codec 的 activity 计数、RuntimeManager 的 daemon 重启逻辑均以
   `agent.kind == "codex-web"` / `drivers.contains("codex-web")` 为键，backend
   不挂载即不渲染/不触发。未删是退役模式的一部分，不是漏删。

## 回滚步骤

1. MacBridge：`RuntimeManager.swift` 默认 drivers 加回 `"codex-web"`；
   `MacBridgeBehaviorTests` 对应断言翻转回 True；重新 Release 构建覆盖安装。
2. iOS：`BackendModels.swift` `serverCreationCases` 加回 `.codexWeb`、
   `isDeprecated` 移除该 case；`BridgeModelsTests` 断言翻转回。
3. daemon seat 随 drivers 加回自动恢复（无需单独改）。

## 验证证据

| 项 | 结果 | 耗时 |
| --- | --- | --- |
| Mac 定向单测 `MacBridgeBehaviorTests` | 30/30 通过（含翻转断言） | ~30s |
| iOS 定向单测 `BridgeModelsTests`（iPhone 17 Pro Max 模拟器） | 54/54 通过 | ~47s |
| Release 构建（`build-unsigned-release.sh`） | 成功，runtime commit bbbdbb78df3b | ~1min（增量） |
| 覆盖安装 `/Applications` + `killall CordCodeLink` + `pkill -f cordcode-bridge-runtime` + open | 完成 | — |
| 运行态：进程路径 | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`（无临时产物进程） | — |
| 运行态：进程代际 | PID 18931 启动于 00:27:13，晚于本次构建；8777 由其监听 | — |
| 运行态：codex-web 缺席的四重证据 | argv `-drivers`、`runtime.json` drivers、`/internal/agents`（恰 5 个 backend 全 available）、启动日志 | — |

iOS 侧未做真机安装（改动为入口移除 + deprecated 标记，单测 + 模拟器构建已覆盖）。

## 遗留观察点（预期行为，不是 bug）

1. **启动日志 WARN**：`go-bridge: codex-web agent not present; topology bridge
   dimension stays unresolved`——topology monitor（main.go）的既有优雅降级路径，
   能力推导型，每次启动出现一次。勿据此排查「runtime 异常」。
2. **日志中偶见 "codex-web" 字样的 catalog 行**：那是旧工作树**目录名**
   （如 `cordcode-ios-codex-web-backend`）出现在 workspace filter 的
   dropped_basenames 里，与 backend 无关。
3. **go-bridge standalone fallback**：`go-bridge/main.go` `defaultDrivers` 仍含
   `codex-web`——仅无 Swift 传参的手工调试场景生效，产品态永远由
   RuntimeManager 显式传 `-drivers` 覆盖（先例如此）。
